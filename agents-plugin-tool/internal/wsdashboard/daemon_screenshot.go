package wsdashboard

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DaemonScreenshotPlaywrightModuleEnv configures an optional runtime
	// Playwright module path for Tier B headless route rendering. It is
	// intentionally distinct from the test-only WS_PLAYWRIGHT_MODULE ((1755)).
	DaemonScreenshotPlaywrightModuleEnv = "WS_DAEMON_SCREENSHOT_PLAYWRIGHT_MODULE"

	daemonScreenshotMaxBytes      = daemonBridgeReplyMaxBytes // 8 MiB
	daemonScreenshotCookieName    = "ws_dashboard_screenshot_render"
	daemonScreenshotTokenTTL      = 30 * time.Second
	daemonScreenshotNativeTimeout = 20 * time.Second
	daemonScreenshotRenderTimeout = 25 * time.Second
	daemonScreenshotMaxActiveToks = 32
)

// DaemonScreenshotRequest is the structured input to ws/daemon.screenshot.
type DaemonScreenshotRequest struct {
	TabID string `json:"tab_id,omitempty"`
	// SessionID is the caller's hosted conversation reply ID (set server-side
	// by MCP so PNGs land under that session's daemon cache directory).
	SessionID string `json:"-"`
}

// DaemonScreenshotResponse is the staged return contract for
// ws/daemon.screenshot ((1755)). Image bytes are never inlined: Tier A and B
// write PNG files under the daemon's per-session cache directory and return
// metadata; Tier C returns an honest dom_fallback with the bounded DOM snapshot.
type DaemonScreenshotResponse struct {
	Mode   string             `json:"mode"`
	Live   bool               `json:"live"`
	TabID  string             `json:"tab_id,omitempty"`
	Route  string             `json:"route,omitempty"`
	Path   string             `json:"path,omitempty"`
	Width  int                `json:"width,omitempty"`
	Height int                `json:"height,omitempty"`
	Bytes  int                `json:"bytes,omitempty"`
	Reason string             `json:"reason,omitempty"`
	DOM    *DaemonDOMSnapshot `json:"dom,omitempty"`
}

// screenshotAuthState holds short-lived, in-memory-only loopback credentials
// minted exclusively for Tier B headless route rendering. Zero disk writes.
type screenshotAuthState struct {
	mu     sync.Mutex
	now    func() time.Time
	tokens map[string]time.Time
	module string
}

func (a *screenshotAuthState) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *screenshotAuthState) mint(ttl time.Duration) (string, func(), error) {
	if ttl <= 0 {
		ttl = daemonScreenshotTokenTTL
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	tok := hex.EncodeToString(buf)

	a.mu.Lock()
	now := a.clock()
	if a.tokens == nil {
		a.tokens = map[string]time.Time{}
	}
	for k, exp := range a.tokens {
		if !now.Before(exp) {
			delete(a.tokens, k)
		}
	}
	if len(a.tokens) >= daemonScreenshotMaxActiveToks {
		a.mu.Unlock()
		return "", nil, fmt.Errorf("too many active screenshot render credentials")
	}
	a.tokens[tok] = now.Add(ttl)
	a.mu.Unlock()

	var once sync.Once
	revoke := func() {
		once.Do(func() {
			a.mu.Lock()
			delete(a.tokens, tok)
			a.mu.Unlock()
		})
	}
	return tok, revoke, nil
}

func (a *screenshotAuthState) valid(got string) bool {
	if got == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.tokens) == 0 {
		return false
	}
	now := a.clock()
	matched := false
	for tok, exp := range a.tokens {
		if !now.Before(exp) {
			delete(a.tokens, tok)
			continue
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(tok)) == 1 {
			matched = true
		}
	}
	return matched
}

