// Package wsmeta is the dashboard daemon's shared metadata store: one SQLite
// database per dashboard state directory that takes over the bundled
// daemon-only metadata files (ticket 261121-refactor-ws-daemon-metadata-sqlite).
//
// This package is deliberately not wired to any caller yet (Phase 1). It owns
//
//   - the held SQLite handle and its schema (store.go),
//   - the durable legacy capture journal and its fence bookkeeping (capture.go),
//   - the one-time transactional import with its completion marker (import.go),
//   - the startup snapshot read (snapshot.go), and
//   - the conversation record delta and holder cursor writes (records.go).
//
// The package reads and writes JSON at the member level and never imports the
// packages that own the legacy formats (wsagent, wsdashboard, wstrackerhook),
// so those packages can depend on it when Phase 2 switches authority. Every
// stored document keeps its unknown members as raw JSON.
//
// Only one daemon may hold the database: it opens in locking_mode=EXCLUSIVE,
// so a successor waits (bounded) until its predecessor closes the handle.
package wsmeta

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

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	// DBName is the store's database file in the dashboard state directory.
	DBName = "metadata.sqlite"

	// SchemaVersion is the store schema this binary reads and writes. A
	// database carrying a newer version is refused before any write.
	SchemaVersion = 1

	// OpenRetryBound covers the brief overlap while a draining predecessor
	// still holds the database in locking_mode=EXCLUSIVE.
	OpenRetryBound = 2 * time.Second

	// CheckpointIdle is how long the store must see no write before its
	// background checkpoint folds the WAL into the database, away from any
	// interactive action. wal_autocheckpoint is off.
	CheckpointIdle = 30 * time.Second
)

var (
	// ErrNewerSchema reports a database written by a newer store schema.
	ErrNewerSchema = errors.New("wsmeta: database has a newer schema")
	// ErrClosed reports a call on a closed store.
	ErrClosed = errors.New("wsmeta: store is closed")
)

// Options supplies the clock, logging and deterministic test seams.
type Options struct {
	Now  func() time.Time
	Logf func(format string, args ...any)
	// Fault, when set, is consulted at named stages; a non-nil error aborts
	// the operation there, as a crash at that point would (tests only).
	Fault func(stage string) error
	// Observe, when set, receives every named stage reached.
	Observe func(stage string)
	// ReadFile, when set, replaces the legacy file read (tests only: it
	// injects a transient read failure such as a Windows sharing violation).
	ReadFile func(path string) ([]byte, error)
	// CheckpointIdle overrides CheckpointIdle; a negative value disables the
	// background checkpoint (Close still checkpoints).
	CheckpointIdle time.Duration
}

// Store is the held metadata database of one dashboard state directory.
type Store struct {
	dir  string
	path string
	db   *sql.DB
	opts Options

	// cutover serializes the legacy cutover calls (Stage, ApplyFence,
	// ApplyRemovalFence, Commit) end to end. A fence's journal record and its
	// file-system publication are one transition, so no other call may plan,
	// stage or commit between them. Taken before mu.
	cutover sync.Mutex
	// mu serializes every write transaction and the synchronous level the
	// single connection runs it at.
	mu              sync.Mutex
	closed          bool
	imported        bool // the import marker is known to have committed
	checkpointTimer *time.Timer
}

// Open opens or creates dir's metadata database, configures the held
// connection and applies the schema. It never reads or writes a legacy
// metadata file.
func Open(dir string, opts Options) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("wsmeta: empty dashboard directory")
	}
	// One absolute root for the owner-only hardening, the database URI (a
	// file: URI path is absolute) and legacy capture.
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ReadFile == nil {
		// Regular files only; retries a Windows sharing violation
		// (antivirus, indexer) before failing.
		opts.ReadFile = readRegularFile
	}
	if opts.CheckpointIdle == 0 {
		opts.CheckpointIdle = CheckpointIdle
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, DBName)
	// Owner-only before SQLite opens it, on every open (private_*.go).
	if err := makePrivate(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteURI(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	s := &Store{dir: dir, path: path, db: db, opts: opts}
	ctx, cancel := context.WithTimeout(context.Background(), OpenRetryBound)
	defer cancel()
	// locking_mode is first: WAL then keeps its index in heap memory and
	// never creates the -shm file, and no other process can open the
	// database while this daemon holds it.
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
			return nil, s.openError(stmt, err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, s.openError("migrate", err)
	}
	return s, nil
}

// sqliteURI is path as an SQLite file: URI. The driver splits a plain DSN at
// its first '?', so a cache root containing one would open another file;
// percent-encoding every byte outside the unreserved set keeps '?', '#' and
// '%' literal.
func sqliteURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/x becomes /C:/x, which SQLite's Windows VFS accepts
	} else if strings.HasPrefix(p, "//") {
		p = "//" + p // a UNC path keeps an empty URI authority
	}
	var b strings.Builder
	b.WriteString("file:")
	for i := 0; i < len(p); i++ {
		c := p[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-._~/:", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func (s *Store) openError(stage string, err error) error {
	if errors.Is(err, ErrNewerSchema) {
		return fmt.Errorf("wsmeta %s: %w", s.path, err)
	}
	if isBusy(err) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("wsmeta %s is busy after %s (another daemon holds it); retry after it exits: %w", s.path, OpenRetryBound, err)
	}
	return fmt.Errorf("wsmeta %s: %s: %w", s.path, stage, err)
}

