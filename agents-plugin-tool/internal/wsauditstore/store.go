// Package wsauditstore persists audit envelopes in one append-only segment
// ring per session (a Type 1 store, ai-docs/manuals/performance.md): every
// body redacted before it touches disk, each body capped to head and tail,
// metadata bounded, replayed source records deduplicated, and the rings
// bounded by a per-session byte cap, a store-wide least-recently-used byte
// cap, and a retention period after each session was last seen.
//
// One writer goroutine owns every ring and file mutation; producers hand
// envelopes over without waiting on disk, and a full queue or a failed write
// drops the event with a log line rather than stalling capture. Reads are
// served from the in-memory index, which is recovered from a ring's
// segments once, on its first use. Capture's recovery questions
// (wsaudit.SourcedSink Recover and Retained) are answered from a per-ring
// identity ledger under its own lock, never behind a disk write.
package wsauditstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

var (
	// ErrQueueFull drops an envelope the writer has no room for.
	ErrQueueFull = errors.New("wsauditstore: write queue full; audit event dropped")
	// ErrClosed refuses calls after Close.
	ErrClosed = errors.New("wsauditstore: store closed")
)

// Options configures Open.
type Options struct {
	// Root is the audit store directory; each session's ring is a
	// subdirectory keyed by session ID.
	Root string
	// Limits are the byte caps and retention; the zero value uses
	// DefaultLimits. Invalid limits are clamped with a logged warning.
	Limits Limits
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// Log receives one line per dropped event, refused write, and eviction;
	// nil discards.
	Log io.Writer
	// QueueDepth bounds the envelopes waiting for the writer (default 4096).
	QueueDepth int
	// LastSeen reports a session's registry last-seen time. The retention
	// sweep asks it before removing a ring and records a newer answer, so a
	// quiet session that still heartbeats keeps its ring. It receives the
	// normalized session ID, which is the raw ID for every ID NormalizeID
	// keeps verbatim (registry reply IDs are). Nil skips it.
	LastSeen func(session string) (time.Time, bool)
	// SweepInterval is how often retention runs after the first sweep
	// (default one hour; negative disables the periodic sweep).
	SweepInterval time.Duration
	// DeferFirstSweep skips the retention sweep at Open, for an owner whose
	// LastSeen cannot answer yet (a registry not loaded); it calls
	// RequestSweep once it can.
	DeferFirstSweep bool
}

type jobKind int

const (
	jobAppend jobKind = iota
	jobFinalize
	jobLoss
	jobTouch
	jobDelete
	jobLoad
	jobSweep
	jobBarrier
)

type job struct {
	kind    jobKind
	session string
	ev      wsaudit.Envelope
	id      *wsaudit.SourceID
	reason  string
	source  string
	seq     uint64
	at      time.Time
	reply   chan jobResult
}

type jobResult struct {
	err error
}

// sessionInfo is one ring directory the store knows. ring is nil until the
// ring is first used; mu guards ring reads against the writer.
type sessionInfo struct {
	dir      string
	session  string
	bytes    int64
	lastSeen time.Time
	recorded time.Time

	mu   sync.Mutex
	ring *ring

	// led is ring's identity ledger once the ring is loaded; loading is set
	// while a load is queued, so readers queue it at most once.
	led     atomic.Pointer[ledger]
	loading atomic.Bool
}

// Store is the audit segment ring store. It implements wsaudit.SourcedSink.
type Store struct {
	opts   Options
	limits Limits

	jobs chan job
	wake chan struct{}
	stop chan struct{}
	done chan struct{}

	mu       sync.Mutex
	sessions map[string]*sessionInfo // by ring directory name
	total    int64
	deferred []job
	closed   bool

	idx   *indexLog                 // writer goroutine only
	dirty map[*sessionInfo]struct{} // rings with pending control lines; writer only

	dropped atomic.Uint64
}

var _ wsaudit.SourcedSink = (*Store)(nil)

