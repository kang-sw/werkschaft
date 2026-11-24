// agent_delivery.go holds child delivery, expiry, retention, and token routing.
package wsagent

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

func reportSummary(res TurnResult) string {
	line, _, _ := strings.Cut(strings.TrimSpace(res.Message), "\n")
	if runes := []rune(line); len(runes) > labelRunes {
		line = string(runes[:labelRunes-1]) + "…"
	}
	if line == "" && res.Error != "" {
		line = strings.Join(strings.Fields(res.Error), " ")
	}
	if line == "" {
		return res.Status
	}
	return res.Status + ": " + line
}

// Delivery is one message for the delivery seam: its text and the sender's
// reply-id, told when the wake it asks is refused ("" tells no one).
type Delivery struct {
	From string
	Text string
}

type pendingDelivery struct {
	name   string
	sender string
}

// Deliver hands d to conversation id as its next input, waking it when it
// sleeps; the width cap does not apply. When it sleeps and its admission
// is refused now, the message still queues, it stays asleep, and the
// sender gets one notice (Options.WakeRefused).
func (r *Runner) Deliver(id string, d Delivery) (State, error) {
	var refusal error
	var dead *admissionRefusal
	checkout := ""
	if c := r.conv(id); c != nil {
		c.mu.Lock()
		asleep := c.proc == nil && !c.turn
		rec := c.rec
		// A running process is checked from memory: its checkouts were
		// settled when it was loaded, and a release's notice updates them.
		// A sleeping one is about to start, a cold path, so its markers are
		// read: a release whose notice never reached this daemon still
		// refuses the wake and tells the sender.
		if asleep {
			checkout = c.releasedOnDiskLocked()
		} else {
			checkout = c.releasedLocked()
		}
		c.mu.Unlock()
		dead = r.deadBindingRefusal(rec, checkout)
		switch {
		case checkout != "":
			// Its checkout was released after it was bound: no launch, wake
			// or resume there, live process or not.
			refusal = checkoutReleasedRefusal(checkout)
		case asleep:
			// Ahead of the profile: its edits never mask a cross-harness refusal.
			if refusal = r.admitWorkerRecord(rec); refusal == nil {
				if rp, resolveErr := r.resolve(rec.Profile, rec.Root); resolveErr != nil {
					refusal = resolveErr
				} else {
					_, _, refusal = r.childAdmission(rec, rp.Definition)
				}
			}
		}
	}
	sender := d.From
	if refusal != nil {
		// The notice below owns this send, even if close wins immediately
		// after enqueue. A later close only tells senders still owed notice.
		sender = ""
	}
	st, err := r.sendFrom(id, d.Text, false, sender)
	if err != nil {
		return st, err
	}
	if refusal != nil {
		if dead != nil {
			r.tellUndeliverable(d.From, id, dead)
		} else {
			r.tellRefused(d.From, id, checkout, refusal.Error())
		}
	}
	return st, nil
}

// mailRelation is how sender, a reply-id, relates to rec through the
// Runner spawn lineage (Record.Parent): "parent" for rec's parent, "child"
// for a conversation rec spawned, else "peer"; "" for no sender (system
// mail). It is computed at each delivery, never stored.
func (r *Runner) mailRelation(rec Record, sender string) string {
	switch {
	case sender == "":
		return ""
	case rec.Parent != nil && rec.Parent.ReplyID == sender:
		return "parent"
	}
	if s, ok := r.recordOf(sender); ok && s.Parent != nil && s.Parent.ReplyID == rec.ReplyID {
		return "child"
	}
	return "peer"
}

// SpawnLabel is the label its spawner gave the spawned conversation whose
// reply-id is replyID (Record.Label of a conversation with a Parent), ""
// for a conversation the runner does not know or one nobody spawned. Mail
// delivery reads it for the sender legend; it reads memory only.
func (r *Runner) SpawnLabel(replyID string) string {
	rec, ok := r.recordOf(replyID)
	if !ok || rec.Parent == nil {
		return ""
	}
	return rec.Label
}

