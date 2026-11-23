package wsdashboard

import (
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// eachLineMatch calls emit with the nonempty matches on one stored line,
// in order, as byte offsets, until emit returns true. A stored seam separates
// the windows for consumption while assertions can still see their neighbours.
func (s auditSearch) eachLineMatch(line string, seam int, emit func(start, end int) bool) {
	if seam < 0 {
		s.eachWindowMatch(line, 0, len(line), emit)
		return
	}
	if !s.eachWindowMatch(line, 0, seam, emit) {
		s.eachWindowMatch(line, seam, len(line), emit)
	}
}

// eachWindowMatch reports whether emit stopped the scan.
func (s auditSearch) eachWindowMatch(line string, lo, hi int, emit func(start, end int) bool) bool {
	if s.spanProg == nil {
		if s.lit == "" {
			return false
		}
		for at := lo; at < hi; {
			i := strings.Index(line[at:hi], s.lit)
			if i < 0 {
				return false
			}
			start := at + i
			at = start + len(s.lit)
			if emit(start, at) {
				return true
			}
		}
		return false
	}
	scan := spanScan{
		prog: s.spanProg, text: line[lo:hi],
		left: -1, right: -1, words: (len(s.spanProg.Inst) + 63) / 64,
	}
	if s.seamed != nil {
		if lo > 0 {
			scan.left = rune(seamSentinels[sentinelKind(line, lo-1)][0])
		}
		if hi < len(line) {
			scan.right = rune(seamSentinels[sentinelKind(line, hi)][0])
		}
	}
	released := 0
	for start := 0; start < len(scan.text); {
		for released < start/spanScanPage {
			scan.visited[released] = nil
			released++
		}
		end := scan.findAt(start)
		if end > start {
			if emit(lo+start, lo+end) {
				return true
			}
			// Paths still in flight when the match won were at or before
			// end. All states visited beyond end have already failed, so
			// the next search can reuse them. Retry the states at end.
			clear(scan.row(end))
			start = end
		} else {
			// Both a miss and an empty match advance by one rune.
			_, width := utf8.DecodeRuneInString(scan.text[start:])
			start += width
		}
	}
	return false
}

type spanState struct {
	pc  uint32
	pos int
}

const spanScanPage = 256

type spanScan struct {
	prog        *syntax.Prog
	text        string
	left, right rune // assertion context outside text, never consumed
	words       int  // bitset words per byte position
	visited     [][]uint64
	pending     []spanState
}

// row allocates visited bits only as the matcher reaches new pages. A scan
// that stops at the span cap does not build a table for the remaining line.
func (s *spanScan) row(pos int) []uint64 {
	page := pos / spanScanPage
	if page == len(s.visited) {
		s.visited = append(s.visited, make([]uint64, spanScanPage*s.words))
	}
	offset := (pos % spanScanPage) * s.words
	return s.visited[page][offset : offset+s.words]
}

// findAt tries the program's preferred branches first, as regexp's bounded
// backtracker does. A state is visited at most once until that byte position
// becomes a match end. Captures cannot affect a later decision.
func (s *spanScan) findAt(start int) int {
	s.pending = append(s.pending[:0], spanState{pc: uint32(s.prog.Start), pos: start})
nextBranch:
	for len(s.pending) > 0 {
		last := len(s.pending) - 1
		state := s.pending[last]
		s.pending = s.pending[:last]
		for {
			row := s.row(state.pos)
			word, bit := state.pc/64, uint64(1)<<(state.pc%64)
			if row[word]&bit != 0 {
				continue nextBranch
			}
			row[word] |= bit

			inst := &s.prog.Inst[state.pc]
			switch inst.Op {
			case syntax.InstMatch:
				return state.pos
			case syntax.InstFail:
				continue nextBranch
			case syntax.InstAlt, syntax.InstAltMatch:
				s.pending = append(s.pending, spanState{pc: inst.Arg, pos: state.pos})
			case syntax.InstEmptyWidth:
				prev, next := s.left, s.right
				if state.pos > 0 {
					prev, _ = utf8.DecodeLastRuneInString(s.text[:state.pos])
				}
				if state.pos < len(s.text) {
					next, _ = utf8.DecodeRuneInString(s.text[state.pos:])
				}
				want := syntax.EmptyOp(inst.Arg)
				if syntax.EmptyOpContext(prev, next)&want != want {
					continue nextBranch
				}
			case syntax.InstRune, syntax.InstRune1, syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
				if state.pos == len(s.text) {
					continue nextBranch
				}
				r, width := utf8.DecodeRuneInString(s.text[state.pos:])
				if !spanRuneMatches(inst, r) {
					continue nextBranch
				}
				state.pos += width
			}
			state.pc = inst.Out
		}
	}
	return -1
}

func spanRuneMatches(inst *syntax.Inst, r rune) bool {
	switch inst.Op {
	case syntax.InstRune1:
		return r == inst.Rune[0]
	case syntax.InstRuneAny:
		return true
	case syntax.InstRuneAnyNotNL:
		return r != '\n'
	default:
		return inst.MatchRune(r)
	}
}
