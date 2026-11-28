// runner_create.go holds conversation creation and backend resolution.
package wsagent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsstate"
)

type requestError struct {
	status int
	msg    string
}

func (e *requestError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &requestError{status: http.StatusBadRequest, msg: fmt.Sprintf(format, args...)}
}

var errUnknown = &requestError{status: http.StatusNotFound, msg: "unknown agent conversation"}
var errClosed = &requestError{status: http.StatusConflict, msg: "the conversation is closed"}
var errHandedOff = &requestError{status: http.StatusConflict, msg: "the session was handed to a terminal: it runs there, not in the dashboard"}

// endedError is the refusal of a write to a conversation no loop serves.
var errPiRetired = &requestError{status: http.StatusConflict, msg: "the Pi harness is retired; its sessions are available as history"}

// Retained name check: legacy profile and record backend (TestRetiredPiProfileRemainsReadable).
func endedError(rec Record) error {
	if rec.Backend == BackendPi {
		return errPiRetired
	}
	if rec.ClosedAt == "" && rec.handedOff() {
		return errHandedOff
	}
	return errClosed
}

// LeadOptions are a lead conversation's spawn choices; an empty one takes
// the profile's value (the harness default when it has none).
type LeadOptions struct {
	// HireRequestID and HireLabel are set only by CreateHireLead, never HTTP.
	HireRequestID string
	HireLabel     string
	// CompanionRole and CompanionSystemPrompt are set only by internal daemon
	// companion session launch ("companion-body" or "companion-work"), never HTTP.
	CompanionRole         string
	CompanionSystemPrompt string
	Model           string
	Provider        string
	Effort          string
	PermissionMode  string
	PluginIsolation string
	// AccountSlotID selects the harness account slot for the new lead (empty
	// uses the harness's active default slot). Additive T0 field.
	AccountSlotID string
	// ConvHistoryAccess opts the new lead into conv.* MCP tools. Additive T0.
	ConvHistoryAccess bool
	// Common intent is transient; selection is stored separately from Record.
	PermissionLevel        PermissionLevel
	PermissionFullAck      bool
	permissionUser         bool // set by the trusted control owner, never request JSON
	DashboardControlAccess bool
}

// leadPluginIsolation supplies the additive field's legacy default.
func leadPluginIsolation(value string) string {
	if value == "" {
		return "default"
	}
	return value
}

// Create opens a conversation of profile bound to root. It resolves and
// checks the profile (backend, instruction file) now, so a bad launch fails
// here; the process itself starts on the first send. A lead profile is
// refused: its conversations open through CreateLead, behind the trusted
// gate.
func (r *Runner) Create(profile, root string) (State, error) {
	return r.create(profile, root, nil, nil)
}

// CreateLead opens a hosted lead session of harness backend on root, with
// the builtin lead profile of that harness (or the user's definition of the
// same name). The caller has passed the trusted gate.
func (r *Runner) CreateLead(backend, root string, opts LeadOptions) (State, error) {
	profile := LeadProfileFor(strings.TrimSpace(backend), r.opts.Backends)
	if profile == "" {
		return State{}, categorized(ErrInvalidProfile, badRequest("harness must be one of %s; got %q", strings.Join(r.leadHarnesses(), ", "), backend))
	}
	if opts.Model != "" && !modelPattern.MatchString(opts.Model) {
		return State{}, categorized(ErrInvalidProfile, badRequest("model %q must match %s", opts.Model, modelPattern.String()))
	}
	if opts.Effort != "" && !effortPattern.MatchString(opts.Effort) {
		return State{}, categorized(ErrInvalidProfile, badRequest("effort %q must be a lowercase word", opts.Effort))
	}
	modes := permissionModes(strings.TrimSpace(backend), []map[string]BackendRegistration{r.opts.Backends})
	if opts.PermissionMode != "" && !slices.Contains(modes, opts.PermissionMode) {
		return State{}, categorized(ErrInvalidProfile, badRequest("permission_mode %q must be one of %s", opts.PermissionMode, strings.Join(modes, ", ")))
	}
	opts.PluginIsolation = leadPluginIsolation(opts.PluginIsolation)
	if opts.PluginIsolation != "default" && opts.PluginIsolation != "isolated" {
		return State{}, categorized(ErrInvalidProfile, badRequest("plugin_isolation must be default or isolated; got %q", opts.PluginIsolation))
	}
	if opts.AccountSlotID != "" {
		slotID := wscred.NormalizeAccountSlotID(opts.AccountSlotID)
		if !wscred.ValidAccountSlotID(slotID) {
			return State{}, categorized(ErrInvalidProfile, badRequest("account_slot_id %q is invalid", opts.AccountSlotID))
		}
		if r.opts.AccountSlots != nil && wscred.SupportedSlotHarness(strings.TrimSpace(backend)) {
			if _, ok := r.opts.AccountSlots.ResolveSlot(strings.TrimSpace(backend), slotID); !ok {
				return State{}, categorized(ErrInvalidProfile, badRequest("account_slot_id %q is not registered for %s", opts.AccountSlotID, strings.TrimSpace(backend)))
			}
		}
	}
	return r.create(profile, root, &opts, nil)
}

