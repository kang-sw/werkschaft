package wsusercmd

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

// StoreName is the user commands' database file in the daemon's state
// directory. It is a database of its own: no other store (the exec job rows,
// the whiteboard boards) knows it, so an older daemon neither reads nor
// overwrites it.
const StoreName = "user-commands.db"

// storeSchemaVersion is the PRAGMA user_version this code writes and reads. A
// database stamped with a newer version is refused, never rewritten.
const storeSchemaVersion = 2

// ErrStoreNewer marks a database written by a newer schema than this code knows.
var ErrStoreNewer = errors.New("user command store has a newer schema")

// ErrStoreClosed is returned by every Store method after Close.
var ErrStoreClosed = errors.New("user command store is closed")

const storeSchema = `CREATE TABLE IF NOT EXISTS user_commands (
	reply       TEXT NOT NULL,
	root        TEXT NOT NULL,
	id          TEXT NOT NULL,
	label       TEXT NOT NULL,
	description TEXT NOT NULL,
	line        TEXT NOT NULL,
	revision    INTEGER NOT NULL,
	seq         INTEGER NOT NULL,
	PRIMARY KEY (reply, root, id)
);
CREATE TABLE IF NOT EXISTS meta (
	k TEXT PRIMARY KEY,
	v INTEGER NOT NULL
)`

// metaRevision is the meta row holding the highest revision ever issued: it
// is raised in the same transaction as every set and removal and never lowered,
// so a revision of a withdrawn command is never issued again, whatever the
// clock does across a restart.
const metaRevision = "revision"

// row is one stored user command with its scope.
type row struct {
	Reply, Root string
	Command
	Seq int64
}

// Store is the user commands' small SQLite database (storage type 2), held
// open by the daemon for its life (one connection, WAL): each Put or Delete is
// one commit, and Load runs once at start. It is safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	db     *sql.DB
	closed bool
	ops    atomic.Int64
}

// Ops is the number of database operations (a load or a commit) the store ran
// since it opened; tests and measurements read it.
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
	// The version check runs before any pragma that would write a newer file
	// (WAL conversion).
	if err := sqliteRetry(ctx, func() error { return storeMigrate(ctx, db) }); err != nil {
		_ = db.Close()
		return nil, err
	}
	for _, stmt := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA synchronous=NORMAL`, `PRAGMA busy_timeout=5000`} {
		if err := sqliteRetry(ctx, func() error { _, err := db.ExecContext(ctx, stmt); return err }); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure user command store %s: %w", stmt, err)
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
		return fmt.Errorf("read user command store version: %w", err)
	}
	if version > storeSchemaVersion {
		return fmt.Errorf("%w: version %d, this build knows %d", ErrStoreNewer, version, storeSchemaVersion)
	}
	if version == storeSchemaVersion {
		return nil
	}
	if _, err := tx.ExecContext(ctx, storeSchema); err != nil {
		return fmt.Errorf("create user command store schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version=%d`, storeSchemaVersion)); err != nil {
		return fmt.Errorf("stamp user command store version: %w", err)
	}
	return tx.Commit()
}

// highWater is the highest revision ever issued (0 for a new store).
func (s *Store) highWater() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrStoreClosed
	}
	ctx := context.Background()
	s.ops.Add(1)
	var v int64
	err := sqliteRetry(ctx, func() error {
		err := s.db.QueryRowContext(ctx, `SELECT v FROM meta WHERE k = ?`, metaRevision).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			v, err = 0, nil
		}
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("load user command revision: %w", err)
	}
	return v, nil
}

// load returns every stored row, oldest registration first.
func (s *Store) load() ([]row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrStoreClosed
	}
	ctx := context.Background()
	s.ops.Add(1)
	var out []row
	err := sqliteRetry(ctx, func() error {
		out = nil
		rows, err := s.db.QueryContext(ctx, `SELECT reply, root, id, label, description, line, revision, seq FROM user_commands ORDER BY seq, reply, root, id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.Reply, &r.Root, &r.ID, &r.Label, &r.Description, &r.Line, &r.Revision, &r.Seq); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("load user commands: %w", err)
	}
	return out, nil
}

// raise lifts the stored revision high-water to rev inside tx.
func raise(ctx context.Context, tx *sql.Tx, rev int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO meta (k, v) VALUES (?, ?)
		ON CONFLICT(k) DO UPDATE SET v = MAX(v, excluded.v)`, metaRevision, rev)
	return err
}

// put writes r and raises the revision high-water to r.Revision in one
// transaction (one WAL commit).
func (s *Store) put(r row) error {
	return s.write("put user command "+r.ID, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_commands (reply, root, id, label, description, line, revision, seq) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(reply, root, id) DO UPDATE SET label = excluded.label, description = excluded.description,
			line = excluded.line, revision = excluded.revision`,
			r.Reply, r.Root, r.ID, r.Label, r.Description, r.Line, r.Revision, r.Seq); err != nil {
			return err
		}
		return raise(ctx, tx, r.Revision)
	})
}

// del removes one command and raises the revision high-water to rev in one
// transaction (one WAL commit): a withdrawal never lowers it.
func (s *Store) del(reply, root, id string, rev int64) error {
	return s.write("delete user command", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_commands WHERE reply = ? AND root = ? AND id = ?`, reply, root, id); err != nil {
			return err
		}
		return raise(ctx, tx, rev)
	})
}

// delReplies removes every command of the given conversations in one
// transaction.
func (s *Store) delReplies(replies []string) error {
	if len(replies) == 0 {
		return nil
	}
	return s.write("delete user commands of closed conversations", func(ctx context.Context, tx *sql.Tx) error {
		for _, reply := range replies {
			if _, err := tx.ExecContext(ctx, `DELETE FROM user_commands WHERE reply = ?`, reply); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) write(what string, fn func(context.Context, *sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
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

// Close checkpoints the WAL and releases the held database. It is idempotent.
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
