package mcp

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// TestTouchConcurrentExpiredEntryStatsOnce pins that concurrent unlocked
// callers hitting one expired entry run a single stat + Chtimes: the first
// claims the window under touchMu, the rest see it warm.
func TestTouchConcurrentExpiredEntryStatsOnce(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	now := time.Now().Truncate(time.Second)
	s := newSessionStore()
	s.now = func() time.Time { return now }
	key, err := s.mint("/work", roleLead, "")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.dirs()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := d.partPath(key, partKey)
	old := now.Add(-3 * touchGuardWindow)
	if err := os.Chtimes(keyPath, old, old); err != nil {
		t.Fatal(err)
	}
	const callers = 16
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(callers)
	done.Add(callers)
	before := wsstate.FileIOCounters()
	for range callers {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			s.touch(d, key)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	after := wsstate.FileIOCounters()
	if got := siteDelta(before, after, "mcp/session_layout.go:"); got != 1 {
		t.Fatalf("%d concurrent touches of one expired entry ran %d stats, want 1", callers, got)
	}
	if got := mtimeOf(t, keyPath); !got.Equal(now) {
		t.Fatalf("activity mark mtime = %v, want %v", got, now)
	}
	// A failed touch (no activity mark) must not leave a claim that
	// suppresses the retry.
	s.clearLastTouched(key)
	missing := "missing-key-00"
	s.touch(d, missing)
	s.touchMu.Lock()
	_, claimed := s.lastTouched[missing]
	s.touchMu.Unlock()
	if claimed {
		t.Fatal("a touch that found no activity mark left its claim behind")
	}
}

func TestResolveSessionLookupRetainsSecondReadAndTouch(t *testing.T) {
	root, s := hostedLoginEnv(t)
	t.Setenv(envHostedSession, "")
	key, err := s.sessions.mint(root, roleLead, "")
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := s.sessions.dirs()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dirs.keyDir(key), "key.json")
	old := time.Now().Add(-2 * touchGuardWindow)
	reads := 0
	s.sessions.readFile = func(path string, newValue func() any) ([]byte, any, error) {
		reads++
		if reads == 2 {
			// Another writer may replace/backdate the activity mark after lookup's
			// first touch. The serialized read must still perform its own touch.
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
		return readRecordFile(path, newValue)
	}
	got, err := s.resolveSession(sessionAccess{mode: sessionAccessLookup, key: key})
	if err != nil || !got.found || reads != 2 {
		t.Fatalf("lookup = %+v, %v; reads %d, want 2", got, err, reads)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().After(old) {
		t.Fatalf("second read left activity mark at %v", info.ModTime())
	}
}
