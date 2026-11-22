// worktree_release_revival_test.go holds the release-reservation tests of the
// paths that bind an existing session to a worktree again without a create.
package wsagent

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
func TestReleaseRefusesAPiChildRevival(t *testing.T) {
	h, _ := childHarness(t)
	_, _, token, child, oldProc := workerWithChild(t, h)
	if code, body := h.tokenCall(token, "stop", map[string]any{"id": child.ID}); code != http.StatusOK {
		t.Fatalf("stop = %d %s", code, body)
	}
	if !oldProc.isClosed() {
		t.Fatal("pi.stop left the process alive")
	}
	file := piSessionFile(wsdashboard.SessionDir(h.dir, child.ID))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("persisted conversation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bound := len(h.runner.ActiveChildrenInWorktree(h.root))
	release := holdRelease(t, h.root)
	code, body := h.tokenCall(token, "send", map[string]any{"id": child.ID, "message": "continue"})
	if code != http.StatusConflict || !strings.Contains(body, "currently being released") {
		t.Fatalf("revival during a release = %d %s", code, body)
	}
	if h.state(child.ID).Status != StatusClosed || h.record(child.ID).ClosedAt == "" || h.backend.startCount() != 2 {
		t.Fatal("a refused revival must leave the record closed and start nothing")
	}
	if got := h.runner.ActiveChildrenInWorktree(h.root); len(got) != bound {
		t.Fatalf("a refused revival changed the bound sessions: %+v", got)
	}
	release()
	if code, body := h.tokenCall(token, "send", map[string]any{"id": child.ID, "message": "continue"}); code != http.StatusOK {
		t.Fatalf("revival after the release = %d %s", code, body)
	}
	if text := h.backend.waitProc(t, 2).nextSent(t); text != h.envelope("continue") {
		t.Fatalf("resumed prompt = %q", text)
	}
}
