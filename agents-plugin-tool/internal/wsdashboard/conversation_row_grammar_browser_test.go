package wsdashboard

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsauditstore"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

// Exercises shipped conversation regions and browser controllers in an isolated HTTP fixture.
// WS_CONVO_SHOTS is opt-in; leave unset until screenshot approval.
func TestConversationRowGrammarBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run isolated Chromium acceptance")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	for _, key := range []string{"HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "WS_CACHE_HOME", "WS_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		dir := filepath.Join(home, key)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	t.Setenv("WS_MCP_DASHBOARD_DEV_SOURCE", "")
	fx := &fixtureAudit{}
	f := startPageFixture(t, func(o *DaemonOptions) { o.Home, o.auditRead = os.Getenv("HOME"), fx })

	log := wswhiteboard.Effect{Kind: "log", Topic: "composer-ux", EntryID: "e7", Field: "text", After: "Mention memo result. The test failure was in the test tooling."}
	wb, ok := wslive.NewWhiteboardEffects([]wswhiteboard.Effect{log})
	if !ok {
		t.Fatal("effects")
	}
	ring := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: "user", Origin: "terminal", Text: "please check the failing test"}),
		ringRecord(2, wslive.RingEntry{Kind: "reply", Text: "On it. Looking at the failure now."}),
		ringRecord(3, wslive.RingEntry{Kind: wslive.RingWake, Cause: "subagent_report", Source: "Diagnose selection [B] test failure", Text: "The failure is a cross-realm comparison.\nsecond line"}),
		ringRecord(4, wslive.RingEntry{Kind: wslive.RingWake, Cause: "subagent_done", Source: "Merge viewed re-fix head", Caption: "finished"}),
		ringRecord(5, wslive.RingEntry{Kind: wslive.RingWake, Cause: "background_exit", Source: "shell", Caption: "exited 0"}),
		ringRecord(6, wslive.RingEntry{Kind: wslive.RingMail, Direction: "sent", Peer: "EXEC 3", Title: "[B] result. My Claude agent confirmed the result", HistoryKey: "h1"}),
		ringRecord(7, wslive.RingEntry{Kind: wslive.RingMail, Direction: "recv", Peer: "EXEC 2", Title: "1074 answer with verified source coverage", HistoryKey: "h2"}),
		ringRecord(8, wslive.RingEntry{Kind: "tools", Tools: toolsSummary()}),
		ringRecord(9, wb),
		ringRecord(10, wslive.RingEntry{Kind: "notice", Notice: &wslive.Notice{Kind: "usage_limit", Text: "limit hit"}}),
		ringRecord(11, wslive.RingEntry{Kind: wslive.RingWake, Cause: "mail", Text: boardNudgeEnvelope + "Whiteboard lead nudge (unblocked).\n- One: ready to proceed; resolved targets: plan"}),
		// a compaction pill between tool and turn-footer rows, for the spacing check
		ringRecord(12, wslive.RingEntry{Kind: "tools", Tools: toolsSummary()}),
		ringRecord(13, wslive.RingEntry{Kind: wslive.RingTurn, Origin: "terminal", Done: 12, Failed: 2, DurationMs: 372_000}),
		ringRecord(14, wslive.RingEntry{Kind: wslive.RingCompact, Trigger: wslive.TriggerManual}),
		ringRecord(15, wslive.RingEntry{Kind: wslive.RingWake, Cause: "subagent_report", Source: "Merge landing root-guide follow-up", Text: "Merged."}),
		ringRecord(17, wslive.RingEntry{Kind: wslive.RingCompact, CompactFailed: true, Text: "prompt too long"}),
		ringRecord(18, wslive.RingEntry{Kind: wslive.RingUI, UI: &wslive.UICommand{Op: wslive.UISearch, Arg: "row grammar"}}),
		ringRecord(16, wslive.RingEntry{Kind: wslive.RingTurn, Origin: "terminal", DurationMs: 0}),
	}
	// Append through the real ring writer so audit times and data-at share a clock.
	var entries []wslive.RingEntry
	callTools := &wslive.ToolSummary{Calls: 1, Failed: 1, DurationMs: 900, Batches: []wslive.ToolBatch{{Names: []string{"Bash"}, Calls: 1, Failed: 1, CallArgs: []wslive.ToolCallArgs{{Name: "Bash", ID: "grammar-call", ArgsPreview: "go test ./internal/wsdashboard"}}}}}
	for i := range ring {
		if ring[i].Kind == "tools" {
			ring[i].Tools = callTools
			break
		}
	}
	for _, rec := range ring {
		entries = append(entries, rec.RingEntry)
	}
	before := time.Now().UTC()
	if err := wslive.AppendRing(wslive.RingDir(SessionDir(f.dir, "livesession")), entries); err != nil {
		t.Fatal(err)
	}
	f.src.setPage("livesession", PageContent{MailBodies: map[string]string{"h1": "body one", "h2": "body two"}})
	writeSnapshot(t, f.dir, "livesession", &wslive.Snapshot{SchemaVersion: 1, Adapter: "claude", UpdatedAt: time.Now().UnixMilli(), HeartbeatMs: 30000, Host: &wslive.Host{}})
	f.change(t, "livesession", f.project, "row grammar", 5)
	records, err := wslive.ReadRing(wslive.RingDir(SessionDir(f.dir, "livesession")))
	if err != nil {
		t.Fatal(err)
	}
	var events []wsaudit.Envelope
	add := func(id string, at time.Time, kind wsaudit.Kind, sub, call, agent string) {
		events = append(events, wsaudit.Envelope{V: 1, ID: id, Seq: uint64(len(events) + 1), Session: "livesession", At: at, Harness: "claude", Kind: kind, Subkind: sub, CallID: call, AgentID: agent, In: `{"command":"go test ./internal/wsdashboard"}`, Out: "fixture result"})
	}
	add("grammar-init", records[0].At, wsaudit.KindSystem, "init", "", "")
	events[0].Out = strings.Repeat("System instruction. ", 900)
	callAdded := false
	for _, rec := range records {
		switch rec.Kind {
		case wslive.RingTools:
			if !callAdded {
				add("grammar-call-event", rec.At, wsaudit.KindToolCall, "Bash", "grammar-call", "")
				add("grammar-hook", rec.At, wsaudit.KindHook, "PreToolUse", "grammar-call", "")
				add("grammar-progress", rec.At, wsaudit.KindSystem, "tool_progress", "grammar-call", "")
				add("grammar-result", rec.At, wsaudit.KindToolResult, "Bash", "grammar-call", "")
				events[len(events)-1].Meta = map[string]any{"is_error": true}
				callAdded = true
			}
		case wslive.RingCompact:
			if rec.CompactFailed {
				add("grammar-compact-fail", rec.At, wsaudit.KindSystem, "compact_failed", "", "")
			} else {
				add("grammar-compact", rec.At, wsaudit.KindCompaction, "compact_boundary", "", "")
			}
		case wslive.RingWake:
			if rec.Cause == "subagent_done" {
				add("grammar-task", rec.At, wsaudit.KindSubagent, "task_notification", "", "fixture-agent")
			}
		}
	}
	add("grammar-thinking", before, wsaudit.KindThinking, "reasoning", "", "")
	add("grammar-reminder", before, wsaudit.KindSystemReminder, "reminder", "", "")
	add("grammar-retry", time.Now().UTC(), wsaudit.KindSystem, "api_retry", "", "")
	add("grammar-approval", time.Now().UTC(), wsaudit.KindSystem, "control_request:can_use_tool", "grammar-approval-call", "")
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	for i := range events {
		events[i].Seq = uint64(i + 1)
	}
	fx.set("livesession", wsauditstore.View{Events: events})
	// Produce live-state variants with the same server renderer as the dashboard.
	variants := map[string]string{}
	for name, lead := range map[string]wslive.Lead{
		"compacting": {Activity: wslive.Activity{Phase: wslive.PhaseCompacting, PhaseStartedAt: before.UnixMilli()}},
		"retry":      {},
	} {
		session := liveSession("livesession", f.project, before)
		session.Hosted = true
		snap := liveSnap(before, lead)
		if name == "retry" {
			snap.Host = &wslive.Host{Retry: &wslive.Retry{Attempt: 2, MaxRetries: 10, Error: "overloaded", Status: 529}}
		}
		view := buildSessionView(pageInputs{Session: session, Snapshot: snap, Now: before})
		regions, err := renderSessionRegions(view, nil)
		if err != nil {
			t.Fatal(err)
		}
		variants[name] = string(regions[regionConvLive].HTML)
	}
	dir := t.TempDir()
	payload, err := json.Marshal(variants)
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "live.json")
	if err := os.WriteFile(fixture, payload, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, "testdata/conversation-row-grammar.browser.cjs", f.base, fixture, os.Getenv("WS_CONVO_SHOTS")).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("row grammar: %v", err)
	}
}
