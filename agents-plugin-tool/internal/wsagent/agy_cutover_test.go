package wsagent

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
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
	seedHarnessVersions(t, h, map[string]string{BackendAgy: real.ExecCutover.Since})
	h.execJobs = newFakeExecJobs()
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

// A cutover agy mapper does not track run_command as a background shell.
func TestAgyCutoverMapperSkipsBackgroundTracking(t *testing.T) {
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
}

// A cutover agy worker's run_command is refused ahead of worker
// auto-approval, which still answers its other requests. The refused
// turn's empty reply is not its report: the parent hears from the worker
// once the turn the instruction starts ends.
func TestAgyCutoverWorkerRefusesRunCommandAndHoldsItsReport(t *testing.T) {
	h, got, _, holder := startAgyCutoverWorker(t, nil)

	// Worker auto-approval answers an ordinary request...
	h.holders.out(holder, `{"jsonrpc":"2.0","method":"session/request_permission","params":{"sessionId":"agy-s1","toolCall":{"toolCallId":"w1","name":"write_to_file","title":"write"},"options":[{"optionId":"allow","kind":"allow_once"},{"optionId":"deny","kind":"reject_once"}]},"id":"8"}`)
	eventually(t, "the auto-approved write", func() bool { return agyAnswered(h, holder, "8", "allow") })
	// ...but never run_command: the daemon refuses it first.
	h.holders.out(holder, agyRunCommandRequest("9", "k1")...)
	eventually(t, "the refused run_command", func() bool { return agyAnswered(h, holder, "9", "deny") })
	if agyAnswered(h, holder, "9", "allow") {
		t.Fatal("worker auto-approval allowed run_command")
	}
	h.holders.out(holder, agyRefusedUpdate("k1"), agyEndTurn(h, holder, 0))

	eventually(t, "the instruction prompt", func() bool { return countInstruction(agyCutoverPrompts(t, h, holder)) == 1 })
	if n := len(got.list()); n != 0 {
		t.Fatalf("the refused turn reported to the parent: %+v", got.list())
	}
	h.holders.out(holder,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ran it with exec_shell"}}}}`,
		agyEndTurn(h, holder, 1))
	eventually(t, "the worker's report", func() bool { return len(got.list()) == 1 })
	if r := got.list()[0]; r.Result.Status != TurnDone || r.Result.Message != "ran it with exec_shell" {
		t.Fatalf("report: %+v", r)
	}
}

// startAgyCutoverWorker starts a held cutover agy worker of a lead (setup,
// when set, adjusts the harness first) and returns its reports, id and
// holder, with its first turn open.
func startAgyCutoverWorker(t *testing.T, setup func(*leadHarness, *agyFake)) (*leadHarness, *reports, string, string) {
	t.Helper()
	h, f := newAgyCutoverHarness(t)
	got := &reports{}
	h.opts.Report = got.add
	h.opts.WSMCP = func(string) (MCPCommand, error) { return MCPCommand{Command: "/unused/ws-mcp"}, nil }
	reg := h.opts.Backends[BackendAgy]
	reg.WorkerCapable, reg.WorkerProfile, reg.WorkerHTTPMCP, reg.WorkerAutoApprove = true, AgentWorkerProfile(BackendAgy), true, true
	h.opts.Backends[BackendAgy] = reg
	if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.opts.Config, "small", "", "gemini-3.8-flash", BackendAgy, "medium"); err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(h, f)
	}
	h.startAt("http://127.0.0.1:1")
	id, holder := spawnHeldRecoveryWorker(t, h, BackendAgy, "workdir-write")
	if rec := h.record(id); rec.ExecCutoverHolder == "" {
		t.Fatalf("the agy worker did not start under the cutover: %+v", rec)
	}
	return h, got, id, holder
}