// create opens a conversation; lead is a lead's spawn choices, spawn a
// spawned conversation's (Runner.Spawn), at most one of them set.
func (r *Runner) create(profile, root string, lead *LeadOptions, spawn *SpawnSpec) (State, error) {
	root = strings.TrimSpace(root)
	if !filepath.IsAbs(root) {
		return State{}, badRequest("root must be an absolute path; got %q", root)
	}
	root = filepath.Clean(root)
	if info, err := wsstate.Stat(root); err != nil || !info.IsDir() {
		return State{}, badRequest("root %s is not a directory", root)
	}
	// For direct Runner.Spawn with a parent (not already width-reserved by
	// AgentSpawn), reserve the parent's in-flight spawn slot before taking
	// worktree reservations or holdParentAdmission (selection.admission.RLock):
	// waiting on <-r.expiring[parent] while holding selection.admission.RLock
	// would deadlock against a concurrent SetPermissionLevel on the expiring
	// parent's loop.
	var releaseDirectParent func()
	var claimParent func()
	if spawn != nil && spawn.Parent != nil && spawn.claimWidth == nil {
		claim, release, err := r.reserveParentSpawn(*spawn.Parent)
		if err != nil {
			return State{}, err
		}
		claimParent = claim
		releaseDirectParent = release
		defer releaseDirectParent()
	}
	// Every path that opens a conversation (agent.spawn, pi.spawn, a lead)
	// holds the worktree's shared release reservation until the conversation
	// is registered, so worktree.release cannot check for bound children in
	// between and then reset and clean under it. It lets go once the
	// conversation is registered, where release counts it: a spawn's first
	// send must not use up release's wait for the lock.
	boundCwd := ""
	if spawn != nil {
		boundCwd = spawn.Cwd
	}
	unlockBound, err := reserveBoundWorktrees(root, boundCwd)
	if err != nil {
		return State{}, boundReservationRefusal(err)
	}
	unlockBound = sync.OnceFunc(unlockBound)
	defer unlockBound()
	rp, err := r.resolve(profile, root)
	if err != nil {
		return State{}, err
	}
	if rp.Definition.Lead != (lead != nil) {
		if lead == nil {
			return State{}, &requestError{status: http.StatusForbidden, msg: fmt.Sprintf("agent profile %q is a lead profile: a lead session is spawned from the dashboard on this machine", rp.Name)}
		}
		return State{}, badRequest("agent profile %q is not a lead profile (lead: true)", rp.Name)
	}
	spawnerBackend := ""
	if spawn != nil {
		spawnerBackend = spawn.spawnerBackend
	}
	if lead != nil {
		effectiveProvider := rp.Definition.Provider
		if lead.Provider != "" {
			effectiveProvider = lead.Provider
		}
		effectiveModel := rp.Definition.Model
		if lead.Model != "" {
			effectiveModel = lead.Model
		}
		if choice, ok := r.opts.Backends[rp.Definition.Backend].Backend.(leadProviderAdmitter); ok {
			if err := choice.admitLeadProvider(effectiveProvider, effectiveModel); err != nil {
				return State{}, categorized(ErrInvalidProfile, err)
			}
		} else if effectiveProvider != "" {
			return State{}, badRequest("this harness does not take a provider selection")
		}
	}
	if lead == nil {
		// Every worker creation, profile-based or spawned, passes the
		// cross-harness check before anything is minted or written: a
		// spawn for its caller's verified backend, a dashboard or REST
		// creation for none.
		if err := r.admitWorker(rp.Definition.Backend, spawnerBackend); err != nil {
			return State{}, &requestError{status: http.StatusForbidden, msg: err.Error()}
		}
		effectiveProvider := rp.Definition.Provider
		if spawn != nil && spawn.provider != "" {
			effectiveProvider = spawn.provider
		}
		if why := r.workerStartProviderGap(rp.Definition.Backend, effectiveProvider); why != "" {
			return State{}, badRequest("%s", why)
		}
	}
	if spawn != nil {
		releaseAdmission := r.holdParentAdmission(spawn.Parent)
		defer releaseAdmission()
		level, source, err := resolveWorkerPermission(rp.Definition, spawn.PermissionLevel, spawn.PermissionLevel != "", nil)
		if err != nil {
			return State{}, err
		}
		level = r.legacyDetachedWorkerLevel(spawn.Parent, level, source)
		if spawn.PermissionLevel != "" && !spawn.delegatedAuthority {
			current := legacyWorkerPermission(rp.Definition)
			if current.rank() == 0 {
				return State{}, badRequest("the worker's current permission level is unresolved")
			}
			if err := authorizePermission(current, level, spawn.permissionUser, spawn.PermissionFullAck); err != nil {
				return State{}, err
			}
		}
		if err := r.admitWorkerLevel(spawn.Parent, level, spawn.delegatedAuthority || source == "legacy-derived"); err != nil {
			return State{}, err
		}
	}
	if lead != nil && lead.PermissionLevel != "" {
		base := Record{Lead: true, Backend: rp.Definition.Backend, PermissionMode: rp.Definition.PermissionMode}
		if lead.PermissionMode != "" {
			base.PermissionMode = lead.PermissionMode
		}
		if !r.supportsLeadPermission(base.Backend, lead.PermissionLevel) {
			return State{}, badRequest("permission_level %q is not supported on %s", lead.PermissionLevel, base.Backend)
		}
		if err := authorizePermission(r.legacyLeadPermission(base), lead.PermissionLevel, lead.permissionUser, lead.PermissionFullAck); err != nil {
			return State{}, err
		}
	}
	releaseDurableCreation := func() {}
	if r.durableProcess(rp.Definition.Backend) {
		release, err := r.admitDurableTransition()
		if err != nil {
			return State{}, err
		}
		releaseDurableCreation = sync.OnceFunc(release)
		defer releaseDurableCreation()
	}
	var key string
	if lead != nil && lead.CompanionRole != "" {
		cks, ok := r.opts.LeadKeys.(CompanionLeadKeyStore)
		if !ok {
			return State{}, badRequest("companion lead sessions require a companion-capable key store")
		}
		if gitRoot, gitErr := r.opts.LeadKeys.LeadRoot(root); gitErr == nil && gitRoot != "" {
			root = gitRoot
		}
		if key, err = cks.MintCompanionLead(root, lead.CompanionRole); err != nil {
			return State{}, fmt.Errorf("mint the companion session key: %w", err)
		}
	} else if lead != nil {
		// A lead key lives at the canonical git root, which is then the
		// session's root: its registration and session record must agree.
		if root, err = r.opts.LeadKeys.LeadRoot(root); err != nil {
			return State{}, badRequest("a lead session needs a git worktree: %v", err)
		}
		if key, err = r.opts.LeadKeys.MintLead(root); err != nil {
			return State{}, fmt.Errorf("mint the lead session key: %w", err)
		}
	} else {
		if child, ok := r.opts.Keys.(ChildKeyStore); ok && spawn != nil && spawn.policyKey != "" {
			key, err = child.MintChild(root, spawn.policyKey)
		} else {
			key, err = r.opts.Keys.Mint(root)
		}
		if err != nil {
			return State{}, fmt.Errorf("mint ws session key: %w", err)
		}
	}
	now := r.opts.Now()
	rec := Record{
		ReplyID:   wsmailbox.ReplyID(key),
		Profile:   rp.Name,
		Backend:   rp.Definition.Backend,
		Provider:  rp.Definition.Provider,
		Root:      root,
		Model:     rp.Definition.Model,
		Effort:    rp.Definition.Effort,
		WSKey:     key,
		Listed:    rp.Definition.ListInIndex,
		CreatedAt: now.UTC().Format(time.RFC3339),
		// The same instant at nanosecond precision, for the release
		// boundary.
		CreatedUnixNano: now.UnixNano(),
	}
	if lead == nil {
		rec.SpawnerBackend = spawnerBackend
	}
	if lead != nil {
		rec.Lead = true
		rec.ConvHistoryAccess = lead.ConvHistoryAccess
		rec.DashboardControlAccess = lead.DashboardControlAccess
		if lead.CompanionRole != "" {
			rec.Listed = false
			rec.ConvHistoryAccess = false
			rec.DashboardControlAccess = false
			rec.CompanionRole = lead.CompanionRole
			rec.CompanionSystemPrompt = lead.CompanionSystemPrompt
		}
		if lead.Provider != "" {
			rec.Provider = lead.Provider
		}
		rec.PluginIsolation = leadPluginIsolation(lead.PluginIsolation)
		rec.PermissionMode = rp.Definition.PermissionMode
		if lead.Model != "" {
			rec.Model = lead.Model
		}
		if lead.Effort != "" {
			rec.Effort = lead.Effort
		}
		if lead.PermissionMode != "" {
			rec.PermissionMode = lead.PermissionMode
		}
		if len(r.opts.Backends[rec.Backend].PermissionModes) == 0 {
			rec.PermissionMode = ""
		}
		rec.selectPermissionMode(rec.PermissionMode)
		if lead.AccountSlotID != "" {
			rec.AccountSlotID = storedAccountSlotID(lead.AccountSlotID)
		} else if r.opts.AccountSlots != nil && wscred.SupportedSlotHarness(rec.Backend) {
			rec.AccountSlotID = storedAccountSlotID(r.opts.AccountSlots.ActiveSlot(rec.Backend).ID)
		}
	} else if r.opts.AccountSlots != nil && wscred.SupportedSlotHarness(rec.Backend) {
		rec.AccountSlotID = storedAccountSlotID(r.opts.AccountSlots.ActiveSlot(rec.Backend).ID)
	}
	if spawn != nil {
		spawn.apply(&rec)
	}
	if !wsdashboard.ValidReplyID(rec.ReplyID) {
		return State{}, fmt.Errorf("minted key gives an invalid reply-id %q", rec.ReplyID)
	}
	dir := wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return State{}, err
	}
	if lead != nil && lead.PermissionLevel != "" {
		if err := r.writePermissionSelection(rec.ReplyID, dir, lead.PermissionLevel); err != nil {
			return State{}, err
		}
		if mode := r.permissionPolicy(rec.Backend).nativeMode(lead.PermissionLevel); mode != "" {
			rec.PermissionMode = mode
		}
	}
	if spawn != nil && spawn.PermissionLevel != "" {
		writer := r.writeGenericPermissionSelection
		if spawn.delegatedAuthority {
			writer = r.writeDelegatedPermissionSelection
		}
		if err := writer(rec.ReplyID, dir, spawn.PermissionLevel); err != nil {
			return State{}, err
		}
	}
	if spawn != nil && spawn.SystemPrompt != "" {
		if err := writeFileAtomic(filepath.Join(dir, systemPromptFile), []byte(spawn.SystemPrompt), 0o600); err != nil {
			return State{}, err
		}
	}
	if lead != nil && lead.HireRequestID != "" {
		if err := r.markHired(rec.ReplyID, lead.HireRequestID, lead.HireLabel); err != nil {
			return State{}, err
		}
	}
	if err := r.persistAgentRecord(dir, rec); err != nil {
		return State{}, err
	}
	var seed []wslive.SlashCommand
	if rec.Lead && (rec.CompanionRole == "" || rec.CompanionRole == companionRoleWork) {
		// A new lead (including the Mobile Full Lead companion-work Brain)
		// offers its playbook commands before its process first reports the
		// harness's own.
		seed = r.withPlaybookCommands(rec, r.lastCommands(rec))
	}
	var claimWidth func()
	spawnPending := false
	if spawn != nil {
		claimWidth = spawn.claimWidth
		spawnPending = claimWidth != nil
	}
	if claimWidth == nil {
		claimWidth = claimParent
	}
	c := r.startConvWith(rec, seed, spawnPending, claimWidth, nil)
	releaseDurableCreation()
	unlockBound()
	if spawn != nil {
		return r.spawned(c, spawn.Prompt)
	}
	return c.state(), nil
}

