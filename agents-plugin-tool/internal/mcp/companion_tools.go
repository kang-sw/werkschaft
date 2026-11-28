package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

const (
	companionSpeakTool        = "companion.speak"
	companionFaceTool         = "companion.face"
	companionMotionTool       = "companion.motion"
	companionPhonePickupTool  = "companion.phone_pickup"
	companionPhoneReadTool    = "companion.phone_read"
	companionPhoneSendTool    = "companion.phone_send"
	companionPhonePutdownTool = "companion.phone_putdown"
	companionDelegateWorkTool = "companion.delegate_work"
	delegationReportTool      = "delegation.report"

	companionRefusalHandsBusy   = "Hands busy holding smartphone — call ws/companion.phone_putdown first to use your laptop."
	companionRefusalPhoneOnDesk = "Smartphone is on the desk — call ws/companion.phone_pickup first to check or reply."
)

// CompanionHost bridges the MCP server to the dashboard daemon's in-memory
// companion embodiment and phone messenger store (wsdashboard.Daemon).
type CompanionHost interface {
	CompanionPosture(owner string) string
	CompanionUnreadPhoneCount() int
	CompanionSpeak(sessionID, text string, ttlMS int) bool
	CompanionFace(sessionID, expression string, ttlMS int) bool
	CompanionMotion(sessionID, gesture, prop string, ttlMS int) bool
	CompanionPhonePickup(owner string) (wsdashboard.CompanionPhoneReadResult, error)
	CompanionPhoneRead(owner string) (wsdashboard.CompanionPhoneReadResult, error)
	CompanionPhoneSend(owner, text, tone string) (wsdashboard.CompanionPhoneMessage, error)
	CompanionPhonePutdown(owner, style, next string) (wsdashboard.CompanionPhoneSnapshot, error)
	CompanionReleaseOnTurnEnd(owner string) bool
}

// CompanionDelegationAccepted is the non-blocking acknowledgement returned by
// ws/companion.delegate_work once the daemon accepts and queues/dispatches a brief.
type CompanionDelegationAccepted struct {
	DelegationID string `json:"delegation_id"`
	Status       string `json:"status"`
}

// CompanionDelegationReportAck is the acknowledgement returned by
// ws/delegation.report once the daemon records and forwards Tier-2's report.
type CompanionDelegationReportAck struct {
	DelegationID string `json:"delegation_id"`
	Status       string `json:"status"`
}

// CompanionDelegationHost bridges ws/companion.delegate_work (Tier-1) and
// ws/delegation.report (Tier-2) to the daemon's in-memory delegation table.
type CompanionDelegationHost interface {
	CompanionDelegateWork(callerKey, brief string) (CompanionDelegationAccepted, error)
	CompanionDelegationReport(callerKey, delegationID, status, result string, artifacts []string) (CompanionDelegationReportAck, error)
}

func isCompanionTool(name string) bool {
	return strings.HasPrefix(name, "companion.")
}

func isCompanionPhoneTool(name string) bool {
	switch name {
	case companionPhonePickupTool, companionPhoneReadTool, companionPhoneSendTool, companionPhonePutdownTool:
		return true
	default:
		return false
	}
}

// isCompanionBodyOnlyTool reports tools exclusively available to Tier-1
// (companion-body) sessions and never visible/reachable by unmarked leads or
// Tier-2 (companion-work).
func isCompanionBodyOnlyTool(name string) bool {
	return isCompanionPhoneTool(name) || name == companionDelegateWorkTool
}

func isDelegationReportTool(name string) bool {
	return name == delegationReportTool
}

func formatStructuredGateError(code, tool, requiredTool, message string) string {
	payload := struct {
		Error        string `json:"error"`
		Tool         string `json:"tool"`
		RequiredTool string `json:"required_tool,omitempty"`
		Message      string `json:"message"`
	}{
		Error:        code,
		Tool:         tool,
		RequiredTool: requiredTool,
		Message:      message,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return code + ": " + message
	}
	return string(raw)
}

