package wsfs

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplacementKeepsDarwinACLUnderInheritingParent(t *testing.T) {
	root := t.TempDir()
	must(t, os.Chmod(root, 0o777))
	path := filepath.Join(root, "private")
	must(t, os.WriteFile(path, []byte("before"), 0o600))
	if out, err := exec.Command("chmod", "+a", "everyone allow search,read,file_inherit,directory_inherit", root).CombinedOutput(); err != nil {
		t.Skipf("cannot install a native inheritable ACL: %v (%s)", err, out)
	}
	probe := filepath.Join(root, "probe")
	must(t, os.WriteFile(probe, nil, 0o600))
	if len(unixPermissionsAt(t, probe).acl) == 0 {
		t.Skip("test filesystem does not inherit native ACLs on file creation")
	}
	for _, withACL := range []bool{false, true} {
		name := "without extended ACL"
		if withACL {
			name = "with extended ACL"
		}
		t.Run(name, func(t *testing.T) {
			if withACL {
				out, err := exec.Command("chmod", "+a", "everyone deny execute", path).CombinedOutput()
				if err != nil {
					t.Fatalf("install destination ACL: %v (%s)", err, out)
				}
			}
			want := unixPermissionsAt(t, path)
			if (len(want.acl) != 0) != withACL {
				t.Fatal("destination ACL fixture did not take effect")
			}
			var observed bool
			files := &Files{Root: root, Authority: AuthorityWorkdirWrite, beforeReplacementWrite: func(stage *os.File) {
				observed = true
				must(t, want.verify(stage, nil))
				dir := unixPermissionsAt(t, filepath.Dir(stage.Name()))
				if dir.mode != 0o700 || len(dir.acl) != 0 {
					t.Fatalf("staging directory mode = %v, ACL = %x", dir.mode, dir.acl)
				}
			}}
			_, err := callMutation(t, files, "write", map[string]any{"path": "private", "content": name})
			must(t, err)
			if !observed {
				t.Fatal("missed first write")
			}
			file, err := os.Open(path)
			must(t, err)
			defer file.Close()
			must(t, want.verify(file, nil))
			assertReplacementContent(t, path, name)
		})
	}
}

func TestDarwinReplacementPreservesPreexistingStage(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private")
	oldName := sha256.Sum256([]byte("private"))
	stageDir := filepath.Join(root, ".ws-stage-"+hex.EncodeToString(oldName[:]))
	stagePath := filepath.Join(stageDir, darwinMutationStageFile)
	must(t, os.WriteFile(path, []byte("before"), 0o600))
	must(t, os.Mkdir(stageDir, 0o700))
	must(t, os.WriteFile(stagePath, []byte("keep"), 0o600))
	files := &Files{Root: root, Authority: AuthorityWorkdirWrite, beforeReplacementWrite: func(stage *os.File) {
		if filepath.Dir(stage.Name()) == stageDir {
			t.Fatal("replacement reused an existing directory")
		}
	}}
	_, err := callMutation(t, files, "write", map[string]any{"path": "private", "content": "after"})
	must(t, err)
	assertReplacementContent(t, path, "after")
	assertReplacementContent(t, stagePath, "keep")
	files.beforeReplacementWrite = func(*os.File) { t.Fatal("identical retry unexpectedly wrote bytes") }
	_, err = callMutation(t, files, "write", map[string]any{"path": "private", "content": "after"})
	must(t, err)
	assertReplacementContent(t, stagePath, "keep")
}

func TestDarwinReplacementSerializesDestination(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private")
	must(t, os.WriteFile(path, []byte("before"), 0o600))
	other := &Files{Root: root, Authority: AuthorityWorkdirWrite}
	args := map[string]any{"path": "private", "content": "other"}
	files := &Files{Root: root, Authority: AuthorityWorkdirWrite, beforeReplacementWrite: func(*os.File) {
		_, err := callMutation(t, other, "write", args)
		if err == nil || !strings.Contains(err.Error(), "lock replacement destination") {
			t.Fatalf("concurrent replacement = %v, want active-stage refusal", err)
		}
	}}
	_, err := callMutation(t, files, "write", map[string]any{"path": "private", "content": "after"})
	must(t, err)
	_, err = callMutation(t, other, "write", args)
	must(t, err)
	assertReplacementContent(t, path, "other")
}
