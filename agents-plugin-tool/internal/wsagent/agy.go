package wsagent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsharnessenv"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wslive"
)

// agy.go is the Antigravity backend: `agy acp`, the Agent Client Protocol
// (v1: JSON-RPC 2.0 over stdio, one message per line). Hosted leads run
// under a holder, as do workers using the default
// agent with their instructions appended to its system prompt.
//
// The wire facts it relies on are pinned by agy_fixture_test.go against
// the recorded agy-<version>-*.jsonl fixtures:
//
//   - A lead's process runs under a session holder, which keeps agy's output
//     (never ws's input) in its ring. Every request ws writes therefore
//     carries an id that names its kind (agyID), so a later daemon replaying
//     the ring tells a prompt's response from a control's.
//   - A turn is a session/prompt. A prompt sent while one runs steers it:
//     agy answers the earlier one at once and the run belongs to the later
//     one, so the turn ends with the last outstanding prompt's response.
//   - Permission requests are agent requests whose JSON-RPC id is echoed
//     raw in the answer; the option is picked by kind, never by a fixed id.
//     Nothing withdraws a request on cancel, so the turn's end settles every
//     one still pending.
//   - ws tools arrive over the daemon's HTTP MCP route (mcp_session.go) with
//     the token in the session request, never in argv or the environment. A
//     refused token only drops the tools, with no error, so a start waits for
//     the token to reach the route and fails loudly when it does not.

// AgyBackend runs Antigravity's CLI as an ACP agent, one process per
// conversation (--process-per-session, which agy documents as required for
// HTTP MCP servers a client configures).
type AgyBackend struct {
	// Command is the agy executable; empty looks up "agy" on PATH.
	Command string
}

func (b AgyBackend) registration() (string, BackendRegistration) {
	// No OneShot, probed 2026-11-22 with agy 1.5.14: `agy -p` (stdin or
	// argument, --output-format text, --disable-slash-commands) has no flag
	// that removes its tools or MCP servers; --mode and --sandbox restrict or
	// gate them without disabling them, so no one-shot mode is verifiable.
	return BackendAgy, BackendRegistration{
		Backend:         b,
		LeadProfile:     LeadProfileAgy,
		PermissionModes: AgyPermissionModes,
		Catalog:         AgyCatalog(),
		ToolNameScheme:  ToolNameSchemeSanitize,
		CallFallback:    true,
		DirectTools: []string{
			"workflow_manual", "workflow_state", "agenda.set", "agenda.list", "agenda.clear",
			"whiteboard.update", "whiteboard.query", "mailbox.send", "mailbox.recv", "mailbox.history", "mailbox.read", "mailbox.recipients",
			"agent.spawn", "agent.interrupt", "agent.list", "playbook.read", "playbook.render",
			"tickets.query", "tickets.move", "git.status", "git.log", "git.commit", "git.merge",
			"worktree.acquire", "worktree.release", "note.query", "note.write",
			"config.resolve_agent", "todo.list", "todo.check", "compact",
		},
		ToolsAlwaysLoad:  true,
		ManualHarness:    BackendAgy,
		Dashboard:        agyDashboardTraits(),
		PlaybookCommands: true,
		// An agy lead explores with its own subagents and delegates through
		// agent.spawn, preferring another harness when one fits, unless
		// overridden via agy.prefer_native_subagent / agy.avoid_agy_subagent.
		PreferNativeSubagent: true,
		ResolveLeadSubagent:  agyLeadSubagentPolicy,
		// Same-harness workers only: an agy session (a hosted agy lead, or
		// an agy worker spawning a nested child) may run agy workers; every
		// other caller, and a creation with none, is refused (admitWorker).
		WorkerCapable:       true,
		crossHarnessRefusal: agyCrossHarnessRefusal,
		WorkerProfile:       AgentWorkerProfile(BackendAgy),
		WorkerHTTPMCP:       true,
		WorkerAutoApprove:   true,
		Lifecycle: LifecyclePolicy{
			ComposedLeadInstructions: true,
			// No stdio fallback: an agy lead has no ws-mcp without the route.
			LeadHTTPMCP:               true,
			TurnActivity:              true,
			InternalTurns:             true,
			WorkerLog:                 true,
			SingleHardCompactionSteer: true,
			EphemeralIsolatedStateDir: true,
		},
		// Native default mode asks before each run_command, and the daemon
		// refuses it (agy 1.5.14 live probe: native auto and
		// --dangerously-skip-permissions run commands without asking, and
		// top-level --settings rules do not reach ACP sessions).
		ExecCutover: &ExecCutoverSupport{
			Since:                     "1.5.14",
			LaunchMode:                agyCutoverMode,
			RejectTools:               []string{agyCommandTool},
			AutoApprove:               agyCutoverAutoApprove,
			Instruction:               agyExecCutoverInstruction,
			UnrequestedCommandWarning: agyUnrequestedCommandWarning,
		},
		Profiles: []BuiltinProfileRegistration{
			agentWorkerBuiltinProfile(BackendAgy, nil),
			leadBuiltinProfile(LeadProfileAgy, BackendAgy, ""),
		},
	}
}

// agyCrossHarnessRefusal refuses an agy worker entry whose caller is not an
// agy session. Antigravity's FAQ says an Antigravity login used through
// third-party software violates its terms and that wrapping its CLI in
// another coding agent is unsupported on consumer plans, which is what an
// agy worker under a Claude, Codex, or Pi session is. agy-to-agy use is the
// owner's accepted risk, restrained by the lead's manual alone.
const agyCrossHarnessRefusal = `harness "agy" accepts workers only from an agy session: Antigravity's consumer terms do not support running agy under another agent`

// AgyPermissionModes are the hosted lead choices. bypass is ws-side intent;
// the process still runs in agy's native default mode.
var AgyPermissionModes = []string{"default", "accept-edits", "auto", "plan", "bypass"}

// agyEfforts are the available effort values; Choices falls back to them
// before any session reported its own.
var agyEfforts = []string{"low", "medium", "high", "xhigh"}

// Timeouts of the requests a start and a control wait on. A session request
// may connect MCP servers before it answers.
var (
	agyInitTimeout    = 30 * time.Second
	agySessionTimeout = 2 * time.Minute
	agyControlTimeout = 30 * time.Second
	// agyMCPConnectWait bounds how long a start waits, after its session
	// opened, for agy to reach the ws MCP route with the session's token.
	// agy connects its MCP servers before it answers the session request
	// (1.5.10), so a live token has resolved by then; the wait only covers
	// the route's own lag.
	agyMCPConnectWait = 10 * time.Second
	// agyWakeUsageQuiet closes an unsolicited background-wakeup turn once
	// usage_update arrives and no tool_call follows (agy emits tool_call in
	// the same burst as usage_update, whereas a final reply ends right after
	// usage_update). agyWakeIdleQuiet is the fallback when no usage_update
	// arrives.
	agyWakeUsageQuiet = 1500 * time.Millisecond
	agyWakeIdleQuiet  = 30 * time.Second
)

// agyDropEnv are the parent harness variable prefixes an agy session starts
// without. wsharnessenv.Drop removes ws's own session identity.
var agyDropEnv = []string{"CLAUDE", "CODEX_", "PI_", "ORCA_"}

// agyBgShellRule is appended to the system prompt of an agy session
// outside the exec cutover (lead or worker), overriding Antigravity's
// reactive-wakeup guidance for background commands: the daemon tracks a
// running non-daemon run_command and keeps the session awake for it.
const agyBgShellRule = `Never end your turn while a non-daemon ` + "`run_command`" + ` task you started is still running (ignore this for ` + "`IsDaemon: true`" + ` processes). Override Antigravity's "end the turn and wait" / reactive-wakeup guidance for background commands: stay in the same turn — do other work or check status with ` + "`manage_task`" + ` — until it finishes.`

// agyCutoverMode is the native mode a cutover agy session runs in: the one
// that asks before each run_command, so the daemon can refuse it.
const agyCutoverMode = "default"

// agyExecCutoverInstruction is appended to a cutover agy session's system
// prompt at session creation, and sent once as a prompt at the end of a
// turn whose run_command call the daemon refused (or that ran one without
// a permission request): agy shows the model no refusal text of its own.
const agyExecCutoverInstruction = "Shell commands do not run through `run_command` in this session: every `run_command` call is refused. Run commands with the ws exec tools instead: `exec_shell` (cmd: one shell line) or `exec_run` (exe and args, no shell). Do not delegate shell commands to `invoke_subagent` to bypass this rule. Each waits up to its execution window, then answers with an exec id it keeps running under (async answers with the id at once); follow a running job with `exec_tail`, `exec_stop` and `exec_list`. Set `stdin_open: true` at launch to send input later with `exec_write`. A job that outlives its call reports its completion to you as a message, so you need not wait in the turn for it."

// agyUnrequestedCommandWarning is surfaced to the user when ~/.gemini
// always-allow rules let run_command execute without a permission request
// under the exec cutover.
const agyUnrequestedCommandWarning = "A native run_command ran without a permission request under the exec cutover (check ~/.gemini always-allow rules so shell commands route through ws exec tools)."

// agyRingSkip matches agy's thought chunks, which the mapper drops: a
// lead's holder keeps them out of its ring. A line whose shape this no
// longer matches is kept.
const agyRingSkip = `^\{"jsonrpc":"2\.0","method":"session/update","params":\{"sessionId":"[^"]*","update":\{"sessionUpdate":"agent_thought_chunk"`

// agyRingPulse replaces dropped thought lines at a bounded rate. It is a
// content-free session update, so old and new mappers both turn it into the
// existing in-memory ModelActivity signal without exposing thought text.
const agyRingPulse = `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"ws_activity"}}}`

