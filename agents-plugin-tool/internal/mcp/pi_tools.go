package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wslive"
)

var legacyPiToolNames = []string{"pi.spawn", "pi.send", "pi.stop", "pi.wait", "pi.list", "pi.transcript"}

func isPiTool(name string) bool {
	return slices.Contains(legacyPiToolNames, name)
}

func (s *Server) handlePiTool(ctx context.Context, id json.RawMessage, tool string, args map[string]any) response {
	if tool != "pi.list" && tool != "pi.transcript" {
		return toolTextResponse(id, "", fmt.Errorf("%s: the Pi harness is retired; use agent.* for worker operations", tool))
	}
	if s.workerForwards() {
		key, err := stringArg(tool, "session_key", args)
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		if _, found := s.lookupSession(key); !found {
			return toolTextResponse(id, "", fmt.Errorf("%s: unknown_session: session key not found", tool))
		}
		return s.forwardPiHistory(ctx, id, tool, args)
	}
	key, err := s.requireLeadSessionKey(tool, args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	if s.agents == nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: Pi session history requires a connection to the dashboard daemon", tool))
	}

	parent := wsagent.ParentRef{Kind: wsagent.ParentLead, ReplyID: s.mailboxReplyID(key)}
	if tool == "pi.list" {
		return piListResponse(id, args, s.agents.Children(parent))
	}
	child, err := stringArg(tool, "id", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	st, err := s.agents.Lineage(child)
	if err != nil || st.Backend != wsagent.BackendPi || st.Parent == nil || *st.Parent != parent {
		return toolTextResponse(id, "", fmt.Errorf("%s: %s is not one of this session's Pi subagents (pi.list lists them)", tool, child))
	}
	records, err := s.agents.Transcript(child)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	return piTranscriptResponse(id, args, child, records)
}

func (s *Server) forwardPiHistory(ctx context.Context, id json.RawMessage, tool string, args map[string]any) response {
	op := strings.TrimPrefix(tool, "pi.")
	if op == "list" {
		var answer struct {
			Conversations []wsagent.State `json:"conversations"`
		}
		if err := s.workerCall(ctx, op, map[string]any{}, workerForwardTimeout, &answer); err != nil {
			return agentFailure(id, tool, err)
		}
		return piListResponse(id, args, answer.Conversations)
	}
	child, err := stringArg(tool, "id", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	var answer struct {
		Records []wslive.RingRecord `json:"records"`
	}
	if err := s.workerCall(ctx, op, map[string]any{"id": child}, workerForwardTimeout, &answer); err != nil {
		return agentFailure(id, tool, err)
	}
	return piTranscriptResponse(id, args, child, answer.Records)
}

func piListResponse(id json.RawMessage, args map[string]any, states []wsagent.State) response {
	children := make([]wsagent.State, 0, len(states))
	for _, st := range states {
		if st.Backend == wsagent.BackendPi {
			children = append(children, st)
		}
	}
	if wantsJSON(args) {
		out := make([]piChildOut, 0, len(children))
		for _, st := range children {
			out = append(out, piChild(st))
		}
		return toolJSONResponse(id, out, nil)
	}
	return toolTextResponse(id, formatPiList(children), nil)
}

func piTranscriptResponse(id json.RawMessage, args map[string]any, child string, records []wslive.RingRecord) response {
	limit := intFromArgument(args["limit"], 40)
	if len(records) > limit {
		records = records[len(records)-limit:]
	}
	if wantsJSON(args) {
		return toolJSONResponse(id, piTranscriptJSON(records), nil)
	}
	return toolTextResponse(id, formatPiTranscript(child, records), nil)
}

type piChildOut struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Label   string `json:"label,omitempty"`
	Tier    string `json:"tier,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Cwd     string `json:"cwd"`
	Held    int    `json:"held,omitempty"`
	Error   string `json:"error,omitempty"`
	Created string `json:"created_at,omitempty"`
	Closed  string `json:"closed_at,omitempty"`
}

func piChild(st wsagent.State) piChildOut {
	return piChildOut{ID: st.ID, Status: wsagent.StatusClosed, Label: st.Label, Tier: st.Tier, Model: st.Model, Effort: st.Effort, Cwd: piCwd(st), Held: st.Held, Error: st.Error, Created: st.CreatedAt, Closed: st.ClosedAt}
}

// piCwd is the directory a child runs in.
func piCwd(st wsagent.State) string {
	if st.Cwd != "" {
		return st.Cwd
	}
	return st.Root
}

func formatPiList(children []wsagent.State) string {
	if len(children) == 0 {
		return "no Pi subagents\n"
	}
	var b strings.Builder
	for _, st := range children {
		fmt.Fprintf(&b, "%s  %s  %s/%s  %s", st.ID, wsagent.StatusClosed, orDash(st.Model), orDash(st.Effort), piCwd(st))
		if st.Label != "" {
			fmt.Fprintf(&b, "  %q", st.Label)
		}
		if st.Held > 0 {
			fmt.Fprintf(&b, "  held %d", st.Held)
		}
		if st.Error != "" {
			fmt.Fprintf(&b, "  error: %s", oneLine(st.Error))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// piTranscriptEntry is one transcript line in JSON.
type piTranscriptEntry struct {
	At    string `json:"at"`
	Kind  string `json:"kind"`
	Text  string `json:"text,omitempty"`
	Tools string `json:"tools,omitempty"`
}

func piTranscriptJSON(records []wslive.RingRecord) []piTranscriptEntry {
	out := make([]piTranscriptEntry, 0, len(records))
	for _, r := range records {
		out = append(out, piTranscriptEntry{At: r.At.UTC().Format(time.RFC3339), Kind: r.Kind, Text: r.Text, Tools: piToolsLine(r)})
	}
	return out
}

func formatPiTranscript(id string, records []wslive.RingRecord) string {
	if len(records) == 0 {
		return fmt.Sprintf("Pi subagent %s: no transcript yet\n", id)
	}
	var b strings.Builder
	for _, r := range records {
		stamp := r.At.UTC().Format(time.RFC3339)
		switch {
		case r.Kind == wslive.RingUser || r.Kind == wslive.RingReply:
			fmt.Fprintf(&b, "[%s] %s:\n%s\n\n", stamp, r.Kind, r.Text)
		case piToolsLine(r) != "":
			fmt.Fprintf(&b, "[%s] tools: %s\n\n", stamp, piToolsLine(r))
		case r.Kind == wslive.RingTurn:
			fmt.Fprintf(&b, "[%s] turn ended (%d tool calls, %d failed, %.0fs)\n\n", stamp, r.Done+r.Failed, r.Failed, float64(r.DurationMs)/1000)
		case r.Text != "":
			fmt.Fprintf(&b, "[%s] %s: %s\n\n", stamp, r.Kind, r.Text)
		}
	}
	return b.String()
}

// piToolsLine summarizes a tools entry's calls by tool, "" for another kind.
func piToolsLine(r wslive.RingRecord) string {
	if r.Kind != wslive.RingTools || r.Tools == nil {
		return ""
	}
	parts := make([]string, 0, len(r.Tools.Tools)+1)
	for _, t := range r.Tools.Tools {
		parts = append(parts, fmt.Sprintf("%s x%d", t.Name, t.Calls))
	}
	if r.Tools.OtherCalls > 0 {
		parts = append(parts, fmt.Sprintf("other x%d", r.Tools.OtherCalls))
	}
	line := fmt.Sprintf("%d calls", r.Tools.Calls)
	if r.Tools.Failed > 0 {
		line += fmt.Sprintf(", %d failed", r.Tools.Failed)
	}
	if len(parts) > 0 {
		line += " (" + strings.Join(parts, ", ") + ")"
	}
	return line
}
