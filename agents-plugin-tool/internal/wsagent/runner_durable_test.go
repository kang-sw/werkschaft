package wsagent

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

func TestDurableRunnerLoadsPrivateStateAcrossAnOlderScheduler(t *testing.T) {
	h := newLeadHarness(t)
	opts := h.opts
	opts.Holders = nil
	gotReports := &reports{}
	opts.Report = gotReports.add
	reg := opts.Backends[BackendClaude]
	reg.ProcessKind = ProcessDurable
	opts.Backends[BackendClaude] = reg

	start := func(options Options) *Runner {
		t.Helper()
		r := NewRunner(options)
		r.Start(nil)
		t.Cleanup(r.Close)
		return r
	}

	rec := Record{
		ReplyID: "fixture", WSKey: "fixture-key", Backend: BackendClaude, Profile: AgentWorkerProfile(BackendClaude),
		Root: h.root, CreatedAt: opts.Now().UTC().Format(time.RFC3339),
		Parent:   &ParentRef{Kind: ParentLead, ReplyID: "parent"},
		TurnOpen: true, TurnMark: 2, TurnStartedAt: opts.Now().UnixMilli(),
		ReportID: "earlier-report", LastReportedTurn: "earlier-report",
		LastTurn: &PendingReport{ID: "earlier-report", Result: TurnResult{ID: "fixture", Status: TurnDone, Message: "settled history"}},
	}
	dir := wsdashboard.SessionDir(opts.Dir, rec.ReplyID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	newRunner := start(opts)
	if err := newRunner.persistAgentRecord(dir, rec); err != nil {
		t.Fatal(err)
	}
	projection, err := readRecord(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.handedOff() || projection.TurnOpen || projection.TurnStartedAt != 0 || projection.ReportID != "" {
		t.Fatal("legacy projection contains active runner state")
	}
	if projection.LastTurn == nil || projection.LastTurn.Result.Message != "settled history" {
		t.Fatal("legacy projection lost settled history")
	}
	newRunner.Close()

	oldOpts := opts
	oldOpts.Backends = maps.Clone(opts.Backends)
	reg.ProcessKind = ProcessExternal
	oldOpts.Backends[BackendClaude] = reg
	oldRunner := start(oldOpts)
	if oldRunner.conv(rec.ReplyID) != nil || len(gotReports.list()) != 0 {
		t.Fatal("legacy startup scheduled the projected record")
	}
	oldRunner.Close()

	// Simulate a missing history projection after a successful private
	// commit. The successor can reconstruct it without changing the turn.
	if err := os.Remove(recordPath(dir)); err != nil {
		t.Fatal(err)
	}
	successor := start(opts)
	c := successor.conv(rec.ReplyID)
	if c == nil {
		t.Fatal("successor did not restore the private record")
	}
	recovered := c.record()
	if !recovered.TurnOpen || recovered.TurnMark != rec.TurnMark || recovered.TurnStartedAt != rec.TurnStartedAt {
		t.Fatal("successor changed the active turn")
	}
	if len(gotReports.list()) != 0 {
		t.Fatal("startup sent a report before recovery")
	}
	projection, err = readRecord(dir)
	if err != nil || !projection.handedOff() || projection.TurnOpen {
		t.Fatalf("successor did not reconstruct a fenced projection: %v", err)
	}
}

func TestRunnerWithExternalBackendsDoesNotOpenPrivateStore(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(Options{Dir: dir})
	r.Start(nil)
	t.Cleanup(r.Close)
	if _, err := os.Stat(filepath.Join(dir, wsmeta.DBName)); !os.IsNotExist(err) {
		t.Fatalf("metadata store opened without a durable backend: %v", err)
	}
}

func TestDurableRunnerForgetClosedDeletesPrivateRecord(t *testing.T) {
	const backend = "fixture"
	r := NewRunner(Options{
		Dir:      t.TempDir(),
		Backends: map[string]BackendRegistration{backend: {ProcessKind: ProcessDurable}},
	})
	r.Start(nil)
	t.Cleanup(r.Close)
	rec := Record{
		ReplyID: "finished", Backend: backend, Root: t.TempDir(), WSKey: "fixture-key",
		ClosedAt: r.opts.Now().UTC().Format(time.RFC3339),
	}
	dir := wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := r.persistAgentRecord(dir, rec); err != nil {
		t.Fatal(err)
	}
	r.loadClosed(rec)

	r.ForgetClosed(rec.ReplyID)

	rows, err := r.durableRecords.DurableAgentRecords(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatalf("private records after purge = %d, error %v", len(rows), err)
	}
	if _, err := os.Stat(recordPath(dir)); err != nil {
		t.Fatalf("caller no longer owns directory cleanup: %v", err)
	}
}

func TestDurableSettlementCommitsBeforeWaitAndReport(t *testing.T) {
	for _, held := range []bool{false, true} {
		name := "immediate"
		if held {
			name = "held by exec"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			const backend = "fixture"
			var r *Runner
			reports := 0
			r = NewRunner(Options{
				Dir: dir,
				Backends: map[string]BackendRegistration{
					backend: {ProcessKind: ProcessDurable},
				},
				Report: func(rep TurnReport) {
					reports++
					rows, err := r.durableRecords.DurableAgentRecords(context.Background())
					if err != nil || len(rows) != 1 {
						t.Fatalf("durable state during callback = %v, %v", rows, err)
					}
					committed, err := decodeRecord("fixture", rows[0].ReplyID, rows[0].JSON)
					if err != nil || committed.TurnOpen || committed.DurableTurn != nil || committed.ReportID != "original" ||
						committed.LastTurn == nil || committed.LastTurn.Mark != 2 || committed.LastTurn.Result.Message != rep.Result.Message {
						t.Fatalf("callback did not observe its committed settlement: %+v, %v", committed, err)
					}
				},
			})
			r.openDurableAgentRecords()
			t.Cleanup(r.Close)
			if r.durableRecords == nil {
				t.Fatal("the private record store did not open")
			}
			rec := Record{ReplyID: "fixturechild", Backend: backend, Root: dir, WSKey: "fixture-key",
				Parent: &ParentRef{Kind: ParentLead, ReplyID: "parent"}, TurnMark: 2,
				TurnOpen: true, TurnStartedAt: r.opts.Now().UnixMilli(), DurableTurn: &durableTurnInput{ID: "original", Text: "work"},
			}
			sessionDir := wsdashboard.SessionDir(dir, rec.ReplyID)
			if err := os.MkdirAll(sessionDir, 0o700); err != nil {
				t.Fatal(err)
			}
			c := &conv{r: r, rec: rec, dir: sessionDir, reply: "final answer", settleC: make(chan struct{})}
			c.live = newLiveWriter(sessionDir, rec.WSKey, backend, r.opts.Now, r.opts.Log)
			if err := r.persistAgentRecord(c.dir, rec); err != nil {
				t.Fatal(err)
			}

			if held {
				r.ExpectExecJobs()
				if err := c.settleTurn(TurnDone, "", 2); err != nil {
					t.Fatal(err)
				}
				if reports != 0 || c.rec.HeldTurn == nil || c.rec.DurableHeldReportID != "original" || c.rec.DurableTurn != nil {
					t.Fatal("the exec hold lost the original turn identity")
				}
				r.BindExecJobs(nil)
			}

			signal := c.settleC
			if err := r.durableRecords.Close(); err != nil {
				t.Fatal(err)
			}
			if held {
				c.releaseHeld()
				if c.heldRes == nil || c.rec.DurableHeldReportID != "original" {
					t.Fatal("failed write discarded the held result")
				}
			} else {
				if err := c.settleTurn(TurnDone, "", 2); err == nil {
					t.Fatal("settlement reported success despite a closed private store")
				}
				if !c.rec.TurnOpen || c.rec.DurableTurn == nil || c.reply == "" {
					t.Fatal("failed write discarded the open turn")
				}
			}
			select {
			case <-signal:
				t.Fatal("failed write woke result waiters")
			default:
			}
			if reports != 0 || c.settled.n != 0 {
				t.Fatal("failed write published a turn")
			}

			r.openDurableAgentRecords()
			if held {
				c.releaseHeld()
				c.releaseHeld()
			} else if err := c.settleTurn(TurnDone, "", 2); err != nil {
				t.Fatal(err)
			}
			if reports != 1 || c.settled.n != 1 || c.settled.mark != 2 {
				t.Fatalf("settled %d times and reported %d times", c.settled.n, reports)
			}
			if !isClosed(signal) {
				t.Fatal("committed result did not wake waiters")
			}
			projected, err := readRecord(sessionDir)
			if err != nil || projected.DurableTurn != nil || projected.DurableHeldReportID != "" ||
				projected.LastTurn == nil || projected.LastTurn.Result.Message != "final answer" {
				t.Fatalf("incorrect history projection after settlement: %+v, %v", projected, err)
			}
		})
	}
}

func durableTurnHarness(t *testing.T) (*harness, *reports, <-chan durableTurnInput) {
	t.Helper()
	h := newHarness(t, map[string]string{"chat": `{"backend":"claude","ws_tools":["tickets.query"]}`})
	h.runner.Close()
	submitted := make(chan durableTurnInput, 16)
	reg := h.config.Backends[suiteBackend]
	reg.Backend = durableStartFixture{fakeBackend: h.backend, submitted: submitted}
	reg.ProcessKind = ProcessDurable
	h.config.Backends[suiteBackend] = reg
	h.endpoint = "http://127.0.0.1:1"
	got := &reports{}
	h.config.Report = got.add
	h.start()
	return h, got, submitted
}

func nextDurableInput(t *testing.T, submitted <-chan durableTurnInput) durableTurnInput {
	t.Helper()
	select {
	case input := <-submitted:
		return input
	case <-time.After(5 * time.Second):
		t.Fatal("no typed input reached the process")
		return durableTurnInput{}
	}
}

func TestDurableRunnerResumesOriginalTurnAfterExecBinding(t *testing.T) {
	h, got, submitted := durableTurnHarness(t)
	idle := h.create("chat")
	child := h.spawn("first input", leadParent)
	input := nextDurableInput(t, submitted)
	firstSpec := h.backend.waitProcStart(t, 0)
	old := h.runner.conv(child.ID).record()
	if old.DurableTurn == nil || *old.DurableTurn != input || input.ID == "" || !strings.Contains(input.Text, "first input") ||
		firstSpec.SessionID == "" || old.SessionID != firstSpec.SessionID || !old.TurnOpen || len(old.Queued) != 0 {
		t.Fatal("process received input without a matching admission and backend identity")
	}
	h.runner.Close()
	if len(got.list()) != 0 {
		t.Fatal("replacement settled the unfinished turn")
	}

	h.runner = NewRunner(h.config)
	t.Cleanup(h.runner.Close)
	h.runner.SetEndpoint(h.endpoint)
	h.runner.ExpectExecJobs()
	h.runner.Start(nil)
	c := h.runner.conv(child.ID)
	if c == nil || h.backend.startCount() != 1 {
		t.Fatal("active turn was not retained behind the exec binding fence")
	}
	h.runner.BindExecJobs(nil)
	if recovered := nextDurableInput(t, submitted); recovered != input {
		t.Fatalf("resumed input changed: got %+v, want %+v", recovered, input)
	}
	spec := h.backend.waitProcStart(t, 1)
	rec := c.record()
	if spec.SessionID != old.SessionID || rec.TurnMark != old.TurnMark || rec.TurnStartedAt != old.TurnStartedAt {
		t.Fatal("replacement started a different session or turn boundary")
	}
	hello, _, ok := h.runner.ResolveMCPToken(spec.MCPSession.Token)
	if !ok || hello.Conversation != child.ID {
		t.Fatal("resumed process has no restored MCP authority")
	}

	c.do(func() { c.handle(Event{TurnEnd: true, TurnID: "earlier"}) })
	if rec := c.record(); rec.DurableTurn == nil || rec.DurableTurn.ID != input.ID || len(got.list()) != 0 {
		t.Fatal("a stale end event settled the active turn")
	}
	p := h.backend.proc(1)
	p.events <- Event{TurnID: input.ID, Live: &wslive.Event{Kind: wslive.KindReply, Text: "final result", Final: true}}
	p.events <- Event{TurnID: input.ID, TurnEnd: true}
	eventually(t, "the recovered turn is reported", func() bool { return len(got.list()) == 1 })
	c.do(func() { c.handle(Event{TurnID: input.ID, TurnEnd: true}) })
	rec = c.record()
	if rec.DurableTurn != nil || rec.TurnOpen || rec.ReportID != input.ID || got.list()[0].Result.Message != "final result" {
		t.Fatal("original turn's completion did not reconcile with the original report ID")
	}
	h.runner.conv(idle.ID).do(func() {})
	if h.backend.startCount() != 2 || len(got.list()) != 1 {
		t.Fatal("replacement woke the idle session or sent an extra report")
	}
	if _, err := h.runner.Send(idle.ID, "wake"); err != nil {
		t.Fatal(err)
	}
	if wake := nextDurableInput(t, submitted); wake.ID == input.ID || !strings.Contains(wake.Text, "wake") {
		t.Fatal("an idle session did not accept a new turn on wake")
	}
}

func TestDurableRunnerRecoversInboxClaimsAcrossCommit(t *testing.T) {
	for _, committed := range []bool{false, true} {
		name := "before commit"
		if committed {
			name = "after commit"
		}
		t.Run(name, func(t *testing.T) {
			h, _, submitted := durableTurnHarness(t)
			st := h.create("chat")
			c := h.runner.conv(st.ID)
			_, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Parts: []string{"once only"}}, h.config.Now())
			if err != nil {
				t.Fatal(err)
			}
			held, err := wsinbox.Held(c.inbox)
			if err != nil || len(held) != 1 {
				t.Fatalf("stage inbox fixture: %v, %d messages", err, len(held))
			}
			claims, err := wsinbox.ClaimInputs(c.inbox, held)
			if err != nil || len(claims) != 1 {
				t.Fatalf("claim inbox fixture: %v, %d messages", err, len(claims))
			}
			if committed {
				c.do(func() {
					err = c.commitDurableRecord(func(rec *Record) {
						rec.Queued = []QueuedInput{{Text: claims[0].Entry.Text()}}
						rec.DurableInboxClaims = []string{claims[0].Name}
					}, nil)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			h.runner.Close()
			h.start()
			input := nextDurableInput(t, submitted)
			if strings.Count(input.Text, "once only") != 1 {
				t.Fatalf("claimed input was lost or duplicated: %q", input.Text)
			}
			drained, err := wsinbox.Drained(c.inbox)
			if err != nil || len(drained) != 1 || drained[0].Name != claims[0].Name {
				t.Fatal("the recovered claim did not reach drained history once")
			}
		})
	}
}
