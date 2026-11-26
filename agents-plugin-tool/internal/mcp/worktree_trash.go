package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsgit"
)

// worktree.remove retires a checkout in O(1) on the request path: it renames
// the directory into its pool root's poolTrashDirName and the worktree's Git
// admin directory into the common dir's adminTrashDirName (both same-volume
// renames), then deletes both in the background. Neither trash directory is a
// registered worktree, and every pool scan also skips poolTrashDirName
// explicitly (inPoolTrash), so a trashed checkout is never listed, reused or
// nudged.
const (
	poolTrashDirName  = ".trash"
	adminTrashDirName = "ws-worktree-trash"

	// poolTrashGrace is how old a trash entry must be before a sweep deletes
	// it. A remove moves its checkout back out of the trash when unregistering
	// fails; that rollback runs within milliseconds of the move, and the
	// grace keeps another process's sweep from deleting the entry in between.
	poolTrashGrace = time.Minute
	// poolTrashSweepInterval spaces the sweeps of one repository's trash in
	// one process. The first worktree.* call (or dashboard rail read) per
	// repository after a process starts sweeps at once, so a daemon start
	// retries what an earlier process left behind.
	poolTrashSweepInterval = 30 * time.Minute
	// poolTrashSweepLimit bounds the entries one sweep deletes per trash
	// directory; the rest wait for the next sweep.
	poolTrashSweepLimit = 32
	// poolTrashSweepTimeout bounds a sweep's git reads.
	poolTrashSweepTimeout = 10 * time.Second

	// worktreeOrphanLabel is the profile worktree.remove reports for an
	// unregistered pool directory it cleaned up.
	worktreeOrphanLabel = "orphan"
)

// renameWorktreeAside moves a checkout into, or (rollback) back out of, its
// pool trash; unregisterWorktreeAdmin moves a worktree's admin directory into
// the common dir's trash. Variables so a test can make either fail: the
// Windows CWD lock that fails a real rename cannot be produced on Linux.
var (
	renameWorktreeAside     = os.Rename
	unregisterWorktreeAdmin = os.Rename
)

// worktreeTrash is the process's trash deleter, shared by every server the
// process hosts.
var worktreeTrash = newTrashBin()

// trashBin deletes trash entries on one background goroutine, started when
// work is queued and ended when the queue drains (no polling). removeAll and
// now are fixed at construction; tests build their own bin.
type trashBin struct {
	removeAll func(string) error
	now       func() time.Time

	mu      sync.Mutex
	tasks   []func()
	running bool
	swept   map[string]time.Time
	pending sync.WaitGroup
}

func newTrashBin() *trashBin {
	return &trashBin{removeAll: os.RemoveAll, now: time.Now, swept: map[string]time.Time{}}
}

func (b *trashBin) submit(task func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending.Add(1)
	b.tasks = append(b.tasks, task)
	if !b.running {
		b.running = true
		go b.run()
	}
}

func (b *trashBin) run() {
	for {
		b.mu.Lock()
		if len(b.tasks) == 0 {
			b.running = false
			b.mu.Unlock()
			return
		}
		task := b.tasks[0]
		b.tasks = b.tasks[1:]
		b.mu.Unlock()
		task()
		b.pending.Done()
	}
}

// wait blocks until every queued task has run (tests).
func (b *trashBin) wait() { b.pending.Wait() }

// discard queues path for deletion. A failed deletion stays in the trash for
// a later sweep.
func (b *trashBin) discard(path string) {
	b.submit(func() { _ = b.removeAll(path) })
}

// maybeSweep queues a sweep of the trash directories of the repository at
// root (each owned pool root's poolTrashDirName and the common dir's
// adminTrashDirName) unless one ran in this process within
// poolTrashSweepInterval. It returns at once: every git read, listing and
// deletion runs on the background goroutine.
func (b *trashBin) maybeSweep(runner wsgit.Runner, root, poolConfigValue string) {
	root = strings.TrimSpace(root)
	if root == "" || runner == nil {
		return
	}
	key := filepath.Clean(root) + "\x00" + poolConfigValue
	now := b.now()
	b.mu.Lock()
	if at, ok := b.swept[key]; ok && now.Sub(at) < poolTrashSweepInterval {
		b.mu.Unlock()
		return
	}
	b.swept[key] = now
	b.mu.Unlock()
	b.submit(func() {
		for _, dir := range trashDirs(runner, root, poolConfigValue) {
			b.sweepDir(dir)
		}
	})
}

