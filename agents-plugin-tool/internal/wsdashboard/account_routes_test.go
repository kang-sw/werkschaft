package wsdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
)

type accountTestStore struct {
	*wscred.Store
	verify  func(context.Context, wscred.Secret) (wscred.ChangeResult, error)
	signIn  func(context.Context, wscred.OpenAISignIn, func(string) error) error
	allow   func(string, uint64, bool) error
	dismiss func(uint64)
	signOut func(string) error
}

func (s *accountTestStore) VerifyOpenRouterKey(ctx context.Context, key wscred.Secret) (wscred.ChangeResult, error) {
	return s.verify(ctx, key)
}

func (s *accountTestStore) SignInOpenAI(ctx context.Context, selectAccount wscred.OpenAISignIn, open func(string) error) error {
	return s.signIn(ctx, selectAccount, open)
}

func (s *accountTestStore) SetActiveOpenAIBackgroundConsent(clientID string, prompt uint64, allowed bool) error {
	return s.allow(clientID, prompt, allowed)
}

func (s *accountTestStore) DismissOpenAIConsentPrompt(prompt uint64) { s.dismiss(prompt) }

func (s *accountTestStore) SignOutOpenAI(clientID string) error { return s.signOut(clientID) }

func requestAccount(t *testing.T, d *Daemon, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := authRequest(method, accountPrefix+path, body)
	req.AddCookie(authCookie(t, d))
	if method == http.MethodPost {
		req.Header.Set("Origin", "http://"+req.Host)
	}
	return authServe(d, req)
}

