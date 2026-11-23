package wsagent

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wslive"
)

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
