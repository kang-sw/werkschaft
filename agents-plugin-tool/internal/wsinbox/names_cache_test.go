package wsinbox

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// settle backdates the directories past namesTrustAge, as an inbox nobody
// wrote for a while; restoring the same time after a write also stands in for
// a filesystem whose directory time did not move.
func settle(t *testing.T, old time.Time, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}
}

func mustNames(t *testing.T, list func(string) ([]string, error), dir string) []string {
	t.Helper()
	names, err := list(dir)
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func readDirs() int64 { return wsstate.FileIOCounters()["file_io_read_dir"] }

func TestUnchangedInboxIsNotRelisted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inbox")
	a, _ := send(t, dir, "first")
	old := time.Now().Add(-time.Hour)
	settle(t, old, dir)
	if got := mustNames(t, Names, dir); !slices.Equal(got, []string{a}) {
		t.Fatalf("Names = %v, want [%s]", got, a)
	}
	before := readDirs()
	for range 20 {
		if got := mustNames(t, Names, dir); !slices.Equal(got, []string{a}) {
			t.Fatalf("Names = %v, want [%s]", got, a)
		}
		if held, err := Held(dir); err != nil || len(held) != 1 {
			t.Fatalf("Held = %v, %v", held, err)
		}
	}
	if got := readDirs() - before; got != 0 {
		t.Fatalf("40 reads of an unchanged inbox listed it %d times, want 0", got)
	}
}

func TestRecentlyChangedInboxIsRelisted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inbox")
	send(t, dir, "first")
	mustNames(t, Names, dir)
	before := readDirs()
	mustNames(t, Names, dir)
	if got := readDirs() - before; got != 1 {
		t.Fatalf("a directory changed within namesTrustAge was listed %d times, want 1", got)
	}
}

func TestInProcessWriteIsSeenAtOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inbox")
	a, _ := send(t, dir, "first")
	old := time.Now().Add(-time.Hour)
	settle(t, old, dir)
	mustNames(t, Names, dir)
	// Each write is seen even where the directory keeps its old time.
	b, _ := send(t, dir, "second")
	settle(t, old, dir)
	if got := mustNames(t, Names, dir); !slices.Equal(got, []string{a, b}) {
		t.Fatalf("after a send: Names = %v, want [%s %s]", got, a, b)
	}
	steered, err := Steer(dir, b)
	if err != nil {
		t.Fatal(err)
	}
	settle(t, old, dir)
	if got := mustNames(t, Names, dir); !slices.Equal(got, []string{a, steered}) {
		t.Fatalf("after a steer: Names = %v, want [%s %s]", got, a, steered)
	}
	drained := DrainedDir(dir)
	if err := os.MkdirAll(drained, 0o755); err != nil {
		t.Fatal(err)
	}
	settle(t, old, drained)
	if got := mustNames(t, DrainedNames, dir); len(got) != 0 {
		t.Fatalf("DrainedNames = %v before the drain", got)
	}
	if recs, err := Drain(dir); err != nil || len(recs) != 2 {
		t.Fatalf("Drain = %v, %v", recs, err)
	}
	settle(t, old, dir, drained)
	if got := mustNames(t, Names, dir); len(got) != 0 {
		t.Fatalf("after a drain: Names = %v, want none", got)
	}
	if got := mustNames(t, DrainedNames, dir); !slices.Equal(got, []string{a, steered}) {
		t.Fatalf("after a drain: DrainedNames = %v, want [%s %s]", got, a, steered)
	}
}

