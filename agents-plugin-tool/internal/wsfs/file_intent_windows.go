package wsfs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"github.com/kang-sw/devenv/internal/wsstate"
	"golang.org/x/sys/windows"
)

type mutationPermissions struct {
	owner     *windows.SID
	dacl      *windows.ACL
	protected bool
	label     *windows.ACL
}

func readMutationPermissions(file *os.File, _ os.FileInfo) (*mutationPermissions, error) {
	const security = windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION | windows.LABEL_SECURITY_INFORMATION
	sd, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, security)
	runtime.KeepAlive(file)
	if err != nil {
		return nil, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return nil, fmt.Errorf("read owner: %w", err)
	}
	if owner == nil {
		return nil, fmt.Errorf("security descriptor has no owner")
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, fmt.Errorf("read DACL: %w", err)
	}
	control, _, err := sd.Control()
	if err != nil {
		return nil, err
	}
	label, _, err := sd.SACL() // Only mandatory integrity information was requested.
	if err != nil && !errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
		return nil, fmt.Errorf("read mandatory integrity label: %w", err)
	}
	if label != nil && label.AceCount == 0 {
		label = nil
	}
	return &mutationPermissions{owner, dacl, control&windows.SE_DACL_PROTECTED != 0, label}, nil
}

func (p *mutationPermissions) creationDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	sd, err := windows.NewSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	if err := sd.SetOwner(p.owner, false); err != nil {
		return nil, fmt.Errorf("preserve owner: %w", err)
	}
	if err := sd.SetDACL(p.dacl, true, false); err != nil {
		return nil, fmt.Errorf("preserve DACL: %w", err)
	}
	// Windows can assign or normalize inherited ACEs during creation. Verify
	// the resulting DACL and protection flag before the file receives bytes.
	control := windows.SECURITY_DESCRIPTOR_CONTROL(windows.SE_DACL_AUTO_INHERIT_REQ)
	if p.protected {
		control = windows.SE_DACL_PROTECTED
	}
	if err := sd.SetControl(windows.SE_DACL_PROTECTED|windows.SE_DACL_AUTO_INHERIT_REQ, control); err != nil {
		return nil, fmt.Errorf("preserve DACL protection: %w", err)
	}
	if p.label != nil {
		if err := sd.SetSACL(p.label, true, false); err != nil {
			return nil, fmt.Errorf("preserve mandatory integrity label: %w", err)
		}
	}
	return sd, nil
}

func (p *mutationPermissions) prepare(tmp *os.File) error { return p.verify(tmp, nil) }

func (p *mutationPermissions) verify(file *os.File, _ os.FileInfo) error {
	actual, err := readMutationPermissions(file, nil)
	if err != nil {
		return err
	}
	if !actual.owner.Equals(p.owner) {
		return fmt.Errorf("owner differs")
	}
	if actual.protected != p.protected || !equalMutationACL(actual.dacl, p.dacl) {
		return fmt.Errorf("DACL or DACL protection differs")
	}
	if !equalMutationACL(actual.label, p.label) {
		return fmt.Errorf("mandatory integrity label differs")
	}
	return nil
}

func equalMutationACL(a, b *windows.ACL) bool {
	// A null DACL allows all access. A present DACL with zero ACEs denies it.
	if a == nil || b == nil {
		return a == b
	}
	if a.AceCount != b.AceCount {
		return false
	}
	for i := uint32(0); i < uint32(a.AceCount); i++ {
		var left, right *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(a, i, &left) != nil || windows.GetAce(b, i, &right) != nil || left.Header.AceSize != right.Header.AceSize {
			return false
		}
		if !bytes.Equal(unsafe.Slice((*byte)(unsafe.Pointer(left)), left.Header.AceSize), unsafe.Slice((*byte)(unsafe.Pointer(right)), right.Header.AceSize)) {
			return false
		}
	}
	return true
}

func openMutationTemp(parent *os.File, name string, permissions *mutationPermissions) (*os.File, error) {
	var sd *windows.SECURITY_DESCRIPTOR
	if permissions != nil {
		var err error
		sd, err = permissions.creationDescriptor()
		if err != nil {
			return nil, err
		}
	}
	const access = windows.GENERIC_WRITE | windows.FILE_READ_ATTRIBUTES | windows.DELETE
	file, err := openMutationAt(parent, name, access, windows.FILE_NON_DIRECTORY_FILE, true, sd)
	if err != nil && permissions != nil {
		return nil, fmt.Errorf("create staging file preserving owner, DACL and mandatory integrity label: %w", err)
	}
	return file, err
}

func publishMutationTemp(parent, _, tmp *os.File, name string, replace bool) error {
	if !validMutationName(name) {
		return &os.PathError{Op: "rename", Path: filepath.Join(parent.Name(), name), Err: windows.ERROR_INVALID_NAME}
	}
	return wsstate.RenameFileAt(parent, tmp, name, replace)
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
	return openMutationAt(parent, name, access, windows.FILE_DIRECTORY_FILE, create, nil)
}

func openMutationFile(parent *os.File, name string, flags int) (*os.File, error) {
	access := uint32(windows.GENERIC_READ)
	if flags&os.O_WRONLY != 0 {
		access = windows.GENERIC_WRITE | windows.FILE_READ_ATTRIBUTES
	}
	return openMutationAt(parent, name, access, windows.FILE_NON_DIRECTORY_FILE, flags&os.O_CREATE != 0, nil)
}

const mutationShare = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

func validMutationName(name string) bool {
	return filepath.IsLocal(name) && !strings.ContainsAny(name, `:/\`)
}

func openMutationAt(parent *os.File, name string, access, options uint32, create bool, sd *windows.SECURITY_DESCRIPTOR) (*os.File, error) {
	path := filepath.Join(parent.Name(), name)
	if !validMutationName(name) {
		return nil, &os.PathError{Op: "open", Path: path, Err: windows.ERROR_INVALID_NAME}
	}
	child, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	attrs := windows.OBJECT_ATTRIBUTES{
		RootDirectory:      windows.Handle(parent.Fd()),
		ObjectName:         child,
		Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
		SecurityDescriptor: sd,
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
