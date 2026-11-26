package wswhiteboard

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wssqlite"
)

func createHoldTestBoard(t *testing.T, commands string) (*BoardDB, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "board")
	st := &State{}
	mustApply(t, st, commands)
	b, err := CreateBoardDB(dir, st, BoardDBOptions{})
	if err != nil {
		t.Fatalf("CreateBoardDB: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b, dir
}

func TestBoardDBHoldsPersistIdempotenceAndErrors(t *testing.T) {
	b, dir := createHoldTestBoard(t, `
<wb:topic name="alpha">
  <wb:add class="policy"><wb:title>Pick DB</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
  <wb:add class="check"><wb:title>Verify backup</wb:title></wb:add>
  <wb:add class="technical"><wb:title>Ack note</wb:title></wb:add>
  <wb:add class="policy"><wb:title>Closed ask</wb:title><wb:option>x</wb:option><wb:option>y</wb:option></wb:add>
</wb:topic>
<wb:answer id="4" value="a">4=a</wb:answer>
`)

	revBefore := b.Revision()

	// Unknown IDs return ErrHoldItemNotFound for both true and false.
	if _, err := b.SetHold(999, true); !errors.Is(err, ErrHoldItemNotFound) {
		t.Fatalf("SetHold(999, true) err = %v, want ErrHoldItemNotFound", err)
	}
	if _, err := b.SetHold(999, false); !errors.Is(err, ErrHoldItemNotFound) {
		t.Fatalf("SetHold(999, false) err = %v, want ErrHoldItemNotFound", err)
	}

	// Non-open item returns ErrHoldItemNotOpen for both true and false.
	if _, err := b.SetHold(4, true); !errors.Is(err, ErrHoldItemNotOpen) {
		t.Fatalf("SetHold(4, true) err = %v, want ErrHoldItemNotOpen", err)
	}
	if _, err := b.SetHold(4, false); !errors.Is(err, ErrHoldItemNotOpen) {
		t.Fatalf("SetHold(4, false) err = %v, want ErrHoldItemNotOpen", err)
	}

	// Clearing an unheld open item is a success no-op.
	got, err := b.SetHold(1, false)
	if err != nil || len(got) != 0 {
		t.Fatalf("SetHold(1, false) = (%v, %v), want empty nil err", got, err)
	}

	// Hold policy (1), check (2), and ack/technical (3).
	for _, want := range []struct {
		id  int
		ids []int
	}{
		{3, []int{3}},
		{1, []int{1, 3}},
		{2, []int{1, 2, 3}},
		{2, []int{1, 2, 3}}, // repeat set is idempotent
	} {
		got, err := b.SetHold(want.id, true)
		if err != nil {
			t.Fatalf("SetHold(%d, true): %v", want.id, err)
		}
		if !slices.Equal(got, want.ids) {
			t.Fatalf("SetHold(%d, true) = %v, want %v", want.id, got, want.ids)
		}
	}

	// Hold writes must not advance board Revision/generation.
	if got := b.Revision(); got != revBefore {
		t.Fatalf("Revision() after holds = %d, want unchanged %d", got, revBefore)
	}

	// Clear item 2.
	got, err = b.SetHold(2, false)
	if err != nil || !slices.Equal(got, []int{1, 3}) {
		t.Fatalf("SetHold(2, false) = (%v, %v), want [1 3]", got, err)
	}
	// Repeat clear is idempotent.
	got, err = b.SetHold(2, false)
	if err != nil || !slices.Equal(got, []int{1, 3}) {
		t.Fatalf("repeat SetHold(2, false) = (%v, %v), want [1 3]", got, err)
	}

	// Persist across close and reopen; schema_version stays 1.
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := OpenBoardDB(dir, nil, BoardDBOptions{})
	if err != nil {
		t.Fatalf("OpenBoardDB: %v", err)
	}
	defer reopened.Close()

	if !slices.Equal(reopened.HeldIDs(), []int{1, 3}) {
		t.Fatalf("reopened HeldIDs() = %v, want [1 3]", reopened.HeldIDs())
	}
	st, rev, held := reopened.SnapshotWithHolds()
	if rev != revBefore || !slices.Equal(held, []int{1, 3}) {
		t.Fatalf("SnapshotWithHolds = rev %d held %v, want rev %d held [1 3]", rev, held, revBefore)
	}
	if st.OpenAskCount() != 2 || st.OpenAskCountExcluding(held) != 1 || st.OpenAskIDs() != "1 2" {
		t.Fatalf("counts: OpenAskCount=%d Excluding=%d OpenAskIDs=%q", st.OpenAskCount(), st.OpenAskCountExcluding(held), st.OpenAskIDs())
	}

	// Verify schema_version stays 1 and an older reader ignoring item_holds reads cleanly.
	var schemaVer int
	if err := reopened.db.QueryRow(`SELECT schema_version FROM board_meta WHERE id=1`).Scan(&schemaVer); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if schemaVer != 1 {
		t.Fatalf("schema_version = %d, want 1", schemaVer)
	}
}

func TestBoardDBHoldsAutoClearAndNoOpRetention(t *testing.T) {
	b, dir := createHoldTestBoard(t, `
<wb:topic name="t1">
  <wb:add class="policy"><wb:title>Answer me</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
  <wb:add class="policy"><wb:title>Patch me</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
  <wb:add class="policy"><wb:title>No-op patch me</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
  <wb:add class="check"><wb:title>Keep me</wb:title></wb:add>
</wb:topic>
<wb:topic name="t_close">
  <wb:add class="policy"><wb:title>Withdraw me</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
</wb:topic>
<wb:topic name="t2">
  <wb:body>Second topic</wb:body>
</wb:topic>
`)

	for _, id := range []int{1, 2, 3, 4, 5} {
		if _, err := b.SetHold(id, true); err != nil {
			t.Fatalf("SetHold(%d): %v", id, err)
		}
	}

	apply := func(cmds string) {
		t.Helper()
		if err := b.Update(func(st *State) error {
			mustApply(t, st, cmds)
			return nil
		}); err != nil {
			t.Fatalf("Update(%s): %v", cmds, err)
		}
	}

	// 1. Answer clears item 1.
	apply(`<wb:answer id="1" value="a">1=a</wb:answer>`)
	if !slices.Equal(b.HeldIDs(), []int{2, 3, 4, 5}) {
		t.Fatalf("after answer: HeldIDs = %v, want [2 3 4 5]", b.HeldIDs())
	}

	// 2. Topic close (applyClose) withdraws open item 5 and clears its hold.
	apply(`<wb:topic name="t_close"><wb:close result="Superseded."/></wb:topic>`)
	if !slices.Equal(b.HeldIDs(), []int{2, 3, 4}) {
		t.Fatalf("after close/withdraw: HeldIDs = %v, want [2 3 4]", b.HeldIDs())
	}

	// 3. Content-changing patch clears item 2.
	apply(`<wb:patch id="2"><wb:title>Patch me updated</wb:title></wb:patch>`)
	if !slices.Equal(b.HeldIDs(), []int{3, 4}) {
		t.Fatalf("after content patch: HeldIDs = %v, want [3 4]", b.HeldIDs())
	}

	// 4. No-op patch keeps item 3 held.
	apply(`<wb:patch id="3"><wb:title>No-op patch me</wb:title></wb:patch>`)
	if !slices.Equal(b.HeldIDs(), []int{3, 4}) {
		t.Fatalf("after no-op patch: HeldIDs = %v, want [3 4]", b.HeldIDs())
	}

	// 5. Unrelated topic mutation keeps items 3 and 4 held.
	apply(`<wb:topic name="t2"><wb:log>progress note</wb:log></wb:topic>`)
	if !slices.Equal(b.HeldIDs(), []int{3, 4}) {
		t.Fatalf("after unrelated topic log: HeldIDs = %v, want [3 4]", b.HeldIDs())
	}

	// Confirm SQLite item_holds rows match in-memory state on reopen.
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := OpenBoardDB(dir, nil, BoardDBOptions{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if !slices.Equal(reopened.HeldIDs(), []int{3, 4}) {
		t.Fatalf("reopened HeldIDs = %v, want [3 4]", reopened.HeldIDs())
	}
}

func TestBoardDBHoldsClearedByLoadArchive(t *testing.T) {
	b, _ := createHoldTestBoard(t, `
<wb:topic name="t1">
  <wb:add class="policy"><wb:title>Q1</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
</wb:topic>
`)
	if _, err := b.SetHold(1, true); err != nil {
		t.Fatalf("SetHold: %v", err)
	}
	archiveBytes, err := b.ExportArchive()
	if err != nil {
		t.Fatalf("ExportArchive: %v", err)
	}
	zipPath := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(zipPath, archiveBytes, 0o600); err != nil {
		t.Fatalf("WriteFile archive: %v", err)
	}
	if _, _, err := b.LoadArchive(zipPath, time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("LoadArchive: %v", err)
	}
	if got := b.HeldIDs(); len(got) != 0 {
		t.Fatalf("HeldIDs after LoadArchive = %v, want empty", got)
	}
	var count int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM item_holds`).Scan(&count); err != nil {
		t.Fatalf("count item_holds: %v", err)
	}
	if count != 0 {
		t.Fatalf("SQLite item_holds count after LoadArchive = %d, want 0", count)
	}
}

func TestBoardDBHoldsPrunedAfterSimulatedOlderBinaryWrite(t *testing.T) {
	b, dir := createHoldTestBoard(t, `
<wb:topic name="t1">
  <wb:add class="policy"><wb:title>Q1</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
  <wb:add class="policy"><wb:title>Q2</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
  <wb:add class="check"><wb:title>C3</wb:title></wb:add>
</wb:topic>
`)
	for _, id := range []int{1, 2, 3} {
		if _, err := b.SetHold(id, true); err != nil {
			t.Fatalf("SetHold(%d): %v", id, err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Simulate an older binary reading board_meta (schema_version=1, ignoring item_holds)
	// and mutating items without clearing item_holds: mismatch item 1's Changed, mismatch
	// item 2's LoadedAt, and insert an orphan ID 99, while leaving item 3 valid.
	rawDB, err := sql.Open("sqlite", wssqlite.FileURI(filepath.Join(dir, boardDBName)))
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	var schemaVer int
	if err := rawDB.QueryRow(`SELECT schema_version FROM board_meta WHERE id=1`).Scan(&schemaVer); err != nil || schemaVer != 1 {
		rawDB.Close()
		t.Fatalf("older reader schema_version = (%d, %v), want (1, nil)", schemaVer, err)
	}
	if _, err := rawDB.Exec(`UPDATE item_holds SET item_changed = item_changed + 5 WHERE item_id = 1`); err != nil {
		rawDB.Close()
		t.Fatalf("mutate item 1 hold: %v", err)
	}
	if _, err := rawDB.Exec(`UPDATE item_holds SET loaded_at = '2099-01-01T00:00:00.000Z' WHERE item_id = 2`); err != nil {
		rawDB.Close()
		t.Fatalf("mutate item 2 hold: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO item_holds(item_id,loaded_at,item_changed) VALUES(99,'',0)`); err != nil {
		rawDB.Close()
		t.Fatalf("insert orphan hold: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw sqlite: %v", err)
	}

	// Reopening with the new binary prunes items 1, 2, and 99 from item_holds while keeping item 3.
	reopened, err := OpenBoardDB(dir, nil, BoardDBOptions{})
	if err != nil {
		t.Fatalf("OpenBoardDB after older binary write: %v", err)
	}
	defer reopened.Close()
	if !slices.Equal(reopened.HeldIDs(), []int{3}) {
		t.Fatalf("pruned HeldIDs = %v, want [3]", reopened.HeldIDs())
	}
	var count int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM item_holds`).Scan(&count); err != nil {
		t.Fatalf("count item_holds: %v", err)
	}
	if count != 1 {
		t.Fatalf("SQLite item_holds count after prune = %d, want 1", count)
	}
}

func TestBoardRenderHeldMarkupCountPolicyAndTopicRevision(t *testing.T) {
	st := &State{}
	mustApply(t, st, `
<wb:topic name="t1">
  <wb:add class="policy"><wb:title>P1</wb:title><wb:option>a</wb:option><wb:option>b</wb:option></wb:add>
  <wb:add class="check"><wb:title>C2</wb:title></wb:add>
  <wb:add class="technical"><wb:title>A3</wb:title></wb:add>
</wb:topic>
<wb:topic name="t2">
  <wb:add class="policy"><wb:title>P4</wb:title><wb:option>x</wb:option><wb:option>y</wb:option></wb:add>
</wb:topic>
`)

	renderWithRevs := func(v View) (Regions, map[string]string) {
		revs := map[string]string{}
		reg, _, err := RenderRegionsWithTopics(v, func(id, revision string, render func() (TopicUnit, error)) (TopicUnit, error) {
			revs[id] = revision
			return render()
		})
		if err != nil {
			t.Fatalf("RenderRegionsWithTopics: %v", err)
		}
		return reg, revs
	}

	viewNone := BuildView(st, ViewInput{Layout: LayoutFull, ReplyID: "render-hold", Location: time.UTC})
	regNone, revsNone := renderWithRevs(viewNone)
	if regNone.CountPolicy != 3 {
		t.Fatalf("unheld CountPolicy = %d, want 3", regNone.CountPolicy)
	}
	if strings.Contains(string(regNone.Board), "wb-held") {
		t.Fatalf("unheld board unexpectedly contains wb-held")
	}

	// Hold all asks in t1 (policy 1, check 2, ack 3); t2 remains unheld.
	viewHeld := BuildView(st, ViewInput{Layout: LayoutFull, ReplyID: "render-hold", Location: time.UTC, Held: []int{1, 2, 3}})
	var t1 TopicView
	for _, tv := range viewHeld.Topics {
		if tv.Name == "t1" {
			t1 = tv
		}
	}
	// Preserve invariant: TopicView.AskCount() and waiting() still include held asks (topic does not become Quiet).
	if t1.AskCount() != 2 || !t1.waiting() || t1.Quiet {
		t.Fatalf("t1 AskCount=%d waiting=%v Quiet=%v, want AskCount=2 waiting=true Quiet=false", t1.AskCount(), t1.waiting(), t1.Quiet)
	}
	if t1.UnheldAskCount() != 0 {
		t.Fatalf("t1 UnheldAskCount=%d, want 0", t1.UnheldAskCount())
	}

	regHeld, revsHeld := renderWithRevs(viewHeld)
	// Regions.CountPolicy excludes held policy/check asks (only P4 in t2 remains).
	if regHeld.CountPolicy != 1 {
		t.Fatalf("held CountPolicy = %d, want 1", regHeld.CountPolicy)
	}

	html := string(regHeld.Board)
	for _, want := range []string{
		`<div class="card policy wb-held" id="item-1" data-id="01">`,
		`<div class="ckrow wb-held" id="item-2" data-id="02">`,
		`<div class="arow wb-held" id="item-3" data-id="03">`,
		`data-wb-hold aria-pressed="true"`,
		`<div class="card policy" id="item-4" data-id="04">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered board missing %q", want)
		}
	}

	// topicRevision changes only for t1; untouched t2 keeps identical revision.
	if revsNone["t1"] == revsHeld["t1"] {
		t.Fatalf("t1 topicRevision did not change when items were held")
	}
	if revsNone["t2"] != revsHeld["t2"] {
		t.Fatalf("untouched t2 topicRevision changed from %q to %q", revsNone["t2"], revsHeld["t2"])
	}
}
