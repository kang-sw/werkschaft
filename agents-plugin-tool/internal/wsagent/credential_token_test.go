package wsagent

import (
	"testing"
)

// testProviderHarness is a test-only backend whose registration admits
// provider credentials, as the builtin harness's will.
const testProviderHarness = "test-provider-harness"

// TestNoCurrentBackendAdmitsProviderCredentials: none of the harnesses ws
// hosts today may obtain provider credentials; each brings its own login.
func TestNoCurrentBackendAdmitsProviderCredentials(t *testing.T) {
	regs := BuiltinBackendRegistrations()
	if len(regs) == 0 {
		t.Fatal("no builtin registrations")
	}
	for name, reg := range regs {
		if reg.ProviderCredentials {
			t.Errorf("backend %s admits provider credentials", name)
		}
	}
}

// TestCredentialTokenFollowsTheRegistrationCapability: the token route
// admits a session only when its backend registration sets
// ProviderCredentials: every current backend is refused, a test-only
// registration that sets it is admitted, and an unknown or voided token
// resolves to nothing. Resolving for credentials is not the harness
// reaching ws-mcp.
func TestCredentialTokenFollowsTheRegistrationCapability(t *testing.T) {
	shortStartWait(t)
	h := newHarness(t, map[string]string{"chat": chatProfile})
	h.runner.Close()
	backends := BuiltinBackendRegistrations()
	backends[testProviderHarness] = BackendRegistration{Backend: h.backend, ProviderCredentials: true}
	h.config.Backends = backends
	h.start()
	r := h.runner
	r.SetEndpoint("http://127.0.0.1:7/")

	for name := range backends {
		s := r.attachMCP("c-"+name, "", nil, "/w", 0, name)
		if s == nil {
			t.Fatalf("%s: no session", name)
		}
		s.Started([]string{"PATH=/bin"}, 4242)
		session, ok := r.ResolveCredentialToken(s.Token)
		if !ok || session.Harness != name || session.Conversation != "c-"+name || session.Lead {
			t.Fatalf("%s: session = %+v, %v", name, session, ok)
		}
		if want := name == testProviderHarness; session.Permitted != want {
			t.Errorf("%s: permitted = %v, want %v", name, session.Permitted, want)
		}
		select {
		case <-s.Connected:
			t.Fatalf("%s: a credential resolution counted as reaching ws-mcp", name)
		default:
		}
	}

	if _, ok := r.ResolveCredentialToken("fixture-unknown-token"); ok {
		t.Fatal("an unknown token resolved")
	}
	if _, ok := r.ResolveCredentialToken(""); ok {
		t.Fatal("an empty token resolved")
	}
	first := r.attachMCP("c-void", "", nil, "/w", 0, testProviderHarness)
	first.Started(nil, 1)
	second := r.attachMCP("c-void", "", nil, "/w", 0, testProviderHarness)
	second.Started(nil, 1)
	if _, ok := r.ResolveCredentialToken(first.Token); ok {
		t.Fatal("a voided token resolved")
	}
	if session, ok := r.ResolveCredentialToken(second.Token); !ok || !session.Permitted {
		t.Fatalf("the reissued token = %+v, %v", session, ok)
	}
}
