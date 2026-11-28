package wsdashboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func TestDeniedBasePathsBlocksManagedAndCustomAccountSlots(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv(wsstate.EnvCacheHome, cacheHome)

	managedDir, err := wscred.CreateManagedSlotDir(cacheHome, "codex", "team-a")
	if err != nil {
		t.Fatal(err)
	}
	managedAuth := filepath.Join(managedDir, "auth.json")
	if err := os.WriteFile(managedAuth, []byte(`{"tokens":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	customDir := filepath.Join(t.TempDir(), "custom-codex-home")
	if err := os.MkdirAll(customDir, 0o700); err != nil {
		t.Fatal(err)
	}
	customAuth := filepath.Join(customDir, "auth.json")
	customClaude := filepath.Join(customDir, ".credentials.json")
	if err := os.WriteFile(customAuth, []byte(`{"tokens":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(customClaude, []byte(`{"claudeAiOauth":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	reg := wscred.NewAccountSlotRegistry(filepath.Join(cacheHome, "account-slots.json"))
	t.Cleanup(reg.Close)
	if _, err := reg.AddSlot(wscred.AccountSlot{
		ID: "custom", Harness: "codex", Label: "Custom", ConfigDir: customDir,
	}); err != nil {
		t.Fatal(err)
	}

	deny := newDenySet()
	for _, target := range []string{managedDir, managedAuth, customDir, customAuth, customClaude} {
		if !deny.denied(target) {
			t.Errorf("expected slot path %q to be denied", target)
		}
	}

	// Unrelated directory outside slots must remain allowed.
	other := filepath.Join(t.TempDir(), "safe.txt")
	if err := os.WriteFile(other, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if deny.denied(other) {
		t.Errorf("unrelated file %q should not be denied", other)
	}
}

func TestPlanUsageConfigHomeUsesD10Resolver(t *testing.T) {
	explicitHome := t.TempDir()
	envCodex := t.TempDir()
	slotDir := t.TempDir()

	reg := wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), "slots.json"))
	t.Cleanup(reg.Close)
	if _, err := reg.AddSlot(wscred.AccountSlot{
		ID: "alt", Harness: "codex", Label: "Alt", ConfigDir: slotDir,
	}); err != nil {
		t.Fatal(err)
	}

	store := newPlanUsageStoreBase(t.TempDir(), explicitHome, func(k string) string {
		if k == "CODEX_HOME" {
			return envCodex
		}
		return ""
	})
	store.explicitHome = explicitHome
	store.accountSlots = reg

	// Implicit default slot with explicitHome suppresses inherited CODEX_HOME.
	if got := store.configHome("CODEX_HOME", ".codex"); got != filepath.Join(explicitHome, ".codex") {
		t.Fatalf("explicitHome suppression: got %q", got)
	}

	// Active slot config_dir takes precedence over explicitHome and env.
	if err := reg.SetActive("codex", "alt"); err != nil {
		t.Fatal(err)
	}
	if got := store.configHome("CODEX_HOME", ".codex"); got != slotDir {
		t.Fatalf("active slot configHome: got %q want %q", got, slotDir)
	}
}