func companionTools() []map[string]any {
	return []map[string]any{
		{
			"name":        companionSpeakTool,
			"description": "Show a short floating speech quip on the Werkschaft companion stage.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"text":        stringProperty("Quip text shown in the companion bubble (clamped to 140 runes)."),
					"ttl_ms":      integerProperty("Optional display duration in milliseconds (1000-15000)."),
				},
				"required": []string{"text"},
			},
		},
		{
			"name":        companionFaceTool,
			"description": "Set the companion facial expression (idle, idle_doze, focused, half_lid, oo, cry, happy, wink, merong, startled, annoyed).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"expression":  stringProperty("Target facial expression ID."),
					"ttl_ms":      integerProperty("Optional duration in milliseconds (1000-15000)."),
				},
				"required": []string{"expression"},
			},
		},
		{
			"name":        companionMotionTool,
			"description": "Trigger a companion gesture and optional prop (e.g. wave, point, headrub, coffee, knock_startle, annoyed_tap, wakeup_jolt, phone_buzz, phone_pickup, phone_typing, phone_putdown, phone_merong_facedown).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"gesture":     stringProperty("Target gesture ID."),
					"prop":        stringProperty("Optional prop ID (smartphone, drone, coffee, mail)."),
					"ttl_ms":      integerProperty("Optional duration in milliseconds (1000-15000)."),
				},
				"required": []string{"gesture"},
			},
		},
		{
			"name":        companionPhonePickupTool,
			"description": "Pick up the desk smartphone with one hand, lock the companion Embodiment Lease to phone posture, and drain unread user messenger messages. While holding the smartphone, laptop/work tools are locked until ws/companion.phone_putdown is called.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"format":      stringProperty(formatPropertyDescription),
				},
			},
		},
		{
			"name":        companionPhoneReadTool,
			"description": "Read newly arrived unread user messages while holding the smartphone. Refused while posture is laptop; call ws/companion.phone_pickup first.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"format":      stringProperty(formatPropertyDescription),
				},
			},
		},
		{
			"name":        companionPhoneSendTool,
			"description": "Send a companion reply to the smartphone messenger thread while holding the smartphone. Refused while posture is laptop; call ws/companion.phone_pickup first.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"text":        stringProperty("Reply message text."),
					"tone":        stringProperty("Optional expression/reaction tone (e.g. happy, laugh, merong, annoyed, startled, focused)."),
					"format":      stringProperty(formatPropertyDescription),
				},
				"required": []string{"text"},
			},
		},
		{
			"name":        companionPhonePutdownTool,
			"description": "Put the smartphone back on the desk and return to laptop posture. Set style to normal or facedown_merong, and next to ambient (release lease) or work (keep lease for laptop work).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"style":       enumStringProperty(`Putdown animation style: "normal" (face-up) or "facedown_merong" (cheeky wink/merong + flip face-down). Defaults to "normal".`, []string{"normal", "facedown_merong"}),
					"next":        enumStringProperty(`Next embodiment state: "ambient" (release Embodiment Lease to laptop_ambient, default) or "work" (keep lease in laptop_work).`, []string{"ambient", "work"}),
					"format":      stringProperty(formatPropertyDescription),
				},
			},
		},
		{
			"name":        companionDelegateWorkTool,
			"description": "Hand off a lossless task brief to the hidden companion work brain without blocking. Callable only in laptop posture; returns immediately with a delegation_id while the companion shows laptop work visuals.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"brief":       stringProperty("Complete, lossless task brief for the hidden work brain (all needed context, constraints, and references; no length cap)."),
					"format":      stringProperty(formatPropertyDescription),
				},
				"required": []string{"brief"},
			},
		},
	}
}

func delegationTools() []map[string]any {
	return []map[string]any{
		{
			"name":        delegationReportTool,
			"description": "Report the structured outcome of an active companion work delegation back to the embodied companion persona.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key":   stringProperty(sessionKeyPropertyDescription),
					"delegation_id": stringProperty("Identifier of the active delegation being reported."),
					"status":        enumStringProperty("Outcome status of the delegation.", []string{"done", "partial", "blocked"}),
					"result":        stringProperty("Complete, lossless technical result for the companion persona (no length cap)."),
					"artifacts": map[string]any{
						"type":        "array",
						"description": "Optional artifact paths or links produced during the delegation.",
						"items":       map[string]any{"type": "string"},
					},
					"format": stringProperty(formatPropertyDescription),
				},
				"required": []string{"delegation_id", "status", "result"},
			},
		},
	}
}

func (s *Server) companionDelegationHost() CompanionDelegationHost {
	if s.companionDelegation != nil {
		return s.companionDelegation
	}
	if host, ok := s.companion.(CompanionDelegationHost); ok {
		return host
	}
	return nil
}

