package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsholder"
)

// allowSessions is a HostedSessions that runs every call at once.
type allowSessions struct{}

func (allowSessions) Owner(id string) (execjob.HostedOwner, error) {
	return execjob.HostedOwner{ID: id, Gen: 1, Lead: true}, nil
}
func (allowSessions) Approve(context.Context, string, execjob.HostedCall) (bool, error) {
	return false, nil
}
func (allowSessions) Immediate(string) (bool, error) { return true, nil }
func (allowSessions) Notify(string, uint64, string, string) (execjob.NoticeOutcome, error) {
	return execjob.NoticeAccepted, nil
}

// TestHostedExecOverTheTokenRouteFollowsTheBinding: a session the daemon
// reaches over its per-session token route gets the hosted exec tools, owned
// by the conversation that token names; the same request without a bound
// conversation is refused.
func TestHostedExecOverTheTokenRouteFollowsTheBinding(t *testing.T) {
	for _, bound := range []bool{true, false} {
		t.Run(map[bool]string{true: "bound", false: "unbound"}[bound], func(t *testing.T) {
			root := t.TempDir()
			hello := wsdashboard.ProxyHello{
				Protocol: wsdashboard.ProxyProtocol, Mode: wsdashboard.ProxyModeMCP, Cwd: root, ProjectRoot: root,
				OwnerPID: os.Getpid(), Harness: "claude", Version: "0.50.2",
				Env: map[string]string{
					"WS_CACHE_HOME": t.TempDir(), "WS_CONFIG_HOME": t.TempDir(),
					"WS_MCP_NO_DASHBOARD": "1", "WS_MCP_ALLOWED_TOOLS": "exec.list",
					wsagent.EnvPiToken: "", wsagent.EnvPiEndpoint: "",
				},
			}
			if bound {
				hello.Conversation = "bound-conversation"
			}
			agents := &httpTokenAgents{token: "exec-session-token", void: make(chan struct{}), hello: hello}
			state := t.TempDir()
			store, err := execjob.OpenHostedStore(filepath.Join(state, hostedExecStoreName))
			if err != nil {
				t.Fatal(err)
			}
			holders := wsholder.New(wsholder.Options{Dir: filepath.Join(state, wsholder.DirName)})
			t.Cleanup(holders.Close)
			hosted, err := execjob.NewHosted(execjob.HostedOptions{Holders: execjob.HolderManager{Manager: holders}, Store: store, Sessions: allowSessions{}, ExitDir: filepath.Join(state, hostedExecExitsName)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = hosted.Close() })
			host := &proxyHost{logw: io.Discard}
			host.exec.Store(hosted)
			d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: t.TempDir(), Port: 0, Version: "0.50.2", LockWait: 200 * time.Millisecond, Proxy: host, Agents: agents})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(d.Close)
			url := "http://127.0.0.1:" + strconv.Itoa(d.Endpoint().Port) + wsdashboard.ProxyHTTPPath
			client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}}
			post := func(body string) string {
				t.Helper()
				req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+agents.token)
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				raw, _ := io.ReadAll(resp.Body)
				return string(raw)
			}
			post(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"claude-code","version":"2.1.326"}}}`)
			raw := post(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"exec.list","arguments":{}}}`)
			var r struct {
				Result struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
					IsError bool `json:"isError"`
				} `json:"result"`
			}
			if err := json.Unmarshal([]byte(raw), &r); err != nil || len(r.Result.Content) == 0 {
				t.Fatalf("exec.list reply = %s (%v)", raw, err)
			}
			text := r.Result.Content[0].Text
			if bound && (r.Result.IsError || !strings.Contains(text, "no exec jobs")) {
				t.Fatalf("bound session: %s", raw)
			}
			if !bound && (!r.Result.IsError || !strings.Contains(text, "runs only in a session the dashboard hosts")) {
				t.Fatalf("unbound session: %s", raw)
			}
		})
	}
}

