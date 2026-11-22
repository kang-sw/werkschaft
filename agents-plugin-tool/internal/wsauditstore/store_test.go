package wsauditstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsstate"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func openTest(t *testing.T, root string, mutate func(*Options)) (*Store, *syncBuffer) {
	t.Helper()
	log := &syncBuffer{}
	opts := Options{Root: root, Log: log, SweepInterval: -1}
	if mutate != nil {
		mutate(&opts)
	}
	s, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, log
}

func flush(t *testing.T, s *Store) {
	t.Helper()
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func sourced(session string, seq uint64, kind wsaudit.Kind) wsaudit.Envelope {
	ev, _ := wsaudit.NewEnvelope(session, "claude", time.Now(), kind)
	ev.ID = fmt.Sprintf("ev-%d", seq)
	ev.Seq = seq
	ev.Source = &wsaudit.SourceID{Source: "holder:1", Seq: seq}
	ev.Out = fmt.Sprintf("output %d", seq)
	return ev
}

func mustAppend(t *testing.T, s *Store, ev wsaudit.Envelope) {
	t.Helper()
	if err := s.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
}

func events(t *testing.T, s *Store, session string) []wsaudit.Envelope {
	t.Helper()
	evs, err := s.Events(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func sourceSeqs(evs []wsaudit.Envelope) []uint64 {
	var out []uint64
	for _, ev := range evs {
		if ev.Source != nil {
			out = append(out, ev.Source.Seq)
		}
	}
	return out
}

// TestStoreRestartReplayDedupsAndContinuesSequence pins Decision 5: append,
// restart, replay the holder's records, and append again — sequences stay
// continuous and no retained source identity repeats.
func TestStoreRestartReplayDedupsAndContinuesSequence(t *testing.T) {
	root := t.TempDir()
	s, _ := openTest(t, root, nil)
	for seq := uint64(1); seq <= 5; seq++ {
		mustAppend(t, s, sourced("sess", seq, wsaudit.KindToolCall))
	}
	flush(t, s)
	_ = s.Close()

	s2, _ := openTest(t, root, nil)
	rec, err := s2.Recover(context.Background(), "sess")
	if err != nil {
		t.Fatal(err)
	}
	if rec.MaxSeq != 5 || rec.Sources["holder:1"].Last.Seq != 5 || rec.Sources["holder:1"].Boundary != 5 {
		t.Fatalf("recovery = %+v", rec)
	}
	for seq := uint64(1); seq <= 5; seq++ { // holder reattachment replays
		mustAppend(t, s2, sourced("sess", seq, wsaudit.KindToolCall))
	}
	for seq := uint64(6); seq <= 7; seq++ {
		mustAppend(t, s2, sourced("sess", rec.MaxSeq+seq-5, wsaudit.KindToolCall))
	}
	flush(t, s2)
	evs := events(t, s2, "sess")
	if got := fmt.Sprint(sourceSeqs(evs)); got != "[1 2 3 4 5 6 7]" {
		t.Fatalf("source identities = %s", got)
	}
	for i, ev := range evs {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("event %d has seq %d", i, ev.Seq)
		}
	}
}

// TestStoreFailedWriteIsLossNeverLateAppend pins Decision 5: fail the append
// of source 10, append 11, restart and replay — 10 is not appended after 11,
// the loss count is 1, and the compaction cut is unchanged.
func TestStoreFailedWriteIsLossNeverLateAppend(t *testing.T) {
	root := t.TempDir()
	orig := writeFile
	t.Cleanup(func() { writeFile = orig })
	writeFile = func(f *os.File, b []byte) (int, error) {
		if bytes.Contains(b, []byte(`"id":"ev-10"`)) {
			return 0, errors.New("injected write failure")
		}
		return orig(f, b)
	}
	s, log := openTest(t, root, nil)
	compaction := sourced("sess", 9, wsaudit.KindCompaction)
	compaction.Meta = map[string]any{wsaudit.MetaEstimated: true}
	mustAppend(t, s, compaction)
	mustAppend(t, s, sourced("sess", 10, wsaudit.KindToolCall))
	mustAppend(t, s, sourced("sess", 11, wsaudit.KindToolResult))
	flush(t, s)
	if !strings.Contains(log.String(), "injected write failure") {
		t.Fatalf("write failure not logged: %s", log.String())
	}
	before := wsaudit.FindModelSeenCut(events(t, s, "sess"))
	_ = s.Close()
	writeFile = orig

	s2, _ := openTest(t, root, nil)
	for seq := uint64(9); seq <= 11; seq++ {
		mustAppend(t, s2, sourced("sess", seq, wsaudit.KindToolCall))
	}
	flush(t, s2)
	evs := events(t, s2, "sess")
	if got := fmt.Sprint(sourceSeqs(evs)); got != "[9 11]" {
		t.Fatalf("source identities = %s", got)
	}
	rec, _ := s2.Recover(context.Background(), "sess")
	if rec.Loss != 1 {
		t.Fatalf("loss = %d", rec.Loss)
	}
	if after := wsaudit.FindModelSeenCut(evs); after != before || !after.Estimated {
		t.Fatalf("cut moved: %+v -> %+v", before, after)
	}
	_ = s2.Close()
	s3, _ := openTest(t, root, nil)
	mustAppend(t, s3, sourced("sess", 10, wsaudit.KindToolCall))
	flush(t, s3)
	if rec, _ := s3.Recover(context.Background(), "sess"); rec.Loss != 1 {
		t.Fatalf("a second replay counted the loss again: %d", rec.Loss)
	}
}

// TestStoreFinalizedBoundaryRefusesLateRecords pins the per-source
// finalized boundary: a record at or below it without a retained identity
// is loss, even across a restart.
func TestStoreFinalizedBoundaryRefusesLateRecords(t *testing.T) {
	root := t.TempDir()
	s, _ := openTest(t, root, nil)
	ctx := context.Background()
	mustAppend(t, s, sourced("sess", 1, wsaudit.KindToolCall))
	if err := s.Finalize(ctx, "sess", "holder:1", 3); err != nil { // 2 and 3 dropped upstream
		t.Fatal(err)
	}
	mustAppend(t, s, sourced("sess", 2, wsaudit.KindToolCall))
	mustAppend(t, s, sourced("sess", 4, wsaudit.KindToolCall))
	flush(t, s)
	_ = s.Close()
	s2, _ := openTest(t, root, nil)
	mustAppend(t, s2, sourced("sess", 3, wsaudit.KindToolCall))
	flush(t, s2)
	if got := fmt.Sprint(sourceSeqs(events(t, s2, "sess"))); got != "[1 4]" {
		t.Fatalf("source identities = %s", got)
	}
	rec, _ := s2.Recover(ctx, "sess")
	if rec.Loss != 1 || rec.Sources["holder:1"].Boundary != 4 {
		t.Fatalf("recovery = %+v", rec)
	}
}

// TestStoreRefusesNewerRingFormat pins Decision 4: a ring holding a newer
// schema version is not written.
func TestStoreRefusesNewerRingFormat(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sess")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	seg := filepath.Join(dir, segmentName(1))
	content := []byte(`{"t":"h","v":2,"session":"sess"}` + "\n" + `{"t":"future"}` + "\n")
	if err := os.WriteFile(seg, content, 0o600); err != nil {
		t.Fatal(err)
	}
	s, log := openTest(t, root, nil)
	mustAppend(t, s, sourced("sess", 1, wsaudit.KindToolCall))
	flush(t, s)
	raw, _ := os.ReadFile(seg)
	if !bytes.Equal(raw, content) {
		t.Fatal("the newer ring was written")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("a new segment was added: %d files", len(entries))
	}
	if !strings.Contains(log.String(), "newer format") {
		t.Fatalf("refusal not logged: %s", log.String())
	}
}

// TestStoreRecoversIndependentRingsModesAndHeldHandle pins recovery into the
// in-memory index, one ring per session, 0600/0700 modes, and one held
// handle: steady appends open nothing and write once each.
func TestStoreRecoversIndependentRingsModesAndHeldHandle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "audit")
	s, _ := openTest(t, root, nil)
	mustAppend(t, s, sourced("alpha", 1, wsaudit.KindToolCall))
	mustAppend(t, s, sourced("beta", 1, wsaudit.KindToolCall))
	flush(t, s)
	before := wsstate.FileIOCounters()
	const n = 50
	for seq := uint64(2); seq < 2+n; seq++ {
		mustAppend(t, s, sourced("alpha", seq, wsaudit.KindToolCall))
	}
	flush(t, s)
	after := wsstate.FileIOCounters()
	if opens := after["file_io_open_file"] - before["file_io_open_file"]; opens != 0 {
		t.Fatalf("steady appends opened %d files", opens)
	}
	if writes := after["file_io_write"] - before["file_io_write"]; writes != n {
		t.Fatalf("%d appends made %d writes", n, writes)
	}
	t.Logf("file ops for %d steady appends: total %d (writes %d)", n, after["file_io_total"]-before["file_io_total"], after["file_io_write"]-before["file_io_write"])
	_ = s.Close()

	s2, _ := openTest(t, root, nil)
	if got := len(events(t, s2, "alpha")); got != 1+n {
		t.Fatalf("alpha recovered %d events", got)
	}
	if got := fmt.Sprint(sourceSeqs(events(t, s2, "beta"))); got != "[1]" {
		t.Fatalf("beta = %s", got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, path := range []string{filepath.Join(root, "alpha"), filepath.Join(root, "beta"), root} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode %v %v", path, info.Mode().Perm(), err)
		}
	}
	info, err := os.Stat(filepath.Join(root, "alpha", segmentName(1)))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("segment mode %v %v", info.Mode().Perm(), err)
	}
}

