package wsagent

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

var errDurableStopping = conflict("durable sessions are stopping for Full Quit")
var errDurableClose = errors.New("the durable session could not be marked closed")
var errDurableUnavailable = errors.New("the durable session record is unavailable")

// admitDurableTransition orders creation, process start and turn submission
// against Full Quit. The fence drains admitted steps before returning.
func (r *Runner) admitDurableTransition() (func(), error) {
	if r.durableStopping.Load() {
		return nil, errDurableStopping
	}
	r.durableAdmissionMu.RLock()
	if r.durableStopping.Load() {
		r.durableAdmissionMu.RUnlock()
		return nil, errDurableStopping
	}
	return r.durableAdmissionMu.RUnlock, nil
}

// FenceDurableStarts blocks new and waking durable sessions. A partial
// Full Quit resumes any wake that encountered the fence.
func (r *Runner) FenceDurableStarts() func() {
	r.durableStopping.Store(true)
	r.durableAdmissionMu.Lock()
	r.durableAdmissionMu.Unlock()
	return func() {
		r.durableStopping.Store(false)
		for _, session := range r.DurableSessions() {
			if !session.Unavailable {
				r.InboxChanged(session.ID)
			}
		}
	}
}

// DurableSessions lists open durable sessions, including sleeping ones
// and live projections whose private record is currently unavailable.
func (r *Runner) DurableSessions() []wsdashboard.DurableSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	var sessions []wsdashboard.DurableSession
	for id, c := range r.convs {
		c.mu.Lock()
		live := !c.closed && r.durableProcess(c.rec.Backend)
		c.mu.Unlock()
		if live {
			sessions = append(sessions, wsdashboard.DurableSession{ID: id})
		}
	}
	for id, rec := range r.closed {
		if r.convs[id] == nil && r.durableProcess(rec.Backend) && rec.ClosedAt == "" && rec.handedOff() {
			sessions = append(sessions, wsdashboard.DurableSession{ID: id, Unavailable: true})
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	return sessions
}

// StopDurableSession closes an editable durable lifetime through the same
// descendant and exec fences as an explicit conversation close.
func (r *Runner) StopDurableSession(id string) error {
	rec, ok := r.recordOf(id)
	if !ok || !r.durableProcess(rec.Backend) || rec.ClosedAt != "" {
		return wsdashboard.ErrUnknownDurableSession
	}
	if rec.handedOff() {
		return errDurableUnavailable
	}
	_, err := r.CloseConversation(id)
	if err == errClosed || err == errUnknown {
		return wsdashboard.ErrUnknownDurableSession
	}
	return err
}

func (c *conv) closeDurable() error {
	now := c.r.opts.Now()
	err := c.commitDurableRecord(func(rec *Record) {
		rec.ClosedAt = now.UTC().Format(time.RFC3339)
		rec.OpenGen++
		finishActiveTurn(rec, now.UnixMilli())
		rec.TurnOpen, rec.DurableTurn, rec.DurableSteers = false, nil, nil
		rec.HeldTurn, rec.DurableHeldReportID = nil, ""
		rec.Queued, rec.Mail, rec.Reports, rec.DurableInboxClaims = nil, nil, nil, nil
		rec.Capability = ""
		rec.forgetMCP()
	}, func() {
		c.closed = true
	})
	if err != nil {
		return fmt.Errorf("%w: %w", errDurableClose, err)
	}
	c.finishClose()
	return nil
}
