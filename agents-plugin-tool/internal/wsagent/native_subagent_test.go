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

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmailbox"
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
	t.Run("second start for an open thread keeps the first row", func(t *testing.T) {
		r := newCodexRun("parent")
		r.feed(`{"method":"turn/started","params":{"threadId":"parent","turn":{"id":"t1"}}}`)
		first := nativeEvents(r.feed(codexNativeLine("item/started", "spawn-1", "started", "child", "t1", 1_000)))
		second := nativeEvents(r.feed(codexNativeLine("item/started", "spawn-2", "started", "child", "t1", 2_000)))
		done := nativeEvents(r.feed(codexNativeLine("item/started", "end-1", "completed", "child", "t1", 3_000)))
		if len(first) != 1 || len(second) != 0 {
			t.Fatalf("starts = %+v then %+v, want only the first row opened", first, second)
		}
		if len(done) != 1 || done[0].AgentID != wslive.NativeCodexPrefix+"spawn-1" || done[0].Status != wslive.StatusDone {
			t.Fatalf("completion = %+v, want the first row done", done)
		}
	})
}

func nativeRowEvent(id, label, status string) Event {
	return Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: id, Label: label, Status: status}}
}

// TestAgyNativeFixtureThroughHostedLead drives the recorded invoke_subagent
// turn through a hosted conversation: the row runs after the tool call and
// ends done as observation ended (flagged, ended at the accepted turn end)
// with no added work once the runner accepts the turn's end.
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
	if !ok || a.Label != "Kiwi Responder" || a.Status != wslive.StatusDone || !a.EndUnknown || a.EndedAt < running.StartedAt || a.ActiveMs != running.ActiveMs {
		t.Fatalf("row after end_turn = %+v (running was %+v), want done end-unknown at the turn end, unchanged ActiveMs", a, running)
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
	if d, ok := snapAgent(disk, agyFixtureNativeRow); !ok || d.Status != wslive.StatusDone || !d.EndUnknown || d.EndedAt != a.EndedAt {
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
	if a, _ := liveAgent(t, c, row); a.Status != wslive.StatusDone || !a.EndUnknown || a.EndedAt <= 0 || a.ActiveMs != 0 {
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
	if a, _ := liveAgent(t, c, row); a.Status != wslive.StatusDone || !a.EndUnknown {
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
		if a, ok := snapAgent(c.live.st.Snapshot(time.Now().UnixMilli()), row); ok && a.Status != wslive.StatusRunning {
			t.Errorf("a replayed turn end settled the row: %+v", a)
		}
		c.handle(Event{ReplayDone: true})
		a, _ := snapAgent(c.live.st.Snapshot(time.Now().UnixMilli()), row)
		if a.Status != wslive.StatusDone || !a.EndUnknown || a.EndedAt <= 0 {
			t.Errorf("row after replay completion = %+v, want end-unknown", a)
		}
		settledAt = a.EndedAt
	})
	p.events <- Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: "continued", Final: true}}
	p.events <- nativeRowEvent(row, "", wslive.StatusRunning)
	p.events <- Event{TurnEnd: true}
	eventually(t, "continuation settled", func() bool { return h.state(id).Status == StatusIdle })
	if a, _ := liveAgent(t, c, row); a.Status != wslive.StatusDone || !a.EndUnknown || a.EndedAt != settledAt {
		t.Fatalf("row after the live continuation end = %+v, want unchanged end-unknown", a)
	}
}