// screenshotRenderAuthenticated reports whether r carries a valid short-lived
// loopback-only Tier B screenshot render cookie on a safe read-only HTTP route.
// It strictly forbids non-loopback peers, non-GET/HEAD methods, WebSocket
// upgrades (so headless Chromium can never register as a shell tab), and all
// credential/auth/settings/admin paths.
func (d *Daemon) screenshotRenderAuthenticated(r *http.Request) bool {
	if !localViewer(r) {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if wsUpgrade(r) {
		return false
	}
	cleanPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	if isDaemonCurlForbiddenPath(cleanPath) {
		return false
	}
	c, err := r.Cookie(daemonScreenshotCookieName)
	if err != nil {
		return false
	}
	return d.screenshotAuth.valid(c.Value)
}

func (d *Daemon) isScreenshotRenderOnly(r *http.Request) bool {
	return !d.localhostAuthenticated(r) && d.screenshotRenderAuthenticated(r)
}

// SetScreenshotPlaywrightModule configures the optional runtime Playwright
// module path used by Tier B route rendering (empty falls back to
// WS_DAEMON_SCREENSHOT_PLAYWRIGHT_MODULE).
func (d *Daemon) SetScreenshotPlaywrightModule(modulePath string) {
	d.screenshotAuth.mu.Lock()
	d.screenshotAuth.module = strings.TrimSpace(modulePath)
	d.screenshotPlaywrightModule = d.screenshotAuth.module
	d.screenshotAuth.mu.Unlock()
}

func (d *Daemon) configuredScreenshotPlaywrightModule() string {
	d.screenshotAuth.mu.Lock()
	mod := strings.TrimSpace(d.screenshotAuth.module)
	if mod == "" {
		mod = strings.TrimSpace(d.screenshotPlaywrightModule)
	}
	d.screenshotAuth.mu.Unlock()
	if mod != "" {
		return mod
	}
	return strings.TrimSpace(os.Getenv(DaemonScreenshotPlaywrightModuleEnv))
}

func (d *Daemon) screenshotNativeTimeout() time.Duration {
	if d.daemonScreenshotNativeTimeout > 0 {
		return d.daemonScreenshotNativeTimeout
	}
	return daemonScreenshotNativeTimeout
}

func (d *Daemon) screenshotRenderTimeout() time.Duration {
	if d.daemonScreenshotRenderTimeout > 0 {
		return d.daemonScreenshotRenderTimeout
	}
	return daemonScreenshotRenderTimeout
}

func validateScreenshotRoute(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "/"
	}
	clean, err := validateDaemonCurlPath(raw)
	if err != nil {
		return "", false
	}
	if isDaemonCurlForbiddenPath(clean) {
		return "", false
	}
	return clean, true
}

func (d *Daemon) screenshotCacheDir(sessionID string) string {
	if ValidReplyID(sessionID) {
		return filepath.Join(SessionDir(d.dir, sessionID), "screenshots")
	}
	return filepath.Join(d.dir, "screenshots")
}

func (d *Daemon) allocateScreenshotPath(sessionID string) (string, error) {
	dir := d.screenshotCacheDir(sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	name := fmt.Sprintf("shot-%d-%s.png", time.Now().UTC().UnixMilli(), hex.EncodeToString(suffix))
	return filepath.Join(dir, name), nil
}

func validatePNGBytes(data []byte) (width, height int, err error) {
	if len(data) == 0 {
		return 0, 0, &DaemonControlError{Code: "screenshot_unavailable", Message: "empty screenshot payload"}
	}
	if len(data) > daemonScreenshotMaxBytes {
		return 0, 0, &DaemonControlError{
			Code:    "payload_too_large",
			Message: fmt.Sprintf("screenshot exceeds %d bytes", daemonScreenshotMaxBytes),
		}
	}
	cfg, decErr := png.DecodeConfig(bytes.NewReader(data))
	if decErr != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, &DaemonControlError{Code: "screenshot_unavailable", Message: "invalid PNG screenshot payload"}
	}
	return cfg.Width, cfg.Height, nil
}

func (d *Daemon) writeDaemonScreenshotPNG(sessionID string, data []byte) (string, int, int, error) {
	width, height, err := validatePNGBytes(data)
	if err != nil {
		return "", 0, 0, err
	}
	outPath, err := d.allocateScreenshotPath(sessionID)
	if err != nil {
		return "", 0, 0, err
	}
	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, 0, err
	}
	_, wErr := f.Write(data)
	cErr := f.Close()
	if wErr != nil || cErr != nil {
		_ = os.Remove(outPath)
		if wErr != nil {
			return "", 0, 0, wErr
		}
		return "", 0, 0, cErr
	}
	return outPath, width, height, nil
}

