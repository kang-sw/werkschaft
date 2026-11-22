//go:build darwin

package wsawake

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fixtureHelper writes a stand-in for caffeinate that records its arguments
// and pid, then waits; the real helper is never run by tests.
func fixtureHelper(t *testing.T) (path, argsFile, pidFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile, pidFile = filepath.Join(dir, "args"), filepath.Join(dir, "pid")
	path = filepath.Join(dir, "caffeinate")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\necho $$ > " + pidFile + "\nexec sleep 60\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, argsFile, pidFile
}

func readWhenWritten(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(raw), "\n") {
			return strings.TrimSpace(string(raw))
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never written", path)
	return ""
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func TestCaffeinateStartsAndReapsOneHelper(t *testing.T) {
	path, argsFile, pidFile := fixtureHelper(t)
	b := &caffeinate{path: path, pid: 4242}
	if err := b.Acquire(); err != nil {
		t.Fatal(err)
	}
	if got := readWhenWritten(t, argsFile); got != "-i -w 4242" {
		t.Fatalf("helper args = %q, want -i -w 4242", got)
	}
	first, _ := strconv.Atoi(readWhenWritten(t, pidFile))
	// A second Acquire reaps the first helper before it starts another.
	os.Remove(pidFile)
	if err := b.Acquire(); err != nil {
		t.Fatal(err)
	}
	second, _ := strconv.Atoi(readWhenWritten(t, pidFile))
	if first == second || alive(first) {
		t.Fatalf("first helper %d still runs beside %d", first, second)
	}
	if err := b.Release(); err != nil {
		t.Fatal(err)
	}
	if alive(second) {
		t.Fatalf("helper %d survived Release", second)
	}
	if err := b.Release(); err != nil {
		t.Fatalf("repeated Release: %v", err)
	}
}

func TestCaffeinateReleaseAfterHelperLost(t *testing.T) {
	path, _, pidFile := fixtureHelper(t)
	b := &caffeinate{path: path, pid: os.Getpid()}
	if err := b.Acquire(); err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(readWhenWritten(t, pidFile))
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := b.Release(); err != nil {
		t.Fatalf("Release after a lost helper: %v", err)
	}
}

func TestCaffeinateStartFailure(t *testing.T) {
	b := &caffeinate{path: filepath.Join(t.TempDir(), "missing"), pid: 1}
	if err := b.Acquire(); err == nil {
		t.Fatal("Acquire of a missing helper succeeded")
	}
	if err := b.Release(); err != nil {
		t.Fatalf("Release after a failed Acquire: %v", err)
	}
}

func TestPlatformBackendIsCaffeinate(t *testing.T) {
	b, ok := Platform(nil).(*caffeinate)
	if !ok || b.path != "/usr/bin/caffeinate" || b.pid != os.Getpid() || b.cmd != nil {
		t.Fatalf("Platform() = %#v", Platform(nil))
	}
}
