package wsdashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

func TestConversationLogMarkdownInlineRendering(t *testing.T) {
	targets := map[string]string{
		"item-1654": boardTopicTarget("plan"),
		"p2":        boardTopicTarget("plan"),
	}

	got := string(renderConversationLogMarkdown(
		"Checked `safePreviewHref` in **bold** and *italic* with ~~old~~ [docs](https://example.com/a?x=1&y=2) and ![alt <tag>](https://example.com/x.png) plus https://example.org/auto.",
		targets,
	))
	for _, want := range []string{
		"<code>safePreviewHref</code>",
		"<strong>bold</strong>",
		"<em>italic</em>",
		"<del>old</del>",
		`<a href="https://example.com/a?x=1&amp;y=2" target="_blank" rel="noopener noreferrer">docs</a>`,
		"alt &lt;tag&gt;",
		`<a href="https://example.org/auto" target="_blank" rel="noopener noreferrer">https://example.org/auto</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, bad := range []string{"<p>", "<img", "<h1>", "<ul>", "<blockquote>"} {
		if strings.Contains(got, bad) {
			t.Errorf("unexpected block or image markup %q in %s", bad, got)
		}
	}

	// Block syntax stays inline-only (no headings, lists, blockquotes, or fenced <pre>).
	inlineOnly := string(renderConversationLogMarkdown("# Heading\n- item\n> quote\n```go\nx\n```", nil))
	for _, bad := range []string{"<h1", "<ul", "<li", "<blockquote", "<pre", "<p>"} {
		if strings.Contains(inlineOnly, bad) {
			t.Errorf("block-level tag %q leaked into inline-only log output: %s", bad, inlineOnly)
		}
	}
	if !strings.Contains(inlineOnly, "# Heading\n- item\n&gt; quote") || !strings.Contains(inlineOnly, "<code>") {
		t.Errorf("expected inline-only text without block elements, got: %s", inlineOnly)
	}
}

func TestConversationLogMarkdownXSSAndEscaping(t *testing.T) {
	targets := map[string]string{"item-1654": boardTopicTarget("plan")}
	hostile := `<script>alert(1)</script><img src=x onerror="attack()"> Vec<T> [bad](javascript:alert(1)) [mail](mailto:hi@example.com) ![pic <svg onload=1>](https://x.test/a.png)`
	got := string(renderConversationLogMarkdown(hostile, targets))

	for _, want := range []string{
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		"&lt;img src=x onerror=&quot;attack()&quot;&gt;",
		"Vec&lt;T&gt;",
		"bad",
		`<a href="mailto:hi@example.com" target="_blank" rel="noopener noreferrer">mail</a>`,
		"pic &lt;svg onload=1&gt;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing escaped/safe output %q in %s", want, got)
		}
	}
	for _, bad := range []string{"<script", "<img", "<svg", "<iframe", "javascript:"} {
		if strings.Contains(strings.ToLower(got), bad) {
			t.Errorf("unsafe fragment %q survived in %s", bad, got)
		}
	}
}

func TestConversationLogMarkdownBoardRefs(t *testing.T) {
	targets := map[string]string{
		"item-1654": boardTopicTarget("plan"),
		"item-58":   boardTopicTarget("other"),
		"p2":        boardTopicTarget("plan"),
	}
	input := "Ref {{1654}} and **{{0058}}** and *{{p2}}* plus `{{1654}}` and [see {{1654}}](https://example.com) and [bad {{58}}](javascript:alert(1)) and invalid {{0}} {{9999999}} {{e8}} {{x}}"
	got := string(renderConversationLogMarkdown(input, targets))

	for _, want := range []string{
		`<span class="wbref" role="link" tabindex="0" data-wb-nav="item-1654" data-wb-topic="topic-706c616e">(1654)</span>`,
		`<strong><span class="wbref" role="link" tabindex="0" data-wb-nav="item-58" data-wb-topic="topic-6f74686572">(58)</span></strong>`,
		`<em><span class="wbref" role="link" tabindex="0" data-wb-nav="p2" data-wb-topic="topic-706c616e">p2</span></em>`,
		`<code>{{1654}}</code>`,
		`<a href="https://example.com" target="_blank" rel="noopener noreferrer">see (1654)</a>`,
		`bad <span class="wbref" role="link" tabindex="0" data-wb-nav="item-58" data-wb-topic="topic-6f74686572">(58)</span>`,
		"{{0}} {{9999999}} {{e8}} {{x}}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing expected ref output %q in %s", want, got)
		}
	}
	if strings.Contains(got, `<a href="https://example.com" target="_blank" rel="noopener noreferrer">see <span class="wbref"`) {
		t.Errorf("interactive .wbref must not nest inside <a>: %s", got)
	}
}

