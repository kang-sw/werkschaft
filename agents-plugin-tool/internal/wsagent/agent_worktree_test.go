// agent_worktree_test.go holds concurrent spawn and worktree child tests.
package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kang-sw/devenv/internal/wsgit"
)

func TestAgentSpawnWidthCapAtomicUnderConcurrentSpawns(t *testing.T) {
	h, _ := agentHarness(t, nil)
	lead := h.leadCaller()
	const remaining = 5
	for i := range MaxSpawnWidth - remaining {
		h.agentSpawn(lead, AgentSpawnRequest{Prompt: fmt.Sprintf("pre %d", i), Tier: "small", Authority: "read-only"})
	}
	const contenders = 20
	start := make(chan struct{})
	errs := make(chan error, contenders)
	var wg sync.WaitGroup
	for i := range contenders {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			_, err := h.runner.AgentSpawn(lead, AgentSpawnRequest{Prompt: fmt.Sprintf("race %d", n), Tier: "small", Authority: "read-only"})
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	succeeded, refused := 0, 0
	for err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		agentRefused(t, err, http.StatusForbidden, "the spawn width cap is 99 awake direct children")
		refused++
	}
	if succeeded != remaining || refused != contenders-remaining {
		t.Fatalf("concurrent spawn results: succeeded=%d refused=%d, want %d and %d", succeeded, refused, remaining, contenders-remaining)
	}
	if n := len(h.runner.AgentList(lead)); n != MaxSpawnWidth {
		t.Fatalf("AgentList count = %d, want %d", n, MaxSpawnWidth)
	}
	if n := h.runner.awakeChildren(leadParent); n != MaxSpawnWidth {
		t.Fatalf("awakeChildren = %d, want %d", n, MaxSpawnWidth)
	}
	h.runner.mu.Lock()
	pending := len(h.runner.pendingSpawns)
	h.runner.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pendingSpawns leaked %d entries", pending)
	}
}

// releaseTestWorktree makes root a Git repository and adds a linked worktree
// outside it, returning the worktree's path.
func releaseTestWorktree(t *testing.T, root string) string {
	t.Helper()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(root, "init")
	git(root, "config", "user.email", "test@example.com")
	git(root, "config", "user.name", "Test")
	git(root, "commit", "--allow-empty", "-m", "base")
	wt := filepath.Join(t.TempDir(), "pooled-wt")
	git(root, "worktree", "add", "-b", "impl/test/bound", wt, "HEAD")
	return wt
}

