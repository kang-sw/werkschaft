package wsmeta

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var errCrash = errors.New("simulated crash")

// crashAt returns Options whose Fault fails once at stage while *armed.
func crashAt(stage string, armed *bool) Options {
	opts := testOpts()
	opts.Fault = func(got string) error {
		if *armed && got == stage {
			*armed = false
			return errCrash
		}
		return nil
	}
	return opts
}

const (
	fencePerm       = "sessions/hostedlead/permission.json"
	fenceAbsentPerm = "sessions/clionly/permission.json"
	fenceHiring     = "sessions/hostedlead/hiring.json"
	hooksWitness    = "tracker-hooks-config.json"
)

var fencedSelector = []byte(`{"permission_level":"!cutover-fenced"}`)

func TestFenceLandedBeforeJournalMarkKeepsOriginal(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	armed := false
	s := openTest(t, dir, crashAt("fence-replaced", &armed))
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	// Each replacement lands; the process dies before marking it.
	for _, rel := range []string{fencePerm, fenceAbsentPerm} {
		armed = true
		if err := s.ApplyFence(ctx, rel, fencedSelector); !errors.Is(err, errCrash) {
			t.Fatalf("fence %s = %v", rel, err)
		}
	}
	armed = true
	if err := s.ApplyRemovalFence(ctx, "tracker-hooks.json", hooksWitness); !errors.Is(err, errCrash) {
		t.Fatalf("removal fence = %v", err)
	}
	res, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reused != 3 || len(res.Unfenced) != 0 || len(res.Pending) != 0 {
		t.Fatalf("retry stage = %+v", res)
	}
	caps := captureMap(t, s)
	if c := caps[fencePerm]; c.State != CapturePresent || string(c.Bytes) != `{"permission_level":"workdir-write","delegated_authority":false,"future_perm":"keep"}` || c.FenceState != FenceIntent || !c.FenceRequired {
		t.Fatalf("fenced capture = %+v", c)
	}
	if c := caps[fenceAbsentPerm]; c.State != CaptureAbsent {
		t.Fatalf("absent-before-fence capture = %+v", c)
	}
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["hostedlead"]; p.State != "valid" || p.Level != "workdir-write" {
		t.Fatalf("imported permission = %+v (want the staged original)", p)
	}
	if _, ok := snap.Permissions["clionly"]; ok {
		t.Fatal("a fence on an absent selection imported a row")
	}
	if c := captureMap(t, s)["tracker-hooks.json"]; c.State != CapturePresent || c.ImportStatus != ImportRetained {
		t.Fatalf("retained hooks config = %+v", c)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, hooksWitness)); err != nil || string(raw) != `{"hooks":[{"command":["notify"]}]}` {
		t.Fatalf("witness = %q, %v", raw, err)
	}
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); !errors.Is(err, ErrImportComplete) {
		t.Fatalf("fence after import = %v", err)
	}
}

// A legacy writer that leaves the fence's exact bytes is still not the
// fence: the staged original is not reused over its write (the old hiring
// toggle writes the same disabled document).
func TestByteIdenticalLegacyWriteIsNotTheFence(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	disabled := `{"enabled":false,"hired":true,"request_id":"req-1"}`
	if err := s.ApplyFence(ctx, fenceHiring, []byte(disabled)); err != nil {
		t.Fatal(err)
	}
	// The old daemon's toggle re-enables and then disables hiring again,
	// ending at the fence's bytes with a current time.
	put(t, dir, fenceHiring, `{"enabled":true,"hired":true,"request_id":"req-1"}`)
	put(t, dir, fenceHiring, disabled)
	res, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Unfenced, []string{fenceHiring}) || res.Reused != 0 {
		t.Fatalf("stage = %+v, want the hiring fence invalidated", res)
	}
	if c := captureMap(t, s)[fenceHiring]; string(c.Bytes) != disabled {
		t.Fatalf("capture = %q, want the owner's latest bytes", c.Bytes)
	}
}

// A re-fence never keeps the live file's mtime: a stat-keyed legacy cache
// whose identity survives the replace (NTFS tunneling keeps the creation
// time) would otherwise serve its stale bytes for a same-size fence.
func TestRefenceNeverReusesTheLiveMtime(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	prev := fenceSentinel
	t.Cleanup(func() { fenceSentinel = prev })
	first := randomFenceSentinel()
	draws := 0
	fenceSentinel = func() time.Time {
		draws++
		if draws <= 3 {
			return first
		}
		return first.Add(2 * time.Second)
	}
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(dir, filepath.FromSlash(fencePerm))
	before, err := os.Stat(abs)
	if err != nil || before.ModTime().Unix() != first.Unix() {
		t.Fatalf("first fence mtime = %v, %v; want %v", before, err, first)
	}
	// The same bytes again, drawing the live sentinel twice first.
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if after.ModTime().Unix() == before.ModTime().Unix() || after.Size() != before.Size() {
		t.Fatalf("re-fence mtime %v size %d kept the live mtime %v", after.ModTime(), after.Size(), before.ModTime())
	}
	res, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reused == 0 || len(res.Unfenced) != 0 || len(res.Pending) != 0 {
		t.Fatalf("re-fence not proven: %+v", res)
	}
}

