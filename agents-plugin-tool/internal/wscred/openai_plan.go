package wscred

import (
	"context"
	"errors"
)

// ErrOpenAIPlanPaused points to the recovery for the provider's plan limit.
var ErrOpenAIPlanPaused = errors.New("ChatGPT plan usage is paused. Review https://chatgpt.com/settings/usage, then send a message to try again")

type OpenAIPlanOutcome int

const (
	OpenAIPlanUnchanged OpenAIPlanOutcome = iota
	OpenAIPlanSucceeded
	OpenAIPlanLimitExceeded
)

// OpenAIPlanRequest holds the credential for one Responses request. Finish
// releases its probe (if any) and updates the pause for that registration.
type OpenAIPlanRequest struct {
	Credential Credential
	Probe      bool
	Finish     func(OpenAIPlanOutcome)
}

type openAIPlanPause struct {
	probing bool
}

// BeginOpenAIPlanRequest admits a plan-backed inference request for the active
// registration before any credential refresh. A paused registration admits one
// operator probe at a time. The Store lives only in this daemon, as does the pause.
func (s *Store) BeginOpenAIPlanRequest(ctx context.Context, background bool) (OpenAIPlanRequest, error) {
	return s.BeginOpenAIPlanRequestFor(ctx, "", background)
}

// BeginOpenAIPlanRequestFor admits a plan-backed inference request for a
// specific registered clientID (or the active account when clientID is empty),
// allowing a turn-pinned request to finish on its original account after an
// account switch (D7).
func (s *Store) BeginOpenAIPlanRequestFor(ctx context.Context, clientID string, background bool) (OpenAIPlanRequest, error) {
	s.mu.Lock()
	if err := s.loadLocked(false); err != nil {
		s.mu.Unlock()
		return OpenAIPlanRequest{}, err
	}
	acct := s.openAIAccount
	if clientID != "" && clientID != acct.ClientID {
		acct = s.doc.openAIState().account(clientID)
	}
	account := acct.ClientID
	if account == "" || acct.IDToken == "" {
		s.mu.Unlock()
		return OpenAIPlanRequest{}, openAIProviderError(ErrNotConfigured)
	}
	pause := s.openAIPlanPauses[account]
	if pause != nil {
		if background || pause.probing {
			s.mu.Unlock()
			return OpenAIPlanRequest{}, ErrOpenAIPlanPaused
		}
		pause.probing = true
	}
	s.mu.Unlock()
	finish := func(outcome OpenAIPlanOutcome) {
		s.mu.Lock()
		defer s.mu.Unlock()
		current := s.openAIPlanPauses[account]
		if pause != nil && current == pause {
			pause.probing = false
			if outcome == OpenAIPlanSucceeded {
				delete(s.openAIPlanPauses, account)
			}
		}
		if outcome == OpenAIPlanLimitExceeded && current == nil && s.doc.openAIState().account(account).ClientID == account {
			if s.openAIPlanPauses == nil {
				s.openAIPlanPauses = make(map[string]*openAIPlanPause)
			}
			s.openAIPlanPauses[account] = &openAIPlanPause{}
		}
	}

	credential, err := s.openAICredential(ctx, Request{
		Provider: ProviderOpenAI, AgentSession: true, Background: background, OpenAIAccount: account,
	})
	if err != nil {
		finish(OpenAIPlanUnchanged)
		return OpenAIPlanRequest{}, err
	}
	return OpenAIPlanRequest{Credential: credential, Probe: pause != nil, Finish: finish}, nil
}
