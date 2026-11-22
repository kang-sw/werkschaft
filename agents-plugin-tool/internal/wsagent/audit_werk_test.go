package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsaudit"
)

func werkLineJSON(l werk.Line) string {
	l.At = 1_700_000_000_000
	raw, _ := json.Marshal(l)
	return string(raw)
}

// TestWerkAuditMapsEveryLineKindToItsEnvelope pins the adapter per kind:
// one fixture line of each type of the harness's protocol and the envelope
// it yields.
func TestWerkAuditMapsEveryLineKindToItsEnvelope(t *testing.T) {
	a := newWerkAudit("s1")
	at := time.Unix(1_700_000_000, 0)
	one := func(l werk.Line) wsaudit.Envelope {
		t.Helper()
		ok, carried, evs := a.mapLine([]byte(werkLineJSON(l)), at)
		if !ok || len(carried) != 0 || len(evs) != 1 {
			t.Fatalf("%s: recognized %v, carried %d, events %d", l.T, ok, len(carried), len(evs))
		}
		if err := evs[0].ev.Validate(); err != nil {
			t.Fatalf("%s: %v", l.T, err)
		}
		if evs[0].ev.Harness != BackendWerk || evs[0].ev.Session != "s1" || !evs[0].ev.At.Equal(time.UnixMilli(1_700_000_000_000)) {
			t.Fatalf("%s: envelope = %+v", l.T, evs[0].ev)
		}
		return evs[0].ev
	}

	if ev := one(werk.Line{T: werk.LineUser, Turn: 1, Text: "do it"}); ev.Kind != wsaudit.KindUser || ev.Out != "do it" {
		t.Errorf("user = %+v", ev)
	}
	if ev := one(werk.Line{T: werk.LineUser, Turn: 1, Text: "<system-reminder>be careful</system-reminder>"}); ev.Kind != wsaudit.KindSystemReminder {
		t.Errorf("reminder = %+v", ev)
	}
	// Usage opens the response's request in the chain, with its context.
	usage := one(werk.Line{T: werk.LineUsage, Turn: 1, Response: "gen-1", InputTokens: 900, OutputTokens: 40})
	req, _ := usage.Meta[wsaudit.MetaRequest].(map[string]any)
	if usage.Kind != wsaudit.KindSystem || usage.Subkind != werk.LineUsage || req["id"] != "gen-1" || req["ctx"] != int64(900) {
		t.Errorf("usage = %+v", usage)
	}
	if !strings.Contains(usage.Out, `"input_tokens":900`) {
		t.Errorf("usage out = %q", usage.Out)
	}
	thinking := one(werk.Line{T: werk.LineReasoning, Turn: 1, Response: "gen-1", Item: "rs_1", Text: "plan the change"})
	if thinking.Kind != wsaudit.KindThinking || thinking.Out != "plan the change" || thinking.Meta[wsaudit.MetaSignatureOnly] != nil {
		t.Errorf("thinking = %+v", thinking)
	}
	if opaque := one(werk.Line{T: werk.LineReasoning, Turn: 1, Response: "gen-1", Encrypted: true}); opaque.Meta[wsaudit.MetaSignatureOnly] != true || opaque.Out != "" {
		t.Errorf("opaque reasoning = %+v", opaque)
	}
	call := one(werk.Line{T: werk.LineToolStart, Turn: 1, Response: "gen-1", Call: "call_1", Name: "edit", Args: `{"path":"a.go"}`})
	if call.Kind != wsaudit.KindToolCall || call.Subkind != "edit" || call.CallID != "call_1" || call.In != `{"path":"a.go"}` {
		t.Errorf("tool call = %+v", call)
	}
	if r, _ := call.Meta[wsaudit.MetaRequest].(map[string]any); r["id"] != "gen-1" {
		t.Errorf("tool call request = %+v", call.Meta)
	}
	res := one(werk.Line{T: werk.LineToolEnd, Turn: 1, Call: "call_1", Name: "edit", Output: "--- a.go", Failed: true, Truncated: true})
	if res.Kind != wsaudit.KindToolResult || res.CallID != "call_1" || res.Subkind != "edit" || res.Out != "--- a.go" || res.Meta["failed"] != true || res.Meta["truncated"] != true {
		t.Errorf("tool result = %+v", res)
	}
	reply := one(werk.Line{T: werk.LineReply, Turn: 1, Response: "gen-1", Item: "m1", Text: "done", Final: true})
	if reply.Kind != wsaudit.KindAssistantText || reply.Out != "done" {
		t.Errorf("reply = %+v", reply)
	}
	// The next response's request names the one before it.
	next := one(werk.Line{T: werk.LineUsage, Turn: 1, Response: "gen-2", InputTokens: 950})
	if r, _ := next.Meta[wsaudit.MetaRequest].(map[string]any); r["id"] != "gen-2" || r["prev_id"] != "gen-1" {
		t.Errorf("request chain = %+v", next.Meta)
	}
	for _, typ := range []string{werk.LineInit, werk.LineTurnStart, werk.LineTurnEnd, werk.LineRetry, werk.LineSteerAck, werk.LineUserTaken, werk.LineNotice, werk.LineStatus} {
		if ev := one(werk.Line{T: typ, Turn: 1, Text: "x"}); ev.Kind != wsaudit.KindSystem || ev.Subkind != typ || !strings.Contains(ev.Out, `"t":"`+typ+`"`) {
			t.Errorf("%s = %+v", typ, ev)
		}
	}

	// A streamed reply is recognized and dropped; a line outside the
	// protocol is not recognized, so capture keeps it raw.
	if ok, _, evs := a.mapLine([]byte(werkLineJSON(werk.Line{T: werk.LineReply, Item: "m1", Text: "par"})), at); !ok || len(evs) != 0 {
		t.Errorf("streamed reply: recognized %v, events %d", ok, len(evs))
	}
	for _, junk := range []string{"not json", `{"t":"never_heard_of_it"}`, `{"no_type":1}`} {
		if ok, _, evs := a.mapLine([]byte(junk), at); ok || len(evs) != 0 {
			t.Errorf("%q: recognized %v, events %d", junk, ok, len(evs))
		}
	}
}

