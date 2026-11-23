package werk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscompact"
)

func compactProseReply(value string) string {
	fields := make(map[string]string, len(wscompact.ProseFields()))
	for _, field := range wscompact.ProseFields() {
		fields[field.Key] = value
	}
	raw, _ := json.Marshal(fields)
	return msgItem(string(raw))
}

func TestCheckpointSelectsOnlySummaryAndDeltasAfterReopen(t *testing.T) {
	ctx := context.Background()
	dir, session := t.TempDir(), NewSessionID()
	turn := Command{Type: CmdUser, TurnID: "first", Text: "Keep the original user request."}
	ticket := "261122-feat-ws-builtin-agent-harness"
	ws := &fakeWS{tools: []MCPTool{{Name: "playbook.read"}}, byName: map[string]func(context.Context, json.RawMessage) (CallResult, error){
		"playbook.read": func(context.Context, json.RawMessage) (CallResult, error) {
			return CallResult{Text: "TOOL-RESULT-SENTINEL"}, nil
		},
	}}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		reply(callOf("book", "playbook_read", `{"name":"implement","ticket_stem":"`+ticket+`","phase":4}`)),
		reply(msgItem("The first turn finished.")),
		reply(compactProseReply("Review the worker's changes.")),
	}}
	var lines []Line
	cfg := Config{Dir: dir, Session: session, Model: "fixture", Authority: AuthorityReadOnly, Endpoint: model, WS: ws,
		Emit: func(line Line) { lines = append(lines, line) }}
	a, err := New(cfg)
	must(t, err)
	t.Cleanup(func() { _ = a.Close() })
	must(t, a.runDurableTurn(ctx, ctx, turn))
	a.cfg.CompactionState = func(time.Time) CompactionState {
		return CompactionState{
			ChildrenInFlight: []string{"in-flight child"}, ChildrenFinished: []string{"finished child"}, ExecJobs: []string{"running job"},
		}
	}
	a.reportCompaction(ctx, nil)
	cp := lines[len(lines)-1]
	if cp.T != LineCheckpoint || !ValidSessionID(cp.ID) || cp.Replayed {
		t.Fatalf("unexpected checkpoint event: %+v", cp)
	}
	for _, want := range []string{ticket, "phase 4", "Active playbook: implement", turn.Text, "The first turn finished.",
		"in-flight child", "finished child", "running job", "werk/" + session + ".jsonl"} {
		if !strings.Contains(cp.Text, want) {
			t.Errorf("checkpoint missing %q", want)
		}
	}
	for _, field := range wscompact.ProseFields() {
		if !strings.Contains(cp.Text, "## "+field.Heading+"\nReview the worker's changes.") {
			t.Errorf("checkpoint missing prose for %q", field.Heading)
		}
	}
	requests := model.requests()
	summary := requests[len(requests)-1]
	if len(summary.Tools) != 0 || len(summary.Input) != 1 || bytes.Contains(summary.Input[0], []byte("TOOL-RESULT-SENTINEL")) {
		t.Fatal("summary call exposed tools or tool results")
	}
	if strings.Contains(cp.Text, "TOOL-RESULT-SENTINEL") {
		t.Fatal("tool result escaped the raw archive")
	}
	must(t, a.Close())
	raw, err := os.ReadFile(segmentPath(dir, session))
	must(t, err)
	if !bytes.Contains(raw, []byte("TOOL-RESULT-SENTINEL")) {
		t.Fatal("compaction lost the raw tool result")
	}

	nextModel := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){reply(msgItem("New answer."))}}
	cfg.Endpoint = nextModel
	lines = nil
	b, err := New(cfg)
	must(t, err)
	t.Cleanup(func() { _ = b.Close() })
	if selected := b.Transcript(); len(selected) != 1 || !bytes.Equal(selected[0], userItem(cp.Text)) {
		t.Fatalf("checkpoint reopened with %d items", len(selected))
	}
	// Reconciliation with a runner that resubmits the last finished turn
	// must neither call the model nor reopen the archived raw input.
	cmd := make(chan Command, 2)
	cmd <- turn
	cmd <- Command{Type: CmdUser, TurnID: "second", Text: "Follow up."}
	done := make(chan error, 1)
	events := make(chan Line, 32)
	b.out.emit = func(line Line) { events <- line }
	host, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { done <- b.ServeCommands(host, cmd) }()
	select {
	case replay := <-events:
		if replay.ID != cp.ID || replay.At != cp.At || replay.Text != cp.Text || !replay.Replayed {
			t.Fatalf("checkpoint replay changed: %+v", replay)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("checkpoint replay did not arrive")
	}
	end := nextTypedTurnEnd(t, events)
	if end.TurnID != turn.TurnID {
		t.Fatalf("reconciliation ended %q", end.TurnID)
	}
	end = nextTypedTurnEnd(t, events)
	if end.TurnID != "second" || end.Turn != 2 {
		t.Fatalf("second turn identity changed: %+v", end)
	}
	close(cmd)
	must(t, <-done)
	requests = nextModel.requests()
	if len(requests) != 1 || len(requests[0].Input) != 2 || !bytes.Equal(requests[0].Input[0], userItem(cp.Text)) ||
		!bytes.Equal(requests[0].Input[1], userItem("Follow up.")) {
		t.Fatal("model received more than the checkpoint and new input")
	}
	must(t, b.Close())

	cfg.Endpoint = &scripted{}
	c, err := New(cfg)
	must(t, err)
	defer c.Close()
	selected := c.Transcript()
	if len(selected) != 3 || !bytes.Equal(selected[2], json.RawMessage(msgItem("New answer."))) {
		t.Fatalf("replay did not select the checkpoint and the post-checkpoint delta: %s", selected)
	}
}

