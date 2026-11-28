package mcp

import (
	"errors"
	"slices"

	"github.com/kang-sw/devenv/internal/execjob"
)

// server_exec_cutover.go decides what a connection whose bound conversation
// launched under the exec cutover (its native shell tools removed in favour
// of the ws exec tools) lists and admits beyond the ordinary inventory.

// cutoverExecTools are the exec tools a cutover session lists. The legacy
// launch and reader names stay callable everywhere but are never listed.
var cutoverExecTools = []string{"exec.run", "exec.shell", "exec.stop", "exec.tail", "exec.list", "exec.write"}

// fsReadToolNames are the read-only fs.* tools offered to cutover sessions
// whose backend supplies this read surface.
var fsReadToolNames = []string{"fs.read", "fs.grep", "fs.list"}

// isFSReadTool reports whether name is one of the fs.* read tools.
func isFSReadTool(name string) bool { return slices.Contains(fsReadToolNames, name) }

var errNoFSReadTools = errors.New("this read tool requires a dashboard-hosted session with daemon file tools enabled; use your harness's file tools here")

// execToolsListed reports whether this connection lists the exec tools: a
// trusted binding to a conversation that launched under the cutover, on a
// daemon that runs hosted exec jobs (or a daemon-side lead manual render for
// a cutover session).
func (s *Server) execToolsListed() bool {
	return (s.hostedExec() || s.connectionDescriptor().leadRender) && s.execCutover != ""
}

// isDirectTool reports whether canonicalName is listed directly in tools/list
// when descriptor.directTools restricts discovery. Cutover-listed tools and
// opt-in conv.* tools are always direct so directTools does not silently strip
// them behind call.
func (s *Server) isDirectTool(descriptor connectionDescriptor, canonicalName string) bool {
	return canonicalName == "call" || slices.Contains(descriptor.directTools, canonicalName) || s.cutoverListed(canonicalName) || (isDaemonControlTool(canonicalName) && s.daemonControlListedFor(canonicalName)) || (isConvTool(canonicalName) && s.convToolsListed())
}

// fsReadTools requires a trusted cutover binding to a backend that declares
// the daemon read tools. A supplied session key cannot grant this surface.
func (s *Server) fsReadTools() bool {
	if s.conversation == "" {
		return false
	}
	if s.fileWrites != nil && s.fileWrites.CurrentAuthority != nil {
		return s.fileWrites.CurrentAuthority().Valid() && s.lifecycleOf(s.agentAdmissionBackend()).ExecReadTools
	}
	return s.execCutover != "" && s.lifecycleOf(s.execCutover).ExecReadTools
}

// cutoverListed reports whether a tool permanentlyHiddenTool hides is
// listed on this connection anyway.
func (s *Server) cutoverListed(name string) bool {
	switch {
	case isFSWriteTool(name):
		return s.fileWriteTools()
	case isFSReadTool(name):
		return s.fsReadTools()
	case isWebFetchTool(name):
		return s.webFetchTools()
	case slices.Contains(cutoverExecTools, name):
		return s.execToolsListed() && !s.refuseBoundReadOnlyExec(name)
	}
	return false
}

// worktreeExecFence reserves worktree target for a release or removal
// against exec jobs: the daemon's hosted jobs (execjob.Hosted.FenceRoot)
// and this process's file-backed legacy jobs (execjob.FenceLegacyRoot),
// each under one lock with its own admission.
func (s *Server) worktreeExecFence(target string) (func(), error) {
	return s.worktreeExecFenceAs(target, execjob.FenceReleasing)
}

// worktreeAcquireExecFence is worktreeExecFence for worktree.acquire's
// claim: a launch it refuses is told the worktree is being acquired.
func (s *Server) worktreeAcquireExecFence(target string) (func(), error) {
	return s.worktreeExecFenceAs(target, execjob.FenceAcquiring)
}

// worktreeExecFenceAs is worktreeExecFence whose refusal of a new launch
// names reason.
func (s *Server) worktreeExecFenceAs(target, reason string) (func(), error) {
	unfenceLegacy, err := execjob.FenceLegacyRootAs(target, reason)
	if err != nil {
		return nil, err
	}
	unfenceHosted, err := s.execJobs().FenceRootAs(target, reason)
	if err != nil {
		unfenceLegacy()
		return nil, err
	}
	return func() {
		unfenceHosted()
		unfenceLegacy()
	}, nil
}
