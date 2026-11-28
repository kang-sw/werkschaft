package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// slotTestBackend wraps wsCompactBackend and implements accountSlotContinuity so
// Runner exercises the real codex preflight, rollout copy, and 2a->2b logic.
type slotTestBackend struct {
	wsCompactBackend
	failResumeStart bool
	rejectClearCmd  bool
	onStart         func(spec StartSpec) (Process, error)
}

func (b *slotTestBackend) slotAuthPreflight(configDir string) error {
	return codexSlotAuthPreflight(configDir)
}

func (b *slotTestBackend) migrateSlotSession(srcConfigDir, dstConfigDir, sessionID string) error {
	return copyCodexRollout(srcConfigDir, dstConfigDir, sessionID)
}

func (b *slotTestBackend) isSlot2aTurnRejection(err error) bool {
	return isCodex2aTurnRejection(err)
}

func (b *slotTestBackend) Start(ctx context.Context, spec StartSpec) (Process, error) {
	b.mu.Lock()
	failResume := b.failResumeStart
	rejectClear := b.rejectClearCmd
	onStart := b.onStart
	b.mu.Unlock()
	if onStart != nil {
		return onStart(spec)
	}
	if failResume && spec.SessionID != "" {
		b.mu.Lock()
		b.starts = append(b.starts, spec)
		b.mu.Unlock()
		return nil, errors.New("thread/resume rejected")
	}
	p, err := b.wsCompactBackend.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	if rejectClear {
		return &clearUnsupportedProc{wsCompactProc: p.(*wsCompactProc)}, nil
	}
	return p, nil
}

type clearUnsupportedProc struct {
	*wsCompactProc
}

func (p *clearUnsupportedProc) RunCommand(cmd wsinbox.Command) error {
	if cmd.Name == "clear" {
		return errors.ErrUnsupported
	}
	return p.wsCompactProc.RunCommand(cmd)
}

