package wsconfigservice

import (
	"fmt"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// WriteTier applies the existing config.tune compound value to its tier leaf.
func WriteTier(opts wsconfig.Options, value any, harness string, scope wsconfig.Scope, reset bool) (wsconfig.Config, bool, error) {
	fields, ok := value.(map[string]any)
	if !ok {
		return wsconfig.Config{}, false, fmt.Errorf("config.tune: agents.tier value must be an object with tier/backend/model/effort/provider fields (only tier when reset is true)")
	}
	// Tier validation belongs to wsconfig: it accepts legacy synonyms as well
	// as canonical tiers, matching the CLI and the former writer.
	tier, _ := fields["tier"].(string)
	if reset {
		if len(fields) != 1 {
			return wsconfig.Config{}, false, fmt.Errorf("config.tune: agents.tier reset value must contain only tier")
		}
	}
	global := scope == wsconfig.ScopeGlobal
	var cfg wsconfig.Config
	var err error
	removed := false
	if reset {
		if global {
			cfg, removed, err = wsconfig.UnsetGlobalAgentsTierForHarness(opts, tier, harness)
		} else {
			cfg, removed, err = wsconfig.UnsetAgentsTierForHarness(opts, tier, harness)
		}
	} else {
		var mapping wsconfig.AgentTier
		mapping.Backend, _ = fields["backend"].(string)
		mapping.Model, _ = fields["model"].(string)
		mapping.Effort, _ = fields["effort"].(string)
		if provider, present := fields["provider"]; present {
			var ok bool
			mapping.Provider, ok = provider.(string)
			if !ok {
				return wsconfig.Config{}, false, fmt.Errorf("config.tune: agents.tier provider must be a string")
			}
		}
		cfg, err = wsconfig.SetAgentTierValue(opts, global, tier, harness, mapping)
	}
	return cfg, removed, err
}
