package wsdashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wsproctree"
)

// fakeProcTable is a process table whose CPU times advance per snapshot.
type fakeProcTable struct {
	procs []wsproctree.Proc
	asks  [][]int
	err   error
}

func (f *fakeProcTable) snap(roots []int) ([]wsproctree.Proc, error) {
	f.asks = append(f.asks, append([]int(nil), roots...))
	if f.err != nil {
		return nil, f.err
	}
	var kept []wsproctree.Proc
	in := map[int]bool{}
	for _, r := range roots {
		for _, p := range wsproctree.Descendants(f.procs, r) {
			in[p.PID] = true
		}
	}
	for _, p := range f.procs {
		if in[p.PID] {
			kept = append(kept, p)
		}
	}
	return kept, nil
}

// advance adds cpu to pid's CPU time.
func (f *fakeProcTable) advance(pid int, cpu time.Duration) {
	for i := range f.procs {
		if f.procs[i].PID == pid {
			f.procs[i].CPU += cpu
		}
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func traceFixture() (*holderTrace, *fakeProcTable, *fakeClock, []wsholder.Info) {
	table := &fakeProcTable{procs: []wsproctree.Proc{
		{PID: 1, PPID: 0, Name: "init"},
		{PID: 4300, PPID: 1, Name: "holder", RSS: 10 << 20},
		{PID: 4301, PPID: 4300, Name: "claude", RSS: 200 << 20},
		{PID: 4302, PPID: 4301, Name: "go test", RSS: 90 << 20},
		{PID: 4400, PPID: 1, Name: "holder", RSS: 8 << 20},
		{PID: 9000, PPID: 1, Name: "unrelated", RSS: 1 << 30},
	}}
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	list := []wsholder.Info{
		{ID: "hlive", PID: 4300, Attached: true, Child: &wsholder.ChildStatus{State: "running", PID: 4301}},
		{ID: "hlost", PID: 4400},
		{ID: "hgone", PID: 4500},
		{ID: "durable.x", Attached: true},
	}
	return &holderTrace{snap: table.snap, now: clock.now}, table, clock, list
}

// TestHolderTraceAggregatesSubtrees pins one holder's numbers: resident
// memory and CPU% summed over its OS subtree, the descendants listed
// without the holder process, CPU% from the CPU time between two
// snapshots, and no entry for a holder whose process is gone or that has
// none (durable).
func TestHolderTraceAggregatesSubtrees(t *testing.T) {
	trace, table, clock, list := traceFixture()
	first := trace.sample(list)
	if !reflect.DeepEqual(table.asks, [][]int{{4300, 4301, 4400, 4500}}) {
		t.Fatalf("roots asked = %v", table.asks)
	}
	if first.IntervalMS != 1500 || first.TS != clock.t.UnixMilli() {
		t.Errorf("interval/ts = %d/%d", first.IntervalMS, first.TS)
	}
	if len(first.Holders) != 2 {
		t.Fatalf("holders = %v, want hlive and hlost", first.Holders)
	}
	live := first.Holders["hlive"]
	if live.PID != 4300 || live.RSS != 300<<20 || live.CPU != 0 {
		t.Errorf("first hlive = %+v, want the subtree's 300 MiB at 0%% (no earlier snapshot)", live)
	}
	if got := []int{live.Procs[0].PID, live.Procs[1].PID}; len(live.Procs) != 2 || !reflect.DeepEqual(got, []int{4301, 4302}) || live.Procs[1].PPID != 4301 || live.Procs[1].Name != "go test" {
		t.Errorf("hlive procs = %+v", live.Procs)
	}
	if lost := first.Holders["hlost"]; lost.RSS != 8<<20 || len(lost.Procs) != 0 || lost.Procs == nil {
		t.Errorf("hlost = %+v, want its own memory and an empty (not null) process list", lost)
	}

	clock.t = clock.t.Add(2 * time.Second)
	table.advance(4301, time.Second)          // 50%
	table.advance(4302, 500*time.Millisecond) // 25%
	second := trace.sample(list)
	live = second.Holders["hlive"]
	if live.CPU != 75 || live.Procs[0].CPU != 50 || live.Procs[1].CPU != 25 {
		t.Errorf("second hlive cpu = %v (%v, %v), want 75 (50, 25)", live.CPU, live.Procs[0].CPU, live.Procs[1].CPU)
	}
	if !reflect.DeepEqual(live.Spark.CPU, []float64{0, 75}) || !reflect.DeepEqual(live.Spark.RSS, []uint64{300 << 20, 300 << 20}) {
		t.Errorf("hlive spark = %+v", live.Spark)
	}

	// A process whose CPU time went back (a reused pid) reads 0%, not less.
	clock.t = clock.t.Add(2 * time.Second)
	table.advance(4302, -time.Second)
	if third := trace.sample(list); third.Holders["hlive"].Procs[1].CPU != 0 {
		t.Errorf("reused pid cpu = %v, want 0", third.Holders["hlive"].Procs[1].CPU)
	}
}

// TestHolderTraceRingReuseAndIdleExpiry pins the ring: 24 points per
// holder, an ask within a second answered from the last snapshot without
// another, the history dropped after 30s without an ask, a holder's
// history dropped once it leaves the listing, and no snapshot without a
// holder process.
func TestHolderTraceRingReuseAndIdleExpiry(t *testing.T) {
	trace, table, clock, list := traceFixture()
	for i := 0; i < 30; i++ {
		trace.sample(list)
		clock.t = clock.t.Add(1500 * time.Millisecond)
	}
	ans := trace.sample(list)
	if n := len(ans.Holders["hlive"].Spark.RSS); n != 24 {
		t.Fatalf("history length = %d, want 24", n)
	}
	asks := len(table.asks)
	clock.t = clock.t.Add(500 * time.Millisecond)
	if again := trace.sample(list); len(table.asks) != asks || again.TS != ans.TS {
		t.Errorf("an ask within a second took a snapshot (%d -> %d)", asks, len(table.asks))
	}
	clock.t = clock.t.Add(31 * time.Second)
	if fresh := trace.sample(list); len(fresh.Holders["hlive"].Spark.RSS) != 1 {
		t.Errorf("history after 31s idle = %d points, want 1", len(fresh.Holders["hlive"].Spark.RSS))
	}
	clock.t = clock.t.Add(2 * time.Second)
	trace.sample(list[1:])
	clock.t = clock.t.Add(2 * time.Second)
	if back := trace.sample(list); len(back.Holders["hlive"].Spark.RSS) != 1 {
		t.Errorf("a holder that left and came back kept %d points", len(back.Holders["hlive"].Spark.RSS))
	}
	asks = len(table.asks)
	clock.t = clock.t.Add(2 * time.Second)
	if none := trace.sample(list[3:]); len(none.Holders) != 0 {
		t.Errorf("durable-only answer = %v", none.Holders)
	}
	if len(table.asks) != asks {
		t.Errorf("a listing without a holder process took a snapshot: %v", table.asks[asks:])
	}

	// A collector failure answers unsupported with no holders.
	table.err = errors.New("ps failed")
	clock.t = clock.t.Add(2 * time.Second)
	if failed := trace.sample(list); failed.Supported || len(failed.Holders) != 0 {
		t.Errorf("failed sample = %+v, want unsupported and empty", failed)
	}
}

// TestHolderMetricsRoute pins GET /holders/metrics: a local viewer's read
// with no-store and nosniff, GET only, 404 for a LAN viewer (token or not)
// as for the stop route, and an answer in the documented shape.
func TestHolderMetricsRoute(t *testing.T) {
	holders := newFakeHolders()
	holders.listing = holderFixtureListing()
	f := startPageFixture(t, lanMode, withHolders(holders))
	table := &fakeProcTable{procs: []wsproctree.Proc{{PID: 4300, PPID: 1, Name: "holder", RSS: 4096}, {PID: 4301, PPID: 4300, Name: "sh", RSS: 8192}}}
	f.d.holderTrace.snap = table.snap
	port := strconv.Itoa(f.d.Endpoint().Port)
	local := viewer{peer: "127.0.0.1:50000", host: "127.0.0.1:" + port, localCookie: true}

	rec := serve(f.d, local, http.MethodGet, "/holders/metrics", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /holders/metrics = %d (%s)", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	for k, v := range map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Content-Type": "application/json"} {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ts", "interval_ms", "supported", "holders"} {
		if wire[k] == nil {
			t.Errorf("answer lacks %q: %s", k, rec.Body.String())
		}
	}
	var ans holderMetrics
	_ = json.Unmarshal(rec.Body.Bytes(), &ans)
	if h := ans.Holders["hlive"]; h.PID != 4300 || h.RSS != 12288 || len(h.Procs) != 1 || h.Procs[0].Name != "sh" {
		t.Errorf("hlive = %+v", h)
	}
	if !strings.Contains(rec.Body.String(), `"spark":{"rss":[12288],"cpu":[0]}`) || !strings.Contains(rec.Body.String(), `"ppid":4300`) {
		t.Errorf("wire shape: %s", rec.Body.String())
	}
	if rec := serve(f.d, local, http.MethodPost, "/holders/metrics", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /holders/metrics = %d, want 405", rec.Code)
	}
	token := readLANToken(t, f.dir)
	lanHost := "192.168.7.20:" + port
	for _, v := range []viewer{
		{name: "foreign peer with the token", peer: "192.168.7.30:50000", host: lanHost, cookie: token},
		{name: "loopback peer, foreign Host, token", peer: "127.0.0.1:50000", host: lanHost, cookie: token},
	} {
		if rec := serve(f.d, v, http.MethodGet, "/holders/metrics", ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: GET /holders/metrics = %d, want 404", v.name, rec.Code)
		}
	}
	if len(table.asks) != 1 {
		t.Errorf("snapshots = %d, want only the local read's", len(table.asks))
	}
	// "metrics" is not a holder id: its stop finds no route.
	if rec := postFrom(f.d, local, "/holders/metrics/stop", "http://"+local.host); rec.Code != http.StatusNotFound {
		t.Errorf("POST /holders/metrics/stop = %d, want 404", rec.Code)
	}
}

// parentAgentsStub is a durable agent host that also knows its children's
// parents.
type parentAgentsStub struct {
	durableAgentsStub
	parents map[string]string
	asked   []string
}

func (s *parentAgentsStub) AgentParents(ids []string) map[string]string {
	s.asked = append(s.asked, ids...)
	out := map[string]string{}
	for _, id := range ids {
		if p := s.parents[id]; p != "" {
			out[id] = p
		}
	}
	return out
}

// TestHolderListingNamesLiveParents pins the parents fallback: a holder
// whose descriptor has a reply id but no parent_reply_id gets the agent
// host's parent by holder id; one that names its parent, or has no reply
// id, is not looked up; without a parent-aware host the field is absent.
func TestHolderListingNamesLiveParents(t *testing.T) {
	holders := newFakeHolders()
	holders.listing = wsholder.Listing{Holders: []wsholder.Info{
		{ID: "hlead", PID: 10, Attached: true, Session: json.RawMessage(`{"kind":"lead","reply_id":"lead1"}`)},
		{ID: "hold", PID: 11, Attached: true, Session: json.RawMessage(`{"kind":"agent-worker","reply_id":"w1"}`)},
		{ID: "hnew", PID: 12, Attached: true, Session: json.RawMessage(`{"kind":"agent-worker","reply_id":"w2","parent_reply_id":"lead1"}`)},
		{ID: "hexec", PID: 13, Attached: true, Session: json.RawMessage(`{"kind":"exec"}`)},
	}}
	agents := &parentAgentsStub{parents: map[string]string{"w1": "lead1", "w2": "lead1"}}
	f := startPageFixture(t, withHolders(holders), func(o *DaemonOptions) { o.Agents = agents })
	local := viewer{peer: "127.0.0.1:50000", host: "127.0.0.1:" + strconv.Itoa(f.d.Endpoint().Port), localCookie: true}
	list := decodeHolderList(t, serve(f.d, local, http.MethodGet, "/holders", ""))
	if !reflect.DeepEqual(list.Parents, map[string]string{"hold": "lead1"}) {
		t.Errorf("parents = %v, want hold -> lead1", list.Parents)
	}
	if !reflect.DeepEqual(agents.asked, []string{"lead1", "w1"}) {
		t.Errorf("looked up %v, want the descriptors without a parent", agents.asked)
	}

	plain := startPageFixture(t, withHolders(holders))
	plainLocal := viewer{peer: "127.0.0.1:50000", host: "127.0.0.1:" + strconv.Itoa(plain.d.Endpoint().Port), localCookie: true}
	if rec := serve(plain.d, plainLocal, http.MethodGet, "/holders", ""); strings.Contains(rec.Body.String(), `"parents"`) {
		t.Errorf("a host without parents answered them: %s", rec.Body.String())
	}
}
