package wsdashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

var gcTestNow = time.Date(2026, 11, 29, 12, 0, 0, 0, time.UTC)

func gcTestConfig() CacheGCConfig {
	cfg := DefaultCacheGCConfig()
	cfg.Now = func() time.Time { return gcTestNow }
	return cfg
}

func gcDays(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func gcTouch(t *testing.T, path string, age time.Duration) {
	t.Helper()
	at := gcTestNow.Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func gcFile(t *testing.T, path, body string, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	gcTouch(t, path, age)
	return path
}

func gcPresent(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestCacheGCPrunesRollbackDirByAgeAndCount(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, rollbackDirName)
	mk := func(name string, age time.Duration) string {
		t.Helper()
		path := filepath.Join(root, name)
		gcFile(t, filepath.Join(path, "payload"), "x", age)
		gcTouch(t, path, age)
		return path
	}
	// Newest first: fresh(2h), d2, d3 are the kept three; d5 and d6 are
	// within 7 days but beyond the count; d8 and d30 are past 7 days.
	fresh := mk("swap-fresh", 2*time.Hour)
	d2 := mk("swap-d2", gcDays(2))
	d3 := mk("swap-d3", gcDays(3))
	d5 := mk("swap-d5", gcDays(5))
	d6 := mk("swap-d6", gcDays(6))
	d8 := mk("swap-d8", gcDays(8))
	d30 := mk("swap-d30", gcDays(30))
	cfg := gcTestConfig()

	if n := PruneRollbackDir(dir, cfg); n != 4 {
		t.Fatalf("pruned %d, want 4 (d5, d6, d8, d30)", n)
	}
	for path, want := range map[string]bool{fresh: true, d2: true, d3: true, d5: false, d6: false, d8: false, d30: false} {
		if got := gcPresent(path); got != want {
			t.Errorf("%s present = %v, want %v", filepath.Base(path), got, want)
		}
	}
	// Idempotent.
	if n := PruneRollbackDir(dir, cfg); n != 0 {
		t.Fatalf("second pass pruned %d", n)
	}
}

func TestCacheGCRollbackNeverRemovesWithinGraceEvenBeyondKeep(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, rollbackDirName)
	var paths []string
	for i := 0; i < 6; i++ {
		path := filepath.Join(root, fmt.Sprintf("swap-%d", i))
		gcFile(t, filepath.Join(path, "payload"), "x", time.Hour)
		gcTouch(t, path, time.Duration(i+1)*time.Hour)
		paths = append(paths, path)
	}
	if n := PruneRollbackDir(dir, gcTestConfig()); n != 0 {
		t.Fatalf("pruned %d entries under the 24h grace", n)
	}
	for _, path := range paths {
		if !gcPresent(path) {
			t.Fatalf("%s removed", path)
		}
	}
	// A missing rollback directory is quiet and creates nothing.
	if n := PruneRollbackDir(t.TempDir(), gcTestConfig()); n != 0 {
		t.Fatalf("missing dir pruned %d", n)
	}
}

func buildName(n int) string { return fmt.Sprintf("ws-mcp-dashboard-%016x", n) }

func TestPruneStagedExecutablesKeepsPinSetAndForeignEntries(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, stagedBinDir)
	build := func(n int, age time.Duration) string {
		t.Helper()
		path := gcFile(t, filepath.Join(binDir, buildName(n)), "binary", age)
		gcFile(t, path+stagedMetadataSuffix, `{"v":1}`, age)
		return path
	}
	b1 := build(1, gcDays(40)) // oldest, unpinned: goes
	b2 := build(2, gcDays(30)) // recorded for restart: pinned
	b3 := build(3, gcDays(20)) // the running exe: pinned
	b4 := build(4, gcDays(15)) // content hash of the running exe: pinned
	b5 := build(5, gcDays(12)) // beyond the newest five and old: goes
	b6 := build(6, gcDays(10)) // newest five
	b7 := build(7, gcDays(9))
	b8 := build(8, gcDays(8))
	b9 := build(9, gcDays(3))
	b10 := build(10, 2*time.Hour) // inside the grace

	stable := gcFile(t, filepath.Join(binDir, macStableExecutable), "mutable", gcDays(60))
	foreignDir := filepath.Join(binDir, "electron-package")
	gcFile(t, filepath.Join(foreignDir, "app.asar"), "x", gcDays(60))
	gcTouch(t, foreignDir, gcDays(60))
	buildNamedDir := filepath.Join(binDir, buildName(0xd1))
	gcFile(t, filepath.Join(buildNamedDir, "inner"), "x", gcDays(60))
	gcTouch(t, buildNamedDir, gcDays(60))
	link := filepath.Join(binDir, buildName(0xa1))
	linked := os.Symlink(b1, link) == nil
	foreignFiles := []string{
		gcFile(t, filepath.Join(binDir, "ws-mcp-dashboard-short.exe"), "x", gcDays(60)),
		gcFile(t, filepath.Join(binDir, ".ws-mcp-dashboard-other.123.tmp"), "x", gcDays(60)),
		gcFile(t, filepath.Join(binDir, "readme.txt"), "x", gcDays(60)),
		gcFile(t, filepath.Join(binDir, "other.metadata.json"), "{}", gcDays(60)),
		gcFile(t, filepath.Join(binDir, buildName(0xb1)+".bak"), "x", gcDays(60)),
	}
	// Sidecars with no build: the old one is orphaned, the young one may be
	// a launcher's write still in flight.
	orphanOld := gcFile(t, filepath.Join(binDir, buildName(0xc1)+stagedMetadataSuffix), "{}", gcDays(30))
	orphanYoung := gcFile(t, filepath.Join(binDir, buildName(0xc2)+stagedMetadataSuffix), "{}", time.Hour)

	record, err := json.Marshal(runningExecutable{1, b2, "1.0.0", strings.Repeat("0", 64), 0})
	if err != nil {
		t.Fatal(err)
	}
	gcFile(t, filepath.Join(dir, RunningExecutableFile), string(record), gcDays(1))
	hash4 := fmt.Sprintf("%016x", 4) + strings.Repeat("0", 48)

	n := pruneStagedExecutables(dir, b3, hash4, gcTestConfig(), nil)
	// b1, b5 and their two sidecars, plus the old orphan sidecar.
	if n != 5 {
		t.Fatalf("removed %d, want 5", n)
	}
	for name, want := range map[string]bool{"b1": false, "b2": true, "b3": true, "b4": true, "b5": false, "b6": true, "b7": true, "b8": true, "b9": true, "b10": true} {
		path := map[string]string{"b1": b1, "b2": b2, "b3": b3, "b4": b4, "b5": b5, "b6": b6, "b7": b7, "b8": b8, "b9": b9, "b10": b10}[name]
		if got := gcPresent(path); got != want {
			t.Errorf("%s present = %v, want %v", name, got, want)
		}
		if got := gcPresent(path + stagedMetadataSuffix); got != want {
			t.Errorf("%s sidecar present = %v, want %v", name, got, want)
		}
	}
	keep := append([]string{stable, foreignDir, filepath.Join(foreignDir, "app.asar"), buildNamedDir, orphanYoung}, foreignFiles...)
	if linked {
		keep = append(keep, link)
	}
	for _, path := range keep {
		if !gcPresent(path) {
			t.Errorf("foreign or pinned entry %s was removed", path)
		}
	}
	if gcPresent(orphanOld) {
		t.Error("an aged orphan sidecar survived")
	}
	if n := pruneStagedExecutables(dir, b3, hash4, gcTestConfig(), nil); n != 0 {
		t.Fatalf("second pass removed %d", n)
	}
}

