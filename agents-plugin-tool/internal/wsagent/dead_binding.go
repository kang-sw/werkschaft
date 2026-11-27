package wsagent

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsgit"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// deadBindingRefusal is narrower than wake admission: a released checkout
// and the immutable agy spawner rule cannot be repaired by a later wake.
func (r *Runner) deadBindingRefusal(rec Record, checkout string) *admissionRefusal {
	if checkout != "" {
		return deadCheckoutRefusal(checkout)
	}
	// Record admission only checks the immutable, registered spawner rule.
	// Retryable permission and profile admission happen separately.
	refusal, _ := r.admitWorkerRecord(rec).(*admissionRefusal)
	return refusal
}

func stampClosed(rec *Record, now time.Time) {
	rec.ClosedAt = now.UTC().Format(time.RFC3339)
	rec.OpenGen++ // completion fence, as closeIf
	finishActiveTurn(rec, now.UnixMilli())
	rec.TurnOpen = false
}

type deadBindingClosure struct {
	Reason   string `json:"reason"`
	Checkout string `json:"checkout,omitempty"`
}

func closeDeadRecord(rec *Record, now time.Time, refusal *admissionRefusal) {
	stampClosed(rec, now)
	rec.DeadBindingClosure = &deadBindingClosure{Reason: refusal.reason, Checkout: refusal.checkout}
	if rec.DurableTurn != nil {
		rec.DurableClosedTurnID = rec.DurableTurn.ID
	}
	rec.DurableTurn, rec.DurableSteers = nil, nil
	rec.HeldTurn, rec.DurableHeldReportID = nil, ""
	rec.Queued, rec.Mail, rec.Reports, rec.DurableInboxClaims = nil, nil, nil, nil
	rec.Capability = ""
	rec.forgetMCP()
}

func (c *conv) closeDeadAfterRefusal(refusal *admissionRefusal) {
	if c.closed || c.crossHarnessRejoin && c.rec.HolderID != "" {
		return
	}
	if err := c.closeDeadBinding(refusal); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: %v\n", c.rec.ReplyID, err)
		return
	}
	if parent := c.rec.Parent; parent != nil && parent.Kind == ParentConversation {
		c.r.releaseLater(parent.ReplyID)
	}
}

