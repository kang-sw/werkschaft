package wsauditstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

func isRingLoaded(t *testing.T, s *Store, session string) bool {
	t.Helper()
	dir := sessionDirName(wsaudit.NormalizeID(session))
	s.mu.Lock()
	info := s.sessions[dir]
	var inMap bool
	if info != nil {
		_, inMap = s.resident[info]
	}
	s.mu.Unlock()
	if info == nil {
		return false
	}
	return inMap && info.view.Load() != nil && info.led.Load() != nil
}

func TestIdleTTLUnloadsIdleUnpinnedRingsOnly(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)}
	s, _ := openTest(t, root, func(o *Options) {
		o.Now = clock.Now
		o.IdleCheckInterval = -1
	})
	ctx := context.Background()

	mustAppend(t, s, sourced("s1", 1, wsaudit.KindToolCall))
	mustAppend(t, s, sourced("s2", 1, wsaudit.KindToolCall))
	flush(t, s)
	if got := s.LoadedRings(); got != 2 {
		t.Fatalf("LoadedRings = %d, want 2", got)
	}

	// Advance 10m and refresh s2 via Recover; advance another 6m so s1 is 16m
	// idle (>= 15m DefaultIdleTTL) while s2 is 6m idle (< 15m).
	clock.Advance(10 * time.Minute)
	_ = s.Recover("s2", "holder:1")
	clock.Advance(6 * time.Minute)
	if err := s.UnloadIdle(ctx); err != nil {
		t.Fatal(err)
	}

	if got := s.LoadedRings(); got != 1 {
		t.Fatalf("LoadedRings after TTL = %d, want 1", got)
	}
	if isRingLoaded(t, s, "s1") {
		t.Fatal("s1 remained loaded past 15m idle TTL")
	}
	if !isRingLoaded(t, s, "s2") {
		t.Fatal("s2 was unloaded despite recent Recover activity")
	}

	// Idle unload must delete no data on disk; cold View answers identically
	// without loading s1 back into memory.
	v, err := s.View(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Events) != 1 || v.Events[0].ID != "ev-1" {
		t.Fatalf("cold View of unloaded s1 = %+v, want ev-1", v.Events)
	}
	if isRingLoaded(t, s, "s1") {
		t.Fatal("cold View reloaded s1")
	}
}

func TestIdlePeriodicTickerUnloadsIdleRings(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)}
	s, _ := openTest(t, root, func(o *Options) {
		o.Now = clock.Now
		o.IdleTTL = 15 * time.Minute
		o.IdleCheckInterval = 15 * time.Millisecond
	})

	mustAppend(t, s, sourced("tick-s1", 1, wsaudit.KindToolCall))
	flush(t, s)
	if got := s.LoadedRings(); got != 1 {
		t.Fatalf("LoadedRings = %d, want 1", got)
	}

	clock.Advance(16 * time.Minute)
	deadline := time.Now().Add(5 * time.Second)
	for s.LoadedRings() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("periodic idle ticker did not unload ring; LoadedRings = %d", s.LoadedRings())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestIdleLRUCeilingEvictsOldestUnpinnedWithinTTL(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)}
	// Default MaxLoadedRings is 64 and DefaultIdleTTL is 15m.
	s, _ := openTest(t, root, func(o *Options) {
		o.Now = clock.Now
		o.IdleCheckInterval = -1
	})

	const total = DefaultMaxLoadedRings + 6
	for i := 0; i < total; i++ {
		clock.Advance(time.Second) // all within ~70s, well under 15m TTL
		mustAppend(t, s, sourced(fmt.Sprintf("s%03d", i), 1, wsaudit.KindToolCall))
		flush(t, s)
	}
	if got := s.LoadedRings(); got != DefaultMaxLoadedRings {
		t.Fatalf("LoadedRings = %d, want %d", got, DefaultMaxLoadedRings)
	}
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("s%03d", i)
		if isRingLoaded(t, s, name) {
			t.Fatalf("oldest unpinned ring %s was not evicted by 64-ring LRU ceiling", name)
		}
	}
	for i := 6; i < total; i++ {
		name := fmt.Sprintf("s%03d", i)
		if !isRingLoaded(t, s, name) {
			t.Fatalf("recent ring %s was unexpectedly evicted", name)
		}
	}
}

