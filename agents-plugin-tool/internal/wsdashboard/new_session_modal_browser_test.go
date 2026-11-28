package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestNewSessionModalBrowser verifies the redesigned v3 New Session modal in
// headless Chromium across dark/light themes and wide (1280px) / narrow (480px)
// viewports: searchable Named Tier dropdown, editable Model combobox + custom
// input, horizontal drag Effort knob + keyboard + reset, permission capsules +
// collapsed Advanced native disclosure + danger CTA states, backdrop/Escape/×
// dismissal, and wide Model vs Effort rect alignment (<= 1px).
// WS_NEW_SESSION_SHOTS=<abs dir> saves screenshots for visual inspection.
func TestNewSessionModalBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_NEW_SESSION_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_NEW_SESSION_SHOTS must be absolute")
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
		o.Agents = &guideAgents{}
		o.Worktrees = permUIWorktrees{}
	})
	repo := filepath.Join(t.TempDir(), "devenv")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	register(t, f.dir, "reposession", repo, time.Now())
	f.d.Index().Refresh()
	waitFor(t, "the repository session to be indexed", func() bool {
		_, ok := f.d.Index().Session("reposession")
		return ok
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/new-session-modal.browser.cjs", f.base, shots)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("new session modal in Chromium: %v", err)
	}
}
