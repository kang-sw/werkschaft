package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 40, G: 120, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func postBinaryBridgeReply(t *testing.T, f *pageFixture, reqID, nonce, tabID string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.base+DaemonBridgeReplyPath, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("Origin", f.base)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-WS-Request-ID", reqID)
	req.Header.Set("X-WS-Nonce", nonce)
	if tabID != "" {
		req.Header.Set("X-WS-Tab-ID", tabID)
	}
	req.AddCookie(localhostTestCookie(t, f.d))
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestDaemonScreenshotTierANativeCaptureAndPayloadCap(t *testing.T) {
	t.Setenv(DaemonScreenshotPlaywrightModuleEnv, "")
	f := startPageFixture(t)
	s := f.d.daemonBridge.newStream("tab-native", true, true)
	defer s.close()
	s.handleFrame(`{"event":"tab-heartbeat","tab_id":"tab-native","visible":true,"focused":true,"last_focused_at":1000,"active_route":"/s/sess1","capabilities":{"confirm_banner":true,"native_capture":true}}`)

	wantPNG := makeTestPNG(t, 64, 48)
	const sessionID = "sesstiera"

	// 1. Happy path: Tier A native PNG arrives over POST /api/daemon/bridge-reply,
	// written under SessionDir(d.dir, sessionID)/screenshots with parsed dimensions
	// and never inlined in the JSON response.
	type shotOut struct {
		resp DaemonScreenshotResponse
		err  error
	}
	done := make(chan shotOut, 1)
	go func() {
		resp, err := f.d.DaemonScreenshot(context.Background(), DaemonScreenshotRequest{
			TabID:     "tab-native",
			SessionID: sessionID,
		})
		done <- shotOut{resp: resp, err: err}
	}()

	var push bridgePushEvent
	select {
	case push = <-s.pushCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for screenshot.capture bridge request")
	}
	if push.Event != DaemonBridgeRequestEvent {
		t.Fatalf("event = %q, want %q", push.Event, DaemonBridgeRequestEvent)
	}
	var reqMsg struct {
		RequestID string `json:"request_id"`
		Nonce     string `json:"nonce"`
		TabID     string `json:"tab_id"`
		Op        string `json:"op"`
	}
	if err := json.Unmarshal(push.Data, &reqMsg); err != nil {
		t.Fatal(err)
	}
	if reqMsg.Op != "screenshot.capture" || reqMsg.TabID != "tab-native" {
		t.Fatalf("unexpected bridge push: %+v", reqMsg)
	}

	// Bad nonce and foreign tab are rejected without consuming the pending request.
	badNonce := postBinaryBridgeReply(t, f, reqMsg.RequestID, "wrong-nonce", "tab-native", wantPNG)
	_ = badNonce.Body.Close()
	if badNonce.StatusCode != http.StatusForbidden {
		t.Fatalf("bad nonce status = %d, want 403", badNonce.StatusCode)
	}
	foreignTab := postBinaryBridgeReply(t, f, reqMsg.RequestID, reqMsg.Nonce, "other-tab", wantPNG)
	_ = foreignTab.Body.Close()
	if foreignTab.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign tab status = %d, want 403", foreignTab.StatusCode)
	}

	// Valid binary PNG reply settles Tier A.
	okRes := postBinaryBridgeReply(t, f, reqMsg.RequestID, reqMsg.Nonce, "tab-native", wantPNG)
	_ = okRes.Body.Close()
	if okRes.StatusCode != http.StatusOK {
		t.Fatalf("valid binary reply status = %d, want 200", okRes.StatusCode)
	}

	// Replay with same nonce is rejected as stale.
	replay := postBinaryBridgeReply(t, f, reqMsg.RequestID, reqMsg.Nonce, "tab-native", wantPNG)
	_ = replay.Body.Close()
	if replay.StatusCode != http.StatusConflict {
		t.Fatalf("replay status = %d, want 409", replay.StatusCode)
	}

	out := <-done
	if out.err != nil {
		t.Fatalf("DaemonScreenshot Tier A error: %v", out.err)
	}
	if out.resp.Mode != "native" || !out.resp.Live || out.resp.TabID != "tab-native" || out.resp.Width != 64 || out.resp.Height != 48 || out.resp.Bytes != len(wantPNG) {
		t.Fatalf("unexpected Tier A response: %+v", out.resp)
	}
	wantDir := filepath.Join(SessionDir(f.d.dir, sessionID), "screenshots")
	if filepath.Dir(out.resp.Path) != wantDir {
		t.Fatalf("PNG path %q not under per-session cache dir %q", out.resp.Path, wantDir)
	}
	diskBytes, err := os.ReadFile(out.resp.Path)
	if err != nil {
		t.Fatalf("read written PNG: %v", err)
	}
	if !bytes.Equal(diskBytes, wantPNG) {
		t.Fatalf("disk PNG mismatch (%d vs %d bytes)", len(diskBytes), len(wantPNG))
	}
	rawJSON, _ := json.Marshal(out.resp)
	if strings.Contains(string(rawJSON), "iVBORw0KGgo") {
		t.Fatalf("response unexpectedly inlined base64 PNG: %s", rawJSON)
	}

	// 2. Oversized (> 8 MiB) binary PNG reply returns 413 and settles with payload_too_large.
	doneBig := make(chan shotOut, 1)
	go func() {
		resp, err := f.d.DaemonScreenshot(context.Background(), DaemonScreenshotRequest{
			TabID:     "tab-native",
			SessionID: sessionID,
		})
		doneBig <- shotOut{resp: resp, err: err}
	}()
	select {
	case push = <-s.pushCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for second screenshot.capture")
	}
	if err := json.Unmarshal(push.Data, &reqMsg); err != nil {
		t.Fatal(err)
	}
	oversized := make([]byte, daemonScreenshotMaxBytes+1)
	copy(oversized, wantPNG)
	bigRes := postBinaryBridgeReply(t, f, reqMsg.RequestID, reqMsg.Nonce, "tab-native", oversized)
	_ = bigRes.Body.Close()
	if bigRes.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want 413", bigRes.StatusCode)
	}
	outBig := <-doneBig
	dce, ok := IsDaemonControlError(outBig.err)
	if !ok || dce.Code != "payload_too_large" {
		t.Fatalf("oversized error = %v, want payload_too_large", outBig.err)
	}
}

