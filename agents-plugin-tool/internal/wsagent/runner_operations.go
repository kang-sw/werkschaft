// runner_operations.go holds conversation queries and controls.
package wsagent

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

func (r *Runner) Get(id string) (State, error) {
	r.mu.Lock()
	c, open := r.convs[id]
	rec, closed := r.closed[id]
	var st State
	if closed && !open {
		st = r.memoryClosedState(rec)
	}
	r.mu.Unlock()
	switch {
	case open:
		c.refreshHeld() // explicit API reads retain authoritative held counts
		if c.record().Lead {
			r.revalidatePermissionSelection(id, c.dir)
		}
		return c.state(), nil
	case closed:
		if st.Status == StatusHandedOff {
			return r.closedState(rec), nil
		}
		return r.permissionState(st, rec, wsdashboard.SessionDir(r.opts.Dir, id)), nil
	}
	return State{}, errUnknown
}

// List reads every visible conversation's state, oldest first.
// Companion-managed sessions (CompanionRole != "") are hidden from the roster.
func (r *Runner) List() []State {
	r.mu.Lock()
	out := make([]State, 0, len(r.convs)+len(r.closed))
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	for _, rec := range r.closed {
		if rec.CompanionRole != "" {
			continue
		}
		out = append(out, r.memoryClosedState(rec))
	}
	r.mu.Unlock()
	for _, c := range convs {
		if c.record().CompanionRole != "" {
			continue
		}
		out = append(out, c.memoryState())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// IsCompanion reports whether conversation id is a daemon-managed companion
// session (companion-body or companion-work).
func (r *Runner) IsCompanion(id string) bool {
	rec, ok := r.recordOf(id)
	return ok && rec.CompanionRole != ""
}

// CompanionRecord returns the persisted record snapshot for conversation id.
func (r *Runner) CompanionRecord(id string) (Record, bool) {
	return r.recordOf(id)
}

// IsConversationLive reports whether conversation id is currently open (not
// closed and not handed off).
func (r *Runner) IsConversationLive(id string) bool {
	r.mu.Lock()
	c, ok := r.convs[id]
	r.mu.Unlock()
	if !ok {
		return false
	}
	c.mu.Lock()
	live := !c.closed
	c.mu.Unlock()
	return live
}

// HasLiveChildren reports whether conversation parentID currently has any live
// spawned descendant conversation (running, starting, or holding queued work).
func (r *Runner) HasLiveChildren(parentID string) bool {
	if parentID == "" {
		return false
	}
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	r.mu.Unlock()
	type convInfo struct {
		id       string
		parentID string
		active   bool
	}
	byID := make(map[string]convInfo, len(convs))
	for _, c := range convs {
		c.mu.Lock()
		pid := ""
		if c.rec.Parent != nil {
			pid = c.rec.Parent.ReplyID
		}
		info := convInfo{
			id:       c.rec.ReplyID,
			parentID: pid,
			active:   !c.closed && (c.turn || c.proc != nil || c.spawnPending || c.sends > max(c.settled.mark, c.voided)),
		}
		c.mu.Unlock()
		byID[info.id] = info
	}
	for _, info := range byID {
		if !info.active {
			continue
		}
		cur := info.parentID
		for depth := 0; cur != "" && depth < 8; depth++ {
			if cur == parentID {
				return true
			}
			p, ok := byID[cur]
			if !ok {
				break
			}
			cur = p.parentID
		}
	}
	return false
}

// CompactCompanionIdle requests a non-resuming compaction for a companion
// session at its idle boundary via the existing hosted-session compaction path.
func (r *Runner) CompactCompanionIdle(wsKey string) CompactResult {
	resume := false
	return r.Compact(wsKey, "", &resume)
}

// Send holds text as the conversation's next user message. It is submitted
// now when no turn runs and otherwise waits for the current turn to end.
// Ended conversations refuse sends.
// It reports whether the message is still held.
//
// Send accepts operator input and returns once it is in the conversation's inbox: it neither
// waits for the loop nor starts the process, which the loop does on its
// signal (inboxChanged), so a busy loop or a slow start never holds the
// caller. A start failure reaches the conversation's State.Error, not this
// answer. The inbox write is serialized against close under c.mu, so a send
// racing CloseConversation either lands before the close (which drops it
// as it drops every held message) or is refused, never written into a
// closed conversation's inbox that nothing reads.
func (r *Runner) Send(id, text string) (State, error) {
	return r.sendUser(id, text, true)
}

func (r *Runner) send(id, text string, operator bool) (State, error) {
	return r.sendWithSender(id, text, operator, false, "")
}

func (r *Runner) sendUser(id, text string, operator bool) (State, error) {
	return r.sendWithSender(id, text, operator, true, "")
}

func (r *Runner) sendFrom(id, text string, operator bool, sender string) (State, error) {
	return r.sendWithSender(id, text, operator, false, sender)
}

func (r *Runner) sendWithSender(id, text string, operator, user bool, sender string) (State, error) {
	if strings.TrimSpace(text) == "" {
		return State{}, badRequest("text is empty")
	}
	c, err := r.open(id)
	if err != nil {
		return State{}, err
	}
	if r.opts.sendAdmitting != nil {
		r.opts.sendAdmitting(id)
	}
	c.wmu.Lock()
	c.mu.Lock()
	if c.parkedOut {
		// Parked after open returned (maybePark holds wmu while it parks):
		// open rehydrates it, and the send goes to the new conversation.
		c.mu.Unlock()
		c.wmu.Unlock()
		return r.sendWithSender(id, text, operator, user, sender)
	}
	defer c.wmu.Unlock()
	if c.closed {
		err := endedError(c.rec)
		c.mu.Unlock()
		return State{}, err
	}
	name, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Parts: []string{text}, Operator: operator && c.rec.Lead, User: user}, r.opts.Now())
	if err == nil {
		// After the write, under the lock kick drains with: a turn whose
		// drain counted this send holds its message (Wait). These rec
		// stamps come from outside the loop, so the loop copies the whole
		// record under mu (conv.record), never as a bare c.rec value.
		c.sends++
		if sender != "" {
			if c.deliverySenders == nil {
				c.deliverySenders = map[uint64]pendingDelivery{}
			}
			c.deliverySenders[c.sends] = pendingDelivery{name: name, sender: sender}
		}
		c.held++
		c.rec.Sends = c.sends
		c.syncStateLocked()
		c.cancelModelWaitBackoffForInput()
		c.cancelQuotaContinuationForInput()
		if c.rec.Parent != nil || c.rec.Lead {
			// Any message resets a spawned child's waiting expiry and tracks
			// a hosted lead's last activity in memory (riding recordDirty).
			c.rec.LastActiveAt = r.opts.Now().UTC().Format(time.RFC3339)
		}
	}
	c.mu.Unlock()
	if err != nil {
		return State{}, err
	}
	c.inboxMutated(1)
	// The inbox already owns the text durably. Coverage and the child's
	// activity stamp ride the next immediate record write or shutdown flush.
	c.recordDirty = true
	c.signalInbox()
	return c.state(), nil
}