// DaemonScreenshot implements the staged ws/daemon.screenshot contract ((1755)):
//   - Tier A (native): when the target tab advertises native_capture, captures
//     the live desktop WebView surface over POST /api/daemon/bridge-reply and
//     writes PNG under the per-session cache directory (mode="native", live=true).
//   - Tier B (route_render): when Tier A is absent and an optional runtime
//     Playwright module is configured, mints a short-lived loopback-only
//     read-only cookie and renders the tab's validated daemon-relative route in
//     an isolated headless context (mode="route_render", live=false). Any clean
//     Tier B failure degrades to Tier C.
//   - Tier C (dom_fallback): never uses html2canvas or SVG foreignObject;
//     returns mode="dom_fallback", live=false, reason="screenshot_unavailable"
//     with a bounded ws/daemon.dom snapshot.
func (d *Daemon) DaemonScreenshot(ctx context.Context, req DaemonScreenshotRequest) (DaemonScreenshotResponse, error) {
	b := &d.daemonBridge
	b.mu.Lock()
	tab, err := b.selectTabLocked(strings.TrimSpace(req.TabID), false)
	if err != nil {
		b.mu.Unlock()
		return DaemonScreenshotResponse{}, err
	}
	tabID := tab.tabID
	hasNative := tab.capabilities.NativeCapture
	activeRoute := tab.activeRoute
	b.mu.Unlock()

	// Tier A: Native Tauri WebView capture via POST /api/daemon/bridge-reply.
	if hasNative {
		out, _, err := d.sendDaemonBridgeRequest(ctx, tabID, "screenshot.capture", nil, d.screenshotNativeTimeout())
		if err != nil {
			if dce, ok := IsDaemonControlError(err); ok {
				switch dce.Code {
				case "payload_too_large", "tab_gone", "no_active_tab":
					return DaemonScreenshotResponse{}, err
				}
			}
			// Transient or unavailable native capture degrades honestly to Tier B/C.
		} else if len(out.Binary) > 0 {
			filePath, width, height, writeErr := d.writeDaemonScreenshotPNG(req.SessionID, out.Binary)
			if writeErr != nil {
				if dce, ok := IsDaemonControlError(writeErr); ok && dce.Code == "payload_too_large" {
					return DaemonScreenshotResponse{}, writeErr
				}
			} else {
				return DaemonScreenshotResponse{
					Mode:   "native",
					Live:   true,
					TabID:  tabID,
					Route:  activeRoute,
					Path:   filePath,
					Width:  width,
					Height: height,
					Bytes:  len(out.Binary),
				}, nil
			}
		}
	}

	// Tier B: Optional headless Playwright route render with isolated
	// short-lived loopback-only credential.
	if modulePath := d.configuredScreenshotPlaywrightModule(); modulePath != "" && !strings.ContainsRune(modulePath, 0) {
		if resp, ok := d.tryPlaywrightRouteRender(ctx, req.SessionID, tabID, activeRoute, modulePath); ok {
			return resp, nil
		}
	}

	// Tier C: Honest dom_fallback (never html2canvas or SVG foreignObject).
	snap, err := d.DaemonDOM(ctx, DaemonDOMRequest{TabID: tabID})
	if err != nil {
		return DaemonScreenshotResponse{}, err
	}
	return DaemonScreenshotResponse{
		Mode:   "dom_fallback",
		Live:   false,
		TabID:  snap.TabID,
		Route:  snap.Route,
		Reason: "screenshot_unavailable",
		DOM:    &snap,
	}, nil
}

