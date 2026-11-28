package wscred

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	openAIResource   = "https://api.openai.com/v1"
	openAIIssuer     = "https://auth.openai.com"
	openAIPlanScope  = "chatgpt.tokens.use.direct"
	openAIScopes     = "openid profile email offline_access resource.invoke " + openAIPlanScope
	dynamicClientID  = "dynamic_agent_client"
	openAICallback   = "/auth/callback"
	openAISignInWait = 5 * time.Minute
)

type openAIOAuth struct {
	issuer, api string
	http        *http.Client
}

func newOpenAIOAuth() openAIOAuth {
	return openAIOAuth{issuer: openAIIssuer, api: openAIResource, http: &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type openAITokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
}

// Provider bodies and transport errors can include request or response
// credentials. Only an HTTP status, allowlisted OAuth codes and local error
// messages leave this client.
func (o openAIOAuth) request(ctx context.Context, method, endpoint string, form url.Values, bearer Secret) (*http.Response, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, errors.New("invalid OpenAI endpoint")
	}
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if !bearer.IsZero() {
		req.Header.Set("Authorization", "Bearer "+bearer.Reveal())
	}
	resp, err := o.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not reach OpenAI; try again")
	}
	return resp, nil
}

func readOpenAIJSON(r io.Reader, out any) error {
	const maxBytes = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil || len(raw) > maxBytes || json.Unmarshal(raw, out) != nil {
		return errors.New("OpenAI returned an unreadable response")
	}
	return nil
}

type openAIGrantError struct {
	status int
	code   string
}

func (e *openAIGrantError) Error() string {
	if e.code == "invalid_client" {
		return "OpenAI rejected the registered client; check the connection in ChatGPT settings or register again"
	}
	if e.code != "" {
		return fmt.Sprintf("OpenAI rejected the grant (HTTP %d, %s)", e.status, e.code)
	}
	return fmt.Sprintf("OpenAI rejected the grant (HTTP %d)", e.status)
}

func (e *openAIGrantError) unusableRefresh() bool {
	switch e.code {
	case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
		return true
	}
	return false
}

func (o openAIOAuth) grant(ctx context.Context, form url.Values) (openAITokens, error) {
	form.Set("resource", openAIResource)
	resp, err := o.request(ctx, http.MethodPost, o.issuer+"/api/accounts/oauth/token", form, Secret{})
	if err != nil {
		return openAITokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		failure := &openAIGrantError{status: resp.StatusCode}
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
			var body struct {
				Error string `json:"error"`
			}
			if readOpenAIJSON(resp.Body, &body) == nil {
				switch body.Error {
				case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused", "invalid_client":
					failure.code = body.Error
				}
			}
		}
		return openAITokens{}, failure
	}
	var tokens openAITokens
	err = readOpenAIJSON(resp.Body, &tokens)
	return tokens, err
}

// OpenAISignInReady carries the remote-login details for an active sign-in attempt.
type OpenAISignInReady struct {
	AuthorizeURL string
	Dynamic      bool
	Complete     func(input string) error
}

// OpenAISignInCallbacks configures dual local and remote OpenAI SIWC sign-in (D18).
type OpenAISignInCallbacks struct {
	// OpenBrowser opens the local one-time /auth/start/<token> entry URL.
	// May be nil when starting in copy-link-only mode.
	OpenBrowser func(startURL string) error
	// IgnoreBrowserError keeps waiting for manual callback/code completion when
	// OpenBrowser is nil or returns an error.
	IgnoreBrowserError bool
	// OnReady receives the copyable authorize URL (without id_token_hint) and
	// the manual completion hook once the local listener is bound.
	OnReady func(info OpenAISignInReady)
}

func (o openAIOAuth) authorize(ctx context.Context, hostID string, account openAIAccount, reconsent bool, cb OpenAISignInCallbacks, now func() time.Time) (string, openAIIdentity, openAITokens, error) {
	clientID := account.ClientID
	if clientID == "" {
		clientID = dynamicClientID
	}
	for attempt := 0; ; attempt++ {
		callback, verifier, redirect, nonce, err := o.browserCode(ctx, hostID, clientID, account.IDToken, reconsent, cb)
		if err != nil {
			return "", openAIIdentity{}, openAITokens{}, err
		}
		clientID = callback.clientID
		tokens, err := o.grant(ctx, url.Values{
			"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {callback.code},
			"redirect_uri": {redirect}, "code_verifier": {verifier},
		})
		if err != nil {
			var failure *openAIGrantError
			if attempt == 0 && errors.As(err, &failure) && failure.code == "invalid_grant" {
				// The code is spent. Start a fresh browser transaction with
				// the issued client ID, keeping it in this attempt only.
				continue
			}
			return "", openAIIdentity{}, openAITokens{}, err
		}
		identity, err := o.verifyIdentity(ctx, tokens, clientID, nonce, now)
		return clientID, identity, tokens, err
	}
}

