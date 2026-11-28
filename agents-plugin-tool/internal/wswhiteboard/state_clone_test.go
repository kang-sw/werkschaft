package wswhiteboard

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fullCloneState populates every field of State and of every type reachable
// from it, including the unexported ones (State.shared excepted, which a
// dedicated test covers because a clone deliberately clears it). It builds a
// fresh value each call so a test can compare a mutated original against a
// pristine twin.
func fullCloneState() *State {
	return &State{
		Counter:      9,
		EntrySeq:     8,
		PanelCounter: 7,
		Seq:          6,
		SettleSeq:    5,
		AnswerSeq:    4,
		Items: []Item{
			{ID: 1, Class: ClassPolicy, Topic: "alpha", Title: "t1", Options: []string{"a", "b"}, Recommendation: "a", Rationale: "r",
				Status: StatusAnswered, Answer: "a", AnswerQuote: "q", AnsweredAt: "2026-11-28T00:00:00Z", Links: []string{"{{2}}", "{{3}}"},
				Changed: 3, ChangedAt: "2026-11-28T00:00:01Z", Settled: 2, ReaskedAt: "2026-11-28T00:00:02.000Z", Asked: 1},
			{ID: 2, Class: ClassCheck, Topic: "beta", Title: "t2", Status: StatusOpen, Options: []string{"ok"}, Links: []string{"x"}},
		},
		ClosedTopics: []string{"alpha", "gamma"},
		CloseResults: map[string]string{"alpha": "done", "gamma": "dropped"},
		Topics: []TopicDoc{
			{
				Name: "alpha", Body: "body", Changed: 3, ChangedAt: "2026-11-28T00:00:03Z",
				Marks:         []BlockMark{{Block: 0, Kind: MarkNew}, {Block: 2, Kind: MarkMod}},
				BodyTimeProof: &BodyTimeProof{Version: 1, At: "2026-11-28T00:00:03Z", Changed: 3, Digest: "abc"},
				Recap:         "recap", RecapChanged: 2, RecapAt: "2026-11-28T00:00:04Z",
				Tickets: []string{"260101-feat-a", "260102-bug-b"},
				Entries: []Entry{
					{
						ID: "e1", Kind: "log", Text: "hello", At: "2026-11-28T00:00:05Z",
						TopicState:       json.RawMessage(`{"v":1,"op":"set"}`),
						BoardNotice:      json.RawMessage(`{"v":1}`),
						WaitingOn:        json.RawMessage(`{"v":1,"targets":["a"]}`),
						UnblockedReceipt: json.RawMessage(`{"v":1}`),
						Inputs:           json.RawMessage(`["i1","i2"]`),
						InputsFloor:      "f", Seq: 11, AnswerSentAt: 12345,
						unknown: map[string]json.RawMessage{"future": json.RawMessage(`{"n":[1,2,3]}`), "other": json.RawMessage(`"s"`)},
					},
					{
						ID: "e2", Kind: "panel", PanelID: "p1", PanelName: "main", LegacyContent: true, ItemID: 1, Recap: "r", Text: "<p>x</p>",
						At: "2026-11-28T00:00:06Z", Imported: true,
						Mockup: &MockupMeta{Root: "index.html", SHA256: "deadbeef"},
					},
				},
			},
			{
				Name: "beta", Body: "b2",
				// Opaque evidence carried through round-trips verbatim.
				BodyTimeProof: &BodyTimeProof{Version: 2, raw: json.RawMessage(`{"v":2,"future":true}`)},
			},
		},
		Labels:             map[string]string{"alpha": "Alpha", "beta": "Beta"},
		LastNudgeAt:        "2026-11-28T00:00:07Z",
		MetadataRevision:   13,
		StaleNudgeRevision: "rev",
		LoadedAt:           "2026-11-28T00:00:08.000Z",
	}
}