func TestPruneStagedExecutablesPinsRunningExeByPathNameAndStableCopy(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, stagedBinDir)
	old := gcFile(t, filepath.Join(binDir, buildName(1)), "a", gcDays(90))
	other := gcFile(t, filepath.Join(binDir, buildName(2)), "b", gcDays(90))
	cfg := gcTestConfig()
	cfg.BinKeep = 1
	// Nothing newer exists: build 2 is the newest-one, build 1 is exe.
	gcTouch(t, other, gcDays(80))
	if n := pruneStagedExecutables(dir, old, "", cfg, nil); n != 0 {
		t.Fatalf("pruned %d with exe pinned and one newest kept", n)
	}
	// A running macOS daemon launched from the stable path pins its staged
	// twin by content hash, not by path.
	stable := gcFile(t, filepath.Join(binDir, macStableExecutable), "a", gcDays(90))
	twinHash := fmt.Sprintf("%016x", 1) + strings.Repeat("f", 48)
	third := gcFile(t, filepath.Join(binDir, buildName(3)), "c", gcDays(60))
	if n := pruneStagedExecutables(dir, stable, twinHash, cfg, nil); n != 1 || gcPresent(other) {
		t.Fatalf("pruned %d; build 2 present = %v (want it gone, 1 and 3 kept)", n, gcPresent(other))
	}
	if !gcPresent(old) || !gcPresent(third) || !gcPresent(stable) {
		t.Fatal("a pinned or newest entry was removed")
	}
}