// TestAgyNativeRowsWaitForReplayInCatchUp: in a catch-up replay only the
// records through the holder's sequence mark are quiet; a buffered unseen
// prompt response arrives live while the replay still runs. Its turn end
// settles no row before the replay ends; the rows it ended observation of
// settle at the replay boundary at that turn end's time, unless a launch
// failure buffered later in the same replay ended the row first.
func TestAgyNativeRowsWaitForReplayInCatchUp(t *testing.T) {
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
	failing, open := wslive.NativeAgyPrefix+"fails-later", wslive.NativeAgyPrefix+"open"
	c.do(func() {
		c.replaying = true
		quiet := func(ev Event) { ev.Replay = true; c.handle(ev) }
		quiet(nativeRowEvent(failing, "research", wslive.StatusRunning))
		quiet(nativeRowEvent(open, "probe", wslive.StatusRunning))
		// Past the sequence mark: live records while the replay runs.
		before := c.r.opts.Now().UnixMilli()
		c.handle(Event{TurnEnd: true})
		after := c.r.opts.Now().UnixMilli()
		snap := c.live.st.Snapshot(after)
		for _, row := range []string{failing, open} {
			if a, ok := snapAgent(snap, row); ok && a.Status != wslive.StatusRunning {
				t.Errorf("a live turn end during the replay settled %s: %+v", row, a)
			}
		}
		c.handle(Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: failing, Status: wslive.StatusFailed, At: after + 5}})
		c.handle(Event{ReplayDone: true})
		snap = c.live.st.Snapshot(c.r.opts.Now().UnixMilli())
		if a, _ := snapAgent(snap, failing); a.Status != wslive.StatusFailed || a.EndUnknown || a.EndedAt != after+5 {
			t.Errorf("row whose launch failed later in the replay = %+v, want failed at %d", a, after+5)
		}
		if a, _ := snapAgent(snap, open); a.Status != wslive.StatusDone || !a.EndUnknown || a.EndedAt < before || a.EndedAt > after {
			t.Errorf("row the live turn end accepted = %+v, want end-unknown at that end (%d..%d)", a, before, after)
		}
	})
}

// TestNativeRowEventsPublishRAMOnly: a hosted lead's native row event after
// the disk interval touches no file: no snapshot write, no registration lock
// or read (the real owner fence runs on the proof the last file write made),
// and it carries no wake, turn, or batch authority; the row reaches disk with
// the next turn-end write, which re-validates the row.
func TestNativeRowEventsPublishRAMOnly(t *testing.T) {
	now := time.Unix(1000, 0)
	base := t.TempDir()
	dir := filepath.Join(t.TempDir(), "native")
	r := NewRunner(Options{Dir: base, Now: func() time.Time { return now }, Log: io.Discard, Alive: func(int) bool { return true }})
	c := &conv{r: r, dir: dir, rec: Record{ReplyID: "native", WSKey: "key", Root: dir, Backend: BackendAgy, Lead: true, HostPID: 4242}, turn: true}
	if err := wsdashboard.WriteRegistration(base, wsdashboard.Registration{ReplyID: "native", Project: dir, Harness: BackendAgy, Hosted: true, HostPID: 4242}); err != nil {
		t.Fatal(err)
	}
	r.setSummaryOwner("native", c)
	c.live = newLiveWriter(dir, "key", "agy", r.opts.Now, io.Discard)
	c.live.owner, c.live.ownerRAM = c.lockSummaryWriter, c.lockSummaryWriterRAM
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
	rowLocks, rowFileReads := c.rowReads.Load(), registrationReads()
	row := wslive.NativeCodexPrefix + "ram"
	turn, batches := c.turn, c.batches
	c.handle(Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: row, Label: "probe", Status: wslive.StatusRunning}})
	c.handle(Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: wslive.NativeAgyPrefix + "ram", Label: "research", Status: wslive.StatusRunning}})
	if writes != 1 {
		t.Fatalf("native row events wrote %d snapshots, want none", writes-1)
	}
	if locks, reads := c.rowReads.Load()-rowLocks, registrationReads()-rowFileReads; locks != 0 || reads != 0 {
		t.Fatalf("native row events took %d registration locks and %d registration reads, want none", locks, reads)
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
	if c.rowReads.Load() == rowLocks {
		t.Fatal("the turn-end write did not re-validate the row")
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
	// A row the terminal took keeps the fence shut without a file read.
	c.markForeign()
	before := slot.snapshot
	c.handle(Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: wslive.NativeCodexPrefix + "late", Label: "late", Status: wslive.StatusRunning}})
	if _, ok := snapAgent(slot.snapshot, wslive.NativeCodexPrefix+"late"); ok || !reflect.DeepEqual(before, slot.snapshot) {
		t.Fatal("a native row published after the session's row left the daemon")
	}
}

