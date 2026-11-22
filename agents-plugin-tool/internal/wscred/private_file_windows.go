//go:build windows

package wscred

import (
	"os"

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
