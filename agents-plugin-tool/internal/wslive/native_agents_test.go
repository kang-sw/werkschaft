package wslive

import (
	"encoding/json"
	"testing"
)

func nativeRow(t *testing.T, snap Snapshot, id string) (Agent, bool) {
	t.Helper()
	for _, a := range snap.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// TestNativeEndUnknownSettlesWithoutEndTime: an end-unknown terminal event
// ends a running native row without an end time and without adding the
// unobserved span to ActiveMs; the wire never carries the flag.
func TestNativeEndUnknownSettlesWithoutEndTime(t *testing.T) {
	st := NewState(ownerKey)
	id := NativeAgyPrefix + "call_1"
	mustApply(t, st, Event{Kind: KindAgent, AgentID: id, Label: "Kiwi Responder", Status: StatusRunning}, 1_000)
	mustApply(t, st, Event{Kind: KindAgent, AgentID: id, Status: StatusDone, EndUnknown: true}, 9_000)
	a, ok := nativeRow(t, st.Snapshot(9_500), id)
	if !ok {
		t.Fatal("native row missing")
	}
	if a.Status != StatusDone || a.EndedAt != 0 || a.ActiveMs != 0 || a.TurnStartedAt != 0 || a.StartedAt != 1_000 {
		t.Fatalf("end-unknown row = %+v, want done, no end, no active time, started 1000", a)
	}
	if a.Phase != PhaseIdle || a.PhaseStartedAt != 9_000 {
		t.Fatalf("end-unknown row phase = %q since %d, want idle since the settlement 9000", a.Phase, a.PhaseStartedAt)
	}
	raw, err := json.Marshal(Event{Kind: KindAgent, AgentID: id, Status: StatusDone, EndUnknown: true})
	if err != nil {
		t.Fatal(err)
	}
	var back Event
	if err := json.Unmarshal([]byte(`{"kind":"agent","agentId":"x","status":"done","EndUnknown":true,"endUnknown":true}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.EndUnknown || string(raw) != `{"kind":"agent","agentId":"agy-native:call_1","status":"done"}` {
		t.Fatalf("EndUnknown crossed the wire: marshal %s, unmarshal %v", raw, back.EndUnknown)
	}
}

// TestNativeRowsAreMonotonic: a terminal native row ignores a later running
// and a later terminal event, so its status, end time, and ActiveMs stand;
// an ordinary subagent row still reopens on running as before.
func TestNativeRowsAreMonotonic(t *testing.T) {
	st := NewState(ownerKey)
	codex := NativeCodexPrefix + "call_a"
	mustApply(t, st, Event{Kind: KindAgent, AgentID: codex, Label: "ok_reply", Status: StatusRunning, At: 1_000}, 1_000)
	mustApply(t, st, Event{Kind: KindAgent, AgentID: codex, Status: StatusDone, At: 4_000}, 4_000)
	before, _ := nativeRow(t, st.Snapshot(5_000), codex)
	if before.EndedAt != 4_000 || before.ActiveMs != 3_000 {
		t.Fatalf("codex row = %+v, want ended 4000 with 3000 active", before)
	}
	if res := mustApply(t, st, Event{Kind: KindAgent, AgentID: codex, Status: StatusRunning, At: 6_000}, 6_000); res.Changed {
		t.Fatal("a running event for a terminal native row reported a change")
	}
	if res := mustApply(t, st, Event{Kind: KindAgent, AgentID: codex, Status: StatusFailed, At: 7_000}, 7_000); res.Changed {
		t.Fatal("a second terminal event for a native row reported a change")
	}
	after, _ := nativeRow(t, st.Snapshot(8_000), codex)
	if after.Status != StatusDone || after.EndedAt != 4_000 || after.ActiveMs != 3_000 {
		t.Fatalf("codex row after later events = %+v, want unchanged", after)
	}

	mustApply(t, st, Event{Kind: KindAgent, AgentID: "plain", Status: StatusRunning}, 1_000)
	mustApply(t, st, Event{Kind: KindAgent, AgentID: "plain", Status: StatusDone}, 2_000)
	mustApply(t, st, Event{Kind: KindAgent, AgentID: "plain", Status: StatusRunning}, 3_000)
	if a, _ := nativeRow(t, st.Snapshot(3_000), "plain"); a.Status != StatusRunning {
		t.Fatalf("ordinary row = %+v, want reopened running", a)
	}
}

// TestNativeRowsSurviveSeedAndReplay: a finished native row restored from a
// recovery snapshot, then the replayed events that created and ended it,
// keeps its status, end time (or its absence), and ActiveMs.
func TestNativeRowsSurviveSeedAndReplay(t *testing.T) {
	codex, agy := NativeCodexPrefix+"call_a", NativeAgyPrefix+"call_b"
	replay := func(st *State, now int64) {
		mustApply(t, st, Event{Kind: KindAgent, AgentID: codex, Label: "ok_reply", Status: StatusRunning, At: 1_000}, now)
		mustApply(t, st, Event{Kind: KindAgent, AgentID: codex, Status: StatusDone, At: 4_000}, now)
		mustApply(t, st, Event{Kind: KindAgent, AgentID: agy, Label: "Kiwi Responder", Status: StatusRunning}, now)
		mustApply(t, st, Event{Kind: KindAgent, AgentID: agy, Status: StatusDone, EndUnknown: true}, now)
	}
	first := NewState(ownerKey)
	mustApply(t, first, Event{Kind: KindAgent, AgentID: codex, Label: "ok_reply", Status: StatusRunning, At: 1_000}, 1_000)
	mustApply(t, first, Event{Kind: KindAgent, AgentID: codex, Status: StatusDone, At: 4_000}, 4_000)
	mustApply(t, first, Event{Kind: KindAgent, AgentID: agy, Label: "Kiwi Responder", Status: StatusRunning}, 2_000)
	mustApply(t, first, Event{Kind: KindAgent, AgentID: agy, Status: StatusDone, EndUnknown: true}, 6_000)
	disk := first.Snapshot(7_000).ForDisk()
	raw, err := json.Marshal(disk)
	if err != nil {
		t.Fatal(err)
	}
	var restored Snapshot
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	second := NewState(ownerKey)
	second.SeedFinished(restored)
	replay(second, 50_000)
	for _, id := range []string{codex, agy} {
		want, _ := nativeRow(t, disk, id)
		got, ok := nativeRow(t, second.Snapshot(51_000), id)
		if !ok {
			t.Fatalf("%s lost across seed and replay", id)
		}
		if got.Status != want.Status || got.EndedAt != want.EndedAt || got.ActiveMs != want.ActiveMs || got.StartedAt != want.StartedAt {
			t.Fatalf("%s after replay = %+v, want %+v", id, got, want)
		}
	}
	if a, _ := nativeRow(t, second.Snapshot(51_000), agy); a.EndedAt != 0 {
		t.Fatalf("end-unknown row gained an end time %d", a.EndedAt)
	}
}

// TestEndUnknownRowSurvivesFullFinishedCap: with the finished cap full of
// rows that ended after the agy row started, the newly settled end-unknown
// row ranks newest, at settlement and after a restore.
func TestEndUnknownRowSurvivesFullFinishedCap(t *testing.T) {
	st := NewState(ownerKey)
	agy := NativeAgyPrefix + "call_z"
	mustApply(t, st, Event{Kind: KindAgent, AgentID: agy, Label: "research", Status: StatusRunning}, 1_000)
	for i := 0; i < FinishedAgentCap; i++ {
		id := "done-" + string(rune('a'+i))
		mustApply(t, st, Event{Kind: KindAgent, AgentID: id, Status: StatusRunning}, int64(1_100+i))
		mustApply(t, st, Event{Kind: KindAgent, AgentID: id, Status: StatusDone}, int64(2_000+i*100))
	}
	mustApply(t, st, Event{Kind: KindAgent, AgentID: agy, Status: StatusDone, EndUnknown: true}, 9_000)
	snap := st.Snapshot(9_500)
	if len(snap.Agents) != FinishedAgentCap {
		t.Fatalf("kept %d finished rows, want %d", len(snap.Agents), FinishedAgentCap)
	}
	if _, ok := nativeRow(t, snap, agy); !ok {
		t.Fatal("end-unknown row dropped at settlement")
	}
	if _, ok := nativeRow(t, snap, "done-a"); ok {
		t.Fatal("the oldest finished row survived instead of yielding to the newer end-unknown row")
	}
	raw, err := json.Marshal(snap.ForDisk())
	if err != nil {
		t.Fatal(err)
	}
	var restored Snapshot
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	next := NewState(ownerKey)
	next.SeedFinished(restored)
	// A later ordinary finish pushes the oldest out, never the end-unknown row.
	mustApply(t, next, Event{Kind: KindAgent, AgentID: "late", Status: StatusRunning}, 9_600)
	mustApply(t, next, Event{Kind: KindAgent, AgentID: "late", Status: StatusDone}, 9_700)
	after := next.Snapshot(9_800)
	if _, ok := nativeRow(t, after, agy); !ok {
		t.Fatal("end-unknown row dropped after restore")
	}
	if _, ok := nativeRow(t, after, "done-b"); ok {
		t.Fatal("after restore the oldest ended row survived instead of yielding")
	}
}

func TestRunningAgentsByPrefix(t *testing.T) {
	st := NewState(ownerKey)
	for _, id := range []string{NativeAgyPrefix + "b", NativeAgyPrefix + "a", NativeCodexPrefix + "c", "plain"} {
		mustApply(t, st, Event{Kind: KindAgent, AgentID: id, Status: StatusRunning}, 1_000)
	}
	mustApply(t, st, Event{Kind: KindAgent, AgentID: NativeAgyPrefix + "b", Status: StatusDone}, 2_000)
	got := st.RunningAgents(NativeAgyPrefix)
	if len(got) != 1 || got[0] != NativeAgyPrefix+"a" {
		t.Fatalf("RunningAgents = %v", got)
	}
}
