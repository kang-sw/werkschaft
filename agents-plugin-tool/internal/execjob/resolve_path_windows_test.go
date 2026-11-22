//go:build windows

package execjob

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The resolver sees through a directory junction into a worktree, which
// filepath.EvalSymlinks keeps as spelled: a job admitted through the
// junction lies in the worktree's forms, and the worktree resolves to
// the same form from either side.
func TestResolvePathSeesThroughADirectoryJunction(t *testing.T) {
	base := t.TempDir()
	wt := filepath.Join(base, "wt")
	if err := os.MkdirAll(filepath.Join(wt, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, wt).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(link) })

	want, err := resolvePath(wt)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := resolvePath(link); err != nil || got != want {
		t.Fatalf("resolvePath(junction) = %q, %v, want %q", got, err, want)
	}
	if got, err := resolvePath(filepath.Join(link, "sub")); err != nil || got != filepath.Join(want, "sub") {
		t.Fatalf("resolvePath(below the junction) = %q, %v, want %q", got, err, filepath.Join(want, "sub"))
	}
	realRoot, realCwd, err := admittedPlaces(base, filepath.Join(link, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if !placesIn(rootForms(wt), []string{realRoot, realCwd}) {
		t.Fatalf("a job admitted through the junction (%q, %q) is outside the worktree's forms %q", realRoot, realCwd, rootForms(wt))
	}
	if placesIn(rootForms(wt), []string{base, filepath.Join(link, "sub")}) {
		t.Fatalf("the recorded spellings alone already match; the test does not exercise the resolver")
	}
}
