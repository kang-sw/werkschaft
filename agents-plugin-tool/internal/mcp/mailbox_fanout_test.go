package mcp

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsstate"
	"github.com/kang-sw/devenv/internal/wswake"
)

// fanOutFixture is one root shared by named inboxes alice/bob/carol@worktree,
// an env-less session dave reached only through its reply-id, and a sender
// server whose lead, worker, and owned-inbox keys send.
type fanOutFixture struct {
	root                       string
	alice, bob, carol, dave    *Server
	aliceKey, bobKey, carolKey string
	daveKey, daveAddr          string
	sender                     *Server
	leadKey, workerKey         string
}

func newFanOutFixture(t *testing.T) fanOutFixture {
	t.Helper()
	setupMailboxTestEnv(t)
	t.Setenv(envMailboxAuto, "")
	t.Setenv(envNamespace, "")
	root := t.TempDir()
	initGit(t, root)
	f := fanOutFixture{root: root}
	named := func(slug string) (*Server, string) {
		t.Setenv(envMailbox, slug)
		s := NewServer(root, "test")
		return s, mailboxLogin(t, s, 1, root)
	}
	f.alice, f.aliceKey = named("alice@worktree")
	f.bob, f.bobKey = named("bob@worktree")
	f.carol, f.carolKey = named("carol@worktree")
	// Register each named inbox's presence, so recv drains it as owner.
	for _, s := range []struct {
		srv *Server
		key string
	}{{f.alice, f.aliceKey}, {f.bob, f.bobKey}, {f.carol, f.carolKey}} {
		callToolWithKey(t, s.srv, 2, s.key, "mailbox.lookup_peers", map[string]any{"scope": "worktree"})
	}
	f.dave, f.daveKey = named("")
	f.daveAddr = wsmailbox.ReplyIDPrefix + wsmailbox.ReplyID(f.daveKey)
	f.sender, f.leadKey = named("")
	f.sender.sessionHarness = "codex"
	f.workerKey, _ = parseLoginResponse(t, callLogin(t, f.sender, 2, root, map[string]any{"parent_session_key": f.leadKey}))
	return f
}

// fanOutMarkerLine matches the group marker a fan-out copy carries; its
// literal format is pinned by TestMailboxGroupMarker alone.
var fanOutMarkerLine = regexp.MustCompile(`^\[ws group mail (g-[0-9a-f]{16}-[1-9][0-9]*): ([0-9]+) public recipients; mailbox\.recipients lists them\]$`)

// fanOutMarker returns the marker line of a copy's content (after a leading
// peer-lead note when noted) and its group id, failing unless the line is a
// well-formed marker counting public recipients. Routing tests build their
// expected bodies from it, so they do not depend on the marker's format.
func fanOutMarker(t *testing.T, content string, noted bool, public int) (line, gid string) {
	t.Helper()
	rest := content
	if noted {
		_, rest, _ = strings.Cut(content, "\n")
	}
	line, _, _ = strings.Cut(rest, "\n")
	m := fanOutMarkerLine.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("copy has no group marker where expected: %q", content)
	}
	if n, _ := strconv.Atoi(m[2]); n != public {
		t.Fatalf("marker counts %d public recipients, want %d: %q", n, public, line)
	}
	return line, m[1]
}

// recvEnvelopes drains key's mail as JSON.
func recvEnvelopes(t *testing.T, s *Server, id int, key string) []wsmailbox.Envelope {
	t.Helper()
	raw := callToolWithKey(t, s, id, key, "mailbox.recv", map[string]any{"format": "json"})
	var envelopes []wsmailbox.Envelope
	if err := json.Unmarshal([]byte(raw), &envelopes); err != nil {
		t.Fatalf("recv JSON: %v: %s", err, raw)
	}
	return envelopes
}