// Path is the database file.
func (s *Store) Path() string { return s.path }

// Dir is the dashboard state directory the store belongs to.
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

// schema is the complete store layout. Tables are keyed by the identity each
// decision names; *_json and body columns hold raw JSON with every member the
// legacy document carried, known or not.
var schema = []string{
	// The capture journal: the durable original bytes of every bundled
	// legacy file, keyed by its slash path relative to the dashboard
	// directory. Rows of a session carry the session generation they were
	// read under. fence_* is the migration-owned fence bookkeeping: a fence
	// records its exact result digest before it replaces the file
	// ('intent'), so a retry recognises a landed fence even when the crash
	// came before 'landed'. witness_* names the file a removal fence last
	// moved to its witness path (identity and content), so the migration
	// only ever deletes its own leftover there. import_* is set by the
	// import commit.
	`CREATE TABLE IF NOT EXISTS legacy_captures (
		path TEXT PRIMARY KEY,
		store TEXT NOT NULL,
		reply_id TEXT NOT NULL DEFAULT '',
		session_gen TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL CHECK(state IN ('present', 'absent', 'unreadable')),
		bytes BLOB,
		digest TEXT NOT NULL,
		read_error TEXT NOT NULL DEFAULT '',
		attempt INTEGER NOT NULL,
		captured_at TEXT NOT NULL,
		fence_state TEXT NOT NULL DEFAULT 'none' CHECK(fence_state IN ('none', 'intent', 'landed')),
		fence_digest TEXT NOT NULL DEFAULT '',
		fence_required INTEGER NOT NULL DEFAULT 0,
		fence_kind TEXT NOT NULL DEFAULT '' CHECK(fence_kind IN ('', 'replace', 'remove')),
		fence_mtime INTEGER NOT NULL DEFAULT 0,
		fence_witness TEXT NOT NULL DEFAULT '',
		witness_id TEXT NOT NULL DEFAULT '',
		witness_content TEXT NOT NULL DEFAULT '',
		import_status TEXT NOT NULL DEFAULT '',
		import_detail TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS legacy_captures_session ON legacy_captures(reply_id)`,
	// Consecutive staging passes on which a bundled path failed with a
	// confirmable read error (capture.go confirmUnread).
	`CREATE TABLE IF NOT EXISTS legacy_unread (
		path TEXT PRIMARY KEY,
		first_at TEXT NOT NULL,
		passes INTEGER NOT NULL,
		error TEXT NOT NULL DEFAULT ''
	)`,
	// One row per discovered session directory and the generation it was
	// captured under; a deleted or recreated directory drops its captures.
	`CREATE TABLE IF NOT EXISTS legacy_sessions (
		reply_id TEXT PRIMARY KEY,
		session_gen TEXT NOT NULL,
		attempt INTEGER NOT NULL
	)`,

	// (1991) Conversation state: a reply-id keyed snapshot (the whole legacy
	// record object, unknown members included) plus bounded top-level member
	// deltas, folded into the snapshot at RecordDeltaBound.
	`CREATE TABLE IF NOT EXISTS agent_records (
		reply_id TEXT PRIMARY KEY,
		session_gen TEXT NOT NULL DEFAULT '',
		snapshot_json BLOB NOT NULL,
		next_delta INTEGER NOT NULL DEFAULT 1
	)`,
	`CREATE TABLE IF NOT EXISTS agent_record_deltas (
		reply_id TEXT NOT NULL REFERENCES agent_records(reply_id) ON DELETE CASCADE,
		seq INTEGER NOT NULL,
		patch_json BLOB NOT NULL,
		PRIMARY KEY(reply_id, seq)
	)`,
	// (1992) Applied holder sequence keyed by conversation and holder
	// identity: a cursor never applies to another holder's ring.
	`CREATE TABLE IF NOT EXISTS agent_holder_cursors (
		reply_id TEXT NOT NULL REFERENCES agent_records(reply_id) ON DELETE CASCADE,
		holder_id TEXT NOT NULL,
		holder_seq INTEGER NOT NULL,
		PRIMARY KEY(reply_id, holder_id)
	)`,

	// (1993) Explicit permission selections. 'invalid' keeps an existing
	// unusable selection (fail closed) apart from absence (no row).
	`CREATE TABLE IF NOT EXISTS permissions (
		reply_id TEXT PRIMARY KEY,
		state TEXT NOT NULL CHECK(state IN ('valid', 'invalid')),
		level TEXT NOT NULL DEFAULT '',
		delegated_set INTEGER NOT NULL DEFAULT 0,
		delegated INTEGER NOT NULL DEFAULT 0,
		body_json BLOB,
		error TEXT NOT NULL DEFAULT ''
	)`,

	// (1994) Support: the last selection apart from the keyed mappings.
	`CREATE TABLE IF NOT EXISTS support_state (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		last_json BLOB,
		extra_json BLOB
	)`,
	`CREATE TABLE IF NOT EXISTS support_mappings (
		store TEXT PRIMARY KEY,
		conversation TEXT NOT NULL
	)`,

	// (1995) Pi probe observations keyed by fingerprint and kind.
	`CREATE TABLE IF NOT EXISTS pi_probes (
		fingerprint TEXT NOT NULL,
		kind TEXT NOT NULL,
		observed_at TEXT NOT NULL,
		body_json BLOB NOT NULL,
		PRIMARY KEY(fingerprint, kind)
	)`,

	// (1996) Rail layout: document-level order and extras, plus one row per
	// workspace (its complete layout object).
	`CREATE TABLE IF NOT EXISTS rail_layout (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		workspace_order_json BLOB NOT NULL,
		extra_json BLOB
	)`,
	`CREATE TABLE IF NOT EXISTS rail_layout_workspaces (
		workspace TEXT PRIMARY KEY,
		body_json BLOB NOT NULL
	)`,

	// (1997) Latest usage projections keyed by source file and entry.
	`CREATE TABLE IF NOT EXISTS usage_latest_docs (
		source TEXT PRIMARY KEY,
		extra_json BLOB
	)`,
	`CREATE TABLE IF NOT EXISTS usage_latest (
		source TEXT NOT NULL REFERENCES usage_latest_docs(source) ON DELETE CASCADE,
		position INTEGER NOT NULL,
		entry_key TEXT NOT NULL,
		body_json BLOB NOT NULL,
		PRIMARY KEY(source, position)
	)`,

	// (1998) Usage samples keyed per history; body keeps unknown members.
	`CREATE TABLE IF NOT EXISTS usage_history_docs (
		history TEXT PRIMARY KEY,
		extra_json BLOB
	)`,
	`CREATE TABLE IF NOT EXISTS usage_samples (
		history TEXT NOT NULL REFERENCES usage_history_docs(history) ON DELETE CASCADE,
		seq INTEGER NOT NULL,
		harness TEXT NOT NULL,
		grp TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,
		source TEXT NOT NULL,
		resets_at INTEGER NOT NULL,
		as_of INTEGER NOT NULL,
		percent REAL NOT NULL,
		body_json BLOB NOT NULL,
		PRIMARY KEY(history, seq)
	)`,
	`CREATE INDEX IF NOT EXISTS usage_samples_series ON usage_samples(history, harness, grp, kind, as_of)`,

	// (1999) Memos keyed by workspace hash; items keep stable order.
	`CREATE TABLE IF NOT EXISTS memo_docs (
		workspace_hash TEXT PRIMARY KEY,
		workspace TEXT NOT NULL DEFAULT '',
		schema_version INTEGER NOT NULL,
		extra_json BLOB
	)`,
	`CREATE TABLE IF NOT EXISTS memos (
		workspace_hash TEXT NOT NULL REFERENCES memo_docs(workspace_hash) ON DELETE CASCADE,
		position INTEGER NOT NULL,
		memo_id TEXT NOT NULL DEFAULT '',
		body_json BLOB NOT NULL,
		PRIMARY KEY(workspace_hash, position)
	)`,

	// (2000) Labels: a cleared label has no row.
	`CREATE TABLE IF NOT EXISTS labels (
		reply_id TEXT PRIMARY KEY,
		label TEXT NOT NULL,
		body_json BLOB NOT NULL
	)`,
	// (2001) Answer-check markers.
	`CREATE TABLE IF NOT EXISTS ack_checks (
		reply_id TEXT PRIMARY KEY,
		body_json BLOB NOT NULL
	)`,
	// (2002) Hiring metadata (the imported original, before any fence).
	`CREATE TABLE IF NOT EXISTS hiring (
		reply_id TEXT PRIMARY KEY,
		enabled INTEGER NOT NULL,
		hired INTEGER NOT NULL,
		request_id TEXT NOT NULL DEFAULT '',
		body_json BLOB NOT NULL
	)`,

	// (2004) Tracker hook progress scoped by registration nonce.
	`CREATE TABLE IF NOT EXISTS tracker_hook_state (
		store_id TEXT NOT NULL,
		nonce TEXT NOT NULL,
		cursor_json BLOB,
		extra_json BLOB,
		PRIMARY KEY(store_id, nonce)
	)`,
	`CREATE TABLE IF NOT EXISTS tracker_hook_processed (
		store_id TEXT NOT NULL,
		nonce TEXT NOT NULL,
		event_id TEXT NOT NULL,
		body_json BLOB NOT NULL,
		PRIMARY KEY(store_id, nonce, event_id),
		FOREIGN KEY(store_id, nonce) REFERENCES tracker_hook_state(store_id, nonce) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS tracker_hook_due (
		store_id TEXT NOT NULL,
		nonce TEXT NOT NULL,
		item_id TEXT NOT NULL,
		at_json BLOB NOT NULL,
		PRIMARY KEY(store_id, nonce, item_id),
		FOREIGN KEY(store_id, nonce) REFERENCES tracker_hook_state(store_id, nonce) ON DELETE CASCADE
	)`,
}

