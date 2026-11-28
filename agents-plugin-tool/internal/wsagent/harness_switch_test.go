package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscompact"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

type switchTestBackend struct {
	wsCompactBackend
	emitSpawned   bool
	pluginWarning string
	onStopError   func()
}

type switchStopHookProc struct {
	*wsCompactProc
	onStopError func()
}

func (p *switchStopHookProc) StopError() error {
	if p.onStopError != nil {
		p.onStopError()
	}
	return p.wsCompactProc.StopError()
}

type admittingSwitchBackend struct {
	*switchTestBackend
	admitFn func(provider, model string) error
}

func (b *admittingSwitchBackend) Executable() string { return "" }
func (b *admittingSwitchBackend) Choices(context.Context) ([]ModelChoice, []string, error) {
	return nil, nil, nil
}
func (b *admittingSwitchBackend) leadProviders() ([]string, string) { return []string{"ok-prov"}, "" }
func (b *admittingSwitchBackend) leadCredentialGap() string         { return "" }
func (b *admittingSwitchBackend) admitLeadProvider(provider, model string) error {
	if b.admitFn != nil {
		return b.admitFn(provider, model)
	}
	return nil
}

func (b *switchTestBackend) Start(ctx context.Context, spec StartSpec) (Process, error) {
	b.mu.Lock()
	warn := b.pluginWarning
	emitSpawned := b.emitSpawned
	failAt := b.failStartAt
	onStopError := b.onStopError
	idx := len(b.starts) + 1
	b.mu.Unlock()
	if warn != "" && spec.Lead != nil && spec.Lead.PluginWarning != nil {
		spec.Lead.PluginWarning(warn)
	}
	if emitSpawned && spec.Lead != nil && spec.Lead.Spawned != nil {
		spec.Lead.Spawned(fmt.Sprintf("early-holder-%d", idx), 5000+idx)
	}
	if failAt > 0 && idx == failAt {
		return nil, errors.New("simulated target start failure")
	}
	proc, err := b.wsCompactBackend.Start(ctx, spec)
	if err != nil || onStopError == nil {
		return proc, err
	}
	return &switchStopHookProc{
		wsCompactProc: proc.(*wsCompactProc),
		onStopError:   onStopError,
	}, nil
}

type fakeCompanionLeadKeys struct {
	fakeLeadKeys
}

func (k *fakeCompanionLeadKeys) MintCompanionLead(root, role string) (string, error) {
	return k.MintLead(root)
}

func (k *fakeCompanionLeadKeys) EnsureCompanionLead(root, key, role string) error {
	return k.EnsureLead(root, key)
}

type switchTestEnv struct {
	t             *testing.T
	dir           string
	root          string
	now           time.Time
	nowMu         sync.Mutex
	timers        *leverTimers
	claude        *switchTestBackend
	codex         *switchTestBackend
	agy           *switchTestBackend
	werk          *quotaBackend
	runner        *Runner
	restoreCalls  []string
	restoreMu     sync.Mutex
	trustedFn     func(*http.Request) (bool, bool)
	quotaResetAt  func(backend, model string) int64
}

func newSwitchTestEnv(t *testing.T, customize func(regs map[string]BackendRegistration)) *switchTestEnv {
	t.Helper()
	dir := t.TempDir()
	root := t.TempDir()
	cfgOpts := wsconfig.Options{RepoRoot: root, CacheHome: t.TempDir(), ConfigHome: t.TempDir()}

	env := &switchTestEnv{
		t:      t,
		dir:    dir,
		root:   root,
		now:    time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC),
		timers: &leverTimers{},
		claude: &switchTestBackend{},
		codex:  &switchTestBackend{},
		agy:    &switchTestBackend{},
		werk:   &quotaBackend{},
		trustedFn: func(req *http.Request) (bool, bool) {
			return true, true
		},
	}

	builtins := BuiltinBackendRegistrations()
	claudeReg := builtins[BackendClaude]
	claudeReg.Backend = env.claude
	codexReg := builtins[BackendCodex]
	codexReg.Backend = env.codex
	agyReg := builtins[BackendAgy]
	agyReg.Backend = env.agy
	werkReg := builtins[BackendWerk]
	werkReg.Backend = env.werk

	regs := map[string]BackendRegistration{
		BackendClaude: claudeReg,
		BackendCodex:  codexReg,
		BackendAgy:    agyReg,
		BackendWerk:   werkReg,
	}
	if customize != nil {
		customize(regs)
	}

	env.runner = NewRunner(Options{
		Dir:      dir,
		Config:   cfgOpts,
		Keys:     &fakeKeys{},
		LeadKeys: &fakeCompanionLeadKeys{},
		Holders:  newFakeHolders(),
		WSMCP:    func(string) (MCPCommand, error) { return MCPCommand{Command: "ws-mcp"}, nil },
		Log:      io.Discard,
		Now: func() time.Time {
			env.nowMu.Lock()
			defer env.nowMu.Unlock()
			return env.now
		},
		Timer: env.timers.start,
		Trusted: func(req *http.Request) (bool, bool) {
			return env.trustedFn(req)
		},
		QuotaResetAt: func(backend, model string) int64 {
			if env.quotaResetAt != nil {
				return env.quotaResetAt(backend, model)
			}
			return 0
		},
		RestoreBundle: func(backend, _, wsKey string, cause RestoreCause, _ []AgentChild) (string, error) {
			env.restoreMu.Lock()
			env.restoreCalls = append(env.restoreCalls, backend)
			env.restoreMu.Unlock()
			return fmt.Sprintf("<ws-restore cause=%q backend=%q>\nkey=%s\n</ws-restore>", cause, backend, wsKey), nil
		},
		Backends: regs,
	})
	env.runner.Start(nil)
	t.Cleanup(env.runner.Close)
	return env
}

func (e *switchTestEnv) createLead(backend string, opts LeadOptions) State {
	e.t.Helper()
	st, err := e.runner.CreateLead(backend, e.root, opts)
	if err != nil {
		e.t.Fatalf("CreateLead(%s): %v", backend, err)
	}
	return st
}

func (e *switchTestEnv) sendAndSettle(id, text string, b *switchTestBackend, sid string) *wsCompactProc {
	e.t.Helper()
	_, _, beforeProcs := b.snapshot()
	beforeSends := 0
	var prevProc *wsCompactProc
	if len(beforeProcs) > 0 {
		prevProc = beforeProcs[len(beforeProcs)-1]
		sends, _ := prevProc.sent()
		beforeSends = len(sends)
	}
	if _, err := e.runner.Send(id, text); err != nil {
		e.t.Fatalf("Send(%s): %v", id, err)
	}
	var proc *wsCompactProc
	eventually(e.t, "backend proc start & send", func() bool {
		_, _, procs := b.snapshot()
		if len(procs) == 0 {
			return false
		}
		p := procs[len(procs)-1]
		sends, _ := p.sent()
		if p != prevProc {
			if len(sends) > 0 {
				proc = p
				return true
			}
			return false
		}
		if !p.closed && len(sends) > beforeSends {
			proc = p
			return true
		}
		return false
	})
	if sid != "" {
		proc.events <- Event{SessionID: sid}
	}
	proc.events <- Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: "ack: " + text, Final: true}}
	proc.events <- Event{TurnEnd: true}
	eventually(e.t, "turn idle", func() bool {
		st, err := e.runner.Get(id)
		return err == nil && st.Status == StatusIdle && (sid == "" || e.diskRecord(id).SessionID == sid)
	})
	return proc
}

func (e *switchTestEnv) diskRecord(id string) Record {
	e.t.Helper()
	rec, err := readRecord(wsdashboard.SessionDir(e.dir, id))
	if err != nil {
		e.t.Fatalf("readRecord(%s): %v", id, err)
	}
	return rec
}

