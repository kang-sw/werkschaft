package werk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsfs"
)

// scripted is a model endpoint that answers each request from the next step.
type scripted struct {
	mu    sync.Mutex
	reqs  []Request
	steps []func(ctx context.Context, req Request, h StreamHandler) (*Response, error)
}

func (s *scripted) Respond(ctx context.Context, req Request, h StreamHandler) (*Response, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	n := len(s.reqs) - 1
	var step func(ctx context.Context, req Request, h StreamHandler) (*Response, error)
	if n < len(s.steps) {
		step = s.steps[n]
	}
	s.mu.Unlock()
	if step == nil {
		return nil, fmt.Errorf("unscripted request %d", n+1)
	}
	return step(ctx, req, h)
}

func (s *scripted) requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

func reply(items ...string) func(context.Context, Request, StreamHandler) (*Response, error) {
	return func(context.Context, Request, StreamHandler) (*Response, error) {
		out := make([]json.RawMessage, len(items))
		for i, it := range items {
			out[i] = json.RawMessage(it)
		}
		cost := 0.002
		return &Response{ID: "r", Output: out, Usage: Usage{InputTokens: 50, OutputTokens: 10, CachedTokens: 5, ReasoningTokens: 3, CostUSD: &cost}}, nil
	}
}

func failWith(err error) func(context.Context, Request, StreamHandler) (*Response, error) {
	return func(context.Context, Request, StreamHandler) (*Response, error) { return nil, err }
}

func msgItem(text string) string {
	return `{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":` + jsonString(text) + `}]}`
}

func callOf(id, name, args string) string {
	return `{"type":"function_call","id":"fc_` + id + `","call_id":"` + id + `","name":"` + name + `","arguments":` + jsonString(args) + `}`
}

func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// fakeWS is the daemon's ws tool surface.
type fakeWS struct {
	mu      sync.Mutex
	tools   []MCPTool
	calls   []string
	running int
	peak    int
	hold    time.Duration
	byName  map[string]func(ctx context.Context, args json.RawMessage) (CallResult, error)
}

func (w *fakeWS) ListTools(context.Context) ([]MCPTool, error) { return w.tools, nil }

func (w *fakeWS) Call(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	w.mu.Lock()
	w.calls = append(w.calls, name)
	w.running++
	w.peak = max(w.peak, w.running)
	fn := w.byName[name]
	hold := w.hold
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.running--
		w.mu.Unlock()
	}()
	if fn != nil {
		return fn(ctx, args)
	}
	if hold > 0 {
		select {
		case <-time.After(hold):
		case <-ctx.Done():
			return CallResult{}, ctx.Err()
		}
	}
	return CallResult{Text: "ran " + name}, nil
}

func (w *fakeWS) callNames() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) lines() []Line {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Line
	for _, raw := range bytes.Split(s.b.Bytes(), []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		var l Line
		if json.Unmarshal(raw, &l) == nil {
			out = append(out, l)
		}
	}
	return out
}

type fileToolFixture struct{ files wsfs.Files }

func (fileToolFixture) ListTools(context.Context) ([]MCPTool, error) {
	return []MCPTool{{Name: "fs.read"}, {Name: "fs.write"}}, nil
}

