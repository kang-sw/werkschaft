package wswhiteboard

// The board DB is the recovery side of the memory-first whiteboard store. It
// is deliberately not wired to MCP callers here: BoardDB keeps one complete
// State in memory and writes only row deltas to one held SQLite connection.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wssqlite"
)

const (
	boardDBName                = ".board.sqlite"
	boardDBSchemaVersion       = 1
	boardSentinelSchemaVersion = 3

	// BoardDBCheckpointIdle and BoardDBCheckpointWALBytes keep checkpoints
	// away from an update response. A board must be idle for this long and its
	// WAL must exceed this size before the background PASSIVE checkpoint runs.
	BoardDBCheckpointIdle     = 30 * time.Second
	BoardDBCheckpointWALBytes = 4 << 20
	// BoardDBOpenRetryBound covers the brief overlap while a draining daemon
	// still owns the database in locking_mode=EXCLUSIVE.
	BoardDBOpenRetryBound = 2 * time.Second
)

// BoardDBOptions supplies observability and deterministic test seams.
type BoardDBOptions struct {
	Logf          func(format string, args ...any)
	Observe       func(stage string)
	Fault         func(stage string) error
	OnLoadArchive func(dir string)
}

// boardDBInstances numbers every opened board (BoardDB.Instance).
var boardDBInstances atomic.Uint64

// BoardDB owns one board's held SQLite handle and complete in-memory state.
// Call Close when the board ages out or the daemon drains.
type BoardDB struct {
	// instance is immutable after open (Instance).
	instance uint64
	dir      string
	path     string
	db       *sql.DB
	opts     BoardDBOptions

	mu                 sync.Mutex
	state              *State
	generation         int
	importedGeneration int
	revisionSlot       int
	nextTopic          int
	topicDirs          map[string]string
	mockups            map[string]string
	mockupRoots        map[string]string
	checkpointTimer    *time.Timer
	checkpointDue      time.Time
	closed             bool
}

type boardDBTopic struct {
	Doc         *TopicDoc `json:"doc,omitempty"`
	Items       []Item    `json:"items,omitempty"`
	Label       *string   `json:"label,omitempty"`
	Closed      bool      `json:"closed,omitempty"`
	CloseResult *string   `json:"close_result,omitempty"`
}

type boardDBTopicRow struct {
	Dir      string
	Key      string
	Position int
	Data     []byte
	Entries  []boardDBEntryRow
}

type boardDBEntryRow struct {
	Seq       int
	ID        string
	TopicDir  string
	Data      []byte
	PanelBody *string
}

type boardDBRows struct {
	State     []byte
	ItemOrder []byte
	Topics    map[string]boardDBTopicRow
	Dirs      map[string]string
	NextTopic int
}

type boardSentinel struct {
	SchemaVersion int `json:"schema_version"`
}

var boardDBSchema = []string{
	`CREATE TABLE IF NOT EXISTS board_meta (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		schema_version INTEGER NOT NULL,
		generation INTEGER NOT NULL,
		imported_generation INTEGER NOT NULL,
		next_topic INTEGER NOT NULL,
		state_json BLOB NOT NULL,
		item_order_json BLOB NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS topics (
		topic_dir TEXT PRIMARY KEY,
		topic_key TEXT NOT NULL UNIQUE,
		position INTEGER NOT NULL,
		topic_json BLOB NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS entries (
		entry_id TEXT PRIMARY KEY,
		entry_seq INTEGER NOT NULL UNIQUE,
		topic_dir TEXT NOT NULL REFERENCES topics(topic_dir),
		entry_json BLOB NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS entries_topic_seq ON entries(topic_dir, entry_seq)`,
	`CREATE TABLE IF NOT EXISTS panels (
		entry_id TEXT PRIMARY KEY REFERENCES entries(entry_id),
		body TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS revision_receipts (
		slot INTEGER PRIMARY KEY CHECK(slot IN (1, 2)),
		generation INTEGER NOT NULL,
		metadata_revision INTEGER NOT NULL,
		stale_nudge_revision TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS load_floors (
		generation INTEGER PRIMARY KEY,
		loaded_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS mockup_bundles (
		entry_id TEXT PRIMARY KEY REFERENCES entries(entry_id) ON DELETE CASCADE,
		root_file TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		bundle BLOB NOT NULL,
		byte_size INTEGER NOT NULL,
		file_count INTEGER NOT NULL,
		created_at TEXT NOT NULL
	)`,
}

