package wsdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/kang-sw/devenv/internal/wscred"
)

const accountPrefix = "/api/account/"

// browserCredentialStore adds the owner dashboard operations to the daemon's
// CLI and harness credential interface.
type browserCredentialStore interface {
	credentialStore
	Status() ([]wscred.ProviderStatus, error)
	VerifyOpenRouterKey(context.Context, wscred.Secret) (wscred.ChangeResult, error)
	RecheckOpenRouterKey(context.Context) (wscred.ChangeResult, error)
	SignInOpenAI(context.Context, wscred.OpenAISignIn, func(string) error) error
	SignOutOpenAI(clientID string) error
	SetActiveOpenAIBackgroundConsent(clientID string, prompt uint64, allowed bool) error
	OpenAIConsentPrompt() (uint64, <-chan struct{})
	DismissOpenAIConsentPrompt(uint64)
}

type accountStatus struct {
	LoopbackPeer  bool                        `json:"loopback_peer"`
	OpenRouter    wscred.ProviderStatus       `json:"openrouter"`
	OpenAI        *wscred.OpenAIAccountStatus `json:"openai"`
	OpenAIReady   bool                        `json:"openai_ready"`
	ConsentPrompt uint64                      `json:"consent_prompt"`
	SignIn        accountSignInStatus         `json:"sign_in"`
	LocalKeys     []wscred.ProviderStatus     `json:"local_keys"`
	VoiceKeys     []wscred.ProviderStatus     `json:"voice_keys"`
}

func accountError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{code})
}

func accountBody(w http.ResponseWriter, r *http.Request, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 72<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(into) != nil || dec.Decode(new(any)) != io.EOF {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func accountStoreError(w http.ResponseWriter, err error) {
	// The request or an upstream error can contain user-supplied data.
	// Every public error here is a fixed code.
	var invalid wscred.InvalidKeyError
	switch {
	case errors.Is(err, wscred.ErrOpenRouterKeyRejected):
		accountError(w, http.StatusUnprocessableEntity, "key_rejected")
	case errors.Is(err, wscred.ErrOpenRouterKeyChanged), errors.Is(err, wscred.ErrOpenAIAccountChanged):
		accountError(w, http.StatusConflict, "credential_changed")
	case errors.Is(err, wscred.ErrNotConfigured):
		accountError(w, http.StatusNotFound, "credential_missing")
	case errors.Is(err, wscred.ErrUnknownProvider):
		accountError(w, http.StatusNotFound, "unknown_provider")
	case errors.As(err, &invalid):
		accountError(w, http.StatusUnprocessableEntity, "invalid_key")
	default:
		accountError(w, http.StatusServiceUnavailable, "credential_unavailable")
	}
}

func (d *Daemon) handleAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !localViewer(r) {
		accountError(w, http.StatusNotFound, "not_found")
		return
	}
	action := r.URL.Path[len(accountPrefix):]
	method := http.MethodPost
	if action == "status" || action == "sign-in/status" {
		method = http.MethodGet
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		accountError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	origins := r.Header.Values("Origin")
	if crossSiteFetch(r) || len(origins) > 1 ||
		((method == http.MethodPost || len(origins) == 1) && !sameOrigin(r)) {
		accountError(w, http.StatusForbidden, "origin_required")
		return
	}
	store, ok := d.credentials.(browserCredentialStore)
	if !ok {
		accountError(w, http.StatusServiceUnavailable, "credential_unavailable")
		return
	}
	switch action {
	case "status":
		d.handleAccountStatus(w, r, store)
	case "key":
		d.handleAccountKey(w, r, store)
	case "endpoints":
		d.handleAccountEndpoint(w, r, store)
	case "consent", "consent/dismiss":
		d.handleAccountConsent(w, r, store, action == "consent/dismiss")
	case "sign-in/start", "sign-in/status", "sign-in/cancel", "sign-out":
		d.handleAccountSignIn(w, r, store, action)
	default:
		accountError(w, http.StatusNotFound, "not_found")
	}
}

func (d *Daemon) handleAccountStatus(w http.ResponseWriter, r *http.Request, store browserCredentialStore) {
	providers, err := store.Status()
	if err != nil {
		accountStoreError(w, err)
		return
	}
	var status accountStatus
	status.LoopbackPeer = loopbackPeer(r.RemoteAddr)
	status.ConsentPrompt, _ = store.OpenAIConsentPrompt()
	status.SignIn = d.accountSignIn.status()
	status.LocalKeys = []wscred.ProviderStatus{}
	status.VoiceKeys = []wscred.ProviderStatus{}
	for _, provider := range providers {
		switch provider.Provider {
		case wscred.ProviderOpenRouter:
			status.OpenRouter = provider
		case wscred.ProviderOpenAI:
			status.OpenAIReady = provider.Configured
			for _, account := range provider.OpenAIAccounts {
				if account.Active {
					status.OpenAI = &account
					break
				}
			}
		default:
			if wscred.IsVoiceOnly(provider.Provider) {
				status.VoiceKeys = append(status.VoiceKeys, provider)
			} else {
				status.LocalKeys = append(status.LocalKeys, provider)
			}
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func (d *Daemon) handleAccountKey(w http.ResponseWriter, r *http.Request, store browserCredentialStore) {
	var body struct {
		Op       string            `json:"op"`
		Provider wscred.ProviderID `json:"provider"`
		Key      string            `json:"key"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	if body.Provider != wscred.ProviderOpenRouter && body.Op != string(wscred.OpRemove) && body.Op != string(wscred.OpSet) {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var result wscred.ChangeResult
	var err error
	switch body.Op {
	case string(wscred.OpSet):
		key := wscred.NewSecret(body.Key)
		if body.Provider == wscred.ProviderOpenRouter {
			result, err = store.VerifyOpenRouterKey(r.Context(), key)
		} else {
			result, err = store.Apply(wscred.Change{Op: wscred.OpSet, Provider: body.Provider, APIKey: key})
		}
	case string(wscred.OpRemove):
		result, err = store.Apply(wscred.Change{Op: wscred.OpRemove, Provider: body.Provider})
	case "recheck":
		result, err = store.RecheckOpenRouterKey(r.Context())
	default:
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err != nil {
		accountStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d *Daemon) handleAccountConsent(w http.ResponseWriter, r *http.Request, store browserCredentialStore, dismiss bool) {
	var body struct {
		ClientID string `json:"client_id"`
		Prompt   uint64 `json:"prompt"`
		Allowed  *bool  `json:"allowed"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	if dismiss && body.Prompt != 0 {
		store.DismissOpenAIConsentPrompt(body.Prompt)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if dismiss || body.Allowed == nil || (body.ClientID == "" && body.Prompt == 0) {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := store.SetActiveOpenAIBackgroundConsent(body.ClientID, body.Prompt, *body.Allowed); err != nil {
		accountStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
