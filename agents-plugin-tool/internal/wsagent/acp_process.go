package wsagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// acp_process.go is the shared ACP agent process: the handshake, the
// session request, prompts, and controls. What differs between ACP harnesses
// comes from the mapper's dialect (p.m.d), which never takes a lock or calls
// back into the process.

// frameCaptureNoter is a process whose holder may keep a frame capture.
type frameCaptureNoter interface {
	NoteFrameCapture(text string) error
}

// NoteFrameCapture adds text to the holder's frame capture, when the holder
// keeps one for this process.
func (p *acpProcess) NoteFrameCapture(text string) error {
	if p.holder == nil {
		return nil
	}
	return p.holder.noteCapture(text)
}

// acpProcess is one ACP agent process serving one session.
type acpProcess struct {
	*streamProcess
	m *acpMapper
	// epoch keeps this process's request ids apart from an earlier
	// daemon's on the same held process.
	epoch string
	seq   atomic.Int64
	// effort is the thought level the conversation asked for last.
	effort        atomic.Pointer[string]
	model         atomic.Pointer[string]
	worker        bool
	ephemeral     bool
	ephemeralSpec StartSpec
}

func (p *acpProcess) processControls() processControls {
	// A steer is a new session/prompt that ends the running one (Steer).
	controls := processControls{Process: p, approval: p, mode: p, steer: p, replacesTurn: true}
	if p.worker {
		controls.reports = p
	}
	if p.ephemeral {
		controls.reset = p
	}
	return controls
}

func (p *acpProcess) Close() error {
	p.m.stopWakeTimer()
	return p.streamProcess.Close()
}

// closeUnsolicitedTurn settles a self-resumed background-wakeup turn once the
// stream goes quiet after its final update, serialized with streamProcess.feed.
func (p *acpProcess) closeUnsolicitedTurn(generation uint64) {
	p.feedMu.Lock()
	defer p.feedMu.Unlock()
	if p.finished {
		return
	}
	for _, ev := range p.m.closeUnsolicited(time.Now().UnixMilli(), generation) {
		p.emit(ev)
	}
}

func (p *acpProcess) newID(kind string) string {
	return acpRequestMark + "-" + kind + "-" + p.epoch + "-" + strconv.FormatInt(p.seq.Add(1), 10)
}

// call sends one request and waits for its result.
func (p *acpProcess) call(kind, method string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
	return p.callContext(context.Background(), kind, method, params, timeout)
}

func (p *acpProcess) callContext(ctx context.Context, kind, method string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
	return p.requestBodyIDContext(ctx, p.newID(kind), method, map[string]any{"method": method, "params": params}, timeout)
}

// open runs a new process's handshake.
func (p *acpProcess) open(spec StartSpec) error {
	version := ""
	if spec.Lead != nil {
		version = spec.Lead.DaemonVersion
	}
	init, err := p.call(acpKindInit, "initialize", map[string]any{
		"protocolVersion": 1,
		// Without fs or terminal capabilities the agent runs its own tools.
		"clientCapabilities": map[string]any{"fs": map[string]any{"readTextFile": false, "writeTextFile": false}, "terminal": false},
		"clientInfo":         map[string]any{"name": "ws", "title": "ws", "version": version},
	}, p.m.d.timing().init)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	var reply struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if json.Unmarshal(init, &reply) != nil || reply.ProtocolVersion != 1 {
		return fmt.Errorf("%s answered initialize with ACP protocol version %d; ws speaks version 1", p.m.d.label(), reply.ProtocolVersion)
	}
	return p.openSession(context.Background(), spec)
}

