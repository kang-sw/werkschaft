package wsdashboard

import (
	"regexp/syntax"
	"slices"
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
	current  []uint32
	next     []uint32
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

func (s *spanScan) seen(pc uint32, pos, start int) bool {
	if pos == start {
		word, bit := pc/64, uint64(1)<<(pc%64)
		if s.atStart[word]&bit != 0 {
			return true
		}
		s.atStart[word] |= bit
		return false
	}
	page, bit := pos/64, uint64(1)<<(pos%64)
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
	if visited[pc]&bit != 0 {
		return true
	}
	visited[pc] |= bit
	return false
}

// findAt advances the live threads in priority order. Each position keeps
// only its first match and advances only higher-priority threads. This
// bounds pending branches by program size, even during long lookahead.
// States visited beyond the final match end have failed and can be reused.
// Use fresh bits at start so the last match's end can be retried.
func (s *spanScan) findAt(start int) int {
	if s.atStart == nil {
		s.atStart = make([]uint64, (len(s.prog.Inst)+63)/64)
	}
	clear(s.atStart)
	s.current = append(s.current[:0], uint32(s.prog.Start))
	prev := s.left
	if start > 0 {
		prev, _ = utf8.DecodeLastRuneInString(s.text[:start])
	}
	end := -1
	for pos := start; len(s.current) > 0; {
		r, width := s.right, 0
		if pos < len(s.text) {
			r, width = utf8.DecodeRuneInString(s.text[pos:])
		}
		s.next = s.next[:0]
	nextBranch:
		for len(s.current) > 0 {
			last := len(s.current) - 1
			pc := s.current[last]
			s.current = s.current[:last]
			for !s.seen(pc, pos, start) {
				inst := &s.prog.Inst[pc]
				switch inst.Op {
				case syntax.InstMatch:
					end = pos
					break nextBranch
				case syntax.InstFail:
					continue nextBranch
				case syntax.InstAlt, syntax.InstAltMatch:
					s.current = append(s.current, inst.Arg)
				case syntax.InstEmptyWidth:
					want := syntax.EmptyOp(inst.Arg)
					if syntax.EmptyOpContext(prev, r)&want != want {
						continue nextBranch
					}
				case syntax.InstRune, syntax.InstRune1, syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
					if width > 0 && spanRuneMatches(inst, r) {
						s.next = append(s.next, inst.Out)
					}
					continue nextBranch
				}
				pc = inst.Out
			}
		}
		// Successors arrive in priority order. Reverse them so the next
		// position can pop the preferred thread and expand its branches first.
		slices.Reverse(s.next)
		s.current, s.next = s.next, s.current
		prev, pos = r, pos+width
	}
	return end
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
