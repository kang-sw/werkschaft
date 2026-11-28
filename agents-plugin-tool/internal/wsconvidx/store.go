// Package wsconvidx maintains the dashboard daemon's held Type 4 SQLite FTS5
// index over wslive conversation rings (conv-index.sqlite).
//
// The daemon opens one Store under dashboard.lock in locking_mode=EXCLUSIVE
// with WAL mode and idle/close checkpointing (matching wsmeta). Entries are
// keyed by (session_id, entry_id), clipped to MaxEntryBytes (64 KiB) on UTF-8
// boundaries, and kept consistent with wslive's RingHistoryCap retention through
// order-independent eviction tombstones.
package wsconvidx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wssqlite"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	// DBName is the conversation index database file in the dashboard state dir.
	DBName = "conv-index.sqlite"

	// SchemaVersion is the database schema version this binary reads and writes.
	SchemaVersion = 1

	// OpenRetryBound bounds waiting for a draining predecessor daemon to release
	// locking_mode=EXCLUSIVE.
	OpenRetryBound = 2 * time.Second

	// CheckpointIdle is the quiet period before a background WAL checkpoint.
	CheckpointIdle = 30 * time.Second

	// MaxEntryBytes caps each indexed entry's stored display text (64 KiB).
	MaxEntryBytes = 64 << 10

	// evictionPruneThreshold bounds per-session explicit eviction tombstones.
	evictionPruneThreshold = 4096
)

var (
	// ErrNewerSchema reports a database written by a newer schema version.
	ErrNewerSchema = errors.New("wsconvidx: database has a newer schema")
	// ErrClosed reports a call on a closed index store.
	ErrClosed = errors.New("wsconvidx: store is closed")
	// ErrNotFound reports a missing session or entry.
	ErrNotFound = errors.New("wsconvidx: entry not found")
	// ErrAmbiguousPrefix reports an entry_id prefix that matches multiple entries.
	ErrAmbiguousPrefix = errors.New("wsconvidx: ambiguous entry_id prefix")
)

// Options configures Open.
type Options struct {
	Now  func() time.Time
	Logf func(format string, args ...any)
	// CheckpointIdle overrides CheckpointIdle; negative disables the background timer.
	CheckpointIdle time.Duration
}

// Store is the held SQLite FTS5 conversation index for one dashboard state dir.
type Store struct {
	dir  string
	path string
	db   *sql.DB
	opts Options

	mu              sync.Mutex
	closed          bool
	checkpointTimer *time.Timer
}

