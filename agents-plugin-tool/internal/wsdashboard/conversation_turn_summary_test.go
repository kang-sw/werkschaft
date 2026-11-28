package wsdashboard

import (
	"bytes"
	stdhtml "html"
	"strconv"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

func renderIdleHistory(t *testing.T, ring []wslive.RingRecord, lang string) regionSet {
	t.Helper()
	now := testNow
	lead := wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}}
	v := buildSessionView(pageInputs{
		Session:  liveSession("self", "/proj", now),
		Snapshot: liveSnap(now, lead),
		Ring:     ring,
		Now:      now,
	})
	if lang != "" {
		v.Lang = lang
	}
	set, err := renderSessionRegions(v, newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestTurnSummaryMultiGapCompletedTurnAndCrossTrackIsolation(t *testing.T) {
	ring := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "Investigate"}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{
			Calls: 3, DurationMs: 4_000, InBytes: 1_200, OutBytes: 2_800,
			Batches: []wslive.ToolBatch{
				{Names: []string{"Read", "mcp__plugin_ws_ws__tickets_query"}, Calls: 3, DurationMs: 4_000,
					CallArgs: []wslive.ToolCallArgs{
						{Name: "Read", ArgsPreview: "a.go", ID: "c1"},
						{Name: "mcp__plugin_ws_ws__tickets_query", ArgsPreview: "status: ready", ID: "c2"},
						{Name: "mcp__plugin_ws_ws__git_status", ArgsPreview: "", ID: "c3"},
					}},
			},
			Usage: &wslive.TokenUsage{Input: 1_000, Output: 200},
		}}),
		ringRecord(3, wslive.RingEntry{Kind: wslive.RingReply, Text: "First interim reply"}),
		ringRecord(4, dashboardEffect("board recap")),
		ringRecord(5, wslive.RingEntry{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: "worker-1", Title: "hello"}),
		ringRecord(6, wslive.RingEntry{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{
			Calls: 4, Failed: 1, DurationMs: 7_400, InBytes: 800, OutBytes: 1_600,
			Batches: []wslive.ToolBatch{
				{Names: []string{"Bash", "mcp__plugin_ws_ws__agent_spawn"}, Calls: 4, Failed: 1, DurationMs: 7_400,
					CallArgs: []wslive.ToolCallArgs{
						{Name: "Bash", ArgsPreview: "go test ./...", ID: "c4", Failed: true},
						{Name: "mcp__plugin_ws_ws__agent_spawn", ArgsPreview: "reviewer", ID: "c5"},
					}},
			},
		}}),
		ringRecord(7, wslive.RingEntry{Kind: wslive.RingReply, Text: "Final answer"}),
		ringRecord(8, wslive.RingEntry{
			Kind:        wslive.RingTurn,
			Origin:      wslive.OriginWhiteboard,
			Done:        6,
			Failed:      1,
			DurationMs:  11_400,
			AddedTokens: 2_800,
			Reason:      "completed",
		}),
	}
	set := renderIdleHistory(t, ring, "en")
	entries := stdhtml.UnescapeString(string(set[regionEntries].HTML))
	live := string(set[regionConvLive].HTML)

	// Idle session must never emit a live turn summary header.
	if strings.Contains(live, "data-conv-turn") {
		t.Fatalf("idle session emitted a live turn summary:\n%s", live)
	}

	// Strictly one collapsed turn summary and one [data-conv-turn-tokens] anchor.
	if got := strings.Count(entries, `data-conv-turn="1"`); got != 1 {
		t.Fatalf("expected 1 turn summary, got %d:\n%s", got, entries)
	}
	if got := strings.Count(entries, `data-conv-turn-tokens`); got != 1 {
		t.Fatalf("expected 1 [data-conv-turn-tokens] anchor, got %d:\n%s", got, entries)
	}

	// Anchored at first RingTools (record 2), carries turn-end timestamp on data-turn-at,
	// uses RingTurn counts/duration/AddedTokens, and hides RingTools #2 (record 6) & RingTurn (record 8).
	for _, want := range []string{
		`data-entry="` + ring[1].Name + `" data-key="turn-` + ring[1].Name + `"`,
		`data-turn-at="2026-11-06T12:00:08Z"`,
		`data-turn-failed="1"`,
		`<summary class="conv-row d-turn-sum" data-conv-turn-summary aria-expanded="false">`,
		`<span class="d-turn-count" data-conv-turn-count>7 tool calls</span>`,
		`<span class="d-turn-mails" data-conv-turn-mails>1 mail</span>`,
		`<span class="d-turn-dur" data-span="11400">11s</span>`,
		`<span class="d-turn-fails" data-conv-turn-fails><span class="d-turn-dot" aria-hidden="true">·</span><b>1 failed</b></span>`,
		`<span class="d-turn-reason">completed</span>`,
		`data-conv-turn-tokens title="+2,800 context tokens added in this turn">+2.8k tok</span>`,
		`data-entry="` + ring[4].Name + `" data-turn-absorbed="1"`,
		`data-entry="` + ring[5].Name + `" data-turn-absorbed="1"`,
		`data-entry="` + ring[7].Name + `" data-turn-absorbed="1"`,
		`data-call="c1"`,
		`data-call="c2"`,
		`data-call="c3"`,
		`data-call="c4"`,
		`data-call="c5"`,
		`class="au-tok"`,
		`data-cat="file"`,
		`data-cat="tickets"`,
		`data-cat="git"`,
		`data-cat="exec"`,
		`data-cat="agent"`,
		`<div class="d-turn-gap-meta"><span class="ttok"`,
	} {
		if !strings.Contains(entries, want) {
			t.Errorf("multi-gap turn summary missing %q:\n%s", want, entries)
		}
	}
	if strings.Contains(entries, `data-estimated="1"`) {
		t.Errorf("exact AddedTokens must not be marked estimated:\n%s", entries)
	}

	// User bubble precedes summary; mail is folded inside the drawer in chronological
	// order between c3 and c4, while interim reply, whiteboard update, and final
	// reply stay outside the drawer.
	userPos := strings.Index(entries, `data-entry="`+ring[0].Name)
	summaryPos := strings.Index(entries, `data-conv-turn="1"`)
	c3Pos := strings.Index(entries, `data-call="c3"`)
	mailPos := strings.Index(entries, `class="d-turn-gap d-turn-mail-gap"`)
	c4Pos := strings.Index(entries, `data-call="c4"`)
	reply1Pos := strings.Index(entries, `First interim reply`)
	wbPos := strings.Index(entries, `class="wbeffects wbupdate"`)
	reply2Pos := strings.Index(entries, `Final answer`)
	if !(userPos >= 0 && userPos < summaryPos && summaryPos < c3Pos && c3Pos < mailPos && mailPos < c4Pos && c4Pos < reply1Pos && reply1Pos < wbPos && wbPos < reply2Pos) {
		t.Errorf("unexpected cross-track/drawer order: user=%d summary=%d c3=%d mail=%d c4=%d reply1=%d wb=%d reply2=%d", userPos, summaryPos, c3Pos, mailPos, c4Pos, reply1Pos, wbPos, reply2Pos)
	}
}