// lastCommands is the command list in the persisted snapshot of the
// newest other hosted lead session of rec's root and profile that has
// one; nil when none has. A new lead's process starts only at its first
// send, and the composer offers commands only from the snapshot's list,
// so without this an empty session would offer none until then.
func (r *Runner) lastCommands(rec Record) []wslive.SlashCommand {
	r.mu.Lock()
	recs := make([]Record, 0, len(r.convs)+len(r.closed))
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	for _, prev := range r.closed {
		recs = append(recs, prev)
	}
	r.mu.Unlock()
	for _, c := range convs {
		recs = append(recs, c.record())
	}
	recs = slices.DeleteFunc(recs, func(prev Record) bool {
		return !prev.Lead || prev.ReplyID == rec.ReplyID || prev.Root != rec.Root || prev.Profile != rec.Profile || prev.Backend != rec.Backend
	})
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].CreatedAt != recs[j].CreatedAt {
			return recs[i].CreatedAt > recs[j].CreatedAt
		}
		return recs[i].ReplyID > recs[j].ReplyID
	})
	for _, prev := range recs {
		snap, err := wslive.ReadSnapshot(wslive.SnapshotPath(wsdashboard.SessionDir(r.opts.Dir, prev.ReplyID)))
		if err == nil && len(snap.Commands) > 0 {
			return snap.Commands
		}
	}
	return nil
}

