package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// mailbox_groups.go keeps the public audience of each fan-out send in memory
// so a copy can carry one short group marker instead of the audience, and
// mailbox.recipients can list that audience on demand. Nothing here is
// written to a file: a daemon restart empties the store, and a marker that
// outlives its record answers the expiry error.

const (
	// mailGroupMaxCount and mailGroupMaxBytes bound the store; both evict
	// oldest first. The newest record is kept even when it alone exceeds the
	// byte bound, so every admitted send still expands once.
	mailGroupMaxCount = 1024
	mailGroupMaxBytes = 4 << 20
	// mailLabelMaxRunes cuts a sender or recipient display label.
	mailLabelMaxRunes = 32
	// mailGroupNotFound is the one refusal mailbox.recipients gives for an
	// unknown id, an evicted one, and a caller outside the group alike, so
	// the tool cannot probe whether a group exists.
	mailGroupNotFound = "group not found or no longer kept"
)

// mailGroupRoute is where a group member's mail lives, compared by value.
// ring is the canonical mailbox ring identity (wsmailbox.Store.RingIdentity):
// the same queue name under another cache root is another queue. A named
// route adds the resolved store path, because one slug@scope names a
// different queue in another worktree, clone, or mailbox home; a reply-id
// route is the reply-id in that ring's registry.
type mailGroupRoute struct {
	named bool
	ring  string
	path  string
	queue string
}

func (r mailGroupRoute) size() int { return len(r.ring) + len(r.path) + len(r.queue) }

// mailGroupMember is one public member: its display address and the label
// it had when the mail was sent, with the route that identifies it.
type mailGroupMember struct {
	address string
	label   string
	route   mailGroupRoute
}

func (m mailGroupMember) size() int { return len(m.address) + len(m.label) + m.route.size() }

// mailGroup is one fan-out send. bcc holds routes only, for the access
// check: no listing ever names a BCC recipient.
type mailGroup struct {
	id      string
	sender  mailGroupMember
	sentAt  string
	to, cc  []mailGroupMember
	bcc     []mailGroupRoute
	retains int
}

func (g *mailGroup) measure() int {
	n := len(g.id) + len(g.sentAt) + g.sender.size()
	for _, m := range g.to {
		n += m.size()
	}
	for _, m := range g.cc {
		n += m.size()
	}
	for _, r := range g.bcc {
		n += r.size()
	}
	return n
}

// MailGroupStore is the bounded in-memory store of fan-out groups. One store
// serves every MCP server of a process, so a recipient on another connection
// of the same daemon finds the sender's record.
type MailGroupStore struct {
	mu         sync.Mutex
	generation string
	next       uint64
	order      []*mailGroup // oldest first
	byID       map[string]*mailGroup
	bytes      int
	maxCount   int
	maxBytes   int
}

// NewMailGroupStore returns an empty store with a fresh generation: an id
// it issues is never issued again by it, and another store's ids differ
// from its own except with negligible probability (64 random bits).
func NewMailGroupStore() *MailGroupStore {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("mailbox groups: read random generation: %v", err))
	}
	return &MailGroupStore{generation: hex.EncodeToString(raw[:]), byID: map[string]*mailGroup{}, maxCount: mailGroupMaxCount, maxBytes: mailGroupMaxBytes}
}

var (
	processMailGroupsOnce sync.Once
	processMailGroupsVal  *MailGroupStore
)

// processMailGroups is this process's shared store: the daemon's, for every
// connection it hosts.
func processMailGroups() *MailGroupStore {
	processMailGroupsOnce.Do(func() { processMailGroupsVal = NewMailGroupStore() })
	return processMailGroupsVal
}

// add assigns g its id, keeps it, and evicts the oldest records past either
// bound. It is an in-memory append: the send path inserts before it enqueues
// the first copy, so no copy can be claimed before its group exists.
func (s *MailGroupStore) add(g mailGroup) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	g.id = "g-" + s.generation + "-" + strconv.FormatUint(s.next, 10)
	g.retains = g.measure()
	rec := &g
	s.order = append(s.order, rec)
	s.byID[rec.id] = rec
	s.bytes += rec.retains
	for len(s.order) > 1 && (len(s.order) > s.maxCount || s.bytes > s.maxBytes) {
		old := s.order[0]
		s.order[0] = nil
		s.order = s.order[1:]
		delete(s.byID, old.id)
		s.bytes -= old.retains
	}
	return rec.id
}

// get returns the record kept under id.
func (s *MailGroupStore) get(id string) (mailGroup, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[id]
	if !ok {
		return mailGroup{}, false
	}
	return *rec, true
}

// mailboxGroupMarker is the one line every copy of a fan-out send carries in
// place of its audience: the group id and the public (To and CC) recipient
// count, identical on every copy. It names no address, so a BCC copy reads
// exactly as a public one.
func mailboxGroupMarker(namespace, gid string, public int) string {
	return fmt.Sprintf("[%s group mail %s: %d public recipients; mailbox.recipients lists them]", namespace, gid, public)
}

