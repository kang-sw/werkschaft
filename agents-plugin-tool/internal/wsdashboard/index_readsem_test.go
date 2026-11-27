package wsdashboard

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestIndexReadLaneReleasesReadSemBeforeOnChange verifies that readLaneLocked
// releases its readSem permit before invoking OnChange, so a slow OnChange
// callback (which may acquire d.inboxMu) cannot starve unrelated session reads.
func TestIndexReadLaneReleasesReadSemBeforeOnChange(t *testing.T) {
	dir := t.TempDir()
	projA, projB := t.TempDir(), t.TempDir()
	now := testNow
	register(t, dir, "alpha", projA, now)
	register(t, dir, "bravo", projB, now)

	src := newFakeSource()
	src.set("alpha", SessionData{HasBoard: true, Fingerprint: "a1"})
	src.set("bravo", SessionData{HasBoard: true, Fingerprint: "b1"})

	inOnChange := make(chan struct{})
	releaseOnChange := make(chan struct{})
	var alphaSeen atomic.Bool
	var semOccupiedInOnChange atomic.Int32

	var idx *Index
	idx = NewIndex(IndexOptions{
		Dir:    dir,
		Source: src,
		Now:    func() time.Time { return now },
		OnChange: func(id string) {
			if id == "alpha" && alphaSeen.CompareAndSwap(false, true) {
				semOccupiedInOnChange.Store(int32(len(idx.readSem)))
				close(inOnChange)
				<-releaseOnChange
			}
		},
	})
	// Narrow readSem to 1 permit so any held permit across OnChange would
	// deterministically block bravo's read.
	idx.readSem = make(chan struct{}, 1)

	alphaDone := make(chan struct{})
	go func() {
		defer close(alphaDone)
		idx.reread("alpha")
	}()

	select {
	case <-inOnChange:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for alpha OnChange")
	}
	if n := semOccupiedInOnChange.Load(); n != 0 {
		close(releaseOnChange)
		<-alphaDone
		t.Fatalf("readSem occupied count inside OnChange = %d, want 0", n)
	}

	bravoDone := make(chan struct{})
	go func() {
		defer close(bravoDone)
		idx.reread("bravo")
	}()

	select {
	case <-bravoDone:
	case <-time.After(3 * time.Second):
		close(releaseOnChange)
		<-alphaDone
		t.Fatal("bravo read was starved while alpha was inside OnChange")
	}
	close(releaseOnChange)
	<-alphaDone

	if s, ok := idx.Session("bravo"); !ok || !s.HasBoard || s.Rev != 1 {
		t.Fatalf("bravo session = %+v, ok=%v", s, ok)
	}
}
