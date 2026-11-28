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
// where no page can ever answer it.
func TestCompanionApprovalNeverParkedWithoutAnswerer(t *testing.T) {
	h, id, _ := startAgyCompanion(t, companionRoleBody)
	c := h.runner.conv(id)
	var parked int
	c.do(func() {
		// A process with no approval controls: nothing can answer.
		mute := struct{ Process }{c.proc}
		orig := c.proc
		c.proc = mute
		defer func() { c.proc = orig }()
		c.addApproval(ApprovalRequest{ID: "ws-call", Tool: "mcp_ws_call"}, false)
		c.addApproval(ApprovalRequest{ID: "shell", Tool: agyCommandTool}, false)
		parked = len(c.approvals)
	})
	if parked != 0 {
		t.Fatalf("companion approvals parked: %d", parked)
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
