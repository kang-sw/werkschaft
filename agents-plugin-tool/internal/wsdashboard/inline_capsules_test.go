package wsdashboard

import (
	"context"
	"encoding/base64"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
	xhtml "golang.org/x/net/html"
)

func TestInlineCapsulesAnchorHydrationAndHchipsDeduplication(t *testing.T) {
	state := &wswhiteboard.State{
		Items: []wswhiteboard.Item{
			{ID: 44, Topic: "plan"},
			{ID: 45, Topic: "plan"},
			{ID: 46, Topic: "plan"},
			{ID: 81, Topic: "ship"},
		},
		Topics: []wswhiteboard.TopicDoc{
			{Name: "ship", Entries: []wswhiteboard.Entry{{ID: "e7", PanelID: "p3"}}},
		},
	}
	targets := BoardNavigationTargets(state)
	entry := wsinbox.Entry{
		Chips: []wsinbox.Chip{
			{ID: "44", Answer: "ok"},
			{ID: "45", Answer: "ok"},
			{ID: "46", Answer: "ok"},
			{ID: "81", Answer: "a", Note: "first line\nsecond line"},
		},
		Topics: []wsinbox.TopicNote{
			{Topic: "ship", Note: "ship note\ndetails"},
			{Topic: "unanchored-topic", Note: "still in header"},
		},
		Mentions: []wsinbox.Mention{
			{Topic: "ship", Entry: "e7", Text: "p3 · Recap summary\nline two"},
			{Topic: "plan", ID: "44", Text: "unanchored mention"},
		},
		Picks: []wsinbox.Pick{
			{URL: "http://localhost:5173/app", Selector: "button[data-id='save']", Text: "Save"},
		},
		Consoles: []wsinbox.Console{
			{URL: "http://localhost:5173/app", Lines: []string{"TypeError: boom", "at line 42"}},
		},
		Parts: []string{
			"Approve (45)=ok and (81)=a with [note:ship] plus {{p3}}, [pick:button[data-id='save']], and [console:http://localhost:5173/app].",
		},
	}

	check := func(kind, htmlOut string) {
		t.Helper()
		for _, want := range []string{
			`class="icapsule icapsule-answer" role="link" tabindex="0" data-wb-nav="item-45"`,
			`class="icapsule icapsule-answer" data-icapsule="1"`,
			`>(81)</b> <span class="icapsule-val">a</span><span class="icapsule-sub">first line second line</span>`,
			`class="icapsule icapsule-topic" data-icapsule="1"`,
			`>[ship]</b><span class="icapsule-sub">ship note details</span>`,
			`class="icapsule icapsule-mention" data-icapsule="1"`,
			`>p3</b><span class="icapsule-sub">Recap summary line two</span>`,
			`class="icapsule icapsule-pick" data-icapsule="1"`,
			`>button[data-id=&#39;save&#39;]</b>`,
			`class="icapsule icapsule-console" data-icapsule="1"`,
			`>console ×2</b>`,
		} {
			if !strings.Contains(htmlOut, want) {
				t.Errorf("%s: missing inline capsule %q in:\n%s", kind, want, htmlOut)
			}
		}
		// Unanchored items (44, 46, unanchored-topic, unanchored mention) stay in .hchips.
		hchipsStart := strings.Index(htmlOut, `<div class="hchips">`)
		if hchipsStart < 0 {
			t.Fatalf("%s: expected unanchored fallback .hchips in:\n%s", kind, htmlOut)
		}
		hchipsEnd := strings.Index(htmlOut[hchipsStart:], `</div>`)
		hchips := htmlOut[hchipsStart : hchipsStart+hchipsEnd]
		for _, keep := range []string{"(44)", "(46)", "[unanchored-topic]", "unanchored mention"} {
			if !strings.Contains(hchips, keep) {
				t.Errorf("%s: unanchored item %q missing from .hchips: %s", kind, keep, hchips)
			}
		}
		for _, deduped := range []string{"(45)", "(81)", "[ship]", "p3 · Recap", "button[data-id=", "console ×2"} {
			if strings.Contains(hchips, deduped) {
				t.Errorf("%s: anchored item %q leaked into .hchips: %s", kind, deduped, hchips)
			}
		}
	}

	deliveredRing := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: entry.Text()}),
	}
	check("delivered", presentationHTML(t, deliveredRing, PageContent{BoardTargets: targets}, newEntryCache()))

	heldSet, err := renderSessionRegions(buildSessionView(pageInputs{
		Session:  liveSession("self", "/proj", testNow),
		Snapshot: liveSnap(testNow, wslive.Lead{}),
		Held:     []wsinbox.Record{{Name: "held.json", Entry: entry}},
		Page:     PageContent{BoardTargets: targets},
		Now:      testNow,
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	check("held", string(heldSet[regionConvLive].HTML))

	// When EVERY header item is anchored inline, .hchips is omitted completely.
	allAnchored := wsinbox.Entry{
		Chips: []wsinbox.Chip{{ID: "81", Answer: "a"}},
		Parts: []string{"Go with (81)=a now."},
	}
	allOut := presentationHTML(t, []wslive.RingRecord{
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: allAnchored.Text()}),
	}, PageContent{BoardTargets: targets}, nil)
	if strings.Contains(allOut, `class="hchips"`) {
		t.Fatalf("fully anchored message still rendered empty/duplicate .hchips:\n%s", allOut)
	}
}

