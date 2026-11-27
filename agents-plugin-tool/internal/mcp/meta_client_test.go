package mcp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wstestutil"
)

// testRecordReader is an in-memory AgentRecordReader for unit tests that build
// a Server without a live dashboard daemon.
type testRecordReader struct {
	mu      sync.RWMutex
	records map[string]wsagent.Record
}

func newTestRecordReader(recs ...wsagent.Record) *testRecordReader {
	r := &testRecordReader{records: make(map[string]wsagent.Record, len(recs))}
	for _, rec := range recs {
		r.records[rec.ReplyID] = rec
	}
	return r
}

func (r *testRecordReader) Set(rec wsagent.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[rec.ReplyID] = rec
}

func (r *testRecordReader) Delete(replyID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.records, replyID)
}

func (r *testRecordReader) RecordByReplyID(replyID string) (wsagent.Record, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.records[replyID]
	return rec, ok
}

func TestMetaClientDirectCallFailsWhenDaemonUnavailable(t *testing.T) {
	cache := t.TempDir()
	cfg := t.TempDir()
	root := t.TempDir()
	t.Setenv("WS_CACHE_HOME", cache)
	t.Setenv("WS_CONFIG_HOME", cfg)
	initGit(t, root)

	var ensureCalls atomic.Int32
	s := NewServerWithOptions(ServerOptions{
		Root:         root,
		Version:      "test",
		DirectCall:   true,
		EnsureDaemon: func() { ensureCalls.Add(1) },
	})
	defer s.Close()

	key, err := NewAgentKeyStore().Mint(root)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.lookupAgentRecord(key)
	if err == nil {
		t.Fatal("expected daemon-unavailable error for raw DirectCall")
	}
	if ensureCalls.Load() != 0 {
		t.Fatalf("raw DirectCall must never auto-start daemon; ensureCalls=%d", ensureCalls.Load())
	}
	msg := err.Error()
	wantCache := wstestutil.CanonicalPath(cache)
	for _, want := range []string{"ws-mcp daemon-start", wantCache, cfg} {
		if !strings.Contains(msg, want) {
			t.Errorf("DirectCall unavailable error %q missing %q", msg, want)
		}
	}
}

func TestMetaClientDirectStdioEnsuresOnceThenRecoversOrFailsRecoverably(t *testing.T) {
	t.Run("recovers after EnsureDaemon starts daemon", func(t *testing.T) {
		dir := dashboardSessionEnv(t)
		root := dashboardGitRoot(t)
		key, err := NewAgentKeyStore().Mint(root)
		if err != nil {
			t.Fatal(err)
		}
		rec := wsagent.Record{
			ReplyID:   wsmailbox.ReplyID(key),
			WSKey:     key,
			Profile:   wsagent.SupportProfileClaude,
			Backend:   wsagent.BackendClaude,
			Root:      root,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err := wsagent.WriteRecordForTest(dir, rec); err != nil {
			t.Fatal(err)
		}

		var daemon *wsdashboard.Daemon
		var runner *wsagent.Runner
		var ensureCalls atomic.Int32
		s := NewServerWithOptions(ServerOptions{
			Root:        root,
			Version:     "test",
			DirectStdio: true,
			EnsureDaemon: func() {
				ensureCalls.Add(1)
				runner = wsagent.NewRunner(wsagent.Options{Dir: dir, Log: io.Discard})
				var err error
				daemon, err = wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
					Dir:    dir,
					Home:   root,
					Agents: runner,
				})
				if err != nil {
					t.Errorf("StartDaemon: %v", err)
				}
			},
		})
		defer func() {
			s.Close()
			if daemon != nil {
				daemon.Close()
			}
			if runner != nil {
				runner.Close()
			}
		}()

		got, ok, err := s.lookupAgentRecord(key)
		if err != nil || !ok {
			t.Fatalf("lookupAgentRecord after EnsureDaemon = %+v, %v, %v", got, ok, err)
		}
		if ensureCalls.Load() != 1 {
			t.Fatalf("ensureCalls = %d, want 1", ensureCalls.Load())
		}
		if got.Profile != rec.Profile || got.Backend != rec.Backend || got.WSKey != key {
			t.Fatalf("got record = %+v, want profile=%s backend=%s", got, rec.Profile, rec.Backend)
		}
	})

	t.Run("returns recoverable error and ensures only once when daemon stays unavailable", func(t *testing.T) {
		cache := t.TempDir()
		cfg := t.TempDir()
		root := t.TempDir()
		t.Setenv("WS_CACHE_HOME", cache)
		t.Setenv("WS_CONFIG_HOME", cfg)
		initGit(t, root)

		var ensureCalls atomic.Int32
		s := NewServerWithOptions(ServerOptions{
			Root:        root,
			Version:     "test",
			DirectStdio: true,
			EnsureDaemon: func() {
				ensureCalls.Add(1)
			},
		})
		defer s.Close()

		key, err := NewAgentKeyStore().Mint(root)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err1 := s.lookupAgentRecord(key)
		if err1 == nil {
			t.Fatal("expected recoverable daemon-unavailable error")
		}
		wantCache := wstestutil.CanonicalPath(cache)
		for _, want := range []string{"recoverable", "ws-mcp daemon-start", wantCache, cfg} {
			if !strings.Contains(err1.Error(), want) {
				t.Errorf("DirectStdio error %q missing %q", err1.Error(), want)
			}
		}
		_, _, err2 := s.lookupAgentRecord(key)
		if err2 == nil {
			t.Fatal("expected second call to also fail")
		}
		if ensureCalls.Load() != 1 {
			t.Fatalf("EnsureDaemon should run at most once per server; got %d", ensureCalls.Load())
		}
	})
}

