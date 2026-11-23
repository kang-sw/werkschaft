// agent_tier_routing_test.go holds agent.spawn and agent.tiers tests for
// the agent.tier_routing graph: role tiers, bundles, and tier ceilings.
package wsagent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsrsrc"
)

var testPackagedRoles = []wsconfig.TierRole{
	{Name: "ticket-worker", Parent: "implement:*", Size: "medium"},
	{Name: "ticket-reviewer-design", Parent: "review:*", Size: "large"},
}

func routingHarness(t *testing.T) *harness {
	t.Helper()
	return routingHarnessWithRoles(t, testPackagedRoles)
}

func routingHarnessWithRoles(t *testing.T, roles []wsconfig.TierRole) *harness {
	t.Helper()
	h, _ := agentHarness(t, func(o *Options) {
		o.Backends[BackendCodex] = BackendRegistration{
			Backend: o.Backends[BackendClaude].Backend, WorkerCapable: true, WorkerProfile: AgentWorkerProfile(BackendCodex),
		}
		o.TierRoles = func() ([]wsconfig.TierRole, error) { return roles, nil }
		for _, tier := range []string{"small", "large"} {
			if _, err := wsconfig.SetGlobalAgentsTierForHarness(o.Config, tier, "", "codex-"+tier, BackendCodex, "low"); err != nil {
				t.Fatal(err)
			}
		}
	})
	return h
}

func setTierRouting(t *testing.T, cfg wsconfig.Options, raw string) {
	t.Helper()
	resolver := wsconfig.NewResolver(cfg, nil, nil, nil)
	if err := resolver.SetStructured(wsconfig.ItemAgentTierRouting, json.RawMessage(raw), wsconfig.ScopeGlobal); err != nil {
		t.Fatal(err)
	}
}

type spawnBinding struct{ Backend, Model, Effort, Tier string }

func (h *harness) spawnBinding(caller AgentCaller, tier string) spawnBinding {
	h.t.Helper()
	id := h.agentSpawn(caller, AgentSpawnRequest{Prompt: "x", Tier: tier, Authority: "read-only"})
	rec := h.record(id)
	return spawnBinding{Backend: rec.Backend, Model: rec.Model, Effort: rec.Effort, Tier: rec.Tier}
}

// With no user config every role tier launches exactly as the bare size
// does on the caller's harness.
func TestAgentSpawnDefaultRoleChainMatchesTheBareSize(t *testing.T) {
	h := routingHarness(t)
	lead := h.leadCaller()
	for _, size := range []string{"small", "medium", "large"} {
		bare := h.spawnBinding(lead, size)
		for _, role := range testPackagedRoles {
			if got := h.spawnBinding(lead, role.Name+":"+size); got != bare {
				t.Fatalf("%s:%s = %+v, bare %s = %+v", role.Name, size, got, size, bare)
			}
		}
	}
}

// A caller whose harness sets no slot at the size is refused for the bare
// size and for an unmapped role tier alike: render and config.resolve_agent
// report the bare size's config fallback for both, but agent.spawn never
// launches another harness's slot.
func TestAgentSpawnRefusesAnUnsetCallerSlotForBareAndRoleTiers(t *testing.T) {
	h := routingHarness(t)
	pi := LeadCaller(leadParent.ReplyID, h.root, BackendWerk, h.config.Config)
	if got := h.spawnBinding(pi, "small"); got.Backend != BackendWerk {
		t.Fatalf("pi small = %+v, want a werk launch", got)
	}
	spawned := len(h.runner.AgentList(pi))
	// The fixture's pi bucket sets small, medium, and large, not xlarge.
	for _, tier := range []string{"xlarge", "ticket-worker:xlarge"} {
		_, err := h.runner.AgentSpawn(pi, AgentSpawnRequest{Prompt: "x", Tier: tier, Authority: "read-only"})
		agentRefused(t, err, http.StatusBadRequest, "werk.xlarge has no usable model")
	}
	if n := len(h.runner.AgentList(pi)); n != spawned {
		t.Fatalf("a refused tier spawned: %d children, was %d", n, spawned)
	}
}