func TestIdlePinnedRingsSkipEvictionAndDrainOnUnpin(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)}
	s, _ := openTest(t, root, func(o *Options) {
		o.Now = clock.Now
		o.MaxLoadedRings = 4
		o.IdleTTL = 15 * time.Minute
		o.IdleCheckInterval = -1
	})
	ctx := context.Background()

	// Pin 6 rings (>= MaxLoadedRings): all 6 stay loaded even past TTL.
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("p%d", i)
		s.PinCapture(name)
		clock.Advance(time.Second)
		mustAppend(t, s, sourced(name, 1, wsaudit.KindToolCall))
	}
	flush(t, s)
	if got := s.LoadedRings(); got != 6 {
		t.Fatalf("LoadedRings with 6 pinned = %d, want 6", got)
	}

	// Refcount test: double-pin p5, then unpin once -> still pinned.
	s.PinCapture("p5")
	s.UnpinCapture("p5")

	// Unpinned rings touched while >= MaxLoadedRings are pinned drain to 0.
	for _, u := range []string{"u0", "u1"} {
		clock.Advance(time.Second)
		mustAppend(t, s, sourced(u, 1, wsaudit.KindToolCall))
	}
	flush(t, s)
	if got := s.LoadedRings(); got != 6 {
		t.Fatalf("LoadedRings with 6 pinned + 2 unpinned = %d, want 6 (unpinned drained to 0)", got)
	}
	for _, u := range []string{"u0", "u1"} {
		if isRingLoaded(t, s, u) {
			t.Fatalf("unpinned ring %s stayed loaded while >= MaxLoadedRings were pinned", u)
		}
	}

	// Advance past TTL: pinned rings still stay loaded.
	clock.Advance(30 * time.Minute)
	if err := s.UnloadIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadedRings(); got != 6 {
		t.Fatalf("pinned rings were unloaded past TTL: LoadedRings = %d, want 6", got)
	}

	// Detach p0..p3: total loaded rings automatically drain back down to cap (4).
	for i := 0; i < 4; i++ {
		clock.Advance(time.Second)
		s.UnpinCapture(fmt.Sprintf("p%d", i))
	}
	flush(t, s)
	if got := s.LoadedRings(); got != 4 {
		t.Fatalf("LoadedRings after unpinning 4 of 6 = %d, want 4", got)
	}
	if !isRingLoaded(t, s, "p4") || !isRingLoaded(t, s, "p5") {
		t.Fatal("still-pinned rings p4/p5 were evicted")
	}
	if isRingLoaded(t, s, "p0") || isRingLoaded(t, s, "p1") {
		t.Fatal("oldest unpinned rings p0/p1 were not evicted first")
	}

	// Unpinning p5's final pin allows TTL to unload it once idle.
	s.UnpinCapture("p5")
	clock.Advance(20 * time.Minute)
	if err := s.UnloadIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadedRings(); got != 1 || !isRingLoaded(t, s, "p4") {
		t.Fatalf("expected only still-pinned p4 loaded; LoadedRings=%d, p4=%v", got, isRingLoaded(t, s, "p4"))
	}
}

func TestIdleLastActiveRefreshedWithoutMutatingLastSeen(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: t0}
	lim := DefaultLimits()
	lim.Retention = 7 * 24 * time.Hour
	s, _ := openTest(t, root, func(o *Options) {
		o.Limits = lim
		o.Now = clock.Now
		o.IdleTTL = 15 * time.Minute
		o.IdleCheckInterval = -1
	})
	ctx := context.Background()

	sessions := []string{"rec", "ret", "comp", "pin", "unpin", "app"}
	for _, name := range sessions {
		mustAppend(t, s, sourced(name, 1, wsaudit.KindToolCall))
	}
	flush(t, s)

	// Record initial persisted lastSeen for every ring.
	readLastSeen := func(name string) time.Time {
		var got time.Time
		if _, err := s.call(ctx, job{kind: jobBarrier, fn: func() {
			s.mu.Lock()
			if info := s.sessions[sessionDirName(name)]; info != nil {
				got = info.lastSeen
			}
			s.mu.Unlock()
		}}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, name := range sessions {
		if got := readLastSeen(name); !got.Equal(t0) {
			t.Fatalf("%s initial lastSeen = %v, want %v", name, got, t0)
		}
	}

	// Advance 10m and exercise each activity trigger.
	clock.Advance(10 * time.Minute)
	_ = s.Recover("rec", "holder:1")
	_ = s.Retained("ret", wsaudit.SourceID{Source: "holder:1", Seq: 1})
	if _, _, err := s.RecoverCompactionBaseline(ctx, "comp", "holder:1"); err != nil {
		t.Fatal(err)
	}
	s.PinCapture("pin")
	s.PinCapture("unpin")
	clock.Advance(time.Minute)
	s.UnpinCapture("unpin")
	// Duplicate AppendSourced refreshes lastActive without mutating lastSeen.
	_ = s.AppendSourced(ctx, wsaudit.SourceID{Source: "holder:1", Seq: 1}, wsaudit.SourceID{Source: "holder:1", Seq: 1}, sourced("app", 1, wsaudit.KindToolCall).Envelope)
	flush(t, s)

	// None of these idle activity refreshes may mutate info.lastSeen.
	for _, name := range sessions {
		if got := readLastSeen(name); !got.Equal(t0) {
			t.Fatalf("%s lastSeen mutated by idle activity: got %v, want %v", name, got, t0)
		}
	}

	// Advance 6m (total 17m since t0, 6-7m since each activity): all 6 stay loaded.
	clock.Advance(6 * time.Minute)
	if err := s.UnloadIdle(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range sessions {
		if !isRingLoaded(t, s, name) {
			t.Fatalf("%s was unloaded despite recent idle activity", name)
		}
	}

	// Advance past 7-day retention while keeping lastActive fresh: retention
	// sweep MUST still expire the ring because info.lastSeen stayed at t0.
	s.UnpinCapture("pin")
	clock.Advance(7 * 24 * time.Hour)
	_ = s.Recover("rec", "holder:1")
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, sessionDirName("rec"))); !os.IsNotExist(err) {
		t.Fatalf("rec survived 7-day retention sweep despite unchanged lastSeen: %v", err)
	}
}

