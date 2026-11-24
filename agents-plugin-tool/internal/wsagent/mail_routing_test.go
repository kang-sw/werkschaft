package wsagent

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// mailHarness is agentHarness with a mailbox the test delivers into.
func mailHarness(t *testing.T, configure func(*Options)) (*harness, *fakeMail) {
	t.Helper()
	mail := newFakeMail()
	h, _ := agentHarness(t, func(o *Options) {
		o.Mail = mail
		if configure != nil {
			configure(o)
		}
	})
	return h, mail
}

// keyOf is conversation id's ws session key: its mailbox address's key.
func (h *harness) keyOf(id string) string {
	h.t.Helper()
	key := h.record(id).WSKey
	if key == "" {
		h.t.Fatalf("%s has no key", id)
	}
	return key
}

// Every spawned child's mailbox is watched, awake or asleep, and mail to a
// running child reaches its turn as a steer, tagged with how its sender
// relates to it through the spawn lineage; system mail carries no tag.
func TestMailIsSteeredIntoARunningTurnWithItsRelation(t *testing.T) {
	h, mail := mailHarness(t, nil)
	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "parent work", Tier: "medium", Authority: "workdir-write"})
	b, bp := h.agentRunning(h.holderOf(a), AgentSpawnRequest{Prompt: "child work", Tier: "small", Authority: "workdir-write"})
	aKey, bKey := h.keyOf(a), h.keyOf(b)
	eventually(t, "both mailboxes watched", func() bool { return mail.watched(aKey) && mail.watched(bKey) })

	mail.deliverFrom(bKey, a, "from the parent")
	mail.deliverFrom(bKey, "peerreplyid", "from a peer")
	mail.deliver(bKey, "a system notice")
	mail.deliverFrom(aKey, b, "from the child")
	mail.deliverFrom(aKey, leadParent.ReplyID, "from the lead")
	eventually(t, "b's steers", func() bool {
		got, _ := bp.steered()
		return slices.Equal(got, []string{"(parent) from the parent", "(peer) from a peer", "a system notice"})
	})
	eventually(t, "a's steers", func() bool {
		got, _ := ap.steered()
		return slices.Equal(got, []string{"(child) from the child", "(parent) from the lead"})
	})
	for _, id := range []string{a, b} {
		if rec := h.record(id); len(rec.Mail) != 0 {
			t.Fatalf("%s: steered mail rides the running turn, not the next: %q", id, rec.Mail)
		}
	}
}

// Mail waits for the next turn where the running turn cannot take it: the
// harness's catalog profile grades steer unsupported (a Claude worker), or
// the process has no steer handle.
func TestMailWaitsForTheNextTurnWithoutSteer(t *testing.T) {
	h, mail := mailHarness(t, nil)
	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "graded", Tier: "medium", Authority: "workdir-write"})
	c := h.runner.conv(a)
	c.setStatus(func() {
		caps := ClaudeCatalog().Resolve(RoleWorker, "")
		c.caps = &caps
	})
	mail.deliverFrom(h.keyOf(a), leadParent.ReplyID, "after this turn")
	eventually(t, "the mail kept for the next turn", func() bool {
		return slices.Equal(h.record(a).Mail, []string{"(parent) after this turn"})
	})
	if got, _ := ap.steered(); len(got) != 0 {
		t.Fatalf("a graded-unsupported steer was used: %q", got)
	}
	ap.reply("first done")
	if next := ap.nextSent(t); !strings.Contains(next, "(parent) after this turn") {
		t.Fatalf("the next turn = %q", next)
	}
	eventually(t, "the delivered mail dropped", func() bool { return len(h.record(a).Mail) == 0 })

	h.backend.mu.Lock()
	h.backend.steers = false
	h.backend.mu.Unlock()
	b, bp := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "no handle", Tier: "small", Authority: "read-only"})
	mail.deliverFrom(h.keyOf(b), "peerreplyid", "when you can")
	eventually(t, "the mail kept for the next turn", func() bool {
		return slices.Equal(h.record(b).Mail, []string{"(peer) when you can"})
	})
	bp.reply("done")
	if next := bp.nextSent(t); !strings.Contains(next, "(peer) when you can") {
		t.Fatalf("the next turn = %q", next)
	}
}

