package wsagent

import (
	"encoding/json"
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
)

// Exercise the wire decoder and finished ring, not just the mapper's raw JSON:
// those are the batches the conversation renderer reads for its token chips.
func TestHostedBatchUsageFromAssistantMessages(t *testing.T) {
	for _, backend := range []string{BackendClaude} {
		t.Run(backend, func(t *testing.T) {
			st := wslive.NewState("lead")
			var ring []wslive.RingEntry
			apply := func(evs []Event) {
				for _, ev := range evs {
					if ev.Live == nil {
						continue
					}
					res, err := st.Apply(*ev.Live, 1000)
					if err != nil {
						t.Fatal(err)
					}
					ring = append(ring, res.Ring...)
				}
			}
			_, _ = st.Apply(wslive.Event{Kind: wslive.KindTurnStart}, 1)
			var batches *batchTracker
			if backend == BackendClaude {
				m := newClaudeMapper(StartSpec{})
				batches = &m.batches
				apply(m.assistant(json.RawMessage(`{"id":"a","content":[{"type":"tool_use","id":"c1","name":"Read","input":{}},{"type":"tool_use","id":"c2","name":"Grep","input":{}}],"usage":{"input_tokens":12,"output_tokens":4,"cache_read_input_tokens":30,"cache_creation_input_tokens":5}}`), 100, false))
				// A split line replaces counts, it must not sum them per call
				// or accidentally count the same message twice.
				update := json.RawMessage(`{"id":"a","content":[],"usage":{"input_tokens":12,"output_tokens":8,"cache_read_input_tokens":30,"cache_creation_input_tokens":5}}`)
				apply(m.assistant(update, 110, false))
				apply(m.assistant(update, 120, false))
			}
			want := wslive.TokenUsage{Input: 12, Output: 8, CacheRead: 30, CacheWrite: 5}
			if b := st.Snapshot(150).Lead.Batches; len(b) != 1 || b[0].Running == nil {
				t.Fatal("missing running batch")
			}
			apply(batches.endCall("c1", false, 0, 200))
			apply(batches.endCall("c2", false, 0, 300))
			apply([]Event{{Live: &wslive.Event{Kind: wslive.KindReply, Text: "done", Final: true}}})
			if len(ring) != 2 || ring[0].Tools == nil || len(ring[0].Tools.Batches) != 1 {
				t.Fatalf("finished batches: %+v", ring)
			}
			b := ring[0].Tools.Batches[0]
			if b.Usage == nil || *b.Usage != want || b.Calls != 2 {
				t.Fatalf("finished usage = %+v, calls = %d", b.Usage, b.Calls)
			}
			// A later batch with no usage stays absent, not the previous
			// message's usage or a fabricated zero-count chip.
			apply(batches.addCall("next", "c3", "read", "", 0, 400))
			apply(batches.endCall("c3", false, 0, 500))
			apply([]Event{{Live: &wslive.Event{Kind: wslive.KindReply, Text: "done again", Final: true}}})
			if b := ring[len(ring)-2].Tools.Batches[0]; b.Usage != nil {
				t.Fatalf("usage leaked between batches: %+v", b)
			}
		})
	}
}
