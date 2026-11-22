package wsmeta

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Import statuses of a capture row.
const (
	ImportImported = "imported"
	ImportAbsent   = "absent"
	ImportInvalid  = "invalid"
	ImportFuture   = "future"
	ImportIgnored  = "ignored"
	// ImportRetained marks an input kept only for its bytes (the legacy
	// tracker-hooks.json, which the cutover moves to a new-daemon path).
	ImportRetained = "retained"
)

// CommitOptions tunes Commit.
type CommitOptions struct {
	// BeforeMarker runs inside the import transaction after the final
	// capture pass and the keyed import, before the completion marker. A
	// non-nil error rolls everything back (the cutover's fence check).
	BeforeMarker func(captures []Capture) error
	// Validate holds the legacy owners' own decoders, keyed by capture
	// store. A present document this package decoded is imported only when
	// its validator accepts it; a refusal imports it as invalid, keeping
	// its bytes in the journal. This package cannot import the owners, so
	// Commit refuses with ErrValidatorMissing while a present capture of a
	// store in RequiredValidators has none: agent records (wsagent's
	// readRecord decode, every known member typed) and the usage histories
	// (wsdashboard's per-harness sample validity).
	Validate map[string]func(replyID string, raw []byte) error
}

// RequiredValidators are the stores whose legacy reader rejects documents
// this package's member-level decode would accept.
var RequiredValidators = []string{StoreAgentRecord, StoreUsageHistory, StoreUsageHistoryCodex, StoreUsageHistoryAgy}

// ErrValidatorMissing reports a commit without a required legacy validator.
var ErrValidatorMissing = errors.New("wsmeta: a required legacy validator is missing")

// ImportResult describes a committed (or already committed) import.
type ImportResult struct {
	AlreadyComplete bool
	Stage           StageResult
	// Status counts capture rows by import status.
	Status map[string]int
}

