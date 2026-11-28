package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"golang.org/x/net/websocket"
)

type testLogWriter struct{ t *testing.T }

func (w *testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

type companionTestBackend struct {
	mu      sync.Mutex
	procs   []*companionTestProc
	handler func(p *companionTestProc, text string)
}

func (b *companionTestBackend) Start(_ context.Context, spec wsagent.StartSpec) (wsagent.Process, error) {
	instr, _ := os.ReadFile(spec.Instructions)
	instrText := string(instr)
	wsKey := extractWSKeyFromInstructions(instrText)
	role := ""
	if strings.Contains(instrText, "desk smartphone messenger") {
		role = mcp.CompanionRoleBody
	} else if strings.Contains(instrText, "hidden companion work brain") {
		role = mcp.CompanionRoleWork
	}
	p := &companionTestProc{
		backend:      b,
		spec:         spec,
		role:         role,
		wsKey:        wsKey,
		instructions: instrText,
		events:       make(chan wsagent.Event, 64),
		stopCh:       make(chan struct{}),
	}
	b.mu.Lock()
	b.procs = append(b.procs, p)
	b.mu.Unlock()
	return p, nil
}

func (b *companionTestBackend) procsFor(role string) []*companionTestProc {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*companionTestProc
	for _, p := range b.procs {
		if p.role == role {
			out = append(out, p)
		}
	}
	return out
}

type companionTestProc struct {
	backend      *companionTestBackend
	spec         wsagent.StartSpec
	role         string
	wsKey        string
	instructions string
	events       chan wsagent.Event
	stopCh       chan struct{}
	eventsMu     sync.RWMutex

	mu        sync.Mutex
	sends     []string
	closed    bool
	closeOnce sync.Once
}

func (p *companionTestProc) Events() <-chan wsagent.Event { return p.events }

func (p *companionTestProc) Send(text string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("process closed")
	}
	p.sends = append(p.sends, text)
	p.mu.Unlock()

	p.backend.mu.Lock()
	h := p.backend.handler
	p.backend.mu.Unlock()
	if h != nil {
		go h(p, text)
	}
	return nil
}

func (p *companionTestProc) Interrupt() error       { return nil }
func (p *companionTestProc) SetModel(string) error  { return nil }
func (p *companionTestProc) SetEffort(string) error { return nil }
func (p *companionTestProc) Close() error {
	p.closeOnce.Do(func() {
		close(p.stopCh)
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		p.eventsMu.Lock()
		close(p.events)
		p.eventsMu.Unlock()
	})
	return nil
}

func (p *companionTestProc) emitEvent(ev wsagent.Event) bool {
	p.eventsMu.RLock()
	defer p.eventsMu.RUnlock()
	select {
	case <-p.stopCh:
		return false
	default:
	}
	select {
	case p.events <- ev:
		return true
	case <-p.stopCh:
		return false
	}
}

func (p *companionTestProc) emitTurn(reply, errText string) {
	if !p.emitEvent(wsagent.Event{TurnStart: true}) {
		return
	}
	if reply != "" {
		if !p.emitEvent(wsagent.Event{Live: &wslive.Event{Kind: wslive.KindReply, Text: reply, Final: true}}) {
			return
		}
	}
	p.emitEvent(wsagent.Event{TurnEnd: true, TurnError: errText})
}

func (p *companionTestProc) emitFatal(err error) {
	p.emitEvent(wsagent.Event{Fatal: err})
}

func (p *companionTestProc) receivedSends() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.sends...)
}

func extractWSKeyFromInstructions(text string) string {
	_, rest, ok := strings.Cut(text, "Your ws session key is `")
	if !ok {
		return ""
	}
	key, _, _ := strings.Cut(rest, "`")
	return strings.TrimSpace(key)
}

type companionFixture struct {
	t          *testing.T
	dir        string
	root       string
	backend    *companionTestBackend
	agents     *wsagent.Runner
	daemon     *wsdashboard.Daemon
	rt         *companionRuntime
	now        atomic.Pointer[time.Time]
	authCookie *http.Cookie
}

