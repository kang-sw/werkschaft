package wsfs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openMutationTemp(parent *os.File, name string) (*os.File, error) {
	return openMutationAt(parent, name, windows.GENERIC_WRITE|windows.FILE_READ_ATTRIBUTES|windows.DELETE, windows.FILE_NON_DIRECTORY_FILE, true)
}

func setMutationTempMode(*os.File, os.FileMode) error { return nil }

func publishMutationTemp(parent, tmp *os.File, name string, replace bool) error {
	// The target name is relative to the pinned directory handle. POSIX
	// replacement allows existing handles that share delete to remain open.
	type renameInfo struct {
		flags         uint32
		rootDirectory windows.Handle
		nameLength    uint32
		name          [256]uint16
	}
	path := filepath.Join(parent.Name(), name)
	if !validMutationName(name) {
		return &os.PathError{Op: "rename", Path: path, Err: windows.ERROR_INVALID_NAME}
	}
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
	err = windows.NtSetInformationFile(handle, &windows.IO_STATUS_BLOCK{}, buf, size, fileRenameInformationEx)
	if errors.Is(err, windows.STATUS_INVALID_PARAMETER) || errors.Is(err, windows.STATUS_NOT_SUPPORTED) || errors.Is(err, windows.STATUS_INVALID_INFO_CLASS) {
		info.flags &= windows.FILE_RENAME_REPLACE_IF_EXISTS
		err = windows.NtSetInformationFile(handle, &windows.IO_STATUS_BLOCK{}, buf, size, windows.FileRenameInformation)
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

func discardMutationTemp(_ *os.File, tmp *os.File) {
	removeOnClose := byte(1)
	_ = windows.SetFileInformationByHandle(windows.Handle(tmp.Fd()), windows.FileDispositionInfo, &removeOnClose, 1)
	runtime.KeepAlive(tmp)
}

func openMutationDir(parent *os.File, name string, create bool) (*os.File, error) {
	const access = windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES
	if parent == nil {
		path, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: name, Err: err}
		}
		handle, err := windows.CreateFile(path, access, mutationShare, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: name, Err: err}
		}
		return mutationHandle(handle, name)
	}
	return openMutationAt(parent, name, access, windows.FILE_DIRECTORY_FILE, create)
}

func openMutationFile(parent *os.File, name string, flags int) (*os.File, error) {
	access := uint32(windows.GENERIC_READ)
	if flags&os.O_WRONLY != 0 {
		access = windows.GENERIC_WRITE | windows.FILE_READ_ATTRIBUTES
	}
	return openMutationAt(parent, name, access, windows.FILE_NON_DIRECTORY_FILE, flags&os.O_CREATE != 0)
}

const mutationShare = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

func validMutationName(name string) bool {
	return filepath.IsLocal(name) && !strings.ContainsAny(name, `:/\`)
}

func openMutationAt(parent *os.File, name string, access, options uint32, create bool) (*os.File, error) {
	path := filepath.Join(parent.Name(), name)
	if !validMutationName(name) {
		return nil, &os.PathError{Op: "open", Path: path, Err: windows.ERROR_INVALID_NAME}
	}
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
	disposition := uint32(windows.FILE_OPEN)
	if create {
		disposition = windows.FILE_CREATE
	}
	options |= windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_REPARSE_POINT
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, &attrs, &status, nil,
		windows.FILE_ATTRIBUTE_NORMAL, mutationShare, disposition, options, 0, 0)
	runtime.KeepAlive(parent)
	if code, ok := err.(windows.NTStatus); ok {
		err = code.Errno()
	}
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return mutationHandle(handle, path)
}

func mutationHandle(handle windows.Handle, path string) (*os.File, error) {
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
