package wscred

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type openAIFixture struct {
	t      *testing.T
	server *httptest.Server
	key    *rsa.PrivateKey
	now    time.Time

	// Browser and token requests are ordered by the flow. The lock also
	// lets the tests check rotation across concurrent credential requests.
	mu          sync.Mutex
	query       url.Values
	clientID    string
	callbackID  string
	hostID      string
	code        string
	access      string
	refresh     string
	priorID     string
	badIdentity string
	retryCode   bool
	revoke      bool
	temporary   bool
	deny        bool
	noPlan      bool
	authorizes  int
	grants      int
	refreshes   int
}

func newOpenAIFixture(t *testing.T, s *Store) *openAIFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &openAIFixture{t: t, key: key, now: s.now()}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	s.openAI.issuer, s.openAI.api = f.server.URL, f.server.URL+"/v1"
	s.now = func() time.Time { return f.now }
	return f
}

func (f *openAIFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/api/accounts/authorize":
		f.authorize(w, r)
	case "/api/accounts/oauth/token":
		f.grant(w, r)
	case "/.well-known/jwks.json":
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "fixture-key", "kty": "RSA", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()), "e": "AQAB",
		}}})
	case "/v1/models":
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+f.access {
			f.t.Error("catalog request did not use the current account bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"slug":"model-z","display_name":"Model Z","visibility":"list"},{"slug":"internal","display_name":"Internal","visibility":"hidden"},{"slug":"model-a","display_name":"Model A","visibility":"list"}]}`))
	default:
		http.NotFound(w, r)
	}
}

func (f *openAIFixture) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.query = q
	f.authorizes++
	if q.Get("ext_agent_host_id") == "" || (f.hostID != "" && f.hostID != q.Get("ext_agent_host_id")) ||
		q.Get("response_type") != "code" || q.Get("scope") != openAIScopes || q.Get("resource") != openAIResource ||
		q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge_method") != "S256" {
		f.t.Error("invalid authorization parameters")
	}
	f.hostID = q.Get("ext_agent_host_id")
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" || redirect.Path != openAICallback {
		f.t.Error("invalid callback URI")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.code = tokenRandom()
	callback := url.Values{"state": {q.Get("state")}, "code": {f.code}, "scope": {openAIScopes}}
	if q.Get("client_id") == dynamicClientID {
		f.clientID = "oaiapp_" + tokenRandom()
		callback.Set("client_id", f.clientID)
		if q.Get("agent_name_hint") != "ws" || q.Has("id_token_hint") {
			f.t.Error("invalid dynamic registration metadata")
		}
	} else {
		f.clientID = q.Get("client_id")
		if q.Has("agent_name_hint") || (f.priorID != "" && q.Get("id_token_hint") != f.priorID) {
			f.t.Error("invalid returning sign-in metadata")
		}
		if f.callbackID != "" {
			callback.Set("client_id", f.callbackID)
		}
	}
	if f.deny {
		callback.Set("error", "access_denied")
	}
	// An unrelated navigation cannot consume the actual callback.
	redirect.RawQuery = "state=wrong"
	resp, err := http.Get(redirect.String())
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		f.t.Error("callback accepted a different state")
	}
	if resp != nil {
		resp.Body.Close()
	}
	redirect.RawQuery = callback.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (f *openAIFixture) grant(w http.ResponseWriter, r *http.Request) {
	f.grants++
	if r.ParseForm() != nil || r.Method != http.MethodPost || r.PostForm.Get("resource") != openAIResource ||
		r.PostForm.Get("client_id") != f.clientID || r.PostForm.Has("client_secret") || r.Header.Get("Authorization") != "" {
		f.t.Error("invalid OAuth token request")
	}
	var idToken string
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		verifier := r.PostForm.Get("code_verifier")
		challenge := sha256.Sum256([]byte(verifier))
		if len(verifier) < 43 || len(verifier) > 128 || r.PostForm.Get("code") != f.code ||
			r.PostForm.Get("redirect_uri") != f.query.Get("redirect_uri") ||
			base64.RawURLEncoding.EncodeToString(challenge[:]) != f.query.Get("code_challenge") {
			f.t.Error("invalid code exchange binding")
		}
		if f.retryCode {
			f.retryCode = false
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		idToken = f.identityToken()
		f.priorID = idToken
	case "refresh_token":
		f.refreshes++
		if r.PostForm.Get("refresh_token") != f.refresh || r.PostForm.Has("scope") {
			f.t.Error("refresh did not use the last rotation or attempted to rescope")
		}
		if f.temporary {
			w.WriteHeader(http.StatusServiceUnavailable)
			// Even an OAuth-looking upstream infrastructure response is
			// not evidence that the renewable session is invalid.
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": f.refresh})
			return
		}
		if f.revoke {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "refresh_token_invalidated", "error_description": f.refresh})
			return
		}
	default:
		f.t.Error("unexpected OAuth grant type")
	}
	f.access, f.refresh = tokenRandom(), tokenRandom()
	scope := openAIScopes
	if f.noPlan {
		scope = "openid profile email"
	}
	_ = json.NewEncoder(w).Encode(openAITokens{AccessToken: f.access, RefreshToken: f.refresh, IDToken: idToken,
		ExpiresIn: 3600, Scope: scope, TokenType: "Bearer"})
}

