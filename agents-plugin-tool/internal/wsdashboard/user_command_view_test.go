package wsdashboard

import (
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

func TestUserCommandCardRenderSSRHeldAndCopy(t *testing.T) {
	payload := wsinbox.FormatUserCommand(wsinbox.UserCommand{
		Label:  `Run <Tests> & "Verify"`,
		Edited: true,
		Cwd:    "agents-plugin-tool",
		Cmd:    "go test -race ./... && echo '<b>ok</b>'",
		Prompt: "Please run & verify.",
	})
	offset := 540
	entry := wsinbox.Entry{
		SentAt:           testNow.UnixMilli(),
		UTCOffsetMinutes: &offset,
		Parts:            []string{payload},
	}

	// 1) Delivered history / SSE rendering via buildSessionView + renderSessionRegions.
	ring := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{
			Kind:   wslive.RingUser,
			Origin: wslive.OriginWhiteboard,
			Text:   entry.Text(),
		}),
	}
	v := buildSessionView(pageInputs{
		Session:  liveSession("self", "/proj", testNow),
		Snapshot: liveSnap(testNow, wslive.Lead{}),
		Ring:     ring,
		Now:      testNow,
	})
	set, err := renderSessionRegions(v, newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	markup := string(set[regionEntries].HTML)
	for _, want := range []string{
		`class="ucmd-req"`,
		`class="ucmd-req-label">Run &lt;Tests&gt; &amp; &#34;Verify&#34;</span>`,
		`class="ucmd-req-edited">inline edited</span>`,
		`class="ucmd-req-cwd" title="agents-plugin-tool">agents-plugin-tool</span>`,
		`class="ucmd-req-cmd"><code>go test -race ./... &amp;&amp; echo &#39;&lt;b&gt;ok&lt;/b&gt;&#39;</code></pre>`,
		`class="ucmd-req-prompt">Please run &amp; verify.</div>`,
	} {
		if !strings.Contains(markup, want) {
			t.Fatalf("SSR markup missing %q:\n%s", want, markup)
		}
	}
	for _, forbid := range []string{"&lt;ws:ucmd", "&lt;/ws:ucmd", "<b>ok</b>"} {
		if strings.Contains(markup, forbid) {
			t.Fatalf("SSR markup leaked %q:\n%s", forbid, markup)
		}
	}

	// 2) Copy API (userBlockTexts) returns raw unwrapped <ws:ucmd> text, matching <wb:selection> conventions.
	texts := userBlockTexts(entry.Text())
	if len(texts) != 1 || texts[0] != payload {
		t.Fatalf("userBlockTexts = %+v, want [%q]", texts, payload)
	}

	// 3) Server-rendered HeldView renders the same .ucmd-req card.
	heldRec := wsinbox.Record{Name: "1700000000000-0001.json", Entry: entry}
	hv := heldViews([]wsinbox.Record{heldRec}, "", "self")
	if len(hv) != 1 {
		t.Fatalf("heldViews = %+v", hv)
	}
	hv[0].Lang = "en"
	hydrated := hv[0].Hydrated()
	if !hydrated.Grouped {
		t.Fatalf("expected held <ws:ucmd> to be grouped")
	}
	heldHTML := string(hydrated.Body)
	if !strings.Contains(heldHTML, `class="ucmd-req"`) || strings.Contains(heldHTML, "&lt;ws:ucmd") {
		t.Fatalf("held HTML = %s", heldHTML)
	}

	// 4) Malformed <ws:ucmd> safely falls back to escaped raw text with no card.
	malformed := "<ws:ucmd label=\"Broken\">\n<cmd>echo hi</cmd>"
	badMarkup := string(leadMarkdownView(t, []wslive.RingEntry{{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: malformed}}, ""))
	if strings.Contains(badMarkup, `class="ucmd-req"`) {
		t.Fatalf("malformed tag rendered as card: %s", badMarkup)
	}
	if !strings.Contains(badMarkup, html.EscapeString(malformed)) {
		t.Fatalf("malformed tag did not fall back to escaped raw text: %s", badMarkup)
	}
}

