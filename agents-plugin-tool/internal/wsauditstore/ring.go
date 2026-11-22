package wsauditstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

// RingSchemaVersion is the on-disk audit ring format version, written in
// every segment's header line. A store refuses to write a ring holding a
// newer version (Decision 4).
const RingSchemaVersion = 1

// ErrNewerFormat refuses a write to a ring a newer binary wrote.
var ErrNewerFormat = errors.New("wsauditstore: audit ring has a newer format")

var (
	segmentPattern    = regexp.MustCompile(`^seg-([0-9]{16})\.jsonl$`)
	literalDirPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
)

func segmentName(seq uint64) string { return fmt.Sprintf("seg-%016d.jsonl", seq) }

// sessionDirName maps a normalized session ID to its ring directory name: the
// ID itself when it is a short portable file name, otherwise "~" and a digest
// ('~' never appears in a literal name, so the two forms cannot collide).
func sessionDirName(session string) string {
	if literalDirPattern.MatchString(session) && session != "." && session != ".." {
		return session
	}
	sum := sha256.Sum256([]byte(session))
	return "~" + hex.EncodeToString(sum[:16])
}

// line is one JSONL record of a segment.
type line struct {
	T string `json:"t"`
	// Header ("h"): the format version, the session, and the recovery state
	// as of the segment's creation, so the oldest retained segment alone
	// seeds recovery after older ones were dropped.
	V       int          `json:"v,omitempty"`
	Session string       `json:"session,omitempty"`
	State   *headerState `json:"state,omitempty"`
	// Event ("e"): a retained envelope and, when it has one, its source
	// identity.
	Event *wsaudit.Envelope `json:"e,omitempty"`
	ID    *wsaudit.SourceID `json:"id,omitempty"`
	// Loss ("loss"): the source record ID finalized without being retained,
	// why, and the session's loss count with it (N), so replay takes the
	// count as a maximum and never counts a line twice — not even one whose
	// identity a later header no longer lists.
	Reason string `json:"reason,omitempty"`
	N      uint64 `json:"n,omitempty"`
	// Boundary ("fin"): a source's finalized boundary (Recovery.Boundary).
	Source string `json:"src,omitempty"`
	Seq    uint64 `json:"seq,omitempty"`
	At     string `json:"at,omitempty"`
}

const (
	lineHeader = "h"
	lineEvent  = "e"
	lineLoss   = "loss"
	lineFin    = "fin"
)

type headerState struct {
	MaxSeq     uint64            `json:"max_seq,omitempty"`
	Boundaries map[string]uint64 `json:"boundaries,omitempty"`
	Loss       uint64            `json:"loss,omitempty"`
	// Lost lists the lost identities already counted in Loss, covered by a
	// boundary or not, so neither a replayed loss line nor a replayed record
	// counts one again once the segment holding its loss line is gone.
	Lost []wsaudit.SourceID `json:"lost,omitempty"`
	// LostFloor is, per source, the sequence at or below which every
	// sequenced identity not retained is already counted lost (see
	// ledger.floor).
	LostFloor map[string]uint64 `json:"lost_floor,omitempty"`
	LastSeen  string            `json:"last_seen,omitempty"`
}

// lostBudget bounds the encoded size of a header's Lost list; past it a
// contiguous accounted prefix folds into LostFloor, and what still does not
// fit is forgotten (see ledger.fold).
const lostBudget = 32 << 10

// sourceBudget bounds the encoded size of a header's per-source maps
// (Boundaries and LostFloor); past it the least recently finalized sources
// are dropped (see ledger.boundSources). With lostBudget and room for the
// session ID and scalars it keeps every header within HeaderBound.
const sourceBudget = int(HeaderBound) - lostBudget - 4<<10

// maxReasonBytes bounds a loss line's reason; a longer one is cut on a rune
// boundary, so a loss line always fits a segment beside its header.
const maxReasonBytes = 256

type segment struct {
	seq    uint64
	size   int64
	events int // retained events that live in this segment
}

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

