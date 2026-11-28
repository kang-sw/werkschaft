package wsagent

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// PermissionLevel is common intent, independent of native modes and capability
// allowlists. It is not a sandbox guarantee.
type PermissionLevel string

const (
	PermissionReadOnly     PermissionLevel = "read-only"
	PermissionWorkdirWrite PermissionLevel = "workdir-write"
	PermissionFull         PermissionLevel = "full"
)

// LeadPermissionLevels returns the ordered common intent choices. Registration
// advertises these for supported lead paths only; workers do not consume them.
func LeadPermissionLevels() []PermissionLevel {
	return []PermissionLevel{PermissionReadOnly, PermissionWorkdirWrite, PermissionFull}
}

func (level PermissionLevel) rank() int {
	switch level {
	case PermissionReadOnly:
		return 1
	case PermissionWorkdirWrite:
		return 2
	case PermissionFull:
		return 3
	}
	return 0
}

// user is supplied by the trusted control owner, never decoded from JSON. The
// acknowledgement is necessary for explicit full selection, not user authority.
func authorizePermission(current, next PermissionLevel, user, ack bool) error {
	if next.rank() == 0 {
		return badRequest("permission_level must be read-only, workdir-write or full")
	}
	if next.rank() > current.rank() && !user {
		return &requestError{status: http.StatusForbidden, msg: "only the user may raise permission_level"}
	}
	if next == PermissionFull && (!user || !ack) {
		return &requestError{status: http.StatusForbidden, msg: "full permission selection requires the user and permission_full_ack after a warning"}
	}
	return nil
}

// legacyLeadPermission is the common level rec's recorded native mode derives
// under its backend's registered policy.
func (r *Runner) legacyLeadPermission(rec Record) PermissionLevel {
	return r.permissionPolicy(rec.Backend).legacyLevel(rec.PermissionMode)
}

func (r *Runner) supportsLeadPermission(backend string, level PermissionLevel) bool {
	reg := r.opts.Backends[backend]
	return reg.Backend != nil && reg.LeadProfile != "" && slices.Contains(reg.PermissionLevels, level)
}

func (r *Runner) leadPermission(rec Record, dir string) (PermissionLevel, string, error) {
	level, explicit, err := r.refreshPermissionSelection(rec.ReplyID, dir)
	return r.resolveLeadPermission(rec, level, explicit, err)
}

func (r *Runner) resolveLeadPermission(rec Record, level PermissionLevel, explicit bool, err error) (PermissionLevel, string, error) {
	if err != nil {
		return "", "", err
	}
	if explicit {
		if !r.supportsLeadPermission(rec.Backend, level) {
			return "", "", badRequest("common lead permissions are not supported on %s", rec.Backend)
		}
		return level, "explicit", nil
	}
	return r.legacyLeadPermission(rec), "legacy-derived", nil
}

func resolveWorkerPermission(def Profile, level PermissionLevel, explicit bool, err error) (PermissionLevel, string, error) {
	if err != nil {
		return "", "", err
	}
	if explicit {
		return level, "explicit", nil
	}
	return legacyWorkerPermission(def), "legacy-derived", nil
}

// workerPermission reads this worker's durable selection, or derives the
// legacy level from the definition that will actually launch. Derived levels
// are never written back.
func (r *Runner) workerPermission(rec Record, def Profile, dir string) (PermissionLevel, string, error) {
	level, explicit, err := r.refreshPermissionSelection(rec.ReplyID, dir)
	return resolveWorkerPermission(def, level, explicit, err)
}

// currentWorkerPermission resolves a parent conversation at admission time.
// Profile edits may change a legacy-derived level, so a launch never reuses a
// stored definition as the parent's current authority.
func (r *Runner) currentWorkerPermission(rec Record) (PermissionLevel, string, error) {
	dir := wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID)
	level, explicit, err := r.refreshPermissionSelection(rec.ReplyID, dir)
	if err != nil || explicit {
		return resolveWorkerPermission(Profile{}, level, explicit, err)
	}
	rp, err := r.resolve(rec.Profile, rec.Root)
	if err != nil {
		return "", "", err
	}
	if rp.Definition.Backend != rec.Backend || rp.Definition.Lead {
		return "", "", fmt.Errorf("agent profile %q no longer resolves to this worker's %s backend", rec.Profile, rec.Backend)
	}
	return resolveWorkerPermission(rp.Definition, "", false, nil)
}

