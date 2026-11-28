package wsagent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wslive"
)

// memSink is an in-memory audit sink. block, when set, holds every append
// until it is closed; fail, when set, refuses appends it returns an error
// for.
type memSink struct {
	mu    sync.Mutex
	evs   []wsaudit.Envelope
	block chan struct{}
	fail  func(wsaudit.Envelope) error
}

func (s *memSink) Append(_ context.Context, ev wsaudit.Envelope) error {
	if s.block != nil {
		<-s.block
	}
	if s.fail != nil {
		if err := s.fail(ev); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.evs = append(s.evs, ev)
	s.mu.Unlock()
	return nil
}

func (s *memSink) events() []wsaudit.Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.evs)
}

// boundarySink is an in-memory SourcedSink that keeps identities, the
// highest seq, each source's finalized boundary, and the losses recorded;
// it outlives a capture, standing in for the store across a daemon restart.
type boundarySink struct {
	memSink
	retained map[wsaudit.SourceID]bool
	maxSeq   uint64
	boundary map[string]uint64
	lost     []wsaudit.SourceID
	failID   func(wsaudit.SourceID) bool
	failEnv  func(wsaudit.Envelope) bool
	// cold makes Recover answer zero, like a store whose ring for the
	// session is not loaded yet; admission still uses the real ledger.
	cold bool
	// recOf is the source record AppendSourced was last handed with each
	// identity; admitLost the identities its own admission recorded as loss.
	recOf     map[wsaudit.SourceID]wsaudit.SourceID
	admitLost []wsaudit.SourceID
}

func newBoundarySink() *boundarySink {
	return &boundarySink{retained: map[wsaudit.SourceID]bool{}, boundary: map[string]uint64{}, recOf: map[wsaudit.SourceID]wsaudit.SourceID{}}
}

func (s *boundarySink) Recover(_, source string) wsaudit.Recovery {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cold {
		return wsaudit.Recovery{}
	}
	return wsaudit.Recovery{MaxSeq: s.maxSeq, Boundary: s.boundary[source]}
}

func (s *boundarySink) Retained(_ string, id wsaudit.SourceID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retained[id]
}

// AppendSourced applies the writer's admission rule like the store does:
// against its own ledger (the live boundary, including what this capture
// finalized), judging the event by its source record rec, not by id.
func (s *boundarySink) AppendSourced(_ context.Context, id, rec wsaudit.SourceID, ev wsaudit.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failID != nil && s.failID(id) || s.failEnv != nil && s.failEnv(ev) {
		return errors.New("injected append failure")
	}
	if s.retained[id] {
		return wsaudit.ErrDuplicateSource
	}
	s.recOf[id] = rec
	if rec.Covered(s.boundary[rec.Source]) {
		s.lost = append(s.lost, id)
		s.admitLost = append(s.admitLost, id)
		return nil
	}
	if ev.Seq <= s.maxSeq { // a cold Recover's seq, renumbered as the store does
		ev.Seq = s.maxSeq + 1
	}
	s.retained[id] = true
	s.maxSeq = ev.Seq
	s.evs = append(s.evs, ev)
	return nil
}

func (s *boundarySink) Finalize(_ context.Context, _, source string, boundary uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.boundary[source] = max(s.boundary[source], boundary)
	return nil
}

func (s *boundarySink) RecordLoss(_ context.Context, _ string, id wsaudit.SourceID, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lost = append(s.lost, id)
	return nil
}

// auditNopHost is a holder host that accepts the mapper's writes and does
// nothing else.
type auditNopHost struct{}

func (auditNopHost) AddSink(wsholder.Sink)  {}
func (auditNopHost) Scan()                  {}
func (auditNopHost) List() wsholder.Listing { return wsholder.Listing{} }
func (auditNopHost) LaunchWith(wsholder.LaunchSpec) (string, error) {
	return "", errors.New("no launch")
}
func (auditNopHost) Spawn(string, wsholder.SpawnSpec) (wsholder.ChildStatus, error) {
	return wsholder.ChildStatus{}, errors.New("no spawn")
}
func (auditNopHost) Input(string, []byte, bool) error { return nil }
func (auditNopHost) Status(string) (wsholder.AttachInfo, error) {
	return wsholder.AttachInfo{}, nil
}
func (auditNopHost) Stop(string) error { return nil }

// auditRig is a Claude lead process under a stand-in holder whose output
// and audit channel events a test delivers by hand, with capture on.
type auditRig struct {
	t       *testing.T
	p       *streamProcess
	h       *holderChild
	mu      sync.Mutex
	evs     []Event
	evsDone chan struct{}
	seq     uint64
	anchor  uint64
	index   uint64
	// conn is the holder connection tee and fence events carry.
	conn uint64
}

func newAuditRig(t *testing.T, sink wsaudit.Sink, holderID string) *auditRig {
	t.Helper()
	return newAuditRigWith(t, sink, holderID, newClaudeMapper(leadSpec()), claudeProtocol{})
}

func newAuditRigWith(t *testing.T, sink wsaudit.Sink, holderID string, m lineMapper, proto lineProtocol) *auditRig {
	t.Helper()
	p := newStreamProcess(m, proto)
	h := &holderChild{proc: p, id: holderID, host: auditNopHost{}, table: &holderTable{}}
	p.child, p.holder = h, h
	p.prepareAudit(StartSpec{audit: &auditTap{Session: wsaudit.Session{ID: "s1", Class: wsaudit.SessionHostedLead}, Sink: sink}}, m)
	p.startAudit(holderID)
	r := &auditRig{t: t, p: p, h: h, evsDone: make(chan struct{})}
	go func() {
		defer close(r.evsDone)
		for ev := range p.events {
			r.mu.Lock()
			r.evs = append(r.evs, ev)
			r.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		p.finish(nil)
		<-r.evsDone
	})
	return r
}

// kept delivers a ring record (a kept line) and returns its sequence.
func (r *auditRig) kept(line string) uint64 {
	r.seq++
	r.h.event(wsholder.Event{Event: "output", Seq: r.seq, Stream: "stdout", Data: []byte(line + "\n")})
	return r.seq
}

func (r *auditRig) replayed(seq uint64, line string) {
	r.h.skipTo = max(r.h.skipTo, seq)
	r.h.event(wsholder.Event{Event: "output", Seq: seq, Stream: "stdout", Data: []byte(line + "\n")})
}

// tee delivers a line the holder skipped, anchored on the last kept line.
func (r *auditRig) tee(line string) {
	r.index++
	r.h.event(wsholder.Event{Event: wsholder.EventAudit, Conn: r.conn, Anchor: r.anchor, Index: r.index, At: time.Now().UnixNano(), Data: []byte(line + "\n")})
}

func (r *auditRig) audit(ev wsholder.Event) { r.h.event(ev) }

// fence closes the open interval and releases the kept lines next..through.
func (r *auditRig) fence(next, through, sent, dropped uint64) {
	r.h.event(wsholder.Event{Event: wsholder.EventAuditFence, Conn: r.conn, Anchor: r.anchor, Next: next, Through: through, Sent: sent, Dropped: dropped})
	r.anchor, r.index = through, 0
}

// keptRun fences and delivers one kept line, as a holder does.
func (r *auditRig) keptRun(line string, sent, dropped uint64) uint64 {
	r.fence(r.seq+1, r.seq+1, sent, dropped)
	return r.kept(line)
}

// end closes capture and waits until it and the display stream have drained.
func (r *auditRig) end() {
	r.t.Helper()
	r.p.finish(nil)
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	select {
	case <-r.p.audit.done:
	case <-deadline.C:
		r.t.Fatal("capture did not drain")
	}
	select {
	case <-r.evsDone:
	case <-deadline.C:
		r.t.Fatal("display events did not drain")
	}
}

func (r *auditRig) displayLines() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, ev := range r.evs {
		if ev.LineEnd {
			n++
		}
	}
	return n
}

func kindsOf(evs []wsaudit.Envelope) []wsaudit.Kind {
	var out []wsaudit.Kind
	for _, ev := range evs {
		out = append(out, ev.Kind)
	}
	return out
}

func find(evs []wsaudit.Envelope, pred func(wsaudit.Envelope) bool) []wsaudit.Envelope {
	var out []wsaudit.Envelope
	for _, ev := range evs {
		if pred(ev) {
			out = append(out, ev)
		}
	}
	return out
}

func ofKind(k wsaudit.Kind) func(wsaudit.Envelope) bool {
	return func(ev wsaudit.Envelope) bool { return ev.Kind == k }
}

func isLoss(ev wsaudit.Envelope) bool {
	return ev.Kind == wsaudit.KindSystem && ev.Subkind == wsaudit.SubkindLoss
}

