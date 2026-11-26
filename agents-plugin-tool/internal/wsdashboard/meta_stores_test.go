package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsmeta"
)

func openTestMetaStore(t *testing.T, dir string) (*wsmeta.Store, *wsmeta.Snapshot) {
	t.Helper()
	store, err := wsmeta.Open(dir, wsmeta.Options{})
	if err != nil {
		t.Fatalf("wsmeta.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	validators := map[string]wsmeta.LegacyValidator{
		wsmeta.StoreAgentRecord:       func(_, _ string, _ []byte) error { return nil },
		wsmeta.StoreUsageHistory:      ValidateLegacyUsageHistory,
		wsmeta.StoreUsageHistoryCodex: ValidateLegacyUsageHistory,
		wsmeta.StoreUsageHistoryAgy:   ValidateLegacyUsageHistory,
	}
	if _, err := store.Cutover(context.Background(), wsmeta.CutoverOptions{Validate: validators}); err != nil {
		t.Fatalf("wsmeta.Cutover: %v", err)
	}
	snap, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("wsmeta.Load: %v", err)
	}
	return store, snap
}

func TestMetaStoresRailLayoutAndImportIssue(t *testing.T) {
	dir := t.TempDir()
	store, snap := openTestMetaStore(t, dir)

	s := openRailLayoutStoreFromMeta(dir, store, snap)
	ws := filepath.Join(dir, "repo-a")
	facts := railFactsOf(RailView{
		Workspaces: []RailWorkspace{{
			Path: ws,
			Sessions: []RailRow{
				{ReplyID: "sess1", Live: true},
				{ReplyID: "sess2", Live: true},
			},
		}},
	})

	if res, err := s.apply(railLayoutRequest{Op: "star", RID: "sess1"}, facts, true); err != nil || res.Reason != "" {
		t.Fatalf("star sess1 failed: res=%+v err=%v", res, err)
	}
	if res, err := s.apply(railLayoutRequest{Op: "attach", RID: "sess2", Parent: "sess1"}, facts, true); err != nil || res.Reason != "" {
		t.Fatalf("attach sess2 under sess1 failed: res=%+v err=%v", res, err)
	}
	wtPath := filepath.Join(ws, "wt-1")
	if ans, err := s.mutateAdmissions(func(cur []railAdmissionRecord) ([]railAdmissionRecord, error) {
		return append(cur, railAdmissionRecord{Repository: ws, Path: wtPath, Origin: "detected", State: "accepted"}), nil
	}); err != nil || ans.Reason != "" {
		t.Fatalf("mutateAdmissions: ans=%+v err=%v", ans, err)
	}
	// Legacy rail-layout.json must not be created after cutover.
	if _, err := os.Stat(filepath.Join(dir, railLayoutFileName)); !os.IsNotExist(err) {
		t.Fatalf("legacy rail-layout.json should not exist, err=%v", err)
	}

	// Reloading from SQLite preserves workspace layout and admission extras.
	snap2, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	s2 := openRailLayoutStoreFromMeta(dir, store, snap2)
	doc, _ := s2.current()
	wdoc := doc.Workspaces[ws]
	if wdoc == nil || len(wdoc.Stars) != 1 || wdoc.Stars[0] != "sess1" {
		t.Fatalf("reloaded stars = %+v, want [sess1]", wdoc)
	}
	if kids := wdoc.Children["sess1"]; len(kids) != 1 || kids[0] != "sess2" {
		t.Fatalf("reloaded children[sess1] = %v, want [sess2]", kids)
	}
	adms, err := railAdmissions(doc)
	if err != nil {
		t.Fatalf("railAdmissions: %v", err)
	}
	if got := admissionAt(adms, ws, wtPath); got.State != "accepted" {
		t.Fatalf("reloaded admission = %+v, want state=accepted", got)
	}

	// Drop worktree order persists through SQLite without creating legacy files.
	if err := s2.dropWorktreeOrder(ws, "wt-gone", ""); err != nil {
		t.Fatalf("dropWorktreeOrder: %v", err)
	}

	// Future schema legacy file -> ImportIssues refuses mutations without overwriting.
	futureDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(futureDir, railLayoutFileName), []byte(`{"schema_version":99,"workspaces":{}}`), 0o600); err != nil {
		t.Fatalf("write future rail layout: %v", err)
	}
	fStore, fSnap := openTestMetaStore(t, futureDir)
	fs := openRailLayoutStoreFromMeta(futureDir, fStore, fSnap)
	if res, _ := fs.apply(railLayoutRequest{Op: "star", RID: "sess1"}, facts, true); res.Reason != layoutStore {
		t.Fatalf("future schema layout apply = %+v, want reason=%q", res, layoutStore)
	}
}