// closeDeadBinding closes the record in the loop and ends a process held
// under a session holder (holder and child), so no idle process keeps its
// working directory inside the ended checkout. A release caller does its
// existing turn cancellation before this step. wmu excludes sends until the
// closed record is installed in memory.
//
// Order: the closed record is installed in memory first (sends refused,
// occupancy still lists the holder as live through rec.HolderID), then the
// holder is stopped (the stream's Close: input EOF, closeGrace, then the
// holder's terminate-and-kill), and only then is the closure persisted
// through persistAgentRecord, with the holder forgotten. The disk record no
// longer stays open to keep a predecessor daemon's orphan sweep protecting
// the holder: the holder is gone before the record closes, and a later
// removal of the checkout's admin directory (which takes the release marker
// with it) cannot reopen the conversation at the next start. A holder whose
// stop fails stays in the persisted closure and in the dead-binding
// occupancy, fenced from this daemon's orphan sweep; the next start's
// complete holder inventory stops it (finalizeDeadHolders).
//
// A conversation with no holder process persists before anything changes in
// memory, so a failed write leaves it open, as before. A direct (legacy
// daemon-child) process is left to finish its event stream normally.
func (c *conv) closeDeadBinding(refusal *admissionRefusal) error {
	c.wmu.Lock()
	rec := c.record()
	closeDeadRecord(&rec, c.r.opts.Now(), refusal)
	// The refusal is known before the record closes in memory, so mail
	// claimed while the holder stops is answered with it.
	c.r.mu.Lock()
	c.r.rememberDeadRefusalLocked(rec.ReplyID, refusal)
	c.r.mu.Unlock()
	c.mu.Lock()
	hp, _ := c.proc.(holderProcess)
	c.mu.Unlock()
	// A direct stream has the holderProcess methods too; only one under a
	// session holder reports one.
	held := false
	if hp != nil {
		_, _, held = hp.HolderInfo()
	}
	if !held && rec.HolderID == "" {
		if err := c.r.persistAgentRecord(c.dir, rec); err != nil {
			c.wmu.Unlock()
			return fmt.Errorf("close dead binding: %w", err)
		}
	}
	c.mu.Lock()
	proc, events, turn := c.proc, c.events, c.turn
	c.rec, c.closed, c.lastErr = rec, true, refusal.Error()
	c.proc, c.events, c.turn = nil, nil, false
	c.syncStateLocked()
	senders := c.deliverySenders
	c.deliverySenders = nil
	c.mu.Unlock()
	c.wmu.Unlock()

	if turn {
		c.live.apply(wslive.Event{Kind: wslive.KindTurnEnd})
	}
	persisted := !held && rec.HolderID == ""
	switch {
	case held:
		rec, persisted = c.stopDeadHolder(proc, hp, rec)
	case rec.HolderID != "":
		// A holder the record names with no attached stream: end it through
		// the manager, then persist the closure. One that cannot be ended
		// stays in the closure, fenced, for the next start's inventory.
		rec = c.stopUnattachedDeadHolder(rec)
		if err := c.r.persistAgentRecord(c.dir, rec); err != nil {
			fmt.Fprintf(c.r.opts.Log, "agent %s: close dead binding: %v\n", rec.ReplyID, err)
		} else {
			persisted = true
		}
	case events != nil:
		// A direct process can still publish events even after the runner
		// closes its conversation. Let its event producer finish normally.
		go c.drainClosedProcess(events)
	}
	c.r.rememberDeadBinding(rec, c.canonRoot, c.canonCwd, refusal, rec.HolderID != "" || (!held && events != nil))
	if persisted && rec.HolderID != "" {
		c.r.markDeadClosurePersisted(rec.ReplyID)
	}
	// The idle timer of a closed conversation has nothing left to stop.
	c.stopIdle()
	c.r.revokeToken(rec.ReplyID)
	c.tellHeldSenders(refusal.checkout, refusal.Error())
	dropped := c.dropHeld()
	droppedNames := make(map[string]bool, len(dropped))
	for _, entry := range dropped {
		droppedNames[entry.Name] = true
	}
	for send, pending := range senders {
		if send > c.voided && (send <= c.drained || droppedNames[pending.name]) {
			c.r.tellUndeliverable(pending.sender, rec.ReplyID, refusal)
		}
	}
	c.r.drainDeadMail(rec, refusal)
	c.r.discardDeadClaims(c.inbox, rec)
	c.live.setAdapter("")
	c.writeRegistration()
	c.signalSettled()
	st := c.memoryState()
	c.r.mu.Lock()
	delete(c.r.convs, rec.ReplyID)
	c.r.liveOwners.CompareAndDelete(rec.ReplyID, c)
	c.r.closed[rec.ReplyID] = rec
	if c.r.closedRows == nil {
		c.r.closedRows = map[string]State{}
	}
	c.r.closedRows[rec.ReplyID] = st
	c.r.mu.Unlock()
	c.watched()
	return nil
}

// stopDeadHolder ends a closed dead binding's held process: the stream's
// Close closes the child's input, waits closeGrace, then has the holder
// terminate and finally kill the child, so it is bounded. A holder that
// ended is forgotten in the record (and in memory); one whose stop failed
// is kept. Either way the closure is then persisted. It returns the record
// as written and whether the write succeeded.
func (c *conv) stopDeadHolder(proc Process, hp holderProcess, rec Record) (Record, bool) {
	_ = proc.Close()
	if err := hp.StopError(); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: end released holder %s: %v\n", rec.ReplyID, rec.HolderID, err)
	} else {
		rec = c.forgetDeadHolder(rec)
	}
	if err := c.r.persistAgentRecord(c.dir, rec); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: close dead binding: %v\n", rec.ReplyID, err)
		return rec, false
	}
	return rec, true
}