// TestMailboxFanOutDeliversIndependentPublicCopies pins the core contract: an
// array To with CC and BCC reaches every explicit destination, named inbox
// and reply-id alike, as its own ordinary envelope; every copy carries the
// same generated group marker (counting the public recipients, naming no
// address) and the supplied body bytes, untrimmed; no copy, recipient
// history, or dashboard rendering names any recipient; and one recipient's
// recv leaves the others' copies queued.
func TestMailboxFanOutDeliversIndependentPublicCopies(t *testing.T) {
	f := newFanOutFixture(t)
	body := "  indented first line\n\ttrailing whitespace  \n"
	receipt := callToolWithKey(t, f.sender, 10, f.workerKey, "mailbox.send", map[string]any{
		"to": []any{"alice@worktree", f.daveAddr}, "cc": []any{"bob@worktree"}, "bcc": []any{"carol@worktree"}, "content": body,
	})

	want := ""
	senderReply := wsmailbox.ReplyIDPrefix + wsmailbox.ReplyID(f.workerKey)
	for _, line := range []string{
		"sent to 4 of 4 recipients\n",
		"delivered alice@worktree [to] reply_to: " + senderReply + "\n",
		"delivered " + f.daveAddr + " [to] reply_to: " + senderReply + "\n",
		"delivered bob@worktree [cc] reply_to: " + senderReply + "\n",
		"delivered carol@worktree [bcc] reply_to: " + senderReply + "\n",
		"your_reply_id: " + senderReply + "\n",
	} {
		if !strings.Contains(receipt, line) {
			t.Fatalf("receipt lacks %q:\n%s", line, receipt)
		}
	}
	if strings.Contains(receipt, "failed") {
		t.Fatalf("receipt reports a failure: %s", receipt)
	}

	// Alice drains first; every other copy must still be queued after it.
	recipients := []struct {
		name string
		srv  *Server
		key  string
	}{{"alice", f.alice, f.aliceKey}, {"dave", f.dave, f.daveKey}, {"bob", f.bob, f.bobKey}, {"carol", f.carol, f.carolKey}}
	for i, r := range recipients {
		envelopes := recvEnvelopes(t, r.srv, 20+i, r.key)
		if len(envelopes) != 1 {
			t.Fatalf("%s received %d envelopes, want its own one copy: %#v", r.name, len(envelopes), envelopes)
		}
		e := envelopes[0]
		if want == "" {
			marker, _ := fanOutMarker(t, e.Content, false, 3)
			want = marker + "\n" + body
		}
		if e.Content != want {
			t.Fatalf("%s copy content = %q, want the same marker and body as every copy, %q", r.name, e.Content, want)
		}
		if e.From != "" || e.ReplyTo != senderReply {
			t.Fatalf("%s copy stamps = from %q reply_to %q, want the sender's reply-id", r.name, e.From, e.ReplyTo)
		}
		for _, other := range []string{"alice", "bob", "carol", f.daveAddr} {
			if strings.Contains(e.Content, other) {
				t.Fatalf("%s copy names recipient %s: %q", r.name, other, e.Content)
			}
		}

		// The recipient's history and the dashboard's rendering of it show
		// the body it received, nothing more.
		hist := historyJSON(t, r.srv, 30+i, r.key, map[string]any{"body": true})
		if len(hist) != 1 || hist[0].Body == nil || *hist[0].Body != want || strings.Contains(hist[0].Title, "carol") {
			t.Fatalf("%s history = %#v, want the one received copy", r.name, hist)
		}
		entry := wsdashboard.ConvEntry{RingEntry: wslive.RingEntry{Kind: wslive.RingMail, Title: hist[0].Title}, Body: *hist[0].Body, HasBody: true}
		if strings.Contains(entry.MailBody(), "carol") || strings.Contains(entry.MailPreview(), "carol") {
			t.Fatalf("%s dashboard rendering reveals the BCC recipient: %q", r.name, entry.MailBody())
		}
	}

	// The sender's own history records each copy under its recipient; BCC
	// is hidden from co-recipients, not from the sender.
	sent := historyJSON(t, f.sender, 40, f.workerKey, nil)
	var counterparties []string
	for _, h := range sent {
		if h.Direction != mailboxHistorySent {
			t.Fatalf("sender history has a non-sent entry: %#v", h)
		}
		counterparties = append(counterparties, h.Counterparty)
	}
	slices.Sort(counterparties)
	wantParties := []string{"alice@worktree", "bob@worktree", "carol@worktree", f.daveAddr}
	slices.Sort(wantParties)
	if !reflect.DeepEqual(counterparties, wantParties) {
		t.Fatalf("sender history counterparties = %v, want %v", counterparties, wantParties)
	}
}

