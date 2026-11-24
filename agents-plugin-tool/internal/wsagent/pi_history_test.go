package wsagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wslive"
)

func TestRetiredPiHistorySurvivesPruneAndRollback(t *testing.T) {
	for _, mode := range []string{"current prune", "0.51.2 rollback"} {
		t.Run(mode, func(t *testing.T) {
			const id = "oldpihistory"
			const raw = "{\"type\":\"session\",\"id\":\"pi-history-fixture\"}\n"
			dir := t.TempDir()
			sessionDir := wsdashboard.SessionDir(dir, id)
			piFile := filepath.Join(sessionDir, "pi", "session.jsonl")
			if err := os.MkdirAll(filepath.Dir(piFile), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(piFile, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, time.November, 24, 0, 0, 0, 0, time.UTC)
			rec := Record{ReplyID: id, Backend: BackendPi, Lead: true, Root: dir, WSKey: "fixture", CreatedAt: now.Format(time.RFC3339)}
			if err := writeRecord(sessionDir, rec); err != nil {
				t.Fatal(err)
			}
			if err := wsdashboard.WriteRegistration(dir, registration(rec, now)); err != nil {
				t.Fatal(err)
			}
			if err := wslive.AppendRing(wslive.RingDir(sessionDir), []wslive.RingEntry{{Kind: "reply", Text: "retired output"}}); err != nil {
				t.Fatal(err)
			}
			start := func(at time.Time) *Runner {
				r := NewRunner(Options{Dir: dir, Now: func() time.Time { return at }})
				t.Cleanup(r.Close)
				r.Start(nil)
				st, err := r.Get(id)
				if err != nil || st.Status != StatusClosed || st.ClosedAt == "" {
					t.Fatalf("retired Pi state = %+v, %v", st, err)
				}
				return r
			}
			start(now).Close()
			pruneAt := now.Add(wsdashboard.RegistryRetention + time.Hour)
			var runner *Runner
			want := []string{"retired output"}
			if mode == "current prune" {
				runner = start(pruneAt)
				if n := wsdashboard.PruneRegistrations(dir, pruneAt, wsdashboard.RegistryRetention); n != 0 {
					t.Fatalf("0.52 pruned %d retired Pi sessions", n)
				}
			} else {
				// The first rollback leaves this entry in the live ring, so
				// it exists when 0.52 runs again and enters its next backup.
				ring := wslive.RingDir(sessionDir)
				if err := wslive.AppendRing(ring, []wslive.RingEntry{{Kind: "reply", Text: "survived first rollback"}}); err != nil {
					t.Fatal(err)
				}
				start(now.Add(time.Minute)).Close()
				want = append(want, "survived first rollback")
				// Entries written only during the second rollback age out
				// under the old daemon's retention.
				if err := wslive.AppendRing(ring, []wslive.RingEntry{{Kind: "reply", Text: "second rollback only"}}); err != nil {
					t.Fatal(err)
				}
				pruneRetiredPiLeadLike0512(t, dir, id, pruneAt)
				if _, err := os.Stat(sessionDir); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("0.51.2 prune left session directory: %v", err)
				}
				// An interrupted publication can leave the prior complete
				// archive in the recovery slot rather than at its final name.
				archive := filepath.Join(dir, retiredPiHistoryDir, id)
				if err := os.Rename(archive, archive+previousPiHistorySuffix); err != nil {
					t.Fatal(err)
				}
				runner = start(pruneAt)
			}
			history, err := runner.Transcript(id)
			if err != nil || len(history) != len(want) {
				t.Fatalf("readback = %+v, %v", history, err)
			}
			for i, text := range want {
				if history[i].Text != text {
					t.Fatalf("readback[%d] = %q, want %q", i, history[i].Text, text)
				}
			}
			if data, err := os.ReadFile(piFile); err != nil || string(data) != raw {
				t.Fatalf("Pi JSONL readback = %q, %v", data, err)
			}
			if reg, err := wsdashboard.ReadRegistration(dir, id); err != nil || reg.EndedAt == "" {
				t.Fatalf("recovered row = %+v, %v", reg, err)
			}
			if err := runner.PrepareWorktreeHistoryPurge(dir, map[string]bool{id: true}); err != nil {
				t.Fatal(err)
			}
			runner.ForgetClosed(id)
			if err := os.RemoveAll(sessionDir); err != nil {
				t.Fatal(err)
			}
			runner.CompleteWorktreeHistoryPurge(dir)
			runner.Close()
			successor := NewRunner(Options{Dir: dir})
			t.Cleanup(successor.Close)
			successor.Start(nil)
			if _, err := successor.Transcript(id); !errors.Is(err, errUnknown) {
				t.Fatalf("explicit deletion did not remove the backup: %v", err)
			}
		})
	}
}

