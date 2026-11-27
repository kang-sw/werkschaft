package wsagent

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

// TestInboxChangedWakesParkedChildAndRacesWithMaybePark verifies Finding 1:
// dashboard direct inbox writes + Runner.InboxChanged rehydrate a parked child
// and deliver the message, and rehydration kicks already-held disk inbox
// entries even if a write landed before or during parking.
func TestInboxChangedWakesParkedChildAndRacesWithMaybePark(t *testing.T) {
	t.Run("inbox changed wakes parked child", func(t *testing.T) {
		h, _ := agentHarness(t, func(o *Options) { o.ParkIdleChildren = true })
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		ap.reply("done 1")
		eventually(t, "turn 1 settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		eventually(t, "child parked", func() bool { return h.runner.isParked(a) && !h.runner.residentConv(a) })

		n := h.backend.startCount()
		inbox := wsinbox.Dir(wsdashboard.SessionDir(h.dir, a))
		if _, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"from dashboard"}}, time.Now()); err != nil {
			t.Fatal(err)
		}
		h.runner.InboxChanged(a)

		p := h.backend.waitProc(t, n)
		if got := p.nextSent(t); !strings.Contains(got, "from dashboard") {
			t.Fatalf("woken turn = %q, want dashboard message", got)
		}
		p.reply("done 2")
	})

	t.Run("rehydration kicks already-held inbox entries", func(t *testing.T) {
		h, _ := agentHarness(t, func(o *Options) { o.ParkIdleChildren = true })
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		ap.reply("done 1")
		eventually(t, "turn 1 settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		eventually(t, "child parked", func() bool { return h.runner.isParked(a) && !h.runner.residentConv(a) })

		n := h.backend.startCount()
		inbox := wsinbox.Dir(wsdashboard.SessionDir(h.dir, a))
		if _, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"held across park"}}, time.Now()); err != nil {
			t.Fatal(err)
		}
		// Unpark directly without calling InboxChanged (e.g., mail or control wake).
		if c := h.runner.unpark(a); c == nil {
			t.Fatal("unpark returned nil")
		}
		p := h.backend.waitProc(t, n)
		if got := p.nextSent(t); !strings.Contains(got, "held across park") {
			t.Fatalf("rehydrated turn = %q, want held inbox entry", got)
		}
		p.reply("done 2")
	})

	t.Run("inbox changed concurrent with maybePark never strands message", func(t *testing.T) {
		h, _ := agentHarness(t, func(o *Options) { o.ParkIdleChildren = true })
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		ap.reply("done 1")
		eventually(t, "turn 1 settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		// Stop the process so maybePark races with the inbox write + InboxChanged.
		n := h.backend.startCount()
		h.asleep(a)
		inbox := wsinbox.Dir(wsdashboard.SessionDir(h.dir, a))
		if _, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"race with park"}}, time.Now()); err != nil {
			t.Fatal(err)
		}
		h.runner.InboxChanged(a)

		p := h.backend.waitProc(t, n)
		if got := p.nextSent(t); !strings.Contains(got, "race with park") {
			t.Fatalf("woken turn = %q, want message", got)
		}
		p.reply("done 2")
	})
}

// TestAdmitCompletionRetriesAcrossMaybePark verifies Finding 2: if a child
// parks between AdmitCompletion's r.open lookup and acquiring c.wmu+c.mu,
// AdmitCompletion detects c.parkedOut, rehydrates the child, and delivers the
// completion notice to the new live incarnation.
func TestAdmitCompletionRetriesAcrossMaybePark(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var hooked atomic.Bool

	h, _ := agentHarness(t, func(o *Options) {
		o.ParkIdleChildren = true
		o.admitCompletionAdmitting = func(string) {
			if hooked.CompareAndSwap(false, true) {
				close(entered)
				<-release
			}
		}
	})
	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
	ap.reply("first done")
	eventually(t, "first turn settled", func() bool {
		rec := h.record(a)
		return rec.LastTurn != nil && !rec.TurnOpen
	})
	owner, err := h.runner.ExecOwner(a)
	if err != nil {
		t.Fatal(err)
	}
	// Put the child to sleep, wait until it is still resident or let AdmitCompletion
	// fetch it before it parks: start AdmitCompletion while resident, then stop
	// the process so maybePark parks the fetched *conv while AdmitCompletion is paused.
	n := h.backend.startCount()
	done := make(chan struct {
		out CompletionOutcome
		err error
	}, 1)
	go func() {
		out, err := h.runner.AdmitCompletion(a, owner.Gen, "exec:race-park:exit", "[ws exec] job finished")
		done <- struct {
			out CompletionOutcome
			err error
		}{out, err}
	}()

	wait(t, "AdmitCompletion paused after open", entered)
	h.asleep(a)
	eventually(t, "child parked while AdmitCompletion paused", func() bool {
		return h.runner.isParked(a) && !h.runner.residentConv(a)
	})
	close(release)

	res := <-done
	if res.err != nil || res.out != CompletionAccepted {
		t.Fatalf("AdmitCompletion = (%v, %v), want (accepted, nil)", res.out, res.err)
	}
	if !h.runner.residentConv(a) || h.runner.isParked(a) {
		t.Fatal("child was not rehydrated after AdmitCompletion retried across park")
	}
	p := h.backend.waitProc(t, n)
	if got := p.nextSent(t); !strings.Contains(got, "[ws exec] job finished") {
		t.Fatalf("woken turn = %q, want completion notice", got)
	}
	p.reply("handled completion")
}