// Interrupt stops the running turn, if any, and takes back the held
// messages, returning their text, as the session page's interrupt does.
// What a drain already took and queued behind a slash command (Record.Queued)
// is not held: the inbox shows it received, so it stays and is sent after
// the interrupted turn ends, as the page's interrupt leaves it too.
func (r *Runner) Interrupt(id string) (State, []string, error) {
	var returned []string
	st, err := r.act(id, func(c *conv) error {
		c.mu.Lock()
		held, _, err := wsinbox.Interrupt(c.inbox, r.opts.Now())
		if err == nil {
			c.held = 0
		}
		c.mu.Unlock()
		if err != nil {
			return err
		}
		c.inboxMutated(len(held))
		c.cutoverInstructionLeft(held)
		for _, rec := range held {
			returned = append(returned, rec.Entry.Text())
		}
		c.cancelQuotaContinuation(false)
		c.checkInterrupt()
		return nil
	})
	return st, returned, err
}

// Stop ends the conversation's process; held messages stay held, and the
// next send resumes the backend session.
func (r *Runner) Stop(id string) (State, error) {
	return r.stop(id, false)
}

func (r *Runner) stop(id string, safeOnly bool) (State, error) {
	return r.act(id, func(c *conv) error {
		if safeOnly && !r.admitsSafeStop(id, c) {
			return &requestError{status: http.StatusConflict, msg: "session no longer safe to stop"}
		}
		c.stopProcess("")
		return nil
	})
}

