package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

// childHarness provides Claude and werk tiers, steered reports, and an MCP endpoint.
func childHarness(t *testing.T) (*harness, *reports) {
	t.Helper()
	setLookPath(t, func(file string) (string, error) { return file, nil })
	h := newHarness(t, map[string]string{
		"chat": chatProfile,
		"piw":  `{"backend":"claude","model":"haiku","ws_tools":["pi.spawn","pi.list"]}`,
	})
	got := &reports{}
	h.config.Report = got.add
	h.config.Backends[BackendWerk] = BackendRegistration{Backend: h.backend, WorkerCapable: true, WorkerProfile: AgentWorkerProfile(BackendWerk)}
	h.backend.steers = true
	h.endpoint = "http://127.0.0.1:1"
	for tier, model := range map[string]string{"small": "prov/small-model", "medium": "prov/medium-model", "large": "prov/large-model"} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.config.Config, tier, "", model, BackendWerk, "low"); err != nil {
			t.Fatal(err)
		}
	}
	for tier, model := range map[string]string{"small": "claude-small", "medium": "claude-medium", "large": "claude-large"} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.config.Config, tier, "", model, BackendClaude, "high"); err != nil {
			t.Fatal(err)
		}
	}
	h.runner.Close()
	h.start()
	return h, got
}

// spawnWorker spawns a lead's worker of tier.
func (h *harness) spawnWorker(tier string) State {
	h.t.Helper()
	st, err := h.runner.Spawn(SpawnSpec{Profile: AgentWorkerProfile(BackendClaude), PermissionLevel: PermissionWorkdirWrite, delegatedAuthority: true, Root: h.root, Prompt: "work", Tier: tier, Label: "worker", Parent: &leadParent})
	if err != nil {
		h.t.Fatalf("Spawn: %v", err)
	}
	return st
}

// token is the capability token of the i-th process start, "" for none.
func (h *harness) token(i int) string {
	h.t.Helper()
	spec := h.backend.waitProcStart(h.t, i)
	if spec.WSMCP == nil {
		return ""
	}
	return spec.WSMCP.Env[EnvPiToken]
}

// tokenCall posts body to the token route's op from this machine.
func (h *harness) tokenCall(token, op string, body any) (int, string) {
	h.t.Helper()
	return h.tokenCallFrom("127.0.0.1:5555", token, op, body)
}

func (h *harness) tokenCallFrom(remote, token, op string, body any) (int, string) {
	h.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, RoutePrefix+"/"+tokenRoute+"/"+op, bytes.NewReader(raw))
	req.RemoteAddr = remote
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.runner.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// parentWait exercises report delivery through the shared wait implementation.
func (h *harness) parentWait(id string, timeout time.Duration) (int, string) {
	h.t.Helper()
	result, err := h.runner.wait(context.Background(), id, timeout, true)
	if err != nil {
		h.t.Fatalf("wait: %v", err)
	}
	body, err := json.Marshal(result)
	if err != nil {
		h.t.Fatal(err)
	}
	return http.StatusOK, string(body)
}

// spawnChild spawns a child of the token holder through the token route.
func (h *harness) spawnChild(token, tier, prompt string) State {
	h.t.Helper()
	code, body := h.tokenCall(token, "agent.spawn", map[string]any{"prompt": prompt, "tier": tier, "authority": "workdir-write"})
	if code != http.StatusCreated {
		h.t.Fatalf("token spawn = %d %s", code, body)
	}
	var result AgentSpawnResult
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		h.t.Fatal(err)
	}
	return h.state(agentID(result.Address))
}

// leadReports are the reports to the lead parent (leadParent).
func (r *reports) lead() []TurnReport {
	var out []TurnReport
	for _, rep := range r.list() {
		if rep.Parent == leadParent {
			out = append(out, rep)
		}
	}
	return out
}

// noLeadReport fails when the lead got a report within a short while.
func noLeadReport(t *testing.T, got *reports, why string) {
	t.Helper()
	time.Sleep(60 * time.Millisecond)
	if n := len(got.lead()); n != 0 {
		t.Fatalf("%s: the lead got %d reports: %+v", why, n, got.lead())
	}
}