func TestTurnSummaryActiveLiveHeaderAndIdleSuppression(t *testing.T) {
	now := testNow
	flushedGap := &wslive.ToolSummary{
		Calls: 2, DurationMs: 1_500, InBytes: 400, OutBytes: 800,
		Batches: []wslive.ToolBatch{{Names: []string{"Read"}, Calls: 2, DurationMs: 1_500}},
	}
	openGap := &wslive.ToolSummary{
		Calls: 3, Failed: 1, DurationMs: 2_500, InBytes: 600, OutBytes: 1_200,
		Batches: []wslive.ToolBatch{{Names: []string{"Bash"}, Calls: 3, Failed: 1, DurationMs: 2_500}},
	}
	ring := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginTerminal, Text: "run"}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingTools, Tools: flushedGap}),
		ringRecord(3, wslive.RingEntry{Kind: wslive.RingReply, Text: "Working on it"}),
	}

	// Active turn: single live header in r-convlive aggregates flushed + open gap + TurnAddedTokens,
	// while flushed RingTools in r-entries is hidden via data-turn-live-absorbed="1".
	activeLead := wslive.Lead{
		Activity:        wslive.Activity{Phase: wslive.PhaseTool},
		TurnStartedAt:   now.UnixMilli() - 4_000,
		TurnAddedTokens: 1_900,
		ToolGap:         openGap,
	}
	activeView := buildSessionView(pageInputs{
		Session:  liveSession("self", "/proj", now),
		Snapshot: liveSnap(now, activeLead),
		Ring:     ring,
		Now:      now,
	})
	set, err := renderSessionRegions(activeView, newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	entries := stdhtml.UnescapeString(string(set[regionEntries].HTML))
	live := stdhtml.UnescapeString(string(set[regionConvLive].HTML))

	if strings.Contains(entries, `data-conv-turn="1"`) {
		t.Errorf("active open-span RingTools must not duplicate summary in r-entries:\n%s", entries)
	}
	if !strings.Contains(entries, `data-entry="`+ring[1].Name+`" data-turn-live-absorbed="1"`) {
		t.Errorf("open-span flushed RingTools missing data-turn-live-absorbed:\n%s", entries)
	}
	for _, want := range []string{
		`class="toolsline d-turn live d-turn-live d-turn-has-fail" data-conv-turn="1" data-key="turn-live"`,
		`<span class="d-turn-count" data-conv-turn-count>5 tool calls</span>`,
		// Live duration ticks from the turn start, not the 4,000ms sum of tool durations.
		`<time class="t d-turn-dur" data-ms="` + strconv.FormatInt(activeLead.TurnStartedAt, 10) + `" data-f="since">`,
		`<span class="d-turn-fails" data-conv-turn-fails><span class="d-turn-dot" aria-hidden="true">·</span><b>1 failed</b></span>`,
		`data-conv-turn-tokens title="+1,900 context tokens added in this turn">+1.9k tok</span>`,
	} {
		if !strings.Contains(live, want) {
			t.Errorf("live turn summary missing %q:\n%s", want, live)
		}
	}
	if got := strings.Count(live, `class="d-turn-gap"`); got != 2 {
		t.Errorf("live drawer should contain 2 gaps (flushed + open), got %d:\n%s", got, live)
	}

	// Idle session even with stale ToolGap / TurnStartedAt MUST NOT render live header,
	// and falls back to gap-derived orphan summary in r-entries with ~ estimate.
	idleLead := wslive.Lead{
		Activity:        wslive.Activity{Phase: wslive.PhaseIdle},
		TurnStartedAt:   now.UnixMilli() - 4_000,
		TurnAddedTokens: 1_900,
		ToolGap:         openGap,
	}
	idleView := buildSessionView(pageInputs{
		Session:  liveSession("self", "/proj", now),
		Snapshot: liveSnap(now, idleLead),
		Ring:     ring,
		Now:      now,
	})
	idleSet, err := renderSessionRegions(idleView, newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(idleSet[regionConvLive].HTML), `data-conv-turn`) {
		t.Errorf("idle session must never render live header:\n%s", string(idleSet[regionConvLive].HTML))
	}
	idleEntries := stdhtml.UnescapeString(string(idleSet[regionEntries].HTML))
	for _, want := range []string{
		`data-conv-turn="1"`,
		`<span class="d-turn-count" data-conv-turn-count>2 tool calls</span>`,
		`data-conv-turn-tokens data-estimated="1"`,
		`~+303 tok</span>`, // (400 + 800 + len("Working on it")=13) / 4 = 303
	} {
		if !strings.Contains(idleEntries, want) {
			t.Errorf("idle orphan summary missing %q:\n%s", want, idleEntries)
		}
	}
}