// TestWorktreeReleaseRefusesSpawnsThatResolveIntoIt verifies that a spawn
// whose cwd release would count as bound (canonical cwd anywhere within the
// worktree) is refused while release holds the lock, when the cwd reaches
// the worktree through a symlink from outside or lies in a nested repo.
func TestWorktreeReleaseRefusesSpawnsThatResolveIntoIt(t *testing.T) {
	h, _ := agentHarness(t, nil)
	wt := releaseTestWorktree(t, h.root)
	sub := filepath.Join(wt, "sub")
	nested := filepath.Join(wt, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(nested, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = nested
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %v\n%s", err, out)
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}

	releaseLock, err := wsgit.WorktreeReleaseLock(context.Background(), wsgit.ExecRunner{}, wt)
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseLock.Lock(); err != nil {
		t.Fatal(err)
	}
	lead := h.leadCaller()
	for _, cwd := range []string{link, nested, filepath.Join(nested, "deep")} {
		_, err := h.runner.AgentSpawn(lead, AgentSpawnRequest{
			Prompt: "raced spawn", Tier: "small", Authority: "read-only", Cwd: cwd,
		})
		agentRefused(t, err, http.StatusConflict, "currently being released")
	}
	if got := h.runner.ActiveChildrenInWorktree(wt); len(got) != 0 {
		t.Fatalf("refused spawns left bound children: %+v", got)
	}
	if err := releaseLock.Unlock(); err != nil {
		t.Fatal(err)
	}
	// Without a release, the same spawns open and count as bound.
	for _, cwd := range []string{link, nested} {
		h.agentSpawn(lead, AgentSpawnRequest{Prompt: "bound", Tier: "small", Authority: "read-only", Cwd: cwd})
	}
	if got := h.runner.ActiveChildrenInWorktree(wt); len(got) != 2 {
		t.Fatalf("bound children = %+v, want 2", got)
	}
}

// TestWorktreeReleaseCountsAChildRootedInANestedRepo verifies that a child
// spawned into a nested repo of a worktree (root and cwd both the nested
// repo, so State carries no cwd) is bound to the outer worktree after its
// spawn lets the reservation go.
func TestWorktreeReleaseCountsAChildRootedInANestedRepo(t *testing.T) {
	h, _ := agentHarness(t, nil)
	wt := releaseTestWorktree(t, h.root)
	nested := filepath.Join(wt, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = nested
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %v\n%s", err, out)
	}
	id := h.agentSpawn(h.leadCaller(), AgentSpawnRequest{Prompt: "nested root", Tier: "small", Authority: "read-only", Worktree: nested})
	for _, target := range []string{wt, nested} {
		if got := h.runner.ActiveChildrenInWorktree(target); len(got) != 1 || got[0].Address != AgentAddress(id) {
			t.Fatalf("bound to %s = %+v, want the nested-rooted child", target, got)
		}
	}
}

// TestWorktreeReservationFailureRefusesTheCreate verifies that a create whose
// release reservation cannot be taken is refused rather than admitted
// unreserved, since a release may already hold the lock it failed to reach.
func TestWorktreeReservationFailureRefusesTheCreate(t *testing.T) {
	h, _ := agentHarness(t, nil)
	broken := filepath.Join(t.TempDir(), "broken-wt")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	// A gitfile whose admin dir cannot be resolved stands in for any lock
	// resolution, open, or acquire error.
	missing := filepath.Join(t.TempDir(), "gone", "worktrees", "broken-wt")
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: "+missing+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := h.runner.AgentSpawn(h.leadCaller(), AgentSpawnRequest{
		Prompt: "unreservable spawn", Tier: "small", Authority: "read-only", Cwd: broken,
	})
	agentRefused(t, err, http.StatusConflict, "could not take the release reservation", "broken-wt", "try again")
	if got := h.runner.ActiveChildrenInWorktree(broken); len(got) != 0 {
		t.Fatalf("refused create left bound children: %+v", got)
	}
}

