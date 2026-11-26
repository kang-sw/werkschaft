package wsdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/kang-sw/devenv/internal/wsmeta"
)

// HiringMetadata is dashboard-owned metadata, separate from registrations and
// runner records: older typed writers of those records must not erase it.
// RequestID correlates a creation retry, not an authority or grouping link.
type HiringMetadata struct {
	Enabled   bool   `json:"enabled"`
	Hired     bool   `json:"hired,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

const hiringFileName = "hiring.json"

func ReadHiringMetadata(dir, replyID string) (HiringMetadata, error) {
	var m HiringMetadata
	raw, err := os.ReadFile(filepath.Join(SessionDir(dir, replyID), hiringFileName))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(raw, &m)
	return m, err
}

func writeHiringMetadata(dir, replyID string, m HiringMetadata) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writePrivateFile(SessionDir(dir, replyID), hiringFileName, append(raw, '\n'))
}

// MarkHired runs before the runner publishes a new lead record. No model-facing
// tool writes this marker; it survives detaching, handoff, and old row writers.
func MarkHired(dir, replyID, requestID, label string) error {
	if err := writeSessionLabel(SessionDir(dir, replyID), label); err != nil {
		return err
	}
	return writeHiringMetadata(dir, replyID, HiringMetadata{Hired: true, RequestID: requestID})
}

// loadHiringFromMeta seeds the in-memory hiring cache from the startup SQLite
// snapshot so hiring reads stay memory-only after cutover (2002).
func (d *Daemon) loadHiringFromMeta(snap *wsmeta.Snapshot) {
	d.hiringMu.Lock()
	defer d.hiringMu.Unlock()
	d.hiring = map[string]HiringMetadata{}
	if snap == nil {
		return
	}
	for replyID, h := range snap.Hiring {
		d.hiring[replyID] = HiringMetadata{
			Enabled:   h.Enabled,
			Hired:     h.Hired,
			RequestID: h.RequestID,
		}
	}
}

func (d *Daemon) readHiringMetadata(replyID string) (HiringMetadata, error) {
	if d.meta != nil {
		d.hiringMu.RLock()
		m := d.hiring[replyID]
		d.hiringMu.RUnlock()
		return m, nil
	}
	return ReadHiringMetadata(d.dir, replyID)
}

func (d *Daemon) writeHiringMetadata(replyID string, m HiringMetadata) error {
	if d.meta != nil {
		d.hiringMu.Lock()
		defer d.hiringMu.Unlock()
		if err := d.meta.PutHiring(context.Background(), replyID, wsmeta.Hiring{
			Enabled:   m.Enabled,
			Hired:     m.Hired,
			RequestID: m.RequestID,
		}); err != nil {
			return err
		}
		if d.hiring == nil {
			d.hiring = map[string]HiringMetadata{}
		}
		d.hiring[replyID] = m
		return nil
	}
	return writeHiringMetadata(d.dir, replyID, m)
}

// RecordHired mirrors a newly hired lead's SQLite label and hiring metadata
// (already committed atomically by wsagent via wsmeta.Store.MarkHired) into
// the daemon's in-memory caches. It acquires only the leaf mutexes d.labelMu
// and d.hiringMu separately — never d.hireMu — because approveHireLocked holds
// d.hireMu across CreateHireLead.
func (d *Daemon) RecordHired(replyID, requestID, label string) {
	if d == nil || d.meta == nil {
		return
	}
	clean, err := cleanLabel(label)
	if err == nil {
		d.labelMu.Lock()
		if d.labels == nil {
			d.labels = map[string]string{}
		}
		if clean == "" {
			delete(d.labels, replyID)
		} else {
			d.labels[replyID] = clean
		}
		d.labelMu.Unlock()
	}
	d.hiringMu.Lock()
	if d.hiring == nil {
		d.hiring = map[string]HiringMetadata{}
	}
	d.hiring[replyID] = HiringMetadata{Hired: true, RequestID: requestID}
	d.hiringMu.Unlock()
}

// deleteSessionMeta removes replyID's bundled session metadata rows from
// SQLite and drops the daemon's in-memory label, ack-check, and hiring caches.
func (d *Daemon) deleteSessionMeta(replyID string) error {
	if d == nil || d.meta == nil || !ValidReplyID(replyID) {
		return nil
	}
	if err := d.meta.DeleteSession(context.Background(), replyID); err != nil {
		return err
	}
	d.labelMu.Lock()
	delete(d.labels, replyID)
	d.labelMu.Unlock()
	d.inboxMu.Lock()
	delete(d.ackChecks, replyID)
	d.inboxMu.Unlock()
	d.hiringMu.Lock()
	delete(d.hiring, replyID)
	d.hiringMu.Unlock()
	return nil
}
