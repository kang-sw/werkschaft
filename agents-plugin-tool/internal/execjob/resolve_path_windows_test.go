//go:build windows

package execjob

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"
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

// longDir creates, under base, a directory whose path is longer than
// MAX_PATH, through the extended-length form, and returns its ordinary
// spelling.
func longDir(t *testing.T, base string) string {
	t.Helper()
	deep := base
	for len(deep) <= windows.MAX_PATH+40 {
		deep = filepath.Join(deep, strings.Repeat("d", 40))
	}
	if err := os.MkdirAll(`\\?\`+deep, 0o755); err != nil {
		t.Fatal(err)
	}
	return deep
}

// makeJunction makes link (an empty directory it creates) a directory
// junction to target through FSCTL_SET_REPARSE_POINT, which, unlike
// mklink, takes a target of any length.
func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	sub := utf16.Encode([]rune(`\??\` + target))
	// Mount-point reparse data: substitute name, NUL, empty print name, NUL.
	names := make([]byte, 0, 2*len(sub)+4)
	for _, u := range sub {
		names = binary.LittleEndian.AppendUint16(names, u)
	}
	names = append(names, 0, 0, 0, 0)
	buf := binary.LittleEndian.AppendUint32(nil, windows.IO_REPARSE_TAG_MOUNT_POINT)
	buf = binary.LittleEndian.AppendUint16(buf, uint16(8+len(names)))
	buf = binary.LittleEndian.AppendUint16(buf, 0)                    // reserved
	buf = binary.LittleEndian.AppendUint16(buf, 0)                    // substitute name offset
	buf = binary.LittleEndian.AppendUint16(buf, uint16(2*len(sub)))   // substitute name length
	buf = binary.LittleEndian.AppendUint16(buf, uint16(2*len(sub)+2)) // print name offset
	buf = binary.LittleEndian.AppendUint16(buf, 0)                    // print name length
	buf = append(buf, names...)
	name, err := windows.UTF16PtrFromString(link)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	var n uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &n, nil); err != nil {
		t.Fatalf("set the junction %s -> %s: %v", link, target, err)
	}
}

// A working directory longer than MAX_PATH resolves, and admission accepts
// it: the resolver opens it in the extended-length form and answers in the
// ordinary one.
func TestResolvePathTakesAPathLongerThanMaxPath(t *testing.T) {
	base, err := resolvePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deep := longDir(t, base)
	got, err := resolvePath(deep)
	if err != nil {
		t.Fatalf("resolvePath(%d-character path): %v", len(deep), err)
	}
	if got != deep {
		t.Fatalf("resolvePath(long path) = %q, want %q", got, deep)
	}
	realRoot, realCwd, err := admittedPlaces(base, deep)
	if err != nil || realRoot != base || realCwd != deep {
		t.Fatalf("admittedPlaces(long cwd) = %q, %q, %v, want %q, %q", realRoot, realCwd, err, base, deep)
	}
}

// A short junction to a target longer than MAX_PATH resolves to that
// target in the ordinary spelling, the form rootForms gives the target, so
// a job entering through the junction lies in it.
func TestResolvePathFollowsAShortJunctionToALongTarget(t *testing.T) {
	base, err := resolvePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := longDir(t, base)
	link := filepath.Join(base, "j")
	makeJunction(t, link, target)
	got, err := resolvePath(link)
	if err != nil {
		t.Fatalf("resolvePath(junction to a %d-character target): %v", len(target), err)
	}
	if got != target || strings.HasPrefix(got, `\\?\`) {
		t.Fatalf("resolvePath(junction) = %q, want the stripped target %q", got, target)
	}
	if !placesIn(rootForms(target), []string{got}) {
		t.Fatalf("the junction's resolution %q is outside the target's forms %q", got, rootForms(target))
	}
}
