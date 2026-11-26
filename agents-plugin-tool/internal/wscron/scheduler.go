package wscron

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsinbox"
)

// Limits on one schedule and one session (Decisions 12, 20).
const (
	MaxPerSession = 32
	MaxPromptSize = 16 << 10
	MaxNameRunes  = 64
)

// Refusals Add and Delete return. Callers match them with errors.Is.
var (
	ErrNotLead         = errors.New("scheduled prompts are available to a hosted lead session only")
	ErrUnknownSchedule = errors.New("unknown schedule")
	ErrDuplicateName   = errors.New("a schedule with this name already exists in this session; delete it first to change it")
	ErrTooMany         = fmt.Errorf("a session holds at most %d schedules", MaxPerSession)
	ErrClosed          = errors.New("the scheduler is closed")
	ErrAtNotFuture     = errors.New("at must be in the future")
	ErrAmbiguousName   = errors.New("more than one schedule in this session has this name; delete it by id")
)

// One-shot delivery retry: a Deliver error re-arms the schedule
// onceRetryDelay later, for at most onceAttempts deliveries in total; then
// the schedule is deleted.
const (
	onceAttempts   = 3
	onceRetryDelay = time.Minute
)

// Delivery is the outcome of handing one fire to a session.
type Delivery int

const (
	// Delivered means the prompt is in the target's inbox.
	Delivered Delivery = iota
	// Suppressed means the target is closed, handed off or not known.
	Suppressed
)

// Sessions is the agent runner as the scheduler's target side.
type Sessions interface {
	// IsLead reports whether id is a conversation the daemon hosts and can
	// deliver to, and a lead; an error means it is not one (closed,
	// handed off or unknown).
	IsLead(id string) (bool, error)
	// Exists reports whether the runner still knows id, open or in its
	// closed set.
	Exists(id string) bool
	// Deliver writes one scheduled prompt to id's inbox as a non-steered
	// entry carrying mark.
	Deliver(id string, mark wsinbox.Cron, prompt string) (Delivery, error)
	// HeldSchedules lists the schedule ids whose cron entries are still
	// held in id's inbox, undrained.
	HeldSchedules(id string) []string
}

// Timer is a stoppable pending call.
type Timer interface{ Stop() bool }

// Clock is the scheduler's time source; tests inject a fake.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

type realClock struct{}

