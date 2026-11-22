package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
)

const rolePlaybookContent = `---
kind: render
role: worker
tier: medium
tier-role: ticket-worker
tier-parent: implement:*
---
# Role Playbook

Do the work.
`

const (
	piInitNative = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"ws-pi-bridge"}}}`
	piInitDaemon = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"ws-pi-bridge"},"capabilities":{"experimental":{"wsPiDaemonAgents":true}}}}`
)

// roleRenderFixture is a Pi lead whose package holds one role playbook.
func roleRenderFixture(t *testing.T) *piRecordFixture {
	t.Helper()
	useResourceFixture(t, buildTestRsrcTree(t, map[string]string{
		"role-pb/role-pb.md": rolePlaybookContent,
	}))
	return newPiRecordFixture(t)
}

// renderResponse completes the initialize request, renders name through
// playbook.render, and returns the response lines (path first).
func renderResponse(t *testing.T, s *Server, key, name, initialize string, extra map[string]any) []string {
	t.Helper()
	var out bytes.Buffer
	if err := s.ServeStdio(context.Background(), strings.NewReader(initialize+"\n"), &out); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	args := map[string]any{"name": name, "session_key": key}
	for k, v := range extra {
		args[k] = v
	}
	return renderLines(t, callToolOnce(t, s, 2, "playbook.render", args))
}

func setRouting(t *testing.T, s *Server, raw string) {
	t.Helper()
	resolver := wsconfig.NewResolver(s.configOptions(), nil, nil, nil)
	if err := resolver.SetStructured(wsconfig.ItemAgentTierRouting, json.RawMessage(raw), wsconfig.ScopeGlobal); err != nil {
		t.Fatal(err)
	}
}

func metadata(lines []string) string { return strings.Join(lines[1:], "\n") }

