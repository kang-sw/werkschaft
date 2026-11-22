package wsagent

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wslive"
)

// TestCodexAuditCapturesEveryKindFromRecordings pins the Codex adapter over
// the recorded app-server streams (0.160.0 and 0.170.0): reasoning as
// thinking, tool items as a call and a result joined by the item id, hooks,
// sub-agent activity, the contextCompaction item as the model-seen cut, and
// every other notification, request, and response as a system event; user
// and agent messages are never captured and nothing lands as other.
func TestCodexAuditCapturesEveryKindFromRecordings(t *testing.T) {
	names := []string{
		"codex-0.160.0-handshake.jsonl", "codex-0.160.0-hosted-lead.jsonl", "codex-0.160.0-lead-session.jsonl",
		"codex-0.160.0-multi-agent-feature-off.jsonl", "codex-0.160.0-plugin-mcp-block.jsonl", "codex-0.160.0-resume.jsonl",
		"codex-0.160.0-stale-approval.jsonl", "codex-0.160.0-subagent-baseline.jsonl", "codex-0.160.0-subagent-removed.jsonl",
		"codex-0.160.0-ws-mcp-shadow.jsonl", "codex-0.170.0-compaction-probe-1.jsonl", "codex-0.170.0-compaction-probe-2.jsonl",
	}
	sink := &memSink{}
	r := newAuditRigWith(t, sink, "h1", newCodexMapper(""), codexProtocol{})
	for _, name := range names {
		for _, line := range claudeFixtureLines(t, name) {
			if !strings.HasPrefix(string(line), ">> ") {
				r.kept(string(line))
			}
		}
	}
	r.end()
	evs := sink.events()
	for _, k := range []wsaudit.Kind{wsaudit.KindThinking, wsaudit.KindToolCall, wsaudit.KindToolResult, wsaudit.KindHook,
		wsaudit.KindSystem, wsaudit.KindCompaction, wsaudit.KindSubagent} {
		if len(find(evs, ofKind(k))) == 0 {
			t.Errorf("no %s event captured", k)
		}
	}
	for _, k := range []wsaudit.Kind{wsaudit.KindUser, wsaudit.KindAssistantText, wsaudit.KindOther} {
		if got := find(evs, ofKind(k)); len(got) > 0 {
			t.Errorf("%s captured: %.300v", k, got[0])
		}
	}
	calls := map[string]bool{}
	for _, ev := range evs {
		if err := ev.Validate(); err != nil {
			t.Fatalf("%+v: %v", ev, err)
		}
		if ev.Harness != BackendCodex {
			t.Fatalf("harness = %q", ev.Harness)
		}
		// Decision 7: the app-server stream reports per-request usage
		// (thread/tokenUsage/updated) but no model request ID, so no event
		// carries meta.request.
		if _, ok := ev.Meta[wsaudit.MetaRequest]; ok {
			t.Fatalf("codex event carries a request without a request ID: %+v", ev)
		}
		if ev.Kind == wsaudit.KindToolCall {
			if ev.CallID == "" || ev.Subkind == "" {
				t.Fatalf("tool call without id or name: %+v", ev)
			}
			calls[ev.CallID] = true
		}
	}
	for _, ev := range find(evs, ofKind(wsaudit.KindToolResult)) {
		if !calls[ev.CallID] {
			t.Fatalf("tool result %q joins no captured call", ev.CallID)
		}
	}
	if mcp := find(evs, func(ev wsaudit.Envelope) bool {
		return ev.Subkind == "ws/codex_probe_echo" && ev.Kind == wsaudit.KindToolResult
	}); len(mcp) == 0 || mcp[0].Out != "ECHO:CODEX-ADAPTER-OK" {
		t.Fatalf("mcp tool result = %+v", mcp)
	}
	if th := find(evs, ofKind(wsaudit.KindThinking)); th[0].Meta[wsaudit.MetaSignatureOnly] != true {
		t.Fatalf("an empty reasoning item = %+v, want it marked signature-only", th[0])
	}
	if hooks := find(evs, ofKind(wsaudit.KindHook)); hooks[0].Subkind != "sessionStart" {
		t.Fatalf("hook = %+v", hooks[0])
	}
}

