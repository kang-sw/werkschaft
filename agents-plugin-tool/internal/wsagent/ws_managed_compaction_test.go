package wsagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wscompact"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

type wsCompactBackend struct {
	mu           sync.Mutex
	starts       []StartSpec
	procs        []*wsCompactProc
	failStartAt  int
	failAuditErr error
	logOrder     []string
}

func (b *wsCompactBackend) Start(_ context.Context, spec StartSpec) (Process, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	idx := len(b.starts) + 1
	if b.failStartAt > 0 && idx == b.failStartAt {
		return nil, errors.New("simulated start failure")
	}
	b.starts = append(b.starts, spec)
	b.logOrder = append(b.logOrder, fmt.Sprintf("start:%d:sid=%s", idx, spec.SessionID))
	p := &wsCompactProc{
		backend:  b,
		idx:      idx,
		holderID: fmt.Sprintf("holder-%d", idx),
		events:   make(chan Event, 64),
	}
	b.procs = append(b.procs, p)
	return p, nil
}

func (b *wsCompactBackend) latest() *wsCompactProc {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.procs) == 0 {
		return nil
	}
	return b.procs[len(b.procs)-1]
}

func (b *wsCompactBackend) snapshot() (starts []StartSpec, order []string, procs []*wsCompactProc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]StartSpec(nil), b.starts...), append([]string(nil), b.logOrder...), append([]*wsCompactProc(nil), b.procs...)
}

type wsCompactProc struct {
	backend  *wsCompactBackend
	idx      int
	holderID string
	events   chan Event

	mu            sync.Mutex
	sends         []string
	commands      []wsinbox.Command
	auditEvents   []wsaudit.Envelope
	auditIDs      []wsaudit.SourceID
	closed        bool
}

func (p *wsCompactProc) Events() <-chan Event { return p.events }
func (p *wsCompactProc) Send(text string) error {
	p.mu.Lock()
	p.sends = append(p.sends, text)
	p.mu.Unlock()
	return nil
}
func (p *wsCompactProc) RunCommand(cmd wsinbox.Command) error {
	p.mu.Lock()
	p.commands = append(p.commands, cmd)
	p.mu.Unlock()
	return nil
}
func (p *wsCompactProc) Interrupt() error         { return nil }
func (p *wsCompactProc) SetModel(string) error    { return nil }
func (p *wsCompactProc) SetEffort(string) error   { return nil }
func (p *wsCompactProc) Close() error {
	p.mu.Lock()
	already := p.closed
	p.closed = true
	p.mu.Unlock()
	if !already {
		p.backend.mu.Lock()
		p.backend.logOrder = append(p.backend.logOrder, fmt.Sprintf("close:%d", p.idx))
		p.backend.mu.Unlock()
		close(p.events)
	}
	return nil
}
func (p *wsCompactProc) HolderInfo() (string, int, bool) { return p.holderID, 1000 + p.idx, true }
func (p *wsCompactProc) ReplayUntil(uint64, uint64)      {}
func (p *wsCompactProc) StopError() error                { return nil }
func (p *wsCompactProc) Detach() bool                    { return false }
func (p *wsCompactProc) commitAuditCheckpoint(_ context.Context, id wsaudit.SourceID, ev wsaudit.Envelope) error {
	p.backend.mu.Lock()
	failErr := p.backend.failAuditErr
	p.backend.logOrder = append(p.backend.logOrder, fmt.Sprintf("audit:%d", p.idx))
	p.backend.mu.Unlock()
	if failErr != nil {
		return failErr
	}
	p.mu.Lock()
	p.auditIDs = append(p.auditIDs, id)
	p.auditEvents = append(p.auditEvents, ev)
	p.mu.Unlock()
	return nil
}

func (p *wsCompactProc) sent() ([]string, []wsinbox.Command) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.sends...), append([]wsinbox.Command(nil), p.commands...)
}

func (p *wsCompactProc) audits() ([]wsaudit.SourceID, []wsaudit.Envelope) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]wsaudit.SourceID(nil), p.auditIDs...), append([]wsaudit.Envelope(nil), p.auditEvents...)
}