// A daemon-agent caller is recommended the role tier and the binding the
// graph resolves; a native caller keeps the bare size and its own
// harness's binding, mapping or not.
func TestRenderRoleRecommendationDaemonVersusNative(t *testing.T) {
	f := roleRenderFixture(t)
	nativeFallback := metadata(strings.Split(withRecommendedRenderBinding("path", wsagent.BackendPi, "medium", f.s.configOptions()), "\n"))

	// Pi sets no medium slot: a native caller keeps the config fallback
	// chain's binding, but agent.spawn refuses pi.medium, so the role
	// recommendation advertises no binding instead of another harness's.
	if daemon := metadata(renderResponse(t, f.s, f.leadKey, "role-pb", piInitDaemon, nil)); daemon != "recommended-tier: ticket-worker:medium" {
		t.Fatalf("slotless daemon metadata = %q", daemon)
	}
	if native := metadata(renderResponse(t, f.s, f.leadKey, "role-pb", piInitNative, nil)); native != nativeFallback {
		t.Fatalf("slotless native metadata = %q, want %q", native, nativeFallback)
	}

	resp := callToolOnce(t, f.s, 9, "config.tune", map[string]any{"key": "agents.tier", "scope": "global", "harness": "pi",
		"value": map[string]any{"tier": "medium", "model": "pi-mid", "effort": "high"}})
	if toolIsError(t, resp) {
		t.Fatalf("set pi.medium: %s", resp)
	}
	nativeBaseline := metadata(strings.Split(withRecommendedRenderBinding("path", wsagent.BackendPi, "medium", f.s.configOptions()), "\n"))
	if nativeBaseline != "recommended-tier: medium\nrecommended-model: pi-mid\nrecommended-reasoning-effort: high" {
		t.Fatalf("baseline = %q", nativeBaseline)
	}

	// Default chain: the daemon caller's binding is the bare size's.
	daemon := metadata(renderResponse(t, f.s, f.leadKey, "role-pb", piInitDaemon, nil))
	if want := strings.Replace(nativeBaseline, "recommended-tier: medium", "recommended-tier: ticket-worker:medium", 1); daemon != want {
		t.Fatalf("default-chain daemon metadata = %q, want %q", daemon, want)
	}
	if native := metadata(renderResponse(t, f.s, f.leadKey, "role-pb", piInitNative, nil)); native != nativeBaseline {
		t.Fatalf("default-chain native metadata = %q, want %q", native, nativeBaseline)
	}

	// A cross-harness mapping moves only the daemon caller's binding.
	setRouting(t, f.s, `{"nodes":{"implement":{"*":"claude.*"}}}`)
	daemon = metadata(renderResponse(t, f.s, f.leadKey, "role-pb", piInitDaemon, nil))
	if want := "recommended-tier: ticket-worker:medium\nrecommended-model: sonnet\nrecommended-reasoning-effort: high"; daemon != want {
		t.Fatalf("mapped daemon metadata = %q, want %q", daemon, want)
	}
	if native := metadata(renderResponse(t, f.s, f.leadKey, "role-pb", piInitNative, nil)); native != nativeBaseline {
		t.Fatalf("mapped native metadata = %q, want %q", native, nativeBaseline)
	}

	// tier_override keeps its bare validation and replaces the role's size.
	daemon = metadata(renderResponse(t, f.s, f.leadKey, "role-pb", piInitDaemon, map[string]any{"tier_override": "opus"}))
	if want := "recommended-tier: ticket-worker:large\nrecommended-model: opus\nrecommended-reasoning-effort: high"; daemon != want {
		t.Fatalf("override daemon metadata = %q, want %q", daemon, want)
	}
	resp = callToolOnce(t, f.s, 3, "playbook.render", map[string]any{"name": "role-pb", "session_key": f.leadKey, "tier_override": "ticket-worker:large"})
	if !toolIsError(t, resp) || !strings.Contains(toolText(t, resp), "tier_override") {
		t.Fatalf("a role-shaped tier_override must stay rejected: %s", resp)
	}

	// An invalid graph keeps the role recommendation without a binding.
	setRouting(t, f.s, `{"nodes":{"implement":{"*":"nosuch:*"}}}`)
	if daemon := metadata(renderResponse(t, f.s, f.leadKey, "role-pb", piInitDaemon, nil)); daemon != "recommended-tier: ticket-worker:medium" {
		t.Fatalf("invalid-graph daemon metadata = %q", daemon)
	}
}

func TestConfigResolveAgentRoutesSpawnTiers(t *testing.T) {
	f := roleRenderFixture(t)
	setRouting(t, f.s, `{
		"nodes":{"implement":{"*":"claude.*"},"review":{"xlarge":"pi.*"}},
		"bundles":{"discussion":{"harness":"agy","model":"claude-opus-4-6-thinking","effort":"high"}}
	}`)
	resolve := func(tier string) (resolveAgentTierResult, string) {
		t.Helper()
		resp := callToolOnce(t, f.s, 4, "config.resolve_agent", map[string]any{"tier": tier, "harness": "pi", "format": "json"})
		if toolIsError(t, resp) {
			return resolveAgentTierResult{}, toolText(t, resp)
		}
		var got resolveAgentTierResult
		if err := json.Unmarshal([]byte(toolText(t, resp)), &got); err != nil {
			t.Fatalf("decode %s: %v", resp, err)
		}
		return got, ""
	}
	want, err := func() (resolveAgentTierResult, error) {
		backend, model, effort, from, err := wsconfig.ResolveAgentTierForHarness(f.s.configOptions(), "medium", "pi")
		return resolveAgentTierResult{Backend: backend, Model: model, Effort: effort, ResolvedFrom: from}, err
	}()
	if err != nil {
		t.Fatal(err)
	}
	if bare, errText := resolve("medium"); errText != "" || bare != want {
		t.Fatalf("bare size = %+v %s, want the pre-change resolution %+v", bare, errText, want)
	}
	if got, errText := resolve("ticket-worker:medium"); errText != "" || got.Backend != "claude" || got.Model != "sonnet" || got.ResolvedFrom != "claude" ||
		got.Trace != "ticket-worker:medium -> implement:medium -> claude.medium" {
		t.Fatalf("role tier = %+v %s", got, errText)
	}
	if got, errText := resolve("discussion"); errText != "" || got.Backend != "agy" || got.Model != "claude-opus-4-6-thinking" || got.ResolvedFrom != "bundle" {
		t.Fatalf("bundle = %+v %s", got, errText)
	}
	if got, errText := resolve("claude.large"); errText != "" || got.Model != "opus" || got.Trace != "claude.large" {
		t.Fatalf("explicit target = %+v %s", got, errText)
	}
	// review:xlarge lands on pi.xlarge, which Pi does not set: agent.spawn
	// refuses it, so config.resolve_agent does too rather than answering
	// with another harness's slot.
	if got, errText := resolve("review:xlarge"); !strings.Contains(errText, "pi.xlarge has no model in agents.tier") {
		t.Fatalf("slotless routed tier = %+v %q", got, errText)
	}
	// With a runner of its own, the server applies the spawn's harness
	// availability check: no harness is registered here.
	f.s.pi.agents = wsagent.NewRunner(wsagent.Options{})
	if got, errText := resolve("ticket-worker:medium"); !strings.Contains(errText, `harness "claude" is not registered in this daemon`) {
		t.Fatalf("unavailable harness = %+v %q", got, errText)
	}
	if got, errText := resolve("discussion"); !strings.Contains(errText, `harness "agy" is not registered`) {
		t.Fatalf("unavailable bundle harness = %+v %q", got, errText)
	}
	f.s.pi.agents = nil
	if _, errText := resolve("nosuch:large"); !strings.Contains(errText, "tier must be small, medium, large, or xlarge") || !strings.Contains(errText, "not a tier node") {
		t.Fatalf("unknown role error = %q", errText)
	}
}

