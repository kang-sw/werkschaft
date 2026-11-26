package wsagent

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

// spawn.go is the runner's spawned conversations: a non-lead conversation
// another session opens with its first message (Spawn) and steers by its
// id, whose settled turns are reported to that parent (Options.Report),
// waited for (Wait), and watched (Watch).
//
// A turn settles once: when it ends (done, or failed with the harness's
// error), when its process ends under it (failed), when the process it
// needed could not start or take the message (failed), or, for a turn the
// daemon's exit cut off, when the next daemon loads the record
// (interrupted). Closing the conversation settles nothing: the parent that
// closed it needs no report.

// Turn settle statuses (TurnResult.Status). TurnRunning and TurnClosed are
// Wait's alone: the wait ended before a settle, or the conversation is
// closed.
const (
	TurnDone        = "done"
	TurnFailed      = "failed"
	TurnInterrupted = "interrupted"
	TurnRunning     = "running"
	TurnClosed      = "closed"
)

// TurnResult is how a conversation's turn settled.
type TurnResult struct {
	// ID is the conversation's id (its reply-id).
	ID     string `json:"id"`
	Status string `json:"status"`
	// Message is the turn's final assistant message; Error the failure of
	// a failed or interrupted turn.
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	// Delivered marks a conversation parent's wait answered with a turn
	// whose report had already reached that parent (steered into its turn
	// or kept for its next one): Message and Error are left out, since
	// the parent read them there.
	Delivered bool `json:"delivered,omitempty"`
	// n counts the conversation's settles so far (0: none yet), and mark
	// is the Runner.Send count the settled turn's messages covered.
	n, mark uint64
}

// TurnReport is one settled turn of a conversation with a parent, for
// Options.Report.
type TurnReport struct {
	Parent ParentRef
	// Child is the conversation's state after the settle.
	Child  State
	Result TurnResult
	// Text, when set, is the report's text in place of ReportText's: a
	// usage-limit failure's exhaustion report (quota_hop.go), which
	// Parent may name past the child's own parent.
	Text string
}

// SpawnSpec is a spawned conversation's launch: Runner.Spawn.
type SpawnSpec struct {
	// Profile is a non-lead agent profile; Root anchors mail and config
	// (absolute). A Pi worker's ws session key is bound to Cwd instead.
	Profile string
	Root    string
	// Cwd is the directory its process runs in: absolute, an existing
	// directory; empty is Root.
	Cwd string
	// Model and Effort replace the profile's; empty keeps it.
	Model  string
	Effort string
	// provider is the credential provider from the selected tier binding.
	provider string
	// Prompt is the first user message; required.
	Prompt string
	// Label is the conversation's one-line name; empty is none.
	Label string
	// Parent is the session the settled turns are reported to; nil
	// reports none.
	Parent *ParentRef
	// Tier is the capability tier the model was chosen by (small, medium,
	// large, or xlarge): the cap of the tiers the conversation's own
	// spawns may ask for (children.go). Empty records none, and such a
	// conversation spawns nothing.
	Tier string
	// PermissionLevel is this worker's common permission input. A non-empty
	// value is stored as its explicit selection (permission.json); omission
	// keeps legacy derivation with no writeback. Every child is re-admitted
	// against its parent's current level at creation and process start.
	PermissionLevel PermissionLevel
	// PermissionFullAck accompanies a trusted user's explicit full choice.
	// It is not authority by itself; only the package's trusted control owner
	// may set permissionUser.
	PermissionFullAck bool
	permissionUser    bool
	// delegatedAuthority is set only by agent.spawn, whose authority is an
	// already-bounded delegation rather than a generic worker raise.
	delegatedAuthority bool
	// spawnerBackend is the spawning caller's verified backend
	// (AgentCaller.VerifiedBackend): the create gate admits the worker for
	// it (admitWorker), and the record keeps it (Record.SpawnerBackend).
	// Empty is none.
	spawnerBackend string
	// policyKey is the spawning caller's ws key (AgentCaller.PolicyKey): the
	// child's key inherits that session's config. Empty is none.
	policyKey string
	// SystemPrompt is appended to the harness's system prompt at every
	// process start; empty is none.
	SystemPrompt string
	// claimWidth transfers an AgentSpawn width reservation when startConv
	// registers the conversation under r.mu.
	claimWidth func()
}