// ring is one session's audit segment ring with its in-memory index. The
// store's writer goroutine is its only mutator; reads take the session's mu
// (events) or the ledger's mu (identities).
type ring struct {
	session string
	dir     string

	segs        []segment
	file        *os.File // newest segment, held open for appends
	forceRotate bool
	readOnly    error

	// view guards events and ids, the in-memory index readers copy from. The
	// writer takes it only around in-memory changes, never across disk I/O.
	view   sync.RWMutex
	events []wsaudit.Envelope
	ids    []*wsaudit.SourceID // parallel to events; nil for an unsourced event
	led    *ledger
	// Control lines written ahead of the next record, or when the writer
	// idles: loss lines in order, and the latest boundary per source.
	pendingLoss [][]byte
	pendingFin  map[string]uint64
	lastSeen    time.Time
	bytes       int64
	// evictErr is the first segment deletion that failed since the store
	// last looked; the segment and its bytes stay accounted.
	evictErr error
}

func newRing(session, dir string) *ring {
	return &ring{session: session, dir: dir, led: newLedger(), pendingFin: map[string]uint64{}}
}

// newerRingOnDisk reports whether any segment of the ring at dir opens with a
// header of a newer format, reading only each segment's first line (at most
// HeaderBound bytes). Age-out and capacity eviction never delete such a ring
// (Decision 4, forward safety). A listing or read that fails is returned as
// an error: the caller keeps the ring rather than guess (fail closed).
func newerRingOnDisk(dir string) (bool, error) {
	segs, _, err := listSegments(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var firstErr error
	for _, seg := range segs {
		head, err := readHead(filepath.Join(dir, segmentName(seg.seq)), int(HeaderBound))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if headNewer(head) {
			return true, nil
		}
	}
	return false, firstErr
}

// headNewer reports whether a segment's head (its first HeaderBound bytes)
// opens with a header this binary must not delete or write: one naming a
// newer format, or a header whose version cannot be read. A complete first
// line is parsed whole; a torn one is scanned key by key, in any key order.
func headNewer(head []byte) bool {
	if nl := bytes.IndexByte(head, '\n'); nl >= 0 {
		_, newer := headerNewer(head[:nl])
		return newer
	}
	t, v, vKnown := scanHeaderKeys(head)
	switch {
	case t != "" && t != lineHeader:
		return false
	case vKnown:
		return v > RingSchemaVersion
	default:
		return t == lineHeader // a header whose version is unreadable
	}
}

// headerNewer classifies one complete first line, in any key order: whether
// it is a header, and whether that header names a newer format or a version
// this binary cannot read (both are never ours to write or delete). A line
// that is not a JSON object is no header.
func headerNewer(text []byte) (header, newer bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(text, &fields) != nil {
		return false, false
	}
	var t string
	if json.Unmarshal(fields["t"], &t) != nil || t != lineHeader {
		return false, false
	}
	var v int
	if raw, ok := fields["v"]; !ok || json.Unmarshal(raw, &v) != nil {
		return true, true
	}
	return true, v > RingSchemaVersion
}

// scanHeaderKeys reads the top-level "t" and "v" of a possibly torn JSON
// object token by token, in any key order, stopping once both are known or
// the input ends.
func scanHeaderKeys(head []byte) (t string, v int, vKnown bool) {
	dec := json.NewDecoder(bytes.NewReader(head))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", 0, false
	}
	for dec.More() && (t == "" || !vKnown) {
		key, err := dec.Token()
		if err != nil {
			return t, v, vKnown
		}
		switch key {
		case "t":
			if dec.Decode(&t) != nil {
				return "", v, vKnown
			}
		case "v":
			if dec.Decode(&v) != nil {
				return t, 0, false
			}
			vKnown = true
		default:
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return t, v, vKnown
			}
		}
	}
	return t, v, vKnown
}

// headerVersion is the "v" of a header line at the start of head, in any key
// order and even when the line is torn; 0 when head does not open with a
// header object carrying one.
func headerVersion(head []byte) int {
	t, v, vKnown := scanHeaderKeys(head)
	if t != lineHeader || !vKnown {
		return 0
	}
	return v
}

// listSegments returns the ring's segment sequence numbers and sizes in
// order, from one directory listing.
func listSegments(dir string) ([]segment, time.Time, error) {
	entries, err := readDir(dir)
	if err != nil {
		return nil, time.Time{}, err
	}
	var segs []segment
	var newest time.Time
	for _, e := range entries {
		m := segmentPattern.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		seq, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil || seq == 0 {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		segs = append(segs, segment{seq: seq, size: info.Size()})
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].seq < segs[j].seq })
	return segs, newest, nil
}

