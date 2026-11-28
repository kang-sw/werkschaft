package wsagent

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// MintCompanionLead and EnsureCompanionLead make fakeLeadKeys a
// CompanionLeadKeyStore, so a test can open a hidden companion lead.
func (k *fakeLeadKeys) MintCompanionLead(_, role string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.n++
	return fmt.Sprintf("%s-key-%d", role, k.n), nil
}

func (k *fakeLeadKeys) EnsureCompanionLead(_, key, _ string) error {
	return k.EnsureLead("", key)
}

func agyPermissionAsk(callID, request, tool, kind string) string {
	kindField := ""
	if kind != "" {
		kindField = `"kind":"` + kind + `",`
	}
	return `{"jsonrpc":"2.0","method":"session/request_permission","params":{"sessionId":"agy-s1","toolCall":{"toolCallId":"` + callID + `","name":"` + tool + `",` + kindField + `"title":"` + tool + `","args":{"CommandLine":"echo fixture"}},"options":[{"optionId":"yes","kind":"allow_once"},{"optionId":"no","kind":"reject_once"}]},"id":"` + request + `"}`
}

// startAgyCompanion opens a hidden agy companion lead of role and runs its
// first turn.
func startAgyCompanion(t *testing.T, role string) (*leadHarness, string, string) {
	t.Helper()
	h, _ := newAgyLeadHarness(t)
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{CompanionRole: role}).ID
	h.send(id, "work")
	holder := h.holder(id)
	eventually(t, "first prompt", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	h.quiesce(id)
	return h, id, holder
}

func pendingApprovals(h *leadHarness, id string) int {
	c := h.runner.conv(id)
	n := -1
	c.do(func() { n = len(c.approvals) })
	return n
}

// TestAgyCompanionPermissionRequestsAreSettledNotParked: a hidden companion
// has no page to answer a prompt, so the daemon answers every permission
// request at once by the companion policy: the ws server's tools and
// read-only tools run for the body, a shell never does, and nothing is ever
// left waiting (a parked prompt stalled the session forever).
func TestAgyCompanionPermissionRequestsAreSettledNotParked(t *testing.T) {
	type ask struct {
		tool, kind string
		allow      bool
	}
	for _, tc := range []struct {
		role string
		asks []ask
	}{
		{companionRoleBody, []ask{
			{"mcp_ws_call", "", true},
			{"mcp_ws_companion_phone_pickup", "", true},
			{"view_file", "read", true},
			{"grep_search", "search", true},
			{agyCommandTool, "", false},
			{agyCommandTool, "execute", false},
			{"write_to_file", "edit", false},
			{"mcp_other_tool", "", false},
			{"unknown_tool", "", false},
		}},
		{companionRoleWork, []ask{
			{"mcp_ws_call", "", true},
			{"view_file", "read", true},
			{"write_to_file", "edit", true},
			{agyCommandTool, "", false},
			{agyCommandTool, "execute", false},
		}},
	} {
		t.Run(tc.role, func(t *testing.T) {
			h, id, holder := startAgyCompanion(t, tc.role)
			if rec := h.record(id); rec.CompanionRole != tc.role || rec.Listed || !rec.Lead || rec.AgyBypass {
				t.Fatalf("companion record: %+v", rec)
			}
			for i, a := range tc.asks {
				request := fmt.Sprintf("req-%d", i)
				h.holders.out(holder, agyPermissionAsk(fmt.Sprintf("call-%d", i), request, a.tool, a.kind))
				want, other := "yes", "no"
				if !a.allow {
					want, other = "no", "yes"
				}
				eventually(t, fmt.Sprintf("%s %q answered %s", a.tool, a.kind, want), func() bool {
					return agyOptionAnswers(h, holder, request, want) == 1
				})
				if agyOptionAnswers(h, holder, request, other) != 0 {
					t.Fatalf("%s %q also answered %s", a.tool, a.kind, other)
				}
				h.quiesce(id)
				if n := pendingApprovals(h, id); n != 0 {
					t.Fatalf("%s %q parked %d approvals", a.tool, a.kind, n)
				}
				if st := h.state(id); len(st.Approvals) != 0 {
					t.Fatalf("%s %q published approvals: %+v", a.tool, a.kind, st.Approvals)
				}
			}
		})
	}
}

// TestCompanionApprovalNeverParkedWithoutAnswerer: even when the process
// cannot be answered, a live companion request is dropped rather than parked
// where no page can ever answer it, and approvalFailed stops the process.
func TestCompanionApprovalNeverParkedWithoutAnswerer(t *testing.T) {
	h, id, _ := startAgyCompanion(t, companionRoleBody)
	c := h.runner.conv(id)
	var (
		parked  int
		lastErr string
		hasProc bool
	)
	c.do(func() {
		// A process with no approval controls: nothing can answer.
		mute := struct{ Process }{c.proc}
		c.proc = mute
		c.addApproval(ApprovalRequest{ID: "ws-call", Tool: "mcp_ws_call"}, false)
		c.addApproval(ApprovalRequest{ID: "shell", Tool: agyCommandTool}, false)
		parked = len(c.approvals)
		lastErr = c.lastErr
		hasProc = c.proc != nil
	})
	if parked != 0 || hasProc || lastErr != errNoApprovalAnswer.Error() {
		t.Fatalf("companion without answerer: parked=%d hasProc=%v lastErr=%q want %q", parked, hasProc, lastErr, errNoApprovalAnswer.Error())
	}
}

