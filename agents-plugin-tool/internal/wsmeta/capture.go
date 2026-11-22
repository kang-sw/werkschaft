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

// Fence states of a capture row.
const (
	FenceNone   = "none"
	FenceIntent = "intent"
	FenceLanded = "landed"
)

// absentDigest is the digest of a missing file, so a removal fence (the
// legacy tracker-hooks.json) is recognised exactly as a replacement is.
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
	// path is proven in place again.
	FenceRequired bool
	// FenceModTime is the sentinel modification time (Unix seconds) a
	// replacement fence carries; FenceWitness is where a removal fence moved
	// the file.
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
	Reused    int // rows whose staged original was kept because its fence landed
	Discarded int // sessions dropped because their directory vanished or was recreated
	// Unfenced lists capture paths that had a fence intent but whose live
	// file is not that fence's result (the fence never landed, or a legacy
	// writer replaced it since). They are live legacy authority again.
	Unfenced []string
	// Unread lists capture paths whose live file could not be read for a
	// transient reason. Their earlier row, if any, is kept unchanged; a
	// commit refuses with ErrLegacyUnread until a pass reads them.
	Unread []string
	// Pending lists capture paths with a required fence that is not proven
	// in place in this pass (Unfenced paths included). Commit refuses with
	// ErrFencePending while any remain; apply the fence again.
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
// one of the cutover's one-time fences (2125). It durably records the intent
// first, with the fenced digest and a sentinel modification time, then
// writes fenced to a temporary file in the same directory stamped with that
// time, atomically replaces rel with it and marks the fence landed. The
// sentinel is a random even second in 2000-2009, which survives coarse
// file-system timestamps and which no legacy writer produces (each writes a
// current time). A later pass keeps the staged original only while the live
// file has both the fenced digest and the sentinel time, so byte-identical
// content a legacy writer wrote is never taken for the fence (2135). The
// directory must exist: a fence never recreates a pruned session.
func (s *Store) ApplyFence(ctx context.Context, rel string, fenced []byte) error {
	sentinel := fenceSentinel()
	if err := s.prepareFence(ctx, rel, contentDigest(fenced), sentinel.Unix(), ""); err != nil {
		return err
	}
	abs := filepath.Join(s.dir, filepath.FromSlash(rel))
	if err := s.hit("fence-prepared"); err != nil {
		return err
	}
	if err := writeFenceFile(abs, fenced, sentinel); err != nil {
		return err
	}
	if err := s.hit("fence-replaced"); err != nil {
		return err
	}
	return s.markFenceLanded(ctx, rel)
}

// ApplyRemovalFence removes the legacy file at rel, a staged present
// capture, by moving it to witness (a slash path relative to the dashboard
// directory that must not exist yet: the new-daemon-only input path). A
// later pass keeps the staged original only while rel is absent and witness
// holds the original's bytes, so an owner's deletion of rel is never taken
// for the fence and never undone by the import (2135).
func (s *Store) ApplyRemovalFence(ctx context.Context, rel, witness string) error {
	if witness == "" || path.IsAbs(witness) || path.Clean(witness) != witness || strings.HasPrefix(witness, "../") || witness == ".." || witness == rel {
		return fmt.Errorf("wsmeta: invalid removal witness %q", witness)
	}
	wabs := filepath.Join(s.dir, filepath.FromSlash(witness))
	if _, err := wsstate.Lstat(wabs); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("wsmeta: removal witness %q already exists", witness)
	}
	if err := s.prepareFence(ctx, rel, absentDigest, 0, witness); err != nil {
		return err
	}
	if err := s.hit("fence-prepared"); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(s.dir, filepath.FromSlash(rel)), wabs); err != nil {
		return err
	}
	if err := s.hit("fence-replaced"); err != nil {
		return err
	}
	return s.markFenceLanded(ctx, rel)
}

// fenceSentinel picks a random even second in [2000, 2010).
func fenceSentinel() time.Time {
	const lo, span = 946684800, 315532800 // 2000-01-01, ten years
	var b [8]byte
	_, _ = rand.Read(b[:])
	n := int64(binary.LittleEndian.Uint64(b[:])>>1) % (span / 2)
	return time.Unix(lo+2*n, 0)
}

// writeFenceFile writes data beside abs, stamps it with mtime and atomically
// replaces abs with it.
func writeFenceFile(abs string, data []byte, mtime time.Time) error {
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
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(tmp, mtime, mtime); err != nil {
		return err
	}
	if err := wsstate.AtomicReplaceFile(tmp, abs); err != nil {
		return err
	}
	ok = true
	return nil
}

