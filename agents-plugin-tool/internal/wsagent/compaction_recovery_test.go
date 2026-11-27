package wsagent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestReplayCompactionOnlyRestoresCrossingFrontier(t *testing.T) {
	r := NewRunner(Options{Backends: map[string]BackendRegistration{BackendClaude: {NativeCompaction: fixtureCompaction()}}})
	c := &conv{r: r, rec: Record{Backend: BackendClaude, WSKey: "key"}}
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
		h.holders.bodies["get_context_usage"] = `{"totalTokens":65000,"rawMaxTokens":100000}`
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
			if strings.Contains(input, "Context usage is 65000 tokens") {
				return true
			}
		}
		return false
	})
}

func TestWorkerResumeCompactionSurvivesDaemonRestart(t *testing.T) {
	setupWorker := func(t *testing.T, probe *restoreProbe) (*leadHarness, *reports, State, string) {
		t.Helper()
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
		return h, got, child, holder
	}

	t.Run("running_across_restart_does_not_publish_held_or_sleep", func(t *testing.T) {
		probe := &restoreProbe{state: "post-restart"}
		h, got, child, holder := setupWorker(t, probe)

		if res := h.runner.Compact(h.record(child.ID).WSKey, "", nil); res.Status != "queued" {
			t.Fatal(res)
		}
		h.holders.out(holder, recoveryWorkerReply("Compaction queued; resuming...")...)
		eventually(t, "worker /compact dispatched", func() bool { return countCompactInputs(h.userInputs(holder)) == 1 })
		eventually(t, "worker PendingCompaction stage=running persisted", func() bool {
			pc := h.record(child.ID).PendingCompaction
			return pc != nil && pc.Resume && pc.Stage == compactStageRunning
		})

		h.restart()
		wait(t, "rejoin after restart while compacting", h.runner.rejoined)

		// Must not publish held "Compaction queued; resuming..." or sleep worker.
		time.Sleep(50 * time.Millisecond)
		if reps := got.list(); len(reps) != 0 {
			t.Fatalf("parent received held pre-compaction message across restart: %+v", reps)
		}
		if rec := h.record(child.ID); rec.HolderID != holder {
			t.Fatalf("worker slept prematurely across restart: holder=%q want %q", rec.HolderID, holder)
		}

		compactBoundary(h, child.ID, holder)
		eventually(t, "resumed continue turn sent after live CompactEnd", func() bool {
			return len(h.userInputs(holder)) == 3
		})
		resumed := h.userInputs(holder)[2]
		if !strings.Contains(resumed, "restore[post-restart]") || !strings.Contains(resumed, "cause=compaction") || !strings.Contains(resumed, "Continue the work that was pending.") {
			t.Fatalf("resumed turn after restart = %q", resumed)
		}
		if strings.Contains(resumed, "cause=restart") {
			t.Fatalf("resumed turn unexpectedly carried cause=restart: %q", resumed)
		}
		eventually(t, "PendingCompaction cleared after resume sent", func() bool {
			return h.record(child.ID).PendingCompaction == nil
		})

		h.holders.out(holder, recoveryWorkerReply("resumed final after restart")...)
		eventually(t, "parent receives final report after resumed turn", func() bool { return len(got.list()) == 1 })
		if rep := got.list()[0]; rep.Result.Status != TurnDone || rep.Result.Message != "resumed final after restart" {
			t.Fatalf("unexpected report: %+v", rep)
		}
	})

	t.Run("completed_in_missed_ring_resumes_immediately_after_reattach", func(t *testing.T) {
		probe := &restoreProbe{state: "post-ring"}
		h, got, child, holder := setupWorker(t, probe)

		if res := h.runner.Compact(h.record(child.ID).WSKey, "", nil); res.Status != "queued" {
			t.Fatal(res)
		}
		h.holders.out(holder, recoveryWorkerReply("Compaction queued; resuming...")...)
		eventually(t, "worker /compact dispatched", func() bool { return countCompactInputs(h.userInputs(holder)) == 1 })

		h.runner.Close()
		h.holders.restart()
		// Emit CompactStart + CompactEnd into the holder while no daemon runs.
		h.holders.out(holder,
			`{"type":"system","subtype":"status","status":"compacting"}`,
			`{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"manual"}}`,
			resultLine(),
			stateLine(HarnessIdle),
		)
		h.start()
		wait(t, "rejoin after offline CompactEnd", h.runner.rejoined)

		eventually(t, "resumed continue turn sent on reattach", func() bool {
			return len(h.userInputs(holder)) == 3
		})
		if reps := got.list(); len(reps) != 0 {
			t.Fatalf("parent settled before resumed turn finished: %+v", reps)
		}
		resumed := h.userInputs(holder)[2]
		if !strings.Contains(resumed, "restore[post-ring]") || !strings.Contains(resumed, "cause=compaction") || !strings.Contains(resumed, "Continue the work that was pending.") {
			t.Fatalf("resumed turn = %q", resumed)
		}

		h.holders.out(holder, recoveryWorkerReply("final after missed CompactEnd")...)
		eventually(t, "parent receives final report", func() bool { return len(got.list()) == 1 })
		if rep := got.list()[0]; rep.Result.Message != "final after missed CompactEnd" {
			t.Fatalf("unexpected report: %+v", rep)
		}
	})

	t.Run("queued_mid_turn_before_restart_compacts_and_resumes", func(t *testing.T) {
		probe := &restoreProbe{state: "queued-resume"}
		h, got, child, holder := setupWorker(t, probe)

		if res := h.runner.Compact(h.record(child.ID).WSKey, "", nil); res.Status != "queued" {
			t.Fatal(res)
		}
		eventually(t, "worker PendingCompaction stage=queued persisted", func() bool {
			pc := h.record(child.ID).PendingCompaction
			return pc != nil && pc.Resume && pc.Stage == compactStageQueued
		})

		h.restart()
		wait(t, "rejoin with queued compact", h.runner.rejoined)

		h.holders.out(holder, recoveryWorkerReply("Compaction queued; resuming...")...)
		eventually(t, "worker /compact dispatched after turn 1 ended post-restart", func() bool {
			return countCompactInputs(h.userInputs(holder)) == 1
		})
		if reps := got.list(); len(reps) != 0 {
			t.Fatalf("parent settled before queued /compact ran: %+v", reps)
		}

		compactBoundary(h, child.ID, holder)
		eventually(t, "resumed continue turn sent", func() bool { return len(h.userInputs(holder)) == 3 })
		h.holders.out(holder, recoveryWorkerReply("final after queued restart")...)
		eventually(t, "parent receives final report", func() bool { return len(got.list()) == 1 })
		if rep := got.list()[0]; rep.Result.Message != "final after queued restart" {
			t.Fatalf("unexpected report: %+v", rep)
		}
	})

	t.Run("compact_failed_before_reattach_releases_held_report", func(t *testing.T) {
		probe := &restoreProbe{state: "failed-resume"}
		h, got, child, holder := setupWorker(t, probe)

		if res := h.runner.Compact(h.record(child.ID).WSKey, "", nil); res.Status != "queued" {
			t.Fatal(res)
		}
		h.holders.out(holder, recoveryWorkerReply("held turn 1 report")...)
		eventually(t, "worker /compact dispatched", func() bool { return countCompactInputs(h.userInputs(holder)) == 1 })

		h.runner.Close()
		h.holders.restart()
		// Compaction turn ends without CompactEnd while daemon is offline.
		h.holders.out(holder, resultLine(), stateLine(HarnessIdle))
		h.start()
		wait(t, "rejoin after failed compact", h.runner.rejoined)

		eventually(t, "parent receives held turn 1 report on failed compaction replay", func() bool { return len(got.list()) == 1 })
		if rep := got.list()[0]; rep.Result.Status != TurnDone || rep.Result.Message != "held turn 1 report" {
			t.Fatalf("unexpected report after failed compaction: %+v", rep)
		}
		eventually(t, "PendingCompaction cleared after failed compaction", func() bool {
			return h.record(child.ID).PendingCompaction == nil
		})
	})
}
