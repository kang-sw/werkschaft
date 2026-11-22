package execjob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsholder"
)

// asyncJob launches an async job for owner "lead" and returns its view.
func (fx *hostedFixture) asyncJob(req HostedLaunch) HostedView {
	fx.t.Helper()
	req.Async = true
	if req.Display == "" {
		req.Display, req.Command = "sleep 99", "sleep 99"
	}
	v, err := fx.launch(context.Background(), nil, req)
	if err != nil || v.Outcome != OutcomeAsync {
		fx.t.Fatalf("launch = %+v, %v", v, err)
	}
	return v
}

func stopNotices(fx *hostedFixture) []execNotice {
	var out []execNotice
	for _, n := range fx.sessions.noticeList() {
		if strings.HasSuffix(n.event, ":stopped-by-user") {
			out = append(out, n)
		}
	}
	return out
}

// TestUserStopMarksAndNotifiesAnAgentsAsyncJob: the user's stop of an
// agent's async job ends it stopped, marks it stopped-by-user and admits one
// steered notice with the stop's event id; a repeat is a no-op.
func TestUserStopMarksAndNotifiesAnAgentsAsyncJob(t *testing.T) {
	var revs atomic.Int32
	fx := &hostedFixture{t: t, holders: newFakeExecHolders(), sessions: newFakeExecSessions(), store: newMemHostedStore(), exitDir: t.TempDir()}
	fx.onRunning = func() { revs.Add(1) }
	fx.open(time.Second)
	v := fx.asyncJob(HostedLaunch{})
	fx.holders.out(v.HolderID, "stderr", "boom\n")
	before := revs.Load()
	sum, err := fx.h.StopByUser("lead", v.ID)
	if err != nil || sum.State != HostedStopped || !sum.StoppedByUser || sum.StopNotice != StopNoticeSent || sum.Origin != OriginAgent {
		t.Fatalf("stop = %+v, %v", sum, err)
	}
	n := stopNotices(fx)
	if len(n) != 1 || n[0].event != "exec:"+v.ID+":stopped-by-user" || n[0].owner != "lead" || n[0].gen != 3 {
		t.Fatalf("notices = %+v", fx.sessions.noticeList())
	}
	for _, want := range []string{"[ws exec] " + v.ID + " was stopped", "command: sleep 99", "stopped by user: cwd " + v.Cwd, "NOTE: deliberate stop", "boom", "exec.tail (exec_id " + v.ID} {
		if !strings.Contains(n[0].text, want) {
			t.Fatalf("notice lacks %q:\n%s", want, n[0].text)
		}
	}
	if all := fx.sessions.noticeList(); len(all) != 1 {
		t.Fatalf("a user stop owes no second (completion) notice: %+v", all)
	}
	// The revision moved once the end and again once the outcome was recorded.
	if got := revs.Load() - before; got < 2 {
		t.Fatalf("OnRunning calls after the stop = %d, want the end and the outcome", got)
	}
	if again, err := fx.h.StopByUser("lead", v.ID); err != nil || !again.StoppedByUser || again.StopNotice != StopNoticeSent {
		t.Fatalf("repeat = %+v, %v", again, err)
	}
	if n := stopNotices(fx); len(n) != 1 {
		t.Fatalf("a repeated stop admitted another notice: %+v", n)
	}
	if got := fx.h.Jobs("lead"); len(got) != 1 || !got[0].StoppedByUser || got[0].Command != "sleep 99" {
		t.Fatalf("jobs = %+v", got)
	}
}

