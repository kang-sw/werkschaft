package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsauditstore"
	"github.com/kang-sw/devenv/internal/wscompact"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
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

	mu           sync.Mutex
	sends        []string
	commands     []wsinbox.Command
	auditEvents  []wsaudit.Envelope
	auditIDs     []wsaudit.SourceID
	closed       bool
	failSend     bool
	failCommand  bool
	mcpReconnect func()
}

func (p *wsCompactProc) Events() <-chan Event { return p.events }
func (p *wsCompactProc) Send(text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failSend {
		p.failSend = false
		return errors.New("simulated send failure")
	}
	p.sends = append(p.sends, text)
	return nil
}
func (p *wsCompactProc) RunCommand(cmd wsinbox.Command) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commands = append(p.commands, cmd)
	if p.failCommand {
		return errors.New("simulated command failure")
	}
	return nil
}
func (p *wsCompactProc) MCPStatus() ([]MCPServer, error) {
	return []MCPServer{{Name: "ws", Status: MCPConnected}}, nil
}

func (p *wsCompactProc) ReconnectMCP(string) error {
	p.mu.Lock()
	reconnect := p.mcpReconnect
	p.mu.Unlock()
	if reconnect != nil {
		reconnect()
	}
	return nil
}

func (p *wsCompactProc) Interrupt() error       { return nil }
func (p *wsCompactProc) SetModel(string) error  { return nil }
func (p *wsCompactProc) SetEffort(string) error { return nil }
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

