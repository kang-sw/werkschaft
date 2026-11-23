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
	if s.spanScan == nil {
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
	scan := s.spanScan
	scan.text, scan.left, scan.right = line[lo:hi], -1, -1
	defer scan.reset()
	if s.seamed != nil {
		if lo > 0 {
			scan.left = rune(seamSentinels[sentinelKind(line, lo-1)][0])
		}
		if hi < len(line) {
			scan.right = rune(seamSentinels[sentinelKind(line, hi)][0])
		}
	}
	for start := 0; start < len(scan.text); {
		scan.releaseBefore(start / 64)
		end := scan.findAt(start)
		if end > start {
			if emit(lo+start, lo+end) {
				return true
			}
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

// A greedy loop often defers the same alternative at each rune. Keep a
// sequence of evenly spaced positions as one stack entry.
type spanBranch struct {
	spanState
	count, step int
}

type spanScan struct {
	prog        *syntax.Prog
	text        string
	left, right rune // assertion context outside text, never consumed

	// One word per reached instruction for every 64 bytes of lookahead.
	// Keep a spare page for the next line or the next step of a short scan.
	pages    []map[uint32]uint64
	spare    map[uint32]uint64
	released int
	atStart  []uint64
	pending  []spanBranch
}

func (s *spanScan) releaseBefore(page int) {
	for ; s.released < min(page, len(s.pages)); s.released++ {
		if s.spare == nil && s.pages[s.released] != nil {
			s.spare = s.pages[s.released]
			clear(s.spare)
		}
		s.pages[s.released] = nil
	}
}

func (s *spanScan) reset() {
	s.releaseBefore(len(s.pages))
	s.pages = s.pages[:0]
	s.released = 0
	s.text = ""
}

func (s *spanScan) seen(state spanState, start int) bool {
	if state.pos == start {
		word, bit := state.pc/64, uint64(1)<<(state.pc%64)
		if s.atStart[word]&bit != 0 {
			return true
		}
		s.atStart[word] |= bit
		return false
	}
	page, bit := state.pos/64, uint64(1)<<(state.pos%64)
	for len(s.pages) <= page {
		s.pages = append(s.pages, nil)
	}
	visited := s.pages[page]
	if visited == nil {
		visited, s.spare = s.spare, nil
		if visited == nil {
			visited = make(map[uint32]uint64)
		}
		s.pages[page] = visited
	}
	if visited[state.pc]&bit != 0 {
		return true
	}
	visited[state.pc] |= bit
	return false
}

func (s *spanScan) push(state spanState) {
	if n := len(s.pending); n > 0 {
		top := &s.pending[n-1]
		if top.pc == state.pc {
			step := state.pos - top.pos
			if top.count == 1 || top.step == step {
				top.pos, top.step = state.pos, step
				top.count++
				return
			}
		}
	}
	s.pending = append(s.pending, spanBranch{spanState: state, count: 1})
}

func (s *spanScan) pop() spanState {
	last := len(s.pending) - 1
	top := &s.pending[last]
	state := top.spanState
	top.count--
	if top.count == 0 {
		s.pending = s.pending[:last]
	} else {
		top.pos -= top.step
	}
	return state
}

// findAt tries the preferred branches first, as regexp's bounded backtracker
// does. States reached strictly beyond a winning end have already failed.
// Start each attempt with fresh bits at its own start so states on the last
// match's end can be retried. Captures cannot affect a later decision.
func (s *spanScan) findAt(start int) int {
	if s.atStart == nil {
		s.atStart = make([]uint64, (len(s.prog.Inst)+63)/64)
	}
	clear(s.atStart)
	s.pending = s.pending[:0]
	s.push(spanState{pc: uint32(s.prog.Start), pos: start})
nextBranch:
	for len(s.pending) > 0 {
		state := s.pop()
		for !s.seen(state, start) {
			inst := &s.prog.Inst[state.pc]
			switch inst.Op {
			case syntax.InstMatch:
				return state.pos
			case syntax.InstFail:
				continue nextBranch
			case syntax.InstAlt, syntax.InstAltMatch:
				s.push(spanState{pc: inst.Arg, pos: state.pos})
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
