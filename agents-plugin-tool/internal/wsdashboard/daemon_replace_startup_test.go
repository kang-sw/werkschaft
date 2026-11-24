package wsdashboard

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
)

type startupBurstAgentHost struct {
	stubAgentHost
	startCalls int
	onStart    func(notify func(replyID, project string))
}

func (h *startupBurstAgentHost) Start(notify func(replyID, project string)) {
	h.startCalls++
	if h.onStart != nil {
		h.onStart(notify)
	}
}

func (h *startupBurstAgentHost) StartForPublication(notify func(replyID, project string), _ <-chan struct{}) {
	h.startCalls++
	if h.onStart != nil {
		h.onStart(notify)
	}
}

type stubAgentHost struct{}

func (stubAgentHost) Handler() http.Handler               { return http.NotFoundHandler() }
func (stubAgentHost) Start(func(replyID, project string)) {}
func (stubAgentHost) InboxChanged(string)                 {}
func (stubAgentHost) Close()                              {}

type slowCredStore struct {
	delay  time.Duration
	closed atomic.Bool
}

func (s *slowCredStore) Credential(context.Context, wscred.Request) (wscred.Credential, error) {
	return wscred.Credential{}, nil
}

func (s *slowCredStore) Apply(wscred.Change) (wscred.ChangeResult, error) {
	return wscred.ChangeResult{}, nil
}

func (s *slowCredStore) Close() {
	time.Sleep(s.delay)
	s.closed.Store(true)
}

// TestSuccessorLockWaitAndDefaultStartWaitBudgets checks that replacement
// lock-wait and start-wait budgets are sized for busy predecessor shutdowns
// while keeping DefaultLockWait unchanged for proxy/credential drain math.
func TestSuccessorLockWaitAndDefaultStartWaitBudgets(t *testing.T) {
	if DefaultLockWait != 5*time.Second {
		t.Fatalf("DefaultLockWait = %s, want 5s (proxy drain budgets depend on it)", DefaultLockWait)
	}
	if SuccessorLockWait != 30*time.Second {
		t.Fatalf("SuccessorLockWait = %s, want 30s", SuccessorLockWait)
	}
	if DefaultStartWait < 45*time.Second {
		t.Fatalf("DefaultStartWait = %s, want >= 45s", DefaultStartWait)
	}

	got := appendLockWaitIfMissing([]string{"dashboard", "--port", "0"}, SuccessorLockWait.String())
	want := []string{"dashboard", "--port", "0", "--lock-wait", "30s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appendLockWaitIfMissing = %v, want %v", got, want)
	}

	custom := appendLockWaitIfMissing([]string{"dashboard", "--lock-wait", "12s"}, SuccessorLockWait.String())
	wantCustom := []string{"dashboard", "--lock-wait", "12s"}
	if !reflect.DeepEqual(custom, wantCustom) {
		t.Fatalf("appendLockWaitIfMissing overrode explicit flag: %v", custom)
	}

	customEq := appendLockWaitIfMissing([]string{"dashboard", "--lock-wait=12s"}, SuccessorLockWait.String())
	wantCustomEq := []string{"dashboard", "--lock-wait=12s"}
	if !reflect.DeepEqual(customEq, wantCustomEq) {
		t.Fatalf("appendLockWaitIfMissing overrode explicit =flag: %v", customEq)
	}
}

// TestDaemonCloseRunsIndependentTeardownInParallelBeforeUnlock verifies that
// independent pre-unlock teardown steps run concurrently and complete before
// Close releases the singleton lock.
func TestDaemonCloseRunsIndependentTeardownInParallelBeforeUnlock(t *testing.T) {
	dir := t.TempDir()
	const stepDelay = 200 * time.Millisecond
	creds := &slowCredStore{delay: stepDelay}
	var beforeUnlockDone atomic.Bool

	d, err := StartDaemon(DaemonOptions{
		Dir:              dir,
		Port:             0,
		Version:          "1.0.0",
		credentialSource: creds,
		BeforeUnlock: func() {
			time.Sleep(stepDelay)
			beforeUnlockDone.Store(true)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	d.Close()
	elapsed := time.Since(started)

	if !creds.closed.Load() || !beforeUnlockDone.Load() {
		t.Fatalf("teardown did not complete before Close returned: creds=%v beforeUnlock=%v", creds.closed.Load(), beforeUnlockDone.Load())
	}
	// Serial execution would take >= 400ms; parallel should finish well under that.
	if elapsed >= 2*stepDelay-30*time.Millisecond {
		t.Fatalf("Close took %s, expected parallel teardown faster than %s", elapsed, 2*stepDelay)
	}
}

// TestDaemonStartupSuppressesPerConvRereadLiveStorm verifies that bulk startup
// notifications during StartForPublication do not trigger per-conversation
// agentChanged / RereadLive wakes before Serve & poll's initial Index.Refresh,
// while post-startup notifications continue to fire normally.
func TestDaemonStartupSuppressesPerConvRereadLiveStorm(t *testing.T) {
	dir := t.TempDir()
	var savedNotify func(replyID, project string)
	agents := &startupBurstAgentHost{
		onStart: func(notify func(replyID, project string)) {
			savedNotify = notify
			for i := range 25 {
				notify("abcdef012345678"+string(rune('0'+i%10)), dir)
			}
		},
	}

	d, err := StartDaemon(DaemonOptions{
		Dir:      dir,
		Port:     0,
		Version:  "1.0.0",
		Agents:   agents,
		Sessions: emptySessionSource{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if got := d.stats.WakeAgent.Load(); got != 0 {
		t.Fatalf("WakeAgent during StartForPublication = %d, want 0", got)
	}
	if savedNotify == nil {
		t.Fatal("StartForPublication did not receive notify callback")
	}
	savedNotify("abcdef0123456789", dir)
	if got := d.stats.WakeAgent.Load(); got != 1 {
		t.Fatalf("WakeAgent after startup = %d, want 1", got)
	}
}

type emptySessionSource struct{}

func (emptySessionSource) ReadSession(Registration) (SessionData, bool, error) {
	return SessionData{}, false, nil
}

// TestWaitForSuccessorHonorsExplicitShortTimeout verifies waitForSuccessor
// succeeds when a successor daemon publishes its endpoint and respects a short
// StartWait when no successor arrives.
func TestWaitForSuccessorHonorsExplicitShortTimeout(t *testing.T) {
	dir := t.TempDir()
	opts := EnsureOptions{
		Dir:          dir,
		Version:      "1.0.0",
		StartWait:    150 * time.Millisecond,
		PollInterval: 10 * time.Millisecond,
	}
	start := time.Now()
	if waitForSuccessor(context.Background(), testClient(), opts, "old-token", false, io.Discard) {
		t.Fatal("waitForSuccessor succeeded with no daemon")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("waitForSuccessor with 150ms StartWait took %s", elapsed)
	}

	// Now start a real daemon after a short delay and verify waitForSuccessor sees it.
	opts.StartWait = 3 * time.Second
	started := make(chan *Daemon, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		d, err := StartDaemon(DaemonOptions{Dir: dir, Port: 0, Version: "1.0.0"})
		if err == nil {
			started <- d
		} else {
			started <- nil
		}
	}()
	if !waitForSuccessor(context.Background(), testClient(), opts, "old-token", false, io.Discard) {
		t.Fatal("waitForSuccessor did not observe newly started successor")
	}
	if d := <-started; d != nil {
		d.Close()
	}
}