// noSend fails when the process was sent a turn within a short while.
func noSend(t *testing.T, p *fakeProc, why string) {
	t.Helper()
	select {
	case s := <-p.sent:
		t.Fatalf("%s: a turn was sent: %q", why, s)
	case <-time.After(60 * time.Millisecond):
	}
}

func (h *harness) record(id string) Record {
	h.t.Helper()
	return readEventually(h.t, "the record", func() (Record, error) { return readRecord(wsdashboard.SessionDir(h.dir, id)) })
}

// workerWithChild is a running worker (process 0, its first turn sent)
// with a running child it spawned (process 1).
func workerWithChild(t *testing.T, h *harness) (w State, wp *fakeProc, token string, gc State, gp *fakeProc) {
	t.Helper()
	w = h.spawnWorker("medium")
	wp = h.backend.waitProc(t, 0)
	wp.nextSent(t)
	token = h.token(0)
	gc = h.spawnChild(token, "small", "sub task")
	gp = h.backend.waitProc(t, 1)
	gp.nextSent(t)
	return w, wp, token, gc, gp
}

func TestCapabilityTokenGoesToSpawnedWorkersOnly(t *testing.T) {
	h, _ := childHarness(t)
	w := h.spawnWorker("medium")
	spec := h.backend.waitProcStart(t, 0)
	env := spec.WSMCP.Env
	first := env[EnvPiToken]
	if len(first) != 64 || env[EnvPiEndpoint] != h.endpoint || !strings.Contains(env[EnvAllowedTools], "agent.spawn") {
		t.Fatalf("a spawned worker's ws-mcp gets a token, the endpoint, and agent.spawn: %v", env)
	}
	if rec := h.record(w.ID); rec.Tier != "medium" || h.state(w.ID).Tier != "medium" {
		t.Fatalf("the tier is recorded: %+v", rec)
	}
	// readFile, not os.ReadFile: it retries the Windows sharing violation
	// a concurrent record replace briefly causes, as production readers do.
	raw, err := readFile(recordPath(wsdashboard.SessionDir(h.dir, w.ID)))
	if err != nil || strings.Contains(string(raw), first) {
		t.Fatalf("the token is never persisted: %v", err)
	}

	// No recorded tier: no token.
	if _, err := h.runner.Spawn(SpawnSpec{Profile: AgentWorkerProfile(BackendClaude), PermissionLevel: PermissionWorkdirWrite, delegatedAuthority: true, Root: h.root, Prompt: "x", Parent: &leadParent}); err != nil {
		t.Fatal(err)
	}
	if tok := h.token(1); tok != "" {
		t.Fatalf("a spawn with no tier gets no token: %q", tok)
	}
	// A conversation opened through POST /agents: no token.
	c := h.create("piw")
	if _, err := h.runner.Send(c.ID, "hi"); err != nil {
		t.Fatal(err)
	}
	if spec := h.backend.waitProcStart(t, 2); spec.WSMCP.Env[EnvPiToken] != "" || spec.WSMCP.Env[EnvPiEndpoint] != "" {
		t.Fatalf("POST /agents gets no token: %v", spec.WSMCP.Env)
	}

	// A token is drawn at every process start; the last one is void.
	if code, _ := h.tokenCall(first, "list", nil); code != http.StatusOK {
		t.Fatalf("the live token lists: %d", code)
	}
	if _, err := h.runner.Stop(w.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := h.tokenCall(first, "list", nil); code != http.StatusUnauthorized {
		t.Fatalf("an ended process's token is void: %d", code)
	}
	if _, err := h.runner.Send(w.ID, "again"); err != nil {
		t.Fatal(err)
	}
	second := h.token(3)
	if second == "" || second == first {
		t.Fatalf("a new start draws a new token: %q", second)
	}
	if code, _ := h.tokenCall(second, "list", nil); code != http.StatusOK {
		t.Fatalf("the new token lists: %d", code)
	}
}

func TestChildReportIsSteeredIntoTheRunningParentTurn(t *testing.T) {
	h, got := childHarness(t)
	w, wp, _, gc, gp := workerWithChild(t, h)
	gp.reply("child result")
	var steered []string
	eventually(t, "the steered report", func() bool {
		steered, _ = wp.steered()
		return len(steered) == 1
	})
	if s := steered[0]; !strings.Contains(s, "[subagent id:"+gc.ID) || !strings.Contains(s, "] turn done") || !strings.Contains(s, "child result") {
		t.Fatalf("steered report = %q", s)
	}
	if rec := h.record(gc.ID); len(rec.PendingReports) != 0 {
		t.Fatalf("a delivered report leaves the child's record: %+v", rec.PendingReports)
	}
	wp.events <- Event{SteerAnswer: &SteerAnswer{}}
	wp.events <- Event{UserTaken: steered[0]}
	wp.reply("worker done")
	eventually(t, "the worker's report", func() bool { return len(got.lead()) == 1 })
	if rep := got.lead()[0]; rep.Result.ID != w.ID || rep.Result.Status != TurnDone || rep.Result.Message != "worker done" {
		t.Fatalf("the worker's report = %+v", rep)
	}
	noSend(t, wp, "a report the run took")
	if _, cleared := wp.steered(); cleared != 0 {
		t.Fatalf("nothing to drop: %d", cleared)
	}
}

func TestHostedLeadClaimedChildMailIsOneSteerOrLaterTurn(t *testing.T) {
	for _, active := range []bool{true, false} {
		name := "later turn"
		if active {
			name = "active steer"
		}
		t.Run(name, func(t *testing.T) {
			h := newLeadHarness(t).start()
			id := h.lead(BackendClaude, LeadOptions{}).ID
			key := h.record(id).WSKey
			var holder string
			var want []string
			if active {
				h.send(id, "question")
				holder = h.holder(id)
				h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"))
				h.sync(id, holder)
				want = append(h.envelopes("question"), "child report")
			} else {
				want = []string{"child report"}
			}
			h.mail.deliver(key, "child report")
			if !active {
				holder = h.holder(id)
			}
			eventually(t, "one child mail delivery", func() bool {
				return slices.Equal(h.userInputs(holder), want)
			})
			h.quiesce(id)
			if queued := h.mail.waiting(key); len(queued) != 0 {
				t.Fatalf("claimed mail remains queued: %q", queued)
			}
			if again, err := h.mail.Drain(h.root, key, nil); err != nil || len(again) != 0 {
				t.Fatalf("second claim = %+v, %v", again, err)
			}
			if got := h.userInputs(holder); !slices.Equal(got, want) {
				t.Fatalf("hosted lead inputs = %q, want %q", got, want)
			}
		})
	}
}

func TestChildReportPromptsAnIdleParentThatHeldItsReport(t *testing.T) {
	h, got := childHarness(t)
	w, wp, _, gc, gp := workerWithChild(t, h)
	wp.reply("interim")
	eventually(t, "the worker idle", func() bool { return h.state(w.ID).Status == StatusIdle })
	noLeadReport(t, got, "a worker with an open child")
	if res, err := h.runner.Wait(context.Background(), w.ID, 50*time.Millisecond); err != nil || res.Status != TurnRunning {
		t.Fatalf("a wait on a held worker = %+v, %v", res, err)
	}

	gp.reply("child result")
	turn := wp.nextSent(t)
	if !strings.Contains(turn, "[subagent id:"+gc.ID) || !strings.Contains(turn, "child result") {
		t.Fatalf("the report's turn = %q", turn)
	}
	if steered, _ := wp.steered(); len(steered) != 0 {
		t.Fatalf("an idle parent is not steered: %q", steered)
	}
	noLeadReport(t, got, "the report's turn runs")
	wp.reply("final")
	eventually(t, "the worker's report", func() bool { return len(got.lead()) == 1 })
	if rep := got.lead()[0]; rep.Result.Message != "final" {
		t.Fatalf("the reported turn = %+v", rep.Result)
	}
	if res, err := h.runner.Wait(context.Background(), w.ID, time.Second); err != nil || res.Status != TurnDone || res.Message != "final" {
		t.Fatalf("a wait returns the reported turn: %+v, %v", res, err)
	}
}

func TestSteeredReportTheRunMissedGoesAsTheNextTurn(t *testing.T) {
	for _, tc := range []struct {
		name string
		// before and after are the answers to the steer before and after
		// the parent's turn ends.
		before, after *SteerAnswer
		cleared       int
	}{
		{name: "queued, not taken", before: &SteerAnswer{}, cleared: 1},
		{name: "answered after the turn", after: &SteerAnswer{}, cleared: 1},
		{name: "refused", before: &SteerAnswer{Refusal: "no"}, cleared: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, got := childHarness(t)
			_, wp, _, gc, gp := workerWithChild(t, h)
			gp.reply("child result")
			eventually(t, "the steer", func() bool { s, _ := wp.steered(); return len(s) == 1 })
			if tc.before != nil {
				wp.events <- Event{SteerAnswer: tc.before}
			}
			wp.reply("interim")
			if tc.after != nil {
				noSend(t, wp, "a steer not yet answered")
				wp.events <- Event{SteerAnswer: tc.after}
			}
			turn := wp.nextSent(t)
			if !strings.Contains(turn, "[subagent id:"+gc.ID) || !strings.Contains(turn, "child result") {
				t.Fatalf("the report's turn = %q", turn)
			}
			if _, cleared := wp.steered(); cleared != tc.cleared {
				t.Fatalf("dropped from the harness's queue %d times, want %d", cleared, tc.cleared)
			}
			noLeadReport(t, got, "the report's turn runs")
			wp.reply("final")
			eventually(t, "the worker's report", func() bool { return len(got.lead()) == 1 })
			if rep := got.lead()[0]; rep.Result.Message != "final" {
				t.Fatalf("the reported turn = %+v", rep.Result)
			}
		})
	}
}

