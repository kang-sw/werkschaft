package wscred

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixtureSessionToken = "session-token-fixture"

// tokenServer is a stand-in daemon: it admits fixtureSessionToken and
// answers from src with ServeToken.
func tokenServer(t *testing.T, src Source) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != TokenRoutePath || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+fixtureSessionToken {
			RefuseToken(w, http.StatusUnauthorized, RefusalUnauthorized, "", "unknown session")
			return
		}
		ServeToken(w, r, src, Request{AgentSession: true})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestServeTokenAnswersAndRefuses(t *testing.T) {
	s := testStore(t)
	srv := tokenServer(t, s)
	post := func(body string) (*http.Response, string) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+TokenRoutePath, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+fixtureSessionToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp, string(raw)
	}
	resp, body := post(`{"provider":"openrouter"}`)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, `"error":"not_configured"`) || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unset key = %d %s %v", resp.StatusCode, body, resp.Header)
	}
	setKey(t, s, fixtureKey)
	resp, body = post(`{"provider":"openrouter"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"secret":"`+fixtureKey+`"`) || !strings.Contains(body, `"kind":"api_key"`) || strings.Contains(body, "expires_at") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("set key = %d %s %v", resp.StatusCode, body, resp.Header)
	}
	if resp, body = post(`{"provider":"nope"}`); resp.StatusCode != http.StatusNotFound || !strings.Contains(body, `"error":"unknown_provider"`) {
		t.Fatalf("unknown provider = %d %s", resp.StatusCode, body)
	}
	if resp, body = post(`{}`); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, `"error":"bad_request"`) {
		t.Fatalf("no provider = %d %s", resp.StatusCode, body)
	}
}

// TestHarnessClientObtainsTheKeyThroughTheRoute: the harness-side client
// gets the key from the daemon's route and authenticates a provider request
// with it; a fake provider checks the bearer it receives.
func TestHarnessClientObtainsTheKeyThroughTheRoute(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	daemon := tokenServer(t, s)
	var gotAuth string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(provider.Close)

	client := NewRouteClient(daemon.URL, NewSecret(fixtureSessionToken), daemon.Client())
	cred, err := client.Credential(context.Background(), Request{Provider: ProviderOpenRouter})
	if err != nil || cred.Provider != ProviderOpenRouter || cred.Kind != KindAPIKey {
		t.Fatalf("Credential = %+v, %v", cred, err)
	}
	req, _ := http.NewRequest(http.MethodGet, provider.URL+"/api/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+cred.Secret.Reveal())
	resp, err := provider.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotAuth != "Bearer "+fixtureKey {
		t.Fatalf("provider saw Authorization %q", gotAuth)
	}
}

func TestRouteClientMapsRefusalsToTypedErrors(t *testing.T) {
	s := testStore(t)
	daemon := tokenServer(t, s)
	ctx := context.Background()
	bad := NewRouteClient(daemon.URL, NewSecret("wrong-token"), daemon.Client())
	if _, err := bad.Credential(ctx, Request{Provider: ProviderOpenRouter}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad token = %v", err)
	}
	client := NewRouteClient(daemon.URL, NewSecret(fixtureSessionToken), daemon.Client())
	if _, err := client.Credential(ctx, Request{Provider: ProviderOpenRouter}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unset = %v", err)
	}
	if _, err := client.Credential(ctx, Request{Provider: "nope"}); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("unknown = %v", err)
	}
	malformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RefuseToken(w, http.StatusBadRequest, RefusalBadRequest, "", "the request names no provider")
	}))
	_, err := NewRouteClient(malformed.URL, NewSecret(fixtureSessionToken), malformed.Client()).Credential(ctx, Request{Provider: ProviderOpenRouter})
	malformed.Close()
	if err == nil || errors.Is(err, ErrDaemonUnavailable) || !strings.Contains(err.Error(), "names no provider") {
		t.Fatalf("bad request = %v", err)
	}
	for code, want := range map[string]error{RefusalNotPermitted: ErrNotPermitted, RefusalConsentRequired: ErrConsentRequired} {
		code := code
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			RefuseToken(w, http.StatusForbidden, code, ProviderOpenRouter, "refused")
		}))
		_, err := NewRouteClient(srv.URL, NewSecret(fixtureSessionToken), srv.Client()).Credential(ctx, Request{Provider: ProviderOpenRouter})
		srv.Close()
		if !errors.Is(err, want) {
			t.Fatalf("%s = %v, want %v", code, err, want)
		}
	}
}

