package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

// dynamicRouterFakeBackend captures ephemeral classifier invocations and serves
// controllable replies, delays, or errors without spawning external processes.
type dynamicRouterFakeBackend struct {
	mu       sync.Mutex
	specs    []wsagent.StartSpec
	prompts  []string
	replyFn  func(prompt string) (string, error)
	startErr error
	delay    time.Duration
}

func (b *dynamicRouterFakeBackend) Start(_ context.Context, spec wsagent.StartSpec) (wsagent.Process, error) {
	b.mu.Lock()
	b.specs = append(b.specs, spec)
	startErr := b.startErr
	b.mu.Unlock()
	if startErr != nil {
		return nil, startErr
	}
	return &dynamicRouterFakeProc{
		backend: b,
		events:  make(chan wsagent.Event, 8),
	}, nil
}

func (b *dynamicRouterFakeBackend) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.prompts)
}

func (b *dynamicRouterFakeBackend) startCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.specs)
}

func (b *dynamicRouterFakeBackend) lastPrompt() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.prompts) == 0 {
		return ""
	}
	return b.prompts[len(b.prompts)-1]
}

type dynamicRouterFakeProc struct {
	backend   *dynamicRouterFakeBackend
	events    chan wsagent.Event
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

func (p *dynamicRouterFakeProc) Events() <-chan wsagent.Event { return p.events }

func (p *dynamicRouterFakeProc) Send(text string) error {
	p.backend.mu.Lock()
	p.backend.prompts = append(p.backend.prompts, text)
	fn := p.backend.replyFn
	delay := p.backend.delay
	p.backend.mu.Unlock()

	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return
		}
		reply := `{"tier":"small"}`
		var err error
		if fn != nil {
			reply, err = fn(text)
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		if err != nil {
			p.events <- wsagent.Event{TurnEnd: true, TurnError: err.Error()}
			p.mu.Unlock()
			return
		}
		p.events <- wsagent.Event{
			Live: &wslive.Event{Kind: wslive.KindReply, Text: reply, Final: true},
		}
		p.events <- wsagent.Event{TurnEnd: true}
		p.mu.Unlock()
	}()
	return nil
}

func (p *dynamicRouterFakeProc) Interrupt() error       { return nil }
func (p *dynamicRouterFakeProc) SetModel(string) error  { return nil }
func (p *dynamicRouterFakeProc) SetEffort(string) error { return nil }
func (p *dynamicRouterFakeProc) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		close(p.events)
		p.mu.Unlock()
	})
	return nil
}

// dynamicRouterSpawnFixture builds a real Server + real *wsagent.Runner + real
// *wsagent.EphemeralPool backed by in-memory fake backends so both classifier
// resolution and final Runner.AgentSpawn resolution run end to end.
type dynamicRouterSpawnFixture struct {
	server     *Server
	runner     *wsagent.Runner
	pool       *wsagent.EphemeralPool
	key        string
	root       string
	cfg        wsconfig.Options
	claudeWork *routeBackend
	codexWork  *routeBackend
	claudePool *dynamicRouterFakeBackend
	codexPool  *dynamicRouterFakeBackend
}

