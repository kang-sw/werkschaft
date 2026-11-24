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

// TestUserStopRacingNaturalExitEndsOnceAndMarksConsistently: the user's stop
// and the child's own exit land together, over many jobs. Each ends in
// exactly one terminal state; stopped-by-user holds exactly when that state
// is stopped, and a marked job has one notice, an exited one none.
func TestUserStopRacingNaturalExitEndsOnceAndMarksConsistently(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	const rounds = 60
	stopped, exited := 0, 0
	for i := 0; i < rounds; i++ {
		v := fx.asyncJob(HostedLaunch{})
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = fx.h.StopByUser("lead", v.ID)
		}()
		go func() {
			defer wg.Done()
			<-start
			fx.holders.exit(v.HolderID, 3)
		}()
		close(start)
		wg.Wait()
		eventuallyExec(t, "the end", func() bool { g, _ := fx.h.Get("lead", v.ID); return g.Terminal() })
		sum, err := fx.h.StopByUser("lead", v.ID) // reads the settled state; a no-op
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if sum.StoppedByUser != (sum.State == HostedStopped) {
			t.Fatalf("round %d: marked %v with state %s", i, sum.StoppedByUser, sum.State)
		}
		var n int
		for _, e := range stopNotices(fx) {
			if strings.Contains(e.event, v.ID) {
				n++
			}
		}
		switch sum.State {
		case HostedStopped:
			stopped++
			if n != 1 || sum.StopNotice != StopNoticeSent {
				t.Fatalf("round %d: stopped job has %d notices, outcome %q", i, n, sum.StopNotice)
			}
		case HostedExited:
			exited++
			if n != 0 {
				t.Fatalf("round %d: an exited job has %d stop notices", i, n)
			}
		default:
			t.Fatalf("round %d: state %s", i, sum.State)
		}
	}
	t.Logf("stopped %d, exited %d of %d", stopped, exited, rounds)
}

// TestUserStopSettingStoppingFirstMarksTheJobAnAgentStopJoins: the user's stop
// sets stopping and is still in the holder; the agent's own stop joins that
// attempt, asks the holder no second time, and returns with it. The user's
// stop decided it: marked, one notice, and no completion notice.
func TestUserStopSettingStoppingFirstMarksTheJobAnAgentStopJoins(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	inUser := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	fx.holders.mu.Lock()
	fx.holders.beforeStop = func(string) {
		if calls.Add(1) == 1 { // later calls are the holder's own retiring
			close(inUser)
			<-release
		}
	}
	fx.holders.mu.Unlock()
	userDone := make(chan error, 1)
	go func() { _, err := fx.h.StopByUser("lead", v.ID); userDone <- err }()
	<-inUser
	agentDone := make(chan error, 1)
	go func() { _, err := fx.h.Stop("lead", v.ID); agentDone <- err }()
	select {
	case err := <-agentDone:
		t.Fatalf("the agent's stop returned before the attempt it joined: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the holder was asked %d times while the first attempt ran, want 1", got)
	}
	close(release)
	if err := <-userDone; err != nil {
		t.Fatal(err)
	}
	if err := <-agentDone; err != nil {
		t.Fatal(err)
	}
	sum := fx.h.Jobs("lead")[0]
	if sum.State != HostedStopped || !sum.StoppedByUser || sum.StopNotice != StopNoticeSent || len(stopNotices(fx)) != 1 || len(fx.sessions.noticeList()) != 1 {
		t.Fatalf("job = %+v, notices %+v", sum, fx.sessions.noticeList())
	}
}

