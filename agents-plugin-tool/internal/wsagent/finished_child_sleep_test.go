package wsagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsgit"
)

// idleHeldWorkerIn spawns a held Claude worker of lead bound to checkout
// root and lets its first turn finish, its process idle under its holder.
func idleHeldWorkerIn(t *testing.T, h *leadHarness, leadID, root string) (string, string) {
	t.Helper()
	st, err := h.runner.Spawn(SpawnSpec{
		Profile:            AgentWorkerProfile(BackendClaude),
		PermissionLevel:    PermissionWorkdirWrite,
		delegatedAuthority: true,
		Root:               root,
		Prompt:             "work",
		Tier:               "small",
		Parent:             &ParentRef{Kind: ParentLead, ReplyID: leadID},
	})
	if err != nil {
		t.Fatal(err)
	}
	holder := h.holder(st.ID)
	h.holders.out(holder, stateLine(HarnessRunning), initLine("worker-s1", "dontAsk"))
	h.holders.out(holder, recoveryWorkerReply("done")...)
	h.idle(st.ID)
	return st.ID, holder
}

// heldReleaseHarness is a held-worker harness whose lead is settled and
// whose root has a linked worktree, returned canonical.
func heldReleaseHarness(t *testing.T) (*leadHarness, string, string) {
	t.Helper()
	h, _ := configureHeldWorkerHarness(t)
	wt, err := filepath.EvalSymlinks(releaseTestWorktree(t, h.root))
	if err != nil {
		t.Fatal(err)
	}
	h.start()
	leadID, _ := h.settledLead()
	return h, wt, leadID
}

func holderListed(h *leadHarness, id string) bool {
	for _, info := range h.holders.List().Holders {
		if info.ID == id {
			return true
		}
	}
	return false
}

// releaseAndCheckStopped force-releases wt and checks that its idle held
// worker's holder ended and that the closure, holder forgotten, is on disk.
func releaseAndCheckStopped(t *testing.T, h *leadHarness, wt, id, holder string) {
	t.Helper()
	clock := newTestClock()
	setClock(clock, h.opts.Now())
	released := h.runner.CheckoutReleased(wt, releaseAfterBindings(t, clock, wt), true)
	if len(released) != 1 || released[0].Address != AgentAddress(id) || released[0].State != AgentClosed {
		t.Fatalf("release outcomes = %+v, want the worker closed", released)
	}
	if n := h.holders.opCount("stop " + holder); n != 1 || holderListed(h, holder) {
		t.Fatalf("the release did not end the worker's holder: stops %d, ops %v", n, h.holders.opList())
	}
	// The record on disk, not the runner's memory.
	rec, err := readRecord(wsdashboard.SessionDir(h.dir, id))
	if err != nil {
		t.Fatal(err)
	}
	if rec.ClosedAt == "" || rec.DeadBindingClosure == nil || rec.DeadBindingClosure.Checkout != wt {
		t.Fatalf("the release closure is not on disk: %+v", rec)
	}
	if rec.HolderID != "" || rec.HostPID != 0 || rec.HolderSeq != 0 {
		t.Fatalf("the closure on disk still names the ended holder: %+v", rec)
	}
	if got := h.runner.ActiveChildrenInWorktree(wt); len(got) != 0 {
		t.Fatalf("the ended worker still occupies the released checkout: %+v", got)
	}
}

// adminDir is the admin directory linked worktree wt's gitfile names.
func adminDir(t *testing.T, wt string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
	if !ok {
		t.Fatalf("not a gitfile: %q", raw)
	}
	return filepath.FromSlash(strings.TrimSpace(dir))
}

