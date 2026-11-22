package wsmeta

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// RecordDeltaBound is how many member deltas a conversation record keeps
// before the next patch folds them into its snapshot (1991): a restore reads
// one snapshot and at most this many deltas.
const RecordDeltaBound = 32

// ErrNoRecord reports a record operation on a conversation the store has no
// record for.
var ErrNoRecord = errors.New("wsmeta: no such agent record")

// ErrStaleHolder reports a holder cursor write from a holder that no longer
// holds the conversation (a late final ring line or heartbeat after a
// replacement attached). The write is refused and changes nothing.
var ErrStaleHolder = errors.New("wsmeta: holder no longer holds the conversation")

// requireImported refuses live writes until the import marker committed: a
// write before it would be discarded by the import's table replacement.
// s.mu is held.
func (s *Store) requireImported(ctx context.Context, q querier) error {
	if s.imported {
		return nil
	}
	state, _, err := importState(ctx, q)
	if err != nil {
		return err
	}
	if state != importComplete {
		return ErrNotImported
	}
	s.imported = true
	return nil
}

func validReplyID(id string) error {
	if !replyIDPattern.MatchString(id) {
		return fmt.Errorf("wsmeta: invalid reply-id %q", id)
	}
	return nil
}

// PutRecord replaces reply-id's whole record (creation, or a deliberate
// full rewrite) and drops its deltas. Its holder cursors stay: they belong
// to the holders, not to the snapshot.
func (s *Store) PutRecord(ctx context.Context, replyID, sessionGen string, record []byte) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	if _, err := object(record); err != nil {
		return fmt.Errorf("wsmeta: record for %s: %w", replyID, err)
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_records(reply_id, session_gen, snapshot_json, next_delta) VALUES(?, ?, ?, 1)
			ON CONFLICT(reply_id) DO UPDATE SET session_gen = excluded.session_gen, snapshot_json = excluded.snapshot_json, next_delta = 1`, replyID, sessionGen, record); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM agent_record_deltas WHERE reply_id = ?`, replyID)
		return err
	})
}

// PatchRecord appends one delta of top-level record members (a null value
// removes the member) instead of rewriting the record. The patch that
// reaches RecordDeltaBound folds every delta into the snapshot in the same
// transaction. An empty patch commits nothing.
func (s *Store) PatchRecord(ctx context.Context, replyID string, patch map[string]json.RawMessage) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	if len(patch) == 0 {
		return nil
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		var next int64
		if err := tx.QueryRowContext(ctx, `SELECT next_delta FROM agent_records WHERE reply_id = ?`, replyID).Scan(&next); errors.Is(err, sql.ErrNoRows) {
			return ErrNoRecord
		} else if err != nil {
			return err
		}
		if newHolder, ok := patch["holder_id"]; ok {
			// A holder change starts the new holder's ring at zero, as the
			// runner's attach does (runner_process.go: HolderID, HolderSeq =
			// id, 0): the old holder's sequence never becomes the new
			// holder's replay position, and no earlier cursor overlays it.
			snapshot, deltas, err := loadRecordParts(ctx, tx, replyID)
			if err != nil {
				return err
			}
			current, err := recordHolder(snapshot, deltas)
			if err != nil {
				return err
			}
			var nextHolder string
			if !isNull(newHolder) {
				_ = json.Unmarshal(newHolder, &nextHolder)
			}
			if nextHolder != current {
				if _, ok := patch["holder_seq"]; !ok {
					reset := make(map[string]json.RawMessage, len(patch)+1)
					for k, v := range patch {
						reset[k] = v
					}
					reset["holder_seq"] = json.RawMessage(`null`)
					if raw, err = json.Marshal(reset); err != nil {
						return err
					}
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM agent_holder_cursors WHERE reply_id = ?`, replyID); err != nil {
					return err
				}
			}
		}
		if next > RecordDeltaBound {
			return s.compactRecord(ctx, tx, replyID, raw)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_record_deltas(reply_id, seq, patch_json) VALUES(?, ?, ?)`, replyID, next, raw); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE agent_records SET next_delta = ? WHERE reply_id = ?`, next+1, replyID)
		return err
	})
}

// compactRecord folds the stored deltas and extra into the snapshot.
func (s *Store) compactRecord(ctx context.Context, tx *sql.Tx, replyID string, extra []byte) error {
	snapshot, deltas, err := loadRecordParts(ctx, tx, replyID)
	if err != nil {
		return err
	}
	folded, err := mergeRecord(snapshot, append(deltas, extra), nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_records SET snapshot_json = ?, next_delta = 1 WHERE reply_id = ?`, folded, replyID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM agent_record_deltas WHERE reply_id = ?`, replyID)
	return err
}

// SetHolderCursor records that the runner applied holderID's ring through
// seq for reply-id (1992). Only the record's current holder (its assembled
// holder_id) may write: a superseded holder's late write fails with
// ErrStaleHolder and leaves the current holder's cursor alone. The cursor
// only advances, and the cursors of earlier holders of that conversation are
// dropped: a sequence never carries over to another holder's ring. This is
// the sequence-only write that replaces the whole-record rewrite on a final
// ring line and a dirty heartbeat.
func (s *Store) SetHolderCursor(ctx context.Context, replyID, holderID string, seq uint64) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	if holderID == "" {
		return fmt.Errorf("wsmeta: empty holder id")
	}
	if seq > math.MaxInt64 {
		// SQLite integers are signed 64-bit; a larger value would compare
		// out of order.
		return fmt.Errorf("wsmeta: holder sequence %d exceeds %d", seq, int64(math.MaxInt64))
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		snapshot, deltas, err := loadRecordParts(ctx, tx, replyID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoRecord
		} else if err != nil {
			return err
		}
		current, err := recordHolder(snapshot, deltas)
		if err != nil {
			return err
		}
		if current != holderID {
			return fmt.Errorf("%w: %s holds %s, not %s", ErrStaleHolder, current, replyID, holderID)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_holder_cursors WHERE reply_id = ? AND holder_id != ?`, replyID, holderID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_holder_cursors(reply_id, holder_id, holder_seq) VALUES(?, ?, ?)
			ON CONFLICT(reply_id, holder_id) DO UPDATE SET holder_seq = excluded.holder_seq
			WHERE excluded.holder_seq > agent_holder_cursors.holder_seq`, replyID, holderID, int64(seq))
		return err
	})
}

// recordHolder returns the holder_id of the record snapshot and deltas
// assemble, scanning the deltas newest first.
func recordHolder(snapshot []byte, deltas [][]byte) (string, error) {
	for i := len(deltas) - 1; i >= 0; i-- {
		patch, err := object(deltas[i])
		if err != nil {
			return "", err
		}
		if v, ok := patch["holder_id"]; ok {
			if isNull(v) {
				return "", nil
			}
			// A non-string holder_id names no holder, as mergeRecord reads it.
			var h string
			_ = json.Unmarshal(v, &h)
			return h, nil
		}
	}
	m, err := object(snapshot)
	if err != nil {
		return "", err
	}
	var h string
	_, _ = member(m, "holder_id", &h)
	return h, nil
}

// DeleteRecord removes reply-id's record, deltas and cursors.
func (s *Store) DeleteRecord(ctx context.Context, replyID string) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM agent_records WHERE reply_id = ?`, replyID)
		return err
	})
}

