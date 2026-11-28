package wsagent

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// AgentWorkerProfile is the internal builtin profile an agent.spawn child
// of backend runs.
func AgentWorkerProfile(backend string) string { return "agent-worker-" + backend }

func isAgentWorkerProfile(name string) bool {
	// Reserve the internal namespace even for an unregistered backend:
	// compatibility spawn paths must never bypass agent.spawn admission.
	return strings.HasPrefix(name, "agent-worker-")
}

// RemovesSubagents reports whether rec's launches remove the harness's
// native subagent tool (StartSpec.RemoveSubagents), so that it delegates
// through agent.spawn alone: a hosted lead and an agent.spawn child do.
// The internal and dashboard profiles that rely on the native tool (the
// Claude tracker support profile, whose only tool it is) keep it.
func RemovesSubagents(rec Record) bool {
	return rec.Lead || isAgentWorkerProfile(rec.Profile)
}

// MaxSpawnWidth caps an agent's awake direct children; a sleeping child
// does not count, and a wake through Deliver ignores the cap.
const MaxSpawnWidth = 99

// DefaultChildWaitingDays is wsconfig.ItemAgentChildWaitingDays's builtin
// default.
const DefaultChildWaitingDays = 15

// EnvPermissionLevel carries a spawned child's authority to its attached
// ws-mcp, which refuses the mutating ws tools for read-only. Blank is none.
const EnvPermissionLevel = "WS_MCP_PERMISSION_LEVEL"

// systemPromptFile is the session directory's copy of an agent.spawn
// system prompt (Record.SystemPrompt).
const systemPromptFile = wsdashboard.SessionSystemPromptFileName

// maxSystemPrompt bounds a system_prompt_path read.
const maxSystemPrompt = 1 << 20

// AgentCaller is the agent.* caller: the parent a spawn records, and the
// root, directory, harness and config its tier and cwd resolve in.
type AgentCaller struct {
	Parent ParentRef
	// Root anchors the child's mail and config; WorkDir (empty: Root) is
	// what a relative cwd, worktree or system_prompt_path is joined to.
	Root    string
	WorkDir string
	// Harness is the caller's own harness: a bare tier resolves there. A
	// lead's comes from its connection's detection, so it selects tiers
	// only and never admits a worker.
	Harness string
	// VerifiedBackend is the backend the caller is verified to run on: a
	// daemon-hosted lead's own record backend, or a token holder's record
	// backend. Empty is no verified binding, which a backend registered
	// with a cross-harness refusal treats as another harness (admitWorker).
	// A spawn stamps it on the child (Record.SpawnerBackend).
	VerifiedBackend string
	// Config is where agents.tier and agent.tier_aliases are read.
	Config wsconfig.Options
	// PolicyKey is the caller's own ws session key. A child's key inherits
	// the caller's session-scope config through it (ChildKeyStore); empty
	// mints an independent key.
	PolicyKey string
	// holder is a token holder's own record (a nested spawn): its depth
	// and tier cap the child's. Nil for a lead.
	holder *Record
}

// LeadCaller is a lead session's agent.* caller: replyID is the lead's
// mailbox reply-id, the parent its children record. It carries no
// VerifiedBackend; a daemon-hosted lead's server sets it from the lead's
// own record.
func LeadCaller(replyID, root, harness string, cfg wsconfig.Options) AgentCaller {
	return AgentCaller{Parent: ParentRef{Kind: ParentLead, ReplyID: replyID}, Root: root, Harness: harness, Config: cfg}
}

// holderCaller is a token holder's agent.* caller.
func (r *Runner) holderCaller(rec Record) AgentCaller {
	cfg := r.opts.Config
	cfg.RepoRoot = rec.Root
	return AgentCaller{
		Parent:          ParentRef{Kind: ParentConversation, ReplyID: rec.ReplyID},
		Root:            rec.Root,
		WorkDir:         rec.workDir(),
		Harness:         rec.Backend,
		VerifiedBackend: rec.Backend,
		Config:          cfg,
		PolicyKey:       rec.WSKey,
		holder:          &rec,
	}
}

func (c AgentCaller) base() string {
	if c.WorkDir != "" {
		return c.WorkDir
	}
	return c.Root
}