func newDynamicRouterSpawnFixture(t *testing.T) *dynamicRouterSpawnFixture {
	t.Helper()
	setupMailboxTestEnv(t)
	t.Setenv(envConfigManifests, wsManifests())
	root := t.TempDir()
	initGit(t, root)
	dir := t.TempDir()

	claudeWork := &routeBackend{}
	codexWork := &routeBackend{}
	regs := map[string]wsagent.BackendRegistration{}
	builtin := wsagent.BuiltinBackendRegistrations()
	claudeReg := builtin[wsagent.BackendClaude]
	claudeReg.Backend = claudeWork
	claudeReg.WorkerHTTPMCP = false
	claudeReg.ExecCutover = nil
	regs[wsagent.BackendClaude] = claudeReg

	codexReg := builtin[wsagent.BackendCodex]
	codexReg.Backend = codexWork
	codexReg.WorkerHTTPMCP = false
	codexReg.ExecCutover = nil
	regs[wsagent.BackendCodex] = codexReg

	claudePool := &dynamicRouterFakeBackend{}
	codexPool := &dynamicRouterFakeBackend{}
	pool := wsagent.NewEphemeralPool(wsagent.EphemeralPoolOptions{
		Backends: map[string]wsagent.Backend{
			wsagent.BackendClaude: claudePool,
			wsagent.BackendCodex:  codexPool,
		},
		ScratchRoot:   t.TempDir(),
		SweepInterval: -1,
	})
	t.Cleanup(func() { _ = pool.Close() })

	s := NewServerWithOptions(ServerOptions{
		Root:          root,
		Version:       "test",
		Harness:       wsagent.BackendClaude,
		EphemeralPool: pool,
	})
	t.Cleanup(s.Close)

	key := mailboxLogin(t, s, 1, root)
	cfg := s.sessionConfigOptions(key)
	for size, model := range map[string]string{
		"small":  "claude-haiku-test",
		"medium": "claude-sonnet-test",
		"large":  "claude-opus-test",
		"xlarge": "claude-opus-xl-test",
	} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(cfg, size, "", model, wsagent.BackendClaude, "medium"); err != nil {
			t.Fatal(err)
		}
	}
	for size, model := range map[string]string{
		"small":  "codex-mini-test",
		"medium": "codex-med-test",
		"large":  "codex-large-test",
	} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(cfg, size, "", model, wsagent.BackendCodex, "low"); err != nil {
			t.Fatal(err)
		}
	}

	rec := wsagent.Record{
		ReplyID:   wsmailbox.ReplyID(key),
		WSKey:     key,
		Root:      root,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Backend:   wsagent.BackendClaude,
		Lead:      true,
	}
	if err := wsagent.WriteRecordForTest(dir, rec); err != nil {
		t.Fatal(err)
	}
	runner := wsagent.NewRunner(wsagent.Options{
		Dir:       dir,
		Backends:  regs,
		Keys:      &routeKeys{},
		Config:    cfg,
		TierRoles: PackagedTierRoles,
		WSMCP: func(root string) (wsagent.MCPCommand, error) {
			return wsagent.MCPCommand{Command: "/bin/ws-mcp", Args: []string{"serve", "--stdio", "--root", root}}, nil
		},
	})
	daemon := httptest.NewServer(runner.Handler())
	t.Cleanup(daemon.Close)
	runner.SetEndpoint(daemon.URL)
	runner.Start(nil)
	t.Cleanup(runner.Close)

	s.agents = runner
	s.agentBackendSource = runner
	s.recordReader = runner
	s.callerBinding = runner
	s.conversation = rec.ReplyID

	return &dynamicRouterSpawnFixture{
		server:     s,
		runner:     runner,
		pool:       pool,
		key:        key,
		root:       root,
		cfg:        cfg,
		claudeWork: claudeWork,
		codexWork:  codexWork,
		claudePool: claudePool,
		codexPool:  codexPool,
	}
}