func (realClock) Now() time.Time                            { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// Options configures a Scheduler.
type Options struct {
	Store    *Store
	Sessions Sessions
	Clock    Clock     // nil: the wall clock
	Log      io.Writer // nil: discarded
}

// Info is one schedule as List reports it. A one-shot schedule has an empty
// Expr and its fire time in At (RFC 3339 in its offset); a recurring one has
// an empty At.
type Info struct {
	ID, Name, Expr, TZ, At string
	// NextAt, LastAt are RFC 3339 in the schedule's offset; LastAt is empty
	// before the first fire.
	NextAt, LastAt string
	Pending        bool
	Dropped        int
}

type schedule struct {
	id, owner, name string
	expr            *Expr // nil for a one-shot
	once            bool
	runAt           time.Time // a one-shot's fire time
	attempts        int       // a one-shot's failed deliveries
	tz              string
	loc             *time.Location
	prompt          string
	created         time.Time
	next            time.Time // the next matching minute; zero when none
	last            time.Time // the last delivered minute
	pending         bool
	dropped         int
	closedLogged    bool
	inflight        int // deliveries of this schedule admitted and not yet recorded
}

// Scheduler holds the daemon's schedules and one timer armed for the
// earliest next fire. Live state is in memory; the store is touched by Add,
// Delete, a coalesced last-fire write and Start's single read.
type Scheduler struct {
	store    *Store
	sessions Sessions
	clock    Clock
	log      io.Writer

	// opMu serializes Add, Delete and ConversationGone, which write the
	// store; the fire path never takes it, so it waits on no file.
	opMu sync.Mutex

	mu      sync.Mutex // guards everything below
	byID    map[string]*schedule
	byOwner map[string]map[string]*schedule
	timer   Timer
	gen     uint64
	closed  bool
	dirty   map[string]int64 // last-fire deltas waiting for the writer
	// deletes holds the ids of finished one-shots whose rows the writer
	// deletes, so the fire path never touches the store.
	deletes map[string]bool
	stopObs func()
	// inflight counts deliveries admitted (past the recheck, handed to the
	// runner, outcome not yet recorded); delivered signals when one ends.
	// Delete waits for its schedule's, Close for all of them.
	inflight  int
	delivered *sync.Cond
	// loadRemoved holds the cron entries seen leaving an inbox while New
	// loads; nil afterwards.
	loadRemoved map[string]bool

	writeKick chan struct{}
	writeDone chan struct{}
	writeQuit chan struct{}

	// Diagnostics go through logCh to the logger goroutine, so no timer
	// callback or locked path ever waits on the log file; a full channel
	// drops the message and counts it.
	logCh      chan string
	logQuit    chan struct{}
	logDone    chan struct{}
	logDropped atomic.Int64

	// closeDone ends the one Close that ran; every other caller waits on it.
	closeDone chan struct{}
	closeErr  error
}

// logQueue bounds the diagnostics waiting for the logger.
const logQueue = 64

// logCloseWait bounds how long Close waits for a log write that does not end.
const logCloseWait = 2 * time.Second

// New builds a scheduler over opts and starts it: it loads every stored
// schedule once, deletes those whose conversation the runner no longer
// knows, rebuilds pending from the cron entries still held in inboxes, and
// arms the timer. Missed recurring fires are not caught up; a one-shot whose
// time passed while the daemon was down fires once now, and one whose entry
// is still held in its inbox was delivered already and is deleted.
func New(opts Options) (*Scheduler, error) {
	if opts.Store == nil || opts.Sessions == nil {
		return nil, errors.New("wscron: a store and sessions are required")
	}
	s := &Scheduler{
		store: opts.Store, sessions: opts.Sessions, clock: opts.Clock, log: opts.Log,
		byID: map[string]*schedule{}, byOwner: map[string]map[string]*schedule{}, dirty: map[string]int64{}, deletes: map[string]bool{},
		writeKick: make(chan struct{}, 1), writeDone: make(chan struct{}), writeQuit: make(chan struct{}),
		logCh: make(chan string, logQueue), logQuit: make(chan struct{}), logDone: make(chan struct{}), closeDone: make(chan struct{}),
	}
	s.delivered = sync.NewCond(&s.mu)
	if s.clock == nil {
		s.clock = realClock{}
	}
	if s.log == nil {
		s.log = io.Discard
	}
	rows, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	go s.logger()
	now := s.clock.Now()
	var gone []string
	held := map[string]map[string]bool{}
	// The removal observer is registered before the held entries are read, so
	// an entry that leaves its inbox while the schedules load is never counted
	// as still held: removals of ids not inserted yet are remembered.
	s.loadRemoved = map[string]bool{}
	s.stopObs = wsinbox.ObserveCronRemoval(s.entryLeft)
	for _, r := range rows {
		if !s.sessions.Exists(r.Owner) {
			gone = append(gone, r.ID)
			continue
		}
		sch, err := scheduleFromRow(r, now)
		if err != nil {
			s.logf("cron: skip stored schedule %s: %v\n", r.ID, err)
			continue
		}
		if held[r.Owner] == nil {
			held[r.Owner] = map[string]bool{}
			for _, id := range s.sessions.HeldSchedules(r.Owner) {
				held[r.Owner][id] = true
			}
		}
		s.mu.Lock()
		if sch.once && (held[r.Owner][sch.id] || s.loadRemoved[sch.id]) {
			// Delivered before the restart; its row's delete did not land.
			s.mu.Unlock()
			gone = append(gone, r.ID)
			continue
		}
		sch.pending = held[r.Owner][sch.id] && !s.loadRemoved[sch.id]
		dup := false
		for _, other := range s.byOwner[r.Owner] {
			if other.name == sch.name {
				dup = true
				break
			}
		}
		s.insertLocked(sch)
		s.mu.Unlock()
		if dup {
			// A binary without one-shot support skips one-shot rows, so a name
			// can be reused while it runs; both schedules are kept, and a delete
			// by that name asks for the id.
			s.logf("cron: session %s holds more than one schedule named %q; delete by id\n", r.Owner, sch.name)
		}
	}
	if len(gone) > 0 {
		if err := s.store.Delete(gone...); err != nil {
			s.logf("cron: reap schedules of ended sessions: %v\n", err)
		}
	}
	s.mu.Lock()
	s.loadRemoved = nil
	s.armLocked()
	s.mu.Unlock()
	go s.writer()
	return s, nil
}

func scheduleFromRow(r Row, now time.Time) (*schedule, error) {
	if r.RunAt > 0 {
		tz, loc, err := ParseTZ(r.TZ)
		if err != nil {
			return nil, err
		}
		sch := &schedule{id: r.ID, owner: r.Owner, name: r.Name, once: true, runAt: time.Unix(r.RunAt, 0), tz: tz, loc: loc, prompt: r.Prompt, created: time.Unix(r.CreatedAt, 0)}
		// A one-shot whose time passed while the daemon was down fires once
		// now: the recurring stale-fire skip never applies to it.
		sch.next = sch.runAt
		if sch.next.Before(now) {
			sch.next = now
		}
		return sch, nil
	}
	expr, err := Parse(r.Expr)
	if err != nil {
		return nil, err
	}
	tz, loc, err := ParseTZ(r.TZ)
	if err != nil {
		return nil, err
	}
	sch := &schedule{id: r.ID, owner: r.Owner, name: r.Name, expr: expr, tz: tz, loc: loc, prompt: r.Prompt, created: time.Unix(r.CreatedAt, 0)}
	if r.LastFire != 0 {
		sch.last = time.Unix(r.LastFire, 0)
	}
	sch.next, _ = expr.Next(now, loc)
	return sch, nil
}

func (s *Scheduler) insertLocked(sch *schedule) {
	s.byID[sch.id] = sch
	if s.byOwner[sch.owner] == nil {
		s.byOwner[sch.owner] = map[string]*schedule{}
	}
	s.byOwner[sch.owner][sch.id] = sch
}

func (s *Scheduler) removeLocked(sch *schedule) {
	delete(s.byID, sch.id)
	if m := s.byOwner[sch.owner]; m != nil {
		delete(m, sch.id)
		if len(m) == 0 {
			delete(s.byOwner, sch.owner)
		}
	}
}

// entryLeft is the inbox removal observer: a held cron entry left its inbox,
// so its schedule is no longer pending.
func (s *Scheduler) entryLeft(c wsinbox.Cron) {
	s.mu.Lock()
	if sch := s.byID[c.ID]; sch != nil {
		sch.pending = false
	}
	if s.loadRemoved != nil {
		s.loadRemoved[c.ID] = true
	}
	s.mu.Unlock()
}

// armLocked arms the one timer for the earliest next fire, replacing any
// armed one. s.mu is held.
func (s *Scheduler) armLocked() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.gen++
	if s.closed {
		return
	}
	var earliest time.Time
	for _, sch := range s.byID {
		if sch.next.IsZero() {
			continue
		}
		if earliest.IsZero() || sch.next.Before(earliest) {
			earliest = sch.next
		}
	}
	if earliest.IsZero() {
		return
	}
	gen := s.gen
	d := earliest.Sub(s.clock.Now())
	if d < 0 {
		d = 0
	}
	s.timer = s.clock.AfterFunc(d, func() { s.fire(gen) })
}

