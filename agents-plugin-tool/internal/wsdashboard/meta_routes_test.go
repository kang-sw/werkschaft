package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubAgentMetaHost struct {
	mu       sync.RWMutex
	records  map[string]AgentRecordSnapshot
	onLookup func(replyID string)
}

func (s *stubAgentMetaHost) Handler() http.Handler                   { return http.NotFoundHandler() }
func (s *stubAgentMetaHost) Start(func(replyID, project string))     {}
func (s *stubAgentMetaHost) InboxChanged(string)                     {}
func (s *stubAgentMetaHost) Close()                                  {}
func (s *stubAgentMetaHost) AgentRecord(replyID string) (AgentRecordSnapshot, bool) {
	s.mu.RLock()
	fn := s.onLookup
	rec, ok := s.records[replyID]
	s.mu.RUnlock()
	if fn != nil {
		fn(replyID)
	}
	return rec, ok
}

func startMetaTestDaemon(t *testing.T, host *stubAgentMetaHost) (*Daemon, string) {
	t.Helper()
	dir := t.TempDir()
	home := t.TempDir()
	d, err := StartDaemon(DaemonOptions{
		Dir:    dir,
		Home:   home,
		Agents: host,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, dir
}

func TestMetaRoutesAuthAndMethodGuards(t *testing.T) {
	host := &stubAgentMetaHost{
		records: map[string]AgentRecordSnapshot{
			"abcd1234": {
				ReplyID: "abcd1234",
				WSKey:   "secret-ws-key-never-wire",
				Profile: "lead-claude",
				Backend: "claude",
				Root:    "/tmp/project",
				Lead:    true,
			},
		},
	}
	d, _ := startMetaTestDaemon(t, host)
	ep := d.Endpoint()
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}

	// As for /shutdown and /full-stop, the exemption covers POST only so a GET
	// meets the access guard (401), and the handler itself still rejects non-POST
	// with 405.
	req, _ := http.NewRequest(http.MethodGet, endpointURL(ep, MetaAgentRecordPath), nil)
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET %s status = %d, want guard 401", MetaAgentRecordPath, resp.StatusCode)
	}
	directGet := httptest.NewRequest(http.MethodGet, endpointURL(ep, MetaAgentRecordPath), nil)
	directGet.RemoteAddr = "127.0.0.1:43210"
	directGet.Header.Set("Authorization", "Bearer "+ep.Token)
	directRec := httptest.NewRecorder()
	d.handleMetaAgentRecord(directRec, directGet)
	if directRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("direct GET %s status = %d, want 405", MetaAgentRecordPath, directRec.Code)
	}

	// Browser Origin header is rejected with 403 even with valid Bearer token.
	for _, path := range []string{MetaAgentRecordPath, MetaHostedDescendantPath, MetaUnregisterCheckPath, MetaPrunePath} {
		req, _ := http.NewRequest(http.MethodPost, endpointURL(ep, path), strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+ep.Token)
		req.Header.Set("Origin", "http://127.0.0.1:9999")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("POST %s with Origin status = %d, want 403", path, resp.StatusCode)
		}
	}

	// Cross-site Fetch metadata header is rejected with 403.
	req, _ = http.NewRequest(http.MethodPost, endpointURL(ep, MetaAgentRecordPath), strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST with Sec-Fetch-Site=cross-site status = %d, want 403", resp.StatusCode)
	}

	// Missing or wrong Bearer token is rejected with 401.
	for _, auth := range []string{"", "Bearer wrong-token", "Basic abc"} {
		req, _ := http.NewRequest(http.MethodPost, endpointURL(ep, MetaAgentRecordPath), strings.NewReader(`{"reply_id":"abcd1234","ws_key":"secret-ws-key-never-wire"}`))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("POST with auth=%q status = %d, want 401", auth, resp.StatusCode)
		}
	}

	// Valid POST returns the snapshot and NEVER serializes WSKey on the wire.
	req, _ = http.NewRequest(http.MethodPost, endpointURL(ep, MetaAgentRecordPath), strings.NewReader(`{"reply_id":"abcd1234","ws_key":"secret-ws-key-never-wire"}`))
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid POST status = %d, body = %s", resp.StatusCode, buf.String())
	}
	if strings.Contains(buf.String(), "secret-ws-key-never-wire") {
		t.Fatalf("wire response leaked WSKey: %s", buf.String())
	}
	var decoded metaAgentRecordResponse
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil || !decoded.Found || decoded.Record == nil || decoded.Record.Backend != "claude" {
		t.Fatalf("decoded = %+v, err = %v", decoded, err)
	}

	// Wrong ws_key answers found=false without leaking the record.
	snap, found, err := FetchAgentRecord(context.Background(), client, d.dir, "abcd1234", "wrong-key")
	if err != nil || found || snap.Backend != "" {
		t.Fatalf("wrong ws_key = %+v, found=%v, err=%v", snap, found, err)
	}
}

