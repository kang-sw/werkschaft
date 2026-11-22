package wsagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
)

// compactRingTriggers are the triggers of the compaction entries that landed.
func compactRingTriggers(h *leadHarness, id string) []string {
	var out []string
	for _, e := range h.ring(id) {
		if e.Kind == wslive.RingCompact && !e.CompactFailed {
			out = append(out, e.Trigger)
		}
	}
	return out
}

// A compaction ws requested is recorded with that request's origin even though
// the harness reports its own trigger for the command that ran it, and the
// runner's state shows the compaction queued/running, then its count and
// latest origin.
func TestCompactionStateAndOriginAttribution(t *testing.T) {
	h := compactionHarness(t)
	id, holder := h.runLead("one")
	key := h.record(id).WSKey
	h.holders.out(holder, turnLines("sess-1", "u1", "first")...)
	h.idle(id)
	if st := h.state(id); st.Compacting || st.Compactions != 0 || st.CompactOrigin != "" {
		t.Fatalf("fresh state: %+v", st)
	}

	// ws auto: the harness reports "manual" for the /compact it was sent.
	h.runner.conv(id).do(func() {
		c := h.runner.conv(id)
		c.compactPending = &compactRequest{auto: true}
		c.publishCompaction(false)
		c.kick()
	})
	if !h.state(id).Compacting {
		t.Fatal("a queued auto compaction is not shown as compacting")
	}
	eventually(t, "auto compact dispatched", func() bool { return strings.Join(h.userInputs(holder), "|") != "" && countCompactInputs(h.userInputs(holder)) == 1 })
	if !h.state(id).Compacting {
		t.Fatal("a running compaction is not shown as compacting")
	}
	compactBoundary(h, id, holder)
	h.idle(id)
	st := h.state(id)
	if st.Compacting || st.Compactions != 1 || st.CompactOrigin != wslive.CompactOriginAuto {
		t.Fatalf("after ws auto: %+v", st)
	}
	if got := compactRingTriggers(h, id); len(got) != 1 || got[0] != wslive.TriggerAuto {
		t.Fatalf("ws auto recorded as %v", got)
	}

	// A request through ws's compact tool is manual.
	resume := false
	if out := h.runner.Compact(key, "", &resume); out.Status != "queued" {
		t.Fatal(out)
	}
	if !h.state(id).Compacting {
		t.Fatal("a queued manual compaction is not shown as compacting")
	}
	eventually(t, "manual compact dispatched", func() bool { return countCompactInputs(h.userInputs(holder)) == 2 })
	compactBoundary(h, id, holder)
	h.idle(id)
	st = h.state(id)
	if st.Compacting || st.Compactions != 2 || st.CompactOrigin != wslive.CompactOriginManual {
		t.Fatalf("after manual: %+v", st)
	}
	if got := compactRingTriggers(h, id); len(got) != 2 || got[1] != wslive.TriggerManual {
		t.Fatalf("manual recorded as %v", got)
	}

	// The harness compacting on its own: shown while it runs, and its own
	// trigger stays on the record.
	h.holders.out(holder, `{"type":"system","subtype":"status","status":"compacting"}`)
	eventually(t, "native compaction shown", func() bool { return h.state(id).Compacting })
	h.holders.out(holder, `{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"auto"}}`, resultLine(), stateLine(HarnessIdle))
	h.sync(id, holder)
	h.idle(id)
	st = h.state(id)
	if st.Compacting || st.Compactions != 3 || st.CompactOrigin != wslive.CompactOriginNative {
		t.Fatalf("after native: %+v", st)
	}
	if got := compactRingTriggers(h, id); len(got) != 3 || got[2] != wslive.TriggerAuto {
		t.Fatalf("native recorded as %v", got)
	}
	// A duplicate end counts nothing.
	h.runner.conv(id).do(func() {
		h.runner.conv(id).compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd})
	})
	if got := h.state(id).Compactions; got != 3 {
		t.Fatalf("duplicate end counted: %d", got)
	}
}

func countCompactInputs(inputs []string) int {
	n := 0
	for _, in := range inputs {
		if in == "/compact" {
			n++
		}
	}
	return n
}