// trashDirs lists the trash directories of the repository at root.
func trashDirs(runner wsgit.Runner, root, poolConfigValue string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), poolTrashSweepTimeout)
	defer cancel()
	entries, err := listWorktrees(ctx, runner, root)
	if err != nil || len(entries) == 0 {
		return nil
	}
	var dirs []string
	for _, pool := range ownedPoolRoots(poolConfigValue, entries[0].Path) {
		dirs = append(dirs, filepath.Join(pool, poolTrashDirName))
	}
	if common, err := wtRun(ctx, runner, root, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Clean(common), adminTrashDirName))
	}
	return dirs
}

// sweepDir deletes up to poolTrashSweepLimit entries of dir older than
// poolTrashGrace.
func (b *trashBin) sweepDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	deleted := 0
	for _, e := range entries {
		if deleted >= poolTrashSweepLimit {
			return
		}
		at, ok := trashEntryTime(e.Name())
		if !ok {
			info, err := e.Info()
			if err != nil {
				continue
			}
			at = info.ModTime()
		}
		if b.now().Sub(at) < poolTrashGrace {
			continue
		}
		_ = b.removeAll(filepath.Join(dir, e.Name()))
		deleted++
	}
}

const trashTimeLayout = "20060102T150405Z"

// newTrashName names a trash entry for base: base, its retirement time and a
// random suffix, so a sweep can tell its age without a stat (a rename keeps a
// directory's mtime) and two removals never collide.
func newTrashName(base string, at time.Time) (string, error) {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return base + "." + at.UTC().Format(trashTimeLayout) + "." + hex.EncodeToString(suffix[:]), nil
}

// trashEntryTime is the retirement time newTrashName encoded in name.
func trashEntryTime(name string) (time.Time, bool) {
	rest, suffix, ok := cutLast(name, ".")
	if !ok || len(suffix) != 8 {
		return time.Time{}, false
	}
	_, stamp, ok := cutLast(rest, ".")
	if !ok {
		return time.Time{}, false
	}
	at, err := time.Parse(trashTimeLayout, stamp)
	return at, err == nil
}

func cutLast(s, sep string) (string, string, bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// inPoolTrash reports whether path is a pool root's trash directory or lies
// inside one.
func inPoolTrash(poolRoots []string, path string) bool {
	for _, root := range poolRoots {
		if root != "" && pathUnder(filepath.Join(root, poolTrashDirName), path) {
			return true
		}
	}
	return false
}

// containingPoolRoot is the longest pool root path lies at or below, or "".
func containingPoolRoot(poolRoots []string, path string) string {
	best := ""
	for _, root := range poolRoots {
		if root != "" && pathUnder(root, path) && len(root) > len(best) {
			best = filepath.Clean(root)
		}
	}
	return best
}

// canonicalPath resolves path's symlinks when it exists, else cleans it.
func canonicalPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(real)
	}
	return filepath.Clean(path)
}

// plainDirectory reports whether info is a directory that is not a symlink,
// junction or other reparse point.
func plainDirectory(info fs.FileInfo) bool {
	return info.Mode().Type() == fs.ModeDir
}

// orphanPoolDir is an unregistered directory under an owned pool root that
// worktree.remove may clean up.
type orphanPoolDir struct {
	target   string // canonical directory path
	poolRoot string // the owned pool root holding it
}