// Commit is the import's single commit boundary (2125). Inside one durable
// SQLite transaction it re-runs the capture pass (re-reading every unfenced
// file and re-enumerating sessions, so a vanished session is not imported),
// replaces the keyed tables with the decoded captures and writes the
// completion marker. After it returns nil, SQLite owns the bundled stores and
// a later Stage, fence or Commit leaves them alone. A commit whose pass finds
// a fenced file unfenced again fails with ErrFenceInvalidated and changes
// nothing; the caller stages and fences again. A pass that cannot read a
// bundled file for a transient reason fails with ErrLegacyUnread and changes
// nothing; the caller retries.
func (s *Store) Commit(ctx context.Context, opts CommitOptions) (ImportResult, error) {
	res := ImportResult{Status: map[string]int{}}
	err := s.write(ctx, durable, func(tx *sql.Tx) error {
		res = ImportResult{Status: map[string]int{}}
		state, attempt, err := importState(ctx, tx)
		if err != nil {
			return err
		}
		if state == importComplete {
			res.AlreadyComplete = true
			return nil
		}
		attempt++
		if res.Stage, err = s.stagePass(ctx, tx, attempt); err != nil {
			return err
		}
		if len(res.Stage.Unfenced) > 0 {
			return fmt.Errorf("%w: %s", ErrFenceInvalidated, strings.Join(res.Stage.Unfenced, ", "))
		}
		if len(res.Stage.Unread) > 0 {
			// Importing it as invalid would lose the record or setting for
			// good once the marker freezes legacy files.
			return fmt.Errorf("%w: %s", ErrLegacyUnread, strings.Join(res.Stage.Unread, ", "))
		}
		if err := s.hit("commit-staged"); err != nil {
			return err
		}
		for _, table := range keyedTables {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
				return err
			}
		}
		captures, err := loadCaptures(ctx, tx)
		if err != nil {
			return err
		}
		if err := checkValidators(captures, opts.Validate); err != nil {
			return err
		}
		for i := range captures {
			c := &captures[i]
			status, detail, err := importCapture(ctx, tx, *c, opts.Validate[c.Store])
			if err != nil {
				return fmt.Errorf("import %s: %w", c.Path, err)
			}
			c.ImportStatus, c.ImportDetail = status, detail
			res.Status[status]++
			if _, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET import_status = ?, import_detail = ? WHERE path = ?`, status, detail, c.Path); err != nil {
				return err
			}
		}
		if err := s.hit("commit-imported"); err != nil {
			return err
		}
		if opts.BeforeMarker != nil {
			if err := opts.BeforeMarker(captures); err != nil {
				return err
			}
		}
		if err := setImportState(ctx, tx, importComplete, attempt); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES('import_completed_at', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, s.opts.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
	if err != nil {
		return res, err
	}
	if !res.AlreadyComplete {
		if err := s.hit("commit-done"); err != nil {
			return res, err
		}
	}
	return res, nil
}

// errFuture marks a document written by a newer schema: it is kept in the
// capture journal for recovery and not imported.
type errFuture struct{ detail string }

func (e errFuture) Error() string { return e.detail }

func futuref(format string, args ...any) error { return errFuture{fmt.Sprintf(format, args...)} }

// errIgnored marks an input that is legitimately not imported (a cleared
// label, an unrecognised file name).
type errIgnored struct{ detail string }

func (e errIgnored) Error() string { return e.detail }

func checkValidators(captures []Capture, validate map[string]func(string, []byte) error) error {
	required := map[string]bool{}
	for _, store := range RequiredValidators {
		required[store] = true
	}
	for _, c := range captures {
		if c.State == CapturePresent && required[c.Store] && validate[c.Store] == nil {
			return fmt.Errorf("%w: %s (%s)", ErrValidatorMissing, c.Store, c.Path)
		}
	}
	return nil
}

func importCapture(ctx context.Context, tx *sql.Tx, c Capture, validate func(string, []byte) error) (status, detail string, err error) {
	switch c.State {
	case CaptureAbsent:
		return ImportAbsent, "", nil
	case CaptureUnreadable:
		if c.Store == StorePermission {
			// An existing unusable selection fails closed: keep it as an
			// invalid row, apart from omission.
			if _, err := tx.ExecContext(ctx, `INSERT INTO permissions(reply_id, state, error) VALUES(?, 'invalid', ?)`, c.ReplyID, c.ReadError); err != nil {
				return "", "", err
			}
		}
		return ImportInvalid, c.ReadError, nil
	}
	if c.Store == StoreTrackerHooksConfig {
		return ImportRetained, "", nil
	}
	// A document rejected halfway leaves none of its rows behind.
	if _, err := tx.ExecContext(ctx, `SAVEPOINT capture`); err != nil {
		return "", "", err
	}
	decodeErr := decodeCapture(ctx, tx, c)
	if decodeErr == nil && validate != nil {
		// After the decode, so a newer document still reads as future.
		if err := validate(c.ReplyID, c.Bytes); err != nil {
			decodeErr = invalidf("%v", err)
		}
	}
	if decodeErr != nil {
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO capture`); err != nil {
			return "", "", err
		}
	}
	if _, err := tx.ExecContext(ctx, `RELEASE capture`); err != nil {
		return "", "", err
	}
	var future errFuture
	var ignored errIgnored
	switch {
	case decodeErr == nil:
		return ImportImported, "", nil
	case errors.As(decodeErr, &future):
		return ImportFuture, future.detail, nil
	case errors.As(decodeErr, &ignored):
		return ImportIgnored, ignored.detail, nil
	}
	var invalid errInvalid
	if !errors.As(decodeErr, &invalid) {
		return "", "", decodeErr // a database error aborts the commit
	}
	if c.Store == StorePermission {
		if _, err := tx.ExecContext(ctx, `INSERT INTO permissions(reply_id, state, body_json, error) VALUES(?, 'invalid', ?, ?)`, c.ReplyID, c.Bytes, invalid.detail); err != nil {
			return "", "", err
		}
	}
	return ImportInvalid, invalid.detail, nil
}