// stopUnattachedDeadHolder ends the holder a closed dead binding's record
// names when no stream of it is attached. Before this daemon's first
// complete holder scan an unknown holder is no proof that it is gone, so it
// is kept.
func (c *conv) stopUnattachedDeadHolder(rec Record) Record {
	hosts := c.r.opts.Holders
	if hosts == nil {
		return rec
	}
	err := hosts.Stop(rec.HolderID)
	gone := errors.Is(err, wsholder.ErrUnknownHolder) && !errors.Is(err, wsholder.ErrUnknownHolderIncomplete) && c.r.scanDone()
	if err != nil && !gone {
		fmt.Fprintf(c.r.opts.Log, "agent %s: end released holder %s: %v\n", rec.ReplyID, rec.HolderID, err)
		return rec
	}
	return c.forgetDeadHolder(rec)
}

// forgetDeadHolder clears an ended holder from a closed record and from the
// conversation's copy, as releaseHolder does for a stopped process.
func (c *conv) forgetDeadHolder(rec Record) Record {
	forget := func(rec *Record) {
		rec.HolderID, rec.HostPID, rec.HolderSeq = "", 0, 0
		if !rec.Lead {
			rec.HandedOffAt = ""
		}
	}
	forget(&rec)
	c.setStatus(func() { forget(&c.rec) })
	return rec
}

// markDeadClosurePersisted notes that a holder-retaining dead binding's
// closure is on disk, so persistRetainedReleaseClosures need not write it.
func (r *Runner) markDeadClosurePersisted(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if binding, ok := r.deadBindings[id]; ok {
		binding.closurePersisted = true
		r.deadBindings[id] = binding
	}
}

func (c *conv) drainClosedProcess(events <-chan Event) {
	id := c.rec.ReplyID
	for {
		select {
		case _, ok := <-events:
			if !ok {
				c.r.mu.Lock()
				delete(c.r.deadBindings, id)
				c.r.mu.Unlock()
				return
			}
		case <-c.r.quit:
			return
		}
	}
}

type deadBindingOccupancy struct {
	rec                 Record
	root, cwd, checkout string
	closurePersisted    bool
	holderExited        bool
}

// DrainDeadBindingMail handles mail queued across or after an automatic
// close. The mailbox claims atomically, even if the close drains alongside it.
func (r *Runner) DrainDeadBindingMail(id string) {
	r.mu.Lock()
	refusal := r.deadRefusals[id]
	c, rec := r.convs[id], r.closed[id]
	r.mu.Unlock()
	if refusal == nil {
		return
	}
	if c != nil {
		rec = c.record()
	}
	if rec.ReplyID != "" {
		r.drainDeadMail(rec, refusal)
	}
}

