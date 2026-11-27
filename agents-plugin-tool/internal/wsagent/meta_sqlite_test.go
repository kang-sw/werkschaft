package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

func openCutoverTestMeta(t *testing.T, dir string) (*wsmeta.Store, *wsmeta.Snapshot) {
	t.Helper()
	store, err := wsmeta.Open(dir, wsmeta.Options{})
	if err != nil {
		t.Fatalf("wsmeta.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.Cutover(context.Background(), wsmeta.CutoverOptions{
		Validate: map[string]wsmeta.LegacyValidator{"agent_record": ValidateLegacyRecord},
	}); err != nil {
		t.Fatalf("wsmeta.Cutover: %v", err)
	}
	snap, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("wsmeta.Load: %v", err)
	}
	return store, snap
}

func assertNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s not to exist, got err=%v", path, err)
	}
}

func TestRunnerSQLiteRecordsAndHolderCursorHotPath(t *testing.T) {
	h := newLeadHarness(t)
	store, snap := openCutoverTestMeta(t, h.dir)

	r1 := NewRunner(h.opts)
	r1.SetMeta(store, snap)
	r1.Start(nil)

	st, err := r1.CreateLead(BackendClaude, h.root, LeadOptions{Model: "claude-sonnet-4-6", Effort: "medium"})
	if err != nil {
		t.Fatalf("CreateLead: %v", err)
	}
	sessDir := wsdashboard.SessionDir(h.dir, st.ID)
	assertNotExist(t, filepath.Join(sessDir, recordFileName))

	ar, ok, err := store.Record(context.Background(), st.ID)
	if err != nil || !ok {
		t.Fatalf("store.Record after create: ok=%v err=%v", ok, err)
	}
	if ar.Deltas != 0 {
		t.Fatalf("initial Deltas = %d, want 0", ar.Deltas)
	}

	// Mutating settings should emit a delta via PatchRecord, not a full PutRecord or agent.json.
	newModel := "claude-opus-4-6"
	if _, err := r1.SetSettings(st.ID, &newModel, nil); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	assertNotExist(t, filepath.Join(sessDir, recordFileName))
	ar, ok, err = store.Record(context.Background(), st.ID)
	if err != nil || !ok {
		t.Fatalf("store.Record after SetSettings: ok=%v err=%v", ok, err)
	}
	if ar.Deltas != 1 {
		t.Fatalf("Deltas after SetSettings = %d, want 1", ar.Deltas)
	}

	// Attach a holder ID via conv.update, then exercise persistSeq() cursor-only hot path.
	c := r1.conv(st.ID)
	if c == nil {
		t.Fatalf("missing open conv for %s", st.ID)
	}
	if err := c.update(func(rec *Record) { rec.HolderID = "holder-alpha"; rec.HolderSeq = 0 }); err != nil {
		t.Fatalf("attach holder: %v", err)
	}
	beforeCursor, ok, err := store.Record(context.Background(), st.ID)
	if err != nil || !ok {
		t.Fatalf("store.Record before cursor: ok=%v err=%v", ok, err)
	}
	c.do(func() {
		c.seq = 42
		c.seqDirty = true
		c.live.ringDirty = true
		c.persistSeq()
	})
	assertNotExist(t, filepath.Join(sessDir, recordFileName))

	afterCursor, ok, err := store.Record(context.Background(), st.ID)
	if err != nil || !ok {
		t.Fatalf("store.Record after cursor: ok=%v err=%v", ok, err)
	}
	if afterCursor.Deltas != beforeCursor.Deltas {
		t.Fatalf("persistSeq appended record delta: before=%d after=%d", beforeCursor.Deltas, afterCursor.Deltas)
	}
	if afterCursor.HolderCursors["holder-alpha"] != 42 {
		t.Fatalf("holder cursor = %d, want 42", afterCursor.HolderCursors["holder-alpha"])
	}
	var decoded Record
	if err := json.Unmarshal(afterCursor.JSON, &decoded); err != nil {
		t.Fatalf("unmarshal assembled record: %v", err)
	}
	if decoded.HolderSeq != 42 || decoded.Model != newModel {
		t.Fatalf("assembled record = %+v, want HolderSeq=42 Model=%s", decoded, newModel)
	}

	// Closing r1 must NOT close the shared daemon store.
	r1.Close()
	snap2, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("shared store closed by Runner.Close: %v", err)
	}

	// Restart a fresh Runner from SQLite snapshot (with zero agent.json on disk).
	r2 := NewRunner(h.opts)
	r2.SetMeta(store, snap2)
	r2.Start(nil)
	defer r2.Close()

	rec2, ok := r2.RecordByReplyID(st.ID)
	if !ok {
		t.Fatalf("r2 did not restore %s from SQLite", st.ID)
	}
	if rec2.Model != newModel || rec2.HolderID != "holder-alpha" || rec2.HolderSeq != 42 {
		t.Fatalf("restored record = %+v", rec2)
	}
	assertNotExist(t, filepath.Join(sessDir, recordFileName))

	// Close and ForgetClosed deletes session rows in SQLite.
	if _, err := r2.CloseConversation(st.ID); err != nil {
		t.Fatalf("CloseConversation: %v", err)
	}
	if !r2.ForgetClosed(st.ID) {
		t.Fatalf("ForgetClosed returned false")
	}
	if _, found, err := store.Record(context.Background(), st.ID); err != nil || found {
		t.Fatalf("store.Record after ForgetClosed = (found=%v, err=%v), want found=false", found, err)
	}
}