// resolveOrphanPoolDir decides whether wtPath names an unregistered directory
// inside the owned pool of the caller's repository (callerRoot), resolving it
// by filesystem path: git cannot resolve such a directory (rev-parse fails, or
// finds an enclosing repository), and the worktree list cannot hold it. ok is
// false for anything the registered-worktree path should handle or refuse: no
// caller repository, a missing path, a path outside the pool, or one at or
// inside a registered worktree. A pool root itself, the pool trash, and a
// symlink or junction are refused here.
func resolveOrphanPoolDir(ctx context.Context, runner wsgit.Runner, callerRoot, wtPath, poolConfigValue string) (orphanPoolDir, bool, error) {
	wtPath = strings.TrimSpace(wtPath)
	if strings.TrimSpace(callerRoot) == "" || wtPath == "" {
		return orphanPoolDir{}, false, nil
	}
	abs, err := filepath.Abs(wtPath)
	if err != nil {
		return orphanPoolDir{}, false, nil
	}
	if _, err := os.Lstat(abs); err != nil {
		return orphanPoolDir{}, false, nil
	}
	entries, err := listWorktrees(ctx, runner, callerRoot)
	if err != nil || len(entries) == 0 {
		return orphanPoolDir{}, false, nil
	}
	target := canonicalPath(abs)
	var roots []string
	for _, r := range ownedPoolRoots(poolConfigValue, entries[0].Path) {
		roots = append(roots, canonicalPath(r))
	}
	root := containingPoolRoot(roots, target)
	if root == "" {
		return orphanPoolDir{}, false, nil
	}
	if registeredAtOrAbove(entries, target) {
		return orphanPoolDir{}, false, nil
	}
	// Inside the primary checkout only an in-tree pool's directories qualify
	// (a pool configured above the primary root does not make its files
	// orphans).
	if primary := canonicalPath(entries[0].Path); pathUnder(primary, target) && !pathUnder(primary, root) {
		return orphanPoolDir{}, false, nil
	}
	if target == root {
		return orphanPoolDir{}, false, fmt.Errorf("worktree.remove refuses %s: it is the worktree pool root itself, not a worktree. Nothing was changed", target)
	}
	if inPoolTrash(roots, target) {
		return orphanPoolDir{}, false, fmt.Errorf("worktree.remove refuses %s: it is in the pool's %s directory, which ws empties in the background. Nothing was changed", target, poolTrashDirName)
	}
	for _, p := range []string{abs, target} {
		info, err := os.Lstat(p)
		if err != nil {
			return orphanPoolDir{}, false, fmt.Errorf("worktree.remove: inspect %s: %w", p, err)
		}
		if !plainDirectory(info) {
			return orphanPoolDir{}, false, fmt.Errorf("worktree.remove refuses %s: it is not a registered git worktree, and it is a symlink, junction or non-directory rather than a plain directory. Nothing was changed", p)
		}
	}
	return orphanPoolDir{target: target, poolRoot: root}, true, nil
}

// registeredAtOrAbove reports whether target is a registered linked worktree
// or lies inside one. entries is the worktree list, primary first; the
// primary is skipped, since the legacy in-tree pool lies inside it.
func registeredAtOrAbove(entries []worktreeEntry, target string) bool {
	for i, e := range entries {
		if i == 0 {
			continue
		}
		if pathUnder(canonicalPath(e.Path), target) {
			return true
		}
	}
	return false
}

// orphanOffender returns the first entry under dir (relative to it, with a
// description) that is not a plain directory, or a .git entry of any kind.
// An orphan holding only directories is what Git leaves behind after a
// removal whose final directory deletion failed; anything else may be work.
func orphanOffender(dir string) (string, error) {
	var offender string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		switch {
		case d.Name() == ".git":
			offender = rel + " (a .git entry)"
		case d.Type()&fs.ModeSymlink != 0:
			offender = rel + " (a symlink)"
		case d.Type() == fs.ModeDir:
			return nil
		case d.Type().IsRegular():
			offender = rel + " (a regular file)"
		default:
			offender = rel + " (a junction or other non-directory entry)"
		}
		return filepath.SkipAll
	})
	if err != nil {
		return "", err
	}
	return offender, nil
}

// removeOrphanPoolDir cleans up an unregistered pool directory under the pool
// acquisition lock (so it never takes the fresh directory of an acquire partway
// through git worktree add): it re-checks that the directory is still
// unregistered, accepts it only when it holds directories alone, and moves it
// into the pool trash for background deletion.
func removeOrphanPoolDir(ctx context.Context, runner wsgit.Runner, callerRoot string, orphan orphanPoolDir) (worktreeRemoveResult, error) {
	res := worktreeRemoveResult{Path: orphan.target, Profile: worktreeOrphanLabel}
	unlock, err := lockWorktreePool(ctx, runner, callerRoot, "worktree.remove")
	if err != nil {
		return res, err
	}
	defer unlock()
	entries, err := listWorktrees(ctx, runner, callerRoot)
	if err != nil {
		return res, fmt.Errorf("worktree.remove: %w", err)
	}
	if registeredAtOrAbove(entries, orphan.target) {
		return res, fmt.Errorf("worktree.remove refuses %s: it became a registered git worktree while this call checked it. Nothing was changed; remove again", orphan.target)
	}
	offender, err := orphanOffender(orphan.target)
	if err != nil {
		return res, fmt.Errorf("worktree.remove: inspect %s: %w. Nothing was changed", orphan.target, err)
	}
	if offender != "" {
		return res, fmt.Errorf("worktree.remove refuses %s: it is not a registered git worktree, and it still holds %s. Only a directory left with empty directories by an interrupted removal is cleaned up; nothing was changed. Check that entry and delete it yourself if it is not needed", orphan.target, offender)
	}
	aside, err := moveIntoPoolTrash(orphan.target, orphan.poolRoot)
	if err != nil {
		return res, fmt.Errorf("%w. Nothing was changed", err)
	}
	worktreeTrash.discard(aside)
	res.Removed = true
	res.Notes = append(res.Notes, fmt.Sprintf("it was not a registered git worktree (a directory an interrupted removal left behind); its empty directories are deleted in the background from %s", aside))
	return res, nil
}

