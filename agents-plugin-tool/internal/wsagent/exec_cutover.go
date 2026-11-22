// exec_cutover.go decides whether a process starts under the exec cutover:
// its harness's native process tools (shell, background shell, monitor)
// removed, with the ws exec tools as their replacement. The decision is
// made at each new process start and stays with that process: a rejoin
// keeps what its process was started with (Record.ExecCutoverHolder), so a
// process already running when the cutover arrived, or one an older daemon
// started, keeps its native tools.
package wsagent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// ExecWSTools are the exec tools a cutover session calls in place of its
// native process tools.
var ExecWSTools = []string{"exec.run", "exec.shell", "exec.stop", "exec.tail", "exec.list", "exec.write"}

// CodexReadWSTools are the read-only tools a cutover Codex session reads
// with in place of its shell; ws-mcp admits them for Codex only.
var CodexReadWSTools = []string{"fs.read", "fs.grep", "fs.list"}

// envNoAgent is ws-mcp's agentless product switch: it hides the exec tools,
// so a session under it has no replacement for its native ones.
const envNoAgent = "WS_MCP_NO_AGENT"

// ExecCutoverSupport is a backend's verified cutover mechanism
// (BackendRegistration.ExecCutover): how its launch removes the native
// process tools, and the oldest harness version that removal was verified
// on. A last reported version older than Since keeps native tools.
type ExecCutoverSupport struct {
	Since string
	// LaunchMode, when set, is the native permission mode a cutover lead
	// runs in whatever mode the user saved: launch, live settings changes,
	// in-stream reports and reconciliation all return to it, and the saved
	// mode stays on record for a start outside the cutover.
	LaunchMode string
	// RejectTools are native tools whose permission requests the daemon
	// refuses for a cutover session, live or replayed, ahead of bypass and
	// worker auto-approval: the harness cannot drop them at launch.
	RejectTools []string
	// Instruction is appended to a cutover session's system prompt at
	// session creation, and sent once as a prompt at the end of a turn in
	// which a RejectTools request was refused, or such a tool ran without
	// a request (Event.UnrequestedCommand). Empty sends nothing.
	Instruction string
}

// execCutoverInputs is what a start's cutover decision reads besides the
// record: whether the process gets the daemon's trusted ws-mcp binding
// (an HTTP session token), and the ws-mcp entry's environment.
type execCutoverInputs struct {
	binding bool
	mcpEnv  map[string]string
	// binary fingerprints the binary the start launches (harnessBinaryOf).
	binary string
}

// execCutoverFor decides whether rec's next process starts under the exec
// cutover, and when not, why. Every condition must hold:
//   - the backend has a verified removal mechanism (registration metadata);
//   - a harness version a process last reported (to this daemon or a
//     predecessor) is known, was reported by a process of the binary this
//     start launches (harness_versions.go), and is not older than the one
//     that mechanism was verified on; anything else keeps native tools;
//   - the process gets the trusted binding that hosted exec jobs require
//     (a Pi bridge or Claude stdio fallback has none), and the daemon's
//     hosted exec jobs are open and bound (BindExecJobs);
//   - its ws tools include the replacement (cutoverReplacement): a lead
//     lists every ws tool, a worker only its profile's allowlist, and
//     agentless ws-mcp lists no exec tool;
//   - the harness's rollout switch is on.
func (c *conv) execCutoverFor(rec Record, def Profile, in execCutoverInputs) (bool, string) {
	reg := c.r.opts.Backends[rec.Backend]
	if reg.ExecCutover == nil {
		return false, "the harness has no verified native tool removal"
	}
	if since := reg.ExecCutover.Since; since != "" {
		switch seen := c.r.harnessVersion(rec.Backend); {
		case strings.HasPrefix(in.binary, harnessUnresolved):
			return false, "the harness command does not resolve to a file (" + strings.TrimPrefix(in.binary, harnessUnresolved) + "), so no reported version can verify the removal for it"
		case seen.Version == "":
			return false, "no process of the harness has reported its version yet, so the removal is unverified for it"
		case seen.Binary != in.binary:
			return false, fmt.Sprintf("the harness binary changed since a process of it reported version %s, so the removal is unverified for it", seen.Version)
		case !versionInRange(seen.Version, since, ""):
			return false, fmt.Sprintf("harness version %s is older than %s, where the removal was verified", seen.Version, since)
		}
	}
	if !in.binding {
		return false, "the process has no trusted ws-mcp binding (stdio or bridge fallback)"
	}
	if c.r.execJobs() == nil {
		// ws-mcp lists the exec tools only over open hosted jobs: a store
		// that failed to open, or one the daemon has not bound yet, would
		// leave the process with neither its shell nor exec.shell.
		return false, "the daemon's hosted exec jobs are not open"
	}
	if !rec.Lead {
		for _, name := range cutoverReplacement(rec.Backend) {
			if !slices.Contains(def.WSTools, name) {
				return false, "the profile's ws tools have no " + name + " to replace the native tools with"
			}
		}
	}
	if envFlagOn(in.mcpEnv[envNoAgent]) || (in.mcpEnv[envNoAgent] == "" && envFlagOn(envValue(environ(), envNoAgent))) {
		return false, "agentless ws-mcp has no exec tools"
	}
	if !c.execRolloutOn(rec) {
		return false, wsconfig.ItemExecNativeToolRemoval + " is off for " + rec.Backend
	}
	return true, ""
}

