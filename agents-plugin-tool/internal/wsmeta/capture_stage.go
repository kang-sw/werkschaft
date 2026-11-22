package wsmeta

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// The staging pass: Stage and the stagePass capture of every bundled legacy file.

// Stage runs one durable capture pass: it discovers every bundled legacy
// file under the dashboard directory, including CLI-only session
// directories, and stores its current bytes, unless the migration's own
// fence demonstrably landed on that exact file, in which case the staged
// original is kept. Sessions recreated since an earlier pass lose their
// journal rows, and so do deleted ones unless a row carries a fence record:
// those are kept as missing (StageResult.Missing) until the directory
// returns. Stage only reads legacy paths; it never creates,
// replaces or removes one. The caller holds dashboard.lock and has drained
// the legacy writers (2125, 2135).
func (s *Store) Stage(ctx context.Context) (StageResult, error) {
	s.cutover.Lock()
	defer s.cutover.Unlock()
	var res StageResult
	err := s.write(ctx, durable, func(tx *sql.Tx) error {
		state, attempt, err := importState(ctx, tx)
		if err != nil {
			return err
		}
		if state == importComplete {
			return ErrImportComplete
		}
		if err := s.checkCutoverDirs(ctx, tx); err != nil {
			return err
		}
		attempt++
		if res, err = s.stagePass(ctx, tx, attempt); err != nil {
			return err
		}
		return setImportState(ctx, tx, importStaging, attempt)
	})
	return res, err
}

