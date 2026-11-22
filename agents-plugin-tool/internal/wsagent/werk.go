package wsagent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsharnessenv"
)

// WerkSubcommand is the ws binary's hidden subcommand that runs the loop
// (cmd/ws-mcp registers it); a session holder keeps that process.
const WerkSubcommand = "werk-run"

// BackendWerk is the builtin Go agent harness (ticket
// 261122-feat-ws-builtin-agent-harness): a worker backend that runs the model
// loop in a subcommand of the ws binary itself, under a session holder like
// every other agent.spawn worker, so it survives a daemon replacement.
//
// The daemon side is this file's backend and process, the line mapper
// (werk_mapper.go) and the audit adapter (audit_werk.go); the loop, the file
// tools and the Responses client are internal/werk. The two speak the line
// protocol of werk/protocol.go over the child's standard input and output.
//
//   - ws tools reach the loop over the daemon's HTTP MCP route with the
//     per-session bearer in the child's environment (EnvMCPSessionURL,
//     EnvMCPSessionToken), never its argv.
//   - The provider credential is never in the child's argument or environment:
//     each model request asks the daemon's provider token route
//     (wscred.RouteClient), which admits the session through
//     BackendRegistration.ProviderCredentials.
//   - There is no default model. A tier is usable only once the user sets its
//     model (agents.tier, harness werk), and the harness is unavailable, with
//     the reason, until an OpenRouter key is configured.
const BackendWerk = "werk"

// WerkBackend starts the builtin harness's process for one worker.
type WerkBackend struct {
	// Command is the ws binary that runs the loop; empty is this process's own
	// executable, which a daemon launches from its retained copy.
	Command string
	// credentialStatus reports whether provider has a credential configured,
	// from the daemon's credential store. nil means the daemon cannot tell,
	// which blocks nothing: the first request then fails with the auth
	// layer's own refusal.
	credentialStatus func(provider string) (configured bool, err error)
	// modelsURL and httpClient stand in for OpenRouter's model list endpoint
	// and its client; a test sets them, a daemon never does.
	modelsURL  string
	httpClient *http.Client
}

// werkWorkerText is the instruction text of the harness's worker profile.
//
//go:embed instructions/agent-worker-werk.md
var werkWorkerText string

func init() {
	builtinInstructionTexts[AgentWorkerProfile(BackendWerk)] = werkWorkerText
}

func (b WerkBackend) registration() (string, BackendRegistration) {
	return BackendWerk, BackendRegistration{
		Backend:       b,
		WorkerCapable: true,
		WorkerProfile: AgentWorkerProfile(BackendWerk),
		WorkerHTTPMCP: true,
		Catalog:       WerkCatalog(),
		// A provider's function names admit no dot, so the model sees ws's
		// tools spelled exec_shell, agent_spawn (werk.ModelToolName).
		ToolNameScheme:      ToolNameSchemeSanitize,
		ProviderCredentials: true,
		Lifecycle: LifecyclePolicy{
			// The common ws/compact lifecycle is Phase 2.
			NoWorkerCompactionSteer: true,
		},
		// The harness has no native process tools: shell goes through the
		// ws exec tools, so every worker runs under the exec cutover (which
		// lists them, holds the report for a live job, and protects the
		// worktree from release). There is no binary version to verify the
		// removal against: the removal is the absence of the tool.
		ExecCutover: &ExecCutoverSupport{},
		Dashboard:   werkDashboardTraits(),
		Profiles:    []BuiltinProfileRegistration{agentWorkerBuiltinProfile(BackendWerk, nil)},
	}
}

// withCredentialStatus is credentialAware: the daemon hands the backend the
// credential store's status function.
func (b WerkBackend) withCredentialStatus(status func(provider string) (bool, error)) Backend {
	b.credentialStatus = status
	return b
}

// executable is the ws binary the loop runs from.
func (b WerkBackend) executable() (string, error) {
	if b.Command != "" {
		return b.Command, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find the ws executable: %w", err)
	}
	return exe, nil
}

// Executable is the command availability is checked against: the ws binary.
func (b WerkBackend) Executable() string {
	exe, err := b.executable()
	if err != nil {
		return "ws-mcp"
	}
	return exe
}

// credentialAware is implemented by a backend whose readiness reads the
// provider credential store; the daemon wires the store in once, through
// WithCredentialStatus, without naming a backend.
type credentialAware interface {
	withCredentialStatus(status func(provider string) (bool, error)) Backend
}

// WithCredentialStatus gives every registered backend that reads provider
// credentials (credentialAware) the daemon's status function. It returns
// registrations as given when status is nil.
func WithCredentialStatus(registrations map[string]BackendRegistration, status func(provider string) (bool, error)) map[string]BackendRegistration {
	if status == nil {
		return registrations
	}
	for name, reg := range registrations {
		if aware, ok := reg.Backend.(credentialAware); ok {
			reg.Backend = aware.withCredentialStatus(status)
			registrations[name] = reg
		}
	}
	return registrations
}

