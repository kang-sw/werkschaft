package wsdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsmeta"
)

type fakeSupportMetaHost struct {
	loadedMeta   atomic.Bool
	loadedLegacy atomic.Bool
}

func (f *fakeSupportMetaHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}
func (f *fakeSupportMetaHost) LoadFromMeta(*wsmeta.Store, *wsmeta.Snapshot) { f.loadedMeta.Store(true) }
func (f *fakeSupportMetaHost) LoadLegacy()                                  { f.loadedLegacy.Store(true) }

type fakeAgentMetaHost struct {
	dir        string
	metaBound  atomic.Bool
	validator  atomic.Bool
	forgetOK   atomic.Bool
	forgotList []string
	records    map[string]AgentRecordSnapshot
}

func (f *fakeAgentMetaHost) Handler() http.Handler      { return http.NotFoundHandler() }
func (f *fakeAgentMetaHost) Start(func(string, string)) {}
func (f *fakeAgentMetaHost) InboxChanged(string)        {}
func (f *fakeAgentMetaHost) Close()                     {}
func (f *fakeAgentMetaHost) SetMeta(store *wsmeta.Store, snap *wsmeta.Snapshot) {
	if store != nil && snap != nil {
		f.metaBound.Store(true)
	}
}
func (f *fakeAgentMetaHost) ValidateLegacyRecord(rel, replyID string, raw []byte) error {
	f.validator.Store(true)
	return nil
}
func (f *fakeAgentMetaHost) ForgetClosed(replyID string) bool {
	f.forgotList = append(f.forgotList, replyID)
	return f.forgetOK.Load()
}
func (f *fakeAgentMetaHost) AgentRecord(replyID string) (AgentRecordSnapshot, bool) {
	rec, ok := f.records[replyID]
	return rec, ok
}