func (f fileToolFixture) Call(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	switch name {
	case "fs.read":
		var in struct{ Path string }
		if err := json.Unmarshal(args, &in); err != nil {
			return CallResult{}, err
		}
		data, err := os.ReadFile(filepath.Join(f.files.Cwd, in.Path))
		return CallResult{Text: string(data)}, err
	case "fs.write":
		text, err := f.files.Write(ctx, args)
		return CallResult{Text: text}, err
	default:
		return CallResult{}, fmt.Errorf("unexpected file tool %q", name)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type rig struct {
	t      *testing.T
	agent  *Agent
	in     *io.PipeWriter
	out    *syncBuf
	done   chan error
	cancel context.CancelFunc
	once   sync.Once
}

func newRig(t *testing.T, cfg Config) *rig {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	if cfg.Authority == "" {
		cfg.Authority = AuthorityFull
	}
	if cfg.Root == "" {
		cfg.Root = t.TempDir()
	}
	if cfg.Cwd == "" {
		cfg.Cwd = cfg.Root
	}
	if cfg.Model == "" {
		cfg.Model = "vendor/model"
	}
	if cfg.WS == nil {
		cfg.WS = fileToolFixture{files: wsfs.Files{Root: cfg.Root, Cwd: cfg.Cwd, Authority: cfg.Authority}}
	}
	out := &syncBuf{}
	cfg.Out = out
	agent, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{t: t, agent: agent, in: pw, out: out, done: make(chan error, 1), cancel: cancel}
	go func() { r.done <- agent.Serve(ctx, pr) }()
	t.Cleanup(func() {
		r.stop()
		agent.Close()
	})
	return r
}

func (r *rig) send(c Command) {
	r.t.Helper()
	raw, _ := json.Marshal(c)
	if _, err := r.in.Write(append(raw, '\n')); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) user(text string) { r.send(Command{Type: CmdUser, Text: text}) }

func (r *rig) stop() {
	r.once.Do(func() {
		r.in.Close()
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			r.cancel()
			<-r.done
		}
	})
}

// waitFor returns the first line matching pred, polling the output.
func (r *rig) waitFor(desc string, pred func(Line) bool) Line {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range r.out.lines() {
			if pred(l) {
				return l
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("no %s line; output: %v", desc, r.out.lines())
	return Line{}
}

func (r *rig) waitTurnEnd(n int) Line {
	r.t.Helper()
	return r.waitFor(fmt.Sprintf("turn_end %d", n), func(l Line) bool { return l.T == LineTurnEnd && l.Turn == n })
}

func types(lines []Line) []string {
	var out []string
	for _, l := range lines {
		out = append(out, l.T)
	}
	return out
}

func TestLoopRunsToolsAndReplaysReasoningUnchanged(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reasoning := `{"type":"reasoning","id":"rs_9","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"AAAA=="}`
	call := callOf("call_a", "fs_read", `{"path":"a.txt"}`)
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(reasoning, call),
		reply(msgItem("the file says hello")),
	}}
	r := newRig(t, Config{Root: root, Endpoint: model, Effort: "high", Instructions: "task text"})
	r.user("read a.txt")
	end := r.waitTurnEnd(1)
	if end.Reason != ReasonDone {
		t.Fatalf("turn end = %+v", end)
	}

	reqs := model.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d", len(reqs))
	}
	if !strings.Contains(reqs[0].Instructions, "task text") || !strings.Contains(reqs[0].Instructions, "full") {
		t.Errorf("instructions = %q", reqs[0].Instructions)
	}
	// The second request carries the whole conversation: user, reasoning
	// exactly as sent, the call, and its result, in that order.
	in := reqs[1].Input
	if len(in) != 4 || string(in[1]) != reasoning || string(in[2]) != call {
		t.Fatalf("second request input = %s", in)
	}
	var out struct{ Type, CallID, Output string }
	_ = json.Unmarshal(in[3], &struct {
		Type   *string `json:"type"`
		CallID *string `json:"call_id"`
		Output *string `json:"output"`
	}{&out.Type, &out.CallID, &out.Output})
	if out.Type != "function_call_output" || out.CallID != "call_a" || !strings.Contains(out.Output, "hello file") {
		t.Errorf("tool result item = %s", in[3])
	}

	got := types(r.out.lines())
	want := []string{LineInit, LineTurnStart, LineUser, LineUsage, LineReasoning, LineToolStart, LineToolEnd, LineUsage, LineReply, LineTurnEnd}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("lines = %v, want %v", got, want)
	}
	usage := r.waitFor("usage", func(l Line) bool { return l.T == LineUsage })
	if usage.InputTokens != 50 || usage.CostUSD == nil || *usage.CostUSD != 0.002 || usage.CachedTokens != 5 {
		t.Errorf("usage line = %+v", usage)
	}
	reply := r.waitFor("reply", func(l Line) bool { return l.T == LineReply && l.Final })
	if reply.Text != "the file says hello" {
		t.Errorf("reply = %+v", reply)
	}

	// The conversation is on disk: a process that resumes the session finds
	// the same items.
	r.stop()
	r.agent.Close()
	_, recovered, err := OpenStore(r.agent.cfg.Dir, r.agent.Session(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 5 || string(recovered[1]) != reasoning {
		t.Errorf("recovered %d items: %s", len(recovered), recovered)
	}
}

func TestReadOnlyCallsRunTogetherOthersAlone(t *testing.T) {
	ws := &fakeWS{hold: 60 * time.Millisecond, tools: []MCPTool{
		{Name: "git.status"}, {Name: "git.log"}, {Name: "exec.shell"}, {Name: "exec.run"},
	}}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(
			callOf("c1", "git_status", `{}`), callOf("c2", "git_log", `{}`),
			callOf("c3", "exec_shell", `{"cmd":"a"}`), callOf("c4", "exec_run", `{"exe":"b"}`),
			callOf("c5", "git_status", `{}`),
		),
		reply(msgItem("done")),
	}}
	r := newRig(t, Config{Endpoint: model, WS: ws})
	r.user("go")
	r.waitTurnEnd(1)
	ws.mu.Lock()
	peak, calls := ws.peak, append([]string(nil), ws.calls...)
	ws.mu.Unlock()
	if peak != 2 {
		t.Errorf("peak concurrency = %d, want 2 (the two leading read-only calls together, every other call alone)", peak)
	}
	if got := strings.Join(calls, ","); !strings.HasPrefix(got, "git.status,git.log") && !strings.HasPrefix(got, "git.log,git.status") {
		t.Errorf("call order = %s", got)
	} else if !strings.HasSuffix(got, "exec.shell,exec.run,git.status") {
		t.Errorf("mutating calls did not run serially in order: %s", got)
	}
	// Every call has its result, in the transcript the next request carries.
	in := model.requests()[1].Input
	var results int
	for _, it := range in {
		if strings.Contains(string(it), `"function_call_output"`) {
			results++
		}
	}
	if results != 5 {
		t.Errorf("results = %d", results)
	}
}

