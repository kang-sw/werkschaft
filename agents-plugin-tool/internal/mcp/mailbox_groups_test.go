package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

// groupFixtureLabels is the label view the group tests give every server:
// the sender worker, alice, and dave have labels; bob and carol do not.
func groupFixtureLabels(f fanOutFixture) MailLabelFunc {
	labels := map[string]string{
		wsmailbox.ReplyID(f.workerKey): "Worker\nOne",
		wsmailbox.ReplyID(f.aliceKey):  "Alice; the [first] reviewer, with a long title",
		wsmailbox.ReplyID(f.daveKey):   "Dave",
	}
	return func(replyID string) string { return labels[replyID] }
}

// sendGroup sends one fan-out from the fixture's worker (To alice and dave,
// CC bob, BCC carol) and returns its group id, read from alice's copy.
func sendGroup(t *testing.T, f fanOutFixture, content string) string {
	t.Helper()
	callToolWithKey(t, f.sender, 10, f.workerKey, "mailbox.send", map[string]any{
		"to": []any{"alice@worktree", f.daveAddr}, "cc": []any{"bob@worktree"}, "bcc": []any{"carol@worktree"}, "content": content,
	})
	got := recvEnvelopes(t, f.alice, 11, f.aliceKey)
	if len(got) != 1 {
		t.Fatalf("alice copies = %#v, want one", got)
	}
	_, gid := fanOutMarker(t, got[0].Content, false, 3)
	return gid
}

type recipientsOut struct {
	ID     string               `json:"id"`
	Sender mailGroupMemberOut   `json:"sender"`
	SentAt string               `json:"sent_at"`
	To     []mailGroupMemberOut `json:"to"`
	Cc     []mailGroupMemberOut `json:"cc"`
}

// TestMailboxRecipientsListsThePublicAudienceToEveryMember pins the read
// contract: the sender and every To, CC, and BCC recipient (each on its own
// MCP connection) get the sender, the sent time, and the To and CC list with
// the labels known when the mail was sent, cleaned and cut; no answer names
// the BCC recipient or any routing identity.
func TestMailboxRecipientsListsThePublicAudienceToEveryMember(t *testing.T) {
	f := newFanOutFixture(t)
	labels := groupFixtureLabels(f)
	for _, s := range []*Server{f.sender, f.alice, f.bob, f.carol, f.dave} {
		s.mailLabels = labels
	}
	gid := sendGroup(t, f, "the body")

	wantTo := []mailGroupMemberOut{{Address: "alice@worktree", Label: "Alice the [first reviewer, with"}, {Address: f.daveAddr, Label: "Dave"}}
	wantCc := []mailGroupMemberOut{{Address: "bob@worktree"}}
	wantSender := mailGroupMemberOut{Address: wsmailbox.ReplyIDPrefix + wsmailbox.ReplyID(f.workerKey), Label: "Worker One"}
	cacheRoot := os.Getenv("WS_CACHE_HOME")
	for i, member := range []struct {
		name string
		srv  *Server
		key  string
	}{{"sender", f.sender, f.workerKey}, {"alice", f.alice, f.aliceKey}, {"dave", f.dave, f.daveKey}, {"bob (cc)", f.bob, f.bobKey}, {"carol (bcc)", f.carol, f.carolKey}} {
		raw := callToolWithKey(t, member.srv, 20+i, member.key, "mailbox.recipients", map[string]any{"id": gid, "format": "json"})
		var out recipientsOut
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("%s: JSON: %v: %s", member.name, err, raw)
		}
		if out.ID != gid || out.Sender != wantSender || out.SentAt == "" || !reflect.DeepEqual(out.To, wantTo) || !reflect.DeepEqual(out.Cc, wantCc) {
			t.Fatalf("%s: recipients = %+v", member.name, out)
		}
		var fields map[string]any
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !reflect.DeepEqual(keys, []string{"cc", "id", "sender", "sent_at", "to"}) {
			t.Fatalf("%s: response fields = %v", member.name, keys)
		}
		text := callToolWithKey(t, member.srv, 40+i, member.key, "mailbox.recipients", map[string]any{"id": gid})
		// A member whose copy is still queued also gets the unread badge.
		if cut := strings.Index(text, "\nunread "); cut >= 0 {
			text = text[:cut]
		}
		for _, leak := range []string{"carol", cacheRoot, f.workerKey, f.aliceKey, f.bobKey, f.carolKey, f.daveKey, "mailbox-ring"} {
			if strings.Contains(raw, leak) || strings.Contains(text, leak) {
				t.Fatalf("%s: answer names %q:\n%s\n%s", member.name, leak, raw, text)
			}
		}
		wantText := "group " + gid + " sent at " + out.SentAt + "\nfrom " + wantSender.Address + " (Worker One)\n" +
			"to alice@worktree (Alice the [first reviewer, with)\nto " + f.daveAddr + " (Dave)\ncc bob@worktree\n"
		if text != wantText {
			t.Fatalf("%s: text = %q, want %q", member.name, text, wantText)
		}
	}
}