func TestRetiredPiArchiveSurvivesPartialRollbackPrune(t *testing.T) {
	const id = "partialpihistory"
	dir := t.TempDir()
	session := wsdashboard.SessionDir(dir, id)
	ring := wslive.RingDir(session)
	archive := filepath.Join(dir, retiredPiHistoryDir, id)
	rec := Record{ReplyID: id, Backend: BackendPi, Lead: true, Root: dir, WSKey: "fixture"}
	if err := writeRecord(session, rec); err != nil {
		t.Fatal(err)
	}
	if err := wslive.AppendRing(ring, []wslive.RingEntry{{Kind: "reply", Text: "retired output"}, {Kind: "reply", Text: "more archived output"}}); err != nil {
		t.Fatal(err)
	}
	start := func() *Runner {
		r := NewRunner(Options{Dir: dir})
		t.Cleanup(r.Close)
		r.Start(nil)
		state, err := r.Get(id)
		if err != nil || state.Status != StatusClosed {
			t.Fatalf("retired Pi state = %+v, %v", state, err)
		}
		return r
	}
	const raw = "{\"type\":\"session\"}\n"
	if err := os.MkdirAll(filepath.Join(session, "pi"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session, retiredPiSessionFile), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	start().Close()

	// Model a v0.51.2 prune interrupted after removing the ring but before
	// removing the record that 0.52 closed after taking the first archive.
	if err := os.RemoveAll(ring); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(session, "pi")); err != nil {
		t.Fatal(err)
	}
	r := start()
	if _, err := os.Stat(ring); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup should leave a partial session for manual recovery: %v", err)
	}
	savedRing := wslive.RingDir(archive)
	saved, err := wslive.ReadRing(savedRing)
	if err != nil || len(saved) != 2 || saved[0].Text != "retired output" || saved[1].Text != "more archived output" {
		t.Fatalf("archived ring after interrupted prune = %+v, %v", saved, err)
	}
	if saved, err := os.ReadFile(filepath.Join(archive, retiredPiSessionFile)); err != nil || string(saved) != raw {
		t.Fatalf("archived Pi JSONL after interrupted prune = %q, %v", saved, err)
	}
	r.Close()

	// Manually restore the missing ring and check the normal history reader.
	if err := os.CopyFS(ring, os.DirFS(savedRing)); err != nil {
		t.Fatal(err)
	}
	r = start()
	history, err := r.Transcript(id)
	if err != nil || len(history) != 2 || history[0].Text != "retired output" {
		t.Fatalf("manually recovered history = %+v, %v", history, err)
	}
	r.Close()

	// A surviving ring can be shorter and still contain rollback-era output.
	if err := os.RemoveAll(ring); err != nil {
		t.Fatal(err)
	}
	if err := wslive.AppendRing(ring, []wslive.RingEntry{{Kind: "reply", Text: "surviving rollback output"}}); err != nil {
		t.Fatal(err)
	}
	start().Close()
	saved, err = wslive.ReadRing(savedRing)
	if err != nil || len(saved) != 3 || saved[0].Text != "retired output" || saved[2].Text != "surviving rollback output" {
		t.Fatalf("archived union of ring entries = %+v, %v", saved, err)
	}
	if changed, err := retiredPiHistoryChanged(session, archive); err != nil || changed {
		t.Fatalf("archived union already covers the live history, changed=%t, %v", changed, err)
	}
}

