package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsharnessenv"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// CodexBackend drives one sustained `codex app-server` process over stdio.
// Hosted leads and workers run under a holder, which owns their pipes
// across daemon replacement, as for agy and Claude.
type CodexBackend struct {
	// Command is the Codex executable; empty looks up "codex" on PATH.
	Command string
	// ResolveCodexHome returns the active slot's resolved CODEX_HOME for
	// model-cache lookups. Nil falls back to the D10 default resolver.
	ResolveCodexHome func() string
}

func (b CodexBackend) registration() (string, BackendRegistration) {
	return BackendCodex, BackendRegistration{
		Backend:           b,
		LeadProfile:       LeadProfileCodex,
		PermissionModes:   CodexPermissionModes,
		PermissionLevels:  LeadPermissionLevels(),
		Catalog:           CodexCatalog(),
		ManualHarness:     BackendCodex,
		WorkerCapable:     true,
		WorkerProfile:     AgentWorkerProfile(BackendCodex),
		WorkerHTTPMCP:     true,
		WorkerAutoApprove: true,
		Permission:        codexPermissionPolicy(),
		Connection:        codexConnectionPolicy(),
		Dashboard:         codexDashboardTraits(),
		Lifecycle: LifecyclePolicy{
			ComposedLeadInstructions: true,
			LeadHTTPMCP:              true,
			ExecReadTools:            true,
		},
		// No OneShot, probed 2026-11-22 with codex-cli 0.170.0: `codex exec
		// --ignore-user-config --ephemeral -s read-only -c
		// features.shell_tool=false -c web_search=disabled` still offered
		// the model exec, apply_patch, spawn_agent, goal tools, and the
		// codex_apps connector MCP tools. No `codex exec` flag removes all
		// tools and MCP servers, so no one-shot mode is verifiable.
		// features.shell_tool=false, verified on start and resume
		// (testdata/codex-0.160.0-multi-agent-feature-off.jsonl).
		ExecCutover: &ExecCutoverSupport{Since: "0.160.0"},
		PlaybookTerminology: PlaybookTerminology{
			ExploreAgent:  "an explorer subagent",
			SpawnIdiom:    "creating a new Codex task",
			ContinueIdiom: "resuming the agent using its task id",
		},
		Profiles: []BuiltinProfileRegistration{
			agentWorkerBuiltinProfile(BackendCodex, nil),
			leadBuiltinProfile(LeadProfileCodex, BackendCodex, ""),
		},
	}
}

// A thread request may connect the user's MCP servers before answering.
var codexRequestTimeout = 2 * time.Minute

func (b CodexBackend) Executable() string {
	if b.Command != "" {
		return b.Command
	}
	return "codex"
}

type codexChoiceEntry struct {
	models  []ModelChoice
	efforts []string
	at      time.Time
}

// codexChoices holds model/list from the last opened process per resolved
// CODEX_HOME, stamped when it arrived. A fresh report takes precedence over
// that slot's cache file listing, and a newer one replaces it; one older than
// choicesTTL answers nothing, so an updated CLI shows through the file again.
var codexChoices struct {
	sync.Mutex
	byHome map[string]codexChoiceEntry
}

func (b CodexBackend) Choices(context.Context) ([]ModelChoice, []string, error) {
	return b.hostedChoices()
}

// codexEnv removes parent-session identities, but keeps the harness-owned
// CODEX_HOME and authentication. When spec.AccountConfigDir is set, it binds
// CODEX_HOME to that isolated slot directory. The HTTP MCP bearer exists only
// in the child's environment, never in argv, the thread config, or its transcript.
func codexEnv(spec StartSpec) []string {
	set := map[string]string{}
	if spec.Lead != nil {
		for name, value := range spec.Lead.Env {
			set[name] = value
		}
	}
	if dir := strings.TrimSpace(spec.AccountConfigDir); dir != "" {
		set["CODEX_HOME"] = filepath.Clean(dir)
	}
	if spec.MCPSession != nil {
		set[EnvMCPSessionToken] = spec.MCPSession.Token
	}
	drop := []string{"CLAUDE", "CODEX_", "PI_", "ORCA_", "WS_PI_"}
	return harnessEnv(wsharnessenv.Drop(spec.Env), drop, map[string]bool{"CODEX_HOME": true}, set)
}

