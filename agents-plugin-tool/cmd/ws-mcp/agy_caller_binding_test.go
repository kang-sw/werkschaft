package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

// agyCallerRefusal is the shipped agy registration's cross-harness refusal
// (261122-feat-ws-agy-same-harness-workers, Decision 8).
const agyCallerRefusal = `harness "agy" accepts workers only from an agy session: Antigravity's consumer terms do not support running agy under another agent`

// callerBindingFixture is a daemon runner over the shipped registrations
// (each process start captured by a fake, so no harness binary runs) with
// an agy lead and an agy-spawned agy worker loaded from their records, and
// the proxy host that accepts connections for it.
type callerBindingFixture struct {
	hello              wsdashboard.ProxyHello
	runner             *wsagent.Runner
	host               *proxyHost
	starts             chan wsagent.StartSpec
	daemon             string
	leadKey, workerKey string
	lead, worker       string
}

func newCallerBindingFixture(t *testing.T) *callerBindingFixture {
	t.Helper()
	f := &callerBindingFixture{hello: cliHello(t), starts: make(chan wsagent.StartSpec, 8)}
	f.hello.Mode = wsdashboard.ProxyModeMCP
	cache := f.hello.Env["WS_CACHE_HOME"]
	t.Setenv("WS_CACHE_HOME", cache)
	t.Setenv("WS_CONFIG_HOME", f.hello.Env["WS_CONFIG_HOME"])
	dir, err := wsdashboard.Dir(cache)
	if err != nil {
		t.Fatal(err)
	}
	keys := mcp.NewAgentKeyStore()
	if f.leadKey, err = keys.MintLead(f.hello.Cwd); err != nil {
		t.Fatal(err)
	}
	if f.workerKey, err = keys.Mint(f.hello.Cwd); err != nil {
		t.Fatal(err)
	}
	f.lead, f.worker = wsmailbox.ReplyID(f.leadKey), wsmailbox.ReplyID(f.workerKey)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, rec := range []wsagent.Record{
		{ReplyID: f.lead, WSKey: f.leadKey, Root: f.hello.Cwd, CreatedAt: now, Backend: wsagent.BackendAgy, Profile: wsagent.LeadProfileAgy, Lead: true},
		{ReplyID: f.worker, WSKey: f.workerKey, Root: f.hello.Cwd, CreatedAt: now, Backend: wsagent.BackendAgy,
			Profile: wsagent.AgentWorkerProfile(wsagent.BackendAgy), Tier: "small", SpawnerBackend: wsagent.BackendAgy,
			Parent: &wsagent.ParentRef{Kind: wsagent.ParentLead, ReplyID: f.lead}},
	} {
		if err := wsagent.WriteRecordForTest(dir, rec); err != nil {
			t.Fatal(err)
		}
	}
	regs := wsagent.BuiltinBackendRegistrations()
	for name, reg := range regs {
		reg.Backend = &cliTokenBackend{starts: f.starts}
		regs[name] = reg
	}
	f.runner = wsagent.NewRunner(wsagent.Options{Dir: dir, Keys: keys, Backends: regs,
		Config: wsconfig.Options{ConfigHome: f.hello.Env["WS_CONFIG_HOME"], CacheHome: cache},
		WSMCP:  func(string) (wsagent.MCPCommand, error) { return wsagent.MCPCommand{Command: "unused"}, nil },
	})
	daemon := httptest.NewServer(f.runner.Handler())
	t.Cleanup(daemon.Close)
	f.daemon = daemon.URL
	f.runner.SetEndpoint(daemon.URL)
	f.runner.Start(nil)
	t.Cleanup(f.runner.Close)
	f.host = &proxyHost{agents: f.runner, logw: io.Discard}
	return f
}

// accept is the server the proxy host builds for hello.
func (f *callerBindingFixture) accept(t *testing.T, hello wsdashboard.ProxyHello) *mcp.Server {
	t.Helper()
	session, err := f.host.Accept(hello)
	if err != nil {
		t.Fatal(err)
	}
	server := session.(*proxyMCPSession).server
	t.Cleanup(server.Close)
	return server
}

// helloWith is the fixture's hello with env laid over its environment.
func (f *callerBindingFixture) helloWith(env map[string]string) wsdashboard.ProxyHello {
	hello := f.hello
	hello.Env = make(map[string]string, len(f.hello.Env)+len(env))
	for k, v := range f.hello.Env {
		hello.Env[k] = v
	}
	for k, v := range env {
		hello.Env[k] = v
	}
	return hello
}

// bindingToolText calls tool on server and returns its text content, failing on
// a protocol error.
func bindingToolText(t *testing.T, server *mcp.Server, tool string, args map[string]any) string {
	t.Helper()
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := server.ServeStdio(context.Background(), bytes.NewReader(append(req, '\n')), &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("%s: %v: %s", tool, err, &out)
	}
	if resp.Error != nil {
		t.Fatalf("%s: %s", tool, resp.Error.Message)
	}
	var text strings.Builder
	for _, c := range resp.Result.Content {
		text.WriteString(c.Text)
	}
	return text.String()
}

func agentTiersOf(t *testing.T, server *mcp.Server, args map[string]any) wsagent.AgentTiers {
	t.Helper()
	args["format"] = "json"
	text := bindingToolText(t, server, "agent.tiers", args)
	var tiers wsagent.AgentTiers
	if err := json.Unmarshal([]byte(text), &tiers); err != nil {
		t.Fatalf("agent.tiers: %v: %s", err, text)
	}
	return tiers
}

