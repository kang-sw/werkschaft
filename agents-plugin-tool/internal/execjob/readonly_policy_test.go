package execjob

import (
	"os"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsgit"
)

func readOnlyLine(t *testing.T, line string) (HostedLaunch, error) {
	t.Helper()
	return ReadOnlyShellLaunch("w", "exec.shell", t.TempDir(), "", line, os.Environ(), nil)
}

func TestReadOnlyShellLineAllowed(t *testing.T) {
	for _, line := range []string{
		"git status",
		"git --no-pager log --oneline -n 5",
		"git log -n 3 2>&1",
		"git diff --stat HEAD~1 | head -n 20",
		"git ls-files '*.go' | wc -l",
		"find . -name '*.go' -type f | sort | uniq -c",
		"rg -g '*.go' 'func main' 2>/dev/null",
		"sed -n '10,40p' README.md",
		"sed -n '$p' README.md",
		"sort -r names.txt",
		"uniq names.txt",
		"ls -la >/dev/null",
		`echo "a b" 'c;d' e\;f`,
		"cat README.md | grep -n x | cut -d: -f1",
		"git log --no-textconv -1",
		"git log --no-ext-diff -1",
		"jq . file.json",
	} {
		if _, err := readOnlyLine(t, line); err != nil {
			t.Errorf("%q refused: %v", line, err)
		}
	}
}

