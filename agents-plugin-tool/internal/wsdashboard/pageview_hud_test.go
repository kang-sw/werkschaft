// Session HUD tests cover handoff and agent projection.

package wsdashboard

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
)

// TestHandedOffSessionMark: the page of a lead session the daemon handed to
// a terminal carries the handed mark on <html> (the page script then reads
// the resume command), only while no terminal runs the session and the
// daemon does not host it again.
func TestHandedOffSessionMark(t *testing.T) {
	now := testNow
	ended := liveSession("handed", "/a", now)
	ended.EndedAt = stampFor(now)
	ended.Live = false
	resumed := liveSession("resumed", "/a", now)
	retaken := ended
	retaken.ReplyID, retaken.Hosted = "retaken", true
	mark := func(s Session, handedOff bool) bool {
		t.Helper()
		v := buildSessionView(pageInputs{Session: s, Now: now, HandedOff: handedOff, Lookup: lookupOf(nil, nil)})
		regions, err := renderSessionRegions(v, nil)
		if err != nil {
			t.Fatal(err)
		}
		html, err := renderSessionPage(v, regions, true, "")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(html, ` data-handed="1"`); got != v.HandedOff {
			t.Errorf("%s: page mark = %v, view = %v", s.ReplyID, got, v.HandedOff)
		}
		return v.HandedOff
	}
	if !mark(ended, true) {
		t.Error("a handed-off session no terminal runs lacks the mark")
	}
	if mark(ended, false) || mark(resumed, true) || mark(retaken, true) {
		t.Error("the mark shows without a handed-off record, on a session a terminal runs, or on a hosted one")
	}
}

