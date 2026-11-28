package wsagent

import (
	"maps"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wspiruntime"
)

// The runner never branches on a harness name. What differs between harnesses
// is declared here: data on the backend's BackendRegistration (PermissionPolicy,
// LifecyclePolicy) and narrow optional interfaces its Backend implements
// (terminalSession, nativeModeAdmitter). The zero value of each declaration is
// the behavior of a backend that asks for nothing special, so a new backend
// registers its differences without editing common code.

// PermissionPolicy is how a backend's native permission modes relate to the
// common permission levels. It is runtime-only registration data: nothing here
// is written to a profile, record, or config.
type PermissionPolicy struct {
	// NativeModes maps a common lead level to the native mode a launch opens
	// in for it. Empty: the backend derives no native mode from a level, and
	// its native mode is never observed or reconciled against a selection.
	NativeModes map[PermissionLevel]string
	// LegacyModes maps a recorded native mode, the empty (omitted) mode
	// included, to the common level a session with no explicit selection
	// shows. A mode it does not name derives LegacyDefault.
	LegacyModes map[string]PermissionLevel
	// LegacyDefault is the level derived for a mode LegacyModes does not name;
	// empty derives none.
	LegacyDefault PermissionLevel
	// LiveModeProjection marks a backend whose launch may omit a mode and keep
	// the user's own native settings while the harness reports the mode it
	// runs in. While its process is live that report derives the display level
	// of a session with no selection, and it never becomes a persisted
	// next-launch mode.
	LiveModeProjection bool
}

func clonePermissionPolicy(p PermissionPolicy) PermissionPolicy {
	p.NativeModes = maps.Clone(p.NativeModes)
	p.LegacyModes = maps.Clone(p.LegacyModes)
	return p
}

// nativeMode is the native mode launched for level, "" when none maps.
func (p PermissionPolicy) nativeMode(level PermissionLevel) string {
	return p.NativeModes[level]
}

// legacyLevel is the common level a recorded native mode derives.
func (p PermissionPolicy) legacyLevel(mode string) PermissionLevel {
	if level, ok := p.LegacyModes[mode]; ok {
		return level
	}
	return p.LegacyDefault
}

// observesNativeMode reports whether the harness reports a native mode ws
// compares against the selection.
func (p PermissionPolicy) observesNativeMode() bool {
	return len(p.NativeModes) > 0
}

// quality is the enforcement evidence for level while the harness runs in the
// active native mode: partial only when the level's own native mode is the
// active read-only one, advisory otherwise.
func (p PermissionPolicy) quality(level PermissionLevel, active string) string {
	if level == PermissionReadOnly {
		if mode := p.NativeModes[PermissionReadOnly]; mode != "" && active == mode {
			return "partial"
		}
	}
	return "advisory"
}

