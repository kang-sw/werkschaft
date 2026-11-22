//go:build windows

package wsawake

import (
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