type fireJob struct {
	sch    *schedule
	id     string
	owner  string
	mark   wsinbox.Cron
	prompt string
	at     time.Time
}

// fire runs when the timer elapses: every schedule whose minute is due
// delivers once, unless the timer is stale (a stalled daemon or a slept
// machine: Decision 18) or the previous fire is still pending (Decision 8).
func (s *Scheduler) fire(gen uint64) {
	var jobs []fireJob
	s.mu.Lock()
	if s.closed || gen != s.gen {
		s.mu.Unlock()
		return
	}
	now := s.clock.Now()
	for _, sch := range s.byID {
		if sch.next.IsZero() || sch.next.After(now) {
			continue
		}
		if sch.once {
			// Cleared before the lock is released, so a concurrent tick never
			// fires it again; only a failed delivery re-arms it.
			sch.next = time.Time{}
			sch.pending = true
			jobs = append(jobs, fireJob{
				sch: sch, id: sch.id, owner: sch.owner, prompt: sch.prompt, at: sch.runAt,
				mark: wsinbox.Cron{ID: sch.id, Name: sch.name, At: sch.runAt.In(sch.loc).Format(time.RFC3339)},
			})
			continue
		}
		matched := sch.next
		// The next fire is always computed from now (Decision 9).
		sch.next, _ = sch.expr.Next(now, sch.loc)
		if now.Sub(matched) > time.Minute {
			continue
		}
		if sch.pending {
			sch.dropped++
			continue
		}
		// Marked before the hand-off, under the lock the removal observer
		// takes: an idle lead may drain the entry before Deliver returns.
		sch.pending = true
		jobs = append(jobs, fireJob{
			sch: sch, id: sch.id, owner: sch.owner, prompt: sch.prompt, at: matched,
			mark: wsinbox.Cron{ID: sch.id, Name: sch.name, At: matched.In(sch.loc).Format(time.RFC3339)},
		})
	}
	s.armLocked()
	s.mu.Unlock()

	sort.Slice(jobs, func(i, j int) bool { return jobs[i].id < jobs[j].id })
	for _, job := range jobs {
		// Admission: a schedule deleted, or a scheduler closed, since the jobs
		// were built does not deliver; one admitted here is waited for by a
		// Delete of its schedule and by Close, so neither returns with a
		// prompt still on its way to the inbox.
		s.mu.Lock()
		if s.closed || s.byID[job.id] != job.sch {
			s.mu.Unlock()
			continue
		}
		job.sch.inflight++
		s.inflight++
		s.mu.Unlock()

		out, err := s.sessions.Deliver(job.owner, job.mark, job.prompt)

		var note string
		s.mu.Lock()
		switch {
		case job.sch.once:
			note = s.onceSettledLocked(job, out, err)
		case err != nil || out != Delivered:
			job.sch.pending = false
			job.sch.dropped++
			if !job.sch.closedLogged {
				job.sch.closedLogged = true
				if err != nil {
					note = fmt.Sprintf("cron: schedule %s (%s) not delivered: %v\n", job.id, job.sch.name, err)
				} else {
					note = fmt.Sprintf("cron: schedule %s (%s) not delivered: its session is closed or handed off\n", job.id, job.sch.name)
				}
			}
		default:
			job.sch.closedLogged = false
			// Overlapping deliveries can finish in either order: the last
			// fire only moves forward, in memory and in the queued delta.
			if job.sch.last.IsZero() || job.at.After(job.sch.last) {
				job.sch.last = job.at
				s.dirty[job.id] = job.at.Unix()
			}
			select {
			case s.writeKick <- struct{}{}:
			default:
			}
		}
		job.sch.inflight--
		s.inflight--
		s.delivered.Broadcast()
		s.mu.Unlock()
		if note != "" {
			// Queued for the logger after the lock: the timer callback never
			// writes the file, and the note is made once per run of refusals
			// (closedLogged).
			s.logf("%s", note)
		}
	}
}

