package wsdashboard

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// spanBatch is how many matches one regexp call reads ahead while a window is
// scanned, so a scan that stops early (the span cap) has materialized only a
// bounded number of matches beyond the ones it used.
const spanBatch = 64

// eachLineMatch calls emit with each match of the search on one stored line,
// in order, as byte offsets of the line, until emit returns true. Matches
// follow Go's FindAll (empty ones included, an empty match advances by one
// rune) under the rules of matchLine: none consumes text from both sides of
// the seam (seam >= 0). Without assertions the head and the tail are matched
// apart; with them, each side is matched as a window of the whole line. The
// work is linear in the line: no text is scanned twice, and the scan ends
// with emit.
func (s auditSearch) eachLineMatch(line string, seam int, emit func(start, end int) bool) {
	if seam < 0 {
		s.eachWindowMatch(line, 0, len(line), emit)
		return
	}
	if !s.eachWindowMatch(line, 0, seam, emit) {
		s.eachWindowMatch(line, seam, len(line), emit)
	}
}

// eachWindowMatch is eachLineMatch over the window [lo, hi) of line; it
// reports whether emit stopped the scan.
func (s auditSearch) eachWindowMatch(line string, lo, hi int, emit func(start, end int) bool) bool {
	switch {
	case s.re == nil: // a case-sensitive literal
		if s.lit == "" {
			return false
		}
		for at := lo; at < hi; {
			i := strings.Index(line[at:hi], s.lit)
			if i < 0 {
				return false
			}
			if emit(at+i, at+i+len(s.lit)) {
				return true
			}
			at += i + len(s.lit)
		}
		return false
	case s.seamed != nil:
		return s.seamed.each(s.re, line, lo, hi, emit)
	}
	// No assertion reads the text around a position, so the scan resumes on
	// the rest of the window without context.
	text := line[lo:hi]
	for pos := 0; pos <= len(text); {
		locs := s.re.FindAllStringIndex(text[pos:], spanBatch)
		for _, l := range locs {
			if emit(lo+pos+l[0], lo+pos+l[1]) {
				return true
			}
		}
		if len(locs) < spanBatch {
			return false
		}
		last := locs[len(locs)-1]
		pos += last[1]
		if last[0] == last[1] {
			pos += runeWidth(text, pos)
		}
	}
	return false
}

// runeWidth is the width in bytes of the character at s[at:], 1 past the end.
func runeWidth(s string, at int) int {
	if at >= len(s) {
		return 1
	}
	_, w := utf8.DecodeRuneInString(s[at:])
	return max(w, 1)
}

// each calls emit with every non-overlapping match of re (the pattern's plain
// expression) inside the window [lo, hi) of line, in order, as Go's FindAll
// gives them on the window with its neighbours as sentinels, until emit
// returns true (reported). A match that touches a sentinel (leftmost-first
// took an alternative reaching out of the window) is not one: the first match
// by shape (see shapeFirst) is taken instead and the scan resumes after it.
//
// The window is copied once; a resume reads the same text from the character
// before the resume point, which is the real context of the point, so a
// resume costs the distance to the next match, not the rest of the window.
func (m *seamMatcher) each(re *regexp.Regexp, line string, lo, hi int, emit func(start, end int) bool) bool {
	left, right := sentinelKind(line, lo-1), sentinelKind(line, hi)
	ls, rs := seamSentinels[left], seamSentinels[right]
	win := ls + line[lo:hi] + rs
	end := len(win) - len(rs) // the window's own text ends here
	delta := lo - len(ls)     // a window offset plus delta is a line offset
	pos := len(ls)
	shape := false // the next match comes from shapeFirst
	for pos < end {
		// t is the text from the character before pos (its context) on.
		c := 0
		if pos > 0 {
			_, w := utf8.DecodeLastRuneInString(win[:pos])
			c = pos - w
		}
		t, ctx, limit := win[c:], pos-c, end-c
		if !shape {
			l := re.FindStringIndex(t)
			if l == nil {
				return false
			}
			if l[0] >= ctx { // not through the context: the run is FindAll's
				locs := re.FindAllStringIndex(t, spanBatch)
				touched := false
				for _, l := range locs {
					if l[1] > limit { // reaches the right sentinel: only the last can
						pos, shape, touched = c+l[0], true, true
						break
					}
					if emit(c+l[0]+delta, c+l[1]+delta) {
						return true
					}
				}
				if touched {
					continue
				}
				if len(locs) < spanBatch {
					return false
				}
				last := locs[len(locs)-1]
				pos = c + last[1]
				if last[0] == last[1] {
					pos += runeWidth(win[:end], pos)
				}
				continue
			}
		}
		shape = false
		start, stop, ok := m.shapeFirst(t, ctx > 0, right, limit)
		if !ok {
			return false
		}
		if emit(c+start+delta, c+stop+delta) {
			return true
		}
		pos = c + stop
		if start == stop {
			pos += runeWidth(win[:end], pos)
		}
	}
	return false
}

// shapeFirst is the first match of the pattern in t that starts after its
// context character (when hasCtx) and ends within limit, as the first match
// of a window whose neighbours are those of t. It tries the expression
// without the right bound first, which stops at the match; only a match that
// reaches the right sentinel needs the bounded form, which reads the rest of
// the text.
func (m *seamMatcher) shapeFirst(t string, hasCtx bool, right, limit int) (int, int, bool) {
	re := m.shape(hasCtx, right, false)
	if re == nil {
		return 0, 0, false
	}
	loc := re.FindStringSubmatchIndex(t)
	if loc == nil {
		return 0, 0, false
	}
	if loc[3] > limit {
		if re = m.shape(hasCtx, right, true); re == nil {
			return 0, 0, false
		}
		if loc = re.FindStringSubmatchIndex(t); loc == nil {
			return 0, 0, false
		}
	}
	return loc[2], loc[3], true
}

// shape is the expression for text opening with one context character
// (hasCtx) and closing with the right sentinel of the given kind: the
// context, a lazy prefix, the pattern's group, and with bounded the rest and
// the sentinel, which keeps the group off the sentinel.
func (m *seamMatcher) shape(hasCtx bool, right int, bounded bool) *regexp.Regexp {
	k := [3]int{0, right, 0}
	expr := `\A`
	if hasCtx {
		k[0] = 1
		expr += `(?s:.)`
	}
	expr += `(?s:.*?)((?:` + m.pattern + `))`
	if bounded {
		k[2] = 1
		expr += `(?s:.*)` + regexp.QuoteMeta(seamSentinels[right]) + `\z`
	}
	if re := m.ctx[k]; re != nil {
		return re
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil
	}
	m.ctx[k] = re
	return re
}
