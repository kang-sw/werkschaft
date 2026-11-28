package wsdashboard

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsmeta"
)

const defaultWerkUsageFlushDebounce = 30 * time.Second

// WerkUsageDelta carries one completed werk model request's token and cost
// figures into the daemon's batched per-model usage store. AccountKey is
// optional: empty for OpenRouter and the non-secret ClientID for OpenAI SIWC.
type WerkUsageDelta struct {
	Provider         string
	AccountKey       string
	Model            string
	InputTokens      int64
	OutputTokens     int64
	CachedTokens     int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	CostUSD          float64
	At               int64
}

// werkModelUsageRow is the JSON-projected per-model breakdown embedded in
// werk usage cards (OpenRouter and OpenAI SIWC).
type werkModelUsageRow struct {
	Model            string  `json:"model"`
	Requests         int64   `json:"requests,omitempty"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CachedTokens     int64   `json:"cachedTokens,omitempty"`
	CacheWriteTokens int64   `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens  int64   `json:"reasoningTokens,omitempty"`
	CostUSD          float64 `json:"costUsd,omitempty"`
	UpdatedAt        int64   `json:"updatedAt,omitempty"`
}

func (s *planUsageStore) loadWerkUsageFromMetaLocked(snap *wsmeta.Snapshot) {
	if snap == nil || len(snap.WerkModelUsage) == 0 {
		return
	}
	if s.werkUsage == nil {
		s.werkUsage = make(map[wsmeta.WerkModelUsageKey]wsmeta.WerkModelUsage, len(snap.WerkModelUsage))
	}
	for k, v := range snap.WerkModelUsage {
		s.werkUsage[k] = v
	}
}

func sanitizeWerkUsageDelta(d WerkUsageDelta) (wsmeta.WerkModelUsage, bool) {
	provider := strings.TrimSpace(d.Provider)
	model := strings.TrimSpace(d.Model)
	accountKey := strings.TrimSpace(d.AccountKey)
	if provider == "" || model == "" {
		return wsmeta.WerkModelUsage{}, false
	}
	clamp := func(v int64) int64 {
		if v < 0 {
			return 0
		}
		return v
	}
	cost := d.CostUSD
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
		cost = 0
	}
	at := d.At
	if at < 0 {
		at = 0
	}
	return wsmeta.WerkModelUsage{
		Provider:         provider,
		AccountKey:       accountKey,
		Model:            model,
		Requests:         1,
		InputTokens:      clamp(d.InputTokens),
		OutputTokens:     clamp(d.OutputTokens),
		CachedTokens:     clamp(d.CachedTokens),
		CacheWriteTokens: clamp(d.CacheWriteTokens),
		ReasoningTokens:  clamp(d.ReasoningTokens),
		CostUSD:          cost,
		UpdatedAt:        at,
	}, true
}

func foldWerkUsage(dst *wsmeta.WerkModelUsage, add wsmeta.WerkModelUsage) {
	if dst.Provider == "" {
		dst.Provider = add.Provider
		dst.AccountKey = add.AccountKey
		dst.Model = add.Model
	}
	dst.Requests += add.Requests
	dst.InputTokens += add.InputTokens
	dst.OutputTokens += add.OutputTokens
	dst.CachedTokens += add.CachedTokens
	dst.CacheWriteTokens += add.CacheWriteTokens
	dst.ReasoningTokens += add.ReasoningTokens
	dst.CostUSD += add.CostUSD
	if add.UpdatedAt > dst.UpdatedAt {
		dst.UpdatedAt = add.UpdatedAt
	}
}

func (s *planUsageStore) werkDebounce() time.Duration {
	if s.werkFlushDebounce > 0 {
		return s.werkFlushDebounce
	}
	return defaultWerkUsageFlushDebounce
}

// recordWerkUsage folds one completed werk response into memory immediately so
// /rail/usage reads stay zero-I/O, and schedules a debounced SQLite delta flush.
func (s *planUsageStore) recordWerkUsage(d WerkUsageDelta) {
	if s == nil {
		return
	}
	delta, ok := sanitizeWerkUsageDelta(d)
	if !ok {
		return
	}
	if delta.UpdatedAt == 0 {
		delta.UpdatedAt = s.nowTime().UnixMilli()
	}
	key := delta.Key()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.werkUsage == nil {
		s.werkUsage = map[wsmeta.WerkModelUsageKey]wsmeta.WerkModelUsage{}
	}
	cur := s.werkUsage[key]
	foldWerkUsage(&cur, delta)
	s.werkUsage[key] = cur

	if s.meta == nil {
		return
	}
	if s.werkPending == nil {
		s.werkPending = map[wsmeta.WerkModelUsageKey]wsmeta.WerkModelUsage{}
	}
	pend := s.werkPending[key]
	foldWerkUsage(&pend, delta)
	s.werkPending[key] = pend

	if s.closed {
		return
	}
	if s.werkFlushTimer == nil {
		s.werkFlushTimer = time.AfterFunc(s.werkDebounce(), s.flushWerkUsageFromTimer)
	}
}

