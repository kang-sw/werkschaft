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
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wslive"
)

// TestDaemonControlBrowser verifies the Phase 1 browser/DOM contract in real
// headless Chromium against an isolated daemon fixture:
//   - Multi-tab local shell heartbeat registration over /rail/events WS.
//   - 1-Click confirmation banner renders ONLY in the focused local tab.
//   - Banner never collides with or overlaps [data-openai-consent] / :root[data-account-banner].
//   - Anti-self-approval: synthetic untrusted click is ignored; real trusted
//     Approve / Deny / Dismiss clicks settle real DaemonCurl calls via
//     POST /api/daemon/bridge-reply with TrustedWrite localhost cookie + nonce.
//   - Per-session opt-in toggle in both the new-session modal (default OFF,
//     never saved to localStorage) and the existing-session popover (posts
//     /agents/<id>/settings/dashboard-control, renders inline restart badge +
//     "Stop now" whenever running && persisted != effective).
func TestDaemonControlBrowser(t *testing.T) {
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
	// Keep Tier B unconfigured initially so Tier C dom_fallback is verified first;
	// Part 5 enables ScreenshotPlaywrightModule explicitly via /ctrl/screenshot-module.
	t.Setenv(DaemonScreenshotPlaywrightModuleEnv, "")
	SetDevAssets(nil)

	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
		o.Agents = &guideAgents{}
		o.Worktrees = permUIWorktrees{}
		o.credentialSource = &consentStreamStore{wscred.NewStore(filepath.Join(t.TempDir(), wscred.FileName))}
	})

	repo := filepath.Join(t.TempDir(), "devenv")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	register(t, f.dir, "reposession", repo, time.Now())
	// Mark livesession as hosted so /s/livesession naturally renders data-hosted="1"
	// over loopback without Playwright document route interception.
	liveReg, ok := f.d.Index().Session("livesession")
	if !ok {
		t.Fatal("livesession missing from fixture")
	}
	liveReg.Hosted = true
	if err := WriteRegistration(f.dir, liveReg.Registration); err != nil {
		t.Fatal(err)
	}
	f.d.Index().Refresh()
	waitFor(t, "the repository and hosted sessions to be indexed", func() bool {
		_, okRepo := f.d.Index().Session("reposession")
		s, okLive := f.d.Index().Session("livesession")
		return okRepo && okLive && s.Hosted
	})
	now := time.Now().UnixMilli()
	f.d.snapshots.SetLive(f.dir, "livesession", wslive.Snapshot{
		SchemaVersion: 1,
		Adapter:       "claude",
		UpdatedAt:     now,
		HeartbeatMs:   30000,
		Lead: wslive.Lead{
			Activity: wslive.Activity{Phase: wslive.PhaseIdle},
		},
	})
	f.d.broker.wake(liveDep("livesession"))

	// Test-only control server bridging the Playwright script to the real
	// in-process Daemon.DaemonTabs, DaemonCurl, DaemonDOM, and DaemonUI methods.
	writeCtrlResult := func(w http.ResponseWriter, resp any, err error) {
		if err != nil {
			var dcErr *DaemonControlError
			if errors.As(err, &dcErr) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok":      false,
					"error":   dcErr.Code,
					"reason":  dcErr.Reason,
					"message": dcErr.Message,
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":      false,
				"error":   "internal",
				"message": err.Error(),
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"response": resp,
		})
	}
	ctrl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ctrl/tabs":
			_ = json.NewEncoder(w).Encode(map[string]any{"tabs": f.d.DaemonTabs()})
		case "/ctrl/curl":
			var req DaemonCurlRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			resp, err := f.d.DaemonCurl(ctx, req)
			writeCtrlResult(w, resp, err)
		case "/ctrl/dom":
			var req DaemonDOMRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			resp, err := f.d.DaemonDOM(ctx, req)
			writeCtrlResult(w, resp, err)
		case "/ctrl/ui":
			var req DaemonUIRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			resp, err := f.d.DaemonUI(ctx, req)
			writeCtrlResult(w, resp, err)
		case "/ctrl/screenshot":
			var body struct {
				TabID     string `json:"tab_id"`
				SessionID string `json:"session_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			resp, err := f.d.DaemonScreenshot(ctx, DaemonScreenshotRequest{
				TabID:     body.TabID,
				SessionID: body.SessionID,
			})
			writeCtrlResult(w, resp, err)
		case "/ctrl/screenshot-module":
			var body struct {
				Module string `json:"module"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			f.d.SetScreenshotPlaywrightModule(body.Module)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/ctrl/confirm":
			var req DaemonConfirmRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			res := f.d.RequestDaemonConfirmation(ctx, req)
			_ = json.NewEncoder(w).Encode(res)
		case "/ctrl/phone-snapshot":
			_ = json.NewEncoder(w).Encode(f.d.CompanionPhone().Snapshot())
		default:
			http.NotFound(w, r)
		}
	}))
	defer ctrl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/daemon-control.browser.cjs", f.base, ctrl.URL)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("daemon control browser test failed: %v", err)
	}
}