// AgentSpawnRequest is agent.spawn's arguments.
type AgentSpawnRequest struct {
	Prompt           string `json:"prompt"`
	Tier             string `json:"tier"`
	SystemPrompt     string `json:"system_prompt,omitempty"`
	SystemPromptPath string `json:"system_prompt_path,omitempty"`
	Authority        string `json:"authority"`
	Cwd              string `json:"cwd,omitempty"`
	Worktree         string `json:"worktree,omitempty"`
	Label            string `json:"label,omitempty"`
}

// AgentSpawnResult is agent.spawn's answer: the child's mailbox address,
// its control key, and its status (AgentStatus).
type AgentSpawnResult struct {
	Address string `json:"address"`
	Status  string `json:"status"`
}

// AgentInterruptResult is agent.interrupt's answer: the child's status
// and the messages it had not taken yet, handed back.
type AgentInterruptResult struct {
	Address  string   `json:"address"`
	Status   string   `json:"status"`
	Returned []string `json:"returned"`
}

// AgentChild is one agent.list entry.
type AgentChild struct {
	Address string `json:"address"`
	Label   string `json:"label,omitempty"`
	// Tier is <harness>.<tier>.
	Tier      string          `json:"tier"`
	Authority PermissionLevel `json:"authority,omitempty"`
	// State is AgentStatus's value.
	State string `json:"state"`
	// LastReport is the first line of its last settled turn's final
	// message, with that turn's status.
	LastReport string `json:"last_report,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Held       int    `json:"held,omitempty"`
	Error      string `json:"error,omitempty"`
	CreatedAt  string `json:"created_at"`
	// Activity carries the runner's compact-list inputs across the token route.
	Activity *AgentChildActivity `json:"activity,omitempty"`
}

type AgentChildActivity struct {
	LastActiveAt          string `json:"last_active_at,omitempty"`
	AwaitingReport        bool   `json:"awaiting_report,omitempty"`
	SettledInterruptError bool   `json:"settled_interrupt_error,omitempty"`
}

// AgentTierEntry is one spawnable tier and what it resolves to.
type AgentTierEntry struct {
	Tier     string `json:"tier"`
	Model    string `json:"model"`
	Effort   string `json:"effort,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// AgentAliasEntry is one agent.tier_aliases entry: its target and, when
// the target is spawnable now, the model it resolves to; Error says why
// not.
type AgentAliasEntry struct {
	Alias  string `json:"alias"`
	Target string `json:"target"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Error  string `json:"error,omitempty"`
}

// AgentTiers is agent.tiers' answer: bare tiers on the caller's harness
// (Native), other harnesses' <harness>.<tier> (Ext), and the user's
// aliases.
type AgentTiers struct {
	Harness string            `json:"harness"`
	Native  []AgentTierEntry  `json:"native"`
	Ext     []AgentTierEntry  `json:"ext"`
	Aliases []AgentAliasEntry `json:"aliases"`
	// Unavailable names each registered worker harness no tier of which is
	// spawnable now, and why.
	Unavailable []string `json:"unavailable,omitempty"`
	// Routing is the agent.tier_routing graph this daemon resolves role
	// tiers and bundles through. A daemon without the graph omits it, so
	// its absence is how a caller detects that role tiers are unsupported.
	Routing *AgentRouting `json:"routing,omitempty"`
}

// AgentRouting is agent.tiers' routing section: whether role tiers resolve
// here, why the graph is unusable when it is not (Error), each bundle and
// node route with its trace, and the legacy aliases that shadow graph names.
type AgentRouting struct {
	Supported  bool              `json:"supported"`
	Error      string            `json:"error,omitempty"`
	Bundles    []AgentRouteEntry `json:"bundles"`
	Routes     []AgentRouteEntry `json:"routes"`
	Collisions []string          `json:"collisions,omitempty"`
}

// AgentRouteEntry is one routed tier: the token, its resolution trace, and
// the binding it spawns with now; Error says why it is not spawnable.
type AgentRouteEntry struct {
	Tier     string `json:"tier"`
	Trace    string `json:"trace,omitempty"`
	Harness  string `json:"harness,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Error    string `json:"error,omitempty"`
}

// TierHarnesses are the harness names a tier route or bundle may name: the
// built-in harnesses and every registered backend. A known harness that is
// unavailable stays valid in config and reports at resolution.
// Retained name check: stored tiers can still name Pi (TestRetiredPiTierRedirectsToWerk).
func TierHarnesses(registries ...map[string]BackendRegistration) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, name := range BuiltinHarnessNames() {
		add(name)
	}
	// The retired harness is still valid in stored routing graphs.
	add(BackendPi)
	for _, reg := range registries {
		for _, name := range sortedRegistrationNames(reg) {
			add(name)
		}
	}
	return out
}

