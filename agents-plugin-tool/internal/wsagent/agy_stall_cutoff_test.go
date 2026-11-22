package wsagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// The log-confirmed headerless cutoff on a hosted agy lead: a fake clock for
// the request boundary, fake timers for the silence timer, and a synthetic
// agy worker log in a temporary directory.

const stallTimeout = 45 * time.Second

type stallClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stallClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stallClock) advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// stallTimers are fake timers on the stall clock: each has a deadline, a
// stopped timer never fires, and advance fires every running timer whose
// deadline it reaches. fire fires one by index regardless (a firing that
// raced its stop).
type stallTimers struct {
	mu        sync.Mutex
	clock     *stallClock
	durs      []time.Duration
	chans     []chan time.Time
	deadlines []time.Time
	done      []bool
}

func (f *stallTimers) start(d time.Duration) (<-chan time.Time, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := len(f.chans)
	c := make(chan time.Time, 1)
	f.durs = append(f.durs, d)
	f.chans = append(f.chans, c)
	f.deadlines = append(f.deadlines, f.clock.Now().Add(d))
	f.done = append(f.done, false)
	return c, func() {
		f.mu.Lock()
		f.done[i] = true
		f.mu.Unlock()
	}
}

func (f *stallTimers) fire(t *testing.T, i int) {
	t.Helper()
	f.mu.Lock()
	c := f.chans[i]
	f.done[i] = true
	f.mu.Unlock()
	c <- time.Now()
}

// advance moves the clock and fires the running timers it makes due; it
// returns how many fired.
func (f *stallTimers) advance(d time.Duration) int {
	now := f.clock.advance(d)
	f.mu.Lock()
	var due []chan time.Time
	for i, at := range f.deadlines {
		if !f.done[i] && !at.After(now) {
			f.done[i] = true
			due = append(due, f.chans[i])
		}
	}
	f.mu.Unlock()
	for _, c := range due {
		c <- now
	}
	return len(due)
}

type stallRig struct {
	t      *testing.T
	h      *leadHarness
	timers *stallTimers
	clock  *stallClock
	logDir string
	id     string
	holder string
	// boundary is the clock reading at the first prompt send.
	boundary time.Time
}

