package wsdashboard

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
)

const (
	openRouterDefaultDebounce = 10 * time.Minute
	openRouterUsageSource     = "via OpenRouter"
)

type openRouterUsageStore interface {
	OpenRouterUsageMeta() (configured bool, masked string, rejected bool, generation uint64)
	ProbeOpenRouterUsage(ctx context.Context) (wscred.OpenRouterUsageSnapshot, error)
}

// openRouterUsageView carries the additive OpenRouter hybrid metrics projected
// on /rail/usage. Unavailable fields are omitted cleanly.
type openRouterUsageView struct {
	TotalCredits      *float64            `json:"totalCredits,omitempty"`
	TotalUsage        *float64            `json:"totalUsage,omitempty"`
	Balance           *float64            `json:"balance,omitempty"`
	Limit             *float64            `json:"limit,omitempty"`
	LimitRemaining    *float64            `json:"limitRemaining,omitempty"`
	Usage             *float64            `json:"usage,omitempty"`
	UsageDaily        *float64            `json:"usageDaily,omitempty"`
	UsageWeekly       *float64            `json:"usageWeekly,omitempty"`
	UsageMonthly      *float64            `json:"usageMonthly,omitempty"`
	IsFreeTier        bool                `json:"isFreeTier,omitempty"`
	CreditsRestricted bool                `json:"creditsRestricted,omitempty"`
	TotalInputTokens  int64               `json:"totalInputTokens,omitempty"`
	TotalOutputTokens int64               `json:"totalOutputTokens,omitempty"`
	TotalCostUSD      float64             `json:"totalCostUsd,omitempty"`
	Models            []werkModelUsageRow `json:"models,omitempty"`
}

func (s *planUsageStore) openRouterStore() openRouterUsageStore {
	if s == nil || s.credentials == nil {
		return nil
	}
	store, _ := s.credentials.(openRouterUsageStore)
	return store
}

func (s *planUsageStore) openRouterDebounceInterval() time.Duration {
	if s != nil && s.openRouterDebounce > 0 {
		return s.openRouterDebounce
	}
	return openRouterDefaultDebounce
}

func (s *planUsageStore) resetOpenRouterLocked() {
	s.openRouterSnap = nil
	s.openRouterStatus = ""
	s.openRouterLastProbe = time.Time{}
}

// refreshOpenRouter triggers a single debounced background /api/v1/key +
// /api/v1/credits probe when an OpenRouter key is configured. It never blocks
// /rail/usage reads and is debounced to at most once per 10 minutes by default.
func (s *planUsageStore) refreshOpenRouter() {
	store := s.openRouterStore()
	if store == nil {
		return
	}
	configured, _, rejected, gen := store.OpenRouterUsageMeta()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if !configured {
		s.resetOpenRouterLocked()
		s.openRouterGen = gen
		s.mu.Unlock()
		return
	}
	if gen != s.openRouterGen {
		s.resetOpenRouterLocked()
		s.openRouterGen = gen
	}
	if rejected {
		s.openRouterStatus = "key rejected"
		s.mu.Unlock()
		return
	}
	now := s.nowTime()
	if s.openRouterProbing {
		s.mu.Unlock()
		return
	}
	if !s.openRouterLastProbe.IsZero() && now.Sub(s.openRouterLastProbe) < s.openRouterDebounceInterval() {
		s.mu.Unlock()
		return
	}
	parentCtx := s.lifetimeCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	s.openRouterProbing = true
	s.probes.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.probes.Done()
		ctx, cancel := context.WithTimeout(parentCtx, 15*time.Second)
		defer cancel()
		snap, err := store.ProbeOpenRouterUsage(ctx)
		_, _, _, latestGen := store.OpenRouterUsageMeta()

		s.mu.Lock()
		defer s.mu.Unlock()
		s.openRouterProbing = false
		if s.closed || latestGen != gen || s.openRouterGen != gen {
			return
		}
		s.openRouterLastProbe = s.nowTime()
		switch {
		case err == nil:
			cp := snap
			s.openRouterSnap = &cp
			s.openRouterStatus = ""
		case errors.Is(err, wscred.ErrOpenRouterKeyRejected):
			s.openRouterSnap = nil
			s.openRouterStatus = "key rejected"
		case errors.Is(err, wscred.ErrNotConfigured):
			s.resetOpenRouterLocked()
		default:
			if s.openRouterSnap == nil {
				s.openRouterStatus = "usage unavailable"
			}
		}
	}()
}

// openRouterEntryWithMetaLocked builds the additive OpenRouter usage card from
// RAM only (zero disk or network I/O) without nesting wscred locks under s.mu.
func (s *planUsageStore) openRouterEntryWithMetaLocked(configured bool, masked string, rejected bool, gen uint64) (planUsageEntry, bool) {
	if gen != s.openRouterGen {
		s.resetOpenRouterLocked()
		s.openRouterGen = gen
	}
	models, totalIn, totalOut, totalCost, latestModelAt := s.werkModelRowsLocked(string(wscred.ProviderOpenRouter), false, "", false)
	if !configured && len(models) == 0 {
		return planUsageEntry{}, false
	}

	view := &openRouterUsageView{
		TotalInputTokens:  totalIn,
		TotalOutputTokens: totalOut,
		TotalCostUSD:      totalCost,
		Models:            models,
	}
	var asOf int64
	if s.openRouterSnap != nil && configured {
		asOf = s.openRouterSnap.AsOf
		view.CreditsRestricted = s.openRouterSnap.CreditsRestricted
		if c := s.openRouterSnap.Credits; c != nil {
			tc := c.TotalCredits
			tu := c.TotalUsage
			bal := tc - tu
			if !math.IsNaN(bal) && !math.IsInf(bal, 0) {
				view.TotalCredits = &tc
				view.TotalUsage = &tu
				view.Balance = &bal
			}
		}
		if k := s.openRouterSnap.Key; k != nil {
			view.Limit = k.Limit
			view.LimitRemaining = k.LimitRemaining
			view.Usage = k.Usage
			view.UsageDaily = k.UsageDaily
			view.UsageWeekly = k.UsageWeekly
			view.UsageMonthly = k.UsageMonthly
			view.IsFreeTier = k.IsFreeTier
		}
	}
	if latestModelAt > asOf {
		asOf = latestModelAt
	}
	status := s.openRouterStatus
	if rejected {
		status = "key rejected"
	} else if status == "" && s.openRouterSnap == nil && len(models) == 0 && !s.openRouterProbing {
		status = "usage unavailable"
	}
	entry := planUsageEntry{
		Harness:      string(wscred.ProviderOpenRouter),
		AccountLabel: masked,
		Source:       openRouterUsageSource,
		Status:       status,
		AsOf:         asOf,
		Windows:      []planUsageWindow{},
		OpenRouter:   view,
	}
	if s.openRouterProbing && s.openRouterSnap == nil && len(models) == 0 {
		entry.Pending = true
	}
	return entry, true
}
