package wsagent

import (
	"cmp"
	"encoding/json"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wslive"
)

// Bound the unredacted assembly even when every individual fence fits.
const agyThoughtCaptureBytes = 1 << 20

// newAudit is the agy dialect's audit adapter, which acpMapper.newAudit
// returns as the mapper's auditSource.
func (agyDialect) newAudit(sessionID string) (string, auditAdapter) {
	return BackendAgy, &agyAudit{session: sessionID, openTools: make(map[string]string)}
}

// agyAudit maps ACP independently of the live mapper. The holder tees
// thought chunks to audit while sending only activity pulses to the live mapper;
// contiguous thought chunks are assembled into one thinking event and flushed
// on any non-thought, non-activity line or at stream end.
type agyAudit struct {
	session    string
	openTools  map[string]string
	thought    *agyAuditThought
	prevTokens int64
	prevWindow int64
}

type agyAuditThought struct {
	at        time.Time
	body      []byte
	lost      uint64
	lossKnown bool
	reason    string
}

func (a *agyAudit) env(kind wsaudit.Kind, at time.Time) wsaudit.Envelope {
	ev, _ := wsaudit.NewEnvelope(a.session, BackendAgy, at, kind)
	return ev
}

func (a *agyAudit) mapLine(line []byte, at time.Time) (bool, []auditEvent, []auditEvent) {
	var msg acpMessage
	if json.Unmarshal(line, &msg) != nil {
		return false, a.finishThought(false), nil
	}
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case msg.Method == "session/update" && !hasID:
		return a.update(msg.Params, at)
	case msg.Method == "session/request_permission" && hasID:
		carried := a.finishThought(false)
		var p struct {
			ToolCall struct {
				ID string `json:"toolCallId"`
			} `json:"toolCall"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		ev := a.env(wsaudit.KindSystem, at)
		ev.Subkind, ev.CallID, ev.Out = "request:"+msg.Method, p.ToolCall.ID, compactJSON(msg.Params)
		return true, carried, slotted([]wsaudit.Envelope{ev})
	case msg.Method == "" && hasID && (msg.Result != nil || msg.Error != nil):
		carried := a.finishThought(false)
		subkind := "response"
		if msg.Error != nil {
			subkind = "response:error"
		}
		return true, carried, a.system(subkind, line, at)
	}
	return false, a.finishThought(false), nil
}

type agyAuditUpdate struct {
	acpSessionUpdate
	RawInput json.RawMessage `json:"rawInput"`
}

func (a *agyAudit) update(params json.RawMessage, at time.Time) (bool, []auditEvent, []auditEvent) {
	var p struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return false, a.finishThought(false), nil
	}
	var update struct {
		Type    string          `json:"sessionUpdate"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(p.Update, &update) != nil {
		return false, a.finishThought(false), nil
	}
	switch update.Type {
	case "ws_activity":
		// Holder pulse injected onto the kept stream while thought chunks are
		// tee'd; never break an open thought assembly on it.
		return true, nil, nil
	case "agent_thought_chunk":
		if a.thought != nil && a.thought.reason == wsaudit.LossCaptureCap {
			return true, nil, nil
		}
		var c struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(update.Content, &c) != nil || c.Type != "text" || c.Text == "" {
			return true, nil, nil
		}
		if a.thought == nil {
			a.thought = &agyAuditThought{at: at}
		}
		th := a.thought
		if len(c.Text) > agyThoughtCaptureBytes-len(th.body) {
			th.body, th.reason, th.lossKnown = nil, wsaudit.LossCaptureCap, false
			return true, nil, a.thoughtEvent(th)
		}
		needed := len(th.body) + len(c.Text)
		if needed > cap(th.body) {
			// Keep the retained allocation within the same budget as the text.
			body := make([]byte, len(th.body), min(agyThoughtCaptureBytes, max(needed, 2*cap(th.body))))
			copy(body, th.body)
			th.body = body
		}
		th.body = append(th.body, c.Text...)
		return true, nil, nil
	case "user_message_chunk", "agent_message_chunk":
		return true, a.finishThought(false), nil
	case "tool_call", "tool_call_update":
		carried := a.finishThought(false)
		var u agyAuditUpdate
		if json.Unmarshal(p.Update, &u) != nil {
			return false, carried, nil
		}
		return true, carried, a.tool(u, at)
	case "usage_update":
		carried := a.finishThought(false)
		evs := a.system(update.Type, p.Update, at)
		if cmpEv, ok := a.checkUsageDrop(p.Update, at); ok {
			evs = append(evs, auditEvent{slot: 1, inferred: true, ev: cmpEv})
		}
		return true, carried, evs
	case "plan", "available_commands_update", "current_mode_update",
		"config_option_update", "session_info_update":
		return true, a.finishThought(false), a.system(update.Type, p.Update, at)
	}
	return false, a.finishThought(false), nil
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
		call.Subkind, call.CallID, call.In = name, id, acpAuditBody(u.Args, u.RawInput)
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
		result.Subkind, result.CallID, result.Out = name, id, acpAuditBody(u.RawOutput, u.Content)
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

func (a *agyAudit) finishThought(ending bool) []auditEvent {
	th := a.thought
	if th == nil {
		return nil
	}
	a.thought = nil
	if th.reason == wsaudit.LossCaptureCap {
		return nil // the overflow already emitted this thought's marker
	}
	if ending && th.reason == "" {
		th.reason = wsaudit.LossStreamEnd
	}
	return a.thoughtEvent(th)
}

func (a *agyAudit) thoughtEvent(th *agyAuditThought) []auditEvent {
	ev := a.env(wsaudit.KindThinking, th.at)
	ev.Out = string(th.body)
	setMeta(&ev, wsaudit.MetaAssembled, true)
	if th.reason != "" {
		setMeta(&ev, wsaudit.MetaIncomplete, true)
		setMeta(&ev, wsaudit.MetaLossReason, th.reason)
		if th.lossKnown && th.lost > 0 {
			setMeta(&ev, wsaudit.MetaLost, th.lost)
		}
	}
	return []auditEvent{{slot: 0, ev: ev}}
}

func (a *agyAudit) markLoss(n uint64, reason string) bool {
	th := a.thought
	if th == nil {
		return false
	}
	if th.reason == wsaudit.LossCaptureCap {
		return true
	}
	if th.reason == "" {
		th.lossKnown = true
	}
	th.reason = reason
	if n == 0 {
		th.lossKnown = false
	}
	th.lost += n
	return true
}

func (a *agyAudit) flush(time.Time) []auditEvent {
	return a.finishThought(true)
}

// checkUsageDrop mirrors conv.checkUsageDropCompaction using AgyCatalog's
// thresholds so an automatic usage-drop cut is emitted synchronously at the
// exact usage_update record, ahead of any subsequent lines in the same burst.
func (a *agyAudit) checkUsageDrop(raw json.RawMessage, at time.Time) (wsaudit.Envelope, bool) {
	p := AgyCatalog().Compaction
	if p.UsageDropRatio <= 0 || p.UsageDropRatio >= 1 {
		return wsaudit.Envelope{}, false
	}
	var u struct {
		Used int64 `json:"used"`
		Size int64 `json:"size"`
	}
	if json.Unmarshal(raw, &u) != nil || u.Used <= 0 || u.Used > wslive.MaxContextTokens || u.Size <= 0 {
		return wsaudit.Envelope{}, false
	}
	if a.prevWindow > 0 && u.Size != a.prevWindow {
		a.prevTokens, a.prevWindow = 0, u.Size
		return wsaudit.Envelope{}, false
	}
	prev := a.prevTokens
	a.prevTokens, a.prevWindow = u.Used, u.Size
	if prev <= 0 {
		return wsaudit.Envelope{}, false
	}
	dropped := prev - u.Used
	if dropped < p.UsageDropMinDelta || float64(u.Used) >= float64(prev)*p.UsageDropRatio {
		return wsaudit.Envelope{}, false
	}
	ev := a.env(wsaudit.KindCompaction, at)
	ev.Subkind = wslive.TriggerAuto
	ev.Meta = map[string]any{
		wsaudit.MetaEstimated: true,
		"inferred_by":         "usage_drop",
		"trigger":             wslive.TriggerAuto,
		"pre_tokens":          prev,
		"post_tokens":         u.Used,
	}
	return ev, true
}

func (a *agyAudit) resetCompactionBaseline() {
	a.prevTokens = 0
}
