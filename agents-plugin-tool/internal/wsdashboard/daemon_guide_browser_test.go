package wsdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestDaemonGuideBrowser verifies the Phase 2 interactive-guide overlay in
// real headless Chromium against an isolated daemon fixture: shell and
// session-frame spotlights with projected rects that follow scroll and
// resize, × / ESC dismissal, excluded and missing targets, the slot cap, the
// companion gaze event, structural monitor hooks reaching a fake delivery
// sink coalesced, the stop chip, and idle frame-freedom.
func TestDaemonGuideBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		if realHome, err := os.UserHomeDir(); err == nil && realHome != "" {
			for _, candidate := range []string{
				filepath.Join(realHome, "Library", "Caches", "ms-playwright"),
				filepath.Join(realHome, ".cache", "ms-playwright"),
			} {
				if info, err := os.Stat(candidate); err == nil && info.IsDir() {
					t.Setenv("PLAYWRIGHT_BROWSERS_PATH", candidate)
					break
				}
			}
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	SetDevAssets(nil)

	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
	})

	// Fake delivery sink: the real registry with real timers and the real
	// tab push, but deliveries recorded instead of mailed.
	var mu sync.Mutex
	var deliveries []map[string]string
	b := &f.d.daemonBridge
	b.mu.Lock()
	b.guide = newGuideMonitorRegistry(guideMonitorDeps{
		clock: realGuideClock{},
		deliver: func(owner, body string) error {
			mu.Lock()
			deliveries = append(deliveries, map[string]string{"owner": owner, "body": body})
			mu.Unlock()
			return nil
		},
		live: func(string) bool { return true },
		push: f.d.broadcastGuidePush,
	})
	b.mu.Unlock()
	defer f.d.closeGuideMonitors()

	writeCtrlResult := func(w http.ResponseWriter, resp any, err error) {
		if err != nil {
			var dcErr *DaemonControlError
			if errors.As(err, &dcErr) {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": dcErr.Code, "reason": dcErr.Reason, "message": dcErr.Message})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "internal", "message": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "response": resp})
	}
	ctrl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		switch r.URL.Path {
		case "/ctrl/tabs":
			_ = json.NewEncoder(w).Encode(map[string]any{"tabs": f.d.DaemonTabs()})
		case "/ctrl/dom":
			var req DaemonDOMRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			resp, err := f.d.DaemonDOM(ctx, req)
			writeCtrlResult(w, resp, err)
		case "/ctrl/highlight":
			var req DaemonHighlightRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			resp, err := f.d.DaemonHighlight(ctx, req)
			writeCtrlResult(w, resp, err)
		case "/ctrl/monitor":
			var req DaemonMonitorRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			req.Owner = "guide-owner"
			resp, err := f.d.DaemonMonitor(ctx, req)
			writeCtrlResult(w, resp, err)
		case "/ctrl/deliveries":
			mu.Lock()
			out := append([]map[string]string(nil), deliveries...)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"deliveries": out})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ctrl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/daemon-guide.browser.cjs", f.base, ctrl.URL)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("daemon guide browser test failed: %v", err)
	}
}
