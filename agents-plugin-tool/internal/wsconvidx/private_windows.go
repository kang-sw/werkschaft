package wsconvidx

import (
	"errors"
	"io/fs"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errNotRegular = errors.New("not a regular file")

// makePrivate gives the database and both side files a protected DACL granting
// full access to the current user alone before SQLite opens any of them.
func makePrivate(dbPath string) error {
	sd, err := ownerOnlyDescriptor()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-journal"} {
		created, err := createPrivate(p, sd)
		if err != nil {
			return err
		}
		if created {
			continue
		}
		info, err := os.Lstat(p)
		switch {
		case err != nil:
			return err
		case !info.Mode().IsRegular():
			return &fs.PathError{Op: "open", Path: p, Err: errNotRegular}
		}
		if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
			nil, nil, dacl, nil); err != nil {
			return &os.PathError{Op: "set owner-only ACL", Path: p, Err: err}
		}
	}
	return nil
}

func createPrivate(p string, sd *windows.SECURITY_DESCRIPTOR) (bool, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return false, err
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, &sa,
		windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return false, nil
	}
	if err != nil {
		return false, &os.PathError{Op: "create", Path: p, Err: err}
	}
	if err := windows.CloseHandle(h); err != nil {
		return true, &os.PathError{Op: "close", Path: p, Err: err}
	}
	return true, nil
}

func ownerOnlyDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
}
