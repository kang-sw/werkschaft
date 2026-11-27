package wsagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

type nonHolderStub struct{ *fakeProc }

func (nonHolderStub) HolderInfo() (string, string, bool) { return "", "", false }

func TestSafeStopStatesAndSessionMeta(t *testing.T) {
	t.Run("idle holder-backed lead is safe_stop; active, approval, bg, and native subagent are not", func(t *testing.T) {
		h := newLeadHarness(t).start()
		id, holder := h.settledLead()

		// Idle holder-backed lead -> SafeStop == true in both State and SessionMeta.
		st := h.state(id)
		if !st.Process || !st.SafeStop {
			t.Fatalf("idle holder-backed lead state = %+v, want Process=true SafeStop=true", st)
		}
		meta, ok := h.runner.SessionMeta(id)
		if !ok || !meta.HasProcess || !meta.SafeStop {
			t.Fatalf("idle holder-backed lead SessionMeta = %+v, %v; want HasProcess=true SafeStop=true", meta, ok)
		}

		// Pending background task -> SafeStop == false.
		c := h.runner.conv(id)
		c.mu.Lock()
		c.bgTasks = 1
		c.mu.Unlock()
		if st := h.state(id); !st.Process || st.SafeStop {
			t.Fatalf("bgTasks=1 state = %+v, want Process=true SafeStop=false", st)
		}
		if meta, _ := h.runner.SessionMeta(id); !meta.HasProcess || meta.SafeStop {
			t.Fatalf("bgTasks=1 SessionMeta = %+v, want HasProcess=true SafeStop=false", meta)
		}
		c.mu.Lock()
		c.bgTasks = 0
		c.mu.Unlock()

		// Running native subagent -> SafeStop == false; finished -> SafeStop == true.
		now := time.Now().UnixMilli()
		c.do(func() {
			c.live.apply(wslive.Event{
				Kind:    wslive.KindAgent,
				AgentID: wslive.NativeAgyPrefix + "call-1",
				Label:   "research",
				Status:  wslive.StatusRunning,
				At:      now,
			})
		})
		if st := h.state(id); !st.Process || st.SafeStop {
			t.Fatalf("running native subagent state = %+v, want SafeStop=false", st)
		}
		if meta, _ := h.runner.SessionMeta(id); !meta.HasProcess || meta.SafeStop {
			t.Fatalf("running native subagent SessionMeta = %+v, want SafeStop=false", meta)
		}
		c.do(func() {
			c.live.apply(wslive.Event{
				Kind:    wslive.KindAgent,
				AgentID: wslive.NativeAgyPrefix + "call-1",
				Status:  wslive.StatusDone,
				At:      now + 100,
			})
		})
		if st := h.state(id); !st.Process || !st.SafeStop {
			t.Fatalf("finished native subagent state = %+v, want SafeStop=true", st)
		}

		// Active turn -> SafeStop == false.
		h.send(id, "work")
		h.holders.out(holder, stateLine(HarnessRunning))
		eventually(t, "active turn", func() bool { return h.state(id).Status == StatusRunning })
		if st := h.state(id); !st.Process || st.SafeStop {
			t.Fatalf("running turn state = %+v, want Process=true SafeStop=false", st)
		}
		h.holders.out(holder, replyLine("u2", "done"), resultLine(), stateLine(HarnessIdle))
		h.idle(id)

		// Pending approval -> SafeStop == false.
		waitID, _ := h.waitingLead()
		if st := h.state(waitID); !st.Process || st.SafeStop {
			t.Fatalf("waiting approval state = %+v, want Process=true SafeStop=false", st)
		}

		// Stopped process -> HasProcess == false, SafeStop == false.
		if _, err := h.runner.Stop(id); err != nil {
			t.Fatal(err)
		}
		eventually(t, "lead stopped", func() bool { return !h.state(id).Process })
		if st := h.state(id); st.Process || st.SafeStop {
			t.Fatalf("stopped lead state = %+v, want Process=false SafeStop=false", st)
		}
		if meta, ok := h.runner.SessionMeta(id); !ok || meta.HasProcess || meta.SafeStop {
			t.Fatalf("stopped lead SessionMeta = %+v, %v; want HasProcess=false SafeStop=false", meta, ok)
		}
	})

	t.Run("hosted child session and parent descendant gating", func(t *testing.T) {
		h, _ := newHeldWorkerHarness(t)
		leadID, _ := h.settledLead()

		// Spawn a holder-backed child under the lead.
		childSt, err := h.runner.Spawn(SpawnSpec{
			Profile:            AgentWorkerProfile(BackendClaude),
			PermissionLevel:    PermissionWorkdirWrite,
			delegatedAuthority: true,
			Root:               h.root,
			Prompt:             "work",
			Tier:               "small",
			Parent:             &ParentRef{Kind: ParentLead, ReplyID: leadID},
		})
		if err != nil {
			t.Fatal(err)
		}
		childHolder := h.holder(childSt.ID)
		h.holders.out(childHolder, stateLine(HarnessRunning), initLine("child-s1", "dontAsk"))
		eventually(t, "child running", func() bool { return h.state(childSt.ID).Status == StatusRunning })

		// While child is active, neither child nor parent is safe_stop.
		if st := h.state(childSt.ID); !st.Process || st.SafeStop {
			t.Fatalf("active child state = %+v, want SafeStop=false", st)
		}
		if st := h.state(leadID); !st.Process || st.SafeStop {
			t.Fatalf("parent of active child state = %+v, want SafeStop=false", st)
		}

		// Settle the child; both child (non-lead!) and parent become safe_stop.
		h.holders.out(childHolder, recoveryWorkerReply("done")...)
		// The idle status becomes visible before the loop settles the child's input.
		h.idle(childSt.ID)
		if st := h.state(childSt.ID); !st.Process || !st.SafeStop {
			t.Fatalf("idle holder-backed child state = %+v, want Process=true SafeStop=true", st)
		}
		if st := h.state(leadID); !st.Process || !st.SafeStop {
			t.Fatalf("parent of idle holder-backed child state = %+v, want Process=true SafeStop=true", st)
		}

		// Non-holder child process makes both the child and its idle parent unsafe to stop.
		childConv := h.runner.conv(childSt.ID)
		childConv.mu.Lock()
		origProc := childConv.proc
		childConv.proc = nonHolderStub{fakeProc: &fakeProc{}} // implements holderProcess, but HolderInfo() reports false
		childConv.mu.Unlock()
		defer func() {
			childConv.mu.Lock()
			childConv.proc = origProc
			childConv.mu.Unlock()
		}()
		if st := h.state(childSt.ID); !st.Process || st.SafeStop {
			t.Fatalf("non-holder child state = %+v, want Process=true SafeStop=false", st)
		}
		if st := h.state(leadID); !st.Process || st.SafeStop {
			t.Fatalf("parent of non-holder child state = %+v, want Process=true SafeStop=false", st)
		}
	})

	t.Run("direct non-holder process is never safe_stop", func(t *testing.T) {
		h, _ := childHarness(t)
		w, p := finishableWorker(t, h)
		p.reply("done")
		eventually(t, "worker idle", func() bool { return h.state(w.ID).Status == StatusIdle })
		st := h.state(w.ID)
		if !st.Process || st.SafeStop {
			t.Fatalf("direct fakeProc worker state = %+v, want Process=true SafeStop=false", st)
		}
		if meta, ok := h.runner.SessionMeta(w.ID); !ok || !meta.HasProcess || meta.SafeStop {
			t.Fatalf("direct fakeProc worker SessionMeta = %+v, %v; want HasProcess=true SafeStop=false", meta, ok)
		}
	})
}