// TestExpiryAbortsWhenParkedDescendantWakesBeforeCloseCascade verifies Finding 3:
// if a parked descendant receives fresh work between expiry's snapshot and
// closeCascade, the admission fence aborts expiry so neither the descendant's
// fresh work nor its ancestor is deleted.
func TestExpiryAbortsWhenParkedDescendantWakesBeforeCloseCascade(t *testing.T) {
	clk := newTestClock()
	var wakeOnce atomic.Bool
	woke := make(chan struct{})
	var h *harness
	var childID string
	var rootID string

	h, _ = agentHarness(t, func(o *Options) {
		o.Now = clk.Now
		o.ChildWaitingDays = 2
		o.ParkIdleChildren = true
		o.beforeExpiryCascade = func(id string) {
			if id == rootID && wakeOnce.CompareAndSwap(false, true) {
				if _, err := h.runner.Send(childID, "fresh descendant work"); err != nil {
					t.Errorf("Send(%s) during expiry race: %v", childID, err)
				}
				close(woke)
			}
		}
	})
	a, b := settledPair(t, h)
	rootID, childID = a, b
	// Make descendant younger than ancestor so ancestor expires first while
	// descendant is still parked and cannot expire independently before it.
	h.runner.conv(b).update(func(rec *Record) { rec.LastActiveAt = clk.Now().Add(day).UTC().Format(time.RFC3339) })
	eventually(t, "both ancestor and descendant parked", func() bool {
		return h.runner.isParked(a) && h.runner.isParked(b) && !h.runner.residentConv(a) && !h.runner.residentConv(b)
	})

	n := h.backend.startCount()
	clk.advance(2*day + 12*time.Hour)
	wait(t, "beforeExpiryCascade woke descendant", woke)

	p := h.backend.waitProc(t, n)
	if got := p.nextSent(t); !strings.Contains(got, "fresh descendant work") {
		t.Fatalf("descendant turn = %q, want fresh descendant work", got)
	}
	// Give expiry goroutine time to settle its abort path.
	time.Sleep(150 * time.Millisecond)
	if gone(h, a) || gone(h, b) {
		t.Fatalf("expiry deleted active subtree: gone(%s)=%v gone(%s)=%v", a, gone(h, a), b, gone(h, b))
	}
	if st := h.state(a); st.Status == StatusClosed {
		t.Fatalf("ancestor was closed despite active descendant: %+v", st)
	}
	if st := h.state(b); st.Status == StatusClosed {
		t.Fatalf("descendant was closed despite fresh work: %+v", st)
	}
	p.reply("fresh work done")
	eventually(t, "descendant finished fresh turn", func() bool {
		rec := h.record(b)
		return rec.LastTurn != nil && rec.LastTurn.Result.Message == "fresh work done"
	})
}

type fakeParkedMailWatcher struct {
	mu            sync.Mutex
	active        map[*func()]struct{}
	stopped       int
	all           []func()
	onWatchParked func()

	resActive  map[*func()]struct{}
	resStopped int
	onWatch    func()
}

func (f *fakeParkedMailWatcher) Watch(root, key string, changed func()) func() {
	f.mu.Lock()
	hook := f.onWatch
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	if f.resActive == nil {
		f.resActive = map[*func()]struct{}{}
	}
	cb := &changed
	f.resActive[cb] = struct{}{}
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		if _, ok := f.resActive[cb]; ok {
			delete(f.resActive, cb)
			f.resStopped++
		}
		f.mu.Unlock()
	}
}

func (f *fakeParkedMailWatcher) Drain(root, key string, relation func(sender string) string) ([]ClaimedMail, error) {
	return nil, nil
}

func (f *fakeParkedMailWatcher) Return(root, key string, texts []string) error {
	return nil
}

func (f *fakeParkedMailWatcher) WatchParked(root, key string, changed func()) func() {
	f.mu.Lock()
	hook := f.onWatchParked
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	if f.active == nil {
		f.active = map[*func()]struct{}{}
	}
	cb := &changed
	f.active[cb] = struct{}{}
	f.all = append(f.all, changed)
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		if _, ok := f.active[cb]; ok {
			delete(f.active, cb)
			f.stopped++
		}
		f.mu.Unlock()
	}
}

