package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsgit"
)

// useTrashBin installs a fresh process trash bin for one test and, at
// cleanup, waits for its queued deletions before restoring the previous one.
func useTrashBin(t *testing.T, removeAll func(string) error) *trashBin {
	t.Helper()
	b := newTrashBin()
	if removeAll != nil {
		b.removeAll = removeAll
	}
	prev := worktreeTrash
	worktreeTrash = b
	t.Cleanup(func() {
		b.wait()
		worktreeTrash = prev
	})
	return b
}

// swapSeam replaces *seam with fn for one test.
func swapSeam(t *testing.T, seam *func(string, string) error, fn func(string, string) error) {
	t.Helper()
	prev := *seam
	*seam = fn
	t.Cleanup(func() { *seam = prev })
}

func dirEntryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func commonDirForTest(t *testing.T, root string) string {
	t.Helper()
	return filepath.Clean(strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "--path-format=absolute", "--git-common-dir"))))
}

// removableWorktree provisions a clean, merged worktree on develop.
func removableWorktree(t *testing.T, root string) worktreeAcquireResult {
	t.Helper()
	wt, err := provisionWorktree(context.Background(), wsgit.ExecRunner{}, nil, root, "develop", "", builtinProfile(t, worktreeProfileFull), "")
	if err != nil {
		t.Fatal(err)
	}
	return wt
}

