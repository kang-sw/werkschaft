package wsmeta

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
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
// with fence_kind, fence_digest and fence_mtime. fence_required and
// fence_witness are sticky: once set, they survive re-staging and restarts.
//
//	none   --apply over the staged original-->  intent  (record durable first)
//	intent --publication durable----------->    landed
//	intent --re-apply, publication proven-->    landed  (only a durability barrier)
//	intent, landed --a pass that cannot prove the publication--> none
//	                  (the live state is re-staged as the original)
//
// Invariants:
//  1. The original is legacy-authored. A pass replaces it with the live
//     state only when the live state is not the record's publication, so
//     bytes the migration wrote are never staged as an original.
//  2. The record names exactly one publication for the current original. A
//     replacement is the record's digest under its sentinel mtime. A removal
//     is the path absent, with its witness (a sibling name, fixed per path)
//     in the original's state. The record changes only while the live state
//     is not that publication, so a publication left by any earlier,
//     interrupted attempt stays provable.
//  3. A fence is applied only over the staged original or over its own
//     proven publication; any other live state is refused (ErrFenceStale).
//     A legacy write is therefore staged before it is fenced, never lost.
//  4. The record is durable before its publication. landed is set only once
//     the publication's file data, sentinel mtime and directory entry are
//     durable. A pass cannot tell whether an unlanded publication is
//     durable, so it stays pending until the fence is re-applied.
//  5. Commit proceeds only when every path with fence_required has a landed
//     record that the commit's own pass proves. When proof fails, the outcome
//     is a re-stage plus a re-fence. The staged original is never written
//     back to the legacy path.
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
// its retained bytes and moved by the cutover, not imported into a table.
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
)

// Stage runs one durable capture pass: it discovers every bundled legacy
// file under the dashboard directory, including CLI-only session
// directories, and stores its current bytes, unless the migration's own
// fence demonstrably landed on that exact file, in which case the staged
// original is kept. Sessions deleted or recreated since an earlier pass lose
// their journal rows. Stage only reads legacy paths; it never creates,
// replaces or removes one. The caller holds dashboard.lock and has drained
// the legacy writers (2125, 2135).
func (s *Store) Stage(ctx context.Context) (StageResult, error) {
	var res StageResult
	err := s.write(ctx, durable, func(tx *sql.Tx) error {
		state, attempt, err := importState(ctx, tx)
		if err != nil {
			return err
		}
		if state == importComplete {
			return ErrImportComplete
		}
		attempt++
		if res, err = s.stagePass(ctx, tx, attempt); err != nil {
			return err
		}
		return setImportState(ctx, tx, importStaging, attempt)
	})
	return res, err
}

// ApplyFence replaces the legacy file at rel, a staged capture, with fenced:
// one of the cutover's one-time fences (2125). It follows the fence
// lifecycle above. The record (the fenced digest and a sentinel modification
// time) is durable first. Then fenced goes to a temporary file stamped with
// the sentinel, its data and time are synced, it atomically replaces rel,
// and the directory is synced before the fence is marked landed. The
// sentinel is a random even second in 2000-2009. Coarse file-system
// timestamps keep it, and no legacy writer produces it, because each writes
// the current time. So content a legacy writer wrote is never taken for the
// fence, even when it is byte-identical (2135).
//
// Re-applying after an interruption is safe. If the publication is already
// in place, ApplyFence only makes it durable and marks it landed; otherwise
// it publishes again over the staged original. A live file that is neither
// returns ErrFenceStale. The directory must exist: a fence never recreates a
// pruned session.
func (s *Store) ApplyFence(ctx context.Context, rel string, fenced []byte) error {
	abs := filepath.Join(s.dir, filepath.FromSlash(rel))
	plan, err := s.planFence(ctx, rel, fenceReplace, contentDigest(fenced), "")
	if err != nil || plan.landed {
		return err
	}
	if err := s.hit("fence-prepared"); err != nil {
		return err
	}
	if plan.proven {
		err = s.syncDir(filepath.Dir(abs))
	} else {
		err = s.publishFile(abs, fenced, time.Unix(plan.mtime, 0))
	}
	if err != nil {
		return err
	}
	if err := s.hit("fence-replaced"); err != nil {
		return err
	}
	return s.markFenceLanded(ctx, rel)
}

