package wsdashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmeta"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

func (s agentSessions) ReadNotes(reg Registration, refresh bool) ([]NotesLayer, error) {
	if src, ok := s.inner.(NotesSource); ok {
		return src.ReadNotes(reg, refresh)
	}
	return nil, fmt.Errorf("notes unavailable")
}

// AgentHost is the daemon's agent runner (internal/wsagent), which hosts
// headless agent conversations as dashboard sessions. The daemon mounts its
// routes, starts it once the daemon holds its lock, tells it when a session
// page wrote to an agent conversation's inbox, and closes it on shutdown.
type AgentHost interface {
	// Handler serves AgentRoutePrefix and everything under it.
	Handler() http.Handler
	// Start loads the conversations; notify re-reads one session.
	Start(notify func(replyID, project string))
	// InboxChanged follows a session page's inbox write on a conversation.
	InboxChanged(replyID string)
	// Close ends every conversation's process.
	Close()
}

// AgentStartupHost restores held sessions before publication and starts
// closed-session presentation recovery when the listener accepts requests.
type AgentStartupHost interface {
	StartForPublication(notify func(replyID, project string), serving <-chan struct{})
}

// AgentPageInputHost distinguishes an explicit composer send from passive
// inbox notifications. PageInput admits write against the conversation's
// in-memory lifetime, then asynchronously restores a hidden session only if
// write committed. Archive restoration must not be inferred from a nudge.
type AgentPageInputHost interface {
	PageInput(replyID string, write func() bool) error
}

// AgentEndpointHost is an AgentHost whose conversations call back on the
// daemon's routes: the daemon gives it its loopback base URL
// ("http://127.0.0.1:<port>") before Start.
type AgentEndpointHost interface {
	SetEndpoint(baseURL string)
}

// AgentMCPTokenHost is an AgentHost whose hosted sessions reach the daemon's
// ws MCP server over HTTP (ProxyHTTPPath) with a per-session token.
type AgentMCPTokenHost interface {
	// ResolveMCPToken resolves a live per-session MCP token to the session's
	// per-connection context, the hello a stdio launcher would have sent
	// (mode, tool allowlist in its env, identity), and a channel closed once
	// the token is void (its process ended, a new start re-issued it, or the
	// conversation closed). ok is false for an unknown or void token.
	ResolveMCPToken(token string) (hello ProxyHello, void <-chan struct{}, ok bool)
}

// AgentMCPTokenDiagnostics is an optional AgentMCPTokenHost that reports a
// reason for a failed resolution. The HTTP route logs the reason locally,
// so implementations must use fixed messages without request or record data.
// An empty reason means the token resolved.
type AgentMCPTokenDiagnostics interface {
	ResolveMCPTokenWithReason(token string) (hello ProxyHello, void <-chan struct{}, reason string)
}

// AgentMCPCallHost is an optional AgentMCPTokenHost that counts the tool
// calls in flight on a session token: MCPCallBegin starts one and returns
// its end, which the route calls once the call is answered or abandoned
// (its client went away, or the session ended). The end is idempotent.
type AgentMCPCallHost interface {
	MCPCallBegin(token string) (end func())
}

// AgentLiveHost is an optional AgentHost whose native lead snapshots are
// delivered in RAM. The daemon binds it before Start; no wire protocol changes.
type AgentLiveHost interface {
	SetLiveHost(LiveHost)
}

// AgentAuditHost receives the daemon's audit sink before Start.
type AgentAuditHost interface {
	SetAuditSink(wsaudit.Sink)
}

// AgentMetaHost is an optional AgentHost that binds the daemon's shared SQLite
// metadata store and startup snapshot before Start.
type AgentMetaHost interface {
	SetMeta(store *wsmeta.Store, snap *wsmeta.Snapshot)
}

// AgentRecordValidatorHost is an optional AgentHost that supplies the legacy
// agent.json validator for the one-time SQLite cutover.
type AgentRecordValidatorHost interface {
	ValidateLegacyRecord(rel, replyID string, raw []byte) error
}

// SupportMetaHost is an optional SupportHost that defers loading support state
// until StartDaemon acquires dashboard.lock and settles SQLite vs legacy mode.
type SupportMetaHost interface {
	LoadFromMeta(store *wsmeta.Store, snap *wsmeta.Snapshot)
	LoadLegacy()
}

