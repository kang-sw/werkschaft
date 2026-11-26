package wsagent

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// quota_hop.go routes a worker's usage-limit failure past exhausted
// ancestors.
//
//   - Exhaustion. A spawned conversation's turn that fails after a usage-limit
//     alert (NoticeUsageLimit) is reported by an exhaustion report in place of
//     the generic one (ReportText): which turn this was is in-memory only
//     (conv.exhausted, conv.exhaustReport), so a report still pending across a
//     daemon restart goes out generic.
//   - Walk. The report goes to the nearest ancestor that is not exhausted
//     itself (exhaustionRoute): Record.Parent upward, a cycle stopping the
//     walk, each hosted ancestor read under its own mu (quotaView). An
//     exhausted ancestor is skipped and never woken; the child's pending
//     report leaves its record, so its parent's hold on it ends. An external
//     lead (a ParentLead no hosted conversation answers) ends the walk.
//   - All exhausted. With no healthy ancestor the report goes to the
//     outermost one under the ALL ANCESTORS header. A hosted lead whose quota
//     continuation is scheduled for later keeps it in Record.Reports without a
//     turn (parkReport): the continuation's own turn carries it, and no other
//     kick starts a turn on parked reports alone (reportsParked).

// exhaustRoute is where an exhaustion report goes: a hosted ancestor (conv),
// reached through ref, or an external lead (conv nil). skipped are the
// addresses of the exhausted hosted ancestors passed, nearest first; all
// marks that no healthy ancestor was found.
type exhaustRoute struct {
	ok      bool
	conv    *conv
	view    quotaView
	ref     ParentRef
	skipped []string
	all     bool
}

// quotaView is what the walk reads of a hosted ancestor, under its mu.
type quotaView struct {
	closed         bool
	lead           bool
	parent         *ParentRef
	failure        *wslive.Notice
	qc             *QuotaContinuation
	backend, model string
	rec            Record
}

func (c *conv) quotaView() quotaView {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := quotaView{
		closed:  c.closed,
		lead:    c.rec.Lead,
		qc:      cloneQuotaContinuation(c.rec.QuotaContinuation),
		backend: c.rec.Backend,
		model:   c.rec.Model,
		rec:     c.rec,
	}
	if p := c.rec.Parent; p != nil {
		parent := *p
		v.parent = &parent
	}
	if f := c.failure; f != nil {
		n := *f
		v.failure = &n
	}
	return v
}

// quotaExhausted reports whether a hosted ancestor's quota is out at nowMs:
// a usage-limit failure whose reset has not passed, a quota continuation not
// yet proven (exhausted, in flight, or scheduled for later), or a cached
// reset time still ahead for its backend and model.
func (r *Runner) quotaExhausted(v quotaView, nowMs int64) bool {
	if f := v.failure; f != nil && f.Kind == wslive.NoticeUsageLimit && (f.ResetsAt <= 0 || nowMs < f.ResetsAt) {
		return true
	}
	if qc := v.qc; qc != nil {
		switch qc.Status {
		case QuotaContinuationExhausted, QuotaContinuationInFlight:
			return true
		case QuotaContinuationScheduled:
			if qc.ResumeAt > nowMs {
				return true
			}
		}
	}
	if lookup := r.opts.QuotaResetAt; lookup != nil && lookup(v.backend, v.model) > nowMs {
		return true
	}
	return false
}

// exhaustionRoute walks child's ancestors for its exhaustion report. Not ok
// is no ancestor to hand it to: its parent is closed or unknown.
func (r *Runner) exhaustionRoute(child Record) exhaustRoute {
	nowMs := r.opts.Now().UnixMilli()
	seen := map[string]bool{child.ReplyID: true}
	type hop struct {
		c    *conv
		view quotaView
		ref  ParentRef
	}
	var chain []hop
	ref := child.Parent
	for ref != nil && !seen[ref.ReplyID] {
		seen[ref.ReplyID] = true
		a := r.conv(ref.ReplyID)
		if a == nil {
			if ref.Kind != ParentConversation {
				// A lead no hosted conversation answers: the external top.
				var skipped []string
				for _, h := range chain {
					skipped = append(skipped, AgentAddress(h.view.rec.ReplyID))
				}
				return exhaustRoute{ok: true, ref: *ref, skipped: skipped, all: len(chain) > 0}
			}
			break
		}
		v := a.quotaView()
		if v.closed {
			break
		}
		if !r.quotaExhausted(v, nowMs) {
			var skipped []string
			for _, h := range chain {
				skipped = append(skipped, AgentAddress(h.view.rec.ReplyID))
			}
			return exhaustRoute{ok: true, conv: a, view: v, ref: *ref, skipped: skipped}
		}
		chain = append(chain, hop{c: a, view: v, ref: *ref})
		ref = v.parent
	}
	if len(chain) == 0 {
		return exhaustRoute{}
	}
	// Every hosted ancestor is exhausted (or the walk met a cycle, a closed
	// or an unknown one): the outermost reached takes it.
	top := chain[len(chain)-1]
	var skipped []string
	for _, h := range chain[:len(chain)-1] {
		skipped = append(skipped, AgentAddress(h.view.rec.ReplyID))
	}
	return exhaustRoute{ok: true, conv: top.c, view: top.view, ref: top.ref, skipped: skipped, all: true}
}