type openAICode struct {
	code, clientID string
	err            error
}

func (o openAIOAuth) browserCode(ctx context.Context, hostID, clientID, idHint string, reconsent bool, cb OpenAISignInCallbacks) (openAICode, string, string, string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return openAICode{}, "", "", "", errors.New("could not start the local OpenAI sign-in listener")
	}
	defer listener.Close()
	host := listener.Addr().String()
	local := "http://" + host
	redirect := local + openAICallback
	state, nonce, verifier := tokenRandom(), tokenRandom(), tokenRandom()
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"client_id": {clientID}, "ext_agent_host_id": {hostID}, "response_type": {"code"},
		"redirect_uri": {redirect}, "scope": {openAIScopes}, "resource": {openAIResource},
		"state": {state}, "nonce": {nonce}, "code_challenge_method": {"S256"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])},
	}
	if clientID == dynamicClientID {
		query.Set("agent_name_hint", "ws")
	}
	if reconsent && clientID != dynamicClientID {
		query.Set("prompt", "consent")
	}
	// The copyable URL deliberately omits id_token_hint so the prior id_token
	// is never exposed in status responses or copyable remote links (D18).
	copyURL := o.issuer + "/api/accounts/authorize?" + query.Encode()
	if clientID != dynamicClientID && idHint != "" {
		query.Set("id_token_hint", idHint)
	}
	authorizeURL := o.issuer + "/api/accounts/authorize?" + query.Encode()

	// Pass the browser only the unguessable local entry URL. The retained
	// ID-token hint goes to OpenAI through the browser, outside process argv.
	startPath := "/auth/start/" + tokenRandom()
	result := make(chan openAICode, 1)
	var startOnce, callbackOnce sync.Once
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			if r.Host != host || r.Method != http.MethodGet {
				http.NotFound(w, r)
				return
			}
			switch r.URL.Path {
			case startPath:
				used := true
				startOnce.Do(func() {
					used = false
					w.Header().Set("Location", authorizeURL)
					w.WriteHeader(http.StatusFound)
				})
				if used {
					http.NotFound(w, r)
				}
			case openAICallback:
				q, err := url.ParseQuery(r.URL.RawQuery)
				if err != nil || len(q["state"]) != 1 ||
					subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
					http.Error(w, "Invalid OpenAI sign-in state.", http.StatusBadRequest)
					return
				}
				used := true
				callbackOnce.Do(func() {
					used = false
					code := parseOpenAICallback(q, clientID)
					if code.err != nil {
						http.Error(w, "OpenAI sign-in was not completed. Return to ws.", http.StatusBadRequest)
					} else {
						_, _ = io.WriteString(w, "OpenAI sign-in response received. Return to ws to finish.")
					}
					result <- code
				})
				if used {
					http.NotFound(w, r)
				}
			default:
				http.NotFound(w, r)
			}
		}),
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
	}()
	complete := func(raw string) error {
		code, err := parseManualOpenAICallback(raw, state, clientID)
		if err != nil {
			return err
		}
		used := true
		callbackOnce.Do(func() {
			used = false
			result <- code
		})
		if used {
			return ErrSignInAlreadyCompleted
		}
		return nil
	}
	if cb.OnReady != nil {
		cb.OnReady(OpenAISignInReady{
			AuthorizeURL: copyURL,
			Dynamic:      clientID == dynamicClientID,
			Complete:     complete,
		})
	}
	var openErr error
	if cb.OpenBrowser != nil {
		openErr = cb.OpenBrowser(local + startPath)
	} else {
		openErr = errors.New("no browser launcher configured")
	}
	if openErr != nil && !cb.IgnoreBrowserError {
		return openAICode{}, "", "", "", errors.New("could not open the browser for OpenAI sign-in")
	}
	select {
	case code := <-result:
		return code, verifier, redirect, nonce, code.err
	case <-ctx.Done():
		return openAICode{}, "", "", "", ctx.Err()
	case <-serveDone:
		return openAICode{}, "", "", "", errors.New("the local OpenAI sign-in listener stopped")
	}
}

