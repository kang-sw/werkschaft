package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRailWsbarGearLayoutBrowser verifies in headless Chromium that the
// workspace bar (.wsbar) orders its right-edge cluster as [gear] [memo-count]
// [ct], keeps [memo-count] and [ct] adjacent at the right edge both with and
// without data-can-control, reveals the gear on hover with zero horizontal
// shift, and produces zero console/page errors or overflow.
// WS_RAIL_WSBAR_SHOTS, when set to an absolute directory, writes idle/hover
// dark/light PNG captures there.
func TestRailWsbarGearLayoutBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	shots := os.Getenv("WS_RAIL_WSBAR_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_RAIL_WSBAR_SHOTS must be absolute")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	SetDevAssets(nil)
	f := startPageFixture(t, withWorkspaces(t), func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
		o.Tracker = &fakeTracker{}
	})
	// Add a second short-named folder workspace with 0 live sessions so the test
	// covers both a multi-session workspace and a short path with idle 0 counts.
	shortWS := filepath.Join(t.TempDir(), "my-app")
	if err := os.MkdirAll(shortWS, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.workspaces.add(f.project, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.workspaces.add(shortWS, false); err != nil {
		t.Fatal(err)
	}
	f.d.Index().Refresh()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/rail-wsbar.browser.cjs", f.base, shots, shortWS)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium rail wsbar acceptance: %v", err)
	}
}
