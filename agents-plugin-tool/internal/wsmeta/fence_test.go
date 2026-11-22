package wsmeta

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
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
	fenceHooks      = "tracker-hooks.json"
	hooksWitness    = "tracker-hooks-config.json"
	hooksBytes      = `{"hooks":[{"command":["notify"]}]}`
	permOriginal    = `{"permission_level":"workdir-write","delegated_authority":false,"future_perm":"keep"}`
)

var fencedSelector = []byte(`{"permission_level":"!cutover-fenced"}`)

func mustStage(t *testing.T, s *Store) StageResult {
	t.Helper()
	res, err := s.Stage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readRel(t *testing.T, dir, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(raw)
}

// A publication interrupted before its mark is proven by the next pass, but
// stays pending until the fence is re-applied (a pass cannot tell whether it
// is durable); the re-application only marks it, and the import keeps the
// originals.
func TestFenceLandedBeforeJournalMarkKeepsOriginal(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	armed := false
	s := openTest(t, dir, crashAt("fence-replaced", &armed))
	mustStage(t, s)
	for _, rel := range []string{fencePerm, fenceAbsentPerm} {
		armed = true
		if err := s.ApplyFence(ctx, rel, fencedSelector); !errors.Is(err, errCrash) {
			t.Fatalf("fence %s = %v", rel, err)
		}
	}
	armed = true
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, errCrash) {
		t.Fatalf("removal fence = %v", err)
	}
	res := mustStage(t, s)
	if want := []string{fencePerm, fenceAbsentPerm, fenceHooks}; res.Reused != 3 || len(res.Unfenced) != 0 || !sameSet(res.Pending, want) {
		t.Fatalf("retry stage = %+v", res)
	}
	caps := captureMap(t, s)
	if c := caps[fencePerm]; c.State != CapturePresent || string(c.Bytes) != permOriginal || c.FenceState != FenceIntent || !c.FenceRequired {
		t.Fatalf("fenced capture = %+v", c)
	}
	if c := caps[fenceAbsentPerm]; c.State != CaptureAbsent {
		t.Fatalf("absent-before-fence capture = %+v", c)
	}
	if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrFencePending) {
		t.Fatalf("commit over unlanded fences = %v", err)
	}
	for _, rel := range []string{fencePerm, fenceAbsentPerm} {
		if err := s.ApplyFence(ctx, rel, fencedSelector); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
		t.Fatal(err)
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
	if c := captureMap(t, s)[fenceHooks]; c.State != CapturePresent || c.ImportStatus != ImportRetained {
		t.Fatalf("retained hooks config = %+v", c)
	}
	if got := readRel(t, dir, hooksWitness); got != hooksBytes {
		t.Fatalf("witness = %q", got)
	}
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); !errors.Is(err, ErrImportComplete) {
		t.Fatalf("fence after import = %v", err)
	}
}

func sameSet(a, b []string) bool {
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return len(a) == len(b)
}

// A retry interrupted after recording but before publishing never replaces
// the proof of a publication an earlier attempt left in place: the record
// stands while its publication is live, so the migration's own bytes are
// never staged as the original.
func TestInterruptedRetryKeepsTheFirstPublicationProvable(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	stage := ""
	opts := testOpts()
	opts.Fault = func(got string) error {
		if got == stage {
			stage = ""
			return errCrash
		}
		return nil
	}
	s := openTest(t, dir, opts)
	mustStage(t, s)
	stage = "fence-replaced"
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); !errors.Is(err, errCrash) {
		t.Fatalf("first attempt = %v", err)
	}
	first := captureMap(t, s)[fencePerm].FenceModTime
	for _, between := range []bool{false, true} {
		if between {
			mustStage(t, s)
		}
		stage = "fence-prepared"
		if err := s.ApplyFence(ctx, fencePerm, fencedSelector); !errors.Is(err, errCrash) {
			t.Fatalf("retry = %v", err)
		}
		if got := captureMap(t, s)[fencePerm].FenceModTime; got != first {
			t.Fatalf("the retry replaced the live publication's sentinel %d with %d", first, got)
		}
	}
	res := mustStage(t, s)
	if res.Reused == 0 || len(res.Unfenced) != 0 || !reflect.DeepEqual(res.Pending, []string{fencePerm}) {
		t.Fatalf("stage = %+v, want the first publication proven and pending its mark", res)
	}
	if c := captureMap(t, s)[fencePerm]; string(c.Bytes) != permOriginal {
		t.Fatalf("original = %q, want the legacy selection", c.Bytes)
	}
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["hostedlead"]; p.State != "valid" || p.Level != "workdir-write" {
		t.Fatalf("imported permission = %+v, want the legacy selection", p)
	}
}

