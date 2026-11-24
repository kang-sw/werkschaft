package wsdashboard

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestHostedInProcessSlotStaysLivePastStaleWindowAndDropsOnClearOrClose(t *testing.T) {
	f := startPageFixture(t)
	snapPath := wslive.SnapshotPath(SessionDir(f.dir, "livesession"))
	diskBefore, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatal(err)
	}

	slot, ok := f.d.OpenHostedSlot(SessionDir(f.dir, "livesession"))
	if !ok {
		t.Fatal("OpenHostedSlot refused served session")
	}
	inProc, ok := slot.(InProcessLiveSlot)
	if !ok {
		t.Fatal("daemon hosted slot does not implement InProcessLiveSlot")
	}
	inProc.KeepAliveInProcess()

	t0 := time.Now().Truncate(time.Millisecond)
	snap := wslive.Snapshot{
		SchemaVersion: 1,
		ReplyID:       "livesession",
		Adapter:       "codex",
		UpdatedAt:     t0.UnixMilli(),
		HeartbeatMs:   wslive.HeartbeatInterval.Milliseconds(),
		Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseThinking}},
	}
	slot.Publish(snap)

	// Simulate conv.loop() stalled for 120s+ (>90s stale window) without any
	// heartbeat tick or snapshot publish.
	future := t0.Add(125 * time.Second)
	pages := f.d.pagesIn("")
	pages.now = func() time.Time { return future }

	if !pages.adapterPresent("livesession", future) {
		t.Fatal("in-process hosted slot flipped adapterPresent=false at +125s")
	}
	if present, _ := pages.adapterStamp("livesession", future); !present {
		t.Fatal("in-process hosted slot flipped adapterStamp=false at +125s")
	}
	view, ok := pages.session("livesession")
	if !ok || !view.Adapter || view.Mode() != modeLive {
		t.Fatalf("session view at +125s = ok:%v adapter:%v mode:%q, want live", ok, view.Adapter, view.Mode())
	}

	// ReadLiveStoresClock reports no stale deadline and stale=false for the
	// in-process RAM snapshot, and Index.RefreshClock triggers zero rereads.
	var hashBuf bytes.Buffer
	_, updatedAt, staleAt := f.d.snapshots.ReadLiveStoresClock(&hashBuf, f.dir, "livesession", future, LiveStoreFingerprint{}, AllLiveStores)
	if !updatedAt.Equal(t0) || !staleAt.IsZero() {
		t.Fatalf("ReadLiveStoresClock = updatedAt:%v staleAt:%v, want %v and zero", updatedAt, staleAt, t0)
	}
	if bytes.Contains(hashBuf.Bytes(), []byte("stale=true")) {
		t.Fatalf("ReadLiveStoresClock hashed stale=true for in-process hosted slot: %q", hashBuf.String())
	}
	readsBefore := f.src.readCount("livesession")
	f.d.Index().RefreshClock()
	f.d.Index().Settle()
	if got := f.src.readCount("livesession"); got != readsBefore {
		t.Fatalf("RefreshClock reread in-process hosted session %d times, want 0", got-readsBefore)
	}

	// Zero disk writes occurred for RAM liveness.
	diskAfter, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(diskBefore, diskAfter) {
		t.Fatal("in-process hosted RAM liveness modified snapshot.json on disk")
	}

	// Publishing an empty Adapter (close/handoff via setAdapter("")) drops
	// adapterPresent immediately even before Close().
	cleared := snap
	cleared.Adapter = ""
	slot.Publish(cleared)
	if pages.adapterPresent("livesession", future) {
		t.Fatal("empty Adapter still reported adapterPresent=true")
	}
	view, ok = pages.session("livesession")
	if !ok || view.Adapter || view.Mode() != modeHistory {
		t.Fatalf("session view after empty Adapter = ok:%v adapter:%v mode:%q, want history", ok, view.Adapter, view.Mode())
	}

	// Restoring a non-empty Adapter (even with the old UpdatedAt) makes the
	// open in-process slot live again, and Close() drops RAM liveness so the
	// aged disk snapshot expires at +125s.
	slot.Publish(snap)
	if !pages.adapterPresent("livesession", future) {
		t.Fatal("re-published in-process snapshot did not return adapterPresent=true")
	}
	slot.Close()
	if pages.adapterPresent("livesession", future) {
		t.Fatal("closed slot still reported adapterPresent=true for aged disk snapshot")
	}
}

