package wsdashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

type fakeConvSessionSource struct{}

func (fakeConvSessionSource) ReadSession(reg Registration) (SessionData, bool, error) {
	return SessionData{}, true, nil
}

type fakeConvAgentHost struct {
	records map[string]AgentRecordSnapshot
	revived []string
	onRevive func(id string) error
}

func (f *fakeConvAgentHost) Handler() http.Handler {
	return http.NotFoundHandler()
}

func (f *fakeConvAgentHost) Start(func(string, string)) {}
func (f *fakeConvAgentHost) InboxChanged(string)        {}
func (f *fakeConvAgentHost) Close()                     {}

func (f *fakeConvAgentHost) AgentRecord(id string) (AgentRecordSnapshot, bool) {
	rec, ok := f.records[id]
	return rec, ok
}

func (f *fakeConvAgentHost) ConvHistoryAccess(id string) bool {
	rec, ok := f.records[id]
	return ok && rec.Lead && rec.ConvHistoryAccess
}

func (f *fakeConvAgentHost) ReviveLead(id string) error {
	f.revived = append(f.revived, id)
	if f.onRevive != nil {
		return f.onRevive(id)
	}
	if rec, ok := f.records[id]; ok {
		rec.ClosedAt = ""
		f.records[id] = rec
	}
	return nil
}

func writeTestReg(t *testing.T, dir string, reg Registration) {
	t.Helper()
	if err := WriteRegistration(dir, reg); err != nil {
		t.Fatalf("WriteRegistration(%s): %v", reg.ReplyID, err)
	}
}

