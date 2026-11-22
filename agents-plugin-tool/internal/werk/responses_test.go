package werk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sse renders events as a server-sent stream.
func sse(events ...[2]string) string {
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", e[0], e[1])
	}
	return b.String()
}

func ev(typ, data string) [2]string { return [2]string{typ, data} }

func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Token: func(context.Context) (string, error) { return "sk-test-secret", nil }, HTTP: srv.Client()}
}

func streamOK(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	io.WriteString(w, body)
}

const reasoningItem = `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thinking about it"}],"encrypted_content":"OPAQUE=="}`
const callItem = `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"path\":\"a.txt\"}","status":"completed"}`

func TestRespondStreamedToolCallKeepsItemsAsReceived(t *testing.T) {
	var got map[string]any
	var auth string
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/responses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		streamOK(w, sse(
			ev("response.created", `{"type":"response.created","response":{"id":"resp_1"}}`),
			ev("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1"}}`),
			ev("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","delta":"thinking "}`),
			ev("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","delta":"about it"}`),
			ev("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":`+reasoningItem+`}`),
			ev("response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":""}}`),
			ev("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\":"}`),
			ev("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"\"a.txt\"}"}`),
			ev("response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{\"path\":\"a.txt\"}"}`),
			ev("response.output_item.done", `{"type":"response.output_item.done","output_index":1,"item":`+callItem+`}`),
			ev("response.completed", `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[`+reasoningItem+`,`+callItem+`],"usage":{"input_tokens":120,"output_tokens":30,"input_tokens_details":{"cached_tokens":100},"output_tokens_details":{"reasoning_tokens":12},"cost":0.0031}}}`),
		))
	})
	var summary strings.Builder
	resp, err := client.Respond(context.Background(), Request{
		Model: "vendor/model", Instructions: "be brief", Effort: "high",
		Input: []json.RawMessage{json.RawMessage(`{"type":"message","role":"user","content":"hi"}`)},
		Tools: []ToolDef{{Name: "read", Description: "read a file"}},
	}, StreamHandler{SummaryDelta: func(_, d string) { summary.WriteString(d) }})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-test-secret" {
		t.Errorf("authorization header = %q", auth)
	}
	if got["store"] != false || got["stream"] != true || got["model"] != "vendor/model" {
		t.Errorf("request = %v", got)
	}
	if _, ok := got["previous_response_id"]; ok {
		t.Error("a stateless request carries no previous_response_id")
	}
	if r, _ := got["reasoning"].(map[string]any); r["effort"] != "high" {
		t.Errorf("reasoning = %v", got["reasoning"])
	}
	if len(resp.Output) != 2 || string(resp.Output[0]) != reasoningItem || string(resp.Output[1]) != callItem {
		t.Errorf("output items were not kept as received: %s", resp.Output)
	}
	if summary.String() != "thinking about it" {
		t.Errorf("summary = %q", summary.String())
	}
	u := resp.Usage
	if u.InputTokens != 120 || u.OutputTokens != 30 || u.CachedTokens != 100 || u.ReasoningTokens != 12 || u.CostUSD == nil || *u.CostUSD != 0.0031 {
		t.Errorf("usage = %+v", u)
	}
}

func TestRespondAssemblesItemsWhenCompletedCarriesNone(t *testing.T) {
	bare := `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":""}`
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		streamOK(w, sse(
			ev("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\":\"b\"}"}`),
			ev("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":`+bare+`}`),
			ev("response.completed", `{"type":"response.completed","response":{"id":"r","output":[]}}`),
		))
	})
	resp, err := client.Respond(context.Background(), Request{Model: "m"}, StreamHandler{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("output = %s", resp.Output)
	}
	var call struct {
		Arguments string `json:"arguments"`
	}
	if err := json.Unmarshal(resp.Output[0], &call); err != nil || call.Arguments != `{"path":"b"}` {
		t.Fatalf("call = %s, %v", resp.Output[0], err)
	}
}

func TestRespondResponseFailed(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		streamOK(w, sse(
			ev("response.created", `{"type":"response.created","response":{"id":"r"}}`),
			ev("response.failed", `{"type":"response.failed","response":{"id":"r","error":{"code":"server_error","message":"the upstream broke"}}}`),
		))
	})
	_, err := client.Respond(context.Background(), Request{Model: "m"}, StreamHandler{})
	var failed *FailedError
	if !errors.As(err, &failed) || failed.Code != "server_error" || !strings.Contains(err.Error(), "the upstream broke") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := Retryable(err); !ok {
		t.Error("a server_error failure is retryable")
	}
}

func TestRespondFailedWithBadRequestIsNotRetryable(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		streamOK(w, sse(ev("response.failed", `{"type":"response.failed","response":{"error":{"code":"invalid_prompt","message":"bad"}}}`)))
	})
	_, err := client.Respond(context.Background(), Request{Model: "m"}, StreamHandler{})
	if _, ok := Retryable(err); ok {
		t.Fatalf("invalid_prompt retried: %v", err)
	}
}

