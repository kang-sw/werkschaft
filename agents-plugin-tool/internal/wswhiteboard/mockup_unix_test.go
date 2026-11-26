//go:build !windows

package wswhiteboard

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestMockupRefusesNonRegularFIFO(t *testing.T) {
	root := t.TempDir()
	dir := writeMockupFixture(t, root, "mock/fifo", map[string][]byte{
		"index.html": []byte("<!DOCTYPE html><html></html>"),
	})
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.txt"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if _, err := CollectMockupBundle(root, "mock/fifo/index.html"); err == nil {
		t.Fatal("expected FIFO to be refused")
	}
}