// Open opens or creates dir's conv-index.sqlite database in EXCLUSIVE WAL mode.
func Open(dir string, opts Options) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("wsconvidx: empty dashboard directory")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.CheckpointIdle == 0 {
		opts.CheckpointIdle = CheckpointIdle
	}
	if err := os.MkdirAll(absDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(absDir, DBName)
	if err := makePrivate(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", wssqlite.FileURI(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	s := &Store{dir: absDir, path: path, db: db, opts: opts}
	ctx, cancel := context.WithTimeout(context.Background(), OpenRetryBound)
	defer cancel()

	for _, stmt := range []string{
		`PRAGMA locking_mode=EXCLUSIVE`,
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA wal_autocheckpoint=0`,
		`PRAGMA busy_timeout=100`,
		`PRAGMA foreign_keys=ON`,
	} {
		if err := retry(ctx, func() error { _, err := db.ExecContext(ctx, stmt); return err }); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("wsconvidx %s: %s: %w", path, stmt, err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("wsconvidx %s: migrate: %w", path, err)
	}
	return s, nil
}

// Path returns the SQLite database file path.
func (s *Store) Path() string { return s.path }

// Dir returns the dashboard state directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) migrate(ctx context.Context) error {
	return retry(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
			return err
		}
		var raw string
		switch err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&raw); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			var version int
			if _, err := fmt.Sscan(raw, &version); err != nil {
				return fmt.Errorf("invalid schema_version %q", raw)
			}
			if version > SchemaVersion {
				return fmt.Errorf("%w (%d, this binary reads %d)", ErrNewerSchema, version, SchemaVersion)
			}
		}
		for _, stmt := range schema {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES('schema_version', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprint(SchemaVersion)); err != nil {
			return err
		}
		return tx.Commit()
	})
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS conv_sessions (
		session_id TEXT PRIMARY KEY,
		workspace TEXT NOT NULL DEFAULT '',
		high_water TEXT NOT NULL DEFAULT '',
		indexed INTEGER NOT NULL DEFAULT 0,
		tombstone_floor TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS conv_entries (
		rowid INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		entry_id TEXT NOT NULL,
		workspace TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,
		ts INTEGER NOT NULL,
		text TEXT NOT NULL,
		full_bytes INTEGER NOT NULL,
		truncated INTEGER NOT NULL DEFAULT 0,
		direction TEXT NOT NULL DEFAULT '',
		peer TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		UNIQUE(session_id, entry_id)
	)`,
	`CREATE INDEX IF NOT EXISTS conv_entries_session_entry ON conv_entries(session_id, entry_id)`,
	`CREATE INDEX IF NOT EXISTS conv_entries_workspace_ts ON conv_entries(workspace, ts)`,
	`CREATE INDEX IF NOT EXISTS conv_entries_ts ON conv_entries(ts)`,
	`CREATE TABLE IF NOT EXISTS conv_evictions (
		session_id TEXT NOT NULL,
		entry_id TEXT NOT NULL,
		PRIMARY KEY(session_id, entry_id)
	)`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS conv_entries_fts USING fts5(
		text,
		title,
		peer,
		content='conv_entries',
		content_rowid='rowid',
		tokenize='unicode61'
	)`,
	`CREATE TRIGGER IF NOT EXISTS conv_entries_ai AFTER INSERT ON conv_entries BEGIN
		INSERT INTO conv_entries_fts(rowid, text, title, peer) VALUES (new.rowid, new.text, new.title, new.peer);
	END`,
	`CREATE TRIGGER IF NOT EXISTS conv_entries_ad AFTER DELETE ON conv_entries BEGIN
		INSERT INTO conv_entries_fts(conv_entries_fts, rowid, text, title, peer) VALUES ('delete', old.rowid, old.text, old.title, old.peer);
	END`,
	`CREATE TRIGGER IF NOT EXISTS conv_entries_au AFTER UPDATE ON conv_entries BEGIN
		INSERT INTO conv_entries_fts(conv_entries_fts, rowid, text, title, peer) VALUES ('delete', old.rowid, old.text, old.title, old.peer);
		INSERT INTO conv_entries_fts(rowid, text, title, peer) VALUES (new.rowid, new.text, new.title, new.peer);
	END`,
}

// Close stops the idle checkpoint timer, folds the WAL, and releases the handle.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.checkpointTimer != nil {
		s.checkpointTimer.Stop()
	}
	cpErr := s.checkpointLocked(context.Background())
	if err := s.db.Close(); err != nil {
		return err
	}
	if cpErr != nil && s.opts.Logf != nil {
		s.opts.Logf("wsconvidx %s: checkpoint on close: %v", s.path, cpErr)
	}
	return nil
}

// Checkpoint folds the WAL into the database immediately.
func (s *Store) Checkpoint(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return s.checkpointLocked(ctx)
}

func (s *Store) checkpointLocked(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func (s *Store) armCheckpointLocked() {
	if s.opts.CheckpointIdle < 0 || s.closed {
		return
	}
	if s.checkpointTimer != nil {
		s.checkpointTimer.Reset(s.opts.CheckpointIdle)
		return
	}
	s.checkpointTimer = time.AfterFunc(s.opts.CheckpointIdle, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return
		}
		if err := s.checkpointLocked(context.Background()); err != nil && s.opts.Logf != nil {
			s.opts.Logf("wsconvidx %s: idle checkpoint: %v", s.path, err)
		}
	})
}

func (s *Store) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	err := retry(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err == nil {
		s.armCheckpointLocked()
	}
	return err
}

func (s *Store) read(ctx context.Context, fn func(db *sql.DB) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return retry(ctx, func() error { return fn(s.db) })
}

// ApplyDelta applies incremental additions and RingHistoryCap evictions for one
// session idempotently and order-independently. An eviction delivered before its
// corresponding add records a tombstone so the later add does not reintroduce a
// stale row.
func (s *Store) ApplyDelta(ctx context.Context, sessionID, workspace string, added []wslive.RingRecord, evicted []string) error {
	if sessionID == "" || (len(added) == 0 && len(evicted) == 0) {
		return nil
	}
	nowMs := s.opts.Now().UnixMilli()
	return s.write(ctx, func(tx *sql.Tx) error {
		st, err := readSessionRowLocked(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if workspace == "" {
			workspace = st.workspace
		}
		if len(evicted) > 0 {
			for _, name := range evicted {
				if name == "" {
					continue
				}
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO conv_evictions(session_id, entry_id) VALUES(?, ?)`, sessionID, name); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM conv_entries WHERE session_id = ? AND entry_id = ?`, sessionID, name); err != nil {
					return err
				}
			}
		}
		highWater := st.highWater
		for _, rec := range added {
			if rec.Name == "" {
				continue
			}
			if rec.Name > highWater {
				highWater = rec.Name
			}
			if st.tombstoneFloor != "" && rec.Name <= st.tombstoneFloor {
				continue
			}
			evictedAlready, err := isEvictedLocked(ctx, tx, sessionID, rec.Name)
			if err != nil {
				return err
			}
			if evictedAlready {
				continue
			}
			if err := upsertEntryLocked(ctx, tx, sessionID, workspace, rec); err != nil {
				return err
			}
		}
		newFloor, err := maybeCompactEvictionsLocked(ctx, tx, sessionID, st.tombstoneFloor)
		if err != nil {
			return err
		}
		return upsertSessionRowLocked(ctx, tx, sessionID, workspace, highWater, st.indexed, newFloor, nowMs)
	})
}

// ReconcileSession reconciles a session's indexed entries against an
// authoritative snapshot of its retained ring records (up to RingHistoryCap)
// and marks the session indexed. Concurrent live additions strictly newer than
// the snapshot's highest entry_id are preserved.
func (s *Store) ReconcileSession(ctx context.Context, sessionID, workspace string, records []wslive.RingRecord) error {
	if sessionID == "" {
		return nil
	}
	nowMs := s.opts.Now().UnixMilli()
	return s.write(ctx, func(tx *sql.Tx) error {
		st, err := readSessionRowLocked(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if workspace == "" {
			workspace = st.workspace
		}
		retained := make(map[string]struct{}, len(records))
		var minName, maxName string
		for _, rec := range records {
			if rec.Name == "" {
				continue
			}
			retained[rec.Name] = struct{}{}
			if minName == "" || rec.Name < minName {
				minName = rec.Name
			}
			if maxName == "" || rec.Name > maxName {
				maxName = rec.Name
			}
			if st.tombstoneFloor != "" && rec.Name <= st.tombstoneFloor {
				continue
			}
			evictedAlready, err := isEvictedLocked(ctx, tx, sessionID, rec.Name)
			if err != nil {
				return err
			}
			if evictedAlready {
				continue
			}
			if err := upsertEntryLocked(ctx, tx, sessionID, workspace, rec); err != nil {
				return err
			}
		}
		// Drop any indexed rows within the snapshot's covered window that are no
		// longer retained by the ring, keeping entries newer than maxName intact.
		if maxName != "" {
			rows, err := tx.QueryContext(ctx, `SELECT entry_id FROM conv_entries WHERE session_id = ? AND entry_id <= ?`, sessionID, maxName)
			if err != nil {
				return err
			}
			var stale []string
			for rows.Next() {
				var entryID string
				if err := rows.Scan(&entryID); err != nil {
					_ = rows.Close()
					return err
				}
				if _, keep := retained[entryID]; !keep {
					stale = append(stale, entryID)
				}
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			_ = rows.Close()
			for _, entryID := range stale {
				if _, err := tx.ExecContext(ctx, `DELETE FROM conv_entries WHERE session_id = ? AND entry_id = ?`, sessionID, entryID); err != nil {
					return err
				}
			}
		}
		if len(records) >= wslive.RingHistoryCap && minName != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM conv_entries WHERE session_id = ? AND entry_id < ?`, sessionID, minName); err != nil {
				return err
			}
		}
		highWater := st.highWater
		if maxName > highWater {
			highWater = maxName
		}
		// If the workspace was populated later, refresh existing entries' workspace tag.
		if workspace != "" && workspace != st.workspace {
			if _, err := tx.ExecContext(ctx, `UPDATE conv_entries SET workspace = ? WHERE session_id = ? AND workspace = ''`, workspace, sessionID); err != nil {
				return err
			}
		}
		return upsertSessionRowLocked(ctx, tx, sessionID, workspace, highWater, true, st.tombstoneFloor, nowMs)
	})
}

