//go:build windows

package execjob

import (
	"context"
	"encoding/binary"
	"errors"
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

// A path ending in a period names the undotted directory, as an ordinary
// Win32 open and CreateProcess see it, short or past MAX_PATH; where both
// names exist (made through the extended-length form), the resolver still
// answers what the unprefixed open sees, so admission and the process
// agree on the directory, and the literal dotted directory, reached by a
// prefixed request or a junction, is refused.
func TestResolvePathOfATrailingDotNamesTheUndottedDirectory(t *testing.T) {
	base, err := resolvePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(base, "checkout")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	dotted := checkout + "."
	if got, err := resolvePath(dotted); err != nil || got != checkout {
		t.Fatalf("resolvePath(%q) = %q, %v, want %q", dotted, got, err, checkout)
	}
	if _, realCwd, err := admittedPlaces(base, dotted); err != nil || realCwd != checkout {
		t.Fatalf("admittedPlaces(dotted cwd) = %q, %v, want %q", realCwd, err, checkout)
	}

	deep := longDir(t, base)
	if got, err := resolvePath(deep + "."); err != nil || got != deep {
		t.Fatalf("resolvePath(long path + \".\") = %q, %v, want %q", got, err, deep)
	}

	dual := filepath.Join(base, "dual")
	for _, d := range []string{dual, dual + "."} {
		if err := os.Mkdir(`\\?\`+d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.Remove(`\\?\` + dual + ".") })
	// What an unprefixed open of the dotted name reaches.
	seen, err := os.Stat(dual + ".")
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolvePath(dual + ".")
	if err != nil {
		t.Fatal(err)
	}
	// The resolution, opened literally.
	resolved, err := os.Stat(`\\?\` + got)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(seen, resolved) || got != dual {
		t.Fatalf("resolvePath(%q) with both names present = %q, want %q, the directory the unprefixed open sees", dual+".", got, dual)
	}

	// The literal `dual.`, which only extended syntax reaches, has no
	// ordinary spelling: CreateProcess given `dual.` starts in `dual`. The
	// resolver names it as it is, for the fence; admission refuses it as a
	// working directory, whether requested in the canonical prefixed form
	// or through a junction whose target it is.
	literal := `\\?\` + dual + "."
	if got, err := resolvePath(literal); err != nil || got != dual+"." {
		t.Fatalf("resolvePath(%q) = %q, %v, want the physical %q", literal, got, err, dual+".")
	}
	if _, _, err := admittedPlaces(base, literal); !errors.Is(err, errNoOrdinarySpelling) || !strings.Contains(err.Error(), "cannot be resolved, so the launch is refused") {
		t.Fatalf("admittedPlaces(prefixed literal dotted cwd) = %v, want a refusal naming %v", err, errNoOrdinarySpelling)
	}
	link := filepath.Join(base, "j")
	makeJunction(t, link, dual+".")
	if got, err := resolvePath(link); err != nil || got != dual+"." {
		t.Fatalf("resolvePath(junction to the literal %q) = %q, %v, want it", dual+".", got, err)
	}
	if _, _, err := admittedPlaces(base, link); !errors.Is(err, errNoOrdinarySpelling) {
		t.Fatalf("admittedPlaces(junction to the literal dotted directory) = %v, want %v", err, errNoOrdinarySpelling)
	}
	// The candidate side does not spawn; it keeps the physical form.
	if forms := rootForms(link); len(forms) != 2 || forms[0] != link || forms[1] != dual+"." {
		t.Fatalf("rootForms(junction to the literal dotted directory) = %q, want %q and %q", forms, link, dual+".")
	}
}

// A worktree whose physical directory ends in a space, `repo `, has no
// ordinary spelling of its own, yet `repo \sub` has one: Windows trims a
// trailing space only from the last component. A job admitted there
// through another junction is live in the worktree, so the candidate's
// forms must keep the physical `repo ` for the fence to match it.
func TestFenceMatchesAJobBelowATrailingSpaceWorktree(t *testing.T) {
	base, err := resolvePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "storage", "repo ")
	sub := repo + `\sub`
	if err := os.MkdirAll(`\\?\`+sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(`\\?\` + sub)
		_ = os.Remove(`\\?\` + repo)
	})
	other := filepath.Join(base, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(base, "checkout")
	makeJunction(t, checkout, repo)
	into := filepath.Join(other, "into")
	makeJunction(t, into, sub)

	if forms := rootForms(checkout); len(forms) != 2 || forms[1] != repo {
		t.Fatalf("rootForms(junction to %q) = %q, want the physical form kept", repo, forms)
	}
	realRoot, realCwd, err := admittedPlaces(other, into)
	if err != nil || realCwd != sub {
		t.Fatalf("admittedPlaces(other, junction to %q) = %q, %q, %v, want it admitted", sub, realRoot, realCwd, err)
	}
	fx := newHostedFixture(t, 0)
	_, err = fx.launch(context.Background(), nil, HostedLaunch{Root: other, Cwd: into, ReadOnly: true})
	var refusal *ErrReadOnlyLaunch
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "outside the worktree root") {
		t.Fatalf("read-only launch through outside junction: %v, want a read-only refusal", err)
	}
	if !placesIn(rootForms(checkout), []string{realRoot, realCwd}) {
		t.Fatalf("a job at %q is outside the forms %q of the worktree it runs in", realCwd, rootForms(checkout))
	}
	// The worktree itself, as a working directory, is still refused.
	if _, _, err := admittedPlaces(checkout, checkout); !errors.Is(err, errNoOrdinarySpelling) {
		t.Fatalf("admittedPlaces(cwd at %q) = %v, want %v", repo, err, errNoOrdinarySpelling)
	}
}

// win32Path leaves a short path and every extended or device path, in
// either separator, as given, and lengthens only a long one.
func TestWin32PathPrefixesOnlyLongPaths(t *testing.T) {
	for _, p := range []string{`C:\short\path`, `\\?\C:\x`, `//?/C:/x`, `\??\C:\x`, `\\.\pipe\x`, `//./pipe/x`} {
		if got := win32Path(p); got != p {
			t.Errorf("win32Path(%q) = %q, want it unchanged", p, got)
		}
	}
	long := `C:\` + strings.Repeat(`d\`, 150) + "end"
	if got, want := win32Path(long), `\\?\`+long; got != want {
		t.Errorf("win32Path(long) = %q, want %q", got, want)
	}
	unc := `\\host\share\` + strings.Repeat(`d\`, 150) + "end"
	if got, want := win32Path(unc), `\\?\UNC\host\share\`+strings.Repeat(`d\`, 150)+"end"; got != want {
		t.Errorf("win32Path(long UNC) = %q, want %q", got, want)
	}
}
