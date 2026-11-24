package wscred

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCredentialReplacementScrubsPreviousGeneration(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	alias := linkCredentialFixture(t, s.Path())
	setKey(t, s, fixtureKey2)
	if got := mustRead(t, alias); got != "" {
		t.Fatal("the previous generation keeps bytes after replacement")
	}
	reopened := NewStore(s.Path())
	if cred, err := openrouter(reopened); err != nil || cred.Secret.Reveal() != fixtureKey2 {
		t.Fatalf("reopen did not load the replacement: %v", err)
	}
	if got := mustRead(t, alias); got != "" {
		t.Fatal("the previous generation keeps bytes after restart")
	}
	assertOnlyCurrentCredential(t, s.Path())
}

func TestCredentialReplacementRenameFailureKeepsCurrent(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	old := mustRead(t, s.Path())
	alias := linkCredentialFixture(t, s.Path())
	_, _, staged := GenerationPaths(s.Path())
	s.writeStep = func(step credentialWriteStep) {
		if step == credentialLinked {
			if err := os.Remove(staged); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.Apply(Change{Op: OpSet, Provider: ProviderOpenRouter, APIKey: NewSecret(fixtureKey2)}); err == nil {
		t.Fatal("the rename reported success after its source disappeared")
	}
	if mustRead(t, s.Path()) != old || mustRead(t, alias) != old {
		t.Fatal("the failed rename changed the previous generation")
	}
	assertOnlyCurrentCredential(t, s.Path())
}

func TestCredentialReplacementRecoversEveryInterruptedStep(t *testing.T) {
	if raw := os.Getenv("WS_CRED_TEST_CRASH_STEP"); raw != "" {
		step, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatal(err)
		}
		s := NewStore(os.Getenv("WS_CRED_TEST_CRASH_PATH"))
		s.writeStep = func(at credentialWriteStep) {
			if int(at) == step {
				os.Exit(42)
			}
		}
		setKey(t, s, fixtureKey2)
		t.Fatal("the replacement did not reach the requested step")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for step, name := range map[credentialWriteStep]string{
		credentialStaged: "staged", credentialLinked: "linked", credentialPublished: "published",
		credentialTruncated: "truncated", credentialRetired: "retired",
	} {
		t.Run(name, func(t *testing.T) {
			s := testStore(t)
			setKey(t, s, fixtureKey)
			old := mustRead(t, s.Path())
			alias := linkCredentialFixture(t, s.Path())
			cmd := exec.Command(exe, "-test.run=^TestCredentialReplacementRecoversEveryInterruptedStep$")
			cmd.Env = append(os.Environ(),
				fmt.Sprintf("WS_CRED_TEST_CRASH_STEP=%d", step), "WS_CRED_TEST_CRASH_PATH="+s.Path())
			out, err := cmd.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 42 {
				t.Fatalf("interruption = %v\n%s", err, out)
			}
			reopened := NewStore(s.Path())
			if err := reopened.MigrateLegacy(); err != nil {
				t.Fatal(err)
			}
			wantKey, wantAlias := fixtureKey, old
			if step >= credentialPublished {
				wantKey, wantAlias = fixtureKey2, ""
			}
			if cred, err := openrouter(reopened); err != nil || cred.Secret.Reveal() != wantKey {
				t.Fatalf("recovered the wrong current generation: %v", err)
			}
			if got := mustRead(t, alias); got != wantAlias {
				t.Fatal("recovery left the previous generation in the wrong state")
			}
			assertOnlyCurrentCredential(t, s.Path())
		})
	}
}

func linkCredentialFixture(t *testing.T, path string) string {
	t.Helper()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	return alias
}

func assertOnlyCurrentCredential(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("credential generations = %v, %v", entries, err)
	}
}

func AfterCredentialPublishForTest(s *Store, fn func()) {
	s.writeStep = func(step credentialWriteStep) {
		if step == credentialPublished {
			fn()
		}
	}
}
