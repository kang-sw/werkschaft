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

// RecordWorktreeRelease writes target's release marker as completed at at,
// replacing any earlier one atomically. worktree.release calls it once its
// hygiene and lease removal have succeeded, under the per-worktree release
// lock, so a spawn registered before the release always predates the marker.
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

// BoundBeforeRelease reports whether a binding created at createdAt (an
// RFC 3339 timestamp, whole seconds as agent records keep it) predates a
// release completed at releasedAt. Only a binding whole seconds earlier
// counts: a binding in the release's own second cannot be ordered against it
// and stays a current binding, which keeps the checkout busy rather than
// handing it out. An unparsable createdAt is current too.
func BoundBeforeRelease(createdAt string, releasedAt time.Time) bool {
	created, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(createdAt))
	if err != nil {
		return false
	}
	return created.Truncate(time.Second).Before(releasedAt.Truncate(time.Second))
}

// CheckoutRoot is the nearest directory at or above path holding a .git
// entry, found without starting Git; "" when there is none. It maps a
// conversation's working directory to the checkout it is bound to.
func CheckoutRoot(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	dir := filepath.Clean(path)
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
