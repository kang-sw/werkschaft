package wsdashboard

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsauditstore"
)

// auditDirName is the audit store root under the daemon state directory. It
// sits beside, not inside, the session registry, so the registry's session
// directory prune does not cut a longer audit retention short (Decision 6).
const auditDirName = "audit"

// auditState is the daemon's audit ring store and the lifecycle bookkeeping
// that feeds it last-seen times at existing write points only: a session's
// first sighting in this daemon run (daemon start from surviving
// registrations, or a new registration) and the index dropping it (prune or
// unregister). Heartbeats write nothing; the retention sweep asks the index
// before removing a ring.
type auditState struct {
	store *wsauditstore.Store
	mu    sync.Mutex
	seen  map[string]bool
}

// openAudit opens the audit store. A failure leaves audit off with a log
// line; the dashboard runs either way.
func (d *Daemon) openAudit(logw io.Writer) {
	limits := wsauditstore.DefaultLimits()
	if d.config != nil && d.config.LoadAuditLimits != nil {
		loaded, warnings, err := d.config.LoadAuditLimits()
		if err != nil {
			fmt.Fprintf(logw, "dashboard: audit settings: %v; using defaults\n", err)
		} else {
			limits = loaded
		}
		for _, w := range warnings {
			fmt.Fprintf(logw, "dashboard: audit settings: %s\n", w)
		}
	}
	store, err := wsauditstore.Open(wsauditstore.Options{
		Root:     filepath.Join(d.dir, auditDirName),
		Limits:   limits,
		Log:      logw,
		LastSeen: d.auditRegistryLastSeen,
		// The index is empty until its first refresh (poll); sweeping before
		// it would ignore the heartbeats of quiet live sessions.
		DeferFirstSweep: d.index != nil,
	})
	if err != nil {
		fmt.Fprintf(logw, "dashboard: audit store unavailable: %v\n", err)
		return
	}
	d.audit = &auditState{store: store, seen: map[string]bool{}}
}

// auditFirstSweep runs the retention sweep deferred at open, once the index
// holds the registry. It never blocks.
func (d *Daemon) auditFirstSweep() {
	if d.audit != nil {
		d.audit.store.RequestSweep()
	}
}

// AuditSink is the daemon's audit store for capture, or nil when it is
// unavailable.
func (d *Daemon) AuditSink() wsaudit.SourcedSink {
	if d.audit == nil {
		return nil
	}
	return d.audit.store
}

// AuditStore is the daemon's audit store for readers, or nil.
func (d *Daemon) AuditStore() *wsauditstore.Store {
	if d.audit == nil {
		return nil
	}
	return d.audit.store
}

func (d *Daemon) auditRegistryLastSeen(session string) (time.Time, bool) {
	if d.index == nil {
		return time.Time{}, false
	}
	s, ok := d.index.Session(session)
	if !ok {
		return time.Time{}, false
	}
	t := s.Registration.LastSeen()
	return t, !t.IsZero()
}

// auditSessionSeen records a session's registry last-seen time the first
// time this daemon run sees it. Rings stay unloaded until active capture or
// an explicit recovery query needs them.
func (d *Daemon) auditSessionSeen(reg Registration) {
	if d.audit == nil || reg.ReplyID == "" {
		return
	}
	d.audit.mu.Lock()
	first := !d.audit.seen[reg.ReplyID]
	d.audit.seen[reg.ReplyID] = true
	d.audit.mu.Unlock()
	if first {
		d.audit.store.Touch(reg.ReplyID, reg.LastSeen())
	}
}

// auditSessionRemoved preserves a session's final last-seen time before the
// registry's copy is gone. It never blocks.
func (d *Daemon) auditSessionRemoved(replyID string, last Registration) {
	if d.audit == nil {
		return
	}
	d.audit.mu.Lock()
	delete(d.audit.seen, replyID)
	d.audit.mu.Unlock()
	d.audit.store.Touch(replyID, last.LastSeen())
}

// auditDeleteSession removes a deleted session's audit ring (Decision 8).
func (d *Daemon) auditDeleteSession(replyID string) {
	if d.audit == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.audit.store.DeleteSession(ctx, replyID); err != nil {
		fmt.Fprintf(d.log, "dashboard: remove audit ring of %s: %v\n", replyID, err)
	}
}

func (d *Daemon) closeAudit() {
	if d.audit != nil {
		_ = d.audit.store.Close()
	}
}
