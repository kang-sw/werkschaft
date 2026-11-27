package wsdashboard

import (
	"testing"
	"time"
)

// A session the daemon's runner owns is live from memory: its registration
// heartbeat ages past LiveWindow on disk and the index still reports it
// live, and neither the clock tick nor the 5-minute sweep reads it again.
func TestOwnedLiveSessionNeedsNoHeartbeatOrReread(t *testing.T) {
	dir := t.TempDir()
	project := t.TempDir()
	src := newFakeSource()
	now := testNow
	owned := map[string]bool{"ownedchild": true}
	idx := NewIndex(IndexOptions{
		Dir: dir, Source: src, Now: func() time.Time { return now },
		OwnedLive: func(id string) bool { return owned[id] },
	})
	register(t, dir, "ownedchild", project, now)
	register(t, dir, "coldexternal", project, now)
	idx.Refresh()
	reads := src.readCounts()

	// Hours later, no heartbeat was ever rewritten.
	now = now.Add(6 * time.Hour)
	idx.RefreshClock()
	idx.Settle()
	idx.Refresh()
	idx.Settle()
	s, ok := idx.Session("ownedchild")
	if !ok || !s.Live || s.Lifecycle == SessionPendingDeletion {
		t.Fatalf("owned session = %+v, %v, want live from memory", s, ok)
	}
	if got := src.readCounts()["ownedchild"]; got != reads["ownedchild"] {
		t.Fatalf("owned session re-read %d times across clock ticks and a sweep", got-reads["ownedchild"])
	}
	cold, _ := idx.Session("coldexternal")
	if cold.Live {
		t.Fatal("an external session with a stale heartbeat reads live")
	}
	// The stale external one is read once more by the clock (its liveness
	// changed), then the sweep skips it while unchanged.
	after := src.readCounts()["coldexternal"]
	idx.Refresh()
	idx.Settle()
	if got := src.readCounts()["coldexternal"]; got != after {
		t.Fatalf("a cold unchanged session was re-read by the sweep (%d -> %d)", after, got)
	}
	// Once the runner lets go of it, it ages out as any other.
	owned["ownedchild"] = false
	if s, _ := idx.Session("ownedchild"); s.Live {
		t.Fatal("a released session still reads live")
	}
}