// TestStoreTornTailIsTrimmed pins recovery from a torn last line.
func TestStoreTornTailIsTrimmed(t *testing.T) {
	root := t.TempDir()
	s, _ := openTest(t, root, nil)
	mustAppend(t, s, sourced("sess", 1, wsaudit.KindToolCall))
	flush(t, s)
	_ = s.Close()
	seg := filepath.Join(root, "sess", segmentName(1))
	f, _ := os.OpenFile(seg, os.O_WRONLY|os.O_APPEND, 0o600)
	_, _ = f.WriteString(`{"t":"e","e":{"v":1,"id":"torn`)
	_ = f.Close()
	s2, _ := openTest(t, root, nil)
	mustAppend(t, s2, sourced("sess", 2, wsaudit.KindToolCall))
	flush(t, s2)
	_ = s2.Close()
	s3, _ := openTest(t, root, nil)
	if got := fmt.Sprint(sourceSeqs(events(t, s3, "sess"))); got != "[1 2]" {
		t.Fatalf("recovered %s", got)
	}
}

// TestStoreSlowOrFailingWriterNeverDelaysProducer pins the non-blocking
// writer: a slow disk fills the queue and drops with a log line, and a
// failing disk drops with a log line, while Append returns at once.
func TestStoreSlowOrFailingWriterNeverDelaysProducer(t *testing.T) {
	orig := writeFile
	t.Cleanup(func() { writeFile = orig })
	release := make(chan struct{})
	writeFile = func(f *os.File, b []byte) (int, error) {
		<-release
		return orig(f, b)
	}
	s, log := openTest(t, t.TempDir(), func(o *Options) { o.QueueDepth = 4 })
	start := time.Now()
	var full int
	for seq := uint64(1); seq <= 200; seq++ {
		if err := s.Append(context.Background(), sourced("sess", seq, wsaudit.KindToolCall)); errors.Is(err, ErrQueueFull) {
			full++
		}
	}
	elapsed := time.Since(start)
	close(release)
	_ = s.Close()
	if elapsed > 500*time.Millisecond {
		t.Fatalf("200 appends against a stalled disk took %s", elapsed)
	}
	if full == 0 || s.Dropped() < uint64(full) || !strings.Contains(log.String(), "write queue full") {
		t.Fatalf("full=%d dropped=%d log=%q", full, s.Dropped(), log.String())
	}

	writeFile = func(*os.File, []byte) (int, error) { return 0, errors.New("disk gone") }
	s2, log2 := openTest(t, t.TempDir(), nil)
	start = time.Now()
	for seq := uint64(1); seq <= 20; seq++ {
		if err := s2.Append(context.Background(), sourced("sess", seq, wsaudit.KindToolCall)); err != nil {
			t.Fatalf("append returned %v", err)
		}
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("appends waited on a failing disk")
	}
	flush(t, s2)
	if s2.Dropped() != 20 || !strings.Contains(log2.String(), "disk gone") {
		t.Fatalf("dropped=%d log=%q", s2.Dropped(), log2.String())
	}
}