func (r *Runner) mcpCommand(durable bool) func(string) (MCPCommand, error) {
	if durable && r.opts.DaemonMCP != nil {
		return r.opts.DaemonMCP
	}
	return r.opts.WSMCP
}

// resolve resolves profile through root and checks everything a launch
// needs: a backend this daemon has, an existing instruction file, and a
// ws-mcp command when the profile allows ws tools.
// Retained name check: legacy profile and record backend (TestRetiredPiProfileRemainsReadable).
func (r *Runner) resolve(profile, root string) (ResolvedProfile, error) {
	var rp ResolvedProfile
	var err error
	if r.fixedProfile != nil {
		if profile != r.fixedProfile.Name {
			return ResolvedProfile{}, badRequest("private answer runner profile mismatch")
		}
		rp = *r.fixedProfile
	} else {
		rp, err = ResolveProfile(r.opts.Config, root, strings.TrimSpace(profile), r.opts.Backends)
	}
	if err != nil {
		return ResolvedProfile{}, categorized(ErrInvalidProfile, badRequest("%v", err))
	}
	if rp.Definition.Backend == BackendPi {
		return ResolvedProfile{}, errPiRetired
	}
	if _, err := r.backend(rp.Name, rp.Definition.Backend); err != nil {
		return ResolvedProfile{}, err
	}
	if path := rp.Definition.InstructionsPath(root); path != "" {
		info, err := wsstate.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return ResolvedProfile{}, categorized(ErrInvalidProfile, badRequest("agent profile %q: instruction file %s does not exist", rp.Name, path))
		}
	}
	durable := r.durableProcess(rp.Definition.Backend)
	mcp := r.mcpCommand(durable)
	if len(rp.Definition.WSTools) > 0 && mcp == nil {
		return ResolvedProfile{}, categorized(ErrInvalidProfile, badRequest("agent profile %q allows ws tools, but this daemon cannot attach ws-mcp", rp.Name))
	}
	if !rp.Definition.Lead && mcp != nil && !slices.Contains(rp.Definition.WSTools, "compact") {
		rp.Definition.WSTools = append(slices.Clone(rp.Definition.WSTools), "compact")
	}
	if rp.Definition.Lead && durable && r.opts.LeadKeys == nil {
		return ResolvedProfile{}, categorized(ErrInvalidProfile, badRequest("agent profile %q requires a lead key store", rp.Name))
	}
	if rp.Definition.Lead && !durable && (r.opts.LeadKeys == nil || r.opts.Holders == nil) {
		return ResolvedProfile{}, categorized(ErrInvalidProfile, badRequest("agent profile %q is a lead profile, but this daemon runs no session holders", rp.Name))
	}
	return rp, nil
}

