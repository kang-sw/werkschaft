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
	V       int               `json:"v,omitempty"`
	Session string            `json:"session,omitempty"`
	State   *headerState      `json:"state,omitempty"`
	Event   *wsaudit.Envelope `json:"e,omitempty"`
	// Loss ("loss"): a source record finalized without being retained.
	Lost *wsaudit.SourceID `json:"lost,omitempty"`
	// Boundary ("fin"): a source's finalized boundary advanced past its last
	// retained record.
	Source string `json:"src,omitempty"`
	Seq    uint64 `json:"seq,omitempty"`
	// Seen ("seen"): a lifecycle last-seen time.
	At string `json:"at,omitempty"`
}

const (
	lineHeader = "h"
	lineEvent  = "e"
	lineLoss   = "loss"
	lineFin    = "fin"
)

type headerState struct {
	wsaudit.Recovery
	Lost     []wsaudit.SourceID `json:"lost,omitempty"`
	LastSeen string             `json:"last_seen,omitempty"`
}

type segment struct {
	seq    uint64
	size   int64
	events int // retained events that live in this segment
}

type sourceState struct {
	last     wsaudit.SourceID
	hasLast  bool
	boundary uint64
}

// ring is one session's audit segment ring with its in-memory index. The
// store's writer goroutine is its only mutator; reads take mu.
type ring struct {
	session string
	dir     string

	segs        []segment
	file        *os.File // newest segment, held open for appends
	forceRotate bool
	readOnly    error

	events   []wsaudit.Envelope
	maxSeq   uint64
	sources  map[string]*sourceState
	retained map[wsaudit.SourceID]struct{}
	lost     map[wsaudit.SourceID]struct{}
	loss     uint64
	pending  [][]byte // control lines written ahead of the next record
	lastSeen time.Time
	bytes    int64
}

func newRing(session, dir string) *ring {
	return &ring{
		session:  session,
		dir:      dir,
		sources:  map[string]*sourceState{},
		retained: map[wsaudit.SourceID]struct{}{},
		lost:     map[wsaudit.SourceID]struct{}{},
	}
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
		good, newer := r.replaySegment(&segs[i], raw, i == 0)
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

// replaySegment applies one segment's lines. It returns the byte length of
// the well-formed prefix and whether the header names a newer format.
func (r *ring) replaySegment(seg *segment, raw []byte, oldest bool) (good int, newer bool) {
	pos := 0
	first := true
	for pos < len(raw) {
		nl := bytes.IndexByte(raw[pos:], '\n')
		if nl < 0 {
			return pos, false // torn last line
		}
		text := raw[pos : pos+nl]
		next := pos + nl + 1
		var ln line
		if err := json.Unmarshal(text, &ln); err != nil {
			if next == len(raw) {
				return pos, false
			}
			pos = next // a damaged line mid-ring is skipped
			continue
		}
		if first {
			first = false
			if ln.T == lineHeader {
				if ln.V > RingSchemaVersion {
					return pos, true
				}
				if oldest && ln.State != nil {
					r.seed(ln.State)
				}
				pos = next
				continue
			}
		}
		r.apply(ln, seg)
		pos = next
	}
	return pos, false
}

// seed starts recovery from the oldest retained segment's header state.
func (r *ring) seed(st *headerState) {
	r.maxSeq = max(r.maxSeq, st.MaxSeq)
	r.loss = max(r.loss, st.Loss)
	for src, s := range st.Sources {
		r.sources[src] = &sourceState{last: s.Last, hasLast: s.Last != (wsaudit.SourceID{}), boundary: s.Boundary}
	}
	for _, id := range st.Lost {
		r.lost[id] = struct{}{}
	}
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
		r.index(*ln.Event)
		if seg != nil {
			seg.events++
		}
	case lineLoss:
		if ln.Lost != nil {
			r.markLost(*ln.Lost)
		}
	case lineFin:
		r.source(ln.Source).boundary = max(r.source(ln.Source).boundary, ln.Seq)
	}
	if t, err := time.Parse(time.RFC3339Nano, ln.At); err == nil && t.After(r.lastSeen) {
		r.lastSeen = t
	}
}

