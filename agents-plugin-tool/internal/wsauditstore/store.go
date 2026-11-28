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
// segments once, on its first use; the query API's View of an unloaded ring
// reads its segments on the caller's goroutine instead, so a query never
// occupies the writer. Capture's recovery questions
// (wsaudit.SourcedSink Recover and Retained) are answered from a per-ring
// identity ledger under its own lock, never behind a disk write.
package wsauditstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

const (
	// DefaultIdleTTL is how long an unpinned loaded ring stays resident in
	// memory with its newest-segment handle open without activity.
	DefaultIdleTTL = 15 * time.Minute
	// DefaultMaxLoadedRings caps simultaneously loaded rings (pinned +
	// unpinned); above it, least-recently-active unpinned rings are unloaded
	// even within IdleTTL.
	DefaultMaxLoadedRings = 64
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
	// LastSeen reports a session's registry last-seen time. Every sweep and
	// Close ask it for each ring and record a newer answer, so a quiet
	// session that still heartbeats keeps its ring, even after the registry
	// pruned it while the daemon was not watching. It receives the
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
	// IdleTTL is how long an unpinned loaded ring stays resident without
	// activity before its handle and in-memory index are released (default
	// DefaultIdleTTL; negative disables time-based idle unload).
	IdleTTL time.Duration
	// MaxLoadedRings caps total loaded rings (pinned + unpinned) before
	// least-recently-active unpinned rings are unloaded even within IdleTTL
	// (default DefaultMaxLoadedRings; negative disables the LRU ceiling).
	MaxLoadedRings int
	// IdleCheckInterval is how often the writer checks for idle rings to
	// unload (default min(1m, max(IdleTTL/3, 10ms)) when IdleTTL > 0;
	// negative disables the periodic idle ticker).
	IdleCheckInterval time.Duration
}

type jobKind int

const (
	jobAppend jobKind = iota
	jobCheckpoint
	jobFinalize
	jobLoss
	jobTouch
	jobDelete
	jobLoad
	jobSweep
	jobUnloadIdle
	jobBarrier
)

type job struct {
	kind    jobKind
	session string
	ev      wsaudit.Envelope
	id      *wsaudit.SourceID
	// rec, on a sourced append, is the position of the source record that
	// yielded the event; admission judges coverage by it.
	rec    wsaudit.SourceID
	reason string
	source string
	seq    uint64
	at     time.Time
	reply  chan jobResult
	// fn, on a barrier, runs on the writer goroutine (tests reach writer
	// state through it).
	fn func()
}

type jobResult struct {
	err  error
	ring *ring
}

// sessionInfo is one ring directory the store knows. ring is nil until the
// ring is first used; mu guards ring reads against the writer.
type sessionInfo struct {
	dir      string
	session  string
	bytes    int64
	lastSeen time.Time
	recorded time.Time
	// lastActive tracks in-memory ring activity (AppendSourced, Recover,
	// Retained, RecoverCompactionBaseline, pin, unpin) under Store.mu for
	// idle unload, separate from persisted lastSeen (7-day disk retention).
	lastActive time.Time

	mu   sync.Mutex
	ring *ring
	// view is ring for readers, who never take mu (the writer holds it across
	// disk I/O); set and cleared with ring.
	view atomic.Pointer[ring]

	// led is ring's identity ledger once the ring is loaded; loading is set
	// while a load is queued, so readers queue it at most once.
	led     atomic.Pointer[ledger]
	loading atomic.Bool
	// newer is set once the ring is known to hold a newer format; age-out and
	// capacity eviction leave it alone.
	newer bool
	// tombstone is set (under Store.mu) while the ring holds only the
	// recovery state store-wide eviction left of it, and cleared by its next
	// write: makeRoom never evicts it into another tombstone and removes it
	// only as the last resort.
	tombstone bool
	// evictFailed is set (under Store.mu) once store-wide eviction of this
	// ring failed (a segment it could not read or delete): makeRoom passes it
	// over on later writes, which would otherwise retry the same failing
	// reads and deletions on every append, and the next sweep clears it to
	// retry once.
	evictFailed bool

	// gen and busy publish the writer's changes to the ring's files while a
	// cold reader could be reading them — loading, eviction, trimming and
	// removal: busy counts the changes under way, and gen advances as each
	// starts and ends. A cold View keeps what it read only when busy was zero
	// and gen unchanged across the read, so it never returns segments read
	// across a change, and the writer never waits on it.
	gen  atomic.Uint64
	busy atomic.Int32
}

