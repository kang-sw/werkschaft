package wsdashboard

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsauditstore"
)

type spanOut struct {
	Matches map[string]struct {
		Through int         `json:"through"`
		Spans   []auditSpan `json:"spans"`
		Capped  bool        `json:"capped"`
	} `json:"matches"`
}

// spanCase is one body, a query and what the matcher answers: the search's
// hit lines and the event route's spans must agree on one matcher.
type spanCase struct {
	name  string
	body  string
	head  int // stored_trunc head bytes; -1 for a body stored whole
	q     string
	regex bool
	cs    bool
	spans []auditSpan
	// hits are the lines /audit/search reports. A hit line without a span is a
	// line only an empty match matched (an empty match marks nothing).
	hits []int
}

func spanFixture(c spanCase) *Daemon {
	ev := auditEv(1, wsaudit.KindToolResult, auditBase.Add(time.Second))
	ev.Out = c.body
	if c.head >= 0 {
		ev.StoredTrunc = &wsaudit.StoredTrunc{Out: &wsaudit.BodyTrunc{OriginalBytes: len(c.body) + 1000, HeadBytes: c.head, TailBytes: len(c.body) - c.head}}
	}
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: []wsaudit.Envelope{ev}})
	return auditDaemon(fx)
}

func (c spanCase) params() string {
	p := "q=" + url.QueryEscape(c.q)
	if c.regex {
		p += "&regex=1"
	}
	if c.cs {
		p += "&case=1"
	}
	return p
}