// A forced release ends an idle held worker's holder and child, and writes
// the closure to disk with the holder forgotten, though the worker ran under
// a holder when the release came. Occupancy names the holder and process of
// the live worker before the release.
func TestForcedReleaseStopsHeldWorkerAndPersistsClosure(t *testing.T) {
	h, wt, leadID := heldReleaseHarness(t)
	id, holder := idleHeldWorkerIn(t, h, leadID, wt)
	rec := h.record(id)
	got := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(id)]
	if !got.Live || got.Holder != holder || got.PID == 0 || got.PID != rec.HostPID {
		t.Fatalf("occupancy of the held worker = %+v, want live under holder %s, pid %d", got, holder, rec.HostPID)
	}
	releaseAndCheckStopped(t, h, wt, id, holder)
	if st := h.state(id); st.Status != StatusClosed || st.Process {
		t.Fatalf("released worker = %+v, want closed with no process", st)
	}
}

// The persisted closure keeps a released worker closed across a restart even
// after the removal of its checkout took the release marker with the admin
// directory and left the emptied directory behind (the Windows failure, which
// the gone-checkout check cannot see: no .git entry remains to compare).
func TestRestartKeepsReleasedChildClosedAfterAdminDirRemoved(t *testing.T) {
	h, wt, leadID := heldReleaseHarness(t)
	id, holder := idleHeldWorkerIn(t, h, leadID, wt)
	releaseAndCheckStopped(t, h, wt, id, holder)
	h.runner.Close()

	admin := adminDir(t, wt)
	if err := os.RemoveAll(admin); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := wsgit.WorktreeReleasedAt(wt); ok || err != nil {
		t.Fatalf("fixture: the release marker survived its admin directory: ok=%v err=%v", ok, err)
	}
	launches := h.holders.opCount("launch")
	h.holders.restart()
	h.start()
	wait(t, "restart holder scan", h.runner.rejoined)
	if st := h.state(id); st.Status != StatusClosed || st.Process {
		t.Fatalf("released worker after restart = %+v, want closed", st)
	}
	if _, err := h.runner.Send(id, "wake"); err == nil {
		t.Fatal("a send woke the released worker")
	}
	time.Sleep(50 * time.Millisecond)
	if n := h.holders.opCount("launch"); n != launches {
		t.Fatalf("restart launched a process for the released worker: %v", h.holders.opList())
	}
	if rec := h.record(id); rec.ClosedAt == "" || rec.HolderID != "" {
		t.Fatalf("released worker record after restart = %+v", rec)
	}
}

