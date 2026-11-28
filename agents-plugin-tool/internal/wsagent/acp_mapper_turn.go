package wsagent

import (
	"cmp"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/wslive"
)

// acpPartialEvery is the least time, in ms, between two sends of the
// streaming reply: every reply event makes the runner take a snapshot.
const acpPartialEvery = 100

// startTurn opens a turn at its first content: a turn ws's own prompt began
// is already running in the runner and a replayed one is rebuilt here. Live
// content without a known prompt opens an unsolicited turn bounded by wakeTimer.
func (m *acpMapper) startTurn(out []Event, replaying bool) []Event {
	if m.inTurn {
		return out
	}
	m.inTurn = true
	m.modelFailure = ""
	m.wakeStepHadText = false
	m.wakeSawUsage = false
	if len(m.outstanding) == 0 && m.reattachedPromptAfter == nil && !replaying {
		m.unsolicitedTurn = true
	}
	m.turnSeq++
	m.group, m.groupCalls = 0, false
	return append(out, Event{TurnStart: true})
}

// flushReply ends the reply the turn streams so far.
func (m *acpMapper) flushReply(out []Event) []Event {
	text := m.reply.String()
	m.reply.Reset()
	m.replyAt = 0
	if strings.TrimSpace(text) == "" {
		return out
	}
	return append(out, Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: text, Final: true}})
}

// stripReplyFailure removes a terminal failure line once later model activity
// proves the agent recovered. The live partial remains a diagnostic, but the final
// reply and parent report contain only the successful attempt.
func (m *acpMapper) stripReplyFailure(failure string) {
	if failure == "" {
		return
	}
	text := m.reply.String()
	trimmed := strings.TrimRight(text, " \t\r\n")
	if !strings.HasSuffix(trimmed, failure) {
		return
	}
	prefix := strings.TrimSuffix(trimmed, failure)
	prefix = strings.TrimSuffix(prefix, "\n")
	prefix = strings.TrimSuffix(prefix, "\r")
	m.reply.Reset()
	m.reply.WriteString(prefix)
	m.replyAt = 0
}

func (m *acpMapper) groupID() string {
	return "t" + strconv.Itoa(m.turnSeq) + "g" + strconv.Itoa(m.group)
}

// endTurn settles a turn at its last prompt's answer (or at the quiet end of
// a background-wakeup turn): the reply, every permission request still
// pending, any non-daemon run_command calls that went async across a normal
// end, and the open tool batch end with it.
func (m *acpMapper) endTurn(msg acpMessage, now int64) []Event {
	out := m.flushReply(nil)
	for id := range m.approvals {
		out = append(out, Event{ApprovalDone: id})
	}
	clear(m.approvals)
	clear(m.approvalOf)
	var r struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(msg.Result, &r)
	normalEnd := msg.Error == nil && (r.StopReason == "" || r.StopReason == "end_turn")
	if b := m.batches.finish(now, !normalEnd); b != nil {
		out = append(out, *b)
	}
	if normalEnd && len(m.openBg) > 0 {
		for id := range m.openBg {
			m.bgCalls[id] = true
		}
		n := len(m.bgCalls)
		out = append(out, Event{BackgroundTasks: &n})
	}
	clear(m.openBg)
	clear(m.openCalls)
	clear(m.pendingTools)
	modelFailure := m.modelFailure
	m.modelFailure = ""
	m.inTurn = false
	m.unsolicitedTurn = false
	m.wakeStepHadText = false
	m.wakeSawUsage = false
	m.stopWakeTimerLocked()
	m.rate.dropUntracked()
	ev := Event{TurnEnd: true, ModelFailure: modelFailure}
	switch {
	case msg.Error != nil:
		ev.TurnError = m.d.label() + ": " + msg.Error.Message
	case r.StopReason == "cancelled":
		ev.TurnReason = wslive.TurnAborted
	case r.StopReason != "" && r.StopReason != "end_turn":
		ev.TurnError = m.d.label() + " stopped the turn: " + r.StopReason
	}
	if alert := m.d.usageLimit(msg, modelFailure, now); alert != nil {
		out = append(out, Event{Alert: alert})
	}
	return append(out, ev)
}

