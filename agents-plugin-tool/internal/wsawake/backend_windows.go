//go:build windows

package wsawake

import (
	"fmt"
	"io"
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

// Execution-state flags of SetThreadExecutionState.
const (
	esSystemRequired uint32 = 0x00000001
	esContinuous     uint32 = 0x80000000
)

var procSetThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

func platformBackend(_ io.Writer, _ int) Backend {
	return newExecutionState(setThreadExecutionState)
}

// setThreadExecutionState calls the API on the calling thread; a zero result
// is its failure.
func setThreadExecutionState(flags uint32) error {
	if r, _, err := procSetThreadExecutionState.Call(uintptr(flags)); r == 0 {
		return fmt.Errorf("SetThreadExecutionState(%#x): %w", flags, err)
	}
	return nil
}

// executionState holds the idle-system-sleep assertion with
// ES_CONTINUOUS|ES_SYSTEM_REQUIRED and clears it with ES_CONTINUOUS. The
// state belongs to the thread that set it, so both calls run on one
// dedicated goroutine locked to its OS thread. Display sleep and sleep the
// user asks for stay under the OS's control.
//
// The OS drops a thread's execution state when the thread exits, and a
// goroutine that ends while locked takes its thread down with it. So a clear
// that fails retires the thread (its state goes with it), and the next call
// starts a fresh one; Close retires it the same way.
type executionState struct {
	set func(flags uint32) error

	mu     sync.Mutex
	thread *executionThread
}

// executionThread is one dedicated, locked goroutine; exited closes once it
// returned, and its OS thread ends with it.
type executionThread struct {
	reqs   chan executionRequest
	exited chan struct{}
}

type executionRequest struct {
	flags uint32
	done  chan error
}

func newExecutionState(set func(uint32) error) *executionState {
	return &executionState{set: set}
}

func (b *executionState) call(flags uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.thread == nil {
		b.thread = b.startThread()
	}
	done := make(chan error, 1)
	b.thread.reqs <- executionRequest{flags: flags, done: done}
	err := <-done
	if err != nil && flags == esContinuous {
		// The thread returns after answering a failed clear; wait for it so
		// the next call cannot reach the retiring thread.
		<-b.thread.exited
		b.thread = nil
		return fmt.Errorf("%w (retired the thread holding the execution state)", err)
	}
	return err
}

// startThread runs the dedicated goroutine. It never unlocks its thread:
// returning while locked ends the OS thread, and with it any execution
// state the thread still holds. It returns after a failed clear or once
// reqs is closed.
func (b *executionState) startThread() *executionThread {
	t := &executionThread{reqs: make(chan executionRequest), exited: make(chan struct{})}
	go func() {
		defer close(t.exited)
		runtime.LockOSThread()
		for req := range t.reqs {
			err := b.set(req.flags)
			req.done <- err
			if err != nil && req.flags == esContinuous {
				return
			}
		}
	}()
	return t
}

func (b *executionState) Acquire() error { return b.call(esContinuous | esSystemRequired) }

func (b *executionState) Release() error { return b.call(esContinuous) }

// Close retires the dedicated thread, if one runs; a later call would start
// a fresh one.
func (b *executionState) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.thread != nil {
		close(b.thread.reqs)
		<-b.thread.exited
		b.thread = nil
	}
	return nil
}
