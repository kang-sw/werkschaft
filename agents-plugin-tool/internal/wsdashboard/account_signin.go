package wsdashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/kang-sw/devenv/internal/wscred"
)

// accountSignIn holds a single OpenAI SIWC flow across HTTP requests. The
// cancel function remains set until the flow exits, including during
// cancellation.
type accountSignIn struct {
	mu       sync.Mutex
	state    accountSignInStatus
	cancel   context.CancelFunc
	complete func(string) error
	closed   bool
}

type accountSignInStatus struct {
	State        string `json:"state"`
	Attempt      string `json:"attempt,omitempty"`
	Reason       string `json:"reason,omitempty"`
	AuthorizeURL string `json:"authorize_url,omitempty"`
	Dynamic      bool   `json:"dynamic,omitempty"`
	BrowserError bool   `json:"browser_error,omitempty"`
}

func (s *accountSignIn) status() accountSignInStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.State == "" {
		return accountSignInStatus{State: "idle"}
	}
	return s.state
}

func (s *accountSignIn) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.complete = nil
	if s.cancel != nil {
		s.cancel()
	}
}

func (d *Daemon) handleAccountSignIn(w http.ResponseWriter, r *http.Request, store browserCredentialStore, action string) {
	s := &d.accountSignIn
	if action == "sign-in/status" {
		writeJSON(w, http.StatusOK, s.status())
		return
	}
	if action == "sign-in/complete" {
		d.handleAccountSignInComplete(w, r)
		return
	}
	var body struct {
		ClientID   string `json:"client_id"`
		NewAccount bool   `json:"new_account"`
		Attempt    string `json:"attempt"`
		EnablePlan bool   `json:"enable_plan"`
		CopyOnly   bool   `json:"copy_only"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	if body.NewAccount && body.ClientID != "" {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "credential_unavailable")
		return
	}
	if action == "sign-in/cancel" {
		if s.cancel == nil || body.Attempt != s.state.Attempt {
			s.mu.Unlock()
			accountError(w, http.StatusConflict, "sign_in_changed")
			return
		}
		s.state.State = "cancelling"
		s.complete = nil
		s.cancel()
		resp := s.state
		s.mu.Unlock()
		writeJSON(w, http.StatusAccepted, resp)
		return
	}
	if s.cancel != nil {
		s.mu.Unlock()
		accountError(w, http.StatusConflict, "sign_in_in_progress")
		return
	}
	if action == "sign-out" {
		if body.ClientID == "" {
			s.mu.Unlock()
			accountError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		err := store.SignOutOpenAI(body.ClientID)
		if err != nil {
			s.mu.Unlock()
			accountStoreError(w, err)
			return
		}
		s.state = accountSignInStatus{State: "idle"}
		s.complete = nil
		s.mu.Unlock()
		d.accountChanged()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	attempt := uuid.NewString()
	s.cancel = cancel
	s.complete = nil
	s.state = accountSignInStatus{State: "starting", Attempt: attempt}
	resp := s.state
	s.mu.Unlock()

	openFn := d.openSystem
	if body.CopyOnly {
		openFn = nil
	}
	go s.run(ctx, attempt, store, wscred.OpenAISignIn{
		ClientID:   body.ClientID,
		NewAccount: body.NewAccount,
		EnablePlan: body.EnablePlan,
	}, openFn, d.accountChanged)
	writeJSON(w, http.StatusAccepted, resp)
}

func (d *Daemon) handleAccountSignInComplete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Attempt string `json:"attempt"`
		Input   string `json:"input"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	input := strings.TrimSpace(body.Input)
	if body.Attempt == "" || input == "" || len(input) > 8192 {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s := &d.accountSignIn
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "credential_unavailable")
		return
	}
	if s.cancel == nil || body.Attempt != s.state.Attempt || s.complete == nil {
		s.mu.Unlock()
		accountError(w, http.StatusConflict, "sign_in_changed")
		return
	}
	complete := s.complete
	s.mu.Unlock()

	// Never log or echo raw callback URLs, codes, or tokens.
	if err := complete(input); err != nil {
		switch {
		case errors.Is(err, wscred.ErrSignInStateMismatch):
			accountError(w, http.StatusUnprocessableEntity, "sign_in_state_mismatch")
		case errors.Is(err, wscred.ErrSignInClientIDMissing):
			accountError(w, http.StatusUnprocessableEntity, "sign_in_client_id_missing")
		case errors.Is(err, wscred.ErrSignInAlreadyCompleted):
			accountError(w, http.StatusConflict, "sign_in_already_completed")
		default:
			accountError(w, http.StatusUnprocessableEntity, "sign_in_invalid_callback")
		}
		return
	}
	writeJSON(w, http.StatusAccepted, s.status())
}

func (s *accountSignIn) run(ctx context.Context, attempt string, store browserCredentialStore, selection wscred.OpenAISignIn, openBrowser func(string) error, onSuccess func()) {
	cb := wscred.OpenAISignInCallbacks{
		IgnoreBrowserError: true,
		OnReady: func(info wscred.OpenAISignInReady) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.state.Attempt != attempt {
				return
			}
			s.state.AuthorizeURL = info.AuthorizeURL
			s.state.Dynamic = info.Dynamic
			s.complete = info.Complete
			if s.state.State == "starting" {
				s.state.State = "waiting"
			}
		},
	}
	if openBrowser != nil {
		cb.OpenBrowser = func(target string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			err := openBrowser(target)
			s.mu.Lock()
			if s.state.Attempt == attempt {
				if err != nil {
					s.state.BrowserError = true
				}
				if s.state.State == "starting" {
					s.state.State = "waiting"
				}
			}
			s.mu.Unlock()
			return err
		}
	}

	err := store.SignInOpenAIWithCallbacks(ctx, selection, cb)

	succeeded := false
	s.mu.Lock()
	// Always clear copyable authorize_url and manual callback hook on terminal
	// transitions so completed/failed status payloads never retain OAuth URLs.
	s.state.AuthorizeURL = ""
	s.state.Dynamic = false
	s.state.BrowserError = false
	s.complete = nil
	switch {
	case err == nil:
		s.state.State = "succeeded"
		s.state.Reason = ""
		succeeded = true
	case errors.Is(err, context.Canceled) || ctx.Err() != nil:
		s.state.State = "cancelled"
		s.state.Reason = ""
	case errors.Is(err, context.DeadlineExceeded):
		s.state.State, s.state.Reason = "failed", "timed_out"
	default:
		s.state.State, s.state.Reason = "failed", "authorization_failed"
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.mu.Unlock()
	if succeeded && onSuccess != nil {
		onSuccess()
	}
}
