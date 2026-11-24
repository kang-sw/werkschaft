package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wscompact"
	"github.com/kang-sw/devenv/internal/wsconfig"
)

func isCompactionKey(key string) bool { _, ok := wsconfig.CompactionDefaults[key]; return ok }
func isCompactionTuneKey(key string) bool {
	if isCompactionKey(key) {
		return true
	}
	switch key {
	// Retained name check: these are the harness-bucketed compaction config keys a
	// user's config may hold; registration data does not rename a stored key
	// (TestCompactionConfigGetTuneAndLegacy).
	case "claude.compaction_advisory_enabled",
		"claude.compaction_advisory_percent",
		"pi.compaction_advisory_percent",
		"pi.compaction_hard_percent":
		return true
	}
	return false
}
func notifyCompactionTune(key string) {
	if isCompactionTuneKey(key) {
		wsagent.NotifyCompactionConfigChanged()
	}
}
var compactionLeadModeEnum = []string{wsconfig.CompactionModeNative, wsconfig.CompactionModeWS}

func compactionLeadModeEntry() configKeyEntry {
	return configKeyEntry{
		Key: wsconfig.ItemCompactionLeadMode, WriterTool: "config.tune", ResetTool: "config.tune",
		ResolverBacked: true, HarnessApplicable: true, NoAgentVisible: true,
		SelectorFields: []tuningField{
			{Name: "harness", Description: "Harness override; * writes the shared layer value.", Enum: promptHarnessEnum},
			{Name: "scope", Enum: wsconfig.ScopeSchemaEnum()},
		},
		ValueFields: []tuningField{
			{Name: "value", Description: "Hosted lead compaction mode: native (harness-native compaction) or ws (ws-managed checkpoint and session rotation).", Enum: compactionLeadModeEnum},
		},
	}
}

func validateCompactionLeadModeTarget(key, harness, value string, registries ...map[string]wsagent.BackendRegistration) error {
	if key != wsconfig.ItemCompactionLeadMode || value != wsconfig.CompactionModeNative {
		return nil
	}
	if harness == "" || harness == "*" || harness == "all" {
		return nil
	}
	if wsagent.LeadCompactionWSOnly(harness, registries...) {
		return fmt.Errorf("config.tune: %s.%s cannot be set to native because %s only supports ws-managed lead compaction", key, harness, harness)
	}
	return nil
}

func compactionKeyEntry(key string) configKeyEntry {
	return configKeyEntry{Key: key, WriterTool: "config.tune", ResetTool: "config.tune", ResolverBacked: true, HarnessApplicable: true, NoAgentVisible: true,
		SelectorFields: []tuningField{{Name: "harness", Description: "Harness override; * writes the shared layer value.", Enum: promptHarnessEnum}, {Name: "scope", Enum: wsconfig.ScopeSchemaEnum()}},
		ValueFields:    []tuningField{{Name: "value", Description: "Percent (1-100), tokens (150k, 1.5m), or one of each joined by |; whichever occurs first."}},
		Normalize:      func(value string) (string, error) { _, err := wsconfig.ParseThreshold(value); return value, err },
	}
}
func (s *Server) compactionValue(r *wsconfig.Resolver, key, item, harness string) (wsconfig.ResolvedValue, string, error) {
	return resolveCompactionValue(r, key, item, harness, s.agentBackends()...)
}
func resolveCompactionValue(r *wsconfig.Resolver, key, item, harness string, registries ...map[string]wsagent.BackendRegistration) (wsconfig.ResolvedValue, string, error) {
	p := wsagent.BuiltinCompaction(harness)
	for _, regs := range registries {
		if reg, ok := regs[harness]; ok {
			p = reg.CompactionProtocol()
			break
		}
	}
	rv, answered, err := r.Compaction(key, item, harness, p.LegacyKey(item))
	if err == nil && item == wsconfig.ItemCompactionLeadMode {
		rv.Value = wsagent.EffectiveLeadCompactionMode(harness, rv.Value, registries...)
	}
	return rv, answered, err
}

func compactToolWithoutLeadSummary(tool map[string]any) map[string]any {
	cloned := make(map[string]any, len(tool))
	for k, v := range tool {
		cloned[k] = v
	}
	schema, ok := tool["inputSchema"].(map[string]any)
	if !ok {
		return cloned
	}
	newSchema := make(map[string]any, len(schema))
	for k, v := range schema {
		newSchema[k] = v
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		newProps := make(map[string]any, len(props))
		for k, v := range props {
			if k == "lead_summary" {
				continue
			}
			newProps[k] = v
		}
		newSchema["properties"] = newProps
	}
	cloned["inputSchema"] = newSchema
	return cloned
}

