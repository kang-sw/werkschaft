// exec_lifecycle.go joins the daemon's hosted exec jobs to the session
// lifecycle: a conversation's live jobs keep it awake (the one sleep
// predicate, awakeLocked and idleNow), hold a worker's done report until
// they end, and end with an explicit close, deepest descendant first.
package wsagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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

// awaitExecBound returns once the hosted exec jobs are bound, or when the
// runner expects none; errExecUnbound after execBindWait.
func (r *Runner) awaitExecBound() error {
	if !r.exec.deferred.Load() || r.exec.bound.Load() {
		return nil
	}
	r.exec.boundMu.Lock()
	if r.exec.bound.Load() {
		r.exec.boundMu.Unlock()
		return nil
	}
	if r.exec.boundC == nil {
		r.exec.boundC = make(chan struct{})
	}
	wait := r.exec.boundC
	r.exec.boundMu.Unlock()
	timer := time.NewTimer(execBindWait)
	defer timer.Stop()
	select {
	case <-wait:
		return nil
	case <-timer.C:
		return errExecUnbound
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
	if src != nil {
		r.exec.src.Store(&src)
		next = src.RunningOwners()
	}
	r.exec.live.Store(&next)
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
	r.execReconsider(changed)
}

// execReconsider has each conversation of ids reconsider its held report
// and its sleep, and wakes its spawn parents.
func (r *Runner) execReconsider(ids map[string]bool) {
	for id := range ids {
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
	var errs []error
	for _, info := range r.opts.Holders.List().Holders {
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
