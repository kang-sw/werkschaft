package werk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
)

func decodeRequestBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	return m
}

func findToolByType(t *testing.T, body map[string]any, typ string) (map[string]any, bool) {
	t.Helper()
	rawTools, ok := body["tools"]
	if !ok || rawTools == nil {
		return nil, false
	}
	list, ok := rawTools.([]any)
	if !ok {
		t.Fatalf("tools is %T, want array", rawTools)
	}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == typ {
			return m, true
		}
	}
	return nil, false
}

func TestRequestBodyWebSearchPerProvider(t *testing.T) {
	base := Request{
		Model: "openai/gpt-5",
		Tools: []ToolDef{{Name: "fs_read"}},
		WebSearch: &WebSearchRequest{
			Engine:     "exa",
			MaxUses:    2,
			MaxResults: 5,
		},
	}

	t.Run("openrouter present", func(t *testing.T) {
		raw, err := base.bodyFor(wscred.ProviderOpenRouter, "")
		if err != nil {
			t.Fatal(err)
		}
		body := decodeRequestBody(t, raw)
		if _, ok := findToolByType(t, body, "function"); !ok {
			t.Fatalf("missing function tool in %s", raw)
		}
		ws, ok := findToolByType(t, body, "openrouter:web_search")
		if !ok {
			t.Fatalf("missing openrouter:web_search tool in %s", raw)
		}
		params, ok := ws["parameters"].(map[string]any)
		if !ok {
			t.Fatalf("parameters = %#v", ws["parameters"])
		}
		if params["engine"] != "exa" || params["max_uses"] != float64(2) || params["max_results"] != float64(5) {
			t.Fatalf("unexpected openrouter parameters: %#v", params)
		}
		if _, hasMaxToolCalls := body["max_tool_calls"]; hasMaxToolCalls {
			t.Fatalf("unexpected max_tool_calls in %s", raw)
		}
	})

	t.Run("openrouter absent when WebSearch nil", func(t *testing.T) {
		req := base
		req.WebSearch = nil
		raw, err := req.bodyFor(wscred.ProviderOpenRouter, "")
		if err != nil {
			t.Fatal(err)
		}
		body := decodeRequestBody(t, raw)
		if _, ok := findToolByType(t, body, "openrouter:web_search"); ok {
			t.Fatalf("unexpected openrouter:web_search in %s", raw)
		}
	})

	t.Run("openai siwc bare web_search without max_tool_calls", func(t *testing.T) {
		raw, err := base.bodyFor(wscred.ProviderOpenAI, "")
		if err != nil {
			t.Fatal(err)
		}
		body := decodeRequestBody(t, raw)
		ws, ok := findToolByType(t, body, "web_search")
		if !ok {
			t.Fatalf("missing web_search tool in %s", raw)
		}
		if len(ws) != 1 {
			t.Fatalf("SIWC web_search must carry only type, got %#v", ws)
		}
		if _, hasMaxToolCalls := body["max_tool_calls"]; hasMaxToolCalls {
			t.Fatalf("SIWC request must not carry max_tool_calls: %s", raw)
		}
	})

	t.Run("local endpoints never attach search", func(t *testing.T) {
		for _, dialect := range []string{"ollama", "vllm"} {
			raw, err := base.bodyFor(wscred.ProviderID("local/desk"), dialect)
			if err != nil {
				t.Fatal(err)
			}
			body := decodeRequestBody(t, raw)
			if _, ok := findToolByType(t, body, "openrouter:web_search"); ok {
				t.Fatalf("dialect %s attached openrouter:web_search", dialect)
			}
			if _, ok := findToolByType(t, body, "web_search"); ok {
				t.Fatalf("dialect %s attached web_search", dialect)
			}
		}
	})
}

type fakeWSTools struct{}

func (fakeWSTools) ListTools(context.Context) ([]MCPTool, error) {
	return []MCPTool{{Name: "fs.read"}}, nil
}

func (fakeWSTools) Call(_ context.Context, name string, _ json.RawMessage) (CallResult, error) {
	return CallResult{Text: "ok:" + name}, nil
}

func sseCompleted(id string, outputJSON string, usageJSON string) string {
	usagePart := ""
	if usageJSON != "" {
		usagePart = `,"usage":` + usageJSON
	}
	return fmt.Sprintf("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"output\":%s%s}}\n\n", id, outputJSON, usagePart)
}

func runSingleTurn(t *testing.T, a *Agent, text string) []Line {
	t.Helper()
	return runTurns(t, a, text)
}

