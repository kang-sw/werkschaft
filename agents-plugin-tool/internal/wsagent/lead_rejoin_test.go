package wsagent

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
)

// TestLeadRejoinsItsProcessAfterADaemonRestart: the daemon's end leaves a
// lead's process running under its holder. The next daemon attaches to it
// and rebuilds the conversation from the holder's replay of its whole
// ring: no second process, no entry written twice, the running turn and
// its waiting prompt back and answerable, the lines the harness wrote
// while no daemon ran applied once, and a wait with no prompt shown again.
func TestLeadRejoinsItsProcessAfterADaemonRestart(t *testing.T) {
	shortActionGrace(t)
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "first")...)
	h.idle(id)
	h.send(id, "two")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), toolUseLine("u2", "toolu_1"), stateLine(HarnessWaiting), canUseToolLine("req-1", "toolu_1"))
	eventually(t, "the prompt", func() bool { return len(h.state(id).Approvals) == 1 })
	// Mapper control sends are asynchronous. The fake appends each answer
	// under the input lock, so observe the launch's and both inits' asks
	// before capturing the holder's final sequence, including ws-context-2.
	eventually(t, "the automatic control answers to be queued", func() bool {
		inputs := h.holders.inputs(holder, "control_request")
		for _, request := range []string{"ws-commands", "ws-context-1", "ws-context-2"} {
			if !slices.ContainsFunc(inputs, func(line string) bool {
				return strings.Contains(line, `"request_id":"`+request+`"`)
			}) {
				return false
			}
		}
		return true
	})
	// The answers can follow the prompt; wait for their LineEnds too.
	target := uint64(h.holders.records(holder))
	c := h.runner.conv(id)
	eventually(t, "all holder records to be applied before shutdown", func() bool {
		var applied bool
		c.do(func() { applied = c.seq >= target })
		return applied
	})
	ringBefore := h.ring(id)
	inputsBefore := h.holders.inputs(holder, "")

	h.runner.Close()
	rec := h.record(id)
	if rec.HolderID != holder || rec.HolderSeq != uint64(h.holders.records(holder)) || h.holders.opCount("stop") != 0 {
		t.Fatalf("the daemon's end leaves the process running, with the applied sequence recorded: record %+v, %d holder records, holder calls %v", rec, h.holders.records(holder), h.holders.opList())
	}
	h.holders.restart()
	h.start()
	eventually(t, "the running turn and its prompt to be back", func() bool {
		st := h.state(id)
		return st.Process && st.Status == StatusRunning && len(st.Approvals) == 1 && st.Approvals[0].ID == "req-1"
	})
	eventually(t, "the permission wait in the snapshot", func() bool {
		lead := h.snapshot(id).Lead
		return lead.Permission != nil && lead.Permission.ToolName == "Bash"
	})
	// The prompt can be answered once the rejoin is over.
	eventually(t, "the answer to be taken", func() bool {
		_, err := h.runner.AnswerApproval(id, "req-1", true, "")
		if err != nil && requestStatus(err) != 409 {
			t.Fatalf("AnswerApproval: %v", err)
		}
		return err == nil
	})
	if got := h.holders.inputs(holder, "control_response"); len(got) != 1 || !strings.Contains(got[0], `"req-1"`) {
		t.Fatalf("answers written = %q", got)
	}
	if h.holders.opCount("launch") != 1 || h.holders.opCount("spawn") != 1 || h.holders.opCount("stop") != 0 {
		t.Fatalf("a rejoin starts and ends no process: %v", h.holders.opList())
	}
	if got := h.ring(id); !reflect.DeepEqual(got, ringBefore) {
		t.Fatalf("a rejoin writes no conversation entry:\n%+v\nbefore\n%+v", got, ringBefore)
	}
	eventually(t, "fresh context usage query after the rejoin", func() bool {
		return slices.ContainsFunc(h.holders.inputs(holder, "control_request"), func(line string) bool {
			return strings.Contains(line, `"request_id":"`+claudeRecoveryContextAsk+`"`) && strings.Contains(line, `"subtype":"get_context_usage"`)
		})
	})
	if got := h.holders.inputs(holder, ""); len(got) != len(inputsBefore)+2 || !slices.Equal(got[:len(inputsBefore)], inputsBefore) {
		t.Fatalf("rejoin writes only a usage query and the approval answer: %q", got)
	}

	// The harness works on while no daemon runs.
	h.runner.Close()
	h.holders.restart()
	h.holders.out(holder, stateLine(HarnessRunning), toolResultLine("toolu_1"), replyLine("u3", "second"), resultLine(), stateLine(HarnessIdle))
	h.start()
	eventually(t, "the lines written meanwhile", func() bool {
		return slices.Equal(ringTexts(h.ring(id), wslive.RingReply), []string{"first", "second"})
	})
	h.send(id, "three")
	eventually(t, "the next turn after the rejoin", func() bool { return slices.Equal(h.userInputs(holder), h.envelopes("one", "two", "three")) })
	if st := h.state(id); len(st.Approvals) != 0 || st.Status != StatusRunning || st.Error != "" {
		t.Fatalf("after the second rejoin: %+v", st)
	}

	// A wait with no prompt to answer: shown, and shown again by the next
	// daemon, until the harness moves on.
	h.holders.out(holder, stateLine(HarnessRunning), toolUseLine("u4", "toolu_2"), stateLine(HarnessWaiting))
	eventually(t, "the unanswerable wait", func() bool { return h.state(id).ActionNeeded != "" })
	logged := strings.Count(h.log.String(), "requires_action")
	h.restart()
	eventually(t, "the unanswerable wait after the rejoin", func() bool {
		st := h.state(id)
		return st.ActionNeeded != "" && st.Status == StatusRunning && len(st.Approvals) == 0
	})
	eventually(t, "the wait in the snapshot", func() bool {
		lead := h.snapshot(id).Lead
		return lead.Permission != nil && lead.Permission.ToolName == actionNeededTool
	})
	if strings.Count(h.log.String(), "requires_action") != logged+1 {
		t.Fatalf("the rejoined wait should be logged once more:\n%s", h.log.String())
	}
	h.holders.out(holder, stateLine(HarnessRunning), replyLine("u5", "third"), resultLine(), stateLine(HarnessIdle))
	h.idle(id)
	eventually(t, "the wait to end", func() bool { return h.state(id).ActionNeeded == "" && h.snapshot(id).Lead.Permission == nil })

	// Three daemons applied the stream; each entry is there once.
	ring := h.ring(id)
	if !slices.Equal(ringTexts(ring, wslive.RingUser), h.envelopes("one", "two", "three")) || !slices.Equal(ringTexts(ring, wslive.RingReply), []string{"first", "second", "third"}) {
		t.Fatalf("ring = %+v", ring)
	}
	if h.holders.opCount("launch") != 1 || h.holders.opCount("spawn") != 1 || h.holders.opCount("stop") != 0 {
		t.Fatalf("holder calls = %v", h.holders.opList())
	}

	// A holder that ended while no daemon ran: the process is over, and
	// the next message starts a new one on the same backend session.
	h.runner.Close()
	h.holders.restart()
	h.holders.vanish(holder)
	h.start()
	eventually(t, "the vanished process to be noticed", func() bool { return !h.state(id).Process && h.record(id).HolderID == "" })
	// processGone clears the process and record before writing the row.
	// Wait for the whole loop step before checking its persisted results.
	h.quiesce(id)
	if st := h.state(id); st.Status != StatusIdle || len(st.Approvals) != 0 {
		t.Fatalf("after the holder vanished: %+v", st)
	}
	if reg := h.row(id); reg.HostPID != 0 {
		t.Fatalf("the row names no process: %+v", reg)
	}
	h.send(id, "four")
	var second string
	eventually(t, "a new holder", func() bool { second = h.record(id).HolderID; return second != "" && second != holder })
	eventually(t, "the message on the new process", func() bool { return slices.Equal(h.userInputs(second), h.envelopes("four")) })
	if session, _ := flagValue(h.holders.spawned(second).Argv, "--resume"); session != "sess-1" {
		t.Fatalf("the new process resumes the session: %v", h.holders.spawned(second).Argv)
	}
}

