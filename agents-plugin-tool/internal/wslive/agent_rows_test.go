package wslive

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// rowsOf projects a snapshot's agents onto AgentRow, sorted by id.
func rowsOf(agents []Agent) []AgentRow {
	out := make([]AgentRow, 0, len(agents))
	for _, a := range agents {
		out = append(out, AgentRow{ID: a.ID, ReplyID: a.ReplyID, AgentKind: a.AgentKind, Status: a.Status, Compactions: a.Compactions, CompactOrigin: a.CompactOrigin})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedRows(rows []AgentRow) []AgentRow {
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func runningNativeOf(snap Snapshot) int {
	n := 0
	for _, a := range snap.Agents {
		if a.Status == StatusRunning && a.AgentKind == "" {
			n++
		}
	}
	return n
}

// TestAgentRowsMatchSnapshot pins the lean row query to what a whole
// snapshot lists, including a native row merged into its pi row, which both
// leave out, so the running-native count and spawn-row seeding read the same
// roster they read through Snapshot before.
func TestAgentRowsMatchSnapshot(t *testing.T) {
	st := NewState("test-session")
	check := func(step string) {
		t.Helper()
		snap := st.Snapshot(10_000)
		if got, want := sortedRows(st.AgentRows()), rowsOf(snap.Agents); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: rows %+v, snapshot %+v", step, got, want)
		}
		if got, want := st.RunningNativeAgents(), runningNativeOf(snap); got != want {
			t.Fatalf("%s: running native %d, snapshot %d", step, got, want)
		}
	}
	check("empty")
	apply := func(ev Event) {
		t.Helper()
		if _, err := st.Apply(ev, 1000); err != nil {
			t.Fatal(err)
		}
	}
	apply(Event{Kind: KindAgent, AgentID: "native-a", Status: StatusRunning})
	apply(Event{Kind: KindAgent, AgentID: "native-b", Status: StatusRunning})
	apply(Event{Kind: KindAgent, AgentID: "native-done", Status: StatusRunning})
	apply(Event{Kind: KindAgent, AgentID: "native-done", Status: StatusDone})
	check("native only")
	if st.RunningNativeAgents() != 2 {
		t.Fatalf("running native = %d, want 2", st.RunningNativeAgents())
	}
	// A pi row and the native row it absorbs (same native id behind pi:).
	apply(Event{Kind: KindAgent, AgentID: "pi:native-b", AgentKind: AgentKindPi, Status: StatusRunning, Compactions: 3, CompactOrigin: CompactOriginAuto})
	apply(Event{Kind: KindAgent, AgentID: "pi:other", AgentKind: AgentKindPi, Status: StatusDone})
	check("merged alias")
	if st.RunningNativeAgents() != 1 {
		t.Fatalf("a native row merged into its pi row still counted: %d", st.RunningNativeAgents())
	}
	ev := observedEvent("o1", "provider/child", 900)
	ev.Kind, ev.AgentID = KindAgent, "native-a"
	apply(ev)
	check("with evidence")
}

// TestSnapshotSharesObservedModels shows Snapshot no longer deep-copies the
// observed models: consecutive snapshots of an unchanged state hand out the
// same read-only models, and the lean row query allocates less than a
// snapshot does.
func TestSnapshotSharesObservedModels(t *testing.T) {
	st := NewState("test-session")
	for i := range 20 {
		_, _ = st.Apply(observedEvent(fmt.Sprintf("lead-%d", i), fmt.Sprintf("m%d", i%3), int64(100+i)), 1000)
		ev := observedEvent(fmt.Sprintf("child-%d", i), fmt.Sprintf("c%d", i%2), int64(100+i))
		ev.Kind, ev.AgentID, ev.Status = KindAgent, "child", StatusRunning
		_, _ = st.Apply(ev, 1000)
	}
	a, b := st.Snapshot(2000), st.Snapshot(3000)
	if a.Lead.Observed == nil || a.Lead.Observed != b.Lead.Observed {
		t.Fatal("lead model cloned per snapshot")
	}
	if len(a.Agents) != 1 || a.Agents[0].Observed == nil || a.Agents[0].Observed != b.Agents[0].Observed || a.ObservedAgents["child"] != b.ObservedAgents["child"] {
		t.Fatal("agent model cloned per snapshot")
	}
	snapAllocs := testing.AllocsPerRun(50, func() { _ = st.Snapshot(4000) })
	rowAllocs := testing.AllocsPerRun(50, func() { _ = st.RunningNativeAgents() })
	if rowAllocs >= snapAllocs {
		t.Fatalf("RunningNativeAgents allocs %.0f, Snapshot %.0f", rowAllocs, snapAllocs)
	}
}

// TestSnapshotObservedSharingRace runs readers of published snapshots
// against a writer that keeps adding evidence. The writer owns the state
// (State is not safe for concurrent use) and hands each snapshot off; the
// readers walk its shared models with no lock. Under -race a write into a
// published model would be reported, and each snapshot's models must read
// back exactly as they were when it was taken.
func TestSnapshotObservedSharingRace(t *testing.T) {
	st := NewState("test-session")
	type taken struct {
		snap Snapshot
		raw  []byte
	}
	snaps := make(chan taken, 64)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range snaps {
				for range 3 {
					raw, err := json.Marshal(observedView(s.snap))
					if err != nil {
						errs <- err
						return
					}
					if string(raw) != string(s.raw) {
						errs <- fmt.Errorf("published model changed after its snapshot:\n%s\n%s", s.raw, raw)
						return
					}
					if o := s.snap.Lead.Observed; o != nil {
						_ = append(o.History, ModelChange{Model: "reader-append"})
					}
				}
			}
		}()
	}
	for i := range 400 {
		ev := observedEvent(fmt.Sprintf("lead-%d", i), fmt.Sprintf("m%d", i%4), int64(100+i))
		rate := float64(i%50 + 1)
		ev.Observed.OutputTps = &rate
		_, _ = st.Apply(ev, int64(1000+i))
		child := observedEvent(fmt.Sprintf("child-%d", i), fmt.Sprintf("c%d", i%3), int64(100+i))
		child.Kind, child.AgentID, child.Status = KindAgent, fmt.Sprintf("child-%d", i%5), StatusRunning
		_, _ = st.Apply(child, int64(1000+i))
		snap := st.Snapshot(int64(2000 + i))
		raw, err := json.Marshal(observedView(snap))
		if err != nil {
			t.Fatal(err)
		}
		snaps <- taken{snap: snap, raw: raw}
	}
	close(snaps)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// observedView is every observed model a snapshot carries.
func observedView(s Snapshot) any {
	agents := map[string]*ObservedModel{}
	for _, a := range s.Agents {
		agents[a.ID] = a.Observed
	}
	return map[string]any{"lead": s.Lead.Observed, "agents": agents, "byId": s.ObservedAgents}
}
