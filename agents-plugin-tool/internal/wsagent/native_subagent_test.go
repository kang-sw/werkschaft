package wsagent

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

const (
	agyFixtureNativeRow = wslive.NativeAgyPrefix + "call_17051178851859014051"
	agyFixturePrompt    = "reply with the single word KIWI"
)

// mapAgyFixture maps every agy-output line of a recorded fixture through one
// mapper (a hosted lead's when lead is set), rewriting response ids as
// feedAgyFixture does.
func mapAgyFixture(t *testing.T, name string, lead bool) []Event {
	t.Helper()
	r := &agyRun{m: newAgyMapper(""), answers: map[string]controlAnswer{}}
	r.m.lead = lead
	clock := int64(100_000)
	r.m.now = func() int64 { clock += 10; return clock }
	ids := map[string]string{}
	n := 0
	for _, line := range readLeadFixture(t, name) {
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(line.raw, &msg); err != nil {
			t.Fatal(err)
		}
		_, hasMethod := msg["method"]
		id, hasID := msg["id"]
		if line.in {
			if !hasMethod || !hasID {
				continue
			}
			var method string
			_ = json.Unmarshal(msg["method"], &method)
			kind := agyFixtureKinds[method]
			if kind == "" {
				kind = "other"
			}
			n++
			wsID := agyRequestMark + "-" + kind + "-native-" + strconv.Itoa(n)
			ids[string(id)] = wsID
			if kind == agyKindPrompt {
				r.m.promptSent(wsID)
			}
			continue
		}
		raw := line.raw
		if !hasMethod && hasID {
			if wsID, ok := ids[string(id)]; ok {
				msg["id"], _ = json.Marshal(wsID)
				raw, _ = json.Marshal(msg)
			}
		}
		r.events = append(r.events, r.m.mapLine(raw, r)...)
	}
	r.m.stopWakeTimer()
	return r.events
}

func nativeEvents(events []Event) []wslive.Event {
	var out []wslive.Event
	for _, ev := range events {
		if ev.Live != nil && ev.Live.Kind == wslive.KindAgent && wslive.NativeAgentID(ev.Live.AgentID) {
			out = append(out, *ev.Live)
		}
	}
	return out
}

func applyLive(t *testing.T, st *wslive.State, events []wslive.Event, now int64) {
	t.Helper()
	for _, ev := range events {
		if _, err := st.Apply(ev, now); err != nil {
			t.Fatalf("Apply(%+v): %v", ev, err)
		}
	}
}

func snapAgent(snap wslive.Snapshot, id string) (wslive.Agent, bool) {
	for _, a := range snap.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return wslive.Agent{}, false
}

func liveAgent(t *testing.T, c *conv, id string) (wslive.Agent, bool) {
	t.Helper()
	var a wslive.Agent
	var ok bool
	c.do(func() { a, ok = snapAgent(c.live.st.Snapshot(time.Now().UnixMilli()), id) })
	return a, ok
}

