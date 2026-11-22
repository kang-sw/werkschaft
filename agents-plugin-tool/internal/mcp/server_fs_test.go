package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/execjob"
)

// fsFixture is a server whose connection is bound to conversation "lead",
// launched under the exec cutover of harness.
type fsFixture struct {
	t    *testing.T
	s    *Server
	root string
	key  string
}

func newFSFixture(t *testing.T, harness string) *fsFixture {
	t.Helper()
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	s := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, s, 900002, root, nil))
	s.conversation, s.execCutover = "lead", harness
	return &fsFixture{t: t, s: s, root: root, key: key}
}

func (fx *fsFixture) call(name string, args map[string]any) (string, bool) {
	fx.t.Helper()
	return callExecTool(fx.t, fx.s, fx.key, name, args)
}

func listedNames(s *Server) []string {
	var names []string
	for _, tool := range s.filteredTools() {
		names = append(names, tool["name"].(string))
	}
	return names
}

// The fs.* tools exist only on a trusted binding to a Codex conversation
// launched under the cutover: absent from every other inventory, and a
// direct call from elsewhere is refused.
func TestFSToolsOnlyInACutoverCodexSession(t *testing.T) {
	for _, tc := range []struct {
		name, conversation, cutover string
		want                        bool
	}{
		{"cutover codex", "lead", "codex", true},
		{"cutover claude", "lead", "claude", false},
		{"cutover agy", "lead", "agy", false},
		{"codex outside the cutover", "lead", "", false},
		{"no trusted binding", "", "codex", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFSFixture(t, "codex")
			mustWrite(t, fx.root, "a.txt", "hello\n")
			fx.s.conversation, fx.s.execCutover = tc.conversation, tc.cutover
			if tc.conversation == "" {
				// NewServerWithOptions drops a cutover without a binding.
				fx.s = NewServerWithOptions(ServerOptions{Root: fx.root, Version: "test", ExecCutover: "codex"})
				fx.key, _ = parseLoginResponse(t, callLogin(t, fx.s, 900003, fx.root, nil))
			}
			names := listedNames(fx.s)
			for _, tool := range codexReadToolNames {
				if got := slices.Contains(names, tool); got != tc.want {
					t.Fatalf("%s listed = %v, want %v", tool, got, tc.want)
				}
			}
			text, isErr := fx.call("fs.read", map[string]any{"path": "a.txt"})
			if tc.want {
				if isErr || !strings.Contains(text, "1\thello") {
					t.Fatalf("fs.read = %v %q", isErr, text)
				}
				return
			}
			if !isErr || !strings.Contains(text, "runs only in a dashboard-hosted Codex session") {
				t.Fatalf("fs.read outside a cutover Codex session = %v %q", isErr, text)
			}
		})
	}
}

// Exec tools are listed only to a cutover session with hosted exec jobs,
// and only the new surface: the legacy names stay callable but unlisted.
func TestExecToolsListedOnlyUnderTheCutover(t *testing.T) {
	fx := newFSFixture(t, "claude")
	hosted := &execjob.Hosted{}
	fx.s.exec = func() *execjob.Hosted { return hosted }
	names := listedNames(fx.s)
	for _, tool := range cutoverExecTools {
		if !slices.Contains(names, tool) {
			t.Fatalf("cutover session misses %s in %v", tool, names)
		}
	}
	for _, tool := range []string{"exec.spawn", "exec.status", "exec.result", "exec.abort", "exec.raw.tail", "exec.raw.read", "exec.raw.grep", "fs.read"} {
		if slices.Contains(names, tool) {
			t.Fatalf("cutover claude session lists %s", tool)
		}
	}
	fx.s.execCutover = ""
	for _, name := range listedNames(fx.s) {
		if strings.HasPrefix(name, "exec.") || strings.HasPrefix(name, "fs.") {
			t.Fatalf("session outside the cutover lists %s", name)
		}
	}
	fx.s.execCutover, fx.s.exec = "claude", nil
	for _, name := range listedNames(fx.s) {
		if strings.HasPrefix(name, "exec.") {
			t.Fatalf("daemon without hosted exec lists %s", name)
		}
	}
	for _, name := range LeadToolNames() {
		if strings.HasPrefix(name, "exec.") || strings.HasPrefix(name, "fs.") {
			t.Fatalf("runtime inventory lists %s", name)
		}
	}
}

