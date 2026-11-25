package wscred

import (
	"errors"
	"math/rand/v2"
)

// ErrOpenAIAccountChanged means a browser action was prepared for an account
// or prompt that is no longer current.
var ErrOpenAIAccountChanged = errors.New("the OpenAI account or consent prompt changed")

// openAIConsentPrompt lives only in the daemon. Its sequence lets a viewer
// dismiss an observed request without clearing a more recent one.
type openAIConsentPrompt struct {
	sequence uint64
	pending  uint64
	account  string
	wake     chan struct{}
}

// OpenAIConsentPrompt returns the pending request (zero when absent) and a
// channel closed when that state changes. It never reads credential files.
func (s *Store) OpenAIConsentPrompt() (uint64, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openAIConsent.wake == nil {
		s.openAIConsent.wake = make(chan struct{})
	}
	return s.openAIConsent.pending, s.openAIConsent.wake
}

// DismissOpenAIConsentPrompt hides the given request without answering it.
// A subsequent refused use can reopen the prompt.
func (s *Store) DismissOpenAIConsentPrompt(prompt uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prompt != 0 && s.openAIConsent.pending == prompt {
		s.clearOpenAIConsentPromptLocked()
	}
}

// RequestOpenAIBackgroundConsent records an actual background refusal that
// happens before requesting a token (such as worker admission or cron).
func (s *Store) RequestOpenAIBackgroundConsent() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestOpenAIConsentLocked()
}

func (s *Store) requestOpenAIConsentLocked() {
	account := s.openAIAccount
	if s.closed || s.openAIConsent.pending != 0 || !canPromptForOpenAIConsent(account) {
		return
	}
	p := &s.openAIConsent
	// Start each store at a random offset so prompts from a replaced daemon
	// cannot match ordinary stale actions. Keep IDs exact in JavaScript.
	if p.sequence == 0 || p.sequence == (1<<53)-1 {
		p.sequence = rand.Uint64() >> 12
	}
	p.sequence++
	p.pending, p.account = p.sequence, account.ClientID
	p.notify()
}

func canPromptForOpenAIConsent(a openAIAccount) bool {
	return a.ClientID != "" && a.IDToken != "" && a.planEnabled() &&
		a.AccessToken != "" && a.RefreshToken != "" && a.Consent == nil
}

func (s *Store) syncOpenAIConsentPromptLocked() {
	if p := s.openAIConsent; p.pending != 0 &&
		(p.account != s.openAIAccount.ClientID || !canPromptForOpenAIConsent(s.openAIAccount)) {
		s.clearOpenAIConsentPromptLocked()
	}
}

func (s *Store) clearOpenAIConsentPromptLocked() {
	p := &s.openAIConsent
	p.pending, p.account = 0, ""
	p.notify()
}

func (p *openAIConsentPrompt) notify() {
	if p.wake != nil {
		close(p.wake)
		p.wake = nil
	}
}

// SetActiveOpenAIBackgroundConsent applies a dashboard answer to the active
// registration, conditional on the displayed account ID or banner prompt.
func (s *Store) SetActiveOpenAIBackgroundConsent(clientID string, prompt uint64, allowed bool) error {
	return s.setOpenAIBackgroundConsent(clientID, prompt, true, allowed)
}
