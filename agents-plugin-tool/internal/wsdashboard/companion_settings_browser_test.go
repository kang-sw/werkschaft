package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The companion's gear and floating settings modal, and a directed command
// with its quip, on the real shell page in Chromium. WS_COMPANION_SHOTS
// (absolute) keeps the captures.
func TestCompanionSettingsBrowser(t *testing.T) {
	runCompanionBrowser(t, "testdata/companion-settings.browser.cjs", "")
}

// The modal's Sources section: the official listing and a local zip import
// preview, install and uninstall. The configured user source is never fetched.
func TestCompanionSourcesBrowser(t *testing.T) {
	runCompanionBrowser(t, "testdata/companion-sources.browser.cjs", companionBrowserSource)
}

const companionBrowserSource = "https://example.invalid/packs.git"

func runCompanionBrowser(t *testing.T, script, sources string) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_COMPANION_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_COMPANION_SHOTS must be absolute")
	}
	if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			t.Setenv("PLAYWRIGHT_BROWSERS_PATH", filepath.Join(home, "Library", "Caches", "ms-playwright"))
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	SetDevAssets(nil)
	backend := settingsTestBackend(t)
	backend.LoadCompanion = func() (CompanionConfig, error) {
		return CompanionConfig{Enabled: true, Director: "rules", Pack: "default", Preset: "werkschaft-d1",
			AgentCooldownSeconds: 30, AgentHourlyCap: 20, Sources: sources}, nil
	}
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
