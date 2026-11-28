package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

const (
	convSessionsTool = "conv.sessions"
	convSearchTool   = "conv.search"
	convHistoryTool  = "conv.history"
	convReadTool     = "conv.read"
	convRevealTool   = "conv.reveal"
)

// ConvHost bridges the MCP server to the dashboard daemon's conversation
// history index, live permission check, and rail reveal publisher
// (*wsdashboard.Daemon).
type ConvHost interface {
	ConvHistoryAccess(replyID string) bool
	ConvSessions(ctx context.Context, req wsdashboard.ConvSessionsRequest) (wsdashboard.ConvSessionsResponse, error)
	ConvSearch(ctx context.Context, req wsdashboard.ConvSearchRequest) (wsdashboard.ConvSearchResponse, error)
	ConvHistory(ctx context.Context, req wsdashboard.ConvHistoryRequest) (wsdashboard.ConvHistoryResponse, error)
	ConvRead(ctx context.Context, req wsdashboard.ConvReadRequest) (wsdashboard.ConvReadResponse, error)
	ConvRevealSession(ctx context.Context, req wsdashboard.ConvRevealRequest) (wsdashboard.ConvRevealResponse, error)
}

var _ ConvHost = (*wsdashboard.Daemon)(nil)

func isConvTool(name string) bool {
	switch name {
	case convSessionsTool, convSearchTool, convHistoryTool, convReadTool, convRevealTool:
		return true
	default:
		return false
	}
}

// convToolsListed reports whether this connection advertises conv.* tools in
// tools/list and agy's directTools. Uses the trusted process start-time
// snapshot bound from ProxyHello (never forgeable by caller).
func (s *Server) convToolsListed() bool {
	return s.conversation != "" && s.convHistoryListed
}

// convAccessRefusal enforces live call-time authorization against the session's
// persisted conv_history_access flag. Revoking permission blocks calls
// immediately even before process stop; enabling before restart allows calls
// by name while schemas remain unlisted until restart (decision 1643).
func (s *Server) convAccessRefusal(name string) string {
	if !isConvTool(name) {
		return ""
	}
	if s.conversation == "" {
		return fmt.Sprintf("%s: permission denied: conversation history tools require a dashboard-hosted lead session with conversation history access enabled", name)
	}
	if s.conv == nil {
		return fmt.Sprintf("%s: conversation history host is not available in this session", name)
	}
	if !s.conv.ConvHistoryAccess(s.conversation) {
		return fmt.Sprintf("%s: permission denied: conversation history access is disabled for this session (enable it in the session settings popover)", name)
	}
	return ""
}

