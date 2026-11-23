package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsfs"
)

type werkFixtureRoundTripFunc func(*http.Request) (*http.Response, error)

func (f werkFixtureRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func werkFixtureReply(text string) (*http.Response, error) {
	return werkReplacementResponse(map[string]any{"type": "message", "id": "msg_1", "role": "assistant",
		"content": []map[string]string{{"type": "output_text", "text": text}}})
}

type werkFixtureRequest struct {
	Input []json.RawMessage
	Tools []struct{ Name string }
}

func readWerkFixtureRequest(req *http.Request) (werkFixtureRequest, error) {
	defer req.Body.Close()
	var model werkFixtureRequest
	err := json.NewDecoder(req.Body).Decode(&model)
	return model, err
}

func awaitWerkFixture[T any](t *testing.T, c <-chan T, what string) T {
	t.Helper()
	select {
	case result := <-c:
		return result
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func (f *werkDaemonFixture) activeLog(id string) ([]byte, string) {
	f.t.Helper()
	rec, err := wsagent.ReadRecord(f.state, id)
	if err != nil {
		f.t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(wsdashboard.SessionDir(f.state, id), "werk", rec.SessionID+".jsonl"))
	if err != nil {
		f.t.Fatal(err)
	}
	var turnID string
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		var row struct {
			Kind   string `json:"k"`
			TurnID string `json:"turn_id"`
		}
		if json.Unmarshal(line, &row) == nil && row.Kind == "turn_start" {
			turnID = row.TurnID
		}
	}
	if turnID == "" {
		f.t.Fatal("the interrupted request had no durable turn identity")
	}
	return raw, turnID
}

func (f *werkDaemonFixture) checkReport(next werkDaemonOpening, id, turnID, answer string) {
	f.t.Helper()
	report := awaitWerkFixture(f.t, f.reports, "the resumed report")
	if report.Result.ID != id || report.Result.Message != answer {
		f.t.Fatal("the resumed turn reported a different result")
	}
	next.daemon.Close()
	rec, err := wsagent.ReadRecord(f.state, id)
	if err != nil || rec.LastReportedTurn != turnID || len(f.reports) != 0 {
		f.t.Fatalf("the report did not settle once under the original turn identity: %v", err)
	}
}

func TestWerkResendsInterruptedModelRequestAcrossDaemonReplacement(t *testing.T) {
	f := newWerkDaemonFixture(t)
	inputs := make(chan werkFixtureRequest, 4)
	var requests atomic.Int32
	installWerkFixtureTransport(t, func(wrapped http.RoundTripper) http.RoundTripper {
		return werkFixtureRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Hostname() != "openrouter.ai" {
				return wrapped.RoundTrip(req)
			}
			model, err := readWerkFixtureRequest(req)
			if err != nil {
				return nil, err
			}
			n := requests.Add(1)
			inputs <- model
			if n == 1 {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}
			return werkFixtureReply("the original request resumed")
		})
	})
	old := f.open(nil)
	id := f.spawn(old, "finish the original work", "read-only")
	first := awaitWerkFixture(t, inputs, "the first provider request")
	_, turnID := f.activeLog(id)
	old.daemon.Close()
	if err := old.jobs.Close(); err != nil {
		t.Fatal(err)
	}
	if len(f.reports) != 0 {
		t.Fatal("replacement settled the interrupted request")
	}
	next := f.open(nil)
	resumed := awaitWerkFixture(t, inputs, "the resent provider request")
	if len(first.Input) != 1 || !strings.Contains(string(first.Input[0]), "finish the original work") ||
		!reflect.DeepEqual(first, resumed) {
		t.Fatal("replacement changed the input or tools of the unfinished request")
	}
	f.checkReport(next, id, turnID, "the original request resumed")
	if n := requests.Load(); n != 2 {
		t.Fatalf("provider received %d requests, want exactly one resend", n)
	}
}

