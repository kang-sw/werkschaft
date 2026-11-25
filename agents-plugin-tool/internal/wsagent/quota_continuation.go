package wsagent

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

const (
	QuotaContinuationScheduled = "scheduled"
	QuotaContinuationInFlight  = "in_flight"
	QuotaContinuationExhausted = "exhausted"

	quotaResetBuffer         = 30 * time.Second
	quotaResetJitterMax      = 60 * time.Second
	quotaFallbackDelay       = time.Hour
	quotaFallbackMaxAttempts = 6
)

const (
	quotaFenceIdle uint32 = iota
	quotaFencePending
	quotaFenceFiring
	quotaFenceCancelled
)

// QuotaContinuation is a hosted lead's persisted post-usage-limit resume state.
// Additive top-level member on Record; preserved as unknown by older daemons (T0).
type QuotaContinuation struct {
	Status              string  `json:"status"`
	ResumeAt            int64   `json:"resume_at,omitempty"`
	Attempts            int     `json:"attempts,omitempty"`
	MaxAttempts         int     `json:"max_attempts,omitempty"`
	Manual              bool    `json:"manual,omitempty"`
	NoticeText          string  `json:"notice_text,omitempty"`
	ScheduledAfterSends *uint64 `json:"scheduled_after_sends,omitempty"`
}

func cloneQuotaContinuation(qc *QuotaContinuation) *QuotaContinuation {
	if qc == nil {
		return nil
	}
	cp := *qc
	if qc.ScheduledAfterSends != nil {
		sends := *qc.ScheduledAfterSends
		cp.ScheduledAfterSends = &sends
	}
	return &cp
}

func toLiveQuotaContinuation(qc *QuotaContinuation) *wslive.QuotaContinuation {
	if qc == nil {
		return nil
	}
	return &wslive.QuotaContinuation{
		Status:      qc.Status,
		ResumeAt:    qc.ResumeAt,
		Attempts:    qc.Attempts,
		MaxAttempts: qc.MaxAttempts,
		Manual:      qc.Manual,
		NoticeText:  qc.NoticeText,
	}
}

func quotaContinuationPrompt(knownReset bool) string {
	if knownReset {
		return "[system] Your previous turn stopped because the provider's usage limit was reached, and the reset window has now passed. Inspect your current session state (agenda, todos, working tree, and any pending work) and continue from where you left off without repeating completed actions."
	}
	return "[system] Your previous turn stopped because the provider's usage limit was reached, and the scheduled retry interval has elapsed. Inspect your current session state (agenda, todos, working tree, and any pending work) and continue from where you left off without repeating completed actions."
}

// ScheduleQuotaContinuation schedules a manual post-usage-limit continuation
// for an open, idle hosted lead currently in a usage-limit failure state.
func (r *Runner) ScheduleQuotaContinuation(id string) (State, error) {
	return r.act(id, func(c *conv) error {
		return c.scheduleQuotaContinuationManual()
	})
}

// CancelQuotaContinuation cancels any pending or exhausted quota continuation
// on a hosted lead. Idempotent when none is active.
func (r *Runner) CancelQuotaContinuation(id string) (State, error) {
	return r.act(id, func(c *conv) error {
		if !c.rec.Lead {
			return badRequest("quota continuation applies to hosted lead sessions only")
		}
		c.cancelQuotaContinuation(false)
		return nil
	})
}

// quotaContinuationMode resolves agent.quota_continuation for rec's harness
// bucket, falling back to .all, bare key, and builtin "off".
func (c *conv) quotaContinuationMode(rec Record) string {
	resolver := c.rolloutResolver(rec)
	values, err := resolver.ListScalars(rec.WSKey, wsconfig.ItemAgentQuotaContinuation)
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: %s: %v\n", rec.ReplyID, wsconfig.ItemAgentQuotaContinuation, err)
		return "off"
	}
	byKey := make(map[string]string, 4)
	for _, v := range values {
		byKey[v.Key] = v.Value
	}
	for _, key := range []string{
		wsconfig.ItemAgentQuotaContinuation + "." + rec.Backend,
		wsconfig.ItemAgentQuotaContinuation + ".all",
		wsconfig.ItemAgentQuotaContinuation,
	} {
		value := strings.ToLower(strings.TrimSpace(byKey[key]))
		if value == "" {
			continue
		}
		if value == "auto" {
			return "auto"
		}
		return "off"
	}
	return "off"
}