func convTools() []map[string]any {
	return []map[string]any{
		{
			"name":        convSessionsTool,
			"description": "List and filter past and active dashboard sessions (metadata only, no message bodies). Defaults to the calling session's current workspace; pass workspace: \"all\" for all workspaces.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key":      stringProperty(sessionKeyPropertyDescription),
					"workspace":        stringProperty(`Workspace path to filter by. Defaults to the current workspace; pass "all" to include every workspace.`),
					"status":           enumStringProperty(`Session status filter: "active", "archived", "closed", or "all" (default "all").`, []string{"active", "archived", "closed", "all"}),
					"include_children": boolProperty("Include child/worker sessions in addition to top-level leads. Defaults to false."),
					"query":            stringProperty("Optional case-insensitive substring filter matching session ID, label, workspace, harness, or model."),
					"since":            stringProperty("Optional inclusive lower bound on last activity (RFC3339 or YYYY-MM-DD UTC)."),
					"until":            stringProperty("Optional inclusive upper bound on last activity (RFC3339 or YYYY-MM-DD UTC)."),
					"limit":            integerProperty("Maximum sessions to return. Defaults to 20; max 100."),
					"offset":           integerProperty("Pagination offset. Defaults to 0."),
					"format":           stringProperty(formatPropertyDescription),
				},
			},
		},
		{
			"name":        convSearchTool,
			"description": "Full-text search across indexed conversation display rings using SQLite FTS5. Defaults to the calling session's current workspace; pass workspace: \"all\" to search every workspace.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key":   stringProperty(sessionKeyPropertyDescription),
					"query":         stringProperty("Search query (required). Supports plain words and quoted phrases."),
					"session_id":    stringProperty("Optional session reply-id to restrict the search to one session."),
					"workspace":     stringProperty(`Workspace path to filter by (ignored when session_id is set). Defaults to the current workspace; pass "all" for all workspaces.`),
					"status":        enumStringProperty(`Session status filter when session_id is unset: "active", "archived", "closed", or "all" (default "all").`, []string{"active", "archived", "closed", "all"}),
					"kinds":         stringArrayProperty("Optional entry kind filters (e.g. user, reply, mail, tools, compact, notice, command, whiteboard-effects)."),
					"since":         stringProperty("Optional inclusive lower timestamp bound (RFC3339 or YYYY-MM-DD UTC)."),
					"until":         stringProperty("Optional inclusive upper timestamp bound (RFC3339 or YYYY-MM-DD UTC)."),
					"order":         enumStringProperty(`Sort order: "relevance" (default BM25) or "time" (newest first).`, []string{"relevance", "time"}),
					"limit":         integerProperty("Maximum matching entries to return. Defaults to 20; max 50."),
					"snippet_chars": integerProperty("Maximum snippet characters per hit. Defaults to 200; max 500."),
					"format":        stringProperty(formatPropertyDescription),
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        convHistoryTool,
			"description": "Return a compact chronological index of conversation ring entries for one session (entry_id, timestamp, kind, preview, byte size, truncated flag) without returning full message bodies.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key":   stringProperty(sessionKeyPropertyDescription),
					"session_id":    stringProperty("Target session reply-id (required)."),
					"kinds":         stringArrayProperty("Optional entry kind filters (e.g. user, reply, mail, tools, compact, notice, command)."),
					"before":        stringProperty("Cursor: return entries strictly before this entry_id."),
					"after":         stringProperty("Cursor: return entries strictly after this entry_id."),
					"since":         stringProperty("Optional inclusive lower timestamp bound (RFC3339 or YYYY-MM-DD UTC)."),
					"until":         stringProperty("Optional inclusive upper timestamp bound (RFC3339 or YYYY-MM-DD UTC)."),
					"limit":         integerProperty("Maximum entries to return. Defaults to 20; max 100."),
					"snippet_chars": integerProperty("Maximum preview characters per entry. Defaults to 160; max 400."),
					"format":        stringProperty(formatPropertyDescription),
				},
				"required": []string{"session_id"},
			},
		},
		{
			"name":        convReadTool,
			"description": "Read verbatim display text from one conversation ring entry (by exact or unambiguous prefix entry_id) with byte_offset/max_bytes pagination, or read a bounded window around around_entry_id.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key":     stringProperty(sessionKeyPropertyDescription),
					"session_id":      stringProperty("Target session reply-id (required)."),
					"entry_id":        stringProperty("Exact or unambiguous prefix of one entry_id to read. Mutually exclusive with around_entry_id."),
					"around_entry_id": stringProperty("Exact or unambiguous prefix of a center entry_id to read with surrounding context. Mutually exclusive with entry_id."),
					"context_before":  integerProperty("Entries to include before around_entry_id. Defaults to 2; max 10."),
					"context_after":   integerProperty("Entries to include after around_entry_id. Defaults to 2; max 10."),
					"max_bytes":       integerProperty("Maximum UTF-8 bytes returned. Defaults to 16384 (16 KiB); max 65536 (64 KiB)."),
					"byte_offset":     integerProperty("Byte offset for paginating a single entry_id read. Defaults to 0."),
					"format":          stringProperty(formatPropertyDescription),
				},
				"required": []string{"session_id"},
			},
		},
		{
			"name":        convRevealTool,
			"description": "Highlight a session in the dashboard rail (with an optional message entry jump badge) and optionally revive an archived or non-pruned closed top-level lead session.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"session_id":  stringProperty("Target session reply-id (required)."),
					"entry_id":    stringProperty("Optional ring entry_id to attach as the #entry=<key> jump target."),
					"revive":      boolProperty("When true, unhide an archived lead or reopen a non-pruned closed lead into the live rail before revealing. Defaults to false."),
					"focus":       boolProperty("When true, instruct the active/focused browser tab to navigate immediately to /s/<session_id>#entry=<entry_id>. Defaults to false."),
					"reason":      stringProperty("Optional short human-facing reason shown with the reveal."),
					"format":      stringProperty(formatPropertyDescription),
				},
				"required": []string{"session_id"},
			},
		},
	}
}