// LifecyclePolicy is how a backend's hosted sessions start, run, and end where
// that differs between harnesses. Every field is runtime-only registration
// data; the zero value asks for none of it.
type LifecyclePolicy struct {
	// TerminalSummaries marks a backend whose hosted lead publishes reasoning
	// summaries from its terminal bridge: the runner starts and projects them,
	// and a terminal session's ws-mcp accepts the summaries its bridge pushes.
	TerminalSummaries bool
	// ComposedLeadInstructions marks a backend whose hosted lead receives the
	// instruction file the daemon composes on every launch (key and workflow
	// manual); another obtains them through its own extension or bridge.
	ComposedLeadInstructions bool
	// LeadHTTPMCP marks a backend whose hosted lead reaches ws-mcp over the
	// daemon's HTTP route with a token drawn at each launch.
	LeadHTTPMCP bool
	// LeadStdioFallback additionally gives that lead the stdio server as its
	// command when the HTTP route is unavailable. A LeadHTTPMCP backend
	// without it has no fallback, so a start whose token was never recorded
	// restarts at a known idle point after a daemon rejoin.
	LeadStdioFallback bool
	// RestoreUsage gives a reattached holder or durable loop's mapper the
	// usage and limits the live state last observed (StartSpec.restoredUsage).
	RestoreUsage bool
	// TurnActivity marks a backend whose settled hosted turn signals
	// Options.AgyActivity (a usage refresh), outside replay and compaction.
	TurnActivity bool
	// NoWorkerCompactionSteer marks a backend whose workers get no daemon
	// compaction guidance: it has no recoverable worker steer path, so only its
	// lead is steered.
	NoWorkerCompactionSteer bool
	// WorkerSessionState marks a backend whose agent worker reports its session
	// state and background tasks (StartSpec.SessionState), so a task it still
	// runs keeps it from idling.
	WorkerSessionState bool
	// ExecReadTools supplies the ws fs.* read tools to a cutover session.
	// A worker's profile must include them before its exec cutover.
	ExecReadTools bool
	// FileWriteTools admits daemon file mutations when the session supplies
	// a durable recorder for them.
	FileWriteTools bool
	// WebFetchTool admits the daemon's bounded ws/web.fetch tool for hosted
	// sessions whose harness has no native web fetch tool (werk).
	WebFetchTool bool
	// ReadOnlyNoExec denies exec launch and stdin writes to a bound read-only
	// lead with live authority, including calls made outside the cutover.
	// Immutable read-only workers use the daemon's read-only exec allowlist.
	ReadOnlyNoExec bool
	// ExecOptIn is the config key and label of an opt-in a backend's exec
	// cutover needs beyond the rollout switch: off unless the key says on, and
	// off when config cannot be read. Zero: the rollout switch alone decides.
	ExecOptIn ExecOptIn
	// InternalTurns marks a backend whose held worker may start a turn of its
	// own with no runner send (the harness's internal prompt), so the runner
	// stamps its request boundary and a replay adopts that turn.
	InternalTurns bool
	// WorkerLog marks a backend whose each hosted process writes its own worker
	// log the daemon locates afresh at every lead start.
	WorkerLog bool
	// LeadCompactionWSOnly marks a backend whose hosted leads always run
	// ws-managed compaction and have no native compaction fallback (werk).
	LeadCompactionWSOnly bool
	// WorkerCompactionWSOnly is the worker counterpart (werk).
	WorkerCompactionWSOnly bool
	// SingleHardCompactionSteer stops repeat ToolBoundary hard-compaction
	// steers once compactHardSteered is set, for backends whose steer injects
	// a full prompt turn instead of an in-turn steer RPC.
	SingleHardCompactionSteer bool
	// EphemeralNoScratchCwd marks an in-process backend that needs no scratch
	// cwd directory created on disk for EphemeralPool slots.
	EphemeralNoScratchCwd bool
	// EphemeralIsolatedStateDir marks a backend whose ephemeral process writes
	// native session state that must be redirected into a pool-owned temporary
	// directory (StartSpec.ephemeralStateDir) and deleted on eviction/close.
	EphemeralIsolatedStateDir bool
	// RestartLeadOnPermissionChange marks an external-process backend whose
	// lead permission enforcement switches via launch-time flags rather than
	// an in-stream native mode RPC: StartSpec.PermissionLevel is propagated
	// on every lead launch, and live lead permission changes restart the
	// process at the turn boundary.
	RestartLeadOnPermissionChange bool
}

// ExecOptIn names an exec-cutover opt-in: the config Key that must say on and
// the Label the refusal reason calls it.
type ExecOptIn struct {
	Key   string
	Label string
}

// ConnectionPolicy is how a backend's client reaches ws-mcp and how the
// daemon's MCP server and prompt renderer treat that connection where it
// differs between harnesses. Every field is runtime-only registration data;
// the zero value asks for none of it.
type ConnectionPolicy struct {
	// NamedClient marks a backend whose client announces the backend's own
	// name as its MCP clientInfo.name, so a connection with no daemon registry
	// (a terminal harness's stdio server) resolves its harness by that name.
	// A backend reached only through the daemon is resolved through its
	// registration instead (ManualHarness).
	NamedClient bool
	// BridgeClientName is the exact clientInfo.name of the backend's own
	// bridge extension, which identifies the backend ahead of any other
	// detection. Empty: no such bridge.
	BridgeClientName string
	// ClientDaemonAgents marks a backend whose client may opt in, by the
	// initialize experimental capability the bridge sets, to delegating
	// through the daemon's agent.spawn and mailbox.send.
	ClientDaemonAgents bool
	// DelegatesThroughDaemon reports whether rec, a conversation of this
	// backend the daemon launched, delegates through agent.spawn alone
	// (playbook terminology). launched resolves the conversation's launched
	// profile when rec holds no snapshot of it, nil when it cannot. Nil:
	// the backend's conversations never do.
	DelegatesThroughDaemon func(rec Record, launched func() *Profile) bool
}

