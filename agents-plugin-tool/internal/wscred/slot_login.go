package wscred

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// SlotLoginMode selects how a harness CLI signs into an isolated account slot (D18).
type SlotLoginMode string

const (
	SlotLoginModeBrowser    SlotLoginMode = "browser"
	SlotLoginModeDeviceAuth SlotLoginMode = "device_auth"
)

var (
	// ErrSlotLoginUnsupported reports a harness or login mode without CLI sign-in support.
	ErrSlotLoginUnsupported = errors.New("slot login mode unsupported")
	// ErrSlotCallbackInvalidURL reports a malformed or oversized pasted callback URL.
	ErrSlotCallbackInvalidURL = errors.New("invalid callback url")
	// ErrSlotCallbackInvalidHost reports a pasted callback URL pointing outside loopback.
	ErrSlotCallbackInvalidHost = errors.New("callback host must be loopback")
	// ErrSlotCallbackPortMismatch reports a pasted callback URL whose port does not match the active listener.
	ErrSlotCallbackPortMismatch = errors.New("callback port mismatch")
	// ErrSlotAuthCodeInvalid reports an empty, oversized, or control-character auth code paste.
	ErrSlotAuthCodeInvalid = errors.New("invalid auth code")
)

// SlotLoginHarnesses returns the harnesses currently enabled for account-slot
// provisioning and topbar switching.
func SlotLoginHarnesses() []string {
	return []string{"codex", "claude"}
}

// SlotLoginModes returns the interactive sign-in modes supported by harness.
func SlotLoginModes(harness string) []SlotLoginMode {
	switch harness {
	case "codex":
		return []SlotLoginMode{SlotLoginModeBrowser, SlotLoginModeDeviceAuth}
	case "claude":
		return []SlotLoginMode{SlotLoginModeBrowser}
	default:
		return nil
	}
}

// CreateManagedSlotDirUnder allocates <baseDir>/slots/<harness>/<slotID> with
// owner-only permissions (0700 on POSIX, protected User-SID DACL on Windows).
func CreateManagedSlotDirUnder(baseDir, harness, slotID string) (string, error) {
	if strings.TrimSpace(baseDir) == "" {
		return CreateManagedSlotDir("", harness, slotID)
	}
	if !SupportedSlotHarness(harness) {
		return "", fmt.Errorf("%w: unsupported harness %q", ErrInvalidAccountSlot, harness)
	}
	if !ValidAccountSlotID(slotID) || slotID == DefaultAccountSlotID {
		return "", fmt.Errorf("%w: invalid slot id %q", ErrInvalidAccountSlot, slotID)
	}
	dir := filepath.Join(filepath.Clean(baseDir), slotsDirName, harness, slotID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := RestrictPrivateDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// tempSlotDirs holds in-progress staging slot directories so deniedBasePaths
// blocks child fs.* access even before a sign-in finishes and registers the slot.
var tempSlotDirs = struct {
	mu   sync.RWMutex
	dirs map[string]int
}{
	dirs: map[string]int{},
}

// RegisterTempSlotDir adds dir to the in-memory deny set until the returned
// release function is called.
func RegisterTempSlotDir(dir string) func() {
	clean := filepath.Clean(strings.TrimSpace(dir))
	if clean == "" || clean == "." {
		return func() {}
	}
	tempSlotDirs.mu.Lock()
	tempSlotDirs.dirs[clean]++
	tempSlotDirs.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			tempSlotDirs.mu.Lock()
			if n := tempSlotDirs.dirs[clean]; n <= 1 {
				delete(tempSlotDirs.dirs, clean)
			} else {
				tempSlotDirs.dirs[clean] = n - 1
			}
			tempSlotDirs.mu.Unlock()
		})
	}
}

// SlotLoginCommandSpec returns the default executable, CLI args, and isolated
// home environment variable for signing into a harness account slot.
func SlotLoginCommandSpec(harness string, mode SlotLoginMode) (defaultCmd string, args []string, envKey string, err error) {
	if mode == "" {
		mode = SlotLoginModeBrowser
	}
	switch harness {
	case "codex":
		switch mode {
		case SlotLoginModeBrowser:
			return "codex", []string{"login"}, "CODEX_HOME", nil
		case SlotLoginModeDeviceAuth:
			return "codex", []string{"login", "--device-auth"}, "CODEX_HOME", nil
		default:
			return "", nil, "", ErrSlotLoginUnsupported
		}
	case "claude":
		switch mode {
		case SlotLoginModeBrowser:
			return "claude", []string{"auth", "login"}, "CLAUDE_CONFIG_DIR", nil
		default:
			return "", nil, "", ErrSlotLoginUnsupported
		}
	default:
		return "", nil, "", ErrSlotLoginUnsupported
	}
}

