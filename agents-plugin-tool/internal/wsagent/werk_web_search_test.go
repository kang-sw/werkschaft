package wsagent

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wslive"
)

func TestWerkLoopSearchUsageCostReachesLiveStatus(t *testing.T) {
	dir := t.TempDir()
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string
			ID     json.RawMessage
		}
		_ = json.NewDecoder(r.Body).Decode(&call)
		switch call.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": map[string]any{}})
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": map[string]any{"tools": []any{}}})
		}
	}))
	defer mcpSrv.Close()

	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"output\":[{\"type\":\"message\",\"id\":\"m1\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"searched\"}]}],\"usage\":{\"input_tokens\":100,\"output_tokens\":40,\"server_tool_use\":{\"web_search_requests\":1},\"cost\":0.0175}}}\n\n")
	}))
	defer modelSrv.Close()

	client := &werk.Client{
		BaseURL:  modelSrv.URL,
		Provider: wscred.ProviderOpenRouter,
		Token:    func(context.Context) (string, error) { return "tok", nil },
	}
	spec := StartSpec{
		Dir:             dir,
		Root:            dir,
		Model:           "openai/gpt-5",
		Provider:        string(wscred.ProviderOpenRouter),
		PermissionLevel: PermissionReadOnly,
		MCPSession:      &MCPSession{URL: mcpSrv.URL, Token: "fixture"},
		sessionConfig: func() (wsconfig.Options, wsconfig.SessionReader, string) {
			return wsconfig.Options{CacheHome: dir, ConfigHome: dir, RepoRoot: dir}, nil, ""
		},
	}
	loop, err := startWerkLoop(context.Background(), spec, client)
	if err != nil {
		t.Fatal(err)
	}
	defer loop.Close()

	_ = nextWerkLoopEvent(t, loop, func(e Event) bool { return e.SessionID != "" })
	if err := loop.Send("find latest docs"); err != nil {
		t.Fatal(err)
	}
	statusEv := nextWerkLoopEvent(t, loop, func(e Event) bool {
		return e.Live != nil && e.Live.Kind == wslive.KindStatus && e.Live.Cost != nil
	})
	if math.Abs(statusEv.Live.Cost.USD-0.0175) > 1e-9 {
		t.Fatalf("live status cost = %v, want 0.0175", statusEv.Live.Cost.USD)
	}
	_ = nextWerkLoopEvent(t, loop, func(e Event) bool { return e.TurnEnd })
}

func TestConvResolveWerkWebSearchLayeredAndFixedProfile(t *testing.T) {
	cfg := wsconfig.Options{CacheHome: t.TempDir(), ConfigHome: t.TempDir()}
	repoRoot := t.TempDir()
	r := wsconfig.NewResolver(wsconfig.Options{CacheHome: cfg.CacheHome, ConfigHome: cfg.ConfigHome, RepoRoot: repoRoot}, wsconfig.WerkWebSearchDefaults(), nil, nil)
	if err := r.Set(wsconfig.ItemWerkWebSearchMaxUses, "3", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(wsconfig.ItemWerkWebSearchEngine, "parallel", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeRepo}); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(wsconfig.ItemWerkWebSearchSIWC, "true", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeProject}); err != nil {
		t.Fatal(err)
	}

	c := &conv{
		r:   &Runner{opts: Options{Config: cfg}},
		rec: Record{Root: repoRoot},
	}
	provider := c.sessionConfigProvider()
	if provider == nil {
		t.Fatal("expected sessionConfigProvider for normal conversation")
	}
	got := resolveWerkWebSearchPolicy(provider)()
	if !got.Enabled || got.MaxUses != 3 || got.MaxResults != 5 || got.Engine != "parallel" || !got.SIWC {
		t.Fatalf("resolveWerkWebSearchPolicy = %+v", got)
	}

	// Private fixed-profile runner (search-answer agent) disables web search
	// end-to-end on the outbound OpenRouter request payload.
	c.r.fixedProfile = &ResolvedProfile{Name: "search-answer"}
	if c.sessionConfigProvider() != nil {
		t.Fatal("fixedProfile runner must return nil sessionConfigProvider so web search stays disabled")
	}

	dir := t.TempDir()
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string
			ID     json.RawMessage
		}
		_ = json.NewDecoder(r.Body).Decode(&call)
		switch call.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": map[string]any{}})
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": map[string]any{"tools": []any{}}})
		}
	}))
	defer mcpSrv.Close()

	var captured []byte
	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r-fixed\",\"output\":[{\"type\":\"message\",\"id\":\"m1\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"local answer\"}]}],\"usage\":{\"input_tokens\":20,\"output_tokens\":5}}}\n\n")
	}))
	defer modelSrv.Close()

	client := &werk.Client{
		BaseURL:  modelSrv.URL,
		Provider: wscred.ProviderOpenRouter,
		Token:    func(context.Context) (string, error) { return "tok", nil },
	}
	spec := StartSpec{
		Dir:             dir,
		Root:            dir,
		Model:           "openai/gpt-5",
		Provider:        string(wscred.ProviderOpenRouter),
		PermissionLevel: PermissionReadOnly,
		MCPSession:      &MCPSession{URL: mcpSrv.URL, Token: "fixture"},
		sessionConfig:   c.sessionConfigProvider(), // nil for fixedProfile
	}
	loop, err := startWerkLoop(context.Background(), spec, client)
	if err != nil {
		t.Fatal(err)
	}
	defer loop.Close()

	_ = nextWerkLoopEvent(t, loop, func(e Event) bool { return e.SessionID != "" })
	if err := loop.Send("answer locally"); err != nil {
		t.Fatal(err)
	}
	_ = nextWerkLoopEvent(t, loop, func(e Event) bool { return e.TurnEnd })

	var reqBody struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(captured, &reqBody); err != nil {
		t.Fatalf("unmarshal fixed-profile request: %v\n%s", err, captured)
	}
	for _, tool := range reqBody.Tools {
		if typ, _ := tool["type"].(string); typ == "openrouter:web_search" || typ == "web_search" {
			t.Fatalf("fixed-profile runner must not attach server-side web search, got %#v", tool)
		}
	}
}
