package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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
	"github.com/kang-sw/devenv/internal/wsfs"
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
			args, err := json.Marshal(map[string]any{
				"cmd": "printf 'started\n'; read value; printf 'finished\n'", "session_key": key, "stdin_open": true,
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
		return werkReplacementResponse(item)
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

func werkReplacementResponse(item map[string]any) (*http.Response, error) {
	event, err := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
		"id": "fixture-response", "status": "completed", "output": []any{item},
	}})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader("event: response.completed\ndata: " + string(event) + "\n\n"))}, nil
}

type werkDaemonFixture struct {
	t                 *testing.T
	state, home, root string
	config            wsconfig.Options
	reports           chan wsagent.TurnReport
	credential        wscred.Source
	configured        func(string) (bool, error)
	log               io.Writer
}

func newWerkDaemonFixture(t *testing.T) *werkDaemonFixture {
	t.Helper()
	f := &werkDaemonFixture{t: t, state: t.TempDir(), home: t.TempDir(),
		reports: make(chan wsagent.TurnReport, 8), log: replacementTestLog{t},
		credential: fixtureWerkCredential{}, configured: func(string) (bool, error) { return true, nil }}
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", f.home)
	f.root = initGitFixtureForHookTest(t)
	f.config = wsconfig.Options{ConfigHome: f.home}
	resolver := wsconfig.NewResolver(f.config, nil, nil, nil)
	if err := resolver.Set(wsconfig.HostedPluginPackageKey, "ws", wsconfig.SetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := wsconfig.SetGlobalAgentsTierForHarness(f.config, "small", "", "vendor/fixture", wsagent.BackendWerk, "medium"); err != nil {
		t.Fatal(err)
	}
	return f
}

type werkDaemonOpening struct {
	runner   *wsagent.Runner
	daemon   *wsdashboard.Daemon
	jobs     *execjob.Hosted
	backends map[string]wsagent.BackendRegistration
}

type werkProxyFileHook struct {
	*proxyHost
	record func(context.Context, wsfs.FileCall, func(context.Context, wsfs.FileCall) error) error
}

func (h werkProxyFileHook) Accept(hello wsdashboard.ProxyHello) (wsdashboard.ProxySession, error) {
	if hello.FileWrites != nil && h.record != nil {
		bound := *hello.FileWrites
		orig := bound.Record
		bound.Record = func(ctx context.Context, call wsfs.FileCall) error {
			return h.record(ctx, call, orig)
		}
		hello.FileWrites = &bound
	}
	return h.proxyHost.Accept(hello)
}

func (f *werkDaemonFixture) open(record func(context.Context, wsfs.FileCall, func(context.Context, wsfs.FileCall) error) error) werkDaemonOpening {
	t := f.t
	t.Helper()
	link := &hostedExecLink{}
	holders := newDashboardHolders(f.state, io.Discard, link)
	keys := mcp.NewAgentKeyStore()
	backends := wsagent.WithProviderCredentials(wsagent.BuiltinBackendRegistrations(), f.credential, f.configured)
	r := wsagent.NewRunner(wsagent.Options{
		Dir: f.state, Config: f.config, Keys: keys, LeadKeys: keys, Log: f.log, Backends: backends, DaemonMCP: agentDaemonMCP,
		Holders: wsagent.ManagerHost{Manager: holders},
		Report:  func(rep wsagent.TurnReport) { f.reports <- rep },
	})
	r.ExpectExecJobs()
	host := &proxyHost{dir: f.state, logw: f.log, agents: r}
	d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: f.state, Home: f.home, Version: version, Proxy: werkProxyFileHook{host, record}, Log: f.log, Holders: holders, Agents: r})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	host.live.Store(d)
	jobs := openHostedExec(f.state, holders, r, link, io.Discard, r.ExecJobsChanged)
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
	return werkDaemonOpening{runner: r, daemon: d, jobs: jobs, backends: backends}
}

func (f *werkDaemonFixture) spawn(open werkDaemonOpening, prompt, authority string) string {
	f.t.Helper()
	const parent = "boundworkertestparent"
	spawned, err := open.runner.AgentSpawn(wsagent.LeadCaller(parent, f.root, wsagent.BackendClaude, f.config), wsagent.AgentSpawnRequest{
		Tier: wsagent.BackendWerk + ".small", Prompt: prompt, Authority: authority,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.TrimPrefix(spawned.Address, "id:")
}

func installWerkFixtureTransport(t *testing.T, transport func(http.RoundTripper) http.RoundTripper) {
	t.Helper()
	previous := http.DefaultClient
	wrapped := previous.Transport
	if wrapped == nil {
		wrapped = http.DefaultTransport
	}
	http.DefaultClient = &http.Client{Transport: transport(wrapped)}
	t.Cleanup(func() { http.DefaultClient = previous })
}

func TestWerkRejoinsAuthenticatedExecAcrossDaemonReplacement(t *testing.T) {
	fixture := newWerkDaemonFixture(t)
	state, root, reports := fixture.state, fixture.root, fixture.reports
	transport := &werkReplacementTransport{continuation: make(chan []json.RawMessage, 1), failure: make(chan string, 1)}
	installWerkFixtureTransport(t, func(wrapped http.RoundTripper) http.RoundTripper {
		transport.wrapped = wrapped
		return transport
	})
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
	old := fixture.open(nil)
	id := fixture.spawn(old, "run one job", "workdir-write")
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
	history, err := fixture.readRecord(&old, id)
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
	if binary := os.Getenv("WS_WERK_0511_TEST_BINARY"); binary != "" {
		fixture.run0511(binary, id, job)
		after, _ := fixture.activeLog(nil, id)
		if string(log) != string(after) {
			t.Fatal("0.51.1 changed the active transcript")
		}
	}
	next := fixture.open(nil)
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
	history, err = fixture.readRecord(nil, id)
	if err != nil || history.LastReportedTurn != turnID {
		t.Fatalf("the projected delivered report lost its original turn identity: %v", err)
	}
	if transport.execCalls.Load() != 1 || len(reports) != 0 {
		t.Fatal("replacement repeated the launch or reported the turn twice")
	}
}

// Build the companion test into a go test binary using the v0.51.1
// sources, then set WS_WERK_0511_TEST_BINARY for the replacement test.
// The release source is archived into a temporary directory, so the
// current worktree and installed binaries play no role in the downgrade.
func (f *werkDaemonFixture) run0511(binary, id string, job execjob.HostedView) {
	t := f.t
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestWerk0511DowngradeFixture$", "-test.v", "-test.timeout=15s")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "WS_WERK_0511_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "WS_WERK_0511_STATE="+f.state, "WS_WERK_0511_HOME="+f.home,
		"WS_WERK_0511_WORKER="+id, "WS_WERK_0511_JOB="+job.ID, "WS_WERK_0511_HOLDER="+job.HolderID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("v0.51.1 daemon fixture failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestWerk0511DowngradeFixture") {
		t.Fatal("the release test binary did not exercise the downgrade")
	}
	t.Log("v0.51.1 loaded the history projection and retained the running exec job without a worker report")
}