func newCompanionFixture(t *testing.T, directorMode string) *companionFixture {
	t.Helper()
	return newCompanionFixtureWithConfig(t, wsdashboard.CompanionConfig{
		Enabled:  true,
		Director: directorMode,
		Pack:     "default",
		SoulDir:  t.TempDir(),
	})
}

func newCompanionFixtureWithConfig(t *testing.T, cfg wsdashboard.CompanionConfig) *companionFixture {
	t.Helper()
	if cfg.Pack == "" {
		cfg.Pack = "default"
	}
	if cfg.SoulDir == "" {
		cfg.SoulDir = t.TempDir()
	}
	directorMode := cfg.Director
	cacheHome := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("WS_CACHE_HOME", cacheHome)
	t.Setenv("WS_CONFIG_HOME", configHome)

	// Write global config enabling companion with the requested director mode.
	cfgPath := filepath.Join(configHome, "config.json")
	cfgPayload := fmt.Sprintf(`{"overrides":{"dashboard.companion_enabled":"true","dashboard.companion_director":%q}}`, directorMode)
	if err := os.WriteFile(cfgPath, []byte(cfgPayload), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	dir := t.TempDir()
	root := t.TempDir()
	nowVal := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	f := &companionFixture{
		t:       t,
		dir:     dir,
		root:    root,
		backend: &companionTestBackend{},
	}
	f.now.Store(&nowVal)

	var rtPtr atomic.Pointer[companionRuntime]
	var daemonPtr atomic.Pointer[wsdashboard.Daemon]

	keys := mcp.NewAgentKeyStore()
	claudeReg := wsagent.BuiltinBackendRegistrations()[wsagent.BackendClaude]
	claudeReg.Backend = wsagent.WrapDurableBackend(f.backend)
	claudeReg.ProcessKind = wsagent.ProcessDurable
	regs := map[string]wsagent.BackendRegistration{
		wsagent.BackendClaude: claudeReg,
	}
	logw := &testLogWriter{t: t}
	agents := wsagent.NewRunner(wsagent.Options{
		Dir:      dir,
		Backends: regs,
		Keys:     keys,
		LeadKeys: keys,
		Log:      logw,
		Now:      func() time.Time { return *f.now.Load() },
		WSMCP: func(string) (wsagent.MCPCommand, error) {
			return wsagent.MCPCommand{Command: "/unused/ws-mcp", Env: map[string]string{}}, nil
		},
		DaemonMCP: func(string) (wsagent.MCPCommand, error) {
			return wsagent.MCPCommand{Env: map[string]string{}}, nil
		},
		OnCompanionTurnEnd: func(role, replyID, wsKey, status, errText string) {
			if rt := rtPtr.Load(); rt != nil {
				rt.OnCompanionTurnEnd(role, replyID, wsKey, status, errText)
			}
		},
		OnCompanionActivity: func(role, replyID, wsKey string) {
			if rt := rtPtr.Load(); rt != nil {
				rt.OnCompanionActivity(role, replyID, wsKey)
			}
		},
		OnCompanionClose: func(role, replyID, wsKey, reason string) {
			if rt := rtPtr.Load(); rt != nil {
				rt.OnCompanionClose(role, replyID, wsKey, reason)
			}
		},
	})
	f.agents = agents

	rt := newCompanionRuntime(dir, root, agents, daemonPtr.Load, logw)
	rt.nowFunc = func() time.Time { return *f.now.Load() }
	rt.resolveLaunch = func(role string) (companionLaunchSpec, bool) {
		return companionLaunchSpec{Backend: wsagent.BackendClaude}, true
	}
	rtPtr.Store(rt)
	f.rt = rt

	daemon, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir:      dir,
		Port:     0,
		Version:  version,
		Log:      io.Discard,
		Agents:   agents,
		Sessions: mcp.NewDashboardSessionSource(nil),
		Config: &wsdashboard.ConfigBackend{
			LoadCompanion: func() (wsdashboard.CompanionConfig, error) {
				return cfg, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("StartDaemon: %v", err)
	}
	daemonPtr.Store(daemon)
	f.daemon = daemon
	ep := daemon.Endpoint()
	agents.SetEndpoint(fmt.Sprintf("http://127.0.0.1:%d", ep.Port))
	daemon.SetCompanionHolderAlive(rt.IsHolderAlive)
	daemon.SetCompanionWake(rt.OnUserWake)
	daemon.SetCompanionMessengerClosed(rt.OnMessengerClosed)

	// Mint the dashboard localhost session cookie via the documented machine auth route.
	cookieReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/auth/cookie", ep.Port), nil)
	if err != nil {
		t.Fatalf("NewRequest(/auth/cookie): %v", err)
	}
	cookieReq.Header.Set("Authorization", "Bearer "+ep.Token)
	cookieResp, err := http.DefaultClient.Do(cookieReq)
	if err != nil {
		t.Fatalf("POST /auth/cookie: %v", err)
	}
	var cookieBody struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(cookieResp.Body).Decode(&cookieBody); err != nil {
		cookieResp.Body.Close()
		t.Fatalf("decode /auth/cookie: %v", err)
	}
	cookieResp.Body.Close()
	if cookieResp.StatusCode != http.StatusOK || cookieBody.Name == "" || cookieBody.Value == "" {
		t.Fatalf("/auth/cookie status=%d body=%+v", cookieResp.StatusCode, cookieBody)
	}
	f.authCookie = &http.Cookie{Name: cookieBody.Name, Value: cookieBody.Value}

	t.Cleanup(func() {
		rt.Close()
		daemon.Close()
	})
	return f
}

func (f *companionFixture) postUserMessage(text string) {
	f.t.Helper()
	ep := f.daemon.Endpoint()
	body, _ := json.Marshal(map[string]string{"text": text})
	url := fmt.Sprintf("http://127.0.0.1:%d/api/companion/phone/send", ep.Port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		f.t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", fmt.Sprintf("http://127.0.0.1:%d", ep.Port))
	req.AddCookie(f.authCookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatalf("POST /api/companion/phone/send: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		f.t.Fatalf("POST send status=%d body=%s", resp.StatusCode, string(raw))
	}
}

type rpcWireResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (f *companionFixture) callMCP(hostedKey, method string, params any) rpcWireResponse {
	f.t.Helper()
	envMap := map[string]string{
		"WS_CACHE_HOME":  os.Getenv("WS_CACHE_HOME"),
		"WS_CONFIG_HOME": os.Getenv("WS_CONFIG_HOME"),
	}
	convID := ""
	if hostedKey != "" {
		envMap[wsagent.EnvHostedSession] = "1"
		envMap[wsagent.EnvHostedSessionKey] = hostedKey
		convID = wsmailbox.ReplyID(hostedKey)
	}
	srv := mcp.NewServerWithOptions(mcp.ServerOptions{
		Root:          f.root,
		Version:       version,
		Env:           mcp.EnvFromMap(envMap),
		Conversation:  convID,
		Companion:     f.daemon,
		DaemonControl: f.daemon,
		RecordReader:  f.agents,
		Agents:        f.agents,
	})
	defer srv.Close()

	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"test","version":"1"}}}` + "\n"
	callObj := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  method,
		"params":  params,
	}
	callRaw, err := json.Marshal(callObj)
	if err != nil {
		f.t.Fatalf("marshal RPC %s: %v", method, err)
	}
	var out bytes.Buffer
	if err := srv.ServeStdio(context.Background(), strings.NewReader(initReq+string(callRaw)+"\n"), &out); err != nil {
		f.t.Fatalf("ServeStdio(%s): %v", method, err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		f.t.Fatalf("ServeStdio(%s) produced %d lines: %q", method, len(lines), out.String())
	}
	// ServeStdio handles JSON-RPC requests concurrently, so id=2 may finish
	// before id=1 (initialize). Match response by id=2 explicitly.
	for _, line := range lines {
		var resp rpcWireResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			f.t.Fatalf("unmarshal RPC response: %v\nraw=%s", err, line)
		}
		if strings.TrimSpace(string(resp.ID)) == "2" {
			return resp
		}
	}
	f.t.Fatalf("ServeStdio(%s) missing response id=2 in output:\n%s", method, out.String())
	return rpcWireResponse{}
}

func (f *companionFixture) callTool(hostedKey, name string, args map[string]any) (text string, isErr bool, rpcErr string) {
	f.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	resp := f.callMCP(hostedKey, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if resp.Error != nil {
		return "", false, resp.Error.Message
	}
	var parsed struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &parsed); err != nil {
		f.t.Fatalf("decode tool result %s: %v\nraw=%s", name, err, string(resp.Result))
	}
	if len(parsed.Content) > 0 {
		text = parsed.Content[0].Text
	}
	return text, parsed.IsError, ""
}

func (f *companionFixture) listTools(hostedKey string) map[string]bool {
	f.t.Helper()
	resp := f.callMCP(hostedKey, "tools/list", map[string]any{})
	if resp.Error != nil {
		f.t.Fatalf("tools/list RPC error: %+v", resp.Error)
	}
	var parsed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &parsed); err != nil {
		f.t.Fatalf("decode tools/list: %v", err)
	}
	out := make(map[string]bool, len(parsed.Tools))
	for _, tool := range parsed.Tools {
		out[tool.Name] = true
	}
	return out
}

func waitCompanionUntil(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func TestCompanionRuntimeIdlePerformsZeroHostedTurns(t *testing.T) {
	f := newCompanionFixture(t, "agent")
	f.rt.Start()
	time.Sleep(120 * time.Millisecond)
	if b, w := f.rt.launchCounts(); b != 0 || w != 0 {
		t.Fatalf("(g) idle daemon launched companion sessions: body=%d work=%d, want 0/0", b, w)
	}
	if len(f.backend.procsFor(mcp.CompanionRoleBody)) != 0 || len(f.backend.procsFor(mcp.CompanionRoleWork)) != 0 {
		t.Fatal("(g) idle daemon started hosted companion processes")
	}
}

func TestCompanionWorkLaunchUsesRegisteredWorkspaceRoot(t *testing.T) {
	cacheHome := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("WS_CACHE_HOME", cacheHome)
	t.Setenv("WS_CONFIG_HOME", configHome)
	cfgPath := filepath.Join(configHome, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"overrides":{"dashboard.companion_enabled":"true","dashboard.companion_director":"agent"}}`), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	// Create a fake Git workspace directory and register it in workspaces.json.
	wsDir := filepath.Join(t.TempDir(), "customer-repo")
	if err := os.MkdirAll(filepath.Join(wsDir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	wsFile := wsdashboard.WorkspacesFilePath(cfgPath)
	if err := os.MkdirAll(filepath.Dir(wsFile), 0o700); err != nil {
		t.Fatalf("mkdir workspaces dir: %v", err)
	}
	wsJSON, err := json.Marshal(map[string]any{
		"version":    1,
		"workspaces": []map[string]string{{"path": wsDir}},
	})
	if err != nil {
		t.Fatalf("marshal workspaces.json: %v", err)
	}
	if err := os.WriteFile(wsFile, wsJSON, 0o600); err != nil {
		t.Fatalf("write workspaces.json: %v", err)
	}

	dir := t.TempDir()
	backend := &companionTestBackend{}
	keys := mcp.NewAgentKeyStore()
	claudeReg := wsagent.BuiltinBackendRegistrations()[wsagent.BackendClaude]
	claudeReg.Backend = wsagent.WrapDurableBackend(backend)
	claudeReg.ProcessKind = wsagent.ProcessDurable
	agents := wsagent.NewRunner(wsagent.Options{
		Dir:      dir,
		Backends: map[string]wsagent.BackendRegistration{wsagent.BackendClaude: claudeReg},
		Keys:     keys,
		LeadKeys: keys,
		Log:      &testLogWriter{t: t},
		WSMCP:    func(string) (wsagent.MCPCommand, error) { return wsagent.MCPCommand{Command: "/unused/ws-mcp"}, nil },
		DaemonMCP: func(string) (wsagent.MCPCommand, error) {
			return wsagent.MCPCommand{}, nil
		},
	})
	t.Cleanup(func() { agents.Close() })

	var dPtr atomic.Pointer[wsdashboard.Daemon]
	// Pass empty root (mirroring cmd/ws-mcp/dashboard.go) so launchRoot resolves
	// from the daemon's active workspace instead of the daemon state dir.
	rt := newCompanionRuntime(dir, "", agents, dPtr.Load, &testLogWriter{t: t})
	rt.resolveLaunch = func(role string) (companionLaunchSpec, bool) {
		return companionLaunchSpec{Backend: wsagent.BackendClaude}, true
	}
	t.Cleanup(rt.Close)

	d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir:            dir,
		Port:           0,
		Log:            &testLogWriter{t: t},
		Agents:         agents,
		WorkspacesFile: wsFile,
	})
	if err != nil {
		t.Fatalf("StartDaemon: %v", err)
	}
	dPtr.Store(d)
	t.Cleanup(func() { d.Close() })

	workID, err := rt.ensureWorkSession(d)
	if err != nil {
		t.Fatalf("ensureWorkSession: %v", err)
	}
	rec, ok := agents.CompanionRecord(workID)
	if !ok {
		t.Fatalf("missing companion-work record %s", workID)
	}
	if rec.Root != wsDir {
		t.Fatalf("companion-work Root=%q, want registered workspace %q (not state dir %q)", rec.Root, wsDir, dir)
	}
}

