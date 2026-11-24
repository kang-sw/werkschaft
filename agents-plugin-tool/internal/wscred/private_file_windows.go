//go:build windows

package wscred

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"github.com/kang-sw/devenv/internal/wsstate"
	"golang.org/x/sys/windows"
)

// restrictFile replaces the still-empty temp file's DACL with a protected
// one (no inherited entries) granting the current user alone full access.
// The rename that follows keeps the file's own security descriptor.
func restrictFile(_ *os.File, name string) error {
	dacl, err := ownerOnlyDACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(name, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// restrictDir leaves the directory's ACL alone: the requirement is the
// file's, and the user's profile directory already excludes other users.
func restrictDir(string) error { return nil }

func openCredentialFile(path string, write bool) (*os.File, error) {
	access := uint32(windows.FILE_READ_ATTRIBUTES)
	if write {
		access |= windows.GENERIC_WRITE | windows.DELETE
	}
	return openCredentialHandle(path, access)
}

func openCredentialHandle(path string, access uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	const share = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE
	const flags = windows.FILE_FLAG_BACKUP_SEMANTICS | windows.FILE_FLAG_OPEN_REPARSE_POINT
	handle, err := windows.CreateFile(name, access, share, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

func replaceCredentialFile(staged, current string) error {
	parent, err := openCredentialHandle(filepath.Dir(current), windows.FILE_TRAVERSE|windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return err
	}
	defer parent.Close()
	// Require both operations before publishing a generation that will
	// need its retiring link truncated and unlinked.
	var volumeFlags uint32
	err = windows.GetVolumeInformationByHandle(windows.Handle(parent.Fd()), nil, 0, nil, nil, &volumeFlags, nil, 0)
	if err != nil {
		return fmt.Errorf("check Windows credential volume capabilities: %w", err)
	}
	const supportsPOSIXUnlinkRename = 0x00000400
	if volumeFlags&supportsPOSIXUnlinkRename == 0 {
		return fmt.Errorf("Windows credential storage requires POSIX rename and unlink: %w", errors.ErrUnsupported)
	}
	tmp, err := openCredentialHandle(staged, windows.DELETE|windows.SYNCHRONIZE)
	if err != nil {
		return err
	}
	defer tmp.Close()
	return wsstate.RetryRenameFileAt(parent, tmp, filepath.Base(current))
}

func removeRetiringFile(file *os.File) error {
	// POSIX unlink removes this name immediately, even when a reader
	// that shares delete keeps the old inode open via a different name.
	flags := uint32(windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS)
	err := windows.SetFileInformationByHandle(windows.Handle(file.Fd()), windows.FileDispositionInfoEx, (*byte)(unsafe.Pointer(&flags)), uint32(unsafe.Sizeof(flags)))
	runtime.KeepAlive(file)
	if err != nil {
		return fmt.Errorf("unlink the retired credential generation with Windows POSIX disposition: %w", err)
	}
	return nil
}

// ownerOnlyDACL is "D:P(A;;FA;;;<current user SID>)".
func ownerOnlyDACL() (*windows.ACL, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, err
	}
	dacl, _, err := sd.DACL()
	return dacl, err
}
