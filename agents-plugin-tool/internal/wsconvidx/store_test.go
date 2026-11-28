package wsconvidx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wssqlite"
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

func TestStoreNonASCIIAndFTSInjectionCorpus(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// Include UTF-8 runes whose encoding contains 0x85 or 0xA0 continuation bytes
	// (e.g. 'à'=C3 A0, 'Å'=C3 85, Korean '다'=EB 8B A4 / '가'=EA B0 80 / '나'=EB 82 98, '사'=EC 82 AC,
	// plus '가' / '다중화', and Turkish 'İ' for makeSnippet length safety).
	recs := []wslive.RingRecord{
		makeRingRec("1000-001", "user", 1000, "인증 다중화 설정을 확인해 주세요"),
		makeRingRec("1000-002", "reply", 2000, "Café à la carte and Ångström physics notes"),
		makeRingRec("1000-003", "reply", 3000, "alpha beta gamma token with col:term and auth* wildcard"),
		makeRingRec("1000-004", "reply", 4000, strings.Repeat("İ", 120)+" needle_after_dotted_I "+strings.Repeat("x", 120)),
		makeRingRec("1000-005", "reply", 5000, "literal NEAR(delta epsilon, 1) expression inside code"),
	}
	if err := st.ApplyDelta(ctx, "sess-utf8", "/ws/u", recs, nil); err != nil {
		t.Fatalf("ApplyDelta: %v", err)
	}

	// Non-ASCII queries containing 0xA0 / 0x85 continuation bytes must match cleanly.
	for _, tc := range []struct {
		name   string
		query  string
		wantID string
	}{
		{"hangul_da", "다중화", "1000-001"},
		{"hangul_multi", "인증 다중화", "1000-001"},
		{"latin_agrave", "à", "1000-002"},
		{"latin_aring", "Ångström", "1000-002"},
		// FTS injection / special-syntax corpus with asserted hit results.
		// NEAR(...) is quoted as literal tokens rather than executing as an FTS5 operator.
		{"fts_near_neutralized", "NEAR(delta epsilon, 1)", "1000-005"},
		{"fts_column_colon", "col:term", "1000-003"},
		{"fts_star", "auth*", "1000-003"},
		{"fts_unbalanced_quote", `"alpha beta`, "1000-003"},
		{"fts_leading_or", "OR alpha", "1000-003"},
		{"fts_trailing_or", "alpha OR", "1000-003"},
		{"fts_leading_trailing_and_not", "AND NOT beta AND", "1000-003"},
		{"dotted_I_snippet", "needle_after_dotted_I", "1000-004"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := st.Search(ctx, SearchParams{Query: tc.query, SessionID: "sess-utf8", SnippetChars: 80})
			if err != nil {
				t.Fatalf("Search(%q) error: %v", tc.query, err)
			}
			if len(hits) != 1 || hits[0].EntryID != tc.wantID {
				t.Fatalf("Search(%q) = %+v, want single hit %s", tc.query, hits, tc.wantID)
			}
			if !utf8.ValidString(hits[0].Snippet) {
				t.Fatalf("Search(%q) produced invalid UTF-8 snippet: %q", tc.query, hits[0].Snippet)
			}
		})
	}

	// Tiny max_bytes (1 byte) on 3-byte Hangul must clamp up to utf8.UTFMax and make progress without looping.
	offset := 0
	var rebuilt strings.Builder
	for step := 0; step < 64; step++ {
		res, err := st.ReadEntry(ctx, "sess-utf8", "1000-001", offset, 1)
		if err != nil {
			t.Fatalf("ReadEntry tiny max_bytes step %d: %v", step, err)
		}
		if res.SliceBytes == 0 && res.HasMore {
			t.Fatalf("pagination stalled at offset %d with max_bytes=1", offset)
		}
		if !utf8.ValidString(res.Entry.Text) {
			t.Fatalf("invalid UTF-8 slice at step %d", step)
		}
		rebuilt.WriteString(res.Entry.Text)
		if !res.HasMore {
			break
		}
		if res.NextOffset <= offset {
			t.Fatalf("NextOffset %d did not advance past %d", res.NextOffset, offset)
		}
		offset = res.NextOffset
	}
	if rebuilt.String() != recs[0].Text {
		t.Fatalf("rebuilt Hangul text = %q, want %q", rebuilt.String(), recs[0].Text)
	}
}

