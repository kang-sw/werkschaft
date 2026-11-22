package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync/atomic"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsholder"
)

// The daemon's hosted exec jobs: their metadata store and exit copies live
// in the daemon's state directory.
const (
	hostedExecStoreName = "exec-jobs.db"
	hostedExecExitsName = "exec-exits"
)

// hostedExecLink binds the daemon's hosted exec jobs once they exist. The
// holder manager takes its sink and exit hook before the daemon starts (its
// first scan runs inside StartDaemon), while the jobs' store opens only
// under the daemon's lock, after StartDaemon: until then a job holder's
// records are dropped (Start replays them) and an exec holder's cleanup
// leaves its exit copy for Start to read.
type hostedExecLink struct {
	hosted atomic.Pointer[execjob.Hosted]
}

// HolderEvent forwards a holder's records to the hosted jobs (wsholder.Sink).
func (l *hostedExecLink) HolderEvent(id string, ev wsholder.Event) {
	if h := l.hosted.Load(); h != nil {
		h.HolderEvent(id, ev)
	}
}

// onExit is the holder manager's OnExit: an exec job's holder goes to the
// hosted jobs, every other kind to next.
func (l *hostedExecLink) onExit(next func(wsholder.Descriptor, wsholder.Exit) error) func(wsholder.Descriptor, wsholder.Exit) error {
	return func(desc wsholder.Descriptor, exit wsholder.Exit) error {
		if _, ok := execjob.ExecHolderID(desc.Session); ok {
			if h := l.hosted.Load(); h != nil {
				h.HolderGone(desc, exit)
			}
			return nil
		}
		return next(desc, exit)
	}
}

// openHostedExec opens the hosted exec jobs over holders for runner's
// conversations and binds them to link. Run it under the daemon's lock,
// once the runner has started. A failure is logged and leaves the daemon on
// the legacy exec path, where the hosted exec tools refuse.
func openHostedExec(dir string, holders *wsholder.Manager, runner *wsagent.Runner, link *hostedExecLink, logw io.Writer) *execjob.Hosted {
	store, err := execjob.OpenHostedStore(filepath.Join(dir, hostedExecStoreName))
	if err != nil {
		fmt.Fprintf(logw, "dashboard: hosted exec unavailable: %v\n", err)
		return nil
	}
	hosted, err := execjob.NewHosted(execjob.HostedOptions{
		Holders:  execjob.HolderManager{Manager: holders},
		Store:    store,
		Sessions: execSessions{r: runner},
		ExitDir:  filepath.Join(dir, hostedExecExitsName),
		Log:      logw,
	})
	if err != nil {
		_ = store.Close()
		fmt.Fprintf(logw, "dashboard: hosted exec unavailable: %v\n", err)
		return nil
	}
	link.hosted.Store(hosted)
	hosted.Start()
	return hosted
}

// execSessions is the agent runner as the hosted exec jobs' owner side.
type execSessions struct{ r *wsagent.Runner }

func (s execSessions) Owner(id string) (execjob.HostedOwner, error) {
	o, err := s.r.ExecOwner(id)
	if err != nil {
		return execjob.HostedOwner{}, err
	}
	return execjob.HostedOwner{ID: o.ID, Gen: o.Gen, Lead: o.Lead}, nil
}

func (s execSessions) Approve(ctx context.Context, id string, call execjob.HostedCall) (bool, error) {
	grant, err := s.r.ApproveExec(ctx, id, wsagent.ExecApproval{Tool: call.Tool, Command: call.Command, Cwd: call.Cwd, Program: call.Program, Env: call.Env, Stdin: call.Stdin, StdinBytes: call.StdinBytes})
	return grant == wsagent.ExecGrantAnswer, err
}

func (s execSessions) Immediate(id string) (bool, error) { return s.r.ExecImmediate(id) }

func (s execSessions) Notify(id string, gen uint64, eventID, text string) (execjob.NoticeOutcome, error) {
	out, err := s.r.AdmitCompletion(id, gen, eventID, text)
	if err != nil {
		return 0, err
	}
	switch out {
	case wsagent.CompletionAccepted:
		return execjob.NoticeAccepted, nil
	case wsagent.CompletionDuplicate:
		return execjob.NoticeDuplicate, nil
	case wsagent.CompletionDeferred:
		return execjob.NoticeDeferred, nil
	}
	return execjob.NoticeDropped, nil
}
