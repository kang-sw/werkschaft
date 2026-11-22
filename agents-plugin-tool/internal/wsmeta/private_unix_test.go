//go:build !windows

package wsmeta

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A database or WAL left with a wider mode (an older file, a restored copy)
// is narrowed to owner-only on the next open, before SQLite writes to it.
func TestReopenNarrowsWidenedFiles(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "dashboard")
	s := openTest(t, dir, testOpts())
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, DBName)
	if err := os.Chmod(db, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db+"-wal", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(db+"-wal", 0o644); err != nil {
		t.Fatal(err)
	}
	s2 := openTest(t, dir, testOpts())
	if err := s2.PutRecord(ctx, "fresh", "g", []byte(`{"reply_id":"fresh"}`)); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{db, db + "-wal"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v after reopen, want 0600", p, info.Mode().Perm())
		}
	}
}