type tierBRenderConfig struct {
	ModulePath  string `json:"modulePath"`
	Origin      string `json:"origin"`
	Route       string `json:"route"`
	CookieName  string `json:"cookieName"`
	CookieValue string `json:"cookieValue"`
	OutputPath  string `json:"outputPath"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

// tierBRenderScript executes inside Node using a caller-configured Playwright
// module. All config (including the short-lived loopback-only cookie) is read
// from stdin so credentials never appear in argv or environment variables.
const tierBRenderScript = `
const fs = require('node:fs');
(async () => {
  const cfg = JSON.parse(fs.readFileSync(0, 'utf8'));
  const pw = require(cfg.modulePath);
  const chromium = pw && pw.chromium;
  if (!chromium || typeof chromium.launch !== 'function') {
    throw new Error('invalid playwright module');
  }
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({
      viewport: { width: cfg.width || 1440, height: cfg.height || 900 },
      locale: 'en-US',
      serviceWorkers: 'block'
    });
    try {
      await context.addCookies([{
        name: cfg.cookieName,
        value: cfg.cookieValue,
        url: cfg.origin,
        httpOnly: true,
        sameSite: 'Strict'
      }]);
      await context.route('**/*', route => {
        try {
          const u = new URL(route.request().url());
          if (u.origin !== cfg.origin) return route.abort();
        } catch (_) {
          return route.abort();
        }
        return route.continue();
      });
      const page = await context.newPage();
      await page.goto(cfg.origin + cfg.route, { waitUntil: 'domcontentloaded', timeout: 15000 });
      await page.waitForLoadState('load', { timeout: 5000 }).catch(() => {});
      const hasFrame = await page.locator('.sframe.on').count().catch(() => 0);
      if (hasFrame > 0) {
        await page.frameLocator('.sframe.on').locator('#grid').waitFor({ timeout: 2500 }).catch(() => {});
      }
      await page.screenshot({ path: cfg.outputPath, type: 'png' });
    } finally {
      await context.close();
    }
  } finally {
    await browser.close();
  }
})().catch(err => {
  process.stderr.write(String(err && err.message || err) + '\n');
  process.exit(1);
});
`

func (d *Daemon) tryPlaywrightRouteRender(ctx context.Context, sessionID, tabID, activeRoute, modulePath string) (DaemonScreenshotResponse, bool) {
	cleanRoute, ok := validateScreenshotRoute(activeRoute)
	if !ok {
		return DaemonScreenshotResponse{}, false
	}
	if d.endpoint.Port <= 0 {
		return DaemonScreenshotResponse{}, false
	}
	token, revoke, err := d.screenshotAuth.mint(daemonScreenshotTokenTTL)
	if err != nil {
		return DaemonScreenshotResponse{}, false
	}
	defer revoke()

	outPath, err := d.allocateScreenshotPath(sessionID)
	if err != nil {
		return DaemonScreenshotResponse{}, false
	}
	removeOnFail := true
	defer func() {
		if removeOnFail {
			_ = os.Remove(outPath)
		}
	}()

	origin := "http://127.0.0.1:" + strconv.Itoa(d.endpoint.Port)
	cfgBytes, err := json.Marshal(tierBRenderConfig{
		ModulePath:  modulePath,
		Origin:      origin,
		Route:       cleanRoute,
		CookieName:  daemonScreenshotCookieName,
		CookieValue: token,
		OutputPath:  outPath,
		Width:       1440,
		Height:      900,
	})
	if err != nil {
		return DaemonScreenshotResponse{}, false
	}

	renderCtx, cancel := context.WithTimeout(ctx, d.screenshotRenderTimeout())
	defer cancel()

	cmd := exec.CommandContext(renderCtx, "node", "-e", tierBRenderScript)
	cmd.Stdin = bytes.NewReader(cfgBytes)
	cmd.WaitDelay = 3 * time.Second
	if _, err := cmd.CombinedOutput(); err != nil {
		return DaemonScreenshotResponse{}, false
	}

	f, err := os.Open(outPath)
	if err != nil {
		return DaemonScreenshotResponse{}, false
	}
	data, readErr := io.ReadAll(io.LimitReader(f, daemonScreenshotMaxBytes+1))
	_ = f.Close()
	if readErr != nil {
		return DaemonScreenshotResponse{}, false
	}
	width, height, valErr := validatePNGBytes(data)
	if valErr != nil {
		return DaemonScreenshotResponse{}, false
	}
	// Tighten file permissions on the rendered PNG.
	_ = os.Chmod(outPath, 0o600)
	removeOnFail = false

	return DaemonScreenshotResponse{
		Mode:   "route_render",
		Live:   false,
		TabID:  tabID,
		Route:  cleanRoute,
		Path:   outPath,
		Width:  width,
		Height: height,
		Bytes:  len(data),
	}, true
}