// TestRemoveWorktreeRetiresIntoTrashAndDeletesInBackground: remove returns
// once the checkout is renamed into the pool trash and its admin directory
// into the common dir's trash, while deletion is still blocked; the
// background deleter then empties both.
func TestRemoveWorktreeRetiresIntoTrashAndDeletesInBackground(t *testing.T) {
	root := profileWorktreeFixture(t)
	ctx := context.Background()
	runner := wsgit.ExecRunner{}
	gate := make(chan struct{})
	var mu sync.Mutex
	var deleted []string
	b := useTrashBin(t, func(p string) error {
		<-gate
		mu.Lock()
		deleted = append(deleted, p)
		mu.Unlock()
		return os.RemoveAll(p)
	})
	wt := removableWorktree(t, root)
	writeFileForTest(t, wt.Path, "out/big.bin", "ignored build output")
	adminDir, err := worktreeAdminDir(ctx, runner, wt.Path)
	if err != nil {
		t.Fatal(err)
	}

	lookup := builtinProfileLookup(t)
	done := make(chan struct{})
	var res worktreeRemoveResult
	go func() {
		defer close(done)
		res, err = remove(ctx, runner, wt.Path, lookup)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		close(gate)
		t.Fatal("remove waited on the background deletion")
	}
	if err != nil || !res.Removed {
		t.Fatalf("remove = %+v, %v", res, err)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "deleted in the background") {
		t.Fatalf("remove notes = %q", res.Notes)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("checkout still at its path: %v", err)
	}
	if _, err := os.Stat(adminDir); !os.IsNotExist(err) {
		t.Fatalf("admin directory still registered: %v", err)
	}
	if worktreeRegistered(t, root, wt.Path) {
		t.Fatal("removed worktree still registered")
	}
	trash := filepath.Join(wt.Pool, poolTrashDirName)
	adminTrash := filepath.Join(commonDirForTest(t, root), adminTrashDirName)
	names := dirEntryNames(t, trash)
	if len(names) != 1 || !strings.HasPrefix(names[0], filepath.Base(wt.Path)+".") {
		t.Fatalf("pool trash = %q", names)
	}
	if _, err := os.Stat(filepath.Join(trash, names[0], "out", "big.bin")); err != nil {
		t.Fatalf("trashed checkout lost its files before the background deletion: %v", err)
	}
	if got := dirEntryNames(t, adminTrash); len(got) != 1 || got[0] != names[0] {
		t.Fatalf("admin trash = %q, want %q", got, names[0])
	}
	// The trashed checkout is in no pool scan.
	list, err := listPoolWorktrees(ctx, runner, root, "", func(string) (time.Time, bool) { return time.Time{}, false })
	if err != nil || len(list.Worktrees) != 0 {
		t.Fatalf("list after remove = %+v, %v", list.Worktrees, err)
	}

	close(gate)
	b.wait()
	if got := dirEntryNames(t, trash); len(got) != 0 {
		t.Fatalf("pool trash after deletion = %q", got)
	}
	if got := dirEntryNames(t, adminTrash); len(got) != 0 {
		t.Fatalf("admin trash after deletion = %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deleted) != 2 {
		t.Fatalf("background deletions = %q", deleted)
	}
}

// untouchedWorktree asserts a refused or rolled-back remove left wt
// registered, with its files and admin directory, and nothing in the trash.
func untouchedWorktree(t *testing.T, root string, wt worktreeAcquireResult, adminDir string) {
	t.Helper()
	if !worktreeRegistered(t, root, wt.Path) {
		t.Fatal("worktree no longer registered")
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "src", "y.go")); err != nil {
		t.Fatalf("worktree files gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(adminDir, "HEAD")); err != nil {
		t.Fatalf("admin directory damaged: %v", err)
	}
	if _, err := wsgit.PorcelainPaths(context.Background(), wsgit.ExecRunner{}, wt.Path); err != nil {
		t.Fatalf("git no longer reads the worktree: %v", err)
	}
	if got := dirEntryNames(t, filepath.Join(wt.Pool, poolTrashDirName)); len(got) != 0 {
		t.Fatalf("pool trash = %q", got)
	}
	if got := dirEntryNames(t, filepath.Join(commonDirForTest(t, root), adminTrashDirName)); len(got) != 0 {
		t.Fatalf("admin trash = %q", got)
	}
}

// TestRemoveWorktreeRenameAsideFailureLeavesItIntact: a rename that fails
// (the Windows CWD lock of a process outside ws) leaves the worktree
// registered with its files, and says so.
func TestRemoveWorktreeRenameAsideFailureLeavesItIntact(t *testing.T) {
	root := profileWorktreeFixture(t)
	ctx := context.Background()
	runner := wsgit.ExecRunner{}
	useTrashBin(t, nil)
	wt := removableWorktree(t, root)
	adminDir, err := worktreeAdminDir(ctx, runner, wt.Path)
	if err != nil {
		t.Fatal(err)
	}
	swapSeam(t, &renameWorktreeAside, func(string, string) error { return errors.New("simulated sharing violation") })
	_, err = remove(ctx, runner, wt.Path, builtinProfileLookup(t))
	if err == nil {
		t.Fatal("remove must fail when the rename aside fails")
	}
	for _, want := range []string{"could not move", "simulated sharing violation", "working directory open", "Nothing was removed", "still a registered worktree"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
	untouchedWorktree(t, root, wt, adminDir)
}

// TestRemoveWorktreeUnregisterFailureRollsBack: when unregistering fails
// after the rename aside, the checkout moves back and stays registered.
func TestRemoveWorktreeUnregisterFailureRollsBack(t *testing.T) {
	root := profileWorktreeFixture(t)
	ctx := context.Background()
	runner := wsgit.ExecRunner{}
	useTrashBin(t, nil)
	wt := removableWorktree(t, root)
	adminDir, err := worktreeAdminDir(ctx, runner, wt.Path)
	if err != nil {
		t.Fatal(err)
	}
	swapSeam(t, &unregisterWorktreeAdmin, func(string, string) error { return errors.New("simulated admin lock") })
	_, err = remove(ctx, runner, wt.Path, builtinProfileLookup(t))
	if err == nil || !strings.Contains(err.Error(), "simulated admin lock") || !strings.Contains(err.Error(), "moved back, so nothing was changed") {
		t.Fatalf("remove with a failing unregister = %v", err)
	}
	untouchedWorktree(t, root, wt, adminDir)
}

// TestRemoveWorktreeFailedRollbackIsLoud: when the rollback rename fails
// too, the error names the trash path and the registered path.
func TestRemoveWorktreeFailedRollbackIsLoud(t *testing.T) {
	root := profileWorktreeFixture(t)
	ctx := context.Background()
	runner := wsgit.ExecRunner{}
	useTrashBin(t, nil)
	wt := removableWorktree(t, root)
	swapSeam(t, &unregisterWorktreeAdmin, func(string, string) error { return errors.New("simulated admin lock") })
	calls := 0
	swapSeam(t, &renameWorktreeAside, func(from, to string) error {
		calls++
		if calls == 2 {
			return errors.New("simulated rollback lock")
		}
		return os.Rename(from, to)
	})
	_, err := remove(ctx, runner, wt.Path, builtinProfileLookup(t))
	trash := filepath.Join(wt.Pool, poolTrashDirName)
	names := dirEntryNames(t, trash)
	if len(names) != 1 {
		t.Fatalf("pool trash = %q, want the stranded checkout", names)
	}
	aside := filepath.Join(trash, names[0])
	if err == nil {
		t.Fatal("remove must fail")
	}
	for _, want := range []string{"simulated admin lock", "simulated rollback lock", aside, wt.Path, "Move " + aside + " back to " + wt.Path} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
	if !worktreeRegistered(t, root, wt.Path) {
		t.Fatal("a failed unregister must leave the registration")
	}
	if _, err := os.Stat(filepath.Join(aside, "src", "y.go")); err != nil {
		t.Fatalf("stranded checkout lost its files: %v", err)
	}
}

// TestRemoveWorktreeRefusesLockedWorktree: a locked worktree is refused
// before any hook or change, as git worktree remove --force refuses it.
func TestRemoveWorktreeRefusesLockedWorktree(t *testing.T) {
	root := profileWorktreeFixture(t)
	ctx := context.Background()
	runner := wsgit.ExecRunner{}
	useTrashBin(t, nil)
	profile := hookProfile(t, "build", nil, map[string][]map[string]any{
		worktreeHookPreRemove: {hook(hookArgv(t, "env", "out/pre_remove.txt"))},
	})
	wt, err := provisionWorktree(ctx, runner, nil, root, "develop", "", profile, "")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "worktree", "lock", "--reason", "on a removable disk", wt.Path)
	_, err = remove(ctx, runner, wt.Path, lookupOf(profile))
	if err == nil || !strings.Contains(err.Error(), "locked (on a removable disk)") || !strings.Contains(err.Error(), "Nothing was changed") {
		t.Fatalf("remove of a locked worktree = %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "out", "pre_remove.txt")); err == nil {
		t.Fatal("a refused remove ran pre_remove")
	}
	if !worktreeRegistered(t, root, wt.Path) {
		t.Fatal("a refused remove unregistered the worktree")
	}
}

// TestPoolTrashSweep: a sweep runs in the background, deletes trash entries
// older than the grace period in both trash directories, skips younger ones
// (a remove's rollback window), and repeats at most once per interval.
func TestPoolTrashSweep(t *testing.T) {
	root := profileWorktreeFixture(t)
	runner := wsgit.ExecRunner{}
	pool := resolvePoolRoot("", root)
	trash := filepath.Join(pool, poolTrashDirName)
	adminTrash := filepath.Join(commonDirForTest(t, root), adminTrashDirName)
	var offset atomic.Int64
	clock := func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	entry := func(dir, base string, at time.Time) string {
		t.Helper()
		name, err := newTrashName(base, at)
		if err != nil {
			t.Fatal(err)
		}
		writeFileForTest(t, filepath.Join(dir, name), "deep/file.txt", "x")
		return filepath.Join(dir, name)
	}
	old := entry(trash, "old", time.Now().Add(-time.Hour))
	young := entry(trash, "young", time.Now())
	oldAdmin := entry(adminTrash, "oldadmin", time.Now().Add(-time.Hour))

	gate := make(chan struct{})
	b := newTrashBin()
	b.now = clock
	b.removeAll = func(p string) error {
		<-gate
		return os.RemoveAll(p)
	}
	returned := make(chan struct{})
	go func() {
		b.maybeSweep(runner, root, "")
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(30 * time.Second):
		close(gate)
		t.Fatal("maybeSweep blocked its caller on the deletion")
	}
	close(gate)
	b.wait()
	for _, gone := range []string{old, oldAdmin} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("sweep kept the old entry %s: %v", gone, err)
		}
	}
	if _, err := os.Stat(young); err != nil {
		t.Fatalf("sweep deleted an entry within the grace period: %v", err)
	}

	again := entry(trash, "again", time.Now().Add(-time.Hour))
	b.maybeSweep(runner, root, "")
	b.wait()
	if _, err := os.Stat(again); err != nil {
		t.Fatalf("a second sweep within the interval ran: %v", err)
	}
	offset.Store(int64(poolTrashSweepInterval + time.Minute))
	b.maybeSweep(runner, root, "")
	b.wait()
	for _, gone := range []string{again, young} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("the sweep after the interval kept %s: %v", gone, err)
		}
	}
}

