package wsmeta

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Live keyed write and delete methods for the bundled daemon metadata stores
// after legacy import completes. Every write guards on requireImported, commits
// at relaxed durability (2006), and modifies only the affected keyed rows.

// Usage store keys matching import.go's usageSources and usageHistories.
const (
	UsageSourcePlan     = "plan-usage"
	UsageSourceAgy      = "plan-usage-agy"
	UsageHistoryDefault = "plan-usage-history"
	UsageHistoryCodex   = "plan-usage-codex-history"
	UsageHistoryAgy     = "plan-usage-agy-history"
)

// DiffJSONObjects returns the top-level JSON member patch turning before into
// after (suitable for PatchRecord). Removed members map to JSON null.
func DiffJSONObjects(before, after []byte) (map[string]json.RawMessage, error) {
	prev, err := object(before)
	if err != nil {
		return nil, err
	}
	next, err := object(after)
	if err != nil {
		return nil, err
	}
	patch := map[string]json.RawMessage{}
	for k, v := range next {
		old, ok := prev[k]
		if !ok || !bytes.Equal(old, v) {
			patch[k] = v
		}
	}
	for k := range prev {
		if _, ok := next[k]; !ok {
			patch[k] = json.RawMessage(`null`)
		}
	}
	return patch, nil
}

// PutPermission upserts reply-id's explicit permission selection (1993),
// keeping presence-sensitive delegation provenance, unknown members in Body,
// and valid vs invalid state distinct from omission.
func (s *Store) PutPermission(ctx context.Context, replyID string, p Permission) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	switch p.State {
	case "valid":
		if !permissionLevels[p.Level] {
			return fmt.Errorf("wsmeta: invalid permission level %q", p.Level)
		}
		if _, err := object(p.Body); err != nil {
			return fmt.Errorf("wsmeta: permission body for %s: %w", replyID, err)
		}
	case "invalid":
	default:
		return fmt.Errorf("wsmeta: invalid permission state %q", p.State)
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO permissions(reply_id, state, level, delegated_set, delegated, body_json, error)
			VALUES(?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(reply_id) DO UPDATE SET state = excluded.state, level = excluded.level,
				delegated_set = excluded.delegated_set, delegated = excluded.delegated,
				body_json = excluded.body_json, error = excluded.error`,
			replyID, p.State, p.Level, p.DelegatedSet, p.Delegated, []byte(p.Body), p.Error)
		return err
	})
}

// DeletePermission removes reply-id's explicit permission selection.
func (s *Store) DeletePermission(ctx context.Context, replyID string) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM permissions WHERE reply_id = ?`, replyID)
		return err
	})
}

// SetSupportLast updates the machine-wide last support selection without
// touching store mappings or unknown top-level support members (1994).
func (s *Store) SetSupportLast(ctx context.Context, lastJSON []byte) error {
	if len(lastJSON) > 0 && !isNull(lastJSON) {
		if _, err := object(lastJSON); err != nil {
			return fmt.Errorf("wsmeta: support last: %w", err)
		}
	} else {
		lastJSON = nil
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO support_state(id, last_json) VALUES(1, ?)
			ON CONFLICT(id) DO UPDATE SET last_json = excluded.last_json`, lastJSON)
		return err
	})
}

// SetSupportMapping sets one store-to-conversation mapping without rewriting
// the last selection or other stores' mappings (1994).
func (s *Store) SetSupportMapping(ctx context.Context, store, conversation string) error {
	if store == "" || conversation == "" {
		return fmt.Errorf("wsmeta: support mapping requires store and conversation")
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO support_mappings(store, conversation) VALUES(?, ?)
			ON CONFLICT(store) DO UPDATE SET conversation = excluded.conversation`, store, conversation)
		return err
	})
}

