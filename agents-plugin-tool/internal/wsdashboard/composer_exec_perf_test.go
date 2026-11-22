package wsdashboard

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestComposerExecPerfProbe prints the composer paths' latencies for the
// release record (WS_COMPOSER_PERF=1): the route's acknowledgement and a card
// read, which touch memory only, and the oversize file write with its
// retention sweep, which runs on the submission's own goroutine. The file
// operations of each path are fixed by the code and counted in the ticket's
// result; this probe measures time on the machine it runs on, which is no
// substitute for the Windows baseline.
func TestComposerExecPerfProbe(t *testing.T) {
	if os.Getenv("WS_COMPOSER_PERF") == "" {
		t.Skip("set WS_COMPOSER_PERF=1 to print the composer latency probe")
	}
	f := startComposerFixture(t)
	zero := 0
	f.svc.mu.Lock()
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		if strings.HasPrefix(req.Line, "big") {
			out := []byte(strings.Repeat("x", 1<<20))
			return ComposerJob{ExecID: "exec_big", Gen: 4, State: "exited", ExitCode: &zero, Command: req.Line, Written: int64(len(out)), Output: out}, nil
		}
		return ComposerJob{ExecID: "exec_s", Gen: 4, State: "exited", ExitCode: &zero, Command: req.Line, Written: 3, Output: []byte("hi\n")}, nil
	}
	f.svc.mu.Unlock()
	stat := func(name string, d []time.Duration) {
		sort.Slice(d, func(a, b int) bool { return d[a] < d[b] })
		t.Logf("%-34s n=%d p50=%s p95=%s max=%s", name, len(d), d[len(d)/2], d[len(d)*95/100], d[len(d)-1])
	}
	var acks, reads, files []time.Duration
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("perf-short-%04d", i)
		start := time.Now()
		if res := composerPost(f.d, id, "!echo hi"); res.Code != http.StatusAccepted {
			t.Fatal(res.Body)
		}
		acks = append(acks, time.Since(start))
		waitCard(t, f.d, id, "done")
		start = time.Now()
		composerGet(f.d, "/s/livesession/composer-exec/"+id)
		reads = append(reads, time.Since(start))
	}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("perf-big-%05d", i)
		composerPost(f.d, id, "!big")
		start := time.Now()
		waitCard(t, f.d, id, "done")
		files = append(files, time.Since(start))
	}
	stat("route ack (short send)", acks)
	stat("card read (memory)", reads)
	stat("oversize 1 MiB: card done", files)
	entries, _ := os.ReadDir(filepath.Join(f.dir, composerOutputDir))
	t.Logf("output files kept: %d", len(entries))
}