// IdleLead is a lead session SleepIdle may stop: one JSON entry of the
// idle list.
type IdleLead struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Backend string `json:"backend"`
}

// sleepable reports a lead conversation whose process may be stopped for
// power saving: it runs, and no turn, replay, background task, active child
// or approval prompt keeps it busy (the idle timer's own test, idleNow).
// Run in the conversation's loop.
func (c *conv) sleepable() bool {
	return c.rec.Lead && !c.closed && c.proc != nil && len(c.approvals) == 0 && c.idleNow()
}

// IdleLeads lists the lead sessions with a running process that SleepIdle
// would stop, and counts the other running ones (busy or waiting for an
// approval), which it keeps.
func (r *Runner) IdleLeads() (idle []IdleLead, kept int) {
	idle = []IdleLead{}
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	r.mu.Unlock()
	created := map[string]string{}
	for _, c := range convs {
		c.do(func() {
			if !c.rec.Lead || c.rec.CompanionRole != "" || c.closed || c.proc == nil {
				return
			}
			if !c.sleepable() {
				kept++
				return
			}
			idle = append(idle, IdleLead{ID: c.rec.ReplyID, Label: c.rec.Label, Backend: c.rec.Backend})
			created[c.rec.ReplyID] = c.rec.CreatedAt
		})
	}
	sort.Slice(idle, func(i, j int) bool {
		if a, b := created[idle[i].ID], created[idle[j].ID]; a != b {
			return a < b
		}
		return idle[i].ID < idle[j].ID
	})
	return idle, kept
}

// SleepIdle stops each named lead session as Stop does, but only when it is
// still idle in its own loop; one that became busy (or is no lead process
// any more) is skipped. Held messages stay held and the next send resumes
// a stopped session.
func (r *Runner) SleepIdle(ids []string) (slept, skipped []string) {
	slept, skipped = []string{}, []string{}
	for _, id := range ids {
		stopped := false
		_, err := r.act(id, func(c *conv) error {
			if c.sleepable() {
				c.stopProcess("")
				stopped = true
			}
			return nil
		})
		if err == nil && stopped {
			slept = append(slept, id)
		} else {
			skipped = append(skipped, id)
		}
	}
	return slept, skipped
}

// Restart ends a lead conversation's process and starts a new one on the
// same backend session at once: the way back from an MCP connection that
// did not recover in-stream. It ends the session's background work with
// the process, so the dashboard asks first.
func (r *Runner) Restart(id string) (State, error) {
	return r.act(id, func(c *conv) error {
		if !c.rec.Lead {
			return badRequest("restart applies to a lead session only")
		}
		return c.restartProcess()
	})
}

// IsLead reports whether id is a lead conversation, open or closed: one
// whose control writes take the trusted gate.
func (r *Runner) IsLead(id string) bool {
	r.mu.Lock()
	c, open := r.convs[id]
	rec, closed := r.closed[id]
	r.mu.Unlock()
	if open {
		return c.record().Lead
	}
	return closed && rec.Lead
}

func (r *Runner) isChildRecord(rec Record) bool {
	if rec.Parent != nil {
		return true
	}
	return r != nil && r.opts.CardBound != nil && r.opts.CardBound(rec.ReplyID)
}

// IsChild reports whether id is a child conversation whose settings are fixed
// at spawn: either a spawned worker (Record.Parent != nil) or a card-bound
// subordinate lead reported by Options.CardBound.
func (r *Runner) IsChild(id string) bool {
	r.mu.Lock()
	c, open := r.convs[id]
	rec, closed := r.closed[id]
	r.mu.Unlock()
	if open {
		return r.isChildRecord(c.record())
	}
	if closed {
		return r.isChildRecord(rec)
	}
	return r.opts.CardBound != nil && r.opts.CardBound(id)
}

// isHolderBacked reports whether proc runs under a live holder process rather
// than directly under the daemon.
func isHolderBacked(p Process) bool {
	hp, ok := p.(holderProcess)
	if !ok {
		return false
	}
	_, _, held := hp.HolderInfo()
	return held
}

