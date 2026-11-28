package wsagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

type pirpcEnvelope struct {
	Type string `json:"type"`
	// response fields
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
	// message events
	Message               json.RawMessage `json:"message"`
	AssistantMessageEvent json.RawMessage `json:"assistantMessageEvent"`
	// tool events
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
	Result     json.RawMessage `json:"result"`
	IsError    bool            `json:"isError"`
	// agent_end / agent_settled
	Messages  []json.RawMessage `json:"messages"`
	WillRetry bool              `json:"willRetry"`
	Aborted   bool              `json:"aborted"`
	// compaction events
	Reason       string `json:"reason"`
	ErrorMessage string `json:"errorMessage"`
	// auto_retry events
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"maxAttempts"`
	DelayMs     int    `json:"delayMs"`
	FinalError  string `json:"finalError"`
	// extension_ui_request fields
	Method     string `json:"method"`
	NotifyType string `json:"notifyType"`
}

type pirpcResponseEnvelope struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

type pirpcStateData struct {
	SessionID    string `json:"sessionId"`
	SessionFile  string `json:"sessionFile"`
	IsStreaming  bool   `json:"isStreaming"`
	IsCompacting bool   `json:"isCompacting"`
	Model        *struct {
		Provider      string `json:"provider"`
		ID            string `json:"id"`
		ContextWindow int64  `json:"contextWindow"`
	} `json:"model"`
	ThinkingLevel string `json:"thinkingLevel"`
}

type pirpcSessionStatsData struct {
	SessionID   string `json:"sessionId"`
	SessionFile string `json:"sessionFile"`
	Tokens      struct {
		Input      int64 `json:"input"`
		Output     int64 `json:"output"`
		CacheRead  int64 `json:"cacheRead"`
		CacheWrite int64 `json:"cacheWrite"`
		Total      int64 `json:"total"`
	} `json:"tokens"`
	Cost         float64 `json:"cost"`
	ContextUsage *struct {
		Tokens        *int64   `json:"tokens"`
		ContextWindow int64    `json:"contextWindow"`
		Percent       *float64 `json:"percent"`
	} `json:"contextUsage"`
}

type pirpcMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	Usage        *struct {
		Input       int64 `json:"input"`
		Output      int64 `json:"output"`
		CacheRead   int64 `json:"cacheRead"`
		CacheWrite  int64 `json:"cacheWrite"`
		TotalTokens int64 `json:"totalTokens"`
		Cost        *struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`
}