func TestFSReadOffsetsLimitsAndRefusals(t *testing.T) {
	fx := newFSFixture(t, "codex")
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	mustWrite(t, fx.root, "many.txt", b.String())
	text, isErr := fx.call("fs.read", map[string]any{"path": "many.txt", "offset": 10, "limit": 3})
	if isErr || text != "11\tline 11\n12\tline 12\n13\tline 13\n[more lines follow line 13; continue with offset=13]\n" {
		t.Fatalf("fs.read offset/limit = %v %q", isErr, text)
	}
	text, _ = fx.call("fs.read", map[string]any{"path": "many.txt"})
	if !strings.HasSuffix(text, "200\tline 200\n[more lines follow line 200; continue with offset=200]\n") {
		t.Fatalf("fs.read default limit: %q", text[max(0, len(text)-120):])
	}
	// The 4 KiB budget cuts before the limit on wide lines.
	mustWrite(t, fx.root, "wide.txt", strings.Repeat(strings.Repeat("x", 100)+"\n", 100))
	text, _ = fx.call("fs.read", map[string]any{"path": "wide.txt"})
	if len(text) > fsResultBudget || !strings.Contains(text, "[truncated at the 4096-byte reply budget after line") {
		t.Fatalf("fs.read budget: %d bytes, %q", len(text), text[max(0, len(text)-120):])
	}
	text, _ = fx.call("fs.read", map[string]any{"path": "many.txt", "offset": 298})
	if text != "299\tline 299\n300\tline 300\n" {
		t.Fatalf("fs.read end = %q", text)
	}
	text, _ = fx.call("fs.read", map[string]any{"path": "many.txt", "offset": 400})
	if !strings.Contains(text, "no lines after offset 400; the file has 300 lines") {
		t.Fatalf("fs.read past the end = %q", text)
	}
	mustWrite(t, fx.root, "long.txt", strings.Repeat("é", 400)+"\n")
	text, _ = fx.call("fs.read", map[string]any{"path": "long.txt"})
	if !strings.Contains(text, "[line cut at 512 of 800 bytes]") {
		t.Fatalf("fs.read long line = %q", text)
	}
	if err := os.WriteFile(filepath.Join(fx.root, "bin.dat"), []byte("ab\x00cd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, isErr := fx.call("fs.read", map[string]any{"path": "bin.dat"}); !isErr || !strings.Contains(text, "binary file") {
		t.Fatalf("fs.read binary = %v %q", isErr, text)
	}
	if text, isErr := fx.call("fs.read", map[string]any{"path": "."}); !isErr || !strings.Contains(text, "is a directory; use fs.list") {
		t.Fatalf("fs.read directory = %v %q", isErr, text)
	}
	// Codex's read-only sandbox reads the whole filesystem: so does fs.read.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("far away\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, isErr := fx.call("fs.read", map[string]any{"path": outside}); isErr || text != "1\tfar away\n" {
		t.Fatalf("fs.read absolute = %v %q", isErr, text)
	}
	if fsReadMaxLines != 2000 || fsReadScanBytes != 64<<20 {
		t.Fatalf("fs.read caps changed: %d lines, %d bytes", fsReadMaxLines, fsReadScanBytes)
	}
}