// A publication interrupted before it is marked landed is not trusted by
// the import: it stays pending until a re-application makes it durable. A
// replacement is interrupted after its rename, before its directory sync; a
// removal after the legacy path's deletion.
func TestUnsyncedPublicationNeedsReapplyBeforeCommit(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	crash := ""
	opts := testOpts()
	opts.Fault = func(got string) error {
		if got == crash {
			crash = ""
			return errCrash
		}
		return nil
	}
	s := openTest(t, dir, opts)
	mustStage(t, s)
	crash = "fence-renamed"
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); !errors.Is(err, errCrash) {
		t.Fatalf("fence = %v", err)
	}
	crash = "fence-deleted"
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, errCrash) {
		t.Fatalf("removal fence = %v", err)
	}
	if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrFencePending) {
		t.Fatalf("commit over unsynced publications = %v", err)
	}
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
}

// The publication is durable before it is marked landed: the temporary
// file's data and sentinel time are synced before the rename, and the
// directory after it.
func TestFencePublicationIsDurableBeforeItLands(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	var events []string
	prevFile, prevDir := syncFile, syncDirectory
	t.Cleanup(func() { syncFile, syncDirectory = prevFile, prevDir })
	syncFile = func(p string) error {
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		sentinel := info.ModTime().Year() < 2010 && info.ModTime().Nanosecond() == 0
		events = append(events, fmt.Sprintf("sync-file:%s:sentinel=%v", filepath.Base(filepath.Dir(p)), sentinel))
		return prevFile(p)
	}
	syncDirectory = func(d string) error {
		rel, _ := filepath.Rel(dir, d)
		events = append(events, "sync-dir:"+filepath.ToSlash(rel))
		return prevDir(d)
	}
	opts := testOpts()
	opts.Fault = func(stage string) error {
		events = append(events, stage)
		return nil
	}
	s := openTest(t, dir, opts)
	mustStage(t, s)
	events = nil
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	// before-commit is a journal transaction: the record before the
	// publication, the landed mark after it.
	want := []string{"before-commit", "fence-prepared", "sync-file:hostedlead:sentinel=true", "fence-renamed", "sync-dir:sessions/hostedlead", "fence-replaced", "before-commit"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("replacement events = %v, want %v", events, want)
	}
	events = nil
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
		t.Fatal(err)
	}
	// A removal publishes the original at the witness, then deletes the
	// legacy path durably (a rename to a dot name, synced, then unlinked).
	if want := []string{"before-commit", "fence-prepared", "sync-file:dashboard:sentinel=true", "fence-renamed", "sync-dir:.", "sync-dir:.", "fence-deleted", "fence-replaced", "before-commit"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("removal events = %v, want %v", events, want)
	}
}

