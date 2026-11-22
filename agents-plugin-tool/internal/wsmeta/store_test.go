package wsmeta

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestOpenIsOwnerOnlyExclusiveAndShmFree(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "dashboard")
	s, err := Open(dir, testOpts())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	var mode, locking string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA locking_mode`).Scan(&locking); err != nil || locking != "exclusive" {
		t.Fatalf("locking_mode = %q, %v", locking, err)
	}
	if _, err := os.Stat(s.Path() + "-shm"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("-shm exists: %v", err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{s.Path(), s.Path() + "-wal"} {
			info, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("%s mode %v is not owner-only", p, info.Mode().Perm())
			}
		}
	}
	// A second daemon cannot open the database while this one holds it.
	if s2, err := Open(dir, testOpts()); err == nil {
		_ = s2.Close()
		t.Fatal("second open succeeded while the first held the database")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Close checkpoints the WAL away, then a successor opens.
	if info, err := os.Stat(s.Path() + "-wal"); err == nil && info.Size() != 0 {
		t.Fatalf("WAL holds %d bytes after close", info.Size())
	}
	s2 := openTest(t, dir, testOpts())
	if done, err := s2.ImportComplete(ctx); err != nil || !done {
		t.Fatalf("successor sees complete=%v err=%v", done, err)
	}
	if err := s.Checkpoint(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("checkpoint on a closed store = %v", err)
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dashboard")
	s, err := Open(dir, testOpts())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", filepath.Join(dir, DBName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE meta SET value = '2' WHERE key = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, testOpts()); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("open newer schema = %v", err)
	}
}

func importedStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	if _, err := s.Commit(context.Background(), CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func recordMembers(t *testing.T, s *Store, id string) (map[string]any, AgentRecord) {
	t.Helper()
	rec, ok, err := s.Record(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("record %s: ok=%v err=%v", id, ok, err)
	}
	return decode(t, rec.JSON), rec
}

func TestLiveWritesWaitForTheImport(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "dashboard"), testOpts())
	if err := s.SetHolderCursor(ctx, "a", "h", 1); !errors.Is(err, ErrNotImported) {
		t.Fatalf("cursor before import = %v", err)
	}
	if err := s.PutRecord(ctx, "a", "", []byte(`{}`)); !errors.Is(err, ErrNotImported) {
		t.Fatalf("put before import = %v", err)
	}
}

func TestHolderCursorIsKeyedByConversationAndHolder(t *testing.T) {
	ctx := context.Background()
	s, dir := importedStore(t)
	before := treeState(t, dir)

	if err := s.SetHolderCursor(ctx, "hostedlead", "h1", 9); err != nil {
		t.Fatal(err)
	}
	if m, _ := recordMembers(t, s, "hostedlead"); m["holder_seq"].(float64) != 9 {
		t.Fatalf("holder_seq = %v, want 9", m["holder_seq"])
	}
	// The cursor only advances.
	if err := s.SetHolderCursor(ctx, "hostedlead", "h1", 8); err != nil {
		t.Fatal(err)
	}
	if m, rec := recordMembers(t, s, "hostedlead"); m["holder_seq"].(float64) != 9 || rec.Deltas != 0 {
		t.Fatalf("holder_seq = %v deltas %d after a lower cursor", m["holder_seq"], rec.Deltas)
	}
	// A holder change: the old holder's cursor never applies to the new one.
	if err := s.PatchRecord(ctx, "hostedlead", map[string]json.RawMessage{"holder_id": json.RawMessage(`"h3"`), "holder_seq": json.RawMessage(`null`)}); err != nil {
		t.Fatal(err)
	}
	if m, _ := recordMembers(t, s, "hostedlead"); m["holder_id"] != "h3" || m["holder_seq"] != nil {
		t.Fatalf("after holder change: %v", m)
	}
	if err := s.SetHolderCursor(ctx, "hostedlead", "h3", 2); err != nil {
		t.Fatal(err)
	}
	m, rec := recordMembers(t, s, "hostedlead")
	if m["holder_seq"].(float64) != 2 || !reflect.DeepEqual(rec.HolderCursors, map[string]uint64{"h3": 2}) {
		t.Fatalf("new holder cursor: %v %v", m["holder_seq"], rec.HolderCursors)
	}
	// A late write from the superseded holder is refused and leaves the
	// current holder's cursor in place.
	if err := s.SetHolderCursor(ctx, "hostedlead", "h1", 50); !errors.Is(err, ErrStaleHolder) {
		t.Fatalf("late old-holder cursor = %v", err)
	}
	m, rec = recordMembers(t, s, "hostedlead")
	if m["holder_seq"].(float64) != 2 || !reflect.DeepEqual(rec.HolderCursors, map[string]uint64{"h3": 2}) {
		t.Fatalf("after a late old-holder write: %v %v", m["holder_seq"], rec.HolderCursors)
	}
	// Another conversation's cursor is untouched.
	if _, w := recordMembers(t, s, "worker"); !reflect.DeepEqual(w.HolderCursors, map[string]uint64{"h2": 3}) {
		t.Fatalf("worker cursors = %v", w.HolderCursors)
	}
	if err := s.SetHolderCursor(ctx, "nosuch", "h", 1); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("cursor on a missing record = %v", err)
	}
	// Live writes never publish to legacy paths.
	if after := treeState(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("a live write touched a legacy file")
	}
}

func TestRecordDeltasStayBoundedAndSurviveRestart(t *testing.T) {
	ctx := context.Background()
	s, dir := importedStore(t)
	for i := 1; i <= RecordDeltaBound; i++ {
		raw, _ := json.Marshal(i)
		if err := s.PatchRecord(ctx, "worker", map[string]json.RawMessage{"sends": raw}); err != nil {
			t.Fatal(err)
		}
	}
	m, rec := recordMembers(t, s, "worker")
	if rec.Deltas != RecordDeltaBound || m["sends"].(float64) != RecordDeltaBound {
		t.Fatalf("deltas %d sends %v", rec.Deltas, m["sends"])
	}
	// The next patch folds every delta into the snapshot.
	if err := s.PatchRecord(ctx, "worker", map[string]json.RawMessage{"turn_open": json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	m, rec = recordMembers(t, s, "worker")
	if rec.Deltas != 0 || m["sends"].(float64) != RecordDeltaBound || m["turn_open"] != true || m["parent"] == nil {
		t.Fatalf("after compaction: deltas %d record %v", rec.Deltas, m)
	}
	if err := s.PatchRecord(ctx, "worker", map[string]json.RawMessage{"mail": json.RawMessage(`["hi"]`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.PatchRecord(ctx, "worker", nil); err != nil {
		t.Fatal(err)
	}
	want, _ := recordMembers(t, s, "worker")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := openTest(t, dir, testOpts())
	snap, err := s2.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(t, snap.Records["worker"].JSON); !reflect.DeepEqual(got, want) || snap.Records["worker"].Deltas != 1 {
		t.Fatalf("restored record = %v (deltas %d), want %v", got, snap.Records["worker"].Deltas, want)
	}
	if err := s2.PatchRecord(ctx, "nosuch", map[string]json.RawMessage{"a": json.RawMessage(`1`)}); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("patch on a missing record = %v", err)
	}
	if err := s2.DeleteRecord(ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s2.Record(ctx, "worker"); err != nil || ok {
		t.Fatalf("deleted record: ok=%v err=%v", ok, err)
	}
	if err := s2.PutRecord(ctx, "fresh", "gen", []byte(`{"reply_id":"fresh","holder_id":"hx","holder_seq":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := s2.SetHolderCursor(ctx, "fresh", "hx", 5); err != nil {
		t.Fatal(err)
	}
	if m, _ := recordMembers(t, s2, "fresh"); m["holder_seq"].(float64) != 5 {
		t.Fatalf("fresh holder_seq = %v", m["holder_seq"])
	}
}
