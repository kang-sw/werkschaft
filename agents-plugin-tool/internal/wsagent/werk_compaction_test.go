package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsaudit"
)

const fixtureWorkerSummary = `{"user_preferences":"","working_practices":"","unrecorded_decisions":"","current_work":"carry forward","next_step":"","mood_rapport":"","residual_details":""}`

// These tests wait for durable writes and model callbacks on loaded runners.
const werkCompactionWait = 30 * time.Second

type unavailableCutSink struct{ *storeSink }

func (s unavailableCutSink) AppendCheckpoint(context.Context, wsaudit.SourceID, wsaudit.Envelope) error {
	return errors.New("injected checkpoint audit failure")
}

func TestWerkCompactionCommitsAtBoundaryAndRestoresAuditCut(t *testing.T) {
	for _, mode := range []string{"committed", "failed summary", "missing audit cut"} {
		t.Run(mode, func(t *testing.T) {
			started, allow := make(chan struct{}), make(chan struct{})
			requests := make(chan werk.Request, 1)
			summarize := func(ctx context.Context, req werk.Request) (*werk.Response, error) {
				requests <- req
				if mode == "failed summary" {
					return nil, errors.New("fixture failed summary")
				}
				return werkMessage(fixtureWorkerSummary)(ctx, req)
			}
			model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
				func(ctx context.Context, req werk.Request) (*werk.Response, error) {
					close(started)
					select {
					case <-allow:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					return werkMessage("original answer")(ctx, req)
				},
				summarize, werkMessage("after replacement"),
			}}
			h, fleet, reports := werkHarness(t, model)
			h.runner.Close()
			sink := openStoreSink(t, t.TempDir())
			h.opts.AuditSink = sink
			if mode == "missing audit cut" {
				h.opts.AuditSink = unavailableCutSink{sink}
			}
			fleet.start(h)
			id := spawnWerk(t, h, "read-only")
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("the first turn never reached the model")
			}
			key := werkRecord(t, h, id).WSKey
			resume := false
			if got := h.runner.Compact(key, "", &resume); got.Status != "queued" {
				t.Fatalf("compact = %+v", got)
			}
			select {
			case <-requests:
				t.Fatal("compaction ran inside the first turn")
			default:
			}
			close(allow)
			select {
			case req := <-requests:
				if len(req.Tools) != 0 || len(req.Input) != 1 || !strings.Contains(string(req.Input[0]), "original answer") {
					t.Fatalf("summary request did not use the settled dialog (%d tools, %d items)", len(req.Tools), len(req.Input))
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the summary never reached the model")
			}
			eventually(t, "the original report and compaction settlement", func() bool {
				st := h.state(id)
				if len(reports.list()) != 1 || st.Compacting {
					return false
				}
				switch mode {
				case "committed":
					return st.Compactions == 1
				case "failed summary":
					return strings.Contains(st.Error, "fixture failed summary")
				default:
					return !st.Process
				}
			})
			h.runner.Close()
			ctx := context.Background()
			if err := sink.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			events, err := sink.Events(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			before := wsaudit.FindModelSeenCut(events)
			if before.HasCompaction != (mode == "committed") {
				t.Fatalf("unexpected pre-replacement audit cut: %+v", before)
			}
			h.opts.AuditSink = sink
			fleet.start(h)
			if _, err := h.runner.Send(id, "follow-up"); err != nil {
				t.Fatal(err)
			}
			result, err := h.runner.Wait(ctx, id, werkCompactionWait)
			if err != nil || result.Status != TurnDone {
				t.Fatalf("waiting for the follow-up turn: %+v, %v", result, err)
			}
			// The turn's settlement wakes Wait before the report callback.
			h.runner.Close()
			if got := len(reports.list()); got != 2 {
				t.Fatalf("got %d reports after the follow-up, want 2", got)
			}
			model.mu.Lock()
			last := model.reqs[len(model.reqs)-1]
			model.mu.Unlock()
			if mode == "failed summary" {
				if len(last.Input) != 3 || strings.Contains(string(last.Input[0]), "Compacted worker conversation") {
					t.Fatal("failed compaction changed the retained input")
				}
			} else if len(last.Input) != 2 || !strings.Contains(string(last.Input[0]), "Compacted worker conversation") {
				t.Fatal("the successor did not first send the committed summary")
			}
			if err := sink.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			events, err = sink.Events(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			seen, after := wsaudit.ApplyModelSeenCut(events)
			var cuts int
			for _, ev := range seen {
				if ev.Kind == wsaudit.KindCompaction {
					cuts++
				}
			}
			if mode == "failed summary" {
				if cuts != 0 {
					t.Fatal("failed compaction published an audit cut")
				}
			} else {
				if cuts != 1 || !after.HasCompaction || !strings.Contains(seen[after.Index].Out, "carry forward") {
					t.Fatalf("audit restored %d cuts: %+v", cuts, after)
				}
				if before.HasCompaction && before.EventID != after.EventID {
					t.Fatalf("replacement changed audit cut identity: %+v to %+v", before, after)
				}
				if !slices.ContainsFunc(seen[:after.Index], func(e wsaudit.Envelope) bool {
					return e.Kind == wsaudit.KindSystem && e.Subkind == werk.LineUsage && !e.ModelSeen
				}) {
					t.Fatal("the original model response usage did not precede the cutoff")
				}
				if !slices.ContainsFunc(seen[after.Index+1:], func(e wsaudit.Envelope) bool {
					return e.Kind == wsaudit.KindSystem && e.Subkind == werk.LineUsage && e.ModelSeen
				}) {
					t.Fatal("the replacement model response usage did not follow the cutoff")
				}
			}
		})
	}
}

func TestWerkCompactionUsesCachedModelWindow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		window int
	}{
		{name: "advisory", window: 370},
		{name: "hard", window: 240},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lookups atomic.Int32
			picker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				lookups.Add(1)
				fmt.Fprintf(w, `{"data":[{"id":"vendor/some-model","context_length":%d}]}`, tc.window)
			}))
			t.Cleanup(picker.Close)
			model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
				werkMessage("first reply"),
			}}
			hard := tc.name == "hard"
			if hard {
				model.steps = append(model.steps, werkMessage(fixtureWorkerSummary))
			}
			model.steps = append(model.steps, werkMessage("second reply"))
			h, fleet, reports := werkHarness(t, model)
			h.runner.Close()
			reg := h.opts.Backends[BackendWerk]
			backend := reg.Backend.(werkFixtureBackend)
			backend.modelsURL = picker.URL
			if _, _, err := backend.Choices(context.Background()); err != nil {
				t.Fatal(err)
			}
			reg.Backend = backend
			h.opts.Backends[BackendWerk] = reg
			fleet.start(h)
			id := spawnWerk(t, h, "read-only")
			eventually(t, "first report", func() bool { return len(reports.list()) == 1 })
			if hard {
				eventually(t, "automatic compaction", func() bool { return h.state(id).Compactions == 1 })
				if got := model.requestCount(); got != 2 {
					t.Fatalf("hard threshold made %d requests before new input, want turn and summary", got)
				}
			} else if got := model.requestCount(); got != 1 {
				t.Fatalf("advisory made %d requests before new input, want one turn", got)
			}
			h.send(id, "second question")
			eventually(t, "second report", func() bool { return len(reports.list()) == 2 })
			model.mu.Lock()
			followUp := model.reqs[1]
			if hard {
				followUp = model.reqs[2]
			}
			model.mu.Unlock()
			if !strings.Contains(string(followUp.Input[len(followUp.Input)-1]), "second question") {
				t.Fatal("compaction discarded the next input")
			}
			if hard {
				if !strings.Contains(string(followUp.Input[0]), "Compacted worker conversation") {
					t.Fatal("hard-threshold checkpoint did not replace input")
				}
			} else if !strings.Contains(string(followUp.Input[len(followUp.Input)-1]), "advisory point") {
				t.Fatal("advisory was not included with the next real input")
			}
			if n := lookups.Load(); n != 1 {
				t.Fatalf("made %d model list requests, want one picker request", n)
			}
		})
	}
}

