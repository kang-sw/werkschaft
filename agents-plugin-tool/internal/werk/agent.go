package werk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// agent.go is the model loop: request, tool calls, tool results, repeat,
// until a response asks for no tool. One Agent serves one session over the
// line protocol (protocol.go).
//
// What the loop guarantees:
//   - A response is acted on only once it completed. Its tool calls run after
//     the stream's terminal event, so a stream cut or a retried request never
//     finds a call that already ran.
//   - A completed tool call is never run again: its result joins the
//     transcript when it finishes, and a recovered conversation answers a call
//     with no result as interrupted instead of running it.
//   - Read-only calls of one response run in parallel; the others run alone,
//     one after the other, in the order the model wrote them.
//   - Output items go back to the provider as received, in order (reasoning
//     included); the harness adds nothing between them.

// Responder is the model endpoint (*Client).
type Responder interface {
	Respond(ctx context.Context, req Request, h StreamHandler) (*Response, error)
}

// WSTools is the daemon's ws tool surface (*MCP).
type WSTools interface {
	ListTools(ctx context.Context) ([]MCPTool, error)
	Call(ctx context.Context, name string, args json.RawMessage) (CallResult, error)
}

// Config is what an Agent runs with.
type Config struct {
	// Dir is the session directory the conversation file lives under.
	Dir string
	// Root and Cwd are the worktree root and the working directory;
	// Authority is the worker's permission level.
	Root, Cwd string
	Authority Authority
	Model     string
	Effort    string
	// Session resumes that session's conversation; empty starts a new one.
	Session string
	// Instructions is the instruction text composed for the session, added to
	// the harness's own preamble.
	Instructions string
	// LocalTools names the file tools the profile offers; empty offers all.
	LocalTools []string
	Endpoint   Responder
	WS         WSTools
	Out        io.Writer

	// Now is the clock (nil is time.Now). Sleep waits for d or the context
	// (nil is a timer). Backoff is the delay before retry n, one-based (nil is
	// bounded exponential with jitter); MaxRetries bounds the retries of one
	// request (0 is DefaultMaxRetries).
	Now        func() time.Time
	Sleep      func(ctx context.Context, d time.Duration) error
	Backoff    func(attempt int) time.Duration
	MaxRetries int

	// BeforeAppend, when set, runs in the background writer ahead of each log
	// append (a seam for tests that delay the write).
	BeforeAppend func(json.RawMessage)
}

// Limits of the loop.
const (
	// ToolOutputBytes bounds a tool result the model sees (and the line
	// that reports it): the head and the tail are kept.
	ToolOutputBytes = 48 << 10
	// ToolArgsLineBytes bounds the arguments a tool_start line carries.
	ToolArgsLineBytes = 256 << 10
	// DefaultMaxRetries is how many times a failed request is sent again.
	DefaultMaxRetries = 5
	// MaxRetryAfter caps a server-requested wait: a longer one ends the turn
	// with an error that names it.
	MaxRetryAfter = 5 * time.Minute
	// replyThrottle spaces the streamed reply lines of one message.
	replyThrottle = 300 * time.Millisecond
)

// parallelMCP are the ws tools that only read, which a response's calls may
// run side by side; every other ws tool runs alone.
var parallelMCP = map[string]bool{
	"tickets.query": true, "tickets.template": true, "tickets.checklist": true, "tickets.verify": true,
	"config.resolve_agent": true, "infra.read": true, "convention.read": true, "playbook.read": true,
	"note.query": true, "rationale.query": true, "git.status": true, "git.diff": true, "git.log": true,
	"git.merge_base": true, "api.list": true, "project_tree": true, "runtime.read": true,
	"todo.list": true, "todo.read": true, "mailbox.recipients": true,
}

// execLaunchTools are the ws tools that start or feed a process. A read-only
// worker is not offered them and a call to one is refused: the daemon
// approves a hosted worker's exec calls whatever its authority, so the
// authority is held here (the generic gap is the exec-tools ticket's).
var execLaunchTools = []string{"exec.shell", "exec.run", "exec.spawn", "exec.write"}

// execLaunchRefusal answers a read-only worker's call to an exec launch tool.
const execLaunchRefusal = "Error: this session is read-only: it may not run commands. Read files with the file tools instead."

// launchesProcess reports whether a model-side tool name is an exec launch
// tool.
func launchesProcess(modelName string) bool {
	for _, n := range execLaunchTools {
		if ModelToolName(n) == modelName {
			return true
		}
	}
	return false
}