// DeleteSupportMapping removes one store-to-conversation mapping (1994).
func (s *Store) DeleteSupportMapping(ctx context.Context, store string) error {
	if store == "" {
		return nil
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM support_mappings WHERE store = ?`, store)
		return err
	})
}

// RailLayoutPatch describes a workspace-scoped rail layout update (1996): only
// changed fields and changed workspaces are written.
type RailLayoutPatch struct {
	OrderChanged     bool
	WorkspaceOrder   []string
	ExtraChanged     bool
	Extra            json.RawMessage
	PutWorkspaces    map[string]json.RawMessage
	DeleteWorkspaces []string
}

// UpdateRailLayout applies patch atomically, writing only the touched document
// row and touched workspace rows. An empty patch commits nothing.
func (s *Store) UpdateRailLayout(ctx context.Context, patch RailLayoutPatch) error {
	if !patch.OrderChanged && !patch.ExtraChanged && len(patch.PutWorkspaces) == 0 && len(patch.DeleteWorkspaces) == 0 {
		return nil
	}
	var orderJSON []byte
	if patch.OrderChanged {
		order := patch.WorkspaceOrder
		if order == nil {
			order = []string{}
		}
		var err error
		if orderJSON, err = json.Marshal(order); err != nil {
			return err
		}
	}
	if patch.ExtraChanged && len(patch.Extra) > 0 && !isNull(patch.Extra) {
		if _, err := object(patch.Extra); err != nil {
			return fmt.Errorf("wsmeta: rail layout extra: %w", err)
		}
	}
	for ws, raw := range patch.PutWorkspaces {
		if _, err := object(raw); err != nil {
			return fmt.Errorf("wsmeta: rail layout workspace %q: %w", ws, err)
		}
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		// Ensure the document row exists so Load always populates Workspaces.
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO rail_layout(id, workspace_order_json) VALUES(1, '[]')`); err != nil {
			return err
		}
		if patch.OrderChanged {
			if _, err := tx.ExecContext(ctx, `UPDATE rail_layout SET workspace_order_json = ? WHERE id = 1`, orderJSON); err != nil {
				return err
			}
		}
		if patch.ExtraChanged {
			var extra []byte
			if len(patch.Extra) > 0 && !isNull(patch.Extra) {
				extra = patch.Extra
			}
			if _, err := tx.ExecContext(ctx, `UPDATE rail_layout SET extra_json = ? WHERE id = 1`, extra); err != nil {
				return err
			}
		}
		for _, ws := range sortedKeys(patch.PutWorkspaces) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO rail_layout_workspaces(workspace, body_json) VALUES(?, ?)
				ON CONFLICT(workspace) DO UPDATE SET body_json = excluded.body_json`, ws, []byte(patch.PutWorkspaces[ws])); err != nil {
				return err
			}
		}
		for _, ws := range patch.DeleteWorkspaces {
			if _, err := tx.ExecContext(ctx, `DELETE FROM rail_layout_workspaces WHERE workspace = ?`, ws); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutUsageLatestEntry updates one keyed latest-usage entry under source
// without rewriting sibling entries or document extras (1997).
func (s *Store) PutUsageLatestEntry(ctx context.Context, source, entryKey string, bodyJSON []byte) error {
	if source == "" || entryKey == "" {
		return fmt.Errorf("wsmeta: usage latest entry requires source and key")
	}
	if _, err := object(bodyJSON); err != nil {
		return fmt.Errorf("wsmeta: usage latest %s/%s: %w", source, entryKey, err)
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO usage_latest_docs(source) VALUES(?)`, source); err != nil {
			return err
		}
		var pos int
		err := tx.QueryRowContext(ctx, `SELECT position FROM usage_latest WHERE source = ? AND entry_key = ? ORDER BY position LIMIT 1`, source, entryKey).Scan(&pos)
		switch {
		case err == nil:
			_, err = tx.ExecContext(ctx, `UPDATE usage_latest SET body_json = ? WHERE source = ? AND position = ?`, bodyJSON, source, pos)
			return err
		case errors.Is(err, sql.ErrNoRows):
			var nextPos int
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position) + 1, 0) FROM usage_latest WHERE source = ?`, source).Scan(&nextPos); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO usage_latest(source, position, entry_key, body_json) VALUES(?, ?, ?, ?)`, source, nextPos, entryKey, bodyJSON)
			return err
		default:
			return err
		}
	})
}