// A legacy writer that leaves the fence's exact bytes is still not the
// fence: the staged original is not reused over its write (the old hiring
// toggle writes the same disabled document).
func TestByteIdenticalLegacyWriteIsNotTheFence(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	mustStage(t, s)
	disabled := `{"enabled":false,"hired":true,"request_id":"req-1"}`
	if err := s.ApplyFence(ctx, fenceHiring, []byte(disabled)); err != nil {
		t.Fatal(err)
	}
	// The old daemon's toggle re-enables and then disables hiring again,
	// ending at the fence's bytes with a current time.
	put(t, dir, fenceHiring, `{"enabled":true,"hired":true,"request_id":"req-1"}`)
	put(t, dir, fenceHiring, disabled)
	res := mustStage(t, s)
	if !reflect.DeepEqual(res.Unfenced, []string{fenceHiring}) || res.Reused != 0 {
		t.Fatalf("stage = %+v, want the hiring fence invalidated", res)
	}
	if c := captureMap(t, s)[fenceHiring]; string(c.Bytes) != disabled {
		t.Fatalf("capture = %q, want the owner's latest bytes", c.Bytes)
	}
}

// A fresh record's sentinel never repeats the live file's mtime: a
// stat-keyed legacy cache whose identity survives the replace (NTFS
// tunneling keeps the creation time) would otherwise serve its stale bytes
// for a same-size fence. Re-applying a landed fence draws nothing and
// writes nothing.
func TestFenceSentinelDiffersFromTheLiveMtime(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	mustStage(t, s)
	prev := fenceSentinel
	t.Cleanup(func() { fenceSentinel = prev })
	collide := randomFenceSentinel()
	abs := filepath.Join(dir, filepath.FromSlash(fencePerm))
	if err := os.Chtimes(abs, collide, collide); err != nil {
		t.Fatal(err)
	}
	mustStage(t, s)
	draws := 0
	fenceSentinel = func() time.Time {
		draws++
		if draws <= 2 {
			return collide
		}
		return collide.Add(2 * time.Second)
	}
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().Unix() == collide.Unix() {
		t.Fatalf("fence kept the live mtime %v", collide)
	}
	n := draws
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	again, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if draws != n || !again.ModTime().Equal(info.ModTime()) || !os.SameFile(info, again) {
		t.Fatalf("re-applying a landed fence republished it (draws %d -> %d)", n, draws)
	}
	if res := mustStage(t, s); len(res.Pending) != 0 || res.Reused == 0 {
		t.Fatalf("stage = %+v", res)
	}
}

// A fence refuses a live file that is neither the staged original nor its
// own publication: the legacy write is staged first, never lost.
func TestFenceRefusesAnUnstagedLegacyWrite(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	mustStage(t, s)
	lowered := `{"permission_level":"read-only"}`
	put(t, dir, fencePerm, lowered)
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); !errors.Is(err, ErrFenceStale) {
		t.Fatalf("fence over an unstaged write = %v", err)
	}
	put(t, dir, fenceHooks, `{"hooks":[]}`)
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, ErrFenceStale) {
		t.Fatalf("removal over an unstaged write = %v", err)
	}
	if got := readRel(t, dir, fencePerm); got != lowered {
		t.Fatalf("legacy write replaced: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, hooksWitness)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused removal moved the file: %v", err)
	}
	mustStage(t, s)
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
}

// An invalidated fence keeps blocking the import across a re-stage and a
// restart until a fence is proven in place again.
func TestForeignWriteOverFenceReturnsToLiveAuthority(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	mustStage(t, s)
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
	res := mustStage(t, s)
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
// (nothing was published) is not undone: the re-applied removal of an
// absent original leaves no witness either.
func TestOwnerDeletionDuringPendingRemovalIsNotRestored(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	armed := false
	s := openTest(t, dir, crashAt("fence-prepared", &armed))
	mustStage(t, s)
	armed = true
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, errCrash) {
		t.Fatalf("removal fence = %v", err)
	}
	if err := os.Remove(filepath.Join(dir, fenceHooks)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrFenceInvalidated) {
		t.Fatalf("commit = %v", err)
	}
	mustStage(t, s)
	if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrFencePending) {
		t.Fatalf("commit before the removal is re-applied = %v", err)
	}
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	if c := captureMap(t, s)[fenceHooks]; c.State != CaptureAbsent || c.Bytes != nil {
		t.Fatalf("hooks capture = %+v, want the owner's deletion", c)
	}
	for _, p := range []string{fenceHooks, hooksWitness} {
		if _, err := os.Lstat(filepath.Join(dir, p)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s recreated: %v", p, err)
		}
	}
}

