package wsmeta

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
		paths := []string{s.Path(), s.Path() + "-wal"}
		if _, err := os.Lstat(s.Path() + "-journal"); err == nil {
			paths = append(paths, s.Path()+"-journal")
		}
		for _, p := range paths {
			checkOwnerOnly(t, p)
		}
	}
	check()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir, testOpts())
	check()
}

// checkOwnerOnly asserts that p has a protected DACL with one ACE, for the
// current user.
func checkOwnerOnly(t *testing.T, p string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
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

// Each database file is owner-only from the instant it exists: the handle
// that creates it already carries the private DACL, so no principal the
// directory admits can open it first and keep that access.
func TestDatabaseFilesAreOwnerOnlyFromCreation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dashboard")
	prev := privateCreated
	t.Cleanup(func() { privateCreated = prev })
	var seen []string
	privateCreated = func(p string) {
		seen = append(seen, filepath.Base(p))
		checkOwnerOnly(t, p)
	}
	openTest(t, dir, testOpts())
	if want := []string{"metadata.sqlite", "metadata.sqlite-wal", "metadata.sqlite-journal"}; strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("created %v, want %v", seen, want)
	}
}
