package wsdashboard

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
	"unicode"

	"github.com/kang-sw/devenv/internal/wssqlite"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	companionEpisodesDBName         = "companion-episodes.sqlite"
	companionEpisodesSchemaVersion  = 1
	companionEpisodesOpenRetryBound = 2 * time.Second
	companionEpisodesCheckpointIdle = 30 * time.Second
	companionEpisodesDefaultLimit   = 50
	companionEpisodesMaxLimit       = 200
)

var (
	errCompanionEpisodesClosed      = errors.New("companion episodes store is closed")
	errCompanionEpisodesNewerSchema = errors.New("companion episodes database has a newer schema")
)

// CompanionEpisodesOptions configures CompanionEpisodesStore lifecycle and test seams.
type CompanionEpisodesOptions struct {
	Now            func() time.Time
	Logf           func(format string, args ...any)
	CheckpointIdle time.Duration // negative disables idle timer; 0 uses default 30s
}

// CompanionEpisode is one machine-local episodic memory record scoped by project.
type CompanionEpisode struct {
	ID         int64    `json:"id"`
	ProjectKey string   `json:"projectKey"`
	CreatedAt  string   `json:"createdAt"`
	Summary    string   `json:"summary"`
	Tags       []string `json:"tags"`
	Source     string   `json:"source"`
}

// CompanionEpisodeInput creates a new local episode.
type CompanionEpisodeInput struct {
	ProjectKey string   `json:"projectKey"`
	Summary    string   `json:"summary"`
	Tags       []string `json:"tags"`
	Source     string   `json:"source"`
	CreatedAt  string   `json:"createdAt"`
}

// CompanionEpisodeQuery filters and searches local episodes via FTS5.
type CompanionEpisodeQuery struct {
	ProjectKey string
	Query      string
	Limit      int
}

// CompanionEpisodePruneOptions selects which episodes to delete.
type CompanionEpisodePruneOptions struct {
	ID            int64  `json:"id,omitempty"`
	ProjectKey    string `json:"project,omitempty"`
	OlderThanDays int    `json:"olderThanDays,omitempty"`
	All           bool   `json:"all,omitempty"`
}

// SoulPendingTarget identifies which Soul artifact a held write targeted.
type SoulPendingTarget string

const (
	SoulPendingTargetShard   SoulPendingTarget = "shard"
	SoulPendingTargetPersona SoulPendingTarget = "persona.md"
	SoulPendingTargetProfile SoulPendingTarget = "profile.md"
)

// SoulPendingItem is a scrubber-held write awaiting user approval in Settings.
type SoulPendingItem struct {
	ID        int64             `json:"id"`
	SoulID    string            `json:"soulId"`
	Target    SoulPendingTarget `json:"target"`
	Category  string            `json:"category,omitempty"`
	Content   string            `json:"content"`
	Findings  []ScrubFinding    `json:"findings"`
	Source    string            `json:"source"`
	CreatedAt string            `json:"createdAt"`
}

// SoulPendingInput inserts a scrubber-held item into soul_pending.
type SoulPendingInput struct {
	SoulID   string
	Target   SoulPendingTarget
	Category string
	Content  string
	Findings []ScrubFinding
	Source   string
}

// CompanionEpisodesStore owns the single daemon-held SQLite connection for
// machine-local episodes (FTS5) and the soul_pending approval queue.
// It is never exported in Soul ZIPs and never Git-synced.
type CompanionEpisodesStore struct {
	dir  string
	path string
	db   *sql.DB
	opts CompanionEpisodesOptions

	mu              sync.Mutex
	closed          bool
	checkpointTimer *time.Timer
}