// A model that keeps calling run_command after its instructions is not
// prompted again past the cap: the turn past it reports to the parent.
func TestAgyCutoverInstructionsAreCapped(t *testing.T) {
	h, got, _, holder := startAgyCutoverWorker(t, nil)
	for turn := 0; turn <= cutoverInstructionCap; turn++ {
		rpc, call := fmt.Sprintf("r%d", turn), fmt.Sprintf("k%d", turn)
		h.holders.out(holder, agyRunCommandRequest(rpc, call)...)
		eventually(t, "the refusal", func() bool { return agyAnswered(h, holder, rpc, "deny") })
		h.holders.out(holder, agyRefusedUpdate(call), agyEndTurn(h, holder, turn))
		if turn < cutoverInstructionCap {
			eventually(t, "the next instruction", func() bool { return countInstruction(agyCutoverPrompts(t, h, holder)) == turn+1 })
			if n := len(got.list()); n != 0 {
				t.Fatalf("turn %d reported while its instruction was owed: %+v", turn, got.list())
			}
		}
	}
	eventually(t, "the report past the cap", func() bool { return len(got.list()) == 1 })
	if n := countInstruction(agyCutoverPrompts(t, h, holder)); n != cutoverInstructionCap {
		t.Fatalf("instruction prompts = %d, want %d", n, cutoverInstructionCap)
	}
	if !strings.Contains(h.log.String(), "exec cutover instruction not sent again") {
		t.Fatalf("log: %s", h.log.String())
	}
}