func (e *switchTestEnv) httpSwitch(id string, body any, trusted, own bool) *httptest.ResponseRecorder {
	e.t.Helper()
	prev := e.trustedFn
	e.trustedFn = func(*http.Request) (bool, bool) { return trusted, own }
	defer func() { e.trustedFn = prev }()

	raw, err := json.Marshal(body)
	if err != nil {
		e.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, RoutePrefix+"/"+id+"/harness", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.runner.Handler().ServeHTTP(rec, req)
	return rec
}

func TestSwitchHarnessIdleLeadAndPostSwitchState(t *testing.T) {
	env := newSwitchTestEnv(t, nil)
	st := env.createLead(BackendClaude, LeadOptions{Model: "claude-opus-4-6", Effort: "high"})
	env.sendAndSettle(st.ID, "remember alpha-secret", env.claude, "claude-sid-1")

	c := env.runner.resident(st.ID)
	if c == nil {
		t.Fatal("resident conv missing")
	}
	// Populate fields that must be cleared atomically on switch, plus cached
	// compaction mode, retired-process host state, and source hard-overflow state.
	c.do(func() {
		c.update(func(rec *Record) {
			rec.SessionFile = "/tmp/old-session.jsonl"
			rec.PermissionMode = "plan"
			rec.AccountSlotID = "work"
			rec.PendingAccountSlotID = "backup"
			rec.AgyBypass = true
			rec.Tier = "large"
			rec.QuotaContinuation = &QuotaContinuation{Status: QuotaContinuationExhausted, NoticeText: "old limit"}
		})
		_ = c.leadCompactionMode(c.record())
		c.compactModeMu.Lock()
		c.compactWorkerMode = "ws"
		c.compactWorkerModeGen = 42
		c.compactModeMu.Unlock()
		c.setStatus(func() {
			bgCount := 1
			monCount := 1
			c.bgTasks = 2
			c.background = &bgCount
			c.monitors = &monCount
		})
		c.retry = &wslive.Retry{Attempt: 2}
		c.alerted = true
		c.compactTokens = 95000
		c.compactWindow = 100000
		c.compactHardAtStart = true
		c.compactPostRotationNoShrink = 2
		c.syncHost(false)
	})
	recBefore := env.diskRecord(st.ID)

	if err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{
		Backend: BackendCodex,
		Model:   "gpt-5-codex",
		Effort:  "medium",
	}); err != nil {
		t.Fatalf("SwitchHarness: %v", err)
	}

	// Verify retired-process host fields and source hard-overflow/no-shrink state
	// were cleared and synced to the host snapshot (Invariant 3).
	var (
		afterBgTasks       int
		afterBgPtr         *int
		afterMonPtr        *int
		afterRetry         *wslive.Retry
		afterAlerted       bool
		afterCompactTokens float64
		afterCompactWindow float64
		afterPostRotCheck  bool
		afterPostRotNoShrk int
		afterHostBgTasks   int
		afterHostRetry     *wslive.Retry
	)
	c.do(func() {
		afterBgTasks = c.bgTasks
		afterBgPtr = c.background
		afterMonPtr = c.monitors
		afterRetry = c.retry
		afterAlerted = c.alerted
		afterCompactTokens = c.compactTokens
		afterCompactWindow = c.compactWindow
		afterPostRotCheck = c.compactPostRotationCheck
		afterPostRotNoShrk = c.compactPostRotationNoShrink
		snap := c.live.st.Snapshot(env.now.UnixMilli())
		if snap.Host != nil {
			afterHostBgTasks = snap.Host.BackgroundTasks
			afterHostRetry = snap.Host.Retry
		}
	})
	if afterBgTasks != 0 || afterBgPtr != nil || afterMonPtr != nil || afterRetry != nil || afterAlerted ||
		afterHostBgTasks != 0 || afterHostRetry != nil {
		t.Fatalf("retired host state lingered after switch: bg=%d/%v/%v retry=%+v alerted=%v hostBg=%d hostRetry=%+v",
			afterBgTasks, afterBgPtr, afterMonPtr, afterRetry, afterAlerted, afterHostBgTasks, afterHostRetry)
	}
	if afterCompactTokens != 0 || afterCompactWindow != 0 || afterPostRotCheck || afterPostRotNoShrk != 0 {
		t.Fatalf("source compaction/hard-overflow state carried onto target: tokens=%v window=%v check=%v noShrink=%d",
			afterCompactTokens, afterCompactWindow, afterPostRotCheck, afterPostRotNoShrk)
	}

	// Verify atomic persisted record state.
	recAfter := env.diskRecord(st.ID)
	if recAfter.WSKey != recBefore.WSKey || recAfter.ReplyID != recBefore.ReplyID {
		t.Fatalf("identity changed: before=(%s,%s) after=(%s,%s)", recBefore.WSKey, recBefore.ReplyID, recAfter.WSKey, recAfter.ReplyID)
	}
	if recAfter.Backend != BackendCodex || recAfter.Profile != LeadProfileFor(BackendCodex, env.runner.opts.Backends) {
		t.Fatalf("backend/profile = (%q,%q), want (%q,%q)", recAfter.Backend, recAfter.Profile, BackendCodex, LeadProfileFor(BackendCodex, env.runner.opts.Backends))
	}
	if recAfter.Model != "gpt-5-codex" || recAfter.Effort != "medium" {
		t.Fatalf("model/effort = (%q,%q), want (gpt-5-codex, medium)", recAfter.Model, recAfter.Effort)
	}
	if recAfter.SessionID != "" || recAfter.SessionFile != "" || recAfter.PermissionMode != "" ||
		recAfter.AccountSlotID != "" || recAfter.PendingAccountSlotID != "" || recAfter.AgyBypass ||
		recAfter.QuotaContinuation != nil || recAfter.Tier != "" {
		t.Fatalf("stale harness fields not cleared: %+v", recAfter)
	}
	if recAfter.PendingCompaction == nil || recAfter.PendingCompaction.RotatedAfterSends == nil {
		t.Fatalf("PendingCompaction not persisted: %+v", recAfter.PendingCompaction)
	}

	// Verify compaction mode cache was cleared (then refreshed on demand).
	c.compactModeMu.Lock()
	if c.compactLeadMode != "" || c.compactModeGen != 0 || c.compactWorkerMode != "" || c.compactWorkerModeGen != 0 {
		c.compactModeMu.Unlock()
		t.Fatalf("compaction mode cache not cleared: lead=%q/%d worker=%q/%d", c.compactLeadMode, c.compactModeGen, c.compactWorkerMode, c.compactWorkerModeGen)
	}
	c.compactModeMu.Unlock()

	// Verify registration.json rewritten with new backend.
	reg, err := wsdashboard.ReadRegistration(env.dir, st.ID)
	if err != nil {
		t.Fatalf("ReadRegistration: %v", err)
	}
	if reg.Harness != BackendCodex {
		t.Fatalf("registration.json Harness = %q, want %q", reg.Harness, BackendCodex)
	}

	// Verify audit cut recorded with old backend on envelope and harness_switch meta.
	codexProc := env.codex.latest()
	if codexProc == nil {
		t.Fatal("target codex process not started")
	}
	_, audits := codexProc.audits()
	if len(audits) != 1 {
		t.Fatalf("audit count = %d, want 1", len(audits))
	}
	if audits[0].Harness != BackendClaude {
		t.Fatalf("audit envelope harness = %q, want old backend %q", audits[0].Harness, BackendClaude)
	}
	if audits[0].Meta["trigger_path"] != "harness_switch" || audits[0].Meta["from_backend"] != BackendClaude || audits[0].Meta["to_backend"] != BackendCodex {
		t.Fatalf("audit meta = %#v", audits[0].Meta)
	}

	// Verify ring preserved on disk.
	page, err := wslive.ReadRingPage(filepath.Join(wsdashboard.SessionDir(env.dir, st.ID), "ring"), "", wslive.RingHistoryCap)
	if err != nil || len(page.Records) == 0 {
		t.Fatalf("ring not preserved: err=%v len=%d", err, len(page.Records))
	}

	// Verify RestoreBundle on next send uses target backend and carries ring checkpoint.
	if _, err := env.runner.Send(st.ID, "continue after switch"); err != nil {
		t.Fatalf("post-switch Send: %v", err)
	}
	eventually(t, "post-switch prompt delivered", func() bool {
		sends, _ := codexProc.sent()
		return len(sends) > 0
	})
	sends, _ := codexProc.sent()
	if !strings.Contains(sends[0], `backend="codex"`) {
		t.Fatalf("RestoreBundle did not receive target backend:\n%s", sends[0])
	}
	if !strings.Contains(sends[0], "remember alpha-secret") {
		t.Fatalf("ring checkpoint missing prior dialog:\n%s", sends[0])
	}
}