func TestParentWaitIsTheReportsDelivery(t *testing.T) {
	h, got := childHarness(t)
	w, wp, _, gc, gp := workerWithChild(t, h)
	type answer struct {
		code int
		body string
	}
	done := make(chan answer, 1)
	go func() {
		code, body := h.parentWait(gc.ID, 5*time.Second)
		done <- answer{code, body}
	}()
	c := h.runner.conv(gc.ID)
	eventually(t, "the parent's wait", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.parentWaits) == 1
	})
	gp.reply("child result")
	a := <-done
	var res TurnResult
	if a.code != http.StatusOK || json.Unmarshal([]byte(a.body), &res) != nil || res.Status != TurnDone || res.Message != "child result" || res.ID != gc.ID {
		t.Fatalf("the wait = %d %s", a.code, a.body)
	}
	time.Sleep(60 * time.Millisecond)
	if steered, _ := wp.steered(); len(steered) != 0 {
		t.Fatalf("a waited report is not steered too: %q", steered)
	}
	if rec := h.record(w.ID); len(rec.Reports) != 0 {
		t.Fatalf("a waited report is not kept for a turn: %q", rec.Reports)
	}
	if rec := h.record(gc.ID); len(rec.PendingReports) != 0 {
		t.Fatalf("a waited report is delivered: %+v", rec.PendingReports)
	}
	wp.reply("worker done")
	eventually(t, "the worker's report", func() bool { return len(got.lead()) == 1 })

	// A wait that comes after the settle takes the pending report, if it
	// is still there.
	ch := h.runner.conv(gc.ID)
	ch.update(func(rec *Record) {
		rec.PendingReports = append(rec.PendingReports, PendingReport{ID: "later", Result: TurnResult{ID: gc.ID, Status: TurnDone}})
	})
	ch.mu.Lock()
	ch.settledReport = "later"
	ch.mu.Unlock()
	if code, body := h.parentWait(gc.ID, 1*time.Second); code != http.StatusOK {
		t.Fatalf("the late wait = %d %s", code, body)
	}
	if rec := h.record(gc.ID); len(rec.PendingReports) != 0 {
		t.Fatalf("the late wait takes the pending report: %+v", rec.PendingReports)
	}
}