// OpenBoardDB opens or imports one board. The first import holds the legacy
// folder lock through the DB commit and schema-3 sentinel write. legacy is the
// old session snapshot accepted by FolderStore.Open when no folder exists.
func OpenBoardDB(dir string, legacy *State, opts BoardDBOptions) (*BoardDB, error) {
	return OpenBoardDBLazy(dir, func() (*State, error) { return legacy, nil }, opts)
}

// OpenBoardDBLazy consults the legacy snapshot only when no folder or DB authority exists.
func OpenBoardDBLazy(dir string, legacy func() (*State, error), opts BoardDBOptions) (*BoardDB, error) {
	if dir == "" {
		return nil, fmt.Errorf("whiteboard board DB: empty directory")
	}
	path := filepath.Join(dir, boardDBName)
	sentinel, err := boardHasSentinel(dir)
	if err != nil {
		return nil, err
	}
	info, statErr := os.Stat(path)
	dbExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, boardDBRollbackError(path, statErr.Error())
	}
	if sentinel && !dbExists {
		return nil, boardDBRollbackError(path, "is missing")
	}
	if dbExists && !info.Mode().IsRegular() {
		return nil, boardDBRollbackError(path, "is unreadable")
	}
	if dbExists {
		board, err := openBoardDBHandle(dir, opts)
		if err != nil {
			return nil, boardDBOpenError(path, err)
		}
		loadErr := board.load()
		if loadErr == nil {
			if !sentinel {
				board.finishSentinel()
			}
			board.armCheckpointLocked()
			(FolderStore{Dir: dir}).rememberIncarnation(board.state.LoadedAt)
			return board, nil
		}
		// A first import commits the schema before its single data
		// transaction. With no sentinel and every table empty, that data
		// transaction never committed: the legacy folder is still the
		// authority, so the import below retries under the legacy lock.
		incomplete := false
		if !sentinel {
			incomplete, _ = board.uncommitted()
		}
		_ = board.db.Close()
		if !incomplete {
			return nil, boardDBRollbackError(path, loadErr.Error())
		}
		board.logf("whiteboard board DB %s: no committed import; retrying from the legacy folder", path)
	}

	legacyStore := FolderStore{Dir: dir}
	present, err := boardAuthorityPresent(dir)
	if dbExists {
		// Only an uncommitted DB reaches here; it is not board authority.
		present, err = boardLegacyAuthorityPresent(dir)
	}
	if err != nil {
		return nil, err
	}
	var legacyState *State
	if !present {
		if legacy != nil {
			legacyState, err = legacy()
			if err != nil {
				return nil, err
			}
		}
		// An uncommitted DB is still discarded under the lock below.
		if legacyState == nil && !dbExists {
			return nil, os.ErrNotExist
		}
	}
	var board *BoardDB
	retryOpen := false
	err = legacyStore.withGuard(true, func() error { return boardLegacyGuard(dir) }, func() error {
		// Another importer may have committed while this opener waited for
		// the legacy lock. Re-enter the existing-DB path after releasing it.
		has, err := boardHasSentinel(dir)
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			if has {
				retryOpen = true
				return nil
			}
			existing, err := openBoardDBHandle(dir, opts)
			if err != nil {
				return boardDBOpenError(path, err)
			}
			empty, err := existing.uncommitted()
			_ = existing.db.Close()
			if err != nil || !empty {
				retryOpen = true
				return nil
			}
			// Proven uncommitted under the legacy lock: discard it and import
			// again (or report no board when no legacy authority exists).
			boardRemoveDBFiles(dir)
		} else if !errors.Is(err, os.ErrNotExist) {
			return boardDBRollbackError(path, err.Error())
		} else if has {
			return boardDBRollbackError(path, "is missing")
		}
		state, meta, err := legacyStore.load(legacyState)
		if err != nil {
			return err
		}
		if state == nil || meta == nil {
			return os.ErrNotExist
		}
		board, err = openBoardDBHandle(dir, opts)
		if err != nil {
			return err
		}
		if err := board.hit("import-schema-committed"); err != nil {
			_ = board.db.Close()
			return err
		}
		if err := board.importState(state, meta, legacyStore); err != nil {
			_ = board.db.Close()
			return err
		}
		if err := board.hit("import-committed"); err != nil {
			_ = board.db.Close()
			return err
		}
		if err := board.writeSentinel(legacyStore); err != nil {
			board.logf("whiteboard board DB %s: write migration sentinel: %v", dir, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if retryOpen {
		return OpenBoardDBLazy(dir, legacy, opts)
	}
	board.armCheckpointLocked()
	(FolderStore{Dir: dir}).rememberIncarnation(board.state.LoadedAt)
	return board, nil
}

func boardDBOpenError(path string, err error) error {
	if boardSQLiteBusy(err) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("whiteboard board DB %s is busy after %s; retry after the owning daemon releases it: %w", path, BoardDBOpenRetryBound, err)
	}
	return boardDBRollbackError(path, err.Error())
}

// uncommitted reports whether no data transaction ever committed: every
// table openBoardDBHandle creates is empty.
func (b *BoardDB) uncommitted() (bool, error) {
	var n int
	err := b.db.QueryRow(`SELECT (SELECT COUNT(*) FROM board_meta)+(SELECT COUNT(*) FROM topics)+(SELECT COUNT(*) FROM entries)+(SELECT COUNT(*) FROM panels)+(SELECT COUNT(*) FROM revision_receipts)+(SELECT COUNT(*) FROM load_floors)+(SELECT COUNT(*) FROM mockup_bundles)`).Scan(&n)
	return err == nil && n == 0, err
}

func boardRemoveDBFiles(dir string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(filepath.Join(dir, boardDBName+suffix))
	}
}

