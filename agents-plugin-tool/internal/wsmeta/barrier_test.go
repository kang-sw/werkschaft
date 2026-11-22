package wsmeta

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fileSystems sets the barrier's file-system query for the test: names maps
// a dashboard-relative directory to its file system ("." is the dashboard
// directory), and every other directory reports def.
func fileSystems(t *testing.T, dir, def string, names map[string]string) {
	t.Helper()
	prev := barrierFileSystem
	t.Cleanup(func() { barrierFileSystem = prev })
	barrierFileSystem = func(p string) (string, error) {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return "", err
		}
		if name, ok := names[filepath.ToSlash(rel)]; ok {
			return name, nil
		}
		return def, nil
	}
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logSink) has(parts ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(line, p)
		}
		if all {
			return true
		}
	}
	return false
}

// checkRefusedCutover asserts a refused cutover changed nothing: the legacy
// tree is as before, no fence is recorded and the import is not complete.
func checkRefusedCutover(t *testing.T, s *Store, dir string, before map[string]fileState) {
	t.Helper()
	ctx := context.Background()
	if after := treeState(t, dir); !reflect.DeepEqual(after, before) {
		t.Fatalf("a refused cutover changed the legacy tree:\nbefore %v\nafter  %v", before, after)
	}
	for p, c := range captureMap(t, s) {
		if c.FenceState != FenceNone || c.FenceRequired || c.FenceWitness != "" {
			t.Fatalf("%s carries a fence after a refused cutover: %+v", p, c)
		}
	}
	if done, err := s.ImportComplete(ctx); err != nil || done {
		t.Fatalf("import complete = %v, %v; want not complete", done, err)
	}
	if _, err := s.Load(ctx); !errors.Is(err, ErrNotImported) {
		t.Fatalf("Load = %v, want ErrNotImported (the legacy store stays authoritative)", err)
	}
}

func TestUnsupportedFileSystemRefusesTheCutover(t *testing.T) {
	ctx := context.Background()

	t.Run("refused before staging, store still opens", func(t *testing.T) {
		dir := legacyFixture(t)
		fileSystems(t, dir, "FAT32", nil)
		var log logSink
		opts := testOpts()
		opts.Logf = log.logf
		s := openTest(t, dir, opts)
		before := treeState(t, dir)
		if _, err := s.Stage(ctx); !errors.Is(err, ErrCutoverUnsupported) {
			t.Fatalf("Stage = %v, want ErrCutoverUnsupported", err)
		}
		if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrCutoverUnsupported) {
			t.Fatalf("Commit = %v, want ErrCutoverUnsupported", err)
		}
		if got := captureMap(t, s); len(got) != 0 {
			t.Fatalf("a refused Stage journaled %d captures", len(got))
		}
		checkRefusedCutover(t, s, dir, before)
		if !log.has("refused", "legacy store stays authoritative", "FAT32") {
			t.Fatalf("no logged reason for the refusal: %q", log.lines)
		}
	})

	for _, name := range []string{"ReFS", "FAT32", "exFAT"} {
		t.Run("refused on "+name, func(t *testing.T) {
			dir := legacyFixture(t)
			fileSystems(t, dir, name, nil)
			s := openTest(t, dir, testOpts())
			before := treeState(t, dir)
			if _, err := s.Stage(ctx); !errors.Is(err, ErrCutoverUnsupported) {
				t.Fatalf("Stage on %s = %v, want ErrCutoverUnsupported", name, err)
			}
			checkRefusedCutover(t, s, dir, before)
		})
	}

	t.Run("a session directory on another volume", func(t *testing.T) {
		dir := legacyFixture(t)
		fileSystems(t, dir, "NTFS", map[string]string{"sessions/hostedlead": "exFAT"})
		s := openTest(t, dir, testOpts())
		before := treeState(t, dir)
		if _, err := s.Stage(ctx); !errors.Is(err, ErrCutoverUnsupported) || !strings.Contains(err.Error(), "exFAT") {
			t.Fatalf("Stage = %v, want ErrCutoverUnsupported naming exFAT", err)
		}
		checkRefusedCutover(t, s, dir, before)
	})

	t.Run("unknown file system", func(t *testing.T) {
		dir := legacyFixture(t)
		prev := barrierFileSystem
		t.Cleanup(func() { barrierFileSystem = prev })
		barrierFileSystem = func(string) (string, error) { return "", errors.New("access denied") }
		s := openTest(t, dir, testOpts())
		before := treeState(t, dir)
		if _, err := s.Stage(ctx); !errors.Is(err, ErrCutoverUnsupported) {
			t.Fatalf("Stage = %v, want ErrCutoverUnsupported", err)
		}
		checkRefusedCutover(t, s, dir, before)
	})

	t.Run("fences refused before their record", func(t *testing.T) {
		dir := legacyFixture(t)
		fileSystems(t, dir, "NTFS", nil)
		s := openTest(t, dir, testOpts())
		mustStage(t, s)
		before := treeState(t, dir)
		fileSystems(t, dir, "FAT32", nil)
		if err := s.ApplyFence(ctx, fencePerm, fencedSelector); !errors.Is(err, ErrCutoverUnsupported) {
			t.Fatalf("ApplyFence = %v, want ErrCutoverUnsupported", err)
		}
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, ErrCutoverUnsupported) {
			t.Fatalf("ApplyRemovalFence = %v, want ErrCutoverUnsupported", err)
		}
		if _, err := s.Commit(ctx, testCommit()); !errors.Is(err, ErrCutoverUnsupported) {
			t.Fatalf("Commit = %v, want ErrCutoverUnsupported", err)
		}
		checkRefusedCutover(t, s, dir, before)
	})

}