func TestPruneStagedExecutablesStopsWhenTheRecordIsUnreadable(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, stagedBinDir)
	for i := 1; i <= 8; i++ {
		gcFile(t, filepath.Join(binDir, buildName(i)), "x", gcDays(30+i))
	}
	gcFile(t, filepath.Join(dir, RunningExecutableFile), "{not json", gcDays(1))
	if n := pruneStagedExecutables(dir, "", "", gcTestConfig(), nil); n != 0 {
		t.Fatalf("pruned %d with an unusable restart record", n)
	}
	// A missing record is not unusable: there is just no recorded pin.
	if err := os.Remove(filepath.Join(dir, RunningExecutableFile)); err != nil {
		t.Fatal(err)
	}
	if n := pruneStagedExecutables(dir, "", "", gcTestConfig(), nil); n != 3 {
		t.Fatalf("pruned %d without a record, want the 3 beyond the newest five", n)
	}
}

func TestPruneStagedExecutablesIgnoresRemovalRefusal(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a POSIX directory the process cannot write")
	}
	dir := t.TempDir()
	binDir := filepath.Join(dir, stagedBinDir)
	var paths []string
	for i := 1; i <= 7; i++ {
		paths = append(paths, gcFile(t, filepath.Join(binDir, buildName(i)), "x", gcDays(30+i)))
	}
	if err := os.Chmod(binDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(binDir, 0o755)
	if n := pruneStagedExecutables(dir, "", "", gcTestConfig(), nil); n != 0 {
		t.Fatalf("counted %d removals that the OS refused", n)
	}
	for _, path := range paths {
		if !gcPresent(path) {
			t.Fatalf("%s vanished", path)
		}
	}
}

func TestPruneStagedExecutablesPublicEntryUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, stagedBinDir)
	var paths []string
	for i := 1; i <= 7; i++ {
		// Real-time ages: PruneStagedExecutables reads the wall clock.
		path := filepath.Join(binDir, buildName(i))
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-gcDays(30 + i))
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	PruneStagedExecutables(dir, paths[6])
	// exe (the oldest, pinned) and the newest five stay; build 6 goes.
	for i, path := range paths {
		want := i != 5
		if got := gcPresent(path); got != want {
			t.Errorf("build %d present = %v, want %v", i+1, got, want)
		}
	}
}

