package wsagent

import (
	"cmp"
	"encoding/json"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wslive"
)

var _ auditSource = (*agyMapper)(nil)

func (*agyMapper) newAudit(sessionID string) (string, auditAdapter) {
	return BackendAgy, &agyAudit{session: sessionID, openTools: make(map[string]string)}
}

// agyAudit maps ACP independently of the live mapper. The holder tees
// thought chunks to audit while sending only activity pulses to the live mapper.
type agyAudit struct {
	session   string
	openTools map[string]string
}

func (a *agyAudit) env(kind wsaudit.Kind, at time.Time) wsaudit.Envelope {
	ev, _ := wsaudit.NewEnvelope(a.session, BackendAgy, at, kind)
	return ev
}

func (a *agyAudit) mapLine(line []byte, at time.Time) (bool, []auditEvent, []auditEvent) {
	recognized, evs := a.mapOwn(line, at)
	return recognized, nil, evs
}

func (a *agyAudit) mapOwn(line []byte, at time.Time) (bool, []auditEvent) {
	var msg agyMessage
	if json.Unmarshal(line, &msg) != nil {
		return false, nil
	}
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case msg.Method == "session/update" && !hasID:
		return a.update(msg.Params, at)
	case msg.Method == "session/request_permission" && hasID:
		var p struct {
			ToolCall struct {
				ID string `json:"toolCallId"`
			} `json:"toolCall"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		ev := a.env(wsaudit.KindSystem, at)
		ev.Subkind, ev.CallID, ev.Out = "request:"+msg.Method, p.ToolCall.ID, compactJSON(msg.Params)
		return true, slotted([]wsaudit.Envelope{ev})
	case msg.Method == "" && hasID && (msg.Result != nil || msg.Error != nil):
		subkind := "response"
		if msg.Error != nil {
			subkind = "response:error"
		}
		return true, a.system(subkind, line, at)
	}
	return false, nil
}

type agyAuditUpdate struct {
	agyUpdate
	RawInput json.RawMessage `json:"rawInput"`
}

func (a *agyAudit) update(params json.RawMessage, at time.Time) (bool, []auditEvent) {
	var p struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return false, nil
	}
	var update struct {
		Type    string          `json:"sessionUpdate"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(p.Update, &update) != nil {
		return false, nil
	}
	switch update.Type {
	case "user_message_chunk", "agent_message_chunk", "ws_activity":
		return true, nil
	case "agent_thought_chunk":
		var c struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(update.Content, &c) != nil || c.Type != "text" || c.Text == "" {
			return true, nil
		}
		ev := a.env(wsaudit.KindThinking, at)
		ev.Out = c.Text
		return true, slotted([]wsaudit.Envelope{ev})
	case "tool_call", "tool_call_update":
		var u agyAuditUpdate
		if json.Unmarshal(p.Update, &u) != nil {
			return false, nil
		}
		return true, a.tool(u, at)
	case "plan", "usage_update", "available_commands_update", "current_mode_update",
		"config_option_update", "session_info_update":
		return true, a.system(update.Type, p.Update, at)
	}
	return false, nil
}

func (a *agyAudit) system(subkind string, body []byte, at time.Time) []auditEvent {
	ev := a.env(wsaudit.KindSystem, at)
	ev.Subkind, ev.Out = subkind, compactJSON(body)
	return slotted([]wsaudit.Envelope{ev})
}

func (a *agyAudit) tool(u agyAuditUpdate, at time.Time) []auditEvent {
	done := u.Status == "completed" || u.Status == "failed"
	if u.SessionUpdate == "tool_call_update" && !done {
		return nil
	}

	id := u.ToolCallID
	name, started := a.openTools[id]
	name = cmp.Or(name, u.Name, u.Title, "tool")
	native := name == agyInvokeSubagentTool && id != ""
	var out []auditEvent
	if !started {
		call := a.env(wsaudit.KindToolCall, at)
		call.Subkind, call.CallID, call.In = name, id, agyAuditBody(u.Args, u.RawInput)
		out = append(out, auditEvent{key: agyAuditToolKey("call", id), ev: call})
		if native {
			start := a.nativeLaunch("launch_started", id, at)
			var args struct {
				Role     string `json:"Role"`
				TypeName string `json:"TypeName"`
			}
			_ = json.Unmarshal([]byte(call.In), &args)
			start.Out = cmp.Or(strings.TrimSpace(args.Role), strings.TrimSpace(args.TypeName), "subagent")
			out = append(out, auditEvent{key: agyAuditToolKey("native-launch", id), ev: start})
		}
	}

	if done {
		delete(a.openTools, id)
		result := a.env(wsaudit.KindToolResult, at)
		result.Subkind, result.CallID, result.Out = name, id, agyAuditBody(u.RawOutput, u.Content)
		setMeta(&result, "status", u.Status)
		// Reserve slot 0 for a call without an ID on this same record.
		out = append(out, auditEvent{slot: 1, ev: result})
		if native {
			// Completion of invoke_subagent acknowledges the launch. ACP
			// supplies neither a child session ID nor the child's outcome.
			end := a.nativeLaunch("launch_"+u.Status, id, at)
			out = append(out, auditEvent{slot: 2, ev: end})
		}
	} else if id != "" {
		a.openTools[id] = name
	}
	return out
}

// The same call or launch can come from a terminal update when its start
// wrapped out of the holder ring. Keep its identity across replays.
func agyAuditToolKey(prefix, id string) string {
	if id == "" {
		return ""
	}
	return prefix + "." + wsaudit.NormalizeID(id)
}

func (a *agyAudit) nativeLaunch(subkind, callID string, at time.Time) wsaudit.Envelope {
	ev := a.env(wsaudit.KindSubagent, at)
	ev.Subkind, ev.AgentID, ev.ParentID = subkind, wslive.NativeAgyPrefix+callID, callID
	return ev
}

func agyAuditBody(raw, fallback json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		raw = fallback
	}
	if string(raw) == "null" {
		return ""
	}
	return compactJSON(raw)
}

func (*agyAudit) markLoss(uint64, string) bool { return false }

func (*agyAudit) flush(time.Time) []auditEvent { return nil }
