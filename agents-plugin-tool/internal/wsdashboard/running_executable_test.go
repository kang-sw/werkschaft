package wsdashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunningExecutableRecordFollowsOnlyTheLockOwner(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "ws-mcp.exe")
	if err := os.WriteFile(source, []byte("first build"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := func(exe, version string) (*Daemon, error) {
		return StartDaemon(DaemonOptions{Dir: dir, Home: t.TempDir(), Port: 0,
			Version: version, Executable: exe, BuildTime: 123, LockWait: 20 * time.Millisecond})
	}
	first, err := start(source, "0.50.7")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	read := func() ([]byte, runningExecutable) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, RunningExecutableFile))
		if err != nil {
			t.Fatal(err)
		}
		var record runningExecutable
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		return raw, record
	}
	raw, record := read()
	if record.SchemaVersion != 1 || record.Version != "0.50.7" || record.BuildTime != 123 ||
		!filepath.IsAbs(record.Executable) || filepath.Dir(record.Executable) != filepath.Join(dir, stagedBinDir) ||
		record.SHA256 != ExecutableContentHash(source) {
		t.Fatalf("record = %+v", record)
	}
	if err := os.WriteFile(source, []byte("second build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if loser, err := start(source, "0.50.8"); err != ErrLocked {
		if loser != nil {
			loser.Close()
		}
		t.Fatalf("losing starter = %v", err)
	}
	if got, _ := read(); string(got) != string(raw) {
		t.Fatal("losing starter overwrote restart evidence")
	}
	first.Close()

	// A pre-record daemon neither reads nor rewrites this additive file.
	// Simulate its StartDaemon inputs: no Executable field at all.
	older, err := start("", "0.50.1")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := read(); string(got) != string(raw) {
		t.Fatal("an older daemon run must leave restart evidence alone")
	}
	older.Close()

	second, err := start(source, "0.50.8")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	_, latest := read()
	if latest.Version != "0.50.8" || latest.Executable == record.Executable || latest.SHA256 != ExecutableContentHash(source) {
		t.Fatalf("successor record = %+v", latest)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	for _, retained := range []runningExecutable{record, latest} {
		if got := ExecutableContentHash(retained.Executable); got != retained.SHA256 {
			t.Fatalf("retained build %s changed/disappeared: %s", retained.Executable, got)
		}
	}
}

func TestRunningExecutableFailureDoesNotPublishEndpoint(t *testing.T) {
	dir := t.TempDir()
	_, err := StartDaemon(DaemonOptions{Dir: dir, Home: t.TempDir(), Port: 0,
		Executable: filepath.Join(t.TempDir(), "missing"), Version: "0.50.7"})
	if err == nil {
		t.Fatal("missing running build accepted")
	}
	for _, file := range []string{endpointFileName, RunningExecutableFile} {
		if _, err := os.Stat(filepath.Join(dir, file)); !os.IsNotExist(err) {
			t.Fatalf("published %s on failed start: %v", file, err)
		}
	}
	// Failure released the singleton.
	d, err := StartDaemon(DaemonOptions{Dir: dir, Home: t.TempDir(), Port: 0, LockWait: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
}

func TestRecordRunningExecutableUsesMatchingStagedCopyWhenLaunchPathOverwritten(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, stagedBinDir)
	srcDir := t.TempDir()

	oldBin := filepath.Join(srcDir, "ws-mcp-old")
	newBin := filepath.Join(srcDir, "ws-mcp-new")
	if err := os.WriteFile(oldBin, []byte("old-daemon-build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newBin, []byte("new-daemon-build"), 0o755); err != nil {
		t.Fatal(err)
	}
	newHash := ExecutableContentHash(newBin)
	stagedNew, err := DaemonExecutable(newBin, dir)
	if err != nil {
		t.Fatal(err)
	}
	stagedNew, err = filepath.Abs(stagedNew)
	if err != nil {
		t.Fatal(err)
	}

	// Model macOS bin/ws-mcp-dashboard initially holding the new build at
	// process startup, then concurrently overwritten with the old build before
	// dashboard.lock is acquired.
	launchPath := filepath.Join(binDir, "ws-mcp-dashboard")
	if err := copyExecutable(oldBin, launchPath); err != nil {
		t.Fatal(err)
	}

	d, err := StartDaemon(DaemonOptions{
		Dir:         dir,
		Home:        t.TempDir(),
		Port:        0,
		Version:     "0.53.2-dev",
		BuildTime:   789,
		ContentHash: newHash,
		Executable:  launchPath,
		LockWait:    20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StartDaemon: %v", err)
	}
	defer d.Close()

	raw, err := os.ReadFile(filepath.Join(dir, RunningExecutableFile))
	if err != nil {
		t.Fatal(err)
	}
	var rec runningExecutable
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.SHA256 != newHash || rec.Executable != stagedNew || rec.BuildTime != 789 {
		t.Fatalf("last-running-executable.json = %+v, want sha256=%q executable=%q build_time=789", rec, newHash, stagedNew)
	}
	if got := ExecutableContentHash(rec.Executable); got != newHash {
		t.Fatalf("recorded executable sha256 = %q, want %q", got, newHash)
	}
}

func TestRecordRunningExecutableRefusesMismatchedDiskExecutableWhenStagedMissing(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, stagedBinDir)
	srcDir := t.TempDir()

	oldBin := filepath.Join(srcDir, "ws-mcp-old")
	newBin := filepath.Join(srcDir, "ws-mcp-new")
	if err := os.WriteFile(oldBin, []byte("old-daemon-build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newBin, []byte("new-daemon-build"), 0o755); err != nil {
		t.Fatal(err)
	}
	newHash := ExecutableContentHash(newBin)
	oldHash := ExecutableContentHash(oldBin)

	// Seed an initial valid record from oldBin, then overwrite launchPath with
	// oldBin while claiming startup ContentHash == newHash (and no staged newBin).
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	launchPath := filepath.Join(binDir, "ws-mcp-dashboard")
	if err := copyExecutable(oldBin, launchPath); err != nil {
		t.Fatal(err)
	}
	if err := recordRunningExecutable(dir, launchPath, oldHash, "0.53.1-dev", 100); err != nil {
		t.Fatal(err)
	}
	priorRaw, err := os.ReadFile(filepath.Join(dir, RunningExecutableFile))
	if err != nil {
		t.Fatal(err)
	}

	if d, err := StartDaemon(DaemonOptions{
		Dir:         dir,
		Home:        t.TempDir(),
		Port:        0,
		Version:     "0.53.2-dev",
		BuildTime:   200,
		ContentHash: newHash,
		Executable:  launchPath,
		LockWait:    20 * time.Millisecond,
	}); err == nil {
		d.Close()
		t.Fatal("StartDaemon succeeded despite overwritten executable and missing staged copy")
	}

	afterRaw, err := os.ReadFile(filepath.Join(dir, RunningExecutableFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterRaw) != string(priorRaw) {
		t.Fatalf("last-running-executable.json changed on mismatch failure: got %s, want %s", afterRaw, priorRaw)
	}
	if _, err := os.Stat(filepath.Join(dir, endpointFileName)); !os.IsNotExist(err) {
		t.Fatalf("published %s after recordRunningExecutable mismatch: %v", endpointFileName, err)
	}
}
