package wsagent

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
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
