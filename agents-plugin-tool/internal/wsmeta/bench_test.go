package wsmeta

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Replacement paths for the legacy baseline in
// wsagent/metadata_baseline_bench_test.go, on the same machine and the same
// record (testdata/baseline-record.json); only the advance is timed:
// go test ./wsmeta -run '^$' -bench . -benchtime 2000x

func benchStore(b *testing.B) *Store {
	b.Helper()
	record, err := os.ReadFile(filepath.Join("testdata", "baseline-record.json"))
	if err != nil {
		b.Fatal(err)
	}
	dir := filepath.Join(b.TempDir(), "dashboard")
	if err := mkdirWrite(filepath.Join(dir, "sessions", "baselinelead", "agent.json"), string(record)); err != nil {
		b.Fatal(err)
	}
	s, err := Open(dir, Options{CheckpointIdle: -1})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	if _, err := s.Commit(context.Background(), testCommit()); err != nil {
		b.Fatal(err)
	}
	if _, ok, err := s.Record(context.Background(), "baselinelead"); err != nil || !ok {
		b.Fatalf("baseline record not imported: %v", err)
	}
	return s
}

func BenchmarkHolderCursor(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.SetHolderCursor(ctx, "baselinelead", "holder-1", uint64(i+2)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRecordPatch(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		raw, _ := json.Marshal(i)
		if err := s.PatchRecord(ctx, "baselinelead", map[string]json.RawMessage{"sends": raw}); err != nil {
			b.Fatal(err)
		}
	}
}
