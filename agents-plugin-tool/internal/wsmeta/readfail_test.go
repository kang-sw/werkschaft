package wsmeta

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// failingReads returns Options whose legacy reads of the named slash paths
// fail with err while *on is true.
func failingReads(dir string, on *bool, err error, rels ...string) Options {
	opts := testOpts()
	opts.ReadFile = func(p string) ([]byte, error) {
		if *on {
			for _, rel := range rels {
				if p == filepath.Join(dir, filepath.FromSlash(rel)) {
					return nil, &fs.PathError{Op: "open", Path: p, Err: err}
				}
			}
		}
		return os.ReadFile(p)
	}
	return opts
}

func TestTransientReadKeepsFencedOriginalAndBlocksCommit(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	const (
		perm   = "sessions/hostedlead/permission.json"
		record = "sessions/worker/agent.json"
		late   = "sessions/clionly/ack-check.json"
	)
	failing := false
	// A sharing violation that outlived the retry: not a stable error.
	s := openTest(t, dir, failingReads(dir, &failing, errors.New("sharing violation"), perm, record, late))
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	staged := captureMap(t, s)
	fenced := []byte(`{"permission_level":"!cutover-fenced"}`)
	if err := s.ApplyFence(ctx, perm, fenced); err != nil {
		t.Fatal(err)
	}
	put(t, dir, late, `{"through":3}`)

	failing = true
	res, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{late, perm, record}; !reflect.DeepEqual(res.Unread, want) || len(res.Unfenced) != 0 {
		t.Fatalf("unread = %v unfenced = %v, want %v and none", res.Unread, res.Unfenced, want)
	}
	now := captureMap(t, s)
	if c := now[perm]; !reflect.DeepEqual(c.Bytes, staged[perm].Bytes) || c.FenceState != FenceLanded || c.State != CapturePresent {
		t.Fatalf("fenced original lost to a transient read: %+v", c)
	}
	if c := now[record]; !reflect.DeepEqual(c.Bytes, staged[record].Bytes) || c.State != CapturePresent {
		t.Fatalf("unfenced capture changed by a transient read: %+v", c)
	}
	// The file that appeared since keeps its earlier absent capture.
	if c := now[late]; c.State != CaptureAbsent || c.Bytes != nil {
		t.Fatalf("an unread new file was captured: %+v", c)
	}
	// A commit refuses and changes nothing while any input is unread.
	if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrLegacyUnread) || !strings.Contains(err.Error(), record) {
		t.Fatalf("commit with unread inputs = %v", err)
	}
	if done, err := s.ImportComplete(ctx); err != nil || done {
		t.Fatalf("refused commit completed the import: %v %v", done, err)
	}
	if c := captureMap(t, s)[perm]; c.FenceState != FenceLanded || !reflect.DeepEqual(c.Bytes, staged[perm].Bytes) {
		t.Fatalf("refused commit changed the journal: %+v", c)
	}

	// Once the reads succeed the retry imports the original selection.
	failing = false
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["hostedlead"]; p.State != "valid" || p.Level != "workdir-write" {
		t.Fatalf("permission = %+v, want the staged original", p)
	}
	if _, ok := snap.Records["worker"]; !ok {
		t.Fatal("worker record not imported")
	}
	if m := decode(t, snap.AckChecks["clionly"]); m["through"].(float64) != 3 {
		t.Fatalf("late ack = %s", snap.AckChecks["clionly"])
	}
}

func TestStableReadErrorIsUnreadableAndFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows treats every read error as transient")
	}
	ctx := context.Background()
	dir := legacyFixture(t)
	const perm = "sessions/hostedlead/permission.json"
	failing := true
	s := openTest(t, dir, failingReads(dir, &failing, fs.ErrPermission, perm))
	res, err := s.Commit(ctx, testCommit())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stage.Unread) != 0 {
		t.Fatalf("a stable error was treated as transient: %v", res.Stage.Unread)
	}
	if c := captureMap(t, s)[perm]; c.State != CaptureUnreadable || c.ImportStatus != ImportInvalid {
		t.Fatalf("capture = %+v", c)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["hostedlead"]; p.State != "invalid" {
		t.Fatalf("permission = %+v, want a fail-closed invalid row", p)
	}
}

