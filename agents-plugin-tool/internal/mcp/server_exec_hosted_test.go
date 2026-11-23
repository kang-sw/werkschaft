package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsholder"
)

// execTestSessions is a HostedSessions that allows every call by posture
// and records the notices.
type execTestSessions struct {
	mu      sync.Mutex
	notices []string
	seen    map[string]bool
}

func (s *execTestSessions) Owner(id string) (execjob.HostedOwner, error) {
	return execjob.HostedOwner{ID: id, Gen: 1, Lead: id != "worker"}, nil
}

func (s *execTestSessions) Approve(context.Context, string, execjob.HostedCall) (bool, error) {
	return false, nil
}

func (s *execTestSessions) Immediate(string) (bool, error) { return true, nil }

func (s *execTestSessions) StopNote(string) string                    { return "" }
func (s *execTestSessions) Hand(string, uint64, string, string) error { return nil }

func (s *execTestSessions) Notify(id string, gen uint64, eventID, text string) (execjob.NoticeOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[eventID] {
		return execjob.NoticeDuplicate, nil
	}
	s.seen[eventID] = true
	s.notices = append(s.notices, id+"|"+eventID+"|"+text)
	return execjob.NoticeAccepted, nil
}

func (s *execTestSessions) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.notices...)
}

type hostedExecFixture struct {
	t        *testing.T
	s        *Server
	root     string
	key      string
	hosted   *execjob.Hosted
	manager  *wsholder.Manager
	sessions *execTestSessions
}

// newHostedExecFixture is a server whose connection is bound to the hosted
// conversation "lead", over real session holders.
func newHostedExecFixture(t *testing.T, window time.Duration) *hostedExecFixture {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("the hosted exec tests need python3 for the session holder: %v", err)
	}
	useLeadProfile(t)
	// The lead's authority is stated, not inherited: a caller running these
	// tests as a read-only worker has the level in its own environment.
	t.Setenv(wsagent.EnvPermissionLevel, "")
	root := t.TempDir()
	mustWrite(t, root, "README.md", "x\n")
	mustWrite(t, root, "sub/.keep", "x\n")
	initGit(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	state := t.TempDir()
	m := wsholder.New(wsholder.Options{Dir: filepath.Join(state, wsholder.DirName), Python: python})
	store, err := execjob.OpenHostedStore(filepath.Join(state, "exec-jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := &execTestSessions{seen: map[string]bool{}}
	hosted, err := execjob.NewHosted(execjob.HostedOptions{Holders: execjob.HolderManager{Manager: m}, Store: store, Sessions: sessions, ExitDir: filepath.Join(state, "exec-exits"), Window: window})
	if err != nil {
		t.Fatal(err)
	}
	m.AddSink(hosted)
	hosted.Start()
	t.Cleanup(func() {
		// Every holder this test started ends with it.
		for _, h := range m.List().Holders {
			_ = m.Stop(h.ID)
		}
		_ = hosted.Close()
		m.Close()
	})
	s := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, s, 900001, root, nil))
	s.exec, s.conversation = func() *execjob.Hosted { return hosted }, "lead"
	return &hostedExecFixture{t: t, s: s, root: root, key: key, hosted: hosted, manager: m, sessions: sessions}
}

// call runs one tool call with the fixture's key and returns its text and
// whether it is an error.
func (fx *hostedExecFixture) call(name string, args map[string]any) (string, bool) {
	fx.t.Helper()
	return callExecTool(fx.t, fx.s, fx.key, name, args)
}

func callExecTool(t *testing.T, s *Server, key, name string, args map[string]any) (string, bool) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	if _, ok := args["session_key"]; !ok && key != "" {
		args["session_key"] = key
	}
	raw, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	resp := s.callTool(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: raw})
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error != nil {
		return decoded.Error.Message, true
	}
	var texts []string
	for _, c := range decoded.Result.Content {
		texts = append(texts, c.Text)
	}
	return strings.Join(texts, "\n"), decoded.Result.IsError
}

