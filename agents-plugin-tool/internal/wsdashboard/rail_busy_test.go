package wsdashboard

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// TestRailBusy pins RailRow.Busy, the full-idle signal a page notifies on:
// a live session is busy while its lead works or waits on a permission, or
// while a ws-known activity remains (a subagent, a background task, an exec
// job under its project, or a busy live child at any depth). A harness's
// unknown background count (-1) reads as none once the turn ended.
func TestRailBusy(t *testing.T) {
	now := testNow
	const project = "/w/repo/wt"
	reg := func(id string, hosted bool) Registration {
		return Registration{ReplyID: id, Project: project, Harness: "claude", Hosted: hosted, RegisteredAt: stampFor(now.Add(-time.Hour)), HeartbeatAt: stampFor(now)}
	}
	snap := func(phase string, mod func(*wslive.Snapshot)) *wslive.Snapshot {
		s := &wslive.Snapshot{SchemaVersion: 1, Adapter: "claude", HeartbeatMs: 30000, UpdatedAt: now.UnixMilli(), Lead: wslive.Lead{Activity: wslive.Activity{Phase: phase}}}
		if mod != nil {
			mod(s)
		}
		return s
	}
	n := func(v int) *int { return &v }
	row := func(id string, hosted, live bool, s *wslive.Snapshot, jobs map[string]int) RailRow {
		in := railInput{Session: Session{Registration: reg(id, hosted)}, Snapshot: s, ExecJobs: railExecJobs(jobs, project)}
		return railRow(in, live, now)
	}
	idle := func(id string) RailRow { return row(id, false, true, snap(wslive.PhaseIdle, nil), nil) }
	// own is a hosted session whose turn ended with n of its own hosted
	// exec jobs still running.
	own := func(id string, live bool, n int) RailRow {
		in := railInput{Session: Session{Registration: reg(id, true)}, Snapshot: snap(wslive.PhaseIdle, nil), OwnExecJobs: n}
		return railRow(in, live, now)
	}
	cases := []struct {
		name   string
		row    RailRow
		want   bool
		active bool
	}{
		{"working lead", row("w", false, true, snap(wslive.PhaseTool, nil), nil), true, true},
		{"hosted approval wait", row("a", true, true, snap(wslive.PhasePermission, func(s *wslive.Snapshot) {
			s.Lead.Permission = &wslive.Permission{ToolName: "Bash"}
		}), nil), true, true},
		{"terminal permission wait", row("p", false, true, snap(wslive.PhasePermission, nil), nil), true, true},
		{"running subagent", row("s", false, true, snap(wslive.PhaseIdle, func(s *wslive.Snapshot) {
			s.Agents = []wslive.Agent{{ID: "kid", Status: wslive.StatusRunning}}
		}), nil), true, true},
		// A settled direct child's running grandchild (an unlisted worker
		// the child started) keeps the lead busy, though the level-1 badge
		// count is zero.
		{"running grandchild of a settled child", row("g", false, true, snap(wslive.PhaseIdle, func(s *wslive.Snapshot) {
			s.Agents = []wslive.Agent{{ID: "kid", Status: wslive.StatusDone}, {ID: "grandkid", ParentID: "kid", Status: wslive.StatusRunning}}
		}), nil), true, true},
		// A running native subagent counts like any running row; an agy one
		// ends (outcome unknown) with its parent's turn and stops counting.
		{"running codex native subagent", row("cn", true, true, snap(wslive.PhaseIdle, func(s *wslive.Snapshot) {
			s.Adapter = "codex"
			s.Agents = []wslive.Agent{{ID: wslive.NativeCodexPrefix + "call_1", Label: "ok_reply", Status: wslive.StatusRunning, StartedAt: 1}}
		}), nil), true, true},
		{"agy native subagent after the parent turn", row("an", true, true, snap(wslive.PhaseIdle, func(s *wslive.Snapshot) {
			s.Adapter = "agy"
			s.Agents = []wslive.Agent{{ID: wslive.NativeAgyPrefix + "call_2", Label: "research", Status: wslive.StatusDone, StartedAt: 1, EndedAt: 2, EndUnknown: true}}
		}), nil), false, false},
		{"background tasks", row("b", false, true, snap(wslive.PhaseIdle, func(s *wslive.Snapshot) {
			s.Usage = &wslive.Usage{Background: n(2)}
		}), nil), true, true},
		{"unknown background count, idle", row("c", false, true, snap(wslive.PhaseIdle, func(s *wslive.Snapshot) {
			s.Adapter = "codex"
			s.Usage = &wslive.Usage{Background: n(-1)}
		}), nil), false, false},
		{"idle", idle("i"), false, false},
		{"exec job under the project", row("e", false, true, snap(wslive.PhaseIdle, nil), map[string]int{"/w/repo/wt": 1, "/w/other": 3}), true, false},
		{"exec job at a root holding the project", row("e2", false, true, snap(wslive.PhaseIdle, nil), map[string]int{"/w/repo/": 1}), true, false},
		{"exec job in an unrelated root", row("u", false, true, snap(wslive.PhaseIdle, nil), map[string]int{"/w/other": 3, "/w/repo/wt2": 1, "/w/repo/wt/sub": 1}), false, false},
		{"own hosted exec job", own("h", true, 1), true, true},
		{"own hosted exec job, not live", own("h2", false, 2), false, false},
		{"not live", row("x", false, false, snap(wslive.PhaseTool, func(s *wslive.Snapshot) {
			s.Usage = &wslive.Usage{Background: n(1)}
		}), map[string]int{project: 1}), false, false},
	}
	for _, c := range cases {
		r := c.row
		if c.name == "running grandchild of a settled child" && r.Subagents != 0 {
			t.Errorf("%s: Subagents badge = %d, want the level-1 count 0", c.name, r.Subagents)
		}
		if got := railSetBusy(&r); got != c.want || r.Busy != c.want {
			t.Errorf("%s: Busy = %v, want %v (row %+v)", c.name, r.Busy, c.want, r)
		}
		// Exec jobs known only by worktree keep a row busy but never make
		// it active (its own activity); its own hosted jobs do.
		if r.Active != c.active {
			t.Errorf("%s: Active = %v, want %v (row %+v)", c.name, r.Active, c.active, r)
		}
	}

	// Through the layout: a busy live child keeps its idle parent busy; idle
	// children leave it idle. Ended rows are never busy.
	view := RailView{Workspaces: []RailWorkspace{{Path: "/w/plain", Sessions: []RailRow{
		idle("par"), row("kidw", false, true, snap(wslive.PhaseThinking, nil), nil),
		idle("par2"), idle("kid2"),
	}, Ended: []RailRow{row("gone", false, false, snap(wslive.PhaseTool, nil), nil)}}}}
	f := railFactsOf(view)
	doc := emptyLayout()
	doc.Workspaces["/w/plain"] = &railLayoutWS{Children: map[string][]string{"par": {"kidw"}, "par2": {"kid2"}}}
	orderRailView(&view, doc, f)
	ws := view.Workspaces[0]
	got, active := map[string]bool{}, map[string]bool{}
	for _, r := range ws.AllSessions() {
		got[r.ReplyID], active[r.ReplyID] = r.Busy, r.Active
	}
	want := map[string]bool{"par": true, "kidw": true, "par2": false, "kid2": false, "gone": false}
	for id, w := range want {
		if b, ok := got[id]; !ok || b != w || active[id] != w {
			t.Errorf("layout row %s: Busy = %v, Active = %v (listed %v), want %v", id, b, active[id], ok, w)
		}
	}
	if len(ws.Sessions) != 2 || len(ws.Sessions[0].Children) != 1 {
		t.Fatalf("children not nested: %+v", ws.Sessions)
	}

	// Depth 2 (the layout nests one level, but the rule holds at any depth).
	deep := RailRow{ReplyID: "g", Live: true, Children: []RailRow{{ReplyID: "p", Live: true, Children: []RailRow{{ReplyID: "c", Live: true, ExecJobs: 1}}}}}
	if !railSetBusy(&deep) || !deep.Children[0].Busy || !deep.Children[0].Children[0].Busy {
		t.Errorf("a busy grandchild did not keep its ancestors busy: %+v", deep)
	}
	if deep.Active || deep.Children[0].Active || deep.Children[0].Children[0].Active {
		t.Errorf("an exec job made a tree active: %+v", deep)
	}
	deep.Children[0].Children[0].ExecJobs, deep.Children[0].Children[0].Working = 0, true
	if !railSetBusy(&deep) || !deep.Active || !deep.Children[0].Active {
		t.Errorf("an active grandchild did not keep its ancestors active: %+v", deep)
	}
	deep.Children[0].Children[0].Working, deep.Children[0].Children[0].OwnExecJobs = false, 1
	if !railSetBusy(&deep) || !deep.Active || !deep.Children[0].Active {
		t.Errorf("a grandchild's own hosted job did not keep its ancestors active: %+v", deep)
	}
	deep.Children[0].Children[0].OwnExecJobs = 0
	if railSetBusy(&deep) || deep.Active {
		t.Errorf("an all-idle tree reads busy or active: %+v", deep)
	}
}

