package execjob

import (
	"context"
	"path/filepath"
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