func (r *Runner) rememberDeadBinding(rec Record, root, cwd string, refusal *admissionRefusal, process bool) {
	if rec.HolderID != "" {
		r.holders.reject(rec.HolderID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rememberDeadRefusalLocked(rec.ReplyID, refusal)
	if rec.HolderID == "" && !process {
		return
	}
	r.rememberDeadOccupancyLocked(deadBindingOccupancy{rec: rec, root: root, cwd: cwd, checkout: refusal.checkout})
}

// restoreRetainedReleaseOccupancy re-registers a detached holder's binding
// when the daemon stopped between persisting the closure and removing the
// checkout. The normal holder inventory determines whether it is still live.
func (r *Runner) restoreRetainedReleaseOccupancy(rec Record) {
	closure := rec.DeadBindingClosure
	if rec.ClosedAt == "" || rec.HolderID == "" || closure == nil || closure.Reason == "" || closure.Checkout == "" {
		return
	}
	info, err := os.Stat(closure.Checkout)
	if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
		return
	}
	root, cwd := canonicalBinding(rec)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rememberDeadOccupancyLocked(deadBindingOccupancy{
		rec: rec, root: root, cwd: cwd, checkout: closure.Checkout, closurePersisted: true,
	})
}

func (r *Runner) rememberDeadOccupancyLocked(binding deadBindingOccupancy) {
	if r.deadBindings == nil {
		r.deadBindings = make(map[string]deadBindingOccupancy)
	}
	r.deadBindings[binding.rec.ReplyID] = binding
}

func (r *Runner) rememberDeadRefusalLocked(id string, refusal *admissionRefusal) {
	if r.deadRefusals == nil {
		r.deadRefusals = make(map[string]*admissionRefusal)
	}
	r.deadRefusals[id] = refusal
}

func (r *Runner) boundDeadProcesses(target string, release checkoutRelease) []retiredPiBinding {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.deadBindings) == 0 {
		return nil
	}
	var complete bool
	alive := map[string]bool{}
	if r.opts.Holders != nil {
		scanned := r.scanDone()
		listed := r.opts.Holders.List()
		complete = scanned && !listed.Incomplete
		for _, info := range listed.Holders {
			alive[info.ID] = info.Child == nil || info.Child.State != "exited"
		}
	}
	var out []retiredPiBinding
	for id, binding := range r.deadBindings {
		rec := binding.rec
		if holder := rec.HolderID; holder != "" {
			live, known := alive[holder]
			if known && !live && !binding.holderExited {
				// The manager can drop this holder while another holder
				// keeps the census incomplete. Keep the exit with the binding
				// while its closed record may still need to be persisted.
				binding.holderExited = true
				r.deadBindings[id] = binding
			}
			if binding.holderExited || !live && complete {
				if _, retained := r.closed[id]; !retained && r.convs[id] == nil {
					delete(r.deadBindings, id)
				}
				continue
			}
		}
		// A retained release closure names the checkout the holder used
		// even if a symlink in its recorded cwd now resolves elsewhere.
		releasedHere := sameWorktreePath(binding.checkout, target)
		if !releasedHere && !worktreePathContains(target, binding.root) && !worktreePathContains(target, binding.cwd) {
			continue
		}
		before := releaseBound(rec) && (releasedHere ||
			release.ok && wsgit.BoundBeforeRelease(rec.CreatedAt, rec.CreatedUnixNano, release.at))
		out = append(out, retiredPiBinding{createdAt: rec.CreatedAt, child: BoundWorktreeChild{
			Address: AgentAddress(id), State: AgentClosed, Cwd: rec.Cwd, BoundBeforeRelease: before, Live: true,
			Holder: rec.HolderID, PID: rec.HostPID,
		}})
	}
	return out
}

func (r *Runner) drainDeadMail(rec Record, refusal *admissionRefusal) {
	if r.opts.Mail == nil || rec.Parent == nil {
		return
	}
	mail, err := r.opts.Mail.Drain(rec.Root, rec.WSKey, func(sender string) string {
		return r.mailRelation(rec, sender)
	})
	if err != nil {
		fmt.Fprintf(r.opts.Log, "agent %s: mailbox: %v\n", rec.ReplyID, err)
	}
	for _, m := range mail {
		r.tellUndeliverable(m.Sender, rec.ReplyID, refusal)
	}
}

func (r *Runner) tellUndeliverable(from, id string, refusal *admissionRefusal) {
	if from != "" && refusal.checkout == "" && r.opts.WakeUndeliverable != nil {
		r.opts.WakeUndeliverable(from, id, refusal.Error())
		return
	}
	r.tellRefused(from, id, refusal.checkout, refusal.Error())
}

func (r *Runner) discardDeadClaims(inbox string, rec Record) {
	if r.durableProcess(rec.Backend) {
		if err := wsinbox.DiscardClaims(inbox); err != nil {
			fmt.Fprintf(r.opts.Log, "agent %s: discard closed inbox claims: %v\n", rec.ReplyID, err)
		}
	}
}

