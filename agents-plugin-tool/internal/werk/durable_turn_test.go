package werk

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsfs"
)

func TestDurableTurnResumesModelRequestAndReusesFinishedResult(t *testing.T) {
	dir, session := t.TempDir(), NewSessionID()
	cmd := Command{Type: CmdUser, TurnID: "original-turn", Text: "one prompt"}
	modelCtx, replace := context.WithCancel(context.Background())
	defer replace()
	predecessor := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
		func(ctx context.Context, req Request, h StreamHandler) (*Response, error) {
			replace()
			return nil, ctx.Err()
		},
	}}
	var lines []Line
	open := func(model Responder, before func(json.RawMessage) error) *Agent {
		t.Helper()
		a, err := New(Config{Dir: dir, Session: session, Endpoint: model, Authority: AuthorityReadOnly,
			Emit: func(l Line) { lines = append(lines, l) }, BeforeAppend: before,
		})
		must(t, err)
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	first := open(predecessor, nil)
	must(t, first.runDurableTurn(modelCtx, modelCtx, cmd))
	must(t, first.Close())
	for _, l := range lines {
		if l.T == LineTurnEnd {
			t.Fatal("replacement published a turn end")
		}
	}

	ctx := context.Background()
	lines = nil
	secondModel := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){reply(msgItem("done"))}}
	second := open(secondModel, func(raw json.RawMessage) error {
		var row turnEndRecord
		if json.Unmarshal(raw, &row) == nil && row.K == "turn_end" {
			return errors.New("simulated failure before the final record")
		}
		return nil
	})
	if err := second.runDurableTurn(ctx, ctx, cmd); err == nil {
		t.Fatal("the failed finish was accepted")
	}
	must(t, second.Close())
	reqs := secondModel.requests()
	if len(reqs) != 1 || len(reqs[0].Input) != 1 || string(reqs[0].Input[0]) != string(userItem(cmd.Text)) {
		t.Fatal("the successor did not retry with exactly the original input")
	}
	for _, l := range lines {
		if l.T == LineTurnEnd || l.T == LineUser {
			t.Fatalf("the successor published %s while the final record failed", l.T)
		}
	}

	// The completed model response was already committed before the
	// final turn record failed. Finish from that response and then from
	// the final record itself, without calling the model in either case.
	for range 2 {
		lines = nil
		model := &scripted{}
		a := open(model, nil)
		must(t, a.runDurableTurn(ctx, ctx, cmd))
		must(t, a.Close())
		if len(model.requests()) != 0 || len(lines) != 3 {
			t.Fatalf("committed turn was re-run or emitted extra lines: %+v", lines)
		}
		if lines[0].T != LineTurnStart || lines[0].TurnID != cmd.TurnID ||
			lines[1].T != LineReply || lines[1].Text != "done" || !lines[1].Final ||
			lines[2].T != LineTurnEnd || lines[2].TurnID != cmd.TurnID || lines[2].Reason != ReasonDone {
			t.Fatalf("recovered turn = %+v", lines)
		}
	}
}

