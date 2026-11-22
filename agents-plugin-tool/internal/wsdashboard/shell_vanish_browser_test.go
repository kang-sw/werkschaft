package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A modal opened in a framed page lifts the frame over the shell's rail and
// header; the frame must stay transparent so they never vanish under the
// page's plain background, even while the shell's own close update is late.
// WS_SHELL_VANISH_SHOTS (absolute) keeps the screenshots.
func TestShellVanishBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium and WebKit")
	}
	shots := os.Getenv("WS_SHELL_VANISH_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_SHELL_VANISH_SHOTS must be absolute")
	}
	if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		if h, err := os.UserHomeDir(); err == nil {
			t.Setenv("PLAYWRIGHT_BROWSERS_PATH", filepath.Join(h, "Library", "Caches", "ms-playwright"))
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/shell-vanish.browser.cjs", f.base, shots).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("shell chrome under a framed modal: %v", err)
	}
}
