package mcp

import (
	"strconv"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// Server-side web search knobs for the builtin agent harness.
var (
	werkWebSearchKey = declaredConfigKey{
		Key:          wsconfig.ItemWerkWebSearch,
		Type:         declaredTypeBoolean,
		Description:  "Enable provider server-side web search for the builtin agent harness (OpenRouter by default; OpenAI SIWC also requires agent.werk_web_search_siwc).",
		DefaultScope: wsconfig.ScopeGlobal,
		Default:      strconv.FormatBool(wsconfig.DefaultWerkWebSearchEnabled),
		DefaultValue: wsconfig.DefaultWerkWebSearchEnabled,
	}
	werkWebSearchMaxUsesKey = declaredConfigKey{
		Key:          wsconfig.ItemWerkWebSearchMaxUses,
		Type:         declaredTypeInteger,
		Minimum:      int64Ptr(1),
		Description:  "Maximum server-side web searches per agent turn. The harness enforces this budget across requests in a turn and omits the search tool once spent; failed or cut requests stay charged on retry.",
		DefaultScope: wsconfig.ScopeGlobal,
		Default:      strconv.Itoa(wsconfig.DefaultWerkWebSearchMaxUses),
		DefaultValue: int64(wsconfig.DefaultWerkWebSearchMaxUses),
	}
	werkWebSearchMaxResultsKey = declaredConfigKey{
		Key:          wsconfig.ItemWerkWebSearchMaxResults,
		Type:         declaredTypeInteger,
		Minimum:      int64Ptr(1),
		Description:  "Maximum results returned per OpenRouter server-side web search call.",
		DefaultScope: wsconfig.ScopeGlobal,
		Default:      strconv.Itoa(wsconfig.DefaultWerkWebSearchMaxResults),
		DefaultValue: int64(wsconfig.DefaultWerkWebSearchMaxResults),
	}
	werkWebSearchEngineKey = declaredConfigKey{
		Key:          wsconfig.ItemWerkWebSearchEngine,
		Type:         declaredTypeEnum,
		Enum:         append([]string(nil), wsconfig.WerkWebSearchEngines...),
		Description:  "OpenRouter web search engine (exa, auto, native, parallel, perplexity, firecrawl). The default exa engine honors max_uses within a request; native and auto may exceed the per-request search budget.",
		DefaultScope: wsconfig.ScopeGlobal,
		Default:      wsconfig.DefaultWerkWebSearchEngine,
		DefaultValue: wsconfig.DefaultWerkWebSearchEngine,
	}
	werkWebSearchSIWCKey = declaredConfigKey{
		Key:          wsconfig.ItemWerkWebSearchSIWC,
		Type:         declaredTypeBoolean,
		Description:  "Opt in to OpenAI Sign in with ChatGPT (SIWC) server-side web search when agent.werk_web_search is enabled. Because SIWC has no per-request search cap, the per-turn budget is best-effort between requests.",
		DefaultScope: wsconfig.ScopeGlobal,
		Default:      strconv.FormatBool(wsconfig.DefaultWerkWebSearchSIWC),
		DefaultValue: wsconfig.DefaultWerkWebSearchSIWC,
	}
)

// werkWebSearchKeys lists the builtin harness web search knobs for every built-in registration site.
var werkWebSearchKeys = []declaredConfigKey{
	werkWebSearchKey,
	werkWebSearchMaxUsesKey,
	werkWebSearchMaxResultsKey,
	werkWebSearchEngineKey,
	werkWebSearchSIWCKey,
}
