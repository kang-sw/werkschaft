package wsaudit

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func intPtr(n int) *int { return &n }

func TestEnvelopeJSONRoundTrip(t *testing.T) {
	loc := time.FixedZone("KST", 9*3600)
	at := time.Date(2026, 11, 22, 17, 5, 6, 123456789, loc)

	orig := Envelope{
		V:         SchemaVersion,
		ID:        "evt-001",
		Seq:       42,
		Session:   "reply-lead-1",
		At:        at,
		Harness:   "claude",
		Kind:      KindToolResult,
		Subkind:   "Bash",
		Turn:      "turn-7",
		CallID:    "toolu_01abc",
		ParentID:  "toolu_01parent",
		AgentID:   "sub-1",
		ModelSeen: true,
		In:        `{"command":"go test ./..."}`,
		Out:       "PASS\n...tail...",
		Meta: map[string]any{
			"exit_code": float64(0),
			"is_error":  false,
		},
		Redactions: []Redaction{
			{Field: FieldOut, Offset: 10, Rule: "github_token", Length: 40},
		},
		Seen: &SeenBounds{
			Out: intPtr(1024),
		},
		StoredTrunc: &StoredTrunc{
			Out: &BodyTrunc{
				OriginalBytes: 500000,
				HeadBytes:     65536,
				TailBytes:     65536,
			},
		},
	}

	if err := orig.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	// Verify schema version, UTC nanosecond timestamp, and exact JSON key names.
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if wire["v"] != float64(SchemaVersion) {
		t.Fatalf("wire v = %#v, want %d", wire["v"], SchemaVersion)
	}
	wantAt := "2026-11-22T08:05:06.123456789Z"
	if wire["at"] != wantAt {
		t.Fatalf("wire at = %#v, want %q", wire["at"], wantAt)
	}
	if _, hasRaw := wire["raw"]; hasRaw {
		t.Fatalf("mapped kind must not emit raw field in JSON: %s", string(data))
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded.Validate() error: %v", err)
	}

	if decoded.V != SchemaVersion {
		t.Errorf("V = %d, want %d", decoded.V, SchemaVersion)
	}
	if decoded.ID != orig.ID || decoded.Seq != orig.Seq || decoded.Session != orig.Session {
		t.Errorf("identity mismatch: got (%q, %d, %q)", decoded.ID, decoded.Seq, decoded.Session)
	}
	if !decoded.At.Equal(orig.At) || decoded.At.Location() != time.UTC {
		t.Errorf("At = %v (%v), want UTC %v", decoded.At, decoded.At.Location(), orig.At.UTC())
	}
	if decoded.Harness != orig.Harness || decoded.Kind != orig.Kind || decoded.Subkind != orig.Subkind {
		t.Errorf("classification mismatch: got (%q, %q, %q)", decoded.Harness, decoded.Kind, decoded.Subkind)
	}
	if decoded.Turn != orig.Turn || decoded.CallID != orig.CallID || decoded.ParentID != orig.ParentID || decoded.AgentID != orig.AgentID {
		t.Errorf("linkage mismatch: got (%q, %q, %q, %q)", decoded.Turn, decoded.CallID, decoded.ParentID, decoded.AgentID)
	}
	if decoded.ModelSeen != orig.ModelSeen || decoded.In != orig.In || decoded.Out != orig.Out {
		t.Errorf("content mismatch: got model_seen=%v in=%q out=%q", decoded.ModelSeen, decoded.In, decoded.Out)
	}
	if len(decoded.Redactions) != 1 || decoded.Redactions[0] != orig.Redactions[0] {
		t.Errorf("redactions = %#v, want %#v", decoded.Redactions, orig.Redactions)
	}
	if decoded.Seen == nil || decoded.Seen.In != nil || decoded.Seen.Out == nil || *decoded.Seen.Out != 1024 {
		t.Errorf("seen = %#v, want out=1024", decoded.Seen)
	}
	if decoded.StoredTrunc == nil || decoded.StoredTrunc.Out == nil || *decoded.StoredTrunc.Out != *orig.StoredTrunc.Out {
		t.Errorf("stored_trunc = %#v, want %#v", decoded.StoredTrunc, orig.StoredTrunc)
	}
}