// TestNativeRowRAMProofEndsWithItsProcess: the hosted harness exits with a
// native event still queued and a terminal login takes the row before the
// daemon consumes the exit. The old conversation still holds its slot, its
// summary ownership and the cached proof, yet the queued native row is not
// published to the transferred session, and the refusal reads no file.
func TestNativeRowRAMProofEndsWithItsProcess(t *testing.T) {
	now := time.Unix(1000, 0)
	base := t.TempDir()
	dir := filepath.Join(t.TempDir(), "native")
	alive := true
	r := NewRunner(Options{Dir: base, Now: func() time.Time { return now }, Log: io.Discard, Alive: func(pid int) bool { return alive && pid == 4242 }})
	c := &conv{r: r, dir: dir, rec: Record{ReplyID: "native", WSKey: "key", Root: dir, Backend: BackendAgy, Lead: true, HostPID: 4242}, turn: true}
	if err := wsdashboard.WriteRegistration(base, wsdashboard.Registration{ReplyID: "native", Project: dir, Harness: BackendAgy, Hosted: true, HostPID: 4242}); err != nil {
		t.Fatal(err)
	}
	r.setSummaryOwner("native", c)
	c.live = newLiveWriter(dir, "key", "agy", r.opts.Now, io.Discard)
	c.live.owner, c.live.ownerRAM = c.lockSummaryWriter, c.lockSummaryWriterRAM
	slot := &idleSnapshotSlot{}
	c.live.slot = slot
	c.live.wake = slot.RingAppended
	c.live.writeSnapshot()
	if c.ownerProof.Load() == nil {
		t.Fatal("the file-fenced write proved no row")
	}
	now = now.Add(2 * wslive.HeartbeatInterval)
	early := wslive.NativeAgyPrefix + "early"
	c.handle(Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: early, Label: "early", Status: wslive.StatusRunning}})
	if _, ok := snapAgent(slot.snapshot, early); !ok {
		t.Fatal("a native row under a running host was not published")
	}
	// The harness exits; a terminal login rewrites the row.
	alive = false
	if err := wsdashboard.WriteRegistration(base, wsdashboard.Registration{ReplyID: "native", Project: dir, Harness: BackendAgy, HarnessPID: 777}); err != nil {
		t.Fatal(err)
	}
	if c.ownerProof.Load() == nil || r.summaryOwner("native") != c || c.live.currentSlot() == nil {
		t.Fatal("the old proof, summary ownership or slot is already gone")
	}
	before, locks, reads := slot.snapshot, c.rowReads.Load(), registrationReads()
	queued := wslive.NativeAgyPrefix + "queued"
	c.handle(Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: queued, Label: "queued", Status: wslive.StatusRunning}})
	if _, ok := snapAgent(slot.snapshot, queued); ok || !reflect.DeepEqual(before, slot.snapshot) {
		t.Fatal("the queued native row was published to the transferred session")
	}
	if l, rd := c.rowReads.Load()-locks, registrationReads()-reads; l != 0 || rd != 0 {
		t.Fatalf("the refusal took %d registration locks and %d reads, want none", l, rd)
	}
	if c.ownerProof.Load() != nil {
		t.Fatal("the proof outlived its process")
	}
	// The next file-fenced write sees the terminal's row and publishes nothing.
	c.apply(wslive.Event{Kind: wslive.KindTurnEnd})
	if _, ok := snapAgent(slot.snapshot, queued); ok {
		t.Fatal("a file-fenced write published to the transferred session")
	}
}

// forgetProc records the launch correlations a conversation drops.
type forgetProc struct {
	Process
	forgot []string
}

func (p *forgetProc) forgetNativeLaunches(ids []string) { p.forgot = append(p.forgot, ids...) }