func TestAgentSpawnRoutesRoleTiersAndBundles(t *testing.T) {
	h := routingHarness(t)
	setTierRouting(t, h.config.Config, `{
		"nodes":{"review":{"*":"codex.*","medium":"discussion"},"implement":{"large":"claude.small"}},
		"bundles":{"discussion":{"harness":"codex","model":"codex-thinking","effort":"high"},"faraway":{"harness":"agy","model":"m"}}
	}`)
	lead := h.leadCaller()
	for _, tc := range []struct {
		tier string
		want spawnBinding
	}{
		// A group mapping moves the role to another harness.
		{"ticket-reviewer-design:small", spawnBinding{BackendCodex, "codex-small", "low", "small"}},
		// A per-size exception routes to a bundle, keeping the routed size.
		{"ticket-reviewer-design:medium", spawnBinding{BackendCodex, "codex-thinking", "high", "medium"}},
		// A pinned target changes the size.
		{"ticket-worker:large", spawnBinding{BackendClaude, "claude-small", "high", "small"}},
		// A bare bundle name supplies no size and records xlarge.
		{"discussion", spawnBinding{BackendCodex, "codex-thinking", "high", "xlarge"}},
		// An explicit target wins over every mapping.
		{"claude.medium", spawnBinding{BackendClaude, "claude-medium", "high", "medium"}},
	} {
		if got := h.spawnBinding(lead, tc.tier); got != tc.want {
			t.Fatalf("tier %q = %+v, want %+v", tc.tier, got, tc.want)
		}
	}
	spawned := len(h.runner.AgentList(lead))
	for tier, why := range map[string]string{
		"nosuch:large":         `"nosuch" is not a tier node`,
		"faraway":              `harness "agy" is not registered`,
		"ticket-worker:xlarge": "claude.xlarge",
		"nobundle":             "no bundle in agent.tier_routing",
	} {
		_, err := h.runner.AgentSpawn(lead, AgentSpawnRequest{Prompt: "x", Tier: tier, Authority: "read-only"})
		if tier == "ticket-worker:xlarge" {
			// claude's xlarge is the builtin floor's: it resolves.
			if err != nil {
				t.Fatalf("tier %q: %v", tier, err)
			}
			spawned++
			continue
		}
		agentRefused(t, err, http.StatusBadRequest, why, "valid tiers: ")
		_, valid, _ := strings.Cut(err.Error(), "valid tiers: ")
		if !strings.Contains(valid, "discussion") || strings.Contains(valid, "faraway") {
			t.Fatalf("tier %q: the valid list must name spawnable bundles only: %v", tier, err)
		}
	}
	if n := len(h.runner.AgentList(lead)); n != spawned {
		t.Fatalf("a refused tier spawned: %d children, want %d", n, spawned)
	}
}

func TestAgentSpawnRoutedTierCeilings(t *testing.T) {
	h := routingHarness(t)
	setTierRouting(t, h.config.Config, `{
		"nodes":{"review":{"*":"discussion"}},
		"bundles":{"discussion":{"harness":"claude","model":"claude-thinking","effort":"xhigh"}}
	}`)
	lead := h.leadCaller()
	small, _ := h.agentRunning(lead, AgentSpawnRequest{Prompt: "small holder", Tier: "small", Authority: "read-only"})
	holder := h.holderOf(small)
	for tier, want := range map[string]string{
		"ticket-worker:medium": `tier "medium" is above this conversation's own tier "small"`,
		// A routed bundle uses its routed size.
		"ticket-reviewer-design:medium": `tier "medium" is above this conversation's own tier "small"`,
		// A bare bundle name ranks xlarge, never below a parent's ceiling.
		"discussion": `tier "xlarge" is above this conversation's own tier "small"`,
	} {
		_, err := h.runner.AgentSpawn(holder, AgentSpawnRequest{Prompt: "x", Tier: tier, Authority: "read-only"})
		agentRefused(t, err, http.StatusForbidden, want)
	}
	// At the ceiling, a routed role and a routed bundle are admitted.
	if got := h.spawnBinding(holder, "ticket-worker:small"); got.Tier != "small" || got.Model != "claude-small" {
		t.Fatalf("routed role at the ceiling = %+v", got)
	}
	if got := h.spawnBinding(holder, "ticket-reviewer-design:small"); got.Tier != "small" || got.Model != "claude-thinking" || got.Effort != "xhigh" {
		t.Fatalf("routed bundle at the ceiling = %+v", got)
	}
	// An xlarge holder may spawn the bare bundle.
	big, _ := h.agentRunning(lead, AgentSpawnRequest{Prompt: "big holder", Tier: "discussion", Authority: "read-only"})
	if got := h.spawnBinding(h.holderOf(big), "discussion"); got.Tier != "xlarge" {
		t.Fatalf("bare bundle under an xlarge holder = %+v", got)
	}
}