// exhaustionText is the exhaustion report: a header naming the child, its
// key lines, and the action lines that apply to the recipient.
func (r *Runner) exhaustionText(child Record, res TurnResult, note wslive.Notice, route exhaustRoute) string {
	var b strings.Builder
	head := "QUOTA EXHAUSTED"
	if route.all {
		head = "ALL ANCESTORS QUOTA-EXHAUSTED"
	}
	label := child.Label
	if label == "" {
		label = "(no label)"
	}
	address := AgentAddress(child.ReplyID)
	fmt.Fprintf(&b, "%s: %s (%s)\n", head, label, address)
	model := child.Model
	if model == "" {
		model = "default"
	}
	fmt.Fprintf(&b, "backend: %s\nmodel: %s\n", child.Backend, model)
	errText := strings.Join(strings.Fields(res.Error), " ")
	if errText == "" {
		errText = strings.Join(strings.Fields(note.Text), " ")
	}
	if errText == "" {
		errText = "usage limit reached"
	}
	fmt.Fprintf(&b, "error: %s\n", errText)
	resets := "unknown"
	if note.ResetsAt > 0 {
		resets = fmt.Sprintf("%s (%d)", time.UnixMilli(note.ResetsAt).UTC().Format(time.RFC3339), note.ResetsAt)
	}
	fmt.Fprintf(&b, "resets_at: %s\n", resets)
	skipped := "none"
	if len(route.skipped) > 0 {
		skipped = strings.Join(route.skipped, ", ")
	}
	fmt.Fprintf(&b, "skipped_ancestors: %s\n", skipped)
	b.WriteString("action:\n")
	fmt.Fprintf(&b, "- the worker keeps its conversation: resume it with mailbox.send(to: %q) once its quota is back\n", address)
	if route.conv != nil && route.view.lead && note.ResetsAt > 0 {
		at := time.UnixMilli(note.ResetsAt).UTC().Add(time.Minute).Truncate(time.Second).Format(time.RFC3339)
		fmt.Fprintf(&b, "- wake yourself after the reset with a one-shot schedule: ws/cron add {name, at: %q, prompt}\n", at)
	}
	if alts := r.alternateTiers(child, route); len(alts) > 0 {
		fmt.Fprintf(&b, "- or move the work to another harness: agent.spawn with tier %s\n", strings.Join(alts, ", "))
	}
	return b.String()
}

// alternateTiers are the tiers the recipient may spawn on a harness other
// than the exhausted worker's, empty when none is available.
func (r *Runner) alternateTiers(child Record, route exhaustRoute) []string {
	var caller AgentCaller
	if route.conv != nil {
		caller = r.holderCaller(route.view.rec)
	} else {
		cfg := r.opts.Config
		cfg.RepoRoot = child.Root
		caller = LeadCaller(route.ref.ReplyID, child.Root, "", cfg)
	}
	tiers, err := r.AgentTiers(caller)
	if err != nil {
		return nil
	}
	exhausted := TierRuntimeHarness(child.Backend)
	var out []string
	for _, e := range tiers.Ext {
		h, _, ok := strings.Cut(e.Tier, ".")
		if !ok || h == child.Backend || TierRuntimeHarness(h) == exhausted {
			continue
		}
		out = append(out, e.Tier)
	}
	return out
}

// noteExhaustion marks the turn res settles as a usage-limit failure when
// it is one: a failed turn of a spawned conversation whose turn ended after
// a usage-limit alert. It runs in the loop, in publish.
func (c *conv) noteExhaustion(res TurnResult) *wslive.Notice {
	limited := c.usageLimitSettled
	c.usageLimitSettled = false
	if !limited || res.Status != TurnFailed || c.rec.Lead || c.rec.Parent == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure == nil || c.failure.Kind != wslive.NoticeUsageLimit {
		return nil
	}
	n := *c.failure
	return &n
}

// keepExhaustion keeps the notice for the reports publish just recorded:
// the pending report a conversation parent takes (deliver) and the report
// Options.Report gets (reportLast). It runs in the loop.
func (c *conv) keepExhaustion(n *wslive.Notice) {
	if n == nil {
		return
	}
	if c.settledReport != "" {
		if c.exhausted == nil {
			c.exhausted = map[string]wslive.Notice{}
		}
		c.exhausted[c.settledReport] = *n
	}
	c.exhaustReport, c.exhaustNotice = c.rec.ReportID, *n
}