func TestAccountBrowserBoundary(t *testing.T) {
	d := authFixture(t)
	d.lan = true
	lanToken, err := d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	d.credentials = wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))
	localPeer := authRequest(http.MethodGet, "/", "").RemoteAddr
	lanPeer := net.JoinHostPort(net.IPv4(192, 0, 2, 8).String(), "8888")
	routes := []struct{ method, path string }{
		{http.MethodGet, "status"},
		{http.MethodGet, "sign-in/status"},
		{http.MethodPost, "key"},
		{http.MethodPost, "endpoints"},
		{http.MethodPost, "consent"},
		{http.MethodPost, "consent/dismiss"},
		{http.MethodPost, "sign-in/start"},
		{http.MethodPost, "sign-in/cancel"},
		{http.MethodPost, "sign-out"},
	}
	for _, route := range routes {
		t.Run(route.path, func(t *testing.T) {
			path := accountPrefix + route.path
			for _, v := range []viewer{
				{peer: lanPeer, host: "dashboard.example", cookie: lanToken},
				{peer: lanPeer, host: "localhost:43210", cookie: lanToken},
				{peer: localPeer, host: "dashboard.example", cookie: lanToken},
			} {
				w := serve(d, v, route.method, path, "{}")
				if w.Code != http.StatusNotFound {
					t.Fatalf("LAN account route = %d, want 404", w.Code)
				}
			}
			req := authRequest(route.method, path, "{}")
			req.Header.Set("Authorization", "Bearer "+d.endpoint.Token)
			req.Header.Set("Origin", "http://"+req.Host)
			if w := authServe(d, req); w.Code != http.StatusUnauthorized {
				t.Fatalf("endpoint bearer admitted without browser cookie: %d", w.Code)
			}
			if route.method != http.MethodPost {
				return
			}
			for _, origins := range [][]string{
				nil, {"null"}, {"https://different.example"}, {"http://" + authRequest(http.MethodGet, "/", "").Host, "https://different.example"},
			} {
				req := authRequest(route.method, path, "{}")
				req.AddCookie(authCookie(t, d))
				for _, origin := range origins {
					req.Header.Add("Origin", origin)
				}
				if w := authServe(d, req); w.Code != http.StatusForbidden {
					t.Fatalf("bad mutation Origin admitted: %d", w.Code)
				}
			}
		})
	}
	if w := requestAccount(t, d, http.MethodGet, "status", ""); w.Code != http.StatusOK ||
		w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"loopback_peer":true`) {
		t.Fatalf("local Account status: %d %s", w.Code, w.Body.String())
	}
}

func TestAccountKeyRoutesRedactResponsesAndErrors(t *testing.T) {
	d := authFixture(t)
	store := &accountTestStore{Store: wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))}
	d.credentials = store
	store.verify = func(ctx context.Context, key wscred.Secret) (wscred.ChangeResult, error) {
		switch key.Reveal() {
		case credFixtureKey:
			return store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenRouter, APIKey: key})
		case credFixtureKey2:
			return wscred.ChangeResult{}, wscred.ErrOpenRouterKeyRejected
		case "format-fixture":
			return wscred.ChangeResult{}, wscred.InvalidKeyError("format-fixture")
		default:
			return wscred.ChangeResult{}, errors.New("private-upstream-fixture-header")
		}
	}
	test := func(path, method, body string, status int, want string) {
		t.Helper()
		w := requestAccount(t, d, method, path, body)
		out := w.Body.String()
		if w.Code != status || !strings.Contains(out, want) {
			t.Fatalf("account %s %s: %d %s", method, path, w.Code, out)
		}
		for _, private := range []string{credFixtureKey, credFixtureKey2, "format-fixture", "private-upstream-fixture-header", "fingerprint", "access_token", "refresh_token"} {
			if strings.Contains(out, private) {
				t.Fatalf("Account %s echoed a private value or field", path)
			}
		}
	}
	set := func(key string) string {
		return `{"op":"set","provider":"openrouter","key":` + strconv.Quote(key) + `}`
	}
	test("key", http.MethodPost, set(credFixtureKey), 200, `"masked":"sk-or-...0001"`)
	test("key", http.MethodPost, set(credFixtureKey2), 422, `"key_rejected"`)
	test("key", http.MethodPost, set("format-fixture"), 422, `"invalid_key"`)
	test("key", http.MethodPost, set("private-upstream-fixture-header"), 503, `"credential_unavailable"`)
	test("key", http.MethodPost, set(credFixtureKey)+`{}`, 400, `"invalid_request"`)
	test("status", http.MethodGet, "", 200, `"masked":"sk-or-...0001"`)
	test("key", http.MethodPost, `{"op":"remove","provider":"openrouter"}`, 200, `"configured":false`)
	test("key", http.MethodPost, `{"op":"recheck","provider":"openrouter"}`, 404, `"credential_missing"`)
}

func TestAccountSignInTrackingAndCancellation(t *testing.T) {
	d := authFixture(t)
	opened := make(chan struct{})
	d.openSystem = func(string) error { return nil }
	store := &accountTestStore{Store: wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))}
	canceled := make(chan struct{})
	store.signIn = func(ctx context.Context, opts wscred.OpenAISignIn, open func(string) error) error {
		if !opts.EnablePlan || opts.ClientID != "fixture-client" {
			return errors.New("unexpected account selection")
		}
		if err := open("http://localhost/fixture-oauth"); err != nil {
			return err
		}
		close(opened)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	}
	d.credentials = store
	t.Cleanup(d.accountSignIn.stop)
	read := func(method, action, request string, status int) accountSignInStatus {
		t.Helper()
		w := requestAccount(t, d, method, action, request)
		if w.Code != status || strings.Contains(w.Body.String(), "fixture-oauth") {
			t.Fatalf("sign-in %s: %d %s", action, w.Code, w.Body.String())
		}
		var response accountSignInStatus
		if status < 300 && json.Unmarshal(w.Body.Bytes(), &response) != nil {
			t.Fatalf("invalid sign-in status response: %s", w.Body.String())
		}
		return response
	}
	start := read(http.MethodPost, "sign-in/start", `{"client_id":"fixture-client","enable_plan":true}`, 202)
	if start.State != "starting" || start.Attempt == "" {
		t.Fatalf("initial sign-in = %+v", start)
	}
	select {
	case <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("sign-in did not open a browser")
	}
	read(http.MethodPost, "sign-in/start", "{}", 409)
	read(http.MethodPost, "sign-out", `{"client_id":"fixture-client"}`, 409)
	read(http.MethodPost, "sign-in/cancel", `{"attempt":"wrong-attempt"}`, 409)
	waiting := read(http.MethodGet, "sign-in/status", "", 200)
	if waiting.State != "waiting" || waiting.Attempt != start.Attempt {
		t.Fatalf("pending sign-in = %+v", waiting)
	}
	read(http.MethodPost, "sign-in/cancel", `{"attempt":`+strconv.Quote(start.Attempt)+`}`, 202)
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not reach the sign-in flow")
	}
	for deadline := time.Now().Add(3 * time.Second); ; {
		if done := read(http.MethodGet, "sign-in/status", "", 200); done.State == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sign-in did not finish cancelling")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAccountConsentAndSignOutActions(t *testing.T) {
	d := authFixture(t)
	store := &accountTestStore{Store: wscred.NewStore(filepath.Join(t.TempDir(), wscred.FileName))}
	d.credentials = store
	store.allow = func(clientID string, prompt uint64, allowed bool) error {
		switch {
		case clientID == "active-fixture" && prompt == 0 && allowed:
			return nil
		case clientID == "" && prompt == 5 && !allowed:
			return nil
		default:
			return wscred.ErrOpenAIAccountChanged
		}
	}
	var dismissed uint64
	store.dismiss = func(prompt uint64) { dismissed = prompt }
	store.signOut = func(clientID string) error {
		if clientID != "active-fixture" {
			return wscred.ErrOpenAIAccountChanged
		}
		return nil
	}
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"consent", `{"client_id":"active-fixture","allowed":true}`, 204},
		{"consent", `{"prompt":5,"allowed":false}`, 204},
		{"consent", `{"prompt":4,"allowed":true}`, 409},
		{"consent", `{"prompt":5}`, 400},
		{"consent/dismiss", `{"prompt":6}`, 204},
		{"sign-out", `{"client_id":"stale-fixture"}`, 409},
		{"sign-out", `{"client_id":"active-fixture"}`, 204},
	} {
		w := requestAccount(t, d, http.MethodPost, tc.path, tc.body)
		if w.Code != tc.want || strings.Contains(w.Body.String(), "fixture") {
			t.Fatalf("Account %s: got %d, want %d", tc.path, w.Code, tc.want)
		}
	}
	if dismissed != 6 {
		t.Fatalf("dismissed prompt %d, want 6", dismissed)
	}
}

type consentStreamStore struct{ *wscred.Store }

func (s *consentStreamStore) OpenAIConsentPrompt() (uint64, <-chan struct{}) { return 5, nil }

func TestAccountConsentSignalUsesDashboardViewerBoundary(t *testing.T) {
	dir := t.TempDir()
	opts := DaemonOptions{
		Dir: dir, Home: t.TempDir(), Version: "test",
		credentialSource: &consentStreamStore{wscred.NewStore(filepath.Join(t.TempDir(), wscred.FileName))},
	}
	lanMode(&opts)
	d, err := StartDaemon(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	for _, tc := range []struct {
		viewer viewer
		local  bool
	}{
		{viewer{peer: authRequest(http.MethodGet, "/", "").RemoteAddr, host: "localhost:43210", localCookie: true}, true},
		{viewer{peer: net.JoinHostPort(net.IPv4(192, 0, 2, 8).String(), "8888"), host: "dashboard.example", cookie: readLANToken(t, dir)}, false},
	} {
		w := serve(d, tc.viewer, http.MethodGet, railEventsRoute, "")
		line := "event: openai-consent\ndata: {\"prompt\":5,\"local\":" + strconv.FormatBool(tc.local) + "}\n\n"
		if w.Code != 200 || !strings.Contains(w.Body.String(), line) {
			t.Fatalf("openai-consent missing from viewer stream (%d, local %v)", w.Code, tc.local)
		}
	}
}
