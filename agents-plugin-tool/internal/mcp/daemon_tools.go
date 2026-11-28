package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

const (
	daemonCurlTool       = "daemon.curl"
	daemonTabsTool       = "daemon.tabs"
	daemonDOMTool        = "daemon.dom"
	daemonUITool         = "daemon.ui"
	daemonScreenshotTool = "daemon.screenshot"
)

// DaemonControlHost bridges the MCP server to the dashboard daemon's local
// loopback curl, multi-tab registry, semantic DOM/UI bridge, staged screenshot
// capture, and 1-click confirmation layer (wsdashboard.Daemon).
type DaemonControlHost interface {
	DaemonCurl(ctx context.Context, req wsdashboard.DaemonCurlRequest) (wsdashboard.DaemonCurlResponse, error)
	DaemonTabs() []wsdashboard.DaemonTabInfo
	DaemonDOM(ctx context.Context, req wsdashboard.DaemonDOMRequest) (wsdashboard.DaemonDOMSnapshot, error)
	DaemonUI(ctx context.Context, req wsdashboard.DaemonUIRequest) (wsdashboard.DaemonUIResponse, error)
	DaemonScreenshot(ctx context.Context, req wsdashboard.DaemonScreenshotRequest) (wsdashboard.DaemonScreenshotResponse, error)
	RequestDaemonConfirmation(ctx context.Context, req wsdashboard.DaemonConfirmRequest) wsdashboard.DaemonConfirmResult
}

func isDaemonControlTool(name string) bool {
	return strings.HasPrefix(name, "daemon.")
}

func isCompanionBodyRecord(rec wsagent.Record) bool {
	if strings.EqualFold(strings.TrimSpace(rec.CompanionRole), CompanionRoleBody) {
		return true
	}
	p := strings.ToLower(strings.TrimSpace(rec.Profile))
	return p == "companion-body" || strings.HasPrefix(p, "companion-body-")
}

func isCompanionWorkRecord(rec wsagent.Record) bool {
	return strings.EqualFold(strings.TrimSpace(rec.CompanionRole), CompanionRoleWork)
}

func daemonControlTools() []map[string]any {
	return append(daemonCoreTools(), daemonGuideTools()...)
}