func (f *fakeParkedMailWatcher) counts() (active, stopped int, callbacks []func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]func(), len(f.all))
	copy(out, f.all)
	return len(f.active), f.stopped, out
}

func (f *fakeParkedMailWatcher) residentCounts() (active, stopped int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.resActive), f.resStopped
}

// TestRunnerCloseCancelsParkedWatchesAndFencesLateWakeCallbacks verifies Finding 4:
// Runner.Close cancels and removes all parked mail watch handles before waiting
// on r.wg, and late/concurrent parked-mail wake callbacks cannot race r.wg.Add
// against r.wg.Wait.
func TestRunnerCloseCancelsParkedWatchesAndFencesLateWakeCallbacks(t *testing.T) {
	mail := &fakeParkedMailWatcher{}
	h, _ := agentHarness(t, func(o *Options) {
		o.ParkIdleChildren = true
		o.Mail = mail
	})
	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
	ap.reply("done")
	eventually(t, "turn settled", func() bool {
		rec := h.record(a)
		return rec.LastTurn != nil && !rec.TurnOpen
	})
	h.asleep(a)
	eventually(t, "child parked with mail watch", func() bool {
		active, _, _ := mail.counts()
		return h.runner.isParked(a) && active == 1
	})

	_, _, callbacks := mail.counts()
	if len(callbacks) == 0 {
		t.Fatal("expected parked mail watch callback")
	}

	// Fire wake callbacks concurrently with Runner.Close AND after Close returns.
	var fireWG sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		fireWG.Add(1)
		go func() {
			defer fireWG.Done()
			<-start
			for _, cb := range callbacks {
				cb()
			}
		}()
	}
	close(start)
	h.runner.Close()
	fireWG.Wait()

	// Also invoke after Close completed to confirm no WaitGroup reuse panic/race.
	for _, cb := range callbacks {
		cb()
	}

	active, stopped, _ := mail.counts()
	if active != 0 || stopped < 1 {
		t.Fatalf("parked watches after Close: active=%d stopped=%d, want active=0 stopped>=1", active, stopped)
	}
}

// TestPageInputRetriesAcrossPark verifies Runner.PageInput retries when a
// resident conversation parks between conv lookup and wmu+mu admission,
// rehydrating the child without returning errUnknown or invoking write twice.
func TestPageInputRetriesAcrossPark(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var hooked atomic.Bool

	h, _ := agentHarness(t, func(o *Options) {
		o.ParkIdleChildren = true
		o.pageInputAdmitting = func(string) {
			if hooked.CompareAndSwap(false, true) {
				close(entered)
				<-release
			}
		}
	})
	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
	ap.reply("done 1")
	eventually(t, "turn 1 settled", func() bool {
		rec := h.record(a)
		return rec.LastTurn != nil && !rec.TurnOpen
	})
	oldConv := h.runner.resident(a)
	if oldConv == nil {
		t.Fatal("expected resident conv before PageInput pause")
	}

	inbox := wsinbox.Dir(wsdashboard.SessionDir(h.dir, a))
	var calls atomic.Int32
	n := h.backend.startCount()
	errCh := make(chan error, 1)
	go func() {
		errCh <- h.runner.PageInput(a, func() bool {
			calls.Add(1)
			if _, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"page input across park"}}, time.Now()); err != nil {
				t.Errorf("wsinbox.Send: %v", err)
				return false
			}
			return true
		})
	}()

	// PageInput now holds the live resident *conv pointer right before wmu.Lock.
	wait(t, "PageInput paused with resident conv", entered)
	h.asleep(a)
	eventually(t, "resident conv parked out", func() bool {
		return h.runner.isParked(a) && !h.runner.residentConv(a) && oldConv.isParkedOut()
	})
	close(release)

	if err := <-errCh; err != nil {
		t.Fatalf("PageInput across park: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("write callback calls = %d, want 1", calls.Load())
	}
	p := h.backend.waitProc(t, n)
	if got := p.nextSent(t); !strings.Contains(got, "page input across park") {
		t.Fatalf("woken turn = %q, want page input message", got)
	}
	p.reply("done 2")
}