// Reasons werk reports itself unavailable, in the words a user acts on.
const (
	werkNoKeyReason = "no OpenRouter API key is configured; set one with the CLI command \"credential set openrouter\" (or in the dashboard settings)"
	werkNoModelFmt  = "no tier model is configured for %s; set one with config.tune key \"agents.tier\" (harness %q, tier small, medium, large or xlarge, and an OpenRouter model ID from the dashboard model picker); no default model is shipped"
)

// workerCredentialGap is workerSetup: no worker can start without the
// OpenRouter credential.
func (b WerkBackend) workerCredentialGap() string {
	if b.credentialStatus == nil {
		return ""
	}
	configured, err := b.credentialStatus(string(werk.Provider))
	switch {
	case err != nil:
		return "the provider credential store could not be read: " + err.Error()
	case !configured:
		return werkNoKeyReason
	}
	return ""
}

// workerTierGap is workerSetup: tiers start empty, so a harness with no tier
// model has nothing to spawn.
func (b WerkBackend) workerTierGap(tiers map[string]wsconfig.AgentTier) string {
	if len(tiers) > 0 {
		return ""
	}
	return fmt.Sprintf(werkNoModelFmt, BackendWerk, BackendWerk)
}

// werkEnv is the child's environment: the daemon's without the parent
// harness sessions' variables, plus the session route and bearer.
func werkEnv(spec StartSpec) []string {
	set := map[string]string{}
	if spec.MCPSession != nil {
		set[werk.EnvSessionURL] = spec.MCPSession.URL
		set[werk.EnvSessionToken] = spec.MCPSession.Token
	}
	drop := []string{"CLAUDE", "CODEX_", "PI_", "ORCA_", "WS_PI_"}
	return harnessEnv(wsharnessenv.Drop(spec.Env), drop, nil, set)
}

// werkToolList is the file tools a launch offers: a read-only worker is
// offered the reading tools only (the tools refuse a write at every level
// regardless), and a profile's own Tools allowlist narrows the rest.
func werkToolList(spec StartSpec) []string {
	all := []string{"read", "edit", "write", "grep", "glob"}
	if spec.PermissionLevel == PermissionReadOnly {
		all = []string{"read", "grep", "glob"}
	}
	if len(spec.Profile.Tools) == 0 {
		return all
	}
	var out []string
	for _, name := range all {
		if slices.ContainsFunc(spec.Profile.Tools, func(t string) bool { return strings.EqualFold(strings.TrimSpace(t), name) }) {
			out = append(out, name)
		}
	}
	return out
}

// werkArgs is the child's command line after the ws binary.
func werkArgs(spec StartSpec) ([]string, error) {
	level := spec.PermissionLevel
	if !slices.Contains(LeadPermissionLevels(), level) {
		return nil, fmt.Errorf("a worker needs an authority (read-only, workdir-write or full); got %q", level)
	}
	if strings.TrimSpace(spec.Model) == "" {
		return nil, fmt.Errorf(werkNoModelFmt, BackendWerk, BackendWerk)
	}
	args := []string{WerkSubcommand,
		"--dir", spec.Dir, "--root", spec.Root, "--cwd", spec.workDir(),
		"--authority", string(level), "--model", spec.Model,
		"--tools", strings.Join(werkToolList(spec), ","),
	}
	if spec.Effort != "" {
		args = append(args, "--effort", spec.Effort)
	}
	if spec.SessionID != "" {
		args = append(args, "--session", spec.SessionID)
	}
	if spec.Instructions != "" {
		args = append(args, "--instructions", spec.Instructions)
	}
	return args, nil
}

// errWerkLead refuses a lead: the lead role is Phase 2.
var errWerkLead = errors.New("werk backend: the builtin harness has no lead role yet")

// Start launches the loop under a session holder, or attaches to the one a
// replaced daemon left running (spec.Holder.id set): an attach sends nothing,
// since the replay of the holder's ring rebuilds the mapper and the runner
// alone decides when to deliver input.
func (b WerkBackend) Start(_ context.Context, spec StartSpec) (Process, error) {
	if spec.Lead != nil {
		return nil, errWerkLead
	}
	if spec.Holder == nil {
		return nil, errors.New("werk backend: a worker runs under a session holder, so it survives a daemon replacement; none was given")
	}
	attach := spec.Holder.id != ""
	if !attach && spec.MCPSession == nil {
		return nil, errors.New("werk backend: the daemon's HTTP MCP route is required; no session was started without ws tools")
	}
	command, err := b.executable()
	if err != nil {
		return nil, fmt.Errorf("werk backend: %w", err)
	}
	var args []string
	if !attach {
		if args, err = werkArgs(spec); err != nil {
			return nil, fmt.Errorf("werk backend: %w", err)
		}
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = spec.workDir()
	cmd.Env = werkEnv(spec)
	mapper := newWerkMapper(spec.SessionID)
	stream, err := startChild(spec, cmd, mapper, werkProtocol{})
	if err != nil {
		return nil, err
	}
	p := &werkProcess{streamProcess: stream, m: mapper, model: spec.Model, effort: spec.Effort}
	if session := spec.MCPSession; session != nil && session.Started != nil {
		_, pid, _ := stream.HolderInfo()
		session.Started(cmd.Env, pid)
	}
	return p, nil
}