// wantInterrupted asserts the refusal reported once a fence may have changed
// the legacy tree: never the legacy-fallback result.
func wantInterrupted(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, ErrCutoverInterrupted) || errors.Is(err, ErrCutoverUnsupported) {
		t.Fatalf("%s = %v, want ErrCutoverInterrupted and never ErrCutoverUnsupported", what, err)
	}
}

// ErrCutoverUnsupported tells the caller to keep serving from the legacy
// store, so it is returned only while no fence was ever recorded. Once one
// may have changed the tree, in this process or an earlier one, a refusal is
// ErrCutoverInterrupted.
func TestRefusalAfterAFenceIsNeverTheFallback(t *testing.T) {
	ctx := context.Background()
	fat := func(string) (string, error) { return "FAT32", nil }

	for _, tc := range []struct {
		name  string
		apply func(s *Store) error
	}{
		{"replacement", func(s *Store) error { return s.ApplyFence(ctx, fencePerm, fencedSelector) }},
		{"removal", func(s *Store) error { return s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness) }},
	} {
		t.Run(tc.name+" refused at its barrier after the rename", func(t *testing.T) {
			dir := legacyFixture(t)
			fileSystems(t, dir, "NTFS", nil)
			var log logSink
			opts := testOpts()
			opts.Logf = log.logf
			opts.Fault = func(stage string) error {
				if stage == "fence-renamed" {
					// The volume answers differently once the rename is made.
					barrierFileSystem = fat
				}
				return nil
			}
			s := openTest(t, dir, opts)
			mustStage(t, s)
			wantInterrupted(t, "fence", tc.apply(s))
			for p, c := range captureMap(t, s) {
				if c.FenceState == FenceLanded {
					t.Fatalf("%s landed without its barrier: %+v", p, c)
				}
			}
			if !log.has("interrupted", "FAT32") {
				t.Fatalf("no logged reason: %q", log.lines)
			}
		})
	}

	t.Run("every later call, across a restart", func(t *testing.T) {
		dir := legacyFixture(t)
		fileSystems(t, dir, "NTFS", nil)
		s, err := Open(dir, testOpts())
		if err != nil {
			t.Fatal(err)
		}
		mustStage(t, s)
		if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		barrierFileSystem = fat
		s = openTest(t, dir, testOpts())
		_, err = s.Stage(ctx)
		wantInterrupted(t, "Stage", err)
		_, err = s.Commit(ctx, testCommit())
		wantInterrupted(t, "Commit", err)
		wantInterrupted(t, "ApplyFence", s.ApplyFence(ctx, fencePerm, fencedSelector))
		wantInterrupted(t, "ApplyRemovalFence", s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness))
	})

	t.Run("after a pass discarded the fenced rows", func(t *testing.T) {
		dir := legacyFixture(t)
		fileSystems(t, dir, "NTFS", nil)
		s := openTest(t, dir, testOpts())
		mustStage(t, s)
		if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
			t.Fatal(err)
		}
		// The fenced session is recreated, so a pass drops its rows.
		if err := os.RemoveAll(filepath.Join(dir, "sessions", "hostedlead")); err != nil {
			t.Fatal(err)
		}
		put(t, dir, fencePerm, permOriginal)
		if res := mustStage(t, s); res.Discarded != 1 {
			t.Fatalf("stage = %+v, want the session discarded", res)
		}
		barrierFileSystem = fat
		_, err := s.Stage(ctx)
		wantInterrupted(t, "Stage", err)
	})

	t.Run("a record without its publication", func(t *testing.T) {
		dir := legacyFixture(t)
		fileSystems(t, dir, "NTFS", nil)
		armed := false
		s := openTest(t, dir, crashAt("fence-prepared", &armed))
		mustStage(t, s)
		armed = true
		if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); !errors.Is(err, errCrash) {
			t.Fatalf("interrupted removal = %v", err)
		}
		barrierFileSystem = fat
		_, err := s.Stage(ctx)
		wantInterrupted(t, "Stage", err)
	})
}

func TestSupportedFileSystemsCompleteTheCutover(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"", "NTFS", "ntfs"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			dir := legacyFixture(t)
			fileSystems(t, dir, name, nil)
			s := openTest(t, dir, testOpts())
			mustStage(t, s)
			if err := s.ApplyFence(ctx, fencePerm, fencedSelector); err != nil {
				t.Fatal(err)
			}
			if err := s.ApplyRemovalFence(ctx, fenceHooks, hooksWitness); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Commit(ctx, testCommit()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
