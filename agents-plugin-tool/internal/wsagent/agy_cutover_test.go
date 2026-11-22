package wsagent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// newAgyCutoverHarness is an agy lead harness whose backend carries the
// real exec cutover support.
func newAgyCutoverHarness(t *testing.T) (*leadHarness, *agyFake) {
	t.Helper()
	h, f := newAgyLeadHarness(t)
	agy := h.opts.Backends[BackendAgy]
	_, real := AgyBackend{}.registration()
	agy.ExecCutover = real.ExecCutover
	agy.WorkerAutoApprove = true
	h.opts.Backends[BackendAgy] = agy
	return h, f
}

func agyRunCommandRequest(rpcID, callID string) []string {
	return []string{
		// The in_progress announcement precedes the request.
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call","toolCallId":"` + callID + `","name":"run_command","status":"in_progress","args":{"CommandLine":"ls","IsDaemon":false}}}}`,
		`{"jsonrpc":"2.0","method":"session/request_permission","params":{"sessionId":"agy-s1","toolCall":{"toolCallId":"` + callID + `","name":"run_command","title":"ls","args":{"CommandLine":"ls"}},"options":[{"optionId":"allow","kind":"allow_once"},{"optionId":"deny","kind":"reject_once"}]},"id":"` + rpcID + `"}`,
	}
}

func agyRefusedUpdate(callID string) string {
	return `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"` + callID + `","name":"run_command","status":"failed","rawOutput":{"output":""},"content":[{"type":"content","content":{"type":"text","text":""}}]}}}`
}

func agyEndTurn(h *leadHarness, holder string, n int) string {
	return `{"jsonrpc":"2.0","result":{"stopReason":"end_turn"},"id":"` + agyPromptIDs(h, holder)[n] + `"}`
}

// answered reports whether the process was answered rpc id with option.
func agyAnswered(h *leadHarness, holder, rpcID, option string) bool {
	return slices.ContainsFunc(h.holders.inputs(holder, ""), func(line string) bool {
		return strings.Contains(line, `"id":"`+rpcID+`"`) && strings.Contains(line, `"optionId":"`+option+`"`)
	})
}

// agyCutoverPrompts are the texts of the prompts written to the process.
func agyCutoverPrompts(t *testing.T, h *leadHarness, holder string) []string {
	t.Helper()
	var out []string
	for _, p := range h.holders.acpInputs(holder, "session/prompt") {
		var v struct {
			Prompt []struct{ Text string } `json:"prompt"`
		}
		_ = json.Unmarshal(p, &v)
		for _, part := range v.Prompt {
			out = append(out, part.Text)
		}
	}
	return out
}

func countInstruction(texts []string) int {
	n := 0
	for _, text := range texts {
		if strings.Contains(text, agyExecCutoverInstruction) {
			n++
		}
	}
	return n
}

func agyModes(t *testing.T, h *leadHarness, holder string) []string {
	t.Helper()
	var modes []string
	for _, p := range h.holders.acpInputs(holder, "session/set_mode") {
		modes = append(modes, acpField(t, p, "modeId").(string))
	}
	return modes
}

