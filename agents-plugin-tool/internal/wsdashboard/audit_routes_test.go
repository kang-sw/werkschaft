package wsdashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsauditstore"
)

// fixtureAudit is a fixture audit store: one View per session, replaceable
// between requests to model eviction.
type fixtureAudit struct {
	mu    sync.Mutex
	views map[string]wsauditstore.View
}

func (f *fixtureAudit) View(_ context.Context, session string) (wsauditstore.View, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.views[session], nil
}

func (f *fixtureAudit) set(session string, v wsauditstore.View) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.views == nil {
		f.views = map[string]wsauditstore.View{}
	}
	f.views[session] = v
}

func auditDaemon(r auditViewer) *Daemon {
	return &Daemon{log: io.Discard, auditRead: r}
}

// auditGet runs one request through the session routes and returns the
// recorder.
func auditGet(t *testing.T, d *Daemon, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	d.handleSessionRoutes(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func auditJSON[T any](t *testing.T, d *Daemon, target string, want int) T {
	t.Helper()
	rec := auditGet(t, d, http.MethodGet, target)
	if rec.Code != want {
		t.Fatalf("GET %s = %d (%s), want %d", target, rec.Code, strings.TrimSpace(rec.Body.String()), want)
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET %s: decode: %v\n%s", target, err, rec.Body.String())
	}
	return out
}

type pageOut struct {
	Events []map[string]any `json:"events"`
	Before string           `json:"before"`
	After  string           `json:"after"`
	End    bool             `json:"end"`
	Cut    bool             `json:"cut"`
}

func (p pageOut) ids() []string {
	out := make([]string, len(p.Events))
	for i, e := range p.Events {
		out[i], _ = e["id"].(string)
	}
	return out
}

var auditBase = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

func auditEv(seq int, kind wsaudit.Kind, at time.Time) wsaudit.Envelope {
	return wsaudit.Envelope{V: 1, ID: fmt.Sprintf("e%d", seq), Seq: uint64(seq), Session: "sess", At: at, Harness: "claude", Kind: kind, ModelSeen: true, Out: fmt.Sprintf("output %d", seq)}
}

// auditSeries is n events one second apart, kinds cycling tool_call,
// tool_result, hook.
func auditSeries(n int) []wsaudit.Envelope {
	kinds := []wsaudit.Kind{wsaudit.KindToolCall, wsaudit.KindToolResult, wsaudit.KindHook}
	evs := make([]wsaudit.Envelope, n)
	for i := range evs {
		evs[i] = auditEv(i+1, kinds[i%3], auditBase.Add(time.Duration(i)*time.Second))
	}
	return evs
}

func seqIDs(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf("e%d", i))
	}
	return out
}

func sameIDs(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// TestAuditEventsPagesLikeHistory pins Decision 1's paging: the newest n by
// default, before/after cursors by event ID, oldest first within a page,
// before/after/end/cut, the default and maximum n, and refusals.
func TestAuditEventsPagesLikeHistory(t *testing.T) {
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: auditSeries(250), Cut: true})
	d := auditDaemon(fx)
	base := "/s/sess/audit/events"

	p := auditJSON[pageOut](t, d, base, 200)
	sameIDs(t, "newest page", p.ids(), seqIDs(151, 250))
	if p.Before != "e151" || p.After != "e250" || p.End || p.Cut {
		t.Fatalf("newest page cursors %q %q end %v cut %v", p.Before, p.After, p.End, p.Cut)
	}
	p = auditJSON[pageOut](t, d, base+"?before=e151", 200)
	sameIDs(t, "second page", p.ids(), seqIDs(51, 150))
	p = auditJSON[pageOut](t, d, base+"?before="+p.Before, 200)
	sameIDs(t, "oldest page", p.ids(), seqIDs(1, 50))
	if !p.End || !p.Cut {
		t.Fatalf("oldest page end %v cut %v, want both (the ring dropped segments)", p.End, p.Cut)
	}
	p = auditJSON[pageOut](t, d, base+"?before=e1", 200)
	if len(p.Events) != 0 || p.Before != "" || p.After != "" || !p.End || !p.Cut {
		t.Fatalf("past the oldest: %+v", p)
	}
	// after walks forward from any event.
	p = auditJSON[pageOut](t, d, base+"?after=e50&n=3", 200)
	sameIDs(t, "after page", p.ids(), seqIDs(51, 53))
	if p.End || p.Cut {
		t.Fatal("an after page with older events is not the end")
	}
	p = auditJSON[pageOut](t, d, base+"?after=e250", 200)
	if len(p.Events) != 0 || p.Before != "" || p.After != "" || p.End {
		t.Fatalf("after the newest: %+v", p)
	}
	// n: clamped to 500, positive only.
	fx.set("big", wsauditstore.View{Events: auditSeries(700)})
	if p := auditJSON[pageOut](t, d, "/s/big/audit/events?n=9999", 200); len(p.Events) != 500 || p.End || p.Cut {
		t.Fatalf("n=9999 gave %d events", len(p.Events))
	}
	if p := auditJSON[pageOut](t, d, "/s/big/audit/events?n=700", 200); len(p.Events) != 500 {
		t.Fatalf("n=700 gave %d events", len(p.Events))
	}
	if p := auditJSON[pageOut](t, d, "/s/big/audit/events?before=e2", 200); !p.End || p.Cut {
		t.Fatalf("an uncut ring's end: end %v cut %v", p.End, p.Cut)
	}
	for _, q := range []string{"n=0", "n=-1", "n=x", "before=e5&after=e9", "kinds=tool_call,nope", "n=1&n=2", "before=" + strings.Repeat("x", auditIDMax+1)} {
		auditJSON[map[string]string](t, d, base+"?"+q, 400)
	}
	// IDs are opaque: a control character is no reason to refuse one.
	auditJSON[map[string]string](t, d, base+"?before=a%00b", 410)
	// The page summaries carry the contract's fields.
	p = auditJSON[pageOut](t, d, base+"?n=1", 200)
	for _, key := range []string{"id", "seq", "at", "kind", "subkind", "call_id", "parent_id", "agent_id", "model_seen", "title", "in_bytes", "out_bytes", "attached"} {
		if _, ok := p.Events[0][key]; !ok {
			t.Errorf("summary lacks %q: %v", key, p.Events[0])
		}
	}
	if p.Events[0]["at"] != auditBase.Add(249*time.Second).Format(wsaudit.TimestampLayout) || p.Events[0]["title"] != "output 250" {
		t.Errorf("summary = %v", p.Events[0])
	}
}

