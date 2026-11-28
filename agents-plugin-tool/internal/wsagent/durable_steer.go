package wsagent

import (
	"slices"

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
	// Clean is the text without the model-only whiteboard directive Text
	// carries; a requeued steer returns to the report queue as Clean so the
	// directive never leaks into a later turn. Empty when Text has none.
	Clean string `json:"clean,omitempty"`
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
	if p == nil && len(pending) == 0 {
		return
	}
	c.admitInput(func() {
		c.mu.Lock()
		var held []wsinbox.Record
		if p != nil {
			held, err = wsinbox.Held(c.inbox)
		}
		pendingCount := len(pending)
		var claimed []wsinbox.Record
		unclaimed := len(held)
		skipSteer := func(rec wsinbox.Record) bool {
			return !rec.Entry.Steer || rec.Entry.Command != nil
		}
		if err == nil {
			pending = slices.DeleteFunc(pending, skipSteer)
			held = slices.DeleteFunc(held, skipSteer)
			c.withCronClaimConsent(pending, held, func(disposition wsinbox.CronClaimDisposition) {
				if err = classifyCronClaims(c.inbox, pending, held, disposition); err != nil {
					return
				}
				claimed, err = wsinbox.ClaimInputs(c.inbox, held)
			})
			if p != nil {
				c.held = unclaimed - len(claimed)
			}
		}
		claimedCount := len(claimed)
		// Leave refusals staged for the idle path to settle. Only admitted
		// text can enter Werk's journal or the runner's steer fallback.
		pending = slices.DeleteFunc(pending, refusedCron)
		claimed = slices.DeleteFunc(claimed, refusedCron)
		// Send coverage is a prefix. A manually promoted message can
		// overtake a held follow-up, so only advance when no input is left.
		covered := c.rec.TurnMark
		if c.held == 0 && pendingCount == len(pending) && claimedCount == len(claimed) && len(c.rec.Queued) == 0 {
			covered = c.sends
		}
		c.mu.Unlock()
		c.inboxMutated(claimedCount)
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
		var names []string
		for _, rec := range recs {
			names = append(names, rec.Name)
		}
		parts := recordParts(recs)
		c.noteSteerWhiteboard(recs)
		c.queueDurableSteer(p, c.directedSteer(partsText(parts), recs, covered), names, liveEvents(parts))
	})
}

// queueDurableSteer records input before it can enter the session loop.
// Claims belong to the same commit, so recovery never admits them twice.
func (c *conv) queueDurableSteer(p durableSteerProcess, steer durableSteer, claims []string, live []wslive.Event) bool {
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
	if steer.Text != "" && p != nil {
		if err := p.submitSteer(c.rec.DurableTurn.ID, steer); err != nil {
			c.durableTurnError("send steering", err)
		} else {
			c.applyLive(live)
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
		if steer.Clean != "" {
			text = steer.Clean
		}
		if steer.Mail != "" {
			text = steer.Mail
		}
		// The steer's text joins a batch's parts: split it again so each
		// exec notice keeps its own element (splitReportTexts).
		rec.Reports = append(rec.Reports, splitReportTexts(text)...)
	}
	rec.DurableSteers = nil
}