// Executable is the agy command availability is checked against.
func (b AgyBackend) Executable() string {
	if b.Command != "" {
		return b.Command
	}
	return "agy"
}

// ResolveCommand resolves the agy executable and any leading arguments the same
// way hosted launches do (honouring Command overrides and unwrapping Windows
// cmd-shims) without spawning a process.
func (b AgyBackend) ResolveCommand() (string, []string, error) {
	return harnessCommand(b.Command, "agy")
}

// agyChoices holds the last session's model and thought-level report,
// which wins over the session-less `agy models` listing.
var agyChoices struct {
	mu      sync.Mutex
	models  []ModelChoice
	efforts []string
}

// Choices uses the last session's report, else a session-less listing.
func (b AgyBackend) Choices(ctx context.Context) ([]ModelChoice, []string, error) {
	return b.hostedChoices(ctx)
}

// agyEnv is a lead's environment: the daemon's without the parent harness
// sessions' variables and ws's own launcher state (including the HTTP MCP
// credentials: agy takes them in its session request), plus the hosted-session
// variables.
func agyEnv(spec StartSpec) []string {
	set := map[string]string{}
	if spec.Lead != nil {
		for name, value := range spec.Lead.Env {
			set[name] = value
		}
	}
	env := harnessEnv(wsharnessenv.Drop(spec.Env), agyDropEnv, nil, set)
	if spec.Lead != nil && spec.Lead.FrameTrace != "" {
		env = withGODEBUG(env, agyFrameTraceGODEBUG)
	}
	return env
}

// agyFrameTraceGODEBUG turns on Go's verbose HTTP/2 frame logging: level 2
// adds the framer read and write lines, whose framer pointer tells
// connections apart and whose stream id tells requests apart.
const agyFrameTraceGODEBUG = "http2debug=2"

// withGODEBUG adds setting to env's GODEBUG, after any settings already
// there (a later setting of the same name wins).
func withGODEBUG(env []string, setting string) []string {
	for i, kv := range env {
		if value, ok := strings.CutPrefix(kv, "GODEBUG="); ok {
			if value != "" {
				setting = value + "," + setting
			}
			out := slices.Clone(env)
			out[i] = "GODEBUG=" + setting
			return out
		}
	}
	return append(slices.Clone(env), "GODEBUG="+setting)
}

// Start launches `agy acp` for spec and, unless it rejoins a held process,
// opens the session before it returns: initialize, then session/new or
// session/resume with the ws MCP server, then the conversation's model,
// effort and mode.
func (b AgyBackend) Start(ctx context.Context, spec StartSpec) (Process, error) {
	if spec.Ephemeral {
		spec.Lead = nil
		spec.Holder = nil
		spec.MCPSession = nil
		spec.audit = nil
		spec.ExecCutover = false
	}
	if spec.Lead != nil && spec.Holder == nil {
		return nil, errors.New("agy backend: a hosted lead requires a session holder")
	}
	attach := spec.Holder != nil && spec.Holder.id != ""
	if !attach && !spec.Ephemeral && spec.MCPSession == nil {
		return nil, errors.New("agy backend: agy reaches ws tools only over the daemon's HTTP MCP route, and none is available; no process was started without ws tools")
	}
	command, pre, err := harnessCommand(b.Command, "agy")
	if err != nil {
		return nil, fmt.Errorf("agy backend: %w", err)
	}
	// Ephemeral slots run one shared `agy acp` process per slot (without
	// --process-per-session, which is only needed for client-configured HTTP
	// MCP servers) under an isolated --gemini-dir so Reset can reuse the
	// resident process via session/new.
	args := append(slices.Clone(pre), "acp")
	if spec.Ephemeral {
		if spec.ephemeralStateDir != "" {
			args = append(args, "--gemini-dir", spec.ephemeralStateDir)
		}
	} else {
		args = append(args, "--process-per-session")
	}
	// Both roles use the user's agy home and default agent. A rejoin
	// attaches to the running process; startChild ignores cmd.
	cmd := exec.Command(command, args...)
	cmd.Dir = spec.workDir()
	cmd.Env = agyEnv(spec)
	if spec.Holder != nil {
		spec.Holder.ringSkip = agyRingSkip
		spec.Holder.ringPulse = agyRingPulse
		if spec.Lead != nil && spec.Lead.FrameTrace != "" {
			// The holder filters the trace out of stderr before its ring.
			spec.Holder.http2Capture = &wsholder.HTTP2Capture{Path: spec.Lead.FrameTrace, MaxBytes: agyFrameCaptureMaxBytes}
		}
	}
	m := newAgyMapper(spec.SessionID)
	// Set before startChild: a rejoin's holder replay maps through m too.
	m.lead = spec.Lead != nil
	m.ephemeral = spec.Ephemeral
	m.cutover = spec.ExecCutover
	m.warnNativeSubagent = spec.ExecCutover && (spec.Lead == nil || !spec.Lead.PreferNativeSubagent)
	m.recoveredSteers = slices.Clone(spec.RecoveredSteers)
	m.reattachedPromptAfter = spec.reattachedPromptAfter
	sp, err := startChild(spec, cmd, m, agyProtocol{})
	if err != nil {
		return nil, err
	}
	p := &agyProcess{
		streamProcess: sp, m: m, worker: spec.Lead == nil,
		ephemeral: spec.Ephemeral, ephemeralSpec: spec,
		epoch: strconv.FormatInt(time.Now().UnixNano(), 36),
	}
	m.setWakeClose(p.closeUnsolicitedTurn)
	holder, pid, _ := sp.HolderInfo()
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	if s := spec.MCPSession; s != nil && s.Started != nil {
		s.Started(cmd.Env, pid)
	}
	if attach {
		return p, nil
	}
	// agy connects ws-mcp inside its session request. A lead's row must
	// name this process first; a worker has no hosted-lead registration.
	if spec.Lead != nil && spec.Lead.Spawned != nil {
		spec.Lead.Spawned(holder, pid)
	}
	if err := p.open(spec); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("agy backend: %w", err)
	}
	return p, nil
}

// agyProtocol writes ACP requests. A user turn needs the session id, so
// agyProcess.Send writes it; userLine is never used.
type agyProtocol struct{}

func (agyProtocol) userLine(string) any { return nil }

func (agyProtocol) requestLine(id string, request map[string]any) any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": request["method"], "params": request["params"]}
}

// Request kinds, the second field of agyID.
const (
	agyKindInit    = "init"
	agyKindNew     = "new"
	agyKindResume  = "resume"
	agyKindClose   = "close"
	agyKindConfig  = "cfg"
	agyKindMode    = "mode"
	agyKindPrompt  = "p"
	agyRequestMark = "ws"
)

// agyIDKind is the kind of a request id ws wrote ("ws-<kind>-<epoch>-<n>"),
// false for any other id.
func agyIDKind(id string) (string, bool) {
	parts := strings.Split(id, "-")
	if len(parts) != 4 || parts[0] != agyRequestMark {
		return "", false
	}
	return parts[1], true
}

// agyProcess is one `agy acp` process serving one session.
// frameCaptureNoter is a process whose holder may keep a frame capture.
type frameCaptureNoter interface {
	NoteFrameCapture(text string) error
}

// NoteFrameCapture adds text to the holder's frame capture, when the holder
// keeps one for this process.
func (p *agyProcess) NoteFrameCapture(text string) error {
	if p.holder == nil {
		return nil
	}
	return p.holder.noteCapture(text)
}

type agyProcess struct {
	*streamProcess
	m *agyMapper
	// epoch keeps this process's request ids apart from an earlier
	// daemon's on the same held process.
	epoch string
	seq   atomic.Int64
	// effort is the thought level the conversation asked for last.
	effort        atomic.Pointer[string]
	model         atomic.Pointer[string]
	worker        bool
	ephemeral     bool
	ephemeralSpec StartSpec
}

func (p *agyProcess) processControls() processControls {
	// A steer is a new session/prompt that ends the running one (Steer).
	controls := processControls{Process: p, approval: p, mode: p, steer: p, replacesTurn: true}
	if p.worker {
		controls.reports = p
	}
	if p.ephemeral {
		controls.reset = p
	}
	return controls
}

func (p *agyProcess) Close() error {
	p.m.stopWakeTimer()
	return p.streamProcess.Close()
}

// closeUnsolicitedTurn settles a self-resumed background-wakeup turn once the
// stream goes quiet after its final update, serialized with streamProcess.feed.
func (p *agyProcess) closeUnsolicitedTurn(generation uint64) {
	p.feedMu.Lock()
	defer p.feedMu.Unlock()
	if p.finished {
		return
	}
	for _, ev := range p.m.closeUnsolicited(time.Now().UnixMilli(), generation) {
		p.emit(ev)
	}
}

func (p *agyProcess) newID(kind string) string {
	return agyRequestMark + "-" + kind + "-" + p.epoch + "-" + strconv.FormatInt(p.seq.Add(1), 10)
}

// call sends one request and waits for its result.
func (p *agyProcess) call(kind, method string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
	return p.callContext(context.Background(), kind, method, params, timeout)
}

func (p *agyProcess) callContext(ctx context.Context, kind, method string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
	return p.requestBodyIDContext(ctx, p.newID(kind), method, map[string]any{"method": method, "params": params}, timeout)
}