func (c *conv) blocksParentSafeStopLocked() bool {
	return c.keepsAwakeLocked() || (!c.closed && c.proc != nil && !isHolderBacked(c.proc))
}

func safeStopParent(id string, lead bool) ParentRef {
	kind := ParentConversation
	if lead {
		kind = ParentLead
	}
	return ParentRef{Kind: kind, ReplyID: id}
}

// admitsSafeStop runs inside the root loop. Keep the target tree and its
// pending spawn slots together under r.mu. TryLock avoids waiting on another
// conversation's transition while holding the registry lock.
func (r *Runner) admitsSafeStop(rootID string, root *conv) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.convs[rootID] != root || !root.mu.TryLock() {
		return false
	}
	defer root.mu.Unlock()
	if !root.safeStopSelfLocked() {
		return false
	}

	live := []*conv{root}
	queue := []ParentRef{safeStopParent(rootID, root.rec.Lead)}
	visited := map[string]bool{rootID: true}
	for i := 0; i < len(queue); i++ {
		parent := queue[i]
		if r.pendingSpawns[parent] > 0 {
			return false
		}
		for id := range r.childrenIndex[parent] {
			if visited[id] {
				continue
			}
			visited[id] = true
			child := r.convs[id]
			if child == nil {
				// Checkout release can close a worker while its children run
				// elsewhere. The retained row still carries their ancestry.
				if rec, ok := r.closed[id]; ok {
					queue = append(queue, safeStopParent(id, rec.Lead))
				}
				continue
			}
			if !child.mu.TryLock() {
				return false
			}
			defer child.mu.Unlock()
			if child.blocksParentSafeStopLocked() {
				return false
			}
			live = append(live, child)
			queue = append(queue, safeStopParent(id, child.rec.Lead))
		}
	}

	// The predicates above read exec counts independently. Validate all live
	// tree members together against one immutable publication with their
	// conversation state still pinned.
	if exec := r.exec.live.Load(); exec != nil {
		for _, c := range live {
			if !c.closed && (*exec)[c.rec.ReplyID] > 0 {
				return false
			}
		}
	}
	return true
}

// descendantsAllowSafeStop is the dashboard advisory for descendant work and
// pending spawns. Read each parent's edges under r.mu, then inspect the live
// children after releasing it.
func (r *Runner) descendantsAllowSafeStop(rootID string, rootIsLead bool) bool {
	if r == nil {
		return true
	}
	queue := []ParentRef{safeStopParent(rootID, rootIsLead)}
	visited := map[string]bool{rootID: true}
	for i := 0; i < len(queue); i++ {
		parent := queue[i]
		r.mu.Lock()
		if r.pendingSpawns[parent] > 0 {
			r.mu.Unlock()
			return false
		}
		var live []*conv
		for id := range r.childrenIndex[parent] {
			if visited[id] {
				continue
			}
			visited[id] = true
			if child := r.convs[id]; child != nil {
				live = append(live, child)
			} else if rec, ok := r.closed[id]; ok {
				queue = append(queue, safeStopParent(id, rec.Lead))
			}
		}
		r.mu.Unlock()
		for _, child := range live {
			child.mu.Lock()
			busy := child.blocksParentSafeStopLocked()
			next := safeStopParent(child.rec.ReplyID, child.rec.Lead)
			child.mu.Unlock()
			if busy {
				return false
			}
			queue = append(queue, next)
		}
	}
	return true
}

// SessionMeta returns in-memory dashboard metadata for replyID without
// touching disk (wsdashboard.AgentSessionMetaHost).
func (r *Runner) SessionMeta(replyID string) (wsdashboard.AgentSessionMeta, bool) {
	r.mu.Lock()
	c, open := r.convs[replyID]
	rec, closed := r.closed[replyID]
	r.mu.Unlock()
	active := false
	hasProcess := false
	selfSafe := false
	if open {
		c.mu.Lock()
		rec = c.rec
		closed = c.closed
		active = !closed && (c.turn || c.holding || len(c.approvals) != 0)
		hasProcess = !closed && c.proc != nil
		selfSafe = c.safeStopSelfLocked()
		c.mu.Unlock()
	} else if !closed {
		return wsdashboard.AgentSessionMeta{}, false
	}
	safeStop := selfSafe && r.descendantsAllowSafeStop(replyID, rec.Lead)
	return wsdashboard.AgentSessionMeta{
		Hosted:       rec.Lead,
		IsChild:      r.isChildRecord(rec),
		Closed:       closed,
		Active:       active,
		HasProcess:   hasProcess,
		SafeStop:     safeStop,
		Backend:      rec.Backend,
		Model:        rec.Model,
		Effort:       rec.Effort,
		LastActiveAt: rec.LastActiveAt,
	}, true
}