// Narrow executable differences are optional interfaces on Backend, asserted
// on the registered instance. A backend that does not implement one takes the
// common default named at its use.

// terminalSession is implemented by a backend whose hosted lead a terminal can
// take over (handoff.go). Without it TakeOver and HandOff are refused.
// Note: explicit terminal handoff (terminalResume) is deprecated; new
// harnesses only need TakeOver support (checkTerminalSession,
// adoptTerminalSession).
type terminalSession interface {
	// terminalResume is the shell command that resumes rec's conversation in a
	// terminal, "" while it has none.
	//
	// Deprecated: Explicit terminal handoff is deprecated and scheduled for
	// removal in 261124-refactor-remove-terminal-handoff; new harnesses do not
	// need to implement terminal handoff.
	terminalResume(rec Record, sessionDir string) string
	// checkTerminalSession refuses a terminal session the daemon could not
	// adopt, before anything is written.
	checkTerminalSession(session string) error
	// adoptTerminalSession points rec at the terminal's session, carrying over
	// the settings the harness can keep and clearing the rest.
	adoptTerminalSession(rec *Record, session string)
}

// nativeModeAdmitter is implemented by a backend that reserves a native
// permission mode to the user (admitNativeMode); an error refuses the request.
type nativeModeAdmitter interface {
	admitNativeMode(rec Record, mode string, user bool) error
}

// workerSetup is implemented by a backend whose workers need setup beyond an
// installed executable: the daemon reports each gap as the harness's
// unavailability, with the reason, in agent.tiers and the dashboard. Neither
// method runs a process or reads a credential's value.
type workerSetup interface {
	// workerCredentialGap is why no worker can start for lack of a provider
	// credential; "" when one is configured, or none is needed.
	workerCredentialGap() string
	// workerProviderGap checks one tier's provider without refreshing tokens.
	workerProviderGap(provider string) string
	// workerTierGap is why no tier of the harness is usable given the tier
	// models the user configured (none: a harness that ships no default
	// model); "" when it is.
	workerTierGap(tiers map[string]wsconfig.AgentTier) string
}

// ephemeralStatePruner is implemented by a backend whose ephemeral sessions
// write native artifacts inside StartSpec.ephemeralStateDir that need bounded
// LRU pruning between resets while preserving keepSession.
type ephemeralStatePruner interface {
	pruneEphemeralStateDir(dir, keepSession string, maxKeep int)
}

// accountSlotContinuity is implemented by a backend that supports Path 2a
// cross-slot native session resume (pre-flight auth check, transcript copy
// without shared auth/db files, and post-resume turn-rejection detection for
// automatic 2a -> 2b rotation fallback). Backends that do not implement it
// take Path 2b directly on immediate account slot switches.
type accountSlotContinuity interface {
	slotAuthPreflight(configDir string) error
	migrateSlotSession(srcConfigDir, dstConfigDir, sessionID string) error
	isSlot2aTurnRejection(err error) bool
}

// slotHomeBinder is implemented by a backend whose Choices/cache lookup reads
// the active account slot's config directory from the runner's registry.
type slotHomeBinder interface {
	withSlotHomeResolver(resolve func() string) Backend
}

// piRuntimeBinder is implemented by a backend that consumes the daemon's
// shared managed Pi runtime manager.
type piRuntimeBinder interface {
	withPiRuntime(mgr *wspiruntime.Manager) Backend
}

func (r *Runner) slotContinuity(backend string) accountSlotContinuity {
	reg, ok := r.opts.Backends[backend]
	if !ok || reg.Backend == nil {
		return nil
	}
	sc, _ := reg.Backend.(accountSlotContinuity)
	return sc
}

func (r *Runner) permissionPolicy(backend string) PermissionPolicy {
	return r.opts.Backends[backend].Permission
}

// heldInternalWorker reports whether rec is a held worker of a backend whose
// held workers start internal turns (LifecyclePolicy.InternalTurns).
func (r *Runner) heldInternalWorker(rec Record) bool {
	return r.lifecycle(rec.Backend).InternalTurns && rec.Parent != nil && rec.heldWorker()
}

// internalPrompt reports whether rec is a worker of such a backend whose
// harness started a turn of its own that the runner stamped but has not opened.
func (r *Runner) internalPrompt(rec Record) bool {
	return r.lifecycle(rec.Backend).InternalTurns && rec.Parent != nil && !rec.TurnOpen && rec.TurnStartedAt != 0
}