func (s *Server) handleConvTool(ctx context.Context, id json.RawMessage, name string, args map[string]any, meta map[string]any) response {
	if refusal := s.convAccessRefusal(name); refusal != "" {
		return toolErrorTextResponse(id, refusal)
	}
	switch name {
	case convSessionsTool:
		return s.handleConvSessions(ctx, id, args, meta)
	case convSearchTool:
		return s.handleConvSearch(ctx, id, args, meta)
	case convHistoryTool:
		return s.handleConvHistory(ctx, id, args)
	case convReadTool:
		return s.handleConvRead(ctx, id, args)
	case convRevealTool:
		return s.handleConvReveal(ctx, id, args)
	default:
		return errorResponse(id, -32602, fmt.Sprintf("unknown tool: %s", name))
	}
}

func (s *Server) resolveConvCallerWorkspace(args map[string]any, meta map[string]any) string {
	if key, _ := args["session_key"].(string); strings.TrimSpace(key) != "" {
		if root, err := s.resolveToolRoot(args, meta); err == nil && strings.TrimSpace(root) != "" {
			return root
		}
	}
	root := strings.TrimSpace(s.root)
	if root != "" && root != "." {
		return root
	}
	return ""
}

func parseConvTimeBound(raw string, endOfDay bool) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC().UnixMilli(), nil
		}
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		if endOfDay {
			t = t.UTC().Add(24*time.Hour - time.Millisecond)
		}
		return t.UTC().UnixMilli(), nil
	}
	return 0, fmt.Errorf("invalid timestamp %q: expected RFC3339 or YYYY-MM-DD", raw)
}

func optionalStringArg(args map[string]any, name string) (string, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return "", nil
	}
	val, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", name)
	}
	return val, nil
}

func (s *Server) handleConvSessions(ctx context.Context, id json.RawMessage, args map[string]any, meta map[string]any) response {
	const tool = convSessionsTool
	workspace, err := optionalStringArg(args, "workspace")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	status, err := optionalStringArg(args, "status")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	query, err := optionalStringArg(args, "query")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	sinceRaw, err := optionalStringArg(args, "since")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	untilRaw, err := optionalStringArg(args, "until")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	sinceMs, err := parseConvTimeBound(sinceRaw, false)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: since: %w", tool, err))
	}
	untilMs, err := parseConvTimeBound(untilRaw, true)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: until: %w", tool, err))
	}
	limit, err := exactIntegerArgument(args, "limit", wsdashboard.DefaultConvSessionsLimit)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	offset, err := exactIntegerArgument(args, "offset", 0)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if offset < 0 {
		return toolTextResponse(id, "", fmt.Errorf("%s: offset must be 0 or greater", tool))
	}
	res, err := s.conv.ConvSessions(ctx, wsdashboard.ConvSessionsRequest{
		CallerWorkspace: s.resolveConvCallerWorkspace(args, meta),
		Workspace:       workspace,
		Status:          status,
		IncludeChildren: boolArgument(args["include_children"]),
		Query:           query,
		SinceMs:         sinceMs,
		UntilMs:         untilMs,
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if wantsJSON(args) {
		return toolJSONResponse(id, res, nil)
	}
	return toolTextResponse(id, formatConvSessionsText(res), nil)
}

func (s *Server) handleConvSearch(ctx context.Context, id json.RawMessage, args map[string]any, meta map[string]any) response {
	const tool = convSearchTool
	query, err := optionalStringArg(args, "query")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if strings.TrimSpace(query) == "" {
		return toolTextResponse(id, "", fmt.Errorf("%s: query is required", tool))
	}
	sessionID, err := optionalStringArg(args, "session_id")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	workspace, err := optionalStringArg(args, "workspace")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	status, err := optionalStringArg(args, "status")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	order, err := optionalStringArg(args, "order")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	sinceRaw, err := optionalStringArg(args, "since")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	untilRaw, err := optionalStringArg(args, "until")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	sinceMs, err := parseConvTimeBound(sinceRaw, false)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: since: %w", tool, err))
	}
	untilMs, err := parseConvTimeBound(untilRaw, true)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: until: %w", tool, err))
	}
	limit, err := exactIntegerArgument(args, "limit", 0)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	snippetChars, err := exactIntegerArgument(args, "snippet_chars", 0)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	res, err := s.conv.ConvSearch(ctx, wsdashboard.ConvSearchRequest{
		CallerWorkspace: s.resolveConvCallerWorkspace(args, meta),
		Query:           query,
		SessionID:       strings.TrimSpace(sessionID),
		Workspace:       workspace,
		Status:          status,
		Kinds:           stringList(args["kinds"]),
		SinceMs:         sinceMs,
		UntilMs:         untilMs,
		Order:           order,
		Limit:           limit,
		SnippetChars:    snippetChars,
	})
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if wantsJSON(args) {
		return toolJSONResponse(id, res, nil)
	}
	return toolTextResponse(id, formatConvSearchText(res), nil)
}