func (c *conv) computeQuotaResumeAt(now time.Time, resetsAt int64) int64 {
	var jitter time.Duration
	if c.r.opts.QuotaContinuationJitter != nil {
		jitter = c.r.opts.QuotaContinuationJitter(quotaResetJitterMax)
	} else {
		jitter = time.Duration(rand.Int64N(int64(quotaResetJitterMax) + 1))
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > quotaResetJitterMax {
		jitter = quotaResetJitterMax
	}
	base := now.UnixMilli()
	if resetsAt > base {
		base = resetsAt
	}
	return base + quotaResetBuffer.Milliseconds() + jitter.Milliseconds()
}

func (c *conv) stopQuotaTimer() {
	if c.quotaStop != nil {
		c.quotaStop()
	}
	c.quotaC, c.quotaStop = nil, nil
	c.quotaPendingFire = false
	c.quotaCancelFence.Store(quotaFenceIdle)
}

func (c *conv) armQuotaTimer(delay time.Duration) {
	c.stopQuotaTimer()
	if delay < 0 {
		delay = 0
	}
	c.quotaCancelFence.Store(quotaFencePending)
	c.mu.Lock()
	held := c.held > 0
	c.quotaArmedInputGen = c.quotaInputGen.Load()
	c.mu.Unlock()
	if held {
		c.cancelQuotaContinuationForInput()
		c.cancelQuotaContinuation(false)
		return
	}
	c.quotaC, c.quotaStop = c.r.opts.Timer(delay)
}

// cancelQuotaContinuationForInput fences a pending quota continuation timer
// from outside the conversation loop when new input is admitted. Callers hold
// c.mu so the generation bump is ordered with quotaContinuationTimedOut's final
// check before wsinbox.Send.
func (c *conv) cancelQuotaContinuationForInput() {
	c.quotaInputGen.Add(1)
	c.quotaCancelFence.CompareAndSwap(quotaFencePending, quotaFenceCancelled)
}

func (c *conv) hasQuotaContinuation() bool {
	return c.quotaC != nil || c.quotaPendingFire || c.rec.QuotaContinuation != nil
}

func (c *conv) setQuotaContinuation(next *QuotaContinuation, now time.Time) {
	cp := cloneQuotaContinuation(next)
	c.update(func(rec *Record) {
		saved := cloneQuotaContinuation(cp)
		if saved != nil && saved.Status == QuotaContinuationScheduled && saved.ScheduledAfterSends == nil {
			sends := rec.Sends
			saved.ScheduledAfterSends = &sends
		}
		rec.QuotaContinuation = saved
	})
	delay := time.Duration(max(0, cp.ResumeAt-now.UnixMilli())) * time.Millisecond
	c.armQuotaTimer(delay)
}

// cancelQuotaContinuation stops any armed timer, clears persisted
// Record.QuotaContinuation when present (zero idle writes), and syncs Host.
func (c *conv) cancelQuotaContinuation(quiet bool) {
	hadTimer := c.quotaC != nil || c.quotaPendingFire
	c.stopQuotaTimer()
	if c.rec.QuotaContinuation == nil {
		if hadTimer {
			c.syncHost(quiet)
		}
		return
	}
	c.update(func(rec *Record) { rec.QuotaContinuation = nil })
	c.syncHost(quiet)
	c.changed(quiet)
}

// handleUsageLimitAlert updates QuotaContinuation state on a live hosted-lead
// NoticeUsageLimit alert. Runs in the loop before syncHost.
func (c *conv) handleUsageLimitAlert(n wslive.Notice, manualOnly bool) {
	if !c.rec.Lead || c.closed || c.hidden || c.isForeign() {
		return
	}
	now := c.r.opts.Now()
	qc := cloneQuotaContinuation(c.rec.QuotaContinuation)
	if qc != nil && qc.Status == QuotaContinuationInFlight {
		if manualOnly && !qc.Manual {
			// Auto-mode continuation hit a manual-only pause: clear state and
			// leave the usage-limit capsule for operator action.
			c.stopQuotaTimer()
			c.update(func(rec *Record) { rec.QuotaContinuation = nil })
			return
		}
		if n.ResetsAt > 0 {
			next := &QuotaContinuation{
				Status:      QuotaContinuationScheduled,
				ResumeAt:    c.computeQuotaResumeAt(now, n.ResetsAt),
				Attempts:    0,
				MaxAttempts: 0,
				Manual:      qc.Manual,
				NoticeText:  n.Text,
			}
			c.setQuotaContinuation(next, now)
			return
		}
		maxAttempts := qc.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = quotaFallbackMaxAttempts
		}
		if qc.Attempts >= maxAttempts {
			c.stopQuotaTimer()
			next := &QuotaContinuation{
				Status:      QuotaContinuationExhausted,
				Attempts:    qc.Attempts,
				MaxAttempts: maxAttempts,
				Manual:      qc.Manual,
				NoticeText:  n.Text,
			}
			c.update(func(rec *Record) { rec.QuotaContinuation = next })
			return
		}
		next := &QuotaContinuation{
			Status:      QuotaContinuationScheduled,
			ResumeAt:    now.Add(quotaFallbackDelay).UnixMilli(),
			Attempts:    qc.Attempts,
			MaxAttempts: maxAttempts,
			Manual:      qc.Manual,
			NoticeText:  n.Text,
		}
		c.setQuotaContinuation(next, now)
		return
	}

	if manualOnly {
		if qc != nil && qc.Status == QuotaContinuationScheduled {
			c.stopQuotaTimer()
			c.update(func(rec *Record) { rec.QuotaContinuation = nil })
		}
		return
	}
	if c.quotaContinuationMode(c.record()) != "auto" {
		return
	}
	if n.ResetsAt > 0 {
		next := &QuotaContinuation{
			Status:      QuotaContinuationScheduled,
			ResumeAt:    c.computeQuotaResumeAt(now, n.ResetsAt),
			Attempts:    0,
			MaxAttempts: 0,
			Manual:      false,
			NoticeText:  n.Text,
		}
		c.setQuotaContinuation(next, now)
		return
	}
	next := &QuotaContinuation{
		Status:      QuotaContinuationScheduled,
		ResumeAt:    now.Add(quotaFallbackDelay).UnixMilli(),
		Attempts:    0,
		MaxAttempts: quotaFallbackMaxAttempts,
		Manual:      false,
		NoticeText:  n.Text,
	}
	c.setQuotaContinuation(next, now)
}

