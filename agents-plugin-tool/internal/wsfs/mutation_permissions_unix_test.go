//go:build darwin || linux

package wsfs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReplacementKeepsUnixPermissionsAtFirstWrite(t *testing.T) {
	root := t.TempDir()
	must(t, os.Chmod(root, 0o777))
	path := filepath.Join(root, "private")
	must(t, os.WriteFile(path, []byte("before"), 0o600))
	want := unixPermissionsAt(t, path)
	var observed bool
	files := &Files{Root: root, Authority: AuthorityWorkdirWrite, beforeReplacementWrite: func(stage *os.File) {
		observed = true
		must(t, want.verify(stage, nil))
		info, err := stage.Stat()
		must(t, err)
		if info.Size() != 0 {
			t.Fatalf("stage already holds %d bytes at first write", info.Size())
		}
	}}
	_, err := callMutation(t, files, "write", map[string]any{"path": "private", "content": "after"})
	must(t, err)
	if !observed {
		t.Fatal("missed first write")
	}
	file, err := os.Open(path)
	must(t, err)
	defer file.Close()
	must(t, want.verify(file, nil))
	assertReplacementContent(t, path, "after")
}

func TestReplacementRechecksUnixDestinationPermissions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private")
	must(t, os.WriteFile(path, []byte("before"), 0o640))
	must(t, os.Chmod(path, 0o640))
	files := &Files{Root: root, Authority: AuthorityWorkdirWrite, beforeReplacementWrite: func(*os.File) {
		must(t, os.Chmod(path, 0o600))
	}}
	_, err := callMutation(t, files, "write", map[string]any{"path": "private", "content": "after"})
	if err == nil || !strings.Contains(err.Error(), "recheck destination permissions") {
		t.Fatalf("error = %v, want destination permission refusal", err)
	}
	assertReplacementContent(t, path, "before")
}

func TestReplacementRefusesUnassignableUnixGroup(t *testing.T) {
	const childRoot = "WS_TEST_REPLACEMENT_FOREIGN_GROUP_ROOT"
	if root := os.Getenv(childRoot); root != "" {
		files := &Files{Root: root, Authority: AuthorityWorkdirWrite, beforeReplacementWrite: func(*os.File) {
			t.Fatal("reached first write without reproducing the group")
		}}
		_, err := callMutation(t, files, "write", map[string]any{"path": "private", "content": "after"})
		if err == nil || !strings.Contains(err.Error(), "preserve owner and group") {
			t.Fatalf("error = %v, want group-preservation refusal", err)
		}
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("creating a destination with a group the test writer cannot assign requires root fixture setup")
	}
	root := t.TempDir()
	must(t, os.Chmod(filepath.Dir(root), 0o755))
	must(t, os.Chmod(root, 0o777))
	path := filepath.Join(root, "private")
	must(t, os.WriteFile(path, []byte("before"), 0o600))
	const uid, gid, foreignGroup = 12345, 12345, 12346
	must(t, os.Chown(path, uid, foreignGroup))
	self, err := os.Executable()
	must(t, err)
	binary, err := os.ReadFile(self)
	must(t, err)
	child := filepath.Join(root, "test-writer")
	must(t, os.WriteFile(child, binary, 0o755))
	cmd := exec.Command(child, "-test.run=^TestReplacementRefusesUnassignableUnixGroup$")
	cmd.Env = append(os.Environ(), childRoot+"="+root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid}}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unprivileged write fixture: %v (%s)", err, out)
	}
	assertReplacementContent(t, path, "before")
}

func unixPermissionsAt(t *testing.T, path string) *mutationPermissions {
	t.Helper()
	file, err := os.Open(path)
	must(t, err)
	defer file.Close()
	permissions, err := readMutationPermissions(file, nil)
	must(t, err)
	return permissions
}
