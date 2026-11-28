package wscred

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const defaultOpenAIPlanUsageURL = "https://chatgpt.com/backend-api/wham/usage"

var (
	accountMatchSaltOnce sync.Once
	accountMatchSalt     [32]byte
)

func initAccountMatchSalt() {
	accountMatchSaltOnce.Do(func() {
		if _, err := io.ReadFull(rand.Reader, accountMatchSalt[:]); err != nil {
			// Fallback to time-derived entropy only if crypto/rand fails.
			sum := sha256.Sum256([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
			accountMatchSalt = sum
		}
	})
}

// AccountMatchToken returns an in-process salted one-way HMAC-SHA256 hex token
// for comparing workspace/account IDs in memory without exposing raw IDs across
// package boundaries, logs, wsmeta, or /rail/usage (1810). Returns empty for
// empty input.
func AccountMatchToken(rawAccountID string) string {
	id := strings.TrimSpace(rawAccountID)
	if id == "" {
		return ""
	}
	initAccountMatchSalt()
	mac := hmac.New(sha256.New, accountMatchSalt[:])
	_, _ = mac.Write([]byte(id))
	return hex.EncodeToString(mac.Sum(nil))
}

// MaskOpenAIAccountLabel produces a safe, non-secret display label from an
// email or registration client ID. Raw workspace/account IDs and tokens are
// never accepted or emitted here.
func MaskOpenAIAccountLabel(email, clientID string) string {
	email = strings.TrimSpace(email)
	if at := strings.IndexByte(email, '@'); at > 0 && at < len(email)-1 {
		local, domain := email[:at], email[at+1:]
		r1, sz1 := utf8.DecodeRuneInString(local)
		if r1 == utf8.RuneError {
			return "***@" + domain
		}
		prefix := string(r1)
		rest := local[sz1:]
		if len(rest) > 1 {
			if r2, _ := utf8.DecodeRuneInString(rest); r2 != utf8.RuneError {
				prefix += string(r2)
			}
		}
		return prefix + "***@" + domain
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ""
	}
	if len(clientID) <= 8 {
		return "***"
	}
	return clientID[:4] + "…" + clientID[len(clientID)-4:]
}

// OpenAIUsageIdentity is the safe, non-secret summary of the active OpenAI SIWC
// account used by the dashboard for Codex CLI workspace-ID matching and card
// rendering. Raw tokens and raw chatgpt_account_id values never leave wscred.
type OpenAIUsageIdentity struct {
	Configured        bool
	ClientID          string
	MaskedLabel       string
	MatchToken        string
	HasAccountClaim   bool
	PlanEnabled       bool
	PlanPaused        bool
	BackgroundConsent bool
}

// ActiveOpenAIUsageIdentity reads the active OpenAI SIWC account summary from
// memory (zero disk I/O after initial store load).
func (s *Store) ActiveOpenAIUsageIdentity() OpenAIUsageIdentity {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadLocked(false) != nil {
		return OpenAIUsageIdentity{}
	}
	acct := s.openAIAccount
	if acct.ClientID == "" || acct.IDToken == "" {
		return OpenAIUsageIdentity{}
	}
	rawID := acct.resolvedAccountID()
	return OpenAIUsageIdentity{
		Configured:        true,
		ClientID:          acct.ClientID,
		MaskedLabel:       MaskOpenAIAccountLabel(acct.Email, acct.ClientID),
		MatchToken:        AccountMatchToken(rawID),
		HasAccountClaim:   rawID != "",
		PlanEnabled:       acct.planEnabled(),
		PlanPaused:        s.openAIPlanPauses[acct.ClientID] != nil,
		BackgroundConsent: acct.Consent != nil && *acct.Consent,
	}
}

// OpenAIPlanUsageWindow is one quota window returned by the conservative
// wham/usage probe.
type OpenAIPlanUsageWindow struct {
	Kind     string  `json:"kind"`
	Percent  float64 `json:"percent"`
	ResetsAt int64   `json:"resets_at,omitempty"`
	AsOf     int64   `json:"as_of"`
}

// OpenAIPlanUsageResult is the safe outcome of probing wham/usage with the
// active OpenAI SIWC account. When Supported is false, callers fall back to
// local per-model aggregation and openai_plan.go pause state.
type OpenAIPlanUsageResult struct {
	Supported      bool                    `json:"supported"`
	FallbackReason string                  `json:"fallback_reason,omitempty"`
	Plan           string                  `json:"plan,omitempty"`
	PlanPaused     bool                    `json:"plan_paused,omitempty"`
	HTTPStatus     int                     `json:"http_status,omitempty"`
	AsOf           int64                   `json:"as_of,omitempty"`
	Windows        []OpenAIPlanUsageWindow `json:"windows,omitempty"`
}

// ProbeOpenAIPlanUsage runs a conservative wham/usage probe inside wscred so
// raw SIWC tokens and raw chatgpt_account_id values never cross package
// boundaries. It refuses to make any network request when background_consent is
// absent, the plan is paused (ErrOpenAIPlanPaused), the plan scope is disabled,
// or the chatgpt_account_id claim is missing (1811). A 401/403 response triggers
// automatic local fallback without revoking or mutating saved credentials.
func (s *Store) ProbeOpenAIPlanUsage(ctx context.Context) (OpenAIPlanUsageResult, error) {
	s.mu.Lock()
	if err := s.loadLocked(false); err != nil {
		s.mu.Unlock()
		return OpenAIPlanUsageResult{FallbackReason: "unavailable"}, err
	}
	acct := s.openAIAccount
	nowFn := s.now
	usageURL := s.openAIUsageURL
	if usageURL == "" {
		usageURL = defaultOpenAIPlanUsageURL
	}
	paused := acct.ClientID != "" && s.openAIPlanPauses[acct.ClientID] != nil
	s.mu.Unlock()

	nowMs := nowFn().UnixMilli()
	if acct.ClientID == "" || acct.IDToken == "" {
		return OpenAIPlanUsageResult{FallbackReason: "not_configured", AsOf: nowMs}, nil
	}
	if !acct.planEnabled() {
		return OpenAIPlanUsageResult{FallbackReason: "plan_disabled", AsOf: nowMs}, nil
	}
	if acct.Consent == nil || !*acct.Consent {
		return OpenAIPlanUsageResult{FallbackReason: "consent_required", AsOf: nowMs}, nil
	}
	if paused {
		return OpenAIPlanUsageResult{FallbackReason: "plan_paused", PlanPaused: true, AsOf: nowMs}, ErrOpenAIPlanPaused
	}
	rawID := acct.resolvedAccountID()
	if rawID == "" {
		return OpenAIPlanUsageResult{FallbackReason: "missing_claim", AsOf: nowMs}, nil
	}

	cred, err := s.openAICredential(ctx, Request{
		Provider:      ProviderOpenAI,
		AgentSession:  true,
		Background:    true,
		OpenAIAccount: acct.ClientID,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrConsentRequired):
			return OpenAIPlanUsageResult{FallbackReason: "consent_required", AsOf: nowMs}, nil
		case errors.Is(err, ErrOpenAIPlanDisabled):
			return OpenAIPlanUsageResult{FallbackReason: "plan_disabled", AsOf: nowMs}, nil
		case errors.Is(err, ErrNotConfigured):
			return OpenAIPlanUsageResult{FallbackReason: "not_configured", AsOf: nowMs}, nil
		default:
			return OpenAIPlanUsageResult{FallbackReason: "unavailable", AsOf: nowMs}, err
		}
	}

	bearer := cred.Secret.Reveal()
	if !validHTTPHeaderValue("Bearer "+bearer) || !validHTTPHeaderValue(rawID) {
		return OpenAIPlanUsageResult{FallbackReason: "unavailable", AsOf: nowMs}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return OpenAIPlanUsageResult{FallbackReason: "unavailable", AsOf: nowMs}, nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("ChatGPT-Account-Id", rawID)
	req.Header.Set("User-Agent", "ws-dashboard")

	resp, err := s.openAI.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return OpenAIPlanUsageResult{FallbackReason: "unavailable", AsOf: nowMs}, ctx.Err()
		}
		return OpenAIPlanUsageResult{FallbackReason: "unavailable", AsOf: nowMs}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return OpenAIPlanUsageResult{
			FallbackReason: "auth_rejected",
			HTTPStatus:     resp.StatusCode,
			AsOf:           nowMs,
		}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return OpenAIPlanUsageResult{
			FallbackReason: "unavailable",
			HTTPStatus:     resp.StatusCode,
			AsOf:           nowMs,
		}, nil
	}

	var body struct {
		Plan   string `json:"plan_type"`
		Limits struct {
			Primary   *openAIUsageHTTPWindow `json:"primary_window"`
			Secondary *openAIUsageHTTPWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := readOpenAIJSON(resp.Body, &body); err != nil {
		return OpenAIPlanUsageResult{FallbackReason: "unavailable", HTTPStatus: resp.StatusCode, AsOf: nowMs}, nil
	}
	windows := parseOpenAIUsageWindows(body.Limits.Primary, body.Limits.Secondary, nowMs)
	if len(windows) == 0 {
		return OpenAIPlanUsageResult{FallbackReason: "unavailable", HTTPStatus: resp.StatusCode, AsOf: nowMs}, nil
	}
	return OpenAIPlanUsageResult{
		Supported:  true,
		Plan:       strings.TrimSpace(body.Plan),
		HTTPStatus: resp.StatusCode,
		AsOf:       nowMs,
		Windows:    windows,
	}, nil
}

type openAIUsageHTTPWindow struct {
	Percent *float64        `json:"used_percent"`
	Seconds int             `json:"limit_window_seconds"`
	Reset   json.RawMessage `json:"reset_at"`
}

func parseOpenAIUsageWindows(primary, secondary *openAIUsageHTTPWindow, asOf int64) []OpenAIPlanUsageWindow {
	var fiveHour, sevenDay *OpenAIPlanUsageWindow
	for _, w := range []*openAIUsageHTTPWindow{primary, secondary} {
		if w == nil || w.Percent == nil || math.IsNaN(*w.Percent) || math.IsInf(*w.Percent, 0) || *w.Percent < 0 || *w.Percent > 100 {
			continue
		}
		switch w.Seconds {
		case 18000:
			if fiveHour == nil {
				fiveHour = &OpenAIPlanUsageWindow{Kind: "five_hour", Percent: *w.Percent, ResetsAt: parseOpenAIUsageReset(w.Reset), AsOf: asOf}
			}
		case 604800:
			if sevenDay == nil {
				sevenDay = &OpenAIPlanUsageWindow{Kind: "seven_day", Percent: *w.Percent, ResetsAt: parseOpenAIUsageReset(w.Reset), AsOf: asOf}
			}
		}
	}
	var out []OpenAIPlanUsageWindow
	if fiveHour != nil {
		out = append(out, *fiveHour)
	}
	if sevenDay != nil {
		out = append(out, *sevenDay)
	}
	return out
}

func parseOpenAIUsageReset(raw json.RawMessage) int64 {
	text := strings.Trim(string(raw), "\"")
	if n, err := strconv.ParseFloat(text, 64); err == nil && n > 0 && !math.IsInf(n, 0) {
		if n < 1e12 {
			n *= 1000
		}
		return int64(n)
	}
	if t, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return t.UnixMilli()
	}
	return 0
}

func validHTTPHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] == 127 || (value[i] < 32 && value[i] != '\t') {
			return false
		}
	}
	return true
}
