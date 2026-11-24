package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kang-sw/devenv/internal/mcpframe"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

func rawCorrelatedToolCall(t *testing.T, s *Server, name, callID string, arguments map[string]any) (string, bool) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": name, "arguments": arguments, "_meta": map[string]string{mcpframe.ToolCallIDMeta: callID}})
	if err != nil {
		t.Fatal(err)
	}
	response := s.callTool(context.Background(), request{ID: json.RawMessage("21"), Method: "tools/call", Params: body})
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return toolText(t, string(raw)), toolIsError(t, string(raw))
}

func TestFileWriteToolsRequireTrustedWerkWriteAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, conversation, backend string
		authority                   wsagent.PermissionLevel
		recorder, want              bool
	}{
		{"workdir", "bound", wsagent.BackendWerk, wsagent.PermissionWorkdirWrite, true, true},
		{"full", "bound", wsagent.BackendWerk, wsagent.PermissionFull, true, true},
		{"read-only", "bound", wsagent.BackendWerk, wsagent.PermissionReadOnly, true, false},
		{"no authority", "bound", wsagent.BackendWerk, "", true, false},
		{"no recorder", "bound", wsagent.BackendWerk, wsagent.PermissionFull, false, false},
		{"codex", "bound", wsagent.BackendCodex, wsagent.PermissionFull, true, false},
		{"unbound", "", wsagent.BackendWerk, wsagent.PermissionFull, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			binding := &FileWriteSession{Root: root, Authority: wsfs.Authority(tc.authority)}
			if tc.recorder {
				binding.Record = func(_ context.Context, call FileWriteCall) error {
					want, err := filepath.EvalSymlinks(root)
					if err != nil || call.ID != "fixture-call" || call.Tool != "fs.write" ||
						call.Intent.Target != filepath.Join(want, "test.txt") ||
						call.Intent.Prior != nil || call.Intent.Outcome() != "not applied" {
						t.Errorf("intent before write = %+v, %v", call, err)
					}
					return nil
				}
			}
			s := NewServerWithOptions(ServerOptions{
				Root: root, Version: "test", Env: []string{}, Harness: wsagent.BackendWerk,
				Conversation: tc.conversation, ExecCutover: tc.backend, FileWrites: binding,
			})
			for _, tool := range []string{"fs.edit", "fs.write"} {
				name := s.connectionDescriptor().aliases.advertised(tool)
				if listed := slices.Contains(listedNames(s), name); listed != tc.want {
					t.Errorf("%s listed = %v, want %v", tool, listed, tc.want)
				}
			}
			text, failed := rawCorrelatedToolCall(t, s, "fs.write", "fixture-call", map[string]any{"path": "test.txt", "content": "new"})
			if failed == tc.want {
				t.Fatalf("fs.write = %v, %s", failed, text)
			}
			content, err := os.ReadFile(filepath.Join(root, "test.txt"))
			if tc.want && (err != nil || string(content) != "new") || !tc.want && !os.IsNotExist(err) {
				t.Fatalf("target = %q, %v", content, err)
			}
		})
	}
}

