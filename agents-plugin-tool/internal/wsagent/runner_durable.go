package wsagent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kang-sw/devenv/internal/wsmeta"
)

func (r *Runner) durableProcess(backend string) bool {
	return r.opts.Backends[backend].ProcessKind == ProcessDurable
}

func (r *Runner) openDurableAgentRecords() map[string]Record {
	enabled := false
	for _, registration := range r.opts.Backends {
		if registration.ProcessKind == ProcessDurable {
			enabled = true
			break
		}
	}
	if !enabled {
		return nil
	}
	var db *wsmeta.Store
	if r.meta != nil {
		db = r.meta
	} else {
		var err error
		db, err = wsmeta.Open(r.opts.Dir, wsmeta.Options{
			Logf: func(format string, args ...any) {
				fmt.Fprintf(r.opts.Log, format+"\n", args...)
			},
		})
		if err != nil {
			fmt.Fprintf(r.opts.Log, "agent: open durable records: %v\n", err)
			return nil
		}
	}
	rows, err := db.DurableAgentRecords(context.Background())
	if err != nil {
		fmt.Fprintf(r.opts.Log, "agent: read durable records: %v\n", err)
		if db != r.meta {
			_ = db.Close()
		}
		return nil
	}
	r.durableRecords = db
	records := make(map[string]Record, len(rows))
	for _, row := range rows {
		if !r.durableProcess(row.Backend) {
			continue
		}
		rec, err := decodeRecord("durable agent record", row.ReplyID, row.JSON)
		if err != nil || rec.Backend != row.Backend {
			fmt.Fprintf(r.opts.Log, "agent %s: invalid durable record\n", row.ReplyID)
			continue
		}
		records[rec.ReplyID] = rec
	}
	return records
}

func (r *Runner) persistAgentRecord(dir string, rec Record) error {
	if !r.durableProcess(rec.Backend) {
		if r.meta != nil {
			return r.persistSQLiteRecord(dir, rec)
		}
		return writeRecord(dir, rec)
	}
	if r.durableRecords == nil {
		return errors.New("the durable agent record store is unavailable")
	}
	if rec.SchemaVersion == 0 {
		rec.SchemaVersion = recordSchemaVersion
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := r.durableRecords.PutDurableAgentRecord(context.Background(), wsmeta.DurableAgentRecord{
		ReplyID: rec.ReplyID, Backend: rec.Backend, JSON: raw,
	}); err != nil {
		return err
	}
	if r.meta == nil {
		r.publishDurableProjection(dir, durableHistoryProjection(rec))
	}
	return nil
}

// durableHistoryProjection lets a reader see settled history and identity.
// An open projection uses the existing handoff fence so older startup
// lists it without restoring a process or settling a turn.
func durableHistoryProjection(rec Record) Record {
	projected := Record{
		SchemaVersion: recordSchemaVersion,
		ReplyID:       rec.ReplyID, Profile: rec.Profile, Backend: rec.Backend,
		Root: rec.Root, Cwd: rec.Cwd, SessionFile: rec.SessionFile,
		SessionID: rec.SessionID, Model: rec.Model, Effort: rec.Effort,
		CreatedAt: rec.CreatedAt, CreatedUnixNano: rec.CreatedUnixNano, ClosedAt: rec.ClosedAt,
		WSKey: rec.WSKey, Lead: rec.Lead, Listed: rec.Listed,
		Parent: rec.Parent, Label: rec.Label, Tier: rec.Tier, SpawnerBackend: rec.SpawnerBackend,
		LastTurn: rec.LastTurn, LastReportedTurn: rec.LastReportedTurn, ActiveMs: rec.ActiveMs,
	}
	if rec.ClosedAt == "" {
		projected.HandedOffAt = rec.CreatedAt
		// Old or imported fixtures may have omitted creation time.
		if projected.HandedOffAt == "" {
			projected.HandedOffAt = "1970-01-01T00:00:00Z"
		}
	}
	return projected
}

func (r *Runner) restoreDurableProjection(dir string, rec Record) {
	projection := durableHistoryProjection(rec)
	if old, err := readRecord(dir); err == nil {
		r.durableProjections.Store(rec.ReplyID, projectionDigest(old))
	}
	r.publishDurableProjection(dir, projection)
}

func (r *Runner) publishDurableProjection(dir string, projection Record) {
	digest := projectionDigest(projection)
	if prior, ok := r.durableProjections.Load(projection.ReplyID); ok && prior == digest {
		return
	}
	// SQLite has committed. A projection write failure is retried after
	// the next record update or startup, without rolling back admission.
	if err := writeRecord(dir, projection); err != nil {
		fmt.Fprintf(r.opts.Log, "agent %s: write durable history projection: %v\n", projection.ReplyID, err)
		return
	}
	r.durableProjections.Store(projection.ReplyID, digest)
}

func projectionDigest(rec Record) [sha256.Size]byte {
	raw, _ := json.Marshal(rec)
	return sha256.Sum256(raw)
}

func (r *Runner) deleteDurableRecord(rec Record) {
	if !r.durableProcess(rec.Backend) || r.durableRecords == nil {
		return
	}
	r.durableProjections.Delete(rec.ReplyID)
	if err := r.durableRecords.DeleteDurableAgentRecord(context.Background(), rec.ReplyID); err != nil {
		fmt.Fprintf(r.opts.Log, "agent %s: delete durable record: %v\n", rec.ReplyID, err)
	}
}

// durableTurnInput is the prompt already admitted for the one active turn.
// The process uses the same ID in its input and result journal.
type durableTurnInput struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Operator bool   `json:"operator,omitempty"`
}

// commitDurableRecord makes a private transition visible only after its
// transaction commits. stage assigns fields or copies any slice it changes.
// after runs under mu after publication, for state read by waiting callers.
func (c *conv) commitDurableRecord(stage func(*Record), after func()) error {
	_, err := c.commitDurableRecordIf(nil, stage, after)
	return err
}

// commitDurableRecordIf is commitDurableRecord guarded by done in the same
// wmu+mu hold that persists and publishes the transition.
func (c *conv) commitDurableRecordIf(done func() bool, stage func(*Record), after func()) (bool, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if done != nil && !done() {
		return false, nil
	}
	next := c.rec
	stage(&next)
	if inject := c.r.opts.beforeDurableWrite; inject != nil {
		if err := inject(next); err != nil {
			return false, err
		}
	}
	if err := c.r.persistAgentRecord(c.dir, next); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: durable record write: %v\n", c.rec.ReplyID, err)
		return false, err
	}
	c.rec = next
	c.recordDirty = false
	if after != nil {
		after()
	}
	c.syncStateLocked()
	return true, nil
}