// A lead's own multi-KB Korean prose must reach the replacement session whole:
// the checkpoint builder must not re-truncate it to the old 2 KiB field cap.
func TestCompactLargeKoreanLeadSummaryCarriedWhole(t *testing.T) {
	root := t.TempDir()
	restoreFn := func(_, _, wsKey string, cause RestoreCause, _ []AgentChild) (string, error) {
		return fmt.Sprintf("## Hosted Session Restore\nkey=%s cause=%s\n", wsKey, cause), nil
	}
	runner, b, id := newWSCompactTestRunner(t, root, BackendClaude, wsconfig.CompactionModeWS, restoreFn)
	if _, err := runner.Send(id, "hello"); err != nil {
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
	p1.events <- Event{TurnEnd: true}
	waitWSCompact(t, "idle", func() bool {
		st, err := runner.Get(id)
		return err == nil && st.Status == StatusIdle
	})

	big := strings.Repeat("긴 한국어 요약입니다. ", 700) // ~12 KiB, 6x the old per-field cap
	lead := testLeadSummary()
	lead.Prose[3] = big
	lead.Prose[2] = big + "끝"
	rec, _ := runner.recordOf(id)
	if res := runner.CompactWithSummary(rec.WSKey, "", nil, lead); res.Status != "queued" {
		t.Fatalf("CompactWithSummary = %+v", res)
	}
	waitWSCompact(t, "rotated session continued", func() bool {
		_, _, procs := b.snapshot()
		if len(procs) != 2 {
			return false
		}
		sends, _ := procs[1].sent()
		return len(sends) >= 1
	})
	_, _, procs := b.snapshot()
	sends, _ := procs[1].sent()
	prompt := sends[0]
	if !strings.Contains(prompt, "### Current work\n"+strings.TrimSpace(big)+"\n") || !strings.Contains(prompt, big+"끝") {
		t.Fatalf("lead prose truncated in the continuation prompt (%d bytes)", len(prompt))
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
			// Only a successfully started replacement can establish the cut.
			auditIdx, closeIdx, start2Idx := -1, -1, -1
			for i, step := range order {
				switch {
				case step == "audit:2":
					auditIdx = i
				case step == "close:1":
					closeIdx = i
				case strings.HasPrefix(step, "start:2:"):
					start2Idx = i
				}
			}
			if !(closeIdx >= 0 && start2Idx > closeIdx && auditIdx > start2Idx) {
				t.Fatalf("unexpected rotation ordering %v (want close:1 < start:2 < audit:2)", order)
			}
			if retained, err := ReadRecord(runner.opts.Dir, id); err != nil || retained.SessionID != "" || retained.SessionFile != "" {
				t.Fatalf("durable native binding after committed rotation = (%q, %q), err=%v; want cleared until replacement reports ID", retained.SessionID, retained.SessionFile, err)
			}
			procs[1].events <- Event{SessionID: "native-sid-2"}
			waitWSCompact(t, "replacement's persisted native ID", func() bool {
				retained, err := ReadRecord(runner.opts.Dir, id)
				return err == nil && retained.SessionID == "native-sid-2" && retained.WSKey == recBefore.WSKey
			})

			ids, evs := procs[1].audits()
			if len(evs) != 1 || evs[0].Kind != wsaudit.KindCompaction {
				t.Fatalf("audit events on replacement = %+v, want 1 KindCompaction", evs)
			}
			if _, est := evs[0].Meta[wsaudit.MetaEstimated]; est {
				t.Fatalf("ws-managed compaction audit must not be marked estimated: %+v", evs[0].Meta)
			}
			if ids[0].Source == "" || ids[0].Seq == 0 {
				t.Fatalf("invalid audit SourceID: %+v", ids[0])
			}

			// Verify continuation prompt carries RestoreBundle + XML-framed checkpoint + reread,
			// with continuation instruction cleanly outside </compaction-checkpoint>, without duplicate children sections.
			sends2, _ := procs[1].sent()
			prompt := sends2[0]
			for _, want := range []string{
				"## Hosted Session Restore",
				`<compaction-checkpoint role="lead">`,
				"<lead-summary>\n## Carried forward by the lead",
				"### Current work\nimplementing phase 1",
				"</lead-summary>",
				"<exec-jobs>\n## Exec jobs",
				"<rereads>\n## Required re-reads (inlined)\n<file path=\"notes.txt\">\ncarry me forward\n</file>\n</rereads>",
				"<dialog>\n<message role=\"user\" timestamp=",
				"hello world",
				"<message role=\"assistant\" timestamp=",
				"\ninitial reply\n</message>\n</dialog>\n</compaction-checkpoint>\n\nContinue the work that was pending.",
			} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("continuation prompt missing %q:\n%s", want, prompt)
				}
			}
			if strings.Contains(prompt, "<children>") || strings.Contains(prompt, "## Child agents in flight") || strings.Contains(prompt, "## Child agents finished") {
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

	b.mu.Lock()
	b.failStartAt = 2
	b.mu.Unlock()
	rec, _ := runner.recordOf(id)
	res := runner.CompactWithSummary(rec.WSKey, "", nil, testLeadSummary())
	if res.Status != "error" || !strings.Contains(res.Reason, "start fresh session") {
		t.Fatalf("expected error on startLead failure, got %+v", res)
	}
	if _, events := p1.audits(); wsaudit.FindModelSeenCut(events).HasCompaction {
		t.Fatalf("failed launch left an audit cut: %+v", events)
	}
	recAfter, err := ReadRecord(runner.opts.Dir, id)
	st, _ := runner.Get(id)
	if err != nil || recAfter.SessionID != "keep-sid" || st.Compactions != 0 {
		t.Fatalf("startLead failure corrupted state: sid=%q compactions=%d err=%v", recAfter.SessionID, st.Compactions, err)
	}

	b.mu.Lock()
	b.failStartAt = 0
	b.failAuditErr = errors.New("audit disk full")
	b.mu.Unlock()
	if _, err := runner.Send(id, "wake after failed rotation"); err != nil {
		t.Fatal(err)
	}
	waitWSCompact(t, "prompt sent on the resumed native session", func() bool {
		starts, _, procs := b.snapshot()
		if len(starts) != 2 || starts[1].SessionID != "keep-sid" {
			return false
		}
		sends, _ := procs[1].sent()
		return len(sends) == 1
	})
	b.latest().events <- Event{TurnEnd: true}
	waitWSCompact(t, "idle on previous native session", func() bool {
		st, err := runner.Get(id)
		return err == nil && st.Status == StatusIdle
	})
	res = runner.CompactWithSummary(rec.WSKey, "", nil, testLeadSummary())
	if res.Status != "error" || !strings.Contains(res.Reason, "audit disk full") {
		t.Fatalf("expected error on audit commit failure, got %+v", res)
	}
	recAfter, err = ReadRecord(runner.opts.Dir, id)
	st, _ = runner.Get(id)
	if err != nil || recAfter.SessionID != "keep-sid" || st.Compactions != 0 {
		t.Fatalf("audit failure corrupted state: sid=%q compactions=%d err=%v", recAfter.SessionID, st.Compactions, err)
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
			st, err := runner.Get(id)
			return len(starts) == 2 && err == nil && st.Compactions == 1
		})
		st, _ := runner.Get(id)
		if st.Compactions != 1 || st.CompactOrigin != wslive.TriggerAuto {
			t.Fatalf("expected auto compaction count=1, got (%d, %q)", st.Compactions, st.CompactOrigin)
		}

		// Verify the post-rotation continuation turn is sent with compactInternalTurn == false
		// (Stage 1's internal flag must NOT leak onto the resumed continuation turn) and its
		// reply is preserved in the ring rather than dropped as internal.
		p2 := b.latest()
		waitWSCompact(t, "post-rotation continuation turn sent", func() bool {
			sends2, _ := p2.sent()
			return len(sends2) == 1
		})
		runner.mu.Lock()
		c := runner.convs[id]
		runner.mu.Unlock()
		var internalDuring bool
		var dir string
		c.do(func() {
			internalDuring = c.compactInternalTurn
			dir = c.dir
		})
		if internalDuring {
			t.Fatal("compactInternalTurn leaked true onto post-rotation continuation turn")
		}
		p2.events <- Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: "resumed work cleanly"}}
		p2.events <- Event{TurnEnd: true}
		waitWSCompact(t, "continuation turn reply written to ring", func() bool {
			recs, err := wslive.ReadRing(filepath.Join(dir, "ring"))
			if err != nil {
				return false
			}
			for _, r := range recs {
				if r.Kind == wslive.RingReply && strings.Contains(r.Text, "resumed work cleanly") {
					return true
				}
			}
			return false
		})
		var internalAfter bool
		c.do(func() {
			internalAfter = c.compactInternalTurn
		})
		if internalAfter {
			t.Fatal("compactInternalTurn remained true after post-rotation continuation turn")
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

	t.Run("usage_limit_auto_quota_continuation_suppresses_stage2_and_resumes", func(t *testing.T) {
		root := t.TempDir()
		cfgOpts := wsconfig.Options{RepoRoot: root, CacheHome: t.TempDir(), ConfigHome: t.TempDir()}
		r := wsconfig.NewResolver(cfgOpts, nil, nil, nil)
		for k, v := range map[string]string{
			wsconfig.ItemCompactionLeadMode + "." + BackendClaude:     wsconfig.CompactionModeWS,
			"compaction.lead_advisory." + BackendClaude:               "60%",
			"compaction.lead_hard." + BackendClaude:                   "80%",
			wsconfig.ItemAgentQuotaContinuation + "." + BackendClaude: "auto",
		} {
			if err := r.Set(k, v, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
				t.Fatal(err)
			}
		}
		NotifyCompactionConfigChanged()

		var timerMu sync.Mutex
		var quotaFire chan time.Time
		now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
		b := &wsCompactBackend{}
		builtin := BuiltinBackendRegistrations()[BackendClaude]
		runner := NewRunner(Options{
			Dir:      t.TempDir(),
			Config:   cfgOpts,
			Keys:     &fakeKeys{},
			LeadKeys: &fakeLeadKeys{},
			Holders:  newFakeHolders(),
			Log:      os.Stderr,
			Now:      func() time.Time { return now },
			Timer: func(time.Duration) (<-chan time.Time, func()) {
				ch := make(chan time.Time, 1)
				timerMu.Lock()
				quotaFire = ch
				timerMu.Unlock()
				return ch, func() {}
			},
			QuotaContinuationJitter: func(time.Duration) time.Duration { return 0 },
			Backends: map[string]BackendRegistration{
				BackendClaude: {
					Backend:          b,
					Permission:       builtin.Permission,
					Lifecycle:        builtin.Lifecycle,
					Catalog:          builtin.Catalog,
					NativeCompaction: builtin.NativeCompaction,
					LeadProfile:      builtin.LeadProfile,
					PermissionModes:  builtin.PermissionModes,
					Profiles:         builtin.Profiles,
				},
			},
		})
		runner.Start(nil)
		t.Cleanup(runner.Close)

		st, err := runner.CreateLead(BackendClaude, root, LeadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		id := st.ID
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

		// The timer becomes due while Stage 1 still owns the conversation.
		fired := make(chan struct{})
		c := runner.conv(id)
		c.do(func() { c.onQuotaAfterFence = func() { close(fired) } })
		resetsAt := now.Add(5 * time.Minute).UnixMilli()
		p1.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, ResetsAt: resetsAt, Text: "limit reached"}}
		var ch chan time.Time
		waitWSCompact(t, "Stage 1 quota timer armed", func() bool {
			timerMu.Lock()
			ch = quotaFire
			timerMu.Unlock()
			return ch != nil
		})
		ch <- now.Add(6 * time.Minute)
		select {
		case <-fired:
		case <-time.After(3 * time.Second):
			t.Fatal("quota timer did not fire during Stage 1")
		}
		c.do(func() { c.onQuotaAfterFence = nil })
		if sends, cmds := p1.sent(); len(sends) != 2 || len(cmds) != 0 {
			t.Fatalf("Stage 1 still owns the turn: sends=%+v commands=%+v", sends, cmds)
		}

		p1.events <- Event{TurnEnd: true, TurnError: "quota exhausted"}
		waitWSCompact(t, "quota continuation prompt dispatched", func() bool {
			sends, _ := p1.sent()
			return len(sends) == 3 && strings.Contains(sends[2], "usage limit was reached")
		})
		_, cmds := p1.sent()
		if len(cmds) != 0 {
			t.Fatalf("unexpected native /compact command after quota continuation resume: %+v", cmds)
		}
	})
}

