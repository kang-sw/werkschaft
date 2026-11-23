package wsdashboard

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsauditstore"
	"github.com/kang-sw/devenv/internal/wslive"
)

// TestAuditViewPhase2Browser exercises Phase 2 hidden-event interleaving,
// UTC [since, until) history paging, open-below oldest on end:true, SSE after=
// incremental refresh, 410 reset & 503 retry, no filter bar or header
// harness chip, J/K/Enter conversation navigation & Left/Right window
// stepping, and optional 4-shot visual captures (when WS_AUDIT_P2_SHOTS is set).
func TestAuditViewPhase2Browser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE for disposable Chromium acceptance")
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

	// The reader is handed over before the daemon serves; its events are set later,
	// under its own lock, so no request races an assignment to the daemon.
	fx := &fixtureAudit{}
	f := startPageFixture(t, func(o *DaemonOptions) { o.Home, o.auditRead = os.Getenv("HOME"), fx })
	ringDir := wslive.RingDir(SessionDir(f.dir, "livesession"))

	tBeforeAll := time.Now().UTC().Add(-2 * time.Second)

	// 1. Seed 4 older entries so initial 50-entry render leaves them in /history (with end:true).
	var older []wslive.RingEntry
	for i := 1; i <= 4; i++ {
		kind := wslive.RingUser
		if i%2 == 0 {
			kind = wslive.RingReply
		}
		older = append(older, wslive.RingEntry{Kind: kind, Text: "older history entry"})
	}
	if err := wslive.AppendRing(ringDir, older); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)
	tInHist := time.Now().UTC()
	time.Sleep(15 * time.Millisecond)

	// 2. Seed 50 newer entries: 47 filler turns + structured replies + 2 tool batches + final reply.
	var newer []wslive.RingEntry
	for i := 1; i <= 47; i++ {
		newer = append(newer, wslive.RingEntry{Kind: wslive.RingReply, Text: "recent turn output"})
	}
	if err := wslive.AppendRing(ringDir, newer); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)
	tBetweenEntries := time.Now().UTC()
	time.Sleep(15 * time.Millisecond)

	if err := wslive.AppendRing(ringDir, []wslive.RingEntry{
		{Kind: wslive.RingUser, Text: "Check config and tests"},
		{
			Kind: wslive.RingTools,
			Tools: &wslive.ToolSummary{
				Calls:      3,
				DurationMs: 650,
				Tools: []wslive.ToolTally{
					{Name: "Read", Calls: 1},
					{Name: "Grep", Calls: 1},
					{Name: "Bash", Calls: 1},
				},
				Batches: []wslive.ToolBatch{
					{
						Names: []string{"Read", "Grep", "Bash"},
						Calls: 3,
						CallArgs: []wslive.ToolCallArgs{
							{Name: "Read", ArgsPreview: "file_path: internal/wsdashboard/pages.go", ID: "p2-c1"},
							{Name: "Grep", ArgsPreview: "pattern: audit_view", ID: "p2-c2"},
							{Name: "Bash", ArgsPreview: "cmd: go test ./internal/wsdashboard", ID: "p2-c3-folded"},
						},
					},
				},
			},
		},
		{Kind: wslive.RingReply, Text: "Completed audit row check."},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)
	tAfterTools := time.Now().UTC()

	f.change(t, "livesession", f.project, "live board text", 5)

	fx.set("livesession", wsauditstore.View{
		Events: []wsaudit.Envelope{
			{
				V: 1, ID: "p2-sys-start", Seq: 1, Session: "livesession", At: tBeforeAll,
				Harness: "claude", Kind: wsaudit.KindSystem, Subkind: "system_prompt",
				ModelSeen: true,
				Out:       "System instructions seeded for audit session start.",
			},
			{
				V: 1, ID: "p2-think-hist", Seq: 2, Session: "livesession", At: tInHist,
				Harness: "claude", Kind: wsaudit.KindThinking, Subkind: "reasoning",
				ModelSeen: false,
				Out:       "Considering archived context before recent turns.",
			},
			{
				V: 1, ID: "p2-attach-hook", Seq: 3, Session: "livesession", At: tBetweenEntries.Add(5 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindHook, Subkind: "PreToolUse", CallID: "p2-c1",
				ModelSeen: false,
				In:        `{"hook":"PreToolUse","tool":"Read"}`,
				Out:       `{"allow":true}`,
			},
			{
				V: 1, ID: "p2-call-1", Seq: 4, Session: "livesession", At: tBetweenEntries.Add(8 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindToolCall, Subkind: "Read", CallID: "p2-c1",
				ModelSeen: true,
				In:        `{"file_path":"internal/wsdashboard/pages.go"}`,
			},
			{
				V: 1, ID: "p2-call-2", Seq: 5, Session: "livesession", At: tBetweenEntries.Add(10 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindToolCall, Subkind: "Grep", CallID: "p2-c2",
				ModelSeen: true,
				In:        `{"pattern":"audit_view"}`,
			},
			{
				V: 1, ID: "p2-orphan-hook", Seq: 6, Session: "livesession", At: tBetweenEntries.Add(11 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindHook, Subkind: "PostToolUse", CallID: "p2-missing-call",
				ModelSeen: false,
				Out:       `{"note":"orphan call id fallback"}`,
			},
			{
				V: 1, ID: "p2-reminder", Seq: 7, Session: "livesession", At: tAfterTools.Add(1 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindSystemReminder, Subkind: "policy_reminder",
				ModelSeen: true,
				Out:       "Remember to inspect screenshot captures.",
			},
			{
				V: 1, ID: "p2-subagent", Seq: 8, Session: "livesession", At: tAfterTools.Add(2 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindSubagent, Subkind: "spawn",
				ModelSeen: false,
				In:        `{"role":"verifier"}`,
			},
			{
				V: 1, ID: "p2-compact", Seq: 9, Session: "livesession", At: tAfterTools.Add(3 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindCompaction, Subkind: "auto",
				ModelSeen: false,
				Out:       "Compacted older messages.",
			},
			{
				V: 1, ID: "p2-other", Seq: 10, Session: "livesession", At: tAfterTools.Add(3*time.Millisecond + 500*time.Nanosecond),
				Harness: "pi", Kind: wsaudit.KindOther, Subkind: "custom_event",
				ModelSeen: true,
				Raw:       `{"custom":"raw-phase2-payload"}`,
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/audit-view-phase2.browser.cjs")
	cmd.Env = append(os.Environ(), "AUDIT_BASE="+f.base, "AUDIT_P2_SHOTS="+os.Getenv("WS_AUDIT_P2_SHOTS"))
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Log(out.String())
		t.Fatalf("audit view phase 2 Chromium: %v", err)
	}
	t.Log(out.String())
}