func TestHUDProjectionStatePrecedenceVisibilityAndHarness(t *testing.T) {
	now := testNow
	leadSess := liveSession("self", "/p", now)
	leadSess.Harness = "claude"
	snap := liveSnap(now, wslive.Lead{},
		// Permission overrides failed/running.
		wslive.Agent{ID: "perm", Label: "perm-node", Status: wslive.StatusFailed, Harness: "agy",
			Activity: wslive.Activity{Permission: &wslive.Permission{ToolName: "bash", ArgsPreview: "rm -rf"}}},
		// Failed overrides holding/alive.
		wslive.Agent{ID: "fail", Status: wslive.StatusFailed, Holding: true, Alive: true, Harness: "codex"},
		// PhaseReplying and PhaseTool both map to tool.
		wslive.Agent{ID: "reply", Status: wslive.StatusRunning, Activity: wslive.Activity{Phase: wslive.PhaseReplying}},
		// Thinking inherits lead harness ("claude") when empty.
		wslive.Agent{ID: "think", Status: wslive.StatusRunning, Activity: wslive.Activity{Phase: wslive.PhaseThinking}},
		// Holding inherits parent harness from "fail" ("codex") instead of lead.
		wslive.Agent{ID: "hold", ParentID: "fail", Status: wslive.StatusDone, Holding: true, Alive: true},
		// Direct alive-idle leaf folds into Folded & Completed.
		wslive.Agent{ID: "alive", Status: wslive.StatusDone, Alive: true, AgentKind: wslive.AgentKindPi},
		// Deeper (depth >= 2) alive-idle leaf stays visible.
		wslive.Agent{ID: "alive-deep", ParentID: "hold", Status: wslive.StatusDone, Alive: true},
		// Deeper (depth >= 2) dead-done leaf folds into Completed only, not Folded.
		wslive.Agent{ID: "done-deep", ParentID: "hold", Status: wslive.StatusDone, EndedAt: 1},
		// Asleep parent kept visible solely for connectivity to running child.
		wslive.Agent{ID: "asleep-anc", Status: wslive.StatusDone},
		wslive.Agent{ID: "grandchild", ParentID: "asleep-anc", Status: wslive.StatusRunning, Activity: wslive.Activity{Phase: wslive.PhaseTool}},
		// Direct alive-idle parent with only dead-done child folds whole subtree into Completed (2) and increments Folded once (1).
		// In a Claude lead's snapshot these rows are native, so each carries
		// its end time to read as done.
		wslive.Agent{ID: "alive-folded-parent", Status: wslive.StatusDone, Alive: true, EndedAt: 1},
		wslive.Agent{ID: "alive-folded-child", ParentID: "alive-folded-parent", Status: wslive.StatusDone, EndedAt: 1},
		// Asleep leaf is folded into Completed and Folded.
		wslive.Agent{ID: "done-leaf", Status: wslive.StatusDone, EndedAt: 1},
	)
	v := buildSessionView(pageInputs{Session: leadSess, Snapshot: snap, Now: now, Lookup: lookupOf(nil, nil)})
	if v.Tree.HUD.Completed != 5 {
		t.Fatalf("HUD.Completed = %d, want 5", v.Tree.HUD.Completed)
	}
	if v.Tree.HUD.Folded != 3 {
		t.Fatalf("HUD.Folded = %d, want 3", v.Tree.HUD.Folded)
	}
	byID := map[string]HUDNode{}
	var collect func([]HUDNode)
	collect = func(nodes []HUDNode) {
		for _, n := range nodes {
			byID[n.ID] = n
			collect(n.Children)
		}
	}
	collect(v.Tree.HUD.Roots)
	for _, hiddenID := range []string{"alive", "done-leaf", "done-deep", "alive-folded-parent", "alive-folded-child"} {
		if _, ok := byID[hiddenID]; ok {
			t.Fatalf("node %q should be excluded from visible HUD roots", hiddenID)
		}
	}
	want := map[string][2]string{
		"perm":       {"permission", "agy"},
		"fail":       {"failed", "codex"},
		"reply":      {"tool", "claude"},
		"think":      {"thinking", "claude"},
		"hold":       {"holding", "codex"},
		"alive-deep": {"idle", "codex"},
		"asleep-anc": {"idle", "claude"},
		"grandchild": {"tool", "claude"},
	}
	for id, exp := range want {
		n, ok := byID[id]
		if !ok {
			t.Fatalf("missing visible HUD node %q", id)
		}
		if n.State != exp[0] || n.Harness != exp[1] {
			t.Fatalf("node %q = (state=%q, harness=%q), want (%q, %q)", id, n.State, n.Harness, exp[0], exp[1])
		}
	}
	for _, id := range []string{"perm", "fail", "reply", "think", "hold", "grandchild"} {
		if !byID[id].CloseActive {
			t.Errorf("node %q should require confirmation", id)
		}
	}
	for _, id := range []string{"alive-deep", "asleep-anc"} {
		if byID[id].CloseActive {
			t.Errorf("node %q should close immediately", id)
		}
	}
	if byID["perm"].Task != "bash rm -rf" {
		t.Fatalf("perm Task = %q, want %q", byID["perm"].Task, "bash rm -rf")
	}
	// A retired Pi row projects as ended without a harness badge.
	piLead := liveSession("self2", "/p", now)
	piLead.Harness = ""
	piView := buildSessionView(pageInputs{
		Session:  piLead,
		Snapshot: liveSnap(now, wslive.Lead{}, wslive.Agent{ID: "pi:1", AgentKind: wslive.AgentKindPi, Status: wslive.StatusRunning}),
		Now:      now,
	})
	if len(piView.Tree.HUD.Roots) != 1 || piView.Tree.HUD.Roots[0].Harness != "" {
		t.Fatalf("retired Pi HUD = %+v", piView.Tree.HUD.Roots)
	}
}

