package wsmeta

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strconv"
)

// Legacy store names, one per bundled file kind. A capture row's store says
// how the import commit decodes its bytes.
const (
	StoreAgentRecord        = "agent_record"
	StorePermission         = "permission"
	StoreLabel              = "label"
	StoreAckCheck           = "ack_check"
	StoreHiring             = "hiring"
	StoreSupport            = "support"
	StoreRailLayout         = "rail_layout"
	StoreUsageLatest        = "usage_latest"
	StoreUsageLatestAgy     = "usage_latest_agy"
	StoreUsageHistory       = "usage_history"
	StoreUsageHistoryCodex  = "usage_history_codex"
	StoreUsageHistoryAgy    = "usage_history_agy"
	StorePiProbe            = "pi_probe"
	StoreMemo               = "memo"
	StoreTrackerHookState   = "tracker_hook_state"
	StoreTrackerHooksConfig = "tracker_hooks_config"
)

// Capture states.
const (
	CapturePresent    = "present"
	CaptureAbsent     = "absent"
	CaptureUnreadable = "unreadable"
)

// Fence lifecycle (2125, 2135). A capture row holds one legacy path's
// original (state, bytes, digest) and at most one fence record: fence_state
// with fence_kind, fence_digest and fence_mtime. fence_required,
// fence_witness and the last witness publication (witness_digest,
// witness_mtime) are sticky: once set, they survive re-staging and restarts.
//
//	none   --apply over the staged original-->  intent  (record durable first)
//	intent --publication durable----------->    landed
//	intent --re-apply, publication proven-->    landed  (published again; a removal
//	                                                     gets a durability barrier)
//	intent, landed --a pass that cannot prove the publication--> none
//	                  (the live state is re-staged as the original)
//
// Invariants:
//  1. The original is legacy-authored. A pass replaces it with the live
//     state only when the live state is not the record's publication, so
//     bytes the migration wrote are never staged as an original.
//  2. The record names exactly one publication for the current original. A
//     replacement is the record's digest under its sentinel mtime at the
//     path. A removal is the path absent, with its witness (a sibling name,
//     fixed per path) holding the original's bytes under the record's
//     sentinel mtime, or absent when the original was. The record changes
//     only while the live state is not that publication, so a publication
//     left by any earlier, interrupted attempt stays provable. No earlier
//     state can prove a new record: a new sentinel differs from the live
//     mtime of the file it replaces, and the witness path is durably empty
//     before a removal record is written.
//  3. The migration overwrites or deletes at the witness path only the
//     file its last removal published there (witness_digest under
//     witness_mtime); any other entry there refuses the removal
//     (ErrWitnessOccupied). The legacy path itself is deleted only once its
//     bytes are published at the witness.
//  4. A fence is applied only over the staged original or over its own
//     proven publication; any other live state is refused (ErrFenceStale).
//     A legacy write is therefore staged before it is fenced, never lost.
//  5. The record is durable before its publication. landed is set only once
//     the publication's file data, sentinel mtime and directory entry are
//     durable. A pass cannot tell whether an unlanded publication is
//     durable, so it stays pending until the fence is re-applied. Every
//     rename and deletion the migration makes is itself durable (a
//     directory sync on POSIX, a write-through rename on Windows). Only a
//     re-applied removal, whose deletion an interrupted attempt made, rests
//     on a directory barrier alone (sync_*.go). On Windows that barrier
//     holds only on NTFS, so the cutover refuses any other file system:
//     ErrCutoverUnsupported while no fence was ever recorded (legacy
//     service may continue), ErrCutoverInterrupted once one was.
//  6. Commit proceeds only when every path with fence_required has a landed
//     record that the commit's own pass proves. When proof fails, the outcome
//     is a re-stage plus a re-fence. The staged original is never written
//     back to the legacy path.
//
// The cutover calls hold the cutover lock end to end, so each transition
// above, journal and file system together, is atomic with respect to the
// others.
const (
	FenceNone   = "none"
	FenceIntent = "intent"
	FenceLanded = "landed"
)

// Fence kinds.
const (
	fenceReplace = "replace"
	fenceRemove  = "remove"
)

// absentDigest is the digest of a missing file.
const absentDigest = "absent"

const sessionsDirName = "sessions"

