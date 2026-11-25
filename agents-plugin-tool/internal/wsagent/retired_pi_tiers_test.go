package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
)

type retiredPiAccountCatalog struct {
	testCredentialSource
}

func (retiredPiAccountCatalog) OpenAIModels(context.Context, wscred.Request) ([]wscred.OpenAIModel, error) {
	return []wscred.OpenAIModel{{Slug: "vendor/account-only"}}, nil
}

func TestRetiredPiTierRedirectsToWerk(t *testing.T) {
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"id":"vendor/exact"},{"id":"vendor/bundle"},{"id":"vendor/lowercase"}]}`)
	}))
	defer models.Close()
	routerConfigured, accountConfigured := true, true
	_, werkReg := (WerkBackend{
		modelsURL: models.URL, httpClient: models.Client(),
		credentials: retiredPiAccountCatalog{},
		credentialStatus: func(provider string) (bool, error) {
			if provider == string(wscred.ProviderOpenAI) {
				return accountConfigured, nil
			}
			return routerConfigured, nil
		},
	}).registration()
	cfg := wsconfig.Options{ConfigHome: t.TempDir()}
	for _, slot := range []struct{ size, harness, model string }{
		{"small", "pi", "vendor/exact"},
		{"medium", "pi", "vendor/lowerCASE"},
		{"medium", "default", "default-model"},
		{"large", "pi", "vendor/exact"},
		{"large", "werk", "vendor/explicit"},
		{"xlarge", "pi", "vendor/account-only"},
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

	for _, direct := range []string{"pi.small", "pi.medium", "pi.large", "pi.xlarge"} {
		if _, _, _, err := r.resolveTier(caller, direct); err == nil || !strings.Contains(err.Error(), `harness "pi" is not registered in this daemon`) {
			t.Errorf("direct %s should be refused: %v", direct, err)
		}
	}
	for _, tc := range []struct{ token, model, rank string }{
		{"werk.small", "vendor/exact", "small"},
		{"legacy-alias", "vendor/exact", "small"},
		{"legacy-route:small", "vendor/exact", "small"},
		{"legacy-bundle", "vendor/bundle", "xlarge"},
		{"legacy-route:large", "vendor/explicit", "large"},
	} {
		harness, rank, binding, err := r.resolveTier(caller, tc.token)
		if err != nil || harness != BackendWerk || rank != tc.rank || binding.Model != tc.model {
			t.Errorf("%s resolved to %s.%s %+v, %v", tc.token, harness, rank, binding, err)
		}
	}
	if _, _, _, err := r.resolveTier(caller, "legacy-route:medium"); err == nil || !strings.Contains(err.Error(), "werk.medium has no usable model") {
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
	if _, _, _, err := r.resolveTier(caller, "legacy-route:xlarge"); err == nil || !strings.Contains(err.Error(), "werk.xlarge has no usable model") {
		t.Fatalf("account-only model should leave the legacy slot unset: %v", err)
	}
	routerConfigured = false
	if _, _, _, err := r.resolveTier(caller, "legacy-bundle"); err == nil || !strings.Contains(err.Error(), werkNoKeyReason) {
		t.Fatalf("account sign-in should not enable the legacy OpenRouter bundle: %v", err)
	}
	setTierRouting(t, cfg, `{"bundles":{"account-bundle":{"harness":"pi","model":"vendor/account-only"}}}`)
	if _, _, _, err := r.resolveTier(caller, "account-bundle"); err == nil || !strings.Contains(err.Error(), "exact OpenRouter model ID") {
		t.Fatalf("account-only model should not resolve a legacy bundle: %v", err)
	}
	accountConfigured = false
	if _, _, _, err := r.resolveTier(caller, "legacy-alias"); err == nil || !strings.Contains(err.Error(), werkNoKeyReason) {
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

func TestRetiredPiTierListingSharesOpenRouterCatalogSnapshot(t *testing.T) {
	var requests atomic.Int32
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		io.WriteString(w, `{"data":[{"id":"vendor/a"},{"id":"vendor/b"}]}`)
	}))
	defer models.Close()
	_, reg := (WerkBackend{modelsURL: models.URL, httpClient: models.Client()}).registration()
	cfg := wsconfig.Options{ConfigHome: t.TempDir()}
	if _, err := wsconfig.SetGlobalAgentsTierForHarness(cfg, "small", "", "vendor/a", BackendPi, ""); err != nil {
		t.Fatal(err)
	}
	setTierRouting(t, cfg, `{"nodes":{"legacy-route":{"*":"legacy-one"}},"bundles":{"legacy-one":{"harness":"pi","model":"vendor/a"},"legacy-two":{"harness":"pi","model":"vendor/b"}}}`)
	r := NewRunner(Options{Backends: map[string]BackendRegistration{BackendWerk: reg}})
	t.Cleanup(r.Close)
	caller := LeadCaller("fixture", t.TempDir(), BackendClaude, cfg)
	for want := int32(1); want <= 2; want++ {
		tiers, err := r.AgentTiers(caller)
		if err != nil || len(tiers.Ext) != 1 || tiers.Routing.Error != "" || len(tiers.Routing.Bundles) != 2 {
			t.Fatalf("legacy tier listing = %+v, %v", tiers, err)
		}
		for _, bundle := range tiers.Routing.Bundles {
			if bundle.Model == "" || bundle.Error != "" {
				t.Fatalf("legacy bundle = %+v", bundle)
			}
		}
		if count := requests.Load(); count != want {
			t.Fatalf("after %d listings the catalog was requested %d times", want, count)
		}
	}
}
