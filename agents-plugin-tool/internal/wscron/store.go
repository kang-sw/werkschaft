package wscron

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// StoreName is the schedule database's file name in the daemon's state
// directory.
const StoreName = "cron-schedules.db"

// storeSchemaVersion is the PRAGMA user_version this code writes and reads. A
// database stamped with a newer version is refused, never rewritten.
const storeSchemaVersion = 1

// ErrStoreNewer marks a schedule database written by a newer schema than this
// code knows.
var ErrStoreNewer = errors.New("cron schedule store has a newer schema")

// ErrStoreClosed is returned by every Store method after Close.
var ErrStoreClosed = errors.New("cron schedule store is closed")

const storeSchema = `CREATE TABLE IF NOT EXISTS schedules (
	id         TEXT PRIMARY KEY,
	owner      TEXT NOT NULL,
	name       TEXT NOT NULL,
	expr       TEXT NOT NULL,
	tz         TEXT NOT NULL,
	prompt     TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	last_fire  INTEGER NOT NULL
)`

// Row is one stored schedule. Times are Unix seconds; LastFire is 0 before
// the first fire.
type Row struct {
	ID, Owner, Name, Expr, TZ, Prompt string
	CreatedAt, LastFire               int64
}

// Store is the schedules' small SQLite database, held open by the daemon for
// its life (one connection, WAL): each Put, Delete or SetLastFire is one
// commit, and Load runs once at start. It is safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	db     *sql.DB
	closed bool
	ops    atomic.Int64
}

// Ops is the number of database operations (a load or a commit) the store
// ran since it opened; tests and measurements read it.
func (s *Store) Ops() int64 { return s.ops.Load() }

// OpenStore opens (creating it and its parent directories) the database at
// path. A database whose schema is newer than this code's is refused with an
// error wrapping ErrStoreNewer and left untouched.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	// The version check runs before any pragma that would write a newer
	// file (WAL conversion).
	if err := sqliteRetry(ctx, func() error { return storeMigrate(ctx, db) }); err != nil {
		_ = db.Close()
		return nil, err
	}
	for _, stmt := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA synchronous=NORMAL`, `PRAGMA busy_timeout=5000`} {
		if err := sqliteRetry(ctx, func() error { _, err := db.ExecContext(ctx, stmt); return err }); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure cron store %s: %w", stmt, err)
		}
	}
	return &Store{db: db}, nil
}

func storeMigrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read cron store version: %w", err)
	}
	if version > storeSchemaVersion {
		return fmt.Errorf("%w: version %d, this build knows %d", ErrStoreNewer, version, storeSchemaVersion)
	}
	if version == storeSchemaVersion {
		return nil
	}
	if _, err := tx.ExecContext(ctx, storeSchema); err != nil {
		return fmt.Errorf("create cron store schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version=%d`, storeSchemaVersion)); err != nil {
		return fmt.Errorf("stamp cron store version: %w", err)
	}
	return tx.Commit()
}

// Load returns every stored row, oldest first.
func (s *Store) Load() ([]Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrStoreClosed
	}
	ctx := context.Background()
	s.ops.Add(1)
	var out []Row
	err := sqliteRetry(ctx, func() error {
		out = nil
		rows, err := s.db.QueryContext(ctx, `SELECT id, owner, name, expr, tz, prompt, created_at, last_fire FROM schedules ORDER BY created_at, id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r Row
			if err := rows.Scan(&r.ID, &r.Owner, &r.Name, &r.Expr, &r.TZ, &r.Prompt, &r.CreatedAt, &r.LastFire); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("load cron schedules: %w", err)
	}
	return out, nil
}

// Put inserts row in one statement (one WAL commit).
func (s *Store) Put(r Row) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	ctx := context.Background()
	s.ops.Add(1)
	err := sqliteRetry(ctx, func() error {
		_, err := s.db.ExecContext(ctx, `INSERT INTO schedules (id, owner, name, expr, tz, prompt, created_at, last_fire) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET owner = excluded.owner, name = excluded.name, expr = excluded.expr, tz = excluded.tz,
			prompt = excluded.prompt, created_at = excluded.created_at, last_fire = excluded.last_fire`,
			r.ID, r.Owner, r.Name, r.Expr, r.TZ, r.Prompt, r.CreatedAt, r.LastFire)
		return err
	})
	if err != nil {
		return fmt.Errorf("put cron schedule %s: %w", r.ID, err)
	}
	return nil
}

// Delete removes the rows with ids in one transaction.
func (s *Store) Delete(ids ...string) error {
	return s.write("delete cron schedules", func(ctx context.Context, tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	}, len(ids))
}

// DeleteOwner removes every row owned by conversation owner.
func (s *Store) DeleteOwner(owner string) error {
	return s.write("delete cron schedules of a conversation", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM schedules WHERE owner = ?`, owner)
		return err
	}, 1)
}

// SetLastFire records the last fire of each id (Unix seconds) in one
// transaction; an id with no row is a no-op.
func (s *Store) SetLastFire(last map[string]int64) error {
	return s.write("update cron last fires", func(ctx context.Context, tx *sql.Tx) error {
		for id, at := range last {
			if _, err := tx.ExecContext(ctx, `UPDATE schedules SET last_fire = ? WHERE id = ?`, at, id); err != nil {
				return err
			}
		}
		return nil
	}, len(last))
}

func (s *Store) write(what string, fn func(context.Context, *sql.Tx) error, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	if n == 0 {
		return nil
	}
	ctx := context.Background()
	s.ops.Add(1)
	err := sqliteRetry(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := fn(ctx, tx); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// Close checkpoints the WAL and releases the held database. It is
// idempotent.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	_, checkpointErr := s.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
	return errors.Join(checkpointErr, s.db.Close())
}

const (
	sqliteAttempts  = 8
	sqliteBaseDelay = 10 * time.Millisecond
	sqliteMaxDelay  = 120 * time.Millisecond
)

// sqliteRetry reruns fn on SQLITE_BUSY/LOCKED with bounded backoff.
func sqliteRetry(ctx context.Context, fn func() error) error {
	var err error
	delay := sqliteBaseDelay
	for attempt := 0; attempt < sqliteAttempts; attempt++ {
		err = fn()
		if !sqliteBusy(err) || attempt == sqliteAttempts-1 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, sqliteMaxDelay)
	}
	return err
}

func sqliteBusy(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		primary := sqliteErr.Code() & 0xff
		return primary == sqlite3.SQLITE_BUSY || primary == sqlite3.SQLITE_LOCKED
	}
	text := strings.ToUpper(err.Error())
	return strings.Contains(text, "SQLITE_BUSY") || strings.Contains(text, "SQLITE_LOCKED") || strings.Contains(text, "DATABASE IS LOCKED")
}
