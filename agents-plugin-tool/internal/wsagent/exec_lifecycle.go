// exec_lifecycle.go joins the daemon's hosted exec jobs to the session
// lifecycle: a conversation's live jobs keep it awake (the one sleep
// predicate, awakeLocked and idleNow), hold a worker's done report until
// they end, and end with an explicit close, deepest descendant first.
package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wsholder"
)

// ExecJobs is the hosted exec jobs as the runner reads them.
type ExecJobs interface {
	// RunningOwners counts the running jobs by owning conversation,
	// including running jobs a predecessor left that are not settled yet.
	// Memory only.
	RunningOwners() map[string]int
	// StopOwner stops every running job of conversation owner (its whole
	// process group) and returns once they ended; its end sends no
	// completion notice. An error means a job may still run.
	StopOwner(owner string) error
	// FenceOwner refuses owner's new jobs from now on (a close in
	// progress); UnfenceOwner lifts that for a revival.
	FenceOwner(owner string)
	UnfenceOwner(owner string)
}

type modelExecRecovery interface {
	RecoverModelJob(owner string, gen uint64, id string) (string, error)
}

func (r *Runner) recoverModelExec(owner string, gen uint64) func(context.Context, string) (string, error) {
	return func(ctx context.Context, jobID string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		src, ok := r.execJobs().(modelExecRecovery)
		if !ok {
			return "", errors.New("hosted exec recovery is unavailable")
		}
		return src.RecoverModelJob(owner, gen, jobID)
	}
}

// agentExecJobs is an optional extension of ExecJobs that counts only
// agent-origin running jobs (excluding user-origin composer `!` commands).
type agentExecJobs interface {
	RunningAgentOwners() map[string]int
}

func agentExecCounts(src ExecJobs, fallback map[string]int) map[string]int {
	if a, ok := src.(agentExecJobs); ok {
		return a.RunningAgentOwners()
	}
	return fallback
}

// execState is the runner's view of the hosted exec jobs: the source once
// the daemon binds it, and the running-job counts by owner its last change
// signal read. Until BindExecJobs runs on a runner that expects jobs
// (ExpectExecJobs), every conversation counts as owning a live job: a held
// report is not released, and no session sleeps, before the jobs a
// predecessor left are known.
type execState struct {
	// mu orders each read of the counts with its store, so a slower
	// reader never stores an older snapshot over a newer one; the source's
	// own lock is taken under it, never the other way round.
	mu        sync.Mutex
	src       atomic.Pointer[ExecJobs]
	live      atomic.Pointer[map[string]int]
	liveAgent atomic.Pointer[map[string]int]
	bound     atomic.Bool
	deferred  atomic.Bool
	// boundC, under boundMu, is closed when the jobs bind: an explicit
	// close that arrives before waits on it (awaitExecBound).
	boundMu sync.Mutex
	boundC  chan struct{}
}

// execBindWait bounds how long an explicit close waits for the hosted exec
// jobs to bind (a test seam).
var execBindWait = 30 * time.Second

// errExecUnbound refuses an explicit close that arrived before the daemon
// bound the exec jobs a predecessor left and did not see them bind in
// time: it cannot stop jobs it does not know yet.
var errExecUnbound = errors.New("the daemon has not restored its exec jobs yet, so the session's jobs cannot be stopped; retry the close shortly")

// errExecUnscanned refuses an explicit close on a daemon whose hosted exec
// jobs did not open, before its first holder scan or while its last scan
// was incomplete (wsholder.Listing.Incomplete): the exec holders a
// predecessor left may not be listed, so the close could not stop them.
var errExecUnscanned = errors.New("the daemon has not found the exec job holders it was left yet, so the session's jobs cannot be stopped; retry the close shortly")