func newSlotTestRunner(t *testing.T, root, leadMode string, regSlots *wscred.AccountSlotRegistry, explicitHome string, b *slotTestBackend) (*Runner, string) {
	t.Helper()
	cfgOpts := wsconfig.Options{RepoRoot: root, CacheHome: t.TempDir(), ConfigHome: t.TempDir()}
	if leadMode != "" {
		r := wsconfig.NewResolver(cfgOpts, nil, nil, nil)
		if err := r.Set(wsconfig.ItemCompactionLeadMode+"."+BackendCodex, leadMode, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
		if err := r.Set("compaction.lead_advisory."+BackendCodex, "60%", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
		if err := r.Set("compaction.lead_hard."+BackendCodex, "80%", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
	}
	NotifyCompactionConfigChanged()

	builtin := BuiltinBackendRegistrations()[BackendCodex]
	codexReg := builtin
	codexReg.Backend = b
	claudeReg := BuiltinBackendRegistrations()[BackendClaude]
	claudeReg.Backend = &wsCompactBackend{}
	restoreFn := func(_, _, wsKey string, cause RestoreCause, _ []AgentChild) (string, error) {
		return fmt.Sprintf("## Hosted Session Restore\nkey=%s cause=%s\n", wsKey, cause), nil
	}
	runner := NewRunner(Options{
		Dir:           t.TempDir(),
		Config:        cfgOpts,
		Keys:          &fakeKeys{},
		LeadKeys:      &fakeLeadKeys{},
		Holders:       newFakeHolders(),
		WSMCP:         func(string) (MCPCommand, error) { return MCPCommand{Command: "ws-mcp"}, nil },
		Log:           io.Discard,
		RestoreBundle: restoreFn,
		AccountSlots:  regSlots,
		ExplicitHome:  explicitHome,
		Backends: map[string]BackendRegistration{
			BackendCodex:  codexReg,
			BackendClaude: claudeReg,
		},
	})
	runner.Start(nil)
	t.Cleanup(runner.Close)

	st, err := runner.CreateLead(BackendCodex, root, LeadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return runner, st.ID
}

type fakePinCredSource struct {
	mu     sync.Mutex
	active string
	tokens map[string]string
}

func (f *fakePinCredSource) ActiveOpenAIAccount() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

func (f *fakePinCredSource) setActive(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active = id
}

func (f *fakePinCredSource) BeginOpenAIPlanRequestFor(_ context.Context, clientID string, _ bool) (wscred.OpenAIPlanRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if clientID == "" {
		clientID = f.active
	}
	tok, ok := f.tokens[clientID]
	if !ok {
		return wscred.OpenAIPlanRequest{}, wscred.ErrNotConfigured
	}
	return wscred.OpenAIPlanRequest{
		Credential: wscred.Credential{Provider: wscred.ProviderOpenAI, Secret: wscred.NewSecret(tok)},
		Finish:     func(wscred.OpenAIPlanOutcome) {},
	}, nil
}

func (f *fakePinCredSource) Credential(_ context.Context, req wscred.Request) (wscred.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := req.OpenAIAccount
	if id == "" {
		id = f.active
	}
	tok, ok := f.tokens[id]
	if !ok {
		return wscred.Credential{}, wscred.ErrNotConfigured
	}
	return wscred.Credential{Provider: req.Provider, Secret: wscred.NewSecret(tok)}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWerkLoopTurnPinnedOpenAIAccountAcrossMidTurnSwitch(t *testing.T) {
	creds := &fakePinCredSource{
		active: "client-A",
		tokens: map[string]string{
			"client-A": "secret-tok-A",
			"client-B": "secret-tok-B",
		},
	}
	var (
		mu       sync.Mutex
		seenAuth []string
		step     int
	)
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		step++
		curStep := step
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		mu.Unlock()

		var body string
		switch curStep {
		case 1:
			// Mid-turn account switch from A -> B right before returning a tool call.
			creds.setActive("client-B")
			body = `{"id":"resp-1","output":[` + werkCall("call-1", "fs_read", `{"path":"README.md"}`) + `],"usage":{"input_tokens":10,"output_tokens":5}}`
		case 2:
			// Second request of Turn 1 MUST STILL use pinned Account A's token!
			body = `{"id":"resp-2","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"turn 1 done"}]}],"usage":{"input_tokens":15,"output_tokens":5}}`
		default:
			// Turn 2 MUST pick up the newly active Account B without process restart!
			body = `{"id":"resp-3","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"turn 2 done"}]}],"usage":{"input_tokens":12,"output_tokens":5}}`
		}
		sse := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + body + "}\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(bytes.NewBufferString(sse)),
		}, nil
	})

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string
			ID     json.RawMessage
		}
		_ = json.NewDecoder(r.Body).Decode(&call)
		var result any
		switch call.Method {
		case "initialize":
			result = map[string]any{}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "fs.read", "inputSchema": map[string]string{"type": "object"}}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": "hello"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result})
	}))
	t.Cleanup(mcpSrv.Close)

	backend := WerkBackend{
		credentials: creds,
		httpClient:  &http.Client{Transport: rt},
	}
	spec := StartSpec{
		Dir:             t.TempDir(),
		Root:            t.TempDir(),
		Model:           "gpt-5",
		Provider:        string(wscred.ProviderOpenAI),
		PermissionLevel: PermissionReadOnly,
		MCPSession:      &MCPSession{URL: mcpSrv.URL, Token: "fixture"},
	}
	proc, err := backend.startLoop(context.Background(), spec)
	if err != nil {
		t.Fatalf("startLoop: %v", err)
	}
	t.Cleanup(func() { _ = proc.Close() })

	// Turn 1: two model requests across a tool call, with active switched mid-turn.
	if err := proc.Send("first turn"); err != nil {
		t.Fatal(err)
	}
	if end := nextWerkLoopEvent(t, proc, func(e Event) bool { return e.TurnEnd }); end.TurnError != "" {
		t.Fatalf("turn 1 error: %s", end.TurnError)
	}

	// Turn 2 on the exact same in-process loop picks up Account B with zero restart.
	if err := proc.Send("second turn"); err != nil {
		t.Fatal(err)
	}
	if end := nextWerkLoopEvent(t, proc, func(e Event) bool { return e.TurnEnd }); end.TurnError != "" {
		t.Fatalf("turn 2 error: %s", end.TurnError)
	}

	mu.Lock()
	got := append([]string(nil), seenAuth...)
	mu.Unlock()
	want := []string{"Bearer secret-tok-A", "Bearer secret-tok-A", "Bearer secret-tok-B"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("model auth headers = %v, want %v", got, want)
	}
}

