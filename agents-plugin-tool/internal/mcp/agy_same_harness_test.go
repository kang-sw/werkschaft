package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
)

// agyCrossHarnessText is the cross-harness refusal of the daemon's own agy
// registration (261122-feat-ws-agy-same-harness-workers, Decision 8).
const agyCrossHarnessText = `harness "agy" accepts workers only from an agy session: Antigravity's consumer terms do not support running agy under another agent`

// shippedAgyRegistrations are the descriptor registrations with the
// daemon's own agy registration over a transport-only backend: no agy
// install is looked up or run.
func shippedAgyRegistrations() map[string]wsagent.BackendRegistration {
	regs := descriptorRegistrations()
	shipped := wsagent.BuiltinBackendRegistrations()[wsagent.BackendAgy]
	shipped.Backend = &lifecycleBackend{}
	regs[wsagent.BackendAgy] = shipped
	return regs
}

func agentTiersJSON(t *testing.T, s *Server, key, tool string) wsagent.AgentTiers {
	t.Helper()
	var tiers wsagent.AgentTiers
	if err := json.Unmarshal([]byte(callToolWithKey(t, s, 2, key, tool, map[string]any{"format": "json"})), &tiers); err != nil {
		t.Fatal(err)
	}
	return tiers
}

// A hosted agy lead's connection is verified on its own agy record: its
// agent.tiers offers the agy bare tiers and lists no agy refusal.
func TestHostedAgyLeadAgentTiersOffersAgyTiers(t *testing.T) {
	s, f := hostedBackendConnection(t, wsagent.BackendAgy, shippedAgyRegistrations())
	if got := s.agentAdmissionBackend(); got != wsagent.BackendAgy {
		t.Fatalf("admission backend = %q, want agy", got)
	}
	tiers := agentTiersJSON(t, s, f.hosted, "agent_tiers")
	if tiers.Harness != wsagent.BackendAgy || len(tiers.Native) != 4 {
		t.Fatalf("harness %q, native %+v", tiers.Harness, tiers.Native)
	}
	if slices.ContainsFunc(tiers.Unavailable, func(e string) bool { return strings.HasPrefix(e, "agy: ") }) {
		t.Fatalf("unavailable = %v", tiers.Unavailable)
	}
}

