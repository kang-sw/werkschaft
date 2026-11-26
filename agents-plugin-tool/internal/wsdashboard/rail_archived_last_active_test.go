package wsdashboard

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestRailMessagesExtractsLastAtFromRing(t *testing.T) {
	dir := t.TempDir()
	ringDir := wslive.RingDir(dir)
	if err := wslive.AppendRing(ringDir, []wslive.RingEntry{
		{Kind: wslive.RingUser, Text: "hello"},
		{Kind: wslive.RingReply, Text: "world"},
		{Kind: wslive.RingTurn, Done: 2, DurationMs: 500},
	}); err != nil {
		t.Fatal(err)
	}
	page, err := wslive.ReadRingPage(ringDir, "", 10)
	if err != nil || len(page.Records) != 3 {
		t.Fatalf("ReadRingPage = %d, err=%v", len(page.Records), err)
	}
	wantLastAt := page.Records[len(page.Records)-1].At.UnixMilli()

	cache := newRailMessages()
	msg, ok := cache.lastResult("sess1", 1, ringDir)
	if !ok {
		t.Fatal("lastResult returned !ok")
	}
	if msg.Text != "world" || msg.User {
		t.Fatalf("msg = %+v, want Text=world User=false", msg)
	}
	if msg.LastAt != wantLastAt {
		t.Fatalf("msg.LastAt = %d, want newest ring entry %d", msg.LastAt, wantLastAt)
	}
	// Cached hit at same rev preserves LastAt with zero extra reads.
	readsBefore := cache.reads
	msg2, ok := cache.lastResult("sess1", 1, ringDir)
	if !ok || msg2.LastAt != wantLastAt || cache.reads != readsBefore {
		t.Fatalf("cached hit = %+v (ok=%v, reads=%d->%d), want preserved LastAt with 0 reads", msg2, ok, readsBefore, cache.reads)
	}
}

func TestArchivedRailRowLastActiveFallbackChain(t *testing.T) {
	now := testNow
	beat := stampFor(now) // heartbeat-polluted; must NEVER be used for LastActive
	tTool := now.Add(-10 * time.Minute)
	tRing := now.Add(-5 * time.Minute)
	tMeta := now.Add(-2 * time.Minute)
	tStopped := now.Add(-2 * time.Hour)
	tReg := now.Add(-24 * time.Hour)

	baseReg := func(id string) Registration {
		return Registration{
			ReplyID:      id,
			Project:      "/w/repo",
			Harness:      "claude",
			Hosted:       true,
			HeartbeatAt:  beat,
			RegisteredAt: stampFor(tReg),
		}
	}

	// 1. max(LastTool, Message.LastAt, AgentSessionMeta.LastActiveAt)
	t.Run("max of tool, ring, and meta", func(t *testing.T) {
		reg := baseReg("max-meta")
		reg.HostStoppedAt = stampFor(tStopped)
		row := railRow(railInput{
			Session:   Session{Registration: reg, Hidden: true},
			Snapshot:  &wslive.Snapshot{Lead: wslive.Lead{Activity: wslive.Activity{Batches: []wslive.BatchView{{CallCount: 1, EndedAt: tTool.UnixMilli()}}}}},
			Message:   railMessage{LastAt: tRing.UnixMilli()},
			AgentMeta: railHistoryMeta{"max-meta": {LastActiveAt: stampFor(tMeta)}},
		}, true, now)
		if !row.Archived {
			t.Fatal("expected archived row")
		}
		if row.LastActive != tMeta.UnixMilli() {
			t.Fatalf("LastActive = %d, want meta %d", row.LastActive, tMeta.UnixMilli())
		}
	})

	t.Run("ring beats older tool and stopped", func(t *testing.T) {
		reg := baseReg("max-ring")
		reg.HostStoppedAt = stampFor(tStopped)
		row := railRow(railInput{
			Session:  Session{Registration: reg, Hidden: true},
			Snapshot: &wslive.Snapshot{Lead: wslive.Lead{Activity: wslive.Activity{Batches: []wslive.BatchView{{CallCount: 1, EndedAt: tTool.UnixMilli()}}}}},
			Message:  railMessage{LastAt: tRing.UnixMilli()},
		}, true, now)
		if row.LastActive != tRing.UnixMilli() {
			t.Fatalf("LastActive = %d, want ring %d", row.LastActive, tRing.UnixMilli())
		}
	})

	t.Run("tool when ring and meta absent", func(t *testing.T) {
		reg := baseReg("tool-only")
		reg.HostStoppedAt = stampFor(tStopped)
		row := railRow(railInput{
			Session:  Session{Registration: reg, Hidden: true},
			Snapshot: &wslive.Snapshot{Lead: wslive.Lead{Activity: wslive.Activity{Batches: []wslive.BatchView{{CallCount: 1, EndedAt: tTool.UnixMilli()}}}}},
		}, true, now)
		if row.LastActive != tTool.UnixMilli() {
			t.Fatalf("LastActive = %d, want tool %d", row.LastActive, tTool.UnixMilli())
		}
	})

	// 2. HostStoppedAt fallback when no activity signals exist
	t.Run("fallback to HostStoppedAt ignoring newer HeartbeatAt", func(t *testing.T) {
		reg := baseReg("stopped-fallback")
		reg.HostStoppedAt = stampFor(tStopped)
		row := railRow(railInput{
			Session: Session{Registration: reg, Hidden: true},
		}, true, now)
		if row.LastActive != tStopped.UnixMilli() {
			t.Fatalf("LastActive = %d, want HostStoppedAt %d (not HeartbeatAt %d)", row.LastActive, tStopped.UnixMilli(), now.UnixMilli())
		}
	})

	// 3. RegisteredAt fallback when HostStoppedAt is also absent
	t.Run("fallback to RegisteredAt ignoring newer HeartbeatAt", func(t *testing.T) {
		reg := baseReg("reg-fallback")
		row := railRow(railInput{
			Session: Session{Registration: reg, Hidden: true},
		}, true, now)
		if row.LastActive != tReg.UnixMilli() {
			t.Fatalf("LastActive = %d, want RegisteredAt %d (not HeartbeatAt %d)", row.LastActive, tReg.UnixMilli(), now.UnixMilli())
		}
	})

	// Non-archived rows keep LastActive == 0.
	t.Run("non-archived leaves LastActive zero", func(t *testing.T) {
		reg := baseReg("live-row")
		row := railRow(railInput{
			Session: Session{Registration: reg, Hidden: false},
			Message: railMessage{LastAt: tRing.UnixMilli()},
		}, true, now)
		if row.Archived || row.LastActive != 0 {
			t.Fatalf("live row Archived=%v LastActive=%d, want false/0", row.Archived, row.LastActive)
		}
	})
}

