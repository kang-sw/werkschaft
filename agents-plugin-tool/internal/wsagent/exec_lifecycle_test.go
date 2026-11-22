package wsagent

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
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
	// stopErr, by owner, fails that owner's stops: its jobs keep running.
	stopErr map[string]error
	// onOwners, when set, runs at the start of RunningOwners, outside the
	// lock (a test holds the read there).
	onOwners func()
}

func newFakeExecJobs() *fakeExecJobs {
	return &fakeExecJobs{counts: map[string]int{}, fenced: map[string]bool{}}
}

func (f *fakeExecJobs) RunningOwners() map[string]int {
	f.mu.Lock()
	hold := f.onOwners
	f.mu.Unlock()
	if hold != nil {
		hold()
	}
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

func (f *fakeExecJobs) StopOwner(owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "stop "+owner)
	if err := f.stopErr[owner]; err != nil {
		return err
	}
	f.counts[owner] = 0
	return nil
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

// Binding reads the counts before they replace the unbound hold: while
// the read runs, every conversation still counts a live job.
func TestExecBindKeepsTheHoldWhileItReads(t *testing.T) {
	r := &Runner{convs: map[string]*conv{}}
	r.ExpectExecJobs()
	jobs := newFakeExecJobs()
	jobs.set("x", 1)
	entered, release := make(chan struct{}), make(chan struct{})
	jobs.onOwners = func() {
		close(entered)
		<-release
	}
	bound := make(chan struct{})
	go func() {
		r.BindExecJobs(jobs)
		close(bound)
	}()
	<-entered
	if n := r.execLive("y"); n != 1 {
		t.Fatalf("a conversation read %d live jobs while the bind read the counts", n)
	}
	close(release)
	<-bound
	if r.execLive("y") != 0 || r.execLive("x") != 1 {
		t.Fatalf("after the bind: y=%d x=%d", r.execLive("y"), r.execLive("x"))
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

// A daemon that settles a predecessor's jobs raises change signals before
// it binds them; binding still has every conversation the unbound hold kept
// reconsider it, so a held report with no live job goes out.
func TestExecBindReleasesReportsHeldBeforeBinding(t *testing.T) {
	h, _ := newFakePiRecoveryHarness(t)
	h.runner.ExpectExecJobs()
	worker, holder := spawnRecoveryWorker(t, h)
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
	h.holders.out(holder, recoveryPiReply("done")...)
	h.idle(worker.ID)
	// A change signal while the jobs settle, before binding.
	h.runner.ExecJobsChanged()
	select {
	case res := <-waited:
		t.Fatalf("the report went out before the jobs were bound: %+v", res)
	case <-time.After(300 * time.Millisecond):
	}
	h.runner.BindExecJobs(newFakeExecJobs())
	select {
	case res := <-waited:
		if res.Status != TurnDone {
			t.Fatalf("released report: %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("binding did not release the held report")
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

// A job that does not stop keeps its conversation, and every conversation
// above it, open and unfenced; the close says why.
func TestExecCloseKeepsTheTreeOpenWhenAJobDoesNotStop(t *testing.T) {
	h, _ := newFakePiRecoveryHarness(t)
	jobs := newFakeExecJobs()
	jobs.stopErr = map[string]error{}
	h.runner.BindExecJobs(jobs)
	parent, _ := spawnRecoveryWorker(t, h)
	child, err := h.runner.Spawn(SpawnSpec{Profile: PiWorkerProfile, Root: h.root, Prompt: "child", Tier: "small", Parent: &ParentRef{Kind: ParentConversation, ReplyID: parent.ID}})
	if err != nil {
		t.Fatal(err)
	}
	h.holder(child.ID)
	jobs.set(child.ID, 1)
	jobs.stopErr[child.ID] = errors.New("holder did not go")
	h.runner.ExecJobsChanged()
	if _, err := h.runner.CloseConversation(parent.ID); err == nil || !strings.Contains(err.Error(), "did not stop") || !strings.Contains(err.Error(), "holder did not go") {
		t.Fatalf("close over a job that did not stop: %v", err)
	}
	if h.state(child.ID).Status == StatusClosed || h.state(parent.ID).Status == StatusClosed {
		t.Fatal("a conversation closed over a job that may still run")
	}
	jobs.mu.Lock()
	fenced := maps.Clone(jobs.fenced)
	jobs.mu.Unlock()
	if len(fenced) != 0 {
		t.Fatalf("still fenced: %v", fenced)
	}
}

// An explicit close that arrives before the daemon bound the exec jobs a
// predecessor left waits for the binding, then stops the jobs it learned
// of; one that sees no binding in time is refused and closes nothing.
func TestExecCloseWaitsForTheJobsToBind(t *testing.T) {
	h, _ := newFakePiRecoveryHarness(t)
	h.runner.ExpectExecJobs()
	worker, _ := spawnRecoveryWorker(t, h)
	old := execBindWait
	execBindWait = 50 * time.Millisecond
	t.Cleanup(func() { execBindWait = old })
	if _, err := h.runner.CloseConversation(worker.ID); err == nil || !strings.Contains(err.Error(), "has not restored its exec jobs") {
		t.Fatalf("a close before the jobs bound: %v", err)
	}
	if h.state(worker.ID).Status == StatusClosed {
		t.Fatal("closed over jobs the daemon did not know yet")
	}
	execBindWait = 5 * time.Second
	closed := make(chan error, 1)
	go func() {
		_, err := h.runner.CloseConversation(worker.ID)
		closed <- err
	}()
	select {
	case err := <-closed:
		t.Fatalf("the close went ahead before the jobs bound: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	jobs := newFakeExecJobs()
	jobs.set(worker.ID, 1)
	h.runner.BindExecJobs(jobs)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the close did not go ahead once the jobs bound")
	}
	if got := jobs.log(); !slices.Contains(got, "stop "+worker.ID) {
		t.Fatalf("the restored job was not stopped: %v", got)
	}
}

// A daemon whose hosted exec jobs did not open (BindExecJobs(nil) after
// ExpectExecJobs) still ends the exec job holders a closing conversation
// owns, and refuses the close while one does not stop, instead of
// reporting the conversation closed over a job that may still run.
func TestExecCloseWithoutOpenJobsStopsTheConversationsHolders(t *testing.T) {
	h := newLeadHarness(t)
	h.start()
	h.runner.ExpectExecJobs()
	h.runner.BindExecJobs(nil)
	st := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	mine := h.holders.add(map[string]string{"kind": "exec", "exec_id": "exec_left-running", "owner": st.ID})
	theirs := h.holders.add(map[string]string{"kind": "exec", "exec_id": "exec_another", "owner": "someone-else"})
	h.holders.Scan()

	h.holders.mu.Lock()
	h.holders.stopErr = errors.New("holder busy")
	h.holders.mu.Unlock()
	if _, err := h.runner.CloseConversation(st.ID); err == nil || !strings.Contains(err.Error(), "did not stop") {
		t.Fatalf("a close over an exec holder that did not stop: %v", err)
	}
	if h.state(st.ID).Status == StatusClosed {
		t.Fatal("closed over an exec job that may still run")
	}

	h.holders.mu.Lock()
	h.holders.stopErr = nil
	h.holders.mu.Unlock()
	if _, err := h.runner.CloseConversation(st.ID); err != nil {
		t.Fatal(err)
	}
	h.holders.mu.Lock()
	ops := slices.Clone(h.holders.ops)
	h.holders.mu.Unlock()
	if !slices.Contains(ops, "stop "+mine) || slices.Contains(ops, "stop "+theirs) {
		t.Fatalf("holder ops = %q: want %s stopped and %s kept", ops, mine, theirs)
	}
}

// A daemon whose hosted exec jobs did not open holds a close until its
// first holder scan: before it the listing does not name the exec holders a
// predecessor left, and a close that saw none would report the conversation
// closed while its job runs on. One that sees no scan in time is refused.
func TestExecCloseWithoutOpenJobsWaitsForTheFirstHolderScan(t *testing.T) {
	h := newLeadHarness(t)
	h.start()
	st := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	mine := h.holders.add(map[string]string{"kind": "exec", "exec_id": "exec_left-running", "owner": st.ID})
	h.runner.Close()
	h.holders.restart()
	entered, release := h.gateScan()
	defer release()
	h.runner = NewRunner(h.opts)
	h.runner.ExpectExecJobs()
	h.runner.Start(func(string, string) {})
	t.Cleanup(h.runner.Close)
	h.runner.BindExecJobs(nil)
	wait(t, "the first holder scan to start", entered)

	old := execBindWait
	execBindWait = 50 * time.Millisecond
	t.Cleanup(func() { execBindWait = old })
	if _, err := h.runner.CloseConversation(st.ID); err == nil || !strings.Contains(err.Error(), "has not found the exec job holders") {
		t.Fatalf("a close before the first holder scan: %v", err)
	}
	if h.state(st.ID).Status == StatusClosed {
		t.Fatal("closed before the exec holders it owns were found")
	}

	release()
	wait(t, "the first holder scan", h.runner.rejoined)
	if _, err := h.runner.CloseConversation(st.ID); err != nil {
		t.Fatal(err)
	}
	h.holders.mu.Lock()
	ops := slices.Clone(h.holders.ops)
	h.holders.mu.Unlock()
	if !slices.Contains(ops, "stop "+mine) {
		t.Fatalf("holder ops = %q: want %s stopped", ops, mine)
	}
}