// keyedTables are the imported-state tables, children before parents, which
// a (re)import clears inside its transaction.
var keyedTables = []string{
	"agent_record_deltas", "agent_holder_cursors", "agent_records",
	"permissions", "support_mappings", "support_state", "pi_probes",
	"rail_layout_workspaces", "rail_layout",
	"usage_latest", "usage_latest_docs", "usage_samples", "usage_history_docs",
	"memos", "memo_docs", "labels", "ack_checks", "hiring",
	"tracker_hook_processed", "tracker_hook_due", "tracker_hook_state",
}

// durability is a write class (2006). relaxed commits at synchronous=NORMAL:
// a process crash keeps every committed transaction and a power loss can
// roll back to an earlier, still consistent commit. durable runs one commit
// at synchronous=FULL, for writes another file's change depends on (a
// capture before its fence replaces the original, the import commit).
type durability int

const (
	relaxed durability = iota
	durable
)

// write runs fn in one write transaction at class d, with bounded busy
// retry. The background checkpoint is re-armed after a commit.
func (s *Store) write(ctx context.Context, d durability, fn func(tx *sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if d == durable {
		if _, err := s.db.ExecContext(ctx, `PRAGMA synchronous=FULL`); err != nil {
			return err
		}
		defer func() { _, _ = s.db.ExecContext(context.Background(), `PRAGMA synchronous=NORMAL`) }()
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
		if err := s.hit("before-commit"); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err == nil {
		s.armCheckpointLocked()
	}
	return err
}

// read runs fn against the held connection under the store lock.
func (s *Store) read(ctx context.Context, fn func(q querier) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return retry(ctx, func() error { return fn(s.db) })
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Store) hit(stage string) error {
	if s.opts.Observe != nil {
		s.opts.Observe(stage)
	}
	if s.opts.Fault != nil {
		return s.opts.Fault(stage)
	}
	return nil
}

func (s *Store) logf(format string, args ...any) {
	if s.opts.Logf != nil {
		s.opts.Logf(format, args...)
	}
}

// armCheckpointLocked schedules the idle checkpoint; s.mu is held.
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
		if err := s.checkpointLocked(context.Background()); err != nil {
			s.logf("wsmeta %s: idle checkpoint: %v", s.path, err)
		}
	})
}

func (s *Store) checkpointLocked(ctx context.Context) error {
	s.hit("checkpoint")
	_, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// Checkpoint folds the WAL into the database now.
func (s *Store) Checkpoint(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return s.checkpointLocked(ctx)
}

// Close drains the store: it stops the idle checkpoint, waits for the
// running transaction, checkpoints the WAL and closes the held handle, which
// releases the exclusive lock. The caller closes the store before it
// releases dashboard.lock (2122).
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
	if cpErr != nil {
		s.logf("wsmeta %s: checkpoint on close: %v", s.path, cpErr)
	}
	return nil
}

const (
	retryAttempts  = 8
	retryBaseDelay = 10 * time.Millisecond
	retryMaxDelay  = 250 * time.Millisecond
)

// retry repeats fn on SQLITE_BUSY/SQLITE_LOCKED with bounded backoff, the
// wsstore precedent (wsstore/retry.go).
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
