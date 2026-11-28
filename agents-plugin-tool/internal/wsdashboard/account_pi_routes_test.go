package wsdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wspiruntime"
)

func seedInstalledPiRuntime(t *testing.T, mgr *wspiruntime.Manager) {
	t.Helper()
	spec, err := wspiruntime.ResolveAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("ResolveAsset: %v", err)
	}
	dir := mgr.InstallDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := mgr.BinaryPath()
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho fake-pi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker, err := json.Marshal(map[string]string{
		"version": wspiruntime.PinnedVersion,
		"goos":    runtime.GOOS,
		"goarch":  runtime.GOARCH,
		"sha256":  spec.SHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ws-ready"), marker, 0o644); err != nil {
		t.Fatal(err)
	}
	mgr.Invalidate()
	if !mgr.Status().Installed {
		t.Fatalf("seeded runtime not reported installed: %+v", mgr.Status())
	}
}

func piDoLocal(t *testing.T, d *Daemon, method, path, body string) (int, string) {
	t.Helper()
	req := authRequest(method, path, body)
	req.AddCookie(authCookie(t, d))
	req.Header.Set("Origin", "http://"+req.Host)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := authServe(d, req)
	return rec.Code, rec.Body.String()
}

func TestPiAccountRoutesAccessControlAndStatus(t *testing.T) {
	d := authFixture(t)
	d.lan = true
	lanToken, err := d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	d.credentials = wscred.NewStore(filepath.Join(t.TempDir(), "auth", wscred.FileName))
	d.accountSlots = wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), "auth", wscred.AccountSlotsFileName))
	t.Cleanup(d.accountSlots.Close)
	d.piRuntime = wspiruntime.NewManager(wspiruntime.Options{CacheRoot: t.TempDir()})
	d.piLogin.daemon = d

	localPeer := authRequest(http.MethodGet, "/", "").RemoteAddr
	lanPeer := net.JoinHostPort(net.IPv4(192, 0, 2, 9).String(), "8888")
	routes := []struct{ method, path string }{
		{http.MethodPost, "pi/runtime/install"},
		{http.MethodPost, "pi/runtime/uninstall"},
		{http.MethodPost, "pi/login/start"},
		{http.MethodGet, "pi/login/status"},
		{http.MethodPost, "pi/login/complete"},
		{http.MethodPost, "pi/login/cancel"},
		{http.MethodGet, "pi/login/ws"},
	}
	for _, route := range routes {
		t.Run(route.path, func(t *testing.T) {
			path := accountPrefix + route.path
			// LAN viewers get 404 as if routes do not exist.
			for _, v := range []viewer{
				{peer: lanPeer, host: "dashboard.example", cookie: lanToken},
				{peer: lanPeer, host: "localhost:43210", cookie: lanToken},
				{peer: localPeer, host: "dashboard.example", cookie: lanToken},
			} {
				if w := serve(d, v, route.method, path, "{}"); w.Code != http.StatusNotFound {
					t.Fatalf("LAN %s = %d, want 404", route.path, w.Code)
				}
			}
			// Endpoint bearer token is not admitted.
			req := authRequest(route.method, path, "{}")
			req.Header.Set("Authorization", "Bearer "+d.endpoint.Token)
			req.Header.Set("Origin", "http://"+req.Host)
			if w := authServe(d, req); w.Code != http.StatusUnauthorized {
				t.Fatalf("endpoint bearer admitted on %s: %d", route.path, w.Code)
			}
			// Cross-origin POST and WS are refused with 403.
			if route.method == http.MethodPost || route.path == "pi/login/ws" {
				req := authRequest(route.method, path, "{}")
				req.AddCookie(authCookie(t, d))
				req.Header.Set("Origin", "https://evil.example")
				if w := authServe(d, req); w.Code != http.StatusForbidden {
					t.Fatalf("cross-origin admitted on %s: %d", route.path, w.Code)
				}
			}
			// Cross-site Sec-Fetch-Site is refused with 403.
			reqCS := authRequest(route.method, path, "{}")
			reqCS.AddCookie(authCookie(t, d))
			reqCS.Header.Set("Sec-Fetch-Site", "cross-site")
			if w := authServe(d, reqCS); w.Code != http.StatusForbidden {
				t.Fatalf("cross-site fetch admitted on %s: %d", route.path, w.Code)
			}
			// Wrong HTTP method gets 405.
			wrongMethod := http.MethodGet
			if route.method == http.MethodGet {
				wrongMethod = http.MethodPost
			}
			code, _ := piDoLocal(t, d, wrongMethod, path, "")
			if code != http.StatusMethodNotAllowed {
				t.Fatalf("wrong method %s %s = %d, want 405", wrongMethod, route.path, code)
			}
		})
	}

	// Non-upgrade GET to pi/login/ws with same Origin gets 426 Upgrade Required.
	code, _ := piDoLocal(t, d, http.MethodGet, accountPrefix+"pi/login/ws?attempt=abc", "")
	if code != http.StatusUpgradeRequired {
		t.Fatalf("non-upgrade ws GET = %d, want 426", code)
	}

	// /api/account/status exposes pi_runtime and slots["pi-rpc"] while keeping
	// claude/codex and slot_login_modes unchanged.
	code, body := piDoLocal(t, d, http.MethodGet, accountPrefix+"status", "")
	if code != http.StatusOK {
		t.Fatalf("GET status = %d (%s)", code, body)
	}
	var st struct {
		Slots          map[string][]wscred.AccountSlotView `json:"slots"`
		SlotLoginModes map[string][]string                 `json:"slot_login_modes"`
		PiRuntime      *piRuntimeStatusView                `json:"pi_runtime"`
	}
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if st.PiRuntime == nil || st.PiRuntime.Installed || st.PiRuntime.Version != wspiruntime.PinnedVersion {
		t.Fatalf("unexpected pi_runtime in status: %+v", st.PiRuntime)
	}
	if len(st.Slots["pi-rpc"]) == 0 || len(st.Slots["claude"]) == 0 || len(st.Slots["codex"]) == 0 {
		t.Fatalf("expected pi-rpc, claude, codex slots in status: %+v", st.Slots)
	}
	if _, leaked := st.SlotLoginModes["pi-rpc"]; leaked {
		t.Fatalf("pi-rpc must not appear in slot_login_modes: %+v", st.SlotLoginModes)
	}
}

