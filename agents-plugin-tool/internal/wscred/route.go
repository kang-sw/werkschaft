package wscred

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// The internal token route: how a harness process the daemon started
// obtains a provider credential. The daemon owns every credential (an OAuth
// refresh token may rotate on use, so two refreshing processes would void
// each other); a harness asks the daemon for the current one over its
// per-session bearer channel. The route is not an MCP tool.
//
//	POST TokenRoutePath
//	Authorization: Bearer <the session's per-session token>
//	{"provider": "openrouter"}
//
//	200 {"provider": "openrouter", "kind": "api_key", "secret": "...", "expires_at": "<RFC 3339, short-lived tokens only>"}
//	4xx/5xx {"error": "<Refusal* code>", "provider": "...", "message": "..."}
//
// The daemon authenticates the bearer and admits only a session whose
// harness registration allows provider credentials (wsdashboard); ServeToken
// answers once it has.
const TokenRoutePath = "/auth/provider/token"

// Token route refusal codes ("error"); a client maps them, not the status.
const (
	RefusalBadRequest      = "bad_request"
	RefusalUnauthorized    = "unauthorized"
	RefusalNotPermitted    = "not_permitted"
	RefusalUnknownProvider = "unknown_provider"
	RefusalNotConfigured   = "not_configured"
	RefusalConsentRequired = "consent_required"
	RefusalUnavailable     = "unavailable"
)

// ErrDaemonUnavailable is a RouteClient's failure to reach the token route
// with no earlier credential to fall back on.
var ErrDaemonUnavailable = errors.New("the daemon's provider token route is not reachable")

// maxTokenBody bounds a token route request or answer body.
const maxTokenBody = 64 << 10

type tokenRequest struct {
	Provider ProviderID `json:"provider"`
}

type tokenAnswer struct {
	Provider  ProviderID `json:"provider"`
	Kind      Kind       `json:"kind"`
	Secret    string     `json:"secret"`
	ExpiresAt string     `json:"expires_at,omitempty"`
}

type tokenRefusal struct {
	Error    string     `json:"error"`
	Provider ProviderID `json:"provider,omitempty"`
	Message  string     `json:"message,omitempty"`
}

// ServeToken answers one token route request whose caller the daemon has
// already authenticated and admitted, from src. req carries the requester
// fields the daemon derived from the session; the provider comes from the
// body.
func ServeToken(w http.ResponseWriter, r *http.Request, src Source, req Request) {
	var body tokenRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxTokenBody)).Decode(&body); err != nil || body.Provider == "" {
		RefuseToken(w, http.StatusBadRequest, RefusalBadRequest, "", "the request names no provider")
		return
	}
	req.Provider = body.Provider
	cred, err := src.Credential(r.Context(), req)
	if err != nil {
		status, code := refusalFor(err)
		RefuseToken(w, status, code, body.Provider, err.Error())
		return
	}
	answer := tokenAnswer{Provider: cred.Provider, Kind: cred.Kind, Secret: cred.Secret.Reveal()}
	if !cred.ExpiresAt.IsZero() {
		answer.ExpiresAt = cred.ExpiresAt.UTC().Format(time.RFC3339)
	}
	writeTokenJSON(w, http.StatusOK, answer)
}

func refusalFor(err error) (int, string) {
	switch {
	case errors.Is(err, ErrUnknownProvider):
		return http.StatusNotFound, RefusalUnknownProvider
	case errors.Is(err, ErrNotConfigured):
		return http.StatusNotFound, RefusalNotConfigured
	case errors.Is(err, ErrConsentRequired):
		return http.StatusForbidden, RefusalConsentRequired
	}
	return http.StatusServiceUnavailable, RefusalUnavailable
}

// RefuseToken writes a token route refusal. message must not quote a
// secret or the caller's token.
func RefuseToken(w http.ResponseWriter, status int, code string, provider ProviderID, message string) {
	writeTokenJSON(w, status, tokenRefusal{Error: code, Provider: provider, Message: message})
}

