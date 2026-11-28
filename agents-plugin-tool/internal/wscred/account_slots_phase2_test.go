package wscred

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSetActiveOpenAIAccountRefusalsAndConcurrency(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "auth", FileName))
	f := newOpenAIFixture(t, store)

	// Sign in Account A with plan enabled + consent.
	signInFixture(t, store, f, OpenAISignIn{EnablePlan: true})
	clientA := f.clientID
	if err := store.SetOpenAIBackgroundConsent(clientA, true); err != nil {
		t.Fatal(err)
	}

	// Sign in Account B with plan enabled, but without consent yet.
	signInFixture(t, store, f, OpenAISignIn{NewAccount: true, EnablePlan: true})
	clientB := f.clientID
	if clientB == clientA {
		t.Fatalf("expected distinct client IDs, got %q", clientA)
	}
	// Active is now B after sign-in; switch back to A (which has consent).
	if err := store.SetActiveOpenAIAccount(clientA); err != nil {
		t.Fatalf("switch to A: %v", err)
	}

	// D8: unknown client ID refused with no state change.
	if err := store.SetActiveOpenAIAccount("oaiapp_missing"); !errors.Is(err, ErrOpenAIAccountNotFound) {
		t.Fatalf("missing account err = %v, want ErrOpenAIAccountNotFound", err)
	}
	if got := store.ActiveOpenAIAccount(); got != clientA {
		t.Fatalf("active mutated after missing refusal: %q", got)
	}

	// D8: target lacking consent is refused with ErrConsentRequired and no state change.
	if err := store.SetActiveOpenAIAccount(clientB); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("no-consent err = %v, want ErrConsentRequired", err)
	}
	if got := store.ActiveOpenAIAccount(); got != clientA {
		t.Fatalf("active mutated after no-consent refusal: %q", got)
	}

	// Sign in Account C without plan scope; grant consent so plan check is isolated.
	f.noPlan = true
	signInFixture(t, store, f, OpenAISignIn{NewAccount: true, EnablePlan: false})
	clientC := f.clientID
	f.noPlan = false
	if err := store.SetOpenAIBackgroundConsent(clientC, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveOpenAIAccount(clientA); err != nil {
		t.Fatal(err)
	}
	// Without plan scope, account.withGrant strips access/refresh tokens so either
	// ErrOpenAITokensMissing or ErrOpenAIPlanDisabled is returned and no state changes.
	if err := store.SetActiveOpenAIAccount(clientC); !errors.Is(err, ErrOpenAITokensMissing) && !errors.Is(err, ErrOpenAIPlanDisabled) {
		t.Fatalf("no-plan/no-tokens err = %v", err)
	}
	if got := store.ActiveOpenAIAccount(); got != clientA {
		t.Fatalf("active mutated after no-plan refusal: %q", got)
	}

	// Craft a plan-disabled account that still has tokens to specifically verify ErrOpenAIPlanDisabled.
	store.openAIMu.Lock()
	store.mu.Lock()
	st := store.doc.openAIState()
	accNoPlan := st.account(clientC)
	accNoPlan.AccessToken = "fake-access-no-plan"
	accNoPlan.RefreshToken = "fake-refresh-no-plan"
	if err := store.writeOpenAIAccountLocked(st, accNoPlan); err != nil {
		store.mu.Unlock()
		store.openAIMu.Unlock()
		t.Fatal(err)
	}
	store.mu.Unlock()
	store.openAIMu.Unlock()
	if err := store.SetActiveOpenAIAccount(clientC); !errors.Is(err, ErrOpenAIPlanDisabled) {
		t.Fatalf("plan-disabled err = %v, want ErrOpenAIPlanDisabled", err)
	} else if strings.Contains(err.Error(), "fake-access-no-plan") || strings.Contains(err.Error(), "fake-refresh-no-plan") {
		t.Fatalf("error leaked raw tokens: %v", err)
	}

	// Grant consent to B; verify switching A <-> B and serving a pinned non-active account concurrently.
	if err := store.SetOpenAIBackgroundConsent(clientB, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveOpenAIAccount(clientB); err != nil {
		t.Fatalf("switch to B: %v", err)
	}
	if got := store.ActiveOpenAIAccount(); got != clientB {
		t.Fatalf("active = %q, want %q", got, clientB)
	}

	// D7: pinned request for non-active A succeeds while B is active.
	reqA, err := store.BeginOpenAIPlanRequestFor(context.Background(), clientA, true)
	if err != nil {
		t.Fatalf("BeginOpenAIPlanRequestFor(non-active A): %v", err)
	}
	if reqA.Credential.Secret.IsZero() {
		t.Fatalf("pinned plan request for A returned empty secret: %+v", reqA)
	}
	if reqA.Finish != nil {
		reqA.Finish(OpenAIPlanSucceeded)
	}
	credA, err := store.Credential(context.Background(), Request{Provider: ProviderOpenAI, AgentSession: true, Background: true, OpenAIAccount: clientA})
	if err != nil || credA.Secret.Reveal() != reqA.Credential.Secret.Reveal() {
		t.Fatalf("Credential(OpenAIAccount=A) = %v, %v", credA.Secret.IsZero(), err)
	}

	// Concurrency test under -race: simultaneous switches and pinned lookups.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		target := clientA
		if i%2 == 1 {
			target = clientB
		}
		go func(id string) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if err := store.SetActiveOpenAIAccount(id); err != nil {
					t.Errorf("concurrent SetActiveOpenAIAccount(%s): %v", id, err)
				}
			}
		}(target)
		go func(pin string) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				pr, err := store.BeginOpenAIPlanRequestFor(context.Background(), pin, true)
				if err != nil || pr.Credential.Secret.IsZero() {
					t.Errorf("concurrent BeginOpenAIPlanRequestFor(%s): %v", pin, err)
				} else if pr.Finish != nil {
					pr.Finish(OpenAIPlanSucceeded)
				}
			}
		}(target)
	}
	wg.Wait()
}

