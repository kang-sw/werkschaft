package wsauditstore

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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
	again := view(t, s2, "cold")
	reads.Range(func(path, n any) bool {
		if *n.(*int) != 1 {
			t.Errorf("%s read %d times by two cold views", path, *n.(*int))
		}
		return true
	})
	loaded(t, s2, "cold")
	warm := view(t, s2, "cold")
	if fmt.Sprint(ids(again.Events)) != fmt.Sprint(ids(warm.Events)) || again.Cut != warm.Cut {
		t.Fatalf("cold view %v (cut %v) differs from the loaded ring's %v (cut %v)", ids(again.Events), again.Cut, ids(warm.Events), warm.Cut)
	}
}

func ids(evs []wsaudit.Envelope) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.ID
	}
	return out
}