// errInvalid marks a malformed legacy document; its bytes stay in the
// capture journal and it is not imported (except as a fail-closed
// permission row).
type errInvalid struct{ detail string }

func (e errInvalid) Error() string { return e.detail }

func invalidf(format string, args ...any) error { return errInvalid{fmt.Sprintf(format, args...)} }

func decodeCapture(ctx context.Context, tx *sql.Tx, c Capture) error {
	switch c.Store {
	case StoreAgentRecord:
		return importAgentRecord(ctx, tx, c)
	case StorePermission:
		return importPermission(ctx, tx, c)
	case StoreLabel:
		return importLabel(ctx, tx, c)
	case StoreAckCheck:
		return importAckCheck(ctx, tx, c)
	case StoreHiring:
		return importHiring(ctx, tx, c)
	case StoreSupport:
		return importSupport(ctx, tx, c)
	case StoreRailLayout:
		return importRailLayout(ctx, tx, c)
	case StoreUsageLatest:
		return importUsageLatest(ctx, tx, c)
	case StoreUsageLatestAgy:
		return importUsageLatestAgy(ctx, tx, c)
	case StoreUsageHistory, StoreUsageHistoryCodex, StoreUsageHistoryAgy:
		return importUsageHistory(ctx, tx, c)
	case StorePiProbe:
		return importPiProbe(ctx, tx, c)
	case StoreMemo:
		return importMemo(ctx, tx, c)
	case StoreTrackerHookState:
		return importTrackerHookState(ctx, tx, c)
	}
	return errIgnored{"unknown store " + c.Store}
}

// object decodes a JSON object's members.
func object(raw []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, invalidf("not a JSON object: %v", err)
	}
	if m == nil {
		return nil, invalidf("not a JSON object")
	}
	return m, nil
}

// member decodes m[key] into into when present and non-null.
func member(m map[string]json.RawMessage, key string, into any) (bool, error) {
	raw, ok := m[key]
	if !ok || isNull(raw) {
		return false, nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return true, invalidf("%s: %v", key, err)
	}
	return true, nil
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// extras returns m without the named members, as JSON, or nil when nothing
// remains.
func extras(m map[string]json.RawMessage, known ...string) ([]byte, error) {
	rest := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		rest[k] = v
	}
	for _, k := range known {
		delete(rest, k)
	}
	if len(rest) == 0 {
		return nil, nil
	}
	return json.Marshal(rest)
}

// schemaVersion reads m's version member: future when above max, invalid
// when present but not an integer, or when required and missing.
func schemaVersion(m map[string]json.RawMessage, key string, max int, required bool) (int, error) {
	var v int
	ok, err := member(m, key, &v)
	if err != nil {
		return 0, err
	}
	if !ok && required {
		return 0, invalidf("missing %s", key)
	}
	if v > max {
		return v, futuref("%s %d is newer than %d", key, v, max)
	}
	return v, nil
}

func importAgentRecord(ctx context.Context, tx *sql.Tx, c Capture) error {
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	if _, err := schemaVersion(m, "schema_version", 1, false); err != nil {
		return err
	}
	var replyID, key, root, holderID string
	var holderSeq uint64
	for _, f := range []struct {
		key  string
		into any
	}{{"reply_id", &replyID}, {"ws_session_key", &key}, {"root", &root}, {"holder_id", &holderID}, {"holder_seq", &holderSeq}} {
		if _, err := member(m, f.key, f.into); err != nil {
			return err
		}
	}
	// readRecord's completeness rule: the record names its own directory and
	// carries a key and a root. A record naming another reply-id is never
	// imported under either identity.
	if replyID != c.ReplyID || key == "" || root == "" {
		return invalidf("not a complete agent record for %s", c.ReplyID)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_records(reply_id, session_gen, snapshot_json) VALUES(?, ?, ?)`, c.ReplyID, c.SessionGen, c.Bytes); err != nil {
		return err
	}
	if holderID != "" && holderSeq > 0 && holderSeq <= math.MaxInt64 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_holder_cursors(reply_id, holder_id, holder_seq) VALUES(?, ?, ?)`, c.ReplyID, holderID, int64(holderSeq)); err != nil {
			return err
		}
	}
	return nil
}