func TestFileWriteToolsResolveWorkingDirectoryAndRefuseUnrecordedCalls(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	cwd := filepath.Join(root, "sub")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, cwd, "edit.txt", "old\n")
	binding := &FileWriteSession{Root: root, Cwd: cwd, Authority: wsfs.AuthorityWorkdirWrite}
	binding.Record = func(_ context.Context, call FileWriteCall) error {
		if call.ID != "okay" || call.Tool != "fs.edit" {
			return errors.New("no pending call")
		}
		if call.Intent.Prior == nil || call.Intent.Outcome() != "not applied" {
			t.Errorf("edit intent = %+v", call)
		}
		return nil
	}
	s := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Env: []string{}, Conversation: "bound", ExecCutover: wsagent.BackendWerk, FileWrites: binding})
	edit := map[string]any{"path": "edit.txt", "edits": []map[string]string{{"oldText": "old", "newText": "new"}}}
	for _, id := range []string{"", "unknown"} {
		if text, failed := rawCorrelatedToolCall(t, s, "fs.edit", id, edit); !failed {
			t.Fatalf("edit with call %q: %s", id, text)
		}
	}
	if text, failed := rawCorrelatedToolCall(t, s, "fs.edit", "okay", edit); failed || !strings.Contains(text, "-old") || !strings.Contains(text, "+new") {
		t.Fatalf("recorded edit = %v, %s", failed, text)
	}
	if data, err := os.ReadFile(filepath.Join(cwd, "edit.txt")); err != nil || string(data) != "new\n" {
		t.Fatalf("edited target = %q, %v", data, err)
	}
	target := filepath.Join(outside, "escaped")
	if text, failed := rawCorrelatedToolCall(t, s, "fs.write", "okay", map[string]any{"path": target, "content": "forbidden"}); !failed || !strings.Contains(text, "outside the session's worktree") {
		t.Fatalf("write outside = %v, %s", failed, text)
	}
}

func TestScopedFileToolsLeaveExternalHardLinkUnchanged(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"fs.write", map[string]any{"path": "file.txt", "content": "new"}},
		{"fs.edit", map[string]any{"path": "file.txt", "edits": []map[string]string{{"oldText": "old", "newText": "new"}}}},
	} {
		for _, timing := range []string{"existing link", "linked during intent"} {
			t.Run(tc.tool+"/"+timing, func(t *testing.T) {
				root := t.TempDir()
				outside := t.TempDir()
				mustWrite(t, root, "file.txt", "old")
				target := filepath.Join(root, "file.txt")
				alias := filepath.Join(outside, "alias.txt")
				binding := &FileWriteSession{Root: root, Authority: wsfs.AuthorityWorkdirWrite}
				binding.Record = func(context.Context, FileWriteCall) error {
					if timing == "linked during intent" {
						return os.Link(target, alias)
					}
					return nil
				}
				if timing == "existing link" {
					if err := os.Link(target, alias); err != nil {
						t.Fatal(err)
					}
				}
				s := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Env: []string{}, Conversation: "bound", ExecCutover: wsagent.BackendWerk, FileWrites: binding})
				if text, failed := rawCorrelatedToolCall(t, s, tc.tool, "fixture-call", tc.args); failed {
					t.Fatalf("%s = %v, %s", tc.tool, failed, text)
				}
				if data, err := os.ReadFile(target); err != nil || string(data) != "new" {
					t.Errorf("target = %q, %v", data, err)
				}
				if data, err := os.ReadFile(alias); err != nil || string(data) != "old" {
					t.Errorf("external alias = %q, %v", data, err)
				}
			})
		}
	}
}

