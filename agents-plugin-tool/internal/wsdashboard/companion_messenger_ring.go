package wsdashboard

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Companion Messenger history persistence (performance.md storage type 1: an
// append-only log as a segment ring). The CompanionPhoneStore stays the
// source of truth and the hot path; each history change is queued here under
// the store lock without blocking and written by one goroutine that holds the
// segment file open. Every segment opens with a snapshot line, so retention
// deletes whole old segments and recovery reads only the newest segment that
// starts with a snapshot. The log is read once, at daemon start, to hydrate
// the store. Unread markers are not persisted: hydrated history reads as seen.
const (
	companionRingDirName      = "companion-messenger"
	companionRingSegmentMax   = 256 << 10
	companionRingKeepSegments = 2
	companionRingSegmentExt   = ".jsonl"
	companionRingSegmentPre   = "seg-"
	companionRingMaxLine      = 1 << 20

	companionRingSnapshot     = "snapshot"
	companionRingMessage      = "msg"
	companionRingSummary      = "summary"
	companionRingResetSummary = "reset_summary"
)

// companionMessengerRecord is one log line. A snapshot carries the whole
// bounded history; a delta carries one change plus the post-change scalars.
type companionMessengerRecord struct {
	Type      string                  `json:"type"`
	MsgSeq    uint64                  `json:"msgSeq,omitempty"`
	Turns     int                     `json:"turns,omitempty"`
	Message   *CompanionPhoneMessage  `json:"message,omitempty"`
	Summary   string                  `json:"summary,omitempty"`
	Messages  []CompanionPhoneMessage `json:"messages,omitempty"`
	Summaries []string                `json:"summaries,omitempty"`
}

// companionMessengerHistory is the bounded state a snapshot records and the
// store hydrates from.
type companionMessengerHistory struct {
	MsgSeq    uint64
	Turns     int
	Messages  []CompanionPhoneMessage
	Summaries []string
}

func (h *companionMessengerHistory) apply(rec companionMessengerRecord) {
	switch rec.Type {
	case companionRingSnapshot:
		h.Messages = append([]CompanionPhoneMessage(nil), rec.Messages...)
		h.Summaries = append([]string(nil), rec.Summaries...)
	case companionRingMessage:
		if rec.Message != nil {
			h.Messages = appendBoundedMessage(h.Messages, *rec.Message, companionPhoneMaxMessages)
		}
	case companionRingSummary:
		h.Summaries = appendBoundedSummary(h.Summaries, rec.Summary, companionPhoneMaxSummaries)
	case companionRingResetSummary:
		h.Summaries = nil
	default:
		return
	}
	if rec.MsgSeq > h.MsgSeq {
		h.MsgSeq = rec.MsgSeq
	}
	h.Turns = rec.Turns
}

func (h *companionMessengerHistory) snapshotRecord() companionMessengerRecord {
	return companionMessengerRecord{
		Type:      companionRingSnapshot,
		MsgSeq:    h.MsgSeq,
		Turns:     h.Turns,
		Messages:  append([]CompanionPhoneMessage(nil), h.Messages...),
		Summaries: append([]string(nil), h.Summaries...),
	}
}

type companionMessengerRing struct {
	dir  string
	logf func(string, ...any)

	mu      sync.Mutex
	pending []companionMessengerRecord
	closed  bool
	signal  chan struct{}
	done    chan struct{}
	exited  chan struct{}

	// segmentMax is the rotation threshold (companionRingSegmentMax; tests
	// lower it). Fixed before the writer starts.
	segmentMax int64

	// Owned by the writer goroutine (and by open before it starts).
	file     *os.File
	segment  uint64
	size     int64
	snapSize int64
	history  companionMessengerHistory
}

// openCompanionMessengerRing recovers the newest valid segment under dir,
// starts a fresh segment holding its snapshot, prunes old segments, and
// starts the writer. It returns the recovered history to hydrate the store.
func openCompanionMessengerRing(dir string, logf func(string, ...any)) (*companionMessengerRing, companionMessengerHistory, error) {
	return openCompanionMessengerRingCap(dir, logf, companionRingSegmentMax)
}

func openCompanionMessengerRingCap(dir string, logf func(string, ...any), segmentMax int64) (*companionMessengerRing, companionMessengerHistory, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, companionMessengerHistory{}, err
	}
	segs, err := companionRingSegments(dir)
	if err != nil {
		return nil, companionMessengerHistory{}, err
	}
	var hist companionMessengerHistory
	for i := len(segs) - 1; i >= 0; i-- {
		h, ok, err := readCompanionRingSegment(filepath.Join(dir, companionRingSegmentName(segs[i])))
		if err != nil {
			logf("companion messenger history: read segment %d: %v", segs[i], err)
			continue
		}
		if ok {
			hist = h
			break
		}
	}
	var next uint64 = 1
	if len(segs) > 0 {
		next = segs[len(segs)-1] + 1
	}
	r := &companionMessengerRing{
		dir:     dir,
		logf:    logf,
		signal:  make(chan struct{}, 1),
		done:    make(chan struct{}),
		exited:  make(chan struct{}),
		history: hist,

		segmentMax: segmentMax,
	}
	if err := r.startSegment(next); err != nil {
		return nil, companionMessengerHistory{}, err
	}
	go r.run()
	return r, hist, nil
}

