package wsauditstore

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"sort"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

// makeRoom makes need more bytes fit the store total, in this order: whole
// least-recently-used rings other than the active one lose their data
// (keeping their recovery state as a tombstone); then the active ring drops
// its own oldest segments, keeping its newest; only then do tombstones go,
// least recently used first, recovery state and all. A tombstone is never
// evicted into another tombstone. A ring whose eviction fails is passed
// over by later calls until the next sweep (sessionInfo.evictFailed).
// active.mu is held.
func (s *Store) makeRoom(active *sessionInfo, need int64) {
	trimmed := false
	for {
		s.mu.Lock()
		if s.total+need <= s.limits.TotalBytes {
			s.mu.Unlock()
			return
		}
		pick := func(tombstones bool) *sessionInfo {
			var victim *sessionInfo
			for _, info := range s.sessions {
				if info == active || info.bytes == 0 || info.evictFailed || info.newer || info.tombstone != tombstones {
					continue
				}
				if victim == nil || info.lastSeen.Before(victim.lastSeen) ||
					(info.lastSeen.Equal(victim.lastSeen) && info.dir < victim.dir) {
					victim = info
				}
			}
			return victim
		}
		victim := pick(false)
		forget := false
		if victim == nil && trimmed {
			victim, forget = pick(true), true
		}
		room := s.limits.TotalBytes - (s.total - active.bytes) - need
		s.mu.Unlock()
		if victim == nil {
			if trimmed {
				return
			}
			trimmed = true
			if !s.foreignLocked(active) {
				s.trimRingLocked(active, max(room, 0))
			}
			continue
		}
		if s.foreign(victim) {
			s.logf("store total cap: keep audit ring of session %s: it holds a newer format", victim.dir)
			s.mu.Lock()
			victim.evictFailed = true
			s.mu.Unlock()
			continue
		}
		var err error
		if forget {
			s.logf("store total cap: forget the recovery state of session %s (%d bytes, last seen %s)", victim.dir, victim.bytes, formatTime(victim.lastSeen))
			err = s.removeRing(victim, true)
		} else {
			s.logf("store total cap: evict audit ring of session %s (%d bytes, last seen %s)", victim.dir, victim.bytes, formatTime(victim.lastSeen))
			err = s.removeRing(victim, false)
		}
		if err != nil {
			s.logf("store total cap: %v; not retried before the next sweep", err)
			s.mu.Lock()
			victim.evictFailed = true
			s.mu.Unlock()
		}
	}
}

// trimRingLocked drops a ring's oldest whole segments until it holds at most
// capBytes, keeping its newest. What remains must still seed the recovery
// state: it opens with a segment whose complete header snapshots everything
// before it. An empty or torn newest segment (a crash right after rotation)
// carries none, so the newest segment with a complete header is kept with
// everything after it, even over the cap, and nothing is deleted when no
// such segment can be read. A loaded ring whose newest segment lacks a header
// writes one first. info.mu is held.
func (s *Store) trimRingLocked(info *sessionInfo, capBytes int64) {
	if r := info.ring; r != nil {
		if r.forceRotate && r.readOnly == nil && r.bytes > capBytes && len(r.segs) > 1 && r.hasState() {
			if _, err := r.write(nil, s.limits.SegmentBytes(), math.MaxInt64, nil); err != nil {
				s.logf("session %s: persist its recovery state before trimming: %v; nothing trimmed", info.dir, err)
				s.account(info)
				return
			}
			r.clearPending()
			delete(s.dirty, info)
		}
		r.trimTo(capBytes)
		s.noteEvictErr(info)
		s.account(info)
		return
	}
	defer info.changing()()
	dir := filepath.Join(s.opts.Root, info.dir)
	segs, _, err := listSegments(dir)
	if err != nil {
		return
	}
	var size int64
	for _, seg := range segs {
		size += seg.size
	}
	// The first segment the cap allows to remain, then the nearest one at or
	// after it — else before it — that opens with a complete header.
	fit, left := 0, size
	for fit < len(segs)-1 && left > capBytes {
		left -= segs[fit].size
		fit++
	}
	keep := -1
	if fit > 0 {
		valid := func(i int) bool {
			head, err := readHead(filepath.Join(dir, segmentName(segs[i].seq)), int(HeaderBound)+1)
			return err == nil && completeHeader(head)
		}
		for i := fit; i < len(segs) && keep < 0; i++ {
			if valid(i) {
				keep = i
			}
		}
		for i := fit - 1; i >= 0 && keep < 0; i-- {
			if valid(i) {
				keep = i
			}
		}
		switch {
		case keep < 0:
			s.logf("session %s: no segment opens with a readable complete header; nothing trimmed", info.dir)
		case keep < fit:
			s.logf("session %s: the newest segment with a complete header is %d; kept over the cap", info.dir, segs[keep].seq)
		}
	}
	for i := 0; i < keep; i++ {
		if err := removeFile(filepath.Join(dir, segmentName(segs[0].seq))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.logf("session %s: delete segment %d: %v; the ring stays over its cap until a later sweep retries", info.dir, segs[0].seq, err)
			break
		}
		size -= segs[0].size
		segs = segs[1:]
	}
	s.mu.Lock()
	s.total += size - info.bytes
	info.bytes = size
	s.mu.Unlock()
}

