//go:build windows

package wsawake

import (
	"errors"
	"runtime"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

// The set and clear calls run on one locked thread, whatever goroutine and
// thread ask for them.
func TestExecutionStateSetAndClearShareOneThread(t *testing.T) {
	var mu sync.Mutex
	type call struct {
		flags  uint32
		thread uint32
	}
	var calls []call
	b := newExecutionState(func(flags uint32) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call{flags, windows.GetCurrentThreadId()})
		return nil
	})
	for i := 0; i < 4; i++ {
		done := make(chan struct{})
		go func() {
			defer close(done)
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			if err := b.Acquire(); err != nil {
				t.Error(err)
			}
			if err := b.Release(); err != nil {
				t.Error(err)
			}
		}()
		<-done
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 8 {
		t.Fatalf("calls = %d, want 8", len(calls))
	}
	for i, c := range calls {
		want := esContinuous | esSystemRequired
		if i%2 == 1 {
			want = esContinuous
		}
		if c.flags != want {
			t.Fatalf("call %d flags = %#x, want %#x", i, c.flags, want)
		}
		if c.thread != calls[0].thread {
			t.Fatalf("call %d ran on thread %d, first on %d", i, c.thread, calls[0].thread)
		}
	}
}

// A clear that fails retires the dedicated thread, which ends locked and
// takes its execution state with it; the next acquire starts a fresh thread,
// and Close retires that one.
func TestExecutionStateFailedClearRetiresThread(t *testing.T) {
	var mu sync.Mutex
	failClear := true
	var threads []uint32
	b := newExecutionState(func(flags uint32) error {
		mu.Lock()
		defer mu.Unlock()
		threads = append(threads, windows.GetCurrentThreadId())
		if flags == esContinuous && failClear {
			failClear = false
			return errors.New("refused")
		}
		return nil
	})
	if err := b.Acquire(); err != nil {
		t.Fatal(err)
	}
	first := b.thread
	if err := b.Release(); err == nil {
		t.Fatal("a failed clear reported success")
	}
	if b.thread != nil {
		t.Fatal("the thread whose clear failed is still in use")
	}
	select {
	case <-first.exited:
	default:
		t.Fatal("the thread whose clear failed was not retired")
	}
	if err := b.Acquire(); err != nil {
		t.Fatal(err)
	}
	second := b.thread
	if second == nil || second == first {
		t.Fatal("the next acquire did not start a fresh thread")
	}
	if err := b.Release(); err != nil {
		t.Fatal(err)
	}
	if b.thread != second {
		t.Fatal("a successful clear retired the thread")
	}
	mu.Lock()
	if len(threads) != 4 || threads[0] != threads[1] || threads[2] != threads[3] {
		t.Fatalf("set and clear did not share their thread: %v", threads)
	}
	mu.Unlock()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second.exited:
	default:
		t.Fatal("Close left the thread running")
	}
	if b.thread != nil {
		t.Fatal("Close kept the retired thread")
	}
}