// mutateCloneEverywhere writes through every reference in c, so an aliased
// original shows the change.
func mutateCloneEverywhere(c *State) {
	c.Counter++
	c.Items[0].Options[0] = "MUT"
	c.Items[0].Links[0] = "MUT"
	c.Items[1].Options[0] = "MUT"
	c.Items[1].Links[0] = "MUT"
	c.Items[0].Title = "MUT"
	c.Items = append(c.Items, Item{ID: 99})
	c.ClosedTopics[0] = "MUT"
	c.CloseResults["alpha"] = "MUT"
	c.CloseResults["new"] = "MUT"
	c.Labels["alpha"] = "MUT"
	delete(c.Labels, "beta")
	for i := range c.Topics {
		d := &c.Topics[i]
		d.Body = "MUT"
		if len(d.Marks) > 0 {
			d.Marks[0].Kind = "MUT"
		}
		if len(d.Tickets) > 0 {
			d.Tickets[0] = "MUT"
		}
		p := d.BodyTimeProof
		p.Digest = "MUT"
		p.At = "MUT"
		if p.raw != nil {
			p.raw[0] = 'X'
		}
		for j := range d.Entries {
			e := &d.Entries[j]
			e.Text = "MUT"
			for _, raw := range []json.RawMessage{e.TopicState, e.BoardNotice, e.WaitingOn, e.UnblockedReceipt, e.Inputs} {
				for k := range raw {
					raw[k] = 'X'
				}
			}
			if e.Mockup != nil {
				e.Mockup.Root, e.Mockup.SHA256 = "MUT", "MUT"
			}
			for key, raw := range e.unknown {
				for k := range raw {
					raw[k] = 'X'
				}
				e.unknown[key+"MUT"] = raw
			}
			delete(e.unknown, "other")
		}
		d.Entries = append(d.Entries, Entry{ID: "MUT"})
		d.Marks = append(d.Marks, BlockMark{Block: 77})
	}
}

func TestStateCloneEquivalenceAndIsolation(t *testing.T) {
	orig := fullCloneState()
	got := orig.clone()

	origJSON, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(origJSON) != string(gotJSON) {
		t.Fatalf("clone JSON differs:\n orig %s\n got  %s", origJSON, gotJSON)
	}
	if !reflect.DeepEqual(orig, got) {
		t.Fatalf("clone not DeepEqual to original")
	}
	// The JSON round-trip the clone replaced must agree on the wire form.
	var viaJSON State
	if err := json.Unmarshal(origJSON, &viaJSON); err != nil {
		t.Fatal(err)
	}
	if viaJSONBytes, _ := json.Marshal(&viaJSON); string(viaJSONBytes) != string(gotJSON) {
		t.Fatalf("clone JSON differs from JSON round-trip:\n via %s\n got %s", viaJSONBytes, gotJSON)
	}
	if !reflect.DeepEqual(&viaJSON, got) {
		t.Fatalf("clone differs in memory from the JSON round-trip it replaced")
	}

	mutateCloneEverywhere(got)
	if !reflect.DeepEqual(orig, fullCloneState()) {
		a, _ := json.Marshal(orig)
		b, _ := json.Marshal(fullCloneState())
		t.Fatalf("mutating the clone changed the original:\n now  %s\n want %s", a, b)
	}

	// And the reverse: mutating the original leaves an earlier clone alone.
	again := fullCloneState()
	cp := again.clone()
	mutateCloneEverywhere(again)
	if !reflect.DeepEqual(cp, fullCloneState()) {
		t.Fatalf("mutating the original changed the clone")
	}
}

func TestStateCloneNilAndEmptySemantics(t *testing.T) {
	if (*State)(nil).clone() != nil {
		t.Fatal("nil State must clone to nil")
	}
	zero := &State{}
	if got := zero.clone(); !reflect.DeepEqual(got, zero) {
		t.Fatalf("zero State clone = %#v", got)
	}
	empty := &State{
		Items:        []Item{{ID: 1, Options: []string{}, Links: nil}},
		ClosedTopics: []string{},
		CloseResults: map[string]string{},
		Topics: []TopicDoc{{
			Name: "t", Entries: []Entry{}, Marks: []BlockMark{}, Tickets: []string{},
		}, {
			Name:    "u",
			Entries: []Entry{{ID: "e1", TopicState: json.RawMessage{}, Inputs: nil, unknown: map[string]json.RawMessage{}}},
		}},
		Labels: map[string]string{},
	}
	// Every one of these fields is omitempty, so the JSON round-trip the clone
	// replaced turned each empty value into nil; callers compare clones with
	// DeepEqual against decoded states and depend on that.
	got := empty.clone()
	ga, _ := json.Marshal(got)
	wa, _ := json.Marshal(empty)
	if string(ga) != string(wa) {
		t.Fatalf("JSON differs: %s vs %s", ga, wa)
	}
	var decoded State
	if err := json.Unmarshal(wa, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, &decoded) {
		t.Fatalf("empty shape differs from the JSON round-trip:\n got  %#v\n want %#v", got, &decoded)
	}
	if got.Items[0].Options != nil || got.ClosedTopics != nil || got.CloseResults != nil || got.Labels != nil ||
		got.Topics[0].Entries != nil || got.Topics[0].Marks != nil || got.Topics[0].Tickets != nil ||
		got.Topics[1].Entries[0].TopicState != nil || got.Topics[1].Entries[0].unknown != nil {
		t.Fatal("empty collections must clone to nil")
	}
}

