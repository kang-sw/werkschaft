package wsagent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

func TestRetiredPiTierRedirectsToWerk(t *testing.T) {
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"id":"vendor/exact"},{"id":"vendor/bundle"},{"id":"vendor/lowercase"}]}`)
	}))
	defer models.Close()
	configured := true
	_, werkReg := (WerkBackend{
		modelsURL: models.URL, httpClient: models.Client(),
		credentialStatus: func(string) (bool, error) { return configured, nil },
	}).registration()
	cfg := wsconfig.Options{ConfigHome: t.TempDir()}
	for _, slot := range []struct{ size, harness, model string }{
		{"small", "pi", "vendor/exact"},
		{"medium", "pi", "vendor/lowerCASE"},
		{"medium", "default", "default-model"},
		{"large", "pi", "vendor/exact"},
		{"large", "werk", "vendor/explicit"},
	} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(cfg, slot.size, "", slot.model, slot.harness, "low"); err != nil {
			t.Fatal(err)
		}
	}
	setTierRouting(t, cfg, `{"nodes":{"legacy-route":{"*":"pi.*"}},"bundles":{"legacy-bundle":{"harness":"pi","model":"vendor/bundle","effort":"high"}}}`)
	setTierAliases(t, cfg, "legacy-alias=pi.small")
	r := NewRunner(Options{Backends: map[string]BackendRegistration{BackendWerk: werkReg}})
	t.Cleanup(r.Close)
	caller := LeadCaller("lead", t.TempDir(), BackendClaude, cfg)

	for _, tc := range []struct{ token, model, rank string }{
		{"pi.small", "vendor/exact", "small"},
		{"werk.small", "vendor/exact", "small"},
		{"legacy-alias", "vendor/exact", "small"},
		{"legacy-route:small", "vendor/exact", "small"},
		{"legacy-bundle", "vendor/bundle", "xlarge"},
		{"pi.large", "vendor/explicit", "large"},
	} {
		harness, rank, binding, err := r.resolveTier(caller, tc.token)
		if err != nil || harness != BackendWerk || rank != tc.rank || binding.Model != tc.model {
			t.Errorf("%s resolved to %s.%s %+v, %v", tc.token, harness, rank, binding, err)
		}
	}
	if _, _, _, err := r.resolveTier(caller, "pi.medium"); err == nil || !strings.Contains(err.Error(), "werk.medium has no usable OpenRouter model") {
		t.Fatalf("inexact model should leave the slot unset: %v", err)
	}
	tiers, err := r.AgentTiers(caller)
	if err != nil || tiers.Routing.Error != "" || len(tiers.Routing.Bundles) != 1 || tiers.Routing.Bundles[0].Harness != BackendWerk || tiers.Routing.Bundles[0].Model != "vendor/bundle" {
		t.Fatalf("stored Pi bundle did not resolve: %+v, %v", tiers, err)
	}
	legacy, err := wsconfig.HarnessTiers(cfg, BackendPi)
	if err != nil || legacy["small"].Model != "vendor/exact" {
		t.Fatalf("stored Pi config changed: %+v, %v", legacy, err)
	}
	configured = false
	if _, _, _, err := r.resolveTier(caller, "pi.small"); err == nil || !strings.Contains(err.Error(), werkNoKeyReason) {
		t.Fatalf("no-key refusal = %v", err)
	}
}

func TestRetiredPiProfileRemainsReadable(t *testing.T) {
	cfg := wsconfig.Options{ConfigHome: t.TempDir()}
	resolver := wsconfig.NewResolver(cfg, nil, nil, nil)
	if err := resolver.SetStructured(ProfilePrefix+"old-pi", json.RawMessage(`{"backend":"pi","model":"vendor/model","tools":["read"]}`), wsconfig.ScopeGlobal); err != nil {
		t.Fatal(err)
	}
	backends := BuiltinBackendRegistrations()
	delete(backends, BackendPi)
	profiles, err := ResolveProfiles(&resolver, backends)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, profile := range profiles {
		if profile.Name == "old-pi" {
			found = profile.Definition.Backend == BackendPi && profile.Definition.Model == "vendor/model"
		}
	}
	if !found {
		t.Fatal("old Pi profile was not retained")
	}
	r := NewRunner(Options{Config: cfg, Backends: backends})
	t.Cleanup(r.Close)
	if _, err := r.resolve("old-pi", t.TempDir()); !errors.Is(err, errPiRetired) {
		t.Fatalf("opening an old Pi profile = %v", err)
	}
}
