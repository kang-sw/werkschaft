package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// userCommandRunnerFake is the runner side of the user commands: open hosted
// leads with their bound worktrees, and the count of worktree reads (each one a
// read of the lead's key record in the real runner).
type userCommandRunnerFake struct {
	mu        sync.Mutex
	gens      map[string]uint64
	roots     map[string]string
	known     map[string]bool
	rootReads int
}

func (r *userCommandRunnerFake) ComposerOpen(id string) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.gens[id]; ok {
		return g, nil
	}
	return 0, wsagent.ErrNotComposerTarget
}

func (r *userCommandRunnerFake) ComposerRoot(id string) (wsagent.ExecOwner, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gens[id]
	if !ok {
		return wsagent.ExecOwner{}, "", wsagent.ErrNotComposerTarget
	}
	r.rootReads++
	return wsagent.ExecOwner{ID: id, Gen: g, Lead: true}, r.roots[id], nil
}

func (r *userCommandRunnerFake) CronKnown(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.known[id]
}

func (r *userCommandRunnerFake) reads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rootReads
}

func openUserCommandsFixture(t *testing.T, dir string, runner *userCommandRunnerFake, changed func(string)) *userCommands {
	t.Helper()
	u, err := openUserCommands(dir, runner, changed, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = u.Close() })
	return u
}