func TestHUDProjectionDepthCapOverflowAndLinkedDedup(t *testing.T) {
	now := testNow
	sessions := map[string]Session{
		"self": liveSession("self", "/p", now),
		"r1":   liveSession("r1", "/p", now),
		"r5":   liveSession("r5", "/p", now),
	}
	// Own snapshot has r1 (depth 1) -> n2 (depth 2) -> n3 (depth 3) -> n4 (depth 4).
	// Linked r1 snapshot duplicates n2 and also carries r5 (depth 3 under n2) -> n4b -> n5a (depth 5, running) + n5done (depth 5, done).
	ownSnap := liveSnap(now, wslive.Lead{},
		wslive.Agent{ID: "pi:r1", ReplyID: "r1", AgentKind: wslive.AgentKindPi, Status: wslive.StatusDone, Holding: true, Alive: true},
		wslive.Agent{ID: "pi:n2", ParentID: "pi:r1", AgentKind: wslive.AgentKindPi, Status: wslive.StatusDone, Holding: true, Alive: true},
	)
	r1Snap := liveSnap(now, wslive.Lead{},
		wslive.Agent{ID: "pi:n2", AgentKind: wslive.AgentKindPi, Status: wslive.StatusDone, Holding: true, Alive: true},
		wslive.Agent{ID: "n3", Status: wslive.StatusRunning},
		wslive.Agent{ID: "n4", ParentID: "n3", Status: wslive.StatusRunning},
		wslive.Agent{ID: "n5-vis", ParentID: "n4", Status: wslive.StatusRunning},
		wslive.Agent{ID: "n6-vis", ParentID: "n5-vis", Status: wslive.StatusDone, Alive: true},
		wslive.Agent{ID: "n5-done", ParentID: "n4", Status: wslive.StatusDone},
	)
	snaps := map[string]*wslive.Snapshot{"r1": r1Snap}
	v := buildSessionView(pageInputs{
		Session:  sessions["self"],
		Snapshot: ownSnap,
		Now:      now,
		Lookup:   lookupOf(sessions, snaps),
	})
	if len(v.Tree.HUD.Roots) != 1 || len(v.Tree.HUD.Roots[0].Children) != 2 {
		t.Fatalf("unexpected linked/dedup roots: %+v", v.Tree.HUD.Roots)
	}
	// Depth hierarchy: r1 (1) -> n3 (2) -> n4 (3) -> n5-vis (4, Overflow=1 for n6-vis).
	var n5 HUDNode
	for _, c := range v.Tree.HUD.Roots[0].Children {
		if c.ID == "n3" {
			n5 = c.Children[0].Children[0]
		}
	}
	if n5.ID != "n5-vis" || n5.Overflow != 1 || len(n5.Children) != 0 {
		t.Fatalf("depth-4 node n5-vis = %+v, want Overflow=1 and no Children", n5)
	}
	if v.Tree.HUD.Completed != 1 || v.Tree.HUD.Folded != 0 {
		t.Fatalf("HUD = (Completed=%d, Folded=%d), want (1, 0) (n5-done only, no double count of pi:n2)", v.Tree.HUD.Completed, v.Tree.HUD.Folded)
	}
	// Verify SubagentCount and CompactAgents stay untouched by HUD holding/alive rules.
	if v.Tree.Subagents != (SubagentCount{}) || len(v.Tree.CompactAgents) != 0 {
		t.Fatalf("legacy Subagents/CompactAgents drifted: %+v / %+v", v.Tree.Subagents, v.Tree.CompactAgents)
	}
	// Verify rendered .ag-hud-data JSON inside .ag-source.
	set, err := renderSessionRegions(v, newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	treeHTML := string(set[regionTree].HTML)
	if !strings.Contains(treeHTML, `<script type="application/json" class="ag-hud-data">`) {
		t.Fatalf("tree HTML missing ag-hud-data carrier:\n%s", treeHTML)
	}
	start := strings.Index(treeHTML, `<script type="application/json" class="ag-hud-data">`) + len(`<script type="application/json" class="ag-hud-data">`)
	end := strings.Index(treeHTML[start:], `</script>`)
	var decoded HUDView
	if err := json.Unmarshal([]byte(treeHTML[start:start+end]), &decoded); err != nil {
		t.Fatalf("unmarshal ag-hud-data: %v", err)
	}
	if len(decoded.Roots) != 1 || decoded.Completed != 1 {
		t.Fatalf("decoded HUD = %+v", decoded)
	}
}

// TestHUDEndUnknownSurvivesAnOlderWriter: an older writer (7482aa91e)
// rewrites the snapshot without the endUnknown member. Read back as a file
// and re-seeded into a new writer's state, the agy row whose observation
// ended is still left out of the HUD completion count.
func TestHUDEndUnknownSurvivesAnOlderWriter(t *testing.T) {
	now := testNow
	ms := now.UnixMilli()
	agyUnknown := wslive.Agent{ID: wslive.NativeAgyPrefix + "call_old", Label: "research", Status: wslive.StatusDone, StartedAt: ms - 60_000, EndedAt: ms - 30_000, EndUnknown: true, Activity: wslive.Activity{Phase: wslive.PhaseIdle, PhaseStartedAt: ms - 30_000}}
	codexDone := wslive.Agent{ID: wslive.NativeCodexPrefix + "call_c", Label: "ok_reply", Status: wslive.StatusDone, StartedAt: ms - 50_000, EndedAt: ms - 40_000, ActiveMs: 10_000}
	raw, err := json.Marshal(liveSnap(now, wslive.Lead{}, agyUnknown, codexDone).ForDisk())
	if err != nil {
		t.Fatal(err)
	}
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
	if strings.Contains(string(raw), "endUnknown") {
		t.Fatal("the older writer's file still carries the flag")
	}
	lead := liveSession("self", "/p", now)
	lead.Harness = "agy"
	check := func(name string, snap *wslive.Snapshot) {
		t.Helper()
		hud := buildSessionView(pageInputs{Session: lead, Snapshot: snap, Now: now, Lookup: lookupOf(nil, nil)}).Tree.HUD
		if hud.Completed != 1 || hud.ObservationEnded != 1 {
			t.Fatalf("%s: HUD completed=%d observationEnded=%d, want 1 and 1 (the end-unknown row is no completion)", name, hud.Completed, hud.ObservationEnded)
		}
	}
	parsed, err := parseSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	check("file read", &parsed.snap)
	var flagless wslive.Snapshot
	if err := json.Unmarshal(raw, &flagless); err != nil {
		t.Fatal(err)
	}
	st := wslive.NewState("hud-older-writer")
	flagless.ReplyID = st.ReplyID()
	st.SeedFinished(flagless)
	check("re-seeded", liveSnap(now, wslive.Lead{}, st.Snapshot(ms).Agents...))
}

// TestHUDProjectionNativeRows pins native subagent rows in the HUD and the
// expanded tree: a running agy row and a completed Codex row carry native,
// startedAt, and endedAt when known and never link; the completed row folds
// into the completed and folded counts, while an end-unknown agy row is
// excluded from both and counted as observation ended; the ended group shows lifecycle details;
// a Claude lead's no-identity row is native; daemon-spawned and Pi extension
// rows keep their JSON.
// TestHUDHarnessLegacyPiAgentKind pins the (1881) survivor: a row written
// before rows carried a harness name shows as Pi by its agent kind alone, and
// a row's own registered name or its parent's wins over that marker.
func TestHUDHarnessLegacyPiAgentKind(t *testing.T) {
	set := testHarnessTraits()
	legacy := wslive.Agent{ID: "pi:x", AgentKind: wslive.AgentKindPi}
	for _, tc := range []struct {
		name   string
		set    HarnessTraitSet
		agent  wslive.Agent
		parent string
		want   string
	}{
		{"kind alone", set, legacy, "", ""},
		{"kind alone, no registrations", nil, legacy, "", ""},
		{"retired row omits parent badge", set, legacy, "claude", ""},
		{"own registered name wins", set, wslive.Agent{ID: "a", AgentKind: wslive.AgentKindPi, Harness: " Codex "}, "claude", "codex"},
		{"unregistered name is not a harness", set, wslive.Agent{ID: "a", Harness: "other"}, "", ""},
		{"no registrations name no harness", nil, wslive.Agent{ID: "a", Harness: "claude"}, "", ""},
		{"extension name", HarnessTraitSet{"fixture": {}}, wslive.Agent{ID: "a", Harness: "fixture"}, "", "fixture"},
	} {
		if got := resolveHUDHarness(tc.set, tc.agent, tc.parent); got != tc.want {
			t.Errorf("%s: harness = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestHUDProjectionNativeRows(t *testing.T) {
	now := testNow
	ms := now.UnixMilli()
	agyRun := wslive.Agent{ID: wslive.NativeAgyPrefix + "call_run", Label: "Kiwi Responder", Status: wslive.StatusRunning, StartedAt: ms - 9_000, TurnStartedAt: ms - 9_000, Activity: wslive.Activity{Phase: wslive.PhaseThinking}}
	agyUnknown := wslive.Agent{ID: wslive.NativeAgyPrefix + "call_old", Label: "research", Status: wslive.StatusDone, StartedAt: ms - 60_000, EndedAt: ms - 30_000, EndUnknown: true, Activity: wslive.Activity{Phase: wslive.PhaseIdle, PhaseStartedAt: ms - 30_000}}
	codexDone := wslive.Agent{ID: wslive.NativeCodexPrefix + "call_c", Label: "ok_reply", Status: wslive.StatusDone, StartedAt: ms - 50_000, EndedAt: ms - 40_000, ActiveMs: 10_000}
	spawned := wslive.Agent{ID: "pi:w1", Label: "worker", Status: wslive.StatusRunning, AgentKind: wslive.AgentKindPi, Harness: "codex", ReplyID: "w1", StartedAt: ms - 5_000}
	piExt := wslive.Agent{ID: "pi:ext", Label: "pi ext", Status: wslive.StatusRunning, AgentKind: wslive.AgentKindPi, StartedAt: ms - 4_000}
	lead := liveSession("self", "/p", now)
	lead.Harness = "agy"
	v := buildSessionView(pageInputs{Session: lead, Snapshot: liveSnap(now, wslive.Lead{}, agyRun, agyUnknown, codexDone, spawned, piExt), Now: now, Lookup: lookupOf(nil, nil)})
	hud := v.Tree.HUD
	if hud.Completed != 1 || hud.ObservationEnded != 1 || hud.Folded != 1 {
		t.Fatalf("HUD completed=%d observationEnded=%d folded=%d, want 1, 1 and 1 (the folded finished-agents count leaves out the end-unknown row)", hud.Completed, hud.ObservationEnded, hud.Folded)
	}
	byID := map[string]HUDNode{}
	for _, n := range hud.Roots {
		byID[n.ID] = n
	}
	run, ok := byID[agyRun.ID]
	if !ok || !run.Native || run.StartedAt != agyRun.StartedAt || run.EndedAt != 0 || run.Linked || run.Harness != "agy" || run.State != "thinking" {
		t.Fatalf("running agy node = %+v", run)
	}
	for _, id := range []string{agyUnknown.ID, codexDone.ID} {
		if _, ok := byID[id]; ok {
			t.Fatalf("finished native row %s stayed in the compact HUD", id)
		}
	}
	// Daemon-spawned and Pi extension rows: no native fields on the wire.
	for _, id := range []string{spawned.ID, piExt.ID} {
		raw, _ := json.Marshal(byID[id])
		if strings.Contains(string(raw), `"native"`) || strings.Contains(string(raw), `"startedAt"`) || strings.Contains(string(raw), `"endedAt"`) {
			t.Fatalf("%s gained native fields: %s", id, raw)
		}
	}
	raw, _ := json.Marshal(hud)
	if !strings.Contains(string(raw), `"observationEnded":1`) || !strings.Contains(string(raw), `"native":true`) {
		t.Fatalf("HUD JSON = %s", raw)
	}
	if v.Tree.Finished != 1 || v.Tree.Running != 3 || v.Tree.Failed != 0 {
		t.Fatalf("tree counts running=%d done=%d failed=%d, want 3, 1 (the codex row only), 0", v.Tree.Running, v.Tree.Finished, v.Tree.Failed)
	}
	ended := map[string]AgentNode{}
	for _, a := range v.Tree.EndedAgents() {
		ended[a.ID] = a
	}
	if u := ended[agyUnknown.ID]; !u.Native || !u.EndUnknown() || u.EndedStatus() != "unknown" {
		t.Fatalf("end-unknown node = %+v", u)
	}
	if c := ended[codexDone.ID]; !c.Native || c.EndUnknown() || c.EndedStatus() != "done" {
		t.Fatalf("codex node = %+v", c)
	}
	html := renderTree(t, v)
	tree := html[strings.Index(html, `<div class="thead">`):]
	group := tree[strings.Index(tree, `<div class="tgroup ended">`):]
	if !strings.Contains(group, `class="agent done native d-unknown" data-end-unknown="1"`) || !strings.Contains(group, "observation ended, completion unknown") {
		t.Fatalf("ended group lacks the end-unknown row: %s", group)
	}
	unknownRow := group[strings.Index(group, `data-end-unknown="1"`):]
	if unknownRow = unknownRow[:strings.Index(unknownRow, "</summary>")]; !strings.Contains(unknownRow, `<span class="amk">?</span>`) || !strings.Contains(unknownRow, `class="nbadge"`) {
		t.Fatalf("end-unknown row shows a completion mark: %s", unknownRow)
	}
	// Its EndedAt is when observation ended, never shown as an end.
	if strings.Contains(unknownRow, `data-ms="`+strconv.FormatInt(agyUnknown.EndedAt, 10)+`"`) {
		t.Fatalf("end-unknown row shows its observation end as an end: %s", unknownRow)
	}
	codexRow := group[strings.Index(group, `data-key="agent-`+codexDone.ID+`"`):]
	codexRow = codexRow[:strings.Index(codexRow, "</summary>")]
	for _, want := range []string{`class="nbadge"`, `data-ms="` + strconv.FormatInt(codexDone.StartedAt, 10) + `" data-f="clock"`, `data-ms="` + strconv.FormatInt(codexDone.EndedAt, 10) + `" data-f="clock"`, `<span class="amk">✓</span>`} {
		if !strings.Contains(codexRow, want) {
			t.Fatalf("codex ended row lacks %q: %s", want, codexRow)
		}
	}
	if strings.Contains(tree, `href="/s/`) {
		t.Fatalf("a native row links to a transcript: %s", tree)
	}

	// A Claude lead's row without daemon identity is native; a Claude lead's
	// daemon-spawned row is not.
	claude := liveSession("self", "/p", now)
	claude.Harness = "claude"
	cv := buildSessionView(pageInputs{Session: claude, Snapshot: liveSnap(now, wslive.Lead{},
		wslive.Agent{ID: "task-1", Label: "explore", Status: wslive.StatusRunning, StartedAt: ms - 1_000},
		spawned), Now: now, Lookup: lookupOf(nil, nil)})
	for _, n := range cv.Tree.HUD.Roots {
		if want := n.ID == "task-1"; n.Native != want {
			t.Fatalf("claude lead node %s native = %v, want %v", n.ID, n.Native, want)
		}
	}
}

// TestHUDNodeContextFigures pins the hover card's context figures: a linked
// node carries its own session's usage (the band's scale and colour rule), an
// unlinked node its row's tokens on the scale its runner carried (ws
// thresholds, else the native window, else none), and an unknown stays absent.
func TestHUDNodeContextFigures(t *testing.T) {
	now := testNow
	tok := func(n int64) *int64 { return &n }
	run := func(a wslive.Agent, usage *wslive.UsageContext) HUDNode {
		t.Helper()
		sessions := map[string]Session{"self": liveSession("self", "/p", now), "r1": liveSession("r1", "/p", now)}
		linked := liveSnap(now, wslive.Lead{})
		if usage != nil {
			linked.Usage = &wslive.Usage{Context: usage}
		}
		v := buildSessionView(pageInputs{
			Session: sessions["self"], Snapshot: liveSnap(now, wslive.Lead{}, a), Now: now,
			Lookup: lookupOf(sessions, map[string]*wslive.Snapshot{"r1": linked}),
		})
		if len(v.Tree.HUD.Roots) != 1 {
			t.Fatalf("roots = %+v", v.Tree.HUD.Roots)
		}
		return v.Tree.HUD.Roots[0]
	}
	linked := wslive.Agent{ID: "pi:r1", ReplyID: "r1", AgentKind: wslive.AgentKindPi, Status: wslive.StatusRunning, ContextTokens: 7}
	plain := wslive.Agent{ID: "n1", Status: wslive.StatusRunning, ContextTokens: 41_000}

	// Normal: the session's own window, no ws thresholds.
	n := run(linked, &wslive.UsageContext{Tokens: tok(104_000), Window: 272_000})
	if n.ContextTokens != 104_000 || n.ContextWindow != 272_000 || n.ContextSoft != 0 || n.ContextLevel != "" || n.ContextText != "104k / 272k" {
		t.Fatalf("linked normal = %+v", n)
	}
	// Past the ws soft threshold: the band's warn colour, scaled to the hard point.
	n = run(linked, &wslive.UsageContext{Tokens: tok(156_000), Window: 1_000_000,
		Compaction: &wslive.UsageCompaction{Soft: 140_000, Hard: 200_000}})
	if n.ContextWindow != 200_000 || n.ContextSoft != 140_000 || n.ContextLevel != "warn" || n.ContextText != "156k / 200k" {
		t.Fatalf("linked past soft = %+v", n)
	}
	// Linked with no known tokens: nothing, not the agent's stale figure.
	n = run(linked, &wslive.UsageContext{Window: 272_000})
	if n.ContextTokens != 0 || n.ContextText != "" || n.ContextWindow != 0 {
		t.Fatalf("linked unknown tokens = %+v", n)
	}
	// Linked, tokens but no window: the figure alone.
	n = run(linked, &wslive.UsageContext{Tokens: tok(41_000)})
	if n.ContextTokens != 41_000 || n.ContextWindow != 0 || n.ContextLevel != "" || n.ContextText != "41.0k" {
		t.Fatalf("linked no window = %+v", n)
	}
	// Linked session without a usage figure at all falls back to the agent's.
	n = run(linked, nil)
	if !n.Linked || n.ContextTokens != 7 || n.ContextText != "7" {
		t.Fatalf("linked no usage = %+v", n)
	}
	// Unlinked: the adapter's token count, no window.
	n = run(plain, nil)
	if n.ContextTokens != 41_000 || n.ContextWindow != 0 || n.ContextText != "41.0k" {
		t.Fatalf("unlinked = %+v", n)
	}
	// Unlinked with the scale its runner carried on the row: the ws thresholds
	// scale the bar as the band's do, past soft reads warn.
	withScale := plain
	withScale.ContextTokens = 156_000
	withScale.ContextWindow = 1_000_000
	withScale.ContextCompaction = &wslive.UsageCompaction{Soft: 140_000, Hard: 200_000}
	n = run(withScale, nil)
	if n.ContextWindow != 200_000 || n.ContextSoft != 140_000 || n.ContextLevel != "warn" || n.ContextText != "156k / 200k" {
		t.Fatalf("unlinked with ws thresholds = %+v", n)
	}
	// Thresholds unresolved: the native window scales the bar, no soft tick.
	withScale.ContextCompaction = nil
	withScale.ContextTokens = 104_000
	withScale.ContextWindow = 272_000
	n = run(withScale, nil)
	if n.ContextWindow != 272_000 || n.ContextSoft != 0 || n.ContextLevel != "" || n.ContextText != "104k / 272k" {
		t.Fatalf("unlinked with a native window = %+v", n)
	}
	// Thresholds alone (window unknown) still scale it.
	withScale.ContextWindow = 0
	withScale.ContextCompaction = &wslive.UsageCompaction{Hard: 200_000}
	n = run(withScale, nil)
	if n.ContextWindow != 200_000 || n.ContextText != "104k / 200k" {
		t.Fatalf("unlinked with a hard point only = %+v", n)
	}
	// Unknown stays out of the JSON.
	plain.ContextTokens = 0
	n = run(plain, nil)
	raw, _ := json.Marshal(n)
	if strings.Contains(string(raw), "context") {
		t.Fatalf("unknown context leaked into %s", raw)
	}
}

// TestHUDNodeCompactionState pins the hover card's compaction fields: they are
// the row's own (compacting now, completed count, latest origin), a node that
// never compacted carries none of them in its JSON, and a negative count is
// not carried.
func TestHUDNodeCompactionState(t *testing.T) {
	now := testNow
	run := func(a wslive.Agent) HUDNode {
		t.Helper()
		v := buildSessionView(pageInputs{
			Session:  liveSession("self", "/p", now),
			Snapshot: liveSnap(now, wslive.Lead{}, a), Now: now,
			Lookup: lookupOf(map[string]Session{"self": liveSession("self", "/p", now)}, nil),
		})
		if len(v.Tree.HUD.Roots) != 1 {
			t.Fatalf("roots = %+v", v.Tree.HUD.Roots)
		}
		return v.Tree.HUD.Roots[0]
	}
	row := wslive.Agent{ID: "pi:r1", AgentKind: wslive.AgentKindPi, Status: wslive.StatusRunning, ContextTokens: 7}

	// D: compacting now with two behind it.
	d := row
	d.Compacting, d.Compactions, d.CompactOrigin = true, 2, wslive.CompactOriginAuto
	if n := run(d); !n.Compacting || n.Compactions != 2 || n.CompactOrigin != "auto" {
		t.Fatalf("compacting now = %+v", n)
	}
	// E: idle with a count, whatever the origin.
	for _, origin := range []string{"auto", "manual", "native", ""} {
		e := row
		e.Compactions, e.CompactOrigin = 3, origin
		if n := run(e); n.Compacting || n.Compactions != 3 || n.CompactOrigin != origin {
			t.Fatalf("count only (%q) = %+v", origin, n)
		}
	}
	// F: never compacted carries nothing.
	raw, _ := json.Marshal(run(row))
	for _, k := range []string{"compacting", "compactions", "compactOrigin"} {
		if strings.Contains(string(raw), k) {
			t.Fatalf("never compacted leaked %s into %s", k, raw)
		}
	}
	neg := row
	neg.Compactions = -4
	if n := run(neg); n.Compactions != 0 {
		t.Fatalf("negative count = %+v", n)
	}
}