func TestDashboardCompactFailureDeliversQueuedPrompt(t *testing.T) {
	runner, b, id := newWSCompactTestRunner(t, t.TempDir(), BackendClaude, wsconfig.CompactionModeWS, nil)
	if _, err := runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	waitWSCompact(t, "first prompt", func() bool {
		p := b.latest()
		if p == nil {
			return false
		}
		sends, _ := p.sent()
		return len(sends) == 1
	})
	p := b.latest()
	p.mu.Lock()
	p.failSend, p.failCommand = true, true
	p.mu.Unlock()

	// Queue both inputs during the first turn so one idle drain sees both.
	c := runner.conv(id)
	if _, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Command: &wsinbox.Command{Name: "compact"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Send(id, "continue after failed compact"); err != nil {
		t.Fatal(err)
	}
	p.events <- Event{TurnEnd: true}

	waitWSCompact(t, "queued prompt after synchronous failures", func() bool {
		sends, commands := p.sent()
		return len(commands) == 1 && commands[0].Name == "compact" &&
			len(sends) == 2 && strings.Contains(sends[1], "continue after failed compact")
	})
}

func TestHostedLeadModeCacheAndStage1AfterToggle(t *testing.T) {
	runner, b, id := newWSCompactTestRunner(t, t.TempDir(), BackendClaude, wsconfig.CompactionModeNative, nil)
	if mode := runner.LeadCompactionMode(id); mode != wsconfig.CompactionModeNative {
		t.Fatalf("initial mode = %q", mode)
	}

	resolver := wsconfig.NewResolver(runner.opts.Config, nil, nil, nil)
	change := func(mode string) {
		t.Helper()
		if err := resolver.Set(wsconfig.ItemCompactionLeadMode+"."+BackendClaude, mode, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runner.Send(id, "work started in native mode"); err != nil {
		t.Fatal(err)
	}
	waitWSCompact(t, "first prompt", func() bool {
		p := b.latest()
		if p == nil {
			return false
		}
		sends, _ := p.sent()
		return len(sends) == 1
	})
	p := b.latest()

	change(wsconfig.CompactionModeWS)
	if mode := runner.LeadCompactionMode(id); mode != wsconfig.CompactionModeNative {
		t.Fatalf("read bypassed cached native mode: %q", mode)
	}
	NotifyCompactionConfigChanged()
	if mode := runner.LeadCompactionMode(id); mode != wsconfig.CompactionModeWS {
		t.Fatalf("native-to-ws toggle: %q", mode)
	}
	c := runner.conv(id)
	if _, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Command: &wsinbox.Command{Name: "compact"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	c.signalInbox()
	p.events <- Event{TurnEnd: true}
	waitWSCompact(t, "Stage 1 after toggle", func() bool {
		sends, _ := p.sent()
		return len(sends) == 2 && sends[1] == stage1CompactionPrompt()
	})
	resume := false
	if result := runner.CompactWithSummary(c.record().WSKey, "", &resume, testLeadSummary()); result.Status != "queued" {
		t.Fatalf("Stage 1 summary: %+v", result)
	}
	p.events <- Event{TurnEnd: true}
	waitWSCompact(t, "successful Stage 1 rotation", func() bool {
		st, err := runner.Get(id)
		return err == nil && st.Compactions == 1 && b.latest() != p
	})

	change(wsconfig.CompactionModeNative)
	if mode := runner.LeadCompactionMode(id); mode != wsconfig.CompactionModeWS {
		t.Fatalf("read bypassed cached ws mode: %q", mode)
	}
	NotifyCompactionConfigChanged()
	if mode := runner.LeadCompactionMode(id); mode != wsconfig.CompactionModeNative {
		t.Fatalf("ws-to-native toggle: %q", mode)
	}
}

func TestRotationResetsInFlightMCPCheck(t *testing.T) {
	runner, b, id := newWSCompactTestRunner(t, t.TempDir(), BackendClaude, wsconfig.CompactionModeWS, nil)
	if _, err := runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	waitWSCompact(t, "first prompt", func() bool {
		p := b.latest()
		if p == nil {
			return false
		}
		sends, _ := p.sent()
		return len(sends) == 1
	})
	old := b.latest()
	old.events <- Event{TurnEnd: true}
	waitWSCompact(t, "idle", func() bool {
		st, err := runner.Get(id)
		return err == nil && st.Status == StatusIdle
	})

	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	old.mu.Lock()
	old.mcpReconnect = func() {
		close(started)
		<-release
	}
	old.mu.Unlock()
	c := runner.conv(id)
	c.do(func() { c.checkMCP([]string{"ws"}) })
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("old process did not start its MCP check")
	}

	resume := false
	if result := runner.CompactWithSummary(c.record().WSKey, "", &resume, testLeadSummary()); result.Status != "queued" {
		t.Fatalf("rotation: %+v", result)
	}
	replacement := b.latest()
	if replacement == old {
		t.Fatal("replacement process did not start")
	}
	newStarted := make(chan struct{})
	replacement.mu.Lock()
	replacement.mcpReconnect = func() { close(newStarted) }
	replacement.mu.Unlock()
	c.do(func() {
		c.mcpChecked(old, nil, map[string]bool{"ws": false}, nil)
		c.checkMCP([]string{"ws"})
	})
	select {
	case <-newStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement MCP check waited behind the retired process")
	}
}

func TestMCPRecoveryWaitsForAcceptedCompaction(t *testing.T) {
	for _, withSummary := range []bool{false, true} {
		t.Run(fmt.Sprintf("summary=%t", withSummary), func(t *testing.T) {
			runner, b, id := newWSCompactTestRunner(t, t.TempDir(), BackendClaude, wsconfig.CompactionModeWS, nil)
			if _, err := runner.Send(id, "work"); err != nil {
				t.Fatal(err)
			}
			waitWSCompact(t, "first prompt", func() bool {
				p := b.latest()
				if p == nil {
					return false
				}
				sends, _ := p.sent()
				return len(sends) == 1
			})
			p := b.latest()
			c := runner.conv(id)
			if _, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Command: &wsinbox.Command{Name: "compact"}}, time.Now()); err != nil {
				t.Fatal(err)
			}
			c.signalInbox()
			p.events <- Event{TurnEnd: true}
			waitWSCompact(t, "Stage 1 prompt", func() bool {
				sends, _ := p.sent()
				return len(sends) == 2 && sends[1] == stage1CompactionPrompt()
			})
			c.do(func() { c.mcpChecked(p, nil, map[string]bool{"ws": false}, nil) })
			if withSummary {
				resume := false
				if result := runner.CompactWithSummary(c.record().WSKey, "", &resume, testLeadSummary()); result.Status != "queued" {
					t.Fatalf("Stage 1 summary: %+v", result)
				}
			}
			p.events <- Event{TurnEnd: true}
			if withSummary {
				waitWSCompact(t, "summary rotation", func() bool {
					state, err := runner.Get(id)
					return err == nil && state.Compactions == 1 && b.latest() != p
				})
			} else {
				waitWSCompact(t, "Stage 2 dispatch on the original process", func() bool {
					_, commands := p.sent()
					return len(commands) == 1 && commands[0].Name == "compact"
				})
			}
		})
	}
}

func TestStage1NativeCutSatisfiesDashboardCompact(t *testing.T) {
	runner, b, id := newWSCompactTestRunner(t, t.TempDir(), BackendClaude, wsconfig.CompactionModeWS, nil)
	if _, err := runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	waitWSCompact(t, "first prompt", func() bool {
		p := b.latest()
		if p == nil {
			return false
		}
		sends, _ := p.sent()
		return len(sends) == 1
	})
	p := b.latest()
	c := runner.conv(id)
	if _, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Command: &wsinbox.Command{Name: "compact"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	c.signalInbox()
	p.events <- Event{TurnEnd: true}
	waitWSCompact(t, "Stage 1 prompt", func() bool {
		sends, _ := p.sent()
		return len(sends) == 2 && sends[1] == stage1CompactionPrompt()
	})

	p.events <- Event{Live: &wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactStart}}
	p.events <- Event{Live: &wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd}}
	p.events <- Event{TurnEnd: true}
	if _, err := runner.Send(id, "work after native cut"); err != nil {
		t.Fatal(err)
	}
	waitWSCompact(t, "prompt after the native cut", func() bool {
		sends, _ := p.sent()
		return len(sends) == 3 && strings.Contains(sends[2], "work after native cut")
	})
	_, commands := p.sent()
	st, err := runner.Get(id)
	if len(commands) != 0 || err != nil || st.Compactions != 1 {
		t.Fatalf("native cut must satisfy Stage 1: commands=%v state=%+v err=%v", commands, st, err)
	}
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

func TestPendingCompactionT0RecordUnknownFieldRoundTrip(t *testing.T) {
	zeroSends := uint64(0)
	rec := Record{
		ReplyID: "lead-pending-compact",
		WSKey:   "ws-key-pending-compact",
		Lead:    true,
		Backend: BackendClaude,
		Model:   "sonnet",
		PendingCompaction: &PendingCompaction{
			Note:              "## Checkpoint\nimplementing phase 1\n",
			RestoreBundle:     true,
			Resume:            true,
			ResumeInternal:    true,
			Origin:            wslive.TriggerManual,
			RotatedAfterSends: &zeroSends,
		},
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an older daemon reading into a Record-shaped unknown-field map
	// (record.go:236-289), mutating an unrelated field, and rewriting the JSON.
	var oldMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &oldMap); err != nil {
		t.Fatal(err)
	}
	if _, ok := oldMap["pending_compaction"]; !ok {
		t.Fatalf("marshaled record missing pending_compaction: %s", raw)
	}
	oldMap["model"] = json.RawMessage(`"opus"`)
	rewritten, err := json.Marshal(oldMap)
	if err != nil {
		t.Fatal(err)
	}

	var reopened Record
	if err := json.Unmarshal(rewritten, &reopened); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reopened.PendingCompaction, rec.PendingCompaction) {
		t.Fatalf("reopened PendingCompaction = %+v, want %+v", reopened.PendingCompaction, rec.PendingCompaction)
	}
	if reopened.PendingCompaction.RotatedAfterSends == nil || *reopened.PendingCompaction.RotatedAfterSends != 0 {
		t.Fatalf("reopened RotatedAfterSends = %v, want pointer to 0", reopened.PendingCompaction.RotatedAfterSends)
	}
	if reopened.Model != "opus" {
		t.Fatalf("reopened Model = %q, want opus", reopened.Model)
	}
}