// newStallRig starts a hosted agy lead with a 45-second silence timeout and
// the given settings, and sends its first prompt.
func newStallRig(t *testing.T, timeout, retries int, settings map[string]string) *stallRig {
	t.Helper()
	h, f := newAgyLeadHarness(t)
	r := &stallRig{t: t, h: h, logDir: t.TempDir()}
	r.clock = &stallClock{now: time.Now().Truncate(time.Second)}
	r.timers = &stallTimers{clock: r.clock}
	h.opts.Now = r.clock.Now
	h.opts.Timer = r.timers.start
	h.opts.AgyModelWaitBackoff = func(int) time.Duration { return 0 }
	h.opts.AgyModelWaitTimeoutSeconds = &timeout
	h.opts.AgyModelWaitRetries = &retries
	h.opts.AgyLogDir = r.logDir
	resolver := wsconfig.NewResolver(h.opts.Config, nil, nil, nil)
	for key, value := range settings {
		if err := resolver.Set(key, value, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
	}
	h.startAt("http://127.0.0.1:1")
	r.id = h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = r.id
	r.boundary = r.clock.Now()
	h.send(r.id, "work")
	r.holder = h.holder(r.id)
	eventually(t, "the first prompt", func() bool { return len(agyPromptIDs(h, r.holder)) == 1 })
	r.sync()
	return r
}

func (r *stallRig) sync() { r.h.quiesce(r.id) }

func (r *stallRig) conv() *conv { return r.h.runner.conv(r.id) }

// writeLog replaces the session's agy worker log with lines.
func (r *stallRig) writeLog(lines ...string) {
	r.t.Helper()
	path := filepath.Join(r.logDir, "acp-worker-20261122_120000-1.log")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// baseLog is a verified tail with no main header since the boundary:
// covered from a minute before it, holding the current request's
// session/prompt line and the user-message line naming the session, one monitor
// header, and one main header of an earlier request of this process (the
// first-request safeguard is past). extra lines join it in time order, as
// agy writes them, by the instants they parse to (so a fixture that
// straddles a year end still orders correctly).
func (r *stallRig) baseLog(extra ...string) {
	b := r.boundary
	lines := []string{
		agyGlogStart(b.Add(-time.Minute), 1),
		agyGlogHeader(b.Add(-50*time.Second), 5),
		agyGlogRequest(b, 2, "session/prompt"),
		agyGlogUserMessage(b, 2, "agy-s1"),
		agyGlogMonitor(b.Add(time.Second), 7),
		agyGlogHeader(b.Add(time.Second), 7),
	}
	lines = append(lines, extra...)
	slices.SortStableFunc(lines, func(x, y string) int {
		lx, _ := parseAgyGlogLine(strings.TrimSuffix(x, "\n"), b)
		ly, _ := parseAgyGlogLine(strings.TrimSuffix(y, "\n"), b)
		return lx.at.Compare(ly.at)
	})
	r.writeLog(lines...)
}

// monitorOnlyLog is a verified tail whose only headers are a monitor's: the
// format is recognized, but this process has shown no main header.
func (r *stallRig) monitorOnlyLog() {
	b := r.boundary
	r.writeLog(
		agyGlogStart(b.Add(-time.Minute), 1),
		agyGlogRequest(b, 2, "session/prompt"),
		agyGlogUserMessage(b, 2, "agy-s1"),
		agyGlogMonitor(b.Add(time.Second), 7),
		agyGlogHeader(b.Add(time.Second), 7),
	)
}

// silenceTimers is the number of silence timers started so far.
func (r *stallRig) silenceTimers() int {
	r.timers.mu.Lock()
	defer r.timers.mu.Unlock()
	n := 0
	for _, d := range r.timers.durs {
		if d == stallTimeout {
			n++
		}
	}
	return n
}

// armed reports whether the silence timer is running now.
func (r *stallRig) armed() bool {
	armed := false
	c := r.conv()
	c.do(func() { armed = c.modelWaitC != nil })
	return armed
}

// fireSilence fires the newest silence timer after advancing the clock.
func (r *stallRig) fireSilence(after time.Duration) {
	r.t.Helper()
	r.sync()
	r.clock.advance(after)
	r.timers.fire(r.t, r.newestSilenceTimer())
	r.sync()
}

// newestSilenceTimer is the index of the last silence timer started.
func (r *stallRig) newestSilenceTimer() int {
	r.t.Helper()
	r.timers.mu.Lock()
	idx := -1
	for i, d := range r.timers.durs {
		if d == stallTimeout {
			idx = i
		}
	}
	r.timers.mu.Unlock()
	if idx < 0 {
		r.t.Fatal("no silence timer was started")
	}
	return idx
}

func (r *stallRig) cancels() int {
	n := 0
	for _, line := range r.h.holders.inputs(r.holder, "") {
		var d struct {
			Method string `json:"method"`
		}
		if json.Unmarshal([]byte(line), &d) == nil && d.Method == "session/cancel" {
			n++
		}
	}
	return n
}

func (r *stallRig) retries() int {
	n := 0
	c := r.conv()
	c.do(func() { n = c.modelWaitRetries })
	return n
}

func (r *stallRig) boundaryNow() time.Time {
	var b time.Time
	c := r.conv()
	c.do(func() { b = c.modelWaitBoundary })
	return b
}

func (r *stallRig) out(lines ...string) {
	r.h.holders.out(r.holder, lines...)
	r.sync()
}

// until waits for the conversation's loop to reach a state; holder output
// reaches the loop asynchronously.
func (r *stallRig) until(what string, cond func(c *conv) bool) {
	r.t.Helper()
	c := r.conv()
	eventually(r.t, what, func() bool {
		ok := false
		c.do(func() { ok = cond(c) })
		return ok
	})
}

// cancelAndContinue answers the interrupted prompt and waits for the
// automatic continue.
func (r *stallRig) cancelAndContinue() {
	r.t.Helper()
	ids := agyPromptIDs(r.h, r.holder)
	r.out(`{"jsonrpc":"2.0","result":{"stopReason":"cancelled"},"id":"` + ids[len(ids)-1] + `"}`)
	eventually(r.t, "the automatic continue", func() bool { return len(agyPromptIDs(r.h, r.holder)) == len(ids)+1 })
	r.sync()
}

func agyUpdateLine(update string) string {
	return `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":` + update + `}}`
}

var (
	agyTextLine        = agyUpdateLine(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"working on it"}}`)
	agyToolStartLine   = agyUpdateLine(`{"sessionUpdate":"tool_call","toolCallId":"t1","name":"view_file","status":"in_progress","args":{}}`)
	agyToolDoneLine    = agyUpdateLine(`{"sessionUpdate":"tool_call_update","toolCallId":"t1","name":"view_file","status":"completed"}`)
	agyBgStartLine     = agyUpdateLine(`{"sessionUpdate":"tool_call","toolCallId":"bg1","name":"run_command","status":"in_progress","args":{"CommandLine":"sleep 20","IsDaemon":false}}`)
	agyBgDoneLine      = agyUpdateLine(`{"sessionUpdate":"tool_call_update","toolCallId":"bg1","name":"run_command","status":"completed"}`)
	agyApprovalReqLine = `{"jsonrpc":"2.0","method":"session/request_permission","params":{"sessionId":"agy-s1","toolCall":{"toolCallId":"c1","name":"run_command","title":"ls","args":{"CommandLine":"ls"}},"options":[{"optionId":"allow","kind":"allow_once"},{"optionId":"deny","kind":"reject_once"}]},"id":"1"}`
)

func TestAgyStallCutoffAfterPromptWithNoMainHeader(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.baseLog(agyGlogHeader(r.boundary.Add(-10*time.Second), 9))
	r.fireSilence(stallTimeout)
	if got := r.cancels(); got != 1 {
		t.Fatalf("cancels = %d, want one interrupt", got)
	}
	r.cancelAndContinue()
	if got := r.retries(); got != 1 {
		t.Fatalf("retries spent = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(wsdashboard.SessionDir(r.h.dir, r.id), AgyFrameLogName)); !os.IsNotExist(err) {
		t.Fatalf("frame trace off wrote a capture file: %v", err)
	}
}

// A tool completion has no time known to precede agy's next request, so
// that request has no boundary: the silence timer stays off and even a
// firing that raced the completion does not cut, though the log shows no
// header since.
func TestAgyStallCutoffToolCompletionSuppressesTheCut(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.out(agyToolStartLine)
	r.until("the pending tool call", func(c *conv) bool { return c.modelWaitPending == 1 })
	if r.armed() {
		t.Fatal("a pending tool call left the silence timer running")
	}
	r.baseLog()
	r.clock.advance(100 * time.Second)
	r.out(agyToolDoneLine)
	r.until("the tool completion", func(c *conv) bool { return c.modelWaitPending == 0 })
	r.expectNoBoundaryNoCut("after a tool completion")
}

// The completion reaches the mapper late (held behind the holder's ring or
// the stream's backpressure) while agy's next request already has its main
// header, logged outside the skew window before the late receipt: no
// boundary from the receipt, so no cut.
func TestAgyStallCutoffLateDeliveredToolCompletionNeverCuts(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.out(agyToolStartLine)
	r.until("the pending tool call", func(c *conv) bool { return c.modelWaitPending == 1 })
	done := r.clock.advance(100 * time.Second)
	// agy finished the tool at done and started its next request a second
	// later; the completion line is delivered five seconds after that.
	r.baseLog(agyGlogHeader(done.Add(time.Second), 9))
	r.clock.advance(5 * time.Second)
	r.out(agyToolDoneLine)
	r.until("the tool completion", func(c *conv) bool { return c.modelWaitPending == 0 })
	r.expectNoBoundaryNoCut("after a late-delivered tool completion")
}

// expectNoBoundaryNoCut checks an attempt with no boundary: no silence
// timer, and a firing that raced its disarm (run directly) neither cuts nor
// spends a retry.
func (r *stallRig) expectNoBoundaryNoCut(what string) {
	r.t.Helper()
	if b := r.boundaryNow(); !b.IsZero() {
		r.t.Fatalf("%s: boundary = %v, want none", what, b)
	}
	if r.armed() {
		r.t.Fatalf("%s: the silence timer runs with no boundary", what)
	}
	r.clock.advance(10 * time.Minute)
	c := r.conv()
	c.do(func() { c.modelWaitTimedOut() })
	r.sync()
	if got, spent := r.cancels(), r.retries(); got != 0 || spent != 0 {
		r.t.Fatalf("%s: cancels=%d retries=%d, want none", what, got, spent)
	}
}

// The first request of a process: a monitor header verifies the format,
// but until the log shows a main header no firing cuts.
func TestAgyStallCutoffMonitorHeaderAloneNeverCuts(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.monitorOnlyLog()
	r.fireSilence(stallTimeout)
	if got, spent := r.cancels(), r.retries(); got != 0 || spent != 0 {
		t.Fatalf("first request with only monitor headers: cancels=%d retries=%d", got, spent)
	}
	if !strings.Contains(r.h.log.String(), "no main request header yet") {
		t.Fatalf("no notice:\n%s", r.h.log.String())
	}
	// A main header later in this process's log enables the cut.
	r.baseLog()
	r.fireSilence(stallTimeout)
	if got := r.cancels(); got != 1 {
		t.Fatalf("after a main header: cancels = %d, want 1", got)
	}
}

// Hosted agy workers get neither the log-confirmed cutoff nor the frame
// trace, whatever the settings say.
func TestAgyStallCutoffWorkerOptsOut(t *testing.T) {
	setLookPath(t, func(file string) (string, error) { return file, nil })
	h, timers, _ := agyWatchdogHarnessWithSilence(t, "45", "3", false)
	resolver := wsconfig.NewResolver(h.config.Config, nil, nil, nil)
	for _, item := range []string{wsconfig.ItemAgyStallLogCheck, wsconfig.ItemAgyHTTP2FrameTrace} {
		if err := resolver.Set(item, "true", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
	}
	id, _ := startAgyWatchdogTurn(t, h, false)
	c := h.runner.conv(id)
	var lead, armed, logCheck bool
	var timeout time.Duration
	var trace string
	c.do(func() {
		lead, armed, timeout = c.rec.Lead, c.modelWaitArmed, c.modelWaitTimeout
		logCheck, trace = c.modelWaitLogCheck, c.agyFrameTrace
	})
	if lead {
		t.Fatal("the watchdog harness conversation is a lead")
	}
	if !armed || timeout != stallTimeout {
		t.Fatalf("worker model wait: armed=%v timeout=%v", armed, timeout)
	}
	if logCheck || trace != "" {
		t.Fatalf("worker got the cutoff (%v) or the trace (%q)", logCheck, trace)
	}
	if started, _ := timers.counts(); started != 0 {
		t.Fatalf("a worker started %d silence timers", started)
	}
}

func TestAgyStallCutoffMainHeaderReArmsWithoutRetry(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.baseLog(agyGlogHeader(r.boundary.Add(5*time.Second), 9))
	before := r.silenceTimers()
	r.fireSilence(stallTimeout)
	if got := r.cancels(); got != 0 {
		t.Fatalf("a request with headers was interrupted %d times", got)
	}
	if got := r.retries(); got != 0 {
		t.Fatalf("retries spent = %d, want 0", got)
	}
	if !r.armed() || r.silenceTimers() != before+1 {
		t.Fatalf("the silence timer did not run again: armed=%v timers %d -> %d", r.armed(), before, r.silenceTimers())
	}
}

// The long tool-call generation the owner protected: headers, then agent
// text, then silence longer than the timeout.
func TestAgyStallCutoffContentAfterBoundaryNeverCuts(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.baseLog(agyGlogHeader(r.boundary.Add(2*time.Second), 9))
	r.clock.advance(3 * time.Second)
	r.out(agyTextLine)
	r.until("the model content", func(c *conv) bool { return c.modelWaitContent })
	if r.armed() {
		t.Fatal("model content left the silence timer running")
	}
	// Even a log without the header changes nothing once content arrived,
	// including a firing that raced the content (the disarmed timer's
	// channel is no longer read, so the handler runs directly).
	r.baseLog()
	r.clock.advance(10 * time.Minute)
	c := r.conv()
	c.do(func() { c.modelWaitTimedOut() })
	r.sync()
	if got, spent := r.cancels(), r.retries(); got != 0 || spent != 0 {
		t.Fatalf("content then silence: cancels=%d retries=%d, want none", got, spent)
	}
}

// An automatic continue starts a fresh window: the earlier request's header
// does not count, and silence with no new header cuts.
func TestAgyStallCutoffContinueStartsAFreshWindow(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	// The first attempt had headers, then failed in band; agy's failure
	// line takes the unchanged in-band path with its 11-minute drain.
	r.baseLog(agyGlogHeader(r.boundary.Add(5*time.Second), 9))
	r.out(agyUpdateLine(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"\n⚠ API error (attempt 1): stream idle timeout\n"}}`))
	r.until("the in-band failure", func(c *conv) bool { return c.modelWaitFailure != "" })
	r.timers.mu.Lock()
	drain := r.timers.durs[len(r.timers.durs)-1]
	r.timers.mu.Unlock()
	if drain != modelNativeIdleRetryFallback {
		t.Fatalf("in-band drain = %v, want %v", drain, modelNativeIdleRetryFallback)
	}
	r.timers.fire(t, len(r.timers.durs)-1)
	r.sync()
	if got := r.cancels(); got != 1 {
		t.Fatalf("in-band drain cancels = %d, want 1", got)
	}
	continued := r.clock.advance(time.Minute)
	r.cancelAndContinue()
	if b := r.boundaryNow(); !b.Equal(continued) {
		t.Fatalf("boundary = %v, want the automatic continue %v", b, continued)
	}
	// agy logs the continue's session/prompt request line (its user-message
	// line is not guaranteed) and no header after it.
	r.baseLog(agyGlogHeader(r.boundary.Add(5*time.Second), 9), agyGlogRequest(continued, 2, "session/prompt"))
	r.fireSilence(stallTimeout)
	if got, spent := r.cancels(), r.retries(); got != 2 || spent != 1 {
		t.Fatalf("after continue: cancels=%d retries=%d, want a second cut on the shared budget", got, spent)
	}
}