func TestDurableTurnRecoversPendingReadsAndFileEvidence(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	session := NewSessionID()
	cmd := Command{Type: CmdUser, TurnID: "original-turn", Text: "read and write"}
	store, _, err := OpenStore(dir, session, nil)
	must(t, err)
	t.Cleanup(func() { _ = store.Close() })
	must(t, store.appendBarrier(turnStartRecord{K: "turn_start", TurnID: cmd.TurnID, User: userItem(cmd.Text)}))
	items := []json.RawMessage{
		json.RawMessage(callOf("finished", "fs_read", `{"path":"already-read"}`)),
		json.RawMessage(callOf("missing-read", "fs_read", `{"path":"should-read"}`)),
	}
	for _, outcome := range []string{"applied", "not applied", "outcome unknown"} {
		id := strings.ReplaceAll(outcome, " ", "-")
		target := filepath.Join(dir, id)
		content := []byte("saved bytes")
		if outcome != "not applied" {
			current := content
			if outcome == "outcome unknown" {
				current = []byte("changed since the intent")
			}
			must(t, os.WriteFile(target, current, 0o600))
		}
		items = append(items, json.RawMessage(callOf(id, "fs_write", `{}`)))
	}
	must(t, store.AppendResponse(&Response{ID: "committed", Output: items}))
	must(t, store.Append(functionOutputItem("finished", "previous result")))
	for _, id := range []string{"applied", "not-applied", "outcome-unknown"} {
		must(t, store.appendBarrier(fileIntentLine{K: "file_intent", CallID: id, Intent: &wsfs.FileIntent{
			Target: filepath.Join(dir, id), Expected: fmt.Sprintf("%x", sha256.Sum256([]byte("saved bytes"))),
		}}))
	}
	must(t, store.Close())

	ws := &fakeWS{tools: []MCPTool{{Name: "fs_read"}, {Name: "fs_write"}}}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){reply(msgItem("done"))}}
	a, err := New(Config{Dir: dir, Session: session, Endpoint: model, Authority: AuthorityWorkdirWrite, WS: ws})
	must(t, err)
	defer a.Close()
	ctx := context.Background()
	must(t, a.runDurableTurn(ctx, ctx, cmd))
	if calls := ws.callNames(); !reflect.DeepEqual(calls, []string{"fs_read"}) {
		t.Fatalf("replacement called %v, want only the unfinished read", calls)
	}
	reqs := model.requests()
	if len(reqs) != 1 {
		t.Fatalf("replacement sent %d continuations, want one", len(reqs))
	}
	var outputs = map[string]string{}
	for _, raw := range reqs[0].Input {
		var item struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		}
		must(t, json.Unmarshal(raw, &item))
		if item.Type == "function_call_output" {
			outputs[item.CallID] = item.Output
		}
	}
	for id, expected := range map[string]string{
		"finished": "previous result", "missing-read": "ran fs_read",
		"applied":         "Recovered file mutation: applied.",
		"not-applied":     "Recovered file mutation: not applied.",
		"outcome-unknown": "Recovered file mutation: outcome unknown.",
	} {
		if !strings.HasPrefix(outputs[id], expected) {
			t.Fatalf("call %q recovered %q, want prefix %q", id, outputs[id], expected)
		}
	}
}

func TestDurableTurnRecoversAssociatedExecWithoutRelaunch(t *testing.T) {
	dir, session := t.TempDir(), NewSessionID()
	cmd := Command{Type: CmdUser, TurnID: "original-turn", Text: "run once"}
	store, _, err := OpenStore(dir, session, nil)
	must(t, err)
	t.Cleanup(func() { _ = store.Close() })
	must(t, store.appendBarrier(turnStartRecord{K: "turn_start", TurnID: cmd.TurnID, User: userItem(cmd.Text)}))
	items := []json.RawMessage{json.RawMessage(callOf("inspect", "exec_tail", `{"exec_id":"job-running"}`))}
	for _, id := range []string{"running", "exited", "lost", "unbound"} {
		items = append(items, json.RawMessage(callOf(id, "exec_run", `{"exe":"printf"}`)))
	}
	must(t, store.AppendResponse(&Response{ID: "committed", Output: items}))
	for _, id := range []string{"running", "exited", "lost"} {
		must(t, store.appendBarrier(execJobLine{K: "exec_job", CallID: id, JobID: "job-" + id}))
	}
	must(t, store.Close())

	var lookedUp, finished []string
	ws := &fakeWS{tools: []MCPTool{{Name: "exec.run"}, {Name: "exec.tail"}}}
	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){reply(msgItem("done"))}}
	a, err := New(Config{Dir: dir, Session: session, Endpoint: model, Authority: AuthorityWorkdirWrite, WS: ws,
		RecoverExec: func(_ context.Context, id string) (string, error) {
			lookedUp = append(lookedUp, id)
			switch id {
			case "job-running":
				return "exec_id: job-running\nstatus: running", nil
			case "job-exited":
				return "exec_id: job-exited\nstatus: exited", nil
			default:
				return "", errors.New("job unavailable")
			}
		},
		Emit: func(line Line) {
			if line.T == LineTurnEnd {
				finished = append(finished, line.TurnID)
			}
		},
	})
	must(t, err)
	defer a.Close()
	ctx := context.Background()
	must(t, a.runDurableTurn(ctx, ctx, cmd))
	if calls := ws.callNames(); !reflect.DeepEqual(calls, []string{"exec.tail"}) {
		t.Fatalf("replacement called %v, want only the unfinished read", calls)
	}
	if !reflect.DeepEqual(lookedUp, []string{"job-running", "job-exited", "job-lost"}) {
		t.Fatalf("looked up jobs %v", lookedUp)
	}
	if !reflect.DeepEqual(finished, []string{cmd.TurnID}) {
		t.Fatalf("finished turns %v", finished)
	}
	reqs := model.requests()
	if len(reqs) != 1 {
		t.Fatalf("replacement sent %d continuations, want one", len(reqs))
	}
	outputs := map[string]string{}
	for _, raw := range reqs[0].Input {
		var item struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		}
		must(t, json.Unmarshal(raw, &item))
		if item.Type == "function_call_output" {
			outputs[item.CallID] = item.Output
		}
	}
	for id, expected := range map[string]string{
		"inspect": "ran exec.tail",
		"running": "exec_id: job-running\nstatus: running",
		"exited":  "exec_id: job-exited\nstatus: exited",
		"lost":    "outcome is unknown",
		"unbound": "outcome is unknown",
	} {
		if !strings.Contains(outputs[id], expected) {
			t.Fatalf("call %q recovered %q, want %q", id, outputs[id], expected)
		}
	}
}