// TestExpiryAbortsOnSameSecondCompletedTurn verifies that even when the clock
// has not ticked (same RFC3339 second), a completed turn on a resident
// descendant between planExpiry and closeExpiryCascade invalidates the
// snapshotted sends/lastTurnID incarnation and aborts expiry.
func TestExpiryAbortsOnSameSecondCompletedTurn(t *testing.T) {
	clk := newTestClock()
	var ranOnce atomic.Bool
	raced := make(chan struct{})
	var h *harness
	var rootID, childID string

	h, _ = agentHarness(t, func(o *Options) {
		o.Now = clk.Now
		o.ChildWaitingDays = 2
		o.beforeExpiryCascade = func(id string) {
			if id == rootID && ranOnce.CompareAndSwap(false, true) {
				n := h.backend.startCount()
				if _, err := h.runner.Send(childID, "same-second fresh turn"); err != nil {
					t.Errorf("Send(%s): %v", childID, err)
					close(raced)
					return
				}
				p := h.backend.waitProc(t, n)
				p.nextSent(t)
				p.reply("same-second done")
				eventually(t, "same-second turn settled", func() bool {
					rec := h.record(childID)
					return rec.LastTurn != nil && rec.LastTurn.Result.Message == "same-second done" && !rec.TurnOpen
				})
				h.asleep(childID)
				close(raced)
			}
		}
	})
	a, b := settledPair(t, h)
	rootID, childID = a, b
	// Age both past expiry, then freeze clock so the raced turn completes in the
	// exact same RFC3339 second as expiry evaluation.
	clk.advance(3 * day)
	h.runner.conv(b).update(func(rec *Record) { rec.LastActiveAt = clk.Now().UTC().Format(time.RFC3339) })
	h.runner.conv(a).update(func(rec *Record) { rec.LastActiveAt = clk.Now().Add(-3 * day).UTC().Format(time.RFC3339) })

	deleted := h.runner.expire(a)
	wait(t, "beforeExpiryCascade completed same-second turn", raced)
	if deleted {
		t.Fatal("expire deleted subtree despite same-second completed turn on descendant")
	}
	if gone(h, a) || gone(h, b) {
		t.Fatalf("subtree removed despite aborted expiry: gone(%s)=%v gone(%s)=%v", a, gone(h, a), b, gone(h, b))
	}
	if st := h.state(a); st.Status == StatusClosed {
		t.Fatalf("root closed despite aborted expiry: %+v", st)
	}
	if st := h.state(b); st.Status == StatusClosed {
		t.Fatalf("descendant closed despite same-second turn: %+v", st)
	}
}

// TestPageInputAndDashboardChildMessageRacingExpiryNeverDropInput verifies
// Important #1:
//   - Direct PageInput commits pageInputs, LastActiveAt, and signalInbox before
//     releasing c.wmu, so a racing expiry aborts instead of closing/dropping it.
//   - Dashboard HTTP POST /s/{child}/message on a spawned child (Agent=true,
//     Hosted=false) routes through Runner.PageInput so it is serialized against
//     expiry under c.wmu (never returning HTTP 200 for a dropped message).
//   - Out-of-band direct wsinbox.Send written before expiry commits is caught by
//     hasFreshDiskHeldLocked under c.wmu+c.mu so expiry aborts without dropHeld.
func TestPageInputAndDashboardChildMessageRacingExpiryNeverDropInput(t *testing.T) {
	t.Run("direct PageInput commits before expiry acquires wmu", func(t *testing.T) {
		clk := newTestClock()
		var ranOnce atomic.Bool
		var h *harness
		var id string
		var startN int

		h, _ = agentHarness(t, func(o *Options) {
			o.Now = clk.Now
			o.ChildWaitingDays = 2
			o.beforeExpiryNodeClose = func(nodeID string) {
				if nodeID == id && ranOnce.CompareAndSwap(false, true) {
					inbox := wsinbox.Dir(wsdashboard.SessionDir(h.dir, id))
					if err := h.runner.PageInput(id, func() bool {
						_, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"direct page race"}}, clk.Now())
						return err == nil
					}); err != nil {
						t.Errorf("PageInput during expiry: %v", err)
					}
				}
			}
		})
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		ap.reply("done 1")
		eventually(t, "turn settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		id = a
		clk.advance(3 * day)
		startN = h.backend.startCount()

		if deleted := h.runner.expire(a); deleted {
			t.Fatal("expire closed and dropped accepted direct PageInput")
		}
		if gone(h, a) {
			t.Fatal("child deleted despite accepted direct PageInput")
		}
		p := h.backend.waitProc(t, startN)
		if got := p.nextSent(t); !strings.Contains(got, "direct page race") {
			t.Fatalf("woken turn = %q, want direct page race", got)
		}
		p.reply("done 2")
	})

	t.Run("dashboard HTTP child message routes through PageInput and aborts expiry", func(t *testing.T) {
		clk := newTestClock()
		var ranOnce atomic.Bool
		var h *harness
		var daemon *wsdashboard.Daemon
		var cookie *http.Cookie
		var id string
		postCode := make(chan int, 1)

		h, _ = agentHarness(t, func(o *Options) {
			o.Now = clk.Now
			o.ChildWaitingDays = 2
			o.beforeExpiryNodeClose = func(nodeID string) {
				if nodeID == id && ranOnce.CompareAndSwap(false, true) {
					req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/s/"+id+"/message", strings.NewReader(`{"text":"dashboard child msg"}`))
					req.RemoteAddr = "127.0.0.1:12345"
					req.Header.Set("Origin", "http://127.0.0.1")
					req.AddCookie(cookie)
					rec := httptest.NewRecorder()
					daemon.Handler().ServeHTTP(rec, req)
					postCode <- rec.Code
				}
			}
		})
		var err error
		daemon, err = wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
			Dir: h.dir, Port: 0, Sessions: hostedPageSessions{}, Agents: h.runner, PollInterval: time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(daemon.Close)
		cookie = daemonCookieForTest(t, daemon)

		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		ap.reply("done 1")
		eventually(t, "turn settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		eventually(t, "dashboard indexed spawned child", func() bool {
			s, ok := daemon.Index().Session(a)
			return ok && s.Agent && !s.Hosted
		})

		id = a
		clk.advance(3 * day)
		startN := h.backend.startCount()

		if deleted := h.runner.expire(a); deleted {
			t.Fatal("expire closed child despite dashboard HTTP message")
		}
		if code := <-postCode; code != http.StatusOK {
			t.Fatalf("POST /s/%s/message = %d, want 200", a, code)
		}
		p := h.backend.waitProc(t, startN)
		if got := p.nextSent(t); !strings.Contains(got, "dashboard child msg") {
			t.Fatalf("woken turn = %q, want dashboard child msg", got)
		}
		p.reply("done 2")
	})

	t.Run("out-of-band disk inbox entry aborts expiry before dropHeld", func(t *testing.T) {
		clk := newTestClock()
		var ranOnce atomic.Bool
		var h *harness
		var id string

		h, _ = agentHarness(t, func(o *Options) {
			o.Now = clk.Now
			o.ChildWaitingDays = 2
			o.beforeExpiryNodeClose = func(nodeID string) {
				if nodeID == id && ranOnce.CompareAndSwap(false, true) {
					inbox := wsinbox.Dir(wsdashboard.SessionDir(h.dir, id))
					if _, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"raw inbox write"}}, clk.Now()); err != nil {
						t.Errorf("wsinbox.Send: %v", err)
					}
				}
			}
		})
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		ap.reply("done 1")
		eventually(t, "turn settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		id = a
		clk.advance(3 * day)

		if deleted := h.runner.expire(a); deleted {
			t.Fatal("expire deleted child despite unconsumed disk inbox entry")
		}
		inbox := wsinbox.Dir(wsdashboard.SessionDir(h.dir, a))
		held, err := wsinbox.Held(inbox)
		if err != nil || len(held) != 1 {
			t.Fatalf("held after aborted expiry = %v, %v (want 1 preserved entry)", held, err)
		}
	})
}