type toolEntry struct {
	local *Tool
	// ws is the daemon-side name of a ws tool.
	ws       string
	readOnly bool
}

// Agent is one session's loop.
type Agent struct {
	cfg   Config
	files *Files
	store *Store
	log   *writer
	out   *emitter
	now   func() time.Time

	mu         sync.Mutex
	model      string
	effort     string
	session    string
	transcript []json.RawMessage
	steers     []string
	turn       int
	tools      map[string]toolEntry
	defs       []ToolDef
	toolsDone  bool
	resumed    int
	repaired   int

	// The last turn's account, for a status ask: whether a turn runs, the
	// final reply and end of the last one, and the steers it took.
	running    bool
	lastReply  string
	lastReason string
	lastError  string
	taken      []string
}

type emitter struct {
	mu  sync.Mutex
	out io.Writer
	now func() time.Time
}

func (e *emitter) line(l Line) {
	if l.At == 0 {
		l.At = e.now().UnixMilli()
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.out.Write(append(raw, '\n'))
}

// New opens the session's conversation and builds the agent. It starts no
// request and sends no line.
func New(cfg Config) (*Agent, error) {
	if cfg.Endpoint == nil {
		return nil, errors.New("werk: no model endpoint")
	}
	if !cfg.Authority.Valid() {
		return nil, fmt.Errorf("werk: a worker needs an authority (read-only, workdir-write or full); got %q", string(cfg.Authority))
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	session := cfg.Session
	if session == "" {
		session = NewSessionID()
	}
	store, items, err := OpenStore(cfg.Dir, session, func() int64 { return now().UnixMilli() })
	if err != nil {
		return nil, fmt.Errorf("werk: open the conversation: %w", err)
	}
	a := &Agent{
		cfg:   cfg,
		files: &Files{Root: cfg.Root, Cwd: cfg.Cwd, Authority: cfg.Authority},
		store: store,
		out:   &emitter{out: cfg.Out, now: now},
		now:   now,
		model: cfg.Model, effort: cfg.Effort, session: session,
		transcript: items, resumed: len(items),
	}
	a.log = newWriter(store, cfg.BeforeAppend, func(err error) {
		a.out.line(Line{T: LineNotice, Text: "the conversation could not be saved: " + err.Error()})
	})
	a.repaired = a.repairPending()
	return a, nil
}

// Session is the session id.
func (a *Agent) Session() string { return a.session }

// Transcript is a copy of the conversation's items.
func (a *Agent) Transcript() []json.RawMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]json.RawMessage(nil), a.transcript...)
}

// interruptedOutput answers a tool call a restart or an interrupt cut short.
const interruptedOutput = "Error: this tool call did not finish: the session was interrupted or restarted while it ran. It was not run again. Check the current state before repeating it."

// repairPending answers every function call that has no result with an
// interruption result, so the transcript is one a provider accepts and no
// call is ever re-run. It returns how many it answered.
func (a *Agent) repairPending() int {
	pending := pendingCalls(a.transcript)
	for _, id := range pending {
		item := functionOutputItem(id, interruptedOutput)
		a.transcript = append(a.transcript, item)
		a.log.Add(item)
	}
	return len(pending)
}

func pendingCalls(items []json.RawMessage) []string {
	var order []string
	open := map[string]bool{}
	for _, raw := range items {
		var it struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
		}
		if json.Unmarshal(raw, &it) != nil {
			continue
		}
		switch it.Type {
		case "function_call":
			if it.CallID != "" && !open[it.CallID] {
				open[it.CallID] = true
				order = append(order, it.CallID)
			}
		case "function_call_output":
			delete(open, it.CallID)
		}
	}
	var out []string
	for _, id := range order {
		if open[id] {
			out = append(out, id)
		}
	}
	return out
}

func functionOutputItem(callID, output string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "function_call_output", "call_id": callID, "output": output})
	return raw
}

func userItem(text string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"type": "message", "role": "user",
		"content": []map[string]string{{"type": "input_text", "text": text}},
	})
	return raw
}

// append adds an item to the transcript and queues it for the log: memory
// first, the disk behind it in the same order (writer.go).
func (a *Agent) append(item json.RawMessage) {
	a.mu.Lock()
	a.transcript = append(a.transcript, item)
	a.mu.Unlock()
	a.log.Add(item)
}

// Close writes what is queued and releases the conversation file.
func (a *Agent) Close() error {
	a.log.Close()
	return a.store.Close()
}

