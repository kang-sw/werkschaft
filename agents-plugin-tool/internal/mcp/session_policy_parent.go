package mcp

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// A daemon-spawned worker's key is a root of its own (no Parent: it is never
// a top-level lead, and Ensure accepts only parentless records), yet the lead
// that spawned it set review_phase and the sage knobs for it. The key names
// that lead as its policy parent, and only the override lineage (walkLineage)
// follows the link; authority, scope and adjacency never do.
//
// The link is its own file in the key directory, outside recordParts, like
// the sub-lead marker (session_sublead.go): writeParts rewrites and removes
// only part files, so a key.json rewrite by any ws version, an older daemon
// included (its first mailbox.send on the key, a todo or agenda write),
// never drops it. An older daemon never opens the file and resolves the key's
// own overrides and the builtin defaults, as it does for every spawned key.
// Only removing the whole key directory (prune after the retention age, or a
// failed claim) removes it.
const policyParentName = "policy_parent.json"

// policyParentMarker is the file's shape.
type policyParentMarker struct {
	Key string `json:"key"`
}

// policyParentEntry is one key's memoized marker read (sessionStore.
// policyParent): an empty key caches an unmarked key too, so the common
// parentless lead's override reads skip the file.
type policyParentEntry struct{ key string }

// writePolicyParent records that key inherits session-scope overrides from
// parentKey. It runs right after the mint, before the key is handed out.
func (s *sessionStore) writePolicyParent(d sessionDirs, key, parentKey string) error {
	payload, err := json.Marshal(policyParentMarker{Key: parentKey})
	if err != nil {
		return err
	}
	if err := s.writeFileAtomic(filepath.Join(d.keyDir(key), policyParentName), payload); err != nil {
		return err
	}
	s.policyParent.Store(key, policyParentEntry{key: parentKey})
	return nil
}

// policyParentOf returns the key whose overrides key inherits, "" when it has
// none. The marker is written once, before the key is handed out, and leaves
// only with the whole key directory, so the first read is memoized for the
// process: steady-state override reads open no file. A cached answer cannot
// go stale for a live key; a key name re-claimed after a prune drops its
// entry in claimKey. Only an absent or malformed marker is cached as
// unmarked; any other read failure (a transient sharing violation) is not
// cached and reads as unmarked for this call only.
func (s *sessionStore) policyParentOf(d sessionDirs, key string) (string, bool) {
	if v, ok := s.policyParent.Load(key); ok {
		parent := v.(policyParentEntry).key
		return parent, parent != ""
	}
	if !sessionKeyFilenamePattern.MatchString(key) {
		return "", false
	}
	entry := policyParentEntry{}
	data, err := wsstate.ReadFileOnce(filepath.Join(d.keyDir(key), policyParentName))
	switch {
	case err == nil:
		var marker policyParentMarker
		if json.Unmarshal(data, &marker) == nil {
			entry.key = strings.TrimSpace(marker.Key)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return "", false
	}
	s.policyParent.Store(key, entry)
	return entry.key, entry.key != ""
}
