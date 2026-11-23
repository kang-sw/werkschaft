package werk

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/kang-sw/devenv/internal/execjob"
)

type approvalLine struct {
	K      string `json:"k"`
	CallID string `json:"call_id"`
}

func approvalTool(tool string) string {
	if name := canonicalFileTool(tool); name != "" {
		return name
	}
	if name := canonicalExecLaunch(tool); name != "" {
		return name
	}
	switch ModelToolName(tool) {
	case "exec_write":
		return "exec.write"
	case "exec_stop":
		return "exec.stop"
	case "exec_abort":
		return "exec.abort"
	default:
		return ""
	}
}

// permitMutation records that a lead is waiting before asking. The second
// barrier closes the replay window before a grant can reach the daemon.
func (a *Agent) permitMutation(ctx context.Context, call functionCall, tool string, args json.RawMessage) error {
	if a.cfg.Approve == nil || tool == "" {
		return nil
	}
	level := a.Authority()
	if level == AuthorityReadOnly {
		return errors.New("this read-only lead cannot run " + tool)
	}
	if level == AuthorityFull && !call.reask {
		return nil
	}
	if call.CallID == "" {
		return errors.New("the tool call needs an identity for approval")
	}
	if !call.reask {
		if err := a.writeApproval("approval_pending", call.CallID); err != nil {
			return err
		}
	}
	if err := a.cfg.Approve(ctx, tool, args); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.Authority() == AuthorityReadOnly {
		return errors.New("the lead became read-only while the tool was waiting")
	}
	return a.writeApproval("approval_dispatch", call.CallID)
}

// ApproveExecLaunch handles the resolved preview of the active MCP launch.
func (a *Agent) ApproveExecLaunch(ctx context.Context, call execjob.ModelLaunch, preview json.RawMessage) error {
	var args map[string]any
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return errors.New("invalid exec launch arguments")
	}
	a.mutationMu.Lock()
	active := a.mutationCall
	if a.cfg.Approve == nil || active == nil || active.id != call.ID || active.tool != call.Tool ||
		!reflect.DeepEqual(active.args, args) || active.approving || active.ctx.Err() != nil || ctx.Err() != nil {
		a.mutationMu.Unlock()
		return errors.New("no matching exec launch is waiting for approval")
	}
	active.approving = true
	a.mutationMu.Unlock()

	if err := a.permitMutation(ctx, functionCall{CallID: active.id, reask: active.reask}, active.tool, preview); err != nil {
		return err
	}
	a.mutationMu.Lock()
	defer a.mutationMu.Unlock()
	if a.mutationCall != active || active.ctx.Err() != nil || ctx.Err() != nil {
		return errors.New("the exec launch is no longer active")
	}
	active.approved = true
	return nil
}

func (a *Agent) writeApproval(kind, callID string) error {
	if err := a.log.addBarrier(approvalLine{K: kind, CallID: callID}); err != nil {
		return err
	}
	return a.log.Flush()
}

// Authority is the permission level used for the next tool call.
func (a *Agent) Authority() Authority {
	if level := a.leadAuthority.Load(); level != nil {
		return *level
	}
	return a.cfg.Authority
}

// SetAuthority changes the level observed by subsequent tool calls.
func (a *Agent) SetAuthority(level Authority) {
	a.leadAuthority.Store(&level)
}
