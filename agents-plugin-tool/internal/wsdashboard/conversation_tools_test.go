package wsdashboard

import (
	stdhtml "html"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
)

func toolsSummary() *wslive.ToolSummary {
	return &wslive.ToolSummary{
		Calls: 11, Failed: 1, DurationMs: 75_000,
		Tools:      []wslive.ToolTally{{Name: "Bash", Calls: 5}, {Name: "mcp__plugin_ws_ws__whiteboard.update", Calls: 2}},
		OtherCalls: 4,
		Batches: []wslive.ToolBatch{
			{Names: []string{"Bash"}, Calls: 5},
			{Names: []string{"Read", "mcp__plugin_ws_ws__whiteboard.update"}, Calls: 6, Failed: 1},
		},
		BatchesOmitted: 3,
	}
}

// TestToolsEntryRendersACollapsedSummaryRow pins the collapsed turn tool
// summary row: one collapsed details row with the shared data-conv-turn-*
// DOM contract (call count, failure badge, elapsed time, and drawer batches
// with stripped tool names and calm category dots). Open state is preserved
// across region swaps by data-key.
func TestToolsEntryRendersACollapsedSummaryRow(t *testing.T) {
	ring := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginTerminal, Text: "go"}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingTools, Tools: toolsSummary()}),
		ringRecord(3, wslive.RingEntry{Kind: wslive.RingReply, Text: "done"}),
	}
	set, err := renderSessionRegions(historyView(ring, "", PageContent{}, sessionLookup{}), newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	html := string(set[regionEntries].HTML)
	unescaped := stdhtml.UnescapeString(html)
	name := ring[1].Name
	for _, want := range []string{
		`class="toolsline d-turn d-turn-has-fail" data-conv-turn="1" data-entry="` + name + `" data-key="turn-` + name + `"`,
		`data-at="2026-11-06T12:00:02Z"`,
		`<summary class="conv-row d-turn-sum" data-conv-turn-summary aria-expanded="false">`,
		`<span class="d-turn-count" data-conv-turn-count>11 tool calls</span>`,
		`<span class="d-turn-dur" data-span="75000">1m 15s</span>`,
		`<span class="d-turn-fails" data-conv-turn-fails><span class="d-turn-dot" aria-hidden="true">·</span><b>1 failed</b></span>`,
		`<span class="row-chevron" aria-hidden="true">`,
		`<div class="d-turn-drawer" data-conv-turn-drawer>`,
		`<li class="brow done">`,
		`<li class="brow fail">`,
		`data-cat="exec"`,
		`data-cat="file"`,
		`whiteboard.update</span>`,
		`<span title="6 calls">×6</span> · <span class="bf" title="1 failed">✗1</span>`,
		`+3 more batches`,
	} {
		if !strings.Contains(unescaped, want) {
			t.Errorf("tools row lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, `data-conv-turn="1" open`) || strings.Contains(html, "<details class=\"toolsline\" open") || strings.Contains(html, "mcp__plugin_ws_ws__whiteboard.update</span>") {
		t.Errorf("tools row is open by default or shows an unstripped tool name:\n%s", html)
	}
	if strings.Index(html, `data-entry="`+ring[0].Name) > strings.Index(html, `data-entry="`+name) || strings.Index(html, `data-entry="`+name) > strings.Index(html, `data-entry="`+ring[2].Name) {
		t.Errorf("tools row is not between the user message and the reply:\n%s", html)
	}
}

func TestWhiteboardEffectsDoNotReplaceLegacyToolSummaries(t *testing.T) {
	view := historyView([]wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingTools, Tools: toolsSummary()}),
		ringRecord(2, dashboardEffect("committed payload")),
		ringRecord(3, wslive.RingEntry{Kind: wslive.RingCommand, Command: "/legacy", Text: "old command output"}),
	}, "", PageContent{}, sessionLookup{})
	regions, err := renderSessionRegions(view, nil)
	if err != nil {
		t.Fatal(err)
	}
	html := string(regions[regionEntries].HTML)
	if strings.Count(html, `data-conv-turn="1"`) != 1 || strings.Count(html, `<article class="wbeffects wbupdate"`) != 1 || !strings.Contains(html, "whiteboard.update") || !strings.Contains(html, "Saved panel summary") || !strings.Contains(html, "old command output") {
		t.Fatalf("effects replaced/misclassified legacy history: %s", html)
	}
	// Whiteboard update card stays outside the collapsed tool drawer.
	drawerEnd := strings.Index(html, `</details>`)
	wbPos := strings.Index(html, `<article class="wbeffects wbupdate"`)
	if drawerEnd < 0 || wbPos < drawerEnd {
		t.Fatalf("whiteboard card was absorbed into tool drawer:\n%s", html)
	}
	// Conversation cards expose committed panel recaps, not panel payloads.
	if strings.Contains(html, "committed payload") {
		t.Fatal("panel payload escaped its board-only surface")
	}
}