// apply sets the spawn's choices on a new conversation's record.
func (spec *SpawnSpec) apply(rec *Record) {
	if spec.Cwd != rec.Root {
		rec.Cwd = spec.Cwd
	}
	if spec.Model != "" {
		rec.Model = spec.Model
	}
	if spec.Effort != "" {
		rec.Effort = spec.Effort
	}
	if spec.provider != "" {
		rec.Provider = spec.provider
	}
	rec.Label = spec.Label
	rec.Tier = spec.Tier
	rec.SystemPrompt = spec.SystemPrompt != ""
	if spec.Parent != nil {
		parent := *spec.Parent
		rec.Parent = &parent
	}
}

// labelRunes bounds SpawnSpec.Label.
const labelRunes = 120

// Spawn opens a non-lead conversation of spec.Profile and sends it
// spec.Prompt, in one call: the conversation either exists with its first
// message held or is not opened at all. The process starts as for any
// first send; a launch failure settles the turn as failed.
func (r *Runner) Spawn(spec SpawnSpec) (State, error) {
	if strings.TrimSpace(spec.Profile) == "" {
		return State{}, badRequest("profile is required")
	}
	if strings.TrimSpace(spec.Prompt) == "" {
		return State{}, badRequest("prompt is empty")
	}
	if spec.Model != "" && !modelPattern.MatchString(spec.Model) {
		return State{}, badRequest("model %q must match %s", spec.Model, modelPattern.String())
	}
	if spec.Effort != "" && !effortPattern.MatchString(spec.Effort) {
		return State{}, badRequest("effort %q must be a lowercase word", spec.Effort)
	}
	if p := spec.Parent; p != nil && (strings.TrimSpace(p.Kind) == "" || !wsdashboard.ValidReplyID(p.ReplyID)) {
		return State{}, badRequest("parent must name a kind and a valid reply-id")
	}
	if spec.PermissionLevel != "" && spec.PermissionLevel.rank() == 0 {
		return State{}, badRequest("permission_level must be read-only, workdir-write or full")
	}
	if isAgentWorkerProfile(strings.TrimSpace(spec.Profile)) && (spec.Parent == nil || spec.PermissionLevel.rank() == 0) {
		// Its ceiling is its authority (agent_tools.go): a spawn without
		// one would run the common loadout unchecked.
		return State{}, badRequest("profile %q is agent.spawn's internal profile: it needs a parent and an authority", spec.Profile)
	}
	if spec.Tier != "" && tierRank(spec.Tier) == 0 {
		return State{}, badRequest("tier must be small, medium, large, or xlarge; got %q", spec.Tier)
	}
	spec.Root = strings.TrimSpace(spec.Root)
	if !filepath.IsAbs(spec.Root) {
		return State{}, badRequest("root must be an absolute path; got %q", spec.Root)
	}
	spec.Root = filepath.Clean(spec.Root)
	cwd := strings.TrimSpace(spec.Cwd)
	if cwd == "" {
		cwd = spec.Root
	}
	if !filepath.IsAbs(cwd) {
		return State{}, badRequest("cwd must be an absolute path; got %q", cwd)
	}
	cwd = filepath.Clean(cwd)
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return State{}, badRequest("cwd %s is not a directory", cwd)
	}
	spec.Cwd = cwd
	spec.Label = cleanLabel(spec.Label)
	return r.create(spec.Profile, spec.Root, nil, &spec)
}