func TestSwitchHarnessWhenProcNil(t *testing.T) {
	env := newSwitchTestEnv(t, nil)
	st := env.createLead(BackendClaude, LeadOptions{})
	env.sendAndSettle(st.ID, "first turn before death", env.claude, "claude-sid-dead")

	// Stop the process so proc == nil and simulate sticky usage-limit failure.
	if _, err := env.runner.Stop(st.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	c := env.runner.resident(st.ID)
	c.do(func() {
		c.setStatus(func() {
			c.failure = &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "quota exceeded"}
		})
		c.update(func(rec *Record) {
			rec.QuotaContinuation = &QuotaContinuation{Status: QuotaContinuationExhausted, NoticeText: "quota exceeded"}
		})
		c.syncHost(false)
	})
	if cur, _ := env.runner.Get(st.ID); cur.Process {
		t.Fatal("expected proc == nil before switch")
	}

	if err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendCodex}); err != nil {
		t.Fatalf("SwitchHarness with proc==nil failed: %v", err)
	}
	after, err := env.runner.Get(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Backend != BackendCodex || !after.Process || after.Failure != nil || after.QuotaContinuation != nil {
		t.Fatalf("unexpected state after proc==nil switch: %+v", after)
	}
}

func TestSwitchHarnessRollbackOnStartAndAuditFailure(t *testing.T) {
	t.Run("start_failure_rolls_back_even_after_spawned_and_plugin_warning", func(t *testing.T) {
		env := newSwitchTestEnv(t, nil)
		st := env.createLead(BackendClaude, LeadOptions{Model: "claude-opus-4-6"})
		env.sendAndSettle(st.ID, "keep this session", env.claude, "claude-sid-keep")

		c := env.runner.resident(st.ID)
		c.do(func() {
			c.update(func(rec *Record) {
				rec.SessionFile = "/tmp/keep.jsonl"
				rec.AccountSlotID = "acct-a"
				rec.Tier = "large"
			})
		})

		env.codex.mu.Lock()
		env.codex.failStartAt = 1
		env.codex.emitSpawned = true
		env.codex.pluginWarning = "simulated target warning"
		env.codex.mu.Unlock()

		err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendCodex, Model: "gpt-5"})
		if err == nil {
			t.Fatal("expected start failure")
		}

		// Both memory and disk must have the old harness/profile/session intact, and proc == nil.
		cur, err := env.runner.Get(st.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cur.Backend != BackendClaude || cur.Process || cur.Compacting {
			t.Fatalf("unexpected state after rollback: backend=%q process=%v compacting=%v", cur.Backend, cur.Process, cur.Compacting)
		}
		rec := env.diskRecord(st.ID)
		if rec.Backend != BackendClaude || rec.Profile != LeadProfileFor(BackendClaude, env.runner.opts.Backends) ||
			rec.SessionID != "claude-sid-keep" || rec.SessionFile != "/tmp/keep.jsonl" ||
			rec.AccountSlotID != "acct-a" || rec.Tier != "large" || rec.PluginWarning != "" {
			t.Fatalf("disk record corrupted on rollback: %+v", rec)
		}
		reg, err := wsdashboard.ReadRegistration(env.dir, st.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reg.Harness != BackendClaude || !reg.HostStopped {
			t.Fatalf("registration not rolled back: %+v", reg)
		}

		// Following Send must resume the old native session on the old backend.
		env.sendAndSettle(st.ID, "resume old harness", env.claude, "claude-sid-keep")
		starts, _, _ := env.claude.snapshot()
		if len(starts) < 2 || starts[len(starts)-1].SessionID != "claude-sid-keep" {
			t.Fatalf("expected resume of claude-sid-keep, got starts=%+v", starts)
		}
	})

	t.Run("audit_failure_rolls_back_and_retires_target", func(t *testing.T) {
		env := newSwitchTestEnv(t, nil)
		st := env.createLead(BackendClaude, LeadOptions{})
		env.sendAndSettle(st.ID, "before audit fail", env.claude, "claude-sid-audit")

		env.codex.mu.Lock()
		env.codex.failAuditErr = errors.New("audit disk full")
		env.codex.mu.Unlock()

		if err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendCodex}); err == nil {
			t.Fatal("expected audit commit failure")
		}
		rec := env.diskRecord(st.ID)
		if rec.Backend != BackendClaude || rec.SessionID != "claude-sid-audit" {
			t.Fatalf("disk record after audit rollback: %+v", rec)
		}
		if codexProc := env.codex.latest(); codexProc == nil || !codexProc.closed {
			t.Fatalf("target process was not retired on audit failure")
		}
		env.sendAndSettle(st.ID, "resume after audit fail", env.claude, "claude-sid-audit")
		starts, _, _ := env.claude.snapshot()
		if starts[len(starts)-1].SessionID != "claude-sid-audit" {
			t.Fatalf("expected resume of claude-sid-audit, got %q", starts[len(starts)-1].SessionID)
		}
	})

	t.Run("commit_persistence_failure_rolls_back_and_emits_compact_failed_without_compact_end", func(t *testing.T) {
		env := newSwitchTestEnv(t, nil)
		st := env.createLead(BackendClaude, LeadOptions{Model: "claude-opus-4-6"})
		env.sendAndSettle(st.ID, "before commit fail", env.claude, "claude-sid-persist")

		c := env.runner.resident(st.ID)
		if c == nil {
			t.Fatal("resident conv missing")
		}
		priorPending := &PendingCompaction{Note: "prior-pending-note", Origin: wslive.TriggerManual}
		c.do(func() {
			c.update(func(rec *Record) {
				rec.PendingCompaction = clonePendingCompaction(priorPending)
			})
			c.compactNote = "prior-compact-note"
			c.compactTokens = 55000
			c.compactWindow = 100000
			c.onPersistRecord = func(rec Record) error {
				if rec.Backend == BackendCodex {
					return errors.New("simulated wal write failure")
				}
				return nil
			}
		})

		err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendCodex, Model: "gpt-5-codex"})
		if err == nil || !strings.Contains(err.Error(), "persist switched record") || !strings.Contains(err.Error(), "simulated wal write failure") {
			t.Fatalf("expected persist switched record failure, got %v", err)
		}

		// Target process must have been retired on commit failure.
		if codexProc := env.codex.latest(); codexProc == nil || !codexProc.closed {
			t.Fatalf("target process was not retired on persistence failure")
		}

		// Compaction state must report failure and never increment cmpCount or emit CompactEnd.
		var (
			compacting    bool
			compactCmd    bool
			compactNote   string
			compactTokens float64
			compactWindow float64
			livePhase     string
		)
		c.do(func() {
			compacting = c.compacting
			compactCmd = c.compactCommand
			compactNote = c.compactNote
			compactTokens = c.compactTokens
			compactWindow = c.compactWindow
			livePhase = c.live.st.Snapshot(env.now.UnixMilli()).Lead.Phase
			c.onPersistRecord = nil
		})
		cmpCount, cmpLast := c.compactionCount()
		if compacting || compactCmd || cmpCount != 0 || cmpLast != "" || livePhase == wslive.PhaseCompacting {
			t.Fatalf("unexpected compaction state after commit failure: compacting=%v cmd=%v cmpCount=%d cmpLast=%q livePhase=%q",
				compacting, compactCmd, cmpCount, cmpLast, livePhase)
		}
		if compactNote != "prior-compact-note" || compactTokens != 55000 || compactWindow != 100000 {
			t.Fatalf("source compaction fields not restored: note=%q tokens=%v window=%v",
				compactNote, compactTokens, compactWindow)
		}

		page, err := wslive.ReadRingPage(filepath.Join(wsdashboard.SessionDir(env.dir, st.ID), "ring"), "", wslive.RingHistoryCap)
		if err != nil {
			t.Fatalf("ReadRingPage: %v", err)
		}
		failedEvents, endEvents := 0, 0
		for _, entry := range page.Records {
			if entry.Kind == wslive.RingCompact {
				if entry.CompactFailed {
					failedEvents++
				} else {
					endEvents++
				}
			}
		}
		if failedEvents != 1 || endEvents != 0 {
			t.Fatalf("ring compaction events failed=%d end=%d (want failed=1 end=0): %+v", failedEvents, endEvents, page.Records)
		}

		// Both memory and disk must retain the pre-switch backend, session, and PendingCompaction.
		cur, err := env.runner.Get(st.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cur.Backend != BackendClaude || cur.Process || cur.Compacting {
			t.Fatalf("unexpected state after persistence rollback: %+v", cur)
		}
		rec := env.diskRecord(st.ID)
		if rec.Backend != BackendClaude || rec.SessionID != "claude-sid-persist" ||
			rec.PendingCompaction == nil || rec.PendingCompaction.Note != "prior-pending-note" {
			t.Fatalf("disk record not rolled back cleanly: %+v", rec)
		}
		reg, err := wsdashboard.ReadRegistration(env.dir, st.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reg.Harness != BackendClaude || !reg.HostStopped {
			t.Fatalf("registration not rolled back: %+v", reg)
		}

		// Subsequent Send resumes the old native session on Claude.
		env.sendAndSettle(st.ID, "resume after commit fail", env.claude, "claude-sid-persist")
		starts, _, _ := env.claude.snapshot()
		if starts[len(starts)-1].SessionID != "claude-sid-persist" {
			t.Fatalf("expected resume of claude-sid-persist, got %q", starts[len(starts)-1].SessionID)
		}
	})

	t.Run("transient_commit_failure_restores_defer_before_holder_release", func(t *testing.T) {
		env := newSwitchTestEnv(t, nil)
		st := env.createLead(BackendClaude, LeadOptions{Model: "claude-opus-4-6"})
		env.sendAndSettle(st.ID, "before transient commit fail", env.claude, "claude-sid-transient")

		c := env.runner.resident(st.ID)
		if c == nil {
			t.Fatal("resident conv missing")
		}
		var (
			releaseHookCalled bool
			deferDuringStop   bool
			recDuringStop     Record
			persistCalls      []string
			persistMu         sync.Mutex
		)
		env.codex.mu.Lock()
		env.codex.onStopError = func() {
			releaseHookCalled = true
			c.wmu.Lock()
			deferDuringStop = c.deferRecordPersist
			recDuringStop = c.record()
			c.wmu.Unlock()
		}
		env.codex.mu.Unlock()

		var failedOnce bool
		c.do(func() {
			c.onPersistRecord = func(rec Record) error {
				persistMu.Lock()
				persistCalls = append(persistCalls, rec.Backend+":"+rec.SessionID)
				shouldFail := !failedOnce && rec.Backend == BackendCodex
				if shouldFail {
					failedOnce = true
				}
				persistMu.Unlock()
				if shouldFail {
					return errors.New("transient wal write error")
				}
				return nil
			}
		})

		err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendCodex, Model: "gpt-5-codex"})
		if err == nil || !strings.Contains(err.Error(), "transient wal write error") {
			t.Fatalf("expected transient wal write error, got %v", err)
		}
		c.do(func() { c.onPersistRecord = nil })

		if !releaseHookCalled {
			t.Fatal("expected releaseHolder -> StopError hook during retireCompactionProcess")
		}
		if !deferDuringStop {
			t.Fatal("c.deferRecordPersist was false during retireCompactionProcess after commit failure")
		}
		if recDuringStop.HolderID == "" {
			t.Fatal("HolderID was cleared before releaseHolder ran")
		}
		if recDuringStop.Backend != BackendClaude || recDuringStop.SessionID != "claude-sid-transient" {
			t.Fatalf("c.rec still held target fields during releaseHolder: backend=%q sid=%q", recDuringStop.Backend, recDuringStop.SessionID)
		}

		// Expect: 1) initial source holder retirement (claude), 2) failed commitHarnessSwitch
		// (codex), 3) rollbackHarnessSwitch (claude). Target releaseHolder during
		// retireCompactionProcess must NOT persist aborted target fields or clear recordDirty.
		persistMu.Lock()
		gotCalls := append([]string(nil), persistCalls...)
		persistMu.Unlock()
		if len(gotCalls) != 3 || gotCalls[0] != "claude:claude-sid-transient" || gotCalls[1] != "codex:" || gotCalls[2] != "claude:claude-sid-transient" {
			t.Fatalf("unexpected persistRecord sequence = %v, want [claude:claude-sid-transient codex: claude:claude-sid-transient]", gotCalls)
		}

		rec := env.diskRecord(st.ID)
		if rec.Backend != BackendClaude || rec.SessionID != "claude-sid-transient" {
			t.Fatalf("disk record corrupted after transient commit failure: %+v", rec)
		}
		reg, err := wsdashboard.ReadRegistration(env.dir, st.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reg.Harness != BackendClaude || !reg.HostStopped {
			t.Fatalf("registration not rolled back: %+v", reg)
		}
	})
}

