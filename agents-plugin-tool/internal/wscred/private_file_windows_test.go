//go:build windows

package wscred

import (
	"errors"
	"io"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestCredentialReplacementWindowsRefusesUnsupportedPOSIXRename(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	old := mustRead(t, s.Path())
	alias := linkCredentialFixture(t, s.Path())
	replace := func(string, string) error { return errors.ErrUnsupported }
	published, err := writePrivateFile(s.Path(), []byte("fixture replacement"), replace, nil)
	if published || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("published = %v, error = %v", published, err)
	}
	if mustRead(t, s.Path()) != old || mustRead(t, alias) != old {
		t.Fatal("unsupported POSIX rename changed the previous generation")
	}
	assertOnlyCurrentCredential(t, s.Path())
}

func TestCredentialReplacementWindowsWithOpenHandles(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	alias := linkCredentialFixture(t, s.Path())
	oldHandle := func(path string) *os.File {
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		const share = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE
		h, err := windows.CreateFile(name, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		file := os.NewFile(uintptr(h), path)
		t.Cleanup(func() { _ = file.Close() })
		return file
	}
	currentHandle, aliasHandle := oldHandle(s.Path()), oldHandle(alias)
	setKey(t, s, fixtureKey2)
	for _, file := range []*os.File{currentHandle, aliasHandle} {
		if data, err := io.ReadAll(file); err != nil || len(data) != 0 {
			t.Fatalf("open handle sees %d retired bytes, error %v", len(data), err)
		}
	}
	if cred, err := openrouter(NewStore(s.Path())); err != nil || cred.Secret.Reveal() != fixtureKey2 {
		t.Fatalf("current path does not open the replacement: %v", err)
	}
	assertOnlyCurrentCredential(t, s.Path())
}

func TestCredentialReplacementWindowsKeepsCurrentWhenScrubHandleIsBlocked(t *testing.T) {
	s := testStore(t)
	setKey(t, s, fixtureKey)
	old := mustRead(t, s.Path())
	alias := linkCredentialFixture(t, s.Path())
	name, err := windows.UTF16PtrFromString(alias)
	if err != nil {
		t.Fatal(err)
	}
	const shareWithoutWrite = windows.FILE_SHARE_READ | windows.FILE_SHARE_DELETE
	h, err := windows.CreateFile(name, windows.GENERIC_READ, shareWithoutWrite, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, updateErr := s.Apply(Change{Op: OpSet, Provider: ProviderOpenRouter, APIKey: NewSecret(fixtureKey2)})
	if err := windows.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(updateErr, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("update while the old generation blocks scrubbing = %v", updateErr)
	}
	if mustRead(t, s.Path()) != old || mustRead(t, alias) != old {
		t.Fatal("the blocked scrub changed the previous generation")
	}
	assertOnlyCurrentCredential(t, s.Path())
}

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
