package mcp

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsagent"
)

// server_exec_cutover.go decides what a connection whose bound conversation
// launched under the exec cutover (its native shell tools removed in favour
// of the ws exec tools) lists and admits beyond the ordinary inventory.

// cutoverExecTools are the exec tools a cutover session lists. The legacy
// launch and reader names stay callable everywhere but are never listed.
var cutoverExecTools = []string{"exec.run", "exec.shell", "exec.stop", "exec.tail", "exec.list", "exec.write"}

// codexReadToolNames are the read-only fs.* tools that stand in for a
// cutover Codex session's shell reads.
var codexReadToolNames = []string{"fs.read", "fs.grep", "fs.list"}

// isCodexReadTool reports whether name is one of the fs.* read tools.
func isCodexReadTool(name string) bool { return slices.Contains(codexReadToolNames, name) }

// errNoCodexReadTools refuses an fs.* call outside a cutover Codex session.
var errNoCodexReadTools = errors.New("this read tool runs only in a dashboard-hosted Codex session whose shell tool is replaced by the ws exec tools; use the harness's own file tools here")

// execToolsListed reports whether this connection lists the exec tools: a
// trusted binding to a conversation that launched under the cutover, on a
// daemon that runs hosted exec jobs.
func (s *Server) execToolsListed() bool {
	return s.hostedExec() && s.execCutover != ""
}

// codexReadTools reports whether this connection lists and admits the
// fs.* read tools: a trusted binding to a Codex conversation that launched
// under the cutover. A supplied session_key or hello field never stands in
// for that binding.
func (s *Server) codexReadTools() bool {
	return s.conversation != "" && s.execCutover == wsagent.BackendCodex
}

// cutoverListed reports whether a tool permanentlyHiddenTool hides is
// listed on this connection anyway.
func (s *Server) cutoverListed(name string) bool {
	switch {
	case isCodexReadTool(name):
		return s.codexReadTools()
	case slices.Contains(cutoverExecTools, name):
		return s.execToolsListed()
	}
	return false
}

// worktreeExecFence reserves worktree target for a release or removal
// against exec jobs: the daemon's hosted jobs under one lock with their
// admission (execjob.Hosted.FenceRoot), and this process's file-backed
// legacy jobs by count.
func (s *Server) worktreeExecFence(target string) (func(), error) {
	legacy := execjob.ActiveRoots()
	n := legacy[filepath.Clean(target)]
	if real, err := filepath.EvalSymlinks(target); err == nil && filepath.Clean(real) != filepath.Clean(target) {
		n += legacy[filepath.Clean(real)]
	}
	if n > 0 {
		return nil, fmt.Errorf("%d exec job(s) still run in it; abort them with exec.abort, or let them finish, first", n)
	}
	return s.execJobs().FenceRoot(target)
}