func TestCodexSlotQueuedBoundaryColdRestart(t *testing.T) {
	setup := func(t *testing.T, leadMode string, rejectClear bool) (*Runner, *slotTestBackend, *wscred.AccountSlotRegistry, string, string) {
		t.Helper()
		root := t.TempDir()
		home := t.TempDir()
		slotBDir := filepath.Join(t.TempDir(), "codex-slot-b")
		if err := os.MkdirAll(slotBDir, 0o700); err != nil {
			t.Fatal(err)
		}
		reg := wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), wscred.AccountSlotsFileName))
		t.Cleanup(reg.Close)
		if _, err := reg.AddSlot(wscred.AccountSlot{ID: "slot-b", Harness: BackendCodex, Label: "Slot B", ConfigDir: slotBDir}); err != nil {
			t.Fatal(err)
		}
		b := &slotTestBackend{rejectClearCmd: rejectClear}
		runner, id := newSlotTestRunner(t, root, leadMode, reg, home, b)
		if _, err := runner.Send(id, "warm up"); err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "initial turn", func() bool {
			p := b.latest()
			if p == nil {
				return false
			}
			sends, _ := p.sent()
			return len(sends) == 1
		})
		p1 := b.latest()
		p1.events <- Event{SessionID: "sid-initial"}
		p1.events <- Event{TurnStart: true}
		p1.events <- Event{TurnEnd: true}
		waitWSCompact(t, "session id persisted", func() bool {
			c := runner.conv(id)
			return c != nil && c.record().SessionID == "sid-initial" && !c.turn
		})
		return runner, b, reg, id, slotBDir
	}

	t.Run("ws_compaction_commits_pending_slot", func(t *testing.T) {
		runner, b, _, id, slotBDir := setup(t, wsconfig.CompactionModeWS, false)
		if _, err := runner.QueueConversationAccountSlot(id, "slot-b"); err != nil {
			t.Fatal(err)
		}
		wsKey := runner.conv(id).record().WSKey
		if res := runner.Compact(wsKey, "", nil); res.Status != "queued" {
			t.Fatalf("Compact = %+v, want status=queued", res)
		}
		waitWSCompact(t, "ws compaction respawn on slot-b", func() bool {
			starts, _, _ := b.snapshot()
			return len(starts) == 2
		})
		starts, _, _ := b.snapshot()
		if starts[1].AccountConfigDir != slotBDir || starts[1].SessionID != "" {
			t.Fatalf("second start spec = %+v, want AccountConfigDir=%q SessionID=\"\"", starts[1], slotBDir)
		}
		rec := runner.conv(id).record()
		if rec.AccountSlotID != "slot-b" || rec.PendingAccountSlotID != "" {
			t.Fatalf("record after ws compaction = %+v", rec)
		}
	})

	t.Run("native_compaction_blocks_reuse_and_cold_restarts", func(t *testing.T) {
		runner, b, _, id, slotBDir := setup(t, wsconfig.CompactionModeNative, false)
		p1 := b.latest()
		if _, err := runner.QueueConversationAccountSlot(id, "slot-b"); err != nil {
			t.Fatal(err)
		}
		wsKey := runner.conv(id).record().WSKey
		if res := runner.Compact(wsKey, "", nil); res.Status != "queued" {
			t.Fatalf("Compact = %+v, want status=queued", res)
		}
		waitWSCompact(t, "native compaction cold restart on slot-b", func() bool {
			starts, _, _ := b.snapshot()
			return len(starts) == 2
		})
		p1.mu.Lock()
		p1Closed := p1.closed
		p1Cmds := append([]wsinbox.Command(nil), p1.commands...)
		p1.mu.Unlock()
		if !p1Closed {
			t.Fatal("old process was not retired during native compaction with pending slot")
		}
		if len(p1Cmds) != 0 {
			t.Fatalf("old process should not have received native compact command when cold-restarting: %+v", p1Cmds)
		}
		starts, _, _ := b.snapshot()
		if starts[1].AccountConfigDir != slotBDir || starts[1].SessionID != "" {
			t.Fatalf("cold restart spec = %+v, want AccountConfigDir=%q", starts[1], slotBDir)
		}
		rec := runner.conv(id).record()
		if rec.AccountSlotID != "slot-b" || rec.PendingAccountSlotID != "" {
			t.Fatalf("record after native cold restart = %+v", rec)
		}
	})

	t.Run("manual_clear_intercepts_before_RunCommand_and_cold_restarts", func(t *testing.T) {
		runner, b, _, id, slotBDir := setup(t, wsconfig.CompactionModeNative, true)
		p1 := b.latest()
		if _, err := runner.QueueConversationAccountSlot(id, "slot-b"); err != nil {
			t.Fatal(err)
		}
		c := runner.conv(id)
		if _, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Command: &wsinbox.Command{Name: "clear"}}, time.Now()); err != nil {
			t.Fatal(err)
		}
		c.signalInbox()
		waitWSCompact(t, "/clear cold restart on slot-b", func() bool {
			starts, _, _ := b.snapshot()
			return len(starts) == 2
		})
		p1.mu.Lock()
		p1Closed := p1.closed
		p1Cmds := append([]wsinbox.Command(nil), p1.commands...)
		p1.mu.Unlock()
		if !p1Closed || len(p1Cmds) != 0 {
			t.Fatalf("expected old proc closed without RunCommand(clear): closed=%v cmds=%+v", p1Closed, p1Cmds)
		}
		starts, _, _ := b.snapshot()
		if starts[1].AccountConfigDir != slotBDir || starts[1].SessionID != "" {
			t.Fatalf("clear cold restart spec = %+v, want AccountConfigDir=%q", starts[1], slotBDir)
		}
		rec := runner.conv(id).record()
		if rec.AccountSlotID != "slot-b" || rec.PendingAccountSlotID != "" {
			t.Fatalf("record after /clear = %+v", rec)
		}
	})

	t.Run("mid_turn_native_autocompact_defers_until_turn_end", func(t *testing.T) {
		runner, b, _, id, slotBDir := setup(t, wsconfig.CompactionModeNative, false)
		p1 := b.latest()
		if _, err := runner.Send(id, "long turn"); err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "second turn sent", func() bool {
			sends, _ := p1.sent()
			return len(sends) == 2
		})
		p1.events <- Event{TurnStart: true}
		waitWSCompact(t, "turn active", func() bool {
			c := runner.conv(id)
			var active bool
			c.do(func() { active = c.turn })
			return active
		})
		if _, err := runner.QueueConversationAccountSlot(id, "slot-b"); err != nil {
			t.Fatal(err)
		}
		// Emit native mid-turn auto-compaction events BEFORE TurnEnd.
		p1.events <- Event{Live: &wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactStart, Trigger: wslive.TriggerAuto}}
		p1.events <- Event{Live: &wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd, Trigger: wslive.TriggerAuto}}
		time.Sleep(60 * time.Millisecond)

		// Process MUST NOT be torn down mid-turn.
		starts, _, _ := b.snapshot()
		if len(starts) != 1 {
			t.Fatalf("mid-turn auto-compaction tore down early: starts=%d", len(starts))
		}
		p1.mu.Lock()
		stillOpen := !p1.closed
		p1.mu.Unlock()
		if !stillOpen {
			t.Fatal("old process closed mid-turn")
		}

		// Now end the turn: deferred boundary cold-restart MUST run on slot-b.
		p1.events <- Event{TurnEnd: true}
		waitWSCompact(t, "post-turn cold restart on slot-b", func() bool {
			starts, _, _ := b.snapshot()
			return len(starts) == 2
		})
		starts, _, _ = b.snapshot()
		if starts[1].AccountConfigDir != slotBDir {
			t.Fatalf("deferred restart spec = %+v, want AccountConfigDir=%q", starts[1], slotBDir)
		}
	})

	t.Run("worker_resume_false_commits_pending_slot_before_idle", func(t *testing.T) {
		root := t.TempDir()
		home := t.TempDir()
		slotBDir := filepath.Join(t.TempDir(), "codex-slot-b")
		if err := os.MkdirAll(slotBDir, 0o700); err != nil {
			t.Fatal(err)
		}
		reg := wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), wscred.AccountSlotsFileName))
		t.Cleanup(reg.Close)
		if _, err := reg.AddSlot(wscred.AccountSlot{ID: "slot-b", Harness: BackendCodex, Label: "Slot B", ConfigDir: slotBDir}); err != nil {
			t.Fatal(err)
		}
		b := &slotTestBackend{}
		runner, leadID := newSlotTestRunner(t, root, wsconfig.CompactionModeNative, reg, home, b)
		worker, err := runner.Spawn(SpawnSpec{
			Profile:            AgentWorkerProfile(BackendCodex),
			Root:               root,
			Prompt:             "worker task 1",
			Parent:             &ParentRef{Kind: ParentLead, ReplyID: leadID},
			PermissionLevel:    PermissionReadOnly,
			delegatedAuthority: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "worker started", func() bool {
			p := b.latest()
			if p == nil {
				return false
			}
			sends, _ := p.sent()
			return len(sends) == 1
		})
		p1 := b.latest()
		p1.events <- Event{SessionID: "worker-sid-1"}
		p1.events <- Event{TurnStart: true}
		if _, err := runner.QueueConversationAccountSlot(worker.ID, "slot-b"); err != nil {
			t.Fatal(err)
		}
		// Queue resume=false compaction (matching turn-end auto-hard compaction for workers).
		wc := runner.conv(worker.ID)
		wc.do(func() {
			wc.compactPending = &compactRequest{resume: false, triggerPath: "auto_hard"}
		})
		p1.events <- Event{TurnEnd: true}
		waitWSCompact(t, "worker retired and committed pending slot", func() bool {
			rec := wc.record()
			var hasProc bool
			wc.do(func() { hasProc = wc.proc != nil })
			return rec.AccountSlotID == "slot-b" && rec.PendingAccountSlotID == "" && rec.SessionID == "" && !hasProc
		})
		// Next wake spawns cleanly on slot-b.
		if _, err := runner.Send(worker.ID, "worker task 2"); err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "worker respawned on slot-b", func() bool {
			starts, _, _ := b.snapshot()
			return len(starts) == 2
		})
		starts, _, _ := b.snapshot()
		if starts[1].AccountConfigDir != slotBDir {
			t.Fatalf("worker respawn AccountConfigDir = %q, want %q", starts[1].AccountConfigDir, slotBDir)
		}
	})
}

