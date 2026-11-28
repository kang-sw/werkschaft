package wsmeta

import (
	"context"
	"database/sql"
	"math"
	"strings"
)

// WerkModelUsageKey identifies one persisted werk per-model usage aggregate.
// AccountKey is optional: empty for single-key providers (e.g. OpenRouter) and
// the non-secret ClientID for OpenAI SIWC accounts (1809).
type WerkModelUsageKey struct {
	Provider   string
	AccountKey string
	Model      string
}

// WerkModelUsage holds cumulative token and cost aggregates (or a batch delta
// to add) for one (Provider, AccountKey, Model) tuple.
type WerkModelUsage struct {
	Provider         string
	AccountKey       string
	Model            string
	Requests         int64
	InputTokens      int64
	OutputTokens     int64
	CachedTokens     int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	CostUSD          float64
	UpdatedAt        int64
}

// Key returns the aggregate's map key.
func (u WerkModelUsage) Key() WerkModelUsageKey {
	return WerkModelUsageKey{
		Provider:   u.Provider,
		AccountKey: u.AccountKey,
		Model:      u.Model,
	}
}

func sanitizeWerkUsageDelta(d WerkModelUsage) (WerkModelUsage, bool) {
	d.Provider = strings.TrimSpace(d.Provider)
	d.AccountKey = strings.TrimSpace(d.AccountKey)
	d.Model = strings.TrimSpace(d.Model)
	if d.Provider == "" || d.Model == "" {
		return WerkModelUsage{}, false
	}
	if d.Requests < 0 {
		d.Requests = 0
	}
	if d.InputTokens < 0 {
		d.InputTokens = 0
	}
	if d.OutputTokens < 0 {
		d.OutputTokens = 0
	}
	if d.CachedTokens < 0 {
		d.CachedTokens = 0
	}
	if d.CacheWriteTokens < 0 {
		d.CacheWriteTokens = 0
	}
	if d.ReasoningTokens < 0 {
		d.ReasoningTokens = 0
	}
	if math.IsNaN(d.CostUSD) || math.IsInf(d.CostUSD, 0) || d.CostUSD < 0 {
		d.CostUSD = 0
	}
	if d.UpdatedAt < 0 {
		d.UpdatedAt = 0
	}
	return d, true
}

// AddWerkModelUsageDeltas folds a batch of per-model usage deltas into
// werk_model_usage in a single relaxed-durability WAL transaction. Empty or
// malformed entries are skipped; a batch with no valid entries commits nothing.
func (s *Store) AddWerkModelUsageDeltas(ctx context.Context, deltas []WerkModelUsage) error {
	if len(deltas) == 0 {
		return nil
	}
	clean := make([]WerkModelUsage, 0, len(deltas))
	for _, raw := range deltas {
		if d, ok := sanitizeWerkUsageDelta(raw); ok {
			clean = append(clean, d)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	return s.write(ctx, relaxed, func(tx *sql.Tx) error {
		if err := s.requireImported(ctx, tx); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO werk_model_usage(
			provider, account_key, model,
			requests, input_tokens, output_tokens, cached_tokens, cache_write_tokens, reasoning_tokens, cost_usd, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, account_key, model) DO UPDATE SET
			requests = werk_model_usage.requests + excluded.requests,
			input_tokens = werk_model_usage.input_tokens + excluded.input_tokens,
			output_tokens = werk_model_usage.output_tokens + excluded.output_tokens,
			cached_tokens = werk_model_usage.cached_tokens + excluded.cached_tokens,
			cache_write_tokens = werk_model_usage.cache_write_tokens + excluded.cache_write_tokens,
			reasoning_tokens = werk_model_usage.reasoning_tokens + excluded.reasoning_tokens,
			cost_usd = werk_model_usage.cost_usd + excluded.cost_usd,
			updated_at = MAX(werk_model_usage.updated_at, excluded.updated_at)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, d := range clean {
			if _, err := stmt.ExecContext(ctx,
				d.Provider, d.AccountKey, d.Model,
				d.Requests, d.InputTokens, d.OutputTokens,
				d.CachedTokens, d.CacheWriteTokens, d.ReasoningTokens,
				d.CostUSD, d.UpdatedAt,
			); err != nil {
				return err
			}
		}
		return nil
	})
}
