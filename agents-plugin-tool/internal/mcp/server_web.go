package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

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
	limit := opts.MaxOutputBytes
	if limit <= 0 {
		limit = wsweb.DefaultMaxOutputBytes
	}
	return toolTextResponse(id, formatWebFetchResult(res, limit), nil)
}

func formatWebFetchResult(res wsweb.Result, maxBytes int) string {
	var hdr strings.Builder
	fmt.Fprintf(&hdr, "URL: %s\nStatus: %d", res.URL, res.StatusCode)
	if res.ContentType != "" {
		fmt.Fprintf(&hdr, "\nContent-Type: %s", res.ContentType)
	}
	if res.Title != "" {
		fmt.Fprintf(&hdr, "\nTitle: %s", res.Title)
	}
	header := hdr.String()
	if res.Text == "" {
		return clampUTF8(header, maxBytes)
	}
	out := header + "\n\n" + res.Text
	if maxBytes <= 0 || len(out) <= maxBytes {
		return out
	}
	avail := maxBytes - len(header) - 2
	if avail <= 0 {
		return clampUTF8(header, maxBytes)
	}
	return header + "\n\n" + clampUTF8(res.Text, avail)
}

func clampUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	const marker = "\n[... truncated ...]\n"
	if maxBytes <= len(marker)+16 {
		n := maxBytes
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		return s[:n]
	}
	half := (maxBytes - len(marker)) / 2
	headEnd := half
	for headEnd > 0 && !utf8.RuneStart(s[headEnd]) {
		headEnd--
	}
	tailStart := len(s) - (maxBytes - len(marker) - headEnd)
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	if tailStart <= headEnd {
		return s[:headEnd]
	}
	return s[:headEnd] + marker + s[tailStart:]
}
