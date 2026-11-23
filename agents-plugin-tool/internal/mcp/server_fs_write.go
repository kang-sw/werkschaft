package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/mcpframe"
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
	authority := binding.AuthorityForCall()
	files := wsfs.Files{
		Root: binding.Root, Cwd: binding.Cwd, Authority: authority,
		RecordMutation: func(ctx context.Context, intent wsfs.FileIntent) error {
			return binding.Record(ctx, FileWriteCall{ID: callID, Tool: name, Arguments: raw, Authority: authority, Intent: intent})
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
