package wsagent

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/wslive"
)

// HarnessSwitchSpec selects the replacement harness and optional model settings
// for an in-place cross-harness session switch (clear + checkpoint handoff).
// Omitted Model, Effort, or Provider resolve to the target lead profile's
// default; source-harness values are never carried over.
type HarnessSwitchSpec struct {
	Backend  string `json:"harness"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// Typed errors returned by SwitchHarness and SwitchCompanionHarness.
var (
	ErrHarnessUnsupported = &requestError{status: http.StatusBadRequest, msg: "harness_unsupported"}
	ErrHarnessUnchanged   = &requestError{status: http.StatusBadRequest, msg: "harness_unchanged"}
	ErrConversationBusy   = &requestError{status: http.StatusConflict, msg: "conversation_busy"}
)

type harnessTarget struct {
	profile  string
	backend  string
	model    string
	effort   string
	provider string
	resolved ResolvedProfile
}

// SwitchHarness switches hosted lead conversation id to spec's target harness
// in place, preserving durable ws identity (WSKey, ReplyID, mailbox, agenda,
// todos, children, and conversation ring) and handing off a ring checkpoint.
// It represents the trusted dashboard user control path.
func (r *Runner) SwitchHarness(ctx context.Context, id string, spec HarnessSwitchSpec) error {
	_, err := r.switchHarness(ctx, id, spec, true)
	return err
}

func (r *Runner) switchHarness(ctx context.Context, id string, spec HarnessSwitchSpec, user bool) (State, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return State{}, err
		}
	}
	return r.act(id, func(c *conv) error {
		return c.switchHarness(ctx, spec, user)
	})
}

func (c *conv) switchHarness(ctx context.Context, spec HarnessSwitchSpec, user bool) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	rec := c.record()
	if c.closed || rec.ClosedAt != "" || rec.handedOff() {
		return endedError(rec)
	}
	if c.isForeign() {
		return errHandedOff
	}
	if !rec.Lead || c.r.durableProcess(rec.Backend) {
		return ErrHarnessUnsupported
	}
	if c.switchBusy(rec) {
		return ErrConversationBusy
	}

	targetBackend := strings.TrimSpace(spec.Backend)
	if targetBackend == rec.Backend {
		return ErrHarnessUnchanged
	}
	if targetBackend == "" || c.r.durableProcess(targetBackend) {
		return ErrHarnessUnsupported
	}
	targetProfile := LeadProfileFor(targetBackend, c.r.opts.Backends)
	if targetProfile == "" {
		return ErrHarnessUnsupported
	}
	rp, err := c.r.resolve(targetProfile, rec.Root)
	if err != nil {
		if errors.Is(err, errPiRetired) || errors.Is(err, ErrBackendUnavailable) {
			return ErrHarnessUnsupported
		}
		return err
	}
	if rp.Definition.Backend != targetBackend || !rp.Definition.Lead || c.r.durableProcess(rp.Definition.Backend) {
		return ErrHarnessUnsupported
	}
	if rec.CompanionRole != "" {
		if why := c.r.companionHarnessGap(targetBackend); why != "" {
			return ErrHarnessUnsupported
		}
	}

	// Pre-flight model/effort/provider validation against the target backend
	// before retiring the running process.
	if spec.Model != "" && !modelPattern.MatchString(spec.Model) {
		return badRequest("model %q must match %s", spec.Model, modelPattern.String())
	}
	if spec.Effort != "" && !effortPattern.MatchString(spec.Effort) {
		return badRequest("effort %q must be a lowercase word", spec.Effort)
	}
	effectiveModel := rp.Definition.Model
	if spec.Model != "" {
		effectiveModel = spec.Model
	}
	effectiveEffort := rp.Definition.Effort
	if spec.Effort != "" {
		effectiveEffort = spec.Effort
	}
	effectiveProvider := rp.Definition.Provider
	if spec.Provider != "" {
		effectiveProvider = spec.Provider
	}
	if admitter, ok := c.r.opts.Backends[targetBackend].Backend.(leadProviderAdmitter); ok {
		if err := admitter.admitLeadProvider(effectiveProvider, effectiveModel); err != nil {
			var re *requestError
			if !errors.As(err, &re) {
				err = badRequest("%v", err)
			}
			return categorized(ErrInvalidProfile, err)
		}
	} else if effectiveProvider != "" {
		return badRequest("this harness does not take a provider selection")
	}

	// Pre-flight permission check before retiring the running process:
	// - Explicit selection carries over unchanged and must be supported on target.
	// - No explicit selection: compare source effective level with target's
	//   legacy-derived level (rank-only; raises require user authority).
	selection := c.r.permissionSlot(rec.ReplyID, c.dir)
	selection.admission.Lock()
	explicitLevel, explicit, permErr := c.r.refreshPermissionSelection(rec.ReplyID, c.dir)
	selection.admission.Unlock()
	if permErr != nil {
		return permErr
	}
	if explicit {
		if !c.r.supportsLeadPermission(targetBackend, explicitLevel) {
			return badRequest("permission_level %q is not supported on %s", explicitLevel, targetBackend)
		}
	} else {
		sourceLevel := c.r.projectLiveMode(c.r.legacyLeadPermission(rec), rec, "legacy-derived", c.activeMode, c.proc != nil)
		targetLevel := c.r.legacyLeadPermission(Record{Lead: true, Backend: targetBackend})
		if !user && mayRaisePermission(sourceLevel, targetLevel) {
			return &requestError{status: http.StatusForbidden, msg: "only the user may raise permission_level"}
		}
	}

	queuedCompact := c.compactPending
	summary := emptyWorkerSummary()
	if queuedCompact != nil && queuedCompact.lead != nil {
		summary = queuedCompact.lead
	}
	req := &compactRequest{
		resume:      false,
		triggerPath: "harness_switch",
		lead:        summary,
		target: &harnessTarget{
			profile:  rp.Name,
			backend:  targetBackend,
			model:    effectiveModel,
			effort:   effectiveEffort,
			provider: effectiveProvider,
			resolved: rp,
		},
	}

	rotated, err := c.rotateExternalSession(req, wslive.TriggerManual)
	if err != nil {
		if c.compacting {
			c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
			c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed, Text: err.Error()})
		}
		c.publishCompaction(false)
		return err
	}
	if !rotated {
		c.publishCompaction(false)
		return errors.New("harness switch rotation did not complete")
	}
	if c.compactPending == queuedCompact {
		c.compactPending = nil
	}
	c.publishCompaction(false)
	return nil
}

// mayRaisePermission reports whether switching from sourceLevel to targetLevel
// without user authority could raise the session's permission level.
// An unresolved ("") level cannot be proven equal-or-lower unless the source is
// already at PermissionFull or the target is PermissionReadOnly.
func mayRaisePermission(sourceLevel, targetLevel PermissionLevel) bool {
	if sourceLevel == PermissionFull || targetLevel == PermissionReadOnly {
		return false
	}
	if sourceLevel == "" || targetLevel == "" {
		return true
	}
	return targetLevel.rank() > sourceLevel.rank()
}

func (c *conv) switchBusy(rec Record) bool {
	if c.turn || c.compacting || c.compactCommand {
		return true
	}
	if c.quotaCancelFence.Load() == quotaFenceFiring {
		return true
	}
	if rec.QuotaContinuation != nil && rec.QuotaContinuation.Status == QuotaContinuationInFlight {
		return true
	}
	if rec.DurableTurn != nil {
		return true
	}
	return false
}

type harnessSwitchState struct {
	prevRec                   Record
	hadQuotaContinuation      bool
	hadRetiredHostState       bool
	hadHostAlert              bool
	prevRecordDirty           bool
	prevFailure               *wslive.Notice
	prevMCPBroken             []string
	prevCaps                  *Capabilities
	prevCommands              []wslive.SlashCommand
	prevActiveMode            string
	prevDashboardControl      bool
	prevUsageLimitSettled     bool
	prevExhausted             map[string]wslive.Notice
	prevExhaustReport         string
	prevExhaustNotice         wslive.Notice
	prevParkedReports         int
	prevQuotaAlerted          bool
	prevQuotaHandled          bool
	prevAccountSlotBoundary   bool
	prevSlot2aVerify          bool
	prevSlot2aFallback        bool
	prevDirtyModel            bool
	prevDirtyEffort           bool
	prevDirtyMode             bool
	prevDirtyPermission       bool
	prevReconcileMode         bool
	prevAgyLogNoted           bool
	prevLastErr               string
	prevUnrequestedCmdWarn    string
	prevActionNeeded          string
}

// beginHarnessSwitch snapshots prior record and loop state, resets harness-bound
// loop state in memory, defers record persistence until the single atomic commit,
// and applies target fields to c.rec in memory before startLead launches.
func (c *conv) beginHarnessSwitch(target *harnessTarget) *harnessSwitchState {
	prevRec := c.record()
	var prevFailure *wslive.Notice
	if c.failure != nil {
		f := *c.failure
		prevFailure = &f
	}
	hadRetiredHostState := c.bgTasks != 0 || c.background != nil || c.monitors != nil || c.retry != nil || c.alerted
	s := &harnessSwitchState{
		prevRec:                 prevRec,
		hadQuotaContinuation:    prevRec.QuotaContinuation != nil,
		hadRetiredHostState:     hadRetiredHostState,
		hadHostAlert:            prevRec.QuotaContinuation != nil || c.failure != nil || len(c.mcpBroken) > 0 || hadRetiredHostState,
		prevRecordDirty:         c.recordDirty,
		prevFailure:             prevFailure,
		prevMCPBroken:           slices.Clone(c.mcpBroken),
		prevCaps:                c.caps,
		prevCommands:            c.commands,
		prevActiveMode:          c.activeMode,
		prevDashboardControl:    c.dashboardControlEffective,
		prevUsageLimitSettled:   c.usageLimitSettled,
		prevExhausted:           c.exhausted,
		prevExhaustReport:       c.exhaustReport,
		prevExhaustNotice:       c.exhaustNotice,
		prevParkedReports:       c.parkedReports,
		prevQuotaAlerted:        c.quotaUsageLimitAlerted,
		prevQuotaHandled:        c.quotaUsageLimitHandled,
		prevAccountSlotBoundary: c.accountSlotBoundaryPending,
		prevSlot2aVerify:        c.slot2aVerifyPending,
		prevSlot2aFallback:      c.slot2aFallbackRetried,
		prevDirtyModel:          c.dirtyModel,
		prevDirtyEffort:         c.dirtyEffort,
		prevDirtyMode:           c.dirtyMode,
		prevDirtyPermission:     c.dirtyPermission,
		prevReconcileMode:       c.reconcileMode,
		prevAgyLogNoted:         c.agyLogNoted,
		prevLastErr:             c.lastErr,
		prevUnrequestedCmdWarn:  c.unrequestedCmdWarn,
		prevActionNeeded:        c.actionNeeded,
	}

	// Stop quota timer in memory only; persisted QuotaContinuation stays intact
	// until the single atomic commit so rollback can re-arm from it.
	c.stopQuotaTimer()
	c.disarmModelWait()
	c.agyLog = nil
	c.agyLogNoted = false
	c.agyFrameTrace = ""

	c.compactModeMu.Lock()
	c.compactLeadMode = ""
	c.compactModeGen = 0
	c.compactWorkerMode = ""
	c.compactWorkerModeGen = 0
	c.compactModeMu.Unlock()

	c.setStatus(func() {
		c.caps = nil
		c.commands = nil
		c.failure = nil
		c.mcpBroken = nil
		c.activeMode = ""
		c.bgTasks = 0
		c.background = nil
		c.monitors = nil
		c.dashboardControlEffective = false
		c.lastErr = ""
		c.unrequestedCmdWarn = ""
		c.actionNeeded = ""
	})
	c.retry = nil
	c.alerted = false
	c.usageLimitSettled = false
	c.exhausted = nil
	c.exhaustReport = ""
	c.exhaustNotice = wslive.Notice{}
	c.parkedReports = 0
	c.quotaUsageLimitAlerted = false
	c.quotaUsageLimitHandled = false
	c.accountSlotBoundaryPending = false
	c.slot2aVerifyPending = false
	c.slot2aFallbackRetried = false
	c.dirtyModel, c.dirtyEffort, c.dirtyMode, c.dirtyPermission, c.reconcileMode = false, false, false, false, false

	c.wmu.Lock()
	c.deferRecordPersist = true
	c.mu.Lock()
	c.rec.Profile = target.profile
	c.rec.Backend = target.backend
	c.rec.Model = target.model
	c.rec.Effort = target.effort
	c.rec.Provider = target.provider
	c.rec.SessionID = ""
	c.rec.SessionFile = ""
	c.rec.PermissionMode = ""
	c.rec.AccountSlotID = ""
	c.rec.PendingAccountSlotID = ""
	c.rec.AgyBypass = false
	c.rec.Tier = ""
	c.rec.PluginIsolation = ""
	c.rec.PluginWarning = ""
	c.syncStateLocked()
	c.mu.Unlock()
	c.wmu.Unlock()

	c.live.st.SetAdapter(target.backend)
	return s
}

// rollbackHarnessSwitch restores c.rec and loop state after a failed target
// start or audit commit without persisting target fields to disk.
func (c *conv) rollbackHarnessSwitch(s *harnessSwitchState, noteBefore string) {
	if s == nil {
		return
	}
	c.compactNote = noteBefore

	c.wmu.Lock()
	c.mu.Lock()
	c.rec.Profile = s.prevRec.Profile
	c.rec.Backend = s.prevRec.Backend
	c.rec.Model = s.prevRec.Model
	c.rec.Effort = s.prevRec.Effort
	c.rec.Provider = s.prevRec.Provider
	c.rec.SessionID = s.prevRec.SessionID
	c.rec.SessionFile = s.prevRec.SessionFile
	c.rec.PermissionMode = s.prevRec.PermissionMode
	c.rec.AccountSlotID = s.prevRec.AccountSlotID
	c.rec.PendingAccountSlotID = s.prevRec.PendingAccountSlotID
	c.rec.AgyBypass = s.prevRec.AgyBypass
	c.rec.QuotaContinuation = cloneQuotaContinuation(s.prevRec.QuotaContinuation)
	c.rec.Tier = s.prevRec.Tier
	c.rec.PluginIsolation = s.prevRec.PluginIsolation
	c.rec.PluginWarning = s.prevRec.PluginWarning
	c.rec.PendingCompaction = clonePendingCompaction(s.prevRec.PendingCompaction)
	c.rec.HolderID = ""
	c.rec.HostPID = 0
	c.rec.HolderSeq = 0
	c.rec.ExecCutoverHolder = ""
	c.rec.forgetMCP()
	c.syncStateLocked()
	c.mu.Unlock()
	c.deferRecordPersist = false
	externallyDirtied := !s.prevRecordDirty && c.recordDirty
	if externallyDirtied {
		_ = c.persistRecord()
	}
	c.wmu.Unlock()

	c.live.st.SetAdapter(s.prevRec.Backend)

	c.compactModeMu.Lock()
	c.compactLeadMode = ""
	c.compactModeGen = 0
	c.compactWorkerMode = ""
	c.compactWorkerModeGen = 0
	c.compactModeMu.Unlock()

	c.disarmModelWait()
	c.agyLog = nil
	c.agyLogNoted = s.prevAgyLogNoted
	c.agyFrameTrace = ""

	c.setStatus(func() {
		c.caps = s.prevCaps
		c.commands = s.prevCommands
		c.failure = s.prevFailure
		c.mcpBroken = s.prevMCPBroken
		c.activeMode = s.prevActiveMode
		c.dashboardControlEffective = s.prevDashboardControl
		c.lastErr = s.prevLastErr
		c.unrequestedCmdWarn = s.prevUnrequestedCmdWarn
		c.actionNeeded = s.prevActionNeeded
	})
	c.usageLimitSettled = s.prevUsageLimitSettled
	c.exhausted = s.prevExhausted
	c.exhaustReport = s.prevExhaustReport
	c.exhaustNotice = s.prevExhaustNotice
	c.parkedReports = s.prevParkedReports
	c.quotaUsageLimitAlerted = s.prevQuotaAlerted
	c.quotaUsageLimitHandled = s.prevQuotaHandled
	c.accountSlotBoundaryPending = s.prevAccountSlotBoundary
	c.slot2aVerifyPending = s.prevSlot2aVerify
	c.slot2aFallbackRetried = s.prevSlot2aFallback
	c.dirtyModel = s.prevDirtyModel
	c.dirtyEffort = s.prevDirtyEffort
	c.dirtyMode = s.prevDirtyMode
	c.dirtyPermission = s.prevDirtyPermission
	c.reconcileMode = s.prevReconcileMode

	c.loadCompaction()
	c.loadModelWait()
	c.restoreQuotaContinuation()
	if s.hadRetiredHostState {
		c.syncHost(false)
	}
	c.writeRegistration()
}

// commitHarnessSwitch persists all switched harness fields and PendingCompaction
// in one atomic WAL commit, rewrites registration.json, and notifies watchers.
func (c *conv) commitHarnessSwitch(s *harnessSwitchState, target *harnessTarget, pending *PendingCompaction) error {
	c.wmu.Lock()
	c.deferRecordPersist = false
	c.mu.Lock()
	sends := c.rec.Sends
	saved := clonePendingCompaction(pending)
	saved.RotatedAfterSends = &sends
	c.rec.Profile = target.profile
	c.rec.Backend = target.backend
	c.rec.Model = target.model
	c.rec.Effort = target.effort
	c.rec.Provider = target.provider
	c.rec.SessionID = ""
	c.rec.SessionFile = ""
	c.rec.PermissionMode = ""
	c.rec.AccountSlotID = ""
	c.rec.PendingAccountSlotID = ""
	c.rec.AgyBypass = false
	c.rec.QuotaContinuation = nil
	c.rec.Tier = ""
	c.rec.PluginIsolation = ""
	c.rec.PendingCompaction = saved
	c.syncStateLocked()
	c.mu.Unlock()
	err := c.persistRecord()
	c.wmu.Unlock()
	if err != nil {
		return err
	}

	c.setStatus(func() {
		c.contextTokens = nil
		c.contextWindow = 0
		c.contextCompaction = nil
		c.observed = nil
		c.observations = nil
	})
	c.compactTokens = 0
	c.compactWindow = 0
	c.compactPostRotationCheck = false
	c.compactPostRotationNoShrink = 0
	c.compactTurnRotations = 0

	c.writeRegistration()
	if s.hadHostAlert {
		c.syncHost(false)
	}
	c.changed(false)
	c.watched()
	return nil
}

// residentByWSKey returns the open resident conversation carrying wsKey without
// rehydrating parked conversations.
func (r *Runner) residentByWSKey(wsKey string) *conv {
	if wsKey == "" {
		return nil
	}
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	r.mu.Unlock()
	for _, c := range convs {
		if c.record().WSKey == wsKey {
			return r.resident(c.id)
		}
	}
	return nil
}

// replyIDByWSKey locates a conversation's ReplyID by WSKey, rehydrating a
// parked conversation when needed (matching CompactWithSummary lookup).
func (r *Runner) replyIDByWSKey(wsKey string) (string, bool) {
	wsKey = strings.TrimSpace(wsKey)
	if wsKey == "" {
		return "", false
	}
	for _, rec := range r.parkedRecords() {
		if rec.WSKey == wsKey {
			r.unpark(rec.ReplyID)
			return rec.ReplyID, true
		}
	}
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	var closedID string
	for id, rec := range r.closed {
		if rec.WSKey == wsKey {
			closedID = id
			break
		}
	}
	r.mu.Unlock()
	for _, c := range convs {
		if c.record().WSKey == wsKey {
			return c.id, true
		}
	}
	if closedID != "" {
		return closedID, true
	}
	return "", false
}