func companionRingSegmentName(n uint64) string {
	return fmt.Sprintf("%s%016d%s", companionRingSegmentPre, n, companionRingSegmentExt)
}

// companionRingSegments lists segment numbers under dir in ascending order.
func companionRingSegments(dir string) ([]uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var segs []uint64
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, companionRingSegmentPre) || !strings.HasSuffix(name, companionRingSegmentExt) {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(name, companionRingSegmentPre), companionRingSegmentExt), 10, 64)
		if err != nil {
			continue
		}
		segs = append(segs, n)
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i] < segs[j] })
	return segs, nil
}

// readCompanionRingSegment replays one segment. ok is false unless its first
// line is a snapshot. A torn or corrupt line ends the replay: everything
// before it is kept (the writer only ever appends, so damage is at the tail).
func readCompanionRingSegment(path string) (companionMessengerHistory, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return companionMessengerHistory{}, false, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64<<10)
	var h companionMessengerHistory
	first := true
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				var rec companionMessengerRecord
				if json.Unmarshal(line, &rec) != nil {
					return h, !first, nil
				}
				if first {
					if rec.Type != companionRingSnapshot {
						return companionMessengerHistory{}, false, nil
					}
					first = false
				}
				h.apply(rec)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return h, !first, nil
			}
			return h, !first, err
		}
	}
}

// startSegment opens segment n, writes the current history as its snapshot
// line, closes the previous segment, and deletes all but the newest
// companionRingKeepSegments segments.
func (r *companionMessengerRing) startSegment(n uint64) error {
	path := filepath.Join(r.dir, companionRingSegmentName(n))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	line, err := json.Marshal(r.history.snapshotRecord())
	if err != nil {
		f.Close()
		return err
	}
	line = append(line, '\n')
	if _, err := f.Write(line); err != nil {
		f.Close()
		return err
	}
	if r.file != nil {
		_ = r.file.Close()
	}
	r.file, r.segment, r.size, r.snapSize = f, n, int64(len(line)), int64(len(line))
	segs, err := companionRingSegments(r.dir)
	if err == nil && len(segs) > companionRingKeepSegments {
		for _, old := range segs[:len(segs)-companionRingKeepSegments] {
			_ = os.Remove(filepath.Join(r.dir, companionRingSegmentName(old)))
		}
	}
	return nil
}

// append queues rec for the writer. It never blocks on I/O: the caller holds
// the CompanionPhoneStore lock.
func (r *companionMessengerRing) append(rec companionMessengerRecord) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.pending = append(r.pending, rec)
	r.mu.Unlock()
	select {
	case r.signal <- struct{}{}:
	default:
	}
}

func (r *companionMessengerRing) run() {
	defer close(r.exited)
	for {
		select {
		case <-r.signal:
			r.flush()
		case <-r.done:
			r.flush()
			if r.file != nil {
				_ = r.file.Close()
				r.file = nil
			}
			return
		}
	}
}