func TestRetiredPiPurgeFailureSurvivesRestartAndRetry(t *testing.T) {
	const id = "pihistorypurge"
	dir, checkout := t.TempDir(), t.TempDir()
	session := wsdashboard.SessionDir(dir, id)
	archive := filepath.Join(dir, retiredPiHistoryDir, id)
	rec := Record{ReplyID: id, Backend: BackendPi, Lead: true, Root: checkout, WSKey: "fixture", ClosedAt: "2026-01-01T00:00:00Z"}
	if err := writeRecord(session, rec); err != nil {
		t.Fatal(err)
	}
	if err := wslive.AppendRing(wslive.RingDir(session), []wslive.RingEntry{{Kind: "reply", Text: "purge fixture"}}); err != nil {
		t.Fatal(err)
	}
	blocked := errors.New("archive removal blocked")
	start := func(fail bool) *Runner {
		r := NewRunner(Options{Dir: dir, removeRetiredPiArchive: func(path string) error {
			if fail && path == archive {
				return blocked
			}
			return os.RemoveAll(path)
		}})
		t.Cleanup(r.Close)
		r.Start(nil)
		return r
	}
	start(false).Close()
	// A partial directory can still have an ended registration in the
	// dashboard but has no runner record that can identify its archive.
	if err := os.Remove(recordPath(session)); err != nil {
		t.Fatal(err)
	}
	first := start(true)
	if _, err := first.Get(id); !errors.Is(err, errUnknown) {
		t.Fatalf("partial session loaded as an intact runner record: %v", err)
	}
	if err := first.PrepareWorktreeHistoryPurge(checkout, map[string]bool{id: true}); !errors.Is(err, blocked) {
		t.Fatalf("purge error = %v, want %v", err, blocked)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("archive should still exist after failed removal: %v", err)
	}
	first.Close()
	// A rollback can remove the live registry while the pending archive
	// persists. Startup must skip restoration in this state.
	if err := os.RemoveAll(session); err != nil {
		t.Fatal(err)
	}
	second := start(false)
	if _, err := second.Transcript(id); !errors.Is(err, errUnknown) {
		t.Fatalf("purged history reappeared at startup: %v", err)
	}
	if err := second.PrepareWorktreeHistoryPurge(checkout, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archive); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retried archive still exists: %v", err)
	}
	second.CompleteWorktreeHistoryPurge(checkout)
	second.Close()
	third := start(false)
	if len(third.piPurges) != 0 {
		t.Fatalf("completed deletion left pending state: %v", third.piPurges)
	}
	if _, err := third.Transcript(id); !errors.Is(err, errUnknown) {
		t.Fatalf("purged history reappeared after completion: %v", err)
	}
}

func TestRetiredPiPurgeDoesNotRearchiveWhileCheckoutRemovalIsPending(t *testing.T) {
	const id = "pihistorypending"
	dir, checkout := t.TempDir(), t.TempDir()
	session := wsdashboard.SessionDir(dir, id)
	archive := filepath.Join(dir, retiredPiHistoryDir, id)
	ended := map[string]bool{id: true}
	rec := Record{ReplyID: id, Backend: BackendPi, Lead: true, Root: checkout, WSKey: "fixture", ClosedAt: "2026-01-01T00:00:00Z"}
	if err := writeRecord(session, rec); err != nil {
		t.Fatal(err)
	}
	start := func() *Runner {
		r := NewRunner(Options{Dir: dir})
		t.Cleanup(r.Close)
		r.Start(nil)
		return r
	}
	first := start()
	if err := first.PrepareWorktreeHistoryPurge(checkout, ended); err != nil {
		t.Fatal(err)
	}
	// Git failed after archive removal, so the checkout and live registry
	// stay available for a retry across the daemon restart.
	first.Close()
	second := start()
	if _, err := os.Stat(archive); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successor rearchived the history pending checkout removal: %v", err)
	}
	if state, err := second.Get(id); err != nil || state.Status != StatusClosed {
		t.Fatalf("history before retry = %+v, %v", state, err)
	}
	if err := second.PrepareWorktreeHistoryPurge(checkout, ended); err != nil {
		t.Fatal(err)
	}
	second.ForgetClosed(id)
	if err := os.RemoveAll(session); err != nil {
		t.Fatal(err)
	}
	second.CompleteWorktreeHistoryPurge(checkout)
	if len(second.piPurges) != 0 {
		t.Fatalf("completed deletion left pending state: %v", second.piPurges)
	}
}

