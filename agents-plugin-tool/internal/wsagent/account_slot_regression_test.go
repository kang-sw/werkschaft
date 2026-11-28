package wsagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

func setupSlotRegressionLead(t *testing.T) (*Runner, *slotTestBackend, string, string, string) {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	defaultCodex := filepath.Join(home, ".codex")
	slotBDir := filepath.Join(t.TempDir(), "codex-slot-b")
	if err := os.MkdirAll(defaultCodex, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(slotBDir, 0o700); err != nil {
		t.Fatal(err)
	}
	reg := wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), wscred.AccountSlotsFileName))
	t.Cleanup(reg.Close)
	if _, err := reg.AddSlot(wscred.AccountSlot{ID: "slot-b", Harness: BackendCodex, Label: "B", ConfigDir: slotBDir}); err != nil {
		t.Fatal(err)
	}
	b := &slotTestBackend{}
	runner, id := newSlotTestRunner(t, root, wsconfig.CompactionModeWS, reg, home, b)
	if _, err := runner.Send(id, "warm turn"); err != nil {
		t.Fatal(err)
	}
	waitWSCompact(t, "warm turn", func() bool {
		p := b.latest()
		if p == nil {
			return false
		}
		sends, _ := p.sent()
		return len(sends) == 1
	})
	p1 := b.latest()
	p1.events <- Event{SessionID: "sid-init"}
	p1.events <- Event{TurnStart: true}
	p1.events <- Event{TurnEnd: true}
	waitWSCompact(t, "idle with sid-init", func() bool {
		c := runner.conv(id)
		var idle bool
		c.do(func() { idle = !c.turn && c.rec.SessionID == "sid-init" })
		return idle
	})
	return runner, b, id, defaultCodex, slotBDir
}

func countRingCompactEntries(t *testing.T, dir string) (succeeded, failed int) {
	t.Helper()
	page, err := wslive.ReadRingPage(filepath.Join(dir, "ring"), "", wslive.RingHistoryCap)
	if err != nil {
		t.Fatalf("read ring: %v", err)
	}
	for _, r := range page.Records {
		if r.Kind != wslive.RingCompact {
			continue
		}
		if r.CompactFailed {
			failed++
		} else {
			succeeded++
		}
	}
	return succeeded, failed
}

// TestAccountSlotPath2bStartFailureUnwindsCompacting verifies Finding 1:
// when Path 2b rotateExternalSession fails after emitting CompactStart,
// c.compacting/c.compactOrigin/c.cmpActive are unwound cleanly via
// CompactFailed so subsequent slot switches and turns are never wedged.
func TestAccountSlotPath2bStartFailureUnwindsCompacting(t *testing.T) {
	runner, b, id, _, slotBDir := setupSlotRegressionLead(t)

	// Fail the first Path 2b start (no auth in slotBDir so 2a preflight fails
	// and falls straight into Path 2b).
	var failOnce atomic.Bool
	failOnce.Store(true)
	b.mu.Lock()
	b.onStart = func(spec StartSpec) (Process, error) {
		if failOnce.CompareAndSwap(true, false) {
			return nil, errors.New("simulated 2b start failure")
		}
		return b.wsCompactBackend.Start(context.Background(), spec)
	}
	b.mu.Unlock()

	_, err := runner.SwitchConversationAccountSlotNow(id, "slot-b")
	if err == nil || !strings.Contains(err.Error(), "simulated 2b start failure") {
		t.Fatalf("expected simulated 2b start failure, got %v", err)
	}

	c := runner.conv(id)
	var compacting, compactCmd, cmpActive bool
	var compactOrigin, pendingSlot string
	c.do(func() {
		compacting = c.compacting
		compactCmd = c.compactCommand
		cmpActive = c.cmpActive
		compactOrigin = c.compactOrigin
		pendingSlot = c.rec.PendingAccountSlotID
	})
	if compacting || compactCmd || cmpActive || compactOrigin != "" {
		t.Fatalf("compaction state stranded after failed 2b switch: compacting=%v compactCmd=%v cmpActive=%v origin=%q",
			compacting, compactCmd, cmpActive, compactOrigin)
	}
	if pendingSlot != "" {
		t.Fatalf("pending slot not restored after failed 2b switch: %q", pendingSlot)
	}
	succeeded, failed := countRingCompactEntries(t, c.dir)
	if succeeded != 0 || failed != 1 {
		t.Fatalf("ring compact entries = (succeeded=%d failed=%d), want (0, 1)", succeeded, failed)
	}

	// Retry the slot switch now that start succeeds: must NOT return 409 conversation_busy.
	res, err := runner.SwitchConversationAccountSlotNow(id, "slot-b")
	if err != nil {
		t.Fatalf("retry SwitchConversationAccountSlotNow failed: %v", err)
	}
	if res.Mode != "rotated_2b" || res.AccountSlotID != "slot-b" {
		t.Fatalf("retry result = %+v, want rotated_2b on slot-b", res)
	}
	allStarts, _, _ := b.snapshot()
	if got := allStarts[len(allStarts)-1].AccountConfigDir; got != slotBDir {
		t.Fatalf("retried 2b start config dir = %q, want %q", got, slotBDir)
	}

	// Subsequent user turn must succeed cleanly.
	pNext := b.latest()
	if _, err := runner.Send(id, "after recovery"); err != nil {
		t.Fatalf("send after recovery: %v", err)
	}
	waitWSCompact(t, "post-recovery prompt delivered", func() bool {
		sends, _ := pNext.sent()
		return len(sends) == 1
	})
	pNext.events <- Event{TurnStart: true}
	pNext.events <- Event{TurnEnd: true}
	waitWSCompact(t, "post-recovery turn settled", func() bool {
		var idle bool
		c.do(func() { idle = !c.turn })
		return idle
	})
}