func TestMetaClientHTTPRoundTripsOverVerifiedDaemon(t *testing.T) {
	dir := dashboardSessionEnv(t)
	root := dashboardGitRoot(t)

	leadKey, err := NewAgentKeyStore().MintLead(root)
	if err != nil {
		t.Fatal(err)
	}
	childKey, err := NewAgentKeyStore().Mint(root)
	if err != nil {
		t.Fatal(err)
	}
	grandKey, err := NewAgentKeyStore().Mint(root)
	if err != nil {
		t.Fatal(err)
	}
	peerKey, err := NewAgentKeyStore().MintLead(root)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, rec := range []wsagent.Record{
		{ReplyID: wsmailbox.ReplyID(leadKey), WSKey: leadKey, Root: root, CreatedAt: now, Backend: wsagent.BackendClaude, Profile: wsagent.LeadProfileClaude, Lead: true},
		{ReplyID: wsmailbox.ReplyID(childKey), WSKey: childKey, Root: root, CreatedAt: now, Backend: wsagent.BackendClaude, Profile: wsagent.AgentWorkerProfile(wsagent.BackendClaude),
			ResolvedDefinition: &wsagent.Profile{Backend: wsagent.BackendClaude, WSTools: []string{"tracker.query"}},
			Parent:             &wsagent.ParentRef{Kind: wsagent.ParentLead, ReplyID: wsmailbox.ReplyID(leadKey)}},
		{ReplyID: wsmailbox.ReplyID(grandKey), WSKey: grandKey, Root: root, CreatedAt: now, Backend: wsagent.BackendClaude, Profile: wsagent.AgentWorkerProfile(wsagent.BackendClaude),
			Parent: &wsagent.ParentRef{Kind: wsagent.ParentConversation, ReplyID: wsmailbox.ReplyID(childKey)}},
		{ReplyID: wsmailbox.ReplyID(peerKey), WSKey: peerKey, Root: root, CreatedAt: now, Backend: wsagent.BackendClaude, Profile: wsagent.LeadProfileClaude, Lead: true},
	} {
		if err := wsagent.WriteRecordForTest(dir, rec); err != nil {
			t.Fatal(err)
		}
	}

	runner := wsagent.NewRunner(wsagent.Options{Dir: dir, Log: io.Discard})
	daemon, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir:    dir,
		Home:   root,
		Agents: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		daemon.Close()
		runner.Close()
	}()

	// Out-of-daemon server (no in-process agents or recordReader) talks to the
	// daemon over loopback /meta/* endpoints.
	s := NewServerWithOptions(ServerOptions{Root: root, Version: "test"})
	defer s.Close()

	rec, ok, err := s.lookupAgentRecord(childKey)
	if err != nil || !ok {
		t.Fatalf("lookupAgentRecord(child) = %+v, %v, %v", rec, ok, err)
	}
	if rec.ResolvedDefinition == nil || len(rec.ResolvedDefinition.WSTools) != 1 || rec.ResolvedDefinition.WSTools[0] != "tracker.query" {
		t.Fatalf("ResolvedDefinition did not round-trip: %+v", rec.ResolvedDefinition)
	}
	if rec.Parent == nil || rec.Parent.ReplyID != wsmailbox.ReplyID(leadKey) {
		t.Fatalf("Parent did not round-trip: %+v", rec.Parent)
	}

	desc, err := s.lookupHostedDescendant(leadKey, grandKey)
	if err != nil || !desc {
		t.Fatalf("lookupHostedDescendant(lead, grand) = %v, %v; want true", desc, err)
	}
	desc, err = s.lookupHostedDescendant(leadKey, peerKey)
	if err != nil || desc {
		t.Fatalf("lookupHostedDescendant(lead, peer) = %v, %v; want false", desc, err)
	}

	has, err := s.lookupHasAgentRecord(dir, wsmailbox.ReplyID(leadKey))
	if err != nil || !has {
		t.Fatalf("lookupHasAgentRecord(lead) = %v, %v; want true", has, err)
	}
	has, err = s.lookupHasAgentRecord(dir, "absent99")
	if err != nil || has {
		t.Fatalf("lookupHasAgentRecord(absent) = %v, %v; want false", has, err)
	}

	if _, err := wsdashboard.RequestPruneRegistrations(context.Background(), nil, dir, wsdashboard.RegistryRetention); err != nil {
		t.Fatalf("RequestPruneRegistrations: %v", err)
	}
}

