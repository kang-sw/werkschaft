// agent_delivery.go holds child delivery, expiry, retention, and token routing.
package wsagent

import (
	"errors"
	"fmt"
	"iter"
	"net/http"
	"os"
	"runtime"
	"slices"
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
	c.mu.Lock()
	c.expiring = true
	c.mu.Unlock()
	c.r.expireLater(rec.ReplyID)
}

// expireLater deletes waiting child id and its descendants, deepest first,
// off the caller's goroutine, unless a descendant is awake or id woke
// meanwhile.
func (r *Runner) expireLater(id string) {
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		r.expireChecked(id, true)
	}()
}

// expire is expireLater's body; it reports whether id was deleted.
func (r *Runner) expire(id string) bool {
	return r.expireChecked(id, false)
}

func (r *Runner) expireChecked(id string, checkAge bool) bool {
	c := r.conv(id) // rehydrates a parked child: its own loop closes it
	if c == nil {
		return false
	}
	c.mu.Lock()
	c.expiring = true // not parked again meanwhile
	c.mu.Unlock()
	if hook := r.opts.beforeExpiryClose; hook != nil {
		hook()
	}
	_, order, err := r.closeConversationExpiry(id, checkAge)
	if err != nil {
		if errors.Is(err, errClosed) {
			// Someone closed it meanwhile (a dashboard close): what they
			// closed is theirs to delete, not the expiry's.
			return false
		}
		fmt.Fprintf(r.opts.Log, "agent %s: expire: %v\n", id, err)
	}
	if len(order) == 0 {
		// A later heartbeat tries again.
		c.do(func() {
			c.mu.Lock()
			c.expiring = false
			c.mu.Unlock()
		})
		return false
	}
	for _, d := range order {
		r.forget(d)
	}
	return true
}

var errExpiryAborted = errors.New("expiry aborted: conversation active")

type expiryIncarnation struct {
	conv   *conv
	parked bool
	gen    uint64
}

type expiryPlan struct {
	root           string
	order          []string
	incarnations   map[string]expiryIncarnation
	treeNodes      map[string]bool
	checkAge       bool
	rootLastActive time.Time
}

func (c *conv) hasHeldInbox() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.held > 0 || len(c.heldKinds) > 0
}

func (r *Runner) planExpiry(id string, checkAge bool) (expiryPlan, bool) {
	root := safeStopParent(id, false)
	type snapNode struct {
		conv   *conv
		closed Record
		hasRec bool
		parked bool
		gen    uint64
	}
	// Snapshot the reachable subtree structure and incarnation generations under
	// r.mu without acquiring any conversation lock, then evaluate waiting/awake
	// state outside r.mu.
	r.mu.Lock()
	rootConv := r.convs[id]
	if rootConv == nil || r.unparking[id] != nil || r.pendingSpawns[root] > 0 {
		r.mu.Unlock()
		return expiryPlan{}, false
	}
	rootGen := r.convGen[id]
	edges := map[ParentRef][]string{}
	nodes := map[string]snapNode{}
	seen := map[string]bool{id: true}
	queue := []ParentRef{root}
	for i := 0; i < len(queue); i++ {
		parent := queue[i]
		if r.pendingSpawns[parent] > 0 {
			r.mu.Unlock()
			return expiryPlan{}, false
		}
		children := r.childrenIndex[parent]
		if len(children) == 0 {
			continue
		}
		list := make([]string, 0, len(children))
		for cid := range children {
			list = append(list, cid)
			if seen[cid] {
				continue
			}
			if r.unparking[cid] != nil {
				r.mu.Unlock()
				return expiryPlan{}, false
			}
			seen[cid] = true
			if child := r.convs[cid]; child != nil {
				nodes[cid] = snapNode{conv: child, gen: r.convGen[cid]}
				// Enqueue both parent kinds so snapshot traversal never reads
				// child.rec.Lead under r.mu.
				queue = append(queue, ParentRef{Kind: ParentConversation, ReplyID: cid}, ParentRef{Kind: ParentLead, ReplyID: cid})
			} else if rec, ok := r.closed[cid]; ok {
				nodes[cid] = snapNode{closed: rec, hasRec: true, parked: r.parked[cid], gen: r.convGen[cid]}
				queue = append(queue, safeStopParent(cid, rec.Lead))
			}
		}
		edges[parent] = list
	}
	r.mu.Unlock()

	now := r.opts.Now()
	expiry := r.childWaitingExpiry()
	rootConv.mu.Lock()
	at := lastActive(rootConv.rec)
	waiting := rootConv.childWaitingLocked() &&
		(rootConv.wakeRefused || rootConv.held == 0) &&
		len(rootConv.inboxC) == 0 && len(rootConv.mailC) == 0 &&
		(!checkAge || (!at.IsZero() && now.Sub(at) >= expiry))
	rootConv.mu.Unlock()
	if !waiting || r.execLive(id) > 0 {
		return expiryPlan{}, false
	}
	descendants, ok := expiryDescendants(root, func(parent ParentRef) iter.Seq[string] {
		return slices.Values(edges[parent])
	}, func(cid string) expiryNode {
		node, ok := nodes[cid]
		if !ok {
			return expiryNode{}
		}
		if node.conv != nil {
			node.conv.mu.Lock()
			defer node.conv.mu.Unlock()
			quiet := !node.conv.parkedOut && !node.conv.awakeLocked() &&
				(node.conv.wakeRefused || node.conv.held == 0) &&
				len(node.conv.inboxC) == 0 && len(node.conv.mailC) == 0
			return expiryNode{
				parent: safeStopParent(cid, node.conv.rec.Lead),
				closed: node.conv.closed,
				quiet:  quiet,
			}
		}
		if node.hasRec {
			if node.parked {
				// Revalidated under admitsExpiryTree and on rehydration before any close.
				return expiryNode{parent: safeStopParent(cid, node.closed.Lead), quiet: r.execLive(cid) == 0}
			}
			return expiryNode{parent: safeStopParent(cid, node.closed.Lead), closed: true}
		}
		return expiryNode{}
	})
	if !ok {
		return expiryPlan{}, false
	}
	order := append(descendants, id)
	inc := make(map[string]expiryIncarnation, len(order))
	inc[id] = expiryIncarnation{conv: rootConv, gen: rootGen}
	for _, cid := range descendants {
		n := nodes[cid]
		inc[cid] = expiryIncarnation{conv: n.conv, parked: n.parked, gen: n.gen}
	}
	return expiryPlan{
		root:           id,
		order:          order,
		incarnations:   inc,
		treeNodes:      seen,
		checkAge:       checkAge,
		rootLastActive: at,
	}, true
}

