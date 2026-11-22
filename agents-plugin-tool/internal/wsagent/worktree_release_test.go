// worktree_release_test.go covers the release boundary of spawned
// conversations bound to a checkout.
package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

// releaseAfterBindings records wt's completed release marker a whole second
// after every binding made so far, as a worktree.release finishing later
// would, and returns its instant. It does not tell the runner: a test passes
// the instant to CheckoutReleased (the release's notice), or restarts the
// runner, which reads the marker when it loads its conversations.
func releaseAfterBindings(t *testing.T, clk *testClock, wt string) time.Time {
	t.Helper()
	clk.advance(2 * time.Second)
	at := clk.Now()
	if err := wsgit.RecordWorktreeRelease(wt, at); err != nil {
		t.Fatal(err)
	}
	return at
}

// setClock sets the harness clock to at.
func setClock(clk *testClock, at time.Time) {
	clk.mu.Lock()
	clk.now = at
	clk.mu.Unlock()
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

	at := releaseAfterBindings(t, clk, wt)
	outcomes := map[string]ReleasedConversation{}
	for _, c := range h.runner.CheckoutReleased(wt, at, true) {
		outcomes[c.Address] = c
	}
	after := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))
	for addr, c := range after {
		if !c.BoundBeforeRelease {
			t.Fatalf("a child bound before the release must be flagged: %s %+v", addr, c)
		}
	}
	if !after[AgentAddress(failing)].Live || after[AgentAddress(sleeper)].Live {
		t.Fatalf("live flags after release = %+v", after)
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

	h.runner.CheckoutReleased(wt, releaseAfterBindings(t, clk, wt), false)
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

// TestCheckoutReleaseRefusesMailboxMail: mail claimed from the mailbox of a
// child bound before a release is held, never steered into a live turn nor
// started as a new one, and each sender hears once, through the
// checkout-release notice naming the worktree. That holds for a child marked
// in memory while its process lives and for an asleep child after a daemon
// restart, which learns the release only when its wake is refused.
func TestCheckoutReleaseRefusesMailboxMail(t *testing.T) {
	h, clk, wt, tells := releaseHarness(t)
	mail := newFakeMail()
	h.config.Mail = mail
	h.runner.Close()
	h.start()
	lead := h.leadCaller()
	spawn := func(prompt string) (string, *fakeProc) {
		return h.agentRunning(lead, AgentSpawnRequest{Prompt: prompt, Tier: "medium", Authority: "workdir-write", Worktree: wt})
	}
	live, liveProc := spawn("still running")
	sleeper, sleeperProc := spawn("finishes early")
	sleeperProc.reply("done")
	eventually(t, "sleeper settled", func() bool { return h.state(sleeper).Status == StatusIdle })
	h.asleep(sleeper)
	liveKey, sleeperKey := h.keyOf(live), h.keyOf(sleeper)
	eventually(t, "mailboxes watched", func() bool { return mail.watched(liveKey) && mail.watched(sleeperKey) })

	checkTold := func(n int, from string) {
		t.Helper()
		eventually(t, "sender told", func() bool { return len(tells.snapshot()) == n })
		tell := tells.snapshot()[n-1]
		if !tell.released || tell.from != from || canonicalWorktreePath(tell.checkout) != canonicalWorktreePath(wt) || !strings.Contains(tell.reason, "released") {
			t.Fatalf("notice must reach the sender through CheckoutReleased naming the worktree: %+v", tell)
		}
	}

	// (a) A live child marked released by the release notice.
	h.runner.CheckoutReleased(wt, releaseAfterBindings(t, clk, wt), false)
	mail.deliverFrom(liveKey, "peerreplyid", "keep going")
	checkTold(1, "peerreplyid")
	if got, _ := liveProc.steered(); len(got) != 0 {
		t.Fatalf("mail was steered into a released child's turn: %q", got)
	}
	liveProc.reply("turn over")
	eventually(t, "live child settled", func() bool { return h.state(live).Status == StatusIdle })
	noTurnSent(t, liveProc, "held mail after the released child's turn")
	if liveProc.isClosed() {
		t.Fatal("a release must never stop a process")
	}

	// (b) An asleep child after a restart: no notice reached this daemon;
	// loading the conversation read the marker.
	h.runner.Close()
	h.start()
	eventually(t, "mailbox watched after restart", func() bool { return mail.watched(sleeperKey) })
	starts := h.backend.startCount()
	mail.deliverFrom(sleeperKey, "otherreplyid", "wake up")
	checkTold(2, "otherreplyid")
	if n := h.backend.startCount(); n != starts {
		t.Fatalf("mail to a released asleep child started %d processes", n-starts)
	}
	if st := h.state(sleeper); st.Process || !strings.Contains(st.Error, "released") {
		t.Fatalf("the asleep child stays asleep with the refusal: %+v", st)
	}
	if got := tells.snapshot(); len(got) != 2 {
		t.Fatalf("each sender is told once: %+v", got)
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

// TestCheckoutReleaseEndsSameSecondEarlierBinding: a binding made earlier in
// the release's own second predates it (nanosecond precision), so its wake
// is refused; one made at the release instant stays current.
func TestCheckoutReleaseEndsSameSecondEarlierBinding(t *testing.T) {
	h, clk, wt, tells := releaseHarness(t)
	lead := h.leadCaller()
	second := time.Date(2026, 11, 22, 10, 0, 5, 0, time.UTC)
	setClock(clk, second.Add(100*time.Millisecond))
	early, earlyProc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "early", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	earlyProc.reply("done")
	eventually(t, "early settled", func() bool { return h.state(early).Status == StatusIdle })
	h.asleep(early)
	if rec := h.record(early); rec.CreatedUnixNano != second.Add(100*time.Millisecond).UnixNano() {
		t.Fatalf("record keeps the binding instant at nanosecond precision: %d", rec.CreatedUnixNano)
	}

	release := second.Add(900 * time.Millisecond)
	setClock(clk, release)
	if err := wsgit.RecordWorktreeRelease(wt, release); err != nil {
		t.Fatal(err)
	}
	h.runner.CheckoutReleased(wt, release, false)
	if c := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(early)]; !c.BoundBeforeRelease {
		t.Fatalf("a binding earlier in the release's second predates it: %+v", c)
	}
	starts := h.backend.startCount()
	if _, err := h.runner.Deliver(early, Delivery{From: "sender-reply", Text: "wake"}); err != nil {
		t.Fatal(err)
	}
	if got := tells.snapshot(); len(got) != 1 || !got[0].released {
		t.Fatalf("the same-second binding's wake must be refused as released: %+v", got)
	}
	if n := h.backend.startCount(); n != starts {
		t.Fatalf("a refused wake started %d processes", n-starts)
	}

	// A binding at the release instant itself is not before it.
	fresh, freshProc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "at release", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	if c := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(fresh)]; c.BoundBeforeRelease {
		t.Fatalf("a binding at the release instant is current: %+v", c)
	}
	freshProc.reply("done")
}

// TestCheckoutReleaseOccupancyCountsClosedLiveProcess: a close marks the
// conversation closed before it stops the process; occupancy still lists it,
// live, while the runner holds that process.
func TestCheckoutReleaseOccupancyCountsClosedLiveProcess(t *testing.T) {
	h, _, wt, _ := releaseHarness(t)
	child, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	// Hold the close inside its process stop: the conversation is already
	// marked closed (closeIf) while the runner still holds the process.
	g := proc.gateClose(t)
	closed := make(chan error, 1)
	go func() {
		_, err := h.runner.CloseConversation(child)
		closed <- err
	}()
	g.awaitEntered(t)
	got := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(child)]
	if got.Address == "" || !got.Live || got.State != AgentClosed {
		t.Fatalf("a closed conversation whose process is still held must be listed live: %+v", got)
	}
	g.release()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if got := boundByAddress(h.runner.ActiveChildrenInWorktree(wt)); len(got) != 0 {
		t.Fatalf("once its process stopped a closed conversation is not listed: %+v", got)
	}
}

