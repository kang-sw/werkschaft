package wsdashboard

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type railHistoryMeta map[string]AgentSessionMeta

func (m railHistoryMeta) SessionMeta(replyID string) (AgentSessionMeta, bool) {
	meta, found := m[replyID]
	return meta, found
}

func TestRailRetiredPiHasNoDeletionCountdown(t *testing.T) {
	endedAt := testNow.Add(-time.Hour)
	ended := stampFor(endedAt)
	meta := railHistoryMeta{"handedoff": {Backend: "pi"}}
	for _, reg := range []Registration{
		{ReplyID: "retiredlead", Harness: "pi", Hosted: true, HeartbeatAt: ended, EndedAt: ended},
		{ReplyID: "retiredworker", Harness: "pi", Agent: true, HeartbeatAt: ended, EndedAt: ended},
		{ReplyID: "handedoff", Harness: "pi", HeartbeatAt: ended, EndedAt: ended},
	} {
		row := railRow(railInput{Session: Session{Registration: reg}, AgentMeta: meta}, false, testNow)
		if row.DeleteAt != 0 {
			t.Errorf("%s has deletion deadline %d, want none for retained Pi history", row.ReplyID, row.DeleteAt)
		}
	}
	standalone := Registration{ReplyID: "standalone", Harness: "pi", HeartbeatAt: ended, EndedAt: ended}
	row := railRow(railInput{Session: Session{Registration: standalone}, AgentMeta: meta}, false, testNow)
	if want := endedAt.Add(RegistryRetention).UnixMilli(); row.DeleteAt != want {
		t.Errorf("standalone terminal deletion deadline = %d, want %d", row.DeleteAt, want)
	}
}

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
	if got := strings.Join(ids(wt.Sessions), ","); got != "activeon,asleep,hostexit,openstale" {
		t.Errorf("unfolded rows = %s", got)
	}
	if got := strings.Join(ids(wt.Archived), ","); got != "archived,stalearch" {
		t.Errorf("archived rows = %s, want archived,stalearch", got)
	}
	if got := strings.Join(ids(wt.Ended), ","); got != "deleted,terminal,terminalexit" {
		t.Errorf("deleted rows = %s, want deleted,terminal,terminalexit", got)
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
	for _, id := range []string{"terminal", "terminalexit"} {
		if r := byID[id]; r.Lifecycle != SessionPendingDeletion || r.DeleteAt != wantDel {
			t.Errorf("%s = %+v, want pending deletion with countdown", id, r)
		}
	}
	if r := byID["hostexit"]; r.Lifecycle != SessionStopped || r.DeleteAt != 0 {
		t.Errorf("hostexit = %+v, want stopped without a deletion countdown", r)
	}
	if r := byID["openstale"]; r.Live || r.Lifecycle != SessionActiveOn || r.DeleteAt != 0 {
		t.Errorf("stale hosted row = %+v, want active lifecycle without a live heartbeat", r)
	}

	// A terminal heartbeat refresh moves the session back to the regular list.
	sessions[5].Session.HeartbeatAt = beat
	recovered := buildRailView(sessions, repos, nil, now)
	if got := strings.Join(ids(recovered.Workspaces[0].Worktrees[0].Sessions), ","); got != "activeon,asleep,hostexit,openstale,terminal" {
		t.Errorf("unfolded rows after heartbeat recovery: %s", got)
	}

	region, err := renderRailRegion(view, "ko")
	if err != nil {
		t.Fatal(err)
	}
	html := string(region.HTML)
	for _, want := range []string{
		`<div class="rfold" role="group" aria-label="접힌 세션" data-key="` + filepath.Clean("/w/repo") + `">`,
		`class="rfh" data-k="arch" aria-haspopup="dialog" aria-expanded="false" title="보관된 세션 2개"><svg class="i" aria-hidden="true"><use href="#i-archive"/></svg>보관 <span class="d-n">2</span></button>`,
		`class="rfh" data-k="del" aria-haspopup="dialog" aria-expanded="false" title="삭제된 세션 3개"><svg class="i" aria-hidden="true"><use href="#i-clock"/></svg>삭제됨 <span class="d-n">3</span></button>`,
		`<div class="rended" data-k="arch"><div class="rsessions"><div class="rs ended stopped archived hosted-row" data-rid="archived"`,
		`<div class="rended" data-k="del"><div class="rsessions"><div class="rs ended" data-rid="deleted"`,
		`<div class="rs stopped hosted-row" data-rid="hostexit"`,
		`<span class="rstate">중단됨</span>`,
		`class="drarch unarch" data-act="unhide" title="보관 해제 · 잠듦으로 돌아감, 쌓인 메일 전달"`,
		`<time class="when del" data-ms="` + strconv.FormatInt(wantDel, 10) + `" data-f="del"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rail lacks %q", want)
		}
	}
	activeRow, _, _ := strings.Cut(strings.SplitN(html, `data-rid="activeon"`, 2)[1], `data-rid="asleep"`)
	if !strings.Contains(activeRow, `class="drstop d-stop-unsafe" data-safe="0" data-hold-mode="radial" title="중지 · 길게 눌러 실행 중인 프로세스 종료"`) {
		t.Errorf("an active hosted row lacks its stop button:\n%s", activeRow)
	}
	if strings.Contains(activeRow, "drarch") {
		t.Errorf("an active hosted row must not offer hover archive:\n%s", activeRow)
	}
	asleepRow, _, _ := strings.Cut(strings.SplitN(html, `data-rid="asleep"`, 2)[1], `data-rid="hostexit"`)
	if strings.Contains(asleepRow, "drstop") || strings.Contains(asleepRow, "drarch") {
		t.Errorf("an asleep hosted row must not offer hover stop or archive:\n%s", asleepRow)
	}
	termRow := strings.SplitN(html, `data-rid="terminal"`, 2)[1]
	termRow, _, _ = strings.Cut(termRow, `<div class="rs`)
	if strings.Contains(termRow, "drarch") || strings.Contains(termRow, "drstop") {
		t.Errorf("a terminal row offers archive or stop:\n%s", termRow)
	}

	// No archived rows: the archived half is disabled, the deleted one is not.
	plain := RailView{Workspaces: []RailWorkspace{{Name: "repo", Path: "/w/repo", Repo: true, Worktrees: []RailWorktree{{Path: "/w/repo", Ended: []RailRow{{ReplyID: "gone"}}}}}}}
	region, err = renderRailRegion(plain, "en")
	if err != nil {
		t.Fatal(err)
	}
	if html := string(region.HTML); !strings.Contains(html, `data-k="arch" aria-haspopup="dialog" aria-expanded="false" title="0 archived" disabled>`) || strings.Contains(html, `data-k="del" aria-haspopup="dialog" aria-expanded="false" title="1 deleted" disabled`) {
		t.Errorf("an empty archived half must be disabled and the deleted half enabled:\n%s", html)
	}
}

func TestRailLifecycleDragMarkup(t *testing.T) {
	repo := filepath.Clean("/w/repo")
	emptyFold := RailView{
		Workspaces: []RailWorkspace{{
			Name: "repo",
			Path: repo,
			Repo: true,
			Worktrees: []RailWorktree{{
				Path:   repo,
				Branch: "main",
				Sessions: []RailRow{
					{
						ReplyID:         "act",
						Live:            true,
						Lifecycle:       SessionActiveOn,
						Hosted:          true,
						HasProcess:      true,
						BackgroundTasks: 1,
						Starred:         true,
					},
					{
						ReplyID:     "execown",
						Live:        true,
						Lifecycle:   SessionActiveOn,
						Hosted:      true,
						OwnExecJobs: 2,
					},
				},
			}},
		}},
	}
	emptyReg, err := renderRailRegion(emptyFold, "en")
	if err != nil {
		t.Fatal(err)
	}
	emptyHTML := string(emptyReg.HTML)
	if !strings.Contains(emptyHTML, `<div class="rfold" role="group" aria-label="Folded sessions" data-key="`+repo+`" data-empty="1">`) {
		t.Fatalf("expected 0/0 fold to include data-empty=1:\n%s", emptyHTML)
	}
	actSlice, _, _ := strings.Cut(strings.SplitN(emptyHTML, `<div class="rs hosted-row" data-rid="act" data-asks="" data-star="1">`, 2)[1], `data-rid="execown"`)
	for _, want := range []string{`data-drag="1"`, `data-host="1"`, `data-star-was="1"`, `data-proc="1"`, `data-bg="1"`} {
		if !strings.Contains(actSlice, want) {
			t.Errorf("act .drgrip missing %s:\n%s", want, actSlice)
		}
	}
	execSlice := strings.SplitN(emptyHTML, `data-rid="execown"`, 2)[1]
	if !strings.Contains(execSlice, `data-bg="1"`) || strings.Contains(execSlice, `data-proc="1"`) || strings.Contains(execSlice, `data-star-was="1"`) {
		t.Errorf("execown .drgrip mismatch:\n%s", execSlice)
	}

	full := RailView{
		Workspaces: []RailWorkspace{{
			Name: "repo",
			Path: repo,
			Repo: true,
			Worktrees: []RailWorktree{{
				Path:   repo,
				Branch: "main",
				Sessions: []RailRow{{
					ReplyID:   "parent",
					Live:      true,
					Lifecycle: SessionActiveOn,
					Hosted:    true,
					Children: []RailRow{{
						ReplyID:   "child",
						Live:      true,
						Lifecycle: SessionActiveOn,
						Child:     true,
						Group:     repo,
						Hosted:    true,
					}},
				}},
				Archived: []RailRow{{
					ReplyID:   "archstar",
					Lifecycle: SessionArchived,
					Archived:  true,
					Hosted:    true,
					Starred:   true,
				}},
				Ended: []RailRow{
					{
						ReplyID:   "delopen",
						Lifecycle: SessionPendingDeletion,
						Hosted:    true,
						Revive:    ReviveReopen,
					},
					{
						ReplyID:       "delblocked",
						Lifecycle:     SessionPendingDeletion,
						Revive:        ReviveTakeover,
						ReviveBlocked: ReviveNoSession,
					},
				},
			}},
		}},
	}
	fullReg, err := renderRailRegion(full, "en")
	if err != nil {
		t.Fatal(err)
	}
	fullHTML := string(fullReg.HTML)
	if !strings.Contains(fullHTML, `<div class="rfold" role="group" aria-label="Folded sessions" data-key="`+repo+`">`) {
		t.Errorf("non-empty fold must omit data-empty:\n%s", fullHTML)
	}
	if !strings.Contains(fullHTML, `<div class="rs ended stopped archived hosted-row" data-rid="archstar" data-asks=""><span class="drgrip" aria-hidden="true" data-drag="1" data-host="1" data-star-was="1">`) {
		t.Errorf("archstar div.rs + .drgrip markup mismatch:\n%s", fullHTML)
	}
	if !strings.Contains(fullHTML, `<div class="rs ended" data-rid="delopen" data-asks="" data-star="0"><span class="drgrip" aria-hidden="true" data-drag="1" data-host="1" data-revive="reopen">`) {
		t.Errorf("delopen div.rs + .drgrip markup mismatch:\n%s", fullHTML)
	}
	blockedSlice := strings.SplitN(fullHTML, `data-rid="delblocked"`, 2)[1]
	if !strings.Contains(blockedSlice, `data-revive="takeover" data-revive-blocked="nosession"`) || strings.Contains(strings.SplitN(blockedSlice, `<div class="drbody"`, 2)[0], `data-drag="1"`) {
		t.Errorf("delblocked .drgrip mismatch:\n%s", blockedSlice)
	}
}
