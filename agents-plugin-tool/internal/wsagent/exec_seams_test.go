package wsagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

type execApprovalResult struct {
	grant ExecGrant
	err   error
}

// askExec runs ApproveExec for lead id in the background.
func askExec(ctx context.Context, h *leadHarness, id, command string) <-chan execApprovalResult {
	ch := make(chan execApprovalResult, 1)
	go func() {
		grant, err := h.runner.ApproveExec(ctx, id, ExecApproval{Tool: "exec.shell", Command: command, Cwd: h.root})
		ch <- execApprovalResult{grant, err}
	}()
	return ch
}

// execPrompt waits for lead id's one waiting prompt.
func execPrompt(t *testing.T, h *leadHarness, id string) ApprovalRequest {
	t.Helper()
	var got ApprovalRequest
	eventually(t, "the exec approval prompt", func() bool {
		st := h.state(id)
		if len(st.Approvals) == 1 {
			got = st.Approvals[0]
			return true
		}
		return false
	})
	return got
}

func takeExecResult(t *testing.T, ch <-chan execApprovalResult) execApprovalResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("ApproveExec did not return")
		return execApprovalResult{}
	}
}

// TestExecApprovalPromptsALeadOutsideFullAndBindsTheCall: a lead in a
// prompt-required mode waits on its page for each exec call, the prompt
// names the call and is answered once, a denial carries the user's
// message, and the end of the call withdraws its prompt.
func TestExecApprovalPromptsALeadOutsideFullAndBindsTheCall(t *testing.T) {
	h := newLeadHarness(t).start()
	id := h.lead(BackendClaude, LeadOptions{PermissionMode: "acceptEdits"}).ID
	if ok, err := h.runner.ExecImmediate(id); err != nil || ok {
		t.Fatalf("acceptEdits runs exec at once = %v, %v; want a prompt", ok, err)
	}

	res := askExec(context.Background(), h, id, "make test")
	p := execPrompt(t, h, id)
	if p.Tool != "exec.shell" || !strings.HasPrefix(p.ID, "exec-") || !strings.Contains(string(p.Input), "make test") || !strings.Contains(string(p.Input), h.root) {
		t.Fatalf("prompt = %+v %s", p, p.Input)
	}
	if _, err := h.runner.AnswerApproval(id, p.ID, true, ""); err != nil {
		t.Fatalf("allow: %v", err)
	}
	if r := takeExecResult(t, res); r.err != nil || r.grant != ExecGrantAnswer {
		t.Fatalf("allowed call = %+v", r)
	}
	if _, err := h.runner.AnswerApproval(id, p.ID, true, ""); !isConflict(err) {
		t.Fatalf("a second answer = %v, want a conflict", err)
	}
	if st := h.state(id); len(st.Approvals) != 0 {
		t.Fatalf("answered prompt still waits: %+v", st.Approvals)
	}

	res = askExec(context.Background(), h, id, "rm -rf build")
	p = execPrompt(t, h, id)
	if _, err := h.runner.AnswerApproval(id, p.ID, false, "not now"); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if r := takeExecResult(t, res); !errors.Is(r.err, ErrExecDenied) || !strings.Contains(r.err.Error(), "not now") {
		t.Fatalf("denied call = %+v", r)
	}

	ctx, cancel := context.WithCancel(context.Background())
	res = askExec(ctx, h, id, "sleep 1")
	p = execPrompt(t, h, id)
	cancel()
	if r := takeExecResult(t, res); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("withdrawn call = %+v", r)
	}
	eventually(t, "the withdrawn prompt to go", func() bool { return len(h.state(id).Approvals) == 0 })
	if _, err := h.runner.AnswerApproval(id, p.ID, true, ""); !isConflict(err) {
		t.Fatalf("answering a withdrawn prompt = %v, want a conflict", err)
	}
}