// A connection that only declares agy (a launcher hello's harness, or the
// initialize clientInfo name) without a daemon-bound agy conversation is refused
// agy workers: agent.tiers lists agy under Unavailable with the
// cross-harness text and agent.spawn of an agy tier, explicit or bare, is
// refused with it. The declared harness still selects tiers.
func TestDeclaredAgyConnectionWithoutAnAgyRecordIsRefusedAgyWorkers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		connect  func(t *testing.T) (*Server, string)
		verified string
	}{
		{"hosted claude lead whose hello declares agy", func(t *testing.T) (*Server, string) {
			s, f := hostedBackendConnection(t, wsagent.BackendClaude, shippedAgyRegistrations())
			s.observeHarness("hello", wsagent.BackendAgy)
			return s, f.hosted
		}, wsagent.BackendClaude},
		{"unhosted connection whose initialize declares agy", func(t *testing.T) (*Server, string) {
			f := newHostedKeyFixture(t)
			s := NewServer(f.root, "test")
			runner := wsagent.NewRunner(wsagent.Options{Backends: shippedAgyRegistrations()})
			s.pi.agents, s.callerBinding = runner, runner
			var out bytes.Buffer
			line := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"agy","version":"0"}}}` + "\n"
			if err := s.ServeStdio(context.Background(), strings.NewReader(line), &out); err != nil {
				t.Fatal(err)
			}
			return s, f.hosted
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, key := tc.connect(t)
			if got := s.playbookHarness(); got != wsagent.BackendAgy {
				t.Fatalf("declared harness = %q, want agy", got)
			}
			if got := s.agentAdmissionBackend(); got != tc.verified {
				t.Fatalf("admission backend = %q, want %q", got, tc.verified)
			}
			tiers := agentTiersJSON(t, s, key, "agent.tiers")
			if tiers.Harness != wsagent.BackendAgy || len(tiers.Native) != 0 || !slices.Contains(tiers.Unavailable, "agy: "+agyCrossHarnessText) {
				t.Fatalf("harness %q, native %+v, unavailable %v", tiers.Harness, tiers.Native, tiers.Unavailable)
			}
			for _, tier := range []string{"agy.small", "small"} {
				text := callToolWithKey(t, s, 3, key, "agent.spawn", map[string]any{"prompt": "x", "tier": tier, "authority": "read-only"})
				if !strings.Contains(text, agyCrossHarnessText) {
					t.Fatalf("agent.spawn %s = %s", tier, text)
				}
			}
		})
	}
}

// The MCP routed binding (playbook.render's recommendation, and
// config.resolve_agent) refuses a route whose terminal refuses the
// connection's verified caller (its token-bound conversation's backend): a role tier falling through to agy returns
// no binding for a connection not verified on agy, even at the caller
// fallthrough, and the binding for a verified agy lead.
func TestRoutedBindingReturnsNoBindingAfterACrossHarnessRefusal(t *testing.T) {
	const tier = "playbook-explore:small"
	claude, _ := hostedBackendConnection(t, wsagent.BackendClaude, shippedAgyRegistrations())
	opts := claude.configOptions()
	res, _, err := claude.routedBinding(tier, wsagent.BackendAgy, opts)
	if !res.CallerFallthrough || res.Harness != wsagent.BackendAgy || err == nil || !strings.Contains(err.Error(), agyCrossHarnessText) {
		t.Fatalf("routed binding = %+v, %v; want the agy fallthrough refused", res, err)
	}
	if model, effort, ok := claude.renderTierBinding(wsagent.BackendAgy, wsagent.BackendAgy, tier, opts); ok || model != "" || effort != "" {
		t.Fatalf("render binding after a refusal = %q %q %v", model, effort, ok)
	}
	if got, err := claude.resolveAgentTier(tier, wsagent.BackendAgy); err == nil || !strings.Contains(err.Error(), agyCrossHarnessText) {
		t.Fatalf("config.resolve_agent after a refusal = %+v, %v", got, err)
	}
	// The claude fallthrough is unaffected for the same connection.
	if _, _, ok := claude.renderTierBinding(wsagent.BackendClaude, wsagent.BackendClaude, tier, opts); !ok {
		t.Fatal("a claude fallthrough lost its binding")
	}

	agy, _ := hostedBackendConnection(t, wsagent.BackendAgy, shippedAgyRegistrations())
	if model, _, ok := agy.renderTierBinding(wsagent.BackendAgy, wsagent.BackendAgy, tier, agy.configOptions()); !ok || model == "" {
		t.Fatalf("an agy lead's render binding = %q %v", model, ok)
	}
	if got, err := agy.resolveAgentTier(tier, wsagent.BackendAgy); err != nil || got.Backend != wsagent.BackendAgy {
		t.Fatalf("an agy lead's config.resolve_agent = %+v, %v", got, err)
	}
}

// A connection whose hosted-session variables and on-disk record name an
// agy lead, but which no session token binds to that conversation, is
// refused agy workers: the record file is not the binding.
func TestHostedAgyRecordWithoutATokenBindingIsRefusedAgyWorkers(t *testing.T) {
	s, f := hostedBackendConnection(t, wsagent.BackendAgy, shippedAgyRegistrations())
	s.conversation = ""
	if s.hostedSessionKey() != f.hosted {
		t.Fatal("precondition: the variables name the hosted agy lead")
	}
	if got := s.agentAdmissionBackend(); got != "" {
		t.Fatalf("admission backend = %q, want none", got)
	}
	tiers := agentTiersJSON(t, s, f.hosted, "agent.tiers")
	if len(tiers.Native) != 0 || !slices.Contains(tiers.Unavailable, "agy: "+agyCrossHarnessText) {
		t.Fatalf("native %+v, unavailable %v", tiers.Native, tiers.Unavailable)
	}
}

// A forwarding worker's server holds no runner (its agent.* calls go over
// the capability token), yet its routed bindings still check the caller it
// is authenticated as against the runner's memory: a Claude worker is
// refused an agy-ending route and keeps its Claude one, and an agy worker
// resolves the agy route.
func TestForwardingServerRoutedBindingChecksItsAuthenticatedCaller(t *testing.T) {
	const tier = "playbook-explore:small"
	f := newHostedKeyFixture(t)
	runner := wsagent.NewRunner(wsagent.Options{Backends: shippedAgyRegistrations()})
	binding := boundCallers{Runner: runner, backends: map[string]string{"token:claude-capability": wsagent.BackendClaude, "agyworker": wsagent.BackendAgy}}
	t.Setenv(wsagent.EnvPiToken, "claude-capability")
	claude := NewServer(f.root, "test")
	claude.callerBinding = binding
	if claude.pi.agents != nil || claude.agentAdmissionBackend() != wsagent.BackendClaude {
		t.Fatalf("precondition: a forwarding Claude worker (runner %v, caller %q)", claude.pi.agents, claude.agentAdmissionBackend())
	}
	opts := claude.configOptions()
	if _, err := claude.resolveAgentTier("agy.small", wsagent.BackendClaude); err == nil || !strings.Contains(err.Error(), agyCrossHarnessText) {
		t.Fatalf("a Claude worker's agy.small = %v", err)
	}
	if model, _, ok := claude.renderTierBinding(wsagent.BackendAgy, wsagent.BackendAgy, tier, opts); ok || model != "" {
		t.Fatalf("a Claude worker's agy fallthrough render binding = %q %v", model, ok)
	}
	if got, err := claude.resolveAgentTier("claude.small", wsagent.BackendClaude); err != nil || got.Backend != wsagent.BackendClaude {
		t.Fatalf("a Claude worker's claude.small = %+v, %v", got, err)
	}

	agy := NewServer(f.root, "test")
	agy.callerBinding, agy.conversation = binding, "agyworker"
	if got, err := agy.resolveAgentTier("agy.small", wsagent.BackendAgy); err != nil || got.Backend != wsagent.BackendAgy {
		t.Fatalf("an agy worker's agy.small = %+v, %v", got, err)
	}
	if model, _, ok := agy.renderTierBinding(wsagent.BackendAgy, wsagent.BackendAgy, tier, agy.configOptions()); !ok || model == "" {
		t.Fatalf("an agy worker's render binding = %q %v", model, ok)
	}
}