func TestWerkCompactionResumeUsesSeparateDurableTurn(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	continuation := make(chan werk.Request, 1)
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(ctx context.Context, req werk.Request) (*werk.Response, error) {
			close(started)
			select {
			case <-release:
				return werkMessage("first reply")(ctx, req)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		werkMessage(fixtureWorkerSummary),
		func(ctx context.Context, req werk.Request) (*werk.Response, error) {
			continuation <- req
			<-ctx.Done()
			return nil, ctx.Err()
		},
		werkMessage("resumed continuation"),
		werkMessage("answer to real input"),
	}}
	h, fleet, reports := werkHarness(t, model)
	id := spawnWerk(t, h, "read-only")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first turn did not start")
	}
	first := werkRecord(t, h, id)
	if first.DurableTurn == nil {
		t.Fatal("first turn has no durable identity")
	}
	resume := true
	if got := h.runner.Compact(first.WSKey, "", &resume); got.Status != "queued" {
		t.Fatalf("compact = %+v", got)
	}
	close(release)
	select {
	case req := <-continuation:
		if len(req.Input) != 2 || !strings.Contains(string(req.Input[0]), "carry forward") ||
			!strings.Contains(string(req.Input[1]), "Continue the work that was pending") {
			t.Fatal("the continuation did not use the summary and its own prompt")
		}
	case <-time.After(werkCompactionWait):
		t.Fatalf("compaction never opened its continuation (%d model requests)", model.requestCount())
	}
	second := werkRecord(t, h, id)
	if second.DurableTurn == nil || second.DurableTurn.ID == first.DurableTurn.ID || len(reports.list()) != 1 {
		t.Fatal("the continuation did not preserve the first report and journal a distinct turn")
	}
	h.runner.Close()
	fleet.start(h)
	eventually(t, "the resumed continuation report", func() bool { return len(reports.list()) == 2 })
	h.send(id, "a new input")
	eventually(t, "the later report", func() bool { return len(reports.list()) == 3 })
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.reqs) != 5 || !slices.EqualFunc(model.reqs[2].Input, model.reqs[3].Input, func(a, b json.RawMessage) bool {
		return bytes.Equal(a, b)
	}) || !strings.Contains(string(model.reqs[4].Input[len(model.reqs[4].Input)-1]), "a new input") {
		t.Fatal("replacement changed the continuation or took the later ordinary input")
	}
}