func (c *conv) scheduleQuotaContinuationManual() error {
	if !c.rec.Lead {
		return badRequest("quota continuation applies to hosted lead sessions only")
	}
	if c.closed || c.hidden || c.isForeign() {
		return conflict("session is not active")
	}
	if c.turn || c.rec.DurableTurn != nil {
		return conflict("cannot schedule quota continuation while a turn is running")
	}
	if c.failure == nil || c.failure.Kind != wslive.NoticeUsageLimit {
		return conflict("session is not in a usage-limit state")
	}
	now := c.r.opts.Now()
	resetsAt := c.failure.ResetsAt
	if resetsAt <= 0 && c.r.opts.QuotaResetAt != nil {
		if at := c.r.opts.QuotaResetAt(c.rec.Backend, c.rec.Model); at > now.UnixMilli() {
			resetsAt = at
		}
	}
	var next *QuotaContinuation
	if resetsAt > 0 {
		next = &QuotaContinuation{
			Status:      QuotaContinuationScheduled,
			ResumeAt:    c.computeQuotaResumeAt(now, resetsAt),
			Attempts:    0,
			MaxAttempts: 0,
			Manual:      true,
			NoticeText:  c.failure.Text,
		}
	} else {
		next = &QuotaContinuation{
			Status:      QuotaContinuationScheduled,
			ResumeAt:    now.Add(quotaFallbackDelay).UnixMilli(),
			Attempts:    0,
			MaxAttempts: quotaFallbackMaxAttempts,
			Manual:      true,
			NoticeText:  c.failure.Text,
		}
	}
	c.setQuotaContinuation(next, now)
	c.syncHost(false)
	c.changed(false)
	return nil
}

// restoreQuotaContinuation re-arms a persisted scheduled continuation on
// daemon restart, or clears an orphaned in_flight state without double-firing.
func (c *conv) restoreQuotaContinuation() {
	if !c.rec.Lead || c.rec.QuotaContinuation == nil || c.closed || c.hidden || c.isForeign() {
		return
	}
	qc := cloneQuotaContinuation(c.rec.QuotaContinuation)
	switch qc.Status {
	case QuotaContinuationScheduled:
		if qc.ScheduledAfterSends != nil && c.rec.Sends > *qc.ScheduledAfterSends {
			// A downgrade to an older daemon that preserved quota_continuation
			// opaquely while advancing Record.Sends invalidated this reservation.
			c.cancelQuotaContinuation(false)
			return
		}
		delay := time.Duration(max(0, qc.ResumeAt-c.r.opts.Now().UnixMilli())) * time.Millisecond
		c.armQuotaTimer(delay)
		c.syncHost(false)
	case QuotaContinuationInFlight:
		if !c.turn && !c.replaying && c.rec.DurableTurn == nil && c.rec.HolderID == "" {
			c.cancelQuotaContinuation(false)
			return
		}
		c.syncHost(false)
	case QuotaContinuationExhausted:
		c.syncHost(false)
	}
}