func TestSafeOnlyStopChecksStateAtAdmission(t *testing.T) {
	h := newLeadHarness(t)
	h.opts.Trusted = func(*http.Request) (bool, bool) { return true, true }
	h.start()
	id, holder := h.settledLead()
	if st := h.state(id); !st.SafeStop {
		t.Fatalf("idle session should advertise safe Stop: %+v", st)
	}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/agents/"+id+"/stop", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		h.runner.Handler().ServeHTTP(out, req)
		return out
	}

	h.send(id, "work after the browser read idle")
	h.holders.out(holder, stateLine(HarnessRunning))
	eventually(t, "new turn running", func() bool { return h.state(id).Status == StatusRunning })
	if out := post(`{"safe_only":true}`); out.Code != http.StatusConflict || !h.state(id).Process {
		t.Fatalf("busy safe-only stop: status %d, body %s", out.Code, out.Body)
	}

	h.holders.out(holder, replyLine("u2", "done"), resultLine(), stateLine(HarnessIdle))
	h.idle(id)
	if out := post(`{"safe_only":true}`); out.Code != http.StatusOK || h.state(id).Process {
		t.Fatalf("idle safe-only stop: status %d, body %s", out.Code, out.Body)
	}

	h.send(id, "wake for a legacy stop")
	eventually(t, "new process running", func() bool { return h.state(id).Process })
	if out := post(""); out.Code != http.StatusOK || h.state(id).Process {
		t.Fatalf("legacy stop: status %d, body %s", out.Code, out.Body)
	}
}

