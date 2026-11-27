package wsdashboard

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

type livePolishAgentHost struct{}

func (*livePolishAgentHost) Start(func(string, string)) {}
func (*livePolishAgentHost) InboxChanged(string)        {}
func (*livePolishAgentHost) Close()                     {}
func (*livePolishAgentHost) SessionMeta(id string) (AgentSessionMeta, bool) {
	if id != "polishsession" {
		return AgentSessionMeta{}, false
	}
	return AgentSessionMeta{
		Hosted:     true,
		HasProcess: true,
		SafeStop:   true,
		Model:      "claude-sonnet-4-6",
		Effort:     "high",
	}, true
}
func (*livePolishAgentHost) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"backend":"claude","status":"running","can_control":true,"process":true}`))
	})
}

func TestConvUXRestyleLivePolishBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium")
	}
	t.Setenv("WS_MCP_DASHBOARD_DEV_SOURCE", "")
	dir := t.TempDir()
	project := t.TempDir()
	now := time.Now()
	if err := WriteRegistration(dir, Registration{
		ReplyID:      "polishsession",
		Project:      project,
		Harness:      "claude",
		Hosted:       true,
		Agent:        true,
		RegisteredAt: now.Add(-5 * time.Minute).UTC().Format(time.RFC3339Nano),
		HeartbeatAt:  now.UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	src := newPageFake()
	src.setPage("polishsession", PageContent{
		HasBoard: true,
		Seq:      4,
		Board:    boardRegions(t, "Live polish board content"),
		MailBodies: map[string]string{
			"m-sent-1": "Sent report body line 1\nline 2",
			"m-recv-1": "Recv reply body line 1\nline 2",
			"m-only-1": "Mail only 1 body",
			"m-only-2": "Mail only 2 body",
		},
	})
	ringEntries := []wslive.RingEntry{
		// Turn 1: tools + sent mail + recv mail + mail wake -> folds into one .d-turn with per-tool tokens & mail count
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "Inspect tool+mail folding and flat unified drawer"},
		{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{
			Calls: 2, DurationMs: 240, InBytes: 400, OutBytes: 800,
			Batches: []wslive.ToolBatch{{
				Calls: 2, DurationMs: 240,
				CallArgs: []wslive.ToolCallArgs{
					{ID: "call-read-1", Name: "Read", ArgsPreview: "dashboard-conversation.css", InBytes: 200, OutBytes: 600},
					{ID: "call-edit-2", Name: "Edit", ArgsPreview: "dashboard.tmpl", InBytes: 200, OutBytes: 200},
				},
			}},
		}},
		{Kind: wslive.RingMail, Direction: wslive.MailSent, Peer: "worker@worktree", Title: "Dispatch live polish task", HistoryKey: "m-sent-1", BodyBytes: 320},
		{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: "worker@worktree", Title: "Completed live polish task", HistoryKey: "m-recv-1", BodyBytes: 440},
		{Kind: wslive.RingReply, Text: "Tool and mail turn completed cleanly."},
		{Kind: wslive.RingTurn, Origin: wslive.OriginWhiteboard, Done: 2, DurationMs: 3100, AddedTokens: 1850, Reason: "completed"},
		// Turn 2: mail-only completed turn (0 tools, 2 mails)
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "Check mail-only turn"},
		{Kind: wslive.RingMail, Direction: wslive.MailSent, Peer: "peer@worktree", Title: "Ping peer", HistoryKey: "m-only-1", BodyBytes: 120},
		{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: "peer@worktree", Title: "Pong peer", HistoryKey: "m-only-2", BodyBytes: 160},
		{Kind: wslive.RingReply, Text: "Mail-only turn done."},
		{Kind: wslive.RingTurn, Origin: wslive.OriginWhiteboard, DurationMs: 900, AddedTokens: 420, Reason: "completed"},
		// Active open turn: flushed mail + live tool gap
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "Run live turn with active motion"},
		{Kind: wslive.RingMail, Direction: wslive.MailSent, Peer: "worker@worktree", Title: "Live turn status ping", HistoryKey: "m-sent-1", BodyBytes: 140},
	}
	if err := wslive.AppendRing(wslive.RingDir(SessionDir(dir, "polishsession")), ringEntries); err != nil {
		t.Fatal(err)
	}
	writeSnapshot(t, dir, "polishsession", &wslive.Snapshot{
		SchemaVersion: 1,
		Adapter:       "claude",
		UpdatedAt:     now.UnixMilli(),
		HeartbeatMs:   3600000,
		Lead: wslive.Lead{
			Activity: wslive.Activity{
				Phase:          wslive.PhaseTool,
				PhaseStartedAt: now.Add(-2 * time.Second).UnixMilli(),
				Done:           2,
				Batches: []wslive.BatchView{
					{ToolNames: []string{"ws/exec.shell"}, CallCount: 2, StartedAt: now.Add(-5 * time.Second).UnixMilli(), EndedAt: now.Add(-2 * time.Second).UnixMilli()},
					{StartedAt: now.Add(-2 * time.Second).UnixMilli(), Running: &wslive.RunningView{ToolName: "ws/exec.shell", ArgsPreview: "go test ./internal/wsdashboard/...", StartedAt: now.Add(-2 * time.Second).UnixMilli()}},
				},
			},
			TurnStartedAt:   now.Add(-6 * time.Second).UnixMilli(),
			TurnAddedTokens: 960,
			ToolGap: &wslive.ToolSummary{
				Calls: 2, DurationMs: 2800, InBytes: 300, OutBytes: 900,
				Batches: []wslive.ToolBatch{{Names: []string{"ws/exec.shell"}, Calls: 2, DurationMs: 2800}},
			},
		},
	})
	d := startIndexedDaemon(t, dir, src, time.Hour, func(o *DaemonOptions) {
		o.noGitBadge = true
		o.Home = t.TempDir()
		o.Agents = &livePolishAgentHost{}
	})
	waitFor(t, "polishsession indexed", func() bool {
		_, ok := d.Index().Session("polishsession")
		return d.Index().Loaded() && ok
	})
	base := endpointURL(d.Endpoint(), "")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/conv-ux-restyle-live-polish.browser.cjs", base).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("live polish browser verification: %v", err)
	}
}