func TestFailedWorkerTurnIsNotHeld(t *testing.T) {
	h, got := childHarness(t)
	w, wp, _, _, _ := workerWithChild(t, h)
	wp.events <- Event{TurnEnd: true, TurnError: "boom"}
	eventually(t, "the failed turn's report", func() bool { return len(got.lead()) == 1 })
	if rep := got.lead()[0]; rep.Result.ID != w.ID || rep.Result.Status != TurnFailed || rep.Result.Error != "boom" {
		t.Fatalf("the report = %+v", rep.Result)
	}
}

func TestHeldReportSurvivesARestart(t *testing.T) {
	t.Run("interrupted child", func(t *testing.T) {
		h, got := childHarness(t)
		w, wp, _, gc, _ := workerWithChild(t, h)
		wp.reply("interim")
		eventually(t, "the worker idle", func() bool { return h.state(w.ID).Status == StatusIdle })
		h.runner.Close()
		noLeadReport(t, got, "before the restart")

		h.start()
		turn := h.backend.waitProc(t, 2).nextSent(t)
		if !strings.Contains(turn, "[subagent id:"+gc.ID) || !strings.Contains(turn, "turn interrupted") {
			t.Fatalf("the interrupted child's report resumes the worker: %q", turn)
		}
		noLeadReport(t, got, "the resumed worker's turn runs")
		h.backend.proc(2).reply("final")
		eventually(t, "the worker's report", func() bool { return len(got.lead()) == 1 })
		if rep := got.lead()[0]; rep.Result.ID != w.ID || rep.Result.Message != "final" {
			t.Fatalf("the report = %+v", rep.Result)
		}
		if rec := h.record(gc.ID); len(rec.PendingReports) != 0 || rec.TurnOpen {
			t.Fatalf("the child's report is delivered: %+v", rec)
		}
	})
	t.Run("pending report", func(t *testing.T) {
		h, got := childHarness(t)
		w, wp, _, gc, _ := workerWithChild(t, h)
		wp.reply("interim")
		eventually(t, "the worker idle", func() bool { return h.state(w.ID).Status == StatusIdle })
		h.runner.Close()
		// The child's turn settled and its report waited for the handover
		// when the daemon exited.
		dir := wsdashboard.SessionDir(h.dir, gc.ID)
		rec, err := readRecord(dir)
		if err != nil {
			t.Fatal(err)
		}
		rec.TurnOpen = false
		rec.PendingReports = []PendingReport{{ID: "p1", Result: TurnResult{ID: gc.ID, Status: TurnDone, Message: "persisted result"}}}
		// publish persists the settlement's send coverage with its pending report.
		rec.LastTurn = &PendingReport{ID: "p1", Result: rec.PendingReports[0].Result, Mark: rec.TurnMark}
		if err := writeRecord(dir, rec); err != nil {
			t.Fatal(err)
		}

		h.start()
		turn := h.backend.waitProc(t, 2).nextSent(t)
		if !strings.Contains(turn, "[subagent id:"+gc.ID) || !strings.Contains(turn, "persisted result") {
			t.Fatalf("the persisted report resumes the worker: %q", turn)
		}
		noLeadReport(t, got, "the resumed worker's turn runs")
		h.backend.proc(2).reply("final")
		eventually(t, "the worker's report", func() bool { return len(got.lead()) == 1 })
		if rep := got.lead()[0]; rep.Result.ID != w.ID || rep.Result.Message != "final" {
			t.Fatalf("the report = %+v", rep.Result)
		}
		if rec := h.record(gc.ID); len(rec.PendingReports) != 0 {
			t.Fatalf("the persisted report is delivered once: %+v", rec.PendingReports)
		}
	})
}

