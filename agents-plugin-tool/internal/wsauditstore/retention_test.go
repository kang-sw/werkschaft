package wsauditstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	seqs := sourceSeqs(t, s, "sess")
	if seqs[len(seqs)-1] != 150 || seqs[0] == 1 {
		t.Fatalf("retained %d..%d", seqs[0], seqs[len(seqs)-1])
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1]+1 {
			t.Fatal("eviction left a hole inside the retained range")
		}
	}
	// Capture finalizes what it mapped.
	if err := s.Finalize(context.Background(), "sess", "holder:1", 150); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s2, _ := openTest(t, root, func(o *Options) { o.Limits = smallLimits(64 * mib) })
	if got := sourceSeqs(t, s2, "sess"); len(got) != len(seqs) || got[0] != seqs[0] {
		t.Fatalf("restart recovered %d events from %d", len(got), got[0])
	}
	// Replaying an evicted record does not resurrect it.
	mustAppend(t, s2, sourced("sess", 1, wsaudit.KindToolCall))
	flush(t, s2)
	if got := sourceSeqs(t, s2, "sess"); got[0] == 1 {
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
	// Its data goes whole; its recovery state stays as one header line.
	if segs := segSeqs(t, filepath.Join(root, "old")); len(segs) != 1 || diskBytes(t, filepath.Join(root, "old")) > 4096 {
		t.Fatalf("least recently used ring survived: %v\n%s", segs, log.String())
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
	// The idle ring's data is evicted; only its recovery state stays, as a
	// header-only segment.
	if evs := events(t, s, "other"); len(evs) != 0 {
		t.Fatalf("the idle ring outlived the active one's need for room: %d events", len(evs))
	}
	dir := filepath.Join(root, "active")
	if got := diskBytes(t, root); got > 4*mib {
		t.Fatalf("store holds %d bytes", got)
	}
	evs := sourceSeqs(t, s, "active")
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
	// A day count past the time.Duration range saturates instead of wrapping
	// into a negative period that clamping would turn into one day.
	for _, days := range []string{"106751", "106752", "1048576", "4000000000000000000"} {
		if err := ValidateValues(map[string]string{KeyRetentionDays: days}); err != nil {
			t.Fatalf("retention %s days refused: %v", days, err)
		}
		l, warnings := LimitsFromValues(map[string]string{KeyRetentionDays: days})
		if l.Retention != time.Duration(MaxRetentionDays)*24*time.Hour || len(warnings) != 0 {
			t.Fatalf("retention %s days = %v %v", days, l.Retention, warnings)
		}
		if c, w := l.Clamp(); c.Retention != l.Retention || len(w) != 0 {
			t.Fatalf("retention %s days clamped to %v", days, c.Retention)
		}
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

func storeTotal(s *Store) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

// TestFailedEvictionsReportAndKeepTheirAccounting pins eviction against a
// deletion the filesystem refuses (a Windows sharing violation): the failure
// is logged, the segment or ring stays accounted, and the store total still
// equals the bytes on disk.
func TestFailedEvictionsReportAndKeepTheirAccounting(t *testing.T) {
	origFile, origAll := removeFile, removeAll
	t.Cleanup(func() { removeFile, removeAll = origFile, origAll })
	refuse := errors.New("sharing violation")
	removeFile = func(path string) error {
		if filepath.Base(path) == segmentName(1) {
			return refuse
		}
		return origFile(path)
	}
	lim := Limits{SessionBytes: 4 * mib, TotalBytes: 64 * mib, BodyBytes: 256 * kib, Retention: MinRetention}
	root := t.TempDir()
	s, log := openTest(t, root, func(o *Options) { o.Limits = lim })
	for seq := uint64(1); seq <= 12; seq++ {
		mustAppend(t, s, bigRecord("sess", seq, lim))
	}
	flush(t, s)
	if _, err := os.Stat(filepath.Join(root, "sess", segmentName(1))); err != nil {
		t.Fatalf("the refused segment is gone: %v", err)
	}
	if got := events(t, s, "sess"); len(got) == 0 || got[0].Seq != 1 {
		t.Fatal("the index dropped the events of a segment still on disk")
	}
	if !strings.Contains(log.String(), "delete segment 1") || !strings.Contains(log.String(), "sharing violation") {
		t.Fatalf("failed segment deletion not logged: %s", log.String())
	}
	if disk, total := diskBytes(t, root), storeTotal(s); disk != total {
		t.Fatalf("store total %d, disk %d", total, disk)
	}
	_ = s.Close()

	removeFile = origFile
	root2 := t.TempDir()
	lim2 := Limits{SessionBytes: 4 * mib, TotalBytes: 6 * mib, BodyBytes: 256 * kib, Retention: MinRetention}
	clock := &fakeClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	s2, log2 := openTest(t, root2, func(o *Options) { o.Limits = lim2; o.Now = clock.Now })
	for seq := uint64(1); seq <= 6; seq++ {
		mustAppend(t, s2, bigRecord("old", seq, lim2))
	}
	flush(t, s2)
	// The whole ring's data refuses deletion.
	removeFile = func(path string) error {
		if filepath.Base(filepath.Dir(path)) == "old" {
			return refuse
		}
		return origFile(path)
	}
	clock.Advance(time.Hour)
	for seq := uint64(1); seq <= 6; seq++ {
		mustAppend(t, s2, bigRecord("new", seq, lim2))
	}
	flush(t, s2)
	if !strings.Contains(log2.String(), "evict audit ring of session old: delete segment 1") {
		t.Fatalf("failed ring eviction not logged: %s", log2.String())
	}
	if disk, total := diskBytes(t, root2), storeTotal(s2); disk != total {
		t.Fatalf("store total %d, disk %d", total, disk)
	}
}

// writeNewerRing writes an unopened ring of a newer format with a padded
// segment of about size bytes.
func writeNewerRing(t *testing.T, root, session string, size int, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(root, session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	seg := filepath.Join(dir, segmentName(1))
	content := `{"t":"h","v":2,"session":"` + session + `"}` + "\n" + `{"t":"future","pad":"` + strings.Repeat("z", size) + `"}` + "\n"
	if err := os.WriteFile(seg, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(seg, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return seg
}

// TestNewerFormatRingIsNeverAgedOutOrEvicted pins forward safety (T0): an
// unopened ring a newer binary wrote survives the retention sweep past its
// age and store-wide eviction, with a log line each time.
func TestNewerFormatRingIsNeverAgedOutOrEvicted(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}
	seg := writeNewerRing(t, root, "future", 3*int(mib), clock.now.Add(-30*24*time.Hour))
	if headerVersion([]byte(`{"t":"h","v":2,"sess`)) != 2 || headerVersion([]byte(`{"v":2,"t":"h","sess`)) != 2 ||
		headerVersion([]byte(`{"t":"e","e":{"v":9}}`)) != 0 {
		t.Fatal("headerVersion misreads a torn header or an event line")
	}
	if !headNewer([]byte(`{"v":3,"t":"h"}`+"\n")) || !headNewer([]byte(`{"t":"h","v":"two"}`+"\n")) ||
		headNewer([]byte(`{"t":"h","v":1}`+"\n")) || headNewer([]byte(`{"t":"e","v":9}`+"\n")) || headNewer([]byte(`{"t":"e","v`)) {
		t.Fatal("headNewer misclassifies a first line")
	}
	lim := Limits{SessionBytes: 4 * mib, TotalBytes: 6 * mib, BodyBytes: 256 * kib, Retention: MinRetention}
	s, log := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(seg); err != nil || !strings.Contains(log.String(), "retention: keep audit ring of session future") {
		t.Fatalf("the newer ring was aged out: %v; log: %s", err, log.String())
	}
	_ = s.Close()
	// A fresh store meets the ring first as an eviction candidate.
	s2, log2 := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now; o.DeferFirstSweep = true })
	for seq := uint64(1); seq <= 8; seq++ {
		mustAppend(t, s2, bigRecord("now", seq, lim))
	}
	flush(t, s2)
	if _, err := os.Stat(seg); err != nil || !strings.Contains(log2.String(), "store total cap: keep audit ring of session future") {
		t.Fatalf("the newer ring was evicted: %v; log: %s", err, log2.String())
	}
}

// TestActiveRingTrimsItselfBeforeEvictingOthers pins the eviction order: a
// write first drops the active ring's own oldest segments under its
// per-session cap, and only then evicts other rings for the store total, so
// an idle ring is not evicted for room the active ring's own trim frees.
func TestActiveRingTrimsItselfBeforeEvictingOthers(t *testing.T) {
	lim := Limits{SessionBytes: 4 * mib, TotalBytes: 5*mib + 256*kib, BodyBytes: 256 * kib, Retention: MinRetention}
	clock := &fakeClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	s, log := openTest(t, t.TempDir(), func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	for seq := uint64(1); seq <= 3; seq++ {
		mustAppend(t, s, bigRecord("idle", seq, lim))
	}
	flush(t, s)
	clock.Advance(time.Hour)
	for seq := uint64(1); seq <= 24; seq++ {
		mustAppend(t, s, bigRecord("busy", seq, lim))
	}
	flush(t, s)
	if got := len(events(t, s, "idle")); got != 3 || strings.Contains(log.String(), "evict audit ring of session idle") {
		t.Fatalf("idle ring evicted though the busy ring's own trim made room: %d events\n%s", got, log.String())
	}
}

// TestOpenEnforcesLoweredCaps pins cap enforcement for rings already on
// disk: reopening with a lower per-session cap trims an unloaded ring to it
// (newest segment kept, events still readable), and a lower store total
// evicts the least recently seen ring.
func TestOpenEnforcesLoweredCaps(t *testing.T) {
	root := t.TempDir()
	wide := Limits{SessionBytes: 16 * mib, TotalBytes: 64 * mib, BodyBytes: 256 * kib, Retention: 30 * 24 * time.Hour}
	clock := &fakeClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	s, _ := openTest(t, root, func(o *Options) { o.Limits = wide; o.Now = clock.Now })
	for seq := uint64(1); seq <= 12; seq++ {
		mustAppend(t, s, bigRecord("big", seq, wide))
	}
	flush(t, s)
	clock.Advance(time.Hour)
	for seq := uint64(1); seq <= 6; seq++ {
		mustAppend(t, s, bigRecord("recent", seq, wide))
	}
	flush(t, s)
	_ = s.Close()
	if got := diskBytes(t, filepath.Join(root, "big")); got <= 4*mib {
		t.Fatalf("setup: big ring holds only %d bytes", got)
	}

	narrow := wide
	narrow.SessionBytes = 4 * mib
	s2, log := openTest(t, root, func(o *Options) { o.Limits = narrow; o.Now = clock.Now; o.DeferFirstSweep = true })
	if got := diskBytes(t, filepath.Join(root, "big")); got > 4*mib {
		t.Fatalf("lowered session cap not enforced at open: %d bytes\n%s", got, log.String())
	}
	if seqs := sourceSeqs(t, s2, "big"); len(seqs) == 0 || seqs[len(seqs)-1] != 12 {
		t.Fatalf("trimmed ring lost its newest records: %v", seqs)
	}
	_ = s2.Close()

	tight := narrow
	tight.TotalBytes = 4 * mib
	s3, log3 := openTest(t, root, func(o *Options) { o.Limits = tight; o.Now = clock.Now })
	if got := diskBytes(t, root); got > 4*mib {
		t.Fatalf("lowered store total not enforced at open: %d bytes\n%s", got, log3.String())
	}
	if got := len(events(t, s3, "big")); got != 0 {
		t.Fatalf("least recently seen ring kept %d events", got)
	}
	if got := len(events(t, s3, "recent")); got == 0 {
		t.Fatal("the most recent ring was evicted whole")
	}
}

// TestEvictedUnloadedRingKeepsItsFinalizedState pins that store-wide
// eviction of a ring not loaded in this run keeps its recovery state durably:
// after another restart its boundary still refuses a late replay.
func TestEvictedUnloadedRingKeepsItsFinalizedState(t *testing.T) {
	root := t.TempDir()
	lim := Limits{SessionBytes: 4 * mib, TotalBytes: 4 * mib, BodyBytes: 256 * kib, Retention: 30 * 24 * time.Hour}
	clock := &fakeClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	ctx := context.Background()
	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	mustAppend(t, s, bigRecord("old", 1, lim))
	mustAppend(t, s, bigRecord("old", 4, lim))
	if err := s.Finalize(ctx, "old", "holder:1", 4); err != nil {
		t.Fatal(err)
	}
	flush(t, s)
	_ = s.Close()

	clock.Advance(time.Hour)
	s2, log := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	for seq := uint64(1); seq <= 8; seq++ { // "old" is never loaded in this run
		mustAppend(t, s2, bigRecord("new", seq, lim))
	}
	flush(t, s2)
	if !strings.Contains(log.String(), "evict audit ring of session old") {
		t.Fatalf("old ring not evicted: %s", log.String())
	}
	_ = s2.Close()

	s3, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	loaded(t, s3, "old")
	if rec := s3.Recover("old", "holder:1"); rec.Boundary != 4 || rec.MaxSeq != 4 {
		t.Fatalf("recovery after eviction and restart = %+v", rec)
	}
	mustAppend(t, s3, sourced("old", 2, wsaudit.KindToolCall)) // late replay
	flush(t, s3)
	if got := len(events(t, s3, "old")); got != 0 || lossCount(t, s3, "old") != 1 {
		t.Fatalf("late replay after eviction: %d events, loss %d", got, lossCount(t, s3, "old"))
	}
}

// TestSweepRecordsHeartbeatsBeforeAnUnwitnessedPrune pins last-seen
// durability across a registry prune the daemon never saw: the periodic
// sweep records a quiet session's newer heartbeat while it is registered, so
// after the registry drops it and the daemon restarts, the ring still ages
// from that heartbeat.
func TestSweepRecordsHeartbeatsBeforeAnUnwitnessedPrune(t *testing.T) {
	root := t.TempDir()
	t0 := time.Now()
	clock := &fakeClock{now: t0}
	heartbeat, registered := t0, true
	lastSeen := func(session string) (time.Time, bool) { return heartbeat, registered && session == "quiet" }
	lim := DefaultLimits()
	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now; o.LastSeen = lastSeen })
	mustAppend(t, s, sourced("quiet", 1, wsaudit.KindToolCall))
	flush(t, s)
	heartbeat = t0.Add(5 * 24 * time.Hour) // heartbeats only, no events
	clock.now = heartbeat
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	registered = false // pruned by a ws-mcp process; no OnRemove reaches the store
	_ = s.Close()
	clock.now = t0.Add(10 * 24 * time.Hour)
	s2, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now; o.LastSeen = lastSeen })
	if len(events(t, s2, "quiet")) != 1 {
		t.Fatal("the ring aged from its last event, not its last recorded heartbeat")
	}
	clock.now = t0.Add(13 * 24 * time.Hour)
	if err := s2.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "quiet")); !os.IsNotExist(err) {
		t.Fatal("ring outlived the retention after the last recorded heartbeat")
	}
}