func TestRespond429CarriesRetryAfter(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"slow down","code":429}}`)
	})
	_, err := client.Respond(context.Background(), Request{Model: "m"}, StreamHandler{})
	var h *HTTPError
	if !errors.As(err, &h) || h.Status != 429 || h.RetryAfter != 7*time.Second {
		t.Fatalf("err = %#v", err)
	}
	delay, ok := Retryable(err)
	if !ok || delay != 7*time.Second {
		t.Errorf("retryable = %v, %v", delay, ok)
	}
	if !strings.Contains(err.Error(), "slow down") {
		t.Errorf("error text = %q", err.Error())
	}
}

func TestRespondAuthFailureIsNotRetryableAndNamesNoToken(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"no auth","code":401}}`)
	})
	_, err := client.Respond(context.Background(), Request{Model: "m"}, StreamHandler{})
	if _, ok := Retryable(err); ok {
		t.Fatal("a 401 is not retryable")
	}
	if strings.Contains(err.Error(), "sk-test-secret") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestRespondCutStream(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		streamOK(w, sse(
			ev("response.created", `{"type":"response.created","response":{"id":"r"}}`),
			ev("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":`+callItem+`}`),
		))
	})
	_, err := client.Respond(context.Background(), Request{Model: "m"}, StreamHandler{})
	if !errors.Is(err, ErrStreamCut) {
		t.Fatalf("err = %v", err)
	}
	if _, ok := Retryable(err); !ok {
		t.Error("a cut stream is retryable")
	}
}

func TestRespondStalledStreamIsCut(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ": keepalive\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	client.StallTimeout = 100 * time.Millisecond
	_, err := client.Respond(context.Background(), Request{Model: "m"}, StreamHandler{})
	if !errors.Is(err, ErrStreamCut) {
		t.Fatalf("err = %v", err)
	}
}

func TestRespondNoCredential(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", Token: func(context.Context) (string, error) { return "", nil }}
	if _, err := c.Respond(context.Background(), Request{Model: "m"}, StreamHandler{}); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	if d := parseRetryAfter("30", now); d != 30*time.Second {
		t.Errorf("seconds = %v", d)
	}
	if d := parseRetryAfter(now.Add(90*time.Second).Format(http.TimeFormat), now); d < 89*time.Second || d > 90*time.Second {
		t.Errorf("date = %v", d)
	}
	if d := parseRetryAfter("soon", now); d != 0 {
		t.Errorf("junk = %v", d)
	}
}

// Provider error text is masked before it enters an error: a key echoed in an
// error body, a bearer credential, and the very token the request carried.
func TestProviderErrorTextIsScrubbed(t *testing.T) {
	key := "sk-or-v1-0123456789abcdef0123456789abcdef"
	for name, handler := range map[string]http.HandlerFunc{
		"json body": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"message":"invalid key `+key+` and Bearer abcdef123456 sent as sk-test-secret","code":401}}`)
		},
		"plain body": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, strings.Repeat("x", 280)+" Authorization: Bearer "+key)
		},
		"response.failed": func(w http.ResponseWriter, r *http.Request) {
			streamOK(w, sse(ev("response.failed", `{"type":"response.failed","response":{"error":{"code":"server_error","message":"upstream saw `+key+` / sk-test-secret"}}}`)))
		},
		"error event": func(w http.ResponseWriter, r *http.Request) {
			streamOK(w, sse(ev("error", `{"type":"error","code":"x","message":"bad Bearer abcdef123456"}`)))
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := serve(t, handler)
			_, err := client.Respond(context.Background(), Request{Model: "m"}, StreamHandler{})
			if err == nil {
				t.Fatal("no error")
			}
			text := err.Error()
			for _, leak := range []string{key, "0123456789abcdef", "abcdef123456", "sk-test-secret"} {
				if strings.Contains(text, leak) {
					t.Errorf("error text keeps %q: %s", leak, text)
				}
			}
		})
	}
}

// A server that accepts the connection and never sends headers is cut by the
// header watchdog, and the cut is retryable under the loop's policy.
func TestRespondHeaderStallIsCutAndRetryable(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		// The body is read first: the server notices a closed connection
		// only once it has, and Close would otherwise wait on this handler.
		io.Copy(io.Discard, r.Body)
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	client.HeaderTimeout = 100 * time.Millisecond
	start := time.Now()
	_, err := client.Respond(context.Background(), Request{Model: "m"}, StreamHandler{})
	if !errors.Is(err, ErrStreamCut) || !strings.Contains(err.Error(), "no response headers") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("the cut took %v", time.Since(start))
	}
	if _, ok := Retryable(err); !ok {
		t.Error("a header stall is retryable")
	}
}

// The caller's own cancel during the header wait is the context's error, not
// a stall.
func TestRespondCancelDuringHeaderWait(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		// The body is read first: the server notices a closed connection
		// only once it has, and Close would otherwise wait on this handler.
		io.Copy(io.Discard, r.Body)
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := client.Respond(ctx, Request{Model: "m"}, StreamHandler{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}
