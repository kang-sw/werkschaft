package wsdashboard

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// Headless Chromium acceptance test for the lazy-loaded VRM companion runtime.
// Runs when WS_PLAYWRIGHT_MODULE is set. Stages self-contained synthetic valid
// VRM 0.x and VRM 1.0 GLBs by default (or optional local untracked fixtures
// via WS_VRM0_SAMPLE / WS_VRM1_SAMPLE) into a temporary config home.
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
		phase = "2"
	}
	out, err := exec.CommandContext(ctx, "node", "testdata/companion-vrm.browser.cjs", f.base, shots, vrm0ID, vrm1ID, phase).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium VRM companion verification: %v", err)
	}
}

// TestCompanionVrmRailDockBrowser verifies the left-rail bottom VRM dock
// layout with a long scrollable session list and pending unregistered
// workspaces: zero dead-zone occlusion at the bottom of #rlist, clean layout
// when details.rws-unreg opens below #rail-companion-slot, complete hide+pause
// below 1600px (1440px 2-col and 720px 1-col), and unclipped FX badges and
// context menu in document.body. Optional WS_COMPANION_RAIL_QA_SHOTS (absolute
// dir) writes the 4 visual QA PNGs.
func TestCompanionVrmRailDockBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_COMPANION_RAIL_QA_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_COMPANION_RAIL_QA_SHOTS must be absolute")
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

	vrm0ID := stageVrmPack("vrm0-rail-qa", "VRM 0 Fixture", os.Getenv("WS_VRM0_SAMPLE"), "0.x", buildRenderableSyntheticVRM(false))
	vrm1ID := stageVrmPack("vrm1-rail-qa", "VRM 1 Fixture", os.Getenv("WS_VRM1_SAMPLE"), "1.0", buildRenderableSyntheticVRM(true))

	wsRoot := t.TempDir()
	wsAlpha := mkDir(t, filepath.Join(wsRoot, "alpha-app"))
	wsBeta := mkDir(t, filepath.Join(wsRoot, "beta-service"))
	unreg1 := mkDir(t, filepath.Join(wsRoot, "scratch-tools"))
	unreg2 := mkDir(t, filepath.Join(wsRoot, "design-sandbox"))
	unreg3 := mkDir(t, filepath.Join(wsRoot, "proto-lab"))
	wsFile := filepath.Join(cfgHome, "dashboard", "workspaces.json")
	if err := os.MkdirAll(filepath.Dir(wsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	wsDoc, err := json.Marshal(workspacesDoc{
		Version:    workspacesSchemaVersion,
		Workspaces: []workspaceRecord{{Path: wsAlpha}, {Path: wsBeta}},
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

	now := time.Now()
	harnesses := []string{"claude", "codex", "pi"}
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
	for i := 1; i <= 10; i++ {
		phase := wslive.PhaseIdle
		if i%4 == 0 {
			phase = wslive.PhaseTool
		}
		addLive(fmt.Sprintf("alpha%02d", i), wsAlpha, harnesses[i%3], phase, time.Duration(30-i)*time.Minute)
	}
	for i := 1; i <= 8; i++ {
		phase := wslive.PhaseIdle
		if i%3 == 0 {
			phase = wslive.PhaseThinking
		}
		addLive(fmt.Sprintf("beta%02d", i), wsBeta, harnesses[(i+1)%3], phase, time.Duration(20-i)*time.Minute)
	}
	addLive("unregs1", unreg1, "claude", wslive.PhaseIdle, 8*time.Minute)
	addLive("unregs2", unreg2, "codex", wslive.PhaseIdle, 6*time.Minute)
	addLive("unregs3", unreg3, "pi", wslive.PhaseIdle, 4*time.Minute)
	f.d.Index().Refresh()
	waitFor(t, "rail dock sessions indexed", func() bool {
		_, ok1 := f.d.Index().Session("beta08")
		_, ok2 := f.d.Index().Session("unregs3")
		return ok1 && ok2
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/companion-vrm-rail-dock.browser.cjs", f.base, shots, vrm0ID, vrm1ID).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium VRM rail dock verification: %v", err)
	}
}

// TestCompanionVrmCoLeadBrowser verifies the Co-Lead seated laptop-typing FK
// pose, beveled foreground laptop framing invariant (bottom <=19%, strictly
// below chin), screen bounce glow modulation, and hover/periodic glance-up
// choreography on both VRM 1.0 and VRM 0.x inside the 1680x960 left-rail dock.
// Optional WS_COMPANION_COLEAD_QA_SHOTS (absolute dir) writes the 4 QA sheets.
func TestCompanionVrmCoLeadBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_COMPANION_COLEAD_QA_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_COMPANION_COLEAD_QA_SHOTS must be absolute")
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

	vrm0ID := stageVrmPack("vrm0-colead", "VRM 0 Fixture", os.Getenv("WS_VRM0_SAMPLE"), "0.x", buildRenderableSyntheticVRM(false))
	vrm1ID := stageVrmPack("vrm1-colead", "VRM 1 Fixture", os.Getenv("WS_VRM1_SAMPLE"), "1.0", buildRenderableSyntheticVRM(true))

	wsRoot := t.TempDir()
	wsAlpha := mkDir(t, filepath.Join(wsRoot, "alpha-app"))
	wsFile := filepath.Join(cfgHome, "dashboard", "workspaces.json")
	if err := os.MkdirAll(filepath.Dir(wsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	wsDoc, err := json.Marshal(workspacesDoc{
		Version:    workspacesSchemaVersion,
		Workspaces: []workspaceRecord{{Path: wsAlpha}},
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
	waitFor(t, "colead sessions indexed", func() bool {
		_, ok := f.d.Index().Session("alpha05")
		return ok
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/companion-vrm-colead.browser.cjs", f.base, shots, vrm0ID, vrm1ID).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium VRM Co-Lead verification: %v", err)
	}
}

// TestCompanionVrmSwapDizzyBrowser verifies Step 3 Gesture #2: live session-switch
// laptop swap (deterministic 6-color palette, smooth tuck-out/slide-in, strict
// bottom framing invariant) and rapid-switch dizzy easter egg (4 rapid clicks in
// left rail -> cute sway + crisp 2D anime spiral/stars FX -> blink-shake & smile
// recovery) on both VRM 1.0 and VRM 0.x inside the 1680x960 left-rail dock.
// Optional WS_COMPANION_STEP3_QA_SHOTS (absolute dir) writes the 4 QA sheets.
func TestCompanionVrmSwapDizzyBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_COMPANION_STEP3_QA_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_COMPANION_STEP3_QA_SHOTS must be absolute")
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

	vrm0ID := stageVrmPack("vrm0-step3", "VRM 0 Fixture", os.Getenv("WS_VRM0_SAMPLE"), "0.x", buildRenderableSyntheticVRM(false))
	vrm1ID := stageVrmPack("vrm1-step3", "VRM 1 Fixture", os.Getenv("WS_VRM1_SAMPLE"), "1.0", buildRenderableSyntheticVRM(true))

	wsRoot := t.TempDir()
	wsAlpha := mkDir(t, filepath.Join(wsRoot, "alpha-app"))
	wsFile := filepath.Join(cfgHome, "dashboard", "workspaces.json")
	if err := os.MkdirAll(filepath.Dir(wsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	wsDoc, err := json.Marshal(workspacesDoc{
		Version:    workspacesSchemaVersion,
		Workspaces: []workspaceRecord{{Path: wsAlpha}},
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

	now := time.Now()
	harnesses := []string{"claude", "codex", "pi"}
	for i := 1; i <= 8; i++ {
		rid := fmt.Sprintf("alpha%02d", i)
		h := harnesses[i%len(harnesses)]
		reg := Registration{
			ReplyID:      rid,
			Project:      wsAlpha,
			Harness:      h,
			RegisteredAt: stampFor(now.Add(-time.Duration(25-i) * time.Minute)),
			HeartbeatAt:  stampFor(now),
		}
		if err := WriteRegistration(f.dir, reg); err != nil {
			t.Fatal(err)
		}
		writeSnapshot(t, f.dir, rid, &wslive.Snapshot{
			SchemaVersion: 1,
			Adapter:       h,
			UpdatedAt:     now.UnixMilli(),
			HeartbeatMs:   30000,
			Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}},
		})
		f.src.setPage(rid, PageContent{HasBoard: true, Seq: 1, Board: boardRegions(t, fmt.Sprintf("session %s board", rid))})
	}
	f.d.Index().Refresh()
	waitFor(t, "step3 sessions indexed", func() bool {
		_, ok := f.d.Index().Session("alpha08")
		return ok
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/companion-vrm-swap-dizzy.browser.cjs", f.base, shots, vrm0ID, vrm1ID).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium VRM Step 3 swap+dizzy verification: %v", err)
	}
}

// buildRenderableSyntheticVRM builds a compact, spec-valid GLB v2 containing
// stylized 3D bust meshes (dark hoodie torso, skin head with morph target,
// dark hair cap, and sleeves) plus all 17 mandatory humanoid bones and valid
// VRMC_vrm (1.0) or VRM (0.x) metadata so @pixiv/three-vrm renders it cleanly
// in WebGL2 without any third-party assets.
func buildRenderableSyntheticVRM(vrm1 bool) []byte {
	var bin bytes.Buffer
	type bv struct {
		Buffer     int `json:"buffer"`
		ByteOffset int `json:"byteOffset"`
		ByteLength int `json:"byteLength"`
	}
	type acc struct {
		BufferView    int       `json:"bufferView"`
		ComponentType int       `json:"componentType"`
		Count         int       `json:"count"`
		Type          string    `json:"type"`
		Min           []float32 `json:"min,omitempty"`
		Max           []float32 `json:"max,omitempty"`
	}
	var views []bv
	var accessors []acc

	appendFloats := func(vals []float32, kind string, withMinMax bool) int {
		for bin.Len()%4 != 0 {
			bin.WriteByte(0)
		}
		off := bin.Len()
		for _, v := range vals {
			_ = binary.Write(&bin, binary.LittleEndian, v)
		}
		bvIdx := len(views)
		views = append(views, bv{Buffer: 0, ByteOffset: off, ByteLength: len(vals) * 4})
		a := acc{BufferView: bvIdx, ComponentType: 5126, Type: kind}
		stride := 3
		if kind == "SCALAR" {
			stride = 1
		}
		a.Count = len(vals) / stride
		if withMinMax && stride == 3 && len(vals) >= 3 {
			mn := []float32{vals[0], vals[1], vals[2]}
			mx := []float32{vals[0], vals[1], vals[2]}
			for i := 3; i < len(vals); i += 3 {
				for k := 0; k < 3; k++ {
					if vals[i+k] < mn[k] {
						mn[k] = vals[i+k]
					}
					if vals[i+k] > mx[k] {
						mx[k] = vals[i+k]
					}
				}
			}
			a.Min, a.Max = mn, mx
		}
		idx := len(accessors)
		accessors = append(accessors, a)
		return idx
	}

	appendIndices := func(vals []uint16) int {
		for bin.Len()%4 != 0 {
			bin.WriteByte(0)
		}
		off := bin.Len()
		for _, v := range vals {
			_ = binary.Write(&bin, binary.LittleEndian, v)
		}
		bvIdx := len(views)
		views = append(views, bv{Buffer: 0, ByteOffset: off, ByteLength: len(vals) * 2})
		idx := len(accessors)
		accessors = append(accessors, acc{
			BufferView: bvIdx, ComponentType: 5123, Count: len(vals), Type: "SCALAR",
		})
		return idx
	}

	makeBox := func(hx, hy, hz, ox, oy, oz float32, mat int, withMorph bool) map[string]any {
		faces := [][4][3]float32{
			{{-hx, -hy, hz}, {hx, -hy, hz}, {hx, hy, hz}, {-hx, hy, hz}},     // +Z
			{{hx, -hy, -hz}, {-hx, -hy, -hz}, {-hx, hy, -hz}, {hx, hy, -hz}}, // -Z
			{{-hx, hy, hz}, {hx, hy, hz}, {hx, hy, -hz}, {-hx, hy, -hz}},     // +Y
			{{-hx, -hy, -hz}, {hx, -hy, -hz}, {hx, -hy, hz}, {-hx, -hy, hz}}, // -Y
			{{hx, -hy, hz}, {hx, -hy, -hz}, {hx, hy, -hz}, {hx, hy, hz}},     // +X
			{{-hx, -hy, -hz}, {-hx, -hy, hz}, {-hx, hy, hz}, {-hx, hy, -hz}}, // -X
		}
		normals := [][3]float32{
			{0, 0, 1}, {0, 0, -1}, {0, 1, 0}, {0, -1, 0}, {1, 0, 0}, {-1, 0, 0},
		}
		var pos, norm, morphDelta []float32
		var idx []uint16
		for fi, f := range faces {
			base := uint16(fi * 4)
			for _, v := range f {
				pos = append(pos, v[0]+ox, v[1]+oy, v[2]+oz)
				norm = append(norm, normals[fi][0], normals[fi][1], normals[fi][2])
				if withMorph {
					morphDelta = append(morphDelta, 0, 0.008*float32(math.Copysign(1, float64(v[1]))), 0.005)
				}
			}
			idx = append(idx, base, base+1, base+2, base, base+2, base+3)
		}
		prim := map[string]any{
			"attributes": map[string]any{
				"POSITION": appendFloats(pos, "VEC3", true),
				"NORMAL":   appendFloats(norm, "VEC3", false),
			},
			"indices":  appendIndices(idx),
			"material": mat,
		}
		if withMorph {
			prim["targets"] = []any{
				map[string]any{
					"POSITION": appendFloats(morphDelta, "VEC3", true),
				},
			}
		}
		return map[string]any{"primitives": []any{prim}}
	}

	materials := []any{
		map[string]any{"name": "hoodie", "pbrMetallicRoughness": map[string]any{"baseColorFactor": []float32{0.16, 0.19, 0.27, 1}, "metallicFactor": 0.05, "roughnessFactor": 0.85}},
		map[string]any{"name": "skin", "pbrMetallicRoughness": map[string]any{"baseColorFactor": []float32{0.96, 0.84, 0.76, 1}, "metallicFactor": 0.0, "roughnessFactor": 0.75}},
		map[string]any{"name": "hair", "pbrMetallicRoughness": map[string]any{"baseColorFactor": []float32{0.14, 0.16, 0.24, 1}, "metallicFactor": 0.1, "roughnessFactor": 0.7}},
		map[string]any{"name": "sleeve", "pbrMetallicRoughness": map[string]any{"baseColorFactor": []float32{0.20, 0.24, 0.34, 1}, "metallicFactor": 0.05, "roughnessFactor": 0.82}},
	}

	meshes := []any{
		makeBox(0.14, 0.16, 0.09, 0, 0.02, 0, 0, false),    // 0: torso hoodie
		makeBox(0.095, 0.105, 0.095, 0, 0.07, 0, 1, true),  // 1: head with morph target
		makeBox(0.105, 0.055, 0.105, 0, 0.14, 0, 2, false), // 2: hair crown
		makeBox(0.08, 0.04, 0.04, 0, 0, 0, 3, false),       // 3: arm segment
	}

	// VRM 1.0 faces +Z (+X is character left); VRM 0.x faces -Z (-X is character left).
	lSign := float32(1)
	if !vrm1 {
		lSign = -1
	}

	nodes := []any{
		map[string]any{"name": "root", "children": []int{1}},                                                                          // 0
		map[string]any{"name": "hips", "translation": []float32{0, 0.90, 0}, "children": []int{2, 13, 16}},                            // 1
		map[string]any{"name": "spine", "translation": []float32{0, 0.10, 0}, "children": []int{3}},                                   // 2
		map[string]any{"name": "chest", "translation": []float32{0, 0.15, 0}, "mesh": 0, "children": []int{4}},                        // 3
		map[string]any{"name": "upperChest", "translation": []float32{0, 0.10, 0}, "children": []int{5, 7, 10}},                       // 4
		map[string]any{"name": "neck", "translation": []float32{0, 0.10, 0}, "children": []int{6}},                                    // 5
		map[string]any{"name": "head", "translation": []float32{0, 0.08, 0}, "mesh": 1, "children": []int{19}},                        // 6
		map[string]any{"name": "leftUpperArm", "translation": []float32{lSign * 0.16, 0.04, 0}, "mesh": 3, "children": []int{8}},      // 7
		map[string]any{"name": "leftLowerArm", "translation": []float32{lSign * 0.18, 0, 0}, "mesh": 3, "children": []int{9}},         // 8
		map[string]any{"name": "leftHand", "translation": []float32{lSign * 0.16, 0, 0}},                                              // 9
		map[string]any{"name": "rightUpperArm", "translation": []float32{-lSign * 0.16, 0.04, 0}, "mesh": 3, "children": []int{11}},   // 10
		map[string]any{"name": "rightLowerArm", "translation": []float32{-lSign * 0.18, 0, 0}, "mesh": 3, "children": []int{12}},      // 11
		map[string]any{"name": "rightHand", "translation": []float32{-lSign * 0.16, 0, 0}},                                            // 12
		map[string]any{"name": "leftUpperLeg", "translation": []float32{lSign * 0.09, -0.05, 0}, "children": []int{14}},               // 13
		map[string]any{"name": "leftLowerLeg", "translation": []float32{0, -0.40, 0}, "children": []int{15}},                          // 14
		map[string]any{"name": "leftFoot", "translation": []float32{0, -0.40, 0}},                                                     // 15
		map[string]any{"name": "rightUpperLeg", "translation": []float32{-lSign * 0.09, -0.05, 0}, "children": []int{17}},             // 16
		map[string]any{"name": "rightLowerLeg", "translation": []float32{0, -0.40, 0}, "children": []int{18}},                         // 17
		map[string]any{"name": "rightFoot", "translation": []float32{0, -0.40, 0}},                                                    // 18
		map[string]any{"name": "hairCap", "mesh": 2},                                                                                  // 19
	}

	boneIdx := map[string]int{
		"hips": 1, "spine": 2, "chest": 3, "upperChest": 4, "neck": 5, "head": 6,
		"leftUpperArm": 7, "leftLowerArm": 8, "leftHand": 9,
		"rightUpperArm": 10, "rightLowerArm": 11, "rightHand": 12,
		"leftUpperLeg": 13, "leftLowerLeg": 14, "leftFoot": 15,
		"rightUpperLeg": 16, "rightLowerLeg": 17, "rightFoot": 18,
	}

	// Include VRM humanoid finger bones on VRM 1.0 and omit them on VRM 0.x so
	// live Chromium tests exercise both finger presets and missing-finger fallback.
	if vrm1 {
		addHandFingers := func(side string, handNodeIdx int, sign float32) {
			handNode := nodes[handNodeIdx].(map[string]any)
			var handChildren []int
			chains := [][3]string{
				{"ThumbMetacarpal", "ThumbProximal", "ThumbDistal"},
				{"IndexProximal", "IndexIntermediate", "IndexDistal"},
				{"MiddleProximal", "MiddleIntermediate", "MiddleDistal"},
				{"RingProximal", "RingIntermediate", "RingDistal"},
				{"LittleProximal", "LittleIntermediate", "LittleDistal"},
			}
			for ci, chain := range chains {
				zOff := float32(0.015 - float32(ci)*0.008)
				prev := -1
				for ji, seg := range chain {
					idx := len(nodes)
					tx := sign * 0.025
					tz := float32(0)
					if ji == 0 {
						tx = sign * 0.045
						tz = zOff
					}
					nodes = append(nodes, map[string]any{
						"name":        side + seg,
						"translation": []float32{tx, 0, tz},
					})
					boneIdx[side+seg] = idx
					if prev < 0 {
						handChildren = append(handChildren, idx)
					} else {
						nodes[prev].(map[string]any)["children"] = []int{idx}
					}
					prev = idx
				}
			}
			handNode["children"] = handChildren
		}
		addHandFingers("left", 9, lSign)
		addHandFingers("right", 12, -lSign)
	}

	gltf := map[string]any{
		"asset":       map[string]any{"version": "2.0", "generator": "ws-synthetic-vrm-test"},
		"scene":       0,
		"scenes":      []any{map[string]any{"nodes": []int{0}}},
		"nodes":       nodes,
		"meshes":      meshes,
		"materials":   materials,
		"buffers":     []any{map[string]any{"byteLength": bin.Len()}},
		"bufferViews": views,
		"accessors":   accessors,
	}

	if vrm1 {
		humanBones := map[string]any{}
		for name, n := range boneIdx {
			humanBones[name] = map[string]any{"node": n}
		}
		mkExpr := func(withBind bool) map[string]any {
			binds := []any{}
			if withBind {
				binds = append(binds, map[string]any{"node": 6, "index": 0, "weight": 1.0})
			}
			return map[string]any{"morphTargetBinds": binds}
		}
		gltf["extensionsUsed"] = []string{"VRMC_vrm"}
		gltf["extensions"] = map[string]any{
			"VRMC_vrm": map[string]any{
				"specVersion": "1.0",
				"meta": map[string]any{
					"name":                           "Synthetic VRM 1.0",
					"version":                        "1.0",
					"authors":                        []string{"ws-test"},
					"licenseUrl":                     "https://vrm.dev/licenses/1.0/",
					"avatarPermission":               "everyone",
					"allowExcessivelyViolentUsage":   false,
					"allowExcessivelySexualUsage":    false,
					"commercialUsage":                "personalNonProfit",
					"allowPoliticalOrReligiousUsage": false,
					"allowAntisocialOrHateUsage":     false,
					"creditNotation":                 "unnecessary",
					"allowRedistribution":            false,
					"modification":                   "prohibited",
				},
				"humanoid": map[string]any{"humanBones": humanBones},
				"expressions": map[string]any{
					"preset": map[string]any{
						"neutral":   mkExpr(false),
						"happy":     mkExpr(true),
						"sad":       mkExpr(true),
						"angry":     mkExpr(true),
						"relaxed":   mkExpr(true),
						"surprised": mkExpr(true),
						"blink":     mkExpr(true),
						"oh":        mkExpr(true),
					},
				},
				"lookAt": map[string]any{
					"offsetFromHeadBone":      []float32{0, 0.06, 0},
					"type":                    "bone",
					"rangeMapHorizontalInner": map[string]any{"inputMaxValue": 90, "outputScale": 10},
					"rangeMapHorizontalOuter": map[string]any{"inputMaxValue": 90, "outputScale": 10},
					"rangeMapVerticalDown":    map[string]any{"inputMaxValue": 90, "outputScale": 10},
					"rangeMapVerticalUp":      map[string]any{"inputMaxValue": 90, "outputScale": 10},
				},
			},
		}
	} else {
		var humanBones []any
		for name, n := range boneIdx {
			humanBones = append(humanBones, map[string]any{
				"bone": name, "node": n, "useDefaultValues": true,
			})
		}
		mkGroup := func(name, preset string, withBind bool) map[string]any {
			binds := []any{}
			if withBind {
				binds = append(binds, map[string]any{"mesh": 1, "index": 0, "weight": 100})
			}
			return map[string]any{"name": name, "presetName": preset, "binds": binds}
		}
		curve := []float32{0, 0, 0, 1, 1, 1, 1, 0}
		gltf["extensionsUsed"] = []string{"VRM"}
		gltf["extensions"] = map[string]any{
			"VRM": map[string]any{
				"exporterVersion": "ws-synthetic-vrm-test",
				"specVersion":     "0.0",
				"meta":            map[string]any{"title": "Synthetic VRM 0.x", "version": "0.0", "author": "ws-test"},
				"humanoid":        map[string]any{"humanBones": humanBones},
				"blendShapeMaster": map[string]any{
					"blendShapeGroups": []any{
						mkGroup("Neutral", "neutral", false),
						mkGroup("Joy", "joy", true),
						mkGroup("Sorrow", "sorrow", true),
						mkGroup("Angry", "angry", true),
						mkGroup("Fun", "fun", true),
						mkGroup("Blink", "blink", true),
						mkGroup("O", "o", true),
					},
				},
				"firstPerson": map[string]any{
					"firstPersonBone":       6,
					"firstPersonBoneOffset": map[string]any{"x": 0, "y": 0.06, "z": 0},
					"lookAtTypeName":        "Bone",
					"lookAtHorizontalInner": map[string]any{"curve": curve, "xRange": 90, "yRange": 10},
					"lookAtHorizontalOuter": map[string]any{"curve": curve, "xRange": 90, "yRange": 10},
					"lookAtVerticalDown":    map[string]any{"curve": curve, "xRange": 90, "yRange": 10},
					"lookAtVerticalUp":      map[string]any{"curve": curve, "xRange": 90, "yRange": 10},
				},
			},
		}
	}

	rawJSON, err := json.Marshal(gltf)
	if err != nil {
		panic(err)
	}
	return buildSyntheticGLB(string(rawJSON), bin.Bytes())
}
