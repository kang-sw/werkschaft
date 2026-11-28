package wsagent

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// acp_mapper.go is the shared ACP line mapper. The wire facts it relies on:
//
//   - A turn is a session/prompt. A prompt sent while one runs steers it: the
//     agent answers the earlier one at once and the run belongs to the later
//     one, so the turn ends with the last outstanding prompt's response.
//   - Permission requests are agent requests whose JSON-RPC id is echoed raw
//     in the answer; the option is picked by kind, never by a fixed id.
//     Nothing withdraws a request on cancel, so the turn's end settles every
//     one still pending.

var _ auditSource = (*acpMapper)(nil)

// acpMapper maps an ACP agent's output lines. Its state is shared with the
// process's writers (Send registers its prompt before writing it), so it has
// a lock. Lock order: streamProcess.feedMu, then mu.
type acpMapper struct {
	mu sync.Mutex
	// d is the harness dialect; its hooks never lock or call back in.
	d acpDialect

	sessionID string
	curMode   string
	version   string
	// options are the config options the session offers now, by id, with
	// the values each takes.
	options map[string][]string
	// models are the ACP variant ids; model is the current config value.
	models []string
	model  string
	// initDemoted and sessionDemoted are the items the initialize answer
	// and the session's own reports show a mismatched shape for.
	initDemoted    []CapabilityItem
	sessionDemoted []CapabilityItem

	// reattachedPromptAfter marks the earlier daemon's open worker prompt.
	reattachedPromptAfter *uint64

	// outstanding tracks this daemon's prompt IDs. inTurn spans first
	// content through the prompt response or the unsolicited wakeup timer.
	outstanding     map[string]bool
	steerText       map[string]string
	steerOrder      []string
	recoveredSteers []string
	inTurn          bool
	unsolicitedTurn bool
	wakeStepHadText bool
	wakeSawUsage    bool
	openCalls       map[string]bool
	pendingTools    map[string]bool
	openBg          map[string]bool
	bgCalls         map[string]bool
	// cutover marks a process started under the exec cutover: its
	// run_command calls are refused, so no background command is tracked
	// (openBg, bgCalls), and requested and ranUnrequested correlate the
	// permission requests and execution evidence of its commands by tool
	// call id (Event.UnrequestedCommand).
	cutover            bool
	warnNativeSubagent bool
	requested          map[string]bool
	ranUnrequested     map[string]bool
	warnedSubagent     map[string]bool
	modelFailure       string
	// content is set while mapping one session/update that carried model
	// content (text, thought, its holder pulse, or a plan).
	content    bool
	turnSeq    int
	group      int
	groupCalls bool
	reply      strings.Builder
	replyAt    int64
	batches    batchTracker

	wakeTimer      *time.Timer
	wakeGeneration uint64
	onWakeClose    func(uint64)

	// approvals are the pending permission requests by ApprovalRequest.ID,
	// approvalOf their ids by tool call.
	approvals  map[string]*acpApproval
	approvalOf map[string]string

	// lead marks the hosted lead's mapper: only it maps native subagents
	// (the dialect's subagent launch tool) to rows; a worker's emits none.
	// nativeLaunch holds the launch calls that have not reported a final
	// status yet, so a failed launch can end its row.
	lead         bool
	ephemeral    bool
	nativeLaunch map[string]bool

	// lastContext retains the latest usage_update context so KindStatus rate
	// events preserve it instead of clearing Usage.Context.
	lastContext *wslive.UsageContext
	rate        acpOutputRateTracker
	// now is an optional local clock seam (epoch ms) for deterministic tests.
	now func() int64
}

// newACPMapper returns a mapper for session sessionID under dialect d. Its
// tool batches are tagged with the dialect's label.
func newACPMapper(sessionID string, d acpDialect) *acpMapper {
	return &acpMapper{
		d:              d,
		sessionID:      sessionID,
		outstanding:    map[string]bool{},
		steerText:      map[string]string{},
		openCalls:      map[string]bool{},
		pendingTools:   map[string]bool{},
		openBg:         map[string]bool{},
		bgCalls:        map[string]bool{},
		batches:        batchTracker{epoch: newBatchTracker(d.label()).epoch, callIDs: true},
		approvals:      map[string]*acpApproval{},
		approvalOf:     map[string]string{},
		requested:      map[string]bool{},
		ranUnrequested: map[string]bool{},
		warnedSubagent: map[string]bool{},
		nativeLaunch:   map[string]bool{},
	}
}

// acpCorrelationCap bounds the tool call ids a mapper correlates; past it
// both sets restart (ids are unique per call, so only a long process's
// oldest calls are forgotten).
const acpCorrelationCap = 4096