// sessionFiles are the bundled files of one session directory, hosted or
// CLI-only alike. Every other file there (registration.json and its lock,
// the wslive snapshot and ring, holder files, inbox, Pi session files,
// system_prompt.md) is adjacent: discovery never opens it.
var sessionFiles = []struct{ name, store string }{
	{"agent.json", StoreAgentRecord},
	{"permission.json", StorePermission},
	{"label.json", StoreLabel},
	{"ack-check.json", StoreAckCheck},
	{"hiring.json", StoreHiring},
}

// dashboardFiles are the fixed-name bundled files of the dashboard
// directory. tracker-hooks.json is input configuration: it is captured for
// its retained bytes and published at its new input path by the cutover,
// not imported into a table.
var dashboardFiles = []struct{ name, store string }{
	{"support.json", StoreSupport},
	{"rail-layout.json", StoreRailLayout},
	{"plan-usage.json", StoreUsageLatest},
	{"plan-usage-agy.json", StoreUsageLatestAgy},
	{"plan-usage-history.json", StoreUsageHistory},
	{"plan-usage-codex-history.json", StoreUsageHistoryCodex},
	{"plan-usage-agy-history.json", StoreUsageHistoryAgy},
	{"tracker-hooks.json", StoreTrackerHooksConfig},
}

// dashboardDirs hold one bundled file per key.
var dashboardDirs = []struct{ name, store string }{
	{"pi-probes-v1", StorePiProbe},
	{"memos", StoreMemo},
	{"tracker-hooks", StoreTrackerHookState},
}

// replyIDPattern is wsdashboard.ValidReplyID's pattern (registry.go); this
// package cannot import wsdashboard.
var replyIDPattern = regexp.MustCompile(`^[a-z0-9]{1,64}$`)

// Capture is one row of the durable capture journal.
type Capture struct {
	Path        string // slash path relative to the dashboard directory
	Store       string
	ReplyID     string
	SessionGen  string
	State       string
	Bytes       []byte // original bytes; nil unless State is CapturePresent
	Digest      string
	ReadError   string
	Attempt     int
	CapturedAt  string
	FenceState  string
	FenceDigest string
	// FenceRequired is set once a fence was applied to this path and stays
	// set across re-staging: the import commits only after a fence on this
	// path is landed and proven in place again.
	FenceRequired bool
	// FenceModTime is the sentinel modification time (Unix seconds) a
	// replacement fence carries. FenceWitness is the sibling path a removal
	// fence moves the file to; it is fixed for the path once chosen.
	FenceModTime int64
	FenceWitness string
	// ImportStatus is set by the import commit: imported, absent, invalid,
	// future, ignored or retained.
	ImportStatus string
	ImportDetail string
}

// StageResult describes one capture pass.
type StageResult struct {
	Attempt   int
	Sessions  int
	Files     int
	Restaged  int // rows whose bytes were (re)captured from the live file
	Reused    int // rows whose staged original was kept because its fence publication is live
	Discarded int // sessions dropped because their directory vanished or was recreated
	// Missing lists sessions whose directory vanished while they carry a
	// fence record: their journal rows, staged originals included, are kept
	// and not imported until the directory returns under its generation.
	Missing []string
	// Unfenced lists capture paths that had a fence intent but whose live
	// file is not that fence's result (the fence never landed, or a legacy
	// writer replaced it since). They are live legacy authority again.
	Unfenced []string
	// Unread lists capture paths whose live file could not be read for a
	// transient reason. Their earlier row, if any, is kept unchanged; a
	// commit refuses with ErrLegacyUnread until a pass reads them.
	Unread []string
	// Pending lists capture paths with a required fence that this pass did
	// not find both landed and proven in place (Unfenced paths included).
	// Commit refuses with ErrFencePending while any remain; apply the fence
	// again.
	Pending []string
}