func runTurns(t *testing.T, a *Agent, prompts ...string) []Line {
	t.Helper()
	var (
		mu    sync.Mutex
		lines []Line
	)
	doneCh := make(chan struct{}, len(prompts))
	origEmit := a.out.emit
	a.out.emit = func(l Line) {
		if origEmit != nil {
			origEmit(l)
		}
		mu.Lock()
		lines = append(lines, l)
		mu.Unlock()
		if l.T == LineTurnEnd {
			doneCh <- struct{}{}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmds := make(chan Command, len(prompts))
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.ServeCommands(ctx, cmds)
	}()
	for _, p := range prompts {
		cmds <- Command{Type: CmdUser, Text: p}
		select {
		case <-doneCh:
		case <-ctx.Done():
			t.Fatal("timed out waiting for turn end")
		}
	}
	close(cmds)
	if err := <-errCh; err != nil {
		t.Fatalf("ServeCommands: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]Line(nil), lines...)
}

func TestAgentWebSearchConfigPresenceAndSIWCDefaultOff(t *testing.T) {
	cases := []struct {
		name     string
		provider wscred.ProviderID
		dialect  string
		policy   *WebSearchPolicy
		wantType string
		wantEng  string
		wantUses int
		wantRes  int
	}{
		{
			name:     "openrouter default on",
			provider: wscred.ProviderOpenRouter,
			policy:   nil, // DefaultWebSearchPolicy
			wantType: "openrouter:web_search",
			wantEng:  "exa",
			wantUses: 2,
			wantRes:  5,
		},
		{
			name:     "openrouter disabled by config",
			provider: wscred.ProviderOpenRouter,
			policy:   &WebSearchPolicy{Enabled: false, MaxUses: 2, MaxResults: 5, Engine: "exa"},
			wantType: "",
		},
		{
			name:     "openrouter custom engine and limits",
			provider: wscred.ProviderOpenRouter,
			policy:   &WebSearchPolicy{Enabled: true, MaxUses: 3, MaxResults: 8, Engine: "native"},
			wantType: "openrouter:web_search",
			wantEng:  "native",
			wantUses: 3,
			wantRes:  8,
		},
		{
			name:     "siwc absent by default",
			provider: wscred.ProviderOpenAI,
			policy:   nil,
			wantType: "",
		},
		{
			name:     "siwc present when opted in",
			provider: wscred.ProviderOpenAI,
			policy:   &WebSearchPolicy{Enabled: true, MaxUses: 2, MaxResults: 5, Engine: "exa", SIWC: true},
			wantType: "web_search",
		},
		{
			name:     "siwc disabled when master switch off",
			provider: wscred.ProviderOpenAI,
			policy:   &WebSearchPolicy{Enabled: false, MaxUses: 2, MaxResults: 5, Engine: "exa", SIWC: true},
			wantType: "",
		},
		{
			name:     "local ollama absent even when enabled",
			provider: wscred.ProviderID("local/ollama"),
			dialect:  "ollama",
			policy:   &WebSearchPolicy{Enabled: true, MaxUses: 2, MaxResults: 5, Engine: "exa", SIWC: true},
			wantType: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, sseCompleted("resp-1",
					`[{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"done"}]}]`,
					`{"input_tokens":10,"output_tokens":5}`))
			}))
			defer srv.Close()

			client := &Client{
				BaseURL:  srv.URL,
				Provider: tc.provider,
				Dialect:  tc.dialect,
				Token:    func(context.Context) (string, error) { return "test-token", nil },
			}
			cfg := Config{
				Dir:       t.TempDir(),
				Authority: AuthorityReadOnly,
				Model:     "test-model",
				Endpoint:  client,
				WS:        fakeWSTools{},
				Emit:      func(Line) {},
			}
			if tc.policy != nil {
				p := *tc.policy
				cfg.WebSearch = func() WebSearchPolicy { return p }
			}
			a, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()

			runSingleTurn(t, a, "hello")
			body := decodeRequestBody(t, captured)
			if tc.wantType == "" {
				if _, ok := findToolByType(t, body, "openrouter:web_search"); ok {
					t.Fatalf("unexpected openrouter:web_search in %s", captured)
				}
				if _, ok := findToolByType(t, body, "web_search"); ok {
					t.Fatalf("unexpected web_search in %s", captured)
				}
				return
			}
			ws, ok := findToolByType(t, body, tc.wantType)
			if !ok {
				t.Fatalf("expected tool %q in %s", tc.wantType, captured)
			}
			if tc.wantType == "openrouter:web_search" {
				params := ws["parameters"].(map[string]any)
				if params["engine"] != tc.wantEng || params["max_uses"] != float64(tc.wantUses) || params["max_results"] != float64(tc.wantRes) {
					t.Fatalf("parameters = %#v, want engine=%s max_uses=%d max_results=%d", params, tc.wantEng, tc.wantUses, tc.wantRes)
				}
			}
		})
	}
}