func (f *openAIFixture) identityToken() string {
	claims := openAIIdentity{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: f.server.URL, Subject: "fixture-subject", Audience: jwt.ClaimStrings{f.clientID},
			ExpiresAt: jwt.NewNumericDate(f.now.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(f.now),
		},
		Nonce: f.query.Get("nonce"), Email: "owner@example.test", EmailVerified: true,
	}
	keyID := "fixture-key"
	switch f.badIdentity {
	case "issuer":
		claims.Issuer = "https://example.test"
	case "audience":
		claims.Audience = jwt.ClaimStrings{"different-client"}
	case "expiration":
		claims.ExpiresAt = jwt.NewNumericDate(f.now.Add(-time.Minute))
	case "nonce":
		claims.Nonce = "different-nonce"
	case "substitution":
		claims.Subject = "different-subject"
	case "signature":
		keyID = "different-key"
	}
	id := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	id.Header["kid"] = keyID
	raw, err := id.SignedString(f.key)
	if err != nil {
		f.t.Error("could not sign fixture identity")
	}
	if f.badIdentity == "invalid-signature" {
		// Keep the published kid and alter the first byte of the signed value.
		signature := strings.LastIndexByte(raw, '.') + 1
		replacement := "A"
		if raw[signature] == 'A' {
			replacement = "B"
		}
		raw = raw[:signature] + replacement + raw[signature+1:]
	}
	return raw
}

func (f *openAIFixture) browser(entry string) error {
	if strings.Contains(entry, "id_token_hint") || strings.Contains(entry, "auth.openai.com") {
		f.t.Error("the browser launcher received a nonlocal URL or an ID token")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(entry)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

func signInFixture(t *testing.T, s *Store, f *openAIFixture, selection OpenAISignIn) {
	t.Helper()
	if err := s.SignInOpenAI(context.Background(), selection, f.browser); err != nil {
		t.Fatal(err)
	}
}

func openAIRequest(background bool) Request {
	return Request{Provider: ProviderOpenAI, AgentSession: true, Background: background}
}

func TestOpenAIRegistrationConsentCatalogAndReauthorization(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	f := newOpenAIFixture(t, s)
	signInFixture(t, s, f, OpenAISignIn{})
	firstClient := f.clientID

	if _, err := s.Credential(ctx, Request{Provider: ProviderOpenAI}); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("non-agent credential request: %v", err)
	}
	if _, err := s.OpenAIModels(ctx, openAIRequest(true)); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("background catalog before consent: %v", err)
	}
	models, err := s.OpenAIModels(ctx, openAIRequest(false))
	if err != nil || len(models) != 2 || models[0] != (OpenAIModel{Slug: "model-z", DisplayName: "Model Z"}) || models[1].Slug != "model-a" {
		t.Fatalf("catalog did not preserve visible model order: %v", err)
	}
	if err := s.SetOpenAIBackgroundConsent(firstClient, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Credential(ctx, openAIRequest(true)); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("background credential after denial: %v", err)
	}
	if err := s.SetOpenAIBackgroundConsent(firstClient, true); err != nil {
		t.Fatal(err)
	}

	reopened := NewStore(s.Path())
	reopened.now, reopened.openAI = s.now, s.openAI
	if credential, err := reopened.Credential(ctx, openAIRequest(true)); err != nil || credential.Secret.Reveal() != f.access {
		t.Fatalf("persisted background consent or credential unavailable: %v", err)
	}
	signInFixture(t, reopened, f, OpenAISignIn{})
	if f.clientID != firstClient || f.query.Has("prompt") || f.query.Get("client_id") == dynamicClientID {
		t.Fatal("routine reauthorization did not reuse the registration")
	}
	if err := reopened.SetOpenAIBackgroundConsent(firstClient, false); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Credential(ctx, openAIRequest(true)); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("background credential after revocation: %v", err)
	}
	status, err := reopened.Status()
	if err != nil || len(status[1].OpenAIAccounts) != 1 || status[1].OpenAIAccounts[0].BackgroundConsent == nil ||
		*status[1].OpenAIAccounts[0].BackgroundConsent || !status[1].Configured {
		t.Fatal("credential status lost the persisted refusal")
	}
	raw, _ := json.Marshal(status)
	for _, private := range []string{f.access, f.refresh, f.priorID} {
		if strings.Contains(string(raw), private) {
			t.Fatal("account status exposed credential material")
		}
	}
}