func TestCutStreamRetryNeverRerunsACompletedCall(t *testing.T) {
	ws := &fakeWS{tools: []MCPTool{{Name: "exec.shell"}}}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(callOf("c1", "exec_shell", `{"cmd":"make"}`)),
		failWith(ErrStreamCut),
		failWith(fmt.Errorf("%w: reset", ErrStreamCut)),
		reply(msgItem("built")),
	}}
	var slept []time.Duration
	r := newRig(t, Config{Endpoint: model, WS: ws, Backoff: func(n int) time.Duration { return time.Duration(n) * time.Second },
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }})
	r.user("build it")
	end := r.waitTurnEnd(1)
	if end.Reason != ReasonDone {
		t.Fatalf("turn end = %+v", end)
	}
	if names := ws.callNames(); len(names) != 1 {
		t.Fatalf("the shell call ran %d times across the retries: %v", len(names), names)
	}
	if len(slept) != 2 || slept[0] != time.Second || slept[1] != 2*time.Second {
		t.Errorf("backoff waits = %v", slept)
	}
	// Each retried request carries the same conversation, including the
	// completed call's result.
	reqs := model.requests()
	if len(reqs) != 4 || len(reqs[1].Input) != 3 || len(reqs[3].Input) != 3 {
		t.Fatalf("requests = %d, retried input = %v", len(reqs), reqs[len(reqs)-1].Input)
	}
	var retries int
	for _, l := range r.out.lines() {
		if l.T == LineRetry {
			retries++
		}
	}
	if retries != 2 {
		t.Errorf("retry lines = %d", retries)
	}
}

func TestCutBeforeCompletionRunsNothing(t *testing.T) {
	ws := &fakeWS{tools: []MCPTool{{Name: "exec.shell"}}}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		failWith(ErrStreamCut),
		reply(msgItem("nothing to do")),
	}}
	r := newRig(t, Config{Endpoint: model, WS: ws, Sleep: func(context.Context, time.Duration) error { return nil }})
	r.user("hi")
	r.waitTurnEnd(1)
	if names := ws.callNames(); len(names) != 0 {
		t.Errorf("tools ran for a response that never completed: %v", names)
	}
}

func TestRetryAfterIsHonored(t *testing.T) {
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		failWith(&HTTPError{Status: 429, Message: "slow down", RetryAfter: 7 * time.Second}),
		reply(msgItem("ok")),
	}}
	var slept []time.Duration
	r := newRig(t, Config{Endpoint: model, Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }})
	r.user("hi")
	end := r.waitTurnEnd(1)
	if end.Reason != ReasonDone || len(slept) != 1 || slept[0] != 7*time.Second {
		t.Fatalf("end = %+v, slept = %v", end, slept)
	}
	retry := r.waitFor("retry", func(l Line) bool { return l.T == LineRetry })
	if retry.Status != 429 || retry.DelayMS != 7000 || retry.Attempt != 1 {
		t.Errorf("retry line = %+v", retry)
	}
}

func TestRetriesAreBounded(t *testing.T) {
	model := &scripted{}
	for range 10 {
		model.steps = append(model.steps, failWith(&HTTPError{Status: 503, Message: "down"}))
	}
	r := newRig(t, Config{Endpoint: model, MaxRetries: 2, Sleep: func(context.Context, time.Duration) error { return nil }})
	r.user("hi")
	end := r.waitTurnEnd(1)
	if end.Reason != ReasonError || !strings.Contains(end.Error, "503") {
		t.Fatalf("end = %+v", end)
	}
	if n := len(model.requests()); n != 3 {
		t.Errorf("requests = %d, want 1 + 2 retries", n)
	}
}

func TestFailedResponseEndsTheTurnWithItsError(t *testing.T) {
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		failWith(&FailedError{Code: "invalid_prompt", Message: "the prompt was refused"}),
	}}
	r := newRig(t, Config{Endpoint: model})
	r.user("hi")
	end := r.waitTurnEnd(1)
	if end.Reason != ReasonError || !strings.Contains(end.Error, "the prompt was refused") {
		t.Fatalf("end = %+v", end)
	}
	if n := len(model.requests()); n != 1 {
		t.Errorf("a non-retryable failure was sent %d times", n)
	}
	// The next turn still works: the session stays.
	model.mu.Lock()
	model.steps = append(model.steps, reply(msgItem("recovered")))
	model.mu.Unlock()
	r.user("again")
	if end := r.waitTurnEnd(2); end.Reason != ReasonDone {
		t.Errorf("second turn = %+v", end)
	}
}

func TestInterruptEndsTheTurnAndAnswersTheCutCall(t *testing.T) {
	started := make(chan struct{})
	ws := &fakeWS{tools: []MCPTool{{Name: "exec.shell"}}, byName: map[string]func(context.Context, json.RawMessage) (CallResult, error){
		"exec.shell": func(ctx context.Context, _ json.RawMessage) (CallResult, error) {
			close(started)
			<-ctx.Done()
			return CallResult{}, ctx.Err()
		},
	}}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(callOf("c1", "exec_shell", `{"cmd":"sleep 99"}`), callOf("c2", "exec_run", `{}`)),
		reply(msgItem("after the interrupt")),
	}}
	ws.tools = append(ws.tools, MCPTool{Name: "exec.run"})
	r := newRig(t, Config{Endpoint: model, WS: ws})
	r.user("run")
	<-started
	r.send(Command{Type: CmdInterrupt})
	end := r.waitTurnEnd(1)
	if end.Reason != ReasonAborted {
		t.Fatalf("end = %+v", end)
	}
	// Neither call is left without a result, and the second never ran.
	items := r.agent.Transcript()
	if p := pendingCalls(items); len(p) != 0 {
		t.Errorf("calls without a result: %v", p)
	}
	if names := ws.callNames(); len(names) != 1 {
		t.Errorf("calls run = %v", names)
	}
	// The session takes the next turn on that transcript.
	r.user("go on")
	if end := r.waitTurnEnd(2); end.Reason != ReasonDone {
		t.Errorf("turn 2 = %+v", end)
	}
}