// Open opens the store at opts.Root: one listing of the root and of each ring
// directory for sizes, the newest index file for recorded last-seen times,
// then a retention sweep (unless deferred). No segment is read until its ring
// is used.
func Open(opts Options) (*Store, error) {
	if opts.Root == "" {
		return nil, errors.New("wsauditstore: root is required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.QueueDepth <= 0 {
		opts.QueueDepth = 4096
	}
	if opts.SweepInterval == 0 {
		opts.SweepInterval = time.Hour
	}
	limits := opts.Limits
	if limits == (Limits{}) {
		limits = DefaultLimits()
	}
	limits, warnings := limits.Clamp()
	for _, w := range warnings {
		fmt.Fprintf(opts.Log, "audit: %s\n", w)
	}
	if err := mkdirPrivate(opts.Root); err != nil {
		return nil, err
	}
	idx, recorded, err := openIndex(opts.Root)
	if err != nil {
		return nil, err
	}
	s := &Store{
		opts: opts, limits: limits,
		jobs: make(chan job, opts.QueueDepth), wake: make(chan struct{}, 1),
		stop: make(chan struct{}), done: make(chan struct{}),
		sessions: map[string]*sessionInfo{}, idx: idx, dirty: map[*sessionInfo]struct{}{},
	}
	entries, err := readDir(opts.Root)
	if err != nil {
		idx.close()
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		segs, newest, err := listSegments(filepath.Join(opts.Root, e.Name()))
		if err != nil {
			continue
		}
		info := &sessionInfo{dir: e.Name(), lastSeen: newest}
		if literalDirPattern.MatchString(e.Name()) {
			info.session = e.Name()
		}
		for _, seg := range segs {
			info.bytes += seg.size
		}
		if rec, ok := recorded[e.Name()]; ok {
			if rec.Session != "" {
				info.session = rec.Session
			}
			if t, err := time.Parse(time.RFC3339Nano, rec.Seen); err == nil {
				info.recorded = t
				if t.After(info.lastSeen) {
					info.lastSeen = t
				}
			}
		}
		s.sessions[info.dir] = info
		s.total += info.bytes
	}
	if !opts.DeferFirstSweep {
		s.sweep()
	}
	go s.run()
	return s, nil
}

// Limits returns the effective (clamped) limits.
func (s *Store) Limits() Limits { return s.limits }

// Dropped counts envelopes dropped because the queue was full, a write
// failed, or the envelope was invalid.
func (s *Store) Dropped() uint64 { return s.dropped.Load() }

func (s *Store) logf(format string, args ...any) {
	fmt.Fprintf(s.opts.Log, "audit: "+format+"\n", args...)
}

// Append hands ev to the writer without waiting on disk. It returns
// ErrQueueFull when the writer is behind; the event is then dropped and the
// caller logs and moves on.
func (s *Store) Append(_ context.Context, ev wsaudit.Envelope) error {
	return s.enqueue(job{kind: jobAppend, session: ev.Session, ev: ev})
}

// AppendSourced hands ev to the writer under source identity id, like Append.
// An identity the store already knows it retains returns
// wsaudit.ErrDuplicateSource; the writer also skips a duplicate it only
// learns of later, and records as loss an identity at or below its source's
// finalized boundary (never appending it late).
func (s *Store) AppendSourced(_ context.Context, id wsaudit.SourceID, ev wsaudit.Envelope) error {
	id = normalizeSourceID(id)
	if l := s.ledgerOf(ev.Session); l != nil && l.isRetained(id) {
		return wsaudit.ErrDuplicateSource
	}
	return s.enqueue(job{kind: jobAppend, session: ev.Session, ev: ev, id: &id})
}

func (s *Store) enqueue(j job) error {
	select {
	case <-s.stop:
		return ErrClosed
	default:
	}
	select {
	case s.jobs <- j:
		return nil
	default:
		s.dropped.Add(1)
		s.logf("drop %s event of session %s: write queue full", j.ev.Kind, j.ev.Session)
		return ErrQueueFull
	}
}

// Recover reports what is retained for session and source, from memory. For
// a ring not yet loaded it queues the load and answers zero: the writer
// applies the same retained and boundary rule to every AppendSourced, so an
// early answer costs at most a renumbered seq, never a duplicate or a late
// append.
func (s *Store) Recover(session, source string) wsaudit.Recovery {
	l := s.ledgerOf(session)
	if l == nil {
		return wsaudit.Recovery{}
	}
	return l.recovery(wsaudit.NormalizeID(source))
}

// Retained reports whether the identity id is retained for session, from
// memory (false for a ring not yet loaded, whose load it queues).
func (s *Store) Retained(session string, id wsaudit.SourceID) bool {
	l := s.ledgerOf(session)
	return l != nil && l.isRetained(normalizeSourceID(id))
}

// Preload queues loading a session's ring, when one exists and is not loaded,
// so capture's recovery questions find it in memory. It never blocks.
func (s *Store) Preload(session string) {
	_ = s.ledgerOf(session)
}

// ledgerOf returns a known session's identity ledger, queuing its ring's
// load when it is not in memory yet (nil then, and for an unknown session).
func (s *Store) ledgerOf(session string) *ledger {
	dir := sessionDirName(wsaudit.NormalizeID(session))
	s.mu.Lock()
	info := s.sessions[dir]
	s.mu.Unlock()
	if info == nil {
		return nil
	}
	if l := info.led.Load(); l != nil {
		return l
	}
	if info.loading.CompareAndSwap(false, true) {
		if err := s.control(job{kind: jobLoad, session: session}); err != nil {
			info.loading.Store(false)
		}
	}
	return nil
}

// Finalize advances a source's finalized boundary (wsaudit.Recovery.Boundary).
// It is ordered after every Append made before it and is never dropped: when
// the queue is full it runs once the queue drains, which only ever applies a
// boundary later. The boundary is persisted ahead of the ring's next record,
// or when the writer next idles.
func (s *Store) Finalize(_ context.Context, session, source string, boundary uint64) error {
	return s.control(job{kind: jobFinalize, session: session, source: source, seq: boundary})
}

// RecordLoss records that the source record id was finalized without being
// retained. Like Finalize it is never dropped.
func (s *Store) RecordLoss(_ context.Context, session string, id wsaudit.SourceID, reason string) error {
	return s.control(job{kind: jobLoss, session: session, id: &id, reason: reason})
}

// Touch records a lifecycle last-seen time for a session's ring (session
// registration, end, the registry's last copy before it drops a session, or
// daemon start from surviving registrations). An unchanged or older time,
// or a session without a ring, writes nothing.
func (s *Store) Touch(session string, at time.Time) {
	if at.IsZero() {
		return
	}
	_ = s.control(job{kind: jobTouch, session: session, at: at})
}

// DeleteSession removes a session's whole audit ring, for an explicit session
// deletion (Decision 8). It returns once the ring is gone.
func (s *Store) DeleteSession(ctx context.Context, session string) error {
	_, err := s.call(ctx, job{kind: jobDelete, session: session})
	return err
}

// Flush waits until every job queued before it has run.
func (s *Store) Flush(ctx context.Context) error {
	_, err := s.call(ctx, job{kind: jobBarrier})
	return err
}

// RequestSweep queues a retention sweep without waiting for it.
func (s *Store) RequestSweep() {
	_ = s.control(job{kind: jobSweep})
}

// Sweep runs retention now and waits for it.
func (s *Store) Sweep(ctx context.Context) error {
	_, err := s.call(ctx, job{kind: jobSweep})
	return err
}

// control queues a job that must not be dropped.
func (s *Store) control(j job) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.mu.Unlock()
	select {
	case s.jobs <- j:
		return nil
	default:
	}
	s.mu.Lock()
	s.deferred = append(s.deferred, j)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

func (s *Store) call(ctx context.Context, j job) (jobResult, error) {
	j.reply = make(chan jobResult, 1)
	if err := s.control(j); err != nil {
		return jobResult{}, err
	}
	select {
	case res := <-j.reply:
		return res, res.err
	case <-ctx.Done():
		return jobResult{}, ctx.Err()
	case <-s.done:
		return jobResult{}, ErrClosed
	}
}

// Close drains the queue, writes any pending control lines, and closes every
// held handle.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.done
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	close(s.stop)
	<-s.done
	return nil
}