// TestToolsRowWithoutNamedToolsShowsTheCallTotal pins the summary when no
// listed name accounts for any call.
func TestToolsRowWithoutNamedToolsShowsTheCallTotal(t *testing.T) {
	ring := []wslive.RingRecord{ringRecord(1, wslive.RingEntry{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{Calls: 2, OtherCalls: 2, Batches: []wslive.ToolBatch{{Calls: 2}}}})}
	set, err := renderSessionRegions(historyView(ring, "", PageContent{}, sessionLookup{}), newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	if html := string(set[regionEntries].HTML); !strings.Contains(html, `<span class="d-turn-count" data-conv-turn-count>2 tool calls</span>`) {
		t.Errorf("summary = %s", html)
	}
}

// TestOpenGapShowsALiveToolsRow pins the live turn summary header: the
// snapshot's open tool gap renders at the head of the convlive region using
// the shared turnsummary component while a turn is active, and a snapshot
// without one renders no row.
func TestOpenGapShowsALiveToolsRow(t *testing.T) {
	view := func(gap *wslive.ToolSummary) string {
		lead := wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseTool}, ToolGap: gap}
		v := buildSessionView(pageInputs{Session: liveSession("self", "/proj", testNow), Snapshot: liveSnap(testNow, lead), Now: testNow})
		set, err := renderSessionRegions(v, newEntryCache())
		if err != nil {
			t.Fatal(err)
		}
		return string(set[regionConvLive].HTML)
	}
	live := view(toolsSummary())
	for _, want := range []string{
		`class="toolsline d-turn live d-turn-live d-turn-has-fail" data-conv-turn="1" data-key="turn-live"`,
		`<span class="d-turn-count" data-conv-turn-count>11 tool calls</span>`,
		`<li class="brow fail">`,
		`Bash</span>`,
	} {
		if !strings.Contains(live, want) {
			t.Errorf("live row lacks %q:\n%s", want, live)
		}
	}
	if strings.Contains(live, `data-entry=`) {
		t.Errorf("the live row carries a ring entry key:\n%s", live)
	}
	if none := view(nil); strings.Contains(none, "toolsline") {
		t.Errorf("a snapshot without an open gap rendered a tools row:\n%s", none)
	}
}