func TestSteerIsTakenAtTheNextRequestAndRefusedWhenIdle(t *testing.T) {
	gate := make(chan struct{})
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		func(ctx context.Context, req Request, h StreamHandler) (*Response, error) {
			<-gate
			return reply(callOf("c1", "fs_read", `{"path":"x"}`))(ctx, req, h)
		},
		reply(msgItem("noted")),
	}}
	r := newRig(t, Config{Endpoint: model})
	r.send(Command{Type: CmdSteer, ID: "s0", Text: "too early"})
	if ack := r.waitFor("refused ack", func(l Line) bool { return l.T == LineSteerAck && l.ID == "s0" }); ack.Refusal == "" {
		t.Errorf("an idle steer was accepted: %+v", ack)
	}
	r.user("work")
	// The first request is in flight (blocked on the gate): the steer can
	// only be taken by the request after it.
	for deadline := time.Now().Add(5 * time.Second); len(model.requests()) < 1 && time.Now().Before(deadline); {
		time.Sleep(2 * time.Millisecond)
	}
	r.send(Command{Type: CmdSteer, ID: "s1", Text: "also do X"})
	if ack := r.waitFor("ack", func(l Line) bool { return l.T == LineSteerAck && l.ID == "s1" }); ack.Refusal != "" {
		t.Fatalf("ack = %+v", ack)
	}
	close(gate)
	r.waitTurnEnd(1)
	taken := r.waitFor("user_taken", func(l Line) bool { return l.T == LineUserTaken })
	if taken.Text != "also do X" {
		t.Errorf("taken = %+v", taken)
	}
	in := model.requests()[1].Input
	last := string(in[len(in)-1])
	if !strings.Contains(last, "also do X") {
		t.Errorf("the steered message is not in the next request: %s", last)
	}
}

func TestUntakenSteerIsDroppedAtTurnEnd(t *testing.T) {
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(msgItem("done")),
		reply(msgItem("second")),
	}}
	r := newRig(t, Config{Endpoint: model})
	r.user("one")
	r.waitTurnEnd(1)
	// A steer that no request took does not leak into a later turn.
	r.agent.mu.Lock()
	r.agent.steers = []Steer{{Text: "stale"}}
	r.agent.mu.Unlock()
	r.send(Command{Type: CmdClearSteered})
	r.user("two")
	r.waitTurnEnd(2)
	for _, it := range model.requests()[1].Input {
		if strings.Contains(string(it), "stale") {
			t.Fatalf("a dropped steer reached a request: %s", it)
		}
	}
}

func TestResumeAnswersAPendingCallWithoutRunningIt(t *testing.T) {
	dir := t.TempDir()
	session := NewSessionID()
	store, _, err := OpenStore(dir, session, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := callOf("c9", "exec_shell", `{"cmd":"rm -rf build"}`)
	for _, it := range []json.RawMessage{userItem("clean"), json.RawMessage(call)} {
		if err := store.Append(it); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()

	ws := &fakeWS{tools: []MCPTool{{Name: "exec.shell"}}}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){reply(msgItem("ok"))}}
	r := newRig(t, Config{Dir: dir, Session: session, Endpoint: model, WS: ws})
	init := r.waitFor("init", func(l Line) bool { return l.T == LineInit })
	if init.Session != session || init.Resumed != 2 || init.Version != ProtocolVersion {
		t.Errorf("init = %+v", init)
	}
	r.waitFor("repair notice", func(l Line) bool { return l.T == LineNotice && strings.Contains(l.Text, "not run again") })
	r.user("continue")
	r.waitTurnEnd(1)
	if names := ws.callNames(); len(names) != 0 {
		t.Fatalf("the interrupted call ran again: %v", names)
	}
	in := model.requests()[0].Input
	if len(in) != 4 || !strings.Contains(string(in[2]), "function_call_output") || !strings.Contains(string(in[2]), "did not finish") {
		t.Errorf("first request after resume = %s", in)
	}
}

func TestSetChangesModelAndEffortForTheNextRequest(t *testing.T) {
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){reply(msgItem("a")), reply(msgItem("b"))}}
	r := newRig(t, Config{Endpoint: model, Effort: "low"})
	r.user("one")
	r.waitTurnEnd(1)
	none := ""
	r.send(Command{Type: CmdSet, Model: "vendor/other", Effort: &none})
	r.user("two")
	r.waitTurnEnd(2)
	reqs := model.requests()
	if reqs[0].Model != "vendor/model" || reqs[0].Effort != "low" || reqs[1].Model != "vendor/other" || reqs[1].Effort != "" {
		t.Errorf("requests = %+v / %+v", reqs[0], reqs[1])
	}
}

