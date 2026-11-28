package wsdashboard

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// ws/daemon.monitor: an opt-in, TTL-bounded subscription to structural UI
// events in the local dashboard. Events are debounced, coalesced, burst-capped,
// and delivered to the owning hosted session through its mailbox queue as one
// system envelope per batch. Only structural names travel; keystrokes, drafts,
// and free text never do.
const (
	guideMonitorDefaultTTL      = 600 * time.Second
	guideMonitorMinTTL          = 60 * time.Second
	guideMonitorMaxTTL          = 900 * time.Second
	guideMonitorDefaultDebounce = 400 * time.Millisecond
	guideMonitorMinDebounce     = 300 * time.Millisecond
	guideMonitorMaxDebounce     = 2000 * time.Millisecond
	guideMonitorBurstCap        = 12
	guideMonitorBurstWindow     = 60 * time.Second
	guideMonitorMaxActive       = 8
	guideMonitorSweepInterval   = 30 * time.Second
	guideMonitorLastStopMax     = 64
	guideMonitorFieldMax        = 64

	// guideMonitorLabel is the display label of a delivered monitor envelope.
	guideMonitorLabel = "guide-monitor"
	// DaemonGuideMonitorEvent pushes the active monitor set to every
	// registered local tab (stop chip and client event hooks follow it).
	DaemonGuideMonitorEvent = "daemon-guide-monitor"

	guideMonitorEventFrame = "guide-monitor-event"
	guideMonitorStopFrame  = "guide-monitor-stop"

	guideMonitorHeader = "[ws/daemon.monitor] structural UI event (follow up with ws/daemon.dom or ws/daemon.highlight if useful; call ws/daemon.monitor action=stop when the guide ends)"
)

// Stop reasons recorded on a monitor's teardown.
const (
	guideStopAgent     = "stopped_by_agent"
	guideStopUser      = "stopped_by_user"
	guideStopExpired   = "expired"
	guideStopSession   = "session_closed"
	guideStopDaemon    = "daemon_stopped"
	guideStopReplaced  = "replaced"
	guideEventRoute    = "route_changed"
	guideEventStopped  = "monitor_stopped"
	guideEventSummary  = "events_suppressed"
	guideMonitorPrefix = "gm"
)

// guideMonitorEventNames is the closed structural event allowlist, in its
// canonical order.
var guideMonitorEventNames = []string{"tab_switched", "section_changed", "modal_opened", "modal_closed", "route_changed", "highlight_clicked"}

func guideMonitorEventAllowed(name string) bool {
	for _, n := range guideMonitorEventNames {
		if n == name {
			return true
		}
	}
	return false
}

// DaemonMonitorRequest is the structured input to ws/daemon.monitor. Owner is
// the calling hosted session's reply id, bound server-side (never from args).
type DaemonMonitorRequest struct {
	Owner      string   `json:"-"`
	Action     string   `json:"action"`
	Events     []string `json:"events,omitempty"`
	TTLSec     int      `json:"ttl_sec,omitempty"`
	DebounceMS int      `json:"debounce_ms,omitempty"`
}