// cutoverReplacement are the ws tools a worker profile's allowlist must
// name for its native tools to be removed: exec.shell, and for Codex,
// whose shell is also its read surface, the fs.* read tools.
func cutoverReplacement(backend string) []string {
	if backend == BackendCodex {
		return append([]string{"exec.shell"}, CodexReadWSTools...)
	}
	return []string{"exec.shell"}
}

// execRolloutOn reads the harness's rollout switch: its bucket, then all,
// then the bare key; on unless one says off. One resolution reads every
// scope's file once for the three keys.
func (c *conv) execRolloutOn(rec Record) bool {
	resolver := c.rolloutResolver(rec)
	values, err := resolver.ListScalars(rec.WSKey, wsconfig.ItemExecNativeToolRemoval)
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: %s: %v\n", rec.ReplyID, wsconfig.ItemExecNativeToolRemoval, err)
		return true
	}
	byKey := make(map[string]string, len(values))
	for _, v := range values {
		byKey[v.Key] = v.Value
	}
	for _, key := range []string{
		wsconfig.ItemExecNativeToolRemoval + "." + rec.Backend,
		wsconfig.ItemExecNativeToolRemoval + ".all",
		wsconfig.ItemExecNativeToolRemoval,
	} {
		switch strings.ToLower(strings.TrimSpace(byKey[key])) {
		case "":
			continue
		case "off", "false", "0", "no":
			return false
		}
		return true
	}
	return true
}

// rolloutResolver is the config resolver rec's start reads its rollout
// switch with.
func (c *conv) rolloutResolver(rec Record) wsconfig.Resolver {
	cfg := c.r.opts.Config
	cfg.RepoRoot = rec.Root
	if roots, ok := c.r.opts.Keys.(interface{ Root(string) (string, bool) }); ok {
		if root, found := roots.Root(rec.WSKey); found {
			cfg.RepoRoot = root
		}
	}
	reader, _ := c.r.opts.Keys.(wsconfig.SessionReader)
	return wsconfig.NewResolver(cfg, nil, reader, nil)
}

// decideExecCutover settles a new process start's cutover into spec and
// marks the start's ws-mcp token, so the connection lists the exec tools
// (and Codex's read tools) from its first request.
func (c *conv) decideExecCutover(rec Record, def Profile, spec *StartSpec, mcpEnv map[string]string) {
	in := execCutoverInputs{binding: spec.MCPSession != nil && spec.MCPSession.Token != "", mcpEnv: mcpEnv}
	if reg := c.r.opts.Backends[rec.Backend]; reg.ExecCutover != nil {
		in.binary = harnessBinaryOf(reg)
	}
	on, why := c.execCutoverFor(rec, def, in)
	spec.ExecCutover = on
	c.setStatus(func() { c.execCutover, c.launchBinary = on, in.binary })
	c.cutoverInstructed = 0
	if on {
		c.r.markMCPCutover(rec.ReplyID, rec.Backend)
	} else if c.r.opts.Backends[rec.Backend].ExecCutover != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: keeps its native process tools: %s\n", rec.ReplyID, why)
	}
}

// rejoinExecCutover restores a rejoined process's cutover from its record:
// on only when the holder it runs under is the one started under it.
func (c *conv) rejoinExecCutover(rec Record) bool {
	on := rec.ExecCutoverHolder != "" && rec.ExecCutoverHolder == rec.HolderID
	// The binary a process this daemon did not launch ran from is unknown:
	// its version report is not kept.
	c.setStatus(func() { c.execCutover, c.launchBinary = on, "" })
	c.cutoverInstructed = 0
	if on {
		c.r.markMCPCutover(rec.ReplyID, rec.Backend)
	}
	return on
}