func TestSafeOnlyStopRefusesOverlappingDescendantTransitions(t *testing.T) {
	h := newLeadHarness(t)
	h.opts.Trusted = func(*http.Request) (bool, bool) { return true, true }
	h.start()
	id, _ := h.settledLead()
	parent := &ParentRef{Kind: ParentLead, ReplyID: id}
	a := &conv{r: h.runner, rec: Record{ReplyID: "child-a", Parent: parent}, lastReg: h.opts.Now()}
	b := &conv{r: h.runner, rec: Record{ReplyID: "child-b", Parent: parent}, lastReg: h.opts.Now(), turn: true}
	h.runner.mu.Lock()
	h.runner.convs[a.rec.ReplyID], h.runner.convs[b.rec.ReplyID] = a, b
	h.runner.indexChildLocked(a.rec)
	h.runner.indexChildLocked(b.rec)
	h.runner.mu.Unlock()
	defer func() {
		h.runner.mu.Lock()
		delete(h.runner.convs, a.rec.ReplyID)
		delete(h.runner.convs, b.rec.ReplyID)
		h.runner.mu.Unlock()
	}()

	// B has work while A is idle. Keep B locked while A accepts work and
	// B settles, so there is no point at which both descendants are safe.
	b.mu.Lock()
	a.mu.Lock()
	a.turn = true
	b.turn = false
	a.mu.Unlock()
	out := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/agents/"+id+"/stop", strings.NewReader(`{"safe_only":true}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.runner.Handler().ServeHTTP(rec, req)
		out <- rec
	}()
	select {
	case rec := <-out:
		b.mu.Unlock()
		if rec.Code != http.StatusConflict || !h.state(id).Process {
			t.Fatalf("contended safe-only stop: status %d, body %s", rec.Code, rec.Body)
		}
	case <-time.After(2 * time.Second):
		b.mu.Unlock()
		<-out
		t.Fatal("safe Stop waited for a descendant transition instead of refusing admission")
	}
}

func TestSafeOnlyStopAllowsUnrelatedLeadContention(t *testing.T) {
	h := newLeadHarness(t).start()
	id, _ := h.settledLead()
	otherID, _ := h.settledLead()
	other := h.runner.conv(otherID)
	other.mu.Lock()
	out := make(chan reply, 1)
	var advisorySafe bool
	go func() {
		advisorySafe = h.state(id).SafeStop
		out <- h.route(localBrowser, http.MethodPost, "/agents/"+id+"/stop", map[string]bool{"safe_only": true})
	}()
	select {
	case res := <-out:
		other.mu.Unlock()
		if !advisorySafe || res.status != http.StatusOK || h.state(id).Process || !h.state(otherID).Process {
			t.Fatalf("safe-only stop with a separate lead locked: advisory safe_stop=%v, status %d, body %s",
				advisorySafe, res.status, res.body)
		}
	case <-time.After(2 * time.Second):
		other.mu.Unlock()
		<-out
		t.Fatal("safe Stop waited on a separate lead")
	}
}

