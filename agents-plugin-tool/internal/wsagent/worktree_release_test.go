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

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsgit"
	"github.com/kang-sw/devenv/internal/wsinbox"
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
	h, clk, wt, _ := releaseHarness(t)
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
	for _, id := range []string{failing, running, sleeper} {
		if st := h.state(id); st.Status != StatusClosed || st.Process || outcomes[AgentAddress(id)].State != AgentClosed {
			t.Fatalf("released child = %+v, notice = %+v", st, outcomes[AgentAddress(id)])
		}
		if _, err := h.runner.Deliver(id, Delivery{Text: "keep going"}); err != errClosed {
			t.Fatalf("Deliver(%s) to closed child = %v", id, err)
		}
	}
	if c := after[AgentAddress(running)]; c.State != AgentClosed || !c.Live {
		t.Fatalf("closed running process keeps the checkout occupied: %+v", c)
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
	one := 1
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

	if st := h.state(child); st.Status != StatusClosed {
		t.Fatalf("background process has a closed row: %+v", st)
	}
	_ = proc.Close()
	eventually(t, "checkout is free after physical process exit", func() bool {
		return len(h.runner.ActiveChildrenInWorktree(wt)) == 0
	})
}

// A release tells each held sender once. A prior wake-refusal notice owns
// its message even when another message from the same sender remains queued.
func TestCheckoutReleaseRefusesMailboxMail(t *testing.T) {
	h, clk, wt, tells := releaseHarness(t)
	mail := newFakeMail()
	h.config.Mail = mail
	h.runner.Close()
	h.start()
	child, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "still running", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	key := h.keyOf(child)
	eventually(t, "mailbox watched", func() bool { return mail.watched(key) })
	proc.failSteers(func(string) bool { return true })
	c := h.runner.conv(child)
	mail.deliverFrom(key, "peerreplyid", "first")
	eventually(t, "first message held", func() bool { return len(h.record(child).Mail) == 1 })
	c.do(func() { c.tellHeldSenders("", "temporary refusal") })
	mail.deliverFrom(key, "peerreplyid", "second")
	mail.deliverFrom(key, "peerreplyid", "third")
	eventually(t, "three messages held", func() bool { return len(h.record(child).Mail) == 3 })

	at := releaseAfterBindings(t, clk, wt)
	h.runner.CheckoutReleased(wt, at, false)
	h.runner.CheckoutReleased(wt, at, false)
	if got := tells.snapshot(); len(got) != 3 || got[0].released || !got[1].released || !got[2].released {
		t.Fatalf("notices = %+v, want one prior notice and two distinct release notices", got)
	}
	if rec := h.record(child); rec.ClosedAt == "" || len(rec.Mail) != 0 || rec.TurnOpen {
		t.Fatalf("released child kept active work: %+v", rec)
	}
	if proc.isClosed() {
		t.Fatal("release stopped the process")
	}
}

func TestCheckoutReleaseDirectDeliveryNoticesSkipCancelledInboxEntry(t *testing.T) {
	h, clk, wt, tells := releaseHarness(t)
	child, _ := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "running", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	for _, sender := range []string{"first", "cancelled", "last"} {
		if _, err := h.runner.Deliver(child, Delivery{From: sender, Text: sender}); err != nil {
			t.Fatal(err)
		}
	}
	c := h.runner.conv(child)
	entries, err := wsinbox.Held(c.inbox)
	if err != nil || len(entries) != 3 {
		t.Fatalf("held = %+v, %v", entries, err)
	}
	cancelled := false
	for _, entry := range entries {
		if len(entry.Entry.Parts) == 1 && entry.Entry.Parts[0] == "cancelled" {
			if _, err := wsinbox.Cancel(c.inbox, entry.Name); err != nil {
				t.Fatal(err)
			}
			cancelled = true
		}
	}
	if !cancelled {
		t.Fatal("pending cancellation was not found")
	}
	h.runner.CheckoutReleased(wt, releaseAfterBindings(t, clk, wt), false)
	if got := tells.snapshot(); len(got) != 2 || got[0].from == "cancelled" || got[1].from == "cancelled" {
		t.Fatalf("notices = %+v, want only the two queued messages", got)
	}
}