// MailLabelFunc returns the display label of the session whose mailbox
// reply-id is replyID: a spawned child's agent.spawn label, else a lead
// session's dashboard title; "" for none. The dashboard daemon supplies it
// from its in-memory agent and session views; a server without one shows
// addresses only.
type MailLabelFunc func(replyID string) string

// mailLabel is replyID's display label, cleaned (cleanMailLabel); "" for
// none.
func (s *Server) mailLabel(replyID string) string {
	if s.mailLabels == nil || replyID == "" {
		return ""
	}
	return cleanMailLabel(s.mailLabels(replyID))
}

// cleanMailLabel makes a label safe on the generated lines that carry it: a
// newline becomes a space, ';' and ']' are dropped, runs of space collapse,
// and the result is cut to mailLabelMaxRunes runes.
func cleanMailLabel(label string) string {
	label = strings.Map(func(r rune) rune {
		switch r {
		case ';', ']':
			return -1
		case '\n', '\r':
			return ' '
		}
		return r
	}, label)
	label = strings.Join(strings.Fields(label), " ")
	if runes := []rune(label); len(runes) > mailLabelMaxRunes {
		label = strings.TrimSpace(string(runes[:mailLabelMaxRunes]))
	}
	return label
}

// routeLabel is the display label of the session behind a fan-out
// recipient whose route the send already resolved: it reuses route.path
// instead of resolving the scope again; a server with no label view skips
// the lookup.
func (s *Server) routeLabel(addr wsmailbox.Address, route mailGroupRoute) string {
	if s.mailLabels == nil {
		return ""
	}
	if addr.Kind == wsmailbox.AddressReplyID {
		return s.mailLabel(addr.ReplyID)
	}
	if route.path == "" {
		return ""
	}
	return s.mailLabel(s.namedOwnerReplyID(route.path, addr.Name))
}

// mailboxScopePaths memoizes the named-store path of a (homes, GIT_* env,
// scope, root) for the label and group-route lookups. Resolving it through
// wsmailbox.Paths.ForScope runs wsstate.Manager.Ensure (directory creation
// and metadata checks) on every call; delivery's sender legend and fan-out
// labels must add no per-message file work (performance manual, rules 1 and
// 5). A root's store path changes only when its Git identity does, which a
// daemon restart picks up; these lookups are display and read-only
// membership, never the send's own enqueue resolution.
var mailboxScopePaths sync.Map

// mailboxScopePathsMax bounds the memo; past it the memo is dropped whole
// and refills from the next lookups.
const mailboxScopePathsMax = 1024

var mailboxScopePathsCount atomic.Int64

// mailboxScopePath is s.mailboxPaths().ForScope(scope, root), memoized in
// mailboxScopePaths after its first success.
func (s *Server) mailboxScopePath(scope wsmailbox.Scope, root string) (string, error) {
	var key strings.Builder
	key.WriteString(s.getenv(wsconfig.EnvConfigHome))
	key.WriteByte(0)
	key.WriteString(s.getenv(wsstate.EnvCacheHome))
	key.WriteByte(0)
	key.WriteString(string(scope))
	key.WriteByte(0)
	key.WriteString(root)
	for _, kv := range s.environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			key.WriteByte(0)
			key.WriteString(kv)
		}
	}
	k := key.String()
	if cached, ok := mailboxScopePaths.Load(k); ok {
		return cached.(string), nil
	}
	path, err := s.mailboxPaths().ForScope(scope, root)
	if err != nil {
		return "", err
	}
	if mailboxScopePathsCount.Add(1) > mailboxScopePathsMax {
		mailboxScopePaths.Clear()
		mailboxScopePathsCount.Store(1)
	}
	mailboxScopePaths.Store(k, path)
	return path, nil
}

// addressReplyID is the reply-id behind a mailbox address, "" for none: an
// id: handle's own, or the reply-id of the session that owns a named
// address now (resolved against root, as a send resolves it). It reads the
// store's memory only.
func (s *Server) addressReplyID(addr wsmailbox.Address, root string) string {
	if addr.Kind == wsmailbox.AddressReplyID {
		return addr.ReplyID
	}
	path, err := s.mailboxScopePath(addr.Scope, root)
	if err != nil {
		return ""
	}
	return s.namedOwnerReplyID(path, addr.Name)
}

// namedOwnerReplyID is the reply-id of the session that owns the named
// queue name in the store at path now, "" for none. It reads the store's
// memory only.
func (s *Server) namedOwnerReplyID(path, name string) string {
	presence, ok, _, err := s.mailboxPaths().Store().NamedQueue(path, name)
	if err != nil || !ok || presence.Owner == "" {
		return ""
	}
	return s.mailboxReplyID(presence.Owner)
}

// handleReplyID is addressReplyID for a head handle, "" when it is not an
// address (a display label, "unknown", system).
func (s *Server) handleReplyID(handle, root string) string {
	addr, err := wsmailbox.ParseAddress(handle)
	if handle == "" || err != nil {
		return ""
	}
	return s.addressReplyID(addr, root)
}

