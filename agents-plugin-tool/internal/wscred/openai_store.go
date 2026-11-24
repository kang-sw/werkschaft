package wscred

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// openAIAccount is the private file representation of a validated OpenAI
// registration. The issued client ID distinguishes workspaces, including
// workspaces with the same email or subject.
type openAIAccount struct {
	ClientID     string    `json:"client_id"`
	Issuer       string    `json:"issuer"`
	Subject      string    `json:"subject"`
	Email        string    `json:"email"`
	IDToken      string    `json:"id_token"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Scopes       []string  `json:"scopes"`
	ExpiresAt    time.Time `json:"expires_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Consent      *bool     `json:"background_consent"`
}

func (a openAIAccount) planEnabled() bool { return slices.Contains(a.Scopes, openAIPlanScope) }

type openAIState struct {
	Kind     Kind                       `json:"kind"`
	HostID   string                     `json:"host_id"`
	Active   string                     `json:"active"`
	Accounts map[string]json.RawMessage `json:"accounts"`
}

func (d document) openAIState() openAIState {
	state := openAIState{Kind: KindSIWC, Accounts: map[string]json.RawMessage{}}
	if raw, found := d.providers[string(ProviderOpenAI)]; found {
		if json.Unmarshal(raw, &state) != nil || state.Kind != KindSIWC {
			return openAIState{Kind: KindSIWC, Accounts: map[string]json.RawMessage{}}
		}
		if state.Accounts == nil {
			state.Accounts = map[string]json.RawMessage{}
		}
	}
	return state
}

func (s openAIState) account(id string) openAIAccount {
	var a openAIAccount
	if id == "" || json.Unmarshal(s.Accounts[id], &a) != nil || a.ClientID != id || a.Issuer == "" || a.Subject == "" {
		return openAIAccount{}
	}
	return a
}

// OpenAIAccountStatus contains account-picker metadata, without credentials.
type OpenAIAccountStatus struct {
	ClientID          string `json:"client_id"`
	Email             string `json:"email,omitempty"`
	Active            bool   `json:"active"`
	SignedIn          bool   `json:"signed_in"`
	PlanEnabled       bool   `json:"plan_enabled"`
	BackgroundConsent *bool  `json:"background_consent,omitempty"`
}

func (s openAIState) status() []OpenAIAccountStatus {
	accounts := make([]OpenAIAccountStatus, 0, len(s.Accounts))
	for id := range s.Accounts {
		a := s.account(id)
		if a.ClientID != "" {
			accounts = append(accounts, OpenAIAccountStatus{
				ClientID: id, Email: a.Email, Active: id == s.Active,
				SignedIn: a.IDToken != "", PlanEnabled: a.planEnabled(),
				BackgroundConsent: a.Consent,
			})
		}
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ClientID < accounts[j].ClientID })
	return accounts
}

// OpenAISignIn selects a saved registration or a new registration. With both
// ClientID and NewAccount unset, the active registration is used if present.
type OpenAISignIn struct {
	ClientID   string
	NewAccount bool
	EnablePlan bool
}

// SignInOpenAI runs the browser flow and activates the account only after
// validating its identity. openBrowser launches the system browser on the
// local entry URL. The store must be the file's sole writer.
func (s *Store) SignInOpenAI(ctx context.Context, selection OpenAISignIn, openBrowser func(string) error) error {
	if openBrowser == nil || (selection.NewAccount && selection.ClientID != "") {
		return errors.New("invalid OpenAI sign-in selection")
	}
	ctx, cancel := context.WithTimeout(ctx, openAISignInWait)
	defer cancel()
	hostID, previous, err := s.prepareOpenAISignIn(selection)
	if err != nil {
		return err
	}
	clientID, identity, tokens, err := s.openAI.authorize(ctx, hostID, previous, selection.EnablePlan, openBrowser, s.now)
	if err != nil {
		return err
	}
	account := openAIAccount{ClientID: clientID, Issuer: identity.Issuer, Subject: identity.Subject, IDToken: tokens.IDToken}
	if identity.EmailVerified {
		account.Email = identity.Email
	}
	account, err = account.withGrant(tokens, s.now())
	if err != nil {
		return err
	}
	return s.finishOpenAISignIn(previous, account)
}

func (s *Store) prepareOpenAISignIn(selection OpenAISignIn) (string, openAIAccount, error) {
	s.openAIMu.Lock()
	defer s.openAIMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareWriteLocked(); err != nil {
		return "", openAIAccount{}, err
	}
	if err := s.flushOpenAIRefreshLocked(); err != nil {
		return "", openAIAccount{}, err
	}
	state := s.doc.openAIState()
	id := selection.ClientID
	if id == "" && !selection.NewAccount {
		id = state.Active
	}
	account := state.account(id)
	if id != "" && account.ClientID == "" {
		return "", openAIAccount{}, errors.New("the selected OpenAI registration is unavailable")
	}
	if state.HostID == "" {
		state.HostID = "urn:uuid:" + uuid.NewString()
		if err := s.writeOpenAIStateLocked(state); err != nil {
			return "", openAIAccount{}, err
		}
	}
	return state.HostID, account, nil
}