// ApplyRemovalFence removes the legacy file at rel, a staged capture, by
// moving it to witness. witness is a file name in rel's directory: the
// new-daemon-only input path. The migration owns that name until the import
// completes, and it is fixed for rel once chosen. The move replaces whatever
// an earlier, invalidated removal left there. When rel was staged absent,
// the fence instead removes any earlier witness, so an owner's deletion is
// never resurrected through it.
//
// A pass proves the removal only while rel is absent and witness is in the
// staged original's state. So an owner's own deletion of rel is never taken
// for the fence, and the import never undoes it (2135). A moved symbolic
// link still resolves the same way, because witness is in the same
// directory. Re-applying and durability follow ApplyFence.
func (s *Store) ApplyRemovalFence(ctx context.Context, rel, witness string) error {
	if err := checkWitnessName(rel, witness); err != nil {
		return err
	}
	witnessRel := path.Join(path.Dir(rel), witness)
	abs := filepath.Join(s.dir, filepath.FromSlash(rel))
	wabs := filepath.Join(s.dir, filepath.FromSlash(witnessRel))
	plan, err := s.planFence(ctx, rel, fenceRemove, absentDigest, witnessRel)
	if err != nil || plan.landed {
		return err
	}
	if err := s.hit("fence-prepared"); err != nil {
		return err
	}
	switch {
	case plan.proven:
	case plan.state == CaptureAbsent:
		if err := wsstate.RemoveFile(wabs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	default:
		if err := wsstate.AtomicReplaceFile(abs, wabs); err != nil {
			return err
		}
	}
	if !plan.proven {
		if err := s.hit("fence-renamed"); err != nil {
			return err
		}
	}
	if err := s.syncDir(filepath.Dir(abs)); err != nil {
		return err
	}
	if err := s.hit("fence-replaced"); err != nil {
		return err
	}
	return s.markFenceLanded(ctx, rel)
}

// checkWitnessName accepts a plain file name, local on every platform. It
// must not be a dot name (the temp and barrier namespace), a bundled file
// name, or rel's own name.
func checkWitnessName(rel, witness string) error {
	bad := witness == "" || strings.ContainsAny(witness, `/\:`) || strings.HasPrefix(witness, ".") ||
		!filepath.IsLocal(witness) || witness == path.Base(rel)
	for _, f := range sessionFiles {
		bad = bad || witness == f.name
	}
	for _, f := range dashboardFiles {
		bad = bad || witness == f.name
	}
	for _, d := range dashboardDirs {
		bad = bad || witness == d.name
	}
	if bad {
		return fmt.Errorf("wsmeta: invalid removal witness %q", witness)
	}
	return nil
}

// fencePlan is what a fence application does after its record is settled.
type fencePlan struct {
	state  string // the staged original's state
	mtime  int64  // the replacement's sentinel
	proven bool   // the publication is already live
	landed bool   // ...and marked landed: nothing left to do
}

// planFence settles the record of a fence on rel inside one durable
// transaction (invariants 2 and 3). A proven publication keeps its record;
// a live file equal to the staged original gets a fresh record; anything
// else is refused.
func (s *Store) planFence(ctx context.Context, rel, kind, digest, witness string) (fencePlan, error) {
	var plan fencePlan
	err := s.write(ctx, durable, func(tx *sql.Tx) error {
		state, _, err := importState(ctx, tx)
		if err != nil {
			return err
		}
		if state == importComplete {
			return ErrImportComplete
		}
		m, ok, err := loadCaptureMeta(ctx, tx, rel)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("wsmeta: no staged capture %q to fence", rel)
		}
		if kind == fenceRemove && m.fenceWitness != "" && m.fenceWitness != witness {
			return fmt.Errorf("%w: %s moves to %s", ErrFenceConflict, rel, m.fenceWitness)
		}
		abs := filepath.Join(s.dir, filepath.FromSlash(rel))
		got := readLegacy(abs, s.opts.ReadFile)
		if got.confirmable && m.state == CaptureUnreadable {
			// A denial a pass confirmed is the original's state while it
			// persists (stagePass), so the fence plans over it; a denial
			// over an original read before still refuses below.
			got = legacyRead{state: CaptureUnreadable, err: got.err}
		}
		if got.transient {
			return fmt.Errorf("%w: %s: %s", ErrLegacyUnread, rel, got.err)
		}
		proven, unread := s.fenceProven(m, got)
		if unread {
			return fmt.Errorf("%w: witness of %s", ErrLegacyUnread, rel)
		}
		plan = fencePlan{state: m.state, mtime: m.fenceMtime, proven: proven}
		if proven {
			if m.fenceKind != kind || kind == fenceReplace && m.fenceDigest != digest {
				return fmt.Errorf("%w: %s", ErrFenceConflict, rel)
			}
			plan.landed = m.fenceState == FenceLanded
			return nil
		}
		if got.state != m.state || got.digest != m.digest {
			return fmt.Errorf("%w: %s", ErrFenceStale, rel)
		}
		if kind == fenceReplace {
			plan.mtime = distinctFenceSentinel(abs).Unix()
		} else {
			plan.mtime = 0
		}
		_, err = tx.ExecContext(ctx, `UPDATE legacy_captures SET fence_state = 'intent', fence_kind = ?, fence_digest = ?, fence_mtime = ?, fence_witness = ?, fence_required = 1 WHERE path = ?`,
			kind, digest, plan.mtime, witness, rel)
		return err
	})
	return plan, err
}

