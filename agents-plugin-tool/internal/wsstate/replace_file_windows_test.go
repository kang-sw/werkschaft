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

func TestReadFileCachedWindowsFailsFastDuringExclusiveOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	const content = "persisted\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	// Hold an exclusive read/write handle directly (matching a publication hold
	// and avoiding os.NewFile wrapper/double-close) so concurrent opens must
	// observe ERROR_SHARING_VIOLATION.
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	closeHold := func() error {
		if closed {
			return nil
		}
		closed = true
		return windows.CloseHandle(handle)
	}
	defer func() { _ = closeHold() }()

	before := readCacheReads()
	if _, err := ReadFileCached(path); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("cached read under exclusive open = %v, want sharing violation", err)
	}
	if attempts := readCacheReads() - before; attempts != 1 {
		t.Fatalf("cached read made %d attempts, want 1", attempts)
	}

	// An uncached background read still waits out the publication hold.

	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	before = FileIOCounters()["file_io_read_file"]
	go func() {
		data, err := ReadFile(path)
		done <- result{data, err}
	}()

	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	// Keep the handle open until a second read proves the first was blocked.
	for FileIOCounters()["file_io_read_file"]-before < 2 {
		select {
		case got := <-done:
			t.Fatalf("uncached read returned before retrying: %v", got.err)
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("uncached read did not retry")
		}
	}
	if err := closeHold(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || string(got.data) != content {
			t.Fatalf("uncached read = %q, %v, want %q", got.data, got.err, content)
		}
	case <-deadline.C:
		t.Fatal("uncached read did not finish after the handle closed")
	}
}

func TestRetryTransientWindows(t *testing.T) {
	t.Run("retries sharing violation and access denied until clear", func(t *testing.T) {
		errs := []error{
			&os.PathError{Op: "open", Path: "state", Err: windows.ERROR_SHARING_VIOLATION},
			&os.PathError{Op: "open", Path: "state", Err: windows.ERROR_ACCESS_DENIED},
			nil,
		}
		attempts := 0
		err := retryTransient(func() error {
			e := errs[attempts]
			attempts++
			return e
		})
		if err != nil {
			t.Fatalf("retryTransient = %v, want nil", err)
		}
		if attempts != 3 {
			t.Fatalf("attempts = %d, want 3", attempts)
		}
	})

	t.Run("does not retry non-transient errors", func(t *testing.T) {
		attempts := 0
		want := &os.PathError{Op: "open", Path: "state", Err: windows.ERROR_FILE_NOT_FOUND}
		err := retryTransient(func() error {
			attempts++
			return want
		})
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			t.Fatalf("retryTransient = %v, want ERROR_FILE_NOT_FOUND", err)
		}
		if attempts != 1 {
			t.Fatalf("attempts = %d, want 1", attempts)
		}
	})
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