func TestCompanionWorkApprovalBannerAndPauseEchoEndToEnd(t *testing.T) {
	f := newCompanionFixture(t, "agent")
	workID, err := f.rt.ensureWorkSession(f.daemon)
	if err != nil {
		t.Fatalf("ensureWorkSession: %v", err)
	}
	rec, ok := f.agents.CompanionRecord(workID)
	if !ok || rec.WSKey == "" {
		t.Fatalf("missing companion-work record for %s", workID)
	}
	workKey := rec.WSKey

	// Connect a real local browser bridge tab to /rail/events.
	ep := f.daemon.Endpoint()
	baseHTTP := fmt.Sprintf("http://127.0.0.1:%d", ep.Port)
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/rail/events?bridge=1&tab_id=tab-cw&display=1", ep.Port)
	cfg, err := websocket.NewConfig(wsURL, baseHTTP)
	if err != nil {
		t.Fatalf("websocket.NewConfig: %v", err)
	}
	cfg.Header.Set("Cookie", f.authCookie.Name+"="+f.authCookie.Value)
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("websocket.DialConfig: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	type wsMsg struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	events := make(chan wsMsg, 64)
	go func() {
		defer close(events)
		for {
			var raw string
			if err := websocket.Message.Receive(conn, &raw); err != nil {
				return
			}
			var m wsMsg
			if json.Unmarshal([]byte(raw), &m) == nil && m.Event != "" {
				events <- m
			}
		}
	}()

	waitCompanionUntil(t, 5*time.Second, "tab-cw registered", func() bool {
		for _, tab := range f.daemon.DaemonTabs() {
			if tab.TabID == "tab-cw" {
				return true
			}
		}
		return false
	})

	waitEvent := func(want string) json.RawMessage {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					t.Fatalf("WS closed while waiting for %q", want)
				}
				if ev.Event == want {
					return ev.Data
				}
			case <-timer.C:
				t.Fatalf("timed out waiting for WS event %q", want)
			}
		}
	}

	postReply := func(payload map[string]any) int {
		t.Helper()
		raw, _ := json.Marshal(payload)
		req, err := http.NewRequest(http.MethodPost, baseHTTP+wsdashboard.DaemonBridgeReplyPath, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", baseHTTP)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.AddCookie(f.authCookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	type confirmPush struct {
		RequestID string `json:"request_id"`
		Nonce     string `json:"nonce"`
		TabID     string `json:"tab_id"`
		Tool      string `json:"tool"`
		Summary   string `json:"summary"`
	}

	// 1. Read-only daemon.curl (GET), daemon.tabs, and allowlisted config.list
	// execute immediately with zero confirmation banner and zero pause echo.
	txt, isErr, rpcErr := f.callTool(workKey, "daemon.curl", map[string]any{
		"session_key": workKey,
		"method":      "GET",
		"path":        "/health",
	})
	if isErr || rpcErr != "" || !strings.Contains(txt, `"status":200`) {
		t.Fatalf("companion-work GET /health failed: txt=%q isErr=%v rpcErr=%q", txt, isErr, rpcErr)
	}
	txt, isErr, rpcErr = f.callTool(workKey, "daemon.tabs", map[string]any{"session_key": workKey})
	if isErr || rpcErr != "" || !strings.Contains(txt, "tab-cw") {
		t.Fatalf("companion-work daemon.tabs failed: txt=%q isErr=%v rpcErr=%q", txt, isErr, rpcErr)
	}
	if f.daemon.CompanionPhone().Snapshot().PendingApproval != nil {
		t.Fatal("read-only calls must not set PendingApproval")
	}

	// 2. High-impact lead tool (config.tune) pauses behind 1-click banner +
	// messenger pause echo; foreign/stale request IDs are rejected; Deny returns
	// denied with zero side effect and clears PendingApproval.
	type toolOut struct {
		text   string
		isErr  bool
		rpcErr string
	}
	denyCh := make(chan toolOut, 1)
	go func() {
		txt, isErr, rpcErr := f.callTool(workKey, "config.tune", map[string]any{
			"session_key": workKey,
			"key":         "workflow.lang",
			"scope":       "global",
			"value":       "ko",
		})
		denyCh <- toolOut{txt, isErr, rpcErr}
	}()

	var pushed confirmPush
	if err := json.Unmarshal(waitEvent(wsdashboard.DaemonConfirmRequestEvent), &pushed); err != nil {
		t.Fatalf("unmarshal confirm push: %v", err)
	}
	if pushed.Tool != "config.tune" || pushed.TabID != "tab-cw" || pushed.RequestID == "" {
		t.Fatalf("unexpected confirm push for config.tune: %+v", pushed)
	}
	waitCompanionUntil(t, 3*time.Second, "PendingApproval populated for config.tune", func() bool {
		pa := f.daemon.CompanionPhone().Snapshot().PendingApproval
		return pa != nil && pa.RequestID == pushed.RequestID && pa.Tool == "config.tune"
	})

	// Foreign request_id and wrong nonce are rejected without settling the pause.
	if code := postReply(map[string]any{
		"request_id": "foreign-request-id",
		"nonce":      pushed.Nonce,
		"tab_id":     "tab-cw",
		"decision":   "approve",
	}); code != http.StatusConflict {
		t.Fatalf("foreign request_id status = %d, want 409", code)
	}
	if code := postReply(map[string]any{
		"request_id": pushed.RequestID,
		"nonce":      "wrong-nonce",
		"tab_id":     "tab-cw",
		"decision":   "approve",
	}); code != http.StatusForbidden {
		t.Fatalf("wrong nonce status = %d, want 403", code)
	}
	if pa := f.daemon.CompanionPhone().Snapshot().PendingApproval; pa == nil || pa.RequestID != pushed.RequestID {
		t.Fatalf("rejected foreign/bad-nonce reply cleared PendingApproval prematurely: %+v", pa)
	}

	// Real Deny settles with denied and zero side effects.
	if code := postReply(map[string]any{
		"request_id": pushed.RequestID,
		"nonce":      pushed.Nonce,
		"tab_id":     "tab-cw",
		"decision":   "deny",
	}); code != http.StatusOK {
		t.Fatalf("deny status = %d, want 200", code)
	}
	denyRes := <-denyCh
	if !denyRes.isErr || denyRes.rpcErr != "" || !strings.Contains(denyRes.text, `"error":"denied"`) {
		t.Fatalf("denied config.tune = %+v, want error=denied", denyRes)
	}
	waitCompanionUntil(t, 3*time.Second, "PendingApproval cleared after deny", func() bool {
		return f.daemon.CompanionPhone().Snapshot().PendingApproval == nil
	})
	cfgTxt, cfgErr, _ := f.callTool(workKey, "config.get", map[string]any{"session_key": workKey, "key": "workflow.lang"})
	if cfgErr || strings.Contains(cfgTxt, "ko") {
		t.Fatalf("denied config.tune mutated config: %q", cfgTxt)
	}

	// 3. Approving config.tune executes and persists workflow.lang=ko, then clears PendingApproval.
	approveCh := make(chan toolOut, 1)
	go func() {
		txt, isErr, rpcErr := f.callTool(workKey, "config.tune", map[string]any{
			"session_key": workKey,
			"key":         "workflow.lang",
			"scope":       "global",
			"value":       "ko",
		})
		approveCh <- toolOut{txt, isErr, rpcErr}
	}()
	var pushed2 confirmPush
	if err := json.Unmarshal(waitEvent(wsdashboard.DaemonConfirmRequestEvent), &pushed2); err != nil {
		t.Fatalf("unmarshal confirm push 2: %v", err)
	}
	if code := postReply(map[string]any{
		"request_id": pushed2.RequestID,
		"nonce":      pushed2.Nonce,
		"tab_id":     "tab-cw",
		"decision":   "approve",
	}); code != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", code)
	}
	approveRes := <-approveCh
	if approveRes.isErr || approveRes.rpcErr != "" {
		t.Fatalf("approved config.tune failed: %+v", approveRes)
	}
	waitCompanionUntil(t, 3*time.Second, "PendingApproval cleared after approve", func() bool {
		return f.daemon.CompanionPhone().Snapshot().PendingApproval == nil
	})
	cfgTxt, cfgErr, _ = f.callTool(workKey, "config.get", map[string]any{"session_key": workKey, "key": "workflow.lang"})
	if cfgErr || !strings.Contains(cfgTxt, "ko") {
		t.Fatalf("approved config.tune did not persist workflow.lang=ko: %q", cfgTxt)
	}

	// 4. Mutating ws/daemon.curl (POST) by companion-work pauses with pause echo,
	// returns denied on dismiss, and executes cleanly on approve.
	curlDismissCh := make(chan toolOut, 1)
	go func() {
		txt, isErr, rpcErr := f.callTool(workKey, "daemon.curl", map[string]any{
			"session_key": workKey,
			"method":      "POST",
			"path":        "/health",
		})
		curlDismissCh <- toolOut{txt, isErr, rpcErr}
	}()
	var pushedCurl confirmPush
	if err := json.Unmarshal(waitEvent(wsdashboard.DaemonConfirmRequestEvent), &pushedCurl); err != nil {
		t.Fatalf("unmarshal curl confirm push: %v", err)
	}
	waitCompanionUntil(t, 3*time.Second, "PendingApproval populated for daemon.curl", func() bool {
		pa := f.daemon.CompanionPhone().Snapshot().PendingApproval
		return pa != nil && pa.RequestID == pushedCurl.RequestID && pa.Tool == "daemon.curl"
	})
	if code := postReply(map[string]any{
		"request_id": pushedCurl.RequestID,
		"nonce":      pushedCurl.Nonce,
		"tab_id":     "tab-cw",
		"decision":   "dismiss",
	}); code != http.StatusOK {
		t.Fatalf("dismiss status = %d, want 200", code)
	}
	curlDismissRes := <-curlDismissCh
	if !curlDismissRes.isErr || !strings.Contains(curlDismissRes.text, `"error":"denied"`) {
		t.Fatalf("dismissed companion-work daemon.curl = %+v, want denied", curlDismissRes)
	}
	waitCompanionUntil(t, 3*time.Second, "PendingApproval cleared after curl dismiss", func() bool {
		return f.daemon.CompanionPhone().Snapshot().PendingApproval == nil
	})

	curlApproveCh := make(chan toolOut, 1)
	go func() {
		txt, isErr, rpcErr := f.callTool(workKey, "daemon.curl", map[string]any{
			"session_key": workKey,
			"method":      "POST",
			"path":        "/health",
		})
		curlApproveCh <- toolOut{txt, isErr, rpcErr}
	}()
	var pushedCurl2 confirmPush
	if err := json.Unmarshal(waitEvent(wsdashboard.DaemonConfirmRequestEvent), &pushedCurl2); err != nil {
		t.Fatalf("unmarshal curl confirm push 2: %v", err)
	}
	if code := postReply(map[string]any{
		"request_id": pushedCurl2.RequestID,
		"nonce":      pushedCurl2.Nonce,
		"tab_id":     "tab-cw",
		"decision":   "approve",
	}); code != http.StatusOK {
		t.Fatalf("approve curl status = %d, want 200", code)
	}
	curlApproveRes := <-curlApproveCh
	if curlApproveRes.isErr || curlApproveRes.rpcErr != "" || !strings.Contains(curlApproveRes.text, `"status":405`) {
		t.Fatalf("approved companion-work POST /health = %+v, want HTTP 405 from daemon mux", curlApproveRes)
	}
	waitCompanionUntil(t, 3*time.Second, "PendingApproval cleared after curl approve", func() bool {
		return f.daemon.CompanionPhone().Snapshot().PendingApproval == nil
	})
}