var companionEpisodesSchema = []string{
	`CREATE TABLE IF NOT EXISTS episodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_key TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		summary TEXT NOT NULL,
		tags TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT 'companion'
	)`,
	`CREATE INDEX IF NOT EXISTS idx_episodes_project_created ON episodes(project_key, created_at DESC, id DESC)`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS episodes_fts USING fts5(
		project_key,
		summary,
		tags,
		content='episodes',
		content_rowid='id',
		tokenize='unicode61'
	)`,
	`CREATE TRIGGER IF NOT EXISTS episodes_ai AFTER INSERT ON episodes BEGIN
		INSERT INTO episodes_fts(rowid, project_key, summary, tags)
		VALUES (new.id, new.project_key, new.summary, new.tags);
	END`,
	`CREATE TRIGGER IF NOT EXISTS episodes_ad AFTER DELETE ON episodes BEGIN
		INSERT INTO episodes_fts(episodes_fts, rowid, project_key, summary, tags)
		VALUES ('delete', old.id, old.project_key, old.summary, old.tags);
	END`,
	`CREATE TRIGGER IF NOT EXISTS episodes_au AFTER UPDATE ON episodes BEGIN
		INSERT INTO episodes_fts(episodes_fts, rowid, project_key, summary, tags)
		VALUES ('delete', old.id, old.project_key, old.summary, old.tags);
		INSERT INTO episodes_fts(rowid, project_key, summary, tags)
		VALUES (new.id, new.project_key, new.summary, new.tags);
	END`,
	`CREATE TABLE IF NOT EXISTS soul_pending (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		soul_id TEXT NOT NULL,
		target TEXT NOT NULL CHECK(target IN ('shard', 'persona.md', 'profile.md')),
		category TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL,
		reasons_json TEXT NOT NULL DEFAULT '[]',
		source TEXT NOT NULL DEFAULT 'companion',
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_soul_pending_soul ON soul_pending(soul_id, created_at DESC, id DESC)`,
}

// OpenCompanionEpisodes opens or creates <dir>/companion-episodes.sqlite as a
// single held SQLite connection in WAL + EXCLUSIVE mode.
func OpenCompanionEpisodes(dir string, opts CompanionEpisodesOptions) (*CompanionEpisodesStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("companion episodes: empty state directory")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.CheckpointIdle == 0 {
		opts.CheckpointIdle = companionEpisodesCheckpointIdle
	}
	if err := os.MkdirAll(absDir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(absDir, companionEpisodesDBName)
	// Ensure owner-only permissions on newly created DB files before SQLite opens it.
	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()

	db, err := sql.Open("sqlite", wssqlite.FileURI(dbPath))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	s := &CompanionEpisodesStore{
		dir:  absDir,
		path: dbPath,
		db:   db,
		opts: opts,
	}
	ctx, cancel := context.WithTimeout(context.Background(), companionEpisodesOpenRetryBound)
	defer cancel()

	for _, stmt := range []string{
		`PRAGMA locking_mode=EXCLUSIVE`,
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA wal_autocheckpoint=0`,
		`PRAGMA busy_timeout=100`,
		`PRAGMA foreign_keys=ON`,
	} {
		if err := companionSQLiteRetry(ctx, func() error { _, e := db.ExecContext(ctx, stmt); return e }); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("companion episodes %s: %s: %w", dbPath, stmt, err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("companion episodes %s: migrate: %w", dbPath, err)
	}
	return s, nil
}

// Path returns the absolute SQLite database path.
func (s *CompanionEpisodesStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *CompanionEpisodesStore) migrate(ctx context.Context) error {
	return companionSQLiteRetry(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS episodes_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
			return err
		}
		var raw string
		switch err := tx.QueryRowContext(ctx, `SELECT value FROM episodes_meta WHERE key = 'schema_version'`).Scan(&raw); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			var version int
			if _, err := fmt.Sscan(raw, &version); err != nil {
				return fmt.Errorf("invalid schema_version %q", raw)
			}
			if version > companionEpisodesSchemaVersion {
				return fmt.Errorf("%w (%d, binary supports %d)", errCompanionEpisodesNewerSchema, version, companionEpisodesSchemaVersion)
			}
		}
		for _, stmt := range companionEpisodesSchema {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO episodes_meta(key, value) VALUES('schema_version', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprint(companionEpisodesSchemaVersion)); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (s *CompanionEpisodesStore) armCheckpointLocked() {
	if s.closed || s.opts.CheckpointIdle < 0 {
		return
	}
	delay := s.opts.CheckpointIdle
	if s.checkpointTimer == nil {
		s.checkpointTimer = time.AfterFunc(delay, s.runIdleCheckpoint)
		return
	}
	s.checkpointTimer.Reset(delay)
}

func (s *CompanionEpisodesStore) runIdleCheckpoint() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return
	}
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`); err != nil && s.opts.Logf != nil {
		s.opts.Logf("companion episodes: idle checkpoint: %v", err)
	}
}

// Close checkpoints the WAL and releases the exclusive SQLite handle.
func (s *CompanionEpisodesStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	if s.checkpointTimer != nil {
		s.checkpointTimer.Stop()
		s.checkpointTimer = nil
	}
	db := s.db
	s.mu.Unlock()

	if db == nil {
		return nil
	}
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return db.Close()
}

// AddEpisode inserts a machine-local episode row (indexed automatically in FTS5).
func (s *CompanionEpisodesStore) AddEpisode(ctx context.Context, in CompanionEpisodeInput) (CompanionEpisode, error) {
	summary := strings.TrimSpace(in.Summary)
	if summary == "" {
		return CompanionEpisode{}, errors.New("episode summary is required")
	}
	projectKey := strings.TrimSpace(in.ProjectKey)
	tags := normalizeCompanionTags(in.Tags)
	tagsStr := strings.Join(tags, " ")
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = "companion"
	}
	createdAt := strings.TrimSpace(in.CreatedAt)
	if createdAt == "" {
		createdAt = s.opts.Now().UTC().Format(time.RFC3339)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return CompanionEpisode{}, errCompanionEpisodesClosed
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO episodes(project_key, created_at, summary, tags, source) VALUES (?, ?, ?, ?, ?)`,
		projectKey, createdAt, summary, tagsStr, source)
	if err != nil {
		return CompanionEpisode{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return CompanionEpisode{}, err
	}
	s.armCheckpointLocked()
	return CompanionEpisode{
		ID:         id,
		ProjectKey: projectKey,
		CreatedAt:  createdAt,
		Summary:    summary,
		Tags:       tags,
		Source:     source,
	}, nil
}

