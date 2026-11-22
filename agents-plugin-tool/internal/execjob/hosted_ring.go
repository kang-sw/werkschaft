package execjob

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/textreader"
)

// Stream names one output stream of a job.
type Stream int

const (
	StreamCombined Stream = iota // interleaved stdout+stderr in arrival order
	StreamStdout
	StreamStderr
)

const streamCount = 3

func (s Stream) String() string {
	switch s {
	case StreamCombined:
		return "combined"
	case StreamStdout:
		return "stdout"
	case StreamStderr:
		return "stderr"
	default:
		return fmt.Sprintf("Stream(%d)", int(s))
	}
}

func (s Stream) valid() bool { return s >= StreamCombined && s <= StreamStderr }

// ParseStream resolves a reader's stream argument. An empty name selects
// stdout, matching the legacy file-backed readers.
func ParseStream(name string) (Stream, string, error) {
	if strings.TrimSpace(name) == "" {
		name = "stdout"
	}
	switch name {
	case "stdout":
		return StreamStdout, name, nil
	case "stderr":
		return StreamStderr, name, nil
	case "combined":
		return StreamCombined, name, nil
	default:
		return StreamStdout, "", fmt.Errorf("invalid stream %q", name)
	}
}

// ringChunkMax bounds one stored record, so trimming the front of a record
// pins at most this much evicted backing memory per job.
const ringChunkMax = 64 << 10

// ringRecord is one run of bytes from one stream, stored once. A record from
// Append belongs to the combined stream and to its own stream; a restored
// preview record belongs to exactly one of them.
type ringRecord struct {
	seq      uint64 // global arrival order across jobs
	stream   Stream // StreamStdout, StreamStderr, or StreamCombined for a restored combined-only tail
	combined bool   // part of the combined stream
	cstart   int64  // absolute combined offset of data[0] (when combined)
	sstart   int64  // absolute own-stream offset of data[0] (when stream != StreamCombined)
	data     []byte // never mutated in place once stored; only resliced or extended
}

func (rec *ringRecord) in(s Stream) bool {
	if s == StreamCombined {
		return rec.combined
	}
	return rec.stream == s
}

func (rec *ringRecord) start(s Stream) int64 {
	if s == StreamCombined {
		return rec.cstart
	}
	return rec.sstart
}

// OutputRings bounds every hosted job's retained output: each job keeps at
// most perJob bytes (counted on the combined stream), and all jobs together
// at most total bytes; the oldest bytes are evicted first. A non-positive
// bound disables that bound. All methods, and the methods of every JobOutput
// it creates, are safe for concurrent use.
type OutputRings struct {
	mu     sync.Mutex
	perJob int64
	total  int64
	used   int64
	seq    uint64
	last   *JobOutput // job of the globally newest record, for in-place merging
	jobs   map[*JobOutput]struct{}
}

func NewOutputRings(perJob, total int64) *OutputRings {
	return &OutputRings{perJob: perJob, total: total, jobs: map[*JobOutput]struct{}{}}
}

// NewJob registers an empty job. The job counts toward the aggregate until
// Release.
func (r *OutputRings) NewJob() *JobOutput {
	j := &JobOutput{rings: r}
	r.mu.Lock()
	r.jobs[j] = struct{}{}
	r.mu.Unlock()
	return j
}

// Used reports the bytes retained across all live jobs.
func (r *OutputRings) Used() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.used
}

// JobOutput is one job's retained output. Offsets are absolute byte offsets in
// the job's whole stream since it started, so eviction never renumbers them.
type JobOutput struct {
	rings        *OutputRings
	recs         []ringRecord // recs[head:] are retained, oldest first
	head         int
	retained     int64
	written      [streamCount]int64
	evicted      [streamCount]int64
	evictedLines [streamCount]int64 // newlines in evicted bytes, for absolute line numbers
	released     bool
}

type Slice struct {
	Stream  Stream
	Data    []byte // raw retained bytes
	Start   int64  // absolute offset of Data[0] in the stream
	End     int64  // Start+len(Data)
	Written int64  // total bytes the stream ever received
	Evicted int64  // absolute offset below which bytes are gone (0 when nothing evicted)
}

