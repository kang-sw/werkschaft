package main

import (
	"bytes"
	"database/sql"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wscron"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

// TestOpenCronRefusesANewerStoreAndLogsIt: a schedule database stamped by a
// newer schema is left untouched and the daemon runs without the tool, with
// the reason logged, never a crash.
func TestOpenCronRefusesANewerStoreAndLogsIt(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, wscron.StoreName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version=99`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	var logw bytes.Buffer
	runner := wsagent.NewRunner(wsagent.Options{Dir: t.TempDir()})
	if sched := openCron(dir, runner, &logw); sched != nil {
		t.Fatal("a newer store opened")
	}
	if !strings.Contains(logw.String(), "cron unavailable") || !strings.Contains(logw.String(), "newer schema") {
		t.Fatalf("log = %q", logw.String())
	}
}

// TestCronHostForgetsASessionOnlyWhenTheRunnerDoes: the index-forget hook
// deletes a conversation's schedules once the runner no longer knows it.
func TestCronHostForgetsASessionOnlyWhenTheRunnerDoes(t *testing.T) {
	holders := wsholder.New(wsholder.Options{Dir: filepath.Join(t.TempDir(), wsholder.DirName)})
	t.Cleanup(holders.Close)
	runner := newAgentRunner(t.TempDir(), io.Discard, nil, nil, holders, nil, nil, nil)
	host := &cronHost{}
	host.sessionForgotten(runner, "before-open") // no scheduler yet: nothing to do
	dir := t.TempDir()
	store, err := wscron.OpenStore(filepath.Join(dir, wscron.StoreName))
	if err != nil {
		t.Fatal(err)
	}
	sessions := &fixedLeads{leads: map[string]bool{"gone": true}}
	sched, err := wscron.New(wscron.Options{Store: store, Sessions: sessions})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sched.Close() })
	host.sched.Store(sched)
	if _, _, err := sched.Add("gone", "tick", "@daily", "p", ""); err != nil {
		t.Fatal(err)
	}
	// The runner knows no conversation "gone", so the hook removes it.
	host.sessionForgotten(runner, "gone")
	deadline := time.Now().Add(5 * time.Second)
	for len(sched.List("gone")) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the schedule of a conversation the runner does not know was kept")
		}
		time.Sleep(10 * time.Millisecond)
	}
	host.close(io.Discard)
	host.close(io.Discard) // idempotent
}

type fixedLeads struct{ leads map[string]bool }

func (s *fixedLeads) IsLead(id string) (bool, error) { return s.leads[id], nil }
func (s *fixedLeads) Exists(id string) bool          { return s.leads[id] }
func (s *fixedLeads) HeldSchedules(string) []string  { return nil }
func (s *fixedLeads) Deliver(string, wsinbox.Cron, string) (wscron.Delivery, error) {
	return wscron.Delivered, nil
}