// AgentForgetter is an optional AgentHost that drops a closed or handed-off
// conversation when the daemon purges its session directory. ForgetClosed
// returns whether the daemon may remove the directory.
type AgentForgetter interface {
	ForgetClosed(replyID string) bool
}

// AgentForgetChecker is an optional AgentForgetter that can check whether a
// closed or handed-off conversation is eligible to be forgotten without
// mutating runner, durable, or SQLite state. Callers check CanForgetClosed
// before removing the session directory and call ForgetClosed only after
// removal succeeds.
type AgentForgetChecker interface {
	AgentForgetter
	CanForgetClosed(replyID string) bool
}

// AgentHistoryPurger prepares archive deletion before Git removes the
// worktree and completes it after the corresponding history is removed.
type AgentHistoryPurger interface {
	PrepareWorktreeHistoryPurge(worktree string, ended map[string]bool) error
	CompleteWorktreeHistoryPurge(worktree string)
}

// AgentSessionMeta holds a runner conversation's in-memory metadata for
// dashboard rendering without touching disk.
type AgentSessionMeta struct {
	Hosted       bool
	IsChild      bool
	Closed       bool
	Active       bool
	HasProcess   bool
	SafeStop     bool
	Backend      string
	Model        string
	Effort       string
	LastActiveAt string
}

// AgentSessionMetaHost is an optional AgentHost that exposes in-memory
// metadata for the session view and rail.
type AgentSessionMetaHost interface {
	SessionMeta(replyID string) (AgentSessionMeta, bool)
}

// AgentRecordParent mirrors wsagent.ParentRef without importing wsagent.
type AgentRecordParent struct {
	Kind    string `json:"kind"`
	ReplyID string `json:"reply_id"`
}

// AgentRecordSnapshot is the daemon-owned metadata snapshot served over the
// authenticated loopback /meta/* routes and read in-process by page rendering
// and registry pruning. WSKey is retained in memory for caller authentication
// and never serialized onto HTTP responses.
type AgentRecordSnapshot struct {
	ReplyID            string             `json:"reply_id,omitempty"`
	WSKey              string             `json:"-"`
	Profile            string             `json:"profile,omitempty"`
	ResolvedDefinition json.RawMessage    `json:"resolved_definition,omitempty"`
	Backend            string             `json:"backend,omitempty"`
	Root               string             `json:"root,omitempty"`
	Model              string             `json:"model,omitempty"`
	Effort             string             `json:"effort,omitempty"`
	Lead              bool               `json:"lead,omitempty"`
	ConvHistoryAccess bool               `json:"conv_history_access,omitempty"`
	ClosedAt          string             `json:"closed_at,omitempty"`
	HandedOffAt        string             `json:"handed_off_at,omitempty"`
	HolderID           string             `json:"holder_id,omitempty"`
	HostPID            int                `json:"host_pid,omitempty"`
	Tier               string             `json:"tier,omitempty"`
	Parent             *AgentRecordParent `json:"parent,omitempty"`
}

// AgentMetadataHost is an optional AgentHost that answers in-memory agent
// record lookups without reading agent.json from disk.
type AgentMetadataHost interface {
	AgentRecord(replyID string) (AgentRecordSnapshot, bool)
}

// AgentConvHost is an optional AgentHost that exposes live conversation
// history access status and lead session revival for conv.* MCP tools.
type AgentConvHost interface {
	ConvHistoryAccess(replyID string) bool
	ReviveLead(replyID string) error
}

// AccountSlotConversation is an in-memory projection of one open hosted
// conversation's current and queued account slot binding.
type AccountSlotConversation struct {
	ID                   string `json:"id"`
	Backend              string `json:"backend"`
	Root                 string `json:"root,omitempty"`
	Label                string `json:"label,omitempty"`
	Lead                 bool   `json:"lead,omitempty"`
	Status               string `json:"status,omitempty"`
	Process              bool   `json:"process,omitempty"`
	AccountSlotID        string `json:"account_slot_id"`
	PendingAccountSlotID string `json:"pending_account_slot,omitempty"`
}

