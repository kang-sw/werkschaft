package wsagent

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// storedAccountSlotID normalizes a slot ID for persistence on Record (empty
// represents the implicit default slot for T0 compatibility).
func storedAccountSlotID(slotID string) string {
	id := wscred.NormalizeAccountSlotID(slotID)
	if id == wscred.DefaultAccountSlotID {
		return ""
	}
	return id
}

// resolveAccountSlotConfigDir returns the explicit isolated config_dir to
// inject via StartSpec.AccountConfigDir for (backend, slotID), or "" for the
// implicit default slot (preserving the user's default home untouched).
func (r *Runner) resolveAccountSlotConfigDir(backend, slotID string) string {
	if r == nil || r.opts.AccountSlots == nil || !wscred.SupportedSlotHarness(backend) {
		return ""
	}
	id := wscred.NormalizeAccountSlotID(slotID)
	if id == wscred.DefaultAccountSlotID {
		return ""
	}
	slot, ok := r.opts.AccountSlots.ResolveSlot(backend, id)
	if !ok {
		return ""
	}
	return strings.TrimSpace(slot.ConfigDir)
}

// resolveAccountSlotHome resolves the full config directory path for
// (backend, slotID) through the D10 single slot-aware home resolver.
func (r *Runner) resolveAccountSlotHome(backend, slotID string) string {
	if r == nil || !wscred.SupportedSlotHarness(backend) {
		return ""
	}
	return wscred.ResolveHarnessHomeFromRegistry(r.opts.AccountSlots, backend, wscred.NormalizeAccountSlotID(slotID), r.opts.ExplicitHome, nil)
}

func (r *Runner) slotAuthPreflight(backend, configDir string, sc accountSlotContinuity) error {
	if r != nil && r.opts.SlotAuthPreflight != nil {
		return r.opts.SlotAuthPreflight(backend, configDir)
	}
	if sc == nil {
		return errors.New("slot_continuity_unsupported")
	}
	return sc.slotAuthPreflight(configDir)
}

func (c *conv) isSlot2aTurnRejection(err error) bool {
	if err == nil {
		return false
	}
	sc := c.r.slotContinuity(c.record().Backend)
	if sc == nil {
		return false
	}
	return sc.isSlot2aTurnRejection(err)
}

func (c *conv) pendingAccountSlot() string {
	rec := c.record()
	if c.r.durableProcess(rec.Backend) {
		return ""
	}
	pending := strings.TrimSpace(rec.PendingAccountSlotID)
	if pending == "" {
		return ""
	}
	normPending := wscred.NormalizeAccountSlotID(pending)
	normCurrent := wscred.NormalizeAccountSlotID(rec.AccountSlotID)
	if normPending == normCurrent {
		return ""
	}
	return normPending
}

func (c *conv) hasPendingAccountSlot() bool {
	return c.pendingAccountSlot() != ""
}

// frontBoundaryCommandPending peeks at the queued/held front command before
// startProcess runs so a /clear or /compact with a pending account switch never
// cold-starts the old slot (which may be dead or expired) only to retire it.
func (c *conv) frontBoundaryCommandPending(held []wsinbox.Record) bool {
	if len(c.rec.Queued) > 0 {
		cmd := c.rec.Queued[0].Command
		return cmd != nil && (cmd.Name == "clear" || cmd.Name == "compact")
	}
	if len(held) > 0 {
		cmd := held[0].Entry.Command
		return cmd != nil && (cmd.Name == "clear" || cmd.Name == "compact")
	}
	return false
}

// retireAndCommitPendingSlot retires any running process and commits the queued
// PendingAccountSlotID with cleared native session identity, leaving the
// conversation idle so its next wake launches cleanly on the target slot (D6).
func (c *conv) retireAndCommitPendingSlot() error {
	pending := c.pendingAccountSlot()
	if pending == "" {
		return nil
	}
	if err := c.retireCompactionProcess(); err != nil {
		return fmt.Errorf("retire process: %w", err)
	}
	c.accountSlotBoundaryPending = false
	c.slot2aVerifyPending = false
	c.slot2aFallbackRetried = false
	c.passive.clearAdvisory()
	c.update(func(rec *Record) {
		rec.AccountSlotID = storedAccountSlotID(pending)
		rec.PendingAccountSlotID = ""
		rec.SessionID, rec.SessionFile = "", ""
	})
	c.changed(false)
	c.watched()
	return nil
}

