package wsagent

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// A fn that exits its goroutine, as a t.Fatal inside the callback does, must
// release the caller of do: the failure then surfaces on the test goroutine
// instead of the run parking at the package timeout.
func TestConvDoReturnsWhenTheCallbackExitsItsGoroutine(t *testing.T) {
	h := permissionHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, initLine("do-goexit", "auto"), stateLine(HarnessRunning))
	h.sync(id, holder)
	c := h.runner.conv(id)

	returned := make(chan bool, 1)
	go func() { returned <- c.do(func() { runtime.Goexit() }) }()
	select {
	case ok := <-returned:
		if !ok {
			t.Fatal("do reported the loop ended before running the callback")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("do stayed parked after its callback exited the loop goroutine")
	}
	// The exit took the loop goroutine with it, so a later request reports it.
	wait(t, "the loop to end", c.done)
	if c.do(func() {}) {
		t.Fatal("do ran a request on a loop that ended")
	}
}

// doT runs fn on the loop and raises the first failure fn reports through
// failf on the calling goroutine: a t.Fatal inside the callback would exit the
// loop goroutine instead of the test (TestDoCallbacksNeverExitTheLoopGoroutine
// keeps the callbacks free of it). A callback that fails returns right after
// failf, since failf does not stop it.
func (c *conv) doT(t testing.TB, fn func(failf func(format string, args ...any))) {
	t.Helper()
	var failure string
	if !c.do(func() {
		fn(func(format string, args ...any) {
			if failure == "" {
				failure = fmt.Sprintf(format, args...)
			}
		})
	}) {
		t.Fatal("conversation loop ended before the callback ran")
	}
	if failure != "" {
		t.Fatal(failure)
	}
}