// TestIndexRecoveryKeepsOlderFilesUntilASnapshotLands pins the index's
// two-file ring: an interrupted rotation (newest file empty) recovers the
// older file's entries and replaces both with a fresh snapshot, and a
// snapshot write that fails leaves the old file and no torn new one.
func TestIndexRecoveryKeepsOlderFilesUntilASnapshotLands(t *testing.T) {
	root := t.TempDir()
	seen := formatTime(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	good := `{"dir":"a","session":"a","seen":"` + seen + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(root, indexName(1)), []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, indexName(2)), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log, rec, err := openIndex(root)
	if err != nil || rec["a"].Seen != seen {
		t.Fatalf("recovered %+v %v", rec, err)
	}
	log.close()
	if names := segSeqs(t, root); fmt.Sprint(names) != fmt.Sprint([]string{indexName(3)}) {
		t.Fatalf("index files after recovery: %v", names)
	}
	log2, rec2, err := openIndex(root)
	if err != nil || rec2["a"].Seen != seen {
		t.Fatalf("snapshot lost the entry: %+v %v", rec2, err)
	}

	orig := writeFile
	t.Cleanup(func() { writeFile = orig })
	writeFile = func(f *os.File, b []byte) (int, error) {
		if bytes.Contains(b, []byte(`"snap"`)) {
			n, _ := orig(f, b[:len(b)/2])
			return n, errors.New("disk full")
		}
		return orig(f, b)
	}
	if err := log2.rotate([]indexEntry{rec2["a"]}); err == nil {
		t.Fatal("rotation reported success")
	}
	log2.close()
	if names := segSeqs(t, root); fmt.Sprint(names) != fmt.Sprint([]string{indexName(3)}) {
		t.Fatalf("a failed snapshot left %v", names)
	}
}