// TestAuditEventSpansAgreeWithSearch pins the event route's match spans
// against /audit/search for the same query: one matcher, per-rune widths,
// scoped flags, Unicode classes, an empty match followed by a nonempty one
// after an astral character, and on a stored_trunc seam line the same-start
// alternative, a greedy match stopping at the seam and an assertion reading
// across it. Offsets are UTF-16 code units of the line, so a browser slices
// without conversion.
func TestAuditEventSpansAgreeWithSearch(t *testing.T) {
	sp := func(line, start, end int) auditSpan { return auditSpan{line, start, end} }
	cases := []spanCase{
		{name: "literal folds case", body: "Foo foo", head: -1, q: "FOO", spans: []auditSpan{sp(1, 0, 3), sp(1, 4, 7)}, hits: []int{1}},
		{name: "literal case sensitive", body: "Foo foo", head: -1, q: "foo", cs: true, spans: []auditSpan{sp(1, 4, 7)}, hits: []int{1}},
		{name: "empty match then letter after an astral char", body: "😀a", head: -1, q: `\p{L}*`, regex: true, spans: []auditSpan{sp(1, 2, 3)}, hits: []int{1}},
		{name: "script name", body: "x αβγ y", head: -1, q: `\p{Greek}+`, regex: true, cs: true, spans: []auditSpan{sp(1, 2, 5)}, hits: []int{1}},
		{name: "scoped flag", body: "FOO bar\nfoo", head: -1, q: `(?i:foo)`, regex: true, cs: true, spans: []auditSpan{sp(1, 0, 3), sp(2, 0, 3)}, hits: []int{1, 2}},
		{name: "leading flag", body: "FOO bar", head: -1, q: `(?i)foo`, regex: true, cs: true, spans: []auditSpan{sp(1, 0, 3)}, hits: []int{1}},
		{name: "begin text matches empty", body: "foo\nbar", head: -1, q: `\A`, regex: true, cs: true, hits: []int{1, 2}},
		{name: "begin text with text", body: "foo\nfoo", head: -1, q: `\Afoo`, regex: true, cs: true, spans: []auditSpan{sp(1, 0, 3), sp(2, 0, 3)}, hits: []int{1, 2}},
		{name: "dot is a rune", body: "😀\nab\né", head: -1, q: `^.$`, regex: true, cs: true, spans: []auditSpan{sp(1, 0, 2), sp(3, 0, 1)}, hits: []int{1, 3}},
		{name: "offsets after astral chars", body: "😀😀x\r\ny", head: -1, q: `x|y`, regex: true, cs: true, spans: []auditSpan{sp(1, 4, 5), sp(2, 0, 1)}, hits: []int{1, 2}},
		{name: "byte that is not UTF-8 is one unit", body: "\xffx", head: -1, q: `x`, spans: []auditSpan{sp(1, 1, 2)}, hits: []int{1}},
		{name: "overlapping literal is not found twice", body: "aaaa", head: -1, q: "aa", cs: true, spans: []auditSpan{sp(1, 0, 2), sp(1, 2, 4)}, hits: []int{1}},
		// A stored_trunc body: head "…head ends alpha", tail "beta tail starts…".
		{name: "seam: matches on both sides", body: "one\nhead ends alphabeta tail starts\nlast", head: 4 + 15, q: `alpha|beta`, regex: true, spans: []auditSpan{sp(2, 10, 15), sp(2, 15, 19)}, hits: []int{2}},
		{name: "seam: no span crosses it", body: "one\nhead ends alphabeta tail starts\nlast", head: 4 + 15, q: `phabe`, hits: nil},
		{name: "seam: same-start alternative marks the head", body: "ab", head: 1, q: `ab|a`, regex: true, spans: []auditSpan{sp(1, 0, 1)}, hits: []int{1}},
		{name: "seam: same-start alternative with an assertion", body: "ab", head: 1, q: `ab|a\B`, regex: true, spans: []auditSpan{sp(1, 0, 1)}, hits: []int{1}},
		{name: "seam: alternative reaching the sentinel is replaced", body: "ab", head: 1, q: `\ba\w?|a\B`, regex: true, spans: []auditSpan{sp(1, 0, 1)}, hits: []int{1}},
		{name: "seam: tail alternative reaching the sentinel is replaced", body: "ab c", head: 1, q: `\w?b\b`, regex: true, spans: []auditSpan{sp(1, 1, 2)}, hits: []int{1}},
		{name: "seam: greedy match stops at the seam", body: "xa12y", head: 3, q: `a.*`, regex: true, spans: []auditSpan{sp(1, 1, 3)}, hits: []int{1}},
		{name: "seam: greedy match on the tail", body: "xa12y", head: 2, q: `\d.*`, regex: true, spans: []auditSpan{sp(1, 2, 5)}, hits: []int{1}},
		{name: "seam: assertion reads across (no boundary)", body: "xab", head: 2, q: `a\b`, regex: true, hits: nil},
		{name: "seam: assertion reads across (no boundary, B)", body: "xab", head: 2, q: `a\B`, regex: true, spans: []auditSpan{sp(1, 1, 2)}, hits: []int{1}},
		{name: "seam: tail is not a line start", body: "xab", head: 2, q: `^b`, regex: true, hits: nil},
		{name: "seam: tail end is the line end", body: "xab", head: 2, q: `b$`, regex: true, spans: []auditSpan{sp(1, 2, 3)}, hits: []int{1}},
		{name: "seam: head is not a line end", body: "xab", head: 2, q: `a$`, regex: true, hits: nil},
		{name: "seam: many matches on both sides", body: "aXaaYa", head: 3, q: `a`, cs: true, spans: []auditSpan{sp(1, 0, 1), sp(1, 2, 3), sp(1, 3, 4), sp(1, 5, 6)}, hits: []int{1}},
		{name: "seam: many matches with an assertion", body: "a a aa a", head: 4, q: `\ba\b`, regex: true, spans: []auditSpan{sp(1, 0, 1), sp(1, 2, 3), sp(1, 7, 8)}, hits: []int{1}},
		{name: "seam: empty match then letter after an astral char", body: "😀a", head: 4, q: `\p{L}*`, regex: true, spans: []auditSpan{sp(1, 2, 3)}, hits: []int{1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := spanFixture(c)
			got := auditJSON[spanOut](t, d, "/s/sess/audit/event/e1?only=matches&"+c.params(), 200)
			out, ok := got.Matches[wsaudit.FieldOut]
			if !ok || out.Through != len(c.body) || out.Capped {
				t.Fatalf("matches = %+v", got.Matches)
			}
			if !reflect.DeepEqual(append([]auditSpan{}, out.Spans...), append([]auditSpan{}, c.spans...)) {
				t.Errorf("spans = %v, want %v", out.Spans, c.spans)
			}
			type searchOut struct {
				Hits []struct {
					Field  string `json:"field"`
					LineNo int    `json:"line_no"`
				} `json:"hits"`
			}
			s := auditJSON[searchOut](t, d, "/s/sess/audit/search?"+c.params(), 200)
			var hits []int
			for _, h := range s.Hits {
				hits = append(hits, h.LineNo)
			}
			if len(hits) != len(c.hits) {
				t.Fatalf("search hit lines = %v, want %v", hits, c.hits)
			}
			for _, line := range c.hits {
				found := false
				for _, h := range hits {
					found = found || h == line
				}
				if !found {
					t.Fatalf("search hit lines = %v, want %v", hits, c.hits)
				}
			}
			// Every span lies on a line the search reports.
			for _, span := range out.Spans {
				found := false
				for _, h := range hits {
					found = found || h == span.Line
				}
				if !found {
					t.Errorf("span %v is on a line the search did not hit (%v)", span, hits)
				}
			}
		})
	}
}

