package wsagent

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wspiruntime"
)

const (
	// BackendPiRPC is the managed Pi standalone RPC backend.
	BackendPiRPC = "pi-rpc"
	// LeadProfilePiRPC names the builtin hosted Pi RPC lead profile.
	LeadProfilePiRPC = "lead-pi-rpc"
	// pirpcBridgeClientName is the exact MCP clientInfo.name advertised by the
	// embedded pirpc_bridge.mjs extension.
	pirpcBridgeClientName = "ws-pi-rpc-bridge"
)

//go:embed instructions/agent-worker-pi-rpc.md
var pirpcWorkerText string

func init() {
	builtinInstructionTexts[AgentWorkerProfile(BackendPiRPC)] = pirpcWorkerText
}

var (
	_ registrationBackend = PiRPCBackend{}
	_ ChoiceLister        = PiRPCBackend{}
	_ slotHomeBinder      = PiRPCBackend{}
	_ workerSetup         = PiRPCBackend{}
	_ piRuntimeBinder     = PiRPCBackend{}
)

// PiRPCBackend drives one sustained managed Pi `--mode rpc` process over stdio.
// Hosted leads and workers run under a holder, matching Claude, Codex, and Agy.
type PiRPCBackend struct {
	// Runtime is the shared managed Pi runtime manager. When nil, a fresh
	// manager is constructed on demand without caching it in
	// builtinRegistrations's sync.OnceValue.
	Runtime *wspiruntime.Manager
	// Command overrides the executable path in tests.
	Command string
	// ResolveHome returns the active slot's resolved PI_CODING_AGENT_DIR for
	// model/thinking-level cache lookups. Nil falls back to the D10 resolver.
	ResolveHome func() string
}

// WithPiRuntime binds a shared managed Pi runtime manager to registered
// backends that accept one (piRuntimeBinder).
func WithPiRuntime(registrations map[string]BackendRegistration, mgr *wspiruntime.Manager) map[string]BackendRegistration {
	if mgr == nil {
		return registrations
	}
	for name, reg := range registrations {
		if binder, ok := reg.Backend.(piRuntimeBinder); ok {
			reg.Backend = binder.withPiRuntime(mgr)
			registrations[name] = reg
		}
	}
	return registrations
}

func (b PiRPCBackend) withPiRuntime(mgr *wspiruntime.Manager) Backend {
	if mgr == nil {
		return b
	}
	b.Runtime = mgr
	return b
}

func (b PiRPCBackend) withSlotHomeResolver(resolve func() string) Backend {
	if b.ResolveHome != nil || resolve == nil {
		return b
	}
	b.ResolveHome = resolve
	return b
}

func (b PiRPCBackend) runtimeManager() *wspiruntime.Manager {
	if b.Runtime != nil {
		return b.Runtime
	}
	return wspiruntime.NewManager(wspiruntime.Options{})
}

func (b PiRPCBackend) registration() (string, BackendRegistration) {
	return BackendPiRPC, BackendRegistration{
		Backend:           b,
		ProcessKind:       ProcessExternal,
		LeadProfile:       LeadProfilePiRPC,
		PermissionLevels:  LeadPermissionLevels(),
		PermissionModes:   nil,
		Permission:        PermissionPolicy{LegacyDefault: PermissionWorkdirWrite},
		Catalog:           PiRPCCatalog(),
		WorkerCapable:     true,
		WorkerProfile:     AgentWorkerProfile(BackendPiRPC),
		WorkerHTTPMCP:     true,
		WorkerAutoApprove: true,
		ManualHarness:     BackendPiRPC,
		PlaybookCommands:  true,
		ToolNameScheme:    ToolNameSchemeSanitize,
		Lifecycle: LifecyclePolicy{
			ComposedLeadInstructions:      true,
			LeadHTTPMCP:                   true,
			ExecReadTools:                 true,
			RestartLeadOnPermissionChange: true,
		},
		ExecCutover: &ExecCutoverSupport{},
		Connection:  ConnectionPolicy{BridgeClientName: pirpcBridgeClientName},
		Dashboard:   wsdashboard.HarnessTraits{ReplayClientName: pirpcBridgeClientName},
		Profiles: []BuiltinProfileRegistration{
			agentWorkerBuiltinProfile(BackendPiRPC, nil),
			leadBuiltinProfile(LeadProfilePiRPC, BackendPiRPC, ""),
		},
	}
}

