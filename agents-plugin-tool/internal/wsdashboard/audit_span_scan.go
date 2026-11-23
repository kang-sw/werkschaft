package wsdashboard

import (
	"regexp"
	"regexp/syntax"
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
// returns true (reported). A match that reaches the right neighbour is not
// one: the window's matches end inside it.
//
// A window with a right neighbour (the head of a stored_trunc line) is scanned
// by boundedMatches, one pass over the window whatever the number of matches
// and whatever the pattern: a preferred alternative that runs to the
// sentinel and fails there, or a match that would take the sentinel, costs
// nothing more than any other step. A window that ends with its line has no
// right sentinel and nothing can reach it, so the matches are FindAll's, read
// in batches. The window is copied once; a resume reads the same text from
// the character before the resume point, which is the real context of the
// point (the left neighbour at the window's start), so a resume costs the
// distance to the next match, not the rest of the window.
func (m *seamMatcher) each(re *regexp.Regexp, line string, lo, hi int, emit func(start, end int) bool) bool {
	left, right := sentinelKind(line, lo-1), sentinelKind(line, hi)
	ls, rs := seamSentinels[left], seamSentinels[right]
	win := ls + line[lo:hi] + rs
	end := len(win) - len(rs) // the window's own text ends here
	delta := lo - len(ls)     // a window offset plus delta is a line offset
	if right != 0 {
		return m.boundedMatches(win, len(ls), end, delta, emit)
	}
	pos := len(ls)
	for pos < end {
		// t is the text from the character before pos (its context) on.
		c := 0
		if pos > 0 {
			_, w := utf8.DecodeLastRuneInString(win[:pos])
			c = pos - w
		}
		t, ctx := win[c:], pos-c
		l := re.FindStringIndex(t)
		if l == nil {
			return false
		}
		if l[0] >= ctx { // not through the context: the run is FindAll's
			locs := re.FindAllStringIndex(t, spanBatch)
			for _, l := range locs {
				if emit(c+l[0]+delta, c+l[1]+delta) {
					return true
				}
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
		// The match starts in the context character: the first match that
		// starts after it is the window's.
		start, stop, ok := m.shapeFirst(t, ctx > 0)
		if !ok {
			return false
		}
		m.stats.shape++
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
// context character (when hasCtx): the context, a lazy prefix and the
// pattern's group. The expression stops at the match, so a call costs the
// distance to it.
func (m *seamMatcher) shapeFirst(t string, hasCtx bool) (start, stop int, ok bool) {
	k := 0
	expr := `\A`
	if hasCtx {
		k = 1
		expr += `(?s:.)`
	}
	re := m.ctx[k]
	if re == nil {
		var err error
		if re, err = regexp.Compile(expr + `(?s:.*?)((?:` + m.pattern + `))`); err != nil {
			return 0, 0, false
		}
		m.ctx[k] = re
	}
	loc := re.FindStringSubmatchIndex(t)
	if loc == nil {
		return 0, 0, false
	}
	return loc[2], loc[3], true
}

// seamStats counts which way the scans went, for tests that pin the work of
// each branch: matches taken from the shape expression, scans of a window with
// a right neighbour by boundedMatches, and the positions it evaluated.
type seamStats struct {
	shape, bounded, rows int
}

// boundedProg is the pattern as a program for boundedMatches: the roots are
// the states a position is evaluated from (the start, and the target of each
// rune-consuming instruction).
type boundedProg struct {
	prog  *syntax.Prog
	roots []uint32
}

func newBoundedProg(pattern string) (*boundedProg, error) {
	tree, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	prog, err := syntax.Compile(tree.Simplify())
	if err != nil {
		return nil, err
	}
	b := &boundedProg{prog: prog, roots: []uint32{uint32(prog.Start)}}
	seen := map[uint32]bool{uint32(prog.Start): true}
	for _, in := range prog.Inst {
		switch in.Op {
		case syntax.InstRune, syntax.InstRune1, syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
			if !seen[in.Out] {
				seen[in.Out] = true
				b.roots = append(b.roots, in.Out)
			}
		}
	}
	return b, nil
}

// boundedMatches emits, from the character boundary from of win on, the
// matches of the window win[:end] as FindAll gives them when no match may
// reach beyond end: at each position the first match by the pattern's
// priority among those that end within the window, the leftmost position
// first; the scan resumes after a match, one character on after an empty one.
// win holds the sentinel of the right neighbour after end and that of the
// left neighbour at its start, so an assertion reads the real neighbours.
//
// It is the program run backwards over the window once: at each position it
// holds, for every state a thread can wait in after one character, the end of
// the first match from that state (or none), which is what the position
// before it needs. Work is the window times the program's size, however many
// matches the window holds, and the text is not scanned twice. A match that
// reaches a sentinel does not exist here, so none needs a retry.
func (m *seamMatcher) boundedMatches(win string, from, end, delta int, emit func(start, end int) bool) bool {
	if from >= end {
		return false
	}
	m.stats.bounded++
	b := m.bounded
	insts := b.prog.Inst
	// first[p-from] is the end of the match at p, -1 for none, -2 for a byte
	// that no character starts at (the window is decoded from from on).
	first := make([]int32, end-from+1)
	for i := range first {
		first[i] = -2
	}
	for p := from; p < end; {
		first[p-from] = -1
		_, w := utf8.DecodeRuneInString(win[p:end])
		p += w
	}
	first[end-from] = -1
	prev := make([]int32, len(insts)) // the row of the next character start
	cur := make([]int32, len(insts))
	seen := make([]int, len(insts))
	stamp := 0
	var p int
	var r rune
	var hasRune bool
	var flags syntax.EmptyOp
	var eval func(pc uint32) int32
	eval = func(pc uint32) int32 {
		if seen[pc] == stamp {
			return -1 // left or in progress: no match through it
		}
		seen[pc] = stamp
		in := &insts[pc]
		switch in.Op {
		case syntax.InstAlt, syntax.InstAltMatch:
			if v := eval(in.Out); v >= 0 {
				return v
			}
			return eval(in.Arg)
		case syntax.InstCapture, syntax.InstNop:
			return eval(in.Out)
		case syntax.InstEmptyWidth:
			if syntax.EmptyOp(in.Arg)&^flags != 0 {
				return -1
			}
			return eval(in.Out)
		case syntax.InstMatch:
			return int32(p)
		case syntax.InstRune:
			if hasRune && in.MatchRune(r) {
				return prev[in.Out]
			}
		case syntax.InstRune1:
			if hasRune && r == in.Rune[0] {
				return prev[in.Out]
			}
		case syntax.InstRuneAny:
			if hasRune {
				return prev[in.Out]
			}
		case syntax.InstRuneAnyNotNL:
			if hasRune && r != '\n' {
				return prev[in.Out]
			}
		}
		return -1
	}
	for p = end; p >= from; p-- {
		if first[p-from] == -2 {
			continue
		}
		m.stats.rows++
		r1, r2 := rune(-1), rune(-1)
		if p > 0 {
			r1 = edgeRune(win[p-1])
		}
		hasRune = p < end
		if hasRune {
			r, _ = utf8.DecodeRuneInString(win[p:end])
			r2 = r
		} else if end < len(win) {
			r2 = edgeRune(win[end])
		}
		flags = syntax.EmptyOpContext(r1, r2)
		for _, root := range b.roots {
			stamp++
			cur[root] = eval(root)
		}
		first[p-from] = cur[b.prog.Start]
		prev, cur = cur, prev
	}
	for pos := from; pos < end; {
		w := runeWidth(win[:end], pos)
		if e := int(first[pos-from]); e >= 0 {
			if emit(pos+delta, e+delta) {
				return true
			}
			if e == pos {
				pos += w
			} else {
				pos = e
			}
			continue
		}
		pos += w
	}
	return false
}

// edgeRune is the character an assertion reads for a neighbouring byte: the
// byte itself when it is ASCII (the only kind that is a word character or a
// line break), else some other character.
func edgeRune(b byte) rune {
	if b < utf8.RuneSelf {
		return rune(b)
	}
	return utf8.RuneError
}