func TestStateCloneClearsShared(t *testing.T) {
	orig := fullCloneState()
	orig.shared = true
	got := orig.clone()
	if got.shared {
		t.Fatal("a clone owns its data and must not be marked shared")
	}
	got.shared = true
	if !reflect.DeepEqual(orig, got) {
		t.Fatal("clone differs beyond shared")
	}
}

// TestStateCloneSharesNothing guards clone against fields added later. It
// walks a fully populated State and its clone side by side and fails on any
// non-nil pointer, non-empty slice, or non-nil map the two share, and on any
// reference-typed field that the fixture leaves empty (which would let a new
// field slip through unchecked).
func TestStateCloneSharesNothing(t *testing.T) {
	orig := fullCloneState()
	got := orig.clone()

	populated := map[string]bool{}
	var shared []string
	var walk func(path string, o, c reflect.Value)
	walk = func(path string, o, c reflect.Value) {
		switch o.Kind() {
		case reflect.Pointer:
			if o.IsNil() {
				return
			}
			if o.Pointer() == c.Pointer() {
				shared = append(shared, path+" (pointer)")
				return
			}
			walk(path, o.Elem(), c.Elem())
		case reflect.Slice:
			if o.IsNil() || o.Len() == 0 {
				return
			}
			if o.Pointer() == c.Pointer() {
				shared = append(shared, path+" (slice)")
				return
			}
			for i := 0; i < o.Len(); i++ {
				walk(fmt.Sprintf("%s[]", path), o.Index(i), c.Index(i))
			}
		case reflect.Map:
			if o.IsNil() {
				return
			}
			if o.Pointer() == c.Pointer() {
				shared = append(shared, path+" (map)")
				return
			}
			for _, key := range o.MapKeys() {
				walk(path+"{}", o.MapIndex(key), c.MapIndex(key))
			}
		case reflect.Struct:
			for i := 0; i < o.NumField(); i++ {
				field := o.Type().Field(i)
				fpath := path + "." + field.Name
				switch field.Type.Kind() {
				case reflect.Pointer, reflect.Slice, reflect.Map:
					if !o.Field(i).IsZero() && !(field.Type.Kind() == reflect.Slice && o.Field(i).Len() == 0) {
						populated[o.Type().Name()+"."+field.Name] = true
					}
				}
				walk(fpath, o.Field(i), c.Field(i))
			}
		case reflect.Interface:
			if !o.IsNil() {
				shared = append(shared, path+" (interface: unsupported by the guard; handle it in clone)")
			}
		case reflect.Array:
			for i := 0; i < o.Len(); i++ {
				walk(fmt.Sprintf("%s[]", path), o.Index(i), c.Index(i))
			}
		}
	}
	walk("State", reflect.ValueOf(orig).Elem(), reflect.ValueOf(got).Elem())
	if len(shared) != 0 {
		t.Fatalf("clone aliases the original at:\n  %s", strings.Join(shared, "\n  "))
	}

	// Every reference-typed field of every reachable struct must be populated
	// by fullCloneState, so the walk above actually exercised it.
	seen := map[reflect.Type]bool{}
	var missing []string
	var types func(rt reflect.Type)
	types = func(rt reflect.Type) {
		switch rt.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			types(rt.Elem())
		case reflect.Struct:
			if seen[rt] {
				return
			}
			seen[rt] = true
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				switch f.Type.Kind() {
				case reflect.Pointer, reflect.Slice, reflect.Map:
					if !populated[rt.Name()+"."+f.Name] {
						missing = append(missing, rt.Name()+"."+f.Name)
					}
				}
				types(f.Type)
			}
		}
	}
	types(reflect.TypeOf(orig))
	if len(missing) != 0 {
		t.Fatalf("fullCloneState leaves reference fields unpopulated, so the guard cannot see them: %s", strings.Join(missing, ", "))
	}
}
