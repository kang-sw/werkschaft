package wscred

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Secret is a credential value. Every printed or marshalled form is
// redacted, so a Secret that reaches a log line, an error, a trace, or a
// JSON encoding by mistake shows only Redacted; Reveal is the one way to the
// value.
type Secret struct{ value string }

// Redacted is every printed or marshalled form of a non-empty Secret.
const Redacted = "[redacted]"

// NewSecret wraps value.
func NewSecret(value string) Secret { return Secret{value: value} }

// Reveal returns the value. Call it only where the value must leave: an
// Authorization header, the token route's answer, the credential file.
func (s Secret) Reveal() string { return s.value }

// IsZero reports an empty secret.
func (s Secret) IsZero() bool { return s.value == "" }

func (s Secret) redacted() string {
	if s.value == "" {
		return ""
	}
	return Redacted
}

// String, GoString, and Format redact every fmt verb, %#v and %q included.
func (s Secret) String() string   { return s.redacted() }
func (s Secret) GoString() string { return `wscred.Secret("` + s.redacted() + `")` }
func (s Secret) Format(f fmt.State, verb rune) {
	if verb == 'v' && f.Flag('#') {
		_, _ = f.Write([]byte(s.GoString()))
		return
	}
	_, _ = f.Write([]byte(s.redacted()))
}

// MarshalJSON and MarshalText redact too: a wire form that must carry the
// value builds its own field from Reveal.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + s.redacted() + `"`), nil }
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.redacted()), nil }

// Credential is a provider's current credential.
type Credential struct {
	Provider ProviderID
	Kind     Kind
	Secret   Secret
	// ExpiresAt is when a short-lived token stops working; zero for an API
	// key, which does not expire on its own.
	ExpiresAt time.Time
}

// Request names the credential a caller asks for and who asks. The
// requester fields matter only to a provider whose credential is restricted
// to the user's own agent sessions; an API-key provider ignores them.
type Request struct {
	Provider ProviderID
	// AgentSession marks a request from an agent harness running a session
	// for the signed-in user. The daemon derives it from its in-process
	// session or the authenticated registration on the token route.
	AgentSession bool
	// Background marks that session as one the user does not drive
	// directly (a worker), which a restricted provider admits only with
	// the user's stored consent.
	Background bool
}

// Source is what a consumer asks for credentials: the daemon's Store in the
// daemon, a RouteClient in a harness process.
type Source interface {
	// Credential returns req.Provider's current credential, or an error
	// matching ErrUnknownProvider, ErrNotConfigured, ErrConsentRequired, or
	// (from a RouteClient) ErrNotPermitted or ErrUnauthorized.
	Credential(ctx context.Context, req Request) (Credential, error)
}

// Refusals a Source answers; match them with errors.Is. A provider-specific
// refusal is a *ProviderError wrapping one of the first three.
var (
	ErrUnknownProvider = errors.New("unknown provider")
	ErrNotConfigured   = errors.New("provider credential not configured")
	ErrConsentRequired = errors.New("provider credential needs the user's consent for background use")
	// ErrNotPermitted is the token route's refusal of a session whose
	// harness may not obtain provider credentials.
	ErrNotPermitted = errors.New("this session may not obtain provider credentials")
	// ErrUnauthorized is the token route's refusal of an unknown or void
	// session token.
	ErrUnauthorized = errors.New("the session token is not valid")
)

// ProviderError is a refusal for one provider; Err is ErrUnknownProvider,
// ErrNotConfigured, or ErrConsentRequired. Its message says what to do.
type ProviderError struct {
	Provider ProviderID
	Err      error
}

func (e *ProviderError) Error() string {
	switch {
	case errors.Is(e.Err, ErrNotConfigured):
		return fmt.Sprintf("provider %q has no credential configured; set it with the CLI command \"credential set %s\"", e.Provider, e.Provider)
	case errors.Is(e.Err, ErrConsentRequired):
		return fmt.Sprintf("provider %q may not be used by a background session until the user allows it in the dashboard", e.Provider)
	case errors.Is(e.Err, ErrUnknownProvider):
		return fmt.Sprintf("unknown provider %q", e.Provider)
	}
	return fmt.Sprintf("provider %q: %v", e.Provider, e.Err)
}

func (e *ProviderError) Unwrap() error { return e.Err }