var execIDLine = regexp.MustCompile(`(?m)^exec_id: (\S+)$`)
var execKeyLine = regexp.MustCompile(`(?m)^exec_key: (\S+)$`)

func lineValue(t *testing.T, re *regexp.Regexp, text string) string {
	t.Helper()
	m := re.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no %s in:\n%s", re, text)
	}
	return m[1]
}

func waitExec(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestHostedExecNeedsTheTrustedBinding: without the connection's binding
// the new tools and the new exec.shell form refuse, whatever session_key the
// call carries, and a legacy launch keeps the file-backed path and key.
func TestHostedExecNeedsTheTrustedBinding(t *testing.T) {
	fx := newHostedExecFixture(t, 5*time.Second)
	launcher := NewServer(fx.root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, launcher, 900002, fx.root, nil))
	launcher.exec = func() *execjob.Hosted { return fx.hosted } // the daemon's jobs, but no binding
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"exec.run", map[string]any{"exe": "true"}},
		{"exec.shell", map[string]any{"cmd": "true"}},
		{"exec.list", nil},
		{"exec.tail", map[string]any{"exec_id": "exec_a-b-c"}},
		{"exec.stop", map[string]any{"exec_id": "exec_a-b-c"}},
		{"exec.write", map[string]any{"exec_id": "exec_a-b-c", "data": "x"}},
	} {
		text, isErr := callExecTool(t, launcher, key, call.name, call.args)
		if !isErr || !strings.Contains(text, "runs only in a session the dashboard hosts") {
			t.Fatalf("%s without a binding = %v %s", call.name, isErr, text)
		}
	}
	// The bound connection's key does not lend the launcher its binding.
	if text, isErr := callExecTool(t, launcher, fx.key, "exec.run", map[string]any{"exe": "true"}); !isErr {
		t.Fatalf("a bound session's key launched on an unbound connection: %s", text)
	}
	text, isErr := callExecTool(t, launcher, key, "exec.shell", map[string]any{"command": "echo legacy-path"})
	if isErr || !regexp.MustCompile(`(?m)^exec_key: exec-[0-9a-f]{8}$`).MatchString(text) || !strings.Contains(text, "legacy-path") {
		t.Fatalf("unbound legacy launch = %v %s", isErr, text)
	}
	if views, _ := fx.hosted.List("lead"); len(views) != 0 {
		t.Fatalf("the unbound legacy launch became a hosted job: %+v", views)
	}
	// A mixed call is refused before any handler.
	if text, isErr := fx.call("exec.shell", map[string]any{"cmd": "true", "command": "true"}); !isErr || !strings.Contains(text, "mixed argument forms") {
		t.Fatalf("mixed exec.shell = %v %s", isErr, text)
	}
}

func TestHostedExecModelLaunchRequiresCorrelationAndAuthority(t *testing.T) {
	fx := newHostedExecFixture(t, 5*time.Second)
	level := wsfs.AuthorityWorkdirWrite
	fx.s.execCutover = wsagent.BackendWerk
	fx.s.fileWrites = &FileWriteSession{CurrentAuthority: func() wsfs.Authority { return level }}
	var recorded []execjob.ModelLaunch
	fx.s.recordExecLaunch = func(_ context.Context, call execjob.ModelLaunch) error {
		if call.ID != "fixture-call" {
			return errors.New("no matching pending call")
		}
		recorded = append(recorded, call)
		return nil
	}
	args := map[string]any{"cmd": "echo correlated", "session_key": fx.key}
	if out, failed := rawCorrelatedToolCall(t, fx.s, "exec.shell", "", args); !failed || !strings.Contains(out, "requires a model call identity") {
		t.Fatalf("uncorrelated call = %v, %s", failed, out)
	}
	if out, failed := rawCorrelatedToolCall(t, fx.s, "exec.shell", "unmatched", args); !failed || !strings.Contains(out, "no matching pending call") {
		t.Fatalf("unmatched call = %v, %s", failed, out)
	}
	level = wsfs.AuthorityReadOnly
	if out, failed := rawCorrelatedToolCall(t, fx.s, "exec.shell", "fixture-call", args); !failed || !strings.Contains(out, "read-only") {
		t.Fatalf("read-only call = %v, %s", failed, out)
	}
	if len(recorded) != 0 || len(fx.manager.List().Holders) != 0 {
		t.Fatal("a refused launch recorded an intent or allocated a holder")
	}
	level = wsfs.AuthorityWorkdirWrite
	out, failed := rawCorrelatedToolCall(t, fx.s, "exec.shell", "fixture-call", args)
	if failed || !strings.Contains(out, "correlated") || len(recorded) != 1 {
		t.Fatalf("correlated call = %v, %s, recorded %d times", failed, out, len(recorded))
	}
	call := recorded[0]
	if call.JobID != lineValue(t, execIDLine, out) || call.Tool != "exec.shell" ||
		!strings.Contains(string(call.Arguments), `"cmd":"echo correlated"`) {
		t.Fatal("the recorder did not receive the launched job and arguments")
	}
}

