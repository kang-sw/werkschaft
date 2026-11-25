package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsweb"
)

func isWebFetchTool(name string) bool { return name == "web.fetch" }

// webFetchTools reports whether this connection may list and invoke web.fetch.
// Admission requires a trusted hosted conversation binding whose backend
// registration declares LifecyclePolicy.WebFetchTool; neither an untrusted
// harness hint nor an explicit tool allowlist alone grants access.
func (s *Server) webFetchTools() bool {
	if s.conversation == "" {
		return false
	}
	if s.fileWrites != nil && s.fileWrites.CurrentAuthority != nil && !s.fileWrites.CurrentAuthority().Valid() {
		return false
	}
	var backend string
	if s.callerBinding != nil {
		backend = s.agentAdmissionBackend()
	} else {
		backend = s.execCutover
	}
	return backend != "" && s.lifecycleOf(backend).WebFetchTool
}

func (s *Server) callWebFetch(ctx context.Context, id json.RawMessage, args map[string]any) response {
	rawURL, _ := args["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return toolTextResponse(id, "", fmt.Errorf("web.fetch: url is required"))
	}
	fetchCtx := ctx
	if s.drainCtx != nil {
		var cancel context.CancelFunc
		fetchCtx, cancel = context.WithCancel(ctx)
		stop := context.AfterFunc(s.drainCtx, cancel)
		defer func() {
			stop()
			cancel()
		}()
	}
	opts := s.webFetchOpts
	res, err := wsweb.Fetch(fetchCtx, rawURL, opts)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("web.fetch: %w", err))
	}
	return toolTextResponse(id, res.Text, nil)
}