func TestPiRuntimeInstallSingleFlightProgressUninstallAndInvalidation(t *testing.T) {
	d := authFixture(t)
	d.accountSlots = wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), "auth", wscred.AccountSlotsFileName))
	t.Cleanup(d.accountSlots.Close)
	mgr := wspiruntime.NewManager(wspiruntime.Options{CacheRoot: t.TempDir()})
	d.piRuntime = mgr
	d.piLogin.daemon = d

	var invalidations atomic.Int32
	d.onPiRuntimeChanged = func() { invalidations.Add(1) }

	// Subscribe to SSE account-changed wakeups via broker.
	if d.broker == nil {
		d.broker = newBroker(&streamStats{})
	}
	sub, cancelSub := d.broker.subscribe()
	defer cancelSub()

	installStarted := make(chan struct{})
	releaseInstall := make(chan struct{})
	var installCalls atomic.Int32
	d.piRuntimeInstall = func(ctx context.Context, onProgress wspiruntime.ProgressFunc) error {
		installCalls.Add(1)
		if onProgress != nil {
			onProgress(wspiruntime.Progress{Phase: "download", Downloaded: 1024, Total: 4096})
		}
		close(installStarted)
		select {
		case <-releaseInstall:
		case <-ctx.Done():
			return ctx.Err()
		}
		if onProgress != nil {
			onProgress(wspiruntime.Progress{Phase: "extract"})
		}
		seedInstalledPiRuntime(t, mgr)
		return nil
	}

	// First install returns 202 and transitions installing=true.
	code, body := piDoLocal(t, d, http.MethodPost, accountPrefix+"pi/runtime/install", "{}")
	if code != http.StatusAccepted {
		t.Fatalf("first install = %d (%s), want 202", code, body)
	}
	select {
	case <-installStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for install goroutine")
	}

	// Second concurrent install shares single-flight (202, installCalls stays 1)
	// and reports live progress.
	code2, body2 := piDoLocal(t, d, http.MethodPost, accountPrefix+"pi/runtime/install", "{}")
	if code2 != http.StatusAccepted {
		t.Fatalf("concurrent install = %d (%s), want 202", code2, body2)
	}
	var view piRuntimeStatusView
	if err := json.Unmarshal([]byte(body2), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Installing || view.Progress == nil || view.Progress.Phase != "download" || view.Progress.Downloaded != 1024 {
		t.Fatalf("concurrent install progress view = %+v", view)
	}
	if installCalls.Load() != 1 {
		t.Fatalf("installCalls = %d, want 1 (single-flight)", installCalls.Load())
	}

	// Uninstall while installing is refused with 409 runtime_busy.
	codeBusy, bodyBusy := piDoLocal(t, d, http.MethodPost, accountPrefix+"pi/runtime/uninstall", "{}")
	if codeBusy != http.StatusConflict || !strings.Contains(bodyBusy, "runtime_busy") {
		t.Fatalf("uninstall while installing = %d (%s), want 409 runtime_busy", codeBusy, bodyBusy)
	}

	// Release install and wait for completion + choices invalidation + SSE wakeup.
	close(releaseInstall)
	termWait(t, "install completes", func() bool {
		st := d.piRuntimeStatus()
		return !st.Installing && st.Installed
	})
	if invalidations.Load() < 1 {
		t.Fatalf("expected OnPiRuntimeChanged after install, got %d", invalidations.Load())
	}
	select {
	case <-sub.ch:
	case <-time.After(time.Second):
		t.Fatal("expected SSE account wakeup on install")
	}

	// Uninstall removes the runtime and triggers invalidation again.
	beforeInv := invalidations.Load()
	codeUn, bodyUn := piDoLocal(t, d, http.MethodPost, accountPrefix+"pi/runtime/uninstall", "{}")
	if codeUn != http.StatusOK {
		t.Fatalf("uninstall = %d (%s), want 200", codeUn, bodyUn)
	}
	var unView piRuntimeStatusView
	if err := json.Unmarshal([]byte(bodyUn), &unView); err != nil {
		t.Fatal(err)
	}
	if unView.Installed || unView.Installing {
		t.Fatalf("post-uninstall view = %+v, want installed=false", unView)
	}
	if invalidations.Load() <= beforeInv {
		t.Fatal("expected OnPiRuntimeChanged after uninstall")
	}
}