func TestReadOnlyShellLineRefused(t *testing.T) {
	for _, line := range []string{
		"touch x", "rm -rf x", "mv a b", "cp a b", "tee x", "awk '{print}'", "python3 -c 1", "go build ./...", "npm i", "vim x",
		"git status; touch x", "git status && touch x", "git status || touch x", "git status &", "git status\ntouch x", "git status & touch x",
		"echo $(touch x)", "echo `touch x`", "cat <(ls)", "cat >(tee x)", "echo $HOME", `echo "$HOME"`, `echo "$(ls)"`, "echo ${HOME}",
		"echo hi > x", "echo hi >> x", "echo hi 1>x", "echo hi &> x", "echo hi >&2", "ls 2>x", "cat <<EOF\nhi\nEOF", "cat < x", "cat <<< hi",
		"FOO=bar git status", "GIT_EXTERNAL_DIFF=x git diff", "(git status)", "{ git status; }", "git status |& cat", "| cat", "git status |",
		"git -c core.pager=x log", "git --config-env=a=b log", "git commit -m x", "git push", "git checkout x", "git config x y", "git status --output=x",
		"git log --output=x", "git diff --output x", "git diff --ext-diff", "git diff --ext", "git log -Ofile", "git grep --open-files-in-pager=x y", "git grep -O y",
		"git show --textconv HEAD:x", "git cat-file --filters HEAD:x", "git cat-file --textconv HEAD:x", "git log --textconv", "git diff --out=x", "git log --o=x",
		"find . -exec rm {} +", "find . -execdir x", "find . -ok x", "find . -okdir x", "find . -delete", "find . -fprint x", "find . -fprintf x y", "find . -fls x",
		"sort -o x y", "sort --output=x", "sort -ro x", "sort --compress-program=sh", "sort --compress=sh", "uniq a b", "uniq -c a b", "sed -i s/a/b/ f", "sed -n -i p f",
		"sed s/a/b/ f", "sed -n 's/a/b/p' f", "sed -n '1w x' f", "sed -n '1p;2p' f", "sed -n 1p f -i", "tree -o x", "tree -ao x", "file -C", "file --compile",
		"rg --pre sh x", "rg --pre-glob '*' x", "rg -z x", "rg --search-zip x", "rg -nz x",
		"/bin/ls", "./ls", "../x/git status", "ls *", "ls ?", "ls [a]", "cat ~/x", "cat ~", "echo {a,b}", "echo a # b", "echo 'unterminated", `echo "unterminated`, `echo \`,
	} {
		if _, err := readOnlyLine(t, line); err == nil {
			t.Errorf("%q passed", line)
		} else if _, ok := err.(*ErrReadOnlyLaunch); !ok {
			t.Errorf("%q: error %T %v, want a read-only refusal", line, err, err)
		}
	}
	if _, err := ReadOnlyShellLaunch("w", "exec.shell", t.TempDir(), "", "ls", os.Environ(), map[string]string{"GIT_PAGER": "x"}); err == nil || !strings.Contains(err.Error(), "environment override") {
		t.Errorf("env overlay: %v", err)
	}
}

func TestReadOnlyShellReserialization(t *testing.T) {
	environ := readOnlyEnvironment(os.Environ())
	git, err := wsgit.LookPathEnv("git", environ)
	if err != nil {
		t.Skip("git not on PATH")
	}
	head, err := wsgit.LookPathEnv("head", environ)
	if err != nil {
		t.Skip("head not on PATH")
	}
	conv := func(p string) string { return shQuote(strings.ReplaceAll(p, `\`, "/")) }
	launch, err := readOnlyLine(t, `git  log   "-n"  1 'it'\''s' 2>&1 | head -n 3 >/dev/null`)
	if err != nil {
		t.Fatal(err)
	}
	want := conv(git) + " 'log' '-n' '1' 'it'\\''s' 2>&1 | " + conv(head) + " '-n' '3' >/dev/null"
	if got := launch.Argv[len(launch.Argv)-1]; got != want {
		t.Fatalf("launched line\n got %s\nwant %s", got, want)
	}
	if launch.Argv[len(launch.Argv)-2] != "-c" {
		t.Fatalf("argv = %q", launch.Argv)
	}
	if strings.Contains(launch.Argv[0], "bash") && launch.Argv[1] != "--noprofile" {
		t.Fatalf("bash without --noprofile --norc: %q", launch.Argv)
	}
	if launch.Command != `git  log   "-n"  1 'it'\''s' 2>&1 | head -n 3 >/dev/null` {
		t.Fatalf("display line = %q", launch.Command)
	}
	// A quoted glob stays one literal word; an unquoted one is refused.
	if l, err := readOnlyLine(t, `git ls-files '*.go'`); err != nil || !strings.HasSuffix(l.Argv[len(l.Argv)-1], " 'ls-files' '*.go'") {
		t.Fatalf("quoted glob = %v %v", l.Argv, err)
	}
	if _, err := readOnlyLine(t, `git ls-files *.go`); err == nil || !strings.Contains(err.Error(), "quote the pattern") {
		t.Fatalf("unquoted glob = %v", err)
	}
}

func TestReadOnlyEnvironmentIsAllowlisted(t *testing.T) {
	in := []string{"PATH=/bin", "HOME=/h", "LC_ALL=C", "LANG=C", "BASH_ENV=/x", "ENV=/x", "GIT_EXTERNAL_DIFF=x", "GIT_PAGER=less", "PAGER=less", "LD_PRELOAD=x", "DYLD_INSERT_LIBRARIES=x", "WS_MCP_SESSION_TOKEN=t", "FOO=1"}
	got := strings.Join(readOnlyEnvironment(in), "\n")
	for _, bad := range []string{"BASH_ENV", "ENV=", "GIT_EXTERNAL_DIFF", "less", "LD_PRELOAD", "DYLD_", "WS_MCP", "FOO"} {
		if strings.Contains(got, bad) {
			t.Errorf("environment keeps %s:\n%s", bad, got)
		}
	}
	for _, want := range []string{"PATH=/bin", "HOME=/h", "LC_ALL=C", "LANG=C", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "PAGER=cat"} {
		if !strings.Contains(got, want) {
			t.Errorf("environment lacks %s:\n%s", want, got)
		}
	}
}

func TestReadOnlyArgvLaunch(t *testing.T) {
	root := t.TempDir()
	for _, argv := range [][]string{{"touch", "x"}, {"git", "commit"}, {"git", "-c", "a=b", "log"}, {"/bin/ls"}, {"sed", "-i", "x"}, {"find", ".", "-delete"}, {"sh", "-c", "ls"}, {"rg", "--pre", "sh", "x"}} {
		if _, err := ReadOnlyArgvLaunch("w", "exec.run", root, root, argv, os.Environ(), nil); err == nil {
			t.Errorf("%q passed", argv)
		}
	}
	l, err := ReadOnlyArgvLaunch("w", "exec.run", root, root, []string{"git", "log", "-n", "1", "*.go"}, append(os.Environ(), "BASH_ENV=/x"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := wsgit.LookPathEnv("git", l.Environ); l.Argv[0] != want || strings.Join(l.Argv[1:], " ") != "log -n 1 *.go" {
		t.Fatalf("argv = %q", l.Argv)
	}
	if strings.Contains(strings.Join(l.Environ, "\n"), "BASH_ENV") {
		t.Fatalf("environment keeps BASH_ENV")
	}
	if _, err := ReadOnlyArgvLaunch("w", "exec.run", root, root, []string{"ls"}, os.Environ(), map[string]string{"PATH": "/tmp"}); err == nil {
		t.Fatal("env override passed")
	}
}