func TestInlineCapsulesLegacyFallbackAndLiteralProseSafety(t *testing.T) {
	// Legacy unanchored message: header items have no anchors in prose -> all stay in .hchips.
	legacy := wsinbox.Entry{
		Chips:    []wsinbox.Chip{{ID: "12", Answer: "b", Note: "legacy note"}},
		Mentions: []wsinbox.Mention{{Topic: "auth", Text: "p2 · Auth panel"}},
		Parts:    []string{"Look at the board please."},
	}
	legacyHTML := presentationHTML(t, []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: legacy.Text()}),
	}, PageContent{}, nil)
	if strings.Contains(legacyHTML, `class="icapsule`) {
		t.Fatalf("legacy unanchored message should not fabricate inline capsules:\n%s", legacyHTML)
	}
	if !strings.Contains(legacyHTML, `class="hchips"`) || !strings.Contains(legacyHTML, "(12)") || !strings.Contains(legacyHTML, "[auth]") {
		t.Fatalf("legacy unanchored message lost .hchips:\n%s", legacyHTML)
	}

	// Literal prose with anchor-shaped tokens and only one matching chip.
	proseOnly := wsinbox.Entry{
		Chips: []wsinbox.Chip{{ID: "51", Answer: "a"}},
		Parts: []string{
			"Literal foo(51)=a and (51)=a once, then duplicate (51)=a, plus (99)=z {{p3}} [note:missing] [pick:#btn] [console:https://x] [ticket:261120-feat-example].",
		},
	}
	proseHTML := presentationHTML(t, []wslive.RingRecord{
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: proseOnly.Text()}),
	}, PageContent{}, nil)
	if got := strings.Count(proseHTML, `class="icapsule `); got != 1 {
		t.Fatalf("expected exactly 1 hydrated capsule for the single matching (51)=a, got %d:\n%s", got, proseHTML)
	}
	for _, literal := range []string{"foo(51)=a", "duplicate (51)=a", "(99)=z", "{{p3}}", "[note:missing]", "[pick:#btn]", "[console:https://x]", "[ticket:261120-feat-example]"} {
		if !strings.Contains(proseHTML, literal) {
			t.Errorf("literal prose %q was altered:\n%s", literal, proseHTML)
		}
	}
}