// TestMailboxRecipientsRefusesOutsidersAlike pins the membership gate and
// the single refusal text: a session outside the group, the owner of the
// same slug@scope inbox in another worktree, its owner under another cache
// root, an unknown id, an evicted id, and an id from before a restart all
// get the same tool error.
func TestMailboxRecipientsRefusesOutsidersAlike(t *testing.T) {
	f := newFanOutFixture(t)
	// One store for every server here, apart from the process's, so the
	// eviction below touches no other test.
	store := NewMailGroupStore()
	share := func(servers ...*Server) {
		for _, s := range servers {
			s.mailGroups = store
		}
	}
	share(f.sender, f.alice)
	gid := sendGroup(t, f, "for members only")

	refused := func(name string, s *Server, key, id string) {
		t.Helper()
		raw := callToolOnce(t, s, 90, "mailbox.recipients", map[string]any{"session_key": key, "id": id})
		if !toolIsError(t, raw) || toolText(t, raw) != "mailbox.recipients: "+mailGroupNotFound {
			t.Fatalf("%s: want the one refusal, got %s", name, raw)
		}
	}

	// A session of the same root with no part in the mail.
	outsider := NewServer(f.root, "test")
	share(outsider)
	refused("outsider", outsider, mailboxLogin(t, outsider, 1, f.root), gid)

	// alice@worktree in another worktree is another queue.
	otherRoot := t.TempDir()
	initGit(t, otherRoot)
	t.Setenv(envMailbox, "alice@worktree")
	elsewhere := NewServer(otherRoot, "test")
	share(elsewhere)
	elsewhereKey := mailboxLogin(t, elsewhere, 1, otherRoot)
	callToolWithKey(t, elsewhere, 2, elsewhereKey, "mailbox.lookup_peers", map[string]any{"scope": "worktree"})
	t.Setenv(envMailbox, "")
	if owner, identity, err := elsewhere.mailboxOwnerCheck(elsewhereKey, otherRoot); err != nil || !owner || identity.address() != "alice@worktree" {
		t.Fatalf("fixture: the other worktree's session must own alice@worktree there: %v %+v %v", owner, identity, err)
	}
	refused("same slug, other worktree", elsewhere, elsewhereKey, gid)

	// alice@worktree under another cache root, in the same worktree, is
	// another queue too.
	env := append(os.Environ(), "WS_CACHE_HOME="+filepath.Join(t.TempDir(), "other-cache"), envMailbox+"=alice@worktree")
	otherCache := NewServerWithOptions(ServerOptions{Root: f.root, Version: "test", Env: env, MailGroups: store})
	otherCacheKey := mailboxLogin(t, otherCache, 1, f.root)
	callToolWithKey(t, otherCache, 2, otherCacheKey, "mailbox.lookup_peers", map[string]any{"scope": "worktree"})
	if owner, _, err := otherCache.mailboxOwnerCheck(otherCacheKey, f.root); err != nil || !owner {
		t.Fatalf("fixture: the other cache root's session must own alice@worktree there: %v %v", owner, err)
	}
	refused("same slug, other cache root", otherCache, otherCacheKey, gid)

	// The real alice reads it; an unknown id does not.
	callToolWithKey(t, f.alice, 3, f.aliceKey, "mailbox.recipients", map[string]any{"id": gid})
	refused("unknown id", f.alice, f.aliceKey, "g-0000000000000000-1")
	refused("malformed id", f.alice, f.aliceKey, "not a group")

	// Eviction: 1024 later groups push it out.
	for range mailGroupMaxCount {
		store.add(mailGroup{sentAt: "later"})
	}
	refused("evicted id", f.alice, f.aliceKey, gid)

	// A restart: a new store, a new generation; an old marker resolves to
	// the expiry error, never to a later group.
	fresh := sendGroup(t, f, "kept")
	callToolWithKey(t, f.alice, 4, f.aliceKey, "mailbox.recipients", map[string]any{"id": fresh})
	store = NewMailGroupStore()
	share(f.sender, f.alice)
	refused("id from before a restart", f.alice, f.aliceKey, fresh)
	after := sendGroup(t, f, "after the restart")
	if after == fresh || strings.Split(after, "-")[1] == strings.Split(fresh, "-")[1] {
		t.Fatalf("a restarted store reissued or kept the generation: %s then %s", fresh, after)
	}
	refused("old id after a new send", f.alice, f.aliceKey, fresh)
}

