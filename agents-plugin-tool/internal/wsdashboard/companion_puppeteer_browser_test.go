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

// TestCompanionPuppeteerCompactMessengerBrowser exercises the 3-tier Companion
// UI flow in headless Chromium when WS_PLAYWRIGHT_MODULE is set:
//   - Messenger mounts compact Brain iframe (/s/<brainRid>?frame=1&compact=1),
//     hides shell chrome, pauses on close (wsShell shown:false), and supports pop-out.
//   - Sending triggers 0ms Client Physical Reflex (phone_pickup + hold),
//     yields cleanly to Puppeteer companion-command (phone_typing + quip),
//     and releases cleanly on companion-brain-state brain_idle.
//   - Stage speech bubble (.ws-companion-quip) renders head-anchored with
//     directional tail, textContent-only safety, dark/light contrast, no
//     viewport/.shell-bottom clipping, and zero console/page errors across
//     both VRM 1.0 and VRM 0.x.
func TestCompanionPuppeteerCompactMessengerBrowser(t *testing.T) {
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
		Enabled:              true,
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
	stageVrmPack := func(id, name, srcPath, wantVer string, fallback []byte) string {
		var raw []byte
		if srcPath != "" {
			var err error
			raw, err = os.ReadFile(srcPath)
			if err != nil {
				t.Fatalf("read VRM fixture %q: %v", srcPath, err)
			}
		} else {
			raw = fallback
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

	vrm0ID := stageVrmPack("vrm0-sample", "VRM 0 Fixture", os.Getenv("WS_VRM0_SAMPLE"), "0.x", buildRenderableSyntheticVRM(false))
	vrm1ID := stageVrmPack("vrm1-sample", "VRM 1 Fixture", os.Getenv("WS_VRM1_SAMPLE"), "1.0", buildRenderableSyntheticVRM(true))

	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
		o.Config = backend
	})

	const brainRid = "brain123"
	now := time.Now()
	register(t, f.dir, brainRid, f.project, now)
	f.src.setPage(brainRid, PageContent{HasBoard: false, Seq: 1})
	writeSnapshot(t, f.dir, "livesession", &wslive.Snapshot{
		SchemaVersion: 1,
		Adapter:       "pi",
		UpdatedAt:     now.UnixMilli(),
		HeartbeatMs:   30000,
		Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}},
	})
	writeSnapshot(t, f.dir, brainRid, &wslive.Snapshot{
		SchemaVersion: 1,
		Adapter:       "pi",
		UpdatedAt:     now.UnixMilli(),
		HeartbeatMs:   30000,
		Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseReplying}, Reply: &wslive.Reply{Text: "Hello from Brain compact frame!"}},
	})
	f.d.SetCompanionBrain(func() (CompanionBrainInfo, error) {
		return CompanionBrainInfo{
			ReplyID:     brainRid,
			WSKey:       "brain-ws-key",
			Harness:     "pi",
			Model:       "test-model",
			Tier:        "medium",
			PersonaName: "Mina",
			Ready:       true,
		}, nil
	})
	f.d.SetCompanionBrainReplyID(brainRid)
	f.d.Index().Refresh()
	waitFor(t, "brain123 indexed", func() bool {
		_, ok := f.d.Index().Session(brainRid)
		return ok
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/companion-puppeteer.browser.cjs", f.base, brainRid, vrm0ID, vrm1ID).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium companion puppeteer verification: %v", err)
	}
}
