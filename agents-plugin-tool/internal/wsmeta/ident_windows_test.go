package wsmeta

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestWitnessIdentitySurvivesTunneling simulates NTFS tunneling: the moved
// file takes another creation time, as when its new name's previous holder
// was just deleted. The witness identity must not change.
func TestWitnessIdentitySurvivesTunneling(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tracker-hooks.json")
	if err := os.WriteFile(src, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := entryIdentity(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "tracker-hooks-config.json")
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}
	u, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(u, windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	old := windows.NsecToFiletime(time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	err = windows.SetFileTime(h, &old, nil, nil)
	windows.CloseHandle(h)
	if err != nil {
		t.Fatal(err)
	}
	after, err := entryIdentity(dst)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("identity changed across a rename with a tunneled creation time: %s -> %s", before, after)
	}
}