// changing marks a writer change to info's files until the returned func
// runs. Changes may nest.
func (info *sessionInfo) changing() func() {
	info.busy.Add(1)
	info.gen.Add(1)
	return func() {
		info.gen.Add(1)
		info.busy.Add(-1)
	}
}

// Store is the audit segment ring store. It implements wsaudit.SourcedSink.
type Store struct {
	opts   Options
	limits Limits

	// The writer's queue: one FIFO for every job, so a job never overtakes
	// one queued before it. Appends count against QueueDepth and are dropped
	// past it; control jobs (boundaries, losses, barriers) are never dropped.
	// closed is set under qmu, so every job accepted before Close is drained.
	qmu     sync.Mutex
	queue   []job
	appends int
	closed  bool
	wake    chan struct{}
	done    chan struct{}

	mu       sync.Mutex
	sessions map[string]*sessionInfo   // by ring directory name
	resident map[*sessionInfo]struct{} // rings currently resident in memory
	pins     map[string]int            // refcounted active captures, by dir name
	total    int64

	idx        *indexLog                 // writer goroutine only
	dirty      map[*sessionInfo]struct{} // rings with pending control lines; writer only
	idleQueued atomic.Bool               // coalesces async jobUnloadIdle from UnpinCapture

	dropped    atomic.Uint64
	queueDrops atomic.Uint64 // queue-full drops not yet logged

	// cold caches the views View read from unloaded rings, and flights the
	// reads under way, by ring directory name.
	coldMu  sync.Mutex
	cold    map[string]coldView
	flights map[string]*coldFlight
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
	if opts.IdleTTL == 0 {
		opts.IdleTTL = DefaultIdleTTL
	}
	if opts.MaxLoadedRings == 0 {
		opts.MaxLoadedRings = DefaultMaxLoadedRings
	}
	if opts.IdleCheckInterval == 0 && opts.IdleTTL > 0 {
		opts.IdleCheckInterval = min(time.Minute, max(opts.IdleTTL/3, 10*time.Millisecond))
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
		wake: make(chan struct{}, 1), done: make(chan struct{}),
		sessions: map[string]*sessionInfo{}, resident: map[*sessionInfo]struct{}{},
		pins: map[string]int{}, idx: idx, dirty: map[*sessionInfo]struct{}{},
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
	} else {
		s.enforceCaps() // the caps need no registry
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
	return s.enqueue(job{kind: jobAppend, session: ev.Session, ev: ev}, true)
}

// AppendSourced hands ev to the writer under source identity id, like Append.
// rec is the position of the source record that yielded ev (id itself for an
// event keyed by its own record). An identity the store already knows it
// retains returns wsaudit.ErrDuplicateSource; the writer also skips a
// duplicate it only learns of later, and records as loss an identity whose
// rec its source's finalized boundary covers (never appending it late).
func (s *Store) AppendSourced(_ context.Context, id, rec wsaudit.SourceID, ev wsaudit.Envelope) error {
	id = normalizeSourceID(id)
	rec = normalizeSourceID(rec)
	if l := s.ledgerOf(ev.Session); l != nil && l.isRetained(id) {
		return wsaudit.ErrDuplicateSource
	}
	return s.enqueue(job{kind: jobAppend, session: ev.Session, ev: ev, id: &id, rec: rec}, true)
}

// AppendCheckpoint places a completed input replacement behind preceding audit
// events. It is never dropped for queue depth, and waits for the writer.
func (s *Store) AppendCheckpoint(ctx context.Context, id wsaudit.SourceID, ev wsaudit.Envelope) error {
	id = normalizeSourceID(id)
	if ev.Kind != wsaudit.KindCompaction || id.Source == "" || id.Seq == 0 || id.Tee || id.Key != "" {
		return errors.New("invalid audit checkpoint")
	}
	_, err := s.call(ctx, job{kind: jobCheckpoint, session: ev.Session, ev: ev, id: &id, rec: id})
	return err
}

// enqueue appends j to the writer's queue in call order. A droppable job
// (an append) is refused with ErrQueueFull once QueueDepth appends wait.
func (s *Store) enqueue(j job, droppable bool) error {
	s.qmu.Lock()
	if s.closed {
		s.qmu.Unlock()
		return ErrClosed
	}
	if droppable && s.appends >= s.opts.QueueDepth {
		s.qmu.Unlock()
		// No log line here: the log may be a file, and the producer must not
		// wait on it. The writer reports the count once it catches up.
		s.dropped.Add(1)
		s.queueDrops.Add(1)
		return ErrQueueFull
	}
	s.queue = append(s.queue, j)
	if droppable {
		s.appends++
	}
	s.qmu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// take removes every queued job, oldest first.
func (s *Store) take() []job {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	batch := s.queue
	s.queue = nil
	s.appends = 0
	return batch
}

func (s *Store) isClosed() bool {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	return s.closed && len(s.queue) == 0
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
	now := s.opts.Now()
	s.mu.Lock()
	info := s.sessions[dir]
	if info != nil {
		info.lastActive = maxTime(info.lastActive, now)
	}
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

// PinCapture marks session as having an active capture attached, protecting
// its ring from idle TTL and LRU ceiling eviction until a matching
// UnpinCapture runs. Calls are refcounted per session so multiple sources
// capturing into the same session stay protected until the last detaches.
func (s *Store) PinCapture(session string) {
	dir := sessionDirName(wsaudit.NormalizeID(session))
	now := s.opts.Now()
	s.mu.Lock()
	s.pins[dir]++
	if info := s.sessions[dir]; info != nil {
		info.lastActive = maxTime(info.lastActive, now)
	}
	s.mu.Unlock()
}

// UnpinCapture releases one active-capture pin on session. When the store is
// above MaxLoadedRings, it queues an asynchronous idle-eviction pass so
// unpinned rings drain back under the ceiling without waiting for the next
// timer tick or write.
func (s *Store) UnpinCapture(session string) {
	dir := sessionDirName(wsaudit.NormalizeID(session))
	now := s.opts.Now()
	s.mu.Lock()
	if n := s.pins[dir]; n <= 1 {
		delete(s.pins, dir)
	} else {
		s.pins[dir] = n - 1
	}
	if info := s.sessions[dir]; info != nil {
		info.lastActive = maxTime(info.lastActive, now)
	}
	overCap := s.opts.MaxLoadedRings > 0 && len(s.resident) > s.opts.MaxLoadedRings
	s.mu.Unlock()
	if overCap && s.idleQueued.CompareAndSwap(false, true) {
		if err := s.control(job{kind: jobUnloadIdle}); err != nil {
			s.idleQueued.Store(false)
		}
	}
}

// UnloadIdle runs idle TTL and LRU ring eviction on the writer goroutine and
// waits for it to finish.
func (s *Store) UnloadIdle(ctx context.Context) error {
	_, err := s.call(ctx, job{kind: jobUnloadIdle})
	return err
}

// LoadedRings reports how many session rings are currently loaded in memory
// (holding their in-memory index and newest-segment handle).
func (s *Store) LoadedRings() int {
	return s.loadedCount()
}

func (s *Store) loadedCount() int {
	s.mu.Lock()
	n := len(s.resident)
	s.mu.Unlock()
	return n
}

// Finalize advances a source's finalized boundary (wsaudit.Recovery.Boundary).
// It runs in call order with every other job and is never dropped. The
// boundary is persisted ahead of the ring's next record,
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

// Flush waits until every job queued before it has run and any pending
// control lines have been written.
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
	return s.enqueue(j, false)
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
	s.qmu.Lock()
	s.closed = true
	s.qmu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	<-s.done
	return nil
}
