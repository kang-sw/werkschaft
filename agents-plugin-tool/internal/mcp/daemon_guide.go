package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// Companion interactive guide tools: ws/daemon.highlight (non-mutating visual
// overlay) and ws/daemon.monitor (opt-in structural UI event subscription).
// Together with daemon.tabs and daemon.dom they form the guide set the Tier-1
// companion-body may use directly; daemon.curl, daemon.ui, and
// daemon.screenshot stay with companion-work and opted-in leads.
const (
	daemonHighlightTool = "daemon.highlight"
	daemonMonitorTool   = "daemon.monitor"
)

// DaemonGuideHost is the optional guide surface of a DaemonControlHost. A
// host that does not implement it answers guide_unavailable.
type DaemonGuideHost interface {
	DaemonHighlight(ctx context.Context, req wsdashboard.DaemonHighlightRequest) (wsdashboard.DaemonHighlightResponse, error)
	DaemonMonitor(ctx context.Context, req wsdashboard.DaemonMonitorRequest) (wsdashboard.DaemonMonitorStatus, error)
}

// isDaemonGuideTool reports the guide subset of ws/daemon.* granted to an open
// top-level companion-body session.
func isDaemonGuideTool(name string) bool {
	switch name {
	case daemonTabsTool, daemonDOMTool, daemonHighlightTool, daemonMonitorTool:
		return true
	default:
		return false
	}
}

const daemonGuideToolList = "daemon.tabs, daemon.dom, daemon.highlight, daemon.monitor"

func daemonGuideTools() []map[string]any {
	return []map[string]any{
		{
			"name":        daemonHighlightTool,
			"description": "Draw a non-mutating guide highlight (spotlight, pulse, or ring with an optional short comment) over one element in the targeted local dashboard tab, or clear highlights. Runs immediately without confirmation; changes no product state. Target by daemon.dom ref, CSS selector, or visible text. Default output is one compact line; pass format=json for the structured object.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"action":      enumStringProperty("show draws or replaces a highlight slot; clear removes one slot; clear_all removes every highlight.", []string{"show", "clear", "clear_all"}),
					"id":          stringProperty("Highlight slot id (letters, digits, '_' or '-', at most 32), default \"default\". Not accepted with clear_all."),
					"tab_id":      stringProperty("Optional explicit local dashboard tab ID (defaults to most recently focused visible tab)."),
					"target": map[string]any{
						"type":        "object",
						"description": "For show: exactly one of ref, selector, or text.",
						"properties": map[string]any{
							"ref":        stringProperty("Ephemeral ref handle from the active daemon.dom snapshot."),
							"generation": map[string]any{"type": "integer", "description": "Optional snapshot generation to assert (ref targets only)."},
							"selector":   stringProperty("CSS selector (at most 256 bytes)."),
							"text":       stringProperty("Visible text or accessible name (at most 200 characters)."),
						},
					},
					"comment": stringProperty("For show: optional guide text shown beside the highlight (at most 280 characters; rendered as plain text)."),
					"style":   enumStringProperty("For show: overlay style, default spotlight.", []string{"spotlight", "pulse", "ring"}),
					"format":  stringProperty("\"json\" for the structured result; default is one compact text line."),
				},
				"required": []string{"action"},
			},
		},
		{
			"name":        daemonMonitorTool,
			"description": "Start, stop, or inspect an opt-in, time-limited subscription to structural local dashboard UI events (tab_switched, section_changed, modal_opened, modal_closed, route_changed, highlight_clicked). Events are debounced, coalesced, and capped at 12 deliveries per minute; each batch arrives as a system message in this session. Never carries keystrokes, drafts, or typed text. One monitor per session; start replaces it. Default output is one compact line; pass format=json for the structured object.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"action":      enumStringProperty("start (or replace), stop, or status.", []string{"start", "stop", "status"}),
					"events": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "enum": []string{"tab_switched", "section_changed", "modal_opened", "modal_closed", "route_changed", "highlight_clicked"}},
						"description": "For start: event names to receive (default all six).",
					},
					"ttl_sec":     map[string]any{"type": "integer", "description": "For start: lifetime in seconds, default 600, clamped to [60, 900]."},
					"debounce_ms": map[string]any{"type": "integer", "description": "For start: batch window in milliseconds, default 400, clamped to [300, 2000]."},
					"format":      stringProperty("\"json\" for the structured result; default is one compact text line."),
				},
				"required": []string{"action"},
			},
		},
	}
}