func sortedRegistrationNames(reg map[string]BackendRegistration) []string {
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// tierGraph is the effective agent.tier_routing graph for caller: the
// packaged playbook roles (Options.TierRoles) with the user's overrides.
func (r *Runner) tierGraph(caller AgentCaller) (*wsconfig.TierGraph, error) {
	var roles []wsconfig.TierRole
	if r.opts.TierRoles != nil {
		var err error
		if roles, err = r.opts.TierRoles(); err != nil {
			return nil, fmt.Errorf("read packaged tier roles: %w", err)
		}
	}
	return wsconfig.LoadTierGraph(caller.Config, roles, TierHarnesses(r.opts.Backends))
}

// Agent statuses: a turn runs; turn ended with background work running; the
// first message waits for the process; a process waits for input; asleep (no
// process, no turn); closed.
const (
	AgentRunning    = "running"
	AgentBackground = "background"
	AgentStarting   = "starting"
	AgentIdle       = "idle"
	AgentWaiting    = "waiting"
	AgentClosed     = "closed"
)

// AgentStatus is st's agent.* status.
func AgentStatus(st State) string {
	switch {
	case st.Status == StatusClosed || st.Status == StatusHandedOff:
		return AgentClosed
	case st.Status == StatusRunning:
		return AgentRunning
	case st.BackgroundTasks > 0:
		return AgentBackground
	case st.Process:
		return AgentIdle
	case st.Held > 0 && st.Error == "":
		return AgentStarting
	}
	return AgentWaiting
}

// AgentAddress is conversation id's mailbox address.
func AgentAddress(id string) string { return "id:" + id }

func agentID(address string) string {
	return strings.TrimPrefix(strings.TrimSpace(address), "id:")
}

// spawnableHarness reports why harness has no spawnable tier now for a
// caller verified on callerBackend: not registered, refused across
// harnesses, or not installed. "" is spawnable.
func (r *Runner) spawnableHarness(callerBackend, harness string) string {
	return r.registeredWorker(callerBackend, harness)
}

// registeredWorker is spawnableHarness before the child check: registration,
// the caller admission, the worker role, and the executable on PATH. It runs
// no subprocess.
func (r *Runner) registeredWorker(callerBackend, harness string) string {
	reg, ok := r.opts.Backends[harness]
	if !ok || reg.Backend == nil {
		return fmt.Sprintf("harness %q is not registered in this daemon", harness)
	}
	if err := r.admitWorker(harness, callerBackend); err != nil {
		return err.Error()
	}
	return workerRoleGap(harness, reg)
}

// workerRoleGap is registeredWorker's caller-independent part: the worker
// role and the executable. A daemon-created lead (companion) reuses it
// without the cross-harness caller admission, which guards workers only.
func workerRoleGap(harness string, reg BackendRegistration) string {
	if !reg.WorkerCapable || reg.WorkerProfile == "" {
		return fmt.Sprintf("harness %q is registered for hosted leads only, not for spawned workers", harness)
	}
	if ok, why := supportBackendAvailability(harness, reg.Backend); !ok {
		return why
	}
	return ""
}

// admitWorker is the one cross-harness worker admission, for both the
// decision and its text: a worker on backend for a caller verified on
// callerBackend ("" is none). Every worker entry passes it:
//   - agent.spawn's tier resolution (spawnableHarness, registeredWorker);
//   - a profile create (create), whose spawner is its spawn's caller and
//     none for a dashboard or REST creation;
//   - every launch or wake of an existing worker record (admitWorkerRecord);
//   - a rejoin (reattach, checkReattach).
//
// The tier listing and the dashboard trace quote it too (registeredWorker,
// unusableHarnessError). It refuses only a backend registered with a
// cross-harness refusal, and only for a caller not verified on that same
// backend; the record itself is kept.
func (r *Runner) admitWorker(backend, callerBackend string) error {
	if reason := r.opts.Backends[backend].crossHarnessRefusal; reason != "" && callerBackend != backend {
		return &admissionRefusal{reason: reason}
	}
	return nil
}

// admitWorkerRecord is admitWorker for an existing record. It decides from
// the record's immutable backend, lead flag, and spawner stamp (a legacy
// record's is derived from its parent's record at startup,
// resolveSpawnerBackends), before any profile resolution, so an edited or
// removed profile cannot mask the refusal. A lead is never refused.
func (r *Runner) admitWorkerRecord(rec Record) error {
	if rec.Lead {
		return nil
	}
	return r.admitWorker(rec.Backend, rec.SpawnerBackend)
}

// workerHarnesses are the registered worker-role backends, sorted.
func (r *Runner) workerHarnesses() []string {
	var out []string
	for name, reg := range r.opts.Backends {
		if reg.WorkerCapable && reg.WorkerProfile != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// unusableHarnessError is an alias or route entry's error for a harness with
// no usable tiers for a caller verified on callerBackend: admitWorker's
// refusal text, else the generic text.
func (r *Runner) unusableHarnessError(callerBackend, harness string) string {
	if err := r.admitWorker(harness, callerBackend); err != nil {
		return err.Error()
	}
	if why := r.registeredWorker(callerBackend, harness); why != "" {
		return why
	}
	return fmt.Sprintf("harness %q has no spawnable tier now", harness)
}

func tierEntries(prefix string, tiers map[string]wsconfig.AgentTier) []AgentTierEntry {
	var out []AgentTierEntry
	for _, t := range []string{"small", "medium", "large", "xlarge"} {
		if m, ok := tiers[t]; ok {
			out = append(out, AgentTierEntry{Tier: prefix + t, Model: m.Model, Effort: m.Effort, Provider: m.Provider})
		}
	}
	return out
}

// AgentTiers lists what caller may pass as agent.spawn's tier.
func (r *Runner) AgentTiers(caller AgentCaller) (AgentTiers, error) {
	return r.agentTiers(caller, NewTierBindingResolver(caller.Config, r.opts.Backends))
}

func (r *Runner) agentTiers(caller AgentCaller, resolver *TierBindingResolver) (AgentTiers, error) {
	out := AgentTiers{Harness: caller.Harness, Native: []AgentTierEntry{}, Ext: []AgentTierEntry{}, Aliases: []AgentAliasEntry{}}
	usable := map[string]map[string]wsconfig.AgentTier{}
	for _, h := range r.workerHarnesses() {
		if why := r.spawnableHarness(caller.VerifiedBackend, h); why != "" {
			out.Unavailable = append(out.Unavailable, h+": "+why)
			continue
		}
		tiers, err := resolver.tierBindings(h, "")
		if err != nil {
			return AgentTiers{}, fmt.Errorf("read agents.tier: %w", err)
		}
		if setup, ok := r.opts.Backends[h].Backend.(workerSetup); ok {
			if why := setup.workerTierGap(tiers); why != "" {
				out.Unavailable = append(out.Unavailable, h+": "+why)
			}
			gaps := make(map[string]string)
			for _, tier := range []string{"small", "medium", "large", "xlarge"} {
				if mapping, set := tiers[tier]; set {
					why, checked := gaps[mapping.Provider]
					if !checked {
						why = setup.workerProviderGap(mapping.Provider)
						gaps[mapping.Provider] = why
					}
					if why != "" {
						out.Unavailable = append(out.Unavailable, h+"."+tier+": "+why)
						delete(tiers, tier)
					}
				}
			}
		}
		usable[h] = tiers
		if h == TierRuntimeHarness(caller.Harness) {
			out.Native = tierEntries("", tiers)
		}
		out.Ext = append(out.Ext, tierEntries(h+".", tiers)...)
	}
	aliases, err := tierAliases(caller.Config)
	if err != nil {
		return AgentTiers{}, err
	}
	names := make([]string, 0, len(aliases))
	for name := range aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := aliases[name]
		entry := AgentAliasEntry{Alias: name, Target: target}
		h, t, _ := strings.Cut(target, ".")
		h = TierRuntimeHarness(h)
		if tiers, ok := usable[h]; !ok {
			entry.Error = r.unusableHarnessError(caller.VerifiedBackend, h)
		} else if m, ok := tiers[t]; !ok {
			entry.Error = fmt.Sprintf("tier %q is not set for %s", t, h)
		} else {
			entry.Model, entry.Effort = m.Model, m.Effort
		}
		out.Aliases = append(out.Aliases, entry)
	}
	routing, err := r.agentRouting(caller, usable, resolver)
	if err != nil {
		return AgentTiers{}, err
	}
	out.Routing = routing
	return out, nil
}

// agentRouting lists the routing graph for caller: each bundle, each role
// at the size its playbook declares, and each group and user node at all
// four sizes, with the binding each spawns with given usable (the
// spawnable harnesses' tiers).
func (r *Runner) agentRouting(caller AgentCaller, usable map[string]map[string]wsconfig.AgentTier, resolver *TierBindingResolver) (*AgentRouting, error) {
	graph, err := r.tierGraph(caller)
	if err != nil {
		return &AgentRouting{Supported: true, Error: err.Error(), Bundles: []AgentRouteEntry{}, Routes: []AgentRouteEntry{}}, nil
	}
	out := &AgentRouting{Supported: true, Bundles: []AgentRouteEntry{}, Routes: []AgentRouteEntry{}, Collisions: graph.Collisions()}
	if gerr := graph.Err(); gerr != nil {
		out.Error = gerr.Error()
		return out, nil
	}
	entry := func(token string) AgentRouteEntry {
		res, err := graph.Resolve(token, caller.Harness)
		if err != nil {
			return AgentRouteEntry{Tier: token, Error: err.Error()}
		}
		harness := TierRuntimeHarness(res.Harness)
		e := AgentRouteEntry{Tier: token, Trace: res.Trace(), Harness: harness}
		tiers, ok := usable[harness]
		switch {
		case !ok:
			e.Error = r.unusableHarnessError(caller.VerifiedBackend, harness)
		case res.Bundle != "":
			binding, err := resolver.Routed(res)
			if err != nil {
				e.Error = err.Error()
			} else if why := r.workerProviderGap(harness, binding.Provider); why != "" {
				e.Error = why
			} else {
				e.Provider, e.Model, e.Effort = binding.Provider, binding.Model, binding.Effort
			}
		default:
			if m, ok := tiers[res.Size]; ok {
				e.Provider, e.Model, e.Effort = m.Provider, m.Model, m.Effort
			} else {
				e.Error = fmt.Sprintf("tier %q is not set for %s", res.Size, harness)
			}
		}
		return e
	}
	bundles := graph.Bundles()
	names := make([]string, 0, len(bundles))
	for name := range bundles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out.Bundles = append(out.Bundles, entry(name))
	}
	for _, token := range graph.RouteTokens() {
		out.Routes = append(out.Routes, entry(token))
	}
	return out, nil
}

// tierAliases reads agent.tier_aliases (global-only) for cfg.
func tierAliases(cfg wsconfig.Options) (map[string]string, error) {
	resolver := wsconfig.NewResolver(cfg, map[string]string{wsconfig.ItemAgentTierAliases: ""}, nil, nil)
	raw, err := resolver.Get("", wsconfig.ItemAgentTierAliases)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", wsconfig.ItemAgentTierAliases, err)
	}
	return wsconfig.ParseTierAliases(raw.Value)
}

