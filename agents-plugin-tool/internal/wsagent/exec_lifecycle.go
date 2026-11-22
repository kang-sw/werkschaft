// exec_lifecycle.go joins the daemon's hosted exec jobs to the session
// lifecycle: a conversation's live jobs keep it awake (the one sleep
// predicate, awakeLocked and idleNow), hold a worker's done report until
// they end, and end with an explicit close, deepest descendant first.
package wsagent

import (
	"maps"
	"sync"
	"sync/atomic"
)

// ExecJobs is the hosted exec jobs as the runner reads them.
type ExecJobs interface {
	// RunningOwners counts the running jobs by owning conversation,
	// including running jobs a predecessor left that are not settled yet.
	// Memory only.
	RunningOwners() map[string]int
	// StopOwner stops every running job of conversation owner (its whole
	// process group) and returns once they ended; its end sends no
	// completion notice.
	StopOwner(owner string)
	// FenceOwner refuses owner's new jobs from now on (a close in
	// progress); UnfenceOwner lifts that for a revival.
	FenceOwner(owner string)
	UnfenceOwner(owner string)
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
	mu       sync.Mutex
	src      atomic.Pointer[ExecJobs]
	live     atomic.Pointer[map[string]int]
	bound    atomic.Bool
	deferred atomic.Bool
}

// ExpectExecJobs marks a runner whose hosted exec jobs the daemon binds
// after Start (BindExecJobs). Call it before Start.
func (r *Runner) ExpectExecJobs() { r.exec.deferred.Store(true) }

// BindExecJobs binds the daemon's hosted exec jobs (nil when it has none)
// and reads their counts. Run it once the jobs settled what a predecessor
// left (execjob.Hosted.Start).
//
// Change signals the source raised while it settled (before binding) do not
// count: binding drops the counts they read, so the read here is the first
// one and has every conversation reconsider what the unbound hold kept.
func (r *Runner) BindExecJobs(src ExecJobs) {
	r.exec.mu.Lock()
	if src != nil {
		r.exec.src.Store(&src)
	}
	r.exec.live.Store(nil)
	r.exec.bound.Store(true)
	r.exec.mu.Unlock()
	r.ExecJobsChanged()
}

func (r *Runner) execJobs() ExecJobs {
	if p := r.exec.src.Load(); p != nil {
		return *p
	}
	return nil
}

// ExecJobsChanged rereads the running-job counts after a job started or
// ended, and has every conversation whose count changed reconsider its
// held report and its sleep. It never waits on a conversation's loop.
func (r *Runner) ExecJobsChanged() {
	r.exec.mu.Lock()
	next := map[string]int{}
	if src := r.execJobs(); src != nil {
		next = src.RunningOwners()
	}
	var prev map[string]int
	if p := r.exec.live.Swap(&next); p != nil {
		prev = *p
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
	if prev == nil {
		// The first read after binding (or ever): every held report and
		// idle timer waited on it.
		r.mu.Lock()
		for id := range r.convs {
			changed[id] = true
		}
		r.mu.Unlock()
	}
	for id := range changed {
		c := r.conv(id)
		if c == nil {
			continue
		}
		r.releaseLater(id)
		select {
		case c.spawnC <- struct{}{}:
		default:
		}
		c.mu.Lock()
		st := c.rec.Parent
		c.mu.Unlock()
		if st != nil {
			r.wakeSpawnParents(State{ID: id, Parent: st})
		}
	}
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

// ownsLiveExec reports whether c owns a live exec job.
func (c *conv) ownsLiveExec() bool { return c.r != nil && c.r.execLive(c.rec.ReplyID) > 0 }

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

// stopExec ends the live exec jobs of ids, in order (deepest first), each
// owner's whole process groups before the next owner's.
func (r *Runner) stopExec(ids []string) {
	src := r.execJobs()
	if src == nil {
		return
	}
	for _, id := range ids {
		src.StopOwner(id)
	}
}