// The v0.51.2 registry walks sessions/ and removes an expired retired row
// unless its handed-off Pi conversation lives there. Apply those branches
// to the fixture, including the directory-wide deletion (registry.go
// lines 386–425, 461–472 at the tag).
func pruneRetiredPiLeadLike0512(t *testing.T, dir, id string, now time.Time) {
	t.Helper()
	sessionDir := wsdashboard.SessionDir(dir, id)
	rec, err := readRecord(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	retired := (rec.Lead || rec.Parent != nil) && (rec.ClosedAt != "" || rec.handedOff())
	terminalFile := rec.Lead && rec.Backend == BackendPi && rec.HandedOffAt != "" &&
		(rec.SessionFile == "" || strings.HasPrefix(filepath.Clean(rec.SessionFile), filepath.Clean(sessionDir)+string(filepath.Separator)))
	reg, err := wsdashboard.ReadRegistration(dir, id)
	if err != nil || !retired || terminalFile || now.Sub(reg.LastSeen()) <= wsdashboard.RegistryRetention {
		t.Fatalf("fixture would escape v0.51.2 pruning: %+v, %v", reg, err)
	}
	wslive.DropRing(wslive.RingDir(sessionDir))
	if err := os.RemoveAll(sessionDir); err != nil {
		t.Fatal(err)
	}
}

func TestRetiredPiHoldersKeepRunning(t *testing.T) {
	dir := t.TempDir()
	holders := newFakeHolders()
	const session = "legacypihistory"
	recordHolder := holders.add(holderSession{Kind: holderKindLead, ReplyID: session, Label: "custom-profile"})
	orphan := holders.add(holderSession{Kind: holderKindPiWorker, ReplyID: "legacy-orphan"})
	customOrphan := holders.add(holderSession{Kind: holderKindLead, ReplyID: "lead-orphan", Label: "custom-profile"})
	knownBackend := holders.add(holderSession{Kind: holderKindAgentWorker, Backend: BackendPi, ReplyID: "other-orphan"})
	ordinaryOrphan := holders.add(holderSession{Kind: holderKindAgentWorker, Backend: BackendClaude, ReplyID: "claude-orphan"})
	sessionDir := wsdashboard.SessionDir(dir, session)
	rec := Record{ReplyID: session, Root: dir, WSKey: "fixture", Profile: "custom-profile", Backend: BackendPi, HolderID: recordHolder, Lead: true, TurnOpen: true}
	if err := writeRecord(sessionDir, rec); err != nil {
		t.Fatal(err)
	}
	if err := wslive.AppendRing(wslive.RingDir(sessionDir), []wslive.RingEntry{{Kind: "reply", Text: "retained Pi output"}}); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(Options{Dir: dir, Holders: holders})
	t.Cleanup(r.Close)
	r.Start(nil)
	<-r.rejoined

	st, err := r.Get(session)
	if err != nil || st.Status != StatusClosed || st.Backend != BackendPi || st.ClosedAt == "" || st.ResumeCommand != "" {
		t.Fatalf("retired conversation = %+v, %v", st, err)
	}
	retained, err := readRecord(sessionDir)
	if err != nil || retained.Backend != BackendPi || retained.HolderID != recordHolder || retained.TurnOpen {
		t.Fatalf("retained record = %+v, %v", retained, err)
	}
	history, err := r.Transcript(session)
	if err != nil || len(history) != 1 || history[0].Text != "retained Pi output" {
		t.Fatalf("retained transcript = %+v, %v", history, err)
	}
	for name, run := range map[string]func() (State, error){
		"reopen":  func() (State, error) { return r.Reopen(session) },
		"wake":    func() (State, error) { return r.Send(session, "wake") },
		"handoff": func() (State, error) { return r.HandOff(session) },
	} {
		if _, err := run(); !errors.Is(err, errPiRetired) {
			t.Errorf("%s = %v, want retirement refusal", name, err)
		}
	}
	for _, id := range []string{recordHolder, orphan, customOrphan, knownBackend} {
		status, err := holders.Status(id)
		if err != nil || status.Child.State != "running" {
			t.Errorf("Pi holder %s = %+v, %v", id, status, err)
		}
	}
	if _, err := holders.Status(ordinaryOrphan); !errors.Is(err, wsholder.ErrDetached) {
		t.Errorf("ordinary orphan holder was not swept: %v", err)
	}
}

func TestRetiredPiRealHolderSurvivesStartup(t *testing.T) {
	for _, tt := range []struct {
		name string
		meta holderSession
	}{
		{"worker", holderSession{Kind: holderKindPiWorker, ReplyID: "legacy-orphan"}},
		{"custom lead", holderSession{Kind: holderKindLead, ReplyID: "lead-orphan", Label: "custom-profile"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			python := lookPython(t)
			manager := wsholder.New(wsholder.Options{Dir: filepath.Join(dir, wsholder.DirName), Python: python})
			t.Cleanup(manager.Close)
			host := ManagerHost{manager}
			meta, err := json.Marshal(tt.meta)
			if err != nil {
				t.Fatal(err)
			}
			id, err := manager.Launch(meta)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Stop(id) })
			child, err := host.Spawn(id, wsholder.SpawnSpec{Mode: "pipe", Argv: []string{python, "-c", "import sys; sys.stdin.read()"}, Cwd: dir})
			if err != nil {
				t.Fatal(err)
			}

			r := NewRunner(Options{Dir: dir, Holders: host})
			t.Cleanup(r.Close)
			r.Start(nil)
			<-r.rejoined

			status, err := host.Status(id)
			if err != nil || status.Child.State != "running" || status.Child.PID != child.PID {
				t.Fatalf("legacy holder child = %+v, %v", status.Child, err)
			}
		})
	}
}

