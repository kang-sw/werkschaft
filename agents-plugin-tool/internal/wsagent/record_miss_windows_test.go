//go:build windows

package wsagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A record deleted with its session reads not-found promptly.
func TestReadRecordFileDeletedSessionFailsFast(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone")
	start := time.Now()
	_, err := readRecordFile(dir)
	if !os.IsNotExist(err) {
		t.Fatalf("err = %v, want not-exist", err)
	}
	if d := time.Since(start); d > recordMissBudget/2 {
		t.Fatalf("took %v for a missing session dir", d)
	}
}

// Reads racing rename-overs never see a not-found.
func TestReadRecordFileSurvivesRenameOver(t *testing.T) {
	dir := t.TempDir()
	path := recordPath(dir)
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tmp := filepath.Join(dir, "tmp")
		for {
			select {
			case <-stop:
				return
			default:
			}
			if os.WriteFile(tmp, []byte("x"), 0o600) == nil {
				_ = replaceFile(tmp, path)
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for i := 0; i < 500; i++ {
		if _, err := readRecordFile(dir); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
}