func TestCacheGCConfigFromEnv(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	def := DefaultCacheGCConfig()
	if got := CacheGCConfigFromEnv(env(nil)); !reflect.DeepEqual(got, def) {
		t.Fatalf("empty env = %+v, want defaults %+v", got, def)
	}
	if def.RollbackKeep != 3 || def.RollbackMaxAge != gcDays(7) || def.BinKeep != 5 || def.BinMaxAge != gcDays(7) ||
		def.ProjTTL != gcDays(14) || def.SharedDocsTTL != gcDays(30) || def.ProjMaxBytes != 2<<30 || def.Interval != 6*time.Hour || def.Grace != 24*time.Hour {
		t.Fatalf("defaults drifted from the owner spec: %+v", def)
	}
	got := CacheGCConfigFromEnv(env(map[string]string{
		"WS_CACHE_GC_ROLLBACK_KEEP":     "7",
		"WS_CACHE_GC_ROLLBACK_MAX_AGE":  "10d",
		"WS_CACHE_GC_BIN_KEEP":          "0",
		"WS_CACHE_GC_BIN_MAX_AGE":       "36h",
		"WS_CACHE_GC_PROJ_TTL":          "2h", // below the grace: floored
		"WS_CACHE_GC_SHARED_DOCS_TTL":   "banana",
		"WS_CACHE_GC_PROJ_MAX_BYTES":    "1048576",
		"WS_CACHE_GC_INTERVAL":          "30m",
		"WS_CACHE_GC_DEAD_WORKTREE_AGE": "-3d",
	}))
	if got.RollbackKeep != 7 || got.RollbackMaxAge != gcDays(10) || got.BinKeep != 5 || got.BinMaxAge != 36*time.Hour ||
		got.ProjTTL != 24*time.Hour || got.SharedDocsTTL != gcDays(30) || got.ProjMaxBytes != 1<<20 || got.Interval != 30*time.Minute || got.DeadWorktreeAge != gcDays(14) {
		t.Fatalf("overrides = %+v", got)
	}
	for _, off := range []string{"off", "0", "FALSE"} {
		if CacheGCConfigFromEnv(env(map[string]string{"WS_CACHE_GC": off})).Enabled {
			t.Errorf("WS_CACHE_GC=%s left the GC enabled", off)
		}
	}
}

func TestCacheRootOnlyForTheDashboardStateDir(t *testing.T) {
	root := t.TempDir()
	if got, ok := cacheRootOf(filepath.Join(root, "dashboard")); !ok || got != root {
		t.Fatalf("cacheRootOf(dashboard) = %q, %v", got, ok)
	}
	if _, ok := cacheRootOf(root); ok {
		t.Fatal("an arbitrary directory was treated as a cache state dir")
	}
}