// TestPeriodicSweepRunsUnderASteadyStreamOfAppends pins that retention is not
// starved by work: while a producer keeps the write queue from ever running
// dry, a sweep that comes due still runs between batches and ages out an
// expired ring.
func TestPeriodicSweepRunsUnderASteadyStreamOfAppends(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "expired")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	seg := filepath.Join(old, segmentName(1))
	if err := os.WriteFile(seg, []byte(`{"t":"h","v":1,"session":"expired"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ageRing(t, old, time.Now().Add(-30*24*time.Hour))
	// Slow writes keep the producer ahead of the writer.
	orig := writeFile
	t.Cleanup(func() { writeFile = orig })
	writeFile = func(f *os.File, b []byte) (int, error) {
		time.Sleep(time.Millisecond)
		return orig(f, b)
	}
	s, log := openTest(t, root, func(o *Options) {
		o.DeferFirstSweep = true
		o.SweepInterval = 20 * time.Millisecond
		o.QueueDepth = 64
	})
	stop := make(chan struct{})
	producing := make(chan struct{})
	go func() {
		defer close(producing)
		for seq := uint64(1); ; seq++ {
			select {
			case <-stop:
				return
			default:
			}
			it := sourced("busy", seq, wsaudit.KindToolCall)
			_ = s.AppendSourced(context.Background(), it.id, it.rec(), it.Envelope)
		}
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(old); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			close(stop)
			<-producing
			t.Fatalf("no sweep ran while appends kept coming\n%s", log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	<-producing
}

// writeRingWithHeader writes an unopened one-segment ring whose first line is
// header, followed by one padding line.
func writeRingWithHeader(t *testing.T, root, session, header string, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(root, session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	seg := filepath.Join(dir, segmentName(1))
	content := header + "\n" + `{"t":"future","pad":"zz"}` + "\n"
	if err := os.WriteFile(seg, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(seg, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return seg
}

// TestColdNewerFormatGuardFailsClosed pins the cold forward-safety guard: a
// newer header is recognized in any key order and whatever shape its other
// fields take, a ring whose headers cannot be read is not removed by an
// automatic sweep, and a newer header this binary cannot decode still makes
// a loaded ring refuse writes.
func TestColdNewerFormatGuardFailsClosed(t *testing.T) {
	root := t.TempDir()
	clock := &fakeClock{now: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}
	expired := clock.now.Add(-30 * 24 * time.Hour)
	reordered := writeRingWithHeader(t, root, "reordered", `{"v":2,"t":"h","session":"reordered"}`, expired)
	reshaped := writeRingWithHeader(t, root, "reshaped", `{"t":"h","v":2,"state":"a newer shape"}`, expired)
	unreadable := writeRingWithHeader(t, root, "unreadable", `{"t":"h","v":1,"session":"unreadable"}`, expired)
	orig := readHead
	t.Cleanup(func() { readHead = orig })
	readHead = func(path string, n int) ([]byte, error) {
		if strings.Contains(path, "unreadable") {
			return nil, errors.New("i/o error")
		}
		return orig(path, n)
	}
	s, log := openTest(t, root, func(o *Options) { o.Now = clock.Now; o.DeferFirstSweep = true })
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, seg := range []string{reordered, reshaped, unreadable} {
		if _, err := os.Stat(seg); err != nil {
			t.Fatalf("%s was aged out: %v\n%s", seg, err, log.String())
		}
	}
	if !strings.Contains(log.String(), "cannot read its segment headers") {
		t.Fatalf("unreadable headers not logged: %s", log.String())
	}
	// Loaded, the undecodable newer header still refuses writes.
	mustAppend(t, s, sourced("reshaped", 1, wsaudit.KindToolCall))
	flush(t, s)
	raw, err := os.ReadFile(reshaped)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"t":"e"`) {
		t.Fatalf("a record was written into a newer-format ring:\n%s", raw)
	}
	// Once the headers read, the ring ages out as usual.
	readHead = orig
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unreadable); !os.IsNotExist(err) {
		t.Fatalf("readable expired ring kept: %v", err)
	}
}