// AnswerApproval answers the waiting tool permission prompt requestID of a
// lead conversation: allow runs the tool call as asked, deny refuses it
// with message for the model. A prompt is answered once: an id that is not
// waiting (answered from another tab, withdrawn, or of an ended process) is
// a conflict.
func (r *Runner) AnswerApproval(id, requestID string, allow bool, message string) (State, error) {
	return r.act(id, func(c *conv) error {
		return c.answerApproval(requestID, allow, message)
	})
}

// SetPermissionMode changes an admitted lead conversation's permission mode.
// Like a model or effort change it applies at the next turn boundary,
// in-stream when the harness takes it; the mode the harness then reports is
// State.ActiveMode.
func (r *Runner) SetPermissionMode(id, mode string) (State, error) {
	return r.setPermissionMode(id, mode, false)
}

func (r *Runner) setPermissionMode(id, mode string, user bool) (State, error) {
	modes := LeadPermissionModes
	if rec, ok := r.recordOf(id); ok {
		modes = permissionModes(rec.Backend, []map[string]BackendRegistration{r.opts.Backends})
	}
	if !slices.Contains(modes, mode) {
		return State{}, categorized(ErrInvalidProfile, badRequest("permission_mode %q must be one of %s", mode, strings.Join(modes, ", ")))
	}
	return r.act(id, func(c *conv) error {
		return c.setNativePermissionMode(mode, user)
	})
}

// CloseConversation ends the conversation: its process stops, held
// messages are dropped, and its session directory stays readable. A spawned
// closed conversation refuses Send. Open descendants through conversation
// parents and a lead's hosted
// children (children.go), close first, deepest first. A conversation parent
// whose done turn waited for this child's report is released. A
// conversation whose retiring holder could not be ended stays open and
// fails the close (errHolderNotEnded); a descendant that does stops the
// cascade before its ancestors close, so closing id again retries it.
func (r *Runner) CloseConversation(id string) (State, error) {
	st, _, err := r.closeConversation(id)
	return st, err
}

// closeConversation returns the deepest-first order on success, or a nil
// order if the cascade fails.
func (r *Runner) closeConversation(id string) (State, []string, error) {
	// The jobs a predecessor left are known only once bound: a close
	// before that would close over jobs it cannot stop. The wait holds no
	// lock.
	if err := r.awaitExecBound(); err != nil {
		return State{}, nil, fmt.Errorf("close %s: %w", id, err)
	}
	r.resumeMu.Lock()
	defer r.resumeMu.Unlock()
	if _, err := r.open(id); err != nil {
		return State{}, nil, err
	}
	descendants := r.descendants(id)
	order := append(descendants, id)
	st, err := r.closeCascade(id, order)
	if err != nil {
		return st, nil, err
	}
	return st, order, nil
}

func (r *Runner) closeConversationExpiry(id string, checkAge bool) (State, []string, error) {
	if err := r.awaitExecBound(); err != nil {
		return State{}, nil, fmt.Errorf("close %s: %w", id, err)
	}
	r.resumeMu.Lock()
	defer r.resumeMu.Unlock()
	if _, err := r.open(id); err != nil {
		return State{}, nil, err
	}
	return r.closeConversationExpiryLocked(id, checkAge)
}

func (r *Runner) closeConversationExpiryLocked(id string, checkAge bool) (State, []string, error) {
	// Close exactly the descendants whose ancestry, incarnation, and quiet
	// state allowed expiry, revalidated under an admission fence before
	// closing any node.
	plan, quiet := r.planExpiry(id, checkAge)
	if !quiet {
		return State{}, nil, nil
	}
	st, err := r.closeExpiryCascade(id, plan)
	if err != nil {
		if errors.Is(err, errExpiryAborted) {
			return State{}, nil, nil
		}
		return st, nil, err
	}
	return st, plan.order, nil
}

