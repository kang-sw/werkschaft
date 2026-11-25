package wscred

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpenAIConsentPromptAndSignOut(t *testing.T) {
	s := testStore(t)
	f := newOpenAIFixture(t, s)
	beforeSignIn := s.ProviderRevision()
	signInFixture(t, s, f, OpenAISignIn{})
	activeRevision := s.ProviderRevision()
	if activeRevision <= beforeSignIn {
		t.Fatal("sign-in did not invalidate the model catalog")
	}
	ctx := context.Background()
	initial, wake := s.OpenAIConsentPrompt()
	if initial != 0 {
		t.Fatal("sign-in alone raised a consent prompt")
	}
	if _, err := s.Credential(ctx, openAIRequest(true)); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("background credential before consent: %v", err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("first refusal did not wake prompt observers")
	}
	first, _ := s.OpenAIConsentPrompt()
	if first == 0 {
		t.Fatal("no prompt after refusal")
	}
	s.RequestOpenAIBackgroundConsent()
	if current, _ := s.OpenAIConsentPrompt(); current != first {
		t.Fatal("repeated requests replaced the visible prompt")
	}

	s.DismissOpenAIConsentPrompt(first)
	if current, _ := s.OpenAIConsentPrompt(); current != 0 {
		t.Fatal("dismiss did not hide the prompt")
	}
	s.RequestOpenAIBackgroundConsent()
	second, _ := s.OpenAIConsentPrompt()
	if second <= first {
		t.Fatal("new refusal did not reopen a dismissed prompt")
	}
	s.DismissOpenAIConsentPrompt(first)
	if err := s.SetActiveOpenAIBackgroundConsent("", first, true); !errors.Is(err, ErrOpenAIAccountChanged) {
		t.Fatalf("stale banner answer: %v", err)
	}
	if err := s.SetActiveOpenAIBackgroundConsent("", second, false); err != nil {
		t.Fatal(err)
	}
	s.RequestOpenAIBackgroundConsent()
	if current, _ := s.OpenAIConsentPrompt(); current != 0 {
		t.Fatal("explicit denial allowed a new prompt")
	}
	if _, err := s.Credential(ctx, openAIRequest(true)); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("background credential after denial: %v", err)
	}
	if err := s.SetActiveOpenAIBackgroundConsent(f.clientID, 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Credential(ctx, openAIRequest(true)); err != nil {
		t.Fatalf("background credential after grant: %v", err)
	}
	if err := s.SignOutOpenAI("stale-fixture-registration"); !errors.Is(err, ErrOpenAIAccountChanged) {
		t.Fatalf("stale sign-out: %v", err)
	}
	if s.ProviderRevision() != activeRevision {
		t.Fatal("consent changed the model catalog revision")
	}
	if err := s.SignOutOpenAI(f.clientID); err != nil {
		t.Fatal(err)
	}
	if s.ProviderRevision() <= activeRevision {
		t.Fatal("sign-out did not invalidate the model catalog")
	}
	next := NewStore(s.Path())
	status, err := next.Status()
	if err != nil || len(status[1].OpenAIAccounts) != 0 || status[1].Configured {
		t.Fatalf("signed-out account survived reopen: %v", err)
	}
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{f.clientID, f.access, f.refresh, f.priorID, "background_consent"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("signed-out registration left private data")
		}
	}
	if _, err := next.Credential(ctx, openAIRequest(false)); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("credential after sign-out: %v", err)
	}
}

func TestOpenAIConsentPromptRejectsPreviousStoreActions(t *testing.T) {
	s := testStore(t)
	f := newOpenAIFixture(t, s)
	signInFixture(t, s, f, OpenAISignIn{})
	s.RequestOpenAIBackgroundConsent()
	previous, _ := s.OpenAIConsentPrompt()
	if previous == 0 {
		t.Fatal("missing prompt in first store")
	}
	s.DismissOpenAIConsentPrompt(previous)

	reopened := NewStore(s.Path())
	if _, err := reopened.Status(); err != nil {
		t.Fatal(err)
	}
	reopened.RequestOpenAIBackgroundConsent()
	current, _ := reopened.OpenAIConsentPrompt()
	if current == 0 || current == previous || current >= 1<<53 {
		t.Fatalf("successor prompt ID %d cannot replace dismissed prompt %d", current, previous)
	}
	reopened.DismissOpenAIConsentPrompt(previous)
	if got, _ := reopened.OpenAIConsentPrompt(); got != current {
		t.Fatal("stale dismiss cleared the successor's prompt")
	}
	if err := reopened.SetActiveOpenAIBackgroundConsent("", previous, true); !errors.Is(err, ErrOpenAIAccountChanged) {
		t.Fatalf("stale answer applied to successor: %v", err)
	}
	if err := reopened.SetActiveOpenAIBackgroundConsent("", current, false); err != nil {
		t.Fatalf("valid successor prompt: %v", err)
	}
}

func TestOpenAIConsentCanBeRevokedAfterRefreshFailure(t *testing.T) {
	s := testStore(t)
	f := newOpenAIFixture(t, s)
	signInFixture(t, s, f, OpenAISignIn{})
	if err := s.SetActiveOpenAIBackgroundConsent(f.clientID, 0, true); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Hour)
	f.revoke = true
	ctx := context.Background()
	if _, err := s.Credential(ctx, openAIRequest(true)); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("revoked refresh: %v", err)
	}
	if err := s.SetActiveOpenAIBackgroundConsent(f.clientID, 0, true); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("grant without usable sign-in: %v", err)
	}
	if err := s.SetActiveOpenAIBackgroundConsent("stale-registration", 0, false); !errors.Is(err, ErrOpenAIAccountChanged) {
		t.Fatalf("revocation for stale account: %v", err)
	}
	if err := s.SetActiveOpenAIBackgroundConsent(f.clientID, 0, false); err != nil {
		t.Fatalf("revoke after lost grant: %v", err)
	}
	reopened := NewStore(s.Path())
	reopened.now, reopened.openAI = s.now, s.openAI
	f.revoke, f.priorID = false, ""
	signInFixture(t, reopened, f, OpenAISignIn{})
	if _, err := reopened.Credential(ctx, openAIRequest(true)); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("sign-in revived revoked consent: %v", err)
	}
}

func TestOpenAICancelledSignInKeepsExistingRegistration(t *testing.T) {
	s := testStore(t)
	f := newOpenAIFixture(t, s)
	signInFixture(t, s, f, OpenAISignIn{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.SignInOpenAI(ctx, OpenAISignIn{NewAccount: true}, func(string) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled sign-in: %v", err)
	}
	status, err := s.Status()
	if err != nil || len(status[1].OpenAIAccounts) != 1 || status[1].OpenAIAccounts[0].ClientID != f.clientID {
		t.Fatalf("cancellation changed active registration: %v", err)
	}
}