// TestAgyNativeLaunchCorrelationEndsWithTheRow: a launch that never reports
// a final status leaves no correlation once its row settles (a cancelled
// turn's accepted end, a replay's end); before that, a failure still ends
// its row.
func TestAgyNativeLaunchCorrelationEndsWithTheRow(t *testing.T) {
	m := newAgyMapper("s")
	m.lead = true
	m.now = func() int64 { return 5_000 }
	t.Cleanup(m.stopWakeTimer)
	launch := func(id string) {
		m.mapLine([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"`+id+`","name":"invoke_subagent","status":"in_progress"}}}`), nopReplies{})
	}
	failure := func(id string) []wslive.Event {
		return nativeEvents(m.mapLine([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"`+id+`","status":"failed"}}}`), nopReplies{}))
	}
	launch("kept")
	launch("settled")
	p := &agyProcess{m: m}
	p.forgetNativeLaunches([]string{wslive.NativeAgyPrefix + "settled"})
	if len(m.nativeLaunch) != 1 || !m.nativeLaunch["kept"] {
		t.Fatalf("correlations after a settlement = %v, want only the open launch", m.nativeLaunch)
	}
	if got := failure("kept"); len(got) != 1 || got[0].Status != wslive.StatusFailed {
		t.Fatalf("failure of an unsettled launch = %+v, want its row failed", got)
	}
	if len(m.nativeLaunch) != 0 {
		t.Fatalf("correlations after the failure = %v, want none", m.nativeLaunch)
	}

	// The conversation hands the rows it settles to the process.
	now := time.Unix(1000, 0)
	dir := filepath.Join(t.TempDir(), "forget")
	r := NewRunner(Options{Dir: dir, Now: func() time.Time { return now }, Log: io.Discard})
	proc := &forgetProc{}
	c := &conv{r: r, dir: dir, rec: Record{ReplyID: "forget", WSKey: "key", Root: dir}, proc: proc}
	c.live = newLiveWriter(dir, "key", "agy", r.opts.Now, io.Discard)
	row := wslive.NativeAgyPrefix + "cancelled"
	c.apply(wslive.Event{Kind: wslive.KindAgent, AgentID: row, Status: wslive.StatusRunning})
	c.apply(wslive.Event{Kind: wslive.KindAgent, AgentID: wslive.NativeCodexPrefix + "other", Status: wslive.StatusRunning})
	c.endAgyNativeObservation(now.UnixMilli())
	if !reflect.DeepEqual(proc.forgot, []string{row}) {
		t.Fatalf("forgotten correlations = %v, want %v", proc.forgot, []string{row})
	}
	replayed := wslive.NativeAgyPrefix + "replayed"
	c.replaying = true
	c.apply(wslive.Event{Kind: wslive.KindAgent, AgentID: replayed, Status: wslive.StatusRunning}, true)
	c.finishAgyNativeReplay()
	if !reflect.DeepEqual(proc.forgot, []string{row, replayed}) {
		t.Fatalf("forgotten correlations after the replay = %v", proc.forgot)
	}
}

// TestAgyNativeReplayKeepsHistoricalRetention: six agy launches in separate
// completed turns leave the newest five rows in the recovery cache. A rejoin
// whose holder ring still holds all six starts replays the oldest one, whose
// settlement left no record. Retention already evicted that row, so the
// replay does not resurrect it, even when a buffered live turn end saw it
// running; the five newer restored rows keep their stored values.
func TestAgyNativeReplayKeepsHistoricalRetention(t *testing.T) {
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
	launch := func(i int) string { return wslive.NativeAgyPrefix + "launch-" + strconv.Itoa(i) }
	c.do(func() {
		var restored wslive.Snapshot
		restored.ReplyID = c.live.st.ReplyID()
		base := time.Now().Add(-time.Hour).UnixMilli()
		restored.UpdatedAt = base + 7*60_000
		for i := 2; i <= 6; i++ {
			at := base + int64(i)*60_000
			restored.Agents = append(restored.Agents, wslive.Agent{ID: launch(i), Label: "research", Status: wslive.StatusDone,
				StartedAt: at - 30_000, EndedAt: at, EndUnknown: true, Activity: wslive.Activity{Phase: wslive.PhaseIdle, PhaseStartedAt: at}})
		}
		c.live.st.SeedFinished(restored)
		c.live.restored = restoredAgyNativeOf(restored, restored.ReplyID)
		c.replaying = true
		quiet := func(ev Event) { ev.Replay = true; c.handle(ev) }
		for i := 1; i <= 6; i++ {
			quiet(Event{TurnStart: true})
			quiet(nativeRowEvent(launch(i), "research", wslive.StatusRunning))
			quiet(Event{TurnEnd: true})
		}
		// A buffered prompt response past the sequence mark.
		c.handle(Event{TurnEnd: true})
		c.handle(Event{ReplayDone: true})
		snap := c.live.st.Snapshot(time.Now().UnixMilli())
		for _, want := range restored.Agents {
			got, ok := snapAgent(snap, want.ID)
			if !ok {
				t.Errorf("restored row %s was displaced", want.ID)
				continue
			}
			if got.Status != want.Status || got.StartedAt != want.StartedAt || got.EndedAt != want.EndedAt || !got.EndUnknown {
				t.Errorf("restored row %s = %+v, want its stored values %+v", want.ID, got, want)
			}
		}
		if a, ok := snapAgent(snap, launch(1)); ok {
			t.Errorf("the replay resurrected the evicted oldest launch: %+v", a)
		}
		if c.live.st.HasAgent(launch(1)) {
			t.Error("the evicted oldest launch is still in the state")
		}
	})
}

