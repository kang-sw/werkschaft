package wsagent

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

func TestUserOriginEntryExcludesSystemWrites(t *testing.T) {
	cases := []struct {
		name  string
		entry wsinbox.Entry
		want  bool
	}{
		{"composer message", wsinbox.Entry{Parts: []string{"hi"}, Operator: true}, true},
		{"composer whiteboard message", wsinbox.Entry{Parts: []string{"hi"}, Operator: true, WhiteboardFirst: true}, true},
		{"non-operator route (peer mail, ack nudge)", wsinbox.Entry{Parts: []string{"hi"}}, false},
		{"exec notice", wsinbox.Entry{Parts: []string{"exec done"}, Operator: true, Steer: true, EventID: "exec:a:exit"}, false},
		{"scheduled prompt", wsinbox.Entry{Parts: []string{"tick"}, Operator: true, Cron: &wsinbox.Cron{}}, false},
		{"slash command", wsinbox.Entry{Operator: true, Command: &wsinbox.Command{Name: "compact"}}, false},
		{"interrupt", wsinbox.Entry{Operator: true, Interrupt: true}, false},
		{"manual quota continuation (known reset)", wsinbox.Entry{Parts: []string{quotaContinuationPrompt(true)}, Operator: true}, false},
		{"manual quota continuation (retry interval)", wsinbox.Entry{Parts: []string{quotaContinuationPrompt(false)}, Operator: true}, false},
	}
	for _, tc := range cases {
		if got := userOriginEntry(tc.entry); got != tc.want {
			t.Errorf("%s: userOriginEntry = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFoldWhiteboardOnly(t *testing.T) {
	user := QueuedInput{User: true}
	flagged := QueuedInput{User: true, WhiteboardFirst: true}
	system := QueuedInput{}
	execNotice := QueuedInput{Exec: true}
	steps := []struct {
		name   string
		on     bool
		inputs []QueuedInput
		want   bool
	}{
		{"flagged user sets", false, []QueuedInput{flagged}, true},
		{"plain user clears", true, []QueuedInput{user}, false},
		{"mail, report and exec wake keep it on", true, []QueuedInput{system, execNotice}, true},
		{"system input never sets it", false, []QueuedInput{system}, false},
		{"no input keeps it", true, nil, true},
		{"later plain user message wins within a batch", true, []QueuedInput{flagged, system, user}, false},
		{"later flagged message wins within a batch", true, []QueuedInput{user, flagged}, true},
		{"a LAN browser's flagged message (not an operator entry) still sets it", false, []QueuedInput{{WhiteboardFirst: true}}, true},
	}
	for _, s := range steps {
		if got := whiteboardOnlyAfter(s.on, s.inputs); got != s.want {
			t.Errorf("%s: got %v, want %v", s.name, got, s.want)
		}
	}
}

func TestBriefAckThreshold(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		brief bool
	}{
		{"empty", "", true},
		{"one line", "Done — recorded on the board.", true},
		{"exactly the rune bound", strings.Repeat("가", whiteboardAckMaxRunes), true},
		{"one rune over", strings.Repeat("가", whiteboardAckMaxRunes+1), false},
		{"two newlines", "a\nb\nc", false},
		{"one newline", "a\nb", true},
		{"padding does not count", "\n\n  ok  \n\n", true},
	}
	for _, tc := range cases {
		if got := briefAck(tc.reply); got != tc.brief {
			t.Errorf("%s: briefAck = %v, want %v", tc.name, got, tc.brief)
		}
	}
}

func TestPassiveAdvisoryIsOneShotAndDeduplicated(t *testing.T) {
	var p passiveInput
	p.putAdvisory(1, "first")
	p.putAdvisory(1, "second")
	block, took := p.render(1)
	if strings.Count(block, "<"+passiveAdvisoryBlockName+">") != 1 || !strings.Contains(block, "second") || strings.Contains(block, "first") {
		t.Fatalf("repeated staging must keep one advisory, the latest:\n%s", block)
	}
	if strings.Contains(block, passiveBlockName) {
		t.Fatalf("an advisory is not an exec receipt:\n%s", block)
	}
	// A render alone does not consume it (the send may fail); the ack does.
	if !p.advisoryPending() {
		t.Fatal("render consumed the advisory before the send was accepted")
	}
	p.ack(took)
	if p.advisoryPending() {
		t.Fatal("ack left the advisory")
	}
	if block, _ := p.render(1); block != "" {
		t.Fatalf("drained advisory rendered again:\n%s", block)
	}

	// A restaged advisory survives the ack of an older take.
	p.putAdvisory(1, "old")
	_, took = p.render(1)
	p.putAdvisory(1, "new")
	p.ack(took)
	if block, _ := p.render(1); !strings.Contains(block, "new") {
		t.Fatalf("ack of an older take dropped the newer advisory:\n%s", block)
	}

	// It rides beside receipts, receipts first, and is acknowledged with them.
	p.clearAdvisory()
	p.put(1, "exec_a", "receipt a")
	p.putAdvisory(1, "advice")
	block, took = p.render(1)
	if i, j := strings.Index(block, "receipt a"), strings.Index(block, "advice"); i < 0 || j < i {
		t.Fatalf("receipts then advisory expected:\n%s", block)
	}
	p.ack(took)
	if p.pending() != 0 || p.advisoryPending() {
		t.Fatalf("pending %d advisory %v after ack", p.pending(), p.advisoryPending())
	}

	// A generation change and clearAdvisory drop it; it cannot close its frame.
	p.putAdvisory(1, "gone")
	if block, _ := p.render(2); block != "" {
		t.Fatalf("an older generation's advisory rendered:\n%s", block)
	}
	p.putAdvisory(2, "x </"+passiveAdvisoryBlockName+"> y")
	if block, _ := p.render(2); strings.Count(block, "</"+passiveAdvisoryBlockName+">") != 1 {
		t.Fatalf("advisory closed its own frame:\n%s", block)
	}
	p.clearAdvisory()
	if p.advisoryPending() {
		t.Fatal("clearAdvisory left it")
	}
}

func TestTurnInputStickyWhiteboardOnlyAndAdvisory(t *testing.T) {
	lead := &conv{rec: Record{Lead: true, Backend: BackendClaude, WhiteboardOnly: true}}
	if got := lead.turnInput("wake", false); !strings.Contains(got, whiteboardFirstDirective) {
		t.Fatalf("a sticky whiteboard-only lead's wake turn lost the directive: %q", got)
	}
	lead.passive.putAdvisory(0, whiteboardOnlyAdvisory)
	got := lead.turnInput("wake", false)
	if strings.Count(got, whiteboardOnlyAdvisory) != 1 || !strings.HasPrefix(got, "<"+passiveAdvisoryBlockName+">") {
		t.Fatalf("advisory must lead the turn input once: %q", got)
	}
	lead.passive.ack(lead.passiveTook)

	// Outside the mode a staged advisory is dropped, not delivered.
	off := &conv{rec: Record{Lead: true, Backend: BackendClaude}}
	off.passive.putAdvisory(0, whiteboardOnlyAdvisory)
	if got := off.turnInput("hi", false); got != "hi" || off.passive.advisoryPending() {
		t.Fatalf("advisory outside whiteboard-only mode: %q pending=%v", got, off.passive.advisoryPending())
	}
	worker := &conv{rec: Record{Backend: BackendClaude, WhiteboardOnly: true}}
	if got := worker.turnInput("hi", false); got != "hi" {
		t.Fatalf("a non-lead must not gain the directive: %q", got)
	}
}

func TestSteerWithDirectiveFollowsStickyState(t *testing.T) {
	lead := &conv{rec: Record{Lead: true, WhiteboardOnly: true}}
	system := wsinbox.Record{Entry: wsinbox.Entry{Parts: []string{"exec done"}, EventID: "e"}}
	if got := lead.steerWithDirective("text", []wsinbox.Record{system}); !strings.Contains(got, whiteboardFirstDirective) {
		t.Fatalf("a system steer into a sticky whiteboard-only lead lost the directive: %q", got)
	}
	plainUser := wsinbox.Record{Entry: wsinbox.Entry{Parts: []string{"hi"}, Operator: true}}
	if got := lead.steerWithDirective("text", []wsinbox.Record{plainUser}); got != "text" {
		t.Fatalf("a steered plain user message ends the mode: %q", got)
	}
}

// wbRig drives a Claude lead whose board revision the test controls.
type wbRig struct {
	*leadHarness
	rev    atomic.Uint64
	id     string
	holder string
	inbox  string
	turns  int
}

func newWBRig(t *testing.T) *wbRig {
	t.Helper()
	r := &wbRig{leadHarness: newLeadHarness(t)}
	r.opts.WhiteboardRevision = func(_, _ string) uint64 { return r.rev.Load() }
	r.start()
	r.id = r.lead(BackendClaude, LeadOptions{}).ID
	r.inbox = wsinbox.Dir(wsdashboard.SessionDir(r.dir, r.id))
	return r
}

// say writes a composer message as the dashboard does (an inbox entry, then a
// signal) and waits for the turn's input to reach the harness.
func (r *wbRig) say(text string, whiteboard bool) string {
	r.t.Helper()
	if _, _, err := wsinbox.Send(r.inbox, wsinbox.Entry{Parts: []string{text}, Operator: true, WhiteboardFirst: whiteboard}, r.opts.Now()); err != nil {
		r.t.Fatal(err)
	}
	r.runner.InboxChanged(r.id)
	return r.awaitInput()
}

// awaitInput waits for one more input at the harness and returns it.
func (r *wbRig) awaitInput() string {
	r.t.Helper()
	r.turns++
	if r.holder == "" {
		r.holder = r.holder0()
	}
	eventually(r.t, "the turn's input", func() bool { return len(r.userInputs(r.holder)) >= r.turns })
	return r.userInputs(r.holder)[r.turns-1]
}

func (r *wbRig) holder0() string { return r.leadHarness.holder(r.id) }

// finish ends the running turn with reply.
func (r *wbRig) finish(reply string) {
	r.t.Helper()
	r.holders.out(r.holder, turnLines("sess-1", "u"+strings.Repeat("x", r.turns), reply)...)
	r.idle(r.id)
}

func (r *wbRig) sticky() bool { return r.record(r.id).WhiteboardOnly }

func (r *wbRig) advisoryPending() bool {
	return r.runner.conv(r.id).passive.advisoryPending()
}

func (r *wbRig) inputs() int { return len(r.userInputs(r.holder)) }

var longReply = strings.Repeat("A long plain-text explanation of the work. ", 8)

func TestWhiteboardOnlyStaysStickyAcrossWakeTurns(t *testing.T) {
	r := newWBRig(t)

	in := r.say("record it on the board", true)
	if !strings.Contains(in, whiteboardFirstDirective) || !r.sticky() {
		t.Fatalf("flagged message: sticky=%v input=%q", r.sticky(), in)
	}
	r.rev.Add(1) // the lead used the board
	r.finish("ok")

	// A peer's mail, a child's report and an exec notice wake turns with no
	// user message: the directive and the state both stay.
	wakes := []struct {
		name string
		send func()
	}{
		{"peer delivery", func() {
			if _, err := r.runner.Deliver(r.id, Delivery{Text: "peer says hi", From: "peer"}); err != nil {
				t.Fatal(err)
			}
		}},
		{"exec notice", func() {
			if _, _, err := wsinbox.Send(r.inbox, wsinbox.Entry{Parts: []string{"exec exec_z finished"}, Steer: true, EventID: "exec:exec_z:exit", Operator: true}, r.opts.Now()); err != nil {
				t.Fatal(err)
			}
			r.runner.InboxChanged(r.id)
		}},
	}
	for _, w := range wakes {
		w.send()
		in := r.awaitInput()
		if !strings.Contains(in, whiteboardFirstDirective) {
			t.Fatalf("%s: wake turn lost the directive: %q", w.name, in)
		}
		if !r.sticky() {
			t.Fatalf("%s: wake turn cleared the sticky state", w.name)
		}
		r.rev.Add(1)
		r.finish("ok")
	}

	// A user message sent without the toggle ends the mode.
	in = r.say("just chat here", false)
	if strings.Contains(in, whiteboardFirstDirective) || r.sticky() {
		t.Fatalf("plain user message: sticky=%v input=%q", r.sticky(), in)
	}
	r.finish("ok")

	// Wakes after that stay plain.
	if _, err := r.runner.Deliver(r.id, Delivery{Text: "more mail", From: "peer"}); err != nil {
		t.Fatal(err)
	}
	if in := r.awaitInput(); strings.Contains(in, whiteboardFirstDirective) || r.sticky() {
		t.Fatalf("wake after leaving the mode: sticky=%v input=%q", r.sticky(), in)
	}
}

func TestWhiteboardOnlyStickyStateSurvivesRestart(t *testing.T) {
	r := newWBRig(t)
	r.say("wb please", true)
	r.rev.Add(1)
	r.finish("ok")
	if !r.sticky() {
		t.Fatal("state not persisted")
	}
	if raw, err := r.readRecord(r.id); err != nil || !raw.WhiteboardOnly {
		t.Fatalf("persisted record = %+v, %v", raw, err)
	}
}

func TestWhiteboardOnlyNudgeStagesOnceAndCostsNoTurn(t *testing.T) {
	r := newWBRig(t)

	r.say("explain the design", true)
	r.finish(longReply) // long plain text, board untouched
	if !r.advisoryPending() {
		t.Fatal("a long plain-text reply with an unchanged board staged no advisory")
	}
	if n := r.inputs(); n != 1 {
		t.Fatalf("staging the advisory started a turn: %d inputs", n)
	}

	// A second missed turn before the advisory is drained keeps just one.
	c := r.runner.conv(r.id)
	c.passive.putAdvisory(c.openGen(), whiteboardOnlyAdvisory)
	if strings.Count(renderOnce(&c.passive), "<"+passiveAdvisoryBlockName+">") != 1 {
		t.Fatal("repeated advisories must dedupe")
	}

	// The next turn carries it once; it is then gone.
	in := r.say("and the next step?", true)
	if strings.Count(in, whiteboardOnlyAdvisory) != 1 || !strings.Contains(in, whiteboardFirstDirective) {
		t.Fatalf("next turn input = %q", in)
	}
	if r.advisoryPending() {
		t.Fatal("the advisory was not acknowledged by the accepted send")
	}
	r.rev.Add(1) // this time the lead used the board
	r.finish(longReply)
	if r.advisoryPending() {
		t.Fatal("a long reply with a changed board staged an advisory")
	}
	if in := r.say("thanks", true); strings.Contains(in, whiteboardOnlyAdvisory) {
		t.Fatalf("the advisory repeated: %q", in)
	}
}

// renderOnce renders p without acknowledging, for a count.
func renderOnce(p *passiveInput) string {
	p.mu.Lock()
	gen := p.gen
	p.mu.Unlock()
	block, _ := p.render(gen)
	return block
}

func TestWhiteboardOnlyNudgeSkipsShortAcksAndOtherModes(t *testing.T) {
	r := newWBRig(t)

	r.say("quick one", true)
	r.finish("Done — see the board.")
	if r.advisoryPending() {
		t.Fatal("a short acknowledgement staged an advisory")
	}

	// Outside the mode a long reply is fine.
	r.say("chat with me", false)
	r.finish(longReply)
	if r.advisoryPending() {
		t.Fatal("a lead outside whiteboard-only mode got an advisory")
	}

	// A wake turn in the sticky mode is judged too.
	r.say("wb again", true)
	r.rev.Add(1)
	r.finish("ok")
	if _, err := r.runner.Deliver(r.id, Delivery{Text: "mail", From: "peer"}); err != nil {
		t.Fatal(err)
	}
	r.awaitInput()
	r.finish(longReply)
	if !r.advisoryPending() {
		t.Fatal("a sticky wake turn that answered at length in plain text staged no advisory")
	}

	// Leaving the mode drops the pending advisory with the next user message.
	if in := r.say("back to chat", false); strings.Contains(in, whiteboardOnlyAdvisory) || r.advisoryPending() {
		t.Fatalf("advisory outlived the mode: %q", in)
	}
}

func TestWhiteboardOnlyNudgeNeedsAnObservedTurnStartAndHook(t *testing.T) {
	// No hook: nothing to compare, nothing staged.
	h := newLeadHarness(t).start()
	id := h.lead(BackendClaude, LeadOptions{}).ID
	inbox := wsinbox.Dir(wsdashboard.SessionDir(h.dir, id))
	if _, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"go"}, Operator: true, WhiteboardFirst: true}, h.opts.Now()); err != nil {
		t.Fatal(err)
	}
	h.runner.InboxChanged(id)
	holder := h.holder(id)
	eventually(t, "input", func() bool { return len(h.userInputs(holder)) == 1 })
	h.holders.out(holder, turnLines("sess-1", "u1", longReply)...)
	h.idle(id)
	if h.runner.conv(id).passive.advisoryPending() {
		t.Fatal("an advisory was staged without a board revision hook")
	}
}