func TestExternalAndNonKeepAliveSnapshotsStillExpireAfterStaleWindow(t *testing.T) {
	f := startPageFixture(t)
	t0 := time.Now().Truncate(time.Millisecond)
	extSnap := wslive.Snapshot{
		SchemaVersion: 1,
		ReplyID:       "livesession",
		Adapter:       "pi",
		UpdatedAt:     t0.UnixMilli(),
		HeartbeatMs:   wslive.HeartbeatInterval.Milliseconds(),
	}
	writeSnapshot(t, f.dir, "livesession", &extSnap)
	f.d.snapshots.Invalidate(f.dir, "livesession")

	pages := f.d.pagesIn("")
	if !pages.adapterPresent("livesession", t0.Add(89*time.Second)) {
		t.Fatal("external disk snapshot expired before 90s")
	}
	if pages.adapterPresent("livesession", t0.Add(91*time.Second)) {
		t.Fatal("external disk snapshot failed to expire past 90s")
	}

	// A HostedSlot without KeepAliveInProcess (proxied external terminal MCP
	// session) still relies on its 90s heartbeat stale window.
	slot, ok := f.d.OpenHostedSlot(SessionDir(f.dir, "livesession"))
	if !ok {
		t.Fatal("OpenHostedSlot failed")
	}
	defer slot.Close()
	slot.Publish(extSnap)
	if !pages.adapterPresent("livesession", t0.Add(89*time.Second)) {
		t.Fatal("non-keepalive hosted slot expired before 90s")
	}
	if pages.adapterPresent("livesession", t0.Add(91*time.Second)) {
		t.Fatal("non-keepalive hosted slot failed to expire past 90s")
	}
}

func TestHostedModeRecoveryHistoryToLiveEmitsModeRegion(t *testing.T) {
	f := startPageFixture(t)
	staleAt := time.Now().Add(-5 * time.Minute).Truncate(time.Millisecond)
	writeSnapshot(t, f.dir, "livesession", &wslive.Snapshot{
		SchemaVersion: 1,
		ReplyID:       "livesession",
		Adapter:       "codex",
		UpdatedAt:     staleAt.UnixMilli(),
		HeartbeatMs:   wslive.HeartbeatInterval.Milliseconds(),
	})
	f.d.snapshots.Invalidate(f.dir, "livesession")

	if view, ok := f.d.pagesIn("").session("livesession"); !ok || view.Mode() != modeHistory {
		t.Fatalf("initial view = ok:%v mode:%q, want history", ok, view.Mode())
	}

	stream := openStream(t, f.base+"/s/livesession/events")
	waitRegionHolding(t, stream, regionMode, modeHistory)
	quiet(t, stream)

	slot, ok := f.d.OpenHostedSlot(SessionDir(f.dir, "livesession"))
	if !ok {
		t.Fatal("OpenHostedSlot failed")
	}
	defer slot.Close()
	slot.(InProcessLiveSlot).KeepAliveInProcess()
	// Publish even with an aged UpdatedAt: in-process RAM liveness must bring
	// the session back to live mode and emit the "mode" SSE event.
	slot.Publish(wslive.Snapshot{
		SchemaVersion: 1,
		ReplyID:       "livesession",
		Adapter:       "codex",
		UpdatedAt:     staleAt.UnixMilli(),
		HeartbeatMs:   wslive.HeartbeatInterval.Milliseconds(),
		Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}},
	})

	waitRegionHolding(t, stream, regionMode, modeLive)
}