// TestAuditEventsKindsAndUTCBoundsSelectBeforeCursors pins Decision 2: kinds
// and since <= at < until select before the cursors and n; omitted bounds are
// open, equal bounds empty, and malformed, non-UTC or reversed bounds 400.
func TestAuditEventsKindsAndUTCBoundsSelectBeforeCursors(t *testing.T) {
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: auditSeries(30)})
	d := auditDaemon(fx)
	ts := func(sec int) string {
		return url.QueryEscape(auditBase.Add(time.Duration(sec) * time.Second).Format(time.RFC3339Nano))
	}
	base := "/s/sess/audit/events?"

	// e(k) is at second k-1: [since 10s, until 20s) holds e11..e20.
	p := auditJSON[pageOut](t, d, base+"since="+ts(10)+"&until="+ts(20), 200)
	sameIDs(t, "bounded", p.ids(), seqIDs(11, 20))
	if !p.End {
		t.Fatal("a bounded page holding every selected event is the end")
	}
	sameIDs(t, "since only", auditJSON[pageOut](t, d, base+"since="+ts(25), 200).ids(), seqIDs(26, 30))
	sameIDs(t, "until only", auditJSON[pageOut](t, d, base+"until="+ts(3), 200).ids(), seqIDs(1, 3))
	if p := auditJSON[pageOut](t, d, base+"since="+ts(10)+"&until="+ts(10), 200); len(p.Events) != 0 || !p.End {
		t.Fatalf("equal bounds: %+v", p)
	}
	// kinds, bounds and cursors combine: tool_result events in [5s, 25s),
	// two per page, walked back by before.
	q := base + "kinds=tool_result&since=" + ts(5) + "&until=" + ts(25) + "&n=2"
	var walked []string
	p = auditJSON[pageOut](t, d, q, 200)
	for {
		walked = append(p.ids(), walked...)
		if p.End {
			break
		}
		p = auditJSON[pageOut](t, d, q+"&before="+p.Before, 200)
	}
	sameIDs(t, "walked tool_result", walked, []string{"e8", "e11", "e14", "e17", "e20", "e23"})
	p = auditJSON[pageOut](t, d, q+"&after=e8", 200) // the cursor is any retained event ID
	sameIDs(t, "after with filters", p.ids(), []string{"e11", "e14"})
	p = auditJSON[pageOut](t, d, base+"kinds=hook,tool_call&n=4", 200)
	sameIDs(t, "two kinds", p.ids(), []string{"e25", "e27", "e28", "e30"})

	for _, bad := range []string{
		"since=yesterday", "until=2026-11-20", "since=" + url.QueryEscape("2026-11-20T18:00:00+09:00"),
		"since=" + ts(20) + "&until=" + ts(10),
	} {
		auditJSON[map[string]string](t, d, base+bad, 400)
	}
}

// TestAuditEventsPagesEqualNanosecondEventsWithoutGaps pins that event-ID
// cursors tell apart events of one nanosecond: more than 500 of them walk
// back and forward in full pages with no gap or duplicate.
func TestAuditEventsPagesEqualNanosecondEventsWithoutGaps(t *testing.T) {
	at := auditBase.Add(123456789)
	var evs []wsaudit.Envelope
	evs = append(evs, auditEv(1, wsaudit.KindHook, auditBase))
	for i := 2; i <= 1201; i++ {
		evs = append(evs, auditEv(i, wsaudit.KindToolCall, at))
	}
	evs = append(evs, auditEv(1202, wsaudit.KindHook, at.Add(time.Nanosecond)))
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: evs})
	d := auditDaemon(fx)
	q := "/s/sess/audit/events?n=500&since=" + url.QueryEscape(at.Format(time.RFC3339Nano)) + "&until=" + url.QueryEscape(at.Add(time.Nanosecond).Format(time.RFC3339Nano))
	var back []string
	pages := 0
	p := auditJSON[pageOut](t, d, q, 200)
	for {
		pages++
		back = append(p.ids(), back...)
		if p.End {
			break
		}
		p = auditJSON[pageOut](t, d, q+"&before="+p.Before, 200)
	}
	sameIDs(t, "walked back", back, seqIDs(2, 1201))
	if pages != 3 {
		t.Fatalf("walked %d pages", pages)
	}
	var fwd []string
	p = auditJSON[pageOut](t, d, q+"&after=e1", 200)
	for len(p.Events) > 0 {
		fwd = append(fwd, p.ids()...)
		p = auditJSON[pageOut](t, d, q+"&after="+p.After, 200)
	}
	sameIDs(t, "walked forward", fwd, seqIDs(2, 1201))
}

