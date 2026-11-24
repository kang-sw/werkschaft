package wsconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var testTierRoles = []TierRole{
	{Name: "playbook-explore", Parent: "explore:*", Size: "small"},
	{Name: "ticket-worker", Parent: "implement:*", Size: "medium"},
	{Name: "ticket-reviewer-design", Parent: "review:*", Size: "large"},
}

var testHarnesses = []string{"claude", "codex", "pi", "agy"}

func mustRouting(t *testing.T, raw string) TierRouting {
	t.Helper()
	routing, err := ParseTierRouting(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("ParseTierRouting(%s): %v", raw, err)
	}
	return routing
}

func mustGraph(t *testing.T, raw string, aliases map[string]string) *TierGraph {
	t.Helper()
	g := NewTierGraph(testTierRoles, mustRouting(t, raw), aliases, testHarnesses)
	if err := g.Err(); err != nil {
		t.Fatalf("graph %s invalid: %v", raw, err)
	}
	return g
}

func mustResolve(t *testing.T, g *TierGraph, token, caller string) TierResolution {
	t.Helper()
	res, err := g.Resolve(token, caller)
	if err != nil {
		t.Fatalf("Resolve(%q, %q): %v", token, caller, err)
	}
	return res
}

func TestTierRoutingDefaultChainIsTheBareSizeOnTheCallerHarness(t *testing.T) {
	g := mustGraph(t, `{}`, nil)
	res := mustResolve(t, g, "ticket-worker:large", "codex")
	if got, want := res.Trace(), "ticket-worker:large -> implement:large -> large -> codex.large"; got != want {
		t.Fatalf("trace = %q, want %q", got, want)
	}
	if res.Harness != "codex" || res.Size != "large" || res.Bundle != "" || !res.CallerFallthrough || res.Rank() != "large" {
		t.Fatalf("resolution = %+v", res)
	}
	// Every role at every size resolves exactly as the bare size would.
	for _, role := range testTierRoles {
		for _, size := range TierSizes {
			for _, caller := range []string{"claude", "codex", "pi"} {
				routed := mustResolve(t, g, role.Name+":"+size, caller)
				bare := mustResolve(t, g, size, caller)
				if routed.Harness != bare.Harness || routed.Size != bare.Size || routed.Model != "" || routed.Effort != "" {
					t.Errorf("%s:%s on %s = %+v, bare = %+v", role.Name, size, caller, routed, bare)
				}
			}
		}
	}
}

func TestTierRoutingOverrideLevels(t *testing.T) {
	// A middle-role override moves the group.
	g := mustGraph(t, `{"nodes":{"review":{"*":"codex.*"}}}`, nil)
	res := mustResolve(t, g, "ticket-reviewer-design:large", "claude")
	if got, want := res.Trace(), "ticket-reviewer-design:large -> review:large -> codex.large"; got != want {
		t.Fatalf("group trace = %q, want %q", got, want)
	}
	if res.CallerFallthrough {
		t.Fatalf("a mapped group is not the caller fallthrough: %+v", res)
	}
	// Other groups keep the caller fallthrough.
	if res := mustResolve(t, g, "ticket-worker:medium", "claude"); res.Harness != "claude" || res.Size != "medium" {
		t.Fatalf("implement moved with review: %+v", res)
	}

	// A fine-role override beats the group and pins its size.
	g = mustGraph(t, `{"nodes":{"review":{"*":"codex.*"},"ticket-reviewer-design":{"*":"claude.xlarge"}}}`, nil)
	res = mustResolve(t, g, "ticket-reviewer-design:small", "codex")
	if got, want := res.Trace(), "ticket-reviewer-design:small -> claude.xlarge"; got != want {
		t.Fatalf("fine trace = %q, want %q", got, want)
	}
	if res.Rank() != "xlarge" {
		t.Fatalf("pinned rank = %q", res.Rank())
	}

	// A fine role can be re-pointed under another group.
	g = mustGraph(t, `{"nodes":{"ticket-worker":{"*":"review:*"},"review":{"*":"codex.*"}}}`, nil)
	if res := mustResolve(t, g, "ticket-worker:large", "claude"); res.Trace() != "ticket-worker:large -> review:large -> codex.large" {
		t.Fatalf("reparented trace = %q", res.Trace())
	}
}

