//go:build !windows

package wsconvidx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMakePrivateRejectsSymlinkedDatabase(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, DBName)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := makePrivate(link); err == nil {
		t.Fatal("makePrivate followed a symlinked database path")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("symlink target was chmodded to %o", info.Mode().Perm())
	}
}

func TestMakePrivateRejectsSymlinkedSideFile(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, DBName)
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, db+"-wal"); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := makePrivate(db); err == nil {
		t.Fatal("makePrivate followed a symlinked WAL path")
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o644 {
		t.Fatalf("symlink target was chmodded to %o", info.Mode().Perm())
	}
}

func TestMakePrivateTightensExistingModes(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, DBName)
	for _, p := range []string{db, db + "-wal", db + "-journal"} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := makePrivate(db); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{db, db + "-wal", db + "-journal"} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, %v; want 0600", p, info.Mode().Perm(), err)
		}
	}
}
