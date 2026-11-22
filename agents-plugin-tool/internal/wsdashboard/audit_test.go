package wsdashboard

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
)

// TestAuditLifecycleRecordsLastSeenAtWritePointsOnly pins Decision 6: the
// daemon records a session's registry last-seen time at its first sighting
// in a run and when the index drops it, never on later changes (heartbeats).
func TestAuditLifecycleRecordsLastSeenAtWritePointsOnly(t *testing.T) {
	d := &Daemon{dir: t.TempDir(), log: io.Discard}
	d.openAudit(io.Discard)
	if d.audit == nil {
		t.Fatal("audit store did not open")
	}
	t.Cleanup(d.closeAudit)
	ctx := context.Background()
	ev, _ := wsaudit.NewEnvelope("sess", "claude", time.Now(), wsaudit.KindToolCall)
	if err := d.AuditSink().Append(ctx, ev); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	stamp := func(t time.Time) string { return t.Format(time.RFC3339) }
	reg := Registration{ReplyID: "sess", RegisteredAt: stamp(base), HeartbeatAt: stamp(base.Add(time.Minute))}
	d.auditSessionSeen(reg)
	reg.HeartbeatAt = stamp(base.Add(2 * time.Minute)) // a heartbeat: not recorded
	d.auditSessionSeen(reg)
	if err := d.AuditStore().Flush(ctx); err != nil {
		t.Fatal(err)
	}
	index := readAuditIndex(t, d.dir)
	if !strings.Contains(index, base.Add(time.Minute).Format(time.RFC3339)) || strings.Contains(index, base.Add(2*time.Minute).Format(time.RFC3339)) {
		t.Fatalf("index after sightings:\n%s", index)
	}
	reg.EndedAt = stamp(base.Add(3 * time.Minute))
	d.auditSessionRemoved("sess", reg)
	if err := d.AuditStore().Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if index := readAuditIndex(t, d.dir); !strings.Contains(index, base.Add(3*time.Minute).Format(time.RFC3339)) {
		t.Fatalf("final last-seen not recorded:\n%s", index)
	}
}

func readAuditIndex(t *testing.T, dir string) string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, auditDirName, "index-*.jsonl"))
	var out strings.Builder
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(raw)
	}
	return out.String()
}

// TestAuditStartupSweepConsultsTheRegistry pins the wired retention path of
// Decision 6 across a daemon restart: the first sweep waits for the index's
// first refresh, so a quiet session whose ring is older than the retention by
// its own time but which still heartbeats keeps its ring, while a ring with
// no live registration is removed.
func TestAuditStartupSweepConsultsTheRegistry(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-30 * 24 * time.Hour)
	for _, id := range []string{"quiet", "orphan"} {
		ring := filepath.Join(dir, auditDirName, id)
		if err := os.MkdirAll(ring, 0o700); err != nil {
			t.Fatal(err)
		}
		seg := filepath.Join(ring, "seg-0000000000000001.jsonl")
		if err := os.WriteFile(seg, []byte(`{"t":"h","v":1,"session":"`+id+`"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(seg, old, old); err != nil {
			t.Fatal(err)
		}
	}
	register(t, dir, "quiet", t.TempDir(), time.Now()) // heartbeating, no events
	d := startIndexedDaemon(t, dir, newPageFake(), time.Hour)
	waitFor(t, "the orphan ring's removal", func() bool {
		_, err := os.Stat(filepath.Join(dir, auditDirName, "orphan"))
		return os.IsNotExist(err)
	})
	if err := d.AuditStore().Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, auditDirName, "quiet", "seg-0000000000000001.jsonl")); err != nil {
		t.Fatalf("the heartbeating session's ring was removed at startup: %v", err)
	}
}
