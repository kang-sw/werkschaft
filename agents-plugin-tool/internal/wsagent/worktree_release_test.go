// worktree_release_test.go covers the release boundary of spawned
// conversations bound to a checkout.
package wsagent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsgit"
)

// releaseTell is one refusal notice the runner sent a message's sender.
type releaseTell struct {
	from, id, checkout, reason string
	released                   bool // through CheckoutReleased, not WakeRefused
}

type releaseTells struct {
	mu   sync.Mutex
	list []releaseTell
}

func (r *releaseTells) add(t releaseTell) {
	r.mu.Lock()
	r.list = append(r.list, t)
	r.mu.Unlock()
}

func (r *releaseTells) snapshot() []releaseTell {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]releaseTell(nil), r.list...)
}

// releaseHarness is an agent harness on a git root with a linked worktree,
// a clock the test advances, and the runner's refusal notices captured.
func releaseHarness(t *testing.T) (h *harness, clk *testClock, wt string, tells *releaseTells) {
	t.Helper()
	clk = newTestClock()
	tells = &releaseTells{}
	h, _ = agentHarness(t, func(o *Options) {
		o.Now = clk.Now
		o.CheckoutReleased = func(from, id, checkout, reason string) {
			tells.add(releaseTell{from: from, id: id, checkout: checkout, reason: reason, released: true})
		}
		o.WakeRefused = func(from, id, reason string) {
			tells.add(releaseTell{from: from, id: id, reason: reason})
		}
	})
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
	wt = filepath.Join(t.TempDir(), "pooled-wt")
	cmd := exec.Command("git", "worktree", "add", "--detach", wt, "HEAD")
	cmd.Dir = h.root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return h, clk, wt, tells
}

// release records wt's completed release a whole second after every binding
// made so far, as a worktree.release finishing later would.
func releaseAfterBindings(t *testing.T, clk *testClock, wt string) {
	t.Helper()
	clk.advance(2 * time.Second)
	if err := wsgit.RecordWorktreeRelease(wt, clk.Now()); err != nil {
		t.Fatal(err)
	}
}

func boundByAddress(children []BoundWorktreeChild) map[string]BoundWorktreeChild {
	out := map[string]BoundWorktreeChild{}
	for _, c := range children {
		out[c.Address] = c
	}
	return out
}