func TestDynamicTierRouterConfigRegistrationAndTune(t *testing.T) {
	f := agentLeadFixture(t, wsManifests(), newFakePiAgents())

	// Default: agent.tier_routing_dynamic is false, classifier is unset.
	listOut := callToolWithKey(t, f.server, 2, f.key, "config.list", map[string]any{"format": "json"})
	if !strings.Contains(listOut, wsconfig.ItemAgentTierRoutingDynamic) || !strings.Contains(listOut, wsconfig.ItemAgentTierRoutingDynamicClassifier) {
		t.Fatalf("config.list missing dynamic tier routing keys:\n%s", listOut)
	}
	res := f.server.sessionResolver(f.key, builtinConfigDefaults())
	if v, err := res.Get(f.key, wsconfig.ItemAgentTierRoutingDynamic); err != nil || v.Value != "false" {
		t.Fatalf("default %s = (%+v, %v), want false", wsconfig.ItemAgentTierRoutingDynamic, v, err)
	}

	// Tune dynamic toggle to true and classifier to codex.small.
	if out := callToolWithKey(t, f.server, 3, f.key, "config.tune", map[string]any{
		"key": wsconfig.ItemAgentTierRoutingDynamic, "value": "true",
	}); strings.Contains(out, "error") || !strings.Contains(out, "true") {
		t.Fatalf("tune dynamic on = %q", out)
	}
	if out := callToolWithKey(t, f.server, 4, f.key, "config.tune", map[string]any{
		"key": wsconfig.ItemAgentTierRoutingDynamicClassifier, "value": "codex.small",
	}); strings.Contains(out, "error") || !strings.Contains(out, "codex.small") {
		t.Fatalf("tune classifier = %q", out)
	}

	// Whitespace in classifier token is rejected.
	if out := callToolWithKey(t, f.server, 5, f.key, "config.tune", map[string]any{
		"key": wsconfig.ItemAgentTierRoutingDynamicClassifier, "value": "claude small",
	}); !strings.Contains(out, "whitespace") {
		t.Fatalf("expected whitespace rejection, got %q", out)
	}

	// Reset restores defaults.
	if out := callToolWithKey(t, f.server, 6, f.key, "config.tune", map[string]any{
		"key": wsconfig.ItemAgentTierRoutingDynamic, "reset": true,
	}); strings.Contains(out, "error") {
		t.Fatalf("reset dynamic = %q", out)
	}
	if v, err := res.Get(f.key, wsconfig.ItemAgentTierRoutingDynamic); err != nil || v.Value != "false" {
		t.Fatalf("after reset %s = (%+v, %v), want false", wsconfig.ItemAgentTierRoutingDynamic, v, err)
	}
}

func routeBackendStarts(b *routeBackend) []wsagent.StartSpec {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]wsagent.StartSpec(nil), b.starts...)
}

func setGlobalConfigForTest(t *testing.T, s *Server, sessionKey, key, value string) {
	t.Helper()
	res := s.sessionResolver(sessionKey, builtinConfigDefaults())
	if err := res.Set(key, value, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
		t.Fatalf("set %s=%q: %v", key, value, err)
	}
}

func TestDynamicTierRouterDefaultOffIsNoOp(t *testing.T) {
	f := newDynamicRouterSpawnFixture(t)
	f.claudePool.replyFn = func(string) (string, error) {
		return `{"tier":"large"}`, nil
	}

	raw := callToolWithKey(t, f.server, 2, f.key, "agent.spawn", map[string]any{
		"prompt":    "inspect repo",
		"tier":      "medium",
		"authority": "read-only",
		"format":    "json",
	})
	var res wsagent.AgentSpawnResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil || res.Address == "" {
		t.Fatalf("agent.spawn default-off = %q (%v)", raw, err)
	}
	if f.claudePool.startCount() != 0 || f.codexPool.startCount() != 0 {
		t.Fatalf("default-off invoked ephemeral pool: claude=%d codex=%d", f.claudePool.startCount(), f.codexPool.startCount())
	}
	spec, _ := f.claudeWork.waitStart(t, 0)
	if spec.Model != "claude-sonnet-test" {
		t.Fatalf("spawned spec = %+v, want static medium model claude-sonnet-test", spec)
	}
}

