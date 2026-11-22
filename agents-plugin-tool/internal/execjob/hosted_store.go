package execjob

import (
	"context"
	"database/sql"
	"encoding/json"
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

// hostedStoreSchemaVersion is the PRAGMA user_version this code writes and
// reads. A database stamped with a newer version is refused, never
// rewritten: its writer may keep columns or meanings this code would drop.
const hostedStoreSchemaVersion = 1

// ErrHostedStoreNewer marks a hosted exec database written by a newer
// schema than this code knows; the caller falls back to the legacy exec
// path rather than touching it.
var ErrHostedStoreNewer = errors.New("hosted exec store has a newer schema")

const hostedStoreSchema = `CREATE TABLE IF NOT EXISTS exec_jobs (
	id               TEXT PRIMARY KEY,
	legacy           INTEGER NOT NULL,
	owner            TEXT NOT NULL,
	gen              INTEGER NOT NULL,
	tool             TEXT NOT NULL,
	root             TEXT NOT NULL,
	cwd              TEXT NOT NULL,
	argv             TEXT NOT NULL,
	command          TEXT NOT NULL,
	shell            TEXT NOT NULL,
	holder_id        TEXT NOT NULL,
	pid              INTEGER NOT NULL,
	state            TEXT NOT NULL,
	exit_code        INTEGER,
	signal           INTEGER,
	error            TEXT NOT NULL,
	started_at       INTEGER NOT NULL,
	ended_at         INTEGER NOT NULL,
	notify           INTEGER NOT NULL,
	notice           TEXT NOT NULL,
	stdin_bytes      INTEGER NOT NULL,
	written_combined INTEGER NOT NULL,
	written_stdout   INTEGER NOT NULL,
	written_stderr   INTEGER NOT NULL,
	tail_combined    BLOB,
	tail_stdout      BLOB,
	tail_stderr      BLOB
)`

const hostedStoreColumns = `id, legacy, owner, gen, tool, root, cwd, argv, command, shell,
	holder_id, pid, state, exit_code, signal, error, started_at, ended_at,
	notify, notice, stdin_bytes, written_combined, written_stdout,
	written_stderr, tail_combined, tail_stdout, tail_stderr`

const hostedStorePut = `INSERT INTO exec_jobs (` + hostedStoreColumns + `)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	legacy = excluded.legacy, owner = excluded.owner, gen = excluded.gen,
	tool = excluded.tool, root = excluded.root, cwd = excluded.cwd,
	argv = excluded.argv, command = excluded.command, shell = excluded.shell,
	holder_id = excluded.holder_id, pid = excluded.pid, state = excluded.state,
	exit_code = excluded.exit_code, signal = excluded.signal,
	error = excluded.error, started_at = excluded.started_at,
	ended_at = excluded.ended_at, notify = excluded.notify,
	notice = excluded.notice, stdin_bytes = excluded.stdin_bytes,
	written_combined = excluded.written_combined,
	written_stdout = excluded.written_stdout,
	written_stderr = excluded.written_stderr,
	tail_combined = excluded.tail_combined, tail_stdout = excluded.tail_stdout,
	tail_stderr = excluded.tail_stderr`

// SQLiteHostedStore is the HostedStore over one SQLite database the daemon
// holds open for its life (performance.md storage type 2, Rule 4): each Put
// or Delete is one WAL commit. It is safe for concurrent use.
type SQLiteHostedStore struct {
	mu     sync.Mutex
	db     *sql.DB
	closed bool
}

var _ HostedStore = (*SQLiteHostedStore)(nil)

// OpenHostedStore opens (creating it and its parent directories) the hosted
// exec database at path. A database whose schema is newer than this code's
// is refused with an error wrapping ErrHostedStoreNewer.
func OpenHostedStore(path string) (*SQLiteHostedStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA busy_timeout=5000`,
	} {
		if err := hostedSQLiteRetry(ctx, func() error { _, err := db.ExecContext(ctx, stmt); return err }); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure hosted exec store %s: %w", stmt, err)
		}
	}
	if err := hostedSQLiteRetry(ctx, func() error { return hostedStoreMigrate(ctx, db) }); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteHostedStore{db: db}, nil
}

func hostedStoreMigrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read hosted exec store version: %w", err)
	}
	if version > hostedStoreSchemaVersion {
		return fmt.Errorf("%w: version %d, this build knows %d", ErrHostedStoreNewer, version, hostedStoreSchemaVersion)
	}
	if version == hostedStoreSchemaVersion {
		return nil
	}
	if _, err := tx.ExecContext(ctx, hostedStoreSchema); err != nil {
		return fmt.Errorf("create hosted exec store schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version=%d`, hostedStoreSchemaVersion)); err != nil {
		return fmt.Errorf("stamp hosted exec store version: %w", err)
	}
	return tx.Commit()
}

