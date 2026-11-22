package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func TestConfigTuneKeepAwakeDuringTurns(t *testing.T) {
	setupMailboxTestEnv(t)
	root := t.TempDir()
	initGit(t, root)
	s := NewServer(root, "test")
	defer s.Close()
	key := mailboxLogin(t, s, 1, root)
	item := wsconfig.ItemAgentKeepAwakeDuringTurns

	if out := callToolWithKey(t, s, 2, key, "config.get", map[string]any{"key": item}); !strings.Contains(out, "false") || !strings.Contains(out, "builtin") {
		t.Fatalf("builtin = %q", out)
	}
	for i, tc := range []struct {
		value, want string
		ok          bool
	}{
		{"TRUE", "agent.keep_awake_during_turns: true [scope:global]", true},
		{"on", "true or false", false},
		{"false", "agent.keep_awake_during_turns: false [scope:global]", true},
		{"true", "agent.keep_awake_during_turns: true [scope:global]", true},
	} {
		out := callToolWithKey(t, s, 10+i, key, "config.tune", map[string]any{"key": item, "value": tc.value})
		if !strings.Contains(out, tc.want) || strings.HasPrefix(out, "config.tune:") == tc.ok {
			t.Fatalf("config.tune %q = %q, want ok %v with %q", tc.value, out, tc.ok, tc.want)
		}
	}
	overrides, err := wsconfig.GlobalOverrides(wsconfig.Options{})
	if err != nil || overrides[item] != "true" {
		t.Fatalf("stored = %v, %v; want the string-encoded true in the global overrides", overrides, err)
	}
	if out := callToolWithKey(t, s, 20, key, "config.tune", map[string]any{"key": item, "value": "false", "scope": "project"}); !strings.Contains(out, "global") || !strings.HasPrefix(out, "config.tune:") {
		t.Fatalf("a project write of the global-only key is refused: %q", out)
	}
	if out := callToolWithKey(t, s, 21, key, "config.tune", map[string]any{"key": item, "reset": true}); !strings.Contains(out, "agent.keep_awake_during_turns: false [scope:builtin]") {
		t.Fatalf("reset = %q", out)
	}
	entry, ok := resolveConfigEntryForKey(item)
	if !ok || !entry.RequiresLeadAuthority || !entry.GlobalOnly() || !entry.NoAgentVisible || entry.HarnessApplicable || len(entry.SelectorFields) != 0 {
		t.Fatalf("entry = %+v, %v", entry, ok)
	}
	list := callToolWithKey(t, s, 22, key, "config.list", map[string]any{})
	if !strings.Contains(list, item) || !strings.Contains(list, "idle system sleep") || !strings.Contains(list, "macOS") || !strings.Contains(list, "Windows") {
		t.Fatalf("config.list does not describe the knob: %s", list)
	}

	// The settings catalog projects a global-only boolean defaulting to
	// false, with no selectors.
	catalog, err := DashboardConfigCatalog("ws")
	if err != nil {
		t.Fatal(err)
	}
	got := catalog.Entry(item, nil, wsconfig.ScopeGlobal, wsconfigservice.Layers{})
	if got.Type != "boolean" || got.Default != false || len(got.Selectors) != 0 {
		t.Fatalf("catalog entry = %+v", got)
	}
	for _, scope := range []wsconfig.Scope{wsconfig.ScopeProject, wsconfig.ScopeRepo, wsconfig.ScopeSession} {
		if _, err := catalog.Validate(scope, wsconfigservice.Operation{Key: item, Action: "set", Value: true}); err != wsconfigservice.ErrScope {
			t.Errorf("set in %s: %v, want a scope refusal", scope, err)
		}
	}
	mutation, err := catalog.Validate(wsconfig.ScopeGlobal, wsconfigservice.Operation{Key: item, Action: "set", Value: true})
	if err != nil || mutation.Scalar != "true" {
		t.Fatalf("global set = %+v, %v", mutation, err)
	}
}

// keepAwakeAgents is the daemon's agent host in the notification test: it
// records each global value its reloads read, and whether the daemon's
// endpoint was already published at its first reload.
type keepAwakeAgents struct {
	opts wsconfig.Options
	dir  string

	mu             sync.Mutex
	reads          []string
	endpointAtLoad bool
}