// coldRestartPendingAccountSlot executes a forced cold teardown, commits the
// pending account slot, clears native thread identity, and respawns on the new
// slot with <ws-restore> queued for the next turn (D6).
func (c *conv) coldRestartPendingAccountSlot(reason string) error {
	pending := c.pendingAccountSlot()
	if pending == "" {
		return nil
	}
	rec := c.record()
	rp, err := c.r.resolve(rec.Profile, rec.Root)
	if err != nil {
		return fmt.Errorf("resolve profile: %w", err)
	}
	if err := c.retireCompactionProcess(); err != nil {
		return fmt.Errorf("retire process: %w", err)
	}
	fresh := c.record()
	fresh.AccountSlotID = storedAccountSlotID(pending)
	fresh.PendingAccountSlotID = ""
	fresh.SessionID, fresh.SessionFile = "", ""
	start := c.startLead
	if !fresh.Lead {
		start = c.startWorker
	}
	if err := start(fresh, rp.Definition, false); err != nil {
		return fmt.Errorf("start session on slot %s (%s): %w", pending, reason, err)
	}
	c.accountSlotBoundaryPending = false
	c.slot2aVerifyPending = false
	c.slot2aFallbackRetried = false
	c.passive.clearAdvisory()
	c.queueRestore(RestoreCompaction)
	c.appendRestoreInstructions()
	c.update(func(rec *Record) {
		rec.AccountSlotID = storedAccountSlotID(pending)
		rec.PendingAccountSlotID = ""
		rec.SessionID, rec.SessionFile = "", ""
	})
	c.changed(false)
	c.watched()
	if !c.turn {
		c.armIdle()
	}
	return nil
}

// fallbackSlot2aTo2b rotates a conversation whose first post-resume turn was
// rejected into a fresh native session on the current slot via
// rotateExternalSession (<ws-restore> checkpoint inside the same permanent ws
// session).
func (c *conv) fallbackSlot2aTo2b(cause error) error {
	fmt.Fprintf(c.r.opts.Log, "agent %s: slot 2a post-resume turn rejected (%v); falling back to 2b session rotation\n", c.rec.ReplyID, cause)
	req := &compactRequest{
		resume:      false,
		triggerPath: "account_slot_2b_fallback",
		lead:        emptyWorkerSummary(),
	}
	rotated, err := c.rotateExternalSession(req, wslive.TriggerManual)
	if err != nil {
		return fmt.Errorf("slot 2b fallback rotation: %w", err)
	}
	if !rotated {
		return errors.New("slot 2b fallback rotation did not complete")
	}
	return nil
}

// switchAccountSlotNow performs an immediate Stop+Restart account slot switch
// on a single conversation, using Path 2a (pre-flight + transcript copy +
// native resume) when the backend implements accountSlotContinuity and
// automatically falling back to Path 2b (rotateExternalSession) on any
// pre-flight, copy, or resume failure (D2).
func (c *conv) switchAccountSlotNow(slotID string) (wsdashboard.AccountSlotSwitchResult, error) {
	rec := c.record()
	if c.closed || rec.ClosedAt != "" || rec.handedOff() {
		return wsdashboard.AccountSlotSwitchResult{}, endedError(rec)
	}
	if !wscred.SupportedSlotHarness(rec.Backend) {
		return wsdashboard.AccountSlotSwitchResult{}, badRequest("account_slot_unsupported")
	}
	target := wscred.NormalizeAccountSlotID(slotID)
	if c.r.opts.AccountSlots != nil {
		if _, ok := c.r.opts.AccountSlots.ResolveSlot(rec.Backend, target); !ok {
			return wsdashboard.AccountSlotSwitchResult{}, &requestError{status: http.StatusNotFound, msg: "account_slot_not_found"}
		}
	}
	if c.turn || c.compacting || c.compactCommand {
		return wsdashboard.AccountSlotSwitchResult{}, &requestError{status: http.StatusConflict, msg: "conversation_busy"}
	}
	current := wscred.NormalizeAccountSlotID(rec.AccountSlotID)
	if current == target {
		if rec.PendingAccountSlotID != "" {
			c.update(func(r *Record) { r.PendingAccountSlotID = "" })
			c.changed(false)
			c.watched()
		}
		return wsdashboard.AccountSlotSwitchResult{
			ConversationID: rec.ReplyID,
			AccountSlotID:  target,
			Mode:           "unchanged",
		}, nil
	}
	// If no backend session exists yet and no process is running, bind directly.
	if rec.SessionID == "" && c.proc == nil {
		c.update(func(r *Record) {
			r.AccountSlotID = storedAccountSlotID(target)
			r.PendingAccountSlotID = ""
		})
		c.changed(false)
		c.watched()
		return wsdashboard.AccountSlotSwitchResult{
			ConversationID: rec.ReplyID,
			AccountSlotID:  target,
			Mode:           "immediate",
		}, nil
	}

	// Attempt Path 2a when the backend implements accountSlotContinuity.
	if rec.SessionID != "" {
		if ok, err := c.trySlot2aSwitch(rec, target); ok && err == nil {
			return wsdashboard.AccountSlotSwitchResult{
				ConversationID: rec.ReplyID,
				AccountSlotID:  target,
				Mode:           "restarted_2a",
			}, nil
		}
	}

	// Path 2b: rotateExternalSession with target slot committed between
	// retireCompactionProcess and startLead/startWorker.
	prevPending := rec.PendingAccountSlotID
	c.update(func(r *Record) { r.PendingAccountSlotID = target })
	req := &compactRequest{
		resume:      false,
		triggerPath: "account_slot_2b",
		lead:        emptyWorkerSummary(),
	}
	rotated, err := c.rotateExternalSession(req, wslive.TriggerManual)
	if err != nil || !rotated {
		// Restore prior pending slot on rotation failure.
		c.update(func(r *Record) { r.PendingAccountSlotID = prevPending })
		if err == nil {
			err = errors.New("account_slot_switch_failed")
		}
		return wsdashboard.AccountSlotSwitchResult{}, err
	}
	return wsdashboard.AccountSlotSwitchResult{
		ConversationID: rec.ReplyID,
		AccountSlotID:  target,
		Mode:           "rotated_2b",
	}, nil
}