// recover reads every retained segment once into the in-memory index, trims
// a torn last line of the newest segment, and opens it for appends.
func (r *ring) recover() error {
	segs, _, err := listSegments(r.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for i := range segs {
		newest := i == len(segs)-1
		raw, err := readFile(filepath.Join(r.dir, segmentName(segs[i].seq)))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		good, newer, header := r.replaySegment(&segs[i], raw)
		if newer {
			r.readOnly = ErrNewerFormat
		}
		if newest && good < len(raw) && !newer {
			if err := truncateFile(filepath.Join(r.dir, segmentName(segs[i].seq)), int64(good)); err != nil {
				r.forceRotate = true
			} else {
				segs[i].size = int64(good)
			}
		}
		if newest && !header {
			// A torn or missing header: appending here would leave a live
			// segment no reader can version or seed from, so the next write
			// opens a new segment with a valid header.
			r.forceRotate = true
		}
		r.segs = append(r.segs, segs[i])
		r.bytes += segs[i].size
		if newer {
			// Later segments of a newer ring are not ours to parse.
			for _, rest := range segs[i+1:] {
				r.segs = append(r.segs, rest)
				r.bytes += rest.size
			}
			break
		}
	}
	r.led.mu.Lock()
	r.led.forgot += r.led.fold() // headers' unions can run over the budget
	r.led.mu.Unlock()
	if r.readOnly == nil && len(r.segs) > 0 && !r.forceRotate {
		f, err := openAppend(filepath.Join(r.dir, segmentName(r.segs[len(r.segs)-1].seq)))
		if err != nil {
			r.forceRotate = true
		} else {
			r.file = f
		}
	}
	return nil
}

// recoverTail recovers a ring's identity state from its newest segments
// alone — back to the newest one opening with a complete, well-formed header,
// whose snapshot holds everything before it (a torn header from a crash
// right after rotation does not count) — for an unloaded ring about to be
// evicted. Every listed segment is accounted in segs and bytes. It returns
// nil when the ring cannot be listed or has no segment, and fails closed
// (nil) when a segment of that tail cannot be read: it may alone carry a
// boundary, and a snapshot taken without it would let a deletion lose it.
func recoverTail(session, dir string) *ring {
	segs, _, err := listSegments(dir)
	if err != nil || len(segs) == 0 {
		return nil
	}
	raws := make([][]byte, len(segs))
	start := len(segs) - 1
	for ; start >= 0; start-- {
		raw, err := readFile(filepath.Join(dir, segmentName(segs[start].seq)))
		if errors.Is(err, fs.ErrNotExist) {
			continue // deleted since the listing
		}
		if err != nil {
			return nil
		}
		raws[start] = raw
		if completeHeader(raw) {
			break
		}
	}
	start = max(start, 0)
	r := newRing(session, dir)
	for i := range segs {
		if i >= start && raws[i] != nil {
			r.replaySegment(&segs[i], raws[i])
		}
		r.bytes += segs[i].size
	}
	r.segs = segs
	return r
}

// completeHeader reports whether raw opens with a newline-terminated line
// that decodes whole as a header.
func completeHeader(raw []byte) bool {
	nl := bytes.IndexByte(raw, '\n')
	if nl < 0 {
		return false
	}
	var ln line
	return json.Unmarshal(raw[:nl], &ln) == nil && ln.T == lineHeader && ln.State != nil
}

// hasState reports whether the ring's ledger holds recovery state worth
// persisting after its data is evicted.
func (r *ring) hasState() bool {
	l := r.led
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.maxSeq > 0 || l.loss > 0 || len(l.bounds) > 0 || len(l.lost) > 0 || len(l.floor) > 0
}

// replaySegment applies one segment's lines. It returns the byte length of
// the well-formed prefix, whether the header names a newer format, and
// whether the segment opens with a well-formed header line.
func (r *ring) replaySegment(seg *segment, raw []byte) (good int, newer, header bool) {
	pos := 0
	first := true
	for pos < len(raw) {
		nl := bytes.IndexByte(raw[pos:], '\n')
		if nl < 0 {
			return pos, false, header // torn last line
		}
		text := raw[pos : pos+nl]
		next := pos + nl + 1
		if first {
			// The version is read on its own first, so a newer header whose
			// shape this binary cannot decode still refuses writes.
			if h, newer := headerNewer(text); h && newer {
				return pos, true, true
			}
		}
		var ln line
		if err := json.Unmarshal(text, &ln); err != nil {
			if next == len(raw) {
				return pos, false, header
			}
			first = false // a damaged first line is no header
			pos = next    // a damaged line mid-ring is skipped
			continue
		}
		if first {
			first = false
			if ln.T == lineHeader {
				header = true
				if r.session == "" {
					r.session = ln.Session
				}
				// Every header seeds: each is a snapshot of the state so far,
				// and seeding takes maxima and unions, so a header whose
				// predecessors are gone or headerless still carries it.
				if ln.State != nil {
					r.seed(ln.State)
				}
				pos = next
				continue
			}
		}
		r.apply(ln, seg)
		pos = next
	}
	return pos, false, header
}

// seed merges a segment header's recovery state into the ring's.
func (r *ring) seed(st *headerState) {
	l := r.led
	l.mu.Lock()
	l.maxSeq = max(l.maxSeq, st.MaxSeq)
	l.loss = max(l.loss, st.Loss)
	for src, b := range st.Boundaries {
		l.bounds[src] = max(l.bounds[src], b)
	}
	for src, f := range st.LostFloor {
		l.floor[src] = max(l.floor[src], f)
	}
	for _, id := range st.Lost {
		l.addLost(id)
	}
	l.boundSources()
	l.mu.Unlock()
	if t, err := time.Parse(time.RFC3339Nano, st.LastSeen); err == nil && t.After(r.lastSeen) {
		r.lastSeen = t
	}
}

func (r *ring) apply(ln line, seg *segment) {
	switch ln.T {
	case lineEvent:
		if ln.Event == nil {
			return
		}
		r.index(*ln.Event, ln.ID)
		if seg != nil {
			seg.events++
		}
	case lineLoss:
		if ln.ID != nil {
			r.replayLost(*ln.ID, ln.N)
		}
	case lineFin:
		r.finalize(ln.Source, ln.Seq)
	}
	if t, err := time.Parse(time.RFC3339Nano, ln.At); err == nil && t.After(r.lastSeen) {
		r.lastSeen = t
	}
}

// index adds a retained event, under its source identity when it has one,
// to the in-memory index.
func (r *ring) index(ev wsaudit.Envelope, id *wsaudit.SourceID) {
	r.view.Lock()
	r.events = append(r.events, ev)
	r.ids = append(r.ids, id)
	r.view.Unlock()
	if ev.At.After(r.lastSeen) {
		r.lastSeen = ev.At
	}
	l := r.led
	l.mu.Lock()
	l.maxSeq = max(l.maxSeq, ev.Seq)
	if id != nil {
		l.retained[*id] = struct{}{}
		l.dropLost(*id)
	}
	l.mu.Unlock()
}

// markLost records a source identity finalized without being retained. It
// reports whether the identity was new.
func (r *ring) markLost(id wsaudit.SourceID) bool {
	l := r.led
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.lost[id]; ok {
		return false
	}
	if _, ok := l.retained[id]; ok {
		return false
	}
	if l.settled(id) {
		return false
	}
	l.addLost(id)
	l.loss++
	l.forgot += l.fold()
	return true
}

// replayLost applies a persisted loss line: the identity joins the lost set
// unless already accounted for, and the loss count rises to the line's
// cumulative count n (a line without one counts a new identity once).
func (r *ring) replayLost(id wsaudit.SourceID, n uint64) {
	l := r.led
	l.mu.Lock()
	defer l.mu.Unlock()
	_, lost := l.lost[id]
	_, kept := l.retained[id]
	fresh := !lost && !kept && !l.settled(id)
	if fresh {
		l.addLost(id)
	}
	switch {
	case n > 0:
		l.loss = max(l.loss, n)
	case fresh:
		l.loss++
	}
	if fresh {
		l.forgot += l.fold()
	}
}

// finalize advances a source's finalized boundary, reporting whether it
// moved. Only capture's Finalize (and its persisted fin line) moves it: a
// retained or lost record does not, so a tee record anchored on the last
// kept line stays admissible until capture finalizes past it.
func (r *ring) finalize(source string, b uint64) bool {
	l := r.led
	l.mu.Lock()
	defer l.mu.Unlock()
	if b <= l.bounds[source] {
		return false
	}
	_, known := l.bounds[source]
	l.bounds[source] = b
	l.tick++
	l.recency[source] = l.tick
	if !known {
		l.boundSources()
	}
	return true
}

// Verdicts of admit.
const (
	admitAppend = iota
	admitDuplicate
	admitLoss
)

// admit decides an incoming envelope's fate by its source identity id and
// the position rec of the source record that yielded it (id itself, or the
// yielding record of a keyed event, whose id carries no position): an
// identity already retained or already recorded lost is a duplicate; one
// whose rec its source's finalized boundary covers is a loss, never appended
// late (the same rule capture applies to its recovered boundary, so an early,
// incomplete recovery answer cannot cause a duplicate or a late append).
func (r *ring) admit(id *wsaudit.SourceID, rec wsaudit.SourceID) int {
	if id == nil {
		return admitAppend
	}
	l := r.led
	if _, ok := l.retained[*id]; ok {
		return admitDuplicate
	}
	if _, ok := l.lost[*id]; ok {
		return admitDuplicate
	}
	if l.settled(*id) {
		return admitDuplicate
	}
	if rec.Covered(l.bounds[rec.Source]) {
		return admitLoss
	}
	return admitAppend
}

// pendingLines is the ring's queued control lines: losses in order, then each
// source's latest boundary.
func (r *ring) pendingLines() []byte {
	var out []byte
	for _, ln := range r.pendingLoss {
		out = append(out, ln...)
	}
	if len(r.pendingFin) > 0 {
		srcs := make([]string, 0, len(r.pendingFin))
		for src := range r.pendingFin {
			srcs = append(srcs, src)
		}
		sort.Strings(srcs)
		for _, src := range srcs {
			if ln, err := encodeLine(line{T: lineFin, Source: src, Seq: r.pendingFin[src]}); err == nil {
				out = append(out, ln...)
			}
		}
	}
	return out
}

func (r *ring) clearPending() {
	r.pendingLoss = nil
	clear(r.pendingFin)
}

func (r *ring) headerLine() ([]byte, error) {
	l := r.led
	l.mu.Lock()
	st := headerState{MaxSeq: l.maxSeq, Loss: l.loss}
	if len(l.bounds) > 0 {
		st.Boundaries = make(map[string]uint64, len(l.bounds))
		for src, b := range l.bounds {
			st.Boundaries[src] = b
		}
	}
	for id := range l.lost {
		st.Lost = append(st.Lost, id)
	}
	if len(l.floor) > 0 {
		st.LostFloor = make(map[string]uint64, len(l.floor))
		for src, f := range l.floor {
			st.LostFloor[src] = f
		}
	}
	l.mu.Unlock()
	sort.Slice(st.Lost, func(i, j int) bool { return st.Lost[i].String() < st.Lost[j].String() })
	if !r.lastSeen.IsZero() {
		st.LastSeen = r.lastSeen.UTC().Format(time.RFC3339Nano)
	}
	return encodeLine(line{T: lineHeader, V: RingSchemaVersion, Session: r.session, State: &st})
}

func encodeLine(ln line) ([]byte, error) {
	raw, err := json.Marshal(ln)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// writeBounded writes payload (whole lines) in chunks small enough that no
// single write — a rotation's header line included — exceeds segBytes, so a
// burst of queued control lines never makes a segment or the ring outgrow
// its bound. Every line fits (a record by Limits.Validate, a header by its
// budgets, a loss line by maxReasonBytes); one that does not is refused.
func (r *ring) writeBounded(payload []byte, segBytes, capBytes int64, room func(n int64)) error {
	header, err := r.headerLine()
	if err != nil {
		return err
	}
	limit := segBytes - int64(len(header))
	for len(payload) > 0 {
		n := chunkEnd(payload, limit)
		if int64(n) > limit {
			return fmt.Errorf("a %d-byte line does not fit a %d-byte segment beside its header", n, segBytes)
		}
		if _, err := r.write(payload[:n], segBytes, capBytes, room); err != nil {
			return err
		}
		payload = payload[n:]
	}
	return nil
}

// chunkEnd is the length of the longest whole-line prefix of p within limit,
// or of its first line when even that exceeds limit.
func chunkEnd(p []byte, limit int64) int {
	if int64(len(p)) <= limit {
		return len(p)
	}
	end := 0
	for end < len(p) {
		next := len(p)
		if nl := bytes.IndexByte(p[end:], '\n'); nl >= 0 {
			next = end + nl + 1
		}
		if int64(next) > limit && end > 0 {
			break
		}
		end = next
	}
	return end
}

// write appends payload (whole lines) to the newest segment, rotating first
// when it would overflow segBytes, then dropping the ring's own oldest
// segments while it would exceed capBytes, and only then calling room with
// the bytes about to be written, so store-wide eviction sees the ring
// already within its own cap. It returns the bytes removed by its own trim.
func (r *ring) write(payload []byte, segBytes, capBytes int64, room func(n int64)) (evicted int64, err error) {
	if r.readOnly != nil {
		return 0, r.readOnly
	}
	n := int64(len(payload))
	cur := len(r.segs) - 1
	if r.file == nil || r.forceRotate || cur < 0 || r.segs[cur].size+n > segBytes {
		header, err := r.headerLine()
		if err != nil {
			return 0, err
		}
		payload = append(header, payload...)
		n = int64(len(payload))
		var seq uint64 = 1
		if cur >= 0 {
			seq = r.segs[cur].seq + 1
		}
		if cur < 0 {
			if err := mkdirPrivate(r.dir); err != nil {
				return 0, err
			}
		}
		f, err := createSegment(filepath.Join(r.dir, segmentName(seq)))
		if err != nil {
			return 0, err
		}
		if r.file != nil {
			_ = closeFile(r.file)
		}
		r.file = f
		r.forceRotate = false
		r.segs = append(r.segs, segment{seq: seq})
	}
	for r.bytes+n > capBytes && len(r.segs) > 1 {
		dropped, err := r.dropOldest()
		if err != nil {
			break // the ring runs over its cap until a later write retries
		}
		evicted += dropped
	}
	if room != nil {
		room(n)
	}
	written, werr := writeFile(r.file, payload)
	r.segs[len(r.segs)-1].size += int64(written)
	r.bytes += int64(written)
	if werr != nil {
		// A torn write must not glue the next record to its tail.
		r.forceRotate = true
		return evicted, werr
	}
	return evicted, nil
}

// dropOldest deletes the oldest segment file and its events from the index.
// The newest segment is never dropped. A deletion that fails (a sharing
// refusal on Windows) leaves the segment, its events, and its bytes in place
// and is reported, also through evictErr.
func (r *ring) dropOldest() (int64, error) {
	if len(r.segs) < 2 {
		return 0, nil
	}
	seg := r.segs[0]
	if err := removeFile(filepath.Join(r.dir, segmentName(seg.seq))); err != nil && !errors.Is(err, fs.ErrNotExist) {
		if r.evictErr == nil {
			r.evictErr = fmt.Errorf("delete segment %d: %w", seg.seq, err)
		}
		return 0, err
	}
	r.segs = r.segs[1:]
	r.bytes -= seg.size
	r.led.mu.Lock()
	for _, id := range r.ids[:seg.events] {
		if id != nil {
			delete(r.led.retained, *id)
		}
	}
	r.led.mu.Unlock()
	r.view.Lock()
	r.events = append([]wsaudit.Envelope(nil), r.events[seg.events:]...)
	r.ids = append([]*wsaudit.SourceID(nil), r.ids[seg.events:]...)
	r.view.Unlock()
	return seg.size, nil
}

// trimTo drops oldest segments until the ring holds at most capBytes,
// always keeping the newest segment.
func (r *ring) trimTo(capBytes int64) int64 {
	var evicted int64
	for r.bytes > capBytes && len(r.segs) > 1 {
		dropped, err := r.dropOldest()
		if err != nil {
			break
		}
		evicted += dropped
	}
	return evicted
}

func (r *ring) close() {
	if r.file != nil {
		_ = closeFile(r.file)
		r.file = nil
	}
}