// open runs a new process's handshake.
func (p *agyProcess) open(spec StartSpec) error {
	version := ""
	if spec.Lead != nil {
		version = spec.Lead.DaemonVersion
	}
	init, err := p.call(agyKindInit, "initialize", map[string]any{
		"protocolVersion": 1,
		// Without fs or terminal capabilities agy runs its own tools.
		"clientCapabilities": map[string]any{"fs": map[string]any{"readTextFile": false, "writeTextFile": false}, "terminal": false},
		"clientInfo":         map[string]any{"name": "ws", "title": "ws", "version": version},
	}, agyInitTimeout)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	var reply struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if json.Unmarshal(init, &reply) != nil || reply.ProtocolVersion != 1 {
		return fmt.Errorf("agy answered initialize with ACP protocol version %d; ws speaks version 1", reply.ProtocolVersion)
	}
	return p.openSession(context.Background(), spec)
}

func (p *agyProcess) openSession(ctx context.Context, spec StartSpec) error {
	mcpServers := []any{}
	if !spec.Ephemeral && spec.MCPSession != nil {
		mcpServers = []any{agyHTTPServer(spec.MCPSession)}
	}
	params := map[string]any{"cwd": spec.workDir(), "mcpServers": mcpServers}
	var appendText string
	if spec.Instructions != "" {
		text, err := os.ReadFile(spec.Instructions)
		if err != nil {
			return fmt.Errorf("read the instruction file: %w", err)
		}
		appendText = string(text)
	}
	if spec.Ephemeral {
		if sp := strings.TrimSpace(spec.SystemPrompt); sp != "" {
			if trimmed := strings.TrimRight(appendText, "\n"); trimmed != "" {
				appendText = trimmed + "\n\n" + sp + "\n"
			} else {
				appendText = sp + "\n"
			}
		}
	} else {
		// A cutover session runs commands through the ws exec tools, whose
		// completions come as messages; one outside it keeps run_command and
		// the rule that keeps its turn open while a background one runs.
		rule := agyBgShellRule
		if spec.ExecCutover {
			rule = agyExecCutoverInstruction
		}
		if trimmed := strings.TrimRight(appendText, "\n"); trimmed != "" {
			appendText = trimmed + "\n\n" + rule + "\n"
		} else {
			appendText = rule + "\n"
		}
	}
	if appendText != "" {
		params["_meta"] = map[string]any{"antigravity": map[string]any{"systemPrompt": map[string]any{"append": appendText}}}
	}
	kind, method := agyKindNew, "session/new"
	if !spec.Ephemeral && spec.SessionID != "" {
		kind, method = agyKindResume, "session/resume"
		params["sessionId"] = spec.SessionID
	}
	if _, err := p.callContext(ctx, kind, method, params, agySessionTimeout); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if !spec.Ephemeral && spec.MCPSession != nil {
		if c := spec.MCPSession.Connected; c != nil {
			select {
			case <-c:
			case <-p.exited:
				return errors.New("agy exited while it opened its session")
			case <-time.After(agyMCPConnectWait):
				return fmt.Errorf("agy did not reach the ws MCP route within %s of opening its session (a refused token only hides the ws tools), so the process was not started without them", agyMCPConnectWait)
			}
		}
	}
	effort := spec.Effort
	if stored := p.effort.Load(); stored != nil && *stored != "" {
		effort = *stored
	}
	if effort != "" {
		p.effort.Store(&effort)
	}
	model := spec.Model
	if stored := p.model.Load(); stored != nil && *stored != "" {
		model = *stored
	}
	if model != "" {
		if err := p.SetModel(model); err != nil {
			return fmt.Errorf("model: %w", err)
		}
	}
	if model == "" && effort != "" {
		if err := p.SetEffort(effort); err != nil {
			return fmt.Errorf("effort: %w", err)
		}
	}
	mode := ""
	if spec.Ephemeral {
		// Most restrictive non-plan native mode: asks permission on tool calls
		// so agyMapper's ephemeral auto-deny can reject them without stalling.
		mode = agyCutoverMode
	} else if spec.Lead != nil {
		mode = spec.Lead.PermissionMode
	}
	if spec.ExecCutover {
		// A cutover worker too: a resumed session reopens in whatever
		// mode it last ran in, and native auto runs commands unasked.
		mode = agyCutoverMode
	}
	if mode != "" && mode != p.m.mode() {
		if err := p.SetPermissionMode(mode); err != nil {
			return fmt.Errorf("permission mode: %w", err)
		}
	}
	return nil
}