// replayFixture runs recorded Claude lines through the rig as a holder with
// the audit channel would deliver them: a line claudeRingSkip matches is a
// tee line, every other line a fenced kept record.
func (r *auditRig) replayFixture(names ...string) {
	skip := regexp.MustCompile(claudeRingSkip)
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
	var sent uint64
	for _, name := range names {
		for _, line := range claudeFixtureLines(r.t, name) {
			if strings.HasPrefix(string(line), ">> ") {
				continue // a line written to the child, not its output
			}
			if skip.Match(line) {
				r.tee(string(line))
				sent++
				continue
			}
			r.keptRun(string(line), sent, 0)
			sent = 0
		}
	}
	r.h.event(wsholder.Event{Event: wsholder.EventAuditFence, Anchor: r.anchor, Sent: sent, Final: true})
}

// TestClaudeAuditCapturesEveryKindFromRecordings pins the Claude adapter
// over recorded stream-json: each kind the policy admits is captured from
// its recording (thinking assembled with the ring-skipped deltas that came
// over the audit channel, tool calls and results joined by call ID, hooks,
// the compaction boundary and its summary, subagent lines, system lines),
// user and assistant text never are, and an unrecognized line is kept raw.
func TestClaudeAuditCapturesEveryKindFromRecordings(t *testing.T) {
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	r.replayFixture("claude-lead-partial-tool-input.jsonl", "claude-tool-turn.jsonl", "claude-lead-compaction.jsonl",
		"claude-subagent-turn.jsonl", "claude-lead-background-agent-states.jsonl")
	// Lines no recording holds: a reminder block, and a line of no known type.
	r.keptRun(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>\nmind the budget\n</system-reminder>"},{"type":"text","text":"hi"}]}}`, 0, 0)
	r.keptRun(`{"type":"brand_new","x":1}`, 0, 0)
	r.end()
	evs := sink.events()

	for _, k := range []wsaudit.Kind{wsaudit.KindThinking, wsaudit.KindToolCall, wsaudit.KindToolResult, wsaudit.KindHook,
		wsaudit.KindSystemReminder, wsaudit.KindSystem, wsaudit.KindCompaction, wsaudit.KindSubagent, wsaudit.KindOther} {
		if len(find(evs, ofKind(k))) == 0 {
			t.Errorf("no %s event captured", k)
		}
	}
	for _, k := range []wsaudit.Kind{wsaudit.KindUser, wsaudit.KindAssistantText} {
		if got := find(evs, ofKind(k)); len(got) > 0 {
			t.Errorf("visible %s captured: %+v", k, got[0])
		}
	}
	for i, ev := range evs {
		if err := ev.Validate(); err != nil {
			t.Fatalf("event %d %+v: %v", i, ev, err)
		}
		if ev.Seq != uint64(i+1) {
			t.Fatalf("event %d has seq %d; seq is assigned in source order from 1", i, ev.Seq)
		}
		if ev.Kind != wsaudit.KindOther && ev.Raw != "" {
			t.Fatalf("mapped event keeps a raw line: %+v", ev)
		}
		if strings.Contains(ev.Out, "thinking_delta") || strings.Contains(ev.Out, "input_json_delta") {
			t.Fatalf("a ring-skipped delta was stored as a line: %.200s", ev.Out)
		}
	}
	other := find(evs, ofKind(wsaudit.KindOther))
	if len(other) != 1 || other[0].Raw != `{"type":"brand_new","x":1}` {
		t.Fatalf("other events = %+v", other)
	}
	if rem := find(evs, ofKind(wsaudit.KindSystemReminder)); len(rem) != 1 || !strings.Contains(rem[0].Out, "mind the budget") {
		t.Fatalf("reminders = %+v", rem)
	}
	// The thinking block of the partial recording is signature-only.
	if th := find(evs, ofKind(wsaudit.KindThinking)); th[0].Meta[wsaudit.MetaSignatureOnly] != true {
		t.Fatalf("first thinking = %+v", th[0])
	}
	// Each tool result carries the call ID of a captured call.
	calls := map[string]bool{}
	for _, ev := range find(evs, ofKind(wsaudit.KindToolCall)) {
		if ev.CallID == "" || ev.In == "" || ev.Subkind == "" {
			t.Fatalf("tool call without id, input, or name: %+v", ev)
		}
		calls[ev.CallID] = true
	}
	for _, ev := range find(evs, ofKind(wsaudit.KindToolResult)) {
		if !calls[ev.CallID] {
			t.Fatalf("tool result %q joins no captured call", ev.CallID)
		}
	}
	if hooks := find(evs, ofKind(wsaudit.KindHook)); hooks[0].Subkind != "SessionStart" || hooks[0].Meta["hook_name"] != "SessionStart:compact" {
		t.Fatalf("hook = %+v", hooks[0])
	}
	if s := find(evs, func(ev wsaudit.Envelope) bool { return ev.Subkind == "compact_summary" }); len(s) == 0 {
		t.Fatal("the compaction summary was not captured")
	}
	if sub := find(evs, ofKind(wsaudit.KindSubagent)); sub[0].ParentID == "" && sub[0].AgentID == "" {
		t.Fatalf("subagent event names neither its call nor its task: %+v", sub[0])
	}
	// The Claude compact_boundary is the model-seen cut.
	marked, cut := wsaudit.ApplyModelSeenCut(evs)
	if !cut.HasCompaction || cut.Estimated || marked[cut.Index].Kind != wsaudit.KindCompaction {
		t.Fatalf("cut = %+v", cut)
	}
	if marked[0].ModelSeen || !marked[len(marked)-1].ModelSeen {
		t.Fatal("events before the boundary are not cut off, or later ones are")
	}
	if n := find(evs, isLoss); len(n) != 0 {
		t.Fatalf("a complete replay recorded loss: %+v", n)
	}
}

const (
	thinkStart      = `{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1"}}}`
	thinkBlock      = `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}}`
	thinkStop       = `{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`
	thinkLine       = `{"type":"assistant","message":{"id":"msg_1","content":[{"type":"thinking","thinking":"","signature":"sig"}]}}`
	auditResultLine = `{"type":"result","subtype":"success","result":"ok"}`
)

func thinkDelta(text string) string {
	return fmt.Sprintf(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":%q}}}`, text)
}

// TestAuditTeeArrivingAfterTheNextKeptLineKeepsSourceOrder pins Decision
// 5's ordering: a tee line delivered after the kept lines that follow it is
// still assembled before them, because they wait for the fence that
// releases them; the display stream never waits. The held lines include
// the result, which ends the block: mapped early, it would flush the block
// before the deltas arrived.
func TestAuditTeeArrivingAfterTheNextKeptLineKeepsSourceOrder(t *testing.T) {
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
	r.keptRun(thinkStart, 0, 0)
	r.keptRun(thinkBlock, 0, 0)
	// The stop and the result arrive before the interval's tee lines and
	// fence.
	r.kept(thinkStop)
	r.kept(auditResultLine)
	r.tee(thinkDelta("plan "))
	r.tee(thinkDelta("carefully"))
	r.fence(r.seq-1, r.seq, 2, 0)
	r.end()
	th := find(sink.events(), ofKind(wsaudit.KindThinking))
	if len(th) != 1 || th[0].Out != "plan carefully" || th[0].Meta[wsaudit.MetaAssembled] != true || th[0].Meta[wsaudit.MetaIncomplete] != nil {
		t.Fatalf("thinking = %+v, want the teed deltas assembled whole", th)
	}
	if got := r.displayLines(); got != 4 {
		t.Fatalf("display delivered %d lines, want 4", got)
	}
}

// TestAuditLostFenceClosesItsIntervalBeforeTheNext pins a fence lost to
// overload: the next fence closes the stranded interval first, unfenced and
// marked incomplete, so its tee lines never map after later lines.
func TestAuditLostFenceClosesItsIntervalBeforeTheNext(t *testing.T) {
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
	r.keptRun(thinkStart, 0, 0)
	r.keptRun(thinkBlock, 0, 0)
	r.tee(thinkDelta("first half"))
	// The fence closing that interval is lost; the next run's comes.
	r.anchor, r.index = r.seq+1, 0
	r.kept(`{"type":"system","subtype":"status","status":"between"}`)
	r.fence(r.seq+1, r.seq+1, 0, 0)
	r.kept(auditResultLine)
	r.end()
	evs := sink.events()
	th := find(evs, ofKind(wsaudit.KindThinking))
	if len(th) != 1 || th[0].Out != "first half" || th[0].Meta[wsaudit.MetaIncomplete] != true {
		t.Fatalf("thinking = %+v, want the stranded delta assembled and marked incomplete", th)
	}
	var order []string
	for _, ev := range evs {
		order = append(order, string(ev.Kind)+"/"+ev.Subkind)
	}
	if want := []string{"system/status", "thinking/", "system/result:success"}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

// TestAuditLateTeesCoalesceAndAFenceReopensTheStream pins overload after a
// timeout: the tee lines that keep coming are one loss, not one per line,
// and the next fence on the same connection opens the tee stream again.
func TestAuditLateTeesCoalesceAndAFenceReopensTheStream(t *testing.T) {
	old := auditFenceWait
	auditFenceWait = 30 * time.Millisecond
	t.Cleanup(func() { auditFenceWait = old })
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
	r.kept(thinkStart) // its fence never comes
	waitUntil(t, "the timeout", func() bool { return len(find(sink.events(), isLoss)) == 1 })
	for i := 0; i < 50; i++ {
		r.tee(thinkDelta("x"))
	}
	r.fence(r.seq+1, r.seq+1, 50, 0) // reopens from the next interval
	r.kept(thinkBlock)
	r.tee(thinkDelta("back"))
	r.fence(r.seq+1, r.seq+1, 1, 0)
	r.kept(thinkStop)
	r.keptRun(thinkLine, 0, 0)
	r.end()
	evs := sink.events()
	loss := find(evs, isLoss)
	if len(loss) != 2 || loss[1].Meta[wsaudit.MetaLossReason] != wsaudit.LossLate || loss[1].Meta[wsaudit.MetaLost] != uint64(50) {
		t.Fatalf("loss = %+v, want the timeout and one late loss of 50", loss)
	}
	if th := find(evs, ofKind(wsaudit.KindThinking)); len(th) != 1 || th[0].Out != "back" {
		t.Fatalf("thinking = %+v, want the reopened stream's delta", th)
	}
}

// TestAuditRealHolderAttachReachesCapture pins the manager-to-process path:
// a real holder launched under a manager that offers the audit channel
// attaches before the process is listed, and capture still learns of the
// attach (Manager.AuditAttach), so the ring-skipped deltas are assembled.
func TestAuditRealHolderAttachReachesCapture(t *testing.T) {
	lines := []string{thinkStart, thinkBlock, thinkDelta("real "), thinkDelta("holder"), thinkStop, thinkLine, auditResultLine}
	sink := runRealHolderCapture(t, lines, 5)
	th := find(sink.events(), ofKind(wsaudit.KindThinking))
	if th[0].Out != "real holder" || th[0].Meta[wsaudit.MetaIncomplete] != nil {
		t.Fatalf("thinking = %+v, want the teed deltas from the real holder", th[0])
	}
	if loss := find(sink.events(), isLoss); len(loss) != 0 {
		t.Fatalf("loss = %+v", loss)
	}
}

// TestAuditRealHolderLineCutAcrossReads pins the fence of a kept line longer
// than one holder record that reaches the holder in two reads: the head is
// a cut record no daemon line carries, so the holder fences the run only
// when the line ends. A fence anchored on the head would leave its interval
// waiting for a line that never maps, and the bounded wait would close the
// tee stream with a false loss.
func TestAuditRealHolderLineCutAcrossReads(t *testing.T) {
	long := `{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"FILL"}]}}`
	lines := []string{thinkStart, thinkBlock, "SPLIT" + long, thinkDelta("after "), thinkDelta("the cut"), thinkStop, thinkLine, auditResultLine}
	sink := runRealHolderCapture(t, lines, 6)
	th := find(sink.events(), ofKind(wsaudit.KindThinking))
	if th[0].Out != "after the cut" || th[0].Meta[wsaudit.MetaIncomplete] != nil {
		t.Fatalf("thinking = %+v, want the deltas teed after the cut line", th[0])
	}
	if loss := find(sink.events(), isLoss); len(loss) != 0 {
		t.Fatalf("loss = %+v", loss)
	}
}

// runRealHolderCapture runs lines through a real holder.py with the Claude
// lead's ring skip under capture, and waits for kept display lines and one
// thinking event. A line prefixed SPLIT has its FILL replaced by 1.5 MiB,
// over one record of the lead's ring (1 MiB), and is written in two writes
// 300ms apart, so the holder cuts it and reads the cut head in its own chunk.
func runRealHolderCapture(t *testing.T, lines []string, kept int) *memSink {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	if out, err := exec.Command(python, "-c", "import sys; print(sys.version_info >= (3, 10))").Output(); err != nil || strings.TrimSpace(string(out)) != "True" {
		t.Skip("python3 older than 3.10")
	}
	var table holderTable
	m := wsholder.New(wsholder.Options{Dir: filepath.Join(t.TempDir(), wsholder.DirName), Python: python, AuditChannel: true})
	m.AddSink(&table)
	t.Cleanup(m.Close)
	sink := &memSink{}
	mapper := newClaudeMapper(leadSpec())
	p := newStreamProcess(mapper, claudeProtocol{})
	p.prepareAudit(StartSpec{audit: &auditTap{Session: wsaudit.Session{ID: "s1", Class: wsaudit.SessionHostedLead}, Sink: sink}}, mapper)
	h := &holderChild{host: ManagerHost{m}, table: &table, session: holderSession{Kind: holderKindLead, ReplyID: "s1"}, ringSkip: claudeRingSkip}
	script := "import sys, time\nfor l in sys.argv[1:]:\n    if l.startswith('SPLIT'):\n        l = l[5:].replace('FILL', 'x' * (3 << 19))\n        h = len(l) * 3 // 4\n        sys.stdout.write(l[:h])\n        sys.stdout.flush()\n        time.sleep(0.3)\n        l = l[h:]\n    sys.stdout.write(l + '\\n')\n    sys.stdout.flush()\nsys.stdin.read()\n"
	cmd := exec.Command(python, append([]string{"-c", script}, lines...)...)
	if err := h.start(p, cmd); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(h.id) })
	var mu sync.Mutex
	ended := 0
	go func() {
		for ev := range p.events {
			if ev.LineEnd {
				mu.Lock()
				ended++
				mu.Unlock()
			}
		}
	}()
	waitUntil(t, "the kept lines", func() bool { mu.Lock(); defer mu.Unlock(); return ended >= kept })
	waitUntil(t, "the assembled thinking", func() bool { return len(find(sink.events(), ofKind(wsaudit.KindThinking))) == 1 })
	return sink
}

// TestAuditFenceAheadOfItsAnchorLineWaitsForIt pins the other wire order:
// the holder sends audit messages ahead of ring records, so an interval's
// tee lines and fence can arrive before the kept line they follow. They
// are mapped only after it.
func TestAuditFenceAheadOfItsAnchorLineWaitsForIt(t *testing.T) {
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
	r.keptRun(thinkStart, 0, 0)
	// The fence releasing the block start (seq 2) is sent, then the tee
	// lines anchored on it and their fence, all before record 2 itself.
	r.fence(2, 2, 0, 0)
	r.tee(thinkDelta("after start"))
	r.fence(3, 3, 1, 0)
	r.kept(thinkBlock) // seq 2
	r.kept(thinkStop)  // seq 3
	r.keptRun(thinkLine, 0, 0)
	r.keptRun(auditResultLine, 0, 0)
	r.end()
	th := find(sink.events(), ofKind(wsaudit.KindThinking))
	if len(th) != 1 || th[0].Out != "after start" || th[0].Meta[wsaudit.MetaIncomplete] != nil {
		t.Fatalf("thinking = %+v, want the delta assembled into the block it follows", th)
	}
}

// TestAuditBacklogReplayInterleavesTheOpenInterval pins attach to a holder
// with a backlog: the records before the attach replay without fences, the
// open interval's live tee lines (anchored on a backlog record) wait for
// that record, and live kept lines wait for their fence.
func TestAuditBacklogReplayInterleavesTheOpenInterval(t *testing.T) {
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	// Records 1-2 were kept while no daemon was attached; the holder's
	// next sequence is 3 and its open interval is anchored on 2.
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 3})
	r.anchor = 2
	r.tee(thinkDelta("live "))
	r.tee(thinkDelta("thought"))
	r.fence(3, 3, 2, 0)
	r.kept(thinkStart) // backlog 1
	r.kept(thinkBlock) // backlog 2
	r.kept(thinkStop)  // live 3
	r.keptRun(thinkLine, 0, 0)
	r.keptRun(auditResultLine, 0, 0)
	r.end()
	th := find(sink.events(), ofKind(wsaudit.KindThinking))
	if len(th) != 1 || th[0].Out != "live thought" {
		t.Fatalf("thinking = %+v, want the live deltas after their backlog block start", th)
	}
	if n := find(sink.events(), isLoss); len(n) != 0 {
		t.Fatalf("loss recorded: %+v", n)
	}
}

