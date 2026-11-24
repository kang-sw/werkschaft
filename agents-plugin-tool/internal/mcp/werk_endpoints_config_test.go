package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
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