// TestHostedExecRunAndShellForms: argv runs without a shell, the new shell
// form runs one Bash line, and both answer with the exit and output.
func TestHostedExecRunAndShellForms(t *testing.T) {
	fx := newHostedExecFixture(t, 10*time.Second)
	text, isErr := fx.call("exec.run", map[string]any{"exe": "printf", "args": []string{"%s|%s", "a b", "$HOME"}, "cwd": "sub"})
	if isErr || !strings.Contains(text, "status: exited 0") || !strings.Contains(text, "a b|$HOME") || !strings.Contains(text, filepath.Join("sub")) {
		t.Fatalf("exec.run = %v %s", isErr, text)
	}
	id := lineValue(t, execIDLine, text)
	if !regexp.MustCompile(`^exec_[a-z]+-[a-z]+-[a-z]+$`).MatchString(id) {
		t.Fatalf("exec id %q", id)
	}
	text, isErr = fx.call("exec.shell", map[string]any{"cmd": `echo "$((6*7))"; echo oops >&2; exit 3`})
	if isErr || !strings.Contains(text, "status: exited 3") || !strings.Contains(text, "42\n") || !strings.Contains(text, "oops") {
		t.Fatalf("exec.shell = %v %s", isErr, text)
	}
	// Raw bytes stay raw; the reply decodes them as validated UTF-8.
	text, isErr = fx.call("exec.run", map[string]any{"exe": "printf", "args": []string{`ok \377\n`}})
	if isErr || !strings.Contains(text, "ok �") || !strings.Contains(text, "invalid UTF-8") {
		t.Fatalf("invalid UTF-8 output = %v %s", isErr, text)
	}
	if text, isErr := fx.call("exec.run", map[string]any{"exe": "no-such-program-ws-exec-test"}); !isErr || !strings.Contains(text, "not on the job's PATH") {
		t.Fatalf("missing program = %v %s", isErr, text)
	}
	// Hosted output lives in memory: no file anywhere under the cache home
	// or the worktree is named for the job, and no file-backed job directory
	// was made.
	for _, dir := range []string{fx.s.getenv("WS_CACHE_HOME"), fx.root} {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && (strings.Contains(path, id) || d.IsDir() && d.Name() == "exec-jobs") {
				t.Fatalf("a hosted job left a per-job output path: %s", path)
			}
			return nil
		})
	}
}