// AccountSlotSwitchResult describes the outcome of queuing or applying an
// account slot switch on a conversation or harness.
type AccountSlotSwitchResult struct {
	ConversationID       string `json:"conversation_id,omitempty"`
	AccountSlotID        string `json:"account_slot_id"`
	PendingAccountSlotID string `json:"pending_account_slot,omitempty"`
	// Mode is "queued", "restarted_2a", "rotated_2b", "immediate", or "unchanged".
	Mode         string `json:"mode"`
	QueuedCount  int    `json:"queued_count,omitempty"`
	SkippedCount int    `json:"skipped_count,omitempty"`
}

// AgentAccountSlotHost is an optional AgentHost that exposes per-conversation
// account slot bindings and applies harness-wide or per-conversation switches.
type AgentAccountSlotHost interface {
	AccountSlotConversations(harness string) []AccountSlotConversation
	QueueHarnessAccountSlot(harness, slotID string) (queued, skipped int, err error)
	QueueConversationAccountSlot(id, slotID string) (AccountSlotSwitchResult, error)
	SwitchConversationAccountSlotNow(id, slotID string) (AccountSlotSwitchResult, error)
}

// MetaPruner is an optional LiveHost that prunes expired registrations for its
// own state directory using daemon-owned agent metadata in memory.
type MetaPruner interface {
	PruneRegistrationsFor(stateDir string, now time.Time, retention time.Duration) (pruned int, handled bool)
}

// AgentRoutePrefix is where the daemon mounts AgentHost.Handler.
const AgentRoutePrefix = "/agents"

// SupportRoutePrefix is where the daemon mounts DaemonOptions.Support.
const SupportRoutePrefix = "/support"

// agentSessions is the daemon's session source: agent registrations are
// read from their live stores alone (no ws-mcp serves them and they have no
// session record), and every other one from the configured source.
type agentSessions struct {
	inner SessionSource
	dir   string
	now   func() time.Time
	// snaps is the daemon's snapshot cache; nil reads every time.
	snaps *SnapshotCache
	// hosted reports whether a live hosted slot currently owns replyID;
	// hosted sessions merge any external ring appends on explicit rereads.
	hosted func(replyID string) bool
	// onNonHostedRead reconciles an unpinned non-hosted session's ring into
	// the conversation index when ReadSession refreshes it.
	onNonHostedRead func(replyID string)
}

func (s agentSessions) ReadSession(reg Registration) (SessionData, bool, error) {
	// This is an authoritative index read, already coalesced under its lane,
	// not a presentation read. Refresh the daemon's snapshot owner even for
	// leads whose fingerprint uses the inner source's separate cache: one
	// nudge must not disappear behind either owner's presentation TTL.
	s.snaps.Invalidate(s.dir, reg.ReplyID)
	if s.hosted != nil && s.hosted(reg.ReplyID) {
		wslive.SyncExternalRing(wslive.RingDir(SessionDir(s.dir, reg.ReplyID)))
	} else if s.onNonHostedRead != nil {
		s.onNonHostedRead(reg.ReplyID)
	}
	if !reg.Agent {
		s.snaps.snapshotWithoutObserved(s.dir, reg.ReplyID)
		s.snaps.warmOutputRate(s.dir, reg.ReplyID)
		return s.inner.ReadSession(reg)
	}
	if _, err := os.Stat(SessionDir(s.dir, reg.ReplyID)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SessionData{}, false, nil
		}
		return SessionData{}, false, err
	}
	s.snaps.warmOutputRate(s.dir, reg.ReplyID)
	h := sha256.New()
	stores, updatedAt, staleAt := s.snaps.ReadLiveStoresClock(h, s.dir, reg.ReplyID, s.now(), LiveStoreFingerprint{}, AllLiveStores)
	return SessionData{Fingerprint: hex.EncodeToString(h.Sum(nil)[:16]), LiveStores: stores, SnapshotUpdatedAt: updatedAt, SnapshotStaleAt: staleAt}, true, nil
}

// ReadLiveSession bypasses the inner source's non-live stores for runner
// writes. Legacy/custom sources retain their ordinary read-through behavior.
func (s agentSessions) ReadLiveSession(reg Registration, previous SessionData, stores LiveStores) (SessionData, bool, error) {
	s.snaps.Invalidate(s.dir, reg.ReplyID)
	if !reg.Agent {
		s.snaps.snapshotWithoutObserved(s.dir, reg.ReplyID)
		s.snaps.warmOutputRate(s.dir, reg.ReplyID)
		if live, ok := s.inner.(LiveSessionSource); ok {
			return live.ReadLiveSession(reg, previous, stores)
		}
		return s.inner.ReadSession(reg)
	}
	s.snaps.warmOutputRate(s.dir, reg.ReplyID)
	h := sha256.New()
	previous.LiveStores, previous.SnapshotUpdatedAt, previous.SnapshotStaleAt = s.snaps.ReadLiveStoresClock(h, s.dir, reg.ReplyID, s.now(), previous.LiveStores, stores)
	previous.Fingerprint = hex.EncodeToString(h.Sum(nil)[:16])
	return previous, true, nil
}