// TestUserStopInsideTheSyncWaitSendsNoNotice: a job still inside its launch
// call is stopped-by-user, and the waiting call reports the stop itself.
func TestUserStopInsideTheSyncWaitSendsNoNotice(t *testing.T) {
	fx := newHostedFixture(t, time.Minute)
	started := make(chan string, 1)
	fx.holders.onSpawn = func(id string) { started <- id }
	type res struct {
		v   HostedView
		err error
	}
	done := make(chan res, 1)
	go func() {
		v, err := fx.launch(context.Background(), nil, HostedLaunch{Display: "sleep 9"})
		done <- res{v, err}
	}()
	<-started
	var id string
	eventuallyExec(t, "the running job", func() bool {
		jobs := fx.h.Jobs("lead")
		if len(jobs) == 1 {
			id = jobs[0].ID
		}
		return id != ""
	})
	sum, err := fx.h.StopByUser("lead", id)
	if err != nil || !sum.StoppedByUser || sum.StopNotice != StopNoticeNone {
		t.Fatalf("stop = %+v, %v", sum, err)
	}
	r := <-done
	if r.v.Outcome != OutcomeDone || r.v.State != HostedStopped {
		t.Fatalf("the waiting call reports the stop: %+v, %v", r.v, r.err)
	}
	if n := fx.sessions.noticeList(); len(n) != 0 {
		t.Fatalf("a sync-wait job's stop sent notices: %+v", n)
	}
	// Not even a Retry admits one for it.
	if _, err := fx.h.StopByUser("lead", id); err != nil || len(fx.sessions.noticeList()) != 0 {
		t.Fatalf("retry = %v, notices %+v", err, fx.sessions.noticeList())
	}
}

// TestOnlyTheUsersStopMarks: the agent's exec.stop and the close cascade end
// a job stopped but never mark it; a job that exited before the user's stop
// is not marked either, and no notice goes.
func TestOnlyTheUsersStopMarks(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	byAgent := fx.asyncJob(HostedLaunch{})
	byClose := fx.asyncJob(HostedLaunch{})
	byCascadeToo := fx.asyncJob(HostedLaunch{})
	if _, err := fx.h.Stop("lead", byAgent.ID); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.StopOwner("lead"); err != nil {
		// byClose and byCascadeToo were still running: the cascade stopped them.
		t.Fatal(err)
	}
	for _, id := range []string{byAgent.ID, byClose.ID, byCascadeToo.ID} {
		if s, _ := fx.h.StopByUser("lead", id); s.StoppedByUser || s.State != HostedStopped {
			t.Fatalf("job %s = %+v, want stopped and unmarked", id, s)
		}
	}
	fx2 := newHostedFixture(t, time.Second)
	done := fx2.asyncJob(HostedLaunch{})
	fx2.holders.exit(done.HolderID, 0)
	eventuallyExec(t, "the exit", func() bool { g, _ := fx2.h.Get("lead", done.ID); return g.Terminal() })
	if s, err := fx2.h.StopByUser("lead", done.ID); err != nil || s.StoppedByUser || s.State != HostedExited {
		t.Fatalf("a job that exited first = %+v, %v", s, err)
	}
	for _, f := range []*hostedFixture{fx, fx2} {
		if n := stopNotices(f); len(n) != 0 {
			t.Fatalf("unmarked stops sent user-stop notices: %+v", n)
		}
	}
}

// TestUserStopLosingTheRaceToAnAgentStopIsNotMarked: whichever stop sets
// stopping first decides "by you".
func TestUserStopLosingTheRaceToAnAgentStopIsNotMarked(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	fx.h.mu.Lock()
	fx.h.jobs[v.ID].stopping = true // the agent's stop is under way
	fx.h.mu.Unlock()
	sum, err := fx.h.StopByUser("lead", v.ID)
	if err != nil || sum.State != HostedStopped || sum.StoppedByUser || len(stopNotices(fx)) != 0 {
		t.Fatalf("stop = %+v, %v, notices %+v", sum, err, fx.sessions.noticeList())
	}
}

// TestUserStopWhoseHolderDidNotGoMarksNothing: a failed holder stop leaves
// the job running and unmarked, so a later natural exit is not mislabeled.
func TestUserStopWhoseHolderDidNotGoMarksNothing(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	fx.holders.mu.Lock()
	fx.holders.stopErr = errors.New("holder unreachable")
	fx.holders.mu.Unlock()
	if _, err := fx.h.StopByUser("lead", v.ID); err == nil {
		t.Fatal("a failed holder stop must be an error")
	}
	fx.h.mu.Lock()
	j := fx.h.jobs[v.ID]
	intent, stopping := j.stopIntent, j.stopping
	fx.h.mu.Unlock()
	if intent || stopping {
		t.Fatalf("intent %v stopping %v survive a stop that did not take", intent, stopping)
	}
	fx.holders.exit(v.HolderID, 7)
	eventuallyExec(t, "the exit", func() bool { g, _ := fx.h.Get("lead", v.ID); return g.Terminal() })
	got := fx.h.Jobs("lead")[0]
	if got.State != HostedExited || got.StoppedByUser || len(stopNotices(fx)) != 0 {
		t.Fatalf("natural exit after a failed stop = %+v", got)
	}
}