func (s *Server) handleConvHistory(ctx context.Context, id json.RawMessage, args map[string]any) response {
	const tool = convHistoryTool
	sessionID, err := optionalStringArg(args, "session_id")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if strings.TrimSpace(sessionID) == "" {
		return toolTextResponse(id, "", fmt.Errorf("%s: session_id is required", tool))
	}
	before, err := optionalStringArg(args, "before")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	after, err := optionalStringArg(args, "after")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	sinceRaw, err := optionalStringArg(args, "since")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	untilRaw, err := optionalStringArg(args, "until")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	sinceMs, err := parseConvTimeBound(sinceRaw, false)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: since: %w", tool, err))
	}
	untilMs, err := parseConvTimeBound(untilRaw, true)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: until: %w", tool, err))
	}
	limit, err := exactIntegerArgument(args, "limit", 0)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	snippetChars, err := exactIntegerArgument(args, "snippet_chars", 0)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	res, err := s.conv.ConvHistory(ctx, wsdashboard.ConvHistoryRequest{
		SessionID:    strings.TrimSpace(sessionID),
		Kinds:        stringList(args["kinds"]),
		Before:       strings.TrimSpace(before),
		After:        strings.TrimSpace(after),
		SinceMs:      sinceMs,
		UntilMs:      untilMs,
		Limit:        limit,
		SnippetChars: snippetChars,
	})
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if wantsJSON(args) {
		return toolJSONResponse(id, res, nil)
	}
	return toolTextResponse(id, formatConvHistoryText(res), nil)
}

func (s *Server) handleConvRead(ctx context.Context, id json.RawMessage, args map[string]any) response {
	const tool = convReadTool
	sessionID, err := optionalStringArg(args, "session_id")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if strings.TrimSpace(sessionID) == "" {
		return toolTextResponse(id, "", fmt.Errorf("%s: session_id is required", tool))
	}
	entryID, err := optionalStringArg(args, "entry_id")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	aroundID, err := optionalStringArg(args, "around_entry_id")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	rawBefore, hasBefore := args["context_before"]
	if rawBefore == nil {
		hasBefore = false
	}
	rawAfter, hasAfter := args["context_after"]
	if rawAfter == nil {
		hasAfter = false
	}
	contextBefore := 0
	if hasBefore {
		contextBefore, err = exactIntegerArgument(args, "context_before", 0)
		if err != nil {
			return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
		}
		if contextBefore < 0 {
			return toolTextResponse(id, "", fmt.Errorf("%s: context_before must be 0 or greater", tool))
		}
	}
	contextAfter := 0
	if hasAfter {
		contextAfter, err = exactIntegerArgument(args, "context_after", 0)
		if err != nil {
			return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
		}
		if contextAfter < 0 {
			return toolTextResponse(id, "", fmt.Errorf("%s: context_after must be 0 or greater", tool))
		}
	}
	maxBytes, err := exactIntegerArgument(args, "max_bytes", 0)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if maxBytes < 0 {
		return toolTextResponse(id, "", fmt.Errorf("%s: max_bytes must be 0 or greater", tool))
	}
	byteOffset, err := exactIntegerArgument(args, "byte_offset", 0)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if byteOffset < 0 {
		return toolTextResponse(id, "", fmt.Errorf("%s: byte_offset must be 0 or greater", tool))
	}
	res, err := s.conv.ConvRead(ctx, wsdashboard.ConvReadRequest{
		SessionID:     strings.TrimSpace(sessionID),
		EntryID:       strings.TrimSpace(entryID),
		AroundEntryID: strings.TrimSpace(aroundID),
		ContextBefore: contextBefore,
		ContextAfter:  contextAfter,
		HasBefore:     hasBefore,
		HasAfter:      hasAfter,
		ByteOffset:    byteOffset,
		MaxBytes:      maxBytes,
	})
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if wantsJSON(args) {
		return toolJSONResponse(id, res, nil)
	}
	return toolTextResponse(id, formatConvReadText(res), nil)
}

