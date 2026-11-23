package wsdashboard

import (
	"bytes"
	"context"
	"fmt"
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

// TestAuditViewPhase3Browser verifies Phase 3 conversation-wide audit search,
// badges, subagent parent_id routing, cursor pagination, truncated-body line
// jump, regex/case/kind filters, error/empty states, and 4 optional screenshots.
func TestAuditViewPhase3Browser(t *testing.T) {
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

	t0 := time.Now().UTC().Add(-2 * time.Second)
	var filler []wslive.RingEntry
	for i := 1; i <= 10; i++ {
		filler = append(filler, wslive.RingEntry{Kind: wslive.RingReply, Text: "prior turn output"})
	}
	if err := wslive.AppendRing(ringDir, filler); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)
	tAfterFiller := time.Now().UTC()
	time.Sleep(15 * time.Millisecond)

	if err := wslive.AppendRing(ringDir, []wslive.RingEntry{
		{Kind: wslive.RingUser, Text: "Run phase 3 search checks"},
		{
			Kind: wslive.RingTools,
			Tools: &wslive.ToolSummary{
				Calls:      3,
				DurationMs: 420,
				Tools: []wslive.ToolTally{
					{Name: "Bash", Calls: 2},
					{Name: "Agent", Calls: 1},
				},
				Batches: []wslive.ToolBatch{
					{
						Names: []string{"Bash", "Agent", "Bash"},
						Calls: 3,
						CallArgs: []wslive.ToolCallArgs{
							{Name: "Bash", ArgsPreview: "cmd: grep ALPHA_NEEDLE", ID: "p3-c-multi"},
							{Name: "Agent", ArgsPreview: "subagent check", ID: "p3-c-parent"},
							{Name: "Bash", ArgsPreview: "cmd: long log", ID: "p3-c-trunc"},
						},
					},
				},
			},
		},
		{Kind: wslive.RingReply, Text: "Audit search fixture ready."},
	}); err != nil {
		t.Fatal(err)
	}

	var truncLines strings.Builder
	for i := 1; i <= 455; i++ {
		if i == 450 {
			truncLines.WriteString("line 450 TRUNC_NEEDLE_450 reached\n")
		} else {
			fmt.Fprintf(&truncLines, "padding line %d\n", i)
		}
	}

	events := []wsaudit.Envelope{
		{
			V: 1, ID: "p3-ev-unrendered", Seq: 1, Session: "livesession", At: t0.Add(1 * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindToolResult, Subkind: "Bash", CallID: "p3-c-unrendered", ModelSeen: true,
			Out: "first line\nALPHA_NEEDLE UNRENDERED_NEEDLE here on line 2\nthird line\n",
		},
		{
			V: 1, ID: "p3-ev-think", Seq: 2, Session: "livesession", At: tAfterFiller.Add(2 * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindThinking, Subkind: "reasoning", ModelSeen: true,
			Out: "thought line 1\nALPHA_NEEDLE in thinking row\n",
		},
		{
			V: 1, ID: "p3-ev-multi-in", Seq: 3, Session: "livesession", At: t0.Add(3 * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindToolCall, Subkind: "Bash", CallID: "p3-c-multi", ModelSeen: true,
			In: "input header\ncmd: ALPHA_NEEDLE input match\n",
		},
		{
			V: 1, ID: "p3-ev-multi-out", Seq: 4, Session: "livesession", At: t0.Add(4 * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindToolResult, Subkind: "Bash", CallID: "p3-c-multi", ModelSeen: true,
			Out: "out line 1 plain\nout line 2 ALPHA_NEEDLE first\nout line 3 ALPHA_NEEDLE second\n",
		},
		{
			V: 1, ID: "p3-ev-parent-call", Seq: 5, Session: "livesession", At: t0.Add(5 * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindToolCall, Subkind: "Agent", CallID: "p3-c-parent", ModelSeen: true,
			In: `{"prompt":"run child task"}`, Out: "spawned child",
		},
		{
			V: 1, ID: "p3-ev-sub", Seq: 6, Session: "livesession", At: t0.Add(6 * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindSubagent, Subkind: "child_result",
			CallID: "p3-c-sub", ParentID: "p3-c-parent", ModelSeen: true,
			In: "child start", Out: "sub 1\nsub 2\nsub 3 ALPHA_NEEDLE from subagent\n",
		},
		{
			V: 1, ID: "p3-ev-trunc", Seq: 7, Session: "livesession", At: t0.Add(7 * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindToolResult, Subkind: "Bash", CallID: "p3-c-trunc", ModelSeen: true,
			Out: truncLines.String(),
		},
		{
			V: 1, ID: "p3-ev-case", Seq: 8, Session: "livesession", At: t0.Add(8 * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindToolResult, Subkind: "Bash", CallID: "p3-c-multi", ModelSeen: true,
			Out: "lowercase casecheck_77\nExact CaseCheck_77 match\n",
		},
	}

	seq := uint64(20)
	for i := 1; i <= 105; i++ {
		seq++
		events = append(events, wsaudit.Envelope{
			V: 1, ID: fmt.Sprintf("p3-pg-%03d", i), Seq: seq, Session: "livesession",
			At:      t0.Add(time.Duration(100+i) * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindToolResult, Subkind: "Bash",
			CallID:    "p3-c-multi",
			ModelSeen: true,
			Out:       fmt.Sprintf("item %03d PAGINATED_TOKEN line", i),
		})
	}

	fx.set("livesession", wsauditstore.View{Events: events})
	f.change(t, "livesession", f.project, "live phase 3 text", 5)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/audit-view-phase3.browser.cjs")
	cmd.Env = append(os.Environ(), "AUDIT_BASE="+f.base, "AUDIT_P3_SHOTS="+os.Getenv("WS_AUDIT_P3_SHOTS"))
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Log(out.String())
		t.Fatalf("audit view phase 3 Chromium: %v", err)
	}
	t.Log(out.String())
}
