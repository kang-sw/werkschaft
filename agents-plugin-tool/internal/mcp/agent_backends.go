package mcp

import "github.com/kang-sw/devenv/internal/wsagent"

// AgentBackendRegistry exposes the daemon runner's registered backend
// descriptors without granting the lead runner handle (PiAgents). Worker
// connections receive this so they can resolve their backend descriptor and
// profiles while keeping pi.*/agent.* calls on the token-forwarding path.
type AgentBackendRegistry interface {
	BackendRegistrations() map[string]wsagent.BackendRegistration
}

// backendPolicyLookup is the optional per-name read of a registry source: the
// daemon runner answers it without cloning every registration, which the
// per-call policy reads (the fs.* gate, tools/list) would otherwise pay.
type backendPolicyLookup interface {
	BackendLifecycle(name string) (wsagent.LifecyclePolicy, bool)
	BackendConnection(name string) (wsagent.ConnectionPolicy, bool)
}

// policyLookup is the connection's per-name registry reader, nil when it has
// no registry or the registry offers only the full copy.
func (s *Server) policyLookup() backendPolicyLookup {
	// The same source precedence as agentBackends: the dedicated source wins.
	if s.agentBackendSource != nil {
		lookup, _ := s.agentBackendSource.(backendPolicyLookup)
		return lookup
	}
	lookup, _ := s.pi.agents.(backendPolicyLookup)
	return lookup
}

// ambientRefresher is an optional in-memory capability of the daemon runner.
type ambientRefresher interface {
	RefreshAmbient()
}

// refreshAgentAmbient reaches the runner through either daemon wiring path.
// Return after the first match because both paths may hold the same runner.
func (s *Server) refreshAgentAmbient() {
	if refresher, ok := s.agentBackendSource.(ambientRefresher); ok {
		refresher.RefreshAmbient()
		return
	}
	if refresher, ok := s.pi.agents.(ambientRefresher); ok {
		refresher.RefreshAmbient()
	}
}

// agentBackends binds config admission and connection descriptors to the
// daemon's one runner registry. A standalone server has no process registry
// and retains builtin-only reads.
func (s *Server) agentBackends() []map[string]wsagent.BackendRegistration {
	if s.agentBackendSource != nil {
		return []map[string]wsagent.BackendRegistration{s.agentBackendSource.BackendRegistrations()}
	}
	if source, ok := s.pi.agents.(AgentBackendRegistry); ok {
		return []map[string]wsagent.BackendRegistration{source.BackendRegistrations()}
	}
	return nil
}
