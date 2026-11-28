package wsauditstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

func view(t *testing.T, s *Store, session string) View {
	t.Helper()
	v, err := s.View(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestViewReportsCapacityCutNotWholeRingRemoval pins the query API's read
// hook: a ring that dropped older segments to its cap reports Cut, live and
// after a restart; whole-ring time retention leaves no evidence, so a ring
// the session starts afterwards is uncut; an unknown session is empty.
func TestViewReportsCapacityCutNotWholeRingRemoval(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	clock := &fakeClock{now: time.Now()}
	lim := smallLimits(64 * mib)
	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	if v := view(t, s, "nobody"); len(v.Events) != 0 || v.Cut {
		t.Fatalf("unknown session view = %d events, cut %v", len(v.Events), v.Cut)
	}
	fill(t, s, "sess", 1, 20)
	if v := view(t, s, "sess"); v.Cut || len(v.Events) != 20 {
		t.Fatalf("uncut ring: %d events, cut %v", len(v.Events), v.Cut)
	}
	fill(t, s, "sess", 21, 130) // through the 4 MiB cap
	v := view(t, s, "sess")
	if !v.Cut || len(v.Events) == 0 || v.Events[0].Seq == 1 {
		t.Fatalf("evicted ring: %d events from seq %d, cut %v", len(v.Events), v.Events[0].Seq, v.Cut)
	}
	for i := 1; i < len(v.Events); i++ {
		if v.Events[i].Seq <= v.Events[i-1].Seq {
			t.Fatalf("view seq not increasing at %d", i)
		}
	}
	_ = s.Close()

	s2, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	if v := view(t, s2, "sess"); !v.Cut || len(v.Events) == 0 {
		t.Fatalf("recovered evicted ring: %d events, cut %v", len(v.Events), v.Cut)
	}
	clock.Advance(10 * 24 * time.Hour)
	if err := s2.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if v := view(t, s2, "sess"); len(v.Events) != 0 || v.Cut {
		t.Fatalf("expired ring: %d events, cut %v", len(v.Events), v.Cut)
	}
	mustAppend(t, s2, sourced("sess", 1, wsaudit.KindToolCall))
	flush(t, s2)
	if v := view(t, s2, "sess"); len(v.Events) != 1 || v.Cut {
		t.Fatalf("ring after expiry: %d events, cut %v", len(v.Events), v.Cut)
	}
}

// TestViewSnapshotNeverHoldsUpTheWriter pins that a reader scanning a View
// for as long as it likes holds no lock the writer needs: appends and an
// eviction land while the snapshot is still being read, and the snapshot
// keeps the events it was taken with.
func TestViewSnapshotNeverHoldsUpTheWriter(t *testing.T) {
	s, _ := openTest(t, t.TempDir(), func(o *Options) { o.Limits = smallLimits(64 * mib) })
	fill(t, s, "sess", 1, 10)
	snap := view(t, s, "sess")
	first := snap.Events[0].ID
	done := make(chan struct{})
	go func() {
		defer close(done)
		fill(t, s, "sess", 11, 130) // appends and evicts while snap is held
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
		t.Fatal("the writer waited on a held snapshot")
	}
	if len(snap.Events) != 10 || snap.Events[0].ID != first {
		t.Fatalf("snapshot changed under its reader: %d events, first %s", len(snap.Events), snap.Events[0].ID)
	}
	if v := view(t, s, "sess"); !v.Cut || v.Events[len(v.Events)-1].Seq != 140 {
		t.Fatalf("fresh view after the writes: cut %v, last seq %d", v.Cut, v.Events[len(v.Events)-1].Seq)
	}
}

// TestColdViewNeverOccupiesTheWriter pins that the query API's first read
// of an unloaded ring is served off the capture writer: with that read held
// inside a segment read, appends to another session are accepted past the
// queue depth, persisted and readable, and nothing is dropped. Released, the
// cold read answers what the loaded ring holds, and a repeat answers from
// its cache without rereading the segments.
func TestColdViewNeverOccupiesTheWriter(t *testing.T) {
	root := t.TempDir()
	s, _ := openTest(t, root, nil)
	for seq := uint64(1); seq <= 5; seq++ {
		mustAppend(t, s, sourced("cold", seq, wsaudit.KindToolCall))
	}
	flush(t, s)
	_ = s.Close()

	s2, _ := openTest(t, root, func(o *Options) { o.QueueDepth = 4 })
	dir := filepath.Join(root, sessionDirName("cold")) + string(filepath.Separator)
	orig := readFile
	t.Cleanup(func() { readFile = orig })
	entered, release := make(chan struct{}), make(chan struct{})
	var hold, freed sync.Once
	t.Cleanup(func() { freed.Do(func() { close(release) }) }) // before s2 closes
	var reads sync.Map
	readFile = func(path string) ([]byte, error) {
		if strings.HasPrefix(path, dir) {
			n, _ := reads.LoadOrStore(path, new(int))
			*n.(*int)++
			hold.Do(func() { close(entered); <-release })
		}
		return orig(path)
	}
	type answer struct {
		v   View
		err error
	}
	got := make(chan answer, 1)
	go func() {
		v, err := s2.View(context.Background(), "cold")
		got <- answer{v, err}
	}()
	<-entered // the cold read is inside its segment read

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for seq := uint64(1); seq <= 32; seq++ {
		it := sourced("live", seq, wsaudit.KindToolCall)
		if err := s2.AppendSourced(ctx, it.id, it.rec(), it.Envelope); err != nil {
			t.Fatalf("append %d while a cold read is held: %v", seq, err)
		}
		if seq%4 == 0 {
			if err := s2.Flush(ctx); err != nil {
				t.Fatalf("flush while a cold read is held: %v", err)
			}
		}
	}
	if n := len(events(t, s2, "live")); n != 32 || s2.Dropped() != 0 {
		t.Fatalf("while a cold read is held: %d live events, %d dropped", n, s2.Dropped())
	}

	freed.Do(func() { close(release) })
	a := <-got
	if a.err != nil || len(a.v.Events) != 5 || a.v.Cut {
		t.Fatalf("cold view = %d events, cut %v, err %v", len(a.v.Events), a.v.Cut, a.err)
	}
	listings, infos := countListings(t, filepath.Join(root, sessionDirName("cold")))
	again := view(t, s2, "cold")
	reads.Range(func(path, n any) bool {
		if *n.(*int) != 1 {
			t.Errorf("%s read %d times by two cold views", path, *n.(*int))
		}
		return true
	})
	if listings.Load() != 0 || infos.Load() != 0 {
		t.Errorf("a repeat cold view listed the ring %d times and statted %d entries", listings.Load(), infos.Load())
	}
	loaded(t, s2, "cold")
	warm := view(t, s2, "cold")
	if fmt.Sprint(ids(again.Events)) != fmt.Sprint(ids(warm.Events)) || again.Cut != warm.Cut {
		t.Fatalf("cold view %v (cut %v) differs from the loaded ring's %v (cut %v)", ids(again.Events), again.Cut, ids(warm.Events), warm.Cut)
	}
}

// TestColdViewFirstReadsShareOneReplay pins that simultaneous first reads
// of an unloaded ring share one replay: eight Views racing the first one
// (held inside its first segment read) list the ring once, stat each
// segment once, read each segment once, and all answer the same events.
func TestColdViewFirstReadsShareOneReplay(t *testing.T) {
	root := t.TempDir()
	lim := smallLimits(64 * mib)
	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim })
	fill(t, s, "cold", 1, 24) // two segments
	_ = s.Close()
	ringDir := filepath.Join(root, sessionDirName("cold"))
	segs, _, err := listSegments(ringDir)
	if err != nil || len(segs) < 2 {
		t.Fatalf("fixture: %+v %v", segs, err)
	}

	s2, _ := openTest(t, root, func(o *Options) { o.Limits = lim })
	listings, infos := countListings(t, ringDir)
	orig := readFile
	t.Cleanup(func() { readFile = orig })
	entered, release := make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	var freed sync.Once
	t.Cleanup(func() { freed.Do(func() { close(release) }) }) // before s2 closes
	var mu sync.Mutex
	reads := map[string]int{}
	readFile = func(path string) ([]byte, error) {
		if filepath.Dir(path) == ringDir {
			mu.Lock()
			reads[path]++
			mu.Unlock()
			if held.CompareAndSwap(false, true) {
				close(entered)
				<-release
			}
		}
		return orig(path)
	}
	const readers = 8
	type answer struct {
		v   View
		err error
	}
	got := make(chan answer, readers)
	read := func() {
		v, err := s2.View(context.Background(), "cold")
		got <- answer{v, err}
	}
	go read()
	<-entered
	for i := 1; i < readers; i++ {
		go read()
	}
	time.Sleep(200 * time.Millisecond) // the racers reach the shared read
	freed.Do(func() { close(release) })
	var first []string
	for i := 0; i < readers; i++ {
		a := <-got
		if a.err != nil || len(a.v.Events) != 24 {
			t.Fatalf("view %d: %d events, err %v", i, len(a.v.Events), a.err)
		}
		if i == 0 {
			first = ids(a.v.Events)
		} else if fmt.Sprint(ids(a.v.Events)) != fmt.Sprint(first) {
			t.Fatalf("view %d answered other events", i)
		}
	}
	if listings.Load() != 1 || infos.Load() != int64(len(segs)) {
		t.Errorf("%d simultaneous first reads listed the ring %d times and statted %d entries (want 1 and %d)", readers, listings.Load(), infos.Load(), len(segs))
	}
	for path, n := range reads {
		if n != 1 {
			t.Errorf("%s read %d times by %d simultaneous first reads", path, n, readers)
		}
	}
}

