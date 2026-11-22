package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
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
	// handErr refuses PassiveHand.
	handErr error
}

func (r *composerRunnerFake) ComposerOpen(id string) (uint64, error) {
	return r.target.Owner.Gen, r.err
}

func (r *composerRunnerFake) ComposerTarget(id string) (wsagent.ComposerTarget, error) {
	return r.target, r.err
}

func (r *composerRunnerFake) PassiveHand(id string, gen uint64, execID, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handErr != nil {
		return r.handErr
	}
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
	mu    sync.Mutex
	asked int
	gen   uint64
	// stopHands are the receipts of user-stopped composer jobs (Hand).
	stopHands []string
	events    *[]string
	notified  chan struct{}
	// ownerCalls counts Owner calls; afterOwner runs after call n has read
	// its generation (a test's interleaving point).
	ownerCalls int
	afterOwner func(call int)
}

func (s *denySessions) Owner(id string) (execjob.HostedOwner, error) {
	s.mu.Lock()
	s.ownerCalls++
	call, gen, hook := s.ownerCalls, s.gen, s.afterOwner
	s.mu.Unlock()
	if hook != nil {
		defer hook(call)
	}
	return execjob.HostedOwner{ID: id, Gen: gen, Lead: true}, nil
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
func (s *denySessions) StopNote(string) string { return "" }
func (s *denySessions) Hand(id string, gen uint64, execID, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopHands = append(s.stopHands, execID+"|"+text)
	return nil
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
	return openComposerFixtureAnyShell(t, gen)
}

// openComposerFixtureAnyShell is the fixture without the POSIX-shell skip: its
// jobs run through execjob.ShellArgv, the shell exec.shell itself picks (Git
// Bash on Windows).
func openComposerFixtureAnyShell(t *testing.T, gen uint64) (*composerExec, *composerRunnerFake, *denySessions, *hostedExecDaemon, string, *[]string) {
	t.Helper()
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
		return c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Gen: runner.target.Owner.Gen, Line: line, Window: window})
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

// TestComposerExecSelectsItsShellThroughShellArgv runs a line on any platform
// through the shell execjob.ShellArgv picks for the job (Git Bash on Windows,
// bash or /bin/sh elsewhere), as exec.shell does: the lines are portable shell
// (echo, redirection, cat at EOF), the recorded shell is ShellArgv's, and the
// job runs in the lead's bound worktree. It skips only where there is no such
// shell at all.
func TestComposerExecSelectsItsShellThroughShellArgv(t *testing.T) {
	want, err := execjob.ShellArgv("true", os.Environ())
	if err != nil {
		t.Skipf("no shell for exec.shell here: %v", err)
	}
	c, runner, sessions, _, root, _ := openComposerFixtureAnyShell(t, 1)
	job, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Gen: runner.target.Owner.Gen, Line: "echo out\necho err 1>&2\ncat\n", Window: 60 * time.Second})
	if err != nil || !job.Terminal() || job.State != "exited" || job.ExitCode == nil || *job.ExitCode != 0 {
		t.Fatalf("job = %+v, %v", job, err)
	}
	got := strings.ReplaceAll(string(job.Output), "\r\n", "\n")
	if !strings.Contains(got, "out\n") || !strings.Contains(got, "err\n") || job.Written != int64(len(job.Output)) {
		t.Fatalf("output %q written %d", job.Output, job.Written)
	}
	views, _ := c.hosted.List("lead")
	if len(views) != 1 || views[0].Tool != "exec.shell" || views[0].Shell != want[0] || !strings.EqualFold(filepath.Clean(views[0].Cwd), filepath.Clean(root)) {
		t.Fatalf("listing = %+v, want shell %q in %q", views, want[0], root)
	}
	sessions.mu.Lock()
	asked := sessions.asked
	sessions.mu.Unlock()
	if asked != 0 {
		t.Fatalf("the agent's approval policy was consulted %d times", asked)
	}
}

