package execjob

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A worktree with a running job refuses its release; with none, the fence
// refuses new jobs there (also in a subdirectory) until it lifts, while
// jobs elsewhere start.
func TestHostedFenceRootRefusesLiveJobsAndNewOnes(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	wt := filepath.Join(t.TempDir(), "wt")
	other := filepath.Join(t.TempDir(), "other")
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Root: wt, Cwd: filepath.Join(wt, "sub"), Async: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := fx.h.LiveIn(wt); n != 1 {
		t.Fatalf("LiveIn = %d", n)
	}
	if _, err := fx.h.FenceRoot(wt); err == nil || !strings.Contains(err.Error(), "1 exec job(s) still run") {
		t.Fatalf("FenceRoot with a live job: %v", err)
	}
	if _, err := fx.h.Stop("lead", v.ID); err != nil {
		t.Fatal(err)
	}
	unfence, err := fx.h.FenceRoot(wt)
	if err != nil {
		t.Fatalf("FenceRoot after the stop: %v", err)
	}
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Root: wt, Async: true}); err == nil || !strings.Contains(err.Error(), "being released or removed") {
		t.Fatalf("a launch into a fenced worktree: %v", err)
	}
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Root: other, Cwd: filepath.Join(wt, "deep"), Async: true}); err == nil {
		t.Fatal("a launch whose cwd lies in the fenced worktree started")
	}
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Root: other, Async: true}); err != nil {
		t.Fatalf("a launch elsewhere: %v", err)
	}
	unfence()
	unfence() // idempotent
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Root: wt, Async: true}); err != nil {
		t.Fatalf("a launch after the fence lifted: %v", err)
	}
}

// A closing owner's jobs end through StopOwner (no notice), its new jobs
// are refused while fenced, and one that passed admission while waiting
// for approval never starts its process.
func TestHostedOwnerFenceAndStopOwner(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	a, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := fx.launch(context.Background(), nil, HostedLaunch{Owner: "other", Async: true})
	if err != nil {
		t.Fatal(err)
	}
	fx.h.FenceOwner("lead")
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true}); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("a fenced owner's launch: %v", err)
	}
	fx.h.StopOwner("lead")
	for _, id := range []string{a.ID, b.ID} {
		if v, _ := fx.h.Get("lead", id); v.State != HostedStopped {
			t.Fatalf("job %s state %s", id, v.State)
		}
	}
	if v, _ := fx.h.Get("other", keep.ID); v.State != HostedRunning {
		t.Fatalf("another owner's job: %s", v.State)
	}
	if n := fx.h.LiveCount("lead"); n != 0 {
		t.Fatalf("live after StopOwner: %d", n)
	}
	if len(fx.sessions.noticeList()) != 0 {
		t.Fatalf("a stop sent notices: %+v", fx.sessions.noticeList())
	}
	fx.h.UnfenceOwner("lead")
	if _, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true}); err != nil {
		t.Fatalf("an unfenced owner's launch: %v", err)
	}

	// Admitted, then the owner closes while the start waits on the live
	// mode: the process never runs.
	release := make(chan struct{})
	fx.sessions.mu.Lock()
	fx.sessions.immediate = func(string) (bool, error) {
		<-release
		return true, nil
	}
	fx.sessions.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := fx.launch(context.Background(), nil, HostedLaunch{Owner: "other", Async: true})
		done <- err
	}()
	eventuallyExec(t, "the admitted job", func() bool { return fx.h.LiveCount("other") == 2 })
	fx.h.FenceOwner("other")
	fx.h.StopOwner("other")
	close(release)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "closed before the job started") {
		t.Fatalf("a start after the close: %v", err)
	}
	if n := fx.h.LiveCount("other"); n != 0 {
		t.Fatalf("live after the refused start: %d", n)
	}
}