// TestStartDaemonCutoverImportsLegacyAndClosesMetaBeforeUnlock verifies that
// StartDaemon runs the one-time cutover under dashboard.lock, binds AgentMetaHost
// and SupportMetaHost, and closes metadata.sqlite before releasing dashboard.lock
// so a successor immediately opens the store without SQLITE_BUSY (2122).
func TestStartDaemonCutoverImportsLegacyAndClosesMetaBeforeUnlock(t *testing.T) {
	dir := t.TempDir()
	project := t.TempDir()
	const rid = "legacyrid01"

	// Seed legacy stores before first StartDaemon.
	if err := WriteRegistration(dir, Registration{
		SchemaVersion: 1, ReplyID: rid, Project: project, Harness: "claude",
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
		HeartbeatAt:  time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	sessDir := SessionDir(dir, rid)
	if err := writeSessionLabel(sessDir, "Imported Label"); err != nil {
		t.Fatal(err)
	}
	if err := writeAckCheck(sessDir, ackCheckRecord{Through: 123456, InterruptedAt: 120000}); err != nil {
		t.Fatal(err)
	}
	if err := writeHiringMetadata(dir, rid, HiringMetadata{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	layoutRaw := `{"schema_version":1,"workspace_order":[],"workspaces":{"` + filepath.ToSlash(project) + `":{"stars":["` + rid + `"]}}}`
	if err := os.WriteFile(filepath.Join(dir, railLayoutFileName), []byte(layoutRaw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, wsmeta.LegacyTrackerHooksConfigName), []byte(`{"hooks":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, AgentRecordFileName), []byte(`{"reply_id":"`+rid+`","ws_session_key":"k1","root":"`+filepath.ToSlash(project)+`","lead":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	agents := &fakeAgentMetaHost{dir: dir}
	support := &fakeSupportMetaHost{}
	d1, err := StartDaemon(DaemonOptions{
		Dir:        dir,
		Home:       t.TempDir(),
		Version:    "0.53.1",
		noGitBadge: true,
		Agents:     agents,
		Support:    support,
	})
	if err != nil {
		t.Fatalf("StartDaemon: %v", err)
	}
	if d1.Meta() == nil || d1.MetaSnapshot() == nil {
		d1.Close()
		t.Fatal("expected SQLite meta store and snapshot after cutover")
	}
	if !agents.metaBound.Load() || !agents.validator.Load() || !support.loadedMeta.Load() {
		d1.Close()
		t.Fatalf("bindings missing: metaBound=%v validator=%v supportMeta=%v", agents.metaBound.Load(), agents.validator.Load(), support.loadedMeta.Load())
	}
	if got := d1.sessionLabel(rid); got != "Imported Label" {
		d1.Close()
		t.Fatalf("imported label = %q, want Imported Label", got)
	}
	if got := d1.readAckCheck(rid, sessDir); got != (ackCheckRecord{Through: 123456, InterruptedAt: 120000}) {
		d1.Close()
		t.Fatalf("imported ack-check = %+v", got)
	}
	if got, err := d1.readHiringMetadata(rid); err != nil || !got.Enabled {
		d1.Close()
		t.Fatalf("imported hiring = %+v, %v", got, err)
	}
	// Removal fence moved tracker-hooks.json -> tracker-hooks-v2.json.
	if _, err := os.Stat(filepath.Join(dir, wsmeta.LegacyTrackerHooksConfigName)); !errors.Is(err, os.ErrNotExist) {
		d1.Close()
		t.Fatalf("legacy tracker-hooks.json still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, wsmeta.TrackerHooksWitnessName)); err != nil {
		d1.Close()
		t.Fatalf("tracker-hooks-v2.json missing: %v", err)
	}
	// Mutate label post-cutover and verify successor sees it immediately on lock handoff.
	if err := d1.setSessionLabel(rid, "Post-Cutover Label"); err != nil {
		d1.Close()
		t.Fatal(err)
	}

	// Start a successor contending for dashboard.lock while d1 closes (2122).
	startErr := make(chan error, 1)
	var d2 *Daemon
	go func() {
		var err error
		d2, err = StartDaemon(DaemonOptions{
			Dir:        dir,
			Home:       t.TempDir(),
			Version:    "0.53.1",
			LockWait:   5 * time.Second,
			noGitBadge: true,
		})
		startErr <- err
	}()
	time.Sleep(40 * time.Millisecond)
	d1.Close()
	if err := <-startErr; err != nil {
		t.Fatalf("successor StartDaemon failed across lock handoff: %v", err)
	}
	defer d2.Close()
	if got := d2.sessionLabel(rid); got != "Post-Cutover Label" {
		t.Fatalf("successor label = %q, want Post-Cutover Label", got)
	}
	// Legacy label.json must still hold the pre-cutover bytes (never rewritten post-cutover).
	if raw, err := os.ReadFile(filepath.Join(sessDir, labelFileName)); err != nil || !json.Valid(raw) || string(raw) == `{"label":"Post-Cutover Label"}`+"\n" {
		t.Fatalf("legacy label.json was modified post-cutover: %q (%v)", raw, err)
	}
}

// TestStartDaemonCutoverUnsupportedFallsBackToLegacy verifies that an
// ErrCutoverUnsupported condition (an unreadable directory at tracker-hooks.json)
// leaves the daemon cleanly running on legacy files and invokes LoadLegacy /
// OnLegacyFallback.
func TestStartDaemonCutoverUnsupportedFallsBackToLegacy(t *testing.T) {
	dir := t.TempDir()
	// A directory at tracker-hooks.json triggers ErrRemovalUnreadable -> ErrCutoverUnsupported
	// before any replacement fence runs.
	if err := os.MkdirAll(filepath.Join(dir, wsmeta.LegacyTrackerHooksConfigName), 0o700); err != nil {
		t.Fatal(err)
	}
	support := &fakeSupportMetaHost{}
	var fallbackCalled atomic.Bool
	d, err := StartDaemon(DaemonOptions{
		Dir:              dir,
		Home:             t.TempDir(),
		Version:          "0.53.1",
		noGitBadge:       true,
		Support:          support,
		OnLegacyFallback: func() { fallbackCalled.Store(true) },
	})
	if err != nil {
		t.Fatalf("StartDaemon on unsupported legacy dir failed: %v", err)
	}
	defer d.Close()
	if d.Meta() != nil || d.MetaSnapshot() != nil {
		t.Fatal("expected nil Meta() on ErrCutoverUnsupported fallback")
	}
	if !support.loadedLegacy.Load() || !fallbackCalled.Load() {
		t.Fatalf("legacy fallback hooks not called: support=%v cb=%v", support.loadedLegacy.Load(), fallbackCalled.Load())
	}
}

// TestStartDaemonPruneDeletesSessionMetaAndRespectsForgetClosed verifies that
// pruneRegistrationsInMeta consults AgentForgetter.ForgetClosed for hosted
// records before removing files or SQLite rows, and deletes SQLite rows +
// in-memory caches once pruned.
func TestStartDaemonPruneDeletesSessionMetaAndRespectsForgetClosed(t *testing.T) {
	dir := t.TempDir()
	project := t.TempDir()
	old := time.Now().Add(-RegistryRetention - 2*time.Hour)
	const rid = "prunedlead01"

	if err := WriteRegistration(dir, Registration{
		SchemaVersion: 1, ReplyID: rid, Project: project, Harness: "claude",
		Hosted: true, RegisteredAt: stampFor(old), HeartbeatAt: stampFor(old), EndedAt: stampFor(old),
	}); err != nil {
		t.Fatal(err)
	}
	agents := &fakeAgentMetaHost{
		dir: dir,
		records: map[string]AgentRecordSnapshot{
			rid: {ReplyID: rid, Lead: true, ClosedAt: "2026-10-01T00:00:00Z"},
		},
	}
	agents.forgetOK.Store(false)
	d, err := StartDaemon(DaemonOptions{
		Dir:        dir,
		Home:       t.TempDir(),
		Version:    "0.53.1",
		noGitBadge: true,
		Agents:     agents,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Seed per-session SQLite state (record, label, ack-check, hiring).
	recRaw := []byte(`{"reply_id":"` + rid + `","ws_session_key":"k","root":"` + filepath.ToSlash(project) + `","lead":true,"closed_at":"2026-10-01T00:00:00Z"}`)
	if err := d.meta.PutRecord(context.Background(), rid, project, recRaw); err != nil {
		t.Fatal(err)
	}
	if err := d.setSessionLabel(rid, "To Be Pruned"); err != nil {
		t.Fatal(err)
	}
	if err := d.writeHiringMetadata(rid, HiringMetadata{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	d.inboxMu.Lock()
	err = d.writeAckCheckLocked(rid, SessionDir(dir, rid), ackCheckRecord{Through: 99})
	d.inboxMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	// 1. ForgetClosed returns false -> session directory AND SQLite rows stay.
	if n := d.pruneRegistrationsInMeta(time.Now(), RegistryRetention); n != 0 {
		t.Fatalf("pruned %d while ForgetClosed returned false, want 0", n)
	}
	if d.sessionLabel(rid) != "To Be Pruned" {
		t.Fatal("label was dropped even though ForgetClosed returned false")
	}

	// 2. ForgetClosed returns true -> directory removed AND SQLite + caches purged.
	agents.forgetOK.Store(true)
	if n := d.pruneRegistrationsInMeta(time.Now(), RegistryRetention); n != 1 {
		t.Fatalf("pruned %d, want 1", n)
	}
	if _, err := os.Stat(SessionDir(dir, rid)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pruned session dir still exists: %v", err)
	}
	if d.sessionLabel(rid) != "" || d.readAckCheck(rid, SessionDir(dir, rid)) != (ackCheckRecord{}) {
		t.Fatal("in-memory session caches were not cleared on prune")
	}
	if h, _ := d.readHiringMetadata(rid); h.Enabled || h.Hired {
		t.Fatalf("hiring cache not cleared on prune: %+v", h)
	}
	snap, err := d.meta.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Records[rid]; ok {
		t.Fatal("SQLite agent record survived prune")
	}
	if _, ok := snap.Labels[rid]; ok {
		t.Fatal("SQLite label survived prune")
	}
	if _, ok := snap.AckChecks[rid]; ok {
		t.Fatal("SQLite ack_check survived prune")
	}
	if _, ok := snap.Hiring[rid]; ok {
		t.Fatal("SQLite hiring survived prune")
	}
}

type fakeAgentForgetChecker struct {
	*fakeAgentMetaHost
	meta          *wsmeta.Store
	canForgetList []string
}

func (f *fakeAgentForgetChecker) SetMeta(store *wsmeta.Store, snap *wsmeta.Snapshot) {
	f.meta = store
	f.fakeAgentMetaHost.SetMeta(store, snap)
}

func (f *fakeAgentForgetChecker) CanForgetClosed(replyID string) bool {
	f.canForgetList = append(f.canForgetList, replyID)
	return f.forgetOK.Load()
}

func (f *fakeAgentForgetChecker) ForgetClosed(replyID string) bool {
	if !f.fakeAgentMetaHost.ForgetClosed(replyID) {
		return false
	}
	// Mirror wsagent.Runner.ForgetClosed: evict runner record and delete SQLite rows.
	delete(f.records, replyID)
	if f.meta != nil {
		_ = f.meta.DeleteSession(context.Background(), replyID)
	}
	return true
}

// TestStartDaemonPruneRetainsMetaOnRemoveFailureAndRetries verifies that when
// removing a closed hosted session directory fails (for example, a transient
// Windows AV handle), runner state, SQLite rows, and dashboard caches remain
// intact so a subsequent prune retry succeeds instead of stranding the dir.
func TestStartDaemonPruneRetainsMetaOnRemoveFailureAndRetries(t *testing.T) {
	dir := t.TempDir()
	project := t.TempDir()
	old := time.Now().Add(-RegistryRetention - 2*time.Hour)
	const rid = "pruneretry01"

	if err := WriteRegistration(dir, Registration{
		SchemaVersion: 1, ReplyID: rid, Project: project, Harness: "claude",
		Agent: true, Hosted: true,
		RegisteredAt: stampFor(old), HeartbeatAt: stampFor(old), EndedAt: stampFor(old),
	}); err != nil {
		t.Fatal(err)
	}
	checker := &fakeAgentForgetChecker{
		fakeAgentMetaHost: &fakeAgentMetaHost{
			dir: dir,
			records: map[string]AgentRecordSnapshot{
				rid: {ReplyID: rid, Lead: true, ClosedAt: "2026-10-01T00:00:00Z"},
			},
		},
	}
	checker.forgetOK.Store(true)
	d, err := StartDaemon(DaemonOptions{
		Dir:        dir,
		Home:       t.TempDir(),
		Version:    "0.53.1",
		noGitBadge: true,
		Agents:     checker,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	recRaw := []byte(`{"reply_id":"` + rid + `","ws_session_key":"k","root":"` + filepath.ToSlash(project) + `","lead":true,"closed_at":"2026-10-01T00:00:00Z"}`)
	if err := d.meta.PutRecord(context.Background(), rid, project, recRaw); err != nil {
		t.Fatal(err)
	}
	if err := d.setSessionLabel(rid, "Retry Label"); err != nil {
		t.Fatal(err)
	}
	if err := d.writeHiringMetadata(rid, HiringMetadata{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	d.inboxMu.Lock()
	err = d.writeAckCheckLocked(rid, SessionDir(dir, rid), ackCheckRecord{Through: 77})
	d.inboxMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	origRemove := removeSessionDir
	t.Cleanup(func() { removeSessionDir = origRemove })

	// 1. Simulated os.RemoveAll failure must leave runner, SQLite, and dashboard
	// caches intact so the directory is not stranded on subsequent passes.
	removeSessionDir = func(string) error { return errors.New("simulated AV lock") }
	if n := d.pruneRegistrationsInMeta(time.Now(), RegistryRetention); n != 0 {
		t.Fatalf("pruned %d on failed remove, want 0", n)
	}
	if len(checker.canForgetList) != 1 || checker.canForgetList[0] != rid {
		t.Fatalf("CanForgetClosed calls = %v, want [%s]", checker.canForgetList, rid)
	}
	if len(checker.forgotList) != 0 {
		t.Fatalf("ForgetClosed must not run when RemoveAll fails; got %v", checker.forgotList)
	}
	if _, ok := checker.records[rid]; !ok {
		t.Fatal("runner record was evicted despite RemoveAll failure")
	}
	if _, err := os.Stat(SessionDir(dir, rid)); err != nil {
		t.Fatalf("session dir missing after failed remove: %v", err)
	}
	if d.sessionLabel(rid) != "Retry Label" {
		t.Fatalf("dashboard label cache lost on failed remove: %q", d.sessionLabel(rid))
	}
	if got := d.readAckCheck(rid, SessionDir(dir, rid)); got.Through != 77 {
		t.Fatalf("dashboard ack-check cache lost on failed remove: %+v", got)
	}
	if h, _ := d.readHiringMetadata(rid); !h.Enabled {
		t.Fatalf("dashboard hiring cache lost on failed remove: %+v", h)
	}
	snap, err := d.meta.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Records[rid]; !ok {
		t.Fatal("SQLite agent record deleted despite RemoveAll failure")
	}
	if _, ok := snap.Labels[rid]; !ok {
		t.Fatal("SQLite label deleted despite RemoveAll failure")
	}
	if _, ok := snap.AckChecks[rid]; !ok {
		t.Fatal("SQLite ack_check deleted despite RemoveAll failure")
	}
	if _, ok := snap.Hiring[rid]; !ok {
		t.Fatal("SQLite hiring deleted despite RemoveAll failure")
	}

	// 2. Once removal is unblocked, retry prunes the directory and cleans up
	// runner, SQLite, and dashboard caches cleanly.
	removeSessionDir = origRemove
	if n := d.pruneRegistrationsInMeta(time.Now(), RegistryRetention); n != 1 {
		t.Fatalf("retry pruned %d, want 1", n)
	}
	if len(checker.forgotList) != 1 || checker.forgotList[0] != rid {
		t.Fatalf("ForgetClosed calls after retry = %v, want [%s]", checker.forgotList, rid)
	}
	if _, ok := checker.records[rid]; ok {
		t.Fatal("runner record survived successful retry prune")
	}
	if _, err := os.Stat(SessionDir(dir, rid)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session dir survived successful retry prune: %v", err)
	}
	if d.sessionLabel(rid) != "" || d.readAckCheck(rid, SessionDir(dir, rid)) != (ackCheckRecord{}) {
		t.Fatal("in-memory session caches were not cleared on retry prune")
	}
	if h, _ := d.readHiringMetadata(rid); h.Enabled || h.Hired {
		t.Fatalf("hiring cache not cleared on retry prune: %+v", h)
	}
	snap2, err := d.meta.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap2.Records[rid]; ok {
		t.Fatal("SQLite agent record survived retry prune")
	}
	if _, ok := snap2.Labels[rid]; ok {
		t.Fatal("SQLite label survived retry prune")
	}
	if _, ok := snap2.AckChecks[rid]; ok {
		t.Fatal("SQLite ack_check survived retry prune")
	}
	if _, ok := snap2.Hiring[rid]; ok {
		t.Fatal("SQLite hiring survived retry prune")
	}
}
