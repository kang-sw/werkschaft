package wsholder

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests run real holders in a per-test holders directory, as
// manager_test.go does; testHoldersDir kills any a test leaves behind.

// holderPIDs are the PIDs of the holders m lists, by id.
func holderPIDs(m *Manager) map[string]int {
	out := map[string]int{}
	for _, h := range m.List().Holders {
		out[h.ID] = h.PID
	}
	return out
}

// TestStopAllEndsEveryHolderAndThenRefusesLaunches pins the full stop: the
// holders this manager started, each with a running child (a session in
// progress), a holder a gone daemon left that the manager has not scanned
// yet, and one it cannot attach to are all ended with their children, their
// directories removed, and reported stopped with nothing remaining; after
// the complete stop no holder starts.
func TestStopAllEndsEveryHolderAndThenRefusesLaunches(t *testing.T) {
	dir := testHoldersDir(t)
	lost, lostChild := launchWithChild(t, dir)
	unattachable(t, dir, lost, CauseNoAnswer)
	left, leftChild := launchWithChild(t, dir)

	m := testManager(t, dir, nil)
	a, err := m.Launch(nil)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	childA := spawnTicker(t, m, a)
	b, err := m.Launch(nil)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	childB := spawnTicker(t, m, b)
	pids := holderPIDs(m)

	res := m.StopAll()
	want := []string{lost.ID, left.ID, a, b}
	slices.Sort(want)
	if !slices.Equal(res.Stopped, want) || len(res.Remaining) != 0 {
		t.Fatalf("StopAll = stopped %v, remaining %+v; want stopped %v and none remaining", res.Stopped, res.Remaining, want)
	}
	for name, pid := range map[string]int{"holder " + a: pids[a], "holder " + b: pids[b], "holder " + lost.ID: lost.PID, "holder " + left.ID: left.PID} {
		if processAlive(pid) {
			t.Errorf("%s (pid %d) is alive after the full stop", name, pid)
		}
	}
	for name, pid := range map[string]int{"child of " + a: childA.PID, "child of " + b: childB.PID, "child of " + lost.ID: lostChild.PID, "child of " + left.ID: leftChild.PID} {
		waitFor(t, name+" to end with its holder", func() bool { return !processAlive(pid) })
	}
	for _, id := range want {
		if _, err := os.Stat(filepath.Join(dir, id)); !os.IsNotExist(err) {
			t.Errorf("holder %s's directory remains after the full stop: %v", id, err)
		}
	}
	if rest := m.List().Holders; len(rest) != 0 {
		t.Errorf("listing after the full stop = %+v, want none", rest)
	}
	if _, err := m.Launch(nil); !errors.Is(err, ErrStopping) {
		t.Fatalf("Launch after a complete full stop = %v, want ErrStopping", err)
	}
	if again := m.StopAll(); len(again.Stopped) != 0 || len(again.Remaining) != 0 {
		t.Errorf("a second StopAll = %+v, want nothing stopped or remaining", again)
	}
}

// TestStopAllReportsTheHoldersItCouldNotStop pins the partial failure: a
// holder whose guarded kill is refused (the process under its PID did not
// start when its descriptor says) keeps running with its child and is
// reported remaining with the reason, the other holder is still stopped,
// and launches are allowed again, since the caller keeps running.
func TestStopAllReportsTheHoldersItCouldNotStop(t *testing.T) {
	dir := testHoldersDir(t)
	refused, refusedChild := launchWithChild(t, dir)
	unattachable(t, dir, refused, CauseToken)

	m := testManager(t, dir, nil, func(o *Options) {
		// Both clocks disagree with the descriptor, so the refusal holds
		// whichever one this OS matches on.
		o.procStart = func(pid int) (time.Time, error) {
			real, err := processStartTime(pid)
			return real.Add(startTimeTolerance + time.Second), err
		}
		o.procTicks = func(pid int) (uint64, error) {
			real, err := processStartTicks(pid)
			if real != 0 {
				real++
			}
			return real, err
		}
	})
	ok, err := m.Launch(nil)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	okChild := spawnTicker(t, m, ok)
	okPID := holderPIDs(m)[ok]

	res := m.StopAll()
	if !slices.Equal(res.Stopped, []string{ok}) {
		t.Errorf("stopped = %v, want [%s]", res.Stopped, ok)
	}
	if len(res.Remaining) != 1 || res.Remaining[0].ID != refused.ID || res.Remaining[0].PID != refused.PID ||
		!strings.Contains(res.Remaining[0].Error, "start time") {
		t.Fatalf("remaining = %+v, want %s with the refused kill's reason", res.Remaining, refused.ID)
	}
	if processAlive(okPID) {
		t.Errorf("the stoppable holder (pid %d) is alive after the full stop", okPID)
	}
	waitFor(t, "the stoppable holder's child to end", func() bool { return !processAlive(okChild.PID) })
	if !processAlive(refused.PID) || !processAlive(refusedChild.PID) {
		t.Fatalf("the refused holder or its child ended: holder alive %v, child alive %v", processAlive(refused.PID), processAlive(refusedChild.PID))
	}
	if _, err := ReadDescriptor(filepath.Join(dir, refused.ID)); err != nil {
		t.Errorf("the refused holder's descriptor is gone: %v", err)
	}
	if _, err := m.Launch(nil); err != nil {
		t.Fatalf("Launch after an incomplete full stop = %v, want launches allowed again", err)
	}
}

// TestStopAllStopsTheHolderOfARunningLaunch pins the concurrent start: a
// launch already running when the full stop is asked for finishes, and its
// holder is stopped too; a launch asked for while the stop waits starts no
// holder that outlives it. No holder process remains either way.
func TestStopAllStopsTheHolderOfARunningLaunch(t *testing.T) {
	dir := testHoldersDir(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	m := testManager(t, dir, nil, func(o *Options) {
		o.start = func(exe string, args []string, wd string, breakaway bool) (*exec.Cmd, error) {
			once.Do(func() {
				close(entered)
				<-release
			})
			return startDetached(exe, args, wd, breakaway)
		}
	})
	type launched struct {
		id  string
		err error
	}
	first := make(chan launched, 1)
	go func() {
		id, err := m.Launch(nil)
		first <- launched{id, err}
	}()
	<-entered
	stopped := make(chan StopAllResult, 1)
	go func() { stopped <- m.StopAll() }()
	// Neither pause is needed for the assertions; they make the stop
	// likely to be waiting when the second launch is asked for.
	time.Sleep(50 * time.Millisecond)
	second := make(chan launched, 1)
	go func() {
		id, err := m.Launch(nil)
		second <- launched{id, err}
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)

	l1 := <-first
	if l1.err != nil {
		t.Fatalf("the running launch failed: %v", l1.err)
	}
	var res StopAllResult
	select {
	case res = <-stopped:
	case <-time.After(60 * time.Second):
		t.Fatal("StopAll did not return")
	}
	l2 := <-second
	if len(res.Remaining) != 0 || !slices.Contains(res.Stopped, l1.id) {
		t.Fatalf("StopAll = stopped %v, remaining %+v; want the running launch's holder %s stopped", res.Stopped, res.Remaining, l1.id)
	}
	switch {
	case errors.Is(l2.err, ErrStopping):
	case l2.err == nil && slices.Contains(res.Stopped, l2.id):
		// It took the operation lock before the stop did.
	default:
		t.Fatalf("the launch asked for during the stop = (%q, %v), want ErrStopping or its holder stopped", l2.id, l2.err)
	}
	if left := m.survivors(); len(left) != 0 {
		t.Fatalf("holders alive after the full stop: %+v", left)
	}
}