func TestWSToolNamesAreSanitizedAndUnknownToolsFail(t *testing.T) {
	if got := ModelToolName("exec.shell"); got != "exec_shell" {
		t.Errorf("ModelToolName = %q", got)
	}
	if got := ModelToolName("a.b/c d"); got != "a_b_c_d" {
		t.Errorf("ModelToolName = %q", got)
	}
	var peeked atomic.Value
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		func(ctx context.Context, req Request, h StreamHandler) (*Response, error) {
			var names []string
			for _, d := range req.Tools {
				names = append(names, d.Name)
			}
			peeked.Store(strings.Join(names, ","))
			return reply(callOf("c1", "no_such_tool", `{}`))(ctx, req, h)
		},
		reply(msgItem("ok")),
	}}
	ws := &fakeWS{tools: []MCPTool{{Name: "exec.shell"}, {Name: "agent.spawn"}}}
	r := newRig(t, Config{Endpoint: model, WS: ws})
	r.user("hi")
	r.waitTurnEnd(1)
	if got, _ := peeked.Load().(string); got != "exec_shell,agent_spawn" {
		t.Errorf("offered tools = %q", got)
	}
	end := r.waitFor("tool_end", func(l Line) bool { return l.T == LineToolEnd })
	if !end.Failed || !strings.Contains(end.Output, "unknown tool") {
		t.Errorf("tool_end = %+v", end)
	}
}

func TestLargeToolOutputIsTruncatedForModelAndLine(t *testing.T) {
	ws := &fakeWS{tools: []MCPTool{{Name: "exec.shell"}}, byName: map[string]func(context.Context, json.RawMessage) (CallResult, error){
		"exec.shell": func(context.Context, json.RawMessage) (CallResult, error) {
			return CallResult{Text: strings.Repeat("x", 200<<10)}, nil
		},
	}}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(callOf("c1", "exec_shell", `{}`)), reply(msgItem("ok")),
	}}
	r := newRig(t, Config{Endpoint: model, WS: ws})
	r.user("hi")
	r.waitTurnEnd(1)
	end := r.waitFor("tool_end", func(l Line) bool { return l.T == LineToolEnd })
	if !end.Truncated || len(end.Output) > ToolOutputBytes+200 {
		t.Errorf("tool_end output = %d bytes, truncated %v", len(end.Output), end.Truncated)
	}
	in := model.requests()[1].Input
	if got := len(in[len(in)-1]); got > ToolOutputBytes+400 {
		t.Errorf("the model saw %d bytes", got)
	}
}

func TestDeliveryUnknownCallIsNotRepeatedAndSaysSo(t *testing.T) {
	ws := &fakeWS{tools: []MCPTool{{Name: "exec.shell"}}, byName: map[string]func(context.Context, json.RawMessage) (CallResult, error){
		"exec.shell": func(context.Context, json.RawMessage) (CallResult, error) {
			return CallResult{}, fmt.Errorf("%w: reset", ErrDeliveryUnknown)
		},
	}}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(callOf("c1", "exec_shell", `{}`)), reply(msgItem("ok")),
	}}
	r := newRig(t, Config{Endpoint: model, WS: ws})
	r.user("hi")
	r.waitTurnEnd(1)
	end := r.waitFor("tool_end", func(l Line) bool { return l.T == LineToolEnd })
	if !end.Failed || !strings.Contains(end.Output, "may or may not have taken effect") {
		t.Errorf("tool_end = %+v", end)
	}
	if n := len(ws.callNames()); n != 1 {
		t.Errorf("calls = %d", n)
	}
}

func TestNewRefusesAWorkerWithNoAuthority(t *testing.T) {
	_, err := New(Config{Dir: t.TempDir(), Endpoint: &scripted{}, Out: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(context.Canceled, context.Canceled) {
		t.Fatal("unreachable")
	}
}

// A read-only worker is offered no exec launch tool, and a forced call to one
// is refused without reaching the daemon, whose hosted exec approves every
// worker. Its read tools stay; workdir-write and full keep the shell.
func TestReadOnlyWorkerHasNoExecLaunchTools(t *testing.T) {
	tools := []MCPTool{
		{Name: "exec.shell"}, {Name: "exec.run"}, {Name: "exec.spawn"}, {Name: "exec.write"},
		{Name: "exec.list"}, {Name: "exec.tail"}, {Name: "git.status"},
		{Name: "fs.read"}, {Name: "fs.grep"}, {Name: "fs.list"},
	}
	offered := func(req Request) map[string]bool {
		m := map[string]bool{}
		for _, d := range req.Tools {
			m[d.Name] = true
		}
		return m
	}

	ws := &fakeWS{tools: tools}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(callOf("c1", "exec_shell", `{"cmd":"touch x"}`), callOf("c2", "exec_run", `{"exe":"touch"}`)),
		reply(msgItem("done")),
	}}
	r := newRig(t, Config{Endpoint: model, WS: ws, Authority: AuthorityReadOnly})
	r.user("go")
	r.waitTurnEnd(1)
	reqs := model.requests()
	got := offered(reqs[0])
	for _, n := range []string{"exec_shell", "exec_run", "exec_spawn", "exec_write"} {
		if got[n] {
			t.Errorf("a read-only worker is offered %s", n)
		}
	}
	for _, n := range []string{"fs_read", "fs_grep", "fs_list", "git_status", "exec_list", "exec_tail"} {
		if !got[n] {
			t.Errorf("a read-only worker lost %s", n)
		}
	}
	if strings.Contains(reqs[0].Instructions, "exec_shell") {
		t.Errorf("the instructions send a read-only worker to a shell it lacks: %q", reqs[0].Instructions)
	}
	if calls := ws.callNames(); len(calls) != 0 {
		t.Errorf("a forced exec call reached the daemon: %v", calls)
	}
	for _, c := range []string{"c1", "c2"} {
		var out string
		for _, it := range reqs[1].Input {
			var m map[string]any
			if json.Unmarshal(it, &m) == nil && m["type"] == "function_call_output" && m["call_id"] == c {
				out, _ = m["output"].(string)
			}
		}
		if !strings.Contains(out, "read-only") {
			t.Errorf("call %s answered %q, want a read-only refusal", c, out)
		}
	}

	for _, auth := range []Authority{AuthorityWorkdirWrite, AuthorityFull} {
		m := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){reply(msgItem("ok"))}}
		rr := newRig(t, Config{Endpoint: m, WS: &fakeWS{tools: tools}, Authority: auth})
		rr.user("go")
		rr.waitTurnEnd(1)
		if got := offered(m.requests()[0]); !got["exec_shell"] || !got["exec_run"] {
			t.Errorf("%s lost the shell: %v", auth, got)
		}
	}
}

