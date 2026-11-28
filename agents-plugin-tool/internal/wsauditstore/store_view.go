package wsauditstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

// coldView is a cached View of an unloaded ring, valid while the ring's
// entry and generation are the ones it was read at.
type coldView struct {
	info *sessionInfo
	gen  uint64
	view View
}

// coldFlight is one cold read of a ring generation, shared by every reader
// that misses the cache meanwhile. view, ok and err are set before done
// closes.
type coldFlight struct {
	info *sessionInfo
	gen  uint64
	done chan struct{}
	view View
	ok   bool
	err  error
}

// coldViews bounds the cached cold views.
const coldViews = 4

// View is a read-only snapshot of one session's retained events, for the
// query API.
type View struct {
	// Events are the retained events, oldest first, in strictly increasing
	// Seq. The slice is shared with the store: callers must not modify it or
	// its elements (Meta maps included).
	Events []wsaudit.Envelope
	// Cut reports that the session's current ring dropped older segments to
	// capacity eviction, so events older than Events[0] existed and are gone.
	// Whole-ring removal (time retention, session deletion) leaves no
	// evidence: a ring started afterwards reports false until it drops a
	// segment itself.
	Cut bool
}

// View returns a snapshot of the session's retained events and eviction
// state. A loaded ring answers from memory, and the read lock is held only to
// copy the slice header: a caller scanning the snapshot for as long as it
// likes never holds up the writer, which only appends past the snapshot's
// length or replaces the slice. An unloaded ring is read from disk on the
// caller's goroutine (coldRead), never through the writer, so a query never
// delays capture; while the writer is changing its files the read waits for
// the change to end and answers what follows it. An unknown session yields an
// empty View.
func (s *Store) View(ctx context.Context, session string) (View, error) {
	dir := sessionDirName(wsaudit.NormalizeID(session))
	wait := time.Millisecond
	for try := 0; ; try++ {
		// The generation is sampled with the entry, under s.mu, so before the
		// entry can be retired (removal advances it before the entry leaves
		// the map) and before the ring is found unloaded (a load published
		// afterwards advances it): the cold read below never accepts files a
		// loaded ring, or a replacement entry's ring, was writing meanwhile.
		s.mu.Lock()
		info := s.sessions[dir]
		var gen uint64
		if info != nil {
			gen = info.gen.Load()
		}
		s.mu.Unlock()
		if info == nil {
			return View{}, nil
		}
		viewLookupGap()
		if r := info.view.Load(); r != nil {
			r.view.RLock()
			v := View{Events: r.events[:len(r.events):len(r.events)], Cut: r.cut}
			r.view.RUnlock()
			return v, nil
		}
		viewColdGap()
		v, ok, err := s.coldRead(ctx, info, session, gen)
		if ok || err != nil {
			return v, err
		}
		if try == coldTries {
			return View{}, fmt.Errorf("audit ring of session %s: changed under %d reads; retry later", dir, coldTries)
		}
		select {
		case <-ctx.Done():
			return View{}, ctx.Err()
		case <-time.After(wait):
		}
		wait = min(2*wait, 64*time.Millisecond)
	}
}

// coldTries bounds the cold reads View makes of a ring the writer keeps
// changing (about ten seconds of waits).
const coldTries = 160

// viewLookupGap runs between View finding a ring's entry and checking
// whether it is loaded, and viewColdGap between finding it unloaded and
// reading it cold; tests hold a reader there.
var (
	viewLookupGap = func() {}
	viewColdGap   = func() {}
)

// coldCurrent reports that info is still unloaded at generation gen with no
// change under way: what was read at gen is what the ring holds.
func coldCurrent(info *sessionInfo, gen uint64) bool {
	return info.busy.Load() == 0 && info.gen.Load() == gen && info.view.Load() == nil
}

