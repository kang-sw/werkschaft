package wsdashboard

import (
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
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
)

type stubAccountSlotHost struct {
	AgentHost
	mu             sync.Mutex
	convs          []AccountSlotConversation
	queuedHarness  []string
	queuedSlot     string
	switchNowCalls []string
	queueConvCalls []string
	nowErr         error
}

func (*stubAccountSlotHost) Handler() http.Handler { return http.NotFoundHandler() }

func (h *stubAccountSlotHost) AccountSlotConversations(harness string) []AccountSlotConversation {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []AccountSlotConversation
	for _, c := range h.convs {
		if harness == "" || c.Backend == harness {
			out = append(out, c)
		}
	}
	return out
}

func (h *stubAccountSlotHost) QueueHarnessAccountSlot(harness, slotID string) (queued, skipped int, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queuedHarness = append(h.queuedHarness, harness+":"+slotID)
	h.queuedSlot = slotID
	for i := range h.convs {
		if h.convs[i].Backend != harness {
			continue
		}
		if wscred.NormalizeAccountSlotID(h.convs[i].AccountSlotID) == slotID {
			skipped++
			continue
		}
		h.convs[i].PendingAccountSlotID = slotID
		queued++
	}
	return queued, skipped, nil
}

func (h *stubAccountSlotHost) QueueConversationAccountSlot(convID, slotID string) (AccountSlotSwitchResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queueConvCalls = append(h.queueConvCalls, convID+":"+slotID)
	for i := range h.convs {
		if h.convs[i].ID == convID {
			h.convs[i].PendingAccountSlotID = slotID
			return AccountSlotSwitchResult{ConversationID: convID, AccountSlotID: slotID, PendingAccountSlotID: slotID, Mode: "queued"}, nil
		}
	}
	return AccountSlotSwitchResult{}, errors.New("unknown conversation")
}

func (h *stubAccountSlotHost) SwitchConversationAccountSlotNow(convID, slotID string) (AccountSlotSwitchResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.switchNowCalls = append(h.switchNowCalls, convID+":"+slotID)
	if h.nowErr != nil {
		return AccountSlotSwitchResult{}, h.nowErr
	}
	for i := range h.convs {
		if h.convs[i].ID == convID {
			h.convs[i].AccountSlotID = slotID
			h.convs[i].PendingAccountSlotID = ""
			return AccountSlotSwitchResult{ConversationID: convID, AccountSlotID: slotID, Mode: "restarted_2a"}, nil
		}
	}
	return AccountSlotSwitchResult{}, errors.New("unknown conversation")
}

func TestAccountSlotRoutesAccessControl(t *testing.T) {
	d := authFixture(t)
	d.lan = true
	lanToken, err := d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	d.credentials = wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))
	d.accountSlots = wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), "auth", wscred.AccountSlotsFileName))
	t.Cleanup(d.accountSlots.Close)

	localPeer := authRequest(http.MethodGet, "/", "").RemoteAddr
	lanPeer := net.JoinHostPort(net.IPv4(192, 0, 2, 8).String(), "8888")
	routes := []struct{ method, path string }{
		{http.MethodPost, "openai/switch"},
		{http.MethodPost, "sign-in/complete"},
		{http.MethodPost, "slots/add"},
		{http.MethodPost, "slots/remove"},
		{http.MethodPost, "slots/switch"},
		{http.MethodPost, "slots/sign-in/start"},
		{http.MethodGet, "slots/sign-in/status"},
		{http.MethodPost, "slots/sign-in/complete"},
		{http.MethodPost, "slots/sign-in/cancel"},
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
}