func TestRetiredPiHolderBlocksWorktreeUntilExit(t *testing.T) {
	dir, checkout := t.TempDir(), t.TempDir()
	holders := newFakeHolders()
	const session = "boundpihistory"
	holder := holders.add(holderSession{Kind: holderKindPiWorker, ReplyID: session})
	rec := Record{
		ReplyID: session, Backend: BackendPi, HolderID: holder,
		Root: t.TempDir(), Cwd: checkout, WSKey: "fixture", Profile: "custom-profile", Parent: &leadParent,
	}
	if err := writeRecord(wsdashboard.SessionDir(dir, session), rec); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(Options{Dir: dir, Holders: holders})
	t.Cleanup(r.Close)
	r.Start(nil)
	wait(t, "holder scan", r.rejoined)
	if st, err := r.Get(session); err != nil || st.Status != StatusClosed {
		t.Fatalf("retired history = %+v, %v", st, err)
	}
	bound := r.ActiveChildrenInWorktree(checkout)
	if len(bound) != 1 || bound[0].Address != AgentAddress(session) ||
		!bound[0].Live || bound[0].State == AgentClosed || bound[0].BoundBeforeRelease {
		t.Fatalf("release and reuse guard inputs for the live holder = %+v", bound)
	}
	holders.exit(holder, 0)
	if bound := r.ActiveChildrenInWorktree(checkout); len(bound) != 0 {
		t.Fatalf("exited holder still occupies the worktree: %+v", bound)
	}
}
