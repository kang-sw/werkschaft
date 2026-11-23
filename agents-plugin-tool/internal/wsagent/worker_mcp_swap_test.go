package wsagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsholder"
)

type workerSwapProxy struct{}

func (p workerSwapProxy) Accept(wsdashboard.ProxyHello) (wsdashboard.ProxySession, error) {
	return p, nil
}

func (workerSwapProxy) Drain(context.Context) bool { return true }

func (workerSwapProxy) Serve(_ context.Context, stream io.ReadWriteCloser) {
	in, out := json.NewDecoder(stream), json.NewEncoder(stream)
	for {
		var message struct {
			ID     json.RawMessage
			Method string
		}
		if in.Decode(&message) != nil {
			return
		}
		if message.ID != nil {
			if out.Encode(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": message.Method}) != nil {
				return
			}
		}
	}
}

func TestHeldClaudeWorkerMCPAcrossDashboardReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h, _ := workerRecoveryHarness(t, BackendClaude)
	h.runner.Close()
	python := lookPython(t)
	home := t.TempDir()
	script := filepath.Join(home, "claude-transport.py")
	const claude = `
import json, sys
for line in sys.stdin:
    if json.loads(line).get("type") == "user":
        print('{"type":"system","subtype":"session_state_changed","state":"running"}', flush=True)
        print('{"type":"system","subtype":"init","session_id":"held-claude","permissionMode":"dontAsk"}', flush=True)
`
	if err := os.WriteFile(script, []byte(claude), 0o600); err != nil {
		t.Fatal(err)
	}
	environ = func() []string { return []string{"HOME=" + home, "USERPROFILE=" + home} }
	h.opts.Alive = nil

	holderOpts := wsholder.Options{Dir: filepath.Join(home, "holders"), Python: python}
	newManager := func() *wsholder.Manager { return wsholder.New(holderOpts) }
	var daemon *wsdashboard.Daemon
	t.Cleanup(func() {
		if daemon != nil {
			daemon.Close()
		}
		manager := newManager()
		defer manager.Close()
		manager.Scan()
		for _, info := range manager.List().Holders {
			if err := manager.Stop(info.ID); err != nil && !recoveryStoppedLinuxZombie(filepath.Join(holderOpts.Dir, info.ID), info.PID, err) {
				t.Errorf("stop fixture holder: %v", err)
			}
		}
	})
	start := func(port int) {
		t.Helper()
		manager := newManager()
		h.opts.Holders = recoveryPythonHost{ManagerHost: ManagerHost{manager}, python: python, script: script}
		h.runner = NewRunner(h.opts)
		var err error
		daemon, err = wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
			Dir: h.dir, Home: home, Port: port, Version: h.opts.DaemonVersion, Agents: h.runner, Holders: manager, Proxy: workerSwapProxy{}, Log: h.log,
		})
		if err != nil {
			manager.Close()
			t.Fatal(err)
		}
	}
	start(0)
	originalPort := daemon.Endpoint().Port

	// Hold a different port open until after the original listener is gone.
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close()
	movedPort := reservation.Addr().(*net.TCPAddr).Port

	caller := LeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.opts.Config)
	res, err := h.runner.AgentSpawn(caller, AgentSpawnRequest{Prompt: "work", Tier: "claude.small", Authority: "workdir-write"})
	if err != nil {
		t.Fatal(err)
	}
	id := agentID(res.Address)
	var rec Record
	eventually(t, "running held Claude worker", func() bool {
		rec = h.record(id)
		return rec.HolderID != "" && rec.HostPID > 0 && rec.SessionID != "" && rec.TurnOpen && rec.MCPToken != ""
	})
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	post := func(url, method string) (int, error) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q}`, method)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+rec.MCPToken)
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		var reply struct{ Result string }
		if resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
				t.Fatal(err)
			}
			if reply.Result != method {
				t.Fatalf("route replied %q, want %s", reply.Result, method)
			}
		}
		return resp.StatusCode, nil
	}
	checkRoute := func(url, method string, want int) {
		t.Helper()
		if status, err := post(url, method); err != nil || status != want {
			t.Fatalf("route returned %d, want %d (%v)", status, want, err)
		}
	}
	checkRoute(rec.MCPURL, "initialize", http.StatusOK)

	shutdown := func() {
		t.Helper()
		if err := wsdashboard.RequestShutdown(ctx, client, daemon.Endpoint()); err != nil {
			t.Fatal(err)
		}
		daemon.Close()
	}
	checkHolder := func() {
		t.Helper()
		for _, info := range h.opts.Holders.List().Holders {
			if info.ID == rec.HolderID && info.Attached && info.Child != nil && info.Child.PID == rec.HostPID {
				return
			}
		}
		t.Fatal("successor did not reattach the same live holder and Claude transport")
	}
	shutdown()
	start(originalPort)
	checkHolder()
	checkRoute(rec.MCPURL, "tools/list", http.StatusOK)

	shutdown()
	reservation.Close()
	start(movedPort)
	checkHolder()
	if daemon.Endpoint().Port != movedPort {
		t.Fatal("successor did not move to the reserved port")
	}
	oldPort, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", originalPort))
	if err != nil {
		t.Fatalf("the original port was not released: %v", err)
	}
	defer oldPort.Close()
	checkRoute(h.runner.mcpURL(), "tools/list", http.StatusUnauthorized)

	log := h.log.String()
	for _, line := range []string{
		"agent " + id + ": worker MCP rejoin: restored",
		"agent " + id + ": worker MCP rejoin: URL mismatch",
		"dashboard: /mcp/session 401: token unknown",
	} {
		if !strings.Contains(log, line+"\n") {
			t.Errorf("missing diagnostic %q", line)
		}
		t.Log(line)
	}
	if strings.Contains(log, rec.MCPToken) {
		t.Fatal("diagnostics exposed a bearer")
	}
}
