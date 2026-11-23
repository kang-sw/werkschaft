package wsconfig

import "testing"

func TestParseWerkEndpoints(t *testing.T) {
	endpoints, err := ParseWerkEndpoints(`{"desk":{"base_url":"https://example.invalid/models/v1/","kind":"ollama"},"lab":{"base_url":"http://example.invalid/v1","kind":"vllm"}}`)
	if err != nil || len(endpoints) != 2 || endpoints["desk"].BaseURL != "https://example.invalid/models/v1" {
		t.Fatalf("endpoints = %+v, %v", endpoints, err)
	}
	for _, raw := range []string{
		`null`,
		`{} {}`,
		`{"Desk":{"base_url":"http://example.invalid/v1","kind":"ollama"}}`,
		`{"desk":{"base_url":"http://example.invalid/v1","kind":"other"}}`,
		`{"desk":{"base_url":"/v1","kind":"ollama"}}`,
		`{"desk":{"base_url":"http://user:secret@example.invalid/v1","kind":"ollama"}}`,
		`{"desk":{"base_url":"http://example.invalid/v1?key=secret","kind":"ollama"}}`,
		`{"desk":{"base_url":"http://example.invalid/v1#secret","kind":"ollama"}}`,
		`{"desk":{"base_url":"http://example.invalid/v1","kind":"ollama","api_key":"secret"}}`,
	} {
		if _, err := ParseWerkEndpoints(raw); err == nil {
			t.Errorf("accepted an invalid endpoint: %s", raw)
		}
	}
	if err := ValidateTierProvider("werk", "local/desk"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTierProvider("codex", "local/desk"); err == nil {
		t.Fatal("accepted a local Werk endpoint for another harness")
	}
}