// Start initializes and opens a new process before returning. A holder
// reattach sends nothing: replay rebuilds the mapper, and the runner alone
// decides when to deliver input or reconcile a gap.
func (b CodexBackend) Start(ctx context.Context, spec StartSpec) (Process, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if spec.Ephemeral {
		spec.PermissionLevel = PermissionReadOnly
		spec.Lead = nil
		spec.Holder = nil
		spec.MCPSession = nil
		spec.audit = nil
	}
	if spec.Lead != nil && spec.Holder == nil {
		return nil, errors.New("codex backend: a hosted lead requires a session holder")
	}
	attach := spec.Holder != nil && spec.Holder.id != ""
	if !attach && !spec.Ephemeral && spec.MCPSession == nil {
		return nil, errors.New("codex backend: the daemon's HTTP MCP route is required; no session was started without ws tools")
	}
	mode, policy, err := codexStartMode(spec)
	if err != nil {
		return nil, fmt.Errorf("codex backend: %w", err)
	}
	command, pre, err := harnessCommand(b.Command, "codex")
	if err != nil {
		return nil, fmt.Errorf("codex backend: %w", err)
	}
	env := codexEnv(spec)
	args := append(pre, "app-server")
	var disableMCP []string
	if spec.Ephemeral {
		// Disable plugin MCP servers, codex_apps connectors, and every user
		// MCP server declared in $CODEX_HOME/config.toml before app-server
		// initialization so none connect or expose tools to ephemeral turns.
		disableMCP = codexUserMCPServerNames(env)
		args = append(args, "-c", "features.apps=false", "-c", "features.plugins=false")
		for _, name := range disableMCP {
			if isCodexBareTOMLKey(name) {
				args = append(args, "-c", "mcp_servers."+name+".enabled=false")
			}
		}
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = spec.workDir()
	cmd.Env = env
	mapper := newCodexMapper(spec.SessionID)
	mapper.codexHome = codexHomeFromEnv(env)
	mapper.readOnlyWorker = spec.Lead == nil && policy == "never"
	mapper.worker = spec.Lead == nil
	if attach {
		// The holder may have dropped a reply the earlier daemon already
		// wrote. Read its durable identity once at reattach; steady-state
		// send, mapping and recovery stay entirely in memory.
		page, err := wslive.ReadRingPage(wslive.RingDir(spec.Dir), "", wslive.RingHistoryCap)
		if err != nil {
			return nil, fmt.Errorf("codex backend: read persisted reply identities: %w", err)
		}
		for _, entry := range page.Records {
			if entry.Kind == wslive.RingReply && entry.Source != "" {
				mapper.persisted[entry.Source] = true
			}
		}
	}
	stream, err := startChild(spec, cmd, mapper, codexProtocol{})
	if err != nil {
		return nil, err
	}
	p := &codexProcess{
		streamProcess: stream, m: mapper, epoch: newMCPToken(),
		model: spec.Model, effort: spec.Effort, mode: mode, policy: policy, worker: spec.Lead == nil,
		ephemeral: spec.Ephemeral, ephemeralSpec: spec, ephemeralDisableMCP: disableMCP,
	}
	holder, pid, _ := stream.HolderInfo()
	if p.worker && cmd.Process != nil {
		// A direct worker's MCP hello names the child itself.
		pid = cmd.Process.Pid
	}
	if session := spec.MCPSession; session != nil && session.Started != nil {
		session.Started(cmd.Env, pid)
	}
	if attach {
		return p, nil
	}
	// MCP admission checks the hosted row's physical PID. Publish it before
	// thread/start connects, not after the handshake that waits on it.
	if spec.Lead != nil && spec.Lead.Spawned != nil {
		spec.Lead.Spawned(holder, pid)
	}
	if err := p.open(ctx, spec); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("codex backend: %w", err)
	}
	return p, nil
}

type codexProtocol struct{}

func (codexProtocol) userLine(string) any { return nil }

func (codexProtocol) requestLine(id string, request map[string]any) any {
	return map[string]any{"id": id, "method": request["method"], "params": request["params"]}
}

