package wsdashboard

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
)

// The companion's gear, quick menu, Dashboard Settings /settings?section=companion
// section (core, director gating, 2D wardrobe & palette, disabled-but-editable),
// and a directed command with its quip, on the real shell page in Chromium.
// WS_COMPANION_SHOTS (absolute) keeps the captures.
func TestCompanionSettingsBrowser(t *testing.T) {
	runCompanionBrowser(t, "testdata/companion-settings.browser.cjs", "")
}

// Companion Pack Sources & Advanced .zip bundle import in Dashboard Settings:
// trust notice, official listing, local zip validate/import/partial error,
// inline 2-step pack delete, and authoring links. The configured user source
// is never fetched.
func TestCompanionSourcesBrowser(t *testing.T) {
	runCompanionBrowser(t, "testdata/companion-sources.browser.cjs", companionBrowserSource)
}

// Dashboard Settings Companion section visual/layout invariants in both Dark
// and Light themes, VRM-active vs 2D-fallback wardrobe switching, disabled-but-
// editable banner, inline 2-step delete, Advanced .zip accordion, and gear-to-
// settings handoff. When WS_COMPANION_QA_SHOTS (absolute) is set, captures all
// 4 high-DPI QA screenshots.
func TestCompanionSettingsVrmAndThemeBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	qaShots := os.Getenv("WS_COMPANION_QA_SHOTS")
	if qaShots != "" && !filepath.IsAbs(qaShots) {
		t.Fatal("WS_COMPANION_QA_SHOTS must be absolute")
	}
	ensurePlaywrightBrowsersPath(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	SetDevAssets(nil)

	backend := companionSettingsBackend(t, CompanionConfig{
		Enabled:              true,
		Director:             "rules",
		Pack:                 "studio-vrm",
		Preset:               "werkschaft-d1",
		AgentCooldownSeconds: 30,
		AgentHourlyCap:       20,
		Sources:              companionBrowserSource,
	})
	paths, err := companionPaths(backend.Options)
	if err != nil {
		t.Fatal(err)
	}
	stageSyntheticVrmPack(t, paths.packs, "studio-vrm", "Studio VRM Avatar", true)
	stageSyntheticVrmPack(t, paths.packs, "alt-vrm", "Alternate VRM Avatar", false)

	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
		o.Config = backend
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/companion-settings-qa.browser.cjs", f.base, qaShots, companionBrowserSource).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium companion settings VRM/theme QA: %v", err)
	}
}

const companionBrowserSource = "https://example.invalid/packs.git"

func ensurePlaywrightBrowsersPath(t *testing.T) {
	t.Helper()
	if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			t.Setenv("PLAYWRIGHT_BROWSERS_PATH", filepath.Join(home, "Library", "Caches", "ms-playwright"))
		}
	}
}

