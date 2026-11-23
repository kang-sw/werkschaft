package werk

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wsfs"
)

type correlatedFileFixture struct {
	fakeWS
	call func(context.Context, wsfs.FileCall) (CallResult, error)
}

func (w *correlatedFileFixture) CallCorrelated(ctx context.Context, tool string, args json.RawMessage, id string) (CallResult, error) {
	return w.call(ctx, wsfs.FileCall{ID: id, Tool: tool, Arguments: args})
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
			a, err := New(Config{Dir: dir, Authority: AuthorityWorkdirWrite, MCPFilesOnly: true, WS: tool,
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
			_, failed := a.invokeMutating(ctx, map[string]toolEntry{"fs_write": {ws: "fs.write"}},
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
