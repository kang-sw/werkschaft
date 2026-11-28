package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestHoldersPopupBrowser runs the shell's bottom-bar Holders trigger and its
// upward popup in Chromium across Dark/Light x 1440px/1180px and the
// warning, healthy, empty, no-frame, and incomplete scenarios: the trigger's
// state and count, the popup standing above the bar inside the viewport
// without overflow, the 1.5s hold on an unattached holder's Stop (a short
// press only explains it), the inline terminal confirmation and Escape's
// order (confirmation, then popup), and the bottom-bar popovers excluding
// each other. The listings are the page's own /holders and terminal answers,
// fulfilled per scenario by the browser. WS_HOLDERS_POPUP_SHOTS (absolute)
// keeps the captures beside the approved mockup's.
func TestHoldersPopupBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_HOLDERS_POPUP_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_HOLDERS_POPUP_SHOTS must be absolute")
	}
	mockup, err := filepath.Abs(filepath.Join("..", "..", "..", "ai-docs", "assets", "mockups", "261128", "global-holders-popup", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mockup); err != nil {
		mockup = ""
	}
	// The disposable HOME below would hide the cached browsers.
	if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		if real, err := os.UserHomeDir(); err == nil {
			for _, c := range []string{filepath.Join(real, "Library", "Caches", "ms-playwright"), filepath.Join(real, ".cache", "ms-playwright")} {
				if _, err := os.Stat(c); err == nil {
					t.Setenv("PLAYWRIGHT_BROWSERS_PATH", c)
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
		o.Holders = &stateFilterTerminalHost{newFakeHolders()}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/holders-popup.browser.cjs", f.base, shots, mockup).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium holders popup: %v", err)
	}
}
