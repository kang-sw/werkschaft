package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func TestFileToolsProtectProviderCredentials(t *testing.T) {
	fx := newFSFixture(t, wsagent.BackendWerk)
	fx.s.callerBinding = boundCallers{backends: map[string]string{fx.s.conversation: wsagent.BackendWerk}}
	fx.s.fileWrites = &FileWriteSession{
		Authority:        wsfs.AuthorityReadOnly,
		CurrentAuthority: func() wsfs.Authority { return wsfs.AuthorityReadOnly },
	}
	if !fx.s.readOnlyAuthority() || !fx.s.fsReadTools() {
		t.Fatal("expected a read-only session with daemon file reads")
	}
	cache := os.Getenv(wsstate.EnvCacheHome)
	current, err := wscred.DefaultPath(cache)
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(cache, "auth", wscred.FileName)
	const secret = "fixture-value-private"
	const protected = "the provider credential store is protected"
	for _, path := range []string{current, legacy} {
		mustWrite(t, filepath.Dir(path), filepath.Base(path), secret+"\n")
	}
	currentAlias := filepath.Join(fx.root, "current-alias.txt")
	legacyAlias := filepath.Join(fx.root, "legacy-alias.txt")
	if err := os.Link(current, currentAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(legacy, legacyAlias); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, fx.root, "public.txt", "fixture-value-public\n")
	mustWrite(t, cache, "public.txt", "fixture-value-public\n")
	for _, path := range []string{current, legacy, currentAlias, legacyAlias} {
		for _, tool := range []string{"fs.read", "fs.grep"} {
			args := map[string]any{"path": path}
			if tool == "fs.grep" {
				args["pattern"] = "fixture-value-"
			}
			if text, failed := fx.call(tool, args); !failed || !strings.Contains(text, protected) || strings.Contains(text, secret) {
				t.Errorf("%s %s = %v, %q", tool, path, failed, text)
			}
		}
	}
	for _, path := range []string{fx.root, cache} {
		if text, failed := fx.call("fs.grep", map[string]any{"path": path, "pattern": "fixture-value-"}); failed || !strings.Contains(text, "fixture-value-public") || strings.Contains(text, secret) {
			t.Errorf("directory search %s = %v, %q", path, failed, text)
		}
	}
	for _, dir := range []string{filepath.Dir(current), filepath.Dir(legacy)} {
		if text, failed := fx.call("fs.list", map[string]any{"path": dir}); !failed || !strings.Contains(text, protected) {
			t.Errorf("fs.list %s = %v, %q", dir, failed, text)
		}
	}

	for _, authority := range []wsfs.Authority{wsfs.AuthorityWorkdirWrite, wsfs.AuthorityFull} {
		s := NewServerWithOptions(ServerOptions{
			Root: fx.root, Version: "test", Env: []string{}, Conversation: "bound", ExecCutover: wsagent.BackendWerk,
			FileWrites: &FileWriteSession{Root: fx.root, Authority: authority, Record: func(context.Context, FileWriteCall) error {
				t.Error("protected file reached mutation intent recording")
				return nil
			}},
		})
		for _, path := range []string{current, legacy, currentAlias, legacyAlias} {
			if text, failed := rawCorrelatedToolCall(t, s, "fs.write", "write", map[string]any{"path": path, "content": "replacement"}); !failed || !strings.Contains(text, protected) || strings.Contains(text, secret) {
				t.Errorf("%s fs.write %s = %v, %q", authority, path, failed, text)
			}
			args := map[string]any{"path": path, "edits": []map[string]string{{"oldText": "fixture-value-", "newText": "replacement"}}}
			if text, failed := rawCorrelatedToolCall(t, s, "fs.edit", "edit", args); !failed || !strings.Contains(text, protected) || strings.Contains(text, secret) {
				t.Errorf("%s fs.edit %s = %v, %q", authority, path, failed, text)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != secret+"\n" {
				t.Errorf("read %s = %q, %v", path, data, err)
			}
		}
	}

	t.Run("symlinked credential parent", func(t *testing.T) {
		alias := filepath.Join(fx.root, "credential-directory")
		if err := os.Symlink(filepath.Dir(current), alias); err != nil {
			t.Skipf("directory symlinks unavailable: %v", err)
		}
		if text, failed := fx.call("fs.read", map[string]any{"path": filepath.Join(alias, wscred.FileName)}); !failed || !strings.Contains(text, protected) || strings.Contains(text, secret) {
			t.Fatalf("symlink read = %v, %q", failed, text)
		}
		s := NewServerWithOptions(ServerOptions{
			Root: fx.root, Version: "test", Env: []string{}, Conversation: "bound", ExecCutover: wsagent.BackendWerk,
			FileWrites: &FileWriteSession{Root: fx.root, Authority: wsfs.AuthorityFull, Record: func(context.Context, FileWriteCall) error {
				t.Error("protected file reached mutation intent recording")
				return nil
			}},
		})
		path := filepath.Join(alias, "new", "file.txt")
		if text, failed := rawCorrelatedToolCall(t, s, "fs.write", "write", map[string]any{"path": path, "content": "replacement"}); !failed || !strings.Contains(text, protected) {
			t.Fatalf("symlink write = %v, %q", failed, text)
		}
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Errorf("created a directory under the credential store: %v", err)
		}
	})
}

func TestFileEditRechecksCredentialIdentityAfterIntent(t *testing.T) {
	root := t.TempDir()
	t.Setenv(wsstate.EnvCacheHome, t.TempDir())
	credential, err := wscred.DefaultPath("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(credential), 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "fixture-value-private"
	mustWrite(t, root, "file.txt", secret)
	linked := false
	s := NewServerWithOptions(ServerOptions{
		Root: root, Version: "test", Env: []string{}, Conversation: "bound", ExecCutover: wsagent.BackendWerk,
		FileWrites: &FileWriteSession{Root: root, Authority: wsfs.AuthorityFull, Record: func(context.Context, FileWriteCall) error {
			err := os.Link(filepath.Join(root, "file.txt"), credential)
			linked = err == nil
			return err
		}},
	})
	args := map[string]any{"path": "file.txt", "edits": []map[string]string{{"oldText": "fixture-value-", "newText": "replacement"}}}
	if text, failed := rawCorrelatedToolCall(t, s, "fs.edit", "edit", args); !linked || !failed || !strings.Contains(text, "the provider credential store is protected") || strings.Contains(text, secret) {
		t.Fatalf("edit with linked credential = linked %v, failed %v, %q", linked, failed, text)
	}
	if data, err := os.ReadFile(credential); err != nil || string(data) != secret {
		t.Fatalf("read linked credential = %q, %v", data, err)
	}
}