func TestStoreReconcileWindowTombstoneCompactionAndMidRuneClip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// 1. Mid-rune 64 KiB clip: 1 ASCII byte + repeated 3-byte Hangul guarantees
	// MaxEntryBytes (65536 = 1 + 21845*3) lands 1 byte after a rune boundary?
	// Wait: 65536 % 3 == 1, so with 2 ASCII prefix bytes, 65536 - 2 = 65534, and
	// 65534 % 3 == 2 -> byte 65536 falls squarely inside a 3-byte Hangul rune!
	midRuneText := "ab" + strings.Repeat("다", 22000)
	if err := st.ApplyDelta(ctx, "sess-clip", "/ws/c", []wslive.RingRecord{
		makeRingRec("1000-mid", "reply", 1000, midRuneText),
	}, nil); err != nil {
		t.Fatalf("ApplyDelta mid-rune: %v", err)
	}
	rClip, err := st.ReadEntry(ctx, "sess-clip", "1000-mid", 0, MaxEntryBytes)
	if err != nil {
		t.Fatalf("ReadEntry mid-rune: %v", err)
	}
	if !rClip.Entry.StoredTruncated || rClip.Entry.FullBytes != len(midRuneText) {
		t.Fatalf("clip metadata mismatch: truncated=%v full=%d want %d", rClip.Entry.StoredTruncated, rClip.Entry.FullBytes, len(midRuneText))
	}
	if !utf8.ValidString(rClip.Entry.Text) || len(rClip.Entry.Text) != 2+21844*3 {
		t.Fatalf("mid-rune clip len=%d valid=%v, want %d valid=true", len(rClip.Entry.Text), utf8.ValidString(rClip.Entry.Text), 2+21844*3)
	}

	// 2. ReconcileSession window semantics:
	// - Concurrent newer live add (entry_id > max(ring)) is preserved.
	// - At RingHistoryCap, entries older than min(ring) are dropped; high-water advances.
	if err := st.ApplyDelta(ctx, "sess-win", "/ws/w", []wslive.RingRecord{
		makeRingRec("00000001-old", "user", 1000, "old_below_cap_window"),
		makeRingRec("99999999-live", "reply", 999999, "concurrent_newer_live_add"),
	}, nil); err != nil {
		t.Fatalf("ApplyDelta seed window: %v", err)
	}
	capRing := make([]wslive.RingRecord, 0, wslive.RingHistoryCap)
	for i := 0; i < wslive.RingHistoryCap; i++ {
		id := fmt.Sprintf("10%06d-cap", i)
		capRing = append(capRing, makeRingRec(id, "user", int64(2000+i), fmt.Sprintf("window_entry_%d", i)))
	}
	if err := st.ReconcileSession(ctx, "sess-win", "/ws/w", capRing); err != nil {
		t.Fatalf("ReconcileSession cap: %v", err)
	}
	oldHits, err := st.Search(ctx, SearchParams{Query: "old_below_cap_window", SessionID: "sess-win"})
	if err != nil || len(oldHits) != 0 {
		t.Fatalf("entry older than min(ring) at RingHistoryCap survived: %+v (err=%v)", oldHits, err)
	}
	liveHits, err := st.Search(ctx, SearchParams{Query: "concurrent_newer_live_add", SessionID: "sess-win"})
	if err != nil || len(liveHits) != 1 {
		t.Fatalf("concurrent newer live add was dropped by reconcile: %+v (err=%v)", liveHits, err)
	}
	_, hw, err := st.SessionIndexed(ctx, "sess-win")
	if err != nil || hw != "99999999-live" {
		t.Fatalf("high-water mark = %q (err=%v), want 99999999-live", hw, err)
	}

	// 3. Tombstone compaction past evictionPruneThreshold advances tombstone_floor
	// and suppresses late adds at or below tombstone_floor.
	evictIDs := make([]string, 0, evictionPruneThreshold+50)
	for i := 0; i < evictionPruneThreshold+50; i++ {
		evictIDs = append(evictIDs, fmt.Sprintf("ev-%06d", i))
	}
	if err := st.ApplyDelta(ctx, "sess-tomb", "/ws/t", nil, evictIDs); err != nil {
		t.Fatalf("ApplyDelta mass evict: %v", err)
	}
	var tombCount int
	var tombFloor string
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM conv_evictions WHERE session_id = ?`, "sess-tomb").Scan(&tombCount); err != nil {
		t.Fatalf("count conv_evictions: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT tombstone_floor FROM conv_sessions WHERE session_id = ?`, "sess-tomb").Scan(&tombFloor); err != nil {
		t.Fatalf("select tombstone_floor: %v", err)
	}
	if tombCount >= evictionPruneThreshold || tombFloor == "" {
		t.Fatalf("tombstone compaction did not run: count=%d floor=%q", tombCount, tombFloor)
	}
	// A late add at or below tombstone_floor must be skipped.
	if err := st.ApplyDelta(ctx, "sess-tomb", "/ws/t", []wslive.RingRecord{
		makeRingRec("ev-000001", "user", 1000, "below_floor_ghost_token"),
		makeRingRec("ev-999999", "user", 2000, "above_floor_live_token"),
	}, nil); err != nil {
		t.Fatalf("ApplyDelta late below-floor: %v", err)
	}
	ghostHits, err := st.Search(ctx, SearchParams{Query: "below_floor_ghost_token", SessionID: "sess-tomb"})
	if err != nil || len(ghostHits) != 0 {
		t.Fatalf("late add <= tombstone_floor was not skipped: %+v (err=%v)", ghostHits, err)
	}
	aboveHits, err := st.Search(ctx, SearchParams{Query: "above_floor_live_token", SessionID: "sess-tomb"})
	if err != nil || len(aboveHits) != 1 {
		t.Fatalf("add > tombstone_floor missing: %+v (err=%v)", aboveHits, err)
	}
}