// TestMailboxGroupRecordPrecedesEveryCopy pins the ordering guarantee: a
// copy's marker carries the group id only the store's insert issues, so a
// group is kept before its first copy is composed, let alone enqueued; a
// send whose every copy fails still leaves its record.
func TestMailboxGroupRecordPrecedesEveryCopy(t *testing.T) {
	f := newFanOutFixture(t)
	f.sender.mailGroups = NewMailGroupStore()
	breakWorktreeScope(t, f.root)
	raw := callToolOnce(t, f.sender, 10, "mailbox.send", map[string]any{
		"session_key": f.workerKey, "to": []any{"alice@worktree"}, "cc": []any{"bob@worktree"}, "content": "nobody gets this",
	})
	if !toolIsError(t, raw) {
		t.Fatalf("fixture: every copy must fail: %s", raw)
	}
	f.sender.mailGroups.mu.Lock()
	kept := len(f.sender.mailGroups.order)
	var id string
	if kept == 1 {
		id = f.sender.mailGroups.order[0].id
	}
	f.sender.mailGroups.mu.Unlock()
	if kept != 1 {
		t.Fatalf("a send with no delivered copy kept %d group records, want 1", kept)
	}
	if out := callToolWithKey(t, f.sender, 11, f.workerKey, "mailbox.recipients", map[string]any{"id": id}); !strings.Contains(out, "to alice@worktree\ncc bob@worktree\n") {
		t.Fatalf("the sender cannot read its kept group: %s", out)
	}
}

// TestMailboxRecipientsTransportAdmission pins which sessions reach the
// tool: an agent.spawn child's standard mailbox loadout lists and answers
// it, while a closed allowlist without it (the worker profile's) neither
// lists nor accepts it.
func TestMailboxRecipientsTransportAdmission(t *testing.T) {
	f := newFanOutFixture(t)
	if !slices.Contains(wsagent.AgentWSTools, "mailbox.recipients") {
		t.Fatal("the agent.spawn loadout lacks mailbox.recipients")
	}
	child := NewServerWithOptions(ServerOptions{Root: f.root, Version: "test", Env: append(os.Environ(), envAllowedTools+"="+strings.Join(wsagent.AgentWSTools, ","))})
	childKey, err := child.sessions.mint(f.root, roleDelegate, "")
	if err != nil {
		t.Fatal(err)
	}
	if !listedToolNames(t, child)["mailbox.recipients"] {
		t.Fatal("an agent.spawn child does not list mailbox.recipients")
	}
	childAddr := wsmailbox.ReplyIDPrefix + wsmailbox.ReplyID(childKey)
	callToolWithKey(t, f.sender, 10, f.workerKey, "mailbox.send", map[string]any{"to": []any{childAddr, "alice@worktree"}, "content": "to the child"})
	got := recvEnvelopes(t, child, 11, childKey)
	if len(got) != 1 {
		t.Fatalf("child copies = %#v", got)
	}
	_, gid := fanOutMarker(t, got[0].Content, false, 2)
	if out := callToolWithKey(t, child, 12, childKey, "mailbox.recipients", map[string]any{"id": gid}); !strings.Contains(out, "to "+childAddr+"\nto alice@worktree\n") {
		t.Fatalf("the child's expansion = %s", out)
	}

	closed := NewServerWithOptions(ServerOptions{Root: f.root, Version: "test", Env: append(os.Environ(), envAllowedTools+"=worker.bootstrap,mailbox.send,mailbox.recv")})
	closedKey, err := closed.sessions.mint(f.root, roleDelegate, "")
	if err != nil {
		t.Fatal(err)
	}
	if listedToolNames(t, closed)["mailbox.recipients"] {
		t.Fatal("a closed worker allowlist lists mailbox.recipients")
	}
	if raw := callToolOnce(t, closed, 13, "mailbox.recipients", map[string]any{"session_key": closedKey, "id": gid}); !strings.Contains(raw, "mailbox.recipients") || strings.Contains(raw, "alice@worktree") {
		t.Fatalf("a closed worker allowlist reached the tool: %s", raw)
	}
}