// After a removal landed and the old daemon's owner recreated the config,
// re-fencing publishes the new config over the stale witness at the same
// input path; the witness name is fixed per path.
func TestRemovalRefenceReplacesAStaleWitness(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	mustStage(t, s)
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
		t.Fatal(err)
	}
	recreated := `{"hooks":[{"command":["page"]}]}`
	put(t, dir, fenceHooks, recreated)
	res := mustStage(t, s)
	if !reflect.DeepEqual(res.Unfenced, []string{fenceHooks}) || !reflect.DeepEqual(res.Pending, []string{fenceHooks}) {
		t.Fatalf("stage = %+v", res)
	}
	if err := s.ApplyRemovalFence(ctx, fenceHooks, "other-config.json"); !errors.Is(err, ErrFenceConflict) {
		t.Fatalf("removal to another witness = %v", err)
	}
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	if c := captureMap(t, s)[fenceHooks]; string(c.Bytes) != recreated || c.ImportStatus != ImportRetained {
		t.Fatalf("retained config = %+v", c)
	}
	if got := readRel(t, dir, hooksWitness); got != recreated {
		t.Fatalf("witness = %q, want the recreated config", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, fenceHooks)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy config left in place: %v", err)
	}
}

// The witness is a plain sibling name on every platform, so the publication
// stays in the path's directory. A legacy symbolic link is read through: the
// witness becomes a regular copy of its target, and the target is kept.
func TestRemovalWitnessStaysBesideItsPath(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	mustStage(t, s)
	for _, w := range []string{"", "../outside.json", `..\outside.json`, `sub\w.json`, "sub/w.json", "c:w.json", ".hidden.json", "..", "plan-usage.json", "memos", fenceHooks} {
		if err := s.ApplyRemovalFence(ctx, fenceHooks, w); err == nil {
			t.Fatalf("witness %q accepted", w)
		}
	}
	if got := readRel(t, dir, fenceHooks); got != hooksBytes {
		t.Fatalf("a refused witness moved the config: %q", got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	put(t, dir, "hooks-src/conf.json", hooksBytes)
	abs := filepath.Join(dir, fenceHooks)
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("hooks-src", "conf.json"), abs); err != nil {
		t.Fatal(err)
	}
	mustStage(t, s)
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
		t.Fatal(err)
	}
	if res := mustStage(t, s); len(res.Pending) != 0 || len(res.Unfenced) != 0 {
		t.Fatalf("moved link not proven: %+v", res)
	}
	if info, err := os.Lstat(filepath.Join(dir, hooksWitness)); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("witness = %v, %v; want a regular copy", info, err)
	}
	if got := readRel(t, dir, hooksWitness); got != hooksBytes {
		t.Fatalf("witness reads %q", got)
	}
	if got := readRel(t, dir, "hooks-src/conf.json"); got != hooksBytes {
		t.Fatalf("link target = %q, want it kept", got)
	}
	if _, err := os.Lstat(abs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy link left in place: %v", err)
	}
}

func TestFenceRefusesUnstagedPathsAndPrunedSessions(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	mustStage(t, s)
	if err := s.ApplyFence(ctx, "sessions/nosuch/permission.json", fencedSelector); err == nil {
		t.Fatal("fence on an unstaged path succeeded")
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
	generationOf = func(path string, _ os.FileInfo) (string, error) { return "same:" + filepath.Base(path), nil }
	t.Cleanup(func() { generationOf = prev })
	s := openTest(t, dir, testOpts())
	mustStage(t, s)
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "sessions", "hostedlead")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, fencePerm, string(fencedSelector))
	res := mustStage(t, s)
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

