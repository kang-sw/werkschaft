package wsagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wslive"
)

func hardUsage(tokens, window int64) Event {
	return Event{Live: &wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &tokens, Window: window}}}
}

// toolBoundary feeds one single-call tool batch: its result is a ToolBoundary.
func toolBoundary(c *conv, bt *batchTracker, id string, seq int64) {
	c.do(func() {
		for _, ev := range bt.addCall("b", id, "tool", "", 0, seq) {
			c.handle(ev)
		}
		for _, ev := range bt.endCall(id, false, 0, seq+1) {
			c.handle(ev)
		}
	})
}

func TestHardCapStage1LeavesTurnRunningStage2OnlyAtBoundary(t *testing.T) {
	h := compactionHarness(t)
	id, holder := h.runLead("work")
	c := h.runner.conv(id)
	c.do(func() {
		c.compactHard = wsconfig.Threshold{Tokens: 250_000}
		c.handle(hardUsage(265_000, 1_000_000))
		if !c.compactHardSteered || c.compactHardInterrupt || !c.turn {
			t.Error("Stage 1 must steer and leave the turn running")
		}
		// Mid-tool usage far above 120% must not interrupt.
		c.handle(hardUsage(400_000, 1_000_000))
		if c.compactHardInterrupt {
			t.Error("Stage 2 fired on a status event, not a tool boundary")
		}
	})
	bt := newBatchTracker("t")
	for i, want := range []bool{false, false, true} {
		// Keep usage inside 120% so only the ignored-boundary count escalates.
		c.do(func() { c.handle(hardUsage(265_000, 1_000_000)) })
		toolBoundary(c, &bt, string(rune('a'+i)), int64(10*i))
		c.do(func() {
			if c.compactHardInterrupt != want {
				t.Errorf("boundary %d: interrupt=%v want %v", i+1, c.compactHardInterrupt, want)
			}
		})
	}
	if got := h.userInputs(holder); len(got) < 2 {
		t.Fatalf("Stage 1 steers missing: %q", got)
	}
}

func TestHardCapStage2OverflowCeilingRotatesLeadAndResumes(t *testing.T) {
	h := compactionHarness(t)
	id, holder := h.runLead("work")
	c := h.runner.conv(id)
	c.do(func() {
		c.compactHard = wsconfig.Threshold{Tokens: 250_000}
		c.handle(hardUsage(265_000, 1_000_000))
		c.handle(hardUsage(300_000, 1_000_000))
	})
	bt := newBatchTracker("t")
	toolBoundary(c, &bt, "a", 1)
	c.do(func() {
		if !c.compactHardInterrupt || c.interruptAsked {
			t.Errorf("120%% boundary: hardInterrupt=%v interruptAsked=%v", c.compactHardInterrupt, c.interruptAsked)
		}
		c.summaryReason = wslive.TurnAborted
		c.endTurn("", false)
		c.summaryReason = ""
		if c.compactHardInterrupt {
			t.Error("endTurn left the interrupt flag set")
		}
	})
	eventually(t, "lead rotated without Stage 1 prose", func() bool {
		rec, err := h.readRecord(id)
		return err == nil && rec.HolderID != "" && rec.HolderID != holder
	})
	for _, in := range h.userInputs(holder) {
		if strings.Contains(in, "lead_summary") || in == "/compact" {
			t.Fatalf("overflowed lead got %q instead of direct rotation", in)
		}
	}
	next := h.holder(id)
	eventually(t, "resume after rotation", func() bool {
		in := h.userInputs(next)
		return len(in) >= 1 && strings.Contains(in[0], "Continue the work that was pending.")
	})
	c.do(func() {
		if c.compactNoShrink != 0 || c.compactHardHandled || c.compactHardSteered {
			t.Error("rotation did not reset hard-cap state")
		}
	})
}

func TestHardCapClaudeWorkerCeilingRespectsCompactingAndHoldsReport(t *testing.T) {
	h, id, holder := wsWorkerHarness(t)
	c := h.runner.conv(id)
	bt := newBatchTracker("t")
	c.do(func() {
		c.compactHard = wsconfig.Threshold{Tokens: 200_000}
		c.handle(hardUsage(220_000, 320_000))
	})
	toolBoundary(c, &bt, "a", 1)
	c.do(func() {
		if c.compactHardInterrupt || c.compactHardSteered {
			t.Error("claude worker escalated below the 120% headroom")
		}
		c.handle(hardUsage(250_000, 320_000))
		c.compacting = true
	})
	toolBoundary(c, &bt, "b", 10)
	c.do(func() {
		if c.compactHardInterrupt {
			t.Error("escalated while the harness was compacting")
		}
		c.compacting = false
	})
	toolBoundary(c, &bt, "c", 20)
	c.do(func() {
		if !c.compactHardInterrupt {
			t.Error("claude worker at >=120% did not escalate")
			return
		}
		settledBefore := c.settled.n
		c.summaryReason = wslive.TurnAborted
		c.endTurn("", false)
		c.summaryReason = ""
		if c.settled.n != settledBefore {
			t.Errorf("premature TurnDone published: settled %d -> %d", settledBefore, c.settled.n)
		}
	})
	eventually(t, "worker rotated", func() bool {
		rec, err := h.readRecord(id)
		return err == nil && rec.HolderID != "" && rec.HolderID != holder
	})
	next := h.holder(id)
	eventually(t, "worker resumed", func() bool {
		in := h.userInputs(next)
		return len(in) >= 1 && strings.Contains(in[0], "Continue the work that was pending.")
	})
	if slices.Contains(h.userInputs(holder), "/compact") {
		t.Fatal("ws-mode worker must not send native /compact")
	}
}

