package wscompact

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLeadSummarySchemaHasNoRequiredFields(t *testing.T) {
	schema := LeadSummarySchema()
	if _, ok := schema["required"]; ok {
		t.Fatalf("lead_summary schema must not require fields: %v", schema["required"])
	}
	if len(schema["properties"].(map[string]any)) != len(ProseFields())+1 {
		t.Fatalf("properties = %v", schema["properties"])
	}
}

// Korean text is 3 bytes per character; the old 2 KiB cap rejected ~680 chars.
func TestParseLeadSummaryKeepsLargeKoreanProseIntact(t *testing.T) {
	big := strings.Repeat("한국어 요약 문장입니다. ", 1200) // well over 4 KiB per field
	fields := map[string]string{}
	for _, f := range ProseFields() {
		fields[f.Key] = big + f.Key
	}
	raw, _ := json.Marshal(fields)
	if len(raw) <= MaxSummaryBytes {
		t.Fatalf("fixture must exceed the old %d byte total cap, got %d", MaxSummaryBytes, len(raw))
	}
	lead, err := ParseLeadSummary(raw)
	if err != nil {
		t.Fatalf("ParseLeadSummary: %v", err)
	}
	for i, f := range ProseFields() {
		if lead.Prose[i] != strings.TrimSpace(big+f.Key) || len(lead.Prose[i]) <= ProseFieldBytes*2 {
			t.Fatalf("%s truncated or altered: %d bytes", f.Key, len(lead.Prose[i]))
		}
	}
	checkpoint := BuildCheckpoint(CheckpointInput{Lead: true, Prose: lead.Prose})
	for i, f := range ProseFields() {
		if !strings.Contains(checkpoint, lead.Prose[i]) {
			t.Fatalf("checkpoint lost the full %s text", f.Key)
		}
	}
	if strings.Contains(checkpoint, "…") {
		t.Fatal("checkpoint truncated lead prose")
	}
}

func TestParseLeadSummaryLenientWarnings(t *testing.T) {
	paths := []string{"a", " ", "bad\npath", strings.Repeat("x", 1025), "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n"}
	rawPaths, _ := json.Marshal(append(paths, "o")) // 18 entries: 3 invalid, 15 valid
	raw := fmt.Sprintf(`{"user_preferences":"p","working_practices":null,"next_step":42,"mood_rapport":true,"bogus":1,"extra":2,"reread":%s}`, rawPaths)
	draft, warnings, err := ParseLeadSummaryDraft(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("lenient parse failed: %v", err)
	}
	lead := draft.Summary()
	want := map[string]string{"user_preferences": "p", "working_practices": "", "unrecorded_decisions": "", "current_work": "", "next_step": "42", "mood_rapport": "true", "residual_details": ""}
	for i, f := range ProseFields() {
		if lead.Prose[i] != want[f.Key] {
			t.Errorf("%s = %q, want %q", f.Key, lead.Prose[i], want[f.Key])
		}
	}
	if len(lead.Paths) != maxRereads || lead.Paths[0] != "a" || lead.Paths[1] != "b" {
		t.Fatalf("paths = %v", lead.Paths)
	}
	joined := strings.Join(warnings, "|")
	for _, sub := range []string{"working_practices is null", "next_step was not a string", "mood_rapport was not a string", "dropped 3 blank or invalid", "kept the first 12 of 15", "unknown key(s): bogus, extra"} {
		if !strings.Contains(joined, sub) {
			t.Errorf("warnings missing %q: %v", sub, warnings)
		}
	}
	if draft.Has[1] || !draft.Has[0] || !draft.Has[4] {
		t.Fatalf("Has = %v", draft.Has)
	}
}

func TestParseLeadSummaryEmptyObjectAndNonCoercible(t *testing.T) {
	if lead, err := ParseLeadSummary(json.RawMessage(`{}`)); err != nil || len(lead.Prose) != 7 || lead.Paths != nil {
		t.Fatalf("empty object: %+v %v", lead, err)
	}
	draft, _, err := ParseLeadSummaryDraft(json.RawMessage(`{"user_preferences":"keep","current_work":{"x":1},"reread":"notanarray"}`))
	if err == nil || !strings.Contains(err.Error(), "current_work must be a string") || !strings.Contains(err.Error(), "reread must be an array") {
		t.Fatalf("err = %v, want both problems", err)
	}
	if draft == nil || draft.Prose[0] != "keep" || !draft.Has[0] || draft.Has[3] {
		t.Fatalf("valid fields not preserved on error: %+v", draft)
	}
	for _, bad := range []string{`"str"`, `[1]`, `null`, `7`} {
		if d, _, err := ParseLeadSummaryDraft(json.RawMessage(bad)); err == nil || d != nil {
			t.Errorf("%s: draft=%v err=%v, want refusal", bad, d, err)
		}
	}
}

func TestLeadDraftOverlay(t *testing.T) {
	base, _, _ := ParseLeadSummaryDraft(json.RawMessage(`{"user_preferences":"old","next_step":"go","reread":["a.txt"]}`))
	retry, _, _ := ParseLeadSummaryDraft(json.RawMessage(`{"next_step":""}`))
	got := retry.Overlay(base).Summary()
	if got.Prose[0] != "old" || got.Prose[4] != "" || len(got.Paths) != 1 {
		t.Fatalf("overlay = %+v", got)
	}
	if base.Prose[4] != "go" {
		t.Fatal("overlay mutated its base")
	}
	cleared, _, _ := ParseLeadSummaryDraft(json.RawMessage(`{"reread":[]}`))
	if p := cleared.Overlay(base).Summary().Paths; len(p) != 0 {
		t.Fatalf("explicit empty reread must replace cached paths: %v", p)
	}
}

func TestParseLeadSummaryMemoryCeiling(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"current_work": strings.Repeat("a", MaxLeadSummaryBytes)})
	if _, err := ParseLeadSummary(raw); err == nil {
		t.Fatal("expected the memory ceiling to refuse")
	}
}
