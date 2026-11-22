package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsholder"
)

// TestColdInitStagingBrowser checks in Chromium that a framed session page
// the shell loads hidden defers the tracker's per-store views and the chat
// tabs' terminal replay to its first show, and that a page that is not
// framed starts them at load.
func TestColdInitStagingBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	SetDevAssets(nil)
	f := startPageFixture(t, withHolders(wsholder.New(wsholder.Options{Dir: filepath.Join(t.TempDir(), wsholder.DirName)})), func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/cold-init-staging.browser.cjs", f.base).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("cold init staging Chromium acceptance: %v", err)
	}
}