// childWaitingExpiry is how long a child may wait before it is deleted:
// agent.child_waiting_days, read once per runner.
func (r *Runner) childWaitingExpiry() time.Duration {
	r.expiryOnce.Do(func() {
		days := DefaultChildWaitingDays
		if r.opts.ChildWaitingDays > 0 {
			days = r.opts.ChildWaitingDays
		} else {
			resolver := wsconfig.NewResolver(r.opts.Config, map[string]string{wsconfig.ItemAgentChildWaitingDays: strconv.Itoa(DefaultChildWaitingDays)}, nil, nil)
			if rv, err := resolver.Get("", wsconfig.ItemAgentChildWaitingDays); err == nil {
				if n, err := strconv.Atoi(strings.TrimSpace(rv.Value)); err == nil && n > 0 {
					days = n
				}
			}
		}
		r.expiry = time.Duration(days) * 24 * time.Hour
	})
	return r.expiry
}

// childWaitingLocked reports a spawned child asleep with no turn and no
// input or claimed mail about to start one (a refused wake's does not
// count). Under mu.
func (c *conv) childWaitingLocked() bool {
	return !c.closed && !c.rec.Lead && c.rec.Parent != nil && c.proc == nil && !c.turn && !c.spawnPending &&
		(c.wakeRefused || c.sends <= max(c.settled.mark, c.voided) && len(c.rec.Mail) == 0)
}

// lastActive is a spawned record's expiry clock start.
func lastActive(rec Record) time.Time {
	for _, v := range []string{rec.LastActiveAt, rec.CreatedAt} {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// expireIfDue has the runner delete a child waiting past its expiry
// (expireLater). It runs in the loop, on the heartbeat: no file is read.
func (c *conv) expireIfDue() {
	c.mu.Lock()
	due := !c.expiring && c.childWaitingLocked()
	rec := c.rec
	c.mu.Unlock()
	if !due {
		return
	}
	at := lastActive(rec)
	if at.IsZero() || c.r.opts.Now().Sub(at) < c.r.childWaitingExpiry() {
		return
	}
	// The expiry walk needs every open descendant registered before it can
	// decide whether closing the ancestor is safe.
	if !c.r.startupReady() {
		return
	}
	c.expiring = true
	c.r.expireLater(rec.ReplyID)
}

// expireLater deletes waiting child id and its descendants, deepest first,
// off the caller's goroutine, unless a descendant is awake or id woke
// meanwhile.
func (r *Runner) expireLater(id string) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.expire(id)
	}()
}

// expire is expireLater's body; it reports whether id was deleted.
func (r *Runner) expire(id string) bool {
	c := r.conv(id)
	if c == nil {
		return false
	}
	c.mu.Lock()
	waiting := c.childWaitingLocked()
	c.mu.Unlock()
	desc := r.descendants(id)
	busy := !waiting
	for _, d := range desc {
		if dc := r.conv(d); dc != nil {
			dc.mu.Lock()
			busy = busy || dc.awakeLocked()
			dc.mu.Unlock()
		}
	}
	if busy {
		c.do(func() { c.expiring = false })
		return false
	}
	if _, err := r.CloseConversation(id); err != nil {
		if errors.Is(err, errClosed) {
			// Someone closed it meanwhile (a dashboard close): what they
			// closed is theirs to delete, not the expiry's.
			return false
		}
		fmt.Fprintf(r.opts.Log, "agent %s: expire: %v\n", id, err)
		// A later heartbeat tries again.
		c.do(func() { c.expiring = false })
		return false
	}
	for _, d := range append(desc, id) {
		r.forget(d)
	}
	return true
}

var _ wsdashboard.AgentForgetter = (*Runner)(nil)

