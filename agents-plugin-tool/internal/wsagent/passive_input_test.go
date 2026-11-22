package wsagent

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPassiveInputBounds(t *testing.T) {
	var p passiveInput
	// 17 receipts: the oldest collapses to its id line, the rest stay whole.
	for i := 1; i <= 17; i++ {
		p.put(1, fmt.Sprintf("exec_%02d", i), fmt.Sprintf("receipt %02d", i))
	}
	block, took := p.render(1)
	if len(took) != 17 {
		t.Fatalf("took %d receipts, want 17", len(took))
	}
	if !strings.Contains(block, "job exec_01: see exec.list") || strings.Contains(block, "receipt 01") {
		t.Fatalf("oldest not collapsed:\n%s", block)
	}
	for i := 2; i <= 17; i++ {
		if !strings.Contains(block, fmt.Sprintf("receipt %02d", i)) {
			t.Fatalf("receipt %d lost:\n%s", i, block)
		}
	}
	if !strings.HasPrefix(block, "<"+passiveBlockName+">") || !strings.Contains(block, "untrusted data") {
		t.Fatalf("the block is not framed as untrusted data:\n%s", block)
	}

	// 16 KiB: big receipts collapse oldest first until the text fits.
	var q passiveInput
	big := strings.Repeat("x", 6<<10)
	for i := 1; i <= 4; i++ {
		q.put(1, fmt.Sprintf("exec_%d", i), big+fmt.Sprint(i))
	}
	_, took = q.render(1)
	q.mu.Lock()
	total, full := 0, 0
	for _, it := range q.items {
		total += len(it.text)
		if !it.collapsed {
			full++
		}
	}
	first := q.items[0].text
	q.mu.Unlock()
	if total > passiveMaxBytes || full != 2 || first != "job exec_1: see exec.list" {
		t.Fatalf("bytes %d (cap %d), full %d, first %q", total, passiveMaxBytes, full, first)
	}
	// A flood of receipts never grows past the byte bound.
	var f passiveInput
	for i := 0; i < 5000; i++ {
		f.put(1, fmt.Sprintf("exec_flood_%d", i), "r")
	}
	f.mu.Lock()
	n := 0
	for _, it := range f.items {
		n += len(it.text)
	}
	f.mu.Unlock()
	if n > passiveMaxBytes {
		t.Fatalf("flood kept %d bytes", n)
	}
}

func TestPassiveInputAckKeepsNewerWrites(t *testing.T) {
	var p passiveInput
	p.put(1, "exec_a", "first")
	_, took := p.render(1)
	// A replacement arrives while the send that took "first" is in flight.
	p.put(1, "exec_a", "second")
	p.put(1, "exec_b", "late")
	p.ack(took)
	block, _ := p.render(1)
	if !strings.Contains(block, "second") || !strings.Contains(block, "late") || strings.Contains(block, "first") {
		t.Fatalf("after ack:\n%s", block)
	}
	// Ack of everything empties it, and a generation change drops it.
	_, took = p.render(1)
	p.ack(took)
	if p.pending() != 0 {
		t.Fatalf("pending = %d", p.pending())
	}
	p.put(1, "exec_c", "old gen")
	if block, _ := p.render(2); block != "" {
		t.Fatalf("a closed generation's receipts survived: %q", block)
	}
}

func TestTurnInputCarriesPassiveReceiptsAfterTheNoteBeforeAmbient(t *testing.T) {
	c := &conv{rec: Record{Lead: true, OpenGen: 2}, ambient: "<amb>", compactNote: "restore"}
	c.passive.put(2, "exec_a", "receipt A")
	got := c.turnInput("hello")
	block, _ := c.passive.render(2)
	if want := "restore\n\n" + block + "\n\nhello\n\n<amb>"; got != want {
		t.Fatalf("input = %q\nwant %q", got, want)
	}
	if len(c.passiveTook) != 1 {
		t.Fatalf("took = %v", c.passiveTook)
	}
	// Composition alone consumes nothing; a stale generation's receipts are
	// not carried.
	c.rec.OpenGen = 3
	if got := c.turnInput("hello"); strings.Contains(got, "receipt A") {
		t.Fatalf("a reopened conversation carried the old receipt: %q", got)
	}
}