func TestTurnSummaryWakeLabelZeroToolPreservationAndReasoningHost(t *testing.T) {
	ring := []wslive.RingRecord{
		// Turn 1: 0-tool turn keeps .turnline unchanged.
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginTerminal, Text: "hi"}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingReply, Text: "hello"}),
		ringRecord(3, wslive.RingEntry{Kind: wslive.RingTurn, Origin: wslive.OriginTerminal, Done: 0, DurationMs: 300}),
		// Turn 2: wake turn with tools + reasoning summary host preserved at turn-end.
		ringRecord(4, wslive.RingEntry{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{
			Calls: 1, DurationMs: 900, Batches: []wslive.ToolBatch{{Names: []string{"Read"}, Calls: 1, DurationMs: 900}},
		}}),
		ringRecord(5, wslive.RingEntry{
			Kind:       wslive.RingTurn,
			WakeTurn:   true,
			Done:       1,
			DurationMs: 900,
			Reason:     "mail",
			SummaryHistory: &wslive.SummaryHistory{
				V:      1,
				Commit: strings.Repeat("b", 64),
				Requests: []wslive.SummaryAttempt{{
					Request: 1, Attempt: 1, Provider: "openai", API: "openai-responses", Model: "gpt-5", Status: wslive.SummaryComplete,
					Parts: []wslive.SummaryPart{{
						Output: 0, Part: 0, Status: wslive.SummaryComplete,
						Text: "Checked mail.", ObservedBytes: 13, RetainedBytes: 13,
					}},
				}},
			},
		}),
	}
	set := renderIdleHistory(t, ring, "ko")
	entries := stdhtml.UnescapeString(string(set[regionEntries].HTML))

	// Turn 1 keeps .turnline unchanged.
	if strings.Count(entries, `class="turnline"`) != 1 {
		t.Errorf("expected 1 preserved .turnline for 0-tool turn:\n%s", entries)
	}
	// Turn 2 preserves wake label, reason, localized Korean tool count, and .turn-with-summary at turn-end without duplicating .turnline.
	for _, want := range []string{
		`data-wake-turn="1"`,
		`<span class="d-turn-wake" title="웨이크 턴">↳</span>`,
		`<span class="d-turn-count" data-conv-turn-count>도구 호출 1회</span>`,
		`<span class="d-turn-reason">mail</span>`,
		`<div class="turn-with-summary" data-entry="` + ring[4].Name + `"`,
		`Checked mail.`,
	} {
		if !strings.Contains(entries, want) {
			t.Errorf("wake/reasoning turn missing %q:\n%s", want, entries)
		}
	}
	// Zero bytes and zero AddedTokens omits [data-conv-turn-tokens] cleanly:
	// the tool turn has none. Turn 1's reply is foldable text, so only its
	// hidden plain summary (before the tool turn) estimates tokens.
	toolTurn := strings.Index(entries, `data-wake-turn="1"><summary`)
	if toolTurn < 0 || strings.Contains(entries[toolTurn:], `data-conv-turn-tokens`) {
		t.Errorf("zero AddedTokens and zero bytes should omit token span on the tool turn:\n%s", entries)
	}
	if got := strings.Count(entries[:toolTurn], `data-conv-turn-tokens`); got != 1 || !strings.Contains(entries[:toolTurn], `data-conv-turn-plain="1" hidden`) {
		t.Errorf("0-tool turn's hidden plain summary should carry the one token estimate, got %d", got)
	}
}