// TestAuditFenceAccounting pins the fence cases: a zero-skip interval closes
// at once; a tee the holder dropped is recorded on the open assembled event
// as incomplete, or as a standalone loss marker when none spans it; the
// final fence at child exit closes the last interval with no disconnect.
func TestAuditFenceAccounting(t *testing.T) {
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
	r.keptRun(thinkStart, 0, 0)
	r.keptRun(thinkBlock, 0, 0)
	r.tee(thinkDelta("a"))
	r.index++ // the holder dropped the second delta
	r.tee(thinkDelta("c"))
	r.fence(r.seq+1, r.seq+1, 2, 1)
	r.kept(auditResultLine) // the block never stopped: the turn was cut short
	// A zero-skip interval, then a final fence reporting one dropped line
	// that no open block spans.
	r.keptRun(`{"type":"system","subtype":"status","status":null}`, 0, 0)
	r.h.event(wsholder.Event{Event: wsholder.EventAuditFence, Anchor: r.anchor, Dropped: 1, Final: true})
	r.h.event(wsholder.Event{Event: "exit", Seq: r.seq + 1, ExitCode: new(int)})
	r.end()
	evs := sink.events()
	th := find(evs, ofKind(wsaudit.KindThinking))
	if len(th) != 1 || th[0].Out != "ac" || th[0].Meta[wsaudit.MetaIncomplete] != true || th[0].Meta[wsaudit.MetaLost] != uint64(1) {
		t.Fatalf("thinking = %+v, want the assembly marked incomplete with one lost line", th)
	}
	loss := find(evs, isLoss)
	if len(loss) != 1 || loss[0].Meta[wsaudit.MetaLossReason] != wsaudit.LossDropped || loss[0].Meta[wsaudit.MetaLost] != uint64(1) {
		t.Fatalf("loss markers = %+v, want one for the final interval's dropped line", loss)
	}
	if find(evs, func(ev wsaudit.Envelope) bool { return ev.Meta[wsaudit.MetaLossReason] == wsaudit.LossDisconnect }) != nil {
		t.Fatal("child exit recorded a disconnect")
	}
}

