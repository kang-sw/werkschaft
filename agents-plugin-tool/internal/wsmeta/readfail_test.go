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
	if err := s.PrepareFence(ctx, perm, fenced, false); err != nil {
		t.Fatal(err)
	}
	put(t, dir, perm, string(fenced))
	if err := s.MarkFenceLanded(ctx, perm); err != nil {
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
