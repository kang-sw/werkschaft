package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
)

type cliTokenBackend struct{ starts chan wsagent.StartSpec }

func (b *cliTokenBackend) Start(_ context.Context, spec wsagent.StartSpec) (wsagent.Process, error) {
	b.starts <- spec
	return &cliTokenProcess{events: make(chan wsagent.Event)}, nil
}

type cliTokenProcess struct {
	events chan wsagent.Event
	once   sync.Once
}

func (p *cliTokenProcess) Events() <-chan wsagent.Event { return p.events }
func (p *cliTokenProcess) Send(string) error            { return nil }
func (p *cliTokenProcess) Interrupt() error             { return nil }
func (p *cliTokenProcess) SetModel(string) error        { return nil }
func (p *cliTokenProcess) SetEffort(string) error       { return nil }
func (p *cliTokenProcess) Close() error {
	p.once.Do(func() { close(p.events) })
	return nil
}

func TestHostedCLIWorkerTokenRouting(t *testing.T) {
	hello := cliHello(t)
	t.Setenv("WS_CACHE_HOME", hello.Env["WS_CACHE_HOME"])
	t.Setenv("WS_CONFIG_HOME", hello.Env["WS_CONFIG_HOME"])
	keys := mcp.NewAgentKeyStore()
	key, err := keys.Mint(hello.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	config := wsconfig.Options{ConfigHome: hello.Env["WS_CONFIG_HOME"], CacheHome: hello.Env["WS_CACHE_HOME"]}
	for _, tier := range []string{"small", "medium", "large"} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(config, tier, "", "provider/"+tier, wsagent.BackendClaude, "low"); err != nil {
			t.Fatal(err)
		}
	}
	backend := &cliTokenBackend{starts: make(chan wsagent.StartSpec, 8)}
	runner := wsagent.NewRunner(wsagent.Options{Dir: t.TempDir(), Config: config, Keys: keys,
		Backends: map[string]wsagent.BackendRegistration{
			wsagent.BackendClaude: {
				Backend: backend, WorkerCapable: true, WorkerProfile: wsagent.AgentWorkerProfile(wsagent.BackendClaude),
				Permission: wsagent.BuiltinBackendRegistrations()[wsagent.BackendClaude].Permission,
			},
		},
		WSMCP: func(string) (wsagent.MCPCommand, error) { return wsagent.MCPCommand{Command: "unused"}, nil },
	})
	daemon := httptest.NewServer(runner.Handler())
	defer daemon.Close()
	runner.SetEndpoint(daemon.URL)
	runner.Start(nil)
	defer runner.Close()
	parent := wsagent.LeadCaller("parentreplyid", hello.Cwd, wsagent.BackendClaude, config)
	spawnWorker := func() string {
		t.Helper()
		_, err := runner.AgentSpawn(parent, wsagent.AgentSpawnRequest{Prompt: "work", Tier: "medium", Authority: "read-only"})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case spec := <-backend.starts:
			if spec.WSMCP == nil || spec.WSMCP.Env[wsagent.EnvPiToken] == "" {
				t.Fatal("worker has no capability token")
			}
			return spec.WSMCP.Env[wsagent.EnvPiToken]
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not start")
			return ""
		}
	}
	tokenA, tokenB := spawnWorker(), spawnWorker()
	hello.Env[wsagent.EnvPiEndpoint] = daemon.URL
	hello.Env[wsagent.EnvPiToken] = tokenA
	hello.Env["WS_MCP_ALLOWED_TOOLS"] = "agent.spawn,agent.list,agent.interrupt,pi.wait"
	host := &proxyHost{agents: runner, logw: io.Discard}
	// Legacy Pi calls remain hidden for worker and lead sessions.
	for _, token := range []string{tokenA, "  "} {
		hello.Env[wsagent.EnvPiToken] = token
		out, stderr, code := hostedCLIResultWithHost(t, host, hello, "tools", "pi.spawn")
		if code == 0 || !strings.Contains(stderr, "tool not found: pi.spawn") {
			t.Fatalf("schema with token %q: %q %q %d", token, out, stderr, code)
		}
	}
	hello.Env[wsagent.EnvPiToken] = tokenA
	call := func(tool, args string) (string, string, int) {
		t.Helper()
		return hostedCLIResultWithHost(t, host, hello, "call", tool, args)
	}
	out, stderr, code := call("agent.spawn", fmt.Sprintf(`{"session_key":%q,"prompt":"child","tier":"small","authority":"read-only","format":"json"}`, key))
	if code != 0 {
		t.Fatalf("spawn: %q %q %d", out, stderr, code)
	}
	var child wsagent.AgentSpawnResult
	if err := json.Unmarshal([]byte(out), &child); err != nil || child.Address == "" {
		t.Fatalf("child: %q %v", out, err)
	}
	args := fmt.Sprintf(`{"session_key":%q}`, key)
	out, stderr, code = call("agent.list", args)
	if code != 0 || !strings.Contains(out, child.Address) {
		t.Fatalf("own list: %q %q %d", out, stderr, code)
	}
	hello.Env[wsagent.EnvPiToken] = tokenB
	out, stderr, code = call("agent.list", args)
	if code != 0 || strings.Contains(out, child.Address) {
		t.Fatalf("foreign list: %q %q %d", out, stderr, code)
	}
	out, stderr, code = call("agent.spawn", fmt.Sprintf(`{"session_key":%q,"prompt":"too high","tier":"large","authority":"read-only","format":"json"}`, key))
	if code == 0 || !strings.Contains(stderr, "above this conversation's own tier") {
		t.Fatalf("tier cap: %q %q %d", out, stderr, code)
	}
	// 0.53 removed the pi.wait refusal stub; the dispatcher now answers it
	// as an unknown tool.
	out, stderr, code = call("pi.wait", fmt.Sprintf(`{"session_key":%q,"id":%q}`, key, child.Address))
	if code == 0 || !strings.Contains(stderr, "unknown tool: pi.wait") {
		t.Fatalf("legacy wait: %q %q %d", out, stderr, code)
	}
}
