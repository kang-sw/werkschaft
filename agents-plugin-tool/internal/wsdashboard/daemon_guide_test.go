package wsdashboard

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsstate"
)

type fakeGuideTimer struct {
	c       *fakeGuideClock
	at      time.Time
	f       func()
	stopped bool
	fired   bool
}

func (t *fakeGuideTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.stopped && !t.fired
	t.stopped = true
	return was
}

type fakeGuideClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeGuideTimer
}

func newFakeGuideClock() *fakeGuideClock {
	return &fakeGuideClock{now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeGuideClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeGuideClock) AfterFunc(d time.Duration, f func()) guideTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeGuideTimer{c: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves the clock forward, firing due timers in time order (timers a
// callback schedules inside the window fire too).
func (c *fakeGuideClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *fakeGuideTimer
		for _, t := range c.timers {
			if t.stopped || t.fired || t.at.After(target) {
				continue
			}
			if next == nil || t.at.Before(next.at) {
				next = t
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		next.fired = true
		if next.at.After(c.now) {
			c.now = next.at
		}
		c.mu.Unlock()
		next.f()
	}
}

type guideDelivery struct{ owner, body string }

type guideHarness struct {
	clock *fakeGuideClock
	reg   *guideMonitorRegistry
	mu    sync.Mutex
	got   []guideDelivery
	dead  map[string]bool
	push  [][]byte
}

func newGuideHarness(t *testing.T, d *Daemon) *guideHarness {
	t.Helper()
	h := &guideHarness{clock: newFakeGuideClock(), dead: map[string]bool{}}
	deps := guideMonitorDeps{
		clock: h.clock,
		deliver: func(owner, body string) error {
			h.mu.Lock()
			h.got = append(h.got, guideDelivery{owner, body})
			h.mu.Unlock()
			return nil
		},
		live: func(owner string) bool {
			h.mu.Lock()
			defer h.mu.Unlock()
			return !h.dead[owner]
		},
		push: func(event string, data []byte) {
			h.mu.Lock()
			h.push = append(h.push, data)
			h.mu.Unlock()
			if d != nil {
				d.broadcastGuidePush(event, data)
			}
		},
	}
	h.reg = newGuideMonitorRegistry(deps)
	if d != nil {
		d.daemonBridge.mu.Lock()
		d.daemonBridge.guide = h.reg
		d.daemonBridge.mu.Unlock()
	}
	return h
}

func (h *guideHarness) deliveries() []guideDelivery {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]guideDelivery(nil), h.got...)
}

func (h *guideHarness) setDead(owner string) {
	h.mu.Lock()
	h.dead[owner] = true
	h.mu.Unlock()
}

func decodeGuideBody(t *testing.T, body string) map[string]any {
	t.Helper()
	head, line, ok := strings.Cut(body, "\n")
	if !ok || head != guideMonitorHeader {
		t.Fatalf("delivery body missing header: %q", body)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("delivery line %q: %v", line, err)
	}
	return m
}

func mustStart(t *testing.T, h *guideHarness, owner string, req DaemonMonitorRequest) DaemonMonitorStatus {
	t.Helper()
	req.Owner, req.Action = owner, "start"
	st, err := h.reg.handle(req)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return st
}

func guideErrCode(err error) string {
	if dce, ok := IsDaemonControlError(err); ok {
		return dce.Code
	}
	return ""
}

func TestGuideMonitorStartStopStatusReplaceAndClamps(t *testing.T) {
	h := newGuideHarness(t, nil)
	st := mustStart(t, h, "owner1", DaemonMonitorRequest{})
	if !st.Active || st.TTLSec != 600 || st.DebounceMS != 400 || len(st.Events) != 6 || !strings.HasPrefix(st.MonitorID, "gm-") || st.RemainingSec != 600 {
		t.Fatalf("default start = %+v", st)
	}
	for _, tc := range []struct {
		ttl, deb, wantTTL, wantDeb int
	}{{5, 100, 60, 300}, {5000, 9999, 900, 2000}, {120, 1500, 120, 1500}} {
		st = mustStart(t, h, "owner1", DaemonMonitorRequest{TTLSec: tc.ttl, DebounceMS: tc.deb})
		if st.TTLSec != tc.wantTTL || st.DebounceMS != tc.wantDeb {
			t.Fatalf("clamp %+v = ttl %d debounce %d", tc, st.TTLSec, st.DebounceMS)
		}
	}
	if h.reg.activeCount() != 1 {
		t.Fatalf("replace kept %d monitors", h.reg.activeCount())
	}
	prevID := st.MonitorID
	st = mustStart(t, h, "owner1", DaemonMonitorRequest{Events: []string{"modal_opened", "tab_switched", "modal_opened"}})
	if st.MonitorID == prevID || strings.Join(st.Events, ",") != "modal_opened,tab_switched" {
		t.Fatalf("replace = %+v (prev %s)", st, prevID)
	}
	h.reg.mu.Lock()
	_, stale := h.reg.byID[prevID]
	h.reg.mu.Unlock()
	if stale {
		t.Fatal("replaced monitor id still registered")
	}
	if _, err := h.reg.handle(DaemonMonitorRequest{Owner: "owner1", Action: "start", Events: []string{"keystroke"}}); guideErrCode(err) != "invalid_request" {
		t.Fatalf("unknown event err = %v", err)
	}
	if _, err := h.reg.handle(DaemonMonitorRequest{Owner: "", Action: "status"}); guideErrCode(err) != "invalid_request" {
		t.Fatalf("missing owner err = %v", err)
	}
	if _, err := h.reg.handle(DaemonMonitorRequest{Owner: "owner1", Action: "pause"}); guideErrCode(err) != "invalid_request" {
		t.Fatalf("bad action err = %v", err)
	}
	stopped, err := h.reg.handle(DaemonMonitorRequest{Owner: "owner1", Action: "stop"})
	if err != nil || stopped.Active || stopped.StopReason != guideStopAgent || stopped.MonitorID != st.MonitorID {
		t.Fatalf("stop = %+v %v", stopped, err)
	}
	status, _ := h.reg.handle(DaemonMonitorRequest{Owner: "owner1", Action: "status"})
	if status.Active || status.StopReason != guideStopAgent {
		t.Fatalf("status after stop = %+v", status)
	}
	if len(h.deliveries()) != 0 {
		t.Fatalf("agent stop/replace delivered notices: %v", h.deliveries())
	}
}

func TestGuideMonitorCoalescesIntoOneDelivery(t *testing.T) {
	h := newGuideHarness(t, nil)
	st := mustStart(t, h, "owner1", DaemonMonitorRequest{Events: []string{"tab_switched", "modal_opened"}})
	h.reg.ingest(guideEvent{Name: "tab_switched", TabID: "tab-1", Route: "/s/a"})
	h.clock.Advance(100 * time.Millisecond)
	h.reg.ingest(guideEvent{Name: "section_changed", TabID: "tab-1"}) // not subscribed
	h.reg.ingest(guideEvent{Name: "modal_opened", TabID: "tab-1", Route: "/s/a", Modal: "settings"})
	h.reg.ingest(guideEvent{Name: "tab_switched", TabID: "tab-2", Route: "/s/b"})
	if len(h.deliveries()) != 0 {
		t.Fatal("delivered before the debounce window closed")
	}
	h.clock.Advance(300 * time.Millisecond)
	got := h.deliveries()
	if len(got) != 1 || got[0].owner != "owner1" {
		t.Fatalf("deliveries = %v", got)
	}
	m := decodeGuideBody(t, got[0].body)
	if m["monitor_id"] != st.MonitorID || m["event"] != "tab_switched" || m["tab_id"] != "tab-2" || m["route"] != "/s/b" || m["coalesced_count"] != float64(3) {
		t.Fatalf("coalesced line = %v", m)
	}
	if evs, _ := m["events"].([]any); len(evs) != 2 || evs[0] != "tab_switched" || evs[1] != "modal_opened" {
		t.Fatalf("events = %v", m["events"])
	}
	if _, ok := m["suppressed_count"]; ok {
		t.Fatalf("suppressed_count present with nothing suppressed: %v", m)
	}
	status, _ := h.reg.handle(DaemonMonitorRequest{Owner: "owner1", Action: "status"})
	if status.Delivered != 1 || status.Coalesced != 2 {
		t.Fatalf("status counters = %+v", status)
	}
}

func TestGuideMonitorBurstCapAndSuppressedSummary(t *testing.T) {
	h := newGuideHarness(t, nil)
	mustStart(t, h, "owner1", DaemonMonitorRequest{DebounceMS: 300})
	fire := func(name string) {
		h.reg.ingest(guideEvent{Name: name, TabID: "tab-1"})
		h.clock.Advance(time.Second)
	}
	for i := 0; i < guideMonitorBurstCap; i++ {
		fire("tab_switched")
	}
	if n := len(h.deliveries()); n != guideMonitorBurstCap {
		t.Fatalf("delivered %d before cap, want %d", n, guideMonitorBurstCap)
	}
	fire("modal_opened")
	fire("modal_opened")
	fire("section_changed")
	if n := len(h.deliveries()); n != guideMonitorBurstCap {
		t.Fatalf("over-cap batches delivered: %d", n)
	}
	status, _ := h.reg.handle(DaemonMonitorRequest{Owner: "owner1", Action: "status"})
	if status.Suppressed != 3 {
		t.Fatalf("suppressed = %d, want 3", status.Suppressed)
	}
	// The first delivery was at ~0.3s; the summary fires when it ages out.
	h.clock.Advance(50 * time.Second)
	got := h.deliveries()
	if len(got) != guideMonitorBurstCap+1 {
		t.Fatalf("summary not delivered: %d deliveries", len(got))
	}
	m := decodeGuideBody(t, got[len(got)-1].body)
	if m["event"] != guideEventSummary || m["suppressed_count"] != float64(3) {
		t.Fatalf("summary = %v", m)
	}
	if ev, _ := m["suppressed_events"].(map[string]any); ev["modal_opened"] != float64(2) || ev["section_changed"] != float64(1) {
		t.Fatalf("suppressed_events = %v", m["suppressed_events"])
	}
}

func TestGuideMonitorTTLExpiryFinalNoticeAndLivenessTeardown(t *testing.T) {
	h := newGuideHarness(t, nil)
	st := mustStart(t, h, "owner1", DaemonMonitorRequest{TTLSec: 60})
	h.clock.Advance(61 * time.Second)
	got := h.deliveries()
	if len(got) != 1 {
		t.Fatalf("expiry deliveries = %v", got)
	}
	m := decodeGuideBody(t, got[0].body)
	if m["event"] != guideEventStopped || m["reason"] != guideStopExpired || m["monitor_id"] != st.MonitorID {
		t.Fatalf("expiry notice = %v", m)
	}
	if h.reg.activeCount() != 0 {
		t.Fatal("expired monitor still active")
	}

	// Dead owner at delivery: silent session_closed teardown.
	mustStart(t, h, "owner2", DaemonMonitorRequest{})
	h.setDead("owner2")
	h.reg.ingest(guideEvent{Name: "tab_switched", TabID: "tab-1"})
	h.clock.Advance(time.Second)
	if len(h.deliveries()) != 1 {
		t.Fatalf("dead owner received delivery: %v", h.deliveries())
	}
	if st, _ := h.reg.handle(DaemonMonitorRequest{Owner: "owner2", Action: "status"}); st.Active || st.StopReason != guideStopSession {
		t.Fatalf("dead owner status = %+v", st)
	}

	// Dead owner found by the periodic sweep (no events at all).
	mustStart(t, h, "owner3", DaemonMonitorRequest{})
	h.setDead("owner3")
	h.clock.Advance(guideMonitorSweepInterval + time.Second)
	if st, _ := h.reg.handle(DaemonMonitorRequest{Owner: "owner3", Action: "status"}); st.Active || st.StopReason != guideStopSession {
		t.Fatalf("swept owner status = %+v", st)
	}
	h.reg.mu.Lock()
	sweep := h.reg.sweepTimer
	h.reg.mu.Unlock()
	if sweep != nil {
		t.Fatal("sweep timer kept running with no monitors")
	}
	if len(h.deliveries()) != 1 {
		t.Fatalf("session_closed teardown delivered a notice: %v", h.deliveries())
	}
}

func TestGuideMonitorMaxActiveAndClose(t *testing.T) {
	h := newGuideHarness(t, nil)
	for i := 0; i < guideMonitorMaxActive; i++ {
		mustStart(t, h, "owner"+string(rune('a'+i)), DaemonMonitorRequest{})
	}
	if _, err := h.reg.handle(DaemonMonitorRequest{Owner: "ownerz", Action: "start"}); guideErrCode(err) != "too_many_monitors" {
		t.Fatalf("9th monitor err = %v", err)
	}
	// Replacing an existing owner's monitor is allowed at the cap.
	mustStart(t, h, "ownera", DaemonMonitorRequest{})

	d := &Daemon{dir: t.TempDir()}
	d.daemonBridge.guide = h.reg
	d.closeGuideMonitors()
	if h.reg.activeCount() != 0 {
		t.Fatal("close left monitors running")
	}
	if st, _ := h.reg.handle(DaemonMonitorRequest{Owner: "ownera", Action: "status"}); st.StopReason != guideStopDaemon {
		t.Fatalf("status after close = %+v", st)
	}
	if _, err := h.reg.handle(DaemonMonitorRequest{Owner: "ownera", Action: "start"}); guideErrCode(err) != "guide_unavailable" {
		t.Fatalf("start after close err = %v", err)
	}
	h.clock.Advance(time.Hour)
	if len(h.deliveries()) != 0 {
		t.Fatalf("closed registry delivered: %v", h.deliveries())
	}
	// A registry created after Close starts closed.
	d2 := &Daemon{dir: t.TempDir()}
	d2.closeGuideMonitors()
	if _, err := d2.DaemonMonitor(context.Background(), DaemonMonitorRequest{Owner: "o", Action: "start"}); guideErrCode(err) != "guide_unavailable" {
		t.Fatalf("post-close lazy registry err = %v", err)
	}
}

func drainPush(s *daemonBridgeStream, event string) []string {
	var out []string
	for {
		select {
		case ev := <-s.pushCh:
			if ev.Event == event {
				out = append(out, string(ev.Data))
			}
		default:
			return out
		}
	}
}

func TestGuideMonitorBridgeFramesRouteAndPush(t *testing.T) {
	d := &Daemon{dir: t.TempDir()}
	h := newGuideHarness(t, d)
	tab := d.daemonBridge.newStream("tab-1", true, true)
	tab.handleFrame(`{"event":"tab-heartbeat","tab_id":"tab-1","visible":true,"focused":true,"active_route":"/s/a"}`)
	st := mustStart(t, h, "owner1", DaemonMonitorRequest{})

	pushes := drainPush(tab, DaemonGuideMonitorEvent)
	if len(pushes) != 1 || !strings.Contains(pushes[0], `"active":true`) || !strings.Contains(pushes[0], st.MonitorID) {
		t.Fatalf("start push = %v", pushes)
	}
	// A tab registering while a monitor runs receives the state at once.
	late := d.daemonBridge.newStream("tab-2", true, true)
	if got := drainPush(late, DaemonGuideMonitorEvent); len(got) != 1 {
		t.Fatalf("late tab push = %v", got)
	}

	// Server-derived route_changed from a heartbeat route change.
	tab.handleFrame(`{"event":"tab-heartbeat","tab_id":"tab-1","visible":true,"active_route":"/s/b"}`)
	h.clock.Advance(time.Second)
	got := h.deliveries()
	if len(got) != 1 {
		t.Fatalf("route deliveries = %v", got)
	}
	if m := decodeGuideBody(t, got[0].body); m["event"] != guideEventRoute || m["route"] != "/s/b" || m["tab_id"] != "tab-1" {
		t.Fatalf("route line = %v", m)
	}

	// Client route_changed and non-allowlisted names are dropped.
	tab.handleFrame(`{"event":"guide-monitor-event","tab_id":"tab-1","monitor_event":"route_changed","route":"/s/c"}`)
	tab.handleFrame(`{"event":"guide-monitor-event","tab_id":"tab-1","monitor_event":"keystroke","section":"x"}`)
	h.clock.Advance(time.Second)
	if len(h.deliveries()) != 1 {
		t.Fatalf("dropped frames delivered: %v", h.deliveries())
	}

	// Content fields are clamped and stripped of control characters; the
	// stream's own tab id wins over the frame's.
	long := strings.Repeat("s", 100)
	frame, _ := json.Marshal(map[string]string{"event": "guide-monitor-event", "tab_id": "tab-spoof", "monitor_event": "section_changed", "section": long, "modal": "set\u0007tings", "highlight_id": "h\n1"})
	tab.handleFrame(string(frame))
	h.clock.Advance(time.Second)
	got = h.deliveries()
	if len(got) != 2 {
		t.Fatalf("client event deliveries = %v", got)
	}
	m := decodeGuideBody(t, got[1].body)
	if m["section"] != strings.Repeat("s", guideMonitorFieldMax) || m["modal"] != "settings" || m["highlight_id"] != "h1" || m["tab_id"] != "tab-1" {
		t.Fatalf("clamped line = %v", m)
	}

	// Frames from an unregistered stream are dropped.
	anon := d.daemonBridge.newStream("", true, false)
	anon.handleGuideFrame(`{"event":"guide-monitor-stop","monitor_id":"` + st.MonitorID + `"}`)
	if h.reg.activeCount() != 1 {
		t.Fatal("unregistered stream stopped the monitor")
	}

	// Stop chip: stopped_by_user plus one final notice and an inactive push.
	drainPush(tab, DaemonGuideMonitorEvent)
	tab.handleFrame(`{"event":"guide-monitor-stop","tab_id":"tab-1","monitor_id":"` + st.MonitorID + `"}`)
	got = h.deliveries()
	if len(got) != 3 {
		t.Fatalf("stop chip deliveries = %v", got)
	}
	if m := decodeGuideBody(t, got[2].body); m["event"] != guideEventStopped || m["reason"] != guideStopUser {
		t.Fatalf("stop chip notice = %v", m)
	}
	if pushes := drainPush(tab, DaemonGuideMonitorEvent); len(pushes) != 1 || !strings.Contains(pushes[0], `"active":false`) {
		t.Fatalf("stop push = %v", pushes)
	}
	if st, _ := h.reg.handle(DaemonMonitorRequest{Owner: "owner1", Action: "status"}); st.StopReason != guideStopUser {
		t.Fatalf("status after chip = %+v", st)
	}
}

// answerBridge serves one bridge request pushed to s.
func answerBridge(t *testing.T, d *Daemon, s *daemonBridgeStream, reply func(op string, params map[string]any) (any, string)) <-chan map[string]any {
	t.Helper()
	seen := make(chan map[string]any, 1)
	go func() {
		for ev := range s.pushCh {
			if ev.Event != DaemonBridgeRequestEvent {
				continue
			}
			var req struct {
				RequestID string         `json:"request_id"`
				Op        string         `json:"op"`
				Params    map[string]any `json:"params"`
			}
			_ = json.Unmarshal(ev.Data, &req)
			seen <- req.Params
			payload, code := reply(req.Op, req.Params)
			raw, _ := json.Marshal(payload)
			b := &d.daemonBridge
			b.mu.Lock()
			p := b.pending[req.RequestID]
			delete(b.pending, req.RequestID)
			b.mu.Unlock()
			if p != nil {
				p.ch <- bridgeReplyOutcome{Err: code, Payload: raw}
			}
			return
		}
	}()
	return seen
}

func TestDaemonHighlightValidationAndBridge(t *testing.T) {
	d := &Daemon{dir: t.TempDir()}
	tab := d.daemonBridge.newStream("tab-1", true, true)
	tab.handleFrame(`{"event":"tab-heartbeat","tab_id":"tab-1","visible":true,"focused":true,"active_route":"/"}`)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		req  DaemonHighlightRequest
		code string
	}{
		{"no target", DaemonHighlightRequest{Action: "show"}, "invalid_request"},
		{"two kinds", DaemonHighlightRequest{Action: "show", Target: &DaemonHighlightTarget{Selector: "#a", Text: "A"}}, "invalid_request"},
		{"clear_all id", DaemonHighlightRequest{Action: "clear_all", ID: "x"}, "invalid_request"},
		{"clear target", DaemonHighlightRequest{Action: "clear", Target: &DaemonHighlightTarget{Selector: "#a"}}, "invalid_request"},
		{"bad id", DaemonHighlightRequest{Action: "clear", ID: "no spaces"}, "invalid_request"},
		{"bad style", DaemonHighlightRequest{Action: "show", Style: "glow", Target: &DaemonHighlightTarget{Selector: "#a"}}, "invalid_request"},
		{"long comment", DaemonHighlightRequest{Action: "show", Comment: strings.Repeat("가", 281), Target: &DaemonHighlightTarget{Selector: "#a"}}, "invalid_request"},
		{"long text", DaemonHighlightRequest{Action: "show", Target: &DaemonHighlightTarget{Text: strings.Repeat("a", 201)}}, "invalid_request"},
		{"long selector", DaemonHighlightRequest{Action: "show", Target: &DaemonHighlightTarget{Selector: strings.Repeat("a", 257)}}, "invalid_request"},
		{"generation without ref", DaemonHighlightRequest{Action: "show", Target: &DaemonHighlightTarget{Selector: "#a", Generation: 2}}, "invalid_request"},
		{"excluded selector", DaemonHighlightRequest{Action: "show", Target: &DaemonHighlightTarget{Selector: "[DATA-WS-DOM-EXCLUDE] button"}}, "denied"},
		{"confirm selector", DaemonHighlightRequest{Action: "show", Target: &DaemonHighlightTarget{Selector: ".daemon-confirm-banner"}}, "denied"},
		{"stale ref", DaemonHighlightRequest{Action: "show", Target: &DaemonHighlightTarget{Ref: "g1-r1"}}, "stale_ref"},
		{"bad action", DaemonHighlightRequest{Action: "blink"}, "invalid_request"},
	} {
		if _, err := d.DaemonHighlight(ctx, tc.req); guideErrCode(err) != tc.code {
			t.Fatalf("%s: err = %v, want %s", tc.name, err, tc.code)
		}
	}
	if got := drainPush(tab, DaemonBridgeRequestEvent); len(got) != 0 {
		t.Fatalf("rejected requests reached the tab: %v", got)
	}

	// Ref targets resolve against the active snapshot and pin its tab.
	tabID, gen, navVer, err := d.daemonBridge.beginSnapshot("tab-1")
	if err != nil {
		t.Fatal(err)
	}
	d.daemonBridge.commitSnapshot(tabID, gen, navVer, "/", "", map[string]daemonRefInfo{
		"g1-r1": {Ref: "g1-r1", Label: "Settings"},
		"g1-r2": {Ref: "g1-r2", Excluded: true},
	})
	if _, err := d.DaemonHighlight(ctx, DaemonHighlightRequest{Action: "show", Target: &DaemonHighlightTarget{Ref: "g1-r2"}}); guideErrCode(err) != "denied" {
		t.Fatalf("excluded ref err = %v", err)
	}
	if _, err := d.DaemonHighlight(ctx, DaemonHighlightRequest{Action: "show", Target: &DaemonHighlightTarget{Ref: "g1-r1", Generation: 9}}); guideErrCode(err) != "stale_ref" {
		t.Fatalf("generation mismatch err = %v", err)
	}
	seen := answerBridge(t, d, tab, func(op string, params map[string]any) (any, string) {
		return map[string]any{"scope": "frame", "visible": true, "rect": map[string]any{"x": 1, "y": 2, "width": 30, "height": 40}}, ""
	})
	resp, err := d.DaemonHighlight(ctx, DaemonHighlightRequest{Action: "show", ID: "step-1", Style: "pulse", Comment: "  Open\u0007 settings\nhere  ", Target: &DaemonHighlightTarget{Ref: "g1-r1"}})
	if err != nil {
		t.Fatal(err)
	}
	params := <-seen
	target, _ := params["target"].(map[string]any)
	if params["action"] != "show" || params["id"] != "step-1" || params["style"] != "pulse" || params["comment"] != "Open settings\nhere" || target["kind"] != "ref" || target["ref"] != "g1-r1" || target["generation"] != float64(gen) {
		t.Fatalf("bridge params = %v", params)
	}
	if !resp.OK || resp.TabID != "tab-1" || resp.ID != "step-1" || resp.Style != "pulse" || resp.TargetKind != "ref" || resp.Scope != "frame" || !resp.Visible || resp.Rect == nil || resp.Rect.Width != 30 {
		t.Fatalf("response = %+v", resp)
	}

	// An old client answers invalid_request for the unknown op.
	answerBridge(t, d, tab, func(string, map[string]any) (any, string) { return nil, "invalid_request" })
	_, err = d.DaemonHighlight(ctx, DaemonHighlightRequest{Action: "clear_all"})
	if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "guide_unavailable" || dce.Reason != "client_unsupported" {
		t.Fatalf("old client err = %v", err)
	}
	// Other client codes pass through; an unknown scope is dropped.
	answerBridge(t, d, tab, func(string, map[string]any) (any, string) { return nil, "target_not_found" })
	if _, err := d.DaemonHighlight(ctx, DaemonHighlightRequest{Action: "show", Target: &DaemonHighlightTarget{Text: "Nope"}}); guideErrCode(err) != "target_not_found" {
		t.Fatalf("client code err = %v", err)
	}
	// A guide-aware client names its invalid_request reason; it stays
	// invalid_request instead of looking like an old client.
	answerBridge(t, d, tab, func(string, map[string]any) (any, string) { return nil, "invalid_request:invalid_selector" })
	_, err = d.DaemonHighlight(ctx, DaemonHighlightRequest{Action: "show", Target: &DaemonHighlightTarget{Selector: "a[["}})
	if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "invalid_request" || dce.Reason != "invalid_selector" {
		t.Fatalf("client reason err = %v", err)
	}
	answerBridge(t, d, tab, func(string, map[string]any) (any, string) {
		return map[string]any{"scope": "elsewhere", "cleared": 2}, ""
	})
	resp, err = d.DaemonHighlight(ctx, DaemonHighlightRequest{Action: "clear", ID: "step-1"})
	if err != nil || resp.Scope != "" || resp.Cleared != 2 || resp.Rect != nil {
		t.Fatalf("clear = %+v %v", resp, err)
	}
}

// TestGuideMonitorDeliveryRedactsSecrets pins that the client-observed route
// is secret-redacted on the delivery path before it reaches the agent turn.
func TestGuideMonitorDeliveryRedactsSecrets(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "daemon")
	d := &Daemon{dir: dir}
	d.endpoint.Token = "tok-guide-secret-123"
	owner := "abcdefabcdefabcdefabcdef"
	if err := d.deliverGuideMonitor(owner, guideMonitorHeader+"\n{\"route\":\"/s/x?t=tok-guide-secret-123\"}"); err != nil {
		t.Fatal(err)
	}
	store := (wsmailbox.Paths{State: wsstate.Options{CacheHome: filepath.Dir(dir)}}).Store()
	q, err := store.ReplyQueue(owner)
	if err != nil || len(q) != 1 {
		t.Fatalf("queue = %+v %v", q, err)
	}
	if strings.Contains(q[0].Content, "tok-guide-secret-123") || !strings.Contains(q[0].Content, "[REDACTED]") || q[0].Label != guideMonitorLabel {
		t.Fatalf("delivered = %+v", q[0])
	}
}