// resolveTier resolves agent.spawn's tier for caller: a bare tier on the
// caller's harness, <harness>.<tier>, an alias, or, through the
// agent.tier_routing graph, a <node>:<size> role tier or a bundle name. A
// tier no spawnable harness sets fails with the valid list. bare is the
// tier rank the child records: the routed size, or xlarge for a bare bundle
// name, which supplies its model and effort directly.
func (r *Runner) resolveTier(caller AgentCaller, tier string) (harness, bare string, mapping wsconfig.AgentTier, err error) {
	resolver := NewTierBindingResolver(caller.Config, r.opts.Backends)
	raw := strings.ToLower(strings.TrimSpace(tier))
	if raw == "" {
		return "", "", mapping, r.tierRefusal(caller, resolver, "tier is required")
	}
	switch h, t, dotted := strings.Cut(raw, "."); {
	case dotted:
		harness, bare = h, wsconfig.NormalizedTier(t)
		if IsRetiredDirectTierHarness(harness) && bare != "" {
			return "", "", mapping, r.tierRefusal(caller, resolver, fmt.Sprintf("tier %q: harness %q is not registered in this daemon", tier, harness))
		}
	case wsconfig.NormalizedTier(raw) != "":
		harness, bare = caller.Harness, wsconfig.NormalizedTier(raw)
	default:
		aliases, aerr := tierAliases(caller.Config)
		if aerr != nil {
			return "", "", mapping, aerr
		}
		target, ok := aliases[raw]
		if !ok {
			// Not a legacy token: resolve it through the routing graph.
			// Exact aliases already won above, so the graph's own alias
			// checks only apply at the nodes a walk passes.
			graph, gerr := r.tierGraph(caller)
			if gerr != nil {
				return "", "", mapping, gerr
			}
			res, rerr := graph.Resolve(raw, caller.Harness)
			if rerr != nil {
				return "", "", mapping, r.tierRefusal(caller, resolver, rerr.Error())
			}
			if res.Bundle != "" {
				harness := TierRuntimeHarness(res.Harness)
				if why := r.spawnableHarness(caller.VerifiedBackend, harness); why != "" {
					return "", "", mapping, r.tierRefusal(caller, resolver, fmt.Sprintf("tier %q (%s): %s", tier, res.Trace(), why))
				}
				binding, err := resolver.Routed(res)
				if err != nil {
					return "", "", mapping, badRequest("tier %q (%s): %v", tier, res.Trace(), err)
				}
				if why := r.workerStartProviderGap(harness, binding.Provider); why != "" {
					return "", "", mapping, r.tierRefusal(caller, resolver, fmt.Sprintf("tier %q (%s): %s", tier, res.Trace(), why))
				}
				return harness, res.Rank(), binding, nil
			}
			harness, bare = res.Harness, res.Size
			break
		}
		h, t, _ := strings.Cut(target, ".")
		harness, bare = h, t
	}
	if bare == "" {
		return "", "", mapping, r.tierRefusal(caller, resolver, fmt.Sprintf("tier %q names no tier (small, medium, large, xlarge)", tier))
	}
	harness = TierRuntimeHarness(harness)
	if why := r.spawnableHarness(caller.VerifiedBackend, harness); why != "" {
		return "", "", mapping, r.tierRefusal(caller, resolver, fmt.Sprintf("tier %q: %s", tier, why))
	}
	mapping, err = resolver.harnessSlot(harness, bare)
	var noSlot noTierSlotError
	if errors.As(err, &noSlot) {
		return "", "", mapping, r.tierRefusal(caller, resolver, fmt.Sprintf("tier %q: %v", tier, err))
	}
	if err != nil {
		return "", "", mapping, err
	}
	if why := r.workerStartProviderGap(harness, mapping.Provider); why != "" {
		return "", "", mapping, r.tierRefusal(caller, resolver, fmt.Sprintf("tier %q: %s", tier, why))
	}
	return harness, bare, mapping, nil
}