// TestRouteClientKeepsTheLastCredentialWhileTheDaemonIsDown: during a daemon
// replacement the harness keeps its last credential and asks again after.
func TestRouteClientKeepsTheLastCredentialWhileTheDaemonIsDown(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	var mu sync.Mutex
	mode := "up"
	setMode := func(m string) { mu.Lock(); mode = m; mu.Unlock() }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		m := mode
		mu.Unlock()
		switch m {
		case "draining":
			RefuseToken(w, http.StatusServiceUnavailable, RefusalUnavailable, "", "draining")
		case "unrestored":
			// A successor daemon that has not restored the session's
			// bearer yet.
			RefuseToken(w, http.StatusUnauthorized, RefusalUnauthorized, "", "unknown session token")
		default:
			ServeToken(w, r, s, Request{AgentSession: true})
		}
	}))
	ctx := context.Background()
	client := NewRouteClient(srv.URL, NewSecret(fixtureSessionToken), srv.Client())
	if _, err := client.Credential(ctx, Request{Provider: ProviderOpenRouter}); err != nil {
		t.Fatal(err)
	}
	setMode("draining")
	if cred, err := client.Credential(ctx, Request{Provider: ProviderOpenRouter}); err != nil || cred.Secret.Reveal() != fixtureKey {
		t.Fatalf("draining daemon = %+v, %v", cred, err)
	}
	setMode("unrestored")
	if cred, err := client.Credential(ctx, Request{Provider: ProviderOpenRouter}); err != nil || cred.Secret.Reveal() != fixtureKey {
		t.Fatalf("successor without the bearer yet = %+v, %v", cred, err)
	}
	// With nothing kept, the same answer is unauthorized.
	if _, err := NewRouteClient(srv.URL, NewSecret(fixtureSessionToken), srv.Client()).Credential(ctx, Request{Provider: ProviderOpenRouter}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unrestored with nothing kept = %v", err)
	}
	setMode("up")
	if cred, err := client.Credential(ctx, Request{Provider: ProviderOpenRouter}); err != nil || cred.Secret.Reveal() != fixtureKey {
		t.Fatalf("after the swap = %+v, %v", cred, err)
	}
	srv.Close()
	if cred, err := client.Credential(ctx, Request{Provider: ProviderOpenRouter}); err != nil || cred.Secret.Reveal() != fixtureKey {
		t.Fatalf("unreachable daemon = %+v, %v", cred, err)
	}
	// An expired short-lived credential is not kept.
	client.mu.Lock()
	client.last[ProviderOpenRouter] = Credential{Provider: ProviderOpenRouter, Secret: NewSecret(fixtureKey), ExpiresAt: time.Now().Add(-time.Minute)}
	client.mu.Unlock()
	if _, err := client.Credential(ctx, Request{Provider: ProviderOpenRouter}); !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("expired fallback = %v", err)
	}
	// A fresh client with nothing to fall back on reports the outage.
	if _, err := NewRouteClient(srv.URL, NewSecret(fixtureSessionToken), nil).Credential(ctx, Request{Provider: ProviderOpenRouter}); !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("no fallback = %v", err)
	}
}

// TestRouteClientForgetsARemovedKey: an authoritative refusal drops the
// kept credential, so a removed key stops working at the next request.
func TestRouteClientForgetsARemovedKey(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	srv := tokenServer(t, s)
	client := NewRouteClient(srv.URL, NewSecret(fixtureSessionToken), srv.Client())
	ctx := context.Background()
	if _, err := client.Credential(ctx, Request{Provider: ProviderOpenRouter}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(Change{Op: OpRemove, Provider: ProviderOpenRouter}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Credential(ctx, Request{Provider: ProviderOpenRouter}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("after remove = %v", err)
	}
	srv.Close()
	if _, err := client.Credential(ctx, Request{Provider: ProviderOpenRouter}); !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("a refused credential was kept: %v", err)
	}
}

func TestDaemonBaseURL(t *testing.T) {
	if got, err := DaemonBaseURL("http://127.0.0.1:29417/mcp/session"); err != nil || got != "http://127.0.0.1:29417" {
		t.Fatalf("DaemonBaseURL = %q, %v", got, err)
	}
	if _, err := DaemonBaseURL("not a url"); err == nil {
		t.Fatal("a non-URL was accepted")
	}
}
