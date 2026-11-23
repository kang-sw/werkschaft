package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsholder"
)

const dashboardAuditClaude = `#!/usr/bin/env python3
import json
import sys

if "--version" in sys.argv:
    print("2.1.330 (Claude Code)")
    sys.exit()

for line in sys.stdin:
    if json.loads(line)["type"] != "user":
        continue
    # Only the delta carries the thinking body. The Claude lead's holder
    # skips that delta in its ring and can deliver it only over the audit tee.
    print('{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1"}}}')
    print('{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}}')
    print('{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"dashboard audit tee body"}}}')
    print('{"type":"stream_event","event":{"type":"content_block_stop","index":0}}')
    print('{"type":"assistant","message":{"id":"msg_1","content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"text","text":"dashboard audit fixture reply"}]}}')
    print('{"type":"result","subtype":"success","result":"dashboard audit fixture reply"}', flush=True)
`

func TestDashboardHostedAudit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		blocked bool
	}{
		{name: "live capture"},
		{name: "blocked store", blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			daemon, runner := startDashboardAuditFixture(t, tc.blocked)
			store := daemon.AuditStore()
			if tc.blocked != (store == nil) || tc.blocked != (daemon.AuditSink() == nil) {
				t.Fatal("daemon audit store and sink availability disagree with the fixture")
			}

			root := t.TempDir()
			if err := exec.Command("git", "-C", root, "init", "-q").Run(); err != nil {
				t.Fatalf("init hosted project: %v", err)
			}
			st, err := runner.CreateLead(wsagent.BackendClaude, root, wsagent.LeadOptions{})
			if err != nil {
				t.Fatal("create hosted Claude lead failed")
			}
			t.Cleanup(func() {
				if _, err := runner.CloseConversation(st.ID); err != nil {
					t.Error("close hosted Claude lead failed")
				}
			})
			if _, err := runner.Send(st.ID, "Run the dashboard audit fixture"); err != nil {
				t.Fatal("send to hosted Claude lead failed")
			}
			turn, err := runner.Wait(context.Background(), st.ID, 10*time.Second)
			if err != nil || turn.Status != wsagent.TurnDone || turn.Message != "dashboard audit fixture reply" {
				t.Fatalf("hosted Claude lead did not finish with the fixture reply (turn status %q)", turn.Status)
			}
			if tc.blocked {
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var kept, thinking bool
			waitDashboardAudit(t, "kept result and thinking delta in the audit store", func() bool {
				events, err := store.Events(ctx, st.ID)
				if err != nil {
					t.Fatalf("read hosted audit events: %v", err)
				}
				for _, ev := range events {
					switch ev.Kind {
					case wsaudit.KindSystem:
						kept = kept || (ev.Subkind == "result:success" && strings.Contains(ev.Out, "dashboard audit fixture reply"))
					case wsaudit.KindThinking:
						thinking = thinking || (ev.Out == "dashboard audit tee body" && ev.Meta[wsaudit.MetaIncomplete] == nil)
					}
				}
				return kept && thinking
			})
		})
	}
}

func startDashboardAuditFixture(t *testing.T, blocked bool) (*wsdashboard.Daemon, *wsagent.Runner) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake Claude is a Unix shebang executable")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	if err := exec.Command(python, "-c", "import sys; sys.exit(sys.version_info < (3, 10))").Run(); err != nil {
		t.Skip("holder.py requires Python 3.10 or newer")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(dashboardAuditClaude), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(wsholder.PythonEnv, python)
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	dir, err := wsdashboard.Dir("")
	if err != nil {
		t.Fatal(err)
	}
	if blocked {
		// A regular file blocks the audit directory even on a privileged host.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "audit"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	holders := newDashboardHolders(dir, io.Discard, &hostedExecLink{})
	runner := newAgentRunner(dir, io.Discard, nil, nil, holders, nil, nil)
	daemon, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir: dir, Home: t.TempDir(), Agents: runner, Holders: holders,
	})
	if err != nil {
		runner.Close()
		holders.Close()
		t.Fatal("start isolated dashboard daemon failed")
	}
	t.Cleanup(daemon.Close)
	return daemon, runner
}

func waitDashboardAudit(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
