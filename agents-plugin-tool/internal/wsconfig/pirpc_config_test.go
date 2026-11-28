package wsconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPiRPCTierWritesPreserveBackendAndAvoidCodexSeed(t *testing.T) {
	tmp := t.TempDir()
	opts := Options{
		CacheHome:  filepath.Join(tmp, "cache"),
		ConfigHome: filepath.Join(tmp, "global"),
	}

	// Model names that InferBackend would classify as "claude" or "codex" must
	// stay bound to "pi-rpc" when written to the "pi-rpc" harness bucket.
	if _, err := SetGlobalAgentsTierForHarness(opts, "large", "", "openrouter/anthropic/claude-3.7-sonnet", "pi-rpc", "high"); err != nil {
		t.Fatalf("set pi-rpc large: %v", err)
	}
	if _, err := SetGlobalAgentsTierForHarness(opts, "medium", "", "openai/gpt-5", "pi-rpc", "medium"); err != nil {
		t.Fatalf("set pi-rpc medium: %v", err)
	}
	// Effort-only edit on an unset pi-rpc tier must not seed Codex defaults.
	if _, err := SetGlobalAgentsTierForHarness(opts, "small", "", "", "pi-rpc", "low"); err != nil {
		t.Fatalf("set pi-rpc small effort-only: %v", err)
	}

	tiers, err := HarnessTiers(opts, "pi-rpc")
	if err != nil {
		t.Fatalf("HarnessTiers(pi-rpc): %v", err)
	}
	if got := tiers["large"]; got.Backend != "pi-rpc" || got.Model != "openrouter/anthropic/claude-3.7-sonnet" || got.Effort != "high" {
		t.Fatalf("pi-rpc large tier = %+v", got)
	}
	if got := tiers["medium"]; got.Backend != "pi-rpc" || got.Model != "openai/gpt-5" || got.Effort != "medium" {
		t.Fatalf("pi-rpc medium tier = %+v", got)
	}
	if _, hasSmall := tiers["small"]; hasSmall {
		t.Fatalf("effort-only edit on unset pi-rpc small tier must not seed a model: %+v", tiers["small"])
	}

	// Inspect raw global config file to confirm small has no seeded Codex backend/model.
	globalPath, err := GlobalPath(opts)
	if err != nil {
		t.Fatalf("GlobalPath: %v", err)
	}
	raw, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal global config: %v", err)
	}
	small := cfg.Agents.ModelAliases["small"]["pi-rpc"]
	if small.Backend != "" || small.Model != "" || small.Effort != "low" {
		t.Fatalf("raw pi-rpc small = %+v, want empty backend/model with effort=low", small)
	}

	// Write a pi-rpc tier with omitted backend directly on disk and verify both
	// resolvers default backend to "pi-rpc" instead of InferBackend(model).
	cfg.Agents.ModelAliases["xlarge"] = map[string]AgentTier{
		"pi-rpc": {Model: "openai/gpt-5.4", Effort: "xhigh"},
	}
	patched, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, patched, 0o644); err != nil {
		t.Fatal(err)
	}

	backend, model, effort, err := ResolveAgentForHarnessConfig(opts, "xlarge", "", "", "pi-rpc")
	if err != nil {
		t.Fatalf("ResolveAgentForHarnessConfig(xlarge, pi-rpc): %v", err)
	}
	if backend != "pi-rpc" || model != "openai/gpt-5.4" || effort != "xhigh" {
		t.Fatalf("ResolveAgentForHarnessConfig = (%q, %q, %q), want (pi-rpc, openai/gpt-5.4, xhigh)", backend, model, effort)
	}

	backend, model, effort, from, err := ResolveAgentTierForHarness(opts, "xlarge", "pi-rpc")
	if err != nil {
		t.Fatalf("ResolveAgentTierForHarness(xlarge, pi-rpc): %v", err)
	}
	if backend != "pi-rpc" || model != "openai/gpt-5.4" || effort != "xhigh" || from != "pi-rpc" {
		t.Fatalf("ResolveAgentTierForHarness = (%q, %q, %q, %q)", backend, model, effort, from)
	}
}

func TestPiRPCInspectTierIsExplicitOnlyAndBucketDefaultScopes(t *testing.T) {
	files := map[Scope]Config{
		ScopeGlobal: {
			Agents: AgentsConfig{
				ModelAliases: map[string]map[string]AgentTier{
					"large": {
						"pi-rpc": {Model: "anthropic/claude-opus-4-6", Effort: "high"},
					},
				},
			},
		},
		ScopeProject: {},
	}

	// Unconfigured pi-rpc tier must stay unconfigured (no default/codex fallback,
	// no WerkDefaultProvider injection).
	unset := InspectTier(files, "medium", "pi-rpc")
	if unset.Scope != ScopeBuiltin || unset.Bucket != "" || unset.Value != (AgentTier{}) || len(unset.Layers) != 0 {
		t.Fatalf("unconfigured InspectTier(medium, pi-rpc) = %+v", unset)
	}

	// Configured pi-rpc tier surfaces explicit layer with backend defaulted to
	// "pi-rpc" and empty provider.
	set := InspectTier(files, "large", "pi-rpc")
	if set.Scope != ScopeGlobal || set.Bucket != "pi-rpc" {
		t.Fatalf("configured InspectTier(large, pi-rpc) scope/bucket = (%q, %q)", set.Scope, set.Bucket)
	}
	if set.Value.Backend != "pi-rpc" || set.Value.Model != "anthropic/claude-opus-4-6" || set.Value.Provider != "" {
		t.Fatalf("configured InspectTier(large, pi-rpc) value = %+v", set.Value)
	}

	// All five harness-bucketed config keys must default to ScopeGlobal for pi-rpc.
	for _, key := range []string{
		ItemWorkflowWhiteboardEnabled + ".pi-rpc",
		ItemWorkflowTrackerEnabled + ".pi-rpc",
		ItemExecNativeToolRemoval + ".pi-rpc",
		ItemAgentQuotaContinuation + ".pi-rpc",
		ItemWorkflowProfile + ".pi-rpc",
	} {
		if got := DefaultScope(key); got != ScopeGlobal {
			t.Errorf("DefaultScope(%q) = %q, want %q", key, got, ScopeGlobal)
		}
	}
}
