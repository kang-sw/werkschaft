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

// TestIdeShellPhase3Browser runs the Phase 3 Topbar Zone Split & Final Visual
// Acceptance Playwright checks across 1200x800, 1440x900, and 1920x1080 in Dark
// and Light themes: 36px 3-column topbar (Per-Session Left / Centered Search /
// Global Right), zero overlap & 0px overflow, Top-Right click-through to framed
// tracker handles (.tk-handle/#tkadd) with an aligned tracker slide-out (the
// framed #drhandle retired with the drawer cards), git-backed vs non-git session .worktree-branch telemetry, clean
// non-session pages (Home, Settings), Phase 2 minor polishes (#memo-pad bottom
// clearance & #talert narrow-width cap), and final side-by-side screenshots vs
// the approved Variant D mockup. Set WS_IDE_SHELL_SHOTS (absolute dir) to retain
// visual captures.
func TestIdeShellPhase3Browser(t *testing.T) {
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

	trackerViewJSON := `{"store":{"name":"team"},"sync":{"unpushed":[],"state":"ok","remote":true},"clones":[],"viewer":{"member":"ada","stream":"ada.pc","name":"Ada","admin":true,"followers":[],"reach":{},"isMember":true},"priority":{"names":["Low","Normal","High"],"default":1},"attachments":{"max_bytes":1048576},"labels":[],"orgs":[],"members":{"ada":{"id":"ada","name":"Ada","profile":{}}},"admins":["ada"],"rolesProblems":[],"items":{},"itemOrder":[],"assignments":{},"requests":{},"rules":{},"comments":{},"events":[],"notices":[],"my":{"assignments":[],"today":{"work":[],"pending":[],"confirms":[]},"received":{"pending":[],"done":[],"other":[]},"given":[],"created":[],"team":[],"requests":{"incoming":[],"outgoing":[]},"badge":[],"related":{"cc":[],"mentioned":[],"closed":[]},"ccNotices":[]},"stats":{"events":0,"commits":0,"flagged":0,"concurrent":0},"heads":{},"now":"2026-11-28T05:00:00Z"}`
	f, root, _ := worktreePageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
		o.Tracker = &countingTracker{fakeTracker: newFakeTracker(), raw: trackerViewJSON}
	})
	now := time.Now()

	// Git-backed primary session (gitsess) + sibling sessions in the same git repo.
	for i := 0; i < 6; i++ {
		id := "gitsess"
		if i > 0 {
			id = fmt.Sprintf("sess%02d", i)
		}
		reg := register(t, f.dir, id, root, now.Add(-time.Duration(i)*time.Minute))
		if i%2 == 1 {
			reg.Harness = "codex"
		} else {
			reg.Harness = "claude"
		}
		if err := WriteRegistration(f.dir, reg); err != nil {
			t.Fatal(err)
		}
		writeSnapshot(t, f.dir, id, &wslive.Snapshot{
			SchemaVersion: 1,
			Adapter:       reg.Harness,
			UpdatedAt:     now.UnixMilli(),
			HeartbeatMs:   30000,
			Repo:          &wslive.Repo{Branch: "feat/ide-shell"},
			Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}},
		})
		f.src.setPage(id, PageContent{HasBoard: true, Seq: 1, Board: boardRegions(t, id)})
	}

	// Non-git-backed session (nongitsess) in a plain temporary directory.
	nonGitDir := t.TempDir()
	nonGitReg := register(t, f.dir, "nongitsess", nonGitDir, now.Add(-10*time.Minute))
	nonGitReg.Harness = "codex"
	if err := WriteRegistration(f.dir, nonGitReg); err != nil {
		t.Fatal(err)
	}
	writeSnapshot(t, f.dir, "nongitsess", &wslive.Snapshot{
		SchemaVersion: 1,
		Adapter:       "codex",
		UpdatedAt:     now.UnixMilli(),
		HeartbeatMs:   30000,
		Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}},
	})
	f.src.setPage("nongitsess", PageContent{HasBoard: true, Seq: 1, Board: boardRegions(t, "nongitsess")})

	f.d.Index().Refresh()
	waitFor(t, "gitsess indexed", func() bool { _, ok := f.d.Index().Session("gitsess"); return ok })
	waitFor(t, "nongitsess indexed", func() bool { _, ok := f.d.Index().Session("nongitsess"); return ok })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/ide-shell-phase3.browser.cjs", f.base, shots, mockup).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium Phase 3 IDE shell check: %v", err)
	}
}
