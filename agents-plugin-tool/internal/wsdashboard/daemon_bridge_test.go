package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func waitForDaemonTabs(t *testing.T, d *Daemon, check func([]DaemonTabInfo) bool) []DaemonTabInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tabs := d.DaemonTabs()
		if check(tabs) {
			return tabs
		}
		time.Sleep(15 * time.Millisecond)
	}
	tabs := d.DaemonTabs()
	t.Fatalf("timed out waiting for daemon tabs condition; last=%+v", tabs)
	return nil
}

func waitForWSEvent(t *testing.T, events <-chan sseEvent, name string) sseEvent {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("stream closed while waiting for event %q", name)
			}
			if ev.name == name {
				return ev
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for WS event %q", name)
		}
	}
}

func postBridgeReply(t *testing.T, f *pageFixture, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, f.base+DaemonBridgeReplyPath, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", f.base)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(localhostTestCookie(t, f.d))
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDaemonBridgeMultiTabTargetingAndHeartbeats(t *testing.T) {
	f := startPageFixture(t)

	// Two local tabs open /rail/events with bridge opt-in.
	tabA := openWS(t, f.base, "/rail/events?bridge=1&tab_id=tab-a&display=1", f.base, true)
	tabB := openWS(t, f.base, "/rail/events?bridge=1&tab_id=tab-b&display=1", f.base, true)

	// Send heartbeats within wsMaxFrame (1 KiB): tab-b has a newer last_focused_at.
	hbA := `{"event":"tab-heartbeat","tab_id":"tab-a","visible":true,"focused":false,"last_focused_at":1000,"active_route":"/s/sess-a","active_reply_id":"sess-a","capabilities":{"native_capture":false}}`
	hbB := `{"event":"tab-heartbeat","tab_id":"tab-b","visible":true,"focused":true,"last_focused_at":2000,"active_route":"/s/sess-b","active_reply_id":"sess-b","capabilities":{"native_capture":true}}`
	if len(hbA) > wsMaxFrame || len(hbB) > wsMaxFrame {
		t.Fatalf("heartbeat frame exceeds wsMaxFrame (%d): A=%d B=%d", wsMaxFrame, len(hbA), len(hbB))
	}
	if err := websocket.Message.Send(tabA.conn, hbA); err != nil {
		t.Fatal(err)
	}
	if err := websocket.Message.Send(tabB.conn, hbB); err != nil {
		t.Fatal(err)
	}

	tabs := waitForDaemonTabs(t, f.d, func(tabs []DaemonTabInfo) bool {
		return len(tabs) == 2 && tabs[0].TabID == "tab-b" && tabs[0].ActiveReplyID == "sess-b" && tabs[1].ActiveReplyID == "sess-a"
	})
	if !tabs[0].Capabilities.NativeCapture || !tabs[0].Capabilities.ConfirmBanner {
		t.Fatalf("tab-b capabilities = %+v, want native_capture=true and preserved confirm_banner=true", tabs[0].Capabilities)
	}
	if tabs[1].Capabilities.NativeCapture || !tabs[1].Capabilities.ConfirmBanner {
		t.Fatalf("tab-a capabilities = %+v, want native_capture=false and preserved confirm_banner=true", tabs[1].Capabilities)
	}

	// Clearing active_reply_id via explicit empty string works (does not retain stale value).
	if err := websocket.Message.Send(tabB.conn, `{"event":"tab-heartbeat","tab_id":"tab-b","active_route":"/","active_reply_id":""}`); err != nil {
		t.Fatal(err)
	}
	waitForDaemonTabs(t, f.d, func(tabs []DaemonTabInfo) bool {
		return len(tabs) == 2 && tabs[0].TabID == "tab-b" && tabs[0].ActiveRoute == "/" && tabs[0].ActiveReplyID == ""
	})

	// When tab-b becomes hidden via a standard visibility frame, tab-a becomes top target.
	if err := websocket.Message.Send(tabB.conn, `{"event":"visibility","visible":false}`); err != nil {
		t.Fatal(err)
	}
	waitForDaemonTabs(t, f.d, func(tabs []DaemonTabInfo) bool {
		return len(tabs) == 2 && tabs[0].TabID == "tab-a" && tabs[0].Visible && !tabs[1].Visible
	})

	// Confirmation request targets tab-a ONLY (tab-b receives no confirm event).
	resCh := make(chan DaemonConfirmResult, 1)
	go func() {
		resCh <- f.d.RequestDaemonConfirmation(context.Background(), DaemonConfirmRequest{
			Tool:    "daemon.curl",
			Method:  "POST",
			Path:    "/rail/memos",
			Summary: "POST /rail/memos",
		})
	}()

	ev := waitForWSEvent(t, tabA.events, DaemonConfirmRequestEvent)
	var pushed confirmPushPayload
	if err := json.Unmarshal([]byte(ev.data), &pushed); err != nil {
		t.Fatalf("unmarshal confirm push: %v", err)
	}
	if pushed.TabID != "tab-a" || pushed.Method != "POST" || pushed.Path != "/rail/memos" || pushed.RequestID == "" || pushed.Nonce == "" {
		t.Fatalf("unexpected confirm push payload: %+v", pushed)
	}

	// Ensure tab-b did NOT receive a confirmation banner event.
	select {
	case evB := <-tabB.events:
		if evB.name == DaemonConfirmRequestEvent {
			t.Fatalf("non-target tab-b received %s", DaemonConfirmRequestEvent)
		}
	case <-time.After(60 * time.Millisecond):
	}

	// Wrong nonce and foreign tab_id are rejected without consuming pending request.
	badNonceResp := postBridgeReply(t, f, map[string]any{
		"request_id": pushed.RequestID,
		"nonce":      "wrong-nonce",
		"tab_id":     "tab-a",
		"decision":   "approve",
	})
	badNonceResp.Body.Close()
	if badNonceResp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong nonce status = %d, want 403", badNonceResp.StatusCode)
	}
	foreignTabResp := postBridgeReply(t, f, map[string]any{
		"request_id": pushed.RequestID,
		"nonce":      pushed.Nonce,
		"tab_id":     "tab-b",
		"decision":   "approve",
	})
	foreignTabResp.Body.Close()
	if foreignTabResp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign tab status = %d, want 403", foreignTabResp.StatusCode)
	}

	// Legit approval succeeds.
	okResp := postBridgeReply(t, f, map[string]any{
		"request_id": pushed.RequestID,
		"nonce":      pushed.Nonce,
		"tab_id":     "tab-a",
		"decision":   "approve",
	})
	okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("valid approve status = %d, want 200", okResp.StatusCode)
	}
	res := <-resCh
	if !res.Approved || res.TabID != "tab-a" || res.RequestID != pushed.RequestID {
		t.Fatalf("confirm result = %+v, want approved on tab-a", res)
	}

	// Single-use nonce replay is rejected as stale (409).
	replayResp := postBridgeReply(t, f, map[string]any{
		"request_id": pushed.RequestID,
		"nonce":      pushed.Nonce,
		"tab_id":     "tab-a",
		"decision":   "approve",
	})
	replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusConflict {
		t.Fatalf("replay status = %d, want 409", replayResp.StatusCode)
	}

	// Explicit tab_id can still target tab-b even while hidden.
	resChB := make(chan DaemonConfirmResult, 1)
	go func() {
		resChB <- f.d.RequestDaemonConfirmation(context.Background(), DaemonConfirmRequest{
			TabID:   "tab-b",
			Tool:    "daemon.curl",
			Method:  "DELETE",
			Path:    "/rail/memos",
			Summary: "DELETE /rail/memos",
		})
	}()
	evB := waitForWSEvent(t, tabB.events, DaemonConfirmRequestEvent)
	var pushedB confirmPushPayload
	if err := json.Unmarshal([]byte(evB.data), &pushedB); err != nil {
		t.Fatal(err)
	}
	if pushedB.TabID != "tab-b" {
		t.Fatalf("explicit tab_id pushed to %q, want tab-b", pushedB.TabID)
	}
	denyResp := postBridgeReply(t, f, map[string]any{
		"request_id": pushedB.RequestID,
		"nonce":      pushedB.Nonce,
		"tab_id":     "tab-b",
		"decision":   "dismiss",
	})
	denyResp.Body.Close()
	if denyResp.StatusCode != http.StatusOK {
		t.Fatalf("dismiss reply status = %d, want 200", denyResp.StatusCode)
	}
	resB := <-resChB
	if resB.Approved || resB.Code != "denied" || resB.Reason != "dismissed" {
		t.Fatalf("dismiss result = %+v, want denied/dismissed", resB)
	}
}