// TestAgyNativeReplayClosesRebuiltRowsWithoutSnapshot: with no readable
// restored snapshot there is no restored row to displace, so the rows a
// replay rebuilt from quiet records close end-unknown at the replay's end.
func TestAgyNativeReplayClosesRebuiltRowsWithoutSnapshot(t *testing.T) {
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
	rows := []string{wslive.NativeAgyPrefix + "first", wslive.NativeAgyPrefix + "second"}
	c.do(func() {
		c.live.restored = restoredAgyNative{}
		c.replaying = true
		quiet := func(ev Event) { ev.Replay = true; c.handle(ev) }
		for _, row := range rows {
			quiet(Event{TurnStart: true})
			quiet(nativeRowEvent(row, "research", wslive.StatusRunning))
			quiet(Event{TurnEnd: true})
		}
		before := c.r.opts.Now().UnixMilli()
		c.handle(Event{ReplayDone: true})
		after := c.r.opts.Now().UnixMilli()
		snap := c.live.st.Snapshot(after)
		for _, row := range rows {
			a, ok := snapAgent(snap, row)
			if !ok || a.Status != wslive.StatusDone || !a.EndUnknown || a.EndedAt < before || a.EndedAt > after {
				t.Errorf("row %s = %+v (present %v), want end-unknown at the replay end (%d..%d)", row, a, ok, before, after)
			}
		}
	})
}

// TestAgyNativeReplayClosesRebuiltRowsByEvidence: against a readable restored
// snapshot, a rebuilt row the snapshot listed running closes end-unknown at
// the snapshot's write time (its start moved back to it), a row a record past
// the holder's sequence mark touched closes at that record's time, and a
// quiet-only row the snapshot does not list is dropped.
func TestAgyNativeReplayClosesRebuiltRowsByEvidence(t *testing.T) {
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
	wasRunning, live, evicted := wslive.NativeAgyPrefix+"was-running", wslive.NativeAgyPrefix+"live", wslive.NativeAgyPrefix+"evicted"
	c.do(func() {
		updatedAt := time.Now().Add(-time.Minute).UnixMilli()
		restored := wslive.Snapshot{ReplyID: c.live.st.ReplyID(), UpdatedAt: updatedAt, Agents: []wslive.Agent{
			{ID: wasRunning, Label: "research", Status: wslive.StatusRunning, StartedAt: updatedAt - 10_000},
		}}
		c.live.st.SeedFinished(restored)
		c.live.restored = restoredAgyNativeOf(restored, restored.ReplyID)
		c.replaying = true
		quiet := func(ev Event) { ev.Replay = true; c.handle(ev) }
		quiet(Event{TurnStart: true})
		quiet(nativeRowEvent(evicted, "old", wslive.StatusRunning))
		quiet(nativeRowEvent(wasRunning, "research", wslive.StatusRunning))
		quiet(nativeRowEvent(live, "probe", wslive.StatusRunning))
		liveAt := c.r.opts.Now().UnixMilli() - 5
		c.handle(Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: live, Label: "probe", Status: wslive.StatusRunning, At: liveAt}})
		c.handle(Event{ReplayDone: true})
		snap := c.live.st.Snapshot(c.r.opts.Now().UnixMilli())
		if a, ok := snapAgent(snap, wasRunning); !ok || a.Status != wslive.StatusDone || !a.EndUnknown || a.EndedAt != updatedAt || a.StartedAt > updatedAt {
			t.Errorf("row the snapshot listed running = %+v (present %v), want end-unknown at %d", a, ok, updatedAt)
		}
		if a, ok := snapAgent(snap, live); !ok || a.Status != wslive.StatusDone || !a.EndUnknown || a.EndedAt != liveAt {
			t.Errorf("row a live record touched = %+v (present %v), want end-unknown at %d", a, ok, liveAt)
		}
		if a, ok := snapAgent(snap, evicted); ok {
			t.Errorf("quiet-only row absent from the snapshot was resurrected: %+v", a)
		}
	})
}

