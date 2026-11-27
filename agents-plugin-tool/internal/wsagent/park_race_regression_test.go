package wsagent

import (
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
	mu      sync.Mutex
	active  map[*func()]struct{}
	stopped int
	all     []func()
}

func (f *fakeParkedMailWatcher) Watch(root, key string, changed func()) func() {
	return func() {}
}

func (f *fakeParkedMailWatcher) Drain(root, key string, relation func(sender string) string) ([]ClaimedMail, error) {
	return nil, nil
}

func (f *fakeParkedMailWatcher) Return(root, key string, texts []string) error {
	return nil
}

func (f *fakeParkedMailWatcher) WatchParked(root, key string, changed func()) func() {
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
