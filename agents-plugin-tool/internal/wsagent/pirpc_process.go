package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wsharnessenv"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wspiruntime"
)

// pirpcRequestTimeout bounds Pi RPC control responses.
var pirpcRequestTimeout = 30 * time.Second

// pirpcEnv scrubs parent harness identities, disables telemetry and version
// checks, binds PI_CODING_AGENT_DIR to the active account slot when supplied,
// and injects the daemon's HTTP MCP URL and bearer token strictly via env
// (never argv or disk). PI_OFFLINE is intentionally never set.
func pirpcEnv(spec StartSpec) []string {
	set := map[string]string{
		"PI_SKIP_VERSION_CHECK": "1",
		"PI_TELEMETRY":          "0",
	}
	if spec.Lead != nil {
		for name, value := range spec.Lead.Env {
			set[name] = value
		}
	}
	keep := map[string]bool{}
	if dir := strings.TrimSpace(spec.AccountConfigDir); dir != "" {
		set["PI_CODING_AGENT_DIR"] = filepath.Clean(dir)
	} else {
		keep["PI_CODING_AGENT_DIR"] = true
	}
	if spec.MCPSession != nil {
		if spec.MCPSession.URL != "" {
			set[EnvMCPSessionURL] = spec.MCPSession.URL
		}
		if spec.MCPSession.Token != "" {
			set[EnvMCPSessionToken] = spec.MCPSession.Token
		}
	}
	drop := []string{"CLAUDE", "CODEX_", "PI_", "ORCA_", "WS_PI_"}
	return harnessEnv(wsharnessenv.Drop(spec.Env), drop, keep, set)
}

func pirpcHomeFromEnv(env []string) string {
	for _, kv := range env {
		if val, ok := strings.CutPrefix(kv, "PI_CODING_AGENT_DIR="); ok && strings.TrimSpace(val) != "" {
			return filepath.Clean(strings.TrimSpace(val))
		}
	}
	return normalizePiRPCHomeKey("")
}

func pirpcArgs(spec StartSpec, bridgePath string) []string {
	args := []string{"--mode", "rpc", "-ne"}
	if !spec.Ephemeral && bridgePath != "" {
		args = append(args, "-e", "builtin:mcp", "-e", bridgePath)
	}
	args = append(args, "-ns", "-np", "--no-themes", "-na", "-nc")
	if spec.ExecCutover {
		if spec.PermissionLevel == PermissionReadOnly {
			args = append(args, "-nbt")
		} else {
			args = append(args, "--exclude-tools", "bash")
		}
	}
	if model := strings.TrimSpace(spec.Model); model != "" {
		args = append(args, "--model", model)
	}
	if effort := strings.TrimSpace(spec.Effort); effort != "" {
		args = append(args, "--thinking", effort)
	}
	if spec.Instructions != "" {
		args = append(args, "--append-system-prompt", spec.Instructions)
	}
	if sp := strings.TrimSpace(spec.SystemPrompt); sp != "" {
		args = append(args, "--append-system-prompt", sp)
	}
	if spec.Ephemeral {
		args = append(args, "--no-session")
	} else {
		if spec.Dir != "" {
			args = append(args, "--session-dir", filepath.Join(spec.Dir, "pi-sessions"))
		}
		if spec.SessionFile != "" {
			args = append(args, "--session", spec.SessionFile)
		} else if spec.SessionID != "" {
			args = append(args, "--session", spec.SessionID)
		}
	}
	return args
}

