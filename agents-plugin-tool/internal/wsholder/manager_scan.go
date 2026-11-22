package wsholder

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Scanning the holders directory and attaching to holders: scan, cleanup, attach, and event delivery.

// Scan reads the holders directory and attaches to every holder this
// daemon is not attached to. A descriptor whose process is gone is removed
// with its directory, as is a directory left without a descriptor. So is one
// whose PID now belongs to another process: its endpoint does not answer and
// the process under the PID did not start when the descriptor says.
func (m *Manager) Scan() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.scan()
}

func (m *Manager) scan() {
	if m.isClosed() {
		return
	}
	incomplete := false
	dirs, err := os.ReadDir(m.opts.Dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.logf("scan: %v", err)
			incomplete = true
		}
		dirs = nil
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		id := d.Name()
		if !d.IsDir() || !ValidID(id) {
			continue
		}
		dir := filepath.Join(m.opts.Dir, id)
		desc, err := ReadDescriptor(dir)
		if err != nil {
			info, statErr := os.Stat(dir)
			// A directory gone meanwhile is a holder that exited.
			removed := errors.Is(statErr, os.ErrNotExist)
			// Only a directory with no descriptor at all is an orphan to
			// clear once aged; one whose descriptor exists but does not read
			// or is invalid may still belong to a live holder, so it stays
			// and keeps the listing incomplete.
			missing := errors.Is(err, os.ErrNotExist)
			if statErr == nil && missing && time.Since(info.ModTime()) > orphanDirGrace {
				removed = os.RemoveAll(dir) == nil
			}
			if !removed {
				// A holder may run here whose registration did not read.
				incomplete = true
			}
			continue
		}
		seen[id] = true
		m.mu.Lock()
		e := m.entries[id]
		attached := e != nil && e.client != nil && e.desc.PID == desc.PID && e.desc.Port == desc.Port
		m.mu.Unlock()
		if attached {
			continue
		}
		if !m.opts.alive(desc.PID) {
			m.logf("holder %s (pid %d) is gone; removing its descriptor", id, desc.PID)
			if m.cleanup(desc, "unknown") == nil {
				seen[id] = false
			}
			continue
		}
		// A holder that died without removing its directory (a reboot, a
		// kill) leaves a descriptor whose PID the OS hands to another
		// process. Nothing could ever stop that entry, since the guarded
		// kill rightly refuses the stranger, so it is cleared here. Both
		// signs are required: a start mismatch alone is not proof on an OS
		// matched by wall clock, and a live holder answers its endpoint.
		if err := m.attach(desc); err != nil && attachCause(err) == CauseNoAnswer && !errors.Is(err, ErrDetached) &&
			errors.Is(m.sameProcess(desc), ErrStartTimeMismatch) {
			m.logf("holder %s is gone and pid %d belongs to another process; removing its descriptor", id, desc.PID)
			if m.cleanup(desc, "unknown") == nil {
				seen[id] = false
			}
		}
	}
	// A holder whose directory vanished stays while attached (it rewrites
	// its descriptor); an unattached one is forgotten.
	m.mu.Lock()
	for id, e := range m.entries {
		if !seen[id] && e.client == nil {
			delete(m.entries, id)
		}
	}
	m.incomplete = incomplete
	m.mu.Unlock()
}

// cleanup persists an available exit before removing the holder directory.
// Old or abruptly killed holders may have no exit file.
func (m *Manager) cleanup(desc Descriptor, fallback string) error {
	dir := filepath.Join(m.opts.Dir, desc.ID)
	raw, err := os.ReadFile(filepath.Join(dir, "exit.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logf("holder %s: read exit: %v", desc.ID, err)
		return err
	}
	exit := Exit{Reason: fallback, StoppedAt: time.Now().UTC().Format(time.RFC3339)}
	if err == nil {
		if err := json.Unmarshal(raw, &exit); err != nil {
			m.logf("holder %s: invalid exit: %v", desc.ID, err)
			return err
		}
	}
	if m.opts.OnExit != nil {
		if err := m.opts.OnExit(desc, exit); err != nil {
			m.logf("holder %s: persist exit: %v", desc.ID, err)
			return err
		}
	}
	code, signal := "unknown", "none"
	if exit.ExitCode != nil {
		code = strconv.Itoa(*exit.ExitCode)
	}
	if exit.Signal != nil {
		signal = strconv.Itoa(*exit.Signal)
	}
	m.logf("stopped holder %s (pid %d) at %s: exit %s, signal %s, reason %s", desc.ID, desc.PID, exit.StoppedAt, code, signal, exit.Reason)
	if err := os.RemoveAll(dir); err != nil {
		m.logf("holder %s: remove directory: %v", desc.ID, err)
		return err
	}
	m.forget(desc.ID)
	return nil
}

func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *Manager) isStopping() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopping
}

