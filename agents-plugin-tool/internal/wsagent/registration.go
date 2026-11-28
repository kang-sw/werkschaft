package wsagent

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// ProcessKind selects the process lifecycle for hosted sessions.
type ProcessKind uint8

const (
	// ProcessExternal is the existing executable lifecycle.
	ProcessExternal ProcessKind = iota
	// ProcessDurable is a daemon-hosted loop with a private runner record.
	ProcessDurable
)

// BackendRegistration is the admission descriptor of one registered backend.
// Optional process controls remain instance-owned (controlsFor), not promises
// made by a backend-wide flag. ChoiceLister is asserted on Backend itself.
// This descriptor is runtime-only: it adds nothing to persisted profiles.
type BackendRegistration struct {
	Backend         Backend
	ProcessKind     ProcessKind
	LeadProfile     string
	PermissionModes []string
	// WorkerCapable admits agent.spawn on this backend. WorkerProfile is
	// the internal profile it launches; both must be set to expose tiers.
	WorkerCapable bool
	WorkerProfile string
	// crossHarnessRefusal, when set, admits a worker on this backend only
	// for a caller verified to run on this same backend (a hosted lead's
	// record, or the token holder spawning a nested child), on every worker
	// entry, not agent.spawn alone: a profile create, a wake, and a rejoin
	// are refused too for any other caller or none (admitWorker), and it is
	// the refusal's text. Existing worker records stay listable. Empty
	// admits every caller.
	crossHarnessRefusal string
	// WorkerHTTPMCP attaches a worker's ws tools through the daemon's
	// HTTP route rather than a stdio launcher. Workers never persist it.
	WorkerHTTPMCP bool
	// WorkerAutoApprove answers worker permission requests through the
	// ordinary approval path, retaining their trace. Leads still ask.
	WorkerAutoApprove bool
	// PermissionLevels advertises supported lead selection paths only.
	PermissionLevels []PermissionLevel
	// Catalog is the backend's capability catalog (catalog.go): a ceiling
	// over the instance handles, never a replacement for them. Nil keeps
	// the backend handle-only: no hiding, demotion, or version warning.
	Catalog *HarnessProfile
	// NativeCompaction supplies the compaction descriptor for a backend
	// not yet ported to the full catalog.
	NativeCompaction *CompactionProtocol
	// ToolNameScheme names how this backend's host must spell ws's dotted
	// tool names on a connection of its hosted lead or worker (ToolNameSchemeSanitize
	// below); "" keeps the dotted names. Common code reads it through
	// ApplyToolNameScheme and never names a backend.
	ToolNameScheme string
	// CallFallback exposes call and a tool index on hosted lead connections
	// whose client may hide tools: agy drops definitions over 1,500 bytes
	// including its 39-byte description prefix; other clients may cap count.
	CallFallback bool
	// DirectTools limits hosted lead discovery only; nil lists all tools.
	DirectTools []string
	// ToolsAlwaysLoad marks this connection's listed tools as always loaded.
	ToolsAlwaysLoad bool
	// ManualHarness is the harness name the hosted lead's workflow manual
	// renders under; "" renders with none.
	ManualHarness string
	// PlaybookCommands marks a backend whose hosted sessions run without
	// ws's plugin skills and reach ws only as the ReservedMCPServer server.
	// Its hosted lead offers the package's lead playbooks as dashboard
	// commands (Options.PlaybookCommands), which the runner expands into
	// the playbook body (Options.ExpandCommand), and text ws renders for
	// its sessions names that server and those commands instead of the
	// package's tool and skill namespace.
	PlaybookCommands bool
	// PreferNativeSubagent marks a backend whose hosted lead keeps its own
	// native subagent tool for exploration that needs no ws tools and starts
	// every other delegate through agent.spawn, preferring an external
	// harness when one fits (a prompt rule, not a gate). Its
	// lead's prompt line (Runner.subagentSteer) and the playbook text it
	// executes itself (workflow_manual, playbook.read) say so; text rendered
	// for another process (playbook.render) is unchanged.
	PreferNativeSubagent bool
	// ResolveLeadSubagent, when set alongside PreferNativeSubagent, resolves
	// the hosted-lead subagent policy dynamically from layered wsconfig
	// (project, global, optional session key) for prompt rendering and
	// self-executed workflow text. When nil, LeadSubagent falls back to
	// PreferNativeSubagent and Catalog.NativeSubagentTool(RoleLead).
	ResolveLeadSubagent func(cfg wsconfig.Options, reader wsconfig.SessionReader, wsKey string) LeadSubagentPolicy
	// Profiles are this backend's runtime-owned builtin profile definitions.
	// They feed the config floor but are not fields of the persisted Profile
	// shape. Support marks the profiles offered by tracker support, in order.
	Profiles []BuiltinProfileRegistration
	// BuiltinProfilesOnly keeps the backend out of saved profiles while
	// allowing runtime-owned profiles to use it.
	BuiltinProfilesOnly bool
	// PlaybookTerminology supplies static harness wording for playbook renders.
	PlaybookTerminology PlaybookTerminology
	// ExecCutover is the backend's verified native process tool removal
	// (exec_cutover.go); nil keeps every session's native tools.
	ExecCutover *ExecCutoverSupport
	// OneShot is the backend's isolated single-prompt mode (oneshot.go);
	// nil means the harness has none and a one-shot call is refused.
	OneShot *OneShotSupport
	// ProviderCredentials admits this backend's hosted sessions to the
	// daemon's provider token route (Runner.ResolveCredentialToken): a
	// session may obtain a provider credential with its per-session token.
	// Only a harness that makes model requests itself, for the signed-in
	// user, sets it; a harness that brings its own login never does.
	ProviderCredentials bool
	// Permission relates the backend's native permission modes to the common
	// levels; Lifecycle declares how its hosted sessions start, run, and end
	// (backend_policy.go). Both are runtime-only, and their zero values ask
	// for no special behavior.
	Permission PermissionPolicy
	Lifecycle  LifecyclePolicy
	// Connection is how the backend's client connects and how the daemon's MCP
	// server treats that connection (backend_policy.go); Dashboard is how the
	// dashboard's surfaces treat the backend (wsdashboard.HarnessTraits).
	// Both are runtime-only registration data.
	Connection ConnectionPolicy
	Dashboard  wsdashboard.HarnessTraits
}

