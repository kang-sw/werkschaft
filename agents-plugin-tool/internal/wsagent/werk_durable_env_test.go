package wsagent

import (
	"errors"
	"strings"
	"testing"
)

// A ProcessDurable session (werk) runs inside the daemon, so its MCP hello must
// carry the daemon's environment: the caller-environment resolver behind every
// git and exec tool refuses to fall back to the daemon's PATH, and a hello with
// only WS_* keys left every one of them failing with "git not found".

const (
	durableEnvPath   = "/test/durable-bin:/usr/bin"
	durableEnvHome   = "/test/durable-home"
	durableEnvCache  = "/test/durable-cache"
	durableEnvConfig = "/test/durable-config"
	durableEnvToken  = "daemon-inherited-session-token"
	durableEnvURL    = "http://127.0.0.1:1/inherited"
)

var dirtyParentEnv = []string{
	"WS_MCP_NO_DASHBOARD=parent-no-dashboard",
	"WS_HOSTED_SESSION_KEY=parent-hosted-key",
	"WS_MAILBOX_AUTO=parent@auto",
	"WS_MCP_DEBUG_LOG=/parent/debug.log",
	"WS_RSRC_DEV_CHECKOUT=/parent/dev",
	"CLAUDECODE=1",
	"CLAUDE_CODE_SSE_PORT=123",
	"CODEX_THREAD_ID=abc",
	"PI_SESSION=1",
	"ORCA_FOO=1",
	"WS_PI_CHANNEL_CREDENTIAL=sec",
}

// stubDaemonEnviron replaces the daemon environment the durable start sites read.
// The inherited MCP credentials and session vars model a daemon started from
// inside a parent harness session.
func stubDaemonEnviron(t *testing.T) {
	t.Helper()
	prev := environ
	environ = func() []string {
		base := []string{
			"PATH=" + durableEnvPath, "HOME=" + durableEnvHome, "USERPROFILE=" + durableEnvHome,
			"WS_CACHE_HOME=" + durableEnvCache, "WS_CONFIG_HOME=" + durableEnvConfig,
			EnvMCPSessionURL + "=" + durableEnvURL, EnvMCPSessionToken + "=" + durableEnvToken,
		}
		return append(base, dirtyParentEnv...)
	}
	t.Cleanup(func() { environ = prev })
}

func checkDurableHelloEnv(t *testing.T, env map[string]string, token string) {
	t.Helper()
	if env["PATH"] != durableEnvPath {
		t.Errorf("hello PATH = %q, want %q", env["PATH"], durableEnvPath)
	}
	if env["HOME"] != durableEnvHome {
		t.Errorf("hello HOME = %q, want %q", env["HOME"], durableEnvHome)
	}
	if env["WS_CACHE_HOME"] != durableEnvCache {
		t.Errorf("hello WS_CACHE_HOME = %q, want %q", env["WS_CACHE_HOME"], durableEnvCache)
	}
	if env["WS_CONFIG_HOME"] != durableEnvConfig {
		t.Errorf("hello WS_CONFIG_HOME = %q, want %q", env["WS_CONFIG_HOME"], durableEnvConfig)
	}
	for name, value := range env {
		if strings.EqualFold(name, EnvMCPSessionURL) || strings.EqualFold(name, EnvMCPSessionToken) ||
			strings.Contains(value, durableEnvToken) || (token != "" && strings.Contains(value, token)) {
			t.Errorf("hello env leaks MCP credential %s=%q", name, value)
		}
	}
	for _, name := range []string{
		"WS_MCP_DEBUG_LOG", "WS_RSRC_DEV_CHECKOUT",
		"CLAUDECODE", "CLAUDE_CODE_SSE_PORT", "CODEX_THREAD_ID", "PI_SESSION", "ORCA_FOO", "WS_PI_CHANNEL_CREDENTIAL",
	} {
		if value, ok := env[name]; ok {
			t.Errorf("hello env leaked parent-harness/session var %s=%q", name, value)
		}
	}
	if got := env["WS_MAILBOX_AUTO"]; got == "parent@auto" {
		t.Errorf("hello env leaked parent WS_MAILBOX_AUTO=%q", got)
	}
	if got := env["WS_HOSTED_SESSION_KEY"]; got == "parent-hosted-key" {
		t.Errorf("hello env leaked parent WS_HOSTED_SESSION_KEY=%q", got)
	}
	if got := env[EnvNoDashboard]; got == "parent-no-dashboard" {
		t.Errorf("hello env leaked parent %s=%q", EnvNoDashboard, got)
	}
}

