// park_test.go holds the idle-child I/O storm fix tests: no heartbeat
// rewrites, and waiting children parked out of memory.
package wsagent

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func (r *Runner) residentConv(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.convs[id] != nil
}

func regStamp(t *testing.T, h *harness, id string) (string, time.Time) {
	t.Helper()
	path := wsdashboard.RegistrationPath(h.dir, id)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), info.ModTime()
}

// restartCounting restarts the runner over the same records with notify
// counted from after the start.
func restartCounting(h *harness, parked bool) *atomic.Int64 {
	h.runner.Close()
	h.config.ParkIdleChildren = parked
	var count atomic.Int64
	var armed atomic.Bool
	h.runner = NewRunner(h.config)
	h.runner.Start(func(string, string) {
		if armed.Load() {
			count.Add(1)
		}
	})
	h.t.Cleanup(h.runner.Close)
	time.Sleep(100 * time.Millisecond)
	armed.Store(true)
	return &count
}

func TestIdleConversationsRewriteNoRegistrationAndNotifyNothing(t *testing.T) {
	h, _ := agentHarness(t, nil)
	a, b := settledPair(t, h)
	notified := restartCounting(h, false)
	if !h.runner.residentConv(a) || !h.runner.residentConv(b) {
		t.Fatal("without parking the waiting children stay resident")
	}
	rawA, modA := regStamp(t, h, a)
	rawB, modB := regStamp(t, h, b)
	// The heartbeat is 50 ms: many intervals pass.
	time.Sleep(600 * time.Millisecond)
	for id, was := range map[string]struct {
		raw string
		mod time.Time
	}{a: {rawA, modA}, b: {rawB, modB}} {
		raw, mod := regStamp(t, h, id)
		if raw != was.raw || !mod.Equal(was.mod) {
			t.Fatalf("%s: registration.json rewritten while idle", id)
		}
	}
	if n := notified.Load(); n != 0 {
		t.Fatalf("idle heartbeats notified the dashboard %d times", n)
	}
	if !h.runner.OwnsLive(a) || !h.runner.OwnsLive(b) {
		t.Fatal("a resident open child's liveness is the runner's")
	}
}

func TestIdleChildParksAndRehydratesOnSend(t *testing.T) {
	h, _ := agentHarness(t, func(o *Options) { o.ParkIdleChildren = true })
	a, ap := h.agentRunning(h.leadCaller(), AgentSpawnRequest{Prompt: "work", Tier: "small", Authority: "read-only"})
	ap.reply("first done")
	h.asleep(a)
	eventually(t, "the child parked", func() bool { return h.runner.isParked(a) && !h.runner.residentConv(a) })
	before := h.record(a)
	if before.ClosedAt != "" || before.Parent == nil {
		t.Fatalf("a parked child's record = %+v", before)
	}
	st := h.state(a)
	if st.Status == StatusClosed || st.Lifecycle != wsdashboard.SessionChildWaiting || !st.IsChild {
		t.Fatalf("a parked child's state = %+v", st)
	}
	if !h.runner.OwnsLive(a) {
		t.Fatal("a parked child is still owned live")
	}
	if len(h.runner.AgentList(h.leadCaller())) != 1 {
		t.Fatal("agent.list lost the parked child")
	}
	// No rewrite while parked.
	raw, mod := regStamp(t, h, a)
	time.Sleep(300 * time.Millisecond)
	if raw2, mod2 := regStamp(t, h, a); raw2 != raw || !mod2.Equal(mod) {
		t.Fatal("a parked child's registration was rewritten")
	}

	n := h.backend.startCount()
	if _, err := h.runner.Send(a, "second"); err != nil {
		t.Fatal(err)
	}
	if !h.runner.residentConv(a) || h.runner.isParked(a) {
		t.Fatal("a send did not rehydrate the child")
	}
	p := h.backend.waitProc(t, n)
	if text := p.nextSent(t); !strings.Contains(text, "second") {
		t.Fatalf("the woken turn = %q", text)
	}
	after := h.runner.conv(a).record()
	if after.ReplyID != a || after.WSKey != before.WSKey || after.Parent == nil || *after.Parent != *before.Parent {
		t.Fatalf("rehydrated identity changed: %+v vs %+v", after, before)
	}
	if after.LastTurn == nil || after.LastTurn.Result.Message != "first done" {
		t.Fatalf("history lost across the park: %+v", after.LastTurn)
	}
	p.reply("second done")
	h.asleep(a)
	eventually(t, "parks again", func() bool { return h.runner.isParked(a) && !h.runner.residentConv(a) })
}

func TestDaemonStartKeepsWaitingChildrenParked(t *testing.T) {
	h, clk := expiryHarness(t)
	a, b := settledPair(t, h)
	h.runner.Close()
	h.config.ParkIdleChildren = true
	h.start()
	if h.runner.residentConv(a) || h.runner.residentConv(b) || !h.runner.isParked(a) || !h.runner.isParked(b) {
		t.Fatal("a start built loops for waiting children")
	}
	list := h.runner.AgentList(h.leadCaller())
	if len(list) != 1 || list[0].Address != AgentAddress(a) {
		t.Fatalf("agent.list = %+v", list)
	}
	if kids := h.runner.Children(ParentRef{Kind: ParentConversation, ReplyID: a}); len(kids) != 1 || kids[0].ID != b {
		t.Fatalf("the parked child's children = %+v", kids)
	}
	// A release of the checkout they are bound in meets them.
	if len(h.runner.boundConvs(h.root)) == 0 {
		t.Fatal("a checkout release does not see the parked children")
	}
	for _, id := range []string{a, b} {
		eventually(t, "parks again", func() bool { return h.runner.isParked(id) && !h.runner.residentConv(id) })
	}
	// Past the expiry the one sweep retires the subtree.
	clk.advance(3 * day)
	eventually(t, "the parked subtree expired", func() bool { return gone(h, a) && gone(h, b) })
}

func TestDaemonStartParkedChildWakesOnSend(t *testing.T) {
	h, _ := agentHarness(t, nil)
	a, _ := settledPair(t, h)
	h.runner.Close()
	h.config.ParkIdleChildren = true
	h.start()
	if !h.runner.isParked(a) {
		t.Fatal("not parked at start")
	}
	n := h.backend.startCount()
	if _, err := h.runner.Send(a, "wake up"); err != nil {
		t.Fatal(err)
	}
	p := h.backend.waitProc(t, n)
	if text := p.nextSent(t); !strings.Contains(text, "wake up") {
		t.Fatalf("woken turn = %q", text)
	}
}