// An invalidated fence keeps blocking the import across a re-stage and a
// restart until a fence is proven in place again.
func TestForeignWriteOverFenceReturnsToLiveAuthority(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	// The old daemon lowers the selection over the fence.
	lowered := `{"permission_level":"read-only"}`
	put(t, dir, fencePerm, lowered)

	if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrFenceInvalidated) {
		t.Fatalf("commit over an invalidated fence = %v", err)
	}
	if c := captureMap(t, s)[fencePerm]; c.FenceState != FenceLanded {
		t.Fatalf("refused commit changed the journal: %+v", c)
	}
	res, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Unfenced, []string{fencePerm}) || !reflect.DeepEqual(res.Pending, []string{fencePerm}) {
		t.Fatalf("stage = %+v", res)
	}
	if c := captureMap(t, s)[fencePerm]; string(c.Bytes) != lowered || c.FenceState != FenceNone || !c.FenceRequired {
		t.Fatalf("re-staged capture = %+v", c)
	}
	// Interrupted again before re-fencing: the obligation survives.
	if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrFencePending) {
		t.Fatalf("commit with a re-staged, unfenced path = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir, testOpts())
	if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrFencePending) {
		t.Fatalf("commit after restart = %v", err)
	}
	if done, _ := s.ImportComplete(ctx); done {
		t.Fatal("the import completed with the legacy path writable")
	}
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	var seen []Capture
	if _, err := s.Commit(ctx, withMarker(func(c []Capture) error { seen = c; return nil })); err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("BeforeMarker not called")
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["hostedlead"]; p.Level != "read-only" {
		t.Fatalf("permission = %+v, want the lowered selection", p)
	}
}

// An owner deleting the hooks config while its removal fence is pending
// (the move never happened) is not undone: the retry sees no witness and
// stages the absence.
func TestOwnerDeletionDuringPendingRemovalIsNotRestored(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	armed := false
	s := openTest(t, dir, crashAt("fence-prepared", &armed))
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	armed = true
	if err := s.ApplyRemovalFence(ctx, "tracker-hooks.json", hooksWitness); !errors.Is(err, errCrash) {
		t.Fatalf("removal fence = %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "tracker-hooks.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrFenceInvalidated) {
		t.Fatalf("commit = %v", err)
	}
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	if c := captureMap(t, s)["tracker-hooks.json"]; c.State != CaptureAbsent || c.Bytes != nil {
		t.Fatalf("hooks capture = %+v, want the owner's deletion", c)
	}
	for _, p := range []string{"tracker-hooks.json", hooksWitness} {
		if _, err := os.Lstat(filepath.Join(dir, p)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s recreated: %v", p, err)
		}
	}
}

func TestFenceRefusesUnstagedPathsAndPrunedSessions(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFence(ctx, "sessions/nosuch/permission.json", fencedSelector); err == nil {
		t.Fatal("fence on an unstaged path succeeded")
	}
	if err := s.ApplyRemovalFence(ctx, fenceAbsentPerm, "elsewhere.json"); err == nil {
		t.Fatal("removal fence on an absent capture succeeded")
	}
	if err := s.ApplyRemovalFence(ctx, "tracker-hooks.json", "../outside.json"); err == nil {
		t.Fatal("removal witness outside the dashboard accepted")
	}
	// A session pruned after staging is never recreated by its fence.
	if err := os.RemoveAll(filepath.Join(dir, "sessions", "worker")); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFence(ctx, "sessions/worker/permission.json", fencedSelector); err == nil {
		t.Fatal("fence into a pruned session succeeded")
	}
	if _, err := os.Lstat(filepath.Join(dir, "sessions", "worker")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pruned session recreated: %v", err)
	}
}

// A session pruned and recreated where the generation cannot tell (an inode
// reused without a birth time) never hands the deleted session's staged
// original to the new one, even when the new file has the fence's exact
// bytes: only the migration's sentinel proves a fence.
func TestMissedRecreationNeverReusesTheDeletedSessionsOriginal(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	prev := generationOf
	generationOf = func(path string, _ os.FileInfo) string { return "same:" + filepath.Base(path) }
	t.Cleanup(func() { generationOf = prev })
	s := openTest(t, dir, testOpts())
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "sessions", "hostedlead")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, fencePerm, string(fencedSelector))
	res, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Discarded != 0 {
		t.Fatalf("the simulated reuse was detected: %+v", res)
	}
	if res.Reused != 0 || !reflect.DeepEqual(res.Pending, []string{fencePerm}) {
		t.Fatalf("stage = %+v, want the new file staged and its fence pending", res)
	}
	caps := captureMap(t, s)
	if c := caps[fencePerm]; string(c.Bytes) != string(fencedSelector) {
		t.Fatalf("capture = %q, want the new session's bytes", c.Bytes)
	}
	if c := caps["sessions/hostedlead/agent.json"]; c.State != CaptureAbsent {
		t.Fatalf("deleted session's record still staged: %+v", c)
	}
}
