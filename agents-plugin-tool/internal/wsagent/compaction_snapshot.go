package wsagent

import (
	"fmt"
	"slices"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
)

type compactionState struct {
	ChildrenInFlight []string
	ChildrenFinished []string
	ExecJobs         []string
}

type compactionExecLister interface {
	List(owner string) ([]execjob.HostedView, int)
}

func (r *Runner) compactionState(owner string) func(since time.Time) compactionState {
	return func(since time.Time) compactionState {
		var out compactionState
		children := r.Children(ParentRef{Kind: ParentConversation, ReplyID: owner})
		for _, child := range slices.Backward(children) {
			rec, ok := r.recordOf(child.ID)
			if !ok {
				continue
			}
			state := AgentStatus(child)
			line := fmt.Sprintf("%s · %s · %s", AgentAddress(child.ID), rec.Label, state)
			running := state == AgentRunning || state == AgentBackground || state == AgentStarting
			if running || child.Holding || rec.TurnOpen || len(rec.PendingReports) > 0 {
				out.ChildrenInFlight = append(out.ChildrenInFlight, line)
			} else if rec.LastTurn != nil || rec.ClosedAt != "" {
				when := lastActive(rec)
				if closed, err := time.Parse(time.RFC3339, rec.ClosedAt); err == nil && closed.After(when) {
					when = closed
				}
				if !since.IsZero() && when.Before(since) {
					continue
				}
				if rec.LastTurn != nil {
					line += " · " + reportSummary(rec.LastTurn.Result)
				}
				out.ChildrenFinished = append(out.ChildrenFinished, line)
			}
		}
		if jobs, ok := r.execJobs().(compactionExecLister); ok {
			views, _ := jobs.List(owner)
			for _, job := range views {
				out.ExecJobs = append(out.ExecJobs, fmt.Sprintf("%s · %s · %s", job.ID, job.Tool, job.State))
			}
		}
		return out
	}
}