// Append records data from s (StreamStdout or StreamStderr) once, in arrival
// order, then enforces both bounds. Other streams, empty data, and released
// jobs are ignored. data is copied; the caller may reuse it.
func (r *OutputRings) Append(j *JobOutput, s Stream, data []byte) {
	if len(data) == 0 || (s != StreamStdout && s != StreamStderr) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if j.released {
		return
	}
	if limit := r.limit(); limit > 0 && int64(len(data)) > limit {
		// The new bytes alone fill the bound: everything older goes, and the
		// prefix that would be evicted at once is never copied.
		for j.head < len(j.recs) {
			r.evictFront(j, math.MaxInt64)
		}
		drop := int64(len(data)) - limit
		lines := int64(bytes.Count(data[:drop], newline))
		for _, st := range [...]Stream{StreamCombined, s} {
			j.written[st] += drop
			j.evicted[st] = j.written[st]
			j.evictedLines[st] += lines
		}
		data = data[drop:]
	}
	n := int64(len(data))
	if r.last == j && j.head < len(j.recs) {
		if rec := &j.recs[len(j.recs)-1]; rec.combined && rec.stream == s && len(rec.data) < ringChunkMax {
			// No other job appended since this record: extending it keeps
			// global arrival order exact. Bytes past a reader's snapshot
			// length are never read by that reader.
			room := ringChunkMax - len(rec.data)
			if room > len(data) {
				room = len(data)
			}
			rec.data = append(rec.data, data[:room]...)
			data = data[room:]
		}
	}
	cstart := j.written[StreamCombined] + n - int64(len(data))
	sstart := j.written[s] + n - int64(len(data))
	r.pushChunks(j, ringRecord{stream: s, combined: true, cstart: cstart, sstart: sstart}, data)
	j.written[StreamCombined] += n
	j.written[s] += n
	j.retained += n
	r.used += n
	r.last = j
	r.enforce(j)
}

// Release drops all of j's retained bytes from the aggregate and forgets the
// job; later appends to it are ignored. Its marks stay readable, with every
// stream fully evicted.
func (r *OutputRings) Release(j *JobOutput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j.released {
		return
	}
	for j.head < len(j.recs) {
		r.evictFront(j, math.MaxInt64)
	}
	j.recs, j.head = nil, 0
	j.released = true
	delete(r.jobs, j)
	if r.last == j {
		r.last = nil
	}
}

// Restore seeds a fresh JobOutput from a persisted preview: retained
// combined/stdout/stderr tails plus the absolute Written totals (indexed by
// Stream), so offsets and evicted marks stay right after a daemon restart.
// The preview carries no line counts, so Grep numbers restored lines from 1
// at each stream's retained window. Any previously retained bytes are
// dropped.
func (r *OutputRings) Restore(j *JobOutput, written [3]int64, stdoutTail, stderrTail, combinedTail []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j.released {
		return
	}
	for j.head < len(j.recs) {
		r.evictFront(j, math.MaxInt64)
	}
	j.recs, j.head = nil, 0
	j.evictedLines = [streamCount]int64{}
	tails := [streamCount][]byte{combinedTail, stdoutTail, stderrTail}
	for s := range tails {
		if written[s] < int64(len(tails[s])) {
			written[s] = int64(len(tails[s]))
		}
		start := written[s] - int64(len(tails[s]))
		j.written[s] = written[s]
		j.evicted[s] = start
		if len(tails[s]) == 0 {
			continue
		}
		tmpl := ringRecord{stream: Stream(s)}
		if Stream(s) == StreamCombined {
			tmpl.combined, tmpl.cstart = true, start
		} else {
			tmpl.sstart = start
		}
		r.pushChunks(j, tmpl, tails[s])
		j.retained += int64(len(tails[s]))
		r.used += int64(len(tails[s]))
	}
	r.last = nil
	r.enforce(j)
}

// limit is the tightest enabled bound, or 0 when none is.
func (r *OutputRings) limit() int64 {
	switch {
	case r.perJob > 0 && r.total > 0:
		return min(r.perJob, r.total)
	case r.perJob > 0:
		return r.perJob
	default:
		return r.total
	}
}

// pushChunks stores data as records of at most ringChunkMax bytes, each with
// its own arrival sequence. tmpl supplies stream membership and the starting
// offsets.
func (r *OutputRings) pushChunks(j *JobOutput, tmpl ringRecord, data []byte) {
	for len(data) > 0 {
		k := min(len(data), ringChunkMax)
		r.seq++
		rec := tmpl
		rec.seq = r.seq
		rec.data = append([]byte(nil), data[:k]...)
		j.recs = append(j.recs, rec)
		tmpl.cstart += int64(k)
		tmpl.sstart += int64(k)
		data = data[k:]
	}
}

