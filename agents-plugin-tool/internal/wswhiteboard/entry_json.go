package wswhiteboard

import (
	"encoding/json"
	"maps"
	"slices"
)

// Preserve optional and future Entry members without making them model authority.
// Committed folder log lines are never reserialized; these methods also retain
// unknown members in newly read in-memory entries and explicit retrievals.
type entryJSON Entry

func (e *Entry) UnmarshalJSON(data []byte) error {
	var known entryJSON
	if err := json.Unmarshal(data, &known); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"id", "kind", "panel_id", "panel_name", "legacy_content", "item_id", "recap", "text", "at", "imported", "topic_state", "board_notice", "waiting_on", "unblocked_receipt", "inputs", "inputs_floor", "seq", "answer_sent_at", "mockup"} {
		delete(fields, key)
	}
	*e = Entry(known)
	if len(fields) != 0 {
		e.unknown = fields
	}
	return nil
}

func (e Entry) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(entryJSON(e))
	if err != nil || len(e.unknown) == 0 {
		return data, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for key, value := range e.unknown {
		if _, known := fields[key]; !known {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}

// clone is a zero-aliasing copy of e: every RawMessage payload, the Mockup
// pointer, and the unknown-member map are copied. Any new reference-typed
// Entry field must be copied here; TestStateCloneSharesNothing fails otherwise.
func (e Entry) clone() Entry {
	e.TopicState = cloneRaw(e.TopicState)
	e.BoardNotice = cloneRaw(e.BoardNotice)
	e.WaitingOn = cloneRaw(e.WaitingOn)
	e.UnblockedReceipt = cloneRaw(e.UnblockedReceipt)
	e.Inputs = cloneRaw(e.Inputs)
	if e.Mockup != nil {
		cp := *e.Mockup
		e.Mockup = &cp
	}
	if len(e.unknown) == 0 {
		e.unknown = nil
	} else {
		unknown := make(map[string]json.RawMessage, len(e.unknown))
		for key, value := range e.unknown {
			unknown[key] = cloneRaw(value)
		}
		e.unknown = unknown
	}
	return e
}

// The clone helpers normalize an empty collection or payload to nil. Every
// such State field is omitempty, so that is exactly what the JSON round-trip
// clone produced (and what a decoded board holds); callers and tests compare
// clones with reflect.DeepEqual against decoded states and rely on it.

// cloneRaw copies raw bytes; empty becomes nil.
func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

// cloneSlice copies s; empty becomes nil. Elements are copied by assignment,
// so it only serves value-typed elements.
func cloneSlice[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	return slices.Clone(s)
}

// cloneMap copies m; empty becomes nil.
func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	if len(m) == 0 {
		return nil
	}
	return maps.Clone(m)
}

// clone is a zero-aliasing copy of p, including its opaque raw evidence.
func (p *BodyTimeProof) clone() *BodyTimeProof {
	if p == nil {
		return nil
	}
	cp := *p
	cp.raw = cloneRaw(p.raw)
	return &cp
}

// clone is a zero-aliasing copy of d.
func (d TopicDoc) clone() TopicDoc {
	if len(d.Entries) == 0 {
		d.Entries = nil
	} else {
		entries := make([]Entry, len(d.Entries))
		for i := range d.Entries {
			entries[i] = d.Entries[i].clone()
		}
		d.Entries = entries
	}
	d.Marks = cloneSlice(d.Marks)
	d.BodyTimeProof = d.BodyTimeProof.clone()
	d.Tickets = cloneSlice(d.Tickets)
	return d
}

// clone is a zero-aliasing structural copy of s, replacing the former JSON
// round-trip. It matches that round-trip's in-memory result: empty omitempty
// collections come out nil, and the copy owns all its data (shared is false: a
// copy never aliases FolderCache's retained backing). Every reference-typed
// field is copied by hand; TestStateCloneSharesNothing reflects over a fully
// populated State to catch a field added without it.
func (s *State) clone() *State {
	if s == nil {
		return nil
	}
	out := *s
	out.shared = false
	if len(s.Items) == 0 {
		out.Items = nil
	} else {
		out.Items = make([]Item, len(s.Items))
		for i := range s.Items {
			item := s.Items[i]
			item.Options = cloneSlice(item.Options)
			item.Links = cloneSlice(item.Links)
			out.Items[i] = item
		}
	}
	out.ClosedTopics = cloneSlice(s.ClosedTopics)
	out.CloseResults = cloneMap(s.CloseResults)
	if len(s.Topics) == 0 {
		out.Topics = nil
	} else {
		out.Topics = make([]TopicDoc, len(s.Topics))
		for i := range s.Topics {
			out.Topics[i] = s.Topics[i].clone()
		}
	}
	out.Labels = cloneMap(s.Labels)
	return &out
}