func TestDynamicTierRouterSelectsAllowedTierAndUsesConfiguredClassifier(t *testing.T) {
	f := newDynamicRouterSpawnFixture(t)
	setGlobalConfigForTest(t, f.server, f.key, wsconfig.ItemAgentTierRoutingDynamic, "true")
	setGlobalConfigForTest(t, f.server, f.key, wsconfig.ItemAgentTierRoutingDynamicClassifier, "codex.small")
	setGlobalConfigForTest(t, f.server, f.key, wsconfig.ItemAgentTierRoutingGuidance, "Prefer large for architecture refactors.")
	f.codexPool.replyFn = func(string) (string, error) {
		return `{"tier":"large"}`, nil
	}

	raw := callToolWithKey(t, f.server, 2, f.key, "agent.spawn", map[string]any{
		"prompt":    "Design the cross-module state machine refactor.",
		"tier":      "medium",
		"label":     "Arch Refactor",
		"authority": "read-only",
		"format":    "json",
	})
	var res wsagent.AgentSpawnResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil || res.Address == "" {
		t.Fatalf("agent.spawn dynamic = %q (%v)", raw, err)
	}
	if f.codexPool.callCount() != 1 || f.claudePool.callCount() != 0 {
		t.Fatalf("classifier calls: codex=%d claude=%d, want 1, 0", f.codexPool.callCount(), f.claudePool.callCount())
	}
	if len(f.codexPool.specs) != 1 || !f.codexPool.specs[0].Ephemeral || f.codexPool.specs[0].Model != "codex-mini-test" {
		t.Fatalf("classifier StartSpec = %+v, want Ephemeral codex-mini-test", f.codexPool.specs)
	}
	prompt := f.codexPool.lastPrompt()
	for _, want := range []string{
		"Requested tier/role: medium",
		"Label: Arch Refactor",
		"Prefer large for architecture refactors.",
		"- large",
		"Design the cross-module state machine refactor.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("classifier prompt missing %q:\n%s", want, prompt)
		}
	}
	spec, _ := f.claudeWork.waitStart(t, 0)
	if spec.Model != "claude-opus-test" {
		t.Fatalf("spawned worker spec = %+v, want dynamically chosen large model claude-opus-test", spec)
	}
}

func TestDynamicTierRouterClassifierHonorsSpawnableAndCallerRefusal(t *testing.T) {
	f := newDynamicRouterSpawnFixture(t)
	setGlobalConfigForTest(t, f.server, f.key, wsconfig.ItemAgentTierRoutingDynamic, "true")
	// Configure classifier to agy.small: agy is not registered/spawnable in this
	// runner (and refuses cross-harness callers), so the static resolver must
	// reject it and fall back cleanly to static tier resolution without invoking
	// the pool.
	setGlobalConfigForTest(t, f.server, f.key, wsconfig.ItemAgentTierRoutingDynamicClassifier, "agy.small")

	raw := callToolWithKey(t, f.server, 2, f.key, "agent.spawn", map[string]any{
		"prompt":    "summarize diff",
		"tier":      "medium",
		"authority": "read-only",
		"format":    "json",
	})
	var res wsagent.AgentSpawnResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil || res.Address == "" {
		t.Fatalf("agent.spawn with unspawnable classifier = %q (%v)", raw, err)
	}
	if f.claudePool.startCount() != 0 || f.codexPool.startCount() != 0 {
		t.Fatalf("unspawnable classifier should not run pool: claude=%d codex=%d", f.claudePool.startCount(), f.codexPool.startCount())
	}
	spec, _ := f.claudeWork.waitStart(t, 0)
	if spec.Model != "claude-sonnet-test" {
		t.Fatalf("spawned spec = %+v, want static fallback medium model claude-sonnet-test", spec)
	}
}