// TestAuditFenceTimeoutClosesTheSourceWithoutReordering pins overload: a
// kept line whose fence never comes is mapped after the bound, the source's
// tee stream closes as incomplete, a later tee is lost rather than mapped
// late, and later kept lines no longer wait.
func TestAuditFenceTimeoutClosesTheSourceWithoutReordering(t *testing.T) {
	old := auditFenceWait
	auditFenceWait = 50 * time.Millisecond
	t.Cleanup(func() { auditFenceWait = old })
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
	r.kept(`{"type":"system","subtype":"status","status":"one"}`) // no fence
	waitUntil(t, "the held line to time out", func() bool { return len(sink.events()) >= 2 })
	r.tee(thinkDelta("late"))
	r.kept(`{"type":"system","subtype":"status","status":"two"}`)
	r.end()
	evs := sink.events()
	var subs []string
	for _, ev := range evs {
		subs = append(subs, ev.Subkind+"/"+fmt.Sprint(ev.Meta[wsaudit.MetaLossReason]))
	}
	want := []string{"status/<nil>", "audit_loss/fence_timeout", "audit_loss/late", "status/<nil>"}
	if !slices.Equal(subs, want) {
		t.Fatalf("events = %v, want %v", subs, want)
	}
	if got := r.displayLines(); got != 2 {
		t.Fatalf("display delivered %d lines, want 2", got)
	}
}

// TestAuditDisconnectMarksTheUnfencedIntervalIncomplete pins disconnect: the
// tee lines of an interval no fence closed are mapped in order and the
// assembly they feed is marked incomplete.
func TestAuditDisconnectMarksTheUnfencedIntervalIncomplete(t *testing.T) {
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
	r.keptRun(thinkStart, 0, 0)
	r.keptRun(thinkBlock, 0, 0)
	r.tee(thinkDelta("half a thou"))
	r.audit(wsholder.Event{Event: wsholder.EventAuditDetached})
	r.end()
	th := find(sink.events(), ofKind(wsaudit.KindThinking))
	if len(th) != 1 || th[0].Out != "half a thou" || th[0].Meta[wsaudit.MetaIncomplete] != true || th[0].Meta[wsaudit.MetaLossReason] != wsaudit.LossDisconnect {
		t.Fatalf("thinking = %+v, want the unfenced assembly marked incomplete by the disconnect", th)
	}
}

// TestAuditOldHolderCapturesThePostFilterStream pins the new-daemon,
// old-holder direction: with no audit channel the kept lines are captured
// as they come, never held, and only the skipped-only content is missing.
func TestAuditOldHolderCapturesThePostFilterStream(t *testing.T) {
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	for _, line := range claudeFixtureLines(t, "claude-tool-turn.jsonl") {
		if !strings.HasPrefix(string(line), ">> ") {
			r.kept(string(line))
		}
	}
	waitUntil(t, "capture without fences", func() bool { return len(find(sink.events(), ofKind(wsaudit.KindToolResult))) == 2 })
	r.end()
}

// TestAuditReplayRecordsAFailedAppendAsLoss pins the recovery rule with a
// boundary-keeping sink: append of source sequence 10 fails, 11 succeeds;
// after a restart the holder replays its ring, and 10 is recorded as loss,
// never appended after 11, while retained identities are not duplicated and
// seq resumes above the recovered maximum.
func TestAuditReplayRecordsAFailedAppendAsLoss(t *testing.T) {
	sink := newBoundarySink()
	sink.failID = func(id wsaudit.SourceID) bool { return id.Seq == 10 }
	line := func(i int) string { return fmt.Sprintf(`{"type":"system","subtype":"status","status":"n%d"}`, i) }
	r := newAuditRig(t, sink, "h1")
	for i := 1; i <= 11; i++ {
		r.kept(line(i))
	}
	r.end()
	if len(sink.events()) != 10 || sink.boundary["h1"] != 11 {
		t.Fatalf("first daemon: %d events, boundary %d", len(sink.events()), sink.boundary["h1"])
	}
	sink.failID = nil

	r2 := newAuditRig(t, sink, "h1")
	for i := 1; i <= 11; i++ {
		r2.replayed(uint64(i), line(i))
	}
	r2.seq = 11
	r2.kept(line(12))
	r2.end()
	evs := sink.events()
	if len(evs) != 11 {
		t.Fatalf("after replay: %d events, want 10 retained plus record 12", len(evs))
	}
	last := evs[len(evs)-1]
	if !strings.Contains(last.Out, `"n12"`) || last.Seq != 11 {
		t.Fatalf("last event = %+v, want record 12 at seq 11", last)
	}
	for _, ev := range evs {
		if strings.Contains(ev.Out, `"n10"`) {
			t.Fatal("record 10 was appended late")
		}
	}
	if len(sink.lost) != 1 || sink.lost[0].Seq != 10 {
		t.Fatalf("losses = %+v, want record 10", sink.lost)
	}
}

// TestAuditReplayOrdinalsIgnoreCarriedAssemblies pins the durable ordinal
// of a record that emits several envelopes: live, the result line ends a
// thinking block assembled from tee'd deltas and emits [thinking, result];
// replayed after a restart, no deltas come back, and it emits [result]. The
// assembly is keyed apart from the record, so the replayed result keeps the
// identity it had live: a result whose append failed is recorded as loss,
// never mistaken for the retained thinking, and nothing is appended twice.
func TestAuditReplayOrdinalsIgnoreCarriedAssemblies(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%v", fail), func(t *testing.T) {
			sink := newBoundarySink()
			r := newAuditRig(t, sink, "h1")
			r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
			kept := []string{thinkStart, thinkBlock}
			for _, l := range kept {
				r.keptRun(l, 0, 0)
			}
			r.tee(thinkDelta("carried "))
			r.tee(thinkDelta("thought"))
			r.keptRun(thinkStop, 2, 0)
			resultSeq := r.seq + 1
			isResult := func(ev wsaudit.Envelope) bool { return ev.Subkind == "result:success" }
			if fail {
				sink.failEnv = isResult
			}
			r.keptRun(auditResultLine, 0, 0)
			kept = append(kept, thinkStop, auditResultLine)
			r.end()
			live := sink.events()
			if th := find(live, ofKind(wsaudit.KindThinking)); len(th) != 1 || th[0].Out != "carried thought" {
				t.Fatalf("live thinking = %+v", th)
			}
			sink.failEnv = nil

			r2 := newAuditRig(t, sink, "h1")
			for i, l := range kept {
				r2.replayed(uint64(i+1), l)
			}
			r2.end()
			evs := sink.events()
			if n := len(find(evs, ofKind(wsaudit.KindThinking))); n != 1 {
				t.Fatalf("thinking events after replay = %d, want 1", n)
			}
			results := find(evs, isResult)
			var lost []wsaudit.SourceID
			for _, id := range sink.lost {
				if id.Seq == resultSeq && id.Key == "" {
					lost = append(lost, id)
				}
			}
			if fail {
				if len(results) != 0 || len(lost) != 1 {
					t.Fatalf("failed result: %d appended, losses %+v; want it recorded as loss, not appended late", len(results), sink.lost)
				}
			} else if len(results) != 1 || len(sink.lost) != 0 || len(evs) != len(live) {
				t.Fatalf("clean replay: %d results, losses %+v, %d events (live %d); want nothing new", len(results), sink.lost, len(evs), len(live))
			}
		})
	}
}