// PlaybookTerminology holds static native-tool idioms. Model, effort, and
// namespace-dependent bindings are resolved when a playbook is rendered.
type PlaybookTerminology struct {
	ExploreAgent, SpawnIdiom, ContinueIdiom string
	// SpawnIdiomFor, when set, spells SpawnIdiom for the runtime namespace of
	// the rendering server and replaces SpawnIdiom, which a namespaced idiom
	// cannot state statically.
	SpawnIdiomFor func(namespace string) string
}

// IsZero reports whether no wording is declared, so a render uses the
// host-neutral one.
func (t PlaybookTerminology) IsZero() bool {
	return t.ExploreAgent == "" && t.SpawnIdiom == "" && t.ContinueIdiom == "" && t.SpawnIdiomFor == nil
}

// LeadSubagentPolicy describes how a hosted lead uses its native subagent tool
// vs ws agent.spawn / mailbox.send when rendering its startup prompt or
// self-executed workflow text (workflow_manual, playbook.read, ExpandCommand).
type LeadSubagentPolicy struct {
	// Active reports whether the registration supplies a hosted-lead subagent
	// policy (native-first or explicit prompt-only spawn-only override).
	Active bool
	// PreferNativeSubagent selects native-first exploration + external-tier
	// spawning guidance.
	PreferNativeSubagent bool
	// AvoidSameHarnessSpawn controls whether agent.spawn tier guidance warns
	// against same-harness routes (external terms risk) and steers toward an
	// external fixed tier when another harness fits.
	AvoidSameHarnessSpawn bool
	// NativeSubagentTool names the harness's native subagent tool referred to by
	// prompt prohibition / steering (for example "invoke_subagent"), even when
	// Catalog.ItemSubagentRemoval stays unsupported.
	NativeSubagentTool string
}

