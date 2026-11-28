package wsagent

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// snapshotRunningNative is the count syncRunningNativeAgents took from a
// whole snapshot before it read the lean row query.
func snapshotRunningNative(w *liveWriter) int32 {
	var n int32
	for _, a := range w.st.Snapshot(w.now().UnixMilli()).Agents {
		if a.Status == wslive.StatusRunning && a.AgentKind == "" {
			n++
		}
	}
	return n
}

// TestSyncRunningNativeAgentsMatchesSnapshot pins the running-native mirror
// to the count a whole snapshot gives, through every apply path that syncs
// it, including a native row merged into its pi row.
func TestSyncRunningNativeAgentsMatchesSnapshot(t *testing.T) {
	w := newLiveWriter(t.TempDir(), "running-native-key", "claude", time.Now, io.Discard)
	check := func(step string, want int32) {
		t.Helper()
		if got, snap := w.runningNativeAgents.Load(), snapshotRunningNative(w); got != snap || got != want {
			t.Fatalf("%s: mirror %d, snapshot %d, want %d", step, got, snap, want)
		}
		if w.hasRunningNativeAgents() != (want > 0) {
			t.Fatalf("%s: hasRunningNativeAgents = %v", step, w.hasRunningNativeAgents())
		}
	}
	check("empty", 0)
	w.applyQuiet(wslive.Event{Kind: wslive.KindAgent, AgentID: "native-a", Status: wslive.StatusRunning})
	check("replayed start", 1)
	w.applyRAM(wslive.Event{Kind: wslive.KindAgent, AgentID: "native-b", Status: wslive.StatusRunning})
	check("RAM start", 2)
	w.apply(wslive.Event{Kind: wslive.KindAgent, AgentID: "pi:native-b", AgentKind: wslive.AgentKindPi, Status: wslive.StatusRunning})
	check("native row merged into its pi row", 1)
	w.apply(wslive.Event{Kind: wslive.KindAgent, AgentID: "native-a", Status: wslive.StatusDone})
	check("end", 0)
}

// TestLiveWriterSharedObservedModelsRace runs the conversation loop's
// applies and snapshots against off-loop readers: the running-native mirror
// (runner_conv.go reads it lock-free) and published snapshots whose observed
// models the state now shares instead of cloning. Under -race a write into a
// shared model, or a published model changing afterwards, fails the test.
func TestLiveWriterSharedObservedModelsRace(t *testing.T) {
	w := newLiveWriter(t.TempDir(), "observed-race-key", "claude", time.Now, io.Discard)
	type published struct {
		snap wslive.Snapshot
		raw  []byte
	}
	snaps := make(chan published, 32)
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			_ = w.hasRunningNativeAgents()
		}
	}()
	failed := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range snaps {
				raw, _ := json.Marshal([]any{p.snap.Lead.Observed, p.snap.ObservedAgents})
				if string(raw) != string(p.raw) {
					select {
					case failed <- fmt.Sprintf("published model changed:\n%s\n%s", p.raw, raw):
					default:
					}
				}
			}
		}()
	}
	for i := range 200 {
		w.applyQuiet(wslive.Event{Kind: wslive.KindStatus, Observed: &wslive.ModelObservation{ID: fmt.Sprintf("lead-%d", i), Model: fmt.Sprintf("m%d", i%3), Source: "pi.assistant", At: int64(100 + i)}})
		w.applyQuiet(wslive.Event{Kind: wslive.KindAgent, AgentID: fmt.Sprintf("native-%d", i%4), Status: []string{wslive.StatusRunning, wslive.StatusDone}[i%2],
			Observed: &wslive.ModelObservation{ID: fmt.Sprintf("child-%d", i), Model: fmt.Sprintf("c%d", i%2), Source: "pi.assistant", At: int64(100 + i)}})
		snap := w.st.Snapshot(w.now().UnixMilli())
		raw, _ := json.Marshal([]any{snap.Lead.Observed, snap.ObservedAgents})
		snaps <- published{snap: snap, raw: raw}
	}
	close(snaps)
	stop.Store(true)
	wg.Wait()
	select {
	case msg := <-failed:
		t.Fatal(msg)
	default:
	}
	if got, want := w.runningNativeAgents.Load(), snapshotRunningNative(w); got != want {
		t.Fatalf("mirror %d, snapshot %d", got, want)
	}
}