func TestNewFoldedPlainTurnSummaryReusableContract(t *testing.T) {
	turn := wslive.RingEntry{
		Kind:        wslive.RingTurn,
		Origin:      wslive.OriginWhiteboard,
		Done:        0,
		DurationMs:  1_200,
		AddedTokens: 0,
	}
	v := NewFoldedPlainTurnSummary("turn-plain-1", "plain-1", turn, testNow.UnixMilli(), "2026-11-06T12:00:05Z", 2_000, "Folded response")
	v.Lang = "en"
	if v.EstTokens != 500 || !v.TokenEstimated() || v.TokenText() != "~+500 tok" {
		t.Fatalf("unexpected folded plain estimate: est=%d text=%q", v.EstTokens, v.TokenText())
	}
	a, err := loadDashboardAssets()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := a.templateFor("en").ExecuteTemplate(&buf, "turnsummary", v); err != nil {
		t.Fatal(err)
	}
	html := stdhtml.UnescapeString(buf.String())
	for _, want := range []string{
		`data-conv-turn="1"`,
		`data-conv-turn-summary`,
		`data-conv-turn-drawer`,
		`<span class="d-turn-count" data-conv-turn-count>Folded response</span>`,
		`<span class="d-turn-fails" data-conv-turn-fails hidden>`,
		`data-conv-turn-tokens data-estimated="1"`,
		`~+500 tok</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("folded plain turnsummary missing %q:\n%s", want, html)
		}
	}
}

func TestTurnSummaryEntryCacheInvalidatesAcrossLifecycleAndFrameRender(t *testing.T) {
	now := testNow
	gap := &wslive.ToolSummary{
		Calls: 2, DurationMs: 1_000, InBytes: 800, OutBytes: 800,
		Batches: []wslive.ToolBatch{{Names: []string{"Bash"}, Calls: 2, DurationMs: 1_000}},
	}
	ringOpen := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingTools, Tools: gap}),
	}
	cache := newEntryCache()

	// 1. Active turn: record 1 cached as live-absorbed hidden span.
	vActive := buildSessionView(pageInputs{
		Session:  liveSession("self", "/proj", now),
		Snapshot: liveSnap(now, wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseTool}, TurnStartedAt: now.UnixMilli() - 1_000}),
		Ring:     ringOpen,
		Now:      now,
	})
	s1, err := renderSessionRegions(vActive, cache)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(s1[regionEntries].HTML), `data-turn-live-absorbed="1"`) {
		t.Fatalf("step 1 should be live-absorbed:\n%s", string(s1[regionEntries].HTML))
	}

	// 2. Turn finishes: same cache must invalidate record 1 and emit completed summary with +3.2k tok.
	ringDone := append(append([]wslive.RingRecord(nil), ringOpen...), ringRecord(2, wslive.RingEntry{
		Kind: wslive.RingTurn, Done: 2, DurationMs: 1_500, AddedTokens: 3_200,
	}))
	vDone := buildSessionView(pageInputs{
		Session:  liveSession("self", "/proj", now),
		Snapshot: liveSnap(now, wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}}),
		Ring:     ringDone,
		Now:      now,
	})
	s2, err := renderSessionRegions(vDone, cache)
	if err != nil {
		t.Fatal(err)
	}
	out2 := stdhtml.UnescapeString(string(s2[regionEntries].HTML))
	if !strings.Contains(out2, `data-conv-turn="1"`) || !strings.Contains(out2, `+3.2k tok</span>`) {
		t.Fatalf("step 2 cache did not invalidate to completed turn summary:\n%s", out2)
	}

	// Full ?frame=1 page render succeeds with turn summaries present.
	body, err := renderSessionPageData(sessionPageData{SessionView: vDone, Frame: true, regions: s2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `data-frame="1"`) || !strings.Contains(body, `data-conv-turn="1"`) {
		t.Fatalf("?frame=1 page missing expected turn summary markup")
	}
}

// A turn's summary is anchored at the first folded entry the window still
// holds, so a newest window or history page that truncates the turn's head
// re-anchors it at a later entry with smaller counts. Its identity must stay
// the closing turn entry (data-turn-key) across every such truncation so the
// page can keep exactly one node per turn.
func TestTurnSummaryTurnKeyStaysStableWhenWindowTruncatesTurnHead(t *testing.T) {
	ring := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: "w1", Title: "a"}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: "w2", Title: "b"}),
		ringRecord(3, wslive.RingEntry{Kind: wslive.RingMail, Direction: wslive.MailSent, Peer: "w1", Title: "c"}),
		ringRecord(4, wslive.RingEntry{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{
			Calls: 1, DurationMs: 500, InBytes: 400, OutBytes: 800,
			Batches: []wslive.ToolBatch{{Names: []string{"Read"}, Calls: 1, DurationMs: 500}},
		}}),
		ringRecord(5, wslive.RingEntry{Kind: wslive.RingTurn, Done: 1, DurationMs: 27_000, AddedTokens: 2_000, Reason: "completed"}),
	}
	turnKey := ring[4].Name
	for cut := 0; cut <= 3; cut++ {
		entries := stdhtml.UnescapeString(string(renderIdleHistory(t, ring[cut:], "en")[regionEntries].HTML))
		if got := strings.Count(entries, `data-turn-key="`+turnKey+`"`); got != 1 {
			t.Fatalf("cut=%d: want exactly one summary carrying data-turn-key=%q, got %d:\n%s", cut, turnKey, got, entries)
		}
		wantAnchor := `data-entry="` + ring[cut].Name + `" data-key="turn-` + ring[cut].Name + `" data-turn-key="` + turnKey + `"`
		if !strings.Contains(entries, wantAnchor) {
			t.Errorf("cut=%d: summary must anchor at first surviving folded entry with the stable turn key; want %q:\n%s", cut, wantAnchor, entries)
		}
		// The closing turn never renders a visible footer next to a summary.
		if strings.Contains(entries, `data-entry="`+turnKey+`" data-key="turn-`+turnKey+`"`) {
			t.Errorf("cut=%d: closing turn must be absorbed, not a visible footer:\n%s", cut, entries)
		}
	}
	// Only the closing turn left in the window: no summary exists to carry the
	// key (the page's mergeEntries keeps the turn hidden if it already shows one).
	tail := stdhtml.UnescapeString(string(renderIdleHistory(t, ring[4:], "en")[regionEntries].HTML))
	if strings.Contains(tail, `data-turn-key=`) {
		t.Errorf("a window holding only the closing turn has no folded entries to summarize:\n%s", tail)
	}
}

func TestTurnSummaryFoldedMailOnlyOpenBurstUnlinkedWakeAndBatchTokens(t *testing.T) {
	// 1. Completed mail-only turn (0 tools + 2 mails) in KO: renders "메일 2건"
	// and keeps exact AddedTokens without double-counting MailBytes().
	mailOnlyRing := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: "worker-1", Title: "first report", BodyBytes: 2_000}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingMail, Direction: wslive.MailSent, Peer: "worker-1", Title: "ack", BodyBytes: 1_200}),
		ringRecord(3, wslive.RingEntry{Kind: wslive.RingReply, Text: "All synced."}),
		ringRecord(4, wslive.RingEntry{Kind: wslive.RingTurn, WakeTurn: true, Done: 0, DurationMs: 1_800, AddedTokens: 900, Reason: "mail"}),
	}
	koEntries := stdhtml.UnescapeString(string(renderIdleHistory(t, mailOnlyRing, "ko")[regionEntries].HTML))
	for _, want := range []string{
		`data-entry="` + mailOnlyRing[0].Name + `" data-key="turn-` + mailOnlyRing[0].Name + `"`,
		`<span class="d-turn-count" data-conv-turn-count>메일 2건</span>`,
		`data-conv-turn-tokens title="이 턴에서 추가된 컨텍스트 토큰 +900">+900 tok</span>`,
		`data-entry="` + mailOnlyRing[1].Name + `" data-turn-absorbed="1"`,
		`data-entry="` + mailOnlyRing[3].Name + `" data-turn-absorbed="1"`,
		`class="d-turn-gap d-turn-mail-gap"`,
	} {
		if !strings.Contains(koEntries, want) {
			t.Errorf("mail-only KO turn missing %q:\n%s", want, koEntries)
		}
	}
	if strings.Contains(koEntries, `data-conv-turn-mails`) {
		t.Errorf("mail-only turn should render mail count in d-turn-count, not duplicate d-turn-mails:\n%s", koEntries)
	}
	if got := strings.Count(koEntries, `data-entry="`); got != len(mailOnlyRing) {
		t.Errorf("folded mail rows must not duplicate data-entry; got %d want %d:\n%s", got, len(mailOnlyRing), koEntries)
	}

	// 2. Trailing idle open-span multi-mail burst (RingMail + unlinked RingWake(mail))
	// folds into a collapsed .d-turn with "2 mails" in EN.
	burstRing := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingMail, Direction: wslive.MailSent, Peer: "worker-2", Title: "ping"}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingWake, Cause: "mail", Text: "[ws mailbox] 1 message(s) arrived"}),
	}
	burstEntries := stdhtml.UnescapeString(string(renderIdleHistory(t, burstRing, "en")[regionEntries].HTML))
	for _, want := range []string{
		`data-conv-turn="1"`,
		`<span class="d-turn-count" data-conv-turn-count>2 mails</span>`,
		`class="mail-notification turn-trigger"`,
		`class="mailline turn-trigger sent nobody"`,
		`data-entry="` + burstRing[1].Name + `" data-turn-absorbed="1"`,
	} {
		if !strings.Contains(burstEntries, want) {
			t.Errorf("trailing mail burst missing %q:\n%s", want, burstEntries)
		}
	}
	if got := strings.Count(burstEntries, `data-entry="`); got != len(burstRing) {
		t.Errorf("burst drawer must keep exact top-level data-entry count; got %d want %d", got, len(burstRing))
	}

	// 3. Active live turn with flushed mail + tools folds both into r-convlive.
	now := testNow
	activeRing := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: "peer-a", Title: "status"}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{
			Calls: 1, DurationMs: 500, InBytes: 400, OutBytes: 800,
			Batches: []wslive.ToolBatch{{Names: []string{"Read"}, Calls: 1, DurationMs: 500, InBytes: 400, OutBytes: 800}},
		}}),
	}
	activeView := buildSessionView(pageInputs{
		Session:  liveSession("self", "/proj", now),
		Snapshot: liveSnap(now, wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseTool}, TurnStartedAt: now.UnixMilli() - 1_000}),
		Ring:     activeRing,
		Now:      now,
	})
	liveSet, err := renderSessionRegions(activeView, newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	liveEntries := stdhtml.UnescapeString(string(liveSet[regionEntries].HTML))
	liveHTML := stdhtml.UnescapeString(string(liveSet[regionConvLive].HTML))
	if !strings.Contains(liveEntries, `data-entry="`+activeRing[0].Name+`" data-turn-live-absorbed="1"`) ||
		!strings.Contains(liveEntries, `data-entry="`+activeRing[1].Name+`" data-turn-live-absorbed="1"`) {
		t.Errorf("active turn should live-absorb both flushed mail and tools:\n%s", liveEntries)
	}
	for _, want := range []string{
		`<span class="d-turn-count" data-conv-turn-count>1 tool call</span>`,
		`<span class="d-turn-mails" data-conv-turn-mails>1 mail</span>`,
		`class="d-turn-gap d-turn-mail-gap"`,
		// Legacy batch row inside drawer restores byte-estimated token delta (btok).
		`<span class="btok"`,
	} {
		if !strings.Contains(liveHTML, want) {
			t.Errorf("active live turn with mail missing %q:\n%s", want, liveHTML)
		}
	}
}

