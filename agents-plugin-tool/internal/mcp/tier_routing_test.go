package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	piInitNative    = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"ws-pi-bridge"}}}`
	codexInitNative = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"codex"}}}`
	codexInitDaemon = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"codex"},"capabilities":{"experimental":{"wsPiDaemonAgents":true}}}}`
)

// daemonRenderFixture lets a fixture Codex caller opt in to daemon
// delegation. Live Codex policy is unchanged.
func daemonRenderFixture(t *testing.T) *piRecordFixture {
	f := newPiRecordFixture(t)
	backends := wsagent.BuiltinBackendRegistrations()
	codex := backends[wsagent.BackendCodex]
	codex.Connection.ClientDaemonAgents = true
	backends[wsagent.BackendCodex] = codex
	f.s.agentBackendSource = wsagent.NewRunner(wsagent.Options{Backends: backends})
	return f
}

// roleRenderFixture is a fixture lead whose package holds one role playbook.
func roleRenderFixture(t *testing.T) *piRecordFixture {
	t.Helper()
	useResourceFixture(t, buildTestRsrcTree(t, map[string]string{
		"role-pb/role-pb.md": rolePlaybookContent,
	}))
	return daemonRenderFixture(t)
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

// A daemon-agent caller is recommended the binding the graph resolves, in
// the tier form a v0.51.0 spawn also accepts (the bare size on a fallthrough,
// <harness>.<size> for a mapped slot; the role form only for a bundle or an
// unresolvable graph); a native caller keeps the bare size and its own
// harness's binding, mapping or not.
func TestRenderRoleRecommendationDaemonVersusNative(t *testing.T) {
	f := roleRenderFixture(t)
	resp := callToolOnce(t, f.s, 9, "config.tune", map[string]any{"key": "agents.tier", "scope": "global", "harness": "codex",
		"value": map[string]any{"tier": "medium", "model": "codex-mid", "effort": "high"}})
	if toolIsError(t, resp) {
		t.Fatalf("set codex.medium: %s", resp)
	}
	nativeBaseline := metadata(strings.Split(withRecommendedRenderBinding("path", wsagent.BackendCodex, "medium", f.s.configOptions()), "\n"))
	if nativeBaseline != "recommended-tier: medium\nrecommended-model: codex-mid\nrecommended-reasoning-effort: high" {
		t.Fatalf("baseline = %q", nativeBaseline)
	}

	// Default chain: the daemon caller's binding is the bare size's.
	daemon := metadata(renderResponse(t, f.s, f.leadKey, "role-pb", codexInitDaemon, nil))
	if want := nativeBaseline; daemon != want {
		t.Fatalf("default-chain daemon metadata = %q, want %q", daemon, want)
	}
	if native := metadata(renderResponse(t, f.s, f.leadKey, "role-pb", codexInitNative, nil)); native != nativeBaseline {
		t.Fatalf("default-chain native metadata = %q, want %q", native, nativeBaseline)
	}

	// A cross-harness mapping moves only the daemon caller's binding.
	setRouting(t, f.s, `{"nodes":{"implement":{"*":"claude.*"}}}`)
	daemon = metadata(renderResponse(t, f.s, f.leadKey, "role-pb", codexInitDaemon, nil))
	if want := "recommended-tier: claude.medium\nrecommended-model: sonnet\nrecommended-reasoning-effort: medium"; daemon != want {
		t.Fatalf("mapped daemon metadata = %q, want %q", daemon, want)
	}
	if native := metadata(renderResponse(t, f.s, f.leadKey, "role-pb", codexInitNative, nil)); native != nativeBaseline {
		t.Fatalf("mapped native metadata = %q, want %q", native, nativeBaseline)
	}

	// tier_override keeps its bare validation and replaces the role's size.
	daemon = metadata(renderResponse(t, f.s, f.leadKey, "role-pb", codexInitDaemon, map[string]any{"tier_override": "opus"}))
	if want := "recommended-tier: claude.large\nrecommended-model: sonnet\nrecommended-reasoning-effort: high"; daemon != want {
		t.Fatalf("override daemon metadata = %q, want %q", daemon, want)
	}
	resp = callToolOnce(t, f.s, 3, "playbook.render", map[string]any{"name": "role-pb", "session_key": f.leadKey, "tier_override": "ticket-worker:large"})
	if !toolIsError(t, resp) || !strings.Contains(toolText(t, resp), "tier_override") {
		t.Fatalf("a role-shaped tier_override must stay rejected: %s", resp)
	}

	resp = callToolOnce(t, f.s, 9, "config.tune", map[string]any{"key": "agents.tier", "scope": "global", "harness": "werk",
		"value": map[string]any{"tier": "medium", "backend": "werk", "model": "provider/worker", "effort": "low"}})
	if toolIsError(t, resp) {
		t.Fatalf("set werk.medium: %s", resp)
	}
	setRouting(t, f.s, `{"nodes":{"implement":{"*":"pi.*"}}}`)
	if got := metadata(renderResponse(t, f.s, f.leadKey, "role-pb", codexInitDaemon, nil)); got != "recommended-tier: werk.medium\nrecommended-model: provider/worker\nrecommended-reasoning-effort: low" {
		t.Fatalf("legacy Pi pointer recommends %q", got)
	}

	// An invalid graph keeps the role recommendation without a binding.
	setRouting(t, f.s, `{"nodes":{"implement":{"*":"nosuch:*"}}}`)
	if daemon := metadata(renderResponse(t, f.s, f.leadKey, "role-pb", codexInitDaemon, nil)); daemon != "recommended-tier: ticket-worker:medium" {
		t.Fatalf("invalid-graph daemon metadata = %q", daemon)
	}
}

// With no user config an unmapped role tier, whose chain ends at the bare
// size on the caller's harness, resolves exactly as that bare size does on
// every surface that reports a binding: config.resolve_agent with an omitted harness on a session
// with no detected harness (the default bucket), Codex, and Claude, and the
// render binding for the same harnesses.
func TestUnmappedRoleTierResolvesAsTheBareSize(t *testing.T) {
	f := roleRenderFixture(t)
	resolve := func(tier, harness string) (resolveAgentTierResult, string) {
		t.Helper()
		args := map[string]any{"tier": tier, "format": "json"}
		if harness != "" {
			args["harness"] = harness
		}
		resp := callToolOnce(t, f.s, 4, "config.resolve_agent", args)
		if toolIsError(t, resp) {
			return resolveAgentTierResult{}, toolText(t, resp)
		}
		var got resolveAgentTierResult
		if err := json.Unmarshal([]byte(toolText(t, resp)), &got); err != nil {
			t.Fatalf("decode %s: %v", resp, err)
		}
		return got, ""
	}
	if h := f.s.currentHarness(); h != "" {
		t.Fatalf("fixture session harness = %q, want none detected", h)
	}
	opts := f.s.configOptions()
	for _, harness := range []string{"", wsagent.BackendCodex, wsagent.BackendClaude, wsagent.BackendAgy} {
		bare, bareErr := resolve("medium", harness)
		role, roleErr := resolve("ticket-worker:medium", harness)
		if bareErr != "" || roleErr != "" {
			t.Fatalf("harness %q: bare error %q, role error %q", harness, bareErr, roleErr)
		}
		caller := harness
		if caller == "" {
			caller = "<caller>"
		}
		if want := "ticket-worker:medium -> implement:medium -> medium -> " + caller + ".medium"; role.Trace != want {
			t.Fatalf("harness %q: role trace = %q, want %q", harness, role.Trace, want)
		}
		role.Trace = ""
		if role != bare {
			t.Fatalf("harness %q: role = %+v, bare = %+v", harness, role, bare)
		}
		switch harness {
		case "":
			if bare.ResolvedFrom != "default" {
				t.Fatalf("harness %q: bare resolved_from = %q, want the default bucket fallback", harness, bare.ResolvedFrom)
			}
		default:
			if bare.ResolvedFrom != harness {
				t.Fatalf("harness %q: bare resolved_from = %q", harness, bare.ResolvedFrom)
			}
		}

		// Render: the role recommendation carries the bare recommendation's
		// binding for the same harness, whichever harness the walk uses.
		f.s.rootMu.Lock()
		f.s.sessionHarness = harness
		f.s.rootMu.Unlock()
		wantRender := withRecommendedRenderBinding("path", harness, "medium", opts)
		got := f.s.withRecommendedRenderBindingFor("path", harness, "ticket-worker:medium", opts)
		f.s.rootMu.Lock()
		f.s.sessionHarness = ""
		f.s.rootMu.Unlock()
		if got != wantRender || !strings.Contains(got, "recommended-model: ") {
			t.Fatalf("harness %q: role render = %q, want %q", harness, got, wantRender)
		}
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
		resp := callToolOnce(t, f.s, 4, "config.resolve_agent", map[string]any{"tier": tier, "harness": "codex", "format": "json"})
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
		backend, model, effort, from, err := wsconfig.ResolveAgentTierForHarness(f.s.configOptions(), "medium", "codex")
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
	if got, errText := resolve("claude.large"); errText != "" || got.Model != "sonnet" || got.Trace != "claude.large" {
		t.Fatalf("explicit target = %+v %s", got, errText)
	}
	// review:xlarge lands on pi.xlarge, whose werk slot is unset: agent.spawn
	// refuses it, so config.resolve_agent does too rather than answering
	// with another harness's slot.
	if got, errText := resolve("review:xlarge"); !strings.Contains(errText, "werk.xlarge has no usable model") {
		t.Fatalf("slotless routed tier = %+v %q", got, errText)
	}
	// With a runner of its own, the server applies the spawn's harness
	// availability check: no harness is registered here.
	f.s.agents = wsagent.NewRunner(wsagent.Options{})
	if got, errText := resolve("ticket-worker:medium"); !strings.Contains(errText, `harness "claude" is not registered in this daemon`) {
		t.Fatalf("unavailable harness = %+v %q", got, errText)
	}
	if got, errText := resolve("discussion"); !strings.Contains(errText, `harness "agy" is not registered`) {
		t.Fatalf("unavailable bundle harness = %+v %q", got, errText)
	}
	f.s.agents = nil
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
			{Tier: "ticket-worker:medium", Trace: "ticket-worker:medium -> implement:medium -> medium -> claude.medium", Model: "sonnet", Effort: "medium"},
			{Tier: "x:small", Error: "boom"},
		},
		Collisions: []string{"alias a -> b shadows bundle a"},
	}
	text := formatAgentTiers(base)
	for _, want := range []string{
		"routing (agent.tier_routing;",
		"  discussion: not spawnable now: harness \"agy\" has no spawnable tier now\n",
		"  ticket-worker:medium -> implement:medium -> medium -> claude.medium: sonnet/medium\n",
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

// The shipped playbooks' own frontmatter drives the recommendation: a
// daemon-agent caller is walked through <role>:<size> (the explore playbook's
// role is playbook-explore) and told the v0.51.0-readable form of where it
// lands (the bare size on the default chain, <harness>.<size> once mapped),
// the binding follows the group mapping, and a native caller keeps the bare
// size and caller-harness binding with or without it.
func TestShippedPlaybookFrontmatterRecommendations(t *testing.T) {
	useResourceFixture(t, "../../../agents-plugin/rsrc")
	f := daemonRenderFixture(t)
	resp := callToolOnce(t, f.s, 9, "config.tune", map[string]any{"key": "agents.tier", "scope": "global", "harness": "codex",
		"value": map[string]any{"tier": "large", "model": "codex-large", "effort": "high"}})
	if toolIsError(t, resp) {
		t.Fatalf("set codex.large: %s", resp)
	}
	tier := func(initialize, name string) string {
		first, _, _ := strings.Cut(metadata(renderResponse(t, f.s, f.leadKey, name, initialize, nil)), "\n")
		return first
	}
	for name, bare := range map[string]string{
		"ticket-reviewer-design": "large",
		"ticket-worker-elevated": "large",
		"explore":                "small",
	} {
		if got := tier(codexInitDaemon, name); got != "recommended-tier: "+bare {
			t.Fatalf("daemon %s = %q, want %q", name, got, bare)
		}
		if got := tier(codexInitNative, name); got != "recommended-tier: "+bare {
			t.Fatalf("native %s = %q, want bare %q", name, got, bare)
		}
	}

	nativeBefore := metadata(renderResponse(t, f.s, f.leadKey, "ticket-reviewer-design", codexInitNative, nil))
	daemon := metadata(renderResponse(t, f.s, f.leadKey, "ticket-reviewer-design", codexInitDaemon, nil))
	if want := "recommended-tier: large\nrecommended-model: codex-large\nrecommended-reasoning-effort: high"; daemon != want {
		t.Fatalf("default-chain daemon metadata = %q, want %q", daemon, want)
	}
	setRouting(t, f.s, `{"nodes":{"review":{"*":"claude.*"}}}`)
	if daemon = metadata(renderResponse(t, f.s, f.leadKey, "ticket-reviewer-design", codexInitDaemon, nil)); !strings.HasPrefix(daemon, "recommended-tier: claude.large\nrecommended-model: sonnet") {
		t.Fatalf("mapped daemon metadata = %q", daemon)
	}
	if native := metadata(renderResponse(t, f.s, f.leadKey, "ticket-reviewer-design", codexInitNative, nil)); native != nativeBefore {
		t.Fatalf("mapped native metadata = %q, want %q", native, nativeBefore)
	}
}

// legacyV0510ResolveTier is v0.51.0's agent.spawn tier resolution for a
// caller on callerHarness (f800ba30d:internal/wsagent/agent_tools.go
// resolveTier + harnessSlot path, agent_spawn.go:45-50): a bare size on the
// caller's harness, <harness>.<size>, or an exact agent.tier_aliases entry,
// then that harness's own agents.tier slot. Anything else, a role tier
// included, is refused before a child exists.
func legacyV0510ResolveTier(cfg wsconfig.Options, callerHarness, tier string) (harness string, mapping wsconfig.AgentTier, err error) {
	raw := strings.ToLower(strings.TrimSpace(tier))
	var bare string
	switch h, t, dotted := strings.Cut(raw, "."); {
	case dotted:
		harness, bare = h, wsconfig.NormalizedTier(t)
	case wsconfig.NormalizedTier(raw) != "":
		harness, bare = callerHarness, wsconfig.NormalizedTier(raw)
	default:
		resolver := wsconfig.NewResolver(cfg, map[string]string{wsconfig.ItemAgentTierAliases: ""}, nil, nil)
		rv, gerr := resolver.Get("", wsconfig.ItemAgentTierAliases)
		if gerr != nil {
			return "", mapping, gerr
		}
		aliases, perr := wsconfig.ParseTierAliases(rv.Value)
		if perr != nil {
			return "", mapping, perr
		}
		target, ok := aliases[raw]
		if !ok {
			return "", mapping, fmt.Errorf("tier %q is neither a tier, <harness>.<tier>, nor an alias in %s", tier, wsconfig.ItemAgentTierAliases)
		}
		h, t, _ := strings.Cut(target, ".")
		harness, bare = h, t
	}
	if bare == "" {
		return "", mapping, fmt.Errorf("tier %q names no tier", tier)
	}
	tiers, err := wsconfig.HarnessTiers(cfg, harness)
	if err != nil {
		return "", mapping, err
	}
	mapping, ok := tiers[bare]
	if !ok {
		return "", mapping, fmt.Errorf("tier %q: %s.%s has no model in agents.tier", tier, harness, bare)
	}
	return harness, mapping, nil
}

// A rendered tier is passed to agent.spawn as returned. If the daemon is
// replaced by v0.51.0 between the render and the spawn, a slot-terminal role
// walk was rendered in a form v0.51.0 resolves to the same harness, model,
// and effort the candidate's role walk does; the one residual is a bundle
// terminal, which v0.51.0 cannot express, so its role form stays (and
// v0.51.0 refuses it before creating a child, per the named-tiers decision).
func TestRenderedTierSurvivesDaemonRollbackBeforeSpawn(t *testing.T) {
	f := roleRenderFixture(t)
	resp := callToolOnce(t, f.s, 9, "config.tune", map[string]any{"key": "agents.tier", "scope": "global", "harness": "codex",
		"value": map[string]any{"tier": "medium", "model": "codex-mid", "effort": "high"}})
	if toolIsError(t, resp) {
		t.Fatalf("set codex.medium: %s", resp)
	}
	const callerHarness = wsagent.BackendCodex
	cases := []struct {
		name, routing string
		wantTier      string // recommended-tier the render returns
		bundle        bool   // the residual: v0.51.0 cannot express the target
	}{
		{name: "default chain falls through to the caller's slot", routing: `{}`, wantTier: "medium"},
		{name: "cross-harness slot", routing: `{"nodes":{"implement":{"*":"claude.*"}}}`, wantTier: "claude.medium"},
		{name: "caller-harness slot reached through a pointer", routing: `{"nodes":{"implement":{"*":"codex.*"}}}`, wantTier: "codex.medium"},
		{name: "bundle terminal", routing: `{"nodes":{"implement":{"*":"deep-impl"}},"bundles":{"deep-impl":{"harness":"claude","model":"custom-model","effort":"high"}}}`, wantTier: "ticket-worker:medium", bundle: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRouting(t, f.s, tc.routing)
			// 1. Render on the candidate daemon.
			lines := renderResponse(t, f.s, f.leadKey, "role-pb", codexInitDaemon, nil)
			var tier, model, effort string
			for _, line := range lines[1:] {
				if v, ok := strings.CutPrefix(line, "recommended-tier: "); ok {
					tier = v
				}
				if v, ok := strings.CutPrefix(line, "recommended-model: "); ok {
					model = v
				}
				if v, ok := strings.CutPrefix(line, "recommended-reasoning-effort: "); ok {
					effort = v
				}
			}
			if tier != tc.wantTier {
				t.Fatalf("recommended-tier = %q, want %q (%v)", tier, tc.wantTier, lines)
			}
			// What the candidate's own role walk launches.
			opts := f.s.configOptions()
			resolver := wsagent.NewTierBindingResolver(opts, f.s.agentBackends()...)
			res, want, err := f.s.routedBinding("ticket-worker:medium", callerHarness, opts, resolver)
			if err != nil {
				t.Fatalf("candidate walk: %v", err)
			}
			if model != want.Model || formatEffortForText(want.Effort) != effort {
				t.Fatalf("render binding %q/%q, candidate walk %+v", model, effort, want)
			}
			// 2. Downgrade, 3. spawn on v0.51.0's resolution.
			harness, mapping, err := legacyV0510ResolveTier(f.s.configOptions(), callerHarness, tier)
			if tc.bundle {
				if err == nil {
					t.Fatalf("v0.51.0 resolved the bundle-terminal role tier %q to %s %+v; the residual boundary is gone", tier, harness, mapping)
				}
				return
			}
			if err != nil {
				t.Fatalf("v0.51.0 refuses the rendered tier %q: %v", tier, err)
			}
			if harness != res.Harness || mapping.Model != want.Model || mapping.Effort != want.Effort {
				t.Fatalf("v0.51.0 spawn of %q = %s %+v, candidate walk = %s %+v", tier, harness, mapping, res.Harness, want)
			}
			// Back on the candidate, the same returned tier resolves alike.
			if h, m, err := legacyV0510ResolveTier(f.s.configOptions(), callerHarness, tier); err != nil || h != harness || m != mapping {
				t.Fatalf("repeat resolution drifted: %s %+v, %v", h, m, err)
			}
		})
	}
	// The pre-fix behavior this replaces: v0.51.0 refuses the raw role tier.
	if _, _, err := legacyV0510ResolveTier(f.s.configOptions(), callerHarness, "ticket-worker:medium"); err == nil {
		t.Fatal("v0.51.0 fixture accepted a role tier; it no longer models the old refusal")
	}
}

func TestLegacyWerkTierResolutionRequiresItsProvider(t *testing.T) {
	f := daemonRenderFixture(t)
	backends := wsagent.WithProviderCredentials(wsagent.BuiltinBackendRegistrations(), nil, func(provider string) (bool, error) {
		return provider == "openai", nil
	})
	runner := wsagent.NewRunner(wsagent.Options{Backends: backends})
	t.Cleanup(runner.Close)
	f.s.agentBackendSource = runner
	opts := f.s.configOptions()
	if _, err := wsconfig.SetGlobalAgentsTierForHarness(opts, "small", "", "vendor/model", wsagent.BackendWerk, "low"); err != nil {
		t.Fatal(err)
	}
	setRouting(t, f.s, `{"nodes":{"legacy-role":{"*":"pi.*"}}}`)
	if res, err := f.s.resolveAgentTier("pi.small", "claude"); err == nil || !strings.Contains(err.Error(), `harness "pi" is not registered in this daemon`) || res.Model != "" {
		t.Errorf("direct pi.small should be refused: %+v, %v", res, err)
	}
	if model, _, ok := f.s.renderTierBinding("claude", "claude", "pi.small", opts); ok || model != "" {
		t.Errorf("render direct pi.small supplied %q", model)
	}
	const reason = "no OpenRouter API key"
	for _, tc := range []struct{ tier, harness string }{
		{"werk.small", "claude"},
		{"legacy-role:small", "claude"},
		{"small", "pi"},
		{"small", "werk"},
	} {
		if res, err := f.s.resolveAgentTier(tc.tier, tc.harness); err == nil || !strings.Contains(err.Error(), reason) || res.Model != "" {
			t.Errorf("resolve %s from %s = %+v, %v", tc.tier, tc.harness, res, err)
		}
		if model, _, ok := f.s.renderTierBinding(tc.harness, tc.harness, tc.tier, opts); ok || model != "" {
			t.Errorf("render %s from %s supplied %q", tc.tier, tc.harness, model)
		}
	}
	rsrc, _ := dashboardTierFixture(t)
	view, err := dashboardTierRoutingView(rsrc, opts, nil, "claude", runner.HarnessAvailability, backends)
	if err != nil {
		t.Fatal(err)
	}
	route := tierViewRoute(t, tierViewNode(t, view, "legacy-role"), "small")
	if !strings.Contains(route.BindingError, reason) || route.Model != "" {
		t.Fatalf("config preview = %+v", route)
	}
}