func TestInlineCapsulesPopoverXSSEscaping(t *testing.T) {
	payload := `</template><script>alert(1)</script><img src=x onerror=alert(2)> & "quoted"`
	selBlock, err := wsinbox.SelectionBlock(wsinbox.Selection{
		Mentions: []wsinbox.Mention{{Topic: `t</template><script>`, ID: "5", Entry: "e2", Text: payload}},
		Comment:  payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := wsinbox.Entry{
		Chips:    []wsinbox.Chip{{ID: "5", Answer: "a", Note: payload}},
		Picks:    []wsinbox.Pick{{URL: "https://example.test/" + payload, Selector: "#x", Text: payload}},
		Consoles: []wsinbox.Console{{URL: "https://example.test/c", Lines: []string{payload}}},
		Parts:    []string{"Check (5)=a and [pick:#x] and [console:https://example.test/c]\n\n" + selBlock},
	}
	out := presentationHTML(t, []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: entry.Text()}),
	}, PageContent{}, nil)

	if strings.Contains(out, "<script>") || strings.Contains(out, "<img src=x") {
		t.Fatalf("unescaped script/img tag in rendered HTML:\n%s", out)
	}
	if !strings.Contains(out, html.EscapeString("</template><script>alert(1)</script>")) {
		t.Fatalf("escaped payload missing from popover template:\n%s", out)
	}
	doc, err := xhtml.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatalf("HTML parse failed: %v", err)
	}
	var templates int
	var checkNodes func(*xhtml.Node)
	checkNodes = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			if n.Data == "script" || n.Data == "img" {
				t.Fatalf("unexpected executable/image element <%s> injected into DOM", n.Data)
			}
			if n.Data == "template" {
				templates++
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			checkNodes(c)
		}
	}
	checkNodes(doc)
	if templates != 4 {
		t.Fatalf("expected 4 popover templates (answer, pick, console, selection), got %d", templates)
	}
}

func TestOutboxBubbleInlineHydrationParityNode(t *testing.T) {
	src := mustAsset(t, "dashboard.js")
	runNode(t, "outbox inline hydration parity", selectionCodecSource(t)+composerToastPrelude+outboxDOM+composerFunc(t, src, "heldAct")+composerFunc(t, src, "outboxBubble")+`
function findAll(el, pred, out) {
  out = out || [];
  if (pred(el)) out.push(el);
  (el.children || []).forEach(function (c) { findAll(c, pred, out); });
  return out;
}
function textOf(el) {
  if (!el) return "";
  if (el.tagName === "TEMPLATE") return "";
  return (el.textContent || "") + (el.children || []).map(textOf).join("");
}
var sel = { mentions: [{ topic: "plan", id: "3", entry: "e7", text: "line one\n<script>alert(1)</script>" }], comment: "memo\n</template>", images: [] };
var back = {
  text: "Check (81)=a and {{p3}} and [pick:#save] and [console:http://localhost:5173]\n\n" + selectionBlock(sel) + "\n\nplus literal (99)=z [Image #1]",
  chips: [{ id: "81", answer: "a", note: "multi\nline note" }, { id: "82", answer: "b", note: "" }],
  topics: [{ topic: "unanchored", note: "stays in hchips" }],
  mentions: [{ topic: "plan", entry: "e7", text: "p3 · Recap snippet" }],
  picks: [{ url: "http://localhost:5173", selector: "#save", text: "Save" }],
  consoles: [{ url: "http://localhost:5173", lines: ["err1", "err2"] }]
};
var bubble = outboxBubble({ id: "held", back: back, pics: [] });
var hchips = findAll(bubble, function (e) { return e.className === "hchips"; })[0];
var remaining = textOf(hchips);
check("unanchored items remain in hchips", remaining.indexOf("(82)") >= 0 && remaining.indexOf("[unanchored]") >= 0, true);
check("anchored items deduplicated from hchips", remaining.indexOf("(81)") < 0 && remaining.indexOf("Recap") < 0 && remaining.indexOf("#save") < 0 && remaining.indexOf("console") < 0, true);
var pills = findAll(bubble, function (e) { return (e.className || "").split(/\s+/).indexOf("icapsule") >= 0; });
check("5 inline pills rendered in prose (81, p3, pick, console, selection)", pills.length, 5);
check("selection pill keeps escaped template without innerHTML", pills.every(function (p) { return !p.innerHTML; }), true);
var imgTags = findAll(bubble, function (e) { return e.className === "imgtag"; });
check("image tag rendered inline", imgTags.length, 1);
process.stdout.write(bad.join("\n"));
`)
}

func TestInlineCapsulesRealDashboardBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run real dashboard inline capsule browser acceptance")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	f := startPageFixture(t)
	dir := f.attachDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	imgPath := filepath.Join(dir, "0123456789abcdef-1.png")
	realPNG, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAABQAAAAUCAYAAACNiR0NAAAAFklEQVR42mP8z8Dwn4GKgHHU0FFDAwAN3wP9M4q1UQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imgPath, realPNG, 0o600); err != nil {
		t.Fatal(err)
	}

	selBlock, err := wsinbox.SelectionBlock(wsinbox.Selection{
		Mentions: []wsinbox.Mention{
			{
				Topic: "ship-gate",
				ID:    "81",
				Entry: "e7",
				Text: "func hydrateProse(prose string) template.HTML {\n  // preserves literal prose when header has no match\n  return markedText(lang, prose)\n}",
			},
			{
				Topic: "ship-gate",
				Entry: "e8",
				Text:  "Verify dark and light popover contrast and viewport clamping.",
			},
		},
		Comment: "Let's check the multi-line code snippet and keep the inline pill single-line.",
		Images:  []int{1},
	})
	if err != nil {
		t.Fatal(err)
	}

	userMsg := wsinbox.Entry{
		SentAt: testNow.Add(-2 * time.Minute).UnixMilli(),
		Chips: []wsinbox.Chip{
			{ID: "81", Answer: "a", Note: "Owner approved inline capsules\nwith multi-line detail popover."},
			{ID: "82", Answer: "ok"},
		},
		Topics: []wsinbox.TopicNote{
			{Topic: "ship-gate", Note: "Keep T0 compatibility; zero wire changes."},
		},
		Mentions: []wsinbox.Mention{
			{Topic: "ship-gate", Entry: "e7", Text: "p3 · Hydration Architecture\nMatches composer anchors against header items."},
		},
		Picks: []wsinbox.Pick{
			{URL: "http://localhost:5173/settings", Selector: "#save-btn", Text: "Save Settings"},
		},
		Consoles: []wsinbox.Console{
			{URL: "http://localhost:5173/settings", Lines: []string{"TypeError: Cannot read properties of null", "at dashboard.js:142"}},
		},
		Images:    []string{imgPath},
		ImageNums: []int{1},
		Parts: []string{
			"Proceed with (81)=a and [note:ship-gate]. Check {{p3}} and this code:\n\n" + selBlock + "\n\nAlso inspect [pick:#save-btn], [console:http://localhost:5173/settings], and [Image #1]. Literal (99)=z stays plain.",
		},
	}
	legacyMsg := wsinbox.Entry{
		SentAt: testNow.Add(-4 * time.Minute).UnixMilli(),
		Chips:  []wsinbox.Chip{{ID: "44", Answer: "b", Note: "Historical unanchored answer"}},
		Parts:  []string{"Legacy unanchored message keeps its header chip above."},
	}
	ringDir := wslive.RingDir(SessionDir(f.dir, "livesession"))
	if err := wslive.AppendRing(ringDir, []wslive.RingEntry{
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: legacyMsg.Text()},
		{Kind: wslive.RingReply, Text: "Understood — inline capsules hydrated at exact prose positions with single-line pills and floating detail popovers."},
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: userMsg.Text()},
	}); err != nil {
		t.Fatal(err)
	}

	shotsDir := os.Getenv("WS_INLINE_CAPSULE_SHOTS")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := []string{"testdata/inline-capsules.browser.cjs", f.base + "/s/livesession"}
	if shotsDir != "" {
		args = append(args, shotsDir)
	}
	out, err := exec.CommandContext(ctx, node, args...).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("inline capsules browser test failed: %v", err)
	}
}
