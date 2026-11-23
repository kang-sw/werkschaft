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
	ctx      context.Context
	id, tool string
	args     map[string]any
	recorded bool
}

func (a *Agent) beginMutationCall(ctx context.Context, id, tool string, raw json.RawMessage) (func(), error) {
	var args map[string]any
	if id == "" || json.Unmarshal(raw, &args) != nil || args == nil {
		return nil, errors.New("the mutation needs an identity and object arguments")
	}
	a.mutationMu.Lock()
	defer a.mutationMu.Unlock()
	if a.mutationCall != nil {
		return nil, errors.New("another mutating call is active")
	}
	a.mutationCall = &activeMutationCall{ctx: ctx, id: id, tool: tool, args: args}
	return func() {
		a.mutationMu.Lock()
		a.mutationCall = nil
		a.mutationMu.Unlock()
	}, nil
}

// RecordFileCall is called from the authenticated MCP handler while it
// holds the target. It accepts one intent for the active model call.
func (a *Agent) RecordFileCall(ctx context.Context, call wsfs.FileCall) error {
	return a.recordMutation(ctx, call.ID, call.Tool, call.Arguments, fileIntentLine{K: "file_intent", CallID: call.ID, Intent: &call.Intent})
}

func (a *Agent) RecordExecLaunch(ctx context.Context, call execjob.ModelLaunch) error {
	if call.JobID == "" || canonicalExecLaunch(call.Tool) == "" {
		return errors.New("the exec launch needs a hosted job")
	}
	return a.recordMutation(ctx, call.ID, call.Tool, call.Arguments, execJobLine{K: "exec_job", CallID: call.ID, JobID: call.JobID})
}

func (a *Agent) recordMutation(ctx context.Context, id, tool string, raw json.RawMessage, row any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil {
		return errors.New("invalid mutation arguments")
	}
	a.mutationMu.Lock()
	active := a.mutationCall
	if active == nil || active.id != id || active.tool != tool || !reflect.DeepEqual(active.args, args) || active.recorded {
		a.mutationMu.Unlock()
		return errors.New("no matching unrecorded mutation is active")
	}
	if err := active.ctx.Err(); err != nil {
		a.mutationMu.Unlock()
		return err
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
