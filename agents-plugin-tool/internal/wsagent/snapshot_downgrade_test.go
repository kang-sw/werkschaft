package wsagent

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// v0.51.0 (f800ba30d) members the current snapshot added: the old writer
// decodes the whole file into its fixed Snapshot/Agent structs, so it drops
// each of them on every rewrite.
var (
	snapshotMembersAfter051 = []string{"holderId", "holderSeq"}
	agentMembersAfter051    = []string{"endUnknown", "contextWindow", "contextCompaction", "compacting", "compactions", "compactOrigin"}
)

// rewriteAsV0510 replaces the recovery file at path as a v0.51.0 daemon's
// reattach does: its writer seeds only the done and failed rows of the file
// it found (wslive.State.SeedFinished, f800ba30d:internal/wslive/state.go
// 1037-1064: a running row gets no end, so it is skipped), and writes the
// snapshot through its fixed structs (wsagent/record.go 625-665), which
// carry none of the members added since.
func rewriteAsV0510(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	for _, name := range snapshotMembersAfter051 {
		delete(members, name)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(members["agents"], &rows); err != nil && members["agents"] != nil {
		t.Fatal(err)
	}
	kept := []map[string]json.RawMessage{}
	for _, row := range rows {
		var status string
		_ = json.Unmarshal(row["status"], &status)
		if status != wslive.StatusDone && status != wslive.StatusFailed {
			continue
		}
		for _, name := range agentMembersAfter051 {
			delete(row, name)
		}
		kept = append(kept, row)
	}
	if members["agents"], err = json.Marshal(kept); err != nil {
		t.Fatal(err)
	}
	if raw, err = json.Marshal(members); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestNativeRosterSurvivesAnOldSnapshotRewrite: a current daemon leaves a
// recovery file listing a running agy native row, with holder coverage
// through 20. A v0.51.0 daemon rewrites it: the running row and the
// coverage are gone. The current daemon's rejoin then replays the holder's
// retained records: the running row (started at or below the lost coverage)
// and a launch after the last checkpoint (never in the file) both come
// back, and retention still bounds what the replay rebuilt. Without an older
// writer in between, a covered row the file lacks stays dropped.
func TestNativeRosterSurvivesAnOldSnapshotRewrite(t *testing.T) {
	for _, oldWriter := range []bool{false, true} {
		name := "current writer only"
		if oldWriter {
			name = "after a v0.51.0 rewrite"
		}
		t.Run(name, func(t *testing.T) {
			c, holder := agyReplayConv(t)
			running, launched, evicted := wslive.NativeAgyPrefix+"running", wslive.NativeAgyPrefix+"launched", wslive.NativeAgyPrefix+"evicted"
			path := wslive.SnapshotPath(c.live.dir)
			c.do(func() {
				c.mu.Lock()
				c.rec.HolderID = holder
				c.mu.Unlock()
				c.seq = 20
				c.live.applyRAM(*nativeRowEvent(running, "research", wslive.StatusRunning).Live)
				c.live.forceSnapshot()
				if oldWriter {
					rewriteAsV0510(t, path)
				}

				// The next daemon: a fresh writer over the same directory.
				fresh := newLiveWriter(c.live.dir, c.rec.WSKey, c.rec.Backend, c.r.opts.Now, io.Discard)
				c.live.st, c.live.restored, c.live.extraDisk = fresh.st, fresh.restored, fresh.extraDisk
				c.live.diskHold = false
				c.replaying = true
				c.handle(seqEvent(nativeRowEvent(evicted, "old", wslive.StatusRunning), 5, true))
				c.handle(seqEvent(nativeRowEvent(evicted, "", wslive.StatusFailed), 6, true))
				c.handle(seqEvent(nativeRowEvent(running, "research", wslive.StatusRunning), 10, true))
				c.handle(seqEvent(nativeRowEvent(launched, "research", wslive.StatusRunning), 22, true))
				before := c.r.opts.Now().UnixMilli()
				c.handle(Event{ReplayDone: true})
				end := c.r.opts.Now().UnixMilli()
				snap := c.live.st.Snapshot(end)
				for _, id := range []string{running, launched} {
					a, ok := snapAgent(snap, id)
					if !ok || a.Status != wslive.StatusDone || !a.EndUnknown || a.EndedAt < before || a.EndedAt > end {
						t.Errorf("row %s = %+v (present %v), want end-unknown at the replay end (%d..%d)", id, a, ok, before, end)
					}
				}
				// A covered start the file does not list: dropped as evicted
				// while a current writer's coverage says so; with an older
				// writer's rewrite in between it says nothing, so the row is
				// rebuilt from the retained records.
				if a, ok := snapAgent(snap, evicted); ok != oldWriter {
					t.Errorf("covered unlisted row = %+v (present %v), want present %v", a, ok, oldWriter)
				}
			})
		})
	}
}

// TestNativeRosterRebuildStaysBoundedAfterAnOldRewrite: with the coverage
// erased, a replay of more finished rows than retention keeps still ends at
// the finished cap.
func TestNativeRosterRebuildStaysBoundedAfterAnOldRewrite(t *testing.T) {
	c, holder := agyReplayConv(t)
	path := wslive.SnapshotPath(c.live.dir)
	c.do(func() {
		c.mu.Lock()
		c.rec.HolderID = holder
		c.mu.Unlock()
		c.seq = 40
		c.live.forceSnapshot()
		rewriteAsV0510(t, path)
		fresh := newLiveWriter(c.live.dir, c.rec.WSKey, c.rec.Backend, c.r.opts.Now, io.Discard)
		c.live.st, c.live.restored, c.live.extraDisk = fresh.st, fresh.restored, fresh.extraDisk
		c.replaying = true
		total := wslive.FinishedAgentCap + 6
		for i := 0; i < total; i++ {
			id := wslive.NativeAgyPrefix + "row-" + time.Duration(i).String()
			c.handle(seqEvent(nativeRowEvent(id, "research", wslive.StatusRunning), uint64(2+2*i), true))
			c.handle(seqEvent(nativeRowEvent(id, "", wslive.StatusFailed), uint64(3+2*i), true))
		}
		c.handle(Event{ReplayDone: true})
		finished := 0
		for _, a := range c.live.st.Snapshot(c.r.opts.Now().UnixMilli()).Agents {
			if a.Status != wslive.StatusRunning {
				finished++
			}
		}
		if finished != wslive.FinishedAgentCap {
			t.Errorf("finished rows after the rebuild = %d, want the cap %d", finished, wslive.FinishedAgentCap)
		}
	})
}

// TestSnapshotExtraMarksCoverageOnce: a writer that records holder coverage
// writes the extra file ahead of its first recovery snapshot and never again
// while its content stays the same; a file of the same shape without the
// extra file (a v0.51.0 session upgrading) stays legacy: its unlisted rows
// still read as evicted.
func TestSnapshotExtraMarksCoverageOnce(t *testing.T) {
	c, holder := agyReplayConv(t)
	var writes []string
	c.do(func() {
		c.mu.Lock()
		c.rec.HolderID = holder
		c.mu.Unlock()
		// The conversation's own writes already left the file: start over.
		if err := os.Remove(wslive.SnapshotExtraPath(c.live.dir)); err != nil {
			t.Error(err)
		}
		c.live.extraDisk = nil
		inner := c.live.writeSnapshotFile
		c.live.writeSnapshotFile = func(path string, raw []byte, mode os.FileMode) error {
			writes = append(writes, filepath.Base(path))
			return inner(path, raw, mode)
		}
		c.live.forceSnapshot()
		c.live.forceSnapshot()
	})
	want := []string{wslive.SnapshotExtraName, wslive.SnapshotName, wslive.SnapshotName}
	if !slices.Equal(writes, want) {
		t.Fatalf("writes = %v, want %v", writes, want)
	}
	dir := c.live.dir
	if extra, found := wslive.ReadSnapshotExtra(dir); !found || !extra.Coverage {
		t.Fatalf("extra file = %+v (found %v), want coverage", extra, found)
	}
	rewriteAsV0510(t, wslive.SnapshotPath(dir))
	if r := newLiveWriter(dir, c.rec.WSKey, c.rec.Backend, c.r.opts.Now, io.Discard).takeRestored(); !r.erased || !r.readable {
		t.Fatalf("after an old rewrite restored = %+v, want erased", r)
	}
	if err := os.Remove(wslive.SnapshotExtraPath(dir)); err != nil {
		t.Fatal(err)
	}
	r := newLiveWriter(dir, c.rec.WSKey, c.rec.Backend, c.r.opts.Now, io.Discard).takeRestored()
	if r.erased || !r.evicted(wslive.NativeAgyPrefix+"x", holder, 1) {
		t.Fatalf("a legacy file with no extra file restored = %+v, want the legacy eviction", r)
	}
}

// TestFinishedRowScaleSurvivesAnOldSnapshotRewrite: an unlinked finished
// hosted child (no session of its own to read the scale from, and no later
// report: it finished) keeps its context window, compaction thresholds and
// compaction history through a v0.51.0 rewrite of the recovery file, which
// seeds the row without them. The current reopen takes them back from the
// extra file; a row that is still running is republished by its runner and
// is not kept there.
func TestFinishedRowScaleSurvivesAnOldSnapshotRewrite(t *testing.T) {
	for _, oldWriter := range []bool{false, true} {
		name := "current writer only"
		if oldWriter {
			name = "after a v0.51.0 rewrite"
		}
		t.Run(name, func(t *testing.T) {
			c, _ := agyReplayConv(t)
			path := wslive.SnapshotPath(c.live.dir)
			scale := &wslive.UsageCompaction{Soft: 150_000, Hard: 180_000, Milestones: []int64{120_000}, AdvisoryEnabled: true}
			window, tokens := float64(200_000), float64(90_000)
			failed, done, live := "pi:failed-child", "pi:done-child", "pi:live-child"
			c.do(func() {
				for _, id := range []string{failed, done, live} {
					c.live.apply(wslive.Event{Kind: wslive.KindAgent, AgentID: id, AgentKind: wslive.AgentKindPi, Label: id, Status: wslive.StatusRunning,
						Model: "m", ContextTokens: &tokens, ContextWindow: &window, ContextCompaction: scale, Compactions: 2, CompactOrigin: wslive.CompactOriginAuto})
				}
				c.live.apply(wslive.Event{Kind: wslive.KindAgent, AgentID: failed, AgentKind: wslive.AgentKindPi, Status: wslive.StatusFailed, Compactions: 2, CompactOrigin: wslive.CompactOriginAuto})
				c.live.apply(wslive.Event{Kind: wslive.KindAgent, AgentID: done, AgentKind: wslive.AgentKindPi, Status: wslive.StatusDone, Compactions: 2, CompactOrigin: wslive.CompactOriginAuto})
				c.live.forceSnapshot()
				if oldWriter {
					rewriteAsV0510(t, path)
				}
				fresh := newLiveWriter(c.live.dir, c.rec.WSKey, c.rec.Backend, c.r.opts.Now, io.Discard)
				snap := fresh.st.Snapshot(c.r.opts.Now().UnixMilli())
				for _, id := range []string{failed, done} {
					a, ok := snapAgent(snap, id)
					if !ok {
						t.Errorf("finished row %s was not restored", id)
						continue
					}
					if a.ContextTokens != 90_000 || a.ContextWindow != 200_000 || !reflect.DeepEqual(a.ContextCompaction, scale) || a.Compactions != 2 || a.CompactOrigin != wslive.CompactOriginAuto {
						t.Errorf("finished row %s = tokens %d window %d compaction %+v compactions %d/%q, want its scale kept", id, a.ContextTokens, a.ContextWindow, a.ContextCompaction, a.Compactions, a.CompactOrigin)
					}
				}
				if _, ok := snapAgent(snap, live); ok {
					t.Error("a running row was seeded; no end would come for it")
				}
				if extra, _ := wslive.ReadSnapshotExtra(c.live.dir); len(extra.Agents) != 2 || extra.Agents[live].ContextWindow != 0 {
					t.Errorf("extra file scales = %+v, want the two finished rows only", extra.Agents)
				}
			})
		})
	}
}

// TestSnapshotExtraScaleWritesOnlyOnFinishedChange: a running row's reports
// never rewrite the extra file; its finish does once, and an unchanged
// roster after that writes nothing more.
func TestSnapshotExtraScaleWritesOnlyOnFinishedChange(t *testing.T) {
	c, _ := agyReplayConv(t)
	window, tokens, more := float64(200_000), float64(1_000), float64(2_000)
	id := "pi:cadence"
	c.do(func() {
		c.live.forceSnapshot() // settle the coverage mark
		extraWrites := 0
		inner := c.live.writeSnapshotFile
		c.live.writeSnapshotFile = func(path string, raw []byte, mode os.FileMode) error {
			if filepath.Base(path) == wslive.SnapshotExtraName {
				extraWrites++
			}
			return inner(path, raw, mode)
		}
		agent := func(status string, tok *float64) {
			c.live.apply(wslive.Event{Kind: wslive.KindAgent, AgentID: id, AgentKind: wslive.AgentKindPi, Label: "c", Status: status,
				ContextTokens: tok, ContextWindow: &window, ContextCompaction: &wslive.UsageCompaction{Soft: 1, Hard: 2}})
			c.live.forceSnapshot()
		}
		agent(wslive.StatusRunning, &tokens)
		agent(wslive.StatusRunning, &more)
		if extraWrites != 0 {
			t.Errorf("a running row wrote the extra file %d times", extraWrites)
		}
		agent(wslive.StatusDone, &more)
		agent(wslive.StatusDone, &more)
		c.live.forceSnapshot()
		if extraWrites != 1 {
			t.Errorf("extra file writes after the finish = %d, want 1", extraWrites)
		}
	})
}
