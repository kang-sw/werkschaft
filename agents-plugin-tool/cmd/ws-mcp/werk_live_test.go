package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

const (
	werkLivePath    = "fixture.txt"
	werkLiveBefore  = "werk-live-before"
	werkLiveAfter   = "werk-live-after"
	werkLiveCommand = "printf werk-live-exec"
	werkLiveCalls   = 6
	werkLiveTokens  = 512
)

// TestWerkLiveOpenRouter runs only with WERK_LIVE_OPENROUTER=1 and needs a
// configured global werk.small tier and an existing OpenRouter store entry.
// It uses the store read-only and runs its own daemon and holder in temp dirs.
func TestWerkLiveOpenRouter(t *testing.T) {
	if os.Getenv("WERK_LIVE_OPENROUTER") != "1" {
		t.Skip("live provider verification requires WERK_LIVE_OPENROUTER=1")
	}
	path, err := wscred.DefaultPath("")
	if err != nil {
		t.Fatal("could not resolve the credential store")
	}
	store := wscred.NewStore(path)
	configured := credentialStatus(store)
	ready, err := configured(string(wscred.ProviderOpenRouter))
	if err != nil || !ready {
		t.Fatal("the configured credential store needs an OpenRouter entry")
	}
	tiers, err := wsconfig.HarnessTiers(wsconfig.Options{}, wsagent.BackendWerk)
	if err != nil || tiers["small"].Model == "" {
		t.Fatal("the configured global werk.small tier needs an OpenRouter model")
	}
	tier := tiers["small"]
	f := newWerkDaemonFixture(t)
	f.credential, f.configured, f.log = store, configured, io.Discard
	if _, err := wsconfig.SetGlobalAgentsTierForHarness(f.config, "small", "", tier.Model, wsagent.BackendWerk, tier.Effort); err != nil {
		t.Fatal("could not configure the isolated tier")
	}
	runWerkLiveFixture(t, f, tier.Model, nil)
}

func runWerkLiveFixture(t *testing.T, f *werkDaemonFixture, model string, provider http.RoundTripper) {
	t.Helper()
	target := filepath.Join(f.root, werkLivePath)
	if err := os.WriteFile(target, []byte(werkLiveBefore), 0o600); err != nil {
		t.Fatal("could not seed the isolated file")
	}
	gate := &werkLiveTransport{model: model}
	installWerkFixtureTransport(t, func(wrapped http.RoundTripper) http.RoundTripper {
		gate.wrapped, gate.provider = wrapped, provider
		if gate.provider == nil {
			gate.provider = wrapped
		}
		return gate
	})
	http.DefaultClient.Timeout = 30 * time.Second
	http.DefaultClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	open := f.open(nil)
	gate.proxy.Store(fmt.Sprintf("http://127.0.0.1:%d%s", open.daemon.Endpoint().Port, wsdashboard.ProxyHTTPPath))
	id := f.spawn(open, fmt.Sprintf("Test the fixture in your working directory. In order, call fs_read on %q, "+
		"using your session_key, fs_write on the same path with content exactly %q, "+
		"then exec_shell with cmd exactly %q and your session_key. "+
		"Pass only path and content to fs_write. Use these three tools once each. Reply briefly after they finish.", werkLivePath, werkLiveAfter, werkLiveCommand), "workdir-write")
	select {
	case report := <-f.reports:
		if report.Result.ID != id || report.Result.Status != wsagent.TurnDone {
			t.Fatalf("worker did not complete its turn (requests %d, last HTTP status %d, tool steps %d)",
				gate.requests.Load(), gate.status.Load(), gate.steps.Load())
		}
	case <-time.After(2 * time.Minute):
		t.Fatalf("timed out waiting for the worker report (requests %d, last HTTP status %d, tool steps %d)",
			gate.requests.Load(), gate.status.Load(), gate.steps.Load())
	}
	if gate.steps.Load() != 3 {
		t.Fatalf("completed %d of 3 tool calls", gate.steps.Load())
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != werkLiveAfter {
		t.Fatal("the file did not contain the expected bytes")
	}
	jobs, _ := open.jobs.List(id)
	if len(jobs) != 1 || jobs[0].State != execjob.HostedExited || jobs[0].ExitCode == nil || *jobs[0].ExitCode != 0 {
		t.Fatal("the worker did not finish exactly one hosted exec job")
	}
	_, tail, err := open.jobs.Tail(id, jobs[0].ID, 0)
	if err != nil || !bytes.Contains(tail.Data, []byte("werk-live-exec")) {
		t.Fatal("the hosted job did not produce its expected output")
	}
	open.daemon.Close()
	select {
	case <-f.reports:
		t.Fatal("the worker reported more than once")
	default:
	}
	t.Logf("model %s made %d Responses requests, read and wrote the file, ran one hosted job, and delivered one report",
		model, gate.requests.Load())
}

// werkLiveTransport confines model-selected effects to the three fixture
// operations and keeps provider payloads and errors out of the test output.
type werkLiveTransport struct {
	wrapped, provider http.RoundTripper
	model             string
	proxy             atomic.Value // string
	callMu            sync.Mutex
	callUncertain     bool
	requests          atomic.Int32
	status            atomic.Int32
	steps             atomic.Int32
}

func (g *werkLiveTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		return nil, errors.New("fixture requires an HTTP request body")
	}
	if req.Method != http.MethodPost {
		_ = req.Body.Close()
		return nil, errors.New("fixture allows only POST requests")
	}
	proxy, _ := g.proxy.Load().(string)
	switch req.URL.String() {
	case "https://openrouter.ai/api/v1/responses":
		return g.respond(req)
	case proxy:
		return g.call(req)
	default:
		_ = req.Body.Close()
		return nil, errors.New("fixture refused an unexpected HTTP destination")
	}
}