// ForgetClosed drops a closed or handed-off conversation and any private
// record. It returns whether the caller may remove the session directory.
func (r *Runner) ForgetClosed(id string) bool {
	rec, purge := r.forgetClosed(id)
	if purge && rec.ReplyID != "" {
		r.deleteDurableRecord(rec)
	}
	return purge
}

func (r *Runner) forgetClosed(id string) (Record, bool) {
	rec, purge := r.dropClosed(id)
	// Gone from the runner for good (not merely closed, and not open under
	// the same id): its scheduled prompts go with it.
	if purge && !r.CronKnown(id) {
		r.cronGone(id)
	}
	return rec, purge
}

func (r *Runner) dropClosed(id string) (Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.closed[id]
	if ok && r.hasLiveDescendantsLocked(safeStopParent(id, rec.Lead), nil) {
		return Record{}, false
	}
	// An ended dashboard row can become visible before its conversation
	// moves from convs to closed. Check both parent kinds without taking a
	// conversation lock under the registry lock.
	if r.convs[id] != nil &&
		(r.hasLiveDescendantsLocked(safeStopParent(id, false), nil) ||
			r.hasLiveDescendantsLocked(safeStopParent(id, true), nil)) {
		return Record{}, false
	}
	delete(r.closed, id)
	delete(r.closedRows, id)
	delete(r.deadRefusals, id)
	if ok {
		r.unindexChildLocked(rec)
	}
	return rec, true
}

func (r *Runner) unindexChildLocked(rec Record) {
	if rec.Parent != nil {
		delete(r.childrenIndex[*rec.Parent], rec.ReplyID)
		if len(r.childrenIndex[*rec.Parent]) == 0 {
			delete(r.childrenIndex, *rec.Parent)
		}
	}
}

// hasLiveDescendantsLocked follows retained closed ancestry under r.mu.
// Either an open descendant or a reserved spawn still needs this record.
func (r *Runner) hasLiveDescendantsLocked(root ParentRef, startupOpen map[string]Record) bool {
	queue := []ParentRef{root}
	visited := map[string]bool{root.ReplyID: true}
	for i := 0; i < len(queue); i++ {
		parent := queue[i]
		if r.pendingSpawns[parent] > 0 {
			return true
		}
		for id := range r.childrenIndex[parent] {
			if visited[id] {
				continue
			}
			visited[id] = true
			if _, pending := startupOpen[id]; pending || r.convs[id] != nil {
				return true
			}
			if child, ok := r.closed[id]; ok {
				queue = append(queue, safeStopParent(id, child.Lead))
			}
		}
	}
	return false
}

// forget drops closed conversation id from the runner and deletes its
// session directory.
func (r *Runner) forget(id string) {
	rec, purge := r.forgetClosed(id)
	if !purge || rec.ReplyID == "" {
		return
	}
	r.deleteSessionRecord(rec)
	r.notify(id, rec.Root)
}

func (r *Runner) deleteSessionRecord(rec Record) bool {
	id := rec.ReplyID
	sessionDir := wsdashboard.SessionDir(r.opts.Dir, id)
	wslive.DropRing(wslive.RingDir(sessionDir))
	if err := os.RemoveAll(sessionDir); err != nil {
		fmt.Fprintf(r.opts.Log, "agent %s: delete: %v\n", id, err)
		return false
	}
	r.deleteDurableRecord(rec)
	return true
}