func (s *Store) run() {
	defer close(s.done)
	var tick <-chan time.Time
	if s.opts.SweepInterval > 0 {
		t := time.NewTicker(s.opts.SweepInterval)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case j := <-s.jobs:
			s.handle(j)
		case <-s.wake:
		case <-tick:
			s.sweep()
		case <-s.stop:
			s.drain()
			s.shutdown()
			return
		}
		if len(s.jobs) == 0 {
			s.runDeferred()
			s.flushPending()
		}
	}
}

// flushPending writes the queued control lines (losses, boundaries) of every
// ring that has them, once the writer idles, so a boundary or loss does not
// wait for a record that may never come.
func (s *Store) flushPending() {
	for info := range s.dirty {
		delete(s.dirty, info)
		info.mu.Lock()
		if r := info.ring; r != nil && r.readOnly == nil {
			if payload := r.pendingLines(); len(payload) > 0 {
				if s.writeLocked(info, payload) == nil {
					r.clearPending()
				}
			}
		}
		info.mu.Unlock()
	}
}

func (s *Store) runDeferred() {
	for {
		s.mu.Lock()
		list := s.deferred
		s.deferred = nil
		s.mu.Unlock()
		if len(list) == 0 {
			return
		}
		for _, j := range list {
			s.handle(j)
		}
	}
}