func TestMetaStoresMemosAndImportIssue(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "repo-memo")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}

	// Seed a legacy memo with an unknown field before cutover so we verify
	// unknown item fields survive SQLite edit in place.
	legacyFile := memoFile(dir, ws)
	if err := os.MkdirAll(filepath.Dir(legacyFile), 0o700); err != nil {
		t.Fatalf("mkdir memos: %v", err)
	}
	seedDoc := `{"schema_version":1,"workspace":` + mustJSON(t, ws) + `,"extra_top":"keep","memos":[{"id":"m1","text":"first","custom_tag":"keep-me"}]}`
	if err := os.WriteFile(legacyFile, []byte(seedDoc), 0o600); err != nil {
		t.Fatalf("write seed memo: %v", err)
	}

	store, snap := openTestMetaStore(t, dir)
	// Remove legacy memos dir after cutover so any accidental legacy write fails the check.
	if err := os.RemoveAll(filepath.Join(dir, "memos")); err != nil {
		t.Fatalf("remove legacy memos dir: %v", err)
	}

	d := &Daemon{dir: dir, log: io.Discard, meta: store, metaSnap: snap, broker: newBroker(nil)}
	d.loadMemosFromMeta(snap)
	if got := d.memoCount(ws); got != 1 {
		t.Fatalf("initial memoCount = %d, want 1", got)
	}

	callMemo := func(action, id, text string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/memos", nil)
		d.memoMu.Lock()
		d.handleMemosMetaLocked(rec, req, ws, action, id, text)
		d.memoMu.Unlock()
		return rec
	}

	// Edit existing memo m1 and add a new memo m2.
	if rec := callMemo("edit", "m1", "updated first"); rec.Code != http.StatusOK {
		t.Fatalf("edit m1 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := callMemo("add", "", "second memo"); rec.Code != http.StatusOK {
		t.Fatalf("add second status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := d.memoCount(ws); got != 2 {
		t.Fatalf("memoCount after add = %d, want 2", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "memos")); !os.IsNotExist(err) {
		t.Fatalf("legacy memos dir should not be recreated, err=%v", err)
	}

	// Reload from SQLite: newest-first order, edited text, preserved unknown item field.
	snap2, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	md := snap2.Memos[memoWorkspaceHash(ws)]
	if len(md.Items) != 2 {
		t.Fatalf("reloaded memo items = %d, want 2", len(md.Items))
	}
	if !bytes.Contains(md.Items[1].Body, []byte(`"custom_tag":"keep-me"`)) {
		t.Fatalf("unknown field lost on edit: %s", string(md.Items[1].Body))
	}
	if !bytes.Contains(md.Items[1].Body, []byte(`"text":"updated first"`)) {
		t.Fatalf("edited text missing: %s", string(md.Items[1].Body))
	}

	// Delete m1 and verify count drops to 1.
	if rec := callMemo("delete", "m1", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete m1 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := d.memoCount(ws); got != 1 {
		t.Fatalf("memoCount after delete = %d, want 1", got)
	}

	// Future-schema legacy memo -> ImportIssues blocks with 409 and memoCount 0.
	futureDir := t.TempDir()
	fFile := memoFile(futureDir, ws)
	if err := os.MkdirAll(filepath.Dir(fFile), 0o700); err != nil {
		t.Fatalf("mkdir future memos: %v", err)
	}
	if err := os.WriteFile(fFile, []byte(`{"schema_version":9,"workspace":"x","memos":[]}`), 0o600); err != nil {
		t.Fatalf("write future memo: %v", err)
	}
	fStore, fSnap := openTestMetaStore(t, futureDir)
	fd := &Daemon{dir: futureDir, log: io.Discard, meta: fStore, metaSnap: fSnap, broker: newBroker(nil)}
	fd.loadMemosFromMeta(fSnap)
	if got := fd.memoCount(ws); got != 0 {
		t.Fatalf("future-schema memoCount = %d, want 0", got)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/memos", nil)
	fd.memoMu.Lock()
	fd.handleMemosMetaLocked(rec, req, ws, "add", "", "blocked")
	fd.memoMu.Unlock()
	if rec.Code != http.StatusConflict {
		t.Fatalf("future-schema memo add status = %d, want 409", rec.Code)
	}
}

func TestMetaStoresPlanUsageAndHistoryDeltas(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	store, snap := openTestMetaStore(t, dir)

	s := newPlanUsageStoreFromMeta(dir, home, func(string) string { return "" }, store, snap)
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

	// Per-key latest save (claude only, then gpt, then agy) writes zero legacy files.
	s.mu.Lock()
	s.claude = planUsageEntry{Harness: "claude", Source: "Claude push", AsOf: now.UnixMilli(), Windows: []planUsageWindow{{Kind: "five_hour", Percent: 22, AsOf: now.UnixMilli(), ResetsAt: now.Add(5 * time.Hour).UnixMilli()}}}
	s.saveLocked()
	s.gpt = planUsageEntry{Harness: "codex", Source: "via Codex CLI", AsOf: now.UnixMilli(), Windows: []planUsageWindow{{Kind: "seven_day", Percent: 40, AsOf: now.UnixMilli(), ResetsAt: now.Add(24 * time.Hour).UnixMilli()}}}
	s.saveLocked()
	s.agyGroups = []planUsageEntry{{
		Harness: "agy", Group: "gemini", Label: "Gemini Models", Source: agyUsageSource, AsOf: now.UnixMilli(),
		Windows: []planUsageWindow{{Kind: "five_hour", Percent: 15, AsOf: now.UnixMilli(), ResetsAt: now.Add(4 * time.Hour).UnixMilli()}},
	}}
	s.saveAgyLocked()

	// Append first sample (gets seq=0!), update it in place (same asOf bucket ->
	// must update seq=0 rather than inserting duplicate), then append newer sample
	// and prune an expired one.
	resetAt := now.Add(5 * time.Hour).UnixMilli()
	s1 := planUsageSample{Harness: "claude", Kind: "five_hour", Source: "Claude push", ResetsAt: resetAt, AsOf: now.UnixMilli(), Percent: 22}
	s.appendHistoryFileLocked(&s.hist, defaultHistorySpec, []planUsageSample{s1}, now.UnixMilli())
	if len(s.hist.file.Records) != 1 || !s.hist.file.Records[0].hasSeq || s.hist.file.Records[0].seq != 0 {
		t.Fatalf("first sample seq = %+v, want hasSeq=true seq=0", s.hist.file.Records)
	}
	s1Updated := s1
	s1Updated.Percent = 35
	s.appendHistoryFileLocked(&s.hist, defaultHistorySpec, []planUsageSample{s1Updated}, now.UnixMilli())
	if len(s.hist.file.Records) != 1 || s.hist.file.Records[0].Sample.Percent != 35 || s.hist.file.Records[0].seq != 0 {
		t.Fatalf("in-place update at seq=0 failed: %+v", s.hist.file.Records)
	}

	later := now.Add(15 * 24 * time.Hour)
	s2 := planUsageSample{Harness: "claude", Kind: "five_hour", Source: "Claude push", ResetsAt: later.Add(5 * time.Hour).UnixMilli(), AsOf: later.UnixMilli(), Percent: 50}
	s.appendHistoryFileLocked(&s.hist, defaultHistorySpec, []planUsageSample{s2}, later.UnixMilli())
	s.mu.Unlock()

	for _, name := range []string{planUsageCache, planUsageAgyCache, planUsageHistoryCache, planUsageCodexHistoryCache, planUsageAgyHistoryCache} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("legacy usage file %s should not exist, err=%v", name, err)
		}
	}

	// Reload from SQLite and verify latest entries and pruned history.
	snap2, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	sReload := newPlanUsageStoreFromMeta(dir, home, func(string) string { return "" }, store, snap2)
	sReload.mu.Lock()
	defer sReload.mu.Unlock()
	if sReload.claude.Source != "Claude push" || sReload.gpt.Source != "via Codex CLI" {
		t.Fatalf("reloaded latest mismatch: claude=%+v gpt=%+v", sReload.claude, sReload.gpt)
	}
	if len(sReload.agyGroups) != 1 || sReload.agyGroups[0].Group != "gemini" {
		t.Fatalf("reloaded agy groups = %+v, want [gemini]", sReload.agyGroups)
	}
	hist, err := sReload.readHistoryFileLocked(&sReload.hist, defaultHistorySpec)
	if err != nil {
		t.Fatalf("readHistoryFileLocked after reload: %v", err)
	}
	if len(hist.Records) != 1 || hist.Records[0].Sample.Percent != 50 {
		t.Fatalf("reloaded history = %+v, want single 50%% sample (old seq=0 pruned)", hist.Records)
	}

	// Future-schema plan-usage-history.json stays protected via ImportIssues.
	futureDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(futureDir, planUsageHistoryCache), []byte(`{"schema_version":9,"samples":[]}`), 0o600); err != nil {
		t.Fatalf("write future usage history: %v", err)
	}
	fStore, fSnap := openTestMetaStore(t, futureDir)
	fs := newPlanUsageStoreFromMeta(futureDir, home, func(string) string { return "" }, fStore, fSnap)
	fs.mu.Lock()
	_, herr := fs.readHistoryFileLocked(&fs.hist, defaultHistorySpec)
	fs.mu.Unlock()
	if herr == nil {
		t.Fatal("expected error reading future-schema usage history from SQLite ImportIssues")
	}
}