// TestAuditReplayKeepsCarriedSlotsWhenATeeOnlyBlockIsGone pins carried
// identities that do not depend on what a replay rebuilds: live, the result
// line ends a thinking block whose body was all tee'd and a tool block, and
// carries [thinking, tool_call]; the tool_call's append fails. Replayed, the
// thinking body is gone (no event) and only the tool_call is rebuilt. It
// keeps its block's slot, so it is not mistaken for the retained thinking;
// and its record is at the finalized boundary, so it is recorded as loss,
// never appended late (Decision 5). Nothing is duplicated.
func TestAuditReplayKeepsCarriedSlotsWhenATeeOnlyBlockIsGone(t *testing.T) {
	for _, cold := range []bool{false, true} {
		t.Run(recoveryName(cold), func(t *testing.T) { replayCarriedSlots(t, cold) })
	}
}

func replayCarriedSlots(t *testing.T, cold bool) {
	const (
		toolStart = `{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}}}`
		toolInput = `{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"ls\"}"}}}`
		toolStop  = `{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`
	)
	sink := newBoundarySink()
	r := newAuditRig(t, sink, "h1")
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
	r.keptRun(thinkStart, 0, 0)
	r.keptRun(thinkBlock, 0, 0)
	r.tee(thinkDelta("tee only"))
	r.keptRun(thinkStop, 1, 0)
	r.keptRun(toolStart, 0, 0)
	r.tee(toolInput)
	r.keptRun(toolStop, 1, 0)
	isCall := func(ev wsaudit.Envelope) bool { return ev.Kind == wsaudit.KindToolCall }
	sink.failEnv = isCall
	resultSeq := r.keptRun(auditResultLine, 0, 0)
	kept := []string{thinkStart, thinkBlock, thinkStop, toolStart, toolStop, auditResultLine}
	r.end()
	if th := find(sink.events(), ofKind(wsaudit.KindThinking)); len(th) != 1 || th[0].Out != "tee only" {
		t.Fatalf("live thinking = %+v", th)
	}
	if n := len(find(sink.events(), isCall)); n != 0 {
		t.Fatalf("live tool calls = %d, want the failed append missing", n)
	}
	sink.failEnv = nil
	sink.cold = cold

	r2 := newAuditRig(t, sink, "h1")
	for i, l := range kept {
		r2.replayed(uint64(i+1), l)
	}
	r2.end()
	evs := sink.events()
	if n := len(find(evs, ofKind(wsaudit.KindThinking))); n != 1 {
		t.Fatalf("thinking events after replay = %d, want 1", n)
	}
	if calls := find(evs, isCall); len(calls) != 0 {
		t.Fatalf("tool calls after replay = %+v, want the failed one never appended late", calls)
	}
	// The tool block's slot is its index, 1: positionally it would be #0,
	// the retained thinking's identity, and vanish without a loss.
	want := wsaudit.SourceID{Source: "h1", Key: fmt.Sprintf("asm.%d", resultSeq), Ordinal: 1}
	if !slices.Equal(sink.lost, []wsaudit.SourceID{want}) {
		t.Fatalf("losses = %+v, want only the tool call %+v", sink.lost, want)
	}
	checkLossJudge(t, sink, cold, want, wsaudit.SourceID{Source: "h1", Seq: resultSeq})
	if n := len(find(evs, func(ev wsaudit.Envelope) bool { return ev.Subkind == "result:success" })); n != 1 {
		t.Fatalf("results after replay = %d, want 1", n)
	}
}

// TestAuditCodexReplayAfterTheStartedItemWrapped pins the Codex analogue:
// live, item/started yields the call and item/completed the result; replayed
// after the ring dropped item/started, the completed item yields the call
// too. The call's identity is its item, so it finds the live call retained,
// and the result keeps its identity: a clean replay records no loss and
// appends nothing, and a result whose append failed is recorded as loss.
func TestAuditCodexReplayAfterTheStartedItemWrapped(t *testing.T) {
	item := func(method, status string) string {
		return fmt.Sprintf(`{"method":%q,"params":{"threadId":"th1","turnId":"tu1","item":{"type":"mcpToolCall","id":"call-1","server":"ws","tool":"echo","status":%q,"arguments":{"text":"x"},"result":{"content":[{"type":"text","text":"x"}]},"error":null}}}`, method, status)
	}
	lines := []string{
		`{"method":"turn/started","params":{"threadId":"th1","turn":{"id":"tu1","status":"inProgress","items":[]}}}`,
		item("item/started", "inProgress"),
		item("item/completed", "completed"),
		`{"method":"turn/completed","params":{"threadId":"th1","turn":{"id":"tu1","status":"completed","items":[]}}}`,
	}
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%v", fail), func(t *testing.T) {
			sink := newBoundarySink()
			isResult := func(ev wsaudit.Envelope) bool { return ev.Kind == wsaudit.KindToolResult }
			if fail {
				sink.failEnv = isResult
			}
			r := newAuditRigWith(t, sink, "h1", newCodexMapper("th1"), codexProtocol{})
			for _, l := range lines {
				r.kept(l)
			}
			r.end()
			live := sink.events()
			sink.failEnv = nil

			r2 := newAuditRigWith(t, sink, "h1", newCodexMapper("th1"), codexProtocol{})
			for i := 2; i < len(lines); i++ { // records 1-2 wrapped out of the ring
				r2.replayed(uint64(i+1), lines[i])
			}
			r2.end()
			evs := sink.events()
			if n := len(find(evs, ofKind(wsaudit.KindToolCall))); n != 1 {
				t.Fatalf("tool calls after replay = %d, want the live one only", n)
			}
			if fail {
				if n := len(find(evs, isResult)); n != 0 || len(sink.lost) != 1 || sink.lost[0].Seq != 3 || sink.lost[0].Key != "" {
					t.Fatalf("failed result: %d appended, losses %+v; want only it recorded as loss", n, sink.lost)
				}
				return
			}
			if len(sink.lost) != 0 || len(evs) != len(live) {
				t.Fatalf("clean replay: losses %+v, %d events (live %d); want nothing new", sink.lost, len(evs), len(live))
			}
		})
	}
}

// TestAuditCodexFailedCallIsNeverAppendedAfterTheCut pins Decision 5 for a
// keyed event: live, the call's append fails, its result is retained, and a
// compaction cut follows. Replayed (with item/started still in the ring, or
// wrapped out so the completion yields the call), the call's record is at
// or below the finalized boundary, so it is recorded as loss: appended late
// it would land after its result and after the cut, with a fresh seq.
func TestAuditCodexFailedCallIsNeverAppendedAfterTheCut(t *testing.T) {
	item := func(method, status string) string {
		return fmt.Sprintf(`{"method":%q,"params":{"threadId":"th1","turnId":"tu1","item":{"type":"commandExecution","id":"call-1","command":"ls","cwd":"/","status":%q,"aggregatedOutput":"ok","exitCode":0}}}`, method, status)
	}
	lines := []string{
		`{"method":"turn/started","params":{"threadId":"th1","turn":{"id":"tu1","status":"inProgress","items":[]}}}`,
		item("item/started", "inProgress"),
		item("item/completed", "completed"),
		`{"method":"item/completed","params":{"threadId":"th1","turnId":"tu1","item":{"type":"contextCompaction","id":"cc-1"}}}`,
		`{"method":"turn/completed","params":{"threadId":"th1","turn":{"id":"tu1","status":"completed","items":[]}}}`,
	}
	for _, c := range []struct {
		from int
		cold bool
	}{{1, false}, {3, false}, {1, true}, {3, true}} { // replay the whole ring, or from the completion on
		from, cold := c.from, c.cold
		t.Run(fmt.Sprintf("replay from %d, %s", from, recoveryName(cold)), func(t *testing.T) {
			sink := newBoundarySink()
			isCall := func(ev wsaudit.Envelope) bool { return ev.Kind == wsaudit.KindToolCall }
			sink.failEnv = isCall
			r := newAuditRigWith(t, sink, "h1", newCodexMapper("th1"), codexProtocol{})
			for _, l := range lines {
				r.kept(l)
			}
			r.end()
			live := sink.events()
			if len(find(live, isCall)) != 0 || len(find(live, ofKind(wsaudit.KindToolResult))) != 1 || len(find(live, ofKind(wsaudit.KindCompaction))) != 1 {
				t.Fatalf("live = %+v, want the result and the cut without the call", live)
			}
			sink.failEnv = nil
			sink.cold = cold

			r2 := newAuditRigWith(t, sink, "h1", newCodexMapper("th1"), codexProtocol{})
			for i := from; i <= len(lines); i++ {
				r2.replayed(uint64(i), lines[i-1])
			}
			r2.end()
			evs := sink.events()
			if calls := find(evs, isCall); len(calls) != 0 {
				t.Fatalf("call appended late after its result and the cut: %+v", calls)
			}
			if len(evs) != len(live) {
				t.Fatalf("%d events after replay, live %d; want nothing new", len(evs), len(live))
			}
			want := wsaudit.SourceID{Source: "h1", Key: codexCallKey("call-1")}
			if !slices.Equal(sink.lost, []wsaudit.SourceID{want}) {
				t.Fatalf("losses = %+v, want only the call %+v", sink.lost, want)
			}
			// The call comes from item/started (record 2) when the replay has
			// it, else from item/completed (record 3).
			checkLossJudge(t, sink, cold, want, wsaudit.SourceID{Source: "h1", Seq: uint64(max(from, 2))})
		})
	}
}

