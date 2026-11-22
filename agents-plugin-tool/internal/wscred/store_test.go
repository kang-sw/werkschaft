package wscred

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fixture keys: shaped like a provider key, but never a real one.
const (
	fixtureKey  = "sk-or-v1-fixture-key-0001"
	fixtureKey2 = "sk-or-v1-fixture-key-0002"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(filepath.Join(t.TempDir(), dirName, FileName))
	s.now = func() time.Time { return time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC) }
	return s
}

func openrouter(s *Store) (Credential, error) {
	return s.Credential(context.Background(), Request{Provider: ProviderOpenRouter})
}

func setKey(t *testing.T, s *Store, key string) ChangeResult {
	t.Helper()
	res, err := s.Apply(Change{Op: OpSet, Provider: ProviderOpenRouter, APIKey: NewSecret(key)})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	return res
}

func readDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestRegistryHasStableIDs(t *testing.T) {
	p, ok := Lookup("openrouter")
	if !ok || p.ID != ProviderOpenRouter || p.Kind != KindAPIKey || p.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("openrouter = %+v, %v", p, ok)
	}
	if _, ok := Lookup("anthropic"); ok {
		t.Fatal("an unregistered provider resolves")
	}
	list := Providers()
	list[0].ID = "mutated"
	if Providers()[0].ID != ProviderOpenRouter {
		t.Fatal("Providers exposes the registry itself")
	}
}

