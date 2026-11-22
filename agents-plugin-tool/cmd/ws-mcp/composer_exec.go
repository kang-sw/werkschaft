package main

import (
	"context"
	"errors"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// composerExec is the dashboard's narrow launch dependency
// (wsdashboard.ComposerExecService) over the same hosted exec jobs and runner
// the lead's exec tools use. Owner, generation, bound worktree and
// environment come from the runner, never from the browser.
type composerExec struct {
	hosted *execjob.Hosted
	runner *wsagent.Runner
}

// Launch runs the `!` line as the lead's own exec.shell job, shell and
// working directory chosen as exec.shell does with no cwd argument, empty
// stdin, the user's explicit send as its one-use approval.
func (c composerExec) Launch(ctx context.Context, drain <-chan struct{}, req wsdashboard.ComposerLaunch) (wsdashboard.ComposerJob, error) {
	target, err := c.runner.ComposerTarget(req.ReplyID)
	if err != nil {
		return wsdashboard.ComposerJob{}, err
	}
	launch, err := execjob.ShellLaunch(target.Owner.ID, "exec.shell", target.Root, "", req.Line, target.Environ, nil)
	if err != nil {
		return wsdashboard.ComposerJob{}, err
	}
	// Empty stdin: a reader gets EOF instead of waiting on an input the
	// composer lacks.
	launch.Stdin, launch.StdinSet = nil, true
	launch.Window = req.Window
	launch.ExpectGen, launch.CheckGen = target.Owner.Gen, true
	launch.UserApproved = true
	gen := target.Owner.Gen
	launch.OnConvert = func(v execjob.HostedView) {
		// The id is visible in the lead's pending input before the job
		// can owe its completion notice.
		_ = c.runner.PassiveHand(req.ReplyID, gen, v.ID, wsdashboard.ComposerAsyncReceipt(req.Line, v.ID))
	}
	view, launchErr := c.hosted.Launch(ctx, drain, launch)
	if view.ID == "" {
		return wsdashboard.ComposerJob{}, launchErr
	}
	job := composerJob(view)
	job.Gen = gen
	if view.Terminal() {
		_, out, err := c.hosted.Retained(req.ReplyID, view.ID)
		if err == nil {
			job.Output, job.Start = out.Data, out.Start
		}
	}
	return job, launchErr
}

func (c composerExec) Job(replyID, execID string) (wsdashboard.ComposerJob, error) {
	view, err := c.hosted.Get(replyID, execID)
	if err != nil {
		return wsdashboard.ComposerJob{}, err
	}
	job := composerJob(view)
	job.Gen = view.Gen
	if view.Terminal() {
		_, out, err := c.hosted.Retained(replyID, execID)
		if err != nil {
			return wsdashboard.ComposerJob{}, err
		}
		job.Output, job.Start = out.Data, out.Start
	}
	return job, nil
}

func (c composerExec) Hand(replyID string, gen uint64, execID, text string) error {
	if execID == "" {
		return errors.New("no exec id")
	}
	return c.runner.PassiveHand(replyID, gen, execID, text)
}

// composerJob is a hosted view as the dashboard's card reads it.
func composerJob(v execjob.HostedView) wsdashboard.ComposerJob {
	command := v.Command
	if command == "" {
		command = v.ID
	}
	return wsdashboard.ComposerJob{
		ExecID: v.ID, State: v.State, ExitCode: v.ExitCode, Signal: v.Signal, Error: v.Error, Command: command,
		Async:     v.Outcome == execjob.OutcomeAsync || v.Outcome == execjob.OutcomeConverted || v.Notify,
		StartedAt: v.StartedAt, EndedAt: v.EndedAt, Written: v.Written[execjob.StreamCombined],
	}
}