// A bundled path linking to a FIFO is an unreadable input; the pass never
// blocks opening it.
func TestLinkToFIFOIsUnreadableAndNeverBlocks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs in the Windows file namespace")
	}
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "dashboard")
	put(t, dir, "sessions/a/label.json", `{"label":"A"}`)
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := mkfifo(fifo); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(fifo, filepath.Join(dir, "sessions", "a", "permission.json")); err != nil {
		t.Fatal(err)
	}
	if err := mkfifo(filepath.Join(dir, "support.json")); err != nil {
		t.Fatal(err)
	}
	s := openTest(t, dir, Options{Now: testOpts().Now, CheckpointIdle: -1})
	done := make(chan error, 1)
	go func() { _, err := s.Commit(ctx, testCommit()); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the import blocked on a FIFO")
	}
	caps := captureMap(t, s)
	for _, p := range []string{"sessions/a/permission.json", "support.json"} {
		if c := caps[p]; c.State != CaptureUnreadable {
			t.Fatalf("%s: %+v", p, c)
		}
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["a"]; p.State != "invalid" {
		t.Fatalf("permission = %+v, want fail-closed", p)
	}
}

var errACLDenied = errors.New("access denied (test)")

// A selector its ACL denies for good fails closed for its own session once
// the denial persisted across passes; it no longer blocks every session's
// import. A sharing violation never confirms.
func TestPersistentAccessDenialBecomesAnInvalidSelection(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	prev := classifyReadError
	classifyReadError = func(err error) readClass {
		if errors.Is(err, errACLDenied) {
			return classConfirmable
		}
		return classTransient
	}
	t.Cleanup(func() { classifyReadError = prev })
	const shared = "sessions/worker/agent.json"
	now := testNow
	sharing := true
	opts := testOpts()
	opts.Now = func() time.Time { return now }
	opts.ReadFile = func(p string) ([]byte, error) {
		switch {
		case p == filepath.Join(dir, filepath.FromSlash(fencePerm)):
			return nil, &fs.PathError{Op: "open", Path: p, Err: errACLDenied}
		case sharing && p == filepath.Join(dir, filepath.FromSlash(shared)):
			return nil, &fs.PathError{Op: "open", Path: p, Err: errors.New("sharing violation")}
		}
		return os.ReadFile(p)
	}
	s := openTest(t, dir, opts)
	// Passes in quick succession do not confirm, however many there are.
	for i := 0; i < UnreadConfirmPasses+2; i++ {
		res, err := s.Stage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{fencePerm, shared}; !reflect.DeepEqual(res.Unread, want) {
			t.Fatalf("pass %d unread = %v, want %v", i, res.Unread, want)
		}
	}
	now = now.Add(UnreadConfirmAfter)
	res, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{shared}; !reflect.DeepEqual(res.Unread, want) {
		t.Fatalf("unread after the denial persisted = %v, want only the sharing violation", res.Unread)
	}
	if c := captureMap(t, s)[fencePerm]; c.State != CaptureUnreadable {
		t.Fatalf("denied selector = %+v", c)
	}
	// The sharing violation clears; the import proceeds with the denied
	// selector as a fail-closed invalid row for that session alone.
	sharing = false
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["hostedlead"]; p.State != "invalid" {
		t.Fatalf("denied selector imported as %+v, want invalid", p)
	}
	if _, ok := snap.Records["worker"]; !ok {
		t.Fatal("the other session's record was not imported")
	}
}

