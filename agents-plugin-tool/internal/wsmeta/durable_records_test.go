package wsmeta

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestDurableAgentRecordsSurviveLegacyImportAndReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := openTest(t, dir, Options{})
	rec := DurableAgentRecord{ReplyID: "worker", Backend: "fixture", JSON: json.RawMessage(`{"turn_open":true,"generation":1}`)}
	if err := store.PutDurableAgentRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Record(ctx, rec.ReplyID); err != nil || found {
		t.Fatalf("legacy records include a private record: found %v, error %v", found, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openTest(t, dir, Options{})
	items, err := store.DurableAgentRecords(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("private records = %v, error %v", items, err)
	}
	if got := items[0]; got.ReplyID != rec.ReplyID || got.Backend != rec.Backend || string(got.JSON) != string(rec.JSON) {
		t.Errorf("record = %+v", got)
	}
	if err := store.DeleteDurableAgentRecord(ctx, rec.ReplyID); err != nil {
		t.Fatal(err)
	}
	if items, err := store.DurableAgentRecords(ctx); err != nil || len(items) != 0 {
		t.Fatalf("after delete: %v, error %v", items, err)
	}
}

func TestDurableAgentRecordKeepsPriorStateOnCommitFailure(t *testing.T) {
	ctx := context.Background()
	failure := errors.New("commit failure")
	var fail bool
	store := openTest(t, t.TempDir(), Options{Fault: func(stage string) error {
		if fail && stage == "before-commit" {
			return failure
		}
		return nil
	}})
	rec := DurableAgentRecord{ReplyID: "worker", Backend: "fixture", JSON: json.RawMessage(`{"turn_open":true}`)}
	if err := store.PutDurableAgentRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	fail = true
	rec.JSON = json.RawMessage(`{"turn_open":false}`)
	if err := store.PutDurableAgentRecord(ctx, rec); !errors.Is(err, failure) {
		t.Fatalf("settlement commit error = %v", err)
	}
	items, err := store.DurableAgentRecords(ctx)
	if err != nil || len(items) != 1 || string(items[0].JSON) != `{"turn_open":true}` {
		t.Fatalf("after failed settlement: %v, error %v", items, err)
	}
}