// Reset closes the prior ACP session on the resident `agy acp` process and
// opens a fresh session via session/new, rebinding the mapper to the new id.
func (p *agyProcess) Reset(ctx context.Context) error {
	if !p.ephemeral {
		return ErrUnsupported
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if prev := p.m.session(); prev != "" {
		_, _ = p.callContext(ctx, agyKindClose, "session/close", map[string]any{"sessionId": prev}, agyControlTimeout)
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return p.openSession(ctx, p.ephemeralSpec)
}

// agyHTTPServer is the ws MCP server entry of a session request: the
// daemon's route with the session's bearer token.
func agyHTTPServer(s *MCPSession) map[string]any {
	return map[string]any{
		"type":    "http",
		"name":    ReservedMCPServer,
		"url":     s.URL,
		"headers": []any{map[string]any{"name": "Authorization", "value": "Bearer " + s.Token}},
	}
}

// Send submits a user turn as a session/prompt; sent while a turn runs it
// steers that turn.
func (p *agyProcess) Send(text string) error {
	return p.sendPrompt(text, text)
}

func (p *agyProcess) sendPrompt(text, acknowledgedText string) error {
	sid := p.m.session()
	if sid == "" {
		return errors.New("the agy session is not open")
	}
	id := p.newID(agyKindPrompt)
	p.m.promptSentText(id, acknowledgedText)
	err := p.writeLine(map[string]any{"jsonrpc": "2.0", "id": id, "method": "session/prompt", "params": map[string]any{
		"sessionId": sid,
		"prompt":    []any{map[string]any{"type": "text", "text": text}},
	}})
	if err != nil {
		p.m.promptDropped(id)
	}
	return err
}

// Steer writes text as another prompt: agy ends the running prompt and
// carries its run on under this one (seen on 1.5.9).
func (p *agyProcess) Steer(text string) error { return p.Send(text) }

func (p *agyProcess) steerCompaction(text string) (string, error) {
	return "", p.sendPrompt(text, "")
}

// ClearSteered is a no-op for agy. Its acknowledgement is the prior prompt's
// end_turn response, which also proves the replacement prompt took the run;
// the runner receives SteerAnswer and UserTaken together.
func (p *agyProcess) ClearSteered() error { return nil }

// Interrupt cancels the running turn; its prompt answers "cancelled".
func (p *agyProcess) Interrupt() error {
	return p.writeLine(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": p.m.session()}})
}

// SetModel sets the session's model config option. ACP has no value for
// "the default", so an empty model restarts the process instead. A model
// that offers a thought level gets the conversation's again: agy resets it
// on a switch.
func (p *agyProcess) SetModel(model string) error {
	if model == "" {
		return ErrUnsupported
	}
	base, suffix := agyBaseModel(model)
	if suffix != "" && p.effort.Load() == nil {
		p.effort.Store(&suffix)
	}
	if !p.m.offersValue("model", base) {
		if p.effort.Load() == nil {
			if level := p.m.defaultModelEffort(base); level != "" {
				p.effort.Store(&level)
			}
		}
		if effort := p.effort.Load(); effort != nil {
			if ok, err := p.setModelVariant(base, *effort); ok || err != nil {
				if err == nil {
					p.model.Store(&base)
				}
				return err
			}
		}
	}
	if err := p.setConfig("model", base); err != nil {
		return err
	}
	p.model.Store(&base)
	if effort := p.effort.Load(); effort != nil && *effort != "" {
		return p.SetEffort(*effort)
	}
	return nil
}

// SetEffort uses thought_level when offered, otherwise a suffixed model id.
// A worker fails rather than silently running at another tier's effort. A
// lead keeps an unavailable effort for its next model, as before.
func (p *agyProcess) SetEffort(effort string) error {
	if effort == "" {
		return ErrUnsupported
	}
	p.effort.Store(&effort)
	if p.m.offersValue("thought_level", effort) {
		if err := p.setConfig("thought_level", effort); err != nil {
			return err
		}
		p.emit(Event{Notice: "agy honored effort " + effort + " via thought_level"})
		return nil
	}
	model := p.m.modelName()
	if requested := p.model.Load(); requested != nil {
		model = *requested
	}
	base, _ := agyBaseModel(model)
	if ok, err := p.setModelVariant(base, effort); ok || err != nil {
		return err
	}
	if p.worker {
		return fmt.Errorf("agy offers neither thought_level %q nor model variant %q", effort, base+"-"+effort)
	}
	p.emit(Event{Notice: "agy did not offer effort " + effort + "; retained for the next model"})
	return nil
}

// agyBaseModel separates the thinking axis from agy's model vocabulary.
func agyBaseModel(model string) (string, string) {
	for _, effort := range []string{"xhigh", "high", "medium", "low"} {
		if base, ok := strings.CutSuffix(model, "-"+effort); ok {
			return base, effort
		}
	}
	return model, ""
}

func (p *agyProcess) setModelVariant(base, effort string) (bool, error) {
	variant := base + "-" + effort
	if p.m.offersValue("model", variant) {
		if err := p.setConfig("model", variant); err != nil {
			return true, err
		}
	} else if p.m.hasModel(variant) {
		body, err := p.call(agyKindConfig, "session/set_model", map[string]any{"sessionId": p.m.session(), "modelId": variant}, agyControlTimeout)
		if err != nil {
			return true, err
		}
		var reply struct {
			Models struct {
				CurrentModelID string `json:"currentModelId"`
			} `json:"models"`
		}
		_ = json.Unmarshal(body, &reply)
		if got := reply.Models.CurrentModelID; got != "" && got != variant {
			return true, fmt.Errorf("agy kept model %q after it was set to %q", got, variant)
		}
	} else {
		return false, nil
	}
	p.emit(Event{Notice: "agy honored effort " + effort + " via suffixed model id " + variant})
	return true, nil
}

// setConfig sets one config option and checks that agy took the value: a
// value agy does not apply comes back unchanged rather than refused.
func (p *agyProcess) setConfig(option, value string) error {
	body, err := p.call(agyKindConfig, "session/set_config_option", map[string]any{"sessionId": p.m.session(), "configId": option, "value": value}, agyControlTimeout)
	if err != nil {
		return err
	}
	if got, ok := agyConfigValue(body, option); ok && got != value {
		return fmt.Errorf("agy kept %s %q after it was set to %q", option, got, value)
	}
	return nil
}

// SetPermissionMode sets the session's mode.
func (p *agyProcess) SetPermissionMode(mode string) error {
	if mode == "bypass" || !slices.Contains(AgyPermissionModes, mode) {
		return fmt.Errorf("agy has no permission mode %q; its modes are %s", mode, strings.Join(AgyPermissionModes, ", "))
	}
	_, err := p.call(agyKindMode, "session/set_mode", map[string]any{"sessionId": p.m.session(), "modeId": mode}, agyControlTimeout)
	return err
}

// AnswerApproval answers a permission request with the option of the
// matching kind. ACP carries no denial message. A request it cannot answer
// (no such option, or no longer held) writes nothing: errNoApprovalAnswer.
func (p *agyProcess) AnswerApproval(req ApprovalRequest, allow bool, _ string) error {
	raw, option, err := p.m.answerFor(req.ID, allow)
	if err != nil {
		return fmt.Errorf("%v: %w", err, errNoApprovalAnswer)
	}
	return p.writeLine(map[string]any{"jsonrpc": "2.0", "id": raw, "result": map[string]any{
		"outcome": map[string]any{"outcome": "selected", "optionId": option},
	}})
}

// agyConfigValue is option's currentValue in a configOptions answer.
func agyConfigValue(body json.RawMessage, option string) (string, bool) {
	var r struct {
		ConfigOptions []agyConfigOption `json:"configOptions"`
	}
	if json.Unmarshal(body, &r) != nil {
		return "", false
	}
	for _, o := range r.ConfigOptions {
		if o.ID == option {
			return o.CurrentValue, true
		}
	}
	return "", false
}

type agyConfigOption struct {
	ID           string `json:"id"`
	CurrentValue string `json:"currentValue"`
	Options      []struct {
		Value string `json:"value"`
	} `json:"options"`
}

// agyApproval is a pending permission request: the raw id its answer
// echoes and the options it offered.
type agyApproval struct {
	raw        json.RawMessage
	toolCallID string
	options    []agyOption
}

type agyOption struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
}

// agyMapper maps agy's output lines. Its state is shared with the process's
// writers (Send registers its prompt before writing it), so it has a lock.
type agyMapper struct {
	mu sync.Mutex

	sessionID string
	curMode   string
	version   string
	// options are the config options the session offers now, by id, with
	// the values each takes.
	options map[string][]string
	// models are the ACP variant ids; model is the current config value.
	models []string
	model  string
	// initDemoted and sessionDemoted are the items the initialize answer
	// and the session's own reports show a mismatched shape for.
	initDemoted    []CapabilityItem
	sessionDemoted []CapabilityItem

	// reattachedPromptAfter marks the earlier daemon's open worker prompt.
	reattachedPromptAfter *uint64

	// outstanding tracks this daemon's prompt IDs. inTurn spans first
	// content through the prompt response or the unsolicited wakeup timer.
	outstanding     map[string]bool
	steerText       map[string]string
	steerOrder      []string
	recoveredSteers []string
	inTurn          bool
	unsolicitedTurn bool
	wakeStepHadText bool
	wakeSawUsage    bool
	openCalls       map[string]bool
	pendingTools    map[string]bool
	openBg          map[string]bool
	bgCalls         map[string]bool
	// cutover marks a process started under the exec cutover: its
	// run_command calls are refused, so no background command is tracked
	// (openBg, bgCalls), and requested and ranUnrequested correlate the
	// permission requests and execution evidence of its commands by tool
	// call id (Event.UnrequestedCommand).
	cutover            bool
	warnNativeSubagent bool
	requested          map[string]bool
	ranUnrequested     map[string]bool
	warnedSubagent     map[string]bool
	modelFailure       string
	// content is set while mapping one session/update that carried model
	// content (text, thought, its holder pulse, or a plan).
	content    bool
	turnSeq    int
	group      int
	groupCalls bool
	reply      strings.Builder
	replyAt    int64
	batches    batchTracker

	wakeTimer      *time.Timer
	wakeGeneration uint64
	onWakeClose    func(uint64)

	// approvals are the pending permission requests by ApprovalRequest.ID,
	// approvalOf their ids by tool call.
	approvals  map[string]*agyApproval
	approvalOf map[string]string

	// lead marks the hosted lead's mapper: only it maps native subagents
	// (agy's invoke_subagent) to rows; a worker's emits none. nativeLaunch
	// holds the invoke_subagent calls whose launch has not reported a final
	// status yet, so a failed launch can end its row.
	lead         bool
	ephemeral    bool
	nativeLaunch map[string]bool

	// lastContext retains the latest usage_update context so KindStatus rate
	// events preserve it instead of clearing Usage.Context.
	lastContext *wslive.UsageContext
	rate        agyOutputRateTracker
	// now is an optional local clock seam (epoch ms) for deterministic tests.
	now func() int64
}

// agyNativeSubagentTool is agy's native subagent tool name, surfaced to
// hosted-lead prompt steering when agy.prefer_native_subagent is false while
// keeping AgyCatalog().ItemSubagentRemoval unset.
const agyNativeSubagentTool = "invoke_subagent"

func agyLeadSubagentPolicy(cfg wsconfig.Options, reader wsconfig.SessionReader, wsKey string) LeadSubagentPolicy {
	// Session scope is unsupported for agy.* adapter overrides (noSession=true),
	// so resolve strictly from repo/project/global/builtin layers.
	_, _ = reader, wsKey
	r := wsconfig.NewResolver(cfg, map[string]string{
		wsconfig.ItemAgyPreferNativeSubagent: "true",
		wsconfig.ItemAgyAvoidAgySubagent:     "true",
	}, nil, nil)
	prefer := true
	if rv, err := r.Get("", wsconfig.ItemAgyPreferNativeSubagent); err == nil {
		if b, err := strconv.ParseBool(rv.Value); err == nil {
			prefer = b
		}
	}
	avoid := true
	if rv, err := r.Get("", wsconfig.ItemAgyAvoidAgySubagent); err == nil {
		if b, err := strconv.ParseBool(rv.Value); err == nil {
			avoid = b
		}
	}
	return LeadSubagentPolicy{
		Active:                true,
		PreferNativeSubagent:  prefer,
		AvoidSameHarnessSpawn: avoid,
		NativeSubagentTool:    agyNativeSubagentTool,
	}
}

func newAgyMapper(sessionID string) *agyMapper {
	return &agyMapper{
		sessionID:      sessionID,
		outstanding:    map[string]bool{},
		steerText:      map[string]string{},
		openCalls:      map[string]bool{},
		pendingTools:   map[string]bool{},
		openBg:         map[string]bool{},
		bgCalls:        map[string]bool{},
		batches:        batchTracker{epoch: newBatchTracker("agy").epoch, callIDs: true},
		approvals:      map[string]*agyApproval{},
		approvalOf:     map[string]string{},
		requested:      map[string]bool{},
		ranUnrequested: map[string]bool{},
		warnedSubagent: map[string]bool{},
		nativeLaunch:   map[string]bool{},
	}
}

// agyCommandTool is agy's native process tool.
const agyCommandTool = "run_command"

// agyCorrelationCap bounds the tool call ids a mapper correlates; past it
// both sets restart (ids are unique per call, so only a long process's
// oldest calls are forgotten).
const agyCorrelationCap = 4096

// agyCommandRan reports execution evidence in a run_command update's raw
// output: an exit code, or any output at all. A refused call ends failed
// with an empty output and no exit code.
func agyCommandRan(raw json.RawMessage) bool {
	var out struct {
		Output   string          `json:"output"`
		ExitCode json.RawMessage `json:"exit_code"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &out) != nil {
		return false
	}
	return len(out.ExitCode) > 0 && string(out.ExitCode) != "null" || strings.TrimSpace(out.Output) != ""
}

func (m *agyMapper) setWakeClose(fn func(uint64)) {
	m.mu.Lock()
	m.onWakeClose = fn
	m.mu.Unlock()
}

func (m *agyMapper) stopWakeTimer() {
	m.mu.Lock()
	m.stopWakeTimerLocked()
	m.mu.Unlock()
}

func (m *agyMapper) stopWakeTimerLocked() {
	if m.wakeTimer != nil {
		m.wakeTimer.Stop()
		m.wakeTimer = nil
	}
}

func (m *agyMapper) armWakeTimerLocked(d time.Duration) {
	if m.onWakeClose == nil || !m.unsolicitedTurn {
		return
	}
	m.stopWakeTimerLocked()
	m.wakeGeneration++
	generation := m.wakeGeneration
	fn := m.onWakeClose
	m.wakeTimer = time.AfterFunc(d, func() { fn(generation) })
}

func (m *agyMapper) armWakeIfReadyLocked(replaying bool) {
	if replaying || !m.unsolicitedTurn || len(m.approvals) > 0 {
		return
	}
	if len(m.openCalls) > 0 && !(m.wakeStepHadText && len(m.openCalls) == len(m.openBg)) {
		return
	}
	if m.wakeSawUsage {
		m.armWakeTimerLocked(agyWakeUsageQuiet)
	} else {
		m.armWakeTimerLocked(agyWakeIdleQuiet)
	}
}

type agyReplayObservation struct {
	promptOpen   bool
	pendingTools int
}

// replayState publishes the reconstructed prompt and tool state before the
// runner handles ReplayDone. An open burst without a known prompt can finish
// on the wakeup timer.
func (m *agyMapper) replayState(ev Event) []Event {
	if !ev.ReplayDone {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	promptOpen := m.reattachedPromptAfter != nil
	if m.inTurn && len(m.outstanding) == 0 && !promptOpen {
		m.unsolicitedTurn = true
		m.armWakeIfReadyLocked(false)
	}
	return []Event{{agyReplay: &agyReplayObservation{promptOpen: promptOpen, pendingTools: len(m.pendingTools)}}}
}

func (m *agyMapper) closeUnsolicited(now int64, generation uint64) []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	// A replaced timer may already be waiting to acquire the feed lock.
	if m.wakeTimer == nil || generation != m.wakeGeneration {
		return nil
	}
	if !m.unsolicitedTurn || !m.inTurn || len(m.outstanding) > 0 || len(m.approvals) > 0 {
		return nil
	}
	if len(m.openCalls) > 0 && !(m.wakeStepHadText && len(m.openCalls) == len(m.openBg)) {
		return nil
	}
	return m.endTurn(agyMessage{}, now)
}

func (m *agyMapper) session() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionID
}

// offers reports whether the session offers config option id now; before
// a session reported its options, it does.
func (m *agyMapper) offers(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.options[id]
	return m.options == nil || ok
}

// offersValue reports whether option id takes value now; before a session
// reported its options, it does.
func (m *agyMapper) offersValue(id, value string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.options == nil || slices.Contains(m.options[id], value)
}

func (m *agyMapper) modelName() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.model
}

// defaultModelEffort selects an offered variant when the caller chose a
// base model from Choices without an explicit effort. Prefer the current
// variant, otherwise the harness's own offered order.
func (m *agyMapper) defaultModelEffort(base string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, model := range append(append([]string{m.model}, m.options["model"]...), m.models...) {
		if name, effort := agyBaseModel(model); name == base && effort != "" {
			return effort
		}
	}
	return ""
}

func (m *agyMapper) hasModel(model string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Contains(m.models, model)
}

func (m *agyMapper) mode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.curMode
}

func (m *agyMapper) promptSent(id string) { m.promptSentText(id, "") }

func (m *agyMapper) promptSentText(id, text string) {
	m.mu.Lock()
	if m.unsolicitedTurn {
		m.rate.dropUntracked()
	}
	m.unsolicitedTurn = false
	m.wakeSawUsage = false
	m.stopWakeTimerLocked()
	// A second prompt while one is outstanding is a steer even when agy has
	// not emitted the first session update yet. The runner can submit mail as
	// soon as its turn starts, before that provider update arrives.
	if len(m.outstanding) > 0 {
		m.steerText[id] = text
		m.steerOrder = append(m.steerOrder, id)
	}
	m.outstanding[id] = true
	m.rate.promptSent(id)
	m.mu.Unlock()
}

func (m *agyMapper) promptDropped(id string) {
	m.mu.Lock()
	delete(m.outstanding, id)
	delete(m.steerText, id)
	m.steerOrder = slices.DeleteFunc(m.steerOrder, func(steerID string) bool { return steerID == id })
	m.rate.promptDropped(id)
	m.mu.Unlock()
}

// answerFor is the raw request id and option id that answer approval id. A
// denial is reject_once only: reject_always would make agy refuse that
// tool for the rest of the session, past the one request denied. A request
// without reject_once gets no answer (AnswerApproval: errNoApprovalAnswer)
// and stays for the user.
func (m *agyMapper) answerFor(id string, allow bool) (json.RawMessage, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.approvals[id]
	if a == nil {
		return nil, "", fmt.Errorf("agy has no pending permission request %q", id)
	}
	kinds := []string{"reject_once"}
	if allow {
		kinds = []string{"allow_once", "allow_always"}
	}
	for _, kind := range kinds {
		for _, o := range a.options {
			if o.Kind == kind {
				return a.raw, o.OptionID, nil
			}
		}
	}
	return nil, "", fmt.Errorf("agy's permission request %q offers no %s option", id, kinds[0])
}

type agyMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data,omitempty"`
	} `json:"error"`
}

func (m *agyMapper) mapLine(line []byte, replies lineReplies) []Event {
	return m.mapAgyLine(line, replies, 0)
}

func (m *agyMapper) mapAgyLine(line []byte, replies lineReplies, seq uint64) []Event {
	var msg agyMessage
	if json.Unmarshal(line, &msg) != nil {
		return nil
	}
	now := time.Now().UnixMilli()
	if m.now != nil {
		now = m.now()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case msg.Method != "" && hasID:
		return m.request(msg, replies, now)
	case msg.Method == "session/update":
		oldFailure := m.modelFailure
		m.content = false
		out := m.update(msg.Params, replies, now)
		if _, replaying := replies.(silentReplies); !replaying {
			n := len(m.pendingTools)
			out = append(out, Event{ModelActivity: &n, ModelContent: m.content})
			if oldFailure != m.modelFailure {
				if oldFailure != "" && m.modelFailure == "" {
					out = append(out, Event{Notice: "agy recovered after in-band model failure: " + oldFailure})
				}
				failure := m.modelFailure
				out = append(out, Event{ModelFailureUpdate: &failure})
			}
		}
		return out
	case msg.Method == "" && hasID:
		return m.response(msg, replies, now, seq)
	}
	return nil
}

// request maps an agent request: a permission request waits for the
// user; any other gets an error answer, so agy never waits on it.
func (m *agyMapper) request(msg agyMessage, replies lineReplies, now int64) []Event {
	if msg.Method != "session/request_permission" {
		replies.send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "method not found: " + msg.Method}})
		return []Event{{Notice: "agy asked for " + msg.Method + ", which ws does not serve"}}
	}
	var p struct {
		ToolCall struct {
			ToolCallID string          `json:"toolCallId"`
			Name       string          `json:"name"`
			Kind       string          `json:"kind"`
			Title      string          `json:"title"`
			Args       json.RawMessage `json:"args"`
			RawInput   json.RawMessage `json:"rawInput"`
		} `json:"toolCall"`
		Options []agyOption `json:"options"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	raw := compactRaw(msg.ID)
	id := p.ToolCall.ToolCallID
	if id == "" {
		id = "rpc:" + string(raw)
	}
	_, replaying := replies.(silentReplies)
	if m.ephemeral {
		// Ephemeral sessions have no interactive approver: deny immediately so
		// ACP permission requests never stall until caller Timeout.
		outcome := map[string]any{"outcome": "cancelled"}
		for _, kind := range []string{"reject_once", "reject_always"} {
			found := false
			for _, o := range p.Options {
				if o.Kind == kind {
					outcome = map[string]any{"outcome": "selected", "optionId": o.OptionID}
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		replies.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      raw,
			"result":  map[string]any{"outcome": outcome},
		})
		return []Event{{Notice: "agy ephemeral session auto-denied permission request for " + cmp.Or(p.ToolCall.Name, p.ToolCall.Title)}}
	}
	m.rate.breakRun(replaying)
	out := m.startTurn(nil, replaying)
	m.wakeSawUsage = false
	m.stopWakeTimerLocked()
	m.approvals[id] = &agyApproval{raw: raw, toolCallID: p.ToolCall.ToolCallID, options: p.Options}
	if p.ToolCall.ToolCallID != "" {
		m.approvalOf[p.ToolCall.ToolCallID] = id
		if len(m.requested) >= agyCorrelationCap {
			clear(m.requested)
			clear(m.ranUnrequested)
		}
		m.requested[p.ToolCall.ToolCallID] = true
	}
	input := p.ToolCall.Args
	if len(input) == 0 {
		input = p.ToolCall.RawInput
	}
	tool := p.ToolCall.Name
	if tool == "" {
		tool = p.ToolCall.Title
	}
	return append(out, Event{Approval: &ApprovalRequest{ID: id, Tool: tool, Kind: p.ToolCall.Kind, Input: input, Description: p.ToolCall.Title, ToolUseID: p.ToolCall.ToolCallID, StartedAt: now}})
}

func compactRaw(raw json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return slices.Clone(raw)
	}
	return b.Bytes()
}

// response maps the answer to a request ws wrote: a prompt's ends the turn
// unless a later prompt took the run over; any other answers its waiter
// and reports what the handshake learned.
func (m *agyMapper) response(msg agyMessage, replies lineReplies, now int64, seq uint64) []Event {
	var id string
	if json.Unmarshal(msg.ID, &id) != nil {
		return nil
	}
	kind, ok := agyIDKind(id)
	if !ok {
		return nil
	}
	if kind == agyKindPrompt {
		_, replaying := replies.(silentReplies)
		ours := m.outstanding[id]
		delete(m.outstanding, id)
		reattached := m.reattachedPromptAfter != nil && seq > *m.reattachedPromptAfter
		var steerEvents []Event
		tookSteer := false
		for i, steerID := range m.steerOrder {
			if m.outstanding[steerID] {
				text := m.steerText[steerID]
				delete(m.steerText, steerID)
				m.steerOrder = slices.Delete(m.steerOrder, i, i+1)
				tookSteer = true
				if text != "" {
					steerEvents = append(steerEvents, Event{SteerAnswer: &SteerAnswer{}}, Event{UserTaken: text})
				}
				break
			}
		}
		// Responses from before the open prompt cannot acknowledge its steers.
		recoveredSteer := !tookSteer && len(m.recoveredSteers) > 0 && (m.reattachedPromptAfter == nil || reattached)
		if recoveredSteer {
			text := m.recoveredSteers[0]
			m.recoveredSteers = m.recoveredSteers[1:]
			steerEvents = append(steerEvents, Event{SteerAnswer: &SteerAnswer{}}, Event{UserTaken: text})
		}
		var rateEv *Event
		if rate, ok := m.rate.complete(id, msg, replaying); ok {
			rateCopy := rate
			rateEv = &Event{Live: &wslive.Event{
				Kind:       wslive.KindStatus,
				Context:    cloneUsageContext(m.lastContext),
				OutputTps:  &rateCopy,
				OutputRate: &wslive.OutputRate{OutputTps: rateCopy, At: now},
			}}
		}
		if len(m.outstanding) > 0 || (reattached && recoveredSteer) || (!ours && !reattached && !m.inTurn) {
			if rateEv != nil {
				return append(steerEvents, *rateEv)
			}
			return steerEvents
		}
		if reattached {
			m.reattachedPromptAfter = nil
		}
		out := m.endTurn(msg, now)
		if rateEv != nil {
			// Emit KindStatus before TurnEnd so the runner updates telemetry
			// while the turn is active. Mark this exact pair so file-only
			// recovery persists both in TurnEnd's single snapshot.
			if n := len(out); n > 0 && out[n-1].TurnEnd {
				turnEnd := out[n-1]
				rateEv.LiveCoalescesTurnEnd = true
				out = append(out[:n-1], *rateEv, turnEnd)
			} else {
				out = append(out, *rateEv)
			}
		}
		return append(steerEvents, out...)
	}
	a := controlAnswer{ok: msg.Error == nil, body: msg.Result}
	if msg.Error != nil {
		a.err = msg.Error.Message
	}
	replies.answer(id, a)
	if msg.Error != nil {
		return nil
	}
	switch kind {
	case agyKindInit:
		return m.initialized(msg.Result)
	case agyKindNew, agyKindResume:
		return m.opened(msg.Result)
	case agyKindConfig:
		m.configOptions(msg.Result)
	}
	return nil
}

// initialized reads the harness version and the init shape.
//
// Init-field to item mapping that gates demotion (the catalog's
// HarnessReport):
//
//	agentInfo.version              -> the version the profile resolves at
//	sessionCapabilities.resume     -> resume: absent
//	(session answer) configOptions -> model/effort: no model or thought_level option
//	(session answer) modes         -> approval: no available modes
//	available_commands_update      -> compaction: a list without "compact"
//
// Steer, interrupt, follow-up, usage and the other items have no shape to
// check: their grade follows the version alone.
func (m *agyMapper) initialized(result json.RawMessage) []Event {
	var r struct {
		AgentInfo struct {
			Version string `json:"version"`
		} `json:"agentInfo"`
		AgentCapabilities struct {
			SessionCapabilities map[string]json.RawMessage `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	_ = json.Unmarshal(result, &r)
	m.version = r.AgentInfo.Version
	m.initDemoted = nil
	if _, ok := r.AgentCapabilities.SessionCapabilities["resume"]; !ok {
		m.initDemoted = append(m.initDemoted, ItemResume)
	}
	return []Event{m.harnessReport()}
}

