package wsagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wslive"
)

// A mid-turn ws/compact only queues a request for the idle boundary. These
// tests pin that the queued request never silences hard-cap enforcement for a
// turn that keeps running (a long tool loop once reached 169% of hard).

func steerCapableWorker(reg *BackendRegistration) { reg.Lifecycle.NoWorkerCompactionSteer = false }

func TestQueuedCompactReasonTellsCallerToEndTurn(t *testing.T) {
	h, id, _ := wsWorkerHarness(t)
	key := h.record(id).WSKey
	res := h.runner.Compact(key, "", nil)
	if res.Status != "queued" || !strings.Contains(res.Reason, "end your turn now") {
		t.Fatalf("queued self-compaction must tell the caller to end its turn: %+v", res)
	}
	again := h.runner.Compact(key, "", nil)
	if again.Status != "queued" || !strings.Contains(again.Reason, "already pending") || !strings.Contains(again.Reason, "end your turn now") {
		t.Fatalf("repeat call must repeat the instruction: %+v", again)
	}
}

func TestQueuedCompactSteerCapableWorkerSteersThenInterruptsThenCompactsOnce(t *testing.T) {
	h, id, holder := wsWorkerHarnessWith(t, steerCapableWorker)
	c := h.runner.conv(id)
	key := h.record(id).WSKey
	if res := h.runner.Compact(key, "", nil); res.Status != "queued" {
		t.Fatalf("Compact() = %+v", res)
	}
	bt := newBatchTracker("t")
	c.do(func() {
		c.compactHard = wsconfig.Threshold{Tokens: 200_000}
		// Below hard: nothing fires.
		c.handle(hardUsage(150_000, 320_000))
		if c.compactHardSteered || c.compactHardInterrupt {
			t.Error("hard-cap enforcement fired below hard")
		}
		// At hard with the turn still open: Stage 1 steer must fire despite the
		// queued request.
		c.handle(hardUsage(220_000, 320_000))
		if !c.compactHardSteered || c.compactHardInterrupt || c.compactPending == nil {
			t.Errorf("Stage 1 steer: steered=%v interrupt=%v pending=%v", c.compactHardSteered, c.compactHardInterrupt, c.compactPending != nil)
		}
	})
	steers := 0
	for _, in := range h.userInputs(holder) {
		if strings.Contains(in, "ws/compact request is queued") && strings.Contains(in, "end your turn now") {
			steers++
		}
	}
	if steers != 1 {
		t.Fatalf("queued-aware steer sent %d times: %q", steers, h.userInputs(holder))
	}
	// Within 120% a single boundary does not interrupt a steered turn.
	toolBoundary(c, &bt, "a", 1)
	c.do(func() {
		if c.compactHardInterrupt {
			t.Error("interrupted inside 120% on the first boundary")
		}
		// 120% ceiling reached: Stage 2.
		c.handle(hardUsage(250_000, 320_000))
	})
	toolBoundary(c, &bt, "b", 10)
	c.do(func() {
		if !c.compactHardInterrupt {
			t.Error("Stage 2 did not interrupt past 120% with a queued request")
			return
		}
		if c.compactPending == nil || !c.compactPending.crossing || !c.compactPending.resume {
			t.Errorf("queued request lost or altered: %+v", c.compactPending)
		}
		c.summaryReason = wslive.TurnAborted
		c.endTurn("", false)
		c.summaryReason = ""
	})
	eventually(t, "queued request rotated the worker once", func() bool {
		rec, err := h.readRecord(id)
		return err == nil && rec.HolderID != "" && rec.HolderID != holder
	})
	next := h.holder(id)
	eventually(t, "resume preserved after rotation", func() bool {
		in := h.userInputs(next)
		return len(in) >= 1 && strings.Contains(in[0], "Continue the work that was pending.")
	})
	if slices.Contains(h.userInputs(holder), "/compact") {
		t.Fatal("ws-mode worker must not send native /compact")
	}
	c.do(func() {
		if c.compactPending != nil || c.compacting || c.compactCommand {
			t.Errorf("request left behind: pending=%v compacting=%v command=%v", c.compactPending != nil, c.compacting, c.compactCommand)
		}
		if c.cmpCount != 1 {
			t.Errorf("compacted %d times, want once", c.cmpCount)
		}
	})
}

func TestQueuedCompactNoSteerWorkerInterruptsAtCeilingAndCompacts(t *testing.T) {
	h, id, holder := wsWorkerHarness(t)
	c := h.runner.conv(id)
	if res := h.runner.Compact(h.record(id).WSKey, "", nil); res.Status != "queued" {
		t.Fatalf("Compact() = %+v", res)
	}
	bt := newBatchTracker("t")
	c.do(func() {
		c.compactHard = wsconfig.Threshold{Tokens: 200_000}
		c.handle(hardUsage(220_000, 320_000))
		if c.compactHardSteered {
			t.Error("claude worker got a compaction steer")
		}
	})
	toolBoundary(c, &bt, "a", 1)
	c.do(func() {
		if c.compactHardInterrupt {
			t.Error("claude worker escalated below the 120% ceiling")
		}
		c.handle(hardUsage(250_000, 320_000))
	})
	toolBoundary(c, &bt, "b", 10)
	c.do(func() {
		if !c.compactHardInterrupt {
			t.Error("claude worker at >=120% with a queued request did not escalate")
			return
		}
		c.summaryReason = wslive.TurnAborted
		c.endTurn("", false)
		c.summaryReason = ""
	})
	eventually(t, "queued request rotated the worker", func() bool {
		rec, err := h.readRecord(id)
		return err == nil && rec.HolderID != "" && rec.HolderID != holder
	})
	next := h.holder(id)
	eventually(t, "worker resumed", func() bool {
		in := h.userInputs(next)
		return len(in) >= 1 && strings.Contains(in[0], "Continue the work that was pending.")
	})
	c.do(func() {
		if c.cmpCount != 1 || c.compactPending != nil {
			t.Errorf("cmpCount=%d pending=%v, want one compaction and no leftover request", c.cmpCount, c.compactPending != nil)
		}
	})
}

