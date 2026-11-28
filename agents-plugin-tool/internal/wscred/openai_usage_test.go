package wscred

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func signFixtureJWT(t *testing.T, f *openAIFixture, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "fixture-key"
	signed, err := tok.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func seedOpenAIAccount(t *testing.T, s *Store, acct openAIAccount) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareWriteLocked(); err != nil {
		t.Fatal(err)
	}
	state := s.doc.openAIState()
	state.Active = acct.ClientID
	if err := s.writeOpenAIAccountLocked(state, acct); err != nil {
		t.Fatal(err)
	}
}

func TestAccountMatchTokenAndMaskLabel(t *testing.T) {
	a := AccountMatchToken("acct-workspace-1")
	b := AccountMatchToken("  acct-workspace-1 ")
	c := AccountMatchToken("acct-workspace-2")
	if a == "" || a != b {
		t.Fatalf("expected deterministic match token for same ID, got %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("distinct workspace IDs must not collide")
	}
	if strings.Contains(a, "acct-workspace-1") {
		t.Fatalf("match token must be one-way")
	}
	if AccountMatchToken("") != "" {
		t.Fatalf("empty ID must produce empty match token")
	}
	if got := MaskOpenAIAccountLabel("alice@example.com", "oaiapp_12345678"); got != "al***@example.com" {
		t.Fatalf("unexpected email mask: %q", got)
	}
	if got := MaskOpenAIAccountLabel("a@example.com", "oaiapp_12345678"); got != "a***@example.com" {
		t.Fatalf("unexpected short email mask: %q", got)
	}
	if got := MaskOpenAIAccountLabel("", "oaiapp_12345678"); got != "oaia…5678" {
		t.Fatalf("unexpected clientID mask: %q", got)
	}
}

func TestVerifyIdentityExtractsChatGPTAccountIDClaim(t *testing.T) {
	s := testStore(t)
	f := newOpenAIFixture(t, s)
	now := f.now
	idTok := signFixtureJWT(t, f, jwt.MapClaims{
		"iss":            f.server.URL,
		"sub":            "user-1",
		"aud":            "client-1",
		"iat":            now.Unix(),
		"exp":            now.Add(time.Hour).Unix(),
		"nonce":          "nonce-1",
		"email":          "alice@example.com",
		"email_verified": true,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-claim-from-id",
		},
	})
	identity, err := s.openAI.verifyIdentity(context.Background(), openAITokens{IDToken: idTok, AccessToken: "opaque-access"}, "client-1", "nonce-1", s.now)
	if err != nil {
		t.Fatalf("verifyIdentity: %v", err)
	}
	if identity.AccountID != "acct-claim-from-id" {
		t.Fatalf("expected acct-claim-from-id, got %q", identity.AccountID)
	}

	// Fallback to access_token JWT claim when id_token omits it.
	idTokNoAuth := signFixtureJWT(t, f, jwt.MapClaims{
		"iss":   f.server.URL,
		"sub":   "user-1",
		"aud":   "client-1",
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
		"nonce": "nonce-2",
	})
	accessTok := signFixtureJWT(t, f, jwt.MapClaims{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-claim-from-access",
		},
	})
	identity2, err := s.openAI.verifyIdentity(context.Background(), openAITokens{IDToken: idTokNoAuth, AccessToken: accessTok}, "client-1", "nonce-2", s.now)
	if err != nil {
		t.Fatalf("verifyIdentity access fallback: %v", err)
	}
	if identity2.AccountID != "acct-claim-from-access" {
		t.Fatalf("expected acct-claim-from-access, got %q", identity2.AccountID)
	}
}

