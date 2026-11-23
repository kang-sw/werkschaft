package werk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileIntentRecoversContentState(t *testing.T) {
	root := newTree(t)
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
			if _, err := call(t, f, tc.tool, tc.args); err != nil {
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
	root := newTree(t)
	stop := errors.New("log closed")
	f := &Files{Root: root, Authority: AuthorityWorkdirWrite, RecordMutation: func(context.Context, FileIntent) error { return stop }}
	if _, err := call(t, f, "write", map[string]any{"path": "new/target", "content": "test"}); !errors.Is(err, stop) {
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
	if _, err := call(t, f, "write", map[string]any{"path": "sub/file", "content": "test"}); err == nil || !strings.Contains(err.Error(), "changed while preparing") {
		t.Fatalf("write error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "file")); !os.IsNotExist(err) {
		t.Fatalf("outside file: %v", err)
	}
	if got := intent.Outcome(); got != "outcome unknown" {
		t.Fatalf("after symlink substitution: %q", got)
	}
}