func writeTokenJSON(w http.ResponseWriter, status int, value any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// DaemonBaseURL is the daemon's base URL ("http://127.0.0.1:<port>") from
// a session's ws MCP route URL, which a hosted harness is given.
func DaemonBaseURL(sessionURL string) (string, error) {
	u, err := url.Parse(sessionURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("not a daemon session URL")
	}
	return u.Scheme + "://" + u.Host, nil
}

// RouteClient is a harness process's Source: it asks the daemon's token
// route with the session's bearer. While the daemon cannot be reached (a
// daemon replacement, including a successor that does not know the
// session's bearer yet), it answers the last credential the route gave for
// the provider, unless that one has expired, and asks again on the next
// call; a refusal about the credential itself (not configured, consent,
// not permitted, unknown provider) forgets it.
type RouteClient struct {
	url   string
	token Secret
	http  *http.Client
	now   func() time.Time

	mu   sync.Mutex
	last map[ProviderID]Credential
}

// NewRouteClient returns a client of the token route on the daemon at
// baseURL, authenticated with the session's token; a nil client uses
// http.DefaultClient.
func NewRouteClient(baseURL string, token Secret, client *http.Client) *RouteClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &RouteClient{url: baseURL + TokenRoutePath, token: token, http: client, now: time.Now, last: map[ProviderID]Credential{}}
}

// Credential asks the route for req.Provider's credential (Source). The
// requester fields of req are not sent: the daemon derives them from the
// session the token names.
func (c *RouteClient) Credential(ctx context.Context, req Request) (Credential, error) {
	body, err := json.Marshal(tokenRequest{Provider: req.Provider})
	if err != nil {
		return Credential{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return Credential{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token.Reveal())
	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return Credential{}, ctx.Err()
		}
		return c.fallback(req.Provider, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenBody))
	if err != nil {
		return c.fallback(req.Provider, err)
	}
	if resp.StatusCode == http.StatusOK {
		var answer tokenAnswer
		if err := json.Unmarshal(raw, &answer); err != nil || answer.Secret == "" {
			return Credential{}, fmt.Errorf("the provider token route answered no credential")
		}
		cred := Credential{Provider: answer.Provider, Kind: answer.Kind, Secret: NewSecret(answer.Secret)}
		if answer.ExpiresAt != "" {
			if cred.ExpiresAt, err = time.Parse(time.RFC3339, answer.ExpiresAt); err != nil {
				return Credential{}, fmt.Errorf("the provider token route answered a bad expiry")
			}
		}
		c.mu.Lock()
		c.last[req.Provider] = cred
		c.mu.Unlock()
		return cred, nil
	}
	var refusal tokenRefusal
	_ = json.Unmarshal(raw, &refusal)
	var refused error
	switch refusal.Error {
	case RefusalUnauthorized:
		// A successor daemon that has not restored the session's bearer
		// yet answers this mid-swap, so it is no verdict on the credential:
		// keep the last one (Decision 6) and say unauthorized only when
		// there is none.
		if cred, ok := c.kept(req.Provider); ok {
			return cred, nil
		}
		return Credential{}, ErrUnauthorized
	case RefusalBadRequest:
		return Credential{}, fmt.Errorf("the provider token route refused the request: %s", refusal.Message)
	case RefusalNotPermitted:
		refused = ErrNotPermitted
	case RefusalUnknownProvider:
		refused = &ProviderError{Provider: req.Provider, Err: ErrUnknownProvider}
	case RefusalNotConfigured:
		refused = &ProviderError{Provider: req.Provider, Err: ErrNotConfigured}
	case RefusalConsentRequired:
		refused = &ProviderError{Provider: req.Provider, Err: ErrConsentRequired}
	default:
		// The daemon is up but cannot answer now (draining, a broken
		// credential file): not a verdict on the credential.
		detail := refusal.Message
		if detail == "" {
			detail = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return c.fallback(req.Provider, errors.New(detail))
	}
	c.mu.Lock()
	delete(c.last, req.Provider)
	c.mu.Unlock()
	return Credential{}, refused
}

// fallback answers the last credential while the route is unavailable.
func (c *RouteClient) fallback(provider ProviderID, cause error) (Credential, error) {
	if cred, ok := c.kept(provider); ok {
		return cred, nil
	}
	return Credential{}, fmt.Errorf("%w: %v", ErrDaemonUnavailable, cause)
}

// kept is the last credential the route gave for provider, unless it has
// expired.
func (c *RouteClient) kept(provider ProviderID) (Credential, bool) {
	c.mu.Lock()
	cred, ok := c.last[provider]
	c.mu.Unlock()
	if ok && (cred.ExpiresAt.IsZero() || c.now().Before(cred.ExpiresAt)) {
		return cred, true
	}
	return Credential{}, false
}

// String and GoString keep a printed client from showing its session token
// (fmt prints unexported fields raw).
func (c *RouteClient) String() string   { return "wscred.RouteClient(" + c.url + ")" }
func (c *RouteClient) GoString() string { return c.String() }
