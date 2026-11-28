package wsagent

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wspiruntime"
)

const (
	pirpcStandinLogEnv  = "WSAGENT_PIRPC_STANDIN_LOG"
	pirpcStandinHoldEnv = "WSAGENT_PIRPC_HOLD_TURN"
)

// pirpcStandin is the test binary's Pi `--mode rpc` stand-in, supporting strict
// LF-only JSONL without requiring the external pi binary on the test machine.
func pirpcStandin(logPath string) {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(2)
	}
	defer f.Close()
	logEnc := json.NewEncoder(f)
	outEnc := json.NewEncoder(os.Stdout)

	_ = logEnc.Encode(map[string]any{
		"kind": "boot",
		"argv": os.Args[1:],
		"env":  os.Environ(),
	})

	var (
		streaming  bool
		compacting bool
		modelID    = "claude-3.7-sonnet"
		provider   = "openrouter/anthropic"
		thinking   = "medium"
		sessionID  = "pi-sess-001"
	)
	holdTurn := os.Getenv(pirpcStandinHoldEnv) == "1"

	replyOK := func(id, cmd string, data any) {
		_ = outEnc.Encode(map[string]any{
			"id":      id,
			"type":    "response",
			"command": cmd,
			"success": true,
			"data":    data,
		})
	}
	emit := func(v any) {
		_ = outEnc.Encode(v)
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		raw := append([]byte(nil), sc.Bytes()...)
		_ = logEnc.Encode(map[string]any{"kind": "in", "raw": json.RawMessage(raw)})

		var req struct {
			ID                 string `json:"id"`
			Type               string `json:"type"`
			Message            string `json:"message"`
			Provider           string `json:"provider"`
			ModelID            string `json:"modelId"`
			Level              string `json:"level"`
			CustomInstructions string `json:"customInstructions"`
			Cancelled          bool   `json:"cancelled"`
		}
		if json.Unmarshal(raw, &req) != nil {
			continue
		}
		switch req.Type {
		case "get_state":
			replyOK(req.ID, "get_state", map[string]any{
				"sessionId":     sessionID,
				"sessionFile":   "/tmp/pi-sess-001.jsonl",
				"isStreaming":   streaming,
				"isCompacting":  compacting,
				"thinkingLevel": thinking,
				"model": map[string]any{
					"provider":      provider,
					"id":            modelID,
					"contextWindow": 200000,
				},
			})
		case "get_available_models":
			replyOK(req.ID, "get_available_models", map[string]any{
				"models": []map[string]any{
					{"provider": "openrouter", "id": "anthropic/claude-3.7-sonnet", "name": "Claude 3.7 Sonnet", "reasoning": true, "contextWindow": 200000},
					{"provider": "openai", "id": "gpt-5", "name": "GPT-5", "reasoning": true, "contextWindow": 256000},
				},
			})
		case "get_available_thinking_levels":
			replyOK(req.ID, "get_available_thinking_levels", map[string]any{
				"levels": []string{"off", "low", "medium", "high", "xhigh"},
			})
		case "get_session_stats":
			replyOK(req.ID, "get_session_stats", map[string]any{
				"sessionId": sessionID,
				"tokens": map[string]any{
					"input": 1000, "output": 250, "cacheRead": 400, "cacheWrite": 100, "total": 1750,
				},
				"cost": 0.042,
				"contextUsage": map[string]any{
					"tokens": 1750, "contextWindow": 200000, "percent": 0.875,
				},
			})
		case "set_model":
			provider, modelID = req.Provider, req.ModelID
			replyOK(req.ID, "set_model", map[string]any{"provider": provider, "id": modelID})
		case "set_thinking_level":
			thinking = req.Level
			replyOK(req.ID, "set_thinking_level", map[string]any{"level": thinking})
		case "prompt":
			streaming = true
			replyOK(req.ID, "prompt", nil)
			emit(map[string]any{"type": "agent_start"})
			emit(map[string]any{"type": "turn_start"})
			emit(map[string]any{"type": "message_start", "message": map[string]any{"role": "user", "content": req.Message}})
			emit(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
			emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "Hello "}})
			emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "world"}})
			emit(map[string]any{"type": "tool_execution_start", "toolCallId": "tc-1", "toolName": "mcp__ws__git_status", "args": map[string]any{"format": "text"}})
			emit(map[string]any{"type": "tool_execution_end", "toolCallId": "tc-1", "toolName": "mcp__ws__git_status", "result": map[string]any{"content": []map[string]any{{"type": "text", "text": "clean"}}}, "isError": false})
			// Emit 429 and 529 provider_response bridge notifications plus a dialog prompt.
			emit(map[string]any{
				"type":       "extension_ui_request",
				"id":         "ui-notify-429",
				"method":     "notify",
				"notifyType": "warning",
				"message":    `{"ws_pi_event":"provider_response","status":429,"retry_after":"12","provider":"openrouter","model":"anthropic/claude-3.7-sonnet"}`,
			})
			emit(map[string]any{
				"type":       "extension_ui_request",
				"id":         "ui-notify-529",
				"method":     "notify",
				"notifyType": "warning",
				"message":    `{"ws_pi_event":"provider_response","status":529,"provider":"anthropic","model":"claude-sonnet-4-6"}`,
			})
			emit(map[string]any{
				"type":   "extension_ui_request",
				"id":     "ui-confirm-1",
				"method": "confirm",
				"title":  "Proceed?",
			})
			if !holdTurn {
				emit(map[string]any{
					"type": "message_end",
					"message": map[string]any{
						"role":    "assistant",
						"content": []map[string]any{{"type": "text", "text": "Hello world"}},
						"usage": map[string]any{
							"input": 1000, "output": 250, "cacheRead": 400, "cacheWrite": 100, "totalTokens": 1750,
							"cost": map[string]any{"total": 0.042},
						},
					},
				})
				emit(map[string]any{"type": "turn_end"})
				emit(map[string]any{"type": "agent_end", "messages": []any{}})
				streaming = false
				emit(map[string]any{"type": "agent_settled", "aborted": false})
			}
		case "follow_up":
			replyOK(req.ID, "follow_up", nil)
		case "steer":
			replyOK(req.ID, "steer", nil)
		case "clear_queue":
			replyOK(req.ID, "clear_queue", nil)
		case "abort":
			replyOK(req.ID, "abort", nil)
			if streaming {
				streaming = false
				emit(map[string]any{"type": "agent_settled", "aborted": true})
			}
		case "compact":
			compacting = true
			emit(map[string]any{"type": "compaction_start", "reason": "manual"})
			compacting = false
			emit(map[string]any{"type": "compaction_end", "reason": "manual", "aborted": false})
			replyOK(req.ID, "compact", map[string]any{"tokensBefore": 1750})
		case "extension_ui_response":
			// Logged above; no reply needed.
		}
	}
}

