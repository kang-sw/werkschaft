package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// companionTierRuntime is a companion runtime whose runner registers each
// named harness with a test backend, reading config from throwaway homes in
// which every harness but werk (left with no slot, as built in) and custom
// "zz-" ones sets its small, medium and large slots to "<harness>-<size>"
// with effort "<size>-effort".
func companionTierRuntime(t *testing.T, harnesses ...string) (*companionRuntime, wsconfig.Options) {
	t.Helper()
	cfg := wsconfig.Options{CacheHome: filepath.Join(t.TempDir(), "cache"), ConfigHome: filepath.Join(t.TempDir(), "config")}
	backend := &companionTestBackend{}
	regs := map[string]wsagent.BackendRegistration{}
	for _, name := range harnesses {
		profile := wsagent.LeadProfileFor(name)
		if profile == "" {
			profile = "lead-" + name
		}
		regs[name] = wsagent.BackendRegistration{Backend: backend, LeadProfile: profile, WorkerCapable: true, WorkerProfile: wsagent.AgentWorkerProfile(name)}
		for _, size := range []string{"small", "medium", "large"} {
			if name == "werk" || strings.HasPrefix(name, "zz-") {
				break
			}
			if _, err := wsconfig.SetGlobalAgentsTierForHarness(cfg, size, "", name+"-"+size, name, size+"-effort"); err != nil {
				t.Fatal(err)
			}
		}
	}
	runner := wsagent.NewRunner(wsagent.Options{Dir: t.TempDir(), Backends: regs})
	rt := newCompanionRuntime(t.TempDir(), t.TempDir(), runner, nil, nil)
	rt.configOpts = cfg
	return rt, cfg
}

func setCompanionTier(t *testing.T, cfg wsconfig.Options, scope wsconfig.Scope, key, value string) {
	t.Helper()
	resolver := wsconfig.NewResolver(cfg, map[string]string{key: ""}, nil, nil)
	if value == "" {
		if err := resolver.Unset(key, wsconfig.SetOptions{ExplicitScope: scope}); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := resolver.Set(key, value, wsconfig.SetOptions{ExplicitScope: scope}); err != nil {
		t.Fatal(err)
	}
}

func setCompanionStructured(t *testing.T, cfg wsconfig.Options, key, raw string) {
	t.Helper()
	resolver := wsconfig.NewResolver(cfg, nil, nil, nil)
	if err := resolver.SetStructured(key, json.RawMessage(raw), wsconfig.ScopeGlobal); err != nil {
		t.Fatal(err)
	}
}

type tierWant struct{ harness, tier, model, effort, reason string }

func checkCompanionTier(t *testing.T, label string, got wsagent.CompanionTierResolution, want tierWant) {
	t.Helper()
	if got.Harness != want.harness || got.Tier != want.tier || got.Model != want.model || got.Effort != want.effort || !got.Available {
		t.Fatalf("%s = %+v, want %+v", label, got, want)
	}
	if (want.reason == "") != (got.FallbackReason == "") || !strings.Contains(got.FallbackReason, want.reason) {
		t.Fatalf("%s fallback reason = %q, want one containing %q", label, got.FallbackReason, want.reason)
	}
}

// Auto keeps the legacy launch: the role's size (small for the body, medium
// for work) on the first available harness in built-in order.
func TestCompanionTierAutoKeepsLegacyLaunch(t *testing.T) {
	rt, cfg := companionTierRuntime(t, "agy", "codex", "claude")
	checkCompanionTier(t, "body", rt.resolveRole(mcp.CompanionRoleBody), tierWant{"claude", "small", "claude-small", "small-effort", ""})
	checkCompanionTier(t, "work", rt.resolveRole(mcp.CompanionRoleWork), tierWant{"claude", "medium", "claude-medium", "medium-effort", ""})
	setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsdashboard.CompanionBodyTierKey, "auto")
	checkCompanionTier(t, "explicit auto", rt.resolveRole(mcp.CompanionRoleBody), tierWant{"claude", "small", "claude-small", "small-effort", ""})

	rt2, _ := companionTierRuntime(t, "agy", "codex")
	spec, ok := rt2.pickLaunchSpec(mcp.CompanionRoleWork)
	if !ok || spec != (companionLaunchSpec{Backend: "codex", Model: "codex-medium", Effort: "medium-effort"}) {
		t.Fatalf("auto work launch = %+v (%v), want codex medium", spec, ok)
	}
}