// TestComposerExecConvertsPastTheWindowAndMakesTheIDVisibleFirst: a command
// still running at the window converts, its id reaches the lead's pending
// input before any completion notice, and the lead's close cascade stops it
// like an exec.shell job.
func TestComposerExecConvertsPastTheWindowAndMakesTheIDVisibleFirst(t *testing.T) {
	c, runner, sessions, _, _, events := openComposerFixture(t, 2)
	job, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Gen: 2, Line: "sleep 0.4; echo late", Window: 100 * time.Millisecond})
	if err != nil || job.Terminal() || !job.Async || job.ExecID == "" || job.Gen != 2 {
		t.Fatalf("converted job = %+v, %v", job, err)
	}
	if len(runner.hands) == 0 || !strings.HasPrefix(runner.hands[0], job.ExecID+"|") || !strings.Contains(runner.hands[0], "ran past") {
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
	long, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Gen: 2, Line: "sleep 30", Window: 100 * time.Millisecond})
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
	if _, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Gen: 2, Line: "echo stale", Window: time.Second}); err == nil || !strings.Contains(err.Error(), "reopened") {
		t.Fatalf("stale generation launch = %v", err)
	}
}

// TestComposerExecGenerationInterleavingsFromZero: a submission acknowledged
// under open generation 0 never runs on the generation a close and revival
// produced, whichever side of the launch the revival lands on: before the
// binding reads the runner, or between that read and the job's registration.
func TestComposerExecGenerationInterleavingsFromZero(t *testing.T) {
	t.Run("revived before the binding reads the runner", func(t *testing.T) {
		c, runner, _, _, _, _ := openComposerFixture(t, 0)
		jobs, _ := c.hosted.List("lead")
		before := len(jobs)
		runner.target.Owner.Gen = 1 // closed and revived after the route read 0
		_, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Gen: 0, Line: "echo never", Window: time.Second})
		if err == nil || !strings.Contains(err.Error(), "reopened") {
			t.Fatalf("launch on a revived generation = %v", err)
		}
		if jobs, _ := c.hosted.List("lead"); len(jobs) != before {
			t.Fatalf("a job exists for the stale submission: %d -> %d", before, len(jobs))
		}
	})
	t.Run("revived between the read and the registration", func(t *testing.T) {
		c, _, sessions, _, root, _ := openComposerFixture(t, 0)
		marker := filepath.Join(root, "ran")
		sessions.afterOwner = func(call int) {
			if call == 1 { // the launch's first read; the revival lands right after it
				sessions.mu.Lock()
				sessions.gen = 1
				sessions.mu.Unlock()
			}
		}
		_, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Gen: 0, Line: "touch " + marker, Window: time.Second})
		if err == nil || !strings.Contains(err.Error(), "reopened") {
			t.Fatalf("launch across the revival = %v", err)
		}
		time.Sleep(200 * time.Millisecond)
		if _, statErr := os.Stat(marker); statErr == nil {
			t.Fatal("the command ran on the revived generation")
		}
	})
}

// TestComposerExecConversionProbe prints, for the release record
// (WS_COMPOSER_PERF=1), what a real conversion costs: a real hosted launch of
// a long job with a 100 ms window, the time the conversion itself takes (from
// the window's end to the converted answer: the commit, the id's publication
// to the pending input and the notice release), against a quick run's whole
// launch (holder start, process, end).
func TestComposerExecConversionProbe(t *testing.T) {
	if os.Getenv("WS_COMPOSER_PERF") == "" {
		t.Skip("set WS_COMPOSER_PERF=1 to print the conversion probe")
	}
	c, runner, _, _, root, _ := openComposerFixture(t, 1)
	const window = 100 * time.Millisecond
	var convert, whole, quick []time.Duration
	for i := 0; i < 10; i++ {
		launch, err := execjob.ShellLaunch("lead", "exec.shell", root, "", "sleep 30", os.Environ(), nil)
		if err != nil {
			t.Fatal(err)
		}
		launch.Stdin, launch.StdinSet, launch.Window, launch.UserApproved = nil, true, window, true
		var published time.Time
		launch.OnConvert = func(v execjob.HostedView) error {
			published = time.Now()
			return runner.PassiveHand("lead", 1, v.ID, wsdashboard.ComposerAsyncReceipt(v.ID))
		}
		start := time.Now()
		v, err := c.hosted.Launch(context.Background(), make(chan struct{}), launch)
		end := time.Now()
		if err != nil || v.Outcome != execjob.OutcomeConverted || published.IsZero() {
			t.Fatalf("conversion = %+v, %v", v, err)
		}
		convert = append(convert, end.Sub(published))
		whole = append(whole, end.Sub(start)-window)
		start = time.Now()
		if q, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Gen: runner.target.Owner.Gen, Line: "true", Window: window}); err != nil || q.Async {
			t.Fatalf("quick = %+v, %v", q, err)
		}
		quick = append(quick, time.Since(start))
	}
	_ = c.hosted.StopOwner("lead")
	stat := func(name string, d []time.Duration) {
		sort.Slice(d, func(a, b int) bool { return d[a] < d[b] })
		t.Logf("%-50s n=%-3d p50=%-10s p95=%-10s max=%s", name, len(d), d[len(d)/2], d[len(d)*95/100], d[len(d)-1])
	}
	stat("conversion decision + id publication + release", convert)
	stat("long run: launch time beyond the window (holder start)", whole)
	stat("quick run: whole launch (holder start, process, end)", quick)
}