// execCutoverRouteMark ends the ws-mcp route a cutover process's record
// names (Record.MCPURL). The process itself is given the plain route; the
// mark is a URL fragment, never sent. This daemon reads the marked route as
// its own (servesMCPURL). An older daemon on the same port compares the
// route literally, finds it is not its own, and restarts the process at its
// first idle point without the cutover (its stale-route path): a process
// whose native tools are removed is never left to a daemon that cannot
// serve their replacement.
const execCutoverRouteMark = "#exec-cutover"

// recordedMCPURL is the route a start records for its process: url, marked
// when the process runs under the cutover.
func recordedMCPURL(url string, cutover bool) string {
	if cutover && url != "" {
		return url + execCutoverRouteMark
	}
	return url
}

// servesMCPURL reports whether a recorded route is this daemon's, marked
// or not.
func (r *Runner) servesMCPURL(recorded string) bool {
	return strings.TrimSuffix(recorded, execCutoverRouteMark) == r.mcpURL()
}

// cutoverToolsFor returns the tools of want that p's ws tools admit, in
// want's order: all of them when p names no ws tools (a lead, whose ws-mcp
// lists every tool; a worker without an allowlist has no ws-mcp and never
// starts under the cutover).
func cutoverToolsFor(p Profile, want []string) []string {
	out := make([]string, 0, len(want))
	for _, name := range want {
		if len(p.WSTools) == 0 || slices.Contains(p.WSTools, name) {
			out = append(out, name)
		}
	}
	return out
}

// cutoverHolder is the value Record.ExecCutoverHolder takes for a process
// started under holder id.
func cutoverHolder(on bool, id string) string {
	if on {
		return id
	}
	return ""
}

// markMCPCutover marks conversation id's current ws-mcp token as a cutover
// process's, for harness backend (ResolveMCPToken hands it to ws-mcp).
func (r *Runner) markMCPCutover(id, backend string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.mcpTokens[r.mcpTokenOf[id]]; t != nil {
		t.cutover = backend
	}
}

// noteHarnessVersion keeps a process's report of backend's version,
// launched from binary (harnessVersions.note).
func (r *Runner) noteHarnessVersion(backend, version, binary string) {
	r.versions.note(backend, version, binary)
}

// harnessVersion is backend's last reported version and binary.
func (r *Runner) harnessVersion(backend string) harnessSeen {
	return r.versions.get(backend)
}

// envValue is name's value in env, "" when absent.
func envValue(env []string, name string) string {
	prefix := name + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return env[i][len(prefix):]
		}
	}
	return ""
}

// envFlagOn reports a boolean environment switch that is on.
func envFlagOn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// cutoverSupport is c's backend's cutover support while its current
// process runs under the cutover, nil otherwise.
func (c *conv) cutoverSupport() *ExecCutoverSupport {
	if !c.execCutover {
		return nil
	}
	return c.r.opts.Backends[c.rec.Backend].ExecCutover
}

// cutoverLaunchMode is the native mode a cutover lead must run in, "" when
// none is imposed.
func (c *conv) cutoverLaunchMode() string {
	if s := c.cutoverSupport(); s != nil && c.rec.Lead {
		return s.LaunchMode
	}
	return ""
}

// holdWorkerCutoverMode returns a cutover worker's process to its launch
// mode when it runs in another (reported is the mode it reports). A
// lead's mode goes through its settings reconciliation instead.
func (c *conv) holdWorkerCutoverMode(reported string) {
	s := c.cutoverSupport()
	if s == nil || s.LaunchMode == "" || c.rec.Lead || reported == "" || reported == s.LaunchMode || c.proc == nil {
		return
	}
	if err := controlsFor(c.proc).setPermissionMode(s.LaunchMode); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: return to native %s under the exec cutover: %v\n", c.rec.ReplyID, s.LaunchMode, err)
		return
	}
	fmt.Fprintf(c.r.opts.Log, "agent %s: returned from native %s to %s under the exec cutover\n", c.rec.ReplyID, reported, s.LaunchMode)
}

// cutoverRejects reports whether the daemon refuses a permission request
// for tool in c's current process.
func (c *conv) cutoverRejects(tool string) bool {
	s := c.cutoverSupport()
	return s != nil && slices.Contains(s.RejectTools, tool)
}