// An explicit value pins the harness it names: <harness>.<tier>, a bare
// tier on the first harness, an alias, or a routing bundle.
func TestCompanionTierExplicitValuesPinTheirHarness(t *testing.T) {
	rt, cfg := companionTierRuntime(t, "claude", "codex", "agy")
	setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsconfig.ItemAgentTierAliases, "brain=codex.large")
	setCompanionStructured(t, cfg, wsconfig.ItemAgentTierRouting, `{"bundles":{"quick":{"harness":"agy","model":"agy-quick","effort":"low"}}}`)
	for _, tc := range []struct {
		value string
		want  tierWant
	}{
		{"claude.small", tierWant{"claude", "small", "claude-small", "small-effort", ""}},
		{"codex.medium", tierWant{"codex", "medium", "codex-medium", "medium-effort", ""}},
		{"agy.small", tierWant{"agy", "small", "agy-small", "small-effort", ""}},
		{"Codex.Large", tierWant{"codex", "large", "codex-large", "large-effort", ""}},
		{"large", tierWant{"claude", "large", "claude-large", "large-effort", ""}},
		{"brain", tierWant{"codex", "large", "codex-large", "large-effort", ""}},
		{"quick", tierWant{"agy", "", "agy-quick", "low", ""}},
	} {
		setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsdashboard.CompanionWorkTierKey, tc.value)
		checkCompanionTier(t, tc.value, rt.resolveRole(mcp.CompanionRoleWork), tc.want)
	}
	spec, ok := rt.pickLaunchSpec(mcp.CompanionRoleWork)
	if !ok || spec != (companionLaunchSpec{Backend: "agy", Model: "agy-quick", Effort: "low"}) {
		t.Fatalf("bundle launch = %+v (%v)", spec, ok)
	}
}

// A value that cannot launch falls back to auto and says why; an unknown
// future harness name never breaks the read.
func TestCompanionTierUnavailableValuesFallBackToAuto(t *testing.T) {
	rt, cfg := companionTierRuntime(t, "claude", "codex", "werk")
	auto := tierWant{"claude", "small", "claude-small", "small-effort", ""}
	for _, tc := range []struct{ value, reason string }{
		{"agy.small", `harness "agy" is not registered`},
		{"future-harness.medium", `harness "future-harness" is not registered`},
		{"werk.small", "werk.small has no usable model"},
		{"codex.huge", "names no tier"},
		{"pi.small", "retired"},
		{"nonesuch", "neither a tier"},
	} {
		setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsdashboard.CompanionBodyTierKey, tc.value)
		want := auto
		want.reason = tc.reason
		got := rt.resolveRole(mcp.CompanionRoleBody)
		checkCompanionTier(t, tc.value, got, want)
		if got.Configured != strings.ToLower(tc.value) {
			t.Fatalf("%s: configured = %q", tc.value, got.Configured)
		}
	}
	var log bytes.Buffer
	rt.logw = &log
	if spec, ok := rt.pickLaunchSpec(mcp.CompanionRoleBody); !ok || spec.Backend != "claude" || spec.Model != "claude-small" {
		t.Fatalf("fallback launch = %+v (%v)", spec, ok)
	}
	if !strings.Contains(log.String(), "companion-body tier") || !strings.Contains(log.String(), "using auto") {
		t.Fatalf("fallback not logged: %q", log.String())
	}

	empty, _ := companionTierRuntime(t)
	if res := empty.resolveRole(mcp.CompanionRoleBody); res.Available || res.FallbackReason == "" {
		t.Fatalf("no harness resolution = %+v", res)
	}
	if _, ok := empty.pickLaunchSpec(mcp.CompanionRoleBody); ok {
		t.Fatal("a runner with no harness picked a launch")
	}
}

// The project value wins over the global one; resetting it restores global.
func TestCompanionTierProjectScopeOverridesGlobal(t *testing.T) {
	rt, cfg := companionTierRuntime(t, "claude", "codex", "agy")
	setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsdashboard.CompanionBodyTierKey, "codex.medium")
	setCompanionTier(t, cfg, wsconfig.ScopeProject, wsdashboard.CompanionBodyTierKey, "agy.small")
	checkCompanionTier(t, "project", rt.resolveRole(mcp.CompanionRoleBody), tierWant{"agy", "small", "agy-small", "small-effort", ""})
	setCompanionTier(t, cfg, wsconfig.ScopeProject, wsdashboard.CompanionBodyTierKey, "")
	checkCompanionTier(t, "global", rt.resolveRole(mcp.CompanionRoleBody), tierWant{"codex", "medium", "codex-medium", "medium-effort", ""})
	checkCompanionTier(t, "work untouched", rt.resolveRole(mcp.CompanionRoleWork), tierWant{"claude", "medium", "claude-medium", "medium-effort", ""})
}

// The readback carries both roles and the options: auto first, each
// available harness slot, then resolvable aliases; an alias to an
// unregistered harness is left out.
func TestCompanionAgentsReadbackListsOptions(t *testing.T) {
	rt, cfg := companionTierRuntime(t, "claude", "codex")
	setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsconfig.ItemAgentTierAliases, "brain=codex.large far=agy.small")
	setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsdashboard.CompanionWorkTierKey, "brain")
	raw, err := json.Marshal(rt.AgentsReadback())
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Body, Work map[string]any
		Options    []wsagent.CompanionTierOption
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Work["configured"] != "brain" || got.Work["harness"] != "codex" || got.Work["model"] != "codex-large" || got.Work["available"] != true {
		t.Fatalf("work readback = %v", got.Work)
	}
	if _, has := got.Body["fallback_reason"]; has || got.Body["configured"] != "" {
		t.Fatalf("body readback = %v", got.Body)
	}
	values := map[string]wsagent.CompanionTierOption{}
	for _, o := range got.Options {
		values[o.Value] = o
	}
	if len(got.Options) == 0 || got.Options[0].Value != "auto" || got.Options[0].Kind != "auto" {
		t.Fatalf("options must open with auto: %+v", got.Options)
	}
	for _, v := range []string{"claude.small", "claude.medium", "codex.large"} {
		if values[v].Kind != "harness" || values[v].Model == "" {
			t.Fatalf("option %s = %+v", v, values[v])
		}
	}
	if o := values["brain"]; o.Kind != "alias" || o.Target != "codex.large" || o.Model != "codex-large" || o.Effort != "large-effort" {
		t.Fatalf("alias option = %+v", o)
	}
	if _, listed := values["far"]; listed {
		t.Fatal("an alias to an unregistered harness was offered")
	}
}

