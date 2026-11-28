package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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

func compactionWorkerModeEntry() configKeyEntry {
	e := compactionLeadModeEntry()
	e.Key = wsconfig.ItemCompactionWorkerMode
	e.ValueFields = []tuningField{
		{Name: "value", Description: "Hosted worker compaction mode: native (harness-native compaction) or ws (ws-managed checkpoint and session rotation).", Enum: compactionLeadModeEnum},
	}
	return e
}

func validateCompactionWorkerModeTarget(key, harness, value string, registries ...map[string]wsagent.BackendRegistration) error {
	if key != wsconfig.ItemCompactionWorkerMode || value != wsconfig.CompactionModeNative {
		return nil
	}
	if harness == "" || harness == "*" || harness == "all" {
		return nil
	}
	if wsagent.WorkerCompactionWSOnly(harness, registries...) {
		return fmt.Errorf("config.tune: %s.%s cannot be set to native because %s only supports ws-managed worker compaction", key, harness, harness)
	}
	return nil
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
	if err == nil && item == wsconfig.ItemCompactionWorkerMode {
		rv.Value = wsagent.EffectiveWorkerCompactionMode(harness, rv.Value, registries...)
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
	binding, ok := s.agents.(interface{ LeadCompactionMode(string) string })
	return ok && binding.LeadCompactionMode(s.conversation) == wsconfig.CompactionModeWS
}

// compactTool is discoverable before use; an older daemon refuses the unknown
// tool. It never falls back to sending a slash prompt from a caller.
func compactTool() map[string]any {
	return map[string]any{"name": "compact", "description": "Queue compaction for the next hosted idle boundary, with optional agenda_updates. Queued means pending.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{
		"session_key":    map[string]any{"type": "string", "description": "Your ws session key."},
		"lead_summary":   wscompact.LeadSummarySchema(),
		"agenda_updates": map[string]any{"type": "object", "description": "Self-only map: key -> null (clear) or {value, merge?} (replace/RFC 7396 merge)."},
		"target":         map[string]any{"type": "string", "description": "Self key or owned descendant key/id:<id>; default self."},
		"resume":         map[string]any{"type": "boolean", "description": "Continue turn after compaction; defaults true for self, false for descendant."},
		"format":         map[string]any{"type": "string", "enum": []string{"text", "json"}},
	}, "required": []string{"session_key"}}}
}

type compactRunner interface {
	Compact(string, string, *bool) wsagent.CompactResult
}

func parseCompactAgendaUpdates(sessionKey, target string, raw any) ([]agendaUpdateOp, error) {
	if target != "" && target != sessionKey {
		return nil, fmt.Errorf("compact: agenda_updates is only allowed when compacting self")
	}
	updatesMap, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("compact: agenda_updates must be a JSON object")
	}
	rawKeys := make([]string, 0, len(updatesMap))
	for k := range updatesMap {
		rawKeys = append(rawKeys, k)
	}
	sort.Strings(rawKeys)

	// Every problem is collected so one retry can fix them all.
	var problems []string
	seen := make(map[string]bool, len(rawKeys))
	ops := make([]agendaUpdateOp, 0, len(rawKeys))
	for _, rawKey := range rawKeys {
		trimmed := strings.TrimSpace(rawKey)
		if trimmed == "" {
			problems = append(problems, "key must be non-empty")
			continue
		}
		if seen[trimmed] {
			problems = append(problems, fmt.Sprintf("duplicate key %q", trimmed))
			continue
		}
		seen[trimmed] = true

		entry := updatesMap[rawKey]
		if entry == nil {
			ops = append(ops, agendaUpdateOp{key: trimmed, clear: true})
			continue
		}
		obj, ok := entry.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("[%q] must be null or {value, merge?}", trimmed))
			continue
		}
		bad := false
		for _, field := range sortedKeys(obj) {
			if field != "value" && field != "merge" {
				problems = append(problems, fmt.Sprintf("[%q] has unknown field %q (allowed: value, merge)", trimmed, field))
				bad = true
			}
		}
		value, hasValue := obj["value"]
		if !hasValue {
			problems = append(problems, fmt.Sprintf("[%q].value is required", trimmed))
			bad = true
		}
		merge := false
		if rawMerge, present := obj["merge"]; present && rawMerge != nil {
			b, isBool := rawMerge.(bool)
			if !isBool {
				problems = append(problems, fmt.Sprintf("[%q].merge must be a boolean", trimmed))
				bad = true
			}
			merge = b
		}
		if _, isObj := value.(map[string]any); hasValue && merge && !isObj {
			problems = append(problems, fmt.Sprintf("[%q].value must be a JSON object when merge is true", trimmed))
			bad = true
		}
		if bad {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			problems = append(problems, fmt.Sprintf("[%q].value is not JSON-encodable: %v", trimmed, err))
			continue
		}
		ops = append(ops, agendaUpdateOp{key: trimmed, value: encoded, merge: merge})
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("compact: agenda_updates has %d error(s): %s", len(problems), strings.Join(problems, "; "))
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].key < ops[j].key })
	return ops, nil
}