var mailGroupIDPattern = regexp.MustCompile(`^g-[0-9a-f]{16}-[1-9][0-9]*$`)

// TestMailGroupStoreIDs pins the id contract: g-<16 hex generation>-<n>, n
// only growing within a store, and a new store (a restarted daemon) drawing
// another generation and keeping none of the old records.
func TestMailGroupStoreIDs(t *testing.T) {
	s := NewMailGroupStore()
	first, second := s.add(mailGroup{}), s.add(mailGroup{})
	for _, id := range []string{first, second} {
		if !mailGroupIDPattern.MatchString(id) {
			t.Fatalf("id %q does not match %s", id, mailGroupIDPattern)
		}
	}
	if first == second || !strings.HasSuffix(first, "-1") || !strings.HasSuffix(second, "-2") || first[:19] != second[:19] {
		t.Fatalf("one store's ids share a generation and count up: %s, %s", first, second)
	}
	restarted := NewMailGroupStore()
	if restarted.generation == s.generation {
		t.Fatal("a new store reused the generation")
	}
	if _, ok := restarted.get(first); ok {
		t.Fatal("a new store kept an old record")
	}
}

// TestMailGroupStoreEvictsPastTheCountBound pins the count bound: the 1025th
// group evicts the oldest, and only it.
func TestMailGroupStoreEvictsPastTheCountBound(t *testing.T) {
	s := NewMailGroupStore()
	var ids []string
	for range mailGroupMaxCount + 1 {
		ids = append(ids, s.add(mailGroup{sentAt: "t"}))
	}
	if _, ok := s.get(ids[0]); ok {
		t.Fatal("the oldest group survived past the count bound")
	}
	for _, id := range []string{ids[1], ids[mailGroupMaxCount/2], ids[mailGroupMaxCount]} {
		if _, ok := s.get(id); !ok {
			t.Fatalf("group %s was evicted inside the bound", id)
		}
	}
	if len(s.order) != mailGroupMaxCount || len(s.byID) != mailGroupMaxCount {
		t.Fatalf("store holds %d/%d groups, want %d", len(s.order), len(s.byID), mailGroupMaxCount)
	}
}

// bulkGroup is a group retaining about n bytes of address and label text.
func bulkGroup(n int) mailGroup {
	return mailGroup{to: []mailGroupMember{{address: "a@worktree", label: strings.Repeat("x", n)}}}
}

// TestMailGroupStoreEvictsPastTheByteBound pins the byte bound, apart from
// the count bound: groups retaining 1 MiB each fit four at a time, and the
// fifth evicts the oldest.
func TestMailGroupStoreEvictsPastTheByteBound(t *testing.T) {
	s := NewMailGroupStore()
	var ids []string
	for range 5 {
		ids = append(ids, s.add(bulkGroup(1<<20-256)))
	}
	if _, ok := s.get(ids[0]); ok {
		t.Fatal("the oldest group survived past the byte bound")
	}
	for _, id := range ids[1:] {
		if _, ok := s.get(id); !ok {
			t.Fatalf("group %s was evicted inside the byte bound", id)
		}
	}
	if s.bytes > mailGroupMaxBytes || len(s.order) != 4 {
		t.Fatalf("store retains %d bytes in %d groups", s.bytes, len(s.order))
	}
}

// TestMailGroupStoreKeepsAnOversizedNewestGroup pins the newest-record rule:
// one group above the byte bound is still kept, alone, so the largest
// admitted send still expands; the next group evicts it.
func TestMailGroupStoreKeepsAnOversizedNewestGroup(t *testing.T) {
	s := NewMailGroupStore()
	small := s.add(mailGroup{sentAt: "t"})
	big := s.add(bulkGroup(mailGroupMaxBytes + 1))
	if _, ok := s.get(small); ok {
		t.Fatal("an older group survived beside an oversized newest one")
	}
	if g, ok := s.get(big); !ok || len(g.to[0].label) != mailGroupMaxBytes+1 {
		t.Fatal("the oversized newest group was not kept")
	}
	next := s.add(mailGroup{sentAt: "t"})
	if _, ok := s.get(big); ok {
		t.Fatal("the oversized group outlived a newer one")
	}
	if _, ok := s.get(next); !ok || s.bytes > mailGroupMaxBytes {
		t.Fatalf("store after the oversized group: %d bytes", s.bytes)
	}
}