// LeadSubagent resolves this backend's hosted-lead subagent policy for the
// given layered config, session reader, and session key.
func (r BackendRegistration) LeadSubagent(cfg wsconfig.Options, reader wsconfig.SessionReader, wsKey string) LeadSubagentPolicy {
	if !r.PreferNativeSubagent {
		return LeadSubagentPolicy{}
	}
	if r.ResolveLeadSubagent != nil {
		return r.ResolveLeadSubagent(cfg, reader, wsKey)
	}
	return LeadSubagentPolicy{
		Active:                true,
		PreferNativeSubagent:  true,
		AvoidSameHarnessSpawn: true,
		NativeSubagentTool:    r.Catalog.NativeSubagentTool(RoleLead),
	}
}

// BuiltinProfileRegistration is one builtin profile owned by a backend's
// registration. Definition is serialized into the existing Profile shape;
// Support is runtime-only descriptor data.
type BuiltinProfileRegistration struct {
	Name       string
	Definition Profile
	Support    bool
}

type namedBackendRegistration struct {
	name string
	reg  BackendRegistration
}

// builtinRegistrations is the immutable descriptor floor. Its order preserves
// the advertised config selectors and tracker-support preference.
var builtinRegistrations = sync.OnceValue(func() []namedBackendRegistration {
	backends := []registrationBackend{ClaudeBackend{}, CodexBackend{}, AgyBackend{}, WerkBackend{}, PiRPCBackend{}}
	out := make([]namedBackendRegistration, 0, len(backends))
	for _, backend := range backends {
		name, reg := backend.registration()
		out = append(out, namedBackendRegistration{name: name, reg: cloneBackendRegistration(reg)})
	}
	return out
})

func builtinRegistration(name string) BackendRegistration {
	for _, value := range builtinRegistrations() {
		if value.name == name {
			return value.reg
		}
	}
	return BackendRegistration{}
}

// BuiltinHarnessNames returns the built-in harness names in config listing
// order. Callers can append their selector or storage sentinel to the result.
func BuiltinHarnessNames() []string {
	values := builtinRegistrations()
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.name)
	}
	return names
}

// BuiltinPlaybookTerminology returns the built-in static bindings, or zero
// values when the harness uses the host-neutral wording.
func BuiltinPlaybookTerminology(harness string) PlaybookTerminology {
	return builtinRegistration(harness).PlaybookTerminology
}

// BuiltinLifecycle is the built-in backend's lifecycle declaration; the zero
// value for a name that is not built in. It reads the shared floor.
func BuiltinLifecycle(name string) LifecyclePolicy {
	return builtinRegistration(name).Lifecycle
}

// BuiltinConnection is the built-in backend's connection declaration; the zero
// value for a name that is not built in. It reads the shared floor, which
// callers never modify.
func BuiltinConnection(name string) ConnectionPolicy {
	return builtinRegistration(name).Connection
}

// NamedConnection is a backend's name with its connection declaration.
type NamedConnection struct {
	Name       string
	Connection ConnectionPolicy
}

// BuiltinConnections lists every built-in backend's connection declaration in
// config listing order, for a connection that has no daemon registry. The
// policies are shared and read-only.
func BuiltinConnections() []NamedConnection {
	values := builtinRegistrations()
	out := make([]NamedConnection, 0, len(values))
	for _, value := range values {
		out = append(out, NamedConnection{Name: value.name, Connection: value.reg.Connection})
	}
	return out
}

