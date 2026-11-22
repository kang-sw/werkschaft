package wsmeta

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// Session directory generations, the identity a staged session row is kept under.

// generationOf is the platform's directory generation (gen_*.go; a test
// seam that simulates a missed recreation or a failed lookup). A lookup
// that fails is inconclusive: it never falls back to a weaker token. A
// weaker token would read as another incarnation and discard the session's
// journal rows, fenced originals included. A non-absence error fails the
// pass, which changes nothing, and the caller retries.
var generationOf = dirGeneration

// sessionGenError wraps an inconclusive generation lookup.
func sessionGenError(id string, err error) error {
	return fmt.Errorf("wsmeta: session %s identity unavailable (retry the pass): %w", id, err)
}

// sessionGenerations lists the session directories and their generations.
func (s *Store) sessionGenerations() (map[string]string, error) {
	out := map[string]string{}
	entries, err := wsstate.ReadDir(filepath.Join(s.dir, sessionsDirName))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !replyIDPattern.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		gen, err := generationOf(filepath.Join(s.dir, sessionsDirName, e.Name()), info)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, sessionGenError(e.Name(), err)
		}
		out[e.Name()] = gen
	}
	return out, nil
}

// sessionGeneration re-reads one session directory's generation; ok is
// false when it no longer exists as a directory.
func (s *Store) sessionGeneration(id string) (string, bool, error) {
	info, err := wsstate.Lstat(filepath.Join(s.dir, sessionsDirName, id))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() {
		return "", false, nil
	}
	gen, err := generationOf(filepath.Join(s.dir, sessionsDirName, id), info)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, sessionGenError(id, err)
	}
	return gen, true, nil
}
