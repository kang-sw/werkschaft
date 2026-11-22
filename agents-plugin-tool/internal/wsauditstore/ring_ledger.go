package wsauditstore

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

// ledger is a ring's source-identity state: what capture's recovery
// questions (wsaudit.SourcedSink Recover and Retained) are answered from. The
// writer goroutine is its only mutator and takes mu for each in-memory
// change; readers take mu alone, so they never wait on the writer's disk I/O.
type ledger struct {
	mu       sync.Mutex
	maxSeq   uint64
	bounds   map[string]uint64 // per-source finalized boundary
	retained map[wsaudit.SourceID]struct{}
	// lost holds the identities counted in loss, each with its encoded size;
	// lostBytes is their sum.
	lost      map[wsaudit.SourceID]int
	lostBytes int
	loss      uint64
	// floor is, per source, a sequence through which every sequenced
	// identity is accounted for — retained once or counted lost — folded out
	// of lost to keep the header bounded. It only ever covers a contiguous
	// run of accounted sequences at or below the finalized boundary, so no
	// identity never seen falls under it.
	floor map[string]uint64
	// forgot counts lost identities dropped from lost because folding could
	// not bound it, for the writer's log.
	forgot int
	// recency orders sources by their latest finalize (higher is more
	// recent; header-seeded sources start at 0), so bounding the source maps
	// drops the stalest first. dropped counts sources dropped, for the log.
	recency map[string]uint64
	tick    uint64
	dropped int
}

func newLedger() *ledger {
	return &ledger{
		bounds:   map[string]uint64{},
		retained: map[wsaudit.SourceID]struct{}{},
		lost:     map[wsaudit.SourceID]int{},
		floor:    map[string]uint64{},
		recency:  map[string]uint64{},
	}
}

// sourceCost is the encoded bytes src takes in a header's two source maps,
// with room for the largest values.
func sourceCost(src string) int {
	return 2 * (encodedLen(src) + len(`"":18446744073709551615,`))
}

// boundSources keeps the header's per-source maps within sourceBudget,
// dropping the least recently finalized sources (their boundary, floor and
// lost identities) down to three quarters of it. A dropped source's replay
// is no longer refused as finalized: the store may then append a record it
// already finalized, never drop one. l.mu is held.
func (l *ledger) boundSources() {
	srcs := make([]string, 0, len(l.bounds)+len(l.floor))
	seen := map[string]bool{}
	total := 0
	for _, m := range []map[string]uint64{l.bounds, l.floor} {
		for src := range m {
			if !seen[src] {
				seen[src] = true
				srcs = append(srcs, src)
				total += sourceCost(src)
			}
		}
	}
	if total <= sourceBudget {
		return
	}
	sort.Slice(srcs, func(i, j int) bool {
		if l.recency[srcs[i]] != l.recency[srcs[j]] {
			return l.recency[srcs[i]] < l.recency[srcs[j]]
		}
		return srcs[i] < srcs[j]
	})
	drop := map[string]bool{}
	for _, src := range srcs {
		if total <= sourceBudget*3/4 {
			break
		}
		drop[src] = true
		total -= sourceCost(src)
		delete(l.bounds, src)
		delete(l.floor, src)
		delete(l.recency, src)
		l.dropped++
	}
	for id := range l.lost {
		if drop[id.Source] {
			l.dropLost(id)
		}
	}
}

// settled reports whether id is a sequenced identity at or below its
// source's lost floor: already accounted for, so it is neither appended nor
// counted again. Tee and keyed identities are never settled by a floor.
// l.mu is held.
func (l *ledger) settled(id wsaudit.SourceID) bool {
	return sequenced(id) && id.Seq <= l.floor[id.Source]
}

// sequenced reports whether id names a sequenced source line.
func sequenced(id wsaudit.SourceID) bool {
	return !id.Tee && id.Key == "" && id.Seq > 0
}

// addLost records id as counted lost. l.mu is held.
func (l *ledger) addLost(id wsaudit.SourceID) {
	if _, ok := l.lost[id]; ok {
		return
	}
	n := 64
	if raw, err := json.Marshal(id); err == nil {
		n = len(raw) + 1
	}
	l.lost[id] = n
	l.lostBytes += n
}

func (l *ledger) dropLost(id wsaudit.SourceID) {
	if n, ok := l.lost[id]; ok {
		delete(l.lost, id)
		l.lostBytes -= n
	}
}

// fold keeps the lost set within lostBudget. First each source's floor
// advances over the contiguous run of sequences above it that are accounted
// for (a sequenced identity at that sequence retained or counted lost) and
// covered by the finalized boundary, and the lost entries it settles go.
// Only when that leaves the set over three quarters of the budget are the
// lowest entries — covered ones first — forgotten down to half of it: never
// folded, so a record never seen is never refused for it. A forgotten
// identity replayed after the segment holding its loss line is gone counts
// again, or (uncovered) is appended; the count may run high, a record is
// never dropped. It reports the identities forgotten. l.mu is held.
func (l *ledger) fold() (forgot int) {
	if l.lostBytes <= lostBudget {
		return 0
	}
	accounted := map[string]map[uint64]bool{}
	mark := func(id wsaudit.SourceID) {
		if !sequenced(id) || id.Seq <= l.floor[id.Source] || id.Seq > l.bounds[id.Source] {
			return
		}
		set := accounted[id.Source]
		if set == nil {
			set = map[uint64]bool{}
			accounted[id.Source] = set
		}
		set[id.Seq] = true
	}
	for id := range l.lost {
		mark(id)
	}
	for id := range l.retained {
		mark(id)
	}
	for src, set := range accounted {
		f := l.floor[src]
		for set[f+1] {
			f++
		}
		l.floor[src] = f
	}
	for id := range l.lost {
		if l.settled(id) {
			l.dropLost(id)
		}
	}
	if l.lostBytes <= lostBudget*3/4 {
		return 0
	}
	cands := make([]wsaudit.SourceID, 0, len(l.lost))
	for id := range l.lost {
		cands = append(cands, id)
	}
	sort.Slice(cands, func(i, j int) bool {
		ci, cj := cands[i].Covered(l.bounds[cands[i].Source]), cands[j].Covered(l.bounds[cands[j].Source])
		if ci != cj {
			return ci
		}
		if cands[i].Position() != cands[j].Position() {
			return cands[i].Position() < cands[j].Position()
		}
		return cands[i].String() < cands[j].String()
	})
	for _, id := range cands {
		if l.lostBytes <= lostBudget/2 {
			break
		}
		l.dropLost(id)
		forgot++
	}
	return forgot
}

func (l *ledger) recovery(source string) wsaudit.Recovery {
	l.mu.Lock()
	defer l.mu.Unlock()
	return wsaudit.Recovery{MaxSeq: l.maxSeq, Boundary: l.bounds[source]}
}

func (l *ledger) isRetained(id wsaudit.SourceID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.retained[id]
	return ok
}
