package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
)

// releasedCandidateForTest acquires a pooled worktree and releases it, so it
// is a clean, detached, unleased reuse candidate, and returns its path.
func releasedCandidateForTest(t *testing.T, s *Server, id int, lead, base, branch string) string {
	t.Helper()
	acq := acquireForTest(t, s, id, lead, base, branch)
	if rel := callToolOnce(t, s, id+1, "worktree.release", map[string]any{"session_key": lead, "path": acq.Path}); toolIsError(t, rel) {
		t.Fatalf("release failed: %s", rel)
	}
	return acq.Path
}

// launchLegacyForTest starts a file-backed (legacy) exec job rooted at root
// and returns a stop that aborts it and waits until the legacy fence no
// longer counts it.
func launchLegacyForTest(t *testing.T, root string) func() {
	t.Helper()
	resp, err := execjob.Launch(execjob.LaunchOptions{Root: root, Cmd: "sleep", Args: []string{"30"}})
	if err != nil {
		t.Fatalf("legacy launch in %s: %v", root, err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_, _ = execjob.Abort(root, resp.ExecKey)
		pollUntil(t, "legacy job to end", func() bool {
			unfence, err := execjob.FenceLegacyRoot(root)
			if err != nil {
				return false
			}
			unfence()
			return true
		})
	}
	t.Cleanup(stop)
	return stop
}

// TestWorktreeAcquireSkipsCheckoutWithLegacyExecJob: a live legacy job rooted
// in a released pooled checkout keeps acquisition from reusing it; once the
// job ends, the checkout is reused.
func TestWorktreeAcquireSkipsCheckoutWithLegacyExecJob(t *testing.T) {
	s, base, leadA, _ := releaseGuardServer(t)
	fx := newHostedExecFixture(t, 5*time.Second)
	s.exec, s.conversation = func() *execjob.Hosted { return fx.hosted }, "lead"

	candidate := releasedCandidateForTest(t, s, 1, leadA, base, "impl/test/legacy-a")
	stop := launchLegacyForTest(t, candidate)
	if got := acquireForTest(t, s, 3, leadA, base, "impl/test/legacy-b"); got.Path == candidate {
		t.Fatalf("acquire reused %s while a legacy exec job is rooted there", candidate)
	}
	stop()
	if got := acquireForTest(t, s, 4, leadA, base, "impl/test/legacy-c"); got.Path != candidate {
		t.Fatalf("acquire did not reuse %s after its legacy job ended; got %s", candidate, got.Path)
	}
}

// TestWorktreeAcquireSkipsCheckoutWithHostedJobRootedThere: a hosted job
// whose root is the candidate but whose working directory lies outside it
// (reached through a symlink) keeps acquisition from reusing the candidate.
func TestWorktreeAcquireSkipsCheckoutWithHostedJobRootedThere(t *testing.T) {
	s, base, leadA, _ := releaseGuardServer(t)
	fx := newHostedExecFixture(t, 5*time.Second)
	s.exec, s.conversation = func() *execjob.Hosted { return fx.hosted }, "lead"

	candidate := releasedCandidateForTest(t, s, 1, leadA, base, "impl/test/root-a")
	outside := t.TempDir()
	link := outside + "-link"
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	v, err := fx.hosted.Launch(context.Background(), make(chan struct{}), execjob.HostedLaunch{
		Owner: "lead", Tool: "exec.run", Root: candidate, Cwd: link, Argv: []string{"sleep", "30"},
		Display: "sleep 30", Environ: os.Environ(), Async: true,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if got := acquireForTest(t, s, 3, leadA, base, "impl/test/root-b"); got.Path == candidate {
		t.Fatalf("acquire reused %s while a hosted job rooted there runs (cwd %s)", candidate, link)
	}
	if _, err := fx.hosted.Stop("lead", v.ID); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, "hosted job to end", func() bool {
		got, err := fx.hosted.Get("lead", v.ID)
		return err == nil && got.Terminal()
	})
	if got := acquireForTest(t, s, 4, leadA, base, "impl/test/root-c"); got.Path != candidate {
		t.Fatalf("acquire did not reuse %s after its hosted job ended; got %s", candidate, got.Path)
	}
}

// TestWorktreeAcquireFencesExecLaunchesUntilLeasePublished: once acquisition
// claims a candidate, a hosted and a legacy launch into it are refused,
// both right after the claim (before checkout) and after checkout (at the
// mint, just before the lease is written); after the acquire returns, with
// the lease published and the fence lifted, both launch.
func TestWorktreeAcquireFencesExecLaunchesUntilLeasePublished(t *testing.T) {
	s, root, base, leadA := worktreeListServer(t)
	fx := newHostedExecFixture(t, 5*time.Second)
	s.exec, s.conversation = func() *execjob.Hosted { return fx.hosted }, "lead"

	candidate := releasedCandidateForTest(t, s, 1, leadA, base, "impl/test/race-a")
	tryLaunches := func(when, path string) (hostedErr, legacyErr error) {
		t.Helper()
		v, err := fx.hosted.Launch(context.Background(), make(chan struct{}), execjob.HostedLaunch{
			Owner: "lead", Tool: "exec.run", Root: path, Cwd: path, Argv: []string{"true"},
			Display: "true", Environ: os.Environ(),
		})
		if err == nil {
			_, _ = fx.hosted.Stop("lead", v.ID)
		}
		hostedErr = err
		resp, err := execjob.Launch(execjob.LaunchOptions{Root: path, Cmd: "true"})
		if err == nil {
			_, _ = execjob.ResultWithTimeout(path, resp.ExecKey, 10*time.Second)
		}
		legacyErr = err
		return
	}
	assertRefused := func(when, path string) {
		t.Helper()
		hostedErr, legacyErr := tryLaunches(when, path)
		for name, err := range map[string]error{"hosted": hostedErr, "legacy": legacyErr} {
			if err == nil || !strings.Contains(err.Error(), "being acquired") {
				t.Errorf("%s: a %s launch into the claimed candidate = %v, want the fence's refusal", when, name, err)
			}
		}
	}

	claimed := 0
	afterWorktreeClaim = func(path string) {
		claimed++
		assertRefused("after the claim", path)
	}
	t.Cleanup(func() { afterWorktreeClaim = nil })
	res, err := acquireWorktreeWith(context.Background(), s.gitRunner(), s.environ(), root, base, "impl/test/race-b", builtinProfile(t, worktreeProfileFull), "", leadA, func(path string) (string, error) {
		assertRefused("at the mint", candidate)
		return s.sessions.mint(path, roleLead, leadA)
	}, s.worktreeBusy, s.worktreeAcquireExecFence)
	afterWorktreeClaim = nil
	if err != nil {
		t.Fatal(err)
	}
	if claimed != 1 || !res.Reused {
		t.Fatalf("acquire did not claim the released candidate (claimed %d, reused %v)", claimed, res.Reused)
	}
	lease, err := readWorktreeLease(context.Background(), s.gitRunner(), candidate)
	if err != nil || lease == nil || lease.WorkerKey != res.WorkerKey {
		t.Fatalf("lease not published after the acquire: %+v err=%v", lease, err)
	}
	hostedErr, legacyErr := tryLaunches("after the acquire", candidate)
	if hostedErr != nil || legacyErr != nil {
		t.Fatalf("launches after the acquire returned: hosted %v, legacy %v", hostedErr, legacyErr)
	}
}
