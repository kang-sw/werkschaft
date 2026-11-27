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
	"github.com/kang-sw/devenv/internal/wstestutil"
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
func (s measureSessions) LockExecAuthority(string) (func(), error) { return func() {}, nil }
func (s measureSessions) Immediate(string) (bool, error)           { return true, nil }
func (s measureSessions) StopNote(id string) string                { return s.r.ExecStopNote(id) }
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
	total, sites := siteDeltas(before, after)
	var parts []string
	for site, d := range sites {
		parts = append(parts, fmt.Sprintf("%s=%d", site, d))
	}
	sort.Strings(parts)
	return total, strings.Join(parts, " ")
}

// siteDeltas is the per-site form of siteDelta: site -> counted operations
// between the two reads (only sites that grew).
func siteDeltas(before, after map[string]int64) (int64, map[string]int64) {
	sites := map[string]int64{}
	for k, v := range after {
		site, ok := strings.CutPrefix(k, "file_io_site:")
		if !ok {
			continue
		}
		if d := v - before[k]; d > 0 {
			sites[site] = d
		}
	}
	return after["file_io_total"] - before["file_io_total"], sites
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
	root := wstestutil.CanonicalTempDir(t)
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
	// background is the set of sites the harness itself touches while nothing
	// is asked of the service (filled from the control measurement below). The
	// counters are process-wide, so a heartbeat can land inside a microsecond
	// window; the zero-op assertions therefore count only sites outside it.
	// Any site the code under test could touch (the Hosted, holder, inbox and
	// ring stores) is absent from the control, so it is still counted.
	background := map[string]bool{}
	var measuredSites map[string]int64
	measure := func(name string, do func()) int64 {
		t.Helper()
		before := wsstate.FileIOCounters()
		start := time.Now()
		do()
		took := time.Since(start)
		after := wsstate.FileIOCounters()
		ops, sites := siteDelta(before, after)
		_, measuredSites = siteDeltas(before, after)
		t.Logf("%-46s %3d counted file ops, %v  [%s]", name, ops, took.Round(time.Microsecond), sites)
		return ops
	}
	// foreign is the last measurement's operations at sites outside the
	// background set, and their description.
	foreign := func() (int64, string) {
		var n int64
		var parts []string
		for site, d := range measuredSites {
			if !background[site] {
				n += d
				parts = append(parts, fmt.Sprintf("%s=%d", site, d))
			}
		}
		sort.Strings(parts)
		return n, strings.Join(parts, " ")
	}

	a, b, c := launch(""), launch(""), launch(execjob.OriginUser)
	// Control: the same wait with no action, so the harness's own
	// background file operations (its heartbeat) can be told apart.
	if ops := measure("control: 2.2 s of nothing", func() { time.Sleep(2200 * time.Millisecond) }); ops == 0 {
		t.Fatal("the control saw no background file operations; the harness heartbeat is not being counted")
	}
	for site := range measuredSites {
		background[site] = true
	}
	// Reads: memory only, at any job count (here 3 jobs and a read of the
	// retained output).
	measure("list (Hosted.Jobs, 3 jobs)", func() { _ = hosted.Jobs(id) })
	if n, sites := foreign(); n != 0 {
		t.Fatalf("the list touched %d files [%s]", n, sites)
	}
	measure("output read (16 KiB window)", func() { _, _ = hosted.ReadOutput(id, a, 0, 0) })
	if n, sites := foreign(); n != 0 {
		t.Fatalf("the output read touched %d files [%s]", n, sites)
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
	measure("repeated stop (already stopped, notice sent)", func() { _, _ = hosted.StopByUser(id, a) })
	if n, sites := foreign(); n != 0 {
		t.Fatalf("a repeated stop touched %d files [%s]", n, sites)
	}
}