func (p *acpProcess) openSession(ctx context.Context, spec StartSpec) error {
	mcpServers := []any{}
	if !spec.Ephemeral && spec.MCPSession != nil {
		mcpServers = []any{acpHTTPServer(spec.MCPSession)}
	}
	params := map[string]any{"cwd": spec.workDir(), "mcpServers": mcpServers}
	var appendText string
	if spec.Instructions != "" {
		text, err := os.ReadFile(spec.Instructions)
		if err != nil {
			return fmt.Errorf("read the instruction file: %w", err)
		}
		appendText = string(text)
	}
	// An ephemeral session appends its caller's system prompt; any other the
	// dialect's own session rule.
	extra := ""
	if spec.Ephemeral {
		extra = strings.TrimSpace(spec.SystemPrompt)
	} else {
		extra = p.m.d.instructions(spec)
	}
	if extra != "" {
		if trimmed := strings.TrimRight(appendText, "\n"); trimmed != "" {
			appendText = trimmed + "\n\n" + extra + "\n"
		} else {
			appendText = extra + "\n"
		}
	}
	if appendText != "" {
		params["_meta"] = p.m.d.openMeta(appendText)
	}
	kind, method := acpKindNew, "session/new"
	if !spec.Ephemeral && spec.SessionID != "" {
		kind, method = acpKindResume, "session/resume"
		params["sessionId"] = spec.SessionID
	}
	if _, err := p.callContext(ctx, kind, method, params, p.m.d.timing().session); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if !spec.Ephemeral && spec.MCPSession != nil {
		if c := spec.MCPSession.Connected; c != nil {
			wait := p.m.d.timing().mcpConnect
			select {
			case <-c:
			case <-p.exited:
				return fmt.Errorf("%s exited while it opened its session", p.m.d.label())
			case <-time.After(wait):
				return fmt.Errorf("%s did not reach the ws MCP route within %s of opening its session (a refused token only hides the ws tools), so the process was not started without them", p.m.d.label(), wait)
			}
		}
	}
	effort := spec.Effort
	if stored := p.effort.Load(); stored != nil && *stored != "" {
		effort = *stored
	}
	if effort != "" {
		p.effort.Store(&effort)
	}
	model := spec.Model
	if stored := p.model.Load(); stored != nil && *stored != "" {
		model = *stored
	}
	if model != "" {
		if err := p.SetModel(model); err != nil {
			return fmt.Errorf("model: %w", err)
		}
	}
	if model == "" && effort != "" {
		if err := p.SetEffort(effort); err != nil {
			return fmt.Errorf("effort: %w", err)
		}
	}
	mode := ""
	if spec.Ephemeral {
		// Most restrictive non-plan native mode: asks permission on tool calls
		// so acpMapper's ephemeral auto-deny can reject them without stalling.
		mode = p.m.d.cutoverMode()
	} else if spec.Lead != nil {
		mode = spec.Lead.PermissionMode
	}
	if spec.ExecCutover {
		// A cutover worker too: a resumed session reopens in whatever
		// mode it last ran in, and native auto runs commands unasked.
		mode = p.m.d.cutoverMode()
	}
	if mode != "" && mode != p.m.mode() {
		if err := p.SetPermissionMode(mode); err != nil {
			return fmt.Errorf("permission mode: %w", err)
		}
	}
	return nil
}