// Bundles are listed after aliases unless an alias of the same name shadows
// them, and a name the manifest pattern rejects is never offered.
func TestCompanionTierOptionsListBundlesAndSkipUnsavableNames(t *testing.T) {
	rt, cfg := companionTierRuntime(t, "claude", "codex")
	setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsconfig.ItemAgentTierAliases, "brain=codex.large a@b=codex.small")
	setCompanionStructured(t, cfg, wsconfig.ItemAgentTierRouting,
		`{"bundles":{"quick":{"harness":"codex","model":"codex-quick","effort":"low"},"brain":{"harness":"claude","model":"claude-brain"}}}`)
	opts := rt.agents.CompanionTierOptions(cfg)
	kinds := map[string]string{}
	var order []string
	for _, o := range opts {
		kinds[o.Value] = o.Kind
		order = append(order, o.Value)
		if o.Value == "quick" && (o.Harness != "codex" || o.Model != "codex-quick" || o.Effort != "low") {
			t.Fatalf("bundle option = %+v", o)
		}
	}
	if kinds["quick"] != "bundle" || kinds["brain"] != "alias" {
		t.Fatalf("option kinds = %v", kinds)
	}
	if _, listed := kinds["a@b"]; listed {
		t.Fatalf("an unsavable alias name was offered: %v", order)
	}
	if order[len(order)-1] != "quick" {
		t.Fatalf("bundles must follow aliases: %v", order)
	}
}

// Auto skips a harness that cannot run workers, puts non-built-in harnesses
// after the built-ins by name, and launches the harness default model when
// the first harness has no slot.
func TestCompanionTierAutoCandidateRules(t *testing.T) {
	rt, _ := companionTierRuntime(t, "werk", "zz-b", "zz-a")
	checkCompanionTier(t, "slotless built-in first", rt.resolveRole(mcp.CompanionRoleBody), tierWant{"werk", "small", "", "", ""})
	rt2, _ := companionTierRuntime(t, "zz-b", "zz-a")
	checkCompanionTier(t, "custom by name", rt2.resolveRole(mcp.CompanionRoleWork), tierWant{"zz-a", "medium", "", "", ""})

	cfg := wsconfig.Options{CacheHome: filepath.Join(t.TempDir(), "cache"), ConfigHome: filepath.Join(t.TempDir(), "config")}
	backend := &companionTestBackend{}
	regs := map[string]wsagent.BackendRegistration{
		"claude": {Backend: backend, LeadProfile: wsagent.LeadProfileFor("claude")},
		"codex":  {Backend: backend, LeadProfile: wsagent.LeadProfileFor("codex"), WorkerCapable: true, WorkerProfile: wsagent.AgentWorkerProfile("codex")},
	}
	lead := newCompanionRuntime(t.TempDir(), t.TempDir(), wsagent.NewRunner(wsagent.Options{Dir: t.TempDir(), Backends: regs}), nil, nil)
	lead.configOpts = cfg
	if got := lead.resolveRole(mcp.CompanionRoleBody); got.Harness != "codex" {
		t.Fatalf("auto picked a harness that cannot run workers: %+v", got)
	}
	setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsdashboard.CompanionBodyTierKey, "claude.small")
	if got := lead.resolveRole(mcp.CompanionRoleBody); got.Harness != "codex" || got.FallbackReason == "" {
		t.Fatalf("an explicit worker-less harness must fall back: %+v", got)
	}
}

// A malformed agent.tier_aliases breaks only alias lookups: a plain name
// falls back to auto with the parse error, while <harness>.<tier> resolves.
func TestCompanionTierMalformedAliasesFallBackOnlyForNames(t *testing.T) {
	rt, cfg := companionTierRuntime(t, "claude", "codex")
	setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsconfig.ItemAgentTierAliases, "broken")
	setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsdashboard.CompanionBodyTierKey, "brain")
	checkCompanionTier(t, "alias", rt.resolveRole(mcp.CompanionRoleBody), tierWant{"claude", "small", "claude-small", "small-effort", "is not name="})
	setCompanionTier(t, cfg, wsconfig.ScopeGlobal, wsdashboard.CompanionBodyTierKey, "codex.small")
	checkCompanionTier(t, "dotted", rt.resolveRole(mcp.CompanionRoleBody), tierWant{"codex", "small", "codex-small", "small-effort", ""})
}
