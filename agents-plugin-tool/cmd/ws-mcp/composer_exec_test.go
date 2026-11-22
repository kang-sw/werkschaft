package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// composerRunnerFake is the runner side of a composer launch: one open lead.
type composerRunnerFake struct {
	mu     sync.Mutex
	target wsagent.ComposerTarget
	err    error
	hands  []string
	events *[]string
}

func (r *composerRunnerFake) ComposerTarget(id string) (wsagent.ComposerTarget, error) {
	return r.target, r.err
}

func (r *composerRunnerFake) PassiveHand(id string, gen uint64, execID, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hands = append(r.hands, execID+"|"+text)
	if r.events != nil {
		*r.events = append(*r.events, "hand:"+execID)
	}
	return nil
}

// denySessions runs the lead's own calls but fails any approval: a composer
// launch must be its own approval and never ask the agent's policy.
type denySessions struct {
	recordingSessions
	mu       sync.Mutex
	asked    int
	gen      uint64
	events   *[]string
	notified chan struct{}
}

func (s *denySessions) Owner(id string) (execjob.HostedOwner, error) {
	return execjob.HostedOwner{ID: id, Gen: s.gen, Lead: true}, nil
}
func (s *denySessions) Approve(context.Context, string, execjob.HostedCall) (bool, error) {
	s.mu.Lock()
	s.asked++
	s.mu.Unlock()
	return false, errors.New("approval asked")
}
func (s *denySessions) Immediate(string) (bool, error) {
	s.mu.Lock()
	s.asked++
	s.mu.Unlock()
	return false, nil
}
func (s *denySessions) Notify(id string, gen uint64, eventID, text string) (execjob.NoticeOutcome, error) {
	s.mu.Lock()
	if s.events != nil {
		*s.events = append(*s.events, "notice:"+eventID)
	}
	s.mu.Unlock()
	s.recordingSessions.notices <- eventID + "|" + text
	return execjob.NoticeAccepted, nil
}

func openComposerFixture(t *testing.T, gen uint64) (*composerExec, *composerRunnerFake, *denySessions, *hostedExecDaemon, string, *[]string) {
	t.Helper()
	skipWithoutPOSIXShell(t)
	state := t.TempDir()
	var events []string
	sessions := &denySessions{gen: gen, events: &events}
	sessions.recordingSessions.notices = make(chan string, 8)
	d := openHostedExecDaemonWith(t, state, 30*time.Second, sessions)
	t.Cleanup(func() { d.stopAll(); d.close() })
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &composerRunnerFake{target: wsagent.ComposerTarget{Owner: wsagent.ExecOwner{ID: "lead", Gen: gen, Lead: true}, Root: root, Environ: os.Environ()}, events: &events}
	return &composerExec{hosted: d.hosted, runner: runner}, runner, sessions, d, root, &events
}

