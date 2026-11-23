package wscompact

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const ProseFieldBytes = 2 << 10
const MaxSummaryBytes = 32 << 10

// ProseFields are the carry-forward fields, in checkpoint order.
func ProseFields() [7]struct{ Key, Heading string } {
	return [7]struct{ Key, Heading string }{
		{"user_preferences", "User preferences and style"},
		{"working_practices", "Agreed working practices"},
		{"unrecorded_decisions", "Decisions not yet recorded"},
		{"current_work", "Current work"},
		{"next_step", "Immediate next step"},
		{"mood_rapport", "Mood and rapport"},
		{"residual_details", "Residual details"},
	}
}

const maxRereads = 12

// LeadSummary holds validated carry-forward prose for the next checkpoint.
type LeadSummary struct {
	Prose []string
	Paths []string
}

// LeadSummarySchema is the optional compact tool argument seen at discovery.
func LeadSummarySchema() map[string]any {
	properties := map[string]any{
		"reread": map[string]any{
			"type": "array", "maxItems": maxRereads, "items": map[string]any{"type": "string"},
			"description": "Text files in this worktree to read again after compaction. The daemon inlines whole files within its byte budget.",
		},
	}
	required := make([]string, 0, len(ProseFields()))
	for _, field := range ProseFields() {
		properties[field.Key] = map[string]any{"type": "string", "description": field.Heading + ". Use an empty string when no details need carrying forward."}
		required = append(required, field.Key)
	}
	return map[string]any{
		"type": "object", "properties": properties, "required": required, "additionalProperties": false,
		"description": "A hosted builtin lead compacting itself may supply these seven carry-forward fields and optional re-read paths.",
	}
}

// ParseLeadSummary refuses malformed or excessive input before a turn is queued.
func ParseLeadSummary(raw json.RawMessage) (*LeadSummary, error) {
	var fields map[string]json.RawMessage
	if len(raw) > MaxSummaryBytes || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, errors.New("lead_summary must be a bounded object")
	}
	out := &LeadSummary{Prose: make([]string, len(ProseFields()))}
	for i, field := range ProseFields() {
		var text *string
		if json.Unmarshal(fields[field.Key], &text) != nil || text == nil || len(*text) > ProseFieldBytes {
			return nil, fmt.Errorf("lead_summary.%s requires a string of at most %d bytes", field.Key, ProseFieldBytes)
		}
		out.Prose[i] = strings.TrimSpace(*text)
		delete(fields, field.Key)
	}
	if raw, ok := fields["reread"]; ok {
		if json.Unmarshal(raw, &out.Paths) != nil || out.Paths == nil || len(out.Paths) > maxRereads {
			return nil, fmt.Errorf("lead_summary.reread takes at most %d paths", maxRereads)
		}
		for _, path := range out.Paths {
			if strings.TrimSpace(path) == "" || len(path) > 1024 || strings.ContainsAny(path, "\r\n\x00") {
				return nil, errors.New("lead_summary.reread contains an invalid path")
			}
		}
		delete(fields, "reread")
	}
	if len(fields) != 0 {
		return nil, errors.New("lead_summary contains unknown fields")
	}
	return out, nil
}