// permissionLevels are wsagent's PermissionLevel values with nonzero rank
// (permission.go).
var permissionLevels = map[string]bool{"read-only": true, "workdir-write": true, "full": true}

func importPermission(ctx context.Context, tx *sql.Tx, c Capture) error {
	// readPermissionMembers / readPermissionSelectionState: an object with a
	// known permission_level and, when present, a boolean
	// delegated_authority. Anything else is an invalid existing selection.
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	var level string
	if ok, err := member(m, "permission_level", &level); err != nil || !ok || !permissionLevels[level] {
		return invalidf("invalid permission_level")
	}
	// Presence alone sets provenance; JSON null decodes as false there
	// (permission_store.go readPermissionSelectionState).
	delegatedSet, delegated := false, false
	if raw, ok := m["delegated_authority"]; ok {
		delegatedSet = true
		if err := json.Unmarshal(raw, &delegated); err != nil {
			return invalidf("invalid delegated_authority")
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO permissions(reply_id, state, level, delegated_set, delegated, body_json) VALUES(?, 'valid', ?, ?, ?, ?)`,
		c.ReplyID, level, delegatedSet, delegated, c.Bytes)
	return err
}

func importLabel(ctx context.Context, tx *sql.Tx, c Capture) error {
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	var raw string
	if _, err := member(m, "label", &raw); err != nil {
		return err
	}
	label, ok := cleanLabel(raw)
	if !ok {
		return invalidf("not a label: one line of at most %d characters", maxLabelRunes)
	}
	if label == "" {
		return errIgnored{"empty label"}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO labels(reply_id, label, body_json) VALUES(?, ?, ?)`, c.ReplyID, label, c.Bytes)
	return err
}

// maxLabelRunes and cleanLabel mirror wsdashboard's label rules
// (label_route.go cleanLabel): readSessionLabel trims the value and reads an
// invalid one as no label, so only a clean label gets a live row.
const maxLabelRunes = 64

func cleanLabel(raw string) (string, bool) {
	label := strings.TrimSpace(raw)
	if !utf8.ValidString(label) {
		return "", false
	}
	for _, r := range label {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return "", false
		}
	}
	return label, utf8.RuneCountInString(label) <= maxLabelRunes
}

func importAckCheck(ctx context.Context, tx *sql.Tx, c Capture) error {
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	var through, interrupted int64
	if _, err := member(m, "through", &through); err != nil {
		return err
	}
	if _, err := member(m, "interruptedAt", &interrupted); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ack_checks(reply_id, body_json) VALUES(?, ?)`, c.ReplyID, c.Bytes)
	return err
}

func importHiring(ctx context.Context, tx *sql.Tx, c Capture) error {
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	var enabled, hired bool
	var requestID string
	if _, err := member(m, "enabled", &enabled); err != nil {
		return err
	}
	if _, err := member(m, "hired", &hired); err != nil {
		return err
	}
	if _, err := member(m, "request_id", &requestID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO hiring(reply_id, enabled, hired, request_id, body_json) VALUES(?, ?, ?, ?, ?)`, c.ReplyID, enabled, hired, requestID, c.Bytes)
	return err
}