// Reset closes the prior ACP session on the resident ephemeral process and
// opens a fresh session via session/new, rebinding the mapper to the new id.
func (p *acpProcess) Reset(ctx context.Context) error {
	if !p.ephemeral {
		return ErrUnsupported
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if prev := p.m.session(); prev != "" {
		_, _ = p.callContext(ctx, acpKindClose, "session/close", map[string]any{"sessionId": prev}, p.m.d.timing().control)
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return p.openSession(ctx, p.ephemeralSpec)
}

// acpHTTPServer is the ws MCP server entry of a session request: the
// daemon's route with the session's bearer token.
func acpHTTPServer(s *MCPSession) map[string]any {
	return map[string]any{
		"type":    "http",
		"name":    ReservedMCPServer,
		"url":     s.URL,
		"headers": []any{map[string]any{"name": "Authorization", "value": "Bearer " + s.Token}},
	}
}

// Send submits a user turn as a session/prompt; sent while a turn runs it
// steers that turn.
func (p *acpProcess) Send(text string) error {
	return p.sendPrompt(text, text)
}

func (p *acpProcess) sendPrompt(text, acknowledgedText string) error {
	sid := p.m.session()
	if sid == "" {
		return fmt.Errorf("the %s session is not open", p.m.d.label())
	}
	id := p.newID(acpKindPrompt)
	p.m.promptSentText(id, acknowledgedText)
	err := p.writeLine(map[string]any{"jsonrpc": "2.0", "id": id, "method": "session/prompt", "params": map[string]any{
		"sessionId": sid,
		"prompt":    []any{map[string]any{"type": "text", "text": text}},
	}})
	if err != nil {
		p.m.promptDropped(id)
	}
	return err
}

// Steer writes text as another prompt: the agent ends the running prompt
// and carries its run on under this one.
func (p *acpProcess) Steer(text string) error { return p.Send(text) }

func (p *acpProcess) steerCompaction(text string) (string, error) {
	return "", p.sendPrompt(text, "")
}

// ClearSteered is a no-op for ACP. Its acknowledgement is the prior prompt's
// end_turn response, which also proves the replacement prompt took the run;
// the runner receives SteerAnswer and UserTaken together.
func (p *acpProcess) ClearSteered() error { return nil }

// Interrupt cancels the running turn; its prompt answers "cancelled".
func (p *acpProcess) Interrupt() error {
	return p.writeLine(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": p.m.session()}})
}

// SetModel sets the session's model config option. ACP has no value for
// "the default", so an empty model restarts the process instead. A model
// that offers a thought level gets the conversation's again: the agent may
// reset it on a switch.
func (p *acpProcess) SetModel(model string) error {
	if model == "" {
		return ErrUnsupported
	}
	base, suffix := p.m.d.baseModel(model)
	if suffix != "" && p.effort.Load() == nil {
		p.effort.Store(&suffix)
	}
	if !p.m.offersValue("model", base) {
		if p.effort.Load() == nil {
			if level := p.m.defaultModelEffort(base); level != "" {
				p.effort.Store(&level)
			}
		}
		if effort := p.effort.Load(); effort != nil {
			if ok, err := p.setModelVariant(base, *effort); ok || err != nil {
				if err == nil {
					p.model.Store(&base)
				}
				return err
			}
		}
	}
	if err := p.setConfig("model", base); err != nil {
		return err
	}
	p.model.Store(&base)
	if effort := p.effort.Load(); effort != nil && *effort != "" {
		return p.SetEffort(*effort)
	}
	return nil
}

// SetEffort uses the dialect's effort option when offered, otherwise a model
// variant. A worker fails rather than silently running at another tier's
// effort. A lead keeps an unavailable effort for its next model, as before.
func (p *acpProcess) SetEffort(effort string) error {
	if effort == "" {
		return ErrUnsupported
	}
	p.effort.Store(&effort)
	d := p.m.d
	if option := d.effortOption(); p.m.offersValue(option, effort) {
		if err := p.setConfig(option, effort); err != nil {
			return err
		}
		p.emit(Event{Notice: d.label() + " honored effort " + effort + " via " + option})
		return nil
	}
	model := p.m.modelName()
	if requested := p.model.Load(); requested != nil {
		model = *requested
	}
	base, _ := d.baseModel(model)
	if ok, err := p.setModelVariant(base, effort); ok || err != nil {
		return err
	}
	if p.worker {
		return fmt.Errorf("%s offers neither %s %q nor model variant %q", d.label(), d.effortOption(), effort, d.modelVariant(base, effort))
	}
	p.emit(Event{Notice: d.label() + " did not offer effort " + effort + "; retained for the next model"})
	return nil
}

func (p *acpProcess) setModelVariant(base, effort string) (bool, error) {
	d := p.m.d
	variant := d.modelVariant(base, effort)
	if p.m.offersValue("model", variant) {
		if err := p.setConfig("model", variant); err != nil {
			return true, err
		}
	} else if p.m.hasModel(variant) {
		body, err := p.call(acpKindConfig, "session/set_model", map[string]any{"sessionId": p.m.session(), "modelId": variant}, d.timing().control)
		if err != nil {
			return true, err
		}
		var reply struct {
			Models struct {
				CurrentModelID string `json:"currentModelId"`
			} `json:"models"`
		}
		_ = json.Unmarshal(body, &reply)
		if got := reply.Models.CurrentModelID; got != "" && got != variant {
			return true, fmt.Errorf("%s kept model %q after it was set to %q", d.label(), got, variant)
		}
	} else {
		return false, nil
	}
	p.emit(Event{Notice: d.label() + " honored effort " + effort + " via suffixed model id " + variant})
	return true, nil
}

// setConfig sets one config option and checks that the agent took the
// value: a value it does not apply comes back unchanged rather than refused.
func (p *acpProcess) setConfig(option, value string) error {
	body, err := p.call(acpKindConfig, "session/set_config_option", map[string]any{"sessionId": p.m.session(), "configId": option, "value": value}, p.m.d.timing().control)
	if err != nil {
		return err
	}
	if got, ok := acpConfigValue(body, option); ok && got != value {
		return fmt.Errorf("%s kept %s %q after it was set to %q", p.m.d.label(), option, got, value)
	}
	return nil
}

// SetPermissionMode sets the session's mode.
func (p *acpProcess) SetPermissionMode(mode string) error {
	modes := p.m.d.permissionModes()
	if mode == "bypass" || !slices.Contains(modes, mode) {
		return fmt.Errorf("%s has no permission mode %q; its modes are %s", p.m.d.label(), mode, strings.Join(modes, ", "))
	}
	_, err := p.call(acpKindMode, "session/set_mode", map[string]any{"sessionId": p.m.session(), "modeId": mode}, p.m.d.timing().control)
	return err
}

// AnswerApproval answers a permission request with the option of the
// matching kind. ACP carries no denial message. A request it cannot answer
// (no such option, or no longer held) writes nothing: errNoApprovalAnswer.
func (p *acpProcess) AnswerApproval(req ApprovalRequest, allow bool, _ string) error {
	raw, option, err := p.m.answerFor(req.ID, allow)
	if err != nil {
		return fmt.Errorf("%v: %w", err, errNoApprovalAnswer)
	}
	return p.writeLine(map[string]any{"jsonrpc": "2.0", "id": raw, "result": map[string]any{
		"outcome": map[string]any{"outcome": "selected", "optionId": option},
	}})
}

// forgetNativeLaunches drops the launch correlation of native rows the
// runner settled (cancellation, a replay's end, an accepted turn end): no
// launch failure can change a settled row, so a launch that never reports
// a final status leaves nothing behind. A watchdog continuation settles
// nothing, so a launch failure reported after it still ends its row.
func (p *acpProcess) forgetNativeLaunches(rowIDs []string) {
	p.m.mu.Lock()
	defer p.m.mu.Unlock()
	for _, id := range rowIDs {
		delete(p.m.nativeLaunch, strings.TrimPrefix(id, p.m.d.nativeRowPrefix()))
	}
}