func TestExternalWriteIsSeenOnTheNextCheck(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inbox")
	a, _ := send(t, dir, "first")
	old := time.Now().Add(-time.Hour)
	settle(t, old, dir)
	mustNames(t, Names, dir)
	// Another process's write bumps no counter here; only the directory
	// time tells.
	ext := "20261117T000000.000000000Z-0123abcd.json"
	if err := os.WriteFile(filepath.Join(dir, ext), []byte(`{"schemaVersion":1,"parts":["outside"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mustNames(t, Names, dir); !slices.Equal(got, []string{ext, a}) {
		t.Fatalf("after an external send: Names = %v, want [%s %s]", got, ext, a)
	}
	settle(t, old.Add(time.Minute), dir)
	mustNames(t, Names, dir)
	if err := os.Remove(filepath.Join(dir, a)); err != nil {
		t.Fatal(err)
	}
	if got := mustNames(t, Names, dir); !slices.Equal(got, []string{ext}) {
		t.Fatalf("after an external drain: Names = %v, want [%s]", got, ext)
	}
}

func statCount() int64 { return wsstate.FileIOCounters()["file_io_stat"] }

func TestNamesWithinFallbackInvalidationAndAuthoritativeFreshness(t *testing.T) {
	now := time.Now()
	prevNow := namesNow
	namesNow = func() time.Time { return now }
	t.Cleanup(func() { namesNow = prevNow })

	const maxAge = 5 * time.Second
	root := t.TempDir()
	dirA := filepath.Join(root, "a", "inbox")
	dirB := filepath.Join(root, "b", "inbox")

	// 1. Missing directory is cached for maxAge with zero stats, while
	// authoritative Names still stats immediately and sees external creation.
	if got, err := NamesWithin(dirA, maxAge); err != nil || len(got) != 0 {
		t.Fatalf("cold missing NamesWithin = %v, %v", got, err)
	}
	statsBefore, readsBefore := statCount(), readDirs()
	for range 10 {
		if got, err := NamesWithin(dirA, maxAge); err != nil || len(got) != 0 {
			t.Fatalf("cached missing NamesWithin = %v, %v", got, err)
		}
	}
	if statCount()-statsBefore != 0 || readDirs()-readsBefore != 0 {
		t.Fatalf("cached missing dir used stats=%d readDirs=%d, want 0/0", statCount()-statsBefore, readDirs()-readsBefore)
	}

	// External writer creates dirA with an entry (without bumping namesGen).
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	ext1 := "20261128T000001.000000000Z-aaaa1111.json"
	if err := os.WriteFile(filepath.Join(dirA, ext1), []byte(`{"schemaVersion":1,"parts":["ext1"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Still cached as missing within maxAge.
	if got, err := NamesWithin(dirA, maxAge); err != nil || len(got) != 0 {
		t.Fatalf("within maxAge NamesWithin = %v, %v; want cached empty", got, err)
	}
	// Authoritative Names and Held see external write immediately.
	if got := mustNames(t, Names, dirA); !slices.Equal(got, []string{ext1}) {
		t.Fatalf("authoritative Names missed external creation: %v", got)
	}
	if held, err := Held(dirA); err != nil || len(held) != 1 {
		t.Fatalf("authoritative Held = %v, %v; want 1", held, err)
	}

	// 2. Populating recent (within namesTrustAge) and settled dirs via
	// NamesWithin skips both stat and read_dir until maxAge elapses.
	now = now.Add(maxAge)
	if got, err := NamesWithin(dirA, maxAge); err != nil || !slices.Equal(got, []string{ext1}) {
		t.Fatalf("after maxAge NamesWithin = %v, %v; want [%s]", got, err, ext1)
	}
	bEntry, _ := send(t, dirB, "b-first")
	if got, err := NamesWithin(dirB, maxAge); err != nil || !slices.Equal(got, []string{bEntry}) {
		t.Fatalf("cold dirB NamesWithin = %v, %v", got, err)
	}
	// Re-warm dirA after in-process send(dirB) bumped global namesGen.
	if got, err := NamesWithin(dirA, maxAge); err != nil || !slices.Equal(got, []string{ext1}) {
		t.Fatalf("rewarm dirA NamesWithin = %v, %v", got, err)
	}
	statsBefore, readsBefore = statCount(), readDirs()
	for range 10 {
		if got, err := NamesWithin(dirA, maxAge); err != nil || !slices.Equal(got, []string{ext1}) {
			t.Fatalf("cached dirA NamesWithin = %v, %v", got, err)
		}
		if got, err := NamesWithin(dirB, maxAge); err != nil || !slices.Equal(got, []string{bEntry}) {
			t.Fatalf("cached dirB NamesWithin = %v, %v", got, err)
		}
	}
	if statCount()-statsBefore != 0 || readDirs()-readsBefore != 0 {
		t.Fatalf("warm NamesWithin used stats=%d readDirs=%d, want 0/0", statCount()-statsBefore, readDirs()-readsBefore)
	}

	// 3. Dir-scoped InvalidateNames(dirA) exposes an external write on dirA
	// immediately while keeping dirB cached at zero stats.
	ext2 := "20261128T000002.000000000Z-bbbb2222.json"
	if err := os.WriteFile(filepath.Join(dirA, ext2), []byte(`{"schemaVersion":1,"parts":["ext2"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateNames(dirA)
	if got, err := NamesWithin(dirA, maxAge); err != nil || !slices.Equal(got, []string{ext1, ext2}) {
		t.Fatalf("after dir-scoped InvalidateNames(dirA) = %v, %v", got, err)
	}
	statsBefore = statCount()
	if got, err := NamesWithin(dirB, maxAge); err != nil || !slices.Equal(got, []string{bEntry}) {
		t.Fatalf("unrelated dirB NamesWithin = %v, %v", got, err)
	}
	if statCount()-statsBefore != 0 {
		t.Fatalf("dir-scoped invalidate of dirA caused %d stats on dirB, want 0", statCount()-statsBefore)
	}

	// 4. Global InvalidateNames() expires dirB too.
	extB := "20261128T000003.000000000Z-cccc3333.json"
	if err := os.WriteFile(filepath.Join(dirB, extB), []byte(`{"schemaVersion":1,"parts":["extB"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateNames()
	if got, err := NamesWithin(dirB, maxAge); err != nil || !slices.Equal(got, []string{extB, bEntry}) {
		t.Fatalf("after global InvalidateNames() dirB = %v, %v", got, err)
	}

	// 5. In-process writes (Send, Steer, Drain) invalidate immediately without
	// advancing clock, including DrainedNamesWithin.
	if got, err := DrainedNamesWithin(dirA, maxAge); err != nil || len(got) != 0 {
		t.Fatalf("cold DrainedNamesWithin = %v, %v", got, err)
	}
	inProc, _ := send(t, dirA, "in-process")
	if got, err := NamesWithin(dirA, maxAge); err != nil || !slices.Contains(got, inProc) {
		t.Fatalf("in-process Send not immediately visible to NamesWithin: %v", got)
	}
	if _, err := Drain(dirA); err != nil {
		t.Fatal(err)
	}
	if got, err := NamesWithin(dirA, maxAge); err != nil || len(got) != 0 {
		t.Fatalf("in-process Drain not immediately visible to NamesWithin: %v", got)
	}
	if got, err := DrainedNamesWithin(dirA, maxAge); err != nil || len(got) != 3 {
		t.Fatalf("in-process Drain not immediately visible to DrainedNamesWithin: %v, %v", got, err)
	}

	// 6. Un-nudged external write to DrainedDir is seen immediately by
	// authoritative DrainedNames and after maxAge by DrainedNamesWithin.
	extDrained := "20261128T000004.000000000Z-dddd4444.json"
	if err := os.WriteFile(filepath.Join(DrainedDir(dirA), extDrained), []byte(`{"schemaVersion":1,"parts":["extD"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := DrainedNamesWithin(dirA, maxAge); err != nil || slices.Contains(got, extDrained) {
		t.Fatalf("un-nudged drained write leaked before maxAge: %v", got)
	}
	if got := mustNames(t, DrainedNames, dirA); !slices.Contains(got, extDrained) {
		t.Fatalf("authoritative DrainedNames missed external drained write: %v", got)
	}
	now = now.Add(maxAge)
	if got, err := DrainedNamesWithin(dirA, maxAge); err != nil || !slices.Contains(got, extDrained) {
		t.Fatalf("DrainedNamesWithin after maxAge missed external drained write: %v", got)
	}

	// 7. Generation fence prevents a stale in-flight listing from overwriting an
	// intervening InvalidateNames call.
	gen, dirGen, _, _ := lookupNamesCache(dirA)
	InvalidateNames(dirA)
	storeNames(dirA, namesListing{
		gen:       gen,
		dirGen:    dirGen,
		checkedAt: now,
		names:     []string{"stale-in-flight.json"},
	})
	if got, err := NamesWithin(dirA, maxAge); err != nil || slices.Contains(got, "stale-in-flight.json") {
		t.Fatalf("stale storeNames bypassed dirGen fence: %v, %v", got, err)
	}
}