func TestHardCapNoShrinkLatchIsBoundedAndResets(t *testing.T) {
	h := compactionHarness(t)
	id, _ := h.runLead("work")
	c := h.runner.conv(id)
	endCycle := func() {
		for _, phase := range []string{wslive.CompactStart, wslive.CompactEnd} {
			c.handle(Event{Live: &wslive.Event{Kind: wslive.KindCompact, Phase: phase, Trigger: wslive.TriggerAuto}})
		}
	}
	c.do(func() {
		c.compactHard = wsconfig.Threshold{Tokens: 250_000}
		c.handle(hardUsage(265_000, 1_000_000))
		endCycle()
		if c.compactHardHandled || c.compactNoShrink != 1 {
			t.Errorf("first no-shrink: handled=%v n=%d", c.compactHardHandled, c.compactNoShrink)
		}
		endCycle()
		if !c.compactHardHandled || c.compactNoShrink != compactNoShrinkCap {
			t.Errorf("cap not reached: handled=%v n=%d", c.compactHardHandled, c.compactNoShrink)
		}
		// A sample below hard re-arms protection.
		c.compactAwaitTurn = false
		c.handle(hardUsage(100_000, 1_000_000))
		if c.compactHardHandled || c.compactNoShrink != 0 {
			t.Errorf("not reset below hard: handled=%v n=%d", c.compactHardHandled, c.compactNoShrink)
		}
	})
}

func TestHardCapStage2IgnoresQueuedPreSteerBoundaries(t *testing.T) {
	h := compactionHarness(t)
	id, _ := h.runLead("work")
	c := h.runner.conv(id)
	c.do(func() {
		c.compactHard = wsconfig.Threshold{Tokens: 250_000}
		// Trigger Stage 1 steer without any subsequent model step yet.
		c.handle(hardUsage(265_000, 1_000_000))
		if !c.compactHardSteered || c.compactHardStepPending {
			t.Errorf("expected steered=true stepPending=false, got steered=%v stepPending=%v", c.compactHardSteered, c.compactHardStepPending)
		}
	})
	bt := newBatchTracker("t")
	// 5 sequential 1-call tool completions from the pre-steer model step must
	// not trip Stage 2 before the model runs a post-steer step.
	for i := 0; i < 5; i++ {
		toolBoundary(c, &bt, string(rune('a'+i)), int64(10*i))
	}
	c.do(func() {
		if c.compactHardInterrupt || c.compactHardBoundaries != 0 {
			t.Errorf("pre-steer queued tool burst tripped Stage 2: interrupt=%v boundaries=%d", c.compactHardInterrupt, c.compactHardBoundaries)
		}
	})
	// Now simulate 3 genuine post-steer model steps, each with 2 sequential tool
	// completions: each step increments compactHardBoundaries at most once.
	for step := 1; step <= 3; step++ {
		c.do(func() { c.handle(hardUsage(265_000, 1_000_000)) })
		toolBoundary(c, &bt, string(rune('k'+step*2)), int64(100+step*20))
		if step < 3 {
			toolBoundary(c, &bt, string(rune('l'+step*2)), int64(110+step*20))
		}
		wantInterrupt := step == 3
		c.do(func() {
			if c.compactHardInterrupt != wantInterrupt {
				t.Errorf("step %d: interrupt=%v want %v (boundaries=%d)", step, c.compactHardInterrupt, wantInterrupt, c.compactHardBoundaries)
			}
			if !wantInterrupt && c.compactHardBoundaries != step {
				t.Errorf("step %d: boundaries=%d want %d", step, c.compactHardBoundaries, step)
			}
		})
	}
}

