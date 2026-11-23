package werk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalResponsesFixtures(t *testing.T) {
	for _, dialect := range []string{"vllm", "ollama"} {
		for _, counts := range []string{"absent", "zero", "positive"} {
			for _, credential := range []string{"nil", "empty", "key"} {
				t.Run(dialect+"/"+counts+"/"+credential, func(t *testing.T) {
					usage := "{}"
					if counts != "absent" {
						n := 0
						if counts == "positive" {
							n = 7
						}
						usage = fmt.Sprintf("{%q:20,%q:10,%q:{%q:%d},%q:{%q:%d},%q:%d}", "input_tokens", "output_tokens", "input_tokens_details", "cached_tokens", n, "output_tokens_details", "reasoning_tokens", n, "cache_write_tokens", n)
					}
					// vLLM includes cache writes in input token details; retain
					// the top-level extension too for compatible servers.
					if dialect == "vllm" && counts != "absent" {
						n := 0
						if counts == "positive" {
							n = 7
						}
						usage = fmt.Sprintf(`{"input_tokens":20,"output_tokens":10,"input_tokens_details":{"cached_tokens":%d,"cache_write_tokens":%d},"output_tokens_details":{"reasoning_tokens":%d}}`, n, n, n)
					}
					var replay []json.RawMessage
					requests := 0
					client := serve(t, func(w http.ResponseWriter, r *http.Request) {
						requests++
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if _, ok := body["tool_choice"]; ok {
							t.Error("tool_choice sent")
						}
						if r.URL.Path != "/responses" {
							t.Error(r.URL.Path)
						}
						wantAuth := ""
						if credential == "key" {
							wantAuth = "Bearer local-key"
						}
						if r.Header.Get("Authorization") != wantAuth {
							t.Error("unexpected authorization")
						}
						if requests == 2 {
							var input []json.RawMessage
							json.Unmarshal(body["input"], &input)
							if len(input) != 2 || string(input[0]) != reasoningItem || string(input[1]) != callItem {
								t.Errorf("replay changed: %s", input)
							}
						}
						streamOK(w, sse(
							ev("response.reasoning.delta", `{"type":"response.reasoning.delta","delta":"PRIVATE"}`),
							ev("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","delta":"summary"}`),
							ev("response.completed", `{"type":"response.completed","response":{"id":"local","output":[`+reasoningItem+`,`+callItem+`],"usage":`+usage+`}}`),
						))
					})
					client.Dialect = dialect
					client.Token = nil
					if credential != "nil" {
						client.Token = func(context.Context) (string, error) {
							if credential == "key" {
								return "local-key", nil
							}
							return "", nil
						}
					}
					var summary strings.Builder
					response, err := client.Respond(context.Background(), Request{Model: "local", Tools: []ToolDef{{Name: "read"}}}, StreamHandler{SummaryDelta: func(_, d string) { summary.WriteString(d) }})
					if err != nil {
						t.Fatal(err)
					}
					if summary.String() != "summary" {
						t.Fatalf("summary = %q", summary.String())
					}
					u := response.Usage
					want := int64(0)
					if counts == "positive" {
						want = 7
					}
					if u.ReasoningTokens != want || u.CachedTokens != want {
						t.Fatalf("usage = %+v", u)
					}
					if counts == "absent" {
						if u.CacheWriteTokens != nil {
							t.Fatal("invented cache writes")
						}
					} else if u.CacheWriteTokens == nil || *u.CacheWriteTokens != want {
						t.Fatalf("cache writes = %v", u.CacheWriteTokens)
					}
					// Existing durable counters omit zero, including an explicitly reported zero.
					raw, _ := json.Marshal(Line{T: LineUsage, ReasoningTokens: u.ReasoningTokens, CachedTokens: u.CachedTokens, CacheWriteTokens: u.CacheWriteTokens})
					var line map[string]any
					json.Unmarshal(raw, &line)
					if counts != "positive" {
						if _, ok := line["reasoning_tokens"]; ok {
							t.Fatal("zero reasoning shown")
						}
						if _, ok := line["cached_tokens"]; ok {
							t.Fatal("zero cache read shown")
						}
					}
					if _, ok := line["cache_write_tokens"]; ok != (counts != "absent") {
						t.Fatalf("cache write presence: %s", raw)
					}
					replay = response.Output
					_, err = client.Respond(context.Background(), Request{Model: "local", Input: replay}, StreamHandler{})
					if err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestResponsesRefuseRedirects(t *testing.T) {
	for _, dialect := range []string{"vllm", "ollama"} {
		for _, status := range []int{301, 302, 303, 307, 308} {
			t.Run(fmt.Sprintf("%s/%d", dialect, status), func(t *testing.T) {
				followed := false
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true }))
				defer target.Close()
				client := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, status) })
				client.Dialect = dialect
				custom := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { followed = true; return nil }}
				client.HTTP = custom
				_, err := client.Respond(context.Background(), Request{Model: "m"}, StreamHandler{})
				var he *HTTPError
				if !errors.As(err, &he) || he.Status != status || followed {
					t.Fatalf("redirect: followed=%v err=%v", followed, err)
				}
				if custom.CheckRedirect == nil {
					t.Fatal("caller client changed")
				}
			})
		}
	}
}

func TestHostedResponsesStillRequireCredential(t *testing.T) {
	for _, dialect := range []string{"", "unknown"} {
		c := &Client{Dialect: dialect}
		_, err := c.Respond(context.Background(), Request{}, StreamHandler{})
		if !errors.Is(err, ErrNoCredential) {
			t.Fatalf("dialect %q: %v", dialect, err)
		}
	}
}

func TestLocalResponsesTokenFailure(t *testing.T) {
	sentinel := errors.New("credential lookup failed")
	for _, dialect := range []string{"ollama", "vllm"} {
		c := &Client{Dialect: dialect, Token: func(context.Context) (string, error) { return "", sentinel }}
		_, err := c.Respond(context.Background(), Request{}, StreamHandler{})
		if !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
	}
}
