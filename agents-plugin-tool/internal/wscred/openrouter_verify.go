package wscred

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"
)

// OpenRouterVerificationStatus is safe to publish; the key binding stays private.
type OpenRouterVerificationStatus struct {
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
}
type openRouterVerification struct {
	OpenRouterVerificationStatus
	Fingerprint string `json:"fingerprint"`
}

var (
	ErrOpenRouterKeyRejected = errors.New("OpenRouter rejected the API key")
	ErrOpenRouterKeyChanged  = errors.New("the OpenRouter key record changed during verification")
)

func keyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
func (e apiKeyEntry) verificationStatus() OpenRouterVerificationStatus {
	if e.Verification != nil && e.Verification.Fingerprint == keyFingerprint(e.APIKey) {
		v := e.Verification.OpenRouterVerificationStatus
		if v.State == "verified" || v.State == "unverified" || v.State == "rejected" {
			return v
		}
	}
	return OpenRouterVerificationStatus{State: "unverified"}
}

// Verification uses no environment proxy, redirects, or response body. The
// client timeout bounds headers and transport work even without a caller deadline.
var openRouterHTTPTransport = &http.Transport{
	TLSHandshakeTimeout:   5 * time.Second,
	ResponseHeaderTimeout: 5 * time.Second,
	IdleConnTimeout:       30 * time.Second,
	MaxIdleConnsPerHost:   2,
}

func (s *Store) checkOpenRouter(ctx context.Context, key string) OpenRouterVerificationStatus {
	v := OpenRouterVerificationStatus{State: "unverified", Reason: "network"}
	transport := s.openRouterTransport
	if transport == nil {
		transport = openRouterHTTPTransport
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/key", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		v.Reason = "temporary"
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			v.State, v.Reason = "verified", ""
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			v.State, v.Reason = "rejected", "rejected"
		}
	}
	v.CheckedAt = s.now().UTC().Format(time.RFC3339Nano)
	return v
}

// VerifyOpenRouterKey validates locally before any network work. A definitive
// rejection saves nothing; all other outcomes save the key and safe metadata.
// This is additive: Apply retains its offline CLI contract.
func (s *Store) VerifyOpenRouterKey(ctx context.Context, key Secret) (ChangeResult, error) {
	value := trimKey(key.Reveal())
	if err := validateKey(value); err != nil {
		return ChangeResult{}, err
	}
	v := s.checkOpenRouter(ctx, value)
	if v.State == "rejected" {
		return ChangeResult{}, ErrOpenRouterKeyRejected
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareWriteLocked(); err != nil {
		return ChangeResult{}, err
	}
	return s.publishOpenRouter(value, v)
}

// RecheckOpenRouterKey keeps the current key even on definitive rejection.
// Rejection publishes state/reason "rejected" and returns its status with nil
// error. Any intervening key mutation or verification publication makes this
// result stale (including replacement with the same key after removal).
func (s *Store) RecheckOpenRouterKey(ctx context.Context) (ChangeResult, error) {
	s.mu.Lock()
	if err := s.prepareWriteLocked(); err != nil {
		s.mu.Unlock()
		return ChangeResult{}, err
	}
	entry, ok := s.keys[ProviderOpenRouter]
	generation := s.openRouterGeneration
	s.mu.Unlock()
	if !ok {
		return ChangeResult{}, ErrNotConfigured
	}
	v := s.checkOpenRouter(ctx, entry.APIKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareWriteLocked(); err != nil {
		return ChangeResult{}, err
	}
	current, ok := s.keys[ProviderOpenRouter]
	if !ok || current.APIKey != entry.APIKey || generation != s.openRouterGeneration {
		return ChangeResult{}, ErrOpenRouterKeyChanged
	}
	return s.publishOpenRouter(entry.APIKey, v)
}
func (s *Store) publishOpenRouter(key string, v OpenRouterVerificationStatus) (ChangeResult, error) {
	next := s.doc.clone()
	updatedAt := s.now().UTC().Format(time.RFC3339)
	if current, ok := s.keys[ProviderOpenRouter]; ok && current.APIKey == key {
		updatedAt = current.UpdatedAt
	}
	entry := apiKeyEntry{Kind: KindAPIKey, APIKey: key, UpdatedAt: updatedAt,
		Verification: &openRouterVerification{OpenRouterVerificationStatus: v, Fingerprint: keyFingerprint(key)}}
	raw, err := mergeEntry(next.providers[string(ProviderOpenRouter)], entry)
	if err != nil {
		return ChangeResult{}, err
	}
	next.providers[string(ProviderOpenRouter)] = raw
	if err := s.writeLocked(next); err != nil {
		return ChangeResult{}, err
	}
	// Even equal metadata is a newer publication when a frozen clock is used.
	s.openRouterGeneration++
	p, _ := s.lookupProvider(ProviderOpenRouter)
	return ChangeResult{Changed: true, Status: s.statusLocked(p)}, nil
}