// enforceCaps applies the byte caps to rings already on disk — for limits
// lowered since they were written, or deletions that failed earlier: every
// ring over the per-session cap drops its oldest whole segments, then whole
// least-recently-used rings are evicted under the store total, the most
// recently seen last (trimmed, keeping its newest segment). Rings of a newer
// format are left alone. Writer goroutine, or Open before it starts.
func (s *Store) enforceCaps() {
	capBytes := s.limits.EffectiveSessionBytes()
	s.mu.Lock()
	var over []*sessionInfo
	var newest *sessionInfo
	for _, info := range s.sessions {
		if info.bytes > capBytes {
			over = append(over, info)
		}
		if newest == nil || info.lastSeen.After(newest.lastSeen) ||
			(info.lastSeen.Equal(newest.lastSeen) && info.dir > newest.dir) {
			newest = info
		}
	}
	s.mu.Unlock()
	sort.Slice(over, func(i, j int) bool { return over[i].dir < over[j].dir })
	for _, info := range over {
		if s.foreign(info) {
			continue
		}
		s.logf("session cap: trim audit ring of session %s (%d bytes, cap %d)", info.dir, info.bytes, capBytes)
		info.mu.Lock()
		s.trimRingLocked(info, capBytes)
		info.mu.Unlock()
	}
	if newest != nil {
		newest.mu.Lock()
		s.makeRoom(newest, 0)
		newest.mu.Unlock()
	}
}