func TestBoundFileToolsObservePermissionChanges(t *testing.T) {
	root := t.TempDir()
	level := wsfs.AuthorityFull
	files := &FileWriteSession{
		Root: root, Cwd: root, Authority: wsfs.AuthorityFull,
		CurrentAuthority: func() wsfs.Authority { return level },
		Record:           func(context.Context, FileWriteCall) error { return nil },
	}
	s := NewServerWithOptions(ServerOptions{
		Root: root, Version: "test", Env: []string{},
		Conversation: "bound", ExecCutover: wsagent.BackendWerk, FileWrites: files,
		CallerBinding: boundCallers{backends: map[string]string{"bound": wsagent.BackendWerk}},
	})
	if !s.fsReadTools() || !s.fileWriteTools() {
		t.Fatal("full authority did not admit daemon reads and writes")
	}
	level = wsfs.AuthorityWorkdirWrite
	if s.fileWriteTools() {
		t.Fatal("a connection admitted at full still uses that permission after a downgrade")
	}
	level = wsfs.AuthorityReadOnly
	if !s.fsReadTools() || s.fileWriteTools() || !s.refuseBoundReadOnlyExec("exec.run") {
		t.Fatal("the bound connection did not become read-only")
	}
	if text, failed := rawCorrelatedToolCall(t, s, "fs.write", "fixture-call", map[string]any{"path": "test.txt", "content": "new"}); !failed {
		t.Fatalf("the old connection accepted a direct write: %s", text)
	}
	if _, err := os.Stat(filepath.Join(root, "test.txt")); !os.IsNotExist(err) {
		t.Fatalf("read-only call touched the target: %v", err)
	}
	level = ""
	if s.fsReadTools() || s.fileWriteTools() {
		t.Fatal("revoked connection still admits filesystem tools")
	}

	// An immutable read-only worker (LiveAuthority == false) is not blocked by
	// the lead read-only no-exec gate; its launches go through the read-only
	// allowlist instead.
	worker := NewServerWithOptions(ServerOptions{
		Root: root, Version: "test", Env: []string{},
		Conversation: "worker", ExecCutover: wsagent.BackendWerk,
		FileWrites: &FileWriteSession{
			Root: root, Cwd: root, Authority: wsfs.AuthorityReadOnly,
			CurrentAuthority: func() wsfs.Authority { return wsfs.AuthorityReadOnly },
		},
		CallerBinding: boundCallers{backends: map[string]string{"worker": wsagent.BackendWerk}},
	})
	if worker.refuseBoundReadOnlyExec("exec.run") || worker.refuseBoundReadOnlyExec("exec.shell") {
		t.Fatal("an immutable read-only worker was blocked by the lead no-exec gate")
	}
}

func TestModelFileToolsKeepOperatorSessionStateReadOnly(t *testing.T) {
	fx := newFSFixture(t, wsagent.BackendWerk)
	dir, err := wsdashboard.Dir("")
	if err != nil {
		t.Fatal(err)
	}
	session := wsdashboard.SessionDir(dir, fx.s.conversation)
	fx.s.callerBinding = boundCallers{backends: map[string]string{fx.s.conversation: wsagent.BackendWerk}}
	fx.s.fileWrites = &FileWriteSession{Authority: wsfs.AuthorityReadOnly}
	f := NewServerWithOptions(ServerOptions{
		Root: fx.root, Version: "test", Env: []string{}, Conversation: "bound", ExecCutover: wsagent.BackendWerk,
		FileWrites: &FileWriteSession{Root: fx.root, Authority: wsfs.AuthorityFull,
			Record: func(context.Context, FileWriteCall) error {
				t.Error("a model write to daemon session state reached mutation admission")
				return nil
			}},
	})
	for _, path := range []string{
		filepath.Join(session, "agent.json"),
		filepath.Join(wsinbox.Dir(session), "entry.json"),
	} {
		const prior = `{"operator":false}`
		mustWrite(t, filepath.Dir(path), filepath.Base(path), prior)
		if text, failed := fx.call("fs.read", map[string]any{"path": path}); failed || !strings.Contains(text, prior) {
			t.Fatalf("read-only session state = %v, %s", failed, text)
		}
		if text, failed := rawCorrelatedToolCall(t, f, "fs.write", "write", map[string]any{"path": path, "content": `{"operator":true}`}); !failed || !strings.Contains(text, "session state is read-only") {
			t.Fatalf("session-state write = %v, %s", failed, text)
		}
		edit := map[string]any{"path": path, "edits": []map[string]string{{"oldText": "false", "newText": "true"}}}
		if text, failed := rawCorrelatedToolCall(t, f, "fs.edit", "edit", edit); !failed || !strings.Contains(text, "session state is read-only") {
			t.Fatalf("session-state edit = %v, %s", failed, text)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != prior {
			t.Fatalf("trusted state = %s, %v", got, err)
		}
	}
}

func TestModelFileWriteHoldsPostureThroughPrepareAndApply(t *testing.T) {
	for _, phase := range []string{"prepare", "apply"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "test.txt")
			var guard sync.RWMutex
			level := wsfs.AuthorityFull
			race := make(chan struct{})
			transitioning := make(chan struct{})
			downgraded := make(chan struct{})
			go func() {
				<-race
				close(transitioning)
				guard.Lock()
				level = wsfs.AuthorityWorkdirWrite
				guard.Unlock()
				close(downgraded)
			}()
			assertHeld := func() {
				if guard.TryLock() {
					guard.Unlock()
					t.Error("the file mutation did not hold the posture guard")
				}
			}
			var once sync.Once
			at := func(here string) {
				assertHeld()
				if here == phase {
					once.Do(func() {
						close(race)
						<-transitioning
					})
				}
			}
			binding := &FileWriteSession{
				Root: root, Authority: wsfs.AuthorityFull, LiveAuthority: true,
				HoldAuthority: func() func() {
					guard.RLock()
					return func() {
						if data, err := os.ReadFile(target); err != nil || string(data) != "admitted" {
							t.Errorf("file write after releasing the posture guard = %q, %v", data, err)
						}
						guard.RUnlock()
					}
				},
				CurrentAuthority: func() wsfs.Authority {
					at("prepare")
					return level
				},
				Record: func(_ context.Context, call FileWriteCall) error {
					at("apply")
					if call.Authority != wsfs.AuthorityFull || call.Intent.Outcome() != "not applied" {
						t.Errorf("intent before apply = %+v", call)
					}
					return nil
				},
			}
			s := &Server{fileWrites: binding}
			args := map[string]any{"path": target, "content": "admitted"}
			meta := map[string]any{mcpframe.ToolCallIDMeta: "mutation"}
			res := s.callFSWrite(context.Background(), json.RawMessage("21"), "fs.write", args, meta)
			<-downgraded
			if got, _ := json.Marshal(res); !strings.Contains(string(got), "created") {
				t.Fatalf("write admitted before downgrade = %s", got)
			}
		})
	}
}

