package wscred

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func jsonResponse(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestProbeOpenRouterUsageHybridCreditsAndKey(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	var seen []string
	s.openRouterTransport = routerTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+fixtureKey {
			t.Fatalf("missing bearer token")
		}
		seen = append(seen, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/key":
			return jsonResponse(http.StatusOK, `{"data":{"label":"sk-or-secret-leak","limit":50,"limit_remaining":37.5,"usage":12.5,"usage_daily":1.25,"usage_weekly":4.5,"usage_monthly":10.75,"is_free_tier":false}}`), nil
		case "/api/v1/credits":
			return jsonResponse(http.StatusOK, `{"data":{"total_credits":100,"total_usage":23.45}}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})

	snap, err := s.ProbeOpenRouterUsage(context.Background())
	if err != nil {
		t.Fatalf("ProbeOpenRouterUsage: %v", err)
	}
	if len(seen) != 2 || seen[0] != "/api/v1/key" || seen[1] != "/api/v1/credits" {
		t.Fatalf("unexpected request order: %v", seen)
	}
	if snap.Key == nil || snap.Credits == nil || snap.CreditsRestricted {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if *snap.Key.Limit != 50 || *snap.Key.LimitRemaining != 37.5 || *snap.Key.UsageDaily != 1.25 || *snap.Key.UsageWeekly != 4.5 || *snap.Key.UsageMonthly != 10.75 {
		t.Fatalf("unexpected key limits: %+v", snap.Key)
	}
	if snap.Credits.TotalCredits != 100 || snap.Credits.TotalUsage != 23.45 {
		t.Fatalf("unexpected credits: %+v", snap.Credits)
	}
	raw, _ := json.Marshal(snap)
	if strings.Contains(string(raw), fixtureKey) || strings.Contains(string(raw), "sk-or-secret-leak") {
		t.Fatalf("secret or label leaked in snapshot: %s", raw)
	}
}

func TestProbeOpenRouterUsageCredits401And403NeverRejectsValidKey(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			s := testStore(t)
			s.openRouterTransport = routerTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/api/v1/key":
					return jsonResponse(http.StatusOK, `{"data":{"usage":5.2,"usage_daily":0.5,"usage_weekly":2.1,"usage_monthly":5.2,"limit":null,"limit_remaining":null}}`), nil
				case "/api/v1/credits":
					return jsonResponse(code, `{"error":"management key required"}`), nil
				default:
					return jsonResponse(http.StatusNotFound, `{}`), nil
				}
			})
			// First verify the key via checkOpenRouter (anchored on /api/v1/key 2xx).
			res, err := s.VerifyOpenRouterKey(context.Background(), NewSecret(fixtureKey))
			if err != nil || res.Status.Verification == nil || res.Status.Verification.State != "verified" {
				t.Fatalf("VerifyOpenRouterKey: %+v %v", res, err)
			}

			snap, err := s.ProbeOpenRouterUsage(context.Background())
			if err != nil {
				t.Fatalf("credits %d must not fail valid key probe: %v", code, err)
			}
			if !snap.CreditsRestricted || snap.Credits != nil || snap.CreditsStatus != code {
				t.Fatalf("expected CreditsRestricted without credits: %+v", snap)
			}
			if snap.Key == nil || snap.Key.UsageDaily == nil || *snap.Key.UsageDaily != 0.5 || snap.Key.Limit != nil {
				t.Fatalf("expected /key fallback metrics: %+v", snap.Key)
			}
			// Verification status in the store must remain verified, never rejected.
			configured, _, rejected, _ := s.OpenRouterUsageMeta()
			if !configured || rejected {
				t.Fatalf("store verification must remain verified after /credits %d", code)
			}
		})
	}
}

func TestProbeOpenRouterUsageKeyRejectedSkipsCredits(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	creditsCalled := false
	s.openRouterTransport = routerTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/credits" {
			creditsCalled = true
		}
		return jsonResponse(http.StatusUnauthorized, `{"error":"invalid"}`), nil
	})
	snap, err := s.ProbeOpenRouterUsage(context.Background())
	if !errors.Is(err, ErrOpenRouterKeyRejected) {
		t.Fatalf("expected ErrOpenRouterKeyRejected, got %v", err)
	}
	if snap.KeyStatus != http.StatusUnauthorized || creditsCalled {
		t.Fatalf("unexpected rejection behavior: snap=%+v creditsCalled=%v", snap, creditsCalled)
	}
}

func TestProbeOpenRouterUsageUnconfigured(t *testing.T) {
	s := testStore(t)
	_, err := s.ProbeOpenRouterUsage(context.Background())
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}