func TestRunnerSQLitePermissionsFailClosedAndUnknownFields(t *testing.T) {
	h := newLeadHarness(t)
	reg := h.opts.Backends[BackendClaude]
	reg.PermissionLevels = LeadPermissionLevels()
	h.opts.Backends[BackendClaude] = reg
	store, snap := openCutoverTestMeta(t, h.dir)

	r := NewRunner(h.opts)
	r.SetMeta(store, snap)
	r.Start(nil)

	lead1, err := r.CreateLead(BackendClaude, h.root, LeadOptions{PermissionLevel: PermissionWorkdirWrite})
	if err != nil {
		t.Fatalf("CreateLead 1: %v", err)
	}
	lead2, err := r.CreateLead(BackendClaude, h.root, LeadOptions{})
	if err != nil {
		t.Fatalf("CreateLead 2: %v", err)
	}
	assertNotExist(t, filepath.Join(wsdashboard.SessionDir(h.dir, lead1.ID), wsdashboard.SessionPermissionFileName))

	// Inject unknown field on lead1 and an invalid permission row on lead2, then reload runner.
	if err := store.PutPermission(context.Background(), lead1.ID, wsmeta.Permission{
		State:        "valid",
		Level:        string(PermissionWorkdirWrite),
		DelegatedSet: true,
		Delegated:    true,
		Body:         json.RawMessage(`{"permission_level":"workdir-write","delegated_authority":true,"future_flag":42}`),
	}); err != nil {
		t.Fatalf("PutPermission lead1: %v", err)
	}
	if err := store.PutPermission(context.Background(), lead2.ID, wsmeta.Permission{
		State: "invalid",
		Error: "corrupt imported permission",
	}); err != nil {
		t.Fatalf("PutPermission lead2: %v", err)
	}
	r.Close()

	snap2, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	r2 := NewRunner(h.opts)
	r2.SetMeta(store, snap2)
	r2.Start(nil)
	defer r2.Close()

	// Lowering lead1 must write to SQLite, preserve unknown members + delegation, and write no permission.json.
	if _, err := r2.SetPermissionLevel(lead1.ID, PermissionReadOnly); err != nil {
		t.Fatalf("SetPermissionLevel lead1: %v", err)
	}
	assertNotExist(t, filepath.Join(wsdashboard.SessionDir(h.dir, lead1.ID), wsdashboard.SessionPermissionFileName))

	snap3, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load 3: %v", err)
	}
	p1 := snap3.Permissions[lead1.ID]
	if p1.State != "valid" || p1.Level != string(PermissionReadOnly) || !p1.DelegatedSet || !p1.Delegated {
		t.Fatalf("lead1 permission = %+v", p1)
	}
	var members map[string]any
	if err := json.Unmarshal(p1.Body, &members); err != nil {
		t.Fatalf("unmarshal permission body: %v", err)
	}
	if members["future_flag"] != float64(42) {
		t.Fatalf("unknown member lost: %s", p1.Body)
	}

	// lead2's invalid permission must fail closed on read and refuse writes.
	if _, err := r2.SetPermissionLevel(lead2.ID, PermissionReadOnly); err == nil {
		t.Fatalf("SetPermissionLevel succeeded on invalid permission row, want fail-closed error")
	}
	if p2 := snap3.Permissions[lead2.ID]; p2.State != "invalid" {
		t.Fatalf("lead2 permission state = %q, want invalid", p2.State)
	}
}