// newAudit returns the dialect's audit adapter (auditSource); a nil dialect
// or adapter keeps no capture.
func (m *acpMapper) newAudit(sessionID string) (string, auditAdapter) {
	if m.d == nil {
		return "", nil
	}
	return m.d.newAudit(sessionID)
}

func (m *acpMapper) setWakeClose(fn func(uint64)) {
	m.mu.Lock()
	m.onWakeClose = fn
	m.mu.Unlock()
}

func (m *acpMapper) stopWakeTimer() {
	m.mu.Lock()
	m.stopWakeTimerLocked()
	m.mu.Unlock()
}

func (m *acpMapper) stopWakeTimerLocked() {
	if m.wakeTimer != nil {
		m.wakeTimer.Stop()
		m.wakeTimer = nil
	}
}

func (m *acpMapper) armWakeTimerLocked(d time.Duration) {
	if m.onWakeClose == nil || !m.unsolicitedTurn {
		return
	}
	m.stopWakeTimerLocked()
	m.wakeGeneration++
	generation := m.wakeGeneration
	fn := m.onWakeClose
	m.wakeTimer = time.AfterFunc(d, func() { fn(generation) })
}

func (m *acpMapper) armWakeIfReadyLocked(replaying bool) {
	if replaying || !m.unsolicitedTurn || len(m.approvals) > 0 {
		return
	}
	if len(m.openCalls) > 0 && !(m.wakeStepHadText && len(m.openCalls) == len(m.openBg)) {
		return
	}
	// Read live: tests change a harness's quiet windows at run time.
	timing := m.d.timing()
	if m.wakeSawUsage {
		m.armWakeTimerLocked(timing.wakeUsageQuiet)
	} else {
		m.armWakeTimerLocked(timing.wakeIdleQuiet)
	}
}

// acpReplayObservation is the mapper's in-memory state at a holder replay's
// end: whether a prompt is still open and how many tools are pending.
type acpReplayObservation struct {
	promptOpen   bool
	pendingTools int
}

// replayState publishes the reconstructed prompt and tool state before the
// runner handles ReplayDone. An open burst without a known prompt can finish
// on the wakeup timer.
func (m *acpMapper) replayState(ev Event) []Event {
	if !ev.ReplayDone {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	promptOpen := m.reattachedPromptAfter != nil
	if m.inTurn && len(m.outstanding) == 0 && !promptOpen {
		m.unsolicitedTurn = true
		m.armWakeIfReadyLocked(false)
	}
	return []Event{{acpReplay: &acpReplayObservation{promptOpen: promptOpen, pendingTools: len(m.pendingTools)}}}
}

func (m *acpMapper) closeUnsolicited(now int64, generation uint64) []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	// A replaced timer may already be waiting to acquire the feed lock.
	if m.wakeTimer == nil || generation != m.wakeGeneration {
		return nil
	}
	if !m.unsolicitedTurn || !m.inTurn || len(m.outstanding) > 0 || len(m.approvals) > 0 {
		return nil
	}
	if len(m.openCalls) > 0 && !(m.wakeStepHadText && len(m.openCalls) == len(m.openBg)) {
		return nil
	}
	return m.endTurn(acpMessage{}, now)
}

func (m *acpMapper) session() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionID
}

// offers reports whether the session offers config option id now; before
// a session reported its options, it does.
func (m *acpMapper) offers(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.options[id]
	return m.options == nil || ok
}

// offersValue reports whether option id takes value now; before a session
// reported its options, it does.
func (m *acpMapper) offersValue(id, value string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.options == nil || slices.Contains(m.options[id], value)
}

func (m *acpMapper) modelName() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.model
}

// defaultModelEffort selects an offered variant when the caller chose a
// base model from Choices without an explicit effort. Prefer the current
// variant, otherwise the harness's own offered order.
func (m *acpMapper) defaultModelEffort(base string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, model := range append(append([]string{m.model}, m.options["model"]...), m.models...) {
		if name, effort := m.d.baseModel(model); name == base && effort != "" {
			return effort
		}
	}
	return ""
}

func (m *acpMapper) hasModel(model string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Contains(m.models, model)
}

func (m *acpMapper) mode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.curMode
}

func (m *acpMapper) promptSent(id string) { m.promptSentText(id, "") }