func TestWSManagedLeadRotationRealProtocolRestartAndAuditStore(t *testing.T) {
	newFixture := func(t *testing.T) (*leadHarness, *wsauditstore.Store) {
		t.Helper()
		h := newLeadHarness(t)
		builtin := BuiltinBackendRegistrations()[BackendClaude]
		reg := h.opts.Backends[BackendClaude]
		reg.Catalog = builtin.Catalog
		reg.NativeCompaction = builtin.NativeCompaction
		reg.Profiles = builtin.Profiles
		h.opts.Backends[BackendClaude] = reg
		auditRoot := filepath.Join(t.TempDir(), "audit")
		store, err := wsauditstore.Open(wsauditstore.Options{Root: auditRoot, SweepInterval: -1})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		h.opts.AuditSink = store
		h.opts.RestoreBundle = func(_, _, _ string, cause RestoreCause, _ []AgentChild) (string, error) {
			return "## Hosted Session Restore\n\n(cause=" + string(cause) + ")\n\n## Direct Children\n\n- worker-1\n", nil
		}
		resolver := wsconfig.NewResolver(h.opts.Config, nil, nil, nil)
		if err := resolver.Set(wsconfig.ItemCompactionLeadMode+"."+BackendClaude, wsconfig.CompactionModeWS, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
		NotifyCompactionConfigChanged()
		h.start()
		return h, store
	}

	t.Run("resume_false_restart_before_first_prompt", func(t *testing.T) {
		h, store := newFixture(t)
		id, h1 := h.runLead("initial work")
		h.holders.out(h1,
			stateLine(HarnessRunning),
			initLine("claude-sid-1", "auto"),
			toolUseLine("u1-tool", "tu-pre-1"),
			toolResultLine("tu-pre-1"),
			replyLine("u1", "initial reply"),
			resultLine(),
			stateLine(HarnessIdle),
		)
		h.idle(id)

		recBefore := h.record(id)
		if recBefore.SessionID != "claude-sid-1" {
			t.Fatalf("SessionID before rotation = %q, want claude-sid-1", recBefore.SessionID)
		}

		resume := false
		res := h.runner.CompactWithSummary(recBefore.WSKey, "", &resume, testLeadSummary("notes.txt"))
		if res.Status != "queued" {
			t.Fatalf("CompactWithSummary = %+v, want queued", res)
		}

		var h2 string
		eventually(t, "replacement holder started", func() bool {
			rec := h.record(id)
			h2 = rec.HolderID
			return h2 != "" && h2 != h1
		})
		h.quiesce(id)

		h.holders.mu.Lock()
		spec := h.holders.holders[h2].spec
		h.holders.mu.Unlock()
		if spec == nil || slices.Contains(spec.Argv, "--resume") {
			t.Fatalf("replacement holder args unexpectedly resumed old session: %+v", spec)
		}
		recRotated := h.record(id)
		if recRotated.PendingCompaction == nil {
			t.Fatal("expected PendingCompaction persisted after rotation with resume=false")
		}
		if recRotated.PendingCompaction.Resume || !recRotated.PendingCompaction.RestoreBundle || recRotated.PendingCompaction.Origin != wslive.TriggerManual {
			t.Fatalf("unexpected PendingCompaction after rotation: %+v", recRotated.PendingCompaction)
		}
		if !strings.Contains(recRotated.PendingCompaction.Note, "implementing phase 1") {
			t.Fatalf("PendingCompaction.Note missing checkpoint prose: %q", recRotated.PendingCompaction.Note)
		}
		if got := h.userInputs(h2); len(got) != 0 {
			t.Fatalf("unexpected prompt delivered before user follow-up with resume=false: %v", got)
		}

		// Restart daemon BEFORE the first prompt reaches the replacement session.
		// Real Claude stream-json emits init only after the first prompt arrives.
		h.restart()
		wait(t, "rejoin after restart", h.runner.rejoined)
		h.idle(id)

		recAfterRestart := h.record(id)
		if recAfterRestart.PendingCompaction == nil {
			t.Fatal("PendingCompaction lost across daemon restart")
		}
		if got := h.userInputs(h2); len(got) != 0 {
			t.Fatalf("resume=false sent a prompt automatically on restart: %v", got)
		}

		h.send(id, "follow up after restart")
		eventually(t, "first prompt delivered to replacement after restart", func() bool {
			return len(h.userInputs(h2)) == 1
		})
		prompt := h.userInputs(h2)[0]
		for _, want := range []string{
			"## Hosted Session Restore",
			"cause=compaction",
			"implementing phase 1",
			"- notes.txt",
			"initial reply",
			"follow up after restart",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("post-restart prompt missing %q:\n%s", want, prompt)
			}
		}
		if strings.Contains(prompt, "cause=restart") {
			t.Fatalf("generic restart restore overwrote compaction handoff:\n%s", prompt)
		}
		if strings.Contains(prompt, "## Child agents in flight") || strings.Contains(prompt, "## Child agents finished") {
			t.Fatalf("duplicate child sections present with RestoreBundle:\n%s", prompt)
		}

		eventually(t, "PendingCompaction cleared after delivery", func() bool {
			return h.record(id).PendingCompaction == nil
		})

		h.holders.out(h2,
			stateLine(HarnessRunning),
			initLine("claude-sid-2", "auto"),
			toolUseLine("u2-tool", "tu-post-2"),
			toolResultLine("tu-post-2"),
			replyLine("u2", "post-restart reply"),
			resultLine(),
			stateLine(HarnessIdle),
		)
		h.idle(id)
		if got := h.record(id).SessionID; got != "claude-sid-2" {
			t.Fatalf("SessionID after replacement turn = %q, want claude-sid-2", got)
		}

		if err := store.Flush(context.Background()); err != nil {
			t.Fatal(err)
		}
		events, err := store.Events(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		cut := wsaudit.FindModelSeenCut(events)
		if !cut.HasCompaction {
			t.Fatalf("audit store missing compaction cut: %+v", events)
		}
		var prevSeq uint64
		preIdx, compactIdx, postIdx := -1, -1, -1
		for i, ev := range events {
			if ev.Seq <= prevSeq {
				t.Fatalf("non-monotonic audit seq at %d: %d <= %d", i, ev.Seq, prevSeq)
			}
			prevSeq = ev.Seq
			if ev.Kind == wsaudit.KindCompaction {
				if _, est := ev.Meta[wsaudit.MetaEstimated]; est {
					t.Fatalf("ws-managed compaction audit must not be estimated: %+v", ev.Meta)
				}
				if ev.Meta["mode"] != "ws" {
					t.Fatalf("compaction audit mode = %q, want ws", ev.Meta["mode"])
				}
				compactIdx = i
			}
			if ev.CallID == "tu-pre-1" && preIdx < 0 {
				preIdx = i
			}
			if ev.CallID == "tu-post-2" && postIdx < 0 {
				postIdx = i
			}
		}
		if !(preIdx >= 0 && compactIdx > preIdx && postIdx > compactIdx) {
			t.Fatalf("unexpected audit ordering across rotation+restart: pre=%d compact=%d post=%d (len=%d)", preIdx, compactIdx, postIdx, len(events))
		}
	})

	t.Run("resume_true_restart_before_continuation_delivery", func(t *testing.T) {
		h, _ := newFixture(t)
		id, h1 := h.runLead("initial work")
		h.holders.out(h1, turnLines("claude-sid-1", "u1", "initial reply")...)
		h.idle(id)

		recBefore := h.record(id)
		resume := false
		if res := h.runner.CompactWithSummary(recBefore.WSKey, "", &resume, testLeadSummary("notes.txt")); res.Status != "queued" {
			t.Fatalf("CompactWithSummary = %+v, want queued", res)
		}
		var h2 string
		eventually(t, "replacement holder started", func() bool {
			rec := h.record(id)
			h2 = rec.HolderID
			return h2 != "" && h2 != h1
		})
		h.quiesce(id)

		// Simulate a crash right after rotateExternalSession persisted
		// PendingCompaction{Resume: true} and before compactIdle dispatched the
		// standalone continuation.
		h.runner.Close()
		h.holders.restart()
		rec := h.record(id)
		if rec.PendingCompaction == nil {
			t.Fatal("expected PendingCompaction on disk before restart")
		}
		rec.PendingCompaction.Resume = true
		if err := writeRecord(wsdashboard.SessionDir(h.dir, id), rec); err != nil {
			t.Fatal(err)
		}
		h.start()
		wait(t, "rejoin after restart", h.runner.rejoined)

		eventually(t, "standalone continuation delivered after restart", func() bool {
			return len(h.userInputs(h2)) == 1
		})
		prompt := h.userInputs(h2)[0]
		for _, want := range []string{
			"## Hosted Session Restore",
			"cause=compaction",
			"implementing phase 1",
			"Continue the work that was pending.",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("post-restart continuation prompt missing %q:\n%s", want, prompt)
			}
		}
		if strings.Contains(prompt, "cause=restart") {
			t.Fatalf("generic restart restore overwrote compaction continuation:\n%s", prompt)
		}
		if strings.Contains(prompt, "## Child agents in flight") || strings.Contains(prompt, "## Child agents finished") {
			t.Fatalf("duplicate child sections present with RestoreBundle:\n%s", prompt)
		}
		eventually(t, "PendingCompaction cleared after continuation send", func() bool {
			return h.record(id).PendingCompaction == nil
		})
	})
}

