package wsdashboard

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRailLifecycle pins row placement and the distinction between
// heartbeat liveness, a stopped process, and deletion by the user.
func TestRailLifecycle(t *testing.T) {
	now := testNow
	beat := stampFor(now)
	old := stampFor(now.Add(-36 * time.Hour))
	in := func(reg Registration, hidden bool) railInput {
		reg.Project, reg.Harness, reg.RegisteredAt = "/w/repo", "claude", old
		return railInput{Session: Session{Registration: reg, Hidden: hidden}, Repo: "/w/repo", CanRevive: true}
	}
	sessions := []railInput{
		in(Registration{ReplyID: "activeon", Hosted: true, HeartbeatAt: beat}, false),
		in(Registration{ReplyID: "asleep", Hosted: true, HeartbeatAt: beat, HostStopped: true, HostExitReason: "stop"}, false),
		in(Registration{ReplyID: "archived", Hosted: true, HeartbeatAt: beat, HostStopped: true, HostExitReason: "stop"}, true),
		// A stale archived lead stays archived; its open record exempts it
		// from the prune.
		in(Registration{ReplyID: "stalearch", Hosted: true, HeartbeatAt: old}, true),
		in(Registration{ReplyID: "deleted", Hosted: true, HeartbeatAt: old, EndedAt: old}, true),
		in(Registration{ReplyID: "terminal", HeartbeatAt: old}, false),
		in(Registration{ReplyID: "terminalexit", HeartbeatAt: old, EndedAt: old}, false),
		in(Registration{ReplyID: "hostexit", Hosted: true, HeartbeatAt: beat, HostStopped: true, HostExitReason: "child_exited"}, false),
		// The holder can keep running while its heartbeat ages during sleep.
		in(Registration{ReplyID: "openstale", Hosted: true, HeartbeatAt: old}, false),
	}
	repos := map[string]railRepo{"/w/repo": {Read: true, Entries: []railWorktreeEntry{{Path: "/w/repo", Branch: "main"}}}}
	view := buildRailView(sessions, repos, nil, now)
	splitRailArchived(&view)
	wt := view.Workspaces[0].Worktrees[0]
	ids := func(rows []RailRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.ReplyID)
		}
		return out
	}
	if got := strings.Join(ids(wt.Sessions), ","); got != "activeon,asleep,hostexit,openstale,terminal,terminalexit" {
		t.Errorf("unfolded rows = %s", got)
	}
	if got := strings.Join(ids(wt.Archived), ","); got != "archived,stalearch" {
		t.Errorf("archived rows = %s, want archived,stalearch", got)
	}
	if got := strings.Join(ids(wt.Ended), ","); got != "deleted" {
		t.Errorf("deleted rows = %s, want deleted", got)
	}
	if view.Live != 3 || view.Workspaces[0].Live != 3 {
		t.Errorf("live counts = %d/%d, want 3 live registrations", view.Live, view.Workspaces[0].Live)
	}
	byID := map[string]RailRow{}
	for _, list := range [][]RailRow{wt.Sessions, wt.Archived, wt.Ended} {
		for _, r := range list {
			byID[r.ReplyID] = r
		}
	}
	if r := byID["archived"]; !r.Archived || r.Live || !r.Stopped() || r.Revive != "" || r.DeleteAt != 0 {
		t.Errorf("archived row = %+v, want archived, asleep, not live, no revive, no prune time", r)
	}
	if r := byID["asleep"]; r.Archived || !r.Stopped() {
		t.Errorf("asleep row = %+v, want stopped and not archived", r)
	}
	wantDel := now.Add(-36 * time.Hour).Add(RegistryRetention).UnixMilli()
	if r := byID["deleted"]; r.Archived || r.Lifecycle != SessionPendingDeletion || r.DeleteAt != wantDel {
		t.Errorf("deleted row = %+v, want pending deletion with prune time %d", r, wantDel)
	}
	for _, id := range []string{"terminal", "terminalexit", "hostexit"} {
		if r := byID[id]; r.Lifecycle != SessionStopped || r.DeleteAt != 0 {
			t.Errorf("%s = %+v, want stopped without a deletion countdown", id, r)
		}
	}
	if r := byID["openstale"]; r.Live || r.Lifecycle != SessionActiveOn || r.DeleteAt != 0 {
		t.Errorf("stale hosted row = %+v, want active lifecycle without a live heartbeat", r)
	}

	// A terminal heartbeat refresh changes the marker within the same group
	// and ordering position as the rows on either side of it.
	sessions[5].Session.HeartbeatAt = beat
	recovered := buildRailView(sessions, repos, nil, now)
	if got := strings.Join(ids(recovered.Workspaces[0].Worktrees[0].Sessions), ","); got != strings.Join(ids(wt.Sessions), ",") {
		t.Errorf("unfolded row order changed after heartbeat recovery: %s", got)
	}

	region, err := renderRailRegion(view, "ko")
	if err != nil {
		t.Fatal(err)
	}
	html := string(region.HTML)
	for _, want := range []string{
		`<div class="rfold" role="group" aria-label="접힌 세션" data-key="` + filepath.Clean("/w/repo") + `">`,
		`class="rfh" data-k="arch" aria-expanded="false" title="보관된 세션 2개"><svg class="i" aria-hidden="true"><use href="#i-archive"/></svg>보관 <span class="d-n">2</span></button>`,
		`class="rfh" data-k="del" aria-expanded="false" title="삭제된 세션 1개"><svg class="i" aria-hidden="true"><use href="#i-clock"/></svg>삭제됨 <span class="d-n">1</span></button>`,
		`<div class="rended" data-k="arch"><div class="rsessions"><div class="rs ended stopped archived hosted-row" data-rid="archived"`,
		`<div class="rended" data-k="del"><div class="rsessions"><div class="rs ended" data-rid="deleted"`,
		`<div class="rs stopped" data-rid="terminal"`,
		`<span class="rstate">중단됨</span>`,
		`class="drarch unarch" data-act="unhide" title="보관 해제 · 잠듦으로 돌아감, 쌓인 메일 전달"`,
		`<time class="when del" data-ms="` + strconv.FormatInt(wantDel, 10) + `" data-f="del"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rail lacks %q", want)
		}
	}
	activeRow, _, _ := strings.Cut(strings.SplitN(html, `data-rid="activeon"`, 2)[1], `data-rid="asleep"`)
	if !strings.Contains(activeRow, `class="drarch" data-act="hide" title="보관 · 프로세스를 끝내고 레일에서 숨김"`) {
		t.Errorf("an active hosted row lacks its archive button:\n%s", activeRow)
	}
	termRow := strings.SplitN(html, `data-rid="terminal"`, 2)[1]
	termRow, _, _ = strings.Cut(termRow, `<div class="rs`)
	if strings.Contains(termRow, "drarch") {
		t.Errorf("a terminal row offers archive:\n%s", termRow)
	}

	// No archived rows: the archived half is disabled, the deleted one is not.
	plain := RailView{Workspaces: []RailWorkspace{{Name: "repo", Path: "/w/repo", Repo: true, Worktrees: []RailWorktree{{Path: "/w/repo", Ended: []RailRow{{ReplyID: "gone"}}}}}}}
	region, err = renderRailRegion(plain, "en")
	if err != nil {
		t.Fatal(err)
	}
	if html := string(region.HTML); !strings.Contains(html, `data-k="arch" aria-expanded="false" title="0 archived" disabled>`) || strings.Contains(html, `data-k="del" aria-expanded="false" title="1 deleted" disabled`) {
		t.Errorf("an empty archived half must be disabled and the deleted half enabled:\n%s", html)
	}
}
