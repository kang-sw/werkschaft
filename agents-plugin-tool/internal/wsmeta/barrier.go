package wsmeta

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// ErrCutoverUnsupported reports a cutover call refused because a directory
// it would change is on a file system whose directory durability barrier is
// not established (sync_*.go), or whose file system could not be
// determined. The refusal happens before the call changes anything: the
// legacy files stay the authority, and the caller keeps running on them
// (re-review e33c561c, lead decision E(a)). Opening the store and its
// pre-import reads are never refused.
var ErrCutoverUnsupported = errors.New("wsmeta: the legacy cutover is not supported on this file system")

// barrierFileSystem names the file system holding dir, or "" where the
// platform's barrier needs none: a directory fsync on POSIX. On Windows the
// barrier rests on NTFS's ordered metadata log, so only NTFS and ReFS
// volumes are accepted (fs_*.go; a test seam).
var barrierFileSystem = platformFileSystem

// barrierFileSystems are the Windows file systems whose metadata ordering
// the barrier relies on.
var barrierFileSystems = []string{"NTFS", "ReFS"}

// checkBarrierDir refuses dir unless its file system supports the barrier.
// The refusal is logged with its reason. A missing dir passes when
// missingOK: no fence can land in it.
func (s *Store) checkBarrierDir(dir string, missingOK bool) error {
	name, err := barrierFileSystem(dir)
	if missingOK && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		err = fmt.Errorf("%w: the file system of %s is unknown: %w", ErrCutoverUnsupported, dir, err)
	} else if name != "" && !supportedFileSystem(name) {
		err = fmt.Errorf("%w: %s is on %s, not %s", ErrCutoverUnsupported, dir, name, strings.Join(barrierFileSystems, " or "))
	}
	if err != nil {
		s.logf("wsmeta: legacy cutover refused; the legacy store stays authoritative: %v", err)
	}
	return err
}

func supportedFileSystem(name string) bool {
	for _, ok := range barrierFileSystems {
		if strings.EqualFold(name, ok) {
			return true
		}
	}
	return false
}

// checkCutoverDirs checks every directory a fence can change before a
// cutover pass: the dashboard directory, each session directory and each
// keyed-file directory. Stage and Commit run it first, so an unsupported
// volume refuses the whole cutover before its journal or any legacy file
// changes.
func (s *Store) checkCutoverDirs() error {
	if err := s.checkBarrierDir(s.dir, false); err != nil {
		return err
	}
	sessions := filepath.Join(s.dir, sessionsDirName)
	entries, err := wsstate.ReadDir(sessions)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	dirs := make([]string, 0, len(entries)+len(dashboardDirs))
	for _, e := range entries {
		if e.IsDir() && replyIDPattern.MatchString(e.Name()) {
			dirs = append(dirs, filepath.Join(sessions, e.Name()))
		}
	}
	for _, d := range dashboardDirs {
		dirs = append(dirs, filepath.Join(s.dir, d.name))
	}
	for _, d := range dirs {
		// A pruned or never-created directory holds nothing to fence.
		if err := s.checkBarrierDir(d, true); err != nil {
			return err
		}
	}
	return nil
}
