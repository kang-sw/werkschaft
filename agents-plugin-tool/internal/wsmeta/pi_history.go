package wsmeta

import (
	"context"
	"database/sql"
)

// RetiredPiPurges loads committed worktree history deletions at startup.
func (s *Store) RetiredPiPurges(ctx context.Context) (map[string]string, error) {
	var pending map[string]string
	err := s.read(ctx, func(q querier) error {
		rows, err := q.QueryContext(ctx, `SELECT reply_id, worktree FROM retired_pi_purges`)
		if err != nil {
			return err
		}
		defer rows.Close()
		pending = make(map[string]string)
		for rows.Next() {
			var id, worktree string
			if err := rows.Scan(&id, &worktree); err != nil {
				return err
			}
			pending[id] = worktree
		}
		return rows.Err()
	})
	return pending, err
}

// BeginRetiredPiPurge commits the deletion before any archived bytes are removed.
func (s *Store) BeginRetiredPiPurge(ctx context.Context, id, worktree string) error {
	if err := validReplyID(id); err != nil {
		return err
	}
	return s.write(ctx, durable, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO retired_pi_purges(reply_id, worktree) VALUES (?, ?) ON CONFLICT(reply_id) DO NOTHING`, id, worktree)
		return err
	})
}

// FinishRetiredPiPurge drops the state once the session and archive are gone.
func (s *Store) FinishRetiredPiPurge(ctx context.Context, id string) error {
	return s.write(ctx, durable, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM retired_pi_purges WHERE reply_id = ?`, id)
		return err
	})
}
