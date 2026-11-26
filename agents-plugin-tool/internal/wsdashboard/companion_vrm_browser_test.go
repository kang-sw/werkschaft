package wsdashboard

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// Headless Chromium acceptance test for the lazy-loaded VRM companion runtime.
// Runs when WS_PLAYWRIGHT_MODULE is set; mounts local untracked VRM 0.x / 1.0
// fixtures from WS_VRM0_SAMPLE and WS_VRM1_SAMPLE into a temporary config home.
// Optional WS_COMPANION_SHOTS (absolute dir) saves screenshots for inspection.
func TestCompanionVrmBrowser(t *testing.T) {
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
	cfgHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", cfgHome)
	SetDevAssets(nil)

	backend := settingsTestBackend(t)
	backend.LoadCompanion = func() (CompanionConfig, error) {
		return CompanionConfig{
			Enabled:              false,
			Director:             "rules",
			Pack:                 "default",
			Preset:               "werkschaft-d1",
			AgentCooldownSeconds: 30,
			AgentHourlyCap:       20,
		}, nil
	}

	paths, err := companionPaths(backend.Options)
	if err != nil {
		t.Fatal(err)
	}
	stageVrmPack := func(id, name, srcPath string) string {
		if srcPath == "" {
			return ""
		}
		raw, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("read VRM fixture %q: %v", srcPath, err)
		}
		dir := filepath.Join(paths.packs, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "model.vrm"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		manifest, err := json.Marshal(map[string]any{
			"schema": "ws-companion-vrm/v1",
			"id":     id,
			"name":   name,
			"model":  "model.vrm",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), manifest, 0o644); err != nil {
			t.Fatal(err)
		}
		return id
	}

	vrm0ID := stageVrmPack("vrm0-sample", "VRM 0 Fixture", os.Getenv("WS_VRM0_SAMPLE"))
	vrm1ID := stageVrmPack("vrm1-sample", "VRM 1 Fixture", os.Getenv("WS_VRM1_SAMPLE"))

	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
		o.Config = backend
	})
	writeSnapshot(t, f.dir, "livesession", &wslive.Snapshot{
		SchemaVersion: 1,
		Adapter:       "pi",
		UpdatedAt:     time.Now().UnixMilli(),
		HeartbeatMs:   30000,
		Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}},
	})
	f.d.Index().Refresh()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	phase := os.Getenv("WS_VRM_TEST_PHASE")
	if phase == "" {
		phase = "1"
	}
	out, err := exec.CommandContext(ctx, "node", "testdata/companion-vrm.browser.cjs", f.base, shots, vrm0ID, vrm1ID, phase).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium VRM companion verification: %v", err)
	}
}