func (s *Store) drain() {
	for {
		select {
		case j := <-s.jobs:
			s.handle(j)
		default:
			s.runDeferred()
			if len(s.jobs) == 0 {
				return
			}
		}
	}
}

func (s *Store) shutdown() {
	s.mu.Lock()
	infos := make([]*sessionInfo, 0, len(s.sessions))
	for _, info := range s.sessions {
		infos = append(infos, info)
	}
	s.mu.Unlock()
	for _, info := range infos {
		info.mu.Lock()
		if r := info.ring; r != nil {
			if payload := r.pendingLines(); len(payload) > 0 && r.readOnly == nil {
				if _, err := r.write(payload, s.limits.SegmentBytes(), s.limits.EffectiveSessionBytes()); err == nil {
					r.clearPending()
				}
				s.account(info)
			}
			r.close()
		}
		info.mu.Unlock()
	}
	s.idx.close()
}

func (s *Store) handle(j job) {
	var res jobResult
	switch j.kind {
	case jobAppend:
		s.appendEvent(j.ev, j.id)
	case jobFinalize:
		s.finalize(j.session, j.source, j.seq)
	case jobLoss:
		s.recordLoss(j.session, *j.id, j.reason)
	case jobTouch:
		s.touch(j.session, j.at)
	case jobDelete:
		res.err = s.deleteSession(j.session)
	case jobLoad:
		var info *sessionInfo
		info, res.err = s.info(wsaudit.NormalizeID(j.session), false)
		if info != nil {
			info.loading.Store(false)
		}
	case jobSweep:
		s.sweep()
	case jobBarrier:
	}
	if j.reply != nil {
		j.reply <- res
	}
}

// info returns the session's entry with its ring recovered, creating the
// entry when create is set. It returns nil without error for an unknown
// session when create is unset.
func (s *Store) info(session string, create bool) (*sessionInfo, error) {
	dir := sessionDirName(session)
	s.mu.Lock()
	info := s.sessions[dir]
	if info == nil {
		if !create {
			s.mu.Unlock()
			return nil, nil
		}
		info = &sessionInfo{dir: dir, session: session}
		s.sessions[dir] = info
	}
	s.mu.Unlock()
	if info.ring != nil {
		return info, nil
	}
	r := newRing(session, filepath.Join(s.opts.Root, dir))
	if err := r.recover(); err != nil {
		return nil, err
	}
	if r.readOnly != nil {
		s.logf("session %s: %v; writes refused", session, r.readOnly)
	}
	info.mu.Lock()
	info.ring = r
	info.session = session
	info.mu.Unlock()
	info.led.Store(r.led)
	s.account(info)
	return info, nil
}