// TestLiveWriterRestoresAgyNativeRows: the live writer keeps what the
// snapshot an earlier daemon left says of agy native rows, once; a missing
// snapshot or one of another session is not readable.
func TestLiveWriterRestoresAgyNativeRows(t *testing.T) {
	now := func() time.Time { return time.Unix(1000, 0) }
	missing := newLiveWriter(t.TempDir(), "key", "agy", now, io.Discard)
	if missing.takeRestored().readable {
		t.Fatal("a missing snapshot reads as readable")
	}
	write := func(snap wslive.Snapshot) string {
		dir := t.TempDir()
		raw, err := json.Marshal(snap)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wslive.SnapshotPath(dir), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	running, done := wslive.NativeAgyPrefix+"running", wslive.NativeAgyPrefix+"done"
	snap := wslive.Snapshot{ReplyID: wsmailbox.ReplyID("key"), UpdatedAt: 42_000, HolderID: "holder-1", HolderSeq: 17, Agents: []wslive.Agent{
		{ID: running, Status: wslive.StatusRunning, StartedAt: 1},
		{ID: done, Status: wslive.StatusDone, StartedAt: 1, EndedAt: 2},
		{ID: wslive.NativeCodexPrefix + "other", Status: wslive.StatusRunning, StartedAt: 1},
	}}
	w := newLiveWriter(write(snap), "key", "agy", now, io.Discard)
	got := w.takeRestored()
	want := restoredAgyNative{readable: true, updatedAt: 42_000, listed: map[string]bool{running: true, done: true}, running: map[string]wslive.Agent{running: snap.Agents[0]},
		holderID: "holder-1", coveredSeq: 17}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored = %+v, want %+v", got, want)
	}
	if again := w.takeRestored(); !again.readable || len(again.listed) != 0 || len(again.running) != 0 {
		t.Fatalf("second take = %+v, want readable with nothing restored", again)
	}
	snap.ReplyID = "another"
	if newLiveWriter(write(snap), "key", "agy", now, io.Discard).takeRestored().readable {
		t.Fatal("another session's snapshot reads as readable")
	}
}

// agyReplayConv starts an agy conversation for a replay test and returns it
// with the holder its record names (set to one when the harness has none).
func agyReplayConv(t *testing.T) (*conv, string) {
	t.Helper()
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
	var holder string
	c.do(func() {
		c.mu.Lock()
		if c.rec.HolderID == "" {
			c.rec.HolderID = "holder-replay"
		}
		holder = c.rec.HolderID
		c.mu.Unlock()
	})
	return c, holder
}

// seqEvent is ev from holder record seq, replayed (quiet) or not.
func seqEvent(ev Event, seq uint64, quiet bool) Event {
	ev.Seq, ev.Replay = seq, quiet
	return ev
}