func TestDurableSteerReceiptsReconcilePartialLogCommit(t *testing.T) {
	dir, session := t.TempDir(), NewSessionID()
	ctx := context.Background()
	cmd := Command{Type: CmdUser, TurnID: "original-turn", Text: "start"}
	steers := []Steer{{ID: "first-steer", Text: "same text"}, {ID: "second-steer", Text: "same text"}}
	var receipts []string
	var ended bool
	open := func(model Responder, appendHook func(json.RawMessage) error) *Agent {
		t.Helper()
		a, err := New(Config{
			Dir: dir, Session: session, Endpoint: model, Authority: AuthorityReadOnly,
			BeforeAppend: appendHook,
			Emit: func(l Line) {
				if l.T == LineUserTaken {
					if l.TurnID != cmd.TurnID {
						t.Errorf("steer receipt had turn %q", l.TurnID)
					}
					receipts = append(receipts, l.ID)
				}
				if l.T == LineTurnEnd {
					ended = true
				}
			},
		})
		must(t, err)
		t.Cleanup(func() { _ = a.Close() })
		a.steers = append([]Steer(nil), steers...)
		return a
	}
	firstModel := &scripted{}
	writeFailure := errors.New("simulated failure committing a steer")
	first := open(firstModel, func(raw json.RawMessage) error {
		var row turnSteerRecord
		if json.Unmarshal(raw, &row) == nil && row.K == "turn_steer" && row.ID == steers[1].ID {
			return writeFailure
		}
		return nil
	})
	if err := first.runDurableTurn(ctx, ctx, cmd); !errors.Is(err, writeFailure) {
		t.Fatalf("steer commit failed with %v", err)
	}
	must(t, first.Close())
	if len(firstModel.requests()) != 0 || ended || !reflect.DeepEqual(receipts, []string{steers[0].ID}) {
		t.Fatalf("partially committed steering yielded receipts %v and ended=%v", receipts, ended)
	}

	receipts = nil
	changed := open(&scripted{}, nil)
	changed.steers[0].Text = "different text for the same identity"
	if err := changed.runDurableTurn(ctx, ctx, cmd); err == nil || !strings.Contains(err.Error(), "input changed") {
		t.Fatalf("the changed steer returned %v", err)
	}
	must(t, changed.Close())
	if len(receipts) != 0 || ended {
		t.Fatal("the changed steer received an acknowledgement or settled the turn")
	}

	model := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){reply(msgItem("done"))}}
	next := open(model, nil)
	must(t, next.runDurableTurn(ctx, ctx, cmd))
	must(t, next.Close())
	if !ended || !reflect.DeepEqual(receipts, []string{steers[0].ID, steers[1].ID}) {
		t.Fatalf("recovery yielded receipts %v and ended=%v", receipts, ended)
	}
	reqs := model.requests()
	if len(reqs) != 1 || len(reqs[0].Input) != 3 {
		t.Fatalf("recovery duplicated or lost a steer: %v", reqs)
	}
	for _, item := range reqs[0].Input[1:] {
		if string(item) != string(userItem("same text")) {
			t.Fatalf("recovered steer = %s", item)
		}
	}
}