// releaseNotifyingRunner can pause the release loop immediately after the ended
// registration becomes visible to the dashboard's index.
type releaseNotifyingRunner struct {
	*Runner
	afterNotify func(string)
}

func (r releaseNotifyingRunner) StartForPublication(notify func(string, string), serving <-chan struct{}) {
	r.Runner.StartForPublication(func(id, project string) {
		notify(id, project)
		r.afterNotify(id)
	}, serving)
}

func TestSafeOnlyStopFindsRunningGrandchildThroughReleasedWorker(t *testing.T) {
	testReleasedWorkerDelete(t, releasedWorkerDeleteOptions{})
}

func TestDeadBindingCheckoutDeleteSurvivesReplacement(t *testing.T) {
	testReleasedWorkerDelete(t, releasedWorkerDeleteOptions{restart: true})
}

func TestDeadBindingCheckoutDeleteWhileReleasePublishesEndedRow(t *testing.T) {
	testReleasedWorkerDelete(t, releasedWorkerDeleteOptions{restart: true, pauseRelease: true})
}

// The interrupted-delete variants run with a released holder whose stop
// fails (wedged): the release keeps it in the persisted closure and in the
// checkout's occupancy, a replacement daemon whose holder inventory is
// incomplete restores that occupancy, and the next complete startup
// inventory ends it (finalizeDeadHolders).
func TestDeadBindingInterruptedCheckoutDeleteRestoresOccupancy(t *testing.T) {
	testReleasedWorkerDelete(t, releasedWorkerDeleteOptions{restart: true, interruptDelete: true, wedged: true})
}

func TestDeadBindingInterruptedCheckoutDeleteWithChangedCwd(t *testing.T) {
	testReleasedWorkerDelete(t, releasedWorkerDeleteOptions{restart: true, interruptDelete: true, changedCwd: true, wedged: true})
}

type releasedWorkerDeleteOptions struct {
	restart         bool
	pauseRelease    bool
	interruptDelete bool
	changedCwd      bool
	// wedged makes the released worker's holder impossible to end, so the
	// release's stop fails and the holder stays in the closure.
	wedged bool
}