type pirpcProviderResponseEvent struct {
	Event      string `json:"ws_pi_event"`
	Status     int    `json:"status"`
	RetryAfter string `json:"retry_after"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
}

type pirpcMapper struct {
	mu sync.Mutex

	sessionID     string
	piHome        string
	worker        bool
	inTurn        bool
	turnSeq       int
	msgSeq        int
	statsSeq      int
	contextWindow int64
	replyBuf      strings.Builder
	turnErr       string
	batches       batchTracker
	pendingTools  map[string]string
	finalStatsIDs map[string]struct{}
	now           func() int64
}

func newPiRPCMapper(sessionID, piHome string, worker bool) *pirpcMapper {
	return &pirpcMapper{
		sessionID:     sessionID,
		piHome:        piHome,
		worker:        worker,
		batches:       newBatchTracker("pirpc-"),
		pendingTools:  map[string]string{},
		finalStatsIDs: map[string]struct{}{},
	}
}

func (m *pirpcMapper) running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inTurn
}

func (m *pirpcMapper) clock() int64 {
	if m.now != nil {
		return m.now()
	}
	return time.Now().UnixMilli()
}

func (m *pirpcMapper) mapLine(line []byte, replies lineReplies) []Event {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}
	var ev pirpcEnvelope
	if json.Unmarshal(line, &ev) != nil || ev.Type == "" {
		return nil
	}
	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	switch ev.Type {
	case "response":
		return m.handleResponse(ev, replies)
	case "agent_start":
		m.inTurn = true
		m.turnSeq++
		m.replyBuf.Reset()
		m.turnErr = ""
		clear(m.pendingTools)
		return []Event{{TurnStart: true, HarnessState: HarnessRunning}}
	case "turn_start":
		m.msgSeq++
		return nil
	case "message_start":
		return m.handleMessageStart(ev.Message)
	case "message_update":
		return m.handleMessageUpdate(ev.AssistantMessageEvent)
	case "message_end":
		return m.handleMessageEnd(ev.Message)
	case "tool_execution_start":
		return m.handleToolStart(ev, now)
	case "tool_execution_end":
		return m.handleToolEnd(ev, now)
	case "turn_end":
		if batchEv := m.batches.finish(now, false); batchEv != nil {
			return []Event{*batchEv}
		}
		return nil
	case "agent_end":
		return m.handleAgentEnd(ev)
	case "agent_settled":
		return m.handleAgentSettled(ev, replies, now)
	case "compaction_start":
		trigger := pirpcCompactionTrigger(ev.Reason)
		return []Event{{Live: &wslive.Event{
			Kind:    wslive.KindCompact,
			Phase:   wslive.CompactStart,
			Trigger: trigger,
		}}}
	case "compaction_end":
		return m.handleCompactionEnd(ev, replies)
	case "auto_retry_start":
		return []Event{{Retry: &wslive.Retry{
			Attempt:    ev.Attempt,
			MaxRetries: ev.MaxAttempts,
			Error:      ev.ErrorMessage,
		}}}
	case "auto_retry_end":
		return []Event{{RetryEnd: true}}
	case "extension_ui_request":
		return m.handleExtensionUIRequest(ev, replies)
	}
	return nil
}

func (m *pirpcMapper) handleResponse(ev pirpcEnvelope, replies lineReplies) []Event {
	var out []Event
	kind := pirpcIDKind(ev.ID)

	_, isFinalStats := m.finalStatsIDs[ev.ID]
	if isFinalStats {
		delete(m.finalStatsIDs, ev.ID)
	}
	if ev.Success {
		switch ev.Command {
		case "get_state":
			var st pirpcStateData
			if json.Unmarshal(ev.Data, &st) == nil {
				if st.Model != nil && st.Model.ContextWindow > 0 {
					m.contextWindow = st.Model.ContextWindow
				}
				sid := strings.TrimSpace(st.SessionID)
				if sid == "" {
					sid = strings.TrimSpace(st.SessionFile)
				}
				if sid != "" && sid != m.sessionID {
					m.sessionID = sid
					out = append(out, Event{SessionID: sid})
				}
			}
		case "get_session_stats":
			if statusEv := m.parseSessionStats(ev.Data, isFinalStats); statusEv != nil {
				out = append(out, *statusEv)
			}
		case "get_available_models":
			if models := parsePiRPCModels(ev.Data); len(models) > 0 {
				recordPiRPCChoices(m.piHome, models, nil)
			}
		case "get_available_thinking_levels":
			if levels := parsePiRPCThinkingLevels(ev.Data); len(levels) > 0 {
				recordPiRPCChoices(m.piHome, nil, levels)
			}
		case "compact":
			out = append(out, Event{compactCommandDone: true})
		}
	}

	if kind == "workerSteer" || (m.worker && ev.Command == "steer") {
		if ev.Success {
			out = append(out, Event{SteerAnswer: &SteerAnswer{}})
		} else {
			out = append(out, Event{SteerAnswer: &SteerAnswer{Refusal: ev.Error}})
		}
	}

	replies.answer(ev.ID, controlAnswer{
		ok:   ev.Success,
		err:  ev.Error,
		body: ev.Data,
	})
	return out
}

func pirpcIDKind(id string) string {
	rest, ok := strings.CutPrefix(id, "wspi/")
	if !ok {
		return ""
	}
	kind, _, _ := strings.Cut(rest, "/")
	return kind
}

func (m *pirpcMapper) handleMessageStart(raw json.RawMessage) []Event {
	var msg pirpcMessage
	if json.Unmarshal(raw, &msg) != nil {
		return nil
	}
	switch msg.Role {
	case "user":
		if !m.worker {
			return nil
		}
		if text := extractPiRPCContentText(msg.Content); text != "" {
			return []Event{{UserTaken: text}}
		}
	case "assistant":
		m.replyBuf.Reset()
		m.msgSeq++
	}
	return nil
}

func (m *pirpcMapper) handleMessageUpdate(raw json.RawMessage) []Event {
	var delta struct {
		Type    string `json:"type"`
		Delta   string `json:"delta"`
		Content string `json:"content"`
		Reason  string `json:"reason"`
	}
	if json.Unmarshal(raw, &delta) != nil {
		return nil
	}
	pending := len(m.pendingTools)
	switch delta.Type {
	case "text_delta":
		if delta.Delta == "" {
			return nil
		}
		m.replyBuf.WriteString(delta.Delta)
		return []Event{
			{
				Live:          &wslive.Event{Kind: wslive.KindReply, Text: m.replyBuf.String()},
				RetryEnd:      true,
				ModelActivity: &pending,
				ModelContent:  true,
			},
		}
	case "text_end":
		if m.replyBuf.Len() == 0 && delta.Content != "" {
			m.replyBuf.WriteString(delta.Content)
		}
		return []Event{{ModelActivity: &pending, ModelContent: true}}
	case "thinking_delta", "thinking_end":
		return []Event{{ModelActivity: &pending, ModelContent: true}}
	case "error":
		if delta.Reason != "" && m.turnErr == "" {
			m.turnErr = delta.Reason
		}
	}
	return nil
}

func (m *pirpcMapper) handleMessageEnd(raw json.RawMessage) []Event {
	var msg pirpcMessage
	if json.Unmarshal(raw, &msg) != nil || msg.Role != "assistant" {
		return nil
	}
	var out []Event
	text := extractPiRPCContentText(msg.Content)
	if strings.TrimSpace(text) == "" {
		text = m.replyBuf.String()
	}
	m.replyBuf.Reset()
	if strings.TrimSpace(text) != "" {
		source := fmt.Sprintf("pi-rpc/%s/%d/%d", m.sessionID, m.turnSeq, m.msgSeq)
		out = append(out,
			Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: text, Final: true, Source: source}},
			Event{RetryEnd: true},
		)
	}
	if msg.ErrorMessage != "" {
		m.turnErr = msg.ErrorMessage
	} else if msg.StopReason == "error" && m.turnErr == "" {
		m.turnErr = "assistant turn ended with error"
	}
	if msg.Usage != nil {
		u := msg.Usage
		total := u.TotalTokens
		if total == 0 {
			total = u.Input + u.Output + u.CacheRead + u.CacheWrite
		}
		group := fmt.Sprintf("turn-%d-msg-%d", m.turnSeq, m.msgSeq)
		out = append(out, m.batches.setUsage(group, &wslive.TokenUsage{
			Input:      u.Input,
			Output:     u.Output,
			CacheRead:  u.CacheRead,
			CacheWrite: u.CacheWrite,
		})...)
		if total > 0 || m.contextWindow > 0 {
			tokens := total
			ctxUsage := &wslive.UsageContext{
				Tokens: &tokens,
				Window: m.contextWindow,
			}
			if m.contextWindow > 0 {
				pct := float64(tokens) * 100 / float64(m.contextWindow)
				ctxUsage.Percent = &pct
			}
			if denom := u.Input + u.CacheRead + u.CacheWrite; denom > 0 && u.CacheRead > 0 {
				hit := float64(u.CacheRead) * 100 / float64(denom)
				ctxUsage.CacheHit = &hit
			}
			var cost *wslive.UsageCost
			if u.Cost != nil && u.Cost.Total > 0 {
				cost = &wslive.UsageCost{USD: u.Cost.Total}
			}
			out = append(out, Event{Live: &wslive.Event{
				Kind:    wslive.KindStatus,
				Context: ctxUsage,
				Cost:    cost,
			}})
		}
	}
	return out
}

func (m *pirpcMapper) handleToolStart(ev pirpcEnvelope, now int64) []Event {
	if ev.ToolCallID == "" {
		return nil
	}
	m.pendingTools[ev.ToolCallID] = ev.ToolName
	group := fmt.Sprintf("turn-%d-msg-%d", m.turnSeq, m.msgSeq)
	return m.batches.addCall(group, ev.ToolCallID, pirpcDisplayToolName(ev.ToolName), argsPreview(ev.Args), len(ev.Args), now)
}

func (m *pirpcMapper) handleToolEnd(ev pirpcEnvelope, now int64) []Event {
	if ev.ToolCallID == "" {
		return nil
	}
	name := ev.ToolName
	if name == "" {
		name = m.pendingTools[ev.ToolCallID]
	}
	delete(m.pendingTools, ev.ToolCallID)
	out := m.batches.endCall(ev.ToolCallID, ev.IsError, len(ev.Result), now)
	if !ev.IsError {
		if wsTool := pirpcResultTool(name); wsTool != "" {
			if text := extractPiRPCToolResultText(ev.Result); text != "" {
				out = append(out, resultEvents(wsTool, text)...)
			}
		}
	}
	return out
}

func (m *pirpcMapper) handleAgentEnd(ev pirpcEnvelope) []Event {
	if m.turnErr == "" {
		for i := len(ev.Messages) - 1; i >= 0; i-- {
			var msg pirpcMessage
			if json.Unmarshal(ev.Messages[i], &msg) == nil && msg.Role == "assistant" {
				if msg.ErrorMessage != "" {
					m.turnErr = msg.ErrorMessage
				}
				break
			}
		}
	}
	return nil
}

func (m *pirpcMapper) handleAgentSettled(ev pirpcEnvelope, replies lineReplies, now int64) []Event {
	var out []Event
	if batchEv := m.batches.finish(now, ev.Aborted); batchEv != nil {
		out = append(out, *batchEv)
	}
	clear(m.pendingTools)
	m.inTurn = false
	m.replyBuf.Reset()

	turnEnd := Event{
		TurnEnd:      true,
		TurnError:    m.turnErr,
		HarnessState: HarnessIdle,
		RetryEnd:     true,
	}
	if ev.Aborted {
		turnEnd.TurnReason = wslive.TurnAborted
	}
	m.turnErr = ""
	out = append(out, turnEnd)

	m.requestStats(replies, true)
	return out
}

func (m *pirpcMapper) handleCompactionEnd(ev pirpcEnvelope, replies lineReplies) []Event {
	trigger := pirpcCompactionTrigger(ev.Reason)
	phase := wslive.CompactEnd
	if ev.Aborted || strings.TrimSpace(ev.ErrorMessage) != "" {
		phase = wslive.CompactFailed
	}
	m.requestStats(replies, false)
	return []Event{{Live: &wslive.Event{
		Kind:    wslive.KindCompact,
		Phase:   phase,
		Trigger: trigger,
	}}}
}

func (m *pirpcMapper) requestStats(replies lineReplies, final bool) {
	m.statsSeq++
	id := "wspi/stats/auto-" + strconv.Itoa(m.statsSeq)
	if final {
		if m.finalStatsIDs == nil {
			m.finalStatsIDs = map[string]struct{}{}
		}
		m.finalStatsIDs[id] = struct{}{}
	}
	replies.send(map[string]any{
		"id":   id,
		"type": "get_session_stats",
	})
}

func (m *pirpcMapper) parseSessionStats(raw json.RawMessage, final bool) *Event {
	var stats pirpcSessionStatsData
	if json.Unmarshal(raw, &stats) != nil {
		return nil
	}
	var ctxUsage *wslive.UsageContext
	if stats.ContextUsage != nil {
		window := stats.ContextUsage.ContextWindow
		if window > 0 {
			m.contextWindow = window
		} else {
			window = m.contextWindow
		}
		ctxUsage = &wslive.UsageContext{
			Tokens:  stats.ContextUsage.Tokens,
			Window:  window,
			Percent: stats.ContextUsage.Percent,
		}
	} else if stats.Tokens.Total > 0 || m.contextWindow > 0 {
		tokens := stats.Tokens.Total
		ctxUsage = &wslive.UsageContext{
			Tokens: &tokens,
			Window: m.contextWindow,
		}
		if m.contextWindow > 0 {
			pct := float64(tokens) * 100 / float64(m.contextWindow)
			ctxUsage.Percent = &pct
		}
	}
	if ctxUsage != nil {
		if denom := stats.Tokens.Input + stats.Tokens.CacheRead + stats.Tokens.CacheWrite; denom > 0 && stats.Tokens.CacheRead > 0 {
			hit := float64(stats.Tokens.CacheRead) * 100 / float64(denom)
			ctxUsage.CacheHit = &hit
		}
	}
	var cost *wslive.UsageCost
	if stats.Cost > 0 {
		cost = &wslive.UsageCost{USD: stats.Cost}
	}
	if ctxUsage == nil && cost == nil {
		return nil
	}
	return &Event{
		Live: &wslive.Event{
			Kind:    wslive.KindStatus,
			Context: ctxUsage,
			Cost:    cost,
		},
		FinalUsage: final,
	}
}

func (m *pirpcMapper) handleExtensionUIRequest(ev pirpcEnvelope, replies lineReplies) []Event {
	switch ev.Method {
	case "notify":
		var msg string
		if json.Unmarshal(ev.Message, &msg) != nil {
			msg = strings.TrimSpace(string(ev.Message))
		}
		if msg == "" {
			return nil
		}
		var pr pirpcProviderResponseEvent
		if json.Unmarshal([]byte(msg), &pr) == nil && pr.Event == "provider_response" {
			return mapPiRPCProviderResponse(pr)
		}
		return []Event{{Notice: msg}}
	case "select", "confirm", "input", "editor":
		if ev.ID != "" {
			replies.send(map[string]any{
				"type":      "extension_ui_response",
				"id":        ev.ID,
				"cancelled": true,
			})
		}
		return nil
	}
	return nil
}

func mapPiRPCProviderResponse(pr pirpcProviderResponseEvent) []Event {
	var parts []string
	parts = append(parts, fmt.Sprintf("provider HTTP %d", pr.Status))
	if pr.Provider != "" {
		parts = append(parts, "provider="+pr.Provider)
	}
	if pr.Model != "" {
		parts = append(parts, "model="+pr.Model)
	}
	if pr.RetryAfter != "" {
		parts = append(parts, "retry_after="+pr.RetryAfter)
	}
	summary := "pi-rpc rate/overload warning: " + strings.Join(parts, " ")
	out := []Event{{Notice: summary}}
	if pr.Status == 429 || pr.Status == 529 {
		out = append(out, Event{
			Retry: &wslive.Retry{
				Attempt: 1,
				Status:  pr.Status,
				Error:   summary,
			},
		})
	}
	return out
}

func pirpcCompactionTrigger(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "manual":
		return wslive.TriggerManual
	case "threshold", "overflow", "auto":
		return wslive.TriggerAuto
	default:
		return ""
	}
}

func pirpcDisplayToolName(name string) string {
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		if server, tool, has := strings.Cut(rest, "__"); has && server != "" && tool != "" {
			return server + "/" + tool
		}
	}
	return name
}

func pirpcResultTool(name string) string {
	return resultTool(name, func(server, tool string) string {
		sanitized, _ := ApplyToolNameScheme(ToolNameSchemeSanitize, tool)
		return "mcp__" + server + "__" + sanitized
	})
}

func extractPiRPCContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "")
}

func extractPiRPCToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &obj) == nil && len(obj.Content) > 0 {
		return extractPiRPCContentText(obj.Content)
	}
	return ""
}

func parsePiRPCModels(raw json.RawMessage) []ModelChoice {
	var wrapper struct {
		Models []struct {
			ID            string `json:"id"`
			Provider      string `json:"provider"`
			Name          string `json:"name"`
			Reasoning     *bool  `json:"reasoning"`
			ContextWindow int64  `json:"contextWindow"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &wrapper) != nil {
		return nil
	}
	out := make([]ModelChoice, 0, len(wrapper.Models))
	seen := map[string]bool{}
	for _, m := range wrapper.Models {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		fullID := id
		if provider := strings.TrimSpace(m.Provider); provider != "" && !strings.HasPrefix(id, provider+"/") {
			fullID = provider + "/" + id
		}
		if seen[fullID] {
			continue
		}
		seen[fullID] = true
		choice := ModelChoice{
			ID:            fullID,
			DisplayName:   strings.TrimSpace(m.Name),
			contextWindow: m.ContextWindow,
		}
		if m.Reasoning != nil {
			v := *m.Reasoning
			choice.Thinking = &v
		}
		out = append(out, choice)
	}
	return out
}

func parsePiRPCThinkingLevels(raw json.RawMessage) []string {
	var wrapper struct {
		Levels []string `json:"levels"`
	}
	var levels []string
	if json.Unmarshal(raw, &wrapper) == nil && len(wrapper.Levels) > 0 {
		levels = wrapper.Levels
	} else {
		_ = json.Unmarshal(raw, &levels)
	}
	out := make([]string, 0, len(levels))
	for _, l := range levels {
		l = strings.TrimSpace(l)
		if l != "" && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}
