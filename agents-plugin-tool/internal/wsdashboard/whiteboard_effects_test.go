package wsdashboard

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

func dashboardEffect(value string) wslive.RingEntry {
	entry, _ := wslive.NewWhiteboardEffects([]wswhiteboard.Effect{
		{Kind: "panel", Topic: "plan", EntryID: "e1", PanelID: "p1", Field: "payload", Before: "old <b>value</b>", After: value},
		{Kind: "panel", Topic: "plan", EntryID: "e1", PanelID: "p1", Field: "recap", After: "Saved panel summary"},
	})
	return entry
}

func TestWhiteboardEffectsPanelRecapOnlyAndStable(t *testing.T) {
	entry := dashboardEffect(`<script>alert(1)</script><img src=x onerror="attack()"> **not markdown**`)
	entry.Truncated = true
	ring := []wslive.RingRecord{ringRecord(1, entry)}
	set, err := renderSessionRegions(historyView(ring, "", PageContent{}, sessionLookup{}), newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	html := string(set[regionEntries].HTML)
	for _, want := range []string{
		`<article class="wbeffects wbupdate" data-entry="` + ring[0].Name + `" data-at="` + ring[0].At.UTC().Format("2006-01-02T15:04:05.999999999Z") + `">`,
		"Committed whiteboard update",
		"Saved panel summary",
		"Some changes or values were omitted",
		`data-wb-jump="p1"`,
		`data-wb-topic="topic-706c616e"`,
		`class="wbupdate-panel-wrap" data-wb-element="p1"`,
		`class="wbupdate-row wbupdate-panel wbupdate-panel-has-preview"`,
		`class="wbupdate-panel-body md"`,
		`<span class="wbupdate-preview-badge">Inline HTML content</span>`,
		`<strong>not markdown</strong>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q: %s", want, html)
		}
	}
	for _, bad := range []string{"<script", "<img", "<iframe", "<canvas", "alert(1)", "attack()", "old &lt;b&gt;value&lt;/b&gt;", "**not markdown**", `class="wbeffects" open`} {
		if strings.Contains(html, bad) {
			t.Errorf("executable/unsafe/before content %q: %s", bad, html)
		}
	}
	bodyIdx := strings.Index(html, `<div class="wbupdate-panel-body md">`)
	if bodyIdx < 0 || strings.Contains(html[bodyIdx:], "<svg") {
		t.Errorf("panel preview body missing or emitted inline svg: %s", html)
	}
}

func TestWhiteboardEffectsVisibleBoundedFallback(t *testing.T) {
	for _, text := range []string{
		`<img src=x onerror="attack()">` + strings.Repeat("雪", 5000),
		`{"v":2,"effects":[{"kind":"item","id":1,"field":"title","after":"new"}]}`,
		`{"v":1,"effects":[{"kind":"future","field":"title","after":"invented"}]}`,
	} {
		ring := []wslive.RingRecord{ringRecord(1, wslive.RingEntry{Kind: wslive.RingWhiteboardEffects, Text: text})}
		set, err := renderSessionRegions(historyView(ring, "", PageContent{}, sessionLookup{}), nil)
		if err != nil {
			t.Fatal(err)
		}
		html := string(set[regionEntries].HTML)
		if !strings.Contains(html, "Details unavailable or unsupported") || !strings.Contains(html, `class="wbeffect-fallback"`) || !strings.Contains(html, `data-entry="`+ring[0].Name) {
			t.Fatalf("missing visible fallback: %s", html)
		}
		if strings.Contains(html, "<img") || strings.Contains(html, `class="wbeffect-values"`) || len(html) > 20*1024 {
			t.Fatalf("unbounded/interpreted fallback: %.400s", html)
		}
	}
}