func TestTierRoutingWildcardPropagationPinningAndExceptions(t *testing.T) {
	g := mustGraph(t, `{"nodes":{
		"explore":{"*":"lead-explore:*","small":"claude.small"},
		"lead-explore":{"*":"codex.*","xlarge":"heavy:large"},
		"heavy":{}
	}}`, nil)
	cases := []struct {
		token, trace string
		fall         bool
	}{
		// The exact-size exception beats the wildcard.
		{"playbook-explore:small", "playbook-explore:small -> explore:small -> claude.small", false},
		// The wildcard carries the requested size through two nodes.
		{"playbook-explore:medium", "playbook-explore:medium -> explore:medium -> lead-explore:medium -> codex.medium", false},
		// A pinned target changes the size; an empty user node falls through.
		{"playbook-explore:xlarge", "playbook-explore:xlarge -> explore:xlarge -> lead-explore:xlarge -> heavy:large -> large -> pi.large", true},
	}
	for _, tc := range cases {
		res := mustResolve(t, g, tc.token, "pi")
		if res.Trace() != tc.trace || res.CallerFallthrough != tc.fall {
			t.Errorf("%s: trace %q fall %v, want %q fall %v", tc.token, res.Trace(), res.CallerFallthrough, tc.trace, tc.fall)
		}
	}
	if res := mustResolve(t, g, "playbook-explore:xlarge", "pi"); res.Rank() != "large" {
		t.Fatalf("pinned rank = %q, want large", res.Rank())
	}
}

func TestTierRoutingBundles(t *testing.T) {
	g := mustGraph(t, `{
		"nodes":{"review":{"*":"discussion","small":"codex.small"}},
		"bundles":{"discussion":{"harness":"agy","model":"claude-opus-4-6-thinking","effort":"high"}}
	}`, nil)
	routed := mustResolve(t, g, "ticket-reviewer-design:medium", "codex")
	if routed.Bundle != "discussion" || routed.Harness != "agy" || routed.Model != "claude-opus-4-6-thinking" || routed.Effort != "high" || routed.Size != "medium" || routed.Rank() != "medium" {
		t.Fatalf("routed bundle = %+v", routed)
	}
	if routed.Trace() != "ticket-reviewer-design:medium -> review:medium -> discussion" {
		t.Fatalf("routed trace = %q", routed.Trace())
	}
	direct := mustResolve(t, g, "discussion", "codex")
	if direct.Bundle != "discussion" || direct.Size != "" || direct.Rank() != "xlarge" {
		t.Fatalf("direct bundle = %+v (rank %s)", direct, direct.Rank())
	}
}

func TestTierRoutingRejectsCyclesAcrossSizesEvenWhenAnAliasMasksAnEdge(t *testing.T) {
	// a:small -> b:large never loops at run time (b has no large pointer),
	// but the node edges a -> b -> a form a cycle.
	raw := `{"nodes":{"a":{"small":"b:large"},"b":{"medium":"a:small"}}}`
	for _, aliases := range []map[string]string{nil, {"b:medium": "codex.medium"}} {
		g := NewTierGraph(testTierRoles, mustRouting(t, raw), aliases, testHarnesses)
		err := g.Err()
		if err == nil || !strings.Contains(err.Error(), "pointer cycle a -> b -> a") {
			t.Errorf("aliases %v: Err = %v, want the a -> b -> a cycle", aliases, err)
		}
		if _, err := g.Resolve("a:small", "codex"); err == nil {
			t.Errorf("aliases %v: an invalid graph resolved a role tier", aliases)
		}
		// Legacy tokens keep resolving on an invalid graph.
		if _, err := g.Resolve("claude.large", "codex"); err != nil {
			t.Errorf("aliases %v: explicit target on invalid graph: %v", aliases, err)
		}
		if aliases != nil {
			if res, err := g.Resolve("b:medium", "codex"); err != nil || res.Harness != "codex" || res.Size != "medium" {
				t.Errorf("an exact alias must still resolve on an invalid graph: %+v, %v", res, err)
			}
		}
	}
	// A self-pointer at any size is a cycle too.
	g := NewTierGraph(testTierRoles, mustRouting(t, `{"nodes":{"explore":{"large":"explore:small"}}}`), nil, testHarnesses)
	if err := g.Err(); err == nil || !strings.Contains(err.Error(), "explore -> explore") {
		t.Fatalf("self pointer Err = %v", err)
	}
}

