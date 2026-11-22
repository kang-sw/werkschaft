package wsdashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
)

// The graph editor reads the stored graph and previews drafts through the
// daemon's TierRouting hook, and a refused save carries the graph's reason.
func TestSettingsTierRoutingRoutes(t *testing.T) {
	catalog, err := wsconfigservice.NewCatalog([]wsconfigservice.Rule{
		{Key: wsconfig.ItemAgentTierRouting, Type: "object", Structured: true, GlobalOnly: true,
			NormalizeObject: func(any, wsconfig.Scope) (json.RawMessage, error) {
				return nil, errors.New("agent.tier_routing: pointer cycle a -> b -> a")
			}},
		{Key: "agent.profile.*", Structured: true, ValidateName: func(string) error { return nil },
			NormalizeObject: func(any, wsconfig.Scope) (json.RawMessage, error) { return nil, errors.New("hidden reason") }},
	})
	if err != nil {
		t.Fatal(err)
	}
	type call struct {
		draft   string
		harness string
	}
	var calls []call
	backend := &ConfigBackend{Catalog: catalog, Options: wsconfig.Options{
		CacheHome: filepath.Join(t.TempDir(), "cache"), ConfigHome: filepath.Join(t.TempDir(), "global"),
	}, TierRouting: func(draft json.RawMessage, harness string) (any, error) {
		calls = append(calls, call{string(draft), harness})
		return map[string]any{"harness": harness, "draft": draft != nil}, nil
	}}
	d := authFixture(t)
	d.config = backend
	own := "http://127.0.0.1:43210"

	got := settingsRequest(t, d, http.MethodGet, "/api/config/tier-routing?harness=codex", "", "")
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"harness":"codex"`) || len(calls) != 1 || calls[0] != (call{"", "codex"}) {
		t.Fatalf("GET = %d %s, calls %+v", got.Code, got.Body, calls)
	}
	preview := `{"harness":"pi","value":{"nodes":{"mine":{}}}}`
	for _, origin := range []string{"", "http://evil.example"} {
		if got := settingsRequest(t, d, http.MethodPost, "/api/config/tier-routing/preview", preview, origin); got.Code != 403 {
			t.Errorf("preview from origin %q = %d %s", origin, got.Code, got.Body)
		}
	}
	got = settingsRequest(t, d, http.MethodPost, "/api/config/tier-routing/preview", preview, own)
	if got.Code != 200 || len(calls) != 2 || calls[1] != (call{`{"nodes":{"mine":{}}}`, "pi"}) {
		t.Fatalf("preview = %d %s, calls %+v", got.Code, got.Body, calls)
	}
	for _, body := range []string{`{"harness":"pi"}`, `{"value":{},"extra":1}`} {
		if got := settingsRequest(t, d, http.MethodPost, "/api/config/tier-routing/preview", body, own); got.Code != 400 {
			t.Errorf("preview %s = %d %s", body, got.Code, got.Body)
		}
	}
	if got := settingsRequest(t, d, http.MethodPost, "/api/config/tier-routing", preview, own); got.Code != 405 {
		t.Errorf("POST to the read route = %d", got.Code)
	}

	// A refused graph save names the reason; other structured keys keep the
	// generic message.
	got = settingsRequest(t, d, http.MethodPost, "/api/config/set", `{"scope":"global","key":"agent.tier_routing","value":{"nodes":{}}}`, own)
	if got.Code != 400 || !strings.Contains(got.Body.String(), "pointer cycle a -\\u003e b -\\u003e a") {
		t.Fatalf("refused graph save = %d %s", got.Code, got.Body)
	}
	got = settingsRequest(t, d, http.MethodPost, "/api/config/set", `{"scope":"global","key":"agent.profile.x","value":{}}`, own)
	if got.Code != 400 || strings.Contains(got.Body.String(), "hidden reason") {
		t.Fatalf("refused profile save = %d %s", got.Code, got.Body)
	}

	backend.TierRouting = nil
	if got := settingsRequest(t, d, http.MethodGet, "/api/config/tier-routing", "", ""); got.Code != 404 || !strings.Contains(got.Body.String(), "upgrade_required") {
		t.Fatalf("no hook = %d %s", got.Code, got.Body)
	}
}

// A graph save names the revision of the copy it edited; the daemon refuses
// a missing or stale revision before writing, so a save built from an old
// copy cannot replace a graph another tab saved in between.
func TestSettingsTierRoutingSaveRefusesAStaleRevision(t *testing.T) {
	catalog, err := wsconfigservice.NewCatalog([]wsconfigservice.Rule{
		{Key: wsconfig.ItemAgentTierRouting, Type: "object", Structured: true, GlobalOnly: true, Editable: true,
			NormalizeObject: func(value any, _ wsconfig.Scope) (json.RawMessage, error) { return json.Marshal(value) }},
	})
	if err != nil {
		t.Fatal(err)
	}
	opts := wsconfig.Options{CacheHome: filepath.Join(t.TempDir(), "cache"), ConfigHome: filepath.Join(t.TempDir(), "global")}
	d := authFixture(t)
	d.config = &ConfigBackend{Catalog: catalog, Options: opts}
	own := "http://127.0.0.1:43210"
	stored := func() (wsconfig.TierRouting, string) {
		t.Helper()
		routing, revision, err := wsconfig.LoadTierRoutingRevision(opts)
		if err != nil {
			t.Fatal(err)
		}
		return routing, revision
	}
	save := func(action, value string, revision *string) *httptest.ResponseRecorder {
		body := `{"scope":"global","key":"agent.tier_routing"`
		if value != "" {
			body += `,"value":` + value
		}
		if revision != nil {
			body += `,"revision":"` + *revision + `"`
		}
		return settingsRequest(t, d, http.MethodPost, "/api/config/"+action, body+"}", own)
	}
	tabA := `{"bundles":{"a":{"harness":"codex","model":"m-a"}}}`
	tabB := `{"bundles":{"a":{"harness":"codex","model":"m-a"},"b":{"harness":"claude","model":"m-b"}}}`

	_, opened := stored()
	if got := save("set", tabA, nil); got.Code != 400 || !strings.Contains(got.Body.String(), "needs the revision") {
		t.Fatalf("save without a revision = %d %s", got.Code, got.Body)
	}
	if routing, _ := stored(); len(routing.Bundles) != 0 {
		t.Fatalf("a refused save wrote %+v", routing)
	}
	if got := save("set", tabA, &opened); got.Code != 200 {
		t.Fatalf("save at the opened revision = %d %s", got.Code, got.Body)
	}
	_, afterA := stored()
	if afterA == opened {
		t.Fatal("the revision did not move with the stored graph")
	}
	// Tab B saved from the current copy; tab A then confirms a delete built
	// from its older copy and is refused.
	if got := save("set", tabB, &afterA); got.Code != 200 {
		t.Fatalf("tab B save = %d %s", got.Code, got.Body)
	}
	routing, afterB := stored()
	if got := save("set", `{"bundles":{"b":{"harness":"claude","model":"m-b"}}}`, &afterA); got.Code != 409 || !strings.Contains(got.Body.String(), "stale_revision") {
		t.Fatalf("stale save = %d %s", got.Code, got.Body)
	}
	if got := save("reset", "", &afterA); got.Code != 409 || !strings.Contains(got.Body.String(), "stale_revision") {
		t.Fatalf("stale reset = %d %s", got.Code, got.Body)
	}
	if now, revision := stored(); revision != afterB || len(now.Bundles) != 2 || now.Bundles["b"] != routing.Bundles["b"] {
		t.Fatalf("a stale save changed the graph: %+v (%s, want %s)", now, revision, afterB)
	}
	if got := save("reset", "", &afterB); got.Code != 200 {
		t.Fatalf("reset at the current revision = %d %s", got.Code, got.Body)
	}
	if _, revision := stored(); revision != opened {
		t.Fatalf("reset revision = %s, want the empty store's %s", revision, opened)
	}
}

// A config.tune-style write through the shared structured writer that lands
// after the dashboard save read the layers and before it writes still
// refuses the stale save: the revision is compared under the config file
// lock, against the file read there.
func TestSettingsTierRoutingSaveRefusesAWriteThatLandsMidSave(t *testing.T) {
	catalog, err := wsconfigservice.NewCatalog([]wsconfigservice.Rule{
		{Key: wsconfig.ItemAgentTierRouting, Type: "object", Structured: true, GlobalOnly: true, Editable: true,
			NormalizeObject: func(value any, _ wsconfig.Scope) (json.RawMessage, error) { return json.Marshal(value) }},
	})
	if err != nil {
		t.Fatal(err)
	}
	opts := wsconfig.Options{CacheHome: filepath.Join(t.TempDir(), "cache"), ConfigHome: filepath.Join(t.TempDir(), "global")}
	d := authFixture(t)
	d.config = &ConfigBackend{Catalog: catalog, Options: opts}
	own := "http://127.0.0.1:43210"
	tuned := json.RawMessage(`{"bundles":{"tuned":{"harness":"claude","model":"m-t"}}}`)
	for _, tc := range []struct{ action, value string }{
		{"set", `,"value":{"bundles":{"mine":{"harness":"codex","model":"m-a"}}}`},
		{"reset", ""},
	} {
		resolver := wsconfig.NewResolver(opts, nil, nil, nil)
		if err := resolver.UnsetStructured(wsconfig.ItemAgentTierRouting, wsconfig.ScopeGlobal); err != nil {
			t.Fatal(err)
		}
		_, opened, err := wsconfig.LoadTierRoutingRevision(opts)
		if err != nil {
			t.Fatal(err)
		}
		writes := 0
		d.beforeConfigWrite = func(key string) {
			if key != wsconfig.ItemAgentTierRouting {
				t.Errorf("hook for %s", key)
			}
			writes++
			// The same writer config.tune uses (mcp/config_structured.go).
			if err := resolver.SetStructured(wsconfig.ItemAgentTierRouting, tuned, wsconfig.ScopeGlobal); err != nil {
				t.Fatal(err)
			}
		}
		body := `{"scope":"global","key":"agent.tier_routing"` + tc.value + `,"revision":"` + opened + `"}`
		got := settingsRequest(t, d, http.MethodPost, "/api/config/"+tc.action, body, own)
		if writes != 1 || got.Code != 409 || !strings.Contains(got.Body.String(), "stale_revision") {
			t.Fatalf("%s overtaken mid-save = %d %s (hook ran %d times)", tc.action, got.Code, got.Body, writes)
		}
		routing, _, err := wsconfig.LoadTierRoutingRevision(opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(routing.Bundles) != 1 || routing.Bundles["tuned"].Model != "m-t" {
			t.Fatalf("%s: the stale save replaced the tuned graph: %+v", tc.action, routing)
		}
	}
}