func TestAgyNativeSubagentMapping(t *testing.T) {
	t.Run("hosted lead fixture opens one running row without the prompt", func(t *testing.T) {
		events := nativeEvents(mapAgyFixture(t, "agy-1.5.9-subagent-baseline.jsonl", true))
		if len(events) != 1 || events[0].AgentID != agyFixtureNativeRow || events[0].Label != "Kiwi Responder" || events[0].Status != wslive.StatusRunning || events[0].At <= 0 {
			t.Fatalf("native events = %+v, want one running %s labelled Kiwi Responder", events, agyFixtureNativeRow)
		}
		raw, _ := json.Marshal(events)
		if strings.Contains(string(raw), agyFixturePrompt) {
			t.Fatalf("native row event carries the prompt: %s", raw)
		}
		st := wslive.NewState("lead-key")
		applyLive(t, st, events, 1_000)
		snap := st.Snapshot(2_000)
		raw, _ = json.Marshal(snap)
		if len(snap.Agents) != 1 || snap.Agents[0].Status != wslive.StatusRunning || strings.Contains(string(raw), agyFixturePrompt) {
			t.Fatalf("snapshot = %s", raw)
		}
	})
	t.Run("worker fixture opens no row", func(t *testing.T) {
		if events := nativeEvents(mapAgyFixture(t, "agy-1.5.9-subagent-baseline.jsonl", false)); len(events) != 0 {
			t.Fatalf("worker mapped native rows: %+v", events)
		}
	})
	t.Run("labels and failed launch", func(t *testing.T) {
		m := newAgyMapper("s")
		m.lead = true
		m.now = func() int64 { return 5_000 }
		t.Cleanup(m.stopWakeTimer)
		call := func(id, status, args string) []Event {
			line := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"` + id + `","name":"invoke_subagent",` + args + `"status":"` + status + `"}}}`
			return m.mapLine([]byte(line), nopReplies{})
		}
		typed := nativeEvents(call("c1", "in_progress", `"args":{"TypeName":"research","Prompt":"secret"},`))
		bare := nativeEvents(call("c2", "in_progress", ""))
		if len(typed) != 1 || typed[0].Label != "research" || len(bare) != 1 || bare[0].Label != "subagent" {
			t.Fatalf("labels = %+v / %+v, want research / subagent", typed, bare)
		}
		failed := nativeEvents(m.mapLine([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"c1","name":"invoke_subagent","status":"failed"}}}`), nopReplies{}))
		if len(failed) != 1 || failed[0].AgentID != wslive.NativeAgyPrefix+"c1" || failed[0].Status != wslive.StatusFailed || failed[0].At != 5_000 {
			t.Fatalf("failed launch = %+v", failed)
		}
		completed := nativeEvents(m.mapLine([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"c2","name":"invoke_subagent","status":"completed"}}}`), nopReplies{}))
		if len(completed) != 0 {
			t.Fatalf("a completed launch ended the row: %+v", completed)
		}
		st := wslive.NewState("lead-key")
		applyLive(t, st, append(append(typed, bare...), failed...), 1_000)
		snap := st.Snapshot(6_000)
		c1, _ := snapAgent(snap, wslive.NativeAgyPrefix+"c1")
		c2, _ := snapAgent(snap, wslive.NativeAgyPrefix+"c2")
		if c1.Status != wslive.StatusFailed || c1.EndedAt != 5_000 || c2.Status != wslive.StatusRunning {
			t.Fatalf("rows = %+v / %+v, want failed ended 5000 / running", c1, c2)
		}
		if raw, _ := json.Marshal(snap); strings.Contains(string(raw), "secret") {
			t.Fatalf("prompt reached the snapshot: %s", raw)
		}
	})
}

// codexNativeLines are a parent thread's subAgentActivity envelopes around a
// parent turn: the child starts in turn t1, the turn settles, and the
// completion arrives afterwards still naming t1.
func codexNativeLine(method, id, kind, child, turn string, at int64) string {
	stamp := `"startedAtMs":`
	if method == "item/completed" {
		stamp = `"completedAtMs":`
	}
	return `{"method":"` + method + `","params":{"item":{"type":"subAgentActivity","id":"` + id + `","kind":"` + kind + `","agentThreadId":"` + child + `","agentPath":"/root/probe"},"threadId":"parent","turnId":"` + turn + `",` + stamp + strconv.FormatInt(at, 10) + `}}`
}

func TestCodexNativeSubagentMapping(t *testing.T) {
	t.Run("fixture", func(t *testing.T) {
		r := feedCodexFixture(t, "codex-0.160.0-subagent-baseline.jsonl")
		events := nativeEvents(r.events)
		const row = wslive.NativeCodexPrefix + "call_991318ad7bb04b65ace1b4b9e9f15336"
		if len(events) != 2 {
			t.Fatalf("native events = %+v, want one start and one completion", events)
		}
		if s := events[0]; s.AgentID != row || s.Label != "ok_reply" || s.Status != wslive.StatusRunning || s.At != 1794922658439 {
			t.Fatalf("start = %+v", s)
		}
		if d := events[1]; d.AgentID != row || d.Status != wslive.StatusDone || d.At != 1794922662299 {
			t.Fatalf("completion = %+v", d)
		}
		st := wslive.NewState("lead-key")
		applyLive(t, st, events, 1794922670000)
		a, _ := snapAgent(st.Snapshot(1794922670000), row)
		if a.Status != wslive.StatusDone || a.StartedAt != 1794922658439 || a.EndedAt != 1794922662299 {
			t.Fatalf("row = %+v", a)
		}
	})
	t.Run("late completion and duplicates", func(t *testing.T) {
		r := newCodexRun("parent")
		r.feed(`{"method":"turn/started","params":{"threadId":"parent","turn":{"id":"t1"}}}`)
		start := nativeEvents(append(r.feed(codexNativeLine("item/started", "spawn-1", "started", "child", "t1", 1_000)),
			r.feed(codexNativeLine("item/completed", "spawn-1", "started", "child", "t1", 1_010))...))
		r.feed(`{"method":"turn/completed","params":{"threadId":"parent","turn":{"id":"t1","status":"completed","items":[]}}}`)
		done := nativeEvents(append(r.feed(codexNativeLine("item/started", "end-1", "completed", "child", "t1", 9_000)),
			r.feed(codexNativeLine("item/completed", "end-1", "completed", "child", "t1", 9_001))...))
		again := nativeEvents(r.feed(codexNativeLine("item/started", "end-2", "completed", "child", "t1", 9_500)))
		stray := nativeEvents(r.feed(codexNativeLine("item/started", "end-3", "completed", "nobody", "", 9_600)))
		foreign := nativeEvents(r.feed(strings.Replace(codexNativeLine("item/started", "spawn-9", "started", "x", "t1", 1), `"threadId":"parent"`, `"threadId":"other"`, 1)))
		if len(start) != 1 || start[0].At != 1_000 || start[0].Label != "probe" {
			t.Fatalf("start pair = %+v, want one transition at 1000", start)
		}
		if len(done) != 1 || done[0].AgentID != wslive.NativeCodexPrefix+"spawn-1" || done[0].Status != wslive.StatusDone || done[0].At != 9_000 {
			t.Fatalf("late completion pair = %+v, want one done at 9000", done)
		}
		if len(again) != 0 || len(stray) != 0 || len(foreign) != 0 {
			t.Fatalf("completion without an open row or foreign thread mapped: %+v %+v %+v", again, stray, foreign)
		}
	})
}

func nativeRowEvent(id, label, status string) Event {
	return Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: id, Label: label, Status: status}}
}

// TestAgyNativeFixtureThroughHostedLead drives the recorded invoke_subagent
// turn through a hosted conversation: the row runs after the tool call and
// ends done with no end time and no added work once the runner accepts the
// turn's end.
func TestAgyNativeFixtureThroughHostedLead(t *testing.T) {
	previous := suiteBackend
	suiteBackend = BackendAgy
	t.Cleanup(func() { suiteBackend = previous })
	h := newHarness(t, map[string]string{"chat": chatProfile})
	id := h.create("chat").ID
	if _, err := h.runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	p := h.backend.waitProc(t, 0)
	p.nextSent(t)
	c := h.runner.conv(id)

	events := mapAgyFixture(t, "agy-1.5.9-subagent-baseline.jsonl", true)
	at := slices.IndexFunc(events, func(ev Event) bool {
		return ev.Live != nil && ev.Live.AgentID == agyFixtureNativeRow
	})
	end := at + slices.IndexFunc(events[at:], func(ev Event) bool { return ev.TurnEnd })
	if at < 0 || end < at {
		t.Fatal("fixture turn with the native row not found")
	}
	// Only the prompt that launched the native subagent goes into this turn.
	start := 0
	for i := at - 1; i >= 0; i-- {
		if events[i].TurnEnd {
			start = i + 1
			break
		}
	}
	for _, ev := range events[start : at+1] {
		p.events <- ev
	}
	eventually(t, "running native row", func() bool {
		a, ok := liveAgent(t, c, agyFixtureNativeRow)
		return ok && a.Status == wslive.StatusRunning
	})
	running, _ := liveAgent(t, c, agyFixtureNativeRow)
	for _, ev := range events[at+1 : end+1] {
		p.events <- ev
	}
	eventually(t, "settled turn", func() bool { return h.state(id).Status == StatusIdle })
	a, ok := liveAgent(t, c, agyFixtureNativeRow)
	if !ok || a.Label != "Kiwi Responder" || a.Status != wslive.StatusDone || a.EndedAt != 0 || a.ActiveMs != running.ActiveMs {
		t.Fatalf("row after end_turn = %+v (running was %+v), want done, no end, unchanged ActiveMs", a, running)
	}
	var agents int
	c.do(func() {
		snap := c.live.st.Snapshot(time.Now().UnixMilli())
		raw, _ := json.Marshal(snap)
		if strings.Contains(string(raw), agyFixturePrompt) {
			t.Errorf("snapshot carries the native prompt")
		}
		for _, a := range snap.Agents {
			if wslive.NativeAgentID(a.ID) {
				agents++
			}
		}
	})
	if agents != 1 {
		t.Fatalf("native rows = %d, want exactly one", agents)
	}
	// The turn end's forced write carries the settled row to disk.
	disk, err := wslive.ReadSnapshot(wslive.SnapshotPath(c.dir))
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := snapAgent(disk, agyFixtureNativeRow); !ok || d.Status != wslive.StatusDone || d.EndedAt != 0 {
		t.Fatalf("disk row = %+v (present %v)", d, ok)
	}
}

func TestAgyNativeRowsSettleOnCancelledTurn(t *testing.T) {
	previous := suiteBackend
	suiteBackend = BackendAgy
	t.Cleanup(func() { suiteBackend = previous })
	h := newHarness(t, map[string]string{"chat": chatProfile})
	id := h.create("chat").ID
	if _, err := h.runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	p := h.backend.waitProc(t, 0)
	p.nextSent(t)
	c := h.runner.conv(id)
	row := wslive.NativeAgyPrefix + "cancel"
	codexRow := wslive.NativeCodexPrefix + "keep"
	p.events <- nativeRowEvent(row, "research", wslive.StatusRunning)
	p.events <- nativeRowEvent(codexRow, "probe", wslive.StatusRunning)
	p.events <- Event{TurnEnd: true, TurnReason: wslive.TurnAborted}
	eventually(t, "cancelled turn settled", func() bool { return h.state(id).Status == StatusIdle })
	if a, _ := liveAgent(t, c, row); a.Status != wslive.StatusDone || a.EndedAt != 0 || a.ActiveMs != 0 {
		t.Fatalf("agy row after cancellation = %+v, want end-unknown", a)
	}
	// Only agy rows end with the parent turn; a Codex row waits for its
	// own completion and keeps the session busy until then.
	if a, _ := liveAgent(t, c, codexRow); a.Status != wslive.StatusRunning {
		t.Fatalf("codex row after the parent turn = %+v, want running", a)
	}
}

// TestAgyNativeRowsWaitForWatchdogContinue: a prompt end the runner
// intercepts for an automatic continue keeps the logical turn, so rows stay
// running until the continued turn's accepted end.
func TestAgyNativeRowsWaitForWatchdogContinue(t *testing.T) {
	h, timers, _ := agyWatchdogHarnessWithRetries(t, "45", "1")
	id, p := startAgyWatchdogTurn(t, h, false)
	c := h.runner.conv(id)
	row := wslive.NativeAgyPrefix + "watch"
	p.events <- nativeRowEvent(row, "research", wslive.StatusRunning)
	fireWatchdog(t, timers, 0)
	eventually(t, "retry interrupt", func() bool { return procInterrupts(p) == 1 })
	p.events <- Event{TurnEnd: true, TurnReason: wslive.TurnAborted}
	if sent := p.nextSent(t); sent != "continue" {
		t.Fatalf("retry prompt = %q, want continue", sent)
	}
	if a, _ := liveAgent(t, c, row); a.Status != wslive.StatusRunning {
		t.Fatalf("row after an intercepted prompt end = %+v, want running", a)
	}
	p.reply("done")
	eventually(t, "continued turn settled", func() bool { return h.state(id).Status == StatusIdle })
	if a, _ := liveAgent(t, c, row); a.Status != wslive.StatusDone || a.EndedAt != 0 {
		t.Fatalf("row after the logical turn end = %+v, want end-unknown", a)
	}
}

// TestAgyNativeRowsSettleOnceReplayCompletes: replayed turn ends, even a
// cancelled prompt whose continuation is still open, never settle agy rows;
// replay completion settles the open ones, and the continuation's later live
// end leaves them as they are.
func TestAgyNativeRowsSettleOnceReplayCompletes(t *testing.T) {
	previous := suiteBackend
	suiteBackend = BackendAgy
	t.Cleanup(func() { suiteBackend = previous })
	h := newHarness(t, map[string]string{"chat": chatProfile})
	id := h.create("chat").ID
	if _, err := h.runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	p := h.backend.waitProc(t, 0)
	p.nextSent(t)
	c := h.runner.conv(id)
	row := wslive.NativeAgyPrefix + "replay"
	var settledAt int64
	c.do(func() {
		c.replaying = true
		quiet := func(ev Event) { ev.Replay = true; c.handle(ev) }
		quiet(nativeRowEvent(row, "research", wslive.StatusRunning))
		quiet(Event{TurnEnd: true, TurnReason: wslive.TurnAborted})
		quiet(Event{TurnStart: true})
		quiet(Event{TurnEnd: true})
		quiet(Event{TurnEnd: true, TurnReason: wslive.TurnAborted})
		if a, _ := snapAgent(c.live.st.Snapshot(time.Now().UnixMilli()), row); a.Status != wslive.StatusRunning {
			t.Errorf("a replayed turn end settled the row: %+v", a)
		}
		c.handle(Event{ReplayDone: true})
		a, _ := snapAgent(c.live.st.Snapshot(time.Now().UnixMilli()), row)
		if a.Status != wslive.StatusDone || a.EndedAt != 0 {
			t.Errorf("row after replay completion = %+v, want end-unknown", a)
		}
		settledAt = a.PhaseStartedAt
	})
	p.events <- Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: "continued", Final: true}}
	p.events <- nativeRowEvent(row, "", wslive.StatusRunning)
	p.events <- Event{TurnEnd: true}
	eventually(t, "continuation settled", func() bool { return h.state(id).Status == StatusIdle })
	if a, _ := liveAgent(t, c, row); a.Status != wslive.StatusDone || a.EndedAt != 0 || a.PhaseStartedAt != settledAt {
		t.Fatalf("row after the live continuation end = %+v, want unchanged end-unknown", a)
	}
}

// TestNativeRowEventsPublishRAMOnly: a native row event after the disk
// interval writes no file and carries no wake, turn, or batch authority;
// the row reaches disk with the next turn-end write.
func TestNativeRowEventsPublishRAMOnly(t *testing.T) {
	now := time.Unix(1000, 0)
	dir := filepath.Join(t.TempDir(), "native")
	r := NewRunner(Options{Dir: dir, Now: func() time.Time { return now }, Log: io.Discard})
	c := &conv{r: r, dir: dir, rec: Record{ReplyID: "native", WSKey: "key", Root: dir}, turn: true}
	c.live = newLiveWriter(dir, "key", "agy", r.opts.Now, io.Discard)
	slot := &idleSnapshotSlot{}
	c.live.slot = slot
	c.live.wake = slot.RingAppended
	writes := 0
	c.live.writeSnapshotFile = func(path string, raw []byte, mode os.FileMode) error {
		writes++
		return writeFileAtomic(path, raw, mode)
	}
	c.live.writeSnapshot()
	if writes != 1 {
		t.Fatalf("initial writes = %d", writes)
	}
	now = now.Add(2 * wslive.HeartbeatInterval)
	row := wslive.NativeCodexPrefix + "ram"
	turn, batches := c.turn, c.batches
	c.handle(Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: row, Label: "probe", Status: wslive.StatusRunning}})
	c.handle(Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: wslive.NativeAgyPrefix + "ram", Label: "research", Status: wslive.StatusRunning}})
	if writes != 1 {
		t.Fatalf("native row events wrote %d snapshots, want none", writes-1)
	}
	if a, ok := snapAgent(slot.snapshot, row); !ok || a.Status != wslive.StatusRunning {
		t.Fatalf("native row not published to RAM: %+v", slot.snapshot.Agents)
	}
	if c.turn != turn || !reflect.DeepEqual(c.batches, batches) || slot.rings != 0 || c.live.st.Snapshot(now.UnixMilli()).Lead.TurnStartedAt != 0 {
		t.Fatalf("native rows touched turn state, batches, or the ring: turn %v, batches %v, rings %d", c.turn, c.batches, slot.rings)
	}
	if disk, err := wslive.ReadSnapshot(wslive.SnapshotPath(dir)); err != nil || len(disk.Agents) != 0 {
		t.Fatalf("disk before the turn end = %+v, %v", disk.Agents, err)
	}
	c.apply(wslive.Event{Kind: wslive.KindTurnEnd})
	if writes != 2 {
		t.Fatalf("turn end writes = %d, want 2", writes)
	}
	disk, err := wslive.ReadSnapshot(wslive.SnapshotPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapAgent(disk, row); !ok {
		t.Fatal("native row missing from the turn-end write")
	}
	// An ordinary row past the interval still writes, as before.
	now = now.Add(2 * wslive.HeartbeatInterval)
	c.handle(Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: "plain", Status: wslive.StatusRunning}})
	if writes != 3 {
		t.Fatalf("ordinary row writes = %d, want 3", writes)
	}
}