// Startup recognizes release by either root or CWD, preserving a sibling
// whose binding still admits a wake.
func TestCheckoutReleaseClosesByRootOrCwdAtStartup(t *testing.T) {
	h, clk, wt, _ := releaseHarness(t)
	sub := filepath.Join(wt, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	lead := h.leadCaller()
	byRoot, rootProc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "root", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	byCwd, cwdProc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "cwd", Tier: "medium", Authority: "workdir-write", Cwd: sub})
	wakeable, wakeableProc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "unreleased", Tier: "medium", Authority: "workdir-write"})
	for id, p := range map[string]*fakeProc{byRoot: rootProc, byCwd: cwdProc, wakeable: wakeableProc} {
		p.reply("retained reply")
		eventually(t, id+" settled", func() bool { return h.state(id).Status == StatusIdle })
		h.asleep(id)
	}
	releaseAfterBindings(t, clk, wt)
	h.runner.Close()
	h.start()
	starts := h.backend.startCount()
	for _, id := range []string{byRoot, byCwd} {
		if st := h.state(id); st.Status != StatusClosed || st.Process {
			t.Fatalf("released child = %+v", st)
		}
		if rec := h.record(id); rec.ClosedAt == "" || rec.LastTurn == nil || rec.LastTurn.Result.Message != "retained reply" {
			t.Fatalf("closed history = %+v", rec)
		}
	}
	if _, err := h.runner.Deliver(wakeable, Delivery{Text: "wake up"}); err != nil {
		t.Fatal(err)
	}
	if got := h.backend.waitProc(t, starts).nextSent(t); !strings.Contains(got, "wake up") {
		t.Fatalf("wakeable child's prompt = %q", got)
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
	h, clk, wt, _ := releaseHarness(t)
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
	if st := h.state(early); st.Status != StatusClosed {
		t.Fatalf("the earlier binding must close: %+v", st)
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
	agentRefused(t, err, http.StatusConflict, "currently being released")
	if got := h.runner.ActiveChildrenInWorktree(wt); len(got) != 0 {
		t.Fatalf("a refused spawn left bound children: %+v", got)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	// With the reservation free the same spawn binds normally.
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
	if st := h.state(victim); st.Status != StatusClosed {
		t.Fatal("the notice still closes the earlier binding")
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
	if st := h.state(child); st.Status != StatusClosed {
		t.Fatalf("released row = %+v", st)
	}
	noTurnSent(t, proc, "a continuation or nudge to a released binding")
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
	mail.deliverFrom(key, "early-sender", "wake up")
	h.start()
	t.Cleanup(release) // runs before the runner's Close cleanup
	wait(t, "initial holder scan", entered)
	starts := h.backend.startCount()
	eventually(t, "sender told before the scan", func() bool { return len(tells.snapshot()) == 1 })
	tell := tells.snapshot()[0]
	if !tell.released || tell.from != "early-sender" || canonicalWorktreePath(tell.checkout) != canonicalWorktreePath(wt) {
		t.Fatalf("the refusal must reach the sender naming the worktree: %+v", tell)
	}
	release()
	if st := h.state(sleeper); st.Status != StatusClosed {
		t.Fatalf("released row = %+v", st)
	}
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
	h, clk, wt, _ := releaseHarness(t)
	module := filepath.Join(wt, "vendor", "mod")
	if err := os.MkdirAll(filepath.Join(module, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A submodule checkout's gitfile, pointing at its admin directory (the
	// superproject's modules directory, which the create reservation
	// resolves).
	moduleAdmin := filepath.Join(t.TempDir(), "modules", "mod")
	if err := os.MkdirAll(moduleAdmin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, ".git"), []byte("gitdir: "+moduleAdmin+"\n"), 0o644); err != nil {
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
	if _, err := h.runner.Deliver(child, Delivery{Text: "wake"}); err != errClosed {
		t.Fatalf("released child accepted wake: %v", err)
	}
	if st := h.state(child); st.Status != StatusClosed {
		t.Fatalf("released row = %+v", st)
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

// TestCheckoutReleaseTokenRouteRefusesForgedNotice: a worker token's release
// notice acts only as far as the durable marker bears it out. With no marker
// (a checkout never released) the notice is refused, with or without an
// instant, and every binding stays current; one naming an instant other than
// the marker's is refused too. The true notice then ends the binding.
func TestCheckoutReleaseTokenRouteRefusesForgedNotice(t *testing.T) {
	h, clk, wt, _ := releaseHarness(t)
	lead := h.leadCaller()
	victim, victimProc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "running", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	_, workerProc := h.agentRunning(lead, AgentSpawnRequest{Prompt: "worker", Tier: "medium", Authority: "workdir-write"})
	token := h.backend.start(h.backend.startCount() - 1).WSMCP.Env[EnvPiToken]
	if token == "" {
		t.Fatal("the worker has no capability token")
	}
	forged := clk.Now().Add(time.Hour).Format(time.RFC3339Nano)
	for _, body := range []map[string]any{
		{"worktree": wt, "released_at": forged, "cancel_turns": true},
		{"worktree": wt},
	} {
		code, out := h.tokenCall(token, "worktree.released", body)
		if code != http.StatusConflict || !strings.Contains(out, "no release marker") {
			t.Fatalf("a notice for a never-released checkout must be refused: %d %s", code, out)
		}
	}
	if c := h.runner.conv(victim); c.released() != "" {
		t.Fatal("a forged notice must not end a binding")
	}
	if rel := h.runner.knownCheckoutRelease(canonicalWorktreePath(wt)); rel.ok {
		t.Fatalf("a forged notice must not enter the release memory: %+v", rel)
	}
	if c := boundByAddress(h.runner.ActiveChildrenInWorktree(wt))[AgentAddress(victim)]; c.BoundBeforeRelease {
		t.Fatalf("a forged notice must leave the binding current: %+v", c)
	}
	if n := procInterrupts(victimProc); n != 0 {
		t.Fatalf("a forged notice interrupted %d turns", n)
	}

	at := releaseAfterBindings(t, clk, wt)
	code, out := h.tokenCall(token, "worktree.released", map[string]any{"worktree": wt, "released_at": at.Add(time.Second).Format(time.RFC3339Nano)})
	if code != http.StatusConflict || !strings.Contains(out, "does not match the release marker") {
		t.Fatalf("a notice naming another instant must be refused: %d %s", code, out)
	}
	if c := h.runner.conv(victim); c.released() != "" {
		t.Fatal("a mismatched notice must not end a binding")
	}
	code, out = h.tokenCall(token, "worktree.released", map[string]any{"worktree": wt, "released_at": at.Format(time.RFC3339Nano)})
	if code != http.StatusOK {
		t.Fatalf("the true notice = %d %s", code, out)
	}
	if st := h.state(victim); st.Status != StatusClosed {
		t.Fatal("the true notice closes the earlier binding")
	}
	victimProc.reply("done")
	workerProc.reply("done")
}

// TestCheckoutReleaseMemoryOnlyMovesLater: the runner's release memory holds
// the latest release it knows of; an older notice or an older marker read
// never moves it back.
func TestCheckoutReleaseMemoryOnlyMovesLater(t *testing.T) {
	h, clk, wt, _ := releaseHarness(t)
	target := canonicalWorktreePath(wt)
	earlier := clk.Now()
	later := earlier.Add(time.Hour)
	h.runner.noteCheckoutRelease(target, later)
	h.runner.CheckoutReleased(wt, earlier, false)
	if got := h.runner.knownCheckoutRelease(target); !got.ok || !got.at.Equal(later) {
		t.Fatalf("an older notice moved the release back: %+v", got)
	}
	if err := wsgit.RecordWorktreeRelease(wt, earlier); err != nil {
		t.Fatal(err)
	}
	if got := h.runner.readCheckoutRelease(target); !got.ok || !got.at.Equal(later) {
		t.Fatalf("an older marker moved the release back: %+v", got)
	}
}

// TestCheckoutReleaseLostNoticeStillRefusesTheWake: a child's spawn found no
// release marker, and its checkout's release notice never reached the
// daemon. Its wake still reads the marker and is refused, telling the sender,
// with no process started: the spawn's "not released" was never remembered.
// That holds for a wake by Deliver, which reads the marker for a sleeping
// child, and for one by mailbox mail, which reaches the process start with no
// earlier check but memory: the start reads the marker itself. Each runs on
// its own runner, since any marker read puts the release in that runner's
// memory for every later check.
func TestCheckoutReleaseLostNoticeStillRefusesTheWake(t *testing.T) {
	for _, by := range []string{"deliver", "mail"} {
		t.Run(by, func(t *testing.T) {
			h, clk, wt, tells := releaseHarness(t)
			mail := newFakeMail()
			h.config.Mail = mail
			h.runner.Close()
			h.start()
			child, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "workdir-write", Worktree: wt})
			proc.reply("done")
			eventually(t, "settled", func() bool { return h.state(child).Status == StatusIdle })
			h.asleep(child)
			key := h.keyOf(child)
			eventually(t, "mailbox watched", func() bool { return mail.watched(key) })
			releaseAfterBindings(t, clk, wt) // no notice reaches the runner
			starts := h.backend.startCount()
			if by == "deliver" {
				if _, err := h.runner.Deliver(child, Delivery{From: "sender", Text: "wake"}); err != nil {
					t.Fatal(err)
				}
				if got := tells.snapshot(); len(got) != 1 {
					t.Fatalf("Deliver must tell its sender at once: %+v", got)
				}
			} else {
				mail.deliverFrom(key, "sender", "wake up")
				eventually(t, "sender told", func() bool { return len(tells.snapshot()) == 1 })
			}
			if got := tells.snapshot()[0]; !got.released || got.from != "sender" || canonicalWorktreePath(got.checkout) != canonicalWorktreePath(wt) {
				t.Fatalf("the wake must be refused to its sender naming the worktree: %+v", got)
			}
			eventually(t, "wake refused", func() bool { return strings.Contains(h.state(child).Error, "released") })
			if n := h.backend.startCount(); n != starts {
				t.Fatalf("a wake after a lost notice started %d processes", n-starts)
			}
		})
	}
}

// TestCheckoutReleaseNoticeDuringAStartRefusesItsTurn: a wake passed every
// release check and its backend is starting when the release notice lands.
// The turn is never sent: the last check, right before the send, sees the
// release. The input stays queued and the refusal shows.
func TestCheckoutReleaseNoticeDuringAStartRefusesItsTurn(t *testing.T) {
	h, clk, wt, _ := releaseHarness(t)
	child, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "workdir-write", Worktree: wt})
	proc.reply("done")
	eventually(t, "settled", func() bool { return h.state(child).Status == StatusIdle })
	h.asleep(child)
	starts := h.backend.startCount()
	g := h.backend.gateStarts(t)
	defer g.release()
	if _, err := h.runner.Deliver(child, Delivery{From: "sender", Text: "wake"}); err != nil {
		t.Fatal(err)
	}
	g.awaitEntered(t)
	at := releaseAfterBindings(t, clk, wt)
	released := make(chan struct{})
	go func() {
		h.runner.CheckoutReleased(wt, at, false)
		close(released)
	}()
	c := h.runner.conv(child)
	eventually(t, "release reaches the starting child", func() bool { return c.released() != "" })
	g.release()
	wait(t, "release closes the starting child", released)
	next := h.backend.waitProc(t, starts)
	noTurnSent(t, next, "a turn whose release landed during its start")
	eventually(t, "refusal recorded", func() bool { return strings.Contains(h.state(child).Error, "released") })
	if rec := h.record(child); rec.ClosedAt == "" || len(rec.Queued) != 0 {
		t.Fatalf("the refused turn must close and discard queued input: %+v", rec)
	}
	if next.isClosed() {
		t.Fatal("a release never stops a process")
	}
}

// TestCheckoutReleaseCrossingAutomaticCompactionInput: a release notice lands
// after the loop's release snapshot (kick, kickInput) and before an
// automatic compaction input reaches the backend. The input's last check,
// which shares the notice's fence (admitInput), drops it quietly: nothing is
// sent, no compaction starts, the nudge is not counted as issued, no error
// is recorded and the process keeps running.
func TestCheckoutReleaseCrossingAutomaticCompactionInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage func(c *conv) *compactRequest
		send  func(c *conv) bool
	}{
		{"nudge", func(c *conv) *compactRequest {
			c.compactNudge = compactionNudge{text: "compaction advisory", level: 1, standalone: true}
			c.compactUsageFresh = true
			return nil
		}, (*conv).sendCompactionNudge},
		{"command", func(c *conv) *compactRequest {
			c.compactPending = &compactRequest{resume: true, auto: true}
			return c.compactPending
		}, (*conv).compactIdle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, clk, wt, _ := releaseHarness(t)
			child, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "workdir-write", Worktree: wt})
			proc.reply("done")
			eventually(t, "settled", func() bool { return h.state(child).Status == StatusIdle })
			releaseAfterBindings(t, clk, wt)
			c := h.runner.conv(child)
			var sent bool
			var req *compactRequest
			c.do(func() {
				req = tc.stage(c)
				if c.released() != "" {
					t.Error("the snapshot must pass: the release has not landed yet")
					return
				}
				c.noteReleased(canonicalWorktreePath(wt))
				sent = tc.send(c)
				if c.compacting || c.compactCommand {
					t.Errorf("a refused compaction input started a compaction: compacting=%v command=%v", c.compacting, c.compactCommand)
				}
				if c.compactAdvised || tc.name == "nudge" && c.compactNudge.level != 1 {
					t.Errorf("a refused nudge counts as issued: advised=%v nudge=%+v", c.compactAdvised, c.compactNudge)
				}
			})
			if sent {
				t.Fatal("a compaction input crossing a release reports it was sent")
			}
			noTurnSent(t, proc, "a compaction input crossing a release")
			if req != nil && (req.err == nil || !strings.Contains(req.err.Error(), "released")) {
				t.Fatalf("the refused compaction request must say the release refused it: %v", req.err)
			}
			if st := h.state(child); st.Error != "" {
				t.Fatalf("a refused compaction input is no failure, yet it recorded %q", st.Error)
			}
			if proc.isClosed() {
				t.Fatal("a release never stops a process")
			}
		})
	}
}

