package wsagent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

func TestUserOriginEntryExcludesSystemWrites(t *testing.T) {
	cases := []struct {
		name  string
		entry wsinbox.Entry
		want  bool
	}{
		{"composer message", wsinbox.Entry{Parts: []string{"hi"}, Operator: true, User: true}, true},
		{"legacy operator composer message", wsinbox.Entry{Parts: []string{"hi"}, Operator: true}, true},
		{"LAN browser message", wsinbox.Entry{Parts: []string{"hi"}, Operator: false, User: true}, true},
		{"LAN browser whiteboard message", wsinbox.Entry{Parts: []string{"hi"}, Operator: false, User: true, WhiteboardFirst: true}, true},
		{"composer whiteboard message", wsinbox.Entry{Parts: []string{"hi"}, Operator: true, WhiteboardFirst: true}, true},
		{"non-operator route (peer mail, ack nudge)", wsinbox.Entry{Parts: []string{"hi"}}, false},
		{"exec notice", wsinbox.Entry{Parts: []string{"exec done"}, Operator: true, User: true, Steer: true, EventID: "exec:a:exit"}, false},
		{"scheduled prompt", wsinbox.Entry{Parts: []string{"tick"}, Operator: true, User: true, Cron: &wsinbox.Cron{}}, false},
		{"slash command", wsinbox.Entry{Operator: true, User: true, Command: &wsinbox.Command{Name: "compact"}}, false},
		{"interrupt", wsinbox.Entry{Operator: true, User: true, Interrupt: true}, false},
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
	p.putAdvisory(1, "first", 0, false)
	p.putAdvisory(1, "second", 0, false)
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
	p.putAdvisory(1, "old", 0, false)
	_, took = p.render(1)
	p.putAdvisory(1, "new", 0, false)
	p.ack(took)
	if block, _ := p.render(1); !strings.Contains(block, "new") {
		t.Fatalf("ack of an older take dropped the newer advisory:\n%s", block)
	}

	// It rides beside receipts, receipts first, and is acknowledged with them.
	p.clearAdvisory()
	p.put(1, "exec_a", "receipt a")
	p.putAdvisory(1, "advice", 0, false)
	block, took = p.render(1)
	if i, j := strings.Index(block, "receipt a"), strings.Index(block, "advice"); i < 0 || j < i {
		t.Fatalf("receipts then advisory expected:\n%s", block)
	}
	p.ack(took)
	if p.pending() != 0 || p.advisoryPending() {
		t.Fatalf("pending %d advisory %v after ack", p.pending(), p.advisoryPending())
	}

	// A generation change and clearAdvisory drop it; it cannot close its frame.
	p.putAdvisory(1, "gone", 0, false)
	if block, _ := p.render(2); block != "" {
		t.Fatalf("an older generation's advisory rendered:\n%s", block)
	}
	p.putAdvisory(2, "x </"+passiveAdvisoryBlockName+"> y", 0, false)
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
	lead.passive.putAdvisory(0, whiteboardOnlyAdvisory, 0, false)
	got := lead.turnInput("wake", false)
	if strings.Count(got, whiteboardOnlyAdvisory) != 1 || !strings.HasPrefix(got, "<"+passiveAdvisoryBlockName+">") {
		t.Fatalf("advisory must lead the turn input once: %q", got)
	}
	lead.passive.ack(lead.passiveTook)

	// Outside the mode a staged advisory is dropped, not delivered.
	off := &conv{rec: Record{Lead: true, Backend: BackendClaude}}
	off.passive.putAdvisory(0, whiteboardOnlyAdvisory, 0, false)
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
	c.passive.putAdvisory(c.openGen(), whiteboardOnlyAdvisory, r.rev.Load(), true)
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

type spyRevisionKeyStore struct {
	rootCalls atomic.Int32
}

func (s *spyRevisionKeyStore) Mint(string) (string, error) { return "k", nil }
func (s *spyRevisionKeyStore) Ensure(string, string) error { return nil }
func (s *spyRevisionKeyStore) Root(string) (string, bool) {
	s.rootCalls.Add(1)
	return "/should/not/be/called", true
}

func TestBoardRevisionZeroIO(t *testing.T) {
	spy := &spyRevisionKeyStore{}
	var gotRoot, gotKey string
	r := &Runner{opts: Options{
		Keys: spy,
		WhiteboardRevision: func(root, key string) uint64 {
			gotRoot, gotKey = root, key
			return 42
		},
	}}

	rootDir := t.TempDir()
	unclean := rootDir + string(filepath.Separator) + "."
	c := &conv{r: r, rec: Record{Lead: true, Root: unclean, WSKey: "ws-1"}}
	rev, ok := c.boardRevision()
	if !ok || rev != 42 {
		t.Fatalf("boardRevision() = (%d, %v), want (42, true)", rev, ok)
	}
	if spy.rootCalls.Load() != 0 {
		t.Fatalf("Keys.Root called %d times, want 0", spy.rootCalls.Load())
	}
	if gotRoot != filepath.Clean(rootDir) || gotKey != "ws-1" {
		t.Fatalf("WhiteboardRevision args = (%q, %q), want (%q, %q)", gotRoot, gotKey, filepath.Clean(rootDir), "ws-1")
	}

	for _, tc := range []struct {
		name string
		conv *conv
	}{
		{"nil conv", nil},
		{"nil runner", &conv{rec: Record{Lead: true, Root: rootDir, WSKey: "ws-1"}}},
		{"non-lead", &conv{r: r, rec: Record{Lead: false, Root: rootDir, WSKey: "ws-1"}}},
		{"empty root", &conv{r: r, rec: Record{Lead: true, Root: "", WSKey: "ws-1"}}},
		{"empty key", &conv{r: r, rec: Record{Lead: true, Root: rootDir, WSKey: ""}}},
	} {
		if rev, ok := tc.conv.boardRevision(); ok || rev != 0 {
			t.Errorf("%s: boardRevision() = (%d, %v), want (0, false)", tc.name, rev, ok)
		}
	}
	if spy.rootCalls.Load() != 0 {
		t.Fatalf("Keys.Root called %d times on guard checks, want 0", spy.rootCalls.Load())
	}
}

func TestLANBrowserCanEnableAndClearStickyWhiteboardOnly(t *testing.T) {
	r := newWBRig(t)
	sendLAN := func(text string, wb bool) string {
		t.Helper()
		if _, _, err := wsinbox.Send(r.inbox, wsinbox.Entry{
			Parts:           []string{text},
			Operator:        false,
			User:            true,
			WhiteboardFirst: wb,
		}, r.opts.Now()); err != nil {
			t.Fatal(err)
		}
		r.runner.InboxChanged(r.id)
		return r.awaitInput()
	}

	in := sendLAN("lan wb on", true)
	if !strings.Contains(in, whiteboardFirstDirective) || !r.sticky() {
		t.Fatalf("LAN flagged message: sticky=%v input=%q", r.sticky(), in)
	}
	r.rev.Add(1)
	r.finish("ok")

	// Non-user wake keeps sticky mode enabled.
	if _, err := r.runner.Deliver(r.id, Delivery{Text: "peer says hi", From: "peer"}); err != nil {
		t.Fatal(err)
	}
	if in := r.awaitInput(); !strings.Contains(in, whiteboardFirstDirective) || !r.sticky() {
		t.Fatalf("non-user wake cleared sticky mode: sticky=%v input=%q", r.sticky(), in)
	}
	r.rev.Add(1)
	r.finish("ok")

	// Subsequent LAN plain message (Operator:false, User:true) clears sticky mode.
	in = sendLAN("lan plain", false)
	if strings.Contains(in, whiteboardFirstDirective) || r.sticky() {
		t.Fatalf("LAN plain message failed to clear sticky mode: sticky=%v input=%q", r.sticky(), in)
	}
	r.finish("ok")

	// Runner.Send and POST /agents/<id>/send stamp User:true; spawn/Deliver leave User:false.
	if _, err := r.runner.Send(r.id, "runner send"); err != nil {
		t.Fatal(err)
	}
	r.awaitInput()
	r.finish("ok")

	handler := r.runner.Handler()
	req := httptest.NewRequest(http.MethodPost, "/agents/"+r.id+"/send", strings.NewReader(`{"text":"http send"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /agents/%s/send = %d: %s", r.id, rec.Code, rec.Body.String())
	}
	r.awaitInput()
	r.finish("ok")

	// Verify drained entries from Runner.Send and HTTP send have User:true, while Deliver had User:false.
	drained, err := wsinbox.Drained(r.inbox)
	if err != nil {
		t.Fatal(err)
	}
	userByBody := map[string]bool{}
	for _, d := range drained {
		userByBody[d.Entry.Text()] = d.Entry.User
	}
	for _, d := range drained {
		if len(d.Entry.Parts) == 1 && (d.Entry.Parts[0] == "runner send" || d.Entry.Parts[0] == "http send") && !d.Entry.User {
			t.Fatalf("expected User=true for %q: %+v", d.Entry.Parts[0], d.Entry)
		}
	}
	_ = userByBody
}

func TestIdleBoardRevisionChangeDropsStaleAdvisory(t *testing.T) {
	var rev atomic.Uint64
	rev.Store(5)
	r := &Runner{opts: Options{
		WhiteboardRevision: func(_, _ string) uint64 { return rev.Load() },
	}}
	c := &conv{r: r, rec: Record{Lead: true, Backend: BackendClaude, Root: t.TempDir(), WSKey: "k1", WhiteboardOnly: true}}
	c.snapshotBoardRevision()
	c.stageWhiteboardNudge("", longReply)
	if !c.passive.advisoryPending() {
		t.Fatal("expected staged advisory")
	}

	// Same revision delivers the advisory once.
	got := c.turnInput("still stale", false)
	if !strings.Contains(got, whiteboardOnlyAdvisory) {
		t.Fatalf("expected advisory at unchanged revision: %q", got)
	}
	c.passive.ack(c.passiveTook)
	c.passiveTook = nil

	// Stage again at rev 5, then advance revision while idle before turnInput.
	c.snapshotBoardRevision()
	c.stageWhiteboardNudge("", longReply)
	if !c.passive.advisoryPending() {
		t.Fatal("expected staged advisory")
	}
	rev.Store(6)
	got = c.turnInput("after idle board update", false)
	if strings.Contains(got, whiteboardOnlyAdvisory) || c.passive.advisoryPending() {
		t.Fatalf("stale advisory was not dropped after idle revision change: %q pending=%v", got, c.passive.advisoryPending())
	}
	if !strings.Contains(got, whiteboardFirstDirective) {
		t.Fatalf("expected sticky directive to remain: %q", got)
	}
}

func TestStandaloneCompactionResumeAndRestartRestoreCarryDirectiveAndClearAdvisory(t *testing.T) {
	t.Run("non-durable resume and restart restore", func(t *testing.T) {
		backend := &wsCompactBackend{}
		r := NewRunner(Options{
			Dir: t.TempDir(),
			Backends: map[string]BackendRegistration{
				BackendClaude: {
					Backend: backend,
				},
			},
			Keys:          &spyRevisionKeyStore{},
			LeadKeys:      &fakeLeadKeys{},
			Holders:       newFakeHolders(),
			RestoreBundle: func(_, _, wsKey string, cause RestoreCause, _ []AgentChild) (string, error) {
				return "<ws-restore cause=\"" + string(cause) + "\">" + wsKey + "</ws-restore>", nil
			},
			Now:           time.Now,
		})
		r.SetEndpoint("http://127.0.0.1:1")
		r.Start(nil)
		t.Cleanup(r.Close)
		dir := t.TempDir()
		sessionDir := wsdashboard.SessionDir(r.opts.Dir, "lead-1")
		if err := os.MkdirAll(sessionDir, 0o755); err != nil {
			t.Fatal(err)
		}
		proc, err := backend.Start(context.Background(), StartSpec{})
		if err != nil {
			t.Fatal(err)
		}
		wp := proc.(*wsCompactProc)
		c := &conv{
			r:             r,
			dir:           sessionDir,
			inbox:         wsinbox.Dir(sessionDir),
			id:            "lead-1",
			rec:           Record{ReplyID: "lead-1", Lead: true, Backend: BackendClaude, Root: dir, WSKey: "k1", WhiteboardOnly: true},
			proc:          wp,
			live:          newLiveWriter(sessionDir, "k1", BackendClaude, time.Now, io.Discard),
			inboxC:        make(chan struct{}, 1),
			compactResume: true,
			compactNote:   "<ws-restore>resumed</ws-restore>",
			ambient:       ambientBlock(WorkflowLanguageInstruction("Korean")),
		}
		c.passive.putAdvisory(0, whiteboardOnlyAdvisory, 1, true)
		c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactStart})
		c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd})
		if c.passive.advisoryPending() {
			t.Fatal("CompactEnd must clear staged advisory")
		}
		if !c.compactIdle() {
			t.Fatal("expected standalone compaction resume to run")
		}
		sends, _ := wp.sent()
		if len(sends) != 1 || !strings.Contains(sends[0], whiteboardFirstDirective) || !strings.Contains(sends[0], "Respond to the user in Korean.") {
			t.Fatalf("compaction resume prompt missing directive/ambient:\n%v", sends)
		}

		// Standalone restart restore also carries directive without consuming passive.
		c.turn = false
		c.compactResume = false
		c.compactNote = restartRestoreMarker + "\n<ws-restore>restarted</ws-restore>"
		c.passive.put(0, "exec_keep", "still waiting")
		if !c.compactIdle() {
			t.Fatal("expected standalone restart restore to run")
		}
		sends, _ = wp.sent()
		if len(sends) != 2 || !strings.Contains(sends[1], whiteboardFirstDirective) {
			t.Fatalf("restart restore prompt missing directive:\n%v", sends)
		}
		if c.passive.pending() != 1 {
			t.Fatalf("standalone restore must not drain passive receipts: pending=%d", c.passive.pending())
		}
	})

	t.Run("durable compaction continue", func(t *testing.T) {
		qp := newQuotaProc()
		r := NewRunner(Options{
			Dir: t.TempDir(),
			Backends: map[string]BackendRegistration{
				BackendCodex: {ProcessKind: ProcessDurable},
			},
			Now: time.Now,
		})
		r.SetEndpoint("http://127.0.0.1:1")
		r.Start(nil)
		t.Cleanup(r.Close)
		rec := Record{ReplyID: "leaddurable", Lead: true, Backend: BackendCodex, Root: t.TempDir(), WSKey: "k2", WhiteboardOnly: true}
		if err := os.MkdirAll(wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID), 0o755); err != nil {
			t.Fatal(err)
		}
		c := &conv{
			r:             r,
			dir:           wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID),
			inbox:         wsinbox.Dir(wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID)),
			id:            rec.ReplyID,
			rec:           rec,
			proc:          qp,
			live:          newLiveWriter(wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID), rec.WSKey, rec.Backend, time.Now, io.Discard),
			inboxC:        make(chan struct{}, 1),
			compactResume: true,
			compactNote:   "<ws-restore>durable</ws-restore>",
		}
		if !c.compactDurableContinue() {
			t.Fatalf("expected compactDurableContinue to succeed: lastErr=%q", c.lastErr)
		}
		sent := qp.nextSend(t)
		if !strings.Contains(sent.Text, whiteboardFirstDirective) {
			t.Fatalf("durable compaction continue missing directive:\n%s", sent.Text)
		}
	})

	t.Run("reset clears advisory", func(t *testing.T) {
		r := NewRunner(Options{Dir: t.TempDir(), Now: time.Now})
		c := &conv{
			r:    r,
			rec:  Record{Lead: true, WhiteboardOnly: true},
			live: newLiveWriter(t.TempDir(), "k3", BackendClaude, time.Now, io.Discard),
		}
		c.passive.putAdvisory(0, whiteboardOnlyAdvisory, 1, true)
		c.apply(wslive.Event{Kind: wslive.KindReset})
		if c.passive.advisoryPending() {
			t.Fatal("KindReset must clear staged advisory")
		}
	})
}

type failingDurableProc struct {
	*quotaProc
	failFirst atomic.Bool
}

func (p *failingDurableProc) submitTurn(id, text string, steers []durableSteer, interactive bool) error {
	if p.failFirst.Swap(false) {
		return errors.New("transient submit error")
	}
	return p.quotaProc.submitTurn(id, text, steers, interactive)
}

func TestDurableTurnRetryDoesNotDoubleDeliverAdvisory(t *testing.T) {
	dir := t.TempDir()
	sessionDir := wsdashboard.SessionDir(dir, "leadretry")
	inbox := wsinbox.Dir(sessionDir)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fp := &failingDurableProc{quotaProc: newQuotaProc()}
	fp.failFirst.Store(true)

	var rev atomic.Uint64
	rev.Store(1)
	r := NewRunner(Options{
		Dir: dir,
		Backends: map[string]BackendRegistration{
			BackendCodex: {ProcessKind: ProcessDurable},
		},
		WhiteboardRevision: func(_, _ string) uint64 { return rev.Load() },
		Now:                time.Now,
	})
	r.SetEndpoint("http://127.0.0.1:1")
	r.Start(nil)
	t.Cleanup(r.Close)
	rec := Record{
		ReplyID:        "leadretry",
		Lead:           true,
		Backend:        BackendCodex,
		Root:           t.TempDir(),
		WSKey:          "k-retry",
		WhiteboardOnly: true,
	}
	c := &conv{
		r:      r,
		dir:    sessionDir,
		inbox:  inbox,
		id:     rec.ReplyID,
		rec:    rec,
		proc:   fp,
		live:   newLiveWriter(sessionDir, rec.WSKey, rec.Backend, time.Now, io.Discard),
		inboxC: make(chan struct{}, 1),
	}
	c.snapshotBoardRevision()
	c.stageWhiteboardNudge("", longReply)
	if !c.passive.advisoryPending() {
		t.Fatal("expected advisory staged")
	}

	if _, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"first user turn"}, Operator: true, User: true, WhiteboardFirst: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	c.kick()

	// Durable turn committed on disk with the advisory, submit failed, and passiveTook was acked.
	if c.rec.DurableTurn == nil || !strings.Contains(c.rec.DurableTurn.Text, whiteboardOnlyAdvisory) {
		t.Fatalf("expected committed DurableTurn with advisory, got %+v", c.rec.DurableTurn)
	}
	if c.passive.advisoryPending() {
		t.Fatal("passive advisory must be acked once DurableTurn commits so retry does not double-deliver")
	}

	// Retry replays the committed DurableTurn (advisory once).
	c.proc = fp
	c.resumeDurableTurn()
	first := fp.nextSend(t)
	if strings.Count(first.Text, whiteboardOnlyAdvisory) != 1 {
		t.Fatalf("expected advisory once on durable retry: %s", first.Text)
	}

	// Simulate turn completion and send a second message: advisory must not repeat.
	c.update(func(rec *Record) { rec.DurableTurn = nil })
	c.turn = false
	if _, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"second user turn"}, Operator: true, User: true, WhiteboardFirst: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	c.kick()
	second := fp.nextSend(t)
	if strings.Contains(second.Text, whiteboardOnlyAdvisory) {
		t.Fatalf("advisory double-delivered on turn after durable retry: %s", second.Text)
	}
}