// DeleteUsageLatestEntry removes one keyed latest-usage entry under source
// without rewriting sibling entries or document extras.
func (s *Store) DeleteUsageLatestEntry(ctx context.Context, source, entryKey string) error {
	if source == "" || entryKey == "" {
		return fmt.Errorf("wsmeta: usage latest entry requires source and key")
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM usage_latest WHERE source = ? AND entry_key = ?`, source, entryKey)
		return err
	})
}

// SetUsageLatestEntries replaces the ordered entries of one latest-usage
// source while keeping usage_latest_docs.extra_json and other sources intact
// (1997).
func (s *Store) SetUsageLatestEntries(ctx context.Context, source string, entries []UsageEntry) error {
	if source == "" {
		return fmt.Errorf("wsmeta: usage latest requires source")
	}
	for i, e := range entries {
		if _, err := object(e.Body); err != nil {
			return fmt.Errorf("wsmeta: usage latest %s[%d]: %w", source, i, err)
		}
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO usage_latest_docs(source) VALUES(?)`, source); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM usage_latest WHERE source = ?`, source); err != nil {
			return err
		}
		for i, e := range entries {
			if _, err := tx.ExecContext(ctx, `INSERT INTO usage_latest(source, position, entry_key, body_json) VALUES(?, ?, ?, ?)`, source, i, e.Key, []byte(e.Body)); err != nil {
				return err
			}
		}
		return nil
	})
}

// MutateUsageHistory inserts new samples, updates existing samples in place by
// Seq, and deletes pruned sample Seqs in one transaction without serializing
// untouched samples or altering usage_history_docs.extra_json (1998). It
// returns the assigned Seq for each sample in adds.
func (s *Store) MutateUsageHistory(ctx context.Context, history string, adds, updates []UsageSample, deleteSeqs []int64) ([]int64, error) {
	if history == "" {
		return nil, fmt.Errorf("wsmeta: empty usage history name")
	}
	if len(adds) == 0 && len(updates) == 0 && len(deleteSeqs) == 0 {
		return nil, nil
	}
	for i, a := range adds {
		if _, err := object(a.Body); err != nil {
			return nil, fmt.Errorf("wsmeta: usage sample add[%d]: %w", i, err)
		}
	}
	for i, u := range updates {
		if _, err := object(u.Body); err != nil {
			return nil, fmt.Errorf("wsmeta: usage sample update[%d]: %w", i, err)
		}
	}
	var addedSeqs []int64
	err := s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO usage_history_docs(history) VALUES(?)`, history); err != nil {
			return err
		}
		for _, seq := range deleteSeqs {
			if _, err := tx.ExecContext(ctx, `DELETE FROM usage_samples WHERE history = ? AND seq = ?`, history, seq); err != nil {
				return err
			}
		}
		for _, u := range updates {
			if _, err := tx.ExecContext(ctx, `UPDATE usage_samples
				SET harness = ?, grp = ?, kind = ?, source = ?, resets_at = ?, as_of = ?, percent = ?, body_json = ?
				WHERE history = ? AND seq = ?`,
				u.Harness, u.Group, u.Kind, u.Source, u.ResetsAt, u.AsOf, u.Percent, []byte(u.Body), history, u.Seq); err != nil {
				return err
			}
		}
		addedSeqs = nil
		if len(adds) > 0 {
			var nextSeq int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq) + 1, 0) FROM usage_samples WHERE history = ?`, history).Scan(&nextSeq); err != nil {
				return err
			}
			addedSeqs = make([]int64, len(adds))
			for i, a := range adds {
				seq := nextSeq
				nextSeq++
				if _, err := tx.ExecContext(ctx, `INSERT INTO usage_samples(history, seq, harness, grp, kind, source, resets_at, as_of, percent, body_json)
					VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					history, seq, a.Harness, a.Group, a.Kind, a.Source, a.ResetsAt, a.AsOf, a.Percent, []byte(a.Body)); err != nil {
					return err
				}
				addedSeqs[i] = seq
			}
		}
		return nil
	})
	return addedSeqs, err
}

// AddMemo prepends one memo item to workspaceHash without rewriting existing
// items (1999), returning its assigned stable Position.
func (s *Store) AddMemo(ctx context.Context, workspaceHash, workspace string, m Memo) (int, error) {
	if !memoHashPattern.MatchString(workspaceHash) {
		return 0, fmt.Errorf("wsmeta: invalid memo workspace hash %q", workspaceHash)
	}
	if _, err := object(m.Body); err != nil {
		return 0, fmt.Errorf("wsmeta: memo body: %w", err)
	}
	var pos int
	err := s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memo_docs(workspace_hash, workspace, schema_version) VALUES(?, ?, 1)
			ON CONFLICT(workspace_hash) DO UPDATE SET workspace = CASE WHEN excluded.workspace != '' THEN excluded.workspace ELSE memo_docs.workspace END`,
			workspaceHash, workspace); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MIN(position) - 1, 0) FROM memos WHERE workspace_hash = ?`, workspaceHash).Scan(&pos); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO memos(workspace_hash, position, memo_id, body_json) VALUES(?, ?, ?, ?)`,
			workspaceHash, pos, m.ID, []byte(m.Body))
		return err
	})
	return pos, err
}

// UpdateMemo updates one memo row in place without rewriting sibling memos
// or document extras (1999).
func (s *Store) UpdateMemo(ctx context.Context, workspaceHash string, position int, m Memo) error {
	if !memoHashPattern.MatchString(workspaceHash) {
		return fmt.Errorf("wsmeta: invalid memo workspace hash %q", workspaceHash)
	}
	if _, err := object(m.Body); err != nil {
		return fmt.Errorf("wsmeta: memo body: %w", err)
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE memos SET memo_id = ?, body_json = ? WHERE workspace_hash = ? AND position = ?`,
			m.ID, []byte(m.Body), workspaceHash, position)
		return err
	})
}