func (m *agyMapper) harnessReport() Event {
	demoted := slices.Clone(m.initDemoted)
	for _, item := range m.sessionDemoted {
		if !slices.Contains(demoted, item) {
			demoted = append(demoted, item)
		}
	}
	return Event{Harness: &HarnessReport{Version: m.version, Demoted: demoted}}
}

// opened reads a session/new or session/resume answer: the session id,
// the mode, and the session shape.
func (m *agyMapper) opened(result json.RawMessage) []Event {
	var r struct {
		SessionID string `json:"sessionId"`
		Models    struct {
			CurrentModelID  string `json:"currentModelId"`
			AvailableModels []struct {
				ModelID string `json:"modelId"`
			} `json:"availableModels"`
		} `json:"models"`
		Modes *struct {
			CurrentModeID  string            `json:"currentModeId"`
			AvailableModes []json.RawMessage `json:"availableModes"`
		} `json:"modes"`
		ConfigOptions []agyConfigOption `json:"configOptions"`
	}
	if json.Unmarshal(result, &r) != nil {
		return nil
	}
	var out []Event
	if r.SessionID != "" {
		if m.sessionID != "" && m.sessionID != r.SessionID {
			// Rebinding the mapper to a fresh session after Reset: drop prior
			// session turn, reply, and approval state.
			m.inTurn = false
			m.unsolicitedTurn = false
			m.wakeStepHadText = false
			m.wakeSawUsage = false
			m.stopWakeTimerLocked()
			m.reply.Reset()
			m.replyAt = 0
			m.modelFailure = ""
			m.lastContext = nil
			clear(m.outstanding)
			clear(m.steerText)
			m.steerOrder = nil
			clear(m.openCalls)
			clear(m.pendingTools)
			clear(m.openBg)
			clear(m.bgCalls)
			clear(m.approvals)
			clear(m.approvalOf)
		}
		m.sessionID = r.SessionID
		out = append(out, Event{SessionID: r.SessionID})
	}
	m.models = nil
	for _, model := range r.Models.AvailableModels {
		m.models = append(m.models, model.ModelID)
	}
	m.model = r.Models.CurrentModelID
	m.configOptions(result)
	// The thought level is per model (agy drops the option for a model
	// without one), so only a missing model option is a shape mismatch.
	m.sessionDemoted = slices.DeleteFunc(m.sessionDemoted, func(item CapabilityItem) bool {
		return item == ItemModelEffort || item == ItemApproval
	})
	if _, ok := m.options["model"]; !ok {
		m.sessionDemoted = append(m.sessionDemoted, ItemModelEffort)
	}
	if r.Modes == nil || len(r.Modes.AvailableModes) == 0 {
		m.sessionDemoted = append(m.sessionDemoted, ItemApproval)
	} else if r.Modes.CurrentModeID != "" {
		m.curMode = r.Modes.CurrentModeID
		out = append(out, Event{PermissionMode: r.Modes.CurrentModeID})
	}
	return append(out, m.harnessReport())
}