func TestOpenAIDualSignInSanitizedURLAndManualComplete(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "auth", FileName))
	f := newOpenAIFixture(t, store)

	// 1) Dynamic registration: browser launch fails, IgnoreBrowserError keeps waiting,
	//    manual completion validates state, missing client_id, and full callback URL.
	readyCh := make(chan OpenAISignInReady, 1)
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- store.SignInOpenAIWithCallbacks(context.Background(), OpenAISignIn{EnablePlan: true}, OpenAISignInCallbacks{
			OpenBrowser:        func(string) error { return errors.New("headless display unavailable") },
			IgnoreBrowserError: true,
			OnReady:            func(info OpenAISignInReady) { readyCh <- info },
		})
	}()

	ready := <-readyCh
	if !ready.Dynamic || ready.AuthorizeURL == "" || ready.Complete == nil {
		t.Fatalf("unexpected ready info: %+v", ready)
	}
	if strings.Contains(ready.AuthorizeURL, "id_token_hint") {
		t.Fatalf("copyable AuthorizeURL must never contain id_token_hint: %s", ready.AuthorizeURL)
	}

	// Visit the authorize URL without following redirect to simulate remote browser login.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(ready.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cbURL := resp.Header.Get("Location")
	parsedCB, err := url.Parse(cbURL)
	if err != nil {
		t.Fatal(err)
	}
	q := parsedCB.Query()

	// State mismatch is refused with ErrSignInStateMismatch.
	badState := *parsedCB
	bq := badState.Query()
	bq.Set("state", "wrong-state")
	badState.RawQuery = bq.Encode()
	if err := ready.Complete(badState.String()); !errors.Is(err, ErrSignInStateMismatch) {
		t.Fatalf("state mismatch err = %v, want ErrSignInStateMismatch", err)
	}

	// Dynamic registration requires client_id: raw code or callback without client_id returns ErrSignInClientIDMissing.
	if err := ready.Complete(q.Get("code")); !errors.Is(err, ErrSignInClientIDMissing) {
		t.Fatalf("raw code for dynamic client err = %v, want ErrSignInClientIDMissing", err)
	}
	noClient := *parsedCB
	ncq := noClient.Query()
	ncq.Del("client_id")
	noClient.RawQuery = ncq.Encode()
	if err := ready.Complete(noClient.String()); !errors.Is(err, ErrSignInClientIDMissing) {
		t.Fatalf("missing client_id err = %v, want ErrSignInClientIDMissing", err)
	}

	// Full pasted callback URL completes the sign-in cleanly.
	if err := ready.Complete(cbURL); err != nil {
		t.Fatalf("manual complete with callback URL: %v", err)
	}
	if err := <-doneCh; err != nil {
		t.Fatalf("SignInOpenAIWithCallbacks failed: %v", err)
	}
	clientID := store.ActiveOpenAIAccount()
	if clientID == "" {
		t.Fatal("expected active client after manual sign-in")
	}

	// 2) Returning sign-in with retained id_token: copyable AuthorizeURL MUST NOT
	//    contain id_token_hint, and raw code completion succeeds for non-dynamic client.
	f.priorID = "" // Copyable URL omits id_token_hint, so fixture shouldn't demand it on the remote authorize URL.
	readyCh2 := make(chan OpenAISignInReady, 1)
	doneCh2 := make(chan error, 1)
	startURLCh := make(chan string, 1)
	go func() {
		doneCh2 <- store.SignInOpenAIWithCallbacks(context.Background(), OpenAISignIn{ClientID: clientID, EnablePlan: true}, OpenAISignInCallbacks{
			OpenBrowser: func(u string) error {
				startURLCh <- u
				return errors.New("browser unavailable")
			},
			IgnoreBrowserError: true,
			OnReady:            func(info OpenAISignInReady) { readyCh2 <- info },
		})
	}()
	ready2 := <-readyCh2
	startURL := <-startURLCh
	if ready2.Dynamic {
		t.Fatal("returning client should have Dynamic=false")
	}
	if strings.Contains(ready2.AuthorizeURL, "id_token_hint") {
		t.Fatalf("returning AuthorizeURL leaked id_token_hint: %s", ready2.AuthorizeURL)
	}
	if strings.Contains(startURL, "id_token_hint") {
		t.Fatalf("local /auth/start URL leaked id_token_hint: %s", startURL)
	}
	resp2, err := noFollow.Get(ready2.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	parsedCB2, err := url.Parse(resp2.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	// Returning non-dynamic client accepts raw authorization code directly.
	if err := ready2.Complete(parsedCB2.Query().Get("code")); err != nil {
		t.Fatalf("raw code complete for returning client: %v", err)
	}
	if err := <-doneCh2; err != nil {
		t.Fatalf("returning SignInOpenAIWithCallbacks failed: %v", err)
	}
}

func TestParseSlotLoginLineAndCallbackSSRFValidation(t *testing.T) {
	// Browser mode output from codex login.
	_, _, _, port := ParseSlotLoginLine("Starting local login server on http://localhost:1455.")
	if port != 1455 {
		t.Fatalf("listener port = %d, want 1455", port)
	}
	authURL, verifyURL, code, port2 := ParseSlotLoginLine(
		"https://auth.openai.com/oauth/authorize?response_type=code&redirect_uri=http%3A%2F%2F127.0.0.1%3A1455%2Fauth%2Fcallback&state=abc",
	)
	if !strings.HasPrefix(authURL, "https://auth.openai.com/oauth/authorize?") || verifyURL != "" || code != "" || port2 != 1455 {
		t.Fatalf("parsed browser line = (%q, %q, %q, %d)", authURL, verifyURL, code, port2)
	}

	// Device-auth output with ANSI styling.
	_, verifyURL, _, _ = ParseSlotLoginLine("  1. Open \x1b[94mhttps://auth.openai.com/codex/device\x1b[0m in your browser")
	if verifyURL != "https://auth.openai.com/codex/device" {
		t.Fatalf("verifyURL = %q", verifyURL)
	}
	_, _, code, _ = ParseSlotLoginLine("  2. Enter code: \x1b[1;94mWXYZ-98765\x1b[0m")
	if code != "WXYZ-98765" {
		t.Fatalf("userCode = %q", code)
	}

	// SSRF-safe callback validation: loopback host + exact port only.
	validURL, err := ValidateLoopbackCallbackReplay("http://127.0.0.1:1455/auth/callback?code=c1&state=s1", 1455)
	if err != nil || !strings.HasPrefix(validURL, "http://127.0.0.1:1455/auth/callback?") {
		t.Fatalf("valid loopback callback failed: %q %v", validURL, err)
	}
	validLocalhost, err := ValidateLoopbackCallbackReplay("http://localhost:1455/auth/callback?code=c1&state=s1", 1455)
	if err != nil || !strings.HasPrefix(validLocalhost, "http://127.0.0.1:1455/auth/callback?") {
		t.Fatalf("valid localhost callback failed: %q %v", validLocalhost, err)
	}
	for _, badHost := range []string{
		"http://evil.example:1455/auth/callback?code=c1",
		"http://169.254.169.254:1455/latest/meta-data",
		"http://127.0.0.1.evil.example:1455/auth/callback",
		"https://127.0.0.1:1455/auth/callback",
	} {
		if _, err := ValidateLoopbackCallbackReplay(badHost, 1455); err == nil {
			t.Fatalf("expected error for non-loopback/non-http %q", badHost)
		}
	}
	if _, err := ValidateLoopbackCallbackReplay("http://127.0.0.1:9999/auth/callback?code=c1", 1455); !errors.Is(err, ErrSlotCallbackPortMismatch) {
		t.Fatalf("port mismatch err = %v, want ErrSlotCallbackPortMismatch", err)
	}
}
