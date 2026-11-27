package wstestutil

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCanonicalTempDirResolvesExistingRoot(t *testing.T) {
	dir := CanonicalTempDir(t)
	if !filepath.IsAbs(dir) {
		t.Fatalf("CanonicalTempDir(%q) is not absolute", dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("CanonicalTempDir directory missing: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	if dir != filepath.Clean(want) {
		t.Fatalf("CanonicalTempDir = %q, want canonical %q", dir, filepath.Clean(want))
	}
}

func TestCanonicalPathResolvesSymlinkAndMissingSuffixes(t *testing.T) {
	if got := CanonicalPath(""); got != "" {
		t.Fatalf("CanonicalPath(\"\") = %q, want empty", got)
	}
	base := CanonicalTempDir(t)
	missing := filepath.Join(base, "not-yet-created", "nested", "cache")
	if got := CanonicalPath(missing); got != missing {
		t.Fatalf("CanonicalPath(missing) = %q, want %q", got, missing)
	}
	messy := filepath.Join(base, ".", "not-yet-created", "temp", "..", "nested", "cache")
	if got := CanonicalPath(messy); got != missing {
		t.Fatalf("CanonicalPath(messy) = %q, want %q", got, missing)
	}

	t.Run("symlink", func(t *testing.T) {
		target := filepath.Join(base, "real-target")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(base, "alias-link")
		if err := os.Symlink(target, alias); err != nil {
			t.Skipf("symlinks unavailable here: %v", err)
		}
		if got := CanonicalPath(alias); got != target {
			t.Fatalf("CanonicalPath(alias) = %q, want %q", got, target)
		}
		aliasMissing := filepath.Join(alias, "child", "leaf")
		wantAliasMissing := filepath.Join(target, "child", "leaf")
		if got := CanonicalPath(aliasMissing); got != wantAliasMissing {
			t.Fatalf("CanonicalPath(alias missing child) = %q, want %q", got, wantAliasMissing)
		}
	})
}

func TestCloseBeforeTempDirClosesHeldFilesBeforeTempDirRemoval(t *testing.T) {
	// Nil checks must be safe no-ops.
	CloseBeforeTempDir(nil, func() error { return nil })
	CloseBeforeTempDir(t, nil)

	var subDir string
	closed := false
	t.Run("nested", func(t *testing.T) {
		subDir = CanonicalTempDir(t)
		heldPath := filepath.Join(subDir, "held.lock")
		f, err := os.Create(heldPath)
		if err != nil {
			t.Fatal(err)
		}
		CloseBeforeTempDir(t, func() error {
			// Verify the directory still exists when closer runs (meaning closer runs
			// BEFORE TempDir's os.RemoveAll cleanup in LIFO order).
			if _, statErr := os.Stat(subDir); statErr != nil {
				return fmt.Errorf("tempDir missing before closer ran: %w", statErr)
			}
			closed = true
			return f.Close()
		})
	})
	if !closed {
		t.Fatal("CloseBeforeTempDir did not invoke closer")
	}
	if _, err := os.Stat(subDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("TempDir %q still exists after nested test finished (err=%v)", subDir, err)
	}
}

type cleanupRecorderTB struct {
	testing.TB
	cleanups []func()
	errors   []string
}

func (r *cleanupRecorderTB) Helper() {}

func (r *cleanupRecorderTB) Cleanup(fn func()) {
	r.cleanups = append(r.cleanups, fn)
}

func (r *cleanupRecorderTB) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func TestCloseBeforeTempDirReportsCloserErrors(t *testing.T) {
	rec := &cleanupRecorderTB{TB: t}
	boom := errors.New("lock held")
	CloseBeforeTempDir(rec, func() error { return boom })
	if len(rec.cleanups) != 1 {
		t.Fatalf("cleanups registered = %d, want 1", len(rec.cleanups))
	}
	rec.cleanups[0]()
	if len(rec.errors) != 1 || !strings.Contains(rec.errors[0], "lock held") {
		t.Fatalf("recorded errors = %v, want boom error", rec.errors)
	}
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate agents-plugin-tool/go.mod from cwd")
		}
		dir = parent
	}
}