func TestRunnerSQLiteHireAndOnHiredCallback(t *testing.T) {
	h := newLeadHarness(t)
	store, snap := openCutoverTestMeta(t, h.dir)

	var callbackCalls int
	var gotID, gotReq, gotLabel string
	opts := h.opts
	opts.OnHired = func(replyID, requestID, label string) {
		callbackCalls++
		gotID, gotReq, gotLabel = replyID, requestID, label
	}

	r1 := NewRunner(opts)
	r1.SetMeta(store, snap)
	r1.Start(nil)

	launch := wsdashboard.HireLaunch{Harness: BackendClaude, Model: "claude-sonnet-4-6", Effort: "high", Label: " hired peer "}
	id1, err := r1.CreateHireLead("req-sqlite-1", h.root, launch)
	if err != nil {
		t.Fatalf("CreateHireLead 1: %v", err)
	}
	id2, err := r1.CreateHireLead("req-sqlite-1", h.root, launch)
	if err != nil || id2 != id1 {
		t.Fatalf("idempotent CreateHireLead = (%q, %v), want %q", id2, err, id1)
	}
	if callbackCalls != 1 || gotID != id1 || gotReq != "req-sqlite-1" || gotLabel != "hired peer" {
		t.Fatalf("OnHired = (%d, %q, %q, %q)", callbackCalls, gotID, gotReq, gotLabel)
	}

	sessDir := wsdashboard.SessionDir(h.dir, id1)
	assertNotExist(t, filepath.Join(sessDir, recordFileName))
	assertNotExist(t, filepath.Join(sessDir, "hiring.json"))
	assertNotExist(t, filepath.Join(sessDir, "label.json"))
	r1.Close()

	snap2, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if snap2.Labels[id1] != "hired peer" {
		t.Fatalf("SQLite label = %q, want %q", snap2.Labels[id1], "hired peer")
	}
	if hm := snap2.Hiring[id1]; !hm.Hired || hm.RequestID != "req-sqlite-1" {
		t.Fatalf("SQLite hiring = %+v", hm)
	}

	// Restarted runner resolves the hired lead from SQLite without reading disk sidecars.
	r2 := NewRunner(opts)
	r2.SetMeta(store, snap2)
	r2.Start(nil)
	defer r2.Close()

	id3, err := r2.CreateHireLead("req-sqlite-1", h.root, launch)
	if err != nil || id3 != id1 {
		t.Fatalf("restarted CreateHireLead = (%q, %v), want %q", id3, err, id1)
	}
}

func TestRunnerSQLiteRetiredPiArchiveAndRestore(t *testing.T) {
	h := newLeadHarness(t)
	store, _ := openCutoverTestMeta(t, h.dir)

	piID := "retiredpisqliteone"
	piRec := Record{
		SchemaVersion: recordSchemaVersion,
		ReplyID:       piID,
		Profile:       "pi",
		Backend:       BackendPi,
		Root:          h.root,
		WSKey:         "retired-pi-key-1",
		CreatedAt:     "2026-11-20T00:00:00Z",
		Lead:          true,
		Listed:        true,
	}
	raw, err := json.Marshal(piRec)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutRecord(context.Background(), piID, "", raw); err != nil {
		t.Fatalf("PutRecord retired Pi: %v", err)
	}
	// Ensure no sessions/<id>/agent.json exists on disk.
	sessDir := wsdashboard.SessionDir(h.dir, piID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	assertNotExist(t, filepath.Join(sessDir, recordFileName))

	snap, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	r1 := NewRunner(h.opts)
	r1.SetMeta(store, snap)
	r1.Start(nil)
	r1.Close()

	archiveRec := filepath.Join(h.dir, retiredPiHistoryDir, piID, recordFileName)
	if _, err := os.Stat(archiveRec); err != nil {
		t.Fatalf("expected retired Pi archive record at %s: %v", archiveRec, err)
	}
	assertNotExist(t, filepath.Join(sessDir, recordFileName))

	// Simulate an old daemon pruning sessions/<id> and remove the SQLite record, then restart.
	if err := os.RemoveAll(sessDir); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRecord(context.Background(), piID); err != nil {
		t.Fatal(err)
	}

	snap2, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load 2: %v", err)
	}
	r2 := NewRunner(h.opts)
	r2.SetMeta(store, snap2)
	r2.Start(nil)
	defer r2.Close()

	restored, ok := r2.RecordByReplyID(piID)
	if !ok || restored.Backend != BackendPi {
		t.Fatalf("retired Pi record was not restored from archive into SQLite: ok=%v rec=%+v", ok, restored)
	}
	if _, found, err := store.Record(context.Background(), piID); err != nil || !found {
		t.Fatalf("restored Pi record missing from SQLite: found=%v err=%v", found, err)
	}
	assertNotExist(t, filepath.Join(sessDir, recordFileName))
}

