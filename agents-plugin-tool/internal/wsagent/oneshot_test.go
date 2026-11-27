package wsagent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wstestutil"
)

func TestOneShotClaudeArgsIsolation(t *testing.T) {
	reg := builtinRegistration(BackendClaude)
	if reg.OneShot == nil {
		t.Fatal("claude registers no one-shot mode")
	}
	args := reg.OneShot.Args("haiku", "low")
	pairs := [][2]string{
		{"--output-format", "text"},
		{"--setting-sources", ""},
		{"--tools", ""},
		{"--permission-mode", "dontAsk"},
		{"--model", "haiku"},
		{"--effort", "low"},
	}
	for _, pair := range pairs {
		i := slices.Index(args, pair[0])
		if i < 0 || i+1 >= len(args) || args[i+1] != pair[1] {
			t.Errorf("args %q lack %s %q", args, pair[0], pair[1])
		}
	}
	for _, flag := range []string{"-p", "--strict-mcp-config", "--no-session-persistence", "--disable-slash-commands"} {
		if !slices.Contains(args, flag) {
			t.Errorf("args %q lack %s", args, flag)
		}
	}
	for _, flag := range []string{"--mcp-config", "--plugin-dir", "--settings", "--input-format"} {
		if slices.Contains(args, flag) {
			t.Errorf("args %q carry %s", args, flag)
		}
	}
	bare := reg.OneShot.Args("", "")
	if slices.Contains(bare, "--model") || slices.Contains(bare, "--effort") {
		t.Errorf("empty model/effort still passed: %q", bare)
	}
}

func TestOneShotRegistrationCloneCopiesSupport(t *testing.T) {
	reg := builtinRegistration(BackendClaude)
	clone := cloneBackendRegistration(reg)
	if clone.OneShot == nil || clone.OneShot == reg.OneShot {
		t.Fatalf("clone shares or drops OneShot: %p vs %p", clone.OneShot, reg.OneShot)
	}
}

func TestOneShotUnsupportedHarnessSpawnsNothing(t *testing.T) {
	for _, harness := range []string{BackendCodex, BackendAgy, BackendPi, "no-such-harness"} {
		if reg := builtinRegistration(harness); reg.OneShot != nil {
			t.Fatalf("%s registers a one-shot mode; update this test with its probe", harness)
		}
		oldLook := lookPath
		lookPath = func(string) (string, error) {
			t.Errorf("%s: one-shot looked up an executable", harness)
			return "", errors.New("unexpected")
		}
		_, err := RunOneShot(context.Background(), harness, "", "", "hi")
		lookPath = oldLook
		if !errors.Is(err, ErrNoOneShot) {
			t.Errorf("%s: err = %v, want ErrNoOneShot", harness, err)
		}
	}
}

// fakeOneShot registers the test binary itself as the harness executable, so
// the child is a real process that runs TestOneShotHelperProcess.
func fakeOneShot(t *testing.T, mode string) BackendRegistration {
	t.Helper()
	t.Setenv("WS_TEST_ONESHOT_MODE", mode)
	t.Setenv("WS_MCP_HARNESS", "must-be-dropped")
	return BackendRegistration{
		Backend: ClaudeBackend{Command: os.Args[0]},
		OneShot: &OneShotSupport{Args: func(model, effort string) []string {
			return []string{"-test.run=^TestOneShotHelperProcess$", "--", model, effort}
		}},
	}
}

func TestOneShotRoundTripNeutralCwd(t *testing.T) {
	reg := fakeOneShot(t, "echo")
	got, err := runOneShot(context.Background(), reg, "fake", "m1", "e1", "hello prompt\n")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("reply = %q", got)
	}
	wantDir := wstestutil.CanonicalPath(os.TempDir())
	gotDir := wstestutil.CanonicalPath(lines[0])
	if gotDir != wantDir {
		t.Errorf("cwd = %q, want %q", gotDir, wantDir)
	}
	if lines[1] != "hello prompt" {
		t.Errorf("stdin echo = %q", lines[1])
	}
	if lines[2] != "args=m1,e1" {
		t.Errorf("args = %q", lines[2])
	}
	if lines[3] != "harness-env=" {
		t.Errorf("ws harness variable inherited: %q", lines[3])
	}
}