// A session identity lookup that fails (a handle held, statx failing) is
// inconclusive: the pass fails and changes nothing, so the session's journal
// rows, its fenced original included, survive until a pass can tell.
func TestInconclusiveSessionIdentityKeepsItsJournal(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	mustStage(t, s)
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	before := captureMap(t, s)
	prev := generationOf
	t.Cleanup(func() { generationOf = prev })
	generationOf = func(path string, info os.FileInfo) (string, error) {
		if filepath.Base(path) == "hostedlead" {
			return "", errors.New("sharing violation")
		}
		return prev(path, info)
	}
	if _, err := s.Stage(ctx); err == nil || !strings.Contains(err.Error(), "hostedlead") {
		t.Fatalf("stage with an inconclusive identity = %v", err)
	}
	if _, err := s.Commit(ctx, testCommit()); err == nil {
		t.Fatal("commit with an inconclusive identity succeeded")
	}
	if after := captureMap(t, s); !reflect.DeepEqual(after[fencePerm], before[fencePerm]) || len(after) != len(before) {
		t.Fatalf("journal changed: %+v", after[fencePerm])
	}
	generationOf = prev
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["hostedlead"]; p.Level != "workdir-write" {
		t.Fatalf("permission = %+v, want the staged original", p)
	}
}

// A retry in an environment that cannot read birth times (a seccomp filter
// rejecting statx) reports the same directory without one. That is the same
// incarnation: the session keeps its journal rows and its fenced original.
func TestIdentityWithoutBirthTimeIsTheSameSession(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	prev := generationOf
	t.Cleanup(func() { generationOf = prev })
	btime := true
	generationOf = func(p string, _ os.FileInfo) (string, error) {
		gen := "ino:8:1:" + filepath.Base(p)
		if btime {
			gen += btimeTag + "1700000000.000000001"
		}
		return gen, nil
	}
	s := openTest(t, dir, testOpts())
	mustStage(t, s)
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	btime = false
	res := mustStage(t, s)
	if res.Discarded != 0 || len(res.Pending) != 0 {
		t.Fatalf("stage without birth times = %+v", res)
	}
	if c := captureMap(t, s)[fencePerm]; string(c.Bytes) != permOriginal {
		t.Fatalf("fenced original = %q", c.Bytes)
	}
	// A different identity, or two different birth times, is another
	// incarnation.
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"ino:8:1:5", "ino:8:1:5" + btimeTag + "1.0", true},
		{"ino:8:1:5" + btimeTag + "1.0", "ino:8:1:5" + btimeTag + "2.0", false},
		{"ino:8:1:5", "ino:8:1:6", false},
		{"ino:8:1:5" + btimeTag + "1.0", "ino:8:1:6" + btimeTag + "1.0", false},
	} {
		if got := sameGeneration(c.a, c.b); got != c.same {
			t.Errorf("sameGeneration(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// Fence calls, staging and the import commit are serialized end to end: a
// second fence or a commit cannot run between a fence's record and its
// publication, so the live publication is always the journal's record and a
// migration-written file is never staged as an original.
func TestConcurrentCutoverCallsWaitForAPublication(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	opts := testOpts()
	opts.Fault = func(stage string) error {
		if stage == "fence-prepared" {
			first := false
			once.Do(func() { first = true })
			if first {
				close(reached)
				<-release
			}
		}
		return nil
	}
	s := openTest(t, dir, opts)
	mustStage(t, s)
	errA, errB := make(chan error, 1), make(chan error, 1)
	commit := make(chan error, 1)
	go func() { errA <- s.ApplyFence(ctx, fencePerm, fencedSelector) }()
	<-reached
	go func() { errB <- s.ApplyFence(ctx, fencePerm, []byte(`{"permission_level":"!other-fence"}`)) }()
	go func() {
		_, err := s.Commit(ctx, testCommit())
		commit <- err
	}()
	select {
	case err := <-errB:
		close(release)
		<-errA
		t.Fatalf("a second fence ran during the first's publication: %v", err)
	case err := <-commit:
		close(release)
		<-errA
		t.Fatalf("a commit ran during a fence's publication: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-errA; err != nil {
		t.Fatal(err)
	}
	if err := <-errB; !errors.Is(err, ErrFenceConflict) && !errors.Is(err, ErrImportComplete) {
		t.Fatalf("second fence = %v, want a conflict or the completed import", err)
	}
	if err := <-commit; err != nil {
		t.Fatal(err)
	}
	if got := readRel(t, dir, fencePerm); got != string(fencedSelector) {
		t.Fatalf("live selector = %q", got)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["hostedlead"]; p.Level != "workdir-write" {
		t.Fatalf("permission = %+v, want the staged original", p)
	}
}

// A removal whose record was written after a re-stage is never proven by
// the witness an earlier removal left, even when the recreated config has
// the same bytes: the old witness is durably removed before the new record,
// so an owner's later deletion of the config stays a deletion.
func TestStaleWitnessNeverProvesANewRemoval(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	armed := false
	s := openTest(t, dir, crashAt("fence-prepared", &armed))
	mustStage(t, s)
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
		t.Fatal(err)
	}
	put(t, dir, fenceHooks, hooksBytes)
	mustStage(t, s)
	armed = true
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, errCrash) {
		t.Fatalf("interrupted removal = %v", err)
	}
	if err := os.Remove(filepath.Join(dir, fenceHooks)); err != nil {
		t.Fatal(err)
	}
	res := mustStage(t, s)
	if res.Reused != 0 || !reflect.DeepEqual(res.Unfenced, []string{fenceHooks}) || !reflect.DeepEqual(res.Pending, []string{fenceHooks}) {
		t.Fatalf("stage after the owner's deletion = %+v, want the deletion staged", res)
	}
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	if c := captureMap(t, s)[fenceHooks]; c.State != CaptureAbsent {
		t.Fatalf("hooks capture = %+v, want the owner's deletion", c)
	}
	if _, err := os.Lstat(filepath.Join(dir, hooksWitness)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("witness of the deleted config: %v", err)
	}
}

// A removal overwrites or deletes at the witness path only the file the
// migration published there. A file there it did not publish (a sibling, or
// its own publication edited since) refuses the removal and is kept. A
// legacy link to the empty witness dangles and refuses the cutover. Only
// fixed bundled paths, which keep their rows while absent, are removed.
func TestRemovalNeverTouchesAFileItDidNotPlace(t *testing.T) {
	ctx := context.Background()
	const ownerConfig = `{"hooks":[{"command":["owner"]}]}`
	occupied := func(t *testing.T, s *Store, dir string, want map[string]string) {
		t.Helper()
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, ErrWitnessOccupied) {
			t.Fatalf("removal = %v, want ErrWitnessOccupied", err)
		}
		for rel, content := range want {
			if got := readRel(t, dir, rel); got != content {
				t.Fatalf("%s = %q, want %q", rel, got, content)
			}
		}
	}
	t.Run("existing sibling", func(t *testing.T) {
		dir := legacyFixture(t)
		put(t, dir, hooksWitness, ownerConfig)
		s := openTest(t, dir, testOpts())
		mustStage(t, s)
		occupied(t, s, dir, map[string]string{fenceHooks: hooksBytes, hooksWitness: ownerConfig})
		// Once the owner clears it, the removal proceeds.
		if err := os.Remove(filepath.Join(dir, hooksWitness)); err != nil {
			t.Fatal(err)
		}
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("own leftover edited in place", func(t *testing.T) {
		dir := legacyFixture(t)
		s := openTest(t, dir, testOpts())
		mustStage(t, s)
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
			t.Fatal(err)
		}
		put(t, dir, fenceHooks, hooksBytes)
		put(t, dir, hooksWitness, ownerConfig)
		mustStage(t, s)
		occupied(t, s, dir, map[string]string{fenceHooks: hooksBytes, hooksWitness: ownerConfig})
	})
	t.Run("absent original", func(t *testing.T) {
		dir := legacyFixture(t)
		if err := os.Remove(filepath.Join(dir, fenceHooks)); err != nil {
			t.Fatal(err)
		}
		put(t, dir, hooksWitness, ownerConfig)
		s := openTest(t, dir, testOpts())
		mustStage(t, s)
		occupied(t, s, dir, map[string]string{hooksWitness: ownerConfig})
	})
	t.Run("enumerated path", func(t *testing.T) {
		dir := legacyFixture(t)
		s := openTest(t, dir, testOpts())
		mustStage(t, s)
		memo := "memos/" + memoHash + ".json"
		before := readRel(t, dir, memo)
		if err := s.ApplyRemovalFence(ctx, memo, "memo-witness.json"); err == nil {
			t.Fatal("removal of an enumerated path accepted")
		}
		if got := readRel(t, dir, memo); got != before {
			t.Fatalf("memo moved: %q", got)
		}
	})
	if runtime.GOOS == "windows" {
		return
	}
	t.Run("link to the witness", func(t *testing.T) {
		dir := legacyFixture(t)
		abs := filepath.Join(dir, fenceHooks)
		if err := os.Remove(abs); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(hooksWitness, abs); err != nil {
			t.Fatal(err)
		}
		s := openTest(t, dir, testOpts())
		mustStage(t, s)
		// The link dangles: an unreadable original refuses the cutover.
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, ErrRemovalUnreadable) {
			t.Fatalf("removal of a link to its witness = %v, want ErrRemovalUnreadable", err)
		}
		if target, err := os.Readlink(abs); err != nil || target != hooksWitness {
			t.Fatalf("link = %q, %v", target, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, hooksWitness)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("witness created: %v", err)
		}
	})
	t.Run("hard link to its own publication loses nothing", func(t *testing.T) {
		dir := legacyFixture(t)
		s := openTest(t, dir, testOpts())
		mustStage(t, s)
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(filepath.Join(dir, hooksWitness), filepath.Join(dir, fenceHooks)); err != nil {
			t.Fatal(err)
		}
		mustStage(t, s)
		// Not fenced as an alias: the staged bytes are published again, so
		// nothing is lost.
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
			t.Fatal(err)
		}
		if got := readRel(t, dir, hooksWitness); got != hooksBytes {
			t.Fatalf("witness = %q", got)
		}
		if _, err := s.Commit(ctx, testCommit()); err != nil {
			t.Fatal(err)
		}
	})
}

// Re-applying a replacement whose publication is in place but unlanded
// publishes it again through its own durable rename, instead of trusting a
// barrier to cover the interrupted attempt's rename. The file keeps the
// fence's bytes and sentinel.
func TestReappliedReplacementIsPublishedAgain(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	armed := false
	var events []string
	opts := crashAt("fence-renamed", &armed)
	crash := opts.Fault
	opts.Fault = func(stage string) error {
		events = append(events, stage)
		return crash(stage)
	}
	s := openTest(t, dir, opts)
	mustStage(t, s)
	armed = true
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); !errors.Is(err, errCrash) {
		t.Fatalf("fence = %v", err)
	}
	first, err := os.Stat(filepath.Join(dir, filepath.FromSlash(fencePerm)))
	if err != nil {
		t.Fatal(err)
	}
	events = nil
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	if want := []string{"before-commit", "fence-prepared", "fence-renamed", "fence-replaced", "before-commit"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("re-application events = %v, want %v", events, want)
	}
	again, err := os.Stat(filepath.Join(dir, filepath.FromSlash(fencePerm)))
	if err != nil {
		t.Fatal(err)
	}
	if !again.ModTime().Equal(first.ModTime()) || readRel(t, dir, fencePerm) != string(fencedSelector) {
		t.Fatalf("republished fence = %v %q, want the same sentinel and bytes", again.ModTime(), readRel(t, dir, fencePerm))
	}
	if runtime.GOOS != "windows" && os.SameFile(first, again) {
		t.Fatal("the re-application did not publish a new file")
	}
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
}

