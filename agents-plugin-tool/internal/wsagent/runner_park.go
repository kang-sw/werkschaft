package wsagent

import (
	"fmt"
	"sort"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// runner_park.go keeps waiting spawned children out of memory.
//
// A child that finished its turn and holds no process is "waiting" for up to
// DefaultChildWaitingDays, and a parent may wake it with a message at any
// point of that window. Holding a conversation loop, its mail watch and its
// 30 s ticker for every one of them cost thousands of file operations a
// second on a daemon with hundreds of historical children. A waiting child
// is therefore parked: its loop ends, its record stays in r.closed (so every
// read of a record sees it) with r.parked set, and its agent.json and
// registration.json stay on disk without ended_at. A mail to its address, a
// send, or any control that needs its loop rehydrates it (unpark) from the
// record, which is the same load a daemon start performs.

// ParkedMailWatcher is the optional LeadMail extension a parked child's mail
// wake needs: WatchParked calls changed once when the mailbox of key holds
// mail (and at registration, when it already does), until stop is called. It
// holds no goroutine of its own per child and writes no file. A LeadMail
// that does not implement it keeps its children resident.
type ParkedMailWatcher interface {
	WatchParked(root, key string, changed func()) (stop func())
}

// canParkLocked reports whether the runner can wake a parked child.
func (r *Runner) canPark() bool {
	if r.opts.Mail == nil {
		return true
	}
	_, ok := r.opts.Mail.(ParkedMailWatcher)
	return ok
}

// recordParkable reports whether a record's child is quiet enough to hold no
// loop: spawned, open, no process or holder, no turn, no input, mail, report
// or compaction in flight. It reads the record alone.
func (r *Runner) recordParkable(rec Record) bool {
	if rec.Lead || rec.Parent == nil || rec.ClosedAt != "" || rec.handedOff() || rec.HolderID != "" ||
		rec.TurnOpen || rec.HeldTurn != nil || rec.DurableTurn != nil || rec.PendingCompaction != nil || rec.QuotaContinuation != nil {
		return false
	}
	if r.durableProcess(rec.Backend) {
		return false
	}
	mark := rec.Voided
	if rec.LastTurn != nil {
		mark = max(mark, rec.LastTurn.Mark)
	}
	return rec.Sends <= mark && len(rec.Queued)+len(rec.Mail)+len(rec.Reports)+len(rec.Steered)+len(rec.PendingReports) == 0
}

// coldParkable picks the open records a daemon start lists parked instead of
// serving: quiet children (recordParkable) that no resident record needs
// above them: a parent stays resident while any of its children does.
func (r *Runner) coldParkable(open []Record, dead map[string]*admissionRefusal, orphans map[string]bool) map[string]bool {
	if !r.parkEnabled() || !r.canPark() {
		return nil
	}
	cold := map[string]bool{}
	for _, rec := range open {
		if dead[rec.ReplyID] == nil && !orphans[rec.ReplyID] && r.recordParkable(rec) {
			cold[rec.ReplyID] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, rec := range open {
			if cold[rec.ReplyID] || rec.Parent == nil || rec.Parent.Kind != ParentConversation {
				continue
			}
			if cold[rec.Parent.ReplyID] {
				cold[rec.Parent.ReplyID] = false
				delete(cold, rec.Parent.ReplyID)
				changed = true
			}
		}
	}
	return cold
}

// loadParked lists open, quiet record rec at Start with no loop: its mail is
// watched by the runner's one parked watch and nothing else of it runs.
func (r *Runner) loadParked(rec Record) {
	st := r.parkedState(rec)
	r.mu.Lock()
	gen := r.registerParkedLocked(rec)
	if r.closedRows == nil {
		r.closedRows = map[string]State{}
	}
	r.closedRows[rec.ReplyID] = st
	r.mu.Unlock()
	r.watchParked(rec, gen)
}

// parkedOwnerSentinel is stored in Runner.liveOwners for parked children so
// OwnsLive can answer true without acquiring r.mu or any conversation lock.
var parkedOwnerSentinel = new(struct{})

// registerParkedLocked puts rec in the closed set as a parked child and
// returns its updated incarnation generation.
func (r *Runner) registerParkedLocked(rec Record) uint64 {
	r.closed[rec.ReplyID] = rec
	if r.parked == nil {
		r.parked = map[string]bool{}
	}
	r.parked[rec.ReplyID] = true
	if r.convGen == nil {
		r.convGen = map[string]uint64{}
	}
	r.convGen[rec.ReplyID]++
	gen := r.convGen[rec.ReplyID]
	if rec.ClosedAt == "" {
		r.liveOwners.Store(rec.ReplyID, parkedOwnerSentinel)
	} else {
		r.liveOwners.Delete(rec.ReplyID)
	}
	r.indexChildLocked(rec)
	return gen
}

// watchParked installs rec's parked mail watch for parked incarnation gen.
func (r *Runner) watchParked(rec Record, gen uint64) {
	w, ok := r.opts.Mail.(ParkedMailWatcher)
	if !ok || rec.WSKey == "" {
		return
	}
	r.mu.Lock()
	skip := r.closing || !r.parked[rec.ReplyID] || r.unparking[rec.ReplyID] != nil || r.convGen[rec.ReplyID] != gen
	r.mu.Unlock()
	if skip {
		return
	}
	id := rec.ReplyID
	stop := w.WatchParked(rec.Root, rec.WSKey, func() {
		r.mu.Lock()
		if r.closing || !r.parked[id] || r.unparking[id] != nil || r.convGen[id] != gen {
			r.mu.Unlock()
			return
		}
		r.wg.Add(1)
		r.mu.Unlock()
		go func() {
			defer r.wg.Done()
			r.unparkIfGen(id, gen)
		}()
	})
	r.mu.Lock()
	if r.parkStop == nil {
		r.parkStop = map[string]func(){}
	}
	if !r.closing && r.parked[id] && r.unparking[id] == nil && r.convGen[id] == gen && r.parkStop[id] == nil {
		r.parkStop[id] = stop
		stop = nil
	}
	r.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// dropParkedLocked forgets id's parked state and stops its mail watch.
func (r *Runner) dropParkedLocked(id string) {
	delete(r.parked, id)
	r.liveOwners.CompareAndDelete(id, parkedOwnerSentinel)
	if stop := r.parkStop[id]; stop != nil {
		delete(r.parkStop, id)
		go stop()
	}
}

// isParked reports whether id is a parked child.
func (r *Runner) isParked(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.parked[id]
}

// unpark rehydrates parked child id (a no-op returning the open
// conversation, or nil when id is neither open nor parked). Concurrent
// callers wait for the one rehydration.
func (r *Runner) unpark(id string) *conv {
	return r.unparkWithGen(id, 0, false)
}

// unparkIfGen rehydrates parked child id only while it still belongs to
// parked incarnation gen, so a delayed watch callback from an earlier parked
// generation never spuriously wakes a later parked generation.
func (r *Runner) unparkIfGen(id string, gen uint64) *conv {
	return r.unparkWithGen(id, gen, true)
}

func (r *Runner) unparkWithGen(id string, gen uint64, checkGen bool) *conv {
	for {
		r.mu.Lock()
		if c := r.convs[id]; c != nil {
			r.mu.Unlock()
			if checkGen {
				return nil
			}
			return c
		}
		if !r.parked[id] || (checkGen && r.convGen[id] != gen) {
			r.mu.Unlock()
			return nil
		}
		if ch := r.unparking[id]; ch != nil {
			r.mu.Unlock()
			if checkGen {
				return nil
			}
			<-ch
			continue
		}
		if ch := r.parkedWrite[id]; ch != nil {
			r.mu.Unlock()
			<-ch
			continue
		}
		if r.closing {
			r.mu.Unlock()
			return nil
		}
		select {
		case <-r.quit:
			r.mu.Unlock()
			return nil
		default:
		}
		ch := make(chan struct{})
		if r.unparking == nil {
			r.unparking = map[string]chan struct{}{}
		}
		r.unparking[id] = ch
		rec := r.closed[id]
		stop := r.parkStop[id]
		delete(r.parkStop, id)
		r.mu.Unlock()
		if stop != nil {
			stop()
		}
		c := r.startConvWith(rec, nil, false, nil, nil)
		r.mu.Lock()
		delete(r.unparking, id)
		closed := r.closing
		r.mu.Unlock()
		close(ch)
		if closed {
			return nil
		}
		if c != nil && c.hasHeldInbox() {
			// Kick any inbox entry already on disk when rehydrating (e.g., a
			// dashboard post that landed right before or during parking).
			c.signalInbox()
		}
		return c
	}
}

// unparkAll rehydrates every parked child among ids.
func (r *Runner) unparkIDs(ids []string) {
	for _, id := range ids {
		r.unpark(id)
	}
}

// parkedRecords lists the parked children's records, ordered by reply-id.
func (r *Runner) parkedRecords() []Record {
	r.mu.Lock()
	out := make([]Record, 0, len(r.parked))
	for id := range r.parked {
		if rec, ok := r.closed[id]; ok {
			out = append(out, rec)
		}
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ReplyID < out[j].ReplyID })
	return out
}

// parkedState is the presentation of a parked child: a waiting one.
func (r *Runner) parkedState(rec Record) State {
	st := closedState(rec)
	st.Status, st.ClosedAt = StatusIdle, ""
	st.IsChild = true
	st.Lifecycle = wsdashboard.SessionChildWaiting
	return st
}

// OwnsLive reports whether this daemon's runner serves session id as an open
// conversation: resident or parked. The index reads liveness of such a
// session from here, so its registration.json heartbeat is never rewritten.
//
// Lock-free: acquires neither r.mu nor c.mu so Index.effective and inbox
// write paths can never deadlock against Runner or conversation locks.
func (r *Runner) OwnsLive(id string) bool {
	var prev any
	for {
		v, ok := r.liveOwners.Load(id)
		if !ok {
			return false
		}
		if v == parkedOwnerSentinel {
			return true
		}
		c, ok := v.(*conv)
		if !ok || c == nil {
			return false
		}
		if !c.closedOrParked.Load() {
			return true
		}
		// If c just transitioned live -> parked (maybePark stores
		// parkedOwnerSentinel in liveOwners before setting c.closedOrParked)
		// or was replaced by a newer *conv generation, re-read liveOwners
		// once so the handoff never produces a transient false negative.
		if v == prev {
			return false
		}
		prev = v
	}
}

func (c *conv) isParkedOut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.parkedOut
}

// parkEligibleLocked reports whether the conversation may leave memory now.
// It runs in the loop with c.mu held.
func (c *conv) parkEligibleLocked() bool {
	rec := &c.rec
	if c.closed || c.parkedOut || c.hidden || c.proc != nil || c.turn || c.spawnPending || c.wakeRefused || c.expiring ||
		c.replaying || c.holding || c.compacting || c.compactCommand || c.compactInternalTurn || c.cmpActive ||
		c.bgTasks != 0 || c.held != 0 || len(c.approvals) != 0 || len(c.steered) != 0 || c.heldRes != nil || c.isForeign() {
		return false
	}
	if c.idle != nil || c.actionTimer != nil || c.bgGraceTimer != nil || c.spawnTimer != nil || c.leverC != nil ||
		c.compactC != nil || c.modelWaitC != nil || c.modelWaitDrainC != nil || c.modelWaitCancelC != nil || c.modelWaitBackoffC != nil || c.quotaC != nil {
		return false
	}
	if len(c.inboxC) != 0 || len(c.mailC) != 0 || len(c.spawnC) != 0 || len(c.reqs) != 0 {
		return false
	}
	if !c.childWaitingLocked() {
		return false
	}
	if c.r.execLive(rec.ReplyID) > 0 {
		return false
	}
	return c.r.recordParkable(*rec)
}

// maybePark runs in the loop on each heartbeat: a waiting child with nothing
// pending leaves memory. It reports whether the loop must end.
func (c *conv) maybePark() bool {
	r := c.r
	if !r.parkEnabled() || !r.canPark() || !r.startupReady() || !r.publicationReady() {
		return false
	}
	c.mu.Lock()
	ok := c.parkEligibleLocked()
	rec := c.rec
	c.mu.Unlock()
	if !ok || !r.noOpenChildren(rec.ReplyID) {
		return false
	}
	// Order with a send that is writing its inbox now: wmu is its first lock.
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.recordDirty {
		if err := c.persistRecord(); err != nil {
			return false
		}
	}
	// The presentation is taken before the runner's lock (c.mu is below it).
	st := c.memoryState()
	st.Lifecycle = wsdashboard.SessionChildWaiting
	// The runner's lock comes first, as everywhere that holds both. TryLock
	// prevents parking from blocking on a contended c.mu while holding r.mu;
	// if c.mu is busy, abort and retry on the next heartbeat.
	r.mu.Lock()
	if !c.mu.TryLock() {
		r.mu.Unlock()
		return false
	}
	if !c.parkEligibleLocked() || r.convs[c.rec.ReplyID] != c || r.unparking[c.rec.ReplyID] != nil || r.hasOpenChildLocked(c.rec.ReplyID) {
		c.mu.Unlock()
		r.mu.Unlock()
		return false
	}
	rec = c.rec
	// Store parkedOwnerSentinel in liveOwners (via registerParkedLocked)
	// BEFORE marking c.closedOrParked=true so a concurrent lock-free OwnsLive
	// never observes a false negative across the live -> parked handoff.
	gen := r.registerParkedLocked(rec)
	c.parkedOut = true
	c.syncStateLocked()
	delete(r.convs, rec.ReplyID)
	if r.closedRows == nil {
		r.closedRows = map[string]State{}
	}
	r.closedRows[rec.ReplyID] = st
	c.mu.Unlock()
	r.mu.Unlock()
	r.watchParked(rec, gen)
	return true
}

// parkEnabled is Options.ParkIdleChildren: the daemon turns it on.
func (r *Runner) parkEnabled() bool { return r.opts.ParkIdleChildren }

// noOpenChildren reports that id has no resident child conversation.
func (r *Runner) noOpenChildren(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.hasOpenChildLocked(id)
}

func (r *Runner) hasOpenChildLocked(id string) bool {
	if r.pendingSpawns[ParentRef{Kind: ParentConversation, ReplyID: id}] > 0 {
		return true
	}
	for child := range r.childrenIndex[ParentRef{Kind: ParentConversation, ReplyID: id}] {
		if r.convs[child] != nil || r.unparking[child] != nil {
			return true
		}
	}
	return false
}

// sweepParkedExpiry is the runner's one periodic sweep over parked children:
// those past agent.child_waiting_days are deleted as an awake child's own
// heartbeat deletes it, and their descendants with them. It reads memory.
func (r *Runner) sweepParkedExpiry() {
	if !r.startupReady() {
		return
	}
	now := r.opts.Now()
	expiry := r.childWaitingExpiry()
	for _, rec := range r.parkedRecords() {
		at := lastActive(rec)
		if at.IsZero() || now.Sub(at) < expiry {
			continue
		}
		r.expireLater(rec.ReplyID)
	}
}

// parkSweeper ticks the parked expiry sweep.
func (r *Runner) startParkSweeper() {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		interval := max(r.opts.heartbeat, time.Second)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.sweepParkedExpiry()
			case <-r.quit:
				return
			}
		}
	}()
}

// boundParkedConvs rehydrates and returns the parked children whose canonical
// root or working directory lies in target, for a checkout release.
func (r *Runner) boundParkedConvs(target string) []*conv {
	var out []*conv
	for _, rec := range r.parkedRecords() {
		root, cwd := canonicalBinding(rec)
		if !worktreePathContains(target, root) && !worktreePathContains(target, cwd) {
			continue
		}
		if c := r.unpark(rec.ReplyID); c != nil {
			out = append(out, c)
		}
	}
	return out
}

func (r *Runner) logPark(format string, args ...any) {
	fmt.Fprintf(r.opts.Log, format+"\n", args...)
}