func (c *conv) settleQuotaInFlightAfterReplay() {
	if !c.rec.Lead || c.turn || c.rec.DurableTurn != nil {
		return
	}
	if c.rec.QuotaContinuation != nil && c.rec.QuotaContinuation.Status == QuotaContinuationInFlight {
		c.cancelQuotaContinuation(false)
	}
}

// quotaContinuationTimedOut fires a scheduled quota continuation. It returns
// true when it injected or deferred a continuation turn, and false when it
// cancelled or skipped so kick() can fall through to pending user/mail input.
func (c *conv) quotaContinuationTimedOut() bool {
	c.quotaC, c.quotaStop = nil, nil
	c.quotaPendingFire = false
	if !c.quotaCancelFence.CompareAndSwap(quotaFencePending, quotaFenceFiring) {
		if c.quotaCancelFence.Load() == quotaFenceCancelled {
			c.cancelQuotaContinuation(false)
		}
		return false
	}
	if hook := c.onQuotaAfterFence; hook != nil {
		hook()
	}
	qc := cloneQuotaContinuation(c.rec.QuotaContinuation)
	if !c.rec.Lead || c.closed || c.hidden || c.isForeign() || qc == nil || qc.Status != QuotaContinuationScheduled {
		c.quotaCancelFence.Store(quotaFenceIdle)
		return false
	}
	c.mu.Lock()
	hasInput := c.quotaInputGen.Load() != c.quotaArmedInputGen || c.held > 0 || len(c.rec.Queued) > 0 || len(c.rec.Mail) > 0
	c.mu.Unlock()
	if hasInput {
		c.cancelQuotaContinuation(false)
		return false
	}
	if c.proc == nil && c.deferKickIfUnpublished() {
		c.quotaPendingFire = true
		c.quotaCancelFence.Store(quotaFencePending)
		return true
	}
	if !c.canKick() || c.rec.DurableTurn != nil || c.compacting || c.compactCommand ||
		(!c.r.startupReady() && (strings.HasPrefix(c.compactNote, compactionRestoreMarker) || strings.HasPrefix(c.compactNote, restartRestoreMarker))) {
		c.quotaPendingFire = true
		c.quotaCancelFence.Store(quotaFencePending)
		return true
	}

	knownReset := qc.MaxAttempts == 0
	next := cloneQuotaContinuation(qc)
	next.Status = QuotaContinuationInFlight
	next.ResumeAt = 0
	next.ScheduledAfterSends = nil
	if next.MaxAttempts > 0 {
		next.Attempts++
	}
	c.update(func(rec *Record) { rec.QuotaContinuation = cloneQuotaContinuation(next) })
	if hook := c.onQuotaBeforeSend; hook != nil {
		hook()
	}

	prompt := quotaContinuationPrompt(knownReset)
	operator := next.Manual && c.rec.Lead
	c.wmu.Lock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.wmu.Unlock()
		c.quotaCancelFence.Store(quotaFenceIdle)
		return false
	}
	raced := c.quotaInputGen.Load() != c.quotaArmedInputGen || c.held > 0 || len(c.rec.Queued) > 0 || len(c.rec.Mail) > 0
	if raced {
		c.mu.Unlock()
		c.wmu.Unlock()
		c.cancelQuotaContinuation(false)
		return false
	}
	_, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Parts: []string{prompt}, Operator: operator}, c.r.opts.Now())
	if err == nil {
		c.sends++
		c.held++
		c.rec.Sends = c.sends
	}
	c.mu.Unlock()
	c.wmu.Unlock()
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: quota continuation send: %v\n", c.rec.ReplyID, err)
		c.cancelQuotaContinuation(false)
		return false
	}
	c.quotaCancelFence.Store(quotaFenceIdle)
	c.syncHost(false)
	c.changed(false)
	c.quotaInjecting = true
	defer func() { c.quotaInjecting = false }()
	c.inboxMutated(1)
	c.recordDirty = true
	// The admitted inbox turn carries the compaction restore note itself.
	// Dispatch it before compactIdle can open a separate resume turn.
	c.kickInput()
	return true
}
