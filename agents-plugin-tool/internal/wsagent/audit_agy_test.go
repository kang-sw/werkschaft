package wsagent

import (
	"reflect"
	"slices"
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
		name       string
		update     string
		recognized bool
		want       []wsaudit.Envelope
	}{
		{"thought", `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"considering options"}}`, true, []wsaudit.Envelope{thought}},
		{"call start", `{"sessionUpdate":"tool_call","toolCallId":"c1","name":"read_file","status":"pending","args":{"path":"notes.md"}}`, true, []wsaudit.Envelope{call}},
		{"call progress", `{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"in_progress"}`, true, nil},
		{"call complete", `{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"completed","rawOutput":{"text":"notes"}}`, true, []wsaudit.Envelope{result}},
		{"native launch", `{"sessionUpdate":"tool_call","toolCallId":"c2","name":"invoke_subagent","rawInput":{"Role":"Reviewer"}}`, true, []wsaudit.Envelope{nativeCall, launch}},
		{"native failure", `{"sessionUpdate":"tool_call_update","toolCallId":"c2","status":"failed","content":` + nativeResult.Out + `}`, true, []wsaudit.Envelope{nativeResult, launchFailed}},
		{"session info", info, true, []wsaudit.Envelope{system}},
		{"unknown", `{"sessionUpdate":"future_update","payload":"keep raw"}`, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recognized, carried, mapped := adapter.mapLine([]byte(agyAuditLine(tc.update)), at)
			if got := envelopesOf(mapped); recognized != tc.recognized || len(carried) != 0 || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("recognized = %v, carried = %v, mapped = %+v, want %v, no carried events, %+v", recognized, carried, got, tc.recognized, tc.want)
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