// awaitExecBound returns once the hosted exec jobs are bound, or when the
// runner expects none; errExecUnbound after execBindWait. When the jobs
// bound without a store (BindExecJobs(nil)), it also waits for the first
// holder scan, whose listing the close's holder stops read
// (errExecUnscanned in time).
func (r *Runner) awaitExecBound() error {
	if !r.exec.deferred.Load() {
		return nil
	}
	timer := time.NewTimer(execBindWait)
	defer timer.Stop()
	if !r.exec.bound.Load() {
		r.exec.boundMu.Lock()
		var wait chan struct{}
		if !r.exec.bound.Load() {
			if r.exec.boundC == nil {
				r.exec.boundC = make(chan struct{})
			}
			wait = r.exec.boundC
		}
		r.exec.boundMu.Unlock()
		if wait != nil {
			select {
			case <-wait:
			case <-timer.C:
				return errExecUnbound
			}
		}
	}
	if r.execJobs() != nil || r.opts.Holders == nil {
		return nil
	}
	select {
	case <-r.scanned:
		return nil
	case <-timer.C:
		return errExecUnscanned
	}
}

// ExpectExecJobs marks a runner whose hosted exec jobs the daemon binds
// after Start (BindExecJobs). Call it before Start.
func (r *Runner) ExpectExecJobs() { r.exec.deferred.Store(true) }

// BindExecJobs binds the daemon's hosted exec jobs (nil when it has none)
// and reads their counts. Run it once the jobs settled what a predecessor
// left (execjob.Hosted.Start). The counts are read before they replace the
// unbound hold, so no conversation reads zero in between; then every
// conversation reconsiders what the hold kept, whatever change signals the
// source raised while it settled.
func (r *Runner) BindExecJobs(src ExecJobs) {
	r.exec.mu.Lock()
	next := map[string]int{}
	nextAgent := map[string]int{}
	if src != nil {
		r.exec.src.Store(&src)
		next = src.RunningOwners()
		nextAgent = agentExecCounts(src, next)
	}
	r.exec.live.Store(&next)
	r.exec.liveAgent.Store(&nextAgent)
	r.exec.bound.Store(true)
	r.exec.mu.Unlock()
	r.exec.boundMu.Lock()
	if r.exec.boundC != nil {
		close(r.exec.boundC)
		r.exec.boundC = nil
	}
	r.exec.boundMu.Unlock()
	r.mu.Lock()
	all := make(map[string]bool, len(r.convs))
	for id := range r.convs {
		all[id] = true
	}
	r.mu.Unlock()
	r.execReconsider(all)
	for id := range all {
		if c := r.resident(id); c != nil && r.durableProcess(c.record().Backend) {
			c.signalInbox()
		}
	}
}

func (r *Runner) execJobs() ExecJobs {
	if p := r.exec.src.Load(); p != nil {
		return *p
	}
	return nil
}

// ExecJobsChanged rereads the running-job counts after a job started or
// ended, and has every conversation whose count changed reconsider its
// held report, its sleep, and its ancestors' live bg exec badge counts.
// It never waits on a conversation's loop.
func (r *Runner) ExecJobsChanged() {
	r.exec.mu.Lock()
	next := map[string]int{}
	nextAgent := map[string]int{}
	if src := r.execJobs(); src != nil {
		next = src.RunningOwners()
		nextAgent = agentExecCounts(src, next)
	}
	var prev, prevAgent map[string]int
	if p := r.exec.live.Swap(&next); p != nil {
		prev = *p
	}
	if p := r.exec.liveAgent.Swap(&nextAgent); p != nil {
		prevAgent = *p
	}
	r.exec.mu.Unlock()
	changed := map[string]bool{}
	for id, n := range next {
		if prev[id] != n {
			changed[id] = true
		}
	}
	for id := range prev {
		if _, ok := next[id]; !ok {
			changed[id] = true
		}
	}
	for id, n := range nextAgent {
		if prevAgent[id] != n {
			changed[id] = true
		}
	}
	for id := range prevAgent {
		if _, ok := nextAgent[id]; !ok {
			changed[id] = true
		}
	}
	r.execReconsider(changed)
}

