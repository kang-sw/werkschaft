package wsmeta

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// DurableAgentRecord holds the authoritative state of a log-backed session.
// Its common history projection is stored independently.
type DurableAgentRecord struct {
	ReplyID string
	Backend string
	JSON    json.RawMessage
}

// PutDurableAgentRecord commits the private snapshot that an admission or
// settlement may depend on. Legacy import does not own these records.
func (s *Store) PutDurableAgentRecord(ctx context.Context, rec DurableAgentRecord) error {
	if err := validReplyID(rec.ReplyID); err != nil {
		return err
	}
	if rec.Backend == "" {
		return errors.New("wsmeta: durable agent record needs a backend")
	}
	if _, err := object(rec.JSON); err != nil {
		return fmt.Errorf("wsmeta: durable agent record for %s: %w", rec.ReplyID, err)
	}
	return s.write(ctx, durable, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO durable_agent_records(reply_id, backend, record_json)
			VALUES (?, ?, ?) ON CONFLICT(reply_id) DO UPDATE SET
			backend = excluded.backend, record_json = excluded.record_json
			WHERE backend != excluded.backend OR record_json != excluded.record_json`,
			rec.ReplyID, rec.Backend, []byte(rec.JSON))
		return err
	})
}

// DurableAgentRecords reads the private records once at daemon startup.
func (s *Store) DurableAgentRecords(ctx context.Context) ([]DurableAgentRecord, error) {
	var records []DurableAgentRecord
	err := s.read(ctx, func(q querier) error {
		rows, err := q.QueryContext(ctx, `SELECT reply_id, backend, record_json FROM durable_agent_records ORDER BY reply_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		records = nil
		for rows.Next() {
			var rec DurableAgentRecord
			if err := rows.Scan(&rec.ReplyID, &rec.Backend, &rec.JSON); err != nil {
				return err
			}
			records = append(records, rec)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

// DeleteDurableAgentRecord removes one private record when its conversation
// is pruned. Settled sessions keep their record until then.
func (s *Store) DeleteDurableAgentRecord(ctx context.Context, replyID string) error {
	if err := validReplyID(replyID); err != nil {
		return err
	}
	return s.write(ctx, durable, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM durable_agent_records WHERE reply_id = ?`, replyID)
		return err
	})
}