func TestDurableEnvStripsInheritedSessionAndHarnessVars(t *testing.T) {
	base := append([]string{
		"PATH=" + durableEnvPath, "HOME=" + durableEnvHome, "USERPROFILE=" + durableEnvHome,
		"WS_CACHE_HOME=" + durableEnvCache, "WS_CONFIG_HOME=" + durableEnvConfig,
	}, dirtyParentEnv...)
	got := workerEnvMap(durableEnv(base))
	for name, want := range map[string]string{
		"PATH":           durableEnvPath,
		"HOME":           durableEnvHome,
		"USERPROFILE":    durableEnvHome,
		"WS_CACHE_HOME":  durableEnvCache,
		"WS_CONFIG_HOME": durableEnvConfig,
	} {
		if got[name] != want {
			t.Errorf("durableEnv %s = %q, want %q", name, got[name], want)
		}
	}
	for _, kv := range dirtyParentEnv {
		name, _, _ := strings.Cut(kv, "=")
		if val, ok := got[name]; ok {
			t.Errorf("durableEnv leaked %s=%q", name, val)
		}
	}
}

func TestMCPHelloStripsSessionCredentialsFromHarnessEnv(t *testing.T) {
	hello := mcpHello([]string{
		"PATH=" + durableEnvPath, "HOME=" + durableEnvHome,
		"WS_CACHE_HOME=" + durableEnvCache, "WS_CONFIG_HOME=" + durableEnvConfig,
		EnvMCPSessionURL + "=" + durableEnvURL, EnvMCPSessionToken + "=" + durableEnvToken,
	}, map[string]string{"WS_MCP_HARNESS": "werk"}, "/cwd", 0, "v")
	checkDurableHelloEnv(t, hello.Env, "")
	if hello.Harness != "werk" {
		t.Errorf("hello harness = %q", hello.Harness)
	}
}

func TestMCPHelloForOSWindowsCaseFoldsCredentialsAndOverrides(t *testing.T) {
	hello := mcpHelloForOS("windows", []string{
		"Path=C:\\bin",
		"Ws_McP_SeSsIoN_ToKeN=" + durableEnvToken,
		"ws_mcp_session_url=" + durableEnvURL,
		"ws_mcp_harness=old",
		"ws_mcp_project_root=C:\\old-root",
	}, map[string]string{
		"WS_MCP_HARNESS":      "werk",
		"WS_MCP_PROJECT_ROOT": "C:\\new-root",
	}, "C:\\cwd", 42, "v")
	for name, value := range hello.Env {
		if strings.EqualFold(name, EnvMCPSessionURL) || strings.EqualFold(name, EnvMCPSessionToken) {
			t.Fatalf("Windows mcpHello leaked credential %s=%q", name, value)
		}
		if name == "ws_mcp_harness" || name == "ws_mcp_project_root" {
			t.Fatalf("Windows mcpHello left mixed-case duplicate %s=%q alongside override", name, value)
		}
	}
	if hello.Env["WS_MCP_HARNESS"] != "werk" || hello.Harness != "werk" {
		t.Fatalf("hello harness = %q (env=%q), want werk", hello.Harness, hello.Env["WS_MCP_HARNESS"])
	}
	if hello.Env["WS_MCP_PROJECT_ROOT"] != "C:\\new-root" || hello.ProjectRoot != "C:\\new-root" {
		t.Fatalf("hello project root = %q (env=%q), want C:\\new-root", hello.ProjectRoot, hello.Env["WS_MCP_PROJECT_ROOT"])
	}
	if hello.Env["Path"] != "C:\\bin" {
		t.Fatalf("hello Path = %q, want C:\\bin", hello.Env["Path"])
	}
}

func TestDurableWorkerHelloCarriesDaemonEnvironment(t *testing.T) {
	stubDaemonEnviron(t)
	h := newHarness(t, map[string]string{"chat": `{"backend":"claude","ws_tools":["tickets.query"]}`})
	h.runner.Close()
	reg := h.config.Backends[suiteBackend]
	reg.Backend = durableStartFixture{fakeBackend: h.backend}
	reg.ProcessKind = ProcessDurable
	h.config.Backends[suiteBackend] = reg
	h.endpoint = "http://127.0.0.1:1"
	h.start()
	st := h.create("chat")
	if _, err := h.runner.Send(st.ID, "hello"); err != nil {
		t.Fatal(err)
	}
	spec := h.backend.waitProcStart(t, 0)
	if spec.MCPSession == nil || len(spec.Env) != 0 {
		t.Fatal("durable start needs a bearer and no child launch environment")
	}
	hello, _, ok := h.runner.ResolveMCPToken(spec.MCPSession.Token)
	if !ok || hello.OwnerPID != 0 {
		t.Fatalf("durable worker hello did not resolve (ok=%v pid=%d)", ok, hello.OwnerPID)
	}
	checkDurableHelloEnv(t, hello.Env, spec.MCPSession.Token)
	if _, ok := hello.Env["WS_HOSTED_SESSION_KEY"]; ok {
		t.Errorf("durable worker hello inherited WS_HOSTED_SESSION_KEY: %q", hello.Env["WS_HOSTED_SESSION_KEY"])
	}
	if hello.Env[EnvAllowedTools] == "" || hello.Env[EnvAgentSessionKey] == "" {
		t.Errorf("the ws-mcp entry's own environment was lost: %v", hello.Env)
	}
}

