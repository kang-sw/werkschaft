package wsdashboard

import (
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestWhiteboardFoldMetadataOnReplyAndPlainTurns(t *testing.T) {
	ring := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "Do it"}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingTools, Tools: &wslive.ToolSummary{Calls: 1, DurationMs: 1_000, Batches: []wslive.ToolBatch{{Names: []string{"Read"}, Calls: 1, DurationMs: 1_000, CallArgs: []wslive.ToolCallArgs{{Name: "Read", ArgsPreview: "a.go", ID: "c1"}}}}}}),
		ringRecord(3, wslive.RingEntry{Kind: wslive.RingReply, Text: "Tool turn reply"}),
		ringRecord(4, wslive.RingEntry{Kind: wslive.RingTurn, Origin: wslive.OriginWhiteboard, Done: 1, DurationMs: 2_000, Reason: "completed"}),
		ringRecord(5, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "Just talk"}),
		ringRecord(6, wslive.RingEntry{Kind: wslive.RingReply, Text: "Plain turn reply"}),
		ringRecord(7, wslive.RingEntry{Kind: wslive.RingTurn, Origin: wslive.OriginWhiteboard, DurationMs: 3_000, Reason: "completed"}),
	}
	entries := string(renderIdleHistory(t, ring, "en")[regionEntries].HTML)

	toolKey := "turn-" + ring[1].Name
	if !strings.Contains(entries, `data-turn="`+toolKey+`"`) {
		t.Errorf("tool-turn reply must name its summary %q:\n%s", toolKey, entries)
	}
	plainKey := "turn-plain-" + ring[5].Name
	for _, want := range []string{
		`class="turn-plain-host" data-entry="` + ring[5].Name + `"`,
		`data-key="` + plainKey + `"`,
		`data-conv-turn-plain="1" hidden`,
		`data-turn="` + plainKey + `"`,
		`data-turn-line="` + plainKey + `"`,
		`data-conv-turn-folded hidden`,
		`Plain replies folded`,
	} {
		if !strings.Contains(entries, want) {
			t.Errorf("zero-tool turn missing %q:\n%s", want, entries)
		}
	}
	// The tool turn's summary is a real one, never the hidden plain kind.
	if got := strings.Count(entries, `data-conv-turn-plain="1"`); got != 1 {
		t.Errorf("exactly one plain summary expected, got %d", got)
	}
	// The hidden summary carries no entry of its own: the host owns the key.
	if got := strings.Count(entries, `data-entry="`+ring[5].Name+`"`); got != 2 {
		t.Errorf("host and reply share the reply's key once each, got %d", got)
	}
}

func TestWhiteboardToggleRendersOnlyOnACapableSession(t *testing.T) {
	render := func(capable bool) string {
		now := testNow
		lead := wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}}
		v := buildSessionView(pageInputs{
			Session:           liveSession("self", "/proj", now),
			Snapshot:          liveSnap(now, lead),
			Now:               now,
			WhiteboardCapable: capable,
		})
		v.Hosted = true
		page, err := renderSessionPage(v, mustRegions(t, v), false, "")
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	on := render(true)
	if !strings.Contains(on, `id="wbonly"`) || !strings.Contains(on, `aria-pressed="false"`) || !strings.Contains(on, `id="wbonly-hint" hidden`) {
		t.Errorf("capable session must offer the toggle, default OFF and hint hidden")
	}
	if off := render(false); strings.Contains(off, `id="wbonly"`) {
		t.Errorf("a session with no ambient must not render the toggle")
	}
}

func mustRegions(t *testing.T, v SessionView) regionSet {
	t.Helper()
	set, err := renderSessionRegions(v, newEntryCache())
	if err != nil {
		t.Fatal(err)
	}
	return set
}
