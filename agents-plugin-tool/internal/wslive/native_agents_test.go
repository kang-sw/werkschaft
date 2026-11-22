package wslive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// TestNativeEndUnknownSettlesAtObservationEnd: an end-unknown terminal event
// ends a running native row at the observation's end, flagged endUnknown on
// the snapshot row, without adding the unobserved span to ActiveMs; the
// event flag itself never crosses the wire.
func TestNativeEndUnknownSettlesAtObservationEnd(t *testing.T) {
	st := NewState(ownerKey)
	id := NativeAgyPrefix + "call_1"
	mustApply(t, st, Event{Kind: KindAgent, AgentID: id, Label: "Kiwi Responder", Status: StatusRunning}, 1_000)
	mustApply(t, st, Event{Kind: KindAgent, AgentID: id, Status: StatusDone, EndUnknown: true}, 9_000)
	a, ok := nativeRow(t, st.Snapshot(9_500), id)
	if !ok {
		t.Fatal("native row missing")
	}
	if a.Status != StatusDone || !a.EndUnknown || a.EndedAt != 9_000 || a.ActiveMs != 0 || a.TurnStartedAt != 0 || a.StartedAt != 1_000 {
		t.Fatalf("end-unknown row = %+v, want done, flagged, ended at the observation end 9000, no active time, started 1000", a)
	}
	if row, err := json.Marshal(a); err != nil || !strings.Contains(string(row), `"endedAt":9000,"endUnknown":true`) {
		t.Fatalf("snapshot row JSON = %s, %v", row, err)
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
// keeps its status, end time, end-unknown flag, and ActiveMs.
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
		if got.Status != want.Status || got.EndedAt != want.EndedAt || got.EndUnknown != want.EndUnknown || got.ActiveMs != want.ActiveMs || got.StartedAt != want.StartedAt {
			t.Fatalf("%s after replay = %+v, want %+v", id, got, want)
		}
	}
	if a, _ := nativeRow(t, second.Snapshot(51_000), agy); a.EndedAt != 6_000 || !a.EndUnknown {
		t.Fatalf("end-unknown row after replay = %+v, want ended 6000 and flagged", a)
	}
}

// TestEndUnknownRowSurvivesFullFinishedCap: with the finished cap full of
// rows that ended after the agy row started, the newly settled end-unknown
// row ranks by its observation end, newest, at settlement and after a
// restore.
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