// A denial that clears before it is confirmed starts its count over.
func TestClearedDenialResetsItsCount(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	prev := classifyReadError
	classifyReadError = func(error) readClass { return classConfirmable }
	t.Cleanup(func() { classifyReadError = prev })
	now := testNow
	failing := true
	opts := testOpts()
	opts.Now = func() time.Time { return now }
	opts.ReadFile = func(p string) ([]byte, error) {
		if failing && p == filepath.Join(dir, filepath.FromSlash(fencePerm)) {
			return nil, &fs.PathError{Op: "open", Path: p, Err: errACLDenied}
		}
		return os.ReadFile(p)
	}
	s := openTest(t, dir, opts)
	for i := 0; i < UnreadConfirmPasses; i++ {
		if _, err := s.Stage(ctx); err != nil {
			t.Fatal(err)
		}
	}
	failing = false
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	failing = true
	now = now.Add(UnreadConfirmAfter)
	res, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Unread, []string{fencePerm}) {
		t.Fatalf("unread = %v: a cleared denial was confirmed", res.Unread)
	}
	if c := captureMap(t, s)[fencePerm]; c.State != CapturePresent {
		t.Fatalf("selector = %+v, want the earlier readable capture", c)
	}
}

// A confirmable denial of a file that an earlier pass read is never
// confirmed: a Windows pending delete can outlast the confirmation window,
// so the staged original stands and the import waits.
func TestDenialAfterAReadablePassKeepsTheOriginal(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	prev := classifyReadError
	classifyReadError = func(error) readClass { return classConfirmable }
	t.Cleanup(func() { classifyReadError = prev })
	const record = "sessions/worker/agent.json"
	now := testNow
	denied := false
	opts := testOpts()
	opts.Now = func() time.Time { return now }
	opts.ReadFile = func(p string) ([]byte, error) {
		if denied && p == filepath.Join(dir, filepath.FromSlash(record)) {
			return nil, &fs.PathError{Op: "open", Path: p, Err: errACLDenied}
		}
		return os.ReadFile(p)
	}
	s := openTest(t, dir, opts)
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	staged := captureMap(t, s)[record]
	denied = true
	for i := 0; i < UnreadConfirmPasses+2; i++ {
		now = now.Add(UnreadConfirmAfter)
		res, err := s.Stage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(res.Unread, []string{record}) {
			t.Fatalf("pass %d unread = %v", i, res.Unread)
		}
	}
	if c := captureMap(t, s)[record]; c.State != CapturePresent || !reflect.DeepEqual(c.Bytes, staged.Bytes) {
		t.Fatalf("record = %+v, want the staged original", c)
	}
	if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrLegacyUnread) {
		t.Fatalf("commit = %v", err)
	}
}

// A selector its ACL denies, once a pass confirmed the denial, still takes
// the permission fence every imported session needs, also after an
// interrupted attempt. The fence replaces the denied file (its directory
// permits that) and the import keeps the fail-closed unreadable original.
func TestConfirmedDenialTakesItsFence(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	prev := classifyReadError
	classifyReadError = func(err error) readClass {
		if errors.Is(err, errACLDenied) {
			return classConfirmable
		}
		return classTransient
	}
	t.Cleanup(func() { classifyReadError = prev })
	now := testNow
	armed := false
	opts := crashAt("fence-prepared", &armed)
	opts.Now = func() time.Time { return now }
	// The denial covers the legacy file, not the migration's publication.
	opts.ReadFile = func(p string) ([]byte, error) {
		raw, err := os.ReadFile(p)
		if err == nil && p == filepath.Join(dir, filepath.FromSlash(fencePerm)) && string(raw) == permOriginal {
			return nil, &fs.PathError{Op: "open", Path: p, Err: errACLDenied}
		}
		return raw, err
	}
	s := openTest(t, dir, opts)
	for i := 0; i < UnreadConfirmPasses; i++ {
		mustStage(t, s)
		now = now.Add(UnreadConfirmAfter)
	}
	if c := captureMap(t, s)[fencePerm]; c.State != CaptureUnreadable {
		t.Fatalf("denied selector = %+v", c)
	}
	armed = true
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); !errors.Is(err, errCrash) {
		t.Fatalf("interrupted fence = %v", err)
	}
	if res := mustStage(t, s); len(res.Unread) != 0 || !reflect.DeepEqual(res.Pending, []string{fencePerm}) {
		t.Fatalf("stage after the interrupted fence = %+v", res)
	}
	if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
		t.Fatal(err)
	}
	if got := readRel(t, dir, fencePerm); got != string(fencedSelector) {
		t.Fatalf("live selector = %q", got)
	}
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["hostedlead"]; p.State != "invalid" {
		t.Fatalf("denied selector imported as %+v, want invalid", p)
	}
}

