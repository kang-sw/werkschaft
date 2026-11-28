package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

const (
	companionTierBody = "small"
	companionTierWork = "medium"

	companionDeadlineTickInterval = 10 * time.Second
	companionClosedDelegationCap  = 200

	// companionBodyRotateTurns is how many settled companion-body turns run
	// on one harness session before the runtime rotates it (same harness,
	// ring checkpoint) at an idle boundary with the phone on the desk.
	companionBodyRotateTurns = 40
	// companionMessengerCloseFallback bounds how long the companion may keep
	// the phone after the Messenger closed and its close-notification turn
	// was delivered; then the lease is force-released.
	companionMessengerCloseFallback = 90 * time.Second
	// companionHandoffDigestMessages caps the Messenger messages a handoff
	// checkpoint carries.
	companionHandoffDigestMessages = 6
)

type companionLaunchSpec struct {
	Backend  string
	Model    string
	Effort   string
	Provider string
}

type companionDelegation struct {
	ID        string
	Brief     string
	CallerKey string
	Status    string // "queued" | "active" | "done" | "partial" | "blocked" | "failed"
}

// companionRuntime coordinates the Single-Embodiment Dual-Brain companion
// lifecycle ((1744)=a): lazy Tier-1 (companion-body) burst session, lazy Tier-2
// (companion-work) hidden work brain, in-memory delegation table + ordered FIFO
// queue, lossless brief/report delivery, turn-end/close failsafes, and sliding
// lease/delegation deadlines. All delegation state stays strictly in memory.
type companionRuntime struct {
	dir     string
	root    string
	agents  *wsagent.Runner
	daemon  func() *wsdashboard.Daemon
	logw    io.Writer
	nowFunc func() time.Time

	// Test seam: overrides harness/tier resolution when non-nil.
	resolveLaunch func(role string) (companionLaunchSpec, bool)
	// configOpts locates the config the companion tier settings and
	// agents.tier read; zero (the default homes) outside tests.
	configOpts wsconfig.Options
	// Test seam: overrides deadline tick interval when > 0.
	tickInterval time.Duration
	// Test seam: overrides companionMessengerCloseFallback when > 0.
	messengerCloseFallback time.Duration

	// Serialization mutexes ordered strictly before phone.mu and r.mu:
	//   bodyLaunchMu / workLaunchMu / inFlightMu -> phone.mu -> r.mu -> wsagent locks
	bodyLaunchMu sync.Mutex
	workLaunchMu sync.Mutex
	inFlightMu   sync.Mutex

	mu sync.Mutex

	bodyReplyID      string
	bodyWSKey        string
	bodyPendingTurns int
	bodyWakeUnreadID string
	bodyWaking       bool
	closedBodyKeys   map[string]struct{}
	// bodyTurns counts settled turns on the current body harness session
	// toward companionBodyRotateTurns.
	bodyTurns int
	// bodyHandoffs counts cross-harness/tier handoffs (observability/tests).
	bodyHandoffs int
	// bodyPrevID is the body that last closed or crashed; the next launch
	// carries its checkpoint so the conversation continues.
	bodyPrevID string

	workReplyID          string
	workWSKey            string
	workPrevID           string
	workHandoffs         int
	workSeededFromLegacy bool
	closedWorkKeys       map[string]struct{}

	delSeq      uint64
	active      *companionDelegation
	queue       []*companionDelegation
	closed      map[string]string
	closedOrder []string

	// Counters for observability/testing.
	bodyLaunches int
	workLaunches int

	stopOnce      sync.Once
	stopCh        chan struct{}
	closedRuntime bool
	wg            sync.WaitGroup
}

func newCompanionRuntime(dir, root string, agents *wsagent.Runner, daemon func() *wsdashboard.Daemon, logw io.Writer) *companionRuntime {
	if logw == nil {
		logw = io.Discard
	}
	return &companionRuntime{
		dir:            dir,
		root:           root,
		agents:         agents,
		daemon:         daemon,
		logw:           logw,
		closedBodyKeys: map[string]struct{}{},
		closedWorkKeys: map[string]struct{}{},
		closed:         map[string]string{},
		stopCh:         make(chan struct{}),
	}
}

func (r *companionRuntime) launchRoot(d *wsdashboard.Daemon) string {
	if r.root != "" {
		return r.root
	}
	if d != nil {
		if wsRoot := d.CompanionActiveRoot(); wsRoot != "" {
			return wsRoot
		}
	}
	return r.dir
}

func (r *companionRuntime) now() time.Time {
	if r.nowFunc != nil {
		return r.nowFunc()
	}
	return time.Now()
}

// spawn launches fn tracked by r.wg unless Close has already begun, ensuring
// background runtime tasks drain cleanly before Close returns.
func (r *companionRuntime) spawn(fn func()) {
	r.mu.Lock()
	if r.closedRuntime {
		r.mu.Unlock()
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		fn()
	}()
}

// Start begins the lightweight in-memory deadline monitor goroutine.
func (r *companionRuntime) Start() {
	interval := r.tickInterval
	if interval <= 0 {
		interval = companionDeadlineTickInterval
	}
	// Reattach any persistent companion-work Brain session a previous daemon
	// left open (closing duplicates) and gracefully close any surviving
	// pre-upgrade companion-body sessions.
	r.spawn(func() {
		if d := r.liveDaemon(); d != nil && r.agents != nil {
			r.workLaunchMu.Lock()
			r.reattachWorkLocked(d)
			r.workLaunchMu.Unlock()
			r.bodyLaunchMu.Lock()
			r.teardownLegacyBodiesLocked()
			r.bodyLaunchMu.Unlock()
		}
	})
	r.spawn(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.stopCh:
				return
			case <-ticker.C:
				r.checkDeadlines(r.now())
			}
		}
	})
}

// Close stops the deadline monitor goroutine and waits for all background
// runtime tasks to exit.
func (r *companionRuntime) Close() {
	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.closedRuntime = true
		r.mu.Unlock()
		close(r.stopCh)
	})
	r.wg.Wait()
}

