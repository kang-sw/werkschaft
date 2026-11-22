package wsagent

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscron"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wsstate"
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

// TestCronIsNotAChildTool: a scheduled prompt can only reach a hosted lead,
// so no worker allowlist lists cron; a worker's call is refused by the
// server (TestCronRefusesAWorker in the mcp package).
func TestCronIsNotAChildTool(t *testing.T) {
	for name, list := range map[string][]string{
		"ChildSafeWSTools": ChildSafeWSTools, "AgentWSTools": AgentWSTools,
		"PiMiniLeadWSTools": PiMiniLeadWSTools, "PiWorkerWSTools": PiWorkerWSTools,
	} {
		if slices.Contains(list, "cron") {
			t.Errorf("%s lists cron", name)
		}
	}
}

// TestAgyLeadReachesCronThroughTheCallFallback pins the mechanism: agy lists
// only a curated set of tools directly, and cron is not among them, so an agy
// lead calls it through the call fallback its registration enables.
func TestAgyLeadReachesCronThroughTheCallFallback(t *testing.T) {
	_, reg := (AgyBackend{}).registration()
	if slices.Contains(reg.DirectTools, "cron") {
		t.Fatalf("agy lists cron directly: %v", reg.DirectTools)
	}
	if !reg.CallFallback {
		t.Fatal("agy has no call fallback, so its lead could not reach cron")
	}
}

// cronFileOps runs fn and returns the wsstate-counted file operations it
// caused, per operation kind and per source site.
func cronFileOps(fn func()) (kinds, sites map[string]int64) {
	before := wsstate.FileIOCounters()
	fn()
	after := wsstate.FileIOCounters()
	kinds, sites = map[string]int64{}, map[string]int64{}
	for k, v := range after {
		if d := v - before[k]; d != 0 {
			if strings.HasPrefix(k, "file_io_site:") {
				sites[strings.TrimPrefix(k, "file_io_site:")] = d
			} else {
				kinds[k] = d
			}
		}
	}
	return kinds, sites
}

// TestDeliverCronFileOperationsAtTheProductionSeam measures one accepted and
// one suppressed delivery through the runner's real seam: the inbox entry
// write and the record publication an accepted one causes, and nothing for
// a suppressed one. SQLite commits are not file operations in this count (the
// store's own file I/O does not pass through wsstate) and are measured in
// wscron. The logged figures are what the ticket records; the assertions
// bound the kinds that must not appear on this path.
func TestDeliverCronFileOperationsAtTheProductionSeam(t *testing.T) {
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), toolUseLine("u1", "toolu_1"))
	h.sync(id, holder)
	mark := wsinbox.Cron{ID: "cron-cc33", Name: "n", At: "2026-03-10T09:00:00Z"}
	var ok bool
	kinds, sites := cronFileOps(func() { ok, _ = h.runner.DeliverCron(id, mark, "tick") })
	if !ok {
		t.Fatal("not delivered")
	}
	t.Logf("accepted delivery: file operations by kind %v", kinds)
	t.Logf("accepted delivery: file operations by site %v", sites)
	// The counters are process-wide, so the runner's own follow-ups to an
	// input (a second record rewrite, a registry read after the wake) can
	// land in the sample; what is pinned is that the two writes the delivery
	// itself makes are there and that each was one temp file plus one atomic
	// replace.
	for _, site := range []string{"wsinbox/inbox_store.go:65:create_temp", "wsagent/record.go:393:create_temp"} {
		if sites[site] < 1 {
			t.Errorf("no %s in %v", site, sites)
		}
	}
	if kinds["file_io_create_temp"] < 2 || kinds["file_io_atomic_replace"] < 2 {
		t.Errorf("the inbox write and the record write are each a temp file and an atomic replace: %v", kinds)
	}
	if _, err := h.runner.CloseConversation(id); err != nil {
		t.Fatal(err)
	}
	kinds, sites = cronFileOps(func() { ok, _ = h.runner.DeliverCron(id, mark, "late") })
	if ok {
		t.Fatal("a closed conversation accepted a delivery")
	}
	t.Logf("suppressed delivery: file operations by kind %v by site %v", kinds, sites)
	if kinds["file_io_total"] != 0 {
		t.Errorf("a suppressed delivery ran file operations: %v %v", kinds, sites)
	}
}

// runnerCronSessions is the runner as the real scheduler's target side, the
// way the daemon wires it (cmd/ws-mcp cronSessions).
type runnerCronSessions struct{ r *Runner }

func (s runnerCronSessions) IsLead(id string) (bool, error) { return s.r.CronIsLead(id) }
func (s runnerCronSessions) Exists(id string) bool          { return s.r.CronKnown(id) }
func (s runnerCronSessions) HeldSchedules(id string) []string {
	return s.r.CronHeld(id)
}
func (s runnerCronSessions) Deliver(id string, mark wsinbox.Cron, prompt string) (wscron.Delivery, error) {
	ok, err := s.r.DeliverCron(id, mark, prompt)
	if err != nil {
		return 0, err
	}
	if !ok {
		return wscron.Suppressed, nil
	}
	return wscron.Delivered, nil
}

// TestForgetClosedRemovesAHostedLeadsStoredSchedules drives a hosted lead
// through close and the real ForgetClosed with the real scheduler and store:
// the rows are gone the moment ForgetClosed returns (no restart reaping), a
// close alone keeps them, and the lead's retained binding is refused after.
func TestForgetClosedRemovesAHostedLeadsStoredSchedules(t *testing.T) {
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "done")...)
	h.idle(id)
	store, err := wscron.OpenStore(filepath.Join(t.TempDir(), wscron.StoreName))
	if err != nil {
		t.Fatal(err)
	}
	sched, err := wscron.New(wscron.Options{Store: store, Sessions: runnerCronSessions{r: h.runner}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sched.Close() })
	h.runner.BindCron(sched)
	t.Cleanup(func() { h.runner.BindCron(nil) })
	rows := func() int {
		got, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	for _, name := range []string{"a", "b"} {
		if _, _, err := sched.Add(id, name, "@daily", "tick", ""); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	if rows() != 2 {
		t.Fatalf("stored rows = %d, want 2", rows())
	}

	// A close keeps the schedules: the conversation is closed, not gone.
	if _, err := h.runner.CloseConversation(id); err != nil {
		t.Fatal(err)
	}
	if rows() != 2 || len(sched.List(id)) != 2 {
		t.Fatalf("a close removed schedules: %d rows, %d live", rows(), len(sched.List(id)))
	}
	// The retained binding is refused while closed: no delivery target.
	if lead, err := sched.IsLead(id); lead || err == nil {
		t.Fatalf("a closed lead is a target: %v, %v", lead, err)
	}

	h.runner.ForgetClosed(id)
	if got := rows(); got != 0 {
		t.Fatalf("stored rows right after ForgetClosed = %d, want 0", got)
	}
	if got := len(sched.List(id)); got != 0 {
		t.Fatalf("%d live schedules after ForgetClosed", got)
	}
	if lead, err := sched.IsLead(id); lead || err == nil {
		t.Fatalf("a forgotten lead is a target: %v, %v", lead, err)
	}
	if _, _, err := sched.Add(id, "late", "@daily", "tick", ""); !errors.Is(err, wscron.ErrNotLead) {
		t.Fatalf("add through the retained binding = %v, want ErrNotLead", err)
	}
	if got := rows(); got != 0 {
		t.Fatalf("a refused add wrote %d rows", got)
	}
}