// TestColdViewNeverSpansAnEviction pins that a cold read never answers
// segments read across a change of the ring's files: held after reading the
// oldest of two segments, while store-wide eviction persists the ring's
// recovery state and deletes both, the read answers what the evicted ring
// holds — not the older segment's events alone, which were never retained
// on their own and would read as uncut.
func TestColdViewNeverSpansAnEviction(t *testing.T) {
	root := t.TempDir()
	lim := smallLimits(64 * mib)
	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim })
	fill(t, s, "old", 1, 24) // two segments
	if err := s.Finalize(context.Background(), "old", "holder:1", 24); err != nil {
		t.Fatal(err)
	}
	flush(t, s)
	_ = s.Close()
	ringDir := filepath.Join(root, sessionDirName("old"))
	segs, _, err := listSegments(ringDir)
	if err != nil || len(segs) != 2 {
		t.Fatalf("fixture: %+v %v", segs, err)
	}
	second := filepath.Join(ringDir, segmentName(segs[1].seq))

	s2, _ := openTest(t, root, func(o *Options) { o.Limits = lim })
	orig := readFile
	t.Cleanup(func() { readFile = orig })
	entered, release := make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	var freed sync.Once
	t.Cleanup(func() { freed.Do(func() { close(release) }) }) // before s2 closes
	readFile = func(path string) ([]byte, error) {
		// The cold reader's read of the second segment is held; the
		// evicting writer's own read of it passes.
		if path == second && held.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return orig(path)
	}
	type answer struct {
		v   View
		err error
	}
	got := make(chan answer, 1)
	go func() {
		v, err := s2.View(context.Background(), "old")
		got <- answer{v, err}
	}()
	<-entered // the oldest segment is read; the second is not

	if _, err := s2.call(context.Background(), job{kind: jobBarrier, fn: func() {
		s2.mu.Lock()
		info := s2.sessions[sessionDirName("old")]
		s2.mu.Unlock()
		if err := s2.removeRing(info, false); err != nil {
			t.Errorf("evict: %v", err)
		}
	}}); err != nil {
		t.Fatal(err)
	}
	for _, seg := range segs {
		if _, err := os.Stat(filepath.Join(ringDir, segmentName(seg.seq))); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("fixture: segment %d survived the eviction: %v", seg.seq, err)
		}
	}
	freed.Do(func() { close(release) })
	a := <-got
	if a.err != nil {
		t.Fatal(a.err)
	}
	after := view(t, s2, "old")
	if fmt.Sprint(ids(a.v.Events)) != fmt.Sprint(ids(after.Events)) || a.v.Cut != after.Cut {
		t.Fatalf("cold view across the eviction = %d events (cut %v); the evicted ring holds %d (cut %v)",
			len(a.v.Events), a.v.Cut, len(after.Events), after.Cut)
	}
	if got := s2.LoadedRings(); got != 0 {
		t.Fatalf("cold eviction left %d loaded rings, want 0", got)
	}
	loaded(t, s2, "old")
	if rec := s2.Recover("old", "holder:1"); rec.Boundary != 24 {
		t.Fatalf("evicted ring lost its boundary: %+v", rec)
	}
}