func TestClosingAWorkerClosesItsSubtreeDeepestFirst(t *testing.T) {
	h, _ := childHarness(t)
	w, _, _, gc, _ := workerWithChild(t, h)
	var mu sync.Mutex
	var order []string
	stop := h.runner.Watch(func(st State) {
		mu.Lock()
		defer mu.Unlock()
		if st.Status == StatusClosed && !slices.Contains(order, st.ID) {
			order = append(order, st.ID)
		}
	})
	defer stop()
	if _, err := h.runner.CloseConversation(w.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := slices.Clone(order)
	mu.Unlock()
	if !slices.Equal(got, []string{gc.ID, w.ID}) {
		t.Fatalf("close order = %v, want the child first", got)
	}
	if h.state(gc.ID).Status != StatusClosed {
		t.Fatalf("the child is closed")
	}
}

func TestStoppedWorkerResumesOnItsChildsReport(t *testing.T) {
	h, got := childHarness(t)
	w, wp, _, gc, gp := workerWithChild(t, h)
	wp.reply("interim")
	eventually(t, "the worker idle", func() bool { return h.state(w.ID).Status == StatusIdle })
	if _, err := h.runner.Stop(w.ID); err != nil {
		t.Fatal(err)
	}
	if st := h.state(gc.ID); st.Status == StatusClosed || !st.Process {
		t.Fatalf("a stop does not cascade: %+v", st)
	}
	gp.reply("child result")
	turn := h.backend.waitProc(t, 2).nextSent(t)
	if !strings.Contains(turn, "[subagent id:"+gc.ID) || !strings.Contains(turn, "child result") {
		t.Fatalf("the report resumes the stopped worker: %q", turn)
	}
	h.backend.proc(2).reply("final")
	eventually(t, "the worker's report", func() bool { return len(got.lead()) == 1 })
}

func TestReportToAClosedParentStays(t *testing.T) {
	h, _ := childHarness(t)
	w, _, _, gc, gp := workerWithChild(t, h)
	// The parent alone closes (a cascade closes its children with it).
	if _, err := h.runner.closeOne(w.ID); err != nil {
		t.Fatal(err)
	}
	gp.reply("late result")
	eventually(t, "the pending report", func() bool { return len(h.record(gc.ID).PendingReports) == 1 })
	time.Sleep(60 * time.Millisecond)
	if rec := h.record(gc.ID); len(rec.PendingReports) != 1 || rec.PendingReports[0].Result.Message != "late result" {
		t.Fatalf("the report stays in the child's record: %+v", rec.PendingReports)
	}
	if rec := h.record(w.ID); len(rec.Reports) != 0 {
		t.Fatalf("a closed parent takes no report: %q", rec.Reports)
	}
}

func TestHeldReportIsReleasedWhenTheChildCloses(t *testing.T) {
	h, got := childHarness(t)
	w, wp, _, gc, _ := workerWithChild(t, h)
	wp.reply("interim")
	eventually(t, "the worker idle", func() bool { return h.state(w.ID).Status == StatusIdle })
	noLeadReport(t, got, "a worker with an open child")
	if _, err := h.runner.CloseConversation(gc.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the held report", func() bool { return len(got.lead()) == 1 })
	if rep := got.lead()[0]; rep.Result.ID != w.ID || rep.Result.Message != "interim" {
		t.Fatalf("the released report = %+v", rep.Result)
	}
}

// TestWaitTakesATurnThatSettlesRightAfterItsCheck: a turn that settles
// between a wait's check and its block still answers the wait at once,
// a parent's token wait (the report's only delivery) and the lead's alike.
func TestWaitTakesATurnThatSettlesRightAfterItsCheck(t *testing.T) {
	h, _ := childHarness(t)
	_, wp, _, gc, gp := workerWithChild(t, h)
	c := h.runner.conv(gc.ID)
	settledCount := func() uint64 {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.settled.n
	}
	// settleOnce settles the child's running turn with msg the first time
	// a wait has checked, before that wait blocks.
	settleOnce := func(msg string) {
		var once sync.Once
		waitChecked = func() {
			once.Do(func() {
				n := settledCount()
				gp.reply(msg)
				eventually(t, "the settle", func() bool { return settledCount() > n })
			})
		}
	}
	t.Cleanup(func() { waitChecked = nil })

	// A wait that missed the settle would block until its timeout and
	// find the turn only then.
	const timeout = 3 * time.Second
	settleOnce("first")
	start := time.Now()
	code, body := h.parentWait(gc.ID, timeout)
	var res TurnResult
	if code != http.StatusOK || json.Unmarshal([]byte(body), &res) != nil || res.Status != TurnDone || res.Message != "first" {
		t.Fatalf("the parent's wait = %d %s", code, body)
	}
	if took := time.Since(start); took > timeout/2 {
		t.Fatalf("the parent's wait answered after %v: it missed the settle", took)
	}
	time.Sleep(60 * time.Millisecond)
	if steered, _ := wp.steered(); len(steered) != 0 {
		t.Fatalf("the waited report is not steered too: %q", steered)
	}

	if _, err := h.runner.Send(gc.ID, "again"); err != nil {
		t.Fatal(err)
	}
	gp.nextSent(t)
	settleOnce("second")
	start = time.Now()
	if res, err := h.runner.Wait(context.Background(), gc.ID, timeout); err != nil || res.Status != TurnDone || res.Message != "second" {
		t.Fatalf("the lead's wait = %+v, %v", res, err)
	}
	if took := time.Since(start); took > timeout/2 {
		t.Fatalf("the lead's wait answered after %v: it missed the settle", took)
	}
}

// TestRestartSettlesTheCutTurnBeforeTheNextStarts: a worker whose turn the
// daemon's exit cut off, with input waiting for its next turn (a child's
// kept report, a queued message), reports the cut turn as interrupted
// before the restart starts that next turn, which then stays open.
func TestRestartSettlesTheCutTurnBeforeTheNextStarts(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Record)
		want string
	}{
		{name: "kept report", set: func(rec *Record) { rec.Reports = []string{"[Pi subagent x] turn done\n\nkept report\n"} }, want: "kept report"},
		{name: "queued message", set: func(rec *Record) { rec.Queued = []QueuedInput{{Text: "queued message"}} }, want: "queued message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, got := childHarness(t)
			w := h.spawnWorker("medium")
			h.backend.waitProc(t, 0).nextSent(t)
			eventually(t, "the turn open on the record", func() bool { return h.record(w.ID).TurnOpen })
			h.runner.Close()
			dir := wsdashboard.SessionDir(h.dir, w.ID)
			rec, err := readRecord(dir)
			if err != nil {
				t.Fatal(err)
			}
			tc.set(&rec)
			if err := writeRecord(dir, rec); err != nil {
				t.Fatal(err)
			}

			h.start()
			p := h.backend.waitProc(t, 1)
			if turn := p.nextSent(t); !strings.Contains(turn, tc.want) {
				t.Fatalf("the restart's turn = %q", turn)
			}
			eventually(t, "the interrupted report", func() bool { return len(got.lead()) == 1 })
			if rep := got.lead()[0]; rep.Result.Status != TurnInterrupted {
				t.Fatalf("the cut turn's report = %+v", rep.Result)
			}
			time.Sleep(60 * time.Millisecond)
			if !h.record(w.ID).TurnOpen || h.state(w.ID).Status != StatusRunning {
				t.Fatalf("the new turn is open: %+v", h.record(w.ID))
			}
			if n := len(got.lead()); n != 1 {
				t.Fatalf("the new turn is not settled: %+v", got.lead())
			}
			p.reply("next done")
			eventually(t, "the new turn's report", func() bool { return len(got.lead()) == 2 })
			if rep := got.lead()[1]; rep.Result.Status != TurnDone || rep.Result.Message != "next done" {
				t.Fatalf("the new turn's report = %+v", rep.Result)
			}
		})
	}
}

