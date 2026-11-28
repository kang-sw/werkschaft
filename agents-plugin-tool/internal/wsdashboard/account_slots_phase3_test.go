package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
)

func TestPhase3HarnessAuthRoutesAccessControlAndNoStore(t *testing.T) {
	d := authFixture(t)
	d.lan = true
	lanToken, err := d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	d.accountSlots = wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), "auth", wscred.AccountSlotsFileName))
	t.Cleanup(d.accountSlots.Close)
	d.planUsage = newTestPlanUsageStore(t.TempDir(), t.TempDir(), func(string) string { return "" })
	t.Cleanup(func() { d.planUsage.close(context.Background()) })
	t.Cleanup(d.harnessAuth.stop)

	localPeer := authRequest(http.MethodGet, "/", "").RemoteAddr
	lanPeer := net.JoinHostPort(net.IPv4(192, 0, 2, 8).String(), "8888")
	routes := []struct{ method, path string }{
		{http.MethodGet, "harness-auth/status"},
		{http.MethodPost, "harness-auth/start"},
		{http.MethodPost, "harness-auth/complete"},
		{http.MethodPost, "harness-auth/cancel"},
		{http.MethodPost, "harness-auth/reverify"},
	}
	for _, route := range routes {
		t.Run(route.path, func(t *testing.T) {
			path := accountPrefix + route.path
			for _, v := range []viewer{
				{peer: lanPeer, host: "dashboard.example", cookie: lanToken},
				{peer: lanPeer, host: "localhost:43210", cookie: lanToken},
				{peer: localPeer, host: "dashboard.example", cookie: lanToken},
			} {
				if w := serve(d, v, route.method, path, "{}"); w.Code != http.StatusNotFound {
					t.Fatalf("LAN %s = %d, want 404", route.path, w.Code)
				}
			}
			req := authRequest(route.method, path, "{}")
			req.Header.Set("Authorization", "Bearer "+d.endpoint.Token)
			req.Header.Set("Origin", "http://"+req.Host)
			if w := authServe(d, req); w.Code != http.StatusUnauthorized {
				t.Fatalf("endpoint bearer admitted on %s: %d", route.path, w.Code)
			}
			if route.method == http.MethodPost {
				req := authRequest(route.method, path, "{}")
				req.AddCookie(authCookie(t, d))
				req.Header.Set("Origin", "https://evil.example")
				if w := authServe(d, req); w.Code != http.StatusForbidden {
					t.Fatalf("cross-origin admitted on %s: %d", route.path, w.Code)
				}
			}
		})
	}

	wStatus := requestAccount(t, d, http.MethodGet, "harness-auth/status", "")
	if wStatus.Code != http.StatusOK {
		t.Fatalf("harness-auth/status = %d %s", wStatus.Code, wStatus.Body.String())
	}
	if cc := wStatus.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
}