func TestSupportSQLiteDeferLoadAndKeyedWrites(t *testing.T) {
	h := newHarness(t, nil)
	claude := &fakeBackend{exe: "fake-claude"}
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	h.config.Now = func() time.Time { return now }
	h.config.Backends = map[string]BackendRegistration{
		BackendClaude: {
			Backend:         claude,
			Permission:      builtinPermission(BackendClaude),
			Lifecycle:       builtinLifecycle(BackendClaude),
			LeadProfile:     LeadProfileClaude,
			PermissionModes: LeadPermissionModes,
		},
	}
	stubLookPath(t)

	// Open SQLite cutover on a clean directory before creating Runner & Support.
	store, snap := openCutoverTestMeta(t, h.dir)
	r := NewRunner(h.config)
	r.SetMeta(store, snap)
	r.Start(nil)
	defer r.Close()

	stores := map[string]string{"alpha": t.TempDir(), "beta": t.TempDir()}
	storeRoot := func(id string) (string, error) {
		if root, ok := stores[id]; ok {
			return root, nil
		}
		return "", ErrStoreUnregistered
	}

	// Plant a decoy support.json that DeferLoad must NOT read before LoadFromMeta.
	decoyPath := filepath.Join(h.dir, supportFileName)
	decoyBytes := []byte(`{"schema_version":1,"last":{"harness":"claude","model":"decoy","effort":"low"},"stores":{"alpha":"decoyid"}}`)
	if err := os.WriteFile(decoyPath, decoyBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	sup := NewSupport(SupportOptions{
		Runner:    r,
		Dir:       h.dir,
		StoreRoot: storeRoot,
		Now:       func() time.Time { return now },
		DeferLoad: true,
	})
	sup.LoadFromMeta(store, snap)

	// Select writes last selection to SQLite, leaving decoy support.json untouched.
	model := "claude-sonnet-4-6"
	effort := "high"
	if _, err := sup.Select("alpha", selectRequest{Harness: strp(BackendClaude), Model: &model, Effort: &effort}); err != nil {
		t.Fatalf("Select: %v", err)
	}
	st, err := sup.Send("alpha", "hello sqlite")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Verify decoy file was never overwritten.
	afterDecoy, err := os.ReadFile(decoyPath)
	if err != nil || string(afterDecoy) != string(decoyBytes) {
		t.Fatalf("support.json was touched under SQLite mode: %q err=%v", string(afterDecoy), err)
	}

	// Verify SQLite holds both Last and Stores["alpha"].
	snap2, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if snap2.Support.Stores["alpha"] != st.ID {
		t.Fatalf("SQLite support store mapping = %q, want %q", snap2.Support.Stores["alpha"], st.ID)
	}
	var last Selection
	if err := json.Unmarshal(snap2.Support.Last, &last); err != nil {
		t.Fatalf("unmarshal SQLite support last: %v", err)
	}
	if last.Harness != BackendClaude || last.Model != model || last.Effort != effort {
		t.Fatalf("SQLite support last = %+v", last)
	}

	// Fresh Support loaded from SQLite sees the same state and deletes mapping on CloseUnregistered.
	sup2 := NewSupport(SupportOptions{
		Runner:    r,
		Dir:       h.dir,
		StoreRoot: storeRoot,
		Now:       func() time.Time { return now },
		DeferLoad: true,
	})
	sup2.LoadFromMeta(store, snap2)
	ans, err := sup2.Store("alpha")
	if err != nil {
		t.Fatalf("Store(alpha): %v", err)
	}
	if ans.Conversation == nil || ans.Conversation.ID != st.ID || ans.Selection.Model != model {
		t.Fatalf("Store(alpha) = %+v", ans)
	}

	delete(stores, "alpha")
	dropped := sup2.CloseUnregistered()
	if len(dropped) != 1 || dropped[0] != "alpha" {
		t.Fatalf("CloseUnregistered = %v, want [alpha]", dropped)
	}
	snap3, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load 3: %v", err)
	}
	if _, exists := snap3.Support.Stores["alpha"]; exists {
		t.Fatalf("alpha mapping remained in SQLite after CloseUnregistered")
	}
}