func newWSCompactTestRunner(t *testing.T, root, backendName, leadMode string, restoreBundle func(string, string, string, RestoreCause, []AgentChild) (string, error)) (*Runner, *wsCompactBackend, string) {
	t.Helper()
	cfgOpts := wsconfig.Options{RepoRoot: root, CacheHome: t.TempDir(), ConfigHome: t.TempDir()}
	if leadMode != "" {
		r := wsconfig.NewResolver(cfgOpts, nil, nil, nil)
		if err := r.Set(wsconfig.ItemCompactionLeadMode+"."+backendName, leadMode, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
		if err := r.Set("compaction.lead_advisory."+backendName, "60%", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
		if err := r.Set("compaction.lead_hard."+backendName, "80%", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
	}
	NotifyCompactionConfigChanged()

	keys := &fakeLeadKeys{}
	b := &wsCompactBackend{}
	builtin := BuiltinBackendRegistrations()[backendName]
	reg := BackendRegistration{
		Backend:          b,
		Permission:       builtin.Permission,
		Lifecycle:        builtin.Lifecycle,
		Catalog:          builtin.Catalog,
		NativeCompaction: builtin.NativeCompaction,
		LeadProfile:      builtin.LeadProfile,
		PermissionModes:  builtin.PermissionModes,
		Profiles:         builtin.Profiles,
	}
	runner := NewRunner(Options{
		Dir:           t.TempDir(),
		Config:        cfgOpts,
		Keys:          &fakeKeys{},
		LeadKeys:      keys,
		Holders:       newFakeHolders(),
		Log:           os.Stderr,
		RestoreBundle: restoreBundle,
		Backends:      map[string]BackendRegistration{backendName: reg},
	})
	runner.Start(nil)
	t.Cleanup(runner.Close)

	st, err := runner.CreateLead(backendName, root, LeadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return runner, b, st.ID
}

func waitWSCompact(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func testLeadSummary(paths ...string) *wscompact.LeadSummary {
	return &wscompact.LeadSummary{
		Prose: []string{"concise", "tdd", "T0 only", "implementing phase 1", "run tests", "calm", "none"},
		Paths: paths,
	}
}

func TestWSManagedLeadRotationAuditOrderAndChildDedup(t *testing.T) {
	for _, backendName := range []string{BackendClaude, BackendCodex, BackendAgy} {
		t.Run(backendName, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("carry me forward\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			restoreFn := func(_, _, wsKey string, cause RestoreCause, _ []AgentChild) (string, error) {
				return fmt.Sprintf("## Hosted Session Restore\nkey=%s cause=%s\n\n## Direct Children\nnone\n", wsKey, cause), nil
			}
			runner, b, id := newWSCompactTestRunner(t, root, backendName, wsconfig.CompactionModeWS, restoreFn)

			if _, err := runner.Send(id, "hello world"); err != nil {
				t.Fatal(err)
			}
			waitWSCompact(t, "initial turn sent", func() bool {
				p := b.latest()
				if p == nil {
					return false
				}
				sends, _ := p.sent()
				return len(sends) == 1
			})
			p1 := b.latest()
			p1.events <- Event{SessionID: "native-sid-1"}
			p1.events <- Event{Live: &wslive.Event{Kind: wslive.KindReply, Final: true, Text: "initial reply"}}
			p1.events <- Event{TurnEnd: true}
			waitWSCompact(t, "idle with SessionID recorded", func() bool {
				rec, ok := runner.recordOf(id)
				st, err := runner.Get(id)
				return ok && rec.SessionID == "native-sid-1" && err == nil && st.Status == StatusIdle
			})

			recBefore, _ := runner.recordOf(id)
			res := runner.CompactWithSummary(recBefore.WSKey, "", nil, testLeadSummary("notes.txt"))
			if res.Status != "queued" {
				t.Fatalf("CompactWithSummary = %+v, want queued", res)
			}

			waitWSCompact(t, "rotated session started and continued", func() bool {
				starts, _, procs := b.snapshot()
				if len(starts) != 2 || len(procs) != 2 {
					return false
				}
				sends, _ := procs[1].sent()
				return len(sends) >= 1
			})

			starts, order, procs := b.snapshot()
			if starts[1].SessionID != "" {
				t.Fatalf("rotated StartSpec.SessionID = %q, want empty (fresh native session)", starts[1].SessionID)
			}
			// Verify audit commit happened BEFORE old process Close and BEFORE new Start.
			auditIdx, closeIdx, start2Idx := -1, -1, -1
			for i, step := range order {
				switch {
				case step == "audit:1":
					auditIdx = i
				case step == "close:1":
					closeIdx = i
				case strings.HasPrefix(step, "start:2:"):
					start2Idx = i
				}
			}
			if !(auditIdx >= 0 && closeIdx > auditIdx && start2Idx > closeIdx) {
				t.Fatalf("unexpected rotation ordering %v (want audit:1 < close:1 < start:2)", order)
			}

			// Verify explicit non-estimated wsaudit.KindCompaction on old process.
			ids, evs := procs[0].audits()
			if len(evs) != 1 || evs[0].Kind != wsaudit.KindCompaction {
				t.Fatalf("audit events on old proc = %+v, want 1 KindCompaction", evs)
			}
			if _, est := evs[0].Meta[wsaudit.MetaEstimated]; est {
				t.Fatalf("ws-managed compaction audit must not be marked estimated: %+v", evs[0].Meta)
			}
			if ids[0].Source == "" || ids[0].Seq == 0 {
				t.Fatalf("invalid audit SourceID: %+v", ids[0])
			}

			// Verify continuation prompt carries RestoreBundle + checkpoint + reread, without duplicate children sections.
			sends2, _ := procs[1].sent()
			prompt := sends2[0]
			for _, want := range []string{
				"## Hosted Session Restore",
				"implementing phase 1",
				"carry me forward",
				"## Exec jobs",
				"Continue the work that was pending.",
			} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("continuation prompt missing %q:\n%s", want, prompt)
				}
			}
			if strings.Contains(prompt, "## Child agents in flight") || strings.Contains(prompt, "## Child agents finished") {
				t.Fatalf("expected checkpoint children sections omitted when RestoreBundle is present:\n%s", prompt)
			}

			// Verify identity preserved and compaction counter incremented.
			recAfter, _ := runner.recordOf(id)
			if recAfter.ReplyID != recBefore.ReplyID || recAfter.WSKey != recBefore.WSKey {
				t.Fatalf("session identity drifted: before=(%s,%s) after=(%s,%s)", recBefore.ReplyID, recBefore.WSKey, recAfter.ReplyID, recAfter.WSKey)
			}
			st, err := runner.Get(id)
			if err != nil || st.Compactions != 1 || st.CompactOrigin != wslive.TriggerManual {
				t.Fatalf("post-rotation state = (compactions=%d, origin=%q, err=%v), want (1, manual, nil)", st.Compactions, st.CompactOrigin, err)
			}
		})
	}
}

func TestWSManagedRotationFailurePreservesSessionIdentity(t *testing.T) {
	root := t.TempDir()
	runner, b, id := newWSCompactTestRunner(t, root, BackendClaude, wsconfig.CompactionModeWS, nil)
	if _, err := runner.Send(id, "hi"); err != nil {
		t.Fatal(err)
	}
	waitWSCompact(t, "turn 1 sent", func() bool {
		p := b.latest()
		if p == nil {
			return false
		}
		sends, _ := p.sent()
		return len(sends) == 1
	})
	p1 := b.latest()
	p1.events <- Event{SessionID: "keep-sid"}
	p1.events <- Event{TurnEnd: true}
	waitWSCompact(t, "idle with sid", func() bool {
		rec, ok := runner.recordOf(id)
		st, err := runner.Get(id)
		return ok && rec.SessionID == "keep-sid" && err == nil && st.Status == StatusIdle
	})

	// 1. Audit commit failure aborts cleanly before closing old proc or clearing SessionID.
	b.mu.Lock()
	b.failAuditErr = errors.New("audit disk full")
	b.mu.Unlock()
	rec, _ := runner.recordOf(id)
	res := runner.CompactWithSummary(rec.WSKey, "", nil, testLeadSummary())
	if res.Status != "error" {
		t.Fatalf("expected error on audit commit failure, got %+v", res)
	}
	recAfter, _ := runner.recordOf(id)
	st, _ := runner.Get(id)
	if recAfter.SessionID != "keep-sid" || st.Compactions != 0 {
		t.Fatalf("audit failure corrupted state: sid=%q compactions=%d", recAfter.SessionID, st.Compactions)
	}

	// 2. startLead failure restores previous SessionID and does not increment Compactions.
	b.mu.Lock()
	b.failAuditErr = nil
	b.failStartAt = 2
	b.mu.Unlock()
	res = runner.CompactWithSummary(rec.WSKey, "", nil, testLeadSummary())
	if res.Status != "error" {
		t.Fatalf("expected error on startLead failure, got %+v", res)
	}
	recAfter, _ = runner.recordOf(id)
	st, _ = runner.Get(id)
	if recAfter.SessionID != "keep-sid" || st.Compactions != 0 {
		t.Fatalf("startLead failure corrupted state: sid=%q compactions=%d", recAfter.SessionID, st.Compactions)
	}
}

func TestDirectNoArgCompactBypassesStage1Steer(t *testing.T) {
	root := t.TempDir()
	runner, b, id := newWSCompactTestRunner(t, root, BackendClaude, wsconfig.CompactionModeWS, nil)
	if _, err := runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	waitWSCompact(t, "turn 1 sent", func() bool {
		p := b.latest()
		if p == nil {
			return false
		}
		sends, _ := p.sent()
		return len(sends) == 1
	})
	p1 := b.latest()
	p1.events <- Event{TurnEnd: true}
	waitWSCompact(t, "idle", func() bool {
		st, err := runner.Get(id)
		return err == nil && st.Status == StatusIdle
	})

	rec, _ := runner.recordOf(id)
	res := runner.Compact(rec.WSKey, "", nil)
	if res.Status != "queued" {
		t.Fatalf("Compact() = %+v, want queued", res)
	}
	waitWSCompact(t, "native /compact dispatched directly", func() bool {
		sends, cmds := p1.sent()
		return len(cmds) == 1 && cmds[0].Name == "compact" && len(sends) == 1
	})
}

func TestTwoStageFallbackHardThresholdAndUsageLimitSuppression(t *testing.T) {
	t.Run("lead_answers_with_summary", func(t *testing.T) {
		root := t.TempDir()
		runner, b, id := newWSCompactTestRunner(t, root, BackendClaude, wsconfig.CompactionModeWS, nil)
		if _, err := runner.Send(id, "work"); err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "turn 1", func() bool {
			p := b.latest()
			if p == nil {
				return false
			}
			sends, _ := p.sent()
			return len(sends) == 1
		})
		p1 := b.latest()
		tokens := int64(85000)
		p1.events <- Event{Live: &wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &tokens, Window: 100000}}}
		p1.events <- Event{TurnEnd: true}

		waitWSCompact(t, "stage 1 steer sent", func() bool {
			sends, _ := p1.sent()
			return len(sends) == 2
		})
		sends, cmds := p1.sent()
		if len(cmds) != 0 {
			t.Fatalf("unexpected native command before Stage 1 completes: %+v", cmds)
		}
		for _, field := range wscompact.ProseFields() {
			if !strings.Contains(sends[1], field.Key) {
				t.Fatalf("Stage 1 steer missing field %q:\n%s", field.Key, sends[1])
			}
		}
		if !strings.Contains(sends[1], "reread") {
			t.Fatalf("Stage 1 steer missing reread:\n%s", sends[1])
		}

		// Lead calls ws/compact(lead_summary) during Stage 1, then ends turn.
		rec, _ := runner.recordOf(id)
		res := runner.CompactWithSummary(rec.WSKey, "", nil, testLeadSummary())
		if res.Status != "queued" {
			t.Fatalf("CompactWithSummary during Stage 1 = %+v", res)
		}
		p1.events <- Event{TurnEnd: true}

		waitWSCompact(t, "rotated to process 2", func() bool {
			starts, _, _ := b.snapshot()
			return len(starts) == 2
		})
		st, _ := runner.Get(id)
		if st.Compactions != 1 || st.CompactOrigin != wslive.TriggerAuto {
			t.Fatalf("expected auto compaction count=1, got (%d, %q)", st.Compactions, st.CompactOrigin)
		}
	})

	t.Run("lead_misses_falls_back_to_native", func(t *testing.T) {
		root := t.TempDir()
		runner, b, id := newWSCompactTestRunner(t, root, BackendClaude, wsconfig.CompactionModeWS, nil)
		if _, err := runner.Send(id, "work"); err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "turn 1", func() bool {
			p := b.latest()
			if p == nil {
				return false
			}
			sends, _ := p.sent()
			return len(sends) == 1
		})
		p1 := b.latest()
		tokens := int64(85000)
		p1.events <- Event{Live: &wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &tokens, Window: 100000}}}
		p1.events <- Event{TurnEnd: true}
		waitWSCompact(t, "stage 1 steer sent", func() bool {
			sends, _ := p1.sent()
			return len(sends) == 2
		})
		// Stage 1 ends without calling ws/compact -> Stage 2 native /compact runs.
		p1.events <- Event{TurnEnd: true}
		waitWSCompact(t, "stage 2 native /compact sent", func() bool {
			_, cmds := p1.sent()
			return len(cmds) == 1 && cmds[0].Name == "compact"
		})
	})

	t.Run("usage_limit_suppresses_stage2", func(t *testing.T) {
		root := t.TempDir()
		runner, b, id := newWSCompactTestRunner(t, root, BackendClaude, wsconfig.CompactionModeWS, nil)
		if _, err := runner.Send(id, "work"); err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "turn 1", func() bool {
			p := b.latest()
			if p == nil {
				return false
			}
			sends, _ := p.sent()
			return len(sends) == 1
		})
		p1 := b.latest()
		tokens := int64(85000)
		p1.events <- Event{Live: &wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &tokens, Window: 100000}}}
		p1.events <- Event{TurnEnd: true}
		waitWSCompact(t, "stage 1 steer sent", func() bool {
			sends, _ := p1.sent()
			return len(sends) == 2
		})
		// Stage 1 ends with NoticeUsageLimit -> Stage 2 MUST be suppressed.
		p1.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit}}
		p1.events <- Event{TurnEnd: true, TurnError: "quota exhausted"}
		waitWSCompact(t, "idle after usage-limited stage 1", func() bool {
			st, err := runner.Get(id)
			return err == nil && st.Status == StatusIdle
		})
		_, cmds := p1.sent()
		if len(cmds) != 0 {
			t.Fatalf("Stage 2 native /compact should be suppressed on NoticeUsageLimit, got cmds=%+v", cmds)
		}
	})
}