func TestAgentSpawnRoutingKeepsLegacyAliasPrecedenceAndRefusesAnInvalidGraph(t *testing.T) {
	h := routingHarness(t)
	setTierAliases(t, h.config.Config, "ticket-worker:large=werk.large implement:small=werk.small")
	setTierRouting(t, h.config.Config, `{"nodes":{"implement":{"*":"claude.xlarge"}}}`)
	lead := h.leadCaller()
	if got := h.spawnBinding(lead, "ticket-worker:large"); got.Backend != BackendWerk || got.Model != "prov/large-model" {
		t.Fatalf("whole-token alias = %+v", got)
	}
	if got := h.spawnBinding(lead, "ticket-worker:small"); got.Backend != BackendWerk || got.Model != "prov/small-model" {
		t.Fatalf("alias at a walked node = %+v", got)
	}
	if got := h.spawnBinding(lead, "ticket-worker:medium"); got.Backend != BackendClaude || got.Tier != "xlarge" {
		t.Fatalf("pointer = %+v", got)
	}

	// A hand-edited cycle refuses role tiers and keeps legacy ones.
	setTierRouting(t, h.config.Config, `{"nodes":{"a":{"small":"b:large"},"b":{"medium":"a:small"}}}`)
	_, err := h.runner.AgentSpawn(lead, AgentSpawnRequest{Prompt: "x", Tier: "ticket-worker:medium", Authority: "read-only"})
	agentRefused(t, err, http.StatusBadRequest, "pointer cycle a -> b -> a")
	h.spawnBinding(lead, "medium")
	h.spawnBinding(lead, "ticket-worker:large")
}

func TestAgentTiersRoutingSection(t *testing.T) {
	h := routingHarness(t)
	setTierAliases(t, h.config.Config, "discussion=werk.small")
	setTierRouting(t, h.config.Config, `{
		"nodes":{"review":{"*":"codex.*"},"legacy":{"*":"pi.*"},"mine":{}},
		"bundles":{"discussion":{"harness":"claude","model":"claude-thinking","effort":"high"},"faraway":{"harness":"agy","model":"m"}}
	}`)
	got, err := h.runner.AgentTiers(h.leadCaller())
	if err != nil {
		t.Fatal(err)
	}
	r := got.Routing
	if r == nil || !r.Supported || r.Error != "" {
		t.Fatalf("routing = %+v", r)
	}
	routes := map[string]AgentRouteEntry{}
	for _, e := range r.Routes {
		routes[e.Tier] = e
	}
	if e := routes["ticket-worker:medium"]; e.Trace != "ticket-worker:medium -> implement:medium -> medium -> claude.medium" || e.Model != "claude-medium" || e.Error != "" {
		t.Fatalf("role route = %+v", e)
	}
	if e := routes["ticket-reviewer-design:large"]; e.Trace != "ticket-reviewer-design:large -> review:large -> codex.large" || e.Model != "codex-large" {
		t.Fatalf("mapped role route = %+v", e)
	}
	if _, listed := routes["ticket-worker:large"]; listed {
		t.Fatal("a role is listed at a size its playbook does not declare")
	}
	if e := routes["legacy:xlarge"]; e.Error != `tier "xlarge" is not set for werk` {
		t.Fatalf("unset slot route = %+v", e)
	}
	if e := routes["mine:small"]; e.Trace != "mine:small -> small -> claude.small" {
		t.Fatalf("user node route = %+v", e)
	}
	// The alias shadows the bundle's name, so the token spawns the alias.
	if len(r.Bundles) != 2 || r.Bundles[0] != (AgentRouteEntry{Tier: "discussion", Trace: "discussion -> werk.small [alias discussion]", Harness: BackendWerk, Model: "prov/small-model", Effort: "low"}) ||
		r.Bundles[1].Error != `harness "agy" is not registered in this daemon` {
		t.Fatalf("bundles = %+v", r.Bundles)
	}
	if len(r.Collisions) != 1 || !strings.Contains(r.Collisions[0], "alias discussion -> werk.small shadows bundle discussion") {
		t.Fatalf("collisions = %v", r.Collisions)
	}

	// An invalid graph is reported, not fatal to the listing.
	setTierRouting(t, h.config.Config, `{"nodes":{"review":{"*":"nosuch:*"}}}`)
	got, err = h.runner.AgentTiers(h.leadCaller())
	if err != nil || got.Routing == nil || !strings.Contains(got.Routing.Error, `unknown node "nosuch"`) || len(got.Native) == 0 {
		t.Fatalf("invalid graph listing = %+v, %v", got, err)
	}
}