func (s agentSessions) ReadPage(reg Registration) (PageContent, bool, error) {
	return s.ReadPageLang(reg, "")
}

func (s agentSessions) ReadPageLang(reg Registration, lang string) (PageContent, bool, error) {
	if reg.Agent {
		return PageContent{}, true, nil
	}
	if ls, ok := s.inner.(LangPageSource); ok {
		return ls.ReadPageLang(reg, lang)
	}
	if ps, ok := s.inner.(PageSource); ok {
		return ps.ReadPage(reg)
	}
	return PageContent{}, true, nil
}

// ReadContacts forwards to the configured source; an agent conversation
// has no contacts.
func (s agentSessions) ReadContacts(reg Registration) ([]Contact, bool) {
	if reg.Agent {
		return nil, true
	}
	if cs, ok := s.inner.(ContactSource); ok {
		return cs.ReadContacts(reg)
	}
	return nil, false
}

func (s agentSessions) LockBoardIncarnation(reg Registration, token string) (func(), bool) {
	if source, ok := s.inner.(BoardInputSource); ok && !reg.Agent {
		return source.LockBoardIncarnation(reg, token)
	}
	return nil, false
}

// RecordDashboardAnswer forwards board-answer arrivals for lead sessions.
func (s agentSessions) RecordDashboardAnswer(reg Registration, id string, now time.Time) error {
	if reg.Agent {
		return nil
	}
	if source, ok := s.inner.(DashboardAnswerSource); ok {
		return source.RecordDashboardAnswer(reg, id, now)
	}
	return nil
}

// BoardItems forwards to the configured source (BoardItemsSource); an agent
// conversation has no board.
func (s agentSessions) BoardItems(reg Registration) (BoardItems, bool) {
	if reg.Agent {
		return BoardItems{}, true
	}
	if bs, ok := s.inner.(BoardItemsSource); ok {
		return bs.BoardItems(reg)
	}
	return BoardItems{}, false
}

func (s agentSessions) LookupMockup(reg Registration, entryID string) (MockupLookup, bool, error) {
	if reg.Agent {
		return MockupLookup{}, false, nil
	}
	if ms, ok := s.inner.(BoardMockupSource); ok {
		return ms.LookupMockup(reg, entryID)
	}
	return MockupLookup{}, false, nil
}

func (s agentSessions) ReadMockupBundle(reg Registration, entryID string) (wswhiteboard.MockupBundle, bool, error) {
	if reg.Agent {
		return wswhiteboard.MockupBundle{}, false, nil
	}
	if ms, ok := s.inner.(BoardMockupSource); ok {
		return ms.ReadMockupBundle(reg, entryID)
	}
	return wswhiteboard.MockupBundle{}, false, nil
}

func (s agentSessions) SetMockupEvictor(fn func(boardDir string)) {
	if ms, ok := s.inner.(BoardMockupSource); ok {
		ms.SetMockupEvictor(fn)
	}
}

// readsBoardItems reports whether the configured source reads board items
// (BoardItemsSource), so the answer check skips a session it could never
// judge before reading anything else of it.
func (s agentSessions) readsBoardItems() bool {
	_, ok := s.inner.(BoardItemsSource)
	return ok
}

// MailPrint forwards to the configured source (MailPrintSource); an agent
// conversation has no mailbox.
func (s agentSessions) MailPrint(reg Registration) (string, bool) {
	if reg.Agent {
		return "", true
	}
	if ms, ok := s.inner.(MailPrintSource); ok {
		return ms.MailPrint(reg)
	}
	return "", false
}

// Counters forwards to the configured source (CounterSource).
func (s agentSessions) Counters() map[string]int64 {
	if cs, ok := s.inner.(CounterSource); ok {
		return cs.Counters()
	}
	return nil
}

