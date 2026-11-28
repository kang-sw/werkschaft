package wsdashboard

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// TestCompanionRigStudioBrowser runs headless Chromium end-to-end verification
// of the standalone Control-Rig Studio (/companion/studio?pack=<id>), VRM 0.x
// and VRM 1.0 loading, Target Attach + IK authoring, Save to Pack,
// BroadcastChannel live dashboard playback without reload, sentinel override
// + idle cadence, .vrma import/playback, and Settings "Open Studio" wiring.
// Optional WS_RIG_STUDIO_QA_SHOTS (absolute dir) saves real-VRM visual QA PNGs
// and requires WS_VRM0_SAMPLE and WS_VRM1_SAMPLE to point to real VRM files.
func TestCompanionRigStudioBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_RIG_STUDIO_QA_SHOTS")
	if shots != "" {
		if !filepath.IsAbs(shots) {
			t.Fatal("WS_RIG_STUDIO_QA_SHOTS must be absolute")
		}
		if os.Getenv("WS_VRM0_SAMPLE") == "" || os.Getenv("WS_VRM1_SAMPLE") == "" {
			t.Fatal("WS_RIG_STUDIO_QA_SHOTS requires WS_VRM0_SAMPLE and WS_VRM1_SAMPLE to be set to real VRM files")
		}
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

	vrm0ID := stageVrmPack("vrm0-studio", "VRM 0.x Rig Pack", os.Getenv("WS_VRM0_SAMPLE"), "0.x", buildRenderableSyntheticVRM(false))
	vrm1ID := stageVrmPack("vrm1-studio", "VRM 1.0 Rig Pack", os.Getenv("WS_VRM1_SAMPLE"), "1.0", buildRenderableSyntheticVRM(true))

	// Stage a valid spec-compliant synthetic VRMA file with a real head-rotation
	// animation track so @pixiv/three-vrm-animation produces a playable clip.
	vrmaPath := filepath.Join(t.TempDir(), "dance_demo.vrma")
	var vrmaBin bytes.Buffer
	for _, f32 := range []float32{
		0.0, 1.0, // input times (bytes 0..8)
		0.0, 0.0, 0.0, 1.0, // quat at t=0 (bytes 8..24)
		0.0, 0.258819, 0.0, 0.965926, // quat at t=1 (~30 deg yaw, bytes 24..40)
	} {
		_ = binary.Write(&vrmaBin, binary.LittleEndian, f32)
	}
	vrmaJSON, err := json.Marshal(map[string]any{
		"asset": map[string]any{"version": "2.0"},
		"nodes": []any{
			map[string]any{"name": "hips", "children": []int{1}},
			map[string]any{"name": "head", "translation": []float64{0, 0.6, 0}},
		},
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"scene":  0,
		"buffers": []any{
			map[string]any{"byteLength": vrmaBin.Len()},
		},
		"bufferViews": []any{
			map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": 8},
			map[string]any{"buffer": 0, "byteOffset": 8, "byteLength": 32},
		},
		"accessors": []any{
			map[string]any{"bufferView": 0, "componentType": 5126, "count": 2, "type": "SCALAR", "min": []float64{0}, "max": []float64{1}},
			map[string]any{"bufferView": 1, "componentType": 5126, "count": 2, "type": "VEC4"},
		},
		"animations": []any{
			map[string]any{
				"name": "dance_demo",
				"samplers": []any{
					map[string]any{"input": 0, "interpolation": "LINEAR", "output": 1},
				},
				"channels": []any{
					map[string]any{"sampler": 0, "target": map[string]any{"node": 1, "path": "rotation"}},
				},
			},
		},
		"extensionsUsed": []string{"VRMC_vrm_animation"},
		"extensions": map[string]any{
			"VRMC_vrm_animation": map[string]any{
				"specVersion": "1.0",
				"humanoid": map[string]any{
					"humanBones": map[string]any{
						"hips": map[string]any{"node": 0},
						"head": map[string]any{"node": 1},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	vrmaGLB := buildSyntheticGLB(string(vrmaJSON), vrmaBin.Bytes())
	if err := os.WriteFile(vrmaPath, vrmaGLB, 0o644); err != nil {
		t.Fatal(err)
	}

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
	out, err := exec.CommandContext(ctx, "node", "testdata/companion-rig-studio.browser.cjs", f.base, shots, vrm0ID, vrm1ID, vrmaPath).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium Control-Rig Studio verification: %v", err)
	}
}
