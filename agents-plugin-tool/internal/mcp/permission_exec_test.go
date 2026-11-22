package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
)

// A worker's hosted session (a Codex, Claude opt-in or agy cutover worker
// all attach the same way) carries its authority in the connection's
// environment (wsagent.EnvPermissionLevel), which the exec gate reads.

func TestReadOnlyWorkerExecAllowlist(t *testing.T) {
	fx := newHostedExecFixture(t, 10*time.Second)
	t.Setenv(wsagent.EnvPermissionLevel, string(wsagent.PermissionReadOnly))
	pwned := filepath.Join(fx.root, "pwned")

	refused := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"exec.run", map[string]any{"exe": "touch", "args": []string{"pwned"}}, "not an allowed read command"},
		{"exec.run", map[string]any{"exe": "git", "args": []string{"commit", "-m", "x"}}, "not an allowed read subcommand"},
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
		text, isErr := fx.call(c.tool, c.args)
		if !isErr || !strings.Contains(text, c.want) || !strings.Contains(text, "permission denied") {
			t.Errorf("%s %v = %v %q, want a permission refusal naming %q", c.tool, c.args, isErr, text, c.want)
		}
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("a refused launch wrote a file")
	}

	// Read-shaped launches pass through the hosted admission and run.
	text, isErr := fx.call("exec.run", map[string]any{"exe": "git", "args": []string{"status", "--short"}})
	if isErr || !strings.Contains(text, "status: exited 0") || !strings.Contains(text, "README.md") {
		t.Fatalf("read-only exec.run git status = %v %s", isErr, text)
	}
	text, isErr = fx.call("exec.shell", map[string]any{"cmd": "git ls-files -o '*.md' 2>&1 | head -n 5"})
	if isErr || !strings.Contains(text, "status: exited 0") || !strings.Contains(text, "README.md") {
		t.Fatalf("read-only exec.shell pipeline = %v %s", isErr, text)
	}
	// The process receives exactly the checked vector: no glob, variable
	// or word-splitting expansion happens.
	text, isErr = fx.call("exec.run", map[string]any{"exe": "echo", "args": []string{"*.md", "$HOME", "a  b"}})
	if isErr || !strings.Contains(text, "*.md $HOME a  b\n") {
		t.Fatalf("exec.run echo = %v %s", isErr, text)
	}
	text, isErr = fx.call("exec.shell", map[string]any{"cmd": `echo '*.md' "$x" ` + "'a  b'"})
	if !isErr {
		t.Fatalf("a $ inside double quotes passed: %s", text)
	}
	text, isErr = fx.call("exec.shell", map[string]any{"cmd": `echo '*.md' '$HOME' "a  b"`})
	if isErr || !strings.Contains(text, "*.md $HOME a  b\n") {
		t.Fatalf("exec.shell echo = %v %s", isErr, text)
	}
}

// An inherited BASH_ENV, ENV, GIT_EXTERNAL_DIFF, GIT_PAGER or PAGER does not
// reach a read-only launch or run startup code.
func TestReadOnlyWorkerExecEnvironmentIsNotInherited(t *testing.T) {
	fx := newHostedExecFixture(t, 10*time.Second)
	startup := filepath.Join(t.TempDir(), "startup.sh")
	marker := filepath.Join(fx.root, "startup-ran")
	if err := os.WriteFile(startup, []byte("touch '"+marker+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"BASH_ENV", "ENV"} {
		t.Setenv(name, startup)
	}
	t.Setenv("GIT_EXTERNAL_DIFF", startup)
	t.Setenv("GIT_PAGER", "touch "+marker)
	t.Setenv("PAGER", "touch "+marker)
	t.Setenv(wsagent.EnvPermissionLevel, string(wsagent.PermissionReadOnly))

	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"exec.shell", map[string]any{"cmd": "echo hi"}},
		{"exec.shell", map[string]any{"cmd": "git status | cat"}},
		{"exec.run", map[string]any{"exe": "git", "args": []string{"log", "-n", "1"}}},
	} {
		if text, isErr := fx.call(c.tool, c.args); isErr && !strings.Contains(text, "does not have any commits") {
			t.Fatalf("%s %v = %s", c.tool, c.args, text)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an inherited startup variable ran code in a read-only launch")
	}

	// The same inherited environment does run in an unrestricted launch
	// (when the shell is bash), which proves the check above can fail.
	t.Setenv(wsagent.EnvPermissionLevel, string(wsagent.PermissionWorkdirWrite))
	if text, isErr := fx.call("exec.shell", map[string]any{"cmd": "echo hi"}); isErr || !strings.Contains(text, "hi") {
		t.Fatalf("workdir-write shell = %v %s", isErr, text)
	}
	if _, err := os.Stat(marker); err != nil {
		if _, bashErr := os.Stat("/bin/bash"); bashErr == nil {
			t.Logf("control launch did not run BASH_ENV: %v", err)
		}
	}
}

// workdir-write and full workers, and a lead, keep unrestricted launches.
func TestNonReadOnlyExecLaunchesAreUnrestricted(t *testing.T) {
	for _, level := range []string{string(wsagent.PermissionWorkdirWrite), string(wsagent.PermissionFull), ""} {
		t.Run("level="+level, func(t *testing.T) {
			fx := newHostedExecFixture(t, 10*time.Second)
			t.Setenv(wsagent.EnvPermissionLevel, level)
			text, isErr := fx.call("exec.shell", map[string]any{"cmd": "echo one > one.txt; touch two.txt"})
			if isErr || !strings.Contains(text, "status: exited 0") {
				t.Fatalf("exec.shell write = %v %s", isErr, text)
			}
			text, isErr = fx.call("exec.run", map[string]any{"exe": "touch", "args": []string{"three.txt"}})
			if isErr || !strings.Contains(text, "status: exited 0") {
				t.Fatalf("exec.run write = %v %s", isErr, text)
			}
			for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
				if _, err := os.Stat(filepath.Join(fx.root, name)); err != nil {
					t.Errorf("%s not written: %v", name, err)
				}
			}
			text, isErr = fx.call("exec.spawn", map[string]any{"cmd": "touch", "args": []string{"four.txt"}})
			if isErr {
				t.Fatalf("exec.spawn = %s", text)
			}
		})
	}
}