// TestWatchParkedAfterUnparkDetachedStopDoesNotLeakOrBreakFuturePark verifies
// Important #2:
//   - If WatchParked returns while unpark already holds r.unparking[id] (after
//     deleting parkStop and before startConvWith publishes), watchParked sees
//     r.unparking[id] != nil and immediately stops the handle instead of
//     installing a stale parkStop.
//   - A subsequent park installs a fresh working parked watch (not blocked by a
//     leftover parkStop), and a stale callback from gen 1 cannot spuriously wake
//     the gen 3 parked incarnation.
func TestWatchParkedAfterUnparkDetachedStopDoesNotLeakOrBreakFuturePark(t *testing.T) {
	watchEntered := make(chan struct{})
	watchRelease := make(chan struct{})
	var blockedFirst atomic.Bool

	mail := &fakeParkedMailWatcher{
		onWatchParked: func() {
			if blockedFirst.CompareAndSwap(false, true) {
				close(watchEntered)
				<-watchRelease
			}
		},
	}
	h, _ := agentHarness(t, func(o *Options) {
		o.ParkIdleChildren = true
		o.Mail = mail
	})
	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
	ap.reply("done 1")
	eventually(t, "turn 1 settled", func() bool {
		rec := h.record(a)
		return rec.LastTurn != nil && !rec.TurnOpen
	})
	h.asleep(a)

	// Child is parked (gen 1) and its maybePark goroutine is paused inside WatchParked.
	wait(t, "first WatchParked entered", watchEntered)

	// Unpark while WatchParked is still in flight; unpark detaches (nil) parkStop
	// and publishes the resident conv.
	n := h.backend.startCount()
	if _, err := h.runner.Send(a, "wake while watch installing"); err != nil {
		t.Fatal(err)
	}
	p2 := h.backend.waitProc(t, n)

	// Release the stale gen-1 WatchParked; it must NOT leave a stale parkStop
	// or active watch behind.
	close(watchRelease)
	eventually(t, "stale gen-1 parked watch stopped", func() bool {
		active, stopped, _ := mail.counts()
		return active == 0 && stopped == 1
	})
	_, _, gen1Callbacks := mail.counts()
	if len(gen1Callbacks) != 1 {
		t.Fatalf("gen-1 callbacks = %d, want 1", len(gen1Callbacks))
	}

	// Settle turn 2 and let the child park a second time (gen 3).
	p2.reply("done 2")
	eventually(t, "turn 2 settled", func() bool {
		rec := h.record(a)
		return rec.LastTurn != nil && rec.LastTurn.Result.Message == "done 2" && !rec.TurnOpen
	})
	h.asleep(a)
	eventually(t, "child parked second time with fresh watch", func() bool {
		active, _, _ := mail.counts()
		return h.runner.isParked(a) && !h.runner.residentConv(a) && active == 1
	})

	// Firing the stale gen-1 callback must NOT wake the gen-3 parked child.
	gen1Callbacks[0]()
	// Give any erroneously spawned goroutine a chance to run.
	time.Sleep(30 * time.Millisecond)
	if !h.runner.isParked(a) || h.runner.residentConv(a) {
		t.Fatal("stale gen-1 WatchParked callback woke gen-3 parked child")
	}

	// Firing the current gen-3 callback MUST wake the child cleanly.
	_, _, allCallbacks := mail.counts()
	if len(allCallbacks) < 2 {
		t.Fatalf("expected fresh gen-3 callback, got %d", len(allCallbacks))
	}
	allCallbacks[len(allCallbacks)-1]()
	eventually(t, "fresh gen-3 callback woke child", func() bool {
		return h.runner.residentConv(a) && !h.runner.isParked(a)
	})
}