func (r *Runner) closeExpiryCascade(id string, plan expiryPlan) (State, error) {
	if r.opts.beforeExpiryCascade != nil {
		r.opts.beforeExpiryCascade(id)
	}
	r.fenceExec(plan.order)
	var markedExpiring []*conv
	clearExpiring := func() {
		for _, c := range markedExpiring {
			c.mu.Lock()
			c.expiring = false
			c.mu.Unlock()
		}
	}
	abortErr := func(from int, err error) (State, error) {
		for _, rest := range plan.order[from:] {
			r.unfenceExec(rest)
		}
		clearExpiring()
		return State{}, err
	}
	abort := func(from int) (State, error) {
		return abortErr(from, errExpiryAborted)
	}
	// Rehydrate parked descendants before closing any node so unread mailbox
	// or inbox entries are claimed and validated together under the admission
	// fence. Any descendant woken since snapshot fails the generation check.
	for _, d := range plan.order {
		snap := plan.incarnations[d]
		if !snap.parked {
			continue
		}
		r.mu.Lock()
		stillParked := r.parked[d] && r.convs[d] == nil && r.unparking[d] == nil && r.convGen[d] == snap.gen
		r.mu.Unlock()
		if !stillParked {
			return abort(0)
		}
		c := r.unpark(d)
		if c == nil {
			return abort(0)
		}
		r.mu.Lock()
		same := r.convs[d] == c && r.convGen[d] == snap.gen+1
		newGen := r.convGen[d]
		r.mu.Unlock()
		if !same {
			return abort(0)
		}
		c.mu.Lock()
		if !c.expiring {
			c.expiring = true
			markedExpiring = append(markedExpiring, c)
		}
		c.mu.Unlock()
		plan.incarnations[d] = expiryIncarnation{conv: c, parked: false, gen: newGen, sends: snap.sends, lastTurnID: snap.lastTurnID}
		if !c.do(func() {
			// Consume startConvWith's initial mail signal before running
			// mailChanged so a resident mailC token does not trip admitsExpiryTree
			// when the mailbox is actually empty.
			select {
			case <-c.mailC:
			default:
			}
			c.refreshHeld()
			if c.hasHeldInbox() {
				c.inboxChanged()
			}
			c.mailChanged()
		}) {
			return abort(0)
		}
	}
	expiringDone, newlyMarked, ok, missingRoot := r.admitsAndFenceExpiryTree(plan)
	markedExpiring = append(markedExpiring, newlyMarked...)
	if !ok {
		if missingRoot {
			return abortErr(0, errUnknown)
		}
		return abort(0)
	}
	defer func() {
		r.mu.Lock()
		for nid := range plan.treeNodes {
			if r.expiring[nid] == expiringDone {
				delete(r.expiring, nid)
			}
		}
		r.mu.Unlock()
		close(expiringDone)
	}()
	for i, d := range plan.order {
		if ok, missingRoot := r.admitsExpiryTree(plan, plan.order[i:]); !ok {
			if missingRoot {
				return abortErr(i, errUnknown)
			}
			return abort(i)
		}
		if err := r.stopExec(d); err != nil {
			for _, rest := range plan.order[i:] {
				r.unfenceExec(rest)
			}
			clearExpiring()
			return State{}, fmt.Errorf("close %s: an exec job did not stop, so it and the sessions above it stay open: %w", d, err)
		}
		if d == id {
			break
		}
		if r.opts.beforeExpiryNodeClose != nil {
			r.opts.beforeExpiryNodeClose(d)
		}
		if _, err := r.closeOneExpiry(d, false, plan.incarnations[d], false, time.Time{}); err != nil {
			if errors.Is(err, errExpiryAborted) {
				return abort(i)
			}
			if errors.Is(err, errHolderNotEnded) || errors.Is(err, errDurableClose) {
				for _, rest := range plan.order[i:] {
					r.unfenceExec(rest)
				}
				clearExpiring()
				return State{}, err
			}
			r.unfenceExec(d)
		}
	}
	if r.opts.beforeExpiryNodeClose != nil {
		r.opts.beforeExpiryNodeClose(id)
	}
	st, err := r.closeOneExpiry(id, true, plan.incarnations[id], plan.checkAge, plan.rootLastActive)
	if err != nil {
		r.unfenceExec(id)
		clearExpiring()
		return st, err
	}
	r.releaseParent(id)
	return st, nil
}

