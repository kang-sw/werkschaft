package werk

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsfs"
)

// canonicalFileTool maps a daemon-advertised alias to the name the handler sees.
func canonicalFileTool(tool string) string {
	switch tool {
	case "fs_write", "fs.write":
		return "fs.write"
	case "fs_edit", "fs.edit":
		return "fs.edit"
	default:
		return ""
	}
}

func canonicalExecLaunch(tool string) string {
	switch tool {
	case "exec_run", "exec.run":
		return "exec.run"
	case "exec_shell", "exec.shell":
		return "exec.shell"
	case "exec_spawn", "exec.spawn":
		return "exec.spawn"
	default:
		return ""
	}
}

type activeMutationCall struct {
	ctx                                                context.Context
	id, tool                                           string
	args                                               map[string]any
	recorded, reask, approving, explicit, execAdmitted bool
}

func (a *Agent) beginMutationCall(ctx context.Context, id, tool string, raw json.RawMessage, reask, explicit bool) (func(), error) {
	var args map[string]any
	if id == "" || json.Unmarshal(raw, &args) != nil || args == nil {
		return nil, errors.New("the mutation needs an identity and object arguments")
	}
	a.mutationMu.Lock()
	defer a.mutationMu.Unlock()
	if a.mutationCall != nil {
		return nil, errors.New("another mutating call is active")
	}
	a.mutationCall = &activeMutationCall{ctx: ctx, id: id, tool: tool, args: args, reask: reask, explicit: explicit}
	return func() {
		a.mutationMu.Lock()
		a.mutationCall = nil
		a.mutationMu.Unlock()
	}, nil
}

// ApproveFileCall obtains or confirms the operator grant for the active
// scoped call before MCP enters its permission admission guard.
func (a *Agent) ApproveFileCall(ctx context.Context, call wsfs.FileCall) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.cfg.Approve == nil {
		return nil
	}
	var args map[string]any
	if json.Unmarshal(call.Arguments, &args) != nil {
		return errors.New("invalid mutation arguments")
	}
	a.mutationMu.Lock()
	active := a.mutationCall
	if active == nil || active.id != call.ID || active.tool != call.Tool || !reflect.DeepEqual(active.args, args) ||
		active.recorded || active.approving || active.ctx.Err() != nil {
		a.mutationMu.Unlock()
		return errors.New("no matching file call is waiting for approval")
	}
	if active.explicit {
		a.mutationMu.Unlock()
		return nil
	}
	active.approving = true
	a.mutationMu.Unlock()

	explicit, err := a.permitMutation(ctx, functionCall{CallID: call.ID, reask: active.reask}, call.Tool, call.Arguments, true)
	a.mutationMu.Lock()
	defer a.mutationMu.Unlock()
	active.approving = false
	if err != nil {
		return err
	}
	if a.mutationCall != active || active.ctx.Err() != nil || ctx.Err() != nil {
		return errors.New("the file call is no longer active")
	}
	active.explicit = explicit
	return nil
}

// RecordFileCall is called from the authenticated MCP handler while it
// holds the target. It accepts one intent for the active model call.
func (a *Agent) RecordFileCall(ctx context.Context, call wsfs.FileCall) error {
	return a.recordMutation(ctx, call.ID, call.Tool, call.Arguments, fileIntentLine{K: "file_intent", CallID: call.ID, Intent: &call.Intent}, call.Authority)
}

func (a *Agent) RecordExecLaunch(ctx context.Context, call execjob.ModelLaunch) error {
	if call.JobID == "" || canonicalExecLaunch(call.Tool) == "" {
		return errors.New("the exec launch needs a hosted job")
	}
	return a.recordMutation(ctx, call.ID, call.Tool, call.Arguments, execJobLine{K: "exec_job", CallID: call.ID, JobID: call.JobID}, "")
}

func (a *Agent) recordMutation(ctx context.Context, id, tool string, raw json.RawMessage, row any, fileAuthority wsfs.Authority) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil {
		return errors.New("invalid mutation arguments")
	}
	a.mutationMu.Lock()
	active := a.mutationCall
	if active == nil || active.id != id || active.tool != tool || !reflect.DeepEqual(active.args, args) || active.recorded || active.approving ||
		(a.cfg.Approve != nil && canonicalExecLaunch(tool) != "" && !active.execAdmitted) {
		a.mutationMu.Unlock()
		return errors.New("no matching unrecorded mutation is active")
	}
	if err := active.ctx.Err(); err != nil {
		a.mutationMu.Unlock()
		return err
	}
	if a.cfg.Approve != nil && !active.explicit && fileAuthority == wsfs.AuthorityWorkdirWrite {
		a.mutationMu.Unlock()
		return errors.New("the scoped file call needs operator approval")
	}
	active.recorded = true
	err := a.log.addBarrier(row)
	a.mutationMu.Unlock()
	if err != nil {
		return err
	}
	if err := a.log.Flush(); err != nil {
		return err
	}
	a.mutationMu.Lock()
	defer a.mutationMu.Unlock()
	if a.mutationCall != active {
		return errors.New("the mutation is no longer active")
	}
	if err := active.ctx.Err(); err != nil {
		return err
	}
	return ctx.Err()
}