// Kind-bearing ids let a later daemon interpret responses in the holder
// ring. A random per-attach epoch prevents collisions with in-flight calls
// from the previous daemon. None of these ids is a persisted counter.
type codexProcess struct {
	*streamProcess
	m     *codexMapper
	epoch string
	seq   atomic.Uint64

	// The conversation loop owns these next-turn settings. mode is the
	// sandbox name and policy the approval policy sent with it.
	model  string
	effort string
	mode   string
	policy string
	// worker marks a spawned child, whose sandbox follows its authority.
	worker bool
	// ephemeral marks an EphemeralPool slot that can reset via warm thread/start.
	ephemeral           bool
	ephemeralSpec       StartSpec
	ephemeralDisableMCP []string
}

func (p *codexProcess) processControls() processControls {
	controls := processControls{Process: p, approval: p, mode: p, steer: p, commands: p, turns: p}
	if p.worker {
		controls.reports = p
	}
	if p.ephemeral {
		controls.reset = p
	}
	return controls
}

func (p *codexProcess) call(kind, method string, params map[string]any) (json.RawMessage, error) {
	return p.callContext(context.Background(), kind, method, params)
}

func (p *codexProcess) callContext(ctx context.Context, kind, method string, params map[string]any) (json.RawMessage, error) {
	id := "wsc/" + kind + "/" + p.epoch + "/" + strconv.FormatUint(p.seq.Add(1), 10)
	return p.requestBodyIDContext(ctx, id, method, map[string]any{"method": method, "params": params}, codexRequestTimeout)
}

func (p *codexProcess) threadParams(spec StartSpec) (map[string]any, error) {
	params := map[string]any{
		"cwd":    spec.workDir(),
		"config": codexThreadConfigWithDisabledMCP(spec, p.ephemeralDisableMCP),
	}
	if spec.Ephemeral {
		params["ephemeral"] = true
	}
	var devInstructions string
	if spec.Instructions != "" {
		text, err := os.ReadFile(spec.Instructions)
		if err != nil {
			return nil, fmt.Errorf("read instruction file: %w", err)
		}
		devInstructions = string(text)
	}
	if sp := strings.TrimSpace(spec.SystemPrompt); sp != "" {
		if strings.TrimSpace(devInstructions) != "" {
			devInstructions = strings.TrimRight(devInstructions, "\n") + "\n\n" + sp
		} else {
			devInstructions = sp
		}
	}
	if devInstructions != "" {
		params["developerInstructions"] = devInstructions
	}
	model := p.model
	if model == "" {
		model = spec.Model
	}
	if model != "" {
		params["model"] = model
	}
	if p.mode != "" {
		params["sandbox"], params["approvalPolicy"] = p.mode, p.policy
	}
	return params, nil
}

func (p *codexProcess) open(ctx context.Context, spec StartSpec) error {
	if ctx == nil {
		ctx = context.Background()
	}
	version := codexWorkerClientVersion
	if spec.Lead != nil {
		version = spec.Lead.DaemonVersion
	}
	_, err := p.callContext(ctx, "init", "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "ws", "version": version},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if err := p.writeLine(map[string]any{"method": "initialized"}); err != nil {
		return err
	}

	params, err := p.threadParams(spec)
	if err != nil {
		return err
	}
	method := "thread/start"
	if !spec.Ephemeral && spec.SessionID != "" {
		params["threadId"] = spec.SessionID
		// The open response only needs metadata, and history can exceed the stream's line limit.
		params["excludeTurns"] = true
		method = "thread/resume"
	}
	if _, err := p.callContext(ctx, "open", method, params); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if spec.Ephemeral {
		return nil
	}
	// A listing failure is not an admission failure: the harness default
	// still works. Expose no guessed model names or effort levels.
	if _, err := p.callContext(ctx, "models", "model/list", map[string]any{}); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p.deliver(Event{Notice: "codex model/list: " + err.Error()})
	}
	return nil
}