// TestEndUnknownRowSurvivesAnOlderWriter pins the T1 downgrade path: an
// older writer knows no endUnknown and ranks finished rows by EndedAt alone
// (7482aa91e state.go:1013), then replaces the cache (record.go:685) without
// the flag. The end-unknown row's real EndedAt keeps it ahead of older
// completions, so a new finish under that writer pushes out the oldest
// completion, never the newer end-unknown row. After an upgrade the reader
// recovers the classification from the invariant (a done agy native row
// ended with its observation), both on seeding and on a file read.
func TestEndUnknownRowSurvivesAnOlderWriter(t *testing.T) {
	st := NewState(ownerKey)
	agy := NativeAgyPrefix + "call_t1"
	for i := 0; i < FinishedAgentCap-1; i++ {
		id := "known-" + string(rune('a'+i))
		mustApply(t, st, Event{Kind: KindAgent, AgentID: id, Status: StatusRunning}, int64(1_000+i))
		mustApply(t, st, Event{Kind: KindAgent, AgentID: id, Status: StatusDone}, int64(2_000+i*100))
	}
	mustApply(t, st, Event{Kind: KindAgent, AgentID: agy, Status: StatusRunning}, 3_000)
	mustApply(t, st, Event{Kind: KindAgent, AgentID: agy, Status: StatusDone, EndUnknown: true}, 5_000)
	raw, err := json.Marshal(st.Snapshot(5_500).ForDisk())
	if err != nil {
		t.Fatal(err)
	}
	// The older writer's struct has no endUnknown member: it decodes the
	// file without the flag and writes it back without it.
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	for _, a := range generic["agents"].([]any) {
		delete(a.(map[string]any), "endUnknown")
	}
	if raw, err = json.Marshal(generic); err != nil {
		t.Fatal(err)
	}
	var old Snapshot
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	older := NewState(ownerKey)
	older.SeedFinished(old)
	mustApply(t, older, Event{Kind: KindAgent, AgentID: "late", Status: StatusRunning}, 6_000)
	mustApply(t, older, Event{Kind: KindAgent, AgentID: "late", Status: StatusDone}, 7_000)
	after := older.Snapshot(7_500)
	a, ok := nativeRow(t, after, agy)
	if !ok {
		t.Fatal("the older writer dropped the newer end-unknown row")
	}
	if a.EndedAt != 5_000 || a.Status != StatusDone {
		t.Fatalf("row after the older writer = %+v, want done at 5000", a)
	}
	if _, ok := nativeRow(t, after, "known-a"); ok {
		t.Fatal("the oldest completion survived instead of yielding")
	}
	// The older writer's file: the flag is gone again.
	if raw, err = json.Marshal(after.ForDisk()); err != nil {
		t.Fatal(err)
	}
	generic = nil
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	for _, a := range generic["agents"].([]any) {
		delete(a.(map[string]any), "endUnknown")
	}
	if raw, err = json.Marshal(generic); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "endUnknown") {
		t.Fatal("the older writer's file still carries the flag")
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := nativeRow(t, read, agy); !ok || !a.EndUnknown || a.EndedAt != 5_000 {
		t.Fatalf("row read after the upgrade = %+v, want end-unknown at 5000", a)
	}
	if a, ok := nativeRow(t, read, "late"); !ok || a.EndUnknown {
		t.Fatalf("an ordinary completion read as end-unknown: %+v", a)
	}
	var flagless Snapshot
	if err := json.Unmarshal(raw, &flagless); err != nil {
		t.Fatal(err)
	}
	upgraded := NewState(ownerKey)
	upgraded.SeedFinished(flagless)
	if a, ok := nativeRow(t, upgraded.Snapshot(8_000), agy); !ok || !a.EndUnknown || a.EndedAt != 5_000 {
		t.Fatalf("row seeded after the upgrade = %+v, want end-unknown at 5000", a)
	}
}

// TestHoldRetentionAppliesCapsOnceToTheFoldedRoster: while retention is
// held no cap applies, so a row an intermediate running cap would evict
// survives until the release, which retains once over the final roster.
func TestHoldRetentionAppliesCapsOnceToTheFoldedRoster(t *testing.T) {
	st := NewState("key")
	id := func(i int) string { return NativeAgyPrefix + "held-" + strconv.Itoa(i) }
	release := st.HoldRetention()
	for i := 0; i <= RunningAgentCap; i++ {
		if _, err := st.Apply(Event{Kind: KindAgent, AgentID: id(i), Status: StatusRunning, At: int64(1_000 + i)}, int64(1_000+i)); err != nil {
			t.Fatal(err)
		}
	}
	if !st.HasAgent(id(0)) {
		t.Fatal("a held state applied the running cap")
	}
	for i := 1; i <= RunningAgentCap; i++ {
		if _, err := st.Apply(Event{Kind: KindAgent, AgentID: id(i), Status: StatusFailed, At: int64(10_000 + i)}, int64(10_000+i)); err != nil {
			t.Fatal(err)
		}
	}
	st.HoldRetention()() // nested: no effect while held
	if n := len(st.Snapshot(20_000).Agents); n != RunningAgentCap+1 {
		t.Fatalf("held roster = %d rows, want %d", n, RunningAgentCap+1)
	}
	release()
	release()
	snap := st.Snapshot(20_000)
	if len(snap.Agents) != 1+FinishedAgentCap {
		t.Fatalf("released roster = %d rows, want 1 running + %d finished", len(snap.Agents), FinishedAgentCap)
	}
	if a, ok := nativeRow(t, snap, id(0)); !ok || a.Status != StatusRunning {
		t.Fatalf("first row = %+v (present %v), want still running", a, ok)
	}
}