// TestHostedExecAsyncWriteTailListStopAndNotice: async answers at once,
// exec.write feeds an open stdin until eof, the tail and listing show the
// job, the end sends one notice, and exec.stop ends a running job's group.
func TestHostedExecAsyncWriteTailListStopAndNotice(t *testing.T) {
	fx := newHostedExecFixture(t, 10*time.Second)
	text, isErr := fx.call("exec.run", map[string]any{"exe": "cat", "async": true})
	if isErr || !strings.Contains(text, "started async, exec id") || !strings.Contains(text, "status: running") {
		t.Fatalf("async cat = %v %s", isErr, text)
	}
	cat := lineValue(t, execIDLine, text)
	if text, isErr := fx.call("exec.write", map[string]any{"exec_id": cat, "data": "first\n"}); isErr {
		t.Fatalf("write = %s", text)
	}
	if text, isErr := fx.call("exec.write", map[string]any{"exec_id": cat, "data": "second\n", "eof": true}); isErr || !strings.Contains(text, "closed stdin") {
		t.Fatalf("write eof = %v %s", isErr, text)
	}
	waitExec(t, "cat to end", func() bool {
		text, _ := fx.call("exec.tail", map[string]any{"exec_id": cat})
		return strings.Contains(text, "status: exited 0")
	})
	text, _ = fx.call("exec.tail", map[string]any{"exec_id": cat, "bytes": 7})
	if !strings.Contains(text, "second\n") || strings.Contains(text, "first") || !strings.Contains(text, "output [6, 13)") {
		t.Fatalf("tail of 7 bytes = %s", text)
	}
	waitExec(t, "the async job's notice", func() bool { return len(fx.sessions.list()) == 1 })
	if n := fx.sessions.list()[0]; !strings.HasPrefix(n, "lead|exec:"+cat+":exit|") || !strings.Contains(n, "exited with code 0") || !strings.Contains(n, "second") {
		t.Fatalf("notice = %s", n)
	}
	if text, isErr := fx.call("exec.write", map[string]any{"exec_id": cat, "data": "late"}); !isErr {
		t.Fatalf("write to an ended job = %s", text)
	}

	// A running job in the listing, ahead of the ended one; exec.stop ends it.
	text, isErr = fx.call("exec.run", map[string]any{"exe": "sleep", "args": []string{"30"}, "async": true})
	if isErr {
		t.Fatal(text)
	}
	sleeper := lineValue(t, execIDLine, text)
	list, _ := fx.call("exec.list", nil)
	if !strings.Contains(list, sleeper+"  running") || !strings.Contains(list, cat+"  exited 0") || strings.Index(list, sleeper) > strings.Index(list, cat) {
		t.Fatalf("listing = %s", list)
	}
	text, isErr = fx.call("exec.stop", map[string]any{"exec_id": sleeper})
	if isErr || !strings.Contains(text, "status: stopped") {
		t.Fatalf("stop = %v %s", isErr, text)
	}
	if len(fx.sessions.list()) != 1 {
		t.Fatalf("a stopped job notified: %q", fx.sessions.list())
	}
	// Another conversation sees none of it.
	other := NewServer(fx.root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, other, 900003, fx.root, nil))
	other.exec, other.conversation = func() *execjob.Hosted { return fx.hosted }, "other"
	if text, _ := callExecTool(t, other, key, "exec.list", nil); !strings.Contains(text, "no exec jobs") {
		t.Fatalf("another conversation's listing = %s", text)
	}
	if text, isErr := callExecTool(t, other, key, "exec.tail", map[string]any{"exec_id": cat}); !isErr || !strings.Contains(text, "not found") {
		t.Fatalf("another conversation's tail = %v %s", isErr, text)
	}
}

// TestHostedExecConversionInTheSameTurn: a job outliving the window answers
// "converted to async" in the same call and notifies once at its end.
func TestHostedExecConversionInTheSameTurn(t *testing.T) {
	fx := newHostedExecFixture(t, 300*time.Millisecond)
	start := time.Now()
	text, isErr := fx.call("exec.shell", map[string]any{"cmd": "sleep 1; echo late"})
	if isErr || !strings.HasPrefix(text, "converted to async, exec id ") || !strings.Contains(text, "status: running") {
		t.Fatalf("conversion = %v %s", isErr, text)
	}
	if elapsed := time.Since(start); elapsed > 900*time.Millisecond {
		t.Fatalf("conversion took %s", elapsed)
	}
	id := lineValue(t, execIDLine, text)
	waitExec(t, "the converted job's notice", func() bool { return len(fx.sessions.list()) == 1 })
	if n := fx.sessions.list()[0]; !strings.Contains(n, "exec:"+id+":exit") || !strings.Contains(n, "late") {
		t.Fatalf("notice = %s", n)
	}
	// A synchronous end sends none.
	if text, isErr := fx.call("exec.run", map[string]any{"exe": "true"}); isErr || !strings.Contains(text, "status: exited 0") {
		t.Fatalf("quick job = %v %s", isErr, text)
	}
	time.Sleep(100 * time.Millisecond)
	if n := fx.sessions.list(); len(n) != 1 {
		t.Fatalf("notices = %q", n)
	}
}