func dialPiLoginWS(t *testing.T, base string, d *Daemon, wsPath string) *termClient {
	t.Helper()
	url := "ws" + strings.TrimPrefix(base, "http") + wsPath
	cfg, err := websocket.NewConfig(url, base)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Header.Set("Cookie", localhostTestCookie(t, d).String())
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("pi login websocket %s: %v", wsPath, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &termClient{conn: conn}
	go func() {
		for {
			var raw string
			if err := websocket.Message.Receive(conn, &raw); err != nil {
				c.mu.Lock()
				c.ended = true
				c.mu.Unlock()
				return
			}
			var frame termFrame
			if json.Unmarshal([]byte(raw), &frame) != nil {
				continue
			}
			c.mu.Lock()
			switch frame.T {
			case "hello":
				c.hello = &frame
			case "out":
				c.seqs, c.recs = append(c.seqs, frame.Seq), append(c.recs, frame.Data)
				c.out.Write(frame.Data)
			case "exit":
				c.seqs, c.recs = append(c.seqs, frame.Seq), append(c.recs, nil)
				c.exit = &frame
			case "gap":
				c.gaps = append(c.gaps, frame)
			case "closed":
				c.closed = true
			case "ping":
				_ = websocket.Message.Send(conn, `{"t":"pong"}`)
			}
			c.mu.Unlock()
		}
	}()
	return c
}

func piLoginHTTPDo(t *testing.T, f *pageFixture, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, f.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(localhostTestCookie(t, f.d))
	req.Header.Set("Origin", f.base)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := termHTTP.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var buf strings.Builder
	b := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(b)
		if n > 0 {
			buf.Write(b[:n])
		}
		if rerr != nil {
			break
		}
	}
	return resp.StatusCode, buf.String()
}

