// worktree_release_revival_test.go holds the release-reservation tests of the
// paths that bind an existing session to a worktree again without a create.
package wsagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsgit"
)

// holdRelease makes dir a Git repository and takes its exclusive release
// lock, as worktree.release does; the returned func lets go.
func holdRelease(t *testing.T, dir string) func() {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		cmd := exec.Command("git", "init")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
	lock, err := wsgit.WorktreeReleaseLock(context.Background(), wsgit.ExecRunner{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := lock.Unlock(); err != nil {
			t.Fatal(err)
		}
	}
}

func refusedForRelease(t *testing.T, what string, err error) {
	t.Helper()
	if !isConflict(err) || !strings.Contains(err.Error(), "currently being released") {
		t.Fatalf("%s during a release = %v, want a conflict naming the release", what, err)
	}
}

// TestReleaseRefusesALeadTakeover: a terminal session is not adopted into a
// worktree being released, and the refusal writes nothing.
func TestReleaseRefusesALeadTakeover(t *testing.T) {
	h := newLeadHarness(t).start()
	const id = "termsess"
	before := h.terminalSession(id, LeadSession{Key: "terminal-lead-key", Harness: BackendClaude, Session: "sess-term", Cwd: h.root}, func(row *wsdashboard.Registration) { row.HarnessPID = 4002 })
	release := holdRelease(t, h.root)
	_, err := h.runner.TakeOver(id)
	refusedForRelease(t, "TakeOver", err)
	if got := h.row(id); got != before {
		t.Fatalf("a refused takeover rewrote the row:\n got %+v\nwant %+v", got, before)
	}
	if _, err := os.Stat(recordPath(wsdashboard.SessionDir(h.dir, id))); err == nil {
		t.Fatal("a refused takeover wrote a record")
	}
	if slices.Contains(h.ensured(), "terminal-lead-key") {
		t.Fatal("a refused takeover wrote the session's key")
	}
	if got := h.runner.ActiveChildrenInWorktree(h.root); len(got) != 0 {
		t.Fatalf("a refused takeover left a bound session: %+v", got)
	}
	release()
	if _, err := h.runner.TakeOver(id); err != nil {
		t.Fatalf("TakeOver after the release: %v", err)
	}
	if got := h.runner.ActiveChildrenInWorktree(h.root); len(got) != 1 {
		t.Fatalf("taken-over session bound = %+v, want 1", got)
	}
}

// TestReleaseCountsATakenOverLeadRunningInAnotherWorktree: a terminal lead
// rooted in one repository whose harness ran in a worktree elsewhere stays
// bound to that worktree after the takeover, so its release is refused while
// the lead is open, though State carries no cwd for a lead.
func TestReleaseCountsATakenOverLeadRunningInAnotherWorktree(t *testing.T) {
	h := newLeadHarness(t).start()
	other := releaseTestWorktree(t, h.root)
	const id = "termsess"
	h.terminalSession(id, LeadSession{Key: "terminal-lead-key", Harness: BackendClaude, Session: "sess-term", Cwd: other}, func(row *wsdashboard.Registration) { row.HarnessPID = 4002 })
	if _, err := h.runner.TakeOver(id); err != nil {
		t.Fatal(err)
	}
	if rec := h.record(id); rec.Root != h.root || rec.Cwd != other {
		t.Fatalf("taken-over record root=%q cwd=%q, want %q and %q", rec.Root, rec.Cwd, h.root, other)
	}
	got := h.runner.ActiveChildrenInWorktree(other)
	if len(got) != 1 || got[0].Address != AgentAddress(id) {
		t.Fatalf("bound to the harness's worktree = %+v, want the taken-over lead", got)
	}
	if _, err := h.runner.CloseConversation(id); err != nil {
		t.Fatal(err)
	}
	if got := h.runner.ActiveChildrenInWorktree(other); len(got) != 0 {
		t.Fatalf("a closed lead stays bound: %+v", got)
	}
}

// TestReleaseRefusesALeadReopen: a closed lead does not reopen into a
// worktree being released, and stays closed.
func TestReleaseRefusesALeadReopen(t *testing.T) {
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "done")...)
	h.idle(id)
	if _, err := h.runner.CloseConversation(id); err != nil {
		t.Fatal(err)
	}
	release := holdRelease(t, h.root)
	_, err := h.runner.Reopen(id)
	refusedForRelease(t, "Reopen", err)
	if st := h.state(id); st.Status != StatusClosed {
		t.Fatalf("a refused reopen opened the session: %+v", st)
	}
	if rec := h.record(id); rec.ClosedAt == "" {
		t.Fatalf("a refused reopen cleared ClosedAt: %+v", rec)
	}
	if got := h.runner.ActiveChildrenInWorktree(h.root); len(got) != 0 {
		t.Fatalf("a refused reopen left a bound session: %+v", got)
	}
	release()
	if _, err := h.runner.Reopen(id); err != nil {
		t.Fatalf("Reopen after the release: %v", err)
	}
	if got := h.runner.ActiveChildrenInWorktree(h.root); len(got) != 1 {
		t.Fatalf("reopened session bound = %+v, want 1", got)
	}
}

// TestReleaseRefusesAPiChildRevival: a closed Pi child's send does not
// revive it into a worktree being released, and starts nothing.
