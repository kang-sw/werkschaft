package wsagent

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

// claudeAudit maps Claude Code's stream-json output to audit envelopes
// (Decision 1's first adapter): thinking, tool calls and results, hooks,
// system reminders, the compaction boundary (the model-seen cut) and its
// summary, subagent lines, and every other system or control line.
//
// With --include-partial-messages the thinking, signature and tool-input
// deltas arrive as stream_event lines the lead's holder keeps out of its
// ring (claudeRingSkip) and tees on its audit channel. They are assembled
// per content block, never kept as raw lines: the complete assistant line
// for the block is preferred, and the assembly fills a thinking body the
// assistant line left empty, or stands in (marked incomplete when it never
// stopped or lost lines) for a block whose assistant line never came.
type claudeAudit struct {
	session string
	msgID   string
	blocks  []*claudeAuditBlock
	// tasks are subagent task ids by the Agent call that started them, so a
	// subagent's lines carry its agent id.
	tasks map[string]string
	// chains are the model conversations' request chains (Decision 7): ""
	// is the lead's own, any other key a native subagent's
	// parent_tool_use_id.
	chains map[string]*wsaudit.RequestChain
	// carried are the assemblies of streamed blocks no assistant line
	// consumed, ended by the line being mapped: tee'd lines' events, not the
	// line's own, so they never shift its ordinals (mapLine).
	carried []wsaudit.Envelope
}

type claudeAuditBlock struct {
	index     int
	typ       string
	id, name  string
	parent    string
	body      strings.Builder
	signature string
	stopped   bool
	used      bool
	at        time.Time
	lost      uint64
	lossKnown bool
	reason    string
	request   map[string]any
}

func newClaudeAudit(session string) *claudeAudit {
	return &claudeAudit{session: session, tasks: map[string]string{}, chains: map[string]*wsaudit.RequestChain{}}
}

// claudeAuditLine is the part of a stream-json line the adapter reads.
type claudeAuditLine struct {
	Type               string          `json:"type"`
	Subtype            string          `json:"subtype"`
	Message            json.RawMessage `json:"message"`
	ParentToolUseID    string          `json:"parent_tool_use_id"`
	Event              json.RawMessage `json:"event"`
	IsSynthetic        bool            `json:"isSynthetic"`
	LocalCommandSource json.RawMessage `json:"local_command_source"`
	CompactResult      string          `json:"compact_result"`
	CompactMetadata    map[string]any  `json:"compact_metadata"`
	HookID             string          `json:"hook_id"`
	HookName           string          `json:"hook_name"`
	HookEvent          string          `json:"hook_event"`
	Output             string          `json:"output"`
	Stdout             string          `json:"stdout"`
	Stderr             string          `json:"stderr"`
	ExitCode           *int            `json:"exit_code"`
	Outcome            string          `json:"outcome"`
	TaskID             string          `json:"task_id"`
	ToolUseID          string          `json:"tool_use_id"`
	Description        string          `json:"description"`
	Summary            string          `json:"summary"`
	Request            json.RawMessage `json:"request"`
}

type claudeAuditEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		ID    string            `json:"id"`
		Usage *claudeAuditUsage `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type     string `json:"type"`
		ID       string `json:"id"`
		Name     string `json:"name"`
		Thinking string `json:"thinking"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		Signature   string `json:"signature"`
	} `json:"delta"`
}

type claudeAuditBlockIn struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type claudeAuditMessage struct {
	ID      string            `json:"id"`
	Content json.RawMessage   `json:"content"`
	Usage   *claudeAuditUsage `json:"usage"`
}

