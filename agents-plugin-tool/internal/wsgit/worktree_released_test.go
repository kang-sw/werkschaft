package wsgit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// linkedCheckout makes a directory shaped like a linked worktree: a gitfile
// pointing at an admin directory elsewhere, as `git worktree add` leaves it.
func linkedCheckout(t *testing.T) (checkout, admin string) {
	t.Helper()
	base := t.TempDir()
	checkout = filepath.Join(base, "wt")
	admin = filepath.Join(base, "repo", ".git", "worktrees", "wt")
	for _, dir := range []string{checkout, admin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: "+admin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return checkout, admin
}

// TestWorktreeReleaseMarkerRoundTripsInTheAdminDir: the marker lives in the
// admin directory (outside reset/clean), survives a fresh read (the process
// restart shape), and a later release replaces it.
func TestWorktreeReleaseMarkerRoundTripsInTheAdminDir(t *testing.T) {
	checkout, admin := linkedCheckout(t)
	if _, ok, err := WorktreeReleasedAt(checkout); err != nil || ok {
		t.Fatalf("no release recorded yet: ok=%v err=%v", ok, err)
	}
	first := time.Date(2026, 11, 22, 10, 0, 0, 123456789, time.UTC)
	if err := RecordWorktreeRelease(checkout, first); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(admin, WorktreeReleasedFileName)); err != nil {
		t.Fatalf("marker must live in the admin directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(checkout, WorktreeReleasedFileName)); !os.IsNotExist(err) {
		t.Fatalf("marker must not live in the working tree: %v", err)
	}
	at, ok, err := WorktreeReleasedAt(checkout)
	if err != nil || !ok || !at.Equal(first) {
		t.Fatalf("read back %v ok=%v err=%v, want %v", at, ok, err, first)
	}
	later := first.Add(time.Hour)
	if err := RecordWorktreeRelease(checkout, later); err != nil {
		t.Fatal(err)
	}
	if at, ok, err := WorktreeReleasedAt(checkout); err != nil || !ok || !at.Equal(later) {
		t.Fatalf("a later release must replace the marker: %v ok=%v err=%v", at, ok, err)
	}
	entries, err := os.ReadDir(admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("no temp file may remain beside the marker: %v", entries)
	}
}

// TestWorktreeReleaseMarkerRefusesNewerAndMalformedRecords: a reader never
// guesses a boundary; callers then count every binding.
func TestWorktreeReleaseMarkerRefusesNewerAndMalformedRecords(t *testing.T) {
	checkout, admin := linkedCheckout(t)
	for name, body := range map[string]string{
		"malformed": "{",
		"newer":     `{"schema_version":2,"released_at":"2026-11-22T10:00:00Z"}`,
		"bad time":  `{"schema_version":1,"released_at":"yesterday"}`,
	} {
		if err := os.WriteFile(filepath.Join(admin, WorktreeReleasedFileName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := WorktreeReleasedAt(checkout); err == nil || ok {
			t.Fatalf("%s marker must be an error, not a boundary: ok=%v err=%v", name, ok, err)
		}
	}
	if _, ok, err := WorktreeReleasedAt(t.TempDir()); err != nil || ok {
		t.Fatalf("a directory without .git has no release: ok=%v err=%v", ok, err)
	}
	if err := RecordWorktreeRelease(t.TempDir(), time.Now()); err == nil {
		t.Fatal("recording a release outside a checkout must fail")
	}
}

// TestBoundBeforeReleaseOrdersWholeSeconds: agent records keep whole seconds,
// so only a binding a whole second earlier predates the release; the
// release's own second stays a current binding.
func TestBoundBeforeReleaseOrdersWholeSeconds(t *testing.T) {
	release := time.Date(2026, 11, 22, 10, 0, 5, 900_000_000, time.UTC)
	for _, tc := range []struct {
		created string
		want    bool
	}{
		{"2026-11-22T10:00:04Z", true},
		{"2026-11-22T09:59:59Z", true},
		{"2026-11-22T10:00:05Z", false}, // same second: not ordered, counts
		{"2026-11-22T10:00:06Z", false},
		{"2026-11-22T19:00:04+09:00", true},
		{"", false},
		{"not a time", false},
	} {
		if got := BoundBeforeRelease(tc.created, release); got != tc.want {
			t.Errorf("BoundBeforeRelease(%q) = %v, want %v", tc.created, got, tc.want)
		}
	}
}

// TestCheckoutRootFindsTheNearestGitEntry maps a working directory below a
// checkout to that checkout.
func TestCheckoutRootFindsTheNearestGitEntry(t *testing.T) {
	checkout, _ := linkedCheckout(t)
	sub := filepath.Join(checkout, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CheckoutRoot(sub); got != checkout {
		t.Fatalf("CheckoutRoot(%q) = %q, want %q", sub, got, checkout)
	}
	if got := CheckoutRoot(checkout); got != checkout {
		t.Fatalf("CheckoutRoot of the root = %q", got)
	}
	if got := CheckoutRoot(""); got != "" {
		t.Fatalf("empty path = %q", got)
	}
}