// execReconsider has each conversation of ids reconsider its held report
// and its sleep, and wakes its spawn parents up to the root lead.
func (r *Runner) execReconsider(ids map[string]bool) {
	for id := range ids {
		c := r.resident(id)
		var parent *ParentRef
		if c != nil {
			r.releaseLater(id)
			select {
			case c.spawnC <- struct{}{}:
			default:
			}
			c.mu.Lock()
			parent = c.rec.Parent
			c.mu.Unlock()
		} else if rec, ok := r.recordOf(id); ok {
			parent = rec.Parent
		}
		if parent != nil {
			r.wakeSpawnParents(State{ID: id, Parent: parent})
		}
	}
}

// SubtreeNode describes one conversation in a session's in-memory subtree
// (depth 0 = root itself, 1 = direct child, 2+ = deeper descendant).
type SubtreeNode struct {
	ID    string
	Label string
	Depth int
	// Path holds display labels from the direct child down to this node;
	// nil/empty for depth 0 (self).
	Path []string
}

// Subtree returns rootID (depth 0) and all of its recursive in-memory
// descendants (open or closed), cycle-safe and in deterministic BFS order.
// Memory only: never reads disk. On the lazy childrenIndex == nil fallback it
// reads c.record() under r.mu (safe because no wsagent path acquires r.mu while
// holding c.mu).
func (r *Runner) Subtree(rootID string) []SubtreeNode {
	if r == nil || rootID == "" {
		return nil
	}
	type rawEdge struct {
		id        string
		conv      *conv
		closedRec Record
		hasClosed bool
		parentID  string
		depth     int
	}
	r.mu.Lock()
	if r.childrenIndex == nil {
		// Snapshot live conversations and read their records outside r.mu so
		// lazy index initialization (used by direct-constructed test runners)
		// never nests c.mu under r.mu.
		convs := make([]*conv, 0, len(r.convs))
		for _, c := range r.convs {
			convs = append(convs, c)
		}
		r.mu.Unlock()
		recs := make([]Record, 0, len(convs))
		for _, c := range convs {
			recs = append(recs, c.record())
		}
		r.mu.Lock()
		if r.childrenIndex == nil {
			r.childrenIndex = map[ParentRef]map[string]bool{}
			for _, rec := range recs {
				r.indexChildLocked(rec)
			}
			for _, rec := range r.closed {
				r.indexChildLocked(rec)
			}
		}
	}
	seen := map[string]bool{rootID: true}
	rootNode := rawEdge{id: rootID}
	if c := r.convs[rootID]; c != nil {
		rootNode.conv = c
	} else if rec, ok := r.closed[rootID]; ok {
		rootNode.closedRec = rec
		rootNode.hasClosed = true
	}
	ordered := []rawEdge{rootNode}
	for i := 0; i < len(ordered); i++ {
		cur := ordered[i]
		var childIDs []string
		for _, kind := range []string{ParentLead, ParentConversation} {
			for cid := range r.childrenIndex[ParentRef{Kind: kind, ReplyID: cur.id}] {
				if seen[cid] {
					continue
				}
				seen[cid] = true
				childIDs = append(childIDs, cid)
			}
		}
		sort.Strings(childIDs)
		for _, cid := range childIDs {
			e := rawEdge{id: cid, parentID: cur.id, depth: cur.depth + 1}
			if c := r.convs[cid]; c != nil {
				e.conv = c
			} else if rec, ok := r.closed[cid]; ok {
				e.closedRec = rec
				e.hasClosed = true
			}
			ordered = append(ordered, e)
		}
	}
	r.mu.Unlock()

	pathByID := make(map[string][]string, len(ordered))
	out := make([]SubtreeNode, 0, len(ordered))
	for _, e := range ordered {
		var label string
		if e.conv != nil {
			label = e.conv.record().Label
		} else if e.hasClosed {
			label = e.closedRec.Label
		}
		if label == "" {
			label = e.id
		}
		var path []string
		if e.depth > 0 {
			parentPath := pathByID[e.parentID]
			path = make([]string, len(parentPath)+1)
			copy(path, parentPath)
			path[len(parentPath)] = label
			pathByID[e.id] = path
		}
		out = append(out, SubtreeNode{
			ID:    e.id,
			Label: label,
			Depth: e.depth,
			Path:  slices.Clone(path),
		})
	}
	return out
}