// PiRPCCatalog is the capability profile of the managed Pi RPC backend at its
// pinned runtime version.
func PiRPCCatalog() *HarnessProfile {
	p := &HarnessProfile{
		Compaction: CompactionProtocol{
			LeadCommand:    true,
			WorkerCommand:  true,
			Lifecycle:      true,
			PromptPersists: true,
		},
		Oldest: wspiruntime.PinnedVersion,
		Newest: wspiruntime.PinnedVersion,
		Entries: []CapabilityEntry{
			{Item: ItemSkillList, Role: RoleLead, Grade: GradeUnsupported,
				Source: "native skills are disabled with -ns; ws playbooks are offered through PlaybookCommands"},
			{Item: ItemSkillInjection, Role: RoleLead, Grade: GradeUnsupported,
				Source: "native skills are disabled with -ns; ws playbooks expand through PlaybookCommands"},
			{Item: ItemCompaction, Role: RoleLead, Grade: GradeNative,
				Source: "RPC compact command with compaction_start/compaction_end lifecycle events"},
			{Item: ItemRewind, Role: RoleLead, Grade: GradeUnsupported, Source: "no ws wiring"},
			{Item: ItemToolRemoval, Role: RoleLead, Grade: GradeNative, Values: []string{"bash"},
				Source: "--exclude-tools bash (workspace-write/full) or -nbt (read-only) under exec cutover"},
			{Item: ItemSubagentRemoval, Role: RoleLead, Grade: GradeNative,
				Source: "Pi launches with -ne and has no native subagent tool; delegation uses ws agent.spawn"},
			{Item: ItemModelEffort, Role: RoleLead, Grade: GradeNative,
				Source: "set_model and set_thinking_level RPCs; choices from get_available_models and get_available_thinking_levels"},
			{Item: ItemApproval, Role: RoleLead, Grade: GradeUnsupported,
				Source: "Pi RPC exposes no native tool approval prompt; lead permission switches restart the process"},
			{Item: ItemSteer, Role: RoleLead, Grade: GradeNative,
				Source: "steer RPC delivers mid-turn guidance into the active turn"},
			{Item: ItemInterrupt, Role: RoleLead, Grade: GradeNative,
				Source: "abort RPC cancels the running turn and settles via agent_settled"},
			{Item: ItemFollowUp, Role: RoleLead, Grade: GradeNative,
				Source: "follow_up RPC queues input for the next turn"},
			{Item: ItemResume, Role: RoleLead, Grade: GradeNative,
				Source: "--session-dir with --session resumes the recorded Pi session"},
			{Item: ItemReasoningStream, Role: RoleLead, Grade: GradeUnsupported,
				Source: "thinking deltas are suppressed; no reasoning summary projection yet"},
			{Item: ItemUsageQuery, Role: RoleLead, Grade: GradeNative,
				Source: "get_session_stats and assistant message_end usage report context tokens and cost"},
		},
	}
	for _, entry := range slices.Clone(p.Entries) {
		entry.Role = RoleWorker
		if entry.Item == ItemSteer {
			entry.Source = "steer and clear_queue RPCs deliver and reconcile worker reports"
		}
		p.Entries = append(p.Entries, entry)
	}
	return p
}

// Executable returns the test command override when set, otherwise the managed
// Pi binary path.
func (b PiRPCBackend) Executable() string {
	if b.Command != "" {
		return b.Command
	}
	return b.runtimeManager().BinaryPath()
}

func (b PiRPCBackend) workerCredentialGap() string { return "" }

func (b PiRPCBackend) workerProviderGap(string) string { return "" }

func (b PiRPCBackend) workerTierGap(tiers map[string]wsconfig.AgentTier) string {
	if len(tiers) > 0 {
		return ""
	}
	return fmt.Sprintf(werkNoModelFmt, BackendPiRPC, BackendPiRPC)
}

type pirpcChoiceEntry struct {
	models  []ModelChoice
	efforts []string
	at      time.Time
}

// pirpcChoices holds models and thinking levels cached per resolved
// PI_CODING_AGENT_DIR from the last opened session or on-demand RPC probe.
var pirpcChoices struct {
	sync.Mutex
	byHome map[string]pirpcChoiceEntry
}

func normalizePiRPCHomeKey(home string) string {
	home = strings.TrimSpace(home)
	if home == "" {
		home = wscred.ResolveHarnessHome(wscred.HarnessHomeOptions{Harness: BackendPiRPC})
	}
	if home == "" {
		return ""
	}
	return filepath.Clean(home)
}

func (b PiRPCBackend) resolvedPiHome() string {
	if b.ResolveHome != nil {
		if home := strings.TrimSpace(b.ResolveHome()); home != "" {
			return filepath.Clean(home)
		}
	}
	return normalizePiRPCHomeKey("")
}