func (r *companionMessengerRing) flush() {
	r.mu.Lock()
	batch := r.pending
	r.pending = nil
	r.mu.Unlock()
	if len(batch) == 0 || r.file == nil {
		return
	}
	var buf bytes.Buffer
	for _, rec := range batch {
		r.history.apply(rec)
		line, err := json.Marshal(rec)
		if err != nil || len(line) > companionRingMaxLine {
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
		// Rotate mid-batch so a burst cannot grow a segment far past the cap.
		// The snapshot already holds every record applied so far, including
		// this one, so the buffered lines are written to the old segment first.
		if r.rotateDue(int64(buf.Len())) {
			if !r.writeBuffered(&buf) {
				return
			}
			if r.rotateDue(0) {
				r.rotate()
			}
		}
	}
	if !r.writeBuffered(&buf) {
		return
	}
	if r.rotateDue(0) {
		r.rotate()
	}
}

// rotateDue reports whether the segment (plus pending bytes) reached the cap.
// The deltas since the segment's snapshot must also reach a quarter of the
// cap, so a snapshot near the cap cannot make every record rotate.
func (r *companionMessengerRing) rotateDue(pending int64) bool {
	size := r.size + pending
	return size >= r.segmentMax && size-r.snapSize >= r.segmentMax/4
}

// writeBuffered appends buf to the current segment and resets it. On a write
// error it logs and rotates: the segment tail may be torn, and the new
// segment's snapshot already covers every applied record. It reports false
// only when no segment is open to keep writing to.
func (r *companionMessengerRing) writeBuffered(buf *bytes.Buffer) bool {
	if r.file == nil {
		buf.Reset()
		return false
	}
	if buf.Len() == 0 {
		return true
	}
	n, err := r.file.Write(buf.Bytes())
	r.size += int64(n)
	buf.Reset()
	if err != nil {
		r.logf("companion messenger history: append: %v", err)
		r.rotate()
	}
	return r.file != nil
}

func (r *companionMessengerRing) rotate() {
	if err := r.startSegment(r.segment + 1); err != nil {
		r.logf("companion messenger history: rotate: %v", err)
	}
}

// close drains queued records to disk and stops the writer.
func (r *companionMessengerRing) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		<-r.exited
		return
	}
	r.closed = true
	r.mu.Unlock()
	close(r.done)
	<-r.exited
}

// hydrateHistory loads recovered history into an empty store at startup.
func (s *CompanionPhoneStore) hydrateHistory(h companionMessengerHistory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if len(h.Messages) == 0 && len(h.Summaries) == 0 && h.MsgSeq == 0 {
		return
	}
	if len(s.messages) == 0 {
		s.messages = append([]CompanionPhoneMessage(nil), h.Messages...)
	}
	if len(s.tier1Summary) == 0 {
		s.tier1Summary = append([]string(nil), h.Summaries...)
	}
	if h.MsgSeq > s.msgSeq {
		s.msgSeq = h.MsgSeq
	}
	if s.turns == 0 {
		s.turns = h.Turns
	}
	s.publishLocked()
}

// setPersist installs (or clears) the history delta sink.
func (s *CompanionPhoneStore) setPersist(fn func(companionMessengerRecord)) {
	s.mu.Lock()
	s.persist = fn
	s.mu.Unlock()
}

// openCompanionMessengerRing opens the history log under the daemon state
// directory and hydrates the phone store from it. A failure leaves the
// Messenger memory-only.
func (d *Daemon) openCompanionMessengerRing() {
	if d.dir == "" {
		return
	}
	logf := func(format string, args ...any) {
		if d.log != nil {
			fmt.Fprintf(d.log, "dashboard: "+format+"\n", args...)
		}
	}
	ring, hist, err := openCompanionMessengerRing(filepath.Join(d.dir, companionRingDirName), logf)
	if err != nil {
		logf("companion messenger history unavailable: %v", err)
		return
	}
	d.companionPhone.hydrateHistory(hist)
	d.messengerRingMu.Lock()
	d.messengerRing = ring
	d.messengerRingMu.Unlock()
	d.companionPhone.setPersist(ring.append)
}

func (d *Daemon) closeCompanionMessengerRing() {
	d.companionPhone.setPersist(nil)
	d.messengerRingMu.Lock()
	ring := d.messengerRing
	d.messengerRing = nil
	d.messengerRingMu.Unlock()
	ring.close()
}

// CompanionMessengerDigest renders the persisted Messenger history (recent
// exchange summaries and the newest messages) as plain text for a
// companion-body handoff checkpoint. At most maxMessages messages are
// included, each cut to a single bounded line.
func (d *Daemon) CompanionMessengerDigest(maxMessages int) string {
	snap := d.companionPhone.Snapshot()
	var b strings.Builder
	if len(snap.Tier1Summary) > 0 {
		b.WriteString("Recent messenger exchanges:\n")
		for _, line := range snap.Tier1Summary {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	msgs := recentPhoneMessages(snap.Messages, maxMessages)
	if len(msgs) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("Latest messenger messages (oldest first):\n")
		for _, m := range msgs {
			who := "user"
			if m.Sender == "companion" {
				who = "you"
			}
			fmt.Fprintf(&b, "- %s: %s\n", who, companionCut(strings.Join(strings.Fields(m.Text), " "), 280))
		}
	}
	return b.String()
}