// TestExpiryCascadeFencesConcurrentChildSpawn verifies Important #3:
//   - An AgentSpawn arriving after admitsExpiryTree released r.mu and before
//     closeOneExpiry runs waits on r.expiring instead of publishing a live child
//     under a closing parent, and returns errClosed once expiry completes.
//   - If the expiry cascade aborts (e.g., input arrives on root), the waiting
//     AgentSpawn unblocks and succeeds cleanly.
func TestExpiryCascadeFencesConcurrentChildSpawn(t *testing.T) {
	t.Run("spawn during completing expiry returns errClosed with no orphan child", func(t *testing.T) {
		clk := newTestClock()
		var ranOnce atomic.Bool
		var h *harness
		var rootID string
		spawnEntered := make(chan struct{})
		spawnErr := make(chan error, 1)

		h, _ = agentHarness(t, func(o *Options) {
			o.Now = clk.Now
			o.ChildWaitingDays = 2
			o.beforeExpiryNodeClose = func(nodeID string) {
				if nodeID == rootID && ranOnce.CompareAndSwap(false, true) {
					parentRec := h.record(rootID)
					go func() {
						close(spawnEntered)
						_, err := h.runner.AgentSpawn(h.runner.holderCaller(parentRec), AgentSpawnRequest{
							Prompt: "raced child", Tier: "small", Authority: "read-only",
						})
						spawnErr <- err
					}()
					wait(t, "raced AgentSpawn started", spawnEntered)
					// Let AgentSpawn enter reserveSpawnWidth and block on r.expiring.
					time.Sleep(25 * time.Millisecond)
				}
			}
		})
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		ap.reply("done")
		eventually(t, "turn settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		rootID = a
		clk.advance(3 * day)

		if deleted := h.runner.expire(a); !deleted {
			t.Fatal("expected expiry to complete")
		}
		err := <-spawnErr
		if !errors.Is(err, errClosed) {
			t.Fatalf("raced AgentSpawn error = %v, want errClosed", err)
		}
		if kids := h.runner.descendants(a); len(kids) != 0 {
			t.Fatalf("closed parent %s has live descendants: %v", a, kids)
		}
	})

	t.Run("spawn during aborted expiry unblocks and succeeds", func(t *testing.T) {
		clk := newTestClock()
		var ranOnce atomic.Bool
		var h *harness
		var rootID string
		spawnEntered := make(chan struct{})
		spawnRes := make(chan AgentSpawnResult, 1)
		spawnErr := make(chan error, 1)

		h, _ = agentHarness(t, func(o *Options) {
			o.Now = clk.Now
			o.ChildWaitingDays = 2
			o.beforeExpiryNodeClose = func(nodeID string) {
				if nodeID == rootID && ranOnce.CompareAndSwap(false, true) {
					parentRec := h.record(rootID)
					go func() {
						close(spawnEntered)
						res, err := h.runner.AgentSpawn(h.runner.holderCaller(parentRec), AgentSpawnRequest{
							Prompt: "raced child after abort", Tier: "small", Authority: "read-only",
						})
						spawnRes <- res
						spawnErr <- err
					}()
					wait(t, "raced AgentSpawn started", spawnEntered)
					time.Sleep(25 * time.Millisecond)
					// Abort root expiry with fresh input before closeOneExpiry runs.
					if _, err := h.runner.Send(rootID, "keep root alive"); err != nil {
						t.Errorf("Send(%s): %v", rootID, err)
					}
				}
			}
		})
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		ap.reply("done")
		eventually(t, "turn settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		rootID = a
		clk.advance(3 * day)

		if deleted := h.runner.expire(a); deleted {
			t.Fatal("expected expiry to abort on fresh root input")
		}
		if err := <-spawnErr; err != nil {
			t.Fatalf("AgentSpawn after aborted expiry failed: %v", err)
		}
		res := <-spawnRes
		childID := strings.TrimPrefix(res.Address, "id:")
		if !h.runner.residentConv(childID) {
			t.Fatalf("spawned child %s is not resident after aborted expiry", childID)
		}
	})
}

// TestResidentMailWatchDoesNotLeakOnRapidCloseOrPark verifies Important #4:
// when Mail.Watch returns after CloseConversation or maybePark has already
// ended the conversation loop, installMailStop immediately stops the returned
// handle under c.mu synchronization with zero data races and zero leaked watches.
func TestResidentMailWatchDoesNotLeakOnRapidCloseOrPark(t *testing.T) {
	t.Run("rapid Close while Mail.Watch is in flight", func(t *testing.T) {
		watchEntered := make(chan struct{})
		watchRelease := make(chan struct{})
		var blocked atomic.Bool

		mail := &fakeParkedMailWatcher{
			onWatch: func() {
				if blocked.CompareAndSwap(false, true) {
					close(watchEntered)
					<-watchRelease
				}
			},
		}
		h, _ := agentHarness(t, func(o *Options) { o.Mail = mail })

		spawnErr := make(chan error, 1)
		go func() {
			_, err := h.runner.AgentSpawn(h.leadCaller(), AgentSpawnRequest{
				Prompt: "work", Tier: "small", Authority: "read-only",
			})
			spawnErr <- err
		}()

		wait(t, "resident Mail.Watch entered", watchEntered)
		// Find the newly registered resident child and close it before Watch returns.
		var childID string
		eventually(t, "child registered", func() bool {
			for _, st := range h.runner.List() {
				if st.Status != StatusClosed {
					childID = st.ID
					return true
				}
			}
			return false
		})
		if _, err := h.runner.CloseConversation(childID); err != nil {
			t.Fatal(err)
		}
		close(watchRelease)
		if err := <-spawnErr; !errors.Is(err, errClosed) {
			t.Fatalf("AgentSpawn after mid-start CloseConversation: got %v, want errClosed", err)
		}

		active, stopped := mail.residentCounts()
		if active != 0 || stopped != 1 {
			t.Fatalf("resident watches after rapid close: active=%d stopped=%d, want active=0 stopped=1", active, stopped)
		}
	})

	t.Run("maybePark defers while unparkWithGen is in flight so re-park installs working parked watch", func(t *testing.T) {
		watchEntered := make(chan struct{})
		watchRelease := make(chan struct{})
		var watchCount atomic.Int32

		mail := &fakeParkedMailWatcher{
			onWatch: func() {
				// Block the second resident Mail.Watch (the unpark rehydration).
				if watchCount.Add(1) == 2 {
					close(watchEntered)
					<-watchRelease
				}
			},
		}
		h, _ := agentHarness(t, func(o *Options) {
			o.ParkIdleChildren = true
			o.Mail = mail
		})
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		ap.reply("done 1")
		eventually(t, "turn 1 settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		eventually(t, "child parked", func() bool {
			active, _, _ := mail.counts()
			return h.runner.isParked(a) && !h.runner.residentConv(a) && active == 1
		})

		unparkDone := make(chan *conv, 1)
		go func() { unparkDone <- h.runner.unpark(a) }()

		wait(t, "unpark resident Mail.Watch entered", watchEntered)
		// While unparkWithGen holds r.unparking[a], maybePark must NOT park the
		// child prematurely (which would cause watchParked to skip watch install).
		time.Sleep(60 * time.Millisecond)
		if h.runner.isParked(a) || !h.runner.residentConv(a) {
			t.Fatal("child re-parked while unparkWithGen still held r.unparking[a]")
		}
		close(watchRelease)
		if got := <-unparkDone; got == nil {
			t.Fatal("unpark returned nil")
		}

		// Once unparkWithGen clears r.unparking[a], the idle child parks cleanly,
		// stops its resident Mail.Watch, and installs a working parked watch.
		eventually(t, "child re-parked with working parked watch", func() bool {
			parkedActive, _, cbs := mail.counts()
			return h.runner.isParked(a) && !h.runner.residentConv(a) && parkedActive == 1 && len(cbs) >= 2
		})
		resActive, resStopped := mail.residentCounts()
		if resActive != 0 || resStopped < 2 {
			t.Fatalf("resident watches after deferred re-park: active=%d stopped=%d, want active=0 stopped>=2", resActive, resStopped)
		}

		// Firing the newly installed parked watch must wake the child back to resident.
		_, _, cbs := mail.counts()
		cbs[len(cbs)-1]()
		eventually(t, "parked watch woke re-parked child", func() bool {
			return h.runner.residentConv(a) && !h.runner.isParked(a)
		})
	})

	t.Run("rapid Close while unpark Mail.Watch is in flight", func(t *testing.T) {
		watchEntered := make(chan struct{})
		watchRelease := make(chan struct{})
		var watchCount atomic.Int32

		mail := &fakeParkedMailWatcher{
			onWatch: func() {
				if watchCount.Add(1) == 2 {
					close(watchEntered)
					<-watchRelease
				}
			},
		}
		h, _ := agentHarness(t, func(o *Options) {
			o.ParkIdleChildren = true
			o.Mail = mail
		})
		a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
		ap.reply("done 1")
		eventually(t, "turn 1 settled", func() bool {
			rec := h.record(a)
			return rec.LastTurn != nil && !rec.TurnOpen
		})
		h.asleep(a)
		eventually(t, "child parked", func() bool { return h.runner.isParked(a) && !h.runner.residentConv(a) })

		unparkDone := make(chan *conv, 1)
		go func() { unparkDone <- h.runner.unpark(a) }()

		wait(t, "unpark resident Mail.Watch entered", watchEntered)
		if _, err := h.runner.CloseConversation(a); err != nil {
			t.Fatal(err)
		}
		close(watchRelease)
		<-unparkDone

		active, stopped := mail.residentCounts()
		if active != 0 || stopped != 2 {
			t.Fatalf("resident watches after rapid unpark+close: active=%d stopped=%d, want active=0 stopped=2", active, stopped)
		}
	})
}

// TestDirectSpawnSetPermissionLevelAndExpiryNoDeadlock verifies that direct
// Runner.Spawn reserves its parent slot (waiting on <-r.expiring[parent])
// BEFORE acquiring holdParentAdmission (selection.admission.RLock). Otherwise
// a concurrent SetPermissionLevel on the expiring parent's loop blocks on
// selection.admission.Lock while expiry waits for the parent's loop to close
// the barrier — a 3-way deadlock.
func TestDirectSpawnSetPermissionLevelAndExpiryNoDeadlock(t *testing.T) {
	clk := newTestClock()
	var ranOnce atomic.Bool
	var h *harness
	var rootID string
	spawnEntered := make(chan struct{})
	spawnErr := make(chan error, 1)
	setPermErr := make(chan error, 1)

	h, _ = agentHarness(t, func(o *Options) {
		o.Now = clk.Now
		o.ChildWaitingDays = 2
		o.beforeExpiryNodeClose = func(nodeID string) {
			if nodeID == rootID && ranOnce.CompareAndSwap(false, true) {
				parentRec := h.record(rootID)
				parentRef := ParentRef{Kind: ParentConversation, ReplyID: rootID}
				go func() {
					close(spawnEntered)
					_, err := h.runner.Spawn(SpawnSpec{
						Profile:         parentRec.Profile,
						Root:            parentRec.Root,
						Prompt:          "raced direct spawn",
						Parent:          &parentRef,
						PermissionLevel: PermissionReadOnly,
					})
					spawnErr <- err
				}()
				wait(t, "raced direct Spawn started", spawnEntered)
				// Let direct Spawn enter reserveParentSpawn and block on r.expiring[rootID].
				time.Sleep(25 * time.Millisecond)

				// Run SetPermissionLevel synchronously while r.expiring[rootID] is held
				// and direct Spawn is blocked waiting for it. If direct Spawn held
				// selection.admission.RLock across <-r.expiring[rootID], this would
				// deadlock permanently against rootID's loop.
				setPermDone := make(chan struct{})
				go func() {
					_, err := h.runner.SetPermissionLevel(rootID, PermissionReadOnly)
					setPermErr <- err
					close(setPermDone)
				}()
				wait(t, "SetPermissionLevel completed while expiry barrier held", setPermDone)
			}
		}
	})

	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "workdir-write"})
	ap.reply("done")
	eventually(t, "turn settled", func() bool {
		rec := h.record(a)
		return rec.LastTurn != nil && !rec.TurnOpen
	})
	h.asleep(a)
	rootID = a
	clk.advance(3 * day)

	if deleted := h.runner.expire(a); !deleted {
		t.Fatal("expected expiry to complete after SetPermissionLevel")
	}
	if err := <-setPermErr; err != nil {
		t.Fatalf("SetPermissionLevel during expiry barrier: %v", err)
	}
	if err := <-spawnErr; !errors.Is(err, errClosed) {
		t.Fatalf("raced direct Runner.Spawn error = %v, want errClosed", err)
	}
	if kids := h.runner.descendants(a); len(kids) != 0 {
		t.Fatalf("closed parent %s has live descendants: %v", a, kids)
	}
}