func TestWhiteboardEffectsSavedLeadModeWithoutLiveState(t *testing.T) {
	now := testNow
	stale := liveSnap(now.Add(-time.Hour), wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseReplying}, Reply: &wslive.Reply{Text: "stale streaming text"}})
	for _, snap := range []*wslive.Snapshot{nil, stale} {
		session := liveSession("self", "/proj", now.Add(-time.Hour))
		v := buildSessionView(pageInputs{Session: session, Snapshot: snap, Ring: []wslive.RingRecord{ringRecord(1, dashboardEffect("saved only"))}, Now: now})
		if v.Adapter || v.Live || v.ProcessRunning || !v.SavedConversation || v.Mode() != modeHistory || v.Conversation.Reply != nil || v.Conversation.Phase != "" || v.Host != nil || len(v.Commands) != 0 {
			t.Fatalf("fabricated live state: %+v", v)
		}
		regions, err := renderSessionRegions(v, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := regions[regionEntries]; !ok {
			t.Fatal("saved entries region omitted")
		}
		for _, absent := range []string{regionTree, regionConvLive, regionCtx, regionCmds, regionSLine} {
			if _, ok := regions[absent]; ok {
				t.Errorf("live region %s present", absent)
			}
		}
		page, err := renderSessionPage(v, regions, true, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`data-mode="history"`, `id="tabbar"`, `id="convo"`, "Saved panel summary"} {
			if !strings.Contains(page, want) {
				t.Errorf("saved page lacks %q", want)
			}
		}
		for _, absent := range []string{`id="composer"`, `id="ctext"`, `id="splitter"`, `id="apbox"`, "stale streaming text"} {
			if strings.Contains(page, absent) {
				t.Errorf("saved page has %q", absent)
			}
		}
		// An empty legacy board remains board-only, and a runner child is
		// never promoted to an adapter-less lead history page.
		if empty := buildSessionView(pageInputs{Session: session, Now: now}); empty.Mode() != modeBoard {
			t.Fatal("empty board changed mode")
		}
		session.Agent = true
		if child := buildSessionView(pageInputs{Session: session, Ring: []wslive.RingRecord{ringRecord(1, dashboardEffect("child"))}, Now: now}); child.SavedConversation {
			t.Fatal("child promoted")
		}
	}
}

