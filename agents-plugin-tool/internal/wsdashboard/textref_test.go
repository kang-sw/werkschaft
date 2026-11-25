package wsdashboard

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

func renderDashboardTemplate(t *testing.T, name string, data any) string {
	t.Helper()
	assets, err := loadDashboardAssets()
	if err != nil {
		t.Fatalf("loadDashboardAssets: %v", err)
	}
	var buf bytes.Buffer
	if err := assets.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		t.Fatalf("ExecuteTemplate(%s): %v", name, err)
	}
	return buf.String()
}

func TestTextRefSSRHeldAndDeliveredParityAndOpacity(t *testing.T) {
	attachDir := t.TempDir()
	imgPath := filepath.Join(attachDir, "0123456789abcdef-1.png")
	if err := writeTestFile(imgPath, []byte("png")); err != nil {
		t.Fatal(err)
	}
	// Body contains every parser trigger (<wb:selection>, Image line, [Image #1],
	// answer anchor, mention, mailbox tag, closing tag, and XSS payloads).
	opaqueBody := strings.Join([]string{
		"첫째 줄 🚀",
		"<wb:selection><wb:mention topic=\"t\" id=\"01\">quoted</wb:mention></wb:selection>",
		"Image #1: " + imgPath,
		"[Image #1] (03)=a [mention:t] <mailbox id=\"id:hidden123\" dir=\"/work/devenv\" branch=\"develop\">Hidden</mailbox>",
		"</textref><script>alert(1)</script></pre></template>",
	}, "\n")
	block, err := wsinbox.TextRefBlock(2, opaqueBody)
	if err != nil {
		t.Fatalf("TextRefBlock: %v", err)
	}
	outerMailbox := `<mailbox id="id:outer123456" dir="/work/devenv" branch="develop" harness="claude">Lead A</mailbox>`
	rawMsg := "See [Text #2], " + outerMailbox + " and literal [Text #9] plus [Image #1]\n\n" + block

	mbCtx := mailboxContext{Dir: "/work/devenv", Branch: "develop"}
	// 1. Delivered userentry path
	delivered := ConvEntry{
		RingEntry:  wslive.RingEntry{Kind: wslive.RingUser, Text: rawMsg + "\n\nImage #1: " + imgPath},
		Key:        "u1",
		Lang:       "en",
		MailboxCtx: mbCtx,
	}
	parseUserContent(&delivered, attachDir, "rid1")
	if len(delivered.TextRefs) != 1 || delivered.TextRefs[0].Num != 2 || delivered.TextRefs[0].Body != opaqueBody {
		t.Fatalf("delivered.TextRefs = %+v, want 1 ref #2 with exact opaque body", delivered.TextRefs)
	}
	if len(delivered.Content) != 0 {
		t.Fatalf("delivered.Content = %+v, want nil (<wb:selection> inside <textref> must stay opaque)", delivered.Content)
	}
	if len(delivered.Mentions) != 0 {
		t.Fatalf("delivered.Mentions = %+v, want nil (<wb:mention> inside <textref> must stay opaque)", delivered.Mentions)
	}
	if len(delivered.Images) != 1 || delivered.Images[0].Num != 1 {
		t.Fatalf("delivered.Images = %+v, want 1 outer image #1 only", delivered.Images)
	}
	deliveredHTML := renderDashboardTemplate(t, "userentry", delivered)

	// 2. Held bubble path
	rec := wsinbox.Record{
		Name: "20261125T000000.000000000Z-12345678.json",
		Entry: wsinbox.Entry{
			Parts:     []string{rawMsg},
			Images:    []string{imgPath},
			ImageNums: []int{1},
		},
	}
	heldList := heldViews([]wsinbox.Record{rec}, attachDir, "rid1")
	if len(heldList) != 1 {
		t.Fatalf("len(heldViews) = %d, want 1", len(heldList))
	}
	held := heldList[0]
	held.Lang = "en"
	held.MailboxCtx = mbCtx
	if len(held.TextRefs) != 1 || held.TextRefs[0].Num != 2 || held.TextRefs[0].Body != opaqueBody {
		t.Fatalf("held.TextRefs = %+v, want 1 ref #2 with exact opaque body", held.TextRefs)
	}
	if len(held.Content) != 0 {
		t.Fatalf("held.Content = %+v, want nil (<wb:selection> inside <textref> must stay opaque)", held.Content)
	}
	heldHTML := renderDashboardTemplate(t, "held", held)

	for _, tc := range []struct {
		name string
		html string
	}{
		{"delivered", deliveredHTML},
		{"held", heldHTML},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range []string{
				`class="txttag" data-icapsule="1" data-icapsule-click="1"`,
				`<span>Text #2</span>`,
				`literal [Text #9]`,
				`class="textref-thumb" data-textref="2" data-icapsule="1" data-icapsule-click="1"`,
				`<use href="#l-file-text"/>`,
				`<pre class="icapsule-pop-textref">`,
				`&lt;script&gt;alert(1)&lt;/script&gt;`,
				`&lt;/pre&gt;&lt;/template&gt;`,
				`&lt;wb:selection&gt;`,
				`&lt;mailbox id=&#34;id:hidden123&#34; dir=&#34;/work/devenv&#34; branch=&#34;develop&#34;&gt;Hidden&lt;/mailbox&gt;`,
				`<span class="icapsule icapsule-mailbox"`,
				`>Lead A</span>`,
				`class="thumb"`,
			} {
				if !strings.Contains(tc.html, want) {
					t.Errorf("%s HTML missing %q\ngot: %s", tc.name, want, tc.html)
				}
			}
			for _, forbid := range []string{
				`<script>alert(1)</script>`,
				`class="icapsule-selection"`,
				`<textref id="#2"`,
				`>Hidden</span>`,
			} {
				if strings.Contains(tc.html, forbid) {
					t.Errorf("%s HTML unexpectedly contains %q\ngot: %s", tc.name, forbid, tc.html)
				}
			}
		})
	}
}

func TestTextRefSSRMalformedFallsBackToLiteral(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"length_mismatch", `Look <textref id="#1" bytes="999">short</textref>`},
		{"missing_close", `Look <textref id="#1" bytes="5">hello`},
		{"cap_truncated", `Look <textref id="#1" bytes="12">hello world</tex`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := ConvEntry{
				RingEntry: wslive.RingEntry{Kind: wslive.RingUser, Text: tc.text},
				Key:       "u-bad",
				Lang:      "en",
			}
			parseUserContent(&e, t.TempDir(), "rid-1")
			if len(e.TextRefs) != 0 {
				t.Fatalf("TextRefs = %+v, want empty for malformed %s", e.TextRefs, tc.name)
			}
			if e.Text != tc.text {
				t.Fatalf("Text = %q, want original literal %q", e.Text, tc.text)
			}
			html := renderDashboardTemplate(t, "userentry", e)
			if strings.Contains(html, `class="textref-thumb"`) || strings.Contains(html, `class="txttag"`) {
				t.Fatalf("malformed %s unexpectedly hydrated: %s", tc.name, html)
			}
			if !strings.Contains(html, `&lt;textref id=&#34;#1&#34;`) {
				t.Fatalf("malformed %s did not render escaped literal tag: %s", tc.name, html)
			}
		})
	}
}

func TestComposerTextRefsBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; run node --test testdata/textrefs.test.cjs locally")
	}
	out, err := exec.Command(node, "--test", "testdata/textrefs.test.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("composer textrefs tests: %v\n%s", err, out)
	}
	t.Log(string(out))
}

func writeTestFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
