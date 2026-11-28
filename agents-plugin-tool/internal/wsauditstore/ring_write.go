package wsauditstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

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
// already within its own cap. A rotation writes the new segment's header on
// its own before anything is dropped: until that header has landed, the
// older segments are the only recovery point (their header snapshots and
// boundaries), so a failed header write returns with nothing dropped and a
// crash leaves them on disk. It returns the bytes removed by its own trim.
func (r *ring) write(payload []byte, segBytes, capBytes int64, room func(n int64)) (evicted int64, err error) {
	if r.readOnly != nil {
		return 0, r.readOnly
	}
	n := int64(len(payload))
	cur := len(r.segs) - 1
	if r.file == nil && !r.forceRotate && cur >= 0 && r.segs[cur].size+n <= segBytes {
		f, err := openAppend(filepath.Join(r.dir, segmentName(r.segs[cur].seq)))
		if err != nil {
			return 0, err
		}
		r.file = f
	}
	if r.file == nil || r.forceRotate || cur < 0 || r.segs[cur].size+n > segBytes {
		header, err := r.headerLine()
		if err != nil {
			return 0, err
		}
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
		r.segs = append(r.segs, segment{seq: seq})
		written, werr := writeFile(f, header)
		r.segs[len(r.segs)-1].size += int64(written)
		r.bytes += int64(written)
		if werr != nil {
			// The rotation is retried by the next write; the torn segment
			// stays accounted and is dropped like any other once a later
			// header lands.
			r.forceRotate = true
			return 0, werr
		}
		r.forceRotate = false
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
	if n == 0 {
		return evicted, nil // a header-only rotation
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
	r.cut = true
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