// holdGap holds the first View to reach the gap hook (viewLookupGap or
// viewColdGap) there until the returned release runs (also at cleanup,
// registered before the store's).
func holdGap(t *testing.T, gap *func()) (entered <-chan struct{}, release func()) {
	t.Helper()
	in, out := make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	var freed sync.Once
	release = func() { freed.Do(func() { close(out) }) }
	orig := *gap
	t.Cleanup(func() { release(); *gap = orig })
	*gap = func() {
		if held.CompareAndSwap(false, true) {
			close(in)
			<-out
		}
	}
	return in, release
}

// TestColdViewNeverSpansAWarmTransition pins that a cold read decided on an
// unloaded ring never accepts files the ring wrote once loaded: held between
// finding the ring unloaded and reading it, then again after reading its
// oldest segment while the now-loaded ring appends past its cap (dropping
// that segment and growing its newest), the read answers the loaded ring's
// view — not the evicted prefix glued to the later records as uncut.
func TestColdViewNeverSpansAWarmTransition(t *testing.T) {
	root := t.TempDir()
	lim := smallLimits(64 * mib)
	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim })
	fill(t, s, "sess", 1, 60) // most of the 4 MiB cap, in four segments
	_ = s.Close()
	ringDir := filepath.Join(root, sessionDirName("sess"))
	segs, _, err := listSegments(ringDir)
	if err != nil || len(segs) < 3 || segs[0].seq != 1 {
		t.Fatalf("fixture: %+v %v", segs, err)
	}

	s2, _ := openTest(t, root, func(o *Options) { o.Limits = lim })
	gapEntered, gapRelease := holdGap(t, &viewColdGap)
	orig := readFile
	t.Cleanup(func() { readFile = orig })
	readEntered, readRelease := make(chan struct{}), make(chan struct{})
	var armed, held atomic.Bool
	var freed sync.Once
	t.Cleanup(func() { freed.Do(func() { close(readRelease) }) })
	second := filepath.Join(ringDir, segmentName(segs[1].seq))
	readFile = func(path string) ([]byte, error) {
		if armed.Load() && path == second && held.CompareAndSwap(false, true) {
			close(readEntered)
			<-readRelease
		}
		return orig(path)
	}
	type answer struct {
		v   View
		err error
	}
	got := make(chan answer, 1)
	go func() {
		v, err := s2.View(context.Background(), "sess")
		got <- answer{v, err}
	}()
	<-gapEntered // found unloaded, not yet read
	loaded(t, s2, "sess")
	armed.Store(true)
	gapRelease()
	// Each answer must be a view the loaded ring held: the one at the time
	// when the reader answers without reading the old files, else the one
	// after the appends it was held across.
	var a answer
	var want View
	select {
	case <-readEntered: // a read of the old files is under way
		fill(t, s2, "sess", 61, 10) // past the cap: the oldest segment goes
		if _, err := os.Stat(filepath.Join(ringDir, segmentName(segs[0].seq))); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("fixture: the oldest segment survived the appends: %v", err)
		}
		if _, err := os.Stat(second); err != nil {
			t.Fatalf("fixture: the held segment went too: %v", err)
		}
		freed.Do(func() { close(readRelease) })
		a = <-got
		want = view(t, s2, "sess")
	case a = <-got:
		want = view(t, s2, "sess")
	case <-time.After(2 * time.Minute):
		t.Fatal("the view neither answered nor read")
	}
	if a.err != nil {
		t.Fatal(a.err)
	}
	if fmt.Sprint(ids(a.v.Events)) != fmt.Sprint(ids(want.Events)) || a.v.Cut != want.Cut {
		first := uint64(0)
		if len(a.v.Events) > 0 {
			first = a.v.Events[0].Seq
		}
		t.Fatalf("view across the warm transition = %d events from seq %d (cut %v); the loaded ring holds %d (cut %v)",
			len(a.v.Events), first, a.v.Cut, len(want.Events), want.Cut)
	}
}