// The whiteboard-only cluster aggregates folded rows from locale-independent
// attributes (never the localized chip text), so a completed turn must expose
// its call/mail/token/time stats and a standalone system mail must be marked
// while a peer mail must not.
func TestTurnSummaryClusterAttributesAreLocaleIndependent(t *testing.T) {
	ring := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "Investigate"}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{
			Calls: 3, DurationMs: 4_000,
			Batches: []wslive.ToolBatch{{Names: []string{"Read"}, Calls: 3, DurationMs: 4_000}},
		}}),
		ringRecord(3, wslive.RingEntry{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: "worker-1", Title: "hello"}),
		ringRecord(4, wslive.RingEntry{Kind: wslive.RingReply, Text: "Final answer"}),
		ringRecord(5, wslive.RingEntry{Kind: wslive.RingTurn, Origin: wslive.OriginWhiteboard, Done: 3, DurationMs: 5_000, AddedTokens: 2_800, Reason: "completed"}),
		ringRecord(6, wslive.RingEntry{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: wsmailbox.SystemLabel, Title: "Whiteboard lead nudge (hourly)."}),
		ringRecord(7, wslive.RingEntry{Kind: wslive.RingMail, Direction: wslive.MailRecv, Peer: "worker-2", Title: "peer note"}),
	}
	for _, lang := range []string{"en", "ko", "ja"} {
		entries := stdhtml.UnescapeString(string(renderIdleHistory(t, ring, lang)[regionEntries].HTML))
		wantTS := strconv.FormatInt(ring[4].At.UnixMilli(), 10)
		for _, want := range []string{
			`data-turn-calls="3"`,
			`data-turn-mails="1"`,
			`data-turn-tok="2800"`,
			`data-turn-ts="` + wantTS + `"`,
			`data-entry="` + ring[5].Name + `" data-mail-system="1" data-mail-ts="` + strconv.FormatInt(ring[5].At.UnixMilli(), 10) + `"`,
		} {
			if !strings.Contains(entries, want) {
				t.Errorf("lang %s: missing %q:\n%s", lang, want, entries)
			}
		}
		if strings.Contains(entries, `data-turn-tok-est`) {
			t.Errorf("lang %s: exact tokens must not be marked estimated", lang)
		}
		if got := strings.Count(entries, `data-mail-system="1"`); got != 1 {
			t.Errorf("lang %s: exactly the system mail is marked, got %d:\n%s", lang, got, entries)
		}
		if strings.Contains(entries, `data-entry="`+ring[6].Name+`" data-mail-system`) {
			t.Errorf("lang %s: a peer mail must never be marked as system mail", lang)
		}
	}
}
