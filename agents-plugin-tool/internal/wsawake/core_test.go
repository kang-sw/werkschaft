package wsawake

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBackend records calls; fail makes the next Acquire calls fail, and
// gate, when set, blocks each call until a value is sent.
type fakeBackend struct {
	mu       sync.Mutex
	held     bool
	acquires int
	releases int
	fail     int
	gate     chan struct{}
	entered  chan struct{}
	changed  chan struct{}
}

func newFake() *fakeBackend {
	return &fakeBackend{changed: make(chan struct{}, 64), entered: make(chan struct{}, 64)}
}

// gated makes every call announce itself on entered and wait for gate.
func newGatedFake() *fakeBackend {
	f := newFake()
	f.gate = make(chan struct{})
	return f
}

func (f *fakeBackend) waitGate() {
	if f.gate != nil {
		f.entered <- struct{}{}
		<-f.gate
	}
}

// awaitEntered waits until a gated call began.
func (f *fakeBackend) awaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no backend call began")
	}
}

func (f *fakeBackend) Acquire() error {
	f.waitGate()
	f.mu.Lock()
	defer f.mu.Unlock()
	defer func() { f.changed <- struct{}{} }()
	f.acquires++
	if f.held {
		panic("Acquire while held")
	}
	if f.fail > 0 {
		f.fail--
		return errors.New("refused")
	}
	f.held = true
	return nil
}

func (f *fakeBackend) Release() error {
	f.waitGate()
	f.mu.Lock()
	defer f.mu.Unlock()
	defer func() { f.changed <- struct{}{} }()
	if !f.held {
		panic("Release while not held")
	}
	f.releases++
	f.held = false
	return nil
}

func (f *fakeBackend) state() (held bool, acquires, releases int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held, f.acquires, f.releases
}

// waitHeld waits until the fake's held state is want.
func waitHeld(t *testing.T, f *fakeBackend, want bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if held, _, _ := f.state(); held == want {
			return
		}
		select {
		case <-f.changed:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("held never became %v", want)
		}
	}
}

// settle gives the worker time to act on a change that should do nothing.
func settle() { time.Sleep(30 * time.Millisecond) }

func TestCoreHoldsWhileEnabledWithMembers(t *testing.T) {
	f := newFake()
	c := New(f, nil)
	defer c.Close()

	a, b := new(int), new(int)
	c.SetMember(a, true)
	settle()
	if held, acq, _ := f.state(); held || acq != 0 {
		t.Fatalf("disabled core acquired: held=%v acquires=%d", held, acq)
	}
	c.SetEnabled(true) // enabling with a live turn acquires
	waitHeld(t, f, true)
	c.SetMember(b, true) // overlap: no second acquisition
	c.SetMember(a, true) // duplicate report
	settle()
	if _, acq, _ := f.state(); acq != 1 || c.Members() != 2 {
		t.Fatalf("overlap: acquires=%d members=%d", acq, c.Members())
	}
	c.SetMember(a, false)
	c.SetMember(a, false) // late duplicate end
	settle()
	if held, _, _ := f.state(); !held || c.Members() != 1 {
		t.Fatalf("one member left: held=%v members=%d", held, c.Members())
	}
	c.SetMember(b, false) // last live turn ends
	waitHeld(t, f, false)
	c.SetMember(a, true) // first live turn while enabled
	waitHeld(t, f, true)
	c.SetEnabled(false) // disable with a live turn releases
	waitHeld(t, f, false)
	if _, acq, rel := f.state(); acq != 2 || rel != 2 {
		t.Fatalf("acquires=%d releases=%d, want 2 and 2", acq, rel)
	}
}

