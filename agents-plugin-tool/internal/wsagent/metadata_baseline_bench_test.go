package wsagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// Legacy baseline for 261121-refactor-ws-daemon-metadata-sqlite: the
// whole-record replacement a held process's sequence-only advance costs
// today (a ring-touching final line or a dirty heartbeat), and an explicit
// permission change. wsmeta's BenchmarkHolderCursor measures the
// replacement path on the same record (wsmeta/testdata/baseline-record.json)
// and the same sequence advance; only that advance is timed on both sides.
// Run: go test ./wsagent -run '^$' -bench MetadataBaseline -benchtime 2000x

const baselineRecordFile = "../wsmeta/testdata/baseline-record.json"

func baselineRecord(b *testing.B) Record {
	b.Helper()
	raw, err := os.ReadFile(baselineRecordFile)
	if err != nil {
		b.Fatal(err)
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		b.Fatal(err)
	}
	return rec
}

func fileOps(before map[string]int64) float64 {
	return float64(wsstate.FileIOCounters()["file_io_total"] - before["file_io_total"])
}

func BenchmarkMetadataBaselineHolderSeqRecordRewrite(b *testing.B) {
	rec := baselineRecord(b)
	dir := filepath.Join(b.TempDir(), "sessions", rec.ReplyID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	if err := writeRecord(dir, rec); err != nil {
		b.Fatal(err)
	}
	before := wsstate.FileIOCounters()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec.HolderSeq = uint64(i + 2)
		if err := writeRecord(dir, rec); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	// Counted wrapper operations only (CreateTemp, replace); MkdirAll,
	// Chmod, Write and Close are additional uncounted calls (5 API calls by
	// the ticket's source count).
	b.ReportMetric(fileOps(before)/float64(b.N), "counted-ops/op")
}

func BenchmarkMetadataBaselinePermissionChange(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "sessions", "baselinelead")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	levels := []PermissionLevel{PermissionReadOnly, PermissionWorkdirWrite}
	before := wsstate.FileIOCounters()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := writeGenericPermissionSelection(dir, levels[i%2]); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(fileOps(before)/float64(b.N), "counted-ops/op")
}
