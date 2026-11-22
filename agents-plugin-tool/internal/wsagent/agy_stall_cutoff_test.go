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

type stallRig struct {
	t      *testing.T
	h      *leadHarness
	timers *leverTimers
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
	r := &stallRig{t: t, h: h, timers: &leverTimers{}, logDir: t.TempDir()}
	r.clock = &stallClock{now: time.Now().Truncate(time.Second)}
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

// baseLog is a verified tail with no main header: covered from a minute
// before the boundary, naming the session, and holding one monitor header.
func (r *stallRig) baseLog(extra ...string) {
	b := r.boundary
	lines := []string{
		agyGlogStart(b.Add(-time.Minute), 1),
		agyGlogUserMessage(b, 2, "agy-s1"),
		agyGlogMonitor(b.Add(time.Second), 7),
		agyGlogHeader(b.Add(time.Second), 7),
	}
	r.writeLog(append(lines, extra...)...)
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
	r.timers.fire(r.t, idx)
	r.sync()
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

func TestAgyStallCutoffAfterToolCompletionWithNoNewHeader(t *testing.T) {
	r := newStallRig(t, 45, 3, nil)
	r.out(agyToolStartLine)
	r.until("the pending tool call", func(c *conv) bool { return c.modelWaitPending == 1 })
	if r.armed() {
		t.Fatal("a pending tool call left the silence timer running")
	}
	// The request that produced the tool call had headers before the
	// tool finished; they do not count for the request after it.
	r.baseLog(agyGlogHeader(r.boundary.Add(5*time.Second), 9))
	done := r.clock.advance(100 * time.Second)
	r.out(agyToolDoneLine)
	r.until("the tool completion", func(c *conv) bool { return c.modelWaitPending == 0 })
	if b := r.boundaryNow(); !b.Equal(done) {
		t.Fatalf("boundary = %v, want the tool completion %v", b, done)
	}
	r.fireSilence(stallTimeout)
	if got := r.cancels(); got != 1 {
		t.Fatalf("cancels = %d, want one interrupt after the tool's request stalled", got)
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
	r.timers.mu.Lock()
	original := slices.Index(r.timers.durs, stallTimeout)
	r.timers.mu.Unlock()
	r.clock.advance(stallTimeout - time.Second)
	before := r.silenceTimers()
	r.h.send(r.id, "steer this")
	r.h.steerHeld(r.id)
	r.h.runner.InboxChanged(r.id)
	eventually(t, "the steer", func() bool { return len(agyPromptIDs(r.h, r.holder)) == 2 })
	r.sync()
	if r.silenceTimers() != before+1 {
		t.Fatalf("a steer started %d silence timers, want one restart", r.silenceTimers()-before)
	}
	// The original deadline passes: its timer was replaced.
	r.clock.advance(time.Second)
	r.timers.fire(t, original)
	r.sync()
	if got := r.cancels(); got != 0 {
		t.Fatalf("the original deadline cut %d times after the steer", got)
	}
	r.fireSilence(stallTimeout - time.Second)
	if got := r.cancels(); got != 1 {
		t.Fatalf("a full timeout after the steer: cancels = %d, want 1", got)
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
	c.do(func() {
		if !c.modelWaitArmed || c.modelWaitTimeout != stallTimeout {
			t.Fatalf("in-band recovery must stay armed: armed=%v timeout=%v", c.modelWaitArmed, c.modelWaitTimeout)
		}
	})
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
	c.do(func() {
		if !c.modelWaitLogCheck || c.modelWaitArmed {
			t.Fatalf("log check %v, armed %v", c.modelWaitLogCheck, c.modelWaitArmed)
		}
	})
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
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "ws: headerless cutoff boundary="+r.boundary.Local().Format(time.RFC3339Nano)+" cutoff="); n != 1 {
		t.Fatalf("capture markers = %q", data)
	}
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
