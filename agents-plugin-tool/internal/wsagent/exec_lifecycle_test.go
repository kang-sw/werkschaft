package wsagent

import (
	"context"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeExecJobs is the hosted exec jobs as the runner reads them: running
// counts by owner, and the fences and stops it was asked for, in order.
type fakeExecJobs struct {
	mu     sync.Mutex
	counts map[string]int
	calls  []string
	fenced map[string]bool
}

func newFakeExecJobs() *fakeExecJobs {
	return &fakeExecJobs{counts: map[string]int{}, fenced: map[string]bool{}}
}

func (f *fakeExecJobs) RunningOwners() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := maps.Clone(f.counts)
	for k, v := range out {
		if v == 0 {
			delete(out, k)
		}
	}
	return out
}

func (f *fakeExecJobs) StopOwner(owner string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "stop "+owner)
	f.counts[owner] = 0
}

func (f *fakeExecJobs) FenceOwner(owner string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "fence "+owner)
	f.fenced[owner] = true
}

func (f *fakeExecJobs) UnfenceOwner(owner string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "unfence "+owner)
	delete(f.fenced, owner)
}

func (f *fakeExecJobs) set(owner string, n int) {
	f.mu.Lock()
	f.counts[owner] = n
	f.mu.Unlock()
}

func (f *fakeExecJobs) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// The one sleep predicate: a running turn, a live job (a harness
// background task or an owned exec job), a held turn, or an awake child
// keeps a session awake; a sleeping child, or a finished child whose
// process merely waits for its own idle stop, does not. idleNow and
// awakeLocked agree on it.
func TestExecSleepPredicateCountsLiveExecJobs(t *testing.T) {
	jobs := newFakeExecJobs()
	r := &Runner{convs: map[string]*conv{}}
	r.BindExecJobs(jobs)
	parent := &conv{r: r, rec: Record{ReplyID: "parent", Lead: true}}
	child := &conv{r: r, rec: Record{ReplyID: "child", Parent: &ParentRef{Kind: ParentLead, ReplyID: "parent"}}}
	r.convs["parent"], r.convs["child"] = parent, child
	if !parent.idleNow() {
		t.Fatal("nothing keeps the parent awake")
	}

	jobs.set("parent", 1)
	r.ExecJobsChanged()
	if parent.idleNow() || !parent.ownsLiveExec() {
		t.Fatal("the parent's own live exec job must keep it awake")
	}
	jobs.set("parent", 0)
	jobs.set("child", 2)
	r.ExecJobsChanged()
	child.mu.Lock()
	awake, keeps := child.awakeLocked(), child.keepsAwakeLocked()
	child.mu.Unlock()
	if !awake || !keeps || parent.idleNow() {
		t.Fatalf("a child's live exec job: awake=%v keeps=%v parent idle=%v", awake, keeps, parent.idleNow())
	}
	jobs.set("child", 0)
	r.ExecJobsChanged()
	// A finished child whose process has not idled out yet: awake for the
	// width cap, asleep for its parent.
	child.proc = &fakeProc{}
	child.mu.Lock()
	awake, keeps = child.awakeLocked(), child.keepsAwakeLocked()
	child.mu.Unlock()
	if !awake || keeps || !parent.idleNow() {
		t.Fatalf("finished child with a process: awake=%v keeps=%v parent idle=%v", awake, keeps, parent.idleNow())
	}
	child.proc = nil
	child.bgTasks = 1
	if parent.idleNow() {
		t.Fatal("a child's background task must keep the parent awake")
	}
}

// Until the daemon binds its hosted exec jobs, a runner that expects them
// counts every conversation as owning a live job: nothing a predecessor
// left running is missed by a held report or the idle timer.
func TestExecJobsDeferredUntilBound(t *testing.T) {
	r := &Runner{convs: map[string]*conv{}}
	if r.execLive("x") != 0 {
		t.Fatal("a runner that expects no jobs counts one")
	}
	r.ExpectExecJobs()
	if r.execLive("x") != 1 {
		t.Fatal("an unbound runner that expects jobs must count one")
	}
	r.ExecJobsChanged()
	if r.execLive("x") != 1 {
		t.Fatal("a change signal before binding must not release the hold")
	}
	r.BindExecJobs(nil)
	if r.execLive("x") != 0 {
		t.Fatal("a daemon without hosted jobs counts none once bound")
	}
}

// A worker whose session owns a live exec job holds its done report until
// the job ends; then the report goes to its parent.
func TestExecWorkerReportHeldWhileItsJobRuns(t *testing.T) {
	h, _ := newFakePiRecoveryHarness(t)
	jobs := newFakeExecJobs()
	h.runner.BindExecJobs(jobs)
	worker, holder := spawnRecoveryWorker(t, h)
	jobs.set(worker.ID, 1)
	h.runner.ExecJobsChanged()
	waited := make(chan TurnResult, 1)
	go func() {
		res, _ := h.runner.wait(context.Background(), worker.ID, 10*time.Second, true)
		waited <- res
	}()
	eventually(t, "the parent's wait", func() bool {
		c := h.runner.conv(worker.ID)
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.parentWaits) > 0
	})
	h.holders.out(holder, recoveryPiReply("started the build; its completion comes as a message")...)
	h.idle(worker.ID)
	select {
	case res := <-waited:
		t.Fatalf("the report went out while the job ran: %+v", res)
	case <-time.After(300 * time.Millisecond):
	}
	if st := h.state(worker.ID); st.Status != StatusIdle {
		t.Fatalf("worker state %+v", st)
	}
	c := h.runner.conv(worker.ID)
	var held bool
	c.do(func() { held = c.heldRes != nil })
	if !held {
		t.Fatal("the done turn is not held")
	}
	jobs.set(worker.ID, 0)
	h.runner.ExecJobsChanged()
	select {
	case res := <-waited:
		if res.Status != TurnDone {
			t.Fatalf("released report: %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the report was not released once the job ended")
	}
}

// An explicit close refuses new exec jobs of the whole tree first, then
// ends each conversation's jobs before it closes, deepest first.
func TestExecCloseFencesThenStopsDeepestFirst(t *testing.T) {
	h, _ := newFakePiRecoveryHarness(t)
	jobs := newFakeExecJobs()
	h.runner.BindExecJobs(jobs)
	parent, _ := spawnRecoveryWorker(t, h)
	child, err := h.runner.Spawn(SpawnSpec{Profile: PiWorkerProfile, Root: h.root, Prompt: "child", Tier: "small", Parent: &ParentRef{Kind: ParentConversation, ReplyID: parent.ID}})
	if err != nil {
		t.Fatal(err)
	}
	h.holder(child.ID)
	jobs.set(parent.ID, 1)
	jobs.set(child.ID, 1)
	h.runner.ExecJobsChanged()
	if _, err := h.runner.CloseConversation(parent.ID); err != nil {
		t.Fatal(err)
	}
	want := []string{"fence " + child.ID, "fence " + parent.ID, "stop " + child.ID, "stop " + parent.ID}
	if got := jobs.log(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if h.state(child.ID).Status != StatusClosed || h.state(parent.ID).Status != StatusClosed {
		t.Fatal("both closed")
	}
}
