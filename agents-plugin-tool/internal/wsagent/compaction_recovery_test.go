package wsagent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestReplayCompactionOnlyRestoresCrossingFrontier(t *testing.T) {
	r := NewRunner(Options{Backends: map[string]BackendRegistration{BackendPi: {NativeCompaction: PiCompaction()}}})
	c := &conv{r: r, rec: Record{Backend: BackendPi, WSKey: "key"}}
	start := wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactStart}
	end := wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd}
	c.replayCompaction(start)
	c.replayCompaction(end)
	if c.compacting || c.compactNote != "" {
		t.Fatal("historical compaction left a restore")
	}
	c.replayCompaction(start)
	c.compactEvent(end)
	if c.compacting || !strings.Contains(c.compactNote, "Context was compacted") || c.compactResume {
		t.Fatalf("crossing restore or inferred continuation: compacting=%v note=%q resume=%v", c.compacting, c.compactNote, c.compactResume)
	}
	old := c.compactNote
	c.compactEvent(end)
	if c.compactNote != old {
		t.Fatal("duplicate end changed restore")
	}
}

func TestClaudeRecoveryUsageRequiresRealTokens(t *testing.T) {
	m := newClaudeMapper(StartSpec{Lead: &LeadSpec{}})
	line := func(body string) []Event {
		var l claudeLine
		if err := json.Unmarshal([]byte(`{"type":"control_response","response":{"subtype":"success","request_id":"ws-recovery-context","response":`+body+`}}`), &l); err != nil {
			t.Fatal(err)
		}
		return m.recoveryContextUsage(l)
	}
	if got := line(`{"rawMaxTokens":100000}`); len(got) != 0 {
		t.Fatalf("missing tokens became fresh usage: %+v", got)
	}
	got := line(`{"rawMaxTokens":100000,"totalTokens":55000}`)
	if len(got) != 1 || got[0].Live == nil || got[0].Live.Context == nil || *got[0].Live.Context.Tokens != 55000 || got[0].Live.Context.Window != 100000 {
		t.Fatalf("query observation = %+v", got)
	}
}

func TestGracefulDrainCompletesDispatchedCompaction(t *testing.T) {
	h := compactionHarness(t)
	probe := &restoreProbe{state: "fresh"}
	h.runner.opts.RestoreBundle = probe.render
	id, holder := h.runLead("one")
	if out := h.runner.Compact(h.record(id).WSKey, "", nil); out.Status != "queued" {
		t.Fatal(out)
	}
	h.holders.out(holder, turnLines("sess-1", "u1", "done")...)
	eventually(t, "compact dispatched", func() bool { return len(h.userInputs(holder)) == 2 })
	done := make(chan struct{})
	go func() { h.runner.Close(); close(done) }()
	h.holders.out(holder, `{"type":"system","subtype":"status","status":"compacting"}`, `{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"manual"}}`, resultLine(), stateLine(HarnessIdle))
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	inputs := h.userInputs(holder)
	if len(inputs) != 3 || !strings.Contains(inputs[2], "restore[fresh]") || !strings.Contains(inputs[2], "Continue the work") {
		t.Fatalf("shutdown lost restore/continuation: %q", inputs)
	}
}

func TestRejoinCompactionSpanningSwap(t *testing.T) {
	h := compactionHarness(t)
	probe := &restoreProbe{state: "fresh"}
	h.runner.opts.RestoreBundle = probe.render
	h.opts.RestoreBundle = probe.render
	id, holder := h.runLead("one")
	h.holders.out(holder, `{"type":"system","subtype":"status","status":"compacting"}`)
	eventually(t, "compaction start applied", func() bool {
		var active bool
		h.runner.conv(id).do(func() { active = h.runner.conv(id).compacting })
		return active
	})
	h.restart()
	wait(t, "rejoin", h.runner.rejoined)
	h.holders.out(holder, `{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"manual"}}`, resultLine(), stateLine(HarnessIdle))
	h.idle(id)
	h.send(id, "next")
	eventually(t, "restore sent with next input", func() bool {
		inputs := h.userInputs(holder)
		return len(inputs) == 2 && strings.Contains(inputs[1], "restore[fresh]")
	})
	h.holders.out(holder, turnLines("sess-1", "u2", "done")...)
	h.idle(id)
	h.restart()
	wait(t, "second rejoin", h.runner.rejoined)
	h.send(id, "later")
	eventually(t, "later input", func() bool { return len(h.userInputs(holder)) == 3 })
	if strings.Contains(h.userInputs(holder)[2], "cause=compaction") || probe.snapshot()[0].cause != RestoreCompaction {
		t.Fatalf("historic compaction restored again: %q / %+v", h.userInputs(holder), probe.snapshot())
	}
	for _, call := range probe.snapshot()[1:] {
		if call.cause == RestoreCompaction {
			t.Fatalf("historic compaction restored again: %+v", probe.snapshot())
		}
	}
}

func TestRejoinQueriesClaudeUsage(t *testing.T) {
	h := compactionHarness(t)
	h.holders.set(func() {
		h.holders.bodies["get_context_usage"] = `{"totalTokens":55000,"rawMaxTokens":100000}`
	})
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "done")...)
	h.idle(id)
	h.restart()
	wait(t, "rejoin", h.runner.rejoined)
	eventually(t, "recovery query", func() bool {
		for _, line := range h.holders.inputs(holder, "control_request") {
			if strings.Contains(line, claudeRecoveryContextAsk) {
				return true
			}
		}
		return false
	})
	eventually(t, "query usage rejudged", func() bool {
		for _, input := range h.userInputs(holder) {
			if strings.Contains(input, "Context usage is 55000 tokens") {
				return true
			}
		}
		return false
	})
}

func TestRejoinQueriesPiUsage(t *testing.T) {
	h, _ := newFakePiRecoveryHarness(t)
	child, holder := spawnRecoveryWorker(t, h)
	before := len(h.holders.inputs(holder, "get_session_stats"))
	h.restart()
	wait(t, "rejoin", h.runner.rejoined)
	eventually(t, "Pi recovery query", func() bool {
		return h.runner.conv(child.ID) != nil && len(h.holders.inputs(holder, "get_session_stats")) > before
	})
}