// A failed compaction ends the compacting state without counting.
func TestCompactionStateFailedIsNotCounted(t *testing.T) {
	h := compactionHarness(t)
	id, holder := h.runLead("one")
	key := h.record(id).WSKey
	h.holders.out(holder, turnLines("sess-1", "u1", "first")...)
	h.idle(id)
	resume := false
	if out := h.runner.Compact(key, "", &resume); out.Status != "queued" {
		t.Fatal(out)
	}
	eventually(t, "compact dispatched", func() bool { return countCompactInputs(h.userInputs(holder)) == 1 })
	h.holders.out(holder, `{"type":"system","subtype":"status","status":"compacting"}`)
	h.sync(id, holder)
	h.runner.conv(id).do(func() {
		c := h.runner.conv(id)
		c.compactCommand = false
		c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
		c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
	})
	st := h.state(id)
	if st.Compacting || st.Compactions != 0 || st.CompactOrigin != "" {
		t.Fatalf("failed compaction: %+v", st)
	}
}

// A hosted Pi worker's compaction state reaches its parent's agent row: the
// compacting flag toggles, and the count and latest origin follow completion,
// a ws auto request recorded as auto though pi names "manual" for its command.
func TestCompactionStateReachesTheParentsAgentRow(t *testing.T) {
	h, _ := newFakePiRecoveryHarness(t)
	reg := h.runner.opts.Backends[BackendPi]
	reg.NativeCompaction = PiCompaction()
	h.runner.opts.Backends[BackendPi] = reg
	lead := h.lead(BackendClaude, LeadOptions{})
	child, err := h.runner.Spawn(SpawnSpec{Profile: PiWorkerProfile, Root: h.root, Prompt: "work", Parent: &ParentRef{Kind: ParentLead, ReplyID: lead.ID}})
	if err != nil {
		t.Fatal(err)
	}
	holder := h.holder(child.ID)
	h.holders.out(holder, `{"type":"agent_start"}`, `{"type":"agent_settled"}`)
	h.idle(child.ID)
	row := func() wslive.Agent {
		for _, a := range h.snapshot(lead.ID).Agents {
			if a.ID == "pi:"+child.ID {
				return a
			}
		}
		return wslive.Agent{}
	}
	eventually(t, "the child's row", func() bool { return row().ID != "" })
	if r := row(); r.Compacting || r.Compactions != 0 || r.CompactOrigin != "" {
		t.Fatalf("fresh row: %+v", r)
	}
	h.runner.conv(child.ID).do(func() {
		c := h.runner.conv(child.ID)
		c.compactPending = &compactRequest{auto: true}
		c.publishCompaction(false)
		c.kick()
	})
	eventually(t, "row shows compacting", func() bool { return row().Compacting })
	eventually(t, "worker RPC compact", func() bool { return len(h.holders.inputs(holder, "compact")) == 1 })
	h.holders.out(holder, `{"type":"compaction_start","reason":"manual"}`, `{"type":"compaction_end","reason":"manual"}`, `{"type":"response","command":"compact","success":true}`)
	eventually(t, "row shows the completed compaction", func() bool {
		r := row()
		return !r.Compacting && r.Compactions == 1 && r.CompactOrigin == wslive.CompactOriginAuto
	})
}

// State, the agent event and the row carry the fields only while set.
func TestCompactionStateProjectionOmitsZero(t *testing.T) {
	ev := SpawnAgentEvent(State{ID: "c1", Status: StatusIdle}, "key")
	raw, _ := json.Marshal(ev)
	for _, k := range []string{"compacting", "compactions", "compactOrigin"} {
		if strings.Contains(string(raw), k) {
			t.Fatalf("zero event carries %s: %s", k, raw)
		}
	}
	raw, _ = json.Marshal(State{ID: "c1"})
	for _, k := range []string{"compacting", "compactions", "compactOrigin"} {
		if strings.Contains(string(raw), k) {
			t.Fatalf("zero state carries %s: %s", k, raw)
		}
	}
	ev = SpawnAgentEvent(State{ID: "c1", Status: StatusRunning, Compacting: true, Compactions: 2, CompactOrigin: wslive.CompactOriginNative}, "key")
	if !ev.Compacting || ev.Compactions != 2 || ev.CompactOrigin != wslive.CompactOriginNative {
		t.Fatalf("event: %+v", ev)
	}
	ev = SpawnAgentEvent(State{ID: "c1", Status: StatusClosed, Compacting: true, Compactions: 2, CompactOrigin: wslive.CompactOriginAuto}, "key")
	if ev.Compacting || ev.Compactions != 2 {
		t.Fatalf("closed row keeps compacting or loses the count: %+v", ev)
	}
}
