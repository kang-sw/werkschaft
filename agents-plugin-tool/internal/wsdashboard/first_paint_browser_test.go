package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestFirstPaintShellFrameBrowser verifies in Chromium that an uncached
// framed session page inside the shell paints the dark/light theme background
// before external stylesheets finish loading, with no white flash in dark mode.
func TestFirstPaintShellFrameBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	shots := os.Getenv("WS_FIRST_PAINT_SHOTS")
	if shots == "" {
		shots = t.TempDir()
	}
	if !filepath.IsAbs(shots) {
		t.Fatal("WS_FIRST_PAINT_SHOTS must be absolute")
	}
	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/first-paint.browser.cjs", f.base, shots)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("first-paint Chromium acceptance: %v", err)
	}
}