// TestWorktreeReleaseRefusesPiSpawnsIntoIt: both Pi spawn paths, a worker's
// nested pi.spawn through the token route and a lead's root Pi spawn, take
// the worktree's release reservation like agent.spawn, so a release holding
// it refuses them and no child binds; once it is free they bind.
// TestCheckoutReleaseReachesALegacyRecordWithoutCwd: a record written before
// it carried a cwd runs in its root (Record.workDir). Rooted in a nested repo
// of a released worktree, such a child is bound to that worktree by the rule
// occupancy uses, so its wake after a restart is refused naming it.
func TestCheckoutReleaseReachesALegacyRecordWithoutCwd(t *testing.T) {
	h, clk, wt, _ := releaseHarness(t)
	nested := filepath.Join(wt, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = nested
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %v\n%s", err, out)
	}
	child, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "nested", Tier: "medium", Authority: "workdir-write", Worktree: nested})
	proc.reply("done")
	eventually(t, "settled", func() bool { return h.state(child).Status == StatusIdle })
	h.asleep(child)
	h.runner.Close()
	rec := h.record(child)
	rec.Cwd, rec.CreatedUnixNano = "", 0
	if err := writeRecord(wsdashboard.SessionDir(h.dir, child), rec); err != nil {
		t.Fatal(err)
	}
	releaseAfterBindings(t, clk, wt)
	h.start()
	starts := h.backend.startCount()
	if _, err := h.runner.Deliver(child, Delivery{Text: "wake"}); err != errClosed {
		t.Fatalf("released child accepted wake: %v", err)
	}
	if st := h.state(child); st.Status != StatusClosed {
		t.Fatalf("released row = %+v", st)
	}
	if n := h.backend.startCount(); n != starts {
		t.Fatalf("a refused wake started %d processes", n-starts)
	}
}