func (m *Manager) forget(id string) {
	m.mu.Lock()
	e := m.entries[id]
	delete(m.entries, id)
	m.mu.Unlock()
	if e != nil && e.client != nil {
		e.client.Close()
	}
}

// attachCause is why a failed attach left the holder unattached.
func attachCause(err error) Cause {
	var attachErr *AttachError
	if errors.As(err, &attachErr) {
		return attachErr.Cause
	}
	return CauseNoAnswer
}

// attach attaches to the holder desc names and starts its record stream,
// after the last sequence this process saw from it. The outcome lands in
// the holder's entry.
func (m *Manager) attach(desc Descriptor) error {
	m.mu.Lock()
	e := m.entries[desc.ID]
	if e == nil {
		e = &entry{}
		m.entries[desc.ID] = e
	}
	if e.client != nil {
		old := e.client
		e.client = nil
		defer old.Close()
	}
	e.desc = desc
	from := uint64(0)
	if e.lastSeq > 0 {
		from = e.lastSeq + 1
	}
	m.mu.Unlock()

	id := desc.ID
	ctx, cancel := context.WithTimeout(context.Background(), attachDialTimeout+2*attachStepTimeout)
	defer cancel()
	audit := 0
	if m.opts.AuditChannel {
		audit = AuditVersion
	}
	client, info, err := AttachWith(ctx, desc, AttachOptions{Audit: audit}, func(ev Event) { m.event(id, ev) })
	if err != nil {
		cause := attachCause(err)
		m.mu.Lock()
		changed := e.cause != cause
		e.cause = cause
		m.mu.Unlock()
		if changed {
			m.logf("holder %s (pid %d) is unattached: %v", id, desc.PID, err)
		}
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		client.Close()
		return ErrDetached
	}
	e.client, e.cause, e.child = client, "", info.Child
	e.audit = Event{}
	if info.Audit > 0 {
		e.audit = Event{Event: EventAuditAttached, Next: info.NextSeq, Conn: info.Conn}
	}
	m.mu.Unlock()
	m.logf("attached to holder %s (pid %d, protocol %d)", id, desc.PID, info.Protocol)
	go func() {
		<-client.Done()
		m.mu.Lock()
		if e.client == client {
			e.client, e.cause, e.audit = nil, CauseNoAnswer, Event{}
		}
		m.mu.Unlock()
	}()
	if _, _, err := client.Replay(from); err != nil {
		client.Close()
		return err
	}
	return nil
}

// event records what an attached holder sent and passes it to the sink.
func (m *Manager) event(id string, ev Event) {
	var newlyLost uint64
	m.mu.Lock()
	if e := m.entries[id]; e != nil {
		switch ev.Event {
		case "output":
			if ev.Seq > e.lastSeq {
				e.lastSeq = ev.Seq
			}
		case "exit":
			if ev.Seq > e.lastSeq {
				e.lastSeq = ev.Seq
			}
			e.child.State, e.child.ExitCode, e.child.Signal = "exited", ev.ExitCode, ev.Signal
		case "gap":
			// Only records this process never received are lost: a
			// replay a consumer asked for from an earlier sequence
			// (Client.Replay) reports again the ones long gone.
			from := ev.From
			if from <= e.lastSeq {
				from = e.lastSeq + 1
			}
			if ev.To > from {
				newlyLost = ev.To - from
				e.lost += newlyLost
			}
			if ev.To > 0 && ev.To-1 > e.lastSeq {
				e.lastSeq = ev.To - 1
			}
		}
	}
	sinks := m.sinks
	m.mu.Unlock()
	if newlyLost > 0 {
		m.logf("holder %s lost %d records before %d: they left its ring before the daemon received them", id, newlyLost, ev.To)
	}
	for _, sink := range sinks {
		sink.HolderEvent(id, ev)
	}
}

// AuditAttach returns the EventAuditAttached of holder id's current
// connection when it negotiated the audit channel. A consumer that starts
// receiving a holder's events after the attach (the client delivered that
// event before the consumer was listed) reads it here; the event may also
// reach it, so its handling must be idempotent per Conn.
func (m *Manager) AuditAttach(id string) (Event, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.entries[id]; e != nil && e.client != nil && e.audit.Event != "" {
		return e.audit, true
	}
	return Event{}, false
}
