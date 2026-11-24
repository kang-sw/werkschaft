package wsagent

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wslive"
)

func agyAuditLine(update string) string {
	return `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"acp-id","update":` + update + `}}`
}

func TestAgyAuditUpdates(t *testing.T) {
	_, adapter := newAgyMapper("acp-id").newAudit("audit-id")
	at := time.Now()
	ev := func(kind wsaudit.Kind, subkind string) wsaudit.Envelope {
		e, err := wsaudit.NewEnvelope("audit-id", BackendAgy, at, kind)
		if err != nil {
			t.Fatal(err)
		}
		e.Subkind = subkind
		return e
	}

	thought := ev(wsaudit.KindThinking, "")
	thought.Out = "considering options"
	thought.Meta = map[string]any{wsaudit.MetaAssembled: true}
	call := ev(wsaudit.KindToolCall, "read_file")
	call.CallID, call.In = "c1", `{"path":"notes.md"}`
	result := ev(wsaudit.KindToolResult, "read_file")
	result.CallID, result.Out = "c1", `{"text":"notes"}`
	result.Meta = map[string]any{"status": "completed"}
	nativeCall := ev(wsaudit.KindToolCall, "invoke_subagent")
	nativeCall.CallID, nativeCall.In = "c2", `{"Role":"Reviewer"}`
	launch := ev(wsaudit.KindSubagent, "launch_started")
	launch.AgentID, launch.ParentID, launch.Out = wslive.NativeAgyPrefix+"c2", "c2", "Reviewer"
	nativeResult := ev(wsaudit.KindToolResult, "invoke_subagent")
	nativeResult.CallID, nativeResult.Out = "c2", `[{"type":"content","content":{"type":"text","text":"launch failed"}}]`
	nativeResult.Meta = map[string]any{"status": "failed"}
	launchFailed := ev(wsaudit.KindSubagent, "launch_failed")
	launchFailed.AgentID, launchFailed.ParentID = launch.AgentID, launch.ParentID
	const info = `{"sessionUpdate":"session_info_update","title":"Session title"}`
	system := ev(wsaudit.KindSystem, "session_info_update")
	system.Out = info

	for _, tc := range []struct {
		name        string
		update      string
		recognized  bool
		wantCarried []wsaudit.Envelope
		want        []wsaudit.Envelope
	}{
		{"thought", `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"considering options"}}`, true, nil, nil},
		{"call start", `{"sessionUpdate":"tool_call","toolCallId":"c1","name":"read_file","status":"pending","args":{"path":"notes.md"}}`, true, []wsaudit.Envelope{thought}, []wsaudit.Envelope{call}},
		{"call progress", `{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"in_progress"}`, true, nil, nil},
		{"call complete", `{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"completed","rawOutput":{"text":"notes"}}`, true, nil, []wsaudit.Envelope{result}},
		{"native launch", `{"sessionUpdate":"tool_call","toolCallId":"c2","name":"invoke_subagent","rawInput":{"Role":"Reviewer"}}`, true, nil, []wsaudit.Envelope{nativeCall, launch}},
		{"native failure", `{"sessionUpdate":"tool_call_update","toolCallId":"c2","status":"failed","content":` + nativeResult.Out + `}`, true, nil, []wsaudit.Envelope{nativeResult, launchFailed}},
		{"session info", info, true, nil, []wsaudit.Envelope{system}},
		{"unknown", `{"sessionUpdate":"future_update","payload":"keep raw"}`, false, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recognized, carried, mapped := adapter.mapLine([]byte(agyAuditLine(tc.update)), at)
			if gotCarried, got := envelopesOf(carried), envelopesOf(mapped); recognized != tc.recognized || !reflect.DeepEqual(gotCarried, tc.wantCarried) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("recognized = %v, carried = %+v, mapped = %+v, want %v, %+v, %+v", recognized, gotCarried, got, tc.recognized, tc.wantCarried, tc.want)
			}
		})
	}
}