// onceSettledLocked records a one-shot delivery's outcome and returns the
// note to log, if any. A delivery, or a target that is closed or handed off,
// removes the schedule and queues its row's delete for the writer; a Deliver
// error re-arms it until onceAttempts deliveries failed. A schedule a Delete
// or ConversationGone already removed is left to them. s.mu is held.
func (s *Scheduler) onceSettledLocked(job fireJob, out Delivery, err error) string {
	sch := job.sch
	live := s.byID[job.id] == sch
	switch {
	case err == nil && out == Delivered:
		if live {
			s.dropOnceLocked(sch)
		}
		return ""
	case err == nil:
		if !live {
			return ""
		}
		s.dropOnceLocked(sch)
		return fmt.Sprintf("cron: one-shot schedule %s (%s) not delivered: its session is closed or handed off; deleted\n", job.id, sch.name)
	}
	sch.pending = false
	sch.attempts++
	if !live {
		return ""
	}
	if sch.attempts >= onceAttempts {
		s.dropOnceLocked(sch)
		return fmt.Sprintf("cron: one-shot schedule %s (%s) not delivered after %d attempts: %v; deleted\n", job.id, sch.name, sch.attempts, err)
	}
	sch.next = s.clock.Now().Add(onceRetryDelay)
	s.armLocked()
	return ""
}