// TestLimitsClampAndValidateAgreeAboveSixteenMiBSegments pins that every
// knob set Clamp returns passes Validate, including sets whose maximal record
// outgrows the 16 MiB preferred segment (256 MiB per session, 6 MiB bodies).
func TestLimitsClampAndValidateAgreeAboveSixteenMiBSegments(t *testing.T) {
	big := Limits{SessionBytes: 256 * mib, TotalBytes: 4096 * mib, BodyBytes: 6 * 1024 * kib, Retention: 7 * 24 * time.Hour}
	if c, w := big.Clamp(); c != big || len(w) != 0 {
		t.Fatalf("clamped %+v to %+v %v", big, c, w)
	}
	if err := big.Validate(); err != nil {
		t.Fatalf("a set Clamp keeps is refused: %v", err)
	}
	for _, session := range []int64{4, 5, 64, 256, 1024, 65536} {
		for _, body := range []int64{4, 64, 1024, 6 * 1024, 16 * 1024, 1 << 20} {
			l, _ := Limits{SessionBytes: session * mib, TotalBytes: 1 << 40, BodyBytes: body * kib, Retention: MinRetention}.Clamp()
			if err := l.Validate(); err != nil {
				t.Fatalf("Clamp returned %+v, which Validate refuses: %v", l, err)
			}
			if l.MaxRecordBytes()+HeaderBound > l.SegmentBytes() {
				t.Fatalf("%+v: segment %d leaves no room for a header beside a record", l, l.SegmentBytes())
			}
		}
	}
}