func TestIdleUnloadFlushesPendingLinesAndAbortsOnFailure(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)}
	s, _ := openTest(t, root, func(o *Options) {
		o.Now = clock.Now
		o.IdleTTL = 15 * time.Minute
		o.IdleCheckInterval = -1
	})
	ctx := context.Background()

	mustAppend(t, s, sourced("sess", 1, wsaudit.KindToolCall))
	flush(t, s)

	origWrite := writeFile
	t.Cleanup(func() { writeFile = origWrite })

	// Inject a write failure while queueing a boundary + loss, flush so the
	// control lines stay buffered in r.pendingLines() at t0, then advance past
	// IdleTTL and attempt idle unload: unload MUST abort and keep the ring
	// loaded and dirty.
	var failWrites atomic.Bool
	var writeFailures atomic.Int32
	failWrites.Store(true)
	writeFile = func(f *os.File, b []byte) (int, error) {
		if failWrites.Load() {
			writeFailures.Add(1)
			return 0, errors.New("injected disk full")
		}
		return origWrite(f, b)
	}

	if err := s.Finalize(ctx, "sess", "holder:1", 5); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLoss(ctx, "sess", wsaudit.SourceID{Source: "holder:1", Seq: 2}, wsaudit.LossAppendFailed); err != nil {
		t.Fatal(err)
	}
	flush(t, s)

	beforeUnloadFails := writeFailures.Load()
	clock.Advance(20 * time.Minute)
	if err := s.UnloadIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if writeFailures.Load() <= beforeUnloadFails {
		t.Fatal("UnloadIdle did not attempt to flush r.pendingLines()")
	}
	if !isRingLoaded(t, s, "sess") {
		t.Fatal("ring unloaded despite pendingLines flush failure")
	}

	// Restore writes; the next UnloadIdle flushes pendingLines and unloads.
	failWrites.Store(false)
	if err := s.UnloadIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if isRingLoaded(t, s, "sess") {
		t.Fatal("ring did not unload after pendingLines flush succeeded")
	}

	// Reopen a fresh store from disk to prove pendingLines landed before unload.
	_ = s.Close()
	s2, _ := openTest(t, root, func(o *Options) {
		o.Now = clock.Now
		o.IdleCheckInterval = -1
	})
	loaded(t, s2, "sess")
	if rec := s2.Recover("sess", "holder:1"); rec.Boundary != 5 || rec.MaxSeq != 1 {
		t.Fatalf("boundary lost across pendingLines flush + unload: %+v", rec)
	}
	if got := lossCount(t, s2, "sess"); got != 1 {
		t.Fatalf("loss count = %d, want 1", got)
	}
}