func TestDaemonBridgeNoActiveTabTimeoutAndDisconnect(t *testing.T) {
	f := startPageFixture(t)

	// No connected tabs -> immediate no_active_tab (never waits 120s).
	start := time.Now()
	res := f.d.RequestDaemonConfirmation(context.Background(), DaemonConfirmRequest{
		Tool: "daemon.curl", Method: "POST", Path: "/rail/memos", Summary: "POST /rail/memos",
	})
	if time.Since(start) > time.Second {
		t.Fatalf("no_active_tab took %v, expected immediate return", time.Since(start))
	}
	if res.Approved || res.Code != "no_active_tab" {
		t.Fatalf("no tab result = %+v, want no_active_tab", res)
	}

	// Timeout pushes daemon-confirm-clear and rejects late reply as stale.
	f.d.daemonConfirmTimeout = 80 * time.Millisecond
	tab := openWS(t, f.base, "/rail/events?bridge=1&tab_id=tab-timeout&display=1", f.base, true)
	waitForDaemonTabs(t, f.d, func(tabs []DaemonTabInfo) bool { return len(tabs) == 1 })

	resCh := make(chan DaemonConfirmResult, 1)
	go func() {
		resCh <- f.d.RequestDaemonConfirmation(context.Background(), DaemonConfirmRequest{
			Tool: "daemon.curl", Method: "POST", Path: "/rail/memos", Summary: "POST /rail/memos",
		})
	}()
	ev := waitForWSEvent(t, tab.events, DaemonConfirmRequestEvent)
	var pushed confirmPushPayload
	_ = json.Unmarshal([]byte(ev.data), &pushed)

	res = <-resCh
	if res.Approved || res.Code != "timeout" {
		t.Fatalf("timeout result = %+v, want timeout", res)
	}
	clearEv := waitForWSEvent(t, tab.events, DaemonConfirmClearEvent)
	if !strings.Contains(clearEv.data, pushed.RequestID) {
		t.Fatalf("clear event = %s, want request_id %s", clearEv.data, pushed.RequestID)
	}
	lateResp := postBridgeReply(t, f, map[string]any{
		"request_id": pushed.RequestID,
		"nonce":      pushed.Nonce,
		"decision":   "approve",
	})
	lateResp.Body.Close()
	if lateResp.StatusCode != http.StatusConflict {
		t.Fatalf("late reply status = %d, want 409 stale_request", lateResp.StatusCode)
	}

	// Tab disconnect mid-wait resolves tab_gone immediately.
	f.d.daemonConfirmTimeout = 10 * time.Second
	resCh2 := make(chan DaemonConfirmResult, 1)
	go func() {
		resCh2 <- f.d.RequestDaemonConfirmation(context.Background(), DaemonConfirmRequest{
			Tool: "daemon.curl", Method: "POST", Path: "/rail/memos", Summary: "POST /rail/memos",
		})
	}()
	_ = waitForWSEvent(t, tab.events, DaemonConfirmRequestEvent)
	_ = tab.conn.Close()

	select {
	case res2 := <-resCh2:
		if res2.Approved || res2.Code != "tab_gone" {
			t.Fatalf("disconnect result = %+v, want tab_gone", res2)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tab_gone after tab disconnect")
	}
	waitForDaemonTabs(t, f.d, func(tabs []DaemonTabInfo) bool { return len(tabs) == 0 })
}

func TestDaemonBridgeReplySecurityAndPayloadCap(t *testing.T) {
	f := startPageFixture(t)
	h := f.d.Handler()

	// 1. Missing localhost cookie is rejected by outer accessGuard (401).
	req := httptest.NewRequest(http.MethodPost, DaemonBridgeReplyPath, strings.NewReader(`{"request_id":"x","nonce":"y"}`))
	req.Host = "127.0.0.1:12345"
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:12345")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without localhost cookie status = %d, want 401", rec.Code)
	}

	// 2. Non-local (LAN) viewer cannot reach bridge-reply even with a valid LAN token.
	lanFix := startPageFixture(t, func(o *DaemonOptions) { o.LAN = true })
	lanTok, err := lanFix.d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	lanReq := httptest.NewRequest(http.MethodPost, DaemonBridgeReplyPath, strings.NewReader(`{"request_id":"x","nonce":"y"}`))
	lanReq.Host = "192.168.1.50:12345"
	lanReq.RemoteAddr = "192.168.1.99:54321"
	lanReq.Header.Set("Content-Type", "application/json")
	lanReq.Header.Set("Origin", "http://192.168.1.50:12345")
	lanReq.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: lanTok})
	lanRec := httptest.NewRecorder()
	lanFix.d.Handler().ServeHTTP(lanRec, lanReq)
	if lanRec.Code != http.StatusNotFound {
		t.Fatalf("LAN viewer bridge-reply status = %d, want 404", lanRec.Code)
	}

	// 3. Cross-origin or missing Origin rejected with 403.
	for name, headers := range map[string]map[string]string{
		"missing origin": {"Content-Type": "application/json"},
		"evil origin":    {"Content-Type": "application/json", "Origin": "http://evil.example"},
		"cross-site":     {"Content-Type": "application/json", "Origin": "http://127.0.0.1:12345", "Sec-Fetch-Site": "cross-site"},
	} {
		r := httptest.NewRequest(http.MethodPost, DaemonBridgeReplyPath, strings.NewReader(`{"request_id":"x","nonce":"y"}`))
		r.Host = "127.0.0.1:12345"
		r.RemoteAddr = "127.0.0.1:54321"
		addLocalhostTestCookie(t, f.d, r)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", name, w.Code)
		}
	}

	// 4. Payload exceeding 8 MiB rejected with 413 payload_too_large.
	oversize := bytes.Repeat([]byte("a"), daemonBridgeReplyMaxBytes+64)
	r := httptest.NewRequest(http.MethodPost, DaemonBridgeReplyPath, bytes.NewReader(oversize))
	r.Host = "127.0.0.1:12345"
	r.RemoteAddr = "127.0.0.1:54321"
	addLocalhostTestCookie(t, f.d, r)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:12345")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "payload_too_large") {
		t.Fatalf("oversize bridge-reply = %d %q, want 413 payload_too_large", w.Code, w.Body.String())
	}
}

