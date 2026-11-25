package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func TestWerkEndpointsDiscoveryKeepsLegacyDirectCalls(t *testing.T) {
	s, _, lead := manifestServer(t)
	t.Cleanup(s.Close)
	const item = wsconfig.ItemWerkEndpoints
	const value = `{"fixture":{"base_url":"https://endpoint.example/v1","kind":"ollama"}}`

	list := func(id int) {
		t.Helper()
		for i, args := range []map[string]any{nil, {"format": "json"}} {
			resp := keyedCall(t, s, id+i, lead, "config.list", args)
			if toolIsError(t, resp) {
				t.Fatal(toolText(t, resp))
			}
			if out := toolText(t, resp); strings.Contains(out, item) || strings.Contains(out, "endpoint.example") {
				t.Fatalf("config.list exposed the concealed setting or value: %s", out)
			}
			if i == 1 {
				var view configListView
				if err := json.Unmarshal([]byte(toolText(t, resp)), &view); err != nil {
					t.Fatal(err)
				}
				for _, knob := range view.Knobs {
					if knob.ID == "agents.tier" {
						for _, field := range knob.ValueFields {
							if field.Name == "provider" && strings.Contains(field.Description, "Settings → Account") {
								return
							}
						}
					}
				}
				t.Fatal("lead-tune catalog is missing the tier provider's Account pointer")
			}
		}
	}
	list(931100)
	changed := keyedCall(t, s, 931102, lead, "config.tune", map[string]any{"key": item, "value": value})
	if toolIsError(t, changed) {
		t.Fatalf("legacy set: %s", toolText(t, changed))
	}
	get := configGetJSON(t, s, 931103, map[string]any{"session_key": lead, "key": item})
	if get.Value != value || get.Scope != "global" {
		t.Fatalf("legacy get after set: %+v", get)
	}
	list(931104)

	catalog, err := DashboardConfigCatalog("ws")
	if err != nil {
		t.Fatal(err)
	}
	layers, err := wsconfig.FileLayers(s.configOptions())
	if err != nil {
		t.Fatal(err)
	}
	entry := catalog.Entry(item, nil, wsconfig.ScopeGlobal, wsconfigservice.Layers{Files: layers})
	if entry.Value != value || entry.SourceScope != wsconfig.ScopeGlobal {
		t.Fatalf("Account projection after set: %+v", entry)
	}

	reset := keyedCall(t, s, 931106, lead, "config.tune", map[string]any{"key": item, "reset": true})
	if toolIsError(t, reset) {
		t.Fatalf("legacy reset: %s", toolText(t, reset))
	}
	overrides, err := wsconfig.GlobalOverrides(s.configOptions())
	if val, present := overrides[item]; err != nil || present {
		t.Fatalf("legacy reset left an override %q: %v", val, err)
	}
	list(931107)
}

func TestWerkEndpointsLegacyTuneUsesCredentialWriter(t *testing.T) {
	s, _, lead := manifestServer(t)
	t.Cleanup(s.Close)
	opts := s.configOptions()
	dir, err := wsdashboard.Dir(opts.CacheHome)
	if err != nil {
		t.Fatal(err)
	}
	credPath, err := wscred.DefaultPath(opts.CacheHome)
	if err != nil {
		t.Fatal(err)
	}
	store := wscred.NewStore(credPath)
	const original = "https://endpoint.example/v1"
	if _, err := store.ApplyLocalEndpoint(opts, wscred.LocalEndpointChange{
		Op: wscred.OpSet, Name: "fixture", BaseURL: original, Kind: "ollama",
		Key: wscred.NewSecret("fake-local-key"),
	}); err != nil {
		t.Fatal(err)
	}
	d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir: dir, Home: filepath.Join(t.TempDir(), "home"), Credentials: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	const item = wsconfig.ItemWerkEndpoints
	const value = `{"fixture":{"base_url":"https://other.example/v1","kind":"ollama"}}`
	for i, args := range []map[string]any{{"key": item, "value": value}, {"key": item, "reset": true}} {
		resp := keyedCall(t, s, 931200+i, lead, "config.tune", args)
		if !toolIsError(t, resp) || !strings.Contains(toolText(t, resp), "was not applied") || !strings.Contains(toolText(t, resp), "launcher") {
			t.Fatalf("bare endpoint writer response: %s", toolText(t, resp))
		}
	}
	req := wscred.Request{Provider: "local/fixture", BaseURL: original}
	if key, err := store.Credential(context.Background(), req); err != nil || key.Secret.Reveal() != "fake-local-key" {
		t.Fatalf("bare endpoint write changed the active key: %v", err)
	}
	resp := keyedCall(t, s, 931203, lead, "config.tune", map[string]any{"key": "workflow.lang", "value": "en", "scope": "global"})
	if toolIsError(t, resp) {
		t.Fatalf("bare unrelated scalar write: %s", toolText(t, resp))
	}
	if value, ok := wsconfig.GlobalOverride(opts, "workflow.lang"); !ok || value != "en" {
		t.Fatalf("bare unrelated scalar write was not persisted: %q, %v", value, ok)
	}
	s.live.host = d
	resp = keyedCall(t, s, 931204, lead, "config.tune", map[string]any{"key": item, "value": value})
	if toolIsError(t, resp) {
		t.Fatalf("hosted endpoint write: %s", toolText(t, resp))
	}
	resp = keyedCall(t, s, 931205, lead, "config.tune", map[string]any{
		"key": item, "value": `{"fixture":{"base_url":"https://endpoint.example/v1","kind":"ollama"}}`,
	})
	if toolIsError(t, resp) {
		t.Fatalf("hosted endpoint restore: %s", toolText(t, resp))
	}
	if _, err := store.Credential(context.Background(), req); !errors.Is(err, wscred.ErrNotConfigured) {
		t.Fatalf("hosted legacy restore revived retired credential: %v", err)
	}
}