// TestExactToolsRowShowsTokensDurationsAndRepeats pins the per-gap drawer
// detail: per-gap .ttok stays inside [data-conv-turn-drawer] (never a second
// token anchor), each batch shows its tools as chips with xN for repeats, and
// trailing numbers show duration and failures only (no per-call token delta).
func TestExactToolsRowShowsTokensDurationsAndRepeats(t *testing.T) {
	summary := &wslive.ToolSummary{
		Calls: 6, Failed: 1, DurationMs: 4_000,
		Tools: []wslive.ToolTally{{Name: "Read", Calls: 3}, {Name: "Bash", Calls: 2}, {Name: "Grep", Calls: 1}},
		Batches: []wslive.ToolBatch{
			{Names: []string{"Read", "Bash"}, Tools: []wslive.ToolTally{{Name: "Read", Calls: 3}, {Name: "Bash", Calls: 1}}, Calls: 4, Failed: 1, DurationMs: 1_234,
				Usage: &wslive.TokenUsage{Input: 12, Output: 1_100, CacheRead: 210_400, CacheWrite: 4_200}},
			{Names: []string{"Bash", "Grep"}, Calls: 2, DurationMs: 40},
		},
		Usage: &wslive.TokenUsage{Input: 12_300, Output: 1_100, CacheRead: 210_400},
	}
	ring := []wslive.RingRecord{ringRecord(1, wslive.RingEntry{Kind: wslive.RingTools, Tools: summary})}
	set, err := renderSessionRegions(historyView(ring, "", PageContent{}, sessionLookup{}), newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	html := string(set[regionEntries].HTML)
	for _, want := range []string{
		`<div class="d-turn-gap-meta"><span class="ttok" title="2 model calls · prompt 222,700 (12,300 input · 210,400 cache read · 0 cache write) · output 1,100">2 calls · prompt 223k · output 1.1k</span></div>`,
		`Read<b class="x">×3</b></span>`,
		`<span class="tool" title="Bash">Bash</span>`,
		`<span class="bm"><span title="duration 1,234 ms">1.2s</span> · <span class="bf" title="1 failed">✗1</span></span>`,
		`<span class="bm"><span title="2 calls">×2</span> · <span title="duration 40 ms">&lt;0.1s</span></span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("exact tools row lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, `>1 call`) || strings.Contains(html, `4 calls`) {
		t.Errorf("an exact batch line still shows a bare call count:\n%s", html)
	}
}

// TestBatchRowsShowArgs pins the argument previews on batch rows inside
// [data-conv-turn-drawer]: a one-call batch shows its call's preview right
// after the tool name, a multi-call batch lists every call's name and preview
// in its title, and a batch without previews renders cleanly.
func TestBatchRowsShowArgs(t *testing.T) {
	long := strings.Repeat("z", 190)
	summary := &wslive.ToolSummary{
		Calls: 6,
		Batches: []wslive.ToolBatch{
			{Names: []string{"Read"}, Tools: []wslive.ToolTally{{Name: "Read", Calls: 1}}, Calls: 1, DurationMs: 600,
				CallArgs: []wslive.ToolCallArgs{{Name: "Read", ArgsPreview: "file_path: /a/" + long}}},
			{Names: []string{"Bash", "mcp__plugin_ws_ws__note_write"}, Tools: []wslive.ToolTally{{Name: "Bash", Calls: 2}, {Name: "mcp__plugin_ws_ws__note_write", Calls: 1}}, Calls: 3,
				CallArgs: []wslive.ToolCallArgs{{Name: "Bash", ArgsPreview: "command: ls"}, {Name: "Bash"}, {Name: "mcp__plugin_ws_ws__note_write", ArgsPreview: "layer: repo"}}},
			{Names: []string{"Grep"}, Tools: []wslive.ToolTally{{Name: "Grep", Calls: 2}}, Calls: 2},
		},
	}
	ring := []wslive.RingRecord{ringRecord(1, wslive.RingEntry{Kind: wslive.RingTools, Tools: summary})}
	set, err := renderSessionRegions(historyView(ring, "", PageContent{}, sessionLookup{}), newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	html := string(set[regionEntries].HTML)
	for _, want := range []string{
		`<span class="bp one"><span class="tool" title="Read">Read</span><span class="ba" title="file_path: /a/` + long + `">file_path: /a/` + long + `</span></span>`,
		"<span class=\"bp\" title=\"Bash command: ls\nBash\nnote_write layer: repo\">" + `<span class="tool">Bash<b class="x">×2</b></span><span class="tool">note_write</span></span>`,
		`<span class="bp"><span class="tool" title="Grep">Grep<b class="x">×2</b></span></span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("args rows lack %q:\n%s", want, html)
		}
	}
	if strings.Count(html, `class="ba"`) != 1 {
		t.Errorf("only the one-call batch shows args inline:\n%s", html)
	}
}

