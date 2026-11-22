package wsmeta

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The database, its WAL and any journal carry a protected DACL with a single ACE for the
// current user, on first open and on reopen.
func TestDatabaseFilesHaveOwnerOnlyACL(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "dashboard")
	s := openTest(t, dir, testOpts())
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			t.Fatal(err)
		}
		paths := []string{s.Path(), s.Path() + "-wal"}
		if _, err := os.Lstat(s.Path() + "-journal"); err == nil {
			paths = append(paths, s.Path()+"-journal")
		}
		for _, p := range paths {
			sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			control, _, err := sd.Control()
			if err != nil {
				t.Fatal(err)
			}
			if control&windows.SE_DACL_PROTECTED == 0 {
				t.Fatalf("%s: DACL inherits from its directory", p)
			}
			dacl, _, err := sd.DACL()
			if err != nil || dacl == nil {
				t.Fatalf("%s: DACL %v %v", p, dacl, err)
			}
			if dacl.AceCount != 1 {
				t.Fatalf("%s: %d ACEs, want 1", p, dacl.AceCount)
			}
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(dacl, 0, &ace); err != nil {
				t.Fatal(err)
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.Equals(user.User.Sid) {
				t.Fatalf("%s: ACE for %s, want %s", p, sid, user.User.Sid)
			}
		}
	}
	check()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir, testOpts())
	check()
}
