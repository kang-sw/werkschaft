package wsmeta

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// A real 8.3 short name of the witness resolves to its long name, and a
// link through it is found to route through the witness. Skipped where the
// volume creates no short names.
func TestShortNameOfTheWitnessIsTheWitness(t *testing.T) {
	dir := t.TempDir()
	wabs := filepath.Join(dir, "tracker-hooks-config.json")
	if err := os.WriteFile(wabs, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	long, err := windows.UTF16PtrFromString(wabs)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		t.Skipf("no short path: %v", err)
	}
	short := filepath.Base(windows.UTF16ToString(buf[:n]))
	if strings.EqualFold(short, filepath.Base(wabs)) {
		t.Skip("the volume creates no 8.3 names")
	}
	got, err := platformEntryName(dir, short)
	if err != nil || got != filepath.Base(wabs) {
		t.Fatalf("platformEntryName(%q) = %q, %v; want %q", short, got, err, filepath.Base(wabs))
	}
	abs := filepath.Join(dir, "tracker-hooks.json")
	if err := os.Symlink(short, abs); err != nil {
		if errors.Is(err, syscall.Errno(1314)) { // ERROR_PRIVILEGE_NOT_HELD
			t.Skip("symbolic links need Developer Mode or the privilege")
		}
		t.Fatal(err)
	}
	if through, err := routesThrough(abs, wabs); err != nil || !through {
		t.Fatalf("routesThrough via %q = %v, %v; want true", short, through, err)
	}
}