// TestAuditStreamEndFlushIsNotCoveredByItsOwnBoundary pins the place of the
// stream-end flush: the assembly still open when the stream ends sits after
// the last kept line, which capture has already finalized once idle. Judged
// by the sink against that live boundary, a flush placed at the line itself
// would be recorded as loss; placed after it, the incomplete thinking is
// stored.
func TestAuditStreamEndFlushIsNotCoveredByItsOwnBoundary(t *testing.T) {
	sink := newBoundarySink()
	r := newAuditRig(t, sink, "h1")
	r.kept(thinkStart)
	r.kept(thinkBlock)
	last := r.kept(thinkDelta("cut short"))
	deadline := time.Now().Add(10 * time.Second)
	for sink.Recover("s1", "h1").Boundary < last {
		if time.Now().After(deadline) {
			t.Fatal("capture never finalized the last kept line")
		}
		time.Sleep(time.Millisecond)
	}
	r.end()
	th := find(sink.events(), ofKind(wsaudit.KindThinking))
	if len(th) != 1 || th[0].Out != "cut short" || th[0].Meta[wsaudit.MetaIncomplete] != true {
		t.Fatalf("thinking = %+v, want the incomplete block stored", th)
	}
	if len(sink.admitLost) != 0 {
		t.Fatalf("sink admission recorded %+v as late", sink.admitLost)
	}
}

func recoveryName(cold bool) string {
	if cold {
		return "cold recovery"
	}
	return "warm recovery"
}

// checkLossJudge checks who recorded the late keyed event lost as loss:
// with a warm Recover, capture (the sink is never handed it); with a cold
// one (zero boundary, a store still loading the session), the sink's own
// admission, judging the source record rec capture handed it alongside the
// identity, since the identity alone has no position.
func checkLossJudge(t *testing.T, sink *boundarySink, cold bool, id, rec wsaudit.SourceID) {
	t.Helper()
	if !cold {
		if len(sink.admitLost) != 0 {
			t.Fatalf("warm: sink admission recorded %+v, want capture to judge", sink.admitLost)
		}
		return
	}
	if !slices.Equal(sink.admitLost, []wsaudit.SourceID{id}) {
		t.Fatalf("cold: sink admission recorded %+v, want %+v", sink.admitLost, id)
	}
	if got := sink.recOf[id]; got != rec {
		t.Fatalf("cold: %+v handed with record %+v, want %+v", id, got, rec)
	}
}

// TestAuditCodexItemWithoutAnIDKeepsCallAndResultApart pins the fallback
// for a tool item with no id: its call cannot be keyed by item, so it keeps
// its record's identity, and the completion that yields both the call and
// the result gives them distinct ordinals; both are stored.
func TestAuditCodexItemWithoutAnIDKeepsCallAndResultApart(t *testing.T) {
	sink := newBoundarySink()
	r := newAuditRigWith(t, sink, "h1", newCodexMapper("th1"), codexProtocol{})
	r.kept(`{"method":"turn/started","params":{"threadId":"th1","turn":{"id":"tu1","status":"inProgress","items":[]}}}`)
	seq := r.kept(`{"method":"item/completed","params":{"threadId":"th1","turnId":"tu1","item":{"type":"commandExecution","id":"","command":"ls","cwd":"/","status":"completed","aggregatedOutput":"ok","exitCode":0}}}`)
	r.end()
	var ids []string
	for _, ev := range sink.events() {
		if ev.Kind == wsaudit.KindToolCall || ev.Kind == wsaudit.KindToolResult {
			ids = append(ids, string(ev.Kind)+" "+ev.ID)
		}
	}
	want := []string{fmt.Sprintf("tool_call h1/s%d#0", seq), fmt.Sprintf("tool_result h1/s%d#1", seq)}
	if !slices.Equal(ids, want) {
		t.Fatalf("stored = %q, want %q", ids, want)
	}
}

// TestAuditAttachIsNeverRefusedByAFullQueue pins that the attach marker of
// a new holder connection survives a full capture queue: the lines and tee
// lines filling it are refused and recorded as loss, but the attach is
// queued, so the new connection's tee lines and fences are accepted and its
// thinking is assembled whole.
func TestAuditAttachIsNeverRefusedByAFullQueue(t *testing.T) {
	sink := &memSink{block: make(chan struct{})}
	r := newAuditRig(t, sink, "h1")
	r.conn = 1
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Conn: 1, Next: 1})
	r.keptRun(`{"type":"system","subtype":"status","status":"first"}`, 0, 0)
	// Capture stalls appending the first event; the queue fills behind it.
	for i := 0; i < auditQueueItems+100; i++ {
		r.tee(`{"type":"keep_alive"}`)
	}
	if r.p.audit.refused.Load() == 0 {
		t.Fatal("the queue never filled")
	}
	r.conn, r.index = 2, 0
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Conn: 2, Next: r.seq + 1})
	close(sink.block)
	waitUntil(t, "the queue to drain", func() bool { return len(r.p.audit.in) == 0 })
	r.keptRun(thinkStart, 0, 0)
	r.keptRun(thinkBlock, 0, 0)
	r.tee(thinkDelta("second "))
	r.tee(thinkDelta("connection"))
	r.keptRun(thinkStop, 2, 0)
	r.keptRun(thinkLine, 0, 0)
	r.end()
	th := find(sink.events(), ofKind(wsaudit.KindThinking))
	if len(th) != 1 || th[0].Out != "second connection" || th[0].Meta[wsaudit.MetaIncomplete] != nil {
		t.Fatalf("thinking = %+v, want the new connection's deltas assembled whole", th)
	}
	if loss := find(sink.events(), func(ev wsaudit.Envelope) bool {
		return isLoss(ev) && ev.Meta[wsaudit.MetaLossReason] == wsaudit.LossQueueFull
	}); len(loss) == 0 {
		t.Fatal("the refused items were not recorded as loss")
	}
}

// TestAuditControlReserveExhaustionNeverBlocks pins the nonblocking holder
// reader once the control reserve is gone too: with capture stalled and its
// queue full, more reconnects than the reserve holds never block the
// delivering reader. The markers past the reserve coalesce to the newest
// connection's, which capture adopts (recording the resync as loss), so the
// newest connection's tee lines and fences are accepted and its thinking is
// assembled whole; a stale attach queued earlier never displaces it.
func TestAuditControlReserveExhaustionNeverBlocks(t *testing.T) {
	sink := &memSink{block: make(chan struct{})}
	unblock := sync.OnceFunc(func() { close(sink.block) })
	t.Cleanup(unblock)
	r := newAuditRig(t, sink, "h1")
	r.conn = 1
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Conn: 1, Next: 1})
	r.keptRun(`{"type":"system","subtype":"status","status":"first"}`, 0, 0)
	for i := 0; i < auditQueueItems+100; i++ {
		r.tee(`{"type":"keep_alive"}`)
	}
	if r.p.audit.refused.Load() == 0 {
		t.Fatal("the queue never filled")
	}
	last := uint64(2 + 3*auditControlItems)
	delivered := make(chan struct{})
	go func() {
		defer close(delivered)
		for conn := uint64(2); conn <= last; conn++ {
			r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Conn: conn, Next: r.seq + 1})
			if conn < last {
				r.audit(wsholder.Event{Event: wsholder.EventAuditDetached, Conn: conn})
			}
		}
		// The manager's copy of an older connection's attach, delivered late.
		r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Conn: last - 1, Next: r.seq + 1})
	}()
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("the holder reader blocked on a full control reserve")
	}
	unblock()
	waitUntil(t, "the queue to drain", func() bool { return len(r.p.audit.in) == 0 })
	r.conn, r.index = last, 0
	r.keptRun(thinkStart, 0, 0)
	r.keptRun(thinkBlock, 0, 0)
	r.tee(thinkDelta("newest "))
	r.tee(thinkDelta("connection"))
	r.keptRun(thinkStop, 2, 0)
	r.keptRun(thinkLine, 0, 0)
	r.end()
	th := find(sink.events(), ofKind(wsaudit.KindThinking))
	if len(th) != 1 || th[0].Out != "newest connection" || th[0].Meta[wsaudit.MetaIncomplete] != nil {
		t.Fatalf("thinking = %+v, want the newest connection's deltas assembled whole", th)
	}
	if loss := find(sink.events(), func(ev wsaudit.Envelope) bool {
		return isLoss(ev) && ev.Meta[wsaudit.MetaLossReason] == wsaudit.LossQueueFull
	}); len(loss) == 0 {
		t.Fatal("the refused items and the resync were not recorded as loss")
	}
}