// TestHostedExecLinkRoutesExecHoldersOnly: before the jobs bind, an exec
// holder's records and exit are dropped without reaching the lead exit
// hook, and every other holder kind keeps that hook. The bound link's
// routing to the jobs runs with real holders in
// TestHostedExecSurvivesAnOldDaemonAndReopens and openHostedExecDaemon's
// tests.
func TestHostedExecLinkRoutesExecHoldersOnly(t *testing.T) {
	link := &hostedExecLink{}
	var nextCalls []string
	next := func(desc wsholder.Descriptor, _ wsholder.Exit) error {
		nextCalls = append(nextCalls, desc.ID)
		return nil
	}
	hook := link.onExit(next)
	execDesc := wsholder.Descriptor{ID: "h000000000001", Session: json.RawMessage(`{"kind":"exec","exec_id":"exec_a-b-c"}`)}
	leadDesc := wsholder.Descriptor{ID: "h000000000002", Session: json.RawMessage(`{"kind":"lead","reply_id":"x"}`)}
	link.HolderEvent(execDesc.ID, wsholder.Event{Event: "output", Seq: 1, Data: []byte("x")})
	if err := hook(execDesc, wsholder.Exit{Reason: "stop"}); err != nil {
		t.Fatal(err)
	}
	if err := hook(leadDesc, wsholder.Exit{Reason: "stop"}); err != nil {
		t.Fatal(err)
	}
	if len(nextCalls) != 1 || nextCalls[0] != leadDesc.ID {
		t.Fatalf("the lead hook saw %q", nextCalls)
	}
}