func (r *Runner) closeDeadAtStart(rec Record, refusal *admissionRefusal, binding resolvedAgentBinding) {
	now := r.opts.Now()
	closeDeadRecord(&rec, now, refusal)
	r.rememberDeadBinding(rec, binding.root, binding.cwd, refusal, false)
	r.loadClosed(rec)
	dir := wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID)
	var persistErr error
	if rec.HolderID == "" {
		persistErr = r.persistAgentRecord(dir, rec)
	}
	if persistErr != nil {
		// Still fence this record in memory. The next startup can retry the
		// write from the same permanent evidence without rejoining its holder.
		fmt.Fprintf(r.opts.Log, "agent %s: close dead binding: %v\n", rec.ReplyID, persistErr)
	} else {
		r.drainDeadMail(rec, refusal)
		inbox := wsinbox.Dir(dir)
		if held, err := wsinbox.Held(inbox); err == nil {
			for _, entry := range held {
				_, _ = wsinbox.Cancel(inbox, entry.Name)
			}
		}
		_, _ = wsinbox.DrainInterrupt(inbox)
		r.discardDeadClaims(inbox, rec)
	}
	if err := wsdashboard.WriteRegistration(r.opts.Dir, registration(rec, now)); err != nil {
		fmt.Fprintf(r.opts.Log, "agent %s: registration write: %v\n", rec.ReplyID, err)
	}
	r.notify(rec.ReplyID, rec.Root)
}

// persistRetainedReleaseClosures makes a holder-backed release durable
// before deleting its marker. The ended rows that can be purged do not need
// an extra record write.
func (r *Runner) persistRetainedReleaseClosures(worktree string, ended map[string]bool) error {
	r.deadClosureMu.Lock()
	defer r.deadClosureMu.Unlock()
	var retained []Record
	r.mu.Lock()
	for id, binding := range r.deadBindings {
		rec := binding.rec
		if rec.HolderID == "" || binding.closurePersisted || !worktreePathContains(worktree, binding.checkout) {
			continue
		}
		if ended[id] && !r.hasLiveDescendantsLocked(safeStopParent(id, rec.Lead), nil) {
			continue
		}
		retained = append(retained, rec)
	}
	r.mu.Unlock()
	for _, rec := range retained {
		id := rec.ReplyID
		if err := r.persistAgentRecord(wsdashboard.SessionDir(r.opts.Dir, id), rec); err != nil {
			return fmt.Errorf("session %s: persist released checkout closure: %w", id, err)
		}
		r.mu.Lock()
		binding := r.deadBindings[id]
		binding.closurePersisted = true
		r.deadBindings[id] = binding
		r.mu.Unlock()
	}
	return nil
}

// deadHolderRecords lists the dead bindings whose record still names a
// holder, for finalizeDeadHolders.
func (r *Runner) deadHolderRecords() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	var records []Record
	for _, binding := range r.deadBindings {
		if rec := binding.rec; rec.HolderID != "" {
			records = append(records, rec)
		}
	}
	return records
}

// finalizeDeadHolders runs on a complete startup inventory. An attached or
// incomplete inventory leaves the predecessor's open record recoverable.
//
// A holder still present is ended first: its conversation is closed, so the
// holder and child only keep a working directory inside an ended checkout
// (an older daemon's release detached them and never stopped them). One
// whose stop fails stays fenced in the closure for the next start.
func (r *Runner) finalizeDeadHolders(records []Record, listed []wsholder.Info) {
	r.deadClosureMu.Lock()
	defer r.deadClosureMu.Unlock()
	present := make(map[string]bool, len(listed))
	for _, info := range listed {
		present[info.ID] = true
	}
	for _, rec := range records {
		if present[rec.HolderID] {
			if err := r.opts.Holders.Stop(rec.HolderID); err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
				fmt.Fprintf(r.opts.Log, "agent %s: end closed conversation's holder %s: %v\n", rec.ReplyID, rec.HolderID, err)
				continue
			}
			fmt.Fprintf(r.opts.Log, "agent %s: ended closed conversation's holder %s\n", rec.ReplyID, rec.HolderID)
		}
		rec.HolderID, rec.HostPID, rec.HolderSeq = "", 0, 0
		if err := r.persistAgentRecord(wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID), rec); err != nil {
			fmt.Fprintf(r.opts.Log, "agent %s: close dead binding: %v\n", rec.ReplyID, err)
			continue
		}
		r.mu.Lock()
		delete(r.deadBindings, rec.ReplyID)
		if _, ok := r.closed[rec.ReplyID]; ok {
			r.closed[rec.ReplyID] = rec
		}
		r.mu.Unlock()
	}
}
