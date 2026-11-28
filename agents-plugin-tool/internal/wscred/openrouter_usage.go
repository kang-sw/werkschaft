package wscred

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"time"
)

const (
	openRouterKeyURL     = "https://openrouter.ai/api/v1/key"
	openRouterCreditsURL = "https://openrouter.ai/api/v1/credits"
	openRouterMaxBytes   = 256 << 10
)

// OpenRouterKeyLimits holds safe per-key spend and limit figures returned by
// GET https://openrouter.ai/api/v1/key.
type OpenRouterKeyLimits struct {
	Limit          *float64 `json:"limit,omitempty"`
	LimitRemaining *float64 `json:"limit_remaining,omitempty"`
	Usage          *float64 `json:"usage,omitempty"`
	UsageDaily     *float64 `json:"usage_daily,omitempty"`
	UsageWeekly    *float64 `json:"usage_weekly,omitempty"`
	UsageMonthly   *float64 `json:"usage_monthly,omitempty"`
	IsFreeTier     bool     `json:"is_free_tier,omitempty"`
}

// OpenRouterCredits holds account-level credit figures returned by
// GET https://openrouter.ai/api/v1/credits when permitted.
type OpenRouterCredits struct {
	TotalCredits float64 `json:"total_credits"`
	TotalUsage   float64 `json:"total_usage"`
}

// OpenRouterUsageSnapshot is the combined safe result of probing OpenRouter's
// /api/v1/key and /api/v1/credits endpoints. No API key or raw upstream body
// ever leaves wscred.
type OpenRouterUsageSnapshot struct {
	Key               *OpenRouterKeyLimits `json:"key,omitempty"`
	Credits           *OpenRouterCredits   `json:"credits,omitempty"`
	KeyStatus         int                  `json:"key_status,omitempty"`
	CreditsStatus     int                  `json:"credits_status,omitempty"`
	CreditsRestricted bool                 `json:"credits_restricted,omitempty"`
	AsOf              int64                `json:"as_of,omitempty"`
}

// OpenRouterUsageMeta returns non-secret OpenRouter configuration metadata from
// memory (zero disk I/O after the store's initial load).
func (s *Store) OpenRouterUsageMeta() (configured bool, masked string, rejected bool, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadLocked(false) != nil {
		return false, "", false, s.openRouterGeneration
	}
	entry, ok := s.keys[ProviderOpenRouter]
	if !ok {
		return false, "", false, s.openRouterGeneration
	}
	p, _ := s.lookupProvider(ProviderOpenRouter)
	v := entry.verificationStatus()
	return true, Mask(p, entry.APIKey), v.State == "rejected", s.openRouterGeneration
}

// ProbeOpenRouterUsage queries /api/v1/key and, when /key returns 2xx, also
// queries /api/v1/credits. A /credits 401/403 never marks the key rejected and
// never returns ErrOpenRouterKeyRejected: key validity remains anchored on
// /api/v1/key 2xx.
func (s *Store) ProbeOpenRouterUsage(ctx context.Context) (OpenRouterUsageSnapshot, error) {
	s.mu.Lock()
	if err := s.loadLocked(false); err != nil {
		s.mu.Unlock()
		return OpenRouterUsageSnapshot{}, err
	}
	entry, ok := s.keys[ProviderOpenRouter]
	transport := s.openRouterTransport
	nowFn := s.now
	s.mu.Unlock()
	if !ok {
		return OpenRouterUsageSnapshot{}, ErrNotConfigured
	}
	if transport == nil {
		transport = openRouterHTTPTransport
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	snap := OpenRouterUsageSnapshot{AsOf: nowFn().UnixMilli()}

	keyStatus, keyRaw, err := fetchOpenRouterJSON(ctx, client, openRouterKeyURL, entry.APIKey)
	snap.KeyStatus = keyStatus
	if err != nil {
		return snap, err
	}
	if keyStatus == http.StatusUnauthorized || keyStatus == http.StatusForbidden {
		return snap, ErrOpenRouterKeyRejected
	}
	if keyStatus < 200 || keyStatus >= 300 {
		return snap, errors.New("OpenRouter key usage unavailable")
	}
	if parsed, ok := parseOpenRouterKeyLimits(keyRaw); ok {
		snap.Key = parsed
	}

	creditsStatus, creditsRaw, creditsErr := fetchOpenRouterJSON(ctx, client, openRouterCreditsURL, entry.APIKey)
	snap.CreditsStatus = creditsStatus
	if creditsErr == nil {
		switch {
		case creditsStatus >= 200 && creditsStatus < 300:
			if parsed, ok := parseOpenRouterCredits(creditsRaw); ok {
				snap.Credits = parsed
			}
		case creditsStatus == http.StatusUnauthorized || creditsStatus == http.StatusForbidden:
			// Non-management inference keys commonly receive 401/403 from /credits.
			// Never reject or fail the probe when /key already returned 2xx.
			snap.CreditsRestricted = true
		}
	}
	return snap, nil
}

func fetchOpenRouterJSON(ctx context.Context, client *http.Client, endpoint, apiKey string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, errors.New("invalid OpenRouter endpoint")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, errors.New("could not reach OpenRouter")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, openRouterMaxBytes+1))
	if err != nil || len(raw) > openRouterMaxBytes {
		return resp.StatusCode, nil, errors.New("OpenRouter returned an unreadable response")
	}
	return resp.StatusCode, raw, nil
}

func sanitizeNonNegFloat(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 {
		return nil
	}
	cp := *v
	return &cp
}

func sanitizeFiniteFloat(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return nil
	}
	cp := *v
	return &cp
}

func parseOpenRouterKeyLimits(raw []byte) (*OpenRouterKeyLimits, bool) {
	var envelope struct {
		Data *struct {
			Limit          *float64 `json:"limit"`
			LimitRemaining *float64 `json:"limit_remaining"`
			Usage          *float64 `json:"usage"`
			UsageDaily     *float64 `json:"usage_daily"`
			UsageWeekly    *float64 `json:"usage_weekly"`
			UsageMonthly   *float64 `json:"usage_monthly"`
			IsFreeTier     bool     `json:"is_free_tier"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Data == nil {
		return nil, false
	}
	d := envelope.Data
	return &OpenRouterKeyLimits{
		Limit:          sanitizeNonNegFloat(d.Limit),
		LimitRemaining: sanitizeFiniteFloat(d.LimitRemaining),
		Usage:          sanitizeNonNegFloat(d.Usage),
		UsageDaily:     sanitizeNonNegFloat(d.UsageDaily),
		UsageWeekly:    sanitizeNonNegFloat(d.UsageWeekly),
		UsageMonthly:   sanitizeNonNegFloat(d.UsageMonthly),
		IsFreeTier:     d.IsFreeTier,
	}, true
}

func parseOpenRouterCredits(raw []byte) (*OpenRouterCredits, bool) {
	var envelope struct {
		Data *struct {
			TotalCredits *float64 `json:"total_credits"`
			TotalUsage   *float64 `json:"total_usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Data == nil {
		return nil, false
	}
	tc := sanitizeNonNegFloat(envelope.Data.TotalCredits)
	tu := sanitizeNonNegFloat(envelope.Data.TotalUsage)
	if tc == nil || tu == nil {
		return nil, false
	}
	return &OpenRouterCredits{
		TotalCredits: *tc,
		TotalUsage:   *tu,
	}, true
}