func TestWerkRecoversInterruptedFileWritesAcrossDaemonReplacement(t *testing.T) {
	for _, tc := range []struct {
		name, prior, current, outcome string
		newPath                       bool
		stopAfterIntent               bool
	}{
		{name: "prior", prior: "old bytes", current: "old bytes", outcome: "not applied", stopAfterIntent: true},
		{name: "expected", prior: "old bytes", current: "new bytes", outcome: "applied"},
		{name: "created", current: "new bytes", outcome: "applied", newPath: true},
		{name: "changed", prior: "old bytes", current: "external bytes", outcome: "outcome unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWerkDaemonFixture(t)
			const answer = "the file outcome was recovered"
			path := "fixture.txt"
			if tc.newPath {
				path = "new/fixture.txt"
			}
			target := filepath.Join(f.root, path)
			if !tc.newPath {
				if err := os.WriteFile(target, []byte(tc.prior), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args, err := json.Marshal(map[string]string{"path": path, "content": "new bytes"})
			if err != nil {
				t.Fatal(err)
			}
			var modelCalls, fileCalls, recorded atomic.Int32
			continuation := make(chan []json.RawMessage, 1)
			fileReply := make(chan []byte, 1)
			installWerkFixtureTransport(t, func(wrapped http.RoundTripper) http.RoundTripper {
				return werkFixtureRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					if req.URL.Hostname() == "openrouter.ai" {
						model, err := readWerkFixtureRequest(req)
						if err != nil {
							return nil, err
						}
						if modelCalls.Add(1) != 1 {
							select {
							case continuation <- model.Input:
							default:
							}
							return werkFixtureReply(answer)
						}
						for _, tool := range model.Tools {
							if tool.Name == "fs_write" {
								return werkReplacementResponse(map[string]any{"type": "function_call", "id": "fc_1", "call_id": "write-1",
									"name": tool.Name, "arguments": string(args)})
							}
						}
						return nil, errors.New("the authenticated worker did not receive fs_write")
					}
					if req.URL.Path != wsdashboard.ProxyHTTPPath || req.GetBody == nil {
						return wrapped.RoundTrip(req)
					}
					body, err := req.GetBody()
					if err != nil {
						return nil, err
					}
					var call struct {
						Method string
						Params struct{ Name string }
					}
					err = json.NewDecoder(body).Decode(&call)
					_ = body.Close()
					if err != nil {
						return nil, err
					}
					if call.Method != "tools/call" || call.Params.Name != "fs.write" {
						return wrapped.RoundTrip(req)
					}
					if fileCalls.Add(1) != 1 {
						return nil, errors.New("the fixture write was repeated")
					}
					res, err := wrapped.RoundTrip(req)
					if err != nil {
						return nil, err
					}
					raw, err := io.ReadAll(res.Body)
					_ = res.Body.Close()
					if err != nil {
						return nil, err
					}
					fileReply <- raw
					// Drop the in-flight result before its append barrier.
					<-req.Context().Done()
					return nil, req.Context().Err()
				})
			})
			old := f.open(func(ctx context.Context, call wsfs.FileCall, record func(context.Context, wsfs.FileCall) error) error {
				if err := record(ctx, call); err != nil {
					return err
				}
				recorded.Add(1)
				if tc.stopAfterIntent {
					return errors.New("fixture interruption after durable intent")
				}
				return nil
			})
			id := f.spawn(old, "write the fixture file", "workdir-write")
			reply := awaitWerkFixture(t, fileReply, "the withheld fs.write reply")
			if recorded.Load() != 1 || bytes.Contains(reply, []byte(`"isError":true`)) != tc.stopAfterIntent {
				t.Fatal("the authenticated write did not stop at the selected boundary")
			}
			raw, turnID := f.activeLog(id)
			if !bytes.Contains(raw, []byte(`"k":"file_intent"`)) || bytes.Contains(raw, []byte("function_call_output")) {
				t.Fatal("the interrupted turn did not retain exactly the pending intent")
			}
			if tc.outcome == "outcome unknown" {
				if err := os.WriteFile(target, []byte(tc.current), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			old.daemon.Close()
			if err := old.jobs.Close(); err != nil {
				t.Fatal(err)
			}
			next := f.open(nil)
			resumed := awaitWerkFixture(t, continuation, "the original turn's tool recovery")
			want := fmt.Sprintf("Recovered file mutation: %s. The tool was not run again.", tc.outcome)
			var outputs int
			for _, raw := range resumed {
				var item struct {
					Type   string
					CallID string `json:"call_id"`
					Output string
				}
				if json.Unmarshal(raw, &item) == nil && item.Type == "function_call_output" &&
					item.CallID == "write-1" && item.Output == want {
					outputs++
				}
			}
			if outputs != 1 {
				t.Fatal("the resumed request did not report the selected outcome once")
			}
			f.checkReport(next, id, turnID, answer)
			onDisk, err := os.ReadFile(target)
			if err != nil || string(onDisk) != tc.current || fileCalls.Load() != 1 || modelCalls.Load() != 2 {
				t.Fatalf("recovery changed the file or repeated the model/tool call: %v", err)
			}
		})
	}
}

func TestWerkDurablePanicFailsOnlyItsOwnTurn(t *testing.T) {
	f := newWerkDaemonFixture(t)
	panicking, release := make(chan struct{}), make(chan struct{})
	healthy := "the other worker finished"
	installWerkFixtureTransport(t, func(wrapped http.RoundTripper) http.RoundTripper {
		return werkFixtureRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Hostname() != "openrouter.ai" {
				return wrapped.RoundTrip(req)
			}
			model, err := readWerkFixtureRequest(req)
			if err != nil {
				return nil, err
			}
			if len(model.Input) == 1 && strings.Contains(string(model.Input[0]), "the panicking turn") {
				close(panicking)
				select {
				case <-release:
					panic("fixture provider panic")
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
			}
			return werkFixtureReply(healthy)
		})
	})
	open := f.open(nil)
	panickedID := f.spawn(open, "the panicking turn", "read-only")
	awaitWerkFixture(t, panicking, "the panicking model request")
	_, original := f.activeLog(panickedID)
	healthyID := f.spawn(open, "the healthy turn", "read-only")
	other := awaitWerkFixture(t, f.reports, "the healthy worker's report")
	if other.Result.ID != healthyID || other.Result.Status != wsagent.TurnDone || other.Result.Message != healthy {
		t.Fatal("the other worker did not complete while the first was running")
	}
	close(release)
	failed := awaitWerkFixture(t, f.reports, "the panicked worker's report")
	if failed.Result.ID != panickedID || failed.Result.Status != wsagent.TurnFailed || !strings.Contains(failed.Result.Error, "failed unexpectedly") {
		t.Fatal("the provider panic did not fail its own turn")
	}
	open.daemon.Close()
	rec, err := wsagent.ReadRecord(f.state, panickedID)
	if err != nil || rec.LastReportedTurn != original || len(f.reports) != 0 {
		t.Fatalf("the panic did not settle once under the original turn identity: %v", err)
	}
}
