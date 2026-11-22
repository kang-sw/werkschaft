package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// TestUserCommandsOnTheProductionRunnerNeverReadTheKeyRecord drives the user
// command binding over the real runner and the production key store: with the
// lead's key record deleted from disk, the list, a page's revision read, a
// cold-cache list and a click's lookup still answer from the runner's memory,
// (the production store would answer from that file, or not at all).
func TestUserCommandsOnTheProductionRunnerNeverReadTheKeyRecord(t *testing.T) {
	f := newCallerBindingFixture(t)
	root := f.hello.Cwd
	u, err := openUserCommands(t.TempDir(), f.runner, nil, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = u.Close() })
	in := wsusercmd.Input{ID: "build", Label: "Build", Line: "make"}
	cmd, _, err := u.Register(f.lead, in)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// The key record the production store reads: remove every file under the
	// key directory so a read of it would fail or return another root.
	var records []string
	_ = filepath.WalkDir(os.Getenv("WS_CACHE_HOME"), func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.Contains(path, f.leadKey) {
			records = append(records, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if len(records) == 0 {
		t.Fatal("found no key record on disk to remove")
	}
	for _, path := range records {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
	if root, ok := mcp.NewAgentKeyStore().Root(f.leadKey); ok && root != "" {
		t.Fatalf("the key record is still readable (%q)", root)
	}
	for range 25 {
		cmds, rev, err := u.List(f.lead)
		if err != nil || len(cmds) != 1 || cmds[0] != cmd || rev == "" {
			t.Fatalf("list: %+v %q %v", cmds, rev, err)
		}
		if u.Revision(f.lead) != rev {
			t.Fatalf("the stream revision %q is not the list's %q", u.Revision(f.lead), rev)
		}
		got, gotRoot, found, err := u.Get(f.lead, "build")
		if err != nil || !found || got != cmd || !sameRoot(gotRoot, root) {
			t.Fatalf("get: %+v %q %v %v (root %q)", got, gotRoot, found, err, root)
		}
	}
	if _, _, err := u.List("someone-else"); err == nil {
		t.Fatal("a conversation that is not an open lead was listed")
	}
	if err := u.RemoveIf(f.lead, "build", cmd.Revision+1); !errors.Is(err, wsusercmd.ErrStale) {
		t.Fatalf("stale removal: %v", err)
	}
}

// TestHostedLeadConnectionWaitsForTheUserCommandsAtStart: a hosted lead that
// connects while the daemon is still starting is built after the user commands
// settled, so its first tool list already names the user command tools; a
// connection with no conversation never waits.
func TestHostedLeadConnectionWaitsForTheUserCommandsAtStart(t *testing.T) {
	f := newCallerBindingFixture(t)
	ready := make(chan struct{})
	f.host.userCmdsReady = ready
	hello := f.hello
	hello.Conversation = f.lead
	done := make(chan struct{})
	go func() {
		defer close(done)
		if session, err := f.host.Accept(hello); err == nil {
			if s, ok := session.(*proxyMCPSession); ok {
				s.server.Close()
			}
		}
	}()
	select {
	case <-done:
		t.Fatal("a hosted lead was accepted before the user commands settled")
	case <-time.After(150 * time.Millisecond):
	}
	close(ready)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the connection never resumed")
	}
	// No conversation: no wait, even with a channel that never closes.
	f.host.userCmdsReady = make(chan struct{})
	plain := f.hello
	plain.Conversation = ""
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		if session, err := f.host.Accept(plain); err == nil {
			if s, ok := session.(*proxyMCPSession); ok {
				s.server.Close()
			}
		}
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("a connection with no conversation waited")
	}
}