func daemonCoreTools() []map[string]any {
	return []map[string]any{
		{
			"name":        daemonCurlTool,
			"description": "Issue an authenticated local loopback HTTP request to the dashboard daemon itself (opted-in top-level hosted leads only). GET and HEAD execute immediately; all other HTTP verbs block up to 120s behind a 1-click user confirmation banner in the active local dashboard shell tab. Credential-bearing routes, auth routes, /settings, /shutdown, and /api/daemon/bridge-reply are always forbidden.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"method":      stringProperty("HTTP method (e.g. GET, HEAD, POST, PUT, PATCH, DELETE). GET and HEAD run immediately; all other methods require 1-click confirmation."),
					"path":        stringProperty("Daemon-relative path starting with '/' (no scheme, host, userinfo, query string, fragment, or '..' segments)."),
					"query": map[string]any{
						"type":                 "object",
						"additionalProperties": map[string]any{"type": "string"},
						"description":          "Optional query parameters.",
					},
					"body": stringProperty("Optional request body (POST-class methods only; max 256 KiB; rejected for GET and HEAD)."),
					"headers": map[string]any{
						"type":                 "object",
						"additionalProperties": map[string]any{"type": "string"},
						"description":          "Optional request headers. Only Accept and Content-Type are allowed; all other headers are rejected.",
					},
					"tab_id": stringProperty("Optional explicit local dashboard tab ID for routing a confirmation banner."),
				},
				"required": []string{"method", "path"},
			},
		},
		{
			"name":        daemonTabsTool,
			"description": "List connected local dashboard shell tabs ordered from most recently focused visible tab first (opted-in top-level hosted leads only). Runs immediately without confirmation.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
				},
			},
		},
		{
			"name":        daemonDOMTool,
			"description": "Capture a bounded semantic/accessibility DOM snapshot of the targeted local dashboard tab (most recently focused visible tab by default, or explicit tab_id). Includes active route, framed session, modal state, stable data-ws-* attributes, and ephemeral ref handles bound to snapshot generation (invalidated by the next snapshot or navigation). Hard caps: 64 KiB and 1500 nodes. Excludes confirmation banners and masks password/secret inputs and credentials.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key":      stringProperty(sessionKeyPropertyDescription),
					"tab_id":           stringProperty("Optional explicit local dashboard tab ID (defaults to most recently focused visible tab)."),
					"scope":            enumStringProperty("Subtree scope to snapshot: all (default: shell + active frame + open settings modal), shell, frame, or settings.", []string{"all", "shell", "frame", "settings"}),
					"interactive_only": map[string]any{"type": "boolean", "description": "When true, include only interactive controls and landmarks."},
					"max_nodes":        map[string]any{"type": "integer", "description": "Optional node limit clamped to [1, 1500]."},
				},
			},
		},
		{
			"name":        daemonUITool,
			"description": "Drive strictly allowlisted semantic UI actions in the targeted local dashboard tab (zero arbitrary JS eval). Non-destructive actions (navigate, focus_session, open_settings, select_panel) run immediately; mutating actions (click_ref, fill_ref) validate snapshot generation/ref and block up to 120s behind the top-level shell 1-click user confirmation banner.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"tab_id":      stringProperty("Optional explicit local dashboard tab ID (defaults to most recently focused visible tab)."),
					"action":      enumStringProperty("Allowlisted semantic UI action.", []string{"navigate", "focus_session", "open_settings", "select_panel", "click_ref", "fill_ref"}),
					"path":        stringProperty("For navigate: daemon-relative path starting with '/' (e.g. '/', '/s/<reply_id>', '/s/<reply_id>#entry=<key>', '/settings')."),
					"reply_id":    stringProperty("For focus_session: session reply ID to focus."),
					"entry_id":    stringProperty("For focus_session: optional conversation entry ID to reveal (#entry=<entry_id>)."),
					"scope":       enumStringProperty("For open_settings: optional config scope.", []string{"global", "project", "repo", "session"}),
					"section":     enumStringProperty("For open_settings: optional settings section.", []string{"workflow", "agents", "tier_routing", "account", "compaction", "adapters", "companion", "profiles", "prompts"}),
					"target":      stringProperty("For open_settings: optional scope target."),
					"worktree":    stringProperty("For open_settings: optional worktree context."),
					"session":     stringProperty("For open_settings: optional session reply ID context."),
					"panel":       enumStringProperty("For select_panel: semantic dashboard panel to focus or open.", []string{"convo", "conversation", "board", "files", "term", "terminal", "git", "overview", "ticketmap", "rail", "search"}),
					"ref":         stringProperty("For click_ref / fill_ref: ephemeral ref handle from the active daemon.dom snapshot."),
					"generation":  map[string]any{"type": "integer", "description": "Optional snapshot generation to assert against the tab's active snapshot."},
					"value":       stringProperty("For fill_ref: text value to fill into the referenced input (max 64 KiB; masked in confirmation summary for sensitive inputs)."),
				},
				"required": []string{"action"},
			},
		},
		{
			"name":        daemonScreenshotTool,
			"description": "Capture visual state of the targeted local dashboard tab (most recently focused visible tab by default, or explicit tab_id). Runs immediately without confirmation. Staged selection: Tier A native desktop WebView PNG (mode='native', live=true) written under the daemon's per-session cache directory (max 8 MiB, never inlined); else Tier B optional headless Playwright route render (mode='route_render', live=false) when configured at runtime; else Tier C honest dom_fallback (mode='dom_fallback', live=false, reason='screenshot_unavailable') with a bounded daemon.dom snapshot.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"tab_id":      stringProperty("Optional explicit local dashboard tab ID (defaults to most recently focused visible tab)."),
				},
			},
		},
	}
}