// TestAuditSummariesLinkCallsAndRequests pins attached, duration_ms, and the
// request fields: parallel calls of one request share request_id and its
// values, ctx_after is the immediately following request's (by prev_id),
// null after a text-only following request (captured without usage, or not
// captured at all while a later event has usage), and two successive
// requests with equal totals stay apart.
func TestAuditSummariesLinkCallsAndRequests(t *testing.T) {
	req := func(id, prev string, ctx any) map[string]any {
		m := map[string]any{"id": id}
		if prev != "" {
			m["prev_id"] = prev
		}
		if ctx != nil {
			m["ctx"] = ctx
		}
		return map[string]any{wsaudit.MetaRequest: m}
	}
	at := func(ms int) time.Time { return auditBase.Add(time.Duration(ms) * time.Millisecond) }
	ev := func(seq int, kind wsaudit.Kind, ms int, mod func(*wsaudit.Envelope)) wsaudit.Envelope {
		e := auditEv(seq, kind, at(ms))
		if mod != nil {
			mod(&e)
		}
		return e
	}
	evs := []wsaudit.Envelope{
		// Request r1 issues two parallel calls.
		ev(1, wsaudit.KindToolCall, 0, func(e *wsaudit.Envelope) {
			e.CallID, e.Subkind, e.In, e.Meta = "c1", "Bash", `{"command":"ls"}`, req("r1", "", int64(1000))
		}),
		ev(2, wsaudit.KindToolCall, 0, func(e *wsaudit.Envelope) { e.CallID, e.Meta = "c2", req("r1", "", int64(1000)) }),
		ev(3, wsaudit.KindSystem, 5, func(e *wsaudit.Envelope) { e.CallID, e.Subkind = "c1", "control_request:can_use_tool" }),
		ev(4, wsaudit.KindToolResult, 1500, func(e *wsaudit.Envelope) { e.CallID = "c1" }),
		ev(5, wsaudit.KindToolResult, 2250, func(e *wsaudit.Envelope) { e.CallID = "c2" }),
		ev(6, wsaudit.KindSystemReminder, 2300, nil),
		// r2 follows r1 (equal total to r3), and calls c3 (a subagent).
		ev(7, wsaudit.KindThinking, 3000, func(e *wsaudit.Envelope) { e.Meta = req("r2", "r1", float64(1500)) }),
		ev(8, wsaudit.KindToolCall, 3000, func(e *wsaudit.Envelope) { e.CallID, e.Subkind, e.Meta = "c3", "Task", req("r2", "r1", float64(1500)) }),
		ev(9, wsaudit.KindSubagent, 3100, func(e *wsaudit.Envelope) { e.ParentID, e.AgentID, e.Subkind = "c3", "task-1", "task_started" }),
		ev(10, wsaudit.KindSubagent, 3200, func(e *wsaudit.Envelope) { e.ParentID, e.CallID, e.Subkind = "c3", "sc1", "tool_use" }),
		ev(11, wsaudit.KindSubagent, 3300, func(e *wsaudit.Envelope) { e.ParentID, e.CallID, e.Subkind = "c3", "sc1", "tool_result" }),
		ev(12, wsaudit.KindToolResult, 4000, func(e *wsaudit.Envelope) { e.CallID = "c3" }),
		// r3 follows r2 with the same total; its call c4 is still running.
		ev(13, wsaudit.KindToolCall, 5000, func(e *wsaudit.Envelope) { e.CallID, e.Meta = "c4", req("r3", "r2", 1500) }),
		ev(14, wsaudit.KindToolResult, 6000, func(e *wsaudit.Envelope) { e.CallID = "c4" }),
		// r4 follows r3 as a text-only reply without usage, so c4's
		// ctx_after stays null although the later r5 has usage; c5 has no
		// request at all.
		ev(15, wsaudit.KindAssistantText, 7000, func(e *wsaudit.Envelope) { e.Meta = req("r4", "r3", nil) }),
		ev(16, wsaudit.KindToolCall, 8000, func(e *wsaudit.Envelope) { e.CallID = "c5" }),
		ev(17, wsaudit.KindToolCall, 9000, func(e *wsaudit.Envelope) { e.CallID, e.Meta = "c6", req("r5", "r4", int64(2000)) }),
		// r6 follows r5; r6's follower r7 is a text-only reply that was
		// never captured, and the next captured event (r8, after r7) has
		// usage: c7's ctx_after is still null, not r8's ctx.
		ev(18, wsaudit.KindToolCall, 10000, func(e *wsaudit.Envelope) { e.CallID, e.Meta = "c7", req("r6", "r5", int64(3000)) }),
		ev(19, wsaudit.KindToolCall, 11000, func(e *wsaudit.Envelope) { e.CallID, e.Meta = "c8", req("r8", "r7", int64(4000)) }),
		// An event naming r8 as prev_id without ctx settles r8's ctx_after
		// as null, though a later event with the same prev_id has usage.
		ev(20, wsaudit.KindAssistantText, 12000, func(e *wsaudit.Envelope) { e.Meta = req("r9", "r8", nil) }),
		ev(21, wsaudit.KindThinking, 12000, func(e *wsaudit.Envelope) { e.Meta = req("r9", "r8", int64(5000)) }),
	}
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: evs})
	d := auditDaemon(fx)
	p := auditJSON[pageOut](t, d, "/s/sess/audit/events", 200)
	byID := map[string]map[string]any{}
	for _, e := range p.Events {
		byID[e["id"].(string)] = e
	}
	num := func(v any) any {
		if f, ok := v.(float64); ok {
			return int64(f)
		}
		return v
	}
	check := func(id string, want map[string]any) {
		t.Helper()
		for k, w := range want {
			got, ok := byID[id][k]
			if !ok {
				t.Errorf("%s lacks %s", id, k)
				continue
			}
			if fmt.Sprint(num(got)) != fmt.Sprint(w) {
				t.Errorf("%s %s = %v, want %v", id, k, got, w)
			}
		}
	}
	null := "<nil>"
	check("e1", map[string]any{"duration_ms": 1500, "request_id": "r1", "ctx_before": 1000, "ctx_after": 1500, "attached": "[e3 e4]", "title": `{"command":"ls"}`})
	check("e2", map[string]any{"duration_ms": 2250, "request_id": "r1", "ctx_before": 1000, "ctx_after": 1500, "attached": "[e5]"})
	check("e8", map[string]any{"duration_ms": 1000, "request_id": "r2", "ctx_before": 1500, "ctx_after": 1500, "attached": "[e9 e10 e11 e12]"})
	check("e10", map[string]any{"attached": "[e11]"})
	check("e13", map[string]any{"duration_ms": 1000, "request_id": "r3", "ctx_before": 1500, "ctx_after": null})
	check("e16", map[string]any{"duration_ms": null, "request_id": null, "ctx_before": null, "ctx_after": null, "attached": "[]"})
	check("e17", map[string]any{"duration_ms": null, "request_id": "r5", "ctx_before": 2000, "ctx_after": 3000})
	check("e18", map[string]any{"request_id": "r6", "ctx_before": 3000, "ctx_after": null})
	check("e19", map[string]any{"request_id": "r8", "ctx_before": 4000, "ctx_after": null})
	for _, id := range []string{"e4", "e7", "e9", "e10", "e15"} {
		if _, ok := byID[id]["duration_ms"]; ok {
			t.Errorf("non-tool_call %s carries the call fields: %v", id, byID[id])
		}
	}
	// A call's result outside the page still sets its duration and links.
	p = auditJSON[pageOut](t, d, "/s/sess/audit/events?n=1&before=e2", 200)
	if fmt.Sprint(p.Events[0]["attached"]) != "[e3 e4]" || num(p.Events[0]["duration_ms"]) != int64(1500) {
		t.Fatalf("page of one call: %v", p.Events[0])
	}
	// Running: the result not yet captured.
	fx.set("sess", wsauditstore.View{Events: evs[:13]})
	p = auditJSON[pageOut](t, d, "/s/sess/audit/events?n=1", 200)
	if v, ok := p.Events[0]["duration_ms"]; !ok || v != nil {
		t.Fatalf("running call duration = %v (present %v)", v, ok)
	}
	// A page with no call ID or request to resolve: its tool_call still
	// carries the call fields, as null.
	fx.set("sess", wsauditstore.View{Events: []wsaudit.Envelope{auditEv(1, wsaudit.KindToolCall, auditBase)}})
	p = auditJSON[pageOut](t, d, "/s/sess/audit/events", 200)
	for _, k := range []string{"duration_ms", "request_id", "ctx_before", "ctx_after"} {
		if v, ok := p.Events[0][k]; !ok || v != nil {
			t.Fatalf("unlinked tool_call %s = %v (present %v)", k, v, ok)
		}
	}
	fx.set("sess", wsauditstore.View{Events: evs})

	// The call lookup joins by call ID only.
	type callOut struct {
		Call   map[string]any   `json:"call"`
		Events []map[string]any `json:"events"`
	}
	c := auditJSON[callOut](t, d, "/s/sess/audit/events?call=c3", 200)
	if c.Call["id"] != "e8" || len(c.Events) != 4 || c.Events[0]["id"] != "e9" || c.Events[3]["id"] != "e12" {
		t.Fatalf("call c3 = %+v", c)
	}
	c = auditJSON[callOut](t, d, "/s/sess/audit/events?call=sc1", 200)
	if c.Call["id"] != "e10" || len(c.Events) != 1 || c.Events[0]["id"] != "e11" {
		t.Fatalf("subagent call sc1 = %+v", c)
	}
	c = auditJSON[callOut](t, d, "/s/sess/audit/events?call=nope", 200)
	if c.Call != nil || len(c.Events) != 0 {
		t.Fatalf("unknown call = %+v", c)
	}
	fx.set("sess", wsauditstore.View{Events: evs[3:]}) // the call itself evicted
	c = auditJSON[callOut](t, d, "/s/sess/audit/events?call=c1", 200)
	if c.Call != nil || len(c.Events) != 1 || c.Events[0]["id"] != "e4" {
		t.Fatalf("call with its call event evicted = %+v", c)
	}
	for _, q := range []string{"call=", "call=c1&since=" + url.QueryEscape(auditBase.Format(time.RFC3339Nano)), "call=c1&before=e5", "call=c1&kinds=tool_call", "call=c1&n=5"} {
		auditJSON[map[string]string](t, d, "/s/sess/audit/events?"+q, 400)
	}
}

