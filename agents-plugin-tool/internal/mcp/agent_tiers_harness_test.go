package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
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