// TestExecApprovalFollowsTheLiveMode: a mode change that runs exec calls
// without a prompt settles a waiting one, a change between two prompting
// modes keeps it waiting, and a lead in full runs at once.
func TestExecApprovalFollowsTheLiveMode(t *testing.T) {
	h := newLeadHarness(t).start()
	id := h.lead(BackendClaude, LeadOptions{PermissionMode: "acceptEdits"}).ID
	res := askExec(context.Background(), h, id, "go test ./...")
	execPrompt(t, h, id)
	if _, err := h.runner.setPermissionMode(id, "plan", true); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-res:
		t.Fatalf("a change to another prompting mode settled the call: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := h.runner.setPermissionMode(id, "bypassPermissions", true); err != nil {
		t.Fatal(err)
	}
	if r := takeExecResult(t, res); r.err != nil || r.grant != ExecGrantPosture {
		t.Fatalf("call settled by bypass = %+v", r)
	}
	eventually(t, "the settled prompt to go", func() bool { return len(h.state(id).Approvals) == 0 })
	if ok, err := h.runner.ExecImmediate(id); err != nil || !ok {
		t.Fatalf("bypass runs exec at once = %v, %v", ok, err)
	}
	// The mode is read live: back in a prompting mode, the next call waits.
	if _, err := h.runner.setPermissionMode(id, "acceptEdits", true); err != nil {
		t.Fatal(err)
	}
	if ok, _ := h.runner.ExecImmediate(id); ok {
		t.Fatal("acceptEdits after bypass still runs exec at once")
	}

	full := h.lead(BackendClaude, LeadOptions{PermissionMode: "bypassPermissions"}).ID
	grant, err := h.runner.ApproveExec(context.Background(), full, ExecApproval{Tool: "exec.run", Command: "true"})
	if err != nil || grant != ExecGrantPosture {
		t.Fatalf("full lead = %v, %v", grant, err)
	}
	if st := h.state(full); len(st.Approvals) != 0 {
		t.Fatalf("a full lead got a prompt: %+v", st.Approvals)
	}
}

