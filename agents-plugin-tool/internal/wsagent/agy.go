package wsagent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsharnessenv"
	"github.com/kang-sw/devenv/internal/wsholder"
)

// agy.go is the Antigravity backend: `agy acp`, the Agent Client Protocol
// (v1: JSON-RPC 2.0 over stdio, one message per line). Hosted leads run
// under a holder, as do workers using the default
// agent with their instructions appended to its system prompt. The protocol
// itself runs in the shared ACP core (acp_process.go, acp_mapper*.go,
// acp_wire.go); agy's differences are its acpDialect (agy_dialect.go).
//
// The wire facts it relies on are pinned by agy_fixture_test.go against
// the recorded agy-<version>-*.jsonl fixtures:
//
//   - A lead's process runs under a session holder, which keeps agy's output
//     (never ws's input) in its ring. Every request ws writes therefore
//     carries an id that names its kind (acpIDKind), so a later daemon
//     replaying the ring tells a prompt's response from a control's.
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
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	sp, err := startChild(spec, cmd, m, acpProtocol{})
	if err != nil {
		return nil, err
	}
	p := &acpProcess{
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
	if err := p.open(ctx, spec); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("agy backend: %w", err)
	}
	return p, nil
}

// newAgyMapper is the shared ACP mapper under agy's dialect.
func newAgyMapper(sessionID string) *acpMapper {
	return newACPMapper(sessionID, agyDialect{})
}
