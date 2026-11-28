package wsdashboard

import (
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// CompanionPuppeteerTier identifies the harness, model, effort, and provider
// resolved for the lightweight cosmetic Puppeteer (dashboard.companion_body_tier).
type CompanionPuppeteerTier struct {
	Harness  string
	Model    string
	Effort   string
	Provider string
}

const (
	companionPriorityRankLow    = 1
	companionPriorityRankNormal = 2
	companionPriorityRankHigh   = 3
)

func companionPriorityRank(kind string) int {
	switch kind {
	case companionTriggerUserMessage, companionTriggerError, companionTriggerToolError, companionTriggerBrainReply:
		return companionPriorityRankHigh
	case companionTriggerBrainWorkStart, companionTriggerSubagentEvent, companionTriggerTurnEnd, companionTriggerCommit, companionTriggerAsk:
		return companionPriorityRankNormal
	case companionTriggerBrainIdle, companionTriggerIdle:
		return companionPriorityRankLow
	default:
		return companionPriorityRankNormal
	}
}

func companionPriorityName(kind string) string {
	switch companionPriorityRank(kind) {
	case companionPriorityRankHigh:
		return companionPriorityHigh
	case companionPriorityRankLow:
		return companionPriorityLow
	default:
		return companionPriorityNormal
	}
}

// SetCompanionPuppeteerTier installs the callback resolving Puppeteer's tier
// (dashboard.companion_body_tier, default small) without importing wsagent.
// Passing nil restores fallback to the observed session's "small" tier slot.
func (d *Daemon) SetCompanionPuppeteerTier(fn func() (CompanionPuppeteerTier, error)) {
	if d == nil {
		return
	}
	if fn == nil {
		d.companionPuppeteerTier.Store(nil)
		return
	}
	d.companionPuppeteerTier.Store(&fn)
}

func (c *companionDirector) resetBrainFor(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.brainWatch.sessionID == sessionID && sessionID != "" {
		return
	}
	c.resetBrainWatchLocked(sessionID)
}

func (c *companionDirector) resetBrainWatchLocked(sessionID string) {
	if c.brainWatch.idle != nil {
		c.brainWatch.idle.Stop()
	}
	if c.brainCoalesce != nil {
		c.brainCoalesce.Stop()
		c.brainCoalesce = nil
	}
	c.brainPending = nil
	c.brainPendingSeq++
	c.brainWatch = companionWatch{sessionID: sessionID}
}

// isPuppeteerToolCall reports whether a tool invocation is self-originated
// from Puppeteer or a cosmetic companion-command so Puppeteer cannot loop on
// its own outputs.
func isPuppeteerToolCall(name, args string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if strings.Contains(lower, "companion.act") ||
		strings.Contains(lower, "companion_act") ||
		strings.Contains(lower, "companion-command") {
		return true
	}
	return strings.Contains(args, `"source":"puppeteer"`) || strings.Contains(args, `"source": "puppeteer"`)
}

// isPuppeteerSelfSnapshot reports whether snap carries non-empty tool batches
// that consist exclusively of Puppeteer / companion-command calls.
func isPuppeteerSelfSnapshot(snap *wslive.Snapshot) bool {
	if snap == nil || len(snap.Lead.Batches) == 0 {
		return false
	}
	sawCall := false
	for _, b := range snap.Lead.Batches {
		if b.Running != nil {
			sawCall = true
			if !isPuppeteerToolCall(b.Running.ToolName, b.Running.ArgsPreview) {
				return false
			}
		}
		for _, call := range b.Calls {
			sawCall = true
			if !isPuppeteerToolCall(call.Name, call.ArgsPreview) {
				return false
			}
		}
	}
	return sawCall
}

// isPuppeteerSelfRing reports whether a conversation ring record is tagged as
// Puppeteer-originated or carries a self-originated companion-command marker.
func isPuppeteerSelfRing(rec wslive.RingRecord) bool {
	if strings.EqualFold(strings.TrimSpace(rec.Source), "puppeteer") ||
		strings.EqualFold(strings.TrimSpace(rec.Origin), "puppeteer") ||
		strings.EqualFold(strings.TrimSpace(rec.Caption), "companion-command") {
		return true
	}
	if rec.Kind == wslive.RingCommand && strings.Contains(strings.ToLower(rec.Command), "companion-command") {
		return true
	}
	if strings.Contains(rec.Text, `"source":"puppeteer"`) ||
		strings.Contains(rec.Text, `"source": "puppeteer"`) ||
		strings.Contains(rec.Text, "[puppeteer]") {
		return true
	}
	return false
}

// observeBrain updates the dedicated Brain watch from one snapshot and returns
// Puppeteer trigger kinds (brain_work_start, subagent_event, error, brain_reply).
// The first observation seeds the watch state without firing stale triggers.
func (w *companionWatch) observeBrain(snap *wslive.Snapshot) []string {
	if snap == nil {
		return nil
	}
	lead := &snap.Lead
	var out []string
	seeded := w.seeded
	selfOnly := isPuppeteerSelfSnapshot(snap)
	isWorking := lead.TurnStartedAt > 0 || (lead.Phase != "" && lead.Phase != wslive.PhaseIdle)

	if seeded && !selfOnly {
		// brain_work_start: Brain transitioned from idle to working or started a new turn.
		if isWorking && (!w.working || (lead.TurnStartedAt > 0 && lead.TurnStartedAt != w.turnAt)) {
			out = append(out, companionTriggerBrainWorkStart)
		}
		// error: failed tool calls rose during an active turn.
		previousFailed := w.failed
		if lead.TurnStartedAt != w.turnAt {
			previousFailed = 0
		}
		if lead.TurnStartedAt > 0 && lead.Failed > previousFailed {
			out = append(out, companionTriggerError)
		}
		// brain_reply: active turn settled back to idle.
		if w.turnAt > 0 && lead.TurnStartedAt == 0 {
			out = append(out, companionTriggerBrainReply)
		}
	}

	// subagent_event: new subagent spawned or any subagent changed status.
	curAgents := make(map[string]string, len(snap.Agents))
	subagentChanged := false
	for _, a := range snap.Agents {
		curAgents[a.ID] = a.Status
		if seeded {
			if prev, ok := w.agents[a.ID]; !ok || prev != a.Status {
				subagentChanged = true
			}
		}
	}
	if subagentChanged {
		out = append(out, companionTriggerSubagentEvent)
	}

	w.seeded = true
	w.turnAt = lead.TurnStartedAt
	w.failed = lead.Failed
	w.phase = lead.Phase
	w.working = isWorking
	w.agents = curAgents
	return out
}

// directorObserveBrainSnapshot observes Brain's snapshots directly regardless
// of tab focus and Embodiment Lease, emitting "companion-brain-state" SSE on
// every transition and enqueueing Puppeteer reactions when agent mode is active.
func (d *Daemon) directorObserveBrainSnapshot(replyID string, snap *wslive.Snapshot, changed bool) {
	if !d.companion.current().config.Enabled {
		return
	}
	c := &d.director
	c.mu.Lock()
	if c.brainWatch.sessionID != replyID {
		c.resetBrainWatchLocked(replyID)
	}
	seeded := c.brainWatch.seeded
	triggers := c.brainWatch.observeBrain(snap)
	if isPuppeteerSelfSnapshot(snap) {
		c.dropLocked("self-loop")
	} else if changed || len(triggers) > 0 || !seeded {
		d.directorBrainActivityLocked(c.clock())
	}
	c.mu.Unlock()

	for _, kind := range triggers {
		d.directorEnqueueBrain(replyID, kind)
	}
}

// directorObserveBrainRing observes Brain's ring appends directly regardless
// of tab focus, classifying new user/reply/error records while dropping
// self-originated Puppeteer markers.
func (d *Daemon) directorObserveBrainRing(replyID string) {
	if !d.companion.current().config.Enabled {
		return
	}
	ring := d.companionRing(replyID)
	c := &d.director
	c.mu.Lock()
	if c.brainWatch.sessionID != replyID {
		c.resetBrainWatchLocked(replyID)
	}
	var newRecords []wslive.RingRecord
	if len(ring) > 0 {
		if !c.brainWatch.ringSeeded {
			c.brainWatch.ringSeeded = true
			c.brainWatch.lastRingID = ring[len(ring)-1].Name
			// RingAppended runs right after one new entry is appended; inspect
			// only that newest entry so historical seed entries never spam.
			newRecords = ring[len(ring)-1:]
		} else {
			start := 0
			for i := len(ring) - 1; i >= 0; i-- {
				if ring[i].Name == c.brainWatch.lastRingID {
					start = i + 1
					break
				}
			}
			if start < len(ring) {
				newRecords = ring[start:]
			}
			c.brainWatch.lastRingID = ring[len(ring)-1].Name
		}
	}
	var triggers []string
	sawReal := false
	for _, rec := range newRecords {
		if isPuppeteerSelfRing(rec) {
			c.dropLocked("self-loop")
			continue
		}
		sawReal = true
		switch rec.Kind {
		case wslive.RingUser:
			triggers = append(triggers, companionTriggerUserMessage)
		case wslive.RingReply:
			triggers = append(triggers, companionTriggerBrainReply)
		case wslive.RingTurn:
			if rec.Failed > 0 {
				triggers = append(triggers, companionTriggerError)
			}
		}
	}
	if sawReal || len(newRecords) == 0 {
		d.directorBrainActivityLocked(c.clock())
	}
	c.mu.Unlock()

	for _, kind := range triggers {
		d.directorEnqueueBrain(replyID, kind)
	}
}

func (d *Daemon) directorBrainActivityLocked(now time.Time) {
	c := &d.director
	w := &c.brainWatch
	w.activeAt = now
	w.idleFired = false
	after := c.idleAfter
	if after <= 0 {
		after = companionIdleAfter
	}
	if w.idle != nil {
		w.idle.Reset(after)
		return
	}
	sessionID := w.sessionID
	w.idle = time.AfterFunc(after, func() { d.directorBrainIdle(sessionID) })
}

func (d *Daemon) directorBrainIdle(sessionID string) {
	if !d.companion.current().config.Enabled {
		return
	}
	c := &d.director
	c.mu.Lock()
	w := &c.brainWatch
	fire := w.sessionID == sessionID && d.CompanionBrainReplyID() == sessionID && !w.idleFired
	if fire {
		w.idleFired = true
	}
	c.mu.Unlock()
	if fire {
		d.directorEnqueueBrain(sessionID, companionTriggerBrainIdle)
	}
}

// directorEnqueueBrain publishes the additive T0 "companion-brain-state" SSE
// immediately, updates companion mood, and folds burst Brain triggers inside a
// 250ms in-memory coalescing window (with high-priority preemption) before
// admitting a Puppeteer LLM call.
func (d *Daemon) directorEnqueueBrain(replyID, kind string) {
	d.PublishCompanionBrainState(kind, replyID)
	if ev := companionMoodEventForBrainTrigger(kind); ev != "" {
		d.ObserveCompanionMood(ev)
	}
	if !d.directorActive() {
		return
	}
	c := &d.director
	prio := companionPriorityRank(kind)
	c.mu.Lock()
	win := c.coalesceWindow
	if win <= 0 {
		win = companionBrainCoalesceWindow
	}
	if c.brainPending == nil {
		c.brainPendingSeq++
		seq := c.brainPendingSeq
		c.brainPending = &companionBrainPending{
			sessionID: replyID,
			kind:      kind,
			priority:  prio,
			seq:       seq,
		}
		c.brainCoalesce = time.AfterFunc(win, func() { d.directorFlushBrain(seq) })
		c.mu.Unlock()
		return
	}
	// Existing pending coalesced event: fold or preempt by priority.
	if prio > c.brainPending.priority {
		c.dropLocked("coalesced")
		if prio == companionPriorityRankHigh {
			// High-priority trigger preempts a lower-priority coalesced wait immediately.
			if c.brainCoalesce != nil {
				c.brainCoalesce.Stop()
				c.brainCoalesce = nil
			}
			c.brainPending = nil
			c.brainPendingSeq++
			c.mu.Unlock()
			d.directorFireBrain(replyID, kind)
			return
		}
		c.brainPending.sessionID = replyID
		c.brainPending.kind = kind
		c.brainPending.priority = prio
		c.mu.Unlock()
		return
	}
	if prio == c.brainPending.priority {
		c.dropLocked("coalesced")
		c.brainPending.sessionID = replyID
		c.brainPending.kind = kind
		c.mu.Unlock()
		return
	}
	// Lower priority than what is already waiting: drop silently.
	c.dropLocked("coalesced")
	c.mu.Unlock()
}

func (d *Daemon) directorFlushBrain(seq uint64) {
	if !d.directorActive() {
		return
	}
	c := &d.director
	c.mu.Lock()
	pending := c.brainPending
	if pending == nil || pending.seq != seq {
		c.mu.Unlock()
		return
	}
	c.brainPending = nil
	c.brainCoalesce = nil
	sessionID, kind := pending.sessionID, pending.kind
	c.mu.Unlock()
	d.directorFireBrain(sessionID, kind)
}

func companionMoodEventForBrainTrigger(kind string) CompanionMoodEvent {
	switch kind {
	case companionTriggerError:
		return CompanionMoodEventToolError
	case companionTriggerBrainIdle:
		return CompanionMoodEventIdle
	case companionTriggerBrainReply:
		return CompanionMoodEventTurnEnd
	case companionTriggerUserMessage:
		return CompanionMoodEventUserMessage
	default:
		return ""
	}
}

// directorFireBrain admits a Brain-originated Puppeteer trigger without
// requiring tab focus or checking EmbodimentLeaseActive(). High-priority
// triggers bypass the short cooldown floor while strictly respecting busy and
// the rolling hourly cap.
func (d *Daemon) directorFireBrain(sessionID, kind string) {
	c := &d.director
	var tier CompanionPuppeteerTier
	tierErr := false
	if fn := d.companionPuppeteerTier.Load(); fn != nil && *fn != nil {
		got, err := (*fn)()
		if err != nil || strings.TrimSpace(got.Harness) == "" {
			tierErr = true
		} else {
			tier = got
		}
	}
	harness := strings.TrimSpace(tier.Harness)
	if harness == "" && !tierErr && d.index != nil {
		if s, ok := d.index.Session(sessionID); ok {
			harness = s.Registration.Harness
		}
	}
	cfg := d.companion.current().config
	cooldown := time.Duration(min(max(cfg.AgentCooldownSeconds, 10), 3600)) * time.Second
	limit := min(max(cfg.AgentHourlyCap, 1), 120)
	highPrio := companionPriorityRank(kind) == companionPriorityRankHigh

	c.mu.Lock()
	focus := c.focusLocked()
	pack := focus.pack
	if pack == "" {
		pack = cfg.Pack
	}
	if pack == "" {
		pack = companionDefaultID
	}
	reason := ""
	switch {
	case tierErr:
		reason = "tier-error"
	case harness == "":
		reason = "no-harness"
	case !c.pool.SupportsHarness(harness):
		reason = "no-ephemeral"
	default:
		reason = c.admitLockedWithPriority(c.clock(), cooldown, limit, highPrio)
	}
	if reason != "" {
		c.dropLocked(reason)
		c.mu.Unlock()
		return
	}
	call := companionCall{
		sessionID:     sessionID,
		pack:          pack,
		kind:          kind,
		harness:       harness,
		phase:         c.brainWatch.phase,
		turnActive:    c.brainWatch.turnAt > 0,
		failed:        c.brainWatch.failed,
		asks:          len(c.brainWatch.asks),
		isBrain:       true,
		priority:      companionPriorityName(kind),
		puppeteerTier: tier,
	}
	c.mu.Unlock()
	go d.directorCall(call)
}