// coldRead answers an unloaded ring's View at generation gen, sampled
// before the ring was found unloaded: from the cache without touching the
// disk, else by joining the read of gen another reader has under way, else
// by reading it (replayCold) and caching it. ok is false, without error,
// when the ring is no longer unloaded at gen — before the read or by its end:
// what was read is dropped, never cached. The generation is checked and the
// flight or cached view published under coldMu, so a reader delayed with an
// older generation never displaces a newer one.
func (s *Store) coldRead(ctx context.Context, info *sessionInfo, session string, gen uint64) (View, bool, error) {
	s.coldMu.Lock()
	if !coldCurrent(info, gen) {
		s.coldMu.Unlock()
		return View{}, false, nil
	}
	if c, ok := s.cold[info.dir]; ok && c.info == info && c.gen == gen {
		s.coldMu.Unlock()
		return c.view, true, nil
	}
	f := s.flights[info.dir]
	if f != nil && f.info == info && f.gen == gen {
		s.coldMu.Unlock()
		select {
		case <-f.done:
		case <-ctx.Done():
			return View{}, false, ctx.Err()
		}
		if errors.Is(f.err, context.Canceled) || errors.Is(f.err, context.DeadlineExceeded) {
			return View{}, false, nil // its reader gave up; read again
		}
		return f.view, f.ok, f.err
	}
	f = &coldFlight{info: info, gen: gen, done: make(chan struct{})}
	if s.flights == nil {
		s.flights = map[string]*coldFlight{}
	}
	s.flights[info.dir] = f
	s.coldMu.Unlock()

	f.view, f.ok, f.err = s.replayCold(ctx, info, session, gen)

	s.coldMu.Lock()
	if s.flights[info.dir] == f {
		delete(s.flights, info.dir)
	}
	f.ok = f.ok && coldCurrent(info, gen)
	if f.ok {
		if s.cold == nil {
			s.cold = map[string]coldView{}
		}
		if _, ok := s.cold[info.dir]; !ok && len(s.cold) >= coldViews {
			for k := range s.cold {
				delete(s.cold, k) // any one: a miss only costs a reread
				break
			}
		}
		s.cold[info.dir] = coldView{info: info, gen: gen, view: f.view}
	}
	s.coldMu.Unlock()
	close(f.done)
	return f.view, f.ok, f.err
}

// replayCold reads an unloaded ring's segments into a View without loading
// the ring: nothing is truncated, opened for writing, or published, and a
// torn last line is left out as recovery would. ok reports that the ring
// stayed unloaded at generation gen until after the last read (coldRead
// checks again under coldMu); otherwise the result is dropped, and so is a
// read error the change may have caused.
func (s *Store) replayCold(ctx context.Context, info *sessionInfo, session string, gen uint64) (View, bool, error) {
	stable := func() bool { return coldCurrent(info, gen) }
	dir := filepath.Join(s.opts.Root, info.dir)
	segs, _, err := listSegments(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return View{}, stable(), nil
	}
	if err != nil {
		if !stable() {
			return View{}, false, nil
		}
		return View{}, false, err
	}
	r := newRing(session, dir)
	for i := range segs {
		if err := ctx.Err(); err != nil {
			return View{}, false, err
		}
		raw, err := readFile(filepath.Join(dir, segmentName(segs[i].seq)))
		if err != nil {
			if !stable() {
				return View{}, false, nil
			}
			return View{}, false, err
		}
		if _, newer, _ := r.replaySegment(&segs[i], raw); newer {
			break // later segments of a newer ring are not ours to parse
		}
	}
	v := View{Events: r.events[:len(r.events):len(r.events)], Cut: len(segs) > 0 && segs[0].seq > 1}
	return v, stable(), nil
}

// Events returns the session's retained events, oldest first, from memory.
// It never waits on the writer's disk I/O.
func (s *Store) Events(ctx context.Context, session string) ([]wsaudit.Envelope, error) {
	var out []wsaudit.Envelope
	err := s.scan(ctx, session, func(evs []wsaudit.Envelope) {
		out = append([]wsaudit.Envelope(nil), evs...)
	})
	return out, err
}

// scan calls fn with the session's retained events under the ring's read
// lock (fn must not keep the slice).
func (s *Store) scan(ctx context.Context, session string, fn func([]wsaudit.Envelope)) error {
	r, err := s.loadedRing(ctx, session)
	if err != nil || r == nil {
		return err
	}
	r.view.RLock()
	defer r.view.RUnlock()
	fn(r.events)
	return nil
}

