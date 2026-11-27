package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

// TestBoardHoldHeaderOverviewRailBrowser runs live Chromium verification of
// Phase 2 topic header open/answered/held chips, Overview placeholder live
// sync, zero localStorage hold access, and left rail .qdot across session
// switches in both dark and light themes.
func TestBoardHoldHeaderOverviewRailBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	shots := os.Getenv("WS_BOARD_HOLD_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_BOARD_HOLD_SHOTS must be absolute")
	}
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	t.Setenv("WS_MCP_DASHBOARD_DEV_SOURCE", "")
	SetDevAssets(nil)

	b1 := newTestBoardDB(t, `
<wb:topic name="design">
  <wb:add class="policy"><wb:title>Pick cache tier</wb:title><wb:option>Redis</wb:option><wb:option>SQLite</wb:option><wb:recommend>a: low latency</wb:recommend></wb:add>
  <wb:add class="policy"><wb:title>Select retention window</wb:title><wb:option>7 days</wb:option><wb:option>30 days</wb:option><wb:recommend>a: compact</wb:recommend></wb:add>
  <wb:add class="policy"><wb:title>Confirm retry backoff</wb:title><wb:option>Linear</wb:option><wb:option>Exponential</wb:option><wb:recommend>b: safer under load</wb:recommend></wb:add>
  <wb:add class="check"><wb:title>Verify backup restore</wb:title></wb:add>
  <wb:add class="technical"><wb:title>Ack schema note</wb:title></wb:add>
</wb:topic>
<wb:topic name="ops">
  <wb:add class="policy"><wb:title>Confirm deploy window</wb:title><wb:option>Morning</wb:option><wb:option>Evening</wb:option></wb:add>
</wb:topic>
`)
	b2 := newTestBoardDB(t, `
<wb:topic name="triage">
  <wb:add class="policy"><wb:title>Second session sole ask</wb:title><wb:option>Proceed</wb:option><wb:option>Defer</wb:option></wb:add>
</wb:topic>
`)
	_, base, _ := startHoldDaemonFixture(t, map[string]*wswhiteboard.BoardDB{
		"sessone": b1,
		"sesstwo": b2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/board-hold-header-overview-rail.browser.cjs", base, shots).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("board hold header/overview/rail browser: %v", err)
	}
}