// closeOneExpiry closes the exact snapshotted incarnation snap only if its
// quiet/expiry predicate still holds atomically under wmu+mu.
func (r *Runner) closeOneExpiry(id string, isRoot bool, snap expiryIncarnation, checkAge bool, rootLastActive time.Time) (State, error) {
	expected := snap.conv
	if expected == nil {
		return State{}, errExpiryAborted
	}
	var closedRec Record
	var closeErr error
	ok := expected.do(func() {
		if expected.closed || expected.parkedOut {
			closeErr = errExpiryAborted
			return
		}
		closed, err := expected.closeIfPermitted(func() bool {
			return expected.expiryPermittedLocked(isRoot, snap, checkAge, rootLastActive)
		})
		if err != nil {
			closeErr = err
			return
		}
		if !closed {
			closeErr = errExpiryAborted
			return
		}
		closedRec = expected.record()
	})
	if !ok {
		return State{}, errExpiryAborted
	}
	if closeErr != nil {
		return State{}, closeErr
	}
	st := expected.state()
	r.mu.Lock()
	if r.convs[id] == expected {
		delete(r.convs, id)
		r.liveOwners.CompareAndDelete(id, expected)
		r.closed[id] = closedRec
		if r.closedRows == nil {
			r.closedRows = map[string]State{}
		}
		r.closedRows[id] = st
	}
	r.mu.Unlock()
	return st, nil
}

func (r *Runner) closeCascade(id string, order []string) (State, error) {
	// New exec jobs are refused before anything closes; each conversation's
	// jobs end (whole process groups, no notice) before it closes, deepest
	// first. A job that does not stop keeps its conversation and every
	// ancestor of it open, and lifts their fences: nothing closes over a
	// job that may still run. A conversation whose close fails keeps
	// running, so it may own jobs again.
	r.fenceExec(order)
	for i, d := range order {
		if err := r.stopExec(d); err != nil {
			for _, rest := range order[i:] {
				r.unfenceExec(rest)
			}
			return State{}, fmt.Errorf("close %s: an exec job did not stop, so it and the sessions above it stay open: %w", d, err)
		}
		if d == id {
			break
		}
		if _, err := r.closeOne(d); err != nil {
			// A descendant whose failed retirement stop left its holder
			// running refuses the close of every conversation above it.
			if errors.Is(err, errHolderNotEnded) || errors.Is(err, errDurableClose) {
				for _, rest := range order[i:] {
					r.unfenceExec(rest)
				}
				return State{}, err
			}
			r.unfenceExec(d)
		}
	}
	st, err := r.closeOne(id)
	if err != nil {
		r.unfenceExec(id)
		return st, err
	}
	r.releaseParent(id)
	return st, nil
}

// releaseParent releases the done turn conversation id's conversation
// parent held for it (releaseHeld), once id closed.
func (r *Runner) releaseParent(id string) {
	if rec, ok := r.recordOf(id); ok && rec.Parent != nil && rec.Parent.Kind == ParentConversation {
		if parent := r.conv(rec.Parent.ReplyID); parent != nil {
			parent.do(parent.releaseHeld)
		}
	}
}

// closeOne closes conversation id alone.
func (r *Runner) closeOne(id string) (State, error) {
	var closedRec Record
	st, err := r.act(id, func(c *conv) error {
		if err := c.close(); err != nil {
			return err
		}
		closedRec = c.record()
		return nil
	})
	if err != nil {
		return st, err
	}
	r.mu.Lock()
	if c, ok := r.convs[id]; ok {
		delete(r.convs, id)
		r.liveOwners.CompareAndDelete(id, c)
		r.closed[id] = closedRec
		if r.closedRows == nil {
			r.closedRows = map[string]State{}
		}
		r.closedRows[id] = st
	}
	r.mu.Unlock()
	return st, nil
}

