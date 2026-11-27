package wsdashboard

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// A live session that crosses into stale is re-read once; afterwards the
// clock tick leaves the cold, unchanged session alone.
func TestRefreshClockRereadsStaleTransitionOnce(t *testing.T) {
	dir, project := t.TempDir(), t.TempDir()
	src := newFakeSource()
	now := testNow
	register(t, dir, "coldone", project, now)
	idx, _ := newTestIndex(t, dir, src, &now)
	idx.Refresh()
	idx.Settle()
	base := src.readCounts()["coldone"]

	now = now.Add(LiveWindow + time.Minute)
	idx.RefreshClock()
	idx.Settle()
	if s, _ := idx.Session("coldone"); s.Live {
		t.Fatalf("session still live after window: %+v", s)
	}
	after := src.readCounts()["coldone"]
	if after != base+1 {
		t.Fatalf("transition reads = %d, want 1", after-base)
	}
	for range 5 {
		now = now.Add(30 * time.Second)
		idx.RefreshClock()
		idx.Settle()
	}
	if got := src.readCounts()["coldone"]; got != after {
		t.Fatalf("cold stale session re-read %d times by clock ticks, want 0", got-after)
	}
}

type slowSource struct {
	*fakeSource
	mu       sync.Mutex
	cur, max int
}

func (s *slowSource) ReadSession(reg Registration) (SessionData, bool, error) {
	s.mu.Lock()
	s.cur++
	s.max = max(s.max, s.cur)
	s.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	s.mu.Lock()
	s.cur--
	s.mu.Unlock()
	return s.fakeSource.ReadSession(reg)
}

func TestIndexBoundsConcurrentReads(t *testing.T) {
	dir, project := t.TempDir(), t.TempDir()
	src := &slowSource{fakeSource: newFakeSource()}
	now := testNow
	const n = 60
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("bounded%02d", i)
		register(t, dir, ids[i], project, now)
	}
	idx, _ := newTestIndex(t, dir, src, &now)
	for _, id := range ids {
		idx.Reread(id)
	}
	done := make(chan struct{})
	go func() { idx.Settle(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("bounded rereads deadlocked")
	}
	src.mu.Lock()
	peak := src.max
	src.mu.Unlock()
	if peak > maxConcurrentReads {
		t.Fatalf("peak concurrent reads = %d, cap %d", peak, maxConcurrentReads)
	}
	for _, id := range ids {
		if _, ok := idx.Session(id); !ok {
			t.Fatalf("session %s was not indexed", id)
		}
	}
}