// stagePass captures every bundled file inside tx. It returns the pass
// counts; rows of files that disappeared are deleted.
func (s *Store) stagePass(ctx context.Context, tx *sql.Tx, attempt int) (StageResult, error) {
	res := StageResult{Attempt: attempt}
	now := s.opts.Now().UTC().Format(time.RFC3339Nano)
	existing := map[string]captureMeta{}
	rows, err := tx.QueryContext(ctx, `SELECT path, state, digest, fence_state, fence_digest, fence_witness, fence_kind, fence_required, fence_mtime, witness_digest, witness_mtime FROM legacy_captures`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var p string
		var m captureMeta
		if err := rows.Scan(&p, &m.state, &m.digest, &m.fenceState, &m.fenceDigest, &m.fenceWitness, &m.fenceKind, &m.fenceRequired, &m.fenceMtime, &m.witnessDigest, &m.witnessMtime); err != nil {
			rows.Close()
			return res, err
		}
		existing[p] = m
	}
	if err := rows.Close(); err != nil {
		return res, err
	}
	knownSessions := map[string]string{}
	rows, err = tx.QueryContext(ctx, `SELECT reply_id, session_gen FROM legacy_sessions`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var id, gen string
		if err := rows.Scan(&id, &gen); err != nil {
			rows.Close()
			return res, err
		}
		knownSessions[id] = gen
	}
	if err := rows.Close(); err != nil {
		return res, err
	}

	unread := map[string]bool{}
	rows, err = tx.QueryContext(ctx, `SELECT path FROM legacy_unread`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return res, err
		}
		unread[p] = true
	}
	if err := rows.Close(); err != nil {
		return res, err
	}

	seen := map[string]bool{}
	capture := func(rel, store, replyID, gen string) error {
		seen[rel] = true
		res.Files++
		got := readLegacy(filepath.Join(s.dir, filepath.FromSlash(rel)), s.opts.ReadFile)
		prev, had := existing[rel]
		if got.confirmable && denialConfirmable(had, prev) {
			// Once confirmed, the capture stays unreadable while the same
			// denial persists; only a successful read changes it.
			confirmed := had && prev.state == CaptureUnreadable
			if !confirmed {
				var err error
				if confirmed, err = s.confirmUnread(ctx, tx, rel, got.err); err != nil {
					return err
				}
			}
			if confirmed {
				got = legacyRead{state: CaptureUnreadable, err: got.err}
			}
		}
		if !got.confirmable || !got.transient {
			if unread[rel] {
				delete(unread, rel)
				if _, err := tx.ExecContext(ctx, `DELETE FROM legacy_unread WHERE path = ?`, rel); err != nil {
					return err
				}
			}
		} else {
			delete(unread, rel)
		}
		if got.transient {
			// Keep the row as staged: a fenced original in particular must
			// survive, since its live file is the fence's result. Commit
			// refuses while any input is unread.
			res.Unread = append(res.Unread, rel)
			if had {
				_, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET attempt = ?, session_gen = ? WHERE path = ?`, attempt, gen, rel)
				return err
			}
			return nil
		}
		if had {
			proven, unread := s.fenceProven(prev, got)
			if unread {
				res.Unread = append(res.Unread, rel)
				_, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET attempt = ?, session_gen = ? WHERE path = ?`, attempt, gen, rel)
				return err
			}
			if proven {
				// The record's publication is the live state: keep the
				// original. Until the publication is known durable it stays
				// pending (invariant 5).
				res.Reused++
				if prev.fenceRequired && prev.fenceState != FenceLanded {
					res.Pending = append(res.Pending, rel)
				}
				_, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET attempt = ?, session_gen = ? WHERE path = ?`, attempt, gen, rel)
				return err
			}
			// Nothing the migration published is live: the live state is the
			// original and the record goes (invariant 1). The obligation
			// outlives this and a restart (invariant 6).
			if prev.fenceState != FenceNone {
				res.Unfenced = append(res.Unfenced, rel)
			}
			if prev.fenceRequired {
				res.Pending = append(res.Pending, rel)
			}
		}
		if had && got.state != CaptureUnreadable && prev.state == got.state && prev.digest == got.digest {
			_, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET attempt = ?, session_gen = ?, fence_state = 'none', fence_digest = '', fence_mtime = 0 WHERE path = ?`, attempt, gen, rel)
			return err
		}
		res.Restaged++
		_, err := tx.ExecContext(ctx, `INSERT INTO legacy_captures(path, store, reply_id, session_gen, state, bytes, digest, read_error, attempt, captured_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET store = excluded.store, reply_id = excluded.reply_id, session_gen = excluded.session_gen,
				state = excluded.state, bytes = excluded.bytes, digest = excluded.digest, read_error = excluded.read_error,
				attempt = excluded.attempt, captured_at = excluded.captured_at, fence_state = 'none', fence_digest = '',
				fence_mtime = 0, import_status = '', import_detail = ''`,
			rel, store, replyID, gen, got.state, got.bytes, got.digest, got.err, attempt, now)
		return err
	}

	// Sessions: every reply-id directory, whether the runner or a stdio
	// ws-mcp registered it.
	current, err := s.sessionGenerations()
	if err != nil {
		return res, err
	}
	// A session whose directory vanished keeps its rows while any carries a
	// fence record: the directory may return (moved aside and back) with a
	// fenced file the journal must not take for its original. Its rows are
	// neither re-read nor imported while it is missing.
	missing := map[string]bool{}
	fenced := func(id string) bool {
		for p, m := range existing {
			if strings.HasPrefix(p, sessionsDirName+"/"+id+"/") && (m.fenceRequired || m.fenceState != FenceNone) {
				return true
			}
		}
		return false
	}
	keepMissing := func(id string) error {
		missing[id] = true
		res.Missing = append(res.Missing, id)
		_, err := tx.ExecContext(ctx, `UPDATE legacy_sessions SET missing = 1 WHERE reply_id = ?`, id)
		return err
	}
	discard := func(id string) error {
		res.Discarded++
		delete(knownSessions, id)
		if _, err := tx.ExecContext(ctx, `DELETE FROM legacy_captures WHERE reply_id = ?`, id); err != nil {
			return err
		}
		for p := range existing {
			if strings.HasPrefix(p, sessionsDirName+"/"+id+"/") {
				delete(existing, p)
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM legacy_sessions WHERE reply_id = ?`, id)
		return err
	}
	for id, gen := range knownSessions {
		cur, ok := current[id]
		switch {
		case !ok && fenced(id):
			if err := keepMissing(id); err != nil {
				return res, err
			}
		case !ok || !sameGeneration(cur, gen):
			if err := discard(id); err != nil {
				return res, err
			}
		}
	}
	ids := make([]string, 0, len(current))
	for id := range current {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		gen := current[id]
		// What a session's reads change is undone, journal and counts
		// alike, if its directory goes or is recreated while it is read.
		before := res
		if _, err := tx.ExecContext(ctx, `SAVEPOINT stage_session`); err != nil {
			return res, err
		}
		for _, f := range sessionFiles {
			if err := capture(path.Join(sessionsDirName, id, f.name), f.store, id, gen); err != nil {
				return res, err
			}
		}
		// The directory may have been pruned or recreated while it was
		// read; such a session is not imported and is never recreated.
		again, ok, err := s.sessionGeneration(id)
		if err != nil {
			return res, err
		}
		if !ok || !sameGeneration(again, gen) {
			res = before
			for _, stmt := range []string{`ROLLBACK TO stage_session`, `RELEASE stage_session`} {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return res, err
				}
			}
			for _, f := range sessionFiles {
				delete(seen, path.Join(sessionsDirName, id, f.name))
			}
			if _, known := knownSessions[id]; !ok && known && fenced(id) {
				err = keepMissing(id)
			} else {
				err = discard(id)
			}
			if err != nil {
				return res, err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `RELEASE stage_session`); err != nil {
			return res, err
		}
		res.Sessions++
		if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_sessions(reply_id, session_gen, attempt) VALUES(?, ?, ?)
			ON CONFLICT(reply_id) DO UPDATE SET session_gen = excluded.session_gen, attempt = excluded.attempt, missing = 0`, id, gen, attempt); err != nil {
			return res, err
		}
		if err := s.hit("stage-session"); err != nil {
			return res, err
		}
	}

	for _, f := range dashboardFiles {
		if err := capture(f.name, f.store, "", ""); err != nil {
			return res, err
		}
	}
	for _, d := range dashboardDirs {
		entries, err := wsstate.ReadDir(filepath.Join(s.dir, d.name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return res, err
		}
		for _, e := range entries {
			name := e.Name()
			// Writers publish through name.*.tmp; skip their temp files.
			if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") || strings.HasPrefix(name, ".") {
				continue
			}
			if err := capture(path.Join(d.name, name), d.store, "", ""); err != nil {
				return res, err
			}
		}
	}
	if err := s.hit("stage-files"); err != nil {
		return res, err
	}

	// Failure counts of paths no longer discovered are dropped.
	for p := range unread {
		if _, err := tx.ExecContext(ctx, `DELETE FROM legacy_unread WHERE path = ?`, p); err != nil {
			return res, err
		}
	}
	// Files gone since an earlier pass are no longer inputs; a missing
	// session's rows stay.
	for p := range existing {
		if parts := strings.SplitN(p, "/", 3); len(parts) == 3 && parts[0] == sessionsDirName && missing[parts[1]] {
			continue
		}
		if !seen[p] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM legacy_captures WHERE path = ?`, p); err != nil {
				return res, err
			}
		}
	}
	sort.Strings(res.Missing)
	sort.Strings(res.Unfenced)
	sort.Strings(res.Unread)
	sort.Strings(res.Pending)
	return res, nil
}

// confirmUnread counts one more pass on which rel failed with a confirmable
// error and reports whether the failure is now confirmed permanent. A
// committed Stage keeps the count; a refused Commit rolls its pass back.
func (s *Store) confirmUnread(ctx context.Context, tx *sql.Tx, rel, readErr string) (bool, error) {
	now := s.opts.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_unread(path, first_at, passes, error) VALUES(?, ?, 1, ?)
		ON CONFLICT(path) DO UPDATE SET passes = passes + 1, error = excluded.error`, rel, now.Format(time.RFC3339Nano), readErr); err != nil {
		return false, err
	}
	var firstAt string
	var passes int
	if err := tx.QueryRowContext(ctx, `SELECT first_at, passes FROM legacy_unread WHERE path = ?`, rel).Scan(&firstAt, &passes); err != nil {
		return false, err
	}
	first, err := time.Parse(time.RFC3339Nano, firstAt)
	if err != nil {
		return false, err
	}
	if passes < UnreadConfirmPasses || now.Sub(first) < UnreadConfirmAfter {
		return false, nil
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM legacy_unread WHERE path = ?`, rel)
	return err == nil, err
}
