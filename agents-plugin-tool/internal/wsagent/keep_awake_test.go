package wsagent

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// fakeAwake is the idle-sleep backend of the keep-awake tests: it records the
// assertion it holds and how often it was taken, and fails the next fail
// Acquire calls.
type fakeAwake struct {
	mu       sync.Mutex
	held     bool
	acquires int
	fail     int
}

func (f *fakeAwake) Acquire() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquires++
	if f.fail > 0 {
		f.fail--
		return errors.New("assertion refused")
	}
	f.held = true
	return nil
}

func (f *fakeAwake) Release() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held = false
	return nil
}

func (f *fakeAwake) state() (held bool, acquires int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held, f.acquires
}

func (f *fakeAwake) failNext(n int) {
	f.mu.Lock()
	f.fail = n
	f.mu.Unlock()
}

// setKeepAwake writes agent.keep_awake_during_turns to the global layer of
// opts ("" resets it), as config.tune or the settings page commits it.
func setKeepAwake(t *testing.T, opts wsconfig.Options, value string) {
	t.Helper()
	resolver := wsconfig.NewResolver(opts, nil, nil, nil)
	set := wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}
	var err error
	if value == "" {
		err = resolver.Unset(wsconfig.ItemAgentKeepAwakeDuringTurns, set)
	} else {
		err = resolver.Set(wsconfig.ItemAgentKeepAwakeDuringTurns, value, set)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// awakeHarness restarts h's runner over a fake idle-sleep backend with the
// setting on.
func awakeHarness(t *testing.T, h *harness) *fakeAwake {
	t.Helper()
	fake := &fakeAwake{}
	h.config.keepAwake = fake
	setKeepAwake(t, h.config.Config, "true")
	h.runner.Close()
	h.start()
	h.runner.ReloadGlobalConfig()
	return fake
}

// convQuiesce waits until the conversation's loop finished its step.
func convQuiesce(t *testing.T, r *Runner, id string) {
	t.Helper()
	c := r.conv(id)
	if c == nil {
		return
	}
	done := make(chan struct{})
	go func() { c.do(func() {}); close(done) }()
	wait(t, "the conversation's loop to finish its step", done)
}

// awaitAwake waits until the runner counts members live turns and the fake
// holds the assertion exactly when held.
func awaitAwake(t *testing.T, r *Runner, fake *fakeAwake, members int, held bool) {
	t.Helper()
	eventually(t, "the keep-awake state", func() bool {
		h, _ := fake.state()
		return r.awake.Members() == members && h == held
	})
}

func TestKeepAwakeFollowsWorkerTurns(t *testing.T) {
	h := newHarness(t, map[string]string{"chat": chatProfile})
	fake := awakeHarness(t, h)
	a, b := h.create("chat").ID, h.create("chat").ID
	convQuiesce(t, h.runner, a)
	if held, acq := fake.state(); held || acq != 0 || h.runner.awake.Members() != 0 {
		t.Fatalf("idle conversations hold the assertion: held=%v acquires=%d members=%d", held, acq, h.runner.awake.Members())
	}

	// First start.
	if _, err := h.runner.Send(a, "first"); err != nil {
		t.Fatal(err)
	}
	pa := h.backend.waitProc(t, 0)
	pa.nextSent(t)
	awaitAwake(t, h.runner, fake, 1, true)
	// Queued input during the turn adds nothing.
	if _, err := h.runner.Send(a, "queued"); err != nil {
		t.Fatal(err)
	}
	// An approval wait and a retry inside the turn keep it counted (the
	// lead test covers a tool call).
	pa.events <- Event{Approval: &ApprovalRequest{ID: "req-1", Tool: "Bash"}}
	pa.events <- Event{Retry: &wslive.Retry{Attempt: 1}}
	convQuiesce(t, h.runner, a)
	if n := h.runner.awake.Members(); n != 1 {
		t.Fatalf("members = %d during an approval wait and a retry", n)
	}

	// Overlap: a second conversation's turn takes no second assertion.
	if _, err := h.runner.Send(b, "other"); err != nil {
		t.Fatal(err)
	}
	pb := h.backend.waitProc(t, 1)
	pb.nextSent(t)
	awaitAwake(t, h.runner, fake, 2, true)

	// A's turn ends and its queued input runs as the next turn: still live.
	pa.events <- Event{ApprovalDone: "req-1"}
	pa.reply("done")
	pa.nextSent(t)
	convQuiesce(t, h.runner, a)
	if n := h.runner.awake.Members(); n != 2 {
		t.Fatalf("members = %d with the queued turn running", n)
	}
	// Interrupt: the harness ends the turn as aborted.
	if _, _, err := h.runner.Interrupt(a); err != nil {
		t.Fatal(err)
	}
	pa.events <- Event{TurnEnd: true, TurnReason: wslive.TurnAborted}
	awaitAwake(t, h.runner, fake, 1, true)
	// EOF mid-turn: the last live turn is gone.
	pb.exit()
	awaitAwake(t, h.runner, fake, 0, false)

	// A turn the harness starts on its own counts, and its end releases.
	pa.events <- Event{TurnStart: true}
	awaitAwake(t, h.runner, fake, 1, true)
	pa.events <- Event{TurnEnd: true}
	awaitAwake(t, h.runner, fake, 0, false)
	if _, acq := fake.state(); acq != 2 {
		t.Fatalf("acquires = %d, want one per activation (2)", acq)
	}
	// Duplicate and late ends change nothing.
	pa.events <- Event{TurnEnd: true}
	pa.events <- Event{TurnEnd: true}
	convQuiesce(t, h.runner, a)
	if held, acq := fake.state(); held || acq != 2 || h.runner.awake.Members() != 0 {
		t.Fatalf("late ends: held=%v acquires=%d members=%d", held, acq, h.runner.awake.Members())
	}
}

func TestKeepAwakeSpawnedWorkerAndNestedChild(t *testing.T) {
	h, _ := childHarness(t)
	fake := awakeHarness(t, h)
	w := h.spawnWorker("medium")
	wp := h.backend.waitProc(t, 0)
	wp.nextSent(t)
	awaitAwake(t, h.runner, fake, 1, true)
	child := h.spawnChild(h.token(0), "small", "child work")
	cp := h.backend.waitProc(t, 1)
	cp.nextSent(t)
	awaitAwake(t, h.runner, fake, 2, true)
	wp.reply("worker interim")
	eventually(t, "the worker idle", func() bool { return h.state(w.ID).Status == StatusIdle })
	awaitAwake(t, h.runner, fake, 1, true)
	cp.reply("child done")
	eventually(t, "the child idle", func() bool { return h.state(child.ID).Status == StatusIdle })
	// The child's report wakes the worker for a turn of its own.
	wp.nextSent(t)
	awaitAwake(t, h.runner, fake, 1, true)
	wp.reply("worker done")
	awaitAwake(t, h.runner, fake, 0, false)
}

// TestKeepAwakeSendHasMembershipAndFailedSendRollsBack: an admitted turn is
// a member while its send is still in the transport, and a send that fails
// (and its one automatic retry) leaves no membership behind.
func TestKeepAwakeSendHasMembershipAndFailedSendRollsBack(t *testing.T) {
	h := newHarness(t, map[string]string{"chat": chatProfile})
	fake := awakeHarness(t, h)
	id := h.create("chat").ID
	h.backend.setFailSends(-1)
	g := h.backend.gateSends(t)
	if _, err := h.runner.Send(id, "first"); err != nil {
		t.Fatal(err)
	}
	g.awaitEntered(t)
	if n := h.runner.awake.Members(); n != 1 {
		t.Fatalf("members = %d while the send is in the transport", n)
	}
	g.release()
	eventually(t, "the failed turn", func() bool { st := h.state(id); return st.Status == StatusIdle && st.Error != "" })
	awaitAwake(t, h.runner, fake, 0, false)
}

// TestKeepAwakeStandaloneSend: a standalone prompt (a compaction note, a
// restore) is a member during its send; a failed one is rolled back at the
// end of the loop step, and a successful one carries into its turn.
func TestKeepAwakeStandaloneSend(t *testing.T) {
	h := newHarness(t, map[string]string{"chat": chatProfile})
	fake := awakeHarness(t, h)
	id := h.create("chat").ID
	if _, err := h.runner.Send(id, "first"); err != nil {
		t.Fatal(err)
	}
	p := h.backend.waitProc(t, 0)
	p.nextSent(t)
	p.reply("done")
	awaitAwake(t, h.runner, fake, 0, false)
	c := h.runner.conv(id)

	during := -1
	c.do(func() {
		err := c.sendStandalonePrompt(false, func() error {
			during = c.r.awake.Members()
			return errors.New("broken pipe")
		})
		if err == nil {
			t.Error("the failed send reported success")
		}
	})
	if during != 1 {
		t.Fatalf("members = %d during the standalone send", during)
	}
	awaitAwake(t, h.runner, fake, 0, false)

	c.do(func() {
		if err := c.sendStandalonePrompt(false, func() error { return nil }); err != nil {
			t.Error(err)
		}
		c.compactInternalTurn = true
		c.beginTurn(false)
	})
	awaitAwake(t, h.runner, fake, 1, true)
	p.events <- Event{TurnEnd: true}
	awaitAwake(t, h.runner, fake, 0, false)
}

// TestKeepAwakeCompactCommands: an ordinary /compact whose send fails
// releases at once (its early return skips the turn end), and a standalone
// native compaction counts until its command lifecycle settles, with no
// separate prompt turn event.
func TestKeepAwakeCompactCommands(t *testing.T) {
	h := newHarness(t, map[string]string{"chat": chatProfile})
	fake := awakeHarness(t, h)
	id := h.create("chat").ID
	if _, err := h.runner.Send(id, "first"); err != nil {
		t.Fatal(err)
	}
	p := h.backend.waitProc(t, 0)
	p.nextSent(t)
	p.reply("done")
	awaitAwake(t, h.runner, fake, 0, false)

	// Ordinary /compact from the page, whose send fails.
	h.backend.setFailSends(1)
	g := h.backend.gateSends(t)
	inbox := wsinbox.Dir(wsdashboard.SessionDir(h.dir, id))
	if _, _, err := wsinbox.Send(inbox, wsinbox.Entry{Command: &wsinbox.Command{Name: "compact"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	h.runner.InboxChanged(id)
	g.awaitEntered(t)
	if n := h.runner.awake.Members(); n != 1 {
		t.Fatalf("members = %d while /compact is in the transport", n)
	}
	g.release()
	eventually(t, "the failed compaction", func() bool { return h.state(id).Error != "" })
	awaitAwake(t, h.runner, fake, 0, false)

	// Standalone native compaction settled by its command's end.
	c := h.runner.conv(id)
	for _, settle := range []Event{{TurnEnd: true}, {CommandError: "compaction refused"}} {
		c.do(func() {
			c.compactPending = &compactRequest{}
			c.kick()
		})
		if got := p.nextSent(t); got != "/compact" {
			t.Fatalf("the compaction command = %q", got)
		}
		awaitAwake(t, h.runner, fake, 1, true)
		p.events <- settle
		awaitAwake(t, h.runner, fake, 0, false)
	}
}

func TestKeepAwakeCloseAndShutdown(t *testing.T) {
	h := newHarness(t, map[string]string{"chat": chatProfile})
	fake := awakeHarness(t, h)
	a, b := h.create("chat").ID, h.create("chat").ID
	for i, id := range []string{a, b} {
		if _, err := h.runner.Send(id, "go"); err != nil {
			t.Fatal(err)
		}
		h.backend.waitProc(t, i).nextSent(t)
	}
	awaitAwake(t, h.runner, fake, 2, true)
	if _, err := h.runner.CloseConversation(a); err != nil {
		t.Fatal(err)
	}
	awaitAwake(t, h.runner, fake, 1, true)
	// Shutdown with a live turn drains the backend before Close returns.
	h.runner.Close()
	if held, _ := fake.state(); held || h.runner.awake.Members() != 0 {
		t.Fatalf("after shutdown: held=%v members=%d", held, h.runner.awake.Members())
	}
	_, before := fake.state()
	h.runner.awake.SetMember(new(int), true)
	h.runner.ReloadGlobalConfig()
	time.Sleep(30 * time.Millisecond)
	if held, acq := fake.state(); held || acq != before {
		t.Fatalf("a late change after shutdown acquired: held=%v acquires=%d", held, acq)
	}
}

// TestKeepAwakeSettingChangesApplyToLiveTurns: enable, disable and reset
// reconcile against a turn already running, overlapping reloads publish the
// newest committed value, and a failed acquisition leaves turns usable and
// is retried at the next activation.
func TestKeepAwakeSettingChangesApplyToLiveTurns(t *testing.T) {
	h := newHarness(t, map[string]string{"chat": chatProfile})
	fake := awakeHarness(t, h)
	setKeepAwake(t, h.config.Config, "false")
	h.runner.ReloadGlobalConfig()
	id := h.create("chat").ID
	if _, err := h.runner.Send(id, "go"); err != nil {
		t.Fatal(err)
	}
	p := h.backend.waitProc(t, 0)
	p.nextSent(t)
	awaitAwake(t, h.runner, fake, 1, false)

	setKeepAwake(t, h.config.Config, "true")
	h.runner.ReloadGlobalConfig()
	awaitAwake(t, h.runner, fake, 1, true)
	setKeepAwake(t, h.config.Config, "false")
	h.runner.ReloadGlobalConfig()
	awaitAwake(t, h.runner, fake, 1, false)
	setKeepAwake(t, h.config.Config, "true")
	h.runner.ReloadGlobalConfig()
	awaitAwake(t, h.runner, fake, 1, true)
	setKeepAwake(t, h.config.Config, "") // reset: builtin false
	h.runner.ReloadGlobalConfig()
	awaitAwake(t, h.runner, fake, 1, false)

	// Overlapping reloads around a committed change and turn boundaries.
	setKeepAwake(t, h.config.Config, "true")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h.runner.ReloadGlobalConfig() }()
	}
	p.reply("done")
	setKeepAwake(t, h.config.Config, "false")
	wg.Add(1)
	go func() { defer wg.Done(); h.runner.ReloadGlobalConfig() }()
	wg.Wait()
	h.runner.ReloadGlobalConfig()
	awaitAwake(t, h.runner, fake, 0, false)
	if h.runner.awake.Enabled() {
		t.Fatal("the newest committed value (false) did not win")
	}

	// A refused assertion: the turn runs, nothing retries within the
	// activation, and the next activation tries again.
	_, before := fake.state()
	fake.failNext(1)
	setKeepAwake(t, h.config.Config, "true")
	h.runner.ReloadGlobalConfig()
	if _, err := h.runner.Send(id, "again"); err != nil {
		t.Fatal(err)
	}
	p.nextSent(t)
	eventually(t, "the refused attempt", func() bool { _, acq := fake.state(); return acq == before+1 })
	if st := h.state(id); st.Status != StatusRunning {
		t.Fatalf("the turn after a refused assertion: %+v", st)
	}
	h.runner.ReloadGlobalConfig()
	time.Sleep(30 * time.Millisecond)
	if held, acq := fake.state(); held || acq != before+1 {
		t.Fatalf("retried within one activation: held=%v acquires=%d (before %d)", held, acq, before)
	}
	p.reply("done")
	awaitAwake(t, h.runner, fake, 0, false)
	if _, err := h.runner.Send(id, "third"); err != nil {
		t.Fatal(err)
	}
	p.nextSent(t)
	awaitAwake(t, h.runner, fake, 1, true)
}

// TestKeepAwakeStartupReadsTheCommittedValue: a disable a writer committed
// while no daemon could be notified is what the next start reads, and a
// sibling key written afterwards keeps the stored value.
func TestKeepAwakeStartupReadsTheCommittedValue(t *testing.T) {
	opts := testConfig(t)
	setKeepAwake(t, opts, "true")
	r := NewRunner(Options{Config: opts, keepAwake: &fakeAwake{}})
	defer r.Close()
	r.ReloadGlobalConfig()
	if !r.awake.Enabled() {
		t.Fatal("the stored true was not read")
	}
	// A sibling override written through the config writer keeps the key.
	sibling := wsconfig.NewResolver(opts, nil, nil, nil)
	if err := sibling.Set(wsconfig.ItemAgentChildWaitingDays, "3", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	overrides, err := wsconfig.GlobalOverrides(opts)
	if err != nil || overrides[wsconfig.ItemAgentKeepAwakeDuringTurns] != "true" {
		t.Fatalf("after a sibling write: %v, %v", overrides, err)
	}
	reopened := NewRunner(Options{Config: opts, keepAwake: &fakeAwake{}})
	defer reopened.Close()
	reopened.ReloadGlobalConfig()
	if !reopened.awake.Enabled() {
		t.Fatal("the reopened runner lost the persisted value")
	}
	// A disable committed with no endpoint to notify.
	setKeepAwake(t, opts, "false")
	next := NewRunner(Options{Config: opts, keepAwake: &fakeAwake{}})
	defer next.Close()
	next.ReloadGlobalConfig()
	if next.awake.Enabled() {
		t.Fatal("startup did not reconcile the newer false")
	}
	// An unparseable value is the builtin false.
	setKeepAwake(t, opts, "maybe")
	next.ReloadGlobalConfig()
	if next.awake.Enabled() {
		t.Fatal("an invalid stored value enabled the assertion")
	}
}

// TestKeepAwakeLeadTurnsAndRejoin: a hosted lead's turn counts; a daemon
// restart rejoins a still-running turn and counts it again, while a turn that
// finished during the downtime, and duplicate or late end lines after the
// rejoin, leave the count at zero.
func TestKeepAwakeLeadTurnsAndRejoin(t *testing.T) {
	h := newLeadHarness(t)
	fake := &fakeAwake{}
	h.opts.keepAwake = fake
	setKeepAwake(t, h.opts.Config, "true")
	start := func() {
		h.start()
		h.runner.ReloadGlobalConfig()
	}
	start()
	id, holder := h.runLead("one")
	awaitAwake(t, h.runner, fake, 1, true)
	h.holders.out(holder, turnLines("sess-1", "u1", "first")...)
	h.idle(id)
	awaitAwake(t, h.runner, fake, 0, false)

	// A turn running across a restart.
	h.send(id, "two")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), toolUseLine("u2", "toolu_1"))
	h.sync(id, holder)
	awaitAwake(t, h.runner, fake, 1, true)
	h.runner.Close()
	if held, _ := fake.state(); held {
		t.Fatal("the closed runner left the assertion held")
	}
	h.holders.restart()
	start()
	eventually(t, "the running turn to be back", func() bool { st := h.state(id); return st.Process && st.Status == StatusRunning })
	awaitAwake(t, h.runner, fake, 1, true)

	// The turn finishes while no daemon runs.
	h.runner.Close()
	h.holders.restart()
	h.holders.out(holder, toolResultLine("toolu_1"), replyLine("u3", "second"), resultLine(), stateLine(HarnessIdle))
	_, before := fake.state()
	start()
	eventually(t, "the finished turn to be replayed", func() bool { st := h.state(id); return st.Process && st.Status == StatusIdle })
	h.quiesce(id)
	if held, acq := fake.state(); held || acq != before || h.runner.awake.Members() != 0 {
		t.Fatalf("a turn finished during downtime: held=%v acquires=%d (before %d) members=%d", held, acq, before, h.runner.awake.Members())
	}
	// Duplicate and late end lines after the rejoin.
	h.holders.out(holder, resultLine(), stateLine(HarnessIdle), resultLine())
	h.sync(id, holder)
	if held, acq := fake.state(); held || acq != before || h.runner.awake.Members() != 0 {
		t.Fatalf("late end lines: held=%v acquires=%d members=%d", held, acq, h.runner.awake.Members())
	}
}