func (s *Store) prepareFence(ctx context.Context, rel, digest string, mtime int64, witness string) error {
	return s.write(ctx, durable, func(tx *sql.Tx) error {
		state, _, err := importState(ctx, tx)
		if err != nil {
			return err
		}
		if state == importComplete {
			return ErrImportComplete
		}
		cond := ``
		if witness != "" {
			cond = ` AND state = 'present'`
		}
		kind := "replace"
		if witness != "" {
			kind = "remove"
		}
		res, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET fence_state = 'intent', fence_digest = ?, fence_mtime = ?, fence_witness = ?, fence_required = 1, fence_kind = ? WHERE path = ?`+cond, digest, mtime, witness, kind, rel)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("wsmeta: no staged capture %q to fence", rel)
		}
		return nil
	})
}

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
		if stableReadError(err) {
			return legacyRead{state: CaptureUnreadable, err: err.Error()}
		}
		return legacyRead{transient: true, err: err.Error()}
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		// A link counts by its target: dangling is an existing, unreadable
		// selection; anything but a regular file is never opened.
		target, err := wsstate.Stat(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return legacyRead{state: CaptureUnreadable, err: "dangling symbolic link"}
		case err != nil && stableReadError(err):
			return legacyRead{state: CaptureUnreadable, err: err.Error()}
		case err != nil:
			return legacyRead{transient: true, err: err.Error()}
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
		case stableReadError(err):
			return legacyRead{state: CaptureUnreadable, err: err.Error()}
		}
		return legacyRead{transient: true, err: err.Error()}
	}
	if data == nil {
		data = []byte{}
	}
	return legacyRead{state: CapturePresent, bytes: data, digest: contentDigest(data), mtime: info.ModTime()}
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
	fenceRequired                                                 bool
	fenceMtime                                         int64
}

// fenceProven reports whether the migration's own fence on a capture is the
// live file: a replacement fence's digest and sentinel time, or a removal
// fence's absent path with the original bytes at its witness. unread is set
// when the witness could not be read for a transient reason.
func (s *Store) fenceProven(prev captureMeta, got legacyRead) (proven, unread bool) {
	if prev.fenceState == FenceNone {
		return false, false
	}
	if prev.fenceWitness != "" {
		if got.state != CaptureAbsent {
			return false, false
		}
		w := readLegacy(filepath.Join(s.dir, filepath.FromSlash(prev.fenceWitness)), s.opts.ReadFile)
		if w.transient {
			return false, true
		}
		return w.state == CapturePresent && w.digest == prev.digest, false
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

	seen := map[string]bool{}
	capture := func(rel, store, replyID, gen string) error {
		seen[rel] = true
		res.Files++
		got := readLegacy(filepath.Join(s.dir, filepath.FromSlash(rel)), s.opts.ReadFile)
		prev, had := existing[rel]
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
				// The migration's own fence is the live file: keep the original.
				res.Reused++
				_, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET attempt = ?, session_gen = ? WHERE path = ?`, attempt, gen, rel)
				return err
			}
			if prev.fenceState != FenceNone {
				res.Unfenced = append(res.Unfenced, rel)
			}
			// The obligation outlives the re-stage below and a restart. A
			// removal is satisfied by a path that is absent and was staged
			// absent: nothing is left to retain or fence.
			removed := prev.fenceKind == "remove" && prev.fenceState == FenceNone && prev.state == CaptureAbsent && got.state == CaptureAbsent
			if prev.fenceRequired && !removed {
				res.Pending = append(res.Pending, rel)
			}
		}
		if had && got.state != CaptureUnreadable && prev.state == got.state && prev.digest == got.digest {
			// Unchanged original (its fence, if any, never landed).
			_, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET attempt = ?, session_gen = ?, fence_state = 'none', fence_digest = '', fence_mtime = 0, fence_witness = '' WHERE path = ?`, attempt, gen, rel)
			return err
		}
		res.Restaged++
		_, err := tx.ExecContext(ctx, `INSERT INTO legacy_captures(path, store, reply_id, session_gen, state, bytes, digest, read_error, attempt, captured_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET store = excluded.store, reply_id = excluded.reply_id, session_gen = excluded.session_gen,
				state = excluded.state, bytes = excluded.bytes, digest = excluded.digest, read_error = excluded.read_error,
				attempt = excluded.attempt, captured_at = excluded.captured_at, fence_state = 'none', fence_digest = '',
				fence_mtime = 0, fence_witness = '', import_status = '', import_detail = ''`,
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
		if cur, ok := current[id]; !ok || cur != gen {
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
		} else if !ok || again != gen {
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
		out[e.Name()] = dirGeneration(info)
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
	return dirGeneration(info), true, nil
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
