package wsagent

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

// codexAudit maps the Codex app-server stream to audit envelopes
// (Decision 1's second adapter): tool items (a call when the item starts, a
// result when it completes, both keyed by the item id as call_id),
// reasoning items as thinking, hooks, sub-agent activity, the
// contextCompaction item's completion as the compaction boundary (the
// model-seen cut; recorded for 0.160.0 in codex-0.160.0-hosted-lead.jsonl,
// unverified for 0.170.0, whose probes never start a thread), server requests and
// responses, and every other notification as a system event. Streaming
// deltas (agent message text, command output) are recognized and dropped:
// the completed item carries the whole body.
type codexAudit struct {
	session string
	// started are the tool items whose call was captured, so a completed
	// item never repeats it.
	started map[string]bool
}

func newCodexAudit(session string) *codexAudit {
	return &codexAudit{session: session, started: map[string]bool{}}
}

// codexAuditItem is the part of an app-server item the adapter reads.
type codexAuditItem struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	Status           string          `json:"status"`
	Command          string          `json:"command"`
	Cwd              string          `json:"cwd"`
	Server           string          `json:"server"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	Result           json.RawMessage `json:"result"`
	Error            json.RawMessage `json:"error"`
	ExitCode         *int            `json:"exitCode"`
	AggregatedOutput *string         `json:"aggregatedOutput"`
	Changes          json.RawMessage `json:"changes"`
	Query            string          `json:"query"`
	Path             string          `json:"path"`
	Prompt           *string         `json:"prompt"`
	Kind             string          `json:"kind"`
	AgentThreadID    string          `json:"agentThreadId"`
	AgentPath        string          `json:"agentPath"`
	Summary          json.RawMessage `json:"summary"`
	Content          json.RawMessage `json:"content"`
}

var codexToolItems = map[string]bool{
	"commandExecution": true, "mcpToolCall": true, "fileChange": true, "webSearch": true,
	"imageView": true, "collabAgentToolCall": true, "dynamicToolCall": true,
}

func (a *codexAudit) env(kind wsaudit.Kind, at time.Time) wsaudit.Envelope {
	ev, _ := wsaudit.NewEnvelope(a.session, BackendCodex, at, kind)
	return ev
}

func (a *codexAudit) mapLine(line []byte, at time.Time) (bool, []wsaudit.Envelope, []wsaudit.Envelope) {
	recognized, evs := a.mapOwn(line, at)
	return recognized, nil, evs
}

func (a *codexAudit) mapOwn(line []byte, at time.Time) (bool, []wsaudit.Envelope) {
	var msg codexWire
	if json.Unmarshal(line, &msg) != nil {
		return false, nil
	}
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case msg.Method != "" && hasID:
		ev := a.env(wsaudit.KindSystem, at)
		ev.Subkind = "request:" + msg.Method
		var p struct {
			ItemID string `json:"itemId"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			ev.CallID = wsaudit.NormalizeID(p.ItemID)
		}
		ev.Out = compactJSON(line)
		return true, []wsaudit.Envelope{ev}
	case msg.Method == "" && hasID:
		ev := a.env(wsaudit.KindSystem, at)
		ev.Subkind = "response"
		if msg.Error != nil {
			ev.Subkind = "response:error"
		}
		ev.Out = compactJSON(line)
		return true, []wsaudit.Envelope{ev}
	case msg.Method == "":
		return false, nil
	}
	var p struct {
		Item codexAuditItem `json:"item"`
		Run  struct {
			ID        string `json:"id"`
			EventName string `json:"eventName"`
			Status    string `json:"status"`
		} `json:"run"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	switch msg.Method {
	case "item/started", "item/completed":
		return true, a.item(p.Item, msg.Method == "item/completed", at)
	case "item/agentMessage/delta", "item/commandExecution/outputDelta", "item/reasoning/summaryTextDelta",
		"item/reasoning/textDelta", "item/fileChange/outputDelta":
		return true, nil
	case "hook/started", "hook/completed":
		ev := a.env(wsaudit.KindHook, at)
		ev.Subkind = p.Run.EventName
		setMeta(&ev, "phase", strings.TrimPrefix(msg.Method, "hook/"))
		setMeta(&ev, "hook_id", p.Run.ID)
		if p.Run.Status != "" {
			setMeta(&ev, "status", p.Run.Status)
		}
		ev.Out = compactJSON(msg.Params)
		return true, []wsaudit.Envelope{ev}
	}
	ev := a.env(wsaudit.KindSystem, at)
	ev.Subkind = msg.Method
	ev.Out = compactJSON(line)
	return true, []wsaudit.Envelope{ev}
}

func (a *codexAudit) item(it codexAuditItem, done bool, at time.Time) []wsaudit.Envelope {
	switch {
	case codexToolItems[it.Type]:
		var out []wsaudit.Envelope
		if !a.started[it.ID] {
			a.started[it.ID] = true
			call := a.env(wsaudit.KindToolCall, at)
			call.Subkind, call.CallID, call.In = codexToolName(it), wsaudit.NormalizeID(it.ID), codexToolInput(it)
			out = append(out, call)
		}
		if done {
			delete(a.started, it.ID)
			res := a.env(wsaudit.KindToolResult, at)
			res.Subkind, res.CallID, res.Out = codexToolName(it), wsaudit.NormalizeID(it.ID), codexToolOutput(it)
			if it.Status != "" {
				setMeta(&res, "status", it.Status)
			}
			if it.ExitCode != nil {
				setMeta(&res, "exit_code", *it.ExitCode)
			}
			if codexPresent(it.Error) {
				setMeta(&res, "error", compactJSON(it.Error))
			}
			out = append(out, res)
		}
		return out
	case it.Type == "reasoning":
		if !done {
			return nil
		}
		ev := a.env(wsaudit.KindThinking, at)
		ev.Out = codexReasoningText(it)
		if ev.Out == "" {
			// Codex sends an encrypted reasoning item with no readable text.
			setMeta(&ev, wsaudit.MetaSignatureOnly, true)
		}
		return []wsaudit.Envelope{ev}
	case it.Type == "contextCompaction":
		if !done {
			return nil
		}
		return []wsaudit.Envelope{a.env(wsaudit.KindCompaction, at)}
	case it.Type == "subAgentActivity":
		ev := a.env(wsaudit.KindSubagent, at)
		ev.Subkind, ev.AgentID, ev.ParentID = it.Kind, it.AgentThreadID, wsaudit.NormalizeID(it.ID)
		ev.Out = it.AgentPath
		if !done {
			return nil
		}
		return []wsaudit.Envelope{ev}
	case it.Type == "userMessage":
		if !done {
			return nil
		}
		ev := a.env(wsaudit.KindUser, at)
		ev.Out = codexContentText(it.Content)
		if isReminder(ev.Out) {
			ev.Kind = wsaudit.KindSystemReminder
		}
		return []wsaudit.Envelope{ev}
	case it.Type == "agentMessage":
		if !done {
			return nil
		}
		ev := a.env(wsaudit.KindAssistantText, at)
		ev.Out = it.Text
		return []wsaudit.Envelope{ev}
	}
	if !done {
		return nil
	}
	ev := a.env(wsaudit.KindSystem, at)
	ev.Subkind = "item:" + it.Type
	raw, _ := json.Marshal(it)
	ev.Out = string(raw)
	return []wsaudit.Envelope{ev}
}

func codexToolName(it codexAuditItem) string {
	switch it.Type {
	case "mcpToolCall":
		return it.Server + "/" + it.Tool
	case "collabAgentToolCall", "dynamicToolCall":
		if it.Tool != "" {
			return it.Type + "/" + it.Tool
		}
	}
	return it.Type
}

func codexToolInput(it codexAuditItem) string {
	switch it.Type {
	case "commandExecution":
		raw, _ := json.Marshal(map[string]string{"command": it.Command, "cwd": it.Cwd})
		return string(raw)
	case "mcpToolCall", "dynamicToolCall":
		return compactJSON(it.Arguments)
	case "fileChange":
		return compactJSON(it.Changes)
	case "webSearch":
		return it.Query
	case "imageView":
		return it.Path
	case "collabAgentToolCall":
		if it.Prompt != nil {
			return *it.Prompt
		}
	}
	return ""
}

func codexToolOutput(it codexAuditItem) string {
	switch {
	case it.AggregatedOutput != nil:
		return *it.AggregatedOutput
	case codexPresent(it.Result):
		var r struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(it.Result, &r) == nil && len(r.Content) > 0 {
			var texts []string
			for _, c := range r.Content {
				if c.Type == "text" {
					texts = append(texts, c.Text)
				}
			}
			if len(texts) > 0 {
				return strings.Join(texts, "\n")
			}
		}
		return compactJSON(it.Result)
	case codexPresent(it.Error):
		return compactJSON(it.Error)
	}
	return ""
}

// codexReasoningText joins a reasoning item's readable summary and content
// parts (strings, or objects with a text field).
func codexReasoningText(it codexAuditItem) string {
	var parts []string
	for _, raw := range []json.RawMessage{it.Summary, it.Content} {
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) != nil {
			continue
		}
		for _, item := range list {
			var s string
			if json.Unmarshal(item, &s) == nil {
				parts = append(parts, s)
				continue
			}
			var obj struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(item, &obj) == nil && obj.Text != "" {
				parts = append(parts, obj.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func codexContentText(raw json.RawMessage) string {
	var list []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return ""
	}
	var parts []string
	for _, c := range list {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (a *codexAudit) markLoss(uint64, string) bool { return false }

func (a *codexAudit) flush(time.Time) []wsaudit.Envelope { return nil }