func TestDaemonBridgeExcludesLANStreamsAndEnforcesWSFrameCap(t *testing.T) {
	// LAN stream: non-loopback Host/RemoteAddr must never be admitted to tab registry.
	f := startPageFixture(t, func(o *DaemonOptions) { o.LAN = true })
	// Verify directly via stream admission rule: localViewer returns false for LAN request.
	lanReq := httptest.NewRequest(http.MethodGet, "/rail/events?bridge=1&tab_id=lan-tab&display=1", nil)
	lanReq.Host = "192.168.1.50:8080"
	lanReq.RemoteAddr = "192.168.1.99:55555"
	if localViewer(lanReq) {
		t.Fatal("expected localViewer to be false for LAN stream request")
	}

	// Oversized inbound WS frame (> wsMaxFrame = 1 KiB) is refused by ws.MaxPayloadBytes
	// and must not register a tab or raise wsMaxFrame.
	tab := openWS(t, f.base, "/rail/events", f.base, true)
	oversize := `{"event":"tab-heartbeat","tab_id":"big","active_route":"/` + strings.Repeat("x", wsMaxFrame+128) + `"}`
	_ = websocket.Message.Send(tab.conn, oversize)
	// Connection should close or ignore the oversized frame; tab "big" must never appear.
	time.Sleep(100 * time.Millisecond)
	for _, info := range f.d.DaemonTabs() {
		if info.TabID == "big" {
			t.Fatalf("oversized WS frame registered tab %+v", info)
		}
	}
}