func TestAgyStallCutoffSteerKeepsTheBoundaryAndItsHeader(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.baseLog(agyGlogHeader(r.boundary.Add(5*time.Second), 9))
	r.clock.advance(30 * time.Second)
	r.h.send(r.id, "steer this")
	r.h.steerHeld(r.id)
	r.h.runner.InboxChanged(r.id)
	eventually(t, "the steer", func() bool { return len(agyPromptIDs(r.h, r.holder)) == 2 })
	r.sync()
	if b := r.boundaryNow(); !b.Equal(r.boundary) {
		t.Fatalf("a steer moved the boundary to %v (was %v)", b, r.boundary)
	}
	r.fireSilence(stallTimeout)
	if got := r.cancels(); got != 0 {
		t.Fatalf("a steered request with headers was interrupted %d times", got)
	}
}

// Neither request has headers: a steer one second before the original
// deadline restarts the timer, so the cut comes one full timeout later.
func TestAgyStallCutoffSteerNearTheDeadlineRestartsTheTimer(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.baseLog()
	r.advanceSilence(stallTimeout-time.Second, 0)
	before := r.silenceTimers()
	r.h.send(r.id, "steer this")
	r.h.steerHeld(r.id)
	r.h.runner.InboxChanged(r.id)
	eventually(t, "the steer", func() bool { return len(agyPromptIDs(r.h, r.holder)) == 2 })
	r.sync()
	r.expectSteerRestart(before)
}