// cleanLabel is label's first non-blank line, at most labelRunes runes.
func cleanLabel(label string) string {
	for _, line := range strings.Split(label, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if runes := []rune(line); len(runes) > labelRunes {
				line = string(runes[:labelRunes-1]) + "…"
			}
			return line
		}
	}
	return ""
}

// spawned sends a new spawned conversation its first message. A failed send
// closes the conversation again, so a spawn that fails leaves nothing
// open.
func (r *Runner) spawned(c *conv, prompt string) (State, error) {
	st, err := r.send(c.rec.ReplyID, prompt, false)
	c.mu.Lock()
	c.spawnPending = false
	c.mu.Unlock()
	if err != nil {
		_, _ = r.CloseConversation(c.rec.ReplyID)
		return State{}, err
	}
	c.watched()
	return st, nil
}

// sendCount is Runner.Send's count so far.
func (c *conv) sendCount() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sends
}

// lastError is State.Error.
func (c *conv) lastError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// settleTurn records a settled turn whose messages covered mark sends, wakes
// the waits, and reports it to the parent. A done turn of a conversation
// whose children's reports it still waits for is held instead (holdsReport,
// children.go): neither reported nor a wait's answer, until a later turn
// settles, or releaseHeld finds nothing left to wait for. The loop calls
// it.
func (c *conv) settleTurn(status, errText string, mark uint64) error {
	res := TurnResult{ID: c.rec.ReplyID, Status: status, Error: errText, mark: mark}
	if status == TurnDone || status == TurnFailed {
		res.Message = c.reply
	}
	if status == TurnDone && c.holdsReport() {
		if c.r.durableProcess(c.rec.Backend) {
			if err := c.holdDurable(res); err != nil {
				return err
			}
		} else {
			c.hold(&res)
		}
		c.reply = ""
		c.refreshShellHold()
		c.watched()
		return nil
	}
	if !c.r.durableProcess(c.rec.Backend) {
		c.hold(nil)
	}
	if err := c.publish(res, false); err != nil {
		return err
	}
	c.reply = ""
	return nil
}

// finishActiveTurn closes a child turn once, including on interruption. Clearing
// its start in the same record update prevents a replay from counting it twice.
func finishActiveTurn(rec *Record, now int64) {
	if rec.Parent == nil || !rec.TurnOpen || rec.TurnStartedAt == 0 {
		return
	}
	rec.ActiveMs += max(now-rec.TurnStartedAt, 0)
	rec.TurnStartedAt = 0
}

// hold sets the done turn held for the children (heldRes), nil for none,
// and mirrors whether one is held under mu for openChildOf. It runs in the
// loop.
func (c *conv) hold(res *TurnResult) {
	c.heldRes = res
	if res != nil {
		// A wait after a restart waits for this held turn, not its predecessor.
		c.update(func(rec *Record) {
			finishActiveTurn(rec, c.r.opts.Now().UnixMilli())
			rec.HeldTurn, rec.TurnOpen, rec.LastTurn = res, false, nil
		})
	}
	c.mu.Lock()
	c.holding = res != nil
	c.mu.Unlock()
}

// publish records res as the conversation's last settled turn and reports
// it: to Options.Report, and to a conversation parent through
// Record.PendingReports, unless that parent's own wait took it (wait). The
// settle, the turn-open mark's clearing, and the pending report are one
// record update, so a parent never reads its child between them. keepOpen
// preserves a successor's boundary when reporting a result held earlier.
func (c *conv) publish(res TurnResult, keepOpen bool) error {
	return c.publishWith(res, keepOpen, nil)
}

