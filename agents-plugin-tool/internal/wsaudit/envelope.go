// Package wsaudit defines the harness-neutral audit event envelope, capture
// policy gate, model-seen context cut rule, and store sink interface.
package wsaudit

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	// SchemaVersion is the current wire schema version of Envelope.
	SchemaVersion = 1

	// TimestampLayout formats envelope timestamps as UTC RFC3339Nano with
	// fixed 9-digit nanosecond precision so timestamps preserve sub-second
	// ordering against display ring stamps.
	TimestampLayout = "2006-01-02T15:04:05.000000000Z"
)

// Body field names used in Redaction records.
const (
	FieldIn  = "in"
	FieldOut = "out"
	FieldRaw = "raw"
)

// Well-known Meta keys shared across adapters, storage, and query consumers.
const (
	// MetaSignatureOnly marks a thinking event delivered only as a signature
	// with an empty Out body.
	MetaSignatureOnly = "signature_only"
	// MetaEstimated marks an inferred compaction event used as the model-seen
	// cut when a harness does not emit an explicit compaction boundary.
	MetaEstimated = "estimated"
)

// Kind identifies the harness-neutral audit event category.
type Kind string

const (
	KindUser           Kind = "user"
	KindAssistantText  Kind = "assistant_text"
	KindThinking       Kind = "thinking"
	KindToolCall       Kind = "tool_call"
	KindToolResult     Kind = "tool_result"
	KindHook           Kind = "hook"
	KindSystemReminder Kind = "system_reminder"
	KindSystem         Kind = "system"
	KindCompaction     Kind = "compaction"
	KindSubagent       Kind = "subagent"
	KindOther          Kind = "other"
)

