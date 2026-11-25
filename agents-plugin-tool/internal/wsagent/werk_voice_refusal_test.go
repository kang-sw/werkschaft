package wsagent

import (
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wscred"
)

func TestWerkProviderRefusesVoiceOnlyProviders(t *testing.T) {
	store := wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))
	if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenAIAPI, APIKey: wscred.NewSecret("sk-proj-fixture-voice-0001")}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderGroq, APIKey: wscred.NewSecret("gsk_fixture_voice_key_0002")}); err != nil {
		t.Fatal(err)
	}
	backend := WerkBackend{credentials: store}
	for _, id := range []string{string(wscred.ProviderOpenAIAPI), string(wscred.ProviderGroq)} {
		if _, err := backend.werkProvider(id); err == nil {
			t.Fatalf("werkProvider(%q) succeeded, want refusal for voice-only provider", id)
		}
	}
	if _, err := backend.werkProvider(string(wscred.ProviderOpenRouter)); err != nil {
		t.Fatalf("werkProvider(openrouter) failed: %v", err)
	}
}