func TestQueuedCompactPromptTurnEndNeitherSteersNorInterrupts(t *testing.T) {
	h, id, holder := wsWorkerHarnessWith(t, steerCapableWorker)
	c := h.runner.conv(id)
	if res := h.runner.Compact(h.record(id).WSKey, "", nil); res.Status != "queued" {
		t.Fatalf("Compact() = %+v", res)
	}
	bt := newBatchTracker("t")
	c.do(func() {
		c.compactHard = wsconfig.Threshold{Tokens: 200_000}
		c.handle(hardUsage(150_000, 320_000))
	})
	toolBoundary(c, &bt, "a", 1)
	c.do(func() {
		if c.compactHardSteered || c.compactHardInterrupt {
			t.Errorf("steer/interrupt below hard: steered=%v interrupt=%v", c.compactHardSteered, c.compactHardInterrupt)
		}
	})
	if got := h.userInputs(holder); len(got) != 1 {
		t.Fatalf("spurious input while the turn ran: %q", got)
	}
	h.holders.out(holder, resultLine())
	eventually(t, "queued request rotated once at the idle boundary", func() bool {
		rec, err := h.readRecord(id)
		return err == nil && rec.HolderID != "" && rec.HolderID != holder
	})
	next := h.holder(id)
	eventually(t, "continuation", func() bool {
		in := h.userInputs(next)
		return len(in) >= 1 && strings.Contains(in[0], "Continue the work that was pending.")
	})
	c.do(func() {
		if c.cmpCount != 1 || c.compactHardInterrupt || c.compactHardSteered {
			t.Errorf("cmpCount=%d interrupt=%v steered=%v", c.cmpCount, c.compactHardInterrupt, c.compactHardSteered)
		}
	})
}

func TestQueuedCompactRestoredAfterRestartStillEnforcesHardCap(t *testing.T) {
	probe := &restoreProbe{state: "queued-hard"}
	h, got := configureHeldWorkerHarness(t)
	reg := h.opts.Backends[BackendClaude]
	reg.NativeCompaction = fixtureCompaction()
	h.opts.Backends[BackendClaude] = reg
	h.opts.RestoreBundle = probe.render
	h.start()
	lead := h.lead(BackendClaude, LeadOptions{})
	child, err := h.runner.Spawn(SpawnSpec{
		Profile:            AgentWorkerProfile(BackendClaude),
		PermissionLevel:    PermissionWorkdirWrite,
		delegatedAuthority: true,
		Root:               h.root,
		Prompt:             "work",
		Parent:             &ParentRef{Kind: ParentLead, ReplyID: lead.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	holder := h.holder(child.ID)
	h.holders.out(holder, stateLine(HarnessRunning))
	eventually(t, "worker running", func() bool { return h.state(child.ID).Status == StatusRunning })

	if res := h.runner.Compact(h.record(child.ID).WSKey, "", nil); res.Status != "queued" {
		t.Fatal(res)
	}
	eventually(t, "queued PendingCompaction persisted", func() bool {
		pc := h.record(child.ID).PendingCompaction
		return pc != nil && pc.Resume && pc.Stage == compactStageQueued
	})
	h.restart()
	wait(t, "rejoin with queued compact", h.runner.rejoined)

	c := h.runner.conv(child.ID)
	eventually(t, "rejoined turn open with the request restored", func() bool {
		var ok bool
		c.do(func() { ok = c.turn && c.proc != nil && !c.replaying && c.compactPending != nil })
		return ok
	})
	bt := newBatchTracker("t")
	c.do(func() {
		// The restart reset every latch; post-rejoin usage past 120% must arm
		// enforcement again even though the request is only queued.
		if c.compactHardSteered || c.compactHardInterrupt {
			t.Error("latches survived the restart")
		}
		c.compactHard = wsconfig.Threshold{Tokens: 200_000}
		c.handle(hardUsage(250_000, 320_000))
	})
	toolBoundary(c, &bt, "a", 1)
	c.do(func() {
		if !c.compactHardInterrupt {
			t.Error("restored queued request suppressed Stage 2 after restart")
			return
		}
		c.summaryReason = wslive.TurnAborted
		c.endTurn("", false)
		c.summaryReason = ""
	})
	eventually(t, "queued /compact dispatched once after the interrupted turn ended", func() bool {
		return countCompactInputs(h.userInputs(holder)) == 1
	})
	if reps := got.list(); len(reps) != 0 {
		t.Fatalf("parent settled before the queued compaction ran: %+v", reps)
	}
	compactBoundary(h, child.ID, holder)
	eventually(t, "resumed continuation sent", func() bool {
		in := h.userInputs(holder)
		return len(in) >= 3 && strings.Contains(in[len(in)-1], "Continue the work that was pending.")
	})
	if n := countCompactInputs(h.userInputs(holder)); n != 1 {
		t.Fatalf("compaction dispatched %d times", n)
	}
}
