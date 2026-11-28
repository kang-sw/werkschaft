package wsdashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func replyNextBridgeRequest(t *testing.T, f *pageFixture, tab wsClient, wantOp string, handler func(params map[string]any) (any, string)) {
	t.Helper()
	go func() {
		ev := waitForWSEvent(t, tab.events, DaemonBridgeRequestEvent)
		var req struct {
			RequestID string         `json:"request_id"`
			Nonce     string         `json:"nonce"`
			TabID     string         `json:"tab_id"`
			Op        string         `json:"op"`
			Params    map[string]any `json:"params"`
		}
		if err := json.Unmarshal([]byte(ev.data), &req); err != nil {
			t.Errorf("unmarshal bridge request: %v", err)
			return
		}
		if wantOp != "" && req.Op != wantOp {
			t.Errorf("bridge op = %q, want %q", req.Op, wantOp)
			return
		}
		payload, errCode := handler(req.Params)
		body := map[string]any{
			"request_id": req.RequestID,
			"nonce":      req.Nonce,
			"tab_id":     req.TabID,
		}
		if errCode != "" {
			body["error"] = errCode
		} else {
			body["payload"] = payload
		}
		resp := postBridgeReply(t, f, body)
		resp.Body.Close()
	}()
}

func settleNextConfirm(t *testing.T, f *pageFixture, tab wsClient, decision string, check func(confirmPushPayload)) {
	t.Helper()
	go func() {
		ev := waitForWSEvent(t, tab.events, DaemonConfirmRequestEvent)
		var pushed confirmPushPayload
		if err := json.Unmarshal([]byte(ev.data), &pushed); err != nil {
			t.Errorf("unmarshal confirm push: %v", err)
			return
		}
		if check != nil {
			check(pushed)
		}
		resp := postBridgeReply(t, f, map[string]any{
			"request_id": pushed.RequestID,
			"nonce":      pushed.Nonce,
			"tab_id":     pushed.TabID,
			"decision":   decision,
		})
		resp.Body.Close()
	}()
}

