package wsdashboard

import (
	"context"
	"errors"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
)

// Retained name check: wire identifiers and source label for the /rail/usage
// Werk OpenAI and unified Codex/OpenAI cards
// (TestPlanUsageCodexOpenAIExactWorkspaceMatchUnifiesAndDeduplicatesProbe,
// TestPlanUsageSameEmailDifferentWorkspacesStaySeparate).
const (
	werkOpenAIDefaultDebounce = 10 * time.Minute
	werkOpenAIUsageSource     = "via Werk OpenAI"
	codexOpenAIUnifiedHarness = "codex_openai"
	werkOpenAIHarness         = "werk_openai"
)

type openAIUsageStore interface {
	ActiveOpenAIUsageIdentity() wscred.OpenAIUsageIdentity
	ProbeOpenAIPlanUsage(ctx context.Context) (wscred.OpenAIPlanUsageResult, error)
}

// werkUsageView carries the additive Werk OpenAI usage projection on
// /rail/usage (attached to unified "codex_openai" and separate "werk_openai").
type werkUsageView struct {
	Unified           bool                `json:"unified,omitempty"`
	Mode              string              `json:"mode,omitempty"`
	FallbackReason    string              `json:"fallbackReason,omitempty"`
	PlanPaused        bool                `json:"planPaused,omitempty"`
	TokenFirst        bool                `json:"tokenFirst,omitempty"`
	TotalInputTokens  int64               `json:"totalInputTokens,omitempty"`
	TotalOutputTokens int64               `json:"totalOutputTokens,omitempty"`
	EstimatedCostUSD  float64             `json:"estimatedCostUsd,omitempty"`
	Models            []werkModelUsageRow `json:"models,omitempty"`
}

func (s *planUsageStore) openAIStore() openAIUsageStore {
	if s == nil || s.credentials == nil {
		return nil
	}
	store, _ := s.credentials.(openAIUsageStore)
	return store
}

func (s *planUsageStore) openAIUsageIdentity() (wscred.OpenAIUsageIdentity, bool) {
	store := s.openAIStore()
	if store == nil {
		return wscred.OpenAIUsageIdentity{}, false
	}
	id := store.ActiveOpenAIUsageIdentity()
	return id, id.Configured
}

func (s *planUsageStore) werkOpenAIDebounceInterval() time.Duration {
	if s != nil && s.werkOpenAIDebounce > 0 {
		return s.werkOpenAIDebounce
	}
	return werkOpenAIDefaultDebounce
}

func (s *planUsageStore) resetWerkOpenAILocked() {
	s.werkOpenAIClientID = ""
	s.werkOpenAIProbe = nil
	s.werkOpenAILastProbe = time.Time{}
}

// matchesCodexAndWerkOpenAI returns true only on exact workspace/account ID
// match via process-salted HMAC match tokens (1776=a, 1810). Never merges by
// email or sub alone.
func matchesCodexAndWerkOpenAI(codexLogin bool, codexMatchToken string, werkID wscred.OpenAIUsageIdentity) bool {
	return codexLogin &&
		codexMatchToken != "" &&
		werkID.Configured &&
		werkID.MatchToken != "" &&
		codexMatchToken == werkID.MatchToken
}

// refreshWerkOpenAI runs a single conservative wham/usage probe for the active
// Werk OpenAI SIWC account when it is NOT unified with Codex CLI (1812: matched
// accounts run strictly one wham/usage probe via Codex). Respects
// background_consent, missing claim, plan pause, and 10m debounce.
func (s *planUsageStore) refreshWerkOpenAI() {
	store := s.openAIStore()
	if store == nil {
		return
	}
	_, codexLogin, codexMatch := s.detectWithCodexMatch()
	id := store.ActiveOpenAIUsageIdentity()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if !id.Configured {
		s.resetWerkOpenAILocked()
		s.mu.Unlock()
		return
	}
	if id.ClientID != s.werkOpenAIClientID {
		s.resetWerkOpenAILocked()
		s.werkOpenAIClientID = id.ClientID
	}
	// Unified Codex/OpenAI cards reuse the single Codex wham/usage probe (1812).
	if matchesCodexAndWerkOpenAI(codexLogin, codexMatch, id) {
		s.mu.Unlock()
		return
	}
	// Local fallback gates: never issue network calls when plan is disabled,
	// background_consent is not granted, plan is paused, or claim is missing.
	if !id.PlanEnabled || !id.BackgroundConsent || id.PlanPaused || !id.HasAccountClaim {
		s.werkOpenAIProbe = nil
		s.mu.Unlock()
		return
	}
	now := s.nowTime()
	if s.werkOpenAIProbing {
		s.mu.Unlock()
		return
	}
	if !s.werkOpenAILastProbe.IsZero() && now.Sub(s.werkOpenAILastProbe) < s.werkOpenAIDebounceInterval() {
		s.mu.Unlock()
		return
	}
	parentCtx := s.lifetimeCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	clientID := id.ClientID
	s.werkOpenAIProbing = true
	s.probes.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.probes.Done()
		ctx, cancel := context.WithTimeout(parentCtx, 15*time.Second)
		defer cancel()
		res, err := store.ProbeOpenAIPlanUsage(ctx)
		latest := store.ActiveOpenAIUsageIdentity()

		s.mu.Lock()
		defer s.mu.Unlock()
		s.werkOpenAIProbing = false
		if s.closed || !latest.Configured || latest.ClientID != clientID || s.werkOpenAIClientID != clientID {
			return
		}
		s.werkOpenAILastProbe = s.nowTime()
		if err != nil && !errors.Is(err, wscred.ErrOpenAIPlanPaused) && res.FallbackReason == "" {
			res.FallbackReason = "unavailable"
		}
		cp := res
		s.werkOpenAIProbe = &cp
	}()
}