func TestCodexSlotStopRestart2aSuccessAnd2bFallbacks(t *testing.T) {
	writeAuth := func(t *testing.T, dir, tok string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"auth_mode":"chatgpt","tokens":{"access_token":%q,"refresh_token":"ref"}}`, tok)
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeRollout := func(t *testing.T, dir, sid string) {
		t.Helper()
		p := filepath.Join(dir, "sessions", "2026", "11", "28", "rollout-2026-11-28-"+sid+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"type":"session_meta","id":"`+sid+`"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	setup := func(t *testing.T, withTargetAuth, failResume bool) (*Runner, *slotTestBackend, string, string, string) {
		t.Helper()
		root := t.TempDir()
		home := t.TempDir()
		defaultCodex := filepath.Join(home, ".codex")
		writeAuth(t, defaultCodex, "tok-default")
		writeRollout(t, defaultCodex, "sid-abc")
		_ = os.WriteFile(filepath.Join(defaultCodex, "state_5.sqlite"), []byte("do-not-copy"), 0o600)

		slotBDir := filepath.Join(t.TempDir(), "codex-slot-b")
		if err := os.MkdirAll(slotBDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if withTargetAuth {
			writeAuth(t, slotBDir, "tok-slot-b")
		}
		reg := wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), wscred.AccountSlotsFileName))
		t.Cleanup(reg.Close)
		if _, err := reg.AddSlot(wscred.AccountSlot{ID: "slot-b", Harness: BackendCodex, Label: "Slot B", ConfigDir: slotBDir}); err != nil {
			t.Fatal(err)
		}
		b := &slotTestBackend{failResumeStart: failResume}
		runner, id := newSlotTestRunner(t, root, wsconfig.CompactionModeWS, reg, home, b)
		if _, err := runner.Send(id, "hi"); err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "warm turn", func() bool {
			p := b.latest()
			if p == nil {
				return false
			}
			sends, _ := p.sent()
			return len(sends) == 1
		})
		p1 := b.latest()
		p1.events <- Event{SessionID: "sid-abc"}
		p1.events <- Event{TurnStart: true}
		p1.events <- Event{TurnEnd: true}
		waitWSCompact(t, "idle with sid-abc", func() bool {
			c := runner.conv(id)
			var idle bool
			c.do(func() { idle = !c.turn && c.rec.SessionID == "sid-abc" })
			return idle
		})
		return runner, b, id, defaultCodex, slotBDir
	}

	t.Run("2a_success_copies_only_rollout_and_resumes", func(t *testing.T) {
		runner, b, id, _, slotBDir := setup(t, true, false)
		res, err := runner.SwitchConversationAccountSlotNow(id, "slot-b")
		if err != nil {
			t.Fatalf("switch now: %v", err)
		}
		if res.Mode != "restarted_2a" || res.AccountSlotID != "slot-b" {
			t.Fatalf("unexpected result: %+v", res)
		}
		starts, _, _ := b.snapshot()
		if len(starts) != 2 || starts[1].SessionID != "sid-abc" || starts[1].AccountConfigDir != slotBDir {
			t.Fatalf("2a start spec = %+v", starts)
		}
		// Rollout copied; auth.json untouched (still tok-slot-b); state_5.sqlite NOT copied.
		copiedRollout := filepath.Join(slotBDir, "sessions", "2026", "11", "28", "rollout-2026-11-28-sid-abc.jsonl")
		if _, err := os.Stat(copiedRollout); err != nil {
			t.Fatalf("rollout missing in target slot: %v", err)
		}
		authRaw, _ := os.ReadFile(filepath.Join(slotBDir, "auth.json"))
		if !strings.Contains(string(authRaw), "tok-slot-b") {
			t.Fatalf("target slot auth.json was overwritten: %s", authRaw)
		}
		if _, err := os.Stat(filepath.Join(slotBDir, "state_5.sqlite")); !os.IsNotExist(err) {
			t.Fatal("state_5.sqlite should not have been copied")
		}
	})

	t.Run("2b_fallback_on_preflight_failure", func(t *testing.T) {
		runner, b, id, _, slotBDir := setup(t, false, false)
		origRec := runner.conv(id).record()
		res, err := runner.SwitchConversationAccountSlotNow(id, "slot-b")
		if err != nil {
			t.Fatalf("switch now: %v", err)
		}
		if res.Mode != "rotated_2b" || res.AccountSlotID != "slot-b" {
			t.Fatalf("unexpected 2b result: %+v", res)
		}
		starts, _, _ := b.snapshot()
		if len(starts) != 2 || starts[1].SessionID != "" || starts[1].AccountConfigDir != slotBDir {
			t.Fatalf("2b start spec = %+v", starts)
		}
		after := runner.conv(id).record()
		if after.ReplyID != origRec.ReplyID || after.WSKey != origRec.WSKey || after.Root != origRec.Root {
			t.Fatalf("permanent ws identity mutated: before=%+v after=%+v", origRec, after)
		}
	})

	t.Run("2b_fallback_on_resume_start_failure", func(t *testing.T) {
		runner, b, id, _, slotBDir := setup(t, true, true)
		res, err := runner.SwitchConversationAccountSlotNow(id, "slot-b")
		if err != nil {
			t.Fatalf("switch now: %v", err)
		}
		if res.Mode != "rotated_2b" {
			t.Fatalf("expected rotated_2b on resume failure, got %+v", res)
		}
		starts, _, _ := b.snapshot()
		last := starts[len(starts)-1]
		if last.SessionID != "" || last.AccountConfigDir != slotBDir {
			t.Fatalf("2b fallback start spec = %+v", last)
		}
	})

	t.Run("2b_fallback_on_post_resume_turn_start_rejection", func(t *testing.T) {
		runner, b, id, _, slotBDir := setup(t, true, false)
		res, err := runner.SwitchConversationAccountSlotNow(id, "slot-b")
		if err != nil || res.Mode != "restarted_2a" {
			t.Fatalf("initial 2a switch = %+v, %v", res, err)
		}
		p2 := b.latest()
		if _, err := runner.Send(id, "post-resume prompt"); err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "post-resume send reached p2", func() bool {
			sends, _ := p2.sent()
			return len(sends) == 1
		})
		// Simulate server-side Account A -> B turn/start rejection (-32600 unauthorized).
		p2.events <- Event{TurnStart: true}
		p2.events <- Event{
			TurnEnd:      true,
			TurnError:    "session belongs to another account",
			TurnErrorVal: &CodexRPCError{Code: -32600, HasCode: true, Info: "unauthorized", Message: "session belongs to another account"},
		}
		waitWSCompact(t, "automatic 2b rotation after turn rejection", func() bool {
			starts, _, _ := b.snapshot()
			return len(starts) == 3
		})
		starts, _, _ := b.snapshot()
		if starts[2].SessionID != "" || starts[2].AccountConfigDir != slotBDir {
			t.Fatalf("post-rejection 2b start spec = %+v", starts[2])
		}
	})
}

func TestQueueHarnessAccountSlotFanOut(t *testing.T) {
	root := t.TempDir()
	slotADir := filepath.Join(t.TempDir(), "codex-a")
	slotBDir := filepath.Join(t.TempDir(), "codex-b")
	_ = os.MkdirAll(slotADir, 0o700)
	_ = os.MkdirAll(slotBDir, 0o700)
	reg := wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), wscred.AccountSlotsFileName))
	t.Cleanup(reg.Close)
	_, _ = reg.AddSlot(wscred.AccountSlot{ID: "slot-a", Harness: BackendCodex, Label: "A", ConfigDir: slotADir})
	_, _ = reg.AddSlot(wscred.AccountSlot{ID: "slot-b", Harness: BackendCodex, Label: "B", ConfigDir: slotBDir})

	b := &slotTestBackend{}
	runner, lead1 := newSlotTestRunner(t, root, wsconfig.CompactionModeWS, reg, t.TempDir(), b)
	lead2, err := runner.CreateLead(BackendCodex, root, LeadOptions{AccountSlotID: "slot-b"})
	if err != nil {
		t.Fatal(err)
	}
	worker1, err := runner.Spawn(SpawnSpec{
		Profile:            AgentWorkerProfile(BackendCodex),
		Root:               root,
		Prompt:             "hi",
		Parent:             &ParentRef{Kind: ParentLead, ReplyID: lead1},
		PermissionLevel:    PermissionReadOnly,
		delegatedAuthority: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pre-queue slot-a on worker1 to verify last-write-wins overwrite.
	if _, err := runner.QueueConversationAccountSlot(worker1.ID, "slot-a"); err != nil {
		t.Fatal(err)
	}
	claudeLead, err := runner.CreateLead(BackendClaude, root, LeadOptions{})
	if err != nil {
		t.Fatal(err)
	}

	queued, skipped, err := runner.QueueHarnessAccountSlot(BackendCodex, "slot-b")
	if err != nil {
		t.Fatalf("QueueHarnessAccountSlot: %v", err)
	}
	if queued != 2 || skipped != 1 {
		t.Fatalf("fan-out counts = (queued=%d, skipped=%d), want (2, 1)", queued, skipped)
	}
	if got := runner.conv(lead1).record().PendingAccountSlotID; got != "slot-b" {
		t.Fatalf("lead1 pending = %q, want slot-b", got)
	}
	if got := runner.conv(lead2.ID).record().PendingAccountSlotID; got != "" {
		t.Fatalf("lead2 (already on slot-b) pending = %q, want empty", got)
	}
	if got := runner.conv(worker1.ID).record().PendingAccountSlotID; got != "slot-b" {
		t.Fatalf("worker1 pending = %q, want slot-b (last write wins)", got)
	}
	if got := runner.conv(claudeLead.ID).record().PendingAccountSlotID; got != "" {
		t.Fatalf("claudeLead pending = %q, want untouched empty", got)
	}
}