// legacyAgentTiersB1d236bc2 is agent.tiers' answer shape before the routing
// graph (b1d236bc2:agents-plugin-tool/internal/wsagent/agent_tools.go:169-177).
type legacyAgentTiersB1d236bc2 struct {
	Harness     string            `json:"harness"`
	Native      []AgentTierEntry  `json:"native"`
	Ext         []AgentTierEntry  `json:"ext"`
	Aliases     []AgentAliasEntry `json:"aliases"`
	Unavailable []string          `json:"unavailable,omitempty"`
}

// legacyResolveTokenB1d236bc2 is the pre-feature agent.spawn token
// resolution (b1d236bc2:agents-plugin-tool/internal/wsagent/agent_tools.go:
// 315-339): explicit, bare, then an exact alias, else a refusal before any
// child is created.
func legacyResolveTokenB1d236bc2(aliases map[string]string, callerHarness, tier string) (harness, bare string, err error) {
	raw := strings.ToLower(strings.TrimSpace(tier))
	switch h, t, dotted := strings.Cut(raw, "."); {
	case dotted:
		return h, wsconfig.NormalizedTier(t), nil
	case wsconfig.NormalizedTier(raw) != "":
		return callerHarness, wsconfig.NormalizedTier(raw), nil
	default:
		target, ok := aliases[raw]
		if !ok {
			return "", "", fmt.Errorf("tier %q is neither a tier, <harness>.<tier>, nor an alias in %s", tier, wsconfig.ItemAgentTierAliases)
		}
		h, t, _ := strings.Cut(target, ".")
		return h, t, nil
	}
}