// SlotAuthReady reports whether configDir contains a readable, structurally
// valid authentication credential file for harness. Never returns or logs raw
// token material.
func SlotAuthReady(harness, configDir string) bool {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return false
	}
	switch harness {
	case "codex":
		raw, err := os.ReadFile(filepath.Join(configDir, "auth.json"))
		if err != nil {
			return false
		}
		var doc struct {
			OpenAIAPIKey string `json:"OPENAI_API_KEY"`
			Tokens       *struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
			} `json:"tokens"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			return false
		}
		if strings.TrimSpace(doc.OpenAIAPIKey) != "" {
			return true
		}
		return doc.Tokens != nil && (strings.TrimSpace(doc.Tokens.AccessToken) != "" || strings.TrimSpace(doc.Tokens.RefreshToken) != "")
	case "claude":
		raw, err := os.ReadFile(filepath.Join(configDir, ".credentials.json"))
		if err != nil {
			return false
		}
		var doc struct {
			OAuth *struct {
				AccessToken  string `json:"accessToken"`
				RefreshToken string `json:"refreshToken"`
			} `json:"claudeAiOauth"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			return false
		}
		return doc.OAuth != nil && (strings.TrimSpace(doc.OAuth.AccessToken) != "" || strings.TrimSpace(doc.OAuth.RefreshToken) != "")
	case "pi-rpc":
		raw, err := os.ReadFile(filepath.Join(configDir, "auth.json"))
		if err != nil {
			return false
		}
		var doc map[string]map[string]any
		if json.Unmarshal(raw, &doc) != nil {
			return false
		}
		for provider, entry := range doc {
			if strings.TrimSpace(provider) == "" || len(entry) == 0 {
				continue
			}
			for _, val := range entry {
				if s, ok := val.(string); ok && strings.TrimSpace(s) != "" {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}

var (
	ansiEscapeRe     = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	httpURLRe        = regexp.MustCompile(`https?://[^\s"'<>]+`)
	deviceUserCodeRe = regexp.MustCompile(`^[A-Z0-9]{4,5}-[A-Z0-9]{4,5}$`)
)

// ParseSlotLoginLine extracts OAuth/device URLs, one-time user codes, and local
// callback listener ports from one line of harness login CLI output (stripping
// ANSI escape sequences).
func ParseSlotLoginLine(line string) (authURL, verifyURL, userCode string, callbackPort int) {
	clean := ansiEscapeRe.ReplaceAllString(line, "")
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return "", "", "", 0
	}
	cleanLower := strings.ToLower(clean)
	authHintLine := strings.Contains(cleanLower, "authentication required") ||
		strings.Contains(cleanLower, "visit the url") ||
		strings.Contains(cleanLower, "open the url")
	for _, raw := range httpURLRe.FindAllString(clean, -1) {
		raw = strings.TrimRight(raw, ".,;:!?)")
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		switch strings.ToLower(u.Scheme) {
		case "http":
			host := strings.ToLower(u.Hostname())
			if host == "127.0.0.1" || host == "localhost" {
				if p, err := strconv.Atoi(u.Port()); err == nil && p > 0 && p <= 65535 {
					callbackPort = p
				}
			}
		case "https":
			pathLower := strings.ToLower(u.Path)
			hostLower := strings.ToLower(u.Hostname())
			if strings.Contains(pathLower, "/device") {
				verifyURL = raw
			} else if authHintLine ||
				strings.Contains(pathLower, "authorize") ||
				strings.Contains(pathLower, "/oauth") ||
				strings.Contains(hostLower, "auth.") ||
				hostLower == "claude.ai" ||
				hostLower == "console.anthropic.com" ||
				hostLower == "accounts.google.com" ||
				u.Query().Get("client_id") != "" {
				authURL = raw
				if redir := u.Query().Get("redirect_uri"); redir != "" {
					if ru, err := url.Parse(redir); err == nil {
						rhost := strings.ToLower(ru.Hostname())
						if strings.EqualFold(ru.Scheme, "http") && (rhost == "127.0.0.1" || rhost == "localhost") {
							if p, err := strconv.Atoi(ru.Port()); err == nil && p > 0 && p <= 65535 {
								callbackPort = p
							}
						}
					}
				}
			}
		}
	}
	if deviceUserCodeRe.MatchString(clean) {
		userCode = clean
	} else {
		for _, tok := range strings.Fields(clean) {
			trimmed := strings.Trim(tok, ".,;:!?()[]\"'`")
			if deviceUserCodeRe.MatchString(trimmed) {
				userCode = trimmed
				break
			}
		}
	}
	return authURL, verifyURL, userCode, callbackPort
}

// ValidateLoopbackCallbackReplay validates a pasted remote OAuth callback URL
// against expectedPort before replaying it to the local CLI login server (D18).
// It enforces strict SSRF safety: http scheme only, loopback host only
// (127.0.0.1 or localhost), no userinfo, and exact port match, returning a
// canonicalized http://127.0.0.1:<expectedPort><path>?<query> URL.
func ValidateLoopbackCallbackReplay(rawURL string, expectedPort int) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || len(rawURL) > 8192 {
		return "", ErrSlotCallbackInvalidURL
	}
	for i := 0; i < len(rawURL); i++ {
		if rawURL[i] < 0x20 || rawURL[i] == 0x7f {
			return "", ErrSlotCallbackInvalidURL
		}
	}
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Scheme, "http") || u.User != nil {
		return "", ErrSlotCallbackInvalidURL
	}
	host := strings.ToLower(u.Hostname())
	if host != "127.0.0.1" && host != "localhost" {
		return "", ErrSlotCallbackInvalidHost
	}
	portStr := u.Port()
	if portStr == "" || expectedPort <= 0 {
		return "", ErrSlotCallbackPortMismatch
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port != expectedPort {
		return "", ErrSlotCallbackPortMismatch
	}
	if u.Path == "" {
		u.Path = "/"
	}
	canon := &url.URL{
		Scheme:   "http",
		Host:     fmt.Sprintf("127.0.0.1:%d", expectedPort),
		Path:     u.Path,
		RawPath:  u.RawPath,
		RawQuery: u.RawQuery,
	}
	return canon.String(), nil
}

// ValidateAuthCodeInput validates a raw single-line authorization code pasted
// from a remote browser for stdin delivery to an interactive CLI login (D18).
// Callers must never log or echo the returned code.
func ValidateAuthCodeInput(raw string) (string, error) {
	code := strings.TrimSpace(raw)
	if code == "" || len(code) > 4096 {
		return "", ErrSlotAuthCodeInvalid
	}
	for i := 0; i < len(code); i++ {
		if code[i] < 0x20 || code[i] == 0x7f {
			return "", ErrSlotAuthCodeInvalid
		}
	}
	return code, nil
}
