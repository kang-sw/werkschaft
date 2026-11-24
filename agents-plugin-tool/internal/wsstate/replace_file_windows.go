//go:build windows

package wsstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// AtomicReplaceFile replaces path with tmp using MoveFileEx(REPLACE_EXISTING),
// which is atomic on NTFS. The replace fails while another handle holds path
// without FILE_SHARE_DELETE, which every plain Go open (os.ReadFile) is: the
// dashboard or another ws-mcp process reading the file, or an AV scanner
// inspecting the previous write. MoveFileEx reports that hold as
// ERROR_ACCESS_DENIED (an open target) or ERROR_SHARING_VIOLATION. The holds
// are brief, so both are retried with back-off for about two seconds before
// giving up; any other error is returned at once.
func AtomicReplaceFile(tmp, path string) error {
	from, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return retryTransient(func() error {
		CountFileIO("atomic_replace")
		return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	})
}

// RenameFileAt publishes an open source file at name in the pinned parent.
// The source handle must grant DELETE and the destination may be held
// open by handles that share delete.
func RenameFileAt(parent, tmp *os.File, name string, replace bool) error {
	return renameFileAt(parent, tmp, name, replace, windows.NtSetInformationFile)
}

// RetryRenameFileAt replaces name while retrying brief sharing violations.
func RetryRenameFileAt(parent, tmp *os.File, name string) error {
	return retryTransient(func() error { return RenameFileAt(parent, tmp, name, true) })
}

func renameFileAt(parent, tmp *os.File, name string, replace bool, setInfo func(windows.Handle, *windows.IO_STATUS_BLOCK, *byte, uint32, uint32) error) error {
	// The target name is relative to the pinned directory handle. POSIX
	// replacement allows existing handles that share delete to remain open.
	type renameInfo struct {
		flags         uint32
		rootDirectory windows.Handle
		nameLength    uint32
		name          [256]uint16
	}
	path := filepath.Join(parent.Name(), name)
	name16, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	info := renameInfo{
		rootDirectory: windows.Handle(parent.Fd()),
		nameLength:    uint32((len(name16) - 1) * 2),
	}
	if len(name16) > len(info.name) {
		return &os.PathError{Op: "rename", Path: path, Err: windows.ERROR_FILENAME_EXCED_RANGE}
	}
	copy(info.name[:], name16)
	if replace {
		info.flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	}
	buf := (*byte)(unsafe.Pointer(&info))
	size := uint32(unsafe.Sizeof(info))
	handle := windows.Handle(tmp.Fd())
	const fileRenameInformationEx = 65
	err = setInfo(handle, &windows.IO_STATUS_BLOCK{}, buf, size, fileRenameInformationEx)
	if errors.Is(err, windows.STATUS_INVALID_PARAMETER) || errors.Is(err, windows.STATUS_NOT_SUPPORTED) || errors.Is(err, windows.STATUS_INVALID_INFO_CLASS) {
		err = fmt.Errorf("Windows POSIX rename is unavailable: %w", errors.ErrUnsupported)
	}
	runtime.KeepAlive(parent)
	runtime.KeepAlive(tmp)
	if code, ok := err.(windows.NTStatus); ok {
		err = code.Errno()
	}
	if err != nil {
		return &os.PathError{Op: "rename", Path: path, Err: err}
	}
	return nil
}

// ReadFile reads path, retrying the brief holds AtomicReplaceFile retries:
// an open that meets another process's replace or removal of path in flight
// fails with ERROR_SHARING_VIOLATION or ERROR_ACCESS_DENIED (a file pending
// deletion). A missing file is reported at once. Every attempt counts at
// ReadFile's caller, as on POSIX, so the counters name the reading code
// rather than this wrapper.
func ReadFile(path string) ([]byte, error) {
	site := fileIOSite(2, "read_file")
	var data []byte
	err := retryTransient(func() error {
		countFileIOAt(site)
		var err error
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}

// RemoveFile removes path, retrying the brief holds AtomicReplaceFile
// retries: a reader holding path without FILE_SHARE_DELETE fails the removal
// with ERROR_SHARING_VIOLATION or ERROR_ACCESS_DENIED.
func RemoveFile(path string) error {
	return retryTransient(func() error { return os.Remove(path) })
}

// retryTransient runs op until it succeeds, fails with an error other than a
// transient hold, or about two seconds pass, backing off between attempts.
func retryTransient(op func() error) error {
	deadline := time.Now().Add(2 * time.Second)
	backoff := 5 * time.Millisecond
	for {
		err := op()
		if err == nil || !isTransientReplaceError(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(backoff)
		if backoff < 200*time.Millisecond {
			backoff *= 2
		}
	}
}

// isTransientReplaceError reports whether err is the brief hold of another
// handle on the replace target.
func isTransientReplaceError(err error) bool {
	var errno windows.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == windows.ERROR_ACCESS_DENIED || errno == windows.ERROR_SHARING_VIOLATION
}
