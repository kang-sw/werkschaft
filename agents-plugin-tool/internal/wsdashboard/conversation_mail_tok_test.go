package wsdashboard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

func TestConversationMailAndWakeTokenCount(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	selfReply := wsmailbox.ReplyID("self-session-key")
	peerReply := wsmailbox.ReplyID("peer-session-key")
	peerAddr := wsmailbox.ReplyIDPrefix + peerReply

	body := strings.Repeat("m", 400) // 400 B -> +100
	delivery := fmt.Sprintf(
		"[ws mailbox] 1 message(s) arrived for session_key id:%s. They were already received through mailbox.recv; do not call it again for these.\n\nmail from %s:\n%s",
		selfReply, peerAddr, body,
	)

	nudge1 := fmt.Sprintf(
		"[ws mailbox] 2 message(s) arrived for session_key id:%s. They were already received through mailbox.recv; do not call it again for these.\n\nmail from %s:\nWhiteboard lead nudge (overdue).\n- Topic One; owner alice is overdue\n%s",
		selfReply, wsmailbox.SystemLabel, strings.Repeat("x", 120),
	)
	nudge2 := fmt.Sprintf(
		"mail from %s:\nWhiteboard lead nudge (unblocked).\n- Topic Two: ready to proceed; waiting on review\n%s",
		wsmailbox.SystemLabel, strings.Repeat("y", 160),
	)

	unmatchedWake := strings.Repeat("w", 200)

	ring := []wslive.RingRecord{
		// 1. Matched peer mail + wake (peermailentry)
		{
			Name: "20261124T090000.000000000Z-0001.json",
			At:   t0,
			RingEntry: wslive.RingEntry{
				Kind:       wslive.RingMail,
				Direction:  wslive.MailRecv,
				Peer:       peerAddr,
				Title:      "matched mail",
				HistoryKey: "h-matched.json",
				BodyBytes:  len(body),
			},
		},
		{
			Name: "20261124T090001.000000000Z-0002.json",
			At:   t0.Add(time.Second),
			RingEntry: wslive.RingEntry{
				Kind:      wslive.RingWake,
				Cause:     "mail",
				Text:      delivery,
				BodyBytes: len(delivery),
			},
		},
		// 2. Standalone mail with body (details.mailline)
		{
			Name: "20261124T090002.000000000Z-0003.json",
			At:   t0.Add(2 * time.Second),
			RingEntry: wslive.RingEntry{
				Kind:       wslive.RingMail,
				Direction:  wslive.MailSent,
				Peer:       "worker@worktree",
				Title:      "sent mail",
				HistoryKey: "h-sent.json",
				BodyBytes:  800,
			},
		},
		// 3. Evicted mail retaining BodyBytes (div.mailline.nobody)
		{
			Name: "20261124T090003.000000000Z-0004.json",
			At:   t0.Add(3 * time.Second),
			RingEntry: wslive.RingEntry{
				Kind:       wslive.RingMail,
				Direction:  wslive.MailRecv,
				Peer:       "worker@worktree",
				Title:      "evicted with bytes",
				HistoryKey: "h-evicted.json",
				BodyBytes:  1024, // 1.0 KB -> +256
			},
		},
		// 4. Legacy evicted mail with 0 BodyBytes (div.mailline.nobody, empty .au-tok)
		{
			Name: "20261124T090004.000000000Z-0005.json",
			At:   t0.Add(4 * time.Second),
			RingEntry: wslive.RingEntry{
				Kind:      wslive.RingMail,
				Direction: wslive.MailRecv,
				Peer:      "worker@worktree",
				Title:     "legacy nobody",
			},
		},
		// 5. Unmatched mail wake with truncated text and larger persisted BodyBytes
		{
			Name: "20261124T090005.000000000Z-0006.json",
			At:   t0.Add(5 * time.Second),
			RingEntry: wslive.RingEntry{
				Kind:      wslive.RingWake,
				Cause:     "mail",
				Text:      unmatchedWake,
				Truncated: true,
				BodyBytes: 2048, // 2.0 KB -> +512
			},
		},
		// 6. Collapsed board-nudge run (2 nudges summed)
		{
			Name: "20261124T090006.000000000Z-0007.json",
			At:   t0.Add(6 * time.Second),
			RingEntry: wslive.RingEntry{
				Kind:      wslive.RingWake,
				Cause:     "mail",
				Text:      nudge1,
				BodyBytes: len(nudge1),
			},
		},
		{
			Name: "20261124T090007.000000000Z-0008.json",
			At:   t0.Add(7 * time.Second),
			RingEntry: wslive.RingEntry{
				Kind:      wslive.RingWake,
				Cause:     "mail",
				Text:      nudge2,
				BodyBytes: len(nudge2),
			},
		},
	}

	page := PageContent{
		MailBodies: map[string]string{
			"h-matched.json": body,
			"h-sent.json":    strings.Repeat("s", 800),
		},
	}

	v := buildSessionView(pageInputs{
		Session:  liveSession(selfReply, "/proj", t0),
		Snapshot: liveSnap(t0, wslive.Lead{}),
		Page:     page,
		Ring:     ring,
		Now:      t0.Add(time.Minute),
	})
	set, err := renderSessionRegions(v, newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	html := string(set[regionEntries].HTML)

	wantTok := func(bytes int) string {
		est := estContextTokens(0, bytes)
		return fmt.Sprintf(`<span class="au-tok" title="~%s tok (%s)">&#43;%s</span>`, compactTokens(est), fmtBytes(int64(bytes)), compactTokens(est))
	}

	cases := []struct {
		name string
		want string
	}{
		{"matched peermailentry uses delivery bytes", wantTok(len(delivery))},
		{"standalone mailline", wantTok(800)},
		{"evicted mailline.nobody uses persisted BodyBytes", wantTok(1024)},
		{"legacy evicted mailline omits title and text", `<span class="au-tok"></span>`},
		{"unmatched mail wake uses pre-truncation BodyBytes", wantTok(2048)},
		{"collapsed board nudge sums payload bytes", wantTok(len(nudge1) + len(nudge2))},
	}
	for _, tc := range cases {
		if !strings.Contains(html, tc.want) {
			t.Errorf("%s: missing %q in rendered HTML:\n%s", tc.name, tc.want, html)
		}
	}
	if strings.Count(html, `class="row-time au-right"`) != 6 {
		t.Errorf("expected 6 visible mail/mail-wake rows with row-time au-right, got %d", strings.Count(html, `class="row-time au-right"`))
	}
}