func importSupport(ctx context.Context, tx *sql.Tx, c Capture) error {
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	if _, err := schemaVersion(m, "schema_version", 1, false); err != nil {
		return err
	}
	stores := map[string]string{}
	if _, err := member(m, "stores", &stores); err != nil {
		return err
	}
	var last []byte
	if raw, ok := m["last"]; ok && !isNull(raw) {
		if _, err := object(raw); err != nil {
			return invalidf("last: not an object")
		}
		last = raw
	}
	extra, err := extras(m, "schema_version", "last", "stores")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO support_state(id, last_json, extra_json) VALUES(1, ?, ?)`, last, extra); err != nil {
		return err
	}
	for _, store := range sortedKeys(stores) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO support_mappings(store, conversation) VALUES(?, ?)`, store, stores[store]); err != nil {
			return err
		}
	}
	return nil
}

func importRailLayout(ctx context.Context, tx *sql.Tx, c Capture) error {
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	if _, err := schemaVersion(m, "schema_version", 1, false); err != nil {
		return err
	}
	order := []string{}
	if _, err := member(m, "workspace_order", &order); err != nil {
		return err
	}
	workspaces := map[string]json.RawMessage{}
	if _, err := member(m, "workspaces", &workspaces); err != nil {
		return err
	}
	orderJSON, err := json.Marshal(order)
	if err != nil {
		return err
	}
	extra, err := extras(m, "schema_version", "workspace_order", "workspaces")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rail_layout(id, workspace_order_json, extra_json) VALUES(1, ?, ?)`, orderJSON, extra); err != nil {
		return err
	}
	for _, ws := range sortedKeys(workspaces) {
		raw := workspaces[ws]
		if isNull(raw) {
			continue // readRailLayout drops a nil workspace
		}
		w, err := object(raw)
		if err != nil {
			return invalidf("workspace %q: not an object", ws)
		}
		// The members railLayoutWS decodes must keep their types.
		var list []string
		var kids map[string][]string
		for _, k := range []string{"worktree_order", "order", "stars"} {
			if _, err := member(w, k, &list); err != nil {
				return invalidf("workspace %q: %v", ws, err)
			}
		}
		if _, err := member(w, "children", &kids); err != nil {
			return invalidf("workspace %q: %v", ws, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO rail_layout_workspaces(workspace, body_json) VALUES(?, ?)`, ws, []byte(raw)); err != nil {
			return err
		}
	}
	return nil
}

// usageSources maps a latest-usage store to its source key.
var usageSources = map[string]string{
	StoreUsageLatest:    "plan-usage",
	StoreUsageLatestAgy: "plan-usage-agy",
}

func importUsageLatest(ctx context.Context, tx *sql.Tx, c Capture) error {
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	// planUsageStore decodes claude and gpt as entries; an undecodable one
	// discards the whole file there, so it is invalid here.
	for _, k := range []string{"claude", "gpt"} {
		if raw, ok := m[k]; ok && !isNull(raw) {
			if _, err := object(raw); err != nil {
				return invalidf("%s: not an object", k)
			}
		}
	}
	source := usageSources[c.Store]
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_latest_docs(source) VALUES(?)`, source); err != nil {
		return err
	}
	for i, k := range sortedKeys(m) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_latest(source, position, entry_key, body_json) VALUES(?, ?, ?, ?)`, source, i, k, []byte(m[k])); err != nil {
			return err
		}
	}
	return nil
}

func importUsageLatestAgy(ctx context.Context, tx *sql.Tx, c Capture) error {
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	if v, err := schemaVersion(m, "schema_version", 1, true); err != nil {
		return err
	} else if v != 1 {
		return invalidf("schema_version %d", v)
	}
	var groups []json.RawMessage
	if _, err := member(m, "groups", &groups); err != nil {
		return err
	}
	extra, err := extras(m, "schema_version", "groups")
	if err != nil {
		return err
	}
	source := usageSources[c.Store]
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_latest_docs(source, extra_json) VALUES(?, ?)`, source, extra); err != nil {
		return err
	}
	for i, raw := range groups {
		g, err := object(raw)
		if err != nil {
			return invalidf("groups[%d]: not an object", i)
		}
		var group string
		if _, err := member(g, "group", &group); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_latest(source, position, entry_key, body_json) VALUES(?, ?, ?, ?)`, source, i, group, []byte(raw)); err != nil {
			return err
		}
	}
	return nil
}

