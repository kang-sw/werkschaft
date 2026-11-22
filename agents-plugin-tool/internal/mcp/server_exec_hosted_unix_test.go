//go:build !windows

package mcp

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHostedExecStopEndsTheJobsProcessGroup: exec.stop ends the job's
// shell and the children it started in the background, not only the
// direct child.
func TestHostedExecStopEndsTheJobsProcessGroup(t *testing.T) {
	fx := newHostedExecFixture(t, 10*time.Second)
	text, isErr := fx.call("exec.shell", map[string]any{"cmd": "sleep 30 & sleep 30 & wait", "async": true})
	if isErr {
		t.Fatal(text)
	}
	id := lineValue(t, execIDLine, text)
	view, err := fx.hosted.Get("lead", id)
	if err != nil || view.PID == 0 {
		t.Fatalf("job = %+v, %v", view, err)
	}
	var children []int
	waitExec(t, "the shell's two background children", func() bool {
		out, _ := exec.Command("pgrep", "-P", strconv.Itoa(view.PID)).Output()
		children = children[:0]
		for _, f := range strings.Fields(string(out)) {
			if pid, err := strconv.Atoi(f); err == nil {
				children = append(children, pid)
			}
		}
		return len(children) == 2
	})
	if text, isErr := fx.call("exec.stop", map[string]any{"exec_id": id}); isErr || !strings.Contains(text, "status: stopped") {
		t.Fatalf("stop = %v %s", isErr, text)
	}
	for _, pid := range append([]int{view.PID}, children...) {
		waitExec(t, "process "+strconv.Itoa(pid)+" to end", func() bool {
			return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
		})
	}
}

// TestHostedExecDetachedProcessIsOutsideTheGuarantee: a process that leaves
// the job's process group (setsid) is not ended by exec.stop; the test ends
// it itself.
func TestHostedExecDetachedProcessIsOutsideTheGuarantee(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3: %v", err)
	}
	fx := newHostedExecFixture(t, 10*time.Second)
	script := `import os, sys, time
pid = os.fork()
if pid == 0:
    os.setsid()
    print(os.getpid(), flush=True)
    time.sleep(30)
    sys.exit(0)
time.sleep(30)
`
	text, isErr := fx.call("exec.run", map[string]any{"exe": "python3", "args": []string{"-c", script}, "async": true})
	if isErr {
		t.Fatal(text)
	}
	id := lineValue(t, execIDLine, text)
	var detached int
	waitExec(t, "the detached child's pid", func() bool {
		text, _ := fx.call("exec.tail", map[string]any{"exec_id": id})
		for _, line := range strings.Split(text, "\n") {
			if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 1 {
				detached = pid
				return true
			}
		}
		return false
	})
	t.Cleanup(func() { _ = syscall.Kill(detached, syscall.SIGKILL) })
	if text, isErr := fx.call("exec.stop", map[string]any{"exec_id": id}); isErr {
		t.Fatalf("stop = %s", text)
	}
	time.Sleep(300 * time.Millisecond)
	if err := syscall.Kill(detached, 0); err != nil {
		t.Fatalf("the detached process ended with the job (%v): the boundary test expects it outside the group", err)
	}
}