func TestDaemonDOMMultiTabTargetingRedactionMaskingAndCaps(t *testing.T) {
	f := startPageFixture(t)

	// No tabs -> immediate no_active_tab.
	if _, err := f.d.DaemonDOM(context.Background(), DaemonDOMRequest{}); err == nil {
		t.Fatal("expected no_active_tab error when no tabs connected")
	} else if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "no_active_tab" {
		t.Fatalf("err = %v, want no_active_tab", err)
	}

	tabA := openWS(t, f.base, "/rail/events?bridge=1&tab_id=tab-a&display=1", f.base, true)
	tabB := openWS(t, f.base, "/rail/events?bridge=1&tab_id=tab-b&display=1", f.base, true)
	_ = websocket.Message.Send(tabA.conn, `{"event":"tab-heartbeat","tab_id":"tab-a","visible":true,"focused":false,"last_focused_at":1000,"active_route":"/s/a","active_reply_id":"a"}`)
	_ = websocket.Message.Send(tabB.conn, `{"event":"tab-heartbeat","tab_id":"tab-b","visible":true,"focused":true,"last_focused_at":2000,"active_route":"/s/b","active_reply_id":"b"}`)
	waitForDaemonTabs(t, f.d, func(tabs []DaemonTabInfo) bool {
		return len(tabs) == 2 && tabs[0].TabID == "tab-b"
	})

	cookieVal, err := f.d.localhostCookieValue()
	if err != nil {
		t.Fatal(err)
	}

	// Default target hits most recently focused visible tab (tab-b), filters
	// excluded nodes, masks password & secret inputs, and redacts credentials
	// including live daemon tokens/cookies and link URLs/text.
	replyNextBridgeRequest(t, f, tabB, "dom.snapshot", func(params map[string]any) (any, string) {
		gen := uint64(params["generation"].(float64))
		return DaemonDOMSnapshot{
			Route:   "/s/b?token=secretQuery123",
			Hash:    "#entry=e1",
			ReplyID: "b",
			Title:   "Bearer superSecretBearerToken9999",
			Frame: &DaemonDOMFrameState{
				Path:    "/s/b",
				ReplyID: "b",
				Title:   "Cookie " + cookieVal,
				Ready:   true,
			},
			Nodes: []DaemonDOMNode{
				{
					Ref:    fmt.Sprintf("g%d-r1", gen),
					Scope:  "shell",
					Tag:    "button",
					Role:   "button",
					Name:   "Approve Confirmation",
					DataWS: map[string]string{"data-ws-dom-exclude": "1"},
				},
				{
					Ref:       fmt.Sprintf("g%d-r2", gen),
					Scope:     "frame",
					Tag:       "input",
					Role:      "textbox",
					Name:      "Password",
					InputType: "password",
					Value:     "raw-password-leak",
				},
				{
					Ref:       fmt.Sprintf("g%d-r3", gen),
					Scope:     "frame",
					Tag:       "input",
					Role:      "textbox",
					Name:      "API Key",
					InputType: "text",
					Value:     "sk-abcdefghijklmnopqrstuvwx",
				},
				{
					Ref:    fmt.Sprintf("g%d-r4", gen),
					Scope:  "frame",
					Tag:    "a",
					Role:   "link",
					Name:   "Open LAN https://host/?token=lanSecretVal123",
					Text:   "Bearer abcdefghijklmnop",
					Href:   "http://127.0.0.1/?login=loginSecretVal456",
					DataWS: map[string]string{"data-ws-link": "ws_dashboard_localhost=" + cookieVal},
				},
			},
		}, ""
	})

	snap, err := f.d.DaemonDOM(context.Background(), DaemonDOMRequest{})
	if err != nil {
		t.Fatalf("DaemonDOM failed: %v", err)
	}
	if snap.TabID != "tab-b" || snap.Generation != 1 {
		t.Fatalf("snap target/gen = (%q, %d), want (tab-b, 1)", snap.TabID, snap.Generation)
	}
	if len(snap.Nodes) != 3 {
		t.Fatalf("expected 3 nodes after excluding data-ws-dom-exclude=1, got %d: %+v", len(snap.Nodes), snap.Nodes)
	}
	rawJSON, _ := json.Marshal(snap)
	s := string(rawJSON)
	for _, secret := range []string{"raw-password-leak", "sk-abcdefghijklmnopqrstuvwx", "secretQuery123", "superSecretBearerToken9999", "lanSecretVal123", "loginSecretVal456", cookieVal, "Approve Confirmation"} {
		if strings.Contains(s, secret) {
			t.Fatalf("DOM snapshot leaked forbidden/secret string %q in: %s", secret, s)
		}
	}
	if !snap.Nodes[0].Masked || snap.Nodes[0].Value != "[REDACTED]" {
		t.Fatalf("password node = %+v, want masked=[REDACTED]", snap.Nodes[0])
	}
	if !snap.Nodes[1].Masked || snap.Nodes[1].Value != "[REDACTED]" {
		t.Fatalf("api key node = %+v, want masked=[REDACTED]", snap.Nodes[1])
	}

	// Explicit tab_id targets tab-a even when tab-b is focused, and enforces
	// the 1500-node and 64 KiB hard caps with truncated=true.
	replyNextBridgeRequest(t, f, tabA, "dom.snapshot", func(params map[string]any) (any, string) {
		gen := uint64(params["generation"].(float64))
		nodes := make([]DaemonDOMNode, 0, 1700)
		for i := 1; i <= 1700; i++ {
			nodes = append(nodes, DaemonDOMNode{
				Ref:   fmt.Sprintf("g%d-r%d", gen, i),
				Scope: "frame",
				Tag:   "button",
				Role:  "button",
				Name:  fmt.Sprintf("Button %d %s", i, strings.Repeat("x", 90)),
			})
		}
		return DaemonDOMSnapshot{Route: "/s/a", ReplyID: "a", Nodes: nodes}, ""
	})

	capped, err := f.d.DaemonDOM(context.Background(), DaemonDOMRequest{TabID: "tab-a"})
	if err != nil {
		t.Fatalf("capped DaemonDOM failed: %v", err)
	}
	if capped.TabID != "tab-a" || !capped.Truncated {
		t.Fatalf("capped = (tab=%q, truncated=%v), want (tab-a, true)", capped.TabID, capped.Truncated)
	}
	if capped.NodeCount > DaemonDOMMaxNodes || len(capped.Nodes) != capped.NodeCount {
		t.Fatalf("node count = %d (len %d), exceeds cap %d", capped.NodeCount, len(capped.Nodes), DaemonDOMMaxNodes)
	}
	encoded, _ := json.Marshal(capped)
	if len(encoded) > DaemonDOMMaxBytes || capped.ByteSize > DaemonDOMMaxBytes {
		t.Fatalf("encoded=%d byte_size=%d exceeds 64 KiB cap %d", len(encoded), capped.ByteSize, DaemonDOMMaxBytes)
	}
	// Ref dropped by truncation must NOT be accepted as valid.
	if _, err := f.d.DaemonUI(context.Background(), DaemonUIRequest{
		TabID:  "tab-a",
		Action: "click_ref",
		Ref:    fmt.Sprintf("g%d-r1700", capped.Generation),
	}); err == nil {
		t.Fatal("expected ref_not_found for truncated-out ref")
	} else if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "ref_not_found" {
		t.Fatalf("truncated ref err = %v, want ref_not_found", err)
	}
}

