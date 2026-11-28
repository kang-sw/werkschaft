package wsdashboard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DaemonBridgeReplyPath is the TrustedWrite endpoint tabs post confirmation
	// decisions and bridge payloads back to.
	DaemonBridgeReplyPath = "/api/daemon/bridge-reply"

	// DaemonConfirmRequestEvent pushes a one-shot 1-click confirmation banner
	// request to a single targeted local dashboard shell tab.
	DaemonConfirmRequestEvent = "daemon-confirm-request"
	// DaemonConfirmClearEvent clears a timed-out or cancelled confirmation
	// banner on the targeted tab.
	DaemonConfirmClearEvent = "daemon-confirm-clear"
	// DaemonBridgeRequestEvent pushes a daemon->tab bridge operation (Phase 2/3)
	// over the existing /rail/events WebSocket stream.
	DaemonBridgeRequestEvent = "daemon-bridge-request"

	daemonConfirmDefaultTimeout = 120 * time.Second
	daemonBridgeReplyMaxBytes   = 8 << 20
	daemonBridgePushBuffer      = 32
	daemonTabIDMaxLen           = 64
	daemonRouteMaxLen           = 256
	daemonReplyIDMaxLen         = 64
)

// DaemonControlError carries a structured error code from the dashboard control
// layer (ticket 261128-feat-ws-dashboard-control-mcp-api, (1756)).
type DaemonControlError struct {
	Code    string `json:"error"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message"`
}

func (e *DaemonControlError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Code + ": " + e.Message
	}
	return e.Code
}

// DaemonTabCapabilities describes optional capture/UI capabilities reported by
// a connected local dashboard shell tab.
type DaemonTabCapabilities struct {
	NativeCapture bool `json:"native_capture"`
	ConfirmBanner bool `json:"confirm_banner"`
}

// DaemonTabInfo is a read-only snapshot of a connected local dashboard tab.
type DaemonTabInfo struct {
	TabID         string                `json:"tab_id"`
	Visible       bool                  `json:"visible"`
	Focused       bool                  `json:"focused"`
	LastFocusedAt int64                 `json:"last_focused_at"`
	ActiveRoute   string                `json:"active_route,omitempty"`
	ActiveReplyID string                `json:"active_reply_id,omitempty"`
	Capabilities  DaemonTabCapabilities `json:"capabilities"`
}

// DaemonConfirmRequest describes a mutating operation awaiting 1-click user
// approval in the top-level dashboard shell.
type DaemonConfirmRequest struct {
	TabID          string `json:"tab_id,omitempty"`
	Tool           string `json:"tool"`
	Method         string `json:"method,omitempty"`
	Path           string `json:"path,omitempty"`
	Action         string `json:"action,omitempty"`
	TargetLabel    string `json:"target_label,omitempty"`
	FillValue      string `json:"fill_value,omitempty"`
	Summary        string `json:"summary"`
	CompanionPause bool   `json:"companion_pause,omitempty"`
}

