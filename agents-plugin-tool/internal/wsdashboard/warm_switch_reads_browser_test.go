package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestWarmSwitchReadsBrowser checks in Chromium that the region updates a
// hidden framed page held apply on its show with one scroll read phase, not
// one per region.
func TestWarmSwitchReadsBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	out, err := exec.CommandContext(ctx, "node", "testdata/warm-switch-reads.browser.cjs", f.base).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("warm switch reads Chromium acceptance: %v", err)
	}
}