func TestModelFileWriteRechecksPostureAfterOperatorGrant(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after wsfs.Authority
	}{
		{"scoped approval", wsfs.AuthorityWorkdirWrite},
		{"read-only after approval", wsfs.AuthorityReadOnly},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "test.txt")
			var guard sync.RWMutex
			level := wsfs.AuthorityWorkdirWrite
			approved, recorded := false, false
			binding := &FileWriteSession{
				Root: root, Authority: wsfs.AuthorityFull, LiveAuthority: true,
				HoldAuthority: func() func() {
					guard.RLock()
					return guard.RUnlock
				},
				CurrentAuthority: func() wsfs.Authority { return level },
				Approve: func(_ context.Context, call FileWriteCall) error {
					if !guard.TryLock() {
						t.Fatal("operator approval was awaited under the posture guard")
					}
					defer guard.Unlock()
					if call.Authority != wsfs.AuthorityWorkdirWrite {
						t.Errorf("approval authority = %s", call.Authority)
					}
					approved = true
					level = tc.after
					return nil
				},
				Record: func(_ context.Context, call FileWriteCall) error {
					recorded = true
					if !approved || call.Authority != wsfs.AuthorityWorkdirWrite {
						t.Errorf("recorded without scoped approval: %+v", call)
					}
					return nil
				},
			}
			s := &Server{fileWrites: binding}
			args := map[string]any{"path": target, "content": "admitted"}
			meta := map[string]any{mcpframe.ToolCallIDMeta: "mutation"}
			res := s.callFSWrite(context.Background(), json.RawMessage("21"), "fs.write", args, meta)
			data, err := os.ReadFile(target)
			wantWrite := tc.after == wsfs.AuthorityWorkdirWrite
			if !approved || recorded != wantWrite || (wantWrite && (err != nil || string(data) != "admitted")) ||
				(!wantWrite && !errors.Is(err, os.ErrNotExist)) {
				t.Fatalf("file after operator grant = %q, %v (approved %v, recorded %v, response %+v)", data, err, approved, recorded, res)
			}
		})
	}
}