// TestHeaderAndControlLinesStayWithinTheirBounds pins the segment bound
// against control traffic: a multi-megabyte loss reason is cut, thousands of
// keyed lost identities and long source names stay within the header's
// budgets (the stalest sources dropped, the latest kept), and no segment or
// header outgrows its bound.
func TestHeaderAndControlLinesStayWithinTheirBounds(t *testing.T) {
	root := t.TempDir()
	lim := smallLimits(64 * mib)
	s, log := openTest(t, root, func(o *Options) { o.Limits = lim })
	ctx := context.Background()
	pad := strings.Repeat("s", 200)
	if err := s.RecordLoss(ctx, "sess", wsaudit.SourceID{Source: "holder:1", Seq: 1}, strings.Repeat("r", 5*int(mib))); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2000; i++ {
		id := wsaudit.SourceID{Source: "daemon", Key: fmt.Sprintf("%s-%d", pad, i)}
		if err := s.RecordLoss(ctx, "sess", id, wsaudit.LossDropped); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 1000; i++ {
		if err := s.Finalize(ctx, "sess", fmt.Sprintf("%s-%d", pad, i), 5); err != nil {
			t.Fatal(err)
		}
	}
	flush(t, s)
	fill(t, s, "sess", 2, 40) // rotate a few times
	dir := filepath.Join(root, "sess")
	segs, _, err := listSegments(dir)
	if err != nil || len(segs) < 2 {
		t.Fatalf("segments %+v %v", segs, err)
	}
	for _, seg := range segs {
		if seg.size > lim.SegmentBytes() {
			t.Fatalf("segment %d holds %d bytes, bound %d", seg.seq, seg.size, lim.SegmentBytes())
		}
		raw, err := os.ReadFile(filepath.Join(dir, segmentName(seg.seq)))
		if err != nil {
			t.Fatal(err)
		}
		first := raw
		if nl := bytes.IndexByte(raw, '\n'); nl >= 0 {
			first = raw[:nl]
		}
		if bytes.HasPrefix(first, []byte(`{"t":"h"`)) && len(first) > int(HeaderBound) {
			t.Fatalf("segment %d header is %d bytes", seg.seq, len(first))
		}
		if bytes.Contains(raw, []byte(strings.Repeat("r", maxReasonBytes+1))) {
			t.Fatal("a loss reason was stored past its bound")
		}
	}
	if rec := s.Recover("sess", fmt.Sprintf("%s-%d", pad, 999)); rec.Boundary != 5 {
		t.Fatalf("the latest finalized source was dropped: %+v", rec)
	}
	for _, want := range []string{"sources dropped", "lost identities forgotten"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("log lacks %q: %s", want, log.String())
		}
	}
}