// TestParentWaitDoesNotRepeatAHandedReport: a parent's wait on a child
// turn whose report already reached it (steered into its running turn, or
// sent as its next turn) answers with the status alone, marked delivered.
func TestParentWaitDoesNotRepeatAHandedReport(t *testing.T) {
	wait := func(h *harness, token, id string) TurnResult {
		t.Helper()
		code, body := h.parentWait(id, 2*time.Second)
		var res TurnResult
		if code != http.StatusOK || json.Unmarshal([]byte(body), &res) != nil {
			t.Fatalf("the wait = %d %s", code, body)
		}
		if strings.Contains(body, "child result") {
			t.Fatalf("the wait repeats the report: %s", body)
		}
		return res
	}
	t.Run("steered", func(t *testing.T) {
		h, _ := childHarness(t)
		_, wp, token, gc, gp := workerWithChild(t, h)
		gp.reply("child result")
		eventually(t, "the steered report", func() bool { s, _ := wp.steered(); return len(s) == 1 })
		if res := wait(h, token, gc.ID); res.ID != gc.ID || res.Status != TurnDone || !res.Delivered || res.Message != "" {
			t.Fatalf("the wait = %+v", res)
		}
	})
	t.Run("next turn", func(t *testing.T) {
		h, _ := childHarness(t)
		w, wp, token, gc, gp := workerWithChild(t, h)
		wp.reply("interim")
		eventually(t, "the worker idle", func() bool { return h.state(w.ID).Status == StatusIdle })
		gp.reply("child result")
		if turn := wp.nextSent(t); !strings.Contains(turn, "child result") {
			t.Fatalf("the report's turn = %q", turn)
		}
		if res := wait(h, token, gc.ID); res.Status != TurnDone || !res.Delivered || res.Message != "" {
			t.Fatalf("the wait = %+v", res)
		}
	})
}