func TestCompactTokensAndBatchSpans(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 950: "950", 1_000: "1.0k", 12_345: "12.3k", 99_949: "99.9k", 99_950: "100k", 210_400: "210k", 999_499: "999k", 999_500: "1.0M", 1_400_000: "1.4M"} {
		if got := compactTokens(n); got != want {
			t.Errorf("compactTokens(%d) = %q, want %q", n, got, want)
		}
	}
	for ms, want := range map[int64]string{50: "<0.1s", 420: "0.4s", 9_960: "10.0s", 12_000: "12s", 75_000: "1m 15s"} {
		if got := batchSpanText(ms); got != want {
			t.Errorf("batchSpanText(%d) = %q, want %q", ms, got, want)
		}
	}
}

// TestToolsSummaryHidesZeroFailureBadge pins that [data-conv-turn-fails] is
// present in the DOM for stable selectors and hidden when Failed == 0, while
// visible when Failed > 0.
func TestToolsSummarySplitsCountsFromNumbers(t *testing.T) {
	render := func(sum *wslive.ToolSummary) string {
		ring := []wslive.RingRecord{ringRecord(1, wslive.RingEntry{Kind: wslive.RingTools, Tools: sum})}
		set, err := renderSessionRegions(historyView(ring, "", PageContent{}, sessionLookup{}), newEntryCache())
		if err != nil {
			t.Fatal(err)
		}
		return stdhtml.UnescapeString(string(set[regionEntries].HTML))
	}
	withFail := render(toolsSummary())
	if !strings.Contains(withFail, `<span class="d-turn-fails" data-conv-turn-fails><span class="d-turn-dot" aria-hidden="true">·</span><b>1 failed</b></span>`) {
		t.Errorf("expected visible failure badge:\n%s", withFail)
	}
	noFail := render(&wslive.ToolSummary{Calls: 2, OtherCalls: 2})
	if !strings.Contains(noFail, `<span class="d-turn-fails" data-conv-turn-fails hidden>`) {
		t.Errorf("expected hidden failure badge when Failed == 0:\n%s", noFail)
	}
}

