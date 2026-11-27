// Package wstestutil holds Windows-safe test helpers shared across internal test
// suites. Production code must not import it.
package wstestutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CanonicalTempDir wraps t.TempDir() and resolves symlinks and Windows 8.3
// short path segments (such as RUNNER~1 vs runneradmin) via filepath.EvalSymlinks,
// falling back gracefully to cleaned absolute paths if resolution fails.
func CanonicalTempDir(t testing.TB) string {
	t.Helper()
	return CanonicalPath(t.TempDir())
}

// CanonicalPath resolves symlinks and Windows 8.3 short path segments for
// existing paths, or for the deepest existing ancestor when trailing path
// components do not exist yet, falling back gracefully to filepath.Clean.
func CanonicalPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	clean := filepath.Clean(trimmed)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = filepath.Clean(abs)
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != "" {
		return filepath.Clean(resolved)
	}
	// Walk upward to the deepest existing ancestor so non-existent suffixes under
	// a symlinked or Windows 8.3 short-path root (such as t.TempDir()) match
	// wsstate's canonicalized paths.
	cur := clean
	var tail []string
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil && resolved != "" {
			parts := make([]string, 0, 1+len(tail))
			parts = append(parts, resolved)
			for i := len(tail) - 1; i >= 0; i-- {
				parts = append(parts, tail[i])
			}
			return filepath.Clean(filepath.Join(parts...))
		}
		if !errors.Is(err, os.ErrNotExist) {
			break
		}
	}
	return clean
}

// CloseBeforeTempDir registers closer via t.Cleanup so open stores, SQLite
// databases, or file handles are closed BEFORE t.TempDir removes directories.
//
// Because testing.TB.Cleanup runs callbacks in LIFO order and the first
// t.TempDir call registers TempDir's os.RemoveAll cleanup, callers must obtain
// or create their TempDir (via CanonicalTempDir(t) or t.TempDir()) before
// calling CloseBeforeTempDir. Registering CloseBeforeTempDir afterwards
// guarantees closer runs first at test teardown, preventing Windows sharing
// violations during TempDir removal.
func CloseBeforeTempDir(t testing.TB, closer func() error) {
	if t == nil || closer == nil {
		return
	}
	t.Helper()
	t.Cleanup(func() {
		if err := closer(); err != nil {
			t.Errorf("close before TempDir cleanup: %v", err)
		}
	})
}