// expiredAtStart picks the open spawned children a daemon start deletes
// instead of serving, deepest first: each quiet (no turn open or held, no
// input) past its expiry whose descendants are all quiet, with those
// descendants. An open descendant beyond a closed bridge blocks the cascade
// and is evaluated separately against its own expiry. A child whose input
// waits on a refused wake is not quiet here; its loop's heartbeat deletes it
// once its start is refused again.
func (r *Runner) expiredAtStart(open []Record, dead map[string]*admissionRefusal) []string {
	now := r.opts.Now()
	expiry := r.childWaitingExpiry()
	byID := map[string]Record{}
	kids := map[ParentRef][]string{}
	var closed []Record
	closedIDs := map[string]bool{}
	add := func(rec Record) {
		byID[rec.ReplyID] = rec
		if rec.Parent != nil {
			kids[*rec.Parent] = append(kids[*rec.Parent], rec.ReplyID)
		}
	}
	for _, rec := range open {
		add(rec)
	}
	r.mu.Lock()
	for id, rec := range r.closed {
		closedIDs[id] = true
		closed = append(closed, rec)
		add(rec)
	}
	r.mu.Unlock()
	children := func(rec Record) []string {
		return kids[safeStopParent(rec.ReplyID, rec.Lead)]
	}
	quiet := func(rec Record) bool {
		mark := rec.Voided
		if rec.LastTurn != nil {
			mark = max(mark, rec.LastTurn.Mark)
		}
		return dead[rec.ReplyID] == nil && !rec.Lead && rec.Parent != nil && rec.HolderID == "" && !rec.TurnOpen && rec.HeldTurn == nil &&
			rec.Sends <= mark && len(rec.Queued)+len(rec.Mail)+len(rec.Reports)+len(rec.Steered)+len(rec.PendingReports) == 0
	}
	seen := map[string]bool{}
	var subtree func(id string, out *[]string, throughClosed bool) bool
	subtree = func(id string, out *[]string, throughClosed bool) bool {
		if seen[id] {
			return false
		}
		seen[id] = true
		closed := closedIDs[id]
		if !closed && (throughClosed || !quiet(byID[id])) {
			return false
		}
		for _, k := range children(byID[id]) {
			if !subtree(k, out, throughClosed || closed) {
				return false
			}
		}
		if !closed {
			*out = append(*out, id)
		}
		return true
	}
	var out []string
	var visit func(id string)
	visit = func(id string) {
		rec := byID[id]
		if at := lastActive(rec); !closedIDs[id] && quiet(rec) && !at.IsZero() && now.Sub(at) >= expiry {
			var tree []string
			saved := maps.Clone(seen)
			if subtree(id, &tree, false) {
				out = append(out, tree...)
				return
			}
			seen = saved
		}
		seen[id] = true
		for _, k := range children(rec) {
			if !seen[k] {
				visit(k)
			}
		}
	}
	visitRoot := func(rec Record) {
		if rec.Parent != nil {
			if parent, ok := byID[rec.Parent.ReplyID]; ok && *rec.Parent == safeStopParent(parent.ReplyID, parent.Lead) {
				return
			}
		}
		visit(rec.ReplyID)
	}
	for _, rec := range open {
		visitRoot(rec)
	}
	for _, rec := range closed {
		visitRoot(rec)
	}
	return out
}

// agentTokenRequest is the token route's body for the agent.* ops.
type agentTokenRequest struct {
	AgentSpawnRequest
	Address string `json:"address"`
}

// serveAgentToken serves a token holder's agent.* op (serveToken).
func (r *Runner) serveAgentToken(w http.ResponseWriter, req *http.Request, caller *conv, op string) {
	var body agentTokenRequest
	if !decode(w, req, &body) {
		return
	}
	ac := r.holderCaller(caller.record())
	switch op {
	case "agent.spawn":
		res, err := r.AgentSpawn(ac, body.AgentSpawnRequest)
		writeAnswer(w, http.StatusCreated, res, err)
	case "agent.interrupt":
		res, err := r.AgentInterrupt(ac, body.Address)
		writeAnswer(w, http.StatusOK, res, err)
	case "agent.list":
		writeAnswer(w, http.StatusOK, map[string]any{"agents": r.AgentList(ac)}, nil)
	case "agent.tiers":
		res, err := r.AgentTiers(ac)
		writeAnswer(w, http.StatusOK, res, err)
	default:
		http.Error(w, "unknown agent route", http.StatusNotFound)
	}
}