func TestProbeOpenAIPlanUsageSafetyAndFallbacks(t *testing.T) {
	consentTrue := true
	consentFalse := false
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	makeStore := func(t *testing.T, acct openAIAccount, handler http.HandlerFunc) (*Store, *int) {
		t.Helper()
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if handler != nil {
				handler(w, r)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		s := testStore(t)
		s.now = func() time.Time { return now }
		s.openAIUsageURL = srv.URL + "/wham/usage"
		seedOpenAIAccount(t, s, acct)
		return s, &calls
	}

	baseAcct := func() openAIAccount {
		return openAIAccount{
			ClientID:     "oaiapp_active_1",
			Issuer:       openAIIssuer,
			Subject:      "user-1",
			Email:        "alice@example.com",
			AccountID:    "acct-secret-workspace-123",
			IDToken:      "id-token-1",
			AccessToken:  "access-token-1",
			RefreshToken: "refresh-token-1",
			Scopes:       []string{"openid", openAIPlanScope},
			ExpiresAt:    now.Add(time.Hour),
			UpdatedAt:    now,
			Consent:      &consentTrue,
		}
	}

	t.Run("missing claim skips network and falls back", func(t *testing.T) {
		acct := baseAcct()
		acct.AccountID = ""
		s, calls := makeStore(t, acct, nil)
		res, err := s.ProbeOpenAIPlanUsage(context.Background())
		if err != nil || res.Supported || res.FallbackReason != "missing_claim" || *calls != 0 {
			t.Fatalf("unexpected result: res=%+v err=%v calls=%d", res, err, *calls)
		}
	})

	t.Run("background consent false blocks network probe", func(t *testing.T) {
		acct := baseAcct()
		acct.Consent = &consentFalse
		s, calls := makeStore(t, acct, nil)
		res, err := s.ProbeOpenAIPlanUsage(context.Background())
		if err != nil || res.Supported || res.FallbackReason != "consent_required" || *calls != 0 {
			t.Fatalf("unexpected result: res=%+v err=%v calls=%d", res, err, *calls)
		}
	})

	t.Run("plan paused blocks network probe", func(t *testing.T) {
		acct := baseAcct()
		s, calls := makeStore(t, acct, nil)
		s.mu.Lock()
		s.openAIPlanPauses = map[string]*openAIPlanPause{acct.ClientID: {}}
		s.mu.Unlock()
		res, err := s.ProbeOpenAIPlanUsage(context.Background())
		if !errors.Is(err, ErrOpenAIPlanPaused) || res.Supported || !res.PlanPaused || res.FallbackReason != "plan_paused" || *calls != 0 {
			t.Fatalf("unexpected result: res=%+v err=%v calls=%d", res, err, *calls)
		}
	})

	t.Run("401 and 403 fall back cleanly without revoking credentials", func(t *testing.T) {
		for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			acct := baseAcct()
			s, calls := makeStore(t, acct, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("ChatGPT-Account-Id") != acct.AccountID || r.Header.Get("Authorization") != "Bearer "+acct.AccessToken {
					t.Errorf("wrong auth headers")
				}
				w.WriteHeader(code)
			})
			res, err := s.ProbeOpenAIPlanUsage(context.Background())
			if err != nil || res.Supported || res.FallbackReason != "auth_rejected" || res.HTTPStatus != code || *calls != 1 {
				t.Fatalf("code=%d unexpected result: res=%+v err=%v calls=%d", code, res, err, *calls)
			}
			// Account credentials must remain intact.
			id := s.ActiveOpenAIUsageIdentity()
			if !id.Configured || !id.PlanEnabled || !id.HasAccountClaim {
				t.Fatalf("credentials altered after %d fallback: %+v", code, id)
			}
		}
	})

	t.Run("200 parses windows and never exposes raw account ID", func(t *testing.T) {
		acct := baseAcct()
		reset5h := now.Add(3 * time.Hour).Unix()
		reset7d := now.Add(5 * 24 * time.Hour).Unix()
		s, calls := makeStore(t, acct, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plan_type": "plus",
				"rate_limit": map[string]any{
					"primary_window":   map[string]any{"used_percent": 32.5, "limit_window_seconds": 18000, "reset_at": reset5h},
					"secondary_window": map[string]any{"used_percent": 58.0, "limit_window_seconds": 604800, "reset_at": reset7d},
				},
			})
		})
		res, err := s.ProbeOpenAIPlanUsage(context.Background())
		if err != nil || !res.Supported || res.Plan != "plus" || len(res.Windows) != 2 || *calls != 1 {
			t.Fatalf("unexpected success result: res=%+v err=%v calls=%d", res, err, *calls)
		}
		if res.Windows[0].Kind != "five_hour" || res.Windows[0].Percent != 32.5 || res.Windows[1].Kind != "seven_day" || res.Windows[1].Percent != 58.0 {
			t.Fatalf("unexpected windows: %+v", res.Windows)
		}
		id := s.ActiveOpenAIUsageIdentity()
		rawIDJSON, _ := json.Marshal(id)
		rawResJSON, _ := json.Marshal(res)
		for _, blob := range []string{string(rawIDJSON), string(rawResJSON)} {
			if strings.Contains(blob, acct.AccountID) || strings.Contains(blob, acct.AccessToken) || strings.Contains(blob, acct.RefreshToken) {
				t.Fatalf("raw Type 5 secret leaked: %s", blob)
			}
		}
	})
}