func TestTwoStageFallbackDashboardSlashCompact(t *testing.T) {
	root := t.TempDir()
	runner, b, id := newWSCompactTestRunner(t, root, BackendClaude, wsconfig.CompactionModeWS, nil)
	if _, err := runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	waitWSCompact(t, "turn 1", func() bool {
		p := b.latest()
		if p == nil {
			return false
		}
		sends, _ := p.sent()
		return len(sends) == 1
	})
	p1 := b.latest()
	p1.events <- Event{TurnEnd: true}
	waitWSCompact(t, "idle", func() bool {
		st, err := runner.Get(id)
		return err == nil && st.Status == StatusIdle
	})

	// Dashboard /compact is intercepted in kickInput() -> Stage 1 steer (not direct RunCommand).
	c := runner.conv(id)
	if _, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Command: &wsinbox.Command{Name: "compact"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	c.signalInbox()
	waitWSCompact(t, "stage 1 steer sent for dashboard /compact", func() bool {
		sends, _ := p1.sent()
		return len(sends) == 2
	})
	_, cmds := p1.sent()
	if len(cmds) != 0 {
		t.Fatalf("dashboard /compact bypassed Stage 1 intercept: %+v", cmds)
	}
	// Lead misses Stage 1 -> Stage 2 native /compact fallback runs.
	p1.events <- Event{TurnEnd: true}
	waitWSCompact(t, "stage 2 fallback after dashboard /compact miss", func() bool {
		_, cmds := p1.sent()
		return len(cmds) == 1 && cmds[0].Name == "compact"
	})
}