// account syncs the store total with the ring's byte count.
func (s *Store) account(info *sessionInfo) {
	if info.ring == nil {
		return
	}
	s.mu.Lock()
	s.total += info.ring.bytes - info.bytes
	info.bytes = info.ring.bytes
	s.mu.Unlock()
}

func (s *Store) appendEvent(raw wsaudit.Envelope, id *wsaudit.SourceID) {
	ev, err := prepare(raw, s.limits)
	if err != nil {
		s.dropped.Add(1)
		s.logf("drop %s event of session %s: %v", raw.Kind, raw.Session, err)
		if raw.Session == "" || id == nil {
			return
		}
		// A dropped record with an identity is finalized as lost.
		if info, err := s.info(wsaudit.NormalizeID(raw.Session), true); err == nil {
			info.mu.Lock()
			s.loseLocked(info, *id, wsaudit.LossAppendFailed)
			info.mu.Unlock()
		}
		return
	}
	info, err := s.info(ev.Session, true)
	if err != nil {
		s.dropped.Add(1)
		s.logf("drop %s event of session %s: recover ring: %v", ev.Kind, ev.Session, err)
		return
	}
	info.mu.Lock()
	defer info.mu.Unlock()
	r := info.ring
	if r.readOnly != nil {
		s.dropped.Add(1)
		return
	}
	switch r.admit(id) {
	case admitDuplicate:
		return
	case admitLoss:
		s.loseLocked(info, *id, wsaudit.LossAppendFailed)
		return
	}
	if ev.Seq <= r.led.maxSeq {
		ev.Seq = r.led.maxSeq + 1
	}
	evLine, err := encodeLine(line{T: lineEvent, Event: &ev, ID: id})
	if err == nil && int64(len(evLine)) > s.limits.MaxRecordBytes() {
		err = fmt.Errorf("record of %d bytes exceeds the %d-byte record bound", len(evLine), s.limits.MaxRecordBytes())
	}
	if err != nil {
		s.dropped.Add(1)
		s.logf("drop %s event of session %s: %v", ev.Kind, ev.Session, err)
		if id != nil {
			s.loseLocked(info, *id, wsaudit.LossAppendFailed)
		}
		return
	}
	payload := append(r.pendingLines(), evLine...)
	if werr := s.writeLocked(info, payload); werr != nil {
		s.dropped.Add(1)
		s.logf("drop %s event of session %s: write: %v", ev.Kind, ev.Session, werr)
		if id != nil {
			s.loseLocked(info, *id, wsaudit.LossAppendFailed)
		}
		return
	}
	r.clearPending()
	delete(s.dirty, info)
	r.segs[len(r.segs)-1].events++
	r.index(ev, id)
	info.lastSeen = maxTime(info.lastSeen, s.opts.Now())
}

// writeLocked writes payload to info's ring, first making room under the
// store total, and syncs the byte accounting. info.mu is held.
func (s *Store) writeLocked(info *sessionInfo, payload []byte) error {
	r := info.ring
	s.makeRoom(info, int64(len(payload))+s.headerAllowance(r, int64(len(payload))))
	_, err := r.write(payload, s.limits.SegmentBytes(), s.limits.EffectiveSessionBytes())
	s.mu.Lock()
	s.total += r.bytes - info.bytes
	info.bytes = r.bytes
	s.mu.Unlock()
	return err
}

// headerAllowance is the header line a write of n bytes would add by
// rotating, for the store-total check.
func (s *Store) headerAllowance(r *ring, n int64) int64 {
	cur := len(r.segs) - 1
	if r.file != nil && !r.forceRotate && cur >= 0 && r.segs[cur].size+n <= s.limits.SegmentBytes() {
		return 0
	}
	h, err := r.headerLine()
	if err != nil {
		return 0
	}
	return int64(len(h))
}

// loseLocked records a source record finalized without being retained; the
// loss line is written ahead of the ring's next record, or when the writer
// idles. info.mu is held.
func (s *Store) loseLocked(info *sessionInfo, id wsaudit.SourceID, reason string) {
	r := info.ring
	if !r.markLost(id) {
		return
	}
	if ln, err := encodeLine(line{T: lineLoss, ID: &id, Reason: reason}); err == nil {
		r.pendingLoss = append(r.pendingLoss, ln)
		s.dirty[info] = struct{}{}
	}
}