// moveIntoPoolTrash renames dir into poolRoot's trash and returns its new
// path. A rename that fails (a process outside ws holding a file or its
// working directory open there on Windows, or a cross-volume move) changes
// nothing; there is no copy-and-delete fallback.
func moveIntoPoolTrash(dir, poolRoot string) (string, error) {
	trash := filepath.Join(poolRoot, poolTrashDirName)
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return "", fmt.Errorf("worktree.remove: create %s: %v", trash, err)
	}
	name, err := newTrashName(filepath.Base(dir), worktreeTrash.now())
	if err != nil {
		return "", fmt.Errorf("worktree.remove: name a trash entry for %s: %v", dir, err)
	}
	aside := filepath.Join(trash, name)
	if err := renameWorktreeAside(dir, aside); err != nil {
		return "", fmt.Errorf("worktree.remove could not move %s aside to %s (%v); a process outside ws may still have a file or its working directory open inside it (close it, then remove again)", dir, aside, err)
	}
	return aside, nil
}

// retireRegisteredWorktree moves a registered worktree's checkout into the
// pool trash, then unregisters it by moving its admin directory (adminDir,
// under commonDir/worktrees) into the common dir's trash, and queues both for
// background deletion. If unregistering fails the checkout is moved back, so
// a registered worktree never loses its directory; if that rollback fails too,
// the error names both paths.
func retireRegisteredWorktree(target, poolRoot, adminDir, commonDir string) (string, error) {
	adminTrash := filepath.Join(commonDir, adminTrashDirName)
	if err := os.MkdirAll(adminTrash, 0o755); err != nil {
		return "", fmt.Errorf("worktree.remove: create %s: %v. Nothing was removed", adminTrash, err)
	}
	aside, err := moveIntoPoolTrash(target, poolRoot)
	if err != nil {
		return "", fmt.Errorf("%w. Nothing was removed, and it is still a registered worktree", err)
	}
	adminAside := filepath.Join(adminTrash, filepath.Base(aside))
	if err := unregisterWorktreeAdmin(adminDir, adminAside); err != nil {
		if rbErr := renameWorktreeAside(aside, target); rbErr != nil {
			return "", fmt.Errorf("worktree.remove: unregistering %s failed (%v) after its directory was moved aside, and moving it back failed too (%v). Its files are now at %s while git still registers it at %s. Move %s back to %s, then remove again", target, err, rbErr, aside, target, aside, target)
		}
		return "", fmt.Errorf("worktree.remove: could not unregister %s by moving %s aside (%v); its directory was moved back, so nothing was changed", target, adminDir, err)
	}
	worktreeTrash.discard(aside)
	worktreeTrash.discard(adminAside)
	return aside, nil
}

// worktreeAdminLayout resolves target's admin directory and the common dir,
// and checks the admin directory is commonDir/worktrees/<id> (the only layout
// remove unregisters by moving the admin directory).
func worktreeAdminLayout(ctx context.Context, runner wsgit.Runner, target, mainRoot string) (adminDir, commonDir string, err error) {
	adminDir, err = worktreeAdminDir(ctx, runner, target)
	if err != nil {
		return "", "", err
	}
	common, err := wtRun(ctx, runner, mainRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	commonDir = canonicalPath(common)
	adminDir = canonicalPath(adminDir)
	if filepath.Dir(adminDir) != filepath.Join(commonDir, "worktrees") {
		return "", "", fmt.Errorf("its git admin directory %s is not under %s", adminDir, filepath.Join(commonDir, "worktrees"))
	}
	return adminDir, commonDir, nil
}
