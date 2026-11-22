//go:build windows

package wscred

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

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
	want := "D:P(A;;FA;;;" + user.User.Sid.String() + ")"
	if got := sd.String(); !strings.HasPrefix(got, want) || strings.Count(got, "(A;") != 1 {
		t.Fatalf("credential file DACL = %s, want only %s", got, want)
	}
}