// Worker discovery also checks provider readiness. Only an actual spawn
// refused for a provider can ask for background consent.
func (r *Runner) workerStartProviderGap(harness, provider string) string {
	why := r.workerProviderGap(harness, provider)
	if why != "" && provider == string(wscred.ProviderOpenAI) {
		requestPlanBackgroundConsent(r.opts.Backends[harness].Backend)
	}
	return why
}

func (r *Runner) workerProviderGap(harness, provider string) string {
	return workerProviderGap(r.opts.Backends, harness, provider)
}

func workerProviderGap(backends map[string]BackendRegistration, harness, provider string) string {
	if setup, ok := backends[harness].Backend.(workerSetup); ok {
		return setup.workerProviderGap(provider)
	}
	return ""
}

// noTierSlotError is a harness with no agents.tier model at a size.
type noTierSlotError struct{ harness, size string }

// Retained name check: stored tiers can still name Pi (TestRetiredPiTierRedirectsToWerk).
func (e noTierSlotError) Error() string {
	if e.harness == BackendWerk {
		return fmt.Sprintf("werk.%s has no usable model; set one with config.tune key \"agents.tier\" (harness \"werk\", tier \"%s\", and an exact model ID for its provider from the dashboard model picker)", e.size, e.size)
	}
	return fmt.Sprintf("%s.%s has no model in agents.tier (config.tune key \"agents.tier\" sets one)", e.harness, e.size)
}