// removeRing deletes a ring's data. A ring that loses its data to
// store-wide eviction keeps its recovery state — read from its newest
// segments first when it was not loaded — and persists it as a header-only
// segment written after every existing one BEFORE any of them is deleted, so
// a crash at any point leaves the boundary on disk and a later record cannot
// replay what was already finalized. forget (retention, explicit deletion)
// drops the whole directory and the entry. A deletion that fails is reported
// and leaves what remains on disk accounted.
func (s *Store) removeRing(info *sessionInfo, forget bool) error {
	if forget {
		return s.forgetRing(info)
	}
	info.mu.Lock()
	defer info.changing()()
	r := info.ring
	cold := r == nil
	dir := filepath.Join(s.opts.Root, info.dir)
	if cold {
		r = recoverTail(info.session, dir)
		if r == nil {
			info.mu.Unlock()
			return fmt.Errorf("evict audit ring of session %s: its segments cannot be listed or read; nothing deleted", info.dir)
		}
	}
	if r.hasState() {
		// The header below carries the queued loss and boundary lines' state;
		// they are dropped only once it is written, so a failed snapshot
		// leaves them for the idle flush or Close to persist.
		r.forceRotate = true
		if _, err := r.write(nil, s.limits.SegmentBytes(), math.MaxInt64, nil); err != nil {
			if cold {
				r.close()
				left, _ := ringBytes(dir) // a torn header segment may have landed
				s.mu.Lock()
				s.total += left - info.bytes
				info.bytes = left
				s.mu.Unlock()
			} else {
				s.account(info)
			}
			info.mu.Unlock()
			return fmt.Errorf("evict audit ring of session %s: persist its recovery state first: %w; nothing deleted", info.dir, err)
		}
		r.clearPending()
		delete(s.dirty, info)
	}
	var err error
	if r.hasState() {
		for len(r.segs) > 1 {
			if _, derr := r.dropOldest(); derr != nil {
				err = fmt.Errorf("evict audit ring of session %s: %w (%d bytes remain)", info.dir, r.evictErr, r.bytes)
				r.evictErr = nil
				break
			}
		}
		if cold {
			// Cold eviction never leaves the ring loaded or holding an open
			// segment handle (whether it completed as a tombstone or failed
			// mid-drop): close r.file, account bytes and tombstone state, and
			// keep info unloaded so the next use replays what remains.
			r.close()
			left := r.bytes
			if err != nil {
				left, _ = ringBytes(dir)
			}
			s.mu.Lock()
			s.total += left - info.bytes
			info.bytes = left
			if info.session == "" {
				info.session = r.session
			}
			info.tombstone = err == nil
			delete(s.resident, info)
			s.mu.Unlock()
			r = nil
		}
	} else {
		// No state worth keeping: the data goes whole.
		r.close()
		if rerr := removeAll(dir); rerr != nil {
			err = fmt.Errorf("evict audit ring of session %s: %w", info.dir, rerr)
		}
		left, _ := ringBytes(dir)
		if cold || left > 0 {
			r = nil // cold stays unloaded; failed removal reloads from what remains
		} else {
			r = newRing(r.session, dir)
		}
		s.mu.Lock()
		s.total += left - info.bytes
		info.bytes = left
		if r == nil {
			delete(s.resident, info)
		}
		s.mu.Unlock()
	}
	if r != nil {
		info.ring = r
		info.view.Store(r)
		info.led.Store(r.led)
		if info.session == "" {
			info.session = r.session
		}
		s.account(info)
		s.mu.Lock()
		info.tombstone = err == nil && r.hasState()
		s.resident[info] = struct{}{}
		s.mu.Unlock()
	} else {
		info.ring = nil
		info.view.Store(nil)
		info.led.Store(nil)
		s.mu.Lock()
		delete(s.resident, info)
		s.mu.Unlock()
	}
	info.mu.Unlock()
	return err
}

// forgetRing deletes a ring's whole directory and, once it is gone, the
// entry. A failed deletion leaves what remains accounted, to be reloaded
// from on next use.
func (s *Store) forgetRing(info *sessionInfo) error {
	info.mu.Lock()
	defer info.changing()()
	if r := info.ring; r != nil {
		r.close()
	}
	dir := filepath.Join(s.opts.Root, info.dir)
	err := removeAll(dir)
	info.ring = nil
	info.view.Store(nil)
	info.led.Store(nil)
	delete(s.dirty, info)
	info.mu.Unlock()
	var left int64
	if err != nil {
		// Relisted outside s.mu: every session's Recover, Retained and
		// AppendSourced take it, and must not wait on this disk.
		left, _ = ringBytes(dir)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.resident, info)
	if err != nil {
		s.total += left - info.bytes
		info.bytes = left
		return fmt.Errorf("remove audit ring of session %s: %w (%d bytes remain)", info.dir, err, left)
	}
	s.total -= info.bytes
	info.bytes = 0
	if s.sessions[info.dir] == info {
		delete(s.sessions, info.dir)
	}
	return nil
}

// ringBytes is the bytes of the ring's segments left on disk.
func ringBytes(dir string) (int64, error) {
	segs, _, err := listSegments(dir)
	var n int64
	for _, seg := range segs {
		n += seg.size
	}
	return n, err
}