// A delayed log write does not hold the loop: the model's next request and
// the turn's end do not wait for the disk, and once the writer catches up the
// log holds every item in the order of the transcript.
func TestSlowLogWriterDoesNotBlockTheLoopAndKeepsOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var mu sync.Mutex
	var written []string
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(callOf("c1", "fs_read", `{"path":"a.txt"}`), callOf("c2", "fs_read", `{"path":"a.txt"}`)),
		reply(msgItem("done")),
	}}
	r := newRig(t, Config{Root: root, Endpoint: model, BeforeAppend: func(item json.RawMessage) error {
		<-release
		mu.Lock()
		written = append(written, string(item))
		mu.Unlock()
		return nil
	}})
	r.user("go")
	// The writer is held at its first item; the loop still makes both model
	// requests and runs its reads. Only the turn's end waits: it is reported
	// once the log is whole.
	deadline := time.Now().Add(5 * time.Second)
	for len(model.requests()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(model.requests()) != 2 {
		t.Fatalf("the loop waited for the log: %d requests", len(model.requests()))
	}
	time.Sleep(100 * time.Millisecond)
	for _, l := range r.out.lines() {
		if l.T == LineTurnEnd {
			t.Fatal("the turn end was reported before the log was flushed")
		}
	}
	mu.Lock()
	if len(written) != 0 {
		t.Errorf("%d items were written while the writer was held", len(written))
	}
	mu.Unlock()

	close(release)
	if end := r.waitTurnEnd(1); end.Reason != ReasonDone {
		t.Fatalf("turn end %+v", end)
	}
	r.stop()
	if err := r.agent.Close(); err != nil {
		t.Fatal(err)
	}
	transcript := r.agent.Transcript()
	mu.Lock()
	defer mu.Unlock()
	if len(written) != len(transcript) || len(transcript) != 6 {
		t.Fatalf("written %d items, transcript %d (want 6: user, 2 calls, 2 results, reply)", len(written), len(transcript))
	}
	for i := range transcript {
		if written[i] != string(transcript[i]) {
			t.Errorf("item %d out of order or changed:\n written %s\n memory  %s", i, written[i], transcript[i])
		}
	}
	_, recovered, err := OpenStore(r.agent.cfg.Dir, r.agent.Session(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != len(transcript) {
		t.Errorf("recovered %d items, want %d", len(recovered), len(transcript))
	}
}

// A call that may change something waits for the log to hold its
// function_call line; a read does not. With the writer held, the read runs
// and the write does not, until the writer is released.
func TestMutatingCallWaitsForDurableCallIntent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(callOf("c1", "fs_read", `{"path":"a.txt"}`)),
		reply(callOf("c2", "fs_write", `{"path":"b.txt","content":"x"}`)),
		reply(msgItem("done")),
	}}
	r := newRig(t, Config{Root: root, Endpoint: model, BeforeAppend: func(json.RawMessage) error { <-release; return nil }})
	r.user("go")
	// The read finishes while the writer is held: two requests were made.
	deadline := time.Now().Add(5 * time.Second)
	for len(model.requests()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(model.requests()) < 2 {
		t.Fatalf("a read call waited for the log: %d requests", len(model.requests()))
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err == nil {
		t.Fatal("the write ran before its function_call line was durable")
	}
	open()
	if end := r.waitTurnEnd(1); end.Reason != ReasonDone {
		t.Fatalf("turn end %+v", end)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err != nil {
		t.Fatalf("the write did not run after the log caught up: %v", err)
	}
}

// A log that cannot be written fails closed: a call that may change something
// is not run, and the model is told why; a read still runs.
func TestMutatingCallIsRefusedWhenTheLogFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(callOf("c1", "fs_read", `{"path":"a.txt"}`), callOf("c2", "fs_write", `{"path":"b.txt","content":"x"}`)),
		reply(msgItem("done")),
	}}
	r := newRig(t, Config{Root: root, Endpoint: model, BeforeAppend: func(json.RawMessage) error {
		return errors.New("disk full")
	}})
	r.user("go")
	if end := r.waitTurnEnd(1); end.Reason != ReasonDone {
		t.Fatalf("turn end %+v", end)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err == nil {
		t.Fatal("the write ran although its intent could not be persisted")
	}
	outputs := map[string]string{}
	for _, l := range r.out.lines() {
		if l.T == LineToolEnd {
			outputs[l.Call] = l.Output
			if l.Call == "c2" && !l.Failed {
				t.Error("the refused call is not marked failed")
			}
		}
	}
	if out := outputs["c2"]; !strings.Contains(out, "disk full") || !strings.Contains(out, "not run") {
		t.Errorf("the refusal does not name the log failure: %q", out)
	}
	if out := outputs["c1"]; !strings.Contains(out, "hello") {
		t.Errorf("the read did not run: %q", out)
	}
	sawNotice := false
	for _, l := range r.out.lines() {
		if l.T == LineNotice && strings.Contains(l.Text, "could not be saved") {
			sawNotice = true
		}
	}
	if !sawNotice {
		t.Error("the log failure was not reported")
	}
}

