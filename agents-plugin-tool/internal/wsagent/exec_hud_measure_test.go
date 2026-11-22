package wsagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// measureSessions is the daemon's execSessions over a real Runner (the
// command layer's adapter, which lives in package main): the production
// notice, receipt and stop-note paths, with the approval answered.
type measureSessions struct{ r *Runner }

func (s measureSessions) Owner(id string) (execjob.HostedOwner, error) {
	o, err := s.r.ExecOwner(id)
	return execjob.HostedOwner{ID: o.ID, Gen: o.Gen, Lead: o.Lead}, err
}
func (s measureSessions) Approve(context.Context, string, execjob.HostedCall) (bool, error) {
	return true, nil
}
func (s measureSessions) Immediate(string) (bool, error) { return true, nil }
func (s measureSessions) StopNote(id string) string      { return s.r.ExecStopNote(id) }
func (s measureSessions) Hand(id string, gen uint64, execID, text string) error {
	return s.r.PassiveHand(id, gen, execID, text)
}
func (s measureSessions) Notify(id string, gen uint64, eventID, text string) (execjob.NoticeOutcome, error) {
	out, err := s.r.AdmitCompletion(id, gen, eventID, text)
	if err != nil {
		return 0, err
	}
	switch out {
	case CompletionAccepted:
		return execjob.NoticeAccepted, nil
	case CompletionDuplicate:
		return execjob.NoticeDuplicate, nil
	case CompletionDeferred:
		return execjob.NoticeDeferred, nil
	}
	return execjob.NoticeDropped, nil
}

type measureLink struct {
	hosted atomic.Pointer[execjob.Hosted]
}

func (l *measureLink) HolderEvent(id string, ev wsholder.Event) {
	if h := l.hosted.Load(); h != nil {
		h.HolderEvent(id, ev)
	}
}

// siteDelta is the counted file operations between two FileIOCounters reads,
// by source site, and their total.
func siteDelta(before, after map[string]int64) (int64, string) {
	var parts []string
	for k, v := range after {
		site, ok := strings.CutPrefix(k, "file_io_site:")
		if !ok {
			continue
		}
		if d := v - before[k]; d > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", site, d))
		}
	}
	sort.Strings(parts)
	return after["file_io_total"] - before["file_io_total"], strings.Join(parts, " ")
}

// TestExecHUDStopAndReadsThroughProductionPaths measures the HUD's service
// actions end to end on the production objects (ai-docs/manuals/performance.md):
// a real Hosted over a real holder process, its notices admitted by a real
// Runner (inbox entry and conversation record), counted with the wrapper
// file-operation counters. The route's own cookie revalidation is the
// separate baseline of the wsdashboard measurement. It logs the table the
// ticket records and pins the I/O-free reads.
func TestExecHUDStopAndReadsThroughProductionPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the jobs run /bin/sh; the Windows host verifies the stop separately")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3: %v", err)
	}
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), toolUseLine("u1", "toolu_1"))
	h.sync(id, holder)

	state := t.TempDir()
	link := &measureLink{}
	m := wsholder.New(wsholder.Options{Dir: filepath.Join(state, wsholder.DirName), Python: python, OnExit: func(desc wsholder.Descriptor, exit wsholder.Exit) error {
		if _, ok := execjob.ExecHolderID(desc.Session); ok {
			if hh := link.hosted.Load(); hh != nil {
				hh.HolderGone(desc, exit)
			}
		}
		return nil
	}})
	m.AddSink(link)
	m.Scan()
	store, err := execjob.OpenHostedStore(filepath.Join(state, "exec-jobs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	hosted, err := execjob.NewHosted(execjob.HostedOptions{Holders: execjob.HolderManager{Manager: m}, Store: store, Sessions: measureSessions{r: h.runner}, ExitDir: filepath.Join(state, "exec-exits"), Window: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	link.hosted.Store(hosted)
	hosted.Start()
	t.Cleanup(func() {
		_ = hosted.Close()
		for _, info := range m.List().Holders {
			_ = m.Stop(info.ID)
		}
		m.Close()
	})
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	launch := func(origin string) string {
		t.Helper()
		req, err := execjob.ShellLaunch(id, "exec.shell", root, "", "echo started; sleep 60", os.Environ(), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Async, req.UserApproved, req.Origin = true, true, origin
		v, err := hosted.Launch(context.Background(), make(chan struct{}), req)
		if err != nil || v.Outcome != execjob.OutcomeAsync {
			t.Fatalf("launch = %+v, %v", v, err)
		}
		eventually(t, "the job's output", func() bool {
			out, err := hosted.ReadOutput(id, v.ID, 0, 0)
			return err == nil && out.Written > 0
		})
		return v.ID
	}
	measure := func(name string, do func()) int64 {
		t.Helper()
		before := wsstate.FileIOCounters()
		start := time.Now()
		do()
		took := time.Since(start)
		ops, sites := siteDelta(before, wsstate.FileIOCounters())
		t.Logf("%-46s %3d counted file ops, %v  [%s]", name, ops, took.Round(time.Microsecond), sites)
		return ops
	}

	a, b, c := launch(""), launch(""), launch(execjob.OriginUser)
	// Control: the same wait with no action, so the harness's own
	// background file operations (its heartbeat) can be told apart.
	measure("control: 2.2 s of nothing", func() { time.Sleep(2200 * time.Millisecond) })
	// Reads: memory only, at any job count (here 3 jobs and a read of the
	// retained output).
	if ops := measure("list (Hosted.Jobs, 3 jobs)", func() { _ = hosted.Jobs(id) }); ops != 0 {
		t.Fatalf("the list touched %d files", ops)
	}
	if ops := measure("output read (16 KiB window)", func() { _, _ = hosted.ReadOutput(id, a, 0, 0) }); ops != 0 {
		t.Fatalf("the output read touched %d files", ops)
	}
	// The agent job's stop while its conversation runs a turn: the notice
	// steers into it.
	measure("stop of an agent job, notice steered (running)", func() {
		if _, err := hosted.StopByUser(id, a); err != nil {
			t.Fatal(err)
		}
	})
	eventually(t, "the steered notice", func() bool {
		for _, in := range h.userInputs(holder) {
			if strings.Contains(in, a+" was stopped") {
				return true
			}
		}
		return false
	})
	// The user's own job: a receipt in the pending input, no turn.
	measure("stop of the user's `!` job, receipt handed", func() {
		if _, err := hosted.StopByUser(id, c); err != nil {
			t.Fatal(err)
		}
	})
	// An idle conversation: the notice starts a turn.
	h.holders.out(holder, turnLines("sess-1", "u2", "done")...)
	h.idle(id)
	measure("stop of an agent job, notice wakes an idle turn", func() {
		if _, err := hosted.StopByUser(id, b); err != nil {
			t.Fatal(err)
		}
	})
	for _, j := range hosted.Jobs(id) {
		if !j.StoppedByUser || j.State != execjob.HostedStopped {
			t.Fatalf("job %s = %+v, want stopped by user", j.ID, j)
		}
	}
	// The same stop of a job that is already stopped is an I/O-free no-op.
	if ops := measure("repeated stop (already stopped, notice sent)", func() { _, _ = hosted.StopByUser(id, a) }); ops != 0 {
		t.Fatalf("a repeated stop touched %d files", ops)
	}
}