func TestPiLoginPTYEndToEndCancelErrorsExistingAndClose(t *testing.T) {
	holdersDir, python := termHoldersDir(t)

	// Interactive fake Pi login script over PTY:
	// - prints READY plus env hygiene check
	// - reads lines:
	//   - "LOGIN" writes valid auth.json into PI_CODING_AGENT_DIR and prints AUTH_WRITTEN
	//   - "QUIT" exits 0
	//   - "FAIL" exits 0 without writing auth.json
	scriptPath := filepath.Join(t.TempDir(), "fake-pi-login.py")
	script := `import json, os, sys
dir_path = os.environ.get("PI_CODING_AGENT_DIR", "")
leaked = [k for k in os.environ if k.startswith(("CLAUDE", "CODEX_", "ORCA_", "WS_PI_")) or k == "PI_OFFLINE"]
print("READY dir=" + dir_path + " skip=" + os.environ.get("PI_SKIP_VERSION_CHECK", "") + " tel=" + os.environ.get("PI_TELEMETRY", "") + " leaked=" + ",".join(leaked) + " argv=" + " ".join(sys.argv[1:]), flush=True)
for line in sys.stdin:
    cmd = line.strip()
    if cmd == "LOGIN":
        with open(os.path.join(dir_path, "auth.json"), "w") as f:
            json.dump({"openai": {"type": "api_key", "key": "sk-fake-secret"}}, f)
        print("AUTH_WRITTEN", flush=True)
    elif cmd == "QUIT":
        print("BYE", flush=True)
        sys.exit(0)
    elif cmd == "FAIL":
        sys.exit(0)
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// Poison parent env to verify scrubbing in piLoginEnv.
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CODEX_HOME", "/poison/codex")
	t.Setenv("ORCA_SECRET", "1")
	t.Setenv("WS_PI_LEAK", "1")

	cacheRoot := t.TempDir()
	mgr := wspiruntime.NewManager(wspiruntime.Options{CacheRoot: cacheRoot})
	seedInstalledPiRuntime(t, mgr)
	slotsReg := wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), wscred.AccountSlotsFileName))
	t.Cleanup(slotsReg.Close)

	var invalidations atomic.Int32
	holderMgr := wsholder.New(wsholder.Options{Dir: holdersDir, Python: python})
	f := startPageFixture(t,
		withHolders(holderMgr),
		func(o *DaemonOptions) {
			o.PiRuntime = mgr
			o.AccountSlots = slotsReg
			o.OnPiRuntimeChanged = func() { invalidations.Add(1) }
			o.piLoginDisconnectGrace = 250 * time.Millisecond
			o.piLoginCommand = func(provider string) (string, []string, error) {
				if provider != "" {
					return python, []string{"-u", scriptPath, "auth", "login", provider}, nil
				}
				return python, []string{"-u", scriptPath, "--no-session"}, nil
			}
		},
	)

	// 1. Invalid provider name rejected with 400 invalid_provider.
	for _, bad := range []string{"-flag", "../escape", "has space", "UPPER"} {
		code, body := piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/login/start", `{"label":"Bad","provider":"`+bad+`"}`)
		if code != http.StatusBadRequest || !strings.Contains(body, "invalid_provider") {
			t.Fatalf("bad provider %q = %d (%s), want 400 invalid_provider", bad, code, body)
		}
	}

	// 2. End-to-end new-slot PTY login: start -> WS hello/out/in/size ->
	//    complete before auth (422, PTY stays alive) -> write auth -> exit ->
	//    AddSlot commit + RegisterTempSlotDir + choices invalidation.
	code, body := piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/login/start", `{"label":"Work Pi","provider":"openai","rows":30,"cols":100}`)
	if code != http.StatusOK {
		t.Fatalf("start login = %d (%s), want 200", code, body)
	}
	var started piLoginStatus
	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatal(err)
	}
	if started.State != "running" || started.Attempt == "" || started.SlotID == "" || started.Provider != "openai" || !strings.Contains(started.WSPath, started.Attempt) {
		t.Fatalf("unexpected start response: %+v", started)
	}
	stagingDir := filepath.Join(f.d.dir, "slots", "pi-rpc", started.SlotID)
	if !slices.Contains(wscred.RegisteredSlotDirs(), filepath.Clean(stagingDir)) {
		t.Fatalf("staging dir %q not protected by RegisterTempSlotDir while running", stagingDir)
	}

	// Single-attempt 409 login_in_progress and uninstall 409 runtime_busy.
	if c2, b2 := piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/login/start", `{"label":"Second"}`); c2 != http.StatusConflict || !strings.Contains(b2, "login_in_progress") {
		t.Fatalf("concurrent start = %d (%s), want 409 login_in_progress", c2, b2)
	}
	if c3, b3 := piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/runtime/uninstall", `{}`); c3 != http.StatusConflict || !strings.Contains(b3, "runtime_busy") {
		t.Fatalf("uninstall during login = %d (%s), want 409 runtime_busy", c3, b3)
	}

	// Connect WebSocket, verify hello + READY output + scrubbed env + resize.
	ws := dialPiLoginWS(t, f.base, f.d, started.WSPath)
	ws.waitText(t, "READY ")
	outText := ws.text()
	if !strings.Contains(outText, "skip=1 tel=0 leaked= argv=auth login openai") {
		t.Fatalf("env/argv hygiene failed in PTY output: %q", outText)
	}
	ws.mu.Lock()
	gotHello := ws.hello
	ws.mu.Unlock()
	if gotHello == nil || gotHello.State != "running" || gotHello.Rows != 30 || gotHello.Cols != 100 {
		t.Fatalf("hello frame = %+v, want running 30x100", gotHello)
	}
	ws.send(t, `{"t":"size","rows":32,"cols":110}`)

	// Calling complete before auth.json exists returns 422 auth_not_ready without killing PTY.
	if cc, cb := piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/login/complete", `{"attempt":"`+started.Attempt+`"}`); cc != http.StatusUnprocessableEntity || !strings.Contains(cb, "auth_not_ready") {
		t.Fatalf("premature complete = %d (%s), want 422 auth_not_ready", cc, cb)
	}

	// Type LOGIN + QUIT over WS -> writes auth.json, exits -> daemon commits slot.
	invBefore := invalidations.Load()
	ws.typeLine(t, "LOGIN")
	ws.waitText(t, "AUTH_WRITTEN")
	ws.typeLine(t, "QUIT")

	termWait(t, "pi login succeeds on child exit", func() bool {
		return f.d.piLogin.status().State == "succeeded"
	})
	finalSt := f.d.piLogin.status()
	if finalSt.AuthReady == nil || !*finalSt.AuthReady {
		t.Fatalf("final status missing auth_ready=true: %+v", finalSt)
	}
	slot, ok := slotsReg.ResolveSlot("pi-rpc", started.SlotID)
	if !ok || slot.Label != "Work Pi" || !slot.Managed {
		t.Fatalf("committed slot = (%+v, %v), want managed Work Pi", slot, ok)
	}
	if invalidations.Load() <= invBefore {
		t.Fatal("expected OnPiRuntimeChanged on login success")
	}

	// 3. Cancel cleanup: removes uncommitted staging dir and stops holder.
	code, body = piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/login/start", `{"label":"To Cancel"}`)
	if code != http.StatusOK {
		t.Fatalf("start cancel test = %d (%s)", code, body)
	}
	var cancelAtt piLoginStatus
	_ = json.Unmarshal([]byte(body), &cancelAtt)
	cancelDir := filepath.Join(f.d.dir, "slots", "pi-rpc", cancelAtt.SlotID)
	if _, err := os.Stat(cancelDir); err != nil {
		t.Fatalf("expected staging dir %q to exist", cancelDir)
	}
	code, body = piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/login/cancel", `{"attempt":"`+cancelAtt.Attempt+`"}`)
	if code != http.StatusOK || !strings.Contains(body, `"cancelled"`) {
		t.Fatalf("cancel = %d (%s), want 200 cancelled", code, body)
	}
	if _, err := os.Stat(cancelDir); !os.IsNotExist(err) {
		t.Fatalf("cancel left uncommitted staging dir behind: %v", err)
	}
	if slices.Contains(wscred.RegisteredSlotDirs(), filepath.Clean(cancelDir)) {
		t.Fatalf("cancel leaked temp deny entry for %q", cancelDir)
	}

	// 4. Unready exit cleanup: child exits without writing auth.json -> state=failed, dir removed.
	code, body = piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/login/start", `{"label":"Will Fail"}`)
	if code != http.StatusOK {
		t.Fatalf("start fail test = %d (%s)", code, body)
	}
	var failAtt piLoginStatus
	_ = json.Unmarshal([]byte(body), &failAtt)
	failDir := filepath.Join(f.d.dir, "slots", "pi-rpc", failAtt.SlotID)
	wsFail := dialPiLoginWS(t, f.base, f.d, failAtt.WSPath)
	wsFail.waitText(t, "READY ")
	wsFail.typeLine(t, "FAIL")
	termWait(t, "unready exit fails", func() bool {
		st := f.d.piLogin.status()
		return st.Attempt == failAtt.Attempt && st.State == "failed" && st.Reason == "auth_not_ready"
	})
	if _, err := os.Stat(failDir); !os.IsNotExist(err) {
		t.Fatalf("unready exit left staging dir behind: %v", err)
	}

	// 5. WebSocket disconnect grace period auto-cancels and cleans up uncommitted dir.
	code, body = piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/login/start", `{"label":"Drop WS"}`)
	if code != http.StatusOK {
		t.Fatalf("start grace test = %d (%s)", code, body)
	}
	var graceAtt piLoginStatus
	_ = json.Unmarshal([]byte(body), &graceAtt)
	graceDir := filepath.Join(f.d.dir, "slots", "pi-rpc", graceAtt.SlotID)
	wsGrace := dialPiLoginWS(t, f.base, f.d, graceAtt.WSPath)
	wsGrace.waitText(t, "READY ")
	_ = wsGrace.conn.Close()
	termWait(t, "disconnect grace cancels attempt", func() bool {
		st := f.d.piLogin.status()
		return st.Attempt == graceAtt.Attempt && st.State == "cancelled" && st.Reason == "disconnected"
	})
	if _, err := os.Stat(graceDir); !os.IsNotExist(err) {
		t.Fatalf("disconnect grace left staging dir behind: %v", err)
	}

	// 6. Existing-slot re-auth: blocks POST /api/account/slots/remove (409 login_in_progress),
	//    and cancelling NEVER deletes the existing slot's directory!
	code, body = piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/login/start", `{"slot_id":"`+started.SlotID+`","existing":true}`)
	if code != http.StatusOK {
		t.Fatalf("start existing re-auth = %d (%s)", code, body)
	}
	var existAtt piLoginStatus
	_ = json.Unmarshal([]byte(body), &existAtt)
	if !existAtt.Existing {
		t.Fatalf("expected existing=true in response: %+v", existAtt)
	}
	codeRem, bodyRem := piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"slots/remove", `{"harness":"pi-rpc","slot_id":"`+started.SlotID+`","delete_dir":true}`)
	if codeRem != http.StatusConflict || !strings.Contains(bodyRem, "login_in_progress") {
		t.Fatalf("remove busy slot = %d (%s), want 409 login_in_progress", codeRem, bodyRem)
	}
	code, body = piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/login/cancel", `{"attempt":"`+existAtt.Attempt+`"}`)
	if code != http.StatusOK {
		t.Fatalf("cancel existing re-auth = %d (%s)", code, body)
	}
	if _, err := os.Stat(stagingDir); err != nil {
		t.Fatalf("cancelling existing slot re-auth deleted slot directory: %v", err)
	}

	// 7. Active login stopped cleanly on Daemon.Close() (holder stopped + uncommitted dir removed).
	code, body = piLoginHTTPDo(t, f, http.MethodPost, accountPrefix+"pi/login/start", `{"label":"Close Cleanup"}`)
	if code != http.StatusOK {
		t.Fatalf("start close cleanup = %d (%s)", code, body)
	}
	var closeAtt piLoginStatus
	_ = json.Unmarshal([]byte(body), &closeAtt)
	closeDir := filepath.Join(f.d.dir, "slots", "pi-rpc", closeAtt.SlotID)
	f.d.Close()
	if _, err := os.Stat(closeDir); !os.IsNotExist(err) {
		t.Fatalf("Daemon.Close() left uncommitted staging dir behind: %v", err)
	}
	// Verify no pi-login holder remains running.
	checkMgr := wsholder.New(wsholder.Options{Dir: holdersDir, Python: python})
	defer checkMgr.Close()
	checkMgr.Scan()
	for _, h := range checkMgr.List().Holders {
		var meta piLoginHolderSession
		if json.Unmarshal(h.Session, &meta) == nil && meta.Kind == piLoginHolderKind {
			t.Fatalf("Daemon.Close() left pi-login holder %s running", h.ID)
		}
	}
}

func TestPiLoginStartupSweepsOrphanHolders(t *testing.T) {
	holdersDir, python := termHoldersDir(t)

	// Simulate a crash-orphaned pi-login holder left behind by a prior daemon.
	preMgr := wsholder.New(wsholder.Options{Dir: holdersDir, Python: python})
	meta, _ := json.Marshal(piLoginHolderSession{
		Kind:    piLoginHolderKind,
		Attempt: "orphan-attempt-1",
		SlotID:  "orphan-slot",
		Label:   "pi-login: orphan",
	})
	orphanID, err := preMgr.Launch(meta)
	if err != nil {
		t.Fatalf("Launch orphan holder: %v", err)
	}
	if _, err := preMgr.Client(orphanID).Spawn(wsholder.SpawnSpec{
		Argv: []string{python, "-c", "import time; time.sleep(60)"},
		Mode: "pty",
		Rows: 24,
		Cols: 80,
	}); err != nil {
		t.Fatalf("Spawn orphan child: %v", err)
	}
	// Disconnect manager without stopping the holder (simulates daemon crash).
	preMgr.Close()

	// Start a fresh daemon pointing at the same holdersDir -> startup sweeps the orphan.
	nextMgr := wsholder.New(wsholder.Options{Dir: holdersDir, Python: python})
	_ = startPageFixture(t, withHolders(nextMgr))

	termWait(t, "startup sweeps orphan pi-login holder", func() bool {
		for _, h := range nextMgr.List().Holders {
			if h.ID == orphanID {
				return false
			}
		}
		return true
	})
}

func TestPiLoginEnvScrubsParentHarnessVars(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PI_API_KEY", "secret")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CODEX_HOME", "/tmp/codex")
	t.Setenv("ORCA_TOKEN", "tok")
	t.Setenv("WS_PI_FOO", "bar")
	t.Setenv("USER_CUSTOM_KEEP", "kept-val")

	targetDir := filepath.Join(t.TempDir(), "slot-a")
	env := piLoginEnv(targetDir)
	if env["PI_CODING_AGENT_DIR"] != filepath.Clean(targetDir) {
		t.Fatalf("PI_CODING_AGENT_DIR = %q, want %q", env["PI_CODING_AGENT_DIR"], filepath.Clean(targetDir))
	}
	if env["PI_SKIP_VERSION_CHECK"] != "1" || env["PI_TELEMETRY"] != "0" || env["TERM"] != "xterm-256color" {
		t.Fatalf("missing required Pi login env vars: %v", env)
	}
	if env["USER_CUSTOM_KEEP"] != "kept-val" {
		t.Fatalf("dropped unrelated user env var")
	}
	for _, forbidden := range []string{"PI_OFFLINE", "PI_API_KEY", "CLAUDECODE", "CODEX_HOME", "ORCA_TOKEN", "WS_PI_FOO"} {
		if _, ok := env[forbidden]; ok {
			t.Fatalf("forbidden parent env var leaked: %s", forbidden)
		}
	}
}

func TestPiRuntimeInstallErrorRecordedAndClearedOnUninstall(t *testing.T) {
	d := authFixture(t)
	mgr := wspiruntime.NewManager(wspiruntime.Options{CacheRoot: t.TempDir()})
	d.piRuntime = mgr
	d.piLogin.daemon = d
	d.piRuntimeInstall = func(context.Context, wspiruntime.ProgressFunc) error {
		return errors.New("sha256 mismatch")
	}
	code, _ := piDoLocal(t, d, http.MethodPost, accountPrefix+"pi/runtime/install", "{}")
	if code != http.StatusAccepted {
		t.Fatalf("install = %d, want 202", code)
	}
	termWait(t, "install error recorded", func() bool {
		st := d.piRuntimeStatus()
		return !st.Installing && strings.Contains(st.Error, "sha256 mismatch")
	})
	codeUn, bodyUn := piDoLocal(t, d, http.MethodPost, accountPrefix+"pi/runtime/uninstall", "{}")
	if codeUn != http.StatusOK {
		t.Fatalf("uninstall = %d (%s)", codeUn, bodyUn)
	}
	if st := d.piRuntimeStatus(); st.Error != "" {
		t.Fatalf("expected error cleared after uninstall, got %q", st.Error)
	}
}

func TestSettingsPageEmitsXtermAssetAndAccountFlagForPiRuntime(t *testing.T) {
	// 1. piRuntime alone enables data-settings-account; without holders, data-term is omitted.
	d := authFixture(t)
	d.config = settingsTestBackend(t)
	d.accountSlots = nil
	d.credentials = nil
	d.piRuntime = wspiruntime.NewManager(wspiruntime.Options{CacheRoot: t.TempDir()})
	d.piLogin.daemon = d

	rec := settingsRequest(t, d, http.MethodGet, "/settings?scope=global&frame=1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings?scope=global&frame=1 (no holders) = %d (%s)", rec.Code, rec.Body.String())
	}
	html := rec.Body.String()
	if !strings.Contains(html, "data-settings-account") {
		t.Fatalf("expected data-settings-account when piRuntime is set, got HTML without it")
	}
	if strings.Contains(html, `data-term="`) {
		t.Fatalf("expected data-term omitted when holders is nil")
	}

	assets, err := loadDashboardAssets()
	if err != nil {
		t.Fatalf("loadDashboardAssets: %v", err)
	}
	if strings.Contains(html, "/s/"+assets.data.FrameXtermFile) {
		t.Fatalf("expected FrameXtermFile stylesheet omitted when holders is nil")
	}

	// 2. With holders attached on a local request, data-term and FrameXtermFile stylesheet are emitted.
	holdersDir, python := termHoldersDir(t)
	mgr := wsholder.New(wsholder.Options{Dir: holdersDir, Python: python})
	defer mgr.Close()
	d.holders = mgr

	rec2 := settingsRequest(t, d, http.MethodGet, "/settings?scope=global&frame=1", "", "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET /settings?scope=global&frame=1 (with holders) = %d (%s)", rec2.Code, rec2.Body.String())
	}
	html2 := rec2.Body.String()
	xtermName, xtermData := XtermAsset()
	if xtermName == "" || len(xtermData) == 0 {
		t.Fatalf("XtermAsset returned empty asset (%q, %d bytes)", xtermName, len(xtermData))
	}
	if !strings.Contains(html2, `data-term="`+xtermName+`"`) {
		t.Fatalf("expected data-term=%q in settings HTML", xtermName)
	}
	if !strings.Contains(html2, `/s/`+assets.data.FrameXtermFile) {
		t.Fatalf("expected FrameXtermFile stylesheet (/s/%s) in settings HTML", assets.data.FrameXtermFile)
	}
}