func (s *Store) finishOpenAISignIn(previous, account openAIAccount) error {
	s.openAIMu.Lock()
	defer s.openAIMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareWriteLocked(); err != nil {
		return err
	}
	if err := s.flushOpenAIRefreshLocked(); err != nil {
		return err
	}
	state := s.doc.openAIState()
	current := state.account(account.ClientID)
	if previous.ClientID != "" && previous.ClientID != account.ClientID ||
		current.ClientID != "" && (current.Subject != account.Subject || current.Issuer != account.Issuer) {
		return errors.New("the signed-in OpenAI account does not match the selected registration")
	}
	account.Consent = current.Consent
	state.Active = account.ClientID
	return s.writeOpenAIAccountLocked(state, account)
}

// SetOpenAIBackgroundConsent records the user's explicit answer for the
// selected registration. A false answer also revokes a previous grant.
func (s *Store) SetOpenAIBackgroundConsent(clientID string, allowed bool) error {
	s.openAIMu.Lock()
	defer s.openAIMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareWriteLocked(); err != nil {
		return err
	}
	if err := s.flushOpenAIRefreshLocked(); err != nil {
		return err
	}
	state := s.doc.openAIState()
	if clientID == "" {
		clientID = state.Active
	}
	a := state.account(clientID)
	if a.ClientID == "" {
		return openAIProviderError(ErrNotConfigured)
	}
	if a.Consent != nil && *a.Consent == allowed {
		return nil
	}
	a.Consent = &allowed
	return s.writeOpenAIAccountLocked(state, a)
}

func openAIProviderError(err error) error {
	return &ProviderError{Provider: ProviderOpenAI, Err: err}
}

func (a openAIAccount) withGrant(tokens openAITokens, now time.Time) (openAIAccount, error) {
	a.Scopes = strings.Fields(tokens.Scope)
	a.UpdatedAt = now.UTC()
	if !a.planEnabled() {
		return a.withoutAccess(), nil
	}
	const maxExpirySeconds = int64((24 * time.Hour) / time.Second)
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || !strings.EqualFold(tokens.TokenType, "Bearer") ||
		tokens.ExpiresIn <= 0 || tokens.ExpiresIn > maxExpirySeconds {
		return openAIAccount{}, errors.New("OpenAI returned an incomplete plan credential")
	}
	a.AccessToken, a.RefreshToken = tokens.AccessToken, tokens.RefreshToken
	a.ExpiresAt = now.UTC().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	return a, nil
}

func (a openAIAccount) withoutAccess() openAIAccount {
	a.AccessToken, a.RefreshToken, a.ExpiresAt = "", "", time.Time{}
	return a
}

func (s *Store) openAICredential(ctx context.Context, req Request) (Credential, error) {
	if !req.AgentSession {
		return Credential{}, openAIProviderError(ErrNotPermitted)
	}
	s.mu.Lock()
	cred, _, refresh, err := s.openAICurrentLocked(req)
	s.mu.Unlock()
	if err != nil || !refresh {
		return cred, err
	}

	// Only the refresh path needs the second lock. It serializes rotations
	// and other OpenAI writes without making ordinary API-key reads wait on
	// OpenAI's network request.
	s.openAIMu.Lock()
	defer s.openAIMu.Unlock()
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return Credential{}, err
	}
	if err := s.flushOpenAIRefreshLocked(); err != nil {
		s.mu.Unlock()
		return Credential{}, err
	}
	cred, account, refresh, err := s.openAICurrentLocked(req)
	if err == nil && refresh {
		err = s.prepareWriteLocked()
	}
	s.mu.Unlock()
	if err != nil || !refresh {
		return cred, err
	}
	return s.refreshOpenAI(ctx, account)
}

func (s *Store) openAICurrentLocked(req Request) (Credential, openAIAccount, bool, error) {
	if err := s.loadLocked(false); err != nil {
		return Credential{}, openAIAccount{}, false, err
	}
	account := s.openAIAccount
	switch {
	case account.ClientID == "" || account.IDToken == "" || (req.openAIAccount != "" && req.openAIAccount != account.ClientID):
		return Credential{}, openAIAccount{}, false, openAIProviderError(ErrNotConfigured)
	case !account.planEnabled():
		return Credential{}, openAIAccount{}, false, openAIProviderError(ErrOpenAIPlanDisabled)
	case req.Background && (account.Consent == nil || !*account.Consent):
		return Credential{}, openAIAccount{}, false, openAIProviderError(ErrConsentRequired)
	case account.AccessToken == "" || account.RefreshToken == "":
		return Credential{}, openAIAccount{}, false, openAIProviderError(ErrNotConfigured)
	}
	refresh := s.pendingOpenAIRefresh != nil || !s.now().Add(30*time.Second).Before(account.ExpiresAt)
	if refresh {
		return Credential{}, account, true, nil
	}
	return account.credential(), account, false, nil
}