var allKinds = [...]Kind{
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

// AllKinds returns all defined audit event kinds in canonical order.
func AllKinds() []Kind {
	out := make([]Kind, len(allKinds))
	copy(out, allKinds[:])
	return out
}

// Valid reports whether k is a known audit event kind.
func (k Kind) Valid() bool {
	switch k {
	case KindUser,
		KindAssistantText,
		KindThinking,
		KindToolCall,
		KindToolResult,
		KindHook,
		KindSystemReminder,
		KindSystem,
		KindCompaction,
		KindSubagent,
		KindOther:
		return true
	default:
		return false
	}
}

// IsMapped reports whether k is a recognized, structured event kind rather than
// the unrecognized-line fallback KindOther.
func (k Kind) IsMapped() bool {
	return k.Valid() && k != KindOther
}

// InCaptureScope reports whether k belongs to the audit capture scope: tool
// calls, hidden harness events, and unrecognized-line other events. Ordinary
// visible conversation kinds (user, assistant_text) stay in the kind set but
// are outside the capture scope.
func (k Kind) InCaptureScope() bool {
	switch k {
	case KindThinking,
		KindToolCall,
		KindToolResult,
		KindHook,
		KindSystemReminder,
		KindSystem,
		KindCompaction,
		KindSubagent,
		KindOther:
		return true
	default:
		return false
	}
}

// Sentinel errors returned by Envelope validation and marshaling.
var (
	ErrInvalidSchemaVersion     = errors.New("wsaudit: invalid schema version")
	ErrUnsupportedSchemaVersion = errors.New("wsaudit: unsupported schema version")
	ErrInvalidKind              = errors.New("wsaudit: invalid event kind")
	ErrMappedKindWithRaw        = errors.New("wsaudit: mapped event kind must not carry raw data")
	ErrInvalidEnvelope          = errors.New("wsaudit: invalid envelope")
)

// Redaction records one secret match masked before persistence. It stores the
// field, byte offset in the redacted body before any per-body cap, rule name,
// and original secret byte length — never the secret text itself.
//
// A Count above 1 marks a summary entry the store wrote when the per-span
// entries overflowed its metadata bound: it stands for Count masked spans of
// one (field, rule) starting at Offset, and Length is their total original
// length.
type Redaction struct {
	Field  string `json:"field"`
	Offset int    `json:"offset"`
	Rule   string `json:"rule"`
	Length int    `json:"length"`
	Count  int    `json:"count,omitempty"`
}

// SeenBounds records how many bytes of a body the harness delivered to the
// model when that is less than the stored body. A nil SeenBounds or nil field
// means fully seen or unknown. Capture sets it in raw body bytes; the audit
// store maps it through redaction and its per-body cap, so a persisted bound
// is a byte offset in the stored body.
type SeenBounds struct {
	In  *int `json:"in,omitempty"`
	Out *int `json:"out,omitempty"`
}

// IsEmpty reports whether neither body has a recorded seen bound.
func (s *SeenBounds) IsEmpty() bool {
	return s == nil || (s.In == nil && s.Out == nil)
}

// BodyTrunc records how an oversized body was shortened to kept head and tail
// bytes by the store's per-body cap.
type BodyTrunc struct {
	OriginalBytes int `json:"original_bytes"`
	HeadBytes     int `json:"head_bytes"`
	TailBytes     int `json:"tail_bytes"`
}

// Validate checks that b describes a valid head-plus-tail truncation.
func (b BodyTrunc) Validate() error {
	if b.OriginalBytes <= 0 || b.HeadBytes < 0 || b.TailBytes < 0 ||
		b.TailBytes >= b.OriginalBytes || b.HeadBytes >= b.OriginalBytes-b.TailBytes {
		return fmt.Errorf("%w: invalid stored_trunc bounds (original=%d head=%d tail=%d)",
			ErrInvalidEnvelope, b.OriginalBytes, b.HeadBytes, b.TailBytes)
	}
	return nil
}

// StoredTrunc marks bodies that the store kept only as head plus tail under its
// per-body cap. Nil means every body is stored whole.
type StoredTrunc struct {
	In  *BodyTrunc `json:"in,omitempty"`
	Out *BodyTrunc `json:"out,omitempty"`
	Raw *BodyTrunc `json:"raw,omitempty"`
}

// IsEmpty reports whether no body carries a stored-truncation marker.
func (s *StoredTrunc) IsEmpty() bool {
	return s == nil || (s.In == nil && s.Out == nil && s.Raw == nil)
}

// Envelope is the single harness-neutral audit record produced by harness
// capture adapters, persisted by the audit ring store, and served by the query
// API. No harness-specific fields live outside Meta and Raw.
type Envelope struct {
	V           int            `json:"v"`
	ID          string         `json:"id"`
	Seq         uint64         `json:"seq"`
	Session     string         `json:"session"`
	At          time.Time      `json:"at"`
	Harness     string         `json:"harness"`
	Kind        Kind           `json:"kind"`
	Subkind     string         `json:"subkind,omitempty"`
	Turn        string         `json:"turn,omitempty"`
	CallID      string         `json:"call_id,omitempty"`
	ParentID    string         `json:"parent_id,omitempty"`
	AgentID     string         `json:"agent_id,omitempty"`
	ModelSeen   bool           `json:"model_seen"`
	In          string         `json:"in,omitempty"`
	Out         string         `json:"out,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
	Raw         string         `json:"raw,omitempty"`
	Redactions  []Redaction    `json:"redactions,omitempty"`
	Seen        *SeenBounds    `json:"seen,omitempty"`
	StoredTrunc *StoredTrunc   `json:"stored_trunc,omitempty"`
}

// NewEnvelope builds a versioned Envelope for a known kind with a UTC
// timestamp and ModelSeen initialized to true.
func NewEnvelope(session, harness string, at time.Time, kind Kind) (Envelope, error) {
	if !kind.Valid() {
		return Envelope{}, fmt.Errorf("%w: %q", ErrInvalidKind, kind)
	}
	return Envelope{
		V:         SchemaVersion,
		Session:   session,
		At:        at.UTC(),
		Harness:   harness,
		Kind:      kind,
		ModelSeen: true,
	}, nil
}

// NewOtherEnvelope builds a versioned KindOther Envelope for an unrecognized
// harness line, keeping the line verbatim in Raw.
func NewOtherEnvelope(session, harness string, at time.Time, rawLine string) Envelope {
	return Envelope{
		V:         SchemaVersion,
		Session:   session,
		At:        at.UTC(),
		Harness:   harness,
		Kind:      KindOther,
		ModelSeen: true,
		Raw:       rawLine,
	}
}

// Validate checks schema version, required metadata, kind invariants (including
// that mapped kinds never carry raw data), and bounds on Seen, StoredTrunc, and
// Redactions.
func (e Envelope) Validate() error {
	if e.V <= 0 {
		return fmt.Errorf("%w: got %d", ErrInvalidSchemaVersion, e.V)
	}
	if e.V != SchemaVersion {
		return fmt.Errorf("%w: got %d, supported %d", ErrUnsupportedSchemaVersion, e.V, SchemaVersion)
	}
	if !e.Kind.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidKind, e.Kind)
	}
	if e.Session == "" {
		return fmt.Errorf("%w: session is required", ErrInvalidEnvelope)
	}
	if e.Harness == "" {
		return fmt.Errorf("%w: harness is required", ErrInvalidEnvelope)
	}
	if e.At.IsZero() {
		return fmt.Errorf("%w: at timestamp is required", ErrInvalidEnvelope)
	}
	if err := checkNoRawOnMappedKind(e); err != nil {
		return err
	}
	if e.Seen != nil {
		if e.Seen.In != nil && *e.Seen.In < 0 {
			return fmt.Errorf("%w: seen.in must be non-negative", ErrInvalidEnvelope)
		}
		if e.Seen.Out != nil && *e.Seen.Out < 0 {
			return fmt.Errorf("%w: seen.out must be non-negative", ErrInvalidEnvelope)
		}
	}
	if e.StoredTrunc != nil {
		if e.StoredTrunc.In != nil {
			if err := e.StoredTrunc.In.Validate(); err != nil {
				return err
			}
		}
		if e.StoredTrunc.Out != nil {
			if err := e.StoredTrunc.Out.Validate(); err != nil {
				return err
			}
		}
		if e.StoredTrunc.Raw != nil {
			if err := e.StoredTrunc.Raw.Validate(); err != nil {
				return err
			}
		}
	}
	for i, r := range e.Redactions {
		switch r.Field {
		case FieldIn, FieldOut, FieldRaw:
		default:
			return fmt.Errorf("%w: redactions[%d] unknown field %q", ErrInvalidEnvelope, i, r.Field)
		}
		if r.Offset < 0 {
			return fmt.Errorf("%w: redactions[%d] negative offset %d", ErrInvalidEnvelope, i, r.Offset)
		}
		if r.Length <= 0 {
			return fmt.Errorf("%w: redactions[%d] non-positive length %d", ErrInvalidEnvelope, i, r.Length)
		}
		if r.Rule == "" {
			return fmt.Errorf("%w: redactions[%d] rule is required", ErrInvalidEnvelope, i)
		}
		if r.Count < 0 {
			return fmt.Errorf("%w: redactions[%d] negative count %d", ErrInvalidEnvelope, i, r.Count)
		}
	}
	return nil
}

func checkNoRawOnMappedKind(e Envelope) error {
	if e.Kind == KindOther {
		return nil
	}
	if e.Raw != "" {
		return fmt.Errorf("%w: kind %q carries raw body", ErrMappedKindWithRaw, e.Kind)
	}
	if e.StoredTrunc != nil && e.StoredTrunc.Raw != nil {
		return fmt.Errorf("%w: kind %q carries stored_trunc.raw", ErrMappedKindWithRaw, e.Kind)
	}
	for _, r := range e.Redactions {
		if r.Field == FieldRaw {
			return fmt.Errorf("%w: kind %q carries raw redaction", ErrMappedKindWithRaw, e.Kind)
		}
	}
	return nil
}

type wireEnvelope struct {
	V           int            `json:"v"`
	ID          string         `json:"id"`
	Seq         uint64         `json:"seq"`
	Session     string         `json:"session"`
	At          string         `json:"at"`
	Harness     string         `json:"harness"`
	Kind        Kind           `json:"kind"`
	Subkind     string         `json:"subkind,omitempty"`
	Turn        string         `json:"turn,omitempty"`
	CallID      string         `json:"call_id,omitempty"`
	ParentID    string         `json:"parent_id,omitempty"`
	AgentID     string         `json:"agent_id,omitempty"`
	ModelSeen   bool           `json:"model_seen"`
	In          string         `json:"in,omitempty"`
	Out         string         `json:"out,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
	Raw         string         `json:"raw,omitempty"`
	Redactions  []Redaction    `json:"redactions,omitempty"`
	Seen        *SeenBounds    `json:"seen,omitempty"`
	StoredTrunc *StoredTrunc   `json:"stored_trunc,omitempty"`
}

// MarshalJSON encodes e with schema version v (defaulting 0 to SchemaVersion),
// UTC nanosecond timestamp formatting, and strict enforcement that mapped kinds
// never carry raw data.
func (e Envelope) MarshalJSON() ([]byte, error) {
	if err := checkNoRawOnMappedKind(e); err != nil {
		return nil, err
	}
	v := e.V
	if v == 0 {
		v = SchemaVersion
	}
	var at string
	if !e.At.IsZero() {
		at = e.At.UTC().Format(TimestampLayout)
	}
	seen := e.Seen
	if seen.IsEmpty() {
		seen = nil
	}
	trunc := e.StoredTrunc
	if trunc.IsEmpty() {
		trunc = nil
	}
	return json.Marshal(wireEnvelope{
		V:           v,
		ID:          e.ID,
		Seq:         e.Seq,
		Session:     e.Session,
		At:          at,
		Harness:     e.Harness,
		Kind:        e.Kind,
		Subkind:     e.Subkind,
		Turn:        e.Turn,
		CallID:      e.CallID,
		ParentID:    e.ParentID,
		AgentID:     e.AgentID,
		ModelSeen:   e.ModelSeen,
		In:          e.In,
		Out:         e.Out,
		Meta:        e.Meta,
		Raw:         e.Raw,
		Redactions:  e.Redactions,
		Seen:        seen,
		StoredTrunc: trunc,
	})
}

// UnmarshalJSON decodes a wire envelope and normalizes At to UTC while
// preserving the decoded schema version V for caller validation.
func (e *Envelope) UnmarshalJSON(data []byte) error {
	var w wireEnvelope
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	var at time.Time
	if w.At != "" {
		parsed, err := time.Parse(time.RFC3339Nano, w.At)
		if err != nil {
			return fmt.Errorf("wsaudit: parse at %q: %w", w.At, err)
		}
		at = parsed.UTC()
	}
	*e = Envelope{
		V:           w.V,
		ID:          w.ID,
		Seq:         w.Seq,
		Session:     w.Session,
		At:          at,
		Harness:     w.Harness,
		Kind:        w.Kind,
		Subkind:     w.Subkind,
		Turn:        w.Turn,
		CallID:      w.CallID,
		ParentID:    w.ParentID,
		AgentID:     w.AgentID,
		ModelSeen:   w.ModelSeen,
		In:          w.In,
		Out:         w.Out,
		Meta:        w.Meta,
		Raw:         w.Raw,
		Redactions:  w.Redactions,
		Seen:        w.Seen,
		StoredTrunc: w.StoredTrunc,
	}
	return nil
}