func TestOpenAIAccountsAndPlanRefusal(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	f := newOpenAIFixture(t, s)
	signInFixture(t, s, f, OpenAISignIn{})
	first := f.clientID
	firstIDToken := f.priorID
	if err := s.SetOpenAIBackgroundConsent(first, true); err != nil {
		t.Fatal(err)
	}
	f.noPlan = true
	signInFixture(t, s, f, OpenAISignIn{NewAccount: true})
	second := f.clientID
	if first == second {
		t.Fatal("new account was not registered separately")
	}
	if _, err := s.Credential(ctx, openAIRequest(false)); !errors.Is(err, ErrOpenAIPlanDisabled) {
		t.Fatalf("credential after plan refusal: %v", err)
	}
	status, _ := s.Status()
	if status[1].Configured || len(status[1].OpenAIAccounts) != 2 {
		t.Fatal("plan refusal did not retain separate account metadata")
	}
	f.noPlan = false
	signInFixture(t, s, f, OpenAISignIn{EnablePlan: true})
	if f.query.Get("prompt") != "consent" || f.query.Get("client_id") != second {
		t.Fatal("explicit consent retry did not reuse registration")
	}
	if _, err := s.Credential(ctx, openAIRequest(true)); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("different registration inherited background consent: %v", err)
	}
	f.priorID = firstIDToken
	signInFixture(t, s, f, OpenAISignIn{ClientID: first})
	if _, err := s.Credential(ctx, openAIRequest(true)); err != nil {
		t.Fatalf("selected account lost its own background consent: %v", err)
	}
}

func TestOpenAIRotationAndRevokedRefresh(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	f := newOpenAIFixture(t, s)
	signInFixture(t, s, f, OpenAISignIn{})
	initialClient := f.clientID
	f.now = f.now.Add(time.Hour - 15*time.Second)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Credential(ctx, openAIRequest(false)); err != nil {
				t.Errorf("concurrent credential: %v", err)
			}
		}()
	}
	wg.Wait()
	if f.refreshes != 1 {
		t.Fatalf("%d refresh requests for one rotation", f.refreshes)
	}

	reopened := NewStore(s.Path())
	reopened.now, reopened.openAI = s.now, s.openAI
	if credential, err := reopened.Credential(ctx, openAIRequest(false)); err != nil || credential.Secret.Reveal() != f.access {
		t.Fatalf("rotation did not survive reopening: %v", err)
	}
	f.now = f.now.Add(time.Hour)
	f.temporary = true
	if _, err := reopened.Credential(ctx, openAIRequest(false)); err == nil || strings.Contains(err.Error(), f.refresh) {
		t.Fatal("temporary failure was swallowed or exposed credential material")
	}
	f.temporary, f.revoke = false, true
	if _, err := reopened.Credential(ctx, openAIRequest(false)); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("revoked refresh did not ask for sign-in: %v", err)
	}
	restarted := NewStore(s.Path())
	restarted.now, restarted.openAI = s.now, s.openAI
	if _, err := restarted.Credential(ctx, openAIRequest(false)); !errors.Is(err, ErrNotConfigured) || f.refreshes != 3 {
		t.Fatal("revoked token was attempted again after reopen")
	}
	f.revoke, f.priorID = false, ""
	signInFixture(t, restarted, f, OpenAISignIn{})
	if f.query.Get("client_id") != initialClient || f.query.Has("id_token_hint") {
		t.Fatal("sign-in after revocation did not reuse the registration without tokens")
	}
}

