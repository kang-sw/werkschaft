package mcp

import (
	"fmt"
	"io"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wswake"
)

// AgentTurnReporter is the daemon runner's Options.Report: it posts each
// settled turn of a lead's spawned child to the lead's reply-id queue as a
// system message labelled agent:<id> and wakes the mailbox waits. The
// registry is the one the daemon's own environment resolves, as for every
// session it hosts. A failed post is logged to log.
func AgentTurnReporter(log io.Writer) func(wsagent.TurnReport) {
	return func(rep wsagent.TurnReport) {
		if rep.Parent.Kind != wsagent.ParentLead {
			return
		}
		address := wsagent.AgentAddress(rep.Result.ID)
		content := fmt.Sprintf("Reply with mailbox.send(to: %q).\n%s", address, wsagent.ReportText(rep))
		if err := (wsmailbox.Paths{}).Store().PostSystem(rep.Parent.ReplyID, "agent:"+rep.Result.ID, content, time.Now()); err != nil {
			if log != nil {
				fmt.Fprintf(log, "agent %s: report to %s: %v\n", rep.Result.ID, rep.Parent.ReplyID, err)
			}
			return
		}
		wswake.Mailbox.Wake()
	}
}