// TestMailboxFanOutStampsEachRoute pins route-appropriate sender identity on
// each copy: an owned worktree inbox stamps from: on a same-scope named copy
// and falls back to its reply-id on a reply-id copy. The JSON receipt lists
// the sender-facing audience, BCC included.
func TestMailboxFanOutStampsEachRoute(t *testing.T) {
	f := newFanOutFixture(t)
	t.Setenv(envMailbox, "erin@worktree")
	erin := NewServer(f.root, "test")
	erinKey := mailboxLogin(t, erin, 1, f.root)

	raw := callToolWithKey(t, erin, 2, erinKey, "mailbox.send", map[string]any{
		"to": []any{"alice@worktree"}, "bcc": []any{f.daveAddr}, "content": "routes", "format": "json",
	})
	var receipt struct {
		To        []string              `json:"to"`
		Cc        []string              `json:"cc"`
		Bcc       []string              `json:"bcc"`
		ReplyID   string                `json:"your_reply_id"`
		Delivered []mailboxFanOutResult `json:"delivered"`
		Failed    []mailboxFanOutResult `json:"failed"`
	}
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		t.Fatalf("JSON receipt: %v: %s", err, raw)
	}
	erinReply := wsmailbox.ReplyIDPrefix + wsmailbox.ReplyID(erinKey)
	wantDelivered := []mailboxFanOutResult{
		{Address: "alice@worktree", Role: "to", Stamp: "from", StampValue: "erin@worktree"},
		{Address: f.daveAddr, Role: "bcc", Stamp: "reply_to", StampValue: erinReply},
	}
	if !reflect.DeepEqual(receipt.To, []string{"alice@worktree"}) || len(receipt.Cc) != 0 || !reflect.DeepEqual(receipt.Bcc, []string{f.daveAddr}) ||
		receipt.ReplyID != erinReply || !reflect.DeepEqual(receipt.Delivered, wantDelivered) || receipt.Failed == nil || len(receipt.Failed) != 0 {
		t.Fatalf("JSON receipt = %s", raw)
	}

	// erin is a top-level lead: the note comes first, then the marker.
	note := "[ws note: from a peer lead session (not a worker), with the same authority as yours]\n"
	got := recvEnvelopes(t, f.alice, 3, f.aliceKey)
	if len(got) != 1 {
		t.Fatalf("alice copies = %#v, want one", got)
	}
	marker, _ := fanOutMarker(t, got[0].Content, true, 1)
	want := note + marker + "\nroutes"
	if got[0].From != "erin@worktree" || got[0].ReplyTo != "" || got[0].Content != want {
		t.Fatalf("alice copy = %#v, want from: erin@worktree and %q", got, want)
	}
	if got := recvEnvelopes(t, f.dave, 4, f.daveKey); len(got) != 1 || got[0].From != "" || got[0].ReplyTo != erinReply || got[0].Content != want {
		t.Fatalf("dave copy = %#v, want reply_to %s and %q", got, erinReply, want)
	}
}