func (m *acpMapper) promptSentText(id, text string) {
	m.mu.Lock()
	if m.unsolicitedTurn {
		m.rate.dropUntracked()
	}
	m.unsolicitedTurn = false
	m.wakeSawUsage = false
	m.stopWakeTimerLocked()
	// A second prompt while one is outstanding is a steer even when the agent
	// has not emitted the first session update yet. The runner can submit mail as
	// soon as its turn starts, before that provider update arrives.
	if len(m.outstanding) > 0 {
		m.steerText[id] = text
		m.steerOrder = append(m.steerOrder, id)
	}
	m.outstanding[id] = true
	m.rate.promptSent(id)
	m.mu.Unlock()
}

func (m *acpMapper) promptDropped(id string) {
	m.mu.Lock()
	delete(m.outstanding, id)
	delete(m.steerText, id)
	m.steerOrder = slices.DeleteFunc(m.steerOrder, func(steerID string) bool { return steerID == id })
	m.rate.promptDropped(id)
	m.mu.Unlock()
}

// answerFor is the raw request id and option id that answer approval id. A
// denial is reject_once only: reject_always would make the agent refuse
// that tool for the rest of the session, past the one request denied. A
// request without reject_once gets no answer (AnswerApproval:
// errNoApprovalAnswer) and stays for the user.
func (m *acpMapper) answerFor(id string, allow bool) (json.RawMessage, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.approvals[id]
	if a == nil {
		return nil, "", fmt.Errorf("%s has no pending permission request %q", m.d.label(), id)
	}
	kinds := []string{"reject_once"}
	if allow {
		kinds = []string{"allow_once", "allow_always"}
	}
	for _, kind := range kinds {
		for _, o := range a.options {
			if o.Kind == kind {
				return a.raw, o.OptionID, nil
			}
		}
	}
	return nil, "", fmt.Errorf("%s's permission request %q offers no %s option", m.d.label(), id, kinds[0])
}

func (m *acpMapper) mapLine(line []byte, replies lineReplies) []Event {
	return m.mapLineSeq(line, replies, 0)
}

// mapLineSeq maps one line with its holder sequence number (seqLineMapper):
// a prompt response from before a reattached prompt cannot end it.
func (m *acpMapper) mapLineSeq(line []byte, replies lineReplies, seq uint64) []Event {
	var msg acpMessage
	if json.Unmarshal(line, &msg) != nil {
		return nil
	}
	now := time.Now().UnixMilli()
	if m.now != nil {
		now = m.now()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case msg.Method != "" && hasID:
		return m.request(msg, replies, now)
	case msg.Method == "session/update":
		oldFailure := m.modelFailure
		m.content = false
		out := m.update(msg.Params, replies, now)
		if _, replaying := replies.(silentReplies); !replaying {
			n := len(m.pendingTools)
			out = append(out, Event{ModelActivity: &n, ModelContent: m.content})
			if oldFailure != m.modelFailure {
				if oldFailure != "" && m.modelFailure == "" {
					out = append(out, Event{Notice: m.d.label() + " recovered after in-band model failure: " + oldFailure})
				}
				failure := m.modelFailure
				out = append(out, Event{ModelFailureUpdate: &failure})
			}
		}
		return out
	case msg.Method == "" && hasID:
		return m.response(msg, replies, now, seq)
	}
	return nil
}

