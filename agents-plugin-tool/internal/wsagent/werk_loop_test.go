package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcpframe"
	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wslive"
)

func startTestWerkLoop(t *testing.T, dir, session string, model *werkModel, sink wsaudit.Sink, called chan<- string) *werkLoop {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing session authentication")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var call struct {
			Method string
			ID     json.RawMessage
			Params struct {
				Name string
				Meta map[string]string `json:"_meta"`
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var result any
		switch call.Method {
		case "initialize":
			result = map[string]any{}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{
				{"name": "fs.read", "inputSchema": map[string]string{"type": "object"}},
			}}
		case "tools/call":
			if called == nil {
				t.Error("unexpected MCP tool call")
			} else {
				called <- call.Params.Name + ":" + call.Params.Meta[mcpframe.ToolCallIDMeta]
			}
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": "file contents"}}}
		default:
			t.Errorf("unexpected method %q", call.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	spec := StartSpec{Dir: dir, Root: dir, SessionID: session, Model: "fixture-model", PermissionLevel: PermissionReadOnly,
		MCPSession: &MCPSession{URL: srv.URL, Token: "fixture"},
	}
	if sink != nil {
		spec.audit = &auditTap{Session: wsaudit.Session{ID: "fixture-worker", Class: wsaudit.SessionHostedChild}, Sink: sink}
	}
	loop, err := startWerkLoop(context.Background(), spec, model)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loop.Close() })
	return loop
}

func nextWerkLoopEvent(t *testing.T, loop Process, match func(Event) bool) Event {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event, ok := <-loop.Events():
			if !ok {
				t.Fatal("session closed before the expected event")
			}
			if match(event) {
				return event
			}
		case <-timeout.C:
			t.Fatal("timed out waiting for session event")
		}
	}
}

func TestWerkLoopUsesDaemonToolsAndMapsTheTurn(t *testing.T) {
	called := make(chan string, 1)
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(_ context.Context, req werk.Request) (*werk.Response, error) {
			if req.Model != "next-model" || req.Effort != "high" {
				t.Errorf("model options: %q, %+v", req.Model, req.Effort)
			}
			wantTools := slices.DeleteFunc(slices.Clone(req.Tools), func(tool werk.ToolDef) bool { return tool.Name == "load_skill" })
			if len(wantTools) != 1 || wantTools[0].Name != "fs_read" {
				t.Errorf("tools = %+v", req.Tools)
			}
			return &werk.Response{ID: "read-response", Output: []json.RawMessage{
				json.RawMessage(werkCall("read-call", "fs_read", `{"path":"README.md"}`)),
			}}, nil
		},
		func(ctx context.Context, req werk.Request) (*werk.Response, error) {
			if !slices.ContainsFunc(req.Input, func(item json.RawMessage) bool {
				return strings.Contains(string(item), "file contents")
			}) {
				t.Error("model did not see the tool output")
			}
			return werkMessage("finished")(ctx, req)
		},
	}}
	sink := &memSink{}
	loop := startTestWerkLoop(t, t.TempDir(), "", model, sink, called)
	if err := loop.SetModel("next-model"); err != nil {
		t.Fatal(err)
	}
	if err := loop.SetEffort("high"); err != nil {
		t.Fatal(err)
	}
	if err := loop.Send("read the file"); err != nil {
		t.Fatal(err)
	}
	nextWerkLoopEvent(t, loop, func(e Event) bool { return e.SessionID != "" })
	nextWerkLoopEvent(t, loop, func(e Event) bool { return e.TurnStart })
	end := nextWerkLoopEvent(t, loop, func(e Event) bool { return e.TurnEnd })
	if end.TurnError != "" {
		t.Fatal(end.TurnError)
	}
	select {
	case got := <-called:
		if got != "fs.read:read-call" {
			t.Errorf("MCP call = %q", got)
		}
	default:
		t.Error("no MCP tool call")
	}
	if err := loop.Close(); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(sink.events(), func(e wsaudit.Envelope) bool {
		return e.Kind == wsaudit.KindToolResult && e.CallID == "read-call"
	}) {
		t.Error("Close returned before the tool result was captured")
	}
	if err := loop.Send("after close"); err != io.ErrClosedPipe {
		t.Errorf("send after close = %v", err)
	}
}

func TestWerkLoopIsolatesTurnPanicAndInterruptsAnotherSession(t *testing.T) {
	panicking := startTestWerkLoop(t, t.TempDir(), "", &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(context.Context, werk.Request) (*werk.Response, error) {
			panic("sensitive panic detail")
		},
	}}, nil, nil)
	started := make(chan struct{})
	active := startTestWerkLoop(t, t.TempDir(), "", &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(ctx context.Context, _ werk.Request) (*werk.Response, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}}, nil, nil)
	if err := active.Send("a separate session"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("separate session did not reach the model")
	}
	if err := panicking.Send("fail"); err != nil {
		t.Fatal(err)
	}
	failure := nextWerkLoopEvent(t, panicking, func(e Event) bool { return e.TurnEnd })
	if failure.TurnError != "the werk turn failed unexpectedly" {
		t.Errorf("turn error = %q", failure.TurnError)
	}
	if err := active.Steer("also consider this"); err != nil {
		t.Fatal(err)
	}
	steer := nextWerkLoopEvent(t, active, func(e Event) bool { return e.SteerAnswer != nil })
	if steer.SteerAnswer.Refusal != "" {
		t.Errorf("steer refused: %s", steer.SteerAnswer.Refusal)
	}
	if err := active.Interrupt(); err != nil {
		t.Fatal(err)
	}
	end := nextWerkLoopEvent(t, active, func(e Event) bool { return e.TurnEnd })
	if end.TurnReason != wslive.TurnAborted {
		t.Errorf("turn ended with %q", end.TurnReason)
	}
}

