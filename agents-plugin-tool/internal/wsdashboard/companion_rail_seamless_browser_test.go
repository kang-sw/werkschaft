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

// TestCompanionRailSeamlessOverlayBrowser verifies the (1798)=a seamless
// rail-companion overlay contract across both a sparse rail and a rich
// scrollable rail with real or synthetic VRM 0.x and VRM 1.0 models in Dark
// and Light themes:
//   - (a) sparse rail: details.rws-unreg hugs top workspaces directly with no void
//   - (b) mid-scroll overlap: rows visibly pass behind the Companion silhouette,
//     with true click & wheel pass-through and document pointermove gaze tracking
//   - (c) max-bottom scroll: collapsed & expanded details.rws-unreg clear above
//     the Companion's head, and resizing the Companion updates .rlist bottom
//     scroll padding in lockstep
//   - (d) projected 3D laptop hit-zone (.ws-companion-laptop-hit) opens and
//     toggles Companion Messenger on click with zero console/page errors.
//
// Runs when WS_PLAYWRIGHT_MODULE is set; optional WS_COMPANION_SEAMLESS_QA_SHOTS
// (absolute dir) saves the 4 visual QA sheets.
func TestCompanionRailSeamlessOverlayBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_COMPANION_SEAMLESS_QA_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_COMPANION_SEAMLESS_QA_SHOTS must be absolute")
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

	vrm0ID := stageVrmPack("vrm0-seamless-qa", "VRM 0 Fixture", os.Getenv("WS_VRM0_SAMPLE"), "0.x", buildRenderableSyntheticVRM(false))
	vrm1ID := stageVrmPack("vrm1-seamless-qa", "VRM 1 Fixture", os.Getenv("WS_VRM1_SAMPLE"), "1.0", buildRenderableSyntheticVRM(true))

	now := time.Now()
	harnesses := []string{"claude", "codex", "pi"}
	startRailDaemon := func(sparse bool) *pageFixture {
		wsRoot := t.TempDir()
		wsAlpha := mkDir(t, filepath.Join(wsRoot, "alpha-app"))
		wsBeta := mkDir(t, filepath.Join(wsRoot, "beta-service"))
		wsGamma := mkDir(t, filepath.Join(wsRoot, "gamma-infra"))
		unreg1 := mkDir(t, filepath.Join(wsRoot, "scratch-tools"))
		unreg2 := mkDir(t, filepath.Join(wsRoot, "design-sandbox"))
		unreg3 := mkDir(t, filepath.Join(wsRoot, "proto-lab"))

		records := []workspaceRecord{{Path: wsAlpha}}
		if !sparse {
			records = append(records, workspaceRecord{Path: wsBeta}, workspaceRecord{Path: wsGamma})
		}
		wsFile := filepath.Join(t.TempDir(), "workspaces.json")
		wsDoc, err := json.Marshal(workspacesDoc{
			Version:    workspacesSchemaVersion,
			Workspaces: records,
		})
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
		addLive := func(rid, proj, harness, phase string, age time.Duration) {
			reg := Registration{
				ReplyID:      rid,
				Project:      proj,
				Harness:      harness,
				RegisteredAt: stampFor(now.Add(-age)),
				HeartbeatAt:  stampFor(now),
			}
			if err := WriteRegistration(f.dir, reg); err != nil {
				t.Fatal(err)
			}
			writeSnapshot(t, f.dir, rid, &wslive.Snapshot{
				SchemaVersion: 1,
				Adapter:       harness,
				UpdatedAt:     now.UnixMilli(),
				HeartbeatMs:   30000,
				Lead:          wslive.Lead{Activity: wslive.Activity{Phase: phase}},
			})
			f.src.setPage(rid, PageContent{HasBoard: true, Seq: 1, Board: boardRegions(t, "active session board")})
		}

		if sparse {
			addLive("alpha01", wsAlpha, "codex", wslive.PhaseIdle, 12*time.Minute)
			addLive("alpha02", wsAlpha, "claude", wslive.PhaseTool, 6*time.Minute)
			addLive("unreg01", unreg1, "claude", wslive.PhaseIdle, 5*time.Minute)
			addLive("unreg02", unreg2, "pi", wslive.PhaseIdle, 3*time.Minute)
			f.d.Index().Refresh()
			waitFor(t, "sparse rail indexed", func() bool {
				_, ok1 := f.d.Index().Session("alpha02")
				_, ok2 := f.d.Index().Session("unreg02")
				return ok1 && ok2
			})
		} else {
			for i := 1; i <= 10; i++ {
				phase := wslive.PhaseIdle
				if i%4 == 0 {
					phase = wslive.PhaseTool
				}
				addLive(fmt.Sprintf("alpha%02d", i), wsAlpha, harnesses[i%3], phase, time.Duration(40-i)*time.Minute)
			}
			for i := 1; i <= 10; i++ {
				phase := wslive.PhaseIdle
				if i%3 == 0 {
					phase = wslive.PhaseThinking
				}
				addLive(fmt.Sprintf("beta%02d", i), wsBeta, harnesses[(i+1)%3], phase, time.Duration(30-i)*time.Minute)
			}
			for i := 1; i <= 8; i++ {
				phase := wslive.PhaseIdle
				if i%2 == 0 {
					phase = wslive.PhaseTool
				}
				addLive(fmt.Sprintf("gamma%02d", i), wsGamma, harnesses[(i+2)%3], phase, time.Duration(20-i)*time.Minute)
			}
			addLive("unreg01", unreg1, "claude", wslive.PhaseIdle, 8*time.Minute)
			addLive("unreg02", unreg2, "codex", wslive.PhaseIdle, 6*time.Minute)
			addLive("unreg03", unreg3, "pi", wslive.PhaseIdle, 4*time.Minute)
			addLive("brain01", wsAlpha, "pi", wslive.PhaseIdle, 2*time.Minute)
			f.d.SetCompanionBrain(func() (CompanionBrainInfo, error) {
				return CompanionBrainInfo{
					ReplyID:     "brain01",
					WSKey:       "brain01-ws-key",
					Harness:     "pi",
					Model:       "test-model",
					Tier:        "medium",
					PersonaName: "Mina",
					Ready:       true,
				}, nil
			})
			f.d.SetCompanionBrainReplyID("brain01")
			f.d.Index().Refresh()
			waitFor(t, "rich rail indexed", func() bool {
				_, ok1 := f.d.Index().Session("gamma08")
				_, ok2 := f.d.Index().Session("unreg03")
				_, ok3 := f.d.Index().Session("brain01")
				return ok1 && ok2 && ok3
			})
		}
		return f
	}

	sparseFixture := startRailDaemon(true)
	richFixture := startRailDaemon(false)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(
		ctx,
		"node",
		"testdata/companion-rail-seamless.browser.cjs",
		sparseFixture.base,
		richFixture.base,
		shots,
		vrm0ID,
		vrm1ID,
	).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium seamless rail companion verification: %v", err)
	}
}