// permissionState is computed even for closed rows; cached records must not
// cache old enforcement evidence. Selection is cached separately and invalidated
// by permission writes.
func (r *Runner) permissionState(st State, rec Record, dir string) State {
	selected, explicit, readErr := r.cachedPermissionSelection(rec.ReplyID, dir)
	var level PermissionLevel
	var source string
	var err error
	if rec.Lead {
		level, source, err = r.resolveLeadPermission(rec, selected, explicit, readErr)
	} else if explicit || readErr != nil {
		level, source, err = resolveWorkerPermission(Profile{}, selected, explicit, readErr)
	} else if st.PermissionLevel != "" {
		level, source = st.PermissionLevel, st.PermissionSource
	} else if rec.ResolvedDefinition != nil {
		level, source = legacyWorkerPermission(*rec.ResolvedDefinition), "legacy-derived"
	}
	if err != nil {
		st.Error = appendDiagnostic(st.Error, err.Error())
		return st
	}
	// A launch with no selection may keep the user's native defaults (Codex).
	// Its reported mode can derive the common display while the process is
	// live, but remains observation rather than a next-launch setting.
	level = r.projectLiveMode(level, rec, source, st.ActiveMode, st.Process)
	if level == "" {
		return st
	}
	st.PermissionLevel, st.PermissionSource = level, source
	active := ""
	if rec.Lead && st.Process {
		active = st.ActiveMode
	}
	st.PermissionEnforcement = r.permissionPolicy(rec.Backend).quality(level, active)
	return st
}

func appendDiagnostic(existing, diagnostic string) string {
	if existing == "" {
		return diagnostic
	}
	if existing == diagnostic || slices.Contains(strings.Split(existing, "; "), diagnostic) {
		return existing
	}
	return existing + "; " + diagnostic
}

// legacyPiWorkerTools preserves permission diagnostics for stored allowlists.
// Retained name check: legacy tool names in stored profiles (TestRetiredPiProfileRemainsReadable).
var legacyPiWorkerTools = []string{"pi.spawn", "pi.send", "pi.list", "pi.stop", "pi.transcript", "pi.wait"}

// A worker projection is used only for the diagnostic. Unknown surfaces remain
// unresolved and never become an authority ceiling or applied selection.
func legacyWorkerPermission(def Profile) PermissionLevel {
	if len(def.MCPServers) != 0 || len(def.Plugins) != 0 {
		return ""
	}
	mutable := false
	for _, tool := range def.Tools {
		switch strings.ToLower(ToolBase(tool)) {
		case "read", "grep", "glob", "find", "ls":
		case "bash", "edit", "write":
			mutable = true
		default:
			return ""
		}
	}
	for _, tool := range def.WSTools {
		switch tool {
		// The runner adds compact to every hosted worker at launch (resolve),
		// and it runs at read-only authority: it cannot raise the level.
		case "git.status", "git.diff", "git.log", "git.merge_base", "compact":
		default:
			if slices.Contains(workerWorkflowTools, tool) || slices.Contains(legacyPiWorkerTools, tool) {
				mutable = true
				continue
			}
			return ""
		}
	}
	if mutable {
		return PermissionWorkdirWrite
	}
	return PermissionReadOnly
}