// TestUserStopEndedByTheHoldersExitRecordIsMarked: the normal path of a
// running job: the holder's stop makes it send the exit record, which ends
// the job. (A holder that went without one is the next test.)
func TestUserStopEndedByTheHoldersExitRecordIsMarked(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	sum, err := fx.h.StopByUser("lead", v.ID)
	if err != nil || !sum.StoppedByUser || len(stopNotices(fx)) != 1 {
		t.Fatalf("stop = %+v, %v, notices %+v", sum, err, stopNotices(fx))
	}
	if sum.Signal == nil {
		t.Fatalf("the exit record's signal is kept: %+v", sum)
	}
}

// TestUserStopWhoseHolderWentWithoutAnExitRecordIsMarked: the holder is
// gone and its exit record did not arrive first: stop ends the job itself.
func TestUserStopWhoseHolderWentWithoutAnExitRecordIsMarked(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	fx.holders.mu.Lock()
	delete(fx.holders.live, v.HolderID) // Stop answers ErrUnknownHolder
	fx.holders.mu.Unlock()
	sum, err := fx.h.StopByUser("lead", v.ID)
	if err != nil || sum.State != HostedStopped || !sum.StoppedByUser || len(stopNotices(fx)) != 1 {
		t.Fatalf("stop = %+v, %v, notices %+v", sum, err, stopNotices(fx))
	}
}

// TestUserStopNoticeOutcomesAndRetry: closed and failed outcomes are held in
// memory; a repeated stop of a marked job whose notice failed is the Retry,
// which re-admits the same event id once.
func TestUserStopNoticeOutcomesAndRetry(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	closed := fx.asyncJob(HostedLaunch{})
	fx.sessions.mu.Lock()
	fx.sessions.outcome = NoticeDropped
	fx.sessions.mu.Unlock()
	if s, _ := fx.h.StopByUser("lead", closed.ID); s.StopNotice != StopNoticeClosed {
		t.Fatalf("dropped notice = %+v", s)
	}
	if s, _ := fx.h.StopByUser("lead", closed.ID); s.StopNotice != StopNoticeClosed {
		t.Fatalf("a closed notice is not retried: %+v", s)
	}
	fx.sessions.mu.Lock()
	fx.sessions.outcome = NoticeDeferred
	fx.sessions.mu.Unlock()
	deferred := fx.asyncJob(HostedLaunch{})
	if s, _ := fx.h.StopByUser("lead", deferred.ID); s.StopNotice != StopNoticeSent {
		t.Fatalf("a deferred notice reads as sent: %+v", s)
	}
	fx.sessions.mu.Lock()
	fx.sessions.outcome = 0
	fx.sessions.notifyErr = errors.New("inbox write failed")
	fx.sessions.mu.Unlock()
	failed := fx.asyncJob(HostedLaunch{})
	if s, err := fx.h.StopByUser("lead", failed.ID); err != nil || !s.StoppedByUser || s.StopNotice != StopNoticeFailed {
		t.Fatalf("failed notice = %+v, %v", s, err)
	}
	fx.sessions.mu.Lock()
	fx.sessions.notifyErr = nil
	fx.sessions.mu.Unlock()
	if s, err := fx.h.StopByUser("lead", failed.ID); err != nil || s.StopNotice != StopNoticeSent {
		t.Fatalf("retry = %+v, %v", s, err)
	}
	var mine []execNotice
	for _, n := range stopNotices(fx) {
		if n.event == "exec:"+failed.ID+":stopped-by-user" {
			mine = append(mine, n)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("the retry admitted %d notices, want 1", len(mine))
	}
	// (the closed and deferred jobs' notices were not recorded by the fake.)
	if _, err := fx.h.StopByUser("lead", failed.ID); err != nil || len(stopNotices(fx)) != 1 {
		t.Fatalf("a further stop re-sent: %+v", stopNotices(fx))
	}
}

// TestUserStopOfTheUsersOwnJobHandsAReceiptAndWakesNothing: a `!` job's stop
// admits no notice; its receipt goes to the pending input, keyed by exec
// id, once; a job stopped inside its launch call hands nothing here (the
// call's own result carries it).
func TestUserStopOfTheUsersOwnJobHandsAReceiptAndWakesNothing(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{Origin: OriginUser})
	fx.holders.out(v.HolderID, "stdout", "partial\n")
	sum, err := fx.h.StopByUser("lead", v.ID)
	if err != nil || sum.Origin != OriginUser || !sum.StoppedByUser || sum.StopNotice != StopNoticeSent {
		t.Fatalf("stop = %+v, %v", sum, err)
	}
	if n := fx.sessions.noticeList(); len(n) != 0 {
		t.Fatalf("a `!` job's stop woke the agent: %+v", n)
	}
	hands := fx.sessions.handList()
	if len(hands) != 1 || hands[0].event != v.ID || hands[0].owner != "lead" || hands[0].gen != 3 {
		t.Fatalf("hands = %+v", hands)
	}
	for _, want := range []string{"[user ran from the dashboard] exec_id " + v.ID + " was stopped by the user", "NOTE: deliberate stop", "partial"} {
		if !strings.Contains(hands[0].text, want) {
			t.Fatalf("receipt lacks %q:\n%s", want, hands[0].text)
		}
	}
	if _, err := fx.h.StopByUser("lead", v.ID); err != nil || len(fx.sessions.handList()) != 1 {
		t.Fatalf("a repeat handed again: %+v", fx.sessions.handList())
	}
}

