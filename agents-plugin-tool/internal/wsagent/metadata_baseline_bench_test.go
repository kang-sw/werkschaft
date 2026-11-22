package wsagent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// Legacy baseline for 261121-refactor-ws-daemon-metadata-sqlite: the
// whole-record replacement a held process's sequence-only advance costs
// today (a ring-touching final line or a dirty heartbeat), and an explicit
// permission change. wsmeta's BenchmarkHolderCursor and
// BenchmarkRecordPatch measure the replacement paths on the same machine.
// Run: go test ./wsagent -run '^$' -bench MetadataBaseline -benchtime 2000x

func baselineRecord(i int) Record {
	reports := make([]string, 8)
	for j := range reports {
		reports[j] = fmt.Sprintf("report %d with a realistic amount of text in it", j)
	}
	return Record{
		ReplyID: "baselinelead", Profile: "lead", Backend: "claude", Root: "/repo/root",
		WSKey: "lead-key-secret", CreatedAt: "2026-11-22T00:00:00Z", Lead: true,
		HolderID: "holder-1", HostPID: 4242, HolderSeq: uint64(i), Reports: reports,
		AcceptedEvents: []string{"e1", "e2", "e3", "e4"}, MCPEnv: map[string]string{"WS_A": "1", "WS_B": "2"},
	}
}

func fileOps(before map[string]int64) float64 {
	return float64(wsstate.FileIOCounters()["file_io_total"] - before["file_io_total"])
}

func BenchmarkMetadataBaselineHolderSeqRecordRewrite(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "sessions", "baselinelead")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	if err := writeRecord(dir, baselineRecord(0)); err != nil {
		b.Fatal(err)
	}
	before := wsstate.FileIOCounters()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := writeRecord(dir, baselineRecord(i+1)); err != nil {
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