// dropOnceLocked removes a finished one-shot from memory and queues its
// row's delete for the writer. s.mu is held.
func (s *Scheduler) dropOnceLocked(sch *schedule) {
	s.removeLocked(sch)
	delete(s.dirty, sch.id)
	s.deletes[sch.id] = true
	select {
	case s.writeKick <- struct{}{}:
	default:
	}
}

// writer persists last-fire deltas and finished one-shots' deletes off the
// delivery path, coalescing what arrived while a write ran into one
// transaction each.
func (s *Scheduler) writer() {
	defer close(s.writeDone)
	for {
		select {
		case <-s.writeKick:
		case <-s.writeQuit:
			s.flush()
			return
		}
		s.flush()
	}
}

func (s *Scheduler) flush() {
	s.mu.Lock()
	batch, deletes := s.dirty, s.deletes
	s.dirty, s.deletes = map[string]int64{}, map[string]bool{}
	s.mu.Unlock()
	if len(batch) > 0 {
		if err := s.store.SetLastFire(batch); err != nil {
			s.logf("cron: persist last fires: %v\n", err)
		}
	}
	if len(deletes) > 0 {
		ids := make([]string, 0, len(deletes))
		for id := range deletes {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if err := s.store.Delete(ids...); err != nil {
			s.logf("cron: delete finished one-shot schedules: %v\n", err)
		}
	}
}

// Flush writes any waiting last-fire deltas and one-shot deletes now; it
// returns when they are written. Tests use it; the writer does the same on its own.
func (s *Scheduler) Flush() { s.flush() }

func newID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "cron-" + hex.EncodeToString(buf), nil
}

// idPattern reports whether name has the id form cron-<hex>.
func idShaped(name string) bool {
	rest, ok := strings.CutPrefix(name, "cron-")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func checkName(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("name is empty")
	}
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return "", fmt.Errorf("name is longer than %d characters", MaxNameRunes)
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errors.New("name holds a control character")
	}
	name = strings.TrimSpace(name)
	if idShaped(name) {
		return "", errors.New(`name must not look like a schedule id ("cron-" followed by hex)`)
	}
	return name, nil
}

// IsLead reports whether owner is a hosted lead conversation the daemon can
// deliver to; the error says it is not (closed, handed off or unknown).
func (s *Scheduler) IsLead(owner string) (bool, error) { return s.sessions.IsLead(owner) }

// Add registers a recurring schedule for conversation owner, which must be a
// hosted lead, and returns its id and first fire (RFC 3339 in the schedule's
// offset). tz is empty, UTC, Z or a fixed offset (ParseTZ).
func (s *Scheduler) Add(owner, name, expr, prompt, tz string) (id, nextAt string, err error) {
	return s.add(owner, name, prompt, func(now time.Time) (*schedule, error) {
		parsed, err := Parse(expr)
		if err != nil {
			return nil, err
		}
		tzName, loc, err := ParseTZ(tz)
		if err != nil {
			return nil, err
		}
		next, ok := parsed.Next(now, loc)
		if !ok {
			return nil, fmt.Errorf("expr %q: %w", parsed, ErrNeverMatches)
		}
		return &schedule{expr: parsed, tz: tzName, loc: loc, next: next}, nil
	})
}

// AddAt registers a one-shot schedule for conversation owner, which must be
// a hosted lead: it fires once at or after at, then is deleted. at must be
// in the future. The schedule reports times in at's own offset when that is
// an accepted tz (ParseTZ), else in UTC.
func (s *Scheduler) AddAt(owner, name, prompt string, at time.Time) (id, nextAt string, err error) {
	return s.add(owner, name, prompt, func(now time.Time) (*schedule, error) {
		if !at.After(now) {
			return nil, fmt.Errorf("%w (at %s, now %s)", ErrAtNotFuture, at.Format(time.RFC3339), now.In(at.Location()).Format(time.RFC3339))
		}
		tzName, loc := onceZone(at)
		return &schedule{once: true, runAt: at, tz: tzName, loc: loc, next: at}, nil
	})
}