func TestEnvelopeMarshalDefaultsSchemaVersionAndEmitsFalseModelSeen(t *testing.T) {
	at := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	ev := Envelope{
		ID:        "evt-zero-v",
		Seq:       1,
		Session:   "s1",
		At:        at,
		Harness:   "codex",
		Kind:      KindToolCall,
		ModelSeen: false,
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"v":1`) {
		t.Fatalf("expected schema version in JSON, got %s", s)
	}
	if !strings.Contains(s, `"model_seen":false`) {
		t.Fatalf("expected explicit model_seen:false in JSON, got %s", s)
	}
	if !strings.Contains(s, `"at":"2026-11-22T08:00:00.000000000Z"`) {
		t.Fatalf("expected fixed nanosecond UTC timestamp in JSON, got %s", s)
	}
}

func TestEnvelopeSignatureOnlyThinkingAndZeroSeen(t *testing.T) {
	at := time.Date(2026, 11, 22, 8, 10, 0, 500, time.UTC)
	ev := Envelope{
		V:         SchemaVersion,
		ID:        "evt-think-sig",
		Seq:       2,
		Session:   "s1",
		At:        at,
		Harness:   "claude",
		Kind:      KindThinking,
		ModelSeen: true,
		Meta:      map[string]any{MetaSignatureOnly: true},
		Seen:      &SeenBounds{Out: intPtr(0)},
	}

	if err := ev.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if decoded.Out != "" {
		t.Errorf("Out = %q, want empty", decoded.Out)
	}
	if sig, _ := decoded.Meta[MetaSignatureOnly].(bool); !sig {
		t.Errorf("meta.signature_only = %#v, want true", decoded.Meta[MetaSignatureOnly])
	}
	if decoded.Seen == nil || decoded.Seen.Out == nil || *decoded.Seen.Out != 0 {
		t.Errorf("seen.out = %#v, want explicit 0", decoded.Seen)
	}
}

func TestOtherEnvelopeCarriesRaw(t *testing.T) {
	at := time.Date(2026, 11, 22, 8, 15, 0, 42, time.UTC)
	rawLine := `{"type":"mystery_future_event","payload":123}`

	ev := NewOtherEnvelope("s1", "pi", at, rawLine)
	ev.ID = "evt-other-1"
	ev.Seq = 9
	ev.StoredTrunc = &StoredTrunc{
		Raw: &BodyTrunc{OriginalBytes: 200000, HeadBytes: 4096, TailBytes: 4096},
	}
	ev.Redactions = []Redaction{
		{Field: FieldRaw, Offset: 5, Rule: "api_key", Length: 20},
	}

	if ev.V != SchemaVersion {
		t.Fatalf("V = %d, want %d", ev.V, SchemaVersion)
	}
	if ev.Kind != KindOther {
		t.Fatalf("Kind = %q, want %q", ev.Kind, KindOther)
	}
	if ev.Raw != rawLine {
		t.Fatalf("Raw = %q, want %q", ev.Raw, rawLine)
	}
	if !ev.ModelSeen {
		t.Fatalf("ModelSeen = false, want true")
	}
	if err := ev.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if decoded.Kind != KindOther || decoded.Raw != rawLine {
		t.Fatalf("decoded = (%q, %q), want (%q, %q)", decoded.Kind, decoded.Raw, KindOther, rawLine)
	}
}

func TestMappedKindNeverCarriesRaw(t *testing.T) {
	at := time.Date(2026, 11, 22, 8, 20, 0, 0, time.UTC)
	for _, kind := range AllKinds() {
		if kind == KindOther {
			continue
		}
		t.Run(string(kind)+"/raw_body", func(t *testing.T) {
			ev := Envelope{
				V:       SchemaVersion,
				ID:      "evt-bad",
				Seq:     1,
				Session: "s1",
				At:      at,
				Harness: "claude",
				Kind:    kind,
				Raw:     `{"raw":"forbidden"}`,
			}
			if err := ev.Validate(); !errors.Is(err, ErrMappedKindWithRaw) {
				t.Fatalf("Validate() = %v, want ErrMappedKindWithRaw", err)
			}
			if _, err := json.Marshal(ev); !errors.Is(err, ErrMappedKindWithRaw) {
				t.Fatalf("json.Marshal() = %v, want ErrMappedKindWithRaw", err)
			}
		})
		t.Run(string(kind)+"/stored_trunc_raw", func(t *testing.T) {
			ev := Envelope{
				V:       SchemaVersion,
				ID:      "evt-bad-trunc",
				Seq:     1,
				Session: "s1",
				At:      at,
				Harness: "claude",
				Kind:    kind,
				StoredTrunc: &StoredTrunc{
					Raw: &BodyTrunc{OriginalBytes: 100, HeadBytes: 10, TailBytes: 10},
				},
			}
			if err := ev.Validate(); !errors.Is(err, ErrMappedKindWithRaw) {
				t.Fatalf("Validate() = %v, want ErrMappedKindWithRaw", err)
			}
			if _, err := json.Marshal(ev); !errors.Is(err, ErrMappedKindWithRaw) {
				t.Fatalf("json.Marshal() = %v, want ErrMappedKindWithRaw", err)
			}
		})
		t.Run(string(kind)+"/redaction_raw", func(t *testing.T) {
			ev := Envelope{
				V:       SchemaVersion,
				ID:      "evt-bad-red",
				Seq:     1,
				Session: "s1",
				At:      at,
				Harness: "claude",
				Kind:    kind,
				Redactions: []Redaction{
					{Field: FieldRaw, Offset: 0, Rule: "token", Length: 12},
				},
			}
			if err := ev.Validate(); !errors.Is(err, ErrMappedKindWithRaw) {
				t.Fatalf("Validate() = %v, want ErrMappedKindWithRaw", err)
			}
			if _, err := json.Marshal(ev); !errors.Is(err, ErrMappedKindWithRaw) {
				t.Fatalf("json.Marshal() = %v, want ErrMappedKindWithRaw", err)
			}
		})
	}
}

func TestEnvelopeValidationErrors(t *testing.T) {
	at := time.Date(2026, 11, 22, 8, 25, 0, 0, time.UTC)
	base := func() Envelope {
		return Envelope{
			V:         SchemaVersion,
			ID:        "e1",
			Seq:       1,
			Session:   "s1",
			At:        at,
			Harness:   "claude",
			Kind:      KindToolCall,
			ModelSeen: true,
		}
	}

	cases := []struct {
		name    string
		mutate  func(*Envelope)
		wantErr error
	}{
		{
			name:    "missing_schema_version",
			mutate:  func(e *Envelope) { e.V = 0 },
			wantErr: ErrInvalidSchemaVersion,
		},
		{
			name:    "unsupported_future_schema_version",
			mutate:  func(e *Envelope) { e.V = SchemaVersion + 1 },
			wantErr: ErrUnsupportedSchemaVersion,
		},
		{
			name:    "invalid_kind",
			mutate:  func(e *Envelope) { e.Kind = Kind("bogus") },
			wantErr: ErrInvalidKind,
		},
		{
			name:    "missing_session",
			mutate:  func(e *Envelope) { e.Session = "" },
			wantErr: ErrInvalidEnvelope,
		},
		{
			name:    "missing_harness",
			mutate:  func(e *Envelope) { e.Harness = "" },
			wantErr: ErrInvalidEnvelope,
		},
		{
			name:    "zero_at",
			mutate:  func(e *Envelope) { e.At = time.Time{} },
			wantErr: ErrInvalidEnvelope,
		},
		{
			name:    "negative_seen",
			mutate:  func(e *Envelope) { e.Seen = &SeenBounds{In: intPtr(-1)} },
			wantErr: ErrInvalidEnvelope,
		},
		{
			name: "stored_trunc_not_actually_truncated",
			mutate: func(e *Envelope) {
				e.StoredTrunc = &StoredTrunc{Out: &BodyTrunc{OriginalBytes: 100, HeadBytes: 50, TailBytes: 50}}
			},
			wantErr: ErrInvalidEnvelope,
		},
		{
			name: "redaction_unknown_field",
			mutate: func(e *Envelope) {
				e.Redactions = []Redaction{{Field: "meta", Offset: 0, Rule: "r", Length: 4}}
			},
			wantErr: ErrInvalidEnvelope,
		},
		{
			name: "redaction_zero_length",
			mutate: func(e *Envelope) {
				e.Redactions = []Redaction{{Field: FieldIn, Offset: 0, Rule: "r", Length: 0}}
			},
			wantErr: ErrInvalidEnvelope,
		},
		{
			name: "redaction_negative_offset",
			mutate: func(e *Envelope) {
				e.Redactions = []Redaction{{Field: FieldIn, Offset: -1, Rule: "r", Length: 4}}
			},
			wantErr: ErrInvalidEnvelope,
		},
		{
			name: "redaction_empty_rule",
			mutate: func(e *Envelope) {
				e.Redactions = []Redaction{{Field: FieldIn, Offset: 0, Rule: "", Length: 4}}
			},
			wantErr: ErrInvalidEnvelope,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := base()
			tc.mutate(&ev)
			err := ev.Validate()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestEnvelopeUnmarshalEdgeCases(t *testing.T) {
	t.Run("non_utc_offset_normalizes_to_utc", func(t *testing.T) {
		raw := `{"v":1,"id":"e1","seq":1,"session":"s1","at":"2026-11-22T17:05:06.000000007+09:00","harness":"claude","kind":"hook","model_seen":true}`
		var ev Envelope
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatalf("Unmarshal error: %v", err)
		}
		if ev.At.Location() != time.UTC || ev.At.Hour() != 8 || ev.At.Nanosecond() != 7 {
			t.Fatalf("At = %v (%v), want 08:05:06.000000007 UTC", ev.At, ev.At.Location())
		}
		if err := ev.Validate(); err != nil {
			t.Fatalf("Validate() error: %v", err)
		}
	})

	t.Run("missing_and_future_schema_version_preserved_for_validation", func(t *testing.T) {
		var missingV Envelope
		if err := json.Unmarshal([]byte(`{"id":"e1","seq":1,"session":"s1","at":"2026-11-22T08:00:00Z","harness":"claude","kind":"hook","model_seen":true}`), &missingV); err != nil {
			t.Fatalf("Unmarshal missing v error: %v", err)
		}
		if err := missingV.Validate(); !errors.Is(err, ErrInvalidSchemaVersion) {
			t.Fatalf("missing v Validate() = %v, want ErrInvalidSchemaVersion", err)
		}

		var futureV Envelope
		if err := json.Unmarshal([]byte(`{"v":2,"id":"e1","seq":1,"session":"s1","at":"2026-11-22T08:00:00Z","harness":"claude","kind":"hook","model_seen":true}`), &futureV); err != nil {
			t.Fatalf("Unmarshal future v error: %v", err)
		}
		if futureV.V != 2 {
			t.Fatalf("futureV.V = %d, want 2", futureV.V)
		}
		if err := futureV.Validate(); !errors.Is(err, ErrUnsupportedSchemaVersion) {
			t.Fatalf("future v Validate() = %v, want ErrUnsupportedSchemaVersion", err)
		}
	})

	t.Run("invalid_timestamp_fails_unmarshal", func(t *testing.T) {
		var ev Envelope
		if err := json.Unmarshal([]byte(`{"v":1,"at":"not-a-time"}`), &ev); err == nil {
			t.Fatalf("expected error for invalid timestamp")
		}
	})

	t.Run("empty_seen_and_stored_trunc_omitted_on_marshal", func(t *testing.T) {
		ev, err := NewEnvelope("s1", "agy", time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC), KindSystem)
		if err != nil {
			t.Fatalf("NewEnvelope error: %v", err)
		}
		ev.Seen = &SeenBounds{}
		ev.StoredTrunc = &StoredTrunc{}
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("Marshal error: %v", err)
		}
		s := string(data)
		if strings.Contains(s, `"seen"`) || strings.Contains(s, `"stored_trunc"`) {
			t.Fatalf("expected empty seen and stored_trunc to be omitted, got %s", s)
		}
	})

	t.Run("new_envelope_rejects_invalid_kind", func(t *testing.T) {
		if _, err := NewEnvelope("s1", "claude", time.Now(), Kind("nope")); !errors.Is(err, ErrInvalidKind) {
			t.Fatalf("NewEnvelope invalid kind = %v, want ErrInvalidKind", err)
		}
	})
}

func TestKindSetCompleteness(t *testing.T) {
	want := []Kind{
		KindUser,
		KindAssistantText,
		KindThinking,
		KindToolCall,
		KindToolResult,
		KindHook,
		KindSystemReminder,
		KindSystem,
		KindCompaction,
		KindSubagent,
		KindOther,
	}
	got := AllKinds()
	if len(got) != len(want) {
		t.Fatalf("len(AllKinds()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllKinds()[%d] = %q, want %q", i, got[i], want[i])
		}
		if !got[i].Valid() {
			t.Errorf("Kind(%q).Valid() = false", got[i])
		}
	}
	if Kind("").Valid() || Kind("unknown").Valid() {
		t.Errorf("invalid kind reported Valid() == true")
	}
	if KindOther.IsMapped() {
		t.Errorf("KindOther.IsMapped() = true, want false")
	}
	if !KindToolCall.IsMapped() {
		t.Errorf("KindToolCall.IsMapped() = false, want true")
	}
}
