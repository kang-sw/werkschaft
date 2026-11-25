package wsdashboard

import (
	"html"
	"strconv"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
	xhtml "golang.org/x/net/html"
)

func TestMailboxTagParserAndDynamicLabels(t *testing.T) {
	ctx := mailboxContext{Dir: "/work/devenv", Branch: "develop"}

	cases := []struct {
		name      string
		raw       string
		wantOK    bool
		wantLabel string
		wantTip   []string
	}{
		{
			name:      "same dir same branch labeled",
			raw:       `<mailbox id="id:abcdef123456" dir="/work/devenv" branch="develop" harness="claude">Review Worker</mailbox>`,
			wantOK:    true,
			wantLabel: "Review Worker",
			wantTip:   []string{"id: id:abcdef123456", "dir: /work/devenv", "branch: develop", "harness: claude"},
		},
		{
			name:      "same dir same branch unlabeled falls back to short id",
			raw:       `<mailbox id="id:abcdef123456" dir="/work/devenv" branch="develop"/>`,
			wantOK:    true,
			wantLabel: "id:abcdef",
			wantTip:   []string{"id: id:abcdef123456", "dir: /work/devenv", "branch: develop"},
		},
		{
			name:      "same dir different branch labeled",
			raw:       `<mailbox id="id:abcdef123456" dir="/work/devenv" branch="feat/badge">Badge Worker</mailbox>`,
			wantOK:    true,
			wantLabel: "feat/badge · Badge Worker",
		},
		{
			name:      "same dir different branch unlabeled",
			raw:       `<mailbox id="id:abcdef123456" dir="/work/devenv" branch="feat/badge"/>`,
			wantOK:    true,
			wantLabel: "feat/badge",
		},
		{
			name:      "different dir with branch labeled",
			raw:       `<mailbox id="id:abcdef123456" dir="/work/other-app" root="/work" branch="main" harness="codex">API</mailbox>`,
			wantOK:    true,
			wantLabel: "other-app:main · API",
			wantTip:   []string{"id: id:abcdef123456", "dir: /work/other-app", "root: /work", "branch: main", "harness: codex"},
		},
		{
			name:      "different dir with branch unlabeled",
			raw:       `<mailbox id="id:abcdef123456" dir="/work/other-app" branch="main"/>`,
			wantOK:    true,
			wantLabel: "other-app:main",
		},
		{
			name:      "different dir plain folder no branch labeled",
			raw:       `<mailbox id="id:abcdef123456" dir="/tmp/scratch-notes">Notes</mailbox>`,
			wantOK:    true,
			wantLabel: "scratch-notes · Notes",
		},
		{
			name:      "different dir plain folder no branch unlabeled",
			raw:       `<mailbox id="id:abcdef123456" dir="/tmp/scratch-notes/"/>`,
			wantOK:    true,
			wantLabel: "scratch-notes",
		},
		{
			name:      "xml escaped attrs and inner label without double unescape",
			raw:       `<mailbox id="id:esc12345" dir="/work/a&amp;b" branch="f&lt;1&gt;" harness="h&quot;x">A &amp; B &lt;3&gt; &amp;lt;lit&amp;gt;</mailbox>`,
			wantOK:    true,
			wantLabel: `a&b:f<1> · A & B <3> &lt;lit&gt;`,
			wantTip:   []string{`dir: /work/a&b`, `branch: f<1>`, `harness: h"x`},
		},
		{
			name:   "missing id rejected",
			raw:    `<mailbox dir="/work/devenv" branch="develop">NoID</mailbox>`,
			wantOK: false,
		},
		{
			name:   "unclosed tag rejected",
			raw:    `<mailbox id="id:abc" dir="/work/devenv">NoClose`,
			wantOK: false,
		},
		{
			name:   "raw angle bracket in label rejected",
			raw:    `<mailbox id="id:abc" dir="/work/devenv">bad<tag></mailbox>`,
			wantOK: false,
		},
		{
			name:   "duplicate attribute rejected",
			raw:    `<mailbox id="id:abc" id="id:def" dir="/work/devenv"/>`,
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag, end, ok := parseMailboxTag(tc.raw, 0)
			if ok != tc.wantOK {
				t.Fatalf("parseMailboxTag(%q) ok=%v, want %v", tc.raw, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if end != len(tc.raw) {
				t.Fatalf("end=%d, want %d", end, len(tc.raw))
			}
			if got := formatMailboxLabel(tag, ctx); got != tc.wantLabel {
				t.Errorf("formatMailboxLabel = %q, want %q", got, tc.wantLabel)
			}
			tip := formatMailboxTooltip(tag)
			for _, wantLine := range tc.wantTip {
				if !strings.Contains(tip, wantLine) {
					t.Errorf("tooltip %q missing %q", tip, wantLine)
				}
			}
		})
	}

	// Windows drive normalization: case-insensitive + slash-insensitive.
	winTag, _, ok := parseMailboxTag(`<mailbox id="id:win123456" dir="C:\Users\Dev\Repo\" branch="develop">Win</mailbox>`, 0)
	if !ok {
		t.Fatal("failed to parse windows path tag")
	}
	if got := formatMailboxLabel(winTag, mailboxContext{Dir: "c:/users/dev/repo", Branch: "develop"}); got != "Win" {
		t.Errorf("windows same-dir label = %q, want Win", got)
	}
}

func TestMailboxBadgeSSRHeldDeliveredXSSAndTextrefOpacity(t *testing.T) {
	sameTag := `<mailbox id="id:abcdef123456" dir="/proj" branch="develop" harness="claude">Worker A</mailbox>`
	diffTag := `<mailbox id="id:998877665544" dir="/other/repo" root="/other" branch="main" harness="codex"/>`
	xssTag := `<mailbox id="id:xss12345" dir="/proj" branch="develop">&lt;script&gt;alert(1)&lt;/script&gt;</mailbox>`
	malformed := `<mailbox id="id:broken" dir="/proj">unclosed`
	innerTextref := `<mailbox id="id:inside123" dir="/proj" branch="develop">InsideTextref</mailbox>`
	textref := `<textref id="#1" bytes="` + strconv.Itoa(len(innerTextref)) + `">` + innerTextref + `</textref>`

	prose := "Ping " + sameTag + " and " + diffTag + " plus " + xssTag + " and " + malformed + " and " + textref + " done."
	entry := wsinbox.Entry{Parts: []string{prose}}

	check := func(kind, htmlOut string) {
		t.Helper()
		if got := strings.Count(htmlOut, `class="icapsule icapsule-mailbox"`); got != 3 {
			t.Fatalf("%s: expected 3 mailbox capsules (not inside textref or malformed), got %d:\n%s", kind, got, htmlOut)
		}
		for _, want := range []string{
			`>Worker A</span>`,
			`>repo:main</span>`,
			`>` + html.EscapeString(`<script>alert(1)</script>`) + `</span>`,
			`root: /other`,
			`harness: codex`,
			html.EscapeString(malformed),
			html.EscapeString(innerTextref),
		} {
			if !strings.Contains(htmlOut, want) {
				t.Errorf("%s: missing %q in:\n%s", kind, want, htmlOut)
			}
		}
		if strings.Contains(htmlOut, "<script>alert(1)</script>") {
			t.Fatalf("%s: unescaped script tag in HTML:\n%s", kind, htmlOut)
		}
		doc, err := xhtml.Parse(strings.NewReader(htmlOut))
		if err != nil {
			t.Fatalf("%s: HTML parse failed: %v", kind, err)
		}
		var walk func(*xhtml.Node)
		walk = func(n *xhtml.Node) {
			if n.Type == xhtml.ElementNode && n.Data == "script" {
				t.Fatalf("%s: unexpected <script> element in DOM", kind)
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
	}

	view := buildSessionView(pageInputs{
		Session:  liveSession("self", "/proj", testNow),
		Snapshot: liveSnap(testNow, wslive.Lead{}),
		Ring:     []wslive.RingRecord{ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: entry.Text()})},
		Held:     []wsinbox.Record{{Name: "held.json", Entry: entry}},
		Now:      testNow,
	})
	view.Git = &GitBadge{Repo: wslive.Repo{Branch: "develop"}}

	cache := newEntryCache()
	set, err := renderSessionRegions(view, cache)
	if err != nil {
		t.Fatal(err)
	}
	check("delivered", string(set[regionEntries].HTML))
	check("held", string(set[regionConvLive].HTML))

	// Switching viewing branch must invalidate entryCache for mailbox-bearing entries
	// and update relative labels, while plain entries remain cached.
	plainView := buildSessionView(pageInputs{
		Session:  liveSession("self", "/proj", testNow),
		Snapshot: liveSnap(testNow, wslive.Lead{}),
		Ring: []wslive.RingRecord{
			ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: wsinbox.Entry{Parts: []string{"plain message"}}.Text()}),
			ringRecord(2, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: wsinbox.Entry{Parts: []string{"Ask " + sameTag}}.Text()}),
		},
		Now: testNow,
	})
	plainView.Git = &GitBadge{Repo: wslive.Repo{Branch: "develop"}}
	c2 := newEntryCache()
	set1, err := renderSessionRegions(plainView, c2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(set1[regionEntries].HTML), `>Worker A</span>`) {
		t.Fatalf("expected same-branch label Worker A, got:\n%s", set1[regionEntries].HTML)
	}
	buildsAfterFirst := c2.buildCount()
	plainView.Git = &GitBadge{Repo: wslive.Repo{Branch: "feat/other"}}
	set2, err := renderSessionRegions(plainView, c2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(set2[regionEntries].HTML), `>develop · Worker A</span>`) {
		t.Fatalf("expected diff-branch label 'develop · Worker A', got:\n%s", set2[regionEntries].HTML)
	}
	if got := c2.buildCount() - buildsAfterFirst; got != 1 {
		t.Fatalf("expected only the 1 mailbox entry to rebuild on branch change (plain entry stays cached), got %d rebuilds", got)
	}
}