func TestSwitchHarnessBusyAndQuotaContinuationGuards(t *testing.T) {
	env := newSwitchTestEnv(t, nil)
	st := env.createLead(BackendClaude, LeadOptions{})
	env.sendAndSettle(st.ID, "seed", env.claude, "claude-sid-busy")
	c := env.runner.resident(st.ID)

	assertBusy := func(name string, setup, cleanup func()) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			c.do(setup)
			defer c.do(cleanup)
			err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendCodex})
			if !errors.Is(err, ErrConversationBusy) {
				t.Fatalf("err = %v, want ErrConversationBusy", err)
			}
		})
	}

	assertBusy("turn_running", func() { c.turn = true }, func() { c.turn = false })
	assertBusy("compacting", func() { c.compacting = true }, func() { c.compacting = false })
	assertBusy("compact_command", func() { c.compactCommand = true }, func() { c.compactCommand = false })
	assertBusy("quota_fence_firing", func() { c.quotaCancelFence.Store(quotaFenceFiring) }, func() { c.quotaCancelFence.Store(quotaFenceIdle) })
	assertBusy("quota_in_flight", func() {
		c.update(func(rec *Record) { rec.QuotaContinuation = &QuotaContinuation{Status: QuotaContinuationInFlight} })
	}, func() {
		c.update(func(rec *Record) { rec.QuotaContinuation = nil })
	})
	assertBusy("durable_turn", func() {
		c.update(func(rec *Record) { rec.DurableTurn = &durableTurnInput{ID: "t1"} })
	}, func() {
		c.update(func(rec *Record) { rec.DurableTurn = nil })
	})

	// Scheduled quota continuation is NOT busy: failure re-arms timer + keeps record; success clears + syncs host.
	resumeAt := env.now.Add(10 * time.Minute).UnixMilli()
	c.do(func() {
		c.setQuotaContinuation(&QuotaContinuation{
			Status:     QuotaContinuationScheduled,
			ResumeAt:   resumeAt,
			NoticeText: "usage limit",
		}, env.now)
		c.syncHost(false)
	})

	env.codex.mu.Lock()
	env.codex.failStartAt = 1
	env.codex.mu.Unlock()
	if err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendCodex}); err == nil {
		t.Fatal("expected simulated failure")
	}
	var armedAfterFail bool
	c.do(func() { armedAfterFail = c.quotaC != nil })
	if !armedAfterFail {
		t.Fatal("scheduled quota continuation timer was not re-armed after failed switch")
	}
	if rec := env.diskRecord(st.ID); rec.QuotaContinuation == nil || rec.QuotaContinuation.Status != QuotaContinuationScheduled {
		t.Fatalf("persisted QuotaContinuation lost on failed switch: %+v", rec.QuotaContinuation)
	}

	// Now allow switch to succeed: scheduled continuation cleared and host view synced.
	env.codex.mu.Lock()
	env.codex.failStartAt = 0
	env.codex.mu.Unlock()
	if err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendCodex}); err != nil {
		t.Fatalf("SwitchHarness with scheduled continuation failed: %v", err)
	}
	var armedAfterSuccess bool
	var hostQC *wslive.QuotaContinuation
	c.do(func() {
		armedAfterSuccess = c.quotaC != nil
		snap := c.live.st.Snapshot(env.now.UnixMilli())
		if snap.Host != nil {
			hostQC = snap.Host.QuotaContinuation
		}
	})
	if armedAfterSuccess {
		t.Fatal("quota timer still armed after successful switch")
	}
	if hostQC != nil {
		t.Fatalf("live host QuotaContinuation not cleared by syncHost: %+v", hostQC)
	}
	if rec := env.diskRecord(st.ID); rec.QuotaContinuation != nil {
		t.Fatalf("persisted QuotaContinuation not cleared: %+v", rec.QuotaContinuation)
	}
}

