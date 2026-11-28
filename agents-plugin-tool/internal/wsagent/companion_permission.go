package wsagent

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Companion sessions (companion-body, companion-work) are daemon-managed
// leads hidden from the roster: no page ever shows their permission prompts,
// so a prompt parked in c.approvals is never answered, and the harness turn
// that asked for it (and every watchdog that skips a session with a pending
// approval) waits forever. The daemon therefore settles each of their
// permission requests itself, the moment it arrives, by the policy below.

// Companion role names, the values of Record.CompanionRole (the same strings
// internal/mcp names CompanionRoleBody / CompanionRoleWork; wsagent does not
// import that package).
const (
	companionRoleBody = "companion-body"
	companionRoleWork = "companion-work"
)

// companionReadOnlyKinds are the harness-reported tool categories (ACP
// toolCall.kind) a companion-body may use without the ws server's tools.
var companionReadOnlyKinds = []string{"read", "search", "think"}

// isWSMCPTool reports whether tool names a tool of the ws MCP server in a
// harness spelling: agy's mcp_ws_<tool>, Claude's mcp__ws__<tool>.
//
// agy flattens server and tool into one name, so a server named ws_<x> would
// read as a ws tool; the runner reserves only "ws" and a companion attaches no
// other server, and the ws server itself gates what a companion-body may call
// (companionBodyBaseAllows). The Claude spelling is matched exactly: a "__"
// inside the tool part names another server's tool.
func isWSMCPTool(tool string) bool {
	tool = strings.ToLower(strings.TrimSpace(tool))
	if rest, ok := strings.CutPrefix(tool, "mcp__"+ReservedMCPServer+"__"); ok {
		return rest != "" && !strings.Contains(rest, "__")
	}
	return strings.HasPrefix(tool, "mcp_"+ReservedMCPServer+"_")
}

// companionShellTools are native process tools of every backend a request may
// name without an execute kind (Claude's Bash family, Codex's shell tools,
// agy's run_command).
var companionShellTools = []string{strings.ToLower(agyCommandTool), "bash", "bashoutput", "killshell", "shell", "local_shell", "exec_command", "write_stdin"}

// isShellRequest reports a native process tool request: a shell tool by name,
// or any request the harness categorizes as execute.
func isShellRequest(a ApprovalRequest) bool {
	return slices.Contains(companionShellTools, strings.ToLower(strings.TrimSpace(a.Tool))) || strings.EqualFold(strings.TrimSpace(a.Kind), "execute")
}

// companionApprovalAllows is the daemon's verdict on one permission request
// of a hidden companion session:
//
//   - companion-body (persona; talks and delegates): the ws server's tools and
//     read-only tools (read/search/think) run; everything else is refused,
//     above all the shell.
//   - companion-work (engineering brain): every tool runs except a shell
//     request; commands go to engineering agents it spawns through ws.
//
// Any other role value is refused.
func companionApprovalAllows(rec Record, a ApprovalRequest) bool {
	if isShellRequest(a) {
		return false
	}
	switch rec.CompanionRole {
	case companionRoleBody:
		return isWSMCPTool(a.Tool) || slices.Contains(companionReadOnlyKinds, strings.ToLower(strings.TrimSpace(a.Kind)))
	case companionRoleWork:
		return true
	}
	return false
}

// settleCompanionApproval answers a hidden companion session's permission
// request by companionApprovalAllows instead of parking it. A nil error means
// a was answered. A non-nil error matching errNoApprovalAnswer means nothing
// was written (no answerer, or a request it could build no answer for), so the
// caller must fail closed via approvalFailed(errNoApprovalAnswer). Any other
// error came from a transport write failure that sendApproval already handled
// via approvalFailed(err), so the caller must preserve c.lastErr rather than
// overwriting it with errNoApprovalAnswer.
func (c *conv) settleCompanionApproval(a ApprovalRequest) error {
	rec := c.record()
	allow := companionApprovalAllows(rec, a)
	answerer := controlsFor(c.proc).approval
	if answerer == nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: companion permission request %s (%s) has no answerer; dropped\n", rec.ReplyID, a.ID, a.Tool)
		return errNoApprovalAnswer
	}
	if err := c.sendApproval(answerer, a, allow, ""); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: companion permission answer for %s: %v\n", rec.ReplyID, a.Tool, err)
		return err
	}
	verdict := "refused"
	if allow {
		verdict = "allowed"
	}
	fmt.Fprintf(c.r.opts.Log, "agent %s: companion %s %s request %s\n", rec.ReplyID, verdict, a.Tool, a.ID)
	if !allow && c.cutoverRejects(a.Tool) {
		c.oweCutoverInstruction(a.ID)
	}
	return nil
}

// settleCompanionPending answers every prompt a hidden companion session
// holds: one a previous daemon left unanswered and a rejoin replayed, or one
// that arrived while the session replayed. It runs where bypass's pending
// answers do.
func (c *conv) settleCompanionPending() {
	if c.replaying || c.record().CompanionRole == "" {
		return
	}
	for _, a := range slices.Clone(c.approvals) {
		if _, daemon := c.execAnswers[a.ID]; daemon {
			continue
		}
		err := c.settleCompanionApproval(a)
		if err == nil {
			c.dropApproval(a.ID, false)
			continue
		}
		// Drop all non-exec approvals and fail closed so no unanswerable
		// prompt stays parked to wedge the companion turn or watchdogs.
		for _, rem := range slices.Clone(c.approvals) {
			if _, daemon := c.execAnswers[rem.ID]; !daemon {
				c.dropApproval(rem.ID, false)
			}
		}
		// Preserve a transport write error sendApproval already recorded in
		// c.lastErr; only stamp errNoApprovalAnswer when nothing was written.
		if errors.Is(err, errNoApprovalAnswer) || c.lastErr == "" {
			c.approvalFailed(errNoApprovalAnswer)
		} else if c.proc != nil {
			c.stopProcess("")
		}
		return
	}
}
