package wsdashboard

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/kang-sw/devenv/internal/wscred"
)

// accountSignIn holds a single browser flow across HTTP requests. The cancel
// function remains set until the flow exits, including during cancellation.
type accountSignIn struct {
	mu     sync.Mutex
	state  accountSignInStatus
	cancel context.CancelFunc
	closed bool
}

type accountSignInStatus struct {
	State   string `json:"state"`
	Attempt string `json:"attempt,omitempty"`
	Reason  string `json:"reason,omitempty"`
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
	var body struct {
		ClientID   string `json:"client_id"`
		Attempt    string `json:"attempt"`
		EnablePlan bool   `json:"enable_plan"`
	}
	if !accountBody(w, r, &body) {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		accountError(w, http.StatusServiceUnavailable, "credential_unavailable")
		return
	}
	if action == "sign-in/cancel" {
		if s.cancel == nil || body.Attempt != s.state.Attempt {
			accountError(w, http.StatusConflict, "sign_in_changed")
			return
		}
		s.state.State = "cancelling"
		s.cancel()
		writeJSON(w, http.StatusAccepted, s.state)
		return
	}
	if s.cancel != nil {
		accountError(w, http.StatusConflict, "sign_in_in_progress")
		return
	}
	if action == "sign-out" {
		if body.ClientID == "" {
			accountError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if err := store.SignOutOpenAI(body.ClientID); err != nil {
			accountStoreError(w, err)
			return
		}
		s.state = accountSignInStatus{State: "idle"}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.state = accountSignInStatus{State: "starting", Attempt: uuid.NewString()}
	go s.run(ctx, store, wscred.OpenAISignIn{ClientID: body.ClientID, EnablePlan: body.EnablePlan}, d.openSystem)
	writeJSON(w, http.StatusAccepted, s.state)
}

func (s *accountSignIn) run(ctx context.Context, store browserCredentialStore, selection wscred.OpenAISignIn, openBrowser func(string) error) {
	openFailed := false
	err := store.SignInOpenAI(ctx, selection, func(target string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if openBrowser == nil || openBrowser(target) != nil {
			openFailed = true
			return errors.New("could not open the browser")
		}
		s.mu.Lock()
		if s.state.State == "starting" {
			s.state.State = "waiting"
		}
		s.mu.Unlock()
		return nil
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case err == nil:
		s.state.State = "succeeded"
	case errors.Is(err, context.Canceled) || ctx.Err() != nil:
		s.state.State = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		s.state.State, s.state.Reason = "failed", "timed_out"
	case openFailed:
		s.state.State, s.state.Reason = "failed", "browser_unavailable"
	default:
		s.state.State, s.state.Reason = "failed", "authorization_failed"
	}
	s.cancel()
	s.cancel = nil
}