// Mail to a sleeping child wakes it even when its parent has the width
// cap's number of awake children.
func TestMailWakesASleepingChildPastTheWidthCap(t *testing.T) {
	h, mail := mailHarness(t, nil)
	lead := h.leadCaller()
	var ids []string
	for i := range MaxSpawnWidth {
		ids = append(ids, h.agentSpawn(lead, AgentSpawnRequest{Prompt: fmt.Sprintf("job %d", i), Tier: "small", Authority: "read-only"}))
	}
	eventually(t, "every child's process", func() bool { return h.backend.startCount() == MaxSpawnWidth })
	sleeper := ids[0]
	h.asleep(sleeper)
	h.agentSpawn(lead, AgentSpawnRequest{Prompt: "the hundredth", Tier: "small", Authority: "read-only"})
	eventually(t, "the parent at the cap", func() bool {
		return h.runner.awakeChildren(leadParent) == MaxSpawnWidth && h.backend.startCount() == MaxSpawnWidth+1
	})
	starts := h.backend.startCount()
	mail.deliverFrom(h.keyOf(sleeper), leadParent.ReplyID, "wake past the cap")
	p := h.backend.waitProc(t, starts)
	if text := p.nextSent(t); !strings.Contains(text, "(parent) wake past the cap") {
		t.Fatalf("the woken turn = %q", text)
	}
	if n := h.runner.awakeChildren(leadParent); n != MaxSpawnWidth+1 {
		t.Fatalf("awake children after the wake = %d", n)
	}
}

// A refused wake keeps the claimed mail with the sleeping child, across a
// daemon restart, and tells each sender of a claim once; mail that waited
// in the mailbox while no daemon ran is claimed at start; the next
// admitted wake delivers it all.
func TestRefusedMailWakeKeepsTheMailAcrossARestart(t *testing.T) {
	refusals := &wakeRefusals{}
	h, mail := mailHarness(t, func(o *Options) { o.WakeRefused = refusals.add })
	a, b := settledPair(t, h)
	bKey := h.keyOf(b)
	if err := writePermissionSelection(wsdashboard.SessionDir(h.dir, a), PermissionReadOnly); err != nil {
		t.Fatal(err)
	}
	starts := h.backend.startCount()
	mail.deliverFrom(bKey, a, "first")
	eventually(t, "the refused start", func() bool { return strings.Contains(h.state(b).Error, "current level read-only") })
	mail.deliverFrom(bKey, "peerreplyid", "from a peer")
	eventually(t, "both kept", func() bool { return len(h.record(b).Mail) == 2 })
	eventually(t, "both senders told", func() bool { return len(refusals.list()) == 2 })
	got := refusals.list()
	if got[0][0] != a || got[1][0] != "peerreplyid" || got[0][1] != b || !strings.Contains(got[1][2], "read-only") {
		t.Fatalf("the notices = %v", got)
	}
	if st := h.state(b); st.Process || st.Lifecycle != wsdashboard.SessionChildWaiting || AgentStatus(st) != AgentWaiting {
		t.Fatalf("the refused child = %+v", st)
	}
	if n := h.runner.awakeChildren(ParentRef{Kind: ParentConversation, ReplyID: a}); n != 0 {
		t.Fatalf("a refused child counts as awake: %d", n)
	}

	h.runner.Close()
	mail.deliverFrom(bKey, "otherreplyid", "while down")
	h.start()
	eventually(t, "the waiting mail claimed at start", func() bool { return len(h.record(b).Mail) == 3 })
	time.Sleep(100 * time.Millisecond)
	if n := h.backend.startCount(); n != starts {
		t.Fatalf("a refused wake started %d processes", n-starts)
	}
	if got := refusals.list(); len(got) != 3 || got[2][0] != "otherreplyid" {
		t.Fatalf("a restart tells only the new sender: %v", got)
	}

	if err := writePermissionSelection(wsdashboard.SessionDir(h.dir, a), PermissionWorkdirWrite); err != nil {
		t.Fatal(err)
	}
	mail.deliverFrom(bKey, a, "second")
	p := h.backend.waitProc(t, starts)
	text := p.nextSent(t)
	for _, want := range []string{"(parent) first", "(peer) from a peer", "(peer) while down", "(parent) second"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the woken turn %q lacks %q", text, want)
		}
	}
	if got := refusals.list(); len(got) != 3 {
		t.Fatalf("an admitted wake told a sender: %v", got)
	}
}