// ForgetSession forwards to the configured source (SessionForgetter).
func (s agentSessions) ForgetSession(replyID string) {
	if f, ok := s.inner.(SessionForgetter); ok {
		f.ForgetSession(replyID)
	}
}

// SetLiveSnapshot forwards to the configured source (LiveSnapshotHolder).
func (s agentSessions) SetLiveSnapshot(stateDir, replyID string, snap wslive.Snapshot) {
	if h, ok := s.inner.(LiveSnapshotHolder); ok {
		h.SetLiveSnapshot(stateDir, replyID, snap)
	}
}

// ClearLiveSnapshot forwards to the configured source (LiveSnapshotHolder).
func (s agentSessions) ClearLiveSnapshot(stateDir, replyID string) {
	if h, ok := s.inner.(LiveSnapshotHolder); ok {
		h.ClearLiveSnapshot(stateDir, replyID)
	}
}

// ReadTicketIdeas forwards to the configured source; an agent conversation
// has no ticket panel.
func (s agentSessions) ReadTicketIdeas(reg Registration) ([]IdeaTicket, bool, error) {
	if src, ok := s.inner.(TicketIdeasSource); ok && !reg.Agent {
		return src.ReadTicketIdeas(reg)
	}
	return nil, false, nil
}

// ReadTicketGraph forwards to the configured source; an agent conversation
// has no ticket panel.
func (s agentSessions) ReadTicketGraph(reg Registration) ([]byte, bool, error) {
	if src, ok := s.inner.(TicketGraphSource); ok && !reg.Agent {
		return src.ReadTicketGraph(reg)
	}
	return nil, false, nil
}

// ReadClosedTopics forwards to the configured source; an agent conversation
// has no board.
func (s agentSessions) ReadClosedTopics(reg Registration, lang string, from, n int) (template.HTML, bool) {
	if cs, ok := s.inner.(ClosedTopicsSource); ok && !reg.Agent {
		return cs.ReadClosedTopics(reg, lang, from, n)
	}
	return "", false
}

// BoardArchive forwards to the configured source; an agent conversation has
// no board.
func (s agentSessions) BoardArchive(reg Registration) ([]byte, bool, error) {
	if bs, ok := s.inner.(BoardArchiveSource); ok && !reg.Agent {
		return bs.BoardArchive(reg)
	}
	return nil, false, nil
}

// SetBoardHold forwards to the configured source; an agent conversation has
// no board.
func (s agentSessions) SetBoardHold(reg Registration, itemID int, held bool) ([]int, bool, error) {
	if hs, ok := s.inner.(BoardHoldSource); ok && !reg.Agent {
		return hs.SetBoardHold(reg, itemID, held)
	}
	return nil, false, nil
}

// ReadTopicHistory forwards to the configured source; an agent conversation
// has no board.
func (s agentSessions) ReadTopicHistory(reg Registration, lang string, names []string, target string) ([]wswhiteboard.TopicFragment, bool) {
	if hs, ok := s.inner.(TopicHistorySource); ok && !reg.Agent {
		return hs.ReadTopicHistory(reg, lang, names, target)
	}
	return nil, false
}

// ReadOutsideLinks forwards to the configured source; an agent conversation
// has no board, so its board links nothing.
func (s agentSessions) ReadOutsideLinks(reg Registration) ([]string, bool) {
	if ls, ok := s.inner.(OutsideLinkSource); ok && !reg.Agent {
		return ls.ReadOutsideLinks(reg)
	}
	return nil, false
}

// HashLiveStores folds the live stores of session replyID under stateDir
// into h and returns the snapshot's updatedAt, reading every file afresh
// (SnapshotCache.HashLiveStores with no cache).
func HashLiveStores(h io.Writer, stateDir, replyID string, now time.Time) time.Time {
	return (*SnapshotCache)(nil).HashLiveStores(h, stateDir, replyID, now)
}

// HashLiveStores folds the live stores of session replyID under stateDir
// into h and returns the snapshot's updatedAt, reading the snapshot through
// the cache. It is the change detector both session kinds share: ws-mcp's
// source for a lead session and agentSessions for an agent conversation.
// The snapshot is hashed without updatedAt but with its staleness at now,
// so a heartbeat rewrite is no change while a writer that stopped is one;
// the ring and the board-answer inbox (held and drained) are hashed by their
// entry names, since an entry file is never rewritten, so held and drained
// names tell a send (a new held name), a steer (a name replaced), and a
// drain apart.
func (c *SnapshotCache) HashLiveStores(h io.Writer, stateDir, replyID string, now time.Time) time.Time {
	updatedAt, _ := c.HashLiveStoresClock(h, stateDir, replyID, now)
	return updatedAt
}