// A close that lands while a job's process is being handed to its holder
// waits for the hand-over and ends the process it started: no process runs
// after StopOwner returns.
func TestHostedStopOwnerWaitsForAnInFlightSpawn(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	entered, release := make(chan string, 1), make(chan struct{})
	fx.holders.mu.Lock()
	fx.holders.beforeSpawn = func(id string) {
		entered <- id
		<-release
	}
	fx.holders.mu.Unlock()
	launched := make(chan error, 1)
	go func() {
		_, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
		launched <- err
	}()
	holder := <-entered
	fx.h.FenceOwner("lead")
	stopped := make(chan error, 1)
	go func() { stopped <- fx.h.StopOwner("lead") }()
	select {
	case err := <-stopped:
		t.Fatalf("StopOwner returned while the spawn ran: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	<-launched
	if !slices.Contains(fx.holders.stops(), holder) || fx.holders.spawnOf(holder).Argv == nil {
		t.Fatalf("the spawned holder was not stopped after its spawn: stops %v", fx.holders.stops())
	}
	if n := fx.h.LiveCount("lead"); n != 0 {
		t.Fatalf("live after StopOwner: %d", n)
	}
}

// A close that lands while a job's holder is still launching ends the job
// at once and, once the holder is up, stops it unspawned.
func TestHostedStopOwnerEndsAJobWhoseHolderIsLaunching(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	fx.holders.mu.Lock()
	fx.holders.beforeLaunch = func() {
		entered <- struct{}{}
		<-release
	}
	fx.holders.mu.Unlock()
	launched := make(chan error, 1)
	go func() {
		_, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
		launched <- err
	}()
	<-entered
	fx.h.FenceOwner("lead")
	if err := fx.h.StopOwner("lead"); err != nil {
		t.Fatal(err)
	}
	if n := fx.h.LiveCount("lead"); n != 0 {
		t.Fatalf("StopOwner returned with the job live: %d", n)
	}
	close(release)
	if err := <-launched; err == nil || !strings.Contains(err.Error(), "closed before the job started") {
		t.Fatalf("launch after the close: %v", err)
	}
	eventuallyExec(t, "the unspawned holder's stop", func() bool { return len(fx.holders.stops()) == 1 })
	fx.holders.mu.Lock()
	spawned := len(fx.holders.spawned)
	fx.holders.mu.Unlock()
	if spawned != 0 {
		t.Fatal("a job whose owner closed during its holder launch spawned")
	}
}

// A holder that does not go fails StopOwner: the job may still run, and
// the close must not report success.
func TestHostedStopOwnerReportsAHolderThatDidNotGo(t *testing.T) {
	fx := newHostedFixture(t, time.Second)
	v, err := fx.launch(context.Background(), nil, HostedLaunch{Async: true})
	if err != nil {
		t.Fatal(err)
	}
	fx.holders.mu.Lock()
	fx.holders.stopErr = errors.New("holder busy")
	fx.holders.mu.Unlock()
	if err := fx.h.StopOwner("lead"); err == nil || !strings.Contains(err.Error(), v.ID) || !strings.Contains(err.Error(), "holder busy") {
		t.Fatalf("StopOwner over a holder that did not go: %v", err)
	}
	if got, _ := fx.h.Get("lead", v.ID); got.State != HostedRunning {
		t.Fatalf("job state %s", got.State)
	}
}

// A legacy launch is counted from its admission: a worktree release that
// lands after the process started and before the job entered the active
// set is refused, and a fenced worktree refuses new legacy launches.
func TestLegacyLaunchCountsBeforeItsActiveEntry(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	root := gitRoot(t)
	entered, release := make(chan struct{}), make(chan struct{})
	beforeLegacyActive = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() { beforeLegacyActive = nil })
	done := make(chan error, 1)
	go func() {
		_, err := Launch(LaunchOptions{Root: root, Cmd: os.Args[0], Args: []string{"-test.run=TestHelperProcess", "--", "ok"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1"}})
		done <- err
	}()
	<-entered
	if got := ActiveRoots()[filepath.Clean(root)]; got != 0 {
		t.Fatalf("the job is active already (%d): the window under test is gone", got)
	}
	if _, err := FenceLegacyRoot(root); err == nil || !strings.Contains(err.Error(), "1 exec job(s) still run") {
		t.Fatalf("a release between the start and the active entry: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	beforeLegacyActive = nil
	unfence, err := FenceLegacyRoot(root)
	if err != nil {
		t.Fatalf("fence after the job ended: %v", err)
	}
	if _, err := Launch(LaunchOptions{Root: root, Cmd: os.Args[0], Args: []string{"-test.run=TestHelperProcess", "--", "ok"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1"}}); err == nil || !strings.Contains(err.Error(), "being released or removed") {
		t.Fatalf("a legacy launch into a fenced worktree: %v", err)
	}
	unfence()
	if _, err := Launch(LaunchOptions{Root: root, Cmd: os.Args[0], Args: []string{"-test.run=TestHelperProcess", "--", "ok"}, Env: map[string]string{"GO_WANT_HELPER_PROCESS": "1"}}); err != nil {
		t.Fatalf("a legacy launch after the fence lifted: %v", err)
	}
}