// SetDashboardControlAccess toggles a hosted lead conversation's persisted
// ws/daemon.* dashboard-control opt-in without stopping a running process.
// Revocation takes effect immediately on server-side MCP dispatch; newly
// enabling takes effect after the next process start.
func (r *Runner) SetDashboardControlAccess(id string, enabled bool) (State, error) {
	return r.act(id, func(c *conv) error {
		return c.setDashboardControlAccess(enabled)
	})
}

// SetSettings changes the conversation's model or effort (nil leaves one
// unchanged, "" returns it to the harness default). The change applies at
// the next turn boundary: in-stream when the backend has a control for it,
// else by restarting the process on the same backend session.
func (r *Runner) SetSettings(id string, model, effort *string) (State, error) {
	if model != nil && *model != "" && !modelPattern.MatchString(*model) {
		return State{}, badRequest("model %q must match %s", *model, modelPattern.String())
	}
	if effort != nil && *effort != "" && !effortPattern.MatchString(*effort) {
		return State{}, badRequest("effort %q must be a lowercase word", *effort)
	}
	return r.act(id, func(c *conv) error {
		if err := c.checkModelSetting(model); err != nil {
			return err
		}
		c.setSettings(model, effort)
		return nil
	})
}

// SetConvHistoryAccess updates a lead conversation's persisted
// conv_history_access flag immediately without stopping a running process.
func (r *Runner) SetConvHistoryAccess(id string, enabled bool) (State, error) {
	return r.act(id, func(c *conv) error {
		return c.setConvHistoryAccess(enabled)
	})
}

// ConvHistoryAccess reports the live persisted conv_history_access flag for
// replyID from memory (zero disk I/O). Non-leads and unknown IDs return false.
func (r *Runner) ConvHistoryAccess(id string) bool {
	rec, ok := r.RecordByReplyID(id)
	return ok && rec.Lead && rec.ConvHistoryAccess
}

// ReviveLead unhides an archived lead or reopens a closed lead. Live leads are
// a no-op; child/worker sessions and unknown/pruned IDs return a clear error.
func (r *Runner) ReviveLead(id string) error {
	r.mu.Lock()
	c, open := r.convs[id]
	rec, closed := r.closed[id]
	parked := r.parked[id]
	r.mu.Unlock()
	if !open && parked {
		if c = r.unpark(id); c != nil {
			open = true
		}
	}
	switch {
	case open:
		rRec := c.record()
		if !rRec.Lead {
			return badRequest("revive applies to lead sessions only")
		}
		if c.state().Hidden {
			_, err := r.SetHidden(id, false)
			return err
		}
		return nil
	case closed:
		if !rec.Lead {
			return badRequest("revive applies to lead sessions only")
		}
		_, err := r.Reopen(id)
		return err
	default:
		return errUnknown
	}
}

// open returns the open conversation id: errClosed for a closed one,
// errUnknown for one the runner never had.
func (r *Runner) open(id string) (*conv, error) {
	r.mu.Lock()
	c, open := r.convs[id]
	rec, closed := r.closed[id]
	parked := r.parked[id]
	r.mu.Unlock()
	if !open && parked {
		// A parked child is open: rehydrate it from its record.
		if c := r.unpark(id); c != nil {
			return c, nil
		}
		r.mu.Lock()
		c, open = r.convs[id]
		rec, closed = r.closed[id]
		r.mu.Unlock()
	}
	switch {
	case open:
		return c, nil
	case closed:
		return nil, endedError(rec)
	}
	return nil, errUnknown
}

// act runs fn in the conversation's loop and returns its state after.
func (r *Runner) act(id string, fn func(c *conv) error) (State, error) {
	c, err := r.open(id)
	if err != nil {
		return State{}, err
	}
	if !c.do(func() {
		if c.closed {
			err = endedError(c.record())
			return
		}
		err = fn(c)
	}) {
		if c.isParkedOut() {
			return r.act(id, fn)
		}
		return State{}, endedError(c.record())
	}
	if err != nil {
		return State{}, err
	}
	return c.state(), nil
}

// Conversation statuses a State reports.