func TestUserCommandsAreScopedToTheLeadAndItsWorktree(t *testing.T) {
	dir := t.TempDir()
	wt1, wt2 := filepath.Join(dir, "wt1"), filepath.Join(dir, "wt2")
	runner := &userCommandRunnerFake{
		gens:  map[string]uint64{"lead": 1, "other": 1},
		roots: map[string]string{"lead": wt1, "other": wt1},
		known: map[string]bool{"lead": true, "other": true},
	}
	u := openUserCommandsFixture(t, dir, runner, nil)
	if _, _, err := u.Register("lead", wsusercmd.Input{ID: "build", Label: "Build", Line: "make"}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := u.List("other", true); len(got) != 0 {
		t.Fatalf("another lead sees %+v", got)
	}
	// The lead moves to another worktree: its commands there are its own, the
	// first worktree's are not shown, and returning shows them again.
	runner.mu.Lock()
	runner.roots["lead"] = wt2
	runner.mu.Unlock()
	if got, _, _ := u.List("lead", true); len(got) != 0 {
		t.Fatalf("another worktree sees %+v", got)
	}
	if _, ok, _ := u.Get("lead", "build"); ok {
		t.Fatal("a click in another worktree resolves the first worktree's command")
	}
	runner.mu.Lock()
	runner.roots["lead"] = wt1
	runner.mu.Unlock()
	if got, _, _ := u.List("lead", true); len(got) != 1 || got[0].Line != "make" {
		t.Fatalf("back in the first worktree: %+v", got)
	}
	// A conversation that is not an open lead has nothing to list or register.
	if _, _, err := u.Register("worker", wsusercmd.Input{ID: "x", Label: "x", Line: "true"}); !errors.Is(err, wsagent.ErrNotComposerTarget) {
		t.Fatalf("non-lead register: %v", err)
	}
	if err := u.Lead("worker"); err == nil {
		t.Fatal("a worker passed the lead check")
	}
}

// TestUserCommandsPageReadsReuseTheBoundWorktree: the list's worktree read goes
// to the lead's key record, so page loads and refetches reuse it for the TTL
// (and for the same open generation); a click, a removal and the tools read
// afresh.
func TestUserCommandsPageReadsReuseTheBoundWorktree(t *testing.T) {
	dir := t.TempDir()
	runner := &userCommandRunnerFake{gens: map[string]uint64{"lead": 1}, roots: map[string]string{"lead": filepath.Join(dir, "wt")}, known: map[string]bool{"lead": true}}
	u := openUserCommandsFixture(t, dir, runner, nil)
	now := time.Now()
	u.now = func() time.Time { return now }
	if _, _, err := u.Register("lead", wsusercmd.Input{ID: "build", Label: "Build", Line: "make"}); err != nil {
		t.Fatal(err)
	}
	base := runner.reads()
	for range 50 {
		if _, _, err := u.List("lead", false); err != nil {
			t.Fatal(err)
		}
		u.Revision("lead")
	}
	if got := runner.reads() - base; got != 0 {
		t.Fatalf("50 page reads made %d worktree reads, want none (the registration cached it)", got)
	}
	// The TTL ends: one read, then cached again.
	now = now.Add(userCommandRootTTL + time.Second)
	_, _, _ = u.List("lead", false)
	_, _, _ = u.List("lead", false)
	if got := runner.reads() - base; got != 1 {
		t.Fatalf("after the TTL: %d reads, want 1", got)
	}
	// A reopened lead (a new generation) never reuses the old read.
	runner.mu.Lock()
	runner.gens["lead"] = 2
	runner.mu.Unlock()
	_, _, _ = u.List("lead", false)
	if got := runner.reads() - base; got != 2 {
		t.Fatalf("after a reopen: %d reads, want 2", got)
	}
	before := runner.reads()
	_, _, _ = u.Get("lead", "build")
	_, _ = u.Remove("lead", "nothing")
	if got := runner.reads() - before; got != 2 {
		t.Fatalf("a click and a removal made %d fresh reads, want 2", got)
	}
}

// TestUserCommandsReloadAfterARestartAndAnOldDaemon: the commands are kept in a
// store of their own. A daemon that does not know it (an older one, which opens
// only its own databases and holder directories) leaves it untouched, and the
// daemon that reopens restores every command with its revision; commands of a
// conversation the runner has forgotten are dropped by that open.
func TestUserCommandsReloadAfterARestartAndAnOldDaemon(t *testing.T) {
	dir := t.TempDir()
	wt := filepath.Join(dir, "wt")
	runner := &userCommandRunnerFake{
		gens:  map[string]uint64{"lead": 1, "gone": 1},
		roots: map[string]string{"lead": wt, "gone": wt},
		known: map[string]bool{"lead": true, "gone": true},
	}
	first, err := openUserCommands(dir, runner, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	stored, _, err := first.Register("lead", wsusercmd.Input{ID: "build", Label: "Build", Description: "d", Line: "make\nmake test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.Register("gone", wsusercmd.Input{ID: "x", Label: "x", Line: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(dir, wsusercmd.StoreName)
	before, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	// The older daemon: it opens its own job store and exit directory in the
	// same state directory and never names the user commands' database.
	old, err := execjob.OpenHostedStore(filepath.Join(dir, hostedExecStoreName))
	if err != nil {
		t.Fatal(err)
	}
	_ = old.Close()
	if err := os.MkdirAll(filepath.Join(dir, hostedExecExitsName), 0o700); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(storePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the older daemon's opens changed the user command store (%v)", err)
	}
	// The conversation "gone" was forgotten by the runner meanwhile.
	runner.mu.Lock()
	runner.known["gone"] = false
	runner.mu.Unlock()
	second := openUserCommandsFixture(t, dir, runner, nil)
	got, rev, err := second.List("lead", true)
	if err != nil || len(got) != 1 || got[0] != stored || rev == "" {
		t.Fatalf("restored %+v rev %q: %v", got, rev, err)
	}
	if got, _, err := second.List("gone", true); err != nil || len(got) != 0 {
		t.Fatalf("a forgotten conversation kept %+v (%v)", got, err)
	}
	// The registry's own change signal reaches the dashboard.
	var changed []string
	third := openUserCommandsFixture(t, t.TempDir(), runner, func(reply string) { changed = append(changed, reply) })
	if _, _, err := third.Register("lead", wsusercmd.Input{ID: "a", Label: "a", Line: "true"}); err != nil {
		t.Fatal(err)
	}
	if _, err := third.Remove("lead", "a"); err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 || changed[0] != "lead" {
		t.Fatalf("change signals %v", changed)
	}
}
