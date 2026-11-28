package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

const (
	companionSpeakTool           = "companion.speak"
	companionFaceTool            = "companion.face"
	companionMotionTool          = "companion.motion"
	companionPhonePickupTool     = "companion.phone_pickup"
	companionPhoneReadTool       = "companion.phone_read"
	companionPhoneSendTool       = "companion.phone_send"
	companionPhonePutdownTool    = "companion.phone_putdown"
	companionDelegateWorkTool    = "companion.delegate_work"
	companionActTool             = "companion.act"
	companionRememberSoulTool    = "companion.remember_soul"
	companionRememberEpisodeTool = "companion.remember_episode"
	companionRecallMemoryTool    = "companion.recall_memory"
	delegationReportTool         = "delegation.report"

	// companionBurstMaxCalls bounds Tier-1 burst tool calls (act, memory tools,
	// delegate_work) per lease/turn (D11).
	companionBurstMaxCalls = wsdashboard.CompanionBurstMaxCalls

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

// CompanionActHost bridges ws/companion.act to the daemon's interruptible wait
// hub and companion-command rail broadcast (D7, D10).
type CompanionActHost interface {
	CompanionAct(ctx context.Context, owner string, req wsdashboard.CompanionActRequest) (wsdashboard.CompanionActResult, error)
}

// CompanionMemoryHost bridges ws/companion.{remember_soul,remember_episode,recall_memory}
// to the daemon's Portable Soul RAM cache, 2-layer scrubber, and Local Episodes
// SQLite FTS5 store (D3, D8).
type CompanionMemoryHost interface {
	CompanionRememberSoul(ctx context.Context, owner, content, kind, projectKey string, tags []string) (wsdashboard.RememberSoulResult, error)
	CompanionRememberEpisode(ctx context.Context, owner, projectKey, summary string, tags []string) (wsdashboard.CompanionEpisode, error)
	CompanionRecallMemory(ctx context.Context, query, scope, projectKey string, limit int) (wsdashboard.CompanionRecallResult, error)
}

// CompanionBurstHost tracks the daemon-side per-turn burst limit for Tier-1
// companion-body sessions (D11).
type CompanionBurstHost interface {
	CompanionConsumeBurstCall(owner string, limit int) (count int, allowed bool)
	CompanionResetBurst(owner string)
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

// isCompanionMemoryTool reports the Soul/Episodes memory tools exclusively
// owned by Brain (companion-work).
func isCompanionMemoryTool(name string) bool {
	switch name {
	case companionRememberSoulTool, companionRememberEpisodeTool, companionRecallMemoryTool:
		return true
	default:
		return false
	}
}

// isCompanionBurstCountedTool reports the Tier-1 tools subject to the
// companionBurstMaxCalls = 12 per-turn cap (D11). Deprecated no-op aliases
// are exempt so legacy callers never stall on burst limits.
func isCompanionBurstCountedTool(name string) bool {
	return name == companionActTool
}

// isCompanionDeprecatedAlias reports legacy 2-tier phone and FIFO delegation
// tools kept registered as safe T0 no-op/redirect aliases.
func isCompanionDeprecatedAlias(name string) bool {
	return isCompanionPhoneTool(name) || name == companionDelegateWorkTool || name == delegationReportTool
}

func companionDeprecatedNotice(name string) string {
	switch name {
	case companionDelegateWorkTool:
		return "ws/companion.delegate_work is deprecated: Companion Messenger now connects directly to the Mobile Full Lead (Brain), which executes work and spawns subagents via ws/agent.spawn without a Body-to-Brain delegation queue."
	case delegationReportTool:
		return "ws/delegation.report is deprecated: Brain now replies directly to the user inside the Companion Messenger conversation instead of reporting back through a Body relay."
	default:
		return fmt.Sprintf("ws/%s is deprecated: Companion Messenger now connects directly to the Mobile Full Lead (Brain) and VRM choreography is driven automatically by client reflex and Puppeteer. No phone lease or call is required.", name)
	}
}

func companionDeprecatedAliasResponse(id json.RawMessage, name string) response {
	return toolJSONResponse(id, struct {
		Deprecated bool   `json:"deprecated"`
		Tool       string `json:"tool"`
		Notice     string `json:"notice"`
	}{
		Deprecated: true,
		Tool:       name,
		Notice:     companionDeprecatedNotice(name),
	}, nil)
}

// isCompanionBodyOnlyTool reports tools exclusively available to Tier-1
// (companion-body) sessions and never visible/reachable by unmarked leads or
// Tier-2 (companion-work).
func isCompanionBodyOnlyTool(name string) bool {
	return isCompanionPhoneTool(name) || name == companionDelegateWorkTool || name == companionActTool
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
			"description": "[Deprecated T0 alias] Companion Messenger now connects directly to Brain and VRM phone motion is driven by client reflex and Puppeteer; returns a structured deprecation notice without acquiring a lease.",
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
			"description": "[Deprecated T0 alias] Companion Messenger now connects directly to Brain; returns a structured deprecation notice without blocking on phone posture.",
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
			"description": "[Deprecated T0 alias] Companion Messenger now connects directly to Brain; returns a structured deprecation notice without writing to the legacy phone store.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"text":        stringProperty("Legacy reply message text (ignored)."),
					"tone":        stringProperty("Legacy expression/reaction tone (ignored)."),
					"format":      stringProperty(formatPropertyDescription),
				},
				"required": []string{"text"},
			},
		},
		{
			"name":        companionPhonePutdownTool,
			"description": "[Deprecated T0 alias] VRM phone putdown is now driven automatically by client reflex and Puppeteer; returns a structured deprecation notice without blocking on leases.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"style":       enumStringProperty(`Legacy putdown style: "normal" or "facedown_merong".`, []string{"normal", "facedown_merong"}),
					"next":        enumStringProperty(`Legacy next state: "ambient" or "work".`, []string{"ambient", "work"}),
					"format":      stringProperty(formatPropertyDescription),
				},
			},
		},
		{
			"name":        companionDelegateWorkTool,
			"description": "[Deprecated T0 alias] Companion Messenger now runs directly on the Mobile Full Lead (Brain) without a Body-to-Brain FIFO; returns a structured deprecation notice.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"brief":       stringProperty("Legacy task brief (ignored)."),
					"format":      stringProperty(formatPropertyDescription),
				},
				"required": []string{"brief"},
			},
		},
		{
			"name":        companionActTool,
			"description": "Broadcast a companion visual/speech beat and wait interruptibly for delay_ms (default 3000 ms, clamped to 1500–6000 ms), waking immediately on an incoming user message, glass knock, or shutdown. Allowed at both phone and laptop posture for companion-body.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"expression":  stringProperty("Optional facial expression ID (e.g. idle, focused, happy, wink, merong, startled, annoyed, cry)."),
					"gesture":     stringProperty("Optional gesture ID (e.g. wave, point, headrub, coffee, knock_startle, phone_typing) or an action key from the ACTIONS block."),
					"prop":        stringProperty("Optional prop ID (e.g. smartphone, drone, coffee, mail)."),
					"fx":          stringProperty("Optional visual effect badge ID (e.g. sparkle, sweat, question, exclamation)."),
					"speech":      stringProperty("Optional short floating speech/quip bubble text (clamped to 140 runes)."),
					"quip":        stringProperty("Optional alias for speech."),
					"delay_ms":    integerProperty("Optional pacing wait in milliseconds (omitted defaults to 3000 ms; provided values clamp to 1500–6000 ms)."),
					"format":      stringProperty(formatPropertyDescription),
				},
			},
		},
		{
			"name":        companionRememberSoulTool,
			"description": "Append a portable personal/rapport memory to the active Soul's per-machine JSONL shard after deterministic 2-layer secret/PII scrubbing (secrets rejected; paths/emails/URLs held in Settings approval queue; ticket stems/project names downgraded to Local Episodes). Never accesses ws/note.*.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"content":     stringProperty("Portable personal/rapport memory text."),
					"kind":        stringProperty("Optional category (e.g. preference, rapport, milestone, trait, note). Defaults to note."),
					"tags": map[string]any{
						"type":        "array",
						"description": "Optional short tags.",
						"items":       map[string]any{"type": "string"},
					},
					"project_key": stringProperty("Optional project key used only if the scrubber downgrades project-specific content to Local Episodes."),
					"format":      stringProperty(formatPropertyDescription),
				},
				"required": []string{"content"},
			},
		},
		{
			"name":        companionRememberEpisodeTool,
			"description": "Record a machine-local project episode in SQLite FTS5 (never synced to Portable Soul and never accesses ws/note.*).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"summary":     stringProperty("Machine-local episodic summary of project work, debugging, or technical context."),
					"project_key": stringProperty("Optional project scope key (defaults to the current workspace/project name)."),
					"tags": map[string]any{
						"type":        "array",
						"description": "Optional short tags.",
						"items":       map[string]any{"type": "string"},
					},
					"format": stringProperty(formatPropertyDescription),
				},
				"required": []string{"summary"},
			},
		},
		{
			"name":        companionRecallMemoryTool,
			"description": "Search Portable Soul shards (daemon RAM hot cache) and/or machine-local episodes (SQLite FTS5). Never accesses ws/note.*.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key": stringProperty(sessionKeyPropertyDescription),
					"query":       stringProperty("Optional search query; omit to retrieve recent memories."),
					"scope":       enumStringProperty(`Memory layer scope: "all" (default), "soul" (Portable Soul RAM cache), or "episodes" (Local Episodes FTS5).`, []string{"all", "soul", "episodes"}),
					"project_key": stringProperty(`Optional project filter for Local Episodes (defaults to the current workspace/project; pass "*" to search across all projects).`),
					"limit":       integerProperty("Optional maximum snippets to return (default 10, max 30)."),
					"format":      stringProperty(formatPropertyDescription),
				},
			},
		},
	}
}

