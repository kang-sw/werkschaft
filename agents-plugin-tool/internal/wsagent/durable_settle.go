package wsagent

import (
	"slices"
	"time"
)

func (c *conv) holdDurable(res TurnResult) error {
	return c.commitDurableRecord(func(rec *Record) {
		finishActiveTurn(rec, c.r.opts.Now().UnixMilli())
		rec.HeldTurn, rec.TurnOpen, rec.LastTurn = &res, false, nil
		if rec.DurableTurn != nil {
			rec.DurableHeldReportID = rec.DurableTurn.ID
			rec.DurableTurn = nil
		}
	}, func() {
		c.heldRes, c.holding = &res, true
	})
}

// publishDurableRecord reconciles the result with its original turn or held
// report before any waiter or report callback can observe the settlement.
func (c *conv) publishDurableRecord(res TurnResult, keepOpen bool, also func(*Record)) (waited bool, err error) {
	var pendingID string
	err = c.commitDurableRecord(func(rec *Record) {
		id := rec.DurableHeldReportID
		if !keepOpen && rec.DurableTurn != nil {
			id = rec.DurableTurn.ID
		}
		if id == "" {
			id = newReportID()
		}
		if also != nil {
			also(rec)
		}
		now := c.r.opts.Now()
		if !keepOpen {
			finishActiveTurn(rec, now.UnixMilli())
			rec.TurnOpen, rec.DurableTurn = false, nil
		}
		rec.HeldTurn, rec.DurableHeldReportID = nil, ""
		res.n = c.settled.n + 1
		if rec.Parent != nil {
			if rec.Parent.Kind == ParentConversation {
				waited = c.parentWaitTakes(res.mark)
				if !waited {
					pendingID = id
					rec.PendingReports = append(slices.Clone(rec.PendingReports), PendingReport{ID: id, Result: res})
				}
			}
			rec.ReportID = id
			rec.LastActiveAt = now.UTC().Format(time.RFC3339)
		}
		rec.LastTurn = &PendingReport{ID: pendingID, Result: res, Mark: res.mark}
	}, func() {
		c.settled, c.settledReport = res, pendingID
		c.heldRes, c.holding = nil, false
	})
	return waited, err
}
