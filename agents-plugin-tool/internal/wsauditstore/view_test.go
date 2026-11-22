package wsauditstore

import (
	"context"
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
