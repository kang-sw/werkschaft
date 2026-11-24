//go:build windows

package wsstate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRenameFileAtRefusesUnsupportedWindowsPOSIXRename(t *testing.T) {
	dir := t.TempDir()
	before, after := filepath.Join(dir, "current"), filepath.Join(dir, "staged")
	for path, value := range map[string]string{before: "committed", after: "staged"} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	tmp, err := os.Open(after)
	if err != nil {
		t.Fatal(err)
	}
	defer tmp.Close()
	calls := 0
	unsupported := func(windows.Handle, *windows.IO_STATUS_BLOCK, *byte, uint32, uint32) error {
		calls++
		return windows.STATUS_INVALID_INFO_CLASS
	}
	if err := renameFileAt(parent, tmp, "current", true, unsupported); !errors.Is(err, errors.ErrUnsupported) || calls != 1 {
		t.Fatalf("rename error = %v after %d calls", err, calls)
	}
	if data, err := os.ReadFile(before); err != nil || string(data) != "committed" {
		t.Fatalf("committed entry = %q, %v", data, err)
	}
}

// TestAtomicReplaceFileWindowsReplacesExisting verifies that AtomicReplaceFile
// succeeds when the destination already exists and the new contents are visible
// after the call.
func TestAtomicReplaceFileWindowsReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "state.json")
	tmp := filepath.Join(dir, "state.json.tmp")

	if err := os.WriteFile(dst, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("WriteFile dst: %v", err)
	}
	if err := os.WriteFile(tmp, []byte("new\n"), 0o644); err != nil {
		t.Fatalf("WriteFile tmp: %v", err)
	}

	if err := AtomicReplaceFile(tmp, dst); err != nil {
		t.Fatalf("AtomicReplaceFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "new\n" {
		t.Errorf("destination = %q, want %q", string(got), "new\n")
	}
	if _, err := os.Stat(tmp); err == nil {
		t.Errorf("temp file %q still exists after replace", tmp)
	}
}

// TestAtomicReplaceFileWindowsWaitsOutAReader: a reader holding the
// destination open (a plain Go open, no FILE_SHARE_DELETE) makes MoveFileEx
// fail with ERROR_ACCESS_DENIED; the replace retries until the reader closes.
func TestAtomicReplaceFileWindowsWaitsOutAReader(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "state.json")
	tmp := filepath.Join(dir, "state.json.tmp")
	if err := os.WriteFile(dst, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		reader.Close()
		close(released)
	}()
	if err := AtomicReplaceFile(tmp, dst); err != nil {
		t.Fatalf("AtomicReplaceFile with a brief reader: %v", err)
	}
	<-released
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new\n" {
		t.Errorf("destination = %q, want %q", got, "new\n")
	}
}

func TestReadFileCachedWindowsWaitsOutExclusiveOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	const content = "persisted\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	hold := os.NewFile(uintptr(handle), path)
	defer hold.Close()

	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	before := readCacheReads()
	go func() {
		data, err := ReadFileCached(path)
		done <- result{data, err}
	}()

	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	// Keep the handle open until a second read proves the first was blocked.
	for readCacheReads()-before < 2 {
		select {
		case got := <-done:
			t.Fatalf("cached read returned before retrying: %v", got.err)
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("cached read did not retry")
		}
	}
	if err := hold.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || string(got.data) != content {
			t.Fatalf("cached read = %q, %v, want %q", got.data, got.err, content)
		}
	case <-deadline.C:
		t.Fatal("cached read did not finish after the handle closed")
	}
}

// TestRemoveFileWindowsWaitsOutAReader: a plain Go reader holding the file
// fails os.Remove on Windows; RemoveFile retries until the reader closes.
func TestRemoveFileWindowsWaitsOutAReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		reader.Close()
	}()
	if err := RemoveFile(path); err != nil {
		t.Fatalf("RemoveFile with a brief reader: %v", err)
	}
	if _, err := ReadFile(path); !os.IsNotExist(err) {
		t.Fatalf("ReadFile after removal = %v, want not-exist", err)
	}
}
