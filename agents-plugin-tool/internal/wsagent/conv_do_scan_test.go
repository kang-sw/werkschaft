package wsagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// doCallbacks calls visit for every function literal passed as an argument to
// a method named do or doT (the failf-reporting wrapper), with the position of
// the call.
func doCallbacks(file *ast.File, visit func(lit *ast.FuncLit)) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "do" && sel.Sel.Name != "doT") {
			return true
		}
		for _, arg := range call.Args {
			if lit, ok := arg.(*ast.FuncLit); ok {
				visit(lit)
			}
		}
		return true
	})
}

// testExitCalls lists the testing calls that end or abandon the calling
// goroutine: inside a conv.do callback that goroutine is the conversation's
// loop, so the loop dies and the test fails far from its cause.
var testExitCalls = map[string]bool{
	"Fatal": true, "Fatalf": true, "FailNow": true,
	"Skip": true, "Skipf": true, "SkipNow": true,
}

// doCallbackTestExits returns "file:line: call" for every testing exit call
// inside a function literal passed to .do( in src.
func doCallbackTestExits(fset *token.FileSet, file *ast.File) []string {
	var found []string
	doCallbacks(file, func(lit *ast.FuncLit) {
		ast.Inspect(lit, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && testExitCalls[sel.Sel.Name] {
				found = append(found, fset.Position(call.Pos()).String()+": "+sel.Sel.Name)
			}
			return true
		})
	})
	return found
}

// doCallbackRecordCopies returns "file:line" for every call inside a function
// literal passed to .do( that takes the conversation's record by value as an
// argument (x.rec): the loop copies it without mu while Send updates Sends and
// LastActiveAt under mu, so the copy goes through c.record() instead.
func doCallbackRecordCopies(fset *token.FileSet, file *ast.File) []string {
	var found []string
	doCallbacks(file, func(lit *ast.FuncLit) {
		ast.Inspect(lit, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, arg := range call.Args {
				if sel, ok := arg.(*ast.SelectorExpr); ok && sel.Sel.Name == "rec" {
					found = append(found, fset.Position(sel.Pos()).String())
				}
			}
			return true
		})
	})
	return found
}

func parseGoSource(t *testing.T, name, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return fset, file
}

func scanPackage(t *testing.T, wantTests bool, scan func(*token.FileSet, *ast.File) []string) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") != wantTests {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		fset, file := parseGoSource(t, name, string(raw))
		found = append(found, scan(fset, file)...)
	}
	sort.Strings(found)
	return found
}

// A callback passed to conv.do runs on the conversation's loop goroutine: a
// t.Fatal there Goexits the loop, so tests copy values out inside do and
// assert on the test goroutine.
func TestDoCallbacksNeverExitTheLoopGoroutine(t *testing.T) {
	if found := scanPackage(t, true, doCallbackTestExits); len(found) > 0 {
		t.Fatalf("testing exit calls inside conv.do callbacks (copy the value out and assert on the test goroutine):\n%s", strings.Join(found, "\n"))
	}

	// The scan sees a planted violation, in each shape the suite uses.
	for _, src := range []string{
		"package p\nfunc f() { c.do(func() { t.Fatal(\"x\") }) }\n",
		"package p\nfunc f() { c.do(func() { h.t.Fatalf(\"x\") }) }\n",
		"package p\nfunc f() { h.runner.conv(id).do(func() { t.FailNow() }) }\n",
		"package p\nfunc f() { c.do(func() { if x { t.Skip() } }) }\n",
		"package p\nfunc f() { c.doT(t, func(failf func(string, ...any)) { t.Fatal(\"x\") }) }\n",
	} {
		fset, file := parseGoSource(t, "planted.go", src)
		if got := doCallbackTestExits(fset, file); len(got) != 1 {
			t.Errorf("scan of %q found %v, want exactly one violation", src, got)
		}
	}
	// Calls outside a do callback and non-test helpers are not violations.
	fset, file := parseGoSource(t, "ok.go", "package p\nfunc f() { t.Fatal(\"x\"); c.do(func() { got = c.record() }); d.do(\"GET\") }\n")
	if got := doCallbackTestExits(fset, file); len(got) != 0 {
		t.Errorf("scan flagged a clean source: %v", got)
	}
}

// The loop-side counterpart, narrower than a locking proof: a call inside a
// do callback must not take the conversation's record field (x.rec) as a
// direct argument, because that copies the whole record without mu while Send
// updates Sends and LastActiveAt under mu; the copy goes through c.record()
// instead, as 340acfb0a settled. It does not see an alias (`r := c.rec; f(r)`)
// or a copy in a loop handler not reached through do, and single-field reads
// of loop-written fields stay unlocked by design.
func TestDoCallbacksPassNoRecordFieldAsCallArgument(t *testing.T) {
	if found := scanPackage(t, false, doCallbackRecordCopies); len(found) > 0 {
		t.Fatalf("conv.do callbacks pass the conversation's record field as a call argument, copying it without mu (use c.record()):\n%s", strings.Join(found, "\n"))
	}
	fset, file := parseGoSource(t, "planted.go", "package p\nfunc f() { c.do(func() { err = endedError(c.rec) }) }\n")
	if got := doCallbackRecordCopies(fset, file); len(got) != 1 {
		t.Errorf("scan of a planted copy found %v, want exactly one", got)
	}
	fset, file = parseGoSource(t, "ok.go", "package p\nfunc f() { c.do(func() { err = endedError(c.record()); n := c.rec.Sends }) }\n")
	if got := doCallbackRecordCopies(fset, file); len(got) != 0 {
		t.Errorf("scan flagged a locked copy or a field read: %v", got)
	}
}
