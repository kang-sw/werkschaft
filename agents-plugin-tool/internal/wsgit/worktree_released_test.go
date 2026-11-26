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

// TestBoundBeforeReleaseOrdersAtNanosecondPrecision: a binding with a
// nanosecond instant predates the release only when strictly before it, so
// one made earlier in the release's own second no longer survives it.
func TestBoundBeforeReleaseOrdersAtNanosecondPrecision(t *testing.T) {
	release := time.Date(2026, 11, 22, 10, 0, 5, 900_000_000, time.UTC)
	for _, tc := range []struct {
		name    string
		created time.Time
		want    bool
	}{
		{"same second, earlier", time.Date(2026, 11, 22, 10, 0, 5, 100_000_000, time.UTC), true},
		{"one nanosecond earlier", release.Add(-time.Nanosecond), true},
		{"a second earlier", release.Add(-time.Second), true},
		{"the release instant", release, false},
		{"same second, later", release.Add(time.Nanosecond), false},
		{"a second later", release.Add(time.Second), false},
	} {
		// The whole-second createdAt agrees with the precise instant, as the
		// record writes both; the precise one decides.
		createdAt := tc.created.UTC().Format(time.RFC3339)
		if got := BoundBeforeRelease(createdAt, tc.created.UnixNano(), release); got != tc.want {
			t.Errorf("%s: BoundBeforeRelease = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestBoundBeforeReleaseFallsBackToWholeSecondsForOldRecords: a record from
// before created_unix_nano keeps only whole seconds, so only a binding a whole
// second earlier predates the release; the release's own second stays a
// current binding.
func TestBoundBeforeReleaseFallsBackToWholeSecondsForOldRecords(t *testing.T) {
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
		if got := BoundBeforeRelease(tc.created, 0, release); got != tc.want {
			t.Errorf("BoundBeforeRelease(%q, 0) = %v, want %v", tc.created, got, tc.want)
		}
	}
}

// TestCheckoutRegistrationGoneNeedsEvidenceOfRemoval: only a missing
// directory, a missing .git entry, or a gitfile naming a missing admin
// directory reads as a removed checkout; a main checkout's .git directory and
// a gitfile naming an existing admin directory are present.
func TestCheckoutRegistrationGoneNeedsEvidenceOfRemoval(t *testing.T) {
	linked, admin := linkedCheckout(t)
	mainCheckout := t.TempDir()
	if err := os.Mkdir(filepath.Join(mainCheckout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	noGit := t.TempDir()
	unregistered, unregisteredAdmin := linkedCheckout(t)
	if err := os.RemoveAll(unregisteredAdmin); err != nil {
		t.Fatal(err)
	}
	relative := t.TempDir()
	if err := os.MkdirAll(filepath.Join(relative, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	relativeCheckout := filepath.Join(relative, "wt")
	if err := os.Mkdir(relativeCheckout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relativeCheckout, ".git"), []byte("gitdir: ../admin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		checkout string
		want     bool
	}{
		{"missing directory", filepath.Join(t.TempDir(), "gone"), true},
		{"missing .git entry", noGit, true},
		{".git directory", mainCheckout, false},
		{"gitfile to an existing admin directory", linked, false},
		{"relative gitfile to an existing admin directory", relativeCheckout, false},
		{"gitfile to a missing admin directory", unregistered, true},
		{"empty path", "", false},
	} {
		if got := CheckoutRegistrationGone(tc.checkout); got != tc.want {
			t.Errorf("%s: CheckoutRegistrationGone(%q) = %v, want %v", tc.name, tc.checkout, got, tc.want)
		}
	}
	if _, err := os.Stat(admin); err != nil {
		t.Fatalf("the check touched the admin directory: %v", err)
	}
}

// TestContainingCheckoutsListsEveryEnclosingCheckout maps a working directory
// to every checkout that contains it, nearest first, so a directory inside a
// submodule of a checkout still reaches that checkout.
func TestContainingCheckoutsListsEveryEnclosingCheckout(t *testing.T) {
	checkout, _ := linkedCheckout(t)
	sub := filepath.Join(checkout, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ContainingCheckouts(sub); len(got) != 1 || got[0] != checkout {
		t.Fatalf("ContainingCheckouts(%q) = %q, want [%q]", sub, got, checkout)
	}
	module := filepath.Join(checkout, "vendor", "mod")
	inModule := filepath.Join(module, "src")
	if err := os.MkdirAll(inModule, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, ".git"), []byte("gitdir: ../../.git/modules/mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ContainingCheckouts(inModule); len(got) != 2 || got[0] != module || got[1] != checkout {
		t.Fatalf("ContainingCheckouts(in submodule) = %q, want [%q %q]", got, module, checkout)
	}
	if got := ContainingCheckouts(""); got != nil {
		t.Fatalf("empty path = %q", got)
	}
}