// TestRailMarkupBusy pins the attributes a page reads: data-busy="1" on a
// busy row, top-level and child alike, data-active="1" only on one busy by
// its own activity, and neither on an idle row.
func TestRailMarkupBusy(t *testing.T) {
	kid := RailRow{ReplyID: "kid", Harness: "pi", Live: true, Child: true, Group: "/w/folder", Busy: true}
	par := RailRow{ReplyID: "par", Harness: "claude", Live: true, HasChildren: true, Children: []RailRow{kid}, Busy: true, Active: true}
	idle := RailRow{ReplyID: "idle", Harness: "claude", Live: true}
	view := RailView{Workspaces: []RailWorkspace{{Name: "folder", Path: "/w/folder", Repo: true, Worktrees: []RailWorktree{{Path: "/w/folder", Sessions: []RailRow{par, idle}}}}}}
	region, err := renderRailRegion(view, "en")
	if err != nil {
		t.Fatal(err)
	}
	html := string(region.HTML)
	for _, want := range []string{`data-rid="par" data-asks="" data-busy="1" data-active="1" `, `data-rid="kid" data-asks="" data-busy="1" data-star=`} {
		if !strings.Contains(html, want) {
			t.Errorf("the rail lacks %s:\n%s", want, html)
		}
	}
	if !strings.Contains(html, `data-rid="idle" data-asks="" data-star=`) || strings.Count(html, "data-busy") != 2 || strings.Count(html, "data-active") != 1 {
		t.Errorf("an idle row carries data-busy:\n%s", html)
	}
}