func testReleasedWorkerDelete(t *testing.T, test releasedWorkerDeleteOptions) {
	t.Helper()
	if test.wedged {
		// The release waits closeGrace for a child that ignores its input
		// EOF. Set before any runner starts, restored after the last stops.
		restoreGrace := closeGrace
		closeGrace = 20 * time.Millisecond
		t.Cleanup(func() { closeGrace = restoreGrace })
	}
	h, _ := configureHeldWorkerHarness(t)
	mail, refusals := newFakeMail(), &wakeRefusals{}
	if test.restart {
		h.opts.Mail = mail
		h.opts.WakeRefused = refusals.add
	}
	worktree, err := filepath.EvalSymlinks(releaseTestWorktree(t, h.root))
	if err != nil {
		t.Fatal(err)
	}
	h.runner = NewRunner(h.opts)
	opts := wsdashboard.DaemonOptions{
		Dir: h.dir, Home: t.TempDir(), Agents: h.runner, Sessions: leadSessions{},
		WorkspacesFile: filepath.Join(t.TempDir(), "workspaces.json"),
	}
	var pauseID atomic.Pointer[string]
	ended, resume := make(chan struct{}), make(chan struct{})
	var paused, resumed sync.Once
	unpause := func() { resumed.Do(func() { close(resume) }) }
	if test.pauseRelease {
		opts.Agents = releaseNotifyingRunner{h.runner, func(id string) {
			if target := pauseID.Load(); target == nil || id != *target {
				return
			}
			row, err := wsdashboard.ReadRegistration(h.dir, id)
			if err == nil && !row.Live(time.Now()) {
				paused.Do(func() { close(ended); <-resume })
			}
		}}
	}
	d, err := wsdashboard.StartDaemon(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	t.Cleanup(h.holders.drain)
	defer unpause()
	base := fmt.Sprintf("http://127.0.0.1:%d", d.Endpoint().Port)
	cookie := daemonCookieForTest(t, d)
	post := func(path string, body any) reply {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, base+path, bytes.NewReader(raw))
		req.RemoteAddr = localBrowser.remote
		req.Header.Set("Origin", base)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		d.Handler().ServeHTTP(res, req)
		return reply{res.Code, res.Body.String()}
	}
	if res := post("/rail/workspaces/add", map[string]string{"path": h.root}); res.status != http.StatusOK {
		t.Fatalf("add workspace: status %d, body %s", res.status, res.body)
	}
	leadID, _ := h.settledLead()
	lead := ParentRef{Kind: ParentLead, ReplyID: leadID}
	spawn := func(parent ParentRef, root, cwd, prompt string) (string, string) {
		t.Helper()
		st, err := h.runner.Spawn(SpawnSpec{
			Profile:            AgentWorkerProfile(BackendClaude),
			PermissionLevel:    PermissionWorkdirWrite,
			delegatedAuthority: true,
			Root:               root,
			Cwd:                cwd,
			Prompt:             prompt,
			Tier:               "small",
			Parent:             &parent,
		})
		if err != nil {
			t.Fatal(err)
		}
		holder := h.holder(st.ID)
		h.holders.out(holder, stateLine(HarnessRunning), initLine(prompt, "dontAsk"))
		eventually(t, prompt+" running", func() bool { return h.state(st.ID).Status == StatusRunning })
		return st.ID, holder
	}
	middleRoot, middleCwd := worktree, ""
	if test.changedCwd {
		middleRoot, middleCwd = h.root, filepath.Join(t.TempDir(), "worktree-link")
		if err := os.Symlink(worktree, middleCwd); err != nil {
			t.Fatal(err)
		}
	}
	middleID, middleHolder := spawn(lead, middleRoot, middleCwd, "middle")
	middleKey := h.record(middleID).WSKey
	leafID, leafHolder := spawn(ParentRef{Kind: ParentConversation, ReplyID: middleID}, h.root, "", "grandchild")
	if test.wedged {
		h.holders.set(func() {
			h.holders.wedged = map[string]error{middleHolder: fmt.Errorf("holder %s: stop refused", middleHolder)}
		})
	}
	holderRunning := func() bool {
		for _, holder := range h.holders.List().Holders {
			if holder.ID == middleHolder && holder.Child != nil && holder.Child.State == "running" {
				return true
			}
		}
		return false
	}
	// A forced release ends the released worker's holder and child, then
	// persists the closure with the holder forgotten; a holder whose stop
	// fails stays running and named in the persisted closure.
	checkReleasedHolder := func(when string) {
		t.Helper()
		if n := h.holders.opCount("stop " + middleHolder); n != 1 {
			t.Errorf("%s: the release asked holder %s to stop %d times, want once: %v", when, middleHolder, n, h.holders.opList())
		}
		rec := h.record(middleID)
		if rec.ClosedAt == "" || rec.DeadBindingClosure == nil || rec.DeadBindingClosure.Checkout != worktree {
			t.Errorf("%s: the release closure is not on disk: %+v", when, rec)
		}
		if test.wedged {
			if !holderRunning() || rec.HolderID != middleHolder {
				t.Errorf("%s: a holder whose stop failed must stay running and in the closure: running=%v, record holder %q", when, holderRunning(), rec.HolderID)
			}
			return
		}
		if holderRunning() || rec.HolderID != "" || rec.HostPID != 0 || rec.HolderSeq != 0 {
			t.Errorf("%s: the released worker's holder survived the release: running=%v, record %+v", when, holderRunning(), rec)
		}
		if got, ok := boundByAddress(h.runner.ActiveChildrenInWorktree(worktree))[AgentAddress(middleID)]; ok {
			t.Errorf("%s: the stopped released worker still occupies the checkout: %+v", when, got)
		}
	}
	if st := h.state(leadID); !st.Process || st.SafeStop {
		t.Fatalf("lead before checkout release = %+v, want a process and unsafe Stop", st)
	}

	clock := newTestClock()
	setClock(clock, h.opts.Now())
	at := releaseAfterBindings(t, clock, worktree)
	outcome := make(chan []ReleasedConversation, 1)
	release := func() { outcome <- h.runner.CheckoutReleased(worktree, at, true) }
	checkRelease := func() {
		t.Helper()
		var released []ReleasedConversation
		select {
		case released = <-outcome:
		case <-time.After(5 * time.Second):
			t.Fatal("release did not finish")
		}
		if len(released) != 1 || released[0].Address != AgentAddress(middleID) || released[0].State != AgentClosed {
			t.Fatalf("release outcomes = %+v, want only the middle worker closed", released)
		}
	}
	checkTree := func(r *Runner) {
		t.Helper()
		rows := SpawnTree(lead, r.Children)
		if len(rows) != 2 || rows[0].ID != middleID || rows[0].Status != StatusClosed ||
			rows[1].ID != leafID || rows[1].Parent == nil || rows[1].Parent.ReplyID != middleID {
			t.Fatalf("spawn tree = %+v, want the closed middle worker and its grandchild", rows)
		}
	}
	if test.pauseRelease {
		pauseID.Store(&middleID)
		go release()
		wait(t, "ended registration", ended)
		if h.runner.conv(middleID) == nil {
			t.Fatal("release moved the worker out of the conversation registry before delete")
		}
	} else {
		release()
		checkRelease()
		checkTree(h.runner)
		if st := h.state(leafID); st.Status != StatusRunning {
			t.Fatalf("grandchild after release = %+v, want running", st)
		}
	}
	if test.restart {
		checkReleasedHolder("before deletion")
	}

	stop := func() reply {
		return post("/agents/"+leadID+"/stop", map[string]bool{"safe_only": true})
	}
	assertStopRefused := func(when string) {
		t.Helper()
		st := h.state(leadID)
		meta, ok := h.runner.SessionMeta(leadID)
		res := stop()
		if !st.Process || st.SafeStop || !ok || !meta.HasProcess || meta.SafeStop ||
			res.status != http.StatusConflict || !h.state(leadID).Process {
			t.Fatalf("%s: advisory safe_stop=%v, metadata safe_stop=%v, Stop status=%d, body=%s",
				when, st.SafeStop, meta.SafeStop, res.status, res.body)
		}
	}
	if !test.pauseRelease {
		assertStopRefused("busy grandchild before checkout delete")
	}

	d.Index().Refresh()
	if s, ok := d.Index().Session(middleID); !ok || s.Registration.Live(time.Now()) {
		t.Fatalf("dashboard session for the released worker = %+v, present=%v, want ended", s, ok)
	}
	if test.interruptDelete {
		// Stop at the dashboard's pre-delete persistence boundary, with
		// the checkout and release marker still in place.
		if err := h.runner.PrepareWorktreeHistoryPurge(worktree, map[string]bool{middleID: true}); err != nil {
			t.Fatal(err)
		}
		// The holder whose stop failed is still named by the closure.
		rec := h.record(middleID)
		if rec.ClosedAt == "" || rec.DeadBindingClosure == nil ||
			rec.DeadBindingClosure.Checkout != worktree || rec.HolderID != middleHolder {
			t.Fatalf("pre-delete record did not persist the held release closure: %+v", rec)
		}
		if got := boundByAddress(h.runner.ActiveChildrenInWorktree(worktree))[AgentAddress(middleID)]; !got.Live || got.Holder != middleHolder {
			t.Fatalf("released worker must occupy the checkout before replacement: %+v", got)
		}
	} else {
		if res := post("/rail/worktrees/delete", map[string]string{"workspace": h.root, "path": worktree}); res.status != http.StatusOK {
			t.Fatalf("delete checkout: status %d, body %s", res.status, res.body)
		}
		if _, err := os.Stat(worktree); !os.IsNotExist(err) {
			t.Fatalf("deleted checkout: stat error = %v, want missing directory", err)
		}
	}
	if test.pauseRelease {
		unpause()
		checkRelease()
	}
	if test.restart {
		_, dirErr := os.Stat(wsdashboard.SessionDir(h.dir, middleID))
		d.Close()
		h.holders.restart()
		if test.wedged {
			// The replacement's holder inventory is incomplete, so it cannot
			// end the retained holder at startup (finalizeDeadHolders).
			h.holders.set(func() { h.holders.incomplete = true })
		}
		if test.changedCwd {
			if err := os.Remove(middleCwd); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(h.root, middleCwd); err != nil {
				t.Fatal(err)
			}
		}
		h.runner = NewRunner(h.opts)
		opts.Agents = h.runner
		successor, err := wsdashboard.StartDaemon(opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(successor.Close)
		if dirErr != nil {
			t.Errorf("retained session directory after checkout delete: %v", dirErr)
		}
		if c := h.runner.conv(middleID); c != nil {
			t.Errorf("released worker rejoined after checkout deletion under holder %s", c.record().HolderID)
		}
		wait(t, "successor holder sweep", h.runner.rejoined)
		if test.wedged {
			if !holderRunning() || h.record(middleID).HolderID != middleHolder || h.holders.opCount("stop "+middleHolder) != 1 {
				t.Errorf("an incomplete inventory ended or forgot the retained holder: running=%v, record %+v, ops %v", holderRunning(), h.record(middleID), h.holders.opList())
			}
		} else if holderRunning() || h.holders.opCount("stop "+middleHolder) != 1 {
			t.Errorf("the released worker's holder came back after replacement: running=%v, ops %v", holderRunning(), h.holders.opList())
		}
		mail.deliverFrom(middleKey, leadID, "mail for the released worker")
		h.runner.DrainDeadBindingMail(middleID)
		if got := refusals.list(); len(got) != 1 || got[0][0] != leadID || got[0][1] != middleID || !strings.Contains(got[0][2], worktree) {
			t.Errorf("mail after replacement produced notices %v, want a release refusal naming %s", got, worktree)
		}
		checkTree(h.runner)
		got := boundByAddress(h.runner.ActiveChildrenInWorktree(worktree))[AgentAddress(middleID)]
		if !test.wedged {
			if got.Address != "" {
				t.Errorf("worker for the deleted checkout occupies it after replacement: %+v", got)
			}
			return
		}
		if !got.Live || got.State != AgentClosed || !got.BoundBeforeRelease || got.Holder != middleHolder {
			t.Fatalf("released worker occupancy after replacement = %+v, want closed and live under %s", got, middleHolder)
		}
		// Under the incomplete census an exited child no longer occupies the
		// checkout, though its holder is still listed.
		h.holders.exit(middleHolder, 0)
		if got := h.runner.ActiveChildrenInWorktree(worktree); len(got) != 0 {
			t.Fatalf("exited worker still blocks checkout reuse: %+v", got)
		}
		// The next start's complete holder inventory ends the retained
		// holder and forgets it in the closure.
		successor.Close()
		h.holders.restart()
		h.holders.set(func() {
			h.holders.incomplete = false
			delete(h.holders.wedged, middleHolder)
		})
		h.runner = NewRunner(h.opts)
		opts.Agents = h.runner
		third, err := wsdashboard.StartDaemon(opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(third.Close)
		wait(t, "complete holder sweep", h.runner.rejoined)
		eventually(t, "the retained holder ended and forgotten", func() bool {
			rec := h.record(middleID)
			return h.holders.opCount("stop "+middleHolder) == 2 && rec.HolderID == "" && rec.HostPID == 0
		})
		if rec := h.record(middleID); rec.ClosedAt == "" || rec.DeadBindingClosure == nil || rec.DeadBindingClosure.Checkout != worktree {
			t.Errorf("finalizing the retained holder lost the closure: %+v", rec)
		}
		if holderRunning() {
			t.Error("the retained holder still runs after a complete startup inventory")
		}
		if got := h.runner.ActiveChildrenInWorktree(worktree); len(got) != 0 {
			t.Errorf("the ended retained holder still occupies the checkout: %+v", got)
		}
		checkTree(h.runner)
		return
	}
	assertStopRefused("busy grandchild after checkout delete")
	if _, err := h.readRecord(middleID); err != nil {
		t.Fatalf("session record for restart after checkout delete: %v", err)
	}

	h.holders.out(leafHolder, recoveryWorkerReply("done")...)
	h.idle(leafID)
	if st := h.state(leadID); !st.SafeStop {
		t.Fatalf("lead with idle grandchild and closed middle worker = %+v, want safe Stop", st)
	}
	if res := stop(); res.status != http.StatusOK || h.state(leadID).Process {
		t.Fatalf("safe-only Stop after the grandchild settled: status %d, body %s", res.status, res.body)
	}
}

// execPublishingHolder publishes a new immutable count snapshot at the
// holder check inside the Stop admission.
type execPublishingHolder struct {
	Process
	holderProcess
	publish func()
	once    sync.Once
}

func (p *execPublishingHolder) HolderInfo() (string, int, bool) {
	p.once.Do(p.publish)
	return p.holderProcess.HolderInfo()
}

func TestSafeOnlyStopScopesExecPublication(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target bool
		status int
	}{
		{name: "separate lead", status: http.StatusOK},
		{name: "target lead", target: true, status: http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLeadHarness(t).start()
			id, _ := h.settledLead()
			owner, _ := h.settledLead()
			if tc.target {
				owner = id
			}
			root := h.runner.conv(id)
			counts := map[string]int{owner: 1}
			if !root.do(func() {
				root.mu.Lock()
				defer root.mu.Unlock()
				root.proc = &execPublishingHolder{
					Process: root.proc, holderProcess: root.proc.(holderProcess),
					publish: func() { h.runner.exec.live.Store(&counts) },
				}
			}) {
				t.Fatal("target conversation exited before Stop")
			}
			res := h.route(localBrowser, http.MethodPost, "/agents/"+id+"/stop", map[string]bool{"safe_only": true})
			if res.status != tc.status || h.runner.execLive(owner) != 1 || h.state(id).Process != tc.target {
				t.Fatalf("safe-only stop with a new exec count for %s: status %d, body %s", owner, res.status, res.body)
			}
		})
	}
}

