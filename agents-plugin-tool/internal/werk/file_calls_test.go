package werk

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsfs"
)

type correlatedFileFixture struct {
	fakeWS
	call func(context.Context, wsfs.FileCall) (CallResult, error)
}

func (w *correlatedFileFixture) CallCorrelated(ctx context.Context, tool string, args json.RawMessage, id string) (CallResult, error) {
	return w.call(ctx, wsfs.FileCall{ID: id, Tool: canonicalFileTool(tool), Arguments: args})
}

func TestFileCallRecordsOnlyMatchingIntentBeforeMutation(t *testing.T) {
	for _, rejectWrite := range []bool{false, true} {
		name := "committed"
		if rejectWrite {
			name = "failed intent write"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "changed.txt")
			args := json.RawMessage(`{"path":"changed.txt","content":"new"}`)
			tool := &correlatedFileFixture{}
			a, err := New(Config{Dir: dir, Authority: AuthorityWorkdirWrite, WS: tool,
				Endpoint: &scripted{}, BeforeAppend: func(raw json.RawMessage) error {
					var line fileIntentLine
					if rejectWrite && json.Unmarshal(raw, &line) == nil && line.K == "file_intent" {
						return errors.New("intent log write failed")
					}
					return nil
				},
			})
			must(t, err)
			t.Cleanup(func() { _ = a.Close() })
			ctx := context.Background()
			if err := a.RecordFileCall(ctx, wsfs.FileCall{ID: "c", Tool: "fs.write", Arguments: args}); err == nil {
				t.Fatal("an unsolicited call was accepted")
			}
			tool.call = func(ctx context.Context, call wsfs.FileCall) (CallResult, error) {
				wrongID := call
				wrongID.ID = "different"
				wrongTool := call
				wrongTool.Tool = "fs.edit"
				wrongArgs := call
				wrongArgs.Arguments = json.RawMessage(`{"path":"changed.txt","content":"different"}`)
				for _, wrong := range []wsfs.FileCall{wrongID, wrongTool, wrongArgs} {
					if err := a.RecordFileCall(ctx, wrong); err == nil {
						t.Error("a mismatched call was accepted")
					}
				}
				files := wsfs.Files{Root: dir, Authority: wsfs.AuthorityWorkdirWrite,
					RecordMutation: func(ctx context.Context, intent wsfs.FileIntent) error {
						call.Intent = intent
						call.Arguments = json.RawMessage(`{ "content": "new", "path": "changed.txt" }`)
						if err := a.RecordFileCall(ctx, call); err != nil {
							return err
						}
						if err := a.RecordFileCall(ctx, call); err == nil {
							t.Error("the same intent was accepted twice")
						}
						return nil
					},
				}
				text, err := files.Write(ctx, args)
				return CallResult{Text: text}, err
			}
			a.append(json.RawMessage(`{"type":"function_call","call_id":"c","name":"fs_write"}`))
			_, failed := a.invokeMutating(ctx, map[string]toolEntry{"fs_write": {ws: "fs_write"}},
				functionCall{CallID: "c", Name: "fs_write", Arguments: string(args)})
			if failed != rejectWrite {
				t.Fatalf("mutation failed = %v, want %v", failed, rejectWrite)
			}
			must(t, a.Close())
			store, items, err := OpenStore(dir, a.Session(), nil)
			must(t, err)
			defer store.Close()
			intent, found := store.fileIntents["c"]
			if len(items) != 1 || found == rejectWrite {
				t.Fatalf("replay got %d model items and intent present = %v", len(items), found)
			}
			if rejectWrite {
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatalf("target changed without durable intent: %v", err)
				}
			} else if intent.Prior != nil || intent.Outcome() != "applied" {
				t.Fatalf("replayed intent has outcome %q and prior %v", intent.Outcome(), intent.Prior)
			}
		})
	}
}

func TestExecLaunchRecordsOnlyMatchingCall(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := New(Config{Dir: dir, Authority: AuthorityWorkdirWrite, Endpoint: &scripted{}})
	must(t, err)
	defer a.Close()
	call := execjob.ModelLaunch{ID: "c", Tool: "exec.run", JobID: "job", Arguments: json.RawMessage(`{"exe":"printf"}`)}
	if a.RecordExecLaunch(ctx, call) == nil {
		t.Fatal("an unsolicited call was recorded")
	}
	done, err := a.beginMutationCall(ctx, call.ID, call.Tool, call.Arguments, false, false)
	must(t, err)
	for _, wrong := range []execjob.ModelLaunch{
		{ID: "wrong", Tool: call.Tool, JobID: call.JobID, Arguments: call.Arguments},
		{ID: call.ID, Tool: "exec.shell", JobID: call.JobID, Arguments: call.Arguments},
		{ID: call.ID, Tool: call.Tool, JobID: call.JobID, Arguments: json.RawMessage(`{"exe":"other"}`)},
		{ID: call.ID, Tool: call.Tool, Arguments: call.Arguments},
	} {
		if a.RecordExecLaunch(ctx, wrong) == nil {
			t.Errorf("a mismatched call was recorded: %+v", wrong)
		}
	}
	must(t, a.RecordExecLaunch(ctx, call))
	call.JobID = "duplicate"
	if a.RecordExecLaunch(ctx, call) == nil {
		t.Error("a duplicate call was recorded")
	}
	done()
	must(t, a.Close())
	store, _, err := OpenStore(dir, a.Session(), nil)
	must(t, err)
	defer store.Close()
	if job := store.execJobs[call.ID]; job != "job" {
		t.Fatalf("associated job = %q", job)
	}
}

