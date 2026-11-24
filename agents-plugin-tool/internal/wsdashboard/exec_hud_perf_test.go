package wsdashboard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// fileOps is the process's counted file operations (wsstate's wrappers, the
// dashboard's own file layer) now.
func fileOps() int64 { return wsstate.FileIOCounters()["file_io_total"] }

// median is the middle of ds.
func median(ds []time.Duration) time.Duration {
	sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
	return ds[len(ds)/2]
}

// TestExecHUDRoutesTouchNoFilesBeyondTheRequestGuard pins the performance
// contract of the HUD's interactive paths (ai-docs/manuals/performance.md,
// Rules 1 and 5): the job list, the output read and the stop route make
// exactly the file operations an equally authenticated request to a route that
// answers 404 makes (the localhost cookie guard's own), whatever the job
// count, and report their latency. The numbers it logs are the ticket's
// measurement.
func TestExecHUDRoutesTouchNoFilesBeyondTheRequestGuard(t *testing.T) {
	f, svc := startHUDFixture(t)
	const rounds = 15
	measure := func(name string, do func() int) (ops int64, latency time.Duration) {
		do() // warm
		var ds []time.Duration
		before := fileOps()
		for i := 0; i < rounds; i++ {
			start := time.Now()
			if code := do(); code != http.StatusOK && code != http.StatusNotFound {
				t.Fatalf("%s = %d", name, code)
			}
			ds = append(ds, time.Since(start))
		}
		return (fileOps() - before) / rounds, median(ds)
	}
	baseOps, baseLatency := measure("baseline", func() int { return hudGet(f, "/s/livesession/exec/nope").Code })
	t.Logf("baseline (a 404 under the same guard): %d file ops/request, p50 %v", baseOps, baseLatency)
	for _, n := range []int{0, 1, 50, 200} {
		jobs := make([]execjob.HostedSummary, 0, n)
		for i := 0; i < n; i++ {
			jobs = append(jobs, execjob.HostedSummary{ID: fmt.Sprintf("exec_job-%03d-x", i), Tool: "exec.shell", Command: "npm run build && npm test -- --watch=false", Cwd: "web/", State: execjob.HostedExited, Origin: execjob.OriginAgent, StartedAt: 1, EndedAt: 2, Written: 4096})
		}
		svc.mu.Lock()
		svc.jobs["livesession"] = jobs
		svc.mu.Unlock()
		ops, latency := measure("list", func() int { return hudGet(f, "/s/livesession/exec/jobs").Code })
		res := hudGet(f, "/s/livesession/exec/jobs")
		t.Logf("list with %3d jobs: %d file ops/request (baseline %d), p50 %v, %d bytes", n, ops, baseOps, latency, res.Body.Len())
		if ops != baseOps {
			t.Fatalf("list with %d jobs made %d file ops per request, the guard alone %d", n, ops, baseOps)
		}
	}
	ops, latency := measure("output", func() int { return hudGet(f, "/s/livesession/exec/exec_a-b-c/output?from=0").Code })
	t.Logf("output read (service fake): %d file ops/request (baseline %d), p50 %v", ops, baseOps, latency)
	if ops != baseOps {
		t.Fatalf("output read made %d file ops per request, the guard alone %d", ops, baseOps)
	}
	post := func() int {
		return composerHTTP(f.d, http.MethodPost, "/s/livesession/exec/exec_a-b-c/stop", "", "http://127.0.0.1", "127.0.0.1:1", nil).Code
	}
	ops, latency = measure("stop", post)
	t.Logf("stop route (service fake): %d file ops/request (baseline %d), p50 %v", ops, baseOps, latency)
	if ops != baseOps {
		t.Fatalf("stop made %d file ops per request, the guard alone %d", ops, baseOps)
	}
}

