package wsauditstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

// smallLimits gives 1 MiB segments in a 4 MiB per-session ring.
func smallLimits(total int64) Limits {
	return Limits{SessionBytes: 4 * mib, TotalBytes: total, BodyBytes: 64 * kib, Retention: 7 * 24 * time.Hour}
}

func fill(t *testing.T, s *Store, session string, from, n uint64) {
	t.Helper()
	body := strings.Repeat("x", 60*1024)
	for seq := from; seq < from+n; seq++ {
		ev := sourced(session, seq, wsaudit.KindToolResult)
		ev.Out = body
		mustAppend(t, s, ev)
		if seq%8 == 0 {
			flush(t, s)
		}
	}
	flush(t, s)
}

func diskBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasPrefix(info.Name(), "seg-") {
			n += info.Size()
		}
		return nil
	})
	return n
}

func segSeqs(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestSessionCapDropsOldestWholeSegments pins Decision 3: the per-session cap
// drops the oldest whole segments, keeps the newest, holds on disk, and reads
// see only retained events.
func TestSessionCapDropsOldestWholeSegments(t *testing.T) {
	root := t.TempDir()
	s, _ := openTest(t, root, func(o *Options) { o.Limits = smallLimits(64 * mib) })
	fill(t, s, "sess", 1, 150) // ~9 MiB through a 4 MiB ring
	dir := filepath.Join(root, "sess")
	if got := diskBytes(t, dir); got > 4*mib {
		t.Fatalf("ring holds %d bytes on disk, cap %d", got, 4*mib)
	}
	segs := segSeqs(t, dir)
	if segs[0] == segmentName(1) || len(segs) < 2 {
		t.Fatalf("oldest segment kept or too few left: %v", segs)
	}
	evs := events(t, s, "sess")
	seqs := sourceSeqs(evs)
	if seqs[len(seqs)-1] != 150 || seqs[0] == 1 {
		t.Fatalf("retained %d..%d", seqs[0], seqs[len(seqs)-1])
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1]+1 {
			t.Fatal("eviction left a hole inside the retained range")
		}
	}
	_ = s.Close()
	s2, _ := openTest(t, root, func(o *Options) { o.Limits = smallLimits(64 * mib) })
	if got := sourceSeqs(events(t, s2, "sess")); len(got) != len(seqs) || got[0] != seqs[0] {
		t.Fatalf("restart recovered %d events from %d", len(got), got[0])
	}
	// Replaying an evicted record does not resurrect it.
	mustAppend(t, s2, sourced("sess", 1, wsaudit.KindToolCall))
	flush(t, s2)
	if got := sourceSeqs(events(t, s2, "sess")); got[0] == 1 {
		t.Fatal("an evicted record was appended again")
	}
}

// TestTotalCapEvictsWholeRingsLeastRecentlyUsed pins Decision 3: the store
// total evicts whole rings, least recently seen first, measured on disk.
func TestTotalCapEvictsWholeRingsLeastRecentlyUsed(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Now()}
	s, log := openTest(t, root, func(o *Options) { o.Limits = smallLimits(8 * mib); o.Now = clock.Now })
	fill(t, s, "old", 1, 50) // ~3 MiB each
	clock.Advance(time.Hour)
	fill(t, s, "mid", 1, 50)
	clock.Advance(time.Hour)
	fill(t, s, "new", 1, 50)
	if _, err := os.Stat(filepath.Join(root, "old")); !os.IsNotExist(err) {
		t.Fatalf("least recently used ring survived: %v\n%s", err, log.String())
	}
	if _, err := os.Stat(filepath.Join(root, "mid")); err != nil {
		t.Fatal("a more recent ring was evicted first")
	}
	if got := diskBytes(t, root); got > 8*mib {
		t.Fatalf("store holds %d bytes on disk, cap %d", got, 8*mib)
	}
	if evs := events(t, s, "old"); len(evs) != 0 {
		t.Fatalf("reads still see %d evicted events", len(evs))
	}
	if !strings.Contains(log.String(), "store total cap: evict audit ring of session old") {
		t.Fatalf("eviction not logged: %s", log.String())
	}
}