func TestArchivedRowsSortByLastActiveDescendingAndRenderRelAgo(t *testing.T) {
	now := testNow
	// Give the oldest-active session the NEWEST heartbeat so we prove sorting
	// follows LastActive descending, not heartbeat-polluted LastSeen().
	tOldest := now.Add(-4 * 24 * time.Hour)
	tMiddle := now.Add(-2 * time.Hour)
	tNewest := now.Add(-3 * time.Minute)

	mk := func(id string, heartbeat time.Time, ringAt time.Time) railInput {
		return railInput{
			Session: Session{
				Registration: Registration{
					ReplyID:      id,
					Project:      "/w/repo",
					Harness:      "claude",
					Hosted:       true,
					HeartbeatAt:  stampFor(heartbeat),
					RegisteredAt: stampFor(now.Add(-10 * 24 * time.Hour)),
				},
				Hidden: true,
			},
			Repo:    "/w/repo",
			Message: railMessage{LastAt: ringAt.UnixMilli()},
		}
	}

	sessions := []railInput{
		mk("arch-old", now, tOldest),
		mk("arch-new", now.Add(-5*time.Hour), tNewest),
		mk("arch-mid", now.Add(-1*time.Hour), tMiddle),
	}
	repos := map[string]railRepo{"/w/repo": {Read: true, Entries: []railWorktreeEntry{{Path: "/w/repo", Branch: "main"}}}}
	view := buildRailView(sessions, repos, nil, now)
	splitRailArchived(&view)

	wt := view.Workspaces[0].Worktrees[0]
	if len(wt.Archived) != 3 {
		t.Fatalf("len(wt.Archived) = %d, want 3", len(wt.Archived))
	}
	gotOrder := []string{wt.Archived[0].ReplyID, wt.Archived[1].ReplyID, wt.Archived[2].ReplyID}
	wantOrder := []string{"arch-new", "arch-mid", "arch-old"}
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("archived order = %v, want %v", gotOrder, wantOrder)
	}

	region, err := renderRailRegion(view, "ko")
	if err != nil {
		t.Fatal(err)
	}
	html := string(region.HTML)
	wantSlot := `<time class="when t" data-ms="` + strconv.FormatInt(tNewest.UnixMilli(), 10) + `" data-f="relago" data-tl="마지막 활성">`
	if !strings.Contains(html, wantSlot) {
		t.Errorf("rail HTML missing archived relago slot %q", wantSlot)
	}
	wantHover := `<span>마지막 활성 <time class="t" data-ms="` + strconv.FormatInt(tNewest.UnixMilli(), 10) + `" data-f="relago">`
	if !strings.Contains(html, wantHover) {
		t.Errorf("rail HTML missing archived hover relago %q", wantHover)
	}
}