// claudeAuditUsage is the part of a message's usage that makes its context
// size: ctx = input + cache read + cache creation tokens.
type claudeAuditUsage struct {
	Input         int64 `json:"input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
}

// request advances parent's request chain with message id and its usage,
// and returns the chain's current request meta (nil without an id). Every
// assistant message passes through, text-only ones included, so the next
// request's prev_id names it.
func (a *claudeAudit) request(parent, id string, u *claudeAuditUsage) map[string]any {
	if id == "" {
		return nil
	}
	c := a.chains[parent]
	if c == nil {
		c = &wsaudit.RequestChain{}
		a.chains[parent] = c
	}
	var ctx int64
	if u != nil {
		ctx = u.Input + u.CacheRead + u.CacheCreation
	}
	c.See(id, ctx, u != nil)
	return c.Meta()
}

// withRequest sets an event's request meta when it has one.
func withRequest(ev *wsaudit.Envelope, req map[string]any) {
	if req != nil {
		setMeta(ev, wsaudit.MetaRequest, req)
	}
}

func (a *claudeAudit) env(kind wsaudit.Kind, at time.Time) wsaudit.Envelope {
	ev, _ := wsaudit.NewEnvelope(a.session, BackendClaude, at, kind)
	return ev
}

func setMeta(ev *wsaudit.Envelope, key string, v any) {
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	ev.Meta[key] = v
}

func compactJSON(raw []byte) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) == nil {
		return b.String()
	}
	return string(raw)
}

func (a *claudeAudit) mapLine(line []byte, at time.Time) (bool, []wsaudit.Envelope, []wsaudit.Envelope) {
	recognized, evs := a.mapOwn(line, at)
	carried := a.carried
	a.carried = nil
	return recognized, carried, evs
}

// mapOwn maps one line to its own events; the assemblies the line ends are
// collected in carried.
func (a *claudeAudit) mapOwn(line []byte, at time.Time) (bool, []wsaudit.Envelope) {
	var l claudeAuditLine
	if json.Unmarshal(line, &l) != nil || l.Type == "" {
		return false, nil
	}
	switch l.Type {
	case "stream_event":
		return true, a.streamEvent(l, at)
	case "assistant":
		return true, a.assistant(l, at)
	case "user":
		return true, a.user(l, at)
	case "system":
		return true, a.system(l, line, at)
	case "control_request":
		ev := a.env(wsaudit.KindSystem, at)
		ev.Subkind = "control_request"
		var req struct {
			Subtype   string `json:"subtype"`
			ToolUseID string `json:"tool_use_id"`
		}
		if json.Unmarshal(l.Request, &req) == nil {
			if req.Subtype != "" {
				ev.Subkind += ":" + req.Subtype
			}
			ev.CallID = req.ToolUseID
		}
		ev.Out = compactJSON(line)
		return true, []wsaudit.Envelope{ev}
	case "result":
		a.carry(at)
		ev := a.env(wsaudit.KindSystem, at)
		ev.Subkind = joinSubkind(l.Type, l.Subtype)
		ev.Out = compactJSON(line)
		return true, []wsaudit.Envelope{ev}
	case "control_response", "control_cancel_request", "rate_limit_event", "keep_alive", "command_lifecycle":
		ev := a.env(wsaudit.KindSystem, at)
		ev.Subkind = joinSubkind(l.Type, l.Subtype)
		ev.Out = compactJSON(line)
		return true, []wsaudit.Envelope{ev}
	}
	return false, nil
}

func joinSubkind(typ, sub string) string {
	if sub == "" {
		return typ
	}
	return typ + ":" + sub
}

// streamEvent assembles the partial-message lines; it yields events only
// when a new message ends the previous one's unconsumed blocks.
func (a *claudeAudit) streamEvent(l claudeAuditLine, at time.Time) []wsaudit.Envelope {
	var e claudeAuditEvent
	if json.Unmarshal(l.Event, &e) != nil {
		return nil
	}
	switch e.Type {
	case "message_start":
		a.carry(at)
		a.msgID, a.blocks = e.Message.ID, nil
		a.request(l.ParentToolUseID, e.Message.ID, e.Message.Usage)
	case "content_block_start":
		b := &claudeAuditBlock{index: e.Index, typ: e.ContentBlock.Type, id: e.ContentBlock.ID, name: e.ContentBlock.Name, parent: l.ParentToolUseID, at: at, request: a.chains[l.ParentToolUseID].Meta()}
		b.body.WriteString(e.ContentBlock.Thinking)
		// A block start opens its index afresh.
		a.blocks = slices.DeleteFunc(a.blocks, func(o *claudeAuditBlock) bool { return o.index == e.Index && !o.used })
		a.blocks = append(a.blocks, b)
	case "content_block_delta":
		b := a.block(e.Index)
		if b == nil {
			typ := "thinking"
			if e.Delta.Type == "input_json_delta" {
				typ = "tool_use"
			}
			b = &claudeAuditBlock{index: e.Index, typ: typ, parent: l.ParentToolUseID, at: at, request: a.chains[l.ParentToolUseID].Meta()}
			a.blocks = append(a.blocks, b)
		}
		switch e.Delta.Type {
		case "thinking_delta":
			b.body.WriteString(e.Delta.Thinking)
		case "input_json_delta":
			b.body.WriteString(e.Delta.PartialJSON)
		case "signature_delta":
			b.signature = e.Delta.Signature
		}
	case "content_block_stop":
		if b := a.block(e.Index); b != nil {
			b.stopped = true
		}
	}
	return nil
}

func (a *claudeAudit) block(index int) *claudeAuditBlock {
	for _, b := range a.blocks {
		if b.index == index {
			return b
		}
	}
	return nil
}

// take is the first unconsumed streamed block matching typ (and id, when
// both carry one), marked consumed.
func (a *claudeAudit) take(typ, id string) *claudeAuditBlock {
	for _, b := range a.blocks {
		if b.used || b.typ != typ || (id != "" && b.id != "" && b.id != id) {
			continue
		}
		b.used = true
		return b
	}
	return nil
}

// carryLoss marks ev incomplete when its assembly lost lines.
func carryLoss(ev *wsaudit.Envelope, b *claudeAuditBlock) {
	if b == nil || b.reason == "" {
		return
	}
	setMeta(ev, wsaudit.MetaIncomplete, true)
	setMeta(ev, wsaudit.MetaLossReason, b.reason)
	if b.lossKnown {
		setMeta(ev, wsaudit.MetaLost, b.lost)
	}
}

func (a *claudeAudit) assistant(l claudeAuditLine, at time.Time) []wsaudit.Envelope {
	var msg claudeAuditMessage
	if json.Unmarshal(l.Message, &msg) != nil {
		return nil
	}
	if a.msgID != "" && msg.ID != "" && msg.ID != a.msgID {
		a.carry(at)
		a.msgID, a.blocks = "", nil
	}
	req := a.request(l.ParentToolUseID, msg.ID, msg.Usage)
	var blocks []json.RawMessage
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return nil
	}
	var out []wsaudit.Envelope
	for _, raw := range blocks {
		var b claudeAuditBlockIn
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		if l.ParentToolUseID != "" {
			out = append(out, a.subagentBlock(l.ParentToolUseID, b, raw, at))
			continue
		}
		switch b.Type {
		case "thinking", "redacted_thinking":
			ev := a.env(wsaudit.KindThinking, at)
			ev.Out = b.Thinking
			asm := a.take("thinking", "")
			if b.Type == "redacted_thinking" {
				ev.Subkind = "redacted"
				asm = nil
			}
			if ev.Out == "" && asm != nil {
				if asm.body.Len() > 0 {
					ev.Out = asm.body.String()
					setMeta(&ev, wsaudit.MetaAssembled, true)
				}
				// A loss that took every delta still marks the event.
				carryLoss(&ev, asm)
			}
			if ev.Out == "" && (b.Signature != "" || b.Type == "redacted_thinking") {
				setMeta(&ev, wsaudit.MetaSignatureOnly, true)
			}
			out = append(out, ev)
		case "tool_use", "server_tool_use", "mcp_tool_use":
			ev := a.env(wsaudit.KindToolCall, at)
			ev.Subkind, ev.CallID, ev.In = b.Name, b.ID, compactJSON(b.Input)
			a.take("tool_use", b.ID)
			out = append(out, ev)
		case "text":
			ev := a.env(wsaudit.KindAssistantText, at)
			ev.Out = b.Text
			out = append(out, ev)
		default:
			if b.ToolUseID != "" {
				// A server tool's result block (web search, web fetch).
				ev := a.env(wsaudit.KindToolResult, at)
				ev.Subkind, ev.CallID, ev.Out = b.Type, b.ToolUseID, compactJSON(b.Content)
				out = append(out, ev)
				continue
			}
			out = append(out, wsaudit.NewOtherEnvelope(a.session, BackendClaude, at, compactJSON(raw)))
		}
	}
	for i := range out {
		withRequest(&out[i], req)
	}
	return out
}

// subagentBlock is one content block of a line a subagent wrote.
func (a *claudeAudit) subagentBlock(parent string, b claudeAuditBlockIn, raw json.RawMessage, at time.Time) wsaudit.Envelope {
	ev := a.env(wsaudit.KindSubagent, at)
	ev.Subkind = b.Type
	ev.ParentID = parent
	ev.AgentID = a.tasks[parent]
	switch b.Type {
	case "text":
		ev.Out = b.Text
	case "thinking":
		ev.Out = b.Thinking
	case "tool_use", "server_tool_use", "mcp_tool_use":
		ev.CallID, ev.In = b.ID, compactJSON(b.Input)
		setMeta(&ev, "tool", b.Name)
	case "tool_result":
		ev.CallID, ev.Out = b.ToolUseID, claudeResultText(b.Content)
		if b.IsError {
			setMeta(&ev, "is_error", true)
		}
	default:
		ev.Out = compactJSON(raw)
	}
	return ev
}

const claudeCompactSummaryPrefix = "This session is being continued from a previous conversation"

func isReminder(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "<system-reminder>")
}

func (a *claudeAudit) user(l claudeAuditLine, at time.Time) []wsaudit.Envelope {
	var msg claudeAuditMessage
	if json.Unmarshal(l.Message, &msg) != nil {
		return nil
	}
	var text string
	if json.Unmarshal(msg.Content, &text) == nil {
		if l.ParentToolUseID != "" {
			return []wsaudit.Envelope{a.subagentBlock(l.ParentToolUseID, claudeAuditBlockIn{Type: "text", Text: text}, nil, at)}
		}
		var ev wsaudit.Envelope
		switch {
		case l.IsSynthetic && strings.HasPrefix(text, claudeCompactSummaryPrefix):
			ev = a.env(wsaudit.KindSystem, at)
			ev.Subkind = "compact_summary"
		case l.IsSynthetic:
			ev = a.env(wsaudit.KindSystem, at)
			ev.Subkind = "synthetic_user"
		case len(l.LocalCommandSource) > 0 && string(l.LocalCommandSource) != "null":
			ev = a.env(wsaudit.KindSystem, at)
			ev.Subkind = "local_command"
		case isReminder(text):
			ev = a.env(wsaudit.KindSystemReminder, at)
		default:
			ev = a.env(wsaudit.KindUser, at)
		}
		ev.Out = text
		return []wsaudit.Envelope{ev}
	}
	var blocks []json.RawMessage
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return nil
	}
	var out []wsaudit.Envelope
	for _, raw := range blocks {
		var b claudeAuditBlockIn
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		if l.ParentToolUseID != "" {
			out = append(out, a.subagentBlock(l.ParentToolUseID, b, raw, at))
			continue
		}
		switch b.Type {
		case "tool_result":
			ev := a.env(wsaudit.KindToolResult, at)
			ev.CallID, ev.Out = b.ToolUseID, claudeResultText(b.Content)
			if b.IsError {
				setMeta(&ev, "is_error", true)
			}
			out = append(out, ev)
		case "text":
			kind := wsaudit.KindUser
			if isReminder(b.Text) {
				kind = wsaudit.KindSystemReminder
			}
			ev := a.env(kind, at)
			ev.Out = b.Text
			out = append(out, ev)
		default:
			ev := a.env(wsaudit.KindUser, at)
			ev.Subkind = b.Type
			out = append(out, ev)
		}
	}
	return out
}

func (a *claudeAudit) system(l claudeAuditLine, line []byte, at time.Time) []wsaudit.Envelope {
	switch l.Subtype {
	case "compact_boundary":
		ev := a.env(wsaudit.KindCompaction, at)
		for _, key := range []string{"trigger", "pre_tokens", "post_tokens"} {
			if v, ok := l.CompactMetadata[key]; ok {
				setMeta(&ev, key, v)
			}
		}
		if trigger, _ := l.CompactMetadata["trigger"].(string); trigger != "" {
			ev.Subkind = trigger
		}
		return []wsaudit.Envelope{ev}
	case "hook_started", "hook_progress", "hook_response":
		ev := a.env(wsaudit.KindHook, at)
		ev.Subkind = l.HookEvent
		setMeta(&ev, "phase", l.Subtype)
		setMeta(&ev, "hook_name", l.HookName)
		setMeta(&ev, "hook_id", l.HookID)
		if l.ExitCode != nil {
			setMeta(&ev, "exit_code", *l.ExitCode)
		}
		if l.Outcome != "" {
			setMeta(&ev, "outcome", l.Outcome)
		}
		ev.Out = l.Output
		if ev.Out == "" {
			ev.Out = l.Stdout
		}
		if l.Stderr != "" {
			setMeta(&ev, "stderr", l.Stderr)
		}
		return []wsaudit.Envelope{ev}
	case "task_started", "task_updated", "task_progress", "task_notification":
		if l.Subtype == "task_started" && l.ToolUseID != "" && l.TaskID != "" {
			a.tasks[l.ToolUseID] = l.TaskID
		}
		ev := a.env(wsaudit.KindSubagent, at)
		ev.Subkind, ev.AgentID, ev.ParentID = l.Subtype, l.TaskID, l.ToolUseID
		ev.Out = l.Summary
		if ev.Out == "" {
			ev.Out = l.Description
		}
		return []wsaudit.Envelope{ev}
	}
	ev := a.env(wsaudit.KindSystem, at)
	ev.Subkind = l.Subtype
	if l.CompactResult == "failed" {
		ev.Subkind = "compact_failed"
	}
	ev.Out = compactJSON(line)
	return []wsaudit.Envelope{ev}
}

// carry collects the open assemblies a line ends.
func (a *claudeAudit) carry(at time.Time) {
	a.carried = append(a.carried, a.flushBlocks(at, false)...)
}

// flushBlocks emits the streamed blocks no assistant line consumed: a
// thinking body or a tool call's (possibly partial) input, marked assembled,
// and incomplete when the block never stopped or lost lines. With ending
// set every open block is cut short (the stream ended).
func (a *claudeAudit) flushBlocks(at time.Time, ending bool) []wsaudit.Envelope {
	var out []wsaudit.Envelope
	for _, b := range a.blocks {
		if b.used {
			continue
		}
		b.used = true
		lost := b.reason != ""
		if !b.stopped && b.reason == "" {
			b.reason = wsaudit.LossStreamEnd
		}
		var ev wsaudit.Envelope
		switch b.typ {
		case "thinking":
			// An empty block is no event unless it lost lines: then it is
			// the incomplete event that carries the loss markLoss gave it.
			if b.body.Len() == 0 && b.signature == "" && !lost {
				continue
			}
			ev = a.env(wsaudit.KindThinking, b.at)
			ev.Out = b.body.String()
			if ev.Out == "" && b.signature != "" {
				setMeta(&ev, wsaudit.MetaSignatureOnly, true)
			}
		case "tool_use", "server_tool_use", "mcp_tool_use":
			ev = a.env(wsaudit.KindToolCall, b.at)
			ev.Subkind, ev.CallID, ev.In = b.name, b.id, b.body.String()
		default:
			continue
		}
		if b.parent != "" {
			ev.Subkind, ev.Kind = b.typ, wsaudit.KindSubagent
			ev.ParentID = b.parent
		}
		setMeta(&ev, wsaudit.MetaAssembled, true)
		carryLoss(&ev, b)
		withRequest(&ev, b.request)
		out = append(out, ev)
	}
	if ending {
		a.blocks, a.msgID = nil, ""
	}
	return out
}

func (a *claudeAudit) markLoss(n uint64, reason string) bool {
	for i := len(a.blocks) - 1; i >= 0; i-- {
		b := a.blocks[i]
		if b.used || b.stopped {
			continue
		}
		if b.reason == "" {
			b.lossKnown = true
		}
		b.reason = reason
		if n == 0 {
			b.lossKnown = false
		}
		b.lost += n
		return true
	}
	return false
}

func (a *claudeAudit) flush(at time.Time) []wsaudit.Envelope {
	return a.flushBlocks(at, true)
}