// distinctFenceSentinel picks a sentinel whose second differs from the live
// file's current mtime. Legacy readers cache a file by its stat (mode, size,
// mtime and identity: wsagent/permission_store.go:85 through
// wsstate.SameFileMetadata). The replacement changes the inode on POSIX, but
// the Windows identity is the creation time, which NTFS tunneling can carry
// over to the replacement. So a fence of the same size must never keep the
// previous mtime. The mtime only needs to differ; no legacy reader of a
// fenced path orders mtimes.
func distinctFenceSentinel(abs string) time.Time {
	sentinel := fenceSentinel()
	if info, err := wsstate.Stat(abs); err == nil {
		for sentinel.Unix() == info.ModTime().Unix() {
			sentinel = fenceSentinel()
		}
	}
	return sentinel
}

// fenceSentinel picks a random even second in [2000, 2010). A variable so a
// test can force a collision.
var fenceSentinel = randomFenceSentinel

func randomFenceSentinel() time.Time {
	const lo, span = 946684800, 315532800 // 2000-01-01, ten years
	var b [8]byte
	_, _ = rand.Read(b[:])
	n := int64(binary.LittleEndian.Uint64(b[:])>>1) % (span / 2)
	return time.Unix(lo+2*n, 0)
}

// publishFile durably replaces abs with data stamped mtime. The temporary
// file's data and mtime are synced before the rename, and the directory
// after it (invariant 4).
func (s *Store) publishFile(abs string, data []byte, mtime time.Time) error {
	f, err := wsstate.CreateTemp(filepath.Dir(abs), "."+filepath.Base(abs)+".cutover-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(tmp, mtime, mtime); err != nil {
		return err
	}
	if err := syncFile(tmp); err != nil {
		return err
	}
	if err := wsstate.AtomicReplaceFile(tmp, abs); err != nil {
		return err
	}
	ok = true
	if err := s.hit("fence-renamed"); err != nil {
		return err
	}
	return s.syncDir(filepath.Dir(abs))
}

// Durability seams: syncFile flushes a file's data and metadata, and
// syncDirectory makes the entries of a directory durable (sync_*.go).
var (
	syncFile      = syncFileData
	syncDirectory = platformSyncDir
)