type rangeOut struct {
	Part   string `json:"part"`
	Offset int    `json:"offset"`
	Len    int    `json:"len"`
	Total  int    `json:"total"`
	Data   string `json:"data"`
	Next   int    `json:"next"`
	End    bool   `json:"end"`
}

// TestAuditEventServesTruncatedBodiesWithRangeFollowUps pins the full
// envelope's 8 MiB cap on each of in, out and raw, and the range follow-up:
// stored bytes as text on character boundaries, an offset inside a UTF-8
// sequence moved forward, len extended by up to 3, and refusals.
func TestAuditEventServesTruncatedBodiesWithRangeFollowUps(t *testing.T) {
	// "한" (3 bytes) straddles the 8 MiB mark, so the head stops before it.
	big := strings.Repeat("a", auditBodyMax-1) + "한" + strings.Repeat("b", 1000)
	call := auditEv(1, wsaudit.KindToolCall, auditBase)
	call.ID, call.In, call.Out = "holder:1/s7#0", big, big+"tail"
	call.Meta = map[string]any{"k": "v"}
	call.Redactions = []wsaudit.Redaction{{Field: "out", Offset: 3, Rule: "aws", Length: 20}}
	n := 4
	call.Seen = &wsaudit.SeenBounds{Out: &n}
	other := wsaudit.NewOtherEnvelope("sess", "claude", auditBase, big)
	other.ID, other.Seq = "o1", 2
	small := auditEv(3, wsaudit.KindHook, auditBase)
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: []wsaudit.Envelope{call, other, small}})
	d := auditDaemon(fx)
	path := "/s/sess/audit/event/" + url.PathEscape(call.ID)

	type envOut struct {
		ID         string                    `json:"id"`
		In         string                    `json:"in"`
		Out        string                    `json:"out"`
		Raw        string                    `json:"raw"`
		Meta       map[string]any            `json:"meta"`
		Redactions []wsaudit.Redaction       `json:"redactions"`
		Seen       *wsaudit.SeenBounds       `json:"seen"`
		Truncated  bool                      `json:"truncated"`
		Range      map[string]map[string]int `json:"range"`
		InBytes    int                       `json:"in_bytes"`
		OutBytes   int                       `json:"out_bytes"`
	}
	e := auditJSON[envOut](t, d, path, 200)
	if e.ID != call.ID || !e.Truncated || len(e.In) != auditBodyMax-1 || len(e.Out) != auditBodyMax-1 || e.Meta["k"] != "v" || len(e.Redactions) != 1 || e.Seen == nil || *e.Seen.Out != 4 {
		t.Fatalf("envelope: id %q truncated %v in %d out %d meta %v", e.ID, e.Truncated, len(e.In), len(e.Out), e.Meta)
	}
	if e.Range["in"]["offset"] != auditBodyMax-1 || e.Range["in"]["total"] != len(big) || e.Range["out"]["total"] != len(big)+4 || e.InBytes != len(big) {
		t.Fatalf("range = %v", e.Range)
	}
	o := auditJSON[envOut](t, d, "/s/sess/audit/event/o1", 200)
	if !o.Truncated || len(o.Raw) != auditBodyMax-1 || o.Range["raw"]["total"] != len(big) {
		t.Fatalf("raw envelope: truncated %v raw %d range %v", o.Truncated, len(o.Raw), o.Range)
	}
	if s := auditJSON[envOut](t, d, "/s/sess/audit/event/e3", 200); s.Truncated || s.Range != nil || s.Out != "output 3" {
		t.Fatalf("small envelope = %+v", s)
	}

	// Follow each part's range to the end and rebuild the stored body.
	for _, c := range []struct {
		target, part, want string
	}{{path, "in", big}, {path, "out", big + "tail"}, {"/s/sess/audit/event/o1", "raw", big}} {
		got := ""
		offset := 0
		for {
			r := auditJSON[rangeOut](t, d, fmt.Sprintf("%s?part=%s&offset=%d&len=%d", c.target, c.part, offset, 3<<20), 200)
			if r.Part != c.part || r.Offset != offset || r.Total != len(c.want) || r.Next != r.Offset+r.Len || r.Len != len(r.Data) {
				t.Fatalf("%s range at %d = part %s offset %d len %d total %d next %d", c.part, offset, r.Part, r.Offset, r.Len, r.Total, r.Next)
			}
			got += r.Data
			offset = r.Next
			if r.End {
				break
			}
		}
		if got != c.want {
			t.Fatalf("%s rebuilt %d bytes, want %d", c.part, len(got), len(c.want))
		}
	}
	// Inside "한": the offset moves forward past it; a slice ending inside
	// it extends to its end.
	r := auditJSON[rangeOut](t, d, fmt.Sprintf("%s?part=in&offset=%d&len=4", path, auditBodyMax), 200)
	if r.Offset != auditBodyMax+2 || r.Data != "bbbb" {
		t.Fatalf("offset inside a sequence: %+v", r)
	}
	r = auditJSON[rangeOut](t, d, fmt.Sprintf("%s?part=in&offset=%d&len=1", path, auditBodyMax-1), 200)
	if r.Len != 3 || r.Data != "한" || r.Next != auditBodyMax+2 {
		t.Fatalf("len extended to the boundary: %+v", r)
	}
	// Defaults: offset 0, len 1 MiB; len clamps to 8 MiB (+3).
	r = auditJSON[rangeOut](t, d, path+"?part=in", 200)
	if r.Offset != 0 || r.Len != auditRangeDefault || r.End {
		t.Fatalf("default range = offset %d len %d", r.Offset, r.Len)
	}
	r = auditJSON[rangeOut](t, d, path+"?part=in&len=99999999", 200)
	if r.Len != auditBodyMax+2 {
		t.Fatalf("clamped len = %d", r.Len)
	}
	r = auditJSON[rangeOut](t, d, fmt.Sprintf("%s?part=in&offset=%d", path, len(big)), 200)
	if r.Len != 0 || !r.End || r.Next != len(big) {
		t.Fatalf("offset at total = %+v", r)
	}
	for _, q := range []string{
		"part=in&len=0", fmt.Sprintf("part=in&offset=%d", len(big)+1), "part=raw", "part=bogus",
		"part=in&offset=-1", "part=in&offset=x", "part=in&len=x", "offset=5", "len=5",
	} {
		auditJSON[map[string]string](t, d, path+"?"+q, 400)
	}
	auditJSON[map[string]string](t, d, "/s/sess/audit/event/e3?part=in", 400) // a body the event lacks
	auditJSON[map[string]string](t, d, "/s/sess/audit/event/nope", 404)
	auditJSON[map[string]string](t, d, "/s/sess/audit/event/nope?part=out", 404)
}