// LiveStores names the directories a write can change. Snapshot evidence is
// always refreshed; it needs no directory listing. Unknown writes name all.
type LiveStores uint8

const (
	LiveRing LiveStores = 1 << iota
	LiveInbox
	LiveObserved
	AllLiveStores = LiveRing | LiveInbox | LiveObserved
)

// LiveStoreFingerprint retains directory-name prints, not entry contents.
// It belongs to one indexed session and is copied with its last full read.
type LiveStoreFingerprint struct {
	known LiveStores
	parts [4]string
}

// HashLiveStoresClock returns clock evidence from the same immutable snapshot
// used by the fingerprint, so a concurrent publish cannot mix generations.
func (c *SnapshotCache) HashLiveStoresClock(h io.Writer, stateDir, replyID string, now time.Time) (updatedAt, staleAt time.Time) {
	_, updatedAt, staleAt = c.ReadLiveStoresClock(h, stateDir, replyID, now, LiveStoreFingerprint{}, AllLiveStores)
	return updatedAt, staleAt
}

// ReadLiveStoresClock refreshes only named directories once initialized, folding
// retained prints for the rest. A cold read still initializes every directory.
func (c *SnapshotCache) ReadLiveStoresClock(h io.Writer, stateDir, replyID string, now time.Time, previous LiveStoreFingerprint, stores LiveStores) (LiveStoreFingerprint, time.Time, time.Time) {
	return c.readLiveStoresClock(h, stateDir, replyID, now, previous, stores, nil)
}

// list is an instance-local counting seam; nil performs the ordinary listing.
func (c *SnapshotCache) readLiveStoresClock(h io.Writer, stateDir, replyID string, now time.Time, previous LiveStoreFingerprint, stores LiveStores, list func(string, func() ([]string, error)) ([]string, error)) (LiveStoreFingerprint, time.Time, time.Time) {
	var updatedAt, staleAt time.Time
	sessionDir := SessionDir(stateDir, replyID)
	if p, err := c.read(wslive.SnapshotPath(sessionDir)); err == nil && p.docOK {
		staleAt = snapshotStaleAt(&p.snap)
		fmt.Fprintf(h, "\x00snapshot stale=%t\x00%s", p.snap.Stale(now), p.canonical)
		if p.snap.UpdatedAt > 0 {
			updatedAt = time.UnixMilli(p.snap.UpdatedAt)
		}
	}
	for i, store := range []struct {
		tag   string
		kind  LiveStores
		names func() ([]string, error)
	}{
		{"model-evidence", LiveObserved, func() ([]string, error) {
			entries, err := os.ReadDir(filepath.Join(sessionDir, wslive.ObservedDirName))
			var names []string
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
					names = append(names, e.Name())
				}
			}
			return names, err
		}},
		{"ring", LiveRing, func() ([]string, error) { return wslive.RingFingerprint(wslive.RingDir(sessionDir)) }},
		{"inbox", LiveInbox, func() ([]string, error) {
			dir := wsinbox.Dir(sessionDir)
			if c == nil {
				return wsinbox.Names(dir)
			}
			return wsinbox.NamesWithin(dir, fileCacheValidationInterval)
		}},
		{"inbox-drained", LiveInbox, func() ([]string, error) {
			dir := wsinbox.Dir(sessionDir)
			if c == nil {
				return wsinbox.DrainedNames(dir)
			}
			return wsinbox.DrainedNamesWithin(dir, fileCacheValidationInterval)
		}},
	} {
		if stores&store.kind != 0 || previous.known&store.kind == 0 {
			namesFn := store.names
			if list != nil {
				namesFn = func() ([]string, error) { return list(store.tag, store.names) }
			}
			previous.parts[i] = ""
			if names, err := namesFn(); err == nil {
				var print strings.Builder
				print.WriteString("\x00" + store.tag)
				for _, name := range names {
					print.WriteString("\x00" + name)
				}
				previous.parts[i] = print.String()
			}
		}
		io.WriteString(h, previous.parts[i])
	}
	previous.known = AllLiveStores
	return previous, updatedAt, staleAt
}