// TestAgyAdmissionTrustsOnlyTheAuthenticatedBinding goes through the
// proxy host's Accept, where a token-resolved hello's conversation becomes
// the server's binding and the runner its memory-only caller check:
//
//   - a launcher connection whose hosted-session variables, registration
//     row, harness PID, and on-disk record all name the agy lead, but which
//     no session token binds, is refused agy workers (agent.tiers and
//     agent.spawn);
//   - a connection bound to the agy lead's conversation is offered them.
func TestAgyAdmissionTrustsOnlyTheAuthenticatedBinding(t *testing.T) {
	f := newCallerBindingFixture(t)
	row := filepath.Join(t.TempDir(), "registration.json")
	raw, err := json.Marshal(wsdashboard.Registration{HostPID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(row, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	spoofed := f.helloWith(map[string]string{
		"WS_HOSTED_SESSION": "1", "WS_HOSTED_SESSION_KEY": f.leadKey, "WS_HOSTED_SESSION_ROW": row,
		"WS_MCP_HARNESS_PID": strconv.Itoa(os.Getpid()),
	})
	spoofed.Harness = wsagent.BackendAgy

	t.Run("spoofed launcher", func(t *testing.T) {
		server := f.accept(t, spoofed)
		// Precondition: the variables make it the agy lead's hosted
		// connection (an omitted session_key is filled in with its key).
		tiers := agentTiersOf(t, server, map[string]any{})
		if tiers.Harness != wsagent.BackendAgy || len(tiers.Native) != 0 || !slices.Contains(tiers.Unavailable, "agy: "+agyCallerRefusal) {
			t.Fatalf("harness %q, native %+v, unavailable %v", tiers.Harness, tiers.Native, tiers.Unavailable)
		}
		for _, tier := range []string{"agy.small", "small"} {
			if text := bindingToolText(t, server, "agent.spawn", map[string]any{"prompt": "x", "tier": tier, "authority": "read-only"}); !strings.Contains(text, agyCallerRefusal) {
				t.Fatalf("agent.spawn %s = %s", tier, text)
			}
		}
		select {
		case spec := <-f.starts:
			t.Fatalf("a refused spawn started %+v", spec)
		default:
		}
	})

	t.Run("bound agy lead", func(t *testing.T) {
		bound := spoofed
		bound.Conversation = f.lead
		tiers := agentTiersOf(t, f.accept(t, bound), map[string]any{})
		if tiers.Harness != wsagent.BackendAgy || len(tiers.Native) == 0 || slices.ContainsFunc(tiers.Unavailable, func(e string) bool { return strings.HasPrefix(e, "agy: ") }) {
			t.Fatalf("harness %q, native %+v, unavailable %v", tiers.Harness, tiers.Native, tiers.Unavailable)
		}
	})
}

// TestForwardingWorkerRoutedBindingRefusesCrossHarnessAgy: a spawned
// worker's connection forwards agent.* over its capability token, so the
// proxy host gives it no runner handle; config.resolve_agent still checks
// the authenticated caller against the runner's memory. A Claude worker
// (authenticated by the capability token the runner issued it) is refused
// an agy-ending tier and keeps its Claude tier; an agy worker (bound to its
// conversation) resolves the agy tier.
func TestForwardingWorkerRoutedBindingRefusesCrossHarnessAgy(t *testing.T) {
	f := newCallerBindingFixture(t)
	// The agy lead, on its bound connection, spawns a Claude worker.
	lead := f.hello
	lead.Conversation = f.lead
	if text := bindingToolText(t, f.accept(t, lead), "agent.spawn", map[string]any{"session_key": f.leadKey, "prompt": "work", "tier": "claude.small", "authority": "read-only"}); !strings.Contains(text, "id:") {
		t.Fatalf("the agy lead's Claude spawn = %s", text)
	}
	var token string
	select {
	case spec := <-f.starts:
		if spec.WSMCP == nil || spec.WSMCP.Env[wsagent.EnvPiToken] == "" {
			t.Fatal("the Claude worker has no capability token")
		}
		token = spec.WSMCP.Env[wsagent.EnvPiToken]
	case <-time.After(5 * time.Second):
		t.Fatal("the Claude worker did not start")
	}
	resolve := func(server *mcp.Server, key, tier string) string {
		return bindingToolText(t, server, "config.resolve_agent", map[string]any{"session_key": key, "tier": tier, "format": "json"})
	}
	forwarding := map[string]string{wsagent.EnvPiToken: token, wsagent.EnvPiEndpoint: f.daemon}

	claude := f.accept(t, f.helloWith(forwarding))
	if text := resolve(claude, f.workerKey, "agy.small"); !strings.Contains(text, agyCallerRefusal) {
		t.Fatalf("a Claude worker's agy tier = %s", text)
	}
	if text := resolve(claude, f.workerKey, "claude.small"); strings.Contains(text, "accepts workers only") || !strings.Contains(text, `"backend":"claude"`) {
		t.Fatalf("a Claude worker's Claude tier = %s", text)
	}
	// A token the runner never issued authenticates nothing.
	stranger := f.accept(t, f.helloWith(map[string]string{wsagent.EnvPiToken: "not-issued", wsagent.EnvPiEndpoint: f.daemon}))
	if text := resolve(stranger, f.workerKey, "agy.small"); !strings.Contains(text, agyCallerRefusal) {
		t.Fatalf("an unissued token's agy tier = %s", text)
	}

	bound := f.helloWith(map[string]string{wsagent.EnvPiToken: "agy-worker-capability", wsagent.EnvPiEndpoint: f.daemon})
	bound.Conversation = f.worker
	if text := resolve(f.accept(t, bound), f.workerKey, "agy.small"); strings.Contains(text, "accepts workers only") || !strings.Contains(text, `"backend":"agy"`) {
		t.Fatalf("an agy worker's agy tier = %s", text)
	}
}