func syncFileData(p string) error {
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (s *Store) syncDir(dir string) error { return syncDirectory(dir) }

func (s *Store) markFenceLanded(ctx context.Context, rel string) error {
	return s.write(ctx, durable, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET fence_state = 'landed' WHERE path = ? AND fence_state = 'intent'`, rel)
		return err
	})
}

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

// legacyRead is one bundled file as discovery found it.
type legacyRead struct {
	state  string
	bytes  []byte
	digest string
	err    string
	mtime  time.Time // of the file (or link target) discovery stat'ed
	// transient marks a read that failed for a reason a retry may clear;
	// state is then unset and the previous capture row, if any, stands.
	transient bool
	// confirmable marks a transient failure that reads as permanent once it
	// persists (readClass).
	confirmable bool
}

// readLegacy reads one bundled file. A dangling symbolic link, a directory
// in its place or a stable read error is an existing, unreadable input, kept
// apart from absence (permission selection fails closed on it). Any other
// read error (a sharing violation outliving the retry, an I/O error) is
// transient: the pass neither captures nor imports it.
func readLegacy(abs string, readFile func(string) ([]byte, error)) legacyRead {
	info, err := wsstate.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return legacyRead{state: CaptureAbsent, digest: absentDigest}
	}
	if err != nil {
		return readFailure(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		// A link counts by its target: dangling is an existing, unreadable
		// selection; anything but a regular file is never opened.
		target, err := wsstate.Stat(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return legacyRead{state: CaptureUnreadable, err: "dangling symbolic link"}
		case err != nil:
			return readFailure(err)
		}
		info = target
	}
	if !info.Mode().IsRegular() {
		return legacyRead{state: CaptureUnreadable, err: "not a regular file"}
	}
	data, err := readFile(abs)
	if err != nil {
		switch {
		case errors.Is(err, errNotRegular):
			return legacyRead{state: CaptureUnreadable, err: "not a regular file"}
		case errors.Is(err, fs.ErrNotExist):
			// Removed (or its link target removed) after the stat: read as
			// changed, and re-read by the next pass.
			return legacyRead{transient: true, err: err.Error()}
		}
		return readFailure(err)
	}
	if data == nil {
		data = []byte{}
	}
	return legacyRead{state: CapturePresent, bytes: data, digest: contentDigest(data), mtime: info.ModTime()}
}

// denialConfirmable reports whether a confirmable denial may become, or stay,
// a row's confirmed unreadable state. A confirmed failure never replaces an
// original read before (a Windows pending delete can outlast the
// confirmation window), nor an absent original under a fence record (the
// live file may be the fence's result). An unreadable original stays
// confirmed even under a fence record: the migration can always read its own
// publication, so a denied live file is still that original.
func denialConfirmable(had bool, prev captureMeta) bool {
	return !had || prev.state == CaptureUnreadable || prev.state == CaptureAbsent && prev.fenceState == FenceNone
}

// readClass sorts a read error: transient errors may clear on a later pass;
// a stable one is the file's state; a confirmable one is transient until it
// persists across UnreadConfirmPasses passes spanning UnreadConfirmAfter,
// then reads as stable.
type readClass int

const (
	classTransient readClass = iota
	classStable
	classConfirmable
)

// classifyReadError is the platform's read error classification (a test
// seam).
var classifyReadError = platformReadErrorClass

// A confirmable read failure (Windows access denied: an ACL, or a pending
// delete) becomes a permanent, unreadable input once it persisted across this
// many staging passes and this long, so a selector its ACL denies fails
// closed for its own session instead of blocking every session's import.
const (
	UnreadConfirmPasses = 3
	UnreadConfirmAfter  = 5 * time.Second
)

func readFailure(err error) legacyRead {
	switch classifyReadError(err) {
	case classStable:
		return legacyRead{state: CaptureUnreadable, err: err.Error()}
	case classConfirmable:
		return legacyRead{transient: true, confirmable: true, err: err.Error()}
	}
	return legacyRead{transient: true, err: err.Error()}
}

// errNotRegular reports a bundled path that opened as something other than a
// regular file.
var errNotRegular = errors.New("not a regular file")

func contentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type captureMeta struct {
	state, digest, fenceState, fenceDigest, fenceWitness, fenceKind string
	fenceRequired                                                   bool
	fenceMtime                                                      int64
}

// loadCaptureMeta reads one capture row's state and fence columns.
func loadCaptureMeta(ctx context.Context, q querier, rel string) (captureMeta, bool, error) {
	var m captureMeta
	err := q.QueryRowContext(ctx, `SELECT state, digest, fence_state, fence_digest, fence_witness, fence_kind, fence_required, fence_mtime FROM legacy_captures WHERE path = ?`, rel).
		Scan(&m.state, &m.digest, &m.fenceState, &m.fenceDigest, &m.fenceWitness, &m.fenceKind, &m.fenceRequired, &m.fenceMtime)
	if errors.Is(err, sql.ErrNoRows) {
		return m, false, nil
	}
	return m, err == nil, err
}

// fenceProven reports whether the live state is the publication of the
// row's fence record (invariant 2). For a replacement that is the record's
// digest under its exact sentinel time. For a removal it is rel absent, with
// the witness in the staged original's state. unread is set when the witness
// could not be read for a transient reason.
func (s *Store) fenceProven(prev captureMeta, got legacyRead) (proven, unread bool) {
	if prev.fenceState == FenceNone {
		return false, false
	}
	if prev.fenceKind == fenceRemove {
		if got.state != CaptureAbsent {
			return false, false
		}
		w := readLegacy(filepath.Join(s.dir, filepath.FromSlash(prev.fenceWitness)), s.opts.ReadFile)
		if w.transient {
			return false, true
		}
		return w.state == prev.state && w.digest == prev.digest, false
	}
	return got.state == CapturePresent && got.digest == prev.fenceDigest &&
		got.mtime.Unix() == prev.fenceMtime && got.mtime.Nanosecond() == 0, false
}

// stagePass captures every bundled file inside tx. It returns the pass
// counts; rows of files that disappeared are deleted.
func (s *Store) stagePass(ctx context.Context, tx *sql.Tx, attempt int) (StageResult, error) {
	res := StageResult{Attempt: attempt}
	now := s.opts.Now().UTC().Format(time.RFC3339Nano)
	existing := map[string]captureMeta{}
	rows, err := tx.QueryContext(ctx, `SELECT path, state, digest, fence_state, fence_digest, fence_witness, fence_kind, fence_required, fence_mtime FROM legacy_captures`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var p string
		var m captureMeta
		if err := rows.Scan(&p, &m.state, &m.digest, &m.fenceState, &m.fenceDigest, &m.fenceWitness, &m.fenceKind, &m.fenceRequired, &m.fenceMtime); err != nil {
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
				// pending (invariant 4).
				res.Reused++
				if prev.fenceRequired && prev.fenceState != FenceLanded {
					res.Pending = append(res.Pending, rel)
				}
				_, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET attempt = ?, session_gen = ? WHERE path = ?`, attempt, gen, rel)
				return err
			}
			// Nothing the migration published is live: the live state is the
			// original and the record goes (invariant 1). The obligation
			// outlives this and a restart (invariant 5).
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
		if cur, ok := current[id]; !ok || !sameGeneration(cur, gen) {
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
		for _, f := range sessionFiles {
			if err := capture(path.Join(sessionsDirName, id, f.name), f.store, id, gen); err != nil {
				return res, err
			}
		}
		// The directory may have been pruned or recreated while it was
		// read; such a session is not imported and is never recreated.
		if again, ok, err := s.sessionGeneration(id); err != nil {
			return res, err
		} else if !ok || !sameGeneration(again, gen) {
			if err := discard(id); err != nil {
				return res, err
			}
			for _, f := range sessionFiles {
				delete(seen, path.Join(sessionsDirName, id, f.name))
			}
			continue
		}
		res.Sessions++
		if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_sessions(reply_id, session_gen, attempt) VALUES(?, ?, ?)
			ON CONFLICT(reply_id) DO UPDATE SET session_gen = excluded.session_gen, attempt = excluded.attempt`, id, gen, attempt); err != nil {
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
	// Files gone since an earlier pass are no longer inputs.
	for p := range existing {
		if !seen[p] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM legacy_captures WHERE path = ?`, p); err != nil {
				return res, err
			}
		}
	}
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