// IsHolderAlive reports whether holder currently names a live Tier-1 session.
// Used by CompanionPhoneStore CAS acquire so a relaunched Tier-1 with a new
// key can acquire immediately after the old holder closed or crashed ((1739)).
func (r *companionRuntime) IsHolderAlive(holder string) bool {
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return false
	}
	r.mu.Lock()
	_, wasClosed := r.closedBodyKeys[holder]
	bodyKey := r.bodyWSKey
	bodyID := r.bodyReplyID
	r.mu.Unlock()
	if wasClosed {
		return false
	}
	if holder == bodyKey && bodyID != "" {
		return r.agents != nil && r.agents.IsConversationOpen(bodyID)
	}
	if r.agents != nil {
		replyID := wsmailbox.ReplyID(holder)
		if r.agents.IsConversationOpen(replyID) {
			return true
		}
		if _, known := r.agents.CompanionRecord(replyID); known {
			// Known to the runner but no longer live -> closed/ended.
			return false
		}
	}
	// Unknown synthetic key (not managed by wsagent): treat as live so direct
	// store-level CAS checks continue to enforce lease_held.
	return true
}

// OnUserWake is retained as a T0-safe callback hook; in the 3-tier
// architecture Messenger communicates directly with Brain (companion-work)
// via the embedded compact frame and Puppeteer observes Brain directly, so
// OnUserWake no longer launches a persistent companion-body chat session.
func (r *companionRuntime) OnUserWake(reason string) {
	_ = reason
}

func (r *companionRuntime) handleBodyWakeError(d *wsdashboard.Daemon, bodyKey, errText string) {
	fmt.Fprintf(r.logw, "companion runtime: %s\n", errText)
	phone := d.CompanionPhone()
	_, unavailable := phone.RecordWakeFailure()
	if bodyKey != "" {
		// Release without letting ForceRelease trigger a duplicate re-wake
		// before we inspect unavailable below.
		phone.ForceRelease(bodyKey)
	}
	if !unavailable && phone.UnreadAgentCount() > 0 && d.CompanionDirectorEnabled() {
		// Bounded retry via release-unread until CompanionWakeMaxRetries is hit.
		r.spawn(func() { r.OnUserWake("release-unread") })
	}
}

// OnCompanionActivity handles live turn activity from Tier-1, Tier-2, or
// children spawned by Tier-2 (renewing sliding deadlines and phone typing).
func (r *companionRuntime) OnCompanionActivity(role, replyID, wsKey string) {
	d := r.liveDaemon()
	if d == nil {
		return
	}
	phone := d.CompanionPhone()
	switch role {
	case mcp.CompanionRoleBody:
		phone.RenewLease(wsKey)
		if phone.PostureFor(wsKey) == wsdashboard.CompanionPosturePhone {
			phone.SetTyping(wsKey, true)
		}
	case mcp.CompanionRoleWork:
		phone.RenewDelegationLease()
	}
}

// OnCompanionTurnEnd handles turn settlement for companion-body and
// companion-work. Must not block on the runner loop.
func (r *companionRuntime) OnCompanionTurnEnd(role, replyID, wsKey, status, errText string) {
	r.spawn(func() { r.handleCompanionTurnEnd(role, replyID, wsKey, status, errText) })
}

func (r *companionRuntime) handleCompanionTurnEnd(role, replyID, wsKey, status, errText string) {
	d := r.liveDaemon()
	if d == nil {
		return
	}
	switch role {
	case mcp.CompanionRoleBody:
		var wakeUnreadID string
		var matchedCurrent bool
		var stillPending bool
		r.mu.Lock()
		if replyID != "" && replyID == r.bodyReplyID {
			matchedCurrent = true
			if r.bodyPendingTurns > 0 {
				r.bodyPendingTurns--
			}
			stillPending = r.bodyPendingTurns > 0
			wakeUnreadID = r.bodyWakeUnreadID
			r.bodyWakeUnreadID = ""
		}
		r.mu.Unlock()
		if !matchedCurrent {
			return
		}
		phone := d.CompanionPhone()
		var failed bool
		if status == wsagent.TurnFailed {
			fmt.Fprintf(r.logw, "companion runtime: tier-1 turn failed (%s): %s\n", replyID, errText)
			failed = true
		} else if wakeUnreadID != "" && phone.OldestUnreadID() == wakeUnreadID {
			// A successful turn that never drained the unread message that woke
			// it made zero progress; count it toward the (1749) retry bound so
			// release-unread cannot re-wake indefinitely.
			fmt.Fprintf(r.logw, "companion runtime: tier-1 turn ended without reading unread message %s\n", wakeUnreadID)
			failed = true
		}
		if failed {
			phone.RecordWakeFailure()
		}
		// Turn-end failsafe: CompanionForceRelease resets the burst counter and
		// releases any unclosed Embodiment Lease (settling to laptop_work if a
		// delegation is in flight, and triggering unread re-wake if messages
		// remain and not unavailable). Even when Tier-1 already released its
		// lease cleanly via phone_putdown before TurnEnd settled, a user message
		// may have arrived while the turn was running (suppressed to avoid
		// preempting the turn); trigger an unread re-wake here if unread
		// messages remain and no queued turn is pending. While the Messenger
		// is open, CompanionReleaseOnTurnEnd keeps a held phone in hand after
		// a successful settle; a failed or zero-progress turn always releases.
		var released bool
		if failed {
			released = d.CompanionForceRelease(wsKey)
		} else {
			released = d.CompanionReleaseOnTurnEnd(wsKey)
		}
		if !released && !stillPending && phone.UnreadAgentCount() > 0 && !phone.Snapshot().Unavailable {
			r.spawn(func() { r.OnUserWake("release-unread") })
			return
		}
		if !stillPending {
			r.maybeRotateBody(d, replyID, wsKey)
		}
	case mcp.CompanionRoleWork:
		// A Tier-2 turn end without ws/delegation.report leaves the delegation
		// in flight (Tier-2 may be waiting on spawned engineering agents).
		// Simply renew its sliding delegation deadline ((1748)).
		d.CompanionPhone().RenewDelegationLease()
	}
}

