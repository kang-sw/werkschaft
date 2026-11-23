package wsdashboard

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fileOpLog records the composer's file operations between start and stop.
type fileOpLog struct {
	mu  sync.Mutex
	ops []string
}

func (l *fileOpLog) start() {
	fn := func(op string) { l.mu.Lock(); l.ops = append(l.ops, op); l.mu.Unlock() }
	composerFileHook.Store(&fn)
}

func (l *fileOpLog) stop() []string {
	composerFileHook.Store(nil)
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.ops
	l.ops = nil
	return out
}

// TestComposerInteractivePathsTouchNoFiles pins the performance contract:
// the route's acknowledgement, a card read, the list read, the session page
// and a settle tick make no composer file operation, and the file operations
// of a finalization and of a retention sweep are exactly the ones listed.
// (The localhost cookie guard's one stat per authenticated request is the
// common baseline of every route and is not the composer's.)
func TestComposerInteractivePathsTouchNoFiles(t *testing.T) {
	f := startComposerFixture(t)
	zero := 0
	big := []byte(strings.Repeat("p", 6000))
	f.svc.mu.Lock()
	f.svc.jobs["exec_async_p"] = ComposerJob{ExecID: "exec_async_p", Gen: 4, State: "running", Async: true}
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		if strings.HasPrefix(req.Line, "async") {
			return ComposerJob{ExecID: "exec_async_p", Gen: 4, State: "running", Async: true}, nil
		}
		if strings.HasPrefix(req.Line, "big") {
			return ComposerJob{ExecID: "exec_big_p", Gen: 4, State: "exited", ExitCode: &zero, Written: int64(len(big)), Output: big}, nil
		}
		return ComposerJob{ExecID: "exec_s_p", Gen: 4, State: "exited", ExitCode: &zero, Written: 3, Output: []byte("hi\n")}, nil
	}
	f.svc.mu.Unlock()
	var log fileOpLog

	// Acknowledgement of a short send and its finalization (no file for 3 bytes).
	log.start()
	if res := composerPost(f.d, "perf-files-001", "!echo hi"); res.Code != http.StatusAccepted {
		t.Fatal(res.Body)
	}
	waitCard(t, f.d, "perf-files-001", "done")
	if ops := log.stop(); len(ops) != 0 {
		t.Fatalf("a short send made file operations: %v", ops)
	}

	// The oversize finalization writes one file atomically and nothing else
	// (retention is the background loop's, asked once).
	log.start()
	if res := composerPost(f.d, "perf-files-002", "!big"); res.Code != http.StatusAccepted {
		t.Fatal(res.Body)
	}
	waitCard(t, f.d, "perf-files-002", "done")
	// The loop's sweep, asked by the write, adds its listing and then one
	// stat for the file; "readdir" alone is observable mid-sweep, so wait for
	// the sweep's last operation.
	waitFor(t, "the write's sweep", func() bool {
		log.mu.Lock()
		defer log.mu.Unlock()
		for _, op := range log.ops {
			if op == "stat" {
				return true
			}
		}
		return false
	})
	ops := log.stop()
	want := []string{"mkdir", "createtemp", "write", "close", "chmod", "rename", "readdir", "stat"}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("oversize finalization + sweep file operations = %v, want %v", ops, want)
	}

	// Page and read paths, with a card shown: memory only.
	log.start()
	for i := 0; i < 5; i++ {
		composerGet(f.d, "/s/livesession/composer-exec")
		composerGet(f.d, "/s/livesession/composer-exec/perf-files-002")
		composerGet(f.d, "/s/livesession")
	}
	if ops := log.stop(); len(ops) != 0 {
		t.Fatalf("page, list and card reads made file operations: %v", ops)
	}

	// Conversion and the settle tick: memory only too.
	log.start()
	composerPost(f.d, "perf-files-003", "!async")
	waitCard(t, f.d, "perf-files-003", "async")
	time.Sleep(120 * time.Millisecond) // several settle ticks
	if ops := log.stop(); len(ops) != 0 {
		t.Fatalf("a converted run's acknowledgement and settling made file operations: %v", ops)
	}
}