// DeleteSession removes all indexed entries, evictions, and session metadata
// for sessionID (called when PruneRegistrations retires a closed session).
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM conv_entries WHERE session_id = ?`, sessionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM conv_evictions WHERE session_id = ?`, sessionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM conv_sessions WHERE session_id = ?`, sessionID); err != nil {
			return err
		}
		return nil
	})
}

// SessionIndexed reports whether sessionID has completed initial indexing, along
// with its recorded high-water entry_id.
func (s *Store) SessionIndexed(ctx context.Context, sessionID string) (indexed bool, highWater string, err error) {
	if sessionID == "" {
		return false, "", nil
	}
	err = s.read(ctx, func(db *sql.DB) error {
		var flag int
		qerr := db.QueryRowContext(ctx, `SELECT indexed, high_water FROM conv_sessions WHERE session_id = ?`, sessionID).Scan(&flag, &highWater)
		if errors.Is(qerr, sql.ErrNoRows) {
			indexed = false
			highWater = ""
			return nil
		}
		if qerr != nil {
			return qerr
		}
		indexed = flag != 0
		return nil
	})
	return indexed, highWater, err
}

// SessionStat holds aggregate index metrics for one session.
type SessionStat struct {
	SessionID  string
	Workspace  string
	HighWater  string
	Indexed    bool
	EntryCount int
	LastTS     int64
}

// SessionStats returns aggregate entry count, last timestamp, and index status
// for the requested sessions (or all tracked sessions when sessionIDs is nil).
func (s *Store) SessionStats(ctx context.Context, sessionIDs []string) (map[string]SessionStat, error) {
	out := map[string]SessionStat{}
	err := s.read(ctx, func(db *sql.DB) error {
		// s.read may retry the closure; drop rows from a failed attempt.
		clear(out)
		var rows *sql.Rows
		var qerr error
		if len(sessionIDs) == 0 {
			rows, qerr = db.QueryContext(ctx, `
				SELECT s.session_id, s.workspace, s.high_water, s.indexed,
				       COUNT(e.rowid), COALESCE(MAX(e.ts), 0)
				FROM conv_sessions s
				LEFT JOIN conv_entries e ON e.session_id = s.session_id
				GROUP BY s.session_id`)
		} else {
			placeholders := strings.Repeat("?,", len(sessionIDs))
			placeholders = placeholders[:len(placeholders)-1]
			args := make([]any, len(sessionIDs))
			for i, id := range sessionIDs {
				args[i] = id
			}
			rows, qerr = db.QueryContext(ctx, fmt.Sprintf(`
				SELECT s.session_id, s.workspace, s.high_water, s.indexed,
				       COUNT(e.rowid), COALESCE(MAX(e.ts), 0)
				FROM conv_sessions s
				LEFT JOIN conv_entries e ON e.session_id = s.session_id
				WHERE s.session_id IN (%s)
				GROUP BY s.session_id`, placeholders), args...)
		}
		if qerr != nil {
			return qerr
		}
		defer rows.Close()
		for rows.Next() {
			var st SessionStat
			var idx int
			if err := rows.Scan(&st.SessionID, &st.Workspace, &st.HighWater, &idx, &st.EntryCount, &st.LastTS); err != nil {
				return err
			}
			st.Indexed = idx != 0
			out[st.SessionID] = st
		}
		return rows.Err()
	})
	return out, err
}

type sessionRow struct {
	workspace      string
	highWater      string
	indexed        bool
	tombstoneFloor string
}

func readSessionRowLocked(ctx context.Context, tx *sql.Tx, sessionID string) (sessionRow, error) {
	var row sessionRow
	var idx int
	err := tx.QueryRowContext(ctx, `SELECT workspace, high_water, indexed, tombstone_floor FROM conv_sessions WHERE session_id = ?`, sessionID).
		Scan(&row.workspace, &row.highWater, &idx, &row.tombstoneFloor)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionRow{}, nil
	}
	if err != nil {
		return sessionRow{}, err
	}
	row.indexed = idx != 0
	return row, nil
}

func upsertSessionRowLocked(ctx context.Context, tx *sql.Tx, sessionID, workspace, highWater string, indexed bool, floor string, updatedAt int64) error {
	idxInt := 0
	if indexed {
		idxInt = 1
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO conv_sessions(session_id, workspace, high_water, indexed, tombstone_floor, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			workspace = CASE WHEN excluded.workspace != '' THEN excluded.workspace ELSE conv_sessions.workspace END,
			high_water = CASE WHEN excluded.high_water > conv_sessions.high_water THEN excluded.high_water ELSE conv_sessions.high_water END,
			indexed = MAX(conv_sessions.indexed, excluded.indexed),
			tombstone_floor = CASE WHEN excluded.tombstone_floor > conv_sessions.tombstone_floor THEN excluded.tombstone_floor ELSE conv_sessions.tombstone_floor END,
			updated_at = excluded.updated_at`,
		sessionID, workspace, highWater, idxInt, floor, updatedAt)
	return err
}