// TestCompanionApprovalVerdictViaAnswerer checks the verdict handed to the
// process through the real conv path with a spy answerer.
func TestCompanionApprovalVerdictViaAnswerer(t *testing.T) {
	for _, tc := range []struct {
		role  string
		tool  string
		kind  string
		allow bool
	}{
		{companionRoleBody, "mcp_ws_call", "", true},
		{companionRoleBody, agyCommandTool, "", false},
		{companionRoleWork, "write_to_file", "edit", true},
		{companionRoleWork, agyCommandTool, "", false},
		{"", "mcp_ws_call", "", false},
	} {
		if tc.role == "" {
			// A non-companion record never takes the companion allowance.
			if companionApprovalAllows(Record{Lead: true, Backend: BackendAgy}, ApprovalRequest{Tool: tc.tool}) {
				t.Fatalf("no-role record allowed %s", tc.tool)
			}
			continue
		}
		h, id, _ := startAgyCompanion(t, tc.role)
		c := h.runner.conv(id)
		spy := &approvalSpy{}
		var parked int
		c.do(func() {
			spy.Process, c.proc = c.proc, spy
			defer func() { c.proc = spy.Process }()
			c.addApproval(ApprovalRequest{ID: "spy-call", Tool: tc.tool, Kind: tc.kind}, false)
			parked = len(c.approvals)
		})
		if parked != 0 || len(spy.answered) != 1 || spy.allowed[0] != tc.allow {
			t.Fatalf("%s %s: parked=%d answered=%d allowed=%v want %v", tc.role, tc.tool, parked, len(spy.answered), spy.allowed, tc.allow)
		}
	}
}

// TestCompanionApprovalPolicyTable pins the verdicts by tool spelling: the ws
// server's tools in both harness spellings, a foreign Claude MCP tool, and the
// shell tools of every backend.
func TestCompanionApprovalPolicyTable(t *testing.T) {
	for _, tc := range []struct {
		role, tool, kind string
		allow            bool
	}{
		{companionRoleBody, "mcp__ws__call", "", true},
		{companionRoleBody, "mcp__ws__evil__tool", "", false},
		{companionRoleBody, "mcp__other__call", "", false},
		{companionRoleBody, "read_file", "read", true},
		{companionRoleBody, "write_to_file", "edit", false},
		{companionRoleBody, "Bash", "", false},
		{companionRoleWork, "Bash", "", false},
		{companionRoleWork, "exec_command", "", false},
		{companionRoleWork, "anything", "execute", false},
		{companionRoleWork, "mcp__ws__call", "", true},
		{companionRoleWork, "write_to_file", "edit", true},
		{"other-role", "mcp_ws_call", "", false},
	} {
		got := companionApprovalAllows(Record{Lead: true, CompanionRole: tc.role}, ApprovalRequest{Tool: tc.tool, Kind: tc.kind})
		if got != tc.allow {
			t.Errorf("%s %s (%s): allow=%v want %v", tc.role, tc.tool, tc.kind, got, tc.allow)
		}
	}
}

// TestCompanionExecApprovalRefusedNotParked: ws exec on a hidden companion
// has no page to approve it, so ApproveExec refuses it at once.
func TestCompanionExecApprovalRefusedNotParked(t *testing.T) {
	h, id, _ := startAgyCompanion(t, companionRoleWork)
	_, err := h.runner.ApproveExec(context.Background(), id, ExecApproval{Tool: "exec_shell", Command: "ls"})
	if !errors.Is(err, ErrExecDenied) {
		t.Fatalf("ApproveExec on a companion: %v", err)
	}
	if n := pendingApprovals(h, id); n != 0 {
		t.Fatalf("companion exec approval parked: %d", n)
	}
}