// daemonControlCallRefusal returns a non-empty refusal message when this
// connection is not authorized to invoke a ws/daemon.* tool. Enforces:
//   - trusted hosted conversation binding (never launcher/out-of-daemon/worker)
//   - open top-level lead record, never a worker child
//   - Tier-1 companion-body: exactly the guide tools (isDaemonGuideTool)
//   - automatic access for Tier-2 companion-work sessions (261127 Phase 2)
//   - persisted && effective opt-in for regular leads (grant after attach stays
//     denied until restart; live revocation denies immediately)
func (s *Server) daemonControlCallRefusal(name string) string {
	if s.connectionDescriptor().leadRender {
		if s.dashboardControlEffective || (s.dashboardGuideEffective && isDaemonGuideTool(name)) {
			return ""
		}
		return fmt.Sprintf("%s: dashboard control is not enabled for this session", name)
	}
	if s.conversation == "" {
		return fmt.Sprintf("%s: dashboard control requires an opted-in dashboard-hosted top-level lead session", name)
	}
	if s.recordReader == nil {
		return fmt.Sprintf("%s: dashboard control record authority is unavailable", name)
	}
	rec, ok := s.recordReader.RecordByReplyID(s.conversation)
	if !ok {
		return fmt.Sprintf("%s: hosted lead session record not found", name)
	}
	if !rec.Lead || rec.Parent != nil || rec.ClosedAt != "" {
		return fmt.Sprintf("%s: dashboard control is restricted to open top-level lead sessions (workers and companion-body are blocked)", name)
	}
	if isCompanionBodyRecord(rec) {
		if isDaemonGuideTool(name) {
			return ""
		}
		return fmt.Sprintf("%s: companion-body may use only the guide tools (%s); hand other dashboard control to companion.delegate_work (workers and companion-body are blocked from it)", name, daemonGuideToolList)
	}
	if isCompanionWorkRecord(rec) {
		return ""
	}
	if !s.dashboardControlEffective {
		return fmt.Sprintf("%s: dashboard control was not enabled when this session started (enable it in session settings and restart the session)", name)
	}
	if !rec.DashboardControlAccess {
		return fmt.Sprintf("%s: dashboard control access has been revoked for this session", name)
	}
	return ""
}

func daemonControlLeadOnlyDenial(name string) string {
	return fmt.Sprintf("%s is restricted to opted-in top-level hosted lead sessions; worker subagents and spawned forks are blocked", name)
}

func (s *Server) handleDaemonTool(ctx context.Context, id json.RawMessage, name string, args map[string]any, companionWork bool) response {
	if s.conversation == "" {
		return toolErrorTextResponse(id, fmt.Sprintf("%s: dashboard control requires an opted-in dashboard-hosted top-level lead session", name))
	}
	if refusal := s.daemonControlCallRefusal(name); refusal != "" {
		return toolErrorTextResponse(id, refusal)
	}
	if s.daemonControl == nil {
		return toolTextResponse(id, "", errors.New(name+": dashboard control host is not available in this session"))
	}
	switch name {
	case daemonCurlTool:
		return s.handleDaemonCurl(ctx, id, args, companionWork)
	case daemonTabsTool:
		return s.handleDaemonTabs(id)
	case daemonDOMTool:
		return s.handleDaemonDOM(ctx, id, args)
	case daemonUITool:
		return s.handleDaemonUI(ctx, id, args, companionWork)
	case daemonScreenshotTool:
		return s.handleDaemonScreenshot(ctx, id, args)
	case daemonHighlightTool:
		return s.handleDaemonHighlight(ctx, id, args)
	case daemonMonitorTool:
		return s.handleDaemonMonitor(ctx, id, args)
	default:
		return errorResponse(id, -32602, fmt.Sprintf("unknown tool: %s", name))
	}
}