func isEvictedLocked(ctx context.Context, tx *sql.Tx, sessionID, entryID string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM conv_evictions WHERE session_id = ? AND entry_id = ?`, sessionID, entryID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func maybeCompactEvictionsLocked(ctx context.Context, tx *sql.Tx, sessionID, currentFloor string) (string, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM conv_evictions WHERE session_id = ?`, sessionID).Scan(&count); err != nil {
		return currentFloor, err
	}
	if count <= evictionPruneThreshold {
		return currentFloor, nil
	}
	var minEntry sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT MIN(entry_id) FROM conv_entries WHERE session_id = ?`, sessionID).Scan(&minEntry); err != nil {
		return currentFloor, err
	}
	limit := count / 2
	var candidate sql.NullString
	if minEntry.Valid && minEntry.String != "" {
		err := tx.QueryRowContext(ctx, `
			SELECT MAX(entry_id) FROM (
				SELECT entry_id FROM conv_evictions
				WHERE session_id = ? AND entry_id < ?
				ORDER BY entry_id ASC LIMIT ?
			)`, sessionID, minEntry.String, limit).Scan(&candidate)
		if err != nil {
			return currentFloor, err
		}
	} else {
		err := tx.QueryRowContext(ctx, `
			SELECT MAX(entry_id) FROM (
				SELECT entry_id FROM conv_evictions
				WHERE session_id = ?
				ORDER BY entry_id ASC LIMIT ?
			)`, sessionID, limit).Scan(&candidate)
		if err != nil {
			return currentFloor, err
		}
	}
	if !candidate.Valid || candidate.String == "" || candidate.String <= currentFloor {
		return currentFloor, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM conv_evictions WHERE session_id = ? AND entry_id <= ?`, sessionID, candidate.String); err != nil {
		return currentFloor, err
	}
	return candidate.String, nil
}

