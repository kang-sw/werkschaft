package mcp

import (
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wscompact"
)

// compactDraftTTL is how long a rejected compact call's valid lead_summary
// fields stay available for a partial retry.
const compactDraftTTL = 15 * time.Minute

// compactDraftCache keeps, in server memory only, the valid lead_summary fields
// of a compact call that failed handler-side validation, keyed by the caller's
// session key. A retry may then resend only what changed. It is never written
// to disk, expires after compactDraftTTL, and is dropped once compact queues.
// The zero value is ready to use.
type compactDraftCache struct {
	mu      sync.Mutex
	now     func() time.Time // test clock; nil means time.Now
	entries map[string]compactDraftEntry
}

type compactDraftEntry struct {
	draft *wscompact.LeadDraft
	at    time.Time
}

func (c *compactDraftCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// pruneLocked drops every expired entry; callers hold c.mu.
func (c *compactDraftCache) pruneLocked(now time.Time) {
	for key, entry := range c.entries {
		if now.Sub(entry.at) >= compactDraftTTL {
			delete(c.entries, key)
		}
	}
}

// merge overlays draft onto the session's fresh cached draft and renews its TTL.
func (c *compactDraftCache) merge(key string, draft *wscompact.LeadDraft) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	c.pruneLocked(now)
	if c.entries == nil {
		c.entries = make(map[string]compactDraftEntry)
	}
	c.entries[key] = compactDraftEntry{draft: draft.Overlay(c.entries[key].draft), at: now}
}

// get returns a copy of the session's fresh cached draft, or nil.
func (c *compactDraftCache) get(key string) *wscompact.LeadDraft {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(c.clock())
	entry, ok := c.entries[key]
	if !ok {
		return nil
	}
	return entry.draft.Overlay(nil)
}

func (c *compactDraftCache) clear(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

func (c *compactDraftCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
}