// runWerkSession runs the real loop against a scripted endpoint and returns
// the lines it wrote: a recording of a session's output.
func runWerkSession(t *testing.T, steps ...func(context.Context, werk.Request) (*werk.Response, error)) []string {
	t.Helper()
	var out lockedBuffer
	agent, err := werk.New(werk.Config{
		Dir: t.TempDir(), Root: t.TempDir(), Authority: werk.AuthorityFull, Model: "vendor/m", Effort: "high",
		Endpoint: &werkModel{steps: steps}, WS: &werkWS{tools: []werk.MCPTool{{Name: "exec.shell"}}},
		Out: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() { _ = agent.Serve(context.Background(), pr); close(done) }()
	_, _ = pw.Write([]byte(`{"type":"user","text":"build it"}` + "\n"))
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), `"t":"turn_end"`) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	pw.Close()
	<-done
	agent.Close()
	return strings.Split(strings.TrimSpace(out.String()), "\n")
}

// TestWerkAuditCapturesARecordedSessionThroughTheTap feeds the lines of a
// session the real loop wrote through the capture tap, as a holder record
// each: reasoning, tool calls and results, hidden events and usage land in
// the audit sink; the visible conversation (user, assistant text) stays out
// of the capture scope; nothing lands as other.
func TestWerkAuditCapturesARecordedSessionThroughTheTap(t *testing.T) {
	reasoning := `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"OPAQUE"}`
	lines := runWerkSession(t,
		func(context.Context, werk.Request) (*werk.Response, error) {
			return &werk.Response{ID: "gen-a", Output: []json.RawMessage{json.RawMessage(reasoning), json.RawMessage(werkCall("c1", "exec_shell", `{"cmd":"make"}`))},
				Usage: werk.Usage{InputTokens: 100, OutputTokens: 10}}, nil
		},
		func(ctx context.Context, req werk.Request) (*werk.Response, error) {
			resp, err := werkMessage("built")(ctx, req)
			resp.ID = "gen-b"
			return resp, err
		},
	)
	sink := &memSink{}
	r := newAuditRigWith(t, sink, "h1", newWerkMapper(""), werkProtocol{})
	for _, line := range lines {
		r.kept(line)
	}
	r.end()
	evs := sink.events()
	for _, k := range []wsaudit.Kind{wsaudit.KindThinking, wsaudit.KindToolCall, wsaudit.KindToolResult, wsaudit.KindSystem} {
		if len(find(evs, ofKind(k))) == 0 {
			t.Errorf("no %s event captured", k)
		}
	}
	for _, k := range []wsaudit.Kind{wsaudit.KindUser, wsaudit.KindAssistantText, wsaudit.KindOther} {
		if got := find(evs, ofKind(k)); len(got) > 0 {
			t.Errorf("%s captured: %.300v", k, got[0])
		}
	}
	calls := map[string]bool{}
	for _, ev := range evs {
		if err := ev.Validate(); err != nil {
			t.Fatalf("%+v: %v", ev, err)
		}
		if ev.Harness != BackendWerk {
			t.Fatalf("harness = %q", ev.Harness)
		}
		if ev.Kind == wsaudit.KindToolCall {
			calls[ev.CallID] = true
		}
	}
	for _, ev := range find(evs, ofKind(wsaudit.KindToolResult)) {
		if !calls[ev.CallID] {
			t.Errorf("tool result %q joins no captured call", ev.CallID)
		}
	}
	th := find(evs, ofKind(wsaudit.KindThinking))
	if th[0].Out != "plan" {
		t.Errorf("thinking = %+v", th[0])
	}
	tc := find(evs, ofKind(wsaudit.KindToolCall))
	if req, _ := tc[0].Meta[wsaudit.MetaRequest].(map[string]any); tc[0].Subkind != "exec_shell" || req["id"] != "gen-a" {
		t.Errorf("tool call = %+v", tc[0])
	}
	// The second response's usage names the first as its predecessor.
	var chained bool
	for _, ev := range find(evs, ofKind(wsaudit.KindSystem)) {
		if req, _ := ev.Meta[wsaudit.MetaRequest].(map[string]any); ev.Subkind == werk.LineUsage && req["id"] == "gen-b" && req["prev_id"] == "gen-a" {
			chained = true
		}
	}
	if !chained {
		t.Error("the request chain does not link the two responses")
	}
}

