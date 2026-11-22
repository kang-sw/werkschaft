package werk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func call(t *testing.T, f *Files, tool string, args any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	for _, tl := range f.Tools(nil) {
		if tl.Def.Name == tool {
			return tl.Run(context.Background(), raw)
		}
	}
	t.Fatalf("no tool %s", tool)
	return "", nil
}

func newTree(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	must(t, os.MkdirAll(filepath.Join(root, "src", "deep"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("package a\n\nfunc One() int { return 1 }\nfunc Two() int { return 2 }\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "src", "deep", "b.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644))
	return root
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Authority tests, one per tool: read, grep and glob are open at every level;
// edit and write are refused read-only, confined to the worktree for
// workdir-write, and open for full.
func TestAuthorityPerTool(t *testing.T) {
	root := newTree(t)
	outside := t.TempDir()
	must(t, os.WriteFile(filepath.Join(outside, "o.txt"), []byte("outside\n"), 0o644))

	for _, level := range []Authority{AuthorityReadOnly, AuthorityWorkdirWrite, AuthorityFull} {
		f := &Files{Root: root, Cwd: root, Authority: level}
		t.Run(string(level)+"/reads", func(t *testing.T) {
			// Reads are not confined, whatever the level.
			if out, err := call(t, f, "read", map[string]any{"path": filepath.Join(outside, "o.txt")}); err != nil || !strings.Contains(out, "outside") {
				t.Errorf("read outside = %q, %v", out, err)
			}
			if out, err := call(t, f, "grep", map[string]any{"pattern": "gamma", "path": filepath.Join(root, "src")}); err != nil || !strings.Contains(out, "deep/b.txt:3:gamma") {
				t.Errorf("grep = %q, %v", out, err)
			}
			if out, err := call(t, f, "glob", map[string]any{"pattern": "**/*.go"}); err != nil || strings.TrimSpace(out) != "src/a.go" {
				t.Errorf("glob = %q, %v", out, err)
			}
		})
	}

	t.Run("read-only refuses edit and write", func(t *testing.T) {
		f := &Files{Root: root, Cwd: root, Authority: AuthorityReadOnly}
		if _, err := call(t, f, "edit", map[string]any{"path": "src/a.go", "edits": []map[string]string{{"oldText": "One", "newText": "Uno"}}}); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("edit err = %v", err)
		}
		if _, err := call(t, f, "write", map[string]any{"path": "src/new.txt", "content": "x"}); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("write err = %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "src", "new.txt")); err == nil {
			t.Error("a refused write created the file")
		}
		if b, _ := os.ReadFile(filepath.Join(root, "src", "a.go")); !strings.Contains(string(b), "One") {
			t.Error("a refused edit changed the file")
		}
	})

	t.Run("workdir-write confines to the worktree", func(t *testing.T) {
		f := &Files{Root: root, Cwd: filepath.Join(root, "src"), Authority: AuthorityWorkdirWrite}
		// Inside, relative to the working directory.
		if _, err := call(t, f, "write", map[string]any{"path": "deep/c.txt", "content": "ok"}); err != nil {
			t.Fatalf("write inside: %v", err)
		}
		if _, err := call(t, f, "edit", map[string]any{"path": "a.go", "edits": []map[string]string{{"oldText": "One", "newText": "Uno"}}}); err != nil {
			t.Fatalf("edit inside: %v", err)
		}
		// Outside: absolute, and by climbing out.
		for _, target := range []string{filepath.Join(outside, "w.txt"), "../../" + filepath.Base(outside) + "/w.txt", "../../../escape.txt"} {
			if _, err := call(t, f, "write", map[string]any{"path": target, "content": "no"}); err == nil || !strings.Contains(err.Error(), "outside the session's worktree") {
				t.Errorf("write %q err = %v", target, err)
			}
		}
		if _, err := call(t, f, "edit", map[string]any{"path": filepath.Join(outside, "o.txt"), "edits": []map[string]string{{"oldText": "outside", "newText": "changed"}}}); err == nil {
			t.Error("edit outside the worktree was allowed")
		}
		if b, _ := os.ReadFile(filepath.Join(outside, "o.txt")); string(b) != "outside\n" {
			t.Errorf("outside file changed: %q", b)
		}
	})

	t.Run("workdir-write follows no symbolic link out", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symbolic links need privileges on Windows")
		}
		must(t, os.Symlink(outside, filepath.Join(root, "link")))
		f := &Files{Root: root, Cwd: root, Authority: AuthorityWorkdirWrite}
		if _, err := call(t, f, "write", map[string]any{"path": "link/escaped.txt", "content": "no"}); err == nil {
			t.Error("a write through a link that leaves the worktree was allowed")
		}
		if _, err := os.Stat(filepath.Join(outside, "escaped.txt")); err == nil {
			t.Error("the file landed outside the worktree")
		}
		if _, err := call(t, f, "edit", map[string]any{"path": "link/o.txt", "edits": []map[string]string{{"oldText": "outside", "newText": "changed"}}}); err == nil {
			t.Error("an edit through a link that leaves the worktree was allowed")
		}
	})

	t.Run("full writes anywhere", func(t *testing.T) {
		f := &Files{Root: root, Cwd: root, Authority: AuthorityFull}
		target := filepath.Join(outside, "deep", "f.txt")
		if _, err := call(t, f, "write", map[string]any{"path": target, "content": "free"}); err != nil {
			t.Fatalf("write: %v", err)
		}
		if b, _ := os.ReadFile(target); string(b) != "free" {
			t.Errorf("content = %q", b)
		}
		if _, err := call(t, f, "edit", map[string]any{"path": target, "edits": []map[string]string{{"oldText": "free", "newText": "freer"}}}); err != nil {
			t.Fatalf("edit: %v", err)
		}
	})

	t.Run("no authority writes nothing", func(t *testing.T) {
		f := &Files{Root: root, Cwd: root}
		if _, err := call(t, f, "write", map[string]any{"path": "n.txt", "content": "x"}); err == nil {
			t.Error("a session with no authority wrote a file")
		}
	})
}