func recordPiRPCChoices(home string, models []ModelChoice, efforts []string) {
	if len(models) == 0 && len(efforts) == 0 {
		return
	}
	key := normalizePiRPCHomeKey(home)
	pirpcChoices.Lock()
	defer pirpcChoices.Unlock()
	if pirpcChoices.byHome == nil {
		pirpcChoices.byHome = map[string]pirpcChoiceEntry{}
	}
	entry := pirpcChoices.byHome[key]
	if len(models) > 0 {
		entry.models = slices.Clone(models)
	}
	if len(efforts) > 0 {
		entry.efforts = slices.Clone(efforts)
	}
	entry.at = time.Now()
	pirpcChoices.byHome[key] = entry
}

func cachedPiRPCChoices(home string) ([]ModelChoice, []string, bool) {
	key := normalizePiRPCHomeKey(home)
	pirpcChoices.Lock()
	defer pirpcChoices.Unlock()
	entry, ok := pirpcChoices.byHome[key]
	if !ok || time.Since(entry.at) >= choicesTTL {
		return nil, nil, false
	}
	if len(entry.models) == 0 {
		return nil, nil, false
	}
	return slices.Clone(entry.models), slices.Clone(entry.efforts), true
}

// ClearPiRPCChoices clears cached Pi RPC model and thinking-level choices.
// An empty home clears every cached home; a non-empty home clears only that
// normalized PI_CODING_AGENT_DIR entry.
func ClearPiRPCChoices(home string) {
	pirpcChoices.Lock()
	defer pirpcChoices.Unlock()
	if strings.TrimSpace(home) == "" {
		clear(pirpcChoices.byHome)
		return
	}
	delete(pirpcChoices.byHome, normalizePiRPCHomeKey(home))
}

var pirpcProbeTimeout = 10 * time.Second

// Choices returns cached model and thinking-level choices for the active
// PI_CODING_AGENT_DIR, falling back to a short bounded on-demand RPC probe
// when the binary is installed and the cache is cold.
func (b PiRPCBackend) Choices(ctx context.Context) ([]ModelChoice, []string, error) {
	home := b.resolvedPiHome()
	if models, efforts, ok := cachedPiRPCChoices(home); ok {
		return models, efforts, nil
	}
	var binPath string
	if b.Command != "" {
		cmdPath, _, err := harnessCommand(b.Command, "pi")
		if err != nil {
			return nil, nil, err
		}
		binPath = cmdPath
	} else {
		st := b.runtimeManager().Status()
		if !st.Installed || st.BinaryPath == "" {
			return nil, nil, errors.New("managed Pi runtime is not installed")
		}
		binPath = st.BinaryPath
	}
	models, efforts, err := probePiRPCChoices(ctx, binPath, home)
	if err != nil {
		return nil, efforts, err
	}
	recordPiRPCChoices(home, models, efforts)
	return models, efforts, nil
}

func probePiRPCChoices(ctx context.Context, binPath, home string) ([]ModelChoice, []string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, pirpcProbeTimeout)
	defer cancel()

	args := []string{"--mode", "rpc", "--no-session", "-ne", "-ns", "-np", "--no-themes", "-na", "-nc"}
	cmd := exec.CommandContext(probeCtx, binPath, args...)
	cmd.Env = pirpcEnv(StartSpec{Env: environ(), AccountConfigDir: home})

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	reqs := []string{
		`{"id":"probe-models","type":"get_available_models"}` + "\n",
		`{"id":"probe-thinking","type":"get_available_thinking_levels"}` + "\n",
	}
	for _, line := range reqs {
		if _, err := stdin.Write([]byte(line)); err != nil {
			return nil, nil, err
		}
	}
	_ = stdin.Close()

	var models []ModelChoice
	var efforts []string
	var modelsErr string
	gotModels, gotEfforts := false, false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var resp pirpcResponseEnvelope
		if json.Unmarshal(sc.Bytes(), &resp) != nil || resp.Type != "response" {
			continue
		}
		switch resp.ID {
		case "probe-models":
			gotModels = true
			if resp.Success {
				models = parsePiRPCModels(resp.Data)
			} else {
				modelsErr = strings.TrimSpace(resp.Error)
				if modelsErr == "" {
					modelsErr = "pi rpc get_available_models failed"
				}
			}
		case "probe-thinking":
			gotEfforts = true
			if resp.Success {
				efforts = parsePiRPCThinkingLevels(resp.Data)
			}
		}
		if gotModels && gotEfforts {
			break
		}
	}
	if err := sc.Err(); err != nil && (!gotModels || !gotEfforts) {
		return nil, efforts, err
	}
	if err := probeCtx.Err(); err != nil && (!gotModels || !gotEfforts) {
		return nil, efforts, err
	}
	if modelsErr != "" {
		return nil, efforts, errors.New(modelsErr)
	}
	if !gotModels {
		return nil, efforts, errors.New("pi rpc get_available_models failed")
	}
	return models, efforts, nil
}