// update maps a session/update notification.
func (m *acpMapper) update(params json.RawMessage, replies lineReplies, now int64) []Event {
	var p struct {
		SessionID string           `json:"sessionId"`
		Update    acpSessionUpdate `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil
	}
	if m.ephemeral && p.SessionID != "" && m.sessionID != "" && p.SessionID != m.sessionID {
		return nil
	}
	u := p.Update
	_, replaying := replies.(silentReplies)
	if u.SessionUpdate != "agent_message_chunk" {
		m.rate.breakRun(replaying)
	}
	switch u.SessionUpdate {
	case "agent_message_chunk", "agent_thought_chunk", "ws_activity", "plan":
		// A holder pulse stands in for dropped thought lines.
		m.content = true
	}
	switch u.SessionUpdate {
	case "agent_message_chunk":
		out := m.startTurn(nil, replaying)
		var c struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(u.Content, &c) != nil || c.Type != "text" || c.Text == "" {
			m.armWakeIfReadyLocked(replaying)
			return out
		}
		m.rate.messageChunk(now, replaying)
		m.wakeStepHadText = true
		m.wakeSawUsage = false
		m.armWakeIfReadyLocked(replaying)
		if m.groupCalls {
			// Text after a tool batch is the next message's.
			m.group++
			m.groupCalls = false
		}
		before := m.reply.String()
		oldFailure := m.modelFailure
		m.reply.WriteString(c.Text)
		m.modelFailure = m.d.replyFailure(m.reply.String())
		if oldFailure != "" && m.modelFailure == "" {
			m.reply.Reset()
			m.reply.WriteString(before)
			m.stripReplyFailure(oldFailure)
			m.reply.WriteString(c.Text)
		}
		if m.replyAt != 0 && now-m.replyAt < acpPartialEvery {
			return out
		}
		m.replyAt = now
		if strings.TrimSpace(m.reply.String()) == "" {
			return out
		}
		return append(out, Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: m.reply.String()}})
	case "agent_thought_chunk":
		out := m.startTurn(nil, replaying)
		m.stripReplyFailure(m.modelFailure)
		m.modelFailure = ""
		m.wakeSawUsage = false
		m.armWakeIfReadyLocked(replaying)
		return out
	case "ws_activity":
		// A holder pulse can trail a prompt response within one read.
		// Extend an open turn's quiet window without starting a new turn.
		if m.unsolicitedTurn && !replaying {
			m.wakeSawUsage = false
			m.armWakeIfReadyLocked(false)
		}
	case "tool_call":
		out := m.startTurn(nil, replaying)
		m.stripReplyFailure(m.modelFailure)
		out = m.flushReply(out)
		m.modelFailure = ""
		m.wakeStepHadText = false
		m.wakeSawUsage = false
		m.stopWakeTimerLocked()
		name := u.Name
		if name == "" {
			name = u.Title
		}
		class := m.d.classifyTool(name, u.Args, nil)
		if u.ToolCallID != "" && u.Status != "completed" && u.Status != "failed" {
			m.pendingTools[u.ToolCallID] = true
			isDaemon := false
			if class.command {
				isDaemon = class.daemon
				if !isDaemon && !m.cutover {
					m.openBg[u.ToolCallID] = true
				}
			}
			if !isDaemon {
				m.openCalls[u.ToolCallID] = true
			}
		}
		out = append(out, m.batches.addCall(m.groupID(), u.ToolCallID, name, argsPreview(u.Args), len(acpAuditBody(u.Args, u.RawInput)), now)...)
		m.groupCalls = true
		if class.subagent {
			out = append(out, m.nativeStart(u, class.label, now)...)
			if m.warnNativeSubagent && u.ToolCallID != "" && !m.warnedSubagent[u.ToolCallID] {
				if len(m.warnedSubagent) >= acpCorrelationCap {
					clear(m.warnedSubagent)
				}
				m.warnedSubagent[u.ToolCallID] = true
				out = append(out, Event{UnrequestedSubagent: u.ToolCallID})
			}
		}
		return append(out, m.toolStatus(u, replaying, now)...)
	case "tool_call_update":
		return m.toolStatus(u, replaying, now)
	case "usage_update":
		if m.inTurn && len(m.approvals) == 0 && (len(m.openCalls) == 0 || (m.wakeStepHadText && len(m.openCalls) == len(m.openBg))) {
			m.wakeSawUsage = true
			m.armWakeIfReadyLocked(replaying)
		}
		if u.Used == nil {
			return nil
		}
		used := *u.Used
		ctx := &wslive.UsageContext{Tokens: &used, Window: u.Size}
		if u.Size > 0 {
			pct := float64(used) * 100 / float64(u.Size)
			ctx.Percent = &pct
		}
		m.lastContext = cloneUsageContext(ctx)
		return []Event{{Live: &wslive.Event{Kind: wslive.KindStatus, Context: ctx}}}
	case "available_commands_update":
		return m.commands(u.AvailableCommands)
	case "current_mode_update":
		if u.CurrentModeID == "" {
			return nil
		}
		m.curMode = u.CurrentModeID
		return []Event{{PermissionMode: u.CurrentModeID}}
	case "config_option_update":
		m.configOptions(params2configOptions(params))
	}
	return nil
}

// toolStatus ends a tool call that reached a final status: its batch
// entry, its permission request, any tracked background command, and a
// tracker write it made.
func (m *acpMapper) toolStatus(u acpSessionUpdate, replaying bool, now int64) []Event {
	if u.Status != "completed" && u.Status != "failed" {
		return nil
	}
	failed := u.Status == "failed"
	var out []Event
	if u.ToolCallID != "" && !m.requested[u.ToolCallID] && !m.ranUnrequested[u.ToolCallID] && m.commandRan(u) {
		// An in_progress announcement may precede the request; only output
		// or an exit code shows the command actually ran without one.
		m.ranUnrequested[u.ToolCallID] = true
		out = append(out, Event{UnrequestedCommand: u.ToolCallID})
	}
	delete(m.pendingTools, u.ToolCallID)
	delete(m.openCalls, u.ToolCallID)
	delete(m.openBg, u.ToolCallID)
	out = append(out, m.batches.endCall(u.ToolCallID, failed, len(acpAuditBody(u.RawOutput, u.Content)), now)...)
	if m.nativeLaunch[u.ToolCallID] {
		// A completed launch only means the child runs asynchronously; its
		// row stays running until the runner settles it at the parent's
		// logical turn end. A failed launch ends the row here.
		delete(m.nativeLaunch, u.ToolCallID)
		if failed {
			out = append(out, Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: m.d.nativeRowPrefix() + u.ToolCallID, Status: wslive.StatusFailed, At: now}})
		}
	}
	if id, ok := m.approvalOf[u.ToolCallID]; ok {
		delete(m.approvalOf, u.ToolCallID)
		delete(m.approvals, id)
		out = append(out, Event{ApprovalDone: id})
	}
	if m.bgCalls[u.ToolCallID] {
		delete(m.bgCalls, u.ToolCallID)
		n := len(m.bgCalls)
		out = append(out, Event{BackgroundTasks: &n})
	}
	if !failed && m.d.mcpToolMatches(u.Name, ReservedMCPServer, TrackerWriteTool) {
		out = append(out, resultEvents(TrackerWriteTool, "")...)
	}
	m.armWakeIfReadyLocked(replaying)
	return out
}

// commandRan reports whether a final update is the dialect's native command
// tool with execution evidence in its raw output.
func (m *acpMapper) commandRan(u acpSessionUpdate) bool {
	class := m.d.classifyTool(cmp.Or(u.Name, u.Title), u.Args, u.RawOutput)
	return class.command && class.ran
}

// nativeStart opens a running native subagent row for a hosted lead's
// native subagent launch, labelled as the dialect classified it. The launch
// prompt is never read into the row. The agent reports no child id,
// progress, or end, so the row carries no wake, turn, or batch authority and
// ends only by a failed launch (toolStatus) or the runner's end-unknown
// settlement.
func (m *acpMapper) nativeStart(u acpSessionUpdate, label string, now int64) []Event {
	if !m.lead || u.ToolCallID == "" {
		return nil
	}
	if u.Status != "completed" && u.Status != "failed" {
		m.nativeLaunch[u.ToolCallID] = true
	}
	out := []Event{{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: m.d.nativeRowPrefix() + u.ToolCallID, Label: label, Status: wslive.StatusRunning, At: now}}}
	if u.Status == "failed" {
		out = append(out, Event{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: m.d.nativeRowPrefix() + u.ToolCallID, Status: wslive.StatusFailed, At: now}})
	}
	return out
}