// usageHistories maps a history store to its key and the members every
// sample must carry (plan_usage_history.go and plan_usage_agy.go specs).
var usageHistories = map[string]struct {
	key      string
	required []string
}{
	StoreUsageHistory:      {"plan-usage-history", []string{"harness", "kind", "source", "resetsAt", "asOf", "percent"}},
	StoreUsageHistoryCodex: {"plan-usage-codex-history", []string{"harness", "kind", "source", "resetsAt", "asOf", "percent"}},
	StoreUsageHistoryAgy:   {"plan-usage-agy-history", []string{"harness", "group", "kind", "source", "resetsAt", "asOf", "percent"}},
}

// importUsageHistory keeps parsePlanUsageHistoryWith's all-or-nothing schema
// and error protection: version 1 exactly, a samples array, and every
// sample with its required members typed and non-null. The per-harness
// validity predicates stay with their owner (wsdashboard) at load time.
func importUsageHistory(ctx context.Context, tx *sql.Tx, c Capture) error {
	spec := usageHistories[c.Store]
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	if v, err := schemaVersion(m, "schema_version", 1, true); err != nil {
		return err
	} else if v != 1 {
		return invalidf("unsupported usage history")
	}
	var records []json.RawMessage
	if err := json.Unmarshal(m["samples"], &records); err != nil || records == nil {
		return invalidf("invalid usage history")
	}
	type sample struct {
		Harness  string  `json:"harness"`
		Group    string  `json:"group"`
		Kind     string  `json:"kind"`
		Source   string  `json:"source"`
		ResetsAt int64   `json:"resetsAt"`
		AsOf     int64   `json:"asOf"`
		Percent  float64 `json:"percent"`
	}
	samples := make([]sample, len(records))
	for i, raw := range records {
		fields, err := object(raw)
		if err != nil {
			return invalidf("invalid usage history sample")
		}
		for _, k := range spec.required {
			if v, ok := fields[k]; !ok || isNull(v) {
				return invalidf("incomplete usage history sample")
			}
		}
		if err := json.Unmarshal(raw, &samples[i]); err != nil {
			return invalidf("invalid usage history sample")
		}
	}
	extra, err := extras(m, "schema_version", "samples")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO usage_history_docs(history, extra_json) VALUES(?, ?)`, spec.key, extra); err != nil {
		return err
	}
	for i, v := range samples {
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_samples(history, seq, harness, grp, kind, source, resets_at, as_of, percent, body_json) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			spec.key, i, v.Harness, v.Group, v.Kind, v.Source, v.ResetsAt, v.AsOf, v.Percent, []byte(records[i])); err != nil {
			return err
		}
	}
	return nil
}

var piProbeKinds = map[string]bool{"models": true, "version": true}

// importPiProbe imports a legacy probe observation (1995): reusing it saves
// a Pi subprocess at the first post-cutover probe, and the owner still
// checks its identity stamps and freshness before trusting it, as the file
// cache does. An unknown name, invalid or future record is re-probed.
func importPiProbe(ctx context.Context, tx *sql.Tx, c Capture) error {
	name := strings.TrimSuffix(path.Base(c.Path), ".json")
	i := strings.LastIndexByte(name, '-')
	if i <= 0 || !piProbeKinds[name[i+1:]] {
		return errIgnored{"unrecognised probe file name"}
	}
	fingerprint, kind := name[:i], name[i+1:]
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	if v, err := schemaVersion(m, "schema_version", 1, true); err != nil {
		return err
	} else if v != 1 {
		return invalidf("schema_version %d", v)
	}
	var gotKind, observed string
	if _, err := member(m, "kind", &gotKind); err != nil {
		return err
	}
	if _, err := member(m, "observed_at", &observed); err != nil {
		return err
	}
	if gotKind != kind || observed == "" {
		return invalidf("probe kind or observation missing")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pi_probes(fingerprint, kind, observed_at, body_json) VALUES(?, ?, ?, ?)`, fingerprint, kind, observed, c.Bytes)
	return err
}

var memoHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// importMemo keeps the workspace hash as the key (1999): the legacy file name
// is the only place it is recorded, and the owner resolves it against the
// workspace list when it loads.
func importMemo(ctx context.Context, tx *sql.Tx, c Capture) error {
	hash := strings.TrimSuffix(path.Base(c.Path), ".json")
	if !memoHashPattern.MatchString(hash) {
		return errIgnored{"unrecognised memo file name"}
	}
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	v, err := schemaVersion(m, "schema_version", 1, true)
	if err != nil {
		return err
	}
	var items []json.RawMessage
	if _, err := member(m, "memos", &items); err != nil {
		return err
	}
	extra, err := extras(m, "schema_version", "memos")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memo_docs(workspace_hash, schema_version, extra_json) VALUES(?, ?, ?)`, hash, v, extra); err != nil {
		return err
	}
	for i, raw := range items {
		if isNull(raw) {
			// readMemoDoc keeps a null item and memos() skips it.
			if _, err := tx.ExecContext(ctx, `INSERT INTO memos(workspace_hash, position, memo_id, body_json) VALUES(?, ?, '', ?)`, hash, i, []byte("null")); err != nil {
				return err
			}
			continue
		}
		item, err := object(raw)
		if err != nil {
			return invalidf("memos[%d]: not an object", i)
		}
		var id string
		_, _ = member(item, "id", &id) // memoDoc.memos tolerates a mistyped id
		if _, err := tx.ExecContext(ctx, `INSERT INTO memos(workspace_hash, position, memo_id, body_json) VALUES(?, ?, ?, ?)`, hash, i, id, []byte(raw)); err != nil {
			return err
		}
	}
	return nil
}

func importTrackerHookState(ctx context.Context, tx *sql.Tx, c Capture) error {
	storeID := strings.TrimSuffix(path.Base(c.Path), ".json")
	if storeID == "" {
		return errIgnored{"empty store id"}
	}
	m, err := object(c.Bytes)
	if err != nil {
		return err
	}
	// loadState: version 1 and this registration's nonce; another version
	// starts over, so it is kept only in the journal.
	if v, err := schemaVersion(m, "v", 1, true); err != nil {
		return err
	} else if v != 1 {
		return invalidf("v %d", v)
	}
	var nonce string
	if _, err := member(m, "nonce", &nonce); err != nil {
		return err
	}
	processed := map[string]json.RawMessage{}
	if _, err := member(m, "processed", &processed); err != nil {
		return err
	}
	due := map[string]json.RawMessage{}
	if _, err := member(m, "due", &due); err != nil {
		return err
	}
	var cursor []byte
	if raw, ok := m["cursor"]; ok {
		cursor = raw
	}
	extra, err := extras(m, "v", "nonce", "cursor", "processed", "due")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tracker_hook_state(store_id, nonce, cursor_json, extra_json) VALUES(?, ?, ?, ?)`, storeID, nonce, cursor, extra); err != nil {
		return err
	}
	for _, id := range sortedKeys(processed) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tracker_hook_processed(store_id, nonce, event_id, body_json) VALUES(?, ?, ?, ?)`, storeID, nonce, id, []byte(processed[id])); err != nil {
			return err
		}
	}
	for _, id := range sortedKeys(due) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tracker_hook_due(store_id, nonce, item_id, at_json) VALUES(?, ?, ?, ?)`, storeID, nonce, id, []byte(due[id])); err != nil {
			return err
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