// DeleteMemo removes one memo row without rewriting sibling memos (1999).
func (s *Store) DeleteMemo(ctx context.Context, workspaceHash string, position int) error {
	if !memoHashPattern.MatchString(workspaceHash) {
		return fmt.Errorf("wsmeta: invalid memo workspace hash %q", workspaceHash)
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM memos WHERE workspace_hash = ? AND position = ?`, workspaceHash, position)
		return err
	})
}

// SetLabel sets reply-id's display label, or deletes the row when label is
// empty (2000).
func (s *Store) SetLabel(ctx context.Context, replyID, label string) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	clean, ok := cleanLabel(label)
	if !ok {
		return fmt.Errorf("wsmeta: invalid label for %s", replyID)
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		if clean == "" {
			_, err := tx.ExecContext(ctx, `DELETE FROM labels WHERE reply_id = ?`, replyID)
			return err
		}
		body, err := json.Marshal(struct {
			Label string `json:"label"`
		}{Label: clean})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO labels(reply_id, label, body_json) VALUES(?, ?, ?)
			ON CONFLICT(reply_id) DO UPDATE SET label = excluded.label, body_json = excluded.body_json`,
			replyID, clean, body)
		return err
	})
}

// DeleteLabel removes reply-id's display label row.
func (s *Store) DeleteLabel(ctx context.Context, replyID string) error {
	return s.SetLabel(ctx, replyID, "")
}

// PutAckCheck stores reply-id's answer-check marker (2001).
func (s *Store) PutAckCheck(ctx context.Context, replyID string, bodyJSON []byte) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	if _, err := object(bodyJSON); err != nil {
		return fmt.Errorf("wsmeta: ack check for %s: %w", replyID, err)
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO ack_checks(reply_id, body_json) VALUES(?, ?)
			ON CONFLICT(reply_id) DO UPDATE SET body_json = excluded.body_json`, replyID, bodyJSON)
		return err
	})
}

// DeleteAckCheck removes reply-id's answer-check marker.
func (s *Store) DeleteAckCheck(ctx context.Context, replyID string) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM ack_checks WHERE reply_id = ?`, replyID)
		return err
	})
}

// PutHiring stores reply-id's hiring metadata (2002).
func (s *Store) PutHiring(ctx context.Context, replyID string, h Hiring) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	body := h.Body
	if len(body) == 0 {
		var err error
		body, err = json.Marshal(struct {
			Enabled   bool   `json:"enabled"`
			Hired     bool   `json:"hired,omitempty"`
			RequestID string `json:"request_id,omitempty"`
		}{Enabled: h.Enabled, Hired: h.Hired, RequestID: h.RequestID})
		if err != nil {
			return err
		}
	} else if _, err := object(body); err != nil {
		return fmt.Errorf("wsmeta: hiring body for %s: %w", replyID, err)
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO hiring(reply_id, enabled, hired, request_id, body_json) VALUES(?, ?, ?, ?, ?)
			ON CONFLICT(reply_id) DO UPDATE SET enabled = excluded.enabled, hired = excluded.hired,
				request_id = excluded.request_id, body_json = excluded.body_json`,
			replyID, h.Enabled, h.Hired, h.RequestID, []byte(body))
		return err
	})
}

// DeleteHiring removes reply-id's hiring metadata.
func (s *Store) DeleteHiring(ctx context.Context, replyID string) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM hiring WHERE reply_id = ?`, replyID)
		return err
	})
}

// MarkHired coordinates label and hiring metadata for a newly hired lead in
// one transaction (2002).
func (s *Store) MarkHired(ctx context.Context, replyID, requestID, label string) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	clean, ok := cleanLabel(label)
	if !ok {
		return fmt.Errorf("wsmeta: invalid label for %s", replyID)
	}
	hiringBody, err := json.Marshal(struct {
		Enabled   bool   `json:"enabled"`
		Hired     bool   `json:"hired,omitempty"`
		RequestID string `json:"request_id,omitempty"`
	}{Enabled: false, Hired: true, RequestID: requestID})
	if err != nil {
		return err
	}
	var labelBody []byte
	if clean != "" {
		labelBody, err = json.Marshal(struct {
			Label string `json:"label"`
		}{Label: clean})
		if err != nil {
			return err
		}
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		if clean == "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM labels WHERE reply_id = ?`, replyID); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `INSERT INTO labels(reply_id, label, body_json) VALUES(?, ?, ?)
				ON CONFLICT(reply_id) DO UPDATE SET label = excluded.label, body_json = excluded.body_json`,
				replyID, clean, labelBody); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO hiring(reply_id, enabled, hired, request_id, body_json) VALUES(?, 0, 1, ?, ?)
			ON CONFLICT(reply_id) DO UPDATE SET enabled = 0, hired = 1, request_id = excluded.request_id, body_json = excluded.body_json`,
			replyID, requestID, hiringBody)
		return err
	})
}

