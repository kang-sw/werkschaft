package mcp

import (
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsfs"
)

// permission_gate.go enforces a spawned agent's authority on its ws tools.
// The runner puts the child's level in its attached ws-mcp's environment
// (wsagent.EnvPermissionLevel): a hosted HTTP session's hello env, or a
// stdio ws-mcp's process env; both reach getenv. Blank is no level, and
// workdir-write and full refuse nothing, so only read-only gates here.

// readOnlyRefusedTool reports a ws tool call of the agent loadout that
// changes the work directory, review ledger, or ticket board, which a
// read-only agent may not call. The sage gate and stamp write sage-review
// posture into a ticket, and reviewers must never write it. mailbox.send and non-bootstrap review.marker
// reads stay allowed.
func readOnlyRefusedTool(name string, args map[string]any) bool {
	switch name {
	case "git.commit", "tickets.create_empty", "tickets.acquire", "tickets.release", "tickets.close", "tickets.move", "review.stamp", "tickets.sage_gate", "tickets.sage_stamp":
		return true
	case "review.marker":
		return boolArgument(args["bootstrap"])
	}
	return false
}

// readOnlyAuthority reports whether this server's caller is a read-only
// worker, whose exec launches the allowlist gate restricts.
func (s *Server) readOnlyAuthority() bool {
	if s.fileWrites != nil && s.fileWrites.CurrentAuthority != nil {
		level := s.fileWrites.CurrentAuthority()
		return level != wsfs.AuthorityFull && level != wsfs.AuthorityWorkdirWrite
	}
	return wsagent.PermissionLevel(strings.TrimSpace(s.getenv(wsagent.EnvPermissionLevel))) == wsagent.PermissionReadOnly
}

// readOnlyRefusedExecForm reports an exec launch form a read-only worker
// may not use at all: exec.spawn and the legacy exec.shell form (a
// caller-selected shell). Its launches go through exec.run and the new
// exec.shell form, which the allowlist gate checks (execjob.ReadOnlyShellLaunch).
func readOnlyRefusedExecForm(tool string, args map[string]any) bool {
	switch tool {
	case "exec.spawn":
		return true
	case "exec.shell":
		family, _ := execShellFamily(args)
		return family.legacy
	}
	return false
}

func (s *Server) refuseBoundReadOnlyExec(tool string) bool {
	switch tool {
	case "exec.run", "exec.shell", "exec.spawn", "exec.write":
	default:
		return false
	}
	if s.conversation == "" || !s.readOnlyAuthority() {
		return false
	}
	// An active immutable read-only worker gets daemon-side read-only allowlist
	// exec parity (exec.run and new exec.shell checked by execjob; exec.spawn
	// and legacy exec.shell refused by readOnlyRefusedExecForm). Read-only
	// leads (LiveAuthority) and downgraded/revoked workers stay fail-closed.
	if s.fileWrites != nil && !s.fileWrites.LiveAuthority &&
		s.fileWrites.Authority == wsfs.AuthorityReadOnly &&
		s.fileWrites.AuthorityForCall() == wsfs.AuthorityReadOnly {
		return false
	}
	backend := s.execCutover
	if backend == "" {
		backend = s.agentAdmissionBackend()
	}
	return s.lifecycleOf(backend).ReadOnlyNoExec
}

// permissionRefusal is why this server refuses tool for its permission
// level, "" when it does not.
func (s *Server) permissionRefusal(tool string, args map[string]any) string {
	if !s.readOnlyAuthority() {
		return ""
	}
	level := strings.TrimSpace(s.getenv(wsagent.EnvPermissionLevel))
	if s.refuseBoundReadOnlyExec(tool) {
		return fmt.Sprintf("%s: permission denied: this read-only session cannot start or send input to an exec job", tool)
	}
	if readOnlyRefusedExecForm(tool, args) {
		return fmt.Sprintf("%s: permission denied: this agent runs at authority %s, which the session that spawned it chose, and %s launches a command in a form that is not checked. Use exec.run (exe and args) or exec.shell (cmd) with a read command such as git log or rg.", tool, level, tool)
	}
	if !readOnlyRefusedTool(tool, args) {
		return ""
	}
	return fmt.Sprintf("%s: permission denied: this agent runs at authority %s, which the session that spawned it chose, and %s changes the work directory, review ledger, or ticket board. Do the read-only part of the task and put the change you would make in your report.", tool, level, tool)
}
