// cron_seams.go holds what the daemon's scheduled prompts need from the
// runner: the target's identity, once-per-fire delivery of one prompt to a
// lead's inbox, the cron entries still held after a daemon start, and the
// notice that a conversation is gone for good.
package wsagent

import (
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

// DeliverCron holds one scheduled prompt as a non-steered, never-merged
// entry in lead conversation id's inbox: the next turn takes it, and a turn
// running now does not see it. It reports false when the conversation is
// closed, handed off, not a lead or unknown, with nothing written. Its send
// counters and record write are Runner.send's, as AdmitCompletion keeps them.
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
	if _, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Parts: []string{prompt}, Cron: &mark}, r.opts.Now()); err != nil {
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