// A mail steered into the running turn restarts the timer as an inbox
// steer does.
func TestAgyStallCutoffMailSteerRestartsTheTimer(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.baseLog()
	r.advanceSilence(stallTimeout-time.Second, 0)
	before := r.silenceTimers()
	c := r.conv()
	steered := false
	c.do(func() { steered = c.steerMail(ClaimedMail{Text: "a mail", Sender: "peer"}) })
	if !steered {
		t.Fatal("the mail was not steered")
	}
	r.sync()
	if b := r.boundaryNow(); !b.Equal(r.boundary) {
		t.Fatalf("a mail steer moved the boundary to %v (was %v)", b, r.boundary)
	}
	r.expectSteerRestart(before)
}

// advanceSilence moves the clock by d and expects want timers to fire.
func (r *stallRig) advanceSilence(d time.Duration, want int) {
	r.t.Helper()
	r.sync()
	if got := r.timers.advance(d); got != want {
		r.t.Fatalf("advancing %v fired %d timers, want %d", d, got, want)
	}
	r.sync()
}

// expectSteerRestart checks a steer one second before the deadline: one
// full-length timer replaced the running one, the original deadline passes
// with nothing firing, and the restarted deadline cuts.
func (r *stallRig) expectSteerRestart(before int) {
	r.t.Helper()
	if r.silenceTimers() != before+1 {
		r.t.Fatalf("a steer started %d silence timers, want one restart", r.silenceTimers()-before)
	}
	r.advanceSilence(time.Second, 0)
	if got := r.cancels(); got != 0 {
		r.t.Fatalf("the original deadline cut %d times after the steer", got)
	}
	r.advanceSilence(stallTimeout-2*time.Second, 0)
	r.advanceSilence(time.Second, 1)
	if got := r.cancels(); got != 1 {
		r.t.Fatalf("a full timeout after the steer: cancels = %d, want 1", got)
	}
}