// rejectCutoverRequest refuses a's permission request (reject_once) and
// owes the turn its instruction prompt. It reports whether a was such a
// request; one the process cannot be told about stays pending for the
// caller's ordinary path to refuse later.
func (c *conv) rejectCutoverRequest(a ApprovalRequest) bool {
	if !c.cutoverRejects(a.Tool) {
		return false
	}
	answerer := controlsFor(c.proc).approval
	if answerer == nil {
		return false
	}
	if err := c.sendApproval(answerer, a, false, ""); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: refuse %s: %v\n", c.rec.ReplyID, a.Tool, err)
		return false
	}
	fmt.Fprintf(c.r.opts.Log, "agent %s: refused %s %s under the exec cutover\n", c.rec.ReplyID, a.Tool, a.ID)
	c.oweCutoverInstruction(a.ID)
	return true
}

// rejectCutoverPending refuses every waiting request the cutover refuses:
// a replayed one at a rejoin's end, or one that was waiting when the mode
// changed. It runs before bypass or worker auto-approval may answer them.
func (c *conv) rejectCutoverPending() {
	if c.replaying || c.cutoverSupport() == nil {
		return
	}
	for _, a := range slices.Clone(c.approvals) {
		if _, daemon := c.execAnswers[a.ID]; daemon {
			continue
		}
		if c.rejectCutoverRequest(a) {
			c.dropApproval(a.ID, false)
		}
	}
}

// oweCutoverInstruction notes that this turn owes its instruction prompt,
// keyed by the turn's first trigger (a refused request's or an unrequested
// run's tool call id), so a replay of the same turn owes the same one.
func (c *conv) oweCutoverInstruction(trigger string) {
	if c.cutoverNudge == "" {
		c.cutoverNudge = trigger
	}
}

// unrequestedCommand handles a native process tool that ran with no
// permission request (a rule the user's own harness settings allow, say):
// a warning, and the same single instruction at the turn's end.
func (c *conv) unrequestedCommand(toolCallID string, quiet bool) {
	if c.cutoverSupport() == nil {
		return
	}
	if !quiet {
		fmt.Fprintf(c.r.opts.Log, "agent %s: warning: a native command ran without a permission request (%s) under the exec cutover\n", c.rec.ReplyID, toolCallID)
	}
	c.oweCutoverInstruction(toolCallID)
}

// cutoverInstructionCap bounds the instruction prompts one process is
// sent: a model that keeps calling the refused tool after them is not
// prompted again, and its turns report as they end.
const cutoverInstructionCap = 3

// cutoverEventPrefix starts the completion event id of a cutover
// instruction prompt.
const cutoverEventPrefix = "exec-cutover:"

// sendCutoverInstruction delivers the turn's owed instruction once it has
// ended, through once-only completion admission: its event id names the
// process holder and the trigger, so a replay of the turn, or a daemon
// restart in between, finds it admitted. A worker's done report of the
// turn is held until the turn the instruction starts (cutoverInstructing),
// so its parent never reads the refused turn's empty reply as its report;
// an instruction not admitted releases it.
func (c *conv) sendCutoverInstruction() {
	trigger := c.cutoverNudge
	c.cutoverNudge = ""
	s := c.cutoverSupport()
	if trigger != "" && s != nil && s.Instruction != "" && c.cutoverInstructed >= cutoverInstructionCap {
		fmt.Fprintf(c.r.opts.Log, "agent %s: exec cutover instruction not sent again: this process had %d already\n", c.rec.ReplyID, c.cutoverInstructed)
		s = nil
	}
	if trigger == "" || s == nil || s.Instruction == "" {
		if trigger != "" {
			c.r.releaseLater(c.rec.ReplyID)
		}
		return
	}
	c.cutoverInstructed++
	rec := c.rec
	event := cutoverEventPrefix + rec.HolderID + ":" + trigger
	c.cutoverInstructing = true
	c.r.wg.Add(1)
	go func() {
		defer c.r.wg.Done()
		outcome, err := c.r.AdmitCompletion(rec.ReplyID, rec.OpenGen, event, s.Instruction)
		if err != nil {
			fmt.Fprintf(c.r.opts.Log, "agent %s: exec cutover instruction: %v\n", rec.ReplyID, err)
		}
		if err == nil && outcome == CompletionAccepted {
			// The turn that takes it from the inbox clears the hold.
			return
		}
		c.do(func() {
			c.cutoverInstructing = false
			c.releaseHeld()
		})
	}()
}