// configOptions keeps the model and thought-level values a session
// reports for Choices.
func (m *agyMapper) configOptions(body json.RawMessage) {
	var r struct {
		ConfigOptions []agyConfigOption `json:"configOptions"`
	}
	if json.Unmarshal(body, &r) != nil || r.ConfigOptions == nil {
		return
	}
	m.options = map[string][]string{}
	for _, o := range r.ConfigOptions {
		values := []string{}
		for _, v := range o.Options {
			values = append(values, v.Value)
		}
		m.options[o.ID] = values
	}
	var models []ModelChoice
	var efforts []string
	seen := map[string]bool{}
	for _, o := range r.ConfigOptions {
		if o.ID == "model" {
			if o.CurrentValue != "" && o.CurrentValue != m.model {
				m.rate.clearWindow()
			}
			m.model = o.CurrentValue
		}
		for _, v := range o.Options {
			switch o.ID {
			case "model":
				base, _ := agyBaseModel(v.Value)
				if !seen[base] {
					models = append(models, ModelChoice{ID: base})
					seen[base] = true
				}
			case "thought_level":
				efforts = append(efforts, v.Value)
			}
		}
	}
	if len(models) == 0 && len(efforts) == 0 || m.ephemeral {
		return
	}
	agyChoices.mu.Lock()
	defer agyChoices.mu.Unlock()
	if len(models) > 0 {
		agyChoices.models = models
	}
	if len(efforts) > 0 {
		agyChoices.efforts = efforts
	}
}

// startTurn opens a turn at its first content: a turn ws's own prompt began
// is already running in the runner and a replayed one is rebuilt here. Live
// content without a known prompt opens an unsolicited turn bounded by wakeTimer.
func (m *agyMapper) startTurn(out []Event, replaying bool) []Event {
	if m.inTurn {
		return out
	}
	m.inTurn = true
	m.modelFailure = ""
	m.wakeStepHadText = false
	m.wakeSawUsage = false
	if len(m.outstanding) == 0 && m.reattachedPromptAfter == nil && !replaying {
		m.unsolicitedTurn = true
	}
	m.turnSeq++
	m.group, m.groupCalls = 0, false
	return append(out, Event{TurnStart: true})
}

// flushReply ends the reply the turn streams so far.
func (m *agyMapper) flushReply(out []Event) []Event {
	text := m.reply.String()
	m.reply.Reset()
	m.replyAt = 0
	if strings.TrimSpace(text) == "" {
		return out
	}
	return append(out, Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: text, Final: true}})
}

// stripReplyFailure removes a terminal failure line once later model activity
// proves agy recovered. The live partial remains a diagnostic, but the final
// reply and parent report contain only the successful attempt.
func (m *agyMapper) stripReplyFailure(failure string) {
	if failure == "" {
		return
	}
	text := m.reply.String()
	trimmed := strings.TrimRight(text, " \t\r\n")
	if !strings.HasSuffix(trimmed, failure) {
		return
	}
	prefix := strings.TrimSuffix(trimmed, failure)
	prefix = strings.TrimSuffix(prefix, "\n")
	prefix = strings.TrimSuffix(prefix, "\r")
	m.reply.Reset()
	m.reply.WriteString(prefix)
	m.replyAt = 0
}

func (m *agyMapper) groupID() string {
	return "t" + strconv.Itoa(m.turnSeq) + "g" + strconv.Itoa(m.group)
}

