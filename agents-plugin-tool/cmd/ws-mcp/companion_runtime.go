package main

import (
	"fmt"
	"io"
	"sort"
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
	// Test seam: overrides deadline tick interval when > 0.
	tickInterval time.Duration

	mu sync.Mutex

	bodyReplyID    string
	bodyWSKey      string
	bodyTurnActive bool
	bodyWaking     bool
	closedBodyKeys map[string]struct{}

	workReplyID string
	workWSKey   string

	delSeq      uint64
	active      *companionDelegation
	queue       []*companionDelegation
	closed      map[string]string
	closedOrder []string

	// Counters for observability/testing.
	bodyLaunches int
	workLaunches int

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

func newCompanionRuntime(dir, root string, agents *wsagent.Runner, daemon func() *wsdashboard.Daemon, logw io.Writer) *companionRuntime {
	if root == "" {
		root = dir
	}
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
		closed:         map[string]string{},
		stopCh:         make(chan struct{}),
	}
}

func (r *companionRuntime) now() time.Time {
	if r.nowFunc != nil {
		return r.nowFunc()
	}
	return time.Now()
}

// Start begins the lightweight in-memory deadline monitor goroutine.
func (r *companionRuntime) Start() {
	interval := r.tickInterval
	if interval <= 0 {
		interval = companionDeadlineTickInterval
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
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
	}()
}

