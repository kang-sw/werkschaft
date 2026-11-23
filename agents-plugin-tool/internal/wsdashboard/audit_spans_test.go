package wsdashboard

import (
	"fmt"
	"math/rand"
	"net/url"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// spansOf answers the event route's match spans for one case.
func spansOf(t *testing.T, c spanCase) (out struct {
	Through int
	Spans   []auditSpan
	Capped  bool
}) {
	t.Helper()
	got := auditJSON[spanOut](t, spanFixture(c), "/s/sess/audit/event/e1?only=matches&"+c.params(), 200).Matches[wsaudit.FieldOut]
	out.Through, out.Spans, out.Capped = got.Through, got.Spans, got.Capped
	return out
}

// TestAuditEventSpansBoundaryMatchesOnRetainedSideAreBounded pins that many
// assertion matches on a retained side of a seam line cost linear work: a
// word character before the tail window makes the first match of every resume
// touch the window's sentinel, which once rescanned the rest of the window per
// word. The spans equal the words' starts, whose offsets are known by
// construction; the time bound is generous (the quadratic form took seconds).
func TestAuditEventSpansBoundaryMatchesOnRetainedSideAreBounded(t *testing.T) {
	for name, tc := range map[string]struct {
		words  int
		capped bool
	}{"below the cap": {9000, false}, "past the cap": {auditSpansMax + 500, true}} {
		t.Run(name, func(t *testing.T) {
			// "x" is the head; the tail window opens on a word character
			// whose left neighbour is a word character, so its first word is
			// not at a boundary.
			c := spanCase{body: "x" + strings.Repeat("ab ", tc.words), head: 1, q: `\b\w`, regex: true, cs: true}
			start := time.Now()
			got := spansOf(t, c)
			if d := time.Since(start); d > 3*time.Second {
				t.Errorf("spans took %v", d)
			}
			// The head "x" is a word of its own; the tail's first word is
			// not at a boundary, every later one is.
			want := min(tc.words, auditSpansMax)
			if len(got.Spans) != want || got.Capped != tc.capped {
				t.Fatalf("%d spans (want %d), capped %v", len(got.Spans), want, got.Capped)
			}
			for i, sp := range got.Spans {
				w := auditSpan{1, 1 + 3*i, 2 + 3*i}
				if i == 0 {
					w = auditSpan{1, 0, 1}
				}
				if sp != w {
					t.Fatalf("span %d = %v, want %v", i, sp, w)
				}
			}
		})
	}
}

// TestAuditEventSpanCapCountsVisibleSpans pins that empty matches never count
// toward the cap: a long run of them before a visible match leaves the match
// and no capped mark, and empty matches between visible ones do not shorten
// the answer.
func TestAuditEventSpanCapCountsVisibleSpans(t *testing.T) {
	got := spansOf(t, spanCase{body: strings.Repeat("1", auditSpansMax+1) + "x", head: -1, q: `\p{L}*`, regex: true, cs: true})
	if want := []auditSpan{{1, auditSpansMax + 1, auditSpansMax + 2}}; !reflect.DeepEqual(got.Spans, want) || got.Capped {
		t.Fatalf("spans %v capped %v, want %v uncapped", got.Spans, got.Capped, want)
	}
	// The same on both sides of a seam, with an assertion in the pattern.
	got = spansOf(t, spanCase{body: strings.Repeat("1", auditSpansMax+1) + " x" + strings.Repeat("1", 50), head: auditSpansMax + 20, q: `\b\p{L}*`, regex: true, cs: true})
	if want := []auditSpan{{1, auditSpansMax + 2, auditSpansMax + 3}}; got.Capped || !reflect.DeepEqual(got.Spans, want) {
		t.Fatalf("seam: spans %v capped %v", got.Spans, got.Capped)
	}
	// Visible spans fill the cap with empty matches between them.
	got = spansOf(t, spanCase{body: strings.Repeat("a1", auditSpansMax+5), head: -1, q: `a?`, regex: true, cs: true})
	if len(got.Spans) != auditSpansMax || !got.Capped {
		t.Fatalf("%d spans, capped %v", len(got.Spans), got.Capped)
	}
}

// TestAuditEventSpansEmptyMatchAtWindowEnd pins that an empty match at the end
// of a seam window (an assertion or a star that matches nothing there) marks
// nothing and does not disturb the window's other matches.
func TestAuditEventSpansEmptyMatchAtWindowEnd(t *testing.T) {
	for _, q := range []string{`b*`, `\b`, `$`, `\w*`} {
		got := spansOf(t, spanCase{body: "ab cd", head: 2, q: q, regex: true, cs: true})
		if len(got.Spans) > 2 {
			t.Errorf("%s: spans %v", q, got.Spans)
		}
	}
	got := spansOf(t, spanCase{body: "ab cd", head: 2, q: `\w*`, regex: true, cs: true})
	if want := []auditSpan{{1, 0, 2}, {1, 3, 5}}; !reflect.DeepEqual(got.Spans, want) {
		t.Fatalf("spans %v, want %v", got.Spans, want)
	}
}

// TestAuditEventSpansStayWithinTheReturnedPrefix pins that with no through_*
// the spans cover exactly the prefix the envelope returns: a character
// crossing the implicit cutoff is clipped from both, so a span lies inside
// the returned text and one ending just before the character is kept.
func TestAuditEventSpansStayWithinTheReturnedPrefix(t *testing.T) {
	body := strings.Repeat("a", auditBodyMax-4) + "end" + "😀" + "tail"
	if body[auditBodyMax-1:auditBodyMax+3] != "😀" {
		t.Fatal("the character does not cross the cutoff")
	}
	for _, tc := range []struct {
		q     string
		spans []auditSpan
	}{
		{"end", []auditSpan{{1, auditBodyMax - 4, auditBodyMax - 1}}},
		{"😀", nil},
		{"tail", nil},
	} {
		c := spanCase{body: body, head: -1, q: tc.q, cs: true}
		d := spanFixture(c)
		raw := auditJSON[map[string]any](t, d, "/s/sess/audit/event/e1?"+c.params(), 200)
		if got := len(raw["out"].(string)); got != auditBodyMax-1 {
			t.Fatalf("envelope out = %d bytes, want %d", got, auditBodyMax-1)
		}
		got := spansOf(t, c)
		if got.Through != auditBodyMax-1 {
			t.Fatalf("%q: through %d, want %d", tc.q, got.Through, auditBodyMax-1)
		}
		if !reflect.DeepEqual(append([]auditSpan{}, got.Spans...), append([]auditSpan{}, tc.spans...)) {
			t.Errorf("%q: spans %v, want %v", tc.q, got.Spans, tc.spans)
		}
	}
}

// TestAuditSpanScanWork checks the full scan below and above the response cap.
// In each case a more preferred branch looks far ahead before losing to ".".
func TestAuditSpanScanWork(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		seamed  bool
	}{
		{"\\w+\\b|.", true},
		{"\\B\\w+\\b|.", true},
		{"a+z\\b|.", false},
		{"a+z|.", false},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			s, err := newAuditSearch(tc.pattern, true, true, nil, "", 1)
			if err != nil {
				t.Fatal(err)
			}
			run := func(n int) time.Duration {
				line, seam := strings.Repeat("a", n+1), -1
				if tc.seamed {
					seam = n
				}
				best := time.Duration(1<<63 - 1)
				for range 3 {
					count := 0
					started := time.Now()
					s.eachLineMatch(line, seam, func(start, end int) bool {
						if start != count || end != count+1 {
							t.Fatalf("match %d = [%d,%d)", count, start, end)
						}
						count++
						return false
					})
					best = min(best, time.Since(started))
					if count != len(line) {
						t.Fatalf("got %d matches, want %d", count, len(line))
					}
				}
				return best
			}
			small, large := run(8000), run(16000)
			t.Logf("8k: %v, 16k: %v", small, large)
			if float64(large) > 3.3*float64(small) {
				t.Errorf("doubling the line grew scan time from %v to %v", small, large)
			}
		})
	}
}