func (s *Store) recordLoss(session string, id wsaudit.SourceID, reason string) {
	session = wsaudit.NormalizeID(session)
	info, err := s.info(session, true)
	if err != nil {
		s.logf("record loss in session %s: %v", session, err)
		return
	}
	info.mu.Lock()
	defer info.mu.Unlock()
	if info.ring.readOnly == nil {
		s.loseLocked(info, normalizeSourceID(id), reason)
		info.lastSeen = maxTime(info.lastSeen, s.opts.Now())
	}
}

func (s *Store) finalize(session, source string, boundary uint64) {
	session = wsaudit.NormalizeID(session)
	source = wsaudit.NormalizeID(source)
	info, err := s.info(session, true)
	if err != nil {
		s.logf("finalize session %s: %v", session, err)
		return
	}
	info.mu.Lock()
	defer info.mu.Unlock()
	r := info.ring
	if r.readOnly != nil || !r.finalize(source, boundary) {
		return
	}
	r.pendingFin[source] = boundary
	s.dirty[info] = struct{}{}
	// A ring holding only control lines is still in use.
	info.lastSeen = maxTime(info.lastSeen, s.opts.Now())
}

// makeRoom evicts whole least-recently-used rings other than the active one
// until need more bytes fit the store total. The active ring keeps itself
// within the effective per-session cap, which never exceeds the total, so
// it alone always fits.
func (s *Store) makeRoom(active *sessionInfo, need int64) {
	for {
		s.mu.Lock()
		if s.total+need <= s.limits.TotalBytes {
			s.mu.Unlock()
			return
		}
		var victim *sessionInfo
		for _, info := range s.sessions {
			if info == active || info.bytes == 0 {
				continue
			}
			if victim == nil || info.lastSeen.Before(victim.lastSeen) ||
				(info.lastSeen.Equal(victim.lastSeen) && info.dir < victim.dir) {
				victim = info
			}
		}
		s.mu.Unlock()
		if victim == nil {
			if r := active.ring; r != nil {
				s.mu.Lock()
				room := s.limits.TotalBytes - (s.total - active.bytes) - need
				s.mu.Unlock()
				r.trimTo(max(room, 0))
				s.mu.Lock()
				s.total += r.bytes - active.bytes
				active.bytes = r.bytes
				s.mu.Unlock()
			}
			return
		}
		s.logf("store total cap: evict audit ring of session %s (%d bytes, last seen %s)", victim.dir, victim.bytes, formatTime(victim.lastSeen))
		s.removeRing(victim, false)
	}
}

// removeRing deletes a ring's directory. A loaded ring that lost only its
// data to store-wide eviction keeps its recovery state, so a later record
// cannot replay what was already finalized; forget drops the entry whole.
func (s *Store) removeRing(info *sessionInfo, forget bool) {
	info.mu.Lock()
	r := info.ring
	if r != nil {
		r.close()
	}
	_ = removeAll(filepath.Join(s.opts.Root, info.dir))
	keep := r != nil && !forget
	if keep {
		r.segs, r.events, r.ids, r.bytes = nil, nil, nil, 0
		r.led.mu.Lock()
		clear(r.led.retained)
		r.led.mu.Unlock()
		r.forceRotate = false
	} else {
		info.ring = nil
		info.led.Store(nil)
		delete(s.dirty, info)
	}
	info.mu.Unlock()
	s.mu.Lock()
	s.total -= info.bytes
	info.bytes = 0
	if !keep && s.sessions[info.dir] == info {
		delete(s.sessions, info.dir)
	}
	s.mu.Unlock()
}

func (s *Store) deleteSession(session string) error {
	dir := sessionDirName(wsaudit.NormalizeID(session))
	s.mu.Lock()
	info := s.sessions[dir]
	s.mu.Unlock()
	if info == nil {
		return removeAll(filepath.Join(s.opts.Root, dir))
	}
	s.removeRing(info, true)
	return nil
}