// OnCompanionClose handles conversation close or unexpected crash for
// companion-body and companion-work. Must not block on the runner loop.
func (r *companionRuntime) OnCompanionClose(role, replyID, wsKey, reason string) {
	r.spawn(func() { r.handleCompanionClose(role, replyID, wsKey, reason) })
}

func (r *companionRuntime) handleCompanionClose(role, replyID, wsKey, reason string) {
	d := r.liveDaemon()
	switch role {
	case mcp.CompanionRoleBody:
		var matchedCurrent bool
		var firstCrash bool
		r.mu.Lock()
		if reason == "close" && wsKey != "" {
			r.closedBodyKeys[wsKey] = struct{}{}
		}
		if replyID != "" && replyID == r.bodyReplyID {
			matchedCurrent = true
			r.bodyPrevID = replyID
			r.bodyReplyID = ""
			r.bodyWSKey = ""
			r.bodyPendingTurns = 0
			r.bodyWakeUnreadID = ""
			if reason == "crash" {
				firstCrash = true
				if wsKey != "" {
					r.closedBodyKeys[wsKey] = struct{}{}
				}
			}
		}
		r.mu.Unlock()
		if d != nil && matchedCurrent {
			if firstCrash {
				d.CompanionPhone().RecordWakeFailure()
			}
			released := d.CompanionForceRelease(wsKey)
			if !released && d.CompanionPhone().UnreadAgentCount() > 0 && !d.CompanionPhone().Snapshot().Unavailable {
				r.spawn(func() { r.OnUserWake("release-unread") })
			}
		}
	case mcp.CompanionRoleWork:
		var failedID string
		r.mu.Lock()
		if wsKey != "" && (reason == "close" || reason == "crash") {
			r.closedWorkKeys[wsKey] = struct{}{}
		}
		// Fail the active delegation only when the closing session is the
		// current Tier-2 session; duplicate/stale close events for an earlier
		// session must never fail a newer healthy delegation.
		if replyID != "" && replyID == r.workReplyID {
			r.workPrevID = replyID
			r.workReplyID = ""
			r.workWSKey = ""
			if r.active != nil {
				failedID = r.active.ID
			}
		}
		r.mu.Unlock()
		if failedID != "" {
			r.failActiveDelegation(failedID, fmt.Sprintf("work brain %s before reporting", reason))
		}
	}
}

// syncDelegationInFlight reconciles CompanionPhoneStore's delegationInFlight
// flag with the runtime's current active/queue state under inFlightMu so
// concurrent report/fail/delegate calls can never land a stale value out of
// order (and without holding r.mu across phone.mu).
func (r *companionRuntime) syncDelegationInFlight(d *wsdashboard.Daemon) {
	if d == nil {
		return
	}
	r.inFlightMu.Lock()
	defer r.inFlightMu.Unlock()
	r.mu.Lock()
	inFlight := r.active != nil || len(r.queue) > 0
	r.mu.Unlock()
	d.CompanionPhone().SetDelegationInFlight(inFlight)
}

// CompanionDelegateWork implements mcp.CompanionDelegationHost for Tier-1's
// ws/companion.delegate_work(brief). Non-blocking; preserves brief verbatim.
func (r *companionRuntime) CompanionDelegateWork(callerKey, brief string) (mcp.CompanionDelegationAccepted, error) {
	if strings.TrimSpace(brief) == "" {
		return mcp.CompanionDelegationAccepted{}, fmt.Errorf("brief must be non-empty")
	}
	r.mu.Lock()
	r.delSeq++
	id := "del-" + strconv.FormatUint(r.delSeq, 10)
	del := &companionDelegation{
		ID:        id,
		Brief:     brief,
		CallerKey: strings.TrimSpace(callerKey),
		Status:    "queued",
	}
	var toDispatch *companionDelegation
	if r.active == nil {
		del.Status = "active"
		r.active = del
		toDispatch = del
	} else {
		r.queue = append(r.queue, del)
	}
	r.mu.Unlock()

	d := r.liveDaemon()
	if d != nil {
		d.ObserveCompanionMood(wsdashboard.CompanionMoodEventDelegationStart)
	}
	r.syncDelegationInFlight(d)
	if toDispatch != nil {
		r.spawn(func() { r.dispatchToWorkBrain(toDispatch) })
	}
	return mcp.CompanionDelegationAccepted{
		DelegationID: id,
		Status:       "accepted",
	}, nil
}

// CompanionDelegationReport implements mcp.CompanionDelegationHost for Tier-2's
// ws/delegation.report(delegation_id, status, result, artifacts).
func (r *companionRuntime) CompanionDelegationReport(callerKey, delegationID, status, result string, artifacts []string) (mcp.CompanionDelegationReportAck, error) {
	delegationID = strings.TrimSpace(delegationID)
	status = strings.TrimSpace(status)
	switch status {
	case "done", "partial", "blocked":
	default:
		return mcp.CompanionDelegationReportAck{}, fmt.Errorf("invalid delegation status %q (want done, partial, or blocked)", status)
	}

	var next *companionDelegation
	var workWSKey string
	r.mu.Lock()
	if r.active == nil || r.active.ID != delegationID {
		prevStatus, wasClosed := r.closed[delegationID]
		r.mu.Unlock()
		if wasClosed {
			return mcp.CompanionDelegationReportAck{}, fmt.Errorf("delegation %s is already closed (status: %s)", delegationID, prevStatus)
		}
		return mcp.CompanionDelegationReportAck{}, fmt.Errorf("delegation %s is not active", delegationID)
	}
	r.recordClosedLocked(delegationID, status)
	r.active = nil
	if len(r.queue) > 0 {
		next = r.queue[0]
		r.queue = r.queue[1:]
		next.Status = "active"
		r.active = next
	}
	workWSKey = r.workWSKey
	r.mu.Unlock()

	d := r.liveDaemon()
	if d != nil {
		switch status {
		case "done":
			d.ObserveCompanionMood(wsdashboard.CompanionMoodEventDelegationDone)
		case "blocked":
			d.ObserveCompanionMood(wsdashboard.CompanionMoodEventDelegationFailed)
		default:
			d.ObserveCompanionMood(wsdashboard.CompanionMoodEventTurnEnd)
		}
	}
	r.syncDelegationInFlight(d)

	// Copy artifacts slice so caller mutations cannot race async delivery.
	artCopy := append([]string(nil), artifacts...)
	r.spawn(func() { r.deliverReportToBody(delegationID, status, result, artCopy) })

	if next != nil {
		r.spawn(func() { r.dispatchToWorkBrain(next) })
	} else if workWSKey != "" && r.agents != nil {
		// Queue Tier-2 idle compaction at the next hosted idle boundary ((1748)).
		r.spawn(func() { r.compactWorkBrainIdle(workWSKey) })
	}

	return mcp.CompanionDelegationReportAck{
		DelegationID: delegationID,
		Status:       status,
	}, nil
}