// A background command that outlived an earlier turn completes during the
// next one: it is not a pending call of this turn, so the boundary stays.
func TestAgyStallCutoffBackgroundCompletionKeepsTheBoundary(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.out(agyBgStartLine, agyTextLine, `{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"`+agyPromptIDs(r.h, r.holder)[0]+`"}`)
	eventually(t, "idle with a background task", func() bool {
		st := r.h.state(r.id)
		return st.Status == StatusIdle && st.BackgroundTasks == 1
	})
	second := r.clock.advance(time.Minute)
	r.h.send(r.id, "next")
	eventually(t, "the second prompt", func() bool { return len(agyPromptIDs(r.h, r.holder)) == 2 })
	r.sync()
	r.clock.advance(20 * time.Second)
	r.out(agyBgDoneLine)
	eventually(t, "the background completion", func() bool { return r.h.state(r.id).BackgroundTasks == 0 })
	r.sync()
	if b := r.boundaryNow(); !b.Equal(second) {
		t.Fatalf("a background completion moved the boundary to %v (want %v)", b, second)
	}
}

func TestAgyStallCutoffUnknownLogDoesNotCut(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.fireSilence(stallTimeout)
	r.fireSilence(stallTimeout)
	if got, spent := r.cancels(), r.retries(); got != 0 || spent != 0 {
		t.Fatalf("an unknown log: cancels=%d retries=%d, want none", got, spent)
	}
	if n := strings.Count(r.h.log.String(), "agy stall log check cannot confirm"); n != 1 {
		t.Fatalf("unknown-log notices = %d, want one per session:\n%s", n, r.h.log.String())
	}
	if !r.armed() {
		t.Fatal("the silence timer stopped after an unknown log")
	}
}