// TestCodexAuditCompactionBoundaryPerVersion pins the compaction boundary
// exactly, per recorded version, line by line through the adapter.
//
//   - 0.160.0: the only recording that compacts is the hosted lead's manual
//     compaction (thread/compact/start at line 74). The contextCompaction
//     item starts at line 78 and completes at line 81. The completion, and
//     nothing else, is the one explicit cut. No thread/compacted
//     notification appears.
//   - 0.170.0: both compaction probes stop at a thread/start error (line 6)
//     before any turn, so the 0.170.0 boundary stays unverified. They yield
//     no compaction event, and the error is captured as a system event.
func TestCodexAuditCompactionBoundaryPerVersion(t *testing.T) {
	type at struct {
		name string
		line int
	}
	// compactions maps each recording through a fresh adapter and returns
	// the lines that yield a compaction event, with every event mapped.
	compactions := func(t *testing.T, names ...string) ([]at, []wsaudit.Envelope) {
		t.Helper()
		var got []at
		var all []wsaudit.Envelope
		for _, name := range names {
			a := newCodexAudit("s1")
			raw, err := os.ReadFile("testdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(raw), "\n") {
				if line == "" || strings.HasPrefix(line, ">> ") {
					continue
				}
				_, carried, evs := a.mapLine([]byte(line), time.Now())
				evs = append(carried, evs...)
				all = append(all, evs...)
				for _, ev := range evs {
					if ev.Kind == wsaudit.KindCompaction {
						got = append(got, at{name, i + 1})
					}
				}
			}
		}
		return got, all
	}

	t.Run("0.160.0", func(t *testing.T) {
		names := []string{
			"codex-0.160.0-handshake.jsonl", "codex-0.160.0-hosted-lead.jsonl", "codex-0.160.0-lead-session.jsonl",
			"codex-0.160.0-multi-agent-feature-off.jsonl", "codex-0.160.0-plugin-mcp-block.jsonl", "codex-0.160.0-resume.jsonl",
			"codex-0.160.0-stale-approval.jsonl", "codex-0.160.0-subagent-baseline.jsonl", "codex-0.160.0-subagent-removed.jsonl",
			"codex-0.160.0-ws-mcp-shadow.jsonl",
		}
		got, all := compactions(t, names...)
		if want := []at{{"codex-0.160.0-hosted-lead.jsonl", 81}}; !slices.Equal(got, want) {
			t.Fatalf("compaction events at %v, want only %v (the contextCompaction completion)", got, want)
		}
		for _, ev := range all {
			if strings.Contains(ev.Out, "thread/compacted") {
				t.Fatalf("a thread/compacted notification was recorded: %.200s", ev.Out)
			}
		}
		marked, cut := wsaudit.ApplyModelSeenCut(all)
		if !cut.HasCompaction || cut.Estimated || marked[cut.Index].Kind != wsaudit.KindCompaction {
			t.Fatalf("cut = %+v, want the contextCompaction completion as the explicit cut", cut)
		}
	})

	t.Run("0.170.0", func(t *testing.T) {
		names := []string{"codex-0.170.0-compaction-probe-1.jsonl", "codex-0.170.0-compaction-probe-2.jsonl"}
		got, all := compactions(t, names...)
		if len(got) != 0 {
			t.Fatalf("compaction events at %v; the 0.170.0 probes never start a thread", got)
		}
		errs := find(all, func(ev wsaudit.Envelope) bool {
			return ev.Kind == wsaudit.KindSystem && strings.Contains(ev.Out, "failed to load configuration")
		})
		if len(errs) != len(names) {
			t.Fatalf("thread/start errors captured = %d, want one per probe", len(errs))
		}
		if _, cut := wsaudit.ApplyModelSeenCut(all); cut.HasCompaction {
			t.Fatalf("cut = %+v, want none", cut)
		}
	})
}

// TestCodexRingCallIDMatchesTheAuditCallID pins (601) for Codex: the
// captured tool_call and tool_result events carry the raw item ids, and the
// tools row's calls carry the display key the store's ingestion makes of
// them, NormalizeID(raw), including a long id (digested).
func TestCodexRingCallIDMatchesTheAuditCallID(t *testing.T) {
	long := "exec-" + strings.Repeat("y", 300)
	item := func(method, id, status string) string {
		return fmt.Sprintf(`{"method":%q,"params":{"threadId":"th1","turnId":"tu1","item":{"type":"mcpToolCall","id":%q,"server":"ws","tool":"echo","status":%q,"arguments":{"text":"x"},"result":{"content":[{"type":"text","text":"x"}]},"error":null}}}`, method, id, status)
	}
	lines := []string{
		`{"method":"turn/started","params":{"threadId":"th1","turn":{"id":"tu1","status":"inProgress","items":[]}}}`,
		item("item/started", long, "inProgress"),
		item("item/started", "exec-short", "inProgress"),
		item("item/completed", long, "completed"),
		item("item/completed", "exec-short", "completed"),
		`{"method":"turn/completed","params":{"threadId":"th1","turn":{"id":"tu1","status":"completed","items":[]}}}`,
	}
	sink := &memSink{}
	r := newAuditRigWith(t, sink, "h1", newCodexMapper("th1"), codexProtocol{})
	for _, line := range lines {
		r.kept(line)
	}
	waitUntil(t, "the batches", func() bool { return r.displayLines() == len(lines) })
	r.end()
	st := wslive.NewState("")
	r.mu.Lock()
	for i, ev := range r.evs {
		if ev.Live != nil {
			if _, err := st.Apply(*ev.Live, int64(1000+i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	r.mu.Unlock()
	res, err := st.Apply(wslive.Event{Kind: wslive.KindReply, Final: true, Text: "done"}, 5000)
	if err != nil {
		t.Fatal(err)
	}
	var ring []string
	for _, e := range res.Ring {
		if e.Kind == wslive.RingTools {
			for _, b := range e.Tools.Batches {
				for _, c := range b.CallArgs {
					ring = append(ring, c.ID)
				}
			}
		}
	}
	if want := []string{wsaudit.NormalizeID(long), "exec-short"}; !slices.Equal(ring, want) {
		t.Fatalf("ring call ids = %q, want %q", ring, want)
	}
	keyed := map[string][]wsaudit.Kind{}
	var raw []string
	for _, ev := range sink.events() {
		if ev.CallID != "" {
			raw = append(raw, ev.CallID)
			key := wsaudit.NormalizeID(ev.CallID) // the store's ingestion
			keyed[key] = append(keyed[key], ev.Kind)
		}
	}
	// Envelopes carry the raw item ids; only the store normalizes.
	if want := []string{long, "exec-short", long, "exec-short"}; !slices.Equal(raw, want) {
		t.Fatalf("envelope call ids = %q, want the raw item ids %q", raw, want)
	}
	for _, id := range ring {
		if !slices.Equal(keyed[id], []wsaudit.Kind{wsaudit.KindToolCall, wsaudit.KindToolResult}) {
			t.Fatalf("history row id %q opens %v, want its call and its result", id, keyed[id])
		}
	}
}