// TestAuditOverflowedDetachFollowsItsQueuedAttach pins the control fallback
// when only a connection's detach overflows: its attach took the reserve's
// last slot. The detach is applied after the queued attach, never before
// it, so the ended connection is not left live: a kept line after it maps
// at once instead of waiting for a fence that connection can no longer
// send (a fence_timeout loss).
func TestAuditOverflowedDetachFollowsItsQueuedAttach(t *testing.T) {
	old := auditFenceWait
	auditFenceWait = 50 * time.Millisecond
	t.Cleanup(func() { auditFenceWait = old })
	sink := &memSink{block: make(chan struct{})}
	unblock := sync.OnceFunc(func() { close(sink.block) })
	t.Cleanup(unblock)
	r := newAuditRig(t, sink, "h1")
	r.conn = 1
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Conn: 1, Next: 1})
	r.keptRun(`{"type":"system","subtype":"status","status":"first"}`, 0, 0)
	c := r.p.audit
	for c.refused.Load() == 0 {
		r.tee(`{"type":"keep_alive"}`)
	}
	// Fill the reserve so the last connection's attach takes its last slot.
	conn := uint64(2)
	for cap(c.in)-len(c.in) > 1 {
		r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Conn: conn, Next: r.seq + 1})
		if cap(c.in)-len(c.in) > 1 {
			r.audit(wsholder.Event{Event: wsholder.EventAuditDetached, Conn: conn})
		}
		conn++
	}
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Conn: conn, Next: r.seq + 1})
	r.audit(wsholder.Event{Event: wsholder.EventAuditDetached, Conn: conn})
	c.overMu.Lock()
	over := c.over
	c.overMu.Unlock()
	if over.attach != nil || over.detached == nil || over.detached.ev.Conn != conn {
		t.Fatalf("overflow = %+v, want only connection %d's detach", over, conn)
	}
	unblock()
	waitUntil(t, "the queue to drain", func() bool { return len(c.in) == 0 })
	r.kept(`{"type":"system","subtype":"status","status":"after"}`)
	time.Sleep(10 * auditFenceWait)
	r.end()
	evs := sink.events()
	if loss := find(evs, func(ev wsaudit.Envelope) bool {
		return isLoss(ev) && ev.Meta[wsaudit.MetaLossReason] == wsaudit.LossFenceTimeout
	}); len(loss) != 0 {
		t.Fatalf("loss = %+v: the ended connection was left live", loss)
	}
	if after := find(evs, func(ev wsaudit.Envelope) bool { return strings.Contains(ev.Out, `"after"`) }); len(after) != 1 {
		t.Fatalf("the kept line after the detach = %+v", after)
	}
}

// TestClaudeAuditTotalLossMarksTheThinking pins that a thinking block whose
// every delta was dropped still records the loss: the open block takes it
// (markLoss), and the event that ends the block, the assistant line's empty
// signed thinking or, without one, the flushed assembly, carries it as
// incomplete with the count.
func TestClaudeAuditTotalLossMarksTheThinking(t *testing.T) {
	for _, tc := range []struct {
		name string
		tail []string
	}{
		{"assistant line", []string{thinkLine, auditResultLine}},
		{"no assistant line", []string{auditResultLine}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &memSink{}
			r := newAuditRig(t, sink, "h1")
			r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
			r.keptRun(thinkStart, 0, 0)
			r.keptRun(thinkBlock, 0, 0)
			r.keptRun(thinkStop, 0, 3) // the holder dropped all three deltas
			for _, l := range tc.tail {
				r.keptRun(l, 0, 0)
			}
			r.end()
			evs := sink.events()
			th := find(evs, ofKind(wsaudit.KindThinking))
			if len(th) != 1 || th[0].Meta[wsaudit.MetaIncomplete] != true || th[0].Meta[wsaudit.MetaLost] != uint64(3) ||
				th[0].Meta[wsaudit.MetaLossReason] != wsaudit.LossDropped {
				t.Fatalf("thinking = %+v, want one incomplete event carrying 3 dropped lines", th)
			}
			if n := find(evs, isLoss); len(n) != 0 {
				t.Fatalf("loss also recorded apart: %+v", n)
			}
		})
	}
}

// TestAuditPendingTeesAreBounded pins the bound on tee lines held for an
// interval no fence closed yet: past auditPendingBytes the tee stream closes
// as incomplete (what was held is assembled, the rest of the interval is
// lost), and the next fence reopens it, so the following block is whole.
func TestAuditPendingTeesAreBounded(t *testing.T) {
	restore := auditPendingBytes
	auditPendingBytes = 1000
	t.Cleanup(func() { auditPendingBytes = restore })
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
	r.keptRun(thinkStart, 0, 0)
	r.keptRun(thinkBlock, 0, 0)
	for i := 0; i < 40; i++ {
		r.tee(thinkDelta(fmt.Sprintf("d%02d ", i)))
	}
	r.keptRun(thinkStop, 40, 0)
	r.keptRun(thinkLine, 0, 0)
	// The next message's block, after the fence reopened the stream.
	r.keptRun(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_2"}}}`, 0, 0)
	r.keptRun(thinkBlock, 0, 0)
	r.tee(thinkDelta("whole"))
	r.keptRun(thinkStop, 1, 0)
	r.keptRun(`{"type":"assistant","message":{"id":"msg_2","content":[{"type":"thinking","thinking":"","signature":"sig"}]}}`, 0, 0)
	r.end()
	th := find(sink.events(), ofKind(wsaudit.KindThinking))
	if len(th) != 2 {
		t.Fatalf("thinking = %+v", th)
	}
	if th[0].Meta[wsaudit.MetaIncomplete] != true || !strings.HasPrefix(th[0].Out, "d00 d01 ") || strings.Contains(th[0].Out, "d39") {
		t.Fatalf("first thinking = %.300v, want the held deltas, cut short and incomplete", th[0])
	}
	if th[1].Out != "whole" || th[1].Meta[wsaudit.MetaIncomplete] != nil {
		t.Fatalf("second thinking = %+v, want it whole after the fence reopened the stream", th[1])
	}
}

// TestAuditSlowOrFailingSinkNeverDelaysLineDelivery pins the non-blocking
// tap: a sink that blocks, or one that fails every append, never delays the
// display stream; the queue's overflow is recorded as loss once it drains.
func TestAuditSlowOrFailingSinkNeverDelaysLineDelivery(t *testing.T) {
	t.Run("blocking", func(t *testing.T) {
		sink := &memSink{block: make(chan struct{})}
		r := newAuditRig(t, sink, "h1")
		const n = auditQueueItems + 500
		start := time.Now()
		for i := 0; i < n; i++ {
			r.kept(`{"type":"system","subtype":"status","status":null}`)
		}
		waitUntil(t, "every line on the display stream", func() bool { return r.displayLines() == n })
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("delivery took %s behind a blocked sink", d)
		}
		close(sink.block)
		r.end()
		loss := find(sink.events(), isLoss)
		if len(loss) == 0 || loss[0].Meta[wsaudit.MetaLossReason] != wsaudit.LossQueueFull {
			t.Fatalf("no queue overflow loss recorded: %+v", loss)
		}
	})
	t.Run("failing", func(t *testing.T) {
		sink := &memSink{fail: func(wsaudit.Envelope) error { return errors.New("disk full") }}
		r := newAuditRig(t, sink, "h1")
		for i := 0; i < 100; i++ {
			r.kept(`{"type":"system","subtype":"status","status":null}`)
		}
		waitUntil(t, "every line on the display stream", func() bool { return r.displayLines() == 100 })
		r.end()
	})
}