// BgExecCounts returns the running agent-origin hosted ws/exec job counts for
// rootID itself and for all of its recursive descendants. Memory only.
func (r *Runner) BgExecCounts(rootID string) (self, desc int) {
	if r == nil || rootID == "" {
		return 0, 0
	}
	p := r.exec.liveAgent.Load()
	if p == nil || len(*p) == 0 {
		return 0, 0
	}
	counts := *p
	self = counts[rootID]
	if len(counts) == 1 && self > 0 {
		return self, 0
	}
	for _, node := range r.Subtree(rootID) {
		if node.Depth > 0 {
			desc += counts[node.ID]
		}
	}
	return self, desc
}

// execLive counts conversation id's live exec jobs; before the daemon
// binds the jobs of a runner that expects them, it counts one for every
// conversation (execState).
func (r *Runner) execLive(id string) int {
	if r.exec.deferred.Load() && !r.exec.bound.Load() {
		return 1
	}
	if p := r.exec.live.Load(); p != nil {
		return (*p)[id]
	}
	return 0
}

// ExecLiveCounts is a copy of the running-job counts by owner.
func (r *Runner) ExecLiveCounts() map[string]int {
	if p := r.exec.live.Load(); p != nil {
		return maps.Clone(*p)
	}
	return map[string]int{}
}

// ownsLiveExec reports whether c owns a live exec job. Safe without c.mu
// because c.id is immutable after startConvWith.
func (c *conv) ownsLiveExec() bool {
	if c.r == nil {
		return false
	}
	id := c.id
	if id == "" {
		id = c.rec.ReplyID
	}
	return c.r.execLive(id) > 0
}

// fenceExec refuses c's new exec jobs, ahead of an explicit close.
func (r *Runner) fenceExec(ids []string) {
	if src := r.execJobs(); src != nil {
		for _, id := range ids {
			src.FenceOwner(id)
		}
	}
}

// unfenceExec lets conversation id own exec jobs again (a revival).
func (r *Runner) unfenceExec(id string) {
	if src := r.execJobs(); src != nil {
		src.UnfenceOwner(id)
	}
}

// stopExec ends conversation id's live exec jobs, each one's whole process
// group; an error means one may still run.
func (r *Runner) stopExec(id string) error {
	src := r.execJobs()
	if src != nil {
		return src.StopOwner(id)
	}
	if !r.exec.deferred.Load() {
		// A runner with no hosted exec jobs.
		return nil
	}
	return r.stopExecHolders(id)
}

// execHolderSession is the part of a hosted exec job holder's session
// (execjob's hostedHolderSession) that names its owning conversation.
type execHolderSession struct {
	Kind  string `json:"kind"`
	Owner string `json:"owner"`
}

// stopExecHolders stops the exec job holders conversation id owns, for a
// daemon whose hosted exec jobs did not open (BindExecJobs(nil) after
// ExpectExecJobs): a job id started before the restart may still run under
// its holder, and nothing else would end it. A holder that does not stop
// is an error, so the close is refused rather than reported done over it.
func (r *Runner) stopExecHolders(id string) error {
	if r.opts.Holders == nil {
		return nil
	}
	select {
	case <-r.scanned:
	default:
		// The listing does not name a predecessor's holders yet.
		return errExecUnscanned
	}
	listing := r.opts.Holders.List()
	if listing.Incomplete {
		// The last scan could not read every holder's registration: one
		// of this conversation's may run unlisted.
		return errExecUnscanned
	}
	var errs []error
	for _, info := range listing.Holders {
		var s execHolderSession
		if json.Unmarshal(info.Session, &s) != nil || s.Kind != "exec" || s.Owner != id {
			continue
		}
		if err := r.opts.Holders.Stop(info.ID); err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
			errs = append(errs, fmt.Errorf("the hosted exec jobs did not open, and exec holder %s of %s did not stop: %w", info.ID, id, err))
		}
	}
	return errors.Join(errs...)
}
