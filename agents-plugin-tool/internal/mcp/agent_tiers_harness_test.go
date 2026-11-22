package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

func TestHostedAgyAgentTiersUsesDescriptorHarness(t *testing.T) {
	regs := descriptorRegistrations()
	reg := regs[wsagent.BackendAgy]
	// A transport-only backend makes tiers available without an agy install
	// or provider call. The MCP handler and tier resolver remain real. The
	// worker role is this fixture's: the shipped agy is lead-only (below).
	reg.Backend = &lifecycleBackend{}
	reg.WorkerCapable = true
	reg.WorkerProfile = wsagent.AgentWorkerProfile(wsagent.BackendAgy)
	regs[wsagent.BackendAgy] = reg
	s, _ := hostedBackendConnection(t, wsagent.BackendAgy, regs)
	if got := s.currentHarness(); got != "" {
		t.Fatalf("detected harness = %q, want empty", got)
	}
	raw := callToolOnce(t, s, 1, "agent_tiers", map[string]any{"format": "json"})
	if toolIsError(t, raw) {
		t.Fatal(raw)
	}
	var tiers wsagent.AgentTiers
	if err := json.Unmarshal([]byte(toolText(t, raw)), &tiers); err != nil {
		t.Fatal(err)
	}
	if tiers.Harness != "agy" {
		t.Fatalf("harness = %q, want agy", tiers.Harness)
	}
	if len(tiers.Native) != 4 {
		t.Fatalf("native tiers = %+v, want four bare tiers", tiers.Native)
	}
	for i, name := range []string{"small", "medium", "large", "xlarge"} {
		tier := tiers.Native[i]
		if tier.Tier != name || tier.Model == "" || tier.Effort == "" {
			t.Errorf("native tier %d = %+v", i, tier)
		}
	}
	if got := s.currentHarness(); got != "" {
		t.Fatalf("agent.tiers changed detected harness to %q", got)
	}
}

// A hosted agy lead's agent.spawn of a codex worker reaches the runner as
// the agy lead's own request and succeeds: lead-only agy refuses agy
// workers, not agy leads' spawns. The runner is a fake; the wsagent tests
// cover the real resolution (TestAgyLeadOnlyRefusesEverySpawnRouteAndListsWhy).
func TestHostedAgyLeadSpawnsACodexWorker(t *testing.T) {
	s, f := hostedBackendConnection(t, wsagent.BackendAgy, descriptorRegistrations())
	// The connection reads its agy descriptor (sanitized tool names) through
	// the real runner once; the fake runner then answers the spawn.
	if s.connectionDescriptor().aliases == nil {
		t.Fatal("the hosted agy connection has no tool aliases")
	}
	agents := newFakePiAgents()
	s.pi.agents = agents
	raw := callToolOnce(t, s, 1, "agent_spawn", map[string]any{"prompt": "review", "tier": "codex.small", "authority": "read-only", "format": "json"})
	if toolIsError(t, raw) {
		t.Fatal(raw)
	}
	var res wsagent.AgentSpawnResult
	if err := json.Unmarshal([]byte(toolText(t, raw)), &res); err != nil || res.Address != "id:kid1" {
		t.Fatalf("agent.spawn = %s (%v)", raw, err)
	}
	if len(agents.agent.callers) != 1 || len(agents.agent.requests) != 1 {
		t.Fatalf("calls: callers %+v, requests %+v", agents.agent.callers, agents.agent.requests)
	}
	caller, req := agents.agent.callers[0], agents.agent.requests[0]
	if caller.Harness != wsagent.BackendAgy || caller.Parent != (wsagent.ParentRef{Kind: wsagent.ParentLead, ReplyID: wsmailbox.ReplyID(f.hosted)}) {
		t.Fatalf("caller = %+v", caller)
	}
	if req.Tier != "codex.small" || req.Authority != "read-only" || req.Prompt != "review" {
		t.Fatalf("request = %+v", req)
	}
}

// With the daemon's own lead-only agy registration, a hosted agy lead's
// agent.tiers offers no bare tier and names agy under Unavailable with the
// agy reason.
func TestHostedAgyAgentTiersListsTheLeadOnlyReason(t *testing.T) {
	regs := descriptorRegistrations()
	shipped := wsagent.BuiltinBackendRegistrations()[wsagent.BackendAgy]
	shipped.Backend = &lifecycleBackend{}
	regs[wsagent.BackendAgy] = shipped
	s, _ := hostedBackendConnection(t, wsagent.BackendAgy, regs)
	raw := callToolOnce(t, s, 1, "agent_tiers", map[string]any{"format": "json"})
	if toolIsError(t, raw) {
		t.Fatal(raw)
	}
	var tiers wsagent.AgentTiers
	if err := json.Unmarshal([]byte(toolText(t, raw)), &tiers); err != nil {
		t.Fatal(err)
	}
	if tiers.Harness != "agy" || len(tiers.Native) != 0 {
		t.Fatalf("harness %q, native %+v", tiers.Harness, tiers.Native)
	}
	i := slices.IndexFunc(tiers.Unavailable, func(e string) bool { return strings.HasPrefix(e, "agy: ") })
	if i < 0 || !strings.Contains(tiers.Unavailable[i], "hosted lead only") || !strings.Contains(tiers.Unavailable[i], "Antigravity") {
		t.Fatalf("unavailable = %v", tiers.Unavailable)
	}
}
