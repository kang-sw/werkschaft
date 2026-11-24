package wsagent

import (
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// A durable steer can outlive the process it was sent to. The model log's
// receipt tells the runner whether to advance coverage or requeue the text.
type durableSteer struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Mark uint64 `json:"mark,omitempty"`
	Mail string `json:"mail,omitempty"`
}

type durableSteerProcess interface {
	submitSteer(turnID string, steer durableSteer) error
}

func (c *conv) steerDurableInbox(p durableSteerProcess) {
	if err := c.ackInboxClaims(); err != nil {
		c.durableTurnError("acknowledge queued steering", err)
		return
	}
	pending, err := wsinbox.PendingClaims(c.inbox, c.rec.DurableInboxClaims)
	if err != nil {
		c.durableTurnError("read claimed steering", err)
		return
	}
	c.admitInput(func() {
		c.mu.Lock()
		held, err := wsinbox.Held(c.inbox)
		deferCron := c.rec.DurableTurn != nil && c.rec.DurableTurn.Operator &&
			(slices.ContainsFunc(pending, isCronInput) || slices.ContainsFunc(held, isCronInput)) && c.separatesPlanCron()
		skipSteer := func(rec wsinbox.Record) bool {
			return !rec.Entry.Steer || rec.Entry.Command != nil || (deferCron && isCronInput(rec))
		}
		pendingCount := len(pending)
		pending = slices.DeleteFunc(pending, skipSteer)
		var claimed []wsinbox.Record
		unclaimed := len(held)
		if err == nil {
			claimed, err = wsinbox.ClaimInputs(c.inbox, slices.DeleteFunc(held, skipSteer))
			c.held = unclaimed - len(claimed)
		}
		// Send coverage is a prefix. A manually promoted message can
		// overtake a held follow-up, so only advance when no input is left.
		covered := c.turnMark
		if c.held == 0 && pendingCount == len(pending) && len(c.rec.Queued) == 0 {
			covered = c.sends
		}
		c.mu.Unlock()
		c.inboxMutated(len(claimed))
		if err != nil {
			c.durableTurnError("claim steering", err)
			return
		}
		recs := append(pending, claimed...)
		if len(recs) == 0 {
			return
		}
		orderInboxClaims(recs)
		c.cutoverInstructionLeft(recs)
		var parts, names []string
		for _, rec := range recs {
			names = append(names, rec.Name)
			if text := strings.TrimSpace(rec.Entry.Text()); text != "" {
				parts = append(parts, text)
			}
		}
		text := strings.Join(parts, "\n\n")
		c.queueDurableSteer(p, durableSteer{Text: text, Mark: covered}, names,
			wslive.Event{Kind: wslive.KindUser, Origin: wslive.OriginWhiteboard, Text: text})
	})
}

// queueDurableSteer records input before it can enter the session loop.
// Claims belong to the same commit, so recovery never admits them twice.
func (c *conv) queueDurableSteer(p durableSteerProcess, steer durableSteer, claims []string, live wslive.Event) bool {
	steer.ID = newReportID()
	err := c.commitDurableRecord(func(rec *Record) {
		if steer.Text != "" {
			rec.DurableSteers = append(slices.Clone(rec.DurableSteers), steer)
		}
		if len(claims) > 0 {
			rec.Drained, rec.DurableInboxClaims = max(rec.Drained, steer.Mark), claims
		}
	}, func() {
		if len(claims) > 0 {
			c.drained, c.inboxClaimsAcked = max(c.drained, steer.Mark), false
		}
	})
	if err != nil {
		c.durableTurnError("save steering", err)
		return false
	}
	if err := c.ackInboxClaims(); err != nil {
		c.durableTurnError("acknowledge steering", err)
	}
	if steer.Text != "" {
		if err := p.submitSteer(c.rec.DurableTurn.ID, steer); err != nil {
			c.durableTurnError("send steering", err)
		} else {
			c.apply(live)
			c.changed(false)
		}
	}
	return true
}

func (c *conv) acceptDurableSteer(id string) {
	pos := slices.IndexFunc(c.rec.DurableSteers, func(steer durableSteer) bool { return steer.ID == id })
	if pos < 0 {
		return
	}
	mark := max(c.turnMark, c.rec.DurableSteers[pos].Mark)
	err := c.commitDurableRecord(func(rec *Record) {
		rec.DurableSteers = slices.Delete(slices.Clone(rec.DurableSteers), pos, pos+1)
		rec.TurnMark = mark
	}, func() { c.turnMark = mark })
	if err != nil {
		c.durableTurnError("save steering receipt", err)
		// Replay the receipt before accepting the turn's terminal event.
		c.stopProcess("")
		c.retryDurableTurn()
	}
}

func requeueDurableSteers(rec *Record) {
	if len(rec.DurableSteers) == 0 {
		return
	}
	rec.Reports = slices.Clone(rec.Reports)
	for _, steer := range rec.DurableSteers {
		text := steer.Text
		if steer.Mail != "" {
			text = steer.Mail
		}
		rec.Reports = append(rec.Reports, text)
	}
	rec.DurableSteers = nil
}
