package wsauditstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

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
	if len(segs) > 0 && segs[0].seq > 1 {
		r.view.Lock()
		r.cut = true
		r.view.Unlock()
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
