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

var _ accountSlotContinuity = ClaudeBackend{}

func (ClaudeBackend) slotAuthPreflight(configDir string) error {
	return claudeSlotAuthPreflight(configDir)
}

func (ClaudeBackend) migrateSlotSession(srcConfigDir, dstConfigDir, sessionID string) error {
	return copyClaudeTranscript(srcConfigDir, dstConfigDir, sessionID)
}

func (ClaudeBackend) isSlot2aTurnRejection(err error) bool {
	return isClaude2aTurnRejection(err)
}

// claudeSlotAuthPreflight verifies that configDir holds a readable, valid
// .credentials.json with non-empty OAuth tokens before attempting cross-slot
// --resume (D2). Never returns or logs raw token material.
func claudeSlotAuthPreflight(configDir string) error {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return errors.New("claude_slot_home_unresolved")
	}
	raw, err := os.ReadFile(filepath.Join(configDir, ".credentials.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errors.New("claude_slot_unauthenticated")
		}
		return errors.New("claude_slot_auth_unreadable")
	}
	var doc struct {
		OAuth *struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return errors.New("claude_slot_auth_corrupt")
	}
	if doc.OAuth != nil && (strings.TrimSpace(doc.OAuth.AccessToken) != "" || strings.TrimSpace(doc.OAuth.RefreshToken) != "") {
		return nil
	}
	return errors.New("claude_slot_unauthenticated")
}

// copyClaudeTranscript copies only the session transcript JSONL for sessionID
// from srcConfigDir/projects/... into dstConfigDir/projects/..., preserving its
// relative path. It never copies .credentials.json, .claude.json, or settings
// files (Phase 3 D2 / D14).
func copyClaudeTranscript(srcConfigDir, dstConfigDir, sessionID string) error {
	srcConfigDir = strings.TrimSpace(srcConfigDir)
	dstConfigDir = strings.TrimSpace(dstConfigDir)
	sessionID = strings.TrimSpace(sessionID)
	if srcConfigDir == "" || dstConfigDir == "" || sessionID == "" {
		return errors.New("claude_transcript_missing_args")
	}
	if filepath.Clean(srcConfigDir) == filepath.Clean(dstConfigDir) {
		return nil
	}
	srcProjects := filepath.Join(srcConfigDir, "projects")
	exactName := sessionID + ".jsonl"
	var foundRel string
	err := filepath.WalkDir(srcProjects, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.Name() == exactName {
			rel, relErr := filepath.Rel(srcProjects, p)
			if relErr == nil {
				foundRel = rel
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan claude projects: %w", err)
	}
	if foundRel == "" {
		return errors.New("claude_transcript_not_found")
	}
	srcPath := filepath.Join(srcProjects, foundRel)
	dstPath := filepath.Join(dstConfigDir, "projects", foundRel)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return fmt.Errorf("create target claude projects dir: %w", err)
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open claude transcript: %w", err)
	}
	defer in.Close()
	data, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("read claude transcript: %w", err)
	}
	if err := writeFileAtomic(dstPath, data, 0o600); err != nil {
		return fmt.Errorf("write target claude transcript: %w", err)
	}
	return nil
}

// isClaude2aTurnRejection reports whether a post-resume Claude turn error
// indicates an upstream auth or cross-account session rejection that should
// trigger the automatic 2a -> 2b rotateExternalSession fallback (D2).
func isClaude2aTurnRejection(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"unauthorized",
		"forbidden",
		"401",
		"403",
		"authentication_error",
		"invalid_auth",
		"not logged in",
		"please run /login",
		"account mismatch",
		"session not found",
		"no conversation found",
		"invalid session",
		"does not belong to",
		"not owned by",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