func (s *Server) handleCompact(ctx context.Context, id json.RawMessage, args map[string]any) response {
	key, _ := args["session_key"].(string)
	key = strings.TrimSpace(key)
	if _, ok := s.lookupSession(key); !ok {
		return toolTextResponse(id, "", fmt.Errorf("compact: unknown_session"))
	}
	target, _ := args["target"].(string)
	target = strings.TrimSpace(target)
	var resume *bool
	if b, ok := args["resume"].(bool); ok {
		resume = &b
	}
	selfCompact := target == "" || target == key
	summarizer, canSummarize := s.agents.(interface {
		CompactWithSummary(string, string, *bool, *wscompact.LeadSummary) wsagent.CompactResult
	})
	canSummarize = canSummarize && !s.workerForwards()
	var (
		hasLeadSummary bool
		prose          *wscompact.LeadSummary
		supplied       *wscompact.LeadDraft
		warnings       []string
		leadErr        error
	)
	if value, present := args["lead_summary"]; present && value != nil {
		raw, err := json.Marshal(value)
		if err != nil {
			return toolTextResponse(id, "", fmt.Errorf("invalid lead_summary: %w", err))
		}
		supplied, warnings, leadErr = wscompact.ParseLeadSummaryDraft(raw)
	}
	var agendaOps []agendaUpdateOp
	var agendaErr error
	if rawUpdates, present := args["agenda_updates"]; present && rawUpdates != nil {
		agendaOps, agendaErr = parseCompactAgendaUpdates(key, target, rawUpdates)
	}
	if leadErr != nil || agendaErr != nil {
		var problems []string
		if leadErr != nil {
			problems = append(problems, "compact: "+leadErr.Error())
		}
		if agendaErr != nil {
			problems = append(problems, agendaErr.Error())
		}
		if len(warnings) > 0 {
			problems = append(problems, "lead_summary warnings: "+strings.Join(warnings, "; "))
		}
		// Only a caller whose retry can reach CompactWithSummary gets a draft;
		// otherwise a cached draft could never be used or cleared by a success.
		if canSummarize && selfCompact && !supplied.Empty() {
			s.compactDrafts.merge(key, supplied)
			problems = append(problems, fmt.Sprintf("Valid lead_summary fields were cached in server memory for %d minutes: fix the errors above and retry omitting lead_summary, or send only the changed fields.", int(compactDraftTTL/time.Minute)))
		}
		return toolTextResponse(id, "", errors.New(strings.Join(problems, "\n")))
	}
	// A retry's fields overlay the draft a failed call left behind.
	var cached *wscompact.LeadDraft
	if canSummarize && selfCompact {
		cached = s.compactDrafts.get(key)
		if cached != nil && supplied == nil && s.conversation != "" {
			// A bare retry must not replay a stale draft as ws-managed once the
			// lead was switched to native compaction.
			if binding, ok := s.agents.(interface{ LeadCompactionMode(string) string }); ok &&
				binding.LeadCompactionMode(s.conversation) == wsconfig.CompactionModeNative {
				s.compactDrafts.clear(key)
				cached = nil
			}
		}
	}
	if supplied != nil || cached != nil {
		hasLeadSummary = true
		prose = supplied.Overlay(cached).Summary()
	}
	out := wsagent.CompactResult{Status: "unsupported", Reason: "native compaction requires a hosted session"}
	if hasLeadSummary {
		if canSummarize {
			out = summarizer.CompactWithSummary(key, target, resume, prose)
		} else {
			out = wsagent.CompactResult{Status: "unsupported", Reason: "lead_summary requires a hosted lead session"}
		}
		// A replayed draft the runner refuses for role can never succeed: drop
		// it so the caller's next bare compact is not locked out.
		if cached != nil && supplied == nil && selfCompact &&
			(out.Status == "unsupported" || (out.Status == "error" && strings.HasPrefix(out.Reason, wsagent.LeadSummaryRefusalPrefix))) {
			s.compactDrafts.clear(key)
		}
	} else if runner, ok := s.agents.(compactRunner); ok {
		out = runner.Compact(key, target, resume)
	} else if s.workerForwards() {
		// The token route limits this request to the token's own key.
		if err := s.workerCall(ctx, "compact", map[string]any{"target": target, "resume": resume}, workerForwardTimeout, &out); err != nil {
			out = wsagent.CompactResult{Status: "error", Reason: err.Error()}
		}
	}
	queued := out.Status == "queued" || out.Status == "pending"
	if queued && selfCompact {
		s.compactDrafts.clear(key)
	}
	var agendaNote string
	if queued && len(agendaOps) > 0 {
		var err error
		agendaNote, err = s.sessions.applyAgendaUpdates(key, agendaOps)
		if err != nil {
			return toolTextResponse(id, "", fmt.Errorf("compact: agenda_updates: %w", err))
		}
	}
	if wantsJSON(args) {
		return toolJSONResponse(id, struct {
			wsagent.CompactResult
			Warnings []string `json:"warnings,omitempty"`
		}{out, warnings}, nil)
	}
	text := fmt.Sprintf("%s: %s", out.Status, out.Reason)
	if agendaNote != "" {
		text += "\n" + agendaNote
	}
	if len(warnings) > 0 {
		text += "\nlead_summary warnings: " + strings.Join(warnings, "; ")
	}
	return toolTextResponse(id, text, nil)
}
