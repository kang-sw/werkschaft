package wsagent

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

type boardIncarnationPageSessions struct {
	hostedPageSessions
	called atomic.Bool
}

func (s *boardIncarnationPageSessions) LockBoardIncarnation(_ wsdashboard.Registration, token string) (func(), bool) {
	s.called.Store(true)
	return func() {}, token == "inc-1"
}

func TestPageInputCallbackCallsOwnsLiveAndDashboardSessionNoDeadlock(t *testing.T) {
	h := newLeadHarness(t)
	h.runner = NewRunner(h.opts)
	sessions := &boardIncarnationPageSessions{}
	daemon, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir: h.dir, Port: 0, Sessions: sessions, Agents: h.runner, PollInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(daemon.Close)
	id := h.lead(BackendClaude, LeadOptions{}).ID
	eventually(t, "hosted index row", func() bool {
		s, ok := daemon.Index().Session(id)
		return ok && s.Hosted
	})

	c := h.runner.conv(id)
	if c == nil {
		t.Fatalf("conv %q missing", id)
	}

	// 1. Direct PageInput with a write callback that acquires an external
	// inbox-like mutex and calls both Index.Session (which runs
	// Index.effective -> Runner.OwnsLive) and Runner.OwnsLive directly on the
	// same conversation. Also verify c.mu is NOT held during write().
	var simInboxMu sync.Mutex
	done := make(chan error, 1)
	go func() {
		done <- h.runner.PageInput(id, func() bool {
			simInboxMu.Lock()
			defer simInboxMu.Unlock()
			if !c.mu.TryLock() {
				t.Errorf("c.mu was held across PageInput write() callback")
			} else {
				c.mu.Unlock()
			}
			if !h.runner.OwnsLive(id) {
				t.Errorf("OwnsLive(%q) = false inside PageInput write()", id)
			}
			s, ok := daemon.Index().Session(id)
			if !ok || !s.Hosted {
				t.Errorf("Index.Session(%q) = (%+v, %v), want hosted", id, s, ok)
			}
			return true
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PageInput: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PageInput deadlocked when write() called Index.Session / OwnsLive")
	}

	// 2. End-to-end HTTP POST hitting handleInboxWrite -> PageInput ->
	// messageWrite -> d.inboxMu -> d.lockBoardIncarnation -> d.index.Session
	// -> Index.effective -> Runner.OwnsLive -> LockBoardIncarnation
	// (exact forensic G39385 deadlock path).
	cookie := daemonCookieForTest(t, daemon)
	httpDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body := `{"text":"board reply","boardIncarnation":"inc-1","chips":[{"id":"1","answer":"a"}]}`
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/s/"+id+"/message", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Origin", "http://127.0.0.1")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		daemon.Handler().ServeHTTP(rec, req)
		httpDone <- rec
	}()
	select {
	case rec := <-httpDone:
		if rec.Code != http.StatusOK {
			t.Fatalf("board-tagged page send = %d %s", rec.Code, rec.Body.String())
		}
		if !sessions.called.Load() {
			t.Fatal("LockBoardIncarnation was not called on board-tagged page send")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("board-tagged HTTP message write deadlocked in PageInput -> inboxMu -> OwnsLive")
	}
}

func TestOwnsLiveLockFreeAndTransitionContinuity(t *testing.T) {
	h, _ := agentHarness(t, func(o *Options) { o.ParkIdleChildren = true })
	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
	c := h.runner.conv(a)
	if c == nil {
		t.Fatalf("conv %q missing", a)
	}

	// Verify OwnsLive succeeds without blocking while r.mu and c.mu are held.
	assertOwnsLiveImmediate := func(label string, want bool) {
		t.Helper()
		res := make(chan bool, 1)
		go func() { res <- h.runner.OwnsLive(a) }()
		select {
		case got := <-res:
			if got != want {
				t.Fatalf("%s: OwnsLive(%q) = %v, want %v", label, a, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: OwnsLive blocked", label)
		}
	}

	h.runner.mu.Lock()
	assertOwnsLiveImmediate("r.mu held", true)
	h.runner.mu.Unlock()

	c.mu.Lock()
	assertOwnsLiveImmediate("c.mu held", true)
	c.mu.Unlock()

	h.runner.mu.Lock()
	c.mu.Lock()
	assertOwnsLiveImmediate("both r.mu and c.mu held", true)
	c.mu.Unlock()
	h.runner.mu.Unlock()

	if h.runner.OwnsLive("unknown-reply-id") {
		t.Fatal("OwnsLive(unknown) = true, want false")
	}

	// Continuously poll OwnsLive across live -> parked -> unparked -> parked
	// transitions: must never dip to false.
	var stopPoll atomic.Bool
	var sawFalse atomic.Bool
	var polls atomic.Int64
	var pollWG sync.WaitGroup
	pollWG.Add(1)
	go func() {
		defer pollWG.Done()
		for !stopPoll.Load() {
			polls.Add(1)
			if !h.runner.OwnsLive(a) {
				sawFalse.Store(true)
				return
			}
			runtime.Gosched()
		}
	}()

	ap.reply("first done")
	h.asleep(a)
	eventually(t, "child parked", func() bool { return h.runner.isParked(a) && !h.runner.residentConv(a) })

	// While parked, OwnsLive must also stay lock-free even when r.mu is held.
	h.runner.mu.Lock()
	assertOwnsLiveImmediate("parked with r.mu held", true)
	h.runner.mu.Unlock()

	n := h.backend.startCount()
	if _, err := h.runner.Send(a, "wake"); err != nil {
		t.Fatal(err)
	}
	p2 := h.backend.waitProc(t, n)
	p2.reply("second done")
	h.asleep(a)
	eventually(t, "child parked again", func() bool { return h.runner.isParked(a) && !h.runner.residentConv(a) })

	stopPoll.Store(true)
	pollWG.Wait()
	if polls.Load() == 0 {
		t.Fatal("OwnsLive poller did not run across transitions")
	}
	if sawFalse.Load() {
		t.Fatal("OwnsLive returned false during live -> parked -> unparked -> parked transition")
	}

	// Once closed, OwnsLive must return false.
	if _, err := h.runner.CloseConversation(a); err != nil {
		t.Fatal(err)
	}
	if h.runner.OwnsLive(a) {
		t.Fatal("OwnsLive returned true after CloseConversation")
	}
}

func TestRMuReadersAndReserveSpawnWidthDoNotBlockOnContendedConvMu(t *testing.T) {
	h, _ := agentHarness(t, nil)
	leadParent := h.leadCaller().Parent
	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
	c := h.runner.conv(a)
	if c == nil {
		t.Fatalf("conv %q missing", a)
	}

	// Hold c.mu artificially on child a and verify r.mu operations and
	// reserveSpawnWidth / awakeChildren do not block.
	c.mu.Lock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if n := h.runner.awakeChildren(leadParent); n != 1 {
			t.Errorf("awakeChildren with contended c.mu = %d, want 1", n)
		}
		_, release, err := h.runner.reserveSpawnWidth(leadParent)
		if err != nil {
			t.Errorf("reserveSpawnWidth with contended c.mu: %v", err)
		} else {
			release()
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		c.mu.Unlock()
		t.Fatal("awakeChildren / reserveSpawnWidth blocked on contended c.mu")
	}

	// Start snapshot-based readers while c.mu is held: they will wait on c.mu
	// AFTER releasing r.mu, so r.mu must remain uncontended throughout.
	readersStarted := make(chan struct{}, 2)
	durableDone := make(chan struct{})
	go func() {
		readersStarted <- struct{}{}
		_ = h.runner.DurableSessions()
		close(durableDone)
	}()
	descDone := make(chan struct{})
	go func() {
		readersStarted <- struct{}{}
		_ = h.runner.descendants(leadParent.ReplyID)
		close(descDone)
	}()
	<-readersStarted
	<-readersStarted
	runtime.Gosched()

	rmuDone := make(chan struct{})
	go func() {
		h.runner.mu.Lock()
		h.runner.mu.Unlock()
		if n := h.runner.awakeChildren(leadParent); n != 1 {
			t.Errorf("awakeChildren while readers wait on c.mu = %d, want 1", n)
		}
		close(rmuDone)
	}()
	select {
	case <-rmuDone:
	case <-time.After(2 * time.Second):
		c.mu.Unlock()
		t.Fatal("r.mu stalled while DurableSessions/descendants waited on contended c.mu")
	}
	select {
	case <-durableDone:
		c.mu.Unlock()
		t.Fatal("DurableSessions completed while c.mu was still held")
	default:
	}

	c.mu.Unlock()
	wait(t, "DurableSessions after c.mu unlock", durableDone)
	wait(t, "descendants after c.mu unlock", descDone)

	// Finish child a and let it sleep so it no longer consumes a width slot.
	ap.reply("done")
	h.asleep(a)
	eventually(t, "sleeping child not counted", func() bool {
		return h.runner.awakeChildren(leadParent) == 0
	})

	// Concurrent reserveSpawnWidth must enforce MaxSpawnWidth accurately.
	const callers = MaxSpawnWidth * 4
	var succeeded atomic.Int32
	var releasesMu sync.Mutex
	var releases []func()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, rel, err := h.runner.reserveSpawnWidth(leadParent)
			if err == nil {
				succeeded.Add(1)
				releasesMu.Lock()
				releases = append(releases, rel)
				releasesMu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := int(succeeded.Load()); got != MaxSpawnWidth {
		t.Fatalf("concurrent reserveSpawnWidth succeeded = %d, want %d", got, MaxSpawnWidth)
	}
	if n := h.runner.awakeChildren(leadParent); n != MaxSpawnWidth {
		t.Fatalf("awakeChildren with reserved slots = %d, want %d", n, MaxSpawnWidth)
	}
	for _, rel := range releases {
		rel()
	}
	if n := h.runner.awakeChildren(leadParent); n != 0 {
		t.Fatalf("awakeChildren after releasing all slots = %d, want 0", n)
	}
}
