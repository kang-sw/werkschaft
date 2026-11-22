//go:build !windows

package wsholder

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// grandchildChild is a pipe child, standing in for a hosted harness, that
// starts long-lived descendants the way its shell tool does and prints their
// PIDs: one in its own session (a detached background shell), one in the
// inherited process group (the holder's), one that also inherits the child's
// pipes, and one grandchild's own child (harness -> shell -> long process).
// Each ends after 90 s, so a leaked one does not stay.
const grandchildChild = `
import subprocess, sys
null = subprocess.DEVNULL
own = subprocess.Popen(["sleep", "90"], start_new_session=True, stdin=null, stdout=null, stderr=null)
inh = subprocess.Popen(["sleep", "91"], stdin=null, stdout=null, stderr=null)
pipe = subprocess.Popen(["sleep", "92"])
sh = subprocess.Popen(["sh", "-c", "sleep 93 & echo $! >&2; wait"], stdin=null, stdout=null, stderr=subprocess.PIPE)
deep = int(sh.stderr.readline())
print("pids own=%d inherited=%d pipes=%d shell=%d deep=%d" % (own.pid, inh.pid, pipe.pid, sh.pid, deep), flush=True)
for line in sys.stdin:
    if line.strip() == "quit":
        break
sys.exit(0)
`

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// TestDescendantsOfAHeldChildSurviveAManagerReplace: closing a daemon's
// manager and opening the next one only drops sockets. Neither the child nor
// any descendant of it (own session, inherited group, inherited pipes, a
// shell's child) is signalled, however often the daemon is replaced. A stop
// is the only end of the group, which the last step records.
func TestDescendantsOfAHeldChildSurviveAManagerReplace(t *testing.T) {
	dir := testHoldersDir(t)
	first := newCollector()
	m1 := testManager(t, dir, first)
	id, err := m1.Launch(json.RawMessage(`{"kind":"test","label":"grandchildren"}`))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	child, err := m1.Client(id).Spawn(SpawnSpec{Argv: []string{testPython(t), "-c", grandchildChild}, Mode: "pipe"})
	if err != nil || child.State != "running" {
		t.Fatalf("spawn: %+v, %v", child, err)
	}
	waitFor(t, "the descendants' PIDs", func() bool { return strings.Contains(first.output(id), "deep=") })
	pids := map[string]int{}
	for _, field := range strings.Fields(strings.TrimSpace(first.output(id))) {
		if name, value, ok := strings.Cut(field, "="); ok {
			n, err := strconv.Atoi(value)
			if err != nil {
				t.Fatalf("PID line %q: %v", first.output(id), err)
			}
			pids[name] = n
		}
	}
	if len(pids) != 5 {
		t.Fatalf("descendant PIDs = %v", pids)
	}
	// Only processes this test started are ever signalled, by PID.
	t.Cleanup(func() {
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	alive := func(when string) {
		t.Helper()
		if !pidAlive(child.PID) {
			t.Fatalf("%s: the held child %d is gone", when, child.PID)
		}
		for name, pid := range pids {
			if !pidAlive(pid) {
				t.Fatalf("%s: descendant %s (pid %d) is gone", when, name, pid)
			}
		}
	}
	alive("before any replace")

	for replace := 1; replace <= 2; replace++ {
		m1.Close()
		if replace == 2 {
			m1 = nil
		}
		time.Sleep(300 * time.Millisecond)
		alive(fmt.Sprintf("after the daemon closed (replace %d)", replace))
		next := testManager(t, dir, newCollector())
		next.Scan()
		if list := next.List(); len(list.Holders) != 1 || !list.Holders[0].Attached || list.Holders[0].Child == nil || list.Holders[0].Child.State != "running" {
			t.Fatalf("replace %d: listing = %+v", replace, list)
		}
		time.Sleep(300 * time.Millisecond)
		alive(fmt.Sprintf("after the successor attached (replace %d)", replace))
		if replace == 1 {
			m1 = next
		} else {
			// A stop is the end of the holder's group; recorded, not a
			// replace behavior.
			if err := next.Stop(id); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			waitFor(t, "the stop's group kill to settle", func() bool { return !pidAlive(pids["inherited"]) })
			for name, pid := range pids {
				t.Logf("after a stop: %s (pid %d) alive=%v", name, pid, pidAlive(pid))
			}
		}
	}
}
