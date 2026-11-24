package wscred

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	_, err = store.Credential(context.Background(), Request{Provider: id, BaseURL: original})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("return to original URL revived retired key: %v", err)
	}
	if _, err := store.Credential(context.Background(), Request{Provider: id}); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("lookup without matching URL = %v", err)
	}
	if _, err := store.Apply(Change{Op: OpRemove, Provider: id}); err != nil {
		t.Fatal(err)
	}
	checkStatus(false)
}

func TestLocalEndpointMutationCleanupRetryAndConcurrency(t *testing.T) {
	opts := wsconfig.Options{ConfigHome: t.TempDir()}
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := NewStore(path)
	set := func(base, key string) error {
		_, err := store.ApplyLocalEndpoint(opts, LocalEndpointChange{Op: OpSet, Name: "desk", BaseURL: base, Kind: "ollama", Key: NewSecret(key)})
		return err
	}
	const first = "http://example.invalid/one"
	if err := set(first, "fake-retired-key"); err != nil {
		t.Fatal(err)
	}
	// Force failure at replacement, after the key-removal bytes were staged.
	// Retrying must finish cleanup without restoring the retired key.
	_, _, staged := GenerationPaths(path)
	store.writeStep = func(step credentialWriteStep) {
		if step == credentialLinked {
			if err := os.Remove(staged); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := set("http://example.invalid/two", ""); err == nil {
		t.Fatal("expected write failure")
	}
	if got := store.LocalProviders(); len(got) != 1 || got[0].BaseURL != "http://example.invalid/two" {
		t.Fatal("failed cleanup left stale catalog")
	}
	if err := set(first, ""); err == nil {
		t.Fatal("accepted URL restoration before retrying cleanup")
	}
	store.writeStep = nil
	// Restart before retrying: recovery must reconcile the saved URL first.
	store = NewStore(path)
	if err := set(first, ""); err != nil {
		t.Fatal(err)
	}
	if err := set("http://example.invalid/two", ""); err != nil {
		t.Fatal(err)
	}
	if err := set(first, ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "fake-retired-key") {
		t.Fatal("retired key persisted")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.ApplyLocalEndpoint(opts, LocalEndpointChange{Op: OpSet, Name: fmt.Sprintf("endpoint-%d", i), BaseURL: first, Kind: "vllm"})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(store.LocalProviders()) != 9 {
		t.Fatal("concurrent edits lost endpoints")
	}
	if err := set(first, "fake-delete-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyLocalEndpoint(opts, LocalEndpointChange{Op: OpRemove, Name: "desk"}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "fake-delete-key") {
		t.Fatal("removed endpoint key persisted")
	}
}

func TestLocalEndpointMutationRejectsSensitiveInput(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	for _, change := range []LocalEndpointChange{
		{Op: OpSet, Name: "desk", BaseURL: "http://secret:password@example.invalid", Kind: "ollama"},
		{Op: OpSet, Name: "desk", BaseURL: "http://example.invalid", Kind: "ollama", Key: NewSecret("secret with whitespace")},
		{Op: OpRemove, Name: "secret/name"},
	} {
		if _, err := store.ApplyLocalEndpoint(wsconfig.Options{ConfigHome: t.TempDir()}, change); !errors.Is(err, ErrInvalidEndpoint) {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestLocalEndpointConfigWriteFailurePreservesCurrentKey(t *testing.T) {
	opts := wsconfig.Options{ConfigHome: t.TempDir()}
	store := NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	change := LocalEndpointChange{Op: OpSet, Name: "desk", BaseURL: "http://example.invalid/one", Kind: "ollama", Key: NewSecret("fake-preserved-key")}
	if _, err := store.ApplyLocalEndpoint(opts, change); err != nil {
		t.Fatal(err)
	}
	configPath, err := wsconfig.GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	// Force the config writer's lock open to fail, while reads remain valid.
	if err := os.Remove(configPath + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing-parent", "lock"), configPath+".lock"); err != nil {
		t.Skipf("symlink failure fixture unavailable: %v", err)
	}
	change.BaseURL = "http://example.invalid/two"
	change.Key = NewSecret("")
	if _, err := store.ApplyLocalEndpoint(opts, change); err == nil {
		t.Fatal("expected config write failure")
	}
	credential, err := store.Credential(context.Background(), Request{Provider: "local/desk", BaseURL: "http://example.invalid/one"})
	if err != nil || credential.Secret.Reveal() != "fake-preserved-key" {
		t.Fatalf("config write failure lost current key: %v", err)
	}
	if err := os.Remove(configPath + ".lock"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyLocalEndpoint(opts, change); err != nil {
		t.Fatal(err)
	}
}