func TestConversationLogWellGroupingAndMixedCards(t *testing.T) {
	// Consecutive logs share one well.
	threeLogs := effectPresentation(t,
		wswhiteboard.Effect{Kind: "log", Topic: "plan", EntryID: "e1", Field: "text", After: "First **log** `a`"},
		wswhiteboard.Effect{Kind: "log", Topic: "plan", EntryID: "e2", Field: "text", After: "Second *log* {{42}}"},
		wswhiteboard.Effect{Kind: "log", Topic: "plan", EntryID: "e3", Field: "text", After: "Third `log`"},
	)
	threeCard := threeLogs.WhiteboardCard()
	if groups := threeCard.Topics[0].RowGroups(); len(groups) != 1 || !groups[0].IsLog || len(groups[0].Rows) != 3 {
		t.Fatalf("expected 1 log group with 3 rows, got %+v", groups)
	}
	threeHTML := effectHTML(t,
		wswhiteboard.Effect{Kind: "log", Topic: "plan", EntryID: "e1", Field: "text", After: "First **log** `a`"},
		wswhiteboard.Effect{Kind: "log", Topic: "plan", EntryID: "e2", Field: "text", After: "Second *log* {{42}}"},
		wswhiteboard.Effect{Kind: "log", Topic: "plan", EntryID: "e3", Field: "text", After: "Third `log`"},
	)
	if n := strings.Count(threeHTML, `<div class="wbupdate-log-well">`); n != 1 {
		t.Fatalf("expected 1 shared .wbupdate-log-well for consecutive logs, got %d: %s", n, threeHTML)
	}
	if n := strings.Count(threeHTML, `class="wbupdate-row wbupdate-log"`); n != 3 {
		t.Fatalf("expected 3 .wbupdate-log rows inside well, got %d: %s", n, threeHTML)
	}

	// Mixed card: panel -> 2 logs -> panel -> 1 log -> item chip.
	mixedEffects := []wswhiteboard.Effect{
		{Kind: "panel", Topic: "plan", PanelID: "p1", EntryID: "e10", Field: "recap", After: "Panel **recap** {{42}}"},
		{Kind: "log", Topic: "plan", EntryID: "e11", Field: "text", After: "Log **one** `cmd`"},
		{Kind: "log", Topic: "plan", EntryID: "e12", Field: "text", After: "Log two <script>x</script>"},
		{Kind: "panel", Topic: "plan", PanelID: "p2", EntryID: "e13", Field: "recap", After: "Second panel"},
		{Kind: "log", Topic: "plan", EntryID: "e14", Field: "text", After: "Log three"},
		{Kind: "item", Topic: "plan", ID: 42, Field: "title", After: "Question"},
	}
	mixedCard := effectPresentation(t, mixedEffects...).WhiteboardCard()
	if len(mixedCard.Topics) != 1 || len(mixedCard.Topics[0].Rows) != 5 {
		t.Fatalf("Rows must stay flat on topic: %+v", mixedCard)
	}
	groups := mixedCard.Topics[0].RowGroups()
	if len(groups) != 4 || groups[0].IsLog || !groups[1].IsLog || len(groups[1].Rows) != 2 || groups[2].IsLog || !groups[3].IsLog || len(groups[3].Rows) != 1 {
		t.Fatalf("unexpected mixed RowGroups: %+v", groups)
	}
	mixedHTML := effectHTML(t, mixedEffects...)
	if n := strings.Count(mixedHTML, `<div class="wbupdate-log-well">`); n != 2 {
		t.Fatalf("expected 2 separate log wells across panel boundary, got %d: %s", n, mixedHTML)
	}
	// Panel recaps remain plain escaped text + .wbref (never log inline markdown).
	if !strings.Contains(mixedHTML, `Panel **recap** <span class="wbref"`) {
		t.Fatalf("panel recap should stay plain text + .wbref: %s", mixedHTML)
	}
	if !strings.Contains(mixedHTML, `<strong>one</strong>`) || !strings.Contains(mixedHTML, `<code>cmd</code>`) || strings.Contains(mixedHTML, `<script>`) {
		t.Fatalf("log markdown or escaping missing in mixed card: %s", mixedHTML)
	}
	if itemsIdx, wellIdx := strings.Index(mixedHTML, `class="wbupdate-items"`), strings.LastIndex(mixedHTML, `class="wbupdate-log-well"`); itemsIdx < 0 || wellIdx < 0 || itemsIdx < wellIdx {
		t.Fatalf("item chips must appear after log wells (items=%d well=%d): %s", itemsIdx, wellIdx, mixedHTML)
	}
}

func TestConversationLogOptionCStyles(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("assets", "dashboard-conversation.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, want := range []string{
		"--wb-log-well-bg: #f6f6f3;",
		"--wb-log-well-bg: color-mix(in srgb, var(--wb-bg) 52%, var(--wb-card));",
		"--wb-log-well-hover:",
		"--wb-log-well-sep:",
		"--wb-log-code-bg:",
		".wbupdate-log-well {",
		".wbupdate-log + .wbupdate-log { border-top: 1px solid var(--wb-log-well-sep);",
		".wbupdate-log .wbupdate-text {",
		".wbupdate-log .wbupdate-text strong {",
		".wbupdate-log .wbupdate-text code {",
		".wbupdate .wbupdate-log .wbref {",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("dashboard-conversation.css missing Option C rule/token %q", want)
		}
	}
	if strings.Count(css, "--wb-log-well-bg: color-mix(in srgb, var(--wb-bg) 52%, var(--wb-card));") != 2 {
		t.Errorf("expected dark-mode --wb-log-well-bg in both [data-theme=dark] and prefers-color-scheme:dark")
	}
}