func TestAgyStallCutoffPendingToolAndApprovalPause(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.baseLog()
	if !r.armed() {
		t.Fatal("the silence timer is not running after the prompt")
	}
	r.out(agyApprovalReqLine)
	eventually(t, "the approval", func() bool { return len(r.h.state(r.id).Approvals) == 1 })
	r.sync()
	if r.armed() {
		t.Fatal("a pending approval left the silence timer running")
	}
	r.out(agyToolStartLine)
	r.until("the pending tool call", func(c *conv) bool { return c.modelWaitPending == 1 })
	if r.armed() {
		t.Fatal("a pending tool call left the silence timer running")
	}
}

func TestAgyStallCutoffOffKeepsInBandOnly(t *testing.T) {
	r := newStallRig(t, 45, 3, map[string]string{wsconfig.ItemAgyStallLogCheck: "false"})
	r.baseLog()
	if r.silenceTimers() != 0 || r.armed() {
		t.Fatalf("agy.stall_log_check=false started %d silence timers", r.silenceTimers())
	}
	c := r.conv()
	var armed bool
	var timeout time.Duration
	c.do(func() { armed, timeout = c.modelWaitArmed, c.modelWaitTimeout })
	if !armed || timeout != stallTimeout {
		t.Fatalf("in-band recovery must stay armed: armed=%v timeout=%v", armed, timeout)
	}
}

