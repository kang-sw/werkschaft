package wswhiteboard

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// realisticBoard is a board of the size a long-lived session reaches: many
// topics, each with panels and a long entry stream carrying machine payloads.
func realisticBoard() *State {
	st := &State{Counter: 400, EntrySeq: 1200, PanelCounter: 60, Seq: 900, SettleSeq: 380, AnswerSeq: 880,
		CloseResults: map[string]string{}, Labels: map[string]string{}, LoadedAt: "2026-11-28T00:00:00.000Z"}
	for i := 0; i < 240; i++ {
		item := Item{ID: i + 1, Class: ClassPolicy, Topic: fmt.Sprintf("topic-%02d", i%24), Title: fmt.Sprintf("Decision %d about the board", i),
			Options: []string{"first option", "second option", "third option"}, Recommendation: "b: because", Rationale: strings.Repeat("rationale ", 12),
			Status: StatusOpen, Links: []string{"{{1}}", "{{2}}"}, Changed: i, ChangedAt: "2026-11-28T00:00:00Z"}
		if i%3 == 0 {
			item.Status, item.Answer, item.AnswerQuote, item.Settled = StatusAnswered, "b", "go with b", i
		}
		st.Items = append(st.Items, item)
	}
	body := strings.Repeat("A paragraph of topic body text that a lead keeps current.\n\n", 40)
	for i := 0; i < 24; i++ {
		name := fmt.Sprintf("topic-%02d", i)
		doc := TopicDoc{Name: name, Body: body, Changed: i, ChangedAt: "2026-11-28T00:00:00Z",
			Marks:         []BlockMark{{Block: 1, Kind: MarkMod}, {Block: 5, Kind: MarkNew}},
			BodyTimeProof: &BodyTimeProof{Version: 1, At: "2026-11-28T00:00:00Z", Changed: i, Digest: strings.Repeat("d", 64)},
			Tickets:       []string{"260101-feat-a", "260102-bug-b"}}
		for j := 0; j < 50; j++ {
			e := Entry{ID: fmt.Sprintf("e%d", i*50+j+1), Kind: "log", Text: strings.Repeat("log line text ", 10), At: "2026-11-28T00:00:00Z", Seq: j}
			switch j % 5 {
			case 0:
				e.Kind, e.PanelID, e.PanelName, e.Text = "panel", fmt.Sprintf("p%d", i), "main", strings.Repeat("<p>panel html</p>", 100)
				e.Mockup = &MockupMeta{Root: "index.html", SHA256: strings.Repeat("a", 64)}
			case 1:
				e.TopicState = json.RawMessage(`{"v":1,"op":"state","run":{"phase":"working","note":"in progress"}}`)
			case 2:
				e.BoardNotice = json.RawMessage(`{"v":1,"reply_id":"r1","reasons":["stale","answered"]}`)
				e.Inputs = json.RawMessage(`["i1","i2","i3"]`)
			case 3:
				e.unknown = map[string]json.RawMessage{"future": json.RawMessage(`{"a":[1,2,3]}`)}
			}
			doc.Entries = append(doc.Entries, e)
		}
		st.Topics = append(st.Topics, doc)
		st.Labels[name] = "Label " + name
		if i%4 == 0 {
			st.ClosedTopics = append(st.ClosedTopics, name)
			st.CloseResults[name] = "done"
		}
	}
	return st
}

// BenchmarkStateClone compares the structural deep copy with the JSON
// round-trip (plus the Entry fix-up pass) it replaced.
func BenchmarkStateClone(b *testing.B) {
	st := realisticBoard()
	b.Run("structural", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = st.clone()
		}
	})
	b.Run("json_roundtrip", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			data, err := json.Marshal(st)
			if err != nil {
				b.Fatal(err)
			}
			var out State
			if err := json.Unmarshal(data, &out); err != nil {
				b.Fatal(err)
			}
			for ti := range st.Topics {
				for j, e := range st.Topics[ti].Entries {
					out.Topics[ti].Entries[j] = e.clone()
				}
			}
		}
	})
}

func TestRealisticBoardCloneMatchesJSON(t *testing.T) {
	st := realisticBoard()
	a, _ := json.Marshal(st)
	b, _ := json.Marshal(st.clone())
	if string(a) != string(b) {
		t.Fatal("realistic board clone JSON differs")
	}
}