// TestColdViewNeverReadsAReplacementRingAsTheRetiredOne pins that a reader
// holding a session entry that retention retires, and a replacement entry
// recreates for the same directory, never reads the replacement's files as
// the retired ring's: held right after the lookup across the expiry and the
// replacement's appends, then (if it reads the files) after the replacement's
// oldest segment while appends past the cap drop it, the read answers the
// replacement's view — not its evicted prefix glued to later records as
// uncut.
func TestColdViewNeverReadsAReplacementRingAsTheRetiredOne(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	lim := smallLimits(64 * mib)
	clock := &fakeClock{now: time.Now()}
	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	fill(t, s, "sess", 1, 24) // the ring retention will retire, unloaded below
	_ = s.Close()
	ringDir := filepath.Join(root, sessionDirName("sess"))

	s2, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	gapEntered, gapRelease := holdGap(t, &viewLookupGap)
	orig := readFile
	t.Cleanup(func() { readFile = orig })
	readEntered, readRelease := make(chan struct{}), make(chan struct{})
	var armed, held atomic.Bool
	var freed sync.Once
	t.Cleanup(func() { freed.Do(func() { close(readRelease) }) })
	second := filepath.Join(ringDir, segmentName(2))
	readFile = func(path string) ([]byte, error) {
		if armed.Load() && path == second && held.CompareAndSwap(false, true) {
			close(readEntered)
			<-readRelease
		}
		return orig(path)
	}
	type answer struct {
		v   View
		err error
	}
	got := make(chan answer, 1)
	go func() {
		v, err := s2.View(ctx, "sess")
		got <- answer{v, err}
	}()
	<-gapEntered // holds the retiring entry
	s2.mu.Lock()
	retired := s2.sessions[sessionDirName("sess")]
	s2.mu.Unlock()
	clock.Advance(10 * 24 * time.Hour)
	if err := s2.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	fill(t, s2, "sess", 1, 60) // the replacement, most of the cap in four segments
	s2.mu.Lock()
	replacement := s2.sessions[sessionDirName("sess")]
	s2.mu.Unlock()
	if replacement == nil || replacement == retired || retired.view.Load() != nil {
		t.Fatalf("fixture: no replacement entry (retired %p, replacement %p)", retired, replacement)
	}
	armed.Store(true)
	gapRelease()

	// The answer must be a view the replacement held: the one at the time
	// when the reader answers without reading the files, else the one after
	// the appends it was held across.
	var a answer
	var want View
	select {
	case <-readEntered: // a read of the replacement's files is under way
		fill(t, s2, "sess", 61, 10) // past the cap: the oldest segment goes
		if _, err := os.Stat(filepath.Join(ringDir, segmentName(1))); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("fixture: the oldest segment survived the appends: %v", err)
		}
		if _, err := os.Stat(second); err != nil {
			t.Fatalf("fixture: the held segment went too: %v", err)
		}
		freed.Do(func() { close(readRelease) })
		a = <-got
		want = view(t, s2, "sess")
	case a = <-got:
		want = view(t, s2, "sess")
	case <-time.After(2 * time.Minute):
		t.Fatal("the view neither answered nor read")
	}
	if a.err != nil {
		t.Fatal(a.err)
	}
	if fmt.Sprint(ids(a.v.Events)) != fmt.Sprint(ids(want.Events)) || a.v.Cut != want.Cut {
		first := uint64(0)
		if len(a.v.Events) > 0 {
			first = a.v.Events[0].Seq
		}
		t.Fatalf("view across the expiry and recreation = %d events from seq %d (cut %v); the replacement holds %d (cut %v)",
			len(a.v.Events), first, a.v.Cut, len(want.Events), want.Cut)
	}
}