// TestAuditStoredTruncBodyKeepsItsMarkerAndMiddleOut pins the store-level
// truncation through the API: stored_trunc returned unchanged, summary
// lengths and range offsets in stored bytes, and search never finding the
// omitted middle, nor a match glued across the seam, while line numbers
// count the stored body's lines.
func TestAuditStoredTruncBodyKeepsItsMarkerAndMiddleOut(t *testing.T) {
	head, tail := "line one\nhead ends alpha", "beta tail starts\nlast line"
	ev := auditEv(1, wsaudit.KindToolResult, auditBase)
	ev.Out = head + tail // the store keeps head and tail; MIDDLE was omitted
	ev.StoredTrunc = &wsaudit.StoredTrunc{Out: &wsaudit.BodyTrunc{OriginalBytes: len(head) + 1000 + len(tail), HeadBytes: len(head), TailBytes: len(tail)}}
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: []wsaudit.Envelope{ev}})
	d := auditDaemon(fx)

	p := auditJSON[pageOut](t, d, "/s/sess/audit/events", 200)
	if int(p.Events[0]["out_bytes"].(float64)) != len(ev.Out) {
		t.Fatalf("out_bytes = %v, want the stored %d", p.Events[0]["out_bytes"], len(ev.Out))
	}
	type envOut struct {
		StoredTrunc wsaudit.StoredTrunc `json:"stored_trunc"`
		Out         string              `json:"out"`
		Truncated   bool                `json:"truncated"`
	}
	env := auditJSON[envOut](t, d, "/s/sess/audit/event/e1", 200)
	if env.StoredTrunc.Out == nil || *env.StoredTrunc.Out != *ev.StoredTrunc.Out || env.Out != ev.Out || env.Truncated {
		t.Fatalf("envelope = %+v", env)
	}
	r := auditJSON[rangeOut](t, d, fmt.Sprintf("/s/sess/audit/event/e1?part=out&offset=%d", len(head)), 200)
	if r.Data != tail || r.Total != len(ev.Out) || !r.End {
		t.Fatalf("range past the seam = %+v", r)
	}
	auditJSON[map[string]string](t, d, fmt.Sprintf("/s/sess/audit/event/e1?part=out&offset=%d", len(ev.Out)+1), 400)

	type searchOut struct {
		Hits []map[string]any `json:"hits"`
		Next *string          `json:"next"`
	}
	// Nor do zero-width assertions answer at the seam as if it were a line
	// end or a word boundary: "alphabeta" is one word of one stored line.
	for _, q := range []string{"MIDDLE", "alphabeta", "alpha.*beta", `alpha$`, `^beta`, `\bbeta`, `alpha\b`, `alpha\z`, `\Abeta`, `^head ends alpha$`, `^beta tail starts$`} {
		if s := auditJSON[searchOut](t, d, "/s/sess/audit/search?regex=1&q="+url.QueryEscape(q), 200); len(s.Hits) != 0 {
			t.Errorf("search %q found %v", q, s.Hits)
		}
	}
	// They still answer at the stored line's real boundaries, on both sides
	// of the seam, and a match hidden inside a crossing one is found — also
	// one ending or starting at the seam, whose assertions see the other
	// side's neighbour ("alpha\B": a word goes on past the seam).
	for _, q := range []string{`^head ends`, `starts$`, `\bends alpha`, `\bbeta\B|\btail\b`, `head|alpha.*tail`,
		`^head.*`, `.*starts$`, `alpha\B`, `\Bbeta tail`} {
		s := auditJSON[searchOut](t, d, "/s/sess/audit/search?regex=1&q="+url.QueryEscape(q), 200)
		if len(s.Hits) != 1 || fmt.Sprint(s.Hits[0]["line_no"]) != "2" {
			t.Errorf("search %q = %v, want one hit on line 2", q, s.Hits)
		}
	}
	// The stored body's lines are "line one", "head ends alphabeta tail
	// starts" (the seam inside it), "last line": a hit on either side of the
	// seam is line 2, shown as the stored line, and the line after it is 3.
	for q, want := range map[string]string{"alpha": "2", "beta": "2", "last": "3"} {
		s := auditJSON[searchOut](t, d, "/s/sess/audit/search?q="+q, 200)
		if len(s.Hits) != 1 || fmt.Sprint(s.Hits[0]["line_no"]) != want {
			t.Fatalf("search %s = %v, want line %s", q, s.Hits, want)
		}
		if want == "2" && s.Hits[0]["line"] != "head ends alphabeta tail starts" {
			t.Fatalf("seam line hit = %v", s.Hits[0])
		}
	}
}

