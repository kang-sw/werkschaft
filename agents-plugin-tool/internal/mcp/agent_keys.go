package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsgit"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

// AgentKeyStore mints and keeps the ws session keys of the dashboard
// daemon's agent conversations (internal/wsagent). Each key is a
// delegate-scope key with no parent, rooted at the conversation's working
// root: lead-only tools, session.*, and config writes stay refused, the
// dashboard never registers it (only a top-level lead registers), and the
// attached ws-mcp's tool allowlist narrows it further.
type AgentKeyStore struct {
	store *sessionStore
}

// NewAgentKeyStore returns a store over this machine's key directory. The
// daemon supplies its held board store for hosted session-state rendering.
func NewAgentKeyStore(boards ...*WhiteboardStores) *AgentKeyStore {
	store := newSessionStore()
	if len(boards) > 0 {
		store.boards = boards[0]
	}
	return &AgentKeyStore{store: store}
}

// Root is the identity root config.get uses for this key's repo layer.
// In particular it preserves a child's recorded cwd binding across resumes.
func (a *AgentKeyStore) Root(key string) (string, bool) {
	entry, ok := a.store.lookup(key)
	return entry.root, ok
}

// GetOverride exposes the same layered session migration reads as config.get.
// The runner uses it only at process start, never per usage observation.
func (a *AgentKeyStore) GetOverride(key, item string) (string, bool) {
	return (sessionConfigAdapter{s: a.store}).GetOverride(key, item)
}

// Mint creates a new agent key rooted at root.
func (a *AgentKeyStore) Mint(root string) (string, error) {
	return a.store.mint(root, roleDelegate, "")
}

// MintChild creates a new agent key rooted at root that inherits
// session-scope overrides (review_phase, the sage knobs) from parentKey, the
// ws key of the lead or worker that spawned it. The key stays parentless for
// authority purposes: Ensure and scope checks are unchanged.
func (a *AgentKeyStore) MintChild(root, parentKey string) (string, error) {
	return a.store.mintWithPolicyParent(root, roleDelegate, "", parentKey)
}

// MintLead creates a parent-less lead key at root's canonical git root, the
// record a top-lead ferrule mint writes, so the session's later ferrule
// re-login at the same root accepts it. The daemon mints a hosted lead
// session's key with it and passes it as WS_HOSTED_SESSION_KEY; the key is
// never written into the dashboard registry, which records only its
// reply-id.
func (a *AgentKeyStore) MintLead(root string) (string, error) {
	canonical, err := canonicalSetupRoot(wsgit.ExecRunner{}, root)
	if err != nil {
		return "", err
	}
	return a.store.mint(canonical, roleLead, "")
}

// Ensure keeps key usable for root: an existing record (either layout) rooted
// at root is touched (key records are pruned by age), a missing one is
// written again with the same key in the split layout, and a record rooted
// elsewhere or of another scope is an error.
func (a *AgentKeyStore) Ensure(root, key string) error {
	return a.ensure(root, key, roleDelegate, "")
}

// LeadRoot is root's canonical git root, the root MintLead records; it
// fails for a folder outside a git worktree.
func (a *AgentKeyStore) LeadRoot(root string) (string, error) {
	return canonicalSetupRoot(wsgit.ExecRunner{}, root)
}

// EnsureLead is Ensure for a hosted lead session's key: canonical is the
// root MintLead recorded (LeadRoot).
func (a *AgentKeyStore) EnsureLead(canonical, key string) error {
	return a.ensure(canonical, key, roleLead, "")
}

// MintCompanionLead creates a parent-less lead key carrying a companion
// capability marker ("companion-body" or "companion-work"). Unlike MintLead,
// root may be any directory (the daemon state directory or a workspace root);
// when root is inside a git worktree it normalizes to its canonical git root.
func (a *AgentKeyStore) MintCompanionLead(root, role string) (string, error) {
	if role != CompanionRoleBody && role != CompanionRoleWork {
		return "", fmt.Errorf("invalid companion role %q", role)
	}
	canonical := root
	if gitRoot, err := canonicalSetupRoot(wsgit.ExecRunner{}, root); err == nil && gitRoot != "" {
		canonical = gitRoot
	}
	return a.store.mintWithCompanionRole(canonical, roleLead, "", "", role)
}

// EnsureCompanionLead keeps a companion session key usable with its marker.
func (a *AgentKeyStore) EnsureCompanionLead(canonical, key, role string) error {
	if role != CompanionRoleBody && role != CompanionRoleWork {
		return fmt.Errorf("invalid companion role %q", role)
	}
	return a.ensure(canonical, key, roleLead, role)
}

// LeadSession finds the parent-less lead key rooted at root whose reply-id
// is replyID, with the harness conversation its last login recorded
// (recordHarnessSession). It scans the key store's names, as the
// dashboard's session source does: no file maps a reply-id to its key.
func (a *AgentKeyStore) LeadSession(root, replyID string) (wsagent.LeadSession, bool) {
	d, err := a.store.dirs()
	if err != nil {
		return wsagent.LeadSession{}, false
	}
	keys, err := a.store.listKeys(d)
	if err != nil {
		return wsagent.LeadSession{}, false
	}
	for _, key := range keys {
		if wsmailbox.ReplyID(key) != replyID {
			continue
		}
		record, ok := a.store.readRecord(d, key, partKey)
		if !ok || record.Root != root || record.Scope != string(roleLead) || record.Parent != "" || record.CompanionRole != "" {
			continue
		}
		return wsagent.LeadSession{Key: key, Root: record.Root, Harness: record.Harness, Session: record.HarnessSession, Cwd: record.HarnessCwd}, true
	}
	return wsagent.LeadSession{}, false
}

func (a *AgentKeyStore) ensure(root, key string, scope toolRole, companionRole string) error {
	if !sessionKeyFilenamePattern.MatchString(key) {
		return fmt.Errorf("malformed session key")
	}
	d, err := a.store.dirs()
	if err != nil {
		return err
	}
	if record, ok := a.store.readRecord(d, key, partKey); ok {
		if record.Root != root || record.Scope != string(scope) || record.Parent != "" || record.CompanionRole != companionRole {
			return fmt.Errorf("session key record is not this conversation's (root %s, scope %s)", record.Root, record.Scope)
		}
		a.store.touch(d, key)
		a.store.rememberCompanionRole(key, companionRole)
		return nil
	}
	payload, err := json.Marshal(sessionKeyRecord{
		SchemaVersion: sessionRecordSchemaVersion,
		Root:          root,
		Scope:         string(scope),
		CompanionRole: companionRole,
	})
	if err != nil {
		return err
	}
	claimed, err := a.store.claimKey(d, key, payload)
	if err != nil {
		return err
	}
	if !claimed {
		return fmt.Errorf("session key record exists but is unreadable")
	}
	a.store.rememberCompanionRole(key, companionRole)
	return nil
}