func TestAgyAuditToolIdentityOnReplay(t *testing.T) {
	_, live := newAgyMapper("acp-id").newAudit("audit-id")
	_, replay := newAgyMapper("acp-id").newAudit("audit-id")
	at := time.Now()
	start := agyAuditLine(`{"sessionUpdate":"tool_call","toolCallId":"c1","name":"invoke_subagent"}`)
	end := agyAuditLine(`{"sessionUpdate":"tool_call_update","toolCallId":"c1","name":"invoke_subagent","status":"completed"}`)
	_, _, started := live.mapLine([]byte(start), at)
	_, _, ended := live.mapLine([]byte(end), at)
	_, _, replayed := replay.mapLine([]byte(end), at)
	if len(started) != 2 || len(ended) != 2 || len(replayed) != 4 {
		t.Fatalf("started = %+v, ended = %+v, replayed = %+v", started, ended, replayed)
	}
	for i, ev := range started {
		if ev.key == "" || replayed[i].key != ev.key {
			t.Errorf("start key = %q, replayed key = %q", ev.key, replayed[i].key)
		}
	}
	for i, ev := range ended {
		if got := replayed[i+len(started)]; got.slot != ev.slot || !reflect.DeepEqual(got.ev, ev.ev) {
			t.Errorf("replayed terminal event = %+v, live = %+v", got, ev)
		}
	}
	if ended[1].ev.Subkind != "launch_completed" {
		t.Fatalf("native launch completion = %+v", ended[1])
	}
}