func noTurnSent(t *testing.T, p *fakeProc, what string) {
	t.Helper()
	select {
	case s := <-p.sent:
		t.Fatalf("%s: a turn was sent: %q", what, s)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestCheckoutReleaseEndsEarlierBindings: a release's notice marks every
// spawned conversation bound before it and, when forced, interrupts each
// running turn without stopping any process, reporting a failed interrupt.
// The occupancy query flags those bindings and their live processes. Mail
// to one of them is refused with a notice naming the checkout and starts no
// turn, live process or not; a child bound after the release launches
// normally.
func TestCheckoutReleaseEndsEarlierBindings(t *testing.T) {
	h, clk, wt, tells := releaseHarness(t)
	lead := h.leadCaller()
	spawn := func(prompt string) (string, *fakeProc) {
		return h.agentRunning(lead, AgentSpawnRequest{Prompt: prompt, Tier: "medium", Authority: "workdir-write", Worktree: wt})
	}
	failing, failingProc := spawn("failing interrupt")
	failingProc.failInterrupts(errors.New("backend refused the interrupt"))
	running, runningProc := spawn("interruptible")
	sleeper, sleeperProc := spawn("finishes early")
	sleeperProc.reply("done")
	eventually(t, "sleeper settled", func() bool { return h.state(sleeper).Status == StatusIdle })
	h.asleep(sleeper)

	before := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))
	if len(before) != 3 {
		t.Fatalf("bound children = %+v, want 3", before)
	}
	for addr, c := range before {
		if c.BoundBeforeRelease {
			t.Fatalf("with no recorded release no binding predates one: %s %+v", addr, c)
		}
	}
	if !before[AgentAddress(failing)].Live || !before[AgentAddress(running)].Live || before[AgentAddress(sleeper)].Live {
		t.Fatalf("live flags = %+v, want the two running children live and the sleeper not", before)
	}

	releaseAfterBindings(t, clk, wt)
	after := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))
	for addr, c := range after {
		if !c.BoundBeforeRelease {
			t.Fatalf("a child bound before the release must be flagged: %s %+v", addr, c)
		}
	}
	if !after[AgentAddress(failing)].Live || after[AgentAddress(sleeper)].Live {
		t.Fatalf("live flags after release = %+v", after)
	}

	outcomes := map[string]ReleasedConversation{}
	for _, c := range h.runner.CheckoutReleased(wt, true) {
		outcomes[c.Address] = c
	}
	if len(outcomes) != 3 {
		t.Fatalf("released conversations = %+v, want 3", outcomes)
	}
	if got := outcomes[AgentAddress(failing)]; got.Cancelled || !strings.Contains(got.Error, "backend refused the interrupt") {
		t.Fatalf("a failed interrupt must be reported, not cancelled: %+v", got)
	}
	if got := outcomes[AgentAddress(running)]; !got.Cancelled || got.Error != "" {
		t.Fatalf("the running turn must be cancelled: %+v", got)
	}
	if got := outcomes[AgentAddress(sleeper)]; got.Cancelled || got.Error != "" {
		t.Fatalf("a sleeping child has no turn to cancel: %+v", got)
	}
	if procInterrupts(failingProc) != 1 || procInterrupts(runningProc) != 1 {
		t.Fatalf("interrupts = %d, %d; want one each", procInterrupts(failingProc), procInterrupts(runningProc))
	}
	if failingProc.isClosed() || runningProc.isClosed() {
		t.Fatal("a release must never stop a process, even when its interrupt failed")
	}
	if !h.state(failing).Process || h.state(failing).Status != StatusRunning {
		t.Fatalf("the failed-interrupt child keeps its process and turn: %+v", h.state(failing))
	}

	// The interrupted turn ends; the process stays, awake and idle, and
	// keeps the worktree's binding live.
	runningProc.reply("interrupted")
	eventually(t, "interrupted turn settled", func() bool { return h.state(running).Status == StatusIdle })
	if c := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(running)]; !c.Live {
		t.Fatalf("an awake idle process is live: %+v", c)
	}

	// Mail to the live child and to the asleep one is refused, naming the
	// released worktree, and starts nothing.
	starts := h.backend.startCount()
	for _, id := range []string{running, sleeper} {
		if _, err := h.runner.Deliver(id, Delivery{From: "sender-reply", Text: "keep going"}); err != nil {
			t.Fatalf("Deliver(%s): %v", id, err)
		}
	}
	noTurnSent(t, runningProc, "mail to a child released while awake")
	if n := h.backend.startCount(); n != starts {
		t.Fatalf("mail to released children started %d processes", n-starts)
	}
	got := tells.snapshot()
	if len(got) != 2 {
		t.Fatalf("refusal notices = %+v, want one per mail", got)
	}
	for _, tell := range got {
		if !tell.released || tell.from != "sender-reply" || canonicalWorktreePath(tell.checkout) != canonicalWorktreePath(wt) || !strings.Contains(tell.reason, "released") {
			t.Fatalf("notice must reach the sender through CheckoutReleased naming the worktree: %+v", tell)
		}
	}
	if st := h.state(sleeper); st.Process || !strings.Contains(st.Error, "released") {
		t.Fatalf("the asleep child stays asleep with the refusal: %+v", st)
	}

	// A child bound after the release (in its own second, still counted as
	// current) launches and works normally.
	fresh, freshProc := spawn("after release")
	if c := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(fresh)]; c.BoundBeforeRelease || !c.Live {
		t.Fatalf("a child bound after the release is a current, live binding: %+v", c)
	}
	freshProc.reply("done")
	eventually(t, "fresh child settled", func() bool { return h.state(fresh).Status == StatusIdle })
	if _, err := h.runner.Deliver(fresh, Delivery{From: "sender-reply", Text: "next"}); err != nil {
		t.Fatal(err)
	}
	freshProc.nextSent(t)
}