// publishWith is publish with also applied to the record in the same write,
// before the settle: a successor's boundary that must not land without it.
func (c *conv) publishWith(res TurnResult, keepOpen bool, also func(rec *Record)) error {
	parent := c.rec.Parent
	toConv := parent != nil && parent.Kind == ParentConversation
	waited := false
	exhausted := c.noteExhaustion(res)
	settle := func() {
		res.n = c.settled.n + 1
		c.settled = res
		c.settledReport = ""
	}
	if c.r.durableProcess(c.rec.Backend) {
		var err error
		waited, err = c.publishDurableRecord(res, keepOpen, also)
		if err != nil {
			return err
		}
	} else if parent == nil && !c.rec.TurnOpen && also == nil {
		c.mu.Lock()
		settle()
		c.mu.Unlock()
	} else {
		c.update(func(rec *Record) {
			if also != nil {
				also(rec)
			}
			settle()
			if !keepOpen {
				finishActiveTurn(rec, c.r.opts.Now().UnixMilli())
				rec.TurnOpen = false
			}
			rec.HeldTurn = nil
			if toConv {
				if waited = c.parentWaitTakes(res.mark); !waited {
					id := newReportID()
					rec.PendingReports = append(rec.PendingReports, PendingReport{ID: id, Result: res})
					c.settledReport = id
				}
			}
			if parent != nil {
				rec.ReportID = newReportID()
				rec.LastTurn = &PendingReport{ID: c.settledReport, Result: res, Mark: res.mark}
				// A settled turn resets the waiting expiry (agent_tools.go).
				rec.LastActiveAt = c.r.opts.Now().UTC().Format(time.RFC3339)
			}
		})
	}
	c.signalSettled()
	if parent == nil {
		return nil
	}
	if !waited {
		// A wait that took the turn answered with it; nothing is routed.
		c.keepExhaustion(exhausted)
	}
	c.reportLast()
	st := c.state()
	if toConv && !waited {
		c.r.deliverLater(c.rec.ReplyID)
	}
	c.r.emit(st)
	return nil
}

// reportLast uses the existing append/report path. Marking after the callback
// accepts one duplicate mail if the daemon dies between append and this write.
func (c *conv) reportLast() {
	last := c.rec.LastTurn
	if last == nil || c.rec.Parent == nil || c.rec.ReportID == "" || c.rec.ReportID == c.rec.LastReportedTurn {
		return
	}
	if suppress := c.r.opts.suppressReportDelivery; suppress != nil && suppress(c.record()) {
		return
	}
	rep := TurnReport{Parent: *c.rec.Parent, Child: c.state(), Result: last.Result}
	if c.exhaustReport != "" && c.exhaustReport == c.rec.ReportID {
		note := c.exhaustNotice
		c.exhaustReport, c.exhaustNotice = "", wslive.Notice{}
		if c.rec.Parent.Kind != ParentConversation {
			// The exhaustion report replaces this generic one: one report,
			// to the lead or past it (quota_hop.go). A conversation
			// parent's goes through deliver instead.
			if route := c.r.exhaustionRoute(c.record()); route.ok {
				c.handOffExhaustion(route, c.r.exhaustionText(c.record(), last.Result, note, route), rep, "")
				c.update(func(rec *Record) { rec.LastReportedTurn = rec.ReportID })
				return
			}
		}
	}
	if report := c.r.opts.Report; report != nil {
		report(rep)
	}
	c.update(func(rec *Record) { rec.LastReportedTurn = rec.ReportID })
}

// interrupted settles the turn a daemon's exit cut off, which the record
// still marks open; the loop calls it once, when the record is loaded.
func (c *conv) interrupted() {
	if c.closed || !c.rec.TurnOpen {
		return
	}
	status, reason := TurnInterrupted, "the dashboard daemon exited while the turn ran"
	if refusal := c.r.admitWorkerRecord(c.record()); refusal != nil {
		// A cross-harness worker whose holder a daemon ended just before it
		// died: the turn fails as the retirement would have settled it.
		status, reason = TurnFailed, refusal.Error()
	}
	// Reconcile the lost turn before any queued input can open its successor.
	// Settlement alone wakes waiters but leaves list/send's running flags stale.
	c.setStatus(func() { c.turn, c.proc, c.lastErr = false, nil, reason })
	c.events = nil
	c.replaying, c.replayAsked, c.replayGap, c.replayBoundary, c.wrapHold = false, false, false, false, false
	c.apply(wslive.Event{Kind: wslive.KindTurnEnd})
	c.settleTurn(status, reason, c.turnMark)
}