// Serve runs the session: it reads commands from in until it ends, and
// returns after the running turn has ended.
func (a *Agent) Serve(ctx context.Context, in io.Reader) error {
	defer a.log.Flush()
	a.mu.Lock()
	a.out.line(Line{T: LineInit, Session: a.session, Version: ProtocolVersion, Model: a.model, Effort: a.effort, Resumed: a.resumed})
	a.mu.Unlock()
	if a.repaired > 0 {
		a.out.line(Line{T: LineNotice, Text: fmt.Sprintf("%d tool call(s) of the earlier process had no result; they were answered as interrupted and not run again", a.repaired)})
	}
	cmds := make(chan Command)
	go func() {
		defer close(cmds)
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 64<<10), 64<<20)
		for sc.Scan() {
			var c Command
			if json.Unmarshal(sc.Bytes(), &c) != nil || c.Type == "" {
				continue
			}
			select {
			case cmds <- c:
			case <-ctx.Done():
				return
			}
		}
	}()

	var (
		cancel  context.CancelFunc
		done    chan struct{}
		queued  []string
		running bool
	)
	start := func(text string) {
		tctx, c := context.WithCancel(ctx)
		cancel, done, running = c, make(chan struct{}), true
		go func(d chan struct{}) {
			defer close(d)
			a.runTurn(tctx, text)
		}(done)
	}
	finish := func() {
		cancel()
		running = false
		a.dropSteers()
		if len(queued) > 0 {
			next := queued[0]
			queued = queued[1:]
			start(next)
		}
	}
	for {
		var doneC <-chan struct{}
		if running {
			doneC = done
		}
		select {
		case <-ctx.Done():
			if running {
				<-done
			}
			return ctx.Err()
		case <-doneC:
			finish()
		case c, ok := <-cmds:
			if !ok {
				if running {
					cancel()
					<-done
				}
				return nil
			}
			switch c.Type {
			case CmdUser:
				if running {
					queued = append(queued, c.Text)
				} else {
					start(c.Text)
				}
			case CmdSteer:
				switch {
				case !running:
					a.out.line(Line{T: LineSteerAck, ID: c.ID, Refusal: "no turn is running"})
				default:
					a.mu.Lock()
					a.steers = append(a.steers, c.Text)
					a.mu.Unlock()
					a.out.line(Line{T: LineSteerAck, ID: c.ID})
				}
			case CmdClearSteered:
				a.dropSteers()
			case CmdStatus:
				a.mu.Lock()
				l := Line{T: LineStatus, ID: c.ID, Turn: a.turn, Running: a.running, Text: a.lastReply, Reason: a.lastReason, Error: a.lastError, Taken: append([]string(nil), a.taken...)}
				a.mu.Unlock()
				a.out.line(l)
			case CmdInterrupt:
				if running {
					cancel()
				}
			case CmdSet:
				a.mu.Lock()
				if c.Model != "" {
					a.model = c.Model
				}
				if c.Effort != nil {
					a.effort = *c.Effort
				}
				a.mu.Unlock()
			}
		}
	}
}

func (a *Agent) dropSteers() {
	a.mu.Lock()
	a.steers = nil
	a.mu.Unlock()
}

// takeSteers moves the queued steered messages into the transcript.
func (a *Agent) takeSteers() {
	a.mu.Lock()
	steers := a.steers
	a.steers = nil
	a.mu.Unlock()
	for _, text := range steers {
		a.append(userItem(text))
		a.mu.Lock()
		a.taken = append(a.taken, text)
		a.mu.Unlock()
		a.out.line(Line{T: LineUserTaken, Text: text})
	}
}

func (a *Agent) hasSteers() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.steers) > 0
}

// runTurn runs one user turn to its end and reports it.
func (a *Agent) runTurn(ctx context.Context, text string) {
	a.mu.Lock()
	a.turn++
	turn := a.turn
	a.running, a.lastReply, a.lastReason, a.lastError, a.taken = true, "", "", "", nil
	a.mu.Unlock()
	a.out.line(Line{T: LineTurnStart, Turn: turn})
	a.out.line(Line{T: LineUser, Turn: turn, Text: text})
	a.append(userItem(text))
	reason, errText := a.loop(ctx, turn)
	a.mu.Lock()
	a.running, a.lastReason, a.lastError = false, reason, errText
	a.mu.Unlock()
	a.out.line(Line{T: LineTurnEnd, Turn: turn, Reason: reason, Error: errText})
	// The log is whole where a turn ends, an interrupted one included.
	a.log.Flush()
}