// TestAuditSeamSearchAllocationsDoNotGrowWithMatches pins that matching a
// pattern holding an assertion on a stored line with a seam allocates a
// bounded amount however many matches the line holds: "^|." matches at every
// byte of a 1 MiB line, and one hit is all the line is matched for.
func TestAuditSeamSearchAllocationsDoNotGrowWithMatches(t *testing.T) {
	s, err := newAuditSearch(`^|.`, true, true, nil, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("a", 1<<20)
	seam := len(line) / 2
	if start, end, ok := s.matchLine(line, seam); !ok || start != 0 || end != 0 {
		t.Fatalf("seam match = %d, %d, %v; want the empty match at the line start", start, end, ok)
	}
	allocs := testing.AllocsPerRun(3, func() { s.matchLine(line, seam) })
	if allocs > 16 {
		t.Fatalf("matching a 1 MiB seam line made %.0f allocations", allocs)
	}
}

// TestAuditSearchHitsPagesAndRefusals pins search: newest event first, each
// hit's field, line number, line, call and parent IDs; literal and regex,
// case folding, kinds; cursor paging to a null next; and refusals.
func TestAuditSearchHitsPagesAndRefusals(t *testing.T) {
	var evs []wsaudit.Envelope
	for i := 1; i <= 6; i++ {
		e := auditEv(i, wsaudit.KindToolResult, auditBase.Add(time.Duration(i)*time.Second))
		e.CallID, e.ParentID = fmt.Sprintf("c%d", i), "task"
		e.In = "needle in input"
		e.Out = fmt.Sprintf("first\r\nNeedle at two %d\nthree\nneedle four\n", i)
		evs = append(evs, e)
	}
	hook := auditEv(7, wsaudit.KindHook, auditBase.Add(10*time.Second))
	hook.Out = "a hook needle"
	evs = append(evs, hook)
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: evs})
	d := auditDaemon(fx)
	type searchOut struct {
		Hits []map[string]any `json:"hits"`
		Next *string          `json:"next"`
	}
	s := auditJSON[searchOut](t, d, "/s/sess/audit/search?q=needle&n=4", 200)
	want := []string{"e7 out 1", "e6 in 1", "e6 out 2", "e6 out 4"}
	for i, h := range s.Hits {
		if got := fmt.Sprintf("%s %s %v", h["event_id"], h["field"], h["line_no"]); got != want[i] {
			t.Fatalf("hit %d = %s, want %s", i, got, want[i])
		}
	}
	if h := s.Hits[2]; h["line"] != "Needle at two 6" || h["call_id"] != "c6" || h["parent_id"] != "task" || h["kind"] != "tool_result" {
		t.Fatalf("hit = %v", h)
	}
	// Follow the cursor to the end: every hit once, in order.
	total := len(s.Hits)
	for s.Next != nil {
		s = auditJSON[searchOut](t, d, "/s/sess/audit/search?q=needle&n=4&cursor="+url.QueryEscape(*s.Next), 200)
		total += len(s.Hits)
	}
	if total != 1+6*3 {
		t.Fatalf("paged %d hits", total)
	}
	// A page ending on the oldest hit has a null next.
	if s := auditJSON[searchOut](t, d, "/s/sess/audit/search?q=needle&n=19", 200); len(s.Hits) != 19 || s.Next != nil {
		t.Fatalf("page ending on the oldest hit: %d hits, next %v", len(s.Hits), s.Next)
	}
	if s := auditJSON[searchOut](t, d, "/s/sess/audit/search?q=needle&n=18", 200); len(s.Hits) != 18 || s.Next == nil {
		t.Fatalf("page before the oldest hit: %d hits, next %v", len(s.Hits), s.Next)
	} else if last := auditJSON[searchOut](t, d, "/s/sess/audit/search?q=needle&n=18&cursor="+url.QueryEscape(*s.Next), 200); len(last.Hits) != 1 || last.Hits[0]["event_id"] != "e1" || fmt.Sprint(last.Hits[0]["line_no"]) != "4" || last.Next != nil {
		t.Fatalf("last page = %v", last)
	}
	if s := auditJSON[searchOut](t, d, "/s/sess/audit/search?q=Needle&case=1", 200); len(s.Hits) != 6 || s.Next != nil {
		t.Fatalf("case-sensitive: %d hits", len(s.Hits))
	}
	if s := auditJSON[searchOut](t, d, "/s/sess/audit/search?q=two+%5B0-9%5D%24&regex=1", 200); len(s.Hits) != 6 {
		t.Fatalf("regex: %d hits", len(s.Hits))
	}
	if s := auditJSON[searchOut](t, d, "/s/sess/audit/search?q=needle&kinds=hook", 200); len(s.Hits) != 1 || s.Hits[0]["event_id"] != "e7" {
		t.Fatalf("kinds: %v", s.Hits)
	}
	if s := auditJSON[searchOut](t, d, "/s/sess/audit/search?q=a.b", 200); len(s.Hits) != 0 {
		t.Fatalf("a literal is not a pattern: %v", s.Hits)
	}
	// A long line comes back as a clipped excerpt around its match.
	long := auditEv(8, wsaudit.KindToolResult, auditBase.Add(time.Minute))
	long.Out = strings.Repeat("x", 5000) + "FIND" + strings.Repeat("y", 5000)
	fx.set("long", wsauditstore.View{Events: []wsaudit.Envelope{long}})
	s = auditJSON[searchOut](t, d, "/s/long/audit/search?q=FIND", 200)
	if line := s.Hits[0]["line"].(string); len(line) > auditLineMax || !strings.Contains(line, "FIND") || s.Hits[0]["clipped"] != true {
		t.Fatalf("long line hit = %d bytes, clipped %v", len(line), s.Hits[0]["clipped"])
	}

	first := auditJSON[searchOut](t, d, "/s/sess/audit/search?q=needle&n=1", 200)
	for _, q := range []string{
		"q=", "regex=1", "q=x&regex=2", "q=x&case=yes", "q=%28&regex=1", "q=x&kinds=bogus", "q=x&n=0",
		"q=x&cursor=%21%21", "q=other&n=1&cursor=" + url.QueryEscape(*first.Next), "q=needle&n=1&case=1&cursor=" + url.QueryEscape(*first.Next),
	} {
		auditJSON[map[string]string](t, d, "/s/sess/audit/search?"+q, 400)
	}
}

