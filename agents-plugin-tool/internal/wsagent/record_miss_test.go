package wsagent

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

func TestIsRecordMiss(t *testing.T) {
	yes := func() bool { return true }
	no := func() bool { return false }
	boom := func() bool { t.Fatal("dirExists must not run"); return false }
	if !isRecordMiss(fs.ErrNotExist, true, yes) {
		t.Error("not-found with live dir inside budget must retry")
	}
	if isRecordMiss(fs.ErrNotExist, true, no) {
		t.Error("not-found with deleted dir must not retry")
	}
	if isRecordMiss(fs.ErrNotExist, false, boom) {
		t.Error("not-found past budget must not retry")
	}
	if isRecordMiss(errors.New("other"), true, boom) {
		t.Error("non-not-found error must not retry")
	}
}

func neverTransient(error) bool { return false }

// A read that misses once while the session dir exists succeeds once the
// record is published inside the budget.
func TestReadRecordRetryingMissThenPublish(t *testing.T) {
	dir := t.TempDir()
	missed := make(chan struct{})
	published := make(chan struct{})
	first := true
	after := func() {
		if first {
			first = false
			close(missed) // first attempt is known to have missed
			<-published
		}
	}
	go func() {
		<-missed
		_ = os.WriteFile(recordPath(dir), []byte("ok"), 0o600)
		close(published)
	}()
	raw, err := readRecordRetrying(dir, wsstate.ReadFileOnce, neverTransient, after)
	if err != nil || string(raw) != "ok" {
		t.Fatalf("raw=%q err=%v, want ok", raw, err)
	}
	if first {
		t.Fatal("first attempt never missed")
	}
}

// A record that is never written reads not-found within the miss budget.
func TestReadRecordRetryingPersistentMissBounded(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	_, err := readRecordRetrying(dir, wsstate.ReadFileOnce, neverTransient, nil)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want not-exist", err)
	}
	if d := time.Since(start); d > recordMissBudget+time.Second {
		t.Fatalf("took %v, budget %v", d, recordMissBudget)
	}
}