func TestWerkExecLaunchRequiresMatchingLiveApproval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prompts := 0
	preview := json.RawMessage(`{"program":"printf"}`)
	a, err := New(Config{
		Dir: t.TempDir(), Lead: true, Authority: AuthorityWorkdirWrite, Endpoint: &scripted{},
		Approve: func(_ context.Context, tool string, input json.RawMessage) error {
			prompts++
			if tool != "exec.run" || string(input) != string(preview) {
				t.Errorf("unexpected approval preview for %q", tool)
			}
			return nil
		},
	})
	must(t, err)
	defer a.Close()
	call := execjob.ModelLaunch{ID: "c", Tool: "exec.run", JobID: "job", Arguments: json.RawMessage(`{"exe":"printf","args":["hello"]}`)}
	if _, err := a.ApproveExecLaunch(ctx, call, preview); err == nil {
		t.Fatal("unsolicited preview was accepted")
	}
	done, err := a.beginMutationCall(ctx, call.ID, call.Tool, call.Arguments, false, false)
	must(t, err)
	if a.RecordExecLaunch(ctx, call) == nil {
		t.Fatal("job was bound before the matching approval")
	}
	for _, wrong := range []execjob.ModelLaunch{
		{ID: "wrong", Tool: call.Tool, Arguments: call.Arguments},
		{ID: call.ID, Tool: "exec.shell", Arguments: call.Arguments},
		{ID: call.ID, Tool: call.Tool, Arguments: json.RawMessage(`{"exe":"printf","args":["changed"]}`)},
	} {
		if _, err := a.ApproveExecLaunch(ctx, wrong, preview); err == nil {
			t.Fatal("mismatched preview was accepted")
		}
	}
	explicit, err := a.ApproveExecLaunch(ctx, call, preview)
	must(t, err)
	if !explicit {
		t.Fatal("the approval answer lost its explicit origin")
	}
	must(t, a.RecordExecLaunch(ctx, call))
	if _, err := a.ApproveExecLaunch(ctx, call, preview); err == nil || a.RecordExecLaunch(ctx, call) == nil || prompts != 1 {
		t.Fatal("approval or job binding could be reused")
	}
	done()

	stale := execjob.ModelLaunch{ID: "cancelled", Tool: call.Tool, JobID: "other-job", Arguments: call.Arguments}
	end, err := a.beginMutationCall(ctx, stale.ID, stale.Tool, stale.Arguments, false, false)
	must(t, err)
	defer end()
	cancel()
	if _, err := a.ApproveExecLaunch(context.Background(), stale, preview); err == nil || a.RecordExecLaunch(context.Background(), stale) == nil {
		t.Fatal("a cancelled call accepted a preview or bound a job")
	}
}

func TestWerkFileCallRequiresApprovalAfterPostureDowngrade(t *testing.T) {
	for _, allow := range []bool{false, true} {
		name := "deny"
		if allow {
			name = "allow"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "changed.txt")
			tool := &correlatedFileFixture{}
			prompts := 0
			a, err := New(Config{Dir: dir, Lead: true, Authority: AuthorityFull, WS: tool,
				Endpoint: &scripted{}, Approve: func(context.Context, string, json.RawMessage) error {
					prompts++
					if _, err := os.Stat(target); !os.IsNotExist(err) {
						t.Fatal("the file changed before approval")
					}
					if !allow {
						return errors.New("operator denied fixture")
					}
					return nil
				},
			})
			must(t, err)
			defer a.Close()
			tool.call = func(ctx context.Context, call wsfs.FileCall) (CallResult, error) {
				if prompts != 0 {
					t.Fatal("the initial full-access dispatch asked for approval")
				}
				// The MCP boundary observes the downgrade before the model
				// loop's permission callback reaches this process.
				call.Authority = wsfs.AuthorityWorkdirWrite
				files := wsfs.Files{Root: dir, Authority: call.Authority, RecordMutation: func(ctx context.Context, intent wsfs.FileIntent) error {
					call.Intent = intent
					return a.RecordFileCall(ctx, call)
				}}
				text, err := files.Write(ctx, call.Arguments)
				return CallResult{Text: text}, err
			}
			args := `{"path":"changed.txt","content":"new"}`
			a.append(json.RawMessage(`{"type":"function_call","call_id":"c","name":"fs_write"}`))
			_, failed := a.invokeMutating(context.Background(), map[string]toolEntry{"fs_write": {ws: "fs_write"}},
				functionCall{CallID: "c", Name: "fs_write", Arguments: args})
			if failed == allow || prompts != 1 {
				t.Fatalf("failed = %v, prompts = %d", failed, prompts)
			}
			data, err := os.ReadFile(target)
			if allow && (err != nil || string(data) != "new") || !allow && !os.IsNotExist(err) {
				t.Fatalf("result = %q, %v", data, err)
			}
		})
	}
}