// compactAdvertisesLeadSummary reports whether tools/list should include the
// lead_summary argument on compact for this connection. Only hosted lead
// sessions with effective compaction.lead_mode=ws see it; workers, native-mode
// leads, and non-hosted/CLI sessions get the stripped schema.
func (s *Server) compactAdvertisesLeadSummary() bool {
	if s.conversation == "" || s.workerForwards() {
		return false
	}
	var sessionKey string
	harness := s.playbookHarness()
	leadConfirmed := false
	if getter, ok := s.agents.(interface {
		Get(string) (wsagent.State, error)
	}); ok {
		st, err := getter.Get(s.conversation)
		if err != nil || !st.Lead {
			return false
		}
		leadConfirmed = true
		if st.Backend != "" {
			harness = st.Backend
		}
	} else if s.userCommands != nil || s.userCommandsDiscovery != nil {
		if !s.userCommandsListed() {
			return false
		}
		leadConfirmed = true
	}
	if !leadConfirmed {
		return false
	}
	if s.callerBinding != nil {
		if b := s.callerBinding.CallerBackend(s.conversation, ""); b != "" {
			harness = b
		}
	}
	if sessionKey == "" && s.env != nil {
		sessionKey = strings.TrimSpace(s.env.get(wsagent.EnvHostedSessionKey))
	}
	resolver := s.sessionResolver(sessionKey, s.declared.withDefaults(builtinConfigDefaults()))
	rv, _, err := s.compactionValue(&resolver, sessionKey, wsconfig.ItemCompactionLeadMode, harness)
	return err == nil && rv.Value == wsconfig.CompactionModeWS
}

// compactTool is discoverable before use; an older daemon refuses the unknown
// tool. It never falls back to sending a slash prompt from a caller.
func compactTool() map[string]any {
	return map[string]any{"name": "compact", "description": "Queue compaction for the next hosted idle boundary. Save your agenda first. Discover support before calling. Queued means pending.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{
		"session_key":  map[string]any{"type": "string", "description": "Your ws session key."},
		"lead_summary": wscompact.LeadSummarySchema(),
		"target":       map[string]any{"type": "string", "description": "Optional self key or owned descendant key / spawn address id:<id>; defaults to self."},
		"resume":       map[string]any{"type": "boolean", "description": "Start one continue turn after confirmed compaction and restoration. Defaults true for self, false for a descendant."},
		"format":       map[string]any{"type": "string", "enum": []string{"text", "json"}},
	}, "required": []string{"session_key"}}}
}

type compactRunner interface {
	Compact(string, string, *bool) wsagent.CompactResult
}

func (s *Server) handleCompact(ctx context.Context, id json.RawMessage, args map[string]any) response {
	key, _ := args["session_key"].(string)
	key = strings.TrimSpace(key)
	if _, ok := s.lookupSession(key); !ok {
		return toolTextResponse(id, "", fmt.Errorf("compact: unknown_session"))
	}
	target, _ := args["target"].(string)
	var resume *bool
	if b, ok := args["resume"].(bool); ok {
		resume = &b
	}
	out := wsagent.CompactResult{Status: "unsupported", Reason: "native compaction requires a hosted session"}
	if value, present := args["lead_summary"]; present {
		raw, err := json.Marshal(value)
		if err != nil {
			return toolTextResponse(id, "", fmt.Errorf("invalid lead_summary: %w", err))
		}
		prose, err := wscompact.ParseLeadSummary(raw)
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		if runner, ok := s.agents.(interface {
			CompactWithSummary(string, string, *bool, *wscompact.LeadSummary) wsagent.CompactResult
		}); ok {
			out = runner.CompactWithSummary(key, target, resume, prose)
		} else {
			out = wsagent.CompactResult{Status: "unsupported", Reason: "lead_summary requires a hosted lead session"}
		}
	} else if runner, ok := s.agents.(compactRunner); ok {
		out = runner.Compact(key, target, resume)
	} else if s.workerForwards() {
		// The token route limits this request to the token's own key.
		if err := s.workerCall(ctx, "compact", map[string]any{"target": target, "resume": resume}, workerForwardTimeout, &out); err != nil {
			out = wsagent.CompactResult{Status: "error", Reason: err.Error()}
		}
	}
	if wantsJSON(args) {
		return toolJSONResponse(id, out, nil)
	}
	return toolTextResponse(id, fmt.Sprintf("%s: %s", out.Status, out.Reason), nil)
}