// harnessSlot is the agents.tier model agent.spawn launches for harness at
// bare: the harness's own slot, with no cross-harness fallback.
func (r *TierBindingResolver) harnessSlot(harness, bare string) (wsconfig.AgentTier, error) {
	tiers, err := r.tierBindings(harness, bare)
	if err != nil {
		return wsconfig.AgentTier{}, fmt.Errorf("read agents.tier: %w", err)
	}
	mapping, ok := tiers[bare]
	if !ok {
		return mapping, noTierSlotError{harness, bare}
	}
	return mapping, nil
}

// Routed returns the binding agent.spawn launches for a routing-graph
// resolution, before harness availability: a bundle's own fields, else the
// resolved harness's agents.tier slot at the resolved size. It never falls
// back to another harness's slot, so a surface that reports it (render,
// config.resolve_agent) names what the spawn uses or fails as it does.
// Retained name check: stored tiers can still name Pi (TestRetiredPiTierRedirectsToWerk).
func (r *TierBindingResolver) Routed(res wsconfig.TierResolution) (wsconfig.AgentTier, error) {
	harness := TierRuntimeHarness(res.Harness)
	if res.Bundle != "" {
		if res.Harness == BackendPi && !r.hasExactModel(res.Model) {
			return wsconfig.AgentTier{}, fmt.Errorf("bundle %q needs an exact OpenRouter model ID for werk; update its model in agent.tier_routing", res.Bundle)
		}
		return wsconfig.AgentTier{Backend: harness, Provider: res.Provider, Model: res.Model, Effort: res.Effort}, nil
	}
	return r.harnessSlot(harness, res.Size)
}