func TestSafeOnlyStopRefusesPendingDescendantSpawn(t *testing.T) {
	h := newLeadHarness(t).start()
	id, _ := h.settledLead()
	parent := &ParentRef{Kind: ParentLead, ReplyID: id}
	child := &conv{r: h.runner, rec: Record{ReplyID: "child", Parent: parent}, lastReg: h.opts.Now()}
	h.runner.mu.Lock()
	h.runner.convs[child.rec.ReplyID] = child
	h.runner.indexChildLocked(child.rec)
	h.runner.mu.Unlock()
	defer func() {
		h.runner.mu.Lock()
		delete(h.runner.convs, child.rec.ReplyID)
		delete(h.runner.closed, child.rec.ReplyID)
		h.runner.mu.Unlock()
	}()

	_, release, err := h.runner.reserveSpawnWidth(ParentRef{Kind: ParentConversation, ReplyID: child.rec.ReplyID})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	post := func() reply {
		return h.route(localBrowser, http.MethodPost, "/agents/"+id+"/stop", map[string]bool{"safe_only": true})
	}
	if res := post(); res.status != http.StatusConflict || !h.state(id).Process {
		t.Fatalf("safe-only stop during a pending grandchild spawn: status %d, body %s", res.status, res.body)
	}

	// Retain the middle row after close while its spawn is still in flight.
	h.runner.mu.Lock()
	delete(h.runner.convs, child.rec.ReplyID)
	h.runner.closed[child.rec.ReplyID] = child.rec
	h.runner.mu.Unlock()
	if st := h.state(id); st.SafeStop {
		t.Fatal("pending grandchild spawn behind a closed worker advertised safe Stop")
	}
	if res := post(); res.status != http.StatusConflict || !h.state(id).Process {
		t.Fatalf("safe-only stop during a pending spawn behind a closed worker: status %d, body %s", res.status, res.body)
	}
	release()
	if res := post(); res.status != http.StatusOK || h.state(id).Process {
		t.Fatalf("safe-only stop after releasing spawn reservation: status %d, body %s", res.status, res.body)
	}
}
