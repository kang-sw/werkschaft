package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
)

func childListFixture(total, running int) []wsagent.AgentChild {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	children := make([]wsagent.AgentChild, total)
	for i := range children {
		state := wsagent.AgentWaiting
		if i < running {
			state = wsagent.AgentRunning
		} else if i%2 == 0 {
			state = wsagent.AgentClosed
		}
		children[i] = wsagent.AgentChild{
			Address: fmt.Sprintf("id:child-%03d", i), Label: fmt.Sprintf("worker %03d", i),
			State: state, Tier: "pi.small", Authority: wsagent.PermissionReadOnly,
			Model: "m", Effort: "low", Cwd: "/workspace",
			CreatedAt:  base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			LastReport: "done: " + strings.Repeat("reported progress on the task. ", 5),
		}
	}
	return children
}

func TestHostedRestoreBundleCapsInactiveChildren(t *testing.T) {
	children := childListFixture(43, 3)
	// An older waiting child recently settled a turn, so it outranks newer records.
	children[3].Activity = &wsagent.AgentChildActivity{LastActiveAt: "2026-01-02T00:00:00Z"}
	got := renderHostedRestoreBundle("session-key", "compaction", sessionRecord{}, children)
	_, section, _ := strings.Cut(got, "## Direct Children\n")
	lines := strings.Split(strings.TrimSpace(section), "\n")
	if len(lines) != 14 || strings.Count(section, "; tier=pi.small; authority=read-only; last=done:") != 3 {
		t.Fatalf("want 3 full rows, 10 names and 1 summary, got %q", section)
	}
	if lines[3] != "- id:child-003 (worker 003)" || lines[4] != "- id:child-042 (worker 042)" || lines[12] != "- id:child-034 (worker 034)" {
		t.Fatalf("recent child order or format = %q", lines[3:13])
	}
	if lines[13] != "More children (15 closed, 15 waiting). "+fullAgentListHint {
		t.Fatalf("remainder = %q", lines[13])
	}
}

func TestAgentListCompactAndFull(t *testing.T) {
	agents := newFakePiAgents()
	agents.agent.children = childListFixture(18, 1)
	agents.agent.children[1].Activity = &wsagent.AgentChildActivity{AwaitingReport: true}
	agents.agent.children[2].Error = "backend failure"
	agents.agent.children[3].State = wsagent.AgentBackground
	agents.agent.children[4].State = wsagent.AgentStarting
	agents.agent.children[5].State = wsagent.AgentIdle
	agents.agent.children[5].Error = "daemon exited"
	agents.agent.children[5].LastReport = "interrupted: daemon exited"
	agents.agent.children[5].Activity = &wsagent.AgentChildActivity{SettledInterruptError: true, LastActiveAt: "2026-01-02T00:00:00Z"}
	agents.agent.children[6].CreatedAt = ""
	f := agentLeadFixture(t, wsManifests(), agents)
	call := func(args map[string]any) string {
		return callToolWithKey(t, f.server, 2, f.key, "agent.list", args)
	}
	defaultText := call(nil)
	for _, want := range []string{
		"id:child-000  running  pi.small", "id:child-001  waiting  pi.small", "id:child-002  closed  pi.small",
		"id:child-003  background  pi.small", "id:child-004  starting  pi.small",
		"id:child-005 (worker 005)\nid:child-017 (worker 017)", "id:child-009 (worker 009)",
		"More children (2 closed, 1 waiting). Activity time unavailable (1 inactive). " + fullAgentListHint,
	} {
		if !strings.Contains(defaultText, want) {
			t.Errorf("default text lacks %q:\n%s", want, defaultText)
		}
	}
	if strings.Contains(defaultText, "id:child-006") || strings.Contains(defaultText, "daemon exited") || strings.Count(defaultText, "last report:") != 5 {
		t.Errorf("default text contains inactive details or omitted names:\n%s", defaultText)
	}
	if full := call(map[string]any{"full": true}); strings.Count(full, "last report:") != 18 || !strings.Contains(full, "id:child-006  closed  pi.small") {
		t.Errorf("full text = %s", full)
	}
	var legacy []wsagent.AgentChild
	legacyText := call(map[string]any{"format": "json"})
	if err := json.Unmarshal([]byte(legacyText), &legacy); err != nil || len(legacy) != 18 || legacy[1].Activity != nil || legacy[17].LastReport == "" {
		t.Errorf("legacy JSON = %s (%v)", legacyText, err)
	}
	if fullJSON := call(map[string]any{"format": "json", "full": true}); fullJSON != legacyText {
		t.Errorf("full JSON differs from legacy JSON: %s", fullJSON)
	}
	var compact compactAgentList
	compactText := call(map[string]any{"format": "json", "full": false})
	if err := json.Unmarshal([]byte(compactText), &compact); err != nil || len(compact.Active) != 5 || len(compact.RecentInactive) != 10 ||
		compact.Active[0] != legacy[0] || compact.RecentInactive[0].Address != "id:child-005" ||
		compact.OmittedByState[wsagent.AgentClosed] != 2 || compact.OmittedByState[wsagent.AgentWaiting] != 1 || compact.UnknownActivity != 1 {
		t.Errorf("compact JSON = %s (%v)", compactText, err)
	}
}