func (s *Store) touch(session string, at time.Time) {
	s.touchNormalized(wsaudit.NormalizeID(session), at)
}

// touchNormalized is touch for an already normalized session ID.
func (s *Store) touchNormalized(session string, at time.Time) {
	dir := sessionDirName(session)
	s.mu.Lock()
	info := s.sessions[dir]
	s.mu.Unlock()
	if info == nil {
		return
	}
	info.lastSeen = maxTime(info.lastSeen, at)
	if !at.After(info.recorded) {
		return
	}
	info.recorded = at
	info.session = session
	if err := s.idx.record(indexEntry{Dir: dir, Session: session, Seen: formatTime(at)}, s.snapshot); err != nil {
		s.logf("record last seen of session %s: %v", session, err)
	}
}

func (s *Store) snapshot() []indexEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]indexEntry, 0, len(s.sessions))
	for _, info := range s.sessions {
		if !info.recorded.IsZero() {
			out = append(out, indexEntry{Dir: info.dir, Session: info.session, Seen: formatTime(info.recorded)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out
}

// sweep removes every ring last seen longer than the retention period ago,
// first asking the registry for a newer last-seen time.
func (s *Store) sweep() {
	now := s.opts.Now()
	s.mu.Lock()
	var expired []*sessionInfo
	for _, info := range s.sessions {
		if now.Sub(info.lastSeen) > s.limits.Retention {
			expired = append(expired, info)
		}
	}
	s.mu.Unlock()
	sort.Slice(expired, func(i, j int) bool { return expired[i].dir < expired[j].dir })
	for _, info := range expired {
		if s.opts.LastSeen != nil && info.session != "" {
			if t, ok := s.opts.LastSeen(info.session); ok && t.After(info.lastSeen) {
				s.touchNormalized(info.session, t)
				if now.Sub(info.lastSeen) <= s.limits.Retention {
					continue
				}
			}
		}
		s.logf("retention: remove audit ring of session %s (last seen %s)", info.dir, formatTime(info.lastSeen))
		s.removeRing(info, true)
	}
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// Events returns the session's retained events, oldest first, from memory.
func (s *Store) Events(ctx context.Context, session string) ([]wsaudit.Envelope, error) {
	info, err := s.loaded(ctx, session)
	if err != nil || info == nil {
		return nil, err
	}
	info.mu.Lock()
	defer info.mu.Unlock()
	if info.ring == nil {
		return nil, nil
	}
	return append([]wsaudit.Envelope(nil), info.ring.events...), nil
}

// Event returns the retained event with the normalized ID id.
func (s *Store) Event(ctx context.Context, session, id string) (wsaudit.Envelope, bool, error) {
	evs, err := s.Events(ctx, session)
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].ID == id {
			return evs[i], true, err
		}
	}
	return wsaudit.Envelope{}, false, err
}

// EventsByCallID returns the retained events carrying the normalized call
// key callID (as a history row carries it), oldest first. The key is never
// re-normalized.
func (s *Store) EventsByCallID(ctx context.Context, session, callID string) ([]wsaudit.Envelope, error) {
	evs, err := s.Events(ctx, session)
	var out []wsaudit.Envelope
	for _, ev := range evs {
		if ev.CallID == callID {
			out = append(out, ev)
		}
	}
	return out, err
}

// loaded returns the session's entry with its ring in memory, recovering
// it through the writer on first use.
func (s *Store) loaded(ctx context.Context, session string) (*sessionInfo, error) {
	dir := sessionDirName(wsaudit.NormalizeID(session))
	s.mu.Lock()
	info := s.sessions[dir]
	s.mu.Unlock()
	if info == nil {
		return nil, nil
	}
	info.mu.Lock()
	ready := info.ring != nil
	info.mu.Unlock()
	if !ready {
		if _, err := s.call(ctx, job{kind: jobLoad, session: session}); err != nil {
			return nil, err
		}
		s.mu.Lock()
		info = s.sessions[dir]
		s.mu.Unlock()
	}
	return info, nil
}
