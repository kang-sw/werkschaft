package wsdashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestConvRevealClientContracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/conv-reveal.test.cjs")
}

type convUIAgentCall struct {
	Method string         `json:"method"`
	Path   string         `json:"path"`
	Body   map[string]any `json:"body,omitempty"`
}

type convUIFakeAgents struct {
	mu            sync.Mutex
	running       bool
	convAccess    bool
	convEffective bool
	calls         []convUIAgentCall
}

func (a *convUIFakeAgents) Start(func(string, string)) {}
func (a *convUIFakeAgents) InboxChanged(string)        {}
func (a *convUIFakeAgents) Close()                     {}

func (a *convUIFakeAgents) stateJSON() map[string]any {
	return map[string]any{
		"id":                            "leadsess",
		"lead":                          true,
		"is_child":                      false,
		"backend":                       "claude",
		"model":                         "claude-opus-4-7",
		"effort":                        "high",
		"permission_mode":               "auto",
		"active_mode":                   "auto",
		"permission_level":              "workdir-write",
		"permission_source":             "explicit",
		"permission_enforcement":        "partial",
		"can_control":                   true,
		"status":                        "idle",
		"process":                       a.running,
		"conv_history_access":           a.convAccess,
		"conv_history_access_effective": a.convEffective,
	}
}

func (a *convUIFakeAgents) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/agents/leadsess":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(a.stateJSON())
		case r.Method == http.MethodPost && r.URL.Path == "/agents/leadsess/settings":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			a.calls = append(a.calls, convUIAgentCall{Method: r.Method, Path: r.URL.Path, Body: body})
			if v, ok := body["conv_history_access"].(bool); ok {
				a.convAccess = v
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(a.stateJSON())
		case r.Method == http.MethodPost && r.URL.Path == "/agents/leadsess/stop":
			a.calls = append(a.calls, convUIAgentCall{Method: r.Method, Path: r.URL.Path})
			a.running = false
			a.convEffective = a.convAccess
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(a.stateJSON())
		default:
			http.NotFound(w, r)
		}
	})
}

func (a *convUIFakeAgents) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running = true
	a.convAccess = false
	a.convEffective = false
	a.calls = nil
}

func (a *convUIFakeAgents) snapshotCalls() []convUIAgentCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]convUIAgentCall, len(a.calls))
	copy(out, a.calls)
	return out
}

// TestConvHistoryUIBrowser verifies Phase 2 dashboard UI behaviors end-to-end
// in headless Chromium across dark & light themes:
//   - Existing-session popover conv_history_access toggle (lead-only, plain
//     /settings without stopping process), inline restart-needed banner, and
//     inline Stop now button (/stop clears banner).
//   - Live conv-reveal WS event: rail row pulse + Jump badge (no auto-nav when
//     focus=false), Jump badge click -> shell navigation to /s/<rid>#entry=<key>,
//     multi-page /s/<rid>/history pagination + flash highlight, not-found notice
//     beyond the 5-page (250-entry) bound with scroll preserved, and focus=true
//     auto-navigation.
//
// Set WS_CONV_HISTORY_SHOTS=<abs dir> to retain dark/light screenshots.
func TestConvHistoryUIBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_CONV_HISTORY_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_CONV_HISTORY_SHOTS must be absolute")
	}
	if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		if h, err := os.UserHomeDir(); err == nil {
			t.Setenv("PLAYWRIGHT_BROWSERS_PATH", filepath.Join(h, "Library", "Caches", "ms-playwright"))
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	SetDevAssets(nil)

	agents := &convUIFakeAgents{running: true}
	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
		o.Agents = agents
		o.Worktrees = permUIWorktrees{}
		o.Support = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/support/choices" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"harnesses":[{"harness":"claude","lead":true,"available":true,"models":[{"id":"claude-opus-4-7"}],"efforts":["low","high"],"permission_modes":["auto","plan","acceptEdits","bypassPermissions"],"permission_levels":["read-only","workdir-write","full"]}]}`))
		})
	})

	repo := filepath.Join(t.TempDir(), "devenv")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	writeTestReg(t, f.dir, Registration{
		ReplyID:      "leadsess",
		Project:      repo,
		Harness:      "claude",
		Hosted:       true,
		HostPID:      12345,
		RegisteredAt: now.UTC().Format(time.RFC3339),
		HeartbeatAt:  now.UTC().Format(time.RFC3339),
	})
	register(t, f.dir, "othersess", repo, now.Add(-time.Minute))
	f.src.setPage("leadsess", PageContent{HasBoard: true, Seq: 1, Board: boardRegions(t, "lead board")})
	f.src.setPage("othersess", PageContent{HasBoard: true, Seq: 1, Board: boardRegions(t, "other board")})

	// Write a live snapshot so leadsess opens in live conversation mode.
	writeSnapshot(t, f.dir, "leadsess", &wslive.Snapshot{
		SchemaVersion: 1,
		ReplyID:       "leadsess",
		Adapter:       "claude",
		UpdatedAt:     now.UnixMilli(),
		HeartbeatMs:   30000,
		Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}},
	})

	// Seed 310 ring entries on leadsess:
	//   - newest 50 (indices 260..309) render initially in live DOM
	//   - index 180 requires 2 pages of /s/leadsess/history?n=50&before=...
	//   - index 2 is older than 5 pages (250 entries) -> triggers not-found notice
	ringDir := wslive.RingDir(SessionDir(f.dir, "leadsess"))
	entries := make([]wslive.RingEntry, 0, 310)
	for i := 0; i < 310; i++ {
		kind := wslive.RingUser
		if i%2 == 1 {
			kind = wslive.RingReply
		}
		entries = append(entries, wslive.RingEntry{
			Kind: kind,
			Text: fmt.Sprintf("Conversation turn #%03d sentinel payload", i),
		})
	}
	if err := wslive.AppendRing(ringDir, entries); err != nil {
		t.Fatal(err)
	}
	recs, err := wslive.ReadRingRecords(ringDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) < 310 {
		t.Fatalf("expected 310 ring records, got %d", len(recs))
	}
	pagedEntry := recs[180].Name
	tooOldEntry := recs[2].Name
	liveEntry := recs[len(recs)-5].Name

	f.d.Index().Refresh()
	waitFor(t, "conv-history browser sessions indexed", func() bool {
		_, ok1 := f.d.Index().Session("leadsess")
		_, ok2 := f.d.Index().Session("othersess")
		return ok1 && ok2
	})

	ctrl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/reveal":
			var ev ConvReveal
			if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ok := f.d.PublishConvReveal(ev)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": ok})
		case "/reset":
			agents.reset()
			w.WriteHeader(http.StatusNoContent)
		case "/calls":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(agents.snapshotCalls())
		default:
			http.NotFound(w, r)
		}
	}))
	defer ctrl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/conv-history-ui.browser.cjs", f.base, ctrl.URL, pagedEntry, tooOldEntry, liveEntry, shots)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("conv-history UI in Chromium: %v", err)
	}
}
