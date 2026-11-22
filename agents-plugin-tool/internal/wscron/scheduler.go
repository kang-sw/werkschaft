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

// Info is one schedule as List reports it.
type Info struct {
	ID, Name, Expr, TZ string
	// NextAt, LastAt are RFC 3339 in the schedule's offset; LastAt is empty
	// before the first fire.
	NextAt, LastAt string
	Pending        bool
	Dropped        int
}

type schedule struct {
	id, owner, name string
	expr            *Expr
	tz              string
	loc             *time.Location
	prompt          string
	created         time.Time
	next            time.Time // the next matching minute; zero when none
	last            time.Time // the last delivered minute
	pending         bool
	dropped         int
	closedLogged    bool
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
	stopObs func()
	// loadRemoved holds the cron entries seen leaving an inbox while New
	// loads; nil afterwards.
	loadRemoved map[string]bool

	writeKick chan struct{}
	writeDone chan struct{}
	writeQuit chan struct{}
}

// New builds a scheduler over opts and starts it: it loads every stored
// schedule once, deletes those whose conversation the runner no longer
// knows, rebuilds pending from the cron entries still held in inboxes, and
// arms the timer. Missed fires are not caught up.
func New(opts Options) (*Scheduler, error) {
	if opts.Store == nil || opts.Sessions == nil {
		return nil, errors.New("wscron: a store and sessions are required")
	}
	s := &Scheduler{
		store: opts.Store, sessions: opts.Sessions, clock: opts.Clock, log: opts.Log,
		byID: map[string]*schedule{}, byOwner: map[string]map[string]*schedule{}, dirty: map[string]int64{},
		writeKick: make(chan struct{}, 1), writeDone: make(chan struct{}), writeQuit: make(chan struct{}),
	}
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
			fmt.Fprintf(s.log, "cron: skip stored schedule %s: %v\n", r.ID, err)
			continue
		}
		if held[r.Owner] == nil {
			held[r.Owner] = map[string]bool{}
			for _, id := range s.sessions.HeldSchedules(r.Owner) {
				held[r.Owner][id] = true
			}
		}
		s.mu.Lock()
		sch.pending = held[r.Owner][sch.id] && !s.loadRemoved[sch.id]
		s.insertLocked(sch)
		s.mu.Unlock()
	}
	if len(gone) > 0 {
		if err := s.store.Delete(gone...); err != nil {
			fmt.Fprintf(s.log, "cron: reap schedules of ended sessions: %v\n", err)
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
		// A schedule deleted, or a scheduler closed, since the jobs were built
		// does not deliver.
		s.mu.Lock()
		if s.closed || s.byID[job.id] != job.sch {
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()
		out, err := s.sessions.Deliver(job.owner, job.mark, job.prompt)
		s.mu.Lock()
		switch {
		case err != nil || out != Delivered:
			job.sch.pending = false
			job.sch.dropped++
			if !job.sch.closedLogged {
				job.sch.closedLogged = true
				if err != nil {
					fmt.Fprintf(s.log, "cron: schedule %s (%s) not delivered: %v\n", job.id, job.sch.name, err)
				} else {
					fmt.Fprintf(s.log, "cron: schedule %s (%s) not delivered: its session is closed or handed off\n", job.id, job.sch.name)
				}
			}
		default:
			job.sch.closedLogged = false
			job.sch.last = job.at
			s.dirty[job.id] = job.at.Unix()
			select {
			case s.writeKick <- struct{}{}:
			default:
			}
		}
		s.mu.Unlock()
	}
}

// writer persists last-fire deltas off the delivery path, coalescing the
// fires that arrived while a write ran into one transaction.
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
	batch := s.dirty
	s.dirty = map[string]int64{}
	s.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if err := s.store.SetLastFire(batch); err != nil {
		fmt.Fprintf(s.log, "cron: persist last fires: %v\n", err)
	}
}

// Flush writes any waiting last-fire deltas now; it returns when they are
// written. Tests use it; the writer does the same on its own.
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

// Add registers a schedule for conversation owner, which must be a hosted
// lead, and returns its id and first fire (RFC 3339 in the schedule's
// offset). tz is empty, UTC, Z or a fixed offset (ParseTZ).
func (s *Scheduler) Add(owner, name, expr, prompt, tz string) (id, nextAt string, err error) {
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
	parsed, err := Parse(expr)
	if err != nil {
		return "", "", err
	}
	tzName, loc, err := ParseTZ(tz)
	if err != nil {
		return "", "", err
	}
	now := s.clock.Now()
	next, ok := parsed.Next(now, loc)
	if !ok {
		return "", "", fmt.Errorf("expr %q: %w", parsed, ErrNeverMatches)
	}
	if id, err = newID(); err != nil {
		return "", "", err
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()
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
	for _, sch := range mine {
		if sch.name == name {
			s.mu.Unlock()
			return "", "", ErrDuplicateName
		}
	}
	s.mu.Unlock()

	sch := &schedule{id: id, owner: owner, name: name, expr: parsed, tz: tzName, loc: loc, prompt: prompt, created: now, next: next}
	if err := s.store.Put(Row{ID: id, Owner: owner, Name: name, Expr: parsed.String(), TZ: tzName, Prompt: prompt, CreatedAt: now.Unix()}); err != nil {
		return "", "", err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", "", ErrClosed
	}
	// The first fire is the first minute after the add, as computed at the
	// add; a timer that did not run yet cannot have passed it.
	s.insertLocked(sch)
	s.armLocked()
	s.mu.Unlock()
	return id, next.Format(time.RFC3339), nil
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
	for _, sch := range s.byOwner[owner] {
		if sch.id == key || sch.name == key {
			target = sch
			break
		}
	}
	s.mu.Unlock()
	if target == nil {
		return ErrUnknownSchedule
	}
	if err := s.store.Delete(target.id); err != nil {
		return err
	}
	s.mu.Lock()
	s.removeLocked(target)
	delete(s.dirty, target.id)
	s.armLocked()
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
		info := Info{ID: sch.id, Name: sch.name, Expr: sch.expr.String(), TZ: sch.tz, Pending: sch.pending, Dropped: sch.dropped}
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
	s.mu.Lock()
	n := len(s.byOwner[owner])
	s.mu.Unlock()
	if n == 0 {
		return
	}
	if err := s.store.DeleteOwner(owner); err != nil {
		fmt.Fprintf(s.log, "cron: delete schedules of an ended session: %v\n", err)
	}
	s.mu.Lock()
	for _, sch := range s.byOwner[owner] {
		s.removeLocked(sch)
		delete(s.dirty, sch.id)
	}
	s.armLocked()
	s.mu.Unlock()
}

// Close stops the timer, writes the waiting last-fire deltas and closes the
// store. It is idempotent; a fire past its lock checks for Close before its
// hand-off, but one already inside Deliver may still complete.
func (s *Scheduler) Close() error {
	s.opMu.Lock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.opMu.Unlock()
		return nil
	}
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
	s.opMu.Unlock()
	if s.stopObs != nil {
		s.stopObs()
	}
	close(s.writeQuit)
	<-s.writeDone
	return s.store.Close()
}