func (g *werkLiveTransport) respond(req *http.Request) (*http.Response, error) {
	if g.requests.Add(1) > werkLiveCalls {
		_ = req.Body.Close()
		return nil, errors.New("fixture exceeded the model request budget")
	}
	defer req.Body.Close()
	var body map[string]json.RawMessage
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, errors.New("fixture could not decode the model request")
	}
	var model string
	if err := json.Unmarshal(body["model"], &model); err != nil || model != g.model {
		return nil, errors.New("fixture refused an unexpected model")
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(body["tools"], &tools); err != nil {
		return nil, errors.New("fixture could not decode the tool list")
	}
	allowed := make([]json.RawMessage, 0, 3)
	for _, raw := range tools {
		var tool struct{ Name string }
		if err := json.Unmarshal(raw, &tool); err != nil {
			return nil, errors.New("fixture could not decode a tool")
		}
		switch tool.Name {
		case "fs_read", "fs_write", "exec_shell":
			allowed = append(allowed, raw)
		}
	}
	if len(allowed) != 3 {
		return nil, errors.New("fixture did not receive its three tools")
	}
	limited, err := json.Marshal(allowed)
	if err != nil {
		return nil, errors.New("fixture could not limit the tool list")
	}
	body["tools"] = limited
	body["max_output_tokens"] = json.RawMessage(fmt.Sprint(werkLiveTokens))
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("fixture could not encode the bounded model request")
	}
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(bytes.NewReader(raw))
	out.ContentLength = int64(len(raw))
	out.GetBody = nil
	res, err := g.provider.RoundTrip(out)
	if err != nil {
		return nil, errors.New("fixture provider transport failed")
	}
	g.status.Store(int32(res.StatusCode))
	return res, nil
}

