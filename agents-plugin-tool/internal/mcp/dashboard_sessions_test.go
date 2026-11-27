package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

// dashboardSessionEnv isolates the cache (and so the session registry and the
// daemon directory) and config homes, and returns the daemon directory.
func dashboardSessionEnv(t *testing.T) string {
	t.Helper()
	useLeadProfile(t)
	t.Setenv("WS_RSRC_ROOT", filepath.Join("..", "..", "..", "agents-plugin", "rsrc"))
	t.Setenv("WS_SKILLS_ROOT", filepath.Join(t.TempDir(), "missing-skills"))
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	dir, err := wsdashboard.Dir("")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func dashboardGitRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, root, "ai-docs/_index.md", "# Index\n")
	initGit(t, root)
	return root
}

// dashboardSessionServer builds a server whose launcher listed the ws
// manifest beside the shipped ws dashboard manifest (enabled) or an
// off-by-default copy of it (disabled), and logs a top-level lead session in at root.
func dashboardSessionServer(t *testing.T, root string, enabled bool) (s *Server, key, loginRoot string) {
	t.Helper()
	dashboard := dashboardPackages[0].dashboard
	if !enabled {
		dashboard = dashboardOffManifest(t)
	}
	t.Setenv(envConfigManifests, claudeManifestPath+";"+dashboard)
	s = NewServer(root, "test")
	s.sessionHarness = "claude"
	key, loginRoot = parseLoginResponse(t, callLogin(t, s, 900000, root, nil))
	return s, key, loginRoot
}

