package wsdashboard

import (
	"fmt"
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// refWindowSpans is the reference the scan is held to: the nonempty matches of
// the window [lo, hi) of line, found the plain way. At each position, in
// order, a regexp anchored there (one context character before it, the
// window's sentinel after it) says whether some match ends inside the window;
// the first position that has one gives the match, and the search resumes after
// it (one character on after an empty one). It reads the rest of the window
// per position, so it is quadratic, and independent of the scan under test.
func refWindowSpans(t *testing.T, query, line string, lo, hi int) [][2]int {
	t.Helper()
	left, right := sentinelKind(line, lo-1), sentinelKind(line, hi)
	ls, rs := seamSentinels[left], seamSentinels[right]
	win := ls + line[lo:hi] + rs
	end := len(win) - len(rs)
	tail := `((?:` + query + `))(?s:.*)` + regexp.QuoteMeta(rs) + `\z`
	first := regexp.MustCompile(`\A` + tail)
	later := regexp.MustCompile(`\A(?s:.)` + tail)
	var out [][2]int
	for pos := len(ls); pos < end; {
		found := false
		for s := pos; s < end; {
			w := runeWidth(win[:end], s)
			re, text, base := first, win, 0
			if s > 0 {
				_, cw := utf8.DecodeLastRuneInString(win[:s])
				re, text, base = later, win[s-cw:], s-cw
			}
			if loc := re.FindStringSubmatchIndex(text); loc != nil {
				a, b := base+loc[2], base+loc[3]
				if b > a {
					out = append(out, [2]int{a + lo - len(ls), b + lo - len(ls)})
					pos = b
				} else {
					pos = a + w
				}
				found = true
				break
			}
			s += w
		}
		if !found {
			break
		}
	}
	return out
}

func refLineSpans(t *testing.T, query, line string, seam int) [][2]int {
	t.Helper()
	if seam < 0 {
		return refWindowSpans(t, query, line, 0, len(line))
	}
	return append(refWindowSpans(t, query, line, 0, seam), refWindowSpans(t, query, line, seam, len(line))...)
}

func scanLineSpans(s auditSearch, line string, seam int) [][2]int {
	var out [][2]int
	s.eachLineMatch(line, seam, func(start, end int) bool {
		if end > start {
			out = append(out, [2]int{start, end})
		}
		return false
	})
	return out
}

// genPattern builds a random pattern from atoms that read the context (the
// assertions), consume text in several ways, and loop, so the scan meets the
// shapes of every retry: a preferred alternative that runs to the sentinel,
// one that fails there, empty matches, nullable loops, and assertions on both
// sides of a seam.
func genPattern(r *rand.Rand, depth int) string {
	atoms := []string{`a`, `b`, `_`, ` `, `é`, `[ab]`, `[^a]`, `.`, `\w`, `\W`, `\s`, `\b`, `\B`, `^`, `$`, `\A`, `\z`, `(?i:a)`, `\p{L}`, `(?m:^)`, `(?m:$)`, `()`, `[a-c_]`, `\d`}
	quants := []string{``, ``, ``, `*`, `+`, `?`, `*?`, `+?`, `??`, `{1,2}`, `{2}`}
	atom := func() string {
		if depth > 0 && r.Intn(4) == 0 {
			alts := make([]string, 1+r.Intn(3))
			for i := range alts {
				alts[i] = genPattern(r, depth-1)
			}
			return `(?:` + strings.Join(alts, `|`) + `)`
		}
		return atoms[r.Intn(len(atoms))]
	}
	n := 1 + r.Intn(3)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(atom() + quants[r.Intn(len(quants))])
	}
	return b.String()
}

func genLine(r *rand.Rand) (string, int) {
	alphabet := []string{"a", "b", "a", "b", "_", " ", "é", "-", "A", "1"}
	var line strings.Builder
	for i, n := 0, r.Intn(14); i < n; i++ {
		line.WriteString(alphabet[r.Intn(len(alphabet))])
	}
	s := line.String()
	seam := -1
	if len(s) > 1 && r.Intn(5) != 0 {
		seam = 1 + r.Intn(len(s)-1)
		for seam < len(s) && !utf8.RuneStart(s[seam]) {
			seam++
		}
		if seam >= len(s) {
			seam = -1
		}
	}
	return s, seam
}