func TestPhase3ClaudeSlotDualSignInPasteCodeAndDeniedBasePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake claude login runs on unix")
	}
	d := authFixture(t)
	var logBuf bytes.Buffer
	d.log = &logBuf
	d.dir = t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	d.accountSlots = wscred.NewAccountSlotRegistry(filepath.Join(d.dir, "auth", wscred.AccountSlotsFileName))
	t.Cleanup(d.accountSlots.Close)
	t.Cleanup(d.slotSignIn.stop)
	// Always stub openSystem in tests: never launch a real browser!
	d.openSystem = func(string) error { return nil }

	receivedCodeFile := filepath.Join(t.TempDir(), "received-code.txt")
	script := writeFakeSlotLoginScript(t, fmt.Sprintf(`
echo "Open this URL in your browser: https://claude.ai/oauth/authorize?code=true&client_id=test-claude&redirect_uri=http%%3A%%2F%%2F127.0.0.1%%3A54123%%2Fcallback&state=st-claude"
IFS= read -r code
printf '%%s' "$code" > %q
if [ -n "$code" ] && [ -n "$CLAUDE_CONFIG_DIR" ]; then
  printf '{"claudeAiOauth":{"accessToken":"tok-claude-slot","refreshToken":"ref-claude-slot","subscriptionType":"max"}}\n' > "$CLAUDE_CONFIG_DIR/.credentials.json"
  exit 0
fi
exit 1
`, receivedCodeFile))

	d.slotLoginCmd = func(harness string, mode wscred.SlotLoginMode) (string, []string, error) {
		if harness != "claude" || mode != wscred.SlotLoginModeBrowser {
			return "", nil, fmt.Errorf("unexpected %s/%s", harness, mode)
		}
		return "/bin/sh", []string{script}, nil
	}

	// Start claude slot sign-in.
	w := requestAccount(t, d, http.MethodPost, "slots/sign-in/start",
		`{"harness":"claude","slot_id":"claude-work","label":"Claude Work","mode":"browser","copy_only":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	var start accountSlotSignInStatus
	if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}

	// Wait for copyable OAuth URL and open stdin pipe.
	var st accountSlotSignInStatus
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		sw := requestAccount(t, d, http.MethodGet, "slots/sign-in/status", "")
		_ = json.Unmarshal(sw.Body.Bytes(), &st)
		if st.AuthorizeURL != "" && st.StdinReady {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !strings.HasPrefix(st.AuthorizeURL, "https://claude.ai/oauth/authorize?") || !st.StdinReady {
		t.Fatalf("claude sign-in status = %+v", st)
	}

	// In-progress claude slot directory must already be protected by deniedBasePaths.
	managedDir := filepath.Join(d.dir, "slots", "claude", "claude-work")
	ds := denySetFor([]string{home})
	if !ds.lexical(filepath.Join(managedDir, ".credentials.json")) {
		t.Fatalf("in-progress claude slot dir %q not protected by deniedBasePaths", managedDir)
	}

	// Paste a raw authorization code; verify it reaches stdin and is never logged or echoed.
	secretCode := "CLAUDE-SECRET-AUTH-CODE-98765#state=st-claude"
	wComplete := requestAccount(t, d, http.MethodPost, "slots/sign-in/complete",
		fmt.Sprintf(`{"attempt":%q,"input":%q}`, start.Attempt, secretCode))
	if wComplete.Code != http.StatusAccepted {
		t.Fatalf("complete = %d %s", wComplete.Code, wComplete.Body.String())
	}
	if strings.Contains(wComplete.Body.String(), secretCode) {
		t.Fatalf("complete response echoed pasted code: %s", wComplete.Body.String())
	}

	for deadline := time.Now().Add(3 * time.Second); ; {
		sw := requestAccount(t, d, http.MethodGet, "slots/sign-in/status", "")
		st = accountSlotSignInStatus{}
		_ = json.Unmarshal(sw.Body.Bytes(), &st)
		if st.State == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("claude slot sign-in did not succeed: %+v", st)
		}
		time.Sleep(15 * time.Millisecond)
	}
	// Completed status must clear AuthorizeURL and never contain the secret code.
	if st.AuthorizeURL != "" {
		t.Fatalf("AuthorizeURL not cleared on completion: %+v", st)
	}
	gotCode, err := os.ReadFile(receivedCodeFile)
	if err != nil || string(gotCode) != secretCode {
		t.Fatalf("fake claude stdin received %q (err=%v), want %q", string(gotCode), err, secretCode)
	}
	if strings.Contains(logBuf.String(), secretCode) {
		t.Fatalf("daemon logs leaked pasted auth code: %s", logBuf.String())
	}
	slot, ok := d.accountSlots.ResolveSlot("claude", "claude-work")
	if !ok || !slot.Managed || !sameSlotPath(slot.ConfigDir, managedDir) {
		t.Fatalf("registered claude slot = %+v (ok=%v)", slot, ok)
	}

	// Custom existing CLAUDE_CONFIG_DIR outside managed slots root is also denied by deniedBasePaths,
	// while the default ~/.claude directory is rejected by slots/add.
	defaultClaude := filepath.Join(home, ".claude")
	if err := os.MkdirAll(defaultClaude, 0o700); err != nil {
		t.Fatal(err)
	}
	wDef := requestAccount(t, d, http.MethodPost, "slots/add",
		fmt.Sprintf(`{"harness":"claude","slot_id":"dup-default","label":"Dup","config_dir":%q}`, defaultClaude))
	if wDef.Code != http.StatusUnprocessableEntity || !strings.Contains(wDef.Body.String(), `"config_dir_is_default"`) {
		t.Fatalf("add default claude home = %d %s", wDef.Code, wDef.Body.String())
	}
	customClaude := filepath.Join(t.TempDir(), "custom-claude-home")
	if err := os.MkdirAll(customClaude, 0o700); err != nil {
		t.Fatal(err)
	}
	wCustom := requestAccount(t, d, http.MethodPost, "slots/add",
		fmt.Sprintf(`{"harness":"claude","slot_id":"custom-c","label":"Custom C","config_dir":%q}`, customClaude))
	if wCustom.Code != http.StatusOK {
		t.Fatalf("add custom claude dir = %d %s", wCustom.Code, wCustom.Body.String())
	}
	if !denySetFor([]string{home}).lexical(filepath.Join(customClaude, ".credentials.json")) {
		t.Fatalf("custom claude slot dir %q not protected by deniedBasePaths", customClaude)
	}
}

func TestPhase3AgyAuthFlowClearsHoldWithoutDaemonRestartAndPreservesBilledLatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake agy helper runs on unix")
	}
	base := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	reset5h := base.Add(3 * time.Hour).Format(time.RFC3339)
	resetWk := base.Add(4 * 24 * time.Hour).Format(time.RFC3339)

	authFlag := filepath.Join(t.TempDir(), "agy-authed")
	receivedCodeFile := filepath.Join(t.TempDir(), "agy-code.txt")
	okJSON := validAgyFixtureJSON(reset5h, resetWk)

	// Fake agy CLI:
	// - When authFlag is absent and stdin is /dev/null (probe mode), emit Authentication required on stderr and exit 1.
	// - When invoked interactively by harness-auth/start (open stdin), emit Authentication required URL on stderr,
	//   read the pasted code from stdin, write authFlag, and exit 0.
	// - Once authFlag exists, emit valid usage JSON on stdout and exit 0.
	scriptBody := fmt.Sprintf(`
if [ -f %q ]; then
  cat <<'EOF'
%s
EOF
  exit 0
fi
printf 'Authentication required. Please visit the URL https://accounts.google.com/o/oauth2/v2/auth?client_id=agy-test&redirect_uri=http%%3A%%2F%%2F127.0.0.1%%3A44112%%2Fcb&response_type=code\n' >&2
if IFS= read -r pasted_code && [ -n "$pasted_code" ]; then
  printf '%%s' "$pasted_code" > %q
  printf 'ok' > %q
  exit 0
fi
exit 1
`, authFlag, okJSON, receivedCodeFile, authFlag)

	s, countFile, _ := fakeAgyEnv(t, scriptBody)
	s.now = func() time.Time { return base }

	d := authFixture(t)
	var logBuf bytes.Buffer
	d.log = &logBuf
	d.planUsage = s
	t.Cleanup(func() { s.close(context.Background()) })
	t.Cleanup(d.harnessAuth.stop)
	// Stub openSystem to simulate a headless/remote browser launch error -> attempt MUST stay waiting!
	var openedURL atomic.Value
	d.openSystem = func(u string) error {
		openedURL.Store(u)
		return errors.New("browser unavailable in test")
	}

	// Step 1: initial probe hits auth-required hold.
	s.refreshAgy()
	s.probes.Wait()
	snap := s.agyAuthState()
	if !snap.AuthRequired || snap.Status != agyStatusSignInRequired || !snap.SingleSlot {
		t.Fatalf("after unauth probe, agyAuthState = %+v", snap)
	}
	if got := fakeAgyCallCount(countFile); got != 1 {
		t.Fatalf("initial probe count = %d, want 1", got)
	}

	// Passive refresh while agyAuthHold is set MUST NOT spawn another probe (D17 preserves passive hold).
	s.refreshAgy()
	s.probes.Wait()
	if got := fakeAgyCallCount(countFile); got != 1 {
		t.Fatalf("passive refresh bypassed agyAuthHold: count=%d", got)
	}

	// Step 2: start explicit settings sign-in for agy.
	wStart := requestAccount(t, d, http.MethodPost, "harness-auth/start", `{"harness":"agy"}`)
	if wStart.Code != http.StatusAccepted {
		t.Fatalf("harness-auth/start = %d %s", wStart.Code, wStart.Body.String())
	}
	var startResp harnessAuthSignInStatus
	if err := json.Unmarshal(wStart.Body.Bytes(), &startResp); err != nil {
		t.Fatal(err)
	}

	// Poll status: should stay "waiting" with AuthorizeURL, StdinReady=true, BrowserError=true (D18).
	var st harnessAuthSignInStatus
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		sw := requestAccount(t, d, http.MethodGet, "harness-auth/status", "")
		var payload struct {
			Providers []harnessAuthSnapshot   `json:"providers"`
			SignIn    harnessAuthSignInStatus `json:"sign_in"`
		}
		_ = json.Unmarshal(sw.Body.Bytes(), &payload)
		st = payload.SignIn
		if st.AuthorizeURL != "" && st.BrowserError && st.StdinReady {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if st.State != "waiting" || !strings.HasPrefix(st.AuthorizeURL, "https://accounts.google.com/o/oauth2/v2/auth?") || !st.BrowserError || !st.StdinReady {
		t.Fatalf("expected waiting with URL + browser_error + stdin_ready, got %+v", st)
	}
	if got, _ := openedURL.Load().(string); !strings.HasPrefix(got, "https://accounts.google.com/") {
		t.Fatalf("openSystem was not called with auth URL: %q", got)
	}

	// Reject non-loopback callback URL (SSRF guard) and control-character code.
	wSSRF := requestAccount(t, d, http.MethodPost, "harness-auth/complete",
		fmt.Sprintf(`{"harness":"agy","attempt":%q,"input":"http://169.254.169.254:44112/cb?code=x"}`, startResp.Attempt))
	if wSSRF.Code != http.StatusUnprocessableEntity || !strings.Contains(wSSRF.Body.String(), `"callback_host_not_loopback"`) {
		t.Fatalf("SSRF check = %d %s", wSSRF.Code, wSSRF.Body.String())
	}
	wBadCode := requestAccount(t, d, http.MethodPost, "harness-auth/complete",
		fmt.Sprintf(`{"harness":"agy","attempt":%q,"input":"bad\ncode"}`, startResp.Attempt))
	if wBadCode.Code != http.StatusUnprocessableEntity || !strings.Contains(wBadCode.Body.String(), `"invalid_auth_code"`) {
		t.Fatalf("bad code check = %d %s", wBadCode.Code, wBadCode.Body.String())
	}

	// Step 3: paste valid login code -> writes to stdin without logging/echoing, exits 0,
	// clears agyAuthHold WITHOUT daemon restart, and triggers re-verify probe!
	secretCode := "AGY-REMOTE-CODE-TOPSECRET-4242"
	wComp := requestAccount(t, d, http.MethodPost, "harness-auth/complete",
		fmt.Sprintf(`{"harness":"agy","attempt":%q,"input":%q}`, startResp.Attempt, secretCode))
	if wComp.Code != http.StatusAccepted {
		t.Fatalf("harness-auth/complete = %d %s", wComp.Code, wComp.Body.String())
	}
	if strings.Contains(wComp.Body.String(), secretCode) {
		t.Fatalf("complete response echoed secret code: %s", wComp.Body.String())
	}

	// Wait for sign-in completion and subsequent automatic re-verify probe.
	for deadline := time.Now().Add(4 * time.Second); ; {
		sw := requestAccount(t, d, http.MethodGet, "harness-auth/status", "")
		var payload struct {
			Providers []harnessAuthSnapshot   `json:"providers"`
			SignIn    harnessAuthSignInStatus `json:"sign_in"`
		}
		_ = json.Unmarshal(sw.Body.Bytes(), &payload)
		if payload.SignIn.State == "succeeded" && len(payload.Providers) == 1 && !payload.Providers[0].AuthRequired && !payload.Providers[0].Probing {
			if payload.SignIn.AuthorizeURL != "" {
				t.Fatalf("AuthorizeURL not cleared after completion: %+v", payload.SignIn)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agy auth flow / reverify did not finish: %s", sw.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.probes.Wait()

	// Verify code reached stdin and never appeared in daemon logs.
	gotCode, err := os.ReadFile(receivedCodeFile)
	if err != nil || string(gotCode) != secretCode {
		t.Fatalf("fake agy stdin got %q (err=%v), want %q", string(gotCode), err, secretCode)
	}
	if strings.Contains(logBuf.String(), secretCode) {
		t.Fatalf("daemon logs leaked pasted agy code: %s", logBuf.String())
	}
	// Verify usage groups are now populated without any daemon restart!
	s.mu.Lock()
	groupCount := len(s.agyGroups)
	holdCleared := !s.agyAuthHold
	s.mu.Unlock()
	if !holdCleared || groupCount != 2 {
		t.Fatalf("after reverify without restart: holdCleared=%v groupCount=%d", holdCleared, groupCount)
	}

	// Step 4: D17 — agyBilledLatch is NEVER cleared by explicit reverify.
	s.mu.Lock()
	s.agyBilledLatch = true
	s.agyAuthHold = true
	s.agyStatus = agyStatusSignInRequired
	s.mu.Unlock()
	beforeCount := fakeAgyCallCount(countFile)
	wRev := requestAccount(t, d, http.MethodPost, "harness-auth/reverify", `{"harness":"agy"}`)
	if wRev.Code != http.StatusAccepted {
		t.Fatalf("harness-auth/reverify = %d %s", wRev.Code, wRev.Body.String())
	}
	s.probes.Wait()
	var revSnap harnessAuthSnapshot
	_ = json.Unmarshal(wRev.Body.Bytes(), &revSnap)
	if !revSnap.BilledLatch || revSnap.AuthRequired {
		t.Fatalf("reverify must clear AuthRequired while preserving BilledLatch: %+v", revSnap)
	}
	if got := fakeAgyCallCount(countFile); got != beforeCount {
		t.Fatalf("reverify spawned probe despite agyBilledLatch: before=%d after=%d", beforeCount, got)
	}
}

func TestPhase3AgyProbeSerializationNeverFansOutConcurrently(t *testing.T) {
	s := newTestPlanUsageStore(t.TempDir(), t.TempDir(), func(string) string { return "" })
	t.Cleanup(func() { s.close(context.Background()) })

	var (
		inFlight    atomic.Int32
		maxInFlight atomic.Int32
		orderMu     sync.Mutex
		completed   int
	)
	step := func(ctx context.Context) {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		inFlight.Add(-1)
		orderMu.Lock()
		completed++
		orderMu.Unlock()
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.refreshAgySerialized(context.Background(), step, step)
		}()
	}
	wg.Wait()

	if max := maxInFlight.Load(); max != 1 {
		t.Fatalf("max concurrent agy probes = %d, want 1 (strict serialization)", max)
	}
	if completed != 8 {
		t.Fatalf("completed serialized steps = %d, want 8", completed)
	}

	// Context cancellation stops remaining serialized steps immediately.
	ctx, cancel := context.WithCancel(context.Background())
	ran := 0
	s.refreshAgySerialized(ctx,
		func(context.Context) { ran++; cancel() },
		func(context.Context) { ran++ },
	)
	if ran != 1 {
		t.Fatalf("cancelled serialized run executed %d steps, want 1", ran)
	}
	_ = strconv.Itoa(ran)
}

func TestPhase3AccountSSEEventNameAndConversationWireContract(t *testing.T) {
	// Verify AccountSlotConversation JSON field names match assets/usage.js consumers.
	raw, err := json.Marshal(AccountSlotConversation{
		ID:                   "conv-wire-1",
		Backend:              "claude",
		Label:                "Claude Lead",
		AccountSlotID:        "default",
		PendingAccountSlotID: "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"id", "backend", "label", "account_slot_id", "pending_account_slot"} {
		if _, ok := fields[want]; !ok {
			t.Fatalf("AccountSlotConversation JSON missing %q: %s", want, string(raw))
		}
	}

	// Verify /rail/events emits the exact "account-changed" SSE event subscribed by dashboard-stream.js.
	f := startPageFixture(t)
	rail := openSSE(t, f.base+"/rail/events")
	_, _ = collect(rail)

	f.d.accountChanged()

	deadline := time.Now().Add(3 * time.Second)
	var gotEvent bool
	for time.Now().Before(deadline) && !gotEvent {
		select {
		case ev := <-rail:
			if ev.name == "account-changed" {
				if !strings.Contains(ev.data, `"rev":1`) {
					t.Fatalf("account-changed payload = %q, want rev:1", ev.data)
				}
				gotEvent = true
			}
		case <-time.After(20 * time.Millisecond):
		}
	}
	if !gotEvent {
		t.Fatal("did not receive SSE event \"account-changed\" after notifyAccountChanged()")
	}
}
