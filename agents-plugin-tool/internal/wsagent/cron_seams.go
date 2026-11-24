// cron_seams.go holds what the daemon's scheduled prompts need from the
// runner: the target's identity, once-per-fire delivery of one prompt to a
// lead's inbox, the cron entries still held after a daemon start, and the
// notice that a conversation is gone for good.
package wsagent

import (
	"errors"
	"slices"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

// CronSink is told when the runner dropped a conversation permanently.
type CronSink interface {
	ConversationGone(id string)
}

type cronLink struct{ sink CronSink }

// BindCron binds the daemon's scheduler (nil unbinds). Run it once the
// scheduler exists; a conversation forgotten before that has its schedules
// reaped at the scheduler's start.
func (r *Runner) BindCron(sink CronSink) {
	if sink == nil {
		r.cron.Store(nil)
		return
	}
	r.cron.Store(&cronLink{sink: sink})
}

func (r *Runner) cronGone(id string) {
	if l := r.cron.Load(); l != nil {
		l.sink.ConversationGone(id)
	}
}

// CronIsLead reports whether the open conversation id is a lead the daemon
// can deliver to. A closed, handed-off or unknown conversation is an error.
func (r *Runner) CronIsLead(id string) (bool, error) {
	c, err := r.open(id)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false, endedError(c.rec)
	}
	return c.rec.Lead, nil
}

// CronKnown reports whether the runner still knows conversation id, open or
// in its closed set (including handed off). It is the test a startup
// reconciliation and an index-forget handler use before deleting schedules.
func (r *Runner) CronKnown(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, open := r.convs[id]
	_, closed := r.closed[id]
	return open || closed
}

// CronHeld returns the schedule ids whose cron entries are still held,
// undrained, in conversation id's inbox.
func (r *Runner) CronHeld(id string) []string {
	c := r.conv(id)
	if c == nil {
		return nil
	}
	held, err := wsinbox.Held(c.inbox)
	if err != nil {
		return nil
	}
	var out []string
	for _, rec := range held {
		if rec.Entry.Cron != nil {
			out = append(out, rec.Entry.Cron.ID)
		}
	}
	return out
}

// DeliverCron holds one scheduled prompt as a steered, never-merged entry in
// lead conversation id's inbox. An interactive ChatGPT plan turn holds it
// while background consent is off. Other running turns use the harness's
// steer rules, including waiting for in-flight MCP calls on a replacesTurn
// harness. It reports false with nothing written when the conversation is
// closed, handed off, not a lead or unknown. Its send counters and record
// write are Runner.send's, as AdmitCompletion keeps them.
func (r *Runner) DeliverCron(id string, mark wsinbox.Cron, prompt string) (accepted bool, err error) {
	c, err := r.open(id)
	if err != nil {
		return false, nil
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.mu.Lock()
	if c.closed || !c.rec.Lead {
		c.mu.Unlock()
		return false, nil
	}
	if _, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Parts: []string{prompt}, Steer: true, Cron: &mark}, r.opts.Now()); err != nil {
		c.mu.Unlock()
		return false, err
	}
	c.sends++
	c.held++
	c.rec.Sends = c.sends
	c.cancelModelWaitBackoffForInput()
	c.mu.Unlock()
	c.inboxMutated(1)
	_ = c.persistRecord()
	c.signalInbox()
	return true, nil
}

func (c *conv) separatesPlanCron() bool {
	if !c.rec.Lead || c.rec.Provider != string(wscred.ProviderOpenAI) {
		return false
	}
	backend, ok := c.r.opts.Backends[c.rec.Backend].Backend.(interface {
		planBackgroundConsent() (allowed, known bool, err error)
	})
	if !ok {
		return false
	}
	allowed, _, err := backend.planBackgroundConsent()
	return err != nil || !allowed
}

func isCronInput(rec wsinbox.Record) bool { return rec.Entry.Cron != nil }

// planInputClaims selects the next batch while cron needs separate admission.
// Keep scheduled input waiting when operator input is ready. Otherwise keep
// inbox order and take one scheduled entry at a time.
func planInputClaims(queue []QueuedInput, pending, held []wsinbox.Record) (take func(wsinbox.Record) bool, cron bool) {
	if !slices.ContainsFunc(held, isCronInput) && !slices.ContainsFunc(pending, isCronInput) {
		return nil, false
	}
	operator := func(rec wsinbox.Record) bool { return rec.Entry.Operator }
	if len(queue) > 0 || slices.ContainsFunc(pending, operator) || slices.ContainsFunc(held, operator) {
		return func(rec wsinbox.Record) bool { return !isCronInput(rec) }, false
	}
	ordered := append(slices.Clone(pending), held...)
	orderInboxClaims(ordered)
	firstCron := slices.IndexFunc(ordered, isCronInput)
	if firstCron == 0 {
		name := ordered[0].Name
		return func(rec wsinbox.Record) bool { return rec.Name == name }, true
	}
	boundary := ordered[firstCron]
	return func(rec wsinbox.Record) bool { return compareInboxClaims(rec, boundary) < 0 }, false
}

// settlePlanCronRefusal keeps a denied prompt out of the model log, where
// a later interactive request would pick it up as conversation history.
func (c *conv) settlePlanCronRefusal(claim string, rest []QueuedInput, mark uint64) {
	message := (&wscred.ProviderError{Provider: wscred.ProviderOpenAI, Err: wscred.ErrConsentRequired}).Error()
	var err error
	refused := c.admitInput(func() {
		var release func()
		release, err = c.r.admitDurableTransition()
		if err != nil {
			return
		}
		defer release()
		err = c.publishWith(TurnResult{ID: c.rec.ReplyID, Status: TurnFailed, Error: message, mark: mark}, false, func(rec *Record) {
			rec.Queued, rec.TurnMark = rest, mark
			if claim != "" {
				// The refusal owns the inbox entry once its settlement commits.
				rec.Drained, rec.DurableInboxClaims = mark, []string{claim}
			}
		})
	})
	switch {
	case refused != "":
		c.refuseStart(checkoutReleasedRefusal(refused))
	case errors.Is(err, errDurableStopping):
		return
	case err != nil:
		c.durableTurnError("save scheduled input refusal", err)
	default:
		if claim != "" {
			c.drained, c.inboxClaimsAcked = mark, false
			if err := c.ackInboxClaims(); err != nil {
				c.durableTurnError("acknowledge scheduled input", err)
				return
			}
		}
		c.setStatus(func() { c.lastErr = message })
		c.live.writeSnapshot()
		c.changed(false)
		c.signalInbox()
	}
	c.armIdle()
}
