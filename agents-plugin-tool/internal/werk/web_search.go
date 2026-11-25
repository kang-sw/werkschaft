package werk

import (
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
)

// WebSearchPolicy controls server-side web search attachment for one turn.
type WebSearchPolicy struct {
	Enabled    bool
	MaxUses    int
	MaxResults int
	Engine     string
	SIWC       bool
}

// DefaultWebSearchPolicy returns the builtin search policy: OpenRouter on with
// 2 searches per turn, 5 results, Exa engine; OpenAI SIWC opt-in off.
func DefaultWebSearchPolicy() WebSearchPolicy {
	d := wsconfig.DefaultWerkWebSearchConfig()
	return WebSearchPolicy{
		Enabled:    d.Enabled,
		MaxUses:    d.MaxUses,
		MaxResults: d.MaxResults,
		Engine:     d.Engine,
		SIWC:       d.SIWC,
	}
}

// turnSearchBudget enforces one turn's server-side search budget across model
// requests and retries. Before a request carries search, reserve deducts the
// entire remaining budget; settle refunds unused budget only when a completed
// response arrives with usage. A cut stream, failed request, or missing usage
// leaves the reservation charged so retries omit the search tool.
type turnSearchBudget struct {
	enabled    bool
	remaining  int
	engine     string
	maxResults int
}

func newTurnSearchBudget(provider wscred.ProviderID, dialect string, policy WebSearchPolicy, priorUsed int) *turnSearchBudget {
	if dialect != "" || !policy.Enabled || policy.MaxUses <= 0 {
		return &turnSearchBudget{}
	}
	switch provider {
	case wscred.ProviderOpenRouter:
	case wscred.ProviderOpenAI:
		if !policy.SIWC {
			return &turnSearchBudget{}
		}
	default:
		return &turnSearchBudget{}
	}
	engine := strings.ToLower(strings.TrimSpace(policy.Engine))
	if !slices.Contains(wsconfig.WerkWebSearchEngines, engine) {
		engine = wsconfig.DefaultWerkWebSearchEngine
	}
	maxResults := policy.MaxResults
	if maxResults <= 0 {
		maxResults = wsconfig.DefaultWerkWebSearchMaxResults
	}
	rem := policy.MaxUses - max(0, priorUsed)
	if rem < 0 {
		rem = 0
	}
	return &turnSearchBudget{
		enabled:    true,
		remaining:  rem,
		engine:     engine,
		maxResults: maxResults,
	}
}

// reserve claims the remaining search budget for one outbound request attempt.
// It returns (nil, 0) once the budget is spent or search is disabled.
func (b *turnSearchBudget) reserve() (*WebSearchRequest, int) {
	if b == nil || !b.enabled || b.remaining <= 0 {
		return nil, 0
	}
	reserved := b.remaining
	b.remaining = 0
	return &WebSearchRequest{
		Engine:     b.engine,
		MaxUses:    reserved,
		MaxResults: b.maxResults,
	}, reserved
}

// settle reconciles reserved against a completed response's reported searches.
// When usage did not arrive, the reservation stays charged.
func (b *turnSearchBudget) settle(reserved int, resp *Response) {
	if b == nil || reserved <= 0 || resp == nil || resp.UsageMissing {
		return
	}
	used := resp.webSearchesUsed()
	if refund := reserved - used; refund > 0 {
		b.remaining += refund
	}
}

func (b *turnSearchBudget) exhaust() {
	if b != nil {
		b.remaining = 0
	}
}