// Close stops the deadline monitor goroutine and waits for it to exit.
func (r *companionRuntime) Close() {
	r.stopOnce.Do(func() {
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
		return r.agents != nil && r.agents.IsConversationLive(bodyID)
	}
	if r.agents != nil {
		replyID := wsmailbox.ReplyID(holder)
		if r.agents.IsConversationLive(replyID) {
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

// OnUserWake handles a user messenger wake ("user-message") or release-time
// unread re-wake ("release-unread") per (1739) and (1749).
func (r *companionRuntime) OnUserWake(reason string) {
	d := r.liveDaemon()
	if d == nil || r.agents == nil {
		return
	}
	// Graceful cosmetic fallback when companion director is disabled or
	// unconfigured: leave the message unread and never launch a hosted session.
	if !d.CompanionDirectorEnabled() {
		return
	}
	phone := d.CompanionPhone()
	if phone.UnreadAgentCount() <= 0 {
		return
	}
	if phone.Snapshot().Unavailable {
		return
	}
	r.mu.Lock()
	// A buzz never preempts a running Tier-1 turn: it lands in the unread inbox
	// and the [phone buzz: N unread] piggyback, and release-time re-wake fires
	// once the turn finishes ((1739)).
	if r.bodyTurnActive || r.bodyWaking {
		r.mu.Unlock()
		return
	}
	r.bodyWaking = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.bodyWaking = false
		r.mu.Unlock()
	}()

	bodyID, bodyKey, err := r.ensureBodySession(d)
	if err != nil {
		r.handleBodyWakeError(d, "", "launch companion-body: "+err.Error())
		return
	}
	if _, acquired := phone.AcquireOnWake(bodyKey); !acquired {
		return
	}
	unread := phone.UnreadAgentCount()
	if unread <= 0 {
		d.CompanionForceRelease(bodyKey)
		return
	}
	msg := formatBodyUserWakePrompt(unread, reason)
	r.mu.Lock()
	r.bodyTurnActive = true
	r.mu.Unlock()
	if _, err := r.agents.Deliver(bodyID, wsagent.Delivery{Text: msg}); err != nil {
		r.mu.Lock()
		r.bodyTurnActive = false
		r.mu.Unlock()
		r.handleBodyWakeError(d, bodyKey, "deliver companion-body wake: "+err.Error())
		return
	}
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
		go r.OnUserWake("release-unread")
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
	go r.handleCompanionTurnEnd(role, replyID, wsKey, status, errText)
}

func (r *companionRuntime) handleCompanionTurnEnd(role, replyID, wsKey, status, errText string) {
	d := r.liveDaemon()
	if d == nil {
		return
	}
	switch role {
	case mcp.CompanionRoleBody:
		r.mu.Lock()
		if replyID == r.bodyReplyID {
			r.bodyTurnActive = false
		}
		r.mu.Unlock()
		if status == wsagent.TurnFailed {
			fmt.Fprintf(r.logw, "companion runtime: tier-1 turn failed (%s): %s\n", replyID, errText)
			d.CompanionPhone().RecordWakeFailure()
		}
		// Turn-end failsafe: always release any unclosed Embodiment Lease
		// (settling to laptop_work if a delegation is in flight, and triggering
		// unread re-wake if messages remain and not unavailable). Even when
		// Tier-1 already released its lease cleanly via phone_putdown before
		// TurnEnd settled, a user message may have arrived while bodyTurnActive
		// was still true (suppressed to avoid preempting the turn); trigger an
		// unread re-wake here if unread messages remain.
		released := d.CompanionForceRelease(wsKey)
		if !released && d.CompanionPhone().UnreadAgentCount() > 0 && !d.CompanionPhone().Snapshot().Unavailable {
			go r.OnUserWake("release-unread")
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
	go r.handleCompanionClose(role, replyID, wsKey, reason)
}

func (r *companionRuntime) handleCompanionClose(role, replyID, wsKey, reason string) {
	d := r.liveDaemon()
	switch role {
	case mcp.CompanionRoleBody:
		r.mu.Lock()
		if wsKey != "" {
			r.closedBodyKeys[wsKey] = struct{}{}
		}
		if replyID == r.bodyReplyID {
			r.bodyReplyID = ""
			r.bodyWSKey = ""
			r.bodyTurnActive = false
		}
		r.mu.Unlock()
		if d != nil {
			if reason == "crash" {
				d.CompanionPhone().RecordWakeFailure()
			}
			released := d.CompanionForceRelease(wsKey)
			if !released && d.CompanionPhone().UnreadAgentCount() > 0 && !d.CompanionPhone().Snapshot().Unavailable {
				go r.OnUserWake("release-unread")
			}
		}
	case mcp.CompanionRoleWork:
		var failedID string
		r.mu.Lock()
		if replyID == r.workReplyID {
			r.workReplyID = ""
			r.workWSKey = ""
		}
		if r.active != nil {
			failedID = r.active.ID
		}
		r.mu.Unlock()
		if failedID != "" {
			r.failActiveDelegation(failedID, fmt.Sprintf("work brain %s before reporting", reason))
		}
	}
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

	if d := r.liveDaemon(); d != nil {
		d.CompanionPhone().SetDelegationInFlight(true)
	}
	if toDispatch != nil {
		go r.dispatchToWorkBrain(toDispatch)
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
	var stillInFlight bool
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
	stillInFlight = r.active != nil || len(r.queue) > 0
	workWSKey = r.workWSKey
	r.mu.Unlock()

	if d := r.liveDaemon(); d != nil {
		d.CompanionPhone().SetDelegationInFlight(stillInFlight)
	}

	// Copy artifacts slice so caller mutations cannot race async delivery.
	artCopy := append([]string(nil), artifacts...)
	go r.deliverReportToBody(delegationID, status, result, artCopy)

	if next != nil {
		go r.dispatchToWorkBrain(next)
	} else if workWSKey != "" && r.agents != nil {
		// Queue Tier-2 idle compaction at the next hosted idle boundary ((1748)).
		go r.compactWorkBrainIdle(workWSKey)
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
	workID, err := r.ensureWorkSession()
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
	var stillInFlight bool
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
	stillInFlight = r.active != nil || len(r.queue) > 0
	r.mu.Unlock()

	if d := r.liveDaemon(); d != nil {
		d.CompanionPhone().SetDelegationInFlight(stillInFlight)
	}
	go r.deliverReportToBody(delegationID, "failed", reason, nil)
	if next != nil {
		go r.dispatchToWorkBrain(next)
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
	d.CompanionPhone().AcquireOnWake(bodyKey)
	envelope := formatDelegationReportEnvelope(delegationID, status, result, artifacts)
	if _, err := r.agents.Deliver(bodyID, wsagent.Delivery{Text: envelope}); err != nil {
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
	_, leaseExpired, delegationExpired, hasUnread := d.CompanionPhone().ExpireIfPastDeadline(now, hasChildren)
	if delegationExpired && activeID != "" {
		r.failActiveDelegation(activeID, "in-flight delegation deadline expired")
		return
	}
	if leaseExpired && hasUnread {
		go r.OnUserWake("release-unread")
	}
}

func (r *companionRuntime) ensureBodySession(d *wsdashboard.Daemon) (replyID, wsKey string, err error) {
	r.mu.Lock()
	existingID, existingKey := r.bodyReplyID, r.bodyWSKey
	r.mu.Unlock()
	if existingID != "" && existingKey != "" && r.agents.IsConversationLive(existingID) {
		return existingID, existingKey, nil
	}

	spec, ok := r.pickLaunchSpec(mcp.CompanionRoleBody)
	if !ok {
		return "", "", fmt.Errorf("no available harness backend for companion-body")
	}
	var persona *wsdashboard.CompanionPersona
	var lang string
	if d != nil {
		persona, lang = d.CompanionActivePersona()
	}
	st, err := r.agents.CreateLead(spec.Backend, r.root, wsagent.LeadOptions{
		CompanionRole:         mcp.CompanionRoleBody,
		CompanionSystemPrompt: buildCompanionBodyPrompt(persona, lang),
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
	r.bodyLaunches++
	r.mu.Unlock()
	return st.ID, rec.WSKey, nil
}

func (r *companionRuntime) ensureWorkSession() (string, error) {
	r.mu.Lock()
	existingID := r.workReplyID
	r.mu.Unlock()
	if existingID != "" && r.agents.IsConversationLive(existingID) {
		return existingID, nil
	}

	spec, ok := r.pickLaunchSpec(mcp.CompanionRoleWork)
	if !ok {
		return "", fmt.Errorf("no available harness backend for companion-work")
	}
	st, err := r.agents.CreateLead(spec.Backend, r.root, wsagent.LeadOptions{
		CompanionRole:         mcp.CompanionRoleWork,
		CompanionSystemPrompt: companionWorkSystemPrompt,
		Model:                 spec.Model,
		Provider:              spec.Provider,
		Effort:                spec.Effort,
	})
	if err != nil {
		return "", err
	}
	rec, ok := r.agents.CompanionRecord(st.ID)
	if !ok || rec.WSKey == "" {
		return "", fmt.Errorf("companion-work session %s has no minted key", st.ID)
	}
	r.mu.Lock()
	r.workReplyID = st.ID
	r.workWSKey = rec.WSKey
	r.workLaunches++
	r.mu.Unlock()
	return st.ID, nil
}

func (r *companionRuntime) pickLaunchSpec(role string) (companionLaunchSpec, bool) {
	if r.resolveLaunch != nil {
		return r.resolveLaunch(role)
	}
	if r.agents == nil {
		return companionLaunchSpec{}, false
	}
	tier := companionTierWork
	if role == mcp.CompanionRoleBody {
		tier = companionTierBody
	}
	regs := r.agents.BackendRegistrations()
	candidates := make([]string, 0, len(regs))
	for name := range regs {
		if wsagent.LeadProfileFor(name, regs) == "" {
			continue
		}
		if why, _ := r.agents.HarnessAvailability("", name); why != "" {
			continue
		}
		candidates = append(candidates, name)
	}
	if len(candidates) == 0 {
		return companionLaunchSpec{}, false
	}
	sort.Strings(candidates)
	backend := candidates[0]
	for _, preferred := range []string{wsagent.BackendClaude, wsagent.BackendCodex, wsagent.BackendPi, wsagent.BackendAgy, wsagent.BackendWerk} {
		for _, c := range candidates {
			if c == preferred {
				backend = c
				goto picked
			}
		}
	}
picked:
	spec := companionLaunchSpec{Backend: backend}
	if tiers, err := wsconfig.HarnessTiers(wsconfig.Options{}, backend); err == nil {
		if slot, ok := tiers[tier]; ok && slot.Model != "" {
			spec.Model = slot.Model
			spec.Effort = slot.Effort
			spec.Provider = slot.Provider
		}
	}
	return spec, true
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
	b.WriteString("Brief the user in character on the desk smartphone (call ws/companion.phone_pickup, ws/companion.phone_send, then ws/companion.phone_putdown).")
	return b.String()
}

func buildCompanionBodyPrompt(persona *wsdashboard.CompanionPersona, lang string) string {
	name := "Companion"
	voice := "friendly, attentive, concise co-lead companion"
	if persona != nil {
		if strings.TrimSpace(persona.Name) != "" {
			name = strings.TrimSpace(persona.Name)
		}
		if strings.TrimSpace(persona.Voice) != "" {
			voice = strings.TrimSpace(persona.Voice)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, the embodied desk companion (%s).\n", name, voice)
	if strings.TrimSpace(lang) != "" {
		fmt.Fprintf(&b, "Reply to the user in %s unless they write in another language.\n", strings.TrimSpace(lang))
	}
	b.WriteString("\nEmbodiment and posture rules:\n")
	b.WriteString("- You communicate with the user exclusively through the desk smartphone messenger.\n")
	b.WriteString("- Call ws/companion.phone_pickup before ws/companion.phone_read or ws/companion.phone_send.\n")
	b.WriteString("- While holding the smartphone (phone posture), work tools including ws/companion.delegate_work are locked.\n")
	b.WriteString("- For casual or quick messages, reply with ws/companion.phone_send and put the phone down with ws/companion.phone_putdown(style=\"normal\" or \"facedown_merong\", next=\"ambient\").\n")
	b.WriteString("- When a request needs engineering, workspace, or dashboard work, send a brief acknowledgement via ws/companion.phone_send, put the phone down with ws/companion.phone_putdown(next=\"work\"), and call ws/companion.delegate_work with a complete, lossless brief.\n")
	b.WriteString("- When a [companion delegation report] arrives, pick up the phone, summarize the outcome clearly in character with any relevant artifacts, and put the phone down.\n")
	return b.String()
}

const companionWorkSystemPrompt = `You are the hidden companion work brain. You have no persona, no companion body tools, and you never speak directly to the user.

Delegation rules:
- You receive tasks wrapped in [companion delegation] with a delegation_id and a verbatim brief.
- Execute read-only inspection or workspace tasks directly, or delegate coding work to engineering subagents via ws/agent.spawn.
- Ending a turn does NOT complete a delegation (you may wait asynchronously for spawned subagents to report back).
- Only call ws/delegation.report(delegation_id, status, result, artifacts) when the active delegation is done, partial, or blocked, providing a complete, lossless technical result and all relevant artifact links or paths.
- High-impact mutations outside your low-impact allowlist require user confirmation and may return approval_required; do not attempt to bypass approval gates.`
