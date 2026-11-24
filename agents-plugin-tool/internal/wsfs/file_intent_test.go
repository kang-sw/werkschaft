package wsfs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func assertReplacementContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	must(t, err)
	if string(data) != want {
		t.Fatalf("content = %q, want %q", data, want)
	}
}

func TestFileIntentRecoversContentState(t *testing.T) {
	root := t.TempDir()
	f := &Files{Root: root, Authority: AuthorityWorkdirWrite}
	for _, tc := range []struct {
		name  string
		prior string
		tool  string
		args  map[string]any
	}{
		{"edit", "before", "edit", map[string]any{"edits": []map[string]string{{"oldText": "before", "newText": "after"}}}},
		{"overwrite", "before", "write", map[string]any{"content": "after"}},
		{"create", "", "write", map[string]any{"content": "after"}},
		{"identical", "after", "write", map[string]any{"content": "after"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, tc.name, "target.txt")
			tc.args["path"] = filepath.Join(tc.name, "target.txt")
			if tc.prior != "" {
				must(t, os.MkdirAll(filepath.Dir(path), 0o755))
				must(t, os.WriteFile(path, []byte(tc.prior), 0o644))
			}
			var recorded FileIntent
			f.RecordMutation = func(_ context.Context, intent FileIntent) error {
				recorded = intent
				want := "not applied"
				if tc.prior == "after" {
					want = "applied"
				}
				if resolved, err := realPath(path); err != nil || resolved != intent.Target {
					t.Fatalf("target = %q, want %q, err %v", intent.Target, resolved, err)
				}
				if (intent.Prior == nil) != (tc.prior == "") || intent.Expected != contentHash([]byte("after")) {
					t.Fatalf("intent = %+v", intent)
				}
				if got := intent.Outcome(); got != want {
					t.Fatalf("before write: %q, want %q", got, want)
				}
				return nil
			}
			if _, err := callMutation(t, f, tc.tool, tc.args); err != nil {
				t.Fatal(err)
			}
			if got := recorded.Outcome(); got != "applied" {
				t.Fatalf("after write: %q", got)
			}
			must(t, os.WriteFile(path, []byte("other state"), 0o644))
			if got := recorded.Outcome(); got != "outcome unknown" {
				t.Fatalf("after unrelated write: %q", got)
			}
		})
	}
}

func TestFileWriteRefusedWhenIntentCannotBeRecorded(t *testing.T) {
	root := t.TempDir()
	stop := errors.New("log closed")
	f := &Files{Root: root, Authority: AuthorityWorkdirWrite, RecordMutation: func(context.Context, FileIntent) error { return stop }}
	if _, err := callMutation(t, f, "write", map[string]any{"path": "new/target", "content": "test"}); !errors.Is(err, stop) {
		t.Fatalf("write error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "new")); !os.IsNotExist(err) {
		t.Fatalf("write touched its parent: %v", err)
	}
}

func TestFileWriteRefusedIfTargetChangesDuringIntent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	root, outside := t.TempDir(), t.TempDir()
	must(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	f := &Files{Root: root, Authority: AuthorityWorkdirWrite}
	var intent FileIntent
	f.RecordMutation = func(_ context.Context, i FileIntent) error {
		intent = i
		must(t, os.Rename(filepath.Join(root, "sub"), filepath.Join(root, "moved")))
		return os.Symlink(outside, filepath.Join(root, "sub"))
	}
	if _, err := callMutation(t, f, "write", map[string]any{"path": "sub/file", "content": "test"}); err == nil || !strings.Contains(err.Error(), "changed while preparing") {
		t.Fatalf("write error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "file")); !os.IsNotExist(err) {
		t.Fatalf("outside file: %v", err)
	}
	if got := intent.Outcome(); got != "outcome unknown" {
		t.Fatalf("after symlink substitution: %q", got)
	}
}

func TestFileMutationRefusesDirectoryReplacedDuringIntent(t *testing.T) {
	for _, tool := range []string{"write", "edit"} {
		t.Run(tool, func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "parent")
			saved := filepath.Join(root, "saved")
			must(t, os.Mkdir(parent, 0o755))
			args := map[string]any{"path": filepath.Join("parent", "nested", "target"), "content": "after"}
			if tool == "edit" {
				args["path"] = filepath.Join("parent", "target")
				args["edits"] = []map[string]string{{"oldText": "before", "newText": "after"}}
				must(t, os.WriteFile(filepath.Join(parent, "target"), []byte("before"), 0o644))
			}
			files := &Files{Root: root, Authority: AuthorityWorkdirWrite}
			files.RecordMutation = func(context.Context, FileIntent) error {
				must(t, os.Rename(parent, saved))
				must(t, os.Mkdir(parent, 0o755))
				if tool == "edit" {
					must(t, os.WriteFile(filepath.Join(parent, "target"), []byte("replacement"), 0o644))
				}
				return nil
			}
			if _, err := callMutation(t, files, tool, args); err == nil || !strings.Contains(err.Error(), "changed while preparing") {
				t.Fatalf("%s error = %v", tool, err)
			}
			if tool == "write" {
				for _, dir := range []string{parent, saved} {
					if _, err := os.Stat(filepath.Join(dir, "nested")); !os.IsNotExist(err) {
						t.Errorf("parent changed: %v", err)
					}
				}
				return
			}
			for dir, want := range map[string]string{parent: "replacement", saved: "before"} {
				data, err := os.ReadFile(filepath.Join(dir, "target"))
				if err != nil || string(data) != want {
					t.Errorf("target = %q, %v, want %q", data, err, want)
				}
			}
		})
	}
}

func TestPinnedMutationOpenUsesTheOriginalDirectoryAfterReplacement(t *testing.T) {
	root, err := realPath(t.TempDir())
	must(t, err)
	parent := filepath.Join(root, "parent")
	original := filepath.Join(root, "original")
	must(t, os.Mkdir(parent, 0o755))
	must(t, os.WriteFile(filepath.Join(parent, "target"), []byte("original"), 0o644))
	target, err := pinMutationTarget(filepath.Join(parent, "target"))
	must(t, err)
	defer target.close()
	must(t, target.checkParent())
	must(t, os.Rename(parent, original))
	must(t, os.Mkdir(parent, 0o755))
	must(t, os.WriteFile(filepath.Join(parent, "target"), []byte("replacement"), 0o644))

	_, data, err := target.inspect(true, nil)
	must(t, err)
	if string(data) != "original" {
		t.Errorf("pinned read followed the replacement: %q", data)
	}
	if err := target.checkParent(); err == nil {
		t.Error("replacement passed parent validation")
	}
}

func callMutation(t *testing.T, files *Files, name string, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	must(t, err)
	if name == "edit" {
		return files.Edit(context.Background(), raw)
	}
	return files.Write(context.Background(), raw)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