// TestCheckoutReleaseBackgroundWorkKeepsBindingLive: a child bound before a
// release whose turn ended with background work still running keeps a live
// binding (the checkout stays out of reuse); once that work ends and its
// process sleeps, the binding is no longer live.
func TestCheckoutReleaseBackgroundWorkKeepsBindingLive(t *testing.T) {
	restoreGrace := bgResumeGrace
	bgResumeGrace = 60 * time.Millisecond
	t.Cleanup(func() { bgResumeGrace = restoreGrace })

	h, clk, wt, _ := releaseHarness(t)
	lead := h.leadCaller()
	child, proc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "run build", Tier: "small", Authority: "workdir-write", Worktree: wt})
	one, zero := 1, 0
	proc.events <- Event{BackgroundTasks: &one}
	proc.reply("started background build")
	eventually(t, "background state", func() bool {
		return findAgent(t, h.runner.AgentList(lead), child).State == AgentBackground
	})

	releaseAfterBindings(t, clk, wt)
	h.runner.CheckoutReleased(wt, false)
	if c := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(child)]; !c.BoundBeforeRelease || !c.Live {
		t.Fatalf("background work after the turn keeps a pre-release binding live: %+v", c)
	}
	if proc.isClosed() {
		t.Fatal("a release must never stop a process")
	}

	proc.events <- Event{BackgroundTasks: &zero}
	eventually(t, "background work ended", func() bool { return h.state(child).BackgroundTasks == 0 })
	h.asleep(child)
	if c := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(child)]; !c.BoundBeforeRelease || c.Live {
		t.Fatalf("an asleep pre-release binding is not live: %+v", c)
	}
}

// TestCheckoutReleaseRefusesWakeByRootOrCwdAfterRestart: a daemon that
// starts after the release has no in-memory notice; the wake of a child
// bound by root (agent.spawn worktree) or by working directory (cwd inside
// the worktree) reads the release marker and is refused, naming the
// worktree, with no process started.
func TestCheckoutReleaseRefusesWakeByRootOrCwdAfterRestart(t *testing.T) {
	h, clk, wt, tells := releaseHarness(t)
	sub := filepath.Join(wt, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	lead := h.leadCaller()
	byRoot, rootProc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "root", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	byCwd, cwdProc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "cwd", Tier: "medium", Authority: "workdir-write", Cwd: sub})
	for id, p := range map[string]*fakeProc{byRoot: rootProc, byCwd: cwdProc} {
		p.reply("done")
		eventually(t, id+" settled", func() bool { return h.state(id).Status == StatusIdle })
		h.asleep(id)
	}
	releaseAfterBindings(t, clk, wt)

	h.runner.Close()
	h.start()
	starts := h.backend.startCount()
	for _, id := range []string{byRoot, byCwd} {
		if _, err := h.runner.Deliver(id, Delivery{From: "sender-" + id, Text: "wake up"}); err != nil {
			t.Fatalf("Deliver(%s): %v", id, err)
		}
	}
	eventually(t, "both wakes refused", func() bool {
		return strings.Contains(h.state(byRoot).Error, "released") && strings.Contains(h.state(byCwd).Error, "released")
	})
	if n := h.backend.startCount(); n != starts {
		t.Fatalf("refused wakes started %d processes", n-starts)
	}
	got := tells.snapshot()
	if len(got) != 2 {
		t.Fatalf("refusal notices = %+v, want two", got)
	}
	for _, tell := range got {
		if !tell.released || tell.from != "sender-"+tell.id || canonicalWorktreePath(tell.checkout) != canonicalWorktreePath(wt) {
			t.Fatalf("notice must name the released worktree to its sender: %+v", tell)
		}
	}
	// Neither counts as a current binding, and neither is live: an
	// acquisition may reuse the worktree.
	for addr, c := range boundByAddress(h.runner.ActiveChildrenInWorktree(wt)) {
		if !c.BoundBeforeRelease || c.Live {
			t.Fatalf("%s after a refused wake: %+v, want pre-release and not live", addr, c)
		}
	}
}

// TestCheckoutReleaseKeepsLeadBindings: a release ends only spawned
// conversations' bindings; the notice and the occupancy flag leave a
// conversation without a parent alone.
func TestCheckoutReleaseKeepsLeadBindings(t *testing.T) {
	rec := Record{Root: "/x", CreatedAt: "2026-11-22T10:00:00Z"}
	if releaseBound(rec) {
		t.Fatal("a conversation nobody spawned keeps its binding")
	}
	rec.Parent = &ParentRef{Kind: ParentLead, ReplyID: "lead"}
	rec.Lead = true
	if releaseBound(rec) {
		t.Fatal("a lead keeps its binding")
	}
	rec.Lead = false
	if !releaseBound(rec) {
		t.Fatal("a spawned worker loses its binding to a release")
	}
}