// loop runs model requests and tool calls until the model asks for no more.
func (a *Agent) loop(ctx context.Context, turn int) (reason, errText string) {
	if err := a.loadTools(ctx); err != nil {
		a.out.line(Line{T: LineNotice, Text: "the ws tools are not available: " + err.Error()})
	}
	for {
		if ctx.Err() != nil {
			return ReasonAborted, ""
		}
		a.takeSteers()
		resp, err := a.request(ctx, turn)
		if err != nil {
			if ctx.Err() != nil {
				return ReasonAborted, ""
			}
			return ReasonError, err.Error()
		}
		for _, item := range resp.Output {
			a.append(item)
		}
		a.report(turn, resp)
		calls := functionCalls(resp.Output)
		if len(calls) == 0 {
			if a.hasSteers() && ctx.Err() == nil {
				continue
			}
			return ReasonDone, ""
		}
		a.runCalls(ctx, turn, resp.ID, calls)
		if ctx.Err() != nil {
			return ReasonAborted, ""
		}
	}
}

type functionCall struct {
	ID, CallID, Name, Arguments string
}

func functionCalls(items []json.RawMessage) []functionCall {
	var out []functionCall
	for _, raw := range items {
		var it struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal(raw, &it) != nil || it.Type != "function_call" {
			continue
		}
		id := it.CallID
		if id == "" {
			id = it.ID
		}
		out = append(out, functionCall{ID: it.ID, CallID: id, Name: it.Name, Arguments: it.Arguments})
	}
	return out
}

