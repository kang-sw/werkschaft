package wsdashboard

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestComposerExecBrowser drives the composer's `!` commands in Chromium
// against the real session page and route (only the command service is a
// fake): the private card in its states in both ko and en, the old-daemon
// fail-closed behavior, the attachment refusal that keeps the draft, and the
// card's absence from the shared transcript and from a LAN view.
func TestComposerExecBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	shots := os.Getenv("WS_COMPOSER_EXEC_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_COMPOSER_EXEC_SHOTS must be absolute")
	}
	f := startComposerFixture(t)
	var mu sync.Mutex
	asyncSince := map[string]time.Time{}
	zero := 0
	f.svc.mu.Lock()
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		line := strings.TrimSpace(req.Line)
		switch {
		case strings.HasPrefix(line, "deny"):
			return ComposerJob{}, errors.New("exec: the worktree is being released")
		case strings.HasPrefix(line, "slow"):
			time.Sleep(1500 * time.Millisecond)
			return ComposerJob{ExecID: "exec_slow", Gen: 4, State: "exited", ExitCode: &zero, Command: line, Written: 5, Output: []byte("slow\n")}, nil
		case strings.HasPrefix(line, "async"):
			mu.Lock()
			asyncSince["exec_async"] = time.Now()
			mu.Unlock()
			return ComposerJob{ExecID: "exec_async", Gen: 4, State: "running", Async: true, Command: line}, nil
		case strings.HasPrefix(line, "big"):
			out := []byte(strings.Repeat("0123456789abcdef\n", 400))
			return ComposerJob{ExecID: "exec_big", Gen: 4, State: "exited", ExitCode: &zero, Command: line, Written: int64(len(out)), Output: out}, nil
		}
		return ComposerJob{ExecID: "exec_hi", Gen: 4, State: "exited", ExitCode: &zero, Command: line, Written: 3, Output: []byte("hi\n")}, nil
	}
	f.svc.mu.Unlock()
	jobs := &composerJobClock{svc: f.svc, since: func() time.Time { mu.Lock(); defer mu.Unlock(); return asyncSince["exec_async"] }}
	f.d.SetComposerExec(jobs)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	script := "testdata/composer-exec.browser.cjs"
	if d := os.Getenv("WS_COMPOSER_EXEC_SCRIPT"); d != "" {
		script = d
	}
	out, err := exec.CommandContext(ctx, "node", script, f.base, shots).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium composer exec acceptance: %v", err)
	}
	if n := f.svc.launchCount(); n < 5 {
		t.Fatalf("launches = %d", n)
	}
	hands := f.svc.handList()
	if len(hands) == 0 {
		t.Fatal("no receipt reached the lead")
	}
}

// composerJobClock answers Job for the async command: running for 2 s after
// its launch, then exited with output.
type composerJobClock struct {
	svc   *composerFake
	since func() time.Time
}

func (c *composerJobClock) Launch(ctx context.Context, drain <-chan struct{}, req ComposerLaunch) (ComposerJob, error) {
	return c.svc.Launch(ctx, drain, req)
}

func (c *composerJobClock) Job(reply, id string) (ComposerJob, error) {
	if id != "exec_async" {
		return ComposerJob{}, errors.New("exec job not found")
	}
	zero := 0
	if time.Since(c.since()) < 2*time.Second {
		return ComposerJob{ExecID: id, Gen: 4, State: "running", Async: true}, nil
	}
	return ComposerJob{ExecID: id, Gen: 4, State: "exited", ExitCode: &zero, Async: true, Written: 6, Output: []byte("later\n")}, nil
}

func (c *composerJobClock) Hand(reply string, gen uint64, execID, text string) error {
	return c.svc.Hand(reply, gen, execID, text)
}