// replaceWith publishes content at rel as a new file (another identity),
// as an owner's editor or copy would.
func replaceWith(t *testing.T, dir, rel, content string) {
	t.Helper()
	tmp := filepath.Join(dir, filepath.FromSlash(rel)+".owner-copy")
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

// A removal is proven only by the migration's own publication at the
// witness: the original's bytes under the record's sentinel mtime. A
// byte-identical copy the owner placed there proves nothing.
func TestCopiedWitnessNeverProvesARemoval(t *testing.T) {
	ctx := context.Background()

	t.Run("retry after an interruption", func(t *testing.T) {
		dir := legacyFixture(t)
		armed := false
		s := openTest(t, dir, crashAt("fence-prepared", &armed))
		mustStage(t, s)
		armed = true
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, errCrash) {
			t.Fatalf("interrupted removal = %v", err)
		}
		if err := os.Remove(filepath.Join(dir, fenceHooks)); err != nil {
			t.Fatal(err)
		}
		replaceWith(t, dir, hooksWitness, hooksBytes)
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, ErrFenceStale) {
			t.Fatalf("retry over the owner's copy = %v, want ErrFenceStale", err)
		}
		if c := captureMap(t, s)[fenceHooks]; c.FenceState == FenceLanded {
			t.Fatalf("removal landed on the owner's copy: %+v", c)
		}
		if _, err := s.Commit(ctx, testCommit()); err == nil {
			t.Fatal("commit accepted a removal the migration never published")
		}
		if readRel(t, dir, hooksWitness) != hooksBytes {
			t.Fatal("the owner's copy was changed")
		}
	})

	t.Run("a landed removal whose witness was replaced", func(t *testing.T) {
		dir := legacyFixture(t)
		s := openTest(t, dir, testOpts())
		mustStage(t, s)
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
			t.Fatal(err)
		}
		replaceWith(t, dir, hooksWitness, hooksBytes)
		if res := mustStage(t, s); !reflect.DeepEqual(res.Unfenced, []string{fenceHooks}) {
			t.Fatalf("stage over a replaced witness = %+v, want the removal unfenced", res)
		}
		if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrFencePending) {
			t.Fatalf("commit = %v, want ErrFencePending", err)
		}
	})
}