func (s *Server) handleDaemonScreenshot(ctx context.Context, id json.RawMessage, args map[string]any) response {
	tabID, err := daemonOptionalStringArg(daemonScreenshotTool, "tab_id", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	resp, err := s.daemonControl.DaemonScreenshot(ctx, wsdashboard.DaemonScreenshotRequest{
		TabID:     tabID,
		SessionID: s.conversation,
	})
	if err != nil {
		return daemonControlErrorResponse(id, err)
	}
	return toolJSONResponse(id, resp, nil)
}

func (s *Server) handleDaemonTabs(id json.RawMessage) response {
	tabs := s.daemonControl.DaemonTabs()
	if tabs == nil {
		tabs = []wsdashboard.DaemonTabInfo{}
	}
	return toolJSONResponse(id, map[string]any{"tabs": tabs}, nil)
}

func (s *Server) handleDaemonDOM(ctx context.Context, id json.RawMessage, args map[string]any) response {
	req := wsdashboard.DaemonDOMRequest{}
	if v, err := daemonOptionalStringArg(daemonDOMTool, "tab_id", args); err != nil {
		return toolTextResponse(id, "", err)
	} else {
		req.TabID = v
	}
	if v, err := daemonOptionalStringArg(daemonDOMTool, "scope", args); err != nil {
		return toolTextResponse(id, "", err)
	} else {
		req.Scope = v
	}
	if raw, present := args["interactive_only"]; present && raw != nil {
		b, ok := raw.(bool)
		if !ok {
			return toolTextResponse(id, "", fmt.Errorf("%s: interactive_only must be a boolean", daemonDOMTool))
		}
		req.InteractiveOnly = b
	}
	if raw, present := args["max_nodes"]; present && raw != nil {
		n, ok := positiveIntArg(raw)
		if !ok {
			return toolTextResponse(id, "", fmt.Errorf("%s: max_nodes must be a positive integer", daemonDOMTool))
		}
		req.MaxNodes = n
	}
	snap, err := s.daemonControl.DaemonDOM(ctx, req)
	if err != nil {
		return daemonControlErrorResponse(id, err)
	}
	return toolJSONResponse(id, snap, nil)
}

func (s *Server) handleDaemonUI(ctx context.Context, id json.RawMessage, args map[string]any, companionWork bool) response {
	action, err := stringArg(daemonUITool, "action", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	req := wsdashboard.DaemonUIRequest{
		Action:         action,
		CompanionPause: companionWork,
	}
	for _, field := range []struct {
		key string
		dst *string
	}{
		{"tab_id", &req.TabID},
		{"path", &req.Path},
		{"reply_id", &req.ReplyID},
		{"entry_id", &req.EntryID},
		{"scope", &req.Scope},
		{"section", &req.Section},
		{"target", &req.Target},
		{"worktree", &req.Worktree},
		{"session", &req.Session},
		{"panel", &req.Panel},
		{"ref", &req.Ref},
		{"value", &req.Value},
	} {
		v, err := daemonOptionalStringArg(daemonUITool, field.key, args)
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		*field.dst = v
	}
	if raw, present := args["generation"]; present && raw != nil {
		n, ok := positiveIntArg(raw)
		if !ok {
			return toolTextResponse(id, "", fmt.Errorf("%s: generation must be a positive integer", daemonUITool))
		}
		req.Generation = uint64(n)
	}
	resp, err := s.daemonControl.DaemonUI(ctx, req)
	if err != nil {
		return daemonControlErrorResponse(id, err)
	}
	return toolJSONResponse(id, resp, nil)
}

func daemonControlErrorResponse(id json.RawMessage, err error) response {
	if dce, ok := wsdashboard.IsDaemonControlError(err); ok {
		raw, _ := json.Marshal(dce)
		return toolErrorTextResponse(id, string(raw))
	}
	return toolTextResponse(id, "", err)
}

func daemonOptionalStringArg(toolName, key string, args map[string]any) (string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return "", nil
	}
	v, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s: %s must be a string", toolName, key)
	}
	return v, nil
}

func positiveIntArg(raw any) (int, bool) {
	switch v := raw.(type) {
	case float64:
		if v >= 1 && v == float64(int(v)) {
			return int(v), true
		}
	case int:
		if v >= 1 {
			return v, true
		}
	case int64:
		if v >= 1 {
			return int(v), true
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n >= 1 {
			return int(n), true
		}
	}
	return 0, false
}

func (s *Server) handleDaemonCurl(ctx context.Context, id json.RawMessage, args map[string]any, companionWork bool) response {
	method, err := stringArg(daemonCurlTool, "method", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	pathStr, err := stringArg(daemonCurlTool, "path", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	var body string
	if raw, present := args["body"]; present && raw != nil {
		v, ok := raw.(string)
		if !ok {
			return toolTextResponse(id, "", fmt.Errorf("%s: body must be a string", daemonCurlTool))
		}
		body = v
	}
	var tabID string
	if raw, present := args["tab_id"]; present && raw != nil {
		v, ok := raw.(string)
		if !ok {
			return toolTextResponse(id, "", fmt.Errorf("%s: tab_id must be a string", daemonCurlTool))
		}
		tabID = v
	}
	query, err := stringMapArg(daemonCurlTool, "query", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	headers, err := stringMapArg(daemonCurlTool, "headers", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}

	resp, err := s.daemonControl.DaemonCurl(ctx, wsdashboard.DaemonCurlRequest{
		Method:         method,
		Path:           pathStr,
		Query:          query,
		Body:           body,
		Headers:        headers,
		TabID:          tabID,
		CompanionPause: companionWork,
	})
	if err != nil {
		if dce, ok := wsdashboard.IsDaemonControlError(err); ok {
			raw, _ := json.Marshal(dce)
			return toolErrorTextResponse(id, string(raw))
		}
		return toolTextResponse(id, "", err)
	}
	return toolJSONResponse(id, resp, nil)
}

func stringMapArg(toolName, key string, args map[string]any) (map[string]string, error) {
	raw, present := args[key]
	if !present || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s must be an object of strings", toolName, key)
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %s.%s must be a string", toolName, key, k)
		}
		out[k] = str
	}
	return out, nil
}