// finalizedOldRing writes ring "old" with records 1 and 4 of holder:1
// finalized through 4, and returns its directory.
func finalizedOldRing(t *testing.T, root string, lim Limits, clock *fakeClock) string {
	t.Helper()
	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	mustAppend(t, s, bigRecord("old", 1, lim))
	mustAppend(t, s, bigRecord("old", 4, lim))
	if err := s.Finalize(context.Background(), "old", "holder:1", 4); err != nil {
		t.Fatal(err)
	}
	flush(t, s)
	_ = s.Close()
	return filepath.Join(root, "old")
}

// evictOld fills ring "new" in a fresh store until "old" (never loaded) is
// evicted for the store total.
func evictOld(t *testing.T, root string, lim Limits, clock *fakeClock) {
	t.Helper()
	clock.Advance(time.Hour)
	s, log := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	for seq := uint64(1); seq <= 8; seq++ {
		mustAppend(t, s, bigRecord("new", seq, lim))
	}
	flush(t, s)
	if !strings.Contains(log.String(), "evict audit ring of session old") {
		t.Fatalf("old ring not evicted: %s", log.String())
	}
	_ = s.Close()
}

// assertOldBoundaryHolds reopens the store and checks ring "old" still
// refuses a late replay of holder:1.
func assertOldBoundaryHolds(t *testing.T, root string, lim Limits, clock *fakeClock) {
	t.Helper()
	s, _ := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	loaded(t, s, "old")
	if rec := s.Recover("old", "holder:1"); rec.Boundary != 4 || rec.MaxSeq != 4 {
		t.Fatalf("recovery after eviction and restart = %+v", rec)
	}
	mustAppend(t, s, sourced("old", 2, wsaudit.KindToolCall)) // late replay
	flush(t, s)
	for _, seq := range sourceSeqs(t, s, "old") {
		if seq == 2 {
			t.Fatal("a late replay after eviction was appended")
		}
	}
}

