package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestConversationRowOverflowBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium")
	}
	t.Setenv("WS_MCP_DASHBOARD_DEV_SOURCE", "")
	f := startPageFixture(t, func(o *DaemonOptions) { o.Home = t.TempDir() })
	preview := strings.Repeat("Long conversation preview ", 26)
	entries := []wslive.RingEntry{
		{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: "id:receivermailgeometry", Title: preview, BodyBytes: 752},
		{Kind: wslive.RingMail, Direction: wslive.MailSent, Peer: "id:recipientmailgeometry", Title: preview, BodyBytes: 752},
		{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{
			Calls: 1, InBytes: 460, DurationMs: 50,
			Batches: []wslive.ToolBatch{{Calls: 1, DurationMs: 50, CallArgs: []wslive.ToolCallArgs{
				{ID: "overflow-call", Name: "mcp__ws__mailbox_send", ArgsPreview: preview, InBytes: 460},
			}}},
		}},
		{Kind: wslive.RingReply, Text: "The lead reply stays in view."},
		{Kind: wslive.RingTurn, Origin: "terminal", Done: 1, DurationMs: 2000},
	}
	if err := wslive.AppendRing(wslive.RingDir(SessionDir(f.dir, "livesession")), entries); err != nil {
		t.Fatal(err)
	}
	f.change(t, "livesession", f.project, "row overflow", 5)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/conversation-row-overflow.browser.cjs", f.base, os.Getenv("WS_CONVO_OVERFLOW_SHOTS")).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal(err)
	}
}