func TestDaemonConvHostIncrementalBackfillRevealAndPrune(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	wsA := filepath.Join(dir, "proj-a")
	wsB := filepath.Join(dir, "proj-b")
	_ = os.MkdirAll(wsA, 0o755)
	_ = os.MkdirAll(wsB, 0o755)

	// Pre-populate an archived lead's ring BEFORE starting the daemon to test backfill.
	archivedID := "leadarchived001"
	writeTestReg(t, dir, Registration{
		ReplyID:      archivedID,
		Project:      wsA,
		Harness:      "claude",
		Hosted:       true,
		RegisteredAt: time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
	})
	if err := WriteSessionHidden(dir, archivedID, true); err != nil {
		t.Fatalf("WriteSessionHidden archived: %v", err)
	}
	archivedRing := wslive.RingDir(SessionDir(dir, archivedID))
	if err := wslive.AppendRing(archivedRing, []wslive.RingEntry{
		{Kind: "user", Text: "legacy backfill secret phrase"},
	}); err != nil {
		t.Fatalf("AppendRing archived: %v", err)
	}

	// Pre-populate a closed lead (within 7-day retention) and an expired closed lead (>7 days).
	closedID := "leadclosed00001"
	writeTestReg(t, dir, Registration{
		ReplyID:      closedID,
		Project:      wsB,
		Harness:      "codex",
		Hosted:       true,
		RegisteredAt: time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
		EndedAt:      time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339),
	})
	if err := wslive.AppendRing(wslive.RingDir(SessionDir(dir, closedID)), []wslive.RingEntry{
		{Kind: "reply", Text: "billing workspace migration checklist"},
	}); err != nil {
		t.Fatalf("AppendRing closed: %v", err)
	}

	expiredID := "leadexpired0001"
	writeTestReg(t, dir, Registration{
		ReplyID:      expiredID,
		Project:      wsA,
		Harness:      "claude",
		Hosted:       true,
		RegisteredAt: time.Now().Add(-10 * 24 * time.Hour).UTC().Format(time.RFC3339),
		EndedAt:      time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339),
	})
	if err := wslive.AppendRing(wslive.RingDir(SessionDir(dir, expiredID)), []wslive.RingEntry{
		{Kind: "reply", Text: "ancient pruned needle token"},
	}); err != nil {
		t.Fatalf("AppendRing expired: %v", err)
	}

	// Active child session under leadA.
	childID := "workerchild0001"
	writeTestReg(t, dir, Registration{
		ReplyID:      childID,
		Project:      wsA,
		Harness:      "claude",
		Agent:        true,
		RegisteredAt: time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
	})
	if err := wslive.AppendRing(wslive.RingDir(SessionDir(dir, childID)), []wslive.RingEntry{
		{Kind: "reply", Text: "child worker diagnostic output"},
	}); err != nil {
		t.Fatalf("AppendRing child: %v", err)
	}

	// Active live lead session.
	liveID := "leadlive0000001"
	writeTestReg(t, dir, Registration{
		ReplyID:      liveID,
		Project:      wsA,
		Harness:      "agy",
		Hosted:       true,
		RegisteredAt: time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339),
	})

	// Unpinned non-hosted active session (simulates external receiver-process write).
	nonHostedID := "extsession00001"
	writeTestReg(t, dir, Registration{
		ReplyID:      nonHostedID,
		Project:      wsA,
		Harness:      "claude",
		RegisteredAt: time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339),
	})

	agentHost := &fakeConvAgentHost{
		records: map[string]AgentRecordSnapshot{
			archivedID: {ReplyID: archivedID, Root: wsA, Backend: "claude", Lead: true},
			closedID:   {ReplyID: closedID, Root: wsB, Backend: "codex", Lead: true, ClosedAt: time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)},
			liveID:     {ReplyID: liveID, Root: wsA, Backend: "agy", Lead: true, ConvHistoryAccess: true},
			childID:    {ReplyID: childID, Root: wsA, Backend: "claude", Lead: false, Parent: &AgentRecordParent{ReplyID: liveID}},
		},
	}

	d, err := StartDaemon(DaemonOptions{
		Dir:      dir,
		Home:     t.TempDir(),
		Port:     0,
		Version:  "test",
		Sessions: fakeConvSessionSource{},
		Agents:   agentHost,
		LockWait: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StartDaemon: %v", err)
	}
	defer d.Close()

	// Wire revive hook so registration + visibility sidecar update like real wsagent does.
	agentHost.onRevive = func(id string) error {
		reg, ok := d.index.Session(id)
		if !ok {
			return nil
		}
		if err := WriteSessionHidden(dir, id, false); err != nil {
			return err
		}
		reg.EndedAt = ""
		if err := WriteRegistration(dir, reg.Registration); err != nil {
			return err
		}
		rec := agentHost.records[id]
		rec.ClosedAt = ""
		agentHost.records[id] = rec
		return nil
	}

	// Wait for startup backfill to finish indexing pre-existing rings.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err := d.ConvSearch(ctx, ConvSearchRequest{CallerWorkspace: wsA, Query: "backfill"})
		if err == nil && res.Indexing == "" && len(res.Hits) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 1. Incremental ring append on live lead is indexed automatically via wslive observer.
	liveRing := wslive.RingDir(SessionDir(dir, liveID))
	wslive.PinRing(liveRing)
	defer wslive.DropRing(liveRing)
	if err := wslive.AppendRing(liveRing, []wslive.RingEntry{
		{Kind: "user", Text: "incremental observer token alpha"},
	}); err != nil {
		t.Fatalf("AppendRing live: %v", err)
	}
	d.FlushConvIndexDeltas()

	incRes, err := d.ConvSearch(ctx, ConvSearchRequest{CallerWorkspace: wsA, Query: "incremental observer"})
	if err != nil || len(incRes.Hits) != 1 || incRes.Hits[0].SessionID != liveID {
		t.Fatalf("incremental search = %+v (err=%v)", incRes, err)
	}

	// 2. Workspace scoping: default current workspace excludes wsB; workspace="all" includes wsB.
	wsARes, err := d.ConvSearch(ctx, ConvSearchRequest{CallerWorkspace: wsA, Query: "billing"})
	if err != nil || len(wsARes.Hits) != 0 {
		t.Fatalf("default workspace search leaked wsB: %+v (err=%v)", wsARes, err)
	}
	allRes, err := d.ConvSearch(ctx, ConvSearchRequest{CallerWorkspace: wsA, Workspace: "all", Query: "billing"})
	if err != nil || len(allRes.Hits) != 1 || allRes.Hits[0].SessionID != closedID {
		t.Fatalf("workspace=all search = %+v (err=%v)", allRes, err)
	}

	// 3. ConvSessions: include_children=false hides child, include_children=true shows child.
	sessNoChild, err := d.ConvSessions(ctx, ConvSessionsRequest{CallerWorkspace: wsA})
	if err != nil {
		t.Fatalf("ConvSessions no child: %v", err)
	}
	for _, item := range sessNoChild.Sessions {
		if item.SessionID == childID {
			t.Fatalf("ConvSessions returned child when include_children=false")
		}
	}
	sessWithChild, err := d.ConvSessions(ctx, ConvSessionsRequest{CallerWorkspace: wsA, IncludeChildren: true})
	if err != nil {
		t.Fatalf("ConvSessions with child: %v", err)
	}
	foundChild := false
	for _, item := range sessWithChild.Sessions {
		if item.SessionID == childID {
			foundChild = true
			if !item.IsChild || item.Parent != liveID {
				t.Fatalf("child metadata mismatch: %+v", item)
			}
		}
	}
	if !foundChild {
		t.Fatalf("ConvSessions with include_children=true omitted child")
	}

	// 4. Child rings ARE readable via ConvHistory and ConvRead.
	childHist, err := d.ConvHistory(ctx, ConvHistoryRequest{SessionID: childID})
	if err != nil || len(childHist.Items) != 1 {
		t.Fatalf("ConvHistory child = %+v (err=%v)", childHist, err)
	}
	childRead, err := d.ConvRead(ctx, ConvReadRequest{SessionID: childID, EntryID: childHist.Items[0].EntryID})
	if err != nil || childRead.Entry == nil || !strings.Contains(childRead.Entry.Text, "child worker diagnostic output") {
		t.Fatalf("ConvRead child = %+v (err=%v)", childRead, err)
	}

	// 5. Unpinned active non-hosted session receiver write reconciled on first conv.* touch.
	if err := wslive.AppendRing(wslive.RingDir(SessionDir(dir, nonHostedID)), []wslive.RingEntry{
		{Kind: "user", Text: "external receiver process write"},
	}); err != nil {
		t.Fatalf("AppendRing non-hosted: %v", err)
	}
	// Clear from index to simulate receiver write from a separate process (no in-process observer).
	if err := d.convIdx.DeleteSession(ctx, nonHostedID); err != nil {
		t.Fatalf("DeleteSession non-hosted: %v", err)
	}
	// First ConvSessions sees it as unindexed -> reports indexing: partial and triggers async backfill!
	partialRes, err := d.ConvSessions(ctx, ConvSessionsRequest{CallerWorkspace: wsA, Query: nonHostedID})
	if err != nil {
		t.Fatalf("ConvSessions partial: %v", err)
	}
	if partialRes.Indexing != "partial" {
		t.Fatalf("expected indexing=partial for unindexed session, got %q", partialRes.Indexing)
	}
	// Wait for async backfill triggered by ConvSessions.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, err := d.ConvSearch(ctx, ConvSearchRequest{CallerWorkspace: wsA, Query: "external receiver"})
		if err == nil && len(r.Hits) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	extSearch, err := d.ConvSearch(ctx, ConvSearchRequest{CallerWorkspace: wsA, Query: "external receiver"})
	if err != nil || len(extSearch.Hits) != 1 || extSearch.Hits[0].SessionID != nonHostedID {
		t.Fatalf("non-hosted reconciled search = %+v (err=%v)", extSearch, err)
	}

	// 6. conv.reveal:
	// - Without revive on archived -> clear error
	if _, err := d.ConvRevealSession(ctx, ConvRevealRequest{SessionID: archivedID}); err == nil || !strings.Contains(err.Error(), "revive: true") {
		t.Fatalf("reveal archived without revive = %v, want revive hint", err)
	}
	// - With revive on child -> clear error
	if _, err := d.ConvRevealSession(ctx, ConvRevealRequest{SessionID: childID, Revive: true}); err == nil || !strings.Contains(err.Error(), "child") {
		t.Fatalf("reveal child with revive = %v, want child error", err)
	}
	// - Capture conv-reveal hub cursor and verify event payload on revive=true for archived lead
	seqBefore, wake := d.convReveals.current()
	revRes, err := d.ConvRevealSession(ctx, ConvRevealRequest{
		SessionID: archivedID,
		EntryID:   "1000-e1",
		Revive:    true,
		Focus:     true,
		Reason:    "matched auth thread",
	})
	if err != nil || !revRes.Revived || !revRes.Emitted || revRes.Status != "active" {
		t.Fatalf("ConvRevealSession archived revive = %+v (err=%v)", revRes, err)
	}
	select {
	case <-wake:
		_, payloads, _ := d.convReveals.since(seqBefore)
		if len(payloads) == 0 {
			t.Fatal("expected conv-reveal payload")
		}
		var ev ConvReveal
		if err := json.Unmarshal(payloads[0], &ev); err != nil {
			t.Fatalf("unmarshal conv-reveal: %v", err)
		}
		if ev.ReplyID != archivedID || ev.EntryID != "1000-e1" || !ev.Focus || ev.Reason != "matched auth thread" {
			t.Fatalf("unexpected conv-reveal event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for conv-reveal event")
	}

	// - Live target with revive=true is a no-op revive that still emits event.
	liveRev, err := d.ConvRevealSession(ctx, ConvRevealRequest{SessionID: liveID, Revive: true})
	if err != nil || liveRev.Revived || !liveRev.Emitted {
		t.Fatalf("ConvRevealSession live revive = %+v (err=%v)", liveRev, err)
	}

	// 7. 7-day prune via PruneRegistrationsFor + handleMetaPrune deletes expiredID's FTS rows!
	prePrune, err := d.ConvSearch(ctx, ConvSearchRequest{CallerWorkspace: wsA, Query: "ancient pruned needle"})
	if err != nil || len(prePrune.Hits) != 1 {
		t.Fatalf("pre-prune search for expired session = %+v (err=%v)", prePrune, err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/meta/sessions/prune", strings.NewReader(`{}`))
	req.RemoteAddr = "127.0.0.1:43210"
	req.Header.Set("Authorization", "Bearer "+d.endpoint.Token)
	rec := httptest.NewRecorder()
	d.handleMetaPrune(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("meta prune status = %d body=%s", rec.Code, rec.Body.String())
	}
	if d.index != nil {
		d.index.Settle()
	}
	postPrune, err := d.ConvSearch(ctx, ConvSearchRequest{CallerWorkspace: wsA, Query: "ancient pruned needle"})
	if err != nil || len(postPrune.Hits) != 0 {
		t.Fatalf("post-prune FTS rows leaked: %+v (err=%v)", postPrune, err)
	}
	// Revealing a pruned session returns a clear not-found error.
	if _, err := d.ConvRevealSession(ctx, ConvRevealRequest{SessionID: expiredID, Revive: true}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("reveal pruned session = %v, want not found", err)
	}

	// 8. SyncExternalRing on a pinned ring feeds external legacy .json writes into convIdx.
	extName := time.Now().Add(time.Minute).UTC().Format("20060102T150405.000000000Z") + "-aabbccdd.json"
	extBody := `{"kind":"user","text":"synced_external_replacement_token"}`
	if err := os.WriteFile(filepath.Join(liveRing, extName), []byte(extBody), 0o600); err != nil {
		t.Fatalf("write external ring file: %v", err)
	}
	wslive.SyncExternalRing(liveRing)
	d.FlushConvIndexDeltas()
	newSync, err := d.ConvSearch(ctx, ConvSearchRequest{CallerWorkspace: wsA, Query: "synced_external_replacement_token"})
	if err != nil || len(newSync.Hits) != 1 || newSync.Hits[0].SessionID != liveID {
		t.Fatalf("SyncExternalRing did not index new entry: %+v (err=%v)", newSync, err)
	}
}