// TestUserStopWaitsForTheConvertedIdsPublication: a `!` job stopped while its
// id is being published hands its receipt only after the publication, so the
// stopped receipt is never replaced by the id's.
func TestUserStopWaitsForTheConvertedIdsPublication(t *testing.T) {
	fx := newHostedFixture(t, time.Minute)
	publishing, release := make(chan struct{}), make(chan struct{})
	published := make(chan HostedView, 1)
	go func() {
		v, _ := fx.launch(context.Background(), nil, HostedLaunch{Origin: OriginUser, Window: 20 * time.Millisecond, Display: "sleep 9", OnConvert: func(HostedView) error {
			close(publishing)
			<-release
			return nil
		}})
		published <- v
	}()
	<-publishing
	jobs := fx.h.Jobs("lead")
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v", jobs)
	}
	sum, err := fx.h.StopByUser("lead", jobs[0].ID)
	if err != nil || !sum.StoppedByUser || sum.StopNotice != StopNoticeNone || len(fx.sessions.handList()) != 0 {
		t.Fatalf("stop during publication = %+v, %v, hands %+v", sum, err, fx.sessions.handList())
	}
	close(release)
	<-published
	eventuallyExec(t, "the receipt after the publication", func() bool { return len(fx.sessions.handList()) == 1 })
	if got := fx.h.Jobs("lead")[0]; got.StopNotice != StopNoticeSent {
		t.Fatalf("outcome = %+v", got)
	}
}

// TestConcurrentUserStopsAdmitOneNotice: racing user stops (and the ending
// goroutine's own call) yield one terminal state and one notice.
func TestConcurrentUserStopsAdmitOneNotice(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = fx.h.StopByUser("lead", v.ID)
		}()
	}
	wg.Wait()
	if n := stopNotices(fx); len(n) != 1 {
		t.Fatalf("notices = %d, want 1", len(n))
	}
	if g, _ := fx.h.Get("lead", v.ID); g.State != HostedStopped {
		t.Fatalf("state = %s", g.State)
	}
}