// signalSettled wakes every Wait.
func (c *conv) signalSettled() {
	c.mu.Lock()
	close(c.settleC)
	c.settleC = make(chan struct{})
	c.mu.Unlock()
}

// watched tells the watchers a conversation with a parent changed.
func (c *conv) watched() {
	if c.record().Parent != nil {
		c.r.emit(c.state())
	}
}

// Watch calls fn with a spawned conversation's state (one with a parent)
// whenever it opens, starts or settles a turn, or closes, until stop is
// called. fn runs on the conversation's loop or on the caller of Spawn or
// CloseConversation: it must return promptly. Only read-only Get, List, and
// Children calls are safe; it must never wait on a conversation's loop.
func (r *Runner) Watch(fn func(State)) (stop func()) {
	key := &fn
	r.watchMu.Lock()
	if r.watchers == nil {
		r.watchers = map[*func(State)]struct{}{}
	}
	r.watchers[key] = struct{}{}
	r.watchMu.Unlock()
	return func() {
		r.watchMu.Lock()
		delete(r.watchers, key)
		r.watchMu.Unlock()
	}
}

func (r *Runner) emit(st State) {
	r.wakeSpawnParents(st)
	r.watchMu.Lock()
	fns := make([]func(State), 0, len(r.watchers))
	for fn := range r.watchers {
		fns = append(fns, *fn)
	}
	r.watchMu.Unlock()
	for _, fn := range fns {
		fn(st)
	}
}

// Children lists the conversations, open or closed, that parent spawned,
// oldest first.
func (r *Runner) Children(parent ParentRef) []State {
	r.mu.Lock()
	var out []State
	var open []*conv
	for id := range r.childrenIndex[parent] {
		if c := r.convs[id]; c != nil {
			open = append(open, c)
		} else if rec, ok := r.closed[id]; ok {
			out = append(out, r.memoryClosedState(rec))
		}
	}
	r.mu.Unlock()
	for _, c := range open {
		out = append(out, c.memoryState())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// indexChildLocked maintains immutable parent edges; closed children stay in
// the index so an already displayed running row can settle after close.
func (r *Runner) indexChildLocked(rec Record) {
	if rec.Parent == nil {
		return
	}
	if r.childrenIndex == nil {
		r.childrenIndex = map[ParentRef]map[string]bool{}
	}
	ids := r.childrenIndex[*rec.Parent]
	if ids == nil {
		ids = map[string]bool{}
		r.childrenIndex[*rec.Parent] = ids
	}
	ids[rec.ReplyID] = true
}

// Wait blocks until a turn of conversation id that took every message
// Runner.Send had sent it when Wait was called settles, and returns how it
// settled; a turn that settled so before the call is returned at once. It
// returns TurnRunning when timeout passes or ctx ends first, and TurnClosed
// for a closed conversation whose last turn does not answer it
// (closedTurn). A turn the conversation's harness began on its
// own holds no new message, so it never answers a wait for a sent one.
func (r *Runner) Wait(ctx context.Context, id string, timeout time.Duration) (TurnResult, error) {
	return r.wait(ctx, id, timeout, false)
}

// Transcript reads conversation id's conversation ring, oldest first.
func (r *Runner) Transcript(id string) ([]wslive.RingRecord, error) {
	r.mu.Lock()
	_, open := r.convs[id]
	_, closed := r.closed[id]
	r.mu.Unlock()
	if !open && !closed {
		return nil, errUnknown
	}
	return wslive.ReadRing(wslive.RingDir(wsdashboard.SessionDir(r.opts.Dir, id)))
}