// A turn interrupted while a mutating call waits for the log does not run it:
// the writer is held, the turn interrupted, the writer released, and the
// write never happens; the call is answered as interrupted.
func TestInterruptWhileWaitingForTheLogDoesNotRunTheMutation(t *testing.T) {
	root := t.TempDir()
	release := make(chan struct{})
	var releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(callOf("c1", "fs_write", `{"path":"b.txt","content":"x"}`)),
		reply(msgItem("must not be asked")),
	}}
	r := newRig(t, Config{Root: root, Endpoint: model, BeforeAppend: func(json.RawMessage) error { <-release; return nil }})
	r.user("go")
	r.waitFor("tool_start", func(l Line) bool { return l.T == LineToolStart })
	time.Sleep(100 * time.Millisecond)
	r.send(Command{Type: CmdInterrupt})
	time.Sleep(100 * time.Millisecond)
	open()
	end := r.waitTurnEnd(1)
	if end.Reason != ReasonAborted {
		t.Fatalf("turn end %+v", end)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err == nil {
		t.Fatal("the write ran after the interrupt")
	}
	var out string
	for _, l := range r.out.lines() {
		if l.T == LineToolEnd && l.Call == "c1" {
			out = l.Output
		}
	}
	if out != interruptedOutput {
		t.Errorf("the call was answered %q, want the interruption result", out)
	}
}

// Parallel read results reach the log in the order the transcript holds them.
func TestParallelResultsKeepLogOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	for i := 0; i < 24; i++ {
		calls = append(calls, callOf(fmt.Sprintf("c%02d", i), "fs_read", `{"path":"a.txt"}`))
	}
	var mu sync.Mutex
	var written []string
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(calls...), reply(msgItem("done")),
	}}
	r := newRig(t, Config{Root: root, Endpoint: model, BeforeAppend: func(item json.RawMessage) error {
		mu.Lock()
		written = append(written, string(item))
		mu.Unlock()
		return nil
	}})
	r.user("go")
	r.waitTurnEnd(1)
	r.stop()
	if err := r.agent.Close(); err != nil {
		t.Fatal(err)
	}
	transcript := r.agent.Transcript()
	mu.Lock()
	defer mu.Unlock()
	if len(written) != len(transcript) {
		t.Fatalf("written %d, transcript %d", len(written), len(transcript))
	}
	for i := range transcript {
		if written[i] != string(transcript[i]) {
			t.Fatalf("item %d: log %s, memory %s", i, written[i], transcript[i])
		}
	}
}

// An interrupt flushes: the log is whole once the turn has ended.
func TestInterruptedTurnFlushesTheLog(t *testing.T) {
	started := make(chan struct{})
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		func(ctx context.Context, _ Request, _ StreamHandler) (*Response, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}}
	r := newRig(t, Config{Endpoint: model})
	r.user("go")
	<-started
	r.send(Command{Type: CmdInterrupt})
	r.waitTurnEnd(1)
	// The turn's end is reported before the flush; wait for the log to be
	// whole through the agent, as shutdown does.
	r.stop()
	if err := r.agent.Close(); err != nil {
		t.Fatal(err)
	}
	_, recovered, err := OpenStore(r.agent.cfg.Dir, r.agent.Session(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 {
		t.Errorf("recovered %d items, want the user message", len(recovered))
	}
}

// A Retry-After above the cap is not retried early: the turn ends with an
// error that names the provider's wait, and no request is sent again.
func TestRetryAfterAboveTheCapEndsTheTurn(t *testing.T) {
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		failWith(&HTTPError{Status: 429, Message: "daily quota", RetryAfter: 2 * time.Hour}),
		reply(msgItem("must not be asked")),
	}}
	var slept []time.Duration
	r := newRig(t, Config{Endpoint: model, Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }})
	r.user("hi")
	end := r.waitTurnEnd(1)
	if end.Reason != ReasonError || !strings.Contains(end.Error, "2h0m0s") || !strings.Contains(end.Error, "daily quota") || !strings.Contains(end.Error, "again after") {
		t.Fatalf("end = %+v", end)
	}
	if len(slept) != 0 || len(model.requests()) != 1 {
		t.Errorf("slept %v, requests %d: nothing may be retried early", slept, len(model.requests()))
	}
	// At the cap itself the wait is honored.
	model = &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		failWith(&HTTPError{Status: 429, Message: "slow", RetryAfter: MaxRetryAfter}),
		reply(msgItem("ok")),
	}}
	slept = nil
	r2 := newRig(t, Config{Endpoint: model, Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }})
	r2.user("hi")
	if end := r2.waitTurnEnd(1); end.Reason != ReasonDone || len(slept) != 1 || slept[0] != MaxRetryAfter {
		t.Fatalf("at the cap: end %+v slept %v", end, slept)
	}
}

