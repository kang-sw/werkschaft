package wscred

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPhase3ClaudeSlotLoginSpecModesAndPreflight(t *testing.T) {
	harnesses := SlotLoginHarnesses()
	if !slices.Contains(harnesses, "codex") || !slices.Contains(harnesses, "claude") {
		t.Fatalf("SlotLoginHarnesses() = %v, want codex and claude", harnesses)
	}
	if slices.Contains(harnesses, "agy") {
		t.Fatalf("agy must stay single-slot (D15), got SlotLoginHarnesses() = %v", harnesses)
	}

	if got := SlotLoginModes("codex"); !slices.Equal(got, []SlotLoginMode{SlotLoginModeBrowser, SlotLoginModeDeviceAuth}) {
		t.Fatalf("SlotLoginModes(codex) = %v", got)
	}
	if got := SlotLoginModes("claude"); !slices.Equal(got, []SlotLoginMode{SlotLoginModeBrowser}) {
		t.Fatalf("SlotLoginModes(claude) = %v", got)
	}
	if got := SlotLoginModes("agy"); len(got) != 0 {
		t.Fatalf("SlotLoginModes(agy) = %v, want empty", got)
	}

	cmd, args, envKey, err := SlotLoginCommandSpec("claude", SlotLoginModeBrowser)
	if err != nil {
		t.Fatalf("SlotLoginCommandSpec(claude, browser): %v", err)
	}
	if cmd != "claude" || !slices.Equal(args, []string{"auth", "login"}) || envKey != "CLAUDE_CONFIG_DIR" {
		t.Fatalf("claude spec = (%q, %v, %q)", cmd, args, envKey)
	}
	if _, _, _, err := SlotLoginCommandSpec("claude", SlotLoginModeDeviceAuth); !errors.Is(err, ErrSlotLoginUnsupported) {
		t.Fatalf("claude device_auth err = %v, want ErrSlotLoginUnsupported", err)
	}

	// Managed claude slot directory permissions and preflight check.
	base := t.TempDir()
	slotDir, err := CreateManagedSlotDirUnder(base, "claude", "work")
	if err != nil {
		t.Fatalf("CreateManagedSlotDirUnder(claude): %v", err)
	}
	if SlotAuthReady("claude", slotDir) {
		t.Fatal("empty claude slot dir must not be auth-ready")
	}
	credPath := filepath.Join(slotDir, ".credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if SlotAuthReady("claude", slotDir) {
		t.Fatal("claude .credentials.json with empty tokens must not be auth-ready")
	}
	if err := os.WriteFile(credPath, []byte(`not-json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if SlotAuthReady("claude", slotDir) {
		t.Fatal("corrupt claude .credentials.json must not be auth-ready")
	}
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"tok_access","refreshToken":"tok_refresh","subscriptionType":"max"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !SlotAuthReady("claude", slotDir) {
		t.Fatal("valid claude .credentials.json should be auth-ready")
	}
}

func TestPhase3ParseSlotLoginLineClaudeAndAgyAndAuthCodeValidation(t *testing.T) {
	// Claude OAuth authorize URL with localhost redirect_uri.
	authURL, verifyURL, userCode, cbPort := ParseSlotLoginLine(
		"Open this URL in your browser: https://claude.ai/oauth/authorize?code=true&client_id=abc&redirect_uri=http%3A%2F%2Flocalhost%3A54545%2Fcallback&state=xyz",
	)
	if !strings.HasPrefix(authURL, "https://claude.ai/oauth/authorize?") || verifyURL != "" || userCode != "" || cbPort != 54545 {
		t.Fatalf("claude oauth parse = (%q, %q, %q, %d)", authURL, verifyURL, userCode, cbPort)
	}

	// Agy Authentication required line with Google OAuth URL.
	agyURL, _, _, agyPort := ParseSlotLoginLine(
		"Authentication required. Please visit the URL https://accounts.google.com/o/oauth2/v2/auth?client_id=test&redirect_uri=http%3A%2F%2F127.0.0.1%3A41234%2Foauth2callback&response_type=code",
	)
	if !strings.HasPrefix(agyURL, "https://accounts.google.com/o/oauth2/v2/auth?") || agyPort != 41234 {
		t.Fatalf("agy auth parse = (%q, %d)", agyURL, agyPort)
	}

	// Agy Authentication required line with custom domain (authHintLine trigger).
	hintURL, _, _, _ := ParseSlotLoginLine("Authentication required. Please visit the URL https://login.example.test/start?session=1.")
	if hintURL != "https://login.example.test/start?session=1" {
		t.Fatalf("hint URL = %q", hintURL)
	}

	// ValidateAuthCodeInput accepts normal codes and rejects empty, control chars, or oversized input.
	if got, err := ValidateAuthCodeInput("  abc-123_XYZ#state=456  "); err != nil || got != "abc-123_XYZ#state=456" {
		t.Fatalf("valid auth code = (%q, %v)", got, err)
	}
	for _, bad := range []string{
		"",
		"   ",
		"code-with\nnewline",
		"code-with\rcr",
		"code-with\x00nul",
		strings.Repeat("a", 4097),
	} {
		if _, err := ValidateAuthCodeInput(bad); !errors.Is(err, ErrSlotAuthCodeInvalid) {
			t.Fatalf("ValidateAuthCodeInput(%q) = %v, want ErrSlotAuthCodeInvalid", bad, err)
		}
	}
}
