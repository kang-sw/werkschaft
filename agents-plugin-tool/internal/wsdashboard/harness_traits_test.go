package wsdashboard

import (
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// testHarnessTraits mirrors the four built-in backends' declarations
// (internal/wsagent *_policy.go, pinned there by TestBuiltinDashboardTraits):
// this package cannot import wsagent, so a test that exercises a built-in
// harness's dashboard behavior declares the same traits itself.
func testHarnessTraits() HarnessTraitSet {
	return HarnessTraitSet{
		"claude": {PushesClaudeUsage: true, UntaggedRowsNative: true, TerminalTakeover: true, ReplayClientName: "claude-code"},
		"codex":  {SeparateFiveHourHistory: true, ReplayClientName: "codex"},
		"pi":     {TerminalTakeover: true, ReplayClientName: "ws-pi-bridge"},
		"agy":    {ReplayClientName: "agy"},
	}
}

func TestHarnessTraitSetReplayClientName(t *testing.T) {
	set := testHarnessTraits()
	cases := map[string]string{
		"pi": "ws-pi-bridge", "claude": "claude-code", "codex": "codex", "agy": "agy",
		"": "ws-mcp-http-replay", "unregistered": "ws-mcp-http-replay",
	}
	for harness, want := range cases {
		if got := set.replayClientName(harness); got != want {
			t.Errorf("replayClientName(%q) = %q, want %q", harness, got, want)
		}
	}
	// A registered harness that declares no name replays the generic one,
	// and no registration at all declares none.
	if got := (HarnessTraitSet{"x": {}}).replayClientName("x"); got != "ws-mcp-http-replay" {
		t.Errorf("undeclared replay name = %q", got)
	}
	if got := (HarnessTraitSet(nil)).replayClientName("claude"); got != "ws-mcp-http-replay" {
		t.Errorf("nil set replay name = %q", got)
	}
}

func TestHarnessTraitSetNormalizesRegisteredNamesOnly(t *testing.T) {
	set := testHarnessTraits()
	for in, want := range map[string]string{"Claude": "claude", " pi ": "pi", "AGY": "agy", "codex": "codex", "other": "", "": ""} {
		if got := set.normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if got := (HarnessTraitSet{"fixture": {}}).normalize("Fixture"); got != "fixture" {
		t.Errorf("an extension's registered name = %q", got)
	}
	if got := (HarnessTraitSet(nil)).normalize("claude"); got != "" {
		t.Errorf("a nil set names no harness, got %q", got)
	}
}

// newTestPlanUsageStore is newPlanUsageStore for a store that serves the
// built-in harnesses' declarations.
func newTestPlanUsageStore(dir, home string, getenv func(string) string) *planUsageStore {
	s := newPlanUsageStore(dir, home, getenv)
	s.harnesses = testHarnessTraits()
	return s
}

// TestHarnessTraitsDriveDashboardBehavior: each dashboard consumer reads the
// declared trait of the harness it is given, so an extension that declares a
// trait gets the behavior and a harness the set does not register gets none.
func TestHarnessTraitsDriveDashboardBehavior(t *testing.T) {
	ext := HarnessTraitSet{"fixture": {PushesClaudeUsage: true, SeparateFiveHourHistory: true, UntaggedRowsNative: true, TerminalTakeover: true}}
	t.Run("untagged rows are native", func(t *testing.T) {
		row := wslive.Agent{ID: "plain"}
		if !hudNative(row, "fixture", ext) || !hudNative(row, " Fixture", ext) {
			t.Error("an extension's untagged row is its own subagent")
		}
		for _, owner := range []string{"claude", "", "other"} {
			if hudNative(row, owner, ext) {
				t.Errorf("owner %q declared no such rows", owner)
			}
		}
		if hudNative(row, "claude", nil) {
			t.Error("a nil set declares no native rows")
		}
		if !hudNative(wslive.Agent{ID: wslive.NativeAgyPrefix + "c"}, "", nil) {
			t.Error("a native-prefixed id is native under any set")
		}
		if hudNative(wslive.Agent{ID: "tagged", Harness: "codex"}, "fixture", ext) {
			t.Error("a row with a daemon identity is never untagged")
		}
	})
	t.Run("rail takeover", func(t *testing.T) {
		in := func(harness string, set HarnessTraitSet) railInput {
			s := railSession("sess", "/w/repo", time.Now().Add(-72*time.Hour), time.Now().Add(-48*time.Hour))
			s.Harness, s.Resumable = harness, true
			return railInput{Session: s, Repo: "/w/repo", CanRevive: true, Harnesses: set}
		}
		if revive, _ := railRevive(in("fixture", ext)); revive != ReviveTakeover {
			t.Errorf("declared takeover = %q", revive)
		}
		for _, harness := range []string{"claude", "pi", "other"} {
			if revive, _ := railRevive(in(harness, ext)); revive != "" {
				t.Errorf("%s offers %q without declaring takeover", harness, revive)
			}
		}
		if revive, _ := railRevive(in("claude", nil)); revive != "" {
			t.Errorf("a nil set offers %q", revive)
		}
	})
	t.Run("usage push and five-hour history", func(t *testing.T) {
		now := time.Now().UnixMilli()
		snap := func(adapter string) *wslive.Snapshot {
			return &wslive.Snapshot{Adapter: adapter, UpdatedAt: now, Usage: &wslive.Usage{LimitsAsOf: now, Limits: []wslive.UsageLimit{{Kind: wslive.LimitFiveHour, Percent: 12, ResetsAt: now + 10_000}}}}
		}
		store := func(set HarnessTraitSet) *planUsageStore {
			home := t.TempDir()
			s := newPlanUsageStore(home, home, func(string) string { return "" })
			s.harnesses = set
			return s
		}
		pushed := func(s *planUsageStore) bool {
			s.mu.Lock()
			defer s.mu.Unlock()
			return len(s.claude.Windows) > 0
		}
		s := store(ext)
		s.pushClaude(snap("claude"))
		if pushed(s) {
			t.Error("a harness the set does not declare pushed usage")
		}
		s.pushClaude(snap("fixture"))
		if !pushed(s) {
			t.Error("a declared harness's push was dropped")
		}
		s = store(nil)
		s.pushClaude(snap("fixture"))
		s.pushClaude(snap("claude"))
		if pushed(s) {
			t.Error("a nil set accepted a push")
		}
		if !ext.separateFiveHour("fixture") || ext.separateFiveHour("codex") || (HarnessTraitSet)(nil).separateFiveHour("codex") {
			t.Error("the five-hour history trait does not follow the declaration")
		}
	})
}
