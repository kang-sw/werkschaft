package mcp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsgit"
)

// worktree.release and worktree.remove reserve their target against exec
// jobs under the per-worktree lock: a live job refuses them, force or not,
// leaving the worktree as it was; otherwise the reservation holds new jobs
// off until the operation is over.
func TestWorktreeReleaseAndRemoveRefuseWhileAnExecJobRuns(t *testing.T) {
	root := profileWorktreeFixture(t)
	ctx := context.Background()
	runner := wsgit.ExecRunner{}
	profile := hookProfile(t, "build", nil, nil)
	lookup := lookupOf(profile)
	live := func(string) (func(), error) {
		return nil, errors.New("1 exec job(s) still run in it; stop them with exec.stop (exec.list shows them), or let them finish, first")
	}

	wt, err := provisionWorktree(ctx, runner, nil, root, "develop", "impl/develop/busy", profile, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		_, err := releaseWorktreeAs(ctx, runner, nil, wt.Path, "", lookup, &worktreeReleaseCaller{Force: force, FenceExec: live})
		if err == nil || !strings.Contains(err.Error(), "worktree.release refuses") || !strings.Contains(err.Error(), "exec job(s) still run") {
			t.Fatalf("release (force=%v) with a live job = %v", force, err)
		}
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("a refused release changed the worktree: %v", err)
	}
	if _, err := removeWorktreeFenced(ctx, runner, nil, wt.Path, "", testStaleAfter, time.Now(), lookup, live, nil, ""); err == nil || !strings.Contains(err.Error(), "worktree.remove refuses") {
		t.Fatalf("remove with a live job = %v", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("a refused remove deleted the worktree: %v", err)
	}

	// With no job the reservation is taken for the operation and lifted
	// after it.
	var held, lifted int
	fence := func(target string) (func(), error) {
		held++
		return func() { lifted++ }, nil
	}
	if _, err := releaseWorktreeAs(ctx, runner, nil, wt.Path, "", lookup, &worktreeReleaseCaller{FenceExec: fence}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if held != 1 || lifted != 1 {
		t.Fatalf("release reservation held %d lifted %d", held, lifted)
	}
	if _, err := removeWorktreeFenced(ctx, runner, nil, wt.Path, "", testStaleAfter, time.Now(), lookup, fence, nil, ""); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if held != 2 || lifted != 2 {
		t.Fatalf("remove reservation held %d lifted %d", held, lifted)
	}
}

// Over the daemon's hosted jobs: a running job in the worktree refuses
// the reservation; after exec.stop it is granted and holds a new launch
// there off until lifted.
func TestWorktreeExecFenceOverHostedJobs(t *testing.T) {
	fx := newHostedExecFixture(t, 5*time.Second)
	text, isErr := fx.call("exec.run", map[string]any{"exe": "sleep", "args": []string{"30"}, "async": true})
	if isErr {
		t.Fatalf("launch: %s", text)
	}
	id := execIDLine.FindStringSubmatch(text)[1]
	if _, err := fx.s.worktreeExecFence(fx.root); err == nil || !strings.Contains(err.Error(), "1 exec job(s) still run") {
		t.Fatalf("fence with a live job: %v", err)
	}
	if text, isErr := fx.call("exec.stop", map[string]any{"exec_id": id}); isErr {
		t.Fatalf("stop: %s", text)
	}
	unfence, err := fx.s.worktreeExecFence(fx.root)
	if err != nil {
		t.Fatalf("fence after the stop: %v", err)
	}
	if text, isErr := fx.call("exec.run", map[string]any{"exe": "true"}); !isErr || !strings.Contains(text, "being released or removed") {
		t.Fatalf("launch into the reserved worktree = %v %s", isErr, text)
	}
	unfence()
	if text, isErr := fx.call("exec.run", map[string]any{"exe": "true"}); isErr {
		t.Fatalf("launch after the reservation: %s", text)
	}
}