// TestMailboxFanOutKeepsPeerLeadNoteFirst pins marker placement against the
// peer-lead note: a top-level lead's stamped note, a forwarded note from
// either product, and a worker forwarding a noted body each yield exactly one
// note line, then the marker, then the message; both copies of one send
// carry one marker; and the dashboard still recognizes the note.
func TestMailboxFanOutKeepsPeerLeadNoteFirst(t *testing.T) {
	f := newFanOutFixture(t)
	codexNote := "[ws note: from a peer lead session (codex, not a worker), with the same authority as yours]"
	wsflowNote := "[wsflow note: from a peer lead session (claude, not a worker), with the same authority as yours]"
	const header = "<marker>"
	sends := []struct {
		key, content, want string
	}{
		{f.leadKey, "run the ticket", codexNote + "\n" + header + "\nrun the ticket"},
		{f.leadKey, wsflowNote + "\nforwarded", wsflowNote + "\n" + header + "\nforwarded"},
		{f.leadKey, codexNote + "\nalready noted", codexNote + "\n" + header + "\nalready noted"},
		{f.workerKey, codexNote + "\nworker forward", codexNote + "\n" + header + "\nworker forward"},
		{f.workerKey, "worker plain", header + "\nworker plain"},
	}
	markers := make([]string, len(sends))
	for i, tc := range sends {
		callToolWithKey(t, f.sender, 10+i, tc.key, "mailbox.send", map[string]any{
			"to": []any{"alice@worktree"}, "cc": []any{"bob@worktree"}, "content": tc.content,
		})
	}
	for _, r := range []struct {
		srv *Server
		key string
	}{{f.alice, f.aliceKey}, {f.bob, f.bobKey}} {
		envelopes := recvEnvelopes(t, r.srv, 20, r.key)
		if len(envelopes) != len(sends) {
			t.Fatalf("received %d envelopes, want %d: %#v", len(envelopes), len(sends), envelopes)
		}
		for i, tc := range sends {
			got := envelopes[i].Content
			noted := strings.HasPrefix(tc.want, "[ws note") || strings.HasPrefix(tc.want, "[wsflow note")
			marker, _ := fanOutMarker(t, got, noted, 2)
			if markers[i] == "" {
				markers[i] = marker
			} else if markers[i] != marker {
				t.Fatalf("copy %d: the two copies of one send carry different markers: %q vs %q", i, markers[i], marker)
			}
			if want := strings.Replace(tc.want, header, marker, 1); got != want {
				t.Fatalf("copy %d content = %q, want %q", i, got, want)
			}
			if n := strings.Count(got, " note: from a peer lead session"); n != map[bool]int{true: 1, false: 0}[noted] {
				t.Fatalf("copy %d carries %d peer-lead notes: %q", i, n, got)
			}
			entry := wsdashboard.ConvEntry{RingEntry: wslive.RingEntry{Kind: wslive.RingMail}, Body: got, HasBody: true}
			if entry.PeerLead() != noted {
				t.Fatalf("copy %d dashboard peer-lead badge = %v, want %v: %q", i, entry.PeerLead(), noted, got)
			}
			if noted && !strings.HasPrefix(entry.MailBody(), marker+"\n") {
				t.Fatalf("copy %d dashboard body does not open with the marker: %q", i, entry.MailBody())
			}
		}
	}
}

