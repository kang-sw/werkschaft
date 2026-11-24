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
			if mode == "current prune" {
				runner = start(pruneAt)
				if n := wsdashboard.PruneRegistrations(dir, pruneAt, wsdashboard.RegistryRetention); n != 0 {
					t.Fatalf("0.52 pruned %d retired Pi sessions", n)
				}
			} else {
				pruneRetiredPiLeadLike0512(t, dir, id, pruneAt)
				if _, err := os.Stat(sessionDir); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("0.51.2 prune left session directory: %v", err)
				}
				runner = start(pruneAt)
			}
			if history, err := runner.Transcript(id); err != nil || len(history) != 1 || history[0].Text != "retired output" {
				t.Fatalf("readback = %+v, %v", history, err)
			}
			if data, err := os.ReadFile(piFile); err != nil || string(data) != raw {
				t.Fatalf("Pi JSONL readback = %q, %v", data, err)
			}
			if reg, err := wsdashboard.ReadRegistration(dir, id); err != nil || reg.EndedAt == "" {
				t.Fatalf("recovered row = %+v, %v", reg, err)
			}
			runner.forget(id)
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