// daemonControlListedFor reports whether one ws/daemon.* tool is advertised
// on this connection's tools/list, call discovery, and agy DirectTools.
func (s *Server) daemonControlListedFor(name string) bool {
	return s.daemonControlCallRefusal(name) == ""
}

func daemonGuideHost(h DaemonControlHost) (DaemonGuideHost, bool) {
	g, ok := h.(DaemonGuideHost)
	return g, ok
}

func guideUnavailableResponse(id json.RawMessage, name string) response {
	raw, _ := json.Marshal(wsdashboard.DaemonControlError{Code: "guide_unavailable", Reason: "host_unsupported", Message: name + ": this dashboard host does not provide guide tools"})
	return toolErrorTextResponse(id, string(raw))
}

func daemonFormatJSON(toolName string, args map[string]any) (bool, error) {
	v, err := daemonOptionalStringArg(toolName, "format", args)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "text":
		return false, nil
	case "json":
		return true, nil
	default:
		return false, fmt.Errorf("%s: format must be \"json\" or omitted", toolName)
	}
}

func (s *Server) handleDaemonHighlight(ctx context.Context, id json.RawMessage, args map[string]any) response {
	host, ok := daemonGuideHost(s.daemonControl)
	if !ok {
		return guideUnavailableResponse(id, daemonHighlightTool)
	}
	asJSON, err := daemonFormatJSON(daemonHighlightTool, args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	action, err := stringArg(daemonHighlightTool, "action", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	req := wsdashboard.DaemonHighlightRequest{Action: action}
	for _, field := range []struct {
		key string
		dst *string
	}{{"id", &req.ID}, {"tab_id", &req.TabID}, {"comment", &req.Comment}, {"style", &req.Style}} {
		v, err := daemonOptionalStringArg(daemonHighlightTool, field.key, args)
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		*field.dst = v
	}
	if raw, present := args["target"]; present && raw != nil {
		obj, ok := raw.(map[string]any)
		if !ok {
			return toolTextResponse(id, "", fmt.Errorf("%s: target must be an object", daemonHighlightTool))
		}
		t := &wsdashboard.DaemonHighlightTarget{}
		for key, val := range obj {
			switch key {
			case "ref", "selector", "text":
				str, ok := val.(string)
				if !ok {
					return toolTextResponse(id, "", fmt.Errorf("%s: target.%s must be a string", daemonHighlightTool, key))
				}
				switch key {
				case "ref":
					t.Ref = str
				case "selector":
					t.Selector = str
				default:
					t.Text = str
				}
			case "generation":
				n, ok := positiveIntArg(val)
				if !ok {
					return toolTextResponse(id, "", fmt.Errorf("%s: target.generation must be a positive integer", daemonHighlightTool))
				}
				t.Generation = uint64(n)
			default:
				return toolTextResponse(id, "", fmt.Errorf("%s: unknown target field %q (allowed: ref, selector, text, generation)", daemonHighlightTool, key))
			}
		}
		req.Target = t
	}
	resp, err := host.DaemonHighlight(ctx, req)
	if err != nil {
		return daemonControlErrorResponse(id, err)
	}
	if asJSON {
		return toolJSONResponse(id, resp, nil)
	}
	return toolTextResponse(id, compactHighlightText(resp), nil)
}

func compactHighlightText(r wsdashboard.DaemonHighlightResponse) string {
	parts := []string{"highlight", r.Action}
	if r.ID != "" {
		parts = append(parts, "id="+r.ID)
	}
	if r.TabID != "" {
		parts = append(parts, "tab="+r.TabID)
	}
	if r.Action == "show" {
		if r.Style != "" {
			parts = append(parts, "style="+r.Style)
		}
		if r.TargetKind != "" {
			parts = append(parts, "target="+r.TargetKind)
		}
		if r.Scope != "" {
			parts = append(parts, "scope="+r.Scope)
		}
		if r.Visible {
			parts = append(parts, "visible")
		} else {
			parts = append(parts, "offscreen")
		}
		if r.Rect != nil {
			parts = append(parts, fmt.Sprintf("rect=%s,%s %sx%s", fmtPx(r.Rect.X), fmtPx(r.Rect.Y), fmtPx(r.Rect.Width), fmtPx(r.Rect.Height)))
		}
	} else {
		parts = append(parts, "cleared="+strconv.Itoa(r.Cleared))
	}
	return strings.Join(parts, " ")
}

func fmtPx(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (s *Server) handleDaemonMonitor(ctx context.Context, id json.RawMessage, args map[string]any) response {
	host, ok := daemonGuideHost(s.daemonControl)
	if !ok {
		return guideUnavailableResponse(id, daemonMonitorTool)
	}
	asJSON, err := daemonFormatJSON(daemonMonitorTool, args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	action, err := stringArg(daemonMonitorTool, "action", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	req := wsdashboard.DaemonMonitorRequest{Owner: s.conversation, Action: action}
	if raw, present := args["events"]; present && raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return toolTextResponse(id, "", fmt.Errorf("%s: events must be an array of strings", daemonMonitorTool))
		}
		for _, item := range list {
			str, ok := item.(string)
			if !ok {
				return toolTextResponse(id, "", fmt.Errorf("%s: events must be an array of strings", daemonMonitorTool))
			}
			req.Events = append(req.Events, str)
		}
	}
	for _, field := range []struct {
		key string
		dst *int
	}{{"ttl_sec", &req.TTLSec}, {"debounce_ms", &req.DebounceMS}} {
		if raw, present := args[field.key]; present && raw != nil {
			n, ok := positiveIntArg(raw)
			if !ok {
				return toolTextResponse(id, "", fmt.Errorf("%s: %s must be a positive integer", daemonMonitorTool, field.key))
			}
			*field.dst = n
		}
	}
	st, err := host.DaemonMonitor(ctx, req)
	if err != nil {
		return daemonControlErrorResponse(id, err)
	}
	if asJSON {
		return toolJSONResponse(id, st, nil)
	}
	return toolTextResponse(id, compactMonitorText(strings.ToLower(strings.TrimSpace(action)), st), nil)
}

func compactMonitorText(action string, st wsdashboard.DaemonMonitorStatus) string {
	if !st.Active && st.MonitorID == "" {
		if st.StopReason != "" {
			return "monitor inactive last_stop=" + st.StopReason
		}
		return "monitor inactive"
	}
	parts := []string{"monitor", action}
	if st.Active {
		parts = append(parts, "active")
	} else {
		parts = append(parts, "stopped")
	}
	parts = append(parts, "id="+st.MonitorID)
	if len(st.Events) > 0 {
		parts = append(parts, "events="+strings.Join(st.Events, ","))
	}
	if st.Active {
		parts = append(parts, fmt.Sprintf("ttl=%ds remaining=%ds debounce=%dms", st.TTLSec, st.RemainingSec, st.DebounceMS))
	}
	parts = append(parts, fmt.Sprintf("delivered=%d coalesced=%d suppressed=%d", st.Delivered, st.Coalesced, st.Suppressed))
	if st.StopReason != "" {
		parts = append(parts, "reason="+st.StopReason)
	}
	return strings.Join(parts, " ")
}
