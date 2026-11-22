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
// dedicated goroutine locked to its OS thread for the process's lifetime.
// Display sleep and sleep the user asks for stay under the OS's control.
type executionState struct {
	set  func(flags uint32) error
	once sync.Once
	reqs chan executionRequest
}

type executionRequest struct {
	flags uint32
	done  chan error
}

func newExecutionState(set func(uint32) error) *executionState {
	return &executionState{set: set, reqs: make(chan executionRequest)}
}

func (b *executionState) call(flags uint32) error {
	b.once.Do(func() {
		go func() {
			// Never unlocked: the thread keeps the state it was given until
			// it is cleared here.
			runtime.LockOSThread()
			for req := range b.reqs {
				req.done <- b.set(req.flags)
			}
		}()
	})
	done := make(chan error, 1)
	b.reqs <- executionRequest{flags: flags, done: done}
	return <-done
}

func (b *executionState) Acquire() error { return b.call(esContinuous | esSystemRequired) }

func (b *executionState) Release() error { return b.call(esContinuous) }
