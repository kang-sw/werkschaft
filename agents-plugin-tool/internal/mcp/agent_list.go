package mcp

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
)

const recentInactiveLimit = 10
const fullAgentListHint = "Use ws/agent.list with full=true for all details."

type agentName struct {
	Address string `json:"address"`
	Label   string `json:"label,omitempty"`
}

func (n agentName) String() string {
	if label := oneLine(n.Label); label != "" {
		return fmt.Sprintf("%s (%s)", n.Address, label)
	}
	return n.Address
}

type compactAgentList struct {
	Active          []wsagent.AgentChild `json:"active"`
	RecentInactive  []agentName          `json:"recent_inactive"`
	OmittedByState  map[string]int       `json:"omitted_by_state,omitempty"`
	UnknownActivity int                  `json:"unknown_activity,omitempty"`
	Hint            string               `json:"hint,omitempty"`
}

func summarizeAgentChildren(children []wsagent.AgentChild) compactAgentList {
	view := compactAgentList{Active: []wsagent.AgentChild{}, RecentInactive: []agentName{}}
	type inactiveChild struct {
		name     agentName
		state    string
		activity time.Time
	}
	var inactive []inactiveChild
	for _, c := range children {
		activity := time.Time{}
		awaitingReport, settledInterruptError := false, false
		if c.Activity != nil {
			awaitingReport = c.Activity.AwaitingReport
			settledInterruptError = c.Activity.SettledInterruptError
			activity, _ = time.Parse(time.RFC3339, c.Activity.LastActiveAt)
		}
		if c.State == wsagent.AgentRunning || c.State == wsagent.AgentStarting || c.State == wsagent.AgentBackground ||
			c.Held > 0 || awaitingReport || c.Error != "" && !settledInterruptError {
			c.Activity = nil
			view.Active = append(view.Active, c)
			continue
		}
		if activity.IsZero() {
			activity, _ = time.Parse(time.RFC3339, c.CreatedAt)
		}
		if activity.IsZero() {
			view.UnknownActivity++
		}
		inactive = append(inactive, inactiveChild{agentName{c.Address, c.Label}, c.State, activity})
	}
	sort.Slice(inactive, func(i, j int) bool {
		a, b := inactive[i], inactive[j]
		if a.activity.Equal(b.activity) {
			return a.name.Address < b.name.Address
		}
		return a.activity.After(b.activity)
	})
	limit := min(len(inactive), recentInactiveLimit)
	for _, c := range inactive[:limit] {
		view.RecentInactive = append(view.RecentInactive, c.name)
	}
	if limit < len(inactive) {
		view.OmittedByState = make(map[string]int)
		for _, c := range inactive[limit:] {
			view.OmittedByState[c.state]++
		}
		view.Hint = fullAgentListHint
	}
	return view
}

func (v compactAgentList) summary() string {
	var b strings.Builder
	if len(v.OmittedByState) > 0 {
		states := make([]string, 0, len(v.OmittedByState))
		for state := range v.OmittedByState {
			states = append(states, state)
		}
		slices.Sort(states)
		counts := make([]string, 0, len(states))
		for _, state := range states {
			counts = append(counts, fmt.Sprintf("%d %s", v.OmittedByState[state], state))
		}
		fmt.Fprintf(&b, "More children (%s). ", strings.Join(counts, ", "))
	}
	if v.UnknownActivity > 0 {
		fmt.Fprintf(&b, "Activity time unavailable (%d inactive). ", v.UnknownActivity)
	}
	b.WriteString(v.Hint)
	if b.Len() == 0 {
		return ""
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func agentListResponse(id json.RawMessage, children []wsagent.AgentChild, args map[string]any) response {
	full, selected := args["full"]
	includeAll, ok := full.(bool)
	if selected && !ok {
		return toolTextResponse(id, "", fmt.Errorf("agent.list: full must be a boolean"))
	}
	jsonOutput := wantsJSON(args)
	// Existing structured calls receive the same array unless they opt in.
	if jsonOutput && (!selected || includeAll) {
		public := make([]wsagent.AgentChild, len(children))
		for i, child := range children {
			child.Activity = nil
			public[i] = child
		}
		return toolJSONResponse(id, public, nil)
	}
	if includeAll || len(children) == 0 && !jsonOutput {
		return toolTextResponse(id, formatAgentList(children), nil)
	}
	view := summarizeAgentChildren(children)
	if jsonOutput {
		return toolJSONResponse(id, view, nil)
	}
	var b strings.Builder
	if len(view.Active) > 0 {
		b.WriteString(formatAgentList(view.Active))
	}
	for _, name := range view.RecentInactive {
		fmt.Fprintln(&b, name)
	}
	b.WriteString(view.summary())
	return toolTextResponse(id, b.String(), nil)
}