func (c *conv) trySlot2aSwitch(rec Record, target string) (bool, error) {
	sc := c.r.slotContinuity(rec.Backend)
	if sc == nil {
		return false, nil
	}
	rp, err := c.r.resolve(rec.Profile, rec.Root)
	if err != nil {
		return false, err
	}
	srcHome := c.r.resolveAccountSlotHome(rec.Backend, rec.AccountSlotID)
	dstHome := c.r.resolveAccountSlotHome(rec.Backend, target)
	if err := c.r.slotAuthPreflight(rec.Backend, dstHome, sc); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: slot 2a preflight failed (%v); falling back to 2b\n", rec.ReplyID, err)
		return false, err
	}
	if err := sc.migrateSlotSession(srcHome, dstHome, rec.SessionID); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: slot 2a session migration failed (%v); falling back to 2b\n", rec.ReplyID, err)
		return false, err
	}
	if err := c.retireCompactionProcess(); err != nil {
		return false, err
	}
	fresh := c.record()
	fresh.AccountSlotID = storedAccountSlotID(target)
	fresh.PendingAccountSlotID = ""
	start := c.startLead
	if !fresh.Lead {
		start = c.startWorker
	}
	if err := start(fresh, rp.Definition, false); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: slot 2a native resume failed (%v); falling back to 2b\n", rec.ReplyID, err)
		return false, err
	}
	c.accountSlotBoundaryPending = false
	c.slot2aVerifyPending = true
	c.slot2aFallbackRetried = false
	c.update(func(r *Record) {
		r.AccountSlotID = storedAccountSlotID(target)
		r.PendingAccountSlotID = ""
	})
	c.changed(false)
	c.watched()
	if !c.turn {
		c.armIdle()
	}
	return true, nil
}

