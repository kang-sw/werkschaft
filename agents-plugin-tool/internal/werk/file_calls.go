package werk

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

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

type activeFileCall struct {
	ctx      context.Context
	id, tool string
	args     map[string]any
	recorded bool
}

func (a *Agent) beginFileCall(ctx context.Context, id, tool string, raw json.RawMessage) (func(), error) {
	var args map[string]any
	if id == "" || json.Unmarshal(raw, &args) != nil || args == nil {
		return nil, errors.New("the file call needs an identity and object arguments")
	}
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	if a.fileCall != nil {
		return nil, errors.New("another file call is active")
	}
	a.fileCall = &activeFileCall{ctx: ctx, id: id, tool: tool, args: args}
	return func() {
		a.fileMu.Lock()
		a.fileCall = nil
		a.fileMu.Unlock()
	}, nil
}

// RecordFileCall is called from the authenticated MCP handler while it
// holds the target. It accepts one intent for the active model call.
func (a *Agent) RecordFileCall(ctx context.Context, call wsfs.FileCall) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var args map[string]any
	if json.Unmarshal(call.Arguments, &args) != nil {
		return errors.New("invalid file call arguments")
	}
	a.fileMu.Lock()
	active := a.fileCall
	if active == nil || active.id != call.ID || active.tool != call.Tool || !reflect.DeepEqual(active.args, args) || active.recorded {
		a.fileMu.Unlock()
		return errors.New("no matching unrecorded file call is active")
	}
	if err := active.ctx.Err(); err != nil {
		a.fileMu.Unlock()
		return err
	}
	active.recorded = true
	err := a.log.addBarrier(fileIntentLine{K: "file_intent", CallID: call.ID, Intent: &call.Intent})
	a.fileMu.Unlock()
	if err != nil {
		return err
	}
	if err := a.log.Flush(); err != nil {
		return err
	}
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	if a.fileCall != active {
		return errors.New("the file call is no longer active")
	}
	if err := active.ctx.Err(); err != nil {
		return err
	}
	return ctx.Err()
}
