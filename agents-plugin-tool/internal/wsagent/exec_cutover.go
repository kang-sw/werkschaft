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
}

// execCutoverInputs is what a start's cutover decision reads besides the
// record: whether the process gets the daemon's trusted ws-mcp binding
// (an HTTP session token), and the ws-mcp entry's environment.
type execCutoverInputs struct {
	binding bool
	mcpEnv  map[string]string
}

// execCutoverFor decides whether rec's next process starts under the exec
// cutover, and when not, why. Every condition must hold:
//   - the backend has a verified removal mechanism (registration metadata);
//   - the harness version this daemon last saw is not older than the one
//     that mechanism was verified on;
//   - the process gets the trusted binding that hosted exec jobs require
//     (a Pi bridge or Claude stdio fallback has none);
//   - its ws tools include the exec replacement (a profile allowlist
//     without exec.shell, or agentless ws-mcp, has none);
//   - the harness's rollout switch is on.
func (c *conv) execCutoverFor(rec Record, def Profile, in execCutoverInputs) (bool, string) {
	reg := c.r.opts.Backends[rec.Backend]
	if reg.ExecCutover == nil {
		return false, "the harness has no verified native tool removal"
	}
	if seen := c.r.harnessVersion(rec.Backend); seen != "" && !versionInRange(seen, reg.ExecCutover.Since, "") {
		return false, fmt.Sprintf("harness version %s is older than %s, where the removal was verified", seen, reg.ExecCutover.Since)
	}
	if !in.binding {
		return false, "the process has no trusted ws-mcp binding (stdio or bridge fallback)"
	}
	if !rec.Lead && !slices.Contains(def.WSTools, "exec.shell") {
		return false, "the profile's ws tools have no exec replacement"
	}
	if envFlagOn(in.mcpEnv[envNoAgent]) || (in.mcpEnv[envNoAgent] == "" && envFlagOn(envValue(environ(), envNoAgent))) {
		return false, "agentless ws-mcp has no exec tools"
	}
	if !c.execRolloutOn(rec) {
		return false, wsconfig.ItemExecNativeToolRemoval + " is off for " + rec.Backend
	}
	return true, ""
}

// execRolloutOn reads the harness's rollout switch: its bucket, then all,
// then the bare key; on unless one says off.
func (c *conv) execRolloutOn(rec Record) bool {
	cfg := c.r.opts.Config
	cfg.RepoRoot = rec.Root
	if roots, ok := c.r.opts.Keys.(interface{ Root(string) (string, bool) }); ok {
		if root, found := roots.Root(rec.WSKey); found {
			cfg.RepoRoot = root
		}
	}
	reader, _ := c.r.opts.Keys.(wsconfig.SessionReader)
	resolver := wsconfig.NewResolver(cfg, nil, reader, nil)
	for _, key := range []string{
		wsconfig.ItemExecNativeToolRemoval + "." + rec.Backend,
		wsconfig.ItemExecNativeToolRemoval + ".all",
		wsconfig.ItemExecNativeToolRemoval,
	} {
		rv, err := resolver.Get(rec.WSKey, key)
		if err != nil {
			fmt.Fprintf(c.r.opts.Log, "agent %s: %s: %v\n", rec.ReplyID, key, err)
			continue
		}
		switch strings.ToLower(strings.TrimSpace(rv.Value)) {
		case "":
			continue
		case "off", "false", "0", "no":
			return false
		}
		return true
	}
	return true
}

// decideExecCutover settles a new process start's cutover into spec and
// marks the start's ws-mcp token, so the connection lists the exec tools
// (and Codex's read tools) from its first request.
func (c *conv) decideExecCutover(rec Record, def Profile, spec *StartSpec, mcpEnv map[string]string) {
	in := execCutoverInputs{binding: spec.MCPSession != nil && spec.MCPSession.Token != "", mcpEnv: mcpEnv}
	on, why := c.execCutoverFor(rec, def, in)
	spec.ExecCutover = on
	c.setStatus(func() { c.execCutover = on })
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
	c.setStatus(func() { c.execCutover = on })
	if on {
		c.r.markMCPCutover(rec.ReplyID, rec.Backend)
	}
	return on
}

// cutoverToolsFor returns the tools of want that p's ws tools admit, in
// want's order: all of them when p names no ws tools.
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

// noteHarnessVersion keeps the newest harness version a process of backend
// reported, for the next start's cutover decision. Memory only.
func (r *Runner) noteHarnessVersion(backend, version string) {
	if version == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.harnessVersions == nil {
		r.harnessVersions = map[string]string{}
	}
	r.harnessVersions[backend] = version
}

// harnessVersion is the harness version a process of backend last
// reported to this daemon, "" when none has.
func (r *Runner) harnessVersion(backend string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.harnessVersions[backend]
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