// Record reads one assembled record (tests and diagnostics; the daemon reads
// every record once through Load).
func (s *Store) Record(ctx context.Context, replyID string) (AgentRecord, bool, error) {
	var rec AgentRecord
	var found bool
	err := s.read(ctx, func(q querier) error {
		if err := s.requireImported(ctx, q); err != nil {
			return err
		}
		var gen string
		if err := q.QueryRowContext(ctx, `SELECT session_gen FROM agent_records WHERE reply_id = ?`, replyID).Scan(&gen); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		snapshot, deltas, err := loadRecordParts(ctx, q, replyID)
		if err != nil {
			return err
		}
		cursors, err := loadCursors(ctx, q, replyID)
		if err != nil {
			return err
		}
		assembled, err := mergeRecord(snapshot, deltas, cursors)
		if err != nil {
			return err
		}
		rec, found = AgentRecord{ReplyID: replyID, SessionGen: gen, JSON: assembled, HolderCursors: cursors, Deltas: len(deltas)}, true
		return nil
	})
	return rec, found, err
}

func loadRecordParts(ctx context.Context, q querier, replyID string) ([]byte, [][]byte, error) {
	var snapshot []byte
	if err := q.QueryRowContext(ctx, `SELECT snapshot_json FROM agent_records WHERE reply_id = ?`, replyID).Scan(&snapshot); err != nil {
		return nil, nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT patch_json FROM agent_record_deltas WHERE reply_id = ? ORDER BY seq`, replyID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var deltas [][]byte
	for rows.Next() {
		var d []byte
		if err := rows.Scan(&d); err != nil {
			return nil, nil, err
		}
		deltas = append(deltas, d)
	}
	return snapshot, deltas, rows.Err()
}

func loadCursors(ctx context.Context, q querier, replyID string) (map[string]uint64, error) {
	rows, err := q.QueryContext(ctx, `SELECT holder_id, holder_seq FROM agent_holder_cursors WHERE reply_id = ?`, replyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uint64{}
	for rows.Next() {
		var h string
		var seq int64
		if err := rows.Scan(&h, &seq); err != nil {
			return nil, err
		}
		out[h] = uint64(seq)
	}
	return out, rows.Err()
}

// mergeRecord applies deltas to snapshot member-wise and, when cursors is
// non-nil, overlays holder_seq from the cursor of the record's own holder
// when that cursor is ahead. A cursor of another holder never applies.
func mergeRecord(snapshot []byte, deltas [][]byte, cursors map[string]uint64) ([]byte, error) {
	m, err := object(snapshot)
	if err != nil {
		return nil, err
	}
	for _, d := range deltas {
		patch, err := object(d)
		if err != nil {
			return nil, err
		}
		for k, v := range patch {
			if isNull(v) {
				delete(m, k)
			} else {
				m[k] = v
			}
		}
	}
	if cursors != nil {
		var holder string
		var seq uint64
		_, _ = member(m, "holder_id", &holder)
		_, _ = member(m, "holder_seq", &seq)
		if c, ok := cursors[holder]; ok && holder != "" && c > seq {
			m["holder_seq"], _ = json.Marshal(c)
		}
	}
	return json.Marshal(m)
}