// A cutover agy worker runs in native default as a lead does: a session
// that opens in another mode (a resumed one's last) is returned to it at
// launch, and an in-stream move away from it is undone.
func TestAgyCutoverWorkerRunsInNativeDefault(t *testing.T) {
	h, _, _, holder := startAgyCutoverWorker(t, func(_ *leadHarness, f *agyFake) { f.openMode = "auto" })
	// The launch sets default; the session's own open report of auto may
	// set it once more.
	eventually(t, "the launch's set_mode", func() bool { return len(agyModes(t, h, holder)) > 0 })
	launch := agyModes(t, h, holder)
	if slices.ContainsFunc(launch, func(m string) bool { return m != "default" }) {
		t.Fatalf("launch set_mode calls = %v", launch)
	}
	h.holders.out(holder, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"agy-s1","update":{"sessionUpdate":"current_mode_update","currentModeId":"auto"}}}`)
	eventually(t, "the worker returned to default", func() bool {
		modes := agyModes(t, h, holder)
		return len(modes) > len(launch) && modes[len(modes)-1] == "default"
	})
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

// A refusal recorded as answered whose reject_once never reached the
// process (the daemon went down between the two) is sent at the rejoin:
// the process does not wait on it forever.
func TestAgyCutoverResendsARecordedRefusalAtRejoin(t *testing.T) {
	h, f := newAgyCutoverHarness(t)
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.send(id, "hello")
	holder := h.holder(id)
	eventually(t, "the prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.idleOrQuiet(id)
	h.runner.Close()
	h.holders.out(holder, agyRunCommandRequest("6", "r2")...)
	dir := wsdashboard.SessionDir(h.dir, id)
	rec, err := readRecord(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The approval's id is the tool call's.
	rec.Answered = append(rec.Answered, "r2")
	if err := writeRecord(dir, rec); err != nil {
		t.Fatal(err)
	}
	h.holders.restart()
	h.startAt("http://127.0.0.1:1")
	eventually(t, "the resent refusal", func() bool { return agyAnswered(h, holder, "6", "deny") })
	if agyAnswered(h, holder, "6", "allow") {
		t.Fatal("the recorded refusal was allowed")
	}
	if got := h.record(id).Answered; len(slices.DeleteFunc(slices.Clone(got), func(a string) bool { return a != "r2" })) > 1 {
		t.Fatalf("answered ids = %v", got)
	}
	if st := h.state(id); len(st.Approvals) != 0 {
		t.Fatalf("the refusal stays pending: %+v", st.Approvals)
	}
}

// A denial never picks reject_always: a request that offers no reject_once
// gets no answer.
func TestAgyDenialNeverPicksRejectAlways(t *testing.T) {
	m := newAgyMapper("s")
	rr := &agyRun{m: m, answers: map[string]controlAnswer{}}
	line := `{"jsonrpc":"2.0","method":"session/request_permission","params":{"sessionId":"agy-s1","toolCall":{"toolCallId":"k9","name":"run_command","title":"ls","args":{"CommandLine":"ls"}},"options":[{"optionId":"allow","kind":"allow_once"},{"optionId":"never","kind":"reject_always"}]},"id":"9"}`
	var approval *ApprovalRequest
	for _, ev := range m.mapLine([]byte(line), rr) {
		if ev.Approval != nil {
			approval = ev.Approval
		}
	}
	if approval == nil {
		t.Fatal("no approval")
	}
	if _, option, err := m.answerFor(approval.ID, false); err == nil || option == "never" {
		t.Fatalf("denial answered with %q (err %v)", option, err)
	}
	if _, option, err := m.answerFor(approval.ID, true); err != nil || option != "allow" {
		t.Fatalf("allow answered with %q (err %v)", option, err)
	}
}

// The instruction hold ends whichever way the instruction leaves the
// inbox: an interrupt that takes it back (or a steer that takes it into a
// running turn) clears it as a turn's drain does, so the worker's report
// is not held for a turn that never comes.
func TestAgyCutoverInstructionHoldEndsWhenItIsTakenBack(t *testing.T) {
	h, _, id, _ := startAgyCutoverWorker(t, nil)
	c := h.runner.conv(id)
	var rec Record
	c.do(func() {
		c.cutoverInstructing = true
		rec = c.rec
	})
	if _, err := h.runner.AdmitCompletion(id, rec.OpenGen, cutoverEventPrefix+rec.HolderID+":k9", "use exec_shell"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.runner.Interrupt(id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the hold to end", func() bool {
		var instructing bool
		c.do(func() { instructing = c.cutoverInstructing })
		return !instructing
	})
}

// The dashboard's cancel and interrupt routes remove inbox entries
// themselves and only signal the inbox: a queued instruction removed that
// way ends the hold too, while one still held keeps it.
func TestAgyCutoverInstructionHoldEndsWhenTheDashboardRemovesIt(t *testing.T) {
	for _, route := range []string{"cancel", "interrupt"} {
		t.Run(route, func(t *testing.T) {
			h, _, id, _ := startAgyCutoverWorker(t, nil)
			c := h.runner.conv(id)
			var rec Record
			c.do(func() {
				c.cutoverInstructing, c.cutoverQueued = true, true
				// A rejoin's replay: no turn takes the inbox meanwhile, so
				// the instruction stays held for the dashboard to remove.
				c.replaying = true
				rec = c.rec
			})
			if _, err := h.runner.AdmitCompletion(id, rec.OpenGen, cutoverEventPrefix+rec.HolderID+":k9", "use exec_shell"); err != nil {
				t.Fatal(err)
			}
			instructing := func() bool {
				var on bool
				c.do(func() { on = c.cutoverInstructing })
				return on
			}
			h.runner.InboxChanged(id)
			if !instructing() {
				t.Fatal("the hold ended while the instruction is still held")
			}
			held, err := wsinbox.Held(c.inbox)
			if err != nil || len(held) == 0 {
				t.Fatalf("held = %v, %v", held, err)
			}
			if route == "cancel" {
				_, err = wsinbox.Cancel(c.inbox, held[0].Name)
			} else {
				_, _, err = wsinbox.Interrupt(c.inbox, time.Now())
			}
			if err != nil {
				t.Fatal(err)
			}
			h.runner.InboxChanged(id)
			eventually(t, "the hold to end", func() bool { return !instructing() })
		})
	}
}
