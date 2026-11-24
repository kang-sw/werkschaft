package wsdashboard

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestRailSafeStopRowAndMarkup(t *testing.T) {
	now := testNow
	beat := stampFor(now)
	meta := railHistoryMeta{
		"safelead":    {Backend: "claude", HasProcess: true, SafeStop: true},
		"unsafelead":  {Backend: "claude", HasProcess: true, SafeStop: false},
		"nativebusy":  {Backend: "claude", HasProcess: true, SafeStop: true},
		"asleeplead":  {Backend: "claude", HasProcess: false, SafeStop: false},
		"archlead":    {Backend: "claude", HasProcess: false, SafeStop: false},
		"workinglead": {Backend: "claude", HasProcess: true, SafeStop: true},
	}
	in := func(id string, hosted, stopped, hidden bool, snap *wslive.Snapshot) railInput {
		reg := Registration{
			ReplyID:      id,
			Project:      "/w/repo",
			Harness:      "claude",
			Hosted:       hosted,
			RegisteredAt: beat,
			HeartbeatAt:  beat,
			HostStopped:  stopped,
		}
		if stopped {
			reg.HostExitReason = "stop"
		}
		return railInput{
			Session:   Session{Registration: reg, Hidden: hidden},
			Snapshot:  snap,
			Repo:      "/w/repo",
			AgentMeta: meta,
		}
	}
	nativeSnap := &wslive.Snapshot{
		SchemaVersion: 1,
		Adapter:       "claude",
		UpdatedAt:     now.UnixMilli(),
		HeartbeatMs:   30000,
		Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}},
		Agents:        []wslive.Agent{{ID: wslive.NativeAgyPrefix + "call1", Status: wslive.StatusRunning}},
	}
	workingSnap := &wslive.Snapshot{
		SchemaVersion: 1,
		Adapter:       "claude",
		UpdatedAt:     now.UnixMilli(),
		HeartbeatMs:   30000,
		Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseThinking}},
	}
	sessions := []railInput{
		in("safelead", true, false, false, nil),
		in("unsafelead", true, false, false, nil),
		in("nativebusy", true, false, false, nativeSnap),
		in("workinglead", true, false, false, workingSnap),
		in("asleeplead", true, true, false, nil),
		in("archlead", true, true, true, nil),
		in("termrow", false, false, false, nil),
	}
	repos := map[string]railRepo{"/w/repo": {Read: true, Entries: []railWorktreeEntry{{Path: "/w/repo", Branch: "main"}}}}
	view := buildRailView(sessions, repos, nil, now)
	splitRailArchived(&view)

	byID := map[string]RailRow{}
	wt := view.Workspaces[0].Worktrees[0]
	for _, list := range [][]RailRow{wt.Sessions, wt.Archived, wt.Ended} {
		for _, r := range list {
			byID[r.ReplyID] = r
		}
	}
	if r := byID["safelead"]; !r.HasProcess || !r.SafeStop {
		t.Errorf("safelead = {HasProcess:%v SafeStop:%v}, want true/true", r.HasProcess, r.SafeStop)
	}
	if r := byID["unsafelead"]; !r.HasProcess || r.SafeStop {
		t.Errorf("unsafelead = {HasProcess:%v SafeStop:%v}, want true/false", r.HasProcess, r.SafeStop)
	}
	if r := byID["nativebusy"]; !r.HasProcess || r.SafeStop {
		t.Errorf("nativebusy = {HasProcess:%v SafeStop:%v}, want true/false (running native subagent)", r.HasProcess, r.SafeStop)
	}
	if r := byID["workinglead"]; !r.HasProcess || r.SafeStop {
		t.Errorf("workinglead = {HasProcess:%v SafeStop:%v}, want true/false (working phase)", r.HasProcess, r.SafeStop)
	}
	if r := byID["asleeplead"]; r.HasProcess || r.SafeStop {
		t.Errorf("asleeplead = {HasProcess:%v SafeStop:%v}, want false/false", r.HasProcess, r.SafeStop)
	}

	// Nil AgentMeta falls back conservatively: HasProcess = !HostStopped, SafeStop = false.
	fallback := railRow(railInput{
		Session: Session{Registration: Registration{ReplyID: "nometa", Project: "/w/repo", Harness: "claude", Hosted: true, HeartbeatAt: beat}},
	}, true, now)
	if !fallback.HasProcess || fallback.SafeStop {
		t.Errorf("nil AgentMeta fallback = {HasProcess:%v SafeStop:%v}, want true/false", fallback.HasProcess, fallback.SafeStop)
	}

	region, err := renderRailRegion(view, "en")
	if err != nil {
		t.Fatal(err)
	}
	html := string(region.HTML)
	rowHTML := func(id string) string {
		_, rest, ok := strings.Cut(html, `data-rid="`+id+`"`)
		if !ok {
			t.Fatalf("missing row %s", id)
		}
		seg, _, _ := strings.Cut(rest, `<div class="rs`)
		return seg
	}
	if seg := rowHTML("safelead"); !strings.Contains(seg, `class="drstop d-stop-safe" data-safe="1"`) || !strings.Contains(seg, `#i-moon`) || strings.Contains(seg, "drarch") {
		t.Errorf("safelead row markup unexpected:\n%s", seg)
	}
	if seg := rowHTML("unsafelead"); !strings.Contains(seg, `class="drstop d-stop-unsafe" data-safe="0"`) || !strings.Contains(seg, `#i-stop`) || strings.Contains(seg, "drarch") {
		t.Errorf("unsafelead row markup unexpected:\n%s", seg)
	}
	if seg := rowHTML("nativebusy"); !strings.Contains(seg, `class="drstop d-stop-unsafe" data-safe="0"`) {
		t.Errorf("nativebusy row should render unsafe stop:\n%s", seg)
	}
	if seg := rowHTML("asleeplead"); strings.Contains(seg, "drstop") || strings.Contains(seg, "drarch") {
		t.Errorf("asleeplead should not render hover stop or archive:\n%s", seg)
	}
	if seg := rowHTML("archlead"); !strings.Contains(seg, `class="drarch unarch" data-act="unhide"`) || strings.Contains(seg, "drstop") {
		t.Errorf("archlead should keep hover unarchive without stop:\n%s", seg)
	}
	if seg := rowHTML("termrow"); strings.Contains(seg, "drstop") || strings.Contains(seg, "drarch") {
		t.Errorf("termrow should not render hover stop or archive:\n%s", seg)
	}
	_ = time.Second
}

func TestHoldConfirmClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; run node --test testdata/hold-confirm.test.cjs locally")
	}
	out, err := exec.Command(node, "--test", "testdata/hold-confirm.test.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("hold-confirm cases failed: %v\n%s", err, out)
	}
	t.Log(string(out))
}