// Start launches a managed Pi RPC process or reattaches to an existing holder.
// When b.Command is unset and the managed runtime is missing, Start fails
// closed without triggering any automatic download.
func (b PiRPCBackend) Start(ctx context.Context, spec StartSpec) (Process, error) {
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
		return nil, errors.New("pi-rpc backend: a hosted lead requires a session holder")
	}
	attach := spec.Holder != nil && spec.Holder.id != ""
	if !attach && !spec.Ephemeral && spec.MCPSession == nil {
		return nil, errors.New("pi-rpc backend: the daemon's HTTP MCP route is required; no session was started without ws tools")
	}

	mgr := b.runtimeManager()
	var (
		command string
		pre     []string
		err     error
	)
	if b.Command != "" {
		command, pre, err = harnessCommand(b.Command, "pi")
		if err != nil {
			return nil, fmt.Errorf("pi-rpc backend: %w", err)
		}
	} else {
		st := mgr.Status()
		if !st.Installed || st.BinaryPath == "" {
			return nil, errors.New("pi-rpc backend: managed Pi runtime is not installed")
		}
		if _, statErr := os.Stat(st.BinaryPath); statErr != nil {
			return nil, fmt.Errorf("pi-rpc backend: managed Pi runtime is not installed: %w", statErr)
		}
		command = st.BinaryPath
	}

	var bridgePath string
	if !spec.Ephemeral {
		bridgePath, err = mgr.BridgePath()
		if err != nil {
			return nil, fmt.Errorf("pi-rpc backend: extract bridge extension: %w", err)
		}
	}

	if spec.AccountConfigDir == "" && b.ResolveHome != nil {
		if home := strings.TrimSpace(b.ResolveHome()); home != "" {
			spec.AccountConfigDir = home
		}
	}
	env := pirpcEnv(spec)
	piHome := pirpcHomeFromEnv(env)
	args := append(pre, pirpcArgs(spec, bridgePath)...)
	cmd := exec.Command(command, args...)
	cmd.Dir = spec.workDir()
	cmd.Env = env

	mapper := newPiRPCMapper(spec.SessionID, piHome, spec.Lead == nil)
	stream, err := startChild(spec, cmd, mapper, pirpcProtocol{})
	if err != nil {
		return nil, err
	}
	p := &pirpcProcess{
		streamProcess: stream,
		m:             mapper,
		epoch:         newMCPToken(),
		worker:        spec.Lead == nil,
		piHome:        piHome,
	}

	holder, pid, _ := stream.HolderInfo()
	if pid == 0 && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	if session := spec.MCPSession; session != nil && session.Started != nil {
		session.Started(cmd.Env, pid)
	}
	if attach {
		return p, nil
	}
	if spec.Lead != nil && spec.Lead.Spawned != nil {
		spec.Lead.Spawned(holder, pid)
	}
	if err := p.open(ctx, spec); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("pi-rpc backend: %w", err)
	}
	return p, nil
}

type pirpcProtocol struct{}

func (pirpcProtocol) userLine(text string) any {
	return map[string]any{"type": "prompt", "message": text}
}

func (pirpcProtocol) requestLine(id string, request map[string]any) any {
	out := make(map[string]any, len(request)+1)
	for k, v := range request {
		out[k] = v
	}
	out["id"] = id
	return out
}

type pirpcProcess struct {
	*streamProcess
	m      *pirpcMapper
	epoch  string
	seq    atomic.Uint64
	worker bool
	piHome string
}

func (p *pirpcProcess) processControls() processControls {
	controls := processControls{
		Process:  p,
		steer:    p,
		commands: p,
		turns:    p,
		// mode is intentionally nil: lead permission switches restart the
		// process via LifecyclePolicy.RestartLeadOnPermissionChange.
	}
	if p.worker {
		controls.reports = p
	}
	return controls
}

func (p *pirpcProcess) nextKindID(kind string) string {
	return "wspi/" + kind + "/" + p.epoch + "/" + strconv.FormatUint(p.seq.Add(1), 10)
}

func (p *pirpcProcess) call(kind string, req map[string]any) (json.RawMessage, error) {
	return p.callContext(context.Background(), kind, req)
}

func (p *pirpcProcess) callContext(ctx context.Context, kind string, req map[string]any) (json.RawMessage, error) {
	id := p.nextKindID(kind)
	name, _ := req["type"].(string)
	if name == "" {
		name = kind
	}
	return p.requestBodyIDContext(ctx, id, name, req, pirpcRequestTimeout)
}

func (p *pirpcProcess) open(ctx context.Context, spec StartSpec) error {
	unknown := -1
	p.deliver(Event{Harness: &HarnessReport{Version: wspiruntime.PinnedVersion}})
	p.deliver(Event{BackgroundTasks: &unknown})
	p.deliver(Event{Live: &wslive.Event{
		Kind: wslive.KindCommands,
		Commands: []wslive.SlashCommand{
			{Name: "compact", Description: "Compact the conversation"},
		},
	}})

	if _, err := p.callContext(ctx, "getState", map[string]any{"type": "get_state"}); err != nil {
		return fmt.Errorf("get_state: %w", err)
	}
	if !spec.Ephemeral {
		// Best-effort warm of model and thinking-level choices for this slot.
		_, _ = p.callContext(ctx, "models", map[string]any{"type": "get_available_models"})
		_, _ = p.callContext(ctx, "thinking", map[string]any{"type": "get_available_thinking_levels"})
	}
	return nil
}