// TestExecImmediatePosture pins the policy over the record alone: non-leads
// run at once, an agy lead without a common profile follows its ws bypass,
// and a Codex lead with no selection counts its reported full-access mode.
func TestExecImmediatePosture(t *testing.T) {
	r := NewRunner(Options{Dir: t.TempDir()})
	for _, tc := range []struct {
		name    string
		rec     Record
		active  string
		process bool
		want    bool
	}{
		{name: "worker", rec: Record{Backend: BackendClaude, Parent: &leadParent}, want: true},
		{name: "non-lead without parent", rec: Record{Backend: BackendClaude}, want: true},
		{name: "agy lead bypass on", rec: Record{Lead: true, Backend: BackendAgy, AgyBypass: true, PermissionMode: "default"}, want: true},
		{name: "agy lead bypass off", rec: Record{Lead: true, Backend: BackendAgy, PermissionMode: "default"}, want: false},
		{name: "agy stale bypass flag", rec: Record{Lead: true, Backend: BackendAgy, AgyBypass: true, PermissionMode: "auto"}, want: false},
		{name: "claude plan", rec: Record{Lead: true, Backend: BackendClaude, PermissionMode: "plan"}, want: false},
		{name: "claude bypass", rec: Record{Lead: true, Backend: BackendClaude, PermissionMode: "bypassPermissions"}, want: true},
		{name: "codex full", rec: Record{Lead: true, Backend: BackendCodex, PermissionMode: "danger-full-access"}, want: true},
		{name: "codex reported full", rec: Record{Lead: true, Backend: BackendCodex}, active: "danger-full-access", process: true, want: true},
		{name: "codex reported full without process", rec: Record{Lead: true, Backend: BackendCodex}, active: "danger-full-access", want: false},
		{name: "codex workspace", rec: Record{Lead: true, Backend: BackendCodex, PermissionMode: "workspace-write"}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &conv{r: r, dir: t.TempDir(), rec: tc.rec, activeMode: tc.active}
			if tc.process {
				c.proc = &fakeProc{events: make(chan Event)}
			}
			if got := c.execImmediate(); got != tc.want {
				t.Fatalf("execImmediate = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestExecApprovalNonLeadRunsAtOnce: a spawned or profile conversation never
// waits on a prompt nobody's page shows.
func TestExecApprovalNonLeadRunsAtOnce(t *testing.T) {
	h := newHarness(t, map[string]string{"chat": chatProfile})
	id := h.create("chat").ID
	grant, err := h.runner.ApproveExec(context.Background(), id, ExecApproval{Tool: "exec.shell", Command: "ls"})
	if err != nil || grant != ExecGrantPosture {
		t.Fatalf("non-lead = %v, %v", grant, err)
	}
	owner, err := h.runner.ExecOwner(id)
	if err != nil || owner.ID != id || owner.Lead {
		t.Fatalf("owner = %+v, %v", owner, err)
	}
}

// TestAdmitCompletionOnceAcrossRestartDrainAndCrash: a notice steers the
// running turn once; a replay of its event, before or after a restart and
// after its entry was drained, admits nothing; an entry written just before
// a crash that lost its record write is found in the inbox; an idle session
// starts a turn with the next notice.
func TestAdmitCompletionOnceAcrossRestartDrainAndCrash(t *testing.T) {
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), toolUseLine("u1", "toolu_1"))
	h.sync(id, holder)
	owner, err := h.runner.ExecOwner(id)
	if err != nil || !owner.Lead {
		t.Fatalf("owner = %+v, %v", owner, err)
	}
	admit := func(event, text string) CompletionOutcome {
		t.Helper()
		out, err := h.runner.AdmitCompletion(id, owner.Gen, event, text)
		if err != nil {
			t.Fatalf("AdmitCompletion(%s): %v", event, err)
		}
		return out
	}
	if out := admit("exec:exec_a:exit", "exec exec_a finished: exit 0"); out != CompletionAccepted {
		t.Fatalf("first admission = %v", out)
	}
	eventually(t, "the notice steered into the running turn", func() bool {
		inputs := h.userInputs(holder)
		return len(inputs) == 2 && strings.Contains(inputs[1], "exec exec_a finished: exit 0")
	})
	if out := admit("exec:exec_a:exit", "exec exec_a finished: exit 0"); out != CompletionDuplicate {
		t.Fatalf("replayed admission = %v", out)
	}
	if rec := h.record(id); len(rec.AcceptedEvents) != 1 || rec.AcceptedEvents[0] != "exec:exec_a:exit" {
		t.Fatalf("accepted events = %q", rec.AcceptedEvents)
	}

	h.holders.out(holder, turnLines("sess-1", "u2", "done")...)
	h.idle(id)
	// The crash right after the inbox write: the entry is there, the record
	// never heard of it, and the daemon is gone before it tells the loop.
	inbox := wsinbox.Dir(wsdashboard.SessionDir(h.dir, id))
	if _, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"exec exec_b finished"}, Steer: true, EventID: "exec:exec_b:exit"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	h.restart()
	if out := admit("exec:exec_a:exit", "again"); out != CompletionDuplicate {
		t.Fatalf("drained event after restart = %v", out)
	}
	if out := admit("exec:exec_b:exit", "again"); out != CompletionDuplicate {
		t.Fatalf("crash-window event after restart = %v", out)
	}
	// A crash before the inbox write left nothing: that notice is new.
	before := len(h.userInputs(holder))
	if out := admit("exec:exec_c:exit", "exec exec_c finished: exit 2"); out != CompletionAccepted {
		t.Fatalf("new event after restart = %v", out)
	}
	eventually(t, "the idle session's notice turn", func() bool {
		inputs := h.userInputs(holder)
		for _, in := range inputs[before:] {
			if strings.Contains(in, "exec exec_c finished: exit 2") {
				return true
			}
		}
		return false
	})
}

// TestAdmitCompletionFencedByClose: a close suppresses the notice, and a
// revival does not bring back a notice admitted before it.
func TestAdmitCompletionFencedByClose(t *testing.T) {
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "done")...)
	h.idle(id)
	owner, err := h.runner.ExecOwner(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.runner.CloseConversation(id); err != nil {
		t.Fatal(err)
	}
	if out, err := h.runner.AdmitCompletion(id, owner.Gen, "exec:x:exit", "late"); err != nil || out != CompletionSuppressed {
		t.Fatalf("after close = %v, %v", out, err)
	}
	if _, err := h.runner.ExecOwner(id); err == nil {
		t.Fatal("a closed conversation owns new jobs")
	}
	if _, err := h.runner.Reopen(id); err != nil {
		t.Fatal(err)
	}
	if out, err := h.runner.AdmitCompletion(id, owner.Gen, "exec:x:exit", "stale"); err != nil || out != CompletionSuppressed {
		t.Fatalf("stale notice after revival = %v, %v", out, err)
	}
	reopened, err := h.runner.ExecOwner(id)
	if err != nil || reopened.Gen != owner.Gen+1 {
		t.Fatalf("reopened owner = %+v, %v (was gen %d)", reopened, err, owner.Gen)
	}
	if out, err := h.runner.AdmitCompletion(id, reopened.Gen, "exec:y:exit", "fresh"); err != nil || out != CompletionAccepted {
		t.Fatalf("notice of the reopened session = %v, %v", out, err)
	}
	if _, err := h.runner.AdmitCompletion("nobody", 0, "exec:z:exit", "x"); err != nil {
		t.Fatalf("unknown conversation: %v", err)
	}
}

// TestAdmitCompletionNeverRevivesAClosedPiChild: ordinary input reopens a
// closed spawned Pi child, a completion notice does not, and after a manual
// revival a notice admitted before the close stays out.
func TestAdmitCompletionNeverRevivesAClosedPiChild(t *testing.T) {
	h, _ := newFakePiRecoveryHarness(t)
	cwd := filepath.Join(h.root, "work")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	worker, err := h.runner.Spawn(SpawnSpec{Profile: PiWorkerProfile, Root: h.root, Cwd: cwd, Model: "prov/medium", Effort: "high", Label: "notice target", Prompt: "work", Tier: "medium", Parent: &leadParent})
	if err != nil {
		t.Fatal(err)
	}
	holder := h.holder(worker.ID)
	eventually(t, "the initial prompt", func() bool { return len(h.holders.inputs(holder, "prompt")) == 1 })
	owner, err := h.runner.ExecOwner(worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(piSessionFile(wsdashboard.SessionDir(h.opts.Dir, worker.ID)), []byte("persisted conversation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runner.CloseConversation(worker.ID); err != nil {
		t.Fatal(err)
	}
	if out, err := h.runner.AdmitCompletion(worker.ID, owner.Gen, "exec:p:exit", "done"); err != nil || out != CompletionSuppressed {
		t.Fatalf("notice to a closed Pi child = %v, %v", out, err)
	}
	if rec := h.record(worker.ID); rec.ClosedAt == "" || rec.HolderID != "" {
		t.Fatalf("the notice revived the child: %+v", rec)
	}
	if st := h.state(worker.ID); st.Status != StatusClosed || st.Process {
		t.Fatalf("the notice revived the child: %+v", st)
	}
	// The user's own message revives it; the earlier notice stays out.
	if _, err := h.runner.Send(worker.ID, "continue"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the revived process", func() bool {
		id := h.record(worker.ID).HolderID
		return id != "" && id != holder
	})
	if out, err := h.runner.AdmitCompletion(worker.ID, owner.Gen, "exec:p:exit", "done"); err != nil || out != CompletionSuppressed {
		t.Fatalf("pre-close notice after manual revival = %v, %v", out, err)
	}
}

// TestAgyNoticeWaitsOutTheInFlightMCPCall: on agy, whose steer replaces the
// running prompt, a completion notice waits while a ws MCP call is in
// flight. The call's cancellation releases it once; a late result after the
// cancellation steers nothing more. The user's own steer is not held.
func TestAgyNoticeWaitsOutTheInFlightMCPCall(t *testing.T) {
	h, f := newAgyLeadHarness(t)
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.send(id, "p1")
	holder := h.holder(id)
	eventually(t, "p1 sent", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	token := h.record(id).MCPToken
	if token == "" {
		t.Fatal("the agy lead has no MCP session token")
	}
	// The token resolution is the trusted binding to this conversation.
	if hello, _, ok := h.runner.ResolveMCPToken(token); !ok || hello.Conversation != id {
		t.Fatalf("token resolves to conversation %q (ok %v), want %q", hello.Conversation, ok, id)
	}
	end := h.runner.MCPCallBegin(token)
	owner, err := h.runner.ExecOwner(id)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := h.runner.AdmitCompletion(id, owner.Gen, "exec:g:exit", "exec exec_g finished"); err != nil || out != CompletionAccepted {
		t.Fatalf("admission = %v, %v", out, err)
	}
	h.runner.InboxChanged(id)
	h.quiesce(id)
	if n := len(agyPromptIDs(h, holder)); n != 1 {
		t.Fatalf("the notice steered during the MCP call: %d prompts", n)
	}
	end() // the originating call is cancelled before its reply
	eventually(t, "the held notice's one steer", func() bool { return len(agyPromptIDs(h, holder)) == 2 })
	end() // a late result after the cancellation
	h.runner.InboxChanged(id)
	h.quiesce(id)
	if n := len(agyPromptIDs(h, holder)); n != 2 {
		t.Fatalf("a late result steered again: %d prompts", n)
	}
	if n := h.runner.conv(id).mcpCalls.Load(); n != 0 {
		t.Fatalf("calls in flight = %d after one call's two ends", n)
	}

	// The user's steer still goes while a call is in flight.
	end = h.runner.MCPCallBegin(token)
	defer end()
	h.send(id, "user steer")
	h.steerHeld(id)
	h.runner.InboxChanged(id)
	eventually(t, "the user's steer", func() bool { return len(agyPromptIDs(h, holder)) == 3 })
	if n := h.runner.conv(id).mcpCalls.Load(); n != 1 {
		t.Fatalf("calls in flight = %d", n)
	}
	if end := h.runner.MCPCallBegin("unknown-token"); end == nil {
		t.Fatal("an unknown token has no end")
	} else {
		end()
	}
}