// TestColdEvictionSkipsATornNewestHeader pins cold eviction of a ring whose
// newest segment holds only a torn header (a crash right after rotation): its
// state is recovered from the newest complete header and segments after it,
// not from the torn one, and persisted before the old segments go.
func TestColdEvictionSkipsATornNewestHeader(t *testing.T) {
	root := t.TempDir()
	lim := Limits{SessionBytes: 4 * mib, TotalBytes: 4 * mib, BodyBytes: 256 * kib, Retention: 30 * 24 * time.Hour}
	clock := &fakeClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	dir := finalizedOldRing(t, root, lim, clock)
	segs, _, err := listSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	torn := `{"t":"h","v":1,"session":"old","state":{"max_seq":4,"boundar`
	if err := os.WriteFile(filepath.Join(dir, segmentName(segs[len(segs)-1].seq+1)), []byte(torn), 0o600); err != nil {
		t.Fatal(err)
	}
	evictOld(t, root, lim, clock)
	assertOldBoundaryHolds(t, root, lim, clock)
}

// TestColdEvictionPersistsStateBeforeDeleting pins the eviction order: the
// replacement header lands before any segment is deleted, so a crash right
// after the first deletion (simulated: it deletes, then the run stops) still
// leaves the boundary on disk.
func TestColdEvictionPersistsStateBeforeDeleting(t *testing.T) {
	root := t.TempDir()
	lim := Limits{SessionBytes: 4 * mib, TotalBytes: 4 * mib, BodyBytes: 256 * kib, Retention: 30 * 24 * time.Hour}
	clock := &fakeClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	finalizedOldRing(t, root, lim, clock)
	origFile, origAll := removeFile, removeAll
	t.Cleanup(func() { removeFile, removeAll = origFile, origAll })
	crashed := errors.New("crashed")
	removeFile = func(path string) error {
		if filepath.Base(filepath.Dir(path)) == "old" {
			_ = origFile(path)
			return crashed
		}
		return origFile(path)
	}
	removeAll = func(path string) error {
		if filepath.Base(path) == "old" {
			_ = origAll(path)
			return crashed
		}
		return origAll(path)
	}
	evictOld(t, root, lim, clock)
	removeFile, removeAll = origFile, origAll
	assertOldBoundaryHolds(t, root, lim, clock)
}

