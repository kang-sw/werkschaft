package wsdashboard

import (
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

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