func TestLeadCheckpointUsesFreshStateAndBoundedEvidence(t *testing.T) {
	ctx := context.Background()
	dir, root, session := t.TempDir(), t.TempDir(), NewSessionID()
	const newest = "NEWEST-DIALOG"
	store, _, err := OpenStore(dir, session, nil)
	must(t, err)
	must(t, store.Append(json.RawMessage(callOf("book", "playbook_read", `{"name":"implement","ticket_stem":"261122-feat-fixture","phase":2}`))))
	must(t, store.Append(userItem("OLD-DIALOG"+strings.Repeat("a", dialogBytes/2))))
	must(t, store.Append(userItem(strings.Repeat("b", dialogBytes/2))))
	must(t, store.Append(userItem(newest)))
	must(t, store.Append(json.RawMessage(`{"type":"function_call_output","call_id":"book","output":"PRIVATE-TOOL-RESULT"}`)))
	must(t, store.Close())

	must(t, os.WriteFile(filepath.Join(root, "small.txt"), []byte("small file text"), 0o600))
	must(t, os.WriteFile(filepath.Join(root, "too-large.txt"), []byte(strings.Repeat("x", rereadBytes)), 0o600))
	endpoint := &scripted{}
	var cp Line
	var states []time.Time
	cfg := Config{Dir: dir, Root: root, Cwd: root, Session: session, Lead: true, Model: "fixture",
		Authority: AuthorityReadOnly, Endpoint: endpoint, Emit: func(line Line) { cp = line },
		CompactionState: func(since time.Time) CompactionState {
			states = append(states, since)
			if since.IsZero() {
				return CompactionState{ChildrenInFlight: []string{"old child"}, ExecJobs: []string{"old job"}}
			}
			return CompactionState{ChildrenFinished: []string{"finished child"}, ExecJobs: []string{"current job"}}
		},
	}
	a, err := New(cfg)
	must(t, err)
	t.Cleanup(func() { _ = a.Close() })
	fields := map[string]any{"reread": []string{"too-large.txt", "small.txt", "missing.txt"}}
	for _, field := range wscompact.ProseFields() {
		fields[field.Key] = "lead prose for " + field.Key
	}
	raw, err := json.Marshal(fields)
	must(t, err)
	lead, err := wscompact.ParseLeadSummary(raw)
	must(t, err)
	a.reportCompaction(ctx, lead)
	first := cp
	a.reportCompaction(ctx, lead)
	if first.T != LineCheckpoint || cp.T != LineCheckpoint || first.ID == cp.ID ||
		len(states) != 2 || !states[0].IsZero() || states[1].UnixMilli() != first.At {
		t.Fatal("compaction did not pass the previous checkpoint boundary")
	}
	for _, field := range wscompact.ProseFields() {
		if !strings.Contains(cp.Text, "## "+field.Heading+"\nlead prose for "+field.Key) {
			t.Errorf("missing lead prose for %s", field.Key)
		}
	}
	for _, want := range []string{"Compacted lead conversation", "261122-feat-fixture, phase 2", "Active playbook: implement",
		"finished child", "current job", newest, "werk/" + session + ".jsonl",
		"<file path=\"small.txt\">\nsmall file text\n</file>", "- too-large.txt\n- missing.txt"} {
		if !strings.Contains(cp.Text, want) {
			t.Errorf("checkpoint did not contain %q", want)
		}
	}
	for _, stale := range []string{"OLD-DIALOG", "PRIVATE-TOOL-RESULT", "old child", "old job", strings.Repeat("x", 64)} {
		if strings.Contains(cp.Text, stale) {
			t.Errorf("checkpoint retained %q", stale)
		}
	}
	_, dialog, ok := strings.Cut(cp.Text, "## Dialog\n")
	_, dialog, okPath := strings.Cut(dialog, "\n")
	if !ok || !okPath || len(dialog) > dialogBytes || len(endpoint.requests()) != 0 {
		t.Fatal("supplied prose used an extra request or overflowed the dialog budget")
	}
	must(t, a.Close())
	raw, err = os.ReadFile(segmentPath(dir, session))
	must(t, err)
	if !bytes.Contains(raw, []byte("PRIVATE-TOOL-RESULT")) || !bytes.Contains(raw, []byte("OLD-DIALOG")) {
		t.Fatal("compaction discarded raw evidence")
	}
	b, err := New(cfg)
	must(t, err)
	defer b.Close()
	if selected := b.Transcript(); len(selected) != 1 || !bytes.Equal(selected[0], userItem(cp.Text)) {
		t.Fatal("reopen did not select just the latest lead checkpoint")
	}
}