func TestPackagedTierRolesFromAResourceTree(t *testing.T) {
	root := buildTestRsrcTree(t, map[string]string{
		"role-pb/role-pb.md":   rolePlaybookContent,
		"plain-pb/plain-pb.md": "---\nkind: render\ntier: small\n---\nbody\n",
	})
	roles, err := packagedTierRoles(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 || roles[0] != (wsconfig.TierRole{Name: "ticket-worker", Parent: "implement:*", Size: "medium"}) {
		t.Fatalf("roles = %+v", roles)
	}
}

func TestFormatAgentTiersRoutingSection(t *testing.T) {
	base := wsagent.AgentTiers{Harness: "claude"}
	if strings.Contains(formatAgentTiers(base), "routing") {
		t.Fatal("a listing without a graph printed a routing section")
	}
	base.Routing = &wsagent.AgentRouting{
		Supported: true,
		Bundles:   []wsagent.AgentRouteEntry{{Tier: "discussion", Trace: "discussion", Harness: "agy", Error: `harness "agy" has no spawnable tier now`}},
		Routes: []wsagent.AgentRouteEntry{
			{Tier: "ticket-worker:medium", Trace: "ticket-worker:medium -> implement:medium -> medium -> claude.medium", Model: "sonnet", Effort: "high"},
			{Tier: "x:small", Error: "boom"},
		},
		Collisions: []string{"alias a -> b shadows bundle a"},
	}
	text := formatAgentTiers(base)
	for _, want := range []string{
		"routing (agent.tier_routing;",
		"  discussion: not spawnable now: harness \"agy\" has no spawnable tier now\n",
		"  ticket-worker:medium -> implement:medium -> medium -> claude.medium: sonnet/high\n",
		"  x:small: not spawnable now: boom\n",
		"  collision: alias a -> b shadows bundle a\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("agent.tiers text missing %q:\n%s", want, text)
		}
	}
	base.Routing = &wsagent.AgentRouting{Supported: true, Error: "agent.tier_routing: pointer cycle a -> b -> a"}
	if text := formatAgentTiers(base); !strings.Contains(text, "  unavailable: agent.tier_routing: pointer cycle a -> b -> a\n") {
		t.Fatalf("invalid graph text:\n%s", text)
	}
}