// TestLeadRejoinOfAWrappedRingHoldsInput: a lead's holder keeps a ring
// larger than the manager's default, and a rejoin whose ring no longer
// holds the turn's start (its first record is past the one the record
// names) takes the turn as running: a message and mail sent meanwhile wait
// for the harness's next turn end, then go once.
func TestLeadRejoinOfAWrappedRingHoldsInput(t *testing.T) {
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	if got := h.holders.launchRing(holder); got != leadRingBytes {
		t.Fatalf("a lead's holder ring = %d bytes, want %d", got, leadRingBytes)
	}
	key := h.record(id).WSKey
	h.holders.out(holder, turnLines("sess-1", "u1", "first")...)
	h.idle(id)
	h.send(id, "two")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"))
	h.sync(id, holder)
	h.runner.Close()
	h.holders.restart()

	// The turn works on while no daemon runs, and its ring drops all but
	// the last record: a tool result, which tells nothing of the turn.
	h.holders.out(holder, toolResultLine("toolu_1"), toolResultLine("toolu_2"))
	h.holders.trim(holder, h.holders.records(holder)-1)
	h.start()
	eventually(t, "the rejoin to take the turn as running", func() bool {
		st := h.state(id)
		return st.Process && st.Status == StatusRunning
	})
	h.send(id, "three")
	h.mail.deliver(key, "From a@x: ping")
	eventually(t, "the mail to be claimed", func() bool { return slices.Equal(h.record(id).Mail, []string{"From a@x: ping"}) })
	h.sync(id, holder)
	if got := h.userInputs(holder); !slices.Equal(got, h.envelopes("one", "two")) || h.state(id).Held != 2 {
		t.Fatalf("input waits for the turn's end: written %q, state %+v", got, h.state(id))
	}
	if n := strings.Count(h.log.String(), "with no turn boundary after them"); n != 1 {
		t.Fatalf("the loss should be logged once, logged %d times:\n%s", n, h.log.String())
	}

	h.holders.out(holder, replyLine("u2", "second"), resultLine(), stateLine(HarnessIdle))
	eventually(t, "the held input as the next turn", func() bool {
		return slices.Equal(h.userInputs(holder), []string{h.envelope("one"), h.envelope("two"), h.envelope("three") + "\n\nFrom a@x: ping"})
	})
	h.sync(id, holder)
	if got := h.userInputs(holder); len(got) != 3 || h.state(id).Held != 0 {
		t.Fatalf("the held input goes once: written %q, state %+v", got, h.state(id))
	}
	if strings.Count(h.log.String(), "left its ring") != 1 {
		t.Fatalf("the loss should be logged once:\n%s", h.log.String())
	}
	if strings.Contains(h.log.String(), "whether a turn runs") || len(h.holders.inputs(holder, "get_state")) != 0 {
		t.Fatalf("a claude lead is not asked whether a turn runs:\n%s", h.log.String())
	}
}