var (
	// ErrImportComplete reports a staging or fence call after the import
	// committed: SQLite owns the bundled stores and legacy files are frozen.
	ErrImportComplete = errors.New("wsmeta: legacy import already completed")
	// ErrNotImported reports a read or live write before the import marker.
	ErrNotImported = errors.New("wsmeta: legacy import has not completed")
	// ErrFenceInvalidated reports a commit attempted while a fenced file was
	// found unfenced again; re-stage and re-fence before committing.
	ErrFenceInvalidated = errors.New("wsmeta: a fenced legacy file is no longer fenced")
	// ErrLegacyUnread reports a commit attempted while a bundled legacy file
	// could not be read for a transient reason; retry the commit.
	ErrLegacyUnread = errors.New("wsmeta: a legacy file could not be read")
	// ErrFencePending reports a commit attempted while a path that needs a
	// fence has none proven in place; apply the fence again.
	ErrFencePending = errors.New("wsmeta: a required fence is not in place")
	// ErrFenceStale reports a fence refused because the live legacy file is
	// neither its staged original nor the fence's own publication; stage
	// again, then fence.
	ErrFenceStale = errors.New("wsmeta: the legacy file changed since it was staged")
	// ErrFenceConflict reports a fence that differs from the one already
	// published on the path (other content, kind or witness).
	ErrFenceConflict = errors.New("wsmeta: a different fence is already in place")
	// ErrWitnessOccupied reports a removal fence refused because its witness
	// path holds an entry the migration did not publish there. The owner
	// resolves it; the migration never replaces or deletes that entry.
	ErrWitnessOccupied = errors.New("wsmeta: the removal witness path is occupied")
)

// Captures lists the capture journal in path order.
func (s *Store) Captures(ctx context.Context) ([]Capture, error) {
	var out []Capture
	err := s.read(ctx, func(q querier) error {
		rows, err := loadCaptures(ctx, q)
		out = rows
		return err
	})
	return out, err
}

const captureColumns = `path, store, reply_id, session_gen, state, bytes, digest, read_error, attempt, captured_at, fence_state, fence_digest, fence_required, fence_mtime, fence_witness, import_status, import_detail`

func loadCaptures(ctx context.Context, q querier) ([]Capture, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+captureColumns+` FROM legacy_captures ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Capture
	for rows.Next() {
		var c Capture
		if err := rows.Scan(&c.Path, &c.Store, &c.ReplyID, &c.SessionGen, &c.State, &c.Bytes, &c.Digest, &c.ReadError, &c.Attempt, &c.CapturedAt, &c.FenceState, &c.FenceDigest, &c.FenceRequired, &c.FenceModTime, &c.FenceWitness, &c.ImportStatus, &c.ImportDetail); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type captureMeta struct {
	state, digest, fenceState, fenceDigest, fenceWitness, fenceKind string
	fenceRequired                                                   bool
	fenceMtime, witnessMtime                                        int64
	witnessDigest                                                   string
}

// loadCaptureMeta reads one capture row's state and fence columns.
func loadCaptureMeta(ctx context.Context, q querier, rel string) (captureMeta, bool, error) {
	var m captureMeta
	err := q.QueryRowContext(ctx, `SELECT state, digest, fence_state, fence_digest, fence_witness, fence_kind, fence_required, fence_mtime, witness_digest, witness_mtime FROM legacy_captures WHERE path = ?`, rel).
		Scan(&m.state, &m.digest, &m.fenceState, &m.fenceDigest, &m.fenceWitness, &m.fenceKind, &m.fenceRequired, &m.fenceMtime, &m.witnessDigest, &m.witnessMtime)
	if errors.Is(err, sql.ErrNoRows) {
		return m, false, nil
	}
	return m, err == nil, err
}

const (
	importStaging  = "staging"
	importComplete = "complete"
)

func importState(ctx context.Context, q querier) (state string, attempt int, err error) {
	rows, err := q.QueryContext(ctx, `SELECT key, value FROM meta WHERE key IN ('import_state', 'import_attempt')`)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return "", 0, err
		}
		switch k {
		case "import_state":
			state = v
		case "import_attempt":
			attempt, _ = strconv.Atoi(v)
		}
	}
	return state, attempt, rows.Err()
}

func setImportState(ctx context.Context, tx *sql.Tx, state string, attempt int) error {
	for k, v := range map[string]string{"import_state": state, "import_attempt": strconv.Itoa(attempt)} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return nil
}

// ImportComplete reports whether the import marker has committed.
func (s *Store) ImportComplete(ctx context.Context) (bool, error) {
	var done bool
	err := s.read(ctx, func(q querier) error {
		state, _, err := importState(ctx, q)
		done = state == importComplete
		return err
	})
	return done, err
}