func TestWerkLoopKeepsSourcedAuditAcrossOpenings(t *testing.T) {
	dir, session := t.TempDir(), werk.NewSessionID()
	sink := openStoreSink(t, t.TempDir())
	run := func(text string) {
		t.Helper()
		model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
			werkMessage(text),
		}}
		loop := startTestWerkLoop(t, dir, session, model, sink, nil)
		if err := loop.Send(text); err != nil {
			t.Fatal(err)
		}
		if end := nextWerkLoopEvent(t, loop, func(e Event) bool { return e.TurnEnd }); end.TurnError != "" {
			t.Fatal(end.TurnError)
		}
		if err := loop.Close(); err != nil {
			t.Fatal(err)
		}
	}
	run("first request")
	run("second request")

	if err := sink.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, err := sink.Events(context.Background(), "fixture-worker")
	if err != nil {
		t.Fatal(err)
	}
	var usage int
	for _, e := range events {
		if e.Kind == wsaudit.KindSystem && e.Subkind == werk.LineUsage {
			usage++
		}
	}
	if usage != 2 {
		t.Errorf("usage events = %d, want both turns", usage)
	}
}

func TestWerkLoopCloseWaitsForAuditDrain(t *testing.T) {
	sink := &memSink{block: make(chan struct{})}
	var unblock sync.Once
	release := func() { unblock.Do(func() { close(sink.block) }) }
	defer release()
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		werkMessage("finished"),
	}}
	loop := startTestWerkLoop(t, t.TempDir(), "", model, sink, nil)
	if err := loop.Send("queue audit events"); err != nil {
		t.Fatal(err)
	}
	nextWerkLoopEvent(t, loop, func(e Event) bool { return e.TurnEnd })

	closed := make(chan error, 1)
	go func() { closed <- loop.Close() }()
	<-loop.ctx.Done()
	select {
	case err := <-closed:
		t.Fatalf("Close returned while the audit sink was blocked: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish after audit drained")
	}
	if !slices.ContainsFunc(sink.events(), func(e wsaudit.Envelope) bool {
		return e.Kind == wsaudit.KindSystem && e.Subkind == werk.LineUsage
	}) {
		t.Error("Close lost the queued usage record")
	}
}

type testCredentialSource func(context.Context, wscred.Request) (wscred.Credential, error)

func (s testCredentialSource) Credential(ctx context.Context, req wscred.Request) (wscred.Credential, error) {
	return s(ctx, req)
}

func TestWerkLoopReadsProviderCredentialsInProcessForEachRequest(t *testing.T) {
	refusal := errors.New("credential withdrawn")
	keys := []string{"sk-or-v1-fixture-first", "sk-or-v1-fixture-second"}
	calls := 0
	source := testCredentialSource(func(_ context.Context, req wscred.Request) (wscred.Credential, error) {
		if req != (wscred.Request{Provider: werk.Provider, AgentSession: true, Background: true}) {
			t.Errorf("provider request = %+v", req)
		}
		calls++
		if calls > len(keys) {
			return wscred.Credential{}, refusal
		}
		return wscred.Credential{Provider: req.Provider, Secret: wscred.NewSecret(keys[calls-1])}, nil
	})
	name, reg := (WerkBackend{}).registration()
	regs := WithProviderCredentials(map[string]BackendRegistration{name: reg}, source, func(provider string) (bool, error) {
		return provider == string(werk.Provider), nil
	})
	backend := regs[name].Backend.(WerkBackend)
	client, err := backend.loopResponder()
	if err != nil {
		t.Fatal(err)
	}
	if provider, _ := wscred.Lookup(werk.Provider); client.BaseURL != provider.BaseURL {
		t.Errorf("provider endpoint = %q", client.BaseURL)
	}
	got := make(chan string, len(keys))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		got <- header
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, "rejected %s", header)
	}))
	defer srv.Close()
	client.BaseURL = srv.URL

	for _, key := range keys {
		_, err := client.Respond(context.Background(), werk.Request{Model: "fixture-model"}, werk.StreamHandler{})
		var httpErr *werk.HTTPError
		if !errors.As(err, &httpErr) || strings.Contains(err.Error(), key) {
			t.Fatalf("unexpected or unredacted provider error: %v", err)
		}
		select {
		case header := <-got:
			if header != "Bearer "+key {
				t.Error("request did not use the current credential")
			}
		default:
			t.Fatal("request did not reach the provider")
		}
	}
	_, err = client.Respond(context.Background(), werk.Request{Model: "fixture-model"}, werk.StreamHandler{})
	if !errors.Is(err, refusal) || calls != 3 {
		t.Errorf("after credential withdrawal: %v, %d source calls", err, calls)
	}
}