// A cutover agy lead launches in native default whatever mode it saved,
// refuses run_command before anyone sees it (under bypass too), allows its
// other requests as bypass does, and sends the instruction once at the end
// of the turn whose command it refused, even though agy ended the turn
// with an empty reply. Live mode changes and in-stream reports return to
// default; the saved preference stays on record.
func TestAgyCutoverDefaultModeRejectOnceAndTurnEndInstruction(t *testing.T) {
	h, f := newAgyCutoverHarness(t)
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{PermissionMode: "auto"}).ID
	f.replyID = id
	h.send(id, "hello")
	holder := h.holder(id)
	eventually(t, "the prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	rec := h.record(id)
	if rec.ExecCutoverHolder != holder || rec.PermissionMode != "auto" {
		t.Fatalf("record: cutover holder %q (holder %q), saved mode %q", rec.ExecCutoverHolder, holder, rec.PermissionMode)
	}
	if modes := agyModes(t, h, holder); slices.Contains(modes, "auto") {
		t.Fatalf("the saved mode was applied: %v", modes)
	}
	news := h.holders.acpInputs(holder, "session/new")
	if text, _ := acpField(t, news[0], "_meta", "antigravity", "systemPrompt", "append").(string); !strings.Contains(text, agyExecCutoverInstruction) {
		t.Fatalf("session/new lacks the exec instruction: %q", text)
	}

	h.holders.out(holder, agyRunCommandRequest("1", "c1")...)
	eventually(t, "the refusal", func() bool { return agyAnswered(h, holder, "1", "deny") })
	if st := h.state(id); len(st.Approvals) != 0 {
		t.Fatalf("the refused request was shown: %+v", st.Approvals)
	}
	// A second refused command in the same turn owes no second prompt.
	h.holders.out(holder, agyRefusedUpdate("c1"))
	h.holders.out(holder, agyRunCommandRequest("2", "c2")...)
	eventually(t, "the second refusal", func() bool { return agyAnswered(h, holder, "2", "deny") })
	h.holders.out(holder, agyRefusedUpdate("c2"), agyEndTurn(h, holder, 0))
	eventually(t, "the instruction prompt", func() bool { return countInstruction(agyCutoverPrompts(t, h, holder)) == 1 })
	h.holders.out(holder, agyEndTurn(h, holder, 1))
	h.idle(id)
	if n := countInstruction(agyCutoverPrompts(t, h, holder)); n != 1 {
		t.Fatalf("instruction prompts = %d", n)
	}

	// A live settings change keeps the saved preference and default mode.
	if _, err := h.runner.SetPermissionMode(id, "accept-edits"); err != nil {
		t.Fatal(err)
	}
	h.quiesce(id)
	if modes := agyModes(t, h, holder); slices.Contains(modes, "accept-edits") || slices.Contains(modes, "auto") {
		t.Fatalf("a live change moved the process off default: %v", modes)
	}
	if rec := h.record(id); rec.PermissionMode != "accept-edits" {
		t.Fatalf("saved preference = %q", rec.PermissionMode)
	}
	// agy reporting another mode is undone at once.
	before := len(agyModes(t, h, holder))
	h.holders.out(holder, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"current_mode_update","currentModeId":"auto"}}}`)
	eventually(t, "the mode reconciled", func() bool {
		modes := agyModes(t, h, holder)
		return len(modes) > before && modes[len(modes)-1] == "default"
	})
	if rec := h.record(id); rec.PermissionMode != "accept-edits" {
		t.Fatalf("an in-stream report replaced the saved preference: %q", rec.PermissionMode)
	}

}

// Under ws bypass a cutover lead still refuses run_command, while bypass
// allows its other requests; a run_command request waiting when the mode
// moves to bypass is refused, never allowed.
func TestAgyCutoverBypassStillRefusesRunCommand(t *testing.T) {
	h, f := newAgyCutoverHarness(t)
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{PermissionMode: "bypass"}).ID
	f.replyID = id
	h.send(id, "hello")
	holder := h.holder(id)
	eventually(t, "the prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.holders.out(holder, agyRunCommandRequest("3", "c3")...)
	h.holders.out(holder, `{"jsonrpc":"2.0","method":"session/request_permission","params":{"sessionId":"agy-s1","toolCall":{"toolCallId":"w1","name":"write_to_file","title":"write"},"options":[{"optionId":"allow","kind":"allow_once"},{"optionId":"deny","kind":"reject_once"}]},"id":"4"}`)
	eventually(t, "bypass answers", func() bool { return agyAnswered(h, holder, "3", "deny") && agyAnswered(h, holder, "4", "allow") })
	if agyAnswered(h, holder, "3", "allow") {
		t.Fatal("bypass allowed run_command")
	}
	h.holders.out(holder, agyRefusedUpdate("c3"), agyEndTurn(h, holder, 0))
	eventually(t, "the instruction", func() bool { return countInstruction(agyCutoverPrompts(t, h, holder)) == 1 })
}

// A command that ran with no request (an allow rule of the user's own,
// say) warns and owes the same single instruction; an announcement alone,
// or a refused call's empty failure, is no evidence.
func TestAgyCutoverUnrequestedCommandWarnsAndInstructsOnce(t *testing.T) {
	h, f := newAgyCutoverHarness(t)
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.send(id, "hello")
	holder := h.holder(id)
	eventually(t, "the prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call","toolCallId":"u0","name":"run_command","status":"in_progress","args":{"CommandLine":"true"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"u0","name":"run_command","status":"failed","rawOutput":{"output":""}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call","toolCallId":"u1","name":"run_command","status":"in_progress","args":{"CommandLine":"ls"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"u1","name":"run_command","status":"completed","rawOutput":{"output":"Command exited with code 0.","exit_code":0}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call","toolCallId":"u2","name":"run_command","status":"in_progress","args":{"CommandLine":"pwd"}}}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"u2","name":"run_command","status":"completed","rawOutput":{"output":"/work","exit_code":0}}}}`,
		agyEndTurn(h, holder, 0),
	)
	eventually(t, "the instruction prompt", func() bool { return countInstruction(agyCutoverPrompts(t, h, holder)) == 1 })
	if n := strings.Count(h.log.String(), "a native command ran without a permission request"); n != 2 {
		t.Fatalf("warnings = %d: %s", n, h.log.String())
	}
	if strings.Contains(h.log.String(), "(u0)") {
		t.Fatal("a refused call's empty failure counted as a run")
	}
	h.holders.out(holder, agyEndTurn(h, holder, 1))
	h.idle(id)

	// A daemon restart replays the ring: the turns are seen again, and
	// the instruction is not sent a second time.
	h.runner.Close()
	h.holders.restart()
	h.startAt("http://127.0.0.1:1")
	h.quiesce(id)
	h.idle(id)
	if n := countInstruction(agyCutoverPrompts(t, h, holder)); n != 1 {
		t.Fatalf("instruction prompts after the replay = %d", n)
	}
}