func (r *companionRuntime) recordClosedLocked(id, status string) {
	if r.closed == nil {
		r.closed = map[string]string{}
	}
	if _, exists := r.closed[id]; !exists {
		r.closedOrder = append(r.closedOrder, id)
	}
	r.closed[id] = status
	for len(r.closedOrder) > companionClosedDelegationCap {
		oldest := r.closedOrder[0]
		r.closedOrder = r.closedOrder[1:]
		delete(r.closed, oldest)
	}
}

func (r *companionRuntime) dispatchToWorkBrain(del *companionDelegation) {
	if del == nil {
		return
	}
	if r.agents == nil {
		r.failActiveDelegation(del.ID, "agent runner unavailable")
		return
	}
	workID, err := r.ensureWorkSession(r.liveDaemon())
	if err != nil {
		r.failActiveDelegation(del.ID, "launch companion-work: "+err.Error())
		return
	}
	envelope := formatDelegationBriefEnvelope(del.ID, del.Brief)
	if _, err := r.agents.Deliver(workID, wsagent.Delivery{Text: envelope}); err != nil {
		r.failActiveDelegation(del.ID, "deliver delegation brief: "+err.Error())
		return
	}
}

func (r *companionRuntime) failActiveDelegation(delegationID, reason string) {
	var next *companionDelegation
	r.mu.Lock()
	if r.active == nil || r.active.ID != delegationID {
		r.mu.Unlock()
		return
	}
	r.recordClosedLocked(delegationID, "failed")
	r.active = nil
	if len(r.queue) > 0 {
		next = r.queue[0]
		r.queue = r.queue[1:]
		next.Status = "active"
		r.active = next
	}
	r.mu.Unlock()

	d := r.liveDaemon()
	if d != nil {
		d.ObserveCompanionMood(wsdashboard.CompanionMoodEventDelegationFailed)
	}
	r.syncDelegationInFlight(d)
	r.spawn(func() { r.deliverReportToBody(delegationID, "failed", reason, nil) })
	if next != nil {
		r.spawn(func() { r.dispatchToWorkBrain(next) })
	}
}

func (r *companionRuntime) deliverReportToBody(delegationID, status, result string, artifacts []string) {
	d := r.liveDaemon()
	if d == nil || r.agents == nil {
		return
	}
	bodyID, bodyKey, err := r.ensureBodySession(d)
	if err != nil {
		fmt.Fprintf(r.logw, "companion runtime: wake tier-1 for delegation %s report: %v\n", delegationID, err)
		return
	}
	// Acquire lease in laptop posture when free (does not preempt if Tier-1 is
	// mid-turn or holding the phone — Runner.Deliver queues behind an active turn).
	if _, acquired := d.CompanionPhone().AcquireOnWake(bodyKey); acquired {
		d.CompanionResetBurst(bodyKey)
	}
	wakeCtx := d.BuildCompanionWakeEnvelope(context.Background(), wsdashboard.CompanionWakeEnvelopeInput{
		Trigger:    "delegation-report",
		ProjectKey: companionProjectKey(r.launchRoot(d)),
		Hint:       status,
	})
	envelope := wakeCtx + "\n\n" + formatDelegationReportEnvelope(delegationID, status, result, artifacts)
	r.mu.Lock()
	if r.bodyReplyID == bodyID {
		r.bodyPendingTurns++
	}
	r.mu.Unlock()
	if _, err := r.agents.Deliver(bodyID, wsagent.Delivery{Text: envelope}); err != nil {
		r.mu.Lock()
		if r.bodyReplyID == bodyID && r.bodyPendingTurns > 0 {
			r.bodyPendingTurns--
		}
		r.mu.Unlock()
		fmt.Fprintf(r.logw, "companion runtime: deliver delegation %s report to tier-1: %v\n", delegationID, err)
	}
}

func (r *companionRuntime) compactWorkBrainIdle(workWSKey string) {
	if r.agents == nil || workWSKey == "" {
		return
	}
	res := r.agents.CompactCompanionIdle(workWSKey)
	if res.Status != "queued" && res.Reason != "" {
		fmt.Fprintf(r.logw, "companion runtime: tier-2 idle compact (%s): %s\n", res.Status, res.Reason)
	}
}

func (r *companionRuntime) checkDeadlines(now time.Time) {
	d := r.liveDaemon()
	if d == nil {
		return
	}
	r.mu.Lock()
	workID := r.workReplyID
	var activeID string
	if r.active != nil {
		activeID = r.active.ID
	}
	r.mu.Unlock()

	hasChildren := workID != "" && r.agents != nil && r.agents.HasLiveChildren(workID)
	// The daemon wrapper also ages out stale Messenger presence and puts the
	// stage's phone down when an expired lease held it.
	leaseExpired, delegationExpired, hasUnread := d.CompanionExpireIfPastDeadline(now, hasChildren)
	if delegationExpired && activeID != "" {
		r.failActiveDelegation(activeID, "in-flight delegation deadline expired")
		return
	}
	if leaseExpired && hasUnread {
		r.spawn(func() { r.OnUserWake("release-unread") })
	}
}