// Load returns every stored row, oldest start first.
func (s *SQLiteHostedStore) Load() ([]HostedJobRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrHostedStoreClosed
	}
	ctx := context.Background()
	var out []HostedJobRow
	err := hostedSQLiteRetry(ctx, func() error {
		out = nil
		rows, err := s.db.QueryContext(ctx, `SELECT `+hostedStoreColumns+` FROM exec_jobs ORDER BY started_at, id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			row, err := scanHostedRow(rows)
			if err != nil {
				return err
			}
			out = append(out, row)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("load hosted exec jobs: %w", err)
	}
	return out, nil
}

func scanHostedRow(rows *sql.Rows) (HostedJobRow, error) {
	var (
		r                   HostedJobRow
		gen                 int64
		argv                string
		exitCode, signal    sql.NullInt64
		legacy, notify      bool
		tailC, tailO, tailE []byte
	)
	if err := rows.Scan(&r.ID, &legacy, &r.Owner, &gen, &r.Tool, &r.Root, &r.Cwd,
		&argv, &r.Command, &r.Shell, &r.HolderID, &r.PID, &r.State, &exitCode,
		&signal, &r.Error, &r.StartedAt, &r.EndedAt, &notify, &r.Notice,
		&r.StdinBytes, &r.Written[StreamCombined], &r.Written[StreamStdout],
		&r.Written[StreamStderr], &tailC, &tailO, &tailE); err != nil {
		return HostedJobRow{}, err
	}
	if err := json.Unmarshal([]byte(argv), &r.Argv); err != nil {
		return HostedJobRow{}, fmt.Errorf("hosted exec job %s argv: %w", r.ID, err)
	}
	r.Legacy, r.Notify = legacy, notify
	// Gen is stored as the int64 with the same bits: SQLite integers are
	// signed, and the bit cast round-trips every uint64.
	r.Gen = uint64(gen)
	if exitCode.Valid {
		v := int(exitCode.Int64)
		r.ExitCode = &v
	}
	if signal.Valid {
		v := int(signal.Int64)
		r.Signal = &v
	}
	r.Tails[StreamCombined] = hostedTail(tailC)
	r.Tails[StreamStdout] = hostedTail(tailO)
	r.Tails[StreamStderr] = hostedTail(tailE)
	return r, nil
}

func hostedTail(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

func hostedNullInt(p *int) any {
	if p == nil {
		return nil
	}
	return int64(*p)
}

func hostedBlob(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// Put inserts or replaces row in one statement (one WAL commit).
func (s *SQLiteHostedStore) Put(row HostedJobRow) error {
	argv, err := json.Marshal(row.Argv)
	if err != nil {
		return fmt.Errorf("hosted exec job %s argv: %w", row.ID, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrHostedStoreClosed
	}
	ctx := context.Background()
	err = hostedSQLiteRetry(ctx, func() error {
		_, err := s.db.ExecContext(ctx, hostedStorePut,
			row.ID, row.Legacy, row.Owner, int64(row.Gen), row.Tool, row.Root,
			row.Cwd, string(argv), row.Command, row.Shell, row.HolderID, row.PID,
			row.State, hostedNullInt(row.ExitCode), hostedNullInt(row.Signal),
			row.Error, row.StartedAt, row.EndedAt, row.Notify, row.Notice,
			row.StdinBytes, row.Written[StreamCombined], row.Written[StreamStdout],
			row.Written[StreamStderr], hostedBlob(row.Tails[StreamCombined]),
			hostedBlob(row.Tails[StreamStdout]), hostedBlob(row.Tails[StreamStderr]))
		return err
	})
	if err != nil {
		return fmt.Errorf("put hosted exec job %s: %w", row.ID, err)
	}
	return nil
}

// Delete removes the rows with ids in one transaction; unknown ids and an
// empty list are no-ops.
func (s *SQLiteHostedStore) Delete(ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrHostedStoreClosed
	}
	if len(ids) == 0 {
		return nil
	}
	ctx := context.Background()
	err := hostedSQLiteRetry(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM exec_jobs WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return fmt.Errorf("delete hosted exec jobs: %w", err)
	}
	return nil
}

// Close checkpoints the WAL and releases the held database. It is
// idempotent; every other method returns ErrHostedStoreClosed after it.
func (s *SQLiteHostedStore) Close() error {
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
	hostedSQLiteAttempts  = 8
	hostedSQLiteBaseDelay = 10 * time.Millisecond
	hostedSQLiteMaxDelay  = 120 * time.Millisecond
)

// hostedSQLiteRetry reruns fn on SQLITE_BUSY/LOCKED with bounded backoff,
// the same policy as wsstore's withSQLiteRetry (unexported there).
func hostedSQLiteRetry(ctx context.Context, fn func() error) error {
	var err error
	delay := hostedSQLiteBaseDelay
	for attempt := 0; attempt < hostedSQLiteAttempts; attempt++ {
		err = fn()
		if !hostedSQLiteBusy(err) || attempt == hostedSQLiteAttempts-1 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, hostedSQLiteMaxDelay)
	}
	return err
}

func hostedSQLiteBusy(err error) bool {
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