// TestComposerExecPerfProbe prints the composer actions' latencies for the
// release record (WS_COMPOSER_PERF=1): the complete actions (session page, the
// page-load list read, acknowledgement, card read, a short and an oversize
// finalization, the settle latency of a converted run and a retention sweep)
// on the machine it runs on, with the file operations each makes counted by
// TestComposerInteractivePathsTouchNoFiles. It is no substitute for the
// Windows file-touch baseline the performance manual asks for.
func TestComposerExecPerfProbe(t *testing.T) {
	if os.Getenv("WS_COMPOSER_PERF") == "" {
		t.Skip("set WS_COMPOSER_PERF=1 to print the composer latency probe")
	}
	f := startComposerFixture(t)
	zero := 0
	f.svc.mu.Lock()
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		switch {
		case strings.HasPrefix(req.Line, "big"):
			out := []byte(strings.Repeat("x", 1<<20))
			return ComposerJob{ExecID: "exec_big", Gen: 4, State: "exited", ExitCode: &zero, Command: req.Line, Written: int64(len(out)), Output: out}, nil
		case strings.HasPrefix(req.Line, "async"):
			return ComposerJob{ExecID: "exec_" + strings.ReplaceAll(strings.TrimSpace(req.Line), " ", "_"), Gen: 4, State: "running", Async: true}, nil
		}
		return ComposerJob{ExecID: "exec_s", Gen: 4, State: "exited", ExitCode: &zero, Command: req.Line, Written: 3, Output: []byte("hi\n")}, nil
	}
	f.svc.mu.Unlock()
	stat := func(name string, d []time.Duration) {
		sort.Slice(d, func(a, b int) bool { return d[a] < d[b] })
		t.Logf("%-40s n=%-3d p50=%-10s p95=%-10s max=%s", name, len(d), d[len(d)/2], d[len(d)*95/100], d[len(d)-1])
	}
	var pageUnbound, pageBound, lists, acks, reads, shortDone, bigDone, settle []time.Duration
	// The session page, composer unbound (the pre-change page's work) and bound.
	get := func(path string) time.Duration {
		start := time.Now()
		if res := composerGet(f.d, path); res.Code != 200 {
			t.Fatalf("%s = %d", path, res.Code)
		}
		return time.Since(start)
	}
	f.d.SetComposerExec(nil)
	get("/s/livesession")
	for i := 0; i < 60; i++ {
		pageUnbound = append(pageUnbound, get("/s/livesession"))
	}
	f.d.SetComposerExec(f.svc)
	get("/s/livesession")
	for i := 0; i < 60; i++ {
		pageBound = append(pageBound, get("/s/livesession"))
	}
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("perf-short-%04d", i)
		start := time.Now()
		if res := composerPost(f.d, id, "!echo hi"); res.Code != http.StatusAccepted {
			t.Fatal(res.Body)
		}
		acks = append(acks, time.Since(start))
		waitCard(t, f.d, id, "done")
		shortDone = append(shortDone, time.Since(start))
		reads = append(reads, get("/s/livesession/composer-exec/"+id))
		lists = append(lists, get("/s/livesession/composer-exec"))
	}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("perf-big-%05d", i)
		start := time.Now()
		composerPost(f.d, id, "!big")
		waitCard(t, f.d, id, "done")
		bigDone = append(bigDone, time.Since(start))
	}
	// A converted run's end to its card done: the settle tick.
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("perf-async-%04d", i)
		execID := fmt.Sprintf("exec_async_%d", i)
		f.svc.mu.Lock()
		f.svc.jobs[execID] = ComposerJob{ExecID: execID, Gen: 4, State: "running", Async: true}
		f.svc.mu.Unlock()
		composerPost(f.d, id, fmt.Sprintf("!async %d", i))
		waitCard(t, f.d, id, "async")
		f.svc.mu.Lock()
		f.svc.jobs[execID] = ComposerJob{ExecID: execID, Gen: 4, State: "exited", ExitCode: &zero, Async: true, Written: 3, Output: []byte("hi\n")}
		f.svc.mu.Unlock()
		start := time.Now()
		waitCard(t, f.d, id, "done")
		settle = append(settle, time.Since(start))
	}
	var sweeps []time.Duration
	for i := 0; i < 10; i++ {
		start := time.Now()
		f.d.composerSweep()
		sweeps = append(sweeps, time.Since(start))
	}
	stat("session page, composer unbound", pageUnbound)
	stat("session page, composer bound", pageBound)
	stat("page-load list read (64 cards max)", lists)
	stat("composer-exec ack (short send)", acks)
	stat("short send: submit -> card done", shortDone)
	stat("card read", reads)
	stat("oversize 1 MiB: submit -> card done", bigDone)
	stat("converted run: end -> card done (settle)", settle)
	stat("retention sweep (directory listing)", sweeps)
	entries, _ := os.ReadDir(filepath.Join(f.dir, composerOutputDir))
	t.Logf("output files kept: %d", len(entries))
}
