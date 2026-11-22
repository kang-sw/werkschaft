package wsmeta

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

// Replacement paths for the legacy baseline in
// wsagent/metadata_baseline_bench_test.go, on the same machine:
// go test ./wsmeta -run '^$' -bench . -benchtime 2000x

func benchStore(b *testing.B) *Store {
	b.Helper()
	dir := filepath.Join(b.TempDir(), "dashboard")
	put := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := mkdirWrite(p, content); err != nil {
			b.Fatal(err)
		}
	}
	put("sessions/baselinelead/agent.json", `{"schema_version":1,"reply_id":"baselinelead","root":"/repo","ws_session_key":"k","holder_id":"holder-1","holder_seq":1}`)
	s, err := Open(dir, Options{CheckpointIdle: -1})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	if _, err := s.Commit(context.Background(), testCommit()); err != nil {
		b.Fatal(err)
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
