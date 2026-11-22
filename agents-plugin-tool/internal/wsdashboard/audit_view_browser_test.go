package wsdashboard

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsauditstore"
	"github.com/kang-sw/devenv/internal/wslive"
)

// TestAuditViewBrowser exercises Phase 1 call rows, lazy hydration, floating
// audit window, narrow bottom sheet, 5 tabs, stored truncation / seen cuts /
// redactions, keyboard navigation, pin cap, error states, and optional
// visual captures (when WS_AUDIT_SHOTS is set).
func TestAuditViewBrowser(t *testing.T) {
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

	f := startPageFixture(t, func(o *DaemonOptions) { o.Home = os.Getenv("HOME") })

	// Populate RingTools entries: one ID'd rich group and one legacy fallback.
	ringDir := wslive.RingDir(SessionDir(f.dir, "livesession"))
	err = wslive.AppendRing(ringDir, []wslive.RingEntry{
		{
			Kind: wslive.RingTools,
			Tools: &wslive.ToolSummary{
				Calls:      2,
				DurationMs: 400,
				Tools:      []wslive.ToolTally{{Name: "Glob", Calls: 2}},
				Batches:    []wslive.ToolBatch{{Names: []string{"Glob"}, Calls: 2}},
			},
		},
		{
			Kind: wslive.RingTools,
			Tools: &wslive.ToolSummary{
				Calls:      6,
				Failed:     1,
				DurationMs: 2400,
				Tools: []wslive.ToolTally{
					{Name: "Read", Calls: 1},
					{Name: "Grep", Calls: 1},
					{Name: "Bash", Calls: 1},
					{Name: "Edit", Calls: 2},
					{Name: "Write", Calls: 1},
				},
				Batches: []wslive.ToolBatch{
					{
						Names: []string{"Read", "Grep", "Bash", "Edit"},
						Calls: 5,
						CallArgs: []wslive.ToolCallArgs{
							{Name: "Read", ArgsPreview: "file_path: src/auth.ts", ID: "c1"},
							{Name: "Grep", ArgsPreview: "pattern: sessionSecret", ID: "c2"},
							{Name: "Bash", ArgsPreview: "cmd: go test ./internal/wsdashboard", ID: "c3"},
							{Name: "Edit", ArgsPreview: "file_path: src/config.go", ID: "c4"},
							{Name: "Edit", ArgsPreview: "file_path: empty.txt", ID: "c_empty"},
						},
					},
					{
						Names: []string{"Write"},
						Calls: 1,
						CallArgs: []wslive.ToolCallArgs{
							{Name: "Write", ArgsPreview: "file_path: notes.md"}, // static uncapped/no-ID
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.change(t, "livesession", f.project, "live board text", 4)

	// Build rich audit events matching c1..c4 (+ c_empty intentionally absent).
	t0 := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	headPart := "{\n  \"path\": \"src/auth.ts\",\n  \"token\": \"***\"\n}\n"
	tailPart := "tail line with needle\nsecond unseen line\n"
	seamSeen := len(headPart)
	longLine := "ok line needle here in paged output " + strings.Repeat("x", 16300) + "\n"
	largeOut := strings.Repeat(longLine, 530) // >8 MiB to trigger auditBodyMax range/load-more with ~530 lines

	fx := &fixtureAudit{}
	fx.set("livesession", wsauditstore.View{
		Events: []wsaudit.Envelope{
			{
				V: 1, ID: "e-hook-1", Seq: 1, Session: "livesession", At: t0.Add(-500 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindHook, Subkind: "PreToolUse", CallID: "c1",
				ModelSeen: false,
				In:        `{"hook":"PreToolUse","tool":"Read"}`,
				Out:       `{"decision":"allow"}`,
			},
			{
				V: 1, ID: "e-call-1", Seq: 2, Session: "livesession", At: t0,
				Harness: "claude", Kind: wsaudit.KindToolCall, Subkind: "Read", CallID: "c1",
				ModelSeen: true,
				In:        headPart + tailPart,
				Out:       largeOut,
				Meta: map[string]any{
					"request":        map[string]any{"id": "req-1", "ctx": 48000},
					"signature_only": false,
				},
				Redactions: []wsaudit.Redaction{
					{Field: wsaudit.FieldIn, Offset: strings.Index(headPart, "***"), Rule: "api-key", Length: 36, Count: 1},
				},
				StoredTrunc: &wsaudit.StoredTrunc{
					In: &wsaudit.BodyTrunc{OriginalBytes: 14000, HeadBytes: len(headPart), TailBytes: len(tailPart)},
				},
				Seen: &wsaudit.SeenBounds{In: &seamSeen},
			},
			{
				V: 1, ID: "e-res-1", Seq: 3, Session: "livesession", At: t0.Add(180 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindToolResult, Subkind: "Read", CallID: "c1",
				ModelSeen: true,
				Out:       "read 14KB result",
			},
			{
				V: 1, ID: "e-call-2", Seq: 4, Session: "livesession", At: t0.Add(10 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindToolCall, Subkind: "Grep", CallID: "c2",
				ModelSeen: true,
				In:        `{"pattern":"sessionSecret"}`,
				Out:       "src/auth.ts:12: const sessionSecret = '***';",
				Meta: map[string]any{
					"request": map[string]any{"id": "req-1", "ctx": 48000}, // parallel sibling!
				},
			},
			{
				V: 1, ID: "e-res-2", Seq: 5, Session: "livesession", At: t0.Add(90 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindToolResult, Subkind: "Grep", CallID: "c2",
				ModelSeen: true,
				Out:       "1 match",
			},
			{
				V: 1, ID: "e-call-3", Seq: 6, Session: "livesession", At: t0.Add(500 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindToolCall, Subkind: "Bash", CallID: "c3",
				ModelSeen: true,
				In:        `{"cmd":"go test ./internal/wsdashboard"}`,
				Out:       "FAIL\tTestSomething (0.12s)\nexit status 1",
				Meta: map[string]any{
					"request":  map[string]any{"id": "req-2", "prev_id": "req-1", "ctx": 50400},
					"is_error": true,
				},
			},
			{
				V: 1, ID: "e-res-3", Seq: 7, Session: "livesession", At: t0.Add(1400 * time.Millisecond),
				Harness: "claude", Kind: wsaudit.KindToolResult, Subkind: "Bash", CallID: "c3",
				ModelSeen: true,
				Out:       "error: test failed",
			},
			{
				V: 1, ID: "e-call-4", Seq: 8, Session: "livesession", At: t0.Add(1600 * time.Millisecond),
				Harness: "pi", Kind: wsaudit.KindOther, CallID: "c4",
				ModelSeen: true,
				Raw:       `{"type":"raw_edit_event","file":"src/config.go"}`,
			},
			{
				V: 1, ID: "e-next-req", Seq: 9, Session: "livesession", At: t0.Add(2 * time.Second),
				Harness: "claude", Kind: wsaudit.KindAssistantText,
				ModelSeen: true,
				Out:       "Done checking.",
				Meta: map[string]any{
					"request": map[string]any{"id": "req-3", "prev_id": "req-2", "ctx": 52100},
				},
			},
		},
	})
	f.d.auditRead = fx

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/audit-view.browser.cjs")
	cmd.Env = append(os.Environ(), "AUDIT_BASE="+f.base, "AUDIT_SHOTS="+os.Getenv("WS_AUDIT_SHOTS"))
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Log(out.String())
		t.Fatalf("audit view Chromium: %v", err)
	}
	t.Log(out.String())
}