func TestAgyStallCutoffZeroTimeoutNeverArms(t *testing.T) {
	r := newStallRig(t, 0, 3, map[string]string{wsconfig.ItemAgyStallLogCheck: "true"})
	r.baseLog()
	r.timers.mu.Lock()
	started := len(r.timers.durs)
	r.timers.mu.Unlock()
	if started != 0 || r.armed() {
		t.Fatalf("a zero timeout started %d timers", started)
	}
	c := r.conv()
	var logCheck, armed bool
	c.do(func() { logCheck, armed = c.modelWaitLogCheck, c.modelWaitArmed })
	if !logCheck || armed {
		t.Fatalf("log check %v, armed %v", logCheck, armed)
	}
}

func TestAgyFrameTraceOnAddsGODEBUGCaptureAndCutoffMarker(t *testing.T) {
	r := newStallRig(t, 45, 3, map[string]string{wsconfig.ItemAgyHTTP2FrameTrace: "true"})
	want := filepath.Join(wsdashboard.SessionDir(r.h.dir, r.id), AgyFrameLogName)
	r.h.holders.mu.Lock()
	spec := r.h.holders.holders[r.holder].spec
	r.h.holders.mu.Unlock()
	if spec == nil || spec.HTTP2Capture == nil || spec.HTTP2Capture.Path != want || spec.HTTP2Capture.MaxBytes != agyFrameCaptureMaxBytes {
		t.Fatalf("holder spawn capture = %+v, want %s", spec, want)
	}
	if got := spec.Env["GODEBUG"]; got != agyFrameTraceGODEBUG {
		t.Fatalf("agy GODEBUG = %q, want %q", got, agyFrameTraceGODEBUG)
	}
	r.baseLog()
	r.fireSilence(stallTimeout)
	if r.cancels() != 1 {
		t.Fatal("no cutoff")
	}
	// The marker is asked for off the loop, after the interrupt.
	eventually(t, "the cutoff marker", func() bool { return len(r.h.holders.captureNotes(r.holder)) == 1 })
	notes := r.h.holders.captureNotes(r.holder)
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "ws: headerless cutoff boundary="+r.boundary.Local().Format(time.RFC3339Nano)+" cutoff=") {
		t.Fatalf("capture notes = %q", notes)
	}
	// The holder owns the file: the daemon never writes it.
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("the daemon wrote the capture file: %v", err)
	}
}

