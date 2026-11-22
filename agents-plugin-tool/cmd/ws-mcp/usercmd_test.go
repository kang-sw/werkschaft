package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

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
	if got, _, _ := u.List("other"); len(got) != 0 {
		t.Fatalf("another lead sees %+v", got)
	}
	// The lead moves to another worktree: its commands there are its own, the
	// first worktree's are not shown, and returning shows them again.
	runner.mu.Lock()
	runner.roots["lead"] = wt2
	runner.mu.Unlock()
	if got, _, _ := u.List("lead"); len(got) != 0 {
		t.Fatalf("another worktree sees %+v", got)
	}
	if _, _, ok, _ := u.Get("lead", "build"); ok {
		t.Fatal("a click in another worktree resolves the first worktree's command")
	}
	runner.mu.Lock()
	runner.roots["lead"] = wt1
	runner.mu.Unlock()
	if got, _, _ := u.List("lead"); len(got) != 1 || got[0].Line != "make" {
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

// TestUserCommandsFollowTheRunnersWorktree: every read takes the lead's bound
// worktree from the runner (memory) as it is at that moment, never from an
// earlier read; the revision a page is told carries the worktree, so a changed
// one moves it; and the list and its revision come from one snapshot.
func TestUserCommandsFollowTheRunnersWorktree(t *testing.T) {
	dir := t.TempDir()
	wt1, wt2 := filepath.Join(dir, "wt1"), filepath.Join(dir, "wt2")
	runner := &userCommandRunnerFake{gens: map[string]uint64{"lead": 1}, roots: map[string]string{"lead": wt1}, known: map[string]bool{"lead": true}}
	u := openUserCommandsFixture(t, dir, runner, nil)
	if _, _, err := u.Register("lead", wsusercmd.Input{ID: "build", Label: "Build", Line: "make"}); err != nil {
		t.Fatal(err)
	}
	cmds, rev1, err := u.List("lead")
	if err != nil || len(cmds) != 1 || u.Revision("lead") != rev1 {
		t.Fatalf("list %+v %q: %v", cmds, rev1, err)
	}
	runner.mu.Lock()
	runner.roots["lead"] = wt2
	runner.mu.Unlock()
	cmds, rev2, _ := u.List("lead")
	if len(cmds) != 0 || rev2 == rev1 || u.Revision("lead") != rev2 {
		t.Fatalf("after the worktree changed: %+v rev %q (was %q)", cmds, rev2, rev1)
	}
	got, root, found, _ := u.Get("lead", "build")
	if found || got.ID != "" || !sameRoot(root, wt2) {
		t.Fatalf("get in the other worktree: %+v %q %v", got, root, found)
	}
	// The two worktree spellings of one directory are one scope.
	if !sameRoot(wt1, wt1+string(filepath.Separator)) || sameRoot(wt1, wt2) {
		t.Fatal("sameRoot")
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
	got, rev, err := second.List("lead")
	if err != nil || len(got) != 1 || got[0] != stored || rev == "" {
		t.Fatalf("restored %+v rev %q: %v", got, rev, err)
	}
	if got, _, err := second.List("gone"); err != nil || len(got) != 0 {
		t.Fatalf("a forgotten conversation kept %+v (%v)", got, err)
	}
	// The registry's own change signal reaches the dashboard.
	var changed []string
	third := openUserCommandsFixture(t, t.TempDir(), runner, func(reply string) { changed = append(changed, reply) })
	if _, _, err := third.Register("lead", wsusercmd.Input{ID: "a", Label: "a", Line: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := third.Unregister("lead", "a"); err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 || changed[0] != "lead" {
		t.Fatalf("change signals %v", changed)
	}
}
