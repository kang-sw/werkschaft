package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsweb"
)

type stubCallerBinding struct {
	backend string
}

func (b stubCallerBinding) CallerBackend(conversation, _ string) string {
	if conversation == "" {
		return ""
	}
	return b.backend
}

func (stubCallerBinding) CallerRefusal(_, _ string) string { return "" }

type stubBackendRegistry struct {
	regs map[string]wsagent.BackendRegistration
}

func (r stubBackendRegistry) BackendRegistrations() map[string]wsagent.BackendRegistration {
	return r.regs
}

type staticWebResolver map[string][]net.IPAddr

func (r staticWebResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if addrs, ok := r[host]; ok {
		return addrs, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func newTestPublicFetchTarget(t *testing.T, handler http.HandlerFunc) (wsweb.Options, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	targetAddr := srv.Listener.Addr().String()
	opts := wsweb.Options{
		Resolver: staticWebResolver{
			"public.example": {{IP: net.ParseIP("93.184.216.34")}},
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, targetAddr)
		},
	}
	return opts, srv.Close
}

func invokeToolRaw(t *testing.T, s *Server, name string, args map[string]any) (string, bool, bool) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	resp := s.callTool(context.Background(), request{ID: json.RawMessage("31"), Method: "tools/call", Params: body})
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Error *rpcError `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	if env.Error != nil {
		return env.Error.Message, true, true
	}
	return toolText(t, string(raw)), toolIsError(t, string(raw)), false
}

func TestWebFetchExposureAndCallPathGating(t *testing.T) {
	var hits atomic.Int32
	fetchOpts, closeTarget := newTestPublicFetchTarget(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Doc</title></head><body><h1>Hello</h1><p>Bounded text.</p></body></html>`))
	})
	defer closeTarget()

	cases := []struct {
		name         string
		conversation string
		harness      string
		backend      string
		allowedTools string
		authority    wsfs.Authority
		dynamicAuth  bool
		wantAllowed  bool
	}{
		{
			name:         "claude hosted session denied even when allowlisted",
			conversation: "conv-claude",
			harness:      wsagent.BackendClaude,
			backend:      wsagent.BackendClaude,
			allowedTools: "web.fetch",
			wantAllowed:  false,
		},
		{
			name:         "codex hosted session denied even when allowlisted",
			conversation: "conv-codex",
			harness:      wsagent.BackendCodex,
			backend:      wsagent.BackendCodex,
			allowedTools: "web.fetch",
			wantAllowed:  false,
		},
		{
			name:         "agy hosted session denied even when allowlisted",
			conversation: "conv-agy",
			harness:      wsagent.BackendAgy,
			backend:      wsagent.BackendAgy,
			allowedTools: "web.fetch",
			wantAllowed:  false,
		},
		{
			name:         "unbound session denied even with werk harness hint and allowlist",
			conversation: "",
			harness:      wsagent.BackendWerk,
			backend:      wsagent.BackendWerk,
			allowedTools: "web.fetch",
			wantAllowed:  false,
		},
		{
			name:         "werk hosted session with void authority denied",
			conversation: "conv-void",
			harness:      wsagent.BackendWerk,
			backend:      wsagent.BackendWerk,
			dynamicAuth:  true,
			authority:    "",
			wantAllowed:  false,
		},
		{
			name:         "werk worker without web.fetch in allowlist denied",
			conversation: "conv-private-worker",
			harness:      wsagent.BackendWerk,
			backend:      wsagent.BackendWerk,
			allowedTools: "fs.read",
			authority:    wsfs.AuthorityReadOnly,
			dynamicAuth:  true,
			wantAllowed:  true, // webFetchTools() true, but toolAllowed() denies
		},
		{
			name:         "werk hosted lead read-only admitted",
			conversation: "conv-werk-lead",
			harness:      wsagent.BackendWerk,
			backend:      wsagent.BackendWerk,
			authority:    wsfs.AuthorityReadOnly,
			dynamicAuth:  true,
			wantAllowed:  true,
		},
		{
			name:         "werk hosted worker with web.fetch allowlisted admitted",
			conversation: "conv-werk-worker",
			harness:      wsagent.BackendWerk,
			backend:      wsagent.BackendWerk,
			allowedTools: "fs.read,web.fetch",
			authority:    wsfs.AuthorityWorkdirWrite,
			dynamicAuth:  true,
			wantAllowed:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits.Store(0)
			root := t.TempDir()
			var env []string
			if tc.allowedTools != "" {
				env = append(env, envAllowedTools+"="+tc.allowedTools)
			}
			opts := ServerOptions{
				Root:          root,
				Version:       "test",
				Env:           env,
				Harness:       tc.harness,
				Conversation:  tc.conversation,
				ExecCutover:   tc.backend,
				CallerBinding: stubCallerBinding{backend: tc.backend},
			}
			if tc.dynamicAuth {
				auth := tc.authority
				opts.FileWrites = &FileWriteSession{
					Root:             root,
					Authority:        auth,
					CurrentAuthority: func() wsfs.Authority { return auth },
				}
			}
			s := NewServerWithOptions(opts)
			s.webFetchOpts = fetchOpts

			wantEffective := tc.wantAllowed && (tc.allowedTools == "" || strings.Contains(tc.allowedTools, "web.fetch"))
			advertised := s.connectionDescriptor().aliases.advertised("web.fetch")
			if listed := slices.Contains(listedNames(s), advertised); listed != wantEffective {
				t.Fatalf("web.fetch listed = %v, want %v (listed=%v)", listed, wantEffective, listedNames(s))
			}

			text, failed, _ := invokeToolRaw(t, s, "web.fetch", map[string]any{"url": "http://public.example/page"})
			if failed == wantEffective {
				t.Fatalf("direct web.fetch failed=%v wantEffective=%v text=%q", failed, wantEffective, text)
			}
			if wantEffective {
				if !strings.Contains(text, "URL: http://public.example/page") || !strings.Contains(text, "Title: Doc") || !strings.Contains(text, "Bounded text.") {
					t.Fatalf("unexpected web.fetch output: %q", text)
				}
				if hits.Load() != 1 {
					t.Fatalf("target hits = %d, want 1", hits.Load())
				}
				return
			}
			if hits.Load() != 0 {
				t.Fatalf("denied caller reached target server (%d hits)", hits.Load())
			}

			// Also verify the call envelope path refuses denied callers on harnesses with call fallback.
			if s.toolAllowed("call") {
				envText, envFailed, _ := invokeToolRaw(t, s, "call", map[string]any{
					"tool":      "web.fetch",
					"arguments": map[string]any{"url": "http://public.example/page"},
				})
				if !envFailed {
					t.Fatalf("call envelope should have refused web.fetch, got %q", envText)
				}
				if hits.Load() != 0 {
					t.Fatalf("denied call envelope reached target server (%d hits)", hits.Load())
				}
			}
		})
	}
}