// TestFailedStopAttemptFailsItsJoinersAndTheNextUserStopStillMarks: a stop's
// holder stop fails while another stop has joined it: both see the failure,
// stopping and the intent are cleared once, and a user stop that follows
// ends the job marked, with its notice and no completion notice.
func TestFailedStopAttemptFailsItsJoinersAndTheNextUserStopStillMarks(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	inFirst := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	fx.holders.mu.Lock()
	fx.holders.stopErr = errors.New("holder unreachable")
	fx.holders.beforeStop = func(string) {
		if calls.Add(1) == 1 {
			close(inFirst)
			<-release
		}
	}
	fx.holders.mu.Unlock()
	first := make(chan error, 1)
	go func() { _, err := fx.h.StopByUser("lead", v.ID); first <- err }()
	<-inFirst
	joiner := make(chan error, 1)
	go func() { _, err := fx.h.StopByUser("lead", v.ID); joiner <- err }()
	time.Sleep(50 * time.Millisecond) // the joiner waits on the attempt
	close(release)
	if err := <-first; err == nil {
		t.Fatal("the first stop must fail")
	}
	if err := <-joiner; err == nil {
		t.Fatal("a stop that joined the failed attempt must see its failure")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the holder was asked %d times by the failed attempt and its joiner, want 1", got)
	}
	fx.h.mu.Lock()
	j := fx.h.jobs[v.ID]
	intent, stopping, attempt := j.stopIntent, j.stopping, j.stopAttempt
	fx.h.mu.Unlock()
	if intent || stopping || attempt != nil {
		t.Fatalf("intent %v stopping %v attempt %v after the failed attempt", intent, stopping, attempt != nil)
	}
	fx.holders.mu.Lock()
	fx.holders.stopErr = nil
	fx.holders.mu.Unlock()
	sum, err := fx.h.StopByUser("lead", v.ID)
	if err != nil || sum.State != HostedStopped || !sum.StoppedByUser || sum.StopNotice != StopNoticeSent {
		t.Fatalf("the next stop = %+v, %v", sum, err)
	}
	for _, n := range fx.sessions.noticeList() {
		if strings.HasSuffix(n.event, ":exit") {
			t.Fatalf("a completion notice went for a user-stopped job: %+v", n)
		}
	}
	if len(stopNotices(fx)) != 1 {
		t.Fatalf("stop notices = %+v", stopNotices(fx))
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
	if s, _ := fx.h.StopByUser("lead", deferred.ID); s.StopNotice != StopNoticePending {
		t.Fatalf("a deferred notice must not read as sent: %+v", s)
	}
	fx.sessions.mu.Lock()
	fx.sessions.outcome = 0
	fx.sessions.mu.Unlock()
	// The completion notices' retry pass admits it, with the same event id.
	fx.h.deliverPending()
	if got := fx.h.Jobs("lead"); len(stopNotices(fx)) != 1 || stopNotices(fx)[0].event != "exec:"+deferred.ID+":stopped-by-user" {
		t.Fatalf("the retry pass: jobs %+v, notices %+v", got, stopNotices(fx))
	}
	if s, _ := fx.h.StopByUser("lead", deferred.ID); s.StopNotice != StopNoticeSent {
		t.Fatalf("a deferred notice that landed = %+v", s)
	}
	fx.sessions.mu.Lock()
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
	// (the closed job's notice was not recorded by the fake; the deferred
	// job's and the failed job's were, once each.)
	if _, err := fx.h.StopByUser("lead", failed.ID); err != nil || len(stopNotices(fx)) != 2 {
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

// TestRevisionMovesOnceTheStartTimeIsSet: the revision fires at admission,
// before the job has its start time, and again once it is spawned and saved,
// so a list read in between is never the last the page holds.
func TestRevisionMovesOnceTheStartTimeIsSet(t *testing.T) {
	var mu sync.Mutex
	var starts []int64
	fx := &hostedFixture{t: t, holders: newFakeExecHolders(), sessions: newFakeExecSessions(), store: newMemHostedStore(), exitDir: t.TempDir()}
	fx.onRunning = func() {
		var at int64 = -1
		if jobs := fx.h.Jobs("lead"); len(jobs) == 1 {
			at = jobs[0].StartedAt
		}
		mu.Lock()
		starts = append(starts, at)
		mu.Unlock()
	}
	fx.open(time.Second)
	fx.asyncJob(HostedLaunch{})
	mu.Lock()
	defer mu.Unlock()
	if len(starts) == 0 || starts[len(starts)-1] <= 0 {
		t.Fatalf("the start times each revision saw = %v: the last must carry one", starts)
	}
}

// TestStopNoticeCarriesNoMoreOutputThanTheCompletionNotice: the notice goes to
// the shared conversation like the completion notice does, so it holds the
// same output tail and no more.
func TestStopNoticeCarriesNoMoreOutputThanTheCompletionNotice(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	fx.holders.out(v.HolderID, "stdout", strings.Repeat("x", 3*HostedPreviewBytes)+"\n")
	if _, err := fx.h.StopByUser("lead", v.ID); err != nil {
		t.Fatal(err)
	}
	n := stopNotices(fx)
	if len(n) != 1 {
		t.Fatalf("notices = %+v", fx.sessions.noticeList())
	}
	// The tail is the last HostedPreviewBytes bytes: that many minus the
	// final newline of x's.
	run := strings.Repeat("x", HostedPreviewBytes-1)
	if !strings.Contains(n[0].text, run+"\n") || strings.Contains(n[0].text, "x"+run) {
		t.Fatalf("the notice does not hold exactly the last %d output bytes", HostedPreviewBytes)
	}
	if got := len(n[0].text); got > HostedPreviewBytes+1024 {
		t.Fatalf("the notice is %d bytes long, more than the tail %d and its fixed text", got, HostedPreviewBytes)
	}
	if !strings.Contains(n[0].text, fmt.Sprintf("output (last %d of %d bytes)", HostedPreviewBytes, 3*HostedPreviewBytes+1)) {
		t.Fatalf("notice lacks the tail header:\n%.300s", n[0].text)
	}
}

// TestUserStopOfAnUnknownHolderUnderAnIncompleteCensusStaysRunning: the
// manager not knowing a holder proves it gone only when its census read every
// holder. Under an incomplete one the stop is an error, the job stays running
// and unmarked with no notice, and the stop works once the census is complete.
// The agent's own stop (exec.stop) shares the path.
func TestUserStopOfAnUnknownHolderUnderAnIncompleteCensusStaysRunning(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	fx.holders.mu.Lock()
	delete(fx.holders.live, v.HolderID) // the manager does not know it
	fx.holders.incomplete = true
	fx.holders.mu.Unlock()
	if _, err := fx.h.StopByUser("lead", v.ID); err == nil || !strings.Contains(err.Error(), "census is incomplete") {
		t.Fatalf("a user stop under an incomplete census = %v", err)
	}
	if _, err := fx.h.Stop("lead", v.ID); err == nil {
		t.Fatal("exec.stop under an incomplete census must be an error too")
	}
	fx.h.mu.Lock()
	j := fx.h.jobs[v.ID]
	state, intent, stopping := j.row.State, j.stopIntent, j.stopping
	fx.h.mu.Unlock()
	if state != HostedRunning || intent || stopping {
		t.Fatalf("state %s intent %v stopping %v after the refused stop", state, intent, stopping)
	}
	if n := fx.sessions.noticeList(); len(n) != 0 {
		t.Fatalf("notices after a refused stop: %+v", n)
	}
	fx.holders.mu.Lock()
	fx.holders.incomplete = false
	fx.holders.mu.Unlock()
	sum, err := fx.h.StopByUser("lead", v.ID)
	if err != nil || sum.State != HostedStopped || !sum.StoppedByUser || len(stopNotices(fx)) != 1 {
		t.Fatalf("the stop under a complete census = %+v, %v, notices %+v", sum, err, fx.sessions.noticeList())
	}
}

// TestSpawnFailureSettlesBeforeAWaitingUserStopReturns: the hand-over fails
// while a user stop waits for it. The stop does not ask the holder a second
// time or end the job itself: the start's own cleanup settles the row once
// (failed, not a user's stop, no notice), and the stop then returns.
func TestSpawnFailureSettlesBeforeAWaitingUserStopReturns(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	entered, release := make(chan string, 1), make(chan struct{})
	fx.holders.mu.Lock()
	fx.holders.beforeSpawn = func(id string) {
		entered <- id
		<-release
	}
	fx.holders.spawnErr = errors.New("spawn refused")
	fx.holders.mu.Unlock()
	launched := make(chan error, 1)
	go func() {
		_, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true, Display: "x", Command: "x"})
		launched <- err
	}()
	<-entered
	var id string
	for _, s := range fx.h.Jobs("lead") {
		id = s.ID
	}
	stopped := make(chan error, 1)
	go func() { _, err := fx.h.StopByUser("lead", id); stopped <- err }()
	// Once the stop owns the attempt it can only be waiting for the
	// hand-over: the spawn has not returned, so it cannot have finished.
	eventuallyExec(t, "the stop to own its attempt", func() bool {
		fx.h.mu.Lock()
		defer fx.h.mu.Unlock()
		return fx.h.jobs[id].stopAttempt != nil
	})
	select {
	case err := <-stopped:
		t.Fatalf("the stop returned before the hand-over settled: %v", err)
	default:
	}
	close(release)
	if err := <-launched; err == nil {
		t.Fatal("the launch must fail")
	}
	if err := <-stopped; err != nil {
		t.Fatalf("the waiting stop = %v", err)
	}
	fx.holders.mu.Lock()
	calls := fx.holders.stopCalls
	fx.holders.mu.Unlock()
	if calls != 1 {
		t.Fatalf("the holder was stopped %d times, want 1 (the cleanup's)", calls)
	}
	sum := fx.h.Jobs("lead")[0]
	if sum.State != HostedFailed || sum.StoppedByUser || len(fx.sessions.noticeList()) != 0 {
		t.Fatalf("job = %+v, notices %+v", sum, fx.sessions.noticeList())
	}
}

// TestDeferredStopNoticeRetriesWithoutMovingTheRevision: a deferred notice
// tried again and deferred again changes nothing the list shows, so the
// revision stays; the retry that lands moves it.
func TestDeferredStopNoticeRetriesWithoutMovingTheRevision(t *testing.T) {
	var revs atomic.Int32
	fx := &hostedFixture{t: t, holders: newFakeExecHolders(), sessions: newFakeExecSessions(), store: newMemHostedStore(), exitDir: t.TempDir()}
	fx.onRunning = func() { revs.Add(1) }
	fx.open(time.Second)
	fx.sessions.mu.Lock()
	fx.sessions.outcome = NoticeDeferred
	fx.sessions.mu.Unlock()
	v := fx.asyncJob(HostedLaunch{})
	if s, _ := fx.h.StopByUser("lead", v.ID); s.StopNotice != StopNoticePending {
		t.Fatalf("stop = %+v", s)
	}
	before := revs.Load()
	fx.h.deliverPending()
	fx.h.deliverPending()
	if s, _ := fx.h.StopByUser("lead", v.ID); s.StopNotice != StopNoticePending {
		t.Fatalf("still handed off: %+v", s)
	}
	if got := revs.Load(); got != before {
		t.Fatalf("revision moved %d times across deferred retries", got-before)
	}
	fx.sessions.mu.Lock()
	fx.sessions.outcome = 0
	fx.sessions.mu.Unlock()
	fx.h.deliverPending()
	if got := fx.h.Jobs("lead")[0]; got.StopNotice != StopNoticeSent || revs.Load() == before {
		t.Fatalf("the landed retry: %+v, revisions %d -> %d", got, before, revs.Load())
	}
}

// TestPruneKeepsAJobWhoseStopNoticeIsStillPending: terminal-row pruning keeps
// a user-stopped job whose notice a handed-off session deferred, as it keeps a
// row with a pending completion notice, so the retry on takeover still finds it.
func TestPruneKeepsAJobWhoseStopNoticeIsStillPending(t *testing.T) {
	// One frozen clock: every job ends in the same millisecond, so only the
	// order they ended in (endSeq) ranks them for pruning.
	frozen := time.UnixMilli(1_700_000_000_000)
	fx := &hostedFixture{t: t, holders: newFakeExecHolders(), sessions: newFakeExecSessions(), store: newMemHostedStore(), exitDir: t.TempDir(), now: func() time.Time { return frozen }}
	fx.open(time.Second)
	fx.sessions.mu.Lock()
	fx.sessions.outcome = NoticeDeferred
	fx.sessions.mu.Unlock()
	first := fx.asyncJob(HostedLaunch{})
	if s, _ := fx.h.StopByUser("lead", first.ID); s.StopNotice != StopNoticePending {
		t.Fatalf("stop = %+v", s)
	}
	// The session takes the other jobs' completion notices, so only the
	// first job's stop notice stays owed.
	fx.sessions.mu.Lock()
	fx.sessions.outcome = 0
	fx.sessions.mu.Unlock()
	for i := 0; i < HostedMaxJobs+5; i++ {
		v := fx.asyncJob(HostedLaunch{})
		fx.holders.exit(v.HolderID, 0)
	}
	if _, err := fx.h.Get("lead", first.ID); err != nil {
		t.Fatalf("the job with a pending stop notice was pruned: %v", err)
	}
	if n := len(fx.h.Jobs("lead")); n > HostedMaxJobs+1 {
		t.Fatalf("jobs = %d: nothing else was pruned either", n)
	}
}

// TestUserStopOfAHolderTheCensusCompletesAfterItsLookupStillFails: the
// manager's answer is taken with its index lookup. A scan that indexes the
// holder and completes the census right after must not turn that answer into
// a success: the job stays running and unmarked, with no notice.
func TestUserStopOfAHolderTheCensusCompletesAfterItsLookupStillFails(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v := fx.asyncJob(HostedLaunch{})
	fx.holders.mu.Lock()
	session := fx.holders.live[v.HolderID]
	delete(fx.holders.live, v.HolderID)
	fx.holders.incomplete = true
	fx.holders.afterUnknownStop = func() {
		// The rediscovering scan: the holder is indexed, the census complete.
		fx.holders.mu.Lock()
		fx.holders.live[v.HolderID] = session
		fx.holders.incomplete = false
		fx.holders.mu.Unlock()
	}
	fx.holders.mu.Unlock()
	if _, err := fx.h.StopByUser("lead", v.ID); err == nil {
		t.Fatal("a stop answered unknown under an incomplete census must fail, whatever a later scan finds")
	}
	fx.h.mu.Lock()
	j := fx.h.jobs[v.ID]
	state, intent, stopping := j.row.State, j.stopIntent, j.stopping
	fx.h.mu.Unlock()
	if state != HostedRunning || intent || stopping || len(fx.sessions.noticeList()) != 0 {
		t.Fatalf("state %s intent %v stopping %v notices %+v", state, intent, stopping, fx.sessions.noticeList())
	}
	fx.holders.mu.Lock()
	fx.holders.afterUnknownStop = nil
	fx.holders.mu.Unlock()
	if sum, err := fx.h.StopByUser("lead", v.ID); err != nil || sum.State != HostedStopped || !sum.StoppedByUser {
		t.Fatalf("the retry against the indexed holder = %+v, %v", sum, err)
	}
}

// TestPruneKeepsAUserStoppedJobThroughItsFirstNoticeAdmission: from the end
// of a marked stop until its first notice outcome is recorded, the row is owed
// that notice and pruning must keep it, or a deferred first outcome schedules
// a retry for a job the timer cannot find.
func TestPruneKeepsAUserStoppedJobThroughItsFirstNoticeAdmission(t *testing.T) {
	frozen := time.UnixMilli(1_700_000_000_000)
	fx := &hostedFixture{t: t, holders: newFakeExecHolders(), sessions: newFakeExecSessions(), store: newMemHostedStore(), exitDir: t.TempDir(), now: func() time.Time { return frozen }}
	fx.open(time.Second)
	first := fx.asyncJob(HostedLaunch{})
	for i := 1; i < HostedMaxJobs; i++ {
		fx.asyncJob(HostedLaunch{})
	}
	inFlight, releaseCh := make(chan struct{}), make(chan struct{})
	// release opens the held admission once; the cleanup runs it too, so a
	// failure before the explicit release leaves no parked goroutine.
	release := sync.OnceFunc(func() { close(releaseCh) })
	t.Cleanup(release)
	fx.sessions.mu.Lock()
	fx.sessions.outcome = NoticeDeferred
	fx.sessions.beforeNotify = func(string) {
		close(inFlight)
		<-releaseCh
	}
	fx.sessions.mu.Unlock()
	stopped := make(chan HostedSummary, 1)
	go func() { s, _ := fx.h.StopByUser("lead", first.ID); stopped <- s }()
	select { // the first admission is in flight: no outcome is recorded yet
	case <-inFlight:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first stop notice admission to start")
	}
	fx.sessions.mu.Lock()
	fx.sessions.beforeNotify = nil
	fx.sessions.mu.Unlock()
	fx.asyncJob(HostedLaunch{}) // the 201st job: admission prunes a finished one
	if _, err := fx.h.Get("lead", first.ID); err != nil {
		t.Fatalf("the stopped job was pruned while its notice was being admitted: %v", err)
	}
	release()
	if s := <-stopped; s.StopNotice != StopNoticePending {
		t.Fatalf("the deferred first outcome = %+v", s)
	}
	fx.sessions.mu.Lock()
	fx.sessions.outcome = 0
	fx.sessions.mu.Unlock()
	fx.h.deliverPending()
	if n := stopNotices(fx); len(n) != 1 || n[0].event != "exec:"+first.ID+":stopped-by-user" {
		t.Fatalf("the retry did not find the job: %+v", n)
	}
}

// TestFailStartThatFindsAStopsExitRecordMovesTheRevision: a holder whose stop
// sends its exit record ends the job as stopped-by-user for a moment; the
// start's cleanup then makes it failed, which changes what the list shows, so
// the revision moves once more and the job ends failed and unmarked.
func TestFailStartThatFindsAStopsExitRecordMovesTheRevision(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	fx := &hostedFixture{t: t, holders: newFakeExecHolders(), sessions: newFakeExecSessions(), store: newMemHostedStore(), exitDir: t.TempDir()}
	fx.onRunning = func() {
		state := "none"
		if jobs := fx.h.Jobs("lead"); len(jobs) == 1 {
			state = jobs[0].State
			if jobs[0].StoppedByUser {
				state += "/user"
			}
		}
		mu.Lock()
		seen = append(seen, state)
		mu.Unlock()
	}
	fx.open(time.Second)
	entered, release := make(chan string, 1), make(chan struct{})
	fx.holders.mu.Lock()
	fx.holders.stopEmitsExit = true
	fx.holders.spawnErr = errors.New("spawn refused")
	fx.holders.beforeSpawn = func(id string) {
		entered <- id
		<-release
	}
	fx.holders.mu.Unlock()
	launched := make(chan error, 1)
	go func() {
		_, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true, Display: "x", Command: "x"})
		launched <- err
	}()
	<-entered
	id := fx.h.Jobs("lead")[0].ID
	stopped := make(chan error, 1)
	go func() { _, err := fx.h.StopByUser("lead", id); stopped <- err }()
	eventuallyExec(t, "the stop to own its attempt", func() bool {
		fx.h.mu.Lock()
		defer fx.h.mu.Unlock()
		return fx.h.jobs[id].stopAttempt != nil
	})
	close(release)
	<-launched
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	sum := fx.h.Jobs("lead")[0]
	mu.Lock()
	last := seen[len(seen)-1]
	mu.Unlock()
	if sum.State != HostedFailed || sum.StoppedByUser || last != HostedFailed {
		t.Fatalf("job %+v; the states the revisions saw = %v", sum, seen)
	}
}