func (r *companionRuntime) ensureBodySession(d *wsdashboard.Daemon) (replyID, wsKey string, err error) {
	r.bodyLaunchMu.Lock()
	defer r.bodyLaunchMu.Unlock()

	r.mu.Lock()
	existingID, existingKey := r.bodyReplyID, r.bodyWSKey
	idle := r.bodyPendingTurns == 0
	r.mu.Unlock()
	if existingID == "" {
		// A previous daemon's body (or one this runtime lost track of) is
		// reattached before anything new is launched.
		existingID, existingKey = r.reattachBodyLocked()
	}

	spec, ok := r.pickLaunchSpec(mcp.CompanionRoleBody)
	if existingID != "" && existingKey != "" && r.agents.IsConversationOpen(existingID) {
		rec, known := r.agents.CompanionRecord(existingID)
		if !ok || !known || !idle || companionSpecMatches(rec, spec) {
			return existingID, existingKey, nil
		}
		// The configured body tier/harness changed: hand the conversation
		// over to a session on the new spec, at an idle boundary only.
		fmt.Fprintf(r.logw, "companion runtime: companion-body spec changed (%s/%s -> %s/%s); handing off\n", rec.Backend, rec.Model, spec.Backend, spec.Model)
	}
	if !ok {
		return "", "", fmt.Errorf("no available harness backend for companion-body")
	}
	if existingID == "" {
		r.mu.Lock()
		existingID = r.bodyPrevID
		r.mu.Unlock()
	}
	return r.launchBodyLocked(d, spec, existingID, existingKey)
}

// companionSpecMatches reports whether rec runs on spec. An empty spec field
// (harness default) matches anything.
func companionSpecMatches(rec wsagent.Record, spec companionLaunchSpec) bool {
	eq := func(have, want string) bool { return want == "" || have == want }
	return eq(rec.Backend, spec.Backend) && eq(rec.Model, spec.Model) && eq(rec.Effort, spec.Effort) && eq(rec.Provider, spec.Provider)
}

// reattachBodyLocked adopts the newest open companion-body record that is
// not known closed as the body session, and closes every other open
// companion-body record (stale duplicates from restarts or crashes). It
// returns the adopted ids, or empty strings when there is none. The caller
// holds bodyLaunchMu.
func (r *companionRuntime) reattachBodyLocked() (string, string) {
	if r.agents == nil {
		return "", ""
	}
	recs := r.agents.CompanionSessions(mcp.CompanionRoleBody)
	r.mu.Lock()
	currentID, currentKey := r.bodyReplyID, r.bodyWSKey
	chosenID, chosenKey := currentID, currentKey
	var stale []wsagent.Record
	for _, rec := range recs {
		if rec.ReplyID == currentID {
			continue
		}
		_, closedKey := r.closedBodyKeys[rec.WSKey]
		if chosenID == "" && rec.WSKey != "" && !closedKey {
			chosenID, chosenKey = rec.ReplyID, rec.WSKey
			continue
		}
		stale = append(stale, rec)
	}
	for _, rec := range stale {
		if rec.WSKey != "" {
			r.closedBodyKeys[rec.WSKey] = struct{}{}
		}
	}
	adopted := currentID == "" && chosenID != ""
	if adopted {
		r.bodyReplyID, r.bodyWSKey = chosenID, chosenKey
		r.bodyPendingTurns, r.bodyWakeUnreadID, r.bodyTurns = 0, "", 0
	}
	r.mu.Unlock()
	if adopted {
		fmt.Fprintf(r.logw, "companion runtime: reattached companion-body %s\n", chosenID)
	}
	for _, rec := range stale {
		if _, err := r.agents.CloseConversation(rec.ReplyID); err != nil {
			fmt.Fprintf(r.logw, "companion runtime: close stale companion-body %s: %v\n", rec.ReplyID, err)
		}
	}
	return chosenID, chosenKey
}

// launchBodyLocked starts a companion-body session on spec. When prevID
// names an earlier body (a tier/harness change, or one whose harness died),
// the new session's launch prompt carries a checkpoint of prevID's dialog
// plus the Messenger digest, the body ids swap atomically, a phone lease
// held by prevKey moves to the new key, and prevID is closed. The caller
// holds bodyLaunchMu.
func (r *companionRuntime) launchBodyLocked(d *wsdashboard.Daemon, spec companionLaunchSpec, prevID, prevKey string) (string, string, error) {
	var sheet wsdashboard.CharacteristicSheet
	var lang string
	var digest string
	if d != nil {
		sheet, lang, _ = d.CompanionActiveSoulLaunch()
		digest = d.CompanionMessengerDigest(companionHandoffDigestMessages)
	}
	prompt := wsdashboard.BuildCompanionBodyLaunchPrompt(sheet, lang)
	if prevID != "" {
		if checkpoint, err := r.agents.CompanionHandoffCheckpoint(prevID, digest); err == nil {
			prompt += "\n\n" + companionHandoffPreamble + "\n\n" + checkpoint
		} else {
			fmt.Fprintf(r.logw, "companion runtime: handoff checkpoint from %s: %v\n", prevID, err)
		}
	}
	st, err := r.agents.CreateLead(spec.Backend, r.launchRoot(d), wsagent.LeadOptions{
		CompanionRole:         mcp.CompanionRoleBody,
		CompanionSystemPrompt: prompt,
		Model:                 spec.Model,
		Provider:              spec.Provider,
		Effort:                spec.Effort,
	})
	if err != nil {
		return "", "", err
	}
	rec, ok := r.agents.CompanionRecord(st.ID)
	if !ok || rec.WSKey == "" {
		return "", "", fmt.Errorf("companion-body session %s has no minted key", st.ID)
	}
	r.mu.Lock()
	r.bodyReplyID = st.ID
	r.bodyWSKey = rec.WSKey
	r.bodyPendingTurns, r.bodyWakeUnreadID, r.bodyTurns = 0, "", 0
	r.bodyPrevID = ""
	r.bodyLaunches++
	if prevKey != "" {
		r.closedBodyKeys[prevKey] = struct{}{}
	}
	if prevID != "" {
		r.bodyHandoffs++
	}
	r.mu.Unlock()
	if prevKey != "" && d != nil {
		d.CompanionPhone().MigrateLease(prevKey, rec.WSKey)
	}
	if prevID != "" && r.agents.IsConversationOpen(prevID) {
		if _, err := r.agents.CloseConversation(prevID); err != nil {
			fmt.Fprintf(r.logw, "companion runtime: close handed-off companion-body %s: %v\n", prevID, err)
		}
	}
	return st.ID, rec.WSKey, nil
}