func (r *OutputRings) enforce(j *JobOutput) {
	if r.perJob > 0 {
		for j.retained > r.perJob {
			r.evictFront(j, j.retained-r.perJob)
		}
	}
	if r.total > 0 {
		for r.used > r.total {
			oldest := r.oldest()
			if oldest == nil {
				return
			}
			r.evictFront(oldest, r.used-r.total)
		}
	}
}

// oldest returns the live job whose front record arrived first. Jobs are few
// (bounded by the job list cap), so a scan beats maintaining a heap.
func (r *OutputRings) oldest() *JobOutput {
	var best *JobOutput
	var bestSeq uint64
	for j := range r.jobs {
		if j.head >= len(j.recs) {
			continue
		}
		if seq := j.recs[j.head].seq; best == nil || seq < bestSeq {
			best, bestSeq = j, seq
		}
	}
	return best
}

var newline = []byte{'\n'}

// evictFront drops up to limit bytes from the front of j's oldest record and
// advances the evicted marks of the streams that record belongs to.
func (r *OutputRings) evictFront(j *JobOutput, limit int64) {
	rec := &j.recs[j.head]
	k := int64(len(rec.data))
	if limit < k {
		k = limit
	}
	lines := int64(bytes.Count(rec.data[:k], newline))
	if rec.combined {
		j.evicted[StreamCombined] = rec.cstart + k
		j.evictedLines[StreamCombined] += lines
	}
	if rec.stream != StreamCombined {
		j.evicted[rec.stream] = rec.sstart + k
		j.evictedLines[rec.stream] += lines
	}
	rec.data = rec.data[k:]
	rec.cstart += k
	rec.sstart += k
	j.retained -= k
	r.used -= k
	if len(rec.data) == 0 {
		j.recs[j.head] = ringRecord{}
		j.head++
		j.compact()
	}
}

func (j *JobOutput) compact() {
	switch {
	case j.head == len(j.recs):
		j.recs, j.head = j.recs[:0], 0
	case j.head >= 32 && j.head*2 >= len(j.recs):
		n := copy(j.recs, j.recs[j.head:])
		clear(j.recs[n:])
		j.recs, j.head = j.recs[:n], 0
	}
}

func (j *JobOutput) Written(s Stream) int64 {
	if !s.valid() {
		return 0
	}
	j.rings.mu.Lock()
	defer j.rings.mu.Unlock()
	return j.written[s]
}

func (j *JobOutput) Evicted(s Stream) int64 {
	if !s.valid() {
		return 0
	}
	j.rings.mu.Lock()
	defer j.rings.mu.Unlock()
	return j.evicted[s]
}

// Tail returns the last <= max bytes of s (max <= 0 means InlineBudget). When
// the cut falls inside a multi-byte UTF-8 sequence, the start moves forward
// to the next rune boundary (at most 3 bytes).
func (j *JobOutput) Tail(s Stream, max int) Slice {
	if !s.valid() {
		return Slice{Stream: s}
	}
	if max <= 0 {
		max = InlineBudget
	}
	j.rings.mu.Lock()
	written, evicted := j.written[s], j.evicted[s]
	start := written - int64(max)
	if start < evicted {
		start = evicted
	}
	pre := start - (utf8.UTFMax - 1)
	if pre < evicted {
		pre = evicted
	}
	buf := j.copyRange(s, pre, written)
	j.rings.mu.Unlock()
	lead := int(start - pre)
	skip := runeAlign(buf, lead, start > 0)
	return Slice{Stream: s, Data: buf[lead+skip:], Start: start + int64(skip), End: written, Written: written, Evicted: evicted}
}

// runeAlign returns how many bytes past buf[lead] the next rune boundary is.
// buf[:lead] holds retained bytes just before the cut; cut reports whether the
// stream had any bytes before buf[lead].
func runeAlign(buf []byte, lead int, cut bool) int {
	for k := 1; k <= lead; k++ {
		if utf8.RuneStart(buf[lead-k]) {
			if _, size := utf8.DecodeRune(buf[lead-k:]); size > k {
				return size - k
			}
			return 0
		}
	}
	if !cut || lead >= utf8.UTFMax-1 {
		return 0
	}
	// The sequence's lead byte is evicted: skip its orphaned continuation bytes.
	n := 0
	for lead+n < len(buf) && lead+n < utf8.UTFMax-1 && !utf8.RuneStart(buf[lead+n]) {
		n++
	}
	return n
}

