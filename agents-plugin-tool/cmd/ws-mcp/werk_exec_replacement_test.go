package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

type replacementTestLog struct{ test *testing.T }

func (l replacementTestLog) Write(data []byte) (int, error) {
	l.test.Log(strings.TrimSpace(string(data)))
	return len(data), nil
}

type fixtureWerkCredential struct{}

func (fixtureWerkCredential) Credential(context.Context, wscred.Request) (wscred.Credential, error) {
	return wscred.Credential{Secret: wscred.NewSecret("fixture-provider-key")}, nil
}

type werkReplacementTransport struct {
	wrapped      http.RoundTripper
	modelCalls   atomic.Int32
	execCalls    atomic.Int32
	continuation chan []json.RawMessage
	failure      chan string
}

func (w *werkReplacementTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Hostname() == "openrouter.ai" {
		var input struct {
			Instructions string
			Input        []json.RawMessage
			Tools        []struct{ Name string }
		}
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			_ = req.Body.Close()
			return nil, err
		}
		_ = req.Body.Close()
		var item map[string]any
		switch w.modelCalls.Add(1) {
		case 1:
			var names []string
			for _, tool := range input.Tools {
				names = append(names, tool.Name)
			}
			for _, expected := range []string{"exec_shell", "fs_read", "fs_grep", "fs_list", "fs_edit", "fs_write"} {
				if !slices.Contains(names, expected) {
					w.failure <- fmt.Sprintf("the authenticated MCP list did not offer %s: %v", expected, names)
					return nil, fmt.Errorf("missing worker tool %s", expected)
				}
			}
			_, rest, ok := strings.Cut(input.Instructions, "Your ws session key is `")
			key, _, _ := strings.Cut(rest, "`")
			if !ok || key == "" {
				return nil, fmt.Errorf("the worker has no session key in its instructions")
			}
			args, err := json.Marshal(map[string]string{
				"cmd": "printf 'started\n'; read value; printf 'finished\n'", "session_key": key,
			})
			if err != nil {
				return nil, err
			}
			item = map[string]any{"type": "function_call", "id": "fc_1", "call_id": "c1", "name": "exec_shell", "arguments": string(args)}
		case 2:
			w.continuation <- input.Input
			item = map[string]any{"type": "message", "id": "msg_1", "role": "assistant",
				"content": []map[string]string{{"type": "output_text", "text": "the exec job was recovered"}}}
		default:
			// Job completion can supply input for the next turn. Leave that
			// turn pending so the original report is the only one expected.
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		event, err := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "fixture-response", "status": "completed", "output": []any{item},
		}})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader("event: response.completed\ndata: " + string(event) + "\n\n"))}, nil
	}
	if req.URL.Path != wsdashboard.ProxyHTTPPath || req.GetBody == nil {
		return w.wrapped.RoundTrip(req)
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	var call struct {
		Method string
		Params struct{ Name string }
	}
	err = json.NewDecoder(body).Decode(&call)
	_ = body.Close()
	if err != nil {
		return nil, err
	}
	if call.Method != "tools/call" || (call.Params.Name != "exec.shell" && call.Params.Name != "exec_shell") {
		return w.wrapped.RoundTrip(req)
	}
	if w.execCalls.Add(1) != 1 {
		return nil, fmt.Errorf("recovery tried to launch the same command again")
	}
	res, err := w.wrapped.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		return nil, err
	}
	if strings.Contains(string(raw), `"isError":true`) || strings.Contains(string(raw), `"error":`) {
		w.failure <- "the authenticated server refused the fixture job"
		res.Body = io.NopCloser(strings.NewReader(string(raw)))
		return res, nil
	}
	// The holder may run, but this reply must never commit in the Werk log.
	// In replacement the MCP server drains first and the old loop is then
	// cancelled, so the HTTP client can lose the reply without stopping the job.
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestWerkRejoinsAuthenticatedExecAcrossDaemonReplacement(t *testing.T) {
	state, home := t.TempDir(), t.TempDir()
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", home)
	root := initGitFixtureForHookTest(t)
	cfg := wsconfig.Options{ConfigHome: home}
	resolver := wsconfig.NewResolver(cfg, nil, nil, nil)
	if err := resolver.Set(wsconfig.HostedPluginPackageKey, "ws", wsconfig.SetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := wsconfig.SetGlobalAgentsTierForHarness(cfg, "small", "", "vendor/fixture", wsagent.BackendWerk, "medium"); err != nil {
		t.Fatal(err)
	}
	previousClient := http.DefaultClient
	transport := &werkReplacementTransport{wrapped: previousClient.Transport, continuation: make(chan []json.RawMessage, 1), failure: make(chan string, 1)}
	if transport.wrapped == nil {
		transport.wrapped = http.DefaultTransport
	}
	http.DefaultClient = &http.Client{Transport: transport}
	t.Cleanup(func() { http.DefaultClient = previousClient })

	type opening struct {
		runner *wsagent.Runner
		daemon *wsdashboard.Daemon
		jobs   *execjob.Hosted
	}
	reports := make(chan wsagent.TurnReport, 8)
	open := func() opening {
		link := &hostedExecLink{}
		holders := newDashboardHolders(state, io.Discard, link)
		keys := mcp.NewAgentKeyStore()
		backends := wsagent.WithProviderCredentials(wsagent.BuiltinBackendRegistrations(), fixtureWerkCredential{}, func(string) (bool, error) { return true, nil })
		r := wsagent.NewRunner(wsagent.Options{
			Dir: state, Config: cfg, Keys: keys, Log: replacementTestLog{t}, Backends: backends, DaemonMCP: agentDaemonMCP,
			Holders: wsagent.ManagerHost{Manager: holders},
			Report:  func(rep wsagent.TurnReport) { reports <- rep },
		})
		r.ExpectExecJobs()
		host := &proxyHost{dir: state, logw: replacementTestLog{t}, agents: r}
		d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: state, Home: home, Version: version, Proxy: host, Log: replacementTestLog{t}, Holders: holders, Agents: r})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(d.Close)
		host.live.Store(d)
		jobs := openHostedExec(state, holders, r, link, io.Discard, r.ExecJobsChanged)
		if jobs == nil {
			t.Fatal("hosted exec jobs unavailable")
		}
		t.Cleanup(func() {
			// The fixture owns this isolated holder manager and all its jobs.
			for _, h := range holders.List().Holders {
				_ = holders.Stop(h.ID)
			}
			d.Close()
			_ = jobs.Close()
		})
		host.exec.Store(jobs)
		r.BindExecJobs(jobs)
		return opening{r, d, jobs}
	}
	wait := func(what string, ready func() bool) {
		t.Helper()
		until := time.Now().Add(10 * time.Second)
		for !ready() {
			if time.Now().After(until) {
				t.Fatalf("timed out waiting for %s (model calls %d, exec calls %d)", what, transport.modelCalls.Load(), transport.execCalls.Load())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	old := open()
	const parent = "boundworkertestparent"
	spawned, err := old.runner.AgentSpawn(wsagent.LeadCaller(parent, root, wsagent.BackendClaude, cfg), wsagent.AgentSpawnRequest{
		Tier: wsagent.BackendWerk + ".small", Prompt: "run one job", Authority: "workdir-write",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(spawned.Address, "id:")
	var job execjob.HostedView
	wait("the first job's output", func() bool {
		select {
		case failure := <-transport.failure:
			t.Fatal(failure)
		case req := <-transport.continuation:
			for _, raw := range req {
				var item struct{ Type, Output string }
				if json.Unmarshal(raw, &item) == nil && item.Type == "function_call_output" {
					t.Fatalf("the model continued before a job started: %s", item.Output)
				}
			}
			t.Fatal("the model continued without an exec result")
		default:
		}
		if st, err := old.runner.Get(id); err != nil || st.Error != "" {
			t.Fatalf("worker stopped before launch: %s (%v)", st.Error, err)
		}
		rows, _ := old.jobs.List(id)
		if len(rows) != 1 {
			return false
		}
		view, tail, err := old.jobs.Tail(id, rows[0].ID, 0)
		if view.Terminal() {
			t.Fatalf("job ended before replacement: %s (%s)", view.State, view.Error)
		}
		if err != nil || !strings.Contains(string(tail.Data), "started") {
			return false
		}
		job = view
		return job.State == execjob.HostedRunning
	})
	history, err := wsagent.ReadRecord(state, id)
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join(wsdashboard.SessionDir(state, id), "werk", history.SessionID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var turnID string
	for _, line := range strings.Split(string(log), "\n") {
		var row struct {
			Kind   string `json:"k"`
			TurnID string `json:"turn_id"`
		}
		if json.Unmarshal([]byte(line), &row) == nil && row.Kind == "turn_start" {
			turnID = row.TurnID
		}
	}
	if turnID == "" || strings.Contains(string(log), "function_call_output") {
		t.Fatal("expected an active logged turn with the exec result still pending")
	}

	old.daemon.Close()
	if err := old.jobs.Close(); err != nil {
		t.Fatal(err)
	}
	next := open()
	// Register cleanup on the successor even if an assertion finds the
	// original job still live. It must end before the test releases its files.
	select {
	case req := <-transport.continuation:
		got, err := json.Marshal(req)
		if err != nil || !strings.Contains(string(got), "Recovered exec launch") ||
			!strings.Contains(string(got), job.ID) || !strings.Contains(string(got), "status: running") {
			t.Fatal("the resumed model did not receive the original running job")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no model continuation after replacement")
	}
	wait("the original turn's report hold", func() bool {
		st, _ := next.runner.Get(id)
		return st.Holding
	})
	if rows, _ := next.jobs.List(id); len(rows) != 1 || rows[0].ID != job.ID || rows[0].HolderID != job.HolderID {
		t.Fatal("the successor did not retain the same job and holder")
	}
	if _, err := next.jobs.FenceRoot(root); err == nil {
		t.Fatal("the worktree was releasable while the original job ran")
	}
	select {
	case <-reports:
		t.Fatal("the original turn reported before its job ended")
	default:
	}
	if _, err := next.jobs.Write(id, job.ID, []byte("continue\n"), true); err != nil {
		t.Fatal(err)
	}
	select {
	case rep := <-reports:
		if rep.Result.ID != id || rep.Result.Message != "the exec job was recovered" {
			t.Fatalf("settled the wrong result: %+v", rep.Result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the report stayed held after the job ended")
	}

	unfence, err := next.jobs.FenceRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	unfence()
	next.daemon.Close()
	history, err = wsagent.ReadRecord(state, id)
	if err != nil || history.LastReportedTurn != turnID {
		t.Fatalf("the projected delivered report lost its original turn identity: %v", err)
	}
	if transport.execCalls.Load() != 1 || len(reports) != 0 {
		t.Fatal("replacement repeated the launch or reported the turn twice")
	}
}
