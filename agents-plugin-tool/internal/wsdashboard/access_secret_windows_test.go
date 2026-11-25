//go:build windows

package wsdashboard

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestPersistedSecretWindowsSharingViolationAndAccessDenied(t *testing.T) {
	if !isSecretSharingViolation(windows.ERROR_SHARING_VIOLATION) {
		t.Fatal("isSecretSharingViolation(ERROR_SHARING_VIOLATION) = false, want true")
	}
	if isSecretSharingViolation(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("isSecretSharingViolation(ERROR_ACCESS_DENIED) = true, want false")
	}

	d := authFixture(t)
	minted, ok, err := d.persistedSecret(LANTokenFileName)
	if err != nil || !ok || minted == "" {
		t.Fatalf("initial persistedSecret = (%q, %v, %v)", minted, ok, err)
	}
	path := filepath.Join(d.dir, LANTokenFileName)
	// Hold an exclusive read/write Windows handle (shareMode == 0) so
	// ReadFileOnce hits a real kernel ERROR_SHARING_VIOLATION (matching the
	// Server 2025 CI-hardened pattern in internal/wsstate).
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(
		p16,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("CreateFile exclusive: %v", err)
	}
	var closeOnce sync.Once
	closeHandle := func() {
		closeOnce.Do(func() {
			_ = windows.CloseHandle(handle)
		})
	}
	defer closeHandle()

	// Same daemon resolves via publishedSecrets immediately while locked.
	got, mintedAgain, err := d.persistedSecret(LANTokenFileName)
	if err != nil || mintedAgain || got != minted {
		t.Fatalf("same-daemon persistedSecret under exclusive handle = (%q, %v, %v), want (%q, false, nil)", got, mintedAgain, err, minted)
	}

	// A peer daemon without publishedSecrets retries until the exclusive handle releases.
	peer := authFixture(t)
	peer.dir = d.dir
	go func() {
		time.Sleep(30 * time.Millisecond)
		closeHandle()
	}()
	got, mintedAgain, err = peer.persistedSecret(LANTokenFileName)
	if err != nil || mintedAgain || got != minted {
		t.Fatalf("peer persistedSecret after exclusive handle release = (%q, %v, %v), want (%q, false, nil)", got, mintedAgain, err, minted)
	}
}
