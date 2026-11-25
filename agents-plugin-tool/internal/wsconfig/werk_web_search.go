package wsconfig

import (
	"slices"
	"strconv"
	"strings"
)

// Layered config keys for the builtin harness's (werk) server-side web search
// tool attachment and per-turn budget. Default write scope is ScopeGlobal; all
// five keys resolve across session > project > repo > global > builtin.
const (
	ItemWerkWebSearch           = "agent.werk_web_search"
	ItemWerkWebSearchMaxUses    = "agent.werk_web_search_max_uses"
	ItemWerkWebSearchMaxResults = "agent.werk_web_search_max_results"
	ItemWerkWebSearchEngine     = "agent.werk_web_search_engine"
	ItemWerkWebSearchSIWC       = "agent.werk_web_search_siwc"
)

// Builtin defaults for the werk server-side web search keys.
const (
	DefaultWerkWebSearchEnabled    = true
	DefaultWerkWebSearchMaxUses    = 2
	DefaultWerkWebSearchMaxResults = 5
	DefaultWerkWebSearchEngine     = "exa"
	DefaultWerkWebSearchSIWC       = false
)

// WerkWebSearchEngines lists the valid OpenRouter web search engine choices.
var WerkWebSearchEngines = []string{"exa", "auto", "native", "parallel", "perplexity", "firecrawl"}

// WerkWebSearchConfig is the resolved server-side web search configuration for
// one werk turn.
type WerkWebSearchConfig struct {
	Enabled    bool
	MaxUses    int
	MaxResults int
	Engine     string
	SIWC       bool
}

// DefaultWerkWebSearchConfig returns the builtin werk web search settings.
func DefaultWerkWebSearchConfig() WerkWebSearchConfig {
	return WerkWebSearchConfig{
		Enabled:    DefaultWerkWebSearchEnabled,
		MaxUses:    DefaultWerkWebSearchMaxUses,
		MaxResults: DefaultWerkWebSearchMaxResults,
		Engine:     DefaultWerkWebSearchEngine,
		SIWC:       DefaultWerkWebSearchSIWC,
	}
}

// WerkWebSearchDefaults returns the builtin string defaults for Resolver.
func WerkWebSearchDefaults() map[string]string {
	return map[string]string{
		ItemWerkWebSearch:           strconv.FormatBool(DefaultWerkWebSearchEnabled),
		ItemWerkWebSearchMaxUses:    strconv.Itoa(DefaultWerkWebSearchMaxUses),
		ItemWerkWebSearchMaxResults: strconv.Itoa(DefaultWerkWebSearchMaxResults),
		ItemWerkWebSearchEngine:     DefaultWerkWebSearchEngine,
		ItemWerkWebSearchSIWC:       strconv.FormatBool(DefaultWerkWebSearchSIWC),
	}
}

// ResolveWerkWebSearch resolves the werk web search configuration through the
// layered resolver. Hand-edited values that fail to parse fall back to defaults.
func ResolveWerkWebSearch(resolver *Resolver, sessionKey string) WerkWebSearchConfig {
	cfg := DefaultWerkWebSearchConfig()
	if resolver == nil {
		return cfg
	}
	if rv, err := resolver.Get(sessionKey, ItemWerkWebSearch); err == nil {
		if b, ok := parseConfigBool(rv.Value); ok {
			cfg.Enabled = b
		}
	}
	if rv, err := resolver.Get(sessionKey, ItemWerkWebSearchMaxUses); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(rv.Value)); err == nil && n >= 1 {
			cfg.MaxUses = n
		}
	}
	if rv, err := resolver.Get(sessionKey, ItemWerkWebSearchMaxResults); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(rv.Value)); err == nil && n >= 1 {
			cfg.MaxResults = n
		}
	}
	if rv, err := resolver.Get(sessionKey, ItemWerkWebSearchEngine); err == nil {
		engine := strings.ToLower(strings.TrimSpace(rv.Value))
		if slices.Contains(WerkWebSearchEngines, engine) {
			cfg.Engine = engine
		}
	}
	if rv, err := resolver.Get(sessionKey, ItemWerkWebSearchSIWC); err == nil {
		if b, ok := parseConfigBool(rv.Value); ok {
			cfg.SIWC = b
		}
	}
	return cfg
}

func parseConfigBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "on", "1", "yes":
		return true, true
	case "false", "off", "0", "no":
		return false, true
	default:
		return false, false
	}
}