// report emits a completed response's lines: its usage first (the figures
// and the response id every later line of the response shares), then its
// reasoning summaries and assistant messages.
func (a *Agent) report(turn int, resp *Response) {
	u := resp.Usage
	a.out.line(Line{
		T: LineUsage, Turn: turn, Response: resp.ID,
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokens: u.CachedTokens, ReasoningTokens: u.ReasoningTokens,
		CostUSD: u.CostUSD, Context: u.InputTokens + u.OutputTokens,
	})
	for _, raw := range resp.Output {
		var it struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Summary []struct {
				Text string `json:"text"`
			} `json:"summary"`
			Encrypted string `json:"encrypted_content"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw, &it) != nil {
			continue
		}
		switch it.Type {
		case "reasoning":
			var parts []string
			for _, s := range it.Summary {
				if s.Text != "" {
					parts = append(parts, s.Text)
				}
			}
			text := strings.Join(parts, "\n")
			a.out.line(Line{T: LineReasoning, Turn: turn, Response: resp.ID, Item: it.ID, Text: text, Encrypted: text == "" && it.Encrypted != ""})
		case "message":
			var parts []string
			for _, c := range it.Content {
				if c.Type == "output_text" || c.Type == "text" {
					parts = append(parts, c.Text)
				}
			}
			if text := strings.Join(parts, ""); strings.TrimSpace(text) != "" {
				a.mu.Lock()
				a.lastReply = text
				a.mu.Unlock()
				a.out.line(Line{T: LineReply, Turn: turn, Response: resp.ID, Item: it.ID, Text: text, Final: true})
			}
		}
	}
}

// request sends one model request, retrying the failures that are worth it.
func (a *Agent) request(ctx context.Context, turn int) (*Response, error) {
	limit := a.cfg.MaxRetries
	if limit <= 0 {
		limit = DefaultMaxRetries
	}
	for attempt := 0; ; attempt++ {
		a.mu.Lock()
		req := Request{
			Model: a.model, Effort: a.effort, Instructions: a.instructions(),
			Input: append([]json.RawMessage(nil), a.transcript...), Tools: a.defs,
		}
		a.mu.Unlock()
		resp, err := a.cfg.Endpoint.Respond(ctx, req, a.streamHandler(turn))
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		delay, ok := Retryable(err)
		if !ok || attempt >= limit {
			return nil, err
		}
		if delay > MaxRetryAfter {
			// The provider asked for a longer wait than a session holds a turn
			// open for. Retrying early would only be refused again, so the turn
			// ends and says how long to wait.
			return nil, fmt.Errorf("the model endpoint asked to wait %s before the next request, longer than the %s this session waits; send the message again after that wait (%v)",
				delay.Round(time.Second), MaxRetryAfter, err)
		}
		if delay <= 0 {
			delay = a.backoff(attempt + 1)
		}
		line := Line{T: LineRetry, Turn: turn, Attempt: attempt + 1, MaxRetries: limit, DelayMS: delay.Milliseconds(), Error: err.Error()}
		var h *HTTPError
		if errors.As(err, &h) {
			line.Status = h.Status
		}
		a.out.line(line)
		if err := a.sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
}

func (a *Agent) backoff(attempt int) time.Duration {
	if a.cfg.Backoff != nil {
		return a.cfg.Backoff(attempt)
	}
	d := time.Second << min(attempt-1, 5)
	return d/2 + time.Duration(rand.Int63n(int64(d/2)+1))
}

func (a *Agent) sleep(ctx context.Context, d time.Duration) error {
	if a.cfg.Sleep != nil {
		return a.cfg.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// streamHandler turns streamed text into throttled reply lines; the final
// text of a message is reported from the completed response.
func (a *Agent) streamHandler(turn int) StreamHandler {
	var mu sync.Mutex
	texts := map[string]*strings.Builder{}
	last := map[string]time.Time{}
	return StreamHandler{TextDelta: func(item, delta string) {
		mu.Lock()
		defer mu.Unlock()
		b := texts[item]
		if b == nil {
			b = &strings.Builder{}
			texts[item] = b
		}
		b.WriteString(delta)
		now := a.now()
		if now.Sub(last[item]) < replyThrottle {
			return
		}
		last[item] = now
		a.out.line(Line{T: LineReply, Turn: turn, Item: item, Text: b.String()})
	}}
}

// instructions is the harness's own preamble followed by the composed
// instruction text. The caller holds a.mu.
func (a *Agent) instructions() string {
	var b strings.Builder
	b.WriteString("You are a ws worker running on ws's builtin agent harness. ")
	if a.cfg.Cwd != "" {
		fmt.Fprintf(&b, "Your working directory is %s. ", a.cfg.Cwd)
	}
	switch a.cfg.Authority {
	case AuthorityReadOnly:
		b.WriteString("Your authority is read-only: you may read and search files but not edit or write them. ")
	case AuthorityWorkdirWrite:
		b.WriteString("Your authority is workdir-write: you may edit and write files inside your worktree only. ")
	case AuthorityFull:
		b.WriteString("Your authority is full. ")
	}
	if a.cfg.Authority == AuthorityReadOnly {
		b.WriteString("You have no shell: read and search files with the file tools.")
	} else {
		b.WriteString("Run shell commands with the ws exec tools (exec_shell, exec_run); read and change files with the file tools.")
	}
	if t := strings.TrimSpace(a.cfg.Instructions); t != "" {
		b.WriteString("\n\n")
		b.WriteString(t)
	}
	return b.String()
}

// loadTools lists the tools once: the profile's file tools and the daemon's
// ws tools. A failed listing leaves the file tools and is tried again on the
// next turn.
func (a *Agent) loadTools(ctx context.Context) error {
	a.mu.Lock()
	done := a.toolsDone
	a.mu.Unlock()
	if done {
		return nil
	}
	tools := map[string]toolEntry{}
	var defs []ToolDef
	for _, t := range a.files.Tools(a.cfg.LocalTools) {
		t := t
		tools[t.Def.Name] = toolEntry{local: &t, readOnly: t.ReadOnly}
		defs = append(defs, t.Def)
	}
	var listErr error
	if a.cfg.WS != nil {
		listed, err := a.cfg.WS.ListTools(ctx)
		if err != nil {
			listErr = err
		}
		for _, mt := range listed {
			name := ModelToolName(mt.Name)
			if _, dup := tools[name]; dup || name == "" {
				continue
			}
			if a.cfg.Authority == AuthorityReadOnly && launchesProcess(name) {
				continue
			}
			tools[name] = toolEntry{ws: mt.Name, readOnly: parallelMCP[mt.Name]}
			defs = append(defs, ToolDef{Name: name, Description: mt.Description, Parameters: mt.InputSchema})
		}
	}
	a.mu.Lock()
	a.tools, a.defs = tools, defs
	a.toolsDone = listErr == nil
	a.mu.Unlock()
	return listErr
}

// ModelToolName is the spelling of a ws tool name the model is offered: a
// provider's function names admit letters, digits, underscore and hyphen, so
// ws's dotted names ("exec.shell") are written with underscores.
func ModelToolName(wsName string) string {
	var b strings.Builder
	for _, r := range wsName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := b.String()
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// runCalls runs one response's tool calls and appends each result as it
// finishes. Consecutive read-only calls run together; any other call runs
// alone. A call the interrupt cut off is answered as interrupted.
func (a *Agent) runCalls(ctx context.Context, turn int, response string, calls []functionCall) {
	a.mu.Lock()
	tools := a.tools
	a.mu.Unlock()
	answered := map[string]bool{}
	i := 0
	for i < len(calls) && ctx.Err() == nil {
		j := i
		if a.readOnlyCall(tools, calls[i]) {
			for j < len(calls) && a.readOnlyCall(tools, calls[j]) {
				j++
			}
		} else {
			j = i + 1
		}
		var wg sync.WaitGroup
		for _, c := range calls[i:j] {
			wg.Add(1)
			go func(c functionCall) {
				defer wg.Done()
				a.runCall(ctx, turn, response, tools, c)
			}(c)
			answered[c.CallID] = true
		}
		wg.Wait()
		i = j
	}
	// Whatever did not run (the turn was interrupted) is answered, so the
	// transcript has a result for every call.
	for _, c := range calls {
		if !answered[c.CallID] {
			a.out.line(Line{T: LineToolStart, Turn: turn, Response: response, Call: c.CallID, Name: c.Name, Args: clip(c.Arguments, ToolArgsLineBytes)})
			a.append(functionOutputItem(c.CallID, interruptedOutput))
			a.out.line(Line{T: LineToolEnd, Turn: turn, Call: c.CallID, Name: c.Name, Output: interruptedOutput, Failed: true})
		}
	}
}

func (a *Agent) readOnlyCall(tools map[string]toolEntry, c functionCall) bool {
	e, ok := tools[c.Name]
	return ok && e.readOnly
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// runCall runs one call: start line, the tool, the result into the
// transcript, end line.
func (a *Agent) runCall(ctx context.Context, turn int, response string, tools map[string]toolEntry, c functionCall) {
	a.out.line(Line{T: LineToolStart, Turn: turn, Response: response, Call: c.CallID, Name: c.Name, Args: clip(c.Arguments, ToolArgsLineBytes), Truncated: len(c.Arguments) > ToolArgsLineBytes})
	output, failed := a.invoke(ctx, tools, c)
	shown, cut := TruncateOutput(output, ToolOutputBytes)
	a.append(functionOutputItem(c.CallID, shown))
	a.out.line(Line{T: LineToolEnd, Turn: turn, Call: c.CallID, Name: c.Name, Output: shown, Truncated: cut, Failed: failed})
}

// invoke runs the tool a call names and returns the text the model sees.
func (a *Agent) invoke(ctx context.Context, tools map[string]toolEntry, c functionCall) (string, bool) {
	if a.cfg.Authority == AuthorityReadOnly && launchesProcess(c.Name) {
		return execLaunchRefusal, true
	}
	e, ok := tools[c.Name]
	if !ok {
		return "Error: unknown tool " + c.Name, true
	}
	args := json.RawMessage(c.Arguments)
	if strings.TrimSpace(c.Arguments) == "" {
		args = json.RawMessage("{}")
	}
	if !json.Valid(args) {
		return "Error: the tool arguments are not valid JSON", true
	}
	if e.local != nil {
		out, err := e.local.Run(ctx, args)
		if err != nil {
			if ctx.Err() != nil {
				return interruptedOutput, true
			}
			return "Error: " + err.Error(), true
		}
		return out, false
	}
	res, err := a.cfg.WS.Call(ctx, e.ws, args)
	switch {
	case err == nil:
		return res.Text, res.IsError
	case ctx.Err() != nil:
		return interruptedOutput, true
	case errors.Is(err, ErrDeliveryUnknown):
		return "Error: the connection to ws failed while this call ran, so it may or may not have taken effect. It was not run again; check the current state before repeating it.", true
	}
	return "Error: " + err.Error(), true
}

// TruncateOutput keeps at most max bytes of s: its head and its tail, with a
// note between them. cut reports whether anything was dropped.
func TruncateOutput(s string, max int) (out string, cut bool) {
	if len(s) <= max {
		return s, false
	}
	head := max * 2 / 3
	tail := max - head
	// Keep whole UTF-8 sequences at the cut points.
	for head > 0 && !startsRune(s[head]) {
		head--
	}
	start := len(s) - tail
	for start < len(s) && !startsRune(s[start]) {
		start++
	}
	return fmt.Sprintf("%s\n[... %d bytes of output omitted ...]\n%s", s[:head], start-head, s[start:]), true
}

func startsRune(b byte) bool { return b&0xC0 != 0x80 }