// mustSymlink creates link, skipping the test only where Windows refuses
// symbolic links for lack of the privilege (Developer Mode off).
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) { // ERROR_PRIVILEGE_NOT_HELD
			t.Skipf("symbolic links need Developer Mode or the privilege: %v", err)
		}
		t.Fatal(err)
	}
}

// A removal interrupted after its publication but before the legacy path's
// deletion leaves the legacy file authoritative: the pass unfences it, and
// the re-applied removal recognizes the witness as its own publication,
// replaces it and completes.
func TestRemovalInterruptedBeforeItsDeletionIsReapplied(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	armed := false
	s := openTest(t, dir, crashAt("fence-renamed", &armed))
	mustStage(t, s)
	armed = true
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, errCrash) {
		t.Fatalf("interrupted removal = %v", err)
	}
	if got := readRel(t, dir, fenceHooks); got != hooksBytes {
		t.Fatalf("legacy config = %q, want it kept", got)
	}
	if res := mustStage(t, s); !reflect.DeepEqual(res.Unfenced, []string{fenceHooks}) || !reflect.DeepEqual(res.Pending, []string{fenceHooks}) {
		t.Fatalf("stage = %+v, want the removal unfenced and pending", res)
	}
	if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
		t.Fatalf("re-applied removal = %v", err)
	}
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	if got := readRel(t, dir, hooksWitness); got != hooksBytes {
		t.Fatalf("witness = %q", got)
	}
}
