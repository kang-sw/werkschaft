package wsmeta

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// ErrCutoverUnsupported reports a cutover call refused because a directory
// it would change is on a file system whose directory durability barrier is
// not established (sync_*.go), whose entry identities cannot tell a moved
// file from a later one at its inode (cutoverRefusal), or whose file system
// or identity could not be determined, while no fence has ever been recorded: no legacy file was
// changed, in this process or an earlier one. The legacy files stay the
// authority, and the caller keeps running on them (lead decision E(a)).
// Opening the store and its pre-import reads are never refused.
var ErrCutoverUnsupported = errors.New("wsmeta: the legacy cutover is not supported on this file system")

// ErrCutoverInterrupted reports the same refusal once a fence may have
// changed the legacy tree: a fence record exists (from this or an earlier
// process), or a publication was already made in this call. Legacy service
// cannot simply resume, since a fenced selector or a moved config may be
// live; the cutover has to be completed on a supported file system or
// resolved by its owner (re-review 80ffd114).
var ErrCutoverInterrupted = errors.New("wsmeta: the legacy cutover was interrupted after a fence may have changed legacy files")

// barrierFileSystem names the file system holding dir, or "" where the
// platform's barrier needs none: a directory fsync on POSIX. On Windows the
// barrier rests on NTFS's ordered metadata log, so only NTFS volumes are
// accepted (fs_*.go; a test seam). ReFS is refused: the ordering argument
// does not cover it, and a documented ReFS setting ignores write-through
// requests altogether (re-review 80ffd114).
var barrierFileSystem = platformFileSystem

// barrierFileSystems are the Windows file systems whose metadata ordering
// the barrier relies on.
var barrierFileSystems = []string{"NTFS"}

// barrierRefusal returns why dir's file system cannot carry the barrier,
// or nil. A missing dir passes when missingOK: no fence can land in it.
func barrierRefusal(dir string, missingOK bool) error {
	name, err := barrierFileSystem(dir)
	if missingOK && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("the file system of %s is unknown: %w", dir, err)
	}
	if name != "" && !supportedFileSystem(name) {
		return fmt.Errorf("%s is on %s, not %s", dir, name, strings.Join(barrierFileSystems, " or "))
	}
	return nil
}

// cutoverRefusal returns why the cutover cannot change entries in dir, or
// nil: its file system cannot carry the barrier (barrierRefusal), or its
// entry identities carry no incarnation evidence, so a removal's witness
// could not be told from a later file at the same inode (sameIncarnation;
// re-review fd1cff5e). A missing dir passes when missingOK.
func cutoverRefusal(dir string, missingOK bool) error {
	if reason := barrierRefusal(dir, missingOK); reason != nil {
		return reason
	}
	id, err := fileIdentity(dir)
	if missingOK && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("the identity of %s is unknown: %w", dir, err)
	}
	if !hasIncarnation(id) {
		return fmt.Errorf("%s: %w", dir, errNoIncarnation)
	}
	return nil
}

// refuseCutover turns a barrier refusal before any publication in this call
// into the caller's result: ErrCutoverUnsupported while the journal holds no
// fence record, ErrCutoverInterrupted once it does. Both are logged with
// their reason.
func (s *Store) refuseCutover(ctx context.Context, q querier, reason error) error {
	started, err := cutoverStarted(ctx, q)
	if err != nil {
		return err
	}
	if started {
		return s.interrupted(reason)
	}
	err = fmt.Errorf("%w: %w", ErrCutoverUnsupported, reason)
	s.logf("wsmeta: legacy cutover refused; the legacy store stays authoritative: %v", err)
	return err
}

// interrupted is the refusal once a fence may have changed the legacy tree.
func (s *Store) interrupted(reason error) error {
	err := fmt.Errorf("%w: %w", ErrCutoverInterrupted, reason)
	s.logf("wsmeta: legacy cutover interrupted; fences may be live in the legacy store: %v", err)
	return err
}

// cutoverStarted reports whether any fence was ever recorded. Every fence
// record sets the meta key cutoverStartedKey in its own transaction, before
// its publication, and nothing clears it: a pass that discards capture rows
// (a vanished session, a key no longer enumerated) cannot erase it
// (re-review fd1cff5e). The capture columns are checked as well.
func cutoverStarted(ctx context.Context, q querier) (bool, error) {
	var started bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM meta WHERE key = '`+cutoverStartedKey+`')
		OR EXISTS(SELECT 1 FROM legacy_captures
			WHERE fence_required = 1 OR fence_state != 'none' OR fence_witness != '' OR witness_id != '')`).Scan(&started)
	return started, err
}

// cutoverStartedKey is the meta key a fence record sets for good.
const cutoverStartedKey = "cutover_started"

// markCutoverStarted records, inside tx, that a fence record exists.
func markCutoverStarted(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES(?, '1') ON CONFLICT(key) DO NOTHING`, cutoverStartedKey)
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

// checkCutoverDirs checks (cutoverRefusal) every directory a fence can
// change before a cutover pass: the dashboard directory, each session directory and each
// keyed-file directory. Stage and Commit run it first, so an unsupported
// volume refuses the whole cutover before its journal or any legacy file
// changes.
func (s *Store) checkCutoverDirs(ctx context.Context, q querier) error {
	if reason := cutoverRefusal(s.dir, false); reason != nil {
		return s.refuseCutover(ctx, q, reason)
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
		if reason := cutoverRefusal(d, true); reason != nil {
			return s.refuseCutover(ctx, q, reason)
		}
	}
	return nil
}
