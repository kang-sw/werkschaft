package wscred

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

type routerTransport func(*http.Request) (*http.Response, error)

func (f routerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type unreadBody struct{ t *testing.T }

func (b unreadBody) Read([]byte) (int, error) { b.t.Error("response body read"); return 0, io.EOF }
func (b unreadBody) Close() error             { return nil }
func routerResponse(t *testing.T, code int) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Location": []string{"https://example.invalid/credential"}}, Body: unreadBody{t}}
}
func TestOpenRouterVerificationOutcomes(t *testing.T) {
	for _, tc := range []struct {
		code          int
		state, reason string
		reject        bool
	}{
		{200, "verified", "", false},
		{401, "", "", true},
		{403, "", "", true},
		{429, "unverified", "temporary", false},
		{503, "unverified", "temporary", false},
		{302, "unverified", "temporary", false},
		{0, "unverified", "network", false},
	} {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			s := testStore(t)
			calls := 0
			s.openRouterTransport = routerTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != "https://openrouter.ai/api/v1/key" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+fixtureKey {
					t.Fatal("wrong request")
				}
				if tc.code == 0 {
					return nil, errors.New(fixtureKey)
				}
				return routerResponse(t, tc.code), nil
			})
			result, err := s.VerifyOpenRouterKey(context.Background(), NewSecret(" "+fixtureKey+" "))
			if tc.reject {
				if !errors.Is(err, ErrOpenRouterKeyRejected) {
					t.Fatal(err)
				}
				if _, err := os.Stat(s.path); !os.IsNotExist(err) {
					t.Fatal("rejection wrote file")
				}
			} else {
				if err != nil || result.Status.Verification.State != tc.state ||
					result.Status.Verification.Reason != tc.reason || result.Status.Verification.CheckedAt == "" {
					t.Fatalf("%+v %v", result, err)
				}
				raw, _ := json.Marshal(result)
				if strings.Contains(string(raw), "fingerprint") || strings.Contains(string(raw), fixtureKey) {
					t.Fatal("unsafe result")
				}
				reopened := NewStore(s.path)
				statuses, err := reopened.Status()
				if err != nil || statuses[0].Verification.State != tc.state || statuses[0].Verification.Reason != tc.reason {
					t.Fatalf("%+v %v", statuses, err)
				}
			}
			if calls != 1 {
				t.Fatal("followed redirect or repeated request")
			}
		})
	}
	s := testStore(t)
	s.openRouterTransport = routerTransport(func(*http.Request) (*http.Response, error) { t.Fatal("invalid key sent"); return nil, nil })
	if _, err := s.VerifyOpenRouterKey(context.Background(), NewSecret("bad key")); err == nil {
		t.Fatal("accepted invalid key")
	}
}
func TestOpenRouterRecheckRejectedKeepsKey(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	s.openRouterTransport = routerTransport(func(*http.Request) (*http.Response, error) { return routerResponse(t, 403), nil })
	res, err := s.RecheckOpenRouterKey(context.Background())
	if err != nil || res.Status.Verification.State != "rejected" || res.Status.Verification.Reason != "rejected" {
		t.Fatalf("%+v %v", res, err)
	}
	cred, err := openrouter(s)
	if err != nil || cred.Secret.Reveal() != fixtureKey {
		t.Fatal("lost key")
	}
}
func TestOpenRouterRecheckGeneration(t *testing.T) {
	for _, mutation := range []string{"replacement", "remove-reinsert", "parallel-recheck"} {
		t.Run(mutation, func(t *testing.T) {
			s := testStore(t)
			setKey(t, s, fixtureKey)
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			s.openRouterTransport = routerTransport(func(*http.Request) (*http.Response, error) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
				}
				return routerResponse(t, 200), nil
			})
			done := make(chan error, 1)
			go func() { _, err := s.RecheckOpenRouterKey(context.Background()); done <- err }()
			<-started
			switch mutation {
			case "replacement":
				setKey(t, s, fixtureKey2)
			case "remove-reinsert":
				if _, err := s.Apply(Change{Op: OpRemove, Provider: ProviderOpenRouter}); err != nil {
					t.Fatal(err)
				}
				setKey(t, s, fixtureKey)
			case "parallel-recheck":
				if _, err := s.RecheckOpenRouterKey(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			if err := <-done; !errors.Is(err, ErrOpenRouterKeyChanged) {
				t.Fatalf("stale publication: %v", err)
			}
		})
	}
}
func TestOpenRouterOldWriterPreservesMetadata(t *testing.T) {
	s := testStore(t)
	s.openRouterTransport = routerTransport(func(*http.Request) (*http.Response, error) { return routerResponse(t, 200), nil })
	if _, err := s.VerifyOpenRouterKey(context.Background(), NewSecret(fixtureKey)); err != nil {
		t.Fatal(err)
	}
	// Model 0.52.0 Apply: merge only its known fields into the raw record.
	// It retains unknown metadata even when replacing the API key.
	for _, key := range []string{fixtureKey, fixtureKey2} {
		raw, err := mergeEntry(s.doc.providers[string(ProviderOpenRouter)], struct {
			Kind      Kind   `json:"kind"`
			APIKey    string `json:"api_key"`
			UpdatedAt string `json:"updated_at,omitempty"`
		}{KindAPIKey, key, "legacy-time"})
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		fields["future_field"] = json.RawMessage(`{"retained":true}`)
		raw, _ = json.Marshal(fields)
		next := s.doc.clone()
		next.providers[string(ProviderOpenRouter)] = raw
		data, _ := next.encode()
		if err := os.WriteFile(s.path, data, 0600); err != nil {
			t.Fatal(err)
		}
		reopened := NewStore(s.path)
		statuses, err := reopened.Status()
		want := "verified"
		if key != fixtureKey {
			want = "unverified"
		}
		if err != nil || statuses[0].Verification.State != want {
			t.Fatalf("%+v %v", statuses, err)
		}
		if _, err := reopened.Apply(Change{Op: OpSet, Provider: ProviderOpenRouter, APIKey: NewSecret("sk-or-fake-third")}); err != nil {
			t.Fatal(err)
		}
		data, _ = os.ReadFile(s.path)
		if !strings.Contains(string(data), "future_field") {
			t.Fatal("unknown field lost")
		}
	}
}

func TestOpenRouterLegacyStatusAndRejectedReplacement(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	states, err := s.Status()
	if err != nil || states[0].Verification.State != "unverified" || states[0].Verification.CheckedAt != "" {
		t.Fatalf("%+v %v", states, err)
	}
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	s.openRouterTransport = routerTransport(func(*http.Request) (*http.Response, error) { return routerResponse(t, 401), nil })
	if _, err := s.VerifyOpenRouterKey(context.Background(), NewSecret(fixtureKey2)); !errors.Is(err, ErrOpenRouterKeyRejected) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(s.path)
	if err != nil || string(before) != string(after) {
		t.Fatal("rejected replacement altered file")
	}
	credential, err := openrouter(s)
	if err != nil || credential.Secret.Reveal() != fixtureKey {
		t.Fatal("rejected replacement lost current key")
	}
}