func TestCompanionPickLaunchSpecFollowsBuiltinHarnessOrder(t *testing.T) {
	names := wsagent.BuiltinHarnessNames()
	if len(names) < 2 {
		t.Fatalf("expected at least 2 built-in harnesses, got %v", names)
	}
	second, last := names[1], names[len(names)-1]
	backend := &companionTestBackend{}
	runner := wsagent.NewRunner(wsagent.Options{
		Dir: t.TempDir(),
		Backends: map[string]wsagent.BackendRegistration{
			"aaa-custom": {
				Backend:       backend,
				LeadProfile:   "lead-custom",
				WorkerCapable: true,
				WorkerProfile: wsagent.AgentWorkerProfile("aaa-custom"),
			},
			last: {
				Backend:       backend,
				LeadProfile:   wsagent.LeadProfileFor(last),
				WorkerCapable: true,
				WorkerProfile: wsagent.AgentWorkerProfile(last),
			},
			second: {
				Backend:       backend,
				LeadProfile:   wsagent.LeadProfileFor(second),
				WorkerCapable: true,
				WorkerProfile: wsagent.AgentWorkerProfile(second),
			},
		},
	})
	rt := newCompanionRuntime(t.TempDir(), t.TempDir(), runner, nil, nil)
	spec, ok := rt.pickLaunchSpec(mcp.CompanionRoleBody)
	if !ok {
		t.Fatal("pickLaunchSpec returned ok=false")
	}
	if spec.Backend != second {
		t.Fatalf("pickLaunchSpec picked %q, want %q (earliest available in BuiltinHarnessNames %v)", spec.Backend, second, names)
	}
}