// A sleeping release-bound worker whose checkout is gone (its directory
// deleted, or its gitfile naming an admin directory that no longer exists)
// is closed at the next start with the "no longer exists" refusal and never
// launches there again.
func TestStartupClosesSleepingChildWhoseCheckoutIsGone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(t *testing.T, wt string)
	}{
		{name: "directory deleted", remove: func(t *testing.T, wt string) {
			if err := os.RemoveAll(wt); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "admin directory deleted", remove: func(t *testing.T, wt string) {
			if err := os.RemoveAll(adminDir(t, wt)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, wt, leadID := heldReleaseHarness(t)
			id, holder := idleHeldWorkerIn(t, h, leadID, wt)
			// The shutdown puts the finished worker to sleep.
			h.runner.Close()
			if rec := h.record(id); rec.HolderID != "" || rec.ClosedAt != "" || h.holders.opCount("stop "+holder) != 1 {
				t.Fatalf("fixture: worker not asleep after shutdown: %+v, ops %v", rec, h.holders.opList())
			}
			tc.remove(t, wt)
			launches := h.holders.opCount("launch")
			h.holders.restart()
			h.start()
			wait(t, "restart holder scan", h.runner.rejoined)
			eventually(t, "the worker closed at startup", func() bool { return h.state(id).Status == StatusClosed })
			rec := h.record(id)
			if rec.ClosedAt == "" || rec.DeadBindingClosure == nil {
				t.Fatalf("the startup close was not persisted: %+v", rec)
			}
			if closure := rec.DeadBindingClosure; closure.Checkout != wt || !strings.Contains(closure.Reason, "no longer exists") {
				t.Fatalf("startup closure = %+v, want the gone-checkout refusal naming %s", closure, wt)
			}
			if _, err := h.runner.Send(id, "wake"); err == nil {
				t.Fatal("a send woke the worker of a gone checkout")
			}
			time.Sleep(50 * time.Millisecond)
			if n := h.holders.opCount("launch"); n != launches {
				t.Fatalf("a process launched for the worker of a gone checkout: %v", h.holders.opList())
			}
		})
	}
}

// goneBinding reads a binding as removed only on evidence of a removal: a
// bound checkout gone, or a root or working directory missing from a parent
// that still exists and contained by no live bound checkout. A missing
// subdirectory of a live checkout, or a path whose parent is missing too (an
// unreachable volume), is not evidence; a lead is never release-bound.
func TestGoneBindingNeedsEvidenceOfRemoval(t *testing.T) {
	wt, err := filepath.EvalSymlinks(releaseTestWorktree(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	child := Record{Parent: &ParentRef{Kind: ParentLead, ReplyID: "lead"}}
	missingSub := filepath.Join(wt, "build", "out")
	removed := filepath.Join(filepath.Dir(wt), "removed-wt")
	unreachable := filepath.Join(filepath.Dir(wt), "no-volume", "wt")
	for _, tc := range []struct {
		name            string
		rec             Record
		root, cwd, want string
		checkouts       []string
	}{
		{name: "intact binding", rec: child, root: wt, cwd: wt, checkouts: []string{wt}},
		{name: "missing subdirectory of a live checkout", rec: child, root: wt, cwd: missingSub, checkouts: []string{wt}},
		{name: "root removed, resolved after the removal", rec: child, root: removed, cwd: removed, want: removed},
		{name: "cwd removed outside the live root checkout", rec: child, root: wt, cwd: removed, checkouts: []string{wt}, want: removed},
		{name: "parent missing too", rec: child, root: unreachable, cwd: unreachable},
		{name: "lead", rec: Record{Lead: true}, root: removed, cwd: removed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := goneBinding(tc.rec, tc.root, tc.cwd, tc.checkouts); got != tc.want {
				t.Fatalf("goneBinding = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("bound checkout removed", func(t *testing.T) {
		if err := os.RemoveAll(adminDir(t, wt)); err != nil {
			t.Fatal(err)
		}
		if got := goneBinding(child, wt, missingSub, []string{wt}); got != wt {
			t.Fatalf("goneBinding = %q, want the unregistered checkout %s", got, wt)
		}
	})
}

// A sleeping worker whose working directory, a subdirectory of its still
// registered checkout, is missing at startup (a branch switch or a clean took
// it) stays open: the checkout lives, so nothing ended the binding.
func TestStartupKeepsSleepingChildWhoseCwdSubdirectoryIsMissing(t *testing.T) {
	h, wt, leadID := heldReleaseHarness(t)
	sub := filepath.Join(wt, "build")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := h.runner.Spawn(SpawnSpec{
		Profile:            AgentWorkerProfile(BackendClaude),
		PermissionLevel:    PermissionWorkdirWrite,
		delegatedAuthority: true,
		Root:               wt,
		Cwd:                sub,
		Prompt:             "work",
		Tier:               "small",
		Parent:             &ParentRef{Kind: ParentLead, ReplyID: leadID},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	holder := h.holder(id)
	h.holders.out(holder, stateLine(HarnessRunning), initLine("worker-s1", "dontAsk"))
	h.holders.out(holder, recoveryWorkerReply("done")...)
	h.idle(id)
	h.runner.Close()
	if rec := h.record(id); rec.HolderID != "" || rec.ClosedAt != "" {
		t.Fatalf("fixture: worker not asleep after shutdown: %+v", rec)
	}
	if err := os.Remove(sub); err != nil {
		t.Fatal(err)
	}
	h.holders.restart()
	h.start()
	wait(t, "restart holder scan", h.runner.rejoined)
	time.Sleep(50 * time.Millisecond)
	if st := h.state(id); st.Status == StatusClosed {
		t.Fatalf("a missing subdirectory of a live checkout closed the worker: %+v", st)
	}
	if rec := h.record(id); rec.ClosedAt != "" || rec.DeadBindingClosure != nil {
		t.Fatalf("a missing subdirectory of a live checkout persisted a closure: %+v", rec)
	}
}

// A finished-idle Codex worker (WorkerHTTPMCP, whose ws-mcp route a restart
// on another port makes stale) sleeps across a daemon restart rather than
// restarting or taking a standalone restore turn, both when the shutdown
// sleeps it (letGo) and when the turn ended while no daemon ran (the
// rejoin's replayDone). A later message wakes it with the restore bundle
// before the work.
func TestFinishedHTTPMCPWorkerSleepsAcrossRestart(t *testing.T) {
	for _, path := range []string{"shutdown", "rejoin"} {
		t.Run(path, func(t *testing.T) {
			h, got := workerRecoveryHarness(t, BackendCodex, true)
			id, holder := spawnHeldRecoveryWorker(t, h, BackendCodex, "workdir-write")
			if path == "shutdown" {
				completeRecoveryWorker(t, h, BackendCodex, holder)
				eventually(t, "the worker's report", func() bool { return len(got.list()) == 1 && !h.record(id).TurnOpen })
				h.quiesce(id)
			}
			h.runner.Close()
			if path == "shutdown" {
				if rec := h.record(id); h.holders.opCount("stop "+holder) != 1 || rec.HolderID != "" || rec.MCPToken != "" {
					t.Fatalf("shutdown did not put the finished worker to sleep: %+v, ops %v", rec, h.holders.opList())
				}
			} else if h.holders.opCount("stop") != 0 {
				t.Fatalf("shutdown stopped the mid-turn worker: %v", h.holders.opList())
			}
			h.holders.restart()
			if path == "rejoin" {
				completeRecoveryWorker(t, h, BackendCodex, holder)
			}
			probe := &restoreProbe{state: "stale"}
			h.opts.RestoreBundle = probe.render
			h.startAt("http://127.0.0.1:2")
			eventually(t, "the worker's one report", func() bool { return len(got.list()) == 1 })
			eventually(t, "the finished worker asleep", func() bool {
				return h.record(id).HolderID == "" && h.holders.opCount("stop "+holder) == 1
			})
			if path == "rejoin" && !strings.Contains(h.log.String(), "finished child sleeps (rejoined after its turn ended)") {
				t.Fatalf("the rejoin did not sleep the finished worker; log:\n%s", h.log.String())
			}
			time.Sleep(50 * time.Millisecond)
			if calls := probe.snapshot(); len(calls) != 0 {
				t.Fatalf("a standalone restore turn for the sleeping worker: %+v", calls)
			}
			if h.holders.opCount("launch") != 1 || h.holders.opCount("spawn") != 1 {
				t.Fatalf("the stale ws-mcp route restarted the finished worker: %v", h.holders.opList())
			}
			if st := h.state(id); st.Process || st.Status == StatusClosed {
				t.Fatalf("worker after restart = %+v, want asleep", st)
			}

			probe.setState("fresh-at-wake")
			h.send(id, "follow-up work")
			eventually(t, "a new holder for the woken worker", func() bool {
				rec := h.record(id)
				return rec.HolderID != "" && rec.HolderID != holder
			})
			next := h.holder(id)
			eventually(t, "the woken worker's turn", func() bool { return len(codexUserInputs(h, next)) == 1 })
			text := codexUserInputs(h, next)[0]
			restoreAt, workAt := strings.Index(text, "restore[fresh-at-wake]"), strings.Index(text, "follow-up work")
			if restoreAt < 0 || workAt < 0 || restoreAt > workAt || !strings.Contains(text, "cause=restart") {
				t.Fatalf("woken worker input = %q, want the restart restore before the work", text)
			}
			if calls := probe.snapshot(); len(calls) != 1 || calls[0].cause != RestoreRestart || calls[0].key != h.record(id).WSKey {
				t.Fatalf("restore calls = %+v", calls)
			}
			if h.record(id).MCPURL == "" || !strings.HasPrefix(h.record(id).MCPURL, "http://127.0.0.1:2") {
				t.Fatalf("the woken worker's ws-mcp route = %q, want this daemon's", h.record(id).MCPURL)
			}
		})
	}
}
