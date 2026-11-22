package wsagent

import (
	"errors"
	"fmt"
	"time"

	"github.com/kang-sw/devenv/internal/wsholder"
)

// Legacy Pi holders retain their descriptor and unfenced record across rejoin.
// Every newly launched worker includes the backend in the common descriptor.
func workerHolderMatches(rec Record, meta holderSession, child *wsholder.ChildStatus) bool {
	kind := meta.Kind == holderKindAgentWorker && meta.Backend == rec.Backend ||
		meta.Kind == holderKindPiWorker && rec.Backend == BackendPi && (meta.Backend == "" || meta.Backend == BackendPi)
	return kind && meta.ReplyID == rec.ReplyID && meta.Cwd == rec.workDir() &&
		meta.Capability == capabilityHash(rec.Capability) &&
		child != nil && rec.HostPID > 0 && child.PID == rec.HostPID
}

// Restore only after the holder identity, including its harness PID, matches.
// An absent or stale HTTP route waits for a safe idle restart, just as leads do.
func (c *conv) restoreWorkerMCP() {
	rec := c.record()
	if !c.r.opts.Backends[rec.Backend].WorkerHTTPMCP {
		return
	}
	if rec.MCPToken == "" || rec.MCPHolder != rec.HolderID || rec.MCPURL != c.r.mcpURL() || rec.MCPEnv == nil {
		c.mcpStale = true
		return
	}
	session := c.r.attachMCP(rec.ReplyID, rec.MCPToken, rec.MCPEnv, rec.workDir(), rec.HostPID, rec.Backend)
	if session != nil {
		session.Started(c.workerMCPEnv, rec.HostPID)
		c.workerMCPEnv = nil
	}
}

// retireLeadOnlyWorker ends the held process of a worker record on a
// lead-only backend. checkReattach calls it only after the holder passed the
// ownership match and the rejected-holder guard; a mismatched holder is never
// stopped. Replay, events, and the MCP bearer were never opened.
//   - The record stays listable, stopped, with the lead-only reason as its
//     error.
//   - An open turn settles failed with that reason, as a process gone at
//     rejoin does (processGone).
//   - A failed stop keeps the holder in the record and the detached
//     association, publishes nothing, and says so. The conversation stays
//     replaying (it submits nothing), and each heartbeat's checkReattach
//     tries the stop again (leadOnlyRetiring), as a downgrade retirement
//     does. Any stopProcess (a user's Stop) retries it here too, never
//     through the detached stream's Close.
func (c *conv) retireLeadOnlyWorker(hp holderProcess) {
	refusal := c.r.admitWorkerRecord(c.rec)
	if refusal == nil {
		return
	}
	reason := refusal.Error()
	id := c.rec.HolderID
	if !c.leadOnlyRetiring {
		hp.Detach()
		c.events = nil
	}
	if err := c.r.opts.Holders.Stop(id); err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
		c.leadOnlyRetiring = true
		// The next beat asks again; replaying keeps checkReattach running.
		c.replayAsked = false
		msg := fmt.Sprintf("%s; the session's previous process could not be ended (holder %s): %v", reason, id, err)
		if msg != c.lastErr {
			c.setStatus(func() { c.lastErr = msg })
			fmt.Fprintf(c.r.opts.Log, "agent %s: end lead-only worker holder %s: %v\n", c.rec.ReplyID, id, err)
			c.writeRegistration()
		}
		return
	}
	c.leadOnlyRetiring = false
	c.replaying, c.replayAsked = false, false
	// A failed attempt's stop error no longer applies.
	c.setStatus(func() { c.proc, c.lastErr = nil, reason })
	c.r.revokeToken(c.rec.ReplyID)
	// The holder and the credentials it carried go in one write. A daemon
	// that dies right after it leaves an open turn the next load settles
	// with this reason (interrupted).
	c.update(func(rec *Record) {
		rec.HolderID, rec.HostPID, rec.HolderSeq = "", 0, 0
		rec.HandedOffAt = ""
		rec.Capability = ""
		rec.forgetMCP()
	})
	// processEnded settles an open turn failed with the reason the rejoin
	// set; leadOnlyRetired then writes the one registration.
	c.processEnded()
	c.leadOnlyRetired()
}

