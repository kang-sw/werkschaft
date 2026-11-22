//go:build windows

package mcp

import (
	"os"
	"syscall"
)

// openGeneratedPathGrant opens the held path.write descriptor with
// FILE_SHARE_DELETE, which os.OpenFile omits. A caller whose editor saves a
// generated review/prompt by delete-or-rename-replace must not hit a sharing
// violation while the daemon holds the grant, as it did not when path.generate
// closed its reservation before returning. A replaced path is a different file,
// so path.write's file-identity check still refuses it.
func openGeneratedPathGrant(path string) (*os.File, error) {
	namep, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := syscall.CreateFile(
		namep,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}