// TestClaudeRingCallIDMatchesTheAuditCallID pins (601) for Claude: the
// captured tool_call and tool_result events carry the raw harness IDs (the
// store normalizes once, at ingestion), and a tools row's calls carry the
// display key, NormalizeID(raw), that the store's ingestion gives them. This
// covers an ID over 256 encoded bytes (digested) and a short literal ID
// spelled as that digest (kept, and a different key).
func TestClaudeRingCallIDMatchesTheAuditCallID(t *testing.T) {
	long := "toolu_" + strings.Repeat("x", 300)
	digest := wsaudit.NormalizeID(long)
	literal := digest // a harness ID spelled exactly like the digest
	turn := []string{
		fmt.Sprintf(`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":%q,"name":"Read","input":{"file_path":"/a"}}]}}`, long),
		fmt.Sprintf(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":"a"}]}}`, long),
		fmt.Sprintf(`{"type":"assistant","message":{"id":"m2","content":[{"type":"tool_use","id":%q,"name":"Read","input":{"file_path":"/b"}}]}}`, literal),
		fmt.Sprintf(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":"b"}]}}`, literal),
		auditResultLine,
	}
	sink := &memSink{}
	r := newAuditRig(t, sink, "h1")
	for _, line := range turn {
		r.kept(line)
	}
	waitUntil(t, "the batches", func() bool { return r.displayLines() == len(turn) })
	r.end()

	st := wslive.NewState("")
	r.mu.Lock()
	for i, ev := range r.evs {
		if ev.Live != nil {
			if _, err := st.Apply(*ev.Live, int64(1000+i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	r.mu.Unlock()
	res, err := st.Apply(wslive.Event{Kind: wslive.KindReply, Final: true, Text: "done"}, 5000)
	if err != nil {
		t.Fatal(err)
	}
	var ring []string
	for _, e := range res.Ring {
		if e.Kind == wslive.RingTools {
			for _, b := range e.Tools.Batches {
				for _, c := range b.CallArgs {
					ring = append(ring, c.ID)
				}
			}
		}
	}
	keyed := map[string][]wsaudit.Kind{}
	var raw []string
	for _, ev := range sink.events() {
		if ev.CallID != "" {
			raw = append(raw, ev.CallID)
			key := wsaudit.NormalizeID(ev.CallID) // the store's ingestion
			keyed[key] = append(keyed[key], ev.Kind)
		}
	}
	if want := []string{long, long, literal, literal}; !slices.Equal(raw, want) {
		t.Fatalf("envelope call ids = %q, want the raw harness ids %q", raw, want)
	}
	wantLiteral := wsaudit.NormalizeID(literal)
	if !slices.Equal(ring, []string{digest, wantLiteral}) || digest == wantLiteral {
		t.Fatalf("ring call ids = %q, want the digest %q then the literal's own key %q", ring, digest, wantLiteral)
	}
	for _, id := range ring {
		if !slices.Equal(keyed[id], []wsaudit.Kind{wsaudit.KindToolCall, wsaudit.KindToolResult}) {
			t.Fatalf("history row id %q opens %v, want its call and its result", id, keyed[id])
		}
	}
}

// TestClaudeAuditEnvelopeIDsStayRaw pins the identifier ownership of
// ring-store Decision 3: the adapter writes the harness's raw IDs into every
// envelope ID field (call_id, parent ID), for a long ID and for a short
// literal spelled like a digest; only the store normalizes, once.
func TestClaudeAuditEnvelopeIDsStayRaw(t *testing.T) {
	long := "toolu_" + strings.Repeat("p", 300)
	digestShaped := wsaudit.NormalizeID(long)
	evs := mapAll(newClaudeAudit("s1"),
		fmt.Sprintf(`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":%q}}`, long),
		fmt.Sprintf(`{"type":"system","subtype":"task_started","task_id":"t1","tool_use_id":%q,"description":"sub"}`, long),
		fmt.Sprintf(`{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"m1","content":[{"type":"tool_use","id":%q,"name":"Read","input":{}}]}}`, long, digestShaped),
		fmt.Sprintf(`{"type":"assistant","message":{"id":"m2","content":[{"type":"tool_use","id":%q,"name":"Read","input":{}}]}}`, digestShaped),
	)
	if len(evs) != 4 {
		t.Fatalf("events = %+v", evs)
	}
	if evs[0].CallID != long {
		t.Fatalf("control_request call_id = %q, want the raw id", evs[0].CallID)
	}
	if evs[1].ParentID != long || evs[1].AgentID != "t1" {
		t.Fatalf("task_started parent = %q, want the raw id", evs[1].ParentID)
	}
	if evs[2].ParentID != long || evs[2].CallID != digestShaped {
		t.Fatalf("subagent tool_use parent %q call %q, want both raw", evs[2].ParentID, evs[2].CallID)
	}
	if evs[3].CallID != digestShaped {
		t.Fatalf("tool_call call_id = %q, want the digest-shaped literal unchanged", evs[3].CallID)
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestClaudeAuditToolProgress covers the SDKToolProgressMessage wire shape,
// including a missing call reference so unattached progress remains foldable.
func TestClaudeAuditToolProgress(t *testing.T) {
	for _, callID := range []string{"toolu_abc123", ""} {
		t.Run(callID, func(t *testing.T) {
			raw := fmt.Sprintf(`{"type":"tool_progress","tool_use_id":%q,"tool_name":"Bash","parent_tool_use_id":null,"elapsed_time_seconds":5.2,"uuid":"progress-1","session_id":"cli-session"}`, callID)
			at := time.Now()
			recognized, mapped := newClaudeAudit("s1").mapOwn([]byte(raw), at)
			events := wsaudit.CaptureLine(wsaudit.Session{ID: "s1", Class: wsaudit.SessionHostedLead}, BackendClaude, at, raw, recognized, mapped)
			if !recognized || len(events) != 1 {
				t.Fatalf("recognized=%v events=%+v", recognized, events)
			}
			ev := events[0]
			if ev.Kind != wsaudit.KindSystem || ev.Subkind != "tool_progress" || ev.CallID != callID || ev.Out != raw || ev.Raw != "" {
				t.Fatalf("progress envelope = %+v", ev)
			}
		})
	}
}

type pinTrackingSink struct {
	*boundarySink
	pinMu             sync.Mutex
	pins              int
	pinEvents         []string
	finalizeEntered   chan struct{}
	finalizeRelease   chan struct{}
	pinsAtFinalize    int
	pinsAtRecover     int
	finalizeSignalled bool
}

func (s *pinTrackingSink) PinCapture(session string) {
	s.pinMu.Lock()
	s.pins++
	s.pinEvents = append(s.pinEvents, "pin:"+session)
	s.pinMu.Unlock()
}

func (s *pinTrackingSink) UnpinCapture(session string) {
	s.pinMu.Lock()
	s.pins--
	s.pinEvents = append(s.pinEvents, "unpin:"+session)
	s.pinMu.Unlock()
}

func (s *pinTrackingSink) pinCount() int {
	s.pinMu.Lock()
	defer s.pinMu.Unlock()
	return s.pins
}

func (s *pinTrackingSink) Recover(session, source string) wsaudit.Recovery {
	s.pinMu.Lock()
	s.pinsAtRecover = s.pins
	s.pinMu.Unlock()
	return s.boundarySink.Recover(session, source)
}

func (s *pinTrackingSink) Finalize(ctx context.Context, session, source string, boundary uint64) error {
	s.pinMu.Lock()
	s.pinsAtFinalize = s.pins
	first := !s.finalizeSignalled
	s.finalizeSignalled = true
	s.pinMu.Unlock()
	if first && s.finalizeEntered != nil {
		close(s.finalizeEntered)
		<-s.finalizeRelease
	}
	return s.boundarySink.Finalize(ctx, session, source, boundary)
}

// TestAuditCapturePinHeldUntilRunDrainsAndFinalizes pins the capture pin
// boundary: newAuditCapture pins before Recover and run, streamProcess.closeAudit
// does not unpin early, and auditCapture.run releases the pin in a defer only
// after draining, flushing, and calling finalize, before closing c.done.
func TestAuditCapturePinHeldUntilRunDrainsAndFinalizes(t *testing.T) {
	sink := &pinTrackingSink{
		boundarySink:    newBoundarySink(),
		finalizeEntered: make(chan struct{}),
		finalizeRelease: make(chan struct{}),
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(sink.finalizeRelease) }) }
	t.Cleanup(release)

	r := newAuditRig(t, sink, "h1")
	if got := sink.pinCount(); got != 1 {
		t.Fatalf("pins after start = %d, want 1", got)
	}
	sink.pinMu.Lock()
	pinsAtRecover := sink.pinsAtRecover
	sink.pinMu.Unlock()
	if pinsAtRecover != 1 {
		t.Fatalf("pins at Recover = %d, want 1 (pin must precede Recover)", pinsAtRecover)
	}

	// Queue a line and immediately close capture via streamProcess.finish
	// (which calls closeAudit -> c.close()), holding run inside Finalize.
	r.kept(`{"type":"system","subtype":"status","status":"before-close"}`)
	<-sink.finalizeEntered
	r.p.closeAudit()
	// Give the goroutine a moment; pin MUST still be held while Finalize blocks.
	time.Sleep(15 * time.Millisecond)
	if got := sink.pinCount(); got != 1 {
		t.Fatalf("pins dropped to %d on closeAudit before run finished finalizing", got)
	}
	sink.pinMu.Lock()
	pinsAtFinalize := sink.pinsAtFinalize
	sink.pinMu.Unlock()
	if pinsAtFinalize != 1 {
		t.Fatalf("pins observed during Finalize = %d, want 1", pinsAtFinalize)
	}

	release()
	r.end()
	// By the time r.end() returns (<-c.done), UnpinCapture must already have run.
	if got := sink.pinCount(); got != 0 {
		t.Fatalf("pins after capture done = %d, want 0", got)
	}
	sink.pinMu.Lock()
	evLog := slices.Clone(sink.pinEvents)
	sink.pinMu.Unlock()
	if !slices.Equal(evLog, []string{"pin:s1", "unpin:s1"}) {
		t.Fatalf("pin lifecycle = %v, want [pin:s1 unpin:s1]", evLog)
	}
}
