package wsdashboard

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsauditstore"
)

type liveClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *liveClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *liveClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// liveAudit opens a real audit store with 1 MiB segments in a 4 MiB
// per-session ring and 64 KiB bodies, read by a bare daemon's audit routes.
func liveAudit(t *testing.T, clock *liveClock) (*wsauditstore.Store, *Daemon) {
	t.Helper()
	opts := wsauditstore.Options{
		Root:          t.TempDir(),
		Limits:        wsauditstore.Limits{SessionBytes: 4 << 20, TotalBytes: 64 << 20, BodyBytes: 64 << 10, Retention: 7 * 24 * time.Hour},
		SweepInterval: -1,
	}
	if clock != nil {
		opts.Now = clock.Now
	}
	store, err := wsauditstore.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, auditDaemon(store)
}

func liveAppend(t *testing.T, store *wsauditstore.Store, from, n int, body string) {
	t.Helper()
	ctx := context.Background()
	for i := from; i < from+n; i++ {
		ev, _ := wsaudit.NewEnvelope("sess", "claude", auditBase.Add(time.Duration(i)*time.Second), wsaudit.KindToolResult)
		ev.ID, ev.Seq, ev.CallID, ev.Out = fmt.Sprintf("e%d", i), uint64(i), fmt.Sprintf("c%d", i), body
		if err := store.Append(ctx, ev); err != nil {
			t.Fatal(err)
		}
		if i%8 == 0 {
			if err := store.Flush(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := store.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

type liveSearch struct {
	Hits []map[string]any `json:"hits"`
	Next *string          `json:"next"`
}

func assertStale(t *testing.T, d *Daemon, target string) {
	t.Helper()
	rec := auditGet(t, d, http.MethodGet, target)
	if rec.Code != http.StatusGone || strings.TrimSpace(rec.Body.String()) != `{"error":"stale_cursor"}` {
		t.Fatalf("GET %s = %d %s, want 410 stale_cursor", target, rec.Code, rec.Body.String())
	}
}

// TestAuditLiveRingCutAndStaleCursors pins cut and the 410 stale answers
// against a live ring: after the ring evicts a segment, the oldest page
// reports cut and cursors into the evicted segment are stale; after
// whole-ring expiry and a new append, the new ring reports no cut and the old
// event and search cursors are stale.
func TestAuditLiveRingCutAndStaleCursors(t *testing.T) {
	clock := &liveClock{now: time.Now()}
	store, d := liveAudit(t, clock)
	body := strings.Repeat("x", 60<<10) + "\nmatch line"
	liveAppend(t, store, 1, 20, body)
	if p := auditJSON[pageOut](t, d, "/s/sess/audit/events?before=e2", 200); !p.End || p.Cut {
		t.Fatalf("before eviction: end %v cut %v", p.End, p.Cut)
	}
	early := auditJSON[liveSearch](t, d, "/s/sess/audit/search?q=match&n=1", 200)
	if early.Next == nil {
		t.Fatal("no search cursor")
	}
	liveAppend(t, store, 21, 130, body) // through the 4 MiB ring cap

	var oldest pageOut
	p := auditJSON[pageOut](t, d, "/s/sess/audit/events?n=500", 200)
	for {
		oldest = p
		if p.End {
			break
		}
		p = auditJSON[pageOut](t, d, "/s/sess/audit/events?n=500&before="+p.Before, 200)
	}
	if !oldest.Cut || oldest.Before == "e1" || len(oldest.Events) == 0 {
		t.Fatalf("oldest page after eviction: before %q cut %v", oldest.Before, oldest.Cut)
	}
	assertStale(t, d, "/s/sess/audit/events?before=e1")
	assertStale(t, d, "/s/sess/audit/events?after=e20")
	assertStale(t, d, "/s/sess/audit/search?q=match&n=1&cursor="+url.QueryEscape(*early.Next))
	late := auditJSON[liveSearch](t, d, "/s/sess/audit/search?q=match&n=1", 200)

	// Whole-ring expiry, then a new append.
	clock.advance(10 * 24 * time.Hour)
	if err := store.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := auditJSON[pageOut](t, d, "/s/sess/audit/events?before=e150", 200); len(p.Events) != 0 || !p.End || p.Cut {
		t.Fatalf("expired ring: %+v", p)
	}
	liveAppend(t, store, 1000, 1, "match again")
	p = auditJSON[pageOut](t, d, "/s/sess/audit/events", 200)
	if len(p.Events) != 1 || p.Before != "e1000" || !p.End || p.Cut {
		t.Fatalf("ring after expiry: %+v", p)
	}
	assertStale(t, d, "/s/sess/audit/events?before=e150")
	assertStale(t, d, "/s/sess/audit/search?q=match&n=1&cursor="+url.QueryEscape(*late.Next))
}

// TestAuditLiveStoredTruncBody pins a body the live store capped to head and
// tail: the summary's out_bytes is the stored length, the envelope carries
// stored_trunc, range offsets end at the stored bytes, and search does not
// find text from the omitted middle.
func TestAuditLiveStoredTruncBody(t *testing.T) {
	store, d := liveAudit(t, nil)
	body := "HEADWORD\n" + strings.Repeat("h", 100<<10) + "\nMIDDLEWORD\n" + strings.Repeat("t", 100<<10) + "\nTAILWORD"
	liveAppend(t, store, 1, 1, body)
	p := auditJSON[pageOut](t, d, "/s/sess/audit/events", 200)
	stored := int(p.Events[0]["out_bytes"].(float64))
	if stored >= len(body) || stored > 64<<10 {
		t.Fatalf("out_bytes = %d of a %d-byte body", stored, len(body))
	}
	type envOut struct {
		Out         string              `json:"out"`
		StoredTrunc wsaudit.StoredTrunc `json:"stored_trunc"`
	}
	env := auditJSON[envOut](t, d, "/s/sess/audit/event/e1", 200)
	if tr := env.StoredTrunc.Out; tr == nil || tr.OriginalBytes != len(body) || tr.HeadBytes+tr.TailBytes != stored || len(env.Out) != stored {
		t.Fatalf("stored_trunc = %+v, out %d", env.StoredTrunc.Out, len(env.Out))
	}
	r := auditJSON[rangeOut](t, d, fmt.Sprintf("/s/sess/audit/event/e1?part=out&offset=%d", stored-8), 200)
	if r.Data != "TAILWORD" || !r.End || r.Total != stored {
		t.Fatalf("range at the stored end = %+v", r)
	}
	auditJSON[map[string]string](t, d, fmt.Sprintf("/s/sess/audit/event/e1?part=out&offset=%d", stored+1), 400)
	for q, want := range map[string]int{"HEADWORD": 1, "TAILWORD": 1, "MIDDLEWORD": 0} {
		if s := auditJSON[liveSearch](t, d, "/s/sess/audit/search?case=1&q="+q, 200); len(s.Hits) != want {
			t.Errorf("search %s: %d hits, want %d", q, len(s.Hits), want)
		}
	}
}

// TestAuditCaptureKeepsUpDuringALongSearch pins that a search never holds up
// capture: with a search held in the middle of its scan, appends are
// accepted, written and readable, and the held search still answers from
// its snapshot; and searches running over the routes alongside a stream of
// appends drop nothing.
func TestAuditCaptureKeepsUpDuringALongSearch(t *testing.T) {
	store, d := liveAudit(t, nil)
	body := strings.Repeat("y", 30<<10) + "\nmatch line"
	liveAppend(t, store, 1, 100, body)
	ctx := context.Background()
	v, err := store.View(ctx, "sess")
	if err != nil {
		t.Fatal(err)
	}
	s, err := newAuditSearch("match", false, false, nil, "", auditSearchMax)
	if err != nil {
		t.Fatal(err)
	}
	inScan, release, finished := make(chan struct{}), make(chan struct{}), make(chan auditSearchAnswer, 1)
	go func() {
		calls := 0
		ans, _ := s.run(v.Events, nil, func() bool {
			if calls++; calls == len(v.Events)/2 {
				close(inScan)
				<-release
			}
			return false
		})
		finished <- ans
	}()
	<-inScan
	appended := make(chan error, 1)
	go func() {
		defer close(appended)
		for i := 101; i <= 300; i++ {
			ev, _ := wsaudit.NewEnvelope("sess", "claude", auditBase.Add(time.Duration(i)*time.Second), wsaudit.KindToolResult)
			ev.ID, ev.Seq, ev.Out = fmt.Sprintf("e%d", i), uint64(i), body
			if err := store.Append(ctx, ev); err != nil {
				appended <- err
				return
			}
		}
		appended <- store.Flush(ctx)
	}()
	select {
	case err := <-appended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("capture waited on a search held mid-scan")
	}
	if p := auditJSON[pageOut](t, d, "/s/sess/audit/events?n=1", 200); p.After != "e300" {
		t.Fatalf("newest event while the search is held = %q", p.After)
	}
	close(release)
	if ans := <-finished; len(ans.Hits) != 100 {
		t.Fatalf("held search answered %d hits from its 100-event snapshot", len(ans.Hits))
	}

	// Searches over the routes alongside more appends.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				auditGet(t, d, http.MethodGet, "/s/sess/audit/search?q=match&n=500")
			}
		}()
	}
	start := time.Now()
	liveAppend(t, store, 301, 200, body)
	close(stop)
	wg.Wait()
	if n := store.Dropped(); n != 0 {
		t.Fatalf("capture dropped %d events during searches", n)
	}
	if p := auditJSON[pageOut](t, d, "/s/sess/audit/events?n=1", 200); p.After != "e500" {
		t.Fatalf("newest event after the searches = %q", p.After)
	}
	t.Logf("200 appends with 4 concurrent searches took %s", time.Since(start))
}
