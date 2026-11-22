package wsmeta

import (
	"os"

	"golang.org/x/sys/windows"
)

// makePrivate creates the database file and its WAL when missing and gives
// both, and any existing journal, a protected DACL that grants the current
// user alone full access. Go's create mode sets no ACL on Windows, so a file
// would otherwise inherit its directory's ACL (a shared custom cache root,
// for example). The WAL is created ahead of SQLite for the same reason;
// SQLite reuses an existing empty WAL. This runs on every open, so a file
// restored or created by an older binary is narrowed too. Agent records hold
// session keys and capability secrets.
func makePrivate(dbPath string) error {
	dacl, err := ownerOnlyDACL()
	if err != nil {
		return err
	}
	for _, p := range []string{dbPath, dbPath + "-wal"} {
		f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-journal"} {
		if p == dbPath+"-journal" {
			if _, err := os.Lstat(p); err != nil {
				continue
			}
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