func TestAuditSpanScanStopsAtResponseCap(t *testing.T) {
	s, err := newAuditSearch(".", true, true, nil, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	run := func(n int) time.Duration {
		line := strings.Repeat("a", n)
		best := time.Duration(1<<63 - 1)
		for range 3 {
			started := time.Now()
			got := s.bodySpans(line, -1, len(line))
			best = min(best, time.Since(started))
			if !got.Capped || len(got.Spans) != auditSpansMax || got.Spans[auditSpansMax-1] != (auditSpan{1, auditSpansMax - 1, auditSpansMax}) {
				t.Fatalf("size %d: got %d spans, capped %v", n, len(got.Spans), got.Capped)
			}
		}
		return best
	}
	short, long := run(auditSpansMax+1), run(100*auditSpansMax)
	t.Logf("near cap: %v, 1M: %v", short, long)
	if long > 5*short {
		t.Errorf("lengthening the unseen suffix grew scan time from %v to %v", short, long)
	}
}

func TestAuditSpanScanScratchAllocation(t *testing.T) {
	const mib = 1 << 20
	for _, tc := range []struct {
		name, pattern, body string
		wantSpans, maxBytes int
	}{
		{"long lookahead", "(?:a+z|.)(?:b{1000})?", strings.Repeat("a", mib), auditSpansMax, 12 * mib},
		{"short lines", "az", strings.Repeat("a\n", mib/2), 0, mib},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := newAuditSearch(tc.pattern, true, true, nil, "", 1)
			if err != nil {
				t.Fatal(err)
			}
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			got := s.bodySpans(tc.body, -1, len(tc.body))
			runtime.ReadMemStats(&after)
			bytes := after.TotalAlloc - before.TotalAlloc
			t.Logf("allocated %d bytes", bytes)
			if bytes > uint64(tc.maxBytes) {
				t.Errorf("allocated %d bytes, want at most %d", bytes, tc.maxBytes)
			}
			if len(got.Spans) != tc.wantSpans || got.Capped != (tc.wantSpans == auditSpansMax) {
				t.Errorf("got %d spans, capped %v", len(got.Spans), got.Capped)
			}
		})
	}
}