// companionPostureRefusal enforces the strict server-side laptop|phone posture
// lock for both Server.callTool and Server.callDiscoveryTools, scoped strictly
// to companion-body sessions ((1740)). Unmarked sessions and companion-work are
// never posture-locked.
func (s *Server) companionPostureRefusal(name, sessionKey string, entry sessionEntry, found bool) string {
	if s.companion == nil || !found || entry.companionRole != CompanionRoleBody {
		return ""
	}
	owner := strings.TrimSpace(sessionKey)
	if owner == "" {
		owner = strings.TrimSpace(s.hostedSessionKey())
	}
	if owner == "" {
		return ""
	}
	posture := s.companion.CompanionPosture(owner)
	ns := s.runtimeNamespace()
	if posture == wsdashboard.CompanionPosturePhone {
		if companionBodyPhonePostureAllows(name) {
			return ""
		}
		return formatStructuredGateError("posture_locked", name, ns+"/"+companionPhonePutdownTool, namespaceText(companionRefusalHandsBusy, ns))
	}
	if name == companionPhoneReadTool || name == companionPhoneSendTool {
		return formatStructuredGateError("posture_locked", name, ns+"/"+companionPhonePickupTool, namespaceText(companionRefusalPhoneOnDesk, ns))
	}
	return ""
}

// applyCompanionPhonePiggyback appends "[phone buzz: N unread]" to non-JSON,
// non-error tool responses for companion-body sessions only whenever unread
// user phone messages are waiting ((1740)). Unmarked sessions and companion-work
// never receive the badge.
func (s *Server) applyCompanionPhonePiggyback(resp response, toolName string, caller sessionEntry, callerFound bool, jsonFormat bool) response {
	if jsonFormat || s.companion == nil || !callerFound || caller.companionRole != CompanionRoleBody {
		return resp
	}
	if toolName == companionPhonePickupTool || toolName == companionPhoneReadTool {
		return resp
	}
	unread := s.companion.CompanionUnreadPhoneCount()
	if unread <= 0 {
		return resp
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		return resp
	}
	if isErr, _ := result["isError"].(bool); isErr {
		return resp
	}
	content, ok := result["content"].([]map[string]string)
	if !ok || len(content) == 0 {
		return resp
	}
	suffix := fmt.Sprintf("\n[phone buzz: %d unread]", unread)
	content[0]["text"] = content[0]["text"] + suffix
	return resp
}