// dashCall runs one tool call through the request handler without a serve
// loop, whose end would mark the session ended.
func dashCall(t *testing.T, s *Server, name string, args map[string]any) string {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	resp := s.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	line, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

func readRegistration(t *testing.T, dir, key string) (wsdashboard.Registration, bool) {
	t.Helper()
	reg, err := wsdashboard.ReadRegistration(dir, wsmailbox.ReplyID(key))
	if os.IsNotExist(err) {
		return wsdashboard.Registration{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return reg, true
}

// fakeDaemon records the nudges it receives and answers with status, or
// blocks every request until the test ends when hang is set.
type fakeDaemon struct {
	mu           sync.Mutex
	nudges       []wsdashboard.NudgeBody
	agentRecords map[string]bool
	pruneCalls   int
}

func (f *fakeDaemon) received() []wsdashboard.NudgeBody {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]wsdashboard.NudgeBody(nil), f.nudges...)
}

func startFakeDaemon(t *testing.T, dir string, status int, hang bool) *fakeDaemon {
	t.Helper()
	fake := &fakeDaemon{agentRecords: map[string]bool{}}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hang {
			<-release
			return
		}
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"service":"ws-dashboard","pid":1,"version":"test"}`))
			return
		case wsdashboard.MetaUnregisterCheckPath:
			var req struct {
				ReplyID string `json:"reply_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			fake.mu.Lock()
			has := fake.agentRecords[req.ReplyID]
			fake.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]bool{"has_agent_record": has})
			return
		case wsdashboard.MetaPrunePath:
			fake.mu.Lock()
			fake.pruneCalls++
			fake.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"pruned":0}`))
			return
		}
		var body wsdashboard.NudgeBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		fake.mu.Lock()
		fake.nudges = append(fake.nudges, body)
		fake.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	endpoint := fmt.Sprintf(`{"schema_version": 1, "port": %d, "pid": 1, "version": "test", "token": "t"}`, port)
	if err := os.WriteFile(filepath.Join(dir, "endpoint.json"), []byte(endpoint), 0o600); err != nil {
		t.Fatal(err)
	}
	return fake
}

func waitForNudge(t *testing.T, fake *fakeDaemon, replyID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, n := range fake.received() {
			if n.ReplyID == replyID {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no nudge for %s; received %+v", replyID, fake.received())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDashboardRegistersATopLevelLeadSessionWithTheKeyOn(t *testing.T) {
	dir := dashboardSessionEnv(t)
	root := dashboardGitRoot(t)
	s, key, loginRoot := dashboardSessionServer(t, root, true)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s.dashboard.now = func() time.Time { return now }

	if _, ok := readRegistration(t, dir, key); ok {
		t.Fatal("registered before any keyed call")
	}
	dashCall(t, s, "todo.list", map[string]any{"session_key": key})
	reg, ok := readRegistration(t, dir, key)
	if !ok {
		t.Fatal("keyed call did not register the session")
	}
	if reg.ReplyID != wsmailbox.ReplyID(key) || reg.Project != loginRoot || reg.Harness != "claude" || reg.HeartbeatAt != dashboardStamp(now) || !reg.Live(now) {
		t.Fatalf("registration = %+v", reg)
	}
	if got, err := filepath.EvalSymlinks(reg.GitCommonDir); err != nil || got != filepath.Join(loginRoot, ".git") {
		t.Errorf("registration git common dir = %q, want %s", reg.GitCommonDir, filepath.Join(loginRoot, ".git"))
	}
	raw, err := os.ReadFile(filepath.Join(wsdashboard.SessionDir(dir, reg.ReplyID), "registration.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) {
		t.Fatalf("registration file carries the session key: %s", raw)
	}

	// A spawned (child) session is never registered.
	child, _ := parseLoginResponse(t, callLogin(t, s, 900001, root, map[string]any{"parent_session_key": key}))
	dashCall(t, s, "todo.list", map[string]any{"session_key": child})
	if _, ok := readRegistration(t, dir, child); ok {
		t.Fatal("a child session was registered")
	}
}

func TestDashboardParentBoundLeadKeepsWorkflowAccessWithoutRegistration(t *testing.T) {
	dir := dashboardSessionEnv(t)
	root := dashboardGitRoot(t)
	s, parent, _ := dashboardSessionServer(t, root, true)
	s.sessionHarness = "pi"

	for _, capability := range []string{"", "lead", "delegate", "leaf"} {
		t.Run("capability="+capability, func(t *testing.T) {
			args := map[string]any{"parent_session_key": parent}
			if capability != "" {
				args["capability"] = capability
			}
			child, _ := parseLoginResponse(t, callLogin(t, s, 900001, root, args))
			// Explicit and bridge-default-filled keys have the same wire shape;
			// child-key-bootstrap.test.ts exercises the omitted-key adapter half.
			if resp := dashCall(t, s, "todo.list", map[string]any{"session_key": child}); isToolError(resp) {
				t.Fatalf("keyed call: %s", resp)
			}
			resp := dashCall(t, s, "workflow_manual", map[string]any{"session_key": child})
			wantAccess := capability == "" || capability == "lead"
			if isToolError(resp) == wantAccess {
				t.Fatalf("workflow_manual access=%v: %s", wantAccess, resp)
			}
			if _, ok := readRegistration(t, dir, child); ok {
				t.Fatal("a parent-bound child registered a dashboard root row")
			}
		})
	}
	if resp := dashCall(t, s, "workflow_manual", map[string]any{"session_key": parent}); isToolError(resp) {
		t.Fatalf("top-level workflow_manual: %s", resp)
	}
	if _, ok := readRegistration(t, dir, parent); !ok {
		t.Fatal("the top-level lead did not register")
	}
}

func TestDashboardKeyOffAndKeyOnServersOnOneRoot(t *testing.T) {
	dir := dashboardSessionEnv(t)
	fake := startFakeDaemon(t, dir, http.StatusNoContent, false)
	root := dashboardGitRoot(t)
	sOff, keyOff, _ := dashboardSessionServer(t, root, false)
	_, keyOn, loginRoot := dashboardSessionServer(t, root, true)
	sOn := newTestBoardOwner(t, root, "test")
	sOn.sessionHarness = "claude"

	for _, c := range []struct {
		s   *Server
		key string
	}{{sOff, keyOff}, {sOn, keyOn}} {
		resp := dashCall(t, c.s, "todo.add", map[string]any{"session_key": c.key, "key": "step", "title": "a step"})
		if isToolError(resp) {
			t.Fatalf("todo.add: %s", resp)
		}
	}
	waitForNudge(t, fake, wsmailbox.ReplyID(keyOn))
	// The key-off session's calls queued nothing; the key-on nudges arrived,
	// so a key-off nudge sent at the same time would have too.
	time.Sleep(100 * time.Millisecond)
	for _, n := range fake.received() {
		if n.ReplyID != wsmailbox.ReplyID(keyOn) || n.Project != loginRoot {
			t.Fatalf("unexpected nudge %+v", n)
		}
	}
	if _, ok := readRegistration(t, dir, keyOff); ok {
		t.Fatal("the key-off session was registered")
	}

	// The daemon's index, read through the real session source, lists only
	// the key-on session, even though it has no board yet.
	idx := wsdashboard.NewIndex(wsdashboard.IndexOptions{Dir: dir, Source: dashboardPageSource(t, nil, sOn.sessions.boards)})
	idx.Refresh()
	sessions := idx.Sessions()
	if len(sessions) != 1 || sessions[0].ReplyID != wsmailbox.ReplyID(keyOn) || sessions[0].HasBoard || !sessions[0].Live {
		t.Fatalf("index = %+v", sessions)
	}

	// A board write is seen on the next re-read of that session.
	resp := dashCall(t, sOn, "whiteboard.update", map[string]any{"session_key": keyOn, "commands": `<wb:topic name="t"><wb:add class="technical"><wb:title>a question</wb:title></wb:add></wb:topic>`})
	if isToolError(resp) {
		t.Fatalf("whiteboard.update: %s", resp)
	}
	if !idx.Nudge(wsmailbox.ReplyID(keyOn), loginRoot) {
		t.Fatal("nudge for the key-on session was ignored")
	}
	idx.Settle()
	if s, _ := idx.Session(wsmailbox.ReplyID(keyOn)); !s.HasBoard || s.Rev != 2 {
		t.Fatalf("session after board write = %+v", s)
	}
}

func TestDashboardNudgeFailureLeavesTheToolResultUnchanged(t *testing.T) {
	old := dashboardNudgeTimeout
	dashboardNudgeTimeout = 3 * time.Second
	t.Cleanup(func() { dashboardNudgeTimeout = old })

	run := func(t *testing.T, mode string) string {
		dir := dashboardSessionEnv(t)
		var s *Server
		// Register before the fake daemon: release its hung requests before
		// joining the sender, but keep cache/config homes until it has exited.
		t.Cleanup(func() {
			if s != nil && s.dashboard.nudgesDone != nil {
				select {
				case <-s.dashboard.nudgesDone:
				case <-time.After(5 * time.Second):
					t.Error("nudge sender did not exit before subtest cleanup")
				}
			}
		})
		var fake *fakeDaemon
		switch mode {
		case "ok":
			fake = startFakeDaemon(t, dir, http.StatusNoContent, false)
		case "error":
			fake = startFakeDaemon(t, dir, http.StatusInternalServerError, false)
		case "hung":
			startFakeDaemon(t, dir, 0, true)
		}
		root := dashboardGitRoot(t)
		s, key, loginRoot := dashboardSessionServer(t, root, true)
		// Close stops queueing but drains asynchronously; the earlier cleanup
		// joins it before Setenv can expose the next subtest's daemon.
		t.Cleanup(s.Close)
		started := time.Now()
		resp := dashCall(t, s, "todo.add", map[string]any{"session_key": key, "key": "step", "title": "a step"})
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("%s: todo.add took %v; the nudge must not delay it", mode, elapsed)
		}
		if isToolError(resp) {
			t.Fatalf("%s: todo.add failed: %s", mode, resp)
		}
		if fake != nil {
			waitForNudge(t, fake, wsmailbox.ReplyID(key))
			for _, n := range fake.received() {
				if n.Project != loginRoot {
					t.Fatalf("nudge project = %q, want %q", n.Project, loginRoot)
				}
			}
		}
		return toolText(t, resp)
	}
	var want string
	t.Run("no daemon", func(t *testing.T) { want = run(t, "none") })
	for _, mode := range []string{"ok", "error", "hung"} {
		t.Run(mode, func(t *testing.T) {
			if got := run(t, mode); got != want {
				t.Fatalf("result with a %s daemon:\n%s\nwant:\n%s", mode, got, want)
			}
		})
	}
}

func TestDashboardSessionScopeTuneOffUnregisters(t *testing.T) {
	dir := dashboardSessionEnv(t)
	fake := startFakeDaemon(t, dir, http.StatusNoContent, false)
	root := dashboardGitRoot(t)
	s, key, _ := dashboardSessionServer(t, root, true)
	dashCall(t, s, "todo.list", map[string]any{"session_key": key})
	if _, ok := readRegistration(t, dir, key); !ok {
		t.Fatal("not registered with the key on")
	}
	waitForNudge(t, fake, wsmailbox.ReplyID(key))

	resp := dashCall(t, s, "config.tune", map[string]any{"session_key": key, "key": DashboardEnabledKey, "value": "false", "scope": "session"})
	if isToolError(resp) {
		t.Fatalf("config.tune: %s", resp)
	}
	if _, ok := readRegistration(t, dir, key); ok {
		t.Fatal("a session-scope tune off left the registration")
	}
	before := len(fake.received())
	dashCall(t, s, "todo.add", map[string]any{"session_key": key, "key": "step", "title": "a step"})
	time.Sleep(100 * time.Millisecond)
	if after := len(fake.received()); after != before {
		t.Fatalf("a key-off session sent %d nudges", after-before)
	}
	if _, ok := readRegistration(t, dir, key); ok {
		t.Fatal("a key-off session re-registered")
	}
}

func TestDashboardHeartbeatIdleCutoffAndEnd(t *testing.T) {
	dir := dashboardSessionEnv(t)
	root := dashboardGitRoot(t)
	s, key, _ := dashboardSessionServer(t, root, true)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s.dashboard.now = func() time.Time { return now }
	dashCall(t, s, "todo.list", map[string]any{"session_key": key})
	registeredAt := dashboardStamp(now)

	now = now.Add(3 * time.Minute)
	s.dashboardHeartbeat()
	reg, _ := readRegistration(t, dir, key)
	if reg.HeartbeatAt != dashboardStamp(now) || reg.RegisteredAt != registeredAt {
		t.Fatalf("after a heartbeat: %+v", reg)
	}

	// Past the idle cutoff with no keyed call, the heartbeat stops and the
	// session reads as ended once its last heartbeat ages out.
	now = now.Add(dashboardIdleCutoff)
	s.dashboardHeartbeat()
	reg, _ = readRegistration(t, dir, key)
	if reg.HeartbeatAt == dashboardStamp(now) || reg.Live(now) {
		t.Fatalf("idle session still heartbeating: %+v", reg)
	}
	// Its next keyed call makes it live again.
	dashCall(t, s, "todo.list", map[string]any{"session_key": key})
	reg, _ = readRegistration(t, dir, key)
	if reg.HeartbeatAt != dashboardStamp(now) || !reg.Live(now) || reg.RegisteredAt != registeredAt {
		t.Fatalf("after resuming: %+v", reg)
	}

	// The end of the serve loop marks it ended; a later keyed call (a host
	// that reconnects) clears the mark.
	s.dashboardEnd()
	reg, _ = readRegistration(t, dir, key)
	if reg.EndedAt != dashboardStamp(now) || reg.Live(now) {
		t.Fatalf("after end: %+v", reg)
	}
	dashCall(t, s, "todo.list", map[string]any{"session_key": key})
	reg, _ = readRegistration(t, dir, key)
	if reg.EndedAt != "" || !reg.Live(now) {
		t.Fatalf("after a call past the end: %+v", reg)
	}
}

func TestDashboardFirstRegistrationPrunesExpiredEntries(t *testing.T) {
	t.Run("via daemon meta prune", func(t *testing.T) {
		dir := dashboardSessionEnv(t)
		root := dashboardGitRoot(t)
		daemon, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: dir, Home: root})
		if err != nil {
			t.Fatal(err)
		}
		defer daemon.Close()
		old := time.Now().Add(-wsdashboard.RegistryRetention - time.Hour).UTC().Format(time.RFC3339)
		if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{ReplyID: "expired", Project: root, HeartbeatAt: old}); err != nil {
			t.Fatal(err)
		}
		s, key, _ := dashboardSessionServer(t, root, true)
		defer s.Close()
		dashCall(t, s, "todo.list", map[string]any{"session_key": key})
		deadline := time.Now().Add(5 * time.Second)
		for {
			_, err := os.Stat(wsdashboard.SessionDir(dir, "expired"))
			if os.IsNotExist(err) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("expired registration kept after daemon /meta/prune: %v", err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if _, ok := readRegistration(t, dir, key); !ok {
			t.Fatal("not registered")
		}
	})

	t.Run("without daemon does not prune directly", func(t *testing.T) {
		dir := dashboardSessionEnv(t)
		root := dashboardGitRoot(t)
		old := time.Now().Add(-wsdashboard.RegistryRetention - time.Hour).UTC().Format(time.RFC3339)
		if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{ReplyID: "expired", Project: root, HeartbeatAt: old}); err != nil {
			t.Fatal(err)
		}
		s, key, _ := dashboardSessionServer(t, root, true)
		defer s.Close()
		dashCall(t, s, "todo.list", map[string]any{"session_key": key})
		if _, err := os.Stat(wsdashboard.SessionDir(dir, "expired")); err != nil {
			t.Fatalf("out-of-daemon server must not delete session dir without daemon confirmation: %v", err)
		}
		if _, ok := readRegistration(t, dir, key); !ok {
			t.Fatal("not registered")
		}
	})
}

func TestDashboardUnregisterRetriesAfterLookupError(t *testing.T) {
	t.Run("retries on next dashboardTouch", func(t *testing.T) {
		dir := dashboardSessionEnv(t)
		root := dashboardGitRoot(t)
		s, key, _ := dashboardSessionServer(t, root, true)
		defer s.Close()

		dashCall(t, s, "todo.list", map[string]any{"session_key": key})
		if _, ok := readRegistration(t, dir, key); !ok {
			t.Fatal("not registered with key on")
		}

		// Tune dashboard off while no daemon is running -> fail-closed deferral
		// leaves registration.json on disk and retains unregisterPending.
		resp := dashCall(t, s, "config.tune", map[string]any{"session_key": key, "key": DashboardEnabledKey, "value": "false", "scope": "session"})
		if isToolError(resp) {
			t.Fatalf("config.tune off: %s", resp)
		}
		if _, ok := readRegistration(t, dir, key); !ok {
			t.Fatal("registration removed despite daemon unavailable")
		}

		// Once daemon recovers, the next touch retries and removes registration.json.
		startFakeDaemon(t, dir, http.StatusNoContent, false)
		dashCall(t, s, "todo.list", map[string]any{"session_key": key})
		if _, ok := readRegistration(t, dir, key); ok {
			t.Fatal("registration remained after daemon recovered and touch retried")
		}
	})

	t.Run("retries on dashboardHeartbeat", func(t *testing.T) {
		dir := dashboardSessionEnv(t)
		root := dashboardGitRoot(t)
		s, key, _ := dashboardSessionServer(t, root, true)
		defer s.Close()

		dashCall(t, s, "todo.list", map[string]any{"session_key": key})
		if _, ok := readRegistration(t, dir, key); !ok {
			t.Fatal("not registered with key on")
		}

		resp := dashCall(t, s, "config.tune", map[string]any{"session_key": key, "key": DashboardEnabledKey, "value": "false", "scope": "session"})
		if isToolError(resp) {
			t.Fatalf("config.tune off: %s", resp)
		}
		if _, ok := readRegistration(t, dir, key); !ok {
			t.Fatal("registration removed despite daemon unavailable")
		}

		// Daemon recovers without another tool call; heartbeat retries unregister.
		startFakeDaemon(t, dir, http.StatusNoContent, false)
		s.dashboardHeartbeat()
		if _, ok := readRegistration(t, dir, key); ok {
			t.Fatal("registration remained after daemon recovered and heartbeat retried")
		}
	})
}

func TestDashboardUnregisterDoesNotHoldLockOrRaceReenable(t *testing.T) {
	dir := dashboardSessionEnv(t)
	root := dashboardGitRoot(t)

	inLookup := make(chan struct{})
	releaseLookup := make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	unlock := func() { releaseOnce.Do(func() { close(releaseLookup) }) }
	defer unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"service":"ws-dashboard","pid":1,"version":"test"}`))
			return
		case wsdashboard.MetaUnregisterCheckPath:
			enterOnce.Do(func() { close(inLookup) })
			<-releaseLookup
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"has_agent_record":false}`))
			return
		case wsdashboard.MetaPrunePath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"pruned":0}`))
			return
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(func() {
		unlock()
		srv.Close()
	})
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	endpoint := fmt.Sprintf(`{"schema_version": 1, "port": %d, "pid": 1, "version": "test", "token": "t"}`, port)
	if err := os.WriteFile(filepath.Join(dir, "endpoint.json"), []byte(endpoint), 0o600); err != nil {
		t.Fatal(err)
	}

	s, key1, loginRoot := dashboardSessionServer(t, root, true)
	defer s.Close()
	key2, _ := parseLoginResponse(t, callLogin(t, s, 900001, root, nil))

	dashCall(t, s, "todo.list", map[string]any{"session_key": key1})
	dashCall(t, s, "todo.list", map[string]any{"session_key": key2})
	if _, ok := readRegistration(t, dir, key1); !ok {
		t.Fatal("key1 not registered")
	}

	// Trigger unregister on key1; its /meta/unregister-check stalls in flight.
	tuneOffDone := make(chan string, 1)
	go func() {
		tuneOffDone <- dashCall(t, s, "config.tune", map[string]any{"session_key": key1, "key": DashboardEnabledKey, "value": "false", "scope": "session"})
	}()

	select {
	case <-inLookup:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for unregister-check to enter handler")
	}

	// Concurrent dashboardPageURL and dashboardTouch for another session must not
	// block behind the stalled unregister lookup.
	concurrentDone := make(chan struct{})
	go func() {
		_, _ = s.dashboardPageURL(key2)
		s.dashboardTouch(key2, loginRoot, false)
		close(concurrentDone)
	}()
	select {
	case <-concurrentDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("dashboardPageURL/dashboardTouch blocked while unregister lookup was in flight")
	}

	// Re-enabling key1 while the unregister lookup is still in flight must also
	// succeed immediately and its newly written registration must survive when
	// the stale unregister finishes.
	reenableDone := make(chan string, 1)
	go func() {
		reenableDone <- dashCall(t, s, "config.tune", map[string]any{"session_key": key1, "key": DashboardEnabledKey, "value": "true", "scope": "session"})
	}()
	select {
	case resp := <-reenableDone:
		if isToolError(resp) {
			t.Fatalf("re-enable config.tune failed: %s", resp)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("re-enabling key1 blocked while unregister lookup was in flight")
	}

	unlock()
	if resp := <-tuneOffDone; isToolError(resp) {
		t.Fatalf("initial tune off failed: %s", resp)
	}
	if _, ok := readRegistration(t, dir, key1); !ok {
		t.Fatal("stale in-flight unregister deleted registration after concurrent re-enable")
	}
}