// DaemonMonitorStatus is the state of the caller's monitor.
type DaemonMonitorStatus struct {
	Active       bool     `json:"active"`
	MonitorID    string   `json:"monitor_id,omitempty"`
	Events       []string `json:"events,omitempty"`
	TTLSec       int      `json:"ttl_sec,omitempty"`
	DebounceMS   int      `json:"debounce_ms,omitempty"`
	StartedAt    string   `json:"started_at,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	RemainingSec int      `json:"remaining_sec"`
	Delivered    int      `json:"delivered"`
	Coalesced    int      `json:"coalesced"`
	Suppressed   int      `json:"suppressed"`
	StopReason   string   `json:"stop_reason,omitempty"`
}

type guideTimer interface{ Stop() bool }

type guideClock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) guideTimer
}

type realGuideClock struct{}

func (realGuideClock) Now() time.Time { return time.Now() }
func (realGuideClock) AfterFunc(d time.Duration, f func()) guideTimer {
	return time.AfterFunc(d, f)
}

// guideMonitorDeps are the registry's injected seams.
type guideMonitorDeps struct {
	clock   guideClock
	deliver func(owner, body string) error
	live    func(owner string) bool
	push    func(event string, data []byte)
}

type guideEvent struct {
	Name        string
	TabID       string
	Route       string
	Section     string
	Modal       string
	HighlightID string
}

type guideBatch struct {
	names   []string
	perName map[string]int
	last    guideEvent
	count   int
}

type guideMonitor struct {
	id, owner         string
	events            []string
	eventSet          map[string]bool
	ttl, debounce     time.Duration
	startedAt         time.Time
	expiresAt         time.Time
	stopped           bool
	ttlTimer          guideTimer
	batchTimer        guideTimer
	summaryTimer      guideTimer
	batch             *guideBatch
	sent              []time.Time
	pendingSuppressed int
	pendingEvents     map[string]int
	delivered         int
	coalesced         int
	suppressed        int
}

type guideMonitorRegistry struct {
	deps   guideMonitorDeps
	pushMu sync.Mutex // serializes state build + push so the last push wins

	mu         sync.Mutex
	closed     bool
	monitors   map[string]*guideMonitor // by owner reply id
	byID       map[string]*guideMonitor
	lastStop   map[string]string
	sweepTimer guideTimer
}

func newGuideMonitorRegistry(deps guideMonitorDeps) *guideMonitorRegistry {
	if deps.clock == nil {
		deps.clock = realGuideClock{}
	}
	return &guideMonitorRegistry{
		deps:     deps,
		monitors: map[string]*guideMonitor{},
		byID:     map[string]*guideMonitor{},
		lastStop: map[string]string{},
	}
}

func guideInvalid(format string, args ...any) error {
	return &DaemonControlError{Code: "invalid_request", Message: fmt.Sprintf(format, args...)}
}

func (r *guideMonitorRegistry) handle(req DaemonMonitorRequest) (DaemonMonitorStatus, error) {
	owner := strings.TrimSpace(req.Owner)
	if !daemonReplyIDPattern.MatchString(owner) {
		return DaemonMonitorStatus{}, guideInvalid("daemon.monitor requires a hosted session owner")
	}
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "start":
		return r.start(owner, req)
	case "stop":
		return r.stop(owner), nil
	case "status":
		return r.status(owner), nil
	default:
		return DaemonMonitorStatus{}, guideInvalid("unsupported daemon.monitor action %q (allowed: start, stop, status)", req.Action)
	}
}

func clampGuideDuration(v int, unit, def, lo, hi time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	d := time.Duration(v) * unit
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

func (r *guideMonitorRegistry) start(owner string, req DaemonMonitorRequest) (DaemonMonitorStatus, error) {
	var events []string
	seen := map[string]bool{}
	for _, raw := range req.Events {
		name := strings.TrimSpace(raw)
		if !guideMonitorEventAllowed(name) {
			return DaemonMonitorStatus{}, guideInvalid("unknown daemon.monitor event %q (allowed: %s)", raw, strings.Join(guideMonitorEventNames, ", "))
		}
		if !seen[name] {
			seen[name] = true
			events = append(events, name)
		}
	}
	if len(events) == 0 {
		events = append([]string(nil), guideMonitorEventNames...)
		for _, n := range events {
			seen[n] = true
		}
	}
	ttl := clampGuideDuration(req.TTLSec, time.Second, guideMonitorDefaultTTL, guideMonitorMinTTL, guideMonitorMaxTTL)
	debounce := clampGuideDuration(req.DebounceMS, time.Millisecond, guideMonitorDefaultDebounce, guideMonitorMinDebounce, guideMonitorMaxDebounce)
	id := mintBridgeID(guideMonitorPrefix, 8)
	if id == "" {
		return DaemonMonitorStatus{}, &DaemonControlError{Code: "guide_unavailable", Reason: "id_unavailable", Message: "monitor id unavailable"}
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return DaemonMonitorStatus{}, &DaemonControlError{Code: "guide_unavailable", Reason: guideStopDaemon, Message: "dashboard daemon is stopping"}
	}
	old := r.monitors[owner]
	active := len(r.monitors)
	if old != nil {
		active--
	}
	if active >= guideMonitorMaxActive {
		r.mu.Unlock()
		return DaemonMonitorStatus{}, &DaemonControlError{Code: "too_many_monitors", Message: fmt.Sprintf("at most %d guide monitors may run at once", guideMonitorMaxActive)}
	}
	if old != nil {
		r.teardownLocked(old, guideStopReplaced)
	}
	now := r.deps.clock.Now()
	m := &guideMonitor{
		id:        id,
		owner:     owner,
		events:    events,
		eventSet:  seen,
		ttl:       ttl,
		debounce:  debounce,
		startedAt: now,
		expiresAt: now.Add(ttl),
	}
	m.ttlTimer = r.deps.clock.AfterFunc(ttl, func() { r.expire(m) })
	r.monitors[owner] = m
	r.byID[id] = m
	r.ensureSweepLocked()
	st := m.statusLocked(now)
	r.mu.Unlock()
	r.broadcast()
	return st, nil
}

func (r *guideMonitorRegistry) stop(owner string) DaemonMonitorStatus {
	r.mu.Lock()
	m := r.monitors[owner]
	if m == nil {
		st := DaemonMonitorStatus{StopReason: r.lastStop[owner]}
		r.mu.Unlock()
		return st
	}
	st := m.statusLocked(r.deps.clock.Now())
	r.teardownLocked(m, guideStopAgent)
	r.mu.Unlock()
	st.Active, st.RemainingSec, st.StopReason = false, 0, guideStopAgent
	r.broadcast()
	return st
}

func (r *guideMonitorRegistry) status(owner string) DaemonMonitorStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m := r.monitors[owner]; m != nil {
		return m.statusLocked(r.deps.clock.Now())
	}
	return DaemonMonitorStatus{StopReason: r.lastStop[owner]}
}

func (m *guideMonitor) statusLocked(now time.Time) DaemonMonitorStatus {
	remaining := int(m.expiresAt.Sub(now).Round(time.Second) / time.Second)
	if remaining < 0 {
		remaining = 0
	}
	return DaemonMonitorStatus{
		Active:       !m.stopped,
		MonitorID:    m.id,
		Events:       append([]string(nil), m.events...),
		TTLSec:       int(m.ttl / time.Second),
		DebounceMS:   int(m.debounce / time.Millisecond),
		StartedAt:    m.startedAt.UTC().Format(time.RFC3339),
		ExpiresAt:    m.expiresAt.UTC().Format(time.RFC3339),
		RemainingSec: remaining,
		Delivered:    m.delivered,
		Coalesced:    m.coalesced,
		Suppressed:   m.suppressed,
	}
}

// teardownLocked removes m and stops every timer it owns.
func (r *guideMonitorRegistry) teardownLocked(m *guideMonitor, reason string) {
	if m.stopped {
		return
	}
	m.stopped = true
	for _, t := range []guideTimer{m.ttlTimer, m.batchTimer, m.summaryTimer} {
		if t != nil {
			t.Stop()
		}
	}
	m.ttlTimer, m.batchTimer, m.summaryTimer, m.batch = nil, nil, nil, nil
	if r.monitors[m.owner] == m {
		delete(r.monitors, m.owner)
	}
	delete(r.byID, m.id)
	if _, ok := r.lastStop[m.owner]; !ok && len(r.lastStop) >= guideMonitorLastStopMax {
		for k := range r.lastStop {
			delete(r.lastStop, k)
			break
		}
	}
	r.lastStop[m.owner] = reason
	if len(r.monitors) == 0 && r.sweepTimer != nil {
		r.sweepTimer.Stop()
		r.sweepTimer = nil
	}
}

func (r *guideMonitorRegistry) ensureSweepLocked() {
	if r.closed || r.sweepTimer != nil || len(r.monitors) == 0 || r.deps.live == nil {
		return
	}
	r.sweepTimer = r.deps.clock.AfterFunc(guideMonitorSweepInterval, r.sweep)
}

// sweep tears down monitors whose owner session is no longer live. It runs
// only while at least one monitor is active.
func (r *guideMonitorRegistry) sweep() {
	r.mu.Lock()
	r.sweepTimer = nil
	if r.closed {
		r.mu.Unlock()
		return
	}
	list := make([]*guideMonitor, 0, len(r.monitors))
	for _, m := range r.monitors {
		list = append(list, m)
	}
	r.mu.Unlock()
	changed := false
	for _, m := range list {
		if r.deps.live(m.owner) {
			continue
		}
		r.mu.Lock()
		if !m.stopped {
			r.teardownLocked(m, guideStopSession)
			changed = true
		}
		r.mu.Unlock()
	}
	r.mu.Lock()
	r.ensureSweepLocked()
	r.mu.Unlock()
	if changed {
		r.broadcast()
	}
}

func (r *guideMonitorRegistry) expire(m *guideMonitor) {
	r.mu.Lock()
	if m.stopped {
		r.mu.Unlock()
		return
	}
	r.teardownLocked(m, guideStopExpired)
	r.mu.Unlock()
	r.notifyStopped(m, guideStopExpired)
	r.broadcast()
}

// userStop handles the stop chip frame from a registered local tab.
func (r *guideMonitorRegistry) userStop(monitorID string) {
	r.mu.Lock()
	m := r.byID[strings.TrimSpace(monitorID)]
	if m == nil || m.stopped {
		r.mu.Unlock()
		return
	}
	r.teardownLocked(m, guideStopUser)
	r.mu.Unlock()
	r.notifyStopped(m, guideStopUser)
	r.broadcast()
}

func (r *guideMonitorRegistry) notifyStopped(m *guideMonitor, reason string) {
	if r.deps.deliver == nil {
		return
	}
	if r.deps.live != nil && !r.deps.live(m.owner) {
		return
	}
	line, _ := json.Marshal(struct {
		MonitorID string `json:"monitor_id"`
		Event     string `json:"event"`
		Reason    string `json:"reason"`
	}{m.id, guideEventStopped, reason})
	_ = r.deps.deliver(m.owner, guideMonitorHeader+"\n"+string(line))
}

// ingest folds one structural event into every active monitor subscribed to
// its name. It never performs I/O; liveness is checked at delivery.
func (r *guideMonitorRegistry) ingest(ev guideEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || len(r.monitors) == 0 {
		return
	}
	now := r.deps.clock.Now()
	for _, m := range r.monitors {
		if m.stopped || !m.eventSet[ev.Name] || !now.Before(m.expiresAt) {
			continue
		}
		if m.batch == nil {
			b := &guideBatch{perName: map[string]int{}}
			m.batch = b
			mon := m
			m.batchTimer = r.deps.clock.AfterFunc(m.debounce, func() { r.flush(mon, b) })
		} else {
			m.coalesced++
		}
		b := m.batch
		if b.perName[ev.Name] == 0 {
			b.names = append(b.names, ev.Name)
		}
		b.perName[ev.Name]++
		b.count++
		b.last = ev
	}
}

func (m *guideMonitor) pruneSent(now time.Time) {
	cut := 0
	for cut < len(m.sent) && !now.Before(m.sent[cut].Add(guideMonitorBurstWindow)) {
		cut++
	}
	if cut > 0 {
		m.sent = append(m.sent[:0], m.sent[cut:]...)
	}
}

func (m *guideMonitor) remainingSec(now time.Time) int {
	rem := int(m.expiresAt.Sub(now) / time.Second)
	if rem < 0 {
		return 0
	}
	return rem
}

type guideDeliveryLine struct {
	MonitorID        string         `json:"monitor_id"`
	Event            string         `json:"event"`
	Events           []string       `json:"events"`
	TabID            string         `json:"tab_id"`
	Route            string         `json:"route"`
	Section          string         `json:"section"`
	Modal            string         `json:"modal"`
	HighlightID      string         `json:"highlight_id"`
	CoalescedCount   int            `json:"coalesced_count"`
	RemainingSec     int            `json:"remaining_sec"`
	SuppressedCount  int            `json:"suppressed_count,omitempty"`
	SuppressedEvents map[string]int `json:"suppressed_events,omitempty"`
}

type guideSummaryLine struct {
	MonitorID        string         `json:"monitor_id"`
	Event            string         `json:"event"`
	SuppressedCount  int            `json:"suppressed_count"`
	SuppressedEvents map[string]int `json:"suppressed_events"`
	RemainingSec     int            `json:"remaining_sec"`
}

// takeSuppressedLocked moves the pending suppressed tally out of m.
func (m *guideMonitor) takeSuppressedLocked() (int, map[string]int) {
	n, ev := m.pendingSuppressed, m.pendingEvents
	m.pendingSuppressed, m.pendingEvents = 0, nil
	if m.summaryTimer != nil {
		m.summaryTimer.Stop()
		m.summaryTimer = nil
	}
	return n, ev
}

func (r *guideMonitorRegistry) flush(m *guideMonitor, b *guideBatch) {
	r.mu.Lock()
	if m.stopped || m.batch != b {
		r.mu.Unlock()
		return
	}
	m.batch, m.batchTimer = nil, nil
	now := r.deps.clock.Now()
	m.pruneSent(now)
	if len(m.sent) >= guideMonitorBurstCap {
		m.pendingSuppressed += b.count
		if m.pendingEvents == nil {
			m.pendingEvents = map[string]int{}
		}
		for name, c := range b.perName {
			m.pendingEvents[name] += c
		}
		m.suppressed += b.count
		r.scheduleSummaryLocked(m, now)
		r.mu.Unlock()
		return
	}
	m.sent = append(m.sent, now)
	m.delivered++
	line := guideDeliveryLine{
		MonitorID:      m.id,
		Event:          b.last.Name,
		Events:         append([]string(nil), b.names...),
		TabID:          b.last.TabID,
		Route:          b.last.Route,
		Section:        b.last.Section,
		Modal:          b.last.Modal,
		HighlightID:    b.last.HighlightID,
		CoalescedCount: b.count,
		RemainingSec:   m.remainingSec(now),
	}
	line.SuppressedCount, line.SuppressedEvents = m.takeSuppressedLocked()
	r.mu.Unlock()
	raw, _ := json.Marshal(line)
	r.deliverRunning(m, guideMonitorHeader+"\n"+string(raw))
}

// deliverRunning posts an event batch or summary only while m is still
// running. The batch was built under r.mu, then released for marshalling;
// re-checking right before the post keeps a stop or replace that landed in
// between from producing one stale turn. (Final notices use deliver
// directly: they are sent precisely because m stopped.)
func (r *guideMonitorRegistry) deliverRunning(m *guideMonitor, body string) {
	r.mu.Lock()
	stopped := m.stopped
	r.mu.Unlock()
	if stopped {
		return
	}
	r.deliver(m, body)
}

func (r *guideMonitorRegistry) scheduleSummaryLocked(m *guideMonitor, now time.Time) {
	if m.summaryTimer != nil || len(m.sent) == 0 {
		return
	}
	wait := m.sent[0].Add(guideMonitorBurstWindow).Sub(now)
	if wait < 0 {
		wait = 0
	}
	mon := m
	m.summaryTimer = r.deps.clock.AfterFunc(wait, func() { r.summary(mon) })
}

func (r *guideMonitorRegistry) summary(m *guideMonitor) {
	r.mu.Lock()
	m.summaryTimer = nil
	if m.stopped || m.pendingSuppressed == 0 {
		r.mu.Unlock()
		return
	}
	now := r.deps.clock.Now()
	m.pruneSent(now)
	if len(m.sent) >= guideMonitorBurstCap {
		r.scheduleSummaryLocked(m, now)
		r.mu.Unlock()
		return
	}
	m.sent = append(m.sent, now)
	m.delivered++
	n, ev := m.takeSuppressedLocked()
	line := guideSummaryLine{MonitorID: m.id, Event: guideEventSummary, SuppressedCount: n, SuppressedEvents: ev, RemainingSec: m.remainingSec(now)}
	r.mu.Unlock()
	raw, _ := json.Marshal(line)
	r.deliverRunning(m, guideMonitorHeader+"\n"+string(raw))
}

// deliver posts body to m's owner unless the owner session is gone, in which
// case the monitor is torn down silently (session_closed).
func (r *guideMonitorRegistry) deliver(m *guideMonitor, body string) {
	if r.deps.live != nil && !r.deps.live(m.owner) {
		r.mu.Lock()
		changed := !m.stopped
		if changed {
			r.teardownLocked(m, guideStopSession)
		}
		r.mu.Unlock()
		if changed {
			r.broadcast()
		}
		return
	}
	if r.deps.deliver != nil {
		_ = r.deps.deliver(m.owner, body)
	}
}

func (r *guideMonitorRegistry) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	for _, m := range r.monitors {
		r.teardownLocked(m, guideStopDaemon)
	}
	if r.sweepTimer != nil {
		r.sweepTimer.Stop()
		r.sweepTimer = nil
	}
}

func (r *guideMonitorRegistry) activeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.monitors)
}

type guidePushMonitor struct {
	MonitorID   string   `json:"monitor_id"`
	Events      []string `json:"events"`
	ExpiresInMs int64    `json:"expires_in_ms"`
}

type guidePushState struct {
	Active   bool               `json:"active"`
	Monitors []guidePushMonitor `json:"monitors"`
}

func (r *guideMonitorRegistry) stateJSON() []byte {
	r.mu.Lock()
	now := r.deps.clock.Now()
	st := guidePushState{Active: len(r.monitors) > 0, Monitors: make([]guidePushMonitor, 0, len(r.monitors))}
	for _, m := range r.monitors {
		ms := m.expiresAt.Sub(now).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		st.Monitors = append(st.Monitors, guidePushMonitor{MonitorID: m.id, Events: append([]string(nil), m.events...), ExpiresInMs: ms})
	}
	r.mu.Unlock()
	sort.Slice(st.Monitors, func(i, j int) bool { return st.Monitors[i].MonitorID < st.Monitors[j].MonitorID })
	raw, _ := json.Marshal(st)
	return raw
}

// broadcast pushes the full active set to every registered tab.
func (r *guideMonitorRegistry) broadcast() {
	if r.deps.push == nil {
		return
	}
	r.pushMu.Lock()
	defer r.pushMu.Unlock()
	r.deps.push(DaemonGuideMonitorEvent, r.stateJSON())
}

// pushTo sends the active set to one newly registered tab while any monitor
// runs.
func (r *guideMonitorRegistry) pushTo(enqueue func(event string, data []byte) bool) {
	if r.activeCount() == 0 {
		return
	}
	r.pushMu.Lock()
	defer r.pushMu.Unlock()
	enqueue(DaemonGuideMonitorEvent, r.stateJSON())
}

// sanitizeGuideField strips control characters and clamps s to maxBytes on a
// rune boundary.
func sanitizeGuideField(s string, maxBytes int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s))
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

type guideMonitorFrame struct {
	Event        string `json:"event"`
	MonitorEvent string `json:"monitor_event"`
	MonitorID    string `json:"monitor_id"`
	Route        string `json:"route"`
	Section      string `json:"section"`
	Modal        string `json:"modal"`
	HighlightID  string `json:"highlight_id"`
}

// handleGuideFrame routes a guide-monitor-* frame from a registered local tab.
// The stream's own registered tab id is authoritative; frames from an
// unregistered stream are dropped, and a client route_changed is never
// accepted (route changes are server-derived from heartbeats).
func (s *daemonBridgeStream) handleGuideFrame(frame string) {
	var msg guideMonitorFrame
	if json.Unmarshal([]byte(frame), &msg) != nil {
		return
	}
	b := s.b
	b.mu.Lock()
	if s.closed || !s.registered || s.tabID == "" || b.guide == nil {
		b.mu.Unlock()
		return
	}
	g, tabID := b.guide, s.tabID
	b.mu.Unlock()
	switch msg.Event {
	case guideMonitorStopFrame:
		g.userStop(msg.MonitorID)
	case guideMonitorEventFrame:
		name := strings.TrimSpace(msg.MonitorEvent)
		if name == guideEventRoute || !guideMonitorEventAllowed(name) {
			return
		}
		g.ingest(guideEvent{
			Name:        name,
			TabID:       tabID,
			Route:       sanitizeGuideField(msg.Route, daemonRouteMaxLen),
			Section:     sanitizeGuideField(msg.Section, guideMonitorFieldMax),
			Modal:       sanitizeGuideField(msg.Modal, guideMonitorFieldMax),
			HighlightID: sanitizeGuideField(msg.HighlightID, guideMonitorFieldMax),
		})
	}
}
