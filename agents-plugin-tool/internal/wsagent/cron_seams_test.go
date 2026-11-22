package wsagent

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

type cronGoneLog struct {
	mu  sync.Mutex
	ids []string
}

func (l *cronGoneLog) ConversationGone(id string) {
	l.mu.Lock()
	l.ids = append(l.ids, id)
	l.mu.Unlock()
}

func (l *cronGoneLog) got() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ids...)
}

type cronRemovals struct {
	mu  sync.Mutex
	ids []string
}

func (l *cronRemovals) add(c wsinbox.Cron) {
	l.mu.Lock()
	l.ids = append(l.ids, c.ID)
	l.mu.Unlock()
}

func (l *cronRemovals) got() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ids...)
}

// TestDeliverCronHoldsUntilTheTurnEndsThenDrains: a prompt that arrives
// mid-turn is held, never steered into the running turn, counted as a send,
// and taken by the next turn, which reports the removal to the observer.
func TestDeliverCronHoldsUntilTheTurnEndsThenDrains(t *testing.T) {
	var removals cronRemovals
	stop := wsinbox.ObserveCronRemoval(removals.add)
	defer stop()
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), toolUseLine("u1", "toolu_1"))
	h.sync(id, holder)
	if lead, err := h.runner.CronIsLead(id); err != nil || !lead {
		t.Fatalf("CronIsLead = %v, %v", lead, err)
	}
	sendsBefore := h.record(id).Sends
	before := len(h.userInputs(holder))
	mark := wsinbox.Cron{ID: "cron-aa11", Name: "board", At: "2026-03-10T09:00:00+09:00"}
	if ok, err := h.runner.DeliverCron(id, mark, "check the board"); err != nil || !ok {
		t.Fatalf("DeliverCron = %v, %v", ok, err)
	}
	if got := h.record(id).Sends; got != sendsBefore+1 {
		t.Fatalf("sends = %d, want %d", got, sendsBefore+1)
	}
	if ids := h.runner.CronHeld(id); len(ids) != 1 || ids[0] != "cron-aa11" {
		t.Fatalf("held cron entries = %v", ids)
	}
	held, err := wsinbox.Held(wsinbox.Dir(wsdashboard.SessionDir(h.dir, id)))
	if err != nil || len(held) != 1 || held[0].Entry.Steer || held[0].Entry.Cron == nil {
		t.Fatalf("held = %+v, %v", held, err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := len(h.userInputs(holder)); got != before {
		t.Fatalf("the prompt reached the running turn (%d inputs, was %d)", got, before)
	}
	if len(removals.got()) != 0 {
		t.Fatalf("observer fired before the drain: %v", removals.got())
	}

	h.holders.out(holder, turnLines("sess-1", "u2", "done")...)
	eventually(t, "the next turn carries the scheduled prompt", func() bool {
		inputs := h.userInputs(holder)
		return len(inputs) > before && strings.Contains(inputs[len(inputs)-1], `<ws:cron id="cron-aa11" name="board" at="2026-03-10T09:00:00+09:00">`)
	})
	if got := removals.got(); len(got) != 1 || got[0] != "cron-aa11" {
		t.Fatalf("drain observed %v", got)
	}
	if ids := h.runner.CronHeld(id); len(ids) != 0 {
		t.Fatalf("still held after the drain: %v", ids)
	}
}

// TestDeliverCronRefusedForClosedUnknownAndNonLead: nothing is written and
// the outcome is "not accepted"; a close drops a held entry and reports it.
func TestDeliverCronRefusedForClosedUnknownAndNonLead(t *testing.T) {
	var removals cronRemovals
	stop := wsinbox.ObserveCronRemoval(removals.add)
	defer stop()
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), toolUseLine("u1", "toolu_1"))
	h.sync(id, holder)
	mark := wsinbox.Cron{ID: "cron-bb22", Name: "n", At: "2026-03-10T09:00:00Z"}
	if ok, err := h.runner.DeliverCron("nobody", mark, "x"); err != nil || ok {
		t.Fatalf("unknown conversation = %v, %v", ok, err)
	}
	if ok, err := h.runner.DeliverCron(id, mark, "held then dropped"); err != nil || !ok {
		t.Fatalf("DeliverCron = %v, %v", ok, err)
	}
	if _, err := h.runner.CloseConversation(id); err != nil {
		t.Fatal(err)
	}
	if got := removals.got(); len(got) != 1 || got[0] != "cron-bb22" {
		t.Fatalf("the held-entry drop observed %v", got)
	}
	if ok, err := h.runner.DeliverCron(id, mark, "late"); err != nil || ok {
		t.Fatalf("closed conversation = %v, %v", ok, err)
	}
	if _, err := h.runner.CronIsLead(id); err == nil {
		t.Fatal("a closed conversation is a delivery target")
	}
	if !h.runner.CronKnown(id) {
		t.Fatal("a closed conversation is still known")
	}
	if h.runner.CronKnown("nobody") {
		t.Fatal("an unknown conversation is known")
	}
}

func TestDeliverCronRefusesANonLead(t *testing.T) {
	h := newHarness(t, map[string]string{"chat": chatProfile})
	id := h.create("chat").ID
	if lead, err := h.runner.CronIsLead(id); err != nil || lead {
		t.Fatalf("CronIsLead = %v, %v", lead, err)
	}
	mark := wsinbox.Cron{ID: "cron-cc33", Name: "n", At: "2026-03-10T09:00:00Z"}
	if ok, err := h.runner.DeliverCron(id, mark, "x"); err != nil || ok {
		t.Fatalf("non-lead = %v, %v", ok, err)
	}
	held, _ := wsinbox.Held(wsinbox.Dir(wsdashboard.SessionDir(h.dir, id)))
	if len(held) != 0 {
		t.Fatalf("a refused delivery wrote %+v", held)
	}
}

// TestForgetClosedReportsPermanentEndOnly: ForgetClosed of a closed
// conversation tells the scheduler; one the runner still holds open is not
// gone.
func TestForgetClosedReportsPermanentEndOnly(t *testing.T) {
	var gone cronGoneLog
	h := newLeadHarness(t).start()
	h.runner.BindCron(&gone)
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "done")...)
	h.idle(id)
	h.runner.ForgetClosed(id) // still open: nothing is forgotten
	if got := gone.got(); len(got) != 0 {
		t.Fatalf("an open conversation was reported gone: %v", got)
	}
	if _, err := h.runner.CloseConversation(id); err != nil {
		t.Fatal(err)
	}
	if got := gone.got(); len(got) != 0 {
		t.Fatalf("a close alone was reported gone: %v", got)
	}
	h.runner.ForgetClosed(id)
	if got := gone.got(); len(got) != 1 || got[0] != id {
		t.Fatalf("ForgetClosed reported %v, want [%s]", got, id)
	}
	if h.runner.CronKnown(id) {
		t.Fatal("a forgotten conversation is still known")
	}
	h.runner.BindCron(nil)
	h.runner.ForgetClosed(id)
}