func TestDaemonUIRefLifecycleConfirmationAndAllowlist(t *testing.T) {
	f := startPageFixture(t)
	tab := openWS(t, f.base, "/rail/events?bridge=1&tab_id=tab-ui&display=1", f.base, true)
	_ = websocket.Message.Send(tab.conn, `{"event":"tab-heartbeat","tab_id":"tab-ui","visible":true,"focused":true,"last_focused_at":3000,"active_route":"/s/sess1","active_reply_id":"sess1"}`)
	waitForDaemonTabs(t, f.d, func(tabs []DaemonTabInfo) bool { return len(tabs) == 1 })

	// 1. Disallowed actions (arbitrary eval, unknown action) and unsafe paths are rejected immediately.
	for _, bad := range []DaemonUIRequest{
		{Action: "eval", Value: "alert(1)"},
		{Action: "navigate", Path: "https://evil.example/"},
		{Action: "navigate", Path: "//evil.example/x"},
		{Action: "navigate", Path: "/s/../settings"},
		{Action: "focus_session", ReplyID: "../bad"},
		{Action: "select_panel", Panel: "arbitrary_css_selector"},
	} {
		if _, err := f.d.DaemonUI(context.Background(), bad); err == nil {
			t.Fatalf("expected invalid_request for %+v", bad)
		} else if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "invalid_request" {
			t.Fatalf("bad %+v err = %v, want invalid_request", bad, err)
		}
	}

	// 2. click_ref before any snapshot -> stale_ref immediately without banner.
	if _, err := f.d.DaemonUI(context.Background(), DaemonUIRequest{Action: "click_ref", Ref: "g1-r1"}); err == nil {
		t.Fatal("expected stale_ref before snapshot")
	} else if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "stale_ref" {
		t.Fatalf("pre-snapshot err = %v, want stale_ref", err)
	}

	// 3. Take snapshot gen=1 with normal button + password input.
	replyNextBridgeRequest(t, f, tab, "dom.snapshot", func(params map[string]any) (any, string) {
		gen := uint64(params["generation"].(float64))
		return DaemonDOMSnapshot{
			Route: "/s/sess1", ReplyID: "sess1",
			Nodes: []DaemonDOMNode{
				{Ref: fmt.Sprintf("g%d-r1", gen), Scope: "frame", Tag: "button", Role: "button", Name: "Run Build"},
				{Ref: fmt.Sprintf("g%d-r2", gen), Scope: "frame", Tag: "input", Role: "textbox", Name: "Secret Pass", InputType: "password"},
			},
		}, ""
	})
	snap1, err := f.d.DaemonDOM(context.Background(), DaemonDOMRequest{})
	if err != nil {
		t.Fatalf("snap1: %v", err)
	}

	// 4. Unknown ref in current generation -> ref_not_found.
	if _, err := f.d.DaemonUI(context.Background(), DaemonUIRequest{Action: "click_ref", Ref: "g1-r999"}); err == nil {
		t.Fatal("expected ref_not_found")
	} else if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "ref_not_found" {
		t.Fatalf("unknown ref err = %v, want ref_not_found", err)
	}

	// 5. click_ref denied in confirmation banner -> denied (never sends ui.click_ref).
	settleNextConfirm(t, f, tab, "deny", func(p confirmPushPayload) {
		if p.Action != "click_ref" || p.TargetLabel != "Run Build" {
			t.Errorf("confirm push = %+v, want click_ref / Run Build", p)
		}
	})
	if _, err := f.d.DaemonUI(context.Background(), DaemonUIRequest{Action: "click_ref", Ref: "g1-r1"}); err == nil {
		t.Fatal("expected denied")
	} else if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "denied" {
		t.Fatalf("denied err = %v, want denied", err)
	}

	// 6. fill_ref on password input masks fill_value in confirmation banner and
	// executes when approved.
	settleNextConfirm(t, f, tab, "approve", func(p confirmPushPayload) {
		if p.Action != "fill_ref" || p.FillValue != "[REDACTED]" {
			t.Errorf("password fill confirm push = %+v, want fill_value=[REDACTED]", p)
		}
		if strings.Contains(p.Summary, "my-secret-pw") {
			t.Errorf("password fill summary leaked plaintext: %q", p.Summary)
		}
		// After approval, respond to ui.fill_ref bridge request.
		replyNextBridgeRequest(t, f, tab, "ui.fill_ref", func(params map[string]any) (any, string) {
			if params["ref"] != "g1-r2" || params["value"] != "my-secret-pw" {
				t.Errorf("ui.fill_ref params = %+v", params)
			}
			return map[string]any{"ok": true, "route": "/s/sess1", "masked": true}, ""
		})
	})
	fillRes, err := f.d.DaemonUI(context.Background(), DaemonUIRequest{
		Action: "fill_ref",
		Ref:    "g1-r2",
		Value:  "my-secret-pw",
	})
	if err != nil {
		t.Fatalf("approved fill_ref failed: %v", err)
	}
	if !fillRes.OK || !fillRes.Masked || fillRes.Ref != "g1-r2" {
		t.Fatalf("fillRes = %+v, want ok+masked on g1-r2", fillRes)
	}

	// 7. Taking snapshot gen=2 invalidates gen=1 refs -> stale_ref.
	replyNextBridgeRequest(t, f, tab, "dom.snapshot", func(params map[string]any) (any, string) {
		gen := uint64(params["generation"].(float64))
		return DaemonDOMSnapshot{
			Route: "/s/sess1", ReplyID: "sess1",
			Nodes: []DaemonDOMNode{{Ref: fmt.Sprintf("g%d-r1", gen), Scope: "frame", Tag: "button", Role: "button", Name: "Run Build"}},
		}, ""
	})
	snap2, err := f.d.DaemonDOM(context.Background(), DaemonDOMRequest{})
	if err != nil || snap2.Generation != snap1.Generation+1 {
		t.Fatalf("snap2 = (%+v, %v), want gen=%d", snap2, err, snap1.Generation+1)
	}
	if _, err := f.d.DaemonUI(context.Background(), DaemonUIRequest{Action: "click_ref", Ref: "g1-r1"}); err == nil {
		t.Fatal("expected stale_ref for gen 1 ref after gen 2 snapshot")
	} else if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "stale_ref" {
		t.Fatalf("stale gen err = %v, want stale_ref", err)
	}

	// 8. Immediate navigate runs without confirmation and invalidates active gen=2 refs.
	replyNextBridgeRequest(t, f, tab, "ui.navigate", func(params map[string]any) (any, string) {
		if params["path"] != "/s/sess2#entry=e9" {
			t.Errorf("navigate path = %v", params["path"])
		}
		return map[string]any{"ok": true, "route": "/s/sess2", "hash": "#entry=e9", "reply_id": "sess2"}, ""
	})
	navRes, err := f.d.DaemonUI(context.Background(), DaemonUIRequest{Action: "navigate", Path: "/s/sess2#entry=e9"})
	if err != nil || !navRes.OK || navRes.Route != "/s/sess2" {
		t.Fatalf("navigate = (%+v, %v)", navRes, err)
	}
	if _, err := f.d.DaemonUI(context.Background(), DaemonUIRequest{Action: "click_ref", Ref: "g2-r1"}); err == nil {
		t.Fatal("expected stale_ref after navigation")
	} else if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "stale_ref" {
		t.Fatalf("post-nav err = %v, want stale_ref", err)
	}

	// 9. Tab disconnect mid-DOM request resolves tab_gone immediately.
	resCh := make(chan error, 1)
	go func() {
		_, err := f.d.DaemonDOM(context.Background(), DaemonDOMRequest{})
		resCh <- err
	}()
	_ = waitForWSEvent(t, tab.events, DaemonBridgeRequestEvent)
	_ = tab.conn.Close()
	select {
	case err := <-resCh:
		if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "tab_gone" {
			t.Fatalf("disconnect err = %v, want tab_gone", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tab_gone")
	}
}
