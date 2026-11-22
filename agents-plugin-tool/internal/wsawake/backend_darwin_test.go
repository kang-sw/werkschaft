//go:build darwin

package wsawake

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	log := &syncBuffer{}
	b := &caffeinate{path: path, pid: 4242, log: log}
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
	if got := log.String(); got != "" {
		t.Fatalf("helpers Release ended were logged as unexpected: %q", got)
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

// syncBuffer is a log the helper's reaper writes from its own goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func waitLogged(t *testing.T, log *syncBuffer, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := log.String(); strings.Contains(got, want) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("log never showed %q: %q", want, log.String())
	return ""
}

// writeHelper writes a stand-in helper script.
func writeHelper(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "caffeinate")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A helper that exits on its own is reaped and its exit logged once, with
// its status and the start of its stderr; the assertion then counts as lost.
func TestCaffeinateLogsUnexpectedExit(t *testing.T) {
	path := writeHelper(t, "echo 'helper refused' >&2\nexit 3\n")
	log := &syncBuffer{}
	b := &caffeinate{path: path, pid: os.Getpid(), log: log}
	if b.Lost() {
		t.Fatal("no helper yet, but Lost")
	}
	if err := b.Acquire(); err != nil {
		t.Fatal(err)
	}
	got := waitLogged(t, log, "exited unexpectedly")
	if !strings.Contains(got, "exit status 3") || !strings.Contains(got, "stderr: helper refused") {
		t.Fatalf("exit log lacks the status or stderr: %q", got)
	}
	select {
	case <-b.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the exited helper was never reaped")
	}
	if !b.Lost() {
		t.Fatal("an exited helper is not reported lost")
	}
	if err := b.Release(); err != nil {
		t.Fatalf("Release after the exit: %v", err)
	}
	if b.Lost() {
		t.Fatal("Lost after Release")
	}
	if n := strings.Count(log.String(), "exited unexpectedly"); n != 1 {
		t.Fatalf("exit logged %d times: %q", n, log.String())
	}
}

// The kept stderr is bounded, whatever the helper writes.
func TestCaffeinateExitLogIsBounded(t *testing.T) {
	path := writeHelper(t, "i=0\nwhile [ $i -lt 400 ]; do echo 0123456789abcdef >&2; i=$((i+1)); done\nexit 1\n")
	log := &syncBuffer{}
	b := &caffeinate{path: path, pid: os.Getpid(), log: log}
	if err := b.Acquire(); err != nil {
		t.Fatal(err)
	}
	got := waitLogged(t, log, "exited unexpectedly")
	if len(got) > helperStderrLimit+512 {
		t.Fatalf("exit log is %d bytes, beyond the %d-byte stderr bound", len(got), helperStderrLimit)
	}
	if err := b.Release(); err != nil {
		t.Fatal(err)
	}
}

// Under Core, a helper found dead at the next membership change while the
// assertion is still wanted is restarted, and Close ends the new one.
func TestCaffeinateRestartedAfterUnexpectedExit(t *testing.T) {
	dir := t.TempDir()
	marker, pidFile := filepath.Join(dir, "started"), filepath.Join(dir, "pid")
	// The first run exits at once; later runs record their pid and wait.
	path := writeHelper(t, "if [ ! -e "+marker+" ]; then : > "+marker+"; exit 2; fi\necho $$ > "+pidFile+"\nexec sleep 60\n")
	log := &syncBuffer{}
	c := New(&caffeinate{path: path, pid: os.Getpid(), log: log}, log)
	c.SetEnabled(true)
	c.SetMember(new(int), true)
	// The exit is logged only once the helper was reaped, so the next
	// change finds it dead.
	waitLogged(t, log, "exit status 2")
	c.SetMember(new(int), true)
	pid, _ := strconv.Atoi(readWhenWritten(t, pidFile))
	if !alive(pid) {
		t.Fatalf("restarted helper %d is not running", pid)
	}
	if !strings.Contains(log.String(), "taking it again") {
		t.Fatalf("the restart was not logged: %q", log.String())
	}
	c.Close()
	if alive(pid) {
		t.Fatalf("helper %d survived Close", pid)
	}
	if n := strings.Count(log.String(), "exited unexpectedly"); n != 1 {
		t.Fatalf("Close's kill was logged as unexpected: %q", log.String())
	}
}

func TestBoundedBufferKeepsTheStart(t *testing.T) {
	w := &boundedBuffer{limit: 5}
	for _, p := range []string{"abc", "defg", "hij"} {
		if n, err := w.Write([]byte(p)); n != len(p) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", p, n, err)
		}
	}
	if got := w.String(); got != "abcde" {
		t.Fatalf("kept %q, want abcde", got)
	}
}