// mailGroupRouteFor is the route of a copy sent to addr from a session at
// root: the queue the copy was enqueued in.
func (s *Server) mailGroupRouteFor(addr wsmailbox.Address, root, ring string) mailGroupRoute {
	if addr.Kind == wsmailbox.AddressReplyID {
		return mailGroupRoute{ring: ring, queue: addr.ReplyID}
	}
	route := mailGroupRoute{named: true, ring: ring, queue: addr.Name}
	if path, err := s.mailboxScopePath(addr.Scope, root); err == nil {
		route.path = filepath.Clean(path)
	}
	return route
}

// recordMailGroup keeps f's audience under a new group id and returns it.
// The sender is identified by the reply identity the send runs under; To
// and CC keep the label each had now; BCC keeps routes only.
func (s *Server) recordMailGroup(sessionKey string, entry sessionEntry, sender mailboxSendIdentity, f *mailboxFanOut, sentAt string) string {
	ring := s.mailboxPaths().Store().RingIdentity()
	replyID := s.mailboxReplyID(sessionKey)
	address := wsmailbox.ReplyIDPrefix + replyID
	if sender.ownNamed != "" {
		address = sender.ownNamed
	}
	g := mailGroup{sentAt: sentAt, sender: mailGroupMember{address: address, route: mailGroupRoute{ring: ring, queue: replyID}}}
	for _, r := range f.recipients {
		route := s.mailGroupRouteFor(r.addr, entry.root, ring)
		switch r.role {
		case mailboxRoleTo:
			g.to = append(g.to, mailGroupMember{address: r.address, label: s.routeLabel(r.addr, route), route: route})
		case mailboxRoleCc:
			g.cc = append(g.cc, mailGroupMember{address: r.address, label: s.routeLabel(r.addr, route), route: route})
		default:
			g.bcc = append(g.bcc, route)
		}
	}
	return s.mailGroups.add(g)
}

// inMailGroup reports whether the caller is g's sender or one of its To,
// CC, or BCC recipients, compared by route, never by display string: its own
// reply-id in its own mailbox ring, or the named queue it owns now (same
// ring, same resolved store path, same name).
func (s *Server) inMailGroup(g mailGroup, sessionKey string, entry sessionEntry) bool {
	ring := s.mailboxPaths().Store().RingIdentity()
	if ring == "" {
		return false
	}
	reply := mailGroupRoute{ring: ring, queue: s.mailboxReplyID(sessionKey)}
	var named mailGroupRoute
	if isOwner, identity, err := s.mailboxOwnerCheck(sessionKey, entry.root); err == nil && isOwner {
		if path, perr := s.mailboxScopePath(identity.Scope, entry.root); perr == nil {
			named = mailGroupRoute{named: true, ring: ring, path: filepath.Clean(path), queue: identity.Name}
		}
	}
	match := func(r mailGroupRoute) bool { return r == reply || (named.path != "" && r == named) }
	if match(g.sender.route) {
		return true
	}
	for _, m := range append(append([]mailGroupMember(nil), g.to...), g.cc...) {
		if match(m.route) {
			return true
		}
	}
	for _, r := range g.bcc {
		if match(r) {
			return true
		}
	}
	return false
}

// mailGroupMemberOut is one listed member: a display address and its label
// when known. It never carries a route.
type mailGroupMemberOut struct {
	Address string `json:"address"`
	Label   string `json:"label,omitempty"`
}

func mailGroupMembersOut(members []mailGroupMember) []mailGroupMemberOut {
	out := make([]mailGroupMemberOut, 0, len(members))
	for _, m := range members {
		out = append(out, mailGroupMemberOut{Address: m.address, Label: m.label})
	}
	return out
}

func (m mailGroupMemberOut) text() string {
	if m.Label == "" {
		return m.Address
	}
	return m.Address + " (" + m.Label + ")"
}

func (s *Server) handleMailboxRecipients(id json.RawMessage, args map[string]any) response {
	const tool = "mailbox.recipients"
	sessionKey, err := sessionStateKey(tool, args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	entry, found := s.lookupSession(sessionKey)
	if !found {
		return toolTextResponse(id, "", mailboxUnknownSessionError(tool))
	}
	gid, err := stringArg(tool, "id", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	g, ok := s.mailGroups.get(strings.TrimSpace(gid))
	if !ok || !s.inMailGroup(g, sessionKey, entry) {
		return toolTextResponse(id, "", fmt.Errorf("%s: %s", tool, mailGroupNotFound))
	}
	// The sender's label is read now; the recipients' are the ones they had
	// when the mail was sent.
	sender := mailGroupMemberOut{Address: g.sender.address, Label: s.mailLabel(g.sender.route.queue)}
	to, cc := mailGroupMembersOut(g.to), mailGroupMembersOut(g.cc)
	if wantsJSON(args) {
		return toolJSONResponse(id, map[string]any{"id": g.id, "sender": sender, "sent_at": g.sentAt, "to": to, "cc": cc}, nil)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "group %s sent at %s\nfrom %s\n", g.id, g.sentAt, sender.text())
	for _, m := range to {
		fmt.Fprintf(&b, "to %s\n", m.text())
	}
	for _, m := range cc {
		fmt.Fprintf(&b, "cc %s\n", m.text())
	}
	return toolTextResponse(id, b.String(), nil)
}
