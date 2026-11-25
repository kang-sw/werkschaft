package wsinbox

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestTextRefRoundTripAndUTF8Framing(t *testing.T) {
	bodies := []string{
		"plain ascii log line\nsecond line",
		"한글 텍스트와 이모지 🚀🔥 그리고 </textref> 리터럴 포함",
		"<script>alert(1)</script>\n</textref>\n<textref id=\"#9\" bytes=\"1\">x</textref>",
		"",
	}
	for i, body := range bodies {
		num := i + 1
		block, err := TextRefBlock(num, body)
		if err != nil {
			t.Fatalf("case %d: TextRefBlock error: %v", i, err)
		}
		wantHeader := fmt.Sprintf(`<textref id="#%d" bytes="%d">`, num, len(body))
		if !strings.HasPrefix(block, wantHeader) || !strings.HasSuffix(block, `</textref>`) {
			t.Fatalf("case %d: unexpected wire format: %q", i, block)
		}
		parts := ParseTextRefs("see [Text #1]\n\n" + block + "\n\nafter")
		if len(parts) != 3 || parts[0].Text != "see [Text #1]\n\n" || parts[2].Text != "\n\nafter" {
			t.Fatalf("case %d: parts = %+v", i, parts)
		}
		if parts[1].TextRef == nil || parts[1].TextRef.Num != num || parts[1].TextRef.Body != body || parts[1].Text != block {
			t.Fatalf("case %d: parsed ref = %+v", i, parts[1])
		}
		prose, refs := StripTextRefs("see [Text #1]\n\n" + block)
		if prose != "see [Text #1]" || !reflect.DeepEqual(refs, []TextRef{{Num: num, Body: body}}) {
			t.Fatalf("case %d: StripTextRefs = (%q, %+v)", i, prose, refs)
		}
	}
}

func TestTextRefMalformedFallsBackToLiteral(t *testing.T) {
	valid, err := TextRefBlock(2, "valid body")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		text string
	}{
		{"length too small", `<textref id="#1" bytes="3">hello</textref>`},
		{"length too large", `<textref id="#1" bytes="99">hello</textref>`},
		{"missing close", `<textref id="#1" bytes="5">hello`},
		{"wrong close", `<textref id="#1" bytes="5">hello</text>`},
		{"zero id", `<textref id="#0" bytes="5">hello</textref>`},
		{"leading zero bytes", `<textref id="#1" bytes="05">hello</textref>`},
		{"truncated by cap", `<textref id="#1" bytes="20">truncated body`},
		{"utf16 count instead of utf8 bytes", `<textref id="#1" bytes="2">한글</textref>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := ParseTextRefs(tc.text)
			if len(parts) != 1 || parts[0].TextRef != nil || parts[0].Text != tc.text {
				t.Fatalf("malformed tag parsed: %+v", parts)
			}
			prose, refs := StripTextRefs(tc.text)
			if prose != tc.text || refs != nil {
				t.Fatalf("StripTextRefs modified malformed text: (%q, %+v)", prose, refs)
			}
			// A malformed tag must not prevent parsing a later valid <textref>.
			combined := tc.text + "\n\n" + valid
			parts = ParseTextRefs(combined)
			if len(parts) != 2 || parts[0].TextRef != nil || parts[0].Text != tc.text+"\n\n" || parts[1].TextRef == nil || parts[1].TextRef.Num != 2 || parts[1].TextRef.Body != "valid body" {
				t.Fatalf("later valid tag lost after malformed tag: %+v", parts)
			}
		})
	}
}

func TestTextRefBodyOpaqueInNormalizeAndParseSelections(t *testing.T) {
	// Build a valid <wb:selection> with an unattached image reference and
	// more than maxMentions mentions; inside a length-framed <textref>, it
	// must NOT be rewritten (preserving bytes="N"), must NOT count toward
	// maxMentions, and must NOT fail attached-image validation.
	mentions := make([]Mention, maxMentions)
	for i := range mentions {
		mentions[i] = Mention{Topic: "t", Text: fmt.Sprintf("item %d", i)}
	}
	// Also use CRLF so normalizeSelectionText would rewrite it if it inspected the body.
	rawSel, err := SelectionBlock(Selection{Mentions: mentions, Comment: "memo", Images: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	crlfSel := strings.ReplaceAll(rawSel, "\n", "\r\n")
	block, err := TextRefBlock(1, crlfSel)
	if err != nil {
		t.Fatal(err)
	}
	input := "Check [Text #1]\n\n" + block
	// Include a legacy mention too so if the inner selection's maxMentions
	// were counted, Normalize would reject for exceeding maxMentions.
	out, err := Normalize(Entry{
		Parts:    []string{input},
		Mentions: []Mention{{Topic: "top", Text: "outer mention"}},
	})
	if err != nil {
		t.Fatalf("Normalize rejected opaque selection inside textref: %v", err)
	}
	if len(out.Parts) != 1 || out.Parts[0] != input {
		t.Fatalf("Normalize mutated textref bytes:\ngot:  %q\nwant: %q", out.Parts, input)
	}
	// ParseSelections must also treat the entire textref as non-selection text.
	selParts := ParseSelections(input)
	if len(selParts) != 1 || selParts[0].Selection != nil || selParts[0].Text != input {
		t.Fatalf("ParseSelections leaked inner selection from textref: %+v", selParts)
	}
	// A real outer selection alongside a textref still parses and validates normally.
	outerSel, err := SelectionBlock(Selection{Mentions: []Mention{{Topic: "u", Text: "real"}}, Comment: "outer"})
	if err != nil {
		t.Fatal(err)
	}
	mixed := outerSel + "\n\n" + input
	selParts = ParseSelections(mixed)
	if len(selParts) != 2 || selParts[0].Selection == nil || selParts[1].Selection != nil || selParts[1].Text != "\n\n"+input {
		t.Fatalf("outer selection beside textref = %+v", selParts)
	}
	// Over-cap textref still fails Normalize with ErrInvalid.
	huge, err := TextRefBlock(1, strings.Repeat("a", maxTextBytes))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(Entry{Parts: []string{huge}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized textref accepted: %v", err)
	}
}

func TestExcerptStripsValidTextRefs(t *testing.T) {
	block, err := TextRefBlock(1, strings.Repeat("secret log payload ", 30))
	if err != nil {
		t.Fatal(err)
	}
	e := Entry{Parts: []string{"Look at [Text #1]\n\n" + block}}
	if got := e.Excerpt(); got != "Look at [Text #1]" {
		t.Fatalf("Excerpt() = %q, want %q", got, "Look at [Text #1]")
	}
}
