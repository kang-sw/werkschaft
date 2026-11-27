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