// Event returns the retained event whose ID key is key. The store owns
// identifier normalization (Decision 3): producers hand it raw IDs and it
// normalizes each exactly once at ingestion, so a record's key is
// wsaudit.NormalizeID(raw ID) — the display key a history row carries. key is
// compared verbatim, never normalized again (a digest key normalized twice
// would name a different record). session is the raw session ID, like every
// other session argument.
func (s *Store) Event(ctx context.Context, session, key string) (wsaudit.Envelope, bool, error) {
	var out wsaudit.Envelope
	var found bool
	err := s.scan(ctx, session, func(evs []wsaudit.Envelope) {
		for i := len(evs) - 1; i >= 0; i-- {
			if evs[i].ID == key {
				out, found = evs[i], true
				return
			}
		}
	})
	return out, found, err
}

// EventsByCallID returns the retained events whose call key is key, oldest
// first. As for Event, key is wsaudit.NormalizeID(raw call ID) — the display
// key — and is compared verbatim.
func (s *Store) EventsByCallID(ctx context.Context, session, key string) ([]wsaudit.Envelope, error) {
	var out []wsaudit.Envelope
	err := s.scan(ctx, session, func(evs []wsaudit.Envelope) {
		for _, ev := range evs {
			if ev.CallID == key {
				out = append(out, ev)
			}
		}
	})
	return out, err
}

// RecoverCompactionBaseline finds the latest retained manual cut for the source.
// Capture calls it on reattachment, off the holder and conversation paths.
// Unlike Recover, it waits for an existing cold ring before answering.
func (s *Store) RecoverCompactionBaseline(ctx context.Context, session, source string) (boundary, counter uint64, err error) {
	r, err := s.loadedRing(ctx, session)
	if err != nil || r == nil {
		return 0, 0, err
	}
	source = wsaudit.NormalizeID(source)
	r.view.RLock()
	defer r.view.RUnlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		ev, id := r.events[i], r.ids[i]
		if id == nil || id.Source != source || ev.Kind != wsaudit.KindCompaction || ev.Meta["inferred_by"] != "command_end" {
			continue
		}
		// The synthetic identity already carries the cut's position even
		// though its SourceID has no sequenced record.
		pos, ok := strings.CutPrefix(id.Key, "inferred.compaction.")
		if !ok {
			continue
		}
		b, n, ok := strings.Cut(pos, ".")
		if !ok {
			continue
		}
		boundary, bErr := strconv.ParseUint(b, 10, 64)
		counter, nErr := strconv.ParseUint(n, 10, 64)
		if bErr == nil && nErr == nil && counter > 0 {
			return boundary, counter, nil
		}
	}
	return 0, 0, nil
}

// loadedRing returns the session's loaded *ring directly (from memory or via
// jobLoad) and refreshes lastActive under s.mu. Returning *ring directly keeps
// callers safe even if an unpinned ring is unloaded immediately after jobLoad
// under MaxLoadedRings pressure.
func (s *Store) loadedRing(ctx context.Context, session string) (*ring, error) {
	dir := sessionDirName(wsaudit.NormalizeID(session))
	now := s.opts.Now()
	s.mu.Lock()
	info := s.sessions[dir]
	if info != nil {
		info.lastActive = maxTime(info.lastActive, now)
	}
	s.mu.Unlock()
	if info == nil {
		return nil, nil
	}
	if r := info.view.Load(); r != nil {
		return r, nil
	}
	res, err := s.call(ctx, job{kind: jobLoad, session: session})
	if err != nil {
		return nil, err
	}
	return res.ring, nil
}

// loaded returns the session's entry with its ring in memory, recovering
// it through the writer on first use.
func (s *Store) loaded(ctx context.Context, session string) (*sessionInfo, error) {
	dir := sessionDirName(wsaudit.NormalizeID(session))
	now := s.opts.Now()
	s.mu.Lock()
	info := s.sessions[dir]
	if info != nil {
		info.lastActive = maxTime(info.lastActive, now)
	}
	s.mu.Unlock()
	if info == nil {
		return nil, nil
	}
	if info.view.Load() == nil {
		if _, err := s.call(ctx, job{kind: jobLoad, session: session}); err != nil {
			return nil, err
		}
		s.mu.Lock()
		info = s.sessions[dir]
		s.mu.Unlock()
	}
	return info, nil
}
