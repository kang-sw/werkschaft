package mcp

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsfs"
)

// A hosted worker's connection (a Codex or Claude cutover worker attaches
// the same way) is bound to a non-lead conversation, and its authority is
// the hello env's wsagent.EnvPermissionLevel. These tests build that
// binding: a ServerOptions with the worker's own Env, Conversation and
// ExecCutover, over the real-holder fixture.

type workerFixture struct {
	*hostedExecFixture
	worker *Server
	key    string
}

func (w *workerFixture) call(name string, args map[string]any) (string, bool) {
	w.t.Helper()
	return callExecTool(w.t, w.worker, w.key, name, args)
}

// newWorkerFixture binds a worker server of harness at level. extraEnv is the
// worker harness's own environment on top of the test process's.
func newWorkerFixture(t *testing.T, harness string, level wsagent.PermissionLevel, extraEnv ...string) *workerFixture {
	t.Helper()
	fx := newHostedExecFixture(t, 10*time.Second)
	// The worker's level is its own, whatever level the test process runs at.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, wsagent.EnvPermissionLevel+"=") {
			env = append(env, kv)
		}
	}
	env = append(env, extraEnv...)
	if level != "" {
		env = append(env, wsagent.EnvPermissionLevel+"="+string(level))
	}
	worker := NewServerWithOptions(ServerOptions{Root: fx.root, Version: "test", Env: env, Harness: harness, Conversation: "worker", ExecCutover: harness, Exec: func() *execjob.Hosted { return fx.hosted }})
	key, _ := parseLoginResponse(t, callLogin(t, worker, 900002, fx.root, nil))
	return &workerFixture{hostedExecFixture: fx, worker: worker, key: key}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestReadOnlyWorkerExecAllowlist(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		t.Run(harness, func(t *testing.T) {
			w := newWorkerFixture(t, harness, wsagent.PermissionReadOnly)
			gitIn(t, w.root, "checkout", "-q", "-b", "topic")
			gitIn(t, w.root, "add", ".")
			gitIn(t, w.root, "commit", "-q", "-m", "first commit on topic")
			pwned := filepath.Join(w.root, "pwned")

			refused := []struct {
				tool string
				args map[string]any
				want string
			}{
				{"exec.run", map[string]any{"exe": "touch", "args": []string{"pwned"}}, "not an allowed read command"},
				{"exec.run", map[string]any{"exe": "git", "args": []string{"commit", "-m", "x"}}, "not an allowed read subcommand"},
				{"exec.run", map[string]any{"exe": "git", "args": []string{"grep", "-nO", "touch pwned", "README"}}, "writes a file or runs a program"},
				{"exec.run", map[string]any{"exe": "git", "args": []string{"status"}, "env": map[string]any{"GIT_PAGER": "touch pwned"}}, "environment override"},
				{"exec.shell", map[string]any{"cmd": "echo x > pwned"}, "only 2>&1"},
				{"exec.shell", map[string]any{"cmd": "git status; touch pwned"}, "command list"},
				{"exec.shell", map[string]any{"cmd": "echo $(touch pwned)"}, "unquoted $"},
				{"exec.shell", map[string]any{"cmd": "ls *"}, "unquoted glob"},
				{"exec.shell", map[string]any{"cmd": "sed -i s/x/y/ README.md"}, "sed"},
				{"exec.shell", map[string]any{"command": "touch pwned"}, "not checked"},
				{"exec.shell", map[string]any{"command": "touch pwned", "shell": "sh"}, "not checked"},
				{"exec.spawn", map[string]any{"cmd": "touch", "args": []string{"pwned"}}, "not checked"},
			}
			for _, c := range refused {
				text, isErr := w.call(c.tool, c.args)
				if !isErr || !strings.Contains(text, c.want) || !strings.Contains(text, "permission denied") {
					t.Errorf("%s %v = %v %q, want a permission refusal naming %q", c.tool, c.args, isErr, text, c.want)
				}
			}
			if _, err := os.Stat(pwned); err == nil {
				t.Fatal("a refused launch wrote a file")
			}

			// Read-shaped launches pass through the hosted admission and run.
			text, isErr := w.call("exec.run", map[string]any{"exe": "git", "args": []string{"log", "--oneline", "-n", "1"}})
			if isErr || !strings.Contains(text, "status: exited 0") || !strings.Contains(text, "first commit on topic") {
				t.Fatalf("read-only exec.run git log = %v %s", isErr, text)
			}
			text, isErr = w.call("exec.shell", map[string]any{"cmd": "git ls-files '*.md' 2>&1 | head -n 5"})
			if isErr || !strings.Contains(text, "status: exited 0") || !strings.Contains(text, "README.md") {
				t.Fatalf("read-only exec.shell pipeline = %v %s", isErr, text)
			}
			// The process receives exactly the checked vector: no glob,
			// variable or word-splitting expansion happens.
			text, isErr = w.call("exec.run", map[string]any{"exe": "echo", "args": []string{"*.md", "$HOME", "a  b"}})
			if isErr || !strings.Contains(text, "*.md $HOME a  b\n") {
				t.Fatalf("exec.run echo = %v %s", isErr, text)
			}
			// A backslash and a space survive intact; on Windows a double
			// quote and a leading @ are refused (the MSYS runtime parses
			// the command line again).
			text, isErr = w.call("exec.run", map[string]any{"exe": "echo", "args": []string{`a\ b`}})
			if isErr || !strings.Contains(text, "a\\ b\n") {
				t.Fatalf("exec.run echo a\\ b = %v %s", isErr, text)
			}
			for _, word := range []string{`a"b`, "@rsp", `e f\`, "it's", "a\nb", "a\rb"} {
				text, isErr = w.call("exec.run", map[string]any{"exe": "echo", "args": []string{word}})
				if goruntime.GOOS == "windows" && (!isErr || !strings.Contains(text, "Windows")) {
					t.Errorf("exec.run echo %q on Windows = %v %s, want a refusal", word, isErr, text)
				}
			}
			text, isErr = w.call("exec.shell", map[string]any{"cmd": `echo '*.md' '$HOME' "a  b"`})
			if isErr || !strings.Contains(text, "*.md $HOME a  b\n") {
				t.Fatalf("exec.shell echo = %v %s", isErr, text)
			}
			if text, isErr = w.call("exec.shell", map[string]any{"cmd": `echo "$x"`}); !isErr {
				t.Fatalf("a $ inside double quotes passed: %s", text)
			}
		})
	}
}

// probeScript prints its argv one per line, then its environment, so a test
// reads what the child actually received. It stands in for an allowed
// program (nl) first on the PATH.
const probeScript = "#!/bin/sh\nprintf 'ARG:%s\\n' \"$@\"\n/usr/bin/env | sed 's/^/ENV:/'\n"

// The child's actual argv and environment: the allowlisted environment with
// the read-only settings, none of the worker harness's startup or helper
// variables, for both launch forms.
func TestReadOnlyWorkerChildArgvAndEnvironment(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("the probe is a POSIX script; the Windows argv assertion is not covered here")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "nl"), []byte(probeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	startup := filepath.Join(t.TempDir(), "startup.sh")
	marker := filepath.Join(t.TempDir(), "startup-ran")
	if err := os.WriteFile(startup, []byte("touch '"+marker+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := bin + string(os.PathListSeparator) + os.Getenv("PATH")
	w := newWorkerFixture(t, "codex", wsagent.PermissionReadOnly,
		"PATH="+path, "BASH_ENV="+startup, "ENV="+startup, "GIT_EXTERNAL_DIFF="+startup, "GIT_PAGER=touch "+marker, "PAGER=touch "+marker,
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.pager", "GIT_CONFIG_VALUE_0=touch "+marker, "WS_TEST_LEAK_SENTINEL=secret")
	for name, call := range map[string]func() (string, bool){
		"run": func() (string, bool) {
			return w.call("exec.run", map[string]any{"exe": "nl", "args": []string{"*.md", "$HOME", "a  b", "it's"}})
		},
		"shell": func() (string, bool) {
			return w.call("exec.shell", map[string]any{"cmd": `nl '*.md' '$HOME' "a  b" "it's"`})
		},
	} {
		text, isErr := call()
		if isErr || !strings.Contains(text, "status: exited 0") {
			t.Fatalf("%s: %v %s", name, isErr, text)
		}
		for _, want := range []string{"ARG:*.md\n", "ARG:$HOME\n", "ARG:a  b\n", "ARG:it's\n", "ENV:GIT_PAGER=cat\n", "ENV:PAGER=cat\n", "ENV:GIT_OPTIONAL_LOCKS=0\n", "ENV:GIT_NO_LAZY_FETCH=1\n",
			"ENV:GIT_CONFIG_COUNT=2\n", "ENV:GIT_CONFIG_KEY_0=diff.autoRefreshIndex\n", "ENV:GIT_CONFIG_VALUE_0=false\n", "ENV:GIT_CONFIG_KEY_1=log.diffMerges\n", "ENV:GIT_CONFIG_VALUE_1=separate\n", "ENV:PATH="} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: child output lacks %q:\n%s", name, want, text)
			}
		}
		for _, bad := range []string{"ENV:BASH_ENV", "ENV:ENV=", "ENV:GIT_EXTERNAL_DIFF", "touch", "ENV:LD_PRELOAD", "ENV:WS_TEST_LEAK_SENTINEL", "core.pager", "ENV:WS_MCP"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: the child received %q:\n%s", name, bad, text)
			}
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an inherited startup variable ran code in a read-only launch")
	}
}

// A read-only git diff and git status leave a stat-stale index byte for
// byte as it was (diff refreshes it by default).
func TestReadOnlyWorkerGitReadsDoNotRewriteTheIndex(t *testing.T) {
	w := newWorkerFixture(t, "claude", wsagent.PermissionReadOnly)
	gitIn(t, w.root, "add", ".")
	gitIn(t, w.root, "commit", "-q", "-m", "c")
	// Same content, new mtime: the cached stat data is stale.
	later := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(w.root, "README.md"), later, later); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(w.root, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"diff"}, {"status", "--short"}, {"diff", "--stat", "HEAD"}} {
		if text, isErr := w.call("exec.run", map[string]any{"exe": "git", "args": args}); isErr || !strings.Contains(text, "status: exited 0") {
			t.Fatalf("git %v = %v %s", args, isErr, text)
		}
	}
	for _, args := range [][]string{{"describe", "--dirty"}, {"describe", "--broken"}} {
		if text, isErr := w.call("exec.run", map[string]any{"exe": "git", "args": args}); !isErr || !strings.Contains(text, "refreshes the index") {
			t.Fatalf("git %v = %v %s", args, isErr, text)
		}
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a read-only git read rewrote the index")
	}
}

// workdir-write and full workers, and a lead, keep unrestricted launches.
func TestNonReadOnlyExecLaunchesAreUnrestricted(t *testing.T) {
	for _, level := range []wsagent.PermissionLevel{wsagent.PermissionWorkdirWrite, wsagent.PermissionFull} {
		t.Run("worker/"+string(level), func(t *testing.T) {
			w := newWorkerFixture(t, "codex", level)
			assertUnrestricted(t, w.root, w.call)
		})
	}
	t.Run("lead", func(t *testing.T) {
		fx := newHostedExecFixture(t, 10*time.Second)
		assertUnrestricted(t, fx.root, fx.call)
	})
}

func assertUnrestricted(t *testing.T, root string, call func(string, map[string]any) (string, bool)) {
	t.Helper()
	text, isErr := call("exec.shell", map[string]any{"cmd": "echo one > one.txt; touch two.txt"})
	if isErr || !strings.Contains(text, "status: exited 0") {
		t.Fatalf("exec.shell write = %v %s", isErr, text)
	}
	text, isErr = call("exec.run", map[string]any{"exe": "touch", "args": []string{"three.txt"}})
	if isErr || !strings.Contains(text, "status: exited 0") {
		t.Fatalf("exec.run write = %v %s", isErr, text)
	}
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
	if text, isErr = call("exec.spawn", map[string]any{"cmd": "touch", "args": []string{"four.txt"}}); isErr {
		t.Fatalf("exec.spawn = %s", text)
	}
}

// A repository that configures log.diffMerges=remerge cannot make -m or
// --diff-merges=on write the reconstructed merge into .git/objects.
func TestReadOnlyWorkerMergeDiffWritesNoObjects(t *testing.T) {
	w := newWorkerFixture(t, "codex", wsagent.PermissionReadOnly)
	gitIn(t, w.root, "checkout", "-q", "-b", "main")
	gitIn(t, w.root, "add", ".")
	gitIn(t, w.root, "commit", "-q", "-m", "base")
	writeReadme := func(text string) {
		if err := os.WriteFile(filepath.Join(w.root, "README.md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, w.root, "checkout", "-q", "-b", "side")
	writeReadme("side\n")
	gitIn(t, w.root, "commit", "-q", "-am", "side")
	gitIn(t, w.root, "checkout", "-q", "main")
	writeReadme("main\n")
	gitIn(t, w.root, "commit", "-q", "-am", "main")
	// A conflicting merge, resolved by hand: its remerge diff is not empty.
	cmd := exec.Command("git", "-c", "user.email=t@t", "-c", "user.name=t", "merge", "--no-ff", "-m", "merge", "side")
	cmd.Dir = w.root
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("the merge did not conflict:\n%s", out)
	}
	writeReadme("resolved\n")
	gitIn(t, w.root, "commit", "-q", "-am", "merge")
	gitIn(t, w.root, "config", "log.diffMerges", "remerge")

	snapshot := func() []string {
		var names []string
		_ = filepath.WalkDir(filepath.Join(w.root, ".git", "objects"), func(path string, d os.DirEntry, err error) error {
			if err == nil {
				names = append(names, path)
			}
			return nil
		})
		return names
	}
	before := snapshot()
	for _, args := range [][]string{{"log", "-m", "-p", "-n", "1"}, {"show", "--diff-merges=on", "HEAD"}, {"show", "--diff-merges=m", "HEAD"}, {"log", "-m", "--stat", "-n", "1"}} {
		if text, isErr := w.call("exec.run", map[string]any{"exe": "git", "args": args}); isErr || !strings.Contains(text, "status: exited 0") || !strings.Contains(text, "merge") {
			t.Fatalf("git %v = %v %s", args, isErr, text)
		} else if strings.Contains(text, "remerge") {
			// The final object directory can look unchanged because git
			// removes its temporary one, so the format itself is asserted.
			t.Fatalf("git %v used the repository's remerge format:\n%s", args, text)
		}
	}
	if after := snapshot(); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("a read-only merge diff changed .git/objects:\nbefore %v\nafter  %v", before, after)
	}
}

// werkHelloEnvWorker binds a read-only werk worker whose Env is shaped like
// the hello a ProcessDurable session publishes (wsagent mcpHello): the WS_*
// entries plus the daemon's PATH and HOME (the fixed behavior; Started(nil, 0)
// gave the hello none of them).
func werkHelloEnvWorker(t *testing.T) *workerFixture {
	t.Helper()
	fx := newHostedExecFixture(t, 10*time.Second)
	env := []string{
		"WS_MCP_HARNESS=" + wsagent.BackendWerk,
		wsagent.EnvPermissionLevel + "=" + string(wsagent.PermissionReadOnly),
		"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(),
	}
	worker := NewServerWithOptions(ServerOptions{Root: fx.root, Version: "test", Env: env, Harness: wsagent.BackendWerk, Conversation: "worker", ExecCutover: wsagent.BackendWerk, Exec: func() *execjob.Hosted { return fx.hosted }})
	key, _ := parseLoginResponse(t, callLogin(t, worker, 900003, fx.root, nil))
	worker.callerBinding = boundCallers{backends: map[string]string{"worker": wsagent.BackendWerk}}
	worker.fileWrites = &FileWriteSession{
		Root: fx.root, Cwd: fx.root, Authority: wsfs.AuthorityReadOnly, LiveAuthority: false,
		CurrentAuthority: func() wsfs.Authority { return wsfs.AuthorityReadOnly },
	}
	worker.recordExecLaunch = func(context.Context, execjob.ModelLaunch) (bool, error) { return false, nil }
	return &workerFixture{hostedExecFixture: fx, worker: worker, key: key}
}

// TestWerkReadOnlyWorkerResolvesGitFromHelloEnv pins both halves of the
// durable-session environment contract: a hello env that carries the
// daemon's PATH resolves git for git.status and the allowlisted exec.run,
// and one without PATH fails with a message naming the unset PATH rather than
// borrowing the test (daemon) process's own.
func TestWerkReadOnlyWorkerResolvesGitFromHelloEnv(t *testing.T) {
	t.Run("hello env with PATH", func(t *testing.T) {
		w := werkHelloEnvWorker(t)
		gitIn(t, w.root, "add", ".")
		gitIn(t, w.root, "commit", "-q", "-m", "werk hello commit")
		if text, failed := w.call("git.status", nil); failed {
			t.Fatalf("git.status = %s", text)
		}
		text, failed := rawCorrelatedToolCall(t, w.worker, "exec.run", "werk-env", map[string]any{
			"session_key": w.key, "exe": "git", "args": []string{"log", "--oneline", "-n", "1"},
		})
		if failed || !strings.Contains(text, "status: exited 0") || !strings.Contains(text, "werk hello commit") {
			t.Fatalf("exec.run git log = %v %s", failed, text)
		}
	})
	t.Run("hello env without PATH", func(t *testing.T) {
		// The hello env the bug produced: WS_* entries only. Even the login's
		// own worktree probe needs git, so the failure shows up there, and it
		// must name the environment rather than "executable file not found".
		fx := newHostedExecFixture(t, 10*time.Second)
		worker := NewServerWithOptions(ServerOptions{Root: fx.root, Version: "test", Harness: wsagent.BackendWerk, Conversation: "worker", ExecCutover: wsagent.BackendWerk,
			Env:  []string{"WS_MCP_HARNESS=" + wsagent.BackendWerk, wsagent.EnvPermissionLevel + "=" + string(wsagent.PermissionReadOnly)},
			Exec: func() *execjob.Hosted { return fx.hosted }})
		resp := callLogin(t, worker, 900004, fx.root, nil)
		if !strings.Contains(resp, "PATH is unset or empty") || strings.Contains(resp, "not found in $PATH") {
			t.Fatalf("login without PATH = %s, want a message naming the unset PATH", resp)
		}
	})
}

func TestWerkReadOnlyLeadFailClosedAndWorkerAllowlistParity(t *testing.T) {
	t.Run("read-only werk lead cannot launch or write exec", func(t *testing.T) {
		w := newWorkerFixture(t, wsagent.BackendWerk, wsagent.PermissionReadOnly)
		w.worker.callerBinding = boundCallers{backends: map[string]string{"worker": wsagent.BackendWerk}}
		w.worker.fileWrites = &FileWriteSession{
			Root: w.root, Cwd: w.root, Authority: wsfs.AuthorityReadOnly, LiveAuthority: true,
			CurrentAuthority: func() wsfs.Authority { return wsfs.AuthorityReadOnly },
		}
		calls := []struct {
			name string
			args map[string]any
		}{
			{"exec.run", map[string]any{"exe": "git", "args": []string{"status"}}},
			{"exec.shell", map[string]any{"cmd": "git status"}},
			{"exec.spawn", map[string]any{"cmd": "git", "args": []string{"status"}}},
			{"exec.write", map[string]any{"exec_id": "fixture", "data": ""}},
		}
		for _, cutover := range []string{wsagent.BackendWerk, ""} {
			w.worker.execCutover = cutover
			names := listedNames(w.worker)
			for _, call := range calls {
				if slices.Contains(names, call.name) || slices.Contains(names, strings.ReplaceAll(call.name, ".", "_")) {
					t.Errorf("read-only lead listing includes %s", call.name)
				}
				text, failed := w.call(call.name, call.args)
				if !failed || !strings.Contains(text, "this read-only session cannot start or send input") {
					t.Errorf("cutover %q, %s = %v %q", cutover, call.name, failed, text)
				}
			}
		}
	})

	t.Run("read-only werk worker gets allowlist exec parity", func(t *testing.T) {
		w := newWorkerFixture(t, wsagent.BackendWerk, wsagent.PermissionReadOnly)
		gitIn(t, w.root, "add", ".")
		gitIn(t, w.root, "commit", "-q", "-m", "werk worker commit")
		recorded := 0
		w.worker.callerBinding = boundCallers{backends: map[string]string{"worker": wsagent.BackendWerk}}
		w.worker.fileWrites = &FileWriteSession{
			Root: w.root, Cwd: w.root, Authority: wsfs.AuthorityReadOnly, LiveAuthority: false,
			CurrentAuthority: func() wsfs.Authority { return wsfs.AuthorityReadOnly },
		}
		w.worker.recordExecLaunch = func(_ context.Context, _ execjob.ModelLaunch) (bool, error) {
			recorded++
			return false, nil
		}
		names := listedNames(w.worker)
		for _, want := range []string{"exec.run", "exec.shell"} {
			if !slices.Contains(names, want) {
				t.Fatalf("read-only werk worker listing misses %s: %v", want, names)
			}
		}

		callCorrelated := func(tool string, args map[string]any) (string, bool) {
			t.Helper()
			withKey := map[string]any{"session_key": w.key}
			for k, v := range args {
				withKey[k] = v
			}
			return rawCorrelatedToolCall(t, w.worker, tool, "werk-call", withKey)
		}

		text, failed := callCorrelated("exec.run", map[string]any{"exe": "git", "args": []string{"log", "--oneline", "-n", "1"}})
		if failed || !strings.Contains(text, "status: exited 0") || !strings.Contains(text, "werk worker commit") {
			t.Fatalf("read-only werk worker exec.run git log = %v %s", failed, text)
		}
		text, failed = callCorrelated("exec.shell", map[string]any{"cmd": "git status --short"})
		if failed || !strings.Contains(text, "status: exited 0") {
			t.Fatalf("read-only werk worker exec.shell git status = %v %s", failed, text)
		}
		if recorded < 2 {
			t.Fatalf("read-only werk worker recorded %d launches, want >= 2", recorded)
		}

		pwned := filepath.Join(w.root, "pwned")
		for _, tc := range []struct {
			tool string
			args map[string]any
			want string
		}{
			{"exec.run", map[string]any{"exe": "touch", "args": []string{"pwned"}}, "not an allowed read command"},
			{"exec.run", map[string]any{"exe": "git", "args": []string{"commit", "-m", "x"}}, "not an allowed read subcommand"},
			{"exec.shell", map[string]any{"cmd": "echo x > pwned"}, "only 2>&1"},
			{"exec.shell", map[string]any{"command": "touch pwned"}, "not checked"},
			{"exec.spawn", map[string]any{"cmd": "touch", "args": []string{"pwned"}}, "not checked"},
		} {
			text, failed := callCorrelated(tc.tool, tc.args)
			if !failed || !strings.Contains(text, tc.want) || !strings.Contains(text, "permission denied") {
				t.Errorf("read-only werk worker %s %v = %v %q, want refusal naming %q", tc.tool, tc.args, failed, text, tc.want)
			}
		}
		if _, err := os.Stat(pwned); err == nil {
			t.Fatal("a refused read-only werk worker launch wrote a file")
		}
	})
}
