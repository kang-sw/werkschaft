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
	daemonCurlTool = "daemon.curl"
)

// DaemonControlHost bridges the MCP server to the dashboard daemon's local
// loopback curl and multi-tab bridge confirmation layer (wsdashboard.Daemon).
type DaemonControlHost interface {
	DaemonCurl(ctx context.Context, req wsdashboard.DaemonCurlRequest) (wsdashboard.DaemonCurlResponse, error)
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

func daemonControlTools() []map[string]any {
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
	}
}

// daemonControlListed reports whether ws/daemon.* tools are advertised on this
// connection's tools/list, call discovery, and agy DirectTools. Requires both
// attach-time effective access and live persisted opt-in on an open top-level
// hosted lead (never workers or Tier-1 companion-body).
func (s *Server) daemonControlListed() bool {
	if s.connectionDescriptor().leadRender {
		return s.dashboardControlEffective
	}
	return s.daemonControlCallRefusal(daemonCurlTool) == ""
}

// daemonControlCallRefusal returns a non-empty refusal message when this
// connection is not authorized to invoke a ws/daemon.* tool. Enforces:
//   - trusted hosted conversation binding (never launcher/out-of-daemon/worker)
//   - persisted && effective opt-in (grant after attach stays denied until restart;
//     live revocation denies immediately without stopping the running process)
//   - open top-level lead record, never a worker child or Tier-1 companion-body
func (s *Server) daemonControlCallRefusal(name string) string {
	if s.connectionDescriptor().leadRender {
		if s.dashboardControlEffective {
			return ""
		}
		return fmt.Sprintf("%s: dashboard control is not enabled for this session", name)
	}
	if s.conversation == "" {
		return fmt.Sprintf("%s: dashboard control requires an opted-in dashboard-hosted top-level lead session", name)
	}
	if !s.dashboardControlEffective {
		return fmt.Sprintf("%s: dashboard control was not enabled when this session started (enable it in session settings and restart the session)", name)
	}
	if s.recordReader == nil {
		return fmt.Sprintf("%s: dashboard control record authority is unavailable", name)
	}
	rec, ok := s.recordReader.RecordByReplyID(s.conversation)
	if !ok {
		return fmt.Sprintf("%s: hosted lead session record not found", name)
	}
	if !rec.Lead || rec.Parent != nil || rec.ClosedAt != "" || isCompanionBodyRecord(rec) {
		return fmt.Sprintf("%s: dashboard control is restricted to open top-level lead sessions (workers and companion-body are blocked)", name)
	}
	if !rec.DashboardControlAccess {
		return fmt.Sprintf("%s: dashboard control access has been revoked for this session", name)
	}
	return ""
}

func daemonControlLeadOnlyDenial(name string) string {
	return fmt.Sprintf("%s is restricted to opted-in top-level hosted lead sessions; worker subagents and spawned forks are blocked", name)
}

func (s *Server) handleDaemonTool(ctx context.Context, id json.RawMessage, name string, args map[string]any) response {
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
		return s.handleDaemonCurl(ctx, id, args)
	default:
		return errorResponse(id, -32602, fmt.Sprintf("unknown tool: %s", name))
	}
}

func (s *Server) handleDaemonCurl(ctx context.Context, id json.RawMessage, args map[string]any) response {
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
		Method:  method,
		Path:    pathStr,
		Query:   query,
		Body:    body,
		Headers: headers,
		TabID:   tabID,
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