func walkGoFiles(t *testing.T, modRoot string, fn func(rel string, file *ast.File, fset *token.FileSet, isTest bool)) {
	t.Helper()
	fset := token.NewFileSet()
	for _, top := range []string{"internal", "cmd"} {
		base := filepath.Join(modRoot, top)
		if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			rel, relErr := filepath.Rel(modRoot, path)
			if relErr != nil {
				rel = path
			}
			fn(filepath.ToSlash(rel), file, fset, strings.HasSuffix(d.Name(), "_test.go"))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWSTestUtilIsolationAndStdlibOnlyImports(t *testing.T) {
	modRoot := findModuleRoot(t)
	const selfImport = "github.com/kang-sw/devenv/internal/wstestutil"

	walkGoFiles(t, modRoot, func(rel string, file *ast.File, _ *token.FileSet, isTest bool) {
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if !isTest && path == selfImport {
				t.Errorf("production file %s must not import %s", rel, selfImport)
			}
			if !isTest && strings.HasPrefix(rel, "internal/wstestutil/") && strings.HasPrefix(path, "github.com/kang-sw/devenv/") {
				t.Errorf("wstestutil production file %s must depend only on stdlib to avoid test import cycles (imports %s)", rel, path)
			}
		}
	})
}

func TestWindowsTestStaticGuards(t *testing.T) {
	modRoot := findModuleRoot(t)

	walkGoFiles(t, modRoot, func(rel string, file *ast.File, fset *token.FileSet, isTest bool) {
		if !isTest {
			return
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}

			// 1. Disallow raw filepath.EvalSymlinks(<x>.TempDir()); use wstestutil.CanonicalTempDir.
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 || !isSelectorCall(call.Fun, "filepath", "EvalSymlinks") {
					return true
				}
				inner, ok := call.Args[0].(*ast.CallExpr)
				if ok && isMethodNamed(inner.Fun, "TempDir") {
					pos := fset.Position(call.Pos())
					t.Errorf("%s:%d: use wstestutil.CanonicalTempDir instead of raw filepath.EvalSymlinks(TempDir())", rel, pos.Line)
				}
				return true
			})

			// 2. Enforce CloseBeforeTempDir appears after the first TempDir/CanonicalTempDir
			// in functions that allocate their own TempDir directly before any helper taking `t`.
			checkDirectCloseBeforeTempDirOrder(t, rel, fset, fn)

			// 3. Flag top-level Test*/Benchmark* functions that open SQLite handles
			// directly without any Close/Cleanup/defer in the same function.
			checkDirectSQLiteOpenHasClose(t, rel, fset, fn)
		}
	})
}

func checkDirectCloseBeforeTempDirOrder(t *testing.T, rel string, fset *token.FileSet, fn *ast.FuncDecl) {
	t.Helper()
	var (
		hasHelperWithT    bool
		firstCloseBeforeT token.Pos
		firstTempDirPos   token.Pos
	)
	// Walk statements sequentially at top-level body (avoid descending into subtests
	// which have their own independent testing.T instance).
	for _, stmt := range fn.Body.List {
		ast.Inspect(stmt, func(n ast.Node) bool {
			// Skip nested func literals (e.g. t.Run closures) since inner t has independent TempDir state.
			if _, isLit := n.(*ast.FuncLit); isLit {
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if isMethodNamed(call.Fun, "TempDir") || isSelectorCall(call.Fun, "wstestutil", "CanonicalTempDir") {
				if firstTempDirPos == token.NoPos {
					firstTempDirPos = call.Pos()
				}
				return true
			}
			if isSelectorCall(call.Fun, "wstestutil", "CloseBeforeTempDir") {
				if firstCloseBeforeT == token.NoPos {
					firstCloseBeforeT = call.Pos()
				}
				return true
			}
			// A helper taking identifier `t` might already have called t.TempDir().
			if firstTempDirPos == token.NoPos && firstCloseBeforeT == token.NoPos {
				for _, arg := range call.Args {
					if id, ok := arg.(*ast.Ident); ok && id.Name == "t" {
						hasHelperWithT = true
					}
				}
			}
			return true
		})
	}
	if !hasHelperWithT && firstCloseBeforeT != token.NoPos && firstTempDirPos != token.NoPos && firstCloseBeforeT < firstTempDirPos {
		pos := fset.Position(firstCloseBeforeT)
		t.Errorf("%s:%d: CloseBeforeTempDir registered before first TempDir() in %s (must follow TempDir for LIFO close-before-remove on Windows)", rel, pos.Line, fn.Name.Name)
	}
}

func checkDirectSQLiteOpenHasClose(t *testing.T, rel string, fset *token.FileSet, fn *ast.FuncDecl) {
	t.Helper()
	if !strings.HasPrefix(fn.Name.Name, "Test") && !strings.HasPrefix(fn.Name.Name, "Benchmark") {
		return
	}
	var openPos token.Pos
	hasClose := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if _, isDefer := n.(*ast.DeferStmt); isDefer {
			hasClose = true
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isSelectorCall(call.Fun, "wsmeta", "Open") ||
			isSelectorCall(call.Fun, "wsusercmd", "Open") ||
			isSelectorCall(call.Fun, "wscron", "OpenStore") ||
			isSelectorCall(call.Fun, "sql", "Open") {
			if openPos == token.NoPos {
				openPos = call.Pos()
			}
		}
		if isMethodNamed(call.Fun, "Close") ||
			isMethodNamed(call.Fun, "Cleanup") ||
			isSelectorCall(call.Fun, "wstestutil", "CloseBeforeTempDir") {
			hasClose = true
		}
		return true
	})
	if openPos != token.NoPos && !hasClose {
		pos := fset.Position(openPos)
		t.Errorf("%s:%d: SQLite/store open in %s without Close/Cleanup/CloseBeforeTempDir (risks Windows TempDir sharing violation)", rel, pos.Line, fn.Name.Name)
	}
}

func isSelectorCall(expr ast.Expr, pkg, fn string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != fn {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func isMethodNamed(expr ast.Expr, method string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == method
}