// TestAuditSpanScanAgreesWithPlainReference holds the scan to the plain
// reference over generated patterns with assertions, lines and seams: the
// nonempty spans are identical on every case, whichever side of the seam they
// lie on. The patterns include alternatives that reach the sentinel,
// preferred loops that fail at it, nullable loops and empty matches.
func TestAuditSpanScanAgreesWithPlainReference(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	patterns := []string{
		`\w+\b|.`, `a*\b|a`, `(?:a|b)+\b|b|a`, `\b\w`, `\ba\w?|a\B`, `\w?b\b`, `a\b`, `(a*)*\b`, `(|a)+\b`, `(?:\b)*a`,
		`\p{L}*`, `\w*`, `$`, `\b`, `^a|b$`, `.*\b|.`,
	}
	for len(patterns) < 260 {
		patterns = append(patterns, genPattern(r, 2))
	}
	cases := 0
	for _, q := range patterns {
		s, err := newAuditSearch(q, true, true, nil, "", 10)
		if err != nil || s.seamed == nil {
			continue // not valid, or no assertion: no sentinel window
		}
		for i := 0; i < 90; i++ {
			line, seam := genLine(r)
			cases++
			got, want := scanLineSpans(s, line, seam), refLineSpans(t, q, line, seam)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("pattern %q line %q seam %d:\n scan      %v\n reference %v", q, line, seam, got, want)
			}
		}
	}
	if cases < 10000 {
		t.Fatalf("only %d cases ran", cases)
	}
}

// sentinelCase is a line holding a stored_trunc seam and a pattern, built so
// the head window's preferred alternative reaches the right sentinel or fails
// there, whichever, over a long run.
type sentinelCase struct {
	name, q string
}

// sentinelShapes are the patterns whose head window used to be scanned again
// from each short match: each has a preferred alternative that runs through the
// whole rest of a run of word characters and either takes the sentinel (a word
// character, the real neighbour of the seam) or fails on it, leaving a short
// alternative.
var sentinelShapes = []sentinelCase{
	{"word-boundary loop takes the sentinel", `\w+\b|.`},
	{"same with a second word branch", `\w+\b|\w`},
	{"character class loop", `[a-z_]+\b|.`},
	{"left context in the loop", `\B\w+\b|.`},
	{"left assertion in front", `(?:\b|a)\w+\b|a`},
	{"loop that fails at the sentinel", `a*\b|a`},
	{"alternation loop that fails at the sentinel", `(?:a|b)+\b|b|a`},
	{"loop ending on end of text", `\w+\z|.`},
	{"lazy loop", `a+?\b|a`},
	{"nullable loop", `(a*)*\b|a`},
}

// TestAuditSpanSentinelRetryIsLinear pins that a head window whose neighbour
// across the seam is a word character costs one pass, whatever the number of
// short matches in it. The body is a run of N word characters with the seam
// at N, so the head's preferred alternative reaches (or fails on) the
// sentinel at every position. The scan evaluates each position of the window
// once; the old retry read the rest of the window per match (1.2 s at 8,000,
// 5 s at 16,000), which the generous time bound also rules out. The spans are
// the reference's at a small size, and the larger sizes give the same ones
// shifted: one per position of the run.
func TestAuditSpanSentinelRetryIsLinear(t *testing.T) {
	for _, tc := range sentinelShapes {
		t.Run(tc.name, func(t *testing.T) {
			// The reference at a small size fixes the spans.
			small := strings.Repeat("a", 200) + "a"
			ref := refLineSpans(t, tc.q, small, 200)
			if len(ref) < 100 {
				t.Fatalf("reference gave %d spans; the case does not exercise the run", len(ref))
			}
			for _, n := range []int{5000, 9000} {
				body := strings.Repeat("a", n) + "a"
				c := spanCase{body: body, head: n, q: tc.q, regex: true, cs: true}
				s, err := newAuditSearch(c.q, true, true, nil, "", 10)
				if err != nil || s.seamed == nil {
					t.Fatalf("search: %v", err)
				}
				start := time.Now()
				res := s.bodySpans(body, n, len(body))
				if d := time.Since(start); d > time.Second {
					t.Errorf("n=%d: spans took %v", n, d)
				}
				st := s.seamed.stats
				// The head is one pass; the tail ("a") is a window of its own with
				// no right neighbour, read by one shape match at most.
				if st.bounded != 1 || st.shape > 1 || st.rows > n+1 {
					t.Errorf("n=%d: stats %+v: want one pass of at most %d positions and no retry", n, st, n+1)
				}
				if res.Capped || len(res.Spans) != len(ref)+n-200 {
					t.Fatalf("n=%d: %d spans (capped %v), want %d", n, len(res.Spans), res.Capped, len(ref)+n-200)
				}
				// The run's spans are the reference's, one per position; the
				// last is the tail's own.
				for i, sp := range res.Spans {
					if sp.Line != 1 || sp.End <= sp.Start || (i > 0 && sp.Start < res.Spans[i-1].End) || sp.End > len(body) {
						t.Fatalf("n=%d: span %d = %v", n, i, sp)
					}
				}
			}
		})
	}
}