func delegationTools() []map[string]any {
	return []map[string]any{
		{
			"name":        delegationReportTool,
			"description": "[Deprecated T0 alias] Brain now replies directly to the user in Companion Messenger without a Body relay; returns a structured deprecation notice.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_key":   stringProperty(sessionKeyPropertyDescription),
					"delegation_id": stringProperty("Legacy delegation identifier (ignored)."),
					"status":        enumStringProperty("Legacy outcome status (ignored).", []string{"done", "partial", "blocked"}),
					"result":        stringProperty("Legacy technical result (ignored)."),
					"artifacts": map[string]any{
						"type":        "array",
						"description": "Legacy artifact paths or links (ignored).",
						"items":       map[string]any{"type": "string"},
					},
					"format": stringProperty(formatPropertyDescription),
				},
				"required": []string{"delegation_id", "status", "result"},
			},
		},
	}
}

func (s *Server) companionActHost() CompanionActHost {
	if host, ok := s.companion.(CompanionActHost); ok {
		return host
	}
	return nil
}

func (s *Server) companionMemoryHost() CompanionMemoryHost {
	if host, ok := s.companion.(CompanionMemoryHost); ok {
		return host
	}
	return nil
}

func (s *Server) companionBurstHost() CompanionBurstHost {
	if host, ok := s.companion.(CompanionBurstHost); ok {
		return host
	}
	return nil
}

