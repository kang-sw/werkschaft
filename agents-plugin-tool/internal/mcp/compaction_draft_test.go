package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
)

func newCompactDraftFixture(t *testing.T) (*Server, *compactAgendaTestRunner, string) {
	t.Helper()
	setupMailboxTestEnv(t)
	root := t.TempDir()
	initGit(t, root)
	runner := &compactAgendaTestRunner{result: wsagent.CompactResult{Status: "queued", Reason: "ws-managed compaction waits for the idle boundary"}}
	s := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Agents: runner})
	t.Cleanup(s.Close)
	key, err := s.sessions.mint(root, roleLead, "")
	if err != nil {
		t.Fatal(err)
	}
	return s, runner, key
}

var badAgenda = map[string]any{"a": "raw", "b": map[string]any{"merge": true}, "c": map[string]any{"value": 1, "extra": 2}}

func TestCompactLargeKoreanLeadSummaryAccepted(t *testing.T) {
	s, runner, key := newCompactDraftFixture(t)
	big := strings.Repeat("긴 한국어 요약입니다. ", 1000)
	resp := keyedCall(t, s, 1, key, "compact", map[string]any{"lead_summary": map[string]any{"current_work": big, "next_step": big}})
	if toolIsError(t, resp) || runner.calledWith == nil {
		t.Fatalf("large Korean prose refused: %s", resp)
	}
	if runner.calledWith.Prose[3] != strings.TrimSpace(big) || runner.calledWith.Prose[4] != strings.TrimSpace(big) {
		t.Fatal("prose truncated before reaching the runner")
	}
}

func TestCompactLenientLeadSummaryWarnsInQueuedResponse(t *testing.T) {
	s, runner, key := newCompactDraftFixture(t)
	resp := keyedCall(t, s, 1, key, "compact", map[string]any{"lead_summary": map[string]any{
		"current_work": nil, "next_step": 5, "bogus": "x", "reread": []any{"ok.txt", "", 7},
	}})
	text := toolText(t, resp)
	if toolIsError(t, resp) || !strings.HasPrefix(text, "queued:") {
		t.Fatalf("lenient call refused: %s", resp)
	}
	for _, sub := range []string{"lead_summary warnings:", "current_work is null", "next_step was not a string", "unknown key(s): bogus", "dropped 2"} {
		if !strings.Contains(text, sub) {
			t.Errorf("response missing %q: %s", sub, text)
		}
	}
	if runner.calledWith.Prose[4] != "5" || len(runner.calledWith.Paths) != 1 {
		t.Fatalf("summary = %+v", runner.calledWith)
	}
}

func TestCompactDraftCacheRetryAfterBadAgendaUpdates(t *testing.T) {
	s, runner, key := newCompactDraftFixture(t)
	summary := map[string]any{"user_preferences": "concise", "current_work": "drafting", "next_step": "ship", "reread": []any{"notes.txt"}}
	resp := keyedCall(t, s, 1, key, "compact", map[string]any{"lead_summary": summary, "agenda_updates": badAgenda})
	text := toolText(t, resp)
	if !toolIsError(t, resp) || runner.calls != 0 {
		t.Fatalf("expected refusal without runner call: %s calls=%d", resp, runner.calls)
	}
	for _, sub := range []string{`["a"] must be null or`, `["b"].value is required`, `["c"] has unknown field "extra"`, "cached in server memory"} {
		if !strings.Contains(text, sub) {
			t.Errorf("error missing %q: %s", sub, text)
		}
	}

	// Retry omitting lead_summary reuses the cache; agenda_updates was never cached.
	resp = keyedCall(t, s, 2, key, "compact", map[string]any{"agenda_updates": map[string]any{"ok": map[string]any{"value": 1}}})
	if toolIsError(t, resp) || runner.calledWith == nil {
		t.Fatalf("retry failed: %s", resp)
	}
	got := runner.calledWith
	if got.Prose[0] != "concise" || got.Prose[3] != "drafting" || got.Prose[4] != "ship" || len(got.Paths) != 1 {
		t.Fatalf("cached draft not used: %+v", got)
	}
	if rec, _ := s.sessions.readState(key, partAgenda); string(rec.Agenda["a"]) != "" || string(rec.Agenda["ok"]) != "1" {
		t.Fatalf("agenda = %+v", rec.Agenda)
	}

	// Success cleared the cache: a bare call is native, not a cached replay.
	runner.calledWith, runner.calledNoArg = nil, false
	keyedCall(t, s, 3, key, "compact", nil)
	if runner.calledWith != nil || !runner.calledNoArg {
		t.Fatalf("cache survived success: %+v", runner.calledWith)
	}
}