func parseManualOpenAICallback(raw, expectedState, clientID string) (openAICode, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 8192 {
		return openAICode{}, ErrSignInInvalidCallback
	}
	if strings.Contains(raw, "://") || strings.Contains(raw, "?") || strings.Contains(raw, "code=") || strings.Contains(raw, "state=") {
		rawQuery := raw
		if u, err := url.Parse(raw); err == nil && u.RawQuery != "" {
			rawQuery = u.RawQuery
		} else if idx := strings.IndexByte(raw, '?'); idx >= 0 {
			rawQuery = raw[idx+1:]
		}
		q, err := url.ParseQuery(rawQuery)
		if err != nil {
			return openAICode{}, ErrSignInInvalidCallback
		}
		if len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(expectedState)) != 1 {
			return openAICode{}, ErrSignInStateMismatch
		}
		if len(q["error"]) == 0 && len(q["code"]) == 1 && q.Get("code") != "" && clientID == dynamicClientID {
			issued := q.Get("client_id")
			if issued == "" || issued == dynamicClientID {
				return openAICode{}, ErrSignInClientIDMissing
			}
		}
		code := parseOpenAICallback(q, clientID)
		if code.err != nil {
			return openAICode{}, ErrSignInInvalidCallback
		}
		return code, nil
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= ' ' || raw[i] >= 0x7f {
			return openAICode{}, ErrSignInInvalidCallback
		}
	}
	if clientID == dynamicClientID {
		return openAICode{}, ErrSignInClientIDMissing
	}
	return openAICode{code: raw, clientID: clientID}, nil
}

func tokenRandom() string {
	// A UUIDv4 supplies 122 random bits; two UUIDs meet the PKCE verifier
	// length and provide independent entropy for each transaction value.
	return uuid.NewString() + uuid.NewString()
}

func parseOpenAICallback(q url.Values, clientID string) openAICode {
	if len(q["error"]) != 0 {
		if q.Get("error") == "access_denied" {
			return openAICode{err: errors.New("ChatGPT authorization was declined")}
		}
		return openAICode{err: errors.New("OpenAI authorization failed")}
	}
	if len(q["code"]) != 1 || q.Get("code") == "" || len(q["client_id"]) > 1 {
		return openAICode{err: errors.New("OpenAI authorization returned an incomplete callback")}
	}
	issued := q.Get("client_id")
	if clientID == dynamicClientID {
		if issued == "" || issued == dynamicClientID {
			return openAICode{err: errors.New("OpenAI registration returned no issued client ID")}
		}
		clientID = issued
	} else if issued != "" && issued != clientID {
		return openAICode{err: errors.New("OpenAI authorization returned a different client ID")}
	}
	return openAICode{code: q.Get("code"), clientID: clientID}
}

type openAIIdentity struct {
	jwt.RegisteredClaims
	Nonce         string `json:"nonce"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Authorized    string `json:"azp"`
	AccessHash    string `json:"at_hash"`
}

func (o openAIOAuth) verifyIdentity(ctx context.Context, tokens openAITokens, clientID, nonce string, now func() time.Time) (openAIIdentity, error) {
	var identity openAIIdentity
	_, err := jwt.ParseWithClaims(tokens.IDToken, &identity, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return o.verificationKey(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(o.issuer), jwt.WithAudience(clientID),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(5*time.Second), jwt.WithTimeFunc(now))
	if err != nil || identity.Subject == "" || identity.IssuedAt == nil ||
		(nonce != "" && identity.Nonce != nonce) ||
		(identity.Authorized != "" && identity.Authorized != clientID) ||
		(len(identity.Audience) > 1 && identity.Authorized != clientID) {
		if ctx.Err() != nil {
			return openAIIdentity{}, ctx.Err()
		}
		return openAIIdentity{}, errors.New("could not validate the OpenAI identity token")
	}
	if identity.AccessHash != "" && tokens.AccessToken != "" {
		sum := sha256.Sum256([]byte(tokens.AccessToken))
		if identity.AccessHash != base64.RawURLEncoding.EncodeToString(sum[:16]) {
			return openAIIdentity{}, errors.New("OpenAI returned mismatched access and identity tokens")
		}
	}
	return identity, nil
}

func (o openAIOAuth) verificationKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if kid == "" {
		return nil, errors.New("missing OpenAI signing key ID")
	}
	resp, err := o.request(ctx, http.MethodGet, o.issuer+"/.well-known/jwks.json", nil, Secret{})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var jwks struct {
		Keys []struct {
			ID  string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if resp.StatusCode == http.StatusOK && readOpenAIJSON(resp.Body, &jwks) == nil {
		for _, key := range jwks.Keys {
			if key.ID != kid || key.Kty != "RSA" || (key.Alg != "" && key.Alg != "RS256") || (key.Use != "" && key.Use != "sig") {
				continue
			}
			n, nErr := base64.RawURLEncoding.DecodeString(key.N)
			e, eErr := base64.RawURLEncoding.DecodeString(key.E)
			if nErr != nil || eErr != nil || len(n) > 1024 || len(e) > 4 {
				break
			}
			modulus := new(big.Int).SetBytes(n)
			exponent := new(big.Int).SetBytes(e).Int64()
			if modulus.BitLen() >= 2048 && exponent >= 3 && exponent <= 1<<31-1 && exponent%2 == 1 {
				return &rsa.PublicKey{N: modulus, E: int(exponent)}, nil
			}
			break
		}
	}
	return nil, errors.New("OpenAI signing key was not available")
}