func (c *conv) permissionNudge(level PermissionLevel, worker bool, active string) {
	rec := c.record()
	policy := c.r.permissionPolicy(rec.Backend)
	quality := policy.quality(level, active)
	limit := "filesystem, network and other tool effects are not proven confined"
	if active == "" && policy.observesNativeMode() {
		limit = "native mode observation is pending; confinement is unproved"
	}
	if worker {
		if level == "" {
			limit = "common worker authority is unresolved; the capability loadout is not confinement evidence"
		} else if rec.Parent == nil {
			limit = "root worker has no authority parent; the capability loadout and harness controls do not prove confinement"
		} else {
			limit = "current-parent admission passed; the capability loadout and harness controls do not prove confinement"
		}
	}
	label := string(level)
	if label == "" {
		label = "unresolved"
	}
	fmt.Fprintf(c.r.opts.Log, "agent %s: Permission %s is %s on %s: %s.\n", rec.ReplyID, label, quality, rec.Backend, limit)
}

// SetPermissionLevel is the authorized agent-lowering seam. It cannot elevate a
// request by supplying an acknowledgement; only trusted HTTP control owns user.
func (r *Runner) SetPermissionLevel(id string, level PermissionLevel) (State, error) {
	return r.act(id, func(c *conv) error { return c.setPermissionLevel(level, false, false) })
}

func (c *conv) setPermissionLevel(level PermissionLevel, user, ack bool) error {
	rec := c.record()
	detachedLeadFallback := false
	selection := c.r.permissionSlot(rec.ReplyID, c.dir)
	selection.admission.Lock()
	defer selection.admission.Unlock()
	if rec.Lead && !c.r.supportsLeadPermission(rec.Backend, level) {
		return badRequest("permission_level %q is not supported on %s", level, rec.Backend)
	}
	var current PermissionLevel
	var err error
	if rec.Lead {
		current, _, err = c.r.leadPermission(rec, c.dir)
	} else {
		rp, resolveErr := c.r.resolve(rec.Profile, rec.Root)
		if resolveErr != nil {
			return resolveErr
		}
		if rp.Definition.Backend != rec.Backend || rp.Definition.Lead {
			return badRequest("agent profile %q no longer resolves to this worker's %s backend", rec.Profile, rec.Backend)
		}
		var source string
		current, source, err = c.r.workerPermission(rec, rp.Definition, c.dir)
		if isAgentWorkerProfile(rec.Profile) && source != "explicit" {
			return badRequest("permission admission: agent.spawn child has no explicit authority record")
		}
		current = c.r.legacyDetachedWorkerLevel(rec.Parent, current, source)
		detachedLeadFallback = c.r.detachedWorkerFallback(rec.ReplyID, c.dir, source)
	}
	if err != nil {
		return err
	}
	if current.rank() == 0 {
		return badRequest("the worker's current permission level is unresolved")
	}
	if err := authorizePermission(current, level, user, ack); err != nil {
		return err
	}
	if !rec.Lead && rec.Parent != nil {
		releaseParent := c.r.holdParentAdmission(rec.Parent)
		defer releaseParent()
		ceiling, err := c.r.agentCeiling(*rec.Parent, detachedLeadFallback)
		if err != nil {
			return badRequest("permission admission: %v", err)
		}
		if level.rank() > ceiling.rank() {
			return &requestError{status: http.StatusForbidden, msg: fmt.Sprintf("permission admission: authority %s is above the parent's current level %s", level, ceiling)}
		}
	}
	if err := c.r.writePermissionSelection(rec.ReplyID, c.dir, level); err != nil {
		return err
	}
	if rec.Lead {
		c.dirtyPermission = c.r.durableProcess(rec.Backend) || c.r.lifecycle(rec.Backend).RestartLeadOnPermissionChange
		if mode := c.r.permissionPolicy(rec.Backend).nativeMode(level); mode != "" {
			c.update(func(rec *Record) { rec.PermissionMode = mode })
			c.dirtyMode, c.reconcileMode = true, false
			c.applyLiveLeadMode(mode)
		}
	} else {
		c.dirtyPermission = true
		c.setStatus(func() {
			c.workerPermissionLevel = level
			c.workerPermissionSource = "explicit"
		})
	}
	if !c.turn {
		c.applySettings()
	}
	c.permissionNudge(level, !rec.Lead, c.activeMode)
	c.rejectCutoverPending()
	c.allowAgyBypassPending()
	c.recheckExecApprovals()
	return nil
}