func TestWhiteboardEffectsPinnedOldRendererOmitsOnlyNewKind(t *testing.T) {
	// Full unedited template at 4ddb1910537678e2e8125ee448a1d26d6f4010ec.
	old, err := os.ReadFile("testdata/pre_effects/dashboard.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(old)); got != "3d68bf0856ccfebf24cb2b063c3298dabefa31a208be8ca119fe2be3f182b504" {
		t.Fatal("old renderer fixture changed: " + got)
	}
	a, err := buildDashboardAssets(func(name string) ([]byte, error) {
		if name == "dashboard.tmpl" {
			return old, nil
		}
		return embeddedDashboardAsset(name)
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := []ConvEntry{
		{RingEntry: wslive.RingEntry{Kind: wslive.RingUser, Text: "legacy user"}, Key: "user"},
		{RingEntry: dashboardEffect("must be omitted"), Key: "effect"},
		{RingEntry: wslive.RingEntry{Kind: wslive.RingTools, Tools: toolsSummary()}, Key: "tools"},
		{RingEntry: wslive.RingEntry{Kind: wslive.RingCommand, Command: "/legacy", Text: "legacy output"}, Key: "command"},
	}
	var buf bytes.Buffer
	for _, entry := range entries {
		if err := a.tmpl.ExecuteTemplate(&buf, "entry", entry); err != nil {
			t.Fatal(err)
		}
	}
	html := buf.String()
	for _, want := range []string{"legacy user", "legacy output", `data-entry="tools"`, "whiteboard.update"} {
		if !strings.Contains(html, want) {
			t.Errorf("old renderer lost %q", want)
		}
	}
	if strings.Contains(html, "must be omitted") || strings.Contains(html, `data-entry="effect"`) {
		t.Fatal("old renderer interpreted new effects")
	}
}

func TestWhiteboardEffectsHistoryLayoutAndClientGuards(t *testing.T) {
	css, err := dashboardTestAsset("assets/dashboard.css")
	if err != nil {
		t.Fatal(err)
	}
	js, err := dashboardTestAsset("assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	// Static tripwires, not browser geometry/interaction acceptance.
	for _, want := range []string{`.dgrid.history[data-tab="board"] > .convo`, `.dgrid.history[data-tab] > .bcol`, `.wbupdate-items { display: flex; flex-wrap: wrap;`, `white-space: pre-wrap; overflow-wrap: anywhere`} {
		if !strings.Contains(string(css), want) {
			t.Errorf("missing responsive guard %q", want)
		}
	}
	for _, forbidden := range []string{`.wbeffect-values`, `.wbeffect-id`, `.wbeffect-details`, `.wbupdate-options`, `.wbupdate-class`} {
		if strings.Contains(string(css), forbidden) {
			t.Errorf("obsolete diagnostic/item styling %q", forbidden)
		}
	}
	for _, want := range []string{`mode !== "history" ? WsRail.drafts`, `var liveMode = mode === "live"`, `q("details[data-key]"`, `convo.addEventListener("scroll", nearTop)`} {
		if !strings.Contains(string(js), want) {
			t.Errorf("missing client guard %q", want)
		}
	}
}

func TestWhiteboardPanelPreviewMarkdownAndBounds(t *testing.T) {
	md := strings.Join([]string{
		"## Overview",
		"",
		"- **Bold** and `inline`",
		"- [Safe](https://example.com) and [Bad](javascript:alert(1))",
		"",
		"> Quoted note",
		"",
		"| Col A | Col B |",
		"| --- | --- |",
		"| 1 | 2 |",
	}, "\n")
	out := string(whiteboardPanelPreview(md, "en"))
	for _, want := range []string{
		"<h2>Overview</h2>",
		"<strong>Bold</strong>",
		"<code>inline</code>",
		`<a href="https://example.com" target="_blank" rel="noopener noreferrer">Safe</a>`,
		"<blockquote>",
		"<table>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing markdown preview element %q: %s", want, out)
		}
	}
	if strings.Contains(out, "javascript:") {
		t.Fatalf("unsafe javascript link survived: %s", out)
	}
	if !strings.Contains(out, ">Bad<") && !strings.Contains(out, "Bad") {
		t.Errorf("unlinked safe label lost: %s", out)
	}

	// Non-empty line cap (>12 lines) drops later lines cleanly.
	var manyLines []string
	for i := 1; i <= 16; i++ {
		manyLines = append(manyLines, fmt.Sprintf("Line %02d summary", i))
	}
	bounded := string(whiteboardPanelPreview(strings.Join(manyLines, "\n"), "en"))
	if !strings.Contains(bounded, "Line 01 summary") || !strings.Contains(bounded, "Line 12 summary") {
		t.Errorf("expected first 12 non-empty lines: %s", bounded)
	}
	if strings.Contains(bounded, "Line 13 summary") || strings.Contains(bounded, "Line 16 summary") {
		t.Errorf("expected tail lines beyond limit omitted: %s", bounded)
	}

	// Open code fence cut mid-block by line/rune limit closes cleanly.
	var fencedLines []string
	fencedLines = append(fencedLines, "```go")
	for i := 1; i <= 16; i++ {
		fencedLines = append(fencedLines, fmt.Sprintf("fmt.Println(%d)", i))
	}
	fencedLines = append(fencedLines, "```")
	fencedLines = append(fencedLines, "After fence")
	fencedOut := string(whiteboardPanelPreview(strings.Join(fencedLines, "\n"), "en"))
	if !strings.Contains(fencedOut, `<pre><code class="language-go">`) || !strings.Contains(fencedOut, `</code></pre>`) {
		t.Errorf("truncated fenced code block did not close cleanly: %s", fencedOut)
	}
	if strings.Contains(fencedOut, "After fence") {
		t.Errorf("content beyond pre-slice limit leaked: %s", fencedOut)
	}

	// Rune limit cuts cleanly on multibyte UTF-8 boundaries.
	korean := strings.Repeat("가", panelPreviewMaxRunes+100)
	koreanOut := string(whiteboardPanelPreview(korean, "ko"))
	if !utf8.ValidString(koreanOut) {
		t.Fatal("invalid UTF-8 after rune bounding")
	}
	if utf8.RuneCountInString(koreanOut) > panelPreviewMaxRunes+64 {
		t.Errorf("rune bound failed to cap long single line: len=%d", utf8.RuneCountInString(koreanOut))
	}
}

func TestWhiteboardPanelPreviewFragmentsAndSanitization(t *testing.T) {
	// Empty/whitespace payload omits preview container completely.
	emptyEntry, _ := wslive.NewWhiteboardEffects([]wswhiteboard.Effect{
		{Kind: "panel", Topic: "plan", EntryID: "e1", PanelID: "p1", Field: "payload", After: "   \n\t\n"},
		{Kind: "panel", Topic: "plan", EntryID: "e1", PanelID: "p1", Field: "recap", After: "Header only recap"},
	})
	regions, err := renderSessionRegions(historyView([]wslive.RingRecord{ringRecord(1, emptyEntry)}, "", PageContent{}, sessionLookup{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	emptyHTML := string(regions[regionEntries].HTML)
	if strings.Contains(emptyHTML, "wbupdate-panel-wrap") || strings.Contains(emptyHTML, "wbupdate-panel-has-preview") || strings.Contains(emptyHTML, "wbupdate-panel-body") {
		t.Fatalf("whitespace-only panel emitted preview markup: %s", emptyHTML)
	}

	// Mockup boilerplate + follow-up text.
	mockupPayload := "Interactive HTML mockup snapshot from `ai-docs/assets/mockups/ui.html` (`snapshot-only`).\n\nPrimary CTA layout"
	mockupOut := string(whiteboardPanelPreview(mockupPayload, "en"))
	for _, want := range []string{
		`<span class="wbupdate-preview-badge">Mockup</span> <code>ai-docs/assets/mockups/ui.html</code>`,
		`<p>Primary CTA layout</p>`,
	} {
		if !strings.Contains(mockupOut, want) {
			t.Errorf("mockup preview missing %q: %s", want, mockupOut)
		}
	}

	// Diagram (mermaid), image with/without alt, SVG, HTML, wb:raw, consecutive collapsing, backtick preservation, sentinel forging.
	payload := strings.Join([]string{
		"```mermaid",
		"graph TD",
		"A-->B",
		"```",
		"",
		"![Architecture overview diagram with extra descriptive suffix](diagram.png) and ![](blank.png)",
		"",
		`<svg viewBox="0 0 10 10"><rect /></svg> <svg><circle /></svg>`,
		`<canvas width="10"></canvas> <script>evil()</script> <wb:raw><b>hidden</b></wb:raw>`,
		"Keep `<svg>` and `<script>` and `![alt](x)` literal.",
		"\uE000WSB:diagram\uE001",
		"```html",
		"<script>insideCode()</script>",
		"```",
	}, "\n")
	out := string(whiteboardPanelPreview(payload, "en"))
	for _, forbidden := range []string{
		"<img", "<svg", "<canvas", "<script", "<iframe", "<style", `class="mermaid"`, "wb:raw", "evil()", "\uE000", "\uE001",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("forbidden heavy/unsafe token %q in preview: %s", forbidden, out)
		}
	}
	if strings.Count(out, `<span class="wbupdate-preview-badge">Diagram</span>`) != 1 {
		t.Errorf("expected exactly 1 diagram badge (mermaid only, forged sentinel stripped): %s", out)
	}
	if strings.Count(out, `<span class="wbupdate-preview-badge">Inline SVG</span>`) != 1 {
		t.Errorf("expected consecutive SVGs to collapse into 1 badge: %s", out)
	}
	if strings.Count(out, `<span class="wbupdate-preview-badge">Inline HTML content</span>`) != 1 {
		t.Errorf("expected consecutive HTML fragments (canvas/script/wb:raw) to collapse into 1 badge: %s", out)
	}
	if strings.Count(out, `<span class="wbupdate-preview-badge">Inline image</span>`) != 2 {
		t.Errorf("expected 2 image badges: %s", out)
	}
	if !strings.Contains(out, `<span class="wbupdate-preview-alt">Architecture overview diagram with extr…</span>`) {
		t.Errorf("expected bounded image alt span: %s", out)
	}
	if !strings.Contains(out, "<code>&lt;svg&gt;</code>") || !strings.Contains(out, "<code>&lt;script&gt;</code>") || !strings.Contains(out, "&lt;script&gt;insideCode()&lt;/script&gt;") {
		t.Errorf("inline code or code fence tags were erroneously replaced: %s", out)
	}

	// Math comparison without closing '>' stays literal text rather than swallowing following lines.
	mathOut := string(whiteboardPanelPreview("if x<y\nnext line survives", "en"))
	if !strings.Contains(mathOut, "x&lt;y") || !strings.Contains(mathOut, "next line survives") {
		t.Errorf("unclosed comparison swallowed subsequent content: %s", mathOut)
	}
}

func TestWhiteboardPanelPreviewI18nAndCacheIsolation(t *testing.T) {
	payload := strings.Join([]string{
		"Interactive HTML mockup snapshot from `ai-docs/assets/mockups/board.html` (`snapshot-only`).",
		"",
		"![Hero](hero.png)",
		`<svg><rect/></svg>`,
		`<div>box</div>`,
		"```mermaid",
		"flowchart LR",
		"```",
	}, "\n")
	koOut := string(whiteboardPanelPreview(payload, "ko"))
	for _, want := range []string{"목업", "인라인된 이미지", "인라인된 SVG", "인라인된 HTML 콘텐츠", "다이어그램"} {
		if !strings.Contains(koOut, want) {
			t.Errorf("korean preview missing %q: %s", want, koOut)
		}
	}

	entry, _ := wslive.NewWhiteboardEffects([]wswhiteboard.Effect{
		{Kind: "panel", Topic: "plan", EntryID: "e1", PanelID: "p1", Field: "payload", After: "Interactive HTML mockup snapshot from `mock.html`."},
		{Kind: "panel", Topic: "plan", EntryID: "e1", PanelID: "p1", Field: "recap", After: "Mock recap"},
	})
	ring := []wslive.RingRecord{ringRecord(1, entry)}
	cache := newEntryCache()

	vEn := historyView(ring, "", PageContent{}, sessionLookup{})
	vEn.Lang = "en"
	enRegions, err := renderSessionRegions(vEn, cache)
	if err != nil {
		t.Fatal(err)
	}
	vKo := historyView(ring, "", PageContent{}, sessionLookup{})
	vKo.Lang = "ko"
	koRegions, err := renderSessionRegions(vKo, cache)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(enRegions[regionEntries].HTML), `<span class="wbupdate-preview-badge">Mockup</span>`) {
		t.Errorf("en cache miss/render failed: %s", string(enRegions[regionEntries].HTML))
	}
	if !strings.Contains(string(koRegions[regionEntries].HTML), `<span class="wbupdate-preview-badge">목업</span>`) {
		t.Errorf("ko cache leaked en translation: %s", string(koRegions[regionEntries].HTML))
	}
}

func TestWhiteboardPanelPreviewCSSAndJSContract(t *testing.T) {
	css, err := dashboardTestAsset("assets/dashboard-conversation.css")
	if err != nil {
		t.Fatal(err)
	}
	cssText := string(css)
	for _, want := range []string{".wbupdate-panel-wrap", ".wbupdate-panel-has-preview", ".wbupdate-panel-body", ".wbupdate-preview-badge", ".wbupdate-preview-alt"} {
		if !strings.Contains(cssText, want) {
			t.Errorf("missing CSS rule %q", want)
		}
	}
	for _, line := range strings.Split(cssText, "\n") {
		if (strings.Contains(line, ".wbupdate-panel") || strings.Contains(line, ".wbupdate-preview")) && strings.Contains(line, ":has(") {
			t.Fatalf("forbidden :has() selector in preview CSS: %s", line)
		}
	}

	js, err := dashboardTestAsset("assets/dashboard-conversation.js")
	if err != nil {
		t.Fatal(err)
	}
	jsText := string(js)
	for _, forbidden := range []string{"wbupdate-panel-body", "wbupdate-panel-has-preview", "wbupdate-preview-"} {
		if strings.Contains(jsText, forbidden) {
			t.Errorf("zero-JS contract violated; found %q in dashboard-conversation.js", forbidden)
		}
	}
}
