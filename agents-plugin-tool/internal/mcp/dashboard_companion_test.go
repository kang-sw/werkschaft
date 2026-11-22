package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"golang.org/x/net/websocket"
)

var companionKeys = []string{
	wsdashboard.CompanionEnabledKey, wsdashboard.CompanionDirectorKey, wsdashboard.CompanionPackKey,
	wsdashboard.CompanionPresetKey, wsdashboard.CompanionAgentCooldownKey, wsdashboard.CompanionAgentHourlyCapKey,
}

func TestCompanionConfigTypedValues(t *testing.T) {
	catalog, err := DashboardConfigCatalog("ws")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range companionKeys {
		if !wsdashboard.IsCompanionConfigKey(key) || !wsconfig.GlobalOnly(key) {
			t.Errorf("%s is not a companion key registered global-only", key)
		}
	}
	defaults := wsdashboard.CompanionConfig{Director: "rules", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 30, AgentHourlyCap: 20}
	for _, tc := range []struct {
		name, overrides string
		want            wsdashboard.CompanionConfig
	}{
		{"unset", `{}`, defaults},
		{"valid", `{"overrides":{"dashboard.companion_enabled":"true","dashboard.companion_director":"agent",` +
			`"dashboard.companion_agent_cooldown_seconds":"45","dashboard.companion_agent_hourly_cap":"120"}}`,
			wsdashboard.CompanionConfig{Enabled: true, Director: "agent", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 45, AgentHourlyCap: 120}},
		{"bounds", `{"overrides":{"dashboard.companion_agent_cooldown_seconds":"10","dashboard.companion_agent_hourly_cap":"1"}}`,
			wsdashboard.CompanionConfig{Director: "rules", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 10, AgentHourlyCap: 1}},
		{"invalid stored values fall back to defaults", `{"overrides":{"dashboard.companion_enabled":"maybe",` +
			`"dashboard.companion_director":"sidecar","dashboard.companion_agent_cooldown_seconds":"9",` +
			`"dashboard.companion_agent_hourly_cap":"121"}}`, defaults},
		{"removed and non-numeric values fall back to defaults", `{"overrides":{"dashboard.companion_enabled":"visual",` +
			`"dashboard.companion_director":"lead-tool","dashboard.companion_agent_cooldown_seconds":"soon",` +
			`"dashboard.companion_agent_hourly_cap":""}}`, defaults},
		{"one invalid value leaves its neighbors", `{"overrides":{"dashboard.companion_enabled":"true","dashboard.companion_agent_hourly_cap":"0"}}`,
			wsdashboard.CompanionConfig{Enabled: true, Director: "rules", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 30, AgentHourlyCap: 20}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := wsconfig.Options{CacheHome: t.TempDir(), ConfigHome: t.TempDir()}
			path, err := wsconfig.GlobalPath(opts)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Dir(path), filepath.Base(path), tc.overrides)
			got, err := dashboardCompanionConfig(catalog, opts)
			if err != nil || got != tc.want {
				t.Fatalf("config = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestCompanionGlobalOnly(t *testing.T) {
	s := dashboardServer(t, dashboardPackages[0].dashboard)
	key, err := s.sessions.mint(s.root, roleLead, "")
	if err != nil {
		t.Fatal(err)
	}
	opts := s.configOptions()
	opts.RepoRoot = s.root
	project, err := wsconfig.Path(opts)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := wsconfig.RepoPath(opts)
	enabled := wsdashboard.CompanionEnabledKey
	for _, path := range []string{project, repo} {
		mustWrite(t, filepath.Dir(path), filepath.Base(path), `{"overrides":{"dashboard.companion_enabled":"true","dashboard.companion_agent_hourly_cap":"99"}}`)
	}
	if err := s.sessions.setOverride(key, enabled, "true"); err != nil {
		t.Fatal(err)
	}
	catalog, err := DashboardConfigCatalog("wsflow")
	if err != nil {
		t.Fatal(err)
	}
	tune := wsconfig.NewResolver(opts, nil, nil, nil)
	if len(companionKeys) != 6 {
		t.Fatalf("companion keys = %v, want the six reshaped keys", companionKeys)
	}
	for _, scope := range []wsconfig.Scope{wsconfig.ScopeProject, wsconfig.ScopeRepo, wsconfig.ScopeSession} {
		for _, item := range companionKeys {
			if !wsconfig.GlobalOnly(item) {
				t.Fatalf("%s is not registered global-only", item)
			}
			for _, action := range []string{"set", "reset"} {
				op := wsconfigservice.Operation{Key: item, Action: action}
				if action == "set" {
					op.Value = "ignored"
				}
				if _, err := catalog.Validate(scope, op); err != wsconfigservice.ErrScope {
					t.Errorf("%s %s in %s: %v, want scope refusal", item, action, scope, err)
				}
			}
			target := wsconfig.SetOptions{ExplicitScope: scope, SessionKey: key}
			if err := tune.Set(item, "ignored", target); err == nil || !strings.Contains(err.Error(), "global-only") {
				t.Errorf("%s set in %s: %v, want scope refusal", item, scope, err)
			}
			if err := tune.Unset(item, target); err == nil || !strings.Contains(err.Error(), "global-only") {
				t.Errorf("%s reset in %s: %v, want scope refusal", item, scope, err)
			}
		}
	}
	for _, want := range []bool{false, true} {
		got, err := s.resolveConfigGet(key, enabled)
		if err != nil || got.Value != want {
			t.Fatalf("resolved enabled = %v, %v; want %v", got.Value, err, want)
		}
		shell, err := dashboardCompanionConfig(catalog, opts)
		if err != nil || shell.Enabled != want || shell.AgentHourlyCap != 20 {
			t.Fatalf("shell = %+v, %v; want enabled %v and the default hourly cap 20 (project, repo, and session layers must not apply)", shell, err, want)
		}
		if !want {
			if err := tune.Set(enabled, "true", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tune.Set(wsdashboard.CompanionPackKey, "../invalid", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if got, err := dashboardCompanionConfig(catalog, opts); err != nil || got.Pack != "default" {
		t.Fatalf("invalid persisted pack resolved to %q, %v", got.Pack, err)
	}
	path, err := wsconfig.GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := tune.Set(enabled, "true", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if after, err := os.Stat(path); err != nil || !os.SameFile(before, after) {
		t.Fatalf("unchanged tune replaced config: %v", err)
	}
}

func TestCompanionStdioHandoff(t *testing.T) {
	if root := os.Getenv("WS_TEST_COMPANION_ROOT"); root != "" {
		t.Setenv("WS_CACHE_HOME", os.Getenv("WS_TEST_COMPANION_CACHE"))
		t.Setenv("WS_CONFIG_HOME", os.Getenv("WS_TEST_COMPANION_CONFIG"))
		t.Setenv(envConfigManifests, dashboardPackages[0].dashboard)
		s := NewServer(root, "test")
		defer s.Close()
		if err := s.ServeStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
			t.Fatal(err)
		}
		return
	}
	opts := wsconfig.Options{CacheHome: t.TempDir(), ConfigHome: t.TempDir()}
	root := t.TempDir()
	dir, err := wsdashboard.Dir(opts.CacheHome)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := DashboardConfigBackend("ws", opts, NewDashboardSessionSource(), nil)
	if err != nil {
		t.Fatal(err)
	}
	daemon, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: dir, Home: t.TempDir(), Config: backend})
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	base := fmt.Sprintf("http://127.0.0.1:%d", daemon.Endpoint().Port)
	cookie := dashboardCookieForTest(t, daemon)
	open := func() *websocket.Conn {
		t.Helper()
		cfg, err := websocket.NewConfig("ws"+strings.TrimPrefix(base, "http")+"/rail/events", base)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Header.Set("Cookie", cookie.String())
		conn, err := websocket.DialConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	want := wsdashboard.CompanionConfig{Director: "rules", Pack: "default", Preset: "werkschaft-d1", AgentCooldownSeconds: 30, AgentHourlyCap: 20}
	stream, disconnected := open(), open()
	expectCompanion(t, stream, want)
	expectCompanion(t, disconnected, want)
	_ = disconnected.Close()

	tune := func(configHome, args string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCompanionStdioHandoff$")
		cmd.Env = append(os.Environ(), "WS_TEST_COMPANION_ROOT="+root,
			"WS_TEST_COMPANION_CACHE="+opts.CacheHome, "WS_TEST_COMPANION_CONFIG="+configHome)
		cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"config.tune","arguments":` + args + "}}\n")
		raw, err := cmd.CombinedOutput()
		line, _, _ := strings.Cut(string(raw), "\n")
		var reply struct {
			Result *struct{ IsError bool } `json:"result"`
		}
		if err != nil || json.Unmarshal([]byte(line), &reply) != nil || reply.Result == nil || reply.Result.IsError {
			t.Fatalf("stdio config.tune: %v, %s", err, raw)
		}
	}
	tune(opts.ConfigHome, `{"key":"dashboard.companion_enabled","value":true}`)
	want.Enabled = true
	expectCompanion(t, stream, want)
	reconnected := open()
	expectCompanion(t, reconnected, want)

	mutate := func(origin string) int {
		req := httptest.NewRequest(http.MethodPost, base+"/api/config/set", strings.NewReader(
			`{"scope":"global","key":"dashboard.companion_pack","value":"auto"}`))
		req.RemoteAddr = "127.0.0.1:12345"
		req.AddCookie(cookie)
		req.Header.Set("Origin", origin)
		out := httptest.NewRecorder()
		daemon.Handler().ServeHTTP(out, req)
		return out.Code
	}
	if code := mutate("http://example.test"); code != http.StatusForbidden {
		t.Fatalf("cross-origin companion mutation returned %d", code)
	}
	if code := mutate(base); code != http.StatusOK {
		t.Fatalf("trusted companion mutation returned %d", code)
	}
	want.Pack = "auto"
	expectCompanion(t, stream, want)
	expectCompanion(t, reconnected, want)

	tune(t.TempDir(), `{"key":"dashboard.companion_enabled","value":true}`)
	tune(opts.ConfigHome, `{"key":"dashboard.companion_enabled","reset":true}`)
	want.Enabled = false
	expectCompanion(t, stream, want)
	expectCompanion(t, reconnected, want)

	for _, path := range []string{"/", "/api/config/snapshot"} {
		req := httptest.NewRequest(http.MethodGet, base+path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.AddCookie(cookie)
		out := httptest.NewRecorder()
		daemon.Handler().ServeHTTP(out, req)
		jsonConfig, _ := json.Marshal(want)
		if out.Code != http.StatusOK || !strings.Contains(out.Body.String(), string(jsonConfig)) {
			t.Fatalf("%s did not bootstrap %s (status %d)", path, jsonConfig, out.Code)
		}
	}
}

func expectCompanion(t *testing.T, conn *websocket.Conn, want wsdashboard.CompanionConfig) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		var frame struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		var raw string
		if err := websocket.Message.Receive(conn, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(raw), &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Event != "companion" {
			continue
		}
		var got wsdashboard.CompanionConfig
		if err := json.Unmarshal(frame.Data, &got); err != nil || got != want {
			t.Fatalf("companion event = %s, %v; want %+v", raw, err, want)
		}
		return
	}
}
