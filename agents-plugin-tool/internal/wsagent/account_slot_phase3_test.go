package wsagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

func TestPhase3ClaudeSlotEnvIsolationAndLeadPlugins(t *testing.T) {
	home := t.TempDir()
	workDir := t.TempDir()
	defaultClaudeDir := filepath.Join(home, ".claude")
	slotClaudeDir := filepath.Join(t.TempDir(), "slots", "claude", "work")
	if err := os.MkdirAll(defaultClaudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(slotClaudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Put a plugin entry in default ~/.claude and a distinct one in the isolated slot dir.
	if err := os.WriteFile(filepath.Join(defaultClaudeDir, "settings.json"), []byte(`{"enabledPlugins":{"ws@default":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slotClaudeDir, "settings.json"), []byte(`{"enabledPlugins":{"ws@slot":true,"other@slot":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	baseEnv := []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"CLAUDE_SESSION_ID=parent-leak",
		"CLAUDE_CONFIG_DIR=" + defaultClaudeDir,
		"PATH=/usr/bin:/bin",
	}

	// Lead env with AccountConfigDir binds CLAUDE_CONFIG_DIR to the isolated slot
	// while still stripping parent CLAUDE* vars.
	leadSpec := StartSpec{
		Dir:              workDir,
		Env:              baseEnv,
		AccountConfigDir: slotClaudeDir,
		Lead:             &LeadSpec{},
	}
	envMap := map[string]string{}
	for _, kv := range claudeLeadEnv(leadSpec) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			envMap[k] = v
		}
	}
	if envMap["CLAUDE_CONFIG_DIR"] != filepath.Clean(slotClaudeDir) {
		t.Fatalf("claudeLeadEnv CLAUDE_CONFIG_DIR = %q, want %q", envMap["CLAUDE_CONFIG_DIR"], filepath.Clean(slotClaudeDir))
	}
	if _, leaked := envMap["CLAUDE_SESSION_ID"]; leaked {
		t.Fatal("claudeLeadEnv leaked parent CLAUDE_SESSION_ID")
	}

	// claudeLeadEnabledPlugins reads the isolated slot's settings.json, not ~/.claude.
	enabled, err := claudeLeadEnabledPlugins(leadSpec)
	if err != nil {
		t.Fatalf("claudeLeadEnabledPlugins: %v", err)
	}
	if !enabled["ws@slot"] || !enabled["other@slot"] || enabled["ws@default"] {
		t.Fatalf("claudeLeadEnabledPlugins(slot) = %v, want slot plugins only", enabled)
	}

	// Empty AccountConfigDir keeps inherited/default behavior untouched.
	workerEnv := claudeApplyAccountConfigDir(claudeWorkerEnv(baseEnv), "")
	workerMap := map[string]string{}
	for _, kv := range workerEnv {
		if k, v, ok := strings.Cut(kv, "="); ok {
			workerMap[k] = v
		}
	}
	if workerMap["CLAUDE_CONFIG_DIR"] != defaultClaudeDir {
		t.Fatalf("default worker CLAUDE_CONFIG_DIR = %q, want %q", workerMap["CLAUDE_CONFIG_DIR"], defaultClaudeDir)
	}
	slotWorkerEnv := claudeApplyAccountConfigDir(claudeWorkerEnv(baseEnv), slotClaudeDir)
	slotWorkerMap := map[string]string{}
	for _, kv := range slotWorkerEnv {
		if k, v, ok := strings.Cut(kv, "="); ok {
			slotWorkerMap[k] = v
		}
	}
	if slotWorkerMap["CLAUDE_CONFIG_DIR"] != filepath.Clean(slotClaudeDir) {
		t.Fatalf("slot worker CLAUDE_CONFIG_DIR = %q, want %q", slotWorkerMap["CLAUDE_CONFIG_DIR"], filepath.Clean(slotClaudeDir))
	}
}

func TestPhase3ClaudeSlotPreflightTranscriptMigrationAnd2aRejection(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Missing credentials fails preflight without leaking paths/tokens.
	if err := claudeSlotAuthPreflight(dstDir); err == nil || err.Error() != "claude_slot_unauthenticated" {
		t.Fatalf("missing creds preflight = %v, want claude_slot_unauthenticated", err)
	}
	// Corrupt credentials fails preflight.
	if err := os.WriteFile(filepath.Join(dstDir, ".credentials.json"), []byte(`{bad`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claudeSlotAuthPreflight(dstDir); err == nil || err.Error() != "claude_slot_auth_corrupt" {
		t.Fatalf("corrupt creds preflight = %v, want claude_slot_auth_corrupt", err)
	}
	// Empty tokens fails preflight.
	if err := os.WriteFile(filepath.Join(dstDir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":""}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claudeSlotAuthPreflight(dstDir); err == nil || err.Error() != "claude_slot_unauthenticated" {
		t.Fatalf("empty tokens preflight = %v, want claude_slot_unauthenticated", err)
	}
	// Valid credentials passes preflight.
	if err := os.WriteFile(filepath.Join(dstDir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"secret-b-tok"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claudeSlotAuthPreflight(dstDir); err != nil {
		t.Fatalf("valid creds preflight = %v", err)
	}

	// Seed source with auth files, settings, and one project transcript.
	if err := os.WriteFile(filepath.Join(srcDir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"secret-a-tok"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"a@example.test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "settings.json"), []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srcTranscript := filepath.Join(srcDir, "projects", "-work-repo", "sess-claude-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(srcTranscript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcTranscript, []byte("{\"type\":\"user\",\"text\":\"hello\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := copyClaudeTranscript(srcDir, dstDir, "sess-claude-1"); err != nil {
		t.Fatalf("copyClaudeTranscript: %v", err)
	}
	dstTranscript := filepath.Join(dstDir, "projects", "-work-repo", "sess-claude-1.jsonl")
	gotTranscript, err := os.ReadFile(dstTranscript)
	if err != nil || !strings.Contains(string(gotTranscript), "hello") {
		t.Fatalf("migrated transcript = %q, err=%v", string(gotTranscript), err)
	}
	// Verify auth/settings files were NEVER copied or overwritten.
	dstCreds, err := os.ReadFile(filepath.Join(dstDir, ".credentials.json"))
	if err != nil || !strings.Contains(string(dstCreds), "secret-b-tok") || strings.Contains(string(dstCreds), "secret-a-tok") {
		t.Fatalf("dst .credentials.json overwritten: %q %v", string(dstCreds), err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf(".claude.json must not be copied, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("settings.json must not be copied, stat err=%v", err)
	}

	// 2a turn rejection classifier.
	for _, reject := range []string{
		"HTTP 401 unauthorized",
		"403 forbidden: account mismatch",
		"authentication_error: invalid token",
		"Please run /login",
		"No conversation found for session",
	} {
		if !isClaude2aTurnRejection(errors.New(reject)) {
			t.Fatalf("expected 2a rejection for %q", reject)
		}
	}
	for _, keep := range []string{
		"rate limit exceeded, retry in 10s",
		"context deadline exceeded",
		"connection reset by peer",
	} {
		if isClaude2aTurnRejection(errors.New(keep)) {
			t.Fatalf("unexpected 2a rejection for transient error %q", keep)
		}
	}
}

type claudeSlotTestBackend struct {
	wsCompactBackend
	failResumeStart bool
	onStart         func(spec StartSpec) (Process, error)
}

func (b *claudeSlotTestBackend) slotAuthPreflight(configDir string) error {
	return claudeSlotAuthPreflight(configDir)
}

func (b *claudeSlotTestBackend) migrateSlotSession(srcConfigDir, dstConfigDir, sessionID string) error {
	return copyClaudeTranscript(srcConfigDir, dstConfigDir, sessionID)
}

func (b *claudeSlotTestBackend) isSlot2aTurnRejection(err error) bool {
	return isClaude2aTurnRejection(err)
}

func (b *claudeSlotTestBackend) Start(ctx context.Context, spec StartSpec) (Process, error) {
	b.mu.Lock()
	failResume := b.failResumeStart
	onStart := b.onStart
	b.mu.Unlock()
	if onStart != nil {
		return onStart(spec)
	}
	if failResume && spec.SessionID != "" {
		b.mu.Lock()
		b.starts = append(b.starts, spec)
		b.mu.Unlock()
		return nil, errors.New("claude --resume rejected")
	}
	return b.wsCompactBackend.Start(ctx, spec)
}

func newClaudeSlotTestRunner(t *testing.T, root, leadMode string, regSlots *wscred.AccountSlotRegistry, explicitHome string, b *claudeSlotTestBackend) (*Runner, string) {
	t.Helper()
	cfgOpts := wsconfig.Options{RepoRoot: root, CacheHome: t.TempDir(), ConfigHome: t.TempDir()}
	if leadMode != "" {
		r := wsconfig.NewResolver(cfgOpts, nil, nil, nil)
		if err := r.Set(wsconfig.ItemCompactionLeadMode+"."+BackendClaude, leadMode, wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
		if err := r.Set("compaction.lead_advisory."+BackendClaude, "60%", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
		if err := r.Set("compaction.lead_hard."+BackendClaude, "80%", wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
			t.Fatal(err)
		}
	}
	NotifyCompactionConfigChanged()

	claudeReg := BuiltinBackendRegistrations()[BackendClaude]
	claudeReg.Backend = b
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
			BackendClaude: claudeReg,
		},
	})
	runner.Start(nil)
	t.Cleanup(runner.Close)

	st, err := runner.CreateLead(BackendClaude, root, LeadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return runner, st.ID
}

func TestPhase3Claude2aSuccess2bFallbackAndQueuedColdRestart(t *testing.T) {
	seedSession := func(t *testing.T, leadMode string, slotBValidCreds bool) (*Runner, *claudeSlotTestBackend, string, string, string) {
		t.Helper()
		root := t.TempDir()
		home := t.TempDir()
		defaultClaude := filepath.Join(home, ".claude")
		slotBDir := filepath.Join(t.TempDir(), "claude-slot-b")
		for _, dir := range []string{defaultClaude, slotBDir} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		// Seed source transcript under default ~/.claude/projects/...
		srcTranscript := filepath.Join(defaultClaude, "projects", "-repo", "claude-sid-1.jsonl")
		if err := os.MkdirAll(filepath.Dir(srcTranscript), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(srcTranscript, []byte("{\"type\":\"user\",\"text\":\"from-slot-a\"}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if slotBValidCreds {
			if err := os.WriteFile(filepath.Join(slotBDir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"tok-b"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		reg := wscred.NewAccountSlotRegistry(filepath.Join(t.TempDir(), wscred.AccountSlotsFileName))
		t.Cleanup(reg.Close)
		if _, err := reg.AddSlot(wscred.AccountSlot{ID: "slot-b", Harness: BackendClaude, Label: "Slot B", ConfigDir: slotBDir}); err != nil {
			t.Fatal(err)
		}
		b := &claudeSlotTestBackend{}
		runner, id := newClaudeSlotTestRunner(t, root, leadMode, reg, home, b)
		if _, err := runner.Send(id, "warm up"); err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "initial turn sent", func() bool {
			p := b.latest()
			if p == nil {
				return false
			}
			sends, _ := p.sent()
			return len(sends) == 1
		})
		p1 := b.latest()
		p1.events <- Event{SessionID: "claude-sid-1"}
		p1.events <- Event{TurnStart: true}
		p1.events <- Event{TurnEnd: true}
		waitWSCompact(t, "initial claude turn settled", func() bool {
			c := runner.conv(id)
			if c == nil {
				return false
			}
			var settled bool
			c.do(func() {
				settled = c.rec.SessionID == "claude-sid-1" && !c.turn
			})
			return settled
		})
		return runner, b, id, defaultClaude, slotBDir
	}

	t.Run("2a_success_migrates_transcript_and_resumes_on_slot_b", func(t *testing.T) {
		runner, b, id, _, slotBDir := seedSession(t, wsconfig.CompactionModeNative, true)
		orig := runner.conv(id).record()
		res, err := runner.SwitchConversationAccountSlotNow(id, "slot-b")
		if err != nil {
			t.Fatalf("SwitchConversationAccountSlotNow: %v", err)
		}
		if res.Mode != "restarted_2a" {
			t.Fatalf("mode = %q, want restarted_2a", res.Mode)
		}
		starts, _, _ := b.snapshot()
		if len(starts) != 2 || starts[1].AccountConfigDir != slotBDir || starts[1].SessionID != "claude-sid-1" {
			t.Fatalf("2a start spec = %+v", starts)
		}
		if _, err := os.Stat(filepath.Join(slotBDir, "projects", "-repo", "claude-sid-1.jsonl")); err != nil {
			t.Fatalf("expected migrated transcript in slot B: %v", err)
		}
		after := runner.conv(id).record()
		if after.ReplyID != orig.ReplyID || after.WSKey != orig.WSKey || after.AccountSlotID != "slot-b" {
			t.Fatalf("identity changed across 2a switch: before=%+v after=%+v", orig, after)
		}
	})

	t.Run("2b_fallback_on_preflight_failure_preserves_ws_identity", func(t *testing.T) {
		runner, b, id, _, slotBDir := seedSession(t, wsconfig.CompactionModeNative, false)
		orig := runner.conv(id).record()
		res, err := runner.SwitchConversationAccountSlotNow(id, "slot-b")
		if err != nil {
			t.Fatalf("SwitchConversationAccountSlotNow: %v", err)
		}
		if res.Mode != "rotated_2b" {
			t.Fatalf("mode = %q, want rotated_2b", res.Mode)
		}
		starts, _, _ := b.snapshot()
		if len(starts) != 2 || starts[1].AccountConfigDir != slotBDir || starts[1].SessionID != "" {
			t.Fatalf("2b start spec = %+v", starts)
		}
		after := runner.conv(id).record()
		if after.ReplyID != orig.ReplyID || after.WSKey != orig.WSKey || after.AccountSlotID != "slot-b" {
			t.Fatalf("identity changed across 2b rotation: before=%+v after=%+v", orig, after)
		}
	})

	t.Run("2b_fallback_on_post_resume_turn_rejection", func(t *testing.T) {
		runner, b, id, _, slotBDir := seedSession(t, wsconfig.CompactionModeNative, true)
		orig := runner.conv(id).record()
		res, err := runner.SwitchConversationAccountSlotNow(id, "slot-b")
		if err != nil || res.Mode != "restarted_2a" {
			t.Fatalf("initial 2a switch = %+v, err=%v", res, err)
		}
		if _, err := runner.Send(id, "turn after cross-account resume"); err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "post-resume turn delivered", func() bool {
			p := b.latest()
			if p == nil {
				return false
			}
			sends, _ := p.sent()
			return len(sends) == 1
		})
		p2 := b.latest()
		p2.events <- Event{TurnStart: true}
		p2.events <- Event{TurnEnd: true, TurnError: "HTTP 401 authentication_error: account mismatch"}

		waitWSCompact(t, "automatic 2a->2b fallback rotation", func() bool {
			starts, _, _ := b.snapshot()
			return len(starts) == 3
		})
		starts, _, _ := b.snapshot()
		if starts[2].AccountConfigDir != slotBDir || starts[2].SessionID != "" {
			t.Fatalf("fallback 2b start = %+v, want slot B fresh session", starts[2])
		}
		after := runner.conv(id).record()
		if after.ReplyID != orig.ReplyID || after.WSKey != orig.WSKey || after.AccountSlotID != "slot-b" {
			t.Fatalf("identity changed across post-resume 2b fallback: before=%+v after=%+v", orig, after)
		}
	})

	t.Run("queued_pending_slot_defers_midturn_autocompact_and_cold_restarts_on_turn_end_and_clear", func(t *testing.T) {
		runner, b, id, _, slotBDir := seedSession(t, wsconfig.CompactionModeNative, true)
		if _, err := runner.Send(id, "long turn"); err != nil {
			t.Fatal(err)
		}
		waitWSCompact(t, "second turn sent", func() bool {
			sends, _ := b.latest().sent()
			return len(sends) == 2
		})
		p1 := b.latest()
		p1.events <- Event{TurnStart: true}
		waitWSCompact(t, "in-turn state", func() bool {
			c := runner.conv(id)
			var active bool
			c.do(func() { active = c.turn })
			return active
		})

		// Queue slot-b while turn is in flight, then emit Claude's internal mid-turn
		// native auto-compaction (CompactStart -> CompactEnd) BEFORE TurnEnd.
		if _, err := runner.QueueConversationAccountSlot(id, "slot-b"); err != nil {
			t.Fatal(err)
		}
		p1.events <- Event{Live: &wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactStart, Trigger: wslive.TriggerAuto}}
		p1.events <- Event{Live: &wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd, Trigger: wslive.TriggerAuto}}
		time.Sleep(50 * time.Millisecond)
		if starts, _, _ := b.snapshot(); len(starts) != 1 {
			t.Fatalf("mid-turn native auto-compaction tore down process early: starts=%d", len(starts))
		}

		// Finish the turn; deferred cold-restart must fire at endTurn -> compactIdle()!
		p1.events <- Event{TurnEnd: true}
		waitWSCompact(t, "deferred cold-restart after turn end", func() bool {
			starts, _, _ := b.snapshot()
			return len(starts) == 2
		})
		starts, _, _ := b.snapshot()
		if starts[1].AccountConfigDir != slotBDir || starts[1].SessionID != "" {
			t.Fatalf("deferred cold-restart spec = %+v, want slot B fresh session", starts[1])
		}
		if rec := runner.conv(id).record(); rec.AccountSlotID != "slot-b" || rec.PendingAccountSlotID != "" {
			t.Fatalf("record after deferred cold-restart = %+v", rec)
		}

		// Now queue back to default and verify /clear in kickInput forces cold-restart.
		if _, err := runner.QueueConversationAccountSlot(id, wscred.DefaultAccountSlotID); err != nil {
			t.Fatal(err)
		}
		c := runner.conv(id)
		if _, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Command: &wsinbox.Command{Name: "clear"}}, time.Now()); err != nil {
			t.Fatal(err)
		}
		c.signalInbox()
		waitWSCompact(t, "claude /clear cold-restart to default", func() bool {
			starts, _, _ := b.snapshot()
			return len(starts) == 3
		})
		if rec := runner.conv(id).record(); wscred.NormalizeAccountSlotID(rec.AccountSlotID) != wscred.DefaultAccountSlotID || rec.PendingAccountSlotID != "" {
			t.Fatalf("record after /clear = %+v", rec)
		}
	})
}