func (s *Server) handleConvReveal(ctx context.Context, id json.RawMessage, args map[string]any) response {
	const tool = convRevealTool
	sessionID, err := optionalStringArg(args, "session_id")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if strings.TrimSpace(sessionID) == "" {
		return toolTextResponse(id, "", fmt.Errorf("%s: session_id is required", tool))
	}
	entryID, err := optionalStringArg(args, "entry_id")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	reason, err := optionalStringArg(args, "reason")
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	res, err := s.conv.ConvRevealSession(ctx, wsdashboard.ConvRevealRequest{
		SessionID: strings.TrimSpace(sessionID),
		EntryID:   strings.TrimSpace(entryID),
		Revive:    boolArgument(args["revive"]),
		Focus:     boolArgument(args["focus"]),
		Reason:    strings.TrimSpace(reason),
	})
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", tool, err))
	}
	if wantsJSON(args) {
		return toolJSONResponse(id, res, nil)
	}
	return toolTextResponse(id, formatConvRevealText(res), nil)
}

func formatConvTS(tsMs int64) string {
	if tsMs <= 0 {
		return "-"
	}
	return time.UnixMilli(tsMs).UTC().Format(time.RFC3339)
}

func formatConvSessionsText(res wsdashboard.ConvSessionsResponse) string {
	var b strings.Builder
	if res.Indexing != "" {
		fmt.Fprintf(&b, "indexing: %s\n", res.Indexing)
	}
	if len(res.Sessions) == 0 {
		b.WriteString("no matching sessions\n")
		return b.String()
	}
	for _, s := range res.Sessions {
		fmt.Fprintf(&b, "%s  [%s]", s.SessionID, s.Status)
		if s.Label != "" {
			fmt.Fprintf(&b, "  %q", s.Label)
		}
		fmt.Fprintf(&b, "  entries=%d", s.EntryCount)
		if s.Harness != "" {
			if s.Model != "" {
				fmt.Fprintf(&b, "  %s/%s", s.Harness, s.Model)
			} else {
				fmt.Fprintf(&b, "  %s", s.Harness)
			}
		}
		if s.Parent != "" {
			fmt.Fprintf(&b, "  parent=%s", s.Parent)
		} else if s.IsChild {
			b.WriteString("  child=true")
		}
		if s.LastActiveAt != "" {
			fmt.Fprintf(&b, "  last=%s", s.LastActiveAt)
		}
		if s.Workspace != "" {
			fmt.Fprintf(&b, "  ws=%s", s.Workspace)
		}
		b.WriteByte('\n')
	}
	if res.Total > len(res.Sessions) {
		fmt.Fprintf(&b, "showing %d-%d of %d\n", res.Offset+1, res.Offset+len(res.Sessions), res.Total)
	}
	return b.String()
}