// BuiltinBackendRegistrations returns fresh runtime descriptors for every
// built-in harness. Callers may mutate the returned map and values.
func BuiltinBackendRegistrations() map[string]BackendRegistration {
	values := builtinRegistrations()
	out := make(map[string]BackendRegistration, len(values))
	for _, value := range values {
		reg := cloneBackendRegistration(value.reg)
		if reg.Catalog != nil {
			catalog := *reg.Catalog
			catalog.Entries = slices.Clone(catalog.Entries)
			for i := range catalog.Entries {
				catalog.Entries[i].Values = slices.Clone(catalog.Entries[i].Values)
			}
			reg.Catalog = &catalog
		}
		if reg.NativeCompaction != nil {
			protocol := *reg.NativeCompaction
			reg.NativeCompaction = &protocol
		}
		out[value.name] = reg
	}
	return out
}

func cloneProfile(p Profile) Profile {
	p.Tools = slices.Clone(p.Tools)
	p.WSTools = slices.Clone(p.WSTools)
	p.Plugins = slices.Clone(p.Plugins)
	if p.MCPServers != nil {
		p.MCPServers = maps.Clone(p.MCPServers)
		for name, raw := range p.MCPServers {
			p.MCPServers[name] = slices.Clone(raw)
		}
	}
	if p.IdleTimeoutSeconds != nil {
		seconds := *p.IdleTimeoutSeconds
		p.IdleTimeoutSeconds = &seconds
	}
	return p
}

func cloneBuiltinProfiles(in []BuiltinProfileRegistration) []BuiltinProfileRegistration {
	out := slices.Clone(in)
	for i := range out {
		out[i].Definition = cloneProfile(out[i].Definition)
	}
	return out
}

func cloneBackendRegistration(reg BackendRegistration) BackendRegistration {
	reg.PermissionModes = slices.Clone(reg.PermissionModes)
	reg.PermissionLevels = slices.Clone(reg.PermissionLevels)
	reg.DirectTools = slices.Clone(reg.DirectTools)
	reg.Permission = clonePermissionPolicy(reg.Permission)
	reg.Profiles = cloneBuiltinProfiles(reg.Profiles)
	if reg.OneShot != nil {
		oneShot := *reg.OneShot
		reg.OneShot = &oneShot
	}
	return reg
}

// ToolNameSchemeSanitize spells a dotted ws tool name the way the Pi bridge
// registers it (agents-plugin-pi/src/bridge.ts sanitizeToolName): each "."
// flattened to "_", which fits the ^[a-zA-Z0-9_-]{1,128}$ names an Anthropic
// API tool accepts. The bridge's "ws__" prefix is not applied: it separates
// Pi's one flat tool namespace, while agy already prefixes a forwarded tool
// with its server name (mcp_ws_).
const ToolNameSchemeSanitize = "sanitize"

// ApplyToolNameScheme spells raw under scheme; ok is false for a scheme
// no one defines. The empty scheme is the identity.
func ApplyToolNameScheme(scheme, raw string) (name string, ok bool) {
	switch scheme {
	case "":
		return raw, true
	case ToolNameSchemeSanitize:
		return strings.ReplaceAll(raw, ".", "_"), true
	}
	return "", false
}

// BackendRegistrations exposes the runner's admitted set to in-process config
// readers. The returned map and slices are copies; backend instances and
// catalogs are shared and read-only.
func (r *Runner) BackendRegistrations() map[string]BackendRegistration {
	out := make(map[string]BackendRegistration, len(r.opts.Backends))
	for name, reg := range r.opts.Backends {
		out[name] = cloneBackendRegistration(reg)
	}
	return out
}

// BackendLifecycle is the registered backend name's lifecycle declaration, for
// a caller on a hot path that must not clone the whole registry. ok is false
// for a name the runner does not register. The policy is a plain value.
func (r *Runner) BackendLifecycle(name string) (LifecyclePolicy, bool) {
	reg, ok := r.opts.Backends[name]
	return reg.Lifecycle, ok
}

// BackendConnection is BackendLifecycle for a backend's connection declaration.
func (r *Runner) BackendConnection(name string) (ConnectionPolicy, bool) {
	reg, ok := r.opts.Backends[name]
	return reg.Connection, ok
}

