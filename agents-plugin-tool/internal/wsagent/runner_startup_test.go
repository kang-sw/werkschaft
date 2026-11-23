package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

func TestDashboardPublishesAfterWorkerRejoinBeforeClosedPresentation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h, _ := workerRecoveryHarness(t, BackendClaude)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	h.runner.SetEndpoint(fmt.Sprintf("http://127.0.0.1:%d", port))
	worker, _ := spawnHeldRecoveryWorker(t, h, BackendClaude, "workdir-write")
	token := h.record(worker).MCPToken
	if token == "" {
		t.Fatal("worker started without a persisted bearer")
	}
	h.runner.Close()
	h.holders.restart()

	const retained = 64
	dir := h.opts.Dir
	for i := range retained {
		id := fmt.Sprintf("closed%04d", i)
		session := wsdashboard.SessionDir(dir, id)
		if err := os.MkdirAll(session, 0o700); err != nil {
			t.Fatal(err)
		}
		rec := Record{ReplyID: id, WSKey: "fixture", Root: h.root, Backend: BackendClaude,
			ClosedAt: "2026-11-22T12:00:00Z", Parent: &ParentRef{Kind: ParentConversation, ReplyID: worker}}
		if err := writeRecord(session, rec); err != nil {
			t.Fatal(err)
		}
		snapshot, err := json.Marshal(wslive.Snapshot{ReplyID: id})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wslive.SnapshotPath(session), snapshot, 0o600); err != nil {
			t.Fatal(err)
		}
		evidence := wslive.ObservedRecord{ModelObservation: wslive.ModelObservation{
			ID: "fixture-response", Model: "fixture-observed", Source: "claude.assistant", At: 1,
		}}
		if err := wslive.AppendObserved(session, []wslive.ObservedRecord{evidence}); err != nil {
			t.Fatal(err)
		}
	}
	scanning, releaseScan := h.gateScan()
	hydrating, hydrate := make(chan struct{}), make(chan struct{})
	var reading, release sync.Once
	releaseHydration := func() { release.Do(func() { close(hydrate) }) }
	h.opts.closedSnapshotRead = func(string) { reading.Do(func() { close(hydrating); <-hydrate }) }
	runner := NewRunner(h.opts)
	var daemon *wsdashboard.Daemon
	started := make(chan struct{})
	home := t.TempDir()
	go func() {
		daemon, err = wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: dir, Home: home, Port: port, Agents: runner})
		close(started)
	}()
	defer func() {
		releaseScan()
		releaseHydration()
		<-started
		if daemon != nil {
			daemon.Close()
		}
	}()
	wait(t, "holder scan", scanning)
	if _, err := wsdashboard.ReadEndpoint(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("endpoint was visible before bearer restore: %v", err)
	}
	releaseScan()
	wait(t, "daemon startup", started)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, "closed presentation read", hydrating)
	if hello, _, ok := runner.ResolveMCPToken(token); !ok || hello.Conversation != worker {
		t.Fatal("initially published daemon did not restore the held worker")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	if _, err := wsdashboard.Health(ctx, client, daemon.Endpoint()); err != nil {
		t.Fatalf("health with presentation blocked: %v", err)
	}
	parent := ParentRef{Kind: ParentConversation, ReplyID: worker}
	if rows := runner.Children(parent); len(rows) != retained || rows[0].Observed != nil {
		t.Fatalf("pending child rail: got %d rows", len(rows))
	}
	releaseHydration()
	eventually(t, "observations in the child rail", func() bool {
		for _, row := range runner.Children(parent) {
			if row.Observed == nil || row.Observed.Model != "fixture-observed" {
				return false
			}
		}
		return true
	})
}
