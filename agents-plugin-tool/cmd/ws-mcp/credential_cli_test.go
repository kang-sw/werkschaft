package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

const credentialCLIFixtureKey = "sk-or-v1-fixture-key-0001"

// runCredentialCLI drives hostedCLI.run with a fresh cache home under temp.
func runCredentialCLI(t *testing.T, host *proxyHost, cache, stdin string, argv ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	hello := wsdashboard.ProxyHello{Protocol: 1, Mode: "cli", Argv: argv, Stdin: stdin, Env: map[string]string{"WS_CACHE_HOME": cache}}
	cli := &hostedCLI{hello: hello, host: host, ctx: context.Background(), stdout: &out, stderr: &errOut}
	code = cli.run()
	for name, text := range map[string]string{"stdout": out.String(), "stderr": errOut.String()} {
		if strings.Contains(text, credentialCLIFixtureKey) {
			t.Fatalf("%s carries the key: %q", name, text)
		}
	}
	return out.String(), errOut.String(), code
}

func TestCredentialCLIDirectWithNoDaemon(t *testing.T) {
	cache := t.TempDir()
	credPath, err := wscred.DefaultPath(cache)
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runCredentialCLI(t, nil, cache, credentialCLIFixtureKey+"\nignored second line\n", "credential", "set", "openrouter")
	if code != 0 || out != "openrouter: set sk-or-...0001\n" {
		t.Fatalf("set = %d %q %q", code, out, errOut)
	}
	raw, err := os.ReadFile(credPath)
	if err != nil || !strings.Contains(string(raw), credentialCLIFixtureKey) {
		t.Fatalf("credential file after set = %q, %v", raw, err)
	}
	out, _, code = runCredentialCLI(t, nil, cache, credentialCLIFixtureKey, "credential", "set", "openrouter")
	if code != 0 || out != "openrouter: unchanged sk-or-...0001\n" {
		t.Fatalf("repeat set = %d %q", code, out)
	}
	out, _, code = runCredentialCLI(t, nil, cache, "", "credential", "show")
	if code != 0 || !strings.HasPrefix(out, "openrouter (OpenRouter): sk-or-...0001, updated ") {
		t.Fatalf("show = %d %q", code, out)
	}
	for _, argv := range [][]string{{"credential", "show", "openrouter", "--format", "json"}, {"credential", "show", "--format", "json", "openrouter"}} {
		out, _, code = runCredentialCLI(t, nil, cache, "", argv...)
		var statuses []wscred.ProviderStatus
		if code != 0 || json.Unmarshal([]byte(out), &statuses) != nil || len(statuses) != 1 || !statuses[0].Configured || statuses[0].Masked != "sk-or-...0001" {
			t.Fatalf("%v = %d %q", argv, code, out)
		}
	}
	out, _, code = runCredentialCLI(t, nil, cache, "", "credential", "show", "--format=json")
	var all []wscred.ProviderStatus
	if code != 0 || json.Unmarshal([]byte(out), &all) != nil || len(all) != 2 ||
		all[0].Provider != wscred.ProviderOpenRouter || !all[0].Configured ||
		all[1].Provider != wscred.ProviderOpenAI || all[1].Configured {
		t.Fatal("credential show did not list both providers and their status")
	}
	out, _, code = runCredentialCLI(t, nil, cache, "", "credential", "remove", "openrouter", "--format", "json")
	var result wscred.ChangeResult
	if code != 0 || json.Unmarshal([]byte(out), &result) != nil || !result.Changed || result.Status.Configured {
		t.Fatalf("json remove = %d %q", code, out)
	}
	out, _, code = runCredentialCLI(t, nil, cache, "", "credential", "remove", "openrouter")
	if code != 0 || out != "openrouter: was not set\n" {
		t.Fatalf("repeat remove = %d %q", code, out)
	}
	out, _, code = runCredentialCLI(t, nil, cache, "", "credential", "show")
	if code != 0 || out != "openrouter (OpenRouter): not set\nopenai (OpenAI): not set\n" {
		t.Fatalf("show after remove = %d %q", code, out)
	}
	if raw, _ := os.ReadFile(credPath); strings.Contains(string(raw), credentialCLIFixtureKey) {
		t.Fatalf("the key survived remove: %q", raw)
	}
}