// TestAuditSpanTailWindowRetryIsLinear pins the window that ends with its line
// and starts after a seam: nothing can reach a right sentinel there, the
// left neighbour is context only, and a match that starts in the context
// character is replaced by the first one after it, each such read costing the
// distance to the match. Many such matches cost a read each, and not a rescan
// of the window (the scan took the shape expression once per match).
func TestAuditSpanTailWindowRetryIsLinear(t *testing.T) {
	for _, q := range []string{`\b\w`, `\ba|a\b|a`, `\Ba`} {
		t.Run(q, func(t *testing.T) {
			n := 20000
			body := "x" + strings.Repeat("aa ", n)
			s, err := newAuditSearch(q, true, true, nil, "", 10)
			if err != nil || s.seamed == nil {
				t.Fatalf("search: %v", err)
			}
			start := time.Now()
			res := s.bodySpans(body, 1, len(body))
			if d := time.Since(start); d > time.Second {
				t.Errorf("spans took %v", d)
			}
			// The head "x" is a window of two positions; the tail is read by
			// the shape expression once per span at most.
			if st := s.seamed.stats; st.bounded != 1 || st.rows > 2 || st.shape > len(res.Spans)+1 {
				t.Errorf("stats %+v for %d spans", st, len(res.Spans))
			}
			if len(res.Spans) < 1000 {
				t.Fatalf("only %d spans", len(res.Spans))
			}
		})
	}
}

// TestAuditSpanBoundedWindowEdges pins the window forms of the bounded scan
// that the generated cases reach rarely: an empty head, a seam inside a
// multi-byte character's neighbourhood, a head that is one character, and the
// cap with a long head, each equal to the reference.
func TestAuditSpanBoundedWindowEdges(t *testing.T) {
	for _, tc := range []struct {
		q, line string
		seam    int
	}{
		{`\w+\b|.`, "ab", 1},
		{`\w+\b|.`, "é", 2},
		{`\w+\b|.`, "éa", 2},
		{`\b.`, "a é", 3},
		{`$|a`, "aaa", 2},
		{`\z|b`, "ab", 1},
		{`(?m:$)|a`, "ab", 1},
		{`\B|a`, "ab", 1},
		{`😀?\b`, "a😀b", 5},
	} {
		s, err := newAuditSearch(tc.q, true, true, nil, "", 10)
		if err != nil || s.seamed == nil {
			t.Fatalf("%q: %v", tc.q, err)
		}
		if got, want := scanLineSpans(s, tc.line, tc.seam), refLineSpans(t, tc.q, tc.line, tc.seam); !reflect.DeepEqual(got, want) {
			t.Errorf("%q on %q seam %d: scan %v, reference %v", tc.q, tc.line, tc.seam, got, want)
		}
	}
	// The cap on a long head: the answer holds the first spans and says so.
	body := strings.Repeat("a", 3*auditSpansMax) + "a"
	s, _ := newAuditSearch(`\w+\b|.`, true, true, nil, "", 10)
	res := s.bodySpans(body, 3*auditSpansMax, len(body))
	if len(res.Spans) != auditSpansMax || !res.Capped || s.seamed.stats.rows > 3*auditSpansMax+1 {
		t.Fatalf("%d spans, capped %v, stats %+v", len(res.Spans), res.Capped, s.seamed.stats)
	}
	if got := fmt.Sprint(res.Spans[auditSpansMax-1]); got != fmt.Sprint(auditSpan{1, auditSpansMax - 1, auditSpansMax}) {
		t.Fatalf("last span %s", got)
	}
}