// boardAuthorityPresent checks only authoritative files; a directory or lock
// alone does not constitute a board.
func boardAuthorityPresent(dir string) (bool, error) {
	return boardFilesPresent(dir, boardDBName, "meta.json", "pending.json", "committed.json")
}

func boardLegacyAuthorityPresent(dir string) (bool, error) {
	return boardFilesPresent(dir, "meta.json", "pending.json", "committed.json")
}

func boardFilesPresent(dir string, names ...string) (bool, error) {
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

// CreateBoardDB installs initial state directly, refusing existing authority.
func CreateBoardDB(dir string, initial *State, opts BoardDBOptions) (*BoardDB, error) {
	return CreateBoardDBWithBundles(dir, initial, nil, opts)
}

// CreateBoardDBWithBundles installs initial state and any accompanying mockup
// bundles in one transaction, refusing existing authority.
func CreateBoardDBWithBundles(dir string, initial *State, bundles []MockupBundle, opts BoardDBOptions) (*BoardDB, error) {
	if dir == "" || initial == nil {
		return nil, fmt.Errorf("whiteboard board DB: directory and initial state are required")
	}
	bundleMap := mockupBundleMap(bundles)
	f := FolderStore{Dir: dir}
	var b *BoardDB
	err := f.withGuard(true, func() error {
		present, err := boardAuthorityPresent(dir)
		if err != nil {
			return err
		}
		if present {
			return fmt.Errorf("whiteboard board already exists: %w", os.ErrExist)
		}
		return nil
	}, func() error {
		rows, err := boardRows(initial, nil, 0, nil)
		if err != nil {
			return err
		}
		cleanup := func() { boardRemoveDBFiles(dir) }
		b, err = openBoardDBHandle(dir, opts)
		if err != nil {
			cleanup()
			return err
		}
		tx, err := b.db.Begin()
		if err != nil {
			_ = b.db.Close()
			cleanup()
			return err
		}
		defer tx.Rollback()
		if err = boardInsertAll(tx, rows, 1, 0, bundleMap); err == nil && (initial.MetadataRevision != 0 || initial.StaleNudgeRevision != "") {
			_, err = tx.Exec(`INSERT INTO revision_receipts(slot,generation,metadata_revision,stale_nudge_revision) VALUES(1,1,?,?)`, initial.MetadataRevision, initial.StaleNudgeRevision)
			b.revisionSlot = 1
		}
		if err == nil && initial.LoadedAt != "" {
			_, err = tx.Exec(`INSERT INTO load_floors(generation,loaded_at) VALUES(1,?)`, initial.LoadedAt)
		}
		if err == nil {
			err = b.hit("transaction-before-commit")
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			_ = tx.Rollback()
			_ = b.db.Close()
			cleanup()
			return err
		}
		b.state, b.generation, b.nextTopic, b.topicDirs = initial.clone(), 1, rows.NextTopic, rows.Dirs
		b.setMockupIndexLocked(rows, bundleMap)
		if err := b.writeSentinel(f); err != nil {
			b.logf("whiteboard board DB %s: write migration sentinel: %v", dir, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	f.rememberIncarnation(b.state.LoadedAt)
	b.armCheckpointLocked()
	return b, nil
}

// Dir returns the board directory path this BoardDB owns.
func (b *BoardDB) Dir() string {
	if b == nil {
		return ""
	}
	return b.dir
}

// MockupIndex returns a copy of the in-memory entry_id -> sha256 index for
// mockup bundles currently present in this board.
func (b *BoardDB) MockupIndex() map[string]string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.mockups) == 0 {
		return nil
	}
	out := make(map[string]string, len(b.mockups))
	for k, v := range b.mockups {
		out[k] = v
	}
	return out
}

// LookupMockupMeta returns the root filename and sha256 digest of a mockup
// bundle from the in-memory index without issuing a SQLite query.
func (b *BoardDB) LookupMockupMeta(entryID string) (rootFile, sha256Hex string, ok bool) {
	if b == nil || entryID == "" {
		return "", "", false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return "", "", false
	}
	sha, exists := b.mockups[entryID]
	if !exists {
		return "", "", false
	}
	return b.mockupRoots[entryID], sha, true
}

// ReadMockupBundle loads one mockup bundle's zip blob on demand from SQLite.
func (b *BoardDB) ReadMockupBundle(entryID string) (MockupBundle, bool, error) {
	if b == nil || entryID == "" {
		return MockupBundle{}, false, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return MockupBundle{}, false, fmt.Errorf("whiteboard board DB is closed")
	}
	if _, exists := b.mockups[entryID]; !exists {
		return MockupBundle{}, false, nil
	}
	if b.opts.Observe != nil {
		b.opts.Observe("mockup-read")
	}
	var out MockupBundle
	out.EntryID = entryID
	err := b.db.QueryRow(
		`SELECT root_file, sha256, bundle, byte_size, file_count, created_at FROM mockup_bundles WHERE entry_id=?`,
		entryID,
	).Scan(&out.RootFile, &out.SHA256, &out.Zip, &out.ByteSize, &out.FileCount, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MockupBundle{}, false, nil
	}
	if err != nil {
		return MockupBundle{}, false, err
	}
	return out, true, nil
}

func (b *BoardDB) setMockupIndexLocked(rows boardDBRows, bundles map[string]MockupBundle) {
	b.mockups = map[string]string{}
	b.mockupRoots = map[string]string{}
	if len(bundles) == 0 {
		return
	}
	for _, topic := range rows.Topics {
		for _, entry := range topic.Entries {
			if bundle, ok := bundles[entry.ID]; ok {
				b.mockups[entry.ID] = bundle.SHA256
				b.mockupRoots[entry.ID] = bundle.RootFile
			}
		}
	}
}

func openBoardDBHandle(dir string, opts BoardDBOptions) (*BoardDB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, boardDBName)
	db, err := sql.Open("sqlite", wssqlite.FileURI(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	b := &BoardDB{
		instance:    boardDBInstances.Add(1),
		dir:         dir,
		path:        path,
		db:          db,
		opts:        opts,
		topicDirs:   map[string]string{},
		mockups:     map[string]string{},
		mockupRoots: map[string]string{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), BoardDBOpenRetryBound)
	defer cancel()
	// locking_mode is deliberately first: WAL never opens a shared-memory
	// coordination path for this daemon-owned database.
	for _, stmt := range []string{
		`PRAGMA locking_mode=EXCLUSIVE`,
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA wal_autocheckpoint=0`,
		`PRAGMA cache_size=-256`,
		`PRAGMA busy_timeout=100`,
		`PRAGMA foreign_keys=ON`,
	} {
		if err := boardSQLiteRetry(ctx, func() error { _, err := db.ExecContext(ctx, stmt); return err }); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure board sqlite %s: %w", stmt, err)
		}
	}
	if err := boardSQLiteRetry(ctx, func() error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, stmt := range boardDBSchema {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return tx.Commit()
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate board sqlite: %w", err)
	}
	return b, nil
}
