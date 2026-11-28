package wscred

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestVoiceOnlyProvidersStoreMaskAndPreserveUnknownFields(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	seed := `{"schema_version":1,"future_meta":{"keep":true},"providers":{"openai_api":{"kind":"api_key","api_key":"sk-fixture-old-1234","custom_note":"keep-me"}}}`
	if err := os.WriteFile(s.Path(), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	openRouterVoiceKey := "sk-or-v1-fixture-voice-5555"
	openAIKey := "sk-proj-fixture-voice-9876"
	groqKey := "gsk_fixture_voice_key_4321"

	res, err := s.Apply(Change{Op: OpSet, Provider: ProviderOpenRouterVoice, APIKey: NewSecret(openRouterVoiceKey)})
	if err != nil || !res.Changed || !res.Status.Configured || res.Status.Masked != "sk-or-...5555" {
		t.Fatalf("set openrouter_voice = %+v, %v", res, err)
	}
	res, err = s.Apply(Change{Op: OpSet, Provider: ProviderOpenAIAPI, APIKey: NewSecret(openAIKey)})
	if err != nil || !res.Changed || !res.Status.Configured || res.Status.Masked != "sk-...9876" {
		t.Fatalf("set openai_api = %+v, %v", res, err)
	}
	res, err = s.Apply(Change{Op: OpSet, Provider: ProviderGroq, APIKey: NewSecret(groqKey)})
	if err != nil || !res.Changed || !res.Status.Configured || res.Status.Masked != "gsk_...4321" {
		t.Fatalf("set groq = %+v, %v", res, err)
	}

	if !IsVoiceOnly(ProviderOpenRouterVoice) || !IsVoiceOnly(ProviderOpenAIAPI) || !IsVoiceOnly(ProviderGroq) || IsVoiceOnly(ProviderOpenRouter) || IsVoiceOnly(ProviderOpenAI) {
		t.Fatalf("IsVoiceOnly flags incorrect")
	}

	doc := readDoc(t, s.Path())
	if doc["future_meta"] == nil {
		t.Fatalf("future_meta lost: %v", doc)
	}
	provs := doc["providers"].(map[string]any)
	entry := provs["openai_api"].(map[string]any)
	if entry["custom_note"] != "keep-me" || entry["api_key"] != openAIKey {
		t.Fatalf("openai_api entry did not preserve unknown field: %v", entry)
	}

	cred, err := s.Credential(context.Background(), Request{Provider: ProviderGroq})
	if err != nil || cred.Secret.Reveal() != groqKey {
		t.Fatalf("groq credential = %+v, %v", cred, err)
	}
}

func TestServeTokenRefusesVoiceOnlyProviders(t *testing.T) {
	s := testStore(t)
	if _, err := s.Apply(Change{Op: OpSet, Provider: ProviderOpenRouterVoice, APIKey: NewSecret("sk-or-v1-fixture-voice-5555")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(Change{Op: OpSet, Provider: ProviderOpenAIAPI, APIKey: NewSecret("sk-proj-fixture-voice-9876")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(Change{Op: OpSet, Provider: ProviderGroq, APIKey: NewSecret("gsk_fixture_voice_key_4321")}); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []ProviderID{ProviderOpenRouterVoice, ProviderOpenAIAPI, ProviderGroq} {
		t.Run(string(pid), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, TokenRoutePath, bytes.NewBufferString(`{"provider":"`+string(pid)+`"}`))
			w := httptest.NewRecorder()
			ServeToken(w, req, s, Request{AgentSession: true})
			if w.Code != http.StatusForbidden {
				t.Fatalf("ServeToken(%s) status = %d, want 403 (%s)", pid, w.Code, w.Body.String())
			}
			var refusal tokenRefusal
			if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil || refusal.Error != RefusalNotPermitted {
				t.Fatalf("ServeToken(%s) body = %s", pid, w.Body.String())
			}
		})
	}
}