// TestAuditStaleCursorsAndEmptySessions pins Decision 6 and Decision 5: an
// evicted event or search cursor answers 410 stale_cursor while the session
// still has audit data; a session without audit data answers empty results.
func TestAuditStaleCursorsAndEmptySessions(t *testing.T) {
	evs := auditSeries(10)
	for i := range evs {
		evs[i].Out = "match here"
	}
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: evs})
	d := auditDaemon(fx)
	type searchOut struct {
		Hits []map[string]any `json:"hits"`
		Next *string          `json:"next"`
	}
	s := auditJSON[searchOut](t, d, "/s/sess/audit/search?q=match&n=8", 200)
	if s.Next == nil {
		t.Fatal("no next cursor")
	}
	fx.set("sess", wsauditstore.View{Events: evs[5:], Cut: true}) // e1..e5 evicted
	for _, target := range []string{
		"/s/sess/audit/events?before=e2", "/s/sess/audit/events?after=e3", "/s/sess/audit/events?before=never-existed",
		"/s/sess/audit/search?q=match&n=8&cursor=" + url.QueryEscape(*s.Next),
	} {
		rec := auditGet(t, d, http.MethodGet, target)
		if rec.Code != http.StatusGone || strings.TrimSpace(rec.Body.String()) != `{"error":"stale_cursor"}` {
			t.Fatalf("GET %s = %d %s", target, rec.Code, rec.Body.String())
		}
	}
	// The same cursors on a session with no audit data: empty, not errors.
	fx.set("sess", wsauditstore.View{Cut: true})
	for _, target := range []string{"/s/sess/audit/events?before=e2", "/s/sess/audit/events", "/s/nobody/audit/events?after=x"} {
		p := auditJSON[pageOut](t, d, target, 200)
		if len(p.Events) != 0 || p.Before != "" || p.After != "" || !p.End {
			t.Fatalf("GET %s = %+v", target, p)
		}
	}
	if p := auditJSON[pageOut](t, d, "/s/sess/audit/events", 200); !p.Cut {
		t.Fatal("an evicted-to-empty ring still reports its cut")
	}
	for _, target := range []string{"/s/nobody/audit/search?q=x", "/s/sess/audit/search?q=match&n=8&cursor=" + url.QueryEscape(*s.Next)} {
		if s := auditJSON[searchOut](t, d, target, 200); len(s.Hits) != 0 || s.Next != nil {
			t.Fatalf("GET %s = %+v", target, s)
		}
	}
	auditJSON[map[string]any](t, d, "/s/nobody/audit/events?call=c1", 200)
	auditJSON[map[string]string](t, d, "/s/nobody/audit/event/e1", 404)
}