func TestAccountDualSignInStaysWaitingOnBrowserFailureAndCompletesManually(t *testing.T) {
	d := authFixture(t)
	d.openSystem = func(string) error { return errors.New("no local browser") }
	store := &accountTestStore{Store: wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))}
	d.credentials = store
	t.Cleanup(d.accountSignIn.stop)

	readySent := make(chan struct{})
	completed := make(chan string, 1)
	store.signInCB = func(ctx context.Context, opts wscred.OpenAISignIn, cb wscred.OpenAISignInCallbacks) error {
		if !cb.IgnoreBrowserError {
			return errors.New("expected IgnoreBrowserError=true")
		}
		if cb.OnReady != nil {
			cb.OnReady(wscred.OpenAISignInReady{
				AuthorizeURL: "https://auth.openai.com/api/accounts/authorize?client_id=dynamic_agent_client&state=s1",
				Dynamic:      true,
				Complete: func(raw string) error {
					if raw == "bad-state" {
						return wscred.ErrSignInStateMismatch
					}
					if raw == "no-client" {
						return wscred.ErrSignInClientIDMissing
					}
					completed <- raw
					return nil
				},
			})
		}
		if cb.OpenBrowser != nil {
			_ = cb.OpenBrowser("http://127.0.0.1:12345/auth/start/secret")
		}
		close(readySent)
		select {
		case <-completed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	w := requestAccount(t, d, http.MethodPost, "sign-in/start", `{"enable_plan":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	var start accountSignInStatus
	if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	<-readySent

	// Browser failure must NOT abort the attempt; status stays waiting with copyable authorize_url.
	var st accountSignInStatus
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sw := requestAccount(t, d, http.MethodGet, "sign-in/status", "")
		if sw.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("missing no-store header: %q", sw.Header().Get("Cache-Control"))
		}
		_ = json.Unmarshal(sw.Body.Bytes(), &st)
		if st.State == "waiting" && st.AuthorizeURL != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st.State != "waiting" || !st.BrowserError || !strings.HasPrefix(st.AuthorizeURL, "https://auth.openai.com/") {
		t.Fatalf("unexpected waiting status after browser error: %+v", st)
	}

	// Fixed error codes on invalid manual input, never echoing input.
	wBad := requestAccount(t, d, http.MethodPost, "sign-in/complete", `{"attempt":`+strconv.Quote(start.Attempt)+`,"input":"bad-state"}`)
	if wBad.Code != http.StatusUnprocessableEntity || !strings.Contains(wBad.Body.String(), `"sign_in_state_mismatch"`) {
		t.Fatalf("bad state = %d %s", wBad.Code, wBad.Body.String())
	}
	wNoClient := requestAccount(t, d, http.MethodPost, "sign-in/complete", `{"attempt":`+strconv.Quote(start.Attempt)+`,"input":"no-client"}`)
	if wNoClient.Code != http.StatusUnprocessableEntity || !strings.Contains(wNoClient.Body.String(), `"sign_in_client_id_missing"`) {
		t.Fatalf("missing client_id = %d %s", wNoClient.Code, wNoClient.Body.String())
	}

	// Valid manual paste completes the attempt and clears authorize_url after completion.
	wOK := requestAccount(t, d, http.MethodPost, "sign-in/complete", `{"attempt":`+strconv.Quote(start.Attempt)+`,"input":"http://127.0.0.1:12345/auth/callback?code=secret-code&state=s1&client_id=oai_1"}`)
	if wOK.Code != http.StatusAccepted || strings.Contains(wOK.Body.String(), "secret-code") {
		t.Fatalf("complete = %d %s", wOK.Code, wOK.Body.String())
	}
	for deadline := time.Now().Add(2 * time.Second); ; {
		sw := requestAccount(t, d, http.MethodGet, "sign-in/status", "")
		st = accountSignInStatus{}
		_ = json.Unmarshal(sw.Body.Bytes(), &st)
		if st.State == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not complete: %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st.AuthorizeURL != "" {
		t.Fatalf("authorize_url should be cleared after completion: %+v", st)
	}
}

func TestAccountOpenAISwitchAndSlotsCRUDAndFanOut(t *testing.T) {
	d := authFixture(t)
	d.accountSlots = wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), "auth", wscred.AccountSlotsFileName))
	t.Cleanup(d.accountSlots.Close)
	d.planUsage = newPlanUsageStore(t.TempDir(), t.TempDir(), func(string) string { return "" })
	d.planUsage.accountSlots = d.accountSlots
	d.planUsage.explicitHome = d.planUsage.home
	d.planUsage.gpt = planUsageEntry{Harness: "codex", AsOf: time.Now().UnixMilli(), Plan: "pro"}
	d.planUsage.lastQuery = time.Now()
	d.accountSlots.SetOnChange(d.accountChanged)

	store := &accountTestStore{Store: wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))}
	store.setActiveOA = func(id string) error {
		switch id {
		case "oai_good":
			return nil
		case "oai_noconsent":
			return wscred.ErrConsentRequired
		default:
			return wscred.ErrOpenAIAccountNotFound
		}
	}
	d.credentials = store

	host := &stubAccountSlotHost{
		convs: []AccountSlotConversation{
			{ID: "conv-1", Backend: "codex", Label: "Lead 1", AccountSlotID: "default"},
			{ID: "conv-2", Backend: "codex", Label: "Worker 2", AccountSlotID: "work"},
		},
	}
	d.agents = host

	// OpenAI switch routes.
	if w := requestAccount(t, d, http.MethodPost, "openai/switch", `{"client_id":"oai_missing"}`); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"account_not_found"`) {
		t.Fatalf("openai switch missing = %d %s", w.Code, w.Body.String())
	}
	if w := requestAccount(t, d, http.MethodPost, "openai/switch", `{"client_id":"oai_noconsent"}`); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"consent_required"`) {
		t.Fatalf("openai switch no consent = %d %s", w.Code, w.Body.String())
	}
	revBefore := d.accountRev.Load()
	if w := requestAccount(t, d, http.MethodPost, "openai/switch", `{"client_id":"oai_good"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mode":"immediate"`) {
		t.Fatalf("openai switch good = %d %s", w.Code, w.Body.String())
	}
	if d.accountRev.Load() <= revBefore {
		t.Fatal("accountRev did not bump after openai switch")
	}

	// Add custom Codex slot; verify default-home aliasing is rejected and deniedBasePaths includes custom dir.
	defaultCodex := filepath.Join(d.planUsage.explicitHome, ".codex")
	if err := os.MkdirAll(defaultCodex, 0o700); err != nil {
		t.Fatal(err)
	}
	if w := requestAccount(t, d, http.MethodPost, "slots/add", `{"harness":"codex","slot_id":"alias","label":"Alias","config_dir":`+strconv.Quote(defaultCodex)+`}`); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"config_dir_is_default"`) {
		t.Fatalf("default alias add = %d %s", w.Code, w.Body.String())
	}
	workDir := filepath.Join(t.TempDir(), "codex-work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if w := requestAccount(t, d, http.MethodPost, "slots/add", `{"harness":"codex","slot_id":"work","label":"Work","config_dir":`+strconv.Quote(workDir)+`}`); w.Code != http.StatusOK {
		t.Fatalf("slots/add = %d %s", w.Code, w.Body.String())
	}
	// Verify deniedBasePaths protects the registered slot dir.
	denied := deniedBasePaths(nil)
	foundDeny := false
	for _, p := range denied {
		if sameSlotPath(p, workDir) {
			foundDeny = true
			break
		}
	}
	if !foundDeny {
		t.Fatalf("workDir %q missing from deniedBasePaths: %v", workDir, denied)
	}

	// Harness-level switch (D13): sets default to "work", queues conv-1, skips conv-2 (already on "work"), and resets planUsage.gpt.
	wSw := requestAccount(t, d, http.MethodPost, "slots/switch", `{"harness":"codex","slot_id":"work"}`)
	if wSw.Code != http.StatusOK {
		t.Fatalf("harness switch = %d %s", wSw.Code, wSw.Body.String())
	}
	var swRes AccountSlotSwitchResult
	if err := json.Unmarshal(wSw.Body.Bytes(), &swRes); err != nil {
		t.Fatal(err)
	}
	if swRes.Mode != "harness_default" || swRes.QueuedCount != 1 || swRes.SkippedCount != 1 {
		t.Fatalf("unexpected harness switch result: %+v", swRes)
	}
	d.planUsage.mu.Lock()
	gptCleared := d.planUsage.gpt.AsOf == 0 && d.planUsage.lastQuery.IsZero()
	d.planUsage.mu.Unlock()
	if !gptCleared {
		t.Fatal("planUsage.gpt cache was not invalidated after codex default slot switch")
	}

	// Per-conversation switch now + busy error mapping.
	host.nowErr = errors.New("conversation_busy")
	if w := requestAccount(t, d, http.MethodPost, "slots/switch", `{"conversation_id":"conv-1","slot_id":"work","mode":"now"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"conversation_busy"`) {
		t.Fatalf("busy switch now = %d %s", w.Code, w.Body.String())
	}
	host.nowErr = nil
	if w := requestAccount(t, d, http.MethodPost, "slots/switch", `{"conversation_id":"conv-1","slot_id":"work","mode":"now"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mode":"restarted_2a"`) {
		t.Fatalf("switch now = %d %s", w.Code, w.Body.String())
	}

	// Status payload exposes slots and conversations from memory.
	wStatus := requestAccount(t, d, http.MethodGet, "status", "")
	if wStatus.Code != http.StatusOK || !strings.Contains(wStatus.Body.String(), `"work"`) || !strings.Contains(wStatus.Body.String(), `"conv-1"`) {
		t.Fatalf("status = %d %s", wStatus.Code, wStatus.Body.String())
	}

	// Removing default slot refused; removing custom slot succeeds.
	if w := requestAccount(t, d, http.MethodPost, "slots/remove", `{"harness":"codex","slot_id":"default"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("remove default = %d %s", w.Code, w.Body.String())
	}
	if w := requestAccount(t, d, http.MethodPost, "slots/remove", `{"harness":"codex","slot_id":"work"}`); w.Code != http.StatusNoContent {
		t.Fatalf("remove work = %d %s", w.Code, w.Body.String())
	}
}

// writeFakeSlotLoginScript creates a portable shell shim (skipped on Windows
// where sh isn't guaranteed) that simulates `codex login` / `--device-auth`
// without ever launching a real CLI or OS browser.
func writeFakeSlotLoginScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake login shim test")
	}
	path := filepath.Join(t.TempDir(), "fake-codex-login.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAccountSlotSignInBrowserSSRFReplayAndDeviceAuthAndCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake login shim test")
	}

	t.Run("browser_ssrf_replay_and_provisioning", func(t *testing.T) {
		d := authFixture(t)
		d.dir = t.TempDir()
		d.accountSlots = wscred.NewAccountSlotRegistry(filepath.Join(d.dir, "auth", wscred.AccountSlotsFileName))
		t.Cleanup(d.accountSlots.Close)
		t.Cleanup(d.slotSignIn.stop)

		// Stub openSystem so NO real OS browser is ever opened!
		openedURL := make(chan string, 1)
		d.openSystem = func(u string) error {
			select {
			case openedURL <- u:
			default:
			}
			return nil
		}

		// Start a test loopback HTTP listener simulating codex login's callback listener.
		cbGot := make(chan string, 1)
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cbGot <- r.URL.RequestURI()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})}
		go srv.Serve(ln)
		t.Cleanup(func() { _ = srv.Close() })

		// Signal file written once callback arrives so the fake CLI writes auth.json and exits.
		doneSignal := filepath.Join(t.TempDir(), "done.flag")
		go func() {
			<-cbGot
			_ = os.WriteFile(doneSignal, []byte("ok"), 0o600)
		}()

		script := writeFakeSlotLoginScript(t, fmt.Sprintf(`
if [ -z "$CODEX_HOME" ]; then
  echo "missing CODEX_HOME" >&2
  exit 2
fi
echo "Starting local login server on http://localhost:%d."
echo "https://auth.openai.com/oauth/authorize?response_type=code&redirect_uri=http%%3A%%2F%%2F127.0.0.1%%3A%d%%2Fauth%%2Fcallback&state=st1"
while [ ! -f %q ]; do sleep 0.02; done
printf '{"auth_mode":"chatgpt","tokens":{"access_token":"tok-a","refresh_token":"ref-a"}}\n' > "$CODEX_HOME/auth.json"
exit 0
`, port, port, doneSignal))

		d.slotLoginCmd = func(harness string, mode wscred.SlotLoginMode) (string, []string, error) {
			return "/bin/sh", []string{script}, nil
		}

		w := requestAccount(t, d, http.MethodPost, "slots/sign-in/start", `{"harness":"codex","slot_id":"team-b","label":"Team B","mode":"browser"}`)
		if w.Code != http.StatusAccepted {
			t.Fatalf("start = %d %s", w.Code, w.Body.String())
		}
		var start accountSlotSignInStatus
		_ = json.Unmarshal(w.Body.Bytes(), &start)

		// Wait until authorize_url + callback_ready are surfaced.
		var st accountSlotSignInStatus
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			sw := requestAccount(t, d, http.MethodGet, "slots/sign-in/status", "")
			_ = json.Unmarshal(sw.Body.Bytes(), &st)
			if st.State == "waiting" && st.AuthorizeURL != "" && st.CallbackReady {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if !st.CallbackReady || !strings.HasPrefix(st.AuthorizeURL, "https://auth.openai.com/") {
			t.Fatalf("status did not become ready: %+v", st)
		}

		// Verify managed slot dir exists with owner-only perms and is already in deniedBasePaths during sign-in!
		managedDir := filepath.Join(d.dir, "slots", "codex", "team-b")
		info, err := os.Stat(managedDir)
		if err != nil || !info.IsDir() {
			t.Fatalf("managed dir missing: %v", err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("managed dir permissions too open: %o", info.Mode().Perm())
		}
		denied := deniedBasePaths(nil)
		inDeny := false
		for _, p := range denied {
			if sameSlotPath(p, managedDir) {
				inDeny = true
				break
			}
		}
		if !inDeny {
			t.Fatalf("in-progress slot dir %q not in deniedBasePaths", managedDir)
		}

		// SSRF guard: reject non-loopback host and mismatched port.
		wHost := requestAccount(t, d, http.MethodPost, "slots/sign-in/complete",
			fmt.Sprintf(`{"attempt":%q,"input":"http://evil.example:%d/auth/callback?code=x"}`, start.Attempt, port))
		if wHost.Code != http.StatusUnprocessableEntity || !strings.Contains(wHost.Body.String(), `"callback_host_not_loopback"`) {
			t.Fatalf("evil host = %d %s", wHost.Code, wHost.Body.String())
		}
		wPort := requestAccount(t, d, http.MethodPost, "slots/sign-in/complete",
			fmt.Sprintf(`{"attempt":%q,"input":"http://127.0.0.1:%d/auth/callback?code=x"}`, start.Attempt, port+1))
		if wPort.Code != http.StatusUnprocessableEntity || !strings.Contains(wPort.Body.String(), `"callback_port_mismatch"`) {
			t.Fatalf("wrong port = %d %s", wPort.Code, wPort.Body.String())
		}

		// Valid loopback callback replays to fake CLI listener and completes registration.
		wOK := requestAccount(t, d, http.MethodPost, "slots/sign-in/complete",
			fmt.Sprintf(`{"attempt":%q,"input":"http://localhost:%d/auth/callback?code=valid-code&state=st1"}`, start.Attempt, port))
		if wOK.Code != http.StatusAccepted {
			t.Fatalf("valid replay = %d %s", wOK.Code, wOK.Body.String())
		}
		for deadline := time.Now().Add(3 * time.Second); ; {
			sw := requestAccount(t, d, http.MethodGet, "slots/sign-in/status", "")
			st = accountSlotSignInStatus{}
			_ = json.Unmarshal(sw.Body.Bytes(), &st)
			if st.State == "succeeded" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("slot sign-in did not complete: %+v", st)
			}
			time.Sleep(15 * time.Millisecond)
		}
		slot, ok := d.accountSlots.ResolveSlot("codex", "team-b")
		if !ok || !slot.Managed || !sameSlotPath(slot.ConfigDir, managedDir) {
			t.Fatalf("registered slot = %+v (ok=%v)", slot, ok)
		}
	})

	t.Run("device_auth_surfaces_url_and_code", func(t *testing.T) {
		d := authFixture(t)
		d.dir = t.TempDir()
		d.accountSlots = wscred.NewAccountSlotRegistry(filepath.Join(d.dir, "auth", wscred.AccountSlotsFileName))
		t.Cleanup(d.accountSlots.Close)
		t.Cleanup(d.slotSignIn.stop)
		d.openSystem = func(string) error { return nil }

		doneSignal := filepath.Join(t.TempDir(), "dev.flag")
		script := writeFakeSlotLoginScript(t, fmt.Sprintf(`
printf 'Open \033[94mhttps://auth.openai.com/codex/device\033[0m\n'
printf 'Enter code: \033[1mABCD-56789\033[0m\n'
while [ ! -f %q ]; do sleep 0.02; done
printf '{"auth_mode":"chatgpt","tokens":{"access_token":"tok-d","refresh_token":"ref-d"}}\n' > "$CODEX_HOME/auth.json"
exit 0
`, doneSignal))
		d.slotLoginCmd = func(harness string, mode wscred.SlotLoginMode) (string, []string, error) {
			if mode != wscred.SlotLoginModeDeviceAuth {
				return "", nil, errors.New("unexpected mode")
			}
			return "/bin/sh", []string{script}, nil
		}

		w := requestAccount(t, d, http.MethodPost, "slots/sign-in/start", `{"harness":"codex","slot_id":"dev-slot","label":"Device Slot","mode":"device_auth","copy_only":true}`)
		if w.Code != http.StatusAccepted {
			t.Fatalf("start = %d %s", w.Code, w.Body.String())
		}
		var st accountSlotSignInStatus
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			sw := requestAccount(t, d, http.MethodGet, "slots/sign-in/status", "")
			_ = json.Unmarshal(sw.Body.Bytes(), &st)
			if st.VerifyURL != "" && st.UserCode != "" {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if st.VerifyURL != "https://auth.openai.com/codex/device" || st.UserCode != "ABCD-56789" {
			t.Fatalf("device-auth status = %+v", st)
		}
		_ = os.WriteFile(doneSignal, []byte("ok"), 0o600)
		for deadline := time.Now().Add(3 * time.Second); ; {
			sw := requestAccount(t, d, http.MethodGet, "slots/sign-in/status", "")
			st = accountSlotSignInStatus{}
			_ = json.Unmarshal(sw.Body.Bytes(), &st)
			if st.State == "succeeded" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("device-auth did not complete: %+v", st)
			}
			time.Sleep(15 * time.Millisecond)
		}
		if _, ok := d.accountSlots.ResolveSlot("codex", "dev-slot"); !ok {
			t.Fatal("device-auth slot was not registered")
		}
	})

	t.Run("cancel_kills_process_and_leaves_no_half_provisioned_slot", func(t *testing.T) {
		d := authFixture(t)
		d.dir = t.TempDir()
		d.accountSlots = wscred.NewAccountSlotRegistry(filepath.Join(d.dir, "auth", wscred.AccountSlotsFileName))
		t.Cleanup(d.accountSlots.Close)
		t.Cleanup(d.slotSignIn.stop)
		d.openSystem = func(string) error { return nil }

		script := writeFakeSlotLoginScript(t, `
echo "https://auth.openai.com/oauth/authorize?response_type=code&state=s"
sleep 30
`)
		d.slotLoginCmd = func(string, wscred.SlotLoginMode) (string, []string, error) {
			return "/bin/sh", []string{script}, nil
		}
		w := requestAccount(t, d, http.MethodPost, "slots/sign-in/start", `{"harness":"codex","slot_id":"abort-me","label":"Abort","mode":"browser","copy_only":true}`)
		if w.Code != http.StatusAccepted {
			t.Fatalf("start = %d %s", w.Code, w.Body.String())
		}
		var start accountSlotSignInStatus
		_ = json.Unmarshal(w.Body.Bytes(), &start)

		wCan := requestAccount(t, d, http.MethodPost, "slots/sign-in/cancel", `{"attempt":`+strconv.Quote(start.Attempt)+`}`)
		if wCan.Code != http.StatusAccepted {
			t.Fatalf("cancel = %d %s", wCan.Code, wCan.Body.String())
		}
		var st accountSlotSignInStatus
		for deadline := time.Now().Add(3 * time.Second); ; {
			sw := requestAccount(t, d, http.MethodGet, "slots/sign-in/status", "")
			_ = json.Unmarshal(sw.Body.Bytes(), &st)
			if st.State == "cancelled" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("cancel did not finish: %+v", st)
			}
			time.Sleep(15 * time.Millisecond)
		}
		if _, ok := d.accountSlots.ResolveSlot("codex", "abort-me"); ok {
			t.Fatal("cancelled sign-in registered a half-provisioned slot")
		}
		if _, err := os.Stat(filepath.Join(d.dir, "slots", "codex", "abort-me")); !os.IsNotExist(err) {
			t.Fatalf("cancelled slot dir was not cleaned up: %v", err)
		}
	})
}