// SearchEpisodes performs project-scoped FTS5 search or reverse-chronological listing.
func (s *CompanionEpisodesStore) SearchEpisodes(ctx context.Context, q CompanionEpisodeQuery) ([]CompanionEpisode, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = companionEpisodesDefaultLimit
	}
	if limit > companionEpisodesMaxLimit {
		limit = companionEpisodesMaxLimit
	}
	projectKey := strings.TrimSpace(q.ProjectKey)
	ftsQuery := buildCompanionFTS5Query(q.Query)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errCompanionEpisodesClosed
	}

	var (
		rows *sql.Rows
		err  error
	)
	if ftsQuery != "" {
		if projectKey != "" {
			rows, err = s.db.QueryContext(ctx, `
				SELECT e.id, e.project_key, e.created_at, e.summary, e.tags, e.source
				FROM episodes_fts
				JOIN episodes e ON e.id = episodes_fts.rowid
				WHERE episodes_fts MATCH ? AND e.project_key = ?
				ORDER BY bm25(episodes_fts), e.created_at DESC, e.id DESC
				LIMIT ?`, ftsQuery, projectKey, limit)
		} else {
			rows, err = s.db.QueryContext(ctx, `
				SELECT e.id, e.project_key, e.created_at, e.summary, e.tags, e.source
				FROM episodes_fts
				JOIN episodes e ON e.id = episodes_fts.rowid
				WHERE episodes_fts MATCH ?
				ORDER BY bm25(episodes_fts), e.created_at DESC, e.id DESC
				LIMIT ?`, ftsQuery, limit)
		}
	} else {
		if projectKey != "" {
			rows, err = s.db.QueryContext(ctx, `
				SELECT id, project_key, created_at, summary, tags, source
				FROM episodes
				WHERE project_key = ?
				ORDER BY created_at DESC, id DESC
				LIMIT ?`, projectKey, limit)
		} else {
			rows, err = s.db.QueryContext(ctx, `
				SELECT id, project_key, created_at, summary, tags, source
				FROM episodes
				ORDER BY created_at DESC, id DESC
				LIMIT ?`, limit)
		}
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]CompanionEpisode, 0)
	for rows.Next() {
		var ep CompanionEpisode
		var tagsRaw string
		if err := rows.Scan(&ep.ID, &ep.ProjectKey, &ep.CreatedAt, &ep.Summary, &tagsRaw, &ep.Source); err != nil {
			return nil, err
		}
		ep.Tags = splitCompanionTags(tagsRaw)
		out = append(out, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListProjects returns distinct non-empty project keys present in the episodes table.
func (s *CompanionEpisodesStore) ListProjects(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errCompanionEpisodesClosed
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT project_key FROM episodes WHERE project_key != '' ORDER BY project_key ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var pk string
		if err := rows.Scan(&pk); err != nil {
			return nil, err
		}
		out = append(out, pk)
	}
	return out, rows.Err()
}

// CountEpisodes returns the total count of local episodes.
func (s *CompanionEpisodesStore) CountEpisodes(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errCompanionEpisodesClosed
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM episodes`).Scan(&count)
	return count, err
}

// PruneEpisodes deletes matching episodes by ID, project, age, or all.
func (s *CompanionEpisodesStore) PruneEpisodes(ctx context.Context, opts CompanionEpisodePruneOptions) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errCompanionEpisodesClosed
	}
	var (
		res sql.Result
		err error
	)
	switch {
	case opts.ID > 0:
		res, err = s.db.ExecContext(ctx, `DELETE FROM episodes WHERE id = ?`, opts.ID)
	case opts.All && strings.TrimSpace(opts.ProjectKey) == "" && opts.OlderThanDays <= 0:
		res, err = s.db.ExecContext(ctx, `DELETE FROM episodes`)
	default:
		var clauses []string
		var args []any
		if pk := strings.TrimSpace(opts.ProjectKey); pk != "" {
			clauses = append(clauses, `project_key = ?`)
			args = append(args, pk)
		}
		if opts.OlderThanDays > 0 {
			cutoff := s.opts.Now().UTC().AddDate(0, 0, -opts.OlderThanDays).Format(time.RFC3339)
			clauses = append(clauses, `created_at < ?`)
			args = append(args, cutoff)
		}
		if len(clauses) == 0 {
			return 0, errors.New("prune requires id, project, olderThanDays, or all")
		}
		res, err = s.db.ExecContext(ctx, `DELETE FROM episodes WHERE `+strings.Join(clauses, " AND "), args...)
	}
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.armCheckpointLocked()
	}
	return n, nil
}

// AddPending records a scrubber-held write in the machine-local soul_pending table.
func (s *CompanionEpisodesStore) AddPending(ctx context.Context, in SoulPendingInput) (SoulPendingItem, error) {
	soulID := strings.TrimSpace(in.SoulID)
	if soulID == "" {
		soulID = companionDefaultSoulID
	}
	target := in.Target
	if target == "" {
		target = SoulPendingTargetShard
	}
	if target != SoulPendingTargetShard && target != SoulPendingTargetPersona && target != SoulPendingTargetProfile {
		return SoulPendingItem{}, fmt.Errorf("invalid pending target %q", target)
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = "companion"
	}
	findings := in.Findings
	if findings == nil {
		findings = []ScrubFinding{}
	}
	reasonsRaw, err := json.Marshal(findings)
	if err != nil {
		return SoulPendingItem{}, err
	}
	createdAt := s.opts.Now().UTC().Format(time.RFC3339)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return SoulPendingItem{}, errCompanionEpisodesClosed
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO soul_pending(soul_id, target, category, content, reasons_json, source, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		soulID, string(target), strings.TrimSpace(in.Category), in.Content, string(reasonsRaw), source, createdAt)
	if err != nil {
		return SoulPendingItem{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return SoulPendingItem{}, err
	}
	s.armCheckpointLocked()
	return SoulPendingItem{
		ID:        id,
		SoulID:    soulID,
		Target:    target,
		Category:  strings.TrimSpace(in.Category),
		Content:   in.Content,
		Findings:  findings,
		Source:    source,
		CreatedAt: createdAt,
	}, nil
}

// ListPending returns scrubber-held approval queue items (filtered by soulID when non-empty).
func (s *CompanionEpisodesStore) ListPending(ctx context.Context, soulID string) ([]SoulPendingItem, error) {
	soulID = strings.TrimSpace(soulID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errCompanionEpisodesClosed
	}
	var (
		rows *sql.Rows
		err  error
	)
	if soulID != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, soul_id, target, category, content, reasons_json, source, created_at FROM soul_pending WHERE soul_id = ? ORDER BY created_at DESC, id DESC`, soulID)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, soul_id, target, category, content, reasons_json, source, created_at FROM soul_pending ORDER BY created_at DESC, id DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]SoulPendingItem, 0)
	for rows.Next() {
		item, err := scanSoulPendingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// GetPending fetches a single pending item by ID.
func (s *CompanionEpisodesStore) GetPending(ctx context.Context, id int64) (SoulPendingItem, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return SoulPendingItem{}, false, errCompanionEpisodesClosed
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, soul_id, target, category, content, reasons_json, source, created_at FROM soul_pending WHERE id = ?`, id)
	var item SoulPendingItem
	var targetRaw, reasonsRaw string
	if err := row.Scan(&item.ID, &item.SoulID, &targetRaw, &item.Category, &item.Content, &reasonsRaw, &item.Source, &item.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SoulPendingItem{}, false, nil
		}
		return SoulPendingItem{}, false, err
	}
	item.Target = SoulPendingTarget(targetRaw)
	item.Findings = decodeScrubFindings(reasonsRaw)
	return item, true, nil
}

// DeletePending removes one item from soul_pending.
func (s *CompanionEpisodesStore) DeletePending(ctx context.Context, id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, errCompanionEpisodesClosed
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM soul_pending WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		s.armCheckpointLocked()
	}
	return n > 0, nil
}

// CountPending counts pending items for soulID (or all souls if empty).
func (s *CompanionEpisodesStore) CountPending(ctx context.Context, soulID string) (int, error) {
	soulID = strings.TrimSpace(soulID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errCompanionEpisodesClosed
	}
	var count int
	var err error
	if soulID != "" {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM soul_pending WHERE soul_id = ?`, soulID).Scan(&count)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM soul_pending`).Scan(&count)
	}
	return count, err
}

func scanSoulPendingRow(rows *sql.Rows) (SoulPendingItem, error) {
	var item SoulPendingItem
	var targetRaw, reasonsRaw string
	if err := rows.Scan(&item.ID, &item.SoulID, &targetRaw, &item.Category, &item.Content, &reasonsRaw, &item.Source, &item.CreatedAt); err != nil {
		return SoulPendingItem{}, err
	}
	item.Target = SoulPendingTarget(targetRaw)
	item.Findings = decodeScrubFindings(reasonsRaw)
	return item, nil
}

func decodeScrubFindings(raw string) []ScrubFinding {
	var findings []ScrubFinding
	if err := json.Unmarshal([]byte(raw), &findings); err != nil || findings == nil {
		return []ScrubFinding{}
	}
	return findings
}

func normalizeCompanionTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, raw := range tags {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		t = strings.Join(strings.Fields(t), "-")
		key := strings.ToLower(t)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	return out
}

func splitCompanionTags(raw string) []string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return []string{}
	}
	return fields
}

// buildCompanionFTS5Query tokenizes arbitrary user search input into quoted
// prefix-friendly FTS5 terms so punctuation (hyphens, colons, quotes) never
// triggers an SQLite FTS5 syntax error.
func buildCompanionFTS5Query(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tok := strings.ReplaceAll(cur.String(), `"`, `""`)
			tokens = append(tokens, `"`+tok+`"*`)
			cur.Reset()
		}
	}
	for _, r := range trimmed {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " AND ")
}

func companionSQLiteRetry(ctx context.Context, fn func() error) error {
	delay := 10 * time.Millisecond
	for {
		err := fn()
		if err == nil || !isCompanionSQLiteBusy(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			if delay < 100*time.Millisecond {
				delay *= 2
			}
		}
	}
}

func isCompanionSQLiteBusy(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	code := se.Code() & 0xff
	return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED
}