func TestAgentWebSearchMultiRequestTurnBudgetAndCost(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []map[string]any
		step     int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		step++
		n := step
		requests = append(requests, decodeRequestBody(t, raw))
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		switch n {
		case 1:
			// Request 1 uses 1 search and invokes a tool -> budget drops 2 -> 1.
			_, _ = io.WriteString(w, sseCompleted("resp-1",
				`[{"type":"function_call","id":"fc1","call_id":"c1","name":"fs_read","arguments":"{}"}]`,
				`{"input_tokens":100,"output_tokens":20,"server_tool_use":{"web_search_requests":1},"cost":0.015}`))
		case 2:
			// Request 2 uses 1 search and invokes a tool -> budget drops 1 -> 0.
			_, _ = io.WriteString(w, sseCompleted("resp-2",
				`[{"type":"function_call","id":"fc2","call_id":"c2","name":"fs_read","arguments":"{}"}]`,
				`{"input_tokens":120,"output_tokens":25,"server_tool_use":{"web_search_requests":1},"cost":0.012}`))
		case 3:
			// Request 3 must omit search; returns final answer.
			_, _ = io.WriteString(w, sseCompleted("resp-3",
				`[{"type":"message","id":"m3","role":"assistant","content":[{"type":"output_text","text":"turn1-done"}]}]`,
				`{"input_tokens":150,"output_tokens":30,"cost":0.003}`))
		default:
			// Turn 2 resets budget to 2.
			_, _ = io.WriteString(w, sseCompleted("resp-4",
				`[{"type":"message","id":"m4","role":"assistant","content":[{"type":"output_text","text":"turn2-done"}]}]`,
				`{"input_tokens":50,"output_tokens":10,"cost":0.001}`))
		}
	}))
	defer srv.Close()

	a, err := New(Config{
		Dir:       t.TempDir(),
		Authority: AuthorityReadOnly,
		Model:     "openai/gpt-5",
		Endpoint: &Client{
			BaseURL:  srv.URL,
			Provider: wscred.ProviderOpenRouter,
			Token:    func(context.Context) (string, error) { return "test-token", nil },
		},
		WS:   fakeWSTools{},
		Emit: func(Line) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	lines := runTurns(t, a, "turn one", "turn two")

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 4 {
		t.Fatalf("got %d requests, want 4", len(requests))
	}
	// Request 1: max_uses=2
	ws1, ok := findToolByType(t, requests[0], "openrouter:web_search")
	if !ok || ws1["parameters"].(map[string]any)["max_uses"] != float64(2) {
		t.Fatalf("req 1 search tool = %#v, want max_uses=2", ws1)
	}
	// Request 2: max_uses=1
	ws2, ok := findToolByType(t, requests[1], "openrouter:web_search")
	if !ok || ws2["parameters"].(map[string]any)["max_uses"] != float64(1) {
		t.Fatalf("req 2 search tool = %#v, want max_uses=1", ws2)
	}
	// Request 3: omitted once budget spent
	if ws3, ok := findToolByType(t, requests[2], "openrouter:web_search"); ok {
		t.Fatalf("req 3 must omit search tool once budget spent, got %#v", ws3)
	}
	// Request 4 (next turn): budget reset to max_uses=2
	ws4, ok := findToolByType(t, requests[3], "openrouter:web_search")
	if !ok || ws4["parameters"].(map[string]any)["max_uses"] != float64(2) {
		t.Fatalf("req 4 (turn 2) search tool = %#v, want reset max_uses=2", ws4)
	}

	var costs []float64
	for _, l := range lines {
		if l.T == LineUsage && l.CostUSD != nil {
			costs = append(costs, *l.CostUSD)
		}
	}
	wantCosts := []float64{0.015, 0.012, 0.003, 0.001}
	if len(costs) != len(wantCosts) {
		t.Fatalf("usage costs = %v, want %v", costs, wantCosts)
	}
	for i := range wantCosts {
		if costs[i] != wantCosts[i] {
			t.Fatalf("usage cost[%d] = %v, want %v", i, costs[i], wantCosts[i])
		}
	}
}

func TestAgentWebSearchOverrunAndZeroUsageRefund(t *testing.T) {
	t.Run("zero searches refunds full budget", func(t *testing.T) {
		var requests []map[string]any
		step := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			requests = append(requests, decodeRequestBody(t, raw))
			step++
			w.Header().Set("Content-Type", "text/event-stream")
			if step == 1 {
				_, _ = io.WriteString(w, sseCompleted("r1",
					`[{"type":"function_call","id":"fc1","call_id":"c1","name":"fs_read","arguments":"{}"}]`,
					`{"input_tokens":10,"output_tokens":5}`))
				return
			}
			_, _ = io.WriteString(w, sseCompleted("r2",
				`[{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"done"}]}]`,
				`{"input_tokens":10,"output_tokens":5}`))
		}))
		defer srv.Close()

		a, err := New(Config{
			Dir: t.TempDir(), Authority: AuthorityReadOnly, Model: "m",
			Endpoint: &Client{BaseURL: srv.URL, Provider: wscred.ProviderOpenRouter, Token: func(context.Context) (string, error) { return "tok", nil }},
			WS:       fakeWSTools{}, Emit: func(Line) {},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()

		runSingleTurn(t, a, "no search needed")
		if len(requests) != 2 {
			t.Fatalf("got %d requests, want 2", len(requests))
		}
		ws2, ok := findToolByType(t, requests[1], "openrouter:web_search")
		if !ok || ws2["parameters"].(map[string]any)["max_uses"] != float64(2) {
			t.Fatalf("req 2 should retain full budget 2, got %#v", ws2)
		}
	})

	t.Run("siwc web_search_call items exhaust budget between requests", func(t *testing.T) {
		var requests []map[string]any
		step := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			requests = append(requests, decodeRequestBody(t, raw))
			step++
			w.Header().Set("Content-Type", "text/event-stream")
			if step == 1 {
				_, _ = io.WriteString(w, sseCompleted("r1",
					`[{"type":"web_search_call","id":"ws1"},{"type":"web_search_call","id":"ws2"},{"type":"function_call","id":"fc1","call_id":"c1","name":"fs_read","arguments":"{}"}]`,
					`{"input_tokens":10,"output_tokens":5}`))
				return
			}
			_, _ = io.WriteString(w, sseCompleted("r2",
				`[{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"done"}]}]`,
				`{"input_tokens":10,"output_tokens":5}`))
		}))
		defer srv.Close()

		a, err := New(Config{
			Dir: t.TempDir(), Authority: AuthorityReadOnly, Model: "gpt-5",
			Endpoint: &Client{BaseURL: srv.URL, Provider: wscred.ProviderOpenAI, Token: func(context.Context) (string, error) { return "tok", nil }},
			WebSearch: func() WebSearchPolicy {
				return WebSearchPolicy{Enabled: true, MaxUses: 2, MaxResults: 5, Engine: "exa", SIWC: true}
			},
			WS: fakeWSTools{}, Emit: func(Line) {},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()

		runSingleTurn(t, a, "search on siwc")
		if len(requests) != 2 {
			t.Fatalf("got %d requests, want 2", len(requests))
		}
		if _, ok := findToolByType(t, requests[0], "web_search"); !ok {
			t.Fatal("req 1 expected web_search")
		}
		if _, ok := findToolByType(t, requests[1], "web_search"); ok {
			t.Fatal("req 2 must omit web_search after 2 web_search_call items")
		}
	})

	t.Run("missing usage leaves reservation charged", func(t *testing.T) {
		var requests []map[string]any
		step := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			requests = append(requests, decodeRequestBody(t, raw))
			step++
			w.Header().Set("Content-Type", "text/event-stream")
			if step == 1 {
				// Completed response with NO usage field at all.
				_, _ = io.WriteString(w, sseCompleted("r1",
					`[{"type":"function_call","id":"fc1","call_id":"c1","name":"fs_read","arguments":"{}"}]`,
					""))
				return
			}
			_, _ = io.WriteString(w, sseCompleted("r2",
				`[{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"done"}]}]`,
				`{"input_tokens":10,"output_tokens":5}`))
		}))
		defer srv.Close()

		a, err := New(Config{
			Dir: t.TempDir(), Authority: AuthorityReadOnly, Model: "m",
			Endpoint: &Client{BaseURL: srv.URL, Provider: wscred.ProviderOpenRouter, Token: func(context.Context) (string, error) { return "tok", nil }},
			WS:       fakeWSTools{}, Emit: func(Line) {},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()

		runSingleTurn(t, a, "missing usage")
		if len(requests) != 2 {
			t.Fatalf("got %d requests, want 2", len(requests))
		}
		if _, ok := findToolByType(t, requests[1], "openrouter:web_search"); ok {
			t.Fatal("req 2 must omit search when req 1 returned no usage")
		}
	})
}