// maybeRotateBody counts a settled body turn and, every
// companionBodyRotateTurns turns, rotates the body's harness session (same
// harness, ring checkpoint) while the phone is on the desk.
func (r *companionRuntime) maybeRotateBody(d *wsdashboard.Daemon, replyID, wsKey string) {
	r.mu.Lock()
	if replyID != r.bodyReplyID {
		r.mu.Unlock()
		return
	}
	r.bodyTurns++
	due := r.bodyTurns >= companionBodyRotateTurns && r.bodyPendingTurns == 0
	r.mu.Unlock()
	if !due || d.CompanionPosture(wsKey) == wsdashboard.CompanionPosturePhone {
		return
	}
	res := r.agents.RotateCompanion(wsKey)
	if res.Status == "queued" {
		r.mu.Lock()
		if replyID == r.bodyReplyID {
			r.bodyTurns = 0
		}
		r.mu.Unlock()
		return
	}
	fmt.Fprintf(r.logw, "companion runtime: rotate companion-body (%s): %s\n", res.Status, res.Reason)
}

// OnMessengerClosed is the daemon's debounced notice that the last Messenger
// closed while holder had the phone in hand. Must not block the caller.
func (r *companionRuntime) OnMessengerClosed(holder string) {
	r.spawn(func() { r.handleMessengerClosed(holder) })
}

// handleMessengerClosed gives companion-body exactly one turn to wrap up
// and put the phone down, with a fallback that force-releases the lease if
// the turn never does. A body already mid-turn needs no extra turn: its turn
// end releases now that the Messenger is closed.
func (r *companionRuntime) handleMessengerClosed(holder string) {
	d := r.liveDaemon()
	if d == nil || r.agents == nil {
		return
	}
	r.mu.Lock()
	bodyID, bodyKey := r.bodyReplyID, r.bodyWSKey
	busy := r.bodyPendingTurns > 0 || r.bodyWaking
	r.mu.Unlock()
	if bodyID == "" || holder != bodyKey || busy {
		return
	}
	if !d.CompanionDirectorEnabled() {
		d.CompanionForceRelease(bodyKey)
		return
	}
	r.mu.Lock()
	if r.bodyReplyID == bodyID {
		r.bodyPendingTurns++
	}
	r.mu.Unlock()
	if _, err := r.agents.Deliver(bodyID, wsagent.Delivery{Text: formatBodyMessengerClosedPrompt()}); err != nil {
		r.mu.Lock()
		if r.bodyReplyID == bodyID && r.bodyPendingTurns > 0 {
			r.bodyPendingTurns--
		}
		r.mu.Unlock()
		fmt.Fprintf(r.logw, "companion runtime: deliver messenger-closed notice: %v\n", err)
		d.CompanionForceRelease(bodyKey)
		return
	}
	fallback := r.messengerCloseFallback
	if fallback <= 0 {
		fallback = companionMessengerCloseFallback
	}
	r.spawn(func() {
		t := time.NewTimer(fallback)
		defer t.Stop()
		select {
		case <-r.stopCh:
			return
		case <-t.C:
		}
		snap := d.CompanionPhone().Snapshot()
		if !d.CompanionMessengerOpen() && snap.Posture == wsdashboard.CompanionPosturePhone && snap.LeaseOwner == bodyKey {
			fmt.Fprintf(r.logw, "companion runtime: phone still held %s after messenger closed; releasing\n", fallback)
			d.CompanionForceRelease(bodyKey)
		}
	})
}

// companionHandoffPreamble introduces the checkpoint a replacement
// companion-body session inherits from the session it replaces.
const companionHandoffPreamble = "[companion handoff]\nYou are continuing as the same companion after a session handoff (a model/harness change or a restart). The checkpoint below carries the previous session's conversation; treat it as your own memory and continue naturally without announcing the handoff."

// companionMessengerOpenNote rides a user wake while the Messenger is open.
const companionMessengerOpenNote = "[messenger open] The user has the messenger open: after replying, keep holding the smartphone and end your turn without ws/companion.phone_putdown (unless switching to work)."

func formatBodyMessengerClosedPrompt() string {
	return "[messenger closed]\nThe user closed the messenger while you are holding the smartphone. " +
		"Wrap up: call ws/companion.phone_putdown (next=\"ambient\", or next=\"work\" if a delegation is running) now. Do not send another messenger reply unless something is genuinely unfinished."
}

// teardownLegacyBodiesLocked gracefully closes any surviving pre-upgrade
// persistent companion-body hosted sessions left open by an earlier daemon.
// The caller holds bodyLaunchMu.
func (r *companionRuntime) teardownLegacyBodiesLocked() {
	if r.agents == nil {
		return
	}
	recs := r.agents.CompanionSessions(mcp.CompanionRoleBody)
	if len(recs) == 0 {
		return
	}
	r.mu.Lock()
	for _, rec := range recs {
		if rec.WSKey != "" {
			r.closedBodyKeys[rec.WSKey] = struct{}{}
		}
	}
	r.bodyReplyID, r.bodyWSKey, r.bodyPendingTurns, r.bodyWakeUnreadID, r.bodyTurns = "", "", 0, "", 0
	r.mu.Unlock()
	for _, rec := range recs {
		if _, err := r.agents.CloseConversation(rec.ReplyID); err != nil {
			fmt.Fprintf(r.logw, "companion runtime: close legacy companion-body %s: %v\n", rec.ReplyID, err)
		}
	}
}

