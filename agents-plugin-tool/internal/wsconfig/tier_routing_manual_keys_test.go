package wsconfig

import (
	"encoding/json"
	"testing"
)

// The manual knobs are siblings of agent.tier_routing: they resolve through
// every scope (unlike the global-only graph), default their writes to
// global, and leave the strictly decoded routing value untouched.
func TestTierRoutingManualKeysResolveThroughEveryScope(t *testing.T) {
	for _, key := range []string{ItemAgentTierRoutingExpose, ItemAgentTierRoutingGuidance} {
		if GlobalOnly(key) {
			t.Fatalf("%s must resolve through session, project, and repo scopes, not only global", key)
		}
		if got := DefaultScope(key); got != ScopeGlobal {
			t.Fatalf("%s default write scope = %s, want global", key, got)
		}
		if !RepoScoped(key, true) {
			t.Fatalf("%s must be readable from the committed repo scope", key)
		}
	}

	sess := newFakeSessionStore()
	_, opts := newRepoTestResolver(t, sess)
	r := NewResolver(opts, map[string]string{ItemAgentTierRoutingExpose: "off"}, sess, sess)
	if rv, err := r.Get("", ItemAgentTierRoutingExpose); err != nil || rv.Value != "off" || rv.Scope != ScopeBuiltin {
		t.Fatalf("builtin expose = %+v, %v; want off/builtin", rv, err)
	}
	if rv, err := r.Get("", ItemAgentTierRoutingGuidance); err != nil || rv.Value != "" || rv.Scope != ScopeBuiltin {
		t.Fatalf("builtin guidance = %+v, %v; want empty/builtin", rv, err)
	}

	// A write naming no scope lands in global; repo and project win over it
	// in the usual order.
	if err := r.Set(ItemAgentTierRoutingExpose, "on", SetOptions{}); err != nil {
		t.Fatalf("set global: %v", err)
	}
	if rv, _ := r.Get("", ItemAgentTierRoutingExpose); rv.Value != "on" || rv.Scope != ScopeGlobal {
		t.Fatalf("expose after default-scope write = %+v, want on/global", rv)
	}
	writeRepoConfig(t, opts.RepoRoot, map[string]string{ItemAgentTierRoutingGuidance: "repo guidance"})
	if rv, _ := r.Get("", ItemAgentTierRoutingGuidance); rv.Value != "repo guidance" || rv.Scope != ScopeRepo {
		t.Fatalf("guidance from repo = %+v, want repo guidance/repo", rv)
	}
	if err := r.Set(ItemAgentTierRoutingGuidance, "project guidance", SetOptions{ExplicitScope: ScopeProject}); err != nil {
		t.Fatalf("set project: %v", err)
	}
	if rv, _ := r.Get("", ItemAgentTierRoutingGuidance); rv.Value != "project guidance" || rv.Scope != ScopeProject {
		t.Fatalf("guidance from project = %+v, want project guidance/project", rv)
	}
	if err := r.Set(ItemAgentTierRoutingExpose, "off", SetOptions{ExplicitScope: ScopeSession, SessionKey: "k1"}); err != nil {
		t.Fatalf("set session: %v", err)
	}
	if rv, _ := r.Get("k1", ItemAgentTierRoutingExpose); rv.Value != "off" || rv.Scope != ScopeSession {
		t.Fatalf("expose from session = %+v, want off/session", rv)
	}

	// Neither knob is stored inside agent.tier_routing, whose strict decoder
	// would refuse an unknown field; the routing value stays absent.
	routing, err := LoadTierRouting(opts)
	if err != nil || routing.Nodes != nil || routing.Bundles != nil {
		t.Fatalf("routing after knob writes = %+v, %v; want untouched", routing, err)
	}
	if _, err := ParseTierRouting(json.RawMessage(`{"expose":"on"}`)); err == nil {
		t.Fatal("agent.tier_routing must keep refusing unknown fields; the knobs are sibling keys")
	}
}