// A cutover agy worker's run_command is refused ahead of worker
// auto-approval, which still answers its other requests.
func TestAgyCutoverWorkerRefusesRunCommandBeforeAutoApproval(t *testing.T) {
	m := newAgyMapper("s")
	m.cutover = true
	rr := &agyRun{m: m, answers: map[string]controlAnswer{}}
	events := m.mapLine([]byte(agyRunCommandRequest("9", "k1")[0]), rr)
	events = append(events, m.mapLine([]byte(agyRunCommandRequest("9", "k1")[1]), rr)...)
	var approval *ApprovalRequest
	for _, ev := range events {
		if ev.Approval != nil {
			approval = ev.Approval
		}
	}
	if approval == nil || approval.Tool != agyCommandTool {
		t.Fatalf("events = %+v", events)
	}
	if len(m.openBg) != 0 {
		t.Fatalf("a cutover mapper tracks run_command as background: %v", m.openBg)
	}
	c := &conv{rec: Record{Backend: BackendAgy}, execCutover: true}
	c.r = &Runner{opts: Options{Backends: map[string]BackendRegistration{BackendAgy: {ExecCutover: &ExecCutoverSupport{RejectTools: []string{agyCommandTool}}, WorkerAutoApprove: true}}}}
	if !c.cutoverRejects(approval.Tool) || c.cutoverRejects("write_to_file") {
		t.Fatal("cutoverRejects")
	}
	c.execCutover = false
	if c.cutoverRejects(approval.Tool) {
		t.Fatal("a process outside the cutover refuses run_command")
	}
}

// A run_command request the process made while no daemon ran is replayed
// to the next one, which refuses it at the rejoin's end, keeps the
// rejoined process in native default whatever mode was saved, and sends
// the turn's instruction once.
func TestAgyCutoverRefusesAReplayedRequestAtRejoin(t *testing.T) {
	h, f := newAgyCutoverHarness(t)
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{PermissionMode: "plan"}).ID
	f.replyID = id
	h.send(id, "hello")
	holder := h.holder(id)
	eventually(t, "the prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.idleOrQuiet(id)
	h.runner.Close()
	h.holders.out(holder, agyRunCommandRequest("5", "r1")...)
	h.holders.restart()
	h.startAt("http://127.0.0.1:1")
	eventually(t, "the replayed refusal", func() bool { return agyAnswered(h, holder, "5", "deny") })
	if agyAnswered(h, holder, "5", "allow") {
		t.Fatal("the replayed request was allowed")
	}
	// A mode report inside the turn is undone at the turn's end.
	h.holders.out(holder, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"current_mode_update","currentModeId":"plan"}}}`)
	h.holders.out(holder, agyRefusedUpdate("r1"), agyEndTurn(h, holder, 0))
	eventually(t, "the instruction", func() bool { return countInstruction(agyCutoverPrompts(t, h, holder)) == 1 })
	eventually(t, "the rejoined mode reconciled", func() bool {
		modes := agyModes(t, h, holder)
		return len(modes) > 0 && modes[len(modes)-1] == "default"
	})
	if rec := h.record(id); rec.PermissionMode != "plan" {
		t.Fatalf("saved preference = %q", rec.PermissionMode)
	}
}

// idleOrQuiet waits until the loop has settled its current step.
func (h *leadHarness) idleOrQuiet(id string) { h.quiesce(id) }