func (g *werkLiveTransport) call(req *http.Request) (*http.Response, error) {
	if req.GetBody == nil {
		_ = req.Body.Close()
		return nil, errors.New("fixture requires a replayable proxy request body")
	}
	body, err := req.GetBody()
	if err != nil {
		_ = req.Body.Close()
		return nil, errors.New("fixture could not copy the proxy request")
	}
	defer body.Close()
	var call struct {
		Method string
		Params struct {
			Name      string
			Arguments map[string]string
		}
	}
	if err := json.NewDecoder(body).Decode(&call); err != nil {
		_ = req.Body.Close()
		return nil, errors.New("fixture could not decode the proxy request")
	}
	switch call.Method {
	case "initialize", "notifications/initialized", "tools/list":
		return g.wrapped.RoundTrip(req)
	case "tools/call":
	default:
		_ = req.Body.Close()
		return nil, errors.New("fixture refused an unexpected proxy method")
	}
	g.callMu.Lock()
	defer g.callMu.Unlock()
	if g.callUncertain {
		_ = req.Body.Close()
		return nil, errors.New("fixture stopped calling tools after an uncertain response")
	}
	args := call.Params.Arguments
	stage := g.steps.Load()
	var want string
	switch {
	case stage == 0 && (call.Params.Name == "fs.read" || call.Params.Name == "fs_read") && args["path"] == werkLivePath && args["session_key"] != "" && len(args) == 2:
		want = werkLiveBefore
	case stage == 1 && (call.Params.Name == "fs.write" || call.Params.Name == "fs_write") && args["path"] == werkLivePath && args["content"] == werkLiveAfter && len(args) == 2:
	case stage == 2 && (call.Params.Name == "exec.shell" || call.Params.Name == "exec_shell") && args["cmd"] == werkLiveCommand && args["session_key"] != "" && len(args) == 2:
		want = "werk-live-exec"
	default:
		_ = req.Body.Close()
		return nil, errors.New("fixture refused a proxy call outside the three allowed operations")
	}
	// Spend the admission before handing the request to the daemon.
	g.callUncertain = true
	res, err := g.wrapped.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	var answer struct {
		Error  json.RawMessage
		Result *struct{ IsError bool }
	}
	if err != nil || res.StatusCode != http.StatusOK || json.Unmarshal(raw, &answer) != nil ||
		answer.Error != nil || answer.Result == nil || answer.Result.IsError || !bytes.Contains(raw, []byte(want)) {
		return nil, errors.New("fixture tool call did not succeed")
	}
	g.steps.Add(1)
	g.callUncertain = false
	res.Body = io.NopCloser(bytes.NewReader(raw))
	return res, nil
}

func TestWerkLiveFixtureWithScriptedProvider(t *testing.T) {
	f := newWerkDaemonFixture(t)
	var calls atomic.Int32
	provider := werkFixtureRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		var wire struct {
			Instructions    string
			Tools           []struct{ Name string }
			MaxOutputTokens int `json:"max_output_tokens"`
		}
		if err := json.NewDecoder(req.Body).Decode(&wire); err != nil || len(wire.Tools) != 3 || wire.MaxOutputTokens != werkLiveTokens {
			t.Error("provider received a request without the fixture limits")
			return nil, errors.New("invalid fixture model request")
		}
		var name string
		var args map[string]string
		_, rest, _ := strings.Cut(wire.Instructions, "Your ws session key is `")
		key, _, _ := strings.Cut(rest, "`")
		n := calls.Add(1)
		switch n {
		case 1:
			name, args = "fs_read", map[string]string{"path": werkLivePath, "session_key": key}
		case 2:
			name, args = "fs_write", map[string]string{"path": werkLivePath, "content": werkLiveAfter}
		case 3:
			name, args = "exec_shell", map[string]string{"cmd": werkLiveCommand, "session_key": key}
		case 4:
			return werkFixtureReply("fixture completed")
		default:
			t.Error("unexpected request after the scripted reply")
			return nil, errors.New("unexpected fixture request")
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, errors.New("could not encode scripted tool call")
		}
		return werkReplacementResponse(map[string]any{
			"type": "function_call", "id": fmt.Sprint("fc_", n), "call_id": fmt.Sprint("call_", n), "name": name, "arguments": string(raw),
		})
	})
	runWerkLiveFixture(t, f, "vendor/fixture", provider)
}

func TestWerkLiveFixtureDoesNotRepeatUncertainEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		step int32
		args map[string]string
	}{
		{"fs.write", 1, map[string]string{"path": werkLivePath, "content": werkLiveAfter}},
		{"exec.shell", 2, map[string]string{"cmd": werkLiveCommand, "session_key": "fixture"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			g := &werkLiveTransport{wrapped: werkFixtureRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				_ = req.Body.Close()
				calls++
				// The local operation ran. Its response stopped before a JSON-RPC result.
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{"))}, nil
			})}
			g.steps.Store(tc.step)
			wire, err := json.Marshal(map[string]any{
				"method": "tools/call", "params": map[string]any{"name": tc.name, "arguments": tc.args},
			})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				req, err := http.NewRequest(http.MethodPost, "http://fixture", bytes.NewReader(wire))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := g.call(req); err == nil {
					t.Fatal("an uncertain operation or its retry passed the gate")
				}
			}
			if calls != 1 || g.steps.Load() != tc.step {
				t.Fatal("the gate dispatched the effect twice or counted it as verified")
			}
		})
	}
}