// admitsExpiryTree revalidates the entire remaining expiry tree together under
// r.mu without blocking on any conversation lock (TryLock only, mirroring
// admitsSafeStop, retrying briefly on transient contention). Any new child,
// pending spawn, unpark, generation change, fresh root activity, pending
// inbox/mail signal, or live exec aborts expiry. missingRoot is true when the
// root vanished from both convs/parked and closed unexpectedly.
func (r *Runner) admitsExpiryTree(plan expiryPlan, remaining []string) (ok bool, missingRoot bool) {
	const maxTryLockRetries = 16
	for attempt := 0; attempt < maxTryLockRetries; attempt++ {
		res, contended, missing := r.admitsExpiryTreeOnce(plan, remaining)
		if !contended {
			return res, missing
		}
		if attempt < 8 {
			runtime.Gosched()
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	return false, false
}

func (r *Runner) admitsExpiryTreeOnce(plan expiryPlan, remaining []string) (ok bool, contended bool, missingRoot bool) {
	now := r.opts.Now()
	expiry := r.childWaitingExpiry()
	r.mu.Lock()
	var locked []*conv
	defer func() {
		for i := len(locked) - 1; i >= 0; i-- {
			locked[i].mu.Unlock()
		}
		r.mu.Unlock()
	}()

	if r.convs[plan.root] == nil && !r.parked[plan.root] {
		if _, closed := r.closed[plan.root]; !closed {
			return false, false, true
		}
		return false, false, false
	}

	root := safeStopParent(plan.root, false)
	queue := []ParentRef{root}
	visited := map[string]bool{plan.root: true}
	for i := 0; i < len(queue); i++ {
		parent := queue[i]
		if r.pendingSpawns[parent] > 0 {
			return false, false, false
		}
		for cid := range r.childrenIndex[parent] {
			if !plan.treeNodes[cid] || r.unparking[cid] != nil {
				return false, false, false
			}
			if visited[cid] {
				continue
			}
			visited[cid] = true
			queue = append(queue, ParentRef{Kind: ParentConversation, ReplyID: cid}, ParentRef{Kind: ParentLead, ReplyID: cid})
		}
	}

	for _, nid := range remaining {
		snap, ok := plan.incarnations[nid]
		if !ok || r.unparking[nid] != nil || r.convGen[nid] != snap.gen {
			return false, false, false
		}
		if snap.parked {
			if !r.parked[nid] || r.convs[nid] != nil {
				return false, false, false
			}
			continue
		}
		c := r.convs[nid]
		if c == nil || c != snap.conv {
			return false, false, false
		}
		if !c.mu.TryLock() {
			return false, true, false
		}
		locked = append(locked, c)
		if c.closed || c.parkedOut || len(c.inboxC) != 0 || len(c.mailC) != 0 {
			return false, false, false
		}
		if !c.wakeRefused && c.held > 0 {
			return false, false, false
		}
		if nid == plan.root {
			at := lastActive(c.rec)
			if !c.childWaitingLocked() || !at.Equal(plan.rootLastActive) || (plan.checkAge && (at.IsZero() || now.Sub(at) < expiry)) {
				return false, false, false
			}
		} else if c.awakeNoExecLocked() {
			return false, false, false
		}
	}

	if exec := r.exec.live.Load(); exec != nil {
		for _, nid := range remaining {
			if (*exec)[nid] > 0 {
				return false, false, false
			}
		}
	}
	return true, false, false
}

type expiryNode struct {
	parent ParentRef
	closed bool
	quiet  bool
}

// expiryDescendants collects the open descendants of a quiet expiry
// candidate, deepest first. A closed record can carry ancestry but breaks
// the cascade: every open record beyond it must meet its own expiry.
func expiryDescendants(root ParentRef, children func(ParentRef) iter.Seq[string], lookup func(string) expiryNode) ([]string, bool) {
	seen := map[string]bool{root.ReplyID: true}
	var out []string
	var walk func(ParentRef, bool) bool
	walk = func(parent ParentRef, throughClosed bool) bool {
		for id := range children(parent) {
			if seen[id] {
				return false
			}
			seen[id] = true
			child := lookup(id)
			if child.parent.ReplyID == "" {
				continue
			}
			if !child.closed && (throughClosed || !child.quiet) {
				return false
			}
			if !walk(child.parent, throughClosed || child.closed) {
				return false
			}
			if !child.closed {
				out = append(out, id)
			}
		}
		return true
	}
	ok := walk(root, false)
	return out, ok
}

var (
	_ wsdashboard.AgentForgetter     = (*Runner)(nil)
	_ wsdashboard.AgentForgetChecker = (*Runner)(nil)
)

// CanForgetClosed reports whether ForgetClosed would allow removing id's
// session directory, without evicting runner, durable, or SQLite state.
func (r *Runner) CanForgetClosed(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _, ok := r.canDropClosedLocked(id)
	return ok
}

// ForgetClosed drops a closed or handed-off conversation and any private
// record. It returns whether the caller may remove the session directory.
func (r *Runner) ForgetClosed(id string) bool {
	rec, purge := r.forgetClosed(id)
	if purge && !r.CronKnown(id) {
		if rec.ReplyID != "" {
			r.deleteDurableRecord(rec)
		}
		r.deleteMetaSession(id)
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

func (r *Runner) canDropClosedLocked(id string) (Record, bool, bool) {
	rec, ok := r.closed[id]
	if ok && r.hasLiveDescendantsLocked(safeStopParent(id, rec.Lead), nil) {
		return Record{}, ok, false
	}
	// An ended dashboard row can become visible before its conversation
	// moves from convs to closed. Check both parent kinds without taking a
	// conversation lock under the registry lock.
	if r.convs[id] != nil &&
		(r.hasLiveDescendantsLocked(safeStopParent(id, false), nil) ||
			r.hasLiveDescendantsLocked(safeStopParent(id, true), nil)) {
		return Record{}, ok, false
	}
	return rec, ok, true
}

func (r *Runner) dropClosed(id string) (Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok, canDrop := r.canDropClosedLocked(id)
	if !canDrop {
		return Record{}, false
	}
	delete(r.closed, id)
	delete(r.closedRows, id)
	delete(r.deadRefusals, id)
	r.dropParkedLocked(id)
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
			if _, pending := startupOpen[id]; pending || r.convs[id] != nil || r.parked[id] {
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
	r.deleteMetaSession(id)
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
	children := func(parent ParentRef) iter.Seq[string] { return slices.Values(kids[parent]) }
	quiet := func(rec Record) bool {
		mark := rec.Voided
		if rec.LastTurn != nil {
			mark = max(mark, rec.LastTurn.Mark)
		}
		return dead[rec.ReplyID] == nil && !rec.Lead && rec.Parent != nil && rec.HolderID == "" && !rec.TurnOpen && rec.HeldTurn == nil &&
			rec.Sends <= mark && len(rec.Queued)+len(rec.Mail)+len(rec.Reports)+len(rec.Steered)+len(rec.PendingReports) == 0
	}
	lookup := func(id string) expiryNode {
		rec := byID[id]
		return expiryNode{parent: safeStopParent(id, rec.Lead), closed: closedIDs[id], quiet: quiet(rec)}
	}
	seen := map[string]bool{}
	var out []string
	var visit func(id string)
	visit = func(id string) {
		seen[id] = true
		rec := byID[id]
		parent := safeStopParent(id, rec.Lead)
		if at := lastActive(rec); !closedIDs[id] && quiet(rec) && !at.IsZero() && now.Sub(at) >= expiry {
			if desc, ok := expiryDescendants(parent, children, lookup); ok {
				out = append(out, desc...)
				out = append(out, id)
				return
			}
		}
		for k := range children(parent) {
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