// A role rendered by the new daemon and sent to the pre-feature resolver:
// without an alias it is refused before creation and agent.tiers has no
// routing section; with an exact alias both versions launch one binding.
func TestRoleTierAgainstThePreFeatureResolver(t *testing.T) {
	h := routingHarness(t)
	lead := h.leadCaller()
	const role = "ticket-worker:large"

	if _, _, err := legacyResolveTokenB1d236bc2(map[string]string{}, BackendClaude, role); err == nil || !strings.Contains(err.Error(), "neither a tier") {
		t.Fatalf("pre-feature resolver accepted %q: %v", role, err)
	}
	legacy, err := json.Marshal(legacyAgentTiersB1d236bc2{Harness: BackendClaude, Native: []AgentTierEntry{}, Ext: []AgentTierEntry{}, Aliases: []AgentAliasEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	var seen AgentTiers
	if err := json.Unmarshal(legacy, &seen); err != nil || seen.Routing != nil {
		t.Fatalf("a pre-feature agent.tiers must read as routing unsupported: %+v, %v", seen.Routing, err)
	}
	current, err := h.runner.AgentTiers(lead)
	if err != nil || current.Routing == nil || !current.Routing.Supported {
		t.Fatalf("the new daemon must report routing support: %+v, %v", current.Routing, err)
	}

	setTierAliases(t, h.config.Config, role+"=werk.large")
	aliases, err := wsconfig.LoadTierAliases(h.config.Config)
	if err != nil {
		t.Fatal(err)
	}
	oldHarness, oldBare, err := legacyResolveTokenB1d236bc2(aliases, BackendClaude, role)
	if err != nil {
		t.Fatal(err)
	}
	tiers, err := wsconfig.HarnessTiers(h.config.Config, oldHarness)
	if err != nil {
		t.Fatal(err)
	}
	old := spawnBinding{Backend: oldHarness, Model: tiers[oldBare].Model, Effort: tiers[oldBare].Effort, Tier: oldBare}
	if got := h.spawnBinding(lead, role); got != old {
		t.Fatalf("exact alias: new daemon %+v, pre-feature %+v", got, old)
	}
}

// shippedRoutingHarness is routingHarness over the role nodes the shipped
// ws package declares in its playbook frontmatter.
func shippedRoutingHarness(t *testing.T) (*harness, []wsrsrc.PlaybookTierRole) {
	t.Helper()
	declared, err := wsrsrc.TierRoles(os.DirFS(filepath.Join("..", "..", "..", "agents-plugin", "rsrc")))
	if err != nil || len(declared) == 0 {
		t.Fatalf("shipped tier roles = %v, %v", declared, err)
	}
	roles := make([]wsconfig.TierRole, 0, len(declared))
	for _, d := range declared {
		roles = append(roles, wsconfig.TierRole{Name: d.Role, Parent: d.Parent, Size: wsconfig.NormalizedTier(d.Size)})
	}
	return routingHarnessWithRoles(t, roles), declared
}

// The recommendation a daemon-agent render returns for a shipped playbook is
// <role>:<size>; agent.spawn takes it as is. Without a mapping it launches
// the bare size on the caller's harness; a group mapping moves only that
// group's playbooks and a role mapping only its own.
func TestShippedPlaybookRecommendationsSpawnWithAndWithoutAMapping(t *testing.T) {
	h, declared := shippedRoutingHarness(t)
	lead := h.leadCaller()
	for _, d := range declared {
		rec := d.Role + ":" + d.Size
		if got, bare := h.spawnBinding(lead, rec), h.spawnBinding(lead, d.Size); got != bare {
			t.Fatalf("%s = %+v, bare %s = %+v", rec, got, d.Size, bare)
		}
	}

	setTierRouting(t, h.config.Config, `{"nodes":{
		"review":{"*":"codex.*"},
		"explore":{"*":"claude.*"},
		"ticket-worker-elevated":{"*":"claude.small"}
	}}`)
	codex := func(size string) spawnBinding { return h.spawnBinding(lead, "codex."+size) }
	claude := func(size string) spawnBinding { return h.spawnBinding(lead, "claude."+size) }
	for _, d := range declared {
		got := h.spawnBinding(lead, d.Role+":"+d.Size)
		var want spawnBinding
		switch {
		case d.Role == "ticket-worker-elevated":
			want = spawnBinding{claude("small").Backend, claude("small").Model, claude("small").Effort, "small"}
		case strings.HasPrefix(d.Parent, "review:"):
			want = codex(d.Size)
		case strings.HasPrefix(d.Parent, "explore:"):
			want = claude(d.Size)
		default:
			want = h.spawnBinding(lead, d.Size)
		}
		if got != want {
			t.Fatalf("mapped %s:%s = %+v, want %+v", d.Role, d.Size, got, want)
		}
	}
	// The explore playbook is its own node under the explore group: the
	// group's mapping reaches it, and the group's own name is not a role.
	if got, want := h.spawnBinding(lead, "playbook-explore:small"), claude("small"); got != want {
		t.Fatalf("playbook-explore:small = %+v, want %+v", got, want)
	}
}