func (s *Store) deleteSession(session string) error {
	dir := sessionDirName(wsaudit.NormalizeID(session))
	s.mu.Lock()
	info := s.sessions[dir]
	s.mu.Unlock()
	if info == nil {
		return removeAll(filepath.Join(s.opts.Root, dir))
	}
	return s.removeRing(info, true)
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

// sweep first carries every registered session's registry last-seen time
// into the store index (refreshLastSeen), then removes every ring last seen
// longer than the retention period ago, then enforces the byte caps.
func (s *Store) sweep() {
	s.refreshLastSeen()
	now := s.opts.Now()
	s.mu.Lock()
	var expired []*sessionInfo
	for _, info := range s.sessions {
		info.evictFailed = false // eviction is retried once per sweep
		if now.Sub(info.lastSeen) > s.limits.Retention {
			expired = append(expired, info)
		}
	}
	s.mu.Unlock()
	sort.Slice(expired, func(i, j int) bool { return expired[i].dir < expired[j].dir })
	for _, info := range expired {
		if s.foreign(info) {
			s.logf("retention: keep audit ring of session %s: it holds a newer format", info.dir)
			continue
		}
		s.logf("retention: remove audit ring of session %s (last seen %s)", info.dir, formatTime(info.lastSeen))
		if err := s.removeRing(info, true); err != nil {
			s.logf("retention: %v", err)
		}
	}
	s.enforceCaps()
	s.evictIdleRings(true, nil)
}

// evictIdleRings unloads idle unpinned rings to release segment file handles
// and in-memory indexes without deleting any data on disk. When includeTTL is
// set and IdleTTL > 0, every unpinned ring whose in-memory lastActive is at
// least IdleTTL old is unloaded regardless of total count. Then, when
// MaxLoadedRings > 0 and total loaded rings (pinned + unpinned) still exceed
// MaxLoadedRings, the least-recently-active unpinned rings are unloaded even
// within IdleTTL until total loaded rings <= MaxLoadedRings or none remain.
// protect, when non-nil, is skipped (for the ring s.info just loaded).
// Writer goroutine only.
func (s *Store) evictIdleRings(includeTTL bool, protect *sessionInfo) {
	type candidate struct {
		info       *sessionInfo
		lastActive time.Time
	}
	sortCandidates := func(cs []candidate) {
		sort.Slice(cs, func(i, j int) bool {
			if !cs[i].lastActive.Equal(cs[j].lastActive) {
				return cs[i].lastActive.Before(cs[j].lastActive)
			}
			return cs[i].info.dir < cs[j].info.dir
		})
	}
	now := s.opts.Now()
	if includeTTL && s.opts.IdleTTL > 0 {
		s.mu.Lock()
		var ttlDue []candidate
		for info := range s.resident {
			if info == protect || s.pins[info.dir] > 0 {
				continue
			}
			if now.Sub(info.lastActive) >= s.opts.IdleTTL {
				ttlDue = append(ttlDue, candidate{info: info, lastActive: info.lastActive})
			}
		}
		s.mu.Unlock()
		sortCandidates(ttlDue)
		for _, c := range ttlDue {
			s.unloadRing(c.info, true, now)
		}
	}
	if s.opts.MaxLoadedRings <= 0 {
		return
	}
	s.mu.Lock()
	if len(s.resident) <= s.opts.MaxLoadedRings {
		s.mu.Unlock()
		return
	}
	lru := make([]candidate, 0, len(s.resident))
	for info := range s.resident {
		if info == protect || s.pins[info.dir] > 0 {
			continue
		}
		lru = append(lru, candidate{info: info, lastActive: info.lastActive})
	}
	s.mu.Unlock()
	sortCandidates(lru)
	for _, c := range lru {
		if s.loadedCount() <= s.opts.MaxLoadedRings {
			break
		}
		s.unloadRing(c.info, false, now)
	}
}

// unloadRing flushes any buffered control lines on info's ring, coordinates
// with concurrent cold View readers via info.changing(), drops info's
// in-memory ring/view/ledger references, and closes its newest-segment handle.
// It deletes no data and leaves info.lastSeen untouched. If flushing
// pendingLines fails, it aborts the unload and keeps the ring dirty.
// Writer goroutine only.
func (s *Store) unloadRing(info *sessionInfo, checkTTL bool, now time.Time) bool {
	info.mu.Lock()
	r := info.ring
	if r == nil {
		info.mu.Unlock()
		return false
	}
	s.mu.Lock()
	skip := s.pins[info.dir] > 0 || (checkTTL && s.opts.IdleTTL > 0 && now.Sub(info.lastActive) < s.opts.IdleTTL)
	s.mu.Unlock()
	if skip {
		info.mu.Unlock()
		return false
	}
	if r.readOnly == nil {
		if payload := r.pendingLines(); len(payload) > 0 {
			if err := s.writeLocked(info, payload); err != nil {
				s.dirty[info] = struct{}{}
				s.logf("session %s: flush pending control lines before idle unload: %v; keeping ring loaded", info.dir, err)
				info.mu.Unlock()
				return false
			}
			r.clearPending()
			delete(s.dirty, info)
		}
	}
	// Re-check pin and TTL under s.mu in case a caller pinned or touched
	// lastActive while pendingLines was flushing. Start info.changing() under
	// s.mu before clearing view/led so no concurrent View can sample the old
	// generation and then observe view==nil as a stable cold state.
	s.mu.Lock()
	if s.pins[info.dir] > 0 || (checkTTL && s.opts.IdleTTL > 0 && now.Sub(info.lastActive) < s.opts.IdleTTL) {
		s.mu.Unlock()
		info.mu.Unlock()
		return false
	}
	end := info.changing()
	info.ring = nil
	info.view.Store(nil)
	info.led.Store(nil)
	delete(s.resident, info)
	s.mu.Unlock()
	r.close()
	end()
	info.mu.Unlock()
	return true
}

// refreshLastSeen records each ring's registry last-seen time when it moved
// past the recorded one. It runs at the periodic sweep and at Close — never
// per heartbeat — so a quiet session's latest heartbeat survives a registry
// prune the daemon did not witness, at most one sweep interval stale.
func (s *Store) refreshLastSeen() {
	if s.opts.LastSeen == nil {
		return
	}
	s.mu.Lock()
	infos := make([]*sessionInfo, 0, len(s.sessions))
	for _, info := range s.sessions {
		if info.session != "" {
			infos = append(infos, info)
		}
	}
	s.mu.Unlock()
	sort.Slice(infos, func(i, j int) bool { return infos[i].dir < infos[j].dir })
	for _, info := range infos {
		if t, ok := s.opts.LastSeen(info.session); ok && t.After(info.recorded) {
			s.touchNormalized(info.session, t)
		}
	}
}

// foreign reports whether info's ring holds a newer format: a loaded ring
// refusing writes, or an unloaded one whose segment headers say so — or whose
// headers cannot be read, so automatic retention fails closed. A newer format
// is remembered; a read failure is asked again. Writer goroutine only.
func (s *Store) foreign(info *sessionInfo) bool {
	info.mu.Lock()
	defer info.mu.Unlock()
	return s.foreignLocked(info)
}

// foreignLocked is foreign with info.mu held.
func (s *Store) foreignLocked(info *sessionInfo) bool {
	if info.newer {
		return true
	}
	if r := info.ring; r != nil {
		info.newer = errors.Is(r.readOnly, ErrNewerFormat)
		return info.newer
	}
	newer, err := newerRingOnDisk(filepath.Join(s.opts.Root, info.dir))
	if err != nil {
		// Fail closed, and ask again next time.
		s.logf("session %s: cannot read its segment headers: %v; it is not removed automatically", info.dir, err)
		return true
	}
	info.newer = newer
	return newer
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