// request maps an agent request: a permission request waits for the
// user; any other gets an error answer, so the agent never waits on it.
func (m *acpMapper) request(msg acpMessage, replies lineReplies, now int64) []Event {
	if msg.Method != "session/request_permission" {
		replies.send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "method not found: " + msg.Method}})
		return []Event{{Notice: m.d.label() + " asked for " + msg.Method + ", which ws does not serve"}}
	}
	var p struct {
		ToolCall struct {
			ToolCallID string          `json:"toolCallId"`
			Name       string          `json:"name"`
			Kind       string          `json:"kind"`
			Title      string          `json:"title"`
			Args       json.RawMessage `json:"args"`
			RawInput   json.RawMessage `json:"rawInput"`
		} `json:"toolCall"`
		Options []acpOption `json:"options"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	raw := compactRaw(msg.ID)
	id := p.ToolCall.ToolCallID
	if id == "" {
		id = "rpc:" + string(raw)
	}
	_, replaying := replies.(silentReplies)
	if m.ephemeral {
		// Ephemeral sessions have no interactive approver: deny immediately so
		// ACP permission requests never stall until caller Timeout.
		outcome := map[string]any{"outcome": "cancelled"}
		for _, kind := range []string{"reject_once", "reject_always"} {
			found := false
			for _, o := range p.Options {
				if o.Kind == kind {
					outcome = map[string]any{"outcome": "selected", "optionId": o.OptionID}
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		replies.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      raw,
			"result":  map[string]any{"outcome": outcome},
		})
		return []Event{{Notice: m.d.label() + " ephemeral session auto-denied permission request for " + cmp.Or(p.ToolCall.Name, p.ToolCall.Title)}}
	}
	m.rate.breakRun(replaying)
	out := m.startTurn(nil, replaying)
	m.wakeSawUsage = false
	m.stopWakeTimerLocked()
	m.approvals[id] = &acpApproval{raw: raw, toolCallID: p.ToolCall.ToolCallID, options: p.Options}
	if p.ToolCall.ToolCallID != "" {
		m.approvalOf[p.ToolCall.ToolCallID] = id
		if len(m.requested) >= acpCorrelationCap {
			clear(m.requested)
			clear(m.ranUnrequested)
		}
		m.requested[p.ToolCall.ToolCallID] = true
	}
	input := p.ToolCall.Args
	if len(input) == 0 {
		input = p.ToolCall.RawInput
	}
	tool := p.ToolCall.Name
	if tool == "" {
		tool = p.ToolCall.Title
	}
	return append(out, Event{Approval: &ApprovalRequest{ID: id, Tool: tool, Kind: p.ToolCall.Kind, Input: input, Description: p.ToolCall.Title, ToolUseID: p.ToolCall.ToolCallID, StartedAt: now}})
}

// response maps the answer to a request ws wrote: a prompt's ends the turn
// unless a later prompt took the run over; any other answers its waiter
// and reports what the handshake learned.
func (m *acpMapper) response(msg acpMessage, replies lineReplies, now int64, seq uint64) []Event {
	var id string
	if json.Unmarshal(msg.ID, &id) != nil {
		return nil
	}
	kind, ok := acpIDKind(id)
	if !ok {
		return nil
	}
	if kind == acpKindPrompt {
		_, replaying := replies.(silentReplies)
		ours := m.outstanding[id]
		delete(m.outstanding, id)
		reattached := m.reattachedPromptAfter != nil && seq > *m.reattachedPromptAfter
		var steerEvents []Event
		tookSteer := false
		for i, steerID := range m.steerOrder {
			if m.outstanding[steerID] {
				text := m.steerText[steerID]
				delete(m.steerText, steerID)
				m.steerOrder = slices.Delete(m.steerOrder, i, i+1)
				tookSteer = true
				if text != "" {
					steerEvents = append(steerEvents, Event{SteerAnswer: &SteerAnswer{}}, Event{UserTaken: text})
				}
				break
			}
		}
		// Responses from before the open prompt cannot acknowledge its steers.
		recoveredSteer := !tookSteer && len(m.recoveredSteers) > 0 && (m.reattachedPromptAfter == nil || reattached)
		if recoveredSteer {
			text := m.recoveredSteers[0]
			m.recoveredSteers = m.recoveredSteers[1:]
			steerEvents = append(steerEvents, Event{SteerAnswer: &SteerAnswer{}}, Event{UserTaken: text})
		}
		var rateEv *Event
		if rate, ok := m.rate.complete(id, msg, replaying); ok {
			rateCopy := rate
			rateEv = &Event{Live: &wslive.Event{
				Kind:       wslive.KindStatus,
				Context:    cloneUsageContext(m.lastContext),
				OutputTps:  &rateCopy,
				OutputRate: &wslive.OutputRate{OutputTps: rateCopy, At: now},
			}}
		}
		if len(m.outstanding) > 0 || (reattached && recoveredSteer) || (!ours && !reattached && !m.inTurn) {
			if rateEv != nil {
				return append(steerEvents, *rateEv)
			}
			return steerEvents
		}
		if reattached {
			m.reattachedPromptAfter = nil
		}
		out := m.endTurn(msg, now)
		if rateEv != nil {
			// Emit KindStatus before TurnEnd so the runner updates telemetry
			// while the turn is active. Mark this exact pair so file-only
			// recovery persists both in TurnEnd's single snapshot.
			if n := len(out); n > 0 && out[n-1].TurnEnd {
				turnEnd := out[n-1]
				rateEv.LiveCoalescesTurnEnd = true
				out = append(out[:n-1], *rateEv, turnEnd)
			} else {
				out = append(out, *rateEv)
			}
		}
		return append(steerEvents, out...)
	}
	a := controlAnswer{ok: msg.Error == nil, body: msg.Result}
	if msg.Error != nil {
		a.err = msg.Error.Message
	}
	replies.answer(id, a)
	if msg.Error != nil {
		return nil
	}
	switch kind {
	case acpKindInit:
		return m.initialized(msg.Result)
	case acpKindNew, acpKindResume:
		return m.opened(msg.Result)
	case acpKindConfig:
		m.configOptions(msg.Result)
	}
	return nil
}