// Retained name check: display label for the unified /rail/usage card
// (TestPlanUsageCodexOpenAIExactWorkspaceMatchUnifiesAndDeduplicatesProbe).
func (s *planUsageStore) unifiedCodexOpenAIEntryLocked(now int64, id wscred.OpenAIUsageIdentity) planUsageEntry {
	x := currentUsage(s.gpt, now)
	x.Harness = codexOpenAIUnifiedHarness
	x.Label = "Codex / OpenAI"
	x.AccountLabel = id.MaskedLabel
	models, totalIn, totalOut, totalCost, latestModelAt := s.werkModelRowsLocked(string(wscred.ProviderOpenAI), true, id.ClientID, true)
	if latestModelAt > x.AsOf {
		x.AsOf = latestModelAt
	}
	mode := "local"
	if len(x.Windows) > 0 {
		mode = "remote"
	}
	x.Werk = &werkUsageView{
		Unified:           true,
		Mode:              mode,
		PlanPaused:        id.PlanPaused,
		TokenFirst:        true,
		TotalInputTokens:  totalIn,
		TotalOutputTokens: totalOut,
		EstimatedCostUSD:  totalCost,
		Models:            models,
	}
	return x
}

func (s *planUsageStore) werkOpenAIEntryLocked(now int64, id wscred.OpenAIUsageIdentity) planUsageEntry {
	if id.ClientID != s.werkOpenAIClientID {
		s.resetWerkOpenAILocked()
		s.werkOpenAIClientID = id.ClientID
	}
	models, totalIn, totalOut, totalCost, latestModelAt := s.werkModelRowsLocked(string(wscred.ProviderOpenAI), true, id.ClientID, true)
	view := &werkUsageView{
		Mode:              "local",
		PlanPaused:        id.PlanPaused,
		TokenFirst:        true,
		TotalInputTokens:  totalIn,
		TotalOutputTokens: totalOut,
		EstimatedCostUSD:  totalCost,
		Models:            models,
	}
	entry := planUsageEntry{
		Harness:      werkOpenAIHarness,
		AccountLabel: id.MaskedLabel,
		Source:       werkOpenAIUsageSource,
		AsOf:         latestModelAt,
		Windows:      []planUsageWindow{},
		Werk:         view,
	}

	switch {
	case !id.PlanEnabled:
		view.FallbackReason = "plan_disabled"
	case id.PlanPaused:
		view.FallbackReason = "plan_paused"
	case !id.BackgroundConsent:
		view.FallbackReason = "consent_required"
	case !id.HasAccountClaim:
		view.FallbackReason = "missing_claim"
	case s.werkOpenAIProbe != nil && s.werkOpenAIProbe.Supported:
		view.Mode = "remote"
		entry.Plan = s.werkOpenAIProbe.Plan
		if s.werkOpenAIProbe.AsOf > entry.AsOf {
			entry.AsOf = s.werkOpenAIProbe.AsOf
		}
		for _, w := range s.werkOpenAIProbe.Windows {
			if validUsage(w.Percent) && (w.ResetsAt == 0 || w.ResetsAt > now) {
				entry.Windows = append(entry.Windows, planUsageWindow{
					Kind:     w.Kind,
					Percent:  w.Percent,
					ResetsAt: w.ResetsAt,
					AsOf:     w.AsOf,
				})
			}
		}
		if len(entry.Windows) == 0 {
			view.Mode = "local"
			view.FallbackReason = "unavailable"
		}
	case s.werkOpenAIProbe != nil && s.werkOpenAIProbe.FallbackReason != "":
		view.FallbackReason = s.werkOpenAIProbe.FallbackReason
		if s.werkOpenAIProbe.AsOf > entry.AsOf {
			entry.AsOf = s.werkOpenAIProbe.AsOf
		}
	}
	return entry
}
