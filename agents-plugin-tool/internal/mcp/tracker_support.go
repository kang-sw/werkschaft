package mcp

import (
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/wsagent"
)

// A tracker store's support conversation is a dashboard agent-runner
// conversation whose working root is the store's local clone and whose
// profile names the tracker tools. Its key is an agent key (delegate scope,
// no parent), which the tracker gate refuses like every spawned key unless
// trackerSupport recognises it; then it passes like a top-level lead, with
// its writes pinned to its own store.

// gitSearchTool is the read-only git search a support conversation reads
// the user's local repositories with.
const gitSearchTool = "git.search"

// hiddenUnlessAllowlisted reports a tool that is listed and callable only
// where WS_MCP_ALLOWED_TOOLS names it (a runner conversation whose profile
// allows it), never in an ordinary session's tool list: a lead gains
// nothing from these, and listing them everywhere would widen every
// session's tool surface.
func hiddenUnlessAllowlisted(name string) bool {
	return name == trackerShowTool || name == trackerUITool || name == gitSearchTool
}

// trackerSupportScope is what an admitted support conversation key may do.
type trackerSupportScope struct {
	// store is the conversation's own store: the registered store whose
	// directory is the conversation's working root.
	store string
	// tools are the conversation profile's ws tools.
	tools map[string]bool
}

// trackerSupport recognises key as a store's support conversation. The
// proof is the daemon's own conversation record (found through the key's
// reply-id and naming this exact key, open, at the key's root) and the
// profile that record names, resolved now; neither the key's scope alone
// (a ferrule lead login also mints parentless keys) nor a caller-settable
// environment variable admits a key. The pinned store is the registry entry
// whose directory is the conversation's root after canonicalisation; a
// conversation at no store's directory is not a support conversation.
func (s *Server) trackerSupport(key string, entry sessionEntry) (trackerSupportScope, bool, error) {
	if entry.scope != roleDelegate || entry.parent != "" {
		return trackerSupportScope{}, false, nil
	}
	rec, found, err := s.lookupAgentRecord(key)
	if err != nil {
		return trackerSupportScope{}, false, err
	}
	if !found || rec.WSKey != key || rec.ClosedAt != "" || canonicalDir(rec.Root) != canonicalDir(entry.root) {
		return trackerSupportScope{}, false, nil
	}
	rp, err := wsagent.ResolveProfile(s.configOptions(), rec.Root, rec.Profile, s.agentBackends()...)
	if err != nil {
		return trackerSupportScope{}, false, nil
	}
	tools := map[string]bool{}
	for _, name := range rp.Definition.WSTools {
		tools[name] = true
	}
	h, err := s.trackerHost()
	if err != nil {
		return trackerSupportScope{}, false, nil
	}
	stores, err := h.Stores()
	if err != nil {
		return trackerSupportScope{}, false, nil
	}
	root := canonicalDir(rec.Root)
	for _, e := range stores {
		if canonicalDir(e.Dir) == root {
			return trackerSupportScope{store: e.ID, tools: tools}, true, nil
		}
	}
	return trackerSupportScope{}, false, nil
}

// trackerSupportAdmits reports whether key is a support conversation whose
// profile names tool.
func (s *Server) trackerSupportAdmits(key string, entry sessionEntry, tool string) (bool, error) {
	scope, ok, err := s.trackerSupport(key, entry)
	if err != nil {
		return false, err
	}
	return ok && scope.tools[tool], nil
}

// trackerSupportReadsTracker reports whether key is a support conversation
// whose profile names any tracker tool, so its tracker manual section
// renders as for a top-level lead.
func (s *Server) trackerSupportReadsTracker(key string, entry sessionEntry) (bool, error) {
	scope, ok, err := s.trackerSupport(key, entry)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	for name := range scope.tools {
		if isTrackerTool(name) {
			return true, nil
		}
	}
	return false, nil
}

// canonicalDir is path with symlinks resolved, cleaned; a path that does
// not resolve is only cleaned.
func canonicalDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if evaluated, err := filepath.EvalSymlinks(path); err == nil {
		path = evaluated
	}
	return filepath.Clean(path)
}