// chainRouting is n1 -> n2 -> ... -> nk, with nk empty (falls through).
func chainRouting(k int) string {
	var parts []string
	for i := 1; i < k; i++ {
		parts = append(parts, fmt.Sprintf(`"n%d":{"*":"n%d:*"}`, i, i+1))
	}
	parts = append(parts, fmt.Sprintf(`"n%d":{}`, k))
	return `{"nodes":{` + strings.Join(parts, ",") + `}}`
}

func TestTierRoutingHopLimit(t *testing.T) {
	// k nodes cost k-1 pointer hops plus fallthrough and caller: k+1 hops.
	g := mustGraph(t, chainRouting(7), nil)
	res := mustResolve(t, g, "n1:large", "codex")
	if len(res.Hops) != TierRoutingMaxHops {
		t.Fatalf("hops = %d (%s), want %d", len(res.Hops), res.Trace(), TierRoutingMaxHops)
	}
	g = NewTierGraph(testTierRoles, mustRouting(t, chainRouting(8)), nil, testHarnesses)
	err := g.Err()
	if err == nil || !strings.Contains(err.Error(), "more than 8 hops") || !strings.Contains(err.Error(), "n1:small") {
		t.Fatalf("nine-hop Err = %v", err)
	}
	if _, err := g.Resolve("n1:large", "codex"); err == nil || !strings.Contains(err.Error(), "more than 8 hops") {
		t.Fatalf("nine-hop Resolve = %v", err)
	}
	// An exact alias that would shorten the path does not rescue it: the
	// graph must stay valid after any alias is reset. Only n1:large is
	// overlong here (every pointer is a large exception), so an
	// alias-following validator would accept the graph.
	var parts []string
	for i := 1; i < 8; i++ {
		parts = append(parts, fmt.Sprintf(`"n%d":{"large":"n%d:large"}`, i, i+1))
	}
	largeOnly := `{"nodes":{` + strings.Join(parts, ",") + `,"n8":{}}}`
	for _, aliases := range []map[string]string{nil, {"n2:large": "codex.large"}} {
		g = NewTierGraph(testTierRoles, mustRouting(t, largeOnly), aliases, testHarnesses)
		if err := g.Err(); err == nil || !strings.Contains(err.Error(), "more than 8 hops") || !strings.Contains(err.Error(), "n1:large") {
			t.Fatalf("aliases %v: large-only nine-hop Err = %v", aliases, err)
		}
	}
	if res := mustResolve(t, mustGraph(t, largeOnly[:strings.Index(largeOnly, `"n7"`)]+`"n7":{}}}`, nil), "n1:large", "codex"); len(res.Hops) != TierRoutingMaxHops {
		t.Fatalf("the large-only chain one node shorter = %d hops (%s)", len(res.Hops), res.Trace())
	}
}

func TestTierRoutingLegacyAliasPrecedence(t *testing.T) {
	aliases := map[string]string{
		"explore:small":       "claude.small",
		"ticket-worker:large": "pi.large",
		"discussion":          "codex.medium",
	}
	g := mustGraph(t, `{
		"nodes":{"explore":{"*":"codex.*"}},
		"bundles":{"discussion":{"harness":"agy","model":"m"}}
	}`, aliases)
	// A whole-token alias wins over the role tier and over a bundle.
	if res := mustResolve(t, g, "ticket-worker:large", "codex"); res.Harness != "pi" || res.Size != "large" || res.Trace() != "ticket-worker:large -> pi.large [alias ticket-worker:large]" {
		t.Fatalf("whole-token alias = %+v %q", res, res.Trace())
	}
	if res := mustResolve(t, g, "discussion", "codex"); res.Bundle != "" || res.Harness != "codex" {
		t.Fatalf("alias over bundle = %+v", res)
	}
	// An alias named <node>:<size> wins at that node during a walk.
	res := mustResolve(t, g, "playbook-explore:small", "codex")
	if res.Trace() != "playbook-explore:small -> explore:small -> claude.small [alias explore:small]" {
		t.Fatalf("walk alias trace = %q", res.Trace())
	}
	if res := mustResolve(t, g, "playbook-explore:large", "claude"); res.Harness != "codex" {
		t.Fatalf("other sizes keep the pointer: %+v", res)
	}
	collisions := strings.Join(g.Collisions(), "\n")
	for _, want := range []string{
		"alias discussion -> codex.medium shadows bundle discussion",
		"alias explore:small -> claude.small takes precedence over node explore's pointers at size small",
		"alias ticket-worker:large -> pi.large takes precedence over node ticket-worker's pointers at size large",
	} {
		if !strings.Contains(collisions, want) {
			t.Errorf("collisions missing %q:\n%s", want, collisions)
		}
	}
}