func TestAgentWebSearchCutStreamAndFailureKeepReservationCharged(t *testing.T) {
	for _, mode := range []string{"cut-stream", "http-500"} {
		t.Run(mode, func(t *testing.T) {
			var (
				mu       sync.Mutex
				requests []map[string]any
				attempt  int
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				attempt++
				n := attempt
				requests = append(requests, decodeRequestBody(t, raw))
				mu.Unlock()

				if n == 1 {
					if mode == "http-500" {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
						return
					}
					// Cut stream: send partial text delta and close without response.completed.
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"m1\",\"delta\":\"hi\"}\n\n")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, sseCompleted("r-retry",
					`[{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}]`,
					`{"input_tokens":10,"output_tokens":5}`))
			}))
			defer srv.Close()

			a, err := New(Config{
				Dir: t.TempDir(), Authority: AuthorityReadOnly, Model: "openai/gpt-5",
				Endpoint: &Client{
					BaseURL:  srv.URL,
					Provider: wscred.ProviderOpenRouter,
					Token:    func(context.Context) (string, error) { return "test-token", nil },
				},
				Backoff: func(int) time.Duration { return time.Millisecond },
				Sleep:   func(context.Context, time.Duration) error { return nil },
				WS:      fakeWSTools{},
				Emit:    func(Line) {},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()

			runSingleTurn(t, a, "search and retry")

			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 2 {
				t.Fatalf("got %d attempts, want 2", len(requests))
			}
			if _, ok := findToolByType(t, requests[0], "openrouter:web_search"); !ok {
				t.Fatal("attempt 1 must include openrouter:web_search")
			}
			if ws, ok := findToolByType(t, requests[1], "openrouter:web_search"); ok {
				t.Fatalf("retry attempt 2 must omit search after %s, got %#v", mode, ws)
			}
		})
	}
}