// TestStoreFileOpsPerAppendedEvent measures file operations per appended
// event (performance.md, Measuring): one write in steady state, and a
// bounded handful at rotation and eviction.
func TestStoreFileOpsPerAppendedEvent(t *testing.T) {
	lim := Limits{SessionBytes: 4 * mib, TotalBytes: 64 * mib, BodyBytes: 64 * kib, Retention: MinRetention}
	s, _ := openTest(t, t.TempDir(), func(o *Options) { o.Limits = lim })
	body := strings.Repeat("x", 60*1024)
	mustAppend(t, s, sourced("sess", 1, wsaudit.KindToolCall))
	flush(t, s)
	before := wsstate.FileIOCounters()
	const n = 200 // ~12 MiB through a 4 MiB ring of 1 MiB segments
	for seq := uint64(2); seq < 2+n; seq++ {
		ev := sourced("sess", seq, wsaudit.KindToolResult)
		ev.Out = body
		mustAppend(t, s, ev)
		if seq%16 == 0 {
			flush(t, s)
		}
	}
	flush(t, s)
	after := wsstate.FileIOCounters()
	delta := func(k string) int64 { return after[k] - before[k] }
	rotations := delta("file_io_open_file")
	t.Logf("%d appends: total %d file ops (write %d, open %d, close %d, remove %d) = %.2f per event; %d rotations",
		n, delta("file_io_total"), delta("file_io_write"), rotations, delta("file_io_close"), delta("file_io_remove"),
		float64(delta("file_io_total"))/n, rotations)
	if delta("file_io_write") != n || delta("file_io_total") > n+4*rotations {
		t.Fatalf("file ops exceed one write per event plus rotation: %+v", delta("file_io_total"))
	}
	if delta("file_io_read_file") != 0 || delta("file_io_read_dir") != 0 {
		t.Fatal("appends reread files")
	}
}
