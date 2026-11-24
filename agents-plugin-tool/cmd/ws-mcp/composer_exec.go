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
	runner composerRunner
}

// composerRunner is the runner side the composer launch reads: the open
// hosted lead's own target, and the passive hand-off of a result.
type composerRunner interface {
	ComposerOpen(id string) (uint64, error)
	ComposerTarget(id string) (wsagent.ComposerTarget, error)
	PassiveHand(id string, gen uint64, execID, text string) error
}

// Open is the live authority of the dashboard's private command routes: the
// open generation of an open hosted lead, read from the runner's memory.
func (c composerExec) Open(replyID string) (uint64, error) { return c.runner.ComposerOpen(replyID) }

// Launch runs the `!` line as the lead's own exec.shell job, shell and
// working directory chosen as exec.shell does with no cwd argument, empty
// stdin, the user's explicit send as its one-use approval.
func (c composerExec) Launch(ctx context.Context, drain <-chan struct{}, req wsdashboard.ComposerLaunch) (wsdashboard.ComposerJob, error) {
	target, err := c.runner.ComposerTarget(req.ReplyID)
	if err != nil {
		return wsdashboard.ComposerJob{}, err
	}
	// A user command's line was read for one worktree: it never runs in another.
	if req.Root != "" && !sameRoot(req.Root, target.Root) {
		return wsdashboard.ComposerJob{}, errors.New("the lead's worktree changed since this command was read")
	}
	launch, err := execjob.ShellLaunch(target.Owner.ID, "exec.shell", target.Root, req.Cwd, req.Line, target.Environ, nil)
	if err != nil {
		return wsdashboard.ComposerJob{}, err
	}
	// Empty stdin: a reader gets EOF instead of waiting on an input the
	// composer lacks.
	launch.Stdin, launch.StdinSet = nil, true
	launch.Window = req.Window
	// The generation the route read before it acknowledged the submission, not
	// whatever is open by now: a lead closed and revived in between refuses it
	// here and again at the job's registration.
	if target.Owner.Gen != req.Gen {
		return wsdashboard.ComposerJob{}, errors.New("the lead reopened since this command was submitted")
	}
	launch.ExpectGen, launch.CheckGen = req.Gen, true
	launch.UserApproved = true
	// The job's origin for the HUD and its stop: the user's own command.
	launch.Origin = execjob.OriginUser
	gen := target.Owner.Gen
	launch.OnConvert = func(v execjob.HostedView) error {
		// The id is visible in the lead's pending input before the job
		// can owe its completion notice; a refusal is returned, and the
		// ordinary notice carries the id instead. PassiveHand checks and
		// stores under one hold, so it either stores the receipt or returns an
		// error with nothing stored: a refusal never leaves both the receipt
		// and the ordinary notice.
		return c.runner.PassiveHand(req.ReplyID, gen, v.ID, wsdashboard.ComposerAsyncReceipt(v.ID))
	}
	view, launchErr := c.hosted.Launch(ctx, drain, launch)
	if view.ID == "" {
		return wsdashboard.ComposerJob{}, launchErr
	}
	job := composerJob(view)
	job.Gen = gen
	// A converted job's end is the ordinary notice's; only a run that ended
	// inside its window has its output read back here.
	if view.Terminal() && !job.Async {
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

// The job HUD's service (wsdashboard.ExecJobsService) is the same binding: one
// definition of the dashboard's reach into the hosted exec jobs and the
// runner, so the `!` card and the HUD share the authority check (Open) and
// the owner, which is always the conversation the page is for.

// Jobs lists the lead's own jobs, memory only, with no output tails.
func (c composerExec) Jobs(owner string) []execjob.HostedSummary { return c.hosted.Jobs(owner) }

// ReadOutput is one bounded offset read of the lead's own job's merged output.
func (c composerExec) ReadOutput(owner, id string, from int64, limit int) (execjob.HostedOutput, error) {
	return c.hosted.ReadOutput(owner, id, from, limit)
}

// StopByUser is the user's stop of the lead's own job; the owner side of its
// notice is the runner (execSessions).
func (c composerExec) StopByUser(owner, id string) (execjob.HostedSummary, error) {
	return c.hosted.StopByUser(owner, id)
}

type execTreeRunner interface {
	ExecOwner(id string) (wsagent.ExecOwner, error)
	Subtree(rootID string) []wsagent.SubtreeNode
}

// OpenTree reports whether replyID is an open hosted conversation (lead or
// spawned worker), for recursive agent background exec monitoring.
func (c composerExec) OpenTree(replyID string) error {
	if tr, ok := c.runner.(execTreeRunner); ok {
		_, err := tr.ExecOwner(replyID)
		return err
	}
	_, err := c.runner.ComposerOpen(replyID)
	return err
}

// Subtree returns replyID (depth 0) and all of its recursive in-memory
// descendants, cycle-safe.
func (c composerExec) Subtree(replyID string) []wsdashboard.ExecTreeNode {
	tr, ok := c.runner.(execTreeRunner)
	if !ok {
		return []wsdashboard.ExecTreeNode{{ID: replyID, Label: replyID, Depth: 0}}
	}
	nodes := tr.Subtree(replyID)
	out := make([]wsdashboard.ExecTreeNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, wsdashboard.ExecTreeNode{
			ID:    n.ID,
			Label: n.Label,
			Depth: n.Depth,
			Path:  n.Path,
		})
	}
	return out
}

// AgentJobsForOwners returns all origin=="agent" jobs owned by any
// conversation in owners, sorted running-first.
func (c composerExec) AgentJobsForOwners(owners map[string]bool) []execjob.HostedSummary {
	return c.hosted.JobsForOwners(owners, execjob.OriginAgent)
}

// ReadAgentOutput reads output of an origin=="agent" job owned by any
// conversation in owners.
func (c composerExec) ReadAgentOutput(owners map[string]bool, id string, from int64, limit int) (execjob.HostedOutput, error) {
	return c.hosted.ReadOutputForOwners(owners, execjob.OriginAgent, id, from, limit)
}

// StopAgentByUser stops a running origin=="agent" job owned by any
// conversation in owners.
func (c composerExec) StopAgentByUser(owners map[string]bool, id string) (execjob.HostedSummary, error) {
	return c.hosted.StopByUserForOwners(owners, execjob.OriginAgent, id)
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

var (
	_ wsdashboard.ComposerExecService = composerExec{}
	_ wsdashboard.ExecJobsService     = composerExec{}
	_ wsdashboard.ExecTreeService     = composerExec{}
)
