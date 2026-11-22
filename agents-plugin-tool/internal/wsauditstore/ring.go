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
	// and why.
	Reason string `json:"reason,omitempty"`
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
	// LostFloor is, per source, the position at or below which every
	// identity not retained is already counted lost (see ledger.floor).
	LostFloor map[string]uint64 `json:"lost_floor,omitempty"`
	LastSeen  string            `json:"last_seen,omitempty"`
}

// lostBudget bounds the encoded size of a header's Lost list; past it the
// lowest positions fold into LostFloor.
const lostBudget = 32 << 10

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
	// floor is, per source, a boundary (in the covered sense) through which
	// every identity not retained is already counted lost: identities folded
	// out of lost to keep the header bounded. It never exceeds the source's
	// finalized boundary unless uncovered identities had to fold.
	floor map[string]uint64
	// forced counts uncovered identities folded into a floor, for the
	// writer's log.
	forced int
}

func newLedger() *ledger {
	return &ledger{
		bounds:   map[string]uint64{},
		retained: map[wsaudit.SourceID]struct{}{},
		lost:     map[wsaudit.SourceID]int{},
		floor:    map[string]uint64{},
	}
}

// settled reports whether id is at or below its source's lost floor: already
// accounted for, so it is neither appended nor counted again. l.mu is held.
func (l *ledger) settled(id wsaudit.SourceID) bool {
	return covered(id, l.floor[id.Source])
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

// fold keeps the lost set within lostBudget by folding the lowest positions
// into the per-source floor: covered identities first (the floor then never
// passes the boundary), uncovered ones only when covered ones do not suffice.
// It reports the uncovered identities folded. l.mu is held.
func (l *ledger) fold() (forced int) {
	if l.lostBytes <= lostBudget {
		return 0
	}
	var cands []wsaudit.SourceID
	for id := range l.lost {
		if id.Key == "" {
			cands = append(cands, id)
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		ci, cj := covered(cands[i], l.bounds[cands[i].Source]), covered(cands[j], l.bounds[cands[j].Source])
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
		if _, ok := l.lost[id]; !ok {
			continue
		}
		if !covered(id, l.bounds[id.Source]) {
			forced++
		}
		f := id.Seq
		if id.Tee {
			f = id.Anchor + 1 // a tee is covered by a floor above its anchor
		}
		l.floor[id.Source] = max(l.floor[id.Source], f)
		for other := range l.lost {
			if other.Source == id.Source && l.settled(other) {
				l.dropLost(other)
			}
		}
	}
	return forced
}

// covered reports whether id is at or below its source's finalized boundary
// b: a sequenced record at or below b, or a tee record anchored below b (a
// tee line sits after its anchor). A daemon-synthesized (Key) record is never
// covered.
func covered(id wsaudit.SourceID, b uint64) bool {
	if b == 0 || id.Key != "" {
		return false
	}
	if id.Tee {
		return id.Anchor < b
	}
	return id.Seq <= b
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

// headReadBytes bounds how much of a segment newerRingOnDisk reads; the
// version is the header's second key.
const headReadBytes = 4 << 10

// newerRingOnDisk reports whether any segment of the ring at dir opens with a
// header of a newer format, reading only each segment's head. Age-out and
// capacity eviction never delete such a ring (Decision 4, forward safety).
func newerRingOnDisk(dir string) bool {
	segs, _, err := listSegments(dir)
	if err != nil {
		return false
	}
	for _, seg := range segs {
		head, err := readHead(filepath.Join(dir, segmentName(seg.seq)), headReadBytes)
		if err == nil && headerVersion(head) > RingSchemaVersion {
			return true
		}
	}
	return false
}

// headerVersion is the "v" of a header line at the start of head, read token
// by token so a torn or long line still yields it; 0 when head does not open
// with a header object carrying one.
func headerVersion(head []byte) int {
	dec := json.NewDecoder(bytes.NewReader(head))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return 0
	}
	header := false
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return 0
		}
		switch key {
		case "t":
			var t string
			if dec.Decode(&t) != nil {
				return 0
			}
			header = t == lineHeader
		case "v":
			var v int
			if dec.Decode(&v) != nil || !header {
				return 0
			}
			return v
		default:
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return 0
			}
		}
	}
	return 0
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
	r.led.forced += r.led.fold() // headers' unions can carry settled identities
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
// alone — back to the newest one opening with a header, whose snapshot holds
// everything before it — for an unloaded ring about to be evicted. It
// returns nil when nothing is readable.
func recoverTail(session, dir string) *ring {
	segs, _, err := listSegments(dir)
	if err != nil || len(segs) == 0 {
		return nil
	}
	raws := make([][]byte, len(segs))
	start := len(segs) - 1
	for ; start >= 0; start-- {
		raw, err := readFile(filepath.Join(dir, segmentName(segs[start].seq)))
		if err != nil {
			continue
		}
		raws[start] = raw
		if headerVersion(raw) > 0 {
			break
		}
	}
	start = max(start, 0)
	r := newRing(session, dir)
	for i := start; i < len(segs); i++ {
		if raws[i] != nil {
			r.replaySegment(&segs[i], raws[i])
		}
	}
	return r
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
				if ln.V > RingSchemaVersion {
					return pos, true, header
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
			r.markLost(*ln.ID)
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
	r.events = append(r.events, ev)
	r.ids = append(r.ids, id)
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
	l.forced += l.fold()
	return true
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
	l.bounds[source] = b
	return true
}

// Verdicts of admit.
const (
	admitAppend = iota
	admitDuplicate
	admitLoss
)

// admit decides an incoming envelope's fate by its source identity: an
// identity already retained or already recorded lost is a duplicate; one
// covered by its source's finalized boundary is a loss, never appended late
// (the same rule capture applies to its recovered boundary, so an early,
// incomplete recovery answer cannot cause a duplicate or a late append).
func (r *ring) admit(id *wsaudit.SourceID) int {
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
	if covered(*id, l.bounds[id.Source]) {
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
	r.events = append([]wsaudit.Envelope(nil), r.events[seg.events:]...)
	r.ids = append([]*wsaudit.SourceID(nil), r.ids[seg.events:]...)
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