// Mail resets a sleeping child's waiting expiry, even when its wake is
// refused and it stays asleep.
func TestMailResetsTheChildWaitingClock(t *testing.T) {
	h, clk := expiryHarness(t)
	mail := newFakeMail()
	h.config.Mail = mail
	h.runner.Close()
	h.start()
	a, b := settledPair(t, h)
	// a stays well inside its own expiry, so only b's clock is measured.
	h.runner.conv(a).update(func(rec *Record) { rec.LastActiveAt = clk.Now().Add(30 * day).UTC().Format(time.RFC3339) })
	if err := writePermissionSelection(wsdashboard.SessionDir(h.dir, a), PermissionReadOnly); err != nil {
		t.Fatal(err)
	}
	clk.advance(day + 12*time.Hour)
	mail.deliverFrom(h.keyOf(b), a, "keep waiting")
	eventually(t, "the refused wake", func() bool { return len(h.record(b).Mail) == 1 && h.state(b).Error != "" })
	if rec := h.record(b); rec.LastActiveAt != clk.Now().UTC().Format(time.RFC3339) {
		t.Fatalf("the mail's LastActiveAt = %q, want %q", rec.LastActiveAt, clk.Now().UTC().Format(time.RFC3339))
	}
	// Two and a half days after the settle, one after the mail.
	clk.advance(day)
	time.Sleep(200 * time.Millisecond)
	if gone(h, b) {
		t.Fatal("a child mailed a day ago expired")
	}
	clk.advance(day + time.Hour)
	eventually(t, "the child expired with its mail", func() bool { return gone(h, b) })
	if gone(h, a) {
		t.Fatal("the parent expired with its child")
	}
}

func TestChildMailEnvelopeKeepsParentRelation(t *testing.T) {
	h, _ := mailHarness(t, nil)
	a, b := settledPair(t, h)
	card := h.card()
	for _, tc := range []struct{ target, sender, want string }{
		{a, leadParent.ReplyID, "parent"},
		{a, b, "child"},
		{b, a, "parent"},
		{b, "someoneelse", "peer"},
		{card, leadParent.ReplyID, "peer"},
		{b, "", ""},
	} {
		if got := h.runner.mailRelation(h.record(tc.target), tc.sender); got != tc.want {
			t.Fatalf("relation of %q to %s = %q, want %q", tc.sender, tc.target, got, tc.want)
		}
	}
}

// fakeLegend stands in for the daemon's legend (Options.MailLegend): it names
// each labeled sender of the texts it is given, taking a text's sender from
// senders or, for a held text, from its "mail from <sender>:" head, and puts
// the line ahead of the first mail text.
type fakeLegend struct {
	mu     sync.Mutex
	labels map[string]string
	calls  [][]string
}

var fakeLegendHead = regexp.MustCompile(`mail from (\S+):`)

func (l *fakeLegend) set(sender, label string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.labels[sender] = label
}