// endTurn settles a turn at its last prompt's answer (or at the quiet end of
// a background-wakeup turn): the reply, every permission request still
// pending, any non-daemon run_command calls that went async across a normal
// end, and the open tool batch end with it.
func (m *agyMapper) endTurn(msg agyMessage, now int64) []Event {
	out := m.flushReply(nil)
	for id := range m.approvals {
		out = append(out, Event{ApprovalDone: id})
	}
	clear(m.approvals)
	clear(m.approvalOf)
	var r struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(msg.Result, &r)
	normalEnd := msg.Error == nil && (r.StopReason == "" || r.StopReason == "end_turn")
	if b := m.batches.finish(now, !normalEnd); b != nil {
		out = append(out, *b)
	}
	if normalEnd && len(m.openBg) > 0 {
		for id := range m.openBg {
			m.bgCalls[id] = true
		}
		n := len(m.bgCalls)
		out = append(out, Event{BackgroundTasks: &n})
	}
	clear(m.openBg)
	clear(m.openCalls)
	clear(m.pendingTools)
	modelFailure := m.modelFailure
	m.modelFailure = ""
	m.inTurn = false
	m.unsolicitedTurn = false
	m.wakeStepHadText = false
	m.wakeSawUsage = false
	m.stopWakeTimerLocked()
	m.rate.dropUntracked()
	ev := Event{TurnEnd: true, ModelFailure: modelFailure}
	switch {
	case msg.Error != nil:
		ev.TurnError = "agy: " + msg.Error.Message
	case r.StopReason == "cancelled":
		ev.TurnReason = wslive.TurnAborted
	case r.StopReason != "" && r.StopReason != "end_turn":
		ev.TurnError = "agy stopped the turn: " + r.StopReason
	}
	if alert := agyUsageLimitAlert(msg, modelFailure, now); alert != nil {
		out = append(out, Event{Alert: alert})
	}
	return append(out, ev)
}

// isAgyQuotaText reports whether an ACP error payload or anchored in-band
// model-failure line indicates a quota or rate-limit exhaustion rather than a
// transient transport/capacity stall.
func isAgyQuotaText(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "resource_exhausted"),
		strings.Contains(lower, "usage limit"),
		strings.Contains(lower, "usage_limit"),
		strings.Contains(lower, "rate limit"),
		strings.Contains(lower, "rate_limit"),
		strings.Contains(lower, "too many requests"),
		strings.Contains(lower, "quota"),
		strings.Contains(lower, "exhausted your capacity"),
		hasHTTPStatus429(lower):
		return true
	}
	return false
}

// hasHTTPStatus429 matches HTTP/API status-code 429 shapes while avoiding false
// positives on unrelated numbers such as line numbers or ports ("line 1429", "port 4290").
func hasHTTPStatus429(lower string) bool {
	if strings.TrimSpace(lower) == "429" {
		return true
	}
	for _, pat := range []string{
		"http 429", "http/1.1 429", "http/2 429",
		"status 429", "status: 429", "status=429",
		"code 429", "code: 429", "code=429",
		"error 429", "error: 429",
		"429 too many", "(429)", "[429]",
	} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// agyUsageLimitAlert classifies an ACP prompt error or anchored in-band model
// failure into a common NoticeUsageLimit alert.
func agyUsageLimitAlert(msg agyMessage, modelFailure string, now int64) *wslive.Notice {
	if msg.Error != nil {
		dataText := string(msg.Error.Data)
		if msg.Error.Code == 429 || msg.Error.Code == 8 || isAgyQuotaText(msg.Error.Message) || isAgyQuotaText(dataText) {
			text := strings.TrimSpace(msg.Error.Message)
			if text == "" {
				text = "usage limit reached"
			}
			return &wslive.Notice{
				Kind:     wslive.NoticeUsageLimit,
				Text:     "agy: " + text,
				ResetsAt: parseAgyErrorResetTime(msg.Error.Data, now),
			}
		}
	}
	if modelFailure != "" && isAgyQuotaText(modelFailure) {
		return &wslive.Notice{
			Kind: wslive.NoticeUsageLimit,
			Text: modelFailure,
		}
	}
	return nil
}

// parseAgyErrorResetTime extracts a future reset timestamp (epoch ms) from an
// optional ACP error.data JSON object when present.
func parseAgyErrorResetTime(raw json.RawMessage, now int64) int64 {
	if len(raw) == 0 {
		return 0
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return 0
	}
	for _, key := range []string{"resetsAt", "resets_at", "reset_time", "resetTime"} {
		val, ok := obj[key]
		if !ok || len(val) == 0 {
			continue
		}
		var s string
		if json.Unmarshal(val, &s) == nil && s != "" {
			if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s)); err == nil {
				if ms := t.UnixMilli(); ms > now {
					return ms
				}
			}
		}
		var n float64
		if json.Unmarshal(val, &n) == nil && n > 0 {
			if n < 1e12 {
				n *= 1000
			}
			if ms := int64(n); ms > now {
				return ms
			}
		}
	}
	return 0
}

// agyMCPToolName is a tool's current name under agy's MCP naming: the
// sanitized tool name prefixed by its server, so ws tracker.update is
// mcp_ws_tracker_update.
func agyMCPToolName(server, tool string) string {
	sanitized, _ := ApplyToolNameScheme(ToolNameSchemeSanitize, tool)
	return "mcp_" + server + "_" + sanitized
}

// agyMCPToolNameMatches also accepts the dotted spelling reported by agy
// connections created before ws began advertising sanitized aliases.
func agyMCPToolNameMatches(name, server, tool string) bool {
	return name == agyMCPToolName(server, tool) || name == "mcp_"+server+"_"+tool
}

type agyUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	// Content is a message chunk's content block, but a tool call
	// update's content list: decoding it as either would fail the other.
	Content           json.RawMessage   `json:"content"`
	ToolCallID        string            `json:"toolCallId"`
	Name              string            `json:"name"`
	Title             string            `json:"title"`
	Status            string            `json:"status"`
	Args              json.RawMessage   `json:"args"`
	RawInput          json.RawMessage   `json:"rawInput"`
	RawOutput         json.RawMessage   `json:"rawOutput"`
	Used              *int64            `json:"used"`
	Size              int64             `json:"size"`
	CurrentModeID     string            `json:"currentModeId"`
	AvailableCommands []json.RawMessage `json:"availableCommands"`
}