func TestDurableLeadHelloCarriesDaemonEnvironment(t *testing.T) {
	h := newLeadHarness(t)
	stubDaemonEnviron(t) // after the harness, which installs its own stub
	backend := &fakeBackend{}
	reg := h.opts.Backends[BackendClaude]
	reg.Backend = durableStartFixture{fakeBackend: backend}
	reg.ProcessKind = ProcessDurable
	h.opts.Backends[BackendClaude] = reg
	h.opts.Holders = nil
	h.opts.WSMCP = func(string) (MCPCommand, error) {
		return MCPCommand{}, errors.New("the child launcher was selected")
	}
	h.opts.DaemonMCP = func(string) (MCPCommand, error) {
		return MCPCommand{Env: map[string]string{"FIXTURE_PACKAGE": "active"}}, nil
	}
	h.startAt("http://127.0.0.1:1")
	st := h.lead(BackendClaude, LeadOptions{})
	h.send(st.ID, "hello")
	spec := backend.waitProcStart(t, 0)
	if spec.MCPSession == nil || len(spec.Env) != 0 {
		t.Fatal("durable lead needs a bearer and no child launch environment")
	}
	hello, _, ok := h.runner.ResolveMCPToken(spec.MCPSession.Token)
	if !ok || hello.OwnerPID != 0 {
		t.Fatalf("durable lead hello did not resolve (ok=%v pid=%d)", ok, hello.OwnerPID)
	}
	checkDurableHelloEnv(t, hello.Env, spec.MCPSession.Token)
	if _, ok := hello.Env[EnvNoDashboard]; ok {
		t.Errorf("durable lead hello inherited %s: %q", EnvNoDashboard, hello.Env[EnvNoDashboard])
	}
	if hello.Env["FIXTURE_PACKAGE"] != "active" {
		t.Errorf("the ws-mcp entry's own environment was lost: %v", hello.Env)
	}
}

// A durable session restarted by a replacement daemon is a fresh start that
// draws a fresh hello from the new daemon's environment: no token or hello is
// persisted for it (rec.forgetMCP, durable branch), so nothing stale or
// credentialed survives the swap.
func TestDurableLeadRestartRereadsDaemonEnvironment(t *testing.T) {
	h := newLeadHarness(t)
	stubDaemonEnviron(t)
	backend := &fakeBackend{}
	reg := h.opts.Backends[BackendClaude]
	reg.Backend = durableStartFixture{fakeBackend: backend}
	reg.ProcessKind = ProcessDurable
	h.opts.Backends[BackendClaude] = reg
	h.opts.Holders = nil
	h.opts.WSMCP = func(string) (MCPCommand, error) { return MCPCommand{}, errors.New("child launcher selected") }
	h.opts.DaemonMCP = func(string) (MCPCommand, error) { return MCPCommand{}, nil }
	h.startAt("http://127.0.0.1:1")
	st := h.lead(BackendClaude, LeadOptions{})
	h.send(st.ID, "hello")
	spec := backend.waitProcStart(t, 0)
	if rec := h.record(st.ID); rec.MCPToken != "" || rec.MCPEnv != nil {
		t.Fatalf("a durable session persisted its MCP session: token=%q env=%v", rec.MCPToken, rec.MCPEnv)
	}
	first := spec.MCPSession.Token

	// The daemon's PATH changes across the swap; the next start must see it.
	const swapped = "/test/swapped-bin"
	environ = func() []string { return []string{"PATH=" + swapped, "HOME=" + durableEnvHome} }
	backend.proc(0).exit()
	h.send(st.ID, "again")
	next := backend.waitProcStart(t, 1)
	if next.MCPSession == nil || next.MCPSession.Token == first {
		t.Fatal("the restart reused the old bearer")
	}
	hello, _, ok := h.runner.ResolveMCPToken(next.MCPSession.Token)
	if !ok || hello.Env["PATH"] != swapped {
		t.Fatalf("restarted hello PATH = %q (ok=%v), want %q", hello.Env["PATH"], ok, swapped)
	}
}