// onceZone is the accepted tz of at's offset, or UTC.
func onceZone(at time.Time) (string, *time.Location) {
	_, off := at.Zone()
	if off == 0 || off%60 != 0 {
		return "UTC", time.UTC
	}
	sign := '+'
	if off < 0 {
		sign, off = '-', -off
	}
	if name, loc, err := ParseTZ(fmt.Sprintf("%c%02d:%02d", sign, off/3600, off%3600/60)); err == nil {
		return name, loc
	}
	return "UTC", time.UTC
}

// add runs Add and AddAt's shared checks and the store write; build validates
// the schedule's timing at the add's clock reading and returns it with expr
// or once/runAt, tz, loc and next set.
func (s *Scheduler) add(owner, name, prompt string, build func(now time.Time) (*schedule, error)) (id, nextAt string, err error) {
	if lead, err := s.sessions.IsLead(owner); err != nil || !lead {
		if err != nil {
			return "", "", fmt.Errorf("%w: %v", ErrNotLead, err)
		}
		return "", "", ErrNotLead
	}
	name, err = checkName(name)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(prompt) == "" {
		return "", "", errors.New("prompt is empty")
	}
	if len(prompt) > MaxPromptSize {
		return "", "", fmt.Errorf("prompt is longer than %d bytes", MaxPromptSize)
	}
	now := s.clock.Now()
	sch, err := build(now)
	if err != nil {
		return "", "", err
	}
	if id, err = newID(); err != nil {
		return "", "", err
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()
	// The owner is checked again while serialized with ConversationGone: the
	// runner forgets a conversation before it tells the scheduler, so an owner
	// that is gone by now is refused here, and one that goes after this check
	// is purged by the ConversationGone that waits behind this Add.
	if !s.sessions.Exists(owner) {
		return "", "", fmt.Errorf("%w: the session has ended", ErrNotLead)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", "", ErrClosed
	}
	mine := s.byOwner[owner]
	if len(mine) >= MaxPerSession {
		s.mu.Unlock()
		return "", "", ErrTooMany
	}
	for _, other := range mine {
		if other.name == name {
			s.mu.Unlock()
			return "", "", ErrDuplicateName
		}
	}
	s.mu.Unlock()

	sch.id, sch.owner, sch.name, sch.prompt, sch.created = id, owner, name, prompt, now
	row := Row{ID: id, Owner: owner, Name: name, TZ: sch.tz, Prompt: prompt, CreatedAt: now.Unix()}
	if sch.once {
		row.RunAt = sch.runAt.Unix()
	} else {
		row.Expr = sch.expr.String()
	}
	if err := s.store.Put(row); err != nil {
		return "", "", err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", "", ErrClosed
	}
	// The first fire is the first minute after the add (a one-shot's at), as
	// computed at the add; a timer that did not run yet cannot have passed it.
	s.insertLocked(sch)
	s.armLocked()
	s.mu.Unlock()
	return id, sch.next.In(sch.loc).Format(time.RFC3339), nil
}

// Delete removes the schedule key (its id or its name) of conversation
// owner. A key that names no schedule of owner, including another session's
// schedule, is ErrUnknownSchedule.
func (s *Scheduler) Delete(owner, key string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	key = strings.TrimSpace(key)
	s.mu.Lock()
	var target *schedule
	byName := 0
	for _, sch := range s.byOwner[owner] {
		if sch.id == key {
			target, byName = sch, 0
			break
		}
		if sch.name == key {
			target = sch
			byName++
		}
	}
	s.mu.Unlock()
	if target == nil {
		return ErrUnknownSchedule
	}
	if byName > 1 {
		// Only after a downgrade period (New keeps both): the caller picks
		// one by id.
		return ErrAmbiguousName
	}
	if err := s.store.Delete(target.id); err != nil {
		return err
	}
	s.mu.Lock()
	s.removeLocked(target)
	delete(s.dirty, target.id)
	delete(s.deletes, target.id)
	s.armLocked()
	// Out of the maps no new delivery is admitted; every one already admitted
	// (overlapping ones included: the next minute can fire while an earlier
	// hand-off still waits on the runner) is waited for, so a prompt is never
	// published after Delete returned.
	for target.inflight > 0 {
		s.delivered.Wait()
	}
	s.mu.Unlock()
	return nil
}

// List reports owner's schedules, oldest first.
func (s *Scheduler) List(owner string) []Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	mine := make([]*schedule, 0, len(s.byOwner[owner]))
	for _, sch := range s.byOwner[owner] {
		mine = append(mine, sch)
	}
	sort.Slice(mine, func(i, j int) bool {
		if !mine[i].created.Equal(mine[j].created) {
			return mine[i].created.Before(mine[j].created)
		}
		return mine[i].name < mine[j].name
	})
	out := make([]Info, len(mine))
	for i, sch := range mine {
		info := Info{ID: sch.id, Name: sch.name, TZ: sch.tz, Pending: sch.pending, Dropped: sch.dropped}
		if sch.once {
			info.At = sch.runAt.In(sch.loc).Format(time.RFC3339)
		} else {
			info.Expr = sch.expr.String()
		}
		if !sch.next.IsZero() {
			info.NextAt = sch.next.In(sch.loc).Format(time.RFC3339)
		}
		if !sch.last.IsZero() {
			info.LastAt = sch.last.In(sch.loc).Format(time.RFC3339)
		}
		out[i] = info
	}
	return out
}

