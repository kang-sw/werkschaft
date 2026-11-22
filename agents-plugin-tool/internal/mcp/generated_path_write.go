package mcp

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// generatedPathWrites records cache-artifact capabilities minted by
// path.generate. Repository-local plan paths and clone-shared documents are
// deliberately excluded: path.write exists only for sandbox-independent
// review/prompt handoffs.
type generatedPathWrites struct {
	mu    sync.RWMutex
	paths map[string]map[string]*generatedPathGrant
}

type generatedPathGrant struct {
	mu      sync.Mutex
	file    *os.File
	created time.Time
}

const maxGeneratedPathGrants = 64

// processGeneratedWrites is shared by the daemon's per-connection MCP
// servers, so an ordinary reconnect does not lose a session's minted path.
// Session keys are globally unique within the cache root; a daemon restart
// intentionally revokes these short-lived cache-write capabilities instead
// of introducing a persisted authority record.
var processGeneratedWrites generatedPathWrites

func (g *generatedPathWrites) grant(sessionKey string, generated []wsstate.GeneratedPath) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return fmt.Errorf("path.generate: session_key is required for writable cache artifacts")
	}
	eligible := make([]wsstate.GeneratedPath, 0, len(generated))
	for _, item := range generated {
		if item.Kind != "review" && item.Kind != "prompt" {
			continue
		}
		eligible = append(eligible, item)
	}
	// The descriptor budget bounds path.write capabilities, not path.generate:
	// a legacy batch larger than the budget still returns every path, and only
	// its newest maxGeneratedPathGrants entries become writable, matching the
	// oldest-first eviction a sequence of smaller batches would produce.
	if len(eligible) > maxGeneratedPathGrants {
		eligible = eligible[len(eligible)-maxGeneratedPathGrants:]
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paths == nil {
		g.paths = make(map[string]map[string]*generatedPathGrant)
	}

	// Every new grant is validated and opened before any earlier grant is
	// evicted, so a refused call leaves the existing grants intact.
	opened := make(map[string]*generatedPathGrant, len(eligible))
	for _, item := range eligible {
		before, err := os.Lstat(item.Path)
		if err != nil {
			closeGeneratedPathGrants(opened)
			return fmt.Errorf("path.generate: inspect writable cache artifact %q: %w", item.Path, err)
		}
		if !before.Mode().IsRegular() {
			closeGeneratedPathGrants(opened)
			return fmt.Errorf("path.generate: writable cache artifact %q is not a regular file", item.Path)
		}
		file, err := openGeneratedPathGrant(item.Path)
		if err != nil {
			closeGeneratedPathGrants(opened)
			return fmt.Errorf("path.generate: open writable cache artifact %q: %w", item.Path, err)
		}
		after, err := file.Stat()
		if err != nil || !os.SameFile(before, after) || (item.Created != nil && !os.SameFile(item.Created, after)) {
			_ = file.Close()
			closeGeneratedPathGrants(opened)
			return fmt.Errorf("path.generate: writable cache artifact %q changed while it was granted", item.Path)
		}
		opened[item.Path] = &generatedPathGrant{file: file, created: time.Now()}
	}
	for g.grantCountLocked()+len(opened) > maxGeneratedPathGrants {
		g.evictOldestLocked()
	}
	for path, grant := range opened {
		if g.paths[sessionKey] == nil {
			g.paths[sessionKey] = make(map[string]*generatedPathGrant)
		}
		if prior := g.paths[sessionKey][path]; prior != nil {
			prior.mu.Lock()
			_ = prior.file.Close()
			prior.mu.Unlock()
		}
		g.paths[sessionKey][path] = grant
	}
	return nil
}

// discardGeneratedCacheArtifacts removes the review/prompt reservations a
// refused path.generate call allocated, so a refusal withholds its paths
// without leaving their empty files behind. It removes only an entry that
// is still the exact empty regular file this call created (GeneratedPath.
// Created): content another process saved into the reservation, or an
// entry that replaced it, is left in place.
func discardGeneratedCacheArtifacts(generated []wsstate.GeneratedPath) {
	for _, item := range generated {
		if item.Kind != "review" && item.Kind != "prompt" || item.Created == nil {
			continue
		}
		current, err := os.Lstat(item.Path)
		if err != nil || !current.Mode().IsRegular() || current.Size() != 0 || !os.SameFile(item.Created, current) {
			continue
		}
		_ = os.Remove(item.Path)
	}
}

func closeGeneratedPathGrants(grants map[string]*generatedPathGrant) {
	for _, grant := range grants {
		_ = grant.file.Close()
	}
}

func (g *generatedPathWrites) grantCountLocked() int {
	total := 0
	for _, paths := range g.paths {
		total += len(paths)
	}
	return total
}

func (g *generatedPathWrites) evictOldestLocked() {
	var oldestKey, oldestPath string
	var oldest *generatedPathGrant
	for key, paths := range g.paths {
		for path, grant := range paths {
			if oldest == nil || grant.created.Before(oldest.created) {
				oldestKey, oldestPath, oldest = key, path, grant
			}
		}
	}
	if oldest == nil {
		return
	}
	delete(g.paths[oldestKey], oldestPath)
	if len(g.paths[oldestKey]) == 0 {
		delete(g.paths, oldestKey)
	}
	oldest.mu.Lock()
	_ = oldest.file.Close()
	oldest.mu.Unlock()
}

func (g *generatedPathWrites) write(sessionKey, path, content string) error {
	sessionKey = strings.TrimSpace(sessionKey)
	g.mu.Lock()
	grant := g.paths[sessionKey][path]
	if grant == nil {
		g.mu.Unlock()
		return fmt.Errorf("path.write refused: path was not generated for this session key as a writable cache artifact")
	}
	delete(g.paths[sessionKey], path)
	if len(g.paths[sessionKey]) == 0 {
		delete(g.paths, sessionKey)
	}
	g.mu.Unlock()
	grant.mu.Lock()
	defer func() {
		_ = grant.file.Close()
		grant.mu.Unlock()
	}()

	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("path.write inspect target: %w", err)
	}
	fileInfo, err := grant.file.Stat()
	if err != nil {
		return fmt.Errorf("path.write inspect granted artifact: %w", err)
	}
	if !pathInfo.Mode().IsRegular() || !fileInfo.Mode().IsRegular() || !os.SameFile(pathInfo, fileInfo) {
		return fmt.Errorf("path.write refused: generated target is not a regular file")
	}
	if err := grant.file.Truncate(0); err != nil {
		return fmt.Errorf("path.write truncate artifact: %w", err)
	}
	if _, err := grant.file.Seek(0, 0); err != nil {
		return fmt.Errorf("path.write seek artifact: %w", err)
	}
	if _, err := grant.file.WriteString(content); err != nil {
		return fmt.Errorf("path.write content: %w", err)
	}
	return nil
}
