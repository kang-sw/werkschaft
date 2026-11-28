package wsdashboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsreview"
)

// gitPageHistory writes a linear main history of n commits with one merged
// side branch through git fast-import (one process, not one per commit),
// checks it out, and returns the oid of commit frontierAt (1-based, oldest
// first). The fixture's newest commits push that commit past the graph's
// first page, so a jump to it pages the graph.
func gitPageHistory(t *testing.T, root string, n, frontierAt int) string {
	t.Helper()
	gitRun(t, root, "init", "-q", "-b", "main")
	var b strings.Builder
	when := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC).Unix()
	commit := func(ref string, mark int, from, merge int, path, body, msg string) {
		fmt.Fprintf(&b, "commit %s\nmark :%d\n", ref, mark)
		fmt.Fprintf(&b, "author Dev <dev@example.com> %d +0000\ncommitter Dev <dev@example.com> %d +0000\n", when, when)
		when += 600
		fmt.Fprintf(&b, "data %d\n%s\n", len(msg), msg)
		if from > 0 {
			fmt.Fprintf(&b, "from :%d\n", from)
		}
		if merge > 0 {
			fmt.Fprintf(&b, "merge :%d\n", merge)
		}
		fmt.Fprintf(&b, "M 100644 inline %s\ndata %d\n%s\n\n", path, len(body), body)
	}
	const side = 100000
	for i := 1; i <= n; i++ {
		from := i - 1
		if i == frontierAt+5 {
			// A two-commit side branch merged back: the graph draws a lane.
			commit("refs/heads/side", side, from, 0, "side/a.txt", "side a\n", "side: first step")
			commit("refs/heads/side", side+1, side, 0, "side/b.txt", "side b\n", "side: second step")
			commit("refs/heads/main", i, from, side+1, "log.txt", fmt.Sprintf("line %d\n", i), "Merge branch 'side'")
			continue
		}
		commit("refs/heads/main", i, from, 0, "log.txt", fmt.Sprintf("line %d\n", i), fmt.Sprintf("step %d: adjust the log", i))
	}
	cmd := exec.Command("git", "-C", root, "fast-import", "--quiet")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	cmd.Stdin = strings.NewReader(b.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import: %v: %s", err, out)
	}
	gitRun(t, root, "reset", "-q", "--hard", "main")
	out, err := exec.Command("git", "-C", root, "rev-parse", fmt.Sprintf("main~%d", n-frontierAt)).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// TestGitPageBrowser runs the gallery's Git page end to end in cached
// Chromium over a real repository: Dark and Light, a wide (900px) and a
// narrow (380px) gallery, the single full-width graph panel, the dirty
// tree's UNSTAGED row above HEAD and its commit window (Refresh after the
// script writes a file into the fixture, then a clean tree once it commits
// everything), the icon-only strip actions and their busy state, the
// Worktrees popover (light list, manual full scan, its close paths), the
// review pill's jump (which pages the graph to a frontier past its first
// page), Ctrl+G's idempotent show, and the no-repository empty state.
// WS_GITPAGE_SHOTS (absolute) saves the screenshots compared against the
// approved mockup.
func TestGitPageBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	shots := os.Getenv("WS_GITPAGE_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_GITPAGE_SHOTS must be absolute")
	}
	// The isolated HOME below would hide the cached Chromium.
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

	// One daemon per git session: each daemon keeps its own worktree size
	// cache, so every session opens on a cold (light, unsized) list.
	gitFixture := func() (*pageFixture, string) {
		poolDir := t.TempDir()
		acquired := time.Now().Add(-90 * time.Minute).UnixMilli()
		var pool []PooledWorktree
		for i, name := range []string{"amber-heron", "brisk-falcon", "cobalt-ridge", "dusky-maple", "ember-cove"} {
			dir := filepath.Join(poolDir, name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, 2048*(i+1)), 0o644); err != nil {
				t.Fatal(err)
			}
			w := PooledWorktree{Name: name, Path: dir, Profile: "full", Detached: true, ActiveAt: acquired - int64(i)*3600_000}
			if i < 2 {
				w = PooledWorktree{Name: name, Path: dir, Profile: "full", Branch: fmt.Sprintf("impl/develop/2611%02d-feat-part-%d", 20+i, i), Leased: true, Holder: fmt.Sprintf("worker%d", i), Parent: "livesession", AcquiredAt: acquired, ActiveAt: acquired}
			}
			pool = append(pool, w)
		}
		src := &fakeWorktrees{pool: WorktreePool{Pool: poolDir, Worktrees: pool}}
		f := startPageFixture(t, func(o *DaemonOptions) {
			o.Worktrees = src
			o.noGitBadge = false
		})
		frontier := gitPageHistory(t, f.project, 260, 30)
		if err := wsreview.Append(f.project, wsreview.Entry{Base: frontier, Head: frontier, Verdict: wsreview.VerdictPass}); err != nil {
			t.Fatal(err)
		}
		// Changed files for the changes region: staged, unstaged, untracked.
		for path, body := range map[string]string{"log.txt": "line edited\nline added\n", "src/app/main.go": "package main\n", "src/app/util.go": "package main\n\nfunc util() {}\n", "README.md": "# fixture\n"} {
			full := filepath.Join(f.project, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		gitRun(t, f.project, "add", "README.md")
		return f, frontier
	}
	// Sessions in order: dark 900, dark 380, light 900, light 380. The
	// fast-import history is fixed, so every fixture's frontier is the same.
	var bases, projects []string
	var frontier string
	for i := 0; i < 4; i++ {
		f, oid := gitFixture()
		if frontier != "" && oid != frontier {
			t.Fatalf("fixture %d frontier %s, want %s", i, oid, frontier)
		}
		frontier = oid
		bases = append(bases, f.base)
		projects = append(projects, f.project)
	}

	// A daemon over a root that is no repository.
	plain := startPageFixture(t, func(o *DaemonOptions) {
		o.Worktrees = &fakeWorktrees{}
		o.noGitBadge = false
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/gitpage.browser.cjs", strings.Join(bases, ","), strings.Join(projects, ","), plain.base, frontier, shots)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("git page browser: %v", err)
	}
}