// TestMailboxScalarSendUnchangedByFanOut pins the legacy path: a scalar To
// with no CC or BCC (including empty arrays) keeps today's trimmed body and
// carries no marker, while a scalar To with a BCC is a fan-out whose marker
// counts only the To recipient, the same on the BCC copy.
func TestMailboxScalarSendUnchangedByFanOut(t *testing.T) {
	f := newFanOutFixture(t)
	out := callToolWithKey(t, f.sender, 10, f.workerKey, "mailbox.send", map[string]any{"to": "alice@worktree", "content": "  scalar body  \n"})
	if !strings.HasPrefix(out, "sent to alice@worktree\nreply_to: ") {
		t.Fatalf("scalar receipt changed: %s", out)
	}
	callToolWithKey(t, f.sender, 11, f.workerKey, "mailbox.send", map[string]any{"to": "alice@worktree", "cc": []any{}, "bcc": []any{}, "content": "  empty lists  "})
	callToolWithKey(t, f.sender, 12, f.workerKey, "mailbox.send", map[string]any{"to": "alice@worktree", "bcc": []any{"bob@worktree"}, "content": "with bcc"})

	got := recvEnvelopes(t, f.alice, 20, f.aliceKey)
	if len(got) != 3 {
		t.Fatalf("alice received %d envelopes, want 3: %#v", len(got), got)
	}
	marker, _ := fanOutMarker(t, got[2].Content, false, 1)
	want := []string{"scalar body", "empty lists", marker + "\nwith bcc"}
	if len(got) != len(want) {
		t.Fatalf("alice received %d envelopes, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Content != want[i] {
			t.Fatalf("alice envelope %d = %q, want %q", i, got[i].Content, want[i])
		}
	}
	if bob := recvEnvelopes(t, f.bob, 21, f.bobKey); len(bob) != 1 || bob[0].Content != want[2] {
		t.Fatalf("bob's BCC copy = %#v, want %q", bob, want[2])
	}
}

// TestMailboxFanOutDeduplicatesRecipients pins one copy per destination: an
// address repeated across To, CC, and BCC is delivered once under its most
// public role, and the marker counts it once.
func TestMailboxFanOutDeduplicatesRecipients(t *testing.T) {
	f := newFanOutFixture(t)
	receipt := callToolWithKey(t, f.sender, 10, f.workerKey, "mailbox.send", map[string]any{
		"to": []any{"alice@worktree", " alice@worktree "}, "cc": []any{"alice@worktree", "bob@worktree"}, "bcc": []any{"bob@worktree", "alice@worktree"}, "content": "once",
	})
	if !strings.Contains(receipt, "sent to 2 of 2 recipients\n") || !strings.Contains(receipt, "delivered bob@worktree [cc]") {
		t.Fatalf("receipt = %s", receipt)
	}
	want := ""
	for _, r := range []struct {
		srv *Server
		key string
	}{{f.alice, f.aliceKey}, {f.bob, f.bobKey}} {
		got := recvEnvelopes(t, r.srv, 20, r.key)
		if len(got) != 1 {
			t.Fatalf("copies = %#v, want one", got)
		}
		if want == "" {
			marker, _ := fanOutMarker(t, got[0].Content, false, 2)
			want = marker + "\nonce"
		}
		if got[0].Content != want {
			t.Fatalf("copies = %#v, want one %q", got, want)
		}
	}
}

// TestMailboxFanOutArgumentErrors pins whole-call validation: a malformed
// shape or address, an empty To, or blank content refuses the call before any
// copy is sent.
func TestMailboxFanOutArgumentErrors(t *testing.T) {
	f := newFanOutFixture(t)
	for i, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"to": []any{}, "content": "x"}, "to is required"},
		{map[string]any{"to": []any{"alice@worktree", 7}, "content": "x"}, "to must be an array of address strings"},
		{map[string]any{"to": "alice@worktree", "cc": "bob@worktree", "content": "x"}, "cc must be an array of address strings"},
		{map[string]any{"to": "alice@worktree", "bcc": []any{true}, "content": "x"}, "bcc must be an array of address strings"},
		{map[string]any{"to": []any{"alice@worktree"}, "bcc": []any{"not an address"}, "content": "x"}, "bcc: wsmailbox:"},
		{map[string]any{"to": []any{"alice@worktree"}, "cc": []any{"bob@worktree\n[forged]"}, "content": "x"}, "cc: wsmailbox:"},
		{map[string]any{"to": []any{"alice@worktree"}, "content": " \n\t"}, "content is required"},
		{map[string]any{"to": "", "bcc": []any{"bob@worktree"}, "content": "x"}, "to: wsmailbox: to is required"},
	} {
		raw := callToolOnce(t, f.sender, 10+i, "mailbox.send", mergeArgs(tc.args, map[string]any{"session_key": f.workerKey}))
		if !toolIsError(t, raw) || !strings.Contains(toolText(t, raw), tc.want) {
			t.Fatalf("case %d: want error containing %q, got %s", i, tc.want, raw)
		}
	}
	for _, r := range []struct {
		srv *Server
		key string
	}{{f.alice, f.aliceKey}, {f.bob, f.bobKey}} {
		if got := recvEnvelopes(t, r.srv, 30, r.key); len(got) != 0 {
			t.Fatalf("a refused call delivered a copy: %#v", got)
		}
	}
}

