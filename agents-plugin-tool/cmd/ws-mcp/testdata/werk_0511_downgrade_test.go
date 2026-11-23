// This test builds against the v0.51.1 tag, not the current module.
// To exercise it from the repository root:
//
//	tmp=$(mktemp -d)
//	git archive v0.51.1 agents-plugin-tool agents-plugin agents-plugin-wsflow | tar -x -C "$tmp"
//	cp agents-plugin-tool/cmd/ws-mcp/testdata/werk_0511_downgrade_test.go "$tmp/agents-plugin-tool/cmd/ws-mcp/"
//	go -C "$tmp/agents-plugin-tool" test -c ./cmd/ws-mcp -o "$tmp/werk-0511.test"
//	WS_WERK_0511_TEST_BINARY="$tmp/werk-0511.test" go -C agents-plugin-tool test -v ./cmd/ws-mcp -run '^TestWerkRejoinsAuthenticatedExecAcrossDaemonReplacement$' -count=1
package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsholder"
)

func TestWerk0511DowngradeFixture(t *testing.T) {
	state, home := os.Getenv("WS_WERK_0511_STATE"), os.Getenv("WS_WERK_0511_HOME")
	id, jobID, holderID := os.Getenv("WS_WERK_0511_WORKER"), os.Getenv("WS_WERK_0511_JOB"), os.Getenv("WS_WERK_0511_HOLDER")
	if state == "" || home == "" || id == "" || jobID == "" || holderID == "" {
		t.Fatal("the downgrade fixture requires the active state from the newer runner")
	}
	projection := filepath.Join(wsdashboard.SessionDir(state, id), "agent.json")
	before, err := os.ReadFile(projection)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := wsagent.ReadRecord(state, id)
	if err != nil || rec.HandedOffAt == "" || rec.TurnOpen {
		t.Fatal("legacy history did not expose the fenced projection")
	}
	var reports atomic.Int32
	link := &hostedExecLink{}
	holders := wsholder.New(wsholder.Options{
		Dir: filepath.Join(state, wsholder.DirName), Log: io.Discard,
		OnExit: link.onExit(persistHolderExit(state)),
	})
	holders.AddSink(link)
	r := wsagent.NewRunner(wsagent.Options{
		Dir: state, Config: wsconfig.Options{ConfigHome: home},
		Holders: wsagent.ManagerHost{Manager: holders},
		Report:  func(wsagent.TurnReport) { reports.Add(1) },
	})
	r.ExpectExecJobs()
	d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: state, Home: home, Version: "0.51.1", Holders: holders, Agents: r})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	jobs := openHostedExec(state, holders, r, link, io.Discard, r.ExecJobsChanged)
	if jobs == nil {
		t.Fatal("0.51.1 could not open the job store")
	}
	t.Cleanup(func() { _ = jobs.Close() })
	r.BindExecJobs(jobs)
	rows, _ := jobs.List(id)
	if len(rows) != 1 || rows[0].ID != jobID || rows[0].HolderID != holderID || rows[0].State != execjob.HostedRunning {
		t.Fatal("0.51.1 failed to retain the original running job")
	}
	d.Close()
	after, err := os.ReadFile(projection)
	if err != nil || !bytes.Equal(before, after) || reports.Load() != 0 {
		t.Fatal("0.51.1 rewrote the projected history or reported the active turn")
	}
}
