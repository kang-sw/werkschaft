package wsagent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