func (a *keepAwakeAgents) Handler() http.Handler               { return http.NotFoundHandler() }
func (a *keepAwakeAgents) Start(func(replyID, project string)) {}
func (a *keepAwakeAgents) InboxChanged(string)                 {}
func (a *keepAwakeAgents) Close()                              {}

func (a *keepAwakeAgents) ReloadGlobalConfig() {
	overrides, _ := wsconfig.GlobalOverrides(a.opts)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.reads) == 0 {
		_, err := wsdashboard.ReadEndpoint(a.dir)
		a.endpointAtLoad = err == nil
	}
	a.reads = append(a.reads, overrides[wsconfig.ItemAgentKeepAwakeDuringTurns])
}

func (a *keepAwakeAgents) last() (string, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.reads) == 0 {
		return "<none>", 0
	}
	return a.reads[len(a.reads)-1], len(a.reads)
}

// TestKeepAwakeConfigNotifiesTheDaemon: a config.tune set and reset from a
// server outside the daemon, and a settings-page set and reset, each make
// the daemon's agent host reread its own global layer; a disable committed
// while no endpoint was discoverable is what the daemon's startup read
// observes.
func TestKeepAwakeConfigNotifiesTheDaemon(t *testing.T) {
	setupMailboxTestEnv(t)
	root := t.TempDir()
	initGit(t, root)
	s := NewServer(root, "test")
	defer s.Close()
	key := mailboxLogin(t, s, 1, root)
	item := wsconfig.ItemAgentKeepAwakeDuringTurns
	id := 10
	tune := func(args map[string]any) {
		t.Helper()
		id++
		args["key"] = item
		if out := callToolWithKey(t, s, id, key, "config.tune", args); strings.HasPrefix(out, "config.tune:") {
			t.Fatalf("config.tune %v = %q", args, out)
		}
	}
	var agents *keepAwakeAgents
	waitRead := func(want string, after int) int {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			value, n := agents.last()
			if n > after && value == want {
				return n
			}
			if time.Now().After(deadline) {
				t.Fatalf("the daemon's reload read %q (%d reads), want %q after read %d", value, n, want, after)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// An earlier session enabled it; a disable is committed while no
	// daemon endpoint is discoverable, so its notification goes nowhere.
	tune(map[string]any{"value": "true"})
	tune(map[string]any{"value": "false"})

	opts := wsconfig.Options{}
	dir, err := wsdashboard.Dir("")
	if err != nil {
		t.Fatal(err)
	}
	agents = &keepAwakeAgents{opts: opts, dir: dir}
	backend, err := DashboardConfigBackend("ws", opts, NewDashboardSessionSource(), nil)
	if err != nil {
		t.Fatal(err)
	}
	daemon, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: dir, Home: t.TempDir(), Config: backend, Agents: agents})
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	n := waitRead("false", 0)
	agents.mu.Lock()
	published := agents.endpointAtLoad
	agents.mu.Unlock()
	if !published {
		t.Fatal("the startup read ran before the endpoint was published")
	}

	tune(map[string]any{"value": "true"})
	n = waitRead("true", n)
	tune(map[string]any{"reset": true})
	n = waitRead("", n)

	base := fmt.Sprintf("http://127.0.0.1:%d", daemon.Endpoint().Port)
	cookie := dashboardCookieForTest(t, daemon)
	mutate := func(action, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, base+"/api/config/"+action, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:12345"
		req.AddCookie(cookie)
		req.Header.Set("Origin", base)
		out := httptest.NewRecorder()
		daemon.Handler().ServeHTTP(out, req)
		if out.Code != http.StatusOK {
			t.Fatalf("settings %s = %d %s", action, out.Code, out.Body.String())
		}
	}
	mutate("set", `{"scope":"global","key":"agent.keep_awake_during_turns","value":true}`)
	n = waitRead("true", n)
	mutate("reset", `{"scope":"global","key":"agent.keep_awake_during_turns"}`)
	waitRead("", n)
}