// TestCompanionReplayApprovalSettledAtReplayDone verifies that prompts parked
// on a hidden companion session during replay or quiet handling are settled by
// companionApprovalAllows at replayDone without falling through to agyBypass,
// while daemon-owned execAnswers entries are skipped.
func TestCompanionReplayApprovalSettledAtReplayDone(t *testing.T) {
	h, id, _ := startAgyCompanion(t, companionRoleBody)
	c := h.runner.conv(id)
	spy := &approvalSpy{}
	execCh := make(chan execAnswer, 1)
	var (
		duringReplay         int
		answeredDuringReplay int
		afterReplay          []ApprovalRequest
		execKept             bool
	)
	c.do(func() {
		spy.Process, c.proc = c.proc, spy
		defer func() { c.proc = spy.Process }()

		// Even with AgyBypass set on the record, allowAgyBypassPending must
		// delegate to settleCompanionPending rather than auto-allowing shell.
		c.update(func(rec *Record) {
			rec.AgyBypass = true
			rec.PermissionMode = "default"
		})

		c.replaying = true
		c.addApproval(ApprovalRequest{ID: "replayed-ws", Tool: "mcp_ws_call"}, false)
		c.addApproval(ApprovalRequest{ID: "replayed-shell", Tool: agyCommandTool, Kind: "execute"}, false)
		c.replaying = false
		c.addApproval(ApprovalRequest{ID: "quiet-read", Tool: "view_file", Kind: "read"}, true)

		if c.execAnswers == nil {
			c.execAnswers = map[string]chan execAnswer{}
		}
		c.execAnswers["exec-wait"] = execCh
		c.approvals = append(c.approvals, ApprovalRequest{ID: "exec-wait", Tool: "exec_shell"})

		c.replaying = true
		c.allowAgyBypassPending()
		duringReplay = len(c.approvals)
		answeredDuringReplay = len(spy.answered)

		c.replayDone(false)
		afterReplay = append([]ApprovalRequest(nil), c.approvals...)
		_, execKept = c.execAnswers["exec-wait"]
	})

	if duringReplay != 4 || answeredDuringReplay != 0 {
		t.Fatalf("mid-replay: parked=%d answered=%d want 4, 0", duringReplay, answeredDuringReplay)
	}
	if len(spy.answered) != 3 {
		t.Fatalf("answered=%+v want 3 (exec-wait must be skipped)", spy.answered)
	}
	want := map[string]bool{
		"replayed-ws":    true,
		"replayed-shell": false,
		"quiet-read":     true,
	}
	for i, req := range spy.answered {
		exp, ok := want[req.ID]
		if !ok {
			t.Fatalf("unexpected answered request %+v", req)
		}
		if spy.allowed[i] != exp {
			t.Fatalf("request %s allowed=%v want %v", req.ID, spy.allowed[i], exp)
		}
		delete(want, req.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing answered requests: %v", want)
	}
	if len(afterReplay) != 1 || afterReplay[0].ID != "exec-wait" || !execKept {
		t.Fatalf("after replayDone approvals=%+v execKept=%v want only exec-wait", afterReplay, execKept)
	}
}

// noAnswerSpy is a Process whose approval answerer returns errNoApprovalAnswer.
type noAnswerSpy struct {
	Process
}

func (s *noAnswerSpy) AnswerApproval(ApprovalRequest, bool, string) error {
	return errNoApprovalAnswer
}

// TestCompanionReplayUnanswerableApprovalFailsClosed verifies Critical #2:
// an unanswerable replayed companion prompt (either no answerer or
// errNoApprovalAnswer) drops all parked non-exec approvals, calls
// approvalFailed(errNoApprovalAnswer) setting lastErr, and stops the process,
// while leaving daemon-owned execAnswers intact.
func TestCompanionReplayUnanswerableApprovalFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(Process) Process
	}{
		{
			name: "no_answerer",
			wrap: func(p Process) Process { return struct{ Process }{p} },
		},
		{
			name: "errNoApprovalAnswer",
			wrap: func(p Process) Process { return &noAnswerSpy{Process: p} },
		},
		{
			name: "nil_proc",
			wrap: func(p Process) Process { _ = p.Close(); return nil },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, id, _ := startAgyCompanion(t, companionRoleBody)
			c := h.runner.conv(id)
			execCh := make(chan execAnswer, 1)
			var (
				afterReplay []ApprovalRequest
				execKept    bool
				hasProc     bool
				lastErr     string
			)
			c.do(func() {
				c.proc = tc.wrap(c.proc)
				c.replaying = true
				c.addApproval(ApprovalRequest{ID: "replayed-1", Tool: "mcp_ws_call"}, false)
				c.addApproval(ApprovalRequest{ID: "replayed-2", Tool: agyCommandTool}, false)
				if c.execAnswers == nil {
					c.execAnswers = map[string]chan execAnswer{}
				}
				c.execAnswers["exec-wait"] = execCh
				c.approvals = append(c.approvals, ApprovalRequest{ID: "exec-wait", Tool: "exec_shell"})

				c.replayDone(false)
				afterReplay = append([]ApprovalRequest(nil), c.approvals...)
				_, execKept = c.execAnswers["exec-wait"]
				hasProc = c.proc != nil
				lastErr = c.lastErr
			})
			if len(afterReplay) != 1 || afterReplay[0].ID != "exec-wait" || !execKept {
				t.Fatalf("%s: approvals=%+v execKept=%v want only exec-wait", tc.name, afterReplay, execKept)
			}
			if hasProc {
				t.Fatalf("%s: process still running after unanswerable replayed approval", tc.name)
			}
			if lastErr != errNoApprovalAnswer.Error() {
				t.Fatalf("%s: lastErr=%q want %q", tc.name, lastErr, errNoApprovalAnswer.Error())
			}
		})
	}
}