func (r *Runner) lifecycle(backend string) LifecyclePolicy {
	return r.opts.Backends[backend].Lifecycle
}

// LeadCompactionWSOnly reports whether harness's backend descriptor forces
// ws-managed lead compaction (no native compaction mode).
func LeadCompactionWSOnly(harness string, registries ...map[string]BackendRegistration) bool {
	for _, regs := range registries {
		if reg, ok := regs[harness]; ok {
			return reg.Lifecycle.LeadCompactionWSOnly
		}
	}
	if reg, ok := BuiltinBackendRegistrations()[harness]; ok {
		return reg.Lifecycle.LeadCompactionWSOnly
	}
	return false
}

// WorkerCompactionWSOnly reports whether harness's backend descriptor forces
// ws-managed worker compaction (no native compaction mode).
func WorkerCompactionWSOnly(harness string, registries ...map[string]BackendRegistration) bool {
	for _, regs := range registries {
		if reg, ok := regs[harness]; ok {
			return reg.Lifecycle.WorkerCompactionWSOnly
		}
	}
	if reg, ok := BuiltinBackendRegistrations()[harness]; ok {
		return reg.Lifecycle.WorkerCompactionWSOnly
	}
	return false
}

// EffectiveWorkerCompactionMode normalizes a resolved compaction.worker_mode
// value against the descriptor's WorkerCompactionWSOnly capability.
func EffectiveWorkerCompactionMode(harness, raw string, registries ...map[string]BackendRegistration) string {
	if WorkerCompactionWSOnly(harness, registries...) {
		return wsconfig.CompactionModeWS
	}
	if raw == wsconfig.CompactionModeNative || raw == wsconfig.CompactionModeWS {
		return raw
	}
	return wsconfig.CompactionDefaults[wsconfig.ItemCompactionWorkerMode]
}

// EffectiveLeadCompactionMode normalizes a resolved compaction.lead_mode value
// against the harness descriptor's LeadCompactionWSOnly capability.
func EffectiveLeadCompactionMode(harness, raw string, registries ...map[string]BackendRegistration) string {
	if LeadCompactionWSOnly(harness, registries...) {
		return wsconfig.CompactionModeWS
	}
	if raw == wsconfig.CompactionModeNative {
		return wsconfig.CompactionModeNative
	}
	if raw == wsconfig.CompactionModeWS {
		return wsconfig.CompactionModeWS
	}
	return wsconfig.CompactionDefaults[wsconfig.ItemCompactionLeadMode]
}

// terminalSessionOf is the registered backend's terminal support, nil when it
// has none.
func (r *Runner) terminalSessionOf(backend string) terminalSession {
	ts, _ := r.opts.Backends[backend].Backend.(terminalSession)
	return ts
}

// adoptTerminalSession points rec at a terminal session under rec's own
// backend, which is the resolved profile's target and may differ from the
// harness the terminal ran (a profile override can re-route a lead). A target
// without terminalSession gets the generic adoption: its native mode cleared,
// the session id set.
func (r *Runner) adoptTerminalSession(rec *Record, session string) {
	if ts := r.terminalSessionOf(rec.Backend); ts != nil {
		ts.adoptTerminalSession(rec, session)
		return
	}
	rec.PermissionMode = ""
	rec.SessionID = session
}

// resumeCommand is the shell command that resumes a lead session's harness
// conversation in a terminal, or "" when its backend has none or the
// conversation has none yet.
//
// Deprecated: Explicit terminal handoff is deprecated and scheduled for
// removal in 261124-refactor-remove-terminal-handoff.
func (r *Runner) resumeCommand(rec Record, sessionDir string) string {
	if !rec.Lead {
		return ""
	}
	if ts := r.terminalSessionOf(rec.Backend); ts != nil {
		return ts.terminalResume(rec, sessionDir)
	}
	return ""
}

// projectLiveMode is the display level of a lead with no selection whose
// backend projects its reported native mode (PermissionPolicy.LiveModeProjection):
// live only, never persisted.
func (r *Runner) projectLiveMode(level PermissionLevel, rec Record, source, active string, process bool) PermissionLevel {
	p := r.permissionPolicy(rec.Backend)
	if level == "" && rec.Lead && p.LiveModeProjection && source == "legacy-derived" && process {
		return p.legacyLevel(active)
	}
	return level
}
