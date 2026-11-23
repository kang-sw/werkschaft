package wscred

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

func TestLocalEndpointKeysFollowTheirConfiguredAPIRoot(t *testing.T) {
	opts := wsconfig.Options{ConfigHome: t.TempDir()}
	resolver := wsconfig.NewResolver(opts, nil, nil, nil)
	path := filepath.Join(t.TempDir(), dirName, FileName)
	store := NewStore(path)
	const id ProviderID = "local/desk"
	const original = "http://example.invalid/one"
	const other = "http://example.invalid/two"
	const key = "example-key-material"
	configure := func(base string) {
		t.Helper()
		raw := fmt.Sprintf(`{"desk":{"base_url":%q,"kind":"ollama"}}`, base)
		if err := resolver.Set(wsconfig.ItemWerkEndpoints, raw, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
		if err := store.LoadLocalEndpoints(opts); err != nil {
			t.Fatal(err)
		}
	}
	checkStatus := func(configured bool) {
		t.Helper()
		statuses, err := store.Status()
		if err != nil || len(statuses) != 3 {
			t.Fatalf("statuses = %v, %v", statuses, err)
		}
		got := statuses[2]
		if got.Provider != id || got.Kind != KindAPIKey || got.Configured != configured || got.Masked == key {
			t.Fatalf("local status = %v", got)
		}
	}
	configure(original)
	checkStatus(false)
	if _, err := store.Credential(context.Background(), Request{Provider: id, BaseURL: original}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing key = %v", err)
	}
	start := store.LocalRevision()
	result, err := store.Apply(Change{Op: OpSet, Provider: id, APIKey: NewSecret(key)})
	if err != nil || !result.Changed || store.LocalRevision() <= start {
		t.Fatalf("set key = %v, %v", result, err)
	}
	checkStatus(true)
	store = NewStore(path)
	configure(other)
	checkStatus(false)
	if _, err := store.Credential(context.Background(), Request{Provider: id, BaseURL: original}); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("old URL lookup after reconfiguration = %v", err)
	}
	if _, err := store.Credential(context.Background(), Request{Provider: id, BaseURL: other}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("new URL inherited prior key: %v", err)
	}
	configure(original + "/")
	cred, err := store.Credential(context.Background(), Request{Provider: id, BaseURL: original})
	if err != nil || cred.Secret.Reveal() != key {
		t.Fatalf("return to original URL did not recover key: %v", err)
	}
	if _, err := store.Credential(context.Background(), Request{Provider: id}); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("lookup without matching URL = %v", err)
	}
	if _, err := store.Apply(Change{Op: OpRemove, Provider: id}); err != nil {
		t.Fatal(err)
	}
	checkStatus(false)
}