// TestUserStopRefusesAnotherOwnersJob: the job id alone never reaches another
// conversation's job.
func TestUserStopRefusesAnotherOwnersJob(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	if _, err := fx.h.StopByUser("other", v.ID); !errors.Is(err, ErrExecNotFound) {
		t.Fatalf("another owner's stop = %v", err)
	}
	if _, err := fx.h.StopByUser("", v.ID); !errors.Is(err, ErrExecNotFound) {
		t.Fatalf("an empty owner's stop = %v", err)
	}
	if g, _ := fx.h.Get("lead", v.ID); g.Terminal() {
		t.Fatal("the job was stopped")
	}
}

// TestJobsListIsOwnerScopedUncappedAndTailless: the HUD list holds only the
// owner's jobs, more than HostedListMax of them, running first, with no
// output tail (the type carries none; the totals do).
func TestJobsListIsOwnerScopedUncappedAndTailless(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	fx.sessions.mu.Lock()
	fx.sessions.owners["other"] = HostedOwner{ID: "other", Gen: 1, Lead: true}
	fx.sessions.mu.Unlock()
	const total = HostedListMax + 10
	var ids []string
	for i := 0; i < total; i++ {
		v := fx.asyncJob(HostedLaunch{Display: fmt.Sprintf("job %d", i)})
		ids = append(ids, v.ID)
	}
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Owner: "other", Async: true}); err != nil {
		t.Fatal(err)
	}
	fx.holders.out(fx.h.jobs[ids[0]].row.HolderID, "stdout", "twelve bytes")
	// End the oldest job.
	fx.holders.exit(fx.h.jobs[ids[0]].row.HolderID, 0)
	eventuallyExec(t, "the end", func() bool { g, _ := fx.h.Get("lead", ids[0]); return g.Terminal() })
	got := fx.h.Jobs("lead")
	if len(got) != total {
		t.Fatalf("jobs = %d, want %d (the cap of List is not applied)", len(got), total)
	}
	if listed, more := fx.h.List("lead"); len(listed) != HostedListMax || more != 10 {
		t.Fatalf("List keeps its cap: %d, %d", len(listed), more)
	}
	if got[total-1].ID != ids[0] || !got[total-1].Terminal() || got[total-1].Written != 12 || got[0].Terminal() {
		t.Fatalf("order/fields: first %+v last %+v", got[0], got[total-1])
	}
	for _, s := range got {
		if s.Origin != OriginAgent {
			t.Fatalf("origin = %q", s.Origin)
		}
	}
	if fx.h.Jobs("nobody") != nil || (*Hosted)(nil).Jobs("lead") != nil {
		t.Fatal("no owner or no service lists nothing")
	}
}

// TestReadOutputByOffsetPollsTheMergedStream: contiguous reads attributed to
// their stream, the job's state riding along, the 16 KiB bound, a clamped
// offset, and another owner refused.
func TestReadOutputByOffsetPollsTheMergedStream(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	fx.holders.out(v.HolderID, "stdout", "out1\n")
	fx.holders.out(v.HolderID, "stderr", "err1\n")
	first, err := fx.h.ReadOutput("lead", v.ID, 0, 0)
	if err != nil || first.From != 0 || first.Next != 10 || first.Written != 10 || first.Gap || first.Job.Terminal() || len(first.Segments) != 2 || first.Segments[1].Stream != StreamStderr {
		t.Fatalf("first = %+v, %v", first, err)
	}
	fx.holders.out(v.HolderID, "stdout", "out2\n")
	fx.holders.exit(v.HolderID, 3)
	eventuallyExec(t, "the end", func() bool { g, _ := fx.h.Get("lead", v.ID); return g.Terminal() })
	next, err := fx.h.ReadOutput("lead", v.ID, first.Next, 0)
	if err != nil || next.From != 10 || next.Next != 15 || flatten(next.Segments) != "out2\n" || !next.Job.Terminal() || next.Job.ExitCode == nil || *next.Job.ExitCode != 3 {
		t.Fatalf("next = %+v, %v", next, err)
	}
	if past, _ := fx.h.ReadOutput("lead", v.ID, 1<<40, 0); past.From != 15 || past.Next != 15 || len(past.Segments) != 0 || past.Gap {
		t.Fatalf("past = %+v", past)
	}
	if neg, _ := fx.h.ReadOutput("lead", v.ID, -5, 0); neg.From != 0 || neg.Next != 15 {
		t.Fatalf("negative offset = %+v", neg)
	}
	if _, err := fx.h.ReadOutput("other", v.ID, 0, 0); !errors.Is(err, ErrExecNotFound) {
		t.Fatalf("another owner's read = %v", err)
	}
	big := fx.asyncJob(HostedLaunch{})
	fx.holders.out(big.HolderID, "stdout", strings.Repeat("y", 100<<10))
	got, err := fx.h.ReadOutput("lead", big.ID, 0, 1<<30)
	if err != nil || got.Next-got.From != HostedOutputLimit || len(flatten(got.Segments)) != HostedOutputLimit {
		t.Fatalf("bounded read = %d bytes, %v", got.Next-got.From, err)
	}
}