func TestTrashEntryTime(t *testing.T) {
	at := time.Date(2026, 11, 26, 4, 5, 6, 0, time.UTC)
	name, err := newTrashName("a.b-c", at)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := trashEntryTime(name)
	if !ok || !got.Equal(at) {
		t.Fatalf("trashEntryTime(%q) = %v, %v", name, got, ok)
	}
	for _, bad := range []string{"plain", "x.notatime.0123abcd", "x.20261126T040506Z.short"} {
		if _, ok := trashEntryTime(bad); ok {
			t.Fatalf("trashEntryTime(%q) parsed", bad)
		}
	}
}

// TestPoolTrashExcludedFromPoolScans: a worktree registered inside the pool
// trash is never listed, reused, nudged, released or removed.
func TestPoolTrashExcludedFromPoolScans(t *testing.T) {
	root := profileWorktreeFixture(t)
	ctx := context.Background()
	runner := wsgit.ExecRunner{}
	useTrashBin(t, nil)
	pool := resolvePoolRoot("", root)
	detachedGhost := filepath.Join(pool, poolTrashDirName, "ghost-detached")
	branchGhost := filepath.Join(pool, poolTrashDirName, "ghost-branch")
	runGit(t, root, "worktree", "add", "--detach", detachedGhost, "develop")
	runGit(t, root, "worktree", "add", "-b", "ghost", branchGhost, "develop")
	detachedGhost, branchGhost = canonicalRootForTest(t, detachedGhost), canonicalRootForTest(t, branchGhost)

	list, err := listPoolWorktrees(ctx, runner, root, "", func(string) (time.Time, bool) { return time.Time{}, false })
	if err != nil || len(list.Worktrees) != 0 {
		t.Fatalf("list = %+v, %v; want the trash excluded", list.Worktrees, err)
	}
	acq, err := provisionWorktree(ctx, runner, nil, root, "develop", "", builtinProfile(t, worktreeProfileFull), "")
	if err != nil {
		t.Fatal(err)
	}
	if acq.Reused || acq.Path == detachedGhost {
		t.Fatalf("acquire reused a worktree in the trash: %+v", acq)
	}
	lines, err := poolNudge(ctx, runner, root, pool, 0, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		if strings.Contains(line, poolTrashDirName) {
			t.Fatalf("nudge lists a worktree in the trash: %q", lines)
		}
	}
	if _, err := remove(ctx, runner, detachedGhost, builtinProfileLookup(t)); err == nil || !strings.Contains(err.Error(), "pool's .trash") {
		t.Fatalf("remove of a trash worktree = %v", err)
	}
	if _, err := releaseWorktree(ctx, runner, nil, branchGhost, "", builtinProfileLookup(t)); err == nil || !strings.Contains(err.Error(), "pool's .trash") {
		t.Fatalf("release of a trash worktree = %v", err)
	}
}