func TestSwitchHarnessConsumesQueuedCompactPending(t *testing.T) {
	env := newSwitchTestEnv(t, nil)
	st := env.createLead(BackendClaude, LeadOptions{})
	env.sendAndSettle(st.ID, "before compact pending", env.claude, "claude-sid-cp")
	c := env.runner.resident(st.ID)

	prose := make([]string, len(wscompact.ProseFields()))
	prose[0] = "carry-forward-goal-from-queued-compact"
	queued := &compactRequest{
		resume:      false,
		triggerPath: "manual",
		lead:        &wscompact.LeadSummary{Prose: prose},
	}
	c.do(func() { c.compactPending = queued })

	// Failed switch must leave compactPending intact.
	env.codex.mu.Lock()
	env.codex.failStartAt = 1
	env.codex.mu.Unlock()
	if err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendCodex}); err == nil {
		t.Fatal("expected start failure")
	}
	var stillQueued bool
	c.do(func() { stillQueued = c.compactPending == queued })
	if !stillQueued {
		t.Fatal("compactPending was cleared on failed switch")
	}

	// Successful switch consumes compactPending and carries its summary prose.
	env.codex.mu.Lock()
	env.codex.failStartAt = 0
	env.codex.mu.Unlock()
	if err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendCodex}); err != nil {
		t.Fatalf("SwitchHarness failed: %v", err)
	}
	var cleared bool
	c.do(func() { cleared = c.compactPending == nil })
	if !cleared {
		t.Fatal("compactPending was not cleared on successful switch")
	}
	codexProc := env.codex.latest()
	if _, err := env.runner.Send(st.ID, "go"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "codex prompt received", func() bool {
		sends, _ := codexProc.sent()
		return len(sends) > 0
	})
	sends, _ := codexProc.sent()
	if !strings.Contains(sends[0], "carry-forward-goal-from-queued-compact") {
		t.Fatalf("queued compactPending prose not carried into switch checkpoint:\n%s", sends[0])
	}
	// Verify no second rotation fired on codex.
	starts, _, _ := env.codex.snapshot()
	if len(starts) != 1 {
		t.Fatalf("expected 1 codex start (no second rotation), got %d", len(starts))
	}
}

func TestSwitchHarnessValidationAndDefaults(t *testing.T) {
	env := newSwitchTestEnv(t, func(regs map[string]BackendRegistration) {
		codex := regs[BackendCodex]
		codex.Backend = &admittingSwitchBackend{
			switchTestBackend: codex.Backend.(*switchTestBackend),
			admitFn: func(provider, model string) error {
				if provider == "bad-prov" {
					return badRequest("unsupported provider %q", provider)
				}
				if model == "forbidden-model" {
					return badRequest("model %q is rejected by target", model)
				}
				return nil
			},
		}
		regs[BackendCodex] = codex
	})

	claudeSt := env.createLead(BackendClaude, LeadOptions{Model: "claude-opus-4-6", Effort: "high"})
	werkSt := env.createLead(BackendWerk, LeadOptions{})
	workerSt, err := env.runner.Create("agent-worker-claude", env.root)
	if err != nil {
		t.Fatalf("Create worker: %v", err)
	}

	// Non-lead worker -> 400 harness_unsupported.
	if err := env.runner.SwitchHarness(context.Background(), workerSt.ID, HarnessSwitchSpec{Backend: BackendCodex}); !errors.Is(err, ErrHarnessUnsupported) {
		t.Fatalf("worker switch err = %v, want ErrHarnessUnsupported", err)
	}
	// From werk -> 400 harness_unsupported.
	if err := env.runner.SwitchHarness(context.Background(), werkSt.ID, HarnessSwitchSpec{Backend: BackendCodex}); !errors.Is(err, ErrHarnessUnsupported) {
		t.Fatalf("from-werk switch err = %v, want ErrHarnessUnsupported", err)
	}
	// To werk -> 400 harness_unsupported.
	if err := env.runner.SwitchHarness(context.Background(), claudeSt.ID, HarnessSwitchSpec{Backend: BackendWerk}); !errors.Is(err, ErrHarnessUnsupported) {
		t.Fatalf("to-werk switch err = %v, want ErrHarnessUnsupported", err)
	}
	// Retired Pi -> 400 harness_unsupported.
	if err := env.runner.SwitchHarness(context.Background(), claudeSt.ID, HarnessSwitchSpec{Backend: BackendPi}); !errors.Is(err, ErrHarnessUnsupported) {
		t.Fatalf("to-pi switch err = %v, want ErrHarnessUnsupported", err)
	}
	// Unknown backend -> 400 harness_unsupported.
	if err := env.runner.SwitchHarness(context.Background(), claudeSt.ID, HarnessSwitchSpec{Backend: "nonexistent"}); !errors.Is(err, ErrHarnessUnsupported) {
		t.Fatalf("unknown switch err = %v, want ErrHarnessUnsupported", err)
	}
	// Same backend -> 400 harness_unchanged.
	if err := env.runner.SwitchHarness(context.Background(), claudeSt.ID, HarnessSwitchSpec{Backend: BackendClaude}); !errors.Is(err, ErrHarnessUnchanged) {
		t.Fatalf("same-backend switch err = %v, want ErrHarnessUnchanged", err)
	}
	// Invalid model pattern -> 400.
	if err := env.runner.SwitchHarness(context.Background(), claudeSt.ID, HarnessSwitchSpec{Backend: BackendCodex, Model: "bad model with spaces"}); err == nil {
		t.Fatal("expected invalid model error")
	}
	// Invalid effort pattern -> 400.
	if err := env.runner.SwitchHarness(context.Background(), claudeSt.ID, HarnessSwitchSpec{Backend: BackendCodex, Effort: "UPPERCASE!"}); err == nil {
		t.Fatal("expected invalid effort error")
	}
	// Rejected by target leadProviderAdmitter -> 400.
	if err := env.runner.SwitchHarness(context.Background(), claudeSt.ID, HarnessSwitchSpec{Backend: BackendCodex, Provider: "bad-prov"}); err == nil {
		t.Fatal("expected provider admission error")
	}
	if err := env.runner.SwitchHarness(context.Background(), claudeSt.ID, HarnessSwitchSpec{Backend: BackendCodex, Model: "forbidden-model"}); err == nil {
		t.Fatal("expected model admission error")
	}
	// Non-empty provider rejected on a backend without leadProviderAdmitter -> 400.
	if err := env.runner.SwitchHarness(context.Background(), claudeSt.ID, HarnessSwitchSpec{Backend: BackendAgy, Provider: "any-prov"}); err == nil {
		t.Fatal("expected provider unsupported error for backend without leadProviderAdmitter")
	}

	// Omitted model/effort/provider resolve to target lead profile default, NEVER source values.
	if err := env.runner.SwitchHarness(context.Background(), claudeSt.ID, HarnessSwitchSpec{Backend: BackendCodex}); err != nil {
		t.Fatalf("SwitchHarness omitted defaults: %v", err)
	}
	after := env.diskRecord(claudeSt.ID)
	if after.Model != "" || after.Effort != "" || after.Provider != "" {
		t.Fatalf("source model/effort/provider leaked into target: model=%q effort=%q provider=%q", after.Model, after.Effort, after.Provider)
	}
}