func TestStorePragmasCheckpointNewerSchemaAndPermissions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := Open(dir, Options{CheckpointIdle: 30 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Verify SQLite pragmas.
	checkPragma := func(pragma, want string) {
		t.Helper()
		var got string
		if err := st.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", pragma, err)
		}
		if !strings.EqualFold(got, want) {
			t.Fatalf("PRAGMA %s = %q, want %q", pragma, got, want)
		}
	}
	checkPragma("locking_mode", "exclusive")
	checkPragma("journal_mode", "wal")
	checkPragma("busy_timeout", "100")
	checkPragma("wal_autocheckpoint", "0")

	// Verify 0600 permissions on Unix.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(st.Path())
		if err != nil {
			t.Fatalf("Stat(%s): %v", st.Path(), err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("DB file mode = %o, want 0600", perm)
		}
	}

	// Write data, let idle checkpoint run, then Close (which also checkpoints).
	if err := st.ApplyDelta(ctx, "sess-p", "/ws/p", []wslive.RingRecord{
		makeRingRec("1000-p1", "user", 1000, "pragma checkpoint payload"),
	}, nil); err != nil {
		t.Fatalf("ApplyDelta: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if err := st.Checkpoint(ctx); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Bump schema_version in DB above SchemaVersion and verify Open refuses with ErrNewerSchema.
	rawDB, err := sql.Open("sqlite", wssqlite.FileURI(st.Path()))
	if err != nil {
		t.Fatalf("raw sql.Open: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `UPDATE meta SET value = ? WHERE key = 'schema_version'`, fmt.Sprintf("%d", SchemaVersion+1)); err != nil {
		_ = rawDB.Close()
		t.Fatalf("bump schema_version: %v", err)
	}
	_ = rawDB.Close()

	if _, err := Open(dir, Options{}); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("Open newer schema err = %v, want ErrNewerSchema", err)
	}
}
