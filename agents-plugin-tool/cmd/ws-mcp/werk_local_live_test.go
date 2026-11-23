package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// TestWerkLiveLocal is opt-in: WERK_LIVE_LOCAL=1, WERK_LIVE_LOCAL_BASE_URL
// (API root), WERK_LIVE_LOCAL_MODEL, and optionally WERK_LIVE_LOCAL_API_KEY.
// It neither downloads models nor reads or changes the user's credential store.
func TestWerkLiveLocal(t *testing.T) {
	if os.Getenv("WERK_LIVE_LOCAL") != "1" {
		t.Skip("requires WERK_LIVE_LOCAL=1 and an existing tool-capable Ollama model")
	}
	base := strings.TrimRight(os.Getenv("WERK_LIVE_LOCAL_BASE_URL"), "/")
	model := os.Getenv("WERK_LIVE_LOCAL_MODEL")
	if base == "" || model == "" {
		t.Fatal("set WERK_LIVE_LOCAL_BASE_URL and WERK_LIVE_LOCAL_MODEL")
	}
	f := newWerkDaemonFixture(t)
	f.log = io.Discard
	raw, err := json.Marshal(map[string]wsconfig.WerkEndpoint{"fixture": {BaseURL: base, Kind: "ollama"}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := wsconfig.NewResolver(f.config, nil, nil, nil)
	if err := resolver.Set(wsconfig.ItemWerkEndpoints, string(raw), wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if _, err := wsconfig.SetAgentTierValue(f.config, true, "small", wsagent.BackendWerk, wsconfig.AgentTier{Provider: "local/fixture", Model: model}); err != nil {
		t.Fatal(err)
	}
	store := wscred.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err := store.LoadLocalEndpoints(f.config); err != nil {
		t.Fatal(err)
	}
	key := os.Getenv("WERK_LIVE_LOCAL_API_KEY")
	if key != "" {
		if _, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: "local/fixture", APIKey: wscred.NewSecret(key)}); err != nil {
			t.Fatal("could not set isolated optional credential")
		}
	}
	f.credential, f.configured = store, credentialStatus(store)
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	secret := hex.EncodeToString(nonce)
	if err := os.WriteFile(filepath.Join(f.root, werkLivePath), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	gate := &werkLocalLiveTransport{endpoint: base + "/responses", model: model, key: key}
	installWerkFixtureTransport(t, func(wrapped http.RoundTripper) http.RoundTripper { gate.wrapped = wrapped; return gate })
	http.DefaultClient.Timeout = 90 * time.Second
	http.DefaultClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	open := f.open(nil)
	gate.proxyPort = fmt.Sprint(open.daemon.Endpoint().Port)
	id := f.spawn(open, fmt.Sprintf("Call fs_read exactly once with path %q and your session_key. Then reply with only the exact file contents. Do not call other tools.", werkLivePath), "read-only")
	select {
	case report := <-f.reports:
		if report.Result.ID != id || report.Result.Status != wsagent.TurnDone || !strings.Contains(report.Result.Message, secret) {
			t.Fatalf("worker report failed validation (status %s, requests %d, HTTP %d, tool calls %d)", report.Result.Status, gate.requests.Load(), gate.status.Load(), gate.steps.Load())
		}
	case <-time.After(3 * time.Minute):
		t.Fatalf("worker report timed out (requests %d, HTTP %d, tool calls %d)", gate.requests.Load(), gate.status.Load(), gate.steps.Load())
	}
	open.daemon.Close()
	if gate.steps.Load() != 1 || len(f.reports) != 0 {
		t.Fatal("expected one real tool call and one completed report")
	}
	t.Logf("model %s: %d real Responses requests, HTTP %d, one successful file read, one completed report containing the file nonce; optional auth=%t", model, gate.requests.Load(), gate.status.Load(), key != "")
}

// Admit only the configured provider and the isolated daemon's read operation.
// Never log request bodies, authorization values, tool results, or provider errors.
type werkLocalLiveTransport struct {
	wrapped                         http.RoundTripper
	endpoint, proxyPort, model, key string
	requests, status, steps         atomic.Int32
}

func (g *werkLocalLiveTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.Body == nil {
		return nil, errors.New("fixture requires POST with a body")
	}
	defer req.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(req.Body, 4<<20))
	if err != nil {
		return nil, errors.New("fixture could not read request")
	}
	switch {
	case req.URL.String() == g.endpoint:
		if g.requests.Add(1) > 4 {
			return nil, errors.New("fixture model request budget exceeded")
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(raw, &body) != nil {
			return nil, errors.New("invalid provider request")
		}
		var model string
		if json.Unmarshal(body["model"], &model) != nil || model != g.model || body["tool_choice"] != nil {
			return nil, errors.New("unexpected model or Ollama tool_choice")
		}
		wantAuth := ""
		if g.key != "" {
			wantAuth = "Bearer " + g.key
		}
		if req.Header.Get("Authorization") != wantAuth {
			return nil, errors.New("unexpected optional authorization")
		}
		var tools []struct{ Name string }
		var definitions []json.RawMessage
		if json.Unmarshal(body["tools"], &tools) != nil || json.Unmarshal(body["tools"], &definitions) != nil {
			return nil, errors.New("invalid tools")
		}
		var allowed []json.RawMessage
		for i, tool := range tools {
			if tool.Name == "fs_read" {
				allowed = append(allowed, definitions[i])
			}
		}
		if len(allowed) != 1 {
			return nil, errors.New("missing read tool")
		}
		body["tools"], _ = json.Marshal(allowed)
		body["max_output_tokens"] = json.RawMessage("1024")
		raw, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	case req.URL.Scheme == "http" && net.ParseIP(req.URL.Hostname()).IsLoopback() &&
		req.URL.Port() == g.proxyPort && req.URL.Path == wsdashboard.ProxyHTTPPath && req.URL.RawQuery == "":
		var call struct {
			Method string
			Params struct {
				Name      string
				Arguments map[string]string
			}
		}
		if json.Unmarshal(raw, &call) != nil {
			return nil, errors.New("invalid proxy request")
		}
		switch call.Method {
		case "initialize", "notifications/initialized", "tools/list":
		case "tools/call":
			args := call.Params.Arguments
			if call.Params.Name != "fs.read" || args["path"] != werkLivePath || args["session_key"] == "" || len(args) != 2 || !g.steps.CompareAndSwap(0, 1) {
				return nil, errors.New("fixture refused tool operation")
			}
		default:
			return nil, errors.New("fixture refused proxy method")
		}
	default:
		return nil, errors.New("fixture refused HTTP destination")
	}
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(bytes.NewReader(raw))
	out.ContentLength = int64(len(raw))
	out.GetBody = nil
	res, err := g.wrapped.RoundTrip(out)
	if err != nil {
		return nil, errors.New("fixture HTTP transport failed")
	}
	if req.URL.String() == g.endpoint {
		g.status.Store(int32(res.StatusCode))
	}
	return res, nil
}