// ResetCompanionBurst resets the per-turn burst counter for owner on both the
// daemon host (when present) and the server's local fallback table.
func (s *Server) ResetCompanionBurst(owner string) {
	if host := s.companionBurstHost(); host != nil {
		host.CompanionResetBurst(owner)
	}
	owner = strings.TrimSpace(owner)
	s.companionBurstMu.Lock()
	if s.companionBurstCounts != nil {
		if owner == "" {
			clear(s.companionBurstCounts)
		} else {
			delete(s.companionBurstCounts, owner)
		}
	}
	s.companionBurstMu.Unlock()
}

// companionBurstRefusal enforces the D11 companionBurstMaxCalls = 12 limit for
// counted Tier-1 tools (act, memory tools, delegate_work) per lease/turn.
func (s *Server) companionBurstRefusal(name, sessionKey string, entry sessionEntry, found bool) string {
	if !found || entry.companionRole != CompanionRoleBody || !isCompanionBurstCountedTool(name) {
		return ""
	}
	owner := strings.TrimSpace(sessionKey)
	if owner == "" {
		owner = strings.TrimSpace(s.hostedSessionKey())
	}
	if owner == "" {
		return ""
	}
	var allowed bool
	if host := s.companionBurstHost(); host != nil {
		_, allowed = host.CompanionConsumeBurstCall(owner, companionBurstMaxCalls)
	} else {
		s.companionBurstMu.Lock()
		if s.companionBurstCounts == nil {
			s.companionBurstCounts = make(map[string]int)
		}
		if s.companionBurstCounts[owner] < companionBurstMaxCalls {
			s.companionBurstCounts[owner]++
			allowed = true
		}
		s.companionBurstMu.Unlock()
	}
	if !allowed {
		return formatStructuredGateError(
			"burst_limit",
			name,
			"",
			fmt.Sprintf("Companion burst call limit (%d) reached for this turn — end your turn cleanly.", companionBurstMaxCalls),
		)
	}
	return ""
}