// TestHostedRunningAgentOwnersAndTreeOps verifies memory-only multi-owner
// queries, strict OriginAgent vs OriginUser separation, deterministic sorting,
// and cross-owner isolation for ReadOutputForOwners and StopByUserForOwners.
func TestHostedRunningAgentOwnersAndTreeOps(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	fx.sessions.mu.Lock()
	fx.sessions.owners["leadA"] = HostedOwner{ID: "leadA", Gen: 1, Lead: true}
	fx.sessions.owners["childA1"] = HostedOwner{ID: "childA1", Gen: 1}
	fx.sessions.owners["leadB"] = HostedOwner{ID: "leadB", Gen: 1, Lead: true}
	fx.sessions.mu.Unlock()

	// Launch an agent job on lead, an agent job on child, a user job on lead,
	// and an agent job on an unrelated peer.
	leadAgent, err := fx.launch(context.Background(), nil, HostedLaunch{
		Owner: "leadA", Origin: OriginAgent, Async: true, Tool: "exec.shell", Command: "lead-cmd", Display: "lead-cmd",
	})
	if err != nil {
		t.Fatal(err)
	}
	childAgent, err := fx.launch(context.Background(), nil, HostedLaunch{
		Owner: "childA1", Origin: OriginAgent, Async: true, Tool: "exec.shell", Command: "child-cmd", Display: "child-cmd",
	})
	if err != nil {
		t.Fatal(err)
	}
	leadUser, err := fx.launch(context.Background(), nil, HostedLaunch{
		Owner: "leadA", Origin: OriginUser, Async: true, Tool: "exec.shell", Command: "user-cmd", Display: "user-cmd",
	})
	if err != nil {
		t.Fatal(err)
	}
	peerAgent, err := fx.launch(context.Background(), nil, HostedLaunch{
		Owner: "leadB", Origin: OriginAgent, Async: true, Tool: "exec.shell", Command: "peer-cmd", Display: "peer-cmd",
	})
	if err != nil {
		t.Fatal(err)
	}

	// RunningAgentOwners must exclude OriginUser jobs.
	agentOwners := fx.h.RunningAgentOwners()
	if agentOwners["leadA"] != 1 || agentOwners["childA1"] != 1 || agentOwners["leadB"] != 1 {
		t.Fatalf("RunningAgentOwners = %v, want leadA=1 childA1=1 leadB=1", agentOwners)
	}
	allOwners := fx.h.RunningOwners()
	if allOwners["leadA"] != 2 {
		t.Fatalf("RunningOwners[leadA] = %d, want 2 (1 agent + 1 user)", allOwners["leadA"])
	}

	// JobsForOwners with OriginAgent over {leadA, childA1} returns only those 2 agent jobs.
	treeOwners := map[string]bool{"leadA": true, "childA1": true}
	got := fx.h.JobsForOwners(treeOwners, OriginAgent)
	if len(got) != 2 {
		t.Fatalf("JobsForOwners len = %d, want 2: %+v", len(got), got)
	}
	seenIDs := map[string]string{}
	for _, j := range got {
		if j.Origin == OriginUser {
			t.Fatalf("JobsForOwners(OriginAgent) leaked user job: %+v", j)
		}
		seenIDs[j.ID] = j.Owner
	}
	if seenIDs[leadAgent.ID] != "leadA" || seenIDs[childAgent.ID] != "childA1" {
		t.Fatalf("JobsForOwners owners = %v", seenIDs)
	}

	// ReadOutputForOwners allows child's agent job, rejects peer's job and user job.
	if _, err := fx.h.ReadOutputForOwners(treeOwners, OriginAgent, childAgent.ID, 0, 1024); err != nil {
		t.Fatalf("ReadOutputForOwners(childAgent) = %v, want nil", err)
	}
	if _, err := fx.h.ReadOutputForOwners(treeOwners, OriginAgent, peerAgent.ID, 0, 1024); !errors.Is(err, ErrExecNotFound) {
		t.Fatalf("ReadOutputForOwners(peerAgent) = %v, want ErrExecNotFound", err)
	}
	if _, err := fx.h.ReadOutputForOwners(treeOwners, OriginAgent, leadUser.ID, 0, 1024); !errors.Is(err, ErrExecNotFound) {
		t.Fatalf("ReadOutputForOwners(leadUser) = %v, want ErrExecNotFound", err)
	}

	// StopByUserForOwners rejects peer's job and user job, succeeds on child's agent job.
	if _, err := fx.h.StopByUserForOwners(treeOwners, OriginAgent, peerAgent.ID); !errors.Is(err, ErrExecNotFound) {
		t.Fatalf("StopByUserForOwners(peerAgent) = %v, want ErrExecNotFound", err)
	}
	if _, err := fx.h.StopByUserForOwners(treeOwners, OriginAgent, leadUser.ID); !errors.Is(err, ErrExecNotFound) {
		t.Fatalf("StopByUserForOwners(leadUser) = %v, want ErrExecNotFound", err)
	}
	stopped, err := fx.h.StopByUserForOwners(treeOwners, OriginAgent, childAgent.ID)
	if err != nil || stopped.Owner != "childA1" {
		t.Fatalf("StopByUserForOwners(childAgent) = %+v, %v", stopped, err)
	}
	eventuallyExec(t, "childAgent terminal", func() bool {
		return fx.h.RunningAgentOwners()["childA1"] == 0
	})

	// After childAgent stops, running leadAgent sorts before finished childAgent.
	after := fx.h.JobsForOwners(treeOwners, OriginAgent)
	if len(after) != 2 || after[0].ID != leadAgent.ID || after[1].ID != childAgent.ID {
		t.Fatalf("JobsForOwners order after stop = %+v", after)
	}

	// Cleanup remaining jobs.
	fx.holders.exit(leadAgent.HolderID, 0)
	fx.holders.exit(leadUser.HolderID, 0)
	fx.holders.exit(peerAgent.HolderID, 0)
}