func TestMetaRoutesLineageUnregisterAndPrune(t *testing.T) {
	host := &stubAgentMetaHost{
		records: map[string]AgentRecordSnapshot{
			"lead0001": {ReplyID: "lead0001", WSKey: "k-lead", Backend: "claude", Lead: true},
			"child001": {ReplyID: "child001", WSKey: "k-child", Backend: "claude", Parent: &AgentRecordParent{Kind: "lead", ReplyID: "lead0001"}},
			"grand001": {ReplyID: "grand001", WSKey: "k-grand", Backend: "claude", Parent: &AgentRecordParent{Kind: "conversation", ReplyID: "child001"}},
			"active01": {ReplyID: "active01", WSKey: "k-active", Backend: "claude", Lead: true},
		},
	}
	_, dir := startMetaTestDaemon(t, host)
	ctx := context.Background()

	ok, err := CheckHostedDescendant(ctx, nil, dir, "lead0001", "grand001", "k-grand", 16)
	if err != nil || !ok {
		t.Fatalf("grandchild descendant = %v, %v; want true", ok, err)
	}
	ok, err = CheckHostedDescendant(ctx, nil, dir, "lead0001", "grand001", "wrong-key", 16)
	if err != nil || ok {
		t.Fatalf("wrong ws_key descendant = %v, %v; want false", ok, err)
	}
	has, err := HasAgentRecord(ctx, nil, dir, "lead0001")
	if err != nil || !has {
		t.Fatalf("HasAgentRecord(lead0001) = %v, %v", has, err)
	}
	has, err = HasAgentRecord(ctx, nil, dir, "absent01")
	if err != nil || has {
		t.Fatalf("HasAgentRecord(absent01) = %v, %v", has, err)
	}

	// Prune removes expired non-agent directory and keeps active agent directory
	// purely from in-memory AgentMetadataHost (no agent.json file exists on disk).
	old := time.Now().Add(-RegistryRetention - time.Hour).UTC().Format(time.RFC3339)
	if err := WriteRegistration(dir, Registration{ReplyID: "expired1", Project: "/tmp/p", HeartbeatAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := WriteRegistration(dir, Registration{ReplyID: "active01", Project: "/tmp/p", HeartbeatAt: old}); err != nil {
		t.Fatal(err)
	}
	pruned, err := RequestPruneRegistrations(ctx, nil, dir, RegistryRetention)
	if err != nil || pruned != 1 {
		t.Fatalf("RequestPruneRegistrations = %d, %v; want 1", pruned, err)
	}
	if _, err := os.Stat(SessionDir(dir, "expired1")); !os.IsNotExist(err) {
		t.Fatalf("expired1 was not pruned: %v", err)
	}
	if _, err := os.Stat(SessionDir(dir, "active01")); err != nil {
		t.Fatalf("active in-memory agent session was wrongly pruned: %v", err)
	}
}

func TestMetaRoutesDrainOnCloseAndReject503(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	unlock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unlock()
	host := &stubAgentMetaHost{
		records: map[string]AgentRecordSnapshot{
			"busy0001": {ReplyID: "busy0001", WSKey: "k-busy", Backend: "claude"},
		},
		onLookup: func(replyID string) {
			if replyID == "busy0001" {
				enterOnce.Do(func() { close(entered) })
				<-release
			}
		},
	}
	d, _ := startMetaTestDaemon(t, host)
	ep := d.Endpoint()

	// Start an in-flight /meta/agent-record request directly against the handler.
	inFlightDone := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, endpointURL(ep, MetaAgentRecordPath), strings.NewReader(`{"reply_id":"busy0001","ws_key":"k-busy"}`))
		req.RemoteAddr = "127.0.0.1:43210"
		req.Header.Set("Authorization", "Bearer "+ep.Token)
		rec := httptest.NewRecorder()
		d.handleMetaAgentRecord(rec, req)
		inFlightDone <- rec.Code
	}()

	select {
	case <-entered:
	case code := <-inFlightDone:
		t.Fatalf("in-flight meta request returned early with status %d before reaching lookup", code)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight meta request to reach lookup")
	}
	// Trigger closeMeta in the background: it must block until the in-flight
	// meta handler completes and immediately reject new requests with 503.
	closeDone := make(chan struct{})
	go func() {
		d.closeMeta()
		close(closeDone)
	}()

	// Wait until closeMeta has flipped metaClosed under lock. Probe a distinct
	// reply_id so a poll racing ahead of closeMeta never blocks in onLookup.
	deadline := time.Now().Add(2 * time.Second)
	for {
		req := httptest.NewRequest(http.MethodPost, endpointURL(ep, MetaUnregisterCheckPath), strings.NewReader(`{"reply_id":"probe001"}`))
		req.RemoteAddr = "127.0.0.1:43211"
		req.Header.Set("Authorization", "Bearer "+ep.Token)
		rec := httptest.NewRecorder()
		d.handleMetaUnregisterCheck(rec, req)
		if rec.Code == http.StatusServiceUnavailable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 503 while closing, last status = %d", rec.Code)
		}
		time.Sleep(5 * time.Millisecond)
	}

	select {
	case <-closeDone:
		t.Fatal("closeMeta returned while in-flight meta handler was still running")
	default:
	}

	unlock()
	if code := <-inFlightDone; code != http.StatusOK {
		t.Fatalf("in-flight request status = %d, want 200", code)
	}
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("closeMeta did not return after in-flight request finished")
	}
}