func TestAgyAuditCapture(t *testing.T) {
	thought := agyAuditLine(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"private reasoning"}}`)
	user := agyAuditLine(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"visible user text"}}`)
	reply := agyAuditLine(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"visible reply text"}}`)
	unknown := agyAuditLine(`{"sessionUpdate":"future_update","payload":"keep raw"}`)

	check := func(t *testing.T, sink *memSink) {
		t.Helper()
		got := sink.events()
		if want := []wsaudit.Kind{wsaudit.KindThinking, wsaudit.KindOther}; !slices.Equal(kindsOf(got), want) {
			t.Fatalf("captured events = %+v, want %v", got, want)
		}
		if got[0].Out != "private reasoning" || got[1].Raw != unknown {
			t.Fatalf("captured bodies = %+v", got)
		}
		for _, e := range got {
			if e.Harness != BackendAgy || e.Session != "s1" {
				t.Errorf("capture identity = %+v", e)
			}
		}
	}

	t.Run("lead holder tee", func(t *testing.T) {
		sink := &memSink{}
		r := newAuditRigWith(t, sink, "h1", newAgyMapper("acp-id"), agyProtocol{})
		if r.p.audit == nil {
			t.Fatal("prepareAudit did not attach the agy lead tap")
		}
		r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
		r.tee(thought)
		r.keptRun(agyRingPulse, 1, 0)
		for _, line := range []string{user, reply, unknown} {
			r.keptRun(line, 0, 0)
		}
		r.audit(wsholder.Event{Event: wsholder.EventAuditFence, Anchor: r.anchor, Final: true})
		r.end()
		check(t, sink)
	})
	t.Run("worker stream", func(t *testing.T) {
		sink := &memSink{}
		mapper := newAgyMapper("acp-id")
		p := newStreamProcess(mapper, agyProtocol{})
		p.prepareAudit(StartSpec{audit: &auditTap{
			Session: wsaudit.Session{ID: "s1", Class: wsaudit.SessionHostedChild},
			Sink:    sink,
		}}, mapper)
		p.startAudit("proc-fixture")
		if p.audit == nil {
			t.Fatal("prepareAudit did not attach the agy worker tap")
		}
		t.Cleanup(p.closeAudit)
		for _, line := range []string{thought, user, reply, unknown} {
			p.feed([]byte(line), 0, false)
		}
		p.closeAudit()
		select {
		case <-p.audit.done:
		case <-time.After(10 * time.Second):
			t.Fatal("capture did not drain")
		}
		check(t, sink)
	})
}

func TestAgyAuditThoughtAssemblyLossAndFlush(t *testing.T) {
	_, adapter := newAgyMapper("acp-id").newAudit("audit-id")
	t0 := time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Second)
	t2 := t0.Add(2 * time.Second)

	if adapter.markLoss(1, wsaudit.LossDropped) {
		t.Fatal("markLoss returned true with no open thought")
	}

	// Contiguous thought chunks across ws_activity pulses and empty chunks assemble into one event.
	for _, line := range []struct {
		raw string
		at  time.Time
	}{
		{agyAuditLine(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"part 1 "}}`), t0},
		{agyRingPulse, t1},
		{agyAuditLine(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":""}}`), t1},
		{agyAuditLine(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"part 2"}}`), t1},
	} {
		rec, carried, mapped := adapter.mapLine([]byte(line.raw), line.at)
		if !rec || len(carried) != 0 || len(mapped) != 0 {
			t.Fatalf("line %s -> rec=%v carried=%+v mapped=%+v", line.raw, rec, carried, mapped)
		}
	}
	rec, carried, mapped := adapter.mapLine([]byte(agyAuditLine(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"reply"}}`)), t2)
	if !rec || len(mapped) != 0 || len(carried) != 1 {
		t.Fatalf("flush on agent_message_chunk -> rec=%v carried=%+v mapped=%+v", rec, carried, mapped)
	}
	th := carried[0].ev
	if th.Kind != wsaudit.KindThinking || th.Out != "part 1 part 2" || !th.At.Equal(t0) || th.Meta[wsaudit.MetaAssembled] != true {
		t.Fatalf("assembled thought = %+v", th)
	}
	if _, ok := th.Meta[wsaudit.MetaIncomplete]; ok {
		t.Fatalf("complete thought marked incomplete: %+v", th.Meta)
	}

	// Loss during an open thought attaches to the thought without a standalone marker.
	adapter.mapLine([]byte(agyAuditLine(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"before drop"}}`)), t0)
	if !adapter.markLoss(3, wsaudit.LossDropped) {
		t.Fatal("markLoss returned false with an open thought")
	}
	adapter.mapLine([]byte(agyAuditLine(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":" after"}}`)), t1)
	_, carried, _ = adapter.mapLine([]byte(agyAuditLine(`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"next"}}`)), t2)
	if len(carried) != 1 {
		t.Fatalf("carried after loss = %+v", carried)
	}
	lost := carried[0].ev
	if lost.Out != "before drop after" || lost.Meta[wsaudit.MetaIncomplete] != true ||
		lost.Meta[wsaudit.MetaLossReason] != wsaudit.LossDropped || lost.Meta[wsaudit.MetaLost] != uint64(3) {
		t.Fatalf("lost thought = %+v", lost)
	}

	// Stream end flush marks an unclosed thought incomplete with LossStreamEnd.
	adapter.mapLine([]byte(agyAuditLine(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"trailing"}}`)), t0)
	flushed := adapter.flush(t2)
	if len(flushed) != 1 {
		t.Fatalf("flush = %+v", flushed)
	}
	endEv := flushed[0].ev
	if endEv.Out != "trailing" || endEv.Meta[wsaudit.MetaAssembled] != true ||
		endEv.Meta[wsaudit.MetaIncomplete] != true || endEv.Meta[wsaudit.MetaLossReason] != wsaudit.LossStreamEnd {
		t.Fatalf("stream-end thought = %+v", endEv)
	}
	if again := adapter.flush(t2); len(again) != 0 {
		t.Fatalf("second flush emitted %+v", again)
	}
}

func TestAgyAuditThoughtCaptureCap(t *testing.T) {
	thought := func(text string) string {
		return agyAuditLine(fmt.Sprintf(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":%q}}`, text))
	}
	boundary := agyAuditLine(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"reply"}}`)
	third := agyThoughtCaptureBytes / 3
	for _, tc := range []struct {
		name     string
		chunks   []int
		overflow int
	}{
		{"across_finalized_intervals", []int{third, third, agyThoughtCaptureBytes - 2*third, 1, 4}, 3},
		{"single_oversized_chunk", []int{agyThoughtCaptureBytes + 1, 4}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := newBoundarySink()
			r := newAuditRigWith(t, sink, "h1", newAgyMapper("acp-id"), agyProtocol{})
			adapter := r.p.audit.adapter.(*agyAudit)
			r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
			for i, size := range tc.chunks {
				r.tee(thought(strings.Repeat("x", size)))
				var dropped uint64
				if i > tc.overflow {
					dropped = 1
				}
				seq := r.keptRun(agyRingPulse, 1, dropped)
				waitUntil(t, "activity pulse finalized", func() bool {
					return sink.Recover("s1", "h1").Boundary >= seq
				})
				// Finalize synchronizes this inspection with the capture
				// goroutine while it waits for the next interval.
				th := adapter.thought
				if th == nil || cap(th.body) > agyThoughtCaptureBytes || i >= tc.overflow && th.body != nil {
					t.Fatalf("chunk %d retained state exceeds its budget or kept a discarded body", i)
				}
				want := 0
				if i >= tc.overflow {
					want = 1
				}
				if got := len(sink.events()); got != want {
					t.Fatalf("chunk %d recorded %d events, want %d", i, got, want)
				}
			}
			r.keptRun(boundary, 0, 0)
			r.tee(thought("normal thought"))
			r.keptRun(agyRingPulse, 1, 0)
			r.keptRun(boundary, 0, 0)
			r.end()

			events := sink.events()
			if want := []wsaudit.Kind{wsaudit.KindThinking, wsaudit.KindThinking}; !slices.Equal(kindsOf(events), want) {
				t.Fatalf("captured event kinds = %v, want %v", kindsOf(events), want)
			}
			marker := events[0]
			if marker.Out != "" || marker.In != "" || marker.Raw != "" ||
				marker.Meta[wsaudit.MetaLossReason] != wsaudit.LossCaptureCap ||
				marker.Meta[wsaudit.MetaIncomplete] != true || marker.Meta[wsaudit.MetaAssembled] != true {
				t.Fatalf("capture-cap marker has body lengths (%d, %d, %d) and metadata %+v",
					len(marker.Out), len(marker.In), len(marker.Raw), marker.Meta)
			}
			if ev := events[1]; ev.Out != "normal thought" || ev.Meta[wsaudit.MetaIncomplete] != nil {
				t.Fatalf("next thought = %+v", ev)
			}
		})
	}
}

func TestAgyAuditCapturesEveryKindFromRecordings(t *testing.T) {
	names := []string{
		"agy-1.5.10-hosted-lead.jsonl",
		"agy-1.5.12-compaction-probe-1.jsonl",
		"agy-1.5.12-compaction-probe-2.jsonl",
	}
	rawThoughtChunks := 0
	var evs []wsaudit.Envelope
	perFile := map[string][]wsaudit.Envelope{}
	for i, name := range names {
		sink := &memSink{}
		r := newAuditRigWith(t, sink, fmt.Sprintf("h%d", i+1), newAgyMapper(""), agyProtocol{})
		for _, line := range claudeFixtureLines(t, name) {
			if strings.HasPrefix(string(line), ">> ") {
				continue
			}
			if strings.Contains(string(line), `"agent_thought_chunk"`) {
				rawThoughtChunks++
			}
			r.kept(string(line))
		}
		r.end()
		got := sink.events()
		perFile[name] = got
		evs = append(evs, got...)
	}
	for _, k := range []wsaudit.Kind{wsaudit.KindThinking, wsaudit.KindToolCall, wsaudit.KindToolResult, wsaudit.KindSystem, wsaudit.KindCompaction} {
		if len(find(evs, ofKind(k))) == 0 {
			t.Errorf("no %s event captured", k)
		}
	}
	for _, k := range []wsaudit.Kind{wsaudit.KindUser, wsaudit.KindAssistantText, wsaudit.KindOther} {
		if got := find(evs, ofKind(k)); len(got) > 0 {
			t.Errorf("%s captured: %.300v", k, got[0])
		}
	}
	// agy-1.5.10-hosted-lead.jsonl runs /compact at line 123 (used 38791 -> 28415 on next turn)
	// and switches models at lines 79/87 (window changes reset baseline without false compaction).
	leadEvs := perFile["agy-1.5.10-hosted-lead.jsonl"]
	leadCompactions := find(leadEvs, ofKind(wsaudit.KindCompaction))
	if len(leadCompactions) != 1 {
		t.Fatalf("agy-1.5.10-hosted-lead compactions = %+v, want exactly 1", leadCompactions)
	}
	marked, cut := wsaudit.ApplyModelSeenCut(leadEvs)
	if !cut.HasCompaction || !cut.Estimated || cut.Index <= 0 || cut.Index >= len(marked)-1 {
		t.Fatalf("lead cut = %+v (len=%d), want HasCompaction=true Estimated=true mid-session", cut, len(marked))
	}
	if marked[0].ModelSeen || !marked[len(marked)-1].ModelSeen {
		t.Fatalf("lead model_seen first=%v last=%v, want false then true", marked[0].ModelSeen, marked[len(marked)-1].ModelSeen)
	}
	// agy-1.5.12-compaction-probe-{1,2}.jsonl stay below UsageDropMinDelta; raw stdout carries
	// no explicit compaction event (manual /compact is inferred on CommandEndConfirms).
	for _, probe := range []string{"agy-1.5.12-compaction-probe-1.jsonl", "agy-1.5.12-compaction-probe-2.jsonl"} {
		if got := find(perFile[probe], ofKind(wsaudit.KindCompaction)); len(got) != 0 {
			t.Fatalf("%s compactions = %+v, want 0 on raw stdout alone", probe, got)
		}
	}
	thoughts := find(evs, ofKind(wsaudit.KindThinking))
	if rawThoughtChunks == 0 || len(thoughts) >= rawThoughtChunks {
		t.Fatalf("thoughts = %d, raw chunks = %d; want contiguous chunks assembled", len(thoughts), rawThoughtChunks)
	}
	for _, th := range thoughts {
		if th.Meta[wsaudit.MetaAssembled] != true {
			t.Fatalf("thought missing assembled mark: %+v", th)
		}
	}
	calls := map[string]bool{}
	for _, ev := range evs {
		if err := ev.Validate(); err != nil {
			t.Fatalf("%+v: %v", ev, err)
		}
		if ev.Harness != BackendAgy {
			t.Fatalf("harness = %q", ev.Harness)
		}
		// Decision 7: ACP does not carry a per-request ID, so no event carries meta.request.
		if _, ok := ev.Meta[wsaudit.MetaRequest]; ok {
			t.Fatalf("agy event carries meta.request without a request ID: %+v", ev)
		}
		if ev.Kind == wsaudit.KindToolCall {
			if ev.CallID == "" || ev.Subkind == "" {
				t.Fatalf("tool call without id or name: %+v", ev)
			}
			calls[ev.CallID] = true
		}
	}
	for _, ev := range find(evs, ofKind(wsaudit.KindToolResult)) {
		if !calls[ev.CallID] {
			t.Fatalf("tool result %q joins no captured call", ev.CallID)
		}
	}
}

// TestAgyRingCallIDMatchesTheAuditCallID pins (601) for Antigravity: the
// captured tool_call and tool_result events carry raw ACP toolCallIds while
// the ring tools row carries wsaudit.NormalizeID(raw), covering a long >256B
// ID (digested), a short literal ID, and a digest-shaped literal ID.
func TestAgyRingCallIDMatchesTheAuditCallID(t *testing.T) {
	long := "agy-call-" + strings.Repeat("x", 300)
	short := "agy-short"
	prefixed := wsaudit.DigestPrefix + "literal-id"
	callLine := func(id string) string {
		return agyAuditLine(fmt.Sprintf(`{"sessionUpdate":"tool_call","toolCallId":%q,"name":"read_file","status":"pending","args":{"path":"a.go"}}`, id))
	}
	doneLine := func(id string) string {
		return agyAuditLine(fmt.Sprintf(`{"sessionUpdate":"tool_call_update","toolCallId":%q,"status":"completed","rawOutput":{"ok":true}}`, id))
	}
	lines := []string{
		callLine(long),
		callLine(short),
		callLine(prefixed),
		doneLine(long),
		doneLine(short),
		doneLine(prefixed),
		agyAuditLine(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done"}}`),
		`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`,
	}
	sink := &memSink{}
	r := newAuditRigWith(t, sink, "h1", newAgyMapper("acp-id"), agyProtocol{})
	for _, line := range lines {
		r.kept(line)
	}
	waitUntil(t, "the batches", func() bool { return r.displayLines() == len(lines) })
	r.end()
	st := wslive.NewState("")
	r.mu.Lock()
	for i, ev := range r.evs {
		if ev.Live != nil {
			if _, err := st.Apply(*ev.Live, int64(1000+i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	r.mu.Unlock()
	res, err := st.Apply(wslive.Event{Kind: wslive.KindReply, Final: true, Text: "done"}, 5000)
	if err != nil {
		t.Fatal(err)
	}
	var ring []string
	for _, e := range res.Ring {
		if e.Kind == wslive.RingTools {
			for _, b := range e.Tools.Batches {
				for _, c := range b.CallArgs {
					ring = append(ring, c.ID)
				}
			}
		}
	}
	wantRing := []string{wsaudit.NormalizeID(long), short, wsaudit.NormalizeID(prefixed)}
	if !slices.Equal(ring, wantRing) {
		t.Fatalf("ring call ids = %q, want %q", ring, wantRing)
	}
	keyed := map[string][]wsaudit.Kind{}
	var raw []string
	for _, ev := range sink.events() {
		if ev.CallID != "" {
			raw = append(raw, ev.CallID)
			key := wsaudit.NormalizeID(ev.CallID)
			keyed[key] = append(keyed[key], ev.Kind)
		}
	}
	wantRaw := []string{long, short, prefixed, long, short, prefixed}
	if !slices.Equal(raw, wantRaw) {
		t.Fatalf("envelope call ids = %q, want raw ids %q", raw, wantRaw)
	}
	for _, id := range ring {
		if !slices.Equal(keyed[id], []wsaudit.Kind{wsaudit.KindToolCall, wsaudit.KindToolResult}) {
			t.Fatalf("history row id %q opens %v, want its call and its result", id, keyed[id])
		}
	}
}

// TestAgyInferredCompactionCut pins Decision 6 for Antigravity: both
// daemon-inferred compaction paths (UsageDropRatio and CommandEndConfirms on
// /compact) write a KindCompaction envelope with meta.estimated=true so
// ApplyModelSeenCut marks Cut.Estimated=true and earlier events unseen.
func TestAgyInferredCompactionCut(t *testing.T) {
	thoughtLine := func(text string) string {
		return agyAuditLine(fmt.Sprintf(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":%q}}`, text))
	}

	t.Run("usage_drop", func(t *testing.T) {
		h, f := newAgyLeadHarness(t)
		sink := &memSink{}
		h.opts.AuditSink = sink
		h.startAt("http://127.0.0.1:1")
		id := h.lead(BackendAgy, LeadOptions{}).ID
		f.replyID = id

		h.send(id, "turn-1")
		holder := h.holder(id)
		eventually(t, "turn 1 prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
		h.holders.out(holder,
			thoughtLine("before auto compact"),
			agyUsageLine(100000, 1048576),
			agyUsageLine(30000, 1048576),
			thoughtLine("after auto compact"),
			agyPromptDoneLine(agyPromptIDs(h, holder)[0]),
		)
		h.idle(id)

		eventually(t, "inferred usage_drop compaction in audit sink", func() bool {
			evs := sink.events()
			return len(find(evs, ofKind(wsaudit.KindCompaction))) == 1 &&
				len(find(evs, func(ev wsaudit.Envelope) bool {
					return ev.Kind == wsaudit.KindThinking && ev.Out == "after auto compact"
				})) == 1
		})

		evs := sink.events()
		marked, cut := wsaudit.ApplyModelSeenCut(evs)
		if !cut.HasCompaction || !cut.Estimated {
			t.Fatalf("cut = %+v, want HasCompaction=true Estimated=true", cut)
		}
		cmpEv := marked[cut.Index]
		if cmpEv.Kind != wsaudit.KindCompaction || cmpEv.Subkind != string(wslive.TriggerAuto) ||
			cmpEv.Meta[wsaudit.MetaEstimated] != true || cmpEv.Meta["inferred_by"] != "usage_drop" ||
			cmpEv.Meta["pre_tokens"] != int64(100000) || cmpEv.Meta["post_tokens"] != int64(30000) {
			t.Fatalf("compaction event = %+v", cmpEv)
		}
		pre := find(marked, func(ev wsaudit.Envelope) bool {
			return ev.Kind == wsaudit.KindThinking && ev.Out == "before auto compact"
		})
		post := find(marked, func(ev wsaudit.Envelope) bool {
			return ev.Kind == wsaudit.KindThinking && ev.Out == "after auto compact"
		})
		if len(pre) != 1 || pre[0].ModelSeen {
			t.Fatalf("pre-compaction thought = %+v, want ModelSeen=false", pre)
		}
		if len(post) != 1 || !post[0].ModelSeen {
			t.Fatalf("post-compaction thought = %+v, want ModelSeen=true", post)
		}
	})

	t.Run("command_end", func(t *testing.T) {
		h, f := newAgyLeadHarness(t)
		auditRoot := t.TempDir()
		sink := openStoreSink(t, auditRoot)
		h.opts.AuditSink = sink
		h.startAt("http://127.0.0.1:1")
		h.runner.mu.Lock()
		h.endpoint = h.runner.endpoint
		h.runner.mu.Unlock()
		id := h.lead(BackendAgy, LeadOptions{}).ID
		f.replyID = id
		key := h.record(id).WSKey
		events := func() []wsaudit.Envelope {
			t.Helper()
			if err := sink.Flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			evs, err := sink.Events(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			return evs
		}
		restart := func() {
			t.Helper()
			h.runner.Close()
			if err := sink.Close(); err != nil {
				t.Fatal(err)
			}
			sink = openStoreSink(t, auditRoot)
			h.opts.AuditSink = sink
			h.restart()
			wait(t, "rejoined agy holder", h.runner.rejoined)
			eventually(t, "agy replay complete", func() bool {
				c := h.runner.conv(id)
				if c == nil {
					return false
				}
				var ready bool
				c.do(func() { ready = c.proc != nil && !c.replaying && !c.turn })
				return ready
			})
		}

		h.send(id, "turn-1")
		holder := h.holder(id)
		eventually(t, "turn 1 prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
		h.holders.out(holder,
			thoughtLine("before manual compact"),
			agyUsageLine(100000, 1048576),
			agyPromptDoneLine(agyPromptIDs(h, holder)[0]),
		)
		h.idle(id)

		noResume := false
		if res := h.runner.Compact(key, "", &noResume); res.Status != "queued" {
			t.Fatalf("Compact() = %+v, want queued", res)
		}
		eventually(t, "compact prompt", func() bool { return len(agyPromptIDs(h, holder)) == 2 })
		h.holders.out(holder, agyPromptDoneLine(agyPromptIDs(h, holder)[1]))
		h.idle(id)
		var beforeRestart wsaudit.Envelope
		eventually(t, "retained manual cut before restart", func() bool {
			cuts := find(events(), ofKind(wsaudit.KindCompaction))
			if len(cuts) != 1 {
				return false
			}
			beforeRestart = cuts[0]
			return true
		})
		restart()
		if got := h.holder(id); got != holder {
			t.Fatalf("holder changed after manual cut: %s -> %s", holder, got)
		}

		// The first sample after the manual cut arrives live after restart.
		h.send(id, "turn-2")
		eventually(t, "turn 2 prompt", func() bool { return len(agyPromptIDs(h, holder)) == 3 })
		h.holders.out(holder,
			agyUsageLine(25000, 1048576),
			thoughtLine("after manual compact"),
			agyPromptDoneLine(agyPromptIDs(h, holder)[2]),
		)
		h.idle(id)

		eventually(t, "inferred command_end compaction in audit sink", func() bool {
			evs := events()
			return len(find(evs, ofKind(wsaudit.KindCompaction))) >= 1 &&
				len(find(evs, func(ev wsaudit.Envelope) bool {
					return ev.Kind == wsaudit.KindThinking && ev.Out == "after manual compact"
				})) == 1
		})

		evs := events()
		compactions := find(evs, ofKind(wsaudit.KindCompaction))
		if len(compactions) != 1 {
			t.Fatalf("compactions = %+v, want exactly 1 (no duplicate on follow-up usage drop)", compactions)
		}
		marked, cut := wsaudit.ApplyModelSeenCut(evs)
		if !cut.HasCompaction || !cut.Estimated {
			t.Fatalf("cut = %+v, want HasCompaction=true Estimated=true", cut)
		}
		cmpEv := marked[cut.Index]
		if cmpEv.ID != beforeRestart.ID || cmpEv.Seq != beforeRestart.Seq ||
			cmpEv.Subkind != string(wslive.TriggerManual) ||
			cmpEv.Meta[wsaudit.MetaEstimated] != true || cmpEv.Meta["inferred_by"] != "command_end" {
			t.Fatalf("compaction event = %+v", cmpEv)
		}
		pre := find(marked, func(ev wsaudit.Envelope) bool {
			return ev.Kind == wsaudit.KindThinking && ev.Out == "before manual compact"
		})
		post := find(marked, func(ev wsaudit.Envelope) bool {
			return ev.Kind == wsaudit.KindThinking && ev.Out == "after manual compact"
		})
		if len(pre) != 1 || pre[0].ModelSeen {
			t.Fatalf("pre-compaction thought = %+v, want ModelSeen=false", pre)
		}
		if len(post) != 1 || !post[0].ModelSeen {
			t.Fatalf("post-compaction thought = %+v, want ModelSeen=true", post)
		}

		// Now the replay includes the post-cut sample. It must still be the
		// baseline for a genuine live drop after reattachment.
		restart()
		if got := h.holder(id); got != holder {
			t.Fatalf("holder changed after post-cut usage: %s -> %s", holder, got)
		}
		h.send(id, "turn-3")
		eventually(t, "turn 3 prompt", func() bool { return len(agyPromptIDs(h, holder)) == 4 })
		h.holders.out(holder, agyUsageLine(12000, 1048576), agyPromptDoneLine(agyPromptIDs(h, holder)[3]))
		h.idle(id)
		eventually(t, "retained live compaction after replayed usage", func() bool {
			compactions = find(events(), ofKind(wsaudit.KindCompaction))
			return len(compactions) >= 2
		})
		if len(compactions) != 2 || compactions[0].ID != beforeRestart.ID ||
			compactions[1].Subkind != string(wslive.TriggerAuto) || compactions[1].Meta["inferred_by"] != "usage_drop" {
			t.Fatalf("compactions after the genuine live drop = %+v", compactions)
		}
	})
}
