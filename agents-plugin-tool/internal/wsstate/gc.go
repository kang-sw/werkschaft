package wsstate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Cache GC defaults for proj/ (ticket 261129-feat-ws-cache-large-growth-gc).
const (
	// GCGrace is the age under which an entry is never removed, whatever the
	// TTL or the size cap says.
	GCGrace = 24 * time.Hour
	// GCProjTTL ages generated review/report/prompt paths and tmp.
	GCProjTTL = 14 * 24 * time.Hour
	// GCSharedDocsTTL ages clone-layer shared docs, which are kept longer.
	GCSharedDocsTTL = 30 * 24 * time.Hour
	// GCMaxBytes caps each GC'd area by total size, oldest entries first.
	GCMaxBytes int64 = 2 << 30
	// GCDeadWorktreeAge is how long a deleted worktree's cache directory is
	// kept after it was last seen.
	GCDeadWorktreeAge = 14 * 24 * time.Hour
)

// GCOptions tunes GCProjects. Zero fields take the defaults above.
type GCOptions struct {
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Grace, TTL, SharedDocsTTL, MaxBytes and DeadWorktreeAge override the
	// defaults. Grace is also the floor of every other age.
	Grace           time.Duration
	TTL             time.Duration
	SharedDocsTTL   time.Duration
	MaxBytes        int64
	DeadWorktreeAge time.Duration
	// SessionActive reports whether a daemon-known session still names
	// worktreePath. It is asked only for a dead-worktree candidate; nil means
	// no session does.
	SessionActive func(worktreePath string) bool
	// Stop, when it returns true, ends the pass between entries.
	Stop func() bool
}

// GCResult counts what one GCProjects pass removed.
type GCResult struct {
	Entries       int
	Bytes         int64
	DeadWorktrees int
}

func (o GCOptions) normalized() GCOptions {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Grace <= 0 {
		o.Grace = GCGrace
	}
	if o.TTL <= 0 {
		o.TTL = GCProjTTL
	}
	if o.SharedDocsTTL <= 0 {
		o.SharedDocsTTL = GCSharedDocsTTL
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = GCMaxBytes
	}
	if o.DeadWorktreeAge <= 0 {
		o.DeadWorktreeAge = GCDeadWorktreeAge
	}
	for _, ttl := range []*time.Duration{&o.TTL, &o.SharedDocsTTL, &o.DeadWorktreeAge} {
		if *ttl < o.Grace {
			*ttl = o.Grace
		}
	}
	return o
}

func (o GCOptions) stopped() bool { return o.Stop != nil && o.Stop() }

// GCProjects removes aged entries from the wsstate-owned areas under
// cacheRoot/proj. It is background maintenance: Ensure and GeneratePaths never
// call it and do no extra file I/O for it.
//
// Only directories named like a worktree key are visited, and inside one only
// review-paths, report-paths, prompt-paths and tmp (TTL, then the size cap) and
// shared/docs (its own TTL, then the cap) are touched. An unknown folder or
// file next to them is never read or removed. A directory of a deleted
// worktree (key project@worktree) goes whole once worktree.json proves the
// path is gone and unseen for DeadWorktreeAge.
func GCProjects(cacheRoot string, opts GCOptions) GCResult {
	opts = opts.normalized()
	var res GCResult
	projRoot := filepath.Join(cacheRoot, "proj")
	entries, err := os.ReadDir(projRoot)
	if err != nil {
		return res
	}
	for _, entry := range entries {
		if opts.stopped() {
			return res
		}
		name := entry.Name()
		if !entry.IsDir() || !validWorktreeKey(name) {
			continue
		}
		dir := filepath.Join(projRoot, name)
		if len(name) == 17 {
			if gcDeadWorktree(dir, name, opts, &res) {
				continue
			}
		}
		for _, area := range []string{"review-paths", "report-paths", "prompt-paths", "tmp"} {
			gcArea(filepath.Join(dir, area), opts.TTL, opts, &res)
		}
		if len(name) == 8 {
			gcArea(filepath.Join(dir, "shared", "docs"), opts.SharedDocsTTL, opts, &res)
		}
	}
	return res
}

