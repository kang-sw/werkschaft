package wsusercmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/execjob"
)

// ValidateLine checks that line is non-blank, within MaxLine bytes, valid
// UTF-8, and free of NUL bytes.
func ValidateLine(line string) error {
	if strings.TrimSpace(line) == "" {
		return fmt.Errorf("%w: command line is required", ErrInvalid)
	}
	if len(line) > MaxLine {
		return fmt.Errorf("%w: command line is %d bytes, at most %d", ErrInvalid, len(line), MaxLine)
	}
	if !utf8.ValidString(line) || strings.ContainsRune(line, 0) {
		return fmt.Errorf("%w: command line is not valid text", ErrInvalid)
	}
	return nil
}

// ValidatePrompt checks optional prompt text bounds and encoding.
func ValidatePrompt(prompt string) error {
	if len(prompt) > MaxPrompt {
		return fmt.Errorf("%w: prompt is %d bytes, at most %d", ErrInvalid, len(prompt), MaxPrompt)
	}
	if !utf8.ValidString(prompt) || strings.ContainsRune(prompt, 0) {
		return fmt.Errorf("%w: prompt is not valid text", ErrInvalid)
	}
	return nil
}

// ValidateCwdSyntax checks optional cwd string bounds and single-line encoding
// without touching the filesystem.
func ValidateCwdSyntax(cwd string) error {
	if !utf8.ValidString(cwd) || strings.IndexFunc(cwd, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: cwd must be one line of text", ErrInvalid)
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	if len(cwd) > MaxCwd {
		return fmt.Errorf("%w: cwd is %d bytes, at most %d", ErrInvalid, len(cwd), MaxCwd)
	}
	return nil
}

// ValidateWorktreeCwd enforces the worktree-root cwd fence synchronously:
// empty cwd resolves to the worktree root; any non-empty cwd must be lexically
// contained within root (execjob.ResolveWorkingDir), must exist as a directory
// (os.Stat IsDir), and after resolving symlinks on both root and the target
// (filepath.EvalSymlinks) must remain inside the real worktree root.
func ValidateWorktreeCwd(root, cwd string) (string, error) {
	if err := ValidateCwdSyntax(cwd); err != nil {
		return "", err
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", nil
	}
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("%w: worktree root is required to validate cwd", ErrInvalid)
	}
	resolved, err := execjob.ResolveWorkingDir(root, cwd)
	if err != nil {
		return "", fmt.Errorf("%w: cwd must stay within the worktree root: %v", ErrInvalid, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: cwd must be an existing directory inside the worktree root: %v", ErrInvalid, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: cwd must be a directory", ErrInvalid)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve worktree root: %v", ErrInvalid, err)
	}
	realResolved, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: resolve cwd: %v", ErrInvalid, err)
	}
	if _, err := execjob.ResolveWorkingDir(realRoot, realResolved); err != nil {
		return "", fmt.Errorf("%w: cwd escapes the worktree root through a symlink: %v", ErrInvalid, err)
	}
	return cwd, nil
}