func TestPiRPCLaunchArgsEnvAndUninstalledFailClosed(t *testing.T) {
	// 1. Uninstalled runtime fails closed without touching HTTP or downloading.
	var httpHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpHits.Add(1)
		http.Error(w, "unexpected download", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	mgr := wspiruntime.NewManager(wspiruntime.Options{
		CacheRoot: cacheDir,
		BaseURL:   srv.URL + "/",
	})
	backend := PiRPCBackend{Runtime: mgr}
	_, err := backend.Start(context.Background(), StartSpec{
		Root:       t.TempDir(),
		Dir:        t.TempDir(),
		MCPSession: &MCPSession{URL: "http://127.0.0.1:9999/mcp", Token: "secret-tok"},
	})
	if err == nil || !strings.Contains(err.Error(), "managed Pi runtime is not installed") {
		t.Fatalf("Start while uninstalled = %v, want managed Pi runtime is not installed", err)
	}
	if httpHits.Load() != 0 {
		t.Fatalf("Start triggered %d HTTP requests; must never auto-download", httpHits.Load())
	}

	// 2. Args verification: workspace-write vs read-only cutover, ephemeral, and absence of -nt.
	bridgePath, err := mgr.BridgePath()
	if err != nil {
		t.Fatalf("BridgePath: %v", err)
	}
	wwArgs := pirpcArgs(StartSpec{
		Dir:             "/tmp/sess",
		Model:           "openrouter/anthropic/claude-3.7-sonnet",
		Effort:          "high",
		ExecCutover:     true,
		PermissionLevel: PermissionWorkdirWrite,
		Instructions:    "lead-instructions.md",
		SystemPrompt:    "extra prompt",
	}, bridgePath)
	for _, want := range []string{"--mode", "rpc", "-ne", "-e", "builtin:mcp", bridgePath, "-ns", "-np", "--no-themes", "-na", "-nc", "--exclude-tools", "bash", "--model", "openrouter/anthropic/claude-3.7-sonnet", "--thinking", "high"} {
		if !slices.Contains(wwArgs, want) {
			t.Errorf("workspace-write args missing %q: %v", want, wwArgs)
		}
	}
	if slices.Contains(wwArgs, "-nt") || slices.Contains(wwArgs, "--no-tools") || slices.Contains(wwArgs, "-nbt") {
		t.Errorf("workspace-write args must not pass -nt or -nbt: %v", wwArgs)
	}
	// Verify `-e builtin:mcp` precedes `-e <bridgePath>`.
	idxBuiltin, idxBridge := slices.Index(wwArgs, "builtin:mcp"), slices.Index(wwArgs, bridgePath)
	if idxBuiltin < 0 || idxBridge < 0 || idxBuiltin >= idxBridge {
		t.Errorf("builtin:mcp must precede bridgePath in argv: %v", wwArgs)
	}

	roArgs := pirpcArgs(StartSpec{
		Dir:             "/tmp/sess",
		ExecCutover:     true,
		PermissionLevel: PermissionReadOnly,
	}, bridgePath)
	if !slices.Contains(roArgs, "-nbt") {
		t.Errorf("read-only cutover args missing -nbt: %v", roArgs)
	}
	if slices.Contains(roArgs, "--exclude-tools") || slices.Contains(roArgs, "-nt") {
		t.Errorf("read-only cutover args must not pass --exclude-tools or -nt: %v", roArgs)
	}

	ephArgs := pirpcArgs(StartSpec{Ephemeral: true, ExecCutover: true, PermissionLevel: PermissionReadOnly}, bridgePath)
	if !slices.Contains(ephArgs, "--no-session") || slices.Contains(ephArgs, bridgePath) || slices.Contains(ephArgs, "builtin:mcp") {
		t.Errorf("ephemeral args = %v", ephArgs)
	}

	// 3. Env verification: required vars present, tokens in env only, parent harness vars scrubbed, PI_OFFLINE unset.
	slotDir := filepath.Join(t.TempDir(), "pi-slot-work")
	env := pirpcEnv(StartSpec{
		AccountConfigDir: slotDir,
		MCPSession:       &MCPSession{URL: "http://127.0.0.1:4321/mcp/session", Token: "tok-secret-123"},
		Env: []string{
			"PATH=/usr/bin",
			"CLAUDECODE=1",
			"CODEX_HOME=/old/codex",
			"PI_OFFLINE=1",
			"PI_CODING_AGENT_DIR=/old/pi",
			"ORCA_FOO=1",
			"WS_PI_BAR=1",
			"WS_MAILBOX=leaked",
			"WS_HOSTED_SESSION=leaked",
		},
	})
	envMap := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		envMap[k] = v
	}
	if envMap["PI_SKIP_VERSION_CHECK"] != "1" || envMap["PI_TELEMETRY"] != "0" {
		t.Errorf("missing Pi hygiene env: %v", envMap)
	}
	if envMap["PI_CODING_AGENT_DIR"] != filepath.Clean(slotDir) {
		t.Errorf("PI_CODING_AGENT_DIR = %q, want %q", envMap["PI_CODING_AGENT_DIR"], filepath.Clean(slotDir))
	}
	if envMap[EnvMCPSessionURL] != "http://127.0.0.1:4321/mcp/session" || envMap[EnvMCPSessionToken] != "tok-secret-123" {
		t.Errorf("missing canonical MCP session env: url=%q token=%q", envMap[EnvMCPSessionURL], envMap[EnvMCPSessionToken])
	}
	for _, forbidden := range []string{"PI_OFFLINE", "CLAUDECODE", "CODEX_HOME", "ORCA_FOO", "WS_PI_BAR", "WS_MAILBOX", "WS_HOSTED_SESSION"} {
		if _, ok := envMap[forbidden]; ok {
			t.Errorf("forbidden env var leaked into child env: %s=%q", forbidden, envMap[forbidden])
		}
	}
	for _, a := range wwArgs {
		if strings.Contains(a, "tok-secret-123") {
			t.Fatalf("MCP token leaked into argv: %v", wwArgs)
		}
	}
}