// TestWaitAfterARestartAnswersASettledTurnAtOnce: a turn that settled
// before a daemon restart answers a wait after it at once: the lead's,
// with the turn, and a parent's on a report it already got, as delivered.
func TestWaitAfterARestartAnswersASettledTurnAtOnce(t *testing.T) {
	h, got := childHarness(t)
	w, wp, _, gc, gp := workerWithChild(t, h)
	gp.reply("child result")
	var steered []string
	eventually(t, "the steered report", func() bool { steered, _ = wp.steered(); return len(steered) == 1 })
	wp.events <- Event{SteerAnswer: &SteerAnswer{}}
	wp.events <- Event{UserTaken: steered[0]}
	wp.reply("worker done")
	eventually(t, "the worker's report", func() bool { return len(got.lead()) == 1 })
	h.runner.Close()

	h.start()
	const timeout = 3 * time.Second
	start := time.Now()
	res, err := h.runner.Wait(context.Background(), w.ID, timeout)
	if err != nil || res.Status != TurnDone || res.Message != "worker done" {
		t.Fatalf("the lead's wait after the restart = %+v, %v", res, err)
	}
	if took := time.Since(start); took > timeout/2 {
		t.Fatalf("the lead's wait answered after %v", took)
	}

	// The worker's next process holds a new token; its wait on the child
	// whose report it got before the restart answers at once.
	if _, err := h.runner.Send(w.ID, "go on"); err != nil {
		t.Fatal(err)
	}
	wp2 := h.backend.waitProc(t, 2)
	wp2.nextSent(t)
	start = time.Now()
	code, body := h.parentWait(gc.ID, timeout)
	var child TurnResult
	if code != http.StatusOK || json.Unmarshal([]byte(body), &child) != nil || child.Status != TurnDone || !child.Delivered {
		t.Fatalf("the parent's wait after the restart = %d %s", code, body)
	}
	if took := time.Since(start); took > timeout/2 {
		t.Fatalf("the parent's wait answered after %v", took)
	}
}

