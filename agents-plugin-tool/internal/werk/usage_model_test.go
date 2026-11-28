package werk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponsesCompletedCapturesResponseModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\n" +
			`data: {"type":"response.completed","response":{"id":"resp-m1","model":"anthropic/claude-sonnet-4-20250514","output":[{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":120,"output_tokens":30,"cost":0.0042}}}` + "\n\n"))
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
		Token:   func(context.Context) (string, error) { return "test-token", nil },
	}
	resp, err := client.Respond(context.Background(), Request{Model: "anthropic/claude-sonnet-4"}, StreamHandler{})
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if resp.Model != "anthropic/claude-sonnet-4-20250514" {
		t.Fatalf("resp.Model = %q, want %q", resp.Model, "anthropic/claude-sonnet-4-20250514")
	}
	if resp.Usage.InputTokens != 120 || resp.Usage.OutputTokens != 30 || resp.Usage.CostUSD == nil || *resp.Usage.CostUSD != 0.0042 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestLineUsageEmitsResponseModelAndFallsBackToRequestModel(t *testing.T) {
	cost1, cost2 := 0.015, 0.025
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		func(_ context.Context, _ Request, _ StreamHandler) (*Response, error) {
			return &Response{
				ID:     "r1",
				Model:  "openai/gpt-5.4-resolved",
				Output: []json.RawMessage{json.RawMessage(msgItem("first"))},
				Usage:  Usage{InputTokens: 200, OutputTokens: 50, CostUSD: &cost1},
			}, nil
		},
		func(_ context.Context, _ Request, _ StreamHandler) (*Response, error) {
			return &Response{
				ID:     "r2",
				Output: []json.RawMessage{json.RawMessage(msgItem("second"))},
				Usage:  Usage{InputTokens: 300, OutputTokens: 60, CostUSD: &cost2},
			}, nil
		},
	}}

	var usageLines []Line
	turnEnded := make(chan struct{}, 2)
	a, err := New(Config{
		Ephemeral: true,
		Model:     "openai/gpt-5.4",
		Authority: AuthorityReadOnly,
		Endpoint:  model,
		Emit: func(line Line) {
			if line.T == LineUsage {
				usageLines = append(usageLines, line)
			}
			if line.T == LineTurnEnd {
				turnEnded <- struct{}{}
			}
		},
	})
	must(t, err)
	defer a.Close()

	ctx := context.Background()
	cmds := make(chan Command, 2)
	done := make(chan error, 1)
	go func() { done <- a.ServeCommands(ctx, cmds) }()
	cmds <- Command{Type: CmdUser, Text: "turn 1"}
	<-turnEnded
	cmds <- Command{Type: CmdUser, Text: "turn 2"}
	<-turnEnded
	close(cmds)
	must(t, <-done)

	if len(usageLines) != 2 {
		t.Fatalf("usageLines = %d, want 2: %+v", len(usageLines), usageLines)
	}
	if usageLines[0].Model != "openai/gpt-5.4-resolved" || usageLines[0].Compaction {
		t.Fatalf("turn 1 usage line = %+v", usageLines[0])
	}
	if usageLines[1].Model != "openai/gpt-5.4" || usageLines[1].Compaction {
		t.Fatalf("turn 2 usage line = %+v", usageLines[1])
	}
}

func TestCompactionEmitsLineUsageWithModelAndTokens(t *testing.T) {
	ctx := context.Background()
	dir, session := t.TempDir(), NewSessionID()
	cost := 0.0085
	type ctxKey struct{}
	var sawWrapped bool

	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(msgItem("Initial reply.")),
		func(rctx context.Context, req Request, _ StreamHandler) (*Response, error) {
			sawWrapped, _ = rctx.Value(ctxKey{}).(bool)
			return &Response{
				ID:     "compact-r1",
				Model:  req.Model + "-resolved",
				Output: []json.RawMessage{json.RawMessage(compactProseReply("Summary prose."))},
				Usage:  Usage{InputTokens: 900, OutputTokens: 180, CachedTokens: 400, ReasoningTokens: 50, CostUSD: &cost},
			}, nil
		},
	}}

	var lines []Line
	a, err := New(Config{
		Dir:       dir,
		Session:   session,
		Model:     "anthropic/claude-sonnet-4",
		Authority: AuthorityReadOnly,
		Endpoint:  model,
		WrapTurnContext: func(c context.Context) context.Context {
			return context.WithValue(c, ctxKey{}, true)
		},
		Emit: func(line Line) { lines = append(lines, line) },
	})
	must(t, err)
	defer a.Close()

	must(t, a.runDurableTurn(ctx, ctx, Command{Type: CmdUser, TurnID: "t1", Text: "Hello"}))
	lines = nil
	a.reportCompaction(ctx, nil)

	if !sawWrapped {
		t.Fatal("compactModelProse did not invoke WrapTurnContext")
	}
	if len(lines) < 2 {
		t.Fatalf("expected LineUsage + LineCheckpoint, got %+v", lines)
	}
	u := lines[len(lines)-2]
	cp := lines[len(lines)-1]
	if cp.T != LineCheckpoint {
		t.Fatalf("last line = %+v, want LineCheckpoint", cp)
	}
	if u.T != LineUsage || !u.Compaction {
		t.Fatalf("penultimate line = %+v, want compaction LineUsage", u)
	}
	if u.Model != "anthropic/claude-sonnet-4-resolved" || u.Response != "compact-r1" {
		t.Fatalf("compaction usage identity = %+v", u)
	}
	if u.InputTokens != 900 || u.OutputTokens != 180 || u.CachedTokens != 400 || u.ReasoningTokens != 50 {
		t.Fatalf("compaction usage tokens = %+v", u)
	}
	if u.CostUSD == nil || *u.CostUSD != cost {
		t.Fatalf("compaction usage cost = %v, want %v", u.CostUSD, cost)
	}
}
