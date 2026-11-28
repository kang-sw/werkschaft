package wsdashboard

import (
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"github.com/kang-sw/devenv/internal/wsstate"
	"golang.org/x/sys/windows"
)

const companionShare = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

func openCompanionPackEntry(parent companionPackFile, name string, directory bool) (companionPackFile, error) {
	if parent == nil {
		// Only the root open takes a full path; children stay relative to
		// their pinned parent handles below.
		open := name
		if abs, err := filepath.Abs(name); err == nil {
			if ext := companionExtendedPath(abs); ext != abs {
				open = ext
			}
		}
		path, err := windows.UTF16PtrFromString(open)
		if err != nil {
			return nil, err
		}
		handle, err := windows.CreateFile(path, windows.GENERIC_READ, companionShare, nil,
			windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(handle), name), nil
	}

	// RootDirectory makes a child lookup relative to its open parent. It
	// stays anchored if the directory is concurrently renamed or replaced.
	child, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	attrs := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(parent.Fd()),
		ObjectName:    child,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	options := uint32(windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_REPARSE_POINT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.GENERIC_READ|windows.SYNCHRONIZE,
		&attrs, &status, nil, 0, companionShare, windows.FILE_OPEN, options, 0, 0)
	runtime.KeepAlive(parent)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), filepath.Join(parent.Name(), name)), nil
}

// The clip write path below resolves no path string after its pins: each
// create, rename and delete is relative to a directory handle opened with
// OBJ_DONT_REPARSE, and every opened handle is checked for a reparse point,
// so a junction or symlink at the pack directory, clips\ or a target name
// refuses the write instead of redirecting it. The replace is a POSIX
// rename of the open temp handle into the pinned directory.

// openCompanionAt opens or creates name relative to parent and refuses a
// reparse point.
func openCompanionAt(parent *os.File, name string, access, options, disposition uint32) (*os.File, error) {
	path := filepath.Join(parent.Name(), name)
	child, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	attrs := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(parent.Fd()),
		ObjectName:    child,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	options |= windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_REPARSE_POINT
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, &attrs, &status, nil,
		windows.FILE_ATTRIBUTE_NORMAL, companionShare, disposition, options, 0, 0)
	runtime.KeepAlive(parent)
	if code, ok := err.(windows.NTStatus); ok {
		err = code.Errno()
	}
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return companionPlainHandle(handle, path)
}

func companionPlainHandle(handle windows.Handle, path string) (*os.File, error) {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(handle, &info)
	if err == nil && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		err = windows.ERROR_CANT_ACCESS_FILE
	}
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// openCompanionDirAt opens a directory for mutation relative to parent (or
// the path name itself when parent is nil), refusing a reparse point.
func openCompanionDirAt(parent *os.File, name string) (*os.File, error) {
	if parent != nil {
		return openCompanionAt(parent, name, windows.GENERIC_READ|windows.FILE_TRAVERSE, windows.FILE_DIRECTORY_FILE, windows.FILE_OPEN)
	}
	open := name
	if abs, err := filepath.Abs(name); err == nil {
		if ext := companionExtendedPath(abs); ext != abs {
			open = ext
		}
	}
	path, err := windows.UTF16PtrFromString(open)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.FILE_TRAVERSE, companionShare, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return companionPlainHandle(handle, name)
}

func mkdirCompanionAt(parent *os.File, name string) error {
	dir, err := openCompanionAt(parent, name, windows.GENERIC_READ, windows.FILE_DIRECTORY_FILE, windows.FILE_CREATE)
	if err != nil {
		return err
	}
	return dir.Close()
}

// createCompanionTempAt creates a new file exclusively in dir. The handle
// grants DELETE so renameCompanionAt can publish it by handle. Windows has no
// POSIX permission bits to carry over, so perm is unused.
func createCompanionTempAt(dir *os.File, name string, _ os.FileMode) (*os.File, error) {
	return openCompanionAt(dir, name, windows.GENERIC_WRITE|windows.DELETE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_NON_DIRECTORY_FILE, windows.FILE_CREATE)
}

// renameCompanionAt replaces name in dir with the still-open temp handle.
func renameCompanionAt(dir, tmp *os.File, _, name string) error {
	return wsstate.RetryRenameFileAt(dir, tmp, name)
}

func removeCompanionAt(dir *os.File, name string) error {
	f, err := openCompanionAt(dir, name, windows.DELETE|windows.FILE_READ_ATTRIBUTES, windows.FILE_NON_DIRECTORY_FILE, windows.FILE_OPEN)
	if err != nil {
		return err
	}
	defer f.Close()
	flags := uint32(windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS)
	err = windows.SetFileInformationByHandle(windows.Handle(f.Fd()), windows.FileDispositionInfoEx, (*byte)(unsafe.Pointer(&flags)), uint32(unsafe.Sizeof(flags)))
	if err != nil {
		// Volumes without POSIX semantics take the classic delete-on-close.
		deleteFile := byte(1)
		err = windows.SetFileInformationByHandle(windows.Handle(f.Fd()), windows.FileDispositionInfo, &deleteFile, 1)
	}
	runtime.KeepAlive(f)
	if err != nil {
		return &os.PathError{Op: "remove", Path: f.Name(), Err: err}
	}
	return nil
}
