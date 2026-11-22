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

// item is an envelope and the source identity capture appends it under.
type item struct {
	id wsaudit.SourceID
	wsaudit.Envelope
}

func sourced(session string, seq uint64, kind wsaudit.Kind) item {
	ev, _ := wsaudit.NewEnvelope(session, "claude", time.Now(), kind)
	ev.ID = fmt.Sprintf("ev-%d", seq)
	ev.Seq = seq
	ev.Out = fmt.Sprintf("output %d", seq)
	return item{id: wsaudit.SourceID{Source: "holder:1", Seq: seq}, Envelope: ev}
}

func tee(session string, anchor, index uint64) item {
	ev, _ := wsaudit.NewEnvelope(session, "claude", time.Now(), wsaudit.KindAssistantText)
	ev.ID = fmt.Sprintf("tee-%d.%d", anchor, index)
	ev.Out = ev.ID
	return item{id: wsaudit.SourceID{Source: "holder:1", Tee: true, Anchor: anchor, Index: index}, Envelope: ev}
}

func mustAppend(t *testing.T, s *Store, it item) {
	t.Helper()
	// Capture treats a known duplicate as success.
	if err := s.AppendSourced(context.Background(), it.id, it.Envelope); err != nil && !errors.Is(err, wsaudit.ErrDuplicateSource) {
		t.Fatal(err)
	}
}

// loaded recovers a session's ring after a restart, so the in-memory
// recovery answers are complete.
func loaded(t *testing.T, s *Store, session string) {
	t.Helper()
	s.Preload(session)
	flush(t, s)
}

// retainedIDs lists the source identities of a session's retained events in
// order, in SourceID.String form.
func retainedIDs(t *testing.T, s *Store, session string) []string {
	t.Helper()
	info, err := s.loaded(context.Background(), session)
	if err != nil || info == nil {
		t.Fatalf("session %s not loaded: %v", session, err)
	}
	info.mu.Lock()
	defer info.mu.Unlock()
	var out []string
	for _, id := range info.ring.ids {
		if id != nil {
			out = append(out, id.String())
		}
	}
	return out
}