// TestAgyNativeReplayStagesRebuiltFailureOutsideRetention: five saved
// terminal rows fill the finished cap. The replay rebuilds an older row
// retention evicted, from a quiet start and a quiet launch failure stamped
// with the replay's clock. Staged outside retention, that row is dropped
// before it can rank ahead of a saved row, so all five saved rows remain
// with their stored values.
func TestAgyNativeReplayStagesRebuiltFailureOutsideRetention(t *testing.T) {
	c, holder := agyReplayConv(t)
	launch := func(i int) string { return wslive.NativeAgyPrefix + "launch-" + strconv.Itoa(i) }
	c.do(func() {
		base := time.Now().Add(-time.Hour).UnixMilli()
		restored := wslive.Snapshot{ReplyID: c.live.st.ReplyID(), UpdatedAt: base + 7*60_000, HolderID: holder, HolderSeq: 20}
		for i := 2; i <= 6; i++ {
			at := base + int64(i)*60_000
			restored.Agents = append(restored.Agents, wslive.Agent{ID: launch(i), Label: "research", Status: wslive.StatusDone,
				StartedAt: at - 30_000, EndedAt: at, EndUnknown: true, Activity: wslive.Activity{Phase: wslive.PhaseIdle, PhaseStartedAt: at}})
		}
		c.live.st.SeedFinished(restored)
		c.live.restored = restoredAgyNativeOf(restored, restored.ReplyID)
		c.replaying = true
		c.handle(seqEvent(nativeRowEvent(launch(1), "research", wslive.StatusRunning), 3, true))
		c.handle(seqEvent(nativeRowEvent(launch(1), "", wslive.StatusFailed), 4, true))
		if c.live.st.HasAgent(launch(1)) {
			t.Error("a replayed row reached the state before the replay ended")
		}
		c.handle(Event{ReplayDone: true})
		snap := c.live.st.Snapshot(time.Now().UnixMilli())
		for _, want := range restored.Agents {
			got, ok := snapAgent(snap, want.ID)
			if !ok {
				t.Errorf("saved row %s was evicted by the rebuilt failure", want.ID)
				continue
			}
			if got.Status != want.Status || got.StartedAt != want.StartedAt || got.EndedAt != want.EndedAt || !got.EndUnknown {
				t.Errorf("saved row %s = %+v, want its stored values %+v", want.ID, got, want)
			}
		}
		if a, ok := snapAgent(snap, launch(1)); ok {
			t.Errorf("the evicted row came back: %+v", a)
		}
	})
}

// TestAgyNativeReplayKeepsCatchUpFailureOfRunningRow: a row the restored
// snapshot lists running, whose launch failure arrives past the holder's
// sequence mark during the replay, ends failed at that record's time.
func TestAgyNativeReplayKeepsCatchUpFailureOfRunningRow(t *testing.T) {
	c, holder := agyReplayConv(t)
	row := wslive.NativeAgyPrefix + "listed-running"
	c.do(func() {
		updatedAt := time.Now().Add(-time.Minute).UnixMilli()
		restored := wslive.Snapshot{ReplyID: c.live.st.ReplyID(), UpdatedAt: updatedAt, HolderID: holder, HolderSeq: 20, Agents: []wslive.Agent{
			{ID: row, Label: "research", Status: wslive.StatusRunning, StartedAt: updatedAt - 10_000},
		}}
		c.live.restored = restoredAgyNativeOf(restored, restored.ReplyID)
		c.replaying = true
		c.handle(seqEvent(nativeRowEvent(row, "research", wslive.StatusRunning), 5, true))
		failedAt := c.r.opts.Now().UnixMilli() - 3
		c.handle(seqEvent(Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: row, Status: wslive.StatusFailed, At: failedAt}}, 25, false))
		c.handle(Event{ReplayDone: true})
		a, ok := snapAgent(c.live.st.Snapshot(c.r.opts.Now().UnixMilli()), row)
		if !ok || a.Status != wslive.StatusFailed || a.EndUnknown || a.EndedAt != failedAt {
			t.Fatalf("row = %+v (present %v), want failed at %d", a, ok, failedAt)
		}
	})
}