// The marker follows the running holder's spawn option, not the daemon's
// current setting: a process spawned without a capture gets none after the
// knob turns on, and one spawned with it keeps it after the knob turns off.
func TestAgyFrameTraceMarkerFollowsTheRunningHolder(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	resolver := wsconfig.NewResolver(r.h.opts.Config, nil, nil, nil)
	if err := resolver.Set(wsconfig.ItemAgyHTTP2FrameTrace, "true", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	c := r.conv()
	c.do(func() { c.loadModelWait() })
	r.baseLog()
	r.fireSilence(stallTimeout)
	if r.cancels() != 1 {
		t.Fatal("no cutoff")
	}
	eventually(t, "the marker request", func() bool { return r.h.holders.captureCallCount() == 1 })
	if notes := r.h.holders.captureNotes(r.holder); len(notes) != 0 {
		t.Fatalf("a holder spawned without a capture took notes %q", notes)
	}
	if _, err := os.Stat(filepath.Join(wsdashboard.SessionDir(r.h.dir, r.id), AgyFrameLogName)); !os.IsNotExist(err) {
		t.Fatalf("a capture file appeared: %v", err)
	}

	on := newStallRig(t, 45, 3, map[string]string{wsconfig.ItemAgyHTTP2FrameTrace: "true"})
	resolver = wsconfig.NewResolver(on.h.opts.Config, nil, nil, nil)
	if err := resolver.Set(wsconfig.ItemAgyHTTP2FrameTrace, "false", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	oc := on.conv()
	oc.do(func() { oc.loadModelWait() })
	on.baseLog()
	on.fireSilence(stallTimeout)
	eventually(t, "the marker", func() bool { return len(on.h.holders.captureNotes(on.holder)) == 1 })
	if notes := on.h.holders.captureNotes(on.holder); on.cancels() != 1 || len(notes) != 1 {
		t.Fatalf("knob turned off on a capturing holder: cancels=%d notes=%q", on.cancels(), notes)
	}
}

// The marker is a diagnostic: capture_note begins only after the interrupt
// went out, and while the holder holds it the conversation's loop still
// answers; the note lands once the holder answers.
func TestAgyFrameTraceMarkerNeverDelaysTheInterrupt(t *testing.T) {
	r := newStallRig(t, 45, 3, map[string]string{wsconfig.ItemAgyHTTP2FrameTrace: "true"})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	cancelsAtNote := make(chan int, 1)
	r.h.holders.mu.Lock()
	r.h.holders.captureHook = func() {
		cancelsAtNote <- r.cancels()
		<-release
	}
	r.h.holders.mu.Unlock()
	r.baseLog()
	r.sync()
	r.clock.advance(stallTimeout)
	r.timers.fire(t, r.newestSilenceTimer())
	// No quiesce: a loop held by the marker would never answer one.
	select {
	case n := <-cancelsAtNote:
		if n != 1 {
			t.Fatalf("capture_note began with %d interrupts sent, want it after the interrupt", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no capture_note")
	}
	answered := make(chan struct{})
	go func() {
		c := r.conv()
		c.do(func() {})
		close(answered)
	}()
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("the conversation's loop waits on the blocked capture_note")
	}
	if notes := r.h.holders.captureNotes(r.holder); len(notes) != 0 {
		t.Fatalf("notes %q landed while the holder was blocked", notes)
	}
	unblock()
	eventually(t, "the marker after the holder answers", func() bool { return len(r.h.holders.captureNotes(r.holder)) == 1 })
}

func TestAgyFrameTraceOffAddsNeither(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.h.holders.mu.Lock()
	spec := r.h.holders.holders[r.holder].spec
	r.h.holders.mu.Unlock()
	if spec == nil || spec.HTTP2Capture != nil {
		t.Fatalf("holder spawn capture = %+v, want none", spec)
	}
	if got, ok := spec.Env["GODEBUG"]; ok {
		t.Fatalf("agy GODEBUG = %q, want unset", got)
	}
}

func TestWithGODEBUGAppendsToExistingSettings(t *testing.T) {
	if got := withGODEBUG([]string{"A=1"}, "http2debug=2"); !slices.Equal(got, []string{"A=1", "GODEBUG=http2debug=2"}) {
		t.Fatalf("fresh = %q", got)
	}
	if got := withGODEBUG([]string{"GODEBUG=madvdontneed=1", "B=2"}, "http2debug=2"); !slices.Equal(got, []string{"GODEBUG=madvdontneed=1,http2debug=2", "B=2"}) {
		t.Fatalf("merged = %q", got)
	}
	spec := StartSpec{Env: []string{"PATH=/bin"}, Lead: &LeadSpec{}}
	if env := agyEnv(spec); slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "GODEBUG=") }) {
		t.Fatalf("trace off env = %q", env)
	}
	spec.Lead.FrameTrace = "/tmp/frames.log"
	if env := agyEnv(spec); !slices.Contains(env, "GODEBUG=http2debug=2") {
		t.Fatalf("trace on env = %q", env)
	}
}
