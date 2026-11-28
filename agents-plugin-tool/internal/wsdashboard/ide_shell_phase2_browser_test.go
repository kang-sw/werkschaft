package wsdashboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// TestIdeShellPhase2Browser runs the Phase 2 Global Bottom Status Bar
// Playwright checks across 1200x800, 1440x900, and 1920x1080 in Dark and Light
// themes: upward-clamped #gmenu and .usage-toggle account switcher popovers,
// mutual exclusion, Home / global settings / #gear reachability below 1600px
// with a single #gear and retired .rfoot, #talert empty & active states with
// 0px cluster overlap, data-fdim modal dimming & click-blocking, 0px
// horizontal overflow, #rail-companion-slot 0px jitter, and non-session pages
// (Home, Settings, Worktree). Set WS_IDE_SHELL_SHOTS (absolute dir) to retain
// visual captures.
func TestIdeShellPhase2Browser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_IDE_SHELL_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_IDE_SHELL_SHOTS must be absolute")
	}
	if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			t.Setenv("PLAYWRIGHT_BROWSERS_PATH", filepath.Join(home, "Library", "Caches", "ms-playwright"))
		}
	}
	mockup, err := filepath.Abs(filepath.Join("..", "..", "..", "ai-docs", "assets", "mockups", "261127", "dashboard-shell-d-layout", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mockup); err != nil {
		mockup = ""
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	SetDevAssets(nil)

	f, root, _ := worktreePageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
	})
	now := time.Now()
	for i := 0; i < 16; i++ {
		id := fmt.Sprintf("sess%02d", i)
		reg := register(t, f.dir, id, root, now.Add(-time.Duration(i)*time.Minute))
		if err := WriteRegistration(f.dir, reg); err != nil {
			t.Fatal(err)
		}
		writeSnapshot(t, f.dir, id, &wslive.Snapshot{
			SchemaVersion: 1,
			Adapter:       "claude",
			UpdatedAt:     now.UnixMilli(),
			HeartbeatMs:   30000,
			Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}},
		})
		f.src.setPage(id, PageContent{HasBoard: true, Seq: 1, Board: boardRegions(t, id)})
	}
	for i := 0; i < 4; i++ {
		unregDir := filepath.Join(t.TempDir(), fmt.Sprintf("unreg-ws-%02d", i))
		if err := os.MkdirAll(unregDir, 0o755); err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("unreg%02d", i)
		reg := register(t, f.dir, id, unregDir, now.Add(-time.Duration(i+1)*time.Minute))
		if err := WriteRegistration(f.dir, reg); err != nil {
			t.Fatal(err)
		}
	}
	f.d.Index().Refresh()
	waitFor(t, "sess00 indexed", func() bool { _, ok := f.d.Index().Session("sess00"); return ok })

	wtRoute := worktreePagePath(root)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/ide-shell-phase2.browser.cjs", f.base, wtRoute, shots, mockup).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium Phase 2 IDE shell check: %v", err)
	}
}