func TestDurableTurnResumeDeductsPriorSearches(t *testing.T) {
	dir := t.TempDir()
	session := NewSessionID()

	// Seed a durable turn that already completed one response using 2 searches
	// and a pending read-only tool call, simulating a crash before the second request.
	store, _, err := OpenStore(dir, session, func() int64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	used := 2
	if err := store.appendBarrier(turnStartRecord{K: "turn_start", TurnID: "turn-1", User: userItem("research")}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendResponse(&Response{
		ID:     "resp-before-crash",
		Output: []json.RawMessage{json.RawMessage(`{"type":"function_call","id":"fc1","call_id":"c1","name":"fs_read","arguments":"{}"}`)},
		Usage:  Usage{InputTokens: 10, OutputTokens: 5, WebSearchRequests: &used},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseCompleted("resp-after-resume",
			`[{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"done"}]}]`,
			`{"input_tokens":20,"output_tokens":10}`))
	}))
	defer srv.Close()

	a, err := New(Config{
		Dir: dir, Session: session, Authority: AuthorityReadOnly, Model: "openai/gpt-5",
		Endpoint: &Client{
			BaseURL:  srv.URL,
			Provider: wscred.ProviderOpenRouter,
			Token:    func(context.Context) (string, error) { return "test-token", nil },
		},
		WS:   fakeWSTools{},
		Emit: func(Line) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	doneCh := make(chan struct{}, 1)
	a.out.emit = func(l Line) {
		if l.T == LineTurnEnd {
			doneCh <- struct{}{}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmds := make(chan Command, 1)
	errCh := make(chan error, 1)
	go func() { errCh <- a.ServeCommands(ctx, cmds) }()
	cmds <- Command{Type: CmdUser, TurnID: "turn-1", Text: "research"}
	select {
	case <-doneCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for resumed durable turn")
	}
	close(cmds)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	body := decodeRequestBody(t, captured)
	if ws, ok := findToolByType(t, body, "openrouter:web_search"); ok {
		t.Fatalf("resumed durable turn must omit search after 2 prior searches, got %#v", ws)
	}
}