// Reset starts a fresh ephemeral thread on the resident codex app-server
// process, clearing conversation context without restarting the CLI, and
// best-effort unsubscribes the superseded thread so resident memory stays bounded.
func (p *codexProcess) Reset(ctx context.Context) error {
	if !p.ephemeral {
		return ErrUnsupported
	}
	prevThread := p.m.session()
	params, err := p.threadParams(p.ephemeralSpec)
	if err != nil {
		return err
	}
	if _, err := p.callContext(ctx, "open", "thread/start", params); err != nil {
		return fmt.Errorf("thread/start: %w", err)
	}
	if prevThread != "" && prevThread != p.m.session() {
		unsubCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, _ = p.callContext(unsubCtx, "unsub", "thread/unsubscribe", map[string]any{"threadId": prevThread})
		cancel()
	}
	return nil
}

// codexThreadConfig builds the thread/start and thread/resume config override.
// Both distributed plugin server names are shadowed, not the plugins: their
// skills and user-controlled hooks stay loaded. Only ws opens a client: one
// HTTP session per bearer means two clients would collide on request IDs.
// Disabled wsflow still needs a transport for Codex to accept it. For spawned
// workers (spec.Lead == nil), harmless ws tools in ChildSafeWSTools are
// pre-approved via mcp_servers.ws.tools.<name>.approval_mode = "approve" so
// read-only workers (approvalPolicy "never") can call them without widening
// mutating tools; leads stay unchanged. Under the exec cutover the exec and
// fs.* tools are pre-approved too, for leads and workers, and
// features.shell_tool is false.
func codexThreadConfig(spec StartSpec) map[string]any {
	return codexThreadConfigWithDisabledMCP(spec, nil)
}

func codexThreadConfigWithDisabledMCP(spec StartSpec, disableMCP []string) map[string]any {
	if spec.Ephemeral {
		// Ephemeral launches attach no ws-mcp and disable bundled plugin MCP
		// servers, codex_apps connectors, plugins, subagents, shell_tool, web
		// search, and any user-configured MCP servers read from CODEX_HOME/config.toml.
		mcpServers := map[string]any{
			"ws":     map[string]any{"url": "http://127.0.0.1:0/disabled", "enabled": false},
			"wsflow": map[string]any{"url": "http://127.0.0.1:0/disabled", "enabled": false},
		}
		for _, name := range disableMCP {
			if _, exists := mcpServers[name]; !exists {
				mcpServers[name] = map[string]any{"enabled": false}
			}
		}
		return map[string]any{
			"agents":     map[string]any{"enabled": false},
			"web_search": "disabled",
			"features": map[string]any{
				"shell_tool": false,
				"apps":       false,
				"plugins":    false,
			},
			"mcp_servers": mcpServers,
		}
	}
	server := map[string]any{
		"url": spec.MCPSession.URL, "bearer_token_env_var": EnvMCPSessionToken, "required": true,
	}
	tools := map[string]any{}
	if spec.Lead == nil {
		tools = codexChildSafeTools(spec)
	}
	if spec.ExecCutover {
		// The tools that replace the disabled shell run without a Codex-side
		// prompt or refusal at every approval policy, the read-only worker's
		// never included: ws decides an exec call's approval itself, and the
		// fs.* reads need none.
		for _, name := range cutoverToolsFor(spec.Profile, append(append([]string{}, ExecWSTools...), CodexReadWSTools...)) {
			tools[name] = map[string]any{"approval_mode": "approve"}
		}
	}
	if len(tools) > 0 {
		server["tools"] = tools
	}
	config := map[string]any{
		"agents": map[string]any{"enabled": false},
		"mcp_servers": map[string]any{
			"ws":     server,
			"wsflow": map[string]any{"url": spec.MCPSession.URL, "enabled": false},
		},
	}
	if spec.ExecCutover {
		// The shell tool is off on start and resume alike; a later
		// permission mode change keeps it off and reopens nothing.
		config["features"] = map[string]any{"shell_tool": false}
	}
	return config
}

func codexChildSafeTools(spec StartSpec) map[string]any {
	safe := childSafeWSToolsFor(spec.Profile)
	tools := make(map[string]any, len(safe))
	for _, name := range safe {
		tools[name] = map[string]any{"approval_mode": "approve"}
	}
	return tools
}

func codexMode(mode string) (sandbox, policy string, err error) {
	if !slices.Contains(CodexPermissionModes, mode) {
		return "", "", fmt.Errorf("codex has no permission mode %q", mode)
	}
	policy = "on-request"
	if mode == "danger-full-access" {
		policy = "never"
	}
	return mode, policy, nil
}