// ConversationGone deletes every schedule of conversation owner: its session
// ended permanently (Decision 11).
func (s *Scheduler) ConversationGone(owner string) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.goneLocked(owner)
}

// goneLocked deletes owner's schedules; opMu is held.
func (s *Scheduler) goneLocked(owner string) {
	s.mu.Lock()
	n := len(s.byOwner[owner])
	s.mu.Unlock()
	if n == 0 {
		return
	}
	if err := s.store.DeleteOwner(owner); err != nil {
		s.logf("cron: delete schedules of an ended session: %v\n", err)
	}
	s.mu.Lock()
	for _, sch := range s.byOwner[owner] {
		s.removeLocked(sch)
		delete(s.dirty, sch.id)
		delete(s.deletes, sch.id)
	}
	s.armLocked()
	s.mu.Unlock()
}

// Close stops the timer, fences new deliveries, waits for the admitted ones,
// writes the waiting last-fire deltas and closes the store. It returns only
// once nothing the scheduler started is still running, so a caller may release
// daemon ownership after it; a concurrent caller waits for the same
// completion and gets the same result.
func (s *Scheduler) Close() error {
	s.opMu.Lock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.opMu.Unlock()
		<-s.closeDone
		return s.closeErr
	}
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	for s.inflight > 0 {
		s.delivered.Wait()
	}
	s.mu.Unlock()
	s.opMu.Unlock()
	if s.stopObs != nil {
		s.stopObs()
	}
	close(s.writeQuit)
	<-s.writeDone
	err := s.store.Close()
	close(s.logQuit)
	select {
	case <-s.logDone:
	case <-time.After(logCloseWait):
	}
	s.closeErr = err
	close(s.closeDone)
	return err
}

// logf queues a diagnostic; it never blocks and never touches the log file.
func (s *Scheduler) logf(format string, args ...any) {
	select {
	case s.logCh <- fmt.Sprintf(format, args...):
	default:
		s.logDropped.Add(1)
	}
}

// LogDropped is the number of diagnostics dropped because the log was slower
// than the scheduler produced them.
func (s *Scheduler) LogDropped() int { return int(s.logDropped.Load()) }

// logger writes the queued diagnostics, and says how many were dropped.
func (s *Scheduler) logger() {
	defer close(s.logDone)
	write := func(msg string) {
		if n := s.logDropped.Swap(0); n > 0 {
			fmt.Fprintf(s.log, "cron: %d diagnostics dropped while the log was busy\n", n)
		}
		io.WriteString(s.log, msg)
	}
	for {
		select {
		case msg := <-s.logCh:
			write(msg)
		case <-s.logQuit:
			for {
				select {
				case msg := <-s.logCh:
					write(msg)
				default:
					return
				}
			}
		}
	}
}

// Reconcile deletes the schedules of every conversation the runner no longer
// knows. The daemon runs it right after binding ConversationGone, so a
// conversation forgotten between New's existence check and the binding, whose
// notice went nowhere, is still reaped.
func (s *Scheduler) Reconcile() {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	owners := make([]string, 0, len(s.byOwner))
	for owner := range s.byOwner {
		owners = append(owners, owner)
	}
	s.mu.Unlock()
	for _, owner := range owners {
		if !s.sessions.Exists(owner) {
			s.goneLocked(owner)
		}
	}
}