// reattachWorkLocked adopts the newest open companion-work Brain session that
// is not known closed, closes any stale duplicates, and registers Brain's
// replyID on the daemon so Puppeteer immediately observes it. The caller holds
// workLaunchMu.
func (r *companionRuntime) reattachWorkLocked(d *wsdashboard.Daemon) (string, string) {
	if r.agents == nil {
		return "", ""
	}
	recs := r.agents.CompanionSessions(mcp.CompanionRoleWork)
	r.mu.Lock()
	currentID, currentKey := r.workReplyID, r.workWSKey
	chosenID, chosenKey := currentID, currentKey
	var stale []wsagent.Record
	for _, rec := range recs {
		if rec.ReplyID == currentID {
			continue
		}
		_, closedKey := r.closedWorkKeys[rec.WSKey]
		if chosenID == "" && rec.WSKey != "" && !closedKey {
			chosenID, chosenKey = rec.ReplyID, rec.WSKey
			continue
		}
		stale = append(stale, rec)
	}
	for _, rec := range stale {
		if rec.WSKey != "" {
			r.closedWorkKeys[rec.WSKey] = struct{}{}
		}
	}
	adopted := currentID == "" && chosenID != ""
	if adopted {
		r.workReplyID, r.workWSKey = chosenID, chosenKey
	}
	r.mu.Unlock()
	if adopted {
		fmt.Fprintf(r.logw, "companion runtime: reattached companion-work brain %s\n", chosenID)
	}
	if chosenID != "" && d != nil {
		d.SetCompanionBrainReplyID(chosenID)
	}
	for _, rec := range stale {
		if _, err := r.agents.CloseConversation(rec.ReplyID); err != nil {
			fmt.Fprintf(r.logw, "companion runtime: close stale companion-work %s: %v\n", rec.ReplyID, err)
		}
	}
	return chosenID, chosenKey
}

// BrainEndpoint serves GET /api/companion/brain by lazily ensuring or
// reattaching the Mobile Full Lead (Brain) session.
func (r *companionRuntime) BrainEndpoint() (wsdashboard.CompanionBrainInfo, error) {
	return r.EnsureBrainSession(r.liveDaemon())
}

// EnsureBrainSession lazily attaches, launches, or cleanly hands off the
// persistent Mobile Full Lead (Brain, companion-work) hosted session.
func (r *companionRuntime) EnsureBrainSession(d *wsdashboard.Daemon) (wsdashboard.CompanionBrainInfo, error) {
	if r.agents == nil {
		return wsdashboard.CompanionBrainInfo{Ready: false}, fmt.Errorf("agent runner unavailable")
	}
	r.workLaunchMu.Lock()
	defer r.workLaunchMu.Unlock()

	r.mu.Lock()
	existingID, existingKey := r.workReplyID, r.workWSKey
	seeded := r.workSeededFromLegacy
	r.mu.Unlock()
	if existingID == "" {
		existingID, existingKey = r.reattachWorkLocked(d)
	}

	personaName := "Companion"
	if d != nil {
		if name := strings.TrimSpace(d.CompanionResolvedPersona().Sheet.Name); name != "" {
			personaName = name
		}
	}

	spec, ok := r.pickLaunchSpec(mcp.CompanionRoleWork)
	tierLabel := companionTierWork
	if r.resolveLaunch == nil && r.agents != nil {
		if res := r.resolveRole(mcp.CompanionRoleWork); strings.TrimSpace(res.Tier) != "" {
			tierLabel = strings.TrimSpace(res.Tier)
		} else if strings.TrimSpace(res.Configured) != "" {
			tierLabel = strings.TrimSpace(res.Configured)
		}
	}

	if existingID != "" && existingKey != "" && r.agents.IsConversationOpen(existingID) {
		rec, known := r.agents.CompanionRecord(existingID)
		if !ok || !known || companionSpecMatches(rec, spec) {
			if d != nil {
				d.SetCompanionBrainReplyID(existingID)
			}
			return wsdashboard.CompanionBrainInfo{
				ReplyID:          existingID,
				WSKey:            existingKey,
				Harness:          rec.Backend,
				Model:            rec.Model,
				Tier:             tierLabel,
				PersonaName:      personaName,
				Ready:            true,
				SeededFromLegacy: seeded,
			}, nil
		}
		fmt.Fprintf(r.logw, "companion runtime: companion-work brain spec changed (%s/%s -> %s/%s); handing off\n", rec.Backend, rec.Model, spec.Backend, spec.Model)
	}
	if !ok {
		return wsdashboard.CompanionBrainInfo{
			PersonaName: personaName,
			Tier:        tierLabel,
			Ready:       false,
		}, fmt.Errorf("no available harness backend for companion-work")
	}
	if existingID == "" {
		r.mu.Lock()
		existingID = r.workPrevID
		r.mu.Unlock()
	}
	return r.launchWorkLocked(d, spec, tierLabel, personaName, existingID, existingKey)
}