// TestBgExecOutputStreamAvoidsPerSampleAuthIO drives the same production
// handler with HTTP snapshots and with one authenticated output stream.
func TestBgExecOutputStreamAvoidsPerSampleAuthIO(t *testing.T) {
	f, fake := startHUDFixture(t)
	f.d.SetExecJobs(&execTreeFake{execJobsFake: fake})
	const jobID = "exec_a-b-c"
	const samples = 4
	url := f.base + "/s/livesession/exec/bg/" + jobID + "/output?from=0&limit=16384"
	cookie := localhostTestCookie(t, f.d)
	backdateSecret(t, filepath.Join(f.dir, localhostSecretFile), time.Hour)
	client := &http.Client{Timeout: 10 * time.Second}
	t.Cleanup(client.CloseIdleConnections)

	get := func(accept string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookie)
		req.Header.Set("Accept", accept)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	snapshot := func() {
		t.Helper()
		res := get("application/json")
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("snapshot status = %d", res.StatusCode)
		}
		if _, err := io.Copy(io.Discard, res.Body); err != nil {
			t.Fatal(err)
		}
	}
	snapshot() // warm the cookie cache
	reads, stats := secretCacheIO()
	var elapsed []time.Duration
	for i := 0; i < samples; i++ {
		start := time.Now()
		snapshot()
		elapsed = append(elapsed, time.Since(start))
	}
	pollReads, pollStats := secretCacheIO()
	if pollReads != reads || pollStats-stats != samples {
		t.Fatalf("%d snapshot requests used %d reads and %d stats, want 0 and %d", samples, pollReads-reads, pollStats-stats, samples)
	}

	setOutput := func(n int, state string) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		view := fake.out[jobID]
		view.From, view.Next, view.Written, view.Job.State = int64(n-1), int64(n), int64(n), state
		view.Segments = []execjob.Segment{{Stream: execjob.StreamStdout, Data: []byte("a")}}
		fake.out[jobID] = view
	}
	setOutput(1, execjob.HostedRunning)
	start := time.Now()
	stream := get("text/event-stream")
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK || stream.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream response = %d %q", stream.StatusCode, stream.Header.Get("Content-Type"))
	}
	scanner := bufio.NewScanner(stream.Body)
	readEvent := func(want int) {
		t.Helper()
		for scanner.Scan() {
			raw, ok := strings.CutPrefix(scanner.Text(), "data: ")
			if !ok {
				continue
			}
			var view execOutputView
			if err := json.Unmarshal([]byte(raw), &view); err != nil || view.Next != int64(want) || len(view.Segments) != 1 || view.Segments[0].Text != "a" {
				t.Fatalf("output event %d = %s, err %v", want, raw, err)
			}
			return
		}
		t.Fatalf("stream ended before event %d: %v", want, scanner.Err())
	}
	readEvent(1)
	connectLatency := time.Since(start)
	connectReads, connectStats := secretCacheIO()
	for n := 2; n <= samples; n++ {
		state := execjob.HostedRunning
		if n == samples {
			state = execjob.HostedExited
		}
		setOutput(n, state)
		readEvent(n)
	}
	for scanner.Scan() {
		if scanner.Text() != "" {
			t.Fatalf("unexpected data after terminal sample: %s", scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	streamReads, streamStats := secretCacheIO()
	if connectReads != pollReads || connectStats-pollStats != 1 || streamReads != connectReads || streamStats != connectStats {
		t.Fatalf("stream connect used %d reads and %d stats, follow-up samples used %d reads and %d stats", connectReads-pollReads, connectStats-pollStats, streamReads-connectReads, streamStats-connectStats)
	}
	t.Logf("%d authenticated samples: JSON polls %d reads/%d stats (p50 %v), SSE %d reads/%d stats (first sample %v), zero auth file operations after connect",
		samples, pollReads-reads, pollStats-stats, median(elapsed), streamReads-pollReads, streamStats-pollStats, connectLatency)

	fresh, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(f.dir, localhostSecretFile, []byte(fresh+"\n")); err != nil {
		t.Fatal(err)
	}
	res := get("text/event-stream")
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stream opened with a rotated cookie: %d", res.StatusCode)
	}
}

// TestExecJobsEventBurstWakeCost measures the wake cost of a start/end burst
// across open streams: the revision event wakes every stream once per
// coalesced round, and the renders it causes stay bounded (the page's render
// is shared and unchanged, so the wake costs a send, not a render per
// stream). The counters it logs are the ticket's measurement; the comparison
// is the same burst of ExecJobsChanged before this change (the rail key
// only).
func TestExecJobsEventBurstWakeCost(t *testing.T) {
	f, _ := startHUDFixture(t)
	const streams, burst = 4, 200
	var all []<-chan sseEvent
	for i := 0; i < streams; i++ {
		all = append(all, openStream(t, f.base+"/s/livesession/events"))
	}
	// Drain the connect rounds.
	for _, ch := range all {
		collect(ch)
	}
	run := func(name string, fire func()) map[string]int64 {
		before := f.d.stats.snapshot()
		fileBefore := fileOps()
		start := time.Now()
		for i := 0; i < burst; i++ {
			fire()
		}
		delivered := 0
		for _, ch := range all {
			evs, _ := collect(ch)
			for _, ev := range evs {
				if ev.name == "execjobs" {
					delivered++
				}
			}
		}
		after := f.d.stats.snapshot()
		d := map[string]int64{}
		for _, k := range []string{"signals", "session_renders", "rail_renders", "shared", "sends", "refresh_skips"} {
			d[k] = after[k] - before[k]
		}
		d["events_delivered"] = int64(delivered)
		d["file_ops"] = fileOps() - fileBefore
		t.Logf("%s: %d changes x %d streams over %v: %v", name, burst, streams, time.Since(start).Round(time.Millisecond), d)
		return d
	}
	// Before this change ExecJobsChanged only woke the rail's key.
	run("rail-key wake burst (before)", func() { f.d.broker.wake(railListRepoKey) })
	got := run("ExecJobsChanged burst (after)", f.d.ExecJobsChanged)
	// One revision event per stream per coalesced round: far fewer than a
	// change each, and none carries job content (TestExecJobsEventCarriesOnlyARevision).
	if got["events_delivered"] == 0 || got["events_delivered"] > int64(streams*burst) {
		t.Fatalf("events delivered = %d", got["events_delivered"])
	}
	// The page's render is shared and unchanged by a job event: no render of
	// it per wake.
	if got["session_renders"] > int64(burst) {
		t.Fatalf("session renders = %d for %d changes", got["session_renders"], burst)
	}
}