func TestCheckpointFailureLeavesInputIntact(t *testing.T) {
	for _, mode := range []string{"endpoint", "malformed", "append", "active turn"} {
		t.Run(mode, func(t *testing.T) {
			dir, session := t.TempDir(), NewSessionID()
			store, _, err := OpenStore(dir, session, nil)
			must(t, err)
			original := userItem("Original transcript.")
			if mode == "active turn" {
				must(t, store.appendBarrier(turnStartRecord{K: "turn_start", TurnID: "active", User: original}))
			} else {
				must(t, store.Append(original))
			}
			must(t, store.Close())
			response := reply(compactProseReply("new summary"))
			switch mode {
			case "endpoint":
				response = failWith(errors.New("unavailable"))
			case "malformed":
				response = reply(msgItem(`{"next_step":"missing fields"}`))
			}
			model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){response}}
			var last Line
			a, err := New(Config{Dir: dir, Session: session, Authority: AuthorityReadOnly, Endpoint: model,
				Emit: func(line Line) { last = line }, BeforeAppend: func(raw json.RawMessage) error {
					if mode == "append" && bytes.Contains(raw, []byte(`"k":"checkpoint"`)) {
						return errors.New("injected append error")
					}
					return nil
				}})
			must(t, err)
			t.Cleanup(func() { _ = a.Close() })
			a.reportCompaction(context.Background(), nil)
			if last.T != LineCompactFailed {
				t.Fatalf("expected a failed compaction event: %+v", last)
			}
			if mode == "active turn" && len(model.requests()) != 0 {
				t.Fatal("an active turn started a summary")
			}
			check := func(items []json.RawMessage) {
				t.Helper()
				if len(items) != 1 || !bytes.Equal(items[0], original) {
					t.Fatal("failed checkpoint replaced input")
				}
			}
			check(a.Transcript())
			must(t, a.Close())
			store, items, err := OpenStore(dir, session, nil)
			must(t, err)
			defer store.Close()
			check(items)
			if store.checkpoint != nil {
				t.Fatal("failed checkpoint was observable on replay")
			}
		})
	}
}