func TestCredentialCLIRefusals(t *testing.T) {
	cache := t.TempDir()
	credPath, _ := wscred.DefaultPath(cache)
	cases := []struct {
		name, stdin, want string
		argv              []string
	}{
		{"key in argv", credentialCLIFixtureKey, "stdin", []string{"credential", "set", "openrouter", credentialCLIFixtureKey}},
		{"key in argv before the flag", "", "stdin", []string{"credential", "set", "openrouter", "--format", "json", credentialCLIFixtureKey}},
		{"empty stdin", "", "no API key on stdin", []string{"credential", "set", "openrouter"}},
		{"blank first line", "  \n" + credentialCLIFixtureKey, "no API key on stdin", []string{"credential", "set", "openrouter"}},
		{"unknown provider", credentialCLIFixtureKey, "known providers: openrouter, openai", []string{"credential", "set", "nonesuch"}},
		{"unknown provider on show", "", "known providers: openrouter, openai", []string{"credential", "show", "nonesuch"}},
		{"openai is not an API key", "", "credential sign-in openai", []string{"credential", "set", "openai"}},
		{"no credential in sign-in arguments", "", "usage", []string{"credential", "sign-in", "openai", credentialCLIFixtureKey}},
		{"incompatible registration selectors", "", "usage", []string{"credential", "sign-in", "openai", "--new-account", "--account", credentialCLIFixtureKey}},
		{"missing provider", "", "usage", []string{"credential", "remove"}},
		{"unknown subcommand", "", "usage", []string{"credential", "list"}},
		// A key typed where an argument belongs is never echoed back
		// (runCredentialCLI fails on any output carrying it).
		{"key in place of the provider", "", "known providers: openrouter, openai", []string{"credential", "set", credentialCLIFixtureKey}},
		{"key in place of the provider on show", "", "known providers: openrouter, openai", []string{"credential", "show", credentialCLIFixtureKey}},
		{"key in place of the provider on remove", "", "known providers: openrouter, openai", []string{"credential", "remove", credentialCLIFixtureKey}},
		{"key in place of the subcommand", "", "use set, remove, show, or sign-in", []string{"credential", credentialCLIFixtureKey}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code := runCredentialCLI(t, nil, cache, tc.stdin, tc.argv...)
			if code == 0 || out != "" || !strings.Contains(errOut, tc.want) {
				t.Fatalf("expected a refusal naming %q, got exit code %d", tc.want, code)
			}
		})
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Fatalf("a refused command wrote the credential file: %v", err)
	}
}

func TestCredentialCLIReadsOnlyTheFirstLine(t *testing.T) {
	// A reader that yields the first line and then blocks would hang a
	// read-to-EOF implementation.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	if _, err := pw.WriteString(credentialCLIFixtureKey + "\n"); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		key, err := readCredentialLine(pr)
		if err != nil {
			key = "error: " + err.Error()
		}
		done <- key
	}()
	select {
	case key := <-done:
		if key != credentialCLIFixtureKey {
			t.Fatalf("key = %q", key)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readCredentialLine waited past the first newline")
	}
	if _, err := readCredentialLine(strings.NewReader(strings.Repeat("a", maxCredentialInput+10))); err == nil {
		t.Fatal("an over-long line was accepted")
	}
}

func TestCredentialCLIDirectWithARunningDaemon(t *testing.T) {
	cache := t.TempDir()
	dir, err := wsdashboard.Dir(cache)
	if err != nil {
		t.Fatal(err)
	}
	credPath, _ := wscred.DefaultPath(cache)
	store := wscred.NewStore(credPath)
	d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: dir, Home: t.TempDir(), Port: 0, Version: "1.0.0", LockWait: 200 * time.Millisecond, Credentials: store})
	if err != nil {
		t.Fatalf("StartDaemon: %v", err)
	}
	t.Cleanup(d.Close)
	out, errOut, code := runCredentialCLI(t, nil, cache, credentialCLIFixtureKey+"\n", "credential", "set", "openrouter")
	if code != 0 || out != "openrouter: set sk-or-...0001\n" {
		t.Fatalf("set = %d %q %q", code, out, errOut)
	}
	got, err := store.Credential(context.Background(), wscred.Request{Provider: "openrouter"})
	if err != nil || got.Secret.Reveal() != credentialCLIFixtureKey {
		t.Fatalf("the daemon's store after the CLI set = %v, %v", got, err)
	}
	out, _, code = runCredentialCLI(t, nil, cache, "", "credential", "remove", "openrouter")
	if code != 0 || out != "openrouter: removed\n" {
		t.Fatalf("remove = %d %q", code, out)
	}
	if _, err := store.Credential(context.Background(), wscred.Request{Provider: "openrouter"}); err == nil {
		t.Fatal("the daemon's store still serves a removed key")
	}
}