func TestPassiveHandRefusesAStaleGeneration(t *testing.T) {
	h := newLeadHarness(t).start()
	id := h.lead(BackendClaude, LeadOptions{}).ID
	owner, err := h.runner.ExecOwner(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.runner.PassiveHand(id, owner.Gen+1, "exec_a", "x"); err == nil {
		t.Fatal("a stale generation was accepted")
	}
	if err := h.runner.PassiveHand(id, owner.Gen, "exec_a", "x"); err != nil {
		t.Fatal(err)
	}
	if err := h.runner.PassiveHand("missing", 0, "exec_a", "x"); err == nil {
		t.Fatal("an unknown conversation was accepted")
	}
}

func TestComposerTargetNeedsAnOpenHostedLead(t *testing.T) {
	h := newLeadHarness(t).start()
	id := h.lead(BackendClaude, LeadOptions{}).ID
	target, err := h.runner.ComposerTarget(id)
	if err != nil || target.Owner.ID != id || !target.Owner.Lead || target.Root == "" || len(target.Environ) == 0 {
		t.Fatalf("target = %+v, %v", target, err)
	}
	if _, err := h.runner.ComposerTarget("missing"); err == nil {
		t.Fatal("an unknown conversation gave a target")
	}
}

// TestPassiveHandStartsNoTurnAndRidesTheNextSend: for every backend's prompt
// shape, a result handed while the lead is idle starts no model request and
// leaves the send and held counters alone; the next ordinary send carries it
// once.
func TestPassiveHandStartsNoTurnAndRidesTheNextSend(t *testing.T) {
	type backendCase struct {
		name   string
		new    func(*testing.T) (*leadHarness, func(string))
		start  func(*leadHarness)
		inputs func(*leadHarness, string) []string
	}
	cases := []backendCase{
		{BackendClaude, func(t *testing.T) (*leadHarness, func(string)) { return newLeadHarness(t), func(string) {} }, func(h *leadHarness) { h.start() }, (*leadHarness).userInputs},
		{BackendPi, func(t *testing.T) (*leadHarness, func(string)) { return newLeadHarness(t), func(string) {} }, func(h *leadHarness) { h.start() }, piPrompts},
		{BackendCodex, func(t *testing.T) (*leadHarness, func(string)) {
			h, fake := newCodexLeadHarness(t)
			return h, func(id string) { fake.replyID = id }
		}, func(h *leadHarness) { h.startAt(codexSyntheticEndpoint) }, codexUserInputs},
		{BackendAgy, func(t *testing.T) (*leadHarness, func(string)) {
			h, fake := newAgyLeadHarness(t)
			return h, func(id string) { fake.replyID = id }
		}, func(h *leadHarness) { h.startAt("http://127.0.0.1:1") }, agyPromptTexts},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, bind := tc.new(t)
			tc.start(h)
			id := h.lead(tc.name, LeadOptions{}).ID
			bind(id)
			owner, err := h.runner.ExecOwner(id)
			if err != nil {
				t.Fatal(err)
			}
			before := h.record(id)
			stateBefore := h.state(id)
			if err := h.runner.PassiveHand(id, owner.Gen, "exec_x", "[user ran from the dashboard] exec_id exec_x exited with code 0\ncommand: ls\noutput:\nfile.txt\n"); err != nil {
				t.Fatal(err)
			}
			// Idle: nothing reaches the model.
			time.Sleep(150 * time.Millisecond)
			if holder := h.record(id).HolderID; holder != "" {
				if got := tc.inputs(h, holder); len(got) != 0 {
					t.Fatalf("a passive hand-off started a turn: %q", got)
				}
			}
			after, stateAfter := h.record(id), h.state(id)
			if after.Sends != before.Sends || stateAfter.Held != stateBefore.Held || stateAfter.Status != stateBefore.Status {
				t.Fatalf("send/held counters moved: sends %d->%d held %d->%d status %v->%v", before.Sends, after.Sends, stateBefore.Held, stateAfter.Held, stateBefore.Status, stateAfter.Status)
			}
			h.send(id, "hello")
			holder := h.holder(id)
			var got []string
			eventually(t, tc.name+" send with the receipt", func() bool {
				got = tc.inputs(h, holder)
				return len(got) == 1
			})
			if !strings.Contains(got[0], "[user ran from the dashboard] exec_id exec_x") || !strings.Contains(got[0], "file.txt") || !strings.Contains(got[0], "untrusted data") || !strings.HasSuffix(strings.TrimSpace(got[0]), h.envelope("hello")) && !strings.Contains(got[0], h.envelope("hello")) {
				t.Fatalf("send = %q", got[0])
			}
			if strings.Count(got[0], "exec_id exec_x") != 1 {
				t.Fatalf("the receipt rode twice: %q", got[0])
			}
		})
	}
}

// TestPassiveReceiptSurvivesAFailedSend: a send that fails leaves the receipt
// pending, a receipt that arrives meanwhile is not lost, and the retried send
// carries both exactly once; the one after it carries neither.
func TestPassiveReceiptSurvivesAFailedSend(t *testing.T) {
	h := newLeadHarness(t).start()
	id := h.lead(BackendClaude, LeadOptions{}).ID
	owner, _ := h.runner.ExecOwner(id)
	if err := h.runner.PassiveHand(id, owner.Gen, "exec_one", "RECEIPT-ONE"); err != nil {
		t.Fatal(err)
	}
	h.holders.failInputs("user", 1)
	h.send(id, "retry")
	if err := h.runner.PassiveHand(id, owner.Gen, "exec_two", "RECEIPT-TWO"); err != nil {
		t.Fatal(err)
	}
	var retryHolder string
	var got []string
	eventually(t, "retried send with both receipts", func() bool {
		retryHolder = h.record(id).HolderID
		if retryHolder == "" {
			return false
		}
		got = h.userInputs(retryHolder)
		return len(got) == 1 && strings.Contains(got[0], "RECEIPT-ONE")
	})
	if strings.Count(got[0], "RECEIPT-ONE") != 1 {
		t.Fatalf("retried send = %q", got[0])
	}
	h.holders.out(retryHolder, turnLines("sess-1", "u1", "ok")...)
	h.idle(id)
	h.send(id, "after")
	eventually(t, "next send", func() bool { return len(h.userInputs(retryHolder)) == 2 })
	next := h.userInputs(retryHolder)
	if strings.Contains(next[1], "RECEIPT-ONE") {
		t.Fatalf("an acknowledged receipt rode again: %q", next[1])
	}
	// RECEIPT-TWO arrived while the first attempt was in flight: it was
	// either carried by the retry or is carried now, never lost or doubled.
	if n := strings.Count(strings.Join(next, "\n"), "RECEIPT-TWO"); n != 1 {
		t.Fatalf("RECEIPT-TWO appears %d times in %q", n, next)
	}
}