func removeOrphanForTest(t *testing.T, root, path string) (worktreeRemoveResult, error) {
	t.Helper()
	return removeWorktreeFenced(context.Background(), wsgit.ExecRunner{}, nil, path, "", testStaleAfter, time.Now(), builtinProfileLookup(t), nil, nil, root)
}

func mkdirForTest(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRemoveOrphanPoolDirectory: an unregistered pool directory holding only
// directories (what git leaves when its final directory deletion fails) is
// cleaned up through the trash, in the out-of-tree pool and the legacy
// in-tree one; anything else is refused without a change.
func TestRemoveOrphanPoolDirectory(t *testing.T) {
	root := profileWorktreeFixture(t)
	b := useTrashBin(t, nil)
	pool := resolvePoolRoot("", root)
	legacy := resolvePoolRoot(legacyInTreePoolTemplate, root)

	for _, dir := range []string{filepath.Join(pool, "orphan-a"), filepath.Join(legacy, "orphan-b")} {
		mkdirForTest(t, filepath.Join(dir, "src", "deep"))
		mkdirForTest(t, filepath.Join(dir, "node_modules"))
		want := canonicalPath(dir)
		res, err := removeOrphanForTest(t, root, dir)
		if err != nil || !res.Removed || res.Profile != worktreeOrphanLabel || res.Path != want {
			t.Fatalf("remove orphan %s = %+v, %v", dir, res, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("orphan %s still in place: %v", dir, err)
		}
	}
	b.wait()
	for _, p := range []string{pool, legacy} {
		if got := dirEntryNames(t, filepath.Join(p, poolTrashDirName)); len(got) != 0 {
			t.Fatalf("trash of %s after deletion = %q", p, got)
		}
	}

	refuse := func(path, want string) {
		t.Helper()
		_, err := removeOrphanForTest(t, root, path)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("remove(%s) = %v, want a refusal containing %q", path, err, want)
		}
		if _, statErr := os.Lstat(path); statErr != nil {
			t.Fatalf("a refused remove changed %s: %v", path, statErr)
		}
	}
	withFile := mkdirForTest(t, filepath.Join(pool, "orphan-file", "a"))
	writeFileForTest(t, withFile, "x.txt", "work")
	refuse(filepath.Dir(withFile), filepath.Join("a", "x.txt")+" (a regular file)")
	withGit := mkdirForTest(t, filepath.Join(pool, "orphan-git"))
	writeFileForTest(t, withGit, ".git", "gitdir: /nonexistent/worktrees/x\n")
	refuse(withGit, ".git (a .git entry)")
	withLink := mkdirForTest(t, filepath.Join(pool, "orphan-link"))
	if err := os.Symlink(t.TempDir(), filepath.Join(withLink, "l")); err != nil {
		t.Fatal(err)
	}
	refuse(withLink, "l (a symlink)")
	real := mkdirForTest(t, filepath.Join(pool, "orphan-real"))
	linked := filepath.Join(pool, "orphan-linked")
	if err := os.Symlink(real, linked); err != nil {
		t.Fatal(err)
	}
	refuse(linked, "symlink, junction or non-directory")
	refuse(pool, "pool root itself")
	refuse(mkdirForTest(t, filepath.Join(pool, poolTrashDirName, "x")), "pool's .trash")
	refuse(filepath.Join(pool, poolTrashDirName), "pool's .trash")
	outside := mkdirForTest(t, filepath.Join(t.TempDir(), "stray"))
	if _, err := removeOrphanForTest(t, root, outside); err == nil {
		t.Fatal("remove of a directory outside the pool must fail")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("a refused remove changed %s: %v", outside, err)
	}
	if _, err := os.Stat(real); err != nil {
		t.Fatalf("a refused symlink remove changed its target: %v", err)
	}
}

// TestRemoveOrphanWaitsForPoolLock: the orphan cleanup runs under the pool
// acquisition lock, so it never takes the fresh directory of an acquire
// partway through git worktree add.
func TestRemoveOrphanWaitsForPoolLock(t *testing.T) {
	root := profileWorktreeFixture(t)
	useTrashBin(t, nil)
	dir := mkdirForTest(t, filepath.Join(resolvePoolRoot("", root), "half-added"))
	unlock, err := lockWorktreeAcquire(context.Background(), wsgit.ExecRunner{}, root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = removeOrphanForTest(t, root, dir)
	unlock()
	if err == nil || !strings.Contains(err.Error(), "contention") {
		t.Fatalf("orphan remove under a held pool lock = %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("orphan remove under a held pool lock changed the directory: %v", err)
	}
	if res, err := removeOrphanForTest(t, root, dir); err != nil || !res.Removed {
		t.Fatalf("orphan remove after the lock = %+v, %v", res, err)
	}
}

// TestWorktreeRemoveDispatchCompat: worktree.remove keeps its arguments
// (path, key, format) and its text and JSON fields, and resolves an orphan
// pool directory from the calling lead's repository.
func TestWorktreeRemoveDispatchCompat(t *testing.T) {
	s, root, base, leadKey := worktreeListServer(t)
	b := useTrashBin(t, nil)
	acq := acquireForTest(t, s, 1, leadKey, base, "impl/test/compat")
	if rel := callToolOnce(t, s, 2, "worktree.release", map[string]any{"session_key": leadKey, "path": acq.Path}); toolIsError(t, rel) {
		t.Fatalf("release: %s", rel)
	}
	resp := callToolOnce(t, s, 3, "worktree.remove", map[string]any{"session_key": leadKey, "path": acq.Path})
	if toolIsError(t, resp) {
		t.Fatalf("remove: %s", resp)
	}
	if text := toolText(t, resp); !strings.HasPrefix(text, "removed: "+acq.Path+"\nprofile: "+worktreeProfileFull+"\n") {
		t.Fatalf("remove text = %q", text)
	}

	acq = acquireForTest(t, s, 4, leadKey, base, "impl/test/compat2")
	if rel := callToolOnce(t, s, 5, "worktree.release", map[string]any{"session_key": leadKey, "key": acq.WorkerKey}); toolIsError(t, rel) {
		t.Fatalf("release: %s", rel)
	}
	resp = callToolOnce(t, s, 6, "worktree.remove", map[string]any{"session_key": leadKey, "path": acq.Path, "format": "json"})
	var fields map[string]any
	if err := json.Unmarshal([]byte(toolText(t, resp)), &fields); err != nil {
		t.Fatalf("unmarshal remove: %v\n%s", err, resp)
	}
	if fields["path"] != acq.Path || fields["removed"] != true || fields["profile"] != worktreeProfileFull {
		t.Fatalf("remove JSON = %v", fields)
	}

	orphan := mkdirForTest(t, filepath.Join(resolvePoolRoot("", canonicalRootForTest(t, root)), "orphan-d", "a"))
	resp = callToolOnce(t, s, 7, "worktree.remove", map[string]any{"session_key": leadKey, "path": filepath.Dir(orphan), "format": "json"})
	var res worktreeRemoveResult
	if err := json.Unmarshal([]byte(toolText(t, resp)), &res); err != nil {
		t.Fatalf("unmarshal orphan remove: %v\n%s", err, resp)
	}
	if !res.Removed || res.Profile != worktreeOrphanLabel {
		t.Fatalf("orphan remove = %+v", res)
	}
	b.wait()
}