// TestLeadRejoinOfARingThatWrappedWhileAttachedHoldsInput: a long turn
// pushes its own start out of the ring while the daemon is still attached,
// so the next daemon's rejoin finds the ring beginning no later than the
// record after the one the record names: nothing left it unseen, yet the
// replay shows no turn boundary. The rejoin takes the turn as running: a
// message sent meanwhile waits for the harness's next turn end, then goes
// once.
func TestLeadRejoinOfARingThatWrappedWhileAttachedHoldsInput(t *testing.T) {
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "first")...)
	h.idle(id)
	h.send(id, "two")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), toolResultLine("toolu_1"), toolResultLine("toolu_2"))
	h.sync(id, holder)
	h.holders.trim(holder, h.holders.records(holder)-1)
	h.runner.Close()
	first, applied := uint64(h.holders.records(holder)), h.record(id).HolderSeq
	if first <= 1 || first > applied+1 {
		t.Fatalf("the ring should begin past 1 and no later than the record after %d, begins at %d", applied, first)
	}
	h.holders.restart()
	h.start()
	eventually(t, "the rejoin to take the turn as running", func() bool {
		st := h.state(id)
		return st.Process && st.Status == StatusRunning
	})
	h.send(id, "three")
	h.sync(id, holder)
	if got := h.userInputs(holder); !slices.Equal(got, h.envelopes("one", "two")) || h.state(id).Held != 1 {
		t.Fatalf("the message waits for the turn's end: written %q, state %+v", got, h.state(id))
	}
	if n := strings.Count(h.log.String(), "with no turn boundary after them"); n != 1 {
		t.Fatalf("the loss should be logged once, logged %d times:\n%s", n, h.log.String())
	}

	h.holders.out(holder, replyLine("u2", "second"), resultLine(), stateLine(HarnessIdle))
	eventually(t, "the held message as the next turn", func() bool {
		return slices.Equal(h.userInputs(holder), h.envelopes("one", "two", "three"))
	})
	h.sync(id, holder)
	if got := h.userInputs(holder); len(got) != 3 || h.state(id).Held != 0 {
		t.Fatalf("the held message goes once: written %q, state %+v", got, h.state(id))
	}
}