func TestAuditSpanScanAgreesWithRegexp(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"a", "b", "_", " ", "\x00", "\xff", "\u00e9", "\U0001f642"}
	patterns := []string{
		".", "(?i:a)+", "a+?b|a", "a.*b|a", "a+z|.", "a*",
		"(a?)*", "(a*|b)+?", "(?:|a)+b|a", "((a)|(b))*", "(?:a??|b?)*a",
		"^|.", "$|a", "\\A.*\\z", "\\w+\\b|.", "\\B\\w+\\b|.",
		"\\ba\\w?|a\\B", "\\w?b\\b", "\\B[a_]+|.",
		"(?:\\b|a)*?b", "\\b\\p{L}*", "\\p{L}*|.", "\\p{So}+",
	}
	for range 24 {
		a, b := patterns[rng.Intn(len(patterns))], patterns[rng.Intn(len(patterns))]
		patterns = append(patterns, "(?:"+a+"|"+b+")"+[]string{"", "?", "*", "+?"}[rng.Intn(4)])
	}
	lines := []string{"", "a", "a b", "abac", strings.Repeat("ab", 130) + "a"}
	for range 48 {
		var line strings.Builder
		for range rng.Intn(24) {
			line.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		lines = append(lines, line.String())
	}
	for _, pattern := range patterns {
		s, err := newAuditSearch(pattern, true, true, nil, "", 1)
		if err != nil {
			t.Fatal(err)
		}
		re := regexp.MustCompile(pattern)
		for _, line := range lines {
			for _, seam := range []int{-1, len(line) / 3, len(line) / 2} {
				var got [][2]int
				s.eachLineMatch(line, seam, func(start, end int) bool {
					got = append(got, [2]int{start, end})
					return false
				})
				want := referenceAuditSpanScan(s.seamed, re, line, seam)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%q on %q at seam %d: got %v, want %v", pattern, line, seam, got, want)
				}
			}
		}
	}
}

// Use the ordinary regexp engine and, on a seamed assertion, the search
// matcher's bounded regexp for each resume. Only the short fixtures use this
// intentionally repetitive reference.
func referenceAuditSpanScan(m *seamMatcher, re *regexp.Regexp, line string, seam int) (spans [][2]int) {
	window := func(lo, hi int) {
		if m == nil || seam < 0 {
			for _, match := range re.FindAllStringIndex(line[lo:hi], -1) {
				if match[0] < match[1] {
					spans = append(spans, [2]int{lo + match[0], lo + match[1]})
				}
			}
			return
		}
		for lo < hi {
			start, end, ok := m.first(line, lo, hi)
			if !ok {
				return
			}
			lo = end
			if start < end {
				spans = append(spans, [2]int{start, end})
			} else {
				_, width := utf8.DecodeRuneInString(line[end:hi])
				lo += width
			}
		}
	}
	if seam < 0 {
		window(0, len(line))
	} else {
		window(0, seam)
		window(seam, len(line))
	}
	return spans
}
