package wsdashboard

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// TestUserCommandInteractivePathsTouchNoStore pins the performance contract of
// the user commands: the list read, the session page, the stream's revision
// event, the click's acknowledgement and a short click's whole run make no
// store operation and no composer file operation; a registration, a
// replacement and a removal commit once each, and an unchanged registration
// commits nothing.
func TestUserCommandInteractivePathsTouchNoStore(t *testing.T) {
	f := startUserCommandFixture(t)
	reg := f.users.reg
	c := f.register(t, "build", "make")
	var log fileOpLog
	ops := reg.Ops()
	log.start()
	for range 20 {
		f.list(t)
		if res := composerGet(f.d, "/s/livesession"); res.Code != 200 {
			t.Fatalf("page: %d", res.Code)
		}
	}
	events := openSSE(t, f.base+"/s/livesession/events")
	if got, _ := collect(events); len(got) == 0 {
		t.Fatal("no stream event")
	}
	if res := f.run("build", c.Revision, "perf-click-0001", false); res.Code != http.StatusAccepted {
		t.Fatalf("click: %d %s", res.Code, res.Body)
	}
	waitCard(t, f.d, "perf-click-0001", "done")
	if fileOps := log.stop(); len(fileOps) != 0 {
		t.Fatalf("interactive paths made composer file operations: %v", fileOps)
	}
	if got := reg.Ops() - ops; got != 0 {
		t.Fatalf("interactive paths made %d store operations", got)
	}
	// Writes: one commit per changed command, nothing for an unchanged one.
	ops = reg.Ops()
	in := wsusercmd.Input{ID: "deploy", Label: "Deploy", Line: "make deploy"}
	for step, want := range []int64{1, 0, 1, 1} {
		switch step {
		case 0, 1:
			_, _, _ = reg.Set("livesession", f.users.root, in)
		case 2:
			in.Line = "make deploy-all"
			_, _, _ = reg.Set("livesession", f.users.root, in)
		case 3:
			_ = reg.Remove("livesession", f.users.root, "deploy")
		}
		if got := reg.Ops() - ops; got != want {
			t.Fatalf("write step %d made %d store operations, want %d", step, got, want)
		}
		ops = reg.Ops()
	}
}

// TestUserCommandPerfProbe prints the latency of the user command actions
// (WS_COMPOSER_PERF=1), the numbers the release record cites next to the store
// operations pinned above. It is no substitute for the Windows file-touch
// baseline the performance manual asks for.
func TestUserCommandPerfProbe(t *testing.T) {
	if os.Getenv("WS_COMPOSER_PERF") == "" {
		t.Skip("set WS_COMPOSER_PERF=1 to print the user command latency probe")
	}
	f := startUserCommandFixture(t)
	reg := f.users.reg
	stat := func(name string, d []time.Duration) {
		sort.Slice(d, func(a, b int) bool { return d[a] < d[b] })
		t.Logf("%-44s n=%-3d p50=%-10s p95=%-10s max=%s", name, len(d), d[len(d)/2], d[len(d)*95/100], d[len(d)-1])
	}
	get := func(path string) time.Duration {
		start := time.Now()
		if res := composerGet(f.d, path); res.Code != 200 {
			t.Fatalf("%s = %d", path, res.Code)
		}
		return time.Since(start)
	}
	var registers, unchanged, replaces, removes, lists, pages, pagesBare, acks, events []time.Duration
	f.d.SetUserCommands(nil)
	get("/s/livesession")
	for range 60 {
		pagesBare = append(pagesBare, get("/s/livesession"))
	}
	f.d.SetUserCommands(f.users)
	get("/s/livesession")
	for range 60 {
		pages = append(pages, get("/s/livesession"))
	}
	for i := range 30 {
		in := wsusercmd.Input{ID: fmt.Sprintf("cmd%d", i), Label: "label", Description: "description", Line: "make all"}
		start := time.Now()
		if _, _, err := reg.Set("livesession", f.users.root, in); err != nil {
			t.Fatal(err)
		}
		registers = append(registers, time.Since(start))
		start = time.Now()
		_, _, _ = reg.Set("livesession", f.users.root, in)
		unchanged = append(unchanged, time.Since(start))
		in.Line = "make clean all"
		start = time.Now()
		_, _, _ = reg.Set("livesession", f.users.root, in)
		replaces = append(replaces, time.Since(start))
		lists = append(lists, get("/s/livesession/user-commands"))
	}
	cur, _, _ := f.users.Get("livesession", "cmd0")
	for i := range 40 {
		id := fmt.Sprintf("perf-click-%04d", i)
		start := time.Now()
		if res := f.run("cmd0", cur.Revision, id, false); res.Code != http.StatusAccepted {
			t.Fatal(res.Body)
		}
		acks = append(acks, time.Since(start))
		waitCard(t, f.d, id, "done")
	}
	sse := openSSE(t, f.base+"/s/livesession/events")
	collect(sse)
	for i := range 20 {
		start := time.Now()
		in := wsusercmd.Input{ID: "evt", Label: "label", Line: fmt.Sprintf("echo %d", i)}
		_, _, _ = reg.Set("livesession", f.users.root, in)
	wait:
		for {
			select {
			case ev, ok := <-sse:
				if !ok {
					t.Fatal("stream closed")
				}
				if ev.name == "usercmd" {
					events = append(events, time.Since(start))
					break wait
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no usercmd event")
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i := range 30 {
		start := time.Now()
		_ = reg.Remove("livesession", f.users.root, fmt.Sprintf("cmd%d", i))
		removes = append(removes, time.Since(start))
	}
	stat("register (one WAL commit)", registers)
	stat("register, unchanged (no commit)", unchanged)
	stat("replace (one WAL commit)", replaces)
	stat("remove (one WAL commit)", removes)
	stat("list read (page load / refetch)", lists)
	stat("session page, user commands unbound", pagesBare)
	stat("session page, user commands bound", pages)
	stat("click acknowledgement", acks)
	stat("registration -> usercmd stream event", events)
}