// AccountSlotConversations implements wsdashboard.AgentAccountSlotHost from
// in-memory resident conversations and parked records (zero disk I/O).
func (r *Runner) AccountSlotConversations(harness string) []wsdashboard.AccountSlotConversation {
	harness = strings.TrimSpace(harness)
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	parked := make([]Record, 0, len(r.parked))
	for id := range r.parked {
		if rec, ok := r.closed[id]; ok {
			parked = append(parked, rec)
		}
	}
	r.mu.Unlock()

	var out []wsdashboard.AccountSlotConversation
	for _, c := range convs {
		st := c.memoryState()
		if st.Status == StatusClosed || st.Status == StatusHandedOff {
			continue
		}
		if harness != "" && st.Backend != harness {
			continue
		}
		if harness == "" && !wscred.SupportedSlotHarness(st.Backend) {
			continue
		}
		label := st.Label
		if label == "" {
			label = filepath.Base(st.Root)
		}
		out = append(out, wsdashboard.AccountSlotConversation{
			ID:                   st.ID,
			Backend:              st.Backend,
			Root:                 st.Root,
			Label:                label,
			Lead:                 st.Lead,
			Status:               st.Status,
			Process:              st.Process,
			AccountSlotID:        wscred.NormalizeAccountSlotID(st.AccountSlotID),
			PendingAccountSlotID: st.PendingAccountSlotID,
		})
	}
	for _, rec := range parked {
		if rec.ClosedAt != "" || rec.handedOff() {
			continue
		}
		if harness != "" && rec.Backend != harness {
			continue
		}
		if harness == "" && !wscred.SupportedSlotHarness(rec.Backend) {
			continue
		}
		label := rec.Label
		if label == "" {
			label = filepath.Base(rec.Root)
		}
		out = append(out, wsdashboard.AccountSlotConversation{
			ID:                   rec.ReplyID,
			Backend:              rec.Backend,
			Root:                 rec.Root,
			Label:                label,
			Lead:                 rec.Lead,
			Status:               StatusIdle,
			Process:              false,
			AccountSlotID:        wscred.NormalizeAccountSlotID(rec.AccountSlotID),
			PendingAccountSlotID: rec.PendingAccountSlotID,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lead != out[j].Lead {
			return out[i].Lead
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (c *conv) applyQueuedAccountSlot(harness, target string) (queued, skipped bool) {
	if c.closed || c.rec.ClosedAt != "" || c.rec.handedOff() || c.rec.Backend != harness {
		return false, false
	}
	cur := wscred.NormalizeAccountSlotID(c.rec.AccountSlotID)
	if cur == target {
		if c.rec.PendingAccountSlotID != "" {
			c.update(func(r *Record) { r.PendingAccountSlotID = "" })
			c.changed(false)
			c.watched()
		}
		return false, true
	}
	c.update(func(r *Record) { r.PendingAccountSlotID = target })
	c.changed(false)
	c.watched()
	return true, false
}

func (r *Runner) queueParkedAccountSlot(id, harness, target string) (queued, skipped bool) {
	for {
		r.mu.Lock()
		if ch := r.parkedWrite[id]; ch != nil {
			r.mu.Unlock()
			<-ch
			continue
		}
		if ch := r.unparking[id]; ch != nil {
			r.mu.Unlock()
			<-ch
			continue
		}
		if c := r.convs[id]; c != nil {
			r.mu.Unlock()
			var q, s bool
			if !c.do(func() {
				q, s = c.applyQueuedAccountSlot(harness, target)
			}) {
				if c.isParkedOut() {
					continue
				}
				return false, false
			}
			return q, s
		}
		rec, stillParked := r.closed[id]
		if !stillParked || !r.parked[id] || rec.Backend != harness || rec.ClosedAt != "" || rec.handedOff() {
			r.mu.Unlock()
			return false, false
		}
		cur := wscred.NormalizeAccountSlotID(rec.AccountSlotID)
		changed := false
		if cur == target {
			if rec.PendingAccountSlotID != "" {
				rec.PendingAccountSlotID = ""
				changed = true
			}
			skipped = true
		} else {
			rec.PendingAccountSlotID = target
			changed = true
			queued = true
		}
		if !changed {
			r.mu.Unlock()
			return queued, skipped
		}
		r.closed[id] = rec
		if r.closedRows != nil {
			r.closedRows[id] = r.parkedState(rec)
		}
		ch := make(chan struct{})
		if r.parkedWrite == nil {
			r.parkedWrite = map[string]chan struct{}{}
		}
		r.parkedWrite[id] = ch
		hook := r.opts.beforeParkedAccountSlotWrite
		r.mu.Unlock()

		func() {
			defer func() {
				r.mu.Lock()
				delete(r.parkedWrite, id)
				r.mu.Unlock()
				close(ch)
			}()
			if hook != nil {
				hook(id)
			}
			dir := wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID)
			_ = r.persistAgentRecord(dir, rec)
		}()
		return queued, skipped
	}
}

// QueueHarnessAccountSlot implements wsdashboard.AgentAccountSlotHost (D13):
// fans out PendingAccountSlotID to every open conversation (leads, resident
// workers, and parked workers) of harness. Already-on-target conversations are
// skipped (clearing any stale pending slot); an existing pending slot on a
// conversation is overwritten (last write wins).
func (r *Runner) QueueHarnessAccountSlot(harness, slotID string) (queued, skipped int, err error) {
	harness = strings.TrimSpace(harness)
	if !wscred.SupportedSlotHarness(harness) {
		return 0, 0, badRequest("account_slot_unsupported")
	}
	target := wscred.NormalizeAccountSlotID(slotID)
	if r.opts.AccountSlots != nil {
		if _, ok := r.opts.AccountSlots.ResolveSlot(harness, target); !ok {
			return 0, 0, &requestError{status: http.StatusNotFound, msg: "account_slot_not_found"}
		}
	}

	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	parkedIDs := make([]string, 0, len(r.parked))
	for id := range r.parked {
		if rec, ok := r.closed[id]; ok && rec.Backend == harness && rec.ClosedAt == "" && !rec.handedOff() {
			parkedIDs = append(parkedIDs, id)
		}
	}
	r.mu.Unlock()

	for _, c := range convs {
		rec := c.record()
		if rec.Backend != harness || rec.ClosedAt != "" || rec.handedOff() {
			continue
		}
		var didQueue, didSkip bool
		if !c.do(func() {
			didQueue, didSkip = c.applyQueuedAccountSlot(harness, target)
		}) {
			if c.isParkedOut() {
				parkedIDs = append(parkedIDs, rec.ReplyID)
			}
			continue
		}
		if didQueue {
			queued++
		} else if didSkip {
			skipped++
		}
	}

	for _, id := range parkedIDs {
		didQueue, didSkip := r.queueParkedAccountSlot(id, harness, target)
		if didQueue {
			queued++
		} else if didSkip {
			skipped++
		}
	}
	return queued, skipped, nil
}

// QueueConversationAccountSlot queues PendingAccountSlotID on one open
// conversation (applied at the next compaction or /clear boundary, D6/D11).
func (r *Runner) QueueConversationAccountSlot(id, slotID string) (wsdashboard.AccountSlotSwitchResult, error) {
	for {
		c, err := r.open(id)
		if err != nil {
			return wsdashboard.AccountSlotSwitchResult{}, err
		}
		var res wsdashboard.AccountSlotSwitchResult
		if !c.do(func() {
			rec := c.record()
			if c.closed || rec.ClosedAt != "" || rec.handedOff() {
				err = endedError(rec)
				return
			}
			if !wscred.SupportedSlotHarness(rec.Backend) {
				err = badRequest("account_slot_unsupported")
				return
			}
			target := wscred.NormalizeAccountSlotID(slotID)
			if r.opts.AccountSlots != nil {
				if _, ok := r.opts.AccountSlots.ResolveSlot(rec.Backend, target); !ok {
					err = &requestError{status: http.StatusNotFound, msg: "account_slot_not_found"}
					return
				}
			}
			cur := wscred.NormalizeAccountSlotID(rec.AccountSlotID)
			if cur == target {
				if rec.PendingAccountSlotID != "" {
					c.update(func(r *Record) { r.PendingAccountSlotID = "" })
					c.changed(false)
					c.watched()
				}
				res = wsdashboard.AccountSlotSwitchResult{
					ConversationID: rec.ReplyID,
					AccountSlotID:  cur,
					Mode:           "unchanged",
				}
				return
			}
			c.update(func(r *Record) { r.PendingAccountSlotID = target })
			c.changed(false)
			c.watched()
			res = wsdashboard.AccountSlotSwitchResult{
				ConversationID:       rec.ReplyID,
				AccountSlotID:        cur,
				PendingAccountSlotID: target,
				Mode:                 "queued",
			}
		}) {
			if c.isParkedOut() {
				continue
			}
			return wsdashboard.AccountSlotSwitchResult{}, endedError(c.record())
		}
		return res, err
	}
}

// SwitchConversationAccountSlotNow performs an immediate Stop+Restart switch
// (2a -> 2b) on one open conversation (D2/D11).
func (r *Runner) SwitchConversationAccountSlotNow(id, slotID string) (wsdashboard.AccountSlotSwitchResult, error) {
	for {
		c, err := r.open(id)
		if err != nil {
			return wsdashboard.AccountSlotSwitchResult{}, err
		}
		var res wsdashboard.AccountSlotSwitchResult
		if !c.do(func() {
			res, err = c.switchAccountSlotNow(slotID)
		}) {
			if c.isParkedOut() {
				continue
			}
			return wsdashboard.AccountSlotSwitchResult{}, endedError(c.record())
		}
		return res, err
	}
}

var _ wsdashboard.AgentAccountSlotHost = (*Runner)(nil)

// SlotAuthPreflight validates a harness account slot directory using the
// backend's registered accountSlotContinuity pre-flight check (D14).
func SlotAuthPreflight(harness, configDir string) error {
	reg, ok := BuiltinBackendRegistrations()[harness]
	if ok && reg.Backend != nil {
		if sc, ok := reg.Backend.(accountSlotContinuity); ok {
			return sc.slotAuthPreflight(configDir)
		}
	}
	if !wscred.SlotAuthReady(harness, configDir) {
		return errors.New("slot_unauthenticated")
	}
	return nil
}
