package mcp

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
	"github.com/kang-sw/devenv/internal/wspackage"
)

func TestDashboardSettingsUICatalog(t *testing.T) {
	t.Setenv(wspackage.EnvDevCheckout, "")
	raw, err := os.ReadFile("../wsdashboard/assets/dashboard-settings-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var ui []struct {
		Key, Section, Control, Label, Description, Surface string
		Selectors                                          []string
	}
	if err := json.Unmarshal(raw, &ui); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(ui))
	for _, row := range ui {
		keys = append(keys, row.Key)
		if row.Surface != "" && !validCatalogSurface(row.Surface) {
			t.Errorf("%s: surface %q is not one of %v", row.Key, row.Surface, catalogSurfaces)
		}
		if row.Label == row.Description || !strings.HasPrefix(row.Label, "js.settings.key.") ||
			!strings.HasPrefix(row.Description, "js.settings.key.") {
			t.Errorf("%s: label and description require distinct settings translation IDs", row.Key)
		}
	}
	for _, name := range []string{"ws", "wsflow"} {
		t.Run(name, func(t *testing.T) {
			catalog, err := DashboardConfigCatalog(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := catalog.Compare(keys); err != nil {
				t.Fatal(err)
			}
			for _, broken := range [][]string{keys[1:], append(slices.Clone(keys), keys[0]), append(slices.Clone(keys), "unknown.setting")} {
				if err := catalog.Compare(broken); err == nil {
					t.Error("accepted changed UI keys")
				}
			}

			projection := catalog.Entries(wsconfig.ScopeGlobal, wsconfigservice.Layers{})
			entries := make(map[string]wsconfigservice.Entry)
			for _, entry := range projection {
				entries[wsconfigservice.FamilyKey(entry.Key)] = entry
			}
			for _, row := range ui {
				entry, ok := entries[row.Key]
				if !ok {
					if !slices.Contains([]string{"agent.profile.*", "worktree.profile.*"}, row.Key) || row.Control != "profile" ||
						row.Section != "profiles" || len(row.Selectors) != 0 {
						t.Errorf("unexpected unprojected row: %+v", row)
					}
					continue
				}
				if len(entry.Selectors) != len(row.Selectors) {
					t.Errorf("%s: UI selectors %v, daemon selectors %v", row.Key, row.Selectors, entry.Selectors)
				}
				for _, selector := range row.Selectors {
					if _, ok := entry.Selectors[selector]; !ok {
						t.Errorf("%s: selector %s unavailable", row.Key, selector)
					}
				}
				switch entry.Type {
				case "boolean", "integer", "enum", "tier":
					want := map[string]string{"boolean": "toggle", "integer": "number", "enum": "choice", "tier": "tier"}[entry.Type]
					if row.Control != want {
						t.Errorf("%s: UI control %q for daemon type %s, want %s", row.Key, row.Control, entry.Type, want)
					}
				}
				if row.Key == "agent.werk_endpoints" && (row.Section != "account" || row.Control != "werk_endpoints") {
					t.Errorf("%s: want the structured Account editor, got %s / %s", row.Key, row.Section, row.Control)
				}
				if strings.HasPrefix(row.Key, "dashboard.companion_") {
					if row.Section != "companion" {
						t.Errorf("%s: companion section = %s", row.Key, row.Section)
					}
				} else if prefix, _, _ := strings.Cut(row.Key, "."); slices.Contains([]string{"claude", "agy", "dashboard", "pi"}, prefix) && row.Section != prefix {
					t.Errorf("%s: adapter section = %s", row.Key, row.Section)
				}
			}
		})
	}
}