func TestSwitchHarnessPermission1838(t *testing.T) {
	t.Run("explicit_selection_carries_over_and_unsupported_refuses_before_retire", func(t *testing.T) {
		env := newSwitchTestEnv(t, func(regs map[string]BackendRegistration) {
			// Make agy NOT support PermissionFull so we can verify 400 refusal before retire.
			agy := regs[BackendAgy]
			agy.PermissionLevels = []PermissionLevel{PermissionReadOnly, PermissionWorkdirWrite}
			regs[BackendAgy] = agy
		})
		st := env.createLead(BackendClaude, LeadOptions{
			PermissionLevel:   PermissionFull,
			PermissionFullAck: true,
			permissionUser:    true,
		})
		oldProc := env.sendAndSettle(st.ID, "running claude", env.claude, "claude-perm-1")

		// Target agy does not support explicit PermissionFull -> 400 before retiring old process!
		err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendAgy})
		var re *requestError
		if !errors.As(err, &re) || re.status != http.StatusBadRequest {
			t.Fatalf("err = %v, want 400", err)
		}
		if oldProc.closed {
			t.Fatal("old process was retired despite pre-flight permission refusal")
		}

		// Target codex supports PermissionFull -> succeeds and carries selection unchanged.
		if err := env.runner.SwitchHarness(context.Background(), st.ID, HarnessSwitchSpec{Backend: BackendCodex}); err != nil {
			t.Fatalf("SwitchHarness to codex: %v", err)
		}
		level, explicit, err := env.runner.refreshPermissionSelection(st.ID, wsdashboard.SessionDir(env.dir, st.ID))
		if err != nil || !explicit || level != PermissionFull {
			t.Fatalf("permission selection after switch = (%q, %v, %v), want (full, true, nil)", level, explicit, err)
		}
	})

	t.Run("legacy_derived_raise_refused_on_companion_allowed_on_http_no_selection_written", func(t *testing.T) {
		env := newSwitchTestEnv(t, func(regs map[string]BackendRegistration) {
			// Claude legacy default = read-only; Codex legacy default = full; Agy legacy default = read-only.
			claude := regs[BackendClaude]
			claude.Permission = PermissionPolicy{LegacyDefault: PermissionReadOnly}
			regs[BackendClaude] = claude
			codex := regs[BackendCodex]
			codex.Permission = PermissionPolicy{LegacyDefault: PermissionFull}
			regs[BackendCodex] = codex
			agy := regs[BackendAgy]
			agy.Permission = PermissionPolicy{LegacyDefault: PermissionReadOnly}
			regs[BackendAgy] = agy
		})
		st := env.createLead(BackendClaude, LeadOptions{})
		oldProc := env.sendAndSettle(st.ID, "hello", env.claude, "claude-leg-1")
		wsKey := env.diskRecord(st.ID).WSKey

		// Companion seam (user=false) raising read-only -> full must refuse 403 before retirement.
		err := env.runner.SwitchCompanionHarness(context.Background(), wsKey, HarnessSwitchSpec{Backend: BackendCodex})
		var re *requestError
		if !errors.As(err, &re) || re.status != http.StatusForbidden {
			t.Fatalf("SwitchCompanionHarness raise err = %v, want 403", err)
		}
		if oldProc.closed {
			t.Fatal("old process was retired despite 403 permission raise refusal")
		}

		// Equal/lower level (read-only -> read-only on agy) succeeds through companion seam, writing NO selection.
		if err := env.runner.SwitchCompanionHarness(context.Background(), wsKey, HarnessSwitchSpec{Backend: BackendAgy}); err != nil {
			t.Fatalf("SwitchCompanionHarness equal level failed: %v", err)
		}
		if _, explicit, err := env.runner.refreshPermissionSelection(st.ID, wsdashboard.SessionDir(env.dir, st.ID)); err != nil || explicit {
			t.Fatalf("switch wrote an explicit permission selection: explicit=%v err=%v", explicit, err)
		}

		// Trusted HTTP user path raising read-only -> full succeeds 200 without ack and writes NO selection.
		w := env.httpSwitch(st.ID, HarnessSwitchSpec{Backend: BackendCodex}, true, true)
		if w.Code != http.StatusOK {
			t.Fatalf("HTTP switch raise status = %d body=%s", w.Code, w.Body.String())
		}
		if _, explicit, err := env.runner.refreshPermissionSelection(st.ID, wsdashboard.SessionDir(env.dir, st.ID)); err != nil || explicit {
			t.Fatalf("HTTP switch wrote an explicit permission selection: explicit=%v err=%v", explicit, err)
		}
	})

	t.Run("builtin_unresolved_levels_fail_closed_for_companion_allowed_for_user", func(t *testing.T) {
		env := newSwitchTestEnv(t, nil)

		// 1. Builtin claude (plan -> read-only) -> codex/agy ("" unresolved): companion refused 403 before retirement; HTTP allowed.
		stPlan := env.createLead(BackendClaude, LeadOptions{PermissionMode: "plan"})
		oldPlanProc := env.sendAndSettle(stPlan.ID, "plan mode", env.claude, "claude-plan-1")
		wsPlan := env.diskRecord(stPlan.ID).WSKey
		for _, target := range []string{BackendCodex, BackendAgy} {
			err := env.runner.SwitchCompanionHarness(context.Background(), wsPlan, HarnessSwitchSpec{Backend: target})
			var re *requestError
			if !errors.As(err, &re) || re.status != http.StatusForbidden {
				t.Fatalf("companion plan->%s err = %v, want 403", target, err)
			}
			if oldPlanProc.closed {
				t.Fatalf("old process retired despite 403 for plan->%s", target)
			}
		}
		if w := env.httpSwitch(stPlan.ID, HarnessSwitchSpec{Backend: BackendCodex}, true, true); w.Code != http.StatusOK {
			t.Fatalf("HTTP plan->codex status = %d body=%s", w.Code, w.Body.String())
		}

		// 2. Builtin claude (bypassPermissions -> full) -> codex ("" unresolved): already max rank, companion allowed.
		stFull := env.createLead(BackendClaude, LeadOptions{PermissionMode: "bypassPermissions"})
		env.sendAndSettle(stFull.ID, "full mode", env.claude, "claude-full-1")
		wsFull := env.diskRecord(stFull.ID).WSKey
		if err := env.runner.SwitchCompanionHarness(context.Background(), wsFull, HarnessSwitchSpec{Backend: BackendCodex}); err != nil {
			t.Fatalf("companion full->codex failed: %v", err)
		}

		// 3. Builtin idle codex (proc==nil -> sourceLevel "") -> claude (workdir-write): companion refused 403; HTTP allowed.
		stIdleCodex := env.createLead(BackendCodex, LeadOptions{})
		wsIdleCodex := env.diskRecord(stIdleCodex.ID).WSKey
		err := env.runner.SwitchCompanionHarness(context.Background(), wsIdleCodex, HarnessSwitchSpec{Backend: BackendClaude})
		var re *requestError
		if !errors.As(err, &re) || re.status != http.StatusForbidden {
			t.Fatalf("companion idle codex->claude err = %v, want 403", err)
		}
		if w := env.httpSwitch(stIdleCodex.ID, HarnessSwitchSpec{Backend: BackendClaude}, true, true); w.Code != http.StatusOK {
			t.Fatalf("HTTP idle codex->claude status = %d body=%s", w.Code, w.Body.String())
		}

		// 4. Builtin live codex (workspace-write -> workdir-write) -> claude (workdir-write): equal rank, companion allowed.
		stLiveCodex := env.createLead(BackendCodex, LeadOptions{})
		env.sendAndSettle(stLiveCodex.ID, "live codex", env.codex, "codex-live-1")
		cLive := env.runner.resident(stLiveCodex.ID)
		cLive.do(func() { cLive.setStatus(func() { cLive.activeMode = "read-only" }) })
		wsLive := env.diskRecord(stLiveCodex.ID).WSKey
		err = env.runner.SwitchCompanionHarness(context.Background(), wsLive, HarnessSwitchSpec{Backend: BackendClaude})
		if !errors.As(err, &re) || re.status != http.StatusForbidden {
			t.Fatalf("companion live codex(read-only)->claude err = %v, want 403", err)
		}
		cLive.do(func() { cLive.setStatus(func() { cLive.activeMode = "workspace-write" }) })
		if err := env.runner.SwitchCompanionHarness(context.Background(), wsLive, HarnessSwitchSpec{Backend: BackendClaude}); err != nil {
			t.Fatalf("companion live codex(workspace-write)->claude failed: %v", err)
		}

		// 5. Builtin agy ("" unresolved) -> claude (workdir-write): companion refused 403; HTTP allowed.
		stAgy := env.createLead(BackendAgy, LeadOptions{})
		wsAgy := env.diskRecord(stAgy.ID).WSKey
		err = env.runner.SwitchCompanionHarness(context.Background(), wsAgy, HarnessSwitchSpec{Backend: BackendClaude})
		if !errors.As(err, &re) || re.status != http.StatusForbidden {
			t.Fatalf("companion agy->claude err = %v, want 403", err)
		}
		if w := env.httpSwitch(stAgy.ID, HarnessSwitchSpec{Backend: BackendClaude}, true, true); w.Code != http.StatusOK {
			t.Fatalf("HTTP agy->claude status = %d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestSwitchCompanionHarnessAndHasUsableHarness(t *testing.T) {
	env := newSwitchTestEnv(t, func(regs map[string]BackendRegistration) {
		agy := regs[BackendAgy]
		agy.WorkerCapable = false
		regs[BackendAgy] = agy
	})
	st := env.createLead(BackendClaude, LeadOptions{
		CompanionRole:         "companion-body",
		CompanionSystemPrompt: "companion prompt",
	})
	// Explicit permission selection carries across supported companion harnesses (claude <-> codex).
	if err := env.runner.writePermissionSelection(st.ID, wsdashboard.SessionDir(env.dir, st.ID), PermissionWorkdirWrite); err != nil {
		t.Fatal(err)
	}
	rec := env.diskRecord(st.ID)
	wsKey := rec.WSKey

	// Idle proc == nil companion lead is usable.
	if !env.runner.HasUsableHarness(wsKey) {
		t.Fatal("expected HasUsableHarness == true for idle proc==nil lead")
	}

	// Target with companionHarnessGap refused with 400 harness_unsupported.
	if err := env.runner.SwitchCompanionHarness(context.Background(), wsKey, HarnessSwitchSpec{Backend: BackendAgy}); !errors.Is(err, ErrHarnessUnsupported) {
		t.Fatalf("companion gap switch err = %v, want ErrHarnessUnsupported", err)
	}

	// Switch to healthy codex succeeds, preserving WSKey.
	if err := env.runner.SwitchCompanionHarness(context.Background(), wsKey, HarnessSwitchSpec{Backend: BackendCodex}); err != nil {
		t.Fatalf("SwitchCompanionHarness(codex): %v", err)
	}
	if after := env.diskRecord(st.ID); after.WSKey != wsKey || after.Backend != BackendCodex {
		t.Fatalf("unexpected companion record after switch: %+v", after)
	}
	if !env.runner.HasUsableHarness(wsKey) {
		t.Fatal("expected HasUsableHarness == true after switch")
	}

	c := env.runner.resident(st.ID)
	// Sticky failure -> false.
	c.do(func() { c.setStatus(func() { c.failure = &wslive.Notice{Kind: wslive.NoticeLogin, Text: "login required"} }) })
	if env.runner.HasUsableHarness(wsKey) {
		t.Fatal("expected HasUsableHarness == false for sticky failure")
	}
	c.do(func() { c.setStatus(func() { c.failure = nil }) })

	// Quota exhausted via QuotaContinuation -> false.
	c.do(func() {
		c.update(func(rec *Record) { rec.QuotaContinuation = &QuotaContinuation{Status: QuotaContinuationExhausted} })
	})
	if env.runner.HasUsableHarness(wsKey) {
		t.Fatal("expected HasUsableHarness == false for quota-exhausted conversation")
	}
	c.do(func() { c.update(func(rec *Record) { rec.QuotaContinuation = nil }) })

	// Hidden -> false.
	c.do(func() { c.setStatus(func() { c.hidden = true }) })
	if env.runner.HasUsableHarness(wsKey) {
		t.Fatal("expected HasUsableHarness == false for hidden conversation")
	}
	c.do(func() { c.setStatus(func() { c.hidden = false }) })

	// Broken MCP -> false.
	c.do(func() { c.setStatus(func() { c.mcpBroken = []string{"ws"} }) })
	if env.runner.HasUsableHarness(wsKey) {
		t.Fatal("expected HasUsableHarness == false when mcpBroken is non-empty")
	}
	c.do(func() { c.setStatus(func() { c.mcpBroken = nil }) })

	// Unregistered backend -> false.
	var savedBackend string
	c.do(func() {
		c.mu.Lock()
		savedBackend = c.rec.Backend
		c.rec.Backend = "unregistered-backend"
		c.mu.Unlock()
	})
	if env.runner.HasUsableHarness(wsKey) {
		t.Fatal("expected HasUsableHarness == false for unregistered backend")
	}
	c.do(func() {
		c.mu.Lock()
		c.rec.Backend = savedBackend
		c.mu.Unlock()
	})

	// Unknown wsKey -> false / errUnknown.
	if env.runner.HasUsableHarness("nonexistent-ws-key") {
		t.Fatal("expected HasUsableHarness == false for unknown wsKey")
	}
	if err := env.runner.SwitchCompanionHarness(context.Background(), "nonexistent-ws-key", HarnessSwitchSpec{Backend: BackendClaude}); !errors.Is(err, errUnknown) {
		t.Fatalf("SwitchCompanionHarness unknown wsKey err = %v, want errUnknown", err)
	}

	// Closed -> false / endedError (409).
	if _, err := env.runner.CloseConversation(st.ID); err != nil {
		t.Fatal(err)
	}
	if env.runner.HasUsableHarness(wsKey) {
		t.Fatal("expected HasUsableHarness == false for closed conversation")
	}
	var closedErr *requestError
	if err := env.runner.SwitchCompanionHarness(context.Background(), wsKey, HarnessSwitchSpec{Backend: BackendClaude}); !errors.As(err, &closedErr) || closedErr.status != http.StatusConflict {
		t.Fatalf("SwitchCompanionHarness closed err = %v, want 409", err)
	}

	// Parked conversation must return false from HasUsableHarness WITHOUT waking it,
	// and SwitchCompanionHarness must unpark and switch it via replyIDByWSKey.
	parkedID := "parkedconv"
	parkedKey := "parked-ws-key-1"
	parkedRec := Record{
		ReplyID:               parkedID,
		WSKey:                 parkedKey,
		Lead:                  true,
		Backend:               BackendClaude,
		Profile:               LeadProfileFor(BackendClaude, env.runner.opts.Backends),
		Root:                  env.root,
		CreatedAt:             env.now.UTC().Format(time.RFC3339),
		CompanionRole:         "companion-body",
		CompanionSystemPrompt: "companion prompt",
	}
	parkedDir := wsdashboard.SessionDir(env.dir, parkedID)
	if err := os.MkdirAll(parkedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := env.runner.persistAgentRecord(parkedDir, parkedRec); err != nil {
		t.Fatal(err)
	}
	if err := env.runner.writePermissionSelection(parkedID, parkedDir, PermissionWorkdirWrite); err != nil {
		t.Fatal(err)
	}
	env.runner.mu.Lock()
	env.runner.registerParkedLocked(parkedRec)
	env.runner.mu.Unlock()
	if env.runner.HasUsableHarness(parkedKey) {
		t.Fatal("expected HasUsableHarness == false for parked conversation")
	}
	env.runner.mu.Lock()
	stillParked := env.runner.parked[parkedID] && env.runner.convs[parkedID] == nil
	env.runner.mu.Unlock()
	if !stillParked {
		t.Fatal("HasUsableHarness woke a parked conversation")
	}
	if err := env.runner.SwitchCompanionHarness(context.Background(), parkedKey, HarnessSwitchSpec{Backend: BackendCodex}); err != nil {
		t.Fatalf("SwitchCompanionHarness on parked conversation failed: %v", err)
	}
	if env.runner.isParked(parkedID) || env.runner.resident(parkedID) == nil {
		t.Fatalf("expected parked conversation %s to be unparked and resident after SwitchCompanionHarness", parkedID)
	}
	if after := env.diskRecord(parkedID); after.Backend != BackendCodex || after.WSKey != parkedKey {
		t.Fatalf("unexpected parked record after SwitchCompanionHarness: %+v", after)
	}
}

func TestSupportChoicesHarnessSwitchFlag(t *testing.T) {
	env := newSwitchTestEnv(t, nil)
	sup := NewSupport(SupportOptions{
		Runner: env.runner,
		Dir:    env.dir,
		Config: env.runner.opts.Config,
	})
	choices := sup.Choices(context.Background())
	byName := map[string]SupportHarness{}
	for _, h := range choices.Harnesses {
		byName[h.Harness] = h
	}
	for _, ext := range []string{BackendClaude, BackendCodex, BackendAgy} {
		h, ok := byName[ext]
		if !ok {
			t.Fatalf("missing harness %q in Choices", ext)
		}
		if !h.HarnessSwitch {
			t.Fatalf("expected harness_switch=true for %q, got false", ext)
		}
	}
	werkH, ok := byName[BackendWerk]
	if !ok {
		t.Fatalf("missing harness %q in Choices", BackendWerk)
	}
	if werkH.HarnessSwitch {
		t.Fatalf("expected harness_switch=false for %q, got true", BackendWerk)
	}
}

func TestHTTPHarnessSwitchRoute(t *testing.T) {
	env := newSwitchTestEnv(t, nil)
	lead := env.createLead(BackendClaude, LeadOptions{Model: "claude-opus-4-6"})
	worker, err := env.runner.Create("agent-worker-claude", env.root)
	if err != nil {
		t.Fatal(err)
	}

	// Untrusted non-local -> 404 for both lead and worker.
	for _, id := range []string{lead.ID, worker.ID} {
		if w := env.httpSwitch(id, HarnessSwitchSpec{Backend: BackendCodex}, false, false); w.Code != http.StatusNotFound {
			t.Fatalf("non-local %s status = %d, want 404", id, w.Code)
		}
		if w := env.httpSwitch(id, HarnessSwitchSpec{Backend: BackendCodex}, true, false); w.Code != http.StatusForbidden {
			t.Fatalf("cross-origin %s status = %d, want 403", id, w.Code)
		}
	}

	// Unknown id -> 404.
	if w := env.httpSwitch("missing-id", HarnessSwitchSpec{Backend: BackendCodex}, true, true); w.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", w.Code)
	}

	// Worker id -> 400 harness_unsupported.
	if w := env.httpSwitch(worker.ID, HarnessSwitchSpec{Backend: BackendCodex}, true, true); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "harness_unsupported") {
		t.Fatalf("worker id response = %d %q, want 400 harness_unsupported", w.Code, w.Body.String())
	}

	// Same backend -> 400 harness_unchanged.
	if w := env.httpSwitch(lead.ID, HarnessSwitchSpec{Backend: BackendClaude}, true, true); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "harness_unchanged") {
		t.Fatalf("unchanged response = %d %q, want 400 harness_unchanged", w.Code, w.Body.String())
	}

	// Busy lead -> 409 conversation_busy.
	c := env.runner.resident(lead.ID)
	c.do(func() { c.turn = true })
	if w := env.httpSwitch(lead.ID, HarnessSwitchSpec{Backend: BackendCodex}, true, true); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "conversation_busy") {
		t.Fatalf("busy response = %d %q, want 409 conversation_busy", w.Code, w.Body.String())
	}
	c.do(func() { c.turn = false })

	// Valid switch -> 200 OK with State view.
	w := env.httpSwitch(lead.ID, HarnessSwitchSpec{Backend: BackendCodex, Model: "gpt-5-codex", Effort: "low"}, true, true)
	if w.Code != http.StatusOK {
		t.Fatalf("valid switch status = %d body=%s", w.Code, w.Body.String())
	}
	var got State
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode State: %v", err)
	}
	if got.Backend != BackendCodex || got.Model != "gpt-5-codex" || got.Effort != "low" || !got.CanControl {
		t.Fatalf("unexpected State view: %+v", got)
	}

	// Closed lead -> 409 (endedError).
	if _, err := env.runner.CloseConversation(lead.ID); err != nil {
		t.Fatal(err)
	}
	if w := env.httpSwitch(lead.ID, HarnessSwitchSpec{Backend: BackendClaude}, true, true); w.Code != http.StatusConflict {
		t.Fatalf("closed lead status = %d body=%s, want 409", w.Code, w.Body.String())
	}
}
