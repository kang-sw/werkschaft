package wsagent

import (
	"errors"
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// ackInboxClaims retires the previous claim batch before a new commit
// replaces its acknowledgement list. A failed move keeps that list durable.
func (c *conv) ackInboxClaims() error {
	if c.inboxClaimsAcked {
		return nil
	}
	if err := wsinbox.AcknowledgeClaims(c.inbox, c.rec.DurableInboxClaims); err != nil {
		return err
	}
	c.inboxClaimsAcked = true
	return nil
}

func (c *conv) commitInboxClaims(recs []wsinbox.Record, queue []QueuedInput, covered uint64) error {
	names := make([]string, 0, len(recs))
	for _, rec := range recs {
		names = append(names, rec.Name)
	}
	if err := c.commitDurableRecord(func(rec *Record) {
		rec.Queued, rec.Drained = queue, covered
		rec.DurableInboxClaims = names
	}, func() {
		c.drained, c.inboxClaimsAcked = covered, false
	}); err != nil {
		return err
	}
	return c.ackInboxClaims()
}

func orderInboxClaims(records []wsinbox.Record) {
	slices.SortStableFunc(records, compareInboxClaims)
}

func compareInboxClaims(a, b wsinbox.Record) int {
	if a.Entry.Steer != b.Entry.Steer {
		if a.Entry.Steer {
			return -1
		}
		return 1
	}
	return strings.Compare(a.Name, b.Name)
}

type durableTurnProcess interface {
	submitTurn(id, text string, steers []durableSteer, interactive bool) error
}

// admitDurableTurn commits the prepared model input with the inbox coverage.
// A failed submit leaves that same input available to a successor. It runs
// under admitInput, which orders the commit and submit against release.
func (c *conv) admitDurableTurn(text string, rest []QueuedInput, mark uint64, startedAt int64, mail, reports, settleHeld, interactive bool) (bool, error) {
	release, err := c.r.admitDurableTransition()
	if err != nil {
		return false, err
	}
	defer release()
	input := &durableTurnInput{ID: newReportID(), Text: text, Operator: interactive}
	stage := func(rec *Record) {
		rec.DurableTurn, rec.DurableInboxClaims = input, nil
		rec.Queued = rest
		rec.Drained, rec.TurnMark, rec.Voided = c.drained, mark, c.voided
		c.setPromptBoundary(rec, startedAt, true)
		if mail {
			rec.Mail = nil
		}
		if reports {
			rec.Reports = nil
		}
		rec.HeldTurn, rec.DurableHeldReportID = nil, ""
		rec.PendingCompaction = nil
	}
	if isAgentWorkerProfile(c.rec.Profile) && c.heldRes != nil && (reports || settleHeld) && !c.ownsLiveExec() {
		err = c.publishWith(*c.heldRes, true, stage)
	} else {
		err = c.commitDurableRecord(stage, func() {
			c.heldRes, c.holding = nil, false
		})
	}
	if err != nil {
		return false, err
	}
	if reports {
		c.parkedReports = 0
	}
	return true, c.submitDurableTurn(input)
}

// compactDurableContinue admits a requested, separate continuation through
// the same journal as every other prompt. Existing user input stays queued.
func (c *conv) compactDurableContinue() bool {
	c.compactResume = false
	if c.heldRes != nil && c.holdsReport() {
		c.compactResume = true
		return false
	}
	if err := c.ackInboxClaims(); err != nil {
		c.durableTurnError("acknowledge queued input", err)
		return false
	}
	text := strings.TrimSpace(c.renderPendingNote(c.compactNote)) + "\n\nContinue the work that was pending."
	if c.rec.Lead {
		text = c.appendAmbient(text, c.rec.WhiteboardOnly)
	}
	start := c.r.opts.Now()
	var committed bool
	err := c.sendStandalonePrompt(true, func() error {
		var err error
		committed, err = c.admitDurableTurn(text, c.rec.Queued, c.rec.TurnMark, start.UnixMilli(), false, false, true, false)
		return err
	})
	if checkout, ok := releasedInput(err); ok {
		c.dropReleasedInput("continuation after compaction", checkout)
		return false
	}
	if errors.Is(err, errDurableStopping) {
		c.compactResume = true
		return true
	}
	if committed {
		c.compactNote = ""
		c.compactResumeInternal = false
	}
	if err != nil {
		c.durableTurnError("continue after compaction", err)
		if committed {
			c.retryDurableTurn()
		}
		return committed
	}
	c.beginTurn(false)
	return true
}

func (c *conv) submitDurableTurn(input *durableTurnInput) error {
	p, ok := c.proc.(durableTurnProcess)
	if !ok {
		return errors.New("the backend does not accept durable turn input")
	}
	c.stopIdle()
	c.stopBgGrace()
	c.turnMark, c.reply = c.rec.TurnMark, ""
	c.snapshotBoardRevision()
	c.setStatus(func() { c.turn, c.lastErr = true, "" })
	c.syncAwake()
	if err := p.submitTurn(input.ID, input.Text, c.rec.DurableSteers, input.Operator); err != nil {
		c.setStatus(func() { c.turn = false })
		return err
	}
	return nil
}

func (c *conv) resumeDurableTurn() {
	if checkout := c.released(); checkout != "" {
		c.refuseStart(checkoutReleasedRefusal(checkout))
		return
	}
	if c.proc == nil {
		if err := c.startProcess(); err != nil {
			if errors.Is(err, errDurableStopping) {
				return
			}
			var refused *admissionRefusal
			if errors.As(err, &refused) {
				c.refuseStart(refused)
			} else {
				c.durableTurnError("restore the durable process", err)
			}
			return
		}
		c.setStatus(func() { c.wakeRefused = false })
	}
	if _, ok := c.proc.(durableSteerProcess); ok && c.rec.Lead {
		// Include any interrupted steering claims in the resumed turn's
		// durable input, before starting the model loop again.
		c.steerDurableInbox(nil)
	}
	var err error
	refused := c.admitInput(func() {
		var release func()
		release, err = c.r.admitDurableTransition()
		if err != nil {
			return
		}
		defer release()
		err = c.submitDurableTurn(c.rec.DurableTurn)
	})
	if refused != "" {
		c.refuseStart(checkoutReleasedRefusal(refused))
		c.armIdle()
		return
	}
	if errors.Is(err, errDurableStopping) {
		return
	}
	if err != nil {
		c.durableTurnError("resume durable turn", err)
		c.stopProcess("")
		c.retryDurableTurn()
		return
	}
	c.apply(wslive.Event{Kind: wslive.KindTurnStart})
	c.watched()
}

func (c *conv) retryDurableTurn() {
	if input := c.rec.DurableTurn; input != nil && !c.closed && !c.quitting && c.durableRetried != input.ID {
		c.durableRetried = input.ID
		c.signalInbox()
	}
}

func (c *conv) durableTurnError(action string, err error) {
	c.setStatus(func() { c.lastErr = action + ": " + err.Error() })
	c.live.writeSnapshot()
	c.r.notify(c.rec.ReplyID, c.rec.Root)
}
