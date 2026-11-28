package wsdashboard

import (
	"encoding/json"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wsproctree"
)

// GET /holders/metrics is the Holders popup's resource trace: each process
// holder's OS subtree (the holder process, its child, and their
// descendants) with resident memory, CPU%, a short history of both, and
// the descendant processes. The popup asks only while it is open, shown,
// and not paused; every answer is one snapshot of the process table
// (wsproctree), so nothing samples between asks. The history lives in
// memory only and is dropped after holderTraceIdle without an ask.
//
// It names arbitrary processes, so it serves a browser on this machine only
// (localViewer; anyone else gets 404, as the stop route does).

const (
	// holderTraceInterval is the ask period the answer suggests.
	holderTraceInterval = 1500 * time.Millisecond
	// holderTraceReuse answers asks closer than this (several tabs) from
	// the last snapshot instead of taking another.
	holderTraceReuse = time.Second
	// holderTraceIdle drops the history after this long without an ask.
	holderTraceIdle = 30 * time.Second
	// holderTraceDepth is each holder's history length.
	holderTraceDepth = 24
	// holderTraceProcs caps one holder's listed descendants.
	holderTraceProcs = 64
)

// holderMetrics is the route's answer.
type holderMetrics struct {
	TS         int64                   `json:"ts"`
	IntervalMS int64                   `json:"interval_ms"`
	Supported  bool                    `json:"supported"`
	Holders    map[string]holderMetric `json:"holders"`
}

type holderMetric struct {
	PID   int          `json:"pid"`
	RSS   uint64       `json:"rss"`
	CPU   float64      `json:"cpu"`
	Spark holderSpark  `json:"spark"`
	Procs []procMetric `json:"procs"`
}

type holderSpark struct {
	RSS []uint64  `json:"rss"`
	CPU []float64 `json:"cpu"`
}

type procMetric struct {
	PID  int     `json:"pid"`
	PPID int     `json:"ppid"`
	Name string  `json:"name"`
	RSS  uint64  `json:"rss"`
	CPU  float64 `json:"cpu"`
}

// holderTrace is the sample ring. The zero value is ready; snap and now
// stand in for the process table and the clock in tests.
type holderTrace struct {
	mu      sync.Mutex
	snap    func([]int) ([]wsproctree.Proc, error)
	now     func() time.Time
	asked   time.Time
	takenAt time.Time
	last    *holderMetrics
	// cpu is each process's CPU time at the last snapshot.
	cpu map[int]time.Duration
	// spark is each holder's history, by holder id.
	spark map[string]*holderSpark
}

// sample answers one ask for the holders of list.
func (t *holderTrace) sample(list []wsholder.Info) holderMetrics {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now
	if t.now != nil {
		now = t.now
	}
	at := now()
	if !t.asked.IsZero() && at.Sub(t.asked) > holderTraceIdle {
		t.cpu, t.spark, t.last = nil, nil, nil
	}
	t.asked = at
	if t.last != nil && at.Sub(t.takenAt) >= 0 && at.Sub(t.takenAt) < holderTraceReuse {
		return *t.last
	}
	ans := holderMetrics{TS: at.UnixMilli(), IntervalMS: holderTraceInterval.Milliseconds(), Supported: wsproctree.Supported(), Holders: map[string]holderMetric{}}
	var roots []int
	for _, h := range list {
		if h.PID > 0 {
			roots = append(roots, h.PID)
		}
		if h.Child != nil && h.Child.PID > 0 {
			roots = append(roots, h.Child.PID)
		}
	}
	snap := wsproctree.Snapshot
	if t.snap != nil {
		snap = t.snap
	}
	var procs []wsproctree.Proc
	var err error
	if len(roots) > 0 {
		procs, err = snap(roots)
	}
	if err != nil {
		ans.Supported = false
		t.cpu, t.spark = nil, nil
		t.last, t.takenAt = &ans, at
		return ans
	}
	elapsed := at.Sub(t.takenAt)
	cpu := make(map[int]time.Duration, len(procs))
	pct := make(map[int]float64, len(procs))
	for _, p := range procs {
		cpu[p.PID] = p.CPU
		if prev, ok := t.cpu[p.PID]; ok && p.CPU >= prev && elapsed > 0 {
			pct[p.PID] = float64(p.CPU-prev) / float64(elapsed) * 100
		}
	}
	spark := map[string]*holderSpark{}
	for _, h := range list {
		if h.PID <= 0 {
			continue
		}
		tree := wsproctree.Descendants(procs, h.PID)
		if h.Child != nil && h.Child.PID > 0 && !hasPID(tree, h.Child.PID) {
			tree = append(tree, wsproctree.Descendants(procs, h.Child.PID)...)
		}
		if len(tree) == 0 {
			continue
		}
		m := holderMetric{PID: h.PID, Procs: []procMetric{}}
		var load float64
		for _, p := range tree {
			m.RSS += p.RSS
			load += pct[p.PID]
			if p.PID != h.PID && len(m.Procs) < holderTraceProcs {
				m.Procs = append(m.Procs, procMetric{PID: p.PID, PPID: p.PPID, Name: p.Name, RSS: p.RSS, CPU: tenth(pct[p.PID])})
			}
		}
		m.CPU = tenth(load)
		s := t.spark[h.ID]
		if s == nil {
			s = &holderSpark{}
		}
		s.RSS = appendCapped(s.RSS, m.RSS)
		s.CPU = appendCapped(s.CPU, m.CPU)
		spark[h.ID] = s
		m.Spark = holderSpark{RSS: append([]uint64(nil), s.RSS...), CPU: append([]float64(nil), s.CPU...)}
		ans.Holders[h.ID] = m
	}
	t.cpu, t.spark = cpu, spark
	t.last, t.takenAt = &ans, at
	return ans
}

func hasPID(ps []wsproctree.Proc, pid int) bool {
	for _, p := range ps {
		if p.PID == pid {
			return true
		}
	}
	return false
}

func appendCapped[T any](s []T, v T) []T {
	s = append(s, v)
	if len(s) > holderTraceDepth {
		s = append(s[:0:0], s[len(s)-holderTraceDepth:]...)
	}
	return s
}

// tenth rounds a percentage to one decimal.
func tenth(v float64) float64 { return math.Round(v*10) / 10 }

// serveHolderMetrics serves GET /holders/metrics.
func (d *Daemon) serveHolderMetrics(w http.ResponseWriter, r *http.Request) {
	if !localViewer(r) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var list []wsholder.Info
	if d.holders != nil {
		list = d.holders.List().Holders
	}
	ans := d.holderTrace.sample(list)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(ans)
}
