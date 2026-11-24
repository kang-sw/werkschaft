package wslive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotInProcessLiveExemptsStaleAndIsNeverSerialized(t *testing.T) {
	base := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	snap := Snapshot{
		SchemaVersion: 1,
		ReplyID:       "hosted1",
		Adapter:       "codex",
		UpdatedAt:     base.UnixMilli(),
		HeartbeatMs:   HeartbeatInterval.Milliseconds(),
		InProcessLive: true,
	}
	if snap.Stale(base.Add(2 * time.Minute)) {
		t.Fatal("in-process live snapshot reported stale at +120s")
	}
	if snap.Stale(base.Add(10 * time.Minute)) {
		t.Fatal("in-process live snapshot reported stale at +10m")
	}

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "InProcessLive") || strings.Contains(string(raw), "inProcessLive") {
		t.Fatalf("InProcessLive leaked into JSON: %s", raw)
	}

	path := filepath.Join(t.TempDir(), SnapshotName)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	disk, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if disk.InProcessLive {
		t.Fatal("disk-loaded snapshot unexpectedly had InProcessLive=true")
	}
	if disk.Stale(base.Add(89 * time.Second)) {
		t.Fatal("disk snapshot expired before stale window")
	}
	if !disk.Stale(base.Add(91 * time.Second)) {
		t.Fatal("disk snapshot failed to expire past stale window")
	}
}
