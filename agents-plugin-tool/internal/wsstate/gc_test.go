package wsstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var gcNow = time.Date(2026, 11, 29, 12, 0, 0, 0, time.UTC)

const (
	gcProject  = "0123abcd"
	gcWorktree = "0123abcd@89abcdef"
)

func gcWrite(t *testing.T, path string, size int, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644); err != nil {
		t.Fatal(err)
	}
	gcAge(t, path, age)
	return path
}

func gcAge(t *testing.T, path string, age time.Duration) {
	t.Helper()
	at := gcNow.Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func gcExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func gcOptions() GCOptions {
	return GCOptions{Now: func() time.Time { return gcNow }}
}

func day(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func TestGCProjectsAgesWsstateAreasAndKeepsFreshEntries(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	for _, key := range []string{gcProject, gcWorktree} {
		dir := filepath.Join(proj, key)
		for _, area := range []string{"review-paths", "report-paths", "prompt-paths", "tmp"} {
			gcWrite(t, filepath.Join(dir, area, "old.md"), 10, day(15))
			gcWrite(t, filepath.Join(dir, area, "mid.md"), 10, day(13))
			gcWrite(t, filepath.Join(dir, area, "fresh.md"), 10, time.Hour)
		}
	}
	// A tmp directory is aged by its newest file: one fresh file keeps it.
	gcWrite(t, filepath.Join(proj, gcProject, "tmp", "busy", "stale.txt"), 5, day(20))
	gcWrite(t, filepath.Join(proj, gcProject, "tmp", "busy", "live.txt"), 5, time.Hour)
	gcWrite(t, filepath.Join(proj, gcProject, "tmp", "done", "stale.txt"), 5, day(20))
	gcAge(t, filepath.Join(proj, gcProject, "tmp", "done"), day(20))

	res := GCProjects(root, gcOptions())

	for _, key := range []string{gcProject, gcWorktree} {
		dir := filepath.Join(proj, key)
		for _, area := range []string{"review-paths", "report-paths", "prompt-paths", "tmp"} {
			if gcExists(filepath.Join(dir, area, "old.md")) {
				t.Errorf("%s/%s/old.md (15d) survived the 14d TTL", key, area)
			}
			for _, keep := range []string{"mid.md", "fresh.md"} {
				if !gcExists(filepath.Join(dir, area, keep)) {
					t.Errorf("%s/%s/%s was removed", key, area, keep)
				}
			}
		}
	}
	if !gcExists(filepath.Join(proj, gcProject, "tmp", "busy", "stale.txt")) {
		t.Error("a tmp directory with a fresh file was aged by its stale file")
	}
	if gcExists(filepath.Join(proj, gcProject, "tmp", "done")) {
		t.Error("a wholly stale tmp directory survived")
	}
	if res.Entries != 8+1 {
		t.Fatalf("removed %d entries, want 9: %+v", res.Entries, res)
	}
}

func TestGCProjectsSharedDocsHaveTheirOwnConservativeTTL(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "proj", gcProject, "shared", "docs")
	gcWrite(t, filepath.Join(docs, "d20.md"), 10, day(20))
	gcWrite(t, filepath.Join(docs, "d29.md"), 10, day(29))
	gcWrite(t, filepath.Join(docs, "d31.md"), 10, day(31))
	gcWrite(t, filepath.Join(docs, "fresh.md"), 10, time.Hour)
	// Locks beside docs are not a GC area.
	locks := gcWrite(t, filepath.Join(root, "proj", gcProject, "shared", "locks", "old.lock"), 1, day(90))
	// A worktree directory has no shared/ of its own to age.
	wtDocs := gcWrite(t, filepath.Join(root, "proj", gcWorktree, "shared", "docs", "old.md"), 1, day(90))

	GCProjects(root, gcOptions())

	for name, want := range map[string]bool{"d20.md": true, "d29.md": true, "d31.md": false, "fresh.md": true} {
		if got := gcExists(filepath.Join(docs, name)); got != want {
			t.Errorf("shared/docs/%s exists = %v, want %v", name, got, want)
		}
	}
	if !gcExists(locks) || !gcExists(wtDocs) {
		t.Error("GC touched shared/locks or a worktree-keyed shared/docs")
	}
}

func TestGCProjectsLRUCapEvictsOldestAndSparesGrace(t *testing.T) {
	root := t.TempDir()
	review := filepath.Join(root, "proj", gcProject, "review-paths")
	docs := filepath.Join(root, "proj", gcProject, "shared", "docs")
	// 100-byte cap per area.
	opts := gcOptions()
	opts.MaxBytes = 100
	gcWrite(t, filepath.Join(review, "a-oldest.md"), 40, day(10))
	gcWrite(t, filepath.Join(review, "b-older.md"), 40, day(5))
	gcWrite(t, filepath.Join(review, "c-newer.md"), 40, day(2))
	gcWrite(t, filepath.Join(review, "d-fresh.md"), 40, time.Hour)
	for i, name := range []string{"x.md", "y.md", "z.md"} {
		gcWrite(t, filepath.Join(docs, name), 60, day(20-i))
	}

	GCProjects(root, opts)

	for name, want := range map[string]bool{"a-oldest.md": false, "b-older.md": false, "c-newer.md": true, "d-fresh.md": true} {
		if got := gcExists(filepath.Join(review, name)); got != want {
			t.Errorf("review-paths/%s exists = %v, want %v", name, got, want)
		}
	}
	// 3 x 60 > 100: the oldest two go until the rest fits.
	for name, want := range map[string]bool{"x.md": false, "y.md": false, "z.md": true} {
		if got := gcExists(filepath.Join(docs, name)); got != want {
			t.Errorf("shared/docs/%s exists = %v, want %v", name, got, want)
		}
	}

	// A cap that only fresh entries exceed removes nothing: the grace wins.
	fresh := filepath.Join(root, "proj", gcWorktree, "tmp")
	for _, name := range []string{"p", "q", "r"} {
		gcWrite(t, filepath.Join(fresh, name), 60, 2*time.Hour)
	}
	GCProjects(root, opts)
	for _, name := range []string{"p", "q", "r"} {
		if !gcExists(filepath.Join(fresh, name)) {
			t.Errorf("tmp/%s younger than 24h was evicted by the cap", name)
		}
	}
}

func TestGCProjectsLeavesUnknownFoldersAndFilesAlone(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	adhoc := gcWrite(t, filepath.Join(proj, gcProject, "my-notes", "old.txt"), 10, day(400))
	gcAge(t, filepath.Dir(adhoc), day(400))
	keep := []string{
		adhoc,
		gcWrite(t, filepath.Join(proj, gcProject, "notes.json"), 10, day(400)),
		gcWrite(t, filepath.Join(proj, gcProject, "state.sqlite"), 10, day(400)),
		gcWrite(t, filepath.Join(proj, gcProject, "sessions", "s", "x"), 10, day(400)),
		gcWrite(t, filepath.Join(proj, gcProject, "agents", "a.json"), 10, day(400)),
		gcWrite(t, filepath.Join(proj, gcProject, "exec-jobs", "j", "log"), 10, day(400)),
		gcWrite(t, filepath.Join(proj, gcProject, "shared", "locks", "l"), 10, day(400)),
		// Directly under proj/: not a worktree key, so never visited.
		gcWrite(t, filepath.Join(proj, "scratch", "review-paths", "old.md"), 10, day(400)),
		gcWrite(t, filepath.Join(proj, "0123abcdz", "tmp", "old"), 10, day(400)),
		gcWrite(t, filepath.Join(proj, "loose.txt"), 10, day(400)),
		gcWrite(t, filepath.Join(root, "dashboard", "tmp", "old"), 10, day(400)),
	}
	GCProjects(root, gcOptions())
	for _, path := range keep {
		if !gcExists(path) {
			t.Errorf("GC removed %s", path)
		}
	}
}

func writeWorktreeMeta(t *testing.T, dir, key, worktreePath string, seen time.Duration) {
	t.Helper()
	meta := WorktreeMetadata{SchemaVersion: 1, ProjectKey: strings.SplitN(key, "@", 2)[0], WorktreePath: worktreePath, WorktreeKey: key, LastSeenAt: gcNow.Add(-seen).Format(time.RFC3339)}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	gcWrite(t, filepath.Join(dir, "worktree.json"), 0, seen)
	if err := os.WriteFile(filepath.Join(dir, "worktree.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	gcAge(t, filepath.Join(dir, "worktree.json"), seen)
}

func TestGCProjectsRemovesOnlyProvablyDeadWorktrees(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	live := t.TempDir() // a worktree path that still exists
	gone := filepath.Join(t.TempDir(), "removed-worktree")

	mk := func(id string, path string, seen time.Duration, setup func(dir string)) string {
		dir := filepath.Join(proj, gcProject+"@"+id)
		gcWrite(t, filepath.Join(dir, "state.sqlite"), 4, seen)
		if path != "" {
			writeWorktreeMeta(t, dir, gcProject+"@"+id, path, seen)
		}
		if setup != nil {
			setup(dir)
		}
		gcAge(t, dir, seen)
		return dir
	}
	dead := mk("00000001", gone, day(15), nil)
	exists := mk("00000002", live, day(40), nil)
	recent := mk("00000003", gone, day(13), nil)
	noMeta := mk("00000004", "", day(40), nil)
	corrupt := mk("00000005", gone, day(40), func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, "worktree.json"), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		gcAge(t, filepath.Join(dir, "worktree.json"), day(40))
	})
	// A file inside changed recently (a tmp file written an hour ago).
	touched := mk("00000006", gone, day(40), func(dir string) {
		gcWrite(t, filepath.Join(dir, "tmp", "new"), 1, time.Hour)
	})
	touchedDir := mk("00000007", gone, day(40), nil)
	gcAge(t, touchedDir, time.Hour) // the directory itself changed recently
	// A daemon-known session still names this one's path.
	sessionPath := filepath.Join(t.TempDir(), "session-named")
	active := mk("00000008", sessionPath, day(40), nil)
	// A project (non-@) directory is never "dead".
	projectDir := filepath.Join(proj, gcProject)
	writeWorktreeMeta(t, projectDir, gcProject, gone, day(40))
	gcAge(t, projectDir, day(40))
	// A worktree.json naming another key is not trusted.
	mismatch := mk("00000009", gone, day(40), func(dir string) {
		writeWorktreeMeta(t, dir, gcProject+"@ffffffff", gone, day(40))
	})

	opts := gcOptions()
	opts.SessionActive = func(path string) bool { return path == sessionPath }

	res := GCProjects(root, opts)

	if gcExists(dead) {
		t.Error("a dead worktree directory (gone, 15d) survived")
	}
	for name, dir := range map[string]string{"exists": exists, "recent": recent, "noMeta": noMeta, "corrupt": corrupt, "touched": touched, "touchedDir": touchedDir, "active": active, "projectDir": projectDir, "mismatch": mismatch} {
		if !gcExists(dir) {
			t.Errorf("%s directory %s was removed", name, dir)
		}
	}
	if res.DeadWorktrees != 1 {
		t.Fatalf("dead worktrees removed = %d, want 1 (%+v)", res.DeadWorktrees, res)
	}
}

func TestGCProjectsStopsBetweenEntries(t *testing.T) {
	root := t.TempDir()
	review := filepath.Join(root, "proj", gcProject, "review-paths")
	for _, name := range []string{"a", "b", "c"} {
		gcWrite(t, filepath.Join(review, name), 1, day(30))
	}
	opts := gcOptions()
	opts.Stop = func() bool { return true }
	if res := GCProjects(root, opts); res.Entries != 0 {
		t.Fatalf("a stopped pass removed %d entries", res.Entries)
	}
}

func TestGCProjectsOptionsNeverGoBelowGrace(t *testing.T) {
	root := t.TempDir()
	review := filepath.Join(root, "proj", gcProject, "review-paths")
	young := gcWrite(t, filepath.Join(review, "young"), 1, 2*time.Hour)
	old := gcWrite(t, filepath.Join(review, "old"), 1, 25*time.Hour)
	opts := gcOptions()
	opts.TTL = time.Minute
	GCProjects(root, opts)
	if !gcExists(young) {
		t.Error("an entry under the 24h grace was removed by a tiny TTL")
	}
	if gcExists(old) {
		t.Error("an entry past grace and TTL survived")
	}
}

func TestGCProjectsMissingRootIsQuiet(t *testing.T) {
	if res := GCProjects(filepath.Join(t.TempDir(), "absent"), gcOptions()); res != (GCResult{}) {
		t.Fatalf("GCProjects on a missing root = %+v", res)
	}
}