// TestAgyNativeReplayHonorsSnapshotCoverage: the restored snapshot covers
// holder records through 10. A rebuilt row it lacks whose start is at or
// below 10 was evicted and is dropped; one started after 10 (still at or
// below the saved holder mark, so quiet) survives, closed by the terminal
// record that followed it or else end-unknown at the replay's end. A row the
// snapshot lists running whose start left the ring is seeded from the
// snapshot and closes end-unknown at the snapshot's write time.
func TestAgyNativeReplayHonorsSnapshotCoverage(t *testing.T) {
	c, holder := agyReplayConv(t)
	historical, after, afterFailed, wrapped, saved := wslive.NativeAgyPrefix+"historical", wslive.NativeAgyPrefix+"after", wslive.NativeAgyPrefix+"after-failed", wslive.NativeAgyPrefix+"wrapped", wslive.NativeAgyPrefix+"saved"
	c.do(func() {
		updatedAt := time.Now().Add(-time.Minute).UnixMilli()
		restored := wslive.Snapshot{ReplyID: c.live.st.ReplyID(), UpdatedAt: updatedAt, HolderID: holder, HolderSeq: 10, Agents: []wslive.Agent{
			{ID: saved, Label: "saved", Status: wslive.StatusDone, StartedAt: updatedAt - 50_000, EndedAt: updatedAt - 40_000, EndUnknown: true},
			{ID: wrapped, Label: "wrapped-label", Status: wslive.StatusRunning, StartedAt: updatedAt - 20_000},
		}}
		c.live.st.SeedFinished(restored)
		c.live.restored = restoredAgyNativeOf(restored, restored.ReplyID)
		c.replaying = true
		c.handle(seqEvent(nativeRowEvent(historical, "old", wslive.StatusRunning), 5, true))
		c.handle(seqEvent(nativeRowEvent(after, "new", wslive.StatusRunning), 12, true))
		c.handle(seqEvent(nativeRowEvent(afterFailed, "new-failed", wslive.StatusRunning), 13, true))
		c.handle(seqEvent(nativeRowEvent(afterFailed, "", wslive.StatusFailed), 14, true))
		before := c.r.opts.Now().UnixMilli()
		c.handle(Event{ReplayDone: true})
		end := c.r.opts.Now().UnixMilli()
		snap := c.live.st.Snapshot(end)
		if a, ok := snapAgent(snap, historical); ok {
			t.Errorf("a covered historical start came back: %+v", a)
		}
		if a, ok := snapAgent(snap, after); !ok || a.Status != wslive.StatusDone || !a.EndUnknown || a.EndedAt < before || a.EndedAt > end {
			t.Errorf("row started after the coverage = %+v (present %v), want end-unknown at the replay end (%d..%d)", a, ok, before, end)
		}
		if a, ok := snapAgent(snap, afterFailed); !ok || a.Status != wslive.StatusFailed || a.EndUnknown {
			t.Errorf("row started after the coverage with a failure = %+v (present %v), want failed", a, ok)
		}
		if a, ok := snapAgent(snap, wrapped); !ok || a.Status != wslive.StatusDone || !a.EndUnknown || a.EndedAt != updatedAt || a.StartedAt != updatedAt-20_000 || a.Label != "wrapped-label" {
			t.Errorf("snapshot-running row with a wrapped start = %+v (present %v), want end-unknown at %d from its stored start", a, ok, updatedAt)
		}
		if a, ok := snapAgent(snap, saved); !ok || a.EndedAt != updatedAt-40_000 {
			t.Errorf("saved row = %+v (present %v), want its stored values", a, ok)
		}
	})
}

// TestSnapshotRecordsHolderCoverage: the recovery file names the holder
// records the state applied; while a replay runs it keeps the coverage of
// the snapshot the state was restored from.
func TestSnapshotRecordsHolderCoverage(t *testing.T) {
	now := time.Unix(1000, 0)
	dir := filepath.Join(t.TempDir(), "coverage")
	r := NewRunner(Options{Dir: dir, Now: func() time.Time { return now }, Log: io.Discard})
	c := &conv{r: r, dir: dir, rec: Record{ReplyID: "coverage", WSKey: "key", Root: dir, HolderID: "holder-now"}, seq: 9}
	c.live = newLiveWriter(dir, "key", "agy", r.opts.Now, io.Discard)
	c.live.coverage = c.snapshotCoverage
	c.live.restored = restoredAgyNative{readable: true, holderID: "holder-then", coveredSeq: 3}
	read := func() wslive.Snapshot {
		t.Helper()
		snap, err := wslive.ReadSnapshot(wslive.SnapshotPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	c.replaying = true
	c.live.forceSnapshot()
	if snap := read(); snap.HolderID != "holder-then" || snap.HolderSeq != 3 {
		t.Fatalf("coverage during the replay = %q/%d, want holder-then/3", snap.HolderID, snap.HolderSeq)
	}
	c.replaying = false
	c.live.forceSnapshot()
	if snap := read(); snap.HolderID != "holder-now" || snap.HolderSeq != 9 {
		t.Fatalf("coverage after the replay = %q/%d, want holder-now/9", snap.HolderID, snap.HolderSeq)
	}
	c.rec.HolderID = ""
	c.live.forceSnapshot()
	if snap := read(); snap.HolderID != "" || snap.HolderSeq != 0 {
		t.Fatalf("coverage without a holder = %q/%d, want none", snap.HolderID, snap.HolderSeq)
	}
}