// TestColdViewOldGenerationNeverDisplacesANewerRead pins the coalescing of
// cold reads across generations: a reader held with the generation sampled
// before a writer change that leaves the ring unloaded, released while a
// reader of the new generation is replaying, joins that replay instead of
// replacing it — the ring is listed once and each segment read once.
func TestColdViewOldGenerationNeverDisplacesANewerRead(t *testing.T) {
	root := t.TempDir()
	lim := smallLimits(64 * mib)
	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim })
	fill(t, s, "cold", 1, 24) // two segments
	_ = s.Close()
	ringDir := filepath.Join(root, sessionDirName("cold"))
	segs, _, err := listSegments(ringDir)
	if err != nil || len(segs) < 2 {
		t.Fatalf("fixture: %+v %v", segs, err)
	}

	s2, _ := openTest(t, root, func(o *Options) { o.Limits = lim })
	gapEntered, gapRelease := holdGap(t, &viewColdGap)
	type answer struct {
		v   View
		err error
	}
	got := make(chan answer, 2)
	read := func() {
		v, err := s2.View(context.Background(), "cold")
		got <- answer{v, err}
	}
	go read() // the old-generation reader
	<-gapEntered
	var info *sessionInfo
	if _, err := s2.call(context.Background(), job{kind: jobBarrier, fn: func() {
		s2.mu.Lock()
		info = s2.sessions[sessionDirName("cold")]
		s2.mu.Unlock()
		info.mu.Lock()
		s2.trimRingLocked(info, math.MaxInt64) // a change that trims nothing
		info.mu.Unlock()
	}}); err != nil {
		t.Fatal(err)
	}
	if info.view.Load() != nil || info.gen.Load() == 0 {
		t.Fatalf("fixture: the ring is loaded or its generation did not advance")
	}

	listings, infos := countListings(t, ringDir)
	orig := readFile
	t.Cleanup(func() { readFile = orig })
	entered, release := make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	var freed sync.Once
	t.Cleanup(func() { freed.Do(func() { close(release) }) })
	var mu sync.Mutex
	reads := map[string]int{}
	readFile = func(path string) ([]byte, error) {
		if filepath.Dir(path) == ringDir {
			mu.Lock()
			reads[path]++
			mu.Unlock()
			if held.CompareAndSwap(false, true) {
				close(entered)
				<-release
			}
		}
		return orig(path)
	}
	go read() // the new-generation reader
	<-entered
	gapRelease()
	time.Sleep(200 * time.Millisecond) // the old-generation reader catches up
	freed.Do(func() { close(release) })
	for i := 0; i < 2; i++ {
		if a := <-got; a.err != nil || len(a.v.Events) != 24 {
			t.Fatalf("view %d: %d events, err %v", i, len(a.v.Events), a.err)
		}
	}
	if listings.Load() != 1 || infos.Load() != int64(len(segs)) {
		t.Errorf("two readers across a generation listed the ring %d times and statted %d entries (want 1 and %d)", listings.Load(), infos.Load(), len(segs))
	}
	for path, n := range reads {
		if n != 1 {
			t.Errorf("%s read %d times by two readers across a generation", path, n)
		}
	}
}

// countListings counts, from now until the test ends, the listings of dir
// and the metadata reads of the entries they return.
func countListings(t *testing.T, dir string) (listings, infos *atomic.Int64) {
	t.Helper()
	listings, infos = new(atomic.Int64), new(atomic.Int64)
	orig := readDir
	t.Cleanup(func() { readDir = orig })
	readDir = func(path string) ([]os.DirEntry, error) {
		entries, err := orig(path)
		if path != dir {
			return entries, err
		}
		listings.Add(1)
		for i, e := range entries {
			entries[i] = countedEntry{e, infos}
		}
		return entries, err
	}
	return listings, infos
}

type countedEntry struct {
	os.DirEntry
	infos *atomic.Int64
}

func (e countedEntry) Info() (fs.FileInfo, error) {
	e.infos.Add(1)
	return e.DirEntry.Info()
}

func ids(evs []wsaudit.Envelope) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.ID
	}
	return out
}