func TestOneShotNonZeroExitIsError(t *testing.T) {
	reg := fakeOneShot(t, "fail")
	_, err := runOneShot(context.Background(), reg, "fake", "", "", "x")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want exit error carrying stderr", err)
	}
}

func TestOneShotOutputCapIsError(t *testing.T) {
	reg := fakeOneShot(t, "flood")
	start := time.Now()
	_, err := runOneShot(context.Background(), reg, "fake", "", "", "x")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want overflow error", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("overflow took %v", time.Since(start))
	}
}

func TestOneShotTimeoutKillsProcessTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pid liveness probe is Unix-only")
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("WS_TEST_ONESHOT_PIDFILE", pidFile)
	reg := fakeOneShot(t, "hang")
	// Cancel once the grandchild has written its pid (bounded), so a loaded
	// machine's slow start cannot beat the deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var cancelledAt atomic.Int64
	go func() {
		for start := time.Now(); time.Since(start) < 20*time.Second; time.Sleep(10 * time.Millisecond) {
			if raw, err := os.ReadFile(pidFile); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
				break
			}
		}
		cancelledAt.Store(time.Now().UnixNano())
		cancel()
	}()
	_, err := runOneShot(ctx, reg, "fake", "", "", "x")
	took := time.Since(time.Unix(0, cancelledAt.Load()))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancelled context", err)
	}
	if took > 5*time.Second {
		t.Fatalf("returned %v after the cancel, want prompt return", took)
	}
	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("grandchild never started: %v", rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if pid <= 0 {
		t.Fatalf("bad grandchild pid %q", raw)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		proc, _ := os.FindProcess(pid)
		if proc.Signal(syscall.Signal(0)) != nil || oneShotProcessZombie(pid) {
			return
		}
		if time.Now().After(deadline) {
			_ = proc.Kill()
			t.Fatalf("grandchild %d survived the timeout", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestOneShotHelperProcess is the fake harness executable, not a test.
func TestOneShotHelperProcess(t *testing.T) {
	mode := os.Getenv("WS_TEST_ONESHOT_MODE")
	if mode == "" {
		t.Skip("helper process only")
	}
	switch mode {
	case "echo":
		buf := make([]byte, 4096)
		n, _ := os.Stdin.Read(buf)
		cwd, _ := os.Getwd()
		args := os.Args
		tail := args[slices.Index(args, "--")+1:]
		os.Stdout.WriteString(cwd + "\n" + strings.TrimSpace(string(buf[:n])) + "\nargs=" + strings.Join(tail, ",") + "\nharness-env=" + os.Getenv("WS_MCP_HARNESS") + "\n")
	case "fail":
		os.Stderr.WriteString("boom\n")
		os.Exit(3)
	case "flood":
		chunk := []byte(strings.Repeat("x", 4096))
		for {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(1)
			}
		}
	case "hang":
		child := exec.Command(os.Args[0], "-test.run=^TestOneShotHelperProcess$")
		child.Env = append(os.Environ(), "WS_TEST_ONESHOT_MODE=sleep")
		if err := child.Start(); err == nil {
			os.WriteFile(os.Getenv("WS_TEST_ONESHOT_PIDFILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		}
		time.Sleep(time.Minute)
	case "sleep":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func TestHasOneShotFollowsRegistrations(t *testing.T) {
	for harness, want := range map[string]bool{"claude": true, "pi": false, "codex": false, "agy": false, "nope": false} {
		if got := HasOneShot(harness); got != want {
			t.Errorf("HasOneShot(%q) = %v, want %v", harness, got, want)
		}
	}
}
