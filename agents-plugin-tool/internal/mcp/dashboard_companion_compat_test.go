package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// Release 0.51.0 shipped dashboard.companion_mode (off | visual | full) and the
// director values sidecar and lead-tool; later releases replaced them with
// dashboard.companion_enabled and rules | agent. These tests pin the upgrade
// scenarios that keep that removal off the T3 old-client boundary
// (ai-docs/manuals/release-compatibility.md).

// v0510CompanionModes is the enum v0.51.0 declared for dashboard.companion_mode
// (v0.51.0:agents-plugin/dashboard-config-manifest.json) and its reader
// accepted, falling back to the default "off" for any other stored value
// (v0.51.0:agents-plugin-tool/internal/mcp/dashboard_settings_config.go,
// dashboardCompanionConfig).
var v0510CompanionModes = []string{"off", "visual", "full"}

// v0510CompanionMode replays how a 0.51.0 binary read the stored mode.
func v0510CompanionMode(overrides map[string]string) string {
	if raw, ok := overrides[wsdashboard.CompanionModeKey]; ok && slices.Contains(v0510CompanionModes, strings.TrimSpace(raw)) {
		return strings.TrimSpace(raw)
	}
	return "off"
}

// rawCompanionOverrides reads the global file's overrides as stored, with no
// reconcile: what an older binary reads.
func rawCompanionOverrides(t *testing.T, opts wsconfig.Options) map[string]string {
	t.Helper()
	path, err := wsconfig.GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Overrides map[string]string `json:"overrides"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if file.Overrides == nil {
		file.Overrides = map[string]string{}
	}
	return file.Overrides
}

// v0510WriteMode replays a 0.51.0 writer: it sets only the stored mode in the
// global file's overrides and keeps every other member (0.51.0's resolver
// rewrote the overrides map whole).
func v0510WriteMode(t *testing.T, opts wsconfig.Options, mode string) {
	t.Helper()
	path, err := wsconfig.GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	file := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatal(err)
		}
	}
	overrides, _ := file["overrides"].(map[string]any)
	if overrides == nil {
		overrides = map[string]any{}
	}
	overrides[wsdashboard.CompanionModeKey] = mode
	file["overrides"] = overrides
	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func companionCompatServer(t *testing.T) *Server {
	t.Helper()
	return dashboardServer(t, dashboardPackages[0].dashboard)
}

func tuneCompanion(t *testing.T, s *Server, key, value string) string {
	t.Helper()
	args := map[string]any{"key": key, "value": value}
	if value == "" {
		args = map[string]any{"key": key, "reset": true}
	}
	resp := dashCall(t, s, "config.tune", args)
	if toolIsError(t, resp) {
		t.Fatalf("config.tune %s=%q refused: %s", key, value, resp)
	}
	return toolText(t, resp)
}

func effectiveCompanion(t *testing.T, s *Server) wsdashboard.CompanionConfig {
	t.Helper()
	catalog, err := DashboardConfigCatalog("ws")
	if err != nil {
		t.Fatal(err)
	}
	got, err := dashboardCompanionConfig(catalog, s.configOptions())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Scenario 1: an old config.tune call is accepted and maps onto the new keys.
func TestCompanionLegacyModeTuneIsAnAlias(t *testing.T) {
	s := companionCompatServer(t)
	opts := s.configOptions()
	for _, tc := range []struct {
		mode        string
		wantEnabled bool
	}{{"visual", true}, {"full", true}, {"off", false}} {
		text := tuneCompanion(t, s, "dashboard.companion_mode", tc.mode)
		if !strings.Contains(text, "deprecated") || !strings.Contains(text, wsdashboard.CompanionEnabledKey) {
			t.Errorf("tune %s: reply does not name the replacement: %q", tc.mode, text)
		}
		stored := rawCompanionOverrides(t, opts)
		// The legacy key keeps the value the caller wrote, so an old reader
		// reads back what its own tune stored (full stays full).
		if stored[wsdashboard.CompanionModeKey] != tc.mode || stored[wsdashboard.CompanionEnabledKey] != boolString(tc.wantEnabled) {
			t.Errorf("tune %s stored %v, want mode %q and enabled %v", tc.mode, stored, tc.mode, tc.wantEnabled)
		}
		if got := effectiveCompanion(t, s); got.Enabled != tc.wantEnabled {
			t.Errorf("tune %s: shell enabled = %v, want %v", tc.mode, got.Enabled, tc.wantEnabled)
		}
		got, err := s.resolveConfigGet("", wsdashboard.CompanionEnabledKey)
		if err != nil || got.Value != tc.wantEnabled {
			t.Errorf("tune %s: config.get enabled = %v, %v; want %v", tc.mode, got.Value, err, tc.wantEnabled)
		}
		old, err := s.resolveConfigGet("", wsdashboard.CompanionModeKey)
		if err != nil || old.Value != tc.mode {
			t.Errorf("tune %s: config.get companion_mode = %v, %v; want %q", tc.mode, old.Value, err, tc.mode)
		}
	}

	// An invalid legacy value is still refused with the legacy enum.
	resp := dashCall(t, s, "config.tune", map[string]any{"key": "dashboard.companion_mode", "value": "on"})
	if !toolIsError(t, resp) || !strings.Contains(resp, "off, visual, full") {
		t.Errorf("tune companion_mode=on: %s, want a refusal listing off, visual, full", resp)
	}

	// Reset through the old key resets the pet to its default, off.
	tuneCompanion(t, s, "dashboard.companion_mode", "full")
	tuneCompanion(t, s, "dashboard.companion_mode", "")
	if stored := rawCompanionOverrides(t, opts); len(stored) != 0 {
		t.Errorf("reset through companion_mode left %v", stored)
	}
	if got := effectiveCompanion(t, s); got.Enabled {
		t.Error("reset through companion_mode left the pet enabled")
	}
}

func TestCompanionLegacyDirectorValuesAreAccepted(t *testing.T) {
	s := companionCompatServer(t)
	for _, legacy := range []string{"sidecar", "lead-tool"} {
		text := tuneCompanion(t, s, "dashboard.companion_director", legacy)
		if !strings.Contains(text, "deprecated") || !strings.Contains(text, `"rules"`) {
			t.Errorf("tune director=%s: reply does not report the stored successor: %q", legacy, text)
		}
		if stored := rawCompanionOverrides(t, s.configOptions()); stored[wsdashboard.CompanionDirectorKey] != "rules" {
			t.Errorf("tune director=%s stored %q, want rules", legacy, stored[wsdashboard.CompanionDirectorKey])
		}
		if got := effectiveCompanion(t, s); got.Director != "rules" {
			t.Errorf("tune director=%s: shell director = %q, want rules", legacy, got.Director)
		}
	}
	// The current values are unchanged and carry no deprecation note.
	if text := tuneCompanion(t, s, "dashboard.companion_director", "agent"); strings.Contains(text, "deprecated") {
		t.Errorf("tune director=agent reported a deprecation: %q", text)
	}
	if got := effectiveCompanion(t, s); got.Director != "agent" {
		t.Errorf("director = %q, want agent", got.Director)
	}
	if resp := dashCall(t, s, "config.tune", map[string]any{"key": "dashboard.companion_director", "value": "other"}); !toolIsError(t, resp) {
		t.Errorf("tune director=other was accepted: %s", resp)
	}
}

// Scenario 2: a value 0.51.0 stored is honoured while the new keys are unset.
func TestCompanionStoredLegacyValuesAreHonoured(t *testing.T) {
	catalog, err := DashboardConfigCatalog("ws")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, overrides string
		want            wsdashboard.CompanionConfig
	}{
		{"visual keeps the pet on", `{"overrides":{"dashboard.companion_mode":"visual"}}`,
			wsdashboard.CompanionConfig{Enabled: true, Director: "rules", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 30, AgentHourlyCap: 20}},
		{"full keeps the pet on", `{"overrides":{"dashboard.companion_mode":"full","dashboard.companion_pack":"auto"}}`,
			wsdashboard.CompanionConfig{Enabled: true, Director: "rules", Pack: "auto", Preset: "werkschaft-d1", AgentCooldownSeconds: 30, AgentHourlyCap: 20}},
		{"off stays off", `{"overrides":{"dashboard.companion_mode":"off"}}`,
			wsdashboard.CompanionConfig{Director: "rules", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 30, AgentHourlyCap: 20}},
		{"an unknown mode falls back to the default", `{"overrides":{"dashboard.companion_mode":"loud"}}`,
			wsdashboard.CompanionConfig{Director: "rules", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 30, AgentHourlyCap: 20}},
		{"removed director values mean rules", `{"overrides":{"dashboard.companion_mode":"visual","dashboard.companion_director":"sidecar"}}`,
			wsdashboard.CompanionConfig{Enabled: true, Director: "rules", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 30, AgentHourlyCap: 20}},
		{"lead-tool means rules", `{"overrides":{"dashboard.companion_director":"lead-tool"}}`,
			wsdashboard.CompanionConfig{Director: "rules", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 30, AgentHourlyCap: 20}},
		{"a stored new key without the legacy key stands", `{"overrides":{"dashboard.companion_enabled":"true","dashboard.companion_director":"agent"}}`,
			wsdashboard.CompanionConfig{Enabled: true, Director: "agent", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 30, AgentHourlyCap: 20}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := companionCompatServer(t)
			opts := s.configOptions()
			path, err := wsconfig.GlobalPath(opts)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Dir(path), filepath.Base(path), tc.overrides)
			got, err := dashboardCompanionConfig(catalog, opts)
			if err != nil || got != tc.want {
				t.Fatalf("config = %+v, %v; want %+v", got, err, tc.want)
			}
			// config.get of the new key reports what the pet does, not the
			// unset default.
			enabled, err := s.resolveConfigGet("", wsdashboard.CompanionEnabledKey)
			if err != nil || enabled.Value != tc.want.Enabled || len(enabled.Warnings) != 0 {
				t.Errorf("config.get enabled = %+v, %v; want %v with no warning", enabled, err, tc.want.Enabled)
			}
			director, err := s.resolveConfigGet("", wsdashboard.CompanionDirectorKey)
			if err != nil || director.Value != tc.want.Director || len(director.Warnings) != 0 {
				t.Errorf("config.get director = %+v, %v; want %q with no warning", director, err, tc.want.Director)
			}
		})
	}
}

// Scenario 3: the new binary writes; a 0.51.0 reader still reads a sensible
// mode from the legacy key, and ignores the keys it does not know.
func TestCompanionNewWriteKeepsTheLegacyKeyForOldReaders(t *testing.T) {
	s := companionCompatServer(t)
	opts := s.configOptions()

	// Nothing stored: a 0.51.0 reader's default is off.
	if got := v0510CompanionMode(rawCompanionOverrides(t, opts)); got != "off" {
		t.Fatalf("fresh config reads %q", got)
	}
	tuneCompanion(t, s, "dashboard.companion_enabled", "true")
	stored := rawCompanionOverrides(t, opts)
	if stored[wsdashboard.CompanionEnabledKey] != "true" || v0510CompanionMode(stored) != "visual" {
		t.Errorf("enabling stored %v; a 0.51.0 reader must read visual", stored)
	}
	tuneCompanion(t, s, "dashboard.companion_enabled", "false")
	if got := v0510CompanionMode(rawCompanionOverrides(t, opts)); got != "off" {
		t.Errorf("disabling reads %q on a 0.51.0 reader, want off", got)
	}
	// A legacy full survives a re-enable: the pet looks the same to both.
	tuneCompanion(t, s, "dashboard.companion_mode", "full")
	tuneCompanion(t, s, "dashboard.companion_enabled", "true")
	if got := v0510CompanionMode(rawCompanionOverrides(t, opts)); got != "full" {
		t.Errorf("re-enabling rewrote full to %q", got)
	}
	// Reset through the new key resets what the old reader sees.
	tuneCompanion(t, s, "dashboard.companion_enabled", "")
	stored = rawCompanionOverrides(t, opts)
	if _, left := stored[wsdashboard.CompanionModeKey]; left || v0510CompanionMode(stored) != "off" {
		t.Errorf("reset stored %v; a 0.51.0 reader must read its default off", stored)
	}

	// A new director value reaches a 0.51.0 reader as an unknown value it
	// falls back from (it reads no director); it is not a refusal and the
	// legacy mode is untouched.
	tuneCompanion(t, s, "dashboard.companion_enabled", "true")
	tuneCompanion(t, s, "dashboard.companion_director", "agent")
	stored = rawCompanionOverrides(t, opts)
	if v0510CompanionMode(stored) != "visual" || stored[wsdashboard.CompanionDirectorKey] != "agent" {
		t.Errorf("stored %v", stored)
	}
}

// Scenario 4: an old binary writes the legacy key after the new binary wrote
// the new keys; the daemon honours the old write and the next new write puts
// both back in step.
func TestCompanionOldWriterAfterNewWriterIsHonoured(t *testing.T) {
	s := companionCompatServer(t)
	opts := s.configOptions()
	tuneCompanion(t, s, "dashboard.companion_enabled", "true")
	tuneCompanion(t, s, "dashboard.companion_pack", "auto")

	v0510WriteMode(t, opts, "off")
	if got := effectiveCompanion(t, s); got.Enabled || got.Pack != "auto" {
		t.Fatalf("after a 0.51.0 mode=off write: %+v, want disabled and the pack kept", got)
	}
	if got, err := s.resolveConfigGet("", wsdashboard.CompanionEnabledKey); err != nil || got.Value != false {
		t.Errorf("config.get enabled = %v, %v; want false", got.Value, err)
	}
	// The stale new key is still in the file; the read result is what counts.
	if stored := rawCompanionOverrides(t, opts); stored[wsdashboard.CompanionEnabledKey] != "true" {
		t.Errorf("the old write changed the new key: %v", stored)
	}

	v0510WriteMode(t, opts, "full")
	if got := effectiveCompanion(t, s); !got.Enabled {
		t.Errorf("after a 0.51.0 mode=full write the pet is %+v, want enabled", got)
	}

	// A new write puts both keys back in step, whatever the old writer left.
	v0510WriteMode(t, opts, "off")
	tuneCompanion(t, s, "dashboard.companion_enabled", "true")
	stored := rawCompanionOverrides(t, opts)
	if stored[wsdashboard.CompanionEnabledKey] != "true" || v0510CompanionMode(stored) != "visual" {
		t.Errorf("a new write left %v out of step", stored)
	}
	// The old writer's rewrite keeps every key it does not know.
	if stored[wsdashboard.CompanionPackKey] != "auto" {
		t.Errorf("pack lost across the old writer: %v", stored)
	}
}

// The legacy key is global-only like the keys that replaced it, and a write
// through it notifies the daemon so the pet changes at once.
func TestCompanionLegacyModeIsGlobalOnlyAndNotifies(t *testing.T) {
	if !wsconfig.GlobalOnly(wsdashboard.CompanionModeKey) || !wsdashboard.IsCompanionConfigKey(wsdashboard.CompanionModeKey) {
		t.Fatal("companion_mode must be a global-only companion key")
	}
	s := companionCompatServer(t)
	resp := dashCall(t, s, "config.tune", map[string]any{"key": "dashboard.companion_mode", "value": "visual", "scope": "project"})
	if !toolIsError(t, resp) || !strings.Contains(resp, "global") {
		t.Errorf("project-scope companion_mode tune: %s, want a global-only refusal", resp)
	}
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