// BuiltinDashboardHarnesses is the built-in backends' dashboard traits by
// name, for a daemon embedded without a runner (a fixture) that still shows
// built-in harnesses.
func BuiltinDashboardHarnesses() wsdashboard.HarnessTraitSet {
	values := builtinRegistrations()
	out := make(wsdashboard.HarnessTraitSet, len(values))
	for _, value := range values {
		out[value.name] = value.reg.Dashboard
	}
	return out
}

// DashboardHarnesses is the registered backends' dashboard traits by name,
// for DaemonOptions.Harnesses. Every registered name is present, a backend
// with nothing to declare as the zero traits.
func (r *Runner) DashboardHarnesses() wsdashboard.HarnessTraitSet {
	out := make(wsdashboard.HarnessTraitSet, len(r.opts.Backends))
	for name, reg := range r.opts.Backends {
		out[name] = reg.Dashboard
	}
	return out
}

// leadHarnesses are the registered backends with a lead profile, sorted.
func (r *Runner) leadHarnesses() []string {
	var names []string
	for name, reg := range r.opts.Backends {
		if reg.LeadProfile != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// profileBackends defaults only keyless/standalone profile readers to the
// builtin profile floor. Actual daemon admission always supplies its registry;
// this is not a second registry of process implementations or descriptors.
func profileBackends(registries []map[string]BackendRegistration) []string {
	var names []string
	if len(registries) > 0 {
		for name := range registries[0] {
			names = append(names, name)
		}
	} else {
		names = BuiltinHarnessNames()
	}
	sort.Strings(names)
	return names
}

func builtinProfilesOnly(name string, registries []map[string]BackendRegistration) bool {
	if len(registries) > 0 {
		return registries[0][name].BuiltinProfilesOnly
	}
	return builtinRegistration(name).BuiltinProfilesOnly
}

func savedProfileBackends(registries []map[string]BackendRegistration) []string {
	return slices.DeleteFunc(profileBackends(registries), func(name string) bool {
		return builtinProfilesOnly(name, registries)
	})
}

// permissionModes preserves legacy Pi values accepted on reads and ignored at
// launch. Registered extensions use their own modes, not the Pi fallback.
// Retained name check: legacy Pi permission values are persisted bytes read
// back by name (TestPermissionModesLegacyFallback).
func permissionModes(backend string, registries []map[string]BackendRegistration) []string {
	if backend == BackendPi {
		return LeadPermissionModes
	}
	if len(registries) > 0 {
		return registries[0][backend].PermissionModes
	}
	return builtinRegistration(backend).PermissionModes
}

func profilePermissionModes(registries []map[string]BackendRegistration) []string {
	modes := []string{}
	seen := map[string]bool{}
	for _, name := range savedProfileBackends(registries) {
		choices := permissionModes(name, registries)
		for _, mode := range choices {
			if !seen[mode] {
				seen[mode] = true
				modes = append(modes, mode)
			}
		}
	}
	return modes
}

func backendAlternatives(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		raw, _ := json.Marshal(name)
		quoted[i] = string(raw)
	}
	return strings.Join(quoted, " or ")
}

var (
	ErrBackendUnavailable = errors.New("backend unavailable")
	ErrInvalidProfile     = errors.New("invalid profile")
	ErrLaunchFailed       = errors.New("launch failed")
)

// categorizedError adds an internal classification without changing the
// original text, requestError status, or underlying error identity.
type categorizedError struct{ kind, cause error }

func (e *categorizedError) Error() string        { return e.cause.Error() }
func (e *categorizedError) Unwrap() error        { return e.cause }
func (e *categorizedError) Is(target error) bool { return target == e.kind }
func categorized(kind, cause error) error {
	if cause == nil {
		return nil
	}
	return &categorizedError{kind: kind, cause: cause}
}

// AdmitsLead is used by in-process consumers such as the hiring gate.
func (r *Runner) AdmitsLead(backend string) bool {
	return LeadProfileFor(backend, r.opts.Backends) != ""
}
