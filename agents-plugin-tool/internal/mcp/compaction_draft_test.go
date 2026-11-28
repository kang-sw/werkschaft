package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wscompact"
	"github.com/kang-sw/devenv/internal/wsconfig"
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
	s.Close()
	if s.compactDrafts.get(key) != nil {
		t.Fatal("draft survived Server.Close")
	}
}

// compactOnlyRunner hosts native compaction but cannot take a lead_summary.
type compactOnlyRunner struct {
	AgentHost
	calls int
}

func (*compactOnlyRunner) Watch(func(wsagent.State)) func() { return func() {} }
func (r *compactOnlyRunner) Compact(string, string, *bool) wsagent.CompactResult {
	r.calls++
	return wsagent.CompactResult{Status: "queued", Reason: "native"}
}

func TestCompactDraftNotCachedWhereRetryCannotSummarize(t *testing.T) {
	setupMailboxTestEnv(t)
	bad := map[string]any{"lead_summary": map[string]any{"next_step": "x"}, "agenda_updates": "bad"}

	t.Run("host without CompactWithSummary", func(t *testing.T) {
		root := t.TempDir()
		runner := &compactOnlyRunner{}
		s := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Agents: runner})
		t.Cleanup(s.Close)
		key, _ := s.sessions.mint(root, roleLead, "")
		resp := keyedCall(t, s, 1, key, "compact", bad)
		if !toolIsError(t, resp) || strings.Contains(toolText(t, resp), "cached in server memory") || s.compactDrafts.get(key) != nil {
			t.Fatalf("draft cached for a host that cannot summarize: %s", resp)
		}
		if resp := keyedCall(t, s, 2, key, "compact", nil); toolIsError(t, resp) || runner.calls != 1 {
			t.Fatalf("bare compact locked out: %s calls=%d", resp, runner.calls)
		}
	})

	t.Run("forwarding worker", func(t *testing.T) {
		var forwarded int
		daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwarded++
			io.WriteString(w, `{"status":"queued","reason":"forwarded"}`)
		}))
		defer daemon.Close()
		t.Setenv(wsagent.EnvPiToken, "tok123")
		t.Setenv(wsagent.EnvPiEndpoint, daemon.URL)
		root := t.TempDir()
		s := NewServer(root, "test")
		t.Cleanup(s.Close)
		if !s.workerForwards() {
			t.Fatal("fixture is not a forwarding worker")
		}
		key, _ := s.sessions.mint(root, roleLead, "")
		resp := keyedCall(t, s, 1, key, "compact", bad)
		if !toolIsError(t, resp) || strings.Contains(toolText(t, resp), "cached in server memory") || s.compactDrafts.get(key) != nil {
			t.Fatalf("draft cached for a forwarding worker: %s", resp)
		}
		resp = keyedCall(t, s, 2, key, "compact", nil)
		if toolIsError(t, resp) || forwarded != 1 || !strings.HasPrefix(toolText(t, resp), "queued: forwarded") {
			t.Fatalf("bare compact was not forwarded natively: %s forwarded=%d", resp, forwarded)
		}
	})
}

func TestCompactBareRetryDropsDraftRunnerWillRefuse(t *testing.T) {
	for _, tc := range []wsagent.CompactResult{
		{Status: "unsupported", Reason: "the caller is not a hosted session"},
		{Status: "error", Reason: wsagent.LeadSummaryRefusalPrefix + " for a hosted lead or ws-mode worker compacting itself"},
	} {
		t.Run(tc.Status, func(t *testing.T) {
			s, runner, key := newCompactDraftFixture(t)
			keyedCall(t, s, 1, key, "compact", map[string]any{"lead_summary": map[string]any{"next_step": "x"}, "agenda_updates": "bad"})
			runner.result = tc
			keyedCall(t, s, 2, key, "compact", nil)
			if s.compactDrafts.get(key) != nil {
				t.Fatal("draft survived a role refusal and would lock the caller out")
			}
			runner.result = wsagent.CompactResult{Status: "queued", Reason: "native"}
			runner.calledWith, runner.calledNoArg = nil, false
			keyedCall(t, s, 3, key, "compact", nil)
			if runner.calledWith != nil || !runner.calledNoArg {
				t.Fatalf("next bare compact was not native: %+v", runner.calledWith)
			}
		})
	}
}