// codexWorkerClientVersion is the initialize clientInfo version of a worker
// start, which has no lead spec to read the daemon's version from.
const codexWorkerClientVersion = "worker"

func codexHomeFromEnv(env []string) string {
	var envCodexHome string
	for _, entry := range env {
		if k, v, ok := strings.Cut(entry, "="); ok && k == "CODEX_HOME" {
			envCodexHome = strings.TrimSpace(v)
		}
	}
	return wscred.ResolveHarnessHome(wscred.HarnessHomeOptions{
		Harness:       BackendCodex,
		SlotConfigDir: envCodexHome,
		Getenv:        func(string) string { return "" },
	})
}

// codexUserMCPServerNames reads $CODEX_HOME/config.toml (default ~/.codex/config.toml)
// once per ephemeral slot start and returns the sorted names of user-configured
// [mcp_servers.<name>] tables so ephemeral launches can disable them explicitly.
func codexUserMCPServerNames(env []string) []string {
	codexHome := codexHomeFromEnv(env)
	if codexHome == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		return nil
	}
	return parseCodexConfigMCPServers(string(raw))
}

func parseCodexConfigMCPServers(raw string) []string {
	seen := map[string]struct{}{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") || strings.HasPrefix(line, "[[") {
			continue
		}
		header, ok := extractTOMLTableHeader(line)
		if !ok {
			continue
		}
		rest, ok := strings.CutPrefix(header, "mcp_servers.")
		if !ok {
			continue
		}
		name := parseFirstTOMLKeySegment(strings.TrimSpace(rest))
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func extractTOMLTableHeader(line string) (string, bool) {
	inQuote := byte(0)
	escaped := false
	for i := 1; i < len(line); i++ {
		c := line[i]
		if inQuote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if inQuote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			continue
		}
		if c == ']' {
			return strings.TrimSpace(line[1:i]), true
		}
	}
	return "", false
}

func parseFirstTOMLKeySegment(s string) string {
	if s == "" {
		return ""
	}
	if s[0] == '"' || s[0] == '\'' {
		q := s[0]
		var b strings.Builder
		escaped := false
		for i := 1; i < len(s); i++ {
			c := s[i]
			if escaped {
				b.WriteByte(c)
				escaped = false
				continue
			}
			if q == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == q {
				return strings.TrimSpace(b.String())
			}
			b.WriteByte(c)
		}
		return ""
	}
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		s = s[:dot]
	}
	return strings.TrimSpace(s)
}

func isCodexBareTOMLKey(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		isDigit := c >= '0' && c <= '9'
		if !isLetter && !isDigit && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// codexWorkerMode maps a spawned child's authority to a sandbox and an
// approval policy. Workdir-write runs in the workspace-write sandbox with
// on-request approvals, which the runner's WorkerAutoApprove answers and
// traces: the confinement is advisory, not enforced. Full authority keeps
// those on-request approvals but uses Codex's danger-full-access sandbox, as
// full already means unrestricted authority. Read-only starts in Codex's
// native read-only sandbox with approval policy never, so Codex refuses an
// escalation itself and no request reaches the auto-allow; codexMode would
// give on-request and let the auto-allow run escalated commands outside the
// sandbox. A worker with no authority is refused rather than started with a
// guess.
func codexWorkerMode(level PermissionLevel) (sandbox, policy string, err error) {
	switch level {
	case PermissionReadOnly:
		return "read-only", "never", nil
	case PermissionWorkdirWrite:
		return "workspace-write", "on-request", nil
	case PermissionFull:
		return "danger-full-access", "on-request", nil
	}
	return "", "", fmt.Errorf("a worker needs an authority (read-only, workdir-write or full); got %q", level)
}

// codexStartMode is the sandbox and approval policy a start opens with: a
// lead's transient common selection or native permission mode, and a worker's
// authority. A lead with neither keeps Codex's own settings.
func codexStartMode(spec StartSpec) (sandbox, policy string, err error) {
	if spec.Lead == nil {
		return codexWorkerMode(spec.PermissionLevel)
	}
	if spec.PermissionLevel != "" {
		return codexMode(codexPermissionPolicy().nativeMode(spec.PermissionLevel))
	}
	if spec.Lead.PermissionMode == "" {
		return "", "", nil
	}
	return codexMode(spec.Lead.PermissionMode)
}

func codexInput(text string) []any {
	return []any{map[string]any{"type": "text", "text": text}}
}

func (p *codexProcess) Send(text string) error {
	session := p.m.session()
	if session == "" {
		return errors.New("the Codex thread is not open")
	}
	params := map[string]any{"threadId": session, "input": codexInput(text)}
	if p.model != "" {
		params["model"] = p.model
	}
	if p.effort != "" {
		params["effort"] = p.effort
	}
	if p.mode != "" {
		kind := map[string]string{
			"read-only": "readOnly", "workspace-write": "workspaceWrite", "danger-full-access": "dangerFullAccess",
		}[p.mode]
		params["sandboxPolicy"] = map[string]any{"type": kind}
		params["approvalPolicy"] = p.policy
	}
	_, err := p.call("turn", "turn/start", params)
	return err
}

func (p *codexProcess) Steer(text string) error {
	session, turn := p.m.active()
	if turn == "" {
		return ErrUnsupported
	}
	if !p.worker {
		_, err := p.call("control", "turn/steer", map[string]any{
			"threadId": session, "expectedTurnId": turn, "input": codexInput(text),
		})
		return err
	}
	id := "wsc/control/" + p.epoch + "/" + strconv.FormatUint(p.seq.Add(1), 10)
	return p.writeLine(map[string]any{"id": id, "method": "turn/steer", "params": map[string]any{
		"threadId": session, "expectedTurnId": turn, "input": codexInput(text),
	}})
}

func (p *codexProcess) steerCompaction(text string) (string, error) {
	session, turn := p.m.active()
	if turn == "" {
		return "", ErrUnsupported
	}
	id := "wsc/compactSteer/" + p.epoch + "/" + strconv.FormatUint(p.seq.Add(1), 10)
	return id, p.writeLine(codexProtocol{}.requestLine(id, map[string]any{"method": "turn/steer", "params": map[string]any{
		"threadId": session, "expectedTurnId": turn, "input": codexInput(text),
	}}))
}

// ClearSteered is a no-op for Codex: turn/steer is scoped by the expected
// active turn id, so an accepted message cannot leak into a later turn.
func (p *codexProcess) ClearSteered() error { return nil }

func (p *codexProcess) Interrupt() error {
	session, turn := p.m.active()
	if turn == "" {
		return nil
	}
	_, err := p.call("control", "turn/interrupt", map[string]any{"threadId": session, "turnId": turn})
	return err
}

func (p *codexProcess) SetModel(model string) error {
	if model == "" {
		return ErrUnsupported
	}
	p.model = model
	return nil
}

func (p *codexProcess) SetEffort(effort string) error {
	if effort == "" {
		return ErrUnsupported
	}
	p.effort = effort
	return nil
}

// A mode change applies at the next turn/start, like model and effort; it
// does not reinterpret an approval waiting in the current turn.
func (p *codexProcess) SetPermissionMode(mode string) error {
	if p.worker {
		return errors.New("a worker's sandbox follows its authority, set when it was spawned")
	}
	_, policy, err := codexMode(mode)
	if err != nil {
		return err
	}
	p.mode, p.policy = mode, policy
	return nil
}

func (p *codexProcess) AnswerApproval(request ApprovalRequest, allow bool, _ string) error {
	raw, result, err := p.m.approvalAnswer(request.ID, allow)
	if err != nil {
		return err
	}
	return p.writeLine(map[string]any{"id": raw, "result": result})
}

func (p *codexProcess) RunCommand(command wsinbox.Command) error {
	if command.Name != "compact" {
		return ErrUnsupported
	}
	_, err := p.call("compact", "thread/compact/start", map[string]any{"threadId": p.m.session()})
	return err
}

// TurnRunning reconciles a ring gap in stream order. The mapper emits the
// recovered final answer before TurnEnd. Returning true prevents the runner's
// separate probe callback from settling the turn ahead of those events.
func (p *codexProcess) TurnRunning() (bool, error) {
	_, err := p.call("recover", "thread/read", map[string]any{"threadId": p.m.session(), "includeTurns": true})
	return true, err
}
