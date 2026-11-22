package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/kang-sw/devenv/internal/wscred"
)

// credential_routes.go is the daemon's side of the provider authentication
// layer (wscred). The daemon owns the one live credential Store and is the
// credential file's only writer while it runs:
//
//   - The token route (wscred.TokenRoutePath) answers a hosted harness
//     session's credential request. Its bearer is the session's per-session
//     token, and only a session whose backend registration admits provider
//     credentials passes (AgentCredentialHost); every other session, a
//     browser, and a non-loopback peer are refused.
//   - The change route (credentialChangePath) takes a set or remove from a
//     CLI run outside the daemon, authenticated like /shutdown by the
//     endpoint file's token (machineAuth). ApplyCredentialChange is that
//     CLI's side: with no daemon running it writes the file itself, holding
//     the daemon's single-instance lock so no daemon starts mid-write.
//
// Neither route logs, and neither echoes a credential or a token in a
// refusal.

// credentialChangePath is the change route.
const credentialChangePath = "/auth/provider/change"

// credentialStore is the daemon's view of its credential store
// (*wscred.Store).
type credentialStore interface {
	wscred.Source
	Apply(wscred.Change) (wscred.ChangeResult, error)
}

// AgentCredentialHost is an AgentHost whose hosted sessions may ask the
// token route for provider credentials.
type AgentCredentialHost interface {
	// ResolveCredentialToken resolves a live per-session token for the
	// token route. ok is false for an unknown or void token; an ok session
	// still passes only when Permitted.
	ResolveCredentialToken(token string) (session CredentialSession, ok bool)
}

// CredentialSession is the session a token route bearer names.
type CredentialSession struct {
	// Conversation is the session's reply-id, Harness its backend.
	Conversation, Harness string
	// Lead marks a hosted lead session, one the user drives directly; any
	// other session is background use of a credential.
	Lead bool
	// Permitted reports that the session's backend registration admits
	// provider credentials.
	Permitted bool
}