// TestExecJobsChangedReachesTheRailStream pins the wake: a changed exec job
// count, by worktree (ExecJobs) or by owning session (HostedExecJobs),
// reaches an open rail stream on ExecJobsChanged alone, rendered from the
// cached rail inputs (no session input is read again). A hosted job counts
// on its owner's row only, and makes it active.
func TestExecJobsChangedReachesTheRailStream(t *testing.T) {
	var mu sync.Mutex
	jobs, owned := map[string]int{}, map[string]int{}
	clone := func(m map[string]int) map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int{}
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	f := startPageFixture(t, withWorkspaces(t), func(o *DaemonOptions) {
		o.ExecJobs = func() map[string]int { return clone(jobs) }
		o.HostedExecJobs = func() map[string]int { return clone(owned) }
	})
	if _, err := f.d.workspaces.add(f.project, false); err != nil {
		t.Fatal(err)
	}
	events := openSSE(t, f.base+"/rail/events")
	railHTML := func() string {
		t.Helper()
		all, closed := collect(events)
		var last string
		for _, e := range all {
			var r regionEvent
			if e.name == "region" && json.Unmarshal([]byte(e.data), &r) == nil && r.Name == regionRail {
				last = string(r.HTML)
			}
		}
		if closed || last == "" {
			t.Fatalf("no rail region (closed=%v): %+v", closed, all)
		}
		return last
	}
	rowOf := func(html, id string) string {
		_, rest, _ := strings.Cut(html, `data-rid="`+id+`"`)
		head, _, _ := strings.Cut(rest, ">")
		return head
	}
	first := railHTML()
	if strings.Contains(rowOf(first, "boardsession"), "data-busy") {
		t.Fatalf("boardsession busy before any job: %s", rowOf(first, "boardsession"))
	}
	if !strings.Contains(rowOf(first, "livesession"), `data-busy="1"`) {
		t.Fatalf("the working livesession is not busy: %s", rowOf(first, "livesession"))
	}
	var reads atomic.Int32
	f.d.railInputs.mu.Lock()
	f.d.railInputs.onRead = func(string) { reads.Add(1) }
	f.d.railInputs.mu.Unlock()

	mu.Lock()
	jobs[f.project] = 1
	mu.Unlock()
	f.d.ExecJobsChanged()
	second := railHTML()
	if !strings.Contains(rowOf(second, "boardsession"), `data-busy="1"`) {
		t.Fatalf("an exec job under the project did not reach the stream: %s", rowOf(second, "boardsession"))
	}
	if strings.Contains(rowOf(second, "boardsession"), "data-active") {
		t.Fatalf("an exec job made boardsession active: %s", rowOf(second, "boardsession"))
	}
	if n := reads.Load(); n != 0 {
		t.Errorf("ExecJobsChanged re-read %d session inputs", n)
	}

	mu.Lock()
	delete(jobs, f.project)
	mu.Unlock()
	f.d.ExecJobsChanged()
	if third := railHTML(); strings.Contains(rowOf(third, "boardsession"), "data-busy") {
		t.Fatalf("the finished job still keeps boardsession busy: %s", rowOf(third, "boardsession"))
	}

	// Its own hosted job makes boardsession busy and active; once that job
	// ends, another conversation's hosted job still running leaves it idle.
	mu.Lock()
	owned["boardsession"], owned["someoneelse"] = 1, 2
	mu.Unlock()
	f.d.ExecJobsChanged()
	if html := railHTML(); !strings.Contains(rowOf(html, "boardsession"), `data-busy="1" data-active="1"`) {
		t.Fatalf("boardsession's own hosted job did not make it busy and active: %s", rowOf(html, "boardsession"))
	}
	mu.Lock()
	delete(owned, "boardsession")
	mu.Unlock()
	f.d.ExecJobsChanged()
	if html := railHTML(); strings.Contains(rowOf(html, "boardsession"), "data-busy") {
		t.Fatalf("another session's hosted job keeps boardsession busy: %s", rowOf(html, "boardsession"))
	}
	if n := reads.Load(); n != 0 {
		t.Errorf("ExecJobsChanged re-read %d session inputs", n)
	}
}