// TestHostedExecLegacyLaunchesReadToEOF: both legacy launch shapes run as
// hosted jobs with the legacy key and reply; a program reading stdin to EOF
// finishes with stdin given and with stdin absent; the five legacy readers
// and abort work on the hosted key.
func TestHostedExecLegacyLaunchesReadToEOF(t *testing.T) {
	fx := newHostedExecFixture(t, 10*time.Second)
	legacyKey := regexp.MustCompile(`(?m)^exec_key: exec-[0-9]+-[0-9a-f]{16}$`)
	// The legacy exec.shell runs cmd.exe on Windows unless a shell is named,
	// so these POSIX lines name Bash there.
	posixShell := func(args map[string]any) map[string]any {
		if runtime.GOOS == "windows" {
			args["shell"] = "bash"
		}
		return args
	}
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"exec.spawn", map[string]any{"cmd": "cat", "stdin": "spawn-in\n"}, "spawn-in"},
		{"exec.spawn", map[string]any{"cmd": "cat"}, ""},
		{"exec.shell", posixShell(map[string]any{"command": "cat; echo shell-done", "stdin": "shell-in\n"}), "shell-in"},
		{"exec.shell", posixShell(map[string]any{"command": "cat; echo shell-done"}), "shell-done"},
	} {
		text, isErr := fx.call(tc.name, tc.args)
		// Empty output has no stream sections, as on the file-backed path.
		streams := tc.want == "" || strings.Contains(text, "========== stdout ==========")
		if isErr || !legacyKey.MatchString(text) || !strings.Contains(text, "status: succeeded") || !strings.Contains(text, "result_ready: true") || !streams || !strings.Contains(text, tc.want) {
			t.Fatalf("%s %v = %v %s", tc.name, tc.args, isErr, text)
		}
	}
	text, _ := fx.call("exec.shell", posixShell(map[string]any{"command": "printf 'l1\\nl2\\n'; printf 'e1\\n' >&2", "working_dir": "sub"}))
	key := lineValue(t, execKeyLine, text)
	// The combined stream is arrival order across two pipes, so stderr's e1
	// may land before, between, or after stdout's lines (on Windows each
	// printf'd line can arrive as its own write). Grep must number e1 by
	// where it actually landed in the combined text.
	combined, _ := fx.call("exec.raw.read", map[string]any{"exec_key": key, "stream": "combined", "offset": 0, "limit": 100})
	_, combinedText, _ := strings.Cut(combined, "========== text ==========\n")
	e1Line := slices.Index(strings.Split(combinedText, "\n"), "e1") + 1
	if e1Line == 0 || strings.Count(combinedText, "\n") != 3 {
		t.Fatalf("combined = %s", combined)
	}
	for _, tc := range []struct {
		name string
		args map[string]any
		want []string
	}{
		{"exec.status", map[string]any{"exec_key": key}, []string{"status: succeeded", "result_ready: true", "stdout_bytes: 6", "stderr_bytes: 3"}},
		{"exec.result", map[string]any{"exec_key": key}, []string{"l1\nl2\n", "e1\n"}},
		{"exec.raw.tail", map[string]any{"exec_key": key, "lines": 1}, []string{"stream: stdout", "l2"}},
		{"exec.raw.read", map[string]any{"exec_key": key, "stream": "stdout", "offset": 3, "limit": 3}, []string{"offset: 3", "next_offset: 6", "size: 6", "l2"}},
		{"exec.raw.grep", map[string]any{"exec_key": key, "stream": "combined", "pattern": "e1"}, []string{"matches: 1", fmt.Sprintf("%d: e1", e1Line)}},
	} {
		got, isErr := fx.call(tc.name, tc.args)
		for _, want := range tc.want {
			if isErr || !strings.Contains(got, want) {
				t.Fatalf("%s = %v %s; want %q", tc.name, isErr, got, want)
			}
		}
	}
}