func TestReopenAfterIdleUnloadUsesOpenAppendWithoutSpuriousRotation(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)}
	s, _ := openTest(t, root, func(o *Options) {
		o.Now = clock.Now
		o.IdleTTL = 15 * time.Minute
		o.IdleCheckInterval = -1
	})
	ctx := context.Background()

	for seq := uint64(1); seq <= 3; seq++ {
		mustAppend(t, s, sourced("sess", seq, wsaudit.KindToolCall))
	}
	flush(t, s)
	ringDir := filepath.Join(root, sessionDirName("sess"))
	before, _, err := listSegments(ringDir)
	if err != nil || len(before) != 1 {
		t.Fatalf("before unload segments = %+v, err = %v", before, err)
	}

	// Idle unload closes the handle and drops the ring.
	clock.Advance(20 * time.Minute)
	if err := s.UnloadIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if isRingLoaded(t, s, "sess") {
		t.Fatal("sess not unloaded")
	}

	// Subsequent append reloads the ring and appends to seg-1 via openAppend.
	mustAppend(t, s, sourced("sess", 4, wsaudit.KindToolCall))
	flush(t, s)
	afterUnload, _, err := listSegments(ringDir)
	if err != nil || len(afterUnload) != 1 || afterUnload[0].seq != before[0].seq {
		t.Fatalf("reopen after idle unload rotated spuriously: %+v, err = %v", afterUnload, err)
	}

	// Also exercise ring_write.go's defensive reopen when r.file == nil on a
	// loaded ring without forceRotate.
	if _, err := s.call(ctx, job{kind: jobBarrier, fn: func() {
		s.mu.Lock()
		info := s.sessions[sessionDirName("sess")]
		s.mu.Unlock()
		info.mu.Lock()
		info.ring.close()
		info.mu.Unlock()
	}}); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, s, sourced("sess", 5, wsaudit.KindToolCall))
	flush(t, s)
	afterClose, _, err := listSegments(ringDir)
	if err != nil || len(afterClose) != 1 || afterClose[0].seq != before[0].seq {
		t.Fatalf("defensive reopen rotated spuriously: %+v, err = %v", afterClose, err)
	}
	evs := events(t, s, "sess")
	if len(evs) != 5 {
		t.Fatalf("events after reopen = %d, want 5", len(evs))
	}
}

func TestColdTombstoneRemoveRingLeavesNoOpenHandle(t *testing.T) {
	root := t.TempDir()
	lim := Limits{SessionBytes: 4 * mib, TotalBytes: 4 * mib, BodyBytes: 256 * kib, Retention: 30 * 24 * time.Hour}
	clock := &fakeClock{now: time.Now().UTC().Add(time.Hour)}
	ctx := context.Background()

	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now; o.IdleCheckInterval = -1 })
	for seq := uint64(1); seq <= 4; seq++ {
		mustAppend(t, s, sourced("old", seq, wsaudit.KindToolCall))
	}
	if err := s.Finalize(ctx, "old", "holder:1", 4); err != nil {
		t.Fatal(err)
	}
	s.Touch("old", clock.Now())
	flush(t, s)
	_ = s.Close()

	clock.Advance(time.Hour)
	s2, logBuf := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now; o.IdleCheckInterval = -1 })
	if got := s2.LoadedRings(); got != 0 {
		t.Fatalf("reopened store loaded %d rings, want 0", got)
	}

	// Add a newer session so enforceCaps selects "old" as the cold LRU victim
	// and routes through makeRoom -> removeRing(old, false) without spending
	// time redacting megabytes of test payloads.
	mustAppend(t, s2, sourced("new", 1, wsaudit.KindToolCall))
	s2.Touch("new", clock.Now())
	flush(t, s2)
	if _, err := s2.call(ctx, job{kind: jobBarrier, fn: func() {
		s2.mu.Lock()
		newer := s2.sessions[sessionDirName("new")]
		s2.total += lim.TotalBytes - newer.bytes
		newer.bytes = lim.TotalBytes
		s2.mu.Unlock()
		s2.enforceCaps()
	}}); err != nil {
		t.Fatal(err)
	}
	if isRingLoaded(t, s2, "old") {
		t.Fatalf("cold-tombstone eviction left old ring loaded\n%s", logBuf.String())
	}
	if got := s2.LoadedRings(); got != 1 {
		t.Fatalf("LoadedRings = %d, want 1 (only new)\n%s", got, logBuf.String())
	}

	// Verify old is marked tombstone and holds no open handle or ring pointer.
	var tombstone, hasRing bool
	if _, err := s2.call(ctx, job{kind: jobBarrier, fn: func() {
		s2.mu.Lock()
		info := s2.sessions[sessionDirName("old")]
		tombstone = info != nil && info.tombstone
		hasRing = info != nil && info.ring != nil
		s2.mu.Unlock()
	}}); err != nil {
		t.Fatal(err)
	}
	if !tombstone || hasRing {
		t.Fatalf("old tombstone=%v hasRing=%v, want true/false\n%s", tombstone, hasRing, logBuf.String())
	}

	// Reloading old recovers its persisted tombstone boundary.
	loaded(t, s2, "old")
	if rec := s2.Recover("old", "holder:1"); rec.Boundary != 4 || rec.MaxSeq != 4 {
		t.Fatalf("reloaded cold tombstone recovery = %+v, want boundary=4 maxSeq=4", rec)
	}
}