func TestPiRPCProtocolRoundTripAndControls(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "standin.jsonl")
	slotDir := filepath.Join(t.TempDir(), "pi-slot")
	cacheDir := t.TempDir()
	mgr := wspiruntime.NewManager(wspiruntime.Options{CacheRoot: cacheDir})

	b := PiRPCBackend{
		Runtime: mgr,
		Command: exe,
	}
	proc, err := b.Start(context.Background(), StartSpec{
		Root:             t.TempDir(),
		Dir:              t.TempDir(),
		AccountConfigDir: slotDir,
		ExecCutover:      true,
		PermissionLevel:  PermissionWorkdirWrite,
		MCPSession:       &MCPSession{URL: "http://127.0.0.1:4567/mcp", Token: "tok-roundtrip"},
		Env:              append(os.Environ(), pirpcStandinLogEnv+"="+logPath),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proc.Close()

	pp := proc.(*pirpcProcess)
	controls := pp.processControls()
	if controls.steer == nil || controls.reports == nil || controls.commands == nil || controls.turns == nil {
		t.Fatalf("worker controls missing expected capabilities: %+v", controls)
	}
	if controls.mode != nil {
		t.Fatal("pi-rpc must not expose modeSetter (lead permission switches restart process)")
	}

	var (
		mu       sync.Mutex
		events   []Event
		settled  = make(chan struct{}, 4)
		gotStats = make(chan struct{}, 4)
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range proc.Events() {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
			if ev.TurnEnd {
				select {
				case settled <- struct{}{}:
				default:
				}
			}
			if ev.FinalUsage {
				select {
				case gotStats <- struct{}{}:
				default:
				}
			}
		}
	}()

	// Prompt -> streaming deltas -> tool call -> 429/529 warnings -> agent_settled -> auto get_session_stats.
	if err := proc.Send("say hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for agent_settled")
	}
	select {
	case <-gotStats:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for post-settlement get_session_stats")
	}

	// Control RPC round-trips.
	if err := pp.SetModel("openrouter/anthropic/claude-3.7-sonnet"); err != nil {
		t.Fatalf("SetModel nested: %v", err)
	}
	if err := pp.SetModel("no-slash-invalid"); err == nil {
		t.Fatal("SetModel without provider/modelId slash should fail")
	}
	if err := pp.SetEffort("xhigh"); err != nil {
		t.Fatalf("SetEffort: %v", err)
	}
	if err := pp.Steer("stay focused"); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if err := pp.ClearSteered(); err != nil {
		t.Fatalf("ClearSteered: %v", err)
	}
	if err := pp.RunCommand(wsinbox.Command{Name: "compact", Args: "preserve decisions"}); err != nil {
		t.Fatalf("RunCommand compact: %v", err)
	}
	running, err := pp.TurnRunning()
	if err != nil || running {
		t.Fatalf("TurnRunning = (%v, %v), want (false, nil)", running, err)
	}
	if _, err := pp.SessionStats(context.Background()); err != nil {
		t.Fatalf("SessionStats: %v", err)
	}
	models, err := pp.AvailableModels(context.Background())
	if err != nil || len(models) != 2 || models[0].ID != "openrouter/anthropic/claude-3.7-sonnet" {
		t.Fatalf("AvailableModels = (%+v, %v)", models, err)
	}
	levels, err := pp.AvailableThinkingLevels(context.Background())
	if err != nil || !slices.Equal(levels, []string{"off", "low", "medium", "high", "xhigh"}) {
		t.Fatalf("AvailableThinkingLevels = (%v, %v)", levels, err)
	}

	_ = proc.Close()
	<-done

	mu.Lock()
	evCopy := slices.Clone(events)
	mu.Unlock()

	var sawHarness, sawSession, sawReply, sawStatus, saw429, saw529, sawCompactStart, sawCompactEnd bool
	for _, ev := range evCopy {
		if ev.Harness != nil && ev.Harness.Version == wspiruntime.PinnedVersion {
			sawHarness = true
		}
		if ev.SessionID == "pi-sess-001" {
			sawSession = true
		}
		if ev.Live != nil && ev.Live.Kind == wslive.KindReply && ev.Live.Final && ev.Live.Text == "Hello world" {
			sawReply = true
		}
		if ev.Live != nil && ev.Live.Kind == wslive.KindStatus && ev.Live.Context != nil && ev.Live.Context.Window == 200000 {
			sawStatus = true
		}
		if ev.Retry != nil && ev.Retry.Status == 429 && strings.Contains(ev.Retry.Error, "retry_after=12") {
			saw429 = true
		}
		if ev.Retry != nil && ev.Retry.Status == 529 {
			saw529 = true
		}
		if strings.Contains(ev.Notice, `"ws_pi_event"`) {
			t.Errorf("raw ws_pi_event JSON leaked into Event.Notice: %q", ev.Notice)
		}
		if ev.Live != nil && ev.Live.Kind == wslive.KindCompact {
			if ev.Live.Phase == wslive.CompactStart {
				sawCompactStart = true
			}
			if ev.Live.Phase == wslive.CompactEnd {
				sawCompactEnd = true
			}
		}
	}
	if !sawHarness || !sawSession || !sawReply || !sawStatus || !saw429 || !saw529 || !sawCompactStart || !sawCompactEnd {
		t.Fatalf("missing expected events: harness=%v session=%v reply=%v status=%v 429=%v 529=%v compactStart=%v compactEnd=%v",
			sawHarness, sawSession, sawReply, sawStatus, saw429, saw529, sawCompactStart, sawCompactEnd)
	}

	// Verify wire log recorded nested set_model split, compact customInstructions,
	// auto-cancelled confirm dialog, and non-empty MCP token in env (not argv).
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read standin log: %v", err)
	}
	var sawSetModel, sawCompact, sawDialogCancel bool
	for _, line := range strings.Split(strings.TrimSpace(string(logBytes)), "\n") {
		var rec struct {
			Kind string          `json:"kind"`
			Argv []string        `json:"argv"`
			Env  []string        `json:"env"`
			Raw  json.RawMessage `json:"raw"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Kind == "boot" {
			for _, a := range rec.Argv {
				if strings.Contains(a, "tok-roundtrip") {
					t.Fatalf("token leaked into standin argv: %v", rec.Argv)
				}
			}
		}
		if rec.Kind == "in" {
			var m map[string]any
			if json.Unmarshal(rec.Raw, &m) != nil {
				continue
			}
			switch m["type"] {
			case "set_model":
				if m["provider"] == "openrouter" && m["modelId"] == "anthropic/claude-3.7-sonnet" {
					sawSetModel = true
				}
			case "compact":
				if m["customInstructions"] == "preserve decisions" {
					sawCompact = true
				}
			case "extension_ui_response":
				if m["id"] == "ui-confirm-1" && m["cancelled"] == true {
					sawDialogCancel = true
				}
			}
		}
	}
	if !sawSetModel || !sawCompact || !sawDialogCancel {
		t.Fatalf("wire log missing expected RPCs: setModel=%v compact=%v dialogCancel=%v", sawSetModel, sawCompact, sawDialogCancel)
	}
}

func TestPiRPCFollowUpAndAbortDuringActiveTurn(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "hold.jsonl")
	mgr := wspiruntime.NewManager(wspiruntime.Options{CacheRoot: t.TempDir()})
	b := PiRPCBackend{Runtime: mgr, Command: exe}
	proc, err := b.Start(context.Background(), StartSpec{
		Root:       t.TempDir(),
		Dir:        t.TempDir(),
		MCPSession: &MCPSession{URL: "http://127.0.0.1:4568/mcp", Token: "tok-hold"},
		Env:        append(os.Environ(), pirpcStandinLogEnv+"="+logPath, pirpcStandinHoldEnv+"=1"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proc.Close()

	turnStarted := make(chan struct{}, 1)
	aborted := make(chan bool, 1)
	go func() {
		for ev := range proc.Events() {
			if ev.TurnStart {
				select {
				case turnStarted <- struct{}{}:
				default:
				}
			}
			if ev.TurnEnd {
				select {
				case aborted <- (ev.TurnReason == wslive.TurnAborted):
				default:
				}
			}
		}
	}()

	if err := proc.Send("first turn"); err != nil {
		t.Fatalf("Send prompt: %v", err)
	}
	select {
	case <-turnStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for TurnStart")
	}

	pp := proc.(*pirpcProcess)
	running, err := pp.TurnRunning()
	if err != nil || !running {
		t.Fatalf("TurnRunning while held = (%v, %v), want (true, nil)", running, err)
	}
	// Second Send while turn is active must use `follow_up`.
	if err := proc.Send("queued follow up"); err != nil {
		t.Fatalf("Send follow_up: %v", err)
	}
	if err := proc.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	select {
	case wasAborted := <-aborted:
		if !wasAborted {
			t.Fatal("TurnEnd reason was not aborted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for aborted agent_settled")
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(logBytes)
	if !strings.Contains(text, `"type":"follow_up"`) || !strings.Contains(text, `"type":"abort"`) {
		t.Fatalf("expected follow_up and abort in wire log, got:\n%s", text)
	}
}

func TestPiRPCChoicesCacheAndColdProbe(t *testing.T) {
	pirpcChoices.Lock()
	saved := pirpcChoices.byHome
	pirpcChoices.byHome = nil
	pirpcChoices.Unlock()
	t.Cleanup(func() {
		pirpcChoices.Lock()
		pirpcChoices.byHome = saved
		pirpcChoices.Unlock()
	})

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	slotDir := filepath.Join(t.TempDir(), "pi-slot-choices")
	logPath := filepath.Join(t.TempDir(), "probe.jsonl")
	t.Setenv(pirpcStandinLogEnv, logPath)

	b := PiRPCBackend{
		Runtime:     wspiruntime.NewManager(wspiruntime.Options{CacheRoot: t.TempDir()}),
		Command:     exe,
		ResolveHome: func() string { return slotDir },
	}
	models, levels, err := b.Choices(context.Background())
	if err != nil {
		t.Fatalf("cold Choices probe: %v", err)
	}
	if len(models) != 2 || models[0].ID != "openrouter/anthropic/claude-3.7-sonnet" {
		t.Fatalf("probed models = %+v", models)
	}
	if !slices.Equal(levels, []string{"off", "low", "medium", "high", "xhigh"}) {
		t.Fatalf("probed levels = %v", levels)
	}

	// Mutating returned slices must not corrupt cache; second call must hit cache
	// without spawning again (verify via removed log file).
	models[0].ID = "mutated"
	levels[0] = "mutated"
	_ = os.Remove(logPath)
	againModels, againLevels, err := b.Choices(context.Background())
	if err != nil || againModels[0].ID != "openrouter/anthropic/claude-3.7-sonnet" || againLevels[0] != "off" {
		t.Fatalf("warm Choices = (%+v, %v, %v)", againModels, againLevels, err)
	}
	if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
		t.Fatal("warm Choices unexpectedly spawned a probe subprocess")
	}

	// workerSetup behavior: tier gap reported when empty, cleared when configured;
	// credential and provider gaps always empty (CLI-owned unfiltered auth).
	if gap := b.workerTierGap(nil); gap == "" {
		t.Fatal("workerTierGap(nil) must report unconfigured tiers")
	}
	if gap := b.workerTierGap(map[string]wsconfig.AgentTier{"medium": {Backend: BackendPiRPC, Model: "openai/gpt-5"}}); gap != "" {
		t.Fatalf("workerTierGap(configured) = %q, want empty", gap)
	}
	if b.workerCredentialGap() != "" || b.workerProviderGap("openrouter") != "" {
		t.Fatal("pi-rpc credential/provider gaps must remain empty")
	}
}

func TestPiRPCSharedRuntimeBinderAndLeadPermissionRestart(t *testing.T) {
	// 1. Shared runtime binder: WithPiRuntime & Options.PiRuntime inject the
	// shared manager without freezing WS_CACHE_HOME in BuiltinBackendRegistrations.
	cacheA := t.TempDir()
	cacheB := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(cacheA); err == nil {
		cacheA = resolved
	}
	if resolved, err := filepath.EvalSymlinks(cacheB); err == nil {
		cacheB = resolved
	}
	t.Setenv("WS_CACHE_HOME", cacheA)
	pathA := BuiltinBackendRegistrations()[BackendPiRPC].Backend.(ChoiceLister).Executable()
	t.Setenv("WS_CACHE_HOME", cacheB)
	pathB := BuiltinBackendRegistrations()[BackendPiRPC].Backend.(ChoiceLister).Executable()
	if pathA == pathB || !strings.HasPrefix(pathA, cacheA) || !strings.HasPrefix(pathB, cacheB) {
		t.Fatalf("BuiltinBackendRegistrations froze WS_CACHE_HOME: pathA=%q pathB=%q", pathA, pathB)
	}

	sharedMgr := wspiruntime.NewManager(wspiruntime.Options{CacheRoot: cacheA})
	regs := WithPiRuntime(BuiltinBackendRegistrations(), sharedMgr)
	if bound, ok := regs[BackendPiRPC].Backend.(PiRPCBackend); !ok || bound.Runtime != sharedMgr {
		t.Fatalf("WithPiRuntime did not bind shared manager: %+v", regs[BackendPiRPC].Backend)
	}

	runnerWithOpt := NewRunner(Options{
		Dir:       t.TempDir(),
		Backends:  BuiltinBackendRegistrations(),
		PiRuntime: sharedMgr,
	})
	if bound, ok := runnerWithOpt.opts.Backends[BackendPiRPC].Backend.(PiRPCBackend); !ok || bound.Runtime != sharedMgr {
		t.Fatalf("Options.PiRuntime did not bind shared manager: %+v", runnerWithOpt.opts.Backends[BackendPiRPC].Backend)
	}

	// 2. Live lead permission-change & holder reattach via leadHarness + fakeHolders.
	h := newLeadHarness(t)
	h.opts.PiRuntime = sharedMgr
	piReg := BuiltinBackendRegistrations()[BackendPiRPC]
	piReg.Backend = PiRPCBackend{Runtime: sharedMgr, Command: fakeHarness("pi")}
	h.opts.Backends[BackendPiRPC] = piReg
	h.opts.WSMCP = func(root string) (MCPCommand, error) {
		return MCPCommand{Command: "ws-mcp", Env: map[string]string{"WS_MCP_PROJECT_ROOT": root}}, nil
	}

	// Enable exec cutover for pi-rpc via global override + fake exec jobs so
	// launch flags flip between --exclude-tools bash and -nbt deterministically.
	h.execJobs = newFakeExecJobs()
	writeClaudeCutoverConfig(t, h, map[string]string{
		wsconfig.ItemExecNativeToolRemoval + ".pi-rpc": "on",
	})

	var (
		spawnMu   sync.Mutex
		spawnArgs [][]string
	)
	h.holders.onSpawn = func(id string, spec wsholder.SpawnSpec) {
		spawnMu.Lock()
		spawnArgs = append(spawnArgs, slices.Clone(spec.Argv))
		spawnMu.Unlock()
	}
	h.holders.onInput = func(id string, data []byte, eof bool) {
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var req struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(line), &req) != nil {
				continue
			}
			switch req.Type {
			case "get_state":
				h.holders.out(id, `{"id":"`+req.ID+`","type":"response","command":"get_state","success":true,"data":{"sessionId":"pi-lead-1","isStreaming":false,"isCompacting":false}}`)
			case "get_available_models":
				h.holders.out(id, `{"id":"`+req.ID+`","type":"response","command":"get_available_models","success":true,"data":{"models":[]}}`)
			case "get_available_thinking_levels":
				h.holders.out(id, `{"id":"`+req.ID+`","type":"response","command":"get_available_thinking_levels","success":true,"data":{"levels":["low","high"]}}`)
			case "prompt":
				h.holders.out(id, `{"id":"`+req.ID+`","type":"response","command":"prompt","success":true}`)
				h.holders.out(id, `{"type":"agent_start"}`)
				h.holders.out(id, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"ack"}]}}`)
				h.holders.out(id, `{"type":"agent_settled","aborted":false}`)
			case "get_session_stats":
				h.holders.out(id, `{"id":"`+req.ID+`","type":"response","command":"get_session_stats","success":true,"data":{"sessionId":"pi-lead-1","tokens":{"total":100},"cost":0.001}}`)
			}
		}
	}

	h.startAt("http://127.0.0.1:1")
	st := h.lead(BackendPiRPC, LeadOptions{})
	if st.PermissionLevel != PermissionWorkdirWrite || st.PermissionEnforcement != "advisory" {
		t.Fatalf("default pi-rpc lead permission = (%q, %q), want (workdir-write, advisory)", st.PermissionLevel, st.PermissionEnforcement)
	}

	// Send first turn -> spawns with --exclude-tools bash (workdir-write cutover).
	h.send(st.ID, "turn 1")
	h.idle(st.ID)
	firstHolder := h.holder(st.ID)
	spawnMu.Lock()
	if len(spawnArgs) != 1 || !slices.Contains(spawnArgs[0], "--exclude-tools") || !slices.Contains(spawnArgs[0], "bash") || slices.Contains(spawnArgs[0], "-nbt") {
		t.Fatalf("first launch args = %v, want --exclude-tools bash", spawnArgs)
	}
	spawnMu.Unlock()

	// Lower permission to read-only -> stops current process cleanly (no HTTP 400).
	if _, err := h.runner.SetPermissionLevel(st.ID, PermissionReadOnly); err != nil {
		t.Fatalf("SetPermissionLevel(read-only): %v", err)
	}
	eventually(t, "process stops on permission change", func() bool {
		return !h.state(st.ID).Process
	})

	// Next turn relaunches a fresh holder with `-nbt`.
	h.send(st.ID, "turn 2")
	h.idle(st.ID)
	secondHolder := h.holder(st.ID)
	if secondHolder == "" || secondHolder == firstHolder {
		t.Fatalf("expected fresh holder after permission change: first=%q second=%q", firstHolder, secondHolder)
	}
	spawnMu.Lock()
	if len(spawnArgs) != 2 || !slices.Contains(spawnArgs[1], "-nbt") || slices.Contains(spawnArgs[1], "--exclude-tools") {
		t.Fatalf("read-only relaunch args = %v, want -nbt", spawnArgs)
	}
	spawnMu.Unlock()

	// Verify permission state stays truthful ("advisory", no fabricated native mode).
	stRO := h.state(st.ID)
	if stRO.PermissionLevel != PermissionReadOnly || stRO.PermissionEnforcement != "advisory" || stRO.ActiveMode != "" {
		t.Fatalf("read-only state = (%q, %q, mode=%q), want (read-only, advisory, '')", stRO.PermissionLevel, stRO.PermissionEnforcement, stRO.ActiveMode)
	}

	// Holder reattach (daemon restart) must keep the holder alive and emit no
	// failed permission reconciliation diagnostic.
	h.restart()
	reattached := h.state(st.ID)
	if !reattached.Process || h.holder(st.ID) != secondHolder {
		t.Fatalf("expected holder %q to survive reattach; state=%+v holder=%q", secondHolder, reattached, h.holder(st.ID))
	}
	if reattached.Error != "" || strings.Contains(h.log.String(), "permission reconciliation") {
		t.Fatalf("unexpected permission reconciliation error on reattach: err=%q log=%s", reattached.Error, h.log.String())
	}

	// Confirm defensive clone on BuiltinBackendRegistrations includes PiRPCBackend.
	if !reflect.DeepEqual(BuiltinBackendRegistrations()[BackendPiRPC].Permission, PermissionPolicy{LegacyDefault: PermissionWorkdirWrite}) {
		t.Fatal("BuiltinBackendRegistrations()[BackendPiRPC].Permission mismatch")
	}
}