// TestTotalCapEvictsActiveRingLastKeepingNewest pins (2127): store-wide
// eviction removes the session being written last, and that session keeps
// its newest segment.
func TestTotalCapEvictsActiveRingLastKeepingNewest(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Now()}
	lim := Limits{SessionBytes: 64 * mib, TotalBytes: 4 * mib, BodyBytes: 64 * kib, Retention: 7 * 24 * time.Hour}
	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	fill(t, s, "other", 1, 10)
	clock.Advance(-time.Hour) // the active session looks older than the other
	fill(t, s, "active", 1, 120)
	if _, err := os.Stat(filepath.Join(root, "other")); !os.IsNotExist(err) {
		t.Fatal("the idle ring outlived the active one's need for room")
	}
	dir := filepath.Join(root, "active")
	if got := diskBytes(t, root); got > 4*mib {
		t.Fatalf("store holds %d bytes", got)
	}
	evs := sourceSeqs(events(t, s, "active"))
	if len(evs) == 0 || evs[len(evs)-1] != 120 {
		t.Fatalf("active ring lost its newest records: %v", evs)
	}
	if segs := segSeqs(t, dir); len(segs) == 0 {
		t.Fatal("active ring lost its newest segment")
	}
}

// TestRetentionExpiresIdleRingsAndLongerKnobKeepsThem pins Decision 6.
func TestRetentionExpiresIdleRingsAndLongerKnobKeepsThem(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name      string
		retention time.Duration
		kept      bool
	}{{"default seven days", 7 * 24 * time.Hour, false}, {"lengthened", 30 * 24 * time.Hour, true}} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			clock := &fakeClock{now: time.Now()}
			lim := DefaultLimits()
			lim.Retention = c.retention
			s, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
			mustAppend(t, s, sourced("idle", 1, wsaudit.KindToolCall))
			flush(t, s)
			clock.Advance(10 * 24 * time.Hour)
			if err := s.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(filepath.Join(root, "idle"))
			if kept := err == nil; kept != c.kept {
				t.Fatalf("kept = %v after 10 idle days", kept)
			}
		})
	}
}

// TestDeleteSessionRemovesWholeRing pins Decision 8.
func TestDeleteSessionRemovesWholeRing(t *testing.T) {
	root := t.TempDir()
	s, _ := openTest(t, root, func(o *Options) { o.Limits = smallLimits(64 * mib) })
	fill(t, s, "gone", 1, 40)
	mustAppend(t, s, sourced("stays", 1, wsaudit.KindToolCall))
	flush(t, s)
	if err := s.DeleteSession(context.Background(), "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "gone")); !os.IsNotExist(err) {
		t.Fatal("deleted session's ring is still on disk")
	}
	if len(events(t, s, "gone")) != 0 || len(events(t, s, "stays")) != 1 {
		t.Fatal("deletion touched the wrong ring")
	}
}

// ageRing backdates a ring's segment files, as if written at t.
func ageRing(t *testing.T, dir string, at time.Time) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(dir, e.Name()), at, at); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLifecycleLastSeenSurvivesRegistryPruneAndRestart pins Decision 6: a
// last-seen time recorded at a lifecycle point (here the registry's last
// copy as it drops the session) ages the ring after the registry is gone and
// the daemon restarted.
func TestLifecycleLastSeenSurvivesRegistryPruneAndRestart(t *testing.T) {
	root := t.TempDir()
	t0 := time.Now().Add(-20 * 24 * time.Hour)
	clock := &fakeClock{now: t0}
	s, _ := openTest(t, root, func(o *Options) { o.Now = clock.Now })
	mustAppend(t, s, sourced("sess", 1, wsaudit.KindToolCall))
	flush(t, s)
	s.Touch("sess", t0.Add(5*24*time.Hour)) // ended five days after its last event
	flush(t, s)
	_ = s.Close()
	ageRing(t, filepath.Join(root, "sess"), t0)

	clock.now = t0.Add(11 * 24 * time.Hour) // six days after the recorded end
	s2, _ := openTest(t, root, func(o *Options) { o.Now = clock.Now })
	if len(events(t, s2, "sess")) != 1 {
		t.Fatal("ring removed before the retention after its recorded last-seen")
	}
	_ = s2.Close()
	clock.now = t0.Add(13 * 24 * time.Hour)
	s3, _ := openTest(t, root, func(o *Options) { o.Now = clock.Now })
	_ = s3
	if _, err := os.Stat(filepath.Join(root, "sess")); !os.IsNotExist(err) {
		t.Fatal("ring outlived the retention after its recorded last-seen")
	}
}