// leadOnlyRetired closes a lead-only rejoin on whichever path ended it:
//   - the holder was ended (retireLeadOnlyWorker);
//   - it was gone (processGone);
//   - it failed the ownership match;
//   - a downgrade retired it.
//
// The record shows the refusal and gets a fresh activity stamp, so its child
// expiry runs one full interval with the reason visible, not at once from
// an old stamp. It does nothing while a retirement is still retrying
// (workerRetiring).
func (c *conv) leadOnlyRetired() {
	if !c.leadOnlyRejoin || c.workerRetiring {
		return
	}
	refusal := c.r.admitWorkerRecord(c.rec)
	if refusal == nil {
		return
	}
	c.leadOnlyRejoin = false
	c.setStatus(func() { c.lastErr, c.wakeRefused = refusal.Error(), true })
	c.update(func(rec *Record) { rec.LastActiveAt = c.r.opts.Now().UTC().Format(time.RFC3339) })
	c.writeRegistration()
}

// A parent's activity stamp denotes settlement only without any later input.
// Strict ordering deliberately adopts equal and same-second timestamps.
func downgradeSettledChild(child, parent Record) bool {
	if !child.heldWorker() || !child.TurnOpen || child.TurnStartedAt == 0 ||
		child.Parent == nil || child.Parent.Kind != ParentConversation ||
		parent.TurnOpen || parent.HeldTurn != nil || parent.LastTurn == nil ||
		parent.Sends > parent.LastTurn.Mark || len(parent.Mail) != 0 || len(parent.Queued) != 0 {
		return false
	}
	switch parent.LastTurn.Result.Status {
	case TurnDone, TurnFailed, TurnInterrupted:
	default:
		return false
	}
	settled, err := time.Parse(time.RFC3339, parent.LastActiveAt)
	return err == nil && time.UnixMilli(child.TurnStartedAt).Before(settled)
}

// This runs after ownership validation and before requesting replay. Retiring
// the downgrade-fenced turn must not publish a second report to its parent.
func (c *conv) retireDowngradedWorker(hp holderProcess) bool {
	rec := c.record()
	if !c.workerRetiring {
		if rec.Parent == nil || rec.Parent.Kind != ParentConversation {
			return false
		}
		parent := c.r.conv(rec.Parent.ReplyID)
		if parent == nil || !downgradeSettledChild(rec, parent.record()) {
			return false
		}
		c.workerRetiring = true
		hp.Detach()
		c.events = nil
	}
	// Keep the detached association until Stop succeeds. A failed Stop may
	// already have removed the holder, so retry before the ordinary list/gone
	// path, which would publish a second terminal result for this turn.
	if err := c.r.opts.Holders.Stop(rec.HolderID); err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
		c.setStatus(func() { c.lastErr = fmt.Sprintf("retire downgraded worker: %v", err) })
		return true
	}
	c.setStatus(func() { c.proc, c.lastErr = nil, "" })
	c.r.revokeToken(rec.ReplyID)
	c.update(func(rec *Record) {
		finishActiveTurn(rec, c.r.opts.Now().UnixMilli())
		rec.TurnOpen = false
		rec.HandedOffAt = ""
		rec.HolderID, rec.HostPID, rec.HolderSeq = "", 0, 0
		rec.Capability = ""
		rec.forgetMCP()
	})
	c.setStatus(func() { c.turn = false })
	c.replaying, c.replayAsked = false, false
	c.workerRetiring = false
	c.seqDirty, c.live.ringDirty = false, false
	c.writeRegistration()
	// Inbox signals consumed while retirement was retrying could not kick.
	// Recheck the retained input now that this child may launch a new turn.
	c.signalInbox()
	return true
}
