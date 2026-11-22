package wsmeta

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// makePrivate gives the database and both side files a protected DACL that
// grants the current user alone full access, before SQLite opens any of them.
// Go's create mode sets no ACL on Windows, and the SQLite Windows VFS creates
// its files with default security attributes. Either way a new file would
// inherit its directory's ACL (a shared custom cache root, for example). So
// each file is created here when missing, without truncation, and narrowed;
// SQLite reuses an existing file and keeps its DACL.
//
// SQLite deletes a pre-created empty journal only when the database itself
// is empty. The journal it then creates for the first write holds no page
// images, because pages past the original size are never journaled. On a
// non-empty database in WAL mode, the pre-created journal stays an empty,
// cold file. This runs on every open, so a file restored or created by an
// older binary is narrowed too. Agent records hold session keys and
// capability secrets.
func makePrivate(dbPath string) error {
	dacl, err := ownerOnlyDACL()
	if err != nil {
		return err
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-journal"} {
		info, err := os.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
			if err != nil {
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
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

// ownerOnlyDACL is a DACL with one ACE: full file access for the process
// token's user.
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