// Send submits a user turn via `prompt`, or `follow_up` when a turn is already
// active on the Pi RPC stream.
func (p *pirpcProcess) Send(text string) error {
	cmdType := "prompt"
	if p.m.running() {
		cmdType = "follow_up"
	}
	_, err := p.call(cmdType, map[string]any{"type": cmdType, "message": text})
	return err
}

func (p *pirpcProcess) Steer(text string) error {
	if !p.worker {
		_, err := p.call("steer", map[string]any{"type": "steer", "message": text})
		return err
	}
	id := p.nextKindID("workerSteer")
	return p.writeLine(map[string]any{"id": id, "type": "steer", "message": text})
}

func (p *pirpcProcess) ClearSteered() error {
	_, err := p.call("clearQueue", map[string]any{"type": "clear_queue"})
	return err
}

func (p *pirpcProcess) Interrupt() error {
	_, err := p.call("abort", map[string]any{"type": "abort"})
	return err
}

// SetModel parses `<provider>/<modelId>` on the first `/` via strings.Cut so
// nested provider model IDs (e.g. `openrouter/anthropic/claude-3.7-sonnet`)
// pass provider and modelId losslessly to Pi's `set_model` RPC.
func (p *pirpcProcess) SetModel(model string) error {
	provider, modelID, ok := parsePiRPCModelSpec(model)
	if !ok {
		return ErrUnsupported
	}
	_, err := p.call("setModel", map[string]any{
		"type":     "set_model",
		"provider": provider,
		"modelId":  modelID,
	})
	return err
}

func parsePiRPCModelSpec(model string) (provider, modelID string, ok bool) {
	model = strings.TrimSpace(model)
	provider, modelID, found := strings.Cut(model, "/")
	if !found || strings.TrimSpace(provider) == "" || strings.TrimSpace(modelID) == "" {
		return "", "", false
	}
	return strings.TrimSpace(provider), strings.TrimSpace(modelID), true
}

func (p *pirpcProcess) SetEffort(effort string) error {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return ErrUnsupported
	}
	_, err := p.call("setThinking", map[string]any{
		"type":  "set_thinking_level",
		"level": effort,
	})
	return err
}

func (p *pirpcProcess) RunCommand(cmd wsinbox.Command) error {
	if cmd.Name != "compact" {
		return ErrUnsupported
	}
	req := map[string]any{"type": "compact"}
	if args := strings.TrimSpace(cmd.Args); args != "" {
		req["customInstructions"] = args
	}
	_, err := p.call("compact", req)
	return err
}

func (p *pirpcProcess) TurnRunning() (bool, error) {
	raw, err := p.call("getState", map[string]any{"type": "get_state"})
	if err != nil {
		return false, err
	}
	var st pirpcStateData
	if err := json.Unmarshal(raw, &st); err != nil {
		return false, fmt.Errorf("parse get_state: %w", err)
	}
	return st.IsStreaming || st.IsCompacting, nil
}

// SessionStats queries Pi's `get_session_stats` RPC directly.
func (p *pirpcProcess) SessionStats(ctx context.Context) (json.RawMessage, error) {
	return p.callContext(ctx, "stats", map[string]any{"type": "get_session_stats"})
}

// AvailableModels queries Pi's `get_available_models` RPC directly.
func (p *pirpcProcess) AvailableModels(ctx context.Context) ([]ModelChoice, error) {
	raw, err := p.callContext(ctx, "models", map[string]any{"type": "get_available_models"})
	if err != nil {
		return nil, err
	}
	return parsePiRPCModels(raw), nil
}

// AvailableThinkingLevels queries Pi's `get_available_thinking_levels` RPC directly.
func (p *pirpcProcess) AvailableThinkingLevels(ctx context.Context) ([]string, error) {
	raw, err := p.callContext(ctx, "thinking", map[string]any{"type": "get_available_thinking_levels"})
	if err != nil {
		return nil, err
	}
	return parsePiRPCThinkingLevels(raw), nil
}
