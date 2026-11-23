package wsfs

import (
	"fmt"
	"strings"
)

const (
	diffContext = 3
	// Past this limit, show the changed region as one replacement.
	diffMaxCells = 4_000_000
)

// unifiedDiff is a unified diff of a to b with diffContext lines of context;
// "" when they are equal. It is a line diff: the common head and tail are cut
// away and the middle is diffed by longest common subsequence.
func unifiedDiff(path, a, b string) string {
	if a == b {
		return ""
	}
	al, bl := splitLines(a), splitLines(b)
	pre := 0
	for pre < len(al) && pre < len(bl) && al[pre] == bl[pre] {
		pre++
	}
	suf := 0
	for suf < len(al)-pre && suf < len(bl)-pre && al[len(al)-1-suf] == bl[len(bl)-1-suf] {
		suf++
	}
	am, bm := al[pre:len(al)-suf], bl[pre:len(bl)-suf]
	type op struct {
		kind byte // ' ', '-', '+'
		text string
	}
	var ops []op
	for _, l := range al[:pre] {
		ops = append(ops, op{' ', l})
	}
	if len(am)*len(bm) > diffMaxCells {
		for _, l := range am {
			ops = append(ops, op{'-', l})
		}
		for _, l := range bm {
			ops = append(ops, op{'+', l})
		}
	} else {
		n, m := len(am), len(bm)
		lcs := make([][]int32, n+1)
		for i := range lcs {
			lcs[i] = make([]int32, m+1)
		}
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if am[i] == bm[j] {
					lcs[i][j] = lcs[i+1][j+1] + 1
				} else {
					lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
				}
			}
		}
		i, j := 0, 0
		for i < n && j < m {
			switch {
			case am[i] == bm[j]:
				ops = append(ops, op{' ', am[i]})
				i++
				j++
			case lcs[i+1][j] >= lcs[i][j+1]:
				ops = append(ops, op{'-', am[i]})
				i++
			default:
				ops = append(ops, op{'+', bm[j]})
				j++
			}
		}
		for ; i < n; i++ {
			ops = append(ops, op{'-', am[i]})
		}
		for ; j < m; j++ {
			ops = append(ops, op{'+', bm[j]})
		}
	}
	for _, l := range al[len(al)-suf:] {
		ops = append(ops, op{' ', l})
	}
	// Hunks: runs of changes with diffContext lines around them.
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", path, path)
	i := 0
	aLine, bLine := 1, 1
	// aAt and bAt are the line numbers of ops[k]'s position.
	aAt := make([]int, len(ops)+1)
	bAt := make([]int, len(ops)+1)
	for k, o := range ops {
		aAt[k], bAt[k] = aLine, bLine
		if o.kind != '+' {
			aLine++
		}
		if o.kind != '-' {
			bLine++
		}
	}
	aAt[len(ops)], bAt[len(ops)] = aLine, bLine
	for i < len(ops) {
		for i < len(ops) && ops[i].kind == ' ' {
			i++
		}
		if i >= len(ops) {
			break
		}
		from := max(i-diffContext, 0)
		to := i
		for {
			for to < len(ops) && ops[to].kind != ' ' {
				to++
			}
			gap := to
			for gap < len(ops) && ops[gap].kind == ' ' {
				gap++
			}
			if gap < len(ops) && gap-to <= 2*diffContext {
				to = gap
				continue
			}
			to = min(to+diffContext, len(ops))
			break
		}
		aCount, bCount := 0, 0
		for _, o := range ops[from:to] {
			if o.kind != '+' {
				aCount++
			}
			if o.kind != '-' {
				bCount++
			}
		}
		aStart, bStart := aAt[from], bAt[from]
		if aCount == 0 {
			aStart--
		}
		if bCount == 0 {
			bStart--
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount)
		for _, o := range ops[from:to] {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		i = to
	}
	return out.String()
}

// splitLines splits text into lines without their terminators; text that ends
// in a newline has no empty last line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