func TestDynamicTierRouterRejectsRetiredDirectTierHarness(t *testing.T) {
	f := newDynamicRouterSpawnFixture(t)
	setGlobalConfigForTest(t, f.server, f.key, wsconfig.ItemAgentTierRoutingDynamic, "true")
	setGlobalConfigForTest(t, f.server, f.key, wsconfig.ItemAgentTierRoutingDynamicClassifier, "pi.small")

	raw := callToolWithKey(t, f.server, 2, f.key, "agent.spawn", map[string]any{
		"prompt":    "inspect repo",
		"tier":      "medium",
		"authority": "read-only",
		"format":    "json",
	})
	var res wsagent.AgentSpawnResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil || res.Address == "" {
		t.Fatalf("agent.spawn with retired pi.small classifier = %q (%v)", raw, err)
	}
	if f.claudePool.startCount() != 0 || f.codexPool.startCount() != 0 {
		t.Fatalf("retired pi.small classifier should not invoke pool: claude=%d codex=%d", f.claudePool.startCount(), f.codexPool.startCount())
	}
	spec, _ := f.claudeWork.waitStart(t, 0)
	if spec.Model != "claude-sonnet-test" {
		t.Fatalf("spawned spec = %+v, want static fallback medium model claude-sonnet-test", spec)
	}
	if f.server.validateDynamicTierChoice("pi.small", wsagent.BackendClaude, f.cfg) {
		t.Fatal("validateDynamicTierChoice accepted retired direct tier token pi.small")
	}
}