// TestAuditRoutesAreReadOnly pins Decision 5: every audit route answers
// reads only, so no route deletes, purges or otherwise writes; a malformed
// session ID or an unknown audit path is not found; an unavailable store is
// 503.
func TestAuditRoutesAreReadOnly(t *testing.T) {
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: auditSeries(3)})
	d := auditDaemon(fx)
	routes := []string{"/s/sess/audit/events", "/s/sess/audit/event/e1", "/s/sess/audit/search?q=x", "/s/sess/audit/events?call=c1", "/s/sess/audit/event/e1?part=out"}
	for _, target := range routes {
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if rec := auditGet(t, d, m, target); rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405", m, target, rec.Code)
			}
		}
		if rec := auditGet(t, d, http.MethodHead, target); rec.Code != http.StatusOK {
			t.Errorf("HEAD %s = %d", target, rec.Code)
		}
		if rec := auditGet(t, d, http.MethodGet, target); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("GET %s = %d %v", target, rec.Code, rec.Header())
		}
	}
	for _, target := range []string{"/s/sess/audit/purge", "/s/sess/audit/", "/s/sess/audit/event/", "/s/bad%20id/audit/events"} {
		if rec := auditGet(t, d, http.MethodGet, target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rec.Code)
		}
	}
	if rec := auditGet(t, &Daemon{log: io.Discard}, http.MethodGet, "/s/sess/audit/events"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no store: %d", rec.Code)
	}
}

// TestAuditSearchBudgetStopsAtTheFirstUnexaminedPosition pins the scan
// budget's continuation: an empty remaining field is not a reason to stop, so
// a page ending on the oldest hit has a null next even at the budget, and a
// budget stop after the n-th hit resumes where the lookahead stopped, not
// after the n-th hit.
func TestAuditSearchBudgetStopsAtTheFirstUnexaminedPosition(t *testing.T) {
	orig := auditSearchBudget
	t.Cleanup(func() { auditSearchBudget = orig })
	auditSearchBudget = 1
	s, err := newAuditSearch("needle", false, false, nil, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	none := func() bool { return false }
	ev := func(seq int, in string) wsaudit.Envelope {
		e := auditEv(seq, wsaudit.KindToolCall, auditBase)
		e.In, e.Out = in, ""
		return e
	}
	// The oldest event's input holds the n-th hit; its output is empty.
	ans, err := s.run([]wsaudit.Envelope{ev(1, "needle one")}, nil, none)
	if err != nil || len(ans.Hits) != 1 || ans.Next != nil {
		t.Fatalf("page ending on the oldest hit at the budget: %d hits, next %v, err %v", len(ans.Hits), ans.Next, err)
	}
	// e3 holds the n-th hit; the lookahead reaches the budget before e2.
	evs := []wsaudit.Envelope{ev(1, "needle three"), ev(2, "no hit here"), ev(3, "needle one")}
	ans, err = s.run(evs, nil, none)
	if err != nil || len(ans.Hits) != 1 || ans.Next == nil {
		t.Fatalf("first page: %d hits, next %v, err %v", len(ans.Hits), ans.Next, err)
	}
	p, err := decodeSearchCursor(*ans.Next, s.query)
	if err != nil || p.Seq != 2 || p.Field != 0 || p.Line != 1 {
		t.Fatalf("budget cursor = %+v (%v), want the start of e2", p, err)
	}
	var got []string
	for ans.Next != nil {
		p, _ := decodeSearchCursor(*ans.Next, s.query)
		if ans, err = s.run(evs, &p, none); err != nil {
			t.Fatal(err)
		}
		for _, h := range ans.Hits {
			got = append(got, h.EventID)
		}
	}
	if fmt.Sprint(got) != "[e1]" {
		t.Fatalf("following the budget cursors found %v, want [e1]", got)
	}
}

// TestAuditIDsRoundTripVerbatim pins that IDs are opaque: an event and call
// ID the store kept verbatim with a control character (wsaudit.NormalizeID
// keeps short values as given) is accepted back as an event path, a cursor
// and a call key.
func TestAuditIDsRoundTripVerbatim(t *testing.T) {
	call := auditEv(1, wsaudit.KindToolCall, auditBase)
	call.ID, call.CallID = "ev\x01one", "call\x02one"
	result := auditEv(2, wsaudit.KindToolResult, auditBase.Add(time.Second))
	result.CallID = call.CallID
	if wsaudit.NormalizeID(call.ID) != call.ID || wsaudit.NormalizeID(call.CallID) != call.CallID {
		t.Fatal("fixture: the IDs are not kept verbatim")
	}
	fx := &fixtureAudit{}
	fx.set("sess", wsauditstore.View{Events: []wsaudit.Envelope{call, result}})
	d := auditDaemon(fx)
	p := auditJSON[pageOut](t, d, "/s/sess/audit/events", 200)
	if p.Before != call.ID || p.Events[0]["call_id"] != call.CallID {
		t.Fatalf("page = %+v", p)
	}
	type envOut struct {
		ID string `json:"id"`
	}
	if e := auditJSON[envOut](t, d, "/s/sess/audit/event/"+url.PathEscape(p.Before), 200); e.ID != call.ID {
		t.Fatalf("event = %q", e.ID)
	}
	if n := auditJSON[pageOut](t, d, "/s/sess/audit/events?after="+url.QueryEscape(p.Before), 200); len(n.Events) != 1 || n.Events[0]["id"] != "e2" {
		t.Fatalf("after the ID: %+v", n)
	}
	type callOut struct {
		Call   map[string]any   `json:"call"`
		Events []map[string]any `json:"events"`
	}
	if c := auditJSON[callOut](t, d, "/s/sess/audit/events?call="+url.QueryEscape(call.CallID), 200); c.Call["id"] != call.ID || len(c.Events) != 1 {
		t.Fatalf("call lookup = %+v", c)
	}
}