func TestServerMetaHTTPClientReusesTransportAndClosesIdle(t *testing.T) {
	dir := dashboardSessionEnv(t)
	root := dashboardGitRoot(t)

	// Closing a server that never called metaHTTPClient must be safe.
	unused := NewServerWithOptions(ServerOptions{Root: root, Version: "test"})
	unused.Close()

	daemon, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir:  dir,
		Home: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()

	s := NewServerWithOptions(ServerOptions{Root: root, Version: "test"})
	c1 := s.metaHTTPClient()
	c2 := s.metaHTTPClient()
	if c1 != c2 {
		s.Close()
		t.Fatal("Server.metaHTTPClient did not reuse *http.Client across calls")
	}
	tr, ok := c1.Transport.(*http.Transport)
	if !ok || tr == nil {
		s.Close()
		t.Fatalf("expected *http.Transport, got %T", c1.Transport)
	}
	if tr != s.metaTransport {
		s.Close()
		t.Fatal("Server.metaHTTPClient transport does not match Server.metaTransport")
	}
	if tr.Proxy != nil {
		s.Close()
		t.Fatal("Server.metaHTTPClient transport must set Proxy: nil")
	}
	if tr.IdleConnTimeout <= 0 || tr.MaxIdleConnsPerHost <= 0 {
		s.Close()
		t.Fatalf("expected bounded idle pool on Server.metaTransport, got IdleConnTimeout=%v MaxIdleConnsPerHost=%d", tr.IdleConnTimeout, tr.MaxIdleConnsPerHost)
	}

	for i := 0; i < 3; i++ {
		has, err := s.lookupHasAgentRecord(dir, "absent99")
		if err != nil || has {
			s.Close()
			t.Fatalf("call %d lookupHasAgentRecord = %v, %v; want false, nil", i, has, err)
		}
		if s.metaHTTPClient().Transport != tr {
			s.Close()
			t.Fatalf("call %d replaced metaHTTPClient transport", i)
		}
	}
	s.Close()
}