func TestMetaHTTPClientReusesLoopbackTransport(t *testing.T) {
	c1 := newMetaHTTPClient(0)
	c2 := newMetaHTTPClient(defaultMetaTimeout)
	c3 := newMetaHTTPClient(5 * time.Second)

	if c1 != c2 {
		t.Fatal("newMetaHTTPClient(0) and newMetaHTTPClient(defaultMetaTimeout) did not reuse defaultMetaClient")
	}
	tr1, ok1 := c1.Transport.(*http.Transport)
	tr3, ok3 := c3.Transport.(*http.Transport)
	if !ok1 || !ok3 || tr1 == nil {
		t.Fatalf("expected *http.Transport, got %T and %T", c1.Transport, c3.Transport)
	}
	if tr1 != tr3 || tr1 != defaultMetaTransport {
		t.Fatal("newMetaHTTPClient did not reuse shared defaultMetaTransport")
	}
	if tr1.Proxy != nil {
		t.Fatal("loopback meta transport must set Proxy: nil")
	}
	if tr1.IdleConnTimeout <= 0 || tr1.MaxIdleConnsPerHost <= 0 {
		t.Fatalf("expected bounded idle pool on defaultMetaTransport, got IdleConnTimeout=%v MaxIdleConnsPerHost=%d", tr1.IdleConnTimeout, tr1.MaxIdleConnsPerHost)
	}

	host := &stubAgentMetaHost{
		records: map[string]AgentRecordSnapshot{
			"reuse001": {ReplyID: "reuse001", WSKey: "k-reuse", Backend: "claude"},
		},
	}
	_, dir := startMetaTestDaemon(t, host)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		has, err := HasAgentRecord(ctx, nil, dir, "reuse001")
		if err != nil || !has {
			t.Fatalf("call %d HasAgentRecord = %v, %v; want true", i, has, err)
		}
	}
}