func stageSyntheticVrmPack(t *testing.T, packsDir, id, name string, vrm1 bool) {
	t.Helper()
	raw := buildRenderableSyntheticVRM(vrm1)
	dir := filepath.Join(packsDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.vrm"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(map[string]any{
		"schema":  "ws-companion-vrm/v1",
		"id":      id,
		"name":    name,
		"version": "1.0.0",
		"model":   "model.vrm",
		"persona": map[string]any{
			"name":  name,
			"voice": "Calm, concise companion status updates.",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
}

func int64Ptr(v int64) *int64 { return &v }

// companionSettingsBackend builds an isolated ConfigBackend containing the real
// dashboard.companion_* config rules and seeds initial global overrides so
// /api/config/snapshot, /api/config/set, /api/config/reset, and LoadCompanion
// stay consistent across Dashboard Settings browser tests.
func companionSettingsBackend(t *testing.T, initial CompanionConfig) *ConfigBackend {
	t.Helper()
	parseBool := func(s string) (any, error) { return strconv.ParseBool(s) }
	normBool := func(s string) (string, error) {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return "", err
		}
		return strconv.FormatBool(b), nil
	}
	parseInt := func(s string) (any, error) { return strconv.ParseInt(s, 10, 64) }
	catalog, err := wsconfigservice.NewCatalog([]wsconfigservice.Rule{
		{Key: "workflow.lang", Type: "string", Default: "English"},
		{Key: "dashboard.companion_enabled", Type: "boolean", Default: "false", GlobalOnly: true, Decode: parseBool, Normalize: normBool},
		{Key: "dashboard.companion_director", Type: "enum", Enum: []string{"rules", "agent"}, Default: "rules", GlobalOnly: true},
		{Key: "dashboard.companion_pack", Type: "string", Default: "default", GlobalOnly: true},
		{Key: "dashboard.companion_preset", Type: "string", Default: "werkschaft-d1", GlobalOnly: true},
		{Key: "dashboard.companion_outfit", Type: "string", Default: "", GlobalOnly: true},
		{Key: "dashboard.companion_hair", Type: "string", Default: "", GlobalOnly: true},
		{Key: "dashboard.companion_eyes", Type: "string", Default: "", GlobalOnly: true},
		{Key: "dashboard.companion_attachments", Type: "string", Default: "", GlobalOnly: true},
		{Key: "dashboard.companion_palette", Type: "string", Default: "", GlobalOnly: true},
		{Key: "dashboard.companion_agent_cooldown_seconds", Type: "integer", Minimum: int64Ptr(10), Maximum: int64Ptr(3600), Default: "30", GlobalOnly: true, Decode: parseInt},
		{Key: "dashboard.companion_agent_hourly_cap", Type: "integer", Minimum: int64Ptr(1), Maximum: int64Ptr(120), Default: "20", GlobalOnly: true, Decode: parseInt},
		{Key: "dashboard.companion_sources", Type: "string", Default: "", GlobalOnly: true},
		{Key: "dashboard.companion_soul", Type: "string", Default: "", GlobalOnly: true},
		{Key: "dashboard.companion_soul_dir", Type: "string", Default: "", GlobalOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfgHome := filepath.Join(t.TempDir(), "global")
	cacheHome := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(cfgHome, 0o700); err != nil {
		t.Fatal(err)
	}
	overrides := map[string]string{
		"dashboard.companion_enabled":                strconv.FormatBool(initial.Enabled),
		"dashboard.companion_director":               initial.Director,
		"dashboard.companion_pack":                   initial.Pack,
		"dashboard.companion_preset":                 initial.Preset,
		"dashboard.companion_agent_cooldown_seconds": strconv.Itoa(initial.AgentCooldownSeconds),
		"dashboard.companion_agent_hourly_cap":       strconv.Itoa(initial.AgentHourlyCap),
	}
	if initial.Sources != "" {
		overrides["dashboard.companion_sources"] = initial.Sources
	}
	if initial.Soul != "" {
		overrides["dashboard.companion_soul"] = initial.Soul
	}
	if initial.SoulDir != "" {
		overrides["dashboard.companion_soul_dir"] = initial.SoulDir
	}
	if initial.Outfit != "" {
		overrides["dashboard.companion_outfit"] = initial.Outfit
	}
	if initial.Hair != "" {
		overrides["dashboard.companion_hair"] = initial.Hair
	}
	if initial.Eyes != "" {
		overrides["dashboard.companion_eyes"] = initial.Eyes
	}
	if initial.Attachments != "" {
		overrides["dashboard.companion_attachments"] = initial.Attachments
	}
	if initial.Palette != "" {
		overrides["dashboard.companion_palette"] = initial.Palette
	}
	raw, err := json.Marshal(map[string]any{"overrides": overrides})
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgHome, "config.json")
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := wsconfig.Options{CacheHome: cacheHome, ConfigHome: cfgHome}
	backend := &ConfigBackend{
		Catalog: catalog,
		Options: opts,
		OrchestrateWarning: func(string) string {
			return "Harness has not been verified."
		},
	}
	backend.LoadCompanion = func() (CompanionConfig, error) {
		cfg := initial
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			return cfg, nil
		}
		var file struct {
			Overrides map[string]string `json:"overrides"`
		}
		if json.Unmarshal(data, &file) != nil || file.Overrides == nil {
			return cfg, nil
		}
		if v, ok := file.Overrides["dashboard.companion_enabled"]; ok {
			cfg.Enabled = v == "true"
		}
		if v, ok := file.Overrides["dashboard.companion_director"]; ok {
			cfg.Director = v
		}
		if v, ok := file.Overrides["dashboard.companion_pack"]; ok {
			cfg.Pack = v
		}
		if v, ok := file.Overrides["dashboard.companion_preset"]; ok {
			cfg.Preset = v
		}
		if v, ok := file.Overrides["dashboard.companion_outfit"]; ok {
			cfg.Outfit = v
		}
		if v, ok := file.Overrides["dashboard.companion_hair"]; ok {
			cfg.Hair = v
		}
		if v, ok := file.Overrides["dashboard.companion_eyes"]; ok {
			cfg.Eyes = v
		}
		if v, ok := file.Overrides["dashboard.companion_attachments"]; ok {
			cfg.Attachments = v
		}
		if v, ok := file.Overrides["dashboard.companion_palette"]; ok {
			cfg.Palette = v
		}
		if v, ok := file.Overrides["dashboard.companion_sources"]; ok {
			cfg.Sources = v
		}
		if v, ok := file.Overrides["dashboard.companion_soul"]; ok {
			cfg.Soul = v
		}
		if v, ok := file.Overrides["dashboard.companion_soul_dir"]; ok {
			cfg.SoulDir = v
		}
		if v, ok := file.Overrides["dashboard.companion_agent_cooldown_seconds"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.AgentCooldownSeconds = n
			}
		}
		if v, ok := file.Overrides["dashboard.companion_agent_hourly_cap"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.AgentHourlyCap = n
			}
		}
		return cfg, nil
	}
	return backend
}

func runCompanionBrowser(t *testing.T, script, sources string) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_COMPANION_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_COMPANION_SHOTS must be absolute")
	}
	ensurePlaywrightBrowsersPath(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	SetDevAssets(nil)
	backend := companionSettingsBackend(t, CompanionConfig{
		Enabled:              true,
		Director:             "rules",
		Pack:                 "default",
		Preset:               "werkschaft-d1",
		AgentCooldownSeconds: 30,
		AgentHourlyCap:       20,
		Sources:              sources,
	})
	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
		o.Config = backend
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", script, f.base, shots, sources).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium companion settings: %v", err)
	}
}