// The tier routing page holds, in order, the expose toggle and the guidance
// text above the graph editor, then the deprecated aliases row no panel
// renders (its stored value surfaces only as the page's clear banner).
func TestDashboardSettingsUICatalogTierRoutingSection(t *testing.T) {
	raw, err := os.ReadFile("../wsdashboard/assets/dashboard-settings-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var ui []struct{ Key, Section, Control, Surface string }
	if err := json.Unmarshal(raw, &ui); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, row := range ui {
		if row.Section == "tier_routing" {
			got = append(got, row.Key+"/"+row.Control+"/"+row.Surface)
		}
	}
	want := []string{
		wsconfig.ItemAgentTierRoutingExpose + "/toggle/",
		wsconfig.ItemAgentTierRoutingGuidance + "/textarea/",
		wsconfig.ItemAgentTierRoutingDynamic + "/toggle/",
		wsconfig.ItemAgentTierRoutingDynamicClassifier + "/text/",
		wsconfig.ItemAgentTierRouting + "/tier_routing/",
		wsconfig.ItemAgentTierAliases + "/text/deprecated",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tier_routing rows = %v, want %v", got, want)
	}
	// The daemon still declares the deprecated key (old config.tune writes and
	// alias resolution keep working), and the expose toggle's default is off.
	catalog, err := DashboardConfigCatalog("ws")
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]wsconfigservice.Entry{}
	for _, entry := range catalog.Entries(wsconfig.ScopeGlobal, wsconfigservice.Layers{}) {
		entries[entry.Key] = entry
	}
	if _, ok := entries[wsconfig.ItemAgentTierAliases]; !ok {
		t.Fatalf("%s left the daemon catalog", wsconfig.ItemAgentTierAliases)
	}
	expose, ok := entries[wsconfig.ItemAgentTierRoutingExpose]
	if !ok || expose.Default != "off" || !slices.Equal(expose.Enum, []string{"on", "off"}) {
		t.Fatalf("%s entry = %+v, want default off and the on/off domain", wsconfig.ItemAgentTierRoutingExpose, expose)
	}
	if guidance, ok := entries[wsconfig.ItemAgentTierRoutingGuidance]; !ok || len(guidance.Enum) != 0 {
		t.Fatalf("%s entry = %+v, want free-form text", wsconfig.ItemAgentTierRoutingGuidance, guidance)
	}
	if dynamic, ok := entries[wsconfig.ItemAgentTierRoutingDynamic]; !ok || dynamic.Default != false || dynamic.Type != "boolean" {
		t.Fatalf("%s entry = %+v, want default false boolean", wsconfig.ItemAgentTierRoutingDynamic, dynamic)
	}
	if classifier, ok := entries[wsconfig.ItemAgentTierRoutingDynamicClassifier]; !ok || len(classifier.Enum) != 0 {
		t.Fatalf("%s entry = %+v, want free-form text", wsconfig.ItemAgentTierRoutingDynamicClassifier, classifier)
	}
}

// catalogSurfaces lists the values a catalog row's optional surface field may
// take. An absent surface means the settings panel.
var catalogSurfaces = []string{"deprecated"}

func validCatalogSurface(surface string) bool { return slices.Contains(catalogSurfaces, surface) }

func TestDashboardSettingsUICatalogSurface(t *testing.T) {
	if !validCatalogSurface("deprecated") || validCatalogSurface("companion-modal") || validCatalogSurface("") || validCatalogSurface("panel") || validCatalogSurface("Deprecated") {
		t.Fatal("surface validation must accept only deprecated")
	}
	raw, err := os.ReadFile("../wsdashboard/assets/dashboard-settings-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var ui []struct{ Key, Section, Surface string }
	if err := json.Unmarshal(raw, &ui); err != nil {
		t.Fatal(err)
	}
	var panelCompanion []string
	for _, row := range ui {
		if row.Key == "dashboard.companion_mode" || row.Key == wsconfig.ItemAgentTierAliases {
			if row.Surface != "deprecated" {
				t.Errorf("%s is a deprecated alias and must be hidden from the panel, surface %q", row.Key, row.Surface)
			}
		} else if row.Surface == "deprecated" {
			t.Errorf("%s: unexpected deprecated surface", row.Key)
		} else if strings.HasPrefix(row.Key, "dashboard.companion_") {
			if row.Surface != "" {
				t.Errorf("%s: companion settings must render on the settings panel, surface %q", row.Key, row.Surface)
			}
			if row.Section != "companion" {
				t.Errorf("%s: companion row left the companion section: %s", row.Key, row.Section)
			}
			panelCompanion = append(panelCompanion, row.Key)
		}
	}
	want := []string{
		"dashboard.companion_agent_cooldown_seconds", "dashboard.companion_agent_hourly_cap",
		"dashboard.companion_attachments", "dashboard.companion_body_tier", "dashboard.companion_director", "dashboard.companion_enabled",
		"dashboard.companion_eyes", "dashboard.companion_hair", "dashboard.companion_outfit",
		"dashboard.companion_pack", "dashboard.companion_palette", "dashboard.companion_preset",
		"dashboard.companion_soul", "dashboard.companion_soul_dir", "dashboard.companion_sources",
		"dashboard.companion_work_tier",
	}
	slices.Sort(panelCompanion)
	if !slices.Equal(panelCompanion, want) {
		t.Fatalf("panel companion rows = %v, want %v", panelCompanion, want)
	}
}