// SpawnableHarness reports why agent.spawn cannot launch harness now for a
// caller verified on callerBackend ("" when it can): the availability check
// a routed tier's terminal harness passes. callerBackend "" is a caller with
// no verified binding.
func (r *Runner) SpawnableHarness(callerBackend, harness string) string {
	return r.spawnableHarness(callerBackend, harness)
}

// CallerRefusal is admitWorker's text for a worker on harness and a caller
// verified on callerBackend ("" is none): why the harness refuses that
// caller across harnesses, "" when it admits it. SpawnableHarness includes
// it; a surface that must not fall back past a refusal asks it alone.
func (r *Runner) CallerRefusal(callerBackend, harness string) string {
	if err := r.admitWorker(harness, callerBackend); err != nil {
		return err.Error()
	}
	return ""
}

// CallerBackend is the backend of the open conversation a ws-mcp connection
// is authenticated as, read from the runner's memory alone (no record file):
// conversation is the binding a hosted session token resolved to
// (wsdashboard.ProxyHello.Conversation), capability the worker capability
// token a launcher connection's environment carries (issueToken). A bound
// conversation decides alone; "" is an unbound connection, or one whose
// conversation is closed or unknown here. It is the verified caller backend
// (AgentCaller.VerifiedBackend) of an MCP server's admission decisions.
func (r *Runner) CallerBackend(conversation, capability string) string {
	var c *conv
	if conversation != "" {
		r.mu.Lock()
		c = r.convs[conversation]
		r.mu.Unlock()
	} else if holder, err := r.tokenHolder(capability); err == nil {
		c = holder
	}
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ""
	}
	return c.rec.Backend
}

// HarnessAvailability serves interactive views without probing a subprocess.
func (r *Runner) HarnessAvailability(caller, harness string) (why string, known bool) {
	return r.registeredWorker(caller, harness), true
}

// tierRefusal is a 400 naming why and every tier caller may spawn now.
func (r *Runner) tierRefusal(caller AgentCaller, resolver *TierBindingResolver, why string) error {
	valid := []string{}
	if list, err := r.agentTiers(caller, resolver); err == nil {
		for _, e := range list.Native {
			valid = append(valid, e.Tier)
		}
		for _, e := range list.Ext {
			valid = append(valid, e.Tier)
		}
		for _, a := range list.Aliases {
			if a.Error == "" {
				valid = append(valid, a.Alias)
			}
		}
		if list.Routing != nil {
			for _, b := range list.Routing.Bundles {
				if b.Error == "" {
					valid = append(valid, b.Tier)
				}
			}
		}
	}
	if len(valid) == 0 {
		return badRequest("agent.spawn: %s; no tier is spawnable now (agent.tiers says why)", why)
	}
	return badRequest("agent.spawn: %s; valid tiers: %s", why, strings.Join(valid, ", "))
}

// admissionRefusal is a process start refused because the conversation's
// authority is above its parent's current level: its input stays, and it
// stays asleep.