func TestTierRoutingExplicitAndBareTokens(t *testing.T) {
	g := mustGraph(t, `{"nodes":{"review":{"*":"codex.*"}}}`, map[string]string{"quick": "claude.small"})
	if res := mustResolve(t, g, "Claude.Opus", "codex"); res.Harness != "claude" || res.Size != "large" || len(res.Hops) != 0 {
		t.Fatalf("explicit = %+v", res)
	}
	if res := mustResolve(t, g, "deep", "pi"); res.Harness != "pi" || res.Size != "large" || !res.CallerFallthrough {
		t.Fatalf("bare = %+v", res)
	}
	if res := mustResolve(t, g, "quick", "pi"); res.Harness != "claude" || res.Size != "small" {
		t.Fatalf("alias = %+v", res)
	}
	if res := mustResolve(t, g, "ticket-worker:opus", "pi"); res.Size != "large" {
		t.Fatalf("a role tier's size accepts tier spellings: %+v", res)
	}
	for token, want := range map[string]string{
		"":                  "tier is required",
		"claude.huge":       "names no tier",
		"nosuch:large":      "is not a tier node",
		"ticket-worker:big": "is not a size",
		"nosuch":            "is neither a tier",
	} {
		if _, err := g.Resolve(token, "codex"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Resolve(%q) = %v, want %q", token, err, want)
		}
	}
	if !IsRoutedTierToken("ticket-worker:large") || !IsRoutedTierToken("discussion") || IsRoutedTierToken("large") || IsRoutedTierToken("claude.large") {
		t.Fatal("IsRoutedTierToken misclassifies")
	}
}

func TestTierRoutingValidation(t *testing.T) {
	parseErrors := map[string]string{
		`{"nodes":{"Bad":{}}}`:                                               "lowercase slug",
		`{"nodes":{"a":{"huge":"codex.*"}}}`:                                 "selector",
		`{"nodes":{"a":{"opus":"codex.*"}}}`:                                 "selector",
		`{"nodes":{"a":{"*":"codex.huge"}}}`:                                 "<harness>.<size|*>",
		`{"nodes":{"a":{"*":"review:opus"}}}`:                                "<node>:<size|*>",
		`{"nodes":{"a":{"*":"Not A Target"}}}`:                               "bundle name",
		`{"bundles":{"b":{"model":"m"}}}`:                                    "needs a harness",
		`{"bundles":{"b":{"harness":"agy"}}}`:                                "needs a model",
		`{"nodes":{"x":{}},"bundles":{"x":{"harness":"agy","model":"m"}}}`:   "both a node and a bundle",
		`{"nodes":{},"extra":1}`:                                             "unknown field",
		`{"nodes":{"a":null}}`:                                               "must be an object of pointers",
		`{"bundles":{"b":{"harness":"claude","model":"two words"}}}`:         "must match",
		`{"bundles":{"b":{"harness":"claude","model":"m","effort":"HIGH"}}}`: "lowercase word",
		`[]`: "cannot unmarshal",
	}
	for raw, want := range parseErrors {
		if _, err := ParseTierRouting(json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseTierRouting(%s) = %v, want %q", raw, err, want)
		}
	}
	graphErrors := map[string]string{
		`{"nodes":{"opus":{}}}`:                                       `node "opus" is a tier spelling`,
		`{"bundles":{"review":{"harness":"agy","model":"m"}}}`:        `bundle "review" is a built-in node`,
		`{"bundles":{"ticket-worker":{"harness":"agy","model":"m"}}}`: `bundle "ticket-worker" is a built-in node`,
		`{"bundles":{"b":{"harness":"nosuch","model":"m"}}}`:          `unknown harness "nosuch"`,
		`{"nodes":{"review":{"*":"nosuch:*"}}}`:                       `unknown node "nosuch"`,
		`{"nodes":{"review":{"*":"nosuch"}}}`:                         `unknown bundle "nosuch"`,
		`{"nodes":{"review":{"*":"nosuch.*"}}}`:                       `unknown harness "nosuch"`,
	}
	for raw, want := range graphErrors {
		g := NewTierGraph(testTierRoles, mustRouting(t, raw), nil, testHarnesses)
		if err := g.Err(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("graph %s Err = %v, want %q", raw, err, want)
		}
	}
	// Without a known-harness list, any harness name stays valid.
	if err := NewTierGraph(testTierRoles, mustRouting(t, `{"nodes":{"review":{"*":"future.*"}}}`), nil, nil).Err(); err != nil {
		t.Fatalf("unknown harness with no list: %v", err)
	}
}

