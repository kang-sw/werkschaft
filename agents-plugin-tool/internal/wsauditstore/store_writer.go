package wsauditstore

import (
	"fmt"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

func (s *Store) run() {
	defer close(s.done)
	var tick <-chan time.Time
	if s.opts.SweepInterval > 0 {
		t := time.NewTicker(s.opts.SweepInterval)
		defer t.Stop()
		tick = t.C
	}
	var idleTick <-chan time.Time
	if s.opts.IdleCheckInterval > 0 {
		t := time.NewTicker(s.opts.IdleCheckInterval)
		defer t.Stop()
		idleTick = t.C
	}
	for {
		select {
		case <-s.wake:
		case <-tick:
			s.sweep()
		case <-idleTick:
			s.evictIdleRings(true, nil)
		}
		for {
			batch := s.take()
			if len(batch) == 0 {
				break
			}
			for _, j := range batch {
				s.handle(j)
			}
			// A steady stream of work must not starve retention or idle
			// handle eviction: ticks that came due run between batches.
			select {
			case <-tick:
				s.sweep()
			default:
			}
			select {
			case <-idleTick:
				s.evictIdleRings(true, nil)
			default:
			}
		}
		if n := s.queueDrops.Swap(0); n > 0 {
			s.logf("dropped %d audit events: write queue full", n)
		}
		s.flushPending()
		if s.opts.MaxLoadedRings > 0 && s.loadedCount() > s.opts.MaxLoadedRings {
			s.evictIdleRings(false, nil)
		}
		if s.isClosed() {
			s.shutdown()
			return
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

func (s *Store) shutdown() {
	s.refreshLastSeen()
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
				if s.writeLocked(info, payload) == nil {
					r.clearPending()
				}
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
		_ = s.appendEvent(j.ev, j.id, j.rec, false)
	case jobCheckpoint:
		res.err = s.appendEvent(j.ev, j.id, j.rec, true)
		if res.err == nil {
			s.finalize(j.session, j.id.Source, j.id.Seq)
		}
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
			res.ring = info.view.Load()
			info.loading.Store(false)
		}
	case jobSweep:
		s.sweep()
	case jobUnloadIdle:
		if j.reply == nil {
			s.idleQueued.Store(false)
		}
		s.evictIdleRings(true, nil)
	case jobBarrier:
		s.flushPending()
		if j.fn != nil {
			j.fn()
		}
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
	now := s.opts.Now()
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
	info.lastActive = maxTime(info.lastActive, now)
	s.mu.Unlock()
	if info.ring != nil {
		return info, nil
	}
	end := info.changing() // recovery may truncate a torn tail
	r := newRing(session, filepath.Join(s.opts.Root, dir))
	if err := r.recover(); err != nil {
		end()
		return nil, err
	}
	if r.readOnly != nil {
		s.logf("session %s: %v; writes refused", session, r.readOnly)
	}
	info.mu.Lock()
	info.ring = r
	info.view.Store(r)
	info.session = session
	info.mu.Unlock()
	info.led.Store(r.led)
	s.mu.Lock()
	s.total += r.bytes - info.bytes
	info.bytes = r.bytes
	info.lastActive = maxTime(info.lastActive, s.opts.Now())
	s.resident[info] = struct{}{}
	overCap := s.opts.MaxLoadedRings > 0 && len(s.resident) > s.opts.MaxLoadedRings
	s.mu.Unlock()
	end()
	if overCap {
		s.evictIdleRings(false, info)
	}
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

func (s *Store) appendEvent(raw wsaudit.Envelope, id *wsaudit.SourceID, rec wsaudit.SourceID, checkpoint bool) error {
	ev, err := prepare(raw, s.limits)
	if err != nil {
		s.dropped.Add(1)
		s.logf("drop %s event of session %s: %v", raw.Kind, raw.Session, err)
		if raw.Session == "" || id == nil || checkpoint {
			return err
		}
		// A dropped record with an identity is finalized as lost.
		if info, err := s.info(wsaudit.NormalizeID(raw.Session), true); err == nil {
			info.mu.Lock()
			s.loseLocked(info, *id, wsaudit.LossAppendFailed)
			info.mu.Unlock()
		}
		return err
	}
	info, err := s.info(ev.Session, true)
	if err != nil {
		s.dropped.Add(1)
		s.logf("drop %s event of session %s: recover ring: %v", ev.Kind, ev.Session, err)
		return err
	}
	info.mu.Lock()
	defer info.mu.Unlock()
	r := info.ring
	if r.readOnly != nil {
		s.dropped.Add(1)
		return r.readOnly
	}
	switch r.admit(id, rec) {
	case admitDuplicate:
		return nil
	case admitLoss:
		if !checkpoint {
			s.loseLocked(info, *id, wsaudit.LossAppendFailed)
		}
		return nil
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
		if id != nil && !checkpoint {
			s.loseLocked(info, *id, wsaudit.LossAppendFailed)
		}
		return err
	}
	payload := append(r.pendingLines(), evLine...)
	if werr := s.writeLocked(info, payload); werr != nil {
		s.dropped.Add(1)
		s.logf("drop %s event of session %s: write: %v", ev.Kind, ev.Session, werr)
		if id != nil && !checkpoint {
			s.loseLocked(info, *id, wsaudit.LossAppendFailed)
		}
		return werr
	}
	r.clearPending()
	delete(s.dirty, info)
	r.segs[len(r.segs)-1].events++
	r.index(ev, id)
	info.lastSeen = maxTime(info.lastSeen, s.opts.Now())
	return nil
}

// writeLocked writes payload to info's ring, first making room under the
// store total, and syncs the byte accounting. info.mu is held.
func (s *Store) writeLocked(info *sessionInfo, payload []byte) error {
	r := info.ring
	err := r.writeBounded(payload, s.limits.SegmentBytes(), s.limits.EffectiveSessionBytes(), func(n int64) {
		s.account(info) // the ring's own trim first
		s.makeRoom(info, n)
	})
	s.noteEvictErr(info)
	s.mu.Lock()
	s.total += r.bytes - info.bytes
	info.bytes = r.bytes
	info.tombstone = false
	info.evictFailed = false // loaded now: a cold read failure no longer applies
	s.mu.Unlock()
	return err
}

// noteEvictErr logs a segment deletion the ring could not make; the segment
// stays accounted, so the caps are retried on later writes. info.mu is held.
func (s *Store) noteEvictErr(info *sessionInfo) {
	if r := info.ring; r != nil && r.evictErr != nil {
		s.logf("session %s: %v; the ring stays over its cap until a later write retries", info.dir, r.evictErr)
		r.evictErr = nil
	}
}

// loseLocked records a source record finalized without being retained; the
// loss line is written ahead of the ring's next record, or when the writer
// idles. info.mu is held.
func (s *Store) loseLocked(info *sessionInfo, id wsaudit.SourceID, reason string) {
	r := info.ring
	if !r.markLost(id) {
		return
	}
	s.noteLedgerTrims(info)
	reason = truncateUTF8(reason, maxReasonBytes)
	r.led.mu.Lock()
	n := r.led.loss
	r.led.mu.Unlock()
	if ln, err := encodeLine(line{T: lineLoss, ID: &id, Reason: reason, N: n}); err == nil {
		r.pendingLoss = append(r.pendingLoss, ln)
		s.dirty[info] = struct{}{}
	}
}

// noteLedgerTrims logs what bounding the ring's header state dropped since
// the store last looked. info.mu is held.
func (s *Store) noteLedgerTrims(info *sessionInfo) {
	l := info.ring.led
	l.mu.Lock()
	forgot, dropped := l.forgot, l.dropped
	l.forgot, l.dropped = 0, 0
	l.mu.Unlock()
	if forgot > 0 {
		s.logf("session %s: lost-identity list over %d bytes; %d lost identities forgotten (a replay of one after its loss line is gone may count again)", info.dir, lostBudget, forgot)
	}
	if dropped > 0 {
		s.logf("session %s: source state over %d bytes; the %d least recently finalized sources dropped (their replays are no longer refused as finalized)", info.dir, sourceBudget, dropped)
	}
}

// truncateUTF8 cuts s to at most n bytes on a rune boundary.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
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
	s.noteLedgerTrims(info)
	r.pendingFin[source] = boundary
	s.dirty[info] = struct{}{}
	// A ring holding only control lines is still in use.
	info.lastSeen = maxTime(info.lastSeen, s.opts.Now())
}