// launchWorkLocked launches a new Mobile Full Lead (Brain, companion-work)
// session on spec. When prevID names an earlier Brain (cross-harness/model/
// effort handoff or crash recovery), it captures CompanionHandoffCheckpoint
// from prevID, updates workReplyID/workWSKey, broadcasts the handoff via
// SetCompanionBrainReplyID, and closes prevID. On a brand-new Brain with no
// prior Brain checkpoint, it performs a single read-only check of legacy
// companion-messenger/seg-*.jsonl to preserve recent conversational continuity.
// The caller holds workLaunchMu.
func (r *companionRuntime) launchWorkLocked(d *wsdashboard.Daemon, spec companionLaunchSpec, tierLabel, personaName, prevID, prevKey string) (wsdashboard.CompanionBrainInfo, error) {
	var sheet wsdashboard.CharacteristicSheet
	var lang string
	var legacySeed string
	if d != nil {
		sheet, lang, _ = d.CompanionActiveSoulLaunch()
		if name := strings.TrimSpace(sheet.Name); name != "" {
			personaName = name
		}
		if prevID == "" {
			legacySeed = d.CompanionLegacyMessengerSeed(10, 4096)
		}
	}
	seeded := legacySeed != ""
	prompt := wsdashboard.BuildCompanionBrainSystemPrompt(sheet, lang, legacySeed)
	if prevID != "" {
		if checkpoint, err := r.agents.CompanionHandoffCheckpoint(prevID, ""); err == nil {
			prompt += "\n\n" + companionHandoffPreamble + "\n\n" + checkpoint
		} else {
			fmt.Fprintf(r.logw, "companion runtime: handoff checkpoint from companion-work %s: %v\n", prevID, err)
		}
	}
	st, err := r.agents.CreateLead(spec.Backend, r.launchRoot(d), wsagent.LeadOptions{
		CompanionRole:         mcp.CompanionRoleWork,
		CompanionSystemPrompt: prompt,
		Model:                 spec.Model,
		Provider:              spec.Provider,
		Effort:                spec.Effort,
	})
	if err != nil {
		return wsdashboard.CompanionBrainInfo{PersonaName: personaName, Tier: tierLabel, Ready: false}, err
	}
	rec, ok := r.agents.CompanionRecord(st.ID)
	if !ok || rec.WSKey == "" {
		return wsdashboard.CompanionBrainInfo{PersonaName: personaName, Tier: tierLabel, Ready: false}, fmt.Errorf("companion-work session %s has no minted key", st.ID)
	}
	r.mu.Lock()
	r.workReplyID = st.ID
	r.workWSKey = rec.WSKey
	r.workPrevID = ""
	r.workSeededFromLegacy = seeded
	r.workLaunches++
	if prevKey != "" {
		r.closedWorkKeys[prevKey] = struct{}{}
	}
	if prevID != "" {
		r.workHandoffs++
	}
	r.mu.Unlock()
	if d != nil {
		d.SetCompanionBrainReplyID(st.ID)
	}
	if prevID != "" && r.agents.IsConversationOpen(prevID) {
		if _, err := r.agents.CloseConversation(prevID); err != nil {
			fmt.Fprintf(r.logw, "companion runtime: close handed-off companion-work %s: %v\n", prevID, err)
		}
	}
	return wsdashboard.CompanionBrainInfo{
		ReplyID:          st.ID,
		WSKey:            rec.WSKey,
		Harness:          rec.Backend,
		Model:            rec.Model,
		Tier:             tierLabel,
		PersonaName:      personaName,
		Ready:            true,
		SeededFromLegacy: seeded,
	}, nil
}

func (r *companionRuntime) ensureWorkSession(d *wsdashboard.Daemon) (string, error) {
	info, err := r.EnsureBrainSession(d)
	if err != nil {
		return "", err
	}
	return info.ReplyID, nil
}

func (r *companionRuntime) liveDaemon() *wsdashboard.Daemon {
	if r.daemon == nil {
		return nil
	}
	return r.daemon()
}

func (r *companionRuntime) launchCounts() (body, work int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bodyLaunches, r.workLaunches
}

func (r *companionRuntime) activeDelegationID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return ""
	}
	return r.active.ID
}

func formatBodyUserWakePrompt(unread int, reason string) string {
	return fmt.Sprintf(
		"[companion phone buzz]\n%d unread user message(s) are waiting on the desk smartphone (trigger: %s).\n"+
			"Call ws/companion.phone_pickup to pick up the smartphone and read the unread messages, reply via ws/companion.phone_send, "+
			"and finish with ws/companion.phone_putdown (next=\"ambient\" for casual chat, or next=\"work\" followed by ws/companion.delegate_work if background engineering work is needed).",
		unread, reason,
	)
}

// formatDelegationBriefEnvelope wraps brief with delegation_id while preserving
// brief byte-identical (no truncation, no trimming, lossless per (1748)).
func formatDelegationBriefEnvelope(delegationID, brief string) string {
	return "[companion delegation]\n" +
		"delegation_id: " + delegationID + "\n" +
		"Complete this task and report back using ws/delegation.report with delegation_id=\"" + delegationID + "\", status (\"done\"|\"partial\"|\"blocked\"), result, and artifacts.\n\n" +
		"---BEGIN BRIEF---\n" +
		brief +
		"\n---END BRIEF---"
}

// formatDelegationReportEnvelope wraps Tier-2's structured report verbatim
// (no truncation, no summarization, lossless per (1748)) for delivery to Tier-1.
func formatDelegationReportEnvelope(delegationID, status, result string, artifacts []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[companion delegation report]\ndelegation_id: %s\nstatus: %s\n", delegationID, status)
	if len(artifacts) > 0 {
		b.WriteString("artifacts:\n")
		for _, a := range artifacts {
			b.WriteString("- ")
			b.WriteString(a)
			b.WriteByte('\n')
		}
	}
	b.WriteString("\n---BEGIN RESULT---\n")
	b.WriteString(result)
	b.WriteString("\n---END RESULT---\n\n")
	b.WriteString("Brief the user in character and current mood on the desk smartphone (call ws/companion.phone_pickup, ws/companion.phone_send, then ws/companion.phone_putdown). Preserve all technical artifacts verbatim (commit hashes, file links/paths, diffs, test outputs, and review verdicts) without altering or omitting them.")
	return b.String()
}

func companionProjectKey(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	base := filepath.Base(filepath.Clean(root))
	if base == "." || base == "/" || base == `\` {
		return ""
	}
	return base
}

const companionWorkSystemPrompt = `You are the hidden companion work brain. You have no persona, no companion body tools, and you never speak directly to the user.

Delegation rules:
- You receive tasks wrapped in [companion delegation] with a delegation_id and a verbatim brief.
- Execute read-only inspection or workspace tasks directly, or delegate coding work to engineering subagents via ws/agent.spawn.
- Ending a turn does NOT complete a delegation (you may wait asynchronously for spawned subagents to report back).
- Only call ws/delegation.report(delegation_id, status, result, artifacts) when the active delegation is done, partial, or blocked, providing a complete, lossless technical result and all relevant artifact links or paths.
- You have direct access to the shared ws/daemon.* tools (ws/daemon.tabs, ws/daemon.curl, ws/daemon.dom, ws/daemon.ui, ws/daemon.screenshot, ws/daemon.highlight, ws/daemon.monitor) for inspecting and controlling the local dashboard; ws/daemon.highlight (visual pointer) and ws/daemon.monitor (structural UI events; stop it when done) never need approval.
- Read-only ws/daemon.* calls and tools in your low-impact allowlist run immediately. High-impact mutations (mutating ws/daemon.* calls and high-impact lead tools) pause behind a 1-click user approval banner in the dashboard and return denied if rejected, dismissed, or timed out; do not attempt to bypass approval gates.`