func (c *conv) setNativePermissionMode(mode string, user bool) error {
	rec := c.record()
	if !rec.Lead || len(c.r.opts.Backends[rec.Backend].PermissionModes) == 0 {
		return categorized(ErrUnsupported, badRequest("the permission mode applies to a supported lead session only"))
	}
	selection := c.r.permissionSlot(rec.ReplyID, c.dir)
	selection.admission.Lock()
	defer selection.admission.Unlock()
	if admitter, ok := c.r.opts.Backends[rec.Backend].Backend.(nativeModeAdmitter); ok {
		if err := admitter.admitNativeMode(rec, mode, user); err != nil {
			return err
		}
	}
	// Extension/native-only registrations retain their historical mode contract.
	if c.r.supportsLeadPermission(rec.Backend, PermissionReadOnly) {
		current, source, err := c.r.leadPermission(rec, c.dir)
		if err != nil {
			return err
		}
		next := c.r.permissionPolicy(rec.Backend).legacyLevel(mode)
		if next == "" {
			return badRequest("native permission mode has no common intent mapping")
		}
		if next.rank() > current.rank() && !user {
			return &requestError{status: http.StatusForbidden, msg: "only the user may raise permission_level through permission_mode"}
		}
		if source == "explicit" && next != current {
			// A native-only legacy URL cannot carry a confirmed explicit full
			// selection. Keep old calls unchanged when no common choice exists.
			if err := authorizePermission(current, next, user, false); err != nil {
				return err
			}
			if err := c.r.writePermissionSelection(rec.ReplyID, c.dir, next); err != nil {
				return err
			}
		}
	}
	if mode != rec.selectedPermissionMode() {
		c.update(func(rec *Record) { rec.selectPermissionMode(mode) })
		c.dirtyMode, c.reconcileMode = true, false
		c.applyLiveLeadMode(mode)
		if !c.turn {
			c.applySettings()
		}
		c.rejectCutoverPending()
		c.allowAgyBypassPending()
		c.recheckExecApprovals()
	}
	return nil
}

// liveLeadMode is a mode control that applies synchronously before the next
// tool, even while its turn is active.
type liveLeadMode interface {
	modeSetter
	activePermissionMode() string
}

func (c *conv) applyLiveLeadMode(mode string) {
	proc, ok := c.proc.(liveLeadMode)
	if !ok || c.replaying || proc.SetPermissionMode(mode) != nil {
		return
	}
	c.dirtyPermission, c.dirtyMode, c.reconcileMode = false, false, false
	c.setStatus(func() { c.activeMode = proc.activePermissionMode() })
}

// A holder attach ignores launch flags. After replay, reconstruct delivery from
// the durable selection/native target instead of trusting a lost dirty flag.
// Replay observations remain evidence, never an acknowledgement of that target.
func (c *conv) reconcileLeadPermission() {
	// The loop owns permission state, but Send updates the shared record
	// concurrently. Copy it under mu before passing it by value.
	rec := c.record()
	if mode := c.cutoverLaunchMode(); mode != "" {
		// The cutover's mode, never the saved one, is what the process must
		// run in; the record keeps the saved preference.
		if !c.dirtyMode && c.activeMode != "" && c.activeMode != mode {
			c.dirtyMode, c.reconcileMode = true, true
		}
		return
	}
	policy := c.r.permissionPolicy(rec.Backend)
	if !rec.Lead || policy.nativeMode(PermissionReadOnly) == "" || c.dirtyMode {
		return
	}
	level, source, err := c.r.leadPermission(rec, c.dir)
	if err != nil {
		// State projects the current selection error without retaining history
		// in the terminal error slot or letting it suppress a process exit.
		return
	}
	if source != "explicit" {
		return
	}
	target := rec.PermissionMode
	if policy.legacyLevel(rec.PermissionMode) != level {
		target = policy.nativeMode(level)
	}
	if target == c.activeMode {
		return
	}
	if target != rec.PermissionMode {
		c.update(func(rec *Record) { rec.PermissionMode = target })
	}
	c.dirtyMode, c.reconcileMode = true, true
}
