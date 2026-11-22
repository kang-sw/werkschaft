package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// TestUserCommandsOnTheProductionRunnerNeverReadTheKeyRecord drives the user
// command binding over the real runner and the production key store: with the
// lead's key record deleted from disk, the list, a page's revision read, a
// cold-cache list and a click's lookup still answer from the runner's memory,
// (the production store would answer from that file, or not at all).
func TestUserCommandsOnTheProductionRunnerNeverReadTheKeyRecord(t *testing.T) {
	f := newCallerBindingFixture(t)
	root := f.hello.Cwd
	u, err := openUserCommands(t.TempDir(), f.runner, nil, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = u.Close() })
	in := wsusercmd.Input{ID: "build", Label: "Build", Line: "make"}
	cmd, _, err := u.Register(f.lead, in)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// The key record the production store reads: remove every file under the
	// key directory so a read of it would fail or return another root.
	var records []string
	_ = filepath.WalkDir(os.Getenv("WS_CACHE_HOME"), func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.Contains(path, f.leadKey) {
			records = append(records, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if len(records) == 0 {
		t.Fatal("found no key record on disk to remove")
	}
	for _, path := range records {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
	if root, ok := mcp.NewAgentKeyStore().Root(f.leadKey); ok && root != "" {
		t.Fatalf("the key record is still readable (%q)", root)
	}
	for range 25 {
		cmds, rev, err := u.List(f.lead)
		if err != nil || len(cmds) != 1 || cmds[0] != cmd || rev == "" {
			t.Fatalf("list: %+v %q %v", cmds, rev, err)
		}
		if u.Revision(f.lead) != rev {
			t.Fatalf("the stream revision %q is not the list's %q", u.Revision(f.lead), rev)
		}
		got, gotRoot, found, err := u.Get(f.lead, "build")
		if err != nil || !found || got != cmd || !sameRoot(gotRoot, root) {
			t.Fatalf("get: %+v %q %v %v (root %q)", got, gotRoot, found, err, root)
		}
	}
	if _, _, err := u.List("someone-else"); err == nil {
		t.Fatal("a conversation that is not an open lead was listed")
	}
	if err := u.RemoveIf(f.lead, "build", cmd.Revision+1); !errors.Is(err, wsusercmd.ErrStale) {
		t.Fatalf("stale removal: %v", err)
	}
}

// tokenAgents answers the daemon's HTTP MCP route with a fixed hello per token
// over the fixture's real runner (whose own lifecycle the fixture owns).
type tokenAgents struct {
	*wsagent.Runner
	hellos map[string]wsdashboard.ProxyHello
}

func (a *tokenAgents) Start(func(string, string)) {}
func (a *tokenAgents) Close()                     {}
func (a *tokenAgents) ResolveMCPToken(token string) (wsdashboard.ProxyHello, <-chan struct{}, bool) {
	h, ok := a.hellos[token]
	return h, nil, ok
}

// TestHostedLeadsFirstToolListWaitsForTheUserCommandsAlone: while the daemon's
// user commands are not settled, a hosted lead's first tool list waits (bounded)
// and then names the user command tools (only discovery waits; a registration
// refuses at once while the store is not open); no other session waits for it: not a
// worker's establishment or tool list, not a second lead's establishment, not a
// request on a session that is already open, and not the first lead's own
// establishment (the HTTP route builds a session under a daemon-wide lock).
func TestHostedLeadsFirstToolListWaitsForTheUserCommandsAlone(t *testing.T) {
	f := newCallerBindingFixture(t)
	ready := make(chan struct{})
	f.host.userCmdsReady = ready
	hello := func(conversation string) wsdashboard.ProxyHello {
		h := f.hello
		h.Conversation, h.Version = conversation, "0.50.2"
		return h
	}
	agents := &tokenAgents{Runner: f.runner, hellos: map[string]wsdashboard.ProxyHello{
		"tok-lead1": hello(f.lead), "tok-lead2": hello(f.lead), "tok-worker": hello(f.worker),
	}}
	d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: t.TempDir(), Port: 0, Version: "0.50.2", LockWait: 200 * time.Millisecond, Proxy: f.host, Agents: agents})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	url := "http://127.0.0.1:" + strconv.Itoa(d.Endpoint().Port) + wsdashboard.ProxyHTTPPath
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}}
	var seq atomic.Int64
	var callParams func(token, method, params string) (map[string]any, error)
	const initParams = `{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"claude-code","version":"2.1.326"}}`
	call := func(token, method string) (map[string]any, error) { return callParams(token, method, initParams) }
	callParams = func(token, method, params string) (map[string]any, error) {
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, seq.Add(1), method, params)
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &out) != nil || out["error"] != nil {
			return nil, fmt.Errorf("%s %s = %d %s", token, method, resp.StatusCode, raw)
		}
		return out, nil
	}
	names := func(r map[string]any) []string {
		var out []string
		tools, _ := r["result"].(map[string]any)["tools"].([]any)
		for _, tool := range tools {
			out = append(out, tool.(map[string]any)["name"].(string))
		}
		return out
	}
	within := func(what string, limit time.Duration, fn func() error) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- fn() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
		case <-time.After(limit):
			t.Fatalf("%s waited for another session's user commands", what)
		}
	}
	// A worker session is open before the first lead lists.
	within("the worker's initialize", 3*time.Second, func() error { _, err := call("tok-worker", "initialize"); return err })

	type listed struct {
		names []string
		err   error
	}
	first := make(chan listed, 1)
	within("the first lead's establishment", 3*time.Second, func() error { _, err := call("tok-lead1", "initialize"); return err })
	go func() {
		r, err := call("tok-lead1", "tools/list")
		first <- listed{names(r), err}
	}()
	select {
	case l := <-first:
		t.Fatalf("the first lead listed before the user commands settled: %+v", l)
	case <-time.After(300 * time.Millisecond):
	}
	// While it waits: another worker call on its open session, the worker's own
	// list, and a second lead's establishment all go through at once.
	within("a request on the worker's open session", 3*time.Second, func() error { _, err := call("tok-worker", "tools/list"); return err })
	within("the second lead's establishment", 3*time.Second, func() error { _, err := call("tok-lead2", "initialize"); return err })
	// A registration never waits for the store: before it is open it refuses at
	// once, while the first lead's tool list is still waiting.
	within("a registration before the store is open", 3*time.Second, func() error {
		r, err := callParams("tok-lead2", "tools/call", `{"name":"session.register_user_command","arguments":{"id":"x","label":"X","command":"echo x"}}`)
		if err != nil {
			return err
		}
		res, _ := r["result"].(map[string]any)
		text, _ := json.Marshal(res)
		if res["isError"] != true || !strings.Contains(string(text), "has not opened their store") {
			return fmt.Errorf("the registration was not refused as not ready: %s", text)
		}
		return nil
	})
	select {
	case l := <-first:
		t.Fatalf("the first lead listed before the user commands settled: %+v", l)
	default:
	}

	u, err := openUserCommands(t.TempDir(), f.runner, nil, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = u.Close() })
	f.host.userCmds.Store(u)
	close(ready)
	select {
	case l := <-first:
		if l.err != nil || !slices.Contains(l.names, "session.register_user_command") {
			t.Fatalf("the first lead's list after the settle: %+v", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first lead's tool list never resumed")
	}
	// The worker never lists them.
	r, err := call("tok-worker", "tools/list")
	if err != nil || slices.Contains(names(r), "session.register_user_command") {
		t.Fatalf("the worker's list: %v %v", names(r), err)
	}
}

// TestHostedConnectionsNeverWaitInAccept: building a session is instant
// whatever the user commands' readiness is; only the lookup of a lead waits, so
// a connection with no conversation or a worker never does either.
func TestHostedConnectionsNeverWaitInAccept(t *testing.T) {
	f := newCallerBindingFixture(t)
	f.host.userCmdsReady = make(chan struct{})
	for name, conversation := range map[string]string{"lead": f.lead, "worker": f.worker, "none": ""} {
		hello := f.hello
		hello.Conversation = conversation
		done := make(chan struct{})
		go func() {
			defer close(done)
			if session, err := f.host.Accept(hello); err == nil {
				if s, ok := session.(*proxyMCPSession); ok {
					s.server.Close()
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("Accept for %s waited for the user commands", name)
		}
	}
	// A worker's and a plain connection's lookup never waits either.
	for _, conversation := range []string{f.worker, ""} {
		start := time.Now()
		if host := f.host.userCommandsFor(conversation)(); host != nil || time.Since(start) > time.Second {
			t.Fatalf("conversation %q: lookup %v after %v", conversation, host, time.Since(start))
		}
	}
}