// generationOf is the platform's directory generation (gen_*.go; a test
// seam that simulates a missed recreation or a failed lookup). A lookup
// that fails is inconclusive: it never falls back to a weaker token. A
// weaker token would read as another incarnation and discard the session's
// journal rows, fenced originals included. A non-absence error fails the
// pass, which changes nothing, and the caller retries.
var generationOf = dirGeneration

// sessionGenError wraps an inconclusive generation lookup.
func sessionGenError(id string, err error) error {
	return fmt.Errorf("wsmeta: session %s identity unavailable (retry the pass): %w", id, err)
}

// sessionGenerations lists the session directories and their generations.
func (s *Store) sessionGenerations() (map[string]string, error) {
	out := map[string]string{}
	entries, err := wsstate.ReadDir(filepath.Join(s.dir, sessionsDirName))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !replyIDPattern.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		gen, err := generationOf(filepath.Join(s.dir, sessionsDirName, e.Name()), info)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, sessionGenError(e.Name(), err)
		}
		out[e.Name()] = gen
	}
	return out, nil
}

// sessionGeneration re-reads one session directory's generation; ok is
// false when it no longer exists as a directory.
func (s *Store) sessionGeneration(id string) (string, bool, error) {
	info, err := wsstate.Lstat(filepath.Join(s.dir, sessionsDirName, id))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() {
		return "", false, nil
	}
	gen, err := generationOf(filepath.Join(s.dir, sessionsDirName, id), info)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, sessionGenError(id, err)
	}
	return gen, true, nil
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
