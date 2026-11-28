package wscompact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ProseFieldBytes bounds machine- or companion-generated prose (model summaries,
// worker residuals). It does not apply to a lead's own lead_summary, which is
// carried forward whole: a UTF-8 byte cap penalizes non-ASCII text (Korean is 3
// bytes per character) and made valid summaries fail.
const ProseFieldBytes = 2 << 10

// MaxSummaryBytes bounds a model-generated summary reply (werk readCompactProse).
const MaxSummaryBytes = 32 << 10

// MaxLeadSummaryBytes is only a memory-DoS ceiling for one lead_summary
// argument, far above any legitimate carry-forward.
const MaxLeadSummaryBytes = 1 << 20

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

// LeadDraft is a leniently parsed lead_summary that remembers which fields the
// caller actually supplied, so a retry can overlay only what changed.
type LeadDraft struct {
	Prose     []string
	Has       []bool
	Paths     []string
	HasReread bool
}

// NewLeadDraft returns an empty draft with every field unset.
func NewLeadDraft() *LeadDraft {
	n := len(ProseFields())
	return &LeadDraft{Prose: make([]string, n), Has: make([]bool, n)}
}

// Empty reports whether no field was supplied.
func (d *LeadDraft) Empty() bool {
	if d == nil {
		return true
	}
	if d.HasReread {
		return false
	}
	for _, has := range d.Has {
		if has {
			return false
		}
	}
	return true
}

// Overlay returns base with every field set in d replacing the base value; a
// field d explicitly set to "" replaces too. Neither input is modified.
func (d *LeadDraft) Overlay(base *LeadDraft) *LeadDraft {
	out := NewLeadDraft()
	if base != nil {
		copy(out.Prose, base.Prose)
		copy(out.Has, base.Has)
		out.Paths, out.HasReread = append([]string(nil), base.Paths...), base.HasReread
	}
	if d == nil {
		return out
	}
	for i := range out.Prose {
		if i < len(d.Has) && d.Has[i] {
			out.Prose[i], out.Has[i] = d.Prose[i], true
		}
	}
	if d.HasReread {
		out.Paths, out.HasReread = append([]string(nil), d.Paths...), true
	}
	return out
}

// Summary finalizes the draft; unset fields become empty strings.
func (d *LeadDraft) Summary() *LeadSummary {
	out := &LeadSummary{Prose: make([]string, len(ProseFields()))}
	if d == nil {
		return out
	}
	copy(out.Prose, d.Prose)
	out.Paths = append([]string(nil), d.Paths...)
	return out
}

// LeadSummarySchema is the optional compact tool argument seen at discovery.
// No field is required: the server fills omitted ones, so a retry may resend
// only what changed.
func LeadSummarySchema() map[string]any {
	properties := map[string]any{
		"reread": map[string]any{
			"type": "array", "maxItems": maxRereads, "items": map[string]any{"type": "string"},
			"description": "Worktree text files to inline within the byte budget.",
		},
	}
	for _, field := range ProseFields() {
		properties[field.Key] = map[string]any{"type": "string"}
	}
	return map[string]any{
		"type": "object", "properties": properties, "additionalProperties": false,
		"description": "Hosted lead self-compaction prose. Omitted or empty sections carry nothing forward.",
	}
}

func validRereadPath(path string) bool {
	return strings.TrimSpace(path) != "" && len(path) <= 1024 && !strings.ContainsAny(path, "\r\n\x00")
}

// ParseLeadSummaryDraft leniently parses lead_summary. Recoverable problems
// (missing or null fields, scalar values, bad or surplus reread paths, unknown
// keys) become warnings. A non-coercible field or a non-object value is an
// error, but the draft still carries every field that parsed so the caller can
// keep them for a retry; draft is nil only when raw is not an object.
func ParseLeadSummaryDraft(raw json.RawMessage) (draft *LeadDraft, warnings []string, err error) {
	if len(raw) > MaxLeadSummaryBytes {
		return nil, nil, fmt.Errorf("lead_summary exceeds the %d byte safety ceiling", MaxLeadSummaryBytes)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, nil, errors.New("lead_summary must be a JSON object")
	}
	draft = NewLeadDraft()
	var problems []string
	for i, field := range ProseFields() {
		value, present := fields[field.Key]
		delete(fields, field.Key)
		value = bytes.TrimSpace(value)
		if !present || string(value) == "null" {
			if present {
				warnings = append(warnings, "lead_summary."+field.Key+" is null; ignored")
			}
			continue
		}
		var text string
		if json.Unmarshal(value, &text) == nil {
			draft.Prose[i], draft.Has[i] = strings.TrimSpace(text), true
			continue
		}
		var scalar json.Number
		var flag bool
		switch {
		case json.Unmarshal(value, &flag) == nil:
			draft.Prose[i], draft.Has[i] = fmt.Sprint(flag), true
		case json.Unmarshal(value, &scalar) == nil:
			draft.Prose[i], draft.Has[i] = scalar.String(), true
		default:
			problems = append(problems, "lead_summary."+field.Key+" must be a string")
			continue
		}
		warnings = append(warnings, "lead_summary."+field.Key+" was not a string; coerced to text")
	}
	if value, ok := fields["reread"]; ok {
		delete(fields, "reread")
		var items []any
		switch v := bytes.TrimSpace(value); {
		case string(v) == "null":
			warnings = append(warnings, "lead_summary.reread is null; ignored")
		case json.Unmarshal(v, &items) != nil:
			problems = append(problems, "lead_summary.reread must be an array of strings")
		default:
			draft.HasReread = true
			dropped := 0
			for _, item := range items {
				if path, ok := item.(string); ok && validRereadPath(path) {
					draft.Paths = append(draft.Paths, path)
				} else {
					dropped++
				}
			}
			if dropped > 0 {
				warnings = append(warnings, fmt.Sprintf("lead_summary.reread dropped %d blank or invalid path(s)", dropped))
			}
			if len(draft.Paths) > maxRereads {
				warnings = append(warnings, fmt.Sprintf("lead_summary.reread kept the first %d of %d paths", maxRereads, len(draft.Paths)))
				draft.Paths = draft.Paths[:maxRereads]
			}
		}
	}
	if len(fields) != 0 {
		unknown := make([]string, 0, len(fields))
		for key := range fields {
			unknown = append(unknown, key)
		}
		sort.Strings(unknown)
		warnings = append(warnings, "lead_summary ignored unknown key(s): "+strings.Join(unknown, ", "))
	}
	if len(problems) > 0 {
		err = errors.New(strings.Join(problems, "; "))
	}
	return draft, warnings, err
}

// ParseLeadSummary parses lead_summary leniently into a finished summary,
// dropping the warnings; only a non-object or non-coercible field is refused.
func ParseLeadSummary(raw json.RawMessage) (*LeadSummary, error) {
	draft, _, err := ParseLeadSummaryDraft(raw)
	if err != nil {
		return nil, err
	}
	return draft.Summary(), nil
}