// TestAuditEventMatchesRequests pins the route's parameters: the envelope
// answer carries spans for every body it returns, only=matches answers them
// alone, a through_<part> prefix limits the text read, the count is capped,
// and the query is refused as /audit/search refuses it.
func TestAuditEventMatchesRequests(t *testing.T) {
	ev := auditEv(1, wsaudit.KindToolCall, auditBase)
	ev.In, ev.Out = "in needle", "out needle\nneedle again"
	other := wsaudit.NewOtherEnvelope("sess", "claude", auditBase, "raw needle")
	other.ID, other.Seq = "o1", 3
	empty := auditEv(2, wsaudit.KindHook, auditBase.Add(time.Second))
	empty.Out = ""
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: []wsaudit.Envelope{ev, empty, other}})
	d := auditDaemon(fx)

	full := auditJSON[struct {
		Out     string `json:"out"`
		InBytes int    `json:"in_bytes"`
		spanOut
	}](t, d, "/s/sess/audit/event/e1?q=needle", 200)
	if full.Out != ev.Out || full.InBytes != len(ev.In) || len(full.Matches) != 2 || len(full.Matches["out"].Spans) != 2 || full.Matches["in"].Spans[0] != (auditSpan{1, 3, 9}) {
		t.Fatalf("envelope with matches = %+v", full)
	}
	only := auditJSON[map[string]any](t, d, "/s/sess/audit/event/e1?q=needle&only=matches", 200)
	if _, has := only["out"]; has || len(only) != 1 {
		t.Fatalf("only=matches = %v", only)
	}
	if none := auditJSON[map[string]any](t, d, "/s/sess/audit/event/e2?q=needle&only=matches", 200); fmt.Sprint(none) != "map[matches:map[]]" {
		t.Fatalf("no body = %v", none)
	}
	if raw := auditJSON[spanOut](t, d, "/s/sess/audit/event/o1?q=needle", 200).Matches["raw"]; len(raw.Spans) != 1 || raw.Spans[0] != (auditSpan{1, 4, 10}) {
		t.Fatalf("raw body matches = %+v", raw)
	}
	plain := auditJSON[map[string]any](t, d, "/s/sess/audit/event/e1", 200)
	if _, has := plain["matches"]; has {
		t.Fatalf("an envelope without q carries matches: %v", plain["matches"])
	}
	// A loaded prefix reads only that much of the body (moved to a character
	// boundary): "needle again" is past byte 10.
	pre := auditJSON[spanOut](t, d, "/s/sess/audit/event/e1?q=needle&only=matches&through_out=10", 200)
	if o := pre.Matches["out"]; o.Through != 10 || len(o.Spans) != 1 {
		t.Fatalf("through_out=10: %+v", o)
	}
	if o := auditJSON[spanOut](t, d, "/s/sess/audit/event/e1?q=needle&only=matches&through_out=999", 200).Matches["out"]; o.Through != len(ev.Out) || len(o.Spans) != 2 {
		t.Fatalf("through_out=999: %+v", o)
	}
	for _, q := range []string{
		"q=", "q=%28&regex=1", "q=x&regex=2", "q=x&case=yes", "q=x&only=all", "q=x&through_out=-1", "q=x&through_in=z",
		"regex=1", "case=1", "only=matches", "through_out=3", "q=x&part=out", "q=x&q=y",
	} {
		auditJSON[map[string]string](t, d, "/s/sess/audit/event/e1?"+q, 400)
	}
	// The refusal text is the search's.
	bad := auditJSON[map[string]string](t, d, "/s/sess/audit/event/e1?q=%28&regex=1", 400)
	want := auditJSON[map[string]string](t, d, "/s/sess/audit/search?q=%28&regex=1", 400)
	if bad["error"] != want["error"] || !strings.Contains(bad["error"], "valid regular expression") {
		t.Fatalf("event %q, search %q", bad["error"], want["error"])
	}
	auditJSON[map[string]string](t, d, "/s/sess/audit/event/nope?q=x", 404)
}

// TestAuditEventSpansAreCapped pins that a body with more matches than the
// cap answers the first auditSpansMax and says so, on one line or many.
func TestAuditEventSpansAreCapped(t *testing.T) {
	for name, body := range map[string]string{
		"one line":   strings.Repeat("a", auditSpansMax+5),
		"many lines": strings.Repeat("a\n", auditSpansMax+5),
	} {
		t.Run(name, func(t *testing.T) {
			c := spanCase{body: body, head: -1, q: "a", cs: true}
			got := auditJSON[spanOut](t, spanFixture(c), "/s/sess/audit/event/e1?only=matches&"+c.params(), 200).Matches["out"]
			if len(got.Spans) != auditSpansMax || !got.Capped {
				t.Fatalf("%d spans, capped %v", len(got.Spans), got.Capped)
			}
		})
	}
	c := spanCase{body: strings.Repeat("a", auditSpansMax), head: -1, q: "a", cs: true}
	if got := auditJSON[spanOut](t, spanFixture(c), "/s/sess/audit/event/e1?only=matches&"+c.params(), 200).Matches["out"]; len(got.Spans) != auditSpansMax || got.Capped {
		t.Fatalf("exactly the cap: %d spans, capped %v", len(got.Spans), got.Capped)
	}
}
