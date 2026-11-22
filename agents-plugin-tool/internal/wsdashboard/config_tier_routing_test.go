package wsdashboard

import (
	"encoding/json"
	"errors"
	"net/http"
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