// routeExhausted hands each pending exhaustion report whose route leads
// past the conversation parent to its recipient, and returns the texts of
// those that still go to that parent. It runs in the child's loop.
func (c *conv) routeExhausted(parent *conv) map[string]string {
	if len(c.exhausted) == 0 {
		return nil
	}
	texts := map[string]string{}
	for _, p := range slices.Clone(c.rec.PendingReports) {
		note, ok := c.exhausted[p.ID]
		if !ok {
			continue
		}
		delete(c.exhausted, p.ID)
		for range 8 {
			rec := c.record()
			route := c.r.exhaustionRoute(rec)
			if !route.ok {
				break
			}
			text := c.r.exhaustionText(rec, p.Result, note, route)
			if route.conv != nil && route.conv == parent && route.ref.Kind == ParentConversation && !route.all {
				texts[p.ID] = text
				break
			}
			rep := TurnReport{Child: c.state(), Result: p.Result}
			if c.handOffExhaustion(route, text, rep, p.ID) {
				break
			}
		}
	}
	return texts
}

// handOffExhaustion delivers an exhaustion report along route, dropping
// pendingID (when set) from the child's pending reports in the same step:
// parked on an all-exhausted hosted lead whose continuation is scheduled,
// taken by a hosted conversation parent's loop, else mailed to the lead
// route.ref names (Options.Report). False is a hosted recipient that closed
// meanwhile. It runs in the child's loop.
func (c *conv) handOffExhaustion(route exhaustRoute, text string, rep TurnReport, pendingID string) bool {
	drop := func() {
		if pendingID == "" {
			return
		}
		c.update(func(rec *Record) {
			rec.PendingReports = slices.DeleteFunc(slices.Clone(rec.PendingReports), func(p PendingReport) bool { return p.ID == pendingID })
		})
	}
	defer func() {
		// Its parent no longer waits on the report: a done turn held for it
		// may go now (releaseHeld).
		if pendingID != "" && c.rec.Parent != nil && c.rec.Parent.Kind == ParentConversation && (route.conv == nil || route.view.rec.ReplyID != c.rec.Parent.ReplyID) {
			c.r.releaseLater(c.rec.Parent.ReplyID)
		}
	}()
	if t := route.conv; t != nil {
		delivered, gone := false, true
		t.do(func() {
			if t.closed {
				return
			}
			gone = false
			if route.all && t.parkable() {
				drop()
				t.parkReport(text)
				delivered = true
				return
			}
			if route.ref.Kind == ParentConversation {
				drop()
				t.takeReport(text)
				delivered = true
			}
		})
		if delivered {
			return true
		}
		if gone && route.ref.Kind == ParentConversation {
			pendingID = ""
			return false
		}
	}
	drop()
	if report := c.r.opts.Report; report != nil {
		rep.Parent, rep.Text = route.ref, text
		report(rep)
	}
	return true
}

// parkable reports whether a hosted lead keeps an exhaustion report for its
// scheduled continuation: one is scheduled for later. It runs in the loop.
func (c *conv) parkable() bool {
	qc := c.rec.QuotaContinuation
	return c.rec.Lead && qc != nil && qc.Status == QuotaContinuationScheduled && qc.ResumeAt > c.r.opts.Now().UnixMilli()
}

// parkReport keeps an exhaustion report in Record.Reports for the scheduled
// continuation's turn: no kick, and the continuation stays. It runs in the
// loop.
func (c *conv) parkReport(text string) {
	allParked := len(c.rec.Reports) == 0 || c.reportsParked()
	c.update(func(rec *Record) { rec.Reports = append(slices.Clone(rec.Reports), text) })
	if allParked {
		c.parkedReports = len(c.rec.Reports)
	}
	c.changed(false)
}

// reportsParked reports whether every report the conversation keeps is
// parked for a continuation still scheduled for later: none of them may
// start a turn on its own. It runs in the loop.
func (c *conv) reportsParked() bool {
	n := len(c.rec.Reports)
	if n == 0 || c.parkedReports != n || !c.rec.Lead {
		return false
	}
	qc := c.rec.QuotaContinuation
	if qc == nil || qc.Status != QuotaContinuationScheduled {
		return false
	}
	return qc.ResumeAt > c.r.opts.Now().UnixMilli() || c.quotaC != nil || c.quotaPendingFire
}

// restoredParkedReports is the parked count of a loaded record: every
// report a lead keeps while its continuation is scheduled for later, since
// an ordinary one would have started its turn before the restart.
func restoredParkedReports(rec Record, nowMs int64) int {
	qc := rec.QuotaContinuation
	if !rec.Lead || len(rec.Reports) == 0 || qc == nil || qc.Status != QuotaContinuationScheduled || qc.ResumeAt <= nowMs {
		return 0
	}
	return len(rec.Reports)
}