func TestOpenAIRefreshRetriesOnlyPersistenceAfterFileFailure(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	f := newOpenAIFixture(t, s)
	setKey(t, s, fixtureKey)
	signInFixture(t, s, f, OpenAISignIn{})
	f.now = f.now.Add(time.Hour)
	path := s.path
	s.path = t.TempDir() // Atomically replacing an existing directory fails.
	_, err := s.Credential(ctx, openAIRequest(false))
	if err == nil || f.refreshes != 1 {
		t.Fatal("rotation did not report the simulated write failure")
	}
	s.path = path
	if _, err := s.Credential(ctx, openAIRequest(false)); err != nil || f.refreshes != 1 {
		t.Fatalf("persistence retry attempted another OAuth rotation: %v", err)
	}
	reopened := NewStore(path)
	reopened.now, reopened.openAI = s.now, s.openAI
	if credential, err := reopened.Credential(ctx, openAIRequest(false)); err != nil || credential.Secret.Reveal() != f.access {
		t.Fatal("retried write did not persist the replacement")
	}
	if credential, err := openrouter(reopened); err != nil || credential.Secret.Reveal() != fixtureKey {
		t.Fatal("SIWC writes changed the API-key provider")
	}
	// A Phase 1 writer sees the OpenAI entry as an unknown provider and
	// preserves its raw JSON while changing OpenRouter.
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := decodeDocument(old)
	if err != nil {
		t.Fatal(err)
	}
	raw := doc.providers[string(ProviderOpenAI)]
	setKey(t, reopened, fixtureKey2)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err = decodeDocument(after)
	if err != nil || string(doc.providers[string(ProviderOpenAI)]) != string(raw) {
		t.Fatal("an API-key rewrite changed the saved OpenAI entry")
	}
}

func TestOpenAIRejectsInvalidIdentityOrCallback(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	f := newOpenAIFixture(t, s)
	for _, mode := range []string{"issuer", "audience", "expiration", "nonce", "signature", "invalid-signature"} {
		t.Run(mode, func(t *testing.T) {
			f.badIdentity = mode
			if err := s.SignInOpenAI(ctx, OpenAISignIn{}, f.browser); err == nil || !strings.Contains(err.Error(), "validate") || strings.Contains(err.Error(), f.priorID) {
				t.Fatal("invalid ID token was accepted or exposed")
			}
			if _, err := s.Credential(ctx, openAIRequest(false)); !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("failed sign-in activated a credential: %v", err)
			}
		})
	}
	f.badIdentity, f.deny = "", true
	grants := f.grants
	if err := s.SignInOpenAI(ctx, OpenAISignIn{}, f.browser); err == nil || f.grants != grants {
		t.Fatal("authorization denial was exchanged")
	}

	f.deny, f.retryCode = false, true
	originalAuthorizes := f.authorizes
	f.priorID = ""
	signInFixture(t, s, f, OpenAISignIn{})
	if f.authorizes != originalAuthorizes+2 || f.query.Get("client_id") == dynamicClientID {
		t.Fatal("invalid code did not start a fresh authorization with the issued client ID")
	}

	grants = f.grants
	access := f.access
	f.callbackID = "different-client"
	if err := s.SignInOpenAI(ctx, OpenAISignIn{}, f.browser); err == nil || f.grants != grants {
		t.Fatal("substituted callback client ID was exchanged")
	}
	f.callbackID = ""
	f.badIdentity = "substitution"
	if err := s.SignInOpenAI(ctx, OpenAISignIn{}, f.browser); err == nil {
		t.Fatal("a different subject replaced the selected account")
	}
	if cred, err := s.Credential(ctx, openAIRequest(false)); err != nil || cred.Secret.Reveal() != access {
		t.Fatal("failed reauthorization changed the active credential")
	}
}