type gcItem struct {
	path string
	mod  time.Time
	size int64
}

// gcArea applies ttl then the byte cap to the direct children of dir. A child
// younger than the grace is neither removed nor counted for removal.
func gcArea(dir string, ttl time.Duration, opts GCOptions, res *GCResult) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := opts.Now()
	var kept []gcItem
	var total int64
	for _, entry := range entries {
		if opts.stopped() {
			return
		}
		item, ok := measureEntry(filepath.Join(dir, entry.Name()), opts.Stop)
		if !ok {
			continue
		}
		age := now.Sub(item.mod)
		if age >= opts.Grace && age > ttl {
			removeItem(item, res)
			continue
		}
		kept = append(kept, item)
		total += item.size
	}
	if total <= opts.MaxBytes {
		return
	}
	sort.Slice(kept, func(i, j int) bool {
		if !kept[i].mod.Equal(kept[j].mod) {
			return kept[i].mod.Before(kept[j].mod)
		}
		return kept[i].path < kept[j].path
	})
	for _, item := range kept {
		if total <= opts.MaxBytes || opts.stopped() {
			return
		}
		if now.Sub(item.mod) < opts.Grace {
			continue
		}
		if removeItem(item, res) {
			total -= item.size
		}
	}
}

func removeItem(item gcItem, res *GCResult) bool {
	// A file another process holds open (Windows sharing violation) stays.
	if err := os.RemoveAll(item.path); err != nil {
		return false
	}
	res.Entries++
	res.Bytes += item.size
	return true
}

// measureEntry returns the size and newest modification time of path: one
// file's own, or a directory tree's totals. Symlinks are measured as links and
// never followed.
//
// It fails closed: ok is false, and the entry is neither aged nor removed, when
// anything under it cannot be read (other than vanishing mid-walk) or when stop
// reports true during the walk. An unreadable subtree would otherwise hide a
// recent file and let a live directory look expired.
func measureEntry(path string, stop func() bool) (gcItem, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return gcItem{}, false
	}
	item := gcItem{path: path, mod: info.ModTime(), size: info.Size()}
	if !info.IsDir() {
		return item, true
	}
	item.size = 0
	complete := true
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if stop != nil && stop() {
			complete = false
			return fs.SkipAll
		}
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			complete = false
			return fs.SkipAll
		}
		fi, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			complete = false
			return fs.SkipAll
		}
		if fi.ModTime().After(item.mod) {
			item.mod = fi.ModTime()
		}
		if !fi.IsDir() {
			item.size += fi.Size()
		}
		return nil
	})
	return item, complete
}

// gcDeadWorktree removes dir when its worktree is provably gone and long
// unseen. Any doubt (missing or unreadable worktree.json, a key mismatch, a
// path whose absence is not certain, a recent mtime, a session naming the
// path) keeps it.
func gcDeadWorktree(dir, key string, opts GCOptions, res *GCResult) bool {
	raw, err := os.ReadFile(filepath.Join(dir, "worktree.json"))
	if err != nil {
		return false
	}
	var meta WorktreeMetadata
	if json.Unmarshal(raw, &meta) != nil || meta.WorktreeKey != key || meta.WorktreePath == "" || !filepath.IsAbs(meta.WorktreePath) {
		return false
	}
	now := opts.Now()
	seen, err := time.Parse(time.RFC3339, meta.LastSeenAt)
	if err != nil || now.Sub(seen) <= opts.DeadWorktreeAge {
		return false
	}
	if _, err := os.Lstat(meta.WorktreePath); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	// Any file touched inside the directory within the age keeps it.
	item, ok := measureEntry(dir, opts.Stop)
	if !ok || now.Sub(item.mod) <= opts.DeadWorktreeAge {
		return false
	}
	if opts.SessionActive != nil && opts.SessionActive(meta.WorktreePath) {
		return false
	}
	if !removeItem(item, res) {
		return false
	}
	res.DeadWorktrees++
	return true
}