func TestEditExactReplacementAndDiff(t *testing.T) {
	root := newTree(t)
	f := &Files{Root: root, Cwd: root, Authority: AuthorityWorkdirWrite}
	out, err := call(t, f, "edit", map[string]any{"path": "src/a.go", "edits": []map[string]string{
		{"oldText": "return 1", "newText": "return 10"},
		{"oldText": "return 2", "newText": "return 20"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := "--- src/a.go\n+++ src/a.go\n@@ -1,4 +1,4 @@\n package a\n \n-func One() int { return 1 }\n-func Two() int { return 2 }\n+func One() int { return 10 }\n+func Two() int { return 20 }\n"
	if out != want {
		t.Errorf("diff =\n%s\nwant\n%s", out, want)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "src", "a.go")); string(b) != "package a\n\nfunc One() int { return 10 }\nfunc Two() int { return 20 }\n" {
		t.Errorf("file = %q", b)
	}
}

func TestEditRefusals(t *testing.T) {
	root := newTree(t)
	f := &Files{Root: root, Cwd: root, Authority: AuthorityFull}
	edit := func(edits ...map[string]string) error {
		_, err := call(t, f, "edit", map[string]any{"path": "src/deep/b.txt", "edits": edits})
		return err
	}
	must(t, os.WriteFile(filepath.Join(root, "dup.txt"), []byte("x\nx\n"), 0o644))
	cases := []struct {
		name string
		do   func() error
		want string
	}{
		{"missing", func() error { return edit(map[string]string{"oldText": "delta", "newText": "d"}) }, "was not found"},
		{"empty", func() error { return edit(map[string]string{"oldText": "", "newText": "d"}) }, "oldText is empty"},
		{"ambiguous", func() error {
			_, err := call(t, f, "edit", map[string]any{"path": "dup.txt", "edits": []map[string]string{{"oldText": "x", "newText": "y"}}})
			return err
		}, "matches 2 places"},
		{"overlap", func() error {
			return edit(map[string]string{"oldText": "alpha\nbeta", "newText": "a"}, map[string]string{"oldText": "beta\ngamma", "newText": "b"})
		}, "overlap"},
	}
	for _, tc := range cases {
		if err := tc.do(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "src", "deep", "b.txt")); string(b) != "alpha\nbeta\ngamma\n" {
		t.Errorf("a refused edit changed the file: %q", b)
	}
}

func TestEditPreservesCRLF(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "w.txt")
	must(t, os.WriteFile(p, []byte("one\r\ntwo\r\nthree\r\n"), 0o644))
	f := &Files{Root: root, Cwd: root, Authority: AuthorityFull}
	if _, err := call(t, f, "edit", map[string]any{"path": "w.txt", "edits": []map[string]string{{"oldText": "one\ntwo", "newText": "uno\ndos"}}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "uno\r\ndos\r\nthree\r\n" {
		t.Errorf("file = %q", b)
	}
}

func TestReadPagesAndRefusesBinary(t *testing.T) {
	root := t.TempDir()
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "line"+string(rune('a'+i-1)))
	}
	must(t, os.WriteFile(filepath.Join(root, "t.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "b.bin"), []byte{1, 2, 0, 3}, 0o644))
	f := &Files{Root: root, Cwd: root, Authority: AuthorityReadOnly}
	out, err := call(t, f, "read", map[string]any{"path": "t.txt", "offset": 3, "limit": 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "     3\tlinec\n     4\tlined\n") || !strings.Contains(out, "pass offset 5 to continue") {
		t.Errorf("read = %q", out)
	}
	if _, err := call(t, f, "read", map[string]any{"path": "b.bin"}); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("binary err = %v", err)
	}
	if _, err := call(t, f, "read", map[string]any{"path": "missing.txt"}); err == nil {
		t.Error("missing file read succeeded")
	}
}

func TestGrepAndGlobFilters(t *testing.T) {
	root := newTree(t)
	must(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, ".git", "config"), []byte("func hidden\n"), 0o644))
	f := &Files{Root: root, Cwd: root, Authority: AuthorityReadOnly}
	out, err := call(t, f, "grep", map[string]any{"pattern": "func", "glob": "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "src/a.go:3:") || strings.Contains(out, "hidden") {
		t.Errorf("grep = %q", out)
	}
	if out, _ := call(t, f, "grep", map[string]any{"pattern": "ONE", "ignoreCase": true}); !strings.Contains(out, "src/a.go:3:") {
		t.Errorf("ignoreCase grep = %q", out)
	}
	if out, _ := call(t, f, "grep", map[string]any{"pattern": "nothing-here"}); out != "no matches" {
		t.Errorf("no-match grep = %q", out)
	}
	if out, _ := call(t, f, "grep", map[string]any{"pattern": "func", "limit": 1}); !strings.Contains(out, "stopped at 1 matches") {
		t.Errorf("limited grep = %q", out)
	}
	if _, err := call(t, f, "grep", map[string]any{"pattern": "("}); err == nil {
		t.Error("a bad pattern was accepted")
	}
	if out, _ := call(t, f, "glob", map[string]any{"pattern": "src/*.go"}); strings.TrimSpace(out) != "src/a.go" {
		t.Errorf("glob = %q", out)
	}
	if out, _ := call(t, f, "glob", map[string]any{"pattern": "**/*.{go,txt}"}); strings.TrimSpace(out) != "src/a.go\nsrc/deep/b.txt" {
		t.Errorf("alternation glob = %q", out)
	}
}

func TestToolsAllowlist(t *testing.T) {
	f := &Files{Authority: AuthorityReadOnly}
	var names []string
	for _, tl := range f.Tools([]string{"Read", "grep"}) {
		names = append(names, tl.Def.Name)
	}
	if strings.Join(names, ",") != "read,grep" {
		t.Errorf("allowlisted tools = %v", names)
	}
	if n := len(f.Tools(nil)); n != 5 {
		t.Errorf("default tools = %d", n)
	}
}

func TestUnifiedDiffHunks(t *testing.T) {
	var a, b []string
	for i := 1; i <= 30; i++ {
		a = append(a, "l"+string(rune('A'+i%26))+string(rune('a'+i%7)))
	}
	b = append(b, a...)
	b[2] = "CHANGED-3"
	b[25] = "CHANGED-26"
	got := unifiedDiff("f", strings.Join(a, "\n")+"\n", strings.Join(b, "\n")+"\n")
	if strings.Count(got, "@@ -") != 2 || !strings.Contains(got, "+CHANGED-3") || !strings.Contains(got, "+CHANGED-26") {
		t.Errorf("diff = %s", got)
	}
	if unifiedDiff("f", "same\n", "same\n") != "" {
		t.Error("equal inputs gave a diff")
	}
	if got := unifiedDiff("f", "", "new\n"); !strings.Contains(got, "@@ -0,0 +1,1 @@") || !strings.Contains(got, "+new") {
		t.Errorf("creation diff = %q", got)
	}
}

func TestTruncateOutputKeepsHeadAndTail(t *testing.T) {
	s := strings.Repeat("a", 100) + strings.Repeat("é", 100) + strings.Repeat("z", 100)
	out, cut := TruncateOutput(s, 120)
	if !cut || !strings.Contains(out, "bytes of output omitted") || !strings.HasPrefix(out, "aaa") || !strings.HasSuffix(out, "zzz") {
		t.Errorf("out = %q", out)
	}
	for _, r := range out {
		if r == '�' {
			t.Fatalf("a rune was split: %q", out)
		}
	}
	if same, cut := TruncateOutput("short", 120); cut || same != "short" {
		t.Errorf("short = %q, %v", same, cut)
	}
}

// A symbolic link whose target does not exist yet is followed by a write,
// which creates the target: the boundary is judged where the link points.
func TestDanglingSymlinkWriteBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	root := newTree(t)
	outside := t.TempDir()
	f := &Files{Root: root, Cwd: root, Authority: AuthorityWorkdirWrite}

	t.Run("a dangling link is refused wherever it points", func(t *testing.T) {
		missing := filepath.Join(outside, "not-yet", "planted.txt")
		must(t, os.Symlink(missing, filepath.Join(root, "dangling")))
		must(t, os.Symlink(filepath.Join("..", filepath.Base(outside), "relative.txt"), filepath.Join(root, "rel")))
		must(t, os.Symlink("dangling", filepath.Join(root, "chain")))
		// A dangling link that points inside the tree is refused as well.
		must(t, os.Symlink(filepath.Join(root, "src", "made.txt"), filepath.Join(root, "inside")))
		for _, name := range []string{"dangling", "rel", "chain", "inside", "dangling/child"} {
			if _, err := call(t, f, "write", map[string]any{"path": name, "content": "no"}); err == nil {
				t.Errorf("write through %s was allowed", name)
			}
			if _, err := call(t, f, "edit", map[string]any{"path": name, "edits": []map[string]string{{"oldText": "a", "newText": "b"}}}); err == nil {
				t.Errorf("edit through %s was allowed", name)
			}
		}
		for _, p := range []string{missing, filepath.Join(outside, "relative.txt"), filepath.Join(root, "src", "made.txt")} {
			if _, err := os.Lstat(p); err == nil {
				t.Errorf("%s was created", p)
			}
		}
		if _, err := os.Stat(filepath.Join(outside, "not-yet")); err == nil {
			t.Error("the outside directory was created")
		}
	})

	t.Run("a loop is refused", func(t *testing.T) {
		must(t, os.Symlink("loop-b", filepath.Join(root, "loop-a")))
		must(t, os.Symlink("loop-a", filepath.Join(root, "loop-b")))
		if _, err := call(t, f, "write", map[string]any{"path": "loop-a", "content": "no"}); err == nil {
			t.Error("a write through a link loop was allowed")
		}
	})
}

// A relative link whose payload walks through a directory link and then ".."
// resolves, in the operating system, relative to the directory link's real
// target. Cleaning the payload lexically would collapse it inside the tree.
func TestRelativeLinkThroughDirectoryLinkAndDotDot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	root := newTree(t)
	outside := t.TempDir()
	deep := filepath.Join(outside, "a", "b")
	must(t, os.MkdirAll(deep, 0o755))
	f := &Files{Root: root, Cwd: root, Authority: AuthorityWorkdirWrite}
	// root/hop -> outside/a/b ; root/esc -> hop/../planted.txt
	// The OS resolves esc as outside/a/planted.txt; lexically it is root/planted.txt.
	must(t, os.Symlink(deep, filepath.Join(root, "hop")))
	must(t, os.Symlink("hop/../planted.txt", filepath.Join(root, "esc")))
	if _, err := call(t, f, "write", map[string]any{"path": "esc", "content": "no"}); err == nil {
		t.Error("write through the link escaped the tree check")
	}
	for _, p := range []string{filepath.Join(outside, "a", "planted.txt"), filepath.Join(root, "planted.txt")} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s was created", p)
		}
	}
	// An existing target reached the same way is judged where it really is.
	must(t, os.WriteFile(filepath.Join(outside, "a", "real.txt"), []byte("x"), 0o644))
	must(t, os.Symlink("hop/../real.txt", filepath.Join(root, "esc2")))
	if _, err := call(t, f, "write", map[string]any{"path": "esc2", "content": "no"}); err == nil || !strings.Contains(err.Error(), "outside the session's worktree") {
		t.Errorf("write to an existing target through the link: err = %v", err)
	}
}
