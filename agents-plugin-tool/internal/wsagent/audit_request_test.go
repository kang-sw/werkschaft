package wsagent

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

func requestOf(t *testing.T, ev wsaudit.Envelope) map[string]any {
	t.Helper()
	req, _ := ev.Meta[wsaudit.MetaRequest].(map[string]any)
	if req == nil {
		t.Fatalf("%s event carries no request: %+v", ev.Kind, ev)
	}
	return req
}

func wantRequest(t *testing.T, ev wsaudit.Envelope, id, prev string, ctx int64, hasCtx bool) {
	t.Helper()
	req := requestOf(t, ev)
	if req["id"] != wsaudit.NormalizeID(id) {
		t.Fatalf("%s request id = %v, want %s", ev.Kind, req["id"], id)
	}
	if got, ok := req["prev_id"]; prev == "" && ok || prev != "" && got != wsaudit.NormalizeID(prev) {
		t.Fatalf("%s request prev_id = %v, want %q", ev.Kind, got, prev)
	}
	got, ok := req["ctx"]
	if ok != hasCtx || hasCtx && got != ctx {
		t.Fatalf("%s request ctx = %v (present %v), want %d (present %v)", ev.Kind, got, ok, ctx, hasCtx)
	}
}

func claudeAssistant(id, usage string, blocks ...string) string {
	u := ""
	if usage != "" {
		u = `,"usage":` + usage
	}
	return fmt.Sprintf(`{"type":"assistant","message":{"id":%q,"content":[%s]%s}}`, id, strings.Join(blocks, ","), u)
}

func claudeToolUse(id string) string {
	return fmt.Sprintf(`{"type":"tool_use","id":%q,"name":"Read","input":{"file_path":"/x"}}`, id)
}

func usage(input, read, create int64) string {
	return fmt.Sprintf(`{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":9}`, input, read, create)
}

func mapAll(a *claudeAudit, lines ...string) []wsaudit.Envelope {
	var out []wsaudit.Envelope
	for _, l := range lines {
		_, carried, evs := a.mapLine([]byte(l), time.Now())
		out = append(out, envelopesOf(carried, evs)...)
	}
	return append(out, envelopesOf(a.flush(time.Now()))...)
}

// envelopesOf is the envelopes of mapped events, in order.
func envelopesOf(sets ...[]auditEvent) []wsaudit.Envelope {
	var out []wsaudit.Envelope
	for _, set := range sets {
		for _, m := range set {
			out = append(out, m.ev)
		}
	}
	return out
}

