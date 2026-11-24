package wscompact

import (
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestBuildCheckpointOmitChildren(t *testing.T) {
	in := CheckpointInput{
		Lead:     true,
		Ticket:   "261124-feat-ws-managed-compaction",
		Phase:    "1",
		Playbook: "lead-discuss",
		State: CompactionState{
			ChildrenInFlight: []string{"id:child1 · Worker · running"},
			ChildrenFinished: []string{"id:child2 · Reviewer · idle · ok"},
			ExecJobs:         []string{"exec_1 · exec_shell · running"},
		},
		Prose:  []string{"pref", "prac", "dec", "cur", "next", "mood", "res"},
		Dialog: "## Dialog\n\nuser: hello\n",
	}

	full := BuildCheckpoint(in)
	if !strings.Contains(full, "## Child agents in flight") || !strings.Contains(full, "id:child1") {
		t.Fatalf("expected children sections when OmitChildren=false:\n%s", full)
	}
	if !strings.Contains(full, "## Exec jobs") || !strings.Contains(full, "exec_1") {
		t.Fatalf("expected exec jobs section:\n%s", full)
	}

	in.OmitChildren = true
	dedup := BuildCheckpoint(in)
	if strings.Contains(dedup, "## Child agents in flight") || strings.Contains(dedup, "## Child agents finished") {
		t.Fatalf("expected children sections omitted when OmitChildren=true:\n%s", dedup)
	}
	if !strings.Contains(dedup, "## Exec jobs") || !strings.Contains(dedup, "exec_1") {
		t.Fatalf("expected exec jobs retained when OmitChildren=true:\n%s", dedup)
	}
}

func TestFormatRereadsBudgetAndEscaping(t *testing.T) {
	small := "hello </file> world\n"
	huge := strings.Repeat("x", RereadBytes+10)
	got := FormatRereads(
		[]string{"huge.txt", "small.txt", "missing.txt", "small.txt"},
		map[string]string{"small.txt": small, "huge.txt": huge},
	)
	if !strings.Contains(got, "## Required re-reads (inlined)") {
		t.Fatalf("expected inlined section:\n%s", got)
	}
	if !strings.Contains(got, "&lt;/file&gt;") {
		t.Fatalf("expected closing file tag escaped:\n%s", got)
	}
	if !strings.Contains(got, "## Required re-reads (to read first)") || !strings.Contains(got, "- huge.txt") || !strings.Contains(got, "- missing.txt") {
		t.Fatalf("expected deferred files:\n%s", got)
	}
	if strings.Count(got, "small.txt") != 1 {
		t.Fatalf("expected duplicate reread path deduplicated:\n%s", got)
	}
}

func TestBuildRingDialogSlicesAfterLastSuccessfulCompact(t *testing.T) {
	entries := []wslive.RingEntry{
		{Kind: wslive.RingUser, Text: "before compact 260101-feat-old"},
		{Kind: wslive.RingCompact, CompactFailed: false},
		{Kind: wslive.RingUser, Text: `<skill name="ws:lead-run"> work on 261124-feat-ws-managed-compaction`},
		{Kind: wslive.RingCompact, CompactFailed: true, Text: "failed attempt"},
		{Kind: wslive.RingReply, Text: "partial reply", Truncated: true},
		{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{
			Calls: 2,
			Tools: []wslive.ToolTally{{Name: "read_file", Calls: 2}},
			Batches: []wslive.ToolBatch{{
				Calls:    2,
				CallArgs: []wslive.ToolCallArgs{{Name: "read_file", ArgsPreview: "a.go"}},
			}},
		}},
		{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: "id:worker1", Title: "done", Text: "all green"},
		{Kind: wslive.RingWake, Cause: "mail", Source: "id:worker1", Caption: "report"},
		{Kind: wslive.RingCommand, Command: "/status", Text: "ok"},
		{Kind: wslive.RingTurn, Done: 1},
		{Kind: wslive.RingUI, UI: &wslive.UICommand{}},
		{Kind: wslive.RingCard, Card: &wslive.CardRef{}},
		{Kind: wslive.RingWhiteboardEffects, Text: "wb"},
	}

	res := BuildRingDialog(entries, "ring", "secret-token-12345")
	if strings.Contains(res.Dialog, "before compact") {
		t.Fatalf("dialog should slice strictly after newest successful RingCompact:\n%s", res.Dialog)
	}
	if res.Ticket != "261124-feat-ws-managed-compaction" {
		t.Fatalf("ticket = %q, want 261124-feat-ws-managed-compaction", res.Ticket)
	}
	if res.Playbook != "lead-run" {
		t.Fatalf("playbook = %q, want lead-run", res.Playbook)
	}
	for _, want := range []string{
		"user: <skill name=\"ws:lead-run\">",
		"assistant: partial reply [truncated]",
		"tools: read_file(a.go)",
		"mail: recv id:worker1: done: all green",
		"wake: mail · id:worker1 · report",
		"command: /status\nok",
	} {
		if !strings.Contains(res.Dialog, want) {
			t.Fatalf("missing %q in dialog:\n%s", want, res.Dialog)
		}
	}
	if strings.Contains(res.Dialog, "failed attempt") || strings.Contains(res.Dialog, "wb") {
		t.Fatalf("UI/lifecycle rows leaked into dialog:\n%s", res.Dialog)
	}
}

func TestBuildRingDialogTailTruncationAndScrub(t *testing.T) {
	secret := "super-secret-value-999"
	var entries []wslive.RingEntry
	for i := 0; i < 60; i++ {
		entries = append(entries, wslive.RingEntry{
			Kind: wslive.RingUser,
			Text: strings.Repeat("a", 1024) + " " + secret,
		})
	}
	res := BuildRingDialog(entries, "ring", secret)
	if len(res.Dialog) > DialogBytes+256 {
		t.Fatalf("dialog exceeded budget: %d bytes", len(res.Dialog))
	}
	if strings.Contains(res.Dialog, secret) {
		t.Fatalf("secret leaked into dialog")
	}
}
