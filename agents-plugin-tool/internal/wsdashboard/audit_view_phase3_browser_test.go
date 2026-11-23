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

// TestAuditViewPhase3Browser verifies the conversation draws no audit search
// bar, kind filter chips, or header harness chip; that a call row still opens
// its popup; and that the popup's own find box draws the server's match spans
// (Unicode, rune width, scoped flags, stored-truncation seams), with optional
// screenshots.
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

	if err := wslive.AppendRing(ringDir, []wslive.RingEntry{
		{Kind: wslive.RingUser, Text: "Run phase 3 popup checks"},
		{
			Kind: wslive.RingTools,
			Tools: &wslive.ToolSummary{
				Calls:      1,
				DurationMs: 420,
				Tools: []wslive.ToolTally{
					{Name: "Bash", Calls: 1},
				},
				Batches: []wslive.ToolBatch{
					{
						Names: []string{"Bash"},
						Calls: 1,
						CallArgs: []wslive.ToolCallArgs{
							{Name: "Bash", ArgsPreview: "cmd: grep ALPHA_NEEDLE", ID: "p3-c-multi"},
						},
					},
				},
			},
		},
		{Kind: wslive.RingReply, Text: "Audit popup fixture ready."},
	}); err != nil {
		t.Fatal(err)
	}

	events := []wsaudit.Envelope{
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
	}

	// Events for the inspector's server-drawn marks: Unicode, rune width, and
	// stored_trunc seam lines (head "xa" + tail "bz", head "pre alpha" + tail
	// "beta post"). No rendered row names them: the test opens them by id.
	events = append(events,
		wsaudit.Envelope{
			V: 1, ID: "p3-ev-uni", Seq: 9, Session: "livesession", At: t0.Add(9 * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindToolResult, Subkind: "Bash", CallID: "p3-c-uni", ModelSeen: true,
			Out: "plain FOO line\nτα ελληνικά αβγ here\n😀\n😀a\n",
		},
		wsaudit.Envelope{
			V: 1, ID: "p3-ev-seam1", Seq: 10, Session: "livesession", At: t0.Add(10 * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindToolResult, Subkind: "Bash", CallID: "p3-c-seam1", ModelSeen: true,
			Out:         "xabz\nlast",
			StoredTrunc: &wsaudit.StoredTrunc{Out: &wsaudit.BodyTrunc{OriginalBytes: 5000, HeadBytes: 2, TailBytes: 8}},
		},
		wsaudit.Envelope{
			V: 1, ID: "p3-ev-seam2", Seq: 11, Session: "livesession", At: t0.Add(11 * time.Millisecond),
			Harness: "claude", Kind: wsaudit.KindToolResult, Subkind: "Bash", CallID: "p3-c-seam2", ModelSeen: true,
			Out:         "pre alphabeta post\nend",
			StoredTrunc: &wsaudit.StoredTrunc{Out: &wsaudit.BodyTrunc{OriginalBytes: 5000, HeadBytes: 9, TailBytes: 13}},
		},
	)

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