// TestControlOnlyRingsAreEvictableAndTombstonesGoLast pins store-wide
// eviction of a loaded ring that holds only control lines (losses,
// boundaries) and no events: it is evicted like any other ring, leaving a
// bounded tombstone that is not evicted again, and a tombstone itself goes —
// recovery state and all — once trimming the active ring cannot make room.
func TestControlOnlyRingsAreEvictableAndTombstonesGoLast(t *testing.T) {
	root := t.TempDir()
	lim := Limits{SessionBytes: 4 * mib, TotalBytes: 4 * mib, BodyBytes: 256 * kib, Retention: 30 * 24 * time.Hour}
	clock := &fakeClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	s, log := openTest(t, root, func(o *Options) { o.Limits = lim; o.Now = clock.Now })
	ctx := context.Background()
	reason := strings.Repeat("q", 250)
	for seq := uint64(1); seq <= 5000; seq++ {
		if err := s.RecordLoss(ctx, "ctl", wsaudit.SourceID{Source: "holder:1", Seq: seq}, reason); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Finalize(ctx, "ctl", "holder:1", 5000); err != nil {
		t.Fatal(err)
	}
	flush(t, s)
	ctl := filepath.Join(root, "ctl")
	// Control lines land when the writer idles.
	for deadline := time.Now().Add(10 * time.Second); diskBytes(t, ctl) < mib; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("setup: control-only ring holds only %d bytes", diskBytes(t, ctl))
		}
	}
	clock.Advance(time.Hour)
	for seq := uint64(1); seq <= 16; seq++ {
		mustAppend(t, s, bigRecord("busy", seq, lim))
	}
	flush(t, s)
	if got := diskBytes(t, ctl); got > HeaderBound {
		t.Fatalf("control-only ring was never evicted: %d bytes\n%s", got, log.String())
	}
	if n := strings.Count(log.String(), "evict audit ring of session ctl"); n != 1 {
		t.Fatalf("ctl evicted %d times\n%s", n, log.String())
	}
	if got := diskBytes(t, root); got > lim.TotalBytes {
		t.Fatalf("store holds %d bytes, cap %d", got, lim.TotalBytes)
	}
	if rec := s.Recover("ctl", "holder:1"); rec.Boundary != 5000 {
		t.Fatalf("the tombstone lost its boundary: %+v", rec)
	}
	// Room no trim of the active ring can make takes the tombstone.
	if _, err := s.call(ctx, job{kind: jobBarrier, fn: func() {
		s.mu.Lock()
		busy := s.sessions["busy"]
		s.mu.Unlock()
		busy.mu.Lock()
		s.makeRoom(busy, lim.TotalBytes)
		busy.mu.Unlock()
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ctl); !os.IsNotExist(err) {
		t.Fatalf("the tombstone survived the last resort: %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "forget the recovery state of session ctl") {
		t.Fatalf("tombstone removal not logged: %s", log.String())
	}
}