func upsertEntryLocked(ctx context.Context, tx *sql.Tx, sessionID, workspace string, rec wslive.RingRecord) error {
	rawText := DisplayText(rec.RingEntry)
	fullBytes := len(rawText)
	clipped, capHit := clipUTF8(rawText, MaxEntryBytes)
	truncInt := 0
	if rec.Truncated || capHit {
		truncInt = 1
	}
	ts := int64(0)
	if !rec.At.IsZero() {
		ts = rec.At.UnixMilli()
	}
	direction, peer, title := "", "", ""
	if rec.Kind == wslive.RingMail {
		direction = rec.Direction
		peer = rec.Peer
		title = rec.Title
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO conv_entries(
			session_id, entry_id, workspace, kind, ts, text, full_bytes, truncated, direction, peer, title
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, entry_id) DO UPDATE SET
			workspace = CASE WHEN excluded.workspace != '' THEN excluded.workspace ELSE conv_entries.workspace END,
			kind = excluded.kind,
			ts = excluded.ts,
			text = excluded.text,
			full_bytes = excluded.full_bytes,
			truncated = excluded.truncated,
			direction = excluded.direction,
			peer = excluded.peer,
			title = excluded.title`,
		sessionID, rec.Name, workspace, rec.Kind, ts, clipped, fullBytes, truncInt, direction, peer, title)
	return err
}

// DisplayText extracts the searchable, readable display text of a wslive ring
// entry as the ring holds it.
func DisplayText(e wslive.RingEntry) string {
	switch e.Kind {
	case wslive.RingUser, wslive.RingReply, wslive.RingWhiteboardEffects:
		return e.Text
	case wslive.RingMail:
		if e.Text != "" {
			return e.Text
		}
		var b strings.Builder
		b.WriteString("[mail")
		if e.Direction != "" {
			b.WriteByte(' ')
			b.WriteString(e.Direction)
		}
		if e.Peer != "" {
			b.WriteByte(' ')
			b.WriteString(e.Peer)
		}
		b.WriteByte(']')
		if e.Title != "" {
			b.WriteByte(' ')
			b.WriteString(e.Title)
		}
		return b.String()
	case wslive.RingWake:
		switch {
		case e.Caption != "" && e.Text != "":
			return e.Caption + "\n" + e.Text
		case e.Text != "":
			return e.Text
		case e.Caption != "":
			return e.Caption
		case e.Cause != "" && e.Source != "":
			return e.Cause + " (" + e.Source + ")"
		case e.Cause != "":
			return e.Cause
		default:
			return e.Source
		}
	case wslive.RingCommand:
		switch {
		case e.Command != "" && e.Text != "":
			return e.Command + "\n" + e.Text
		case e.Command != "":
			return e.Command
		default:
			return e.Text
		}
	case wslive.RingTools:
		if e.Tools == nil {
			return e.Text
		}
		var parts []string
		header := fmt.Sprintf("tools: %d calls", e.Tools.Calls)
		if e.Tools.Failed > 0 {
			header += fmt.Sprintf(" (%d failed)", e.Tools.Failed)
		}
		parts = append(parts, header)
		if len(e.Tools.Tools) > 0 {
			tallies := make([]string, 0, len(e.Tools.Tools))
			for _, t := range e.Tools.Tools {
				tallies = append(tallies, fmt.Sprintf("%s×%d", t.Name, t.Calls))
			}
			parts = append(parts, strings.Join(tallies, ", "))
		}
		for _, batch := range e.Tools.Batches {
			for _, ca := range batch.CallArgs {
				if ca.ArgsPreview != "" {
					parts = append(parts, ca.Name+": "+ca.ArgsPreview)
				}
			}
		}
		return strings.Join(parts, "\n")
	case wslive.RingTurn:
		if e.Text != "" {
			return e.Text
		}
		var parts []string
		parts = append(parts, fmt.Sprintf("turn: done=%d failed=%d duration_ms=%d", e.Done, e.Failed, e.DurationMs))
		if e.Reason != "" {
			parts = append(parts, "reason="+e.Reason)
		}
		return strings.Join(parts, " ")
	case wslive.RingCompact:
		if e.Text != "" {
			return e.Text
		}
		label := "compaction"
		if e.Trigger != "" {
			label += " (" + e.Trigger + ")"
		}
		if e.CompactFailed {
			label += " failed"
		}
		return label
	case wslive.RingCard:
		if e.Card == nil {
			return e.Text
		}
		s := e.Card.Store + "/" + e.Card.Alias
		if e.Card.Title != "" {
			s += " " + e.Card.Title
		}
		return s
	case wslive.RingUI:
		if e.UI == nil {
			return e.Text
		}
		s := "ui " + e.UI.Op + " " + e.UI.Arg
		if e.UI.Comment != "" {
			s += " #" + e.UI.Comment
		}
		return s
	default:
		if e.Notice != nil {
			if e.Notice.Text != "" {
				return e.Notice.Kind + ": " + e.Notice.Text
			}
			if e.Notice.Kind != "" {
				return e.Notice.Kind
			}
		}
		return e.Text
	}
}

// clipUTF8 clips s to at most maxBytes bytes without splitting a UTF-8 rune.
func clipUTF8(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		return "", len(s) > 0
	}
	if len(s) <= maxBytes {
		return s, false
	}
	n := maxBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

const (
	retryAttempts  = 8
	retryBaseDelay = 10 * time.Millisecond
	retryMaxDelay  = 250 * time.Millisecond
)

func retry(ctx context.Context, fn func() error) error {
	delay := retryBaseDelay
	var err error
	for attempt := 0; attempt < retryAttempts; attempt++ {
		if err = fn(); !isBusy(err) {
			return err
		}
		if attempt == retryAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (last: %v)", ctx.Err(), err)
		case <-time.After(delay):
		}
		if delay *= 2; delay > retryMaxDelay {
			delay = retryMaxDelay
		}
	}
	return err
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		primary := sqliteErr.Code() & 0xff
		return primary == sqlite3.SQLITE_BUSY || primary == sqlite3.SQLITE_LOCKED
	}
	return false
}
