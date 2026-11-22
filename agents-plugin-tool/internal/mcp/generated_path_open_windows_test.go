//go:build windows

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A generated artifact under a path longer than MAX_PATH must still open:
// the raw CreateFile call gets the extended-length prefix os.OpenFile adds.
func TestOpenGeneratedPathGrantAcceptsLongPath(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 270 {
		dir = filepath.Join(dir, strings.Repeat("d", 40))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "artifact.md")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := openGeneratedPathGrant(path)
	if err != nil {
		t.Fatalf("open long generated path: %v", err)
	}
	defer file.Close()
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("long-path handle identity mismatch: %v", err)
	}
}

func TestExtendedLengthPathForms(t *testing.T) {
	short := `C:\short\path.md`
	if got := extendedLengthPath(short); got != short {
		t.Fatalf("short path = %q", got)
	}
	long := `C:\` + strings.Repeat(`segment\`, 40) + `file.md`
	if got := extendedLengthPath(long); got != `\\?\`+long {
		t.Fatalf("long drive path = %q", got)
	}
	unc := `\\server\share\` + strings.Repeat(`segment\`, 40) + `file.md`
	if got := extendedLengthPath(unc); got != `\\?\UNC\server\share\`+strings.Repeat(`segment\`, 40)+`file.md` {
		t.Fatalf("long UNC path = %q", got)
	}
	extended := `\\?\` + long
	if got := extendedLengthPath(extended); got != extended {
		t.Fatalf("extended path = %q", got)
	}
}
