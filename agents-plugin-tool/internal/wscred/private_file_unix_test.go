//go:build !windows

package wscred

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialFileIsOwnerOnly(t *testing.T) {
	s := testStore(t)
	// A directory an earlier run (or the user) left wider is narrowed.
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	setKey(t, s, fixtureKey)
	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("credential file mode = %o, want 600", mode)
	}
	dir, err := os.Stat(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if mode := dir.Mode().Perm(); mode != 0o700 {
		t.Fatalf("credential directory mode = %o, want 700", mode)
	}
}