func TestWebFetchGatedByRegistrationDataNotHarnessName(t *testing.T) {
	fetchOpts, closeTarget := newTestPublicFetchTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("registration-driven"))
	})
	defer closeTarget()

	customRegistry := stubBackendRegistry{
		regs: map[string]wsagent.BackendRegistration{
			"custom-fixture": {
				Lifecycle: wsagent.LifecyclePolicy{WebFetchTool: true},
			},
			wsagent.BackendWerk: {
				Lifecycle: wsagent.LifecyclePolicy{WebFetchTool: false},
			},
		},
	}

	// Custom backend with WebFetchTool=true is admitted.
	admitted := NewServerWithOptions(ServerOptions{
		Root:          t.TempDir(),
		Version:       "test",
		Env:           []string{},
		Conversation:  "conv-custom",
		CallerBinding: stubCallerBinding{backend: "custom-fixture"},
		AgentBackends: customRegistry,
	})
	admitted.webFetchOpts = fetchOpts
	if !slices.Contains(listedNames(admitted), "web.fetch") {
		t.Fatalf("custom backend with WebFetchTool=true should list web.fetch")
	}
	text, failed, _ := invokeToolRaw(t, admitted, "web.fetch", map[string]any{"url": "http://public.example/ok"})
	if failed || !strings.Contains(text, "registration-driven") {
		t.Fatalf("custom backend web.fetch failed=%v text=%q", failed, text)
	}

	// Backend named werk with WebFetchTool=false in registration data is denied.
	denied := NewServerWithOptions(ServerOptions{
		Root:          t.TempDir(),
		Version:       "test",
		Env:           []string{},
		Harness:       wsagent.BackendWerk,
		Conversation:  "conv-werk-off",
		ExecCutover:   wsagent.BackendWerk,
		CallerBinding: stubCallerBinding{backend: wsagent.BackendWerk},
		AgentBackends: customRegistry,
	})
	denied.webFetchOpts = fetchOpts
	if slices.Contains(listedNames(denied), "web.fetch") {
		t.Fatalf("backend with WebFetchTool=false must not list web.fetch")
	}
	if text, failed, _ := invokeToolRaw(t, denied, "web.fetch", map[string]any{"url": "http://public.example/ok"}); !failed {
		t.Fatalf("backend with WebFetchTool=false must fail call, got %q", text)
	}
}

func TestWebFetchValidationPrivateRefusalAndOutputCap(t *testing.T) {
	largeBody := strings.Repeat("가나다라abcd\n", 1000)
	fetchOpts, closeTarget := newTestPublicFetchTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(largeBody))
	})
	defer closeTarget()
	fetchOpts.MaxOutputBytes = 512

	s := NewServerWithOptions(ServerOptions{
		Root:          t.TempDir(),
		Version:       "test",
		Env:           []string{},
		Conversation:  "conv-werk",
		CallerBinding: stubCallerBinding{backend: wsagent.BackendWerk},
	})
	s.webFetchOpts = fetchOpts

	if text, failed, _ := invokeToolRaw(t, s, "web.fetch", map[string]any{"url": ""}); !failed || !strings.Contains(text, "url is required") {
		t.Fatalf("empty url = failed:%v text:%q", failed, text)
	}
	if text, failed, _ := invokeToolRaw(t, s, "web.fetch", map[string]any{"url": "http://127.0.0.1/secret"}); !failed || !strings.Contains(text, "non-public") {
		t.Fatalf("loopback url = failed:%v text:%q", failed, text)
	}

	text, failed, _ := invokeToolRaw(t, s, "web.fetch", map[string]any{"url": "http://public.example/big"})
	if failed {
		t.Fatalf("large public fetch failed: %s", text)
	}
	if len(text) > 512 {
		t.Fatalf("formatted output length = %d, exceeds MaxOutputBytes=512", len(text))
	}
	if !utf8.ValidString(text) {
		t.Fatal("formatted output is not valid UTF-8")
	}
	if !strings.Contains(text, "URL: http://public.example/big") || !strings.Contains(text, "Status: 200") {
		t.Fatalf("missing header fields in %q", text)
	}
}
