package wsagent

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
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
const systemPromptFile = "system_prompt.md"

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
	// Harness is the caller's own harness: a bare tier resolves there.
	Harness string
	// Config is where agents.tier and agent.tier_aliases are read.
	Config wsconfig.Options
	// holder is a token holder's own record (a nested spawn): its depth
	// and tier cap the child's. Nil for a lead.
	holder *Record
}

// LeadCaller is a lead session's agent.* caller: replyID is the lead's
// mailbox reply-id, the parent its children record.
func LeadCaller(replyID, root, harness string, cfg wsconfig.Options) AgentCaller {
	return AgentCaller{Parent: ParentRef{Kind: ParentLead, ReplyID: replyID}, Root: root, Harness: harness, Config: cfg}
}

// holderCaller is a token holder's agent.* caller.
func (r *Runner) holderCaller(rec Record) AgentCaller {
	cfg := r.opts.Config
	cfg.RepoRoot = rec.Root
	return AgentCaller{
		Parent:  ParentRef{Kind: ParentConversation, ReplyID: rec.ReplyID},
		Root:    rec.Root,
		WorkDir: rec.workDir(),
		Harness: rec.Backend,
		Config:  cfg,
		holder:  &rec,
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
	Tier   string `json:"tier"`
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
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
	Tier    string `json:"tier"`
	Trace   string `json:"trace,omitempty"`
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Error   string `json:"error,omitempty"`
}

// TierHarnesses are the harness names a tier route or bundle may name: the
// built-in harnesses and every registered backend. A known harness that is
// unavailable stays valid in config and reports at resolution.
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

// spawnableHarness reports why harness has no spawnable tier now: not
// registered, lead-only, or not installed. "" is spawnable.
func (r *Runner) spawnableHarness(harness string) string {
	if why := r.registeredWorker(harness); why != "" {
		return why
	}
	if harness == BackendPi && r.opts.ChildCheck != nil {
		if err := r.opts.ChildCheck(); err != nil {
			return err.Error()
		}
	}
	return ""
}

// registeredWorker is spawnableHarness before the child check: registration,
// the worker role, and the executable on PATH. It runs no subprocess.
func (r *Runner) registeredWorker(harness string) string {
	reg, ok := r.opts.Backends[harness]
	if !ok || reg.Backend == nil {
		return fmt.Sprintf("harness %q is not registered in this daemon", harness)
	}
	if reg.leadOnlyReason != "" {
		return reg.leadOnlyReason
	}
	if !reg.WorkerCapable || reg.WorkerProfile == "" {
		return fmt.Sprintf("harness %q is registered for hosted leads only, not for spawned workers", harness)
	}
	if ok, why := supportBackendAvailability(harness, reg.Backend); !ok {
		return why
	}
	return ""
}

// admitWorkerBackend is the one lead-only check every worker entry passes:
// agent.spawn and a profile create (create), and every launch or wake of an
// existing worker record (childAdmission). A rejoin does not attach such a
// worker either (reattach, holdersScanned). It refuses only a backend
// registered with a lead-only reason; the record itself is kept.
func (r *Runner) admitWorkerBackend(backend string) error {
	if reason := r.opts.Backends[backend].leadOnlyReason; reason != "" {
		return &admissionRefusal{reason: reason}
	}
	return nil
}

// workerHarnesses are the registered worker-role backends, sorted, with the
// lead-only backends whose registration says why (agent.tiers lists those
// under Unavailable).
func (r *Runner) workerHarnesses() []string {
	var out []string
	for name, reg := range r.opts.Backends {
		if reg.WorkerCapable && reg.WorkerProfile != "" || reg.leadOnlyReason != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// unusableHarnessError is an alias or route entry's error for a harness with
// no usable tiers: a lead-only registration's own reason, else the generic
// text.
func (r *Runner) unusableHarnessError(harness string) string {
	if reason := r.opts.Backends[harness].leadOnlyReason; reason != "" {
		return reason
	}
	return fmt.Sprintf("harness %q has no spawnable tier now", harness)
}

func tierEntries(prefix string, tiers map[string]wsconfig.AgentTier) []AgentTierEntry {
	var out []AgentTierEntry
	for _, t := range []string{"small", "medium", "large", "xlarge"} {
		if m, ok := tiers[t]; ok {
			out = append(out, AgentTierEntry{Tier: prefix + t, Model: m.Model, Effort: m.Effort})
		}
	}
	return out
}

// AgentTiers lists what caller may pass as agent.spawn's tier.
func (r *Runner) AgentTiers(caller AgentCaller) (AgentTiers, error) {
	out := AgentTiers{Harness: caller.Harness, Native: []AgentTierEntry{}, Ext: []AgentTierEntry{}, Aliases: []AgentAliasEntry{}}
	usable := map[string]map[string]wsconfig.AgentTier{}
	for _, h := range r.workerHarnesses() {
		if why := r.spawnableHarness(h); why != "" {
			out.Unavailable = append(out.Unavailable, h+": "+why)
			continue
		}
		tiers, err := wsconfig.HarnessTiers(caller.Config, h)
		if err != nil {
			return AgentTiers{}, fmt.Errorf("read agents.tier: %w", err)
		}
		usable[h] = tiers
		if h == caller.Harness {
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
		if tiers, ok := usable[h]; !ok {
			entry.Error = r.unusableHarnessError(h)
		} else if m, ok := tiers[t]; !ok {
			entry.Error = fmt.Sprintf("tier %q is not set for %s", t, h)
		} else {
			entry.Model, entry.Effort = m.Model, m.Effort
		}
		out.Aliases = append(out.Aliases, entry)
	}
	routing, err := r.agentRouting(caller, usable)
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
func (r *Runner) agentRouting(caller AgentCaller, usable map[string]map[string]wsconfig.AgentTier) (*AgentRouting, error) {
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
		e := AgentRouteEntry{Tier: token, Trace: res.Trace(), Harness: res.Harness}
		tiers, ok := usable[res.Harness]
		switch {
		case !ok:
			e.Error = r.unusableHarnessError(res.Harness)
		case res.Bundle != "":
			e.Model, e.Effort = res.Model, res.Effort
		default:
			if m, ok := tiers[res.Size]; ok {
				e.Model, e.Effort = m.Model, m.Effort
			} else {
				e.Error = fmt.Sprintf("tier %q is not set for %s", res.Size, res.Harness)
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
	raw := strings.ToLower(strings.TrimSpace(tier))
	if raw == "" {
		return "", "", mapping, r.tierRefusal(caller, "tier is required")
	}
	switch h, t, dotted := strings.Cut(raw, "."); {
	case dotted:
		harness, bare = h, wsconfig.NormalizedTier(t)
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
				return "", "", mapping, r.tierRefusal(caller, rerr.Error())
			}
			if res.Bundle != "" {
				if why := r.spawnableHarness(res.Harness); why != "" {
					return "", "", mapping, r.tierRefusal(caller, fmt.Sprintf("tier %q (%s): %s", tier, res.Trace(), why))
				}
				return res.Harness, res.Rank(), wsconfig.AgentTier{Backend: res.Harness, Model: res.Model, Effort: res.Effort}, nil
			}
			harness, bare = res.Harness, res.Size
			break
		}
		h, t, _ := strings.Cut(target, ".")
		harness, bare = h, t
	}
	if bare == "" {
		return "", "", mapping, r.tierRefusal(caller, fmt.Sprintf("tier %q names no tier (small, medium, large, xlarge)", tier))
	}
	if why := r.spawnableHarness(harness); why != "" {
		return "", "", mapping, r.tierRefusal(caller, fmt.Sprintf("tier %q: %s", tier, why))
	}
	mapping, err = harnessSlot(caller.Config, harness, bare)
	var noSlot noTierSlotError
	if errors.As(err, &noSlot) {
		return "", "", mapping, r.tierRefusal(caller, fmt.Sprintf("tier %q: %v", tier, err))
	}
	if err != nil {
		return "", "", mapping, err
	}
	return harness, bare, mapping, nil
}

// noTierSlotError is a harness with no agents.tier model at a size.
type noTierSlotError struct{ harness, size string }

func (e noTierSlotError) Error() string {
	return fmt.Sprintf("%s.%s has no model in agents.tier (config.tune key \"agents.tier\" sets one)", e.harness, e.size)
}

// harnessSlot is the agents.tier model agent.spawn launches for harness at
// bare: the harness's own slot, with no cross-harness fallback.
func harnessSlot(cfg wsconfig.Options, harness, bare string) (wsconfig.AgentTier, error) {
	tiers, err := wsconfig.HarnessTiers(cfg, harness)
	if err != nil {
		return wsconfig.AgentTier{}, fmt.Errorf("read agents.tier: %w", err)
	}
	mapping, ok := tiers[bare]
	if !ok {
		return mapping, noTierSlotError{harness, bare}
	}
	return mapping, nil
}

// RoutedTierBinding is the binding agent.spawn launches for a routing-graph
// resolution, before harness availability: a bundle's own fields, else the
// resolved harness's agents.tier slot at the resolved size. It never falls
// back to another harness's slot, so a surface that reports it (render,
// config.resolve_agent) names what the spawn uses or fails as it does.
func RoutedTierBinding(cfg wsconfig.Options, res wsconfig.TierResolution) (wsconfig.AgentTier, error) {
	if res.Bundle != "" {
		return wsconfig.AgentTier{Backend: res.Harness, Model: res.Model, Effort: res.Effort}, nil
	}
	return harnessSlot(cfg, res.Harness, res.Size)
}

// SpawnableHarness reports why agent.spawn cannot launch harness now ("" when
// it can): the availability check a routed tier's terminal harness passes.
func (r *Runner) SpawnableHarness(harness string) string { return r.spawnableHarness(harness) }

// HarnessAvailability is SpawnableHarness for an interactive view: it never
// runs a probe subprocess. The child check (Pi's version) answers from
// ChildCheckKnown; while that needs a probe, or no ChildCheckKnown is set,
// known is false and why is empty: availability is not known yet.
func (r *Runner) HarnessAvailability(harness string) (why string, known bool) {
	if why := r.registeredWorker(harness); why != "" {
		return why, true
	}
	if harness != BackendPi || r.opts.ChildCheck == nil {
		return "", true
	}
	if r.opts.ChildCheckKnown == nil {
		return "", false
	}
	known, err := r.opts.ChildCheckKnown()
	switch {
	case !known:
		return "", false
	case err != nil:
		return err.Error(), true
	}
	return "", true
}

// tierRefusal is a 400 naming why and every tier caller may spawn now.
func (r *Runner) tierRefusal(caller AgentCaller, why string) error {
	valid := []string{}
	if list, err := r.AgentTiers(caller); err == nil {
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
