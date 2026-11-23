package wsagent

import (
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

func observedEvents(t *testing.T, events []Event) []*wslive.ModelObservation {
	t.Helper()
	var out []*wslive.ModelObservation
	for _, ev := range events {
		if ev.Live != nil && ev.Live.Observed != nil {
			out = append(out, ev.Live.Observed)
		}
	}
	return out
}

func observedClaudeLine(id, uuid, model, at, parent string) []byte {
	raw, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": uuid, "timestamp": at, "parent_tool_use_id": parent,
		"message": map[string]any{"id": id, "model": model, "content": []any{map[string]string{"type": "text", "text": "answer"}}},
	})
	return raw
}

func applyObservedEvents(t *testing.T, st *wslive.State, events []Event) {
	t.Helper()
	for _, ev := range events {
		if ev.Live != nil {
			if _, err := st.Apply(*ev.Live, 900000); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestHostedObservedClaudeResponseBlocksAndReplay(t *testing.T) {
	m := newClaudeMapper(StartSpec{Model: "opus", Effort: "max", Lead: &LeadSpec{}})
	m.now = func() time.Time { return time.UnixMilli(900000) }
	st := wslive.NewState("hosted-observed")
	tokens := int64(50)
	m.context = &wslive.UsageContext{Tokens: &tokens}
	m.window = 200000
	cost := 0.25
	m.cost = &wslive.UsageCost{USD: cost}
	first := observedClaudeLine("msg-one", "block-one", "claude-physical-one", "2026-11-15T01:02:03.456Z", "")
	events := m.mapLine(first, nopReplies{})
	observed := observedEvents(t, events)
	at := time.Date(2026, 11, 15, 1, 2, 3, 456000000, time.UTC).UnixMilli()
	want := wslive.ModelObservation{ID: "msg-one", Model: "claude-physical-one", Source: "claude.assistant", At: at}
	if len(observed) != 1 || !reflect.DeepEqual(*observed[0], want) {
		t.Fatalf("response evidence = %+v, want %+v", observed, want)
	}
	applyObservedEvents(t, st, events)
	usage := st.Snapshot(at).Usage
	if usage == nil || usage.Cost == nil || usage.Cost.USD != cost || usage.Context == nil {
		t.Fatalf("observation cleared existing Claude usage: %+v", usage)
	}
	// A later block has its own line identity/time but the same response.
	block := m.mapLine(observedClaudeLine("msg-one", "block-two", "claude-physical-one", "2026-11-15T01:02:04Z", ""), nopReplies{})
	if got := observedEvents(t, block); len(got) != 1 || !reflect.DeepEqual(*got[0], want) {
		t.Fatalf("split block moved original evidence: %+v", got)
	}
	applyObservedEvents(t, st, block)
	applyObservedEvents(t, st, m.mapLine(first, nopReplies{}))
	m.mapLine([]byte(`{"type":"system","subtype":"compact_boundary"}`), nopReplies{})
	if got := m.mapLine(first, nopReplies{}); len(got) != 0 {
		t.Fatalf("compaction replay escaped existing exclusion: %+v", got)
	}
	// A fresh holder mapper replays the original record under a new receipt clock.
	rejoined := newClaudeMapper(StartSpec{Lead: &LeadSpec{}})
	rejoined.now = func() time.Time { return time.UnixMilli(at + 600000) }
	applyObservedEvents(t, st, rejoined.mapLine(first, nopReplies{}))
	snap := st.Snapshot(at + 700000)
	if snap.Lead.Observed == nil || !reflect.DeepEqual(snap.Lead.Observed.Current, want) || len(snap.Lead.Observed.History) != 1 {
		t.Fatalf("repeat/replay changed evidence: %+v", snap.Lead.Observed)
	}
}

func TestHostedObservedClaudeUnknownAndChildExclusion(t *testing.T) {
	m := newClaudeMapper(StartSpec{Model: "opus", Effort: "high", Lead: &LeadSpec{}})
	for _, raw := range [][]byte{
		[]byte(`{"type":"system","subtype":"init","model":"opus"}`),
		observedClaudeLine("", "", "physical", "2026-11-15T01:02:03Z", ""),
		observedClaudeLine("no-time", "uuid", "physical", "", ""),
		observedClaudeLine("bad-time", "uuid", "physical", "not-a-time", ""),
		observedClaudeLine("no-model", "uuid", "", "2026-11-15T01:02:03Z", ""),
		observedClaudeLine("unmatched", "uuid", "other", "2026-11-15T01:02:03Z", "unmatched"),
	} {
		if got := observedEvents(t, m.mapLine(raw, nopReplies{})); len(got) != 0 {
			t.Fatalf("unsupported evidence accepted from %s: %+v", raw, got)
		}
	}
	m.subs["direct-call"] = &claudeSub{task: "direct-task"}
	st := wslive.NewState("independent")
	applyObservedEvents(t, st, []Event{{Live: &wslive.Event{Kind: wslive.KindAgent, AgentID: "direct-task", Status: wslive.StatusRunning, Model: "haiku", Effort: "high"}}})
	applyObservedEvents(t, st, m.mapLine(observedClaudeLine("own", "own-uuid", "lead-output", "2026-11-15T01:02:03Z", ""), nopReplies{}))
	applyObservedEvents(t, st, m.mapLine(observedClaudeLine("child", "child-uuid", "child-output", "2026-11-15T01:02:04Z", "direct-call"), nopReplies{}))
	if got := m.mapLine(observedClaudeLine("deep", "deep-uuid", "deep-output", "2026-11-15T01:02:05Z", "childs-call"), nopReplies{}); len(got) != 0 {
		t.Fatalf("unmatched/deep child was attributed: %+v", got)
	}
	snap := st.Snapshot(900000)
	if snap.Lead.Observed.Current.Model != "lead-output" || len(snap.Agents) != 1 || snap.Agents[0].Observed.Current.Model != "child-output" {
		t.Fatalf("mixed own/child evidence: %+v", snap)
	}
	if child := snap.Agents[0]; child.Effort != "high" || child.Observed.Current.Effort != "" || child.Observed.Current.EffortSource != "" || child.Observed.Current.OutputTps != nil {
		t.Fatalf("spawn effort/rate became response evidence: %+v", child)
	}
	// A worker's parent-tagged lines still cannot become its own evidence.
	worker := newClaudeMapper(StartSpec{})
	worker.subs["direct-call"] = &claudeSub{task: "direct-task"}
	if got := worker.mapLine(observedClaudeLine("child", "child-uuid", "other", "2026-11-15T01:02:03Z", "direct-call"), nopReplies{}); len(got) != 0 {
		t.Fatalf("worker accepted a parent-tagged response: %+v", got)
	}
	fallback := m.mapLine(observedClaudeLine("", "stable-uuid", "fallback-output", "2026-11-15T01:02:03Z", ""), nopReplies{})
	if got := observedEvents(t, fallback); len(got) != 1 || got[0].ID != "stable-uuid" {
		t.Fatalf("original UUID fallback missing: %+v", got)
	}
	if _, err := st.Apply(wslive.Event{Kind: wslive.KindAgent, Observed: &wslive.ModelObservation{ID: "rejected", Model: "bad", Source: "claude.assistant", At: 1}}, 900000); err == nil {
		t.Fatal("existing lead-agent rejection was lost")
	}
}

func observedConversation(t *testing.T, r *Runner, id string, parent *ParentRef) *conv {
	t.Helper()
	dir := wsdashboard.SessionDir(r.opts.Dir, id)
	return &conv{r: r, dir: dir, inbox: filepath.Join(dir, "inbox"), rec: Record{ReplyID: id, Model: "requested-" + id, Effort: "max", Parent: parent},
		live: newLiveWriter(dir, "key-"+id, "pi", r.opts.Now, io.Discard)}
}
