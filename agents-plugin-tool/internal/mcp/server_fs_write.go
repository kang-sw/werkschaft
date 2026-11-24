package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/mcpframe"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsfs"
)

type FileWriteSession = wsfs.FileWriteSession
type FileWriteCall = wsfs.FileCall

func isFSWriteTool(name string) bool { return name == "fs.edit" || name == "fs.write" }

func (s *Server) fileWriteTools() bool {
	binding := s.fileWrites
	if s.conversation == "" || binding == nil || binding.Record == nil || s.readOnlyAuthority() {
		return false
	}
	authority := binding.AuthorityForCall()
	if authority != wsfs.AuthorityFull &&
		(authority != wsfs.AuthorityWorkdirWrite || !filepath.IsAbs(binding.Root)) {
		return false
	}
	backend := s.execCutover
	if backend == "" {
		backend = s.agentAdmissionBackend()
	}
	return s.lifecycleOf(backend).FileWriteTools
}

func (s *Server) callFSWrite(ctx context.Context, id json.RawMessage, name string, args, meta map[string]any) response {
	callID, _ := meta[mcpframe.ToolCallIDMeta].(string)
	if strings.TrimSpace(callID) == "" {
		return toolTextResponse(id, "", fmt.Errorf("%s: the active model call identity is required", name))
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	binding := s.fileWrites
	call := FileWriteCall{ID: callID, Tool: name, Arguments: raw}
	release, err := admitFileWrite(ctx, binding, &call)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", name, err))
	}
	defer release()
	files := wsfs.Files{
		Root: binding.Root, Cwd: binding.Cwd, Authority: call.Authority,
		Protection: func() wsfs.FileProtection { return wsdashboard.NewModelFileWriteProtection() },
		RecordMutation: func(ctx context.Context, intent wsfs.FileIntent) error {
			call.Intent = intent
			return binding.Record(ctx, call)
		},
	}
	var text string
	if name == "fs.edit" {
		text, err = files.Edit(ctx, raw)
	} else {
		text, err = files.Write(ctx, raw)
	}
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", name, err))
	}
	return toolTextResponse(id, text, nil)
}

func admitFileWrite(ctx context.Context, binding *FileWriteSession, call *FileWriteCall) (func(), error) {
	release := func() {}
	if binding.HoldAuthority != nil {
		release = binding.HoldAuthority()
	}
	call.Authority = binding.AuthorityForCall()
	if call.Authority == wsfs.AuthorityWorkdirWrite && binding.Approve != nil {
		release()
		if err := binding.Approve(ctx, *call); err != nil {
			return nil, err
		}
		if binding.HoldAuthority != nil {
			release = binding.HoldAuthority()
		}
		call.Authority = binding.AuthorityForCall()
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