func (s *Server) handleCompanionTool(id json.RawMessage, name string, args map[string]any, callerKey string, caller sessionEntry, callerFound bool) response {
	if isCompanionBodyOnlyTool(name) && (!callerFound || caller.companionRole != CompanionRoleBody) {
		return errorResponse(id, -32601, fmt.Sprintf("tool not available in current %s MCP profile: %s", s.runtimeNamespace(), name))
	}
	if name == companionDelegateWorkTool {
		host := s.companionDelegationHost()
		if host == nil {
			return toolTextResponse(id, "", errors.New(name+": companion delegation host is not available in this session"))
		}
		brief, ok := args["brief"].(string)
		if !ok || strings.TrimSpace(brief) == "" {
			return toolTextResponse(id, "", fmt.Errorf("%s: brief must be a non-empty string", name))
		}
		owner := strings.TrimSpace(callerKey)
		if owner == "" {
			return toolTextResponse(id, "", fmt.Errorf("%s: session_key is required", name))
		}
		ack, err := host.CompanionDelegateWork(owner, brief)
		if err != nil {
			return toolErrorTextResponse(id, err.Error())
		}
		if wantsJSON(args) {
			return toolJSONResponse(id, ack, nil)
		}
		return toolTextResponse(id, fmt.Sprintf("delegation_id: %s\nstatus: %s\n", ack.DelegationID, ack.Status), nil)
	}
	if s.companion == nil {
		return toolTextResponse(id, "", errors.New(name+": companion host is not available in this session"))
	}
	owner := strings.TrimSpace(callerKey)
	if isCompanionPhoneTool(name) && owner == "" {
		return toolTextResponse(id, "", fmt.Errorf("%s: session_key is required", name))
	}
	ttlMS := intFromArgument(args["ttl_ms"], 0)

	switch name {
	case companionSpeakTool:
		text, err := stringArg(name, "text", args)
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		s.companion.CompanionSpeak("*", text, ttlMS)
		return toolTextResponse(id, "ok\n", nil)

	case companionFaceTool:
		expression, err := stringArg(name, "expression", args)
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		s.companion.CompanionFace("*", expression, ttlMS)
		return toolTextResponse(id, "ok\n", nil)

	case companionMotionTool:
		gesture, err := stringArg(name, "gesture", args)
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		prop, _ := args["prop"].(string)
		s.companion.CompanionMotion("*", gesture, prop, ttlMS)
		return toolTextResponse(id, "ok\n", nil)

	case companionPhonePickupTool:
		res, err := s.companion.CompanionPhonePickup(owner)
		if err != nil {
			return toolErrorTextResponse(id, err.Error())
		}
		if wantsJSON(args) {
			return toolJSONResponse(id, res, nil)
		}
		return toolTextResponse(id, formatCompanionPhoneRead("picked up smartphone", res), nil)

	case companionPhoneReadTool:
		res, err := s.companion.CompanionPhoneRead(owner)
		if err != nil {
			return toolErrorTextResponse(id, err.Error())
		}
		if wantsJSON(args) {
			return toolJSONResponse(id, res, nil)
		}
		return toolTextResponse(id, formatCompanionPhoneRead("phone inbox", res), nil)

	case companionPhoneSendTool:
		text, err := stringArg(name, "text", args)
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		tone, _ := args["tone"].(string)
		msg, err := s.companion.CompanionPhoneSend(owner, text, tone)
		if err != nil {
			return toolErrorTextResponse(id, err.Error())
		}
		if wantsJSON(args) {
			return toolJSONResponse(id, msg, nil)
		}
		return toolTextResponse(id, fmt.Sprintf("sent %s\n", msg.ID), nil)

	case companionPhonePutdownTool:
		style, _ := args["style"].(string)
		next, _ := args["next"].(string)
		snap, err := s.companion.CompanionPhonePutdown(owner, style, next)
		if err != nil {
			return toolErrorTextResponse(id, err.Error())
		}
		if wantsJSON(args) {
			return toolJSONResponse(id, snap, nil)
		}
		return toolTextResponse(id, fmt.Sprintf("posture: %s (state: %s)\n", snap.Posture, snap.State), nil)

	default:
		return errorResponse(id, -32602, fmt.Sprintf("unknown companion tool: %s", name))
	}
}

func (s *Server) handleDelegationReport(id json.RawMessage, args map[string]any, callerKey string, caller sessionEntry, callerFound bool) response {
	const name = delegationReportTool
	if !callerFound || caller.companionRole != CompanionRoleWork {
		return errorResponse(id, -32601, fmt.Sprintf("tool not available in current %s MCP profile: %s", s.runtimeNamespace(), name))
	}
	host := s.companionDelegationHost()
	if host == nil {
		return toolTextResponse(id, "", errors.New(name+": companion delegation host is not available in this session"))
	}
	delegationID, err := stringArg(name, "delegation_id", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	status, err := stringArg(name, "status", args)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	switch status {
	case "done", "partial", "blocked":
	default:
		return toolTextResponse(id, "", fmt.Errorf("%s: status must be done, partial, or blocked", name))
	}
	result, ok := args["result"].(string)
	if !ok || strings.TrimSpace(result) == "" {
		return toolTextResponse(id, "", fmt.Errorf("%s: result must be a non-empty string", name))
	}
	artifacts := stringList(args["artifacts"])
	ack, err := host.CompanionDelegationReport(strings.TrimSpace(callerKey), delegationID, status, result, artifacts)
	if err != nil {
		return toolErrorTextResponse(id, err.Error())
	}
	if wantsJSON(args) {
		return toolJSONResponse(id, ack, nil)
	}
	return toolTextResponse(id, fmt.Sprintf("delegation_id: %s\nstatus: %s\n", ack.DelegationID, ack.Status), nil)
}

func formatCompanionPhoneRead(header string, res wsdashboard.CompanionPhoneReadResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (posture: %s, state: %s, unread: %d)\n", header, res.Posture, res.State, len(res.Unread))
	if len(res.Unread) > 0 {
		b.WriteString("\nUnread:\n")
		for _, m := range res.Unread {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", m.ID, m.Sender, m.Text)
		}
	}
	if len(res.Recent) > 0 {
		b.WriteString("\nRecent:\n")
		for _, m := range res.Recent {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", m.ID, m.Sender, m.Text)
		}
	}
	return b.String()
}