// lockedBuffer is a buffer a loop writes while a test reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// A provider error body can echo a key or a bearer credential; the audit
// record of the retry and the turn end carries it masked.
func TestWerkAuditMasksCredentialsInSystemLines(t *testing.T) {
	a := newWerkAudit("s1")
	at := time.Unix(1_700_000_000, 0)
	key := "sk-or-v1-0123456789abcdef0123456789abcdef"
	for _, l := range []werk.Line{
		{T: werk.LineRetry, Turn: 1, Attempt: 1, Error: "the model endpoint answered 502: bad key " + key},
		{T: werk.LineTurnEnd, Turn: 1, Reason: werk.ReasonError, Error: "failed with Bearer abcdef123456"},
		{T: werk.LineNotice, Text: "echoed " + key},
	} {
		_, _, evs := a.mapLine([]byte(werkLineJSON(l)), at)
		if len(evs) != 1 {
			t.Fatalf("%s: events %d", l.T, len(evs))
		}
		out := evs[0].ev.Out
		if strings.Contains(out, "0123456789abcdef") || strings.Contains(out, "abcdef123456") {
			t.Errorf("%s keeps a credential: %s", l.T, out)
		}
		if !strings.Contains(out, `"t":"`+l.T+`"`) {
			t.Errorf("%s lost its shape: %s", l.T, out)
		}
	}
}
