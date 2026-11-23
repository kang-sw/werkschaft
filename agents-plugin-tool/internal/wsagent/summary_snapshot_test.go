package wsagent

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

type summarySnapshotHost struct {
	mu    sync.Mutex
	slots map[string]*summarySnapshotSlot
}

type summarySnapshotSlot struct {
	host   *summarySnapshotHost
	dir    string
	snap   wslive.Snapshot
	closed bool
}

func (h *summarySnapshotHost) OpenHostedSlot(dir string) (wsdashboard.HostedSlot, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.slots == nil {
		h.slots = map[string]*summarySnapshotSlot{}
	}
	s := &summarySnapshotSlot{host: h, dir: dir}
	h.slots[dir] = s
	return s, true
}

func (s *summarySnapshotSlot) LockOwner() (func(), bool) {
	s.host.mu.Lock()
	if s.closed || s.host.slots[s.dir] != s {
		s.host.mu.Unlock()
		return nil, false
	}
	return s.host.mu.Unlock, true
}

func (s *summarySnapshotSlot) Publish(snap wslive.Snapshot) {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	if !s.closed && s.host.slots[s.dir] == s {
		s.snap = snap
	}
}
func (s *summarySnapshotSlot) RingAppended() {}
func (s *summarySnapshotSlot) Close() {
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	s.closed = true
	if s.host.slots[s.dir] == s {
		delete(s.host.slots, s.dir)
	}
}
func (h *summarySnapshotHost) snapshot(dir string) (wslive.Snapshot, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if slot := h.slots[dir]; slot != nil {
		return slot.snap, true
	}
	return wslive.Snapshot{}, false
}

func assertNoSummarySnapshot(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(wslive.SnapshotPath(dir))
	if err != nil || bytes.Contains(raw, []byte(`"reasoningSummary"`)) || bytes.Contains(raw, []byte("received prefix")) {
		t.Fatalf("mutable snapshot contains live data: %s %v", raw, err)
	}
	snap, err := wslive.ReadSnapshot(wslive.SnapshotPath(dir))
	if err != nil || snap.Lead.ReasoningSummary != nil || snap.SchemaVersion != wslive.SchemaVersion {
		t.Fatalf("disk projection/schema changed: %+v %v", snap, err)
	}
}

func TestSummaryRecordWriterReopenBlanksLiveUntilNextEvent(t *testing.T) {
	dir := t.TempDir()
	host := &summarySnapshotHost{}
	w := newLiveWriter(dir, "writer-key", "pi", time.Now, os.Stderr)
	w.owner = func() (func(), bool) { return func() {}, true }
	w.slot, _ = host.OpenHostedSlot(dir)
	w.summarySource = "observed-holder/1"
	w.apply(wslive.Event{Kind: wslive.KindTurnStart, At: 1000})
	w.apply(wslive.Event{Kind: wslive.KindAgent, ReasoningSummary: hostedSummaryFixture()})
	w.writeSnapshot() // heartbeat path uses the same disk-only projection.
	assertNoSummarySnapshot(t, dir)
	if snap, ok := host.snapshot(dir); !ok || snap.Lead.ReasoningSummary == nil {
		t.Fatal("disk filtering mutated RAM publication")
	}
	w.closeSlot()
	assertNoSummarySnapshot(t, dir) // shutdown flush also uses ForDisk, never RAM data
	if _, ok := host.snapshot(dir); ok {
		t.Fatal("closed writer retained a live cache")
	}

	reopened := newLiveWriter(dir, "writer-key", "pi", time.Now, os.Stderr)
	if reopened.st.Snapshot(2000).Lead.ReasoningSummary != nil {
		t.Fatal("reopen claimed recovery of a volatile live field")
	}
	reopened.owner = func() (func(), bool) { return func() {}, true }
	reopened.slot, _ = host.OpenHostedSlot(dir)
	defer reopened.closeSlot()
	reopened.summarySource = "observed-holder/2"
	reopened.apply(wslive.Event{Kind: wslive.KindTurnStart, At: 2000})
	next := hostedSummaryFixture()
	next.Revision = 2
	reopened.apply(wslive.Event{Kind: wslive.KindAgent, ReasoningSummary: next})
	if snap, ok := host.snapshot(dir); !ok || snap.Lead.ReasoningSummary == nil || snap.Lead.ReasoningSummary.Revision != 2 {
		t.Fatal("next admissible event did not repopulate RAM")
	}
	assertNoSummarySnapshot(t, dir)
	if entries, _ := os.ReadDir(filepath.Join(dir, "ring")); len(entries) != 0 {
		t.Fatal("reopen or live event fabricated a natural turn")
	}
}
func hostedSummaryFixture() *wslive.ReasoningSummary {
	return &wslive.ReasoningSummary{V: 1, Producer: "22222222-2222-4222-8222-222222222222", Turn: 1, Revision: 1, ActiveRequest: &wslive.SummaryPair{Request: 1, Attempt: 1}, Requests: []wslive.SummaryAttempt{{Request: 1, Attempt: 1, Provider: "openai-codex", API: "openai-codex-responses", Model: "model", Status: wslive.SummaryStreaming, Parts: []wslive.SummaryPart{{Text: "received prefix", ObservedBytes: 15, RetainedBytes: 15, Status: wslive.SummaryStreaming}}}}}
}
