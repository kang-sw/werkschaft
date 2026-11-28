package wsagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// acpHarnessIdent matches identifiers that name one concrete ACP harness.
var acpHarnessIdent = regexp.MustCompile(`(?i)agy|antigravity|gemini|copilot`)

// TestACPCoreHasNoHarnessIdentifiers keeps the shared ACP core harness
// neutral: harness policy reaches it only through acpDialect, so no
// identifier in a non-test acp_*.go file may name a concrete harness.
func TestACPCoreHasNoHarnessIdentifiers(t *testing.T) {
	files, err := filepath.Glob("acp_*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scanned++
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && acpHarnessIdent.MatchString(id.Name) {
				t.Errorf("%s: harness identifier %q in shared ACP core; route it through acpDialect", fset.Position(id.Pos()), id.Name)
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("no non-test acp_*.go files found; the guard scanned nothing")
	}
}