// TestLeadRejoinOfAWrappedRingTrustsATurnBoundary: when the records after
// the loss show the turn's end, the rejoin takes the state they show, and a
// message sent after it goes at once.
func TestLeadRejoinOfAWrappedRingTrustsATurnBoundary(t *testing.T) {
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"))
	h.sync(id, holder)
	h.runner.Close()
	h.holders.restart()
	h.holders.out(holder, toolResultLine("toolu_1"), replyLine("u1", "first"), resultLine(), stateLine(HarnessIdle))
	h.holders.trim(holder, h.holders.records(holder)-2)
	h.start()
	eventually(t, "the rejoined session to be idle", func() bool {
		st := h.state(id)
		return st.Process && st.Status == StatusIdle
	})
	h.send(id, "two")
	eventually(t, "the message to go at once", func() bool { return slices.Equal(h.userInputs(holder), h.envelopes("one", "two")) })
	if log := h.log.String(); !strings.Contains(log, "the turn state is the one the records after them show") || strings.Contains(log, "with no turn boundary after them") {
		t.Fatalf("the loss should be logged as covered by a turn boundary:\n%s", log)
	}
}

// TestLeadRejoinGapHoldsInputAndDropsAPartialLine: records that leave the
// ring while the replay streams arrive as a gap. A line in progress before
// it is dropped, not spliced onto the line after it; and the turn boundary
// before the gap (an idle state) does not count, so the turn is taken as
// running until the harness ends one.
func TestLeadRejoinGapHoldsInputAndDropsAPartialLine(t *testing.T) {
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "first")...)
	h.idle(id)
	h.sync(id, holder)
	h.runner.Close()
	h.holders.restart()

	// While no daemon runs: a line read in two parts, then a turn starts;
	// all of it but the line's first part leaves the ring mid-replay.
	h.holders.outRaw(holder, `{"type":"assistant","uuid":"u9","mess`, true)
	partial := uint64(h.holders.records(holder))
	h.holders.outRaw(holder, `age":{"id":"msg-u9","content":[]}}`+"\n", false)
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), tasksLine(7))
	h.holders.gapOnReplay(holder, partial+1, partial+4)
	h.start()
	eventually(t, "the line after the gap and the turn taken as running", func() bool {
		st := h.state(id)
		return st.Process && st.BackgroundTasks == 7 && st.Status == StatusRunning
	})
	h.send(id, "two")
	h.sync(id, holder)
	if got := h.userInputs(holder); !slices.Equal(got, h.envelopes("one")) || h.state(id).Held != 1 {
		t.Fatalf("the message waits for the turn's end: written %q, state %+v", got, h.state(id))
	}
	if n := strings.Count(h.log.String(), "with no turn boundary after them"); n != 1 {
		t.Fatalf("the loss should be logged once, logged %d times:\n%s", n, h.log.String())
	}
	h.holders.out(holder, replyLine("u2", "second"), resultLine(), stateLine(HarnessIdle))
	eventually(t, "the held message as the next turn", func() bool { return slices.Equal(h.userInputs(holder), h.envelopes("one", "two")) })
}

// piHeldRejoin runs a pi lead through one turn, restarts the daemon while
// the idle lead's subagent widget resends push every turn boundary out of
// the ring, and rejoins: the rejoin takes the turn as running, a message
// sent then is held, and the rejoin asks pi whether a turn runs. It
// returns the get_state ask's request id.