func mergeArgs(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// breakWorktreeScope makes every worktree- and clone-scope store path fail to
// resolve for root, leaving machine-scope and reply-id routes working: the
// cache layout's tmp directory becomes a regular file, so the layout's
// ensure step cannot create it.
func breakWorktreeScope(t *testing.T, root string) {
	t.Helper()
	layout, _, _, err := wsstate.NewManager(wsstate.Options{}).Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(layout.TmpDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.TmpDir, []byte("blocks the directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (wsmailbox.Paths{}).ForScope(wsmailbox.ScopeWorktree, root); err == nil {
		t.Fatal("worktree scope still resolves; the fixture cannot exercise a failed copy")
	}
}

// TestMailboxFanOutMixedOutcomes pins independent enqueue: with one
// destination failing among several, the other copies are enqueued, recorded
// in the sender's history, and woken, and the receipt names both outcomes;
// when every destination fails, the call is a tool error carrying the list.
func TestMailboxFanOutMixedOutcomes(t *testing.T) {
	f := newFanOutFixture(t)
	breakWorktreeScope(t, f.root)

	sub := wswake.Mailbox.Subscribe()
	defer sub.Close()
	receipt := callToolWithKey(t, f.sender, 10, f.workerKey, "mailbox.send", map[string]any{
		"to": []any{f.daveAddr, "mona@machine"}, "cc": []any{"alice@worktree"}, "content": "partial",
	})
	for _, line := range []string{
		"sent to 2 of 3 recipients\n",
		"delivered " + f.daveAddr + " [to] ",
		"delivered mona@machine [to] ",
		"failed alice@worktree [cc]: ",
	} {
		if !strings.Contains(receipt, line) {
			t.Fatalf("receipt lacks %q:\n%s", line, receipt)
		}
	}
	requireHardWake(t, sub)

	// The marker counts every public recipient, the failed one included.
	daveCopy := recvEnvelopes(t, f.dave, 20, f.daveKey)
	if len(daveCopy) != 1 {
		t.Fatalf("dave copies = %#v, want one", daveCopy)
	}
	marker, _ := fanOutMarker(t, daveCopy[0].Content, false, 3)
	want := marker + "\npartial"
	if daveCopy[0].Content != want {
		t.Fatalf("dave copy = %#v, want %q", daveCopy, want)
	}
	machinePath, err := (wsmailbox.Paths{}).ForScope(wsmailbox.ScopeMachine, f.root)
	if err != nil {
		t.Fatal(err)
	}
	_, _, mona, err := (wsmailbox.Paths{}).Store().NamedQueue(machinePath, "mona")
	if err != nil || len(mona) != 1 || mona[0].Content != want {
		t.Fatalf("mona queue = %#v (%v), want one %q", mona, err, want)
	}
	var parties []string
	for _, h := range historyJSON(t, f.sender, 21, f.workerKey, nil) {
		parties = append(parties, h.Counterparty)
	}
	slices.Sort(parties)
	wantParties := []string{f.daveAddr, "mona@machine"}
	slices.Sort(wantParties)
	if !reflect.DeepEqual(parties, wantParties) {
		t.Fatalf("sender history = %v, want only the delivered copies %v", parties, wantParties)
	}

	allFailed := callToolOnce(t, f.sender, 30, "mailbox.send", map[string]any{
		"session_key": f.workerKey, "to": []any{"alice@worktree"}, "bcc": []any{"bob@clone"}, "content": "nobody",
	})
	text := toolText(t, allFailed)
	if !toolIsError(t, allFailed) || !strings.Contains(text, "delivered to none of 2 recipients") ||
		!strings.Contains(text, "failed alice@worktree [to]: ") || !strings.Contains(text, "failed bob@clone [bcc]: ") {
		t.Fatalf("all-failed fan-out = %s", allFailed)
	}
	// format: json does not change the all-failed shape: it stays the same
	// text tool error, carrying the same failure list.
	allFailedJSON := callToolOnce(t, f.sender, 31, "mailbox.send", map[string]any{
		"session_key": f.workerKey, "to": []any{"alice@worktree"}, "content": "nobody", "format": "json",
	})
	if text := toolText(t, allFailedJSON); !toolIsError(t, allFailedJSON) ||
		!strings.HasPrefix(text, "mailbox.send: delivered to none of 1 recipients\nfailed alice@worktree [to]: ") {
		t.Fatalf("all-failed JSON fan-out = %s", allFailedJSON)
	}
	select {
	case <-sub.Hard():
		t.Fatal("a fan-out that delivered nothing woke the mailbox waits")
	default:
	}

	// A partial JSON receipt lists the failed copy with its error.
	raw := callToolWithKey(t, f.sender, 32, f.workerKey, "mailbox.send", map[string]any{
		"to": []any{f.daveAddr}, "cc": []any{"alice@worktree"}, "content": "partial json", "format": "json",
	})
	var partial struct {
		Delivered []mailboxFanOutResult `json:"delivered"`
		Failed    []mailboxFanOutResult `json:"failed"`
	}
	if err := json.Unmarshal([]byte(raw), &partial); err != nil {
		t.Fatalf("JSON receipt: %v: %s", err, raw)
	}
	if len(partial.Delivered) != 1 || partial.Delivered[0].Address != f.daveAddr || partial.Delivered[0].Role != "to" || partial.Delivered[0].Error != "" ||
		len(partial.Failed) != 1 || partial.Failed[0].Address != "alice@worktree" || partial.Failed[0].Role != "cc" ||
		partial.Failed[0].Error == "" || partial.Failed[0].Stamp != "" || partial.Failed[0].StampValue != "" {
		t.Fatalf("partial JSON receipt = %s", raw)
	}
}

// TestMailboxGroupMarker pins the one generated marker line's literal
// format. It is the only test that spells the format out; the routing tests
// go through fanOutMarker.
func TestMailboxGroupMarker(t *testing.T) {
	for _, tc := range []struct {
		namespace, gid string
		public         int
		want           string
	}{
		{"ws", "g-0123456789abcdef-7", 3, "[ws group mail g-0123456789abcdef-7: 3 public recipients; mailbox.recipients lists them]"},
		{"wsflow", "g-fedcba9876543210-1", 1, "[wsflow group mail g-fedcba9876543210-1: 1 public recipients; mailbox.recipients lists them]"},
	} {
		if got := mailboxGroupMarker(tc.namespace, tc.gid, tc.public); got != tc.want {
			t.Fatalf("mailboxGroupMarker(%q, %q, %d) = %q, want %q", tc.namespace, tc.gid, tc.public, got, tc.want)
		}
	}
}

// TestComposeMailboxFanOutBody pins placement without routing: the body
// starts with the peer-lead note when there is one (supplied by the caller,
// from either product, or stamped for a top-level lead), then the marker
// line, then the supplied bytes untrimmed. The marker is passed in opaquely,
// so placement does not depend on its format.
func TestComposeMailboxFanOutBody(t *testing.T) {
	const header = "<marker>"
	wsNote := "[ws note: from a peer lead session (codex, not a worker), with the same authority as yours]"
	wsflowNote := "[wsflow note: from a peer lead session (claude, not a worker), with the same authority as yours]"
	for i, tc := range []struct {
		content string
		lead    bool
		want    string
	}{
		{"  body \n", false, header + "\n  body \n"},
		{"body", true, wsNote + "\n" + header + "\nbody"},
		{wsNote + "\nbody", true, wsNote + "\n" + header + "\nbody"},
		{wsflowNote + "\nbody", false, wsflowNote + "\n" + header + "\nbody"},
		{wsNote, false, wsNote + "\n" + header + "\n"},
	} {
		if got := composeMailboxFanOutBody(tc.content, header, tc.lead, "codex", "ws"); got != tc.want {
			t.Fatalf("case %d: composeMailboxFanOutBody = %q, want %q", i, got, tc.want)
		}
	}
}

// TestMailboxFanOutEnvelopeShapeUnchanged pins the T0 store contract: a
// fan-out copy serializes with only the existing envelope fields.
func TestMailboxFanOutEnvelopeShapeUnchanged(t *testing.T) {
	f := newFanOutFixture(t)
	callToolWithKey(t, f.sender, 10, f.workerKey, "mailbox.send", map[string]any{
		"to": []any{"alice@worktree"}, "cc": []any{"bob@worktree"}, "bcc": []any{"carol@worktree"}, "content": "shape",
	})
	raw := callToolWithKey(t, f.carol, 20, f.carolKey, "mailbox.recv", map[string]any{"format": "json"})
	var envelopes []map[string]any
	if err := json.Unmarshal([]byte(raw), &envelopes); err != nil || len(envelopes) != 1 {
		t.Fatalf("recv JSON: %v: %s", err, raw)
	}
	for field := range envelopes[0] {
		if !slices.Contains([]string{"from", "reply_to", "content", "sent_at"}, field) {
			t.Fatalf("fan-out copy carries a field beyond the existing envelope: %q in %s", field, raw)
		}
	}
}

// TestMailboxSendAdvertisesFanOutArguments pins the public argument contract:
// to is a string or an array of strings, cc and bcc are optional string
// arrays, required arguments are unchanged, and no all-peers selector exists.
func TestMailboxSendAdvertisesFanOutArguments(t *testing.T) {
	setupMailboxTestEnv(t)
	root := t.TempDir()
	initGit(t, root)
	list := callToolsList(t, NewServer(root, "test"))
	props := toolPropertiesByName(t, list, "mailbox.send")

	var names []string
	for name := range props {
		names = append(names, name)
	}
	slices.Sort(names)
	if want := []string{"bcc", "cc", "content", "format", "session_key", "to"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("mailbox.send properties = %v, want %v", names, want)
	}
	to, _ := props["to"].(map[string]any)
	if _, hasType := to["type"]; hasType {
		t.Fatalf("to must carry its shapes in anyOf, not an outer type: %v", to)
	}
	wantTo := []any{
		map[string]any{"type": "string"},
		map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}
	if !reflect.DeepEqual(to["anyOf"], wantTo) {
		t.Fatalf("to anyOf = %v, want string or string array", to["anyOf"])
	}
	for _, name := range []string{"cc", "bcc"} {
		p, _ := props[name].(map[string]any)
		if p["type"] != "array" || !reflect.DeepEqual(p["items"], map[string]any{"type": "string"}) {
			t.Fatalf("%s = %v, want an array of strings", name, p)
		}
	}
	schema := toolInputSchema("mailbox.send")
	if got := schemaStringList(schema["required"]); !reflect.DeepEqual(got, []string{"session_key", "to", "content"}) {
		t.Fatalf("mailbox.send required = %v, want unchanged", got)
	}
}