func TestWerkTiersResolveDirectlyButCannotBeSavedAsGraphTargets(t *testing.T) {
	for _, raw := range []string{
		`{"nodes":{"review":{"*":"werk.*"}}}`,
		`{"bundles":{"b":{"harness":"werk","model":"m"}}}`,
	} {
		graph := NewTierGraph(testTierRoles, mustRouting(t, raw), nil, append(slices.Clone(testHarnesses), "werk"))
		if err := graph.Err(); err == nil || !strings.Contains(err.Error(), "direct tiers only") {
			t.Fatalf("graph %s: %v", raw, err)
		}
		if got, err := graph.Resolve("werk.small", "claude"); err != nil || got.Harness != "werk" || got.Size != "small" {
			t.Fatalf("direct tier = %+v, %v", got, err)
		}
	}
}

func TestTierRoutingNodesListing(t *testing.T) {
	g := mustGraph(t, `{"nodes":{"review":{"small":"codex.small"},"mine":{}}}`, nil)
	nodes := g.Nodes()
	var names []string
	for _, n := range nodes {
		names = append(names, string(n.Kind)+":"+n.Name)
	}
	want := "group:explore group:implement group:review role:playbook-explore role:ticket-reviewer-design role:ticket-worker user:mine"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("nodes = %s, want %s", got, want)
	}
	for _, n := range nodes {
		if n.Name == "review" && n.Pointers["small"] != "codex.small" {
			t.Fatalf("review pointers = %v", n.Pointers)
		}
		if n.Name == "ticket-reviewer-design" && (n.Size != "large" || n.Pointers["*"] != "review:*") {
			t.Fatalf("role = %+v", n)
		}
	}
}

// v0508Config is the v0.50.8 wsconfig.Config shape and writer
// (v0.50.8:agents-plugin-tool/internal/wsconfig/config.go:34-53,750-767):
// it decodes overrides and structured_overrides and marshals both back.
type v0508Config struct {
	SchemaVersion       int                        `json:"schema_version"`
	Agents              AgentsConfig               `json:"agents"`
	Overrides           map[string]string          `json:"overrides,omitempty"`
	StructuredOverrides map[string]json.RawMessage `json:"structured_overrides,omitempty"`
}

func TestTierRoutingSurvivesAnOlderWriterAndReopens(t *testing.T) {
	home := t.TempDir()
	opts := Options{ConfigHome: home, CacheHome: t.TempDir()}
	graph := `{"nodes":{"review":{"*":"codex.*"}},"bundles":{"discussion":{"harness":"agy","model":"claude-opus-4-6-thinking","effort":"high"}}}`
	r := NewResolver(opts, nil, nil, nil)
	if err := r.SetStructured(ItemAgentTierRouting, json.RawMessage(graph), ScopeGlobal); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config.json")

	// The older daemon writes an unrelated key through its own writer.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var old v0508Config
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	if old.Overrides == nil {
		old.Overrides = map[string]string{}
	}
	old.Overrides[ItemWorkflowLang] = "Korean"
	rewritten, err := json.MarshalIndent(old, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rewritten, 0o644); err != nil {
		t.Fatal(err)
	}

	// Reopening with the new reader recovers the graph and its bindings.
	g, err := LoadTierGraph(opts, testTierRoles, testHarnesses)
	if err != nil || g.Err() != nil {
		t.Fatalf("LoadTierGraph = %v, %v", err, g.Err())
	}
	if res := mustResolve(t, g, "ticket-reviewer-design:large", "claude"); res.Harness != "codex" || res.Size != "large" {
		t.Fatalf("recovered review mapping = %+v", res)
	}
	if res := mustResolve(t, g, "discussion", "claude"); res.Model != "claude-opus-4-6-thinking" {
		t.Fatalf("recovered bundle = %+v", res)
	}
	if v, ok := GlobalOverride(opts, ItemWorkflowLang); !ok || v != "Korean" {
		t.Fatalf("older write lost: %q %v", v, ok)
	}
}

