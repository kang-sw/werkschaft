package mcp

import (
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
)

// policyLookupRegistry counts how a connection reads its registry: the per-call
// policy reads must use the per-name accessors, never the full registry copy.
type policyLookupRegistry struct {
	regs   map[string]wsagent.BackendRegistration
	clones int
}

func (r *policyLookupRegistry) BackendRegistrations() map[string]wsagent.BackendRegistration {
	r.clones++
	return r.regs
}

func (r *policyLookupRegistry) BackendLifecycle(name string) (wsagent.LifecyclePolicy, bool) {
	reg, ok := r.regs[name]
	return reg.Lifecycle, ok
}

func (r *policyLookupRegistry) BackendConnection(name string) (wsagent.ConnectionPolicy, bool) {
	reg, ok := r.regs[name]
	return reg.Connection, ok
}

func TestPolicyReadsDoNotCloneTheRegistry(t *testing.T) {
	reg := &policyLookupRegistry{regs: wsagent.BuiltinBackendRegistrations()}
	s := &Server{agentBackendSource: reg, conversation: "conv", execCutover: wsagent.BackendCodex}
	for i := 0; i < 3; i++ {
		if !s.codexReadTools() {
			t.Fatal("a Codex cutover connection must list the fs.* read tools")
		}
	}
	if s.lifecycleOf("unregistered") != wsagent.BuiltinLifecycle("unregistered") {
		t.Error("an unregistered name must read the built-in floor")
	}
	if got := s.connectionOf(wsagent.BackendPi); got.BridgeClientName == "" {
		t.Error("the Pi registration's connection declaration was not read")
	}
	if reg.clones != 0 {
		t.Errorf("the policy reads copied the whole registry %d time(s)", reg.clones)
	}
}