// TestFallbackSlot2aTo2bFailureUnwindsCompacting verifies Finding 1 for the
// fallbackSlot2aTo2b path: a start error during 2a->2b fallback unwinds
// c.compacting and allows a subsequent slot switch and turn to succeed.
func TestFallbackSlot2aTo2bFailureUnwindsCompacting(t *testing.T) {
	runner, b, id, defaultCodex, slotBDir := setupSlotRegressionLead(t)

	// Prepare valid auth and rollout so initial SwitchConversationAccountSlotNow
	// takes Path 2a and arms slot2aVerifyPending.
	rolloutDir := filepath.Join(defaultCodex, "sessions", "2026", "11", "28")
	if err := os.MkdirAll(rolloutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rolloutDir, "rollout-2026-11-28-sid-init.jsonl"), []byte(`{"type":"session_meta"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slotBDir, "auth.json"), []byte(`{"tokens":{"access_token":"tok-slot-b"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := runner.SwitchConversationAccountSlotNow(id, "slot-b")
	if err != nil || res.Mode != "restarted_2a" {
		t.Fatalf("initial 2a switch = (%+v, %v), want restarted_2a", res, err)
	}

	// Fail the first 2b fallback start, then allow subsequent starts.
	var failOnce atomic.Bool
	failOnce.Store(true)
	b.mu.Lock()
	b.onStart = func(spec StartSpec) (Process, error) {
		if failOnce.CompareAndSwap(true, false) {
			return nil, errors.New("simulated 2b fallback start failure")
		}
		return b.wsCompactBackend.Start(context.Background(), spec)
	}
	b.mu.Unlock()

	p2 := b.latest()
	if _, err := runner.Send(id, "trigger 2a rejection"); err != nil {
		t.Fatal(err)
	}
	waitWSCompact(t, "post-resume prompt sent", func() bool {
		sends, _ := p2.sent()
		return len(sends) == 1
	})
	p2.events <- Event{TurnStart: true}
	p2.events <- Event{
		TurnEnd:      true,
		TurnError:    "session belongs to another account",
		TurnErrorVal: &CodexRPCError{Code: -32600, HasCode: true, Info: "unauthorized", Message: "session belongs to another account"},
	}

	c := runner.conv(id)
	waitWSCompact(t, "fallback failure unwound", func() bool {
		_, failed := countRingCompactEntries(t, c.dir)
		return failed == 1
	})
	var compacting, compactCmd, cmpActive bool
	var compactOrigin string
	c.do(func() {
		compacting = c.compacting
		compactCmd = c.compactCommand
		cmpActive = c.cmpActive
		compactOrigin = c.compactOrigin
	})
	if compacting || compactCmd || cmpActive || compactOrigin != "" {
		t.Fatalf("compaction state stranded after failed fallbackSlot2aTo2b: compacting=%v compactCmd=%v cmpActive=%v origin=%q",
			compacting, compactCmd, cmpActive, compactOrigin)
	}

	// Subsequent turn must start cleanly on slot-b (not blocked by c.compacting).
	if _, err := runner.Send(id, "recovery turn"); err != nil {
		t.Fatalf("send after fallback failure: %v", err)
	}
	waitWSCompact(t, "recovery process started", func() bool {
		p := b.latest()
		if p == nil || p == p2 {
			return false
		}
		sends, _ := p.sent()
		return len(sends) == 1
	})
	p3 := b.latest()
	p3.events <- Event{TurnStart: true}
	p3.events <- Event{TurnEnd: true}
	waitWSCompact(t, "recovery turn settled", func() bool {
		var idle bool
		c.do(func() { idle = !c.turn })
		return idle
	})
}

// TestCompactIdleRotateFailureEmitsSingleCompactFailed verifies compactIdle
// preserves its failure semantics without double-emitting CompactFailed when
// rotateExternalSession fails after emitting CompactStart.
func TestCompactIdleRotateFailureEmitsSingleCompactFailed(t *testing.T) {
	runner, b, id, _, _ := setupSlotRegressionLead(t)

	var failOnce atomic.Bool
	failOnce.Store(true)
	b.mu.Lock()
	b.onStart = func(spec StartSpec) (Process, error) {
		if failOnce.CompareAndSwap(true, false) {
			return nil, errors.New("simulated compact start failure")
		}
		return b.wsCompactBackend.Start(context.Background(), spec)
	}
	b.mu.Unlock()

	c := runner.conv(id)
	req := &compactRequest{
		resume:      false,
		triggerPath: "test_compact_idle_fail",
		lead:        emptyWorkerSummary(),
	}
	c.do(func() {
		c.compactPending = req
		c.compactIdle()
	})
	if req.err == nil || !strings.Contains(req.err.Error(), "simulated compact start failure") {
		t.Fatalf("compactIdle req.err = %v, want simulated compact start failure", req.err)
	}
	var compacting, compactCmd, cmpActive bool
	c.do(func() {
		compacting = c.compacting
		compactCmd = c.compactCommand
		cmpActive = c.cmpActive
	})
	if compacting || compactCmd || cmpActive {
		t.Fatalf("compaction state not cleared: compacting=%v compactCmd=%v cmpActive=%v", compacting, compactCmd, cmpActive)
	}
	succeeded, failed := countRingCompactEntries(t, c.dir)
	if succeeded != 0 || failed != 1 {
		t.Fatalf("ring compact entries = (succeeded=%d failed=%d), want (0, 1)", succeeded, failed)
	}
}

// TestAccountSlotParkUnparkRaces verifies Finding 2:
// 1. Serialized parked persistAgentRecord vs unpark and concurrent fan-out
//    never overwrites a newer agent.json with a stale parked snapshot.
// 2. Concurrent park during QueueConversationAccountSlot,
//    SwitchConversationAccountSlotNow, and QueueHarnessAccountSlot neither
//    returns spurious errClosed nor silently drops the parked child.
func TestAccountSlotParkUnparkRaces(t *testing.T) {
	t.Run("serialized_parked_persist_vs_unpark_and_concurrent_fanout", func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		var hooked atomic.Bool

		slotADir := filepath.Join(t.TempDir(), "slot-a")
		slotBDir := filepath.Join(t.TempDir(), "slot-b")
		if err := os.MkdirAll(slotADir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(slotBDir, 0o700); err != nil {
			t.Fatal(err)
		}
		reg := wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), wscred.AccountSlotsFileName))
		t.Cleanup(reg.Close)

		h, _ := agentHarness(t, func(o *Options) {
			o.ParkIdleChildren = true
			o.AccountSlots = reg
			o.beforeParkedAccountSlotWrite = func(string) {
				if hooked.CompareAndSwap(false, true) {
					close(entered)
					<-release
				}
			}
		})
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		backend := h.record(a).Backend
		if _, err := reg.AddSlot(wscred.AccountSlot{ID: "slot-a", Harness: backend, Label: "A", ConfigDir: slotADir}); err != nil {
			t.Fatal(err)
		}
		if _, err := reg.AddSlot(wscred.AccountSlot{ID: "slot-b", Harness: backend, Label: "B", ConfigDir: slotBDir}); err != nil {
			t.Fatal(err)
		}
		ap.reply("done 1")
		eventually(t, "turn 1 settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		eventually(t, "child parked", func() bool { return h.runner.isParked(a) && !h.runner.residentConv(a) })

		// G1 starts QueueHarnessAccountSlot(slot-a) and pauses right before persistAgentRecord.
		g1Done := make(chan error, 1)
		go func() {
			_, _, err := h.runner.QueueHarnessAccountSlot(backend, "slot-a")
			g1Done <- err
		}()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for beforeParkedAccountSlotWrite hook")
		}

		// While G1 holds r.parkedWrite[a], launch both unpark (G2) and a
		// concurrent QueueHarnessAccountSlot(slot-b) (G3). Both MUST block
		// until G1 finishes persisting slot-a.
		g2Done := make(chan *conv, 1)
		go func() {
			g2Done <- h.runner.unpark(a)
		}()
		g3Done := make(chan error, 1)
		go func() {
			_, _, err := h.runner.QueueHarnessAccountSlot(backend, "slot-b")
			g3Done <- err
		}()

		select {
		case <-g2Done:
			t.Fatal("unpark proceeded before in-flight parked persistAgentRecord finished")
		case <-g3Done:
			t.Fatal("concurrent QueueHarnessAccountSlot proceeded before in-flight parked persistAgentRecord finished")
		case <-time.After(60 * time.Millisecond):
		}

		// Release G1; G1 persists slot-a first, then G2 and G3 proceed in order.
		close(release)
		if err := <-g1Done; err != nil {
			t.Fatalf("G1 QueueHarnessAccountSlot(slot-a): %v", err)
		}
		c := <-g2Done
		if c == nil {
			t.Fatal("G2 unpark returned nil")
		}
		if err := <-g3Done; err != nil {
			t.Fatalf("G3 QueueHarnessAccountSlot(slot-b): %v", err)
		}

		// Memory and on-disk agent.json MUST both reflect slot-b (never clobbered by G1's slot-a).
		if got := h.runner.conv(a).record().PendingAccountSlotID; got != "slot-b" {
			t.Fatalf("memory PendingAccountSlotID = %q, want slot-b", got)
		}
		diskRec, err := readRecord(wsdashboard.SessionDir(h.dir, a))
		if err != nil {
			t.Fatalf("readRecord: %v", err)
		}
		if diskRec.PendingAccountSlotID != "slot-b" {
			t.Fatalf("disk PendingAccountSlotID = %q, want slot-b", diskRec.PendingAccountSlotID)
		}
	})

	t.Run("parked_persist_serialized_against_unpark_mutation", func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		var hooked atomic.Bool

		slotADir := filepath.Join(t.TempDir(), "slot-a")
		slotBDir := filepath.Join(t.TempDir(), "slot-b")
		_ = os.MkdirAll(slotADir, 0o700)
		_ = os.MkdirAll(slotBDir, 0o700)
		reg := wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), wscred.AccountSlotsFileName))
		t.Cleanup(reg.Close)

		h, _ := agentHarness(t, func(o *Options) {
			o.ParkIdleChildren = true
			o.AccountSlots = reg
			o.beforeParkedAccountSlotWrite = func(string) {
				if hooked.CompareAndSwap(false, true) {
					close(entered)
					<-release
				}
			}
		})
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		backend := h.record(a).Backend
		_, _ = reg.AddSlot(wscred.AccountSlot{ID: "slot-a", Harness: backend, Label: "A", ConfigDir: slotADir})
		_, _ = reg.AddSlot(wscred.AccountSlot{ID: "slot-b", Harness: backend, Label: "B", ConfigDir: slotBDir})
		ap.reply("done 1")
		eventually(t, "turn 1 settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		eventually(t, "child parked", func() bool { return h.runner.isParked(a) && !h.runner.residentConv(a) })

		g1Done := make(chan error, 1)
		go func() {
			_, _, err := h.runner.QueueHarnessAccountSlot(backend, "slot-a")
			g1Done <- err
		}()
		<-entered

		g2Done := make(chan error, 1)
		go func() {
			_, err := h.runner.QueueConversationAccountSlot(a, "slot-b")
			g2Done <- err
		}()
		select {
		case <-g2Done:
			t.Fatal("QueueConversationAccountSlot unparked and mutated before parked write finished")
		case <-time.After(60 * time.Millisecond):
		}

		close(release)
		if err := <-g1Done; err != nil {
			t.Fatalf("G1: %v", err)
		}
		if err := <-g2Done; err != nil {
			t.Fatalf("G2: %v", err)
		}

		diskRec, err := readRecord(wsdashboard.SessionDir(h.dir, a))
		if err != nil {
			t.Fatalf("readRecord: %v", err)
		}
		if diskRec.PendingAccountSlotID != "slot-b" {
			t.Fatalf("disk PendingAccountSlotID = %q, want slot-b (unpark mutation must not be clobbered)", diskRec.PendingAccountSlotID)
		}
	})

	t.Run("concurrent_park_during_queue_switch_and_fanout", func(t *testing.T) {
		slotADir := filepath.Join(t.TempDir(), "slot-a")
		slotBDir := filepath.Join(t.TempDir(), "slot-b")
		_ = os.MkdirAll(slotADir, 0o700)
		_ = os.MkdirAll(slotBDir, 0o700)
		reg := wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), wscred.AccountSlotsFileName))
		t.Cleanup(reg.Close)

		h, _ := agentHarness(t, func(o *Options) {
			o.ParkIdleChildren = true
			o.AccountSlots = reg
			o.heartbeat = 5 * time.Millisecond
		})
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		backend := h.record(a).Backend
		_, _ = reg.AddSlot(wscred.AccountSlot{ID: "slot-a", Harness: backend, Label: "A", ConfigDir: slotADir})
		_, _ = reg.AddSlot(wscred.AccountSlot{ID: "slot-b", Harness: backend, Label: "B", ConfigDir: slotBDir})
		ap.reply("done 1")
		eventually(t, "turn 1 settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		eventually(t, "child parked", func() bool { return h.runner.isParked(a) && !h.runner.residentConv(a) })

		// Hammer QueueConversationAccountSlot, SwitchConversationAccountSlotNow,
		// and QueueHarnessAccountSlot concurrently while the fast 5ms heartbeat
		// continuously parks the idle child between operations.
		var wg sync.WaitGroup
		errCh := make(chan error, 64)
		for i := 0; i < 16; i++ {
			target := "slot-a"
			if i%2 == 1 {
				target = "slot-b"
			}
			// Rehydrate so maybePark races with the account-slot operations.
			_ = h.runner.unpark(a)
			wg.Add(2)
			go func(slot string) {
				defer wg.Done()
				if _, err := h.runner.QueueConversationAccountSlot(a, slot); err != nil {
					errCh <- err
				}
			}(target)
			go func(slot string) {
				defer wg.Done()
				if _, _, err := h.runner.QueueHarnessAccountSlot(backend, slot); err != nil {
					errCh <- err
				}
			}(target)
			wg.Wait()
			// Also test SwitchConversationAccountSlotNow racing with park.
			_ = h.runner.unpark(a)
			if _, err := h.runner.SwitchConversationAccountSlotNow(a, target); err != nil {
				t.Fatalf("iter %d SwitchConversationAccountSlotNow(%s) failed: %v", i, target, err)
			}
		}
		close(errCh)
		for err := range errCh {
			t.Fatalf("concurrent account-slot op failed: %v", err)
		}

		// Final fan-out must never silently drop the child regardless of park state.
		if _, _, err := h.runner.QueueHarnessAccountSlot(backend, "slot-a"); err != nil {
			t.Fatalf("final QueueHarnessAccountSlot: %v", err)
		}
		gotRec := h.record(a)
		if gotRec.AccountSlotID != "slot-a" && gotRec.PendingAccountSlotID != "slot-a" {
			t.Fatalf("child silently dropped by fan-out: AccountSlotID=%q PendingAccountSlotID=%q",
				gotRec.AccountSlotID, gotRec.PendingAccountSlotID)
		}
	})
}
