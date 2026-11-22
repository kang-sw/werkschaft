package wsdashboard

import (
	"fmt"
	"net/http"
	"sort"
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