func lossCount(t *testing.T, s *Store, session string) uint64 {
	t.Helper()
	l := s.ledgerOf(session)
	if l == nil {
		t.Fatalf("session %s not loaded", session)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loss
}

func events(t *testing.T, s *Store, session string) []wsaudit.Envelope {
	t.Helper()
	evs, err := s.Events(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func sourceSeqs(t *testing.T, s *Store, session string) []uint64 {
	t.Helper()
	var out []uint64
	for _, id := range retainedIDs(t, s, session) {
		var seq uint64
		if _, err := fmt.Sscanf(id, "holder:1/s%d#0", &seq); err == nil {
			out = append(out, seq)
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
	if rec := s2.Recover("sess", "holder:1"); rec != (wsaudit.Recovery{}) {
		t.Fatalf("recovery of an unloaded ring = %+v, want zero", rec)
	}
	flush(t, s2) // the first question queued the load
	// Appends alone never finalize: capture reports the boundary.
	rec := s2.Recover("sess", "holder:1")
	if rec != (wsaudit.Recovery{MaxSeq: 5}) || !s2.Retained("sess", wsaudit.SourceID{Source: "holder:1", Seq: 5}) {
		t.Fatalf("recovery = %+v", rec)
	}
	if err := s2.AppendSourced(context.Background(), wsaudit.SourceID{Source: "holder:1", Seq: 3}, sourced("sess", 3, wsaudit.KindToolCall).Envelope); !errors.Is(err, wsaudit.ErrDuplicateSource) {
		t.Fatalf("append of a retained identity = %v", err)
	}
	for seq := uint64(1); seq <= 5; seq++ { // holder reattachment replays
		mustAppend(t, s2, sourced("sess", seq, wsaudit.KindToolCall))
	}
	for seq := uint64(6); seq <= 7; seq++ {
		mustAppend(t, s2, sourced("sess", rec.MaxSeq+seq-5, wsaudit.KindToolCall))
	}
	flush(t, s2)
	evs := events(t, s2, "sess")
	if got := fmt.Sprint(sourceSeqs(t, s2, "sess")); got != "[1 2 3 4 5 6 7]" {
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
	if got := fmt.Sprint(sourceSeqs(t, s2, "sess")); got != "[9 11]" {
		t.Fatalf("source identities = %s", got)
	}
	if n := lossCount(t, s2, "sess"); n != 1 {
		t.Fatalf("loss = %d", n)
	}
	if after := wsaudit.FindModelSeenCut(evs); after != before || !after.Estimated {
		t.Fatalf("cut moved: %+v -> %+v", before, after)
	}
	_ = s2.Close()
	s3, _ := openTest(t, root, nil)
	mustAppend(t, s3, sourced("sess", 10, wsaudit.KindToolCall))
	flush(t, s3)
	if n := lossCount(t, s3, "sess"); n != 1 {
		t.Fatalf("a second replay counted the loss again: %d", n)
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
	if got := fmt.Sprint(sourceSeqs(t, s2, "sess")); got != "[1 4]" {
		t.Fatalf("source identities = %s", got)
	}
	if rec, n := s2.Recover("sess", "holder:1"), lossCount(t, s2, "sess"); n != 2 || rec.Boundary != 3 || rec.MaxSeq != 4 {
		t.Fatalf("recovery = %+v, loss %d", rec, n)
	}
}

// TestStoreTeeAfterItsAnchorIsAdmitted pins the tee rule of the finalized
// boundary: retaining or losing a record never moves the boundary, so a tee
// record anchored on the last kept line is appended; once capture finalizes
// past an anchor, a tee anchored below the boundary is loss, and one anchored
// on it is still admitted.
func TestStoreTeeAfterItsAnchorIsAdmitted(t *testing.T) {
	root := t.TempDir()
	s, _ := openTest(t, root, nil)
	ctx := context.Background()
	mustAppend(t, s, sourced("sess", 1, wsaudit.KindToolCall))
	mustAppend(t, s, sourced("sess", 2, wsaudit.KindToolCall))
	mustAppend(t, s, tee("sess", 2, 1)) // after kept line 2, before any finalize
	flush(t, s)
	if err := s.Finalize(ctx, "sess", "holder:1", 2); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, s, tee("sess", 2, 2)) // anchored on the boundary: admitted
	mustAppend(t, s, tee("sess", 1, 1)) // anchored below it: loss
	mustAppend(t, s, sourced("sess", 3, wsaudit.KindToolCall))
	key := sourced("sess", 0, wsaudit.KindSystem)
	key.id = wsaudit.SourceID{Source: "holder:1", Key: "end.3"}
	mustAppend(t, s, key) // a synthesized record is never covered
	flush(t, s)
	want := "[holder:1/s1#0 holder:1/s2#0 holder:1/t2.1#0 holder:1/t2.2#0 holder:1/s3#0 holder:1/kend.3#0]"
	if got := fmt.Sprint(retainedIDs(t, s, "sess")); got != want {
		t.Fatalf("retained = %s", got)
	}
	if n := lossCount(t, s, "sess"); n != 1 {
		t.Fatalf("loss = %d", n)
	}
	_ = s.Close()
	s2, _ := openTest(t, root, nil)
	loaded(t, s2, "sess")
	if rec := s2.Recover("sess", "holder:1"); rec != (wsaudit.Recovery{MaxSeq: 6, Boundary: 2}) {
		t.Fatalf("recovered %+v", rec)
	}
	if got := fmt.Sprint(retainedIDs(t, s2, "sess")); got != want || lossCount(t, s2, "sess") != 1 {
		t.Fatalf("recovered retained = %s", got)
	}
}

// TestStoreRecordLossAndBoundaryPersistWithoutALaterRecord pins that capture's
// RecordLoss and Finalize reach disk once the writer idles, with no further
// record to carry them, and survive a restart.
func TestStoreRecordLossAndBoundaryPersistWithoutALaterRecord(t *testing.T) {
	root := t.TempDir()
	s, _ := openTest(t, root, nil)
	ctx := context.Background()
	mustAppend(t, s, sourced("sess", 1, wsaudit.KindToolCall))
	flush(t, s)
	lost := wsaudit.SourceID{Source: "holder:1", Seq: 2}
	if err := s.RecordLoss(ctx, "sess", lost, wsaudit.LossDropped); err != nil {
		t.Fatal(err)
	}
	if err := s.Finalize(ctx, "sess", "holder:1", 2); err != nil {
		t.Fatal(err)
	}
	flush(t, s)
	flush(t, s) // the writer idled in between
	raw, err := os.ReadFile(filepath.Join(root, "sess", segmentName(1)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"t":"loss"`)) || !bytes.Contains(raw, []byte(`"reason":"dropped"`)) || !bytes.Contains(raw, []byte(`"t":"fin"`)) {
		t.Fatalf("control lines not on disk:\n%s", raw)
	}
	_ = s.Close()
	s3, _ := openTest(t, root, nil)
	loaded(t, s3, "sess")
	if rec := s3.Recover("sess", "holder:1"); rec.Boundary != 2 || lossCount(t, s3, "sess") != 1 {
		t.Fatalf("recovered %+v", rec)
	}
	mustAppend(t, s3, sourced("sess", 2, wsaudit.KindToolCall)) // replayed: already lost
	flush(t, s3)
	if got := fmt.Sprint(sourceSeqs(t, s3, "sess")); got != "[1]" {
		t.Fatalf("source identities = %s", got)
	}
}

// TestStoreRecoveryAnswersNeverWaitOnAWrite pins the SourcedSink contract:
// Recover, Retained and AppendSourced return while the writer is stuck in a
// disk write of the same ring.
func TestStoreRecoveryAnswersNeverWaitOnAWrite(t *testing.T) {
	orig := writeFile
	t.Cleanup(func() { writeFile = orig })
	s, _ := openTest(t, t.TempDir(), nil)
	mustAppend(t, s, sourced("sess", 1, wsaudit.KindToolCall))
	flush(t, s)
	s.Close()
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	writeFile = func(f *os.File, b []byte) (int, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return orig(f, b)
	}
	root := s.opts.Root
	s2, _ := openTest(t, root, nil)
	defer close(release)
	loaded(t, s2, "sess")
	mustAppend(t, s2, sourced("sess", 2, wsaudit.KindToolCall))
	<-entered
	done := make(chan wsaudit.Recovery, 1)
	go func() {
		_ = s2.AppendSourced(context.Background(), wsaudit.SourceID{Source: "holder:1", Seq: 3}, sourced("sess", 3, wsaudit.KindToolCall).Envelope)
		if !s2.Retained("sess", wsaudit.SourceID{Source: "holder:1", Seq: 1}) {
			t.Error("retained identity not reported")
		}
		done <- s2.Recover("sess", "holder:1")
	}()
	select {
	case rec := <-done:
		if rec.MaxSeq != 1 {
			t.Fatalf("recovery during a write = %+v", rec)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery questions waited on the writer's disk write")
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
	if got := fmt.Sprint(sourceSeqs(t, s2, "beta")); got != "[1]" {
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
	if got := fmt.Sprint(sourceSeqs(t, s3, "sess")); got != "[1 2]" {
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
		if err := s.Append(context.Background(), sourced("sess", seq, wsaudit.KindToolCall).Envelope); errors.Is(err, ErrQueueFull) {
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
		if err := s2.Append(context.Background(), sourced("sess", seq, wsaudit.KindToolCall).Envelope); err != nil {
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

// TestStoreSiblingsOfOneSourceRecordAreAllRetained pins the review's
// regression: the envelopes one source record yields (Ordinal) and the tee
// lines of one interval (Index) share a source position, and every one is
// retained on a clean run; once capture finalizes past that position, a
// replayed sibling is a duplicate and one never appended is loss.
func TestStoreSiblingsOfOneSourceRecordAreAllRetained(t *testing.T) {
	s, _ := openTest(t, t.TempDir(), nil)
	ctx := context.Background()
	for ord := 0; ord < 3; ord++ {
		it := sourced("sess", 5, wsaudit.KindToolCall)
		it.id.Ordinal = ord
		it.ID = fmt.Sprintf("ev-5-%d", ord)
		mustAppend(t, s, it)
	}
	for idx := uint64(1); idx <= 2; idx++ {
		mustAppend(t, s, tee("sess", 7, idx))
	}
	flush(t, s)
	want := "[holder:1/s5#0 holder:1/s5#1 holder:1/s5#2 holder:1/t7.1#0 holder:1/t7.2#0]"
	if got := fmt.Sprint(retainedIDs(t, s, "sess")); got != want || lossCount(t, s, "sess") != 0 {
		t.Fatalf("retained %s, loss %d", got, lossCount(t, s, "sess"))
	}
	if err := s.Finalize(ctx, "sess", "holder:1", 8); err != nil {
		t.Fatal(err)
	}
	replay := sourced("sess", 5, wsaudit.KindToolCall)
	replay.id.Ordinal = 1
	mustAppend(t, s, replay)
	missing := sourced("sess", 5, wsaudit.KindToolCall)
	missing.id.Ordinal = 3
	mustAppend(t, s, missing)
	flush(t, s)
	if got := fmt.Sprint(retainedIDs(t, s, "sess")); got != want || lossCount(t, s, "sess") != 1 {
		t.Fatalf("after finalize: retained %s, loss %d", got, lossCount(t, s, "sess"))
	}
}

// TestStoreDisplayKeyLookupNormalizesRawIDsExactlyOnce pins Decision 3's
// ownership split: capture hands the store raw IDs, the store normalizes each
// exactly once at ingestion, and a lookup by the display key
// (wsaudit.NormalizeID of the raw ID) finds the record before and after a
// restart; a key normalized twice does not, and a short literal ID still
// resolves to itself.
func TestStoreDisplayKeyLookupNormalizesRawIDsExactlyOnce(t *testing.T) {
	root := t.TempDir()
	s, _ := openTest(t, root, nil)
	session := strings.Repeat("s", 300) // a raw session ID past the identifier bound
	rawID, rawCall, rawParent := strings.Repeat("i", 300), strings.Repeat("c", 300), strings.Repeat("p", 300)
	long := sourced(session, 1, wsaudit.KindToolCall)
	long.ID, long.CallID, long.ParentID = rawID, rawCall, rawParent
	short := sourced(session, 2, wsaudit.KindToolResult)
	short.ID, short.CallID = "short-id", "call-1"
	mustAppend(t, s, long)
	mustAppend(t, s, short)
	flush(t, s)
	check := func(s *Store) {
		t.Helper()
		ctx := context.Background()
		ev, ok, err := s.Event(ctx, session, wsaudit.NormalizeID(rawID))
		if err != nil || !ok || ev.CallID != wsaudit.NormalizeID(rawCall) || ev.ParentID != wsaudit.NormalizeID(rawParent) {
			t.Fatalf("display-key lookup = %+v %v %v", ev, ok, err)
		}
		if _, ok, _ := s.Event(ctx, session, wsaudit.NormalizeID(wsaudit.NormalizeID(rawID))); ok {
			t.Fatal("a twice-normalized key found the record")
		}
		if evs, _ := s.EventsByCallID(ctx, session, wsaudit.NormalizeID(rawCall)); len(evs) != 1 || evs[0].ID != wsaudit.NormalizeID(rawID) {
			t.Fatalf("call-key lookup = %+v", evs)
		}
		if _, ok, _ := s.Event(ctx, session, "short-id"); !ok {
			t.Fatal("a short literal ID did not resolve to itself")
		}
		if evs, _ := s.EventsByCallID(ctx, session, "call-1"); len(evs) != 1 {
			t.Fatalf("short call key = %+v", evs)
		}
	}
	check(s)
	_ = s.Close()
	s2, _ := openTest(t, root, nil)
	check(s2)
}