func gcWorktreeJSON(t *testing.T, dir, key, worktreePath string, seen time.Duration) {
	t.Helper()
	raw, err := json.Marshal(wsstate.WorktreeMetadata{SchemaVersion: 1, WorktreePath: worktreePath, WorktreeKey: key, LastSeenAt: gcTestNow.Add(-seen).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	gcFile(t, filepath.Join(dir, "worktree.json"), string(raw), seen)
}

// runCacheGC covers all three areas in one pass, with a session still naming
// one deleted worktree and a stop that ends the pass.
func TestDaemonRunCacheGCCoversRollbackBinAndProj(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, dirName)
	d := &Daemon{dir: state, stop: make(chan struct{})}
	cfg := gcTestConfig()

	rollbackOld := filepath.Join(state, rollbackDirName, "swap-old")
	gcFile(t, filepath.Join(rollbackOld, "x"), "x", gcDays(20))
	gcTouch(t, rollbackOld, gcDays(20))
	binOld := gcFile(t, filepath.Join(state, stagedBinDir, buildName(1)), "x", gcDays(40))
	for i := 2; i <= 6; i++ {
		gcFile(t, filepath.Join(state, stagedBinDir, buildName(i)), "x", gcDays(10-i))
	}

	proj := filepath.Join(root, "proj")
	oldReview := gcFile(t, filepath.Join(proj, "0123abcd", "review-paths", "old.md"), "x", gcDays(15))
	midDoc := gcFile(t, filepath.Join(proj, "0123abcd", "shared", "docs", "mid.md"), "x", gcDays(20))
	oldDoc := gcFile(t, filepath.Join(proj, "0123abcd", "shared", "docs", "old.md"), "x", gcDays(31))
	adhoc := gcFile(t, filepath.Join(proj, "0123abcd", "scratch", "old.txt"), "x", gcDays(99))

	gone := filepath.Join(t.TempDir(), "deleted-worktree")
	deadKey, namedKey := "0123abcd@00000001", "0123abcd@00000002"
	for _, key := range []string{deadKey, namedKey} {
		dir := filepath.Join(proj, key)
		gcFile(t, filepath.Join(dir, "state.sqlite"), "x", gcDays(40))
		gcWorktreeJSON(t, dir, key, filepath.Join(gone, key), gcDays(40))
		gcTouch(t, dir, gcDays(40))
	}
	// A daemon-known session still names the second worktree's path.
	if err := WriteRegistration(state, Registration{ReplyID: "namedsession", Project: filepath.Join(gone, namedKey), HeartbeatAt: stampFor(gcTestNow)}); err != nil {
		t.Fatal(err)
	}

	d.runCacheGC(cfg)

	for path, want := range map[string]bool{
		rollbackOld: false, binOld: false, oldReview: false, midDoc: true, oldDoc: false, adhoc: true,
		filepath.Join(proj, deadKey): false, filepath.Join(proj, namedKey): true,
	} {
		if got := gcPresent(path); got != want {
			t.Errorf("%s present = %v, want %v", strings.TrimPrefix(path, root), got, want)
		}
	}

	// A stopped daemon runs no pass at all.
	again := gcFile(t, filepath.Join(proj, "0123abcd", "review-paths", "again.md"), "x", gcDays(30))
	close(d.stop)
	d.runCacheGC(cfg)
	if !gcPresent(again) {
		t.Fatal("a pass ran after the daemon stopped")
	}
	// And a state dir that is not <root>/dashboard collects nothing.
	scratch := &Daemon{dir: filepath.Join(root, "elsewhere"), stop: make(chan struct{})}
	scratch.runCacheGC(cfg)
	if !gcPresent(again) {
		t.Fatal("a scratch state dir triggered proj GC")
	}
}

// The started daemon runs the pass in the background and Close waits for it.
func TestStartDaemonRunsBackgroundCacheGC(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, dirName)
	oldReview := filepath.Join(root, "proj", "0123abcd", "review-paths", "old.md")
	freshReview := filepath.Join(root, "proj", "0123abcd", "review-paths", "fresh.md")
	for path, age := range map[string]time.Duration{oldReview: gcDays(30), freshReview: time.Hour} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	cfg := DefaultCacheGCConfig()
	cfg.StartDelay = 5 * time.Millisecond
	d, err := StartDaemon(DaemonOptions{Dir: state, Home: t.TempDir(), Version: "0.53.1", noGitBadge: true, CacheGC: &cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	waitFor(t, "background cache gc", func() bool { return !gcPresent(oldReview) })
	if !gcPresent(freshReview) {
		t.Fatal("the pass removed a fresh file")
	}
	d.Close()
	select {
	case <-d.gcDone:
	default:
		t.Fatal("Close returned before the cache GC loop ended")
	}

	// Disabled: no loop, nothing collected.
	root2 := t.TempDir()
	keep := filepath.Join(root2, "proj", "0123abcd", "review-paths", "old.md")
	if err := os.MkdirAll(filepath.Dir(keep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-gcDays(30))
	if err := os.Chtimes(keep, at, at); err != nil {
		t.Fatal(err)
	}
	off := cfg
	off.Enabled = false
	d2, err := StartDaemon(DaemonOptions{Dir: filepath.Join(root2, dirName), Home: t.TempDir(), Version: "0.53.1", noGitBadge: true, CacheGC: &off})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d2.Close)
	if d2.gcDone != nil {
		t.Fatal("a disabled GC started its loop")
	}
	time.Sleep(50 * time.Millisecond)
	if !gcPresent(keep) {
		t.Fatal("a disabled GC removed a file")
	}
}

func TestPruneRegistrationsWithAgesByClosedAtBeforeDirectoryModTime(t *testing.T) {
	dir := t.TempDir()
	now := testNow
	old := now.Add(-RegistryRetention - time.Hour)
	records := map[string]agentRecordEnd{}
	session := func(id string, rec *agentRecordEnd) {
		t.Helper()
		// A registration with no timestamp leaves LastSeen zero.
		if err := WriteRegistration(dir, Registration{ReplyID: id, Project: "/p", Hosted: rec != nil}); err != nil {
			t.Fatal(err)
		}
		if rec != nil {
			records[id] = *rec
		}
		// The wsmeta cutover rewrites permission.json: the directory is fresh.
		if err := os.WriteFile(filepath.Join(SessionDir(dir, id), "permission.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(SessionDir(dir, id), now, now); err != nil {
			t.Fatal(err)
		}
	}
	closedOld := stampFor(old)
	closedRecent := stampFor(now.Add(-time.Hour))
	session("closedold", &agentRecordEnd{Lead: true, ClosedAt: closedOld})
	session("handedold", &agentRecordEnd{Lead: true, HandedOffAt: closedOld})
	session("closedrecent", &agentRecordEnd{Lead: true, ClosedAt: closedRecent})
	// The later stamp ages the session: closed long ago, handed off again recently.
	session("reopened", &agentRecordEnd{Lead: true, ClosedAt: closedOld, HandedOffAt: closedRecent})
	session("garbagestamp", &agentRecordEnd{Lead: true, ClosedAt: "not a time"})
	// No record at all: the directory's own fresh ModTime still rules.
	session("plain", nil)
	// Retained: Pi history, and a session the runner still serves.
	session("closedpi", &agentRecordEnd{Lead: true, Backend: "pi", ClosedAt: closedOld})
	session("openlead", &agentRecordEnd{Lead: true})
	// A retired-pi-history archive beside sessions/ is outside the walk.
	archive := filepath.Join(dir, "retired-pi-history", "closedpi", "agent.json")
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(archive, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Dir(archive), old, old); err != nil {
		t.Fatal(err)
	}

	lookup := func(id string) (agentRecordEnd, bool) {
		rec, ok := records[id]
		return rec, ok
	}
	if n := pruneRegistrationsWith(dir, now, RegistryRetention, lookup, nil, nil); n != 2 {
		t.Fatalf("pruned %d, want closedold and handedold", n)
	}
	for id, want := range map[string]bool{
		"closedold": false, "handedold": false, "closedrecent": true, "reopened": true, "garbagestamp": true,
		"plain": true, "closedpi": true, "openlead": true,
	} {
		if got := gcPresent(SessionDir(dir, id)); got != want {
			t.Errorf("%s present = %v, want %v", id, got, want)
		}
	}
	if !gcPresent(archive) {
		t.Fatal("retired-pi-history was touched")
	}
}

func TestPollPrunesExpiredSessionsOnTheHourlyTick(t *testing.T) {
	dir := t.TempDir()
	project := t.TempDir()
	old := time.Now().Add(-RegistryRetention - 2*time.Hour)
	register(t, dir, "expiredsess", project, old)
	register(t, dir, "freshsess", project, time.Now())
	startIndexedDaemon(t, dir, newFakeSource(), time.Hour, func(o *DaemonOptions) {
		o.sessionPruneEvery = 20 * time.Millisecond
	})
	waitFor(t, "periodic prune", func() bool { return !gcPresent(SessionDir(dir, "expiredsess")) })
	if !gcPresent(SessionDir(dir, "freshsess")) {
		t.Fatal("the periodic prune removed a fresh session")
	}
	// The default tick is hourly: no daemon prunes faster by accident.
	if sessionPruneEveryDefault != time.Hour {
		t.Fatalf("default prune tick = %v", sessionPruneEveryDefault)
	}
}