func formatConvSearchText(res wsdashboard.ConvSearchResponse) string {
	var b strings.Builder
	if res.Indexing != "" {
		fmt.Fprintf(&b, "indexing: %s\n", res.Indexing)
	}
	if len(res.Hits) == 0 {
		b.WriteString("no matches\n")
		return b.String()
	}
	for _, h := range res.Hits {
		fmt.Fprintf(&b, "%s  %s  %s  [%s]", h.SessionID, h.EntryID, formatConvTS(h.TS), h.Kind)
		if h.SessionStatus != "" {
			if h.SessionLabel != "" {
				fmt.Fprintf(&b, "  (%s: %s)", h.SessionStatus, h.SessionLabel)
			} else {
				fmt.Fprintf(&b, "  (%s)", h.SessionStatus)
			}
		}
		if h.Truncated {
			b.WriteString("  [truncated]")
		}
		b.WriteByte('\n')
		if h.Kind == "mail" && (h.Direction != "" || h.Peer != "" || h.Title != "") {
			fmt.Fprintf(&b, "  mail: %s %s %s\n", h.Direction, h.Peer, h.Title)
		}
		if h.Snippet != "" {
			fmt.Fprintf(&b, "  %s\n", strings.ReplaceAll(h.Snippet, "\n", " "))
		}
	}
	return b.String()
}

func formatConvHistoryText(res wsdashboard.ConvHistoryResponse) string {
	var b strings.Builder
	if len(res.Items) == 0 {
		fmt.Fprintf(&b, "no history entries for %s\n", res.SessionID)
		return b.String()
	}
	for _, item := range res.Items {
		trunc := ""
		if item.Truncated {
			trunc = ", truncated"
		}
		fmt.Fprintf(&b, "%s  %s  [%s]  (%dB%s)  %s\n",
			item.EntryID,
			formatConvTS(item.TS),
			item.Kind,
			item.Bytes,
			trunc,
			strings.ReplaceAll(item.Preview, "\n", " "),
		)
	}
	if res.HasMore {
		b.WriteString("has_more: true\n")
	}
	return b.String()
}

func formatConvReadText(res wsdashboard.ConvReadResponse) string {
	var b strings.Builder
	if res.Mode == "window" {
		fmt.Fprintf(&b, "session: %s\ntarget: %s\nentries: %d (total_bytes=%d", res.SessionID, res.TargetEntryID, len(res.Entries), res.TotalBytes)
		if res.WindowClipped {
			b.WriteString(", window_clipped=true")
		}
		b.WriteString(")\n")
		for _, e := range res.Entries {
			marker := ""
			if e.EntryID == res.TargetEntryID {
				marker = " [TARGET]"
			}
			trunc := ""
			if e.StoredTruncated {
				trunc = ", truncated"
			}
			fmt.Fprintf(&b, "\n--- %s [%s] %s (%dB%s)%s ---\n", e.EntryID, e.Kind, formatConvTS(e.TS), e.FullBytes, trunc, marker)
			if e.Kind == "mail" && (e.Direction != "" || e.Peer != "" || e.Title != "") {
				fmt.Fprintf(&b, "mail: %s %s — %s\n", e.Direction, e.Peer, e.Title)
			}
			b.WriteString(e.Text)
			if !strings.HasSuffix(e.Text, "\n") {
				b.WriteByte('\n')
			}
		}
		return b.String()
	}
	if res.Entry == nil {
		return errors.New("empty read response").Error() + "\n"
	}
	e := res.Entry
	fmt.Fprintf(&b, "session: %s\nentry_id: %s\nkind: %s\nts: %s\nbytes: %d (slice %d..%d, truncated=%v, has_more=%v)\n",
		res.SessionID, e.EntryID, e.Kind, formatConvTS(e.TS), e.FullBytes, res.ByteOffset, res.NextOffset, e.StoredTruncated, res.HasMore)
	if e.Kind == "mail" && (e.Direction != "" || e.Peer != "" || e.Title != "") {
		fmt.Fprintf(&b, "mail: %s %s — %s\n", e.Direction, e.Peer, e.Title)
	}
	b.WriteByte('\n')
	b.WriteString(e.Text)
	if !strings.HasSuffix(e.Text, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func formatConvRevealText(res wsdashboard.ConvRevealResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "session: %s\nstatus: %s\nrevived: %v\nfocused: %v\nemitted: %v\n",
		res.SessionID, res.Status, res.Revived, res.Focused, res.Emitted)
	if res.EntryID != "" {
		fmt.Fprintf(&b, "entry_id: %s\n", res.EntryID)
	}
	return b.String()
}