func TestTypedLoopIsolatesModelAndToolPanics(t *testing.T) {
	panickingModel := func(context.Context, Request, StreamHandler) (*Response, error) {
		panic("model failure detail")
	}
	ws := &fakeWS{
		tools: []MCPTool{{Name: "git.status"}, {Name: "git.log"}},
		byName: map[string]func(context.Context, json.RawMessage) (CallResult, error){
			"git.status": func(context.Context, json.RawMessage) (CallResult, error) {
				panic("tool failure detail")
			},
		},
	}
	faults := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		panickingModel,
		reply(callOf("c1", "git_status", "{}"), callOf("c2", "git_log", "{}")),
		reply(msgItem("after panic")),
	}}
	unaffected := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(msgItem("other session")),
	}}
	faultCommands, faultEvents := startTypedLoop(t, faults, ws)
	otherCommands, otherEvents := startTypedLoop(t, unaffected, nil)
	faultCommands <- Command{Type: CmdUser, Text: "model panic"}
	otherCommands <- Command{Type: CmdUser, Text: "parallel healthy turn"}
	if end := nextTypedTurnEnd(t, faultEvents); end.Reason != ReasonError || end.Error != "the werk turn failed unexpectedly" {
		t.Fatalf("panicked model turn = %+v", end)
	}
	if end := nextTypedTurnEnd(t, otherEvents); end.Reason != ReasonDone {
		t.Fatalf("other session = %+v", end)
	}
	faultCommands <- Command{Type: CmdUser, Text: "tool panic"}
	if end := nextTypedTurnEnd(t, faultEvents); end.Reason != ReasonError || end.Error != "a werk tool failed unexpectedly" {
		t.Fatalf("panicked tool turn = %+v", end)
	}
	faultCommands <- Command{Type: CmdUser, Text: "next turn"}
	if end := nextTypedTurnEnd(t, faultEvents); end.Reason != ReasonDone {
		t.Fatalf("next turn = %+v", end)
	}
}

func startTypedLoop(t *testing.T, endpoint Responder, ws WSTools) (chan<- Command, <-chan Line) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan Line, 64)
	a, err := New(Config{
		Dir: t.TempDir(), Authority: AuthorityFull, Model: "vendor/model", Endpoint: endpoint, WS: ws,
		Emit: func(line Line) {
			select {
			case events <- line:
			case <-ctx.Done():
			}
		},
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	commands, done := make(chan Command, 8), make(chan struct{})
	go func() {
		defer close(done)
		a.ServeCommands(ctx, commands)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		a.Close()
	})
	return commands, events
}

func nextTypedTurnEnd(t *testing.T, events <-chan Line) Line {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case l := <-events:
			if strings.Contains(l.Error+l.Output+l.Text, "failure detail") {
				t.Fatalf("panic payload reached the event stream: %+v", l)
			}
			if l.T == LineTurnEnd {
				return l
			}
		case <-timer.C:
			t.Fatal("typed loop did not finish the turn")
		}
	}
}

func TestPartialModelResponseLogFailureKeepsAValidPrefix(t *testing.T) {
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(`{"type":"reasoning","id":"r"}`, callOf("c1", "fs_read", `{"path":"a.txt"}`)),
		reply(msgItem("done")),
	}}
	r := newRig(t, Config{Endpoint: model, BeforeAppend: func(item json.RawMessage) error {
		if strings.Contains(string(item), `"type":"function_call"`) {
			return errors.New("fixture response commit failed")
		}
		return nil
	}})
	r.user("go")
	r.waitTurnEnd(1)
	r.stop()
	must(t, r.agent.Close())
	reopened, items, err := OpenStore(r.agent.cfg.Dir, r.agent.Session(), nil)
	must(t, err)
	defer reopened.Close()
	if len(items) != 1 || string(items[0]) != string(userItem("go")) {
		t.Fatalf("recovered %s; want only the user input", items)
	}
}

func TestTruncateOutputKeepsHeadAndTail(t *testing.T) {
	s := strings.Repeat("a", 100) + strings.Repeat("é", 100) + strings.Repeat("z", 100)
	out, cut := TruncateOutput(s, 120)
	if !cut || !strings.Contains(out, "bytes of output omitted") || !strings.HasPrefix(out, "aaa") || !strings.HasSuffix(out, "zzz") {
		t.Errorf("out = %q", out)
	}
	for _, r := range out {
		if r == '�' {
			t.Fatalf("a rune was split: %q", out)
		}
	}
	if same, cut := TruncateOutput("short", 120); cut || same != "short" {
		t.Errorf("short = %q, %v", same, cut)
	}
}