func (s *planUsageStore) flushWerkUsageFromTimer() {
	s.mu.Lock()
	s.werkFlushTimer = nil
	if s.closed || s.meta == nil || len(s.werkPending) == 0 {
		s.mu.Unlock()
		return
	}
	s.probes.Add(1)
	ctx := s.lifetimeCtx
	s.mu.Unlock()
	defer s.probes.Done()
	_ = s.flushWerkUsage(ctx)
}

func (s *planUsageStore) stopWerkFlushLocked() {
	if s.werkFlushTimer != nil {
		s.werkFlushTimer.Stop()
		s.werkFlushTimer = nil
	}
}

// flushWerkUsage drains accumulated in-memory per-model deltas into wsmeta in
// one WAL transaction. Transient write errors restore the drained batch so no
// usage is dropped.
func (s *planUsageStore) flushWerkUsage(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.stopWerkFlushLocked()
	meta := s.meta
	if meta == nil || len(s.werkPending) == 0 {
		s.mu.Unlock()
		return nil
	}
	batch := make([]wsmeta.WerkModelUsage, 0, len(s.werkPending))
	for _, v := range s.werkPending {
		batch = append(batch, v)
	}
	s.werkPending = nil
	s.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	err := meta.AddWerkModelUsageDeltas(ctx, batch)
	if err != nil && !errors.Is(err, wsmeta.ErrClosed) {
		s.mu.Lock()
		if s.werkPending == nil {
			s.werkPending = make(map[wsmeta.WerkModelUsageKey]wsmeta.WerkModelUsage, len(batch))
		}
		for _, v := range batch {
			k := v.Key()
			cur := s.werkPending[k]
			foldWerkUsage(&cur, v)
			s.werkPending[k] = cur
		}
		if !s.closed && s.werkFlushTimer == nil {
			s.werkFlushTimer = time.AfterFunc(s.werkDebounce(), s.flushWerkUsageFromTimer)
		}
		s.mu.Unlock()
		return err
	}
	return err
}

// werkModelRowsLocked returns per-model usage rows for provider and accountKey
// from memory (zero disk I/O). When filterAccount is true, only rows matching
// accountKey are included; otherwise all account keys for provider are folded
// by model.
func (s *planUsageStore) werkModelRowsLocked(provider string, filterAccount bool, accountKey string, tokenFirst bool) ([]werkModelUsageRow, int64, int64, float64, int64) {
	if len(s.werkUsage) == 0 {
		return nil, 0, 0, 0, 0
	}
	byModel := map[string]*werkModelUsageRow{}
	var totalIn, totalOut, latestAt int64
	var totalCost float64
	for _, u := range s.werkUsage {
		if u.Provider != provider {
			continue
		}
		if filterAccount && u.AccountKey != accountKey {
			continue
		}
		row := byModel[u.Model]
		if row == nil {
			row = &werkModelUsageRow{Model: u.Model}
			byModel[u.Model] = row
		}
		row.Requests += u.Requests
		row.InputTokens += u.InputTokens
		row.OutputTokens += u.OutputTokens
		row.CachedTokens += u.CachedTokens
		row.CacheWriteTokens += u.CacheWriteTokens
		row.ReasoningTokens += u.ReasoningTokens
		row.CostUSD += u.CostUSD
		if u.UpdatedAt > row.UpdatedAt {
			row.UpdatedAt = u.UpdatedAt
		}
		totalIn += u.InputTokens
		totalOut += u.OutputTokens
		totalCost += u.CostUSD
		if u.UpdatedAt > latestAt {
			latestAt = u.UpdatedAt
		}
	}
	if len(byModel) == 0 {
		return nil, 0, 0, 0, 0
	}
	rows := make([]werkModelUsageRow, 0, len(byModel))
	for _, row := range byModel {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if tokenFirst {
			ti := rows[i].InputTokens + rows[i].OutputTokens
			tj := rows[j].InputTokens + rows[j].OutputTokens
			if ti != tj {
				return ti > tj
			}
		} else if rows[i].CostUSD != rows[j].CostUSD {
			return rows[i].CostUSD > rows[j].CostUSD
		}
		ti := rows[i].InputTokens + rows[i].OutputTokens
		tj := rows[j].InputTokens + rows[j].OutputTokens
		if ti != tj {
			return ti > tj
		}
		return rows[i].Model < rows[j].Model
	})
	return rows, totalIn, totalOut, totalCost, latestAt
}
