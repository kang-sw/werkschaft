package wsmeta

import (
	"errors"
	"io/fs"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// makePrivate gives the database and both side files a protected DACL that
// grants the current user alone full access, before SQLite opens any of them.
// Go's create mode sets no ACL on Windows, and the SQLite Windows VFS creates
// its files with default security attributes. Either way a new file would
// inherit its directory's ACL (a shared custom cache root, for example), and
// a handle another principal opened in that window would keep its access
// after the DACL is narrowed. So each missing file is created here with the
// owner-only security descriptor itself (CREATE_NEW, no sharing while the
// creating handle is open): no other principal can ever open it. SQLite
// reuses an existing file and keeps its DACL.
//
// SQLite deletes a pre-created empty journal only when the database itself
// is empty. The journal it then creates for the first write holds no page
// images, because pages past the original size are never journaled. On a
// non-empty database in WAL mode, the pre-created journal stays an empty,
// cold file. This runs on every open, so a file restored or created by an
// older binary is narrowed too (access granted to a handle before that
// narrowing cannot be revoked; the migration's own files never have one).
// Agent records hold session keys and capability secrets.
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

// privateCreated runs while the handle that created p is still open (a test
// seam that inspects the new file's security).
var privateCreated = func(p string) {}

// createPrivate creates p with sd as its security descriptor and reports
// whether it did; an existing p is left alone.
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
	privateCreated(p)
	if err := windows.CloseHandle(h); err != nil {
		return true, &os.PathError{Op: "close", Path: p, Err: err}
	}
	return true, nil
}

// ownerOnlyDescriptor is a security descriptor with a protected DACL of one
// ACE: full file access for the process token's user.
func ownerOnlyDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
}