func TestDaemonBridgeRequestRoundTrip(t *testing.T) {
	f := startPageFixture(t)
	tab := openWS(t, f.base, "/rail/events?bridge=1&tab_id=tab-rt&display=1", f.base, true)
	waitForDaemonTabs(t, f.d, func(tabs []DaemonTabInfo) bool { return len(tabs) == 1 })

	type result struct {
		payload json.RawMessage
		tabID   string
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		payload, tabID, err := f.d.SendDaemonBridgeRequest(context.Background(), "", "dom", map[string]any{"max_nodes": 10}, 5*time.Second)
		resCh <- result{payload, tabID, err}
	}()

	ev := waitForWSEvent(t, tab.events, DaemonBridgeRequestEvent)
	var reqMsg struct {
		RequestID string `json:"request_id"`
		Nonce     string `json:"nonce"`
		TabID     string `json:"tab_id"`
		Op        string `json:"op"`
	}
	if err := json.Unmarshal([]byte(ev.data), &reqMsg); err != nil {
		t.Fatal(err)
	}
	if reqMsg.Op != "dom" || reqMsg.TabID != "tab-rt" {
		t.Fatalf("unexpected bridge request: %+v", reqMsg)
	}
	resp := postBridgeReply(t, f, map[string]any{
		"request_id": reqMsg.RequestID,
		"nonce":      reqMsg.Nonce,
		"tab_id":     "tab-rt",
		"payload":    map[string]any{"nodes": 3},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bridge reply status = %d, want 200", resp.StatusCode)
	}
	got := <-resCh
	if got.err != nil || got.tabID != "tab-rt" || !strings.Contains(string(got.payload), `"nodes":3`) {
		t.Fatalf("bridge roundtrip = (%s, %q, %v), want nodes=3 on tab-rt", string(got.payload), got.tabID, got.err)
	}
}

func TestDaemonBridgeCompanionPauseEchoAndDeniedMapping(t *testing.T) {
	f := startPageFixture(t)
	tab := openWS(t, f.base, "/rail/events?bridge=1&tab_id=tab-pause&display=1", f.base, true)
	waitForDaemonTabs(t, f.d, func(tabs []DaemonTabInfo) bool { return len(tabs) == 1 })

	// 1. CompanionPause=true on DaemonCurl sets PendingApproval while awaiting
	// confirmation, broadcasts companion-phone SSE, maps timeout to code="denied"
	// (reason="timeout"), and clears PendingApproval automatically on expiry.
	f.d.daemonConfirmTimeout = 120 * time.Millisecond
	errCh := make(chan error, 1)
	go func() {
		_, err := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{
			Method:         "POST",
			Path:           "/health",
			CompanionPause: true,
		})
		errCh <- err
	}()

	ev := waitForWSEvent(t, tab.events, DaemonConfirmRequestEvent)
	var pushed confirmPushPayload
	if err := json.Unmarshal([]byte(ev.data), &pushed); err != nil {
		t.Fatal(err)
	}
	pa := f.d.CompanionPhone().Snapshot().PendingApproval
	if pa == nil || pa.RequestID != pushed.RequestID || pa.Tool != "daemon.curl" || pa.Summary != "POST /health" {
		t.Fatalf("expected PendingApproval while companion-work curl paused, got %+v", pa)
	}

	err := <-errCh
	dce, ok := IsDaemonControlError(err)
	if !ok || dce.Code != "denied" || dce.Reason != "timeout" {
		t.Fatalf("companion-work curl timeout err = (%v, dce=%+v), want code=denied reason=timeout", err, dce)
	}
	if paAfter := f.d.CompanionPhone().Snapshot().PendingApproval; paAfter != nil {
		t.Fatalf("PendingApproval did not clear after timeout: %+v", paAfter)
	}

	// 2. Regular lead (CompanionPause=false) still preserves code="timeout" and
	// does NOT set PendingApproval.
	errChLead := make(chan error, 1)
	go func() {
		_, err := f.d.DaemonCurl(context.Background(), DaemonCurlRequest{
			Method:         "POST",
			Path:           "/health",
			CompanionPause: false,
		})
		errChLead <- err
	}()
	_ = waitForWSEvent(t, tab.events, DaemonConfirmRequestEvent)
	if paLead := f.d.CompanionPhone().Snapshot().PendingApproval; paLead != nil {
		t.Fatalf("regular lead confirm must not set companion PendingApproval: %+v", paLead)
	}
	errLead := <-errChLead
	dceLead, ok := IsDaemonControlError(errLead)
	if !ok || dceLead.Code != "timeout" {
		t.Fatalf("regular lead curl timeout err = (%v, dce=%+v), want code=timeout", errLead, dceLead)
	}

	// 3. CompanionPause=true on RequestDaemonConfirmation clears PendingApproval on
	// both approve and tab disconnect (tab_gone).
	f.d.daemonConfirmTimeout = 5 * time.Second
	resCh := make(chan DaemonConfirmResult, 1)
	go func() {
		resCh <- f.d.RequestDaemonConfirmation(context.Background(), DaemonConfirmRequest{
			Tool:           "config.tune",
			Summary:        "config.tune (key=workflow.lang, scope=global)",
			CompanionPause: true,
		})
	}()
	ev2 := waitForWSEvent(t, tab.events, DaemonConfirmRequestEvent)
	var pushed2 confirmPushPayload
	_ = json.Unmarshal([]byte(ev2.data), &pushed2)
	if pa2 := f.d.CompanionPhone().Snapshot().PendingApproval; pa2 == nil || pa2.RequestID != pushed2.RequestID || pa2.Tool != "config.tune" {
		t.Fatalf("PendingApproval during config.tune = %+v, want request %s", pa2, pushed2.RequestID)
	}
	_ = tab.conn.Close()
	res2 := <-resCh
	if res2.Approved || res2.Code != "tab_gone" {
		t.Fatalf("tab disconnect result = %+v, want tab_gone", res2)
	}
	if paGone := f.d.CompanionPhone().Snapshot().PendingApproval; paGone != nil {
		t.Fatalf("PendingApproval did not clear on tab disconnect: %+v", paGone)
	}
}