// backend is shared by profile validation and both launch paths. Keep
// unavailable backends explicit, including when rejoining a held process.
func (r *Runner) backend(profile, name string) (Backend, error) {
	backend := r.opts.Backends[name].Backend
	if backend == nil {
		return nil, categorized(ErrBackendUnavailable, badRequest("agent profile %q: backend %q is not available in this daemon", profile, name))
	}
	return backend, nil
}

func (c *conv) startBackend(spec StartSpec) (Process, error) {
	backend, err := c.r.backend(c.rec.Profile, c.rec.Backend)
	if err != nil {
		return nil, err
	}
	if c.r.lifecycle(c.rec.Backend).RestoreUsage &&
		(c.r.durableProcess(c.rec.Backend) || spec.Holder != nil && spec.Holder.id != "") {
		spec.restoredUsage = c.live.st.Snapshot(c.live.now().UnixMilli()).Usage
	}
	if sink := c.r.opts.AuditSink; sink != nil {
		class := wsaudit.SessionHostedChild
		if spec.Lead != nil {
			class = wsaudit.SessionHostedLead
		}
		spec.audit = &auditTap{Session: wsaudit.Session{ID: c.rec.ReplyID, Class: class}, Sink: sink, Log: c.r.opts.Log}
	}
	start := backend.Start
	if c.r.durableProcess(c.rec.Backend) {
		durable, ok := backend.(durableStarter)
		if !ok {
			return nil, categorized(ErrLaunchFailed, errors.New("the backend has no durable process"))
		}
		start = durable.startLoop
		spec.closedTurnID = c.rec.DurableClosedTurnID
		spec.recoverExec = c.r.recoverModelExec(c.rec.ReplyID, c.rec.OpenGen)
		spec.compactState = c.r.compactionState(c.rec.ReplyID)
		spec.sessionSecret = c.rec.WSKey
		spec.sessionConfig = c.sessionConfigProvider()
	}
	proc, err := start(context.Background(), spec)
	return proc, categorized(ErrLaunchFailed, err)
}

func (c *conv) sessionConfigProvider() func() (wsconfig.Options, wsconfig.SessionReader, string) {
	if c.r.fixedProfile != nil {
		// Private fixed-profile runners do not read layered session config.
		return nil
	}
	return func() (wsconfig.Options, wsconfig.SessionReader, string) {
		cfg := c.r.opts.Config
		cfg.RepoRoot = c.rec.Root
		if roots, ok := c.r.opts.Keys.(interface{ Root(string) (string, bool) }); ok {
			if root, found := roots.Root(c.rec.WSKey); found {
				cfg.RepoRoot = root
			}
		}
		reader, _ := c.r.opts.Keys.(wsconfig.SessionReader)
		return cfg, reader, c.rec.WSKey
	}
}

// Get reads one conversation's state.