func TestMetaStoresLabelsAckHiringAndDeleteSession(t *testing.T) {
	dir := t.TempDir()
	store, snap := openTestMetaStore(t, dir)

	d := &Daemon{dir: dir, log: io.Discard, meta: store, metaSnap: snap, broker: newBroker(nil)}
	d.loadLabelsFromMeta(snap)
	d.loadAckChecksFromMeta(snap)
	d.loadHiringFromMeta(snap)

	const rid = "sessmeta01"
	if err := d.setSessionLabel(rid, "Alpha Lead"); err != nil {
		t.Fatalf("setSessionLabel: %v", err)
	}
	if got := d.sessionLabel(rid); got != "Alpha Lead" {
		t.Fatalf("sessionLabel = %q, want Alpha Lead", got)
	}

	// Index uses LabelLookup without touching label.json.
	idx := NewIndex(IndexOptions{
		Dir: dir,
		Source: stubSource(func(reg Registration) (SessionData, bool, error) {
			return SessionData{Fingerprint: "fp1"}, true, nil
		}),
		LabelLookup: d.sessionLabel,
	})
	proj := t.TempDir()
	if err := WriteRegistration(dir, Registration{ReplyID: rid, Project: proj, Harness: "codex", HeartbeatAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatalf("WriteRegistration: %v", err)
	}
	idx.Refresh()
	sess, ok := idx.Session(rid)
	if !ok || sess.Label != "Alpha Lead" {
		t.Fatalf("indexed session = (%+v, %v), want Label=Alpha Lead", sess, ok)
	}

	// Ack check recordInterrupt + CAS commitAnswerCheck.
	inbox := sessionInbox(dir, rid)
	sessDir := SessionDir(dir, rid)
	tInt := time.Date(2026, 11, 27, 11, 0, 0, 0, time.UTC)
	d.inboxMu.Lock()
	d.recordInterrupt(inbox, tInt)
	d.inboxMu.Unlock()
	gotAck := d.readAckCheck(rid, sessDir)
	if gotAck.InterruptedAt != tInt.UnixMilli() {
		t.Fatalf("InterruptedAt = %d, want %d", gotAck.InterruptedAt, tInt.UnixMilli())
	}
	// Stale CAS read must not overwrite.
	d.commitAnswerCheck(sess, sessDir, inbox, ackCheckRecord{Through: 0, InterruptedAt: 0}, tInt.Add(time.Minute).UnixMilli(), nil)
	if afterStale := d.readAckCheck(rid, sessDir); afterStale.Through != 0 {
		t.Fatalf("stale CAS unexpectedly wrote Through=%d", afterStale.Through)
	}
	// Matching CAS read updates Through.
	wantThrough := tInt.Add(2 * time.Minute).UnixMilli()
	d.commitAnswerCheck(sess, sessDir, inbox, gotAck, wantThrough, nil)
	if afterCAS := d.readAckCheck(rid, sessDir); afterCAS.Through != wantThrough {
		t.Fatalf("CAS Through = %d, want %d", afterCAS.Through, wantThrough)
	}

	// Hiring metadata + deadlock-free RecordHired while d.hireMu is held.
	if err := d.writeHiringMetadata(rid, HiringMetadata{Enabled: true}); err != nil {
		t.Fatalf("writeHiringMetadata: %v", err)
	}
	hm, err := d.readHiringMetadata(rid)
	if err != nil || !hm.Enabled || hm.Hired {
		t.Fatalf("readHiringMetadata = (%+v, %v), want Enabled=true", hm, err)
	}
	const hiredRID = "sesshired1"
	if err := store.MarkHired(context.Background(), hiredRID, "req-42", "Hired Lead"); err != nil {
		t.Fatalf("store.MarkHired: %v", err)
	}
	d.hireMu.Lock()
	d.RecordHired(hiredRID, "req-42", "Hired Lead")
	d.hireMu.Unlock()
	if got := d.sessionLabel(hiredRID); got != "Hired Lead" {
		t.Fatalf("hired label = %q, want Hired Lead", got)
	}
	if hm2, err := d.readHiringMetadata(hiredRID); err != nil || !hm2.Hired || hm2.RequestID != "req-42" {
		t.Fatalf("hired metadata = (%+v, %v), want Hired=true RequestID=req-42", hm2, err)
	}

	// Zero legacy session metadata files created.
	for _, f := range []string{labelFileName, ackCheckFileName, hiringFileName} {
		if _, err := os.Stat(filepath.Join(sessDir, f)); !os.IsNotExist(err) {
			t.Fatalf("legacy session file %s should not exist, err=%v", f, err)
		}
	}

	// deleteSessionMeta deletes SQLite rows and clears in-memory caches.
	if err := d.deleteSessionMeta(rid); err != nil {
		t.Fatalf("deleteSessionMeta: %v", err)
	}
	if d.sessionLabel(rid) != "" || d.readAckCheck(rid, sessDir) != (ackCheckRecord{}) {
		t.Fatalf("in-memory caches not cleared for %s", rid)
	}
	if hm3, _ := d.readHiringMetadata(rid); hm3 != (HiringMetadata{}) {
		t.Fatalf("hiring cache not cleared for %s: %+v", rid, hm3)
	}
	snapAfter, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load after delete: %v", err)
	}
	if _, ok := snapAfter.Labels[rid]; ok {
		t.Fatalf("SQLite label row still present for %s", rid)
	}
	if _, ok := snapAfter.AckChecks[rid]; ok {
		t.Fatalf("SQLite ack_check row still present for %s", rid)
	}
	if _, ok := snapAfter.Hiring[rid]; ok {
		t.Fatalf("SQLite hiring row still present for %s", rid)
	}
}

type stubSource func(reg Registration) (SessionData, bool, error)

func (f stubSource) ReadSession(reg Registration) (SessionData, bool, error) { return f(reg) }

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
