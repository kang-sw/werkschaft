package wsdashboard

import (
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestAgentRowBackgroundStatus(t *testing.T) {
	st := wslive.NewState("lead-key")
	if _, err := st.Apply(wslive.Event{
		Kind:            wslive.KindAgent,
		AgentID:         "pi:bgkid",
		Harness:         "agy",
		AgentKind:       wslive.AgentKindPi,
		Label:           "agy-worker",
		Status:          wslive.StatusDone,
		BackgroundTasks: 1,
	}, 1000); err != nil {
		t.Fatal(err)
	}
	snap := st.Snapshot(1000)
	if len(snap.Agents) != 1 || snap.Agents[0].BackgroundTasks != 1 || snap.Agents[0].Status != wslive.StatusDone {
		t.Fatalf("snapshot agent = %+v", snap.Agents)
	}
	node := agentTree(snap.Agents, sessionLookup{})[0]
	if node.EndedStatus() != "background" {
		t.Fatalf("EndedStatus = %q, want background", node.EndedStatus())
	}
	html := renderNamed(t, "agent", node)
	if !strings.Contains(html, `class="chip astatus">background</span>`) {
		t.Fatalf("rendered agent row lacks background chip: %s", html)
	}
	// Clearing BackgroundTasks returns the row to sleep.
	if _, err := st.Apply(wslive.Event{
		Kind:            wslive.KindAgent,
		AgentID:         "pi:bgkid",
		Harness:         "agy",
		AgentKind:       wslive.AgentKindPi,
		Status:          wslive.StatusDone,
		BackgroundTasks: 0,
	}, 2000); err != nil {
		t.Fatal(err)
	}
	node = agentTree(st.Snapshot(2000).Agents, sessionLookup{})[0]
	if node.EndedStatus() != "sleep" {
		t.Fatalf("cleared EndedStatus = %q, want sleep", node.EndedStatus())
	}
}

func TestRetiredPiRowOpensAsEndedHistory(t *testing.T) {
	row := wslive.Agent{ID: "pi:child", ReplyID: "child", AgentKind: wslive.AgentKindPi, Status: wslive.StatusRunning, BackgroundTasks: 1}
	lookup := sessionLookup{session: func(id string) (Session, bool) {
		return Session{Registration: Registration{Agent: true}}, id == row.ReplyID
	}}
	node := agentTree([]wslive.Agent{row}, lookup)[0]
	if node.IsRunning() || node.EndedStatus() != "done" || !node.Linked {
		t.Fatalf("retired Pi row = %+v", node)
	}
	if row.Status != wslive.StatusRunning {
		t.Fatal("history was mutated")
	}
}