// DeleteSession removes all session-keyed bundled metadata for replyID when a
// session is forgotten or pruned.
func (s *Store) DeleteSession(ctx context.Context, replyID string) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		for _, table := range []string{"agent_records", "permissions", "labels", "ack_checks", "hiring", "durable_agent_records"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE reply_id = ?`, replyID); err != nil {
				return err
			}
		}
		return nil
	})
}

// TrackerHookDelta describes one incremental update to a store registration's
// tracker hook progress (2004).
type TrackerHookDelta struct {
	ResetNonce      bool
	CursorChanged   bool
	Cursor          json.RawMessage
	ExtraChanged    bool
	Extra           json.RawMessage
	UpsertProcessed map[string]json.RawMessage
	DeleteProcessed []string
	UpsertDue       map[string]json.RawMessage
	DeleteDue       []string
}

// Empty reports whether delta changes nothing.
func (d TrackerHookDelta) Empty() bool {
	return !d.ResetNonce && !d.CursorChanged && !d.ExtraChanged &&
		len(d.UpsertProcessed) == 0 && len(d.DeleteProcessed) == 0 &&
		len(d.UpsertDue) == 0 && len(d.DeleteDue) == 0
}

// UpdateTrackerHook applies delta to (key.StoreID, key.Nonce) without
// rewriting untouched processed or due rows (2004). An empty delta commits
// nothing.
func (s *Store) UpdateTrackerHook(ctx context.Context, key TrackerHookKey, delta TrackerHookDelta) error {
	if key.StoreID == "" {
		return fmt.Errorf("wsmeta: tracker hook update requires store_id")
	}
	if delta.Empty() {
		return nil
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		if delta.ResetNonce {
			// Dropping other nonces cascades their processed and due rows.
			if _, err := tx.ExecContext(ctx, `DELETE FROM tracker_hook_state WHERE store_id = ? AND nonce != ?`, key.StoreID, key.Nonce); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tracker_hook_state(store_id, nonce) VALUES(?, ?)`, key.StoreID, key.Nonce); err != nil {
			return err
		}
		if delta.CursorChanged {
			if _, err := tx.ExecContext(ctx, `UPDATE tracker_hook_state SET cursor_json = ? WHERE store_id = ? AND nonce = ?`,
				[]byte(delta.Cursor), key.StoreID, key.Nonce); err != nil {
				return err
			}
		}
		if delta.ExtraChanged {
			var extra []byte
			if len(delta.Extra) > 0 && !isNull(delta.Extra) {
				extra = delta.Extra
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tracker_hook_state SET extra_json = ? WHERE store_id = ? AND nonce = ?`,
				extra, key.StoreID, key.Nonce); err != nil {
				return err
			}
		}
		for _, id := range delta.DeleteProcessed {
			if _, err := tx.ExecContext(ctx, `DELETE FROM tracker_hook_processed WHERE store_id = ? AND nonce = ? AND event_id = ?`,
				key.StoreID, key.Nonce, id); err != nil {
				return err
			}
		}
		for _, id := range sortedKeys(delta.UpsertProcessed) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO tracker_hook_processed(store_id, nonce, event_id, body_json) VALUES(?, ?, ?, ?)
				ON CONFLICT(store_id, nonce, event_id) DO UPDATE SET body_json = excluded.body_json`,
				key.StoreID, key.Nonce, id, []byte(delta.UpsertProcessed[id])); err != nil {
				return err
			}
		}
		for _, id := range delta.DeleteDue {
			if _, err := tx.ExecContext(ctx, `DELETE FROM tracker_hook_due WHERE store_id = ? AND nonce = ? AND item_id = ?`,
				key.StoreID, key.Nonce, id); err != nil {
				return err
			}
		}
		for _, id := range sortedKeys(delta.UpsertDue) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO tracker_hook_due(store_id, nonce, item_id, at_json) VALUES(?, ?, ?, ?)
				ON CONFLICT(store_id, nonce, item_id) DO UPDATE SET at_json = excluded.at_json`,
				key.StoreID, key.Nonce, id, []byte(delta.UpsertDue[id])); err != nil {
				return err
			}
		}
		return nil
	})
}