func (a openAIAccount) credential() Credential {
	return Credential{Provider: ProviderOpenAI, Kind: KindSIWC, Secret: NewSecret(a.AccessToken), ExpiresAt: a.ExpiresAt}
}

func (s *Store) refreshOpenAI(ctx context.Context, account openAIAccount) (Credential, error) {
	tokens, err := s.openAI.grant(ctx, url.Values{
		"grant_type": {"refresh_token"}, "client_id": {account.ClientID}, "refresh_token": {account.RefreshToken},
	})
	if err != nil {
		var grant *openAIGrantError
		if !errors.As(err, &grant) || !grant.unusableRefresh() {
			return Credential{}, err
		}
		next := account.withoutAccess()
		next.IDToken, next.Scopes, next.UpdatedAt = "", nil, s.now().UTC()
		if err := s.persistOpenAIRefresh(next); err != nil {
			return Credential{}, err
		}
		return Credential{}, openAIProviderError(ErrNotConfigured)
	}
	// Keep the ID token verified at sign-in for reauthorization. Refresh
	// rotates the access grant, without changing the saved account identity.
	if tokens.Scope == "" {
		// OAuth allows the server to omit the scope when it is unchanged.
		tokens.Scope = strings.Join(account.Scopes, " ")
	}
	next, err := account.withGrant(tokens, s.now())
	if err != nil {
		return Credential{}, err
	}
	if err := s.persistOpenAIRefresh(next); err != nil {
		return Credential{}, err
	}
	if !next.planEnabled() {
		return Credential{}, openAIProviderError(ErrOpenAIPlanDisabled)
	}
	return next.credential(), nil
}

func (s *Store) persistOpenAIRefresh(account openAIAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Once the provider rotated the old refresh token, retry the local
	// write on failure. A subsequent caller must not grant with the old token.
	s.pendingOpenAIRefresh = &account
	return s.flushOpenAIRefreshLocked()
}

func (s *Store) flushOpenAIRefreshLocked() error {
	if s.pendingOpenAIRefresh == nil {
		return nil
	}
	if err := s.prepareWriteLocked(); err != nil {
		return err
	}
	if err := s.writeOpenAIAccountLocked(s.doc.openAIState(), *s.pendingOpenAIRefresh); err != nil {
		return err
	}
	s.pendingOpenAIRefresh = nil
	return nil
}

func (s *Store) writeOpenAIAccountLocked(state openAIState, account openAIAccount) error {
	raw, err := mergeEntry(state.Accounts[account.ClientID], account)
	if err != nil {
		return err
	}
	state.Accounts[account.ClientID] = raw
	return s.writeOpenAIStateLocked(state)
}

func (s *Store) writeOpenAIStateLocked(state openAIState) error {
	next := s.doc.clone()
	id := string(ProviderOpenAI)
	raw, err := mergeEntry(next.providers[id], state)
	if err != nil {
		return err
	}
	next.providers[id] = raw
	return s.writeLocked(next)
}

// OpenAIModel is one model in the account catalog, in the provider's order.
type OpenAIModel struct {
	Slug          string `json:"slug"`
	DisplayName   string `json:"display_name"`
	ContextWindow int64  `json:"context_window,omitempty"`
}

// OpenAIModels returns the account's displayable models. The same session
// and consent rules apply to the catalog and to credentials for inference.
func (s *Store) OpenAIModels(ctx context.Context, req Request) ([]OpenAIModel, error) {
	req.Provider = ProviderOpenAI
	credential, err := s.openAICredential(ctx, req)
	if err != nil {
		return nil, err
	}
	resp, err := s.openAI.request(ctx, http.MethodGet, s.openAI.api+"/models", nil, credential.Secret)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, errors.New("OpenAI rejected the model catalog credential; sign in with ChatGPT again")
		}
		return nil, fmt.Errorf("OpenAI model catalog request failed (HTTP %d)", resp.StatusCode)
	}
	var catalog struct {
		Models []struct {
			OpenAIModel
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if err := readOpenAIJSON(resp.Body, &catalog); err != nil {
		return nil, err
	}
	if catalog.Models == nil {
		return nil, errors.New("OpenAI returned no model catalog")
	}
	models := make([]OpenAIModel, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		if model.Visibility == "list" && model.Slug != "" && model.DisplayName != "" {
			models = append(models, model.OpenAIModel)
		}
	}
	return models, nil
}