func TestCredentialCLIInsideTheDaemonUsesItsOwnStore(t *testing.T) {
	daemonCache, otherCache := t.TempDir(), t.TempDir()
	credPath, _ := wscred.DefaultPath(daemonCache)
	store := wscred.NewStore(credPath)
	host := &proxyHost{credentials: store}
	out, errOut, code := runCredentialCLI(t, host, otherCache, credentialCLIFixtureKey+"\n", "credential", "set", "openrouter")
	if code != 0 || out != "openrouter: set sk-or-...0001\n" {
		t.Fatalf("hosted set = %d %q %q", code, out, errOut)
	}
	got, err := store.Credential(context.Background(), wscred.Request{Provider: "openrouter"})
	if err != nil || got.Secret.Reveal() != credentialCLIFixtureKey {
		t.Fatalf("store after hosted set = %v, %v", got, err)
	}
	out, _, code = runCredentialCLI(t, host, otherCache, "", "credential", "show")
	if code != 0 || !strings.Contains(out, "sk-or-...0001") {
		t.Fatalf("hosted show = %d %q", code, out)
	}
	entries, err := os.ReadDir(otherCache)
	if err != nil || len(entries) != 0 {
		t.Fatalf("the hosted command wrote under the caller's cache: %v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(otherCache, "auth")); !os.IsNotExist(err) {
		t.Fatalf("auth dir under the caller's cache: %v", err)
	}
	out, _, code = runCredentialCLI(t, host, otherCache, "", "credential", "remove", "openrouter")
	if code != 0 || out != "openrouter: removed\n" {
		t.Fatalf("hosted remove = %d %q", code, out)
	}
}

func TestCredentialCLIInsideTheDaemonWithNoStoreRefuses(t *testing.T) {
	cache := t.TempDir()
	for _, argv := range [][]string{{"credential", "set", "openrouter"}, {"credential", "remove", "openrouter"}, {"credential", "show"}} {
		out, errOut, code := runCredentialCLI(t, &proxyHost{}, cache, credentialCLIFixtureKey, argv...)
		if code == 0 || out != "" || !strings.Contains(errOut, "credential store is unavailable") {
			t.Fatalf("%v = %d %q %q", argv, code, out, errOut)
		}
	}
	entries, _ := os.ReadDir(cache)
	if len(entries) != 0 {
		t.Fatalf("a refused hosted command wrote under the cache: %v", entries)
	}
}

// TestCredentialCLIShowMovesTheCandidateFile: `credential show` with no
// daemon moves a credential file a pre-release build wrote beside the daemon
// directory into the protected location, and reports its key.
func TestCredentialCLIShowMovesTheCandidateFile(t *testing.T) {
	cache := t.TempDir()
	credPath, _ := wscred.DefaultPath(cache)
	dir, _ := wsdashboard.Dir(cache)
	legacy := filepath.Join(filepath.Dir(dir), "auth", wscred.FileName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":1,"providers":{"openrouter":{"kind":"api_key","api_key":"` + credentialCLIFixtureKey + `"}}}`
	if err := os.WriteFile(legacy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runCredentialCLI(t, nil, cache, "", "credential", "show")
	if code != 0 || !strings.Contains(out, "sk-or-...0001") {
		t.Fatalf("show = %d %q %q", code, out, errOut)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("the legacy file remains: %v", err)
	}
	if raw, err := os.ReadFile(credPath); err != nil || string(raw) != body {
		t.Fatalf("the moved file = %q, %v", raw, err)
	}
}

func TestCredentialCLIEndpointEdits(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv(wsconfig.EnvConfigHome, configHome)
	cache := t.TempDir()
	out, stderr, code := runCredentialCLI(t, nil, cache, "", "credential", "endpoint", "set", "desk", "--base-url", "http://example.invalid/v1", "--kind", "ollama")
	if code != 0 || out != "Endpoint updated.\n" {
		t.Fatalf("set: %d %q %q", code, out, stderr)
	}
	values, err := wsconfig.GlobalOverrides(wsconfig.Options{ConfigHome: configHome})
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := wsconfig.ParseWerkEndpoints(values[wsconfig.ItemWerkEndpoints])
	if err != nil || len(endpoints) != 1 {
		t.Fatalf("endpoints: %v %v", endpoints, err)
	}
	if _, stderr, code = runCredentialCLI(t, nil, cache, credentialCLIFixtureKey, "credential", "set", "local/desk"); code != 0 {
		t.Fatalf("set endpoint key: %d %q", code, stderr)
	}
	t.Setenv(wsconfig.EnvConfigHome, t.TempDir())
	if _, stderr, code = runCredentialCLI(t, nil, cache, "", "credential", "show"); code != 0 {
		t.Fatalf("show from another config home: %d %q", code, stderr)
	}
	if _, stderr, code = runCredentialCLI(t, nil, cache, "", "credential", "endpoint", "set", "desk", "--base-url", "http://other.invalid/v1", "--kind", "ollama"); code != 0 {
		t.Fatalf("set from another config home: %d %q", code, stderr)
	}
	t.Setenv(wsconfig.EnvConfigHome, configHome)
	out, stderr, code = runCredentialCLI(t, nil, cache, "", "credential", "show", "local/desk")
	if code != 0 || !strings.HasPrefix(out, "local/desk (desk): ...0001, updated ") {
		t.Fatalf("show after alternate config write: %d %q %q", code, out, stderr)
	}
	_, stderr, code = runCredentialCLI(t, nil, cache, "", "credential", "endpoint", "remove", "desk")
	if code != 0 {
		t.Fatalf("remove: %d %q", code, stderr)
	}
	values, err = wsconfig.GlobalOverrides(wsconfig.Options{ConfigHome: configHome})
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err = wsconfig.ParseWerkEndpoints(values[wsconfig.ItemWerkEndpoints])
	if err != nil || len(endpoints) != 0 {
		t.Fatalf("endpoints after removal: %v %v", endpoints, err)
	}
}
