// Package wsproctree reads the OS process table for the dashboard's holder
// resource trace: each process's parent, name, resident memory, and
// cumulative CPU time, without cgo. Windows (Toolhelp32, GetProcessTimes,
// psapi) and Linux (/proc/<pid>/stat) read it in process; Darwin runs one
// `ps` per snapshot, since its cgo-free sysctl reports resident memory in a
// field too narrow for large processes. Other systems are unsupported.
//
// A snapshot is taken only on demand (the dashboard's open Holders popup);
// nothing here polls or keeps state.
package wsproctree

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Proc is one process of a snapshot.
type Proc struct {
	PID, PPID int
	// Name is the executable's base name.
	Name string
	// RSS is the resident memory in bytes.
	RSS uint64
	// CPU is the cumulative user and system CPU time.
	CPU time.Duration
}

// ErrUnsupported is Snapshot's error on a system without a collector.
var ErrUnsupported = errors.New("process metrics are not readable on this OS")

// Snapshot reads the processes of roots and every descendant of theirs. A
// root that is gone is absent from the answer.
func Snapshot(roots []int) ([]Proc, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	return snapshot(roots)
}

// Supported reports whether this OS has a collector.
func Supported() bool { return supported }

// Descendants is root's subtree in all: root first (when present), then its
// descendants in breadth-first order, each once.
func Descendants(all []Proc, root int) []Proc {
	byPID := make(map[int]Proc, len(all))
	kids := map[int][]int{}
	for _, p := range all {
		byPID[p.PID] = p
		if p.PPID != p.PID {
			kids[p.PPID] = append(kids[p.PPID], p.PID)
		}
	}
	var out []Proc
	seen := map[int]bool{}
	queue := []int{root}
	if _, ok := byPID[root]; !ok {
		queue = kids[root]
		seen[root] = true
	}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if p, ok := byPID[pid]; ok {
			out = append(out, p)
		}
		ks := append([]int(nil), kids[pid]...)
		sort.Ints(ks)
		queue = append(queue, ks...)
	}
	return out
}

// keep narrows all to roots and their descendants.
func keep(all []Proc, roots []int) []Proc {
	in := map[int]bool{}
	for _, r := range roots {
		for _, p := range Descendants(all, r) {
			in[p.PID] = true
		}
	}
	out := make([]Proc, 0, len(in))
	for _, p := range all {
		if in[p.PID] {
			out = append(out, p)
		}
	}
	return out
}

// parsePS reads `ps -axo pid=,ppid=,rss=,cputime=,comm=` output: rss in
// KiB, cputime as [[dd-]hh:]mm:ss[.cc], and comm (a path that may hold
// spaces) to the end of the line. A malformed line is skipped.
func parsePS(out string) []Proc {
	var res []Proc
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		rss, e3 := strconv.ParseUint(f[2], 10, 64)
		cpu, e4 := parseCPUTime(f[3])
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			continue
		}
		// comm starts after the fourth field; it keeps its own spaces.
		rest := line
		for i := 0; i < 4; i++ {
			rest = strings.TrimLeft(rest, " \t")
			rest = rest[strings.IndexAny(rest+" ", " \t"):]
		}
		comm := strings.TrimSpace(rest)
		res = append(res, Proc{PID: pid, PPID: ppid, Name: baseName(comm), RSS: rss * 1024, CPU: cpu})
	}
	return res
}

// parseCPUTime reads ps's cputime: [[dd-]hh:]mm:ss[.cc].
func parseCPUTime(s string) (time.Duration, error) {
	var days int64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil {
			return 0, err
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, errors.New("malformed cputime " + strconv.Quote(s))
	}
	sec, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil || sec < 0 {
		return 0, errors.New("malformed cputime " + strconv.Quote(s))
	}
	total := sec
	mult := 60.0
	for i := len(parts) - 2; i >= 0; i-- {
		n, err := strconv.ParseInt(parts[i], 10, 64)
		if err != nil || n < 0 {
			return 0, errors.New("malformed cputime " + strconv.Quote(s))
		}
		total += float64(n) * mult
		mult *= 60
	}
	total += float64(days) * 86400
	return time.Duration(total * float64(time.Second)), nil
}

// parseStat reads one /proc/<pid>/stat line: comm sits in the outermost
// parentheses (it may itself hold spaces and parentheses), utime and stime
// are clock ticks of hz, and rss is in pages of pageSize.
func parseStat(line string, hz, pageSize int64) (Proc, error) {
	open, close := strings.IndexByte(line, '('), strings.LastIndexByte(line, ')')
	if open < 1 || close < open {
		return Proc{}, errors.New("malformed stat")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line[:open]))
	if err != nil {
		return Proc{}, err
	}
	// After comm: state(3) ppid(4) ... utime(14) stime(15) ... rss(24).
	f := strings.Fields(line[close+1:])
	if len(f) < 22 {
		return Proc{}, errors.New("short stat")
	}
	ppid, e1 := strconv.Atoi(f[1])
	utime, e2 := strconv.ParseInt(f[11], 10, 64)
	stime, e3 := strconv.ParseInt(f[12], 10, 64)
	rss, e4 := strconv.ParseInt(f[21], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return Proc{}, errors.New("malformed stat fields")
	}
	if rss < 0 {
		rss = 0
	}
	ticks := utime + stime
	cpu := time.Duration(ticks) * time.Second / time.Duration(hz)
	return Proc{PID: pid, PPID: ppid, Name: line[open+1 : close], RSS: uint64(rss * pageSize), CPU: cpu}, nil
}

func baseName(p string) string {
	p = strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return p
}