// TestCancelledMessageLeavesNoChildOpen: a message a worker sent its child
// that the page cancelled before any turn took it does not keep the child
// open, so the worker's done turn is reported.
func TestCancelledMessageLeavesNoChildOpen(t *testing.T) {
	h, got := childHarness(t)
	w, wp, _, gc, gp := workerWithChild(t, h)
	// pi.send now steers a running child immediately. Hold its loop so
	// this test really cancels before the drain, rather than racing it.
	g := newGate(t, func() {})
	go h.runner.conv(gc.ID).do(g.wait)
	g.awaitEntered(t)
	if _, err := h.runner.Send(gc.ID, "more"); err != nil {
		t.Fatal(err)
	}
	inbox := wsinbox.Dir(wsdashboard.SessionDir(h.dir, gc.ID))
	held, err := wsinbox.Held(inbox)
	if err != nil || len(held) != 1 {
		t.Fatalf("the held message: %v %v", held, err)
	}
	if _, err := wsinbox.Cancel(inbox, held[0].Name); err != nil {
		t.Fatal(err)
	}
	h.runner.InboxChanged(gc.ID)
	g.release()
	wp.reply("interim")
	eventually(t, "the worker idle", func() bool { return h.state(w.ID).Status == StatusIdle })
	gp.reply("child result")
	if turn := wp.nextSent(t); !strings.Contains(turn, "child result") {
		t.Fatalf("the report's turn = %q", turn)
	}
	wp.reply("final")
	eventually(t, "the worker's report", func() bool { return len(got.lead()) == 1 })
	if rep := got.lead()[0]; rep.Result.Message != "final" {
		t.Fatalf("the worker's report = %+v", rep.Result)
	}
	noSend(t, gp, "a cancelled message")
}

// TestTokenSpawnCwdFollowsTheHoldersWorkingDirectory: a worker working in
// a directory of its own (a worktree) spawns its children there: an empty
// cwd is that directory and a relative one resolves against it, while the
// child's root stays the holder's.
// TestPiTokenSpawnMintsTheChildKeyUnderItsHoldersPolicyKey: the legacy token
// pi.spawn route inherits the holder's session-scope config like agent.spawn.