// update maps a session/update notification.
func (m *agyMapper) update(params json.RawMessage, replies lineReplies, now int64) []Event {
	var p struct {
		SessionID string    `json:"sessionId"`
		Update    agyUpdate `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil
	}
	if m.ephemeral && p.SessionID != "" && m.sessionID != "" && p.SessionID != m.sessionID {
		return nil
	}
	u := p.Update
	_, replaying := replies.(silentReplies)
	if u.SessionUpdate != "agent_message_chunk" {
		m.rate.breakRun(replaying)
	}
	switch u.SessionUpdate {
	case "agent_message_chunk", "agent_thought_chunk", "ws_activity", "plan":
		// A holder pulse stands in for dropped thought lines.
		m.content = true
	}
	switch u.SessionUpdate {
	case "agent_message_chunk":
		out := m.startTurn(nil, replaying)
		var c struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(u.Content, &c) != nil || c.Type != "text" || c.Text == "" {
			m.armWakeIfReadyLocked(replaying)
			return out
		}
		m.rate.messageChunk(now, replaying)
		m.wakeStepHadText = true
		m.wakeSawUsage = false
		m.armWakeIfReadyLocked(replaying)
		if m.groupCalls {
			// Text after a tool batch is the next message's.
			m.group++
			m.groupCalls = false
		}
		before := m.reply.String()
		oldFailure := m.modelFailure
		m.reply.WriteString(c.Text)
		m.modelFailure = agyReplyTailFailure(m.reply.String())
		if oldFailure != "" && m.modelFailure == "" {
			m.reply.Reset()
			m.reply.WriteString(before)
			m.stripReplyFailure(oldFailure)
			m.reply.WriteString(c.Text)
		}
		if m.replyAt != 0 && now-m.replyAt < claudePartialEvery {
			return out
		}
		m.replyAt = now
		if strings.TrimSpace(m.reply.String()) == "" {
			return out
		}
		return append(out, Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: m.reply.String()}})
	case "agent_thought_chunk":
		out := m.startTurn(nil, replaying)
		m.stripReplyFailure(m.modelFailure)
		m.modelFailure = ""
		m.wakeSawUsage = false
		m.armWakeIfReadyLocked(replaying)
		return out
	case "ws_activity":
		// A holder pulse can trail a prompt response within one read.
		// Extend an open turn's quiet window without starting a new turn.
		if m.unsolicitedTurn && !replaying {
			m.wakeSawUsage = false
			m.armWakeIfReadyLocked(false)
		}
	case "tool_call":
		out := m.startTurn(nil, replaying)
		m.stripReplyFailure(m.modelFailure)
		out = m.flushReply(out)
		m.modelFailure = ""
		m.wakeStepHadText = false
		m.wakeSawUsage = false
		m.stopWakeTimerLocked()
		name := u.Name
		if name == "" {
			name = u.Title
		}
		if u.ToolCallID != "" && u.Status != "completed" && u.Status != "failed" {
			m.pendingTools[u.ToolCallID] = true
			isDaemon := false
			if name == agyCommandTool {
				var args struct {
					IsDaemon bool `json:"IsDaemon"`
				}
				_ = json.Unmarshal(u.Args, &args)
				isDaemon = args.IsDaemon
				if !isDaemon && !m.cutover {
					m.openBg[u.ToolCallID] = true
				}
			}
			if !isDaemon {
				m.openCalls[u.ToolCallID] = true
			}
		}
		out = append(out, m.batches.addCall(m.groupID(), u.ToolCallID, name, argsPreview(u.Args), len(agyAuditBody(u.Args, u.RawInput)), now)...)
		m.groupCalls = true
		if name == agyInvokeSubagentTool {
			out = append(out, m.nativeStart(u, now)...)
			if m.warnNativeSubagent && u.ToolCallID != "" && !m.warnedSubagent[u.ToolCallID] {
				if len(m.warnedSubagent) >= agyCorrelationCap {
					clear(m.warnedSubagent)
				}
				m.warnedSubagent[u.ToolCallID] = true
				out = append(out, Event{UnrequestedSubagent: u.ToolCallID})
			}
		}
		return append(out, m.toolStatus(u, replaying, now)...)
	case "tool_call_update":
		return m.toolStatus(u, replaying, now)
	case "usage_update":
		if m.inTurn && len(m.approvals) == 0 && (len(m.openCalls) == 0 || (m.wakeStepHadText && len(m.openCalls) == len(m.openBg))) {
			m.wakeSawUsage = true
			m.armWakeIfReadyLocked(replaying)
		}
		if u.Used == nil {
			return nil
		}
		used := *u.Used
		ctx := &wslive.UsageContext{Tokens: &used, Window: u.Size}
		if u.Size > 0 {
			pct := float64(used) * 100 / float64(u.Size)
			ctx.Percent = &pct
		}
		m.lastContext = cloneUsageContext(ctx)
		return []Event{{Live: &wslive.Event{Kind: wslive.KindStatus, Context: ctx}}}
	case "available_commands_update":
		return m.commands(u.AvailableCommands)
	case "current_mode_update":
		if u.CurrentModeID == "" {
			return nil
		}
		m.curMode = u.CurrentModeID
		return []Event{{PermissionMode: u.CurrentModeID}}
	case "config_option_update":
		m.configOptions(params2configOptions(params))
	}
	return nil
}

// params2configOptions lifts a config_option_update's configOptions to the
// shape of a set_config_option answer.
func params2configOptions(params json.RawMessage) json.RawMessage {
	var p struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil
	}
	return p.Update
}

// toolStatus ends a tool call that reached a final status: its batch
// entry, its permission request, any tracked background command, and a
// tracker write it made.
func (m *agyMapper) toolStatus(u agyUpdate, replaying bool, now int64) []Event {
	if u.Status != "completed" && u.Status != "failed" {
		return nil
	}
	failed := u.Status == "failed"
	var out []Event
	if name := cmp.Or(u.Name, u.Title); name == agyCommandTool && u.ToolCallID != "" && !m.requested[u.ToolCallID] && !m.ranUnrequested[u.ToolCallID] && agyCommandRan(u.RawOutput) {
		// An in_progress announcement may precede the request; only output
		// or an exit code shows the command actually ran without one.
		m.ranUnrequested[u.ToolCallID] = true
		out = append(out, Event{UnrequestedCommand: u.ToolCallID})
	}
	delete(m.pendingTools, u.ToolCallID)
	delete(m.openCalls, u.ToolCallID)
	delete(m.openBg, u.ToolCallID)
	out = append(out, m.batches.endCall(u.ToolCallID, failed, len(agyAuditBody(u.RawOutput, u.Content)), now)...)
	if m.nativeLaunch[u.ToolCallID] {
		// A completed launch only means the child runs asynchronously; its
		// row stays running until the runner settles it at the parent's
		// logical turn end. A failed launch ends the row here.
		delete(m.nativeLaunch, u.ToolCallID)
		if failed {
			out = append(out, Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: wslive.NativeAgyPrefix + u.ToolCallID, Status: wslive.StatusFailed, At: now}})
		}
	}
	if id, ok := m.approvalOf[u.ToolCallID]; ok {
		delete(m.approvalOf, u.ToolCallID)
		delete(m.approvals, id)
		out = append(out, Event{ApprovalDone: id})
	}
	if m.bgCalls[u.ToolCallID] {
		delete(m.bgCalls, u.ToolCallID)
		n := len(m.bgCalls)
		out = append(out, Event{BackgroundTasks: &n})
	}
	if !failed && agyMCPToolNameMatches(u.Name, ReservedMCPServer, TrackerWriteTool) {
		out = append(out, resultEvents(TrackerWriteTool, "")...)
	}
	m.armWakeIfReadyLocked(replaying)
	return out
}

// forgetNativeLaunches drops the launch correlation of native rows the
// runner settled (cancellation, a replay's end, an accepted turn end): no
// launch failure can change a settled row, so a launch that never reports
// a final status leaves nothing behind. A watchdog continuation settles
// nothing, so a launch failure reported after it still ends its row.
func (p *agyProcess) forgetNativeLaunches(rowIDs []string) {
	p.m.mu.Lock()
	defer p.m.mu.Unlock()
	for _, id := range rowIDs {
		delete(p.m.nativeLaunch, strings.TrimPrefix(id, wslive.NativeAgyPrefix))
	}
}

// agyInvokeSubagentTool is agy's native subagent launch tool. It stays an
// adapter-local name: the catalog's native-subagent removal entry is left
// unset on purpose, since filling it adds a "never use" line to the hosted
// lead prompt.
const agyInvokeSubagentTool = "invoke_subagent"

// nativeStart opens a running native subagent row for a hosted lead's
// invoke_subagent call: id agy-native:<toolCallId>, labelled by Role, else
// TypeName, else "subagent". The Prompt argument is never read into the row.
// agy reports no child id, progress, or end, so the row carries no wake,
// turn, or batch authority and ends only by a failed launch (toolStatus) or
// the runner's end-unknown settlement.
func (m *agyMapper) nativeStart(u agyUpdate, now int64) []Event {
	if !m.lead || u.ToolCallID == "" {
		return nil
	}
	var args struct {
		Role     string `json:"Role"`
		TypeName string `json:"TypeName"`
	}
	_ = json.Unmarshal(u.Args, &args)
	label := strings.TrimSpace(args.Role)
	if label == "" {
		label = strings.TrimSpace(args.TypeName)
	}
	if label == "" {
		label = "subagent"
	}
	if u.Status != "completed" && u.Status != "failed" {
		m.nativeLaunch[u.ToolCallID] = true
	}
	out := []Event{{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: wslive.NativeAgyPrefix + u.ToolCallID, Label: label, Status: wslive.StatusRunning, At: now}}}
	if u.Status == "failed" {
		out = append(out, Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: wslive.NativeAgyPrefix + u.ToolCallID, Status: wslive.StatusFailed, At: now}})
	}
	return out
}

// commands maps agy's command list (its slash commands and skills); a list
// without "compact" demotes the compaction item.
func (m *agyMapper) commands(raw []json.RawMessage) []Event {
	list := make([]wslive.SlashCommand, 0, len(raw))
	compact := false
	for _, r := range raw {
		var c struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if json.Unmarshal(r, &c) != nil || c.Name == "" {
			continue
		}
		compact = compact || c.Name == "compact"
		list = append(list, wslive.SlashCommand{Name: c.Name, Description: c.Description})
	}
	out := []Event{{Live: &wslive.Event{Kind: wslive.KindCommands, Commands: list}}}
	had := slices.Contains(m.sessionDemoted, ItemCompaction)
	if compact == had {
		if compact {
			m.sessionDemoted = slices.DeleteFunc(m.sessionDemoted, func(item CapabilityItem) bool { return item == ItemCompaction })
		} else {
			m.sessionDemoted = append(m.sessionDemoted, ItemCompaction)
		}
		out = append([]Event{m.harnessReport()}, out...)
	}
	return out
}

func (AgyBackend) pruneEphemeralStateDir(dir, keepSession string, maxKeep int) {
	pruneAgyGeminiDir(dir, keepSession, maxKeep)
}

// pruneAgyGeminiDir bounds growth inside an isolated agy --gemini-dir by
// removing the oldest brain/ session directories and .db session files beyond
// maxKeep, never touching the active keepSession or anything outside geminiDir.
func pruneAgyGeminiDir(geminiDir, keepSession string, maxKeep int) {
	if geminiDir == "" || maxKeep <= 0 {
		return
	}
	type agedPath struct {
		path    string
		modTime time.Time
	}

	// Prune brain/<session-id> subdirectories under <geminiDir>/antigravity-cli/brain
	// and <geminiDir>/brain.
	for _, brainDir := range []string{
		filepath.Join(geminiDir, "antigravity-cli", "brain"),
		filepath.Join(geminiDir, "brain"),
	} {
		entries, err := os.ReadDir(brainDir)
		if err != nil {
			continue
		}
		var dirs []agedPath
		for _, e := range entries {
			if !e.IsDir() || (keepSession != "" && e.Name() == keepSession) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			dirs = append(dirs, agedPath{path: filepath.Join(brainDir, e.Name()), modTime: info.ModTime()})
		}
		if len(dirs) <= maxKeep {
			continue
		}
		sort.Slice(dirs, func(i, j int) bool { return dirs[i].modTime.Before(dirs[j].modTime) })
		for _, victim := range dirs[:len(dirs)-maxKeep] {
			_ = os.RemoveAll(victim.path)
		}
	}

	// Prune excess .db files (and matching -wal/-shm sidecars) inside geminiDir.
	var dbs []agedPath
	_ = filepath.WalkDir(geminiDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".db") {
			return nil
		}
		if keepSession != "" && strings.Contains(d.Name(), keepSession) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		dbs = append(dbs, agedPath{path: path, modTime: info.ModTime()})
		return nil
	})
	if len(dbs) > maxKeep {
		sort.Slice(dbs, func(i, j int) bool { return dbs[i].modTime.Before(dbs[j].modTime) })
		for _, victim := range dbs[:len(dbs)-maxKeep] {
			_ = os.Remove(victim.path)
			_ = os.Remove(victim.path + "-wal")
			_ = os.Remove(victim.path + "-shm")
		}
	}
}
