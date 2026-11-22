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
	cases := []struct {
		name string
		row  RailRow
		want bool
	}{
		{"working lead", row("w", false, true, snap(wslive.PhaseTool, nil), nil), true},
		{"hosted approval wait", row("a", true, true, snap(wslive.PhasePermission, func(s *wslive.Snapshot) {
			s.Lead.Permission = &wslive.Permission{ToolName: "Bash"}
		}), nil), true},
		{"terminal permission wait", row("p", false, true, snap(wslive.PhasePermission, nil), nil), true},
		{"running subagent", row("s", false, true, snap(wslive.PhaseIdle, func(s *wslive.Snapshot) {
			s.Agents = []wslive.Agent{{ID: "kid", Status: wslive.StatusRunning}}
		}), nil), true},
		{"background tasks", row("b", false, true, snap(wslive.PhaseIdle, func(s *wslive.Snapshot) {
			s.Usage = &wslive.Usage{Background: n(2)}
		}), nil), true},
		{"unknown background count, idle", row("c", false, true, snap(wslive.PhaseIdle, func(s *wslive.Snapshot) {
			s.Adapter = "codex"
			s.Usage = &wslive.Usage{Background: n(-1)}
		}), nil), false},
		{"idle", idle("i"), false},
		{"exec job under the project", row("e", false, true, snap(wslive.PhaseIdle, nil), map[string]int{"/w/repo/wt": 1, "/w/other": 3}), true},
		{"exec job at a root holding the project", row("e2", false, true, snap(wslive.PhaseIdle, nil), map[string]int{"/w/repo/": 1}), true},
		{"exec job in an unrelated root", row("u", false, true, snap(wslive.PhaseIdle, nil), map[string]int{"/w/other": 3, "/w/repo/wt2": 1, "/w/repo/wt/sub": 1}), false},
		{"not live", row("x", false, false, snap(wslive.PhaseTool, func(s *wslive.Snapshot) {
			s.Usage = &wslive.Usage{Background: n(1)}
		}), map[string]int{project: 1}), false},
	}
	for _, c := range cases {
		r := c.row
		if got := railSetBusy(&r); got != c.want || r.Busy != c.want {
			t.Errorf("%s: Busy = %v, want %v (row %+v)", c.name, r.Busy, c.want, r)
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
	got := map[string]bool{}
	for _, r := range ws.AllSessions() {
		got[r.ReplyID] = r.Busy
	}
	want := map[string]bool{"par": true, "kidw": true, "par2": false, "kid2": false, "gone": false}
	for id, w := range want {
		if b, ok := got[id]; !ok || b != w {
			t.Errorf("layout row %s: Busy = %v (listed %v), want %v", id, b, ok, w)
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
	deep.Children[0].Children[0].ExecJobs = 0
	if railSetBusy(&deep) {
		t.Errorf("an all-idle tree reads busy: %+v", deep)
	}
}

// TestRailMarkupBusy pins the attribute a page reads: data-busy="1" on a
// busy row, top-level and child alike, and none on an idle row.
func TestRailMarkupBusy(t *testing.T) {
	kid := RailRow{ReplyID: "kid", Harness: "pi", Live: true, Child: true, Group: "/w/folder", Busy: true}
	par := RailRow{ReplyID: "par", Harness: "claude", Live: true, HasChildren: true, Children: []RailRow{kid}, Busy: true}
	idle := RailRow{ReplyID: "idle", Harness: "claude", Live: true}
	view := RailView{Workspaces: []RailWorkspace{{Name: "folder", Path: "/w/folder", Repo: true, Worktrees: []RailWorktree{{Path: "/w/folder", Sessions: []RailRow{par, idle}}}}}}
	region, err := renderRailRegion(view, "en")
	if err != nil {
		t.Fatal(err)
	}
	html := string(region.HTML)
	for _, want := range []string{`data-rid="par" data-asks="" data-busy="1"`, `data-rid="kid" data-asks="" data-busy="1"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the rail lacks %s:\n%s", want, html)
		}
	}
	if !strings.Contains(html, `data-rid="idle" data-asks="" data-star=`) || strings.Count(html, "data-busy") != 2 {
		t.Errorf("an idle row carries data-busy:\n%s", html)
	}
}

// TestExecJobsChangedReachesTheRailStream pins the wake: a changed exec job
// count reaches an open rail stream on ExecJobsChanged alone, rendered from
// the cached rail inputs (no session input is read again).
func TestExecJobsChangedReachesTheRailStream(t *testing.T) {
	var mu sync.Mutex
	jobs := map[string]int{}
	f := startPageFixture(t, withWorkspaces(t), func(o *DaemonOptions) {
		o.ExecJobs = func() map[string]int {
			mu.Lock()
			defer mu.Unlock()
			out := map[string]int{}
			for k, v := range jobs {
				out[k] = v
			}
			return out
		}
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
}
