//go:build windows

package wscred

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileAllAccess is FILE_ALL_ACCESS (SDDL "FA"); x/sys/windows has no name for it.
const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1FF

// TestCredentialFileIsOwnerOnly reads the DACL structurally, not as an SDDL
// string: the string form spells the user's SID as an alias when one exists
// (the built-in administrator, RID 500, prints as "LA", which a CI runner's
// account is) and adds the auto-inherited flag ("PAI") that
// SetNamedSecurityInfo sets, so comparing text to "D:P(A;;FA;;;<sid>)" fails
// on a file that is exactly owner-only.
func TestCredentialFileIsOwnerOnly(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	sd, err := windows.GetNamedSecurityInfo(s.Path(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("credential file DACL inherits from its directory: control %#x, DACL %s", control, sd.String())
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("credential file has no DACL: %v (%s)", err, sd.String())
	}
	if dacl.AceCount != 1 {
		t.Fatalf("credential file DACL has %d entries, want only the current user: %s", dacl.AceCount, sd.String())
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	owner := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !owner.Equals(user.User.Sid) || ace.Mask != fileAllAccess {
		t.Fatalf("credential file DACL = %s, want one allow entry with full access for %s", sd.String(), user.User.Sid)
	}
}