// TestCheckoutReleaseSpawnByCwdTakesTheReservation: a spawn whose working
// directory lies in a pooled worktree takes that worktree's reservation, as
// agent.spawn(worktree) does, so a release or an acquisition holding it
// refuses the spawn and no child binds.
func TestCheckoutReleaseSpawnByCwdTakesTheReservation(t *testing.T) {
	h, _, wt, _ := releaseHarness(t)
	sub := filepath.Join(wt, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	lock, err := wsgit.WorktreeReleaseLock(context.Background(), wsgit.ExecRunner{}, wt)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	_, err = h.runner.AgentSpawn(h.leadCaller(), AgentSpawnRequest{Prompt: "by cwd", Tier: "small", Authority: "workdir-write", Cwd: sub})
	agentRefused(t, err, http.StatusConflict, "being released or re-acquired")
	if got := h.runner.ActiveChildrenInWorktree(wt); len(got) != 0 {
		t.Fatalf("a refused spawn left bound children: %+v", got)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	// With the reservation free the same spawn binds normally; a spawn on the
	// primary checkout takes no worktree reservation at all.
	id, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "by cwd", Tier: "small", Authority: "workdir-write", Cwd: sub})
	if c := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(id)]; c.Address == "" {
		t.Fatalf("the cwd child must be bound to the worktree")
	}
	proc.reply("done")
}

