package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func TestCredentialCLIOmitsAndRefusesVoiceOnlyProviders(t *testing.T) {
	cacheHome := t.TempDir()
	configHome := t.TempDir()
	credPath, err := wscred.DefaultPath(cacheHome)
	if err != nil {
		t.Fatal(err)
	}
	store := wscred.NewStore(credPath)
	if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenAIAPI, APIKey: wscred.NewSecret("sk-proj-fixture-voice-0001")}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderGroq, APIKey: wscred.NewSecret("gsk_fixture_voice_key_0002")}); err != nil {
		t.Fatal(err)
	}

	run := func(stdin string, args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		cli := &hostedCLI{
			hello: wsdashboard.ProxyHello{
				Env: map[string]string{
					wsstate.EnvCacheHome:   cacheHome,
					wsconfig.EnvConfigHome: configHome,
				},
			},
			ctx:    context.Background(),
			stdin:  strings.NewReader(stdin),
			stdout: &stdout,
			stderr: &stderr,
		}
		code := cli.credentialCommand(args)
		return code, stdout.String(), stderr.String()
	}

	// credential show lists only agent providers, never openai_api or groq.
	code, out, errOut := run("", "show")
	if code != 0 {
		t.Fatalf("credential show exited %d: %s", code, errOut)
	}
	for _, hidden := range []string{"openai_api", "groq", "OpenAI Platform", "Groq"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("credential show leaked voice-only provider %q:\n%s", hidden, out)
		}
	}

	// credential show/set/remove refuse voice-only providers.
	for _, provider := range []string{"openai_api", "groq"} {
		for _, tc := range []struct {
			args  []string
			stdin string
		}{
			{args: []string{"show", provider}},
			{args: []string{"set", provider}, stdin: "sk-proj-fixture-voice-9999\n"},
			{args: []string{"remove", provider}},
		} {
			code, _, errOut := run(tc.stdin, tc.args...)
			if code == 0 {
				t.Fatalf("credential %v succeeded, want refusal", tc.args)
			}
			if strings.Contains(errOut, "openai_api") || strings.Contains(errOut, "groq") {
				t.Fatalf("credential %v error listed voice-only provider: %s", tc.args, errOut)
			}
		}
	}
}