// Read returns bytes [max(offset, Evicted), +limit) of s. An offset past
// Written clamps to Written; limit <= 0 means textreader.DefaultReadLimit, and
// limit is capped at textreader.MaxReadLimit.
func (j *JobOutput) Read(s Stream, offset, limit int64) Slice {
	if !s.valid() {
		return Slice{Stream: s}
	}
	if limit <= 0 {
		limit = textreader.DefaultReadLimit
	}
	if limit > textreader.MaxReadLimit {
		limit = textreader.MaxReadLimit
	}
	j.rings.mu.Lock()
	defer j.rings.mu.Unlock()
	written, evicted := j.written[s], j.evicted[s]
	start := max(offset, evicted)
	if start > written {
		start = written
	}
	end := min(start+limit, written)
	return Slice{Stream: s, Data: j.copyRange(s, start, end), Start: start, End: end, Written: written, Evicted: evicted}
}

// copyRange copies the retained bytes of s in [from, to). Caller holds the
// rings lock and passes a range inside [Evicted, Written].
func (j *JobOutput) copyRange(s Stream, from, to int64) []byte {
	out := make([]byte, 0, to-from)
	if from >= to {
		return out
	}
	first := j.head
	for i := len(j.recs) - 1; i >= j.head; i-- {
		if rec := &j.recs[i]; rec.in(s) && rec.start(s) <= from {
			first = i
			break
		}
	}
	for i := first; i < len(j.recs); i++ {
		rec := &j.recs[i]
		if !rec.in(s) {
			continue
		}
		rs := rec.start(s)
		if rs >= to {
			break
		}
		re := rs + int64(len(rec.data))
		if re <= from {
			continue
		}
		out = append(out, rec.data[max(from, rs)-rs:min(to, re)-rs]...)
	}
	return out
}

// snapshot captures the retained record bytes of s without copying them; the
// stored bytes are never mutated, so they are safe to scan after unlocking.
func (j *JobOutput) snapshot(s Stream) (parts [][]byte, size int, evictedLines int64) {
	j.rings.mu.Lock()
	defer j.rings.mu.Unlock()
	for i := j.head; i < len(j.recs); i++ {
		if rec := &j.recs[i]; rec.in(s) {
			parts = append(parts, rec.data)
			size += len(rec.data)
		}
	}
	return parts, size, j.evictedLines[s]
}

// TailLines applies textreader.Tail's contract to the retained bytes of s.
func (j *JobOutput) TailLines(s Stream, lines int) string {
	if !s.valid() {
		return ""
	}
	parts, size, _ := j.snapshot(s)
	// The line bound covers the whole snapshot, so scanning cannot fail.
	out, _ := textreader.TailReader(&partsReader{parts: parts}, lines, size+1)
	return out
}

// Grep applies textreader.Grep's semantics to the retained bytes of s. Line
// numbers are absolute: newlines evicted before the retained window still
// count, so line N stays line N after eviction.
func (j *JobOutput) Grep(s Stream, pattern string, before, after, max int, regex bool) (textreader.GrepResult, error) {
	if !s.valid() {
		return textreader.GrepResult{}, fmt.Errorf("invalid stream %q", s.String())
	}
	parts, size, evictedLines := j.snapshot(s)
	lines, err := textreader.ReadLines(&partsReader{parts: parts}, size+1)
	if err != nil {
		return textreader.GrepResult{}, err
	}
	return textreader.GrepLines("", lines, int(evictedLines)+1, pattern, before, after, max, regex)
}

// partsReader reads a sequence of byte slices as one stream.
type partsReader struct {
	parts [][]byte
}

func (p *partsReader) Read(b []byte) (int, error) {
	for len(p.parts) > 0 && len(p.parts[0]) == 0 {
		p.parts = p.parts[1:]
	}
	if len(p.parts) == 0 {
		return 0, io.EOF
	}
	n := copy(b, p.parts[0])
	p.parts[0] = p.parts[0][n:]
	return n, nil
}

// DecodeText validates UTF-8: it returns the text with each invalid byte
// replaced by U+FFFD and reports whether any replacement happened. Raw bytes
// stay untouched in the ring.
func DecodeText(b []byte) (text string, replaced bool) {
	if utf8.Valid(b) {
		return string(b), false
	}
	var sb strings.Builder
	sb.Grow(len(b) + 8)
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			sb.WriteRune(utf8.RuneError)
		} else {
			sb.Write(b[:size])
		}
		b = b[size:]
	}
	return sb.String(), true
}