func TestAuditCallRowsAndLegacyFallback(t *testing.T) {
	// With no call IDs, RingTools renders inside the collapsed turn summary
	// drawer using batch rows (no au-calls).
	legacy := []wslive.RingRecord{ringRecord(1, wslive.RingEntry{Kind: wslive.RingTools, Tools: toolsSummary()})}
	legacySet, err := renderSessionRegions(historyView(legacy, "", PageContent{}, sessionLookup{}), newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	legacyHTML := string(legacySet[regionEntries].HTML)
	if !strings.Contains(legacyHTML, `data-conv-turn="1"`) || strings.Contains(legacyHTML, `au-calls`) {
		t.Errorf("legacy tools without call IDs did not use batch drawer fallback:\n%s", legacyHTML)
	}

	// With call IDs, calls render inside [data-conv-turn-drawer] as audit call
	// rows with data-call IDs and calm category dots; non-ID'd rows are static.
	summary := &wslive.ToolSummary{
		Calls:      5,
		DurationMs: 1_250,
		Batches: []wslive.ToolBatch{
			{
				Names: []string{"Read", "Grep", "Bash", "Edit"},
				Calls: 4,
				CallArgs: []wslive.ToolCallArgs{
					{Name: "Read", ArgsPreview: "file_path: a.go", ID: "c1"},
					{Name: "Grep", ArgsPreview: "pattern: foo", ID: "c2"},
					{Name: "Bash", ArgsPreview: "cmd: go test", ID: "c3"},
					{Name: "Edit", ArgsPreview: "file_path: b.go", ID: "c4"},
				},
			},
			{
				Names: []string{"Write"},
				Calls: 1,
				CallArgs: []wslive.ToolCallArgs{
					{Name: "Write", ArgsPreview: "file_path: c.go"}, // uncapped / no ID
				},
			},
		},
	}
	rec := ringRecord(2, wslive.RingEntry{Kind: wslive.RingTools, Tools: summary})
	set, err := renderSessionRegions(historyView([]wslive.RingRecord{rec}, "", PageContent{}, sessionLookup{}), newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	html := stdhtml.UnescapeString(string(set[regionEntries].HTML))
	for _, want := range []string{
		`data-conv-turn="1"`,
		`data-at="` + rec.At.UTC().Format("2006-01-02T15:04:05")[:10],
		`<div class="d-turn-drawer" data-conv-turn-drawer>`,
		`data-call="c1"`,
		`data-call="c2"`,
		`data-call="c3"`,
		`data-call="c4"`,
		`<ol class="au-calls">`,
		`<summary class="conv-row au-row au-done au-static">`,
		`data-cat="file"`,
		`data-cat="exec"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("audit call rows lack %q:\n%s", want, html)
		}
	}
	for _, gone := range []string{`au-thead`, `au-more`, `au-fold`, `+2 more`} {
		if strings.Contains(html, gone) {
			t.Errorf("audit call rows kept the group frame shell %q:\n%s", gone, html)
		}
	}
}

func TestActivityRowPolishIconsAndContextTokens(t *testing.T) {
	// Verify estContextTokens / estContextText / estContextTitle helper parity.
	if got := estContextTokens(0, 0); got != 0 {
		t.Fatalf("estContextTokens(0,0) = %d", got)
	}
	if got := estContextText(1, 0); got != "+1" {
		t.Fatalf("estContextText(1,0) = %q", got)
	}
	if got := estContextText(100, 68); got != "+42" {
		t.Fatalf("estContextText(100,68) = %q", got)
	}
	if got := estContextText(2048, 5325); got != "+1.8k" {
		t.Fatalf("estContextText(2048,5325) = %q", got)
	}
	if got := estContextTitle(2048, 5325); got != "~1.8k tok (in 2.0 KB + out 5.2 KB)" {
		t.Fatalf("estContextTitle = %q", got)
	}

	// Single call rows with partial batch failure + collapsed batch summary:
	// drawer rows keep calm category dots, per-tool duration, and per-call
	// estimated token deltas (.au-tok), while the single turn header shows #i-x and ~+Nk tok.
	summary := &wslive.ToolSummary{
		Calls:      5,
		Failed:     2,
		DurationMs: 2_000,
		InBytes:    3048,
		OutBytes:   8125,
		Batches: []wslive.ToolBatch{
			{
				Names:      []string{"Read"},
				Calls:      1,
				DurationMs: 420,
				InBytes:    2048,
				OutBytes:   5325,
				CallArgs: []wslive.ToolCallArgs{
					{Name: "Read", ArgsPreview: "a.go", ID: "c1", Failed: false, InBytes: 2048, OutBytes: 5325},
				},
			},
			{
				Names:    []string{"Bash"},
				Calls:    1,
				Failed:   1,
				InBytes:  200,
				OutBytes: 800,
				CallArgs: []wslive.ToolCallArgs{
					{Name: "Bash", ArgsPreview: "go test", ID: "c2", Failed: true, InBytes: 200, OutBytes: 800},
				},
			},
			{
				Names:    []string{"Edit"},
				Calls:    3,
				Failed:   1,
				InBytes:  800,
				OutBytes: 2000,
			},
		},
	}
	rec := ringRecord(2, wslive.RingEntry{Kind: wslive.RingTools, Tools: summary})
	set, err := renderSessionRegions(historyView([]wslive.RingRecord{rec}, "", PageContent{}, sessionLookup{}), newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	html := stdhtml.UnescapeString(string(set[regionEntries].HTML))
	if strings.Contains(html, "#i-hammer") {
		t.Errorf("turn summary must not render hammer icon:\n%s", html)
	}
	for _, want := range []string{
		`<span class="row-icon d-turn-ico" aria-hidden="true"><svg class="i" aria-hidden="true"><use href="#i-x"/></svg></span>`,
		`data-conv-turn-tokens data-estimated="1"`,
		`~+2.8k tok</span>`,
		`class="conv-row au-row au-done" data-call="c1"><span class="row-icon au-mk"><span class="d-turn-cat" data-cat="file" aria-hidden="true"></span></span>`,
		`class="conv-row au-row au-fail" data-call="c2"><span class="row-icon au-mk"><span class="d-turn-cat" data-cat="exec" aria-hidden="true"></span></span>`,
		`class="conv-row au-row au-fail au-static"><span class="row-icon au-mk"><span class="d-turn-cat" data-cat="file" aria-hidden="true"></span></span>`,
		`<span class="au-dur" title="duration 420 ms">0.4s</span>`,
		`<span class="au-tok" title="~1.8k tok (in 2.0 KB + out 5.2 KB)">+1.8k</span>`,
		`<span class="au-tok" title="~250 tok (in 200 B + out 800 B)">+250</span>`,
		`<span class="au-tok" title="~700 tok (in 800 B + out 2.0 KB)">+700</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered turn summary missing %q:\n%s", want, html)
		}
	}

	// Separate turns with closing RingTurn markers use #i-x when Failed > 0
	// and #i-check when Failed == 0.
	legacyFail := &wslive.ToolSummary{
		Calls:    2,
		Failed:   1,
		InBytes:  2048,
		OutBytes: 5325,
		Tools:    []wslive.ToolTally{{Name: "Bash", Calls: 2}},
		Batches:  []wslive.ToolBatch{{Names: []string{"Bash"}, Calls: 2, Failed: 1, InBytes: 2048, OutBytes: 5325}},
	}
	legacyPass := &wslive.ToolSummary{
		Calls:    1,
		InBytes:  400,
		OutBytes: 800,
		Tools:    []wslive.ToolTally{{Name: "Read", Calls: 1}},
		Batches:  []wslive.ToolBatch{{Names: []string{"Read"}, Calls: 1, InBytes: 400, OutBytes: 800}},
	}
	lset, err := renderSessionRegions(historyView([]wslive.RingRecord{
		ringRecord(3, wslive.RingEntry{Kind: wslive.RingTools, Tools: legacyFail}),
		ringRecord(4, wslive.RingEntry{Kind: wslive.RingTurn, Done: 1, Failed: 1, DurationMs: 2_000}),
		ringRecord(5, wslive.RingEntry{Kind: wslive.RingTools, Tools: legacyPass}),
		ringRecord(6, wslive.RingEntry{Kind: wslive.RingTurn, Done: 1, DurationMs: 500}),
	}, "", PageContent{}, sessionLookup{}), newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	lhtml := stdhtml.UnescapeString(string(lset[regionEntries].HTML))
	if strings.Contains(lhtml, "#i-hammer") {
		t.Errorf("toolsline must not render hammer icon:\n%s", lhtml)
	}
	for _, want := range []string{
		`<span class="row-icon d-turn-ico" aria-hidden="true"><svg class="i" aria-hidden="true"><use href="#i-x"/></svg></span>`,
		`<span class="row-icon d-turn-ico" aria-hidden="true"><svg class="i" aria-hidden="true"><use href="#i-check"/></svg></span>`,
		`~+1.8k tok</span>`,
		`~+300 tok</span>`,
	} {
		if !strings.Contains(lhtml, want) {
			t.Errorf("rendered turn summary missing %q:\n%s", want, lhtml)
		}
	}
}