func TestWSManagedLeadRotationReplacementHolderDiesBeforeFirstPrompt(t *testing.T) {
	newFixture := func(t *testing.T) *leadHarness {
		t.Helper()
		h := newLeadHarness(t)
		builtin := BuiltinBackendRegistrations()[BackendClaude]
		reg := h.opts.Backends[BackendClaude]
		reg.Catalog = builtin.Catalog
		reg.NativeCompaction = builtin.NativeCompaction
		reg.Profiles = builtin.Profiles
		h.opts.Backends[BackendClaude] = reg
		h.opts.RestoreBundle = func(_, _, _ string, cause RestoreCause, _ []AgentChild) (string, error) {
			return "## Hosted Session Restore\n\n(cause=" + string(cause) + ")\n", nil
		}
		resolver := wsconfig.NewResolver(h.opts.Config, nil, nil, nil)
		if err := resolver.Set(wsconfig.ItemCompactionLeadMode+"."+BackendClaude, wsconfig.CompactionModeWS, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
		NotifyCompactionConfigChanged()
		h.start()
		return h
	}

	t.Run("resume_false_dead_replacement_holder_starts_fresh_session", func(t *testing.T) {
		h := newFixture(t)
		id, h1 := h.runLead("initial work")
		h.holders.out(h1, turnLines("claude-sid-1", "u1", "initial reply")...)
		h.idle(id)

		recBefore := h.record(id)
		resume := false
		if res := h.runner.CompactWithSummary(recBefore.WSKey, "", &resume, testLeadSummary("notes.txt")); res.Status != "queued" {
			t.Fatalf("CompactWithSummary = %+v, want queued", res)
		}
		var h2 string
		eventually(t, "replacement holder started", func() bool {
			rec := h.record(id)
			h2 = rec.HolderID
			return h2 != "" && h2 != h1
		})
		h.quiesce(id)

		recAfterRotate := h.record(id)
		if recAfterRotate.SessionID != "" || recAfterRotate.SessionFile != "" {
			t.Fatalf("after committed rotation SessionID=%q SessionFile=%q, want both cleared", recAfterRotate.SessionID, recAfterRotate.SessionFile)
		}

		// Replacement holder dies before any prompt or KindSession arrives.
		h.runner.Close()
		h.holders.restart()
		h.holders.vanish(h2)
		h.start()
		wait(t, "rejoin after dead replacement holder", h.runner.rejoined)

		h.send(id, "next prompt after dead holder")
		var h3 string
		eventually(t, "third holder started on next prompt", func() bool {
			rec := h.record(id)
			h3 = rec.HolderID
			return h3 != "" && h3 != h1 && h3 != h2 && len(h.userInputs(h3)) == 1
		})
		spec := h.holders.spawned(h3)
		if slices.Contains(spec.Argv, "--resume") || slices.Contains(spec.Argv, "claude-sid-1") {
			t.Fatalf("relaunch after dead replacement holder resumed old session: argv=%v", spec.Argv)
		}
		prompt := h.userInputs(h3)[0]
		for _, want := range []string{"## Hosted Session Restore", "cause=compaction", "implementing phase 1", "next prompt after dead holder"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("prompt missing %q:\n%s", want, prompt)
			}
		}
		if strings.Contains(prompt, "cause=restart") {
			t.Fatalf("prompt unexpectedly used cause=restart:\n%s", prompt)
		}
		eventually(t, "PendingCompaction cleared after delivery", func() bool {
			return h.record(id).PendingCompaction == nil
		})
	})

	t.Run("resume_true_dead_replacement_holder_starts_fresh_session", func(t *testing.T) {
		h := newFixture(t)
		id, h1 := h.runLead("initial work")
		h.holders.out(h1, turnLines("claude-sid-1", "u1", "initial reply")...)
		h.idle(id)

		recBefore := h.record(id)
		resume := false
		if res := h.runner.CompactWithSummary(recBefore.WSKey, "", &resume, testLeadSummary("notes.txt")); res.Status != "queued" {
			t.Fatalf("CompactWithSummary = %+v, want queued", res)
		}
		var h2 string
		eventually(t, "replacement holder started", func() bool {
			rec := h.record(id)
			h2 = rec.HolderID
			return h2 != "" && h2 != h1
		})
		h.quiesce(id)

		// Crash right after rotation with Resume=true and replacement holder dead.
		h.runner.Close()
		h.holders.restart()
		h.holders.vanish(h2)
		rec := h.record(id)
		if rec.PendingCompaction == nil {
			t.Fatal("expected PendingCompaction on disk")
		}
		rec.PendingCompaction.Resume = true
		if err := writeRecord(wsdashboard.SessionDir(h.dir, id), rec); err != nil {
			t.Fatal(err)
		}
		h.start()
		wait(t, "rejoin after dead replacement holder", h.runner.rejoined)

		var h3 string
		eventually(t, "third holder launched for standalone continuation", func() bool {
			r := h.record(id)
			h3 = r.HolderID
			return h3 != "" && h3 != h1 && h3 != h2 && len(h.userInputs(h3)) == 1
		})
		spec := h.holders.spawned(h3)
		if slices.Contains(spec.Argv, "--resume") || slices.Contains(spec.Argv, "claude-sid-1") {
			t.Fatalf("resume relaunch after dead replacement holder resumed old session: argv=%v", spec.Argv)
		}
		prompt := h.userInputs(h3)[0]
		for _, want := range []string{"## Hosted Session Restore", "cause=compaction", "implementing phase 1", "Continue the work that was pending."} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("continuation prompt missing %q:\n%s", want, prompt)
			}
		}
		eventually(t, "PendingCompaction cleared after continuation send", func() bool {
			return h.record(id).PendingCompaction == nil
		})
	})

	t.Run("resume_true_dead_replacement_holder_enforces_start_process_guards", func(t *testing.T) {
		h := newFixture(t)
		id, h1 := h.runLead("initial work")
		h.holders.out(h1, turnLines("claude-sid-1", "u1", "initial reply")...)
		h.idle(id)

		recBefore := h.record(id)
		resume := false
		if res := h.runner.CompactWithSummary(recBefore.WSKey, "", &resume, testLeadSummary("notes.txt")); res.Status != "queued" {
			t.Fatalf("CompactWithSummary = %+v, want queued", res)
		}
		var h2 string
		eventually(t, "replacement holder started", func() bool {
			rec := h.record(id)
			h2 = rec.HolderID
			return h2 != "" && h2 != h1
		})
		h.quiesce(id)

		// Dead replacement holder + profile now selects a different backend:
		// compactIdle must route through startProcess() and refuse to launch.
		h.runner.Close()
		h.holders.restart()
		h.holders.vanish(h2)
		rec := h.record(id)
		rec.Profile = "codex-lead"
		rec.PendingCompaction.Resume = true
		if err := writeRecord(wsdashboard.SessionDir(h.dir, id), rec); err != nil {
			t.Fatal(err)
		}
		spawnsBefore := h.holders.opCount("spawn:")
		h.start()
		wait(t, "rejoin after dead replacement holder with mismatched profile", h.runner.rejoined)

		eventually(t, "startProcess guard cleared PendingCompaction.Resume and recorded error", func() bool {
			r := h.record(id)
			st := h.state(id)
			return r.PendingCompaction != nil && !r.PendingCompaction.Resume && strings.Contains(st.Error, "resume after compaction:")
		})
		if got := h.holders.opCount("spawn:"); got != spawnsBefore {
			t.Fatalf("spawn count = %d, want %d (startProcess must refuse backend mismatch)", got, spawnsBefore)
		}
	})
}