func TestRunnerCanForgetClosedNonMutatingAndOpenGuard(t *testing.T) {
	h := newLeadHarness(t)
	store, snap := openCutoverTestMeta(t, h.dir)

	r := NewRunner(h.opts)
	r.SetMeta(store, snap)
	r.Start(nil)
	defer r.Close()

	// Create an open lead and a closed lead with a live child descendant.
	openLead, err := r.CreateLead(BackendClaude, h.root, LeadOptions{})
	if err != nil {
		t.Fatalf("CreateLead open: %v", err)
	}
	closedLead, err := r.CreateLead(BackendClaude, h.root, LeadOptions{})
	if err != nil {
		t.Fatalf("CreateLead closed: %v", err)
	}
	if _, err := r.CloseConversation(closedLead.ID); err != nil {
		t.Fatalf("CloseConversation: %v", err)
	}
	// Attach an open child conversation to closedLead in runner state so
	// live-descendant blocking is exercised.
	childID := "livechild1"
	r.mu.Lock()
	r.convs[childID] = &conv{}
	if r.childrenIndex == nil {
		r.childrenIndex = map[ParentRef]map[string]bool{}
	}
	r.childrenIndex[safeStopParent(closedLead.ID, true)] = map[string]bool{childID: true}
	r.mu.Unlock()

	// With a live child descendant, CanForgetClosed must return false.
	if r.CanForgetClosed(closedLead.ID) {
		t.Fatal("CanForgetClosed = true while live child descendant exists")
	}
	// Drop the live child; CanForgetClosed must now return true without evicting
	// r.closed or deleting the SQLite row.
	r.mu.Lock()
	delete(r.convs, childID)
	delete(r.childrenIndex, safeStopParent(closedLead.ID, true))
	r.mu.Unlock()

	if !r.CanForgetClosed(closedLead.ID) {
		t.Fatal("CanForgetClosed = false after live child cleared")
	}
	if _, ok := r.AgentRecord(closedLead.ID); !ok {
		t.Fatal("CanForgetClosed mutated runner state and dropped closed lead record")
	}
	if _, ok, err := store.Record(context.Background(), closedLead.ID); err != nil || !ok {
		t.Fatalf("CanForgetClosed deleted SQLite record: ok=%v err=%v", ok, err)
	}

	// ForgetClosed on an open conversation with no descendants must not wipe its
	// SQLite record.
	if !r.ForgetClosed(openLead.ID) {
		t.Fatal("ForgetClosed(openLead) = false, want true")
	}
	if _, ok, err := store.Record(context.Background(), openLead.ID); err != nil || !ok {
		t.Fatalf("ForgetClosed wiped SQLite record of open conversation: ok=%v err=%v", ok, err)
	}

	// ForgetClosed on the closed lead evicts runner state and deletes SQLite row.
	if !r.ForgetClosed(closedLead.ID) {
		t.Fatal("ForgetClosed(closedLead) = false, want true")
	}
	if _, ok := r.AgentRecord(closedLead.ID); ok {
		t.Fatal("closed lead record remained in runner after ForgetClosed")
	}
	if _, ok, err := store.Record(context.Background(), closedLead.ID); err != nil || ok {
		t.Fatalf("closed lead SQLite record remained after ForgetClosed: ok=%v err=%v", ok, err)
	}
}

func TestRunnerSetMetaAfterStartDoesNotLeakDurableStore(t *testing.T) {
	runnerDir := t.TempDir()
	r := NewRunner(Options{
		Dir:      runnerDir,
		Backends: map[string]BackendRegistration{"durable": {ProcessKind: ProcessDurable}},
	})
	r.Start(nil)
	t.Cleanup(r.Close)

	origStore := r.durableRecords
	if origStore == nil {
		t.Fatal("expected Start to open durableRecords SQLite store")
	}

	otherStore, otherSnap := openCutoverTestMeta(t, t.TempDir())
	r.SetMeta(otherStore, otherSnap)

	if r.meta != nil {
		t.Fatalf("SetMeta after Start unexpectedly bound r.meta = %p, want nil", r.meta)
	}
	if r.durableRecords != origStore {
		t.Fatalf("SetMeta after Start overwrote durableRecords (%p -> %p), leaking original store handle", origStore, r.durableRecords)
	}
}