func TestCompanionPhoneConcurrentApprovalPauses(t *testing.T) {
	var store CompanionPhoneStore

	// Queue two concurrent approval pauses; snapshot exposes the head (FIFO).
	store.BeginApprovalPause("req-1", "config.tune", "config.tune (key=workflow.lang, value=ko)")
	store.BeginApprovalPause("req-2", "daemon.curl", "POST /api/example")
	if pa := store.Snapshot().PendingApproval; pa == nil || pa.RequestID != "req-1" {
		t.Fatalf("head PendingApproval = %+v, want req-1", pa)
	}

	// Clearing req-1 promotes req-2 without dropping it.
	store.EndApprovalPause("req-1")
	if pa := store.Snapshot().PendingApproval; pa == nil || pa.RequestID != "req-2" || pa.Tool != "daemon.curl" {
		t.Fatalf("promoted PendingApproval = %+v, want req-2", pa)
	}

	// Queue req-3 behind req-2, clear req-3 out of order -> req-2 stays head.
	store.BeginApprovalPause("req-3", "note.write", "note.write (layer=machine)")
	store.EndApprovalPause("req-3")
	if pa := store.Snapshot().PendingApproval; pa == nil || pa.RequestID != "req-2" {
		t.Fatalf("after out-of-order clear PendingApproval = %+v, want req-2", pa)
	}

	// Clearing the final pending approval restores nil cleanly.
	store.EndApprovalPause("req-2")
	if pa := store.Snapshot().PendingApproval; pa != nil {
		t.Fatalf("after final clear PendingApproval = %+v, want nil", pa)
	}
}