// TestClaudeAuditRequestChain pins Decision 7 for Claude: parallel calls of
// one assistant message share its id and ctx, whether they arrive on one
// line or on several lines of the same message (the chain advances once); a
// text-only message is read for the chain though it is not captured, so the
// next request's prev_id names it; a message without usage omits ctx; and
// long IDs take the shared digest form.
func TestClaudeAuditRequestChain(t *testing.T) {
	long := "msg_" + strings.Repeat("L", 300)
	evs := mapAll(newClaudeAudit("s1"),
		claudeAssistant("msg_A", usage(2, 100, 10), `{"type":"thinking","thinking":"plan","signature":"s"}`),
		claudeAssistant("msg_A", usage(2, 100, 10), claudeToolUse("call_1"), claudeToolUse("call_2")),
		claudeAssistant("msg_A", usage(2, 100, 10), claudeToolUse("call_3")),
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"ok"}]}}`,
		claudeAssistant("msg_T", usage(3, 200, 20), `{"type":"text","text":"reading it now"}`),
		claudeAssistant("msg_B", usage(4, 300, 30), claudeToolUse("call_4")),
		claudeAssistant("msg_N", "", claudeToolUse("call_5")),
		claudeAssistant(long, usage(1, 1, 1), claudeToolUse("call_6")),
	)
	calls := find(evs, ofKind(wsaudit.KindToolCall))
	if len(calls) != 6 {
		t.Fatalf("tool calls = %d, want 6: %+v", len(calls), calls)
	}
	th := find(evs, ofKind(wsaudit.KindThinking))
	wantRequest(t, th[0], "msg_A", "", 112, true)
	for _, c := range calls[:3] {
		wantRequest(t, c, "msg_A", "", 112, true)
	}
	wantRequest(t, calls[3], "msg_B", "msg_T", 334, true)
	wantRequest(t, calls[4], "msg_N", "msg_B", 0, false)
	wantRequest(t, calls[5], long, "msg_N", 3, true)
	if id := requestOf(t, calls[5])["id"].(string); !strings.HasPrefix(id, "sha256:") {
		t.Fatalf("a long request id = %q, want the digest form", id)
	}
	if res := find(evs, ofKind(wsaudit.KindToolResult)); len(res) != 1 || res[0].Meta[wsaudit.MetaRequest] != nil {
		t.Fatalf("a tool result came from no model request: %+v", res)
	}
	for _, ev := range evs {
		if err := ev.Validate(); err != nil {
			t.Fatalf("%+v: %v", ev, err)
		}
	}
}

// TestClaudeAuditRequestOnAssembledBlocks pins that a block assembled from
// streamed partial lines carries the request of the message_start that
// opened it, ctx included, even when no assistant line follows.
func TestClaudeAuditRequestOnAssembledBlocks(t *testing.T) {
	evs := mapAll(newClaudeAudit("s1"),
		claudeAssistant("msg_P", usage(1, 50, 5), `{"type":"text","text":"before"}`),
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_S","usage":`+usage(2, 60, 6)+`}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_s","name":"Bash"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"ls\"}"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		auditResultLine,
	)
	calls := find(evs, ofKind(wsaudit.KindToolCall))
	if len(calls) != 1 || calls[0].Meta[wsaudit.MetaAssembled] != true {
		t.Fatalf("assembled calls = %+v", calls)
	}
	wantRequest(t, calls[0], "msg_S", "msg_P", 68, true)
}

// TestClaudeAuditRequestChainsPerConversation pins the per-conversation
// chains over the recorded subagent turn: the lead's Agent call, the native
// subagent's text-only request (keyed by its parent_tool_use_id), then the
// lead's next request, whose prev_id is the lead's earlier request and never
// the interleaved subagent one.
func TestClaudeAuditRequestChainsPerConversation(t *testing.T) {
	const (
		leadFirst = "msg_011Cgrkj9qGXhKzbYx4DnJnZ"
		subagent  = "msg_011CgrkjFjEFcQCdokzETNQU"
		leadNext  = "msg_011CgrkjFtAZj5d4oZbj6c5m"
	)
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	r.replayFixture("claude-subagent-turn.jsonl")
	r.keptRun(claudeAssistant("msg_after", usage(2, 7270, 40), claudeToolUse("call_after")), 0, 0)
	r.end()
	evs := sink.events()

	calls := find(evs, ofKind(wsaudit.KindToolCall))
	if len(calls) != 2 {
		t.Fatalf("lead tool calls = %+v", calls)
	}
	wantRequest(t, calls[0], leadFirst, "", 2+0+6916, true)
	sub := find(evs, func(ev wsaudit.Envelope) bool { return ev.Kind == wsaudit.KindSubagent && ev.Subkind == "text" })
	if len(sub) != 1 {
		t.Fatalf("subagent text events = %+v", sub)
	}
	wantRequest(t, sub[0], subagent, "", 2+0+3944, true)
	th := find(evs, ofKind(wsaudit.KindThinking))
	if len(th) != 1 {
		t.Fatalf("thinking = %+v", th)
	}
	wantRequest(t, th[0], leadNext, leadFirst, 2+6916+352, true)
	// leadNext arrived on a thinking line and a text-only line; the request
	// after it names leadNext. (TestClaudeAuditRequestChain discriminates a
	// chain that advances per line rather than per message.)
	wantRequest(t, calls[1], "msg_after", leadNext, 2+7270+40, true)
}