// TestCheckoutReleaseStartingConversationIsLive: from the release check until
// its backend start finishes, a waking conversation counts as live, so an
// acquisition never reuses its checkout under a start still under way.
func TestCheckoutReleaseStartingConversationIsLive(t *testing.T) {
	h, _, wt, _ := releaseHarness(t)
	child, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	proc.reply("done")
	eventually(t, "settled", func() bool { return h.state(child).Status == StatusIdle })
	h.asleep(child)
	if c := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(child)]; c.Live {
		t.Fatalf("an asleep child is not live: %+v", c)
	}
	starts := h.backend.startCount()
	g := h.backend.gateStarts(t)
	defer g.release()
	if _, err := h.runner.Deliver(child, Delivery{From: "sender", Text: "wake"}); err != nil {
		t.Fatal(err)
	}
	g.awaitEntered(t)
	if c := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(child)]; !c.Live {
		t.Fatalf("a conversation inside its backend start must count as live: %+v", c)
	}
	g.release()
	next := h.backend.waitProc(t, starts)
	next.nextSent(t)
	next.reply("done")
}

// TestCheckoutReleaseTokenRouteCancelsOnlyForRootLead: the daemon's
// worktree.released route ends the earlier bindings for any token caller but
// cancels turns only for a root lead; a spawned worker's token asking to
// cancel is answered cancel_refused, and the running turn is not
// interrupted.
func TestCheckoutReleaseTokenRouteCancelsOnlyForRootLead(t *testing.T) {
	h, clk, wt, _ := releaseHarness(t)
	lead := h.leadCaller()
	victim, victimProc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "running", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	_, workerProc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "worker", Tier: "medium", Authority: "workdir-write"})
	token := h.backend.start(h.backend.startCount() - 1).WSMCP.Env[EnvPiToken]
	if token == "" {
		t.Fatal("the worker has no capability token")
	}
	at := releaseAfterBindings(t, clk, wt)

	body, _ := json.Marshal(map[string]any{"worktree": wt, "released_at": at.Format(time.RFC3339Nano), "cancel_turns": true})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:12345"+RoutePrefix+"/"+tokenRoute+"/worktree.released", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.runner.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token route status = %d: %s", rec.Code, rec.Body.String())
	}
	var answer struct {
		Conversations []ReleasedConversation `json:"conversations"`
		CancelRefused bool                   `json:"cancel_refused"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if !answer.CancelRefused || len(answer.Conversations) != 1 || answer.Conversations[0].Cancelled {
		t.Fatalf("a worker token must not cancel: %+v", answer)
	}
	if n := procInterrupts(victimProc); n != 0 {
		t.Fatalf("a worker token interrupted %d turns", n)
	}
	if c := h.runner.conv(victim); c.released() == "" {
		t.Fatal("the notice still ends the earlier binding")
	}
	victimProc.reply("done")
	workerProc.reply("done")
}

// TestCheckoutReleaseSendsNoContinuationToReleasedBinding: a live process
// that outlived its checkout's release gets no compaction continuation and
// no compaction nudge.
func TestCheckoutReleaseSendsNoContinuationToReleasedBinding(t *testing.T) {
	h, clk, wt, _ := releaseHarness(t)
	child, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	proc.reply("done")
	eventually(t, "settled", func() bool { return h.state(child).Status == StatusIdle })
	h.runner.CheckoutReleased(wt, releaseAfterBindings(t, clk, wt), false)
	c := h.runner.conv(child)
	c.do(func() {
		// A continuation staged before the release: the compaction resume.
		c.compactResume, c.compactNote = true, "carry this forward"
		c.kick()
	})
	noTurnSent(t, proc, "compaction continuation to a released binding")
	c.do(func() {
		c.compactResume, c.compactNote = false, ""
		// A standalone compaction advisory waiting for the idle process.
		c.compactNudge = compactionNudge{text: "compact soon", level: 1, standalone: true}
		c.compactUsageFresh = true
		c.kickInput()
	})
	noTurnSent(t, proc, "compaction nudge to a released binding")
	if proc.isClosed() {
		t.Fatal("a release never stops a process")
	}
}

// TestCheckoutReleaseTellsMailClaimedBeforeHolderScan: after a restart, mail
// claimed for a child bound before a release, before the first holder scan
// ran, is refused to its sender at once: the conversation's release state is
// settled when it is loaded, not at a rejoin. The scan's deferred kick then
// tells no one twice and starts nothing.
func TestCheckoutReleaseTellsMailClaimedBeforeHolderScan(t *testing.T) {
	h, clk, wt, tells := releaseHarness(t)
	mail := newFakeMail()
	h.config.Mail = mail
	h.runner.Close()
	h.start()
	sleeper, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	proc.reply("done")
	eventually(t, "settled", func() bool { return h.state(sleeper).Status == StatusIdle })
	h.asleep(sleeper)
	key := h.keyOf(sleeper)
	releaseAfterBindings(t, clk, wt)

	holders := newFakeHolders()
	entered, gate := make(chan struct{}), make(chan struct{})
	var first, opened sync.Once
	holders.scanHook = func() { first.Do(func() { close(entered); <-gate }) }
	release := func() { opened.Do(func() { close(gate) }) }
	h.config.Holders = holders
	h.runner.Close()
	h.start()
	t.Cleanup(release) // runs before the runner's Close cleanup
	wait(t, "initial holder scan", entered)
	eventually(t, "mailbox watched", func() bool { return mail.watched(key) })
	starts := h.backend.startCount()
	mail.deliverFrom(key, "early-sender", "wake up")
	eventually(t, "sender told before the scan", func() bool { return len(tells.snapshot()) == 1 })
	tell := tells.snapshot()[0]
	if !tell.released || tell.from != "early-sender" || canonicalWorktreePath(tell.checkout) != canonicalWorktreePath(wt) {
		t.Fatalf("the refusal must reach the sender naming the worktree: %+v", tell)
	}
	release()
	eventually(t, "deferred kick refused", func() bool { return strings.Contains(h.state(sleeper).Error, "released") })
	time.Sleep(100 * time.Millisecond)
	if got := tells.snapshot(); len(got) != 1 {
		t.Fatalf("the sender is told once: %+v", got)
	}
	if n := h.backend.startCount(); n != starts {
		t.Fatalf("mail to a released child started %d processes", n-starts)
	}
}

// TestCheckoutReleaseReachesSubmoduleCwdAndServesFromMemory: a child whose
// working directory lies in a tracked submodule of a released worktree is
// bound to that worktree (the containment rule occupancy uses), and its wake
// is refused after a restart. The refusal is served from memory: the marker
// read when the conversation loaded, even once the file is gone.
func TestCheckoutReleaseReachesSubmoduleCwdAndServesFromMemory(t *testing.T) {
	h, clk, wt, tells := releaseHarness(t)
	module := filepath.Join(wt, "vendor", "mod")
	if err := os.MkdirAll(filepath.Join(module, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A submodule checkout's gitfile, pointing into the superproject's
	// modules directory.
	if err := os.WriteFile(filepath.Join(module, ".git"), []byte("gitdir: ../../.git/modules/mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	child, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "in module", Tier: "medium", Authority: "workdir-write", Cwd: filepath.Join(module, "src")})
	proc.reply("done")
	eventually(t, "settled", func() bool { return h.state(child).Status == StatusIdle })
	h.asleep(child)
	releaseAfterBindings(t, clk, wt)

	h.runner.Close()
	h.start()
	admin := wsgitAdminDir(t, wt)
	if err := os.Remove(filepath.Join(admin, wsgit.WorktreeReleasedFileName)); err != nil {
		t.Fatal(err)
	}
	starts := h.backend.startCount()
	if _, err := h.runner.Deliver(child, Delivery{From: "sender", Text: "wake"}); err != nil {
		t.Fatal(err)
	}
	got := tells.snapshot()
	if len(got) != 1 || !got[0].released || canonicalWorktreePath(got[0].checkout) != canonicalWorktreePath(wt) {
		t.Fatalf("the submodule child's wake must be refused naming the worktree: %+v", got)
	}
	if n := h.backend.startCount(); n != starts {
		t.Fatalf("a refused wake started %d processes", n-starts)
	}
}

// wsgitAdminDir is wt's linked-worktree admin directory.
func wsgitAdminDir(t *testing.T, wt string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-dir")
	cmd.Dir = wt
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// TestCheckoutReleaseTellsSendersOfMailHeldBeforeTheRelease: mail claimed
// before the first holder scan, for a child whose checkout is released only
// afterwards, waits with its sender kept beside it; the refusal the scan's
// deferred kick finds still reaches that sender, once.
func TestCheckoutReleaseTellsSendersOfMailHeldBeforeTheRelease(t *testing.T) {
	h, clk, wt, tells := releaseHarness(t)
	mail := newFakeMail()
	h.config.Mail = mail
	h.runner.Close()
	h.start()
	sleeper, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	proc.reply("done")
	eventually(t, "settled", func() bool { return h.state(sleeper).Status == StatusIdle })
	h.asleep(sleeper)
	key := h.keyOf(sleeper)

	holders := newFakeHolders()
	entered, gate := make(chan struct{}), make(chan struct{})
	var first, opened sync.Once
	holders.scanHook = func() { first.Do(func() { close(entered); <-gate }) }
	release := func() { opened.Do(func() { close(gate) }) }
	h.config.Holders = holders
	h.runner.Close()
	h.start()
	t.Cleanup(release)
	wait(t, "initial holder scan", entered)
	eventually(t, "mailbox watched", func() bool { return mail.watched(key) })
	starts := h.backend.startCount()
	mail.deliverFrom(key, "early-sender", "wake up")
	eventually(t, "mail held", func() bool { return len(h.record(sleeper).Mail) == 1 })
	if got := tells.snapshot(); len(got) != 0 {
		t.Fatalf("nothing refused the mail yet: %+v", got)
	}

	h.runner.CheckoutReleased(wt, releaseAfterBindings(t, clk, wt), false)
	release()
	eventually(t, "sender told by the deferred refusal", func() bool { return len(tells.snapshot()) == 1 })
	tell := tells.snapshot()[0]
	if !tell.released || tell.from != "early-sender" || canonicalWorktreePath(tell.checkout) != canonicalWorktreePath(wt) {
		t.Fatalf("the deferred refusal must reach the held mail's sender naming the worktree: %+v", tell)
	}
	time.Sleep(100 * time.Millisecond)
	if got := tells.snapshot(); len(got) != 1 {
		t.Fatalf("the sender is told once: %+v", got)
	}
	if n := h.backend.startCount(); n != starts {
		t.Fatalf("mail to a released child started %d processes", n-starts)
	}
}