func TestDaemonScreenshotTierBCredentialScopingIsolationAndRevocation(t *testing.T) {
	t.Setenv(DaemonScreenshotPlaywrightModuleEnv, "")
	f := startPageFixture(t)
	s := f.d.daemonBridge.newStream("tab-browser", true, true)
	defer s.close()
	s.handleFrame(`{"event":"tab-heartbeat","tab_id":"tab-browser","visible":true,"focused":true,"last_focused_at":1000,"active_route":"/","capabilities":{"confirm_banner":true,"native_capture":false}}`)

	// Verify WS_PLAYWRIGHT_MODULE (test-only) is NEVER read for Tier B.
	t.Setenv("WS_PLAYWRIGHT_MODULE", "/nonexistent/test-only-playwright")
	if mod := f.d.configuredScreenshotPlaywrightModule(); mod != "" {
		t.Fatalf("test-only WS_PLAYWRIGHT_MODULE leaked into Tier B config: %q", mod)
	}

	// Verify short-lived screenshot cookie rules directly on the daemon handler:
	// loopback GET/HEAD on normal routes allowed; POST, WS upgrade, forbidden
	// paths, non-loopback peer/host, LAN access, expiry, and revocation all denied.
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	f.d.screenshotAuth.now = func() time.Time { return now }
	tok, revoke, err := f.d.screenshotAuth.mint(30 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	makeReq := func(method, target, remote, host string, ws bool) *http.Request {
		r := httptest.NewRequest(method, "http://"+host+target, nil)
		r.RemoteAddr = remote
		r.Host = host
		r.AddCookie(&http.Cookie{Name: daemonScreenshotCookieName, Value: tok})
		if ws {
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
		}
		return r
	}
	localhost := "127.0.0.1:49015"
	if !f.d.screenshotRenderAuthenticated(makeReq(http.MethodGet, "/s/abc", "127.0.0.1:1234", localhost, false)) {
		t.Fatal("valid loopback GET should authenticate with screenshot cookie")
	}
	if !f.d.screenshotRenderAuthenticated(makeReq(http.MethodHead, "/", "127.0.0.1:1234", localhost, false)) {
		t.Fatal("valid loopback HEAD should authenticate with screenshot cookie")
	}
	if f.d.screenshotRenderAuthenticated(makeReq(http.MethodPost, "/api/daemon/bridge-reply", "127.0.0.1:1234", localhost, false)) {
		t.Fatal("POST must never authenticate with screenshot cookie")
	}
	if f.d.screenshotRenderAuthenticated(makeReq(http.MethodGet, "/rail/events", "127.0.0.1:1234", localhost, true)) {
		t.Fatal("WebSocket upgrade must never authenticate with screenshot cookie")
	}
	for _, forbidden := range []string{"/settings", "/auth/status", "/auth/cookie", "/shutdown", "/api/daemon/bridge-reply", "/mcp", "/meta/agent-record"} {
		if f.d.screenshotRenderAuthenticated(makeReq(http.MethodGet, forbidden, "127.0.0.1:1234", localhost, false)) {
			t.Fatalf("forbidden path %q unexpectedly authenticated with screenshot cookie", forbidden)
		}
	}
	if f.d.screenshotRenderAuthenticated(makeReq(http.MethodGet, "/", "192.168.1.50:1234", localhost, false)) {
		t.Fatal("non-loopback peer must never authenticate with screenshot cookie")
	}
	if f.d.screenshotRenderAuthenticated(makeReq(http.MethodGet, "/", "127.0.0.1:1234", "evil.example:49015", false)) {
		t.Fatal("non-loopback Host must never authenticate with screenshot cookie")
	}
	// Even if LAN mode is on, a screenshot-only request must never see LAN tokens.
	f.d.lan = true
	if acc := f.d.lanAccessFor(makeReq(http.MethodGet, "/", "127.0.0.1:1234", localhost, false)); acc != nil {
		t.Fatalf("lanAccessFor returned %+v for screenshot-only request", acc)
	}
	f.d.lan = false

	// Expiry invalidates the token automatically.
	now = now.Add(31 * time.Second)
	if f.d.screenshotRenderAuthenticated(makeReq(http.MethodGet, "/", "127.0.0.1:1234", localhost, false)) {
		t.Fatal("expired screenshot cookie must be rejected")
	}
	revoke()
	f.d.screenshotAuth.now = nil

	// Now exercise the real Tier B Node runner with a fake Playwright module
	// that probes live HTTP isolation, records the minted cookie, and writes a real PNG.
	modDir := t.TempDir()
	probeOut := filepath.Join(modDir, "probe.json")
	samplePNG := makeTestPNG(t, 37, 21)
	pngHexPath := filepath.Join(modDir, "sample.png")
	if err := os.WriteFile(pngHexPath, samplePNG, 0o600); err != nil {
		t.Fatal(err)
	}
	fakeModPath := filepath.Join(modDir, "fake-playwright.cjs")
	fakeMod := `
const fs = require('node:fs');
const samplePNG = fs.readFileSync(` + jsonStringLiteral(pngHexPath) + `);
const probeOut = ` + jsonStringLiteral(probeOut) + `;
module.exports = {
  chromium: {
    async launch() {
      let cookies = [];
      let routeHandler = null;
      return {
        async newContext(opts) {
          return {
            async addCookies(list) { cookies = list; },
            async route(_pat, fn) { routeHandler = fn; },
            async newPage() {
              return {
                async goto(targetUrl) {
                  const c = cookies[0];
                  const headers = { Cookie: c.name + '=' + c.value };
                  const okRes = await fetch(targetUrl, { headers });
                  const settingsRes = await fetch(new URL('/settings', targetUrl), { headers });
                  const postRes = await fetch(new URL('/api/daemon/bridge-reply', targetUrl), {
                    method: 'POST',
                    headers: { ...headers, 'Content-Type': 'application/json', Origin: new URL(targetUrl).origin },
                    body: '{}'
                  });
                  let crossOriginAborted = false;
                  if (routeHandler) {
                    await routeHandler({
                      request: () => ({ url: () => 'https://example.invalid/leak' }),
                      abort: () => { crossOriginAborted = true; },
                      continue: () => {}
                    });
                  }
                  fs.writeFileSync(probeOut, JSON.stringify({
                    argvHasCookie: process.argv.join(' ').includes(c.value),
                    envHasCookie: JSON.stringify(process.env).includes(c.value),
                    serviceWorkers: opts.serviceWorkers,
                    cookieName: c.name,
                    cookieValue: c.value,
                    cookieHttpOnly: c.httpOnly,
                    cookieSameSite: c.sameSite,
                    okStatus: okRes.status,
                    settingsStatus: settingsRes.status,
                    postStatus: postRes.status,
                    crossOriginAborted
                  }));
                },
                async waitForLoadState() {},
                locator() { return { count: async () => 0 }; },
                async screenshot({ path: outPath }) {
                  fs.writeFileSync(outPath, samplePNG);
                }
              };
            },
            async close() {}
          };
        },
        async close() {}
      };
    }
  }
};
`
	if err := os.WriteFile(fakeModPath, []byte(fakeMod), 0o600); err != nil {
		t.Fatal(err)
	}
	f.d.SetScreenshotPlaywrightModule(fakeModPath)
	defer f.d.SetScreenshotPlaywrightModule("")

	const sessionID = "sesstierb"
	resp, err := f.d.DaemonScreenshot(context.Background(), DaemonScreenshotRequest{
		TabID:     "tab-browser",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("DaemonScreenshot Tier B error: %v", err)
	}
	if resp.Mode != "route_render" || resp.Live != false || resp.TabID != "tab-browser" || resp.Route != "/" || resp.Width != 37 || resp.Height != 21 || resp.Bytes != len(samplePNG) {
		t.Fatalf("unexpected Tier B response: %+v", resp)
	}
	wantDir := filepath.Join(SessionDir(f.d.dir, sessionID), "screenshots")
	if filepath.Dir(resp.Path) != wantDir {
		t.Fatalf("Tier B PNG path %q not under %q", resp.Path, wantDir)
	}
	// Serialized JSON must explicitly include "live":false.
	rawJSON, _ := json.Marshal(resp)
	if !strings.Contains(string(rawJSON), `"live":false`) {
		t.Fatalf("Tier B JSON missing explicit live:false: %s", rawJSON)
	}

	var probe struct {
		ArgvHasCookie      bool   `json:"argvHasCookie"`
		EnvHasCookie       bool   `json:"envHasCookie"`
		ServiceWorkers     string `json:"serviceWorkers"`
		CookieName         string `json:"cookieName"`
		CookieValue        string `json:"cookieValue"`
		CookieHttpOnly     bool   `json:"cookieHttpOnly"`
		CookieSameSite     string `json:"cookieSameSite"`
		OKStatus           int    `json:"okStatus"`
		SettingsStatus     int    `json:"settingsStatus"`
		PostStatus         int    `json:"postStatus"`
		CrossOriginAborted bool   `json:"crossOriginAborted"`
	}
	probeRaw, err := os.ReadFile(probeOut)
	if err != nil {
		t.Fatalf("read probe output: %v", err)
	}
	if err := json.Unmarshal(probeRaw, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.ArgvHasCookie || probe.EnvHasCookie {
		t.Fatalf("short-lived cookie leaked to argv/env: %+v", probe)
	}
	if probe.ServiceWorkers != "block" || !probe.CookieHttpOnly || probe.CookieSameSite != "Strict" || !probe.CrossOriginAborted {
		t.Fatalf("browser context isolation weak: %+v", probe)
	}
	if probe.OKStatus != http.StatusOK {
		t.Fatalf("GET / with screenshot cookie returned %d, want 200", probe.OKStatus)
	}
	if probe.SettingsStatus == http.StatusOK || probe.PostStatus == http.StatusOK {
		t.Fatalf("restricted routes unexpectedly succeeded with screenshot cookie: settings=%d post=%d", probe.SettingsStatus, probe.PostStatus)
	}
	// After DaemonScreenshot returns, the short-lived cookie MUST be revoked immediately.
	postRevokeReq, _ := http.NewRequest(http.MethodGet, f.base+"/", nil)
	postRevokeReq.AddCookie(&http.Cookie{Name: probe.CookieName, Value: probe.CookieValue})
	postRevokeRes, err := (&http.Client{}).Do(postRevokeReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = postRevokeRes.Body.Close()
	if postRevokeRes.StatusCode == http.StatusOK {
		t.Fatal("screenshot cookie remained valid after DaemonScreenshot returned")
	}
}

func TestDaemonScreenshotTierCFallbackAndDegradationAndErrors(t *testing.T) {
	t.Setenv(DaemonScreenshotPlaywrightModuleEnv, "")
	f := startPageFixture(t)

	// 1. No active tab -> no_active_tab.
	if _, err := f.d.DaemonScreenshot(context.Background(), DaemonScreenshotRequest{}); err == nil {
		t.Fatal("expected no_active_tab error")
	} else if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "no_active_tab" {
		t.Fatalf("err = %v, want no_active_tab", err)
	}

	// 2. Standard browser tab with Tier B unconfigured -> honest Tier C dom_fallback.
	s := f.d.daemonBridge.newStream("tab-c", true, true)
	defer s.close()
	s.handleFrame(`{"event":"tab-heartbeat","tab_id":"tab-c","visible":true,"focused":true,"last_focused_at":1000,"active_route":"/s/demo","capabilities":{"confirm_banner":true,"native_capture":false}}`)

	answerDOM := func(tabStream *daemonBridgeStream) {
		go func() {
			push := <-tabStream.pushCh
			var reqMsg struct {
				RequestID string `json:"request_id"`
				Nonce     string `json:"nonce"`
				TabID     string `json:"tab_id"`
				Op        string `json:"op"`
			}
			_ = json.Unmarshal(push.Data, &reqMsg)
			if reqMsg.Op != "dom.snapshot" {
				return
			}
			body, _ := json.Marshal(map[string]any{
				"request_id": reqMsg.RequestID,
				"nonce":      reqMsg.Nonce,
				"tab_id":     reqMsg.TabID,
				"payload": map[string]any{
					"tab_id":     reqMsg.TabID,
					"generation": 1,
					"route":      "/s/demo?token=secret123",
					"title":      "Demo",
					"nodes": []map[string]any{
						{"scope": "shell", "tag": "button", "role": "button", "ref": "g1-r1", "name": "Run"},
					},
					"node_count": 1,
				},
			})
			req, _ := http.NewRequest(http.MethodPost, f.base+DaemonBridgeReplyPath, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", f.base)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.AddCookie(localhostTestCookie(t, f.d))
			res, err := (&http.Client{}).Do(req)
			if err == nil {
				_ = res.Body.Close()
			}
		}()
	}

	answerDOM(s)
	respC, err := f.d.DaemonScreenshot(context.Background(), DaemonScreenshotRequest{TabID: "tab-c"})
	if err != nil {
		t.Fatalf("Tier C failed: %v", err)
	}
	if respC.Mode != "dom_fallback" || respC.Live != false || respC.Reason != "screenshot_unavailable" || respC.DOM == nil || len(respC.DOM.Nodes) != 1 {
		t.Fatalf("unexpected Tier C response: %+v", respC)
	}
	rawC, _ := json.Marshal(respC)
	if !strings.Contains(string(rawC), `"live":false`) {
		t.Fatalf("Tier C JSON missing explicit live:false: %s", rawC)
	}
	if strings.Contains(string(rawC), "secret123") {
		t.Fatalf("Tier C DOM leaked query secret: %s", rawC)
	}

	// 3. Broken Tier B module degrades cleanly to Tier C (not an error).
	f.d.SetScreenshotPlaywrightModule(filepath.Join(t.TempDir(), "does-not-exist.cjs"))
	defer f.d.SetScreenshotPlaywrightModule("")
	answerDOM(s)
	respDegraded, err := f.d.DaemonScreenshot(context.Background(), DaemonScreenshotRequest{TabID: "tab-c"})
	if err != nil {
		t.Fatalf("Tier B degradation returned error instead of Tier C: %v", err)
	}
	if respDegraded.Mode != "dom_fallback" || respDegraded.Reason != "screenshot_unavailable" || respDegraded.DOM == nil {
		t.Fatalf("unexpected degraded response: %+v", respDegraded)
	}

	// 4. Forbidden route (/settings) refuses Tier B and degrades cleanly to Tier C.
	s.handleFrame(`{"event":"tab-heartbeat","tab_id":"tab-c","visible":true,"focused":true,"last_focused_at":1001,"active_route":"/settings","capabilities":{"confirm_banner":true,"native_capture":false}}`)
	answerDOM(s)
	respForbiddenRoute, err := f.d.DaemonScreenshot(context.Background(), DaemonScreenshotRequest{TabID: "tab-c"})
	if err != nil {
		t.Fatalf("forbidden route degradation failed: %v", err)
	}
	if respForbiddenRoute.Mode != "dom_fallback" {
		t.Fatalf("expected dom_fallback for forbidden route, got %+v", respForbiddenRoute)
	}

	// 5. Native tab disconnecting mid-capture yields tab_gone.
	sGone := f.d.daemonBridge.newStream("tab-gone", true, true)
	sGone.handleFrame(`{"event":"tab-heartbeat","tab_id":"tab-gone","visible":true,"focused":true,"last_focused_at":2000,"active_route":"/","capabilities":{"confirm_banner":true,"native_capture":true}}`)
	go func() {
		<-sGone.pushCh
		sGone.close()
	}()
	if _, err := f.d.DaemonScreenshot(context.Background(), DaemonScreenshotRequest{TabID: "tab-gone"}); err == nil {
		t.Fatal("expected tab_gone error")
	} else if dce, ok := IsDaemonControlError(err); !ok || dce.Code != "tab_gone" {
		t.Fatalf("err = %v, want tab_gone", err)
	}
}

func jsonStringLiteral(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
