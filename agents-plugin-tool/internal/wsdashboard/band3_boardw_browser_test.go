package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestBand3BoardWidthDoesNotLeakBrowser verifies in headless Chromium that a
// persisted Band 4 board width (bb / data-boardw) does not shrink or distort
// Band 3 (1660px and 1920px), keeps .dcv-edge resizing within the constant
// Band 3 stage while preserving bb, and still applies bb when widened to Band 4
// (2560px). WS_BAND3_BOARDW_SHOTS, when set to an absolute directory, writes
// verification PNG captures there.
func TestBand3BoardWidthDoesNotLeakBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE for Chromium acceptance")
	}
	shots := os.Getenv("WS_BAND3_BOARDW_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_BAND3_BOARDW_SHOTS must be absolute")
	}
	if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		if realHome, err := os.UserHomeDir(); err == nil && realHome != "" {
			for _, candidate := range []string{
				filepath.Join(realHome, "Library", "Caches", "ms-playwright"),
				filepath.Join(realHome, ".cache", "ms-playwright"),
			} {
				if info, err := os.Stat(candidate); err == nil && info.IsDir() {
					t.Setenv("PLAYWRIGHT_BROWSERS_PATH", candidate)
					break
				}
			}
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	SetDevAssets(nil)
	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/band3-boardw.browser.cjs", f.base, shots).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium Band 3 boardw acceptance: %v", err)
	}
}
