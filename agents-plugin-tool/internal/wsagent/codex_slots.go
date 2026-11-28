package wsagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	_ accountSlotContinuity = CodexBackend{}
	_ slotHomeBinder        = CodexBackend{}
)

func (b CodexBackend) withSlotHomeResolver(resolve func() string) Backend {
	if b.ResolveCodexHome != nil || resolve == nil {
		return b
	}
	b.ResolveCodexHome = resolve
	return b
}

func (CodexBackend) slotAuthPreflight(configDir string) error {
	return codexSlotAuthPreflight(configDir)
}

func (CodexBackend) migrateSlotSession(srcConfigDir, dstConfigDir, sessionID string) error {
	return copyCodexRollout(srcConfigDir, dstConfigDir, sessionID)
}

func (CodexBackend) isSlot2aTurnRejection(err error) bool {
	return isCodex2aTurnRejection(err)
}

// codexSlotAuthPreflight verifies that codexHome holds a readable, valid
// auth.json with non-empty auth credentials before relying on thread/resume
// (D2 / Phase 1 Spike Findings: thread/resume succeeds locally even with a
// missing or corrupt auth.json). Never returns or logs raw token material.
func codexSlotAuthPreflight(codexHome string) error {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return errors.New("codex_slot_home_unresolved")
	}
	raw, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errors.New("codex_slot_unauthenticated")
		}
		return errors.New("codex_slot_auth_unreadable")
	}
	var doc struct {
		OpenAIAPIKey string `json:"OPENAI_API_KEY"`
		Tokens       *struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return errors.New("codex_slot_auth_corrupt")
	}
	if strings.TrimSpace(doc.OpenAIAPIKey) != "" {
		return nil
	}
	if doc.Tokens != nil && (strings.TrimSpace(doc.Tokens.AccessToken) != "" || strings.TrimSpace(doc.Tokens.RefreshToken) != "") {
		return nil
	}
	return errors.New("codex_slot_unauthenticated")
}

// copyCodexRollout copies only the rollout transcript JSONL for sessionID from
// srcHome/sessions/... into dstHome/sessions/..., preserving its relative path.
// It never copies auth.json, state_5.sqlite, or config.toml (Phase 1 spike & D14).
func copyCodexRollout(srcHome, dstHome, sessionID string) error {
	srcHome = strings.TrimSpace(srcHome)
	dstHome = strings.TrimSpace(dstHome)
	sessionID = strings.TrimSpace(sessionID)
	if srcHome == "" || dstHome == "" || sessionID == "" {
		return errors.New("codex_rollout_missing_args")
	}
	if filepath.Clean(srcHome) == filepath.Clean(dstHome) {
		return nil
	}
	srcSessions := filepath.Join(srcHome, "sessions")
	suffixDash := "-" + sessionID + ".jsonl"
	exactName := sessionID + ".jsonl"
	var foundRel string
	err := filepath.WalkDir(srcSessions, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, suffixDash) || name == exactName {
			rel, relErr := filepath.Rel(srcSessions, p)
			if relErr == nil {
				foundRel = rel
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan codex sessions: %w", err)
	}
	if foundRel == "" {
		return errors.New("codex_rollout_not_found")
	}
	srcPath := filepath.Join(srcSessions, foundRel)
	dstPath := filepath.Join(dstHome, "sessions", foundRel)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return fmt.Errorf("create target codex sessions dir: %w", err)
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open codex rollout: %w", err)
	}
	defer in.Close()
	data, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("read codex rollout: %w", err)
	}
	if err := writeFileAtomic(dstPath, data, 0o600); err != nil {
		return fmt.Errorf("write target codex rollout: %w", err)
	}
	return nil
}

// isCodex2aTurnRejection reports whether a post-resume turn/start error looks
// like an upstream cross-account or session rejection that should trigger the
// automatic 2a -> 2b rotateExternalSession fallback (D2).
func isCodex2aTurnRejection(err error) bool {
	if err == nil {
		return false
	}
	var rpcErr *CodexRPCError
	if errors.As(err, &rpcErr) {
		if rpcErr.HasCode && (rpcErr.Code == -32600 || rpcErr.Code == 401 || rpcErr.Code == 403) {
			return true
		}
		info := strings.ToLower(strings.TrimSpace(rpcErr.Info))
		switch info {
		case "unauthorized", "forbidden", "authentication_error", "invalid_auth",
			"account_mismatch", "session_not_found", "thread_not_found", "invalid_request":
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"unauthorized",
		"forbidden",
		"401",
		"403",
		"account mismatch",
		"thread not found",
		"session not found",
		"invalid session",
		"invalid thread",
		"does not belong to",
		"not owned by",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
