package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

// TestCleanMailLabel pins label safety on the generated lines: newlines
// become spaces, ';' and ']' are dropped, space runs collapse, and the
// result is cut to 32 runes (not bytes).
func TestCleanMailLabel(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"EXEC 4", "EXEC 4"},
		{"line one\nline two\r\nthree", "line one line two three"},
		{"a; b] [c]", "a b [c"},
		{"  spaced   out  ", "spaced out"},
		{strings.Repeat("가", 40), strings.Repeat("가", 32)},
		{strings.Repeat("x", 31) + " tail", strings.Repeat("x", 31)},
		{";]\n", ""},
	} {
		if got := cleanMailLabel(tc.in); got != tc.want {
			t.Fatalf("cleanMailLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// trimMailLegendCases are the shapes trimMailLegend takes and leaves: only a
// one-line "[senders: ...]" paragraph, closed by "]" and a blank line, ahead
// of a "mail from" head is the generated legend. wsdashboard's copy is pinned
// on the same table.
var trimMailLegendCases = []struct{ in, want string }{
	{"[senders: a = A]\n\nmail from a (2026-11-22T01:00:00Z):\nbody", "mail from a (2026-11-22T01:00:00Z):\nbody"},
	{"[senders: a = A]\nmail from a:\nbody", "[senders: a = A]\nmail from a:\nbody"},
	{"[senders: a = A]\n\nnot a head:\nbody", "[senders: a = A]\n\nnot a head:\nbody"},
	{"[senders: a = A\nmore]\n\nmail from a:\nbody", "[senders: a = A\nmore]\n\nmail from a:\nbody"},
	{"[senders: a = A\n\nmail from a:\nbody", "[senders: a = A\n\nmail from a:\nbody"},
	{"[subagent id:x] done\n\nmail from a:\nbody", "[subagent id:x] done\n\nmail from a:\nbody"},
	{"mail from a:\n[senders: x = y]\n\nmail from b:\nbody", "mail from a:\n[senders: x = y]\n\nmail from b:\nbody"},
}

func TestTrimMailLegendTakesOnlyTheGeneratedLine(t *testing.T) {
	for _, tc := range trimMailLegendCases {
		if got := trimMailLegend(tc.in); got != tc.want {
			t.Fatalf("trimMailLegend(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestMailLegendLine pins the legend line: one entry per distinct handle
// with a label, in first-appearance order; none without a labeled sender.
func TestMailLegendLine(t *testing.T) {
	s := NewServerWithOptions(ServerOptions{Env: []string{}, MailLabels: func(id string) string {
		return map[string]string{"r1": "One", "r2": "Two\n;]"}[id]
	}})
	defer s.Close()
	got := s.mailLegendLine([]mailLegendSender{
		{"id:r2", "r2"}, {"id:none", "none"}, {"alice@machine", "r1"}, {"id:r2", "r2"}, {"system", ""},
	})
	if want := "[senders: id:r2 = Two; alice@machine = One]"; got != want {
		t.Fatalf("legend = %q, want %q", got, want)
	}
	if got := s.mailLegendLine([]mailLegendSender{{"id:none", "none"}, {"system", ""}}); got != "" {
		t.Fatalf("a batch without a labeled sender has a legend: %q", got)
	}
	unlabeled := NewServerWithOptions(ServerOptions{Env: []string{}})
	defer unlabeled.Close()
	if got := unlabeled.mailLegendLine([]mailLegendSender{{"id:r1", "r1"}}); got != "" {
		t.Fatalf("a server without a label view has a legend: %q", got)
	}
}

// TestMailboxRecvShowsTheSenderLegend pins mailbox.recv's two renderings:
// the text output's first line is the legend when a sender has a label and
// every other line is unchanged; the structured output fills an addressed
// sender's label and keeps a senderless envelope's stored label.
func TestMailboxRecvShowsTheSenderLegend(t *testing.T) {
	f := newFanOutFixture(t)
	labels := map[string]string{wsmailbox.ReplyID(f.workerKey): "Worker\nOne"}
	f.alice.mailLabels = func(id string) string { return labels[id] }
	workerAddr := wsmailbox.ReplyIDPrefix + wsmailbox.ReplyID(f.workerKey)

	// A batch with no labeled sender has no legend.
	mustSend(t, f.dave, 1, f.daveKey, "alice@worktree", "from dave alone")
	if out := callToolWithKey(t, f.alice, 2, f.aliceKey, "mailbox.recv", nil); strings.Contains(out, mailLegendPrefix) || !strings.Contains(out, "from dave alone") {
		t.Fatalf("an unlabeled batch = %q", out)
	}

	mustSend(t, f.sender, 3, f.workerKey, "alice@worktree", "from the worker")
	mustSend(t, f.dave, 4, f.daveKey, "alice@worktree", "from dave")
	if err := f.alice.mailboxPaths().Store().PostSystem(wsmailbox.ReplyID(f.aliceKey), "agent:child", "a report", time.Now()); err != nil {
		t.Fatal(err)
	}
	queued := peekAliceMail(t, f)
	out := callToolWithKey(t, f.alice, 5, f.aliceKey, "mailbox.recv", nil)
	legend, rest, _ := strings.Cut(out, "\n")
	if legend != "[senders: "+workerAddr+" = Worker One]" {
		t.Fatalf("legend line = %q in %q", legend, out)
	}
	var want strings.Builder
	for _, m := range queued {
		fmt.Fprintf(&want, "[%s] %s: %s\n", m.SentAt, mailboxSender(m), m.Content)
	}
	if rest != want.String() {
		t.Fatalf("the lines after the legend changed:\n got %q\nwant %q", rest, want.String())
	}

	mustSend(t, f.sender, 6, f.workerKey, "alice@worktree", "json one")
	mustSend(t, f.dave, 7, f.daveKey, "alice@worktree", "json two")
	if err := f.alice.mailboxPaths().Store().PostSystem(wsmailbox.ReplyID(f.aliceKey), "agent:child", "json report", time.Now()); err != nil {
		t.Fatal(err)
	}
	stored := peekAliceMail(t, f)
	envelopes := recvEnvelopes(t, f.alice, 8, f.aliceKey)
	if len(envelopes) != 3 || len(stored) != 3 {
		t.Fatalf("envelopes = %#v", envelopes)
	}
	for i, e := range envelopes {
		wantLabel := ""
		switch {
		case e.ReplyTo == workerAddr:
			wantLabel = "Worker One"
		case e.From == "" && e.ReplyTo == "":
			wantLabel = "agent:child"
		}
		if e.Label != wantLabel {
			t.Fatalf("envelope %d label = %q, want %q: %+v", i, e.Label, wantLabel, e)
		}
		if e.Content != stored[i].Content || e.From != stored[i].From || e.ReplyTo != stored[i].ReplyTo || e.SentAt != stored[i].SentAt {
			t.Fatalf("envelope %d changed beyond its label: %+v vs stored %+v", i, e, stored[i])
		}
		if stored[i].Label != "" && stored[i].Label != "agent:child" {
			t.Fatalf("a label reached the store: %+v", stored[i])
		}
	}
}

// peekAliceMail is what alice's recv would return now, read without
// draining: her named queue and her reply-id queue, in recv's order.
func peekAliceMail(t *testing.T, f fanOutFixture) []wsmailbox.Envelope {
	t.Helper()
	path, err := f.alice.mailboxPaths().ForScope(wsmailbox.ScopeWorktree, f.root)
	if err != nil {
		t.Fatal(err)
	}
	_, _, named, err := f.alice.mailboxPaths().Store().NamedQueue(path, "alice")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := f.alice.mailboxPaths().Store().ReplyQueue(wsmailbox.ReplyID(f.aliceKey))
	if err != nil {
		t.Fatal(err)
	}
	all := append(append([]wsmailbox.Envelope(nil), named...), reply...)
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].SentAt < all[j-1].SentAt; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	return all
}

// TestLeadMailboxLegendPlacement pins the hosted legend: it names exactly
// the senders of the texts submitted together (from Drain's senders, or a
// held text's head), sits on the batch head's second line or alone ahead of
// a headless first text, follows a label change at the next submission, and
// leaves every message head and body, and parseMailText's reading of them,
// unchanged.
func TestLeadMailboxLegendPlacement(t *testing.T) {
	f := newFanOutFixture(t)
	stateDir, err := wsdashboard.Dir("")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	labels := map[string]string{"senderone": "One", wsmailbox.ReplyID(f.aliceKey): "Alice"}
	mail := NewLeadMailbox(stateDir).WithLabels(func(id string) string {
		mu.Lock()
		defer mu.Unlock()
		return labels[id]
	})
	defer mail.server.Close()

	batch := []wsmailbox.Envelope{
		{ReplyTo: "id:senderone", Content: "first body", SentAt: "2026-11-22T01:00:00Z"},
		{ReplyTo: "id:sendertwo", Content: "[senders: x = y]\nsecond body", SentAt: "2026-11-22T01:00:01Z"},
		{From: "alice@worktree", Content: "third body", SentAt: "2026-11-22T01:00:02Z"},
		{Label: "agent:child", Content: "a report", SentAt: "2026-11-22T01:00:03Z"},
	}
	texts := formatMailBatch(leadMailLabel, leadMailRecvTool, "lead-key", batch, "peer", "", "child", "")
	head, firstEnvelope, _ := strings.Cut(texts[0], "\n\n")

	// A turn of held texts: the senders come from the heads.
	got := mail.Legend(f.root, texts, nil)
	legend := "[senders: id:senderone = One; alice@worktree = Alice]"
	if want := head + "\n" + legend + "\n\n" + firstEnvelope; got[0] != want {
		t.Fatalf("legend on the batch head:\n got %q\nwant %q", got[0], want)
	}
	for i := 1; i < len(texts); i++ {
		if got[i] != texts[i] {
			t.Fatalf("text %d changed: %q", i, got[i])
		}
	}
	// Drain's senders, when given, name the label source; a submission of
	// the first text alone names only its sender.
	if byDrain := mail.Legend(f.root, texts[:1], []string{"senderone"}); byDrain[0] != head+"\n[senders: id:senderone = One]\n\n"+firstEnvelope {
		t.Fatalf("legend from Drain's sender = %q", byDrain[0])
	}

	// A steer of the second text alone: it names only its own sender, as a
	// first line ahead of a headless text; an unlabeled one has none.
	labels["sendertwo"] = "Two"
	steer := mail.Legend(f.root, texts[1:2], []string{"sendertwo"})
	if want := "[senders: id:sendertwo = Two]\n\n" + texts[1]; steer[0] != want {
		t.Fatalf("legend on a headless text:\n got %q\nwant %q", steer[0], want)
	}
	if unlabeled := mail.Legend(f.root, texts[3:], nil); unlabeled[0] != texts[3] {
		t.Fatalf("a senderless text got a legend: %q", unlabeled[0])
	}
	// A turn whose reports lead its mail (a child's report, then a steered
	// mail no run took): the legend goes on the first mail text, and the
	// report stays as it is.
	report := "[subagent id:child \"a child\"] turn done\n\nnotes:\nall done"
	turn := mail.Legend(f.root, []string{report, texts[1]}, nil)
	if want := "[senders: id:sendertwo = Two]\n\n" + texts[1]; turn[0] != report || turn[1] != want {
		t.Fatalf("legend in a turn led by a report:\n got %q\nwant %q after the report", turn, want)
	}

	// A held text submitted later shows the label current then.
	mu.Lock()
	labels["senderone"] = "One renamed"
	mu.Unlock()
	if later := mail.Legend(f.root, texts[:1], nil); !strings.Contains(later[0], "[senders: id:senderone = One renamed]\n\n") {
		t.Fatalf("a later submission kept the old label: %q", later[0])
	}

	// parseMailText reads a labeled text as it reads the text without the
	// legend, and a body that opens with a literal legend-like line stays
	// whole.
	for i, labeled := range []string{got[0], steer[0]} {
		plain := []string{texts[0], texts[1]}[i]
		if a, b := parseMailText(labeled), parseMailText(plain); a.From != b.From || a.Label != b.Label || a.Content != b.Content || a.SentAt != b.SentAt {
			t.Fatalf("text %d parses differently with its legend: %+v vs %+v", i, a, b)
		}
	}
	if m := parseMailText(steer[0]); m.Content != "[senders: x = y]\nsecond body" || m.From != "id:sendertwo" {
		t.Fatalf("the literal body line was taken for a legend: %+v", m)
	}
	if m := parseMailText(got[0]); m.Content != "first body" || m.From != "id:senderone" || m.SentAt != "2026-11-22T01:00:00Z" {
		t.Fatalf("the labeled batch text parses to %+v", m)
	}
}

// TestLeadMailboxDrainKeepsTextsWithoutALegend pins the storage side: Drain's
// texts, which a runner holds in Record.Mail and Return puts back, carry no
// legend however the senders are labeled.
func TestLeadMailboxDrainKeepsTextsWithoutALegend(t *testing.T) {
	f := newFanOutFixture(t)
	stateDir, err := wsdashboard.Dir("")
	if err != nil {
		t.Fatal(err)
	}
	mail := NewLeadMailbox(stateDir).WithLabels(func(string) string { return "Everyone" })
	defer mail.server.Close()
	mustSend(t, f.sender, 1, f.workerKey, wsmailbox.ReplyIDPrefix+wsmailbox.ReplyID(f.daveKey), "held")
	claimed, err := mail.Drain(f.root, f.daveKey, nil)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Drain = %+v, %v", claimed, err)
	}
	if strings.Contains(claimed[0].Text, mailLegendPrefix) || claimed[0].Sender != wsmailbox.ReplyID(f.workerKey) {
		t.Fatalf("a drained text carries a legend or lost its sender: %+v", claimed[0])
	}
	if got := mail.Legend(f.root, []string{claimed[0].Text}, []string{claimed[0].Sender}); !strings.Contains(got[0], "= Everyone]") {
		t.Fatalf("the submission lacks the legend: %q", got[0])
	}
	raw, _ := json.Marshal(claimed)
	if strings.Contains(string(raw), "Everyone") {
		t.Fatalf("a label reached the claimed mail: %s", raw)
	}
}
