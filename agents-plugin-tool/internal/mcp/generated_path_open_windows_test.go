//go:build windows

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsstate"
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
	before, err := lstatGeneratedPath(path)
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

// The whole grant/write path, identity probes included, must work for an
// artifact whose path needs the extended-length prefix.
func TestGeneratedPathGrantAndWriteOnLongPath(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 270 {
		dir = filepath.Join(dir, strings.Repeat("g", 40))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "artifact.md")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	created, err := file.Stat()
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	const key = "long-path-grant-key"
	if err := processGeneratedWrites.grant(key, []wsstate.GeneratedPath{{Kind: "review", Path: path, Created: created}}); err != nil {
		t.Fatalf("grant on a long path: %v", err)
	}
	if err := processGeneratedWrites.write(key, path, "long path content"); err != nil {
		t.Fatalf("write on a long path: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "long path content" {
		t.Fatalf("long path content = %q, %v", got, err)
	}
}
