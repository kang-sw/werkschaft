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
	durableEnvPath  = "/test/durable-bin:/usr/bin"
	durableEnvHome  = "/test/durable-home"
	durableEnvToken = "daemon-inherited-session-token"
	durableEnvURL   = "http://127.0.0.1:1/inherited"
)

// stubDaemonEnviron replaces the daemon environment the durable start sites read.
// The inherited MCP credentials model a daemon started from inside a session.
func stubDaemonEnviron(t *testing.T) {
	t.Helper()
	prev := environ
	environ = func() []string {
		return []string{
			"PATH=" + durableEnvPath, "HOME=" + durableEnvHome, "USERPROFILE=" + durableEnvHome,
			EnvMCPSessionURL + "=" + durableEnvURL, EnvMCPSessionToken + "=" + durableEnvToken,
		}
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
	for name, value := range env {
		if name == EnvMCPSessionURL || name == EnvMCPSessionToken ||
			strings.Contains(value, durableEnvToken) || (token != "" && strings.Contains(value, token)) {
			t.Errorf("hello env leaks MCP credential %s=%q", name, value)
		}
	}
}

func TestMCPHelloStripsSessionCredentialsFromHarnessEnv(t *testing.T) {
	hello := mcpHello([]string{
		"PATH=" + durableEnvPath, "HOME=" + durableEnvHome,
		EnvMCPSessionURL + "=" + durableEnvURL, EnvMCPSessionToken + "=" + durableEnvToken,
	}, map[string]string{"WS_MCP_HARNESS": "werk"}, "/cwd", 0, "v")
	checkDurableHelloEnv(t, hello.Env, "")
	if hello.Harness != "werk" {
		t.Errorf("hello harness = %q", hello.Harness)
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
