package wsdashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// companionVrmBrowserFixture is a live dashboard with five idle alpha sessions and
// two staged VRM packs (real samples via WS_VRM0_SAMPLE / WS_VRM1_SAMPLE, else
// synthetic renderable fallbacks).
type companionVrmBrowserFixture struct {
	base   string
	vrm0ID string
	vrm1ID string
}

// startCompanionVrmBrowserFixture skips unless WS_PLAYWRIGHT_MODULE is set and
// stages the shared Co-Lead browser fixture under temporary homes.
func startCompanionVrmBrowserFixture(t *testing.T, prefix string) companionVrmBrowserFixture {
	t.Helper()
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
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

	backend := companionSettingsBackend(t, CompanionConfig{
		Enabled:              false,
		Director:             "rules",
		Pack:                 "default",
		Preset:               "werkschaft-d1",
		AgentCooldownSeconds: 30,
		AgentHourlyCap:       20,
	})
	paths, err := companionPaths(backend.Options)
	if err != nil {
		t.Fatal(err)
	}
	stage := func(id, name, srcPath, wantVer string, fallback []byte) string {
		raw := fallback
		if srcPath != "" {
			raw, err = os.ReadFile(srcPath)
			if err != nil {
				t.Fatalf("read VRM fixture %q: %v", srcPath, err)
			}
		}
		if ver, err := inspectCompanionVRM(raw); err != nil || ver != wantVer {
			t.Fatalf("VRM fixture %q failed inspector: ver=%q want=%q err=%v", id, ver, wantVer, err)
		}
		dir := filepath.Join(paths.packs, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "model.vrm"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		manifest, err := json.Marshal(map[string]any{"schema": "ws-companion-vrm/v1", "id": id, "name": name, "model": "model.vrm"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), manifest, 0o644); err != nil {
			t.Fatal(err)
		}
		return id
	}
	fx := companionVrmBrowserFixture{
		vrm0ID: stage(prefix+"-vrm0", "VRM 0 Fixture", os.Getenv("WS_VRM0_SAMPLE"), "0.x", buildRenderableSyntheticVRM(false)),
		vrm1ID: stage(prefix+"-vrm1", "VRM 1 Fixture", os.Getenv("WS_VRM1_SAMPLE"), "1.0", buildRenderableSyntheticVRM(true)),
	}

	wsAlpha := mkDir(t, filepath.Join(t.TempDir(), "alpha-app"))
	wsFile := filepath.Join(cfgHome, "dashboard", "workspaces.json")
	if err := os.MkdirAll(filepath.Dir(wsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	wsDoc, err := json.Marshal(workspacesDoc{Version: workspacesSchemaVersion, Workspaces: []workspaceRecord{{Path: wsAlpha}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wsFile, wsDoc, 0o644); err != nil {
		t.Fatal(err)
	}
	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
		o.Config = backend
		o.WorkspacesFile = wsFile
	})
	now := time.Now()
	for i := 1; i <= 5; i++ {
		rid := fmt.Sprintf("alpha%02d", i)
		reg := Registration{
			ReplyID:      rid,
			Project:      wsAlpha,
			Harness:      "claude",
			RegisteredAt: stampFor(now.Add(-time.Duration(20-i) * time.Minute)),
			HeartbeatAt:  stampFor(now),
		}
		if err := WriteRegistration(f.dir, reg); err != nil {
			t.Fatal(err)
		}
		writeSnapshot(t, f.dir, rid, &wslive.Snapshot{
			SchemaVersion: 1,
			Adapter:       "claude",
			UpdatedAt:     now.UnixMilli(),
			HeartbeatMs:   30000,
			Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}},
		})
		f.src.setPage(rid, PageContent{HasBoard: true, Seq: 1, Board: boardRegions(t, "active session board")})
	}
	f.d.Index().Refresh()
	waitFor(t, prefix+" sessions indexed", func() bool {
		_, ok := f.d.Index().Session("alpha05")
		return ok
	})
	fx.base = f.base
	return fx
}

// TestCompanionVrmPhase2StudioBrowser verifies Phase 2 visual polish on VRM 1.0
// and VRM 0.x: pixel-measured 10-14% headroom with no head/shoulder edge contact
// at 220/280/360px rail widths in Dark and Light themes, the 14-19% laptop lid band
// below the chin, theme-following studio lighting with sane exposure, and the
// held-phone palm grip. Optional WS_COMPANION_PHASE2_QA_SHOTS (absolute dir)
// writes the contact sheets; optional WS_COMPANION_PHASE2_QA_PNGS (absolute dir)
// writes crisp 2x per-VRM/theme/width laptop_work and phone_hold PNGs and checks
// the held phone at every rail width.
func TestCompanionVrmPhase2StudioBrowser(t *testing.T) {
	shots := os.Getenv("WS_COMPANION_PHASE2_QA_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_COMPANION_PHASE2_QA_SHOTS must be absolute")
	}
	if pngs := os.Getenv("WS_COMPANION_PHASE2_QA_PNGS"); pngs != "" && !filepath.IsAbs(pngs) {
		t.Fatal("WS_COMPANION_PHASE2_QA_PNGS must be absolute")
	}
	fx := startCompanionVrmBrowserFixture(t, "phase2")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/companion-vrm-phase2-studio.browser.cjs", fx.base, shots, fx.vrm0ID, fx.vrm1ID).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium VRM Phase 2 studio verification: %v", err)
	}
}