func TestLoadTierGraphReadsGlobalOnlyAndKeepsLegacyTokensOnAParseError(t *testing.T) {
	home, cache := t.TempDir(), t.TempDir()
	opts := Options{ConfigHome: home, CacheHome: cache}
	// A project-file value is never read: the key is global-only.
	project, err := Path(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project, []byte(`{"schema_version":1,"structured_overrides":{"agent.tier_routing":{"nodes":{"review":{"*":"codex.*"}}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := LoadTierGraph(opts, testTierRoles, testHarnesses)
	if err != nil {
		t.Fatal(err)
	}
	if res := mustResolve(t, g, "ticket-reviewer-design:large", "claude"); res.Harness != "claude" {
		t.Fatalf("project value applied: %+v", res)
	}

	// A malformed global value refuses role tiers and keeps legacy ones.
	global := filepath.Join(home, "config.json")
	if err := os.WriteFile(global, []byte(`{"schema_version":1,"overrides":{"agent.tier_aliases":"quick=claude.small"},"structured_overrides":{"agent.tier_routing":{"nodes":{"review":{"*":"codex.huge"}}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err = LoadTierGraph(opts, testTierRoles, testHarnesses)
	if err != nil {
		t.Fatal(err)
	}
	if g.Err() == nil {
		t.Fatal("a malformed value parsed")
	}
	if _, err := g.Resolve("ticket-worker:large", "codex"); err == nil || !strings.Contains(err.Error(), "codex.huge") {
		t.Fatalf("role on malformed graph = %v", err)
	}
	if res := mustResolve(t, g, "quick", "codex"); res.Harness != "claude" {
		t.Fatalf("alias on malformed graph = %+v", res)
	}
}

func TestClaudeDefaultsRetainRoutingAndTierOverrides(t *testing.T) {
	opts := Options{ConfigHome: t.TempDir(), CacheHome: t.TempDir()}
	r := NewResolver(opts, nil, nil, nil)
	if err := r.SetStructured(ItemAgentTierRouting, json.RawMessage(`{"nodes":{"implement":{"*":"claude.xlarge"}},"bundles":{"custom":{"harness":"claude","model":"owner-model","effort":"low"}}}`), ScopeGlobal); err != nil {
		t.Fatal(err)
	}
	g, err := LoadTierGraph(opts, testTierRoles, testHarnesses)
	if err != nil {
		t.Fatal(err)
	}
	if res := mustResolve(t, g, "custom", "claude"); res.Model != "owner-model" || res.Effort != "low" {
		t.Fatalf("bundle override = %+v", res)
	}
	res := mustResolve(t, g, "ticket-worker:medium", "claude")
	if res.Harness != "claude" || res.Size != "xlarge" {
		t.Fatalf("routing override = %+v", res)
	}
	for _, project := range []bool{false, true} {
		model := "global-model"
		if project {
			model = "project-model"
		}
		if project {
			_, err = SetAgentsTierForHarness(opts, res.Size, "claude", model, "claude", "low")
		} else {
			_, err = SetGlobalAgentsTierForHarness(opts, res.Size, "claude", model, "claude", "low")
		}
		if err != nil {
			t.Fatal(err)
		}
		backend, got, effort, _, err := ResolveAgentTierForHarness(opts, res.Size, res.Harness)
		if err != nil || backend != "claude" || got != model || effort != "low" {
			t.Fatalf("project=%v: %s/%s/%s, %v", project, backend, got, effort, err)
		}
	}
}
