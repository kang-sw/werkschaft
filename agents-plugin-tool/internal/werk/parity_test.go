package werk

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/execjob"
)

func TestWerkLoadsOnlyRequestedSkill(t *testing.T) {
	for _, lead := range []bool{false, true} {
		loads := 0
		endpoint := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
			reply(callOf("skill", "load_skill", `{"name":"fixture"}`)),
			reply(msgItem("done")),
		}}
		a, err := New(Config{
			Dir: t.TempDir(), Authority: AuthorityReadOnly, Lead: lead, Endpoint: endpoint,
			Skills: []Skill{{Name: "fixture", Description: "Useful for this task", Load: func() (string, error) {
				loads++
				return "The on-demand fixture body", nil
			}}},
		})
		must(t, err)
		a.runTurn(context.Background(), "Load the fixture")
		must(t, a.Close())
		requests := endpoint.requests()
		if len(requests) != 2 || loads != 1 {
			t.Fatalf("lead=%t, requests=%d, skill loads=%d", lead, len(requests), loads)
		}
		index := requests[0].Instructions
		if !strings.Contains(index, "fixture: Useful for this task") || strings.Contains(index, "The on-demand fixture body") {
			t.Fatal("the initial request should contain the skill index alone")
		}
		if !slices.ContainsFunc(requests[0].Tools, func(tool ToolDef) bool { return tool.Name == "load_skill" }) {
			t.Fatal("the initial request did not advertise the load tool")
		}
		if !strings.Contains(string(requests[1].Input[len(requests[1].Input)-1]), "The on-demand fixture body") {
			t.Fatal("the model did not receive the selected skill")
		}
	}
}

type approvalTestExecTools struct {
	fakeWS
	launch func(context.Context, execjob.ModelLaunch) (CallResult, error)
}

func (w *approvalTestExecTools) CallCorrelated(ctx context.Context, tool string, args json.RawMessage, id string) (CallResult, error) {
	return w.launch(ctx, execjob.ModelLaunch{Tool: tool, Arguments: args, ID: id})
}

func TestWerkRecoversPendingExecApprovalButNeverDispatchesTwice(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cancelIn  string
		wantCalls int
	}{
		{name: "waiting", cancelIn: "approval", wantCalls: 0},
		{name: "dispatched", cancelIn: "tool", wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, session := t.TempDir(), NewSessionID()
			command := Command{Type: CmdUser, TurnID: "persisted-turn", Text: "start the fixture job"}
			firstCtx, replace := context.WithCancel(context.Background())
			defer replace()
			var calls, firstPrompts, secondPrompts int
			predecessor := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){
				reply(callOf("mutation", "exec_run", `{"exe":"printf"}`)),
			}}
			start := func(model Responder, level Authority, approve func(context.Context, string, json.RawMessage) error) *Agent {
				t.Helper()
				ws := &approvalTestExecTools{fakeWS: fakeWS{tools: []MCPTool{{Name: "exec.run"}}}}
				a, err := New(Config{
					Dir: dir, Session: session, Authority: level, Lead: true, WS: ws, Endpoint: model, Approve: approve,
					RecoverExec: func(_ context.Context, job string) (string, error) {
						if job != "fixture-job" {
							return "", errors.New("wrong recovered job")
						}
						return "original job", nil
					},
				})
				must(t, err)
				ws.launch = func(ctx context.Context, call execjob.ModelLaunch) (CallResult, error) {
					if _, err := a.ApproveExecLaunch(ctx, call, json.RawMessage(`{"program":"printf"}`)); err != nil {
						return CallResult{}, err
					}
					call.JobID = "fixture-job"
					if err := a.RecordExecLaunch(ctx, call); err != nil {
						return CallResult{}, err
					}
					calls++
					replace()
					return CallResult{}, ctx.Err()
				}
				t.Cleanup(func() { _ = a.Close() })
				return a
			}
			first := start(predecessor, AuthorityWorkdirWrite, func(ctx context.Context, _ string, _ json.RawMessage) error {
				firstPrompts++
				if tc.cancelIn == "approval" {
					replace()
				}
				return ctx.Err()
			})
			must(t, first.runDurableTurn(firstCtx, firstCtx, command))
			must(t, first.Close())
			if firstPrompts != 1 || calls != tc.wantCalls {
				t.Fatalf("predecessor saw %d prompts and %d calls", firstPrompts, calls)
			}

			wantResult := "Recovered exec launch"
			wantPrompts := 0
			if tc.cancelIn == "approval" {
				wantResult, wantPrompts = "owner declined on recovery", 1
			}
			nextModel := &scripted{steps: []func(context.Context, Request, StreamHandler) (*Response, error){reply(msgItem("done"))}}
			second := start(nextModel, AuthorityFull, func(_ context.Context, name string, _ json.RawMessage) error {
				secondPrompts++
				if name != "exec.run" {
					return errors.New("wrong recovered tool")
				}
				return errors.New("owner declined on recovery")
			})
			ctx := context.Background()
			must(t, second.runDurableTurn(ctx, ctx, command))
			must(t, second.Close())
			requests := nextModel.requests()
			if len(requests) != 1 || secondPrompts != wantPrompts || calls != tc.wantCalls {
				t.Fatalf("successor saw %d requests, %d prompts and %d total tool calls", len(requests), secondPrompts, calls)
			}
			input := requests[0].Input
			if !strings.Contains(string(input[len(input)-1]), wantResult) {
				t.Fatalf("expected the model to see %q", wantResult)
			}
		})
	}
}