func TestMaskKeepsOnlyPrefixAndTail(t *testing.T) {
	p, _ := Lookup(ProviderOpenRouter)
	for _, tc := range []struct{ key, want string }{
		{fixtureKey, "sk-or-...0001"},
		{"plain-fixture-value-wxyz", "...wxyz"},
		{"sk-or-short", "****"},
		{"", "****"},
	} {
		if got := Mask(p, tc.key); got != tc.want {
			t.Errorf("Mask(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestMissingFileIsNotConfiguredAndReadTouchesNoFile(t *testing.T) {
	s := testStore(t)
	_, err := openrouter(s)
	var perr *ProviderError
	if !errors.Is(err, ErrNotConfigured) || !errors.As(err, &perr) || perr.Provider != ProviderOpenRouter {
		t.Fatalf("Credential = %v, want a not-configured ProviderError", err)
	}
	if !strings.Contains(err.Error(), "credential set openrouter") {
		t.Fatalf("not-configured error says nothing actionable: %v", err)
	}
	status, err := s.Status()
	if err != nil || len(status) != 1 || status[0].Configured || status[0].Masked != "" {
		t.Fatalf("Status = %+v, %v", status, err)
	}
	if _, err := os.Stat(filepath.Dir(s.Path())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a read created the credential directory: %v", err)
	}
	if _, err := s.Credential(context.Background(), Request{Provider: "nope"}); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("unknown provider = %v", err)
	}
}

func TestSetServesFromMemoryAndRemoveForgets(t *testing.T) {
	s := testStore(t)
	res := setKey(t, s, "  "+fixtureKey+"\n")
	if !res.Changed || !res.Status.Configured || res.Status.Masked != "sk-or-...0001" || res.Status.UpdatedAt != "2026-11-22T09:00:00Z" {
		t.Fatalf("set result = %+v", res)
	}
	cred, err := openrouter(s)
	if err != nil || cred.Secret.Reveal() != fixtureKey || cred.Kind != KindAPIKey || !cred.ExpiresAt.IsZero() {
		t.Fatalf("Credential = %+v, %v", cred, err)
	}
	doc := readDoc(t, s.Path())
	entry := doc["providers"].(map[string]any)["openrouter"].(map[string]any)
	if doc["schema_version"].(float64) != 1 || entry["kind"] != "api_key" || entry["api_key"] != fixtureKey {
		t.Fatalf("file = %v", doc)
	}

	// The file is read once: a later outside edit is not reread on a
	// request (the daemon is the only writer while it runs).
	other := strings.Replace(mustRead(t, s.Path()), fixtureKey, fixtureKey2, 1)
	if err := os.WriteFile(s.Path(), []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	if cred, _ := openrouter(s); cred.Secret.Reveal() != fixtureKey {
		t.Fatal("a request reread the credential file")
	}
	if err := os.WriteFile(s.Path(), []byte(strings.Replace(other, fixtureKey2, fixtureKey, 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err = s.Apply(Change{Op: OpRemove, Provider: ProviderOpenRouter})
	if err != nil || !res.Changed || res.Status.Configured {
		t.Fatalf("remove = %+v, %v", res, err)
	}
	if _, err := openrouter(s); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("after remove = %v", err)
	}
	if providers := readDoc(t, s.Path())["providers"].(map[string]any); len(providers) != 0 {
		t.Fatalf("file after remove = %v", providers)
	}
	if res, err := s.Apply(Change{Op: OpRemove, Provider: ProviderOpenRouter}); err != nil || res.Changed {
		t.Fatalf("second remove = %+v, %v", res, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestUnchangedStateWritesNothing(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	before, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if res := setKey(t, s, fixtureKey); res.Changed {
		t.Fatalf("setting the same key reported a change: %+v", res)
	}
	after, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("setting the same key replaced the file")
	}
}

func TestAtomicWriteLeavesNoTempAndKeepsMemoryOnFailure(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	setKey(t, s, fixtureKey2)
	entries, err := os.ReadDir(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != FileName {
		t.Fatalf("credential directory = %v, want only %s", entries, FileName)
	}
	// A non-empty directory in the file's place makes the replace fail.
	if err := os.Remove(s.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Path(), "blocker"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(Change{Op: OpSet, Provider: ProviderOpenRouter, APIKey: NewSecret(fixtureKey)}); err == nil {
		t.Fatal("a failed write reported success")
	}
	if cred, _ := openrouter(s); cred.Secret.Reveal() != fixtureKey2 {
		t.Fatal("memory took a change the file never got")
	}
	if entries, _ := os.ReadDir(filepath.Dir(s.Path())); len(entries) != 1 {
		t.Fatalf("a failed write left %v behind", entries)
	}
}

func TestWriterKeepsMembersItDoesNotKnow(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	newer := `{"schema_version":1,"future":{"x":1},"providers":{` +
		`"openai":{"kind":"oauth","refresh":"opaque-fixture"},` +
		`"openrouter":{"kind":"api_key","api_key":"` + fixtureKey + `","label":"work"}}}`
	if err := os.WriteFile(s.Path(), []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	setKey(t, s, fixtureKey2)
	doc := readDoc(t, s.Path())
	providers := doc["providers"].(map[string]any)
	if doc["future"] == nil || providers["openai"] == nil {
		t.Fatalf("unknown members dropped: %v", doc)
	}
	entry := providers["openrouter"].(map[string]any)
	if entry["label"] != "work" || entry["api_key"] != fixtureKey2 {
		t.Fatalf("rewritten entry = %v", entry)
	}
}

func TestNewerSchemaIsReadButNeverRewritten(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":2,"providers":{"openrouter":{"kind":"api_key","api_key":"` + fixtureKey + `"}}}`
	if err := os.WriteFile(s.Path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if cred, err := openrouter(s); err != nil || cred.Secret.Reveal() != fixtureKey {
		t.Fatalf("Credential = %+v, %v", cred, err)
	}
	if _, err := s.Apply(Change{Op: OpRemove, Provider: ProviderOpenRouter}); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("remove under a newer schema = %v", err)
	}
	if mustRead(t, s.Path()) != raw {
		t.Fatal("a newer-schema file was rewritten")
	}
}

func TestBrokenFileErrorsNeverQuoteContentAndAChangeRereads(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path(), []byte(`{"providers":{"openrouter":{"api_key":"`+fixtureKey), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := openrouter(s)
	if err == nil || strings.Contains(err.Error(), fixtureKey) || strings.Contains(err.Error(), "fixture") {
		t.Fatalf("broken file error = %v", err)
	}
	if _, err := s.Status(); err == nil {
		t.Fatal("Status read a broken file")
	}
	// The user removes the broken file; the next change rereads it.
	if err := os.Remove(s.Path()); err != nil {
		t.Fatal(err)
	}
	setKey(t, s, fixtureKey)
	if cred, err := openrouter(s); err != nil || cred.Secret.Reveal() != fixtureKey {
		t.Fatalf("after recovery = %+v, %v", cred, err)
	}
}

func TestBrokenFileFixedByHandIsReadAgainWithoutAChange(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path(), []byte(`{"providers":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openrouter(s); err == nil || errors.Is(err, ErrNotConfigured) {
		t.Fatalf("broken file = %v", err)
	}
	// Unchanged, the file stays broken: the kept failure answers.
	if _, err := openrouter(s); err == nil || errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unchanged broken file = %v", err)
	}
	fixed := `{"schema_version":1,"providers":{"openrouter":{"kind":"api_key","api_key":"` + fixtureKey + `"}}}`
	if err := os.WriteFile(s.Path(), []byte(fixed), 0o600); err != nil {
		t.Fatal(err)
	}
	if cred, err := openrouter(s); err != nil || cred.Secret.Reveal() != fixtureKey {
		t.Fatalf("after the hand fix = %+v, %v", cred, err)
	}
}

func TestKeyValidationNeverQuotesTheKey(t *testing.T) {
	s := testStore(t)
	for _, key := range []string{"", "   ", "sk-or-v1 fixture", "sk-or-v1-fixture\x00", "sk-or-v1-fixtüre", strings.Repeat("k", maxKeyBytes+1)} {
		_, err := s.Apply(Change{Op: OpSet, Provider: ProviderOpenRouter, APIKey: NewSecret(key)})
		if err == nil {
			t.Fatalf("key %q accepted", key)
		}
		if strings.TrimSpace(key) != "" && strings.Contains(err.Error(), strings.TrimSpace(key)[:4]) {
			t.Fatalf("validation error quotes the key: %v", err)
		}
	}
	if _, err := s.Apply(Change{Op: "rotate", Provider: ProviderOpenRouter}); err == nil {
		t.Fatal("an unknown change was accepted")
	}
	if _, err := s.Apply(Change{Op: OpSet, Provider: "nope", APIKey: NewSecret(fixtureKey)}); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("unknown provider = %v", err)
	}
	if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused change wrote the file")
	}
}

func TestSecretsAreRedactedInEveryPrintedForm(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	cred, err := openrouter(s)
	if err != nil {
		t.Fatal(err)
	}
	change := Change{Op: OpSet, Provider: ProviderOpenRouter, APIKey: NewSecret(fixtureKey)}
	client := NewRouteClient("http://127.0.0.1:1", NewSecret(fixtureKey2), nil)
	var forms []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		forms = append(forms, fmt.Sprintf(verb, cred.Secret), fmt.Sprintf(verb, cred), fmt.Sprintf(verb, change),
			fmt.Sprintf(verb, s), fmt.Sprintf(verb, client), fmt.Sprintf(verb, []Credential{cred}))
	}
	for _, value := range []any{cred, change, cred.Secret, map[string]Secret{"k": cred.Secret}} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		forms = append(forms, string(raw))
	}
	status, _ := s.Status()
	raw, _ := json.Marshal(status)
	forms = append(forms, string(raw))
	for _, form := range forms {
		if strings.Contains(form, "fixture") {
			t.Fatalf("a secret leaked into %q", form)
		}
	}
	if fmt.Sprint(cred.Secret) != Redacted || fmt.Sprint(Secret{}) != "" {
		t.Fatalf("redacted forms = %q / %q", fmt.Sprint(cred.Secret), fmt.Sprint(Secret{}))
	}
}

// TestCloseWaitsForTheChangeInFlightAndRefusesLaterOnes: the daemon closes
// its store before it releases the lock, so a change in flight lands before
// Close returns and none lands after.
func TestCloseWaitsForTheChangeInFlightAndRefusesLaterOnes(t *testing.T) {
	s := testStore(t)
	entered, gate := make(chan struct{}), make(chan struct{})
	beforeCredentialWrite = func() {
		close(entered)
		<-gate
	}
	t.Cleanup(func() { beforeCredentialWrite = nil })
	applied := make(chan error, 1)
	go func() {
		_, err := s.Apply(Change{Op: OpSet, Provider: ProviderOpenRouter, APIKey: NewSecret(fixtureKey)})
		applied <- err
	}()
	<-entered
	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a change was in flight")
	case <-time.After(100 * time.Millisecond):
	}
	beforeCredentialWrite = nil
	close(gate)
	if err := <-applied; err != nil {
		t.Fatalf("the change in flight = %v", err)
	}
	<-closed
	raw, err := os.ReadFile(s.Path())
	if err != nil || !strings.Contains(string(raw), fixtureKey) {
		t.Fatalf("the change in flight did not land before Close returned: %v", err)
	}
	if _, err := s.Apply(Change{Op: OpRemove, Provider: ProviderOpenRouter}); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("a change after Close = %v", err)
	}
	if raw2, _ := os.ReadFile(s.Path()); string(raw2) != string(raw) {
		t.Fatal("a refused change rewrote the file")
	}
	if cred, err := openrouter(s); err != nil || cred.Secret.Reveal() != fixtureKey {
		t.Fatalf("a closed store stopped answering reads: %+v, %v", cred, err)
	}
}

func writeLegacy(t *testing.T, root, body string) string {
	t.Helper()
	legacy := filepath.Join(root, dirName, FileName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return legacy
}

// TestDefaultPathLivesUnderTheDaemonDirectory: v0.51.0's file preview denies
// the daemon directory only, so the credential file must live under it.
func TestDefaultPathLivesUnderTheDaemonDirectory(t *testing.T) {
	root := t.TempDir()
	path, err := DefaultPath(root)
	if err != nil {
		t.Fatal(err)
	}
	want := string(filepath.Separator) + filepath.Join("dashboard", dirName, FileName)
	if !strings.HasSuffix(path, want) || filepath.Base(root) != filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path)))) {
		t.Fatalf("DefaultPath = %q, not %s under the cache root %q", path, want, root)
	}
}

// TestMigrateLegacyMovesTheCandidateFileOnce: a file a pre-release build
// wrote beside the daemon directory is moved, not copied, into the store's
// path with its content; the unprotected directory goes; a second call and a
// call with no legacy file change nothing.
func TestMigrateLegacyMovesTheCandidateFileOnce(t *testing.T) {
	root := t.TempDir()
	path, _ := DefaultPath(root)
	body := `{"schema_version":1,"providers":{"openrouter":{"kind":"api_key","api_key":"` + fixtureKey + `","future":1}},"newer":true}`
	legacy := writeLegacy(t, root, body)
	s := NewStore(path)
	if err := s.MigrateLegacy(); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != body {
		t.Fatalf("moved file = %q", got)
	}
	if _, err := os.Stat(filepath.Dir(legacy)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the legacy location remains: %v", err)
	}
	if cred, err := openrouter(s); err != nil || cred.Secret.Reveal() != fixtureKey {
		t.Fatalf("after the move = %+v, %v", cred, err)
	}
	if err := NewStore(path).MigrateLegacy(); err != nil || mustRead(t, path) != body {
		t.Fatalf("second migrate changed the file: %v", err)
	}
	fresh, _ := DefaultPath(t.TempDir())
	if err := NewStore(fresh).MigrateLegacy(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(fresh)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("migrating with no legacy file created a directory: %v", err)
	}
}

func TestMigrateLegacyKeepsTheFileAlreadyAtThePath(t *testing.T) {
	root := t.TempDir()
	path, _ := DefaultPath(root)
	s := NewStore(path)
	setKey(t, s, fixtureKey)
	legacy := writeLegacy(t, root, `{"schema_version":1,"providers":{"openrouter":{"kind":"api_key","api_key":"`+fixtureKey2+`"}}}`)
	if err := NewStore(path).MigrateLegacy(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the stale legacy file remains: %v", err)
	}
	if got := mustRead(t, path); !strings.Contains(got, fixtureKey) || strings.Contains(got, fixtureKey2) {
		t.Fatalf("the file at the path was replaced: %q", got)
	}
}

// TestMigrateLegacyOnlyTouchesTheDefaultPath: a store at any other path has
// no legacy location, so a file beside it is never moved.
func TestMigrateLegacyOnlyTouchesTheDefaultPath(t *testing.T) {
	root := t.TempDir()
	legacy := writeLegacy(t, root, "{}")
	if err := NewStore(filepath.Join(root, "elsewhere", dirName, FileName)).MigrateLegacy(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("an unrelated file moved: %v", err)
	}
}