func TestCompactDraftCachePartialOverrideAndMerge(t *testing.T) {
	s, runner, key := newCompactDraftFixture(t)
	keyedCall(t, s, 1, key, "compact", map[string]any{
		"lead_summary":   map[string]any{"user_preferences": "old", "next_step": "go"},
		"agenda_updates": "bad",
	})
	// A second failing call adds a field to the draft instead of replacing it.
	keyedCall(t, s, 2, key, "compact", map[string]any{
		"lead_summary":   map[string]any{"current_work": "mid"},
		"agenda_updates": "bad",
	})
	// The retry overrides next_step with "" and leaves the rest cached.
	resp := keyedCall(t, s, 3, key, "compact", map[string]any{"lead_summary": map[string]any{"next_step": ""}})
	if toolIsError(t, resp) {
		t.Fatalf("retry refused: %s", resp)
	}
	got := runner.calledWith
	if got.Prose[0] != "old" || got.Prose[3] != "mid" || got.Prose[4] != "" {
		t.Fatalf("overlay = %+v", got)
	}
}

func TestCompactDraftCachedOnNonCoercibleField(t *testing.T) {
	s, runner, key := newCompactDraftFixture(t)
	resp := keyedCall(t, s, 1, key, "compact", map[string]any{"lead_summary": map[string]any{"user_preferences": "kept", "current_work": []any{"x"}}})
	if !toolIsError(t, resp) || !strings.Contains(toolText(t, resp), "current_work must be a string") || !strings.Contains(toolText(t, resp), "cached in server memory") {
		t.Fatalf("response = %s", resp)
	}
	resp = keyedCall(t, s, 2, key, "compact", map[string]any{"lead_summary": map[string]any{"current_work": "fixed"}})
	if toolIsError(t, resp) || runner.calledWith.Prose[0] != "kept" || runner.calledWith.Prose[3] != "fixed" {
		t.Fatalf("retry = %s %+v", resp, runner.calledWith)
	}
}

func TestCompactDraftNotCachedOrUsedAcrossSessionsOrFailedRunner(t *testing.T) {
	s, runner, key := newCompactDraftFixture(t)
	other, err := s.sessions.mint(s.root, roleLead, "")
	if err != nil {
		t.Fatal(err)
	}
	keyedCall(t, s, 1, key, "compact", map[string]any{"lead_summary": map[string]any{"next_step": "mine"}, "agenda_updates": "bad"})
	keyedCall(t, s, 2, other, "compact", nil)
	if runner.calledWith != nil {
		t.Fatalf("draft leaked to another session: %+v", runner.calledWith)
	}
	// A runner failure keeps the draft for the next attempt.
	runner.result = wsagent.CompactResult{Status: "error", Reason: "boom"}
	keyedCall(t, s, 3, key, "compact", nil)
	runner.result = wsagent.CompactResult{Status: "queued", Reason: "ok"}
	keyedCall(t, s, 4, key, "compact", nil)
	if runner.calledWith == nil || runner.calledWith.Prose[4] != "mine" {
		t.Fatalf("draft lost after runner failure: %+v", runner.calledWith)
	}
}

func TestCompactDraftExpiresAfterTTL(t *testing.T) {
	s, runner, key := newCompactDraftFixture(t)
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	s.compactDrafts.now = func() time.Time { return now }
	keyedCall(t, s, 1, key, "compact", map[string]any{"lead_summary": map[string]any{"next_step": "stale"}, "agenda_updates": "bad"})

	now = now.Add(compactDraftTTL - time.Second)
	if s.compactDrafts.get(key) == nil {
		t.Fatal("draft expired early")
	}
	now = now.Add(2 * time.Second)
	keyedCall(t, s, 2, key, "compact", nil)
	if runner.calledWith != nil || !runner.calledNoArg {
		t.Fatalf("expired draft was replayed: %+v", runner.calledWith)
	}
	if len(s.compactDrafts.entries) != 0 {
		t.Fatalf("expired entry not pruned: %v", s.compactDrafts.entries)
	}
}

func TestCompactDraftClearedOnServerClose(t *testing.T) {
	s, _, key := newCompactDraftFixture(t)
	keyedCall(t, s, 1, key, "compact", map[string]any{"lead_summary": map[string]any{"next_step": "x"}, "agenda_updates": "bad"})
	if s.compactDrafts.get(key) == nil {
		t.Fatal("draft not cached")
	}
	s.compactDrafts.reset()
	if s.compactDrafts.get(key) != nil {
		t.Fatal("draft survived reset")
	}
}