func (l *fakeLegend) place(_ string, texts, senders []string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, slices.Clone(texts))
	var names []string
	for i, text := range texts {
		sender := ""
		if i < len(senders) {
			sender = senders[i]
		}
		if m := fakeLegendHead.FindStringSubmatch(text); sender == "" && m != nil {
			sender = m[1]
		}
		if label := l.labels[sender]; label != "" {
			names = append(names, sender+" = "+label)
		}
	}
	if len(names) == 0 {
		return texts
	}
	out := slices.Clone(texts)
	for i, text := range out {
		if fakeLegendHead.MatchString(text) {
			out[i] = "[senders: " + strings.Join(names, "; ") + "]\n\n" + text
			break
		}
	}
	return out
}

// A child's steered mail that no run took goes to the next turn with a
// legend resolved then, not the one its steer carried: the record keeps the
// labeled steer only to match the harness's taken messages, and the text it
// requeues is Drain's.
func TestMailLegendRefreshesOnARequeuedSteer(t *testing.T) {
	legend := &fakeLegend{labels: map[string]string{"senderone": "One"}}
	h, mail := mailHarness(t, func(o *Options) { o.MailLegend = legend.place })
	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "workdir-write"})
	aKey := h.keyOf(a)
	eventually(t, "the mailbox watched", func() bool { return mail.watched(aKey) })

	mail.deliverBatch(aKey, ClaimedMail{Text: "mail from senderone:\nfirst", Sender: "senderone"})
	var steered []string
	eventually(t, "the mail steered", func() bool {
		steered, _ = ap.steered()
		return len(steered) == 1
	})
	const drained = "(peer) mail from senderone:\nfirst"
	if want := "[senders: senderone = One]\n\n" + drained; steered[0] != want {
		t.Fatalf("the steer = %q, want %q", steered[0], want)
	}

	legend.set("senderone", "One renamed")
	ap.events <- Event{SteerAnswer: &SteerAnswer{}}
	ap.reply("done without it")
	next := ap.nextSent(t)
	if want := "[senders: senderone = One renamed]\n\n" + drained; !strings.Contains(next, want) || strings.Count(next, "[senders: ") != 1 {
		t.Fatalf("the requeued mail's turn = %q, want exactly one legend, %q", next, want)
	}
	if _, cleared := ap.steered(); cleared != 1 {
		t.Fatalf("the untaken steer was dropped from the harness's queue %d times, want 1", cleared)
	}
	eventually(t, "the requeued report submitted", func() bool {
		rec := h.record(a)
		return len(rec.Reports) == 0 && len(rec.Steered) == 0
	})
	legend.mu.Lock()
	defer legend.mu.Unlock()
	for _, call := range legend.calls {
		for _, text := range call {
			if strings.Contains(text, "[senders:") {
				t.Fatalf("a legend was handed back in as stored text: %q", text)
			}
		}
	}
}

// Each submission of claimed mail carries the legend of its own texts'
// senders, resolved when it is submitted: of a two-mail claim whose first
// steer goes and second is refused, the steer names only the first sender
// and the held mail's later turn only the second, under the label current
// then. The record never holds a legend, and the harness's acknowledgement
// of the steered text still matches.
func TestMailLegendRidesEachSubmission(t *testing.T) {
	legend := &fakeLegend{labels: map[string]string{"senderone": "One", "sendertwo": "Two"}}
	h, mail := mailHarness(t, func(o *Options) { o.MailLegend = legend.place })
	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "workdir-write"})
	aKey := h.keyOf(a)
	eventually(t, "the mailbox watched", func() bool { return mail.watched(aKey) })
	ap.failSteers(func(text string) bool { return strings.Contains(text, "second") })

	mail.deliverBatch(aKey,
		ClaimedMail{Text: "mail from senderone:\nfirst", Sender: "senderone"},
		ClaimedMail{Text: "mail from sendertwo:\nsecond", Sender: "sendertwo"})
	var steered []string
	eventually(t, "the first steered, the second held", func() bool {
		steered, _ = ap.steered()
		return len(steered) == 1 && len(h.record(a).Mail) == 1
	})
	if want := "[senders: senderone = One]\n\n(peer) mail from senderone:\nfirst"; steered[0] != want {
		t.Fatalf("the steer = %q, want %q", steered[0], want)
	}
	if held := h.record(a).Mail; held[0] != "(peer) mail from sendertwo:\nsecond" {
		t.Fatalf("the held mail carries a legend or changed: %q", held)
	}

	legend.set("sendertwo", "Two renamed")
	ap.events <- Event{SteerAnswer: &SteerAnswer{}}
	ap.events <- Event{UserTaken: steered[0]}
	ap.reply("first done")
	next := ap.nextSent(t)
	if !strings.Contains(next, "[senders: sendertwo = Two renamed]\n\n(peer) mail from sendertwo:\nsecond") || strings.Contains(next, "senderone") {
		t.Fatalf("the held mail's turn = %q", next)
	}
	eventually(t, "the delivered mail dropped", func() bool { return len(h.record(a).Mail) == 0 })
	if _, cleared := ap.steered(); cleared != 0 {
		t.Fatalf("the taken steer was dropped from the harness's queue %d times", cleared)
	}
	legend.mu.Lock()
	defer legend.mu.Unlock()
	for _, call := range legend.calls {
		for _, text := range call {
			if strings.Contains(text, "[senders:") {
				t.Fatalf("a legend was handed back in as stored text: %q", text)
			}
		}
	}
}