// TestWorktreeSpawnReservationSpansRegistrationOnly verifies the
// create-before-release interleave: release cannot take the lock while a
// spawn writes its record before registering, and once the child is
// registered (release counts it) the spawn's first send no longer holds the
// lock, so it cannot use up release's wait.
func TestWorktreeSpawnReservationSpansRegistrationOnly(t *testing.T) {
	var (
		mu       sync.Mutex
		wt       string
		h        *harness
		failures []string
	)
	fail := func(format string, args ...any) {
		mu.Lock()
		failures = append(failures, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	// releaseFree reports whether release could take wt's lock right now.
	releaseFree := func() bool {
		lock, err := wsgit.WorktreeReleaseLock(context.Background(), wsgit.ExecRunner{}, wt)
		if err != nil {
			fail("release lock: %v", err)
			return false
		}
		locked, err := lock.TryLock()
		if err != nil {
			fail("release TryLock: %v", err)
		}
		if locked {
			_ = lock.Unlock()
		}
		return locked
	}
	sends := 0
	h, _ = agentHarness(t, func(o *Options) {
		o.sendAdmitting = func(id string) {
			mu.Lock()
			sends++
			mu.Unlock()
			if len(h.runner.ActiveChildrenInWorktree(wt)) == 0 {
				fail("first send of %s before release counts the child", id)
			}
			if !releaseFree() {
				fail("first send of %s still holds the release reservation", id)
			}
		}
	})
	wt = releaseTestWorktree(t, h.root)
	creates := 0
	observe := func(sessionDir string, rec Record) {
		target := canonicalWorktreePath(wt)
		bound := worktreePathContains(target, canonicalWorktreePath(rec.Root)) || worktreePathContains(target, canonicalWorktreePath(rec.workDir()))
		if rec.Parent == nil || !bound || h.runner.conv(rec.ReplyID) != nil {
			return
		}
		mu.Lock()
		creates++
		mu.Unlock()
		if releaseFree() {
			fail("create of %s wrote its record unreserved before registering", rec.ReplyID)
		}
	}
	recordWritten.Store(&observe)
	t.Cleanup(func() { recordWritten.Store(nil) })

	lead := h.leadCaller()
	h.agentSpawn(lead, AgentSpawnRequest{Prompt: "explicit", Tier: "small", Authority: "read-only", Worktree: wt})
	h.agentSpawn(lead, AgentSpawnRequest{Prompt: "by cwd", Tier: "small", Authority: "read-only", Cwd: wt})
	recordWritten.Store(nil)
	mu.Lock()
	defer mu.Unlock()
	if len(failures) > 0 {
		t.Fatal(strings.Join(failures, "\n"))
	}
	if creates < 2 || sends < 2 {
		t.Fatalf("observed %d pre-registration writes and %d first sends, want 2 each", creates, sends)
	}
}

// TestWorktreeActiveChildrenAndSpawnReleaseReservation verifies daemon-wide
// bound-child discovery (direct, nested grandchild, sleeping/waiting non-closed
// children, and closed exclusion), the /agents/by-token/worktree.children
// token route, and mutual exclusion between AgentSpawn(worktree: ...) and the
// per-worktree release lock.
func TestWorktreeActiveChildrenAndSpawnReleaseReservation(t *testing.T) {
	h, _ := agentHarness(t, nil)
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "base"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = h.root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	wt := filepath.Join(t.TempDir(), "pooled-wt")
	cmd := exec.Command("git", "worktree", "add", "-b", "impl/test/bound", wt, "HEAD")
	cmd.Dir = h.root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	lead := h.leadCaller()
	// While a release holds the exclusive per-worktree lock, AgentSpawn into
	// that worktree must be refused without registering a child.
	releaseLock, err := wsgit.WorktreeReleaseLock(context.Background(), wsgit.ExecRunner{}, wt)
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseLock.Lock(); err != nil {
		t.Fatal(err)
	}
	_, err = h.runner.AgentSpawn(lead, AgentSpawnRequest{
		Prompt: "raced spawn", Tier: "small", Authority: "workdir-write", Worktree: wt,
	})
	agentRefused(t, err, http.StatusConflict, "currently being released")
	// A cwd spawn into the worktree or one of its subdirectories is the same
	// bound child to worktree.release, so it is refused the same way.
	if err := os.MkdirAll(filepath.Join(wt, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{wt, filepath.Join(wt, "sub")} {
		_, err = h.runner.AgentSpawn(lead, AgentSpawnRequest{
			Prompt: "raced cwd spawn", Tier: "small", Authority: "workdir-write", Cwd: cwd,
		})
		agentRefused(t, err, http.StatusConflict, "currently being released")
	}
	if got := h.runner.ActiveChildrenInWorktree(wt); len(got) != 0 {
		t.Fatalf("refused spawn left bound children: %+v", got)
	}
	if err := releaseLock.Unlock(); err != nil {
		t.Fatal(err)
	}

	// Direct child spawned into wt and nested grandchild inheriting wt.
	directID, directProc := h.agentRunning(lead, AgentSpawnRequest{
		Prompt: "direct work", Tier: "medium", Authority: "workdir-write", Worktree: wt,
	})
	directSpec := h.backend.start(0)
	directToken := directSpec.WSMCP.Env[EnvPiToken]
	if directToken == "" {
		t.Fatal("direct child did not receive capability token")
	}
	directRec, ok := h.runner.recordOf(directID)
	if !ok {
		t.Fatal("missing direct child record")
	}
	grandID, grandProc := h.agentRunning(h.runner.holderCaller(directRec), AgentSpawnRequest{
		Prompt: "nested work", Tier: "small", Authority: "read-only",
	})

	// A child bound only through its inherited root (cwd outside the
	// worktree), or inheriting the holder's worktree as cwd, is refused the
	// same way while a release holds the lock.
	if err := releaseLock.Lock(); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{t.TempDir(), ""} {
		_, err = h.runner.AgentSpawn(h.runner.holderCaller(directRec), AgentSpawnRequest{
			Prompt: "raced inherited spawn", Tier: "small", Authority: "read-only", Cwd: cwd,
		})
		agentRefused(t, err, http.StatusConflict, "currently being released")
	}
	if err := releaseLock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if got := h.runner.ActiveChildrenInWorktree(wt); len(got) != 2 {
		t.Fatalf("refused inherited spawns changed bound children: %+v", got)
	}

	// Daemon-wide query sees both direct and nested children (and ignores an
	// unrelated child running on h.root).
	otherID, otherProc := h.agentRunning(lead, AgentSpawnRequest{
		Prompt: "other work", Tier: "small", Authority: "read-only",
	})
	defer otherProc.reply("done")

	bound := h.runner.ActiveChildrenInWorktree(wt)
	if len(bound) != 2 {
		t.Fatalf("ActiveChildrenInWorktree = %+v, want 2 children", bound)
	}
	addrs := []string{bound[0].Address, bound[1].Address}
	if !slices.Contains(addrs, AgentAddress(directID)) || !slices.Contains(addrs, AgentAddress(grandID)) || slices.Contains(addrs, AgentAddress(otherID)) {
		t.Fatalf("unexpected bound addresses: %v (want %s and %s, not %s)", addrs, AgentAddress(directID), AgentAddress(grandID), AgentAddress(otherID))
	}

	// Token route exposes the same daemon-wide query to a token-bearing worker.
	body, _ := json.Marshal(map[string]string{"worktree": wt})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:12345"+RoutePrefix+"/"+tokenRoute+"/worktree.children", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+directToken)
	rec := httptest.NewRecorder()
	h.runner.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token route status = %d: %s", rec.Code, rec.Body.String())
	}
	var tokenAnswer struct {
		Children []BoundWorktreeChild `json:"children"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tokenAnswer); err != nil {
		t.Fatalf("decode token answer: %v", err)
	}
	if len(tokenAnswer.Children) != 2 {
		t.Fatalf("token route children = %+v, want 2", tokenAnswer.Children)
	}

	// Settled/sleeping (waiting) children are still non-closed and still block.
	// Direct child holds until grandchild reports, then receives grandchild's
	// report as its next turn.
	directProc.reply("waiting on nested")
	grandProc.reply("nested done")
	eventually(t, "grandchild settled", func() bool { return h.state(grandID).Status == StatusIdle })
	directProc.nextSent(t)
	directProc.reply("all done")
	eventually(t, "direct child settled", func() bool { return h.state(directID).Status == StatusIdle })
	if _, err := h.runner.Stop(grandID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "grandchild waiting", func() bool { return AgentStatus(h.state(grandID)) == AgentWaiting })
	if got := h.runner.ActiveChildrenInWorktree(wt); len(got) != 2 {
		t.Fatalf("waiting non-closed grandchild dropped from bound children: %+v", got)
	}

	// Closing the grandchild leaves only the direct child; closing both clears it.
	if _, err := h.runner.CloseConversation(grandID); err != nil {
		t.Fatal(err)
	}
	if got := h.runner.ActiveChildrenInWorktree(wt); len(got) != 1 || got[0].Address != AgentAddress(directID) {
		t.Fatalf("after closing grandchild = %+v, want only %s", got, AgentAddress(directID))
	}
	if _, err := h.runner.CloseConversation(directID); err != nil {
		t.Fatal(err)
	}
	if got := h.runner.ActiveChildrenInWorktree(wt); len(got) != 0 {
		t.Fatalf("after closing both children = %+v, want empty", got)
	}

	// Shared spawn reservations allow concurrent spawns while blocking an
	// exclusive release lock until all in-flight spawns release it.
	u1, err := wsgit.ReserveWorktreeSpawn(wt)
	if err != nil {
		t.Fatalf("first spawn reservation: %v", err)
	}
	u2, err := wsgit.ReserveWorktreeSpawn(wt)
	if err != nil {
		u1()
		t.Fatalf("second concurrent spawn reservation: %v", err)
	}
	if locked, err := releaseLock.TryLock(); err != nil || locked {
		if locked {
			_ = releaseLock.Unlock()
		}
		u2()
		u1()
		t.Fatalf("exclusive release lock succeeded while spawn reservations held (locked=%v err=%v)", locked, err)
	}
	u1()
	u2()
	if locked, err := releaseLock.TryLock(); err != nil || !locked {
		t.Fatalf("exclusive release lock failed after spawn reservations released (locked=%v err=%v)", locked, err)
	}
	_ = releaseLock.Unlock()
}