// TestComposerExecRunsTheLinesAsTheLeadsOwnExecShell runs real shell lines
// through the same hosted jobs and holders exec.shell uses.
func TestComposerExecRunsTheLinesAsTheLeadsOwnExecShell(t *testing.T) {
	c, runner, sessions, _, root, _ := openComposerFixture(t, 1)
	run := func(line string, window time.Duration) (wsdashboard.ComposerJob, error) {
		return c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Line: line, Window: window})
	}
	// A multiline source runs as one script, in the lead's bound worktree,
	// with stdin at EOF (cat returns), and stderr joins the combined bytes.
	job, err := run("pwd\necho out\necho err 1>&2\ncat\n", 20*time.Second)
	if err != nil || !job.Terminal() || job.State != "exited" || job.ExitCode == nil || *job.ExitCode != 0 {
		t.Fatalf("job = %+v, %v", job, err)
	}
	got := string(job.Output)
	if !strings.HasPrefix(got, root+"\n") || !strings.Contains(got, "out\n") || !strings.Contains(got, "err\n") || job.Written != int64(len(got)) || job.Gen != 1 {
		t.Fatalf("output %q written %d gen %d", got, job.Written, job.Gen)
	}
	// It is the lead's own exec.shell job: listed under the lead, tool and
	// shell as exec.shell records them.
	views, _ := c.hosted.List("lead")
	if len(views) != 1 || views[0].Tool != "exec.shell" || views[0].Owner != "lead" || views[0].Gen != 1 || !strings.Contains(views[0].Command, "echo out") || views[0].Cwd != root {
		t.Fatalf("listing = %+v", views)
	}
	// The explicit user send was its own approval; the agent's policy was
	// neither asked nor consulted.
	sessions.mu.Lock()
	asked := sessions.asked
	sessions.mu.Unlock()
	if asked != 0 {
		t.Fatalf("the agent's approval policy was consulted %d times", asked)
	}
	if len(runner.hands) != 0 {
		t.Fatalf("a finished run handed a receipt itself: %v", runner.hands)
	}
	// Big output: the raw combined bytes and the whole retained range.
	job, err = run("head -c 10000 /dev/zero | tr '\\0' x", 20*time.Second)
	if err != nil || job.Written != 10000 || len(job.Output) != 10000 || job.Start != 0 {
		t.Fatalf("big job = written %d output %d start %d, %v", job.Written, len(job.Output), job.Start, err)
	}
	// A lead the runner will not name refuses; nothing runs.
	runner.err = errors.New("not an open hosted lead")
	before, _ := c.hosted.List("lead")
	if _, err := run("echo no", time.Second); err == nil {
		t.Fatal("a refused target launched")
	}
	if after, _ := c.hosted.List("lead"); len(after) != len(before) {
		t.Fatalf("a refused target left a job: %d -> %d", len(before), len(after))
	}
	runner.err = nil
}

// TestComposerExecConvertsPastTheWindowAndMakesTheIDVisibleFirst: a command
// still running at the window converts, its id reaches the lead's pending
// input before any completion notice, and the lead's close cascade stops it
// like an exec.shell job.
func TestComposerExecConvertsPastTheWindowAndMakesTheIDVisibleFirst(t *testing.T) {
	c, runner, sessions, _, _, events := openComposerFixture(t, 2)
	job, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Line: "sleep 0.4; echo late", Window: 100 * time.Millisecond})
	if err != nil || job.Terminal() || !job.Async || job.ExecID == "" || job.Gen != 2 {
		t.Fatalf("converted job = %+v, %v", job, err)
	}
	if len(runner.hands) == 0 || !strings.HasPrefix(runner.hands[0], job.ExecID+"|") || !strings.Contains(runner.hands[0], "still running") {
		t.Fatalf("the id was not visible in the pending input: %v", runner.hands)
	}
	select {
	case n := <-sessions.recordingSessions.notices:
		if !strings.HasPrefix(n, "exec:"+job.ExecID+":exit|") || !strings.Contains(n, "late") {
			t.Fatalf("notice = %q", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the ordinary completion notice never came")
	}
	sessions.mu.Lock()
	order := append([]string(nil), *events...)
	sessions.mu.Unlock()
	if len(order) < 2 || order[0] != "hand:"+job.ExecID || order[1] != "notice:exec:"+job.ExecID+":exit" {
		t.Fatalf("the id must be visible before the first admission: %v", order)
	}
	if back, err := c.Job("lead", job.ExecID); err != nil || !back.Terminal() || !strings.Contains(string(back.Output), "late") {
		t.Fatalf("job refresh = %+v, %v", back, err)
	}
	if _, err := c.Job("other", job.ExecID); err == nil {
		t.Fatal("another owner read the job")
	}

	// The close cascade: stopping the lead's jobs stops a composer job.
	long, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Line: "sleep 30", Window: 100 * time.Millisecond})
	if err != nil || long.Terminal() {
		t.Fatalf("long job = %+v, %v", long, err)
	}
	if err := c.hosted.StopOwner("lead"); err != nil {
		t.Fatal(err)
	}
	if v, err := c.hosted.Get("lead", long.ExecID); err != nil || v.State != execjob.HostedStopped {
		t.Fatalf("after the close cascade: %+v, %v", v, err)
	}
	// A stale open generation is refused at launch.
	runner.target.Owner.Gen = 1
	if _, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Line: "echo stale", Window: time.Second}); err == nil || !strings.Contains(err.Error(), "reopened") {
		t.Fatalf("stale generation launch = %v", err)
	}
}