func TestFSGrepLineNumbersGlobAndBinarySkip(t *testing.T) {
	fx := newFSFixture(t, "codex")
	mustWrite(t, fx.root, "src/a.go", "package a\n// needle one\nfunc A() {}\n")
	mustWrite(t, fx.root, "src/b.md", "needle in markdown\n")
	mustWrite(t, fx.root, "src/deep/c.go", "x\ny\nneedle two\n")
	if err := os.WriteFile(filepath.Join(fx.root, "src", "blob.go"), []byte("needle\x00binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, isErr := fx.call("fs.grep", map[string]any{"pattern": "needle", "path": "src", "glob": "*.go"})
	if isErr {
		t.Fatalf("fs.grep = %q", text)
	}
	want := "src/a.go:2:// needle one\nsrc/deep/c.go:3:needle two\n[1 binary files skipped]\n"
	if text != want {
		t.Fatalf("fs.grep = %q, want %q", text, want)
	}
	text, _ = fx.call("fs.grep", map[string]any{"pattern": "^needle", "path": "src/b.md"})
	if text != "src/b.md:1:needle in markdown\n" {
		t.Fatalf("fs.grep one file = %q", text)
	}
	if text, isErr := fx.call("fs.grep", map[string]any{"pattern": "needle", "path": "src/blob.go"}); !isErr || !strings.Contains(text, "binary file") {
		t.Fatalf("fs.grep binary file = %v %q", isErr, text)
	}
	text, _ = fx.call("fs.grep", map[string]any{"pattern": "absent", "path": "."})
	if text != "no matches.\n[1 binary files skipped]\n" {
		t.Fatalf("fs.grep no match = %q", text)
	}
	if text, isErr := fx.call("fs.grep", map[string]any{"pattern": "(", "path": "."}); !isErr || !strings.Contains(text, "bad pattern") {
		t.Fatalf("fs.grep bad pattern = %v %q", isErr, text)
	}
	// .git is never searched.
	text, _ = fx.call("fs.grep", map[string]any{"pattern": "repositoryformatversion", "path": ".", "glob": "config"})
	if text != "no matches.\n" {
		t.Fatalf("fs.grep searched .git: %q", text)
	}
}

func TestFSGrepCapsMatchesAndBudget(t *testing.T) {
	fx := newFSFixture(t, "codex")
	var b strings.Builder
	for i := 0; i < 150; i++ {
		b.WriteString("m\n")
	}
	mustWrite(t, fx.root, "m.txt", b.String())
	text, _ := fx.call("fs.grep", map[string]any{"pattern": "m", "path": "m.txt"})
	if strings.Count(text, "\n") != fsGrepMaxMatches+1 || !strings.Contains(text, "[truncated: stopped at 100 matches]") {
		t.Fatalf("fs.grep match cap: %q", text[max(0, len(text)-80):])
	}
	b.Reset()
	for i := 0; i < 90; i++ {
		b.WriteString(strings.Repeat("w", 100) + "\n")
	}
	mustWrite(t, fx.root, "w.txt", b.String())
	text, _ = fx.call("fs.grep", map[string]any{"pattern": "w", "path": "w.txt"})
	if len(text) > fsResultBudget || !strings.Contains(text, "-byte reply budget after") {
		t.Fatalf("fs.grep budget: %d bytes, %q", len(text), text[max(0, len(text)-80):])
	}
	if fsGrepMaxFiles != 10000 || fsGrepScanBytes != 64<<20 || fsGrepFileBytes != 8<<20 {
		t.Fatalf("fs.grep scan caps changed")
	}
}

func TestFSListGlobDirsAndEntryCap(t *testing.T) {
	fx := newFSFixture(t, "codex")
	mustWrite(t, fx.root, "docs/b.md", "x")
	mustWrite(t, fx.root, "docs/a.md", "x")
	mustWrite(t, fx.root, "docs/c.txt", "x")
	mustWrite(t, fx.root, "docs/sub/d.md", "x")
	text, isErr := fx.call("fs.list", map[string]any{"path": "docs"})
	if isErr || text != "a.md\nb.md\nc.txt\nsub/\n" {
		t.Fatalf("fs.list = %v %q", isErr, text)
	}
	text, _ = fx.call("fs.list", map[string]any{"path": "docs", "glob": "*.md"})
	if text != "a.md\nb.md\n" {
		t.Fatalf("fs.list glob = %q", text)
	}
	if text, isErr := fx.call("fs.list", map[string]any{"path": "docs/a.md"}); !isErr || !strings.Contains(text, "not a directory") {
		t.Fatalf("fs.list file = %v %q", isErr, text)
	}
	dir := filepath.Join(fx.root, "many")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 600; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%03d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	text, _ = fx.call("fs.list", map[string]any{"path": "many"})
	if strings.Count(text, "\n") != fsListMaxEntries+1 || !strings.Contains(text, "[truncated: showing 500 of 600 entries; narrow with glob]") {
		t.Fatalf("fs.list entry cap: %d lines, tail %q", strings.Count(text, "\n"), text[max(0, len(text)-80):])
	}
	if len(text) > fsResultBudget {
		t.Fatalf("fs.list over budget: %d", len(text))
	}
	if fsListScanEntries != 10000 {
		t.Fatalf("fs.list scan cap changed")
	}
}