// handleProviderToken is the token route.
func (d *Daemon) handleProviderToken(w http.ResponseWriter, r *http.Request) {
	if !loopbackPeer(r.RemoteAddr) || len(r.Header.Values("Origin")) != 0 {
		wscred.RefuseToken(w, http.StatusForbidden, wscred.RefusalNotPermitted, "", "the token route serves harness processes on this machine only")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	host, _ := d.agents.(AgentCredentialHost)
	token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if host == nil || !bearer || token == "" {
		wscred.RefuseToken(w, http.StatusUnauthorized, wscred.RefusalUnauthorized, "", "no valid session token")
		return
	}
	session, ok := host.ResolveCredentialToken(token)
	if !ok {
		wscred.RefuseToken(w, http.StatusUnauthorized, wscred.RefusalUnauthorized, "", "no valid session token")
		return
	}
	if !session.Permitted {
		wscred.RefuseToken(w, http.StatusForbidden, wscred.RefusalNotPermitted, "", "this session's harness may not obtain provider credentials")
		return
	}
	if d.credentials == nil {
		wscred.RefuseToken(w, http.StatusServiceUnavailable, wscred.RefusalUnavailable, "", "this daemon holds no provider credentials")
		return
	}
	wscred.ServeToken(w, r, d.credentials, wscred.Request{AgentSession: true, Background: !session.Lead})
}

// credentialChangeBody is the change route's request: the one wire form
// that carries a new key, built from Secret.Reveal by its sender.
type credentialChangeBody struct {
	Op       wscred.ChangeOp   `json:"op"`
	Provider wscred.ProviderID `json:"provider"`
	APIKey   string            `json:"api_key,omitempty"`
}

type credentialChangeRefusal struct {
	Error string `json:"error"`
}

// handleProviderChange is the change route.
func (d *Daemon) handleProviderChange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !d.machineAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if d.credentials == nil {
		writeJSON(w, http.StatusServiceUnavailable, credentialChangeRefusal{Error: "this daemon holds no provider credentials"})
		return
	}
	var body credentialChangeBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, credentialChangeRefusal{Error: "the change is not valid JSON"})
		return
	}
	result, err := d.credentials.Apply(wscred.Change{Op: body.Op, Provider: body.Provider, APIKey: wscred.NewSecret(body.APIKey)})
	if err != nil {
		// Apply's errors never quote the key.
		writeJSON(w, http.StatusUnprocessableEntity, credentialChangeRefusal{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Who served a credential change (ApplyCredentialChange).
const (
	CredentialServedByDaemon = "daemon"
	CredentialServedByFile   = "file"
)

// credentialChangeWait bounds how long ApplyCredentialChange keeps trying
// while the lock is held but no daemon answers yet: a daemon starting (it
// takes the lock before it writes its endpoint) or one draining for its
// successor, both shorter than a lock wait.
var credentialChangeWait = DefaultLockWait + time.Second

// beforeOfflineCredentialWrite runs while ApplyCredentialChange holds the
// lock for an offline write, before it writes; tests observe the lock.
var beforeOfflineCredentialWrite func()

// errRetryCredentialChange is a forwarded change the daemon did not take
// for a reason that passes (no answer, a stale endpoint, a drain).
var errRetryCredentialChange = errors.New("the dashboard daemon did not take the change")

// ApplyCredentialChange applies change from a CLI run outside the daemon,
// keeping the daemon the file's only writer: when the daemon's lock in dir
// is free it takes the lock, applies the change to the credential file at
// credPath itself, and releases the lock; when a daemon holds the lock it
// sends the change to that daemon's change route. served is
// CredentialServedByFile or CredentialServedByDaemon.
func ApplyCredentialChange(ctx context.Context, dir, credPath string, change wscred.Change) (result wscred.ChangeResult, served string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return wscred.ChangeResult{}, "", err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(credentialChangeWait)
	lastErr := errRetryCredentialChange
	for {
		lock := flock.New(filepath.Join(dir, lockFileName))
		locked, err := lock.TryLock()
		if err != nil {
			return wscred.ChangeResult{}, "", fmt.Errorf("dashboard lock: %w", err)
		}
		if locked {
			result, err := applyOfflineCredentialChange(lock, credPath, change)
			return result, CredentialServedByFile, err
		}
		_ = lock.Close()
		if ep, err := ReadEndpoint(dir); err == nil {
			result, err := sendCredentialChange(ctx, client, ep, change)
			if !errors.Is(err, errRetryCredentialChange) {
				return result, CredentialServedByDaemon, err
			}
			lastErr = err
		}
		if time.Now().After(deadline) {
			return wscred.ChangeResult{}, "", fmt.Errorf("%w: a dashboard daemon holds the lock but did not answer", lastErr)
		}
		select {
		case <-ctx.Done():
			return wscred.ChangeResult{}, "", ctx.Err()
		case <-time.After(lockRetryDelay):
		}
	}
}

func applyOfflineCredentialChange(lock *flock.Flock, credPath string, change wscred.Change) (wscred.ChangeResult, error) {
	defer func() { _ = lock.Unlock() }()
	if beforeOfflineCredentialWrite != nil {
		beforeOfflineCredentialWrite()
	}
	return wscred.NewStore(credPath).Apply(change)
}

// sendCredentialChange sends change to the daemon ep names. A refusal that
// passes wraps errRetryCredentialChange.
func sendCredentialChange(ctx context.Context, client *http.Client, ep Endpoint, change wscred.Change) (wscred.ChangeResult, error) {
	if ep.Port <= 0 {
		return wscred.ChangeResult{}, errRetryCredentialChange
	}
	raw, err := json.Marshal(credentialChangeBody{Op: change.Op, Provider: change.Provider, APIKey: change.APIKey.Reveal()})
	if err != nil {
		return wscred.ChangeResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(ep, credentialChangePath), bytes.NewReader(raw))
	if err != nil {
		return wscred.ChangeResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return wscred.ChangeResult{}, ctx.Err()
		}
		return wscred.ChangeResult{}, fmt.Errorf("%w (%v)", errRetryCredentialChange, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case http.StatusOK:
		var result wscred.ChangeResult
		if err := json.Unmarshal(body, &result); err != nil {
			return wscred.ChangeResult{}, fmt.Errorf("the dashboard daemon answered the change unreadably")
		}
		return result, nil
	case http.StatusUnauthorized:
		// A daemon without the route answers its access guard's
		// login_required: the path is not one of its machine routes.
		var refusal credentialChangeRefusal
		if json.Unmarshal(body, &refusal) != nil || refusal.Error != "login_required" {
			break
		}
		fallthrough
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return wscred.ChangeResult{}, errors.New("the running dashboard daemon does not take credential changes; update it, or stop it and run this command again")
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		var refusal credentialChangeRefusal
		if json.Unmarshal(body, &refusal) == nil && refusal.Error != "" {
			return wscred.ChangeResult{}, errors.New(refusal.Error)
		}
	}
	// 401 is a stale endpoint file (a successor not yet written), 503 a
	// drain or a missing store: both pass or reach the deadline.
	return wscred.ChangeResult{}, fmt.Errorf("%w (status %d)", errRetryCredentialChange, resp.StatusCode)
}