func TestCoreRetriesAfterFailureOnlyOnNewActivation(t *testing.T) {
	f := newFake()
	f.fail = 1
	var log bytes.Buffer
	var logMu sync.Mutex
	c := New(f, writerFunc(func(p []byte) (int, error) { logMu.Lock(); defer logMu.Unlock(); return log.Write(p) }))
	defer c.Close()

	a, b := new(int), new(int)
	c.SetEnabled(true)
	c.SetMember(a, true)
	waitAcquires(t, f, 1)
	c.SetMember(b, true) // still eligible: no retry
	c.SetEnabled(true)
	settle()
	if held, acq, _ := f.state(); held || acq != 1 {
		t.Fatalf("retried within one activation: held=%v acquires=%d", held, acq)
	}
	logMu.Lock()
	if n := strings.Count(log.String(), "take the idle-sleep assertion"); n != 1 {
		t.Fatalf("logged %d failures, want 1: %q", n, log.String())
	}
	logMu.Unlock()
	// Turns and the setting stay usable; the next transition to enabled
	// with live work tries again.
	c.SetEnabled(false)
	c.SetEnabled(true)
	waitHeld(t, f, true)
	c.SetMember(a, false)
	c.SetMember(b, false)
	waitHeld(t, f, false)
	// A new first turn after the last ended is a new activation too.
	f.mu.Lock()
	f.fail = 1
	f.mu.Unlock()
	c.SetMember(a, true)
	waitAcquires(t, f, 3)
	c.SetMember(a, false)
	c.SetMember(a, true)
	waitHeld(t, f, true)
}

func TestCoreChangesDuringBackendCallConverge(t *testing.T) {
	f := newGatedFake()
	c := New(f, nil)
	a := new(int)
	c.SetEnabled(true)
	c.SetMember(a, true)
	f.awaitEntered(t) // the worker is inside Acquire
	c.SetMember(a, false)
	c.SetMember(a, true)
	c.SetEnabled(false)  // newest state: disabled
	f.gate <- struct{}{} // Acquire completes
	f.awaitEntered(t)    // the worker sees the newer state and releases
	f.gate <- struct{}{}
	waitHeld(t, f, false)
	settle()
	if _, acq, rel := f.state(); acq != 1 || rel != 1 {
		t.Fatalf("acquires=%d releases=%d, want 1 and 1", acq, rel)
	}
	close(f.gate)
	c.Close()
}

func TestCoreCloseDrainsAndFencesLateAcquisition(t *testing.T) {
	f := newGatedFake()
	c := New(f, nil)
	a := new(int)
	c.SetEnabled(true)
	c.SetMember(a, true)
	f.awaitEntered(t) // the worker is inside Acquire
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	f.gate <- struct{}{} // the late Acquire succeeds after Close began
	f.awaitEntered(t)    // Close's drain releases it
	f.gate <- struct{}{}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	if held, acq, rel := f.state(); held || acq != 1 || rel != 1 {
		t.Fatalf("after Close: held=%v acquires=%d releases=%d", held, acq, rel)
	}
	close(f.gate)
	c.SetMember(new(int), true)
	c.SetEnabled(true)
	c.Close()
	settle()
	if held, acq, _ := f.state(); held || acq != 1 || c.Members() != 0 {
		t.Fatalf("closed core acquired again: held=%v acquires=%d members=%d", held, acq, c.Members())
	}
}

func TestCoreCloseWithoutWorker(t *testing.T) {
	f := newFake()
	c := New(f, nil)
	c.SetMember(new(int), true)
	c.Close()
	c.Close()
	if _, acq, _ := f.state(); acq != 0 {
		t.Fatalf("acquires=%d", acq)
	}
}

func waitAcquires(t *testing.T, f *fakeBackend, n int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if _, acq, _ := f.state(); acq >= n {
			return
		}
		select {
		case <-f.changed:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("acquires never reached %d", n)
		}
	}
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

// closeFake is a fakeBackend that counts Close calls.
type closeFake struct {
	*fakeBackend
	closes atomic.Int32
}

func (f *closeFake) Close() error {
	f.closes.Add(1)
	return nil
}

// The core closes its backend once its worker made the final Release, and
// never while the core runs.
func TestCoreClosesBackend(t *testing.T) {
	f := &closeFake{fakeBackend: newFake()}
	c := New(f, nil)
	c.SetEnabled(true)
	c.SetMember(new(int), true)
	waitHeld(t, f.fakeBackend, true)
	c.SetEnabled(false)
	waitHeld(t, f.fakeBackend, false)
	if f.closes.Load() != 0 {
		t.Fatal("the backend was closed before the core")
	}
	c.Close()
	if n := f.closes.Load(); n != 1 {
		t.Fatalf("backend closes = %d, want 1", n)
	}
}

// A core whose worker never ran made no backend call, so it closes nothing.
func TestCoreCloseWithoutWorkerClosesNoBackend(t *testing.T) {
	f := &closeFake{fakeBackend: newFake()}
	c := New(f, nil)
	c.SetMember(new(int), true)
	c.Close()
	if n := f.closes.Load(); n != 0 {
		t.Fatalf("backend closes = %d, want 0", n)
	}
}