// TestHostedExecSurvivesAnOldDaemonAndReopens is the T0 holder check: a new
// daemon starts a job under a holder with an exit copy and goes; an older
// daemon, which knows no exec holders, attaches it, and its stop and
// generic cleanup remove the holder's directory with exit.json and persist
// nothing; the new daemon then reopens and reads the job's terminal status
// from the exit copy.
func TestHostedExecSurvivesAnOldDaemonAndReopens(t *testing.T) {
	skipWithoutPOSIXShell(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3: %v", err)
	}
	state := t.TempDir()
	holdersDir := filepath.Join(state, wsholder.DirName)
	exits := filepath.Join(state, hostedExecExitsName)
	open := func() (*execjob.Hosted, *wsholder.Manager, *hostedExecLink) {
		link := &hostedExecLink{}
		m := wsholder.New(wsholder.Options{Dir: holdersDir, Python: python, OnExit: link.onExit(func(wsholder.Descriptor, wsholder.Exit) error { return nil })})
		m.AddSink(link)
		store, err := execjob.OpenHostedStore(filepath.Join(state, hostedExecStoreName))
		if err != nil {
			t.Fatal(err)
		}
		h, err := execjob.NewHosted(execjob.HostedOptions{Holders: execjob.HolderManager{Manager: m}, Store: store, Sessions: allowSessions{}, ExitDir: exits, Window: 100 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		link.hosted.Store(h)
		h.Start()
		return h, m, link
	}
	newDaemon, newHolders, _ := open()
	root := t.TempDir()
	v, err := newDaemon.Launch(context.Background(), make(chan struct{}), execjob.HostedLaunch{Owner: "lead", Tool: "exec.shell", Root: root, Cwd: root, Argv: []string{"/bin/sh", "-c", "sleep 30"}, Environ: os.Environ()})
	if err != nil || v.Outcome != execjob.OutcomeConverted {
		t.Fatalf("launch = %+v, %v", v, err)
	}
	// The new daemon goes; its job's holder keeps running.
	_ = newDaemon.Close()
	newHolders.Close()

	// The older daemon: a plain manager whose exit hook persists only lead
	// holders (cmd/ws-mcp persistHolderExit before exec holders existed).
	old := wsholder.New(wsholder.Options{Dir: holdersDir, Python: python, OnExit: func(desc wsholder.Descriptor, exit wsholder.Exit) error {
		var s struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(desc.Session, &s) != nil || s.Kind != "lead" {
			return nil
		}
		t.Fatalf("the old daemon persisted an exec holder")
		return nil
	}})
	old.Scan()
	var attached bool
	for _, info := range old.List().Holders {
		if info.ID == v.HolderID && info.Attached {
			attached = true
		}
	}
	if !attached {
		t.Fatalf("the old daemon did not attach the job's holder: %+v", old.List())
	}
	// The old daemon's stop (its holder drawer's) and generic cleanup.
	if err := old.Stop(v.HolderID); err != nil {
		t.Fatal(err)
	}
	old.Close()
	if _, err := os.Stat(filepath.Join(holdersDir, v.HolderID)); !os.IsNotExist(err) {
		t.Fatalf("the old cleanup left the holder directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(exits, v.ID+".json")); err != nil {
		t.Fatalf("the exit copy did not survive the old cleanup: %v", err)
	}

	reopened, reopenedHolders, _ := open()
	t.Cleanup(func() {
		_ = reopened.Close()
		reopenedHolders.Close()
	})
	got, err := reopened.Get("lead", v.ID)
	if err != nil || got.State != execjob.HostedExited || got.Signal == nil || got.Notice != execjob.NoticeDelivered {
		t.Fatalf("reopened job = %+v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(exits, v.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("the consumed exit copy stays: %v", err)
	}
}

// hostedExecDaemon is one daemon generation's hosted exec jobs over the
// holders directory of state, with real session holders.
type hostedExecDaemon struct {
	hosted   *execjob.Hosted
	holders  *wsholder.Manager
	sessions *recordingSessions
}

// recordingSessions runs every call at once and records the notices.
type recordingSessions struct {
	allowSessions
	notices chan string
}

func (s *recordingSessions) Notify(id string, gen uint64, eventID, text string) (execjob.NoticeOutcome, error) {
	s.notices <- eventID + "|" + text
	return execjob.NoticeAccepted, nil
}

func openHostedExecDaemon(t *testing.T, state string, window time.Duration) *hostedExecDaemon {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3: %v", err)
	}
	link := &hostedExecLink{}
	m := wsholder.New(wsholder.Options{Dir: filepath.Join(state, wsholder.DirName), Python: python, OnExit: link.onExit(func(wsholder.Descriptor, wsholder.Exit) error { return nil })})
	m.AddSink(link)
	// The manager scans, and attaches, before the jobs bind: as in the
	// daemon, where the first scan runs inside StartDaemon.
	m.Scan()
	store, err := execjob.OpenHostedStore(filepath.Join(state, hostedExecStoreName))
	if err != nil {
		t.Fatal(err)
	}
	sessions := &recordingSessions{notices: make(chan string, 8)}
	h, err := execjob.NewHosted(execjob.HostedOptions{Holders: execjob.HolderManager{Manager: m}, Store: store, Sessions: sessions, ExitDir: filepath.Join(state, hostedExecExitsName), Window: window})
	if err != nil {
		t.Fatal(err)
	}
	link.hosted.Store(h)
	h.Start()
	return &hostedExecDaemon{hosted: h, holders: m, sessions: sessions}
}

func (d *hostedExecDaemon) close() {
	_ = d.hosted.Close()
	d.holders.Close()
}

func (d *hostedExecDaemon) stopAll() {
	for _, info := range d.holders.List().Holders {
		_ = d.holders.Stop(info.ID)
	}
}

func waitNotice(t *testing.T, d *hostedExecDaemon, what string) string {
	t.Helper()
	select {
	case n := <-d.sessions.notices:
		return n
	case <-time.After(10 * time.Second):
		t.Fatalf("no notice: %s", what)
		return ""
	}
}

// TestHostedExecChildEndsUnderAnOldDaemon: the job's child ends while an
// older daemon holds its holder (which then lingers, attached, as that
// daemon never stops an exec holder); the reopened new daemon attaches it
// before its jobs bind, replays its ring, and ends the job with the exit
// record, one notice, and the holder's stop.
func TestHostedExecChildEndsUnderAnOldDaemon(t *testing.T) {
	skipWithoutPOSIXShell(t)
	state := t.TempDir()
	first := openHostedExecDaemon(t, state, 100*time.Millisecond)
	root := t.TempDir()
	v, err := first.hosted.Launch(context.Background(), make(chan struct{}), execjob.HostedLaunch{Owner: "lead", Tool: "exec.shell", Root: root, Cwd: root, Argv: []string{"/bin/sh", "-c", "sleep 0.6; echo done-under-old"}, Environ: os.Environ()})
	if err != nil || v.Outcome != execjob.OutcomeConverted {
		t.Fatalf("launch = %+v, %v", v, err)
	}
	first.close()
	old := wsholder.New(wsholder.Options{Dir: filepath.Join(state, wsholder.DirName)})
	old.Scan()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var exited bool
		for _, info := range old.List().Holders {
			exited = exited || info.ID == v.HolderID && info.Child != nil && info.Child.State == "exited"
		}
		if exited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the child did not end under the old daemon: %+v", old.List())
		}
		time.Sleep(50 * time.Millisecond)
	}
	old.Close() // detaches; the holder lingers

	second := openHostedExecDaemon(t, state, 100*time.Millisecond)
	t.Cleanup(func() {
		second.stopAll()
		second.close()
	})
	n := waitNotice(t, second, "the replayed exit")
	if !strings.HasPrefix(n, "exec:"+v.ID+":exit|") || !strings.Contains(n, "exited with code 0") || !strings.Contains(n, "done-under-old") {
		t.Fatalf("notice = %s", n)
	}
	waitFor(t, "the ended job's holder to go", func() bool {
		for _, info := range second.holders.List().Holders {
			if info.ID == v.HolderID {
				return false
			}
		}
		return true
	})
}

// TestHarnessKillSurvivalFixture is the producer side of the harness-kill
// survival check: the job runs under its own holder, not under the harness
// process that asked for it, so stopping that harness (here an
// independently stoppable stand-in process) leaves the job running, and the
// job's end still produces its owner's completion notice for the next
// harness process to take.
func TestHarnessKillSurvivalFixture(t *testing.T) {
	skipWithoutPOSIXShell(t)
	state := t.TempDir()
	d := openHostedExecDaemon(t, state, 100*time.Millisecond)
	t.Cleanup(func() {
		d.stopAll()
		d.close()
	})
	harness := exec.Command("sleep", "60")
	if err := harness.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = harness.Process.Kill(); _ = harness.Wait() })
	root := t.TempDir()
	v, err := d.hosted.Launch(context.Background(), make(chan struct{}), execjob.HostedLaunch{Owner: "lead", Tool: "exec.run", Root: root, Cwd: root, Argv: []string{"/bin/sh", "-c", "sleep 1; echo survived"}, Environ: os.Environ(), Async: true})
	if err != nil || v.Outcome != execjob.OutcomeAsync {
		t.Fatalf("launch = %+v, %v", v, err)
	}
	if err := harness.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = harness.Wait()
	if got, _ := d.hosted.Get("lead", v.ID); got.State != execjob.HostedRunning {
		t.Fatalf("the job did not outlive its harness: %+v", got)
	}
	n := waitNotice(t, d, "the surviving job's end")
	if !strings.HasPrefix(n, "exec:"+v.ID+":exit|") || !strings.Contains(n, "survived") {
		t.Fatalf("notice = %s", n)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// skipWithoutPOSIXShell skips a fixture whose job is a /bin/sh script: the
// holder spawns argv directly, and a Windows runner has no /bin/sh.
func skipWithoutPOSIXShell(t *testing.T) {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("fixture runs a /bin/sh job; Windows has no /bin/sh")
	}
}