// TestReadOutputReportsAGapAtEvicted: an offset below the ring's first
// retained byte reads from Evicted and says output was dropped.
func TestReadOutputReportsAGapAtEvicted(t *testing.T) {
	fx := &hostedFixture{t: t, holders: newFakeExecHolders(), sessions: newFakeExecSessions(), store: newMemHostedStore(), exitDir: t.TempDir()}
	h, err := NewHosted(HostedOptions{Holders: fx.holders, Store: fx.store, Sessions: fx.sessions, ExitDir: fx.exitDir, Window: time.Second, JobBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	fx.holders.sink = h
	fx.h = h
	h.Start()
	v := fx.asyncJob(HostedLaunch{})
	for i := 0; i < 20; i++ {
		fx.holders.out(v.HolderID, "stdout", fmt.Sprintf("line %02d\n", i))
	}
	got, err := h.ReadOutput("lead", v.ID, 0, 0)
	if err != nil || !got.Gap || got.Evicted == 0 || got.From != got.Evicted || got.Next != got.Written || got.Written != 160 {
		t.Fatalf("read = %+v, %v", got, err)
	}
	if again, _ := h.ReadOutput("lead", v.ID, got.Evicted, 0); again.Gap {
		t.Fatalf("a read at Evicted is no gap: %+v", again)
	}
}

// TestUserStopEndedByTheHoldersExitCopyIsMarkedAndNotified: a holder that goes
// before its exit record reaches the daemon ends the job from its exit
// status (HolderGone); the user's stop intent marks that end too, and the
// notice follows it.
func TestUserStopEndedByTheHoldersExitCopyIsMarkedAndNotified(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	fx.h.mu.Lock()
	j := fx.h.jobs[v.ID]
	j.stopping, j.stopIntent = true, true // the user's stop is waiting on the holder
	fx.h.mu.Unlock()
	sig := 9
	fx.h.HolderGone(wsholder.Descriptor{ID: v.HolderID, Session: fx.holders.launched[v.HolderID].Session}, wsholder.Exit{Reason: "stop", Signal: &sig})
	got := fx.h.Jobs("lead")[0]
	if got.State != HostedStopped || !got.StoppedByUser || got.StopNotice != StopNoticeSent || len(stopNotices(fx)) != 1 {
		t.Fatalf("job = %+v, notices %+v", got, stopNotices(fx))
	}
}

// TestUserStopAddsNoDurableState: the marker, origin and notice outcome live
// in memory only (T0, no schema change). A user stop costs the one row save
// an agent's stop costs and stores the same row; after a daemon restart the
// job reads as an ordinary stopped job of an agent.
func TestUserStopAddsNoDurableState(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	byAgent := fx.asyncJob(HostedLaunch{})
	byUser := fx.asyncJob(HostedLaunch{Origin: OriginUser})
	puts := func() int { fx.store.mu.Lock(); defer fx.store.mu.Unlock(); return fx.store.puts }
	before := puts()
	if _, err := fx.h.Stop("lead", byAgent.ID); err != nil {
		t.Fatal(err)
	}
	agentPuts := puts() - before
	before = puts()
	if _, err := fx.h.StopByUser("lead", byUser.ID); err != nil {
		t.Fatal(err)
	}
	if userPuts := puts() - before; userPuts != agentPuts {
		t.Fatalf("a user stop wrote %d rows, an agent's stop %d", userPuts, agentPuts)
	}
	a, _ := fx.store.row(byAgent.ID)
	u, _ := fx.store.row(byUser.ID)
	for _, row := range []*HostedJobRow{&a, &u} {
		row.ID, row.HolderID, row.EndedAt, row.StartedAt, row.Command, row.Argv, row.Notice = "", "", 0, 0, "", nil, ""
		row.Written, row.Tails, row.Notify = [3]int64{}, [3][]byte{}, false
	}
	if a.State != HostedStopped || !reflectEqualRows(a, u) {
		t.Fatalf("stored rows differ: agent %+v user %+v", a, u)
	}
	fx.open(time.Second) // a daemon restart over the same store
	for _, s := range fx.h.Jobs("lead") {
		if s.State != HostedStopped || s.StoppedByUser || s.Origin != OriginAgent || s.StopNotice != StopNoticeNone {
			t.Fatalf("restored job %+v: the marker, origin and outcome are lost on restart", s)
		}
	}
}

func reflectEqualRows(a, b HostedJobRow) bool { return reflect.DeepEqual(a, b) }

// TestHUDDaemonPathsAreMemoryOnly measures the HUD's paths on the real
// Hosted (the ticket's performance measurement; ai-docs/manuals/performance.md
// Rules 1 and 5): the summary list at 0/1/50/200 jobs, a poll of an open
// peek over a 1 MiB output, and a stop touch the job store and the exit
// directory not at all, except the one row save a stop already costs. The
// latencies it logs are the daemon-side cost; the dashboard's route and
// stream numbers are in wsdashboard's exec_hud_perf_test.go.
func TestHUDDaemonPathsAreMemoryOnly(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	storeOps := func() (puts int) { fx.store.mu.Lock(); defer fx.store.mu.Unlock(); return fx.store.puts }
	exitEntries := func() int { e, _ := os.ReadDir(fx.exitDir); return len(e) }
	median := func(do func()) time.Duration {
		var ds []time.Duration
		for i := 0; i < 15; i++ {
			start := time.Now()
			do()
			ds = append(ds, time.Since(start))
		}
		sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
		return ds[len(ds)/2]
	}
	var first HostedView
	for i, want := range []int{1, 50, 200} {
		for len(fx.h.Jobs("lead")) < want {
			v := fx.asyncJob(HostedLaunch{})
			if i == 0 && first.ID == "" {
				first = v
			}
		}
		puts, entries := storeOps(), exitEntries()
		lat := median(func() { fx.h.Jobs("lead") })
		if storeOps() != puts || exitEntries() != entries {
			t.Fatalf("the list with %d jobs touched the store or the exit directory", want)
		}
		t.Logf("Jobs() with %3d jobs: p50 %v, store rows written 0, exit-dir entries unchanged (%d)", want, lat, entries)
	}
	if got := fx.h.Jobs("nobody"); len(got) != 0 {
		t.Fatal(got)
	}
	// A 1 MiB-output job: poll the whole ring in 16 KiB reads, as an open peek
	// does while it follows the tail.
	fx.holders.out(first.HolderID, "stdout", strings.Repeat("0123456789abcdef", (1<<20)/16))
	puts, entries := storeOps(), exitEntries()
	var polls int
	drain := func() {
		polls = 0
		for off := int64(0); ; polls++ {
			got, err := fx.h.ReadOutput("lead", first.ID, off, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got.Next == off {
				return
			}
			off = got.Next
		}
	}
	whole := median(drain)
	one := median(func() { _, _ = fx.h.ReadOutput("lead", first.ID, 1<<19, 0) })
	if storeOps() != puts || exitEntries() != entries {
		t.Fatal("reading output touched the store or the exit directory")
	}
	t.Logf("peek over 1 MiB: one 16 KiB poll p50 %v; the whole ring in %d polls p50 %v; store and exit directory untouched", one, polls, whole)
	// A stop: the row save the stop already makes, and nothing for the HUD.
	puts = storeOps()
	start := time.Now()
	if _, err := fx.h.StopByUser("lead", first.ID); err != nil {
		t.Fatal(err)
	}
	t.Logf("StopByUser: %v, store rows written %d (an agent's stop writes the same)", time.Since(start), storeOps()-puts)
}