func TestHardCapTurnRotationCapAbortsWorkerAndPausesLead(t *testing.T) {
	t.Run("worker_aborts_and_reports_failed_turn", func(t *testing.T) {
		h, id, _ := wsWorkerHarness(t)
		c := h.runner.conv(id)
		bt := newBatchTracker("t")
		c.do(func() {
			c.rec.Parent = &ParentRef{Kind: ParentLead, ReplyID: "leadparent"}
			c.rec.TurnOpen = true
			c.compactHard = wsconfig.Threshold{Tokens: 200_000}
			c.compactTurnRotations = compactTurnRotationCap
			c.handle(hardUsage(250_000, 320_000))
		})
		toolBoundary(c, &bt, "a", 1)
		c.do(func() {
			if !c.compactHardAbort || c.compactHardInterrupt {
				t.Errorf("expected compactHardAbort=true compactHardInterrupt=false, got abort=%v int=%v", c.compactHardAbort, c.compactHardInterrupt)
				return
			}
			settledBefore := c.settled.n
			c.summaryReason = wslive.TurnAborted
			c.endTurn("", false)
			c.summaryReason = ""
			if c.compactPending != nil {
				t.Errorf("worker staged a 4th compaction after hitting rotation cap")
			}
			if c.settled.n != settledBefore+1 {
				t.Errorf("expected TurnFailed settlement, settled %d -> %d", settledBefore, c.settled.n)
			}
			if c.settled.Status != TurnFailed || !strings.Contains(c.settled.Error, "compaction rotation cap reached") {
				t.Errorf("unexpected settled result: %+v", c.settled)
			}
			if c.compactTurnRotations != 0 {
				t.Errorf("compactTurnRotations not reset after cap abort: %d", c.compactTurnRotations)
			}
		})
	})

	t.Run("lead_pauses_auto_compaction_without_interrupt", func(t *testing.T) {
		h := compactionHarness(t)
		id, _ := h.runLead("work")
		c := h.runner.conv(id)
		bt := newBatchTracker("t")
		c.do(func() {
			c.compactHard = wsconfig.Threshold{Tokens: 250_000}
			c.compactTurnRotations = compactTurnRotationCap
			c.handle(hardUsage(265_000, 1_000_000))
			c.handle(hardUsage(310_000, 1_000_000))
		})
		toolBoundary(c, &bt, "a", 1)
		c.do(func() {
			if c.compactHardInterrupt || c.compactHardAbort {
				t.Errorf("lead should pause without interrupting: int=%v abort=%v", c.compactHardInterrupt, c.compactHardAbort)
			}
			if !c.compactHardHandled || !strings.Contains(c.lastErr, "auto-compaction paused") {
				t.Errorf("expected handled=true and paused diagnostic, got handled=%v lastErr=%q", c.compactHardHandled, c.lastErr)
			}
		})
	})
}

func TestHardCapNoShrinkSurvivesExternalRotation(t *testing.T) {
	h := compactionHarness(t)
	id, holder := h.runLead("work")
	c := h.runner.conv(id)
	c.do(func() {
		c.compactHard = wsconfig.Threshold{Tokens: 250_000}
		c.handle(hardUsage(265_000, 1_000_000))
		c.handle(hardUsage(305_000, 1_000_000))
	})
	bt := newBatchTracker("t")
	toolBoundary(c, &bt, "a", 1)
	c.do(func() {
		c.summaryReason = wslive.TurnAborted
		c.endTurn("", false)
		c.summaryReason = ""
	})
	eventually(t, "first rotation completed", func() bool {
		rec, err := h.readRecord(id)
		return err == nil && rec.HolderID != "" && rec.HolderID != holder
	})
	holder2 := h.holder(id)
	eventually(t, "first continuation sent", func() bool {
		return len(h.userInputs(holder2)) >= 1
	})
	// Immediately after rotation, compactNoShrink is 0 until the first fresh
	// usage sample arrives; if that sample is still >= hard, it counts as 1.
	c.do(func() {
		if c.compactNoShrink != 0 || !c.compactPostRotationCheck || c.compactTurnRotations != 1 {
			t.Errorf("post-rotation state: noShrink=%d check=%v turnRot=%d", c.compactNoShrink, c.compactPostRotationCheck, c.compactTurnRotations)
			return
		}
		c.compactHard = wsconfig.Threshold{Tokens: 250_000}
		c.compactAwaitTurn = false
		c.handle(hardUsage(265_000, 1_000_000))
		if c.compactNoShrink != 1 || c.compactHardHandled {
			t.Errorf("after 1st no-shrink sample: noShrink=%d handled=%v", c.compactNoShrink, c.compactHardHandled)
			return
		}
		// Trigger a 2nd rotation that also fails to shrink.
		c.handle(hardUsage(305_000, 1_000_000))
	})
	toolBoundary(c, &bt, "b", 10)
	c.do(func() {
		c.summaryReason = wslive.TurnAborted
		c.endTurn("", false)
		c.summaryReason = ""
	})
	eventually(t, "second rotation completed", func() bool {
		rec, err := h.readRecord(id)
		return err == nil && rec.HolderID != "" && rec.HolderID != holder2
	})
	holder3 := h.holder(id)
	eventually(t, "second continuation sent", func() bool {
		return len(h.userInputs(holder3)) >= 1
	})
	c.do(func() {
		c.compactHard = wsconfig.Threshold{Tokens: 250_000}
		c.compactAwaitTurn = false
		c.handle(hardUsage(265_000, 1_000_000))
		if c.compactNoShrink != compactNoShrinkCap || !c.compactHardHandled || c.compactTurnRotations != 2 {
			t.Errorf("after 2nd no-shrink sample: noShrink=%d handled=%v turnRot=%d", c.compactNoShrink, c.compactHardHandled, c.compactTurnRotations)
		}
	})
}
