package wsagent

import (
	"encoding/json"
	"testing"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wslive"
)

// TestToolBytesPropagateAcrossAllHarnesses verifies that all 4 active
// harnesses (claude, codex, agy, werk) emit exact InBytes and OutBytes on
// finished batch calls matching their audit adapters, and that those bytes
// flow through wslive.State into ToolCallArgs, ToolBatch, and ToolSummary.
func TestToolBytesPropagateAcrossAllHarnesses(t *testing.T) {
	applyAndFlush := func(t *testing.T, events []Event) *wslive.ToolSummary {
		t.Helper()
		st := wslive.NewState("owner-key")
		var ring []wslive.RingEntry
		feed := func(evs []Event) {
			for _, ev := range evs {
				if ev.Live == nil {
					continue
				}
				res, err := st.Apply(*ev.Live, 1000)
				if err != nil {
					t.Fatalf("Apply %+v: %v", ev.Live, err)
				}
				ring = append(ring, res.Ring...)
			}
		}
		feed(events)
		feed([]Event{{Live: &wslive.Event{Kind: wslive.KindReply, Text: "done", Final: true}}})
		for _, r := range ring {
			if r.Kind == wslive.RingTools && r.Tools != nil {
				return r.Tools
			}
		}
		t.Fatalf("no RingTools entry produced from %d events", len(events))
		return nil
	}

	t.Run("claude", func(t *testing.T) {
		m := newClaudeMapper(StartSpec{})
		inRaw := json.RawMessage(`{"file_path":"/tmp/demo.go","limit":50}`)
		outText := "package demo\nfunc main() {}\n"
		var evs []Event
		evs = append(evs, m.assistant(json.RawMessage(`{"id":"msg-1","content":[{"type":"tool_use","id":"tu-1","name":"Read","input":`+string(inRaw)+`}]}`), 100, false)...)
		evs = append(evs, m.toolResults(json.RawMessage(`{"id":"u-1","content":[{"type":"tool_result","tool_use_id":"tu-1","content":`+mustToolBytesJSON(t, outText)+`}]}`), 250)...)
		ts := applyAndFlush(t, evs)
		wantIn := len(compactJSON(inRaw))
		wantOut := len(outText)
		if ts.InBytes != wantIn || ts.OutBytes != wantOut {
			t.Fatalf("claude ToolSummary bytes = (%d, %d), want (%d, %d)", ts.InBytes, ts.OutBytes, wantIn, wantOut)
		}
		c := ts.Batches[0].CallArgs[0]
		if c.InBytes != wantIn || c.OutBytes != wantOut || c.Failed {
			t.Fatalf("claude CallArgs[0] = %+v, want in=%d out=%d failed=false", c, wantIn, wantOut)
		}
	})

	t.Run("codex", func(t *testing.T) {
		m := newCodexMapper("s-codex")
		m.turnID = "turn-1"
		outStr := "hello world\nline 2\n"
		startItem := codexItem{ID: "cmd-1", Type: "commandExecution", Command: "echo hi", Cwd: "/repo"}
		endItem := codexItem{ID: "cmd-1", Type: "commandExecution", Command: "echo hi", Cwd: "/repo", Status: "completed", AggregatedOutput: &outStr}
		var evs []Event
		evs = append(evs, m.item("turn-1", startItem, false, 100)...)
		evs = append(evs, m.item("turn-1", endItem, true, 250)...)
		ts := applyAndFlush(t, evs)
		wantIn := len(codexToolInput(startItem.auditItem()))
		wantOut := len(codexToolOutput(endItem.auditItem()))
		if ts.InBytes != wantIn || ts.OutBytes != wantOut {
			t.Fatalf("codex ToolSummary bytes = (%d, %d), want (%d, %d)", ts.InBytes, ts.OutBytes, wantIn, wantOut)
		}
		c := ts.Batches[0].CallArgs[0]
		if c.InBytes != wantIn || c.OutBytes != wantOut {
			t.Fatalf("codex CallArgs[0] = %+v, want in=%d out=%d", c, wantIn, wantOut)
		}
	})

	t.Run("agy", func(t *testing.T) {
		m := newAgyMapper("s-agy")
		rawIn := json.RawMessage(`{"Path":"/repo/main.go"}`)
		rawOut := json.RawMessage(`{"content":"line1\nline2"}`)
		var evs []Event
		evs = append(evs, m.update(json.RawMessage(`{"update":{"sessionUpdate":"tool_call","toolCallId":"agy-1","name":"read_file","rawInput":`+string(rawIn)+`,"status":"in_progress"}}`), silentReplies{}, 100)...)
		evs = append(evs, m.update(json.RawMessage(`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"agy-1","name":"read_file","rawOutput":`+string(rawOut)+`,"status":"failed"}}`), silentReplies{}, 250)...)
		ts := applyAndFlush(t, evs)
		wantIn := len(agyAuditBody(nil, rawIn))
		wantOut := len(agyAuditBody(rawOut, nil))
		if ts.InBytes != wantIn || ts.OutBytes != wantOut {
			t.Fatalf("agy ToolSummary bytes = (%d, %d), want (%d, %d)", ts.InBytes, ts.OutBytes, wantIn, wantOut)
		}
		c := ts.Batches[0].CallArgs[0]
		if c.InBytes != wantIn || c.OutBytes != wantOut || !c.Failed {
			t.Fatalf("agy CallArgs[0] = %+v, want in=%d out=%d failed=true", c, wantIn, wantOut)
		}
	})

	t.Run("werk", func(t *testing.T) {
		m := newWerkMapper("s-werk")
		args := `{"command":"git status"}`
		output := "On branch develop\nnothing to commit\n"
		var evs []Event
		evs = append(evs, m.mapEvent(werk.Line{T: werk.LineToolStart, Call: "wk-1", Name: "bash", Args: args, At: 100})...)
		evs = append(evs, m.mapEvent(werk.Line{T: werk.LineToolEnd, Call: "wk-1", Name: "bash", Output: output, At: 250})...)
		ts := applyAndFlush(t, evs)
		if ts.InBytes != len(args) || ts.OutBytes != len(output) {
			t.Fatalf("werk ToolSummary bytes = (%d, %d), want (%d, %d)", ts.InBytes, ts.OutBytes, len(args), len(output))
		}
		c := ts.Batches[0].CallArgs[0]
		if c.InBytes != len(args) || c.OutBytes != len(output) || c.Failed {
			t.Fatalf("werk CallArgs[0] = %+v, want in=%d out=%d failed=false", c, len(args), len(output))
		}
	})
}

func mustToolBytesJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