// SpawnLabel is the label source of a spawned sender: the label its spawner
// gave it; nothing for a conversation the runner does not know.
func TestSpawnLabelNamesASpawnedChild(t *testing.T) {
	h, _ := mailHarness(t, nil)
	a, _ := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "workdir-write", Label: "Reviewer of the branch"})
	if got := h.runner.SpawnLabel(a); got != "Reviewer of the branch" {
		t.Fatalf("SpawnLabel(child) = %q", got)
	}
	if got := h.runner.SpawnLabel("nosuchconversation"); got != "" {
		t.Fatalf("SpawnLabel(unknown) = %q", got)
	}
	if got := h.runner.SpawnLabel(leadParent.ReplyID); got != "" {
		t.Fatalf("SpawnLabel(the lead) = %q", got)
	}
}

func TestDeadBindingMailRoutingSurvivesRestart(t *testing.T) {
	refusals := &wakeRefusals{}
	h, mail := mailHarness(t, func(o *Options) { o.WakeUndeliverable = refusals.add })
	spawn := func(prompt string) string {
		t.Helper()
		id, proc := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: prompt, Tier: "small", Authority: "read-only"})
		proc.reply("done")
		eventually(t, "worker settled", func() bool { return h.state(id).Status == StatusIdle })
		h.asleep(id)
		return id
	}
	dead, normal := spawn("permanently refused"), spawn("normally closed")
	deadKey, normalKey := h.keyOf(dead), h.keyOf(normal)
	c := h.runner.conv(dead)
	var closeErr error
	const reason = "permanent binding refusal"
	c.do(func() { closeErr = c.closeDeadBinding(&admissionRefusal{reason: reason}) })
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if _, err := h.runner.CloseConversation(normal); err != nil {
		t.Fatal(err)
	}
	h.runner.Close()
	h.start()
	mail.deliverFrom(deadKey, "peerreplyid", "cannot reach this worker")
	mail.deliverFrom(normalKey, "peerreplyid", "keep until reopened")
	// The production mailbox send callback invokes this after it queues.
	h.runner.DrainDeadBindingMail(dead)
	h.runner.DrainDeadBindingMail(dead)
	h.runner.DrainDeadBindingMail(normal)
	if got := refusals.list(); len(got) != 1 || got[0][0] != "peerreplyid" || got[0][1] != dead || !strings.Contains(got[0][2], reason) {
		t.Fatalf("restart sent these notices: %v", got)
	}
	if got := mail.waiting(deadKey); len(got) != 0 {
		t.Fatalf("permanently undeliverable mail remains queued: %v", got)
	}
	if got := mail.waiting(normalKey); len(got) != 1 {
		t.Fatalf("ordinary closed worker lost its mail: %v", got)
	}
}