func TestRailMessagesFormatsUserCommandPreview(t *testing.T) {
	dir := wslive.RingDir(t.TempDir())
	labeled := wsinbox.FormatUserCommand(wsinbox.UserCommand{
		Label:  "Unit Tests",
		Edited: true,
		Cwd:    "pkg",
		Cmd:    "go test -race ./...\n&& echo ok",
		Prompt: "Run tests.",
	})
	offset := 540
	enveloped := wsinbox.Entry{
		SentAt:           testNow.UnixMilli(),
		UTCOffsetMinutes: &offset,
		Parts:            []string{labeled},
	}.Text()
	if err := wslive.AppendRing(dir, []wslive.RingEntry{
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: enveloped},
	}); err != nil {
		t.Fatal(err)
	}
	c := newRailMessages()
	got := c.last("s", 1, dir)
	if !got.User || got.Text != "[Unit Tests] go test -race ./... && echo ok" {
		t.Fatalf("rail enveloped ucmd = %+v", got)
	}

	unlabeled := wsinbox.FormatUserCommand(wsinbox.UserCommand{
		Cmd: "git status -sb",
	})
	if err := wslive.AppendRing(dir, []wslive.RingEntry{
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: unlabeled},
	}); err != nil {
		t.Fatal(err)
	}
	got2 := c.last("s", 2, dir)
	if !got2.User || got2.Text != "git status -sb" {
		t.Fatalf("rail unlabeled ucmd = %+v", got2)
	}

	// Literal image-syntax lines inside <ws:ucmd> stay intact in the rail preview,
	// while real image attachment lines outside <ws:ucmd> are still stripped.
	imgInsideCmd := wsinbox.FormatUserCommand(wsinbox.UserCommand{
		Label: "Inspect",
		Cmd:   "cat <<EOF\n[image: /tmp/literal.png]\nImage #1: /tmp/literal2.png\nEOF",
	})
	withOutsideImg := "[image: /tmp/outside.png]\n" + imgInsideCmd + "\nImage #2: /tmp/outside2.png"
	if err := wslive.AppendRing(dir, []wslive.RingEntry{
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: withOutsideImg},
	}); err != nil {
		t.Fatal(err)
	}
	got3 := c.last("s", 3, dir)
	want3 := "[Inspect] cat <<EOF [image: /tmp/literal.png] Image #1: /tmp/literal2.png EOF"
	if !got3.User || got3.Text != want3 {
		t.Fatalf("rail ucmd with literal image lines = %q, want %q", got3.Text, want3)
	}
}