func TestPendingCompactionDowngradeReopenDropsStaleHandoff(t *testing.T) {
	newFixture := func(t *testing.T) *leadHarness {
		t.Helper()
		h := newLeadHarness(t)
		builtin := BuiltinBackendRegistrations()[BackendClaude]
		reg := h.opts.Backends[BackendClaude]
		reg.Catalog = builtin.Catalog
		reg.NativeCompaction = builtin.NativeCompaction
		reg.Profiles = builtin.Profiles
		h.opts.Backends[BackendClaude] = reg
		h.opts.RestoreBundle = func(_, _, _ string, cause RestoreCause, _ []AgentChild) (string, error) {
			return "## Hosted Session Restore\n\n(cause=" + string(cause) + ")\n", nil
		}
		resolver := wsconfig.NewResolver(h.opts.Config, nil, nil, nil)
		if err := resolver.Set(wsconfig.ItemCompactionLeadMode+"."+BackendClaude, wsconfig.CompactionModeWS, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
		NotifyCompactionConfigChanged()
		h.start()
		return h
	}

	for _, tc := range []struct {
		name          string
		resume        bool
		zeroWaterMark bool
		vanishHolder  bool
	}{
		{name: "resume_false_live_holder", resume: false},
		{name: "resume_true_live_holder", resume: true},
		{name: "resume_true_dead_holder", resume: true, vanishHolder: true},
		{name: "zero_sends_watermark", resume: true, zeroWaterMark: true, vanishHolder: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFixture(t)
			id, h1 := h.runLead("initial work")
			h.holders.out(h1, turnLines("claude-sid-1", "u1", "initial reply")...)
			h.idle(id)

			recBefore := h.record(id)
			resume := false
			if res := h.runner.CompactWithSummary(recBefore.WSKey, "", &resume, testLeadSummary("notes.txt")); res.Status != "queued" {
				t.Fatalf("CompactWithSummary = %+v, want queued", res)
			}
			var h2 string
			eventually(t, "replacement holder started", func() bool {
				rec := h.record(id)
				h2 = rec.HolderID
				return h2 != "" && h2 != h1
			})
			h.quiesce(id)

			// Simulate 0.52.2 -> 0.52.0 -> 0.52.2: the older daemon preserved
			// unknown pending_compaction as T0, ran a newer turn (advancing
			// Sends), and persisted the record without clearing PendingCompaction.
			h.runner.Close()
			h.holders.restart()
			if tc.vanishHolder {
				h.holders.vanish(h2)
			}
			rec := h.record(id)
			if rec.PendingCompaction == nil || rec.PendingCompaction.RotatedAfterSends == nil {
				t.Fatalf("expected PendingCompaction with RotatedAfterSends, got %+v", rec.PendingCompaction)
			}
			if tc.zeroWaterMark {
				zero := uint64(0)
				rec.PendingCompaction.RotatedAfterSends = &zero
				rec.Sends = 1
			} else {
				if *rec.PendingCompaction.RotatedAfterSends != rec.Sends {
					t.Fatalf("RotatedAfterSends = %d, want %d", *rec.PendingCompaction.RotatedAfterSends, rec.Sends)
				}
				rec.Sends++
			}
			rec.PendingCompaction.Resume = tc.resume
			rec.SessionID = "claude-sid-2"
			if err := writeRecord(wsdashboard.SessionDir(h.dir, id), rec); err != nil {
				t.Fatal(err)
			}

			spawnsBefore := h.holders.opCount("spawn:")
			h.start()
			wait(t, "rejoin after downgrade-reopen", h.runner.rejoined)
			h.quiesce(id)

			if after := h.record(id); after.PendingCompaction != nil {
				t.Fatalf("stale PendingCompaction was not cleared on startup: %+v", after.PendingCompaction)
			}

			var prompt string
			if tc.vanishHolder {
				// With no live holder and stale PendingCompaction dropped,
				// Resume=true must NOT spawn a holder or fire a continuation.
				if got := h.holders.opCount("spawn:"); got != spawnsBefore {
					t.Fatalf("stale Resume=%v spawned holder on startup: spawns=%d want %d", tc.resume, got, spawnsBefore)
				}
				h.send(id, "fresh prompt on 0.52.2")
				var h3 string
				eventually(t, "fresh prompt delivered on new holder with restart restore", func() bool {
					r := h.record(id)
					h3 = r.HolderID
					return h3 != "" && h3 != h1 && h3 != h2 && len(h.userInputs(h3)) == 1
				})
				prompt = h.userInputs(h3)[0]
				if !strings.Contains(prompt, "fresh prompt on 0.52.2") {
					t.Fatalf("prompt missing user input:\n%s", prompt)
				}
			} else {
				// With a live holder rejoined, falling through to normal
				// restart restore delivers the standalone cause=restart bundle
				// rather than the stale compaction continuation.
				eventually(t, "standalone restart restore delivered", func() bool {
					return len(h.userInputs(h2)) == 1
				})
				prompt = h.userInputs(h2)[0]
			}
			if !strings.Contains(prompt, "## Hosted Session Restore") || !strings.Contains(prompt, "cause=restart") {
				t.Fatalf("expected normal restart restore after dropping stale PendingCompaction:\n%s", prompt)
			}
			for _, forbidden := range []string{"cause=compaction", "implementing phase 1", "Continue the work that was pending."} {
				if strings.Contains(prompt, forbidden) {
					t.Fatalf("stale compaction content %q leaked into post-downgrade prompt:\n%s", forbidden, prompt)
				}
			}
		})
	}

	t.Run("restore_bundle_false_is_honored", func(t *testing.T) {
		h := newFixture(t)
		id, h1 := h.runLead("initial work")
		h.holders.out(h1, turnLines("claude-sid-1", "u1", "initial reply")...)
		h.idle(id)

		h.runner.Close()
		h.holders.restart()
		rec := h.record(id)
		sends := rec.Sends
		rec.PendingCompaction = &PendingCompaction{
			Note:              "plain compaction checkpoint without bundle",
			RestoreBundle:     false,
			Resume:            false,
			Origin:            wslive.TriggerManual,
			RotatedAfterSends: &sends,
		}
		if err := writeRecord(wsdashboard.SessionDir(h.dir, id), rec); err != nil {
			t.Fatal(err)
		}

		h.start()
		wait(t, "rejoin with RestoreBundle=false", h.runner.rejoined)
		h.send(id, "user follow-up")
		eventually(t, "follow-up prompt delivered", func() bool {
			return len(h.userInputs(h1)) == 2
		})
		prompt := h.userInputs(h1)[1]
		if !strings.Contains(prompt, "plain compaction checkpoint without bundle") || !strings.Contains(prompt, "user follow-up") {
			t.Fatalf("prompt missing expected handoff/user text:\n%s", prompt)
		}
		if strings.Contains(prompt, "## Hosted Session Restore") || strings.Contains(prompt, "cause=compaction") {
			t.Fatalf("RestoreBundle=false still injected restore bundle:\n%s", prompt)
		}
	})
}