func TestCompactBareRetryAfterLeadModeSwitchedToNative(t *testing.T) {
	setupMailboxTestEnv(t)
	root := t.TempDir()
	runner := &compactSchemaRunner{state: wsagent.State{ID: "lead1", Lead: true, Backend: wsagent.BackendAgy}}
	s := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Agents: runner})
	t.Cleanup(s.Close)
	s.conversation = "lead1"
	key, _ := s.sessions.mint(root, roleLead, "")

	keyedCall(t, s, 1, key, "compact", map[string]any{"lead_summary": map[string]any{"next_step": "x"}, "agenda_updates": "bad"})
	if s.compactDrafts.get(key) == nil {
		t.Fatal("draft not cached under ws mode")
	}
	runner.mode = wsconfig.CompactionModeNative
	if resp := keyedCall(t, s, 2, key, "compact", nil); toolIsError(t, resp) || runner.calledWith != nil || !runner.calledNoArg {
		t.Fatalf("stale draft replayed as ws-managed after switch to native: %s %+v", resp, runner.calledWith)
	}
	if s.compactDrafts.get(key) != nil {
		t.Fatal("stale draft not cleared")
	}

	// An explicit lead_summary stays accepted under the single-acceptance contract.
	runner.calledWith = nil
	if resp := keyedCall(t, s, 3, key, "compact", map[string]any{"lead_summary": map[string]any{"next_step": "y"}}); toolIsError(t, resp) || runner.calledWith == nil {
		t.Fatalf("explicit lead_summary refused under native mode: %s", resp)
	}
}

func TestCompactJSONResponseWarnings(t *testing.T) {
	s, _, key := newCompactDraftFixture(t)
	decode := func(resp string) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal([]byte(toolText(t, resp)), &out); err != nil {
			t.Fatalf("not JSON: %v: %s", err, resp)
		}
		return out
	}
	with := decode(keyedCall(t, s, 1, key, "compact", map[string]any{"format": "json", "lead_summary": map[string]any{"bogus": 1}}))
	if w, _ := with["warnings"].([]any); len(w) != 1 || with["status"] != "queued" {
		t.Fatalf("json response = %v, want status queued and one warning", with)
	}
	without := decode(keyedCall(t, s, 2, key, "compact", map[string]any{"format": "json", "lead_summary": map[string]any{"next_step": "x"}}))
	if _, has := without["warnings"]; has || without["status"] != "queued" {
		t.Fatalf("json response = %v, want no warnings key", without)
	}
}

func TestCompactDescendantTargetNeverTouchesSelfDraft(t *testing.T) {
	s, runner, key := newCompactDraftFixture(t)
	keyedCall(t, s, 1, key, "compact", map[string]any{"lead_summary": map[string]any{"next_step": "mine"}, "agenda_updates": "bad"})

	// A failed descendant call with lead_summary must not add to self's draft.
	resp := keyedCall(t, s, 2, key, "compact", map[string]any{"target": "id:child1", "lead_summary": map[string]any{"user_preferences": "child", "current_work": []any{1}}})
	if !toolIsError(t, resp) || strings.Contains(toolText(t, resp), "cached in server memory") {
		t.Fatalf("descendant failure should not cache: %s", resp)
	}
	if d := s.compactDrafts.get(key); d == nil || d.Has[0] || d.Prose[4] != "mine" {
		t.Fatalf("self draft polluted: %+v", d)
	}

	// A successful descendant call neither uses nor clears self's draft.
	runner.calledWith, runner.calledNoArg = nil, false
	if resp := keyedCall(t, s, 3, key, "compact", map[string]any{"target": "id:child1"}); toolIsError(t, resp) {
		t.Fatalf("descendant compact failed: %s", resp)
	}
	if runner.calledWith != nil || !runner.calledNoArg {
		t.Fatalf("descendant call replayed self's draft: %+v", runner.calledWith)
	}
	if d := s.compactDrafts.get(key); d == nil || d.Prose[4] != "mine" {
		t.Fatalf("descendant success cleared self's draft: %+v", d)
	}
}

func TestCompactCombinedLeadAndAgendaErrorsStillCacheValidFields(t *testing.T) {
	s, runner, key := newCompactDraftFixture(t)
	resp := keyedCall(t, s, 1, key, "compact", map[string]any{
		"lead_summary":   map[string]any{"user_preferences": "kept", "current_work": map[string]any{"x": 1}, "bogus": 1},
		"agenda_updates": badAgenda,
	})
	text := toolText(t, resp)
	for _, sub := range []string{"current_work must be a string", "agenda_updates has 3 error(s)", "lead_summary warnings:", "unknown key(s): bogus", "cached in server memory"} {
		if !toolIsError(t, resp) || !strings.Contains(text, sub) {
			t.Errorf("combined error missing %q: %s", sub, text)
		}
	}
	resp = keyedCall(t, s, 2, key, "compact", map[string]any{"lead_summary": map[string]any{"current_work": "fixed"}})
	if toolIsError(t, resp) || runner.calledWith.Prose[0] != "kept" || runner.calledWith.Prose[3] != "fixed" {
		t.Fatalf("retry = %s %+v", resp, runner.calledWith)
	}
}

func TestCompactDraftCacheConcurrentUse(t *testing.T) {
	var c compactDraftCache
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", g%3)
			draft, _, _ := wscompact.ParseLeadSummaryDraft(json.RawMessage(`{"next_step":"x","reread":["a"]}`))
			for i := 0; i < 200; i++ {
				c.merge(key, draft)
				if d := c.get(key); d != nil && d.Prose[4] != "x" {
					t.Errorf("torn read: %+v", d)
				}
				switch i % 25 {
				case 0:
					c.clear(key)
				case 12:
					c.reset()
				}
			}
		}(g)
	}
	wg.Wait()
}