// TestHostedExecLegacyTimedReplyMatchesTheFileBackedShape: a hosted legacy
// launch outliving its window answers the same running reply, with reader
// guidance, as the file-backed 5 s path, and an older client's follow-up
// status, result and abort keep working on both.
func TestHostedExecLegacyTimedReplyMatchesTheFileBackedShape(t *testing.T) {
	fx := newHostedExecFixture(t, 300*time.Millisecond)
	unbound := NewServer(fx.root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, unbound, 900004, fx.root, nil))
	fileText, isErr := callExecTool(t, unbound, key, "exec.spawn", map[string]any{"cmd": "sleep", "args": []string{"30"}})
	if isErr {
		t.Fatal(fileText)
	}
	hostedText, isErr := fx.call("exec.spawn", map[string]any{"cmd": "sleep", "args": []string{"30"}})
	if isErr {
		t.Fatal(hostedText)
	}
	fields := func(text string) []string {
		var out []string
		for _, line := range strings.Split(text, "\n") {
			if name, _, ok := strings.Cut(line, ": "); ok {
				out = append(out, name)
			}
		}
		return out
	}
	if got, want := strings.Join(fields(hostedText), ","), strings.Join(fields(fileText), ","); got != want {
		t.Fatalf("hosted timed reply fields %s, file-backed %s\nhosted:\n%s\nfile-backed:\n%s", got, want, hostedText, fileText)
	}
	for _, text := range []string{fileText, hostedText} {
		if !strings.Contains(text, "status: running") || !strings.Contains(text, "result_ready: false") || !strings.Contains(text, "guidance: ") {
			t.Fatalf("timed reply = %s", text)
		}
	}
	for _, c := range []struct {
		s    *Server
		key  string
		text string
	}{{unbound, key, fileText}, {fx.s, fx.key, hostedText}} {
		execKey := lineValue(t, execKeyLine, c.text)
		if got, _ := callExecTool(t, c.s, c.key, "exec.status", map[string]any{"exec_key": execKey}); !strings.Contains(got, "status: running") {
			t.Fatalf("status = %s", got)
		}
		if got, _ := callExecTool(t, c.s, c.key, "exec.result", map[string]any{"exec_key": execKey, "timeout_seconds": 0.2}); !strings.Contains(got, "result_ready: false") {
			t.Fatalf("result = %s", got)
		}
		if got, _ := callExecTool(t, c.s, c.key, "exec.abort", map[string]any{"exec_key": execKey}); !strings.Contains(got, "status: cancel") {
			t.Fatalf("abort = %s", got)
		}
	}
}

// TestHostedExecOldFileBackedRecordsStayReadable: the legacy readers of a
// hosted connection still read a file-backed job and its byte offsets.
func TestHostedExecOldFileBackedRecordsStayReadable(t *testing.T) {
	fx := newHostedExecFixture(t, 10*time.Second)
	unbound := NewServer(fx.root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, unbound, 900005, fx.root, nil))
	text, _ := callExecTool(t, unbound, key, "exec.shell", map[string]any{"command": "printf 'old-1\\nold-2\\n'"})
	execKey := lineValue(t, execKeyLine, text)
	got, isErr := fx.call("exec.raw.read", map[string]any{"exec_key": execKey, "offset": 6, "limit": 6})
	if isErr || !strings.Contains(got, "old-2") || !strings.Contains(got, "offset: 6") || !strings.Contains(got, "next_offset: 12") {
		t.Fatalf("hosted connection reading a file-backed job = %v %s", isErr, got)
	}
	if got, _ := fx.call("exec.status", map[string]any{"exec_key": execKey}); !strings.Contains(got, "status: succeeded") {
		t.Fatalf("status = %s", got)
	}
}
