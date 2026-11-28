package wsconvidx

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wslive"
)

func makeRingRec(name, kind string, ts int64, text string) wslive.RingRecord {
	return wslive.RingRecord{
		Name: name,
		At:   time.UnixMilli(ts).UTC(),
		RingEntry: wslive.RingEntry{
			Kind: kind,
			Text: text,
		},
	}
}

func makeMailRec(name string, ts int64, dir, peer, title, text string) wslive.RingRecord {
	return wslive.RingRecord{
		Name: name,
		At:   time.UnixMilli(ts).UTC(),
		RingEntry: wslive.RingEntry{
			Kind:      "mail",
			Direction: dir,
			Peer:      peer,
			Title:     title,
			Text:      text,
		},
	}
}

func TestStoreFTSAndQueryFeatures(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := Open(dir, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	recsA := []wslive.RingRecord{
		makeRingRec("0001000-001-a", "user", 1000, "please refactor the authentication middleware"),
		makeRingRec("0002000-002-b", "reply", 2000, "Refactored authentication middleware to validate JWT claims cleanly."),
		makeMailRec("0003000-003-c", 3000, "recv", "worker-1", "Auth audit report", "Found missing expiry check in middleware."),
	}
	if err := st.ApplyDelta(ctx, "sess-alpha", "/ws/proj-a", recsA, nil); err != nil {
		t.Fatalf("ApplyDelta sess-alpha: %v", err)
	}
	recsB := []wslive.RingRecord{
		makeRingRec("0004000-001-x", "user", 4000, "authentication timeout on billing service"),
	}
	if err := st.ApplyDelta(ctx, "sess-beta", "/ws/proj-b", recsB, nil); err != nil {
		t.Fatalf("ApplyDelta sess-beta: %v", err)
	}

	// Search across all sessions ordered by time.
	hits, err := st.Search(ctx, SearchParams{Query: "authentication", Order: "time"})
	if err != nil {
		t.Fatalf("Search time: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("Search authentication hits = %d, want 3: %+v", len(hits), hits)
	}
	if hits[0].EntryID != "0004000-001-x" || hits[1].EntryID != "0002000-002-b" || hits[2].EntryID != "0001000-001-a" {
		t.Fatalf("unexpected time order: %+v", hits)
	}

	// Filter by session_ids + kind + mail metadata search.
	mailHits, err := st.Search(ctx, SearchParams{
		Query:      "audit expiry",
		SessionIDs: []string{"sess-alpha"},
		Kinds:      []string{"mail"},
	})
	if err != nil {
		t.Fatalf("Search mail: %v", err)
	}
	if len(mailHits) != 1 || mailHits[0].Direction != "recv" || mailHits[0].Peer != "worker-1" || mailHits[0].Title != "Auth audit report" {
		t.Fatalf("mail hit mismatch: %+v", mailHits)
	}

	// FTS syntax special characters fall back cleanly without error.
	if _, err := st.Search(ctx, SearchParams{Query: `auth:middleware (JWT) "unclosed`}); err != nil {
		t.Fatalf("Search with special FTS chars failed: %v", err)
	}

	// Since/Until bounds.
	bounded, err := st.Search(ctx, SearchParams{Query: "authentication", SinceMs: 1500, UntilMs: 3500})
	if err != nil {
		t.Fatalf("Search bounded: %v", err)
	}
	if len(bounded) != 1 || bounded[0].EntryID != "0002000-002-b" {
		t.Fatalf("bounded hits = %+v, want only 0002000-002-b", bounded)
	}

	// Reopen verifies persistence across restarts.
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	st2, err := Open(dir, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	stats, err := st2.SessionStats(ctx, []string{"sess-alpha", "sess-beta"})
	if err != nil {
		t.Fatalf("SessionStats: %v", err)
	}
	if stats["sess-alpha"].EntryCount != 3 || stats["sess-alpha"].HighWater != "0003000-003-c" || stats["sess-beta"].EntryCount != 1 {
		t.Fatalf("unexpected stats after reopen: %+v", stats)
	}
}

func TestStoreOrderIndependentEvictionAndRecommit(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// 1. Eviction delivered BEFORE its add (out-of-order observer delivery):
	// tombstone suppresses the late add so no stale row appears.
	if err := st.ApplyDelta(ctx, "sess-race", "/ws/a", nil, []string{"0001000-001-evict"}); err != nil {
		t.Fatalf("ApplyDelta evict first: %v", err)
	}
	if err := st.ApplyDelta(ctx, "sess-race", "/ws/a", []wslive.RingRecord{
		makeRingRec("0001000-001-evict", "user", 1000, "ghost evicted text"),
		makeRingRec("0002000-002-live", "reply", 2000, "live text initial"),
	}, nil); err != nil {
		t.Fatalf("ApplyDelta late add: %v", err)
	}
	hits, err := st.Search(ctx, SearchParams{Query: "ghost", SessionID: "sess-race"})
	if err != nil {
		t.Fatalf("Search ghost: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("evicted entry resurrected by out-of-order add: %+v", hits)
	}

	// 2. Idempotent re-commit of the same entry_id updates text and FTS cleanly.
	if err := st.ApplyDelta(ctx, "sess-race", "/ws/a", []wslive.RingRecord{
		makeRingRec("0002000-002-live", "reply", 2000, "live text recommitted updated"),
	}, nil); err != nil {
		t.Fatalf("ApplyDelta recommit: %v", err)
	}
	oldHits, err := st.Search(ctx, SearchParams{Query: "initial", SessionID: "sess-race"})
	if err != nil || len(oldHits) != 0 {
		t.Fatalf("stale FTS token survived recommit: %+v (err=%v)", oldHits, err)
	}
	newHits, err := st.Search(ctx, SearchParams{Query: "recommitted", SessionID: "sess-race"})
	if err != nil || len(newHits) != 1 {
		t.Fatalf("recommitted text not indexed: %+v (err=%v)", newHits, err)
	}

	// 3. ReconcileSession drops entries no longer in ring and keeps live ones.
	if err := st.ReconcileSession(ctx, "sess-race", "/ws/a", []wslive.RingRecord{
		makeRingRec("0003000-003-final", "user", 3000, "only surviving record"),
	}); err != nil {
		t.Fatalf("ReconcileSession: %v", err)
	}
	hist, err := st.History(ctx, HistoryParams{SessionID: "sess-race"})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist.Items) != 1 || hist.Items[0].EntryID != "0003000-003-final" {
		t.Fatalf("reconciled history = %+v, want only 0003000-003-final", hist.Items)
	}

	// 4. DeleteSession removes everything.
	if err := st.DeleteSession(ctx, "sess-race"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	indexed, _, err := st.SessionIndexed(ctx, "sess-race")
	if err != nil || indexed {
		t.Fatalf("after DeleteSession: indexed=%v err=%v", indexed, err)
	}
	hitsAfter, err := st.Search(ctx, SearchParams{Query: "surviving"})
	if err != nil || len(hitsAfter) != 0 {
		t.Fatalf("FTS rows leaked after DeleteSession: %+v (err=%v)", hitsAfter, err)
	}
}

func TestStore64KiBClipPaginationPrefixAndWindow(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// Build a >64 KiB multi-byte UTF-8 text (Korean characters are 3 bytes each).
	bigText := strings.Repeat("가나다라마바사아자차", 2600) // ~78 KiB
	recs := []wslive.RingRecord{
		makeRingRec("1000-aaa1", "user", 1000, "first turn"),
		makeRingRec("1000-aaa2", "reply", 2000, "second turn"),
		makeRingRec("2000-bbb1", "reply", 3000, bigText),
		makeRingRec("3000-ccc1", "user", 4000, "fourth turn"),
		makeRingRec("4000-ddd1", "reply", 5000, "fifth turn"),
	}
	if err := st.ReconcileSession(ctx, "sess-read", "/ws/r", recs); err != nil {
		t.Fatalf("ReconcileSession: %v", err)
	}

	// Ambiguous prefix fails cleanly.
	if _, err := st.ReadEntry(ctx, "sess-read", "1000-aaa", 0, 1024); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous prefix error, got: %v", err)
	}

	// Unambiguous prefix succeeds and clips to MaxEntryBytes (64 KiB) with valid UTF-8 and StoredTruncated=true.
	r1, err := st.ReadEntry(ctx, "sess-read", "2000-bbb", 0, 16384)
	if err != nil {
		t.Fatalf("ReadEntry prefix page 1: %v", err)
	}
	if !r1.Entry.StoredTruncated {
		t.Fatalf("expected StoredTruncated=true for >64KiB entry")
	}
	if r1.Entry.FullBytes != len(bigText) {
		t.Fatalf("FullBytes = %d, want %d", r1.Entry.FullBytes, len(bigText))
	}
	if !utf8.ValidString(r1.Entry.Text) || len(r1.Entry.Text) > 16384 || !r1.HasMore {
		t.Fatalf("page 1 invalid: valid=%v len=%d hasMore=%v", utf8.ValidString(r1.Entry.Text), len(r1.Entry.Text), r1.HasMore)
	}

	// Paginate until !HasMore and confirm total bytes <= MaxEntryBytes.
	total := len(r1.Entry.Text)
	offset := r1.NextOffset
	for r1.HasMore {
		next, err := st.ReadEntry(ctx, "sess-read", "2000-bbb1", offset, 16384)
		if err != nil {
			t.Fatalf("ReadEntry page offset=%d: %v", offset, err)
		}
		if !utf8.ValidString(next.Entry.Text) {
			t.Fatalf("invalid UTF-8 slice at offset=%d", offset)
		}
		total += len(next.Entry.Text)
		if next.NextOffset <= offset && next.HasMore {
			t.Fatalf("pagination did not advance at offset=%d", offset)
		}
		offset = next.NextOffset
		r1 = next
	}
	if total > MaxEntryBytes || total < MaxEntryBytes-4 {
		t.Fatalf("total paginated stored bytes = %d, expected near %d", total, MaxEntryBytes)
	}

	// ReadWindow around 2000-bbb1 with context_before=1, context_after=1 and small max_bytes.
	win, err := st.ReadWindow(ctx, "sess-read", "2000-bbb", 1, 1, 1024)
	if err != nil {
		t.Fatalf("ReadWindow: %v", err)
	}
	if win.TargetEntryID != "2000-bbb1" || len(win.Entries) != 3 {
		t.Fatalf("window = target:%s len:%d, want 2000-bbb1 len:3", win.TargetEntryID, len(win.Entries))
	}
	if !win.WindowClipped || win.TotalBytes > 1024 {
		t.Fatalf("expected WindowClipped=true and TotalBytes<=1024, got clipped=%v bytes=%d", win.WindowClipped, win.TotalBytes)
	}

	// History cursor pagination: default returns newest window in chronological order;
	// before pages backward, after pages forward.
	h1, err := st.History(ctx, HistoryParams{SessionID: "sess-read", Limit: 2})
	if err != nil || len(h1.Items) != 2 || !h1.HasMore || h1.Items[0].EntryID != "3000-ccc1" || h1.Items[1].EntryID != "4000-ddd1" {
		t.Fatalf("History page 1: items=%+v hasMore=%v err=%v", h1.Items, h1.HasMore, err)
	}
	hPrev, err := st.History(ctx, HistoryParams{SessionID: "sess-read", Before: h1.Items[0].EntryID, Limit: 10})
	if err != nil || len(hPrev.Items) != 3 || hPrev.HasMore {
		t.Fatalf("History before: items=%+v hasMore=%v err=%v", hPrev.Items, hPrev.HasMore, err)
	}
	hNext, err := st.History(ctx, HistoryParams{SessionID: "sess-read", After: hPrev.Items[0].EntryID, Limit: 2})
	if err != nil || len(hNext.Items) != 2 || !hNext.HasMore || hNext.Items[0].EntryID != "1000-aaa2" {
		t.Fatalf("History after: items=%+v hasMore=%v err=%v", hNext.Items, hNext.HasMore, err)
	}
}