// TestComposerExecRefusedHandOffLeavesTheOrdinaryNotice: when the lead's
// pending input refuses the converted job's id (the conversation reopened), the
// job still converts and its one completion notice, which carries the id,
// reaches the lead: nothing was handed, so the notice is the single delivery.
func TestComposerExecRefusedHandOffLeavesTheOrdinaryNotice(t *testing.T) {
	c, runner, sessions, _, _, _ := openComposerFixture(t, 1)
	runner.handErr = errors.New("the conversation reopened since this command was submitted")
	job, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Gen: 1, Line: "sleep 0.3; echo late", Window: 100 * time.Millisecond})
	if err != nil || !job.Async || job.Terminal() {
		t.Fatalf("converted job = %+v, %v", job, err)
	}
	select {
	case n := <-sessions.recordingSessions.notices:
		if !strings.HasPrefix(n, "exec:"+job.ExecID+":exit|") || !strings.Contains(n, job.ExecID) {
			t.Fatalf("notice = %q", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the ordinary notice never came after a refused hand-off")
	}
	if len(runner.hands) != 0 {
		t.Fatalf("a refused hand-off recorded %v", runner.hands)
	}
}

// TestComposerJobUserStopHandsAStoppedReceiptAndWakesNothing: the user's own
// converted `!` job, stopped from the HUD, reads as the user's origin, ends
// stopped and marked, hands a stopped receipt to the lead's pending input
// (the same runner seam the id receipt used), and sends no steered notice.
func TestComposerJobUserStopHandsAStoppedReceiptAndWakesNothing(t *testing.T) {
	c, _, sessions, _, _, _ := openComposerFixture(t, 2)
	job, err := c.Launch(context.Background(), make(chan struct{}), wsdashboard.ComposerLaunch{ReplyID: "lead", Gen: 2, Line: "echo started; sleep 30", Window: 100 * time.Millisecond})
	if err != nil || !job.Async || job.Terminal() {
		t.Fatalf("converted job = %+v, %v", job, err)
	}
	jobs := c.Jobs("lead")
	if len(jobs) != 1 || jobs[0].Origin != execjob.OriginUser || jobs[0].State != execjob.HostedRunning || !strings.Contains(jobs[0].Command, "sleep 30") {
		t.Fatalf("jobs = %+v", jobs)
	}
	sum, err := c.StopByUser("lead", job.ExecID)
	if err != nil || sum.State != execjob.HostedStopped || !sum.StoppedByUser || sum.StopNotice != execjob.StopNoticeSent {
		t.Fatalf("stop = %+v, %v", sum, err)
	}
	sessions.mu.Lock()
	hands := append([]string(nil), sessions.stopHands...)
	sessions.mu.Unlock()
	if len(hands) != 1 || !strings.HasPrefix(hands[0], job.ExecID+"|[user ran from the dashboard]") || !strings.Contains(hands[0], "was stopped by the user") || !strings.Contains(hands[0], "started") {
		t.Fatalf("receipt = %v", hands)
	}
	select {
	case n := <-sessions.recordingSessions.notices:
		t.Fatalf("a `!` job's stop woke the agent: %q", n)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := c.StopByUser("other", job.ExecID); !errors.Is(err, execjob.ErrExecNotFound) {
		t.Fatalf("another owner's stop = %v", err)
	}
	read, err := c.ReadOutput("lead", job.ExecID, 0, 0)
	if err != nil || !read.Job.Terminal() || len(read.Segments) == 0 || !strings.Contains(string(read.Segments[0].Data), "started") || read.Segments[0].Stream != execjob.StreamStdout {
		t.Fatalf("output = %+v, %v", read, err)
	}
}