// A confirmed read denial moves with the file: a removal over a confirmed
// unreadable original is proven by the witness's identity while the same
// denial persists there, and a copy at the witness proves nothing.
func TestConfirmedDenialMovesWithTheWitness(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (string, *Store) {
		dir := legacyFixture(t)
		prev := classifyReadError
		classifyReadError = func(err error) readClass {
			if errors.Is(err, errACLDenied) {
				return classConfirmable
			}
			return classTransient
		}
		t.Cleanup(func() { classifyReadError = prev })
		now := testNow
		opts := testOpts()
		opts.Now = func() time.Time { return now }
		// The ACL travels with the hooks config's bytes, wherever it is.
		opts.ReadFile = func(p string) ([]byte, error) {
			raw, err := os.ReadFile(p)
			if err == nil && string(raw) == hooksBytes {
				return nil, &fs.PathError{Op: "open", Path: p, Err: errACLDenied}
			}
			return raw, err
		}
		s := openTest(t, dir, opts)
		for i := 0; i < UnreadConfirmPasses; i++ {
			mustStage(t, s)
			now = now.Add(UnreadConfirmAfter)
		}
		if c := captureMap(t, s)[fenceHooks]; c.State != CaptureUnreadable {
			t.Fatalf("denied hooks config = %+v", c)
		}
		return dir, s
	}

	t.Run("proven by identity", func(t *testing.T) {
		_, s := setup(t)
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
			t.Fatal(err)
		}
		if res := mustStage(t, s); len(res.Unread) != 0 || len(res.Pending) != 0 || len(res.Unfenced) != 0 {
			t.Fatalf("stage after the removal = %+v, want it proven", res)
		}
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
			t.Fatalf("re-applied removal = %v", err)
		}
		if _, err := s.Commit(ctx, testCommit()); err != nil {
			t.Fatal(err)
		}
		if c := captureMap(t, s)[fenceHooks]; c.State != CaptureUnreadable {
			t.Fatalf("hooks capture = %+v, want the confirmed denial kept", c)
		}
	})

	// The owner writes a new, readable config before the commit: the
	// re-fence retires the denied witness it moved there, and nothing else.
	t.Run("a re-fence retires the denied witness", func(t *testing.T) {
		dir, s := setup(t)
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
			t.Fatal(err)
		}
		const newHooks = `{"hooks":["new"]}`
		put(t, dir, fenceHooks, newHooks)
		mustStage(t, s)
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
			t.Fatalf("re-fence over the denied witness = %v", err)
		}
		if got := readRel(t, dir, hooksWitness); got != newHooks {
			t.Fatalf("witness = %q, want the new config", got)
		}
		if _, err := s.Commit(ctx, testCommit()); err != nil {
			t.Fatal(err)
		}
		if c := captureMap(t, s)[fenceHooks]; c.State != CapturePresent || string(c.Bytes) != newHooks {
			t.Fatalf("hooks capture = %+v, want the new config", c)
		}
	})

	t.Run("a re-fence never retires a denied copy", func(t *testing.T) {
		dir, s := setup(t)
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
			t.Fatal(err)
		}
		replaceWith(t, dir, hooksWitness, hooksBytes)
		put(t, dir, fenceHooks, `{"hooks":["new"]}`)
		mustStage(t, s)
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, ErrWitnessOccupied) {
			t.Fatalf("re-fence over a denied copy = %v, want ErrWitnessOccupied", err)
		}
		if raw, err := os.ReadFile(filepath.Join(dir, hooksWitness)); err != nil || string(raw) != hooksBytes {
			t.Fatalf("the owner's copy changed: %q, %v", raw, err)
		}
	})

	t.Run("a copy at the witness proves nothing", func(t *testing.T) {
		dir, s := setup(t)
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
			t.Fatal(err)
		}
		replaceWith(t, dir, hooksWitness, hooksBytes)
		if res := mustStage(t, s); !reflect.DeepEqual(res.Unfenced, []string{fenceHooks}) {
			t.Fatalf("stage over a copied witness = %+v, want the removal unfenced", res)
		}
	})
}