// companionPostureRefusal enforces the strict server-side laptop|phone posture
// lock for both Server.callTool and Server.callDiscoveryTools, scoped strictly
// to companion-body sessions ((1740)). Unmarked sessions and companion-work are
// never posture-locked.
func (s *Server) companionPostureRefusal(name, sessionKey string, entry sessionEntry, found bool) string {
	if isCompanionDeprecatedAlias(name) {
		return ""
	}
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

func (s *Server) handleCompanionTool(ctx context.Context, id json.RawMessage, name string, args map[string]any, callerKey string, caller sessionEntry, callerFound bool) response {
	if isCompanionMemoryTool(name) {
		if !callerFound || caller.companionRole != CompanionRoleWork {
			return errorResponse(id, -32601, fmt.Sprintf("tool not available in current %s MCP profile: %s", s.runtimeNamespace(), name))
		}
	} else if isCompanionBodyOnlyTool(name) && (!callerFound || caller.companionRole != CompanionRoleBody) {
		return errorResponse(id, -32601, fmt.Sprintf("tool not available in current %s MCP profile: %s", s.runtimeNamespace(), name))
	}
	if isCompanionDeprecatedAlias(name) {
		return companionDeprecatedAliasResponse(id, name)
	}
	if name == companionActTool {
		host := s.companionActHost()
		if host == nil {
			return toolTextResponse(id, "", errors.New(name+": companion act host is not available in this session"))
		}
		owner := strings.TrimSpace(callerKey)
		speech, _ := args["speech"].(string)
		if strings.TrimSpace(speech) == "" {
			speech, _ = args["quip"].(string)
		}
		rawDelay, hasDelay := args["delay_ms"]
		if hasDelay && rawDelay == nil {
			hasDelay = false
		}
		delayMS := 0
		if hasDelay {
			delayMS = intFromArgument(rawDelay, 0)
		}
		cosmeticOnly, _ := args["cosmetic_only"].(bool)
		req := wsdashboard.CompanionActRequest{
			SessionID:    "*",
			Expression:   strings.TrimSpace(stringValue(args["expression"])),
			Gesture:      strings.TrimSpace(stringValue(args["gesture"])),
			Prop:         strings.TrimSpace(stringValue(args["prop"])),
			FX:           strings.TrimSpace(stringValue(args["fx"])),
			Speech:       strings.TrimSpace(speech),
			DelayMS:      delayMS,
			HasDelay:     hasDelay,
			CosmeticOnly: cosmeticOnly,
			Priority:     strings.TrimSpace(stringValue(args["priority"])),
		}
		waitCtx, cancel := mergeDone(ctx, s.drainContext())
		defer cancel()
		res, err := host.CompanionAct(waitCtx, owner, req)
		if err != nil {
			return toolErrorTextResponse(id, err.Error())
		}
		if wantsJSON(args) {
			return toolJSONResponse(id, res, nil)
		}
		return toolTextResponse(id, formatCompanionActResult(res), nil)
	}
	if name == companionRememberSoulTool || name == companionRememberEpisodeTool || name == companionRecallMemoryTool {
		host := s.companionMemoryHost()
		if host == nil {
			return toolTextResponse(id, "", errors.New(name+": companion memory host is not available in this session"))
		}
		owner := strings.TrimSpace(callerKey)
		defaultProjectKey := companionProjectKeyFromRoot(caller.root)
		switch name {
		case companionRememberSoulTool:
			content, err := stringArg(name, "content", args)
			if err != nil {
				return toolTextResponse(id, "", err)
			}
			kind := strings.TrimSpace(stringValue(args["kind"]))
			projectKey := strings.TrimSpace(stringValue(args["project_key"]))
			if projectKey == "" {
				projectKey = defaultProjectKey
			}
			tags := stringList(args["tags"])
			res, err := host.CompanionRememberSoul(ctx, owner, content, kind, projectKey, tags)
			if err != nil {
				return toolErrorTextResponse(id, err.Error())
			}
			if wantsJSON(args) {
				return toolJSONResponse(id, res, nil)
			}
			return toolTextResponse(id, formatRememberSoulResult(res), nil)

		case companionRememberEpisodeTool:
			summary, err := stringArg(name, "summary", args)
			if err != nil {
				return toolTextResponse(id, "", err)
			}
			projectKey := strings.TrimSpace(stringValue(args["project_key"]))
			if projectKey == "" {
				projectKey = defaultProjectKey
			}
			tags := stringList(args["tags"])
			ep, err := host.CompanionRememberEpisode(ctx, owner, projectKey, summary, tags)
			if err != nil {
				return toolErrorTextResponse(id, err.Error())
			}
			if wantsJSON(args) {
				return toolJSONResponse(id, ep, nil)
			}
			return toolTextResponse(id, fmt.Sprintf("saved episode #%d (project: %s)\n", ep.ID, ep.ProjectKey), nil)

		case companionRecallMemoryTool:
			query := strings.TrimSpace(stringValue(args["query"]))
			scope := strings.TrimSpace(stringValue(args["scope"]))
			projectKey := strings.TrimSpace(stringValue(args["project_key"]))
			if _, provided := args["project_key"]; !provided {
				projectKey = defaultProjectKey
			}
			limit := intFromArgument(args["limit"], 0)
			res, err := host.CompanionRecallMemory(ctx, query, scope, projectKey, limit)
			if err != nil {
				return toolErrorTextResponse(id, err.Error())
			}
			if wantsJSON(args) {
				return toolJSONResponse(id, res, nil)
			}
			return toolTextResponse(id, formatCompanionRecallResult(res), nil)
		}
	}
	if s.companion == nil {
		return toolTextResponse(id, "", errors.New(name+": companion host is not available in this session"))
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

	default:
		return errorResponse(id, -32602, fmt.Sprintf("unknown companion tool: %s", name))
	}
}

func (s *Server) handleDelegationReport(id json.RawMessage, args map[string]any, callerKey string, caller sessionEntry, callerFound bool) response {
	const name = delegationReportTool
	if !callerFound || caller.companionRole != CompanionRoleWork {
		return errorResponse(id, -32601, fmt.Sprintf("tool not available in current %s MCP profile: %s", s.runtimeNamespace(), name))
	}
	return companionDeprecatedAliasResponse(id, name)
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func companionProjectKeyFromRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	base := filepath.Base(filepath.Clean(root))
	if base == "." || base == "/" || base == `\` {
		return ""
	}
	return base
}

func formatCompanionActResult(res wsdashboard.CompanionActResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wake_reason: %s\ndelay_ms: %d\n", res.WakeReason, res.DelayMS)
	if len(res.UserMessages) > 0 {
		b.WriteString("\nUser messages:\n")
		for _, m := range res.UserMessages {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", m.ID, m.Sender, m.Text)
		}
	}
	if res.Knock != nil {
		fmt.Fprintf(&b, "knock: kind=%s count=%d\n", res.Knock.Kind, res.Knock.Count)
	}
	return b.String()
}

func formatRememberSoulResult(res wsdashboard.RememberSoulResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status: %s\n", res.Status)
	if res.Entry != nil {
		fmt.Fprintf(&b, "entry_id: %s\n", res.Entry.ID)
	}
	if res.Pending != nil {
		fmt.Fprintf(&b, "pending_id: %d (held for Settings approval)\n", res.Pending.ID)
	}
	if res.Episode != nil {
		fmt.Fprintf(&b, "episode_id: %d (downgraded to local episodes, project: %s)\n", res.Episode.ID, res.Episode.ProjectKey)
	}
	if len(res.Findings) > 0 {
		var kinds []string
		for _, f := range res.Findings {
			if f.Rule != "" {
				kinds = append(kinds, string(f.Category)+":"+f.Rule)
			} else {
				kinds = append(kinds, string(f.Category))
			}
		}
		fmt.Fprintf(&b, "findings: %s\n", strings.Join(kinds, ", "))
	}
	return b.String()
}

func formatCompanionRecallResult(res wsdashboard.CompanionRecallResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "scope: %s (hits: %d)\n", res.Scope, len(res.Snippets))
	for _, s := range res.Snippets {
		meta := strings.TrimSpace(s.Source)
		if ts := strings.TrimSpace(s.Timestamp); ts != "" {
			if len([]rune(ts)) > 10 {
				ts = string([]rune(ts)[:10])
			}
			if meta != "" {
				meta += " " + ts
			} else {
				meta = ts
			}
		}
		if meta != "" {
			fmt.Fprintf(&b, "- [%s] %s", meta, s.Content)
		} else {
			fmt.Fprintf(&b, "- %s", s.Content)
		}
		if len(s.Tags) > 0 {
			fmt.Fprintf(&b, " (#%s)", strings.Join(s.Tags, " #"))
		}
		b.WriteByte('\n')
	}
	return b.String()
}