func (r *ring) source(name string) *sourceState {
	s := r.sources[name]
	if s == nil {
		s = &sourceState{}
		r.sources[name] = s
	}
	return s
}

// index adds a retained event to the in-memory index.
func (r *ring) index(ev wsaudit.Envelope) {
	r.events = append(r.events, ev)
	r.maxSeq = max(r.maxSeq, ev.Seq)
	if ev.At.After(r.lastSeen) {
		r.lastSeen = ev.At
	}
	if ev.Source != nil {
		id := *ev.Source
		s := r.source(id.Source)
		if !s.hasLast || id.Compare(s.last) > 0 {
			s.last, s.hasLast = id, true
		}
		s.boundary = max(s.boundary, id.Seq)
		r.retained[id] = struct{}{}
		delete(r.lost, id)
	}
}

func (r *ring) markLost(id wsaudit.SourceID) {
	if _, ok := r.lost[id]; ok {
		return
	}
	r.lost[id] = struct{}{}
	r.loss++
	s := r.source(id.Source)
	s.boundary = max(s.boundary, id.Seq)
}

// Verdicts of admit.
const (
	admitAppend = iota
	admitDuplicate
	admitLoss
)

// admit decides an incoming envelope's fate by its source identity: an
// identity at or below the source's last retained one is a duplicate (or a
// known loss); one at or below the finalized boundary without a retained
// identity is a loss, never appended late.
func (r *ring) admit(id *wsaudit.SourceID) int {
	if id == nil {
		return admitAppend
	}
	if _, ok := r.retained[*id]; ok {
		return admitDuplicate
	}
	if _, ok := r.lost[*id]; ok {
		return admitDuplicate
	}
	s := r.sources[id.Source]
	if s == nil {
		return admitAppend
	}
	if s.hasLast && id.Compare(s.last) <= 0 {
		return admitDuplicate
	}
	if id.Seq <= s.boundary {
		return admitLoss
	}
	return admitAppend
}

func (r *ring) recovery() wsaudit.Recovery {
	rec := wsaudit.Recovery{MaxSeq: r.maxSeq, Loss: r.loss}
	if len(r.sources) > 0 {
		rec.Sources = make(map[string]wsaudit.SourceState, len(r.sources))
		for name, s := range r.sources {
			rec.Sources[name] = wsaudit.SourceState{Last: s.last, Boundary: s.boundary}
		}
	}
	return rec
}

func (r *ring) headerLine() ([]byte, error) {
	st := headerState{Recovery: r.recovery()}
	for id := range r.lost {
		if s := r.sources[id.Source]; s == nil || !s.hasLast || id.Compare(s.last) > 0 {
			st.Lost = append(st.Lost, id)
		}
	}
	sort.Slice(st.Lost, func(i, j int) bool {
		if st.Lost[i].Source != st.Lost[j].Source {
			return st.Lost[i].Source < st.Lost[j].Source
		}
		return st.Lost[i].Compare(st.Lost[j]) < 0
	})
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
// when it would overflow segBytes and dropping the oldest segments while the
// ring would exceed capBytes. It returns the bytes removed by eviction.
func (r *ring) write(payload []byte, segBytes, capBytes int64) (evicted int64, err error) {
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
		evicted += r.dropOldest()
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
// The newest segment is never dropped.
func (r *ring) dropOldest() int64 {
	if len(r.segs) < 2 {
		return 0
	}
	seg := r.segs[0]
	_ = removeFile(filepath.Join(r.dir, segmentName(seg.seq)))
	r.segs = r.segs[1:]
	r.bytes -= seg.size
	for _, ev := range r.events[:seg.events] {
		if ev.Source != nil {
			delete(r.retained, *ev.Source)
		}
	}
	r.events = append([]wsaudit.Envelope(nil), r.events[seg.events:]...)
	return seg.size
}

// trimTo drops oldest segments until the ring holds at most capBytes,
// always keeping the newest segment.
func (r *ring) trimTo(capBytes int64) int64 {
	var evicted int64
	for r.bytes > capBytes && len(r.segs) > 1 {
		evicted += r.dropOldest()
	}
	return evicted
}

func (r *ring) close() {
	if r.file != nil {
		_ = closeFile(r.file)
		r.file = nil
	}
}
