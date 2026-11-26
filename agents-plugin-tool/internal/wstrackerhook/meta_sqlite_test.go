package wstrackerhook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsmeta"
	"github.com/kang-sw/devenv/internal/wstracker"
)

func openMetaForHookTest(t *testing.T, dir string) (*wsmeta.Store, *wsmeta.Snapshot) {
	t.Helper()
	store, err := wsmeta.Open(dir, wsmeta.Options{})
	if err != nil {
		t.Fatalf("wsmeta.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	passAll := func(_, _ string, _ []byte) error { return nil }
	validators := map[string]wsmeta.LegacyValidator{}
	for _, name := range wsmeta.RequiredValidators {
		validators[name] = passAll
	}
	if _, err := store.Cutover(context.Background(), wsmeta.CutoverOptions{Validate: validators}); err != nil {
		t.Fatalf("wsmeta.Cutover: %v", err)
	}
	snap, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("wsmeta.Load: %v", err)
	}
	return store, snap
}

func TestRunnerSQLiteWitnessConfigAndZeroLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	store, snap := openMetaForHookTest(t, dir)

	// Legacy tracker-hooks.json must be ignored when Meta != nil (2125).
	legacyCfg := `{"hooks":[{"command":["echo"],"scope":"all"}]}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(legacyCfg), 0o600); err != nil {
		t.Fatalf("write legacy hooks file: %v", err)
	}

	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	src := newFakeSource("alpha")
	src.add("alpha", 1, now.Add(-10*time.Minute), "comment.added", "bo", "ada")

	var (
		gotMu sync.Mutex
		got   []Event
	)
	execFn := func(_ context.Context, _ []string, _ string, stdin []byte) error {
		var ev Event
		if err := json.Unmarshal(stdin, &ev); err != nil {
			t.Errorf("unmarshal hook event: %v", err)
			return err
		}
		gotMu.Lock()
		got = append(got, ev)
		gotMu.Unlock()
		return nil
	}

	r := newRunner(Options{
		Dir:          dir,
		Source:       src,
		Now:          func() time.Time { return now },
		Location:     time.UTC,
		Exec:         execFn,
		Meta:         store,
		MetaSnapshot: snap,
	})
	defer r.Close()

	// Legacy file alone must keep the SQLite-backed runner inactive.
	r.reload()
	r.scanAll()
	r.pending.Wait()
	if src.reads != 0 {
		t.Fatalf("reads with only legacy tracker-hooks.json = %d, want 0", src.reads)
	}

	// Installing tracker-hooks-v2.json activates the runner and baselines head.
	if err := os.WriteFile(filepath.Join(dir, WitnessFileName), []byte(legacyCfg), 0o600); err != nil {
		t.Fatalf("write witness hooks file: %v", err)
	}
	if !r.reload() {
		t.Fatal("reload did not detect tracker-hooks-v2.json")
	}
	r.scanAll()
	r.pending.Wait()

	gotMu.Lock()
	if len(got) != 0 {
		t.Fatalf("initial baseline delivered %d events, want 0", len(got))
	}
	gotMu.Unlock()

	// No legacy state directory/files may be created after cutover.
	if _, err := os.Stat(filepath.Join(dir, stateDirName)); !os.IsNotExist(err) {
		t.Fatalf("legacy tracker-hooks state dir should not exist, stat err = %v", err)
	}

	// Add a new activity event and a due_soon item; next scan delivers both and
	// persists incremental state in SQLite.
	dueDate := now.Add(6 * time.Hour).Format("2006-01-02")
	src.mu.Lock()
	it := src.views["alpha"].Items["maya.pc#1"]
	it.Due = &dueDate
	it.Assignments = []string{"asgn-1"}
	src.views["alpha"].Items["maya.pc#1"] = it
	src.views["alpha"].Assignments["asgn-1"] = wstracker.Assignment{ID: "asgn-1", To: "ada", State: "live", Status: "open"}
	src.mu.Unlock()
	src.add("alpha", 2, now, "comment.added", "bo", "ada")

	r.scanAll()
	r.pending.Wait()

	gotMu.Lock()
	if len(got) != 2 {
		t.Fatalf("second scan delivered %d events, want 2 (activity + due_soon): %+v", len(got), got)
	}
	gotMu.Unlock()

	// Verify SQLite snapshot persisted cursor, both processed events, and due mark.
	reloadedSnap, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load after scan: %v", err)
	}
	key := wsmeta.TrackerHookKey{StoreID: "alpha", Nonce: "n-alpha"}
	st, ok := reloadedSnap.TrackerHooks[key]
	if !ok {
		t.Fatalf("missing SQLite tracker hook state for %+v", key)
	}
	if len(st.Processed) != 2 {
		t.Fatalf("persisted processed count = %d, want 2", len(st.Processed))
	}
	if len(st.Due) != 1 {
		t.Fatalf("persisted due count = %d, want 1", len(st.Due))
	}

	// Restart into a fresh Runner seeded from reloadedSnap with a new ledger
	// epoch (forces catch-up read): neither event nor due_soon may redeliver.
	src.restart()
	r2 := newRunner(Options{
		Dir:          dir,
		Source:       src,
		Now:          func() time.Time { return now.Add(time.Minute) },
		Location:     time.UTC,
		Exec:         execFn,
		Meta:         store,
		MetaSnapshot: reloadedSnap,
	})
	defer r2.Close()
	r2.reload()
	r2.scanAll()
	r2.pending.Wait()

	gotMu.Lock()
	if len(got) != 2 {
		t.Fatalf("catch-up after restart redelivered events: total %d, want 2", len(got))
	}
	gotMu.Unlock()

	// Rotating the store's registration nonce resets SQLite state and cascades
	// the old nonce's rows.
	src.mu.Lock()
	src.stores[0].Nonce = "n-alpha-2"
	src.mu.Unlock()
	r2.scanAll()
	r2.pending.Wait()

	afterReset, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load after nonce reset: %v", err)
	}
	if _, stillOld := afterReset.TrackerHooks[key]; stillOld {
		t.Fatalf("old nonce %+v still present after reset", key)
	}
	newKey := wsmeta.TrackerHookKey{StoreID: "alpha", Nonce: "n-alpha-2"}
	if _, hasNew := afterReset.TrackerHooks[newKey]; !hasNew {
		t.Fatalf("new nonce %+v missing after reset", newKey)
	}
	if _, err := os.Stat(filepath.Join(dir, stateDirName)); !os.IsNotExist(err) {
		t.Fatalf("legacy tracker-hooks dir appeared unexpectedly: %v", err)
	}
}

func TestDiffTrackerHookStateIncrementalDelta(t *testing.T) {
	t0 := time.Date(2026, 11, 27, 8, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	base := storeState{
		V:     stateVersion,
		Nonce: "n1",
		Processed: map[string]mark{
			"ev-keep": {At: t0, TS: t0},
			"ev-drop": {At: t0, TS: t0},
		},
		Due: map[string]time.Time{
			"item-keep:2026-11-28": t0,
			"item-drop:2026-11-25": t0,
		},
	}

	// Unchanged state yields an empty delta (no SQLite transaction needed).
	unchanged, err := diffTrackerHookState(true, false, base, base.clone())
	if err != nil {
		t.Fatalf("diff unchanged: %v", err)
	}
	if !unchanged.Empty() {
		t.Fatalf("unchanged delta not empty: %+v", unchanged)
	}

	// Incremental mutation includes only added/changed upserts and removed deletes.
	next := base.clone()
	delete(next.Processed, "ev-drop")
	next.Processed["ev-new"] = mark{At: t1, TS: t1}
	delete(next.Due, "item-drop:2026-11-25")
	next.Due["item-new:2026-11-29"] = t1

	delta, err := diffTrackerHookState(true, false, base, next)
	if err != nil {
		t.Fatalf("diff incremental: %v", err)
	}
	if delta.CursorChanged || delta.ResetNonce {
		t.Fatalf("unexpected cursor/reset flags: %+v", delta)
	}
	if len(delta.UpsertProcessed) != 1 || delta.UpsertProcessed["ev-new"] == nil {
		t.Fatalf("UpsertProcessed = %v, want only ev-new", delta.UpsertProcessed)
	}
	if len(delta.DeleteProcessed) != 1 || delta.DeleteProcessed[0] != "ev-drop" {
		t.Fatalf("DeleteProcessed = %v, want [ev-drop]", delta.DeleteProcessed)
	}
	if len(delta.UpsertDue) != 1 || delta.UpsertDue["item-new:2026-11-29"] == nil {
		t.Fatalf("UpsertDue = %v, want only item-new:2026-11-29", delta.UpsertDue)
	}
	if len(delta.DeleteDue) != 1 || delta.DeleteDue[0] != "item-drop:2026-11-25" {
		t.Fatalf("DeleteDue = %v, want [item-drop:2026-11-25]", delta.DeleteDue)
	}
}
