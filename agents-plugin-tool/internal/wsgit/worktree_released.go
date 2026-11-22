package wsgit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// WorktreeReleasedFileName is the per-checkout release marker inside a
// worktree's Git admin directory, beside the release lock and the lease. Like
// them it is outside reset/clean's reach and Git deletes it with the admin
// directory. Only this marker records the boundary; no other record changes,
// and a binary that predates it neither reads nor writes the file.
const WorktreeReleasedFileName = "ws-worktree-released.json"

const worktreeReleasedSchemaVersion = 1

// worktreeReleased is the marker's on-disk form: when the checkout's most
// recent release completed.
type worktreeReleased struct {
	SchemaVersion int    `json:"schema_version"`
	ReleasedAt    string `json:"released_at"`
}

// worktreeAdminDirNoSpawn resolves target's Git admin directory from its .git
// entry (a primary .git directory or a linked worktree's gitfile) without
// starting Git. It reports "" when target has no such entry.
func worktreeAdminDirNoSpawn(target string) string {
	dotGit := filepath.Join(target, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return dotGit
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return ""
	}
	pointer, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return ""
	}
	dir := filepath.FromSlash(strings.TrimSpace(pointer))
	if dir != "" && !filepath.IsAbs(dir) {
		dir = filepath.Join(target, dir)
	}
	return dir
}

// RecordWorktreeRelease writes target's release marker as completed at at
// (released_at, RFC 3339 at nanosecond precision), replacing any earlier one
// atomically. worktree.release calls it once its hygiene has succeeded and
// before it removes the lease, under the per-worktree release lock, so a
// spawn registered before the release always predates the marker and a
// release whose marker failed keeps its lease.
func RecordWorktreeRelease(target string, at time.Time) error {
	dir := worktreeAdminDirNoSpawn(strings.TrimSpace(target))
	if dir == "" {
		return fmt.Errorf("record worktree release of %q: .git is not a directory or gitfile", target)
	}
	payload, err := json.Marshal(worktreeReleased{SchemaVersion: worktreeReleasedSchemaVersion, ReleasedAt: at.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "ws-worktree-released-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := wsstate.AtomicReplaceFile(tmpName, filepath.Join(dir, WorktreeReleasedFileName)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// WorktreeReleasedAt reads target's release marker. ok is false when target
// is not a Git checkout or has no recorded release; an unreadable or
// malformed marker is an error, which callers treat as no recorded release
// (every binding then counts).
func WorktreeReleasedAt(target string) (at time.Time, ok bool, err error) {
	dir := worktreeAdminDirNoSpawn(strings.TrimSpace(target))
	if dir == "" {
		return time.Time{}, false, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, WorktreeReleasedFileName))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	var rec worktreeReleased
	if err := json.Unmarshal(data, &rec); err != nil {
		return time.Time{}, false, fmt.Errorf("parse worktree release marker: %v", err)
	}
	if rec.SchemaVersion > worktreeReleasedSchemaVersion {
		return time.Time{}, false, fmt.Errorf("worktree release marker schema %d is newer than %d", rec.SchemaVersion, worktreeReleasedSchemaVersion)
	}
	at, err = time.Parse(time.RFC3339Nano, rec.ReleasedAt)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse worktree release marker time: %v", err)
	}
	return at, true, nil
}

// BoundBeforeRelease reports whether a binding predates a release completed
// at releasedAt (the marker's released_at, written at nanosecond precision by
// every marker writer). createdUnixNano is the binding instant at nanosecond
// precision (the agent record's created_unix_nano): a binding strictly before
// the release instant predates it, so one made earlier in the release's own
// second no longer survives the release.
//
// A record written before created_unix_nano existed (createdUnixNano 0) has
// only createdAt, an RFC 3339 timestamp in whole seconds. Such a binding
// keeps the earlier rule: only a binding whole seconds earlier predates the
// release, and one in the release's own second cannot be ordered against it
// and stays current, which keeps the checkout busy rather than handing it
// out. An unparsable createdAt is current too.
func BoundBeforeRelease(createdAt string, createdUnixNano int64, releasedAt time.Time) bool {
	if createdUnixNano > 0 {
		return time.Unix(0, createdUnixNano).Before(releasedAt)
	}
	created, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(createdAt))
	if err != nil {
		return false
	}
	return created.Truncate(time.Second).Before(releasedAt.Truncate(time.Second))
}

// ContainingCheckouts lists every checkout at or above path, nearest first:
// each directory holding a .git entry, found without starting Git. It is the
// durable side of live occupancy's containment rule (a conversation is bound
// to a checkout that contains its working directory), so a working directory
// inside a tracked submodule of a checkout yields the submodule and then the
// checkout that holds it.
func ContainingCheckouts(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	var out []string
	dir := filepath.Clean(path)
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			out = append(out, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return out
		}
		dir = parent
	}
}

// IsLinkedWorktree reports whether checkout is a linked worktree (a gitfile
// whose admin directory has a commondir), the only kind of checkout a pooled
// worktree is; a primary checkout or a submodule is not.
func IsLinkedWorktree(checkout string) bool {
	info, err := os.Lstat(filepath.Join(checkout, ".git"))
	if err != nil || info.IsDir() {
		return false
	}
	dir := worktreeAdminDirNoSpawn(checkout)
	if dir == "" {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "commondir"))
	return err == nil
}