func TestUserCommandWithAdjacentImages(t *testing.T) {
	attachDir := filepath.Join(t.TempDir(), "attachments")
	if err := os.MkdirAll(attachDir, 0o700); err != nil {
		t.Fatal(err)
	}
	imgName := "0123456789abcdef-1.png"
	imgPath := filepath.Join(attachDir, imgName)
	if err := os.WriteFile(imgPath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	imgLine := "[image: " + imgPath + "]"
	cmd1 := wsinbox.FormatUserCommand(wsinbox.UserCommand{
		Label: "First",
		Cmd:   "echo one",
	})
	cmd2 := wsinbox.FormatUserCommand(wsinbox.UserCommand{
		Label: "Second",
		Cmd:   "echo two",
	})

	cases := []struct {
		name      string
		input     string
		wantCards int
		wantText  []string
	}{
		{
			name:      "prose then image then ucmd",
			input:     "hello\n" + imgLine + "\n" + cmd1,
			wantCards: 1,
			wantText:  []string{"hello", "echo one"},
		},
		{
			name:      "ucmd then image then prose",
			input:     cmd1 + "\n" + imgLine + "\nworld",
			wantCards: 1,
			wantText:  []string{"echo one", "world"},
		},
		{
			name:      "ucmd then image then ucmd",
			input:     cmd1 + "\n" + imgLine + "\n" + cmd2,
			wantCards: 2,
			wantText:  []string{"echo one", "echo two"},
		},
		{
			name:      "image only before ucmd",
			input:     imgLine + "\n" + cmd1,
			wantCards: 1,
			wantText:  []string{"echo one"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rest, content, images, _ := splitSelectionContent(tc.input, attachDir, "livesession")
			if len(images) != 1 || images[0].Name != imgName {
				t.Fatalf("images = %+v, want 1 attachment %s", images, imgName)
			}
			hyd := hydrateUserMessage("en", mailboxContext{}, nil, nil, nil, nil, nil, nil, nil, rest, nil, content)
			if !hyd.Grouped {
				t.Fatalf("expected grouped content for %q", tc.input)
			}
			body := string(hyd.Body)
			if gotCards := strings.Count(body, `class="ucmd-req"`); gotCards != tc.wantCards {
				t.Fatalf("card count = %d, want %d; body:\n%s", gotCards, tc.wantCards, body)
			}
			if strings.Contains(body, "&lt;ws:ucmd") || strings.Contains(body, "&lt;/ws:ucmd") {
				t.Fatalf("raw <ws:ucmd> leaked into body:\n%s", body)
			}
			for _, want := range tc.wantText {
				if !strings.Contains(body, want) {
					t.Fatalf("body missing %q:\n%s", want, body)
				}
			}
		})
	}
}

func TestOutboxBubbleHydratesUserCommandCard(t *testing.T) {
	src := mustAsset(t, "dashboard.js")
	fns := composerFunc(t, src, "ucmdUnescapeXml") +
		composerFunc(t, src, "parseUcmdBlock") +
		composerFunc(t, src, "splitUcmdParts") +
		composerFunc(t, src, "formatUcmdPreview") +
		composerFunc(t, src, "outboxRow") +
		composerFunc(t, src, "outboxBubble")
	runNode(t, "outbox ucmd hydration", `
const assert = require("node:assert/strict");
const window = {};
function makeEl(tag) {
  return {
    tagName: tag.toUpperCase(),
    className: "",
    attrs: {},
    children: [],
    _text: "",
    get textContent() {
      if (this.children.length) return this.children.map(c => c.textContent).join("");
      return this._text;
    },
    set textContent(v) { this._text = String(v); this.children = []; },
    get firstChild() { return this.children[0] || null; },
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return this.attrs[k]; },
    appendChild(c) { this.children.push(c); return c; },
    cloneNode() { return makeEl(tag); },
    addEventListener() {},
    querySelector(sel) {
      for (const c of this.children) {
        if (sel.startsWith(".") && c.className && c.className.split(/\s+/).includes(sel.slice(1))) return c;
        const found = c.querySelector ? c.querySelector(sel) : null;
        if (found) return found;
      }
      return null;
    }
  };
}
const doc = {
  createElement: makeEl,
  createElementNS: (_ns, tag) => makeEl(tag),
  createTextNode: (t) => ({ nodeType: 3, textContent: String(t) }),
  querySelector: () => null
};
function heldAct() { return null; }
function wsT(k) {
  if (k === "js.ucmd.ask.header") return "User command";
  if (k === "js.ucmd.ask.edited") return "(inline edited)";
  return k;
}
function marker(n) { return "[Image #" + n + "]"; }
`+fns+`
const xml = [
  '<ws:ucmd label="Build &amp; Test" edited="true">',
  'User requested execution of the following command.',
  '<cmd cwd="pkg">go test ./... &amp;&amp; echo &lt;ok&gt;</cmd>',
  '<prompt>Verify &amp; report.</prompt>',
  '</ws:ucmd>'
].join("\n");

// 1) outboxRow preview formats cleanly without raw XML.
const row = outboxRow({ back: { text: xml }, pics: [] });
const rowText = row.querySelector(".d-outbox-text");
assert.equal(rowText.textContent, "[Build & Test] go test ./... && echo <ok>");

// 2) outboxBubble hydrates into .ucmd-req card with matching classes and unescaped text.
const bub = outboxBubble({ id: "h1", back: { text: xml }, pics: [] });
const card = bub.querySelector(".ucmd-req");
assert(card, "expected .ucmd-req card in outboxBubble");
assert.equal(card.querySelector(".ucmd-req-label").textContent, "Build & Test");
assert.equal(card.querySelector(".ucmd-req-edited").textContent, "inline edited");
assert.equal(card.querySelector(".ucmd-req-cwd").textContent, "pkg");
assert.equal(card.querySelector(".ucmd-req-cmd").textContent, "go test ./... && echo <ok>");
assert.equal(card.querySelector(".ucmd-req-prompt").textContent, "Verify & report.");
assert.equal(bub.textContent.includes("<ws:ucmd"), false);

// 3) Malformed <ws:ucmd> falls back to plain text without .ucmd-req card.
const bad = "<ws:ucmd label=\"Broken\">\n<cmd>echo hi</cmd>";
const badBub = outboxBubble({ id: "h2", back: { text: bad }, pics: [] });
assert.equal(badBub.querySelector(".ucmd-req"), null);
assert.equal(badBub.textContent.includes("<ws:ucmd"), true);
`)
}