// TestQuietHeartbeatingSessionKeepsRingAcrossRestart pins Decision 6: a
// hosted session that heartbeats without captured events keeps its ring
// across a restart until the retention after its last heartbeat.
func TestQuietHeartbeatingSessionKeepsRingAcrossRestart(t *testing.T) {
	root := t.TempDir()
	t0 := time.Now().Add(-30 * 24 * time.Hour)
	clock := &fakeClock{now: t0}
	heartbeat := t0
	lastSeen := func(session string) (time.Time, bool) { return heartbeat, session == "quiet" }
	s, _ := openTest(t, root, func(o *Options) { o.Now = clock.Now; o.LastSeen = lastSeen })
	mustAppend(t, s, sourced("quiet", 1, wsaudit.KindToolCall))
	flush(t, s)
	_ = s.Close()
	ageRing(t, filepath.Join(root, "quiet"), t0)

	// Eight days of heartbeats, no events; the daemon restarts on day 8.
	heartbeat = t0.Add(8 * 24 * time.Hour)
	clock.now = heartbeat
	s2, _ := openTest(t, root, func(o *Options) { o.Now = clock.Now; o.LastSeen = lastSeen })
	if len(events(t, s2, "quiet")) != 1 {
		t.Fatal("a heartbeating session lost its ring at restart")
	}
	_ = s2.Close()
	// The registry is gone now (pruned); the recorded heartbeat still ages it.
	clock.now = heartbeat.Add(6 * 24 * time.Hour)
	s3, _ := openTest(t, root, func(o *Options) { o.Now = clock.Now })
	if len(events(t, s3, "quiet")) != 1 {
		t.Fatal("the recorded heartbeat did not survive the restart")
	}
	clock.now = heartbeat.Add(8 * 24 * time.Hour)
	if err := s3.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "quiet")); !os.IsNotExist(err) {
		t.Fatal("ring outlived the retention after the last heartbeat")
	}
}

// TestLimitsValidateAndClamp pins (2129)=a (3)-(4): a breaking knob set is
// refused, and a loaded one is clamped to the nearest valid values with a
// warning per change.
func TestLimitsValidateAndClamp(t *testing.T) {
	if err := DefaultLimits().Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	for name, values := range map[string]map[string]string{
		"session below floor": {KeySessionCapMiB: "3"},
		"total below floor":   {KeyTotalCapMiB: "2"},
		"body over 1/16":      {KeyTotalCapMiB: "8", KeyBodyCapKiB: "1024"},
		"body below floor":    {KeyBodyCapKiB: "1"},
		"not a number":        {KeyRetentionDays: "soon"},
	} {
		if err := ValidateValues(values); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := ValidateValues(map[string]string{KeyTotalCapMiB: "8", KeyBodyCapKiB: "512", KeyRetentionDays: "30"}); err != nil {
		t.Fatalf("valid set refused: %v", err)
	}
	bad, _ := LimitsFromValues(map[string]string{KeySessionCapMiB: "1", KeyBodyCapKiB: "4096", KeyRetentionDays: "0"})
	got, warnings := bad.Clamp()
	if err := got.Validate(); err != nil {
		t.Fatalf("clamped set invalid: %v", err)
	}
	if got.SessionBytes != 4*mib || got.BodyBytes != 256*kib || got.Retention != MinRetention || len(warnings) != 3 {
		t.Fatalf("clamped to %+v with %v", got, warnings)
	}
	if again, w := got.Clamp(); again != got || len(w) != 0 {
		t.Fatal("clamping a valid set changed it")
	}
	// Open clamps and logs.
	s, log := openTest(t, t.TempDir(), func(o *Options) { o.Limits = bad })
	if s.Limits() != got || !strings.Contains(log.String(), KeyBodyCapKiB+" lowered") {
		t.Fatalf("Open used %+v, log %q", s.Limits(), log.String())
	}
}