func TestLedgerRetainedPreservedAfterIdleUnloadAndReload(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)}
	s, _ := openTest(t, root, func(o *Options) {
		o.Now = clock.Now
		o.IdleTTL = 15 * time.Minute
		o.IdleCheckInterval = -1
	})
	ctx := context.Background()

	// Retain seq 1 and seq 3 (skip seq 2), then finalize at 3.
	mustAppend(t, s, sourced("sess", 1, wsaudit.KindToolCall))
	mustAppend(t, s, sourced("sess", 3, wsaudit.KindToolCall))
	if err := s.Finalize(ctx, "sess", "holder:1", 3); err != nil {
		t.Fatal(err)
	}
	flush(t, s)

	// Unload the idle ring completely.
	clock.Advance(20 * time.Minute)
	if err := s.UnloadIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if isRingLoaded(t, s, "sess") {
		t.Fatal("sess did not unload")
	}

	// Replay seq 1 (retained -> duplicate, NOT loss) and seq 2 (not retained,
	// covered by boundary 3 -> loss, NOT appended), plus fresh seq 4.
	mustAppend(t, s, sourced("sess", 1, wsaudit.KindToolCall))
	mustAppend(t, s, sourced("sess", 2, wsaudit.KindToolCall))
	mustAppend(t, s, sourced("sess", 4, wsaudit.KindToolCall))
	flush(t, s)

	evs := events(t, s, "sess")
	if got := ids(evs); fmt.Sprint(got) != "[ev-1 ev-3 ev-4]" {
		t.Fatalf("events after unload+replay = %v, want [ev-1 ev-3 ev-4]", got)
	}
	if got := lossCount(t, s, "sess"); got != 1 {
		t.Fatalf("lossCount = %d, want 1 (only seq 2, not retained seq 1)", got)
	}
}

func TestConcurrentViewDuringIdleUnloadViaChanging(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)}
	s, _ := openTest(t, root, func(o *Options) {
		o.Now = clock.Now
		o.IdleTTL = 15 * time.Minute
		o.IdleCheckInterval = -1
	})
	ctx := context.Background()

	for seq := uint64(1); seq <= 5; seq++ {
		mustAppend(t, s, sourced("sess", seq, wsaudit.KindToolCall))
	}
	flush(t, s)

	// Hold a View between sampling info/gen and checking info.view.Load(),
	// unload the ring while View is paused there, then release View.
	entered, release := holdGap(t, &viewLookupGap)
	type viewAns struct {
		v   View
		err error
	}
	got := make(chan viewAns, 1)
	go func() {
		v, err := s.View(ctx, "sess")
		got <- viewAns{v, err}
	}()
	<-entered

	clock.Advance(20 * time.Minute)
	if err := s.UnloadIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if isRingLoaded(t, s, "sess") {
		t.Fatal("sess not unloaded")
	}
	release()

	ans := <-got
	if ans.err != nil {
		t.Fatal(ans.err)
	}
	if len(ans.v.Events) != 5 {
		t.Fatalf("View across unload returned %d events, want 5", len(ans.v.Events))
	}

	// Concurrent race stress: writers, idle unloads, View, Recover, Retained,
	// and RecoverCompactionBaseline running concurrently under -race.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v, err := s.View(ctx, "sess")
				if err != nil {
					t.Errorf("concurrent View: %v", err)
					return
				}
				if len(v.Events) < 5 {
					t.Errorf("concurrent View lost events: got %d", len(v.Events))
					return
				}
				_ = s.Recover("sess", "holder:1")
				_ = s.Retained("sess", wsaudit.SourceID{Source: "holder:1", Seq: 1})
				if _, _, err := s.RecoverCompactionBaseline(ctx, "sess", "holder:1"); err != nil {
					t.Errorf("RecoverCompactionBaseline: %v", err)
					return
				}
			}
		}(g)
	}
	for i := uint64(6); i <= 25; i++ {
		mustAppend(t, s, sourced("sess", i, wsaudit.KindToolCall))
		flush(t, s)
		clock.Advance(20 * time.Minute)
		if err := s.UnloadIdle(ctx); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}
