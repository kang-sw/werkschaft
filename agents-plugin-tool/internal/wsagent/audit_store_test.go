package wsagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsauditstore"
	"github.com/kang-sw/devenv/internal/wsholder"
)

// storeSink is the real audit store as capture's sink, with two test seams:
// failEnv fails matching appends before they reach the store (a live append
// failure), and cold makes Recover answer zero, as the store does while the
// session's ring is still loading. Admission is the store's own.
type storeSink struct {
	*wsauditstore.Store
	mu       sync.Mutex
	failEnv  func(wsaudit.Envelope) bool
	cold     bool
	captured []wsaudit.SourceID // identities capture itself recorded lost
}

func (s *storeSink) Recover(session, source string) wsaudit.Recovery {
	s.mu.Lock()
	cold := s.cold
	s.mu.Unlock()
	if cold {
		return wsaudit.Recovery{}
	}
	return s.Store.Recover(session, source)
}

func (s *storeSink) AppendSourced(ctx context.Context, id, rec wsaudit.SourceID, ev wsaudit.Envelope) error {
	s.mu.Lock()
	fail := s.failEnv != nil && s.failEnv(ev)
	s.mu.Unlock()
	if fail {
		return errors.New("injected append failure")
	}
	return s.Store.AppendSourced(ctx, id, rec, ev)
}

func (s *storeSink) RecordLoss(ctx context.Context, session string, id wsaudit.SourceID, reason string) error {
	s.mu.Lock()
	s.captured = append(s.captured, id)
	s.mu.Unlock()
	return s.Store.RecordLoss(ctx, session, id, reason)
}

var _ wsaudit.SourcedSink = (*storeSink)(nil)