// TestDynamicTierRouterFallbackParityWithStaticResolution verifies measurable
// pass condition (c): forced timeout, classifier error, invalid JSON, and
// out-of-set tier replies each yield the exact same spawned backend, model, and
// effort as an unrouted static agent.spawn call.
func TestDynamicTierRouterFallbackParityWithStaticResolution(t *testing.T) {
	oldTimeout := dynamicTierRouterTimeout
	dynamicTierRouterTimeout = 80 * time.Millisecond
	t.Cleanup(func() { dynamicTierRouterTimeout = oldTimeout })

	spawnOnce := func(t *testing.T, dynamic bool, configure func(*dynamicRouterSpawnFixture)) (backend string, spec wsagent.StartSpec) {
		t.Helper()
		f := newDynamicRouterSpawnFixture(t)
		if dynamic {
			setGlobalConfigForTest(t, f.server, f.key, wsconfig.ItemAgentTierRoutingDynamic, "true")
		}
		if configure != nil {
			configure(f)
		}
		raw := callToolWithKey(t, f.server, 2, f.key, "agent.spawn", map[string]any{
			"prompt":    "check static fallback parity",
			"tier":      "medium",
			"authority": "read-only",
			"format":    "json",
		})
		var res wsagent.AgentSpawnResult
		if err := json.Unmarshal([]byte(raw), &res); err != nil || res.Address == "" {
			t.Fatalf("agent.spawn = %q (%v)", raw, err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			claudeStarts := routeBackendStarts(f.claudeWork)
			codexStarts := routeBackendStarts(f.codexWork)
			if len(claudeStarts) == 1 && len(codexStarts) == 0 {
				return wsagent.BackendClaude, claudeStarts[0]
			}
			if len(codexStarts) == 1 && len(claudeStarts) == 0 {
				return wsagent.BackendCodex, codexStarts[0]
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for spawned worker: claude=%d codex=%d", len(routeBackendStarts(f.claudeWork)), len(routeBackendStarts(f.codexWork)))
		return "", wsagent.StartSpec{}
	}

	wantBackend, wantSpec := spawnOnce(t, false, nil)

	cases := []struct {
		name string
		cfg  func(*dynamicRouterSpawnFixture)
	}{
		{
			name: "forced timeout",
			cfg: func(f *dynamicRouterSpawnFixture) {
				f.claudePool.delay = 400 * time.Millisecond
				f.claudePool.replyFn = func(string) (string, error) {
					return `{"tier":"large"}`, nil
				}
			},
		},
		{
			name: "classifier start error",
			cfg: func(f *dynamicRouterSpawnFixture) {
				f.claudePool.startErr = errors.New("classifier process boom")
			},
		},
		{
			name: "classifier turn error",
			cfg: func(f *dynamicRouterSpawnFixture) {
				f.claudePool.replyFn = func(string) (string, error) {
					return "", errors.New("classifier stream failure")
				}
			},
		},
		{
			name: "invalid JSON reply",
			cfg: func(f *dynamicRouterSpawnFixture) {
				f.claudePool.replyFn = func(string) (string, error) {
					return "Use large tier please", nil
				}
			},
		},
		{
			name: "out-of-set tier value",
			cfg: func(f *dynamicRouterSpawnFixture) {
				f.claudePool.replyFn = func(string) (string, error) {
					return `{"tier":"nonexistent.xlarge"}`, nil
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotBackend, gotSpec := spawnOnce(t, true, tc.cfg)
			if gotBackend != wantBackend || gotSpec.Model != wantSpec.Model || gotSpec.Effort != wantSpec.Effort {
				t.Fatalf("%s resolved to (%s, model=%q, effort=%q), want static parity (%s, model=%q, effort=%q)",
					tc.name, gotBackend, gotSpec.Model, gotSpec.Effort, wantBackend, wantSpec.Model, wantSpec.Effort)
			}
		})
	}
}

func TestDynamicTierRouterForwardingWorkerPath(t *testing.T) {
	setupMailboxTestEnv(t)
	t.Setenv(envConfigManifests, wsManifests())
	root := t.TempDir()
	initGit(t, root)

	var mu sync.Mutex
	var gotSpawnTier string
	var tiersCalled int
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/agent.tiers"):
			mu.Lock()
			tiersCalled++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(wsagent.AgentTiers{
				Harness: wsagent.BackendClaude,
				Native: []wsagent.AgentTierEntry{
					{Tier: "small", Model: "claude-haiku-test", Effort: "low"},
					{Tier: "medium", Model: "claude-sonnet-test", Effort: "medium"},
					{Tier: "large", Model: "claude-opus-test", Effort: "high"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/agent.spawn"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			gotSpawnTier, _ = body["tier"].(string)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(wsagent.AgentSpawnResult{Address: "id:sub1", Status: wsagent.AgentStarting})
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()

	claudePool := &dynamicRouterFakeBackend{
		replyFn: func(string) (string, error) {
			return `{"tier":"small"}`, nil
		},
	}
	pool := wsagent.NewEphemeralPool(wsagent.EphemeralPoolOptions{
		Backends:      map[string]wsagent.Backend{wsagent.BackendClaude: claudePool},
		ScratchRoot:   t.TempDir(),
		SweepInterval: -1,
	})
	defer pool.Close()

	s := NewServerWithOptions(ServerOptions{
		Root:          root,
		Version:       "test",
		Harness:       wsagent.BackendClaude,
		EphemeralPool: pool,
		Env: []string{
			wsagent.EnvPiToken + "=tok-worker",
			wsagent.EnvPiEndpoint + "=" + daemon.URL,
		},
	})
	defer s.Close()

	key, err := s.sessions.mint(root, roleDelegate, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.sessionConfigOptions(key)
	for size, model := range map[string]string{
		"small":  "claude-haiku-test",
		"medium": "claude-sonnet-test",
		"large":  "claude-opus-test",
	} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(cfg, size, "", model, wsagent.BackendClaude, "low"); err != nil {
			t.Fatal(err)
		}
	}
	setGlobalConfigForTest(t, s, key, wsconfig.ItemAgentTierRoutingDynamic, "true")

	raw := callToolWithKey(t, s, 2, key, "agent.spawn", map[string]any{
		"prompt":    "tiny grep lookup",
		"tier":      "large",
		"authority": "read-only",
		"format":    "json",
	})
	if !strings.Contains(raw, `"id:sub1"`) {
		t.Fatalf("forwarded agent.spawn = %q", raw)
	}
	mu.Lock()
	defer mu.Unlock()
	if tiersCalled != 1 || gotSpawnTier != "small" {
		t.Fatalf("forwarded tiersCalled=%d gotSpawnTier=%q, want 1 and small (pool calls=%d)", tiersCalled, gotSpawnTier, claudePool.callCount())
	}
	if fmt.Sprint(claudePool.callCount()) != "1" {
		t.Fatalf("pool calls = %d, want 1", claudePool.callCount())
	}
}