// DaemonConfirmResult is the settled outcome of RequestDaemonConfirmation.
type DaemonConfirmResult struct {
	Approved  bool   `json:"approved"`
	Code      string `json:"code,omitempty"`
	Reason    string `json:"reason,omitempty"`
	TabID     string `json:"tab_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type bridgePushEvent struct {
	Event string
	Data  []byte
}

type bridgeReplyOutcome struct {
	Decision    string
	Err         string
	Payload     json.RawMessage
	Binary      []byte
	ContentType string
}

type bridgePending struct {
	requestID string
	nonce     string
	kind      string // "confirm" | "bridge"
	tabID     string
	tabStream *daemonBridgeStream
	ch        chan bridgeReplyOutcome
}

type daemonBridge struct {
	mu       sync.Mutex
	nextID   uint64
	focusSeq uint64
	tabs     map[string]*daemonBridgeStream
	pending  map[string]*bridgePending
	// guide is the ws/daemon.monitor registry, created on first use
	// (Daemon.guideMonitors); guideClosed marks Daemon.Close.
	guide       *guideMonitorRegistry
	guideClosed bool
}

type daemonRefInfo struct {
	Ref       string
	Scope     string
	Role      string
	Tag       string
	Label     string
	Sensitive bool
	InputType string
	Excluded  bool
}

type daemonSnapshotState struct {
	generation  uint64
	navVersion  uint64
	route       string
	replyID     string
	invalidated bool
	refs        map[string]daemonRefInfo
}

type daemonBridgeStream struct {
	b             *daemonBridge
	streamID      uint64
	tabID         string
	registered    bool
	closed        bool
	visible       bool
	focused       bool
	lastFocusedAt int64
	focusSeq      uint64
	activeRoute   string
	activeReplyID string
	capabilities  DaemonTabCapabilities
	pushCh        chan bridgePushEvent
	nextSnapGen   uint64
	navVersion    uint64
	snapshot      *daemonSnapshotState
}

func (b *daemonBridge) newStream(initialTabID string, initialVisible, optIn bool) *daemonBridgeStream {
	var guide *guideMonitorRegistry
	var s *daemonBridgeStream
	defer func() {
		if guide != nil {
			guide.pushTo(s.enqueue)
		}
	}()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	s = &daemonBridgeStream{
		b:            b,
		streamID:     b.nextID,
		visible:      initialVisible,
		focused:      initialVisible,
		capabilities: DaemonTabCapabilities{ConfirmBanner: true},
		pushCh:       make(chan bridgePushEvent, daemonBridgePushBuffer),
	}
	tabID := sanitizeTabID(initialTabID)
	if optIn || tabID != "" {
		if tabID == "" {
			tabID = mintBridgeID("tab", 8)
		}
		nowMs := time.Now().UnixMilli()
		b.focusSeq++
		s.tabID = tabID
		s.registered = true
		s.lastFocusedAt = nowMs
		s.focusSeq = b.focusSeq
		if b.tabs == nil {
			b.tabs = map[string]*daemonBridgeStream{}
		}
		b.tabs[tabID] = s
		guide = b.guide
	}
	return s
}

func (s *daemonBridgeStream) close() {
	if s == nil || s.b == nil {
		return
	}
	b := s.b
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.snapshot = nil
	if s.registered && s.tabID != "" && b.tabs[s.tabID] == s {
		delete(b.tabs, s.tabID)
	}
	for reqID, p := range b.pending {
		if p.tabStream != s {
			continue
		}
		delete(b.pending, reqID)
		select {
		case p.ch <- bridgeReplyOutcome{Err: "tab_gone"}:
		default:
		}
	}
}

type tabHeartbeatCapabilities struct {
	NativeCapture *bool `json:"native_capture"`
	ConfirmBanner *bool `json:"confirm_banner"`
}

type tabHeartbeatFrame struct {
	Event         string                    `json:"event"`
	TabID         string                    `json:"tab_id"`
	Visible       *bool                     `json:"visible"`
	Focused       *bool                     `json:"focused"`
	LastFocusedAt int64                     `json:"last_focused_at"`
	ActiveRoute   *string                   `json:"active_route"`
	ActiveReplyID *string                   `json:"active_reply_id"`
	Capabilities  *tabHeartbeatCapabilities `json:"capabilities"`
}

func (s *daemonBridgeStream) handleFrame(frame string) {
	if s == nil || s.b == nil || len(frame) == 0 || len(frame) > wsMaxFrame {
		return
	}
	var msg tabHeartbeatFrame
	if json.Unmarshal([]byte(frame), &msg) != nil {
		return
	}
	if msg.Event == guideMonitorEventFrame || msg.Event == guideMonitorStopFrame {
		s.handleGuideFrame(frame)
		return
	}
	// Guide monitor hooks run after the bridge lock is released (deferred
	// before the unlock, so they execute after it).
	var guide *guideMonitorRegistry
	var routeEvent *guideEvent
	newlyRegistered := false
	defer func() {
		if guide == nil {
			return
		}
		if newlyRegistered {
			guide.pushTo(s.enqueue)
		}
		if routeEvent != nil {
			guide.ingest(*routeEvent)
		}
	}()
	b := s.b
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.closed {
		return
	}
	wasRegistered := s.registered
	isHeartbeat := msg.Event == "tab-heartbeat"
	isVisibility := msg.Event == "visibility"
	if !isHeartbeat && !isVisibility {
		return
	}
	tabID := sanitizeTabID(msg.TabID)
	if isVisibility && !s.registered && tabID == "" {
		return
	}
	if isHeartbeat || tabID != "" {
		if tabID == "" {
			if s.tabID != "" {
				tabID = s.tabID
			} else {
				tabID = mintBridgeID("tab", 8)
			}
		}
		if b.tabs == nil {
			b.tabs = map[string]*daemonBridgeStream{}
		}
		if s.registered && s.tabID != "" && s.tabID != tabID && b.tabs[s.tabID] == s {
			delete(b.tabs, s.tabID)
		}
		s.tabID = tabID
		s.registered = true
		b.tabs[tabID] = s
	}
	if msg.Visible != nil {
		s.visible = *msg.Visible
	}
	if msg.Focused != nil {
		s.focused = *msg.Focused && s.visible
	} else if !s.visible {
		s.focused = false
	}
	nowMs := time.Now().UnixMilli()
	focusAdvanced := false
	if msg.LastFocusedAt > 0 {
		if msg.LastFocusedAt > s.lastFocusedAt {
			s.lastFocusedAt = msg.LastFocusedAt
			focusAdvanced = true
		} else if s.lastFocusedAt == 0 {
			s.lastFocusedAt = msg.LastFocusedAt
		}
	} else if isHeartbeat && (s.focused || s.lastFocusedAt == 0) {
		s.lastFocusedAt = nowMs
		focusAdvanced = true
	}
	if s.focused || focusAdvanced {
		b.focusSeq++
		s.focusSeq = b.focusSeq
	}
	navigated := false
	if msg.ActiveRoute != nil {
		nextRoute := clampString(*msg.ActiveRoute, daemonRouteMaxLen)
		if s.activeRoute != "" && nextRoute != s.activeRoute {
			navigated = true
		}
		s.activeRoute = nextRoute
	}
	if msg.ActiveReplyID != nil {
		nextReply := clampString(*msg.ActiveReplyID, daemonReplyIDMaxLen)
		if s.activeReplyID != "" && nextReply != s.activeReplyID {
			navigated = true
		}
		s.activeReplyID = nextReply
	}
	if navigated {
		s.invalidateSnapshotLocked()
	}
	if b.guide != nil {
		guide = b.guide
		newlyRegistered = !wasRegistered && s.registered
		if navigated && s.tabID != "" {
			routeEvent = &guideEvent{Name: guideEventRoute, TabID: s.tabID, Route: sanitizeGuideField(s.activeRoute, daemonRouteMaxLen)}
		}
	}
	if msg.Capabilities != nil {
		if msg.Capabilities.NativeCapture != nil {
			s.capabilities.NativeCapture = *msg.Capabilities.NativeCapture
		}
		if msg.Capabilities.ConfirmBanner != nil {
			s.capabilities.ConfirmBanner = *msg.Capabilities.ConfirmBanner
		}
	}
}

func (s *daemonBridgeStream) enqueue(event string, data []byte) bool {
	select {
	case s.pushCh <- bridgePushEvent{Event: event, Data: data}:
		return true
	default:
		return false
	}
}

func (b *daemonBridge) selectTabLocked(explicitTabID string, requireConfirm bool) (*daemonBridgeStream, error) {
	if explicitTabID != "" {
		t := b.tabs[explicitTabID]
		if t == nil || t.closed || !t.registered {
			return nil, &DaemonControlError{Code: "no_active_tab", Reason: "no_active_tab", Message: "requested tab_id is not connected"}
		}
		if requireConfirm && !t.capabilities.ConfirmBanner {
			return nil, &DaemonControlError{Code: "no_active_tab", Reason: "no_active_tab", Message: "requested tab does not support confirmation banners"}
		}
		return t, nil
	}
	var best *daemonBridgeStream
	for _, t := range b.tabs {
		if t.closed || !t.registered || !t.visible {
			continue
		}
		if requireConfirm && !t.capabilities.ConfirmBanner {
			continue
		}
		if best == nil || tabRanksHigher(t, best) {
			best = t
		}
	}
	if best == nil {
		return nil, &DaemonControlError{Code: "no_active_tab", Reason: "no_active_tab", Message: "no visible local dashboard tab is available"}
	}
	return best, nil
}

func tabRanksHigher(a, b *daemonBridgeStream) bool {
	if a.lastFocusedAt != b.lastFocusedAt {
		return a.lastFocusedAt > b.lastFocusedAt
	}
	if a.focused != b.focused {
		return a.focused
	}
	if a.focusSeq != b.focusSeq {
		return a.focusSeq > b.focusSeq
	}
	return a.streamID > b.streamID
}

// DaemonTabs returns a snapshot of all registered local-viewer shell tabs,
// ordered from most-recently-focused visible tab first.
func (d *Daemon) DaemonTabs() []DaemonTabInfo {
	b := &d.daemonBridge
	b.mu.Lock()
	defer b.mu.Unlock()
	streams := make([]*daemonBridgeStream, 0, len(b.tabs))
	for _, t := range b.tabs {
		if t.closed || !t.registered {
			continue
		}
		streams = append(streams, t)
	}
	sort.Slice(streams, func(i, j int) bool {
		if streams[i].visible != streams[j].visible {
			return streams[i].visible
		}
		return tabRanksHigher(streams[i], streams[j])
	})
	out := make([]DaemonTabInfo, 0, len(streams))
	for _, t := range streams {
		out = append(out, DaemonTabInfo{
			TabID:         t.tabID,
			Visible:       t.visible,
			Focused:       t.focused,
			LastFocusedAt: t.lastFocusedAt,
			ActiveRoute:   t.activeRoute,
			ActiveReplyID: t.activeReplyID,
			Capabilities:  t.capabilities,
		})
	}
	return out
}

func (d *Daemon) confirmTimeout() time.Duration {
	if d.daemonConfirmTimeout > 0 {
		return d.daemonConfirmTimeout
	}
	return daemonConfirmDefaultTimeout
}

type confirmPushPayload struct {
	RequestID   string `json:"request_id"`
	Nonce       string `json:"nonce"`
	TabID       string `json:"tab_id"`
	Tool        string `json:"tool"`
	Method      string `json:"method,omitempty"`
	Path        string `json:"path,omitempty"`
	Action      string `json:"action,omitempty"`
	TargetLabel string `json:"target_label,omitempty"`
	FillValue   string `json:"fill_value,omitempty"`
	Summary     string `json:"summary"`
	ExpiresInMs int64  `json:"expires_in_ms"`
}

// RequestDaemonConfirmation blocks up to 120s (or ctx cancellation) behind a
// one-shot 1-click approval banner rendered in the single targeted local shell
// tab. When no eligible local tab exists, it fails closed immediately with
// Code="no_active_tab" without waiting.
func (d *Daemon) RequestDaemonConfirmation(ctx context.Context, req DaemonConfirmRequest) DaemonConfirmResult {
	timeout := d.confirmTimeout()
	requestID := mintBridgeID("dc", 16)
	nonce := mintBridgeID("n", 24)
	if requestID == "" || nonce == "" {
		return DaemonConfirmResult{Approved: false, Code: "denied", Reason: "nonce_unavailable"}
	}

	b := &d.daemonBridge
	b.mu.Lock()
	tab, err := b.selectTabLocked(strings.TrimSpace(req.TabID), true)
	if err != nil {
		b.mu.Unlock()
		return DaemonConfirmResult{Approved: false, Code: "no_active_tab", Reason: "no_active_tab"}
	}
	payload, _ := json.Marshal(confirmPushPayload{
		RequestID:   requestID,
		Nonce:       nonce,
		TabID:       tab.tabID,
		Tool:        req.Tool,
		Method:      req.Method,
		Path:        req.Path,
		Action:      req.Action,
		TargetLabel: req.TargetLabel,
		FillValue:   req.FillValue,
		Summary:     req.Summary,
		ExpiresInMs: timeout.Milliseconds(),
	})
	if !tab.enqueue(DaemonConfirmRequestEvent, payload) {
		b.mu.Unlock()
		return DaemonConfirmResult{Approved: false, Code: "tab_gone", Reason: "tab_queue_full", TabID: tab.tabID, RequestID: requestID}
	}
	ch := make(chan bridgeReplyOutcome, 1)
	if b.pending == nil {
		b.pending = map[string]*bridgePending{}
	}
	b.pending[requestID] = &bridgePending{
		requestID: requestID,
		nonce:     nonce,
		kind:      "confirm",
		tabID:     tab.tabID,
		tabStream: tab,
		ch:        ch,
	}
	tabID := tab.tabID
	b.mu.Unlock()

	if req.CompanionPause {
		d.companionPhone.BeginApprovalPause(requestID, req.Tool, req.Summary)
		defer d.companionPhone.EndApprovalPause(requestID)
	}

	clearPending := func() {
		b.mu.Lock()
		_, existed := b.pending[requestID]
		delete(b.pending, requestID)
		alive := !tab.closed
		b.mu.Unlock()
		if existed && alive {
			clearData, _ := json.Marshal(map[string]string{"request_id": requestID})
			tab.enqueue(DaemonConfirmClearEvent, clearData)
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case out := <-ch:
		if out.Err == "tab_gone" {
			return DaemonConfirmResult{Approved: false, Code: "tab_gone", Reason: "tab_gone", TabID: tabID, RequestID: requestID}
		}
		if out.Decision == "approve" {
			return DaemonConfirmResult{Approved: true, TabID: tabID, RequestID: requestID}
		}
		if out.Decision == "dismiss" {
			return DaemonConfirmResult{Approved: false, Code: "denied", Reason: "dismissed", TabID: tabID, RequestID: requestID}
		}
		return DaemonConfirmResult{Approved: false, Code: "denied", Reason: "denied", TabID: tabID, RequestID: requestID}
	case <-timer.C:
		clearPending()
		return DaemonConfirmResult{Approved: false, Code: "timeout", Reason: "timeout", TabID: tabID, RequestID: requestID}
	case <-ctx.Done():
		clearPending()
		return DaemonConfirmResult{Approved: false, Code: "denied", Reason: "cancelled", TabID: tabID, RequestID: requestID}
	case <-d.stop:
		clearPending()
		return DaemonConfirmResult{Approved: false, Code: "denied", Reason: "daemon_stopped", TabID: tabID, RequestID: requestID}
	}
}

// SendDaemonBridgeRequest pushes a daemon-bridge-request frame to the targeted
// local tab and waits for its correlated single-use POST /api/daemon/bridge-reply.
func (d *Daemon) SendDaemonBridgeRequest(ctx context.Context, explicitTabID, op string, params any, timeout time.Duration) (json.RawMessage, string, error) {
	out, tabID, err := d.sendDaemonBridgeRequest(ctx, explicitTabID, op, params, timeout)
	if err != nil {
		return nil, tabID, err
	}
	return out.Payload, tabID, nil
}

func (d *Daemon) sendDaemonBridgeRequest(ctx context.Context, explicitTabID, op string, params any, timeout time.Duration) (bridgeReplyOutcome, string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	requestID := mintBridgeID("br", 16)
	nonce := mintBridgeID("n", 24)
	if requestID == "" || nonce == "" {
		return bridgeReplyOutcome{}, "", &DaemonControlError{Code: "denied", Message: "nonce unavailable"}
	}

	b := &d.daemonBridge
	b.mu.Lock()
	tab, err := b.selectTabLocked(strings.TrimSpace(explicitTabID), false)
	if err != nil {
		b.mu.Unlock()
		return bridgeReplyOutcome{}, "", err
	}
	payload, err := json.Marshal(struct {
		RequestID string `json:"request_id"`
		Nonce     string `json:"nonce"`
		TabID     string `json:"tab_id"`
		Op        string `json:"op"`
		Params    any    `json:"params,omitempty"`
	}{
		RequestID: requestID,
		Nonce:     nonce,
		TabID:     tab.tabID,
		Op:        op,
		Params:    params,
	})
	if err != nil {
		b.mu.Unlock()
		return bridgeReplyOutcome{}, "", err
	}
	if !tab.enqueue(DaemonBridgeRequestEvent, payload) {
		b.mu.Unlock()
		return bridgeReplyOutcome{}, tab.tabID, &DaemonControlError{Code: "tab_gone", Message: "target tab queue full"}
	}
	ch := make(chan bridgeReplyOutcome, 1)
	if b.pending == nil {
		b.pending = map[string]*bridgePending{}
	}
	b.pending[requestID] = &bridgePending{
		requestID: requestID,
		nonce:     nonce,
		kind:      "bridge",
		tabID:     tab.tabID,
		tabStream: tab,
		ch:        ch,
	}
	tabID := tab.tabID
	b.mu.Unlock()

	cleanup := func() {
		b.mu.Lock()
		delete(b.pending, requestID)
		b.mu.Unlock()
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case out := <-ch:
		if out.Err != "" {
			return bridgeReplyOutcome{}, tabID, &DaemonControlError{Code: out.Err, Message: out.Err}
		}
		return out, tabID, nil
	case <-timer.C:
		cleanup()
		return bridgeReplyOutcome{}, tabID, &DaemonControlError{Code: "timeout", Message: "bridge request timed out"}
	case <-ctx.Done():
		cleanup()
		return bridgeReplyOutcome{}, tabID, &DaemonControlError{Code: "timeout", Message: "bridge request cancelled"}
	case <-d.stop:
		cleanup()
		return bridgeReplyOutcome{}, tabID, &DaemonControlError{Code: "tab_gone", Message: "daemon stopped"}
	}
}

type daemonBridgeReplyRequest struct {
	RequestID string          `json:"request_id"`
	Nonce     string          `json:"nonce"`
	TabID     string          `json:"tab_id,omitempty"`
	Decision  string          `json:"decision,omitempty"`
	Error     string          `json:"error,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// handleDaemonBridgeReply serves POST /api/daemon/bridge-reply behind the outer
// accessGuard (localhost cookie) plus TrustedWrite (localViewer && sameOrigin)
// and single-use (request_id, nonce) correlation.
func (d *Daemon) handleDaemonBridgeReply(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	local, own := TrustedWrite(r)
	if !local {
		http.NotFound(w, r)
		return
	}
	if !own || crossSiteFetch(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (mt != "application/json" && mt != "image/png") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content_type_json_required"})
		return
	}
	if mt == "image/png" {
		d.handleDaemonBridgeBinaryReply(w, r, mt)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, daemonBridgeReplyMaxBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable_body"})
		return
	}
	if len(raw) > daemonBridgeReplyMaxBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "payload_too_large"})
		return
	}
	var body daemonBridgeReplyRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	body.RequestID = strings.TrimSpace(body.RequestID)
	body.Nonce = strings.TrimSpace(body.Nonce)
	if body.RequestID == "" || body.Nonce == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_request_id_or_nonce"})
		return
	}

	b := &d.daemonBridge
	b.mu.Lock()
	p := b.pending[body.RequestID]
	if p == nil {
		b.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "stale_request"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Nonce), []byte(p.nonce)) != 1 {
		b.mu.Unlock()
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid_nonce"})
		return
	}
	if body.TabID != "" && body.TabID != p.tabID {
		b.mu.Unlock()
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "foreign_tab"})
		return
	}
	outcome := bridgeReplyOutcome{
		Err:     strings.TrimSpace(body.Error),
		Payload: body.Payload,
	}
	if p.kind == "confirm" {
		decision := strings.ToLower(strings.TrimSpace(body.Decision))
		switch decision {
		case "approve", "deny", "dismiss":
			outcome.Decision = decision
		default:
			b.mu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_decision"})
			return
		}
	}
	delete(b.pending, body.RequestID)
	b.mu.Unlock()

	select {
	case p.ch <- outcome:
	default:
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (d *Daemon) handleDaemonBridgeBinaryReply(w http.ResponseWriter, r *http.Request, contentType string) {
	requestID := strings.TrimSpace(r.Header.Get("X-WS-Request-ID"))
	nonce := strings.TrimSpace(r.Header.Get("X-WS-Nonce"))
	tabID := strings.TrimSpace(r.Header.Get("X-WS-Tab-ID"))
	if requestID == "" || nonce == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_request_id_or_nonce"})
		return
	}
	b := &d.daemonBridge
	b.mu.Lock()
	p := b.pending[requestID]
	if p == nil {
		b.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "stale_request"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(nonce), []byte(p.nonce)) != 1 {
		b.mu.Unlock()
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid_nonce"})
		return
	}
	if tabID != "" && tabID != p.tabID {
		b.mu.Unlock()
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "foreign_tab"})
		return
	}
	if p.kind != "bridge" {
		b.mu.Unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content_type_json_required"})
		return
	}
	b.mu.Unlock()

	raw, err := io.ReadAll(io.LimitReader(r.Body, daemonBridgeReplyMaxBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable_body"})
		return
	}

	b.mu.Lock()
	if b.pending[requestID] != p {
		b.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "stale_request"})
		return
	}
	delete(b.pending, requestID)
	b.mu.Unlock()

	if len(raw) > daemonBridgeReplyMaxBytes {
		select {
		case p.ch <- bridgeReplyOutcome{Err: "payload_too_large"}:
		default:
		}
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "payload_too_large"})
		return
	}

	select {
	case p.ch <- bridgeReplyOutcome{Binary: raw, ContentType: contentType}:
	default:
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func sanitizeTabID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > daemonTabIDMaxLen {
		return ""
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == ':'
		if !ok {
			return ""
		}
	}
	return raw
}

func clampString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func mintBridgeID(prefix string, nBytes int) string {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(buf))
}

// IsDaemonControlError extracts a structured DaemonControlError if present.
func IsDaemonControlError(err error) (*DaemonControlError, bool) {
	var dce *DaemonControlError
	if errors.As(err, &dce) {
		return dce, true
	}
	return nil, false
}