func openStoreSink(t *testing.T, root string) *storeSink {
	t.Helper()
	st, err := wsauditstore.Open(wsauditstore.Options{Root: root, SweepInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &storeSink{Store: st}
}

func storeEvents(t *testing.T, s *storeSink) []wsaudit.Envelope {
	t.Helper()
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	evs, err := s.Events(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

// persistedLosses reads the loss lines of every ring segment under root.
func persistedLosses(t *testing.T, root string) []wsaudit.SourceID {
	t.Helper()
	var out []wsaudit.SourceID
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 64<<20)
		for sc.Scan() {
			var ln struct {
				T  string            `json:"t"`
				ID *wsaudit.SourceID `json:"id"`
			}
			if json.Unmarshal(sc.Bytes(), &ln) == nil && ln.T == "loss" && ln.ID != nil {
				out = append(out, *ln.ID)
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAuditColdReplayAgainstTheStoreNeverAppendsAKeyedEventLate is the Phase
// 5 cold-replay check against the real store (ticket
// 261119-feat-ws-audit-capture-harness-adapters, Decision 5): live, a keyed
// event's append fails while its result (and, for Codex, a compaction cut)
// is retained; the daemon restarts and the holder replays while Recover still
// answers zero. Capture cannot judge the keyed identity, so the store's
// writer must: by the source record it is handed, covered by the recovered
// finalized boundary, the event is recorded as loss and never appended after
// its result and the cut.
func TestAuditColdReplayAgainstTheStoreNeverAppendsAKeyedEventLate(t *testing.T) {
	isCall := func(ev wsaudit.Envelope) bool { return ev.Kind == wsaudit.KindToolCall }

	t.Run("codex call", func(t *testing.T) {
		item := func(method, status string) string {
			return fmt.Sprintf(`{"method":%q,"params":{"threadId":"th1","turnId":"tu1","item":{"type":"commandExecution","id":"call-1","command":"ls","cwd":"/","status":%q,"aggregatedOutput":"ok","exitCode":0}}}`, method, status)
		}
		lines := []string{
			`{"method":"turn/started","params":{"threadId":"th1","turn":{"id":"tu1","status":"inProgress","items":[]}}}`,
			item("item/started", "inProgress"),
			item("item/completed", "completed"),
			`{"method":"item/completed","params":{"threadId":"th1","turnId":"tu1","item":{"type":"contextCompaction","id":"cc-1"}}}`,
			`{"method":"turn/completed","params":{"threadId":"th1","turn":{"id":"tu1","status":"completed","items":[]}}}`,
		}
		root := t.TempDir()
		sink := openStoreSink(t, root)
		sink.failEnv = isCall
		r := newAuditRigWith(t, sink, "h1", newCodexMapper("th1"), codexProtocol{})
		for _, l := range lines {
			r.kept(l)
		}
		r.end()
		live := storeEvents(t, sink)
		if len(find(live, isCall)) != 0 || len(find(live, ofKind(wsaudit.KindToolResult))) != 1 || len(find(live, ofKind(wsaudit.KindCompaction))) != 1 {
			t.Fatalf("live = %+v, want the result and the cut without the call", live)
		}
		if err := sink.Close(); err != nil {
			t.Fatal(err)
		}

		cold := openStoreSink(t, root) // the session's ring is not loaded
		cold.cold = true
		r2 := newAuditRigWith(t, cold, "h1", newCodexMapper("th1"), codexProtocol{})
		for i, l := range lines {
			r2.replayed(uint64(i+1), l)
		}
		r2.end()
		evs := storeEvents(t, cold)
		if calls := find(evs, isCall); len(calls) != 0 {
			t.Fatalf("call appended late after its result and the cut: %+v", calls)
		}
		if len(evs) != len(live) {
			t.Fatalf("%d events after replay, live %d; want nothing new", len(evs), len(live))
		}
		want := wsaudit.SourceID{Source: "h1", Key: codexCallKey("call-1")}
		if len(cold.captured) != 0 {
			t.Fatalf("capture recorded %+v lost; with a zero Recover the store must judge", cold.captured)
		}
		if got := persistedLosses(t, root); !slices.Equal(got, []wsaudit.SourceID{want}) {
			t.Fatalf("persisted losses = %+v, want only the call %+v", got, want)
		}
	})

	t.Run("claude carried assembly", func(t *testing.T) {
		const (
			toolStart = `{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}}}`
			toolInput = `{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"ls\"}"}}}`
			toolStop  = `{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`
		)
		root := t.TempDir()
		sink := openStoreSink(t, root)
		r := newAuditRig(t, sink, "h1")
		r.audit(wsholder.Event{Event: wsholder.EventAuditAttached, Next: 1})
		r.keptRun(thinkStart, 0, 0)
		r.keptRun(thinkBlock, 0, 0)
		r.tee(thinkDelta("tee only"))
		r.keptRun(thinkStop, 1, 0)
		r.keptRun(toolStart, 0, 0)
		r.tee(toolInput)
		r.keptRun(toolStop, 1, 0)
		sink.mu.Lock()
		sink.failEnv = isCall
		sink.mu.Unlock()
		resultSeq := r.keptRun(auditResultLine, 0, 0)
		kept := []string{thinkStart, thinkBlock, thinkStop, toolStart, toolStop, auditResultLine}
		r.end()
		live := storeEvents(t, sink)
		if th := find(live, ofKind(wsaudit.KindThinking)); len(th) != 1 || th[0].Out != "tee only" {
			t.Fatalf("live thinking = %+v", th)
		}
		if n := len(find(live, isCall)); n != 0 {
			t.Fatalf("live tool calls = %d, want the failed append missing", n)
		}
		if err := sink.Close(); err != nil {
			t.Fatal(err)
		}

		cold := openStoreSink(t, root) // the session's ring is not loaded
		cold.cold = true
		r2 := newAuditRig(t, cold, "h1")
		for i, l := range kept {
			r2.replayed(uint64(i+1), l)
		}
		r2.end()
		evs := storeEvents(t, cold)
		if calls := find(evs, isCall); len(calls) != 0 {
			t.Fatalf("tool calls after replay = %+v, want the failed one never appended late", calls)
		}
		if n := len(find(evs, ofKind(wsaudit.KindThinking))); n != 1 {
			t.Fatalf("thinking events after replay = %d, want 1", n)
		}
		if len(evs) != len(live) {
			t.Fatalf("%d events after replay, live %d; want nothing new", len(evs), len(live))
		}
		want := wsaudit.SourceID{Source: "h1", Key: fmt.Sprintf("asm.%d", resultSeq), Ordinal: 1}
		if len(cold.captured) != 0 {
			t.Fatalf("capture recorded %+v lost; with a zero Recover the store must judge", cold.captured)
		}
		if got := persistedLosses(t, root); !slices.Equal(got, []wsaudit.SourceID{want}) {
			t.Fatalf("persisted losses = %+v, want only the tool call %+v", got, want)
		}
	})
}
