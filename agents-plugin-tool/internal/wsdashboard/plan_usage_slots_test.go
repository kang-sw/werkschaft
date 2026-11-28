package wsdashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

func openMetaForUsageSlotsTest(t *testing.T, dir string) (*wsmeta.Store, *wsmeta.Snapshot) {
	t.Helper()
	ctx := context.Background()
	meta, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	if _, err := meta.Cutover(ctx, wsmeta.CutoverOptions{}); err != nil {
		t.Fatal(err)
	}
	snap, err := meta.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return meta, snap
}

// TestPlanUsageSlotRestartSeedingAndRoundTrip verifies that:
//  1. After a daemon restart with a non-default active slot, seedSlots() binds
//     the real active slot before any stash or history stamp occurs (non-trivial
//     Harnesses traits required).
//  2. Switching A -> B -> A stashes A, restores A immediately on return, and a
//     newer push supersedes the restored snapshot.
//  3. Removing a stashed slot prunes both in-memory cache and wsmeta entry.
func TestPlanUsageSlotRestartSeedingAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	metaDir := filepath.Join(t.TempDir(), "dashboard")
	home := t.TempDir()
	slotsFile := filepath.Join(t.TempDir(), "auth", wscred.AccountSlotsFileName)
	workDir := filepath.Join(t.TempDir(), "claude-work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}

	slots := wscred.NewAccountSlotRegistry(slotsFile)
	t.Cleanup(slots.Close)
	if _, err := slots.AddSlot(wscred.AccountSlot{ID: "work", Harness: "claude", Label: "Work", ConfigDir: workDir}); err != nil {
		t.Fatal(err)
	}
	if err := slots.SetActive("claude", "work"); err != nil {
		t.Fatal(err)
	}

	meta, snap := openMetaForUsageSlotsTest(t, metaDir)
	now := time.Now().UnixMilli()
	resetWork := now + 3*time.Hour.Milliseconds()
	resetDef := now + 4*time.Hour.Milliseconds()

	// Seed persisted active "claude" entry (as if daemon 1 ran while "work" was active).
	initialWork := planUsageEntry{
		Harness: "claude",
		Source:  "Claude push",
		AsOf:    now - 60_000,
		Windows: []planUsageWindow{{Kind: wslive.LimitFiveHour, Percent: 61, ResetsAt: resetWork, AsOf: now - 60_000}},
	}
	rawWork, err := json.Marshal(initialWork)
	if err != nil {
		t.Fatal(err)
	}
	if err := meta.PutUsageLatestEntry(ctx, wsmeta.UsageSourcePlan, "claude", rawWork); err != nil {
		t.Fatal(err)
	}
	snap, err = meta.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Simulated restart: construct store from meta, wire non-trivial Harnesses, seedSlots.
	s := newPlanUsageStoreFromMeta(metaDir, home, func(string) string { return "" }, meta, snap)
	s.accountSlots = slots
	s.harnesses = testHarnessTraits()
	s.seedSlots()

	// Push on the active non-default slot ("work"): sample must be stamped "work".
	s.pushClaude(&wslive.Snapshot{
		Adapter:   "claude",
		UpdatedAt: now,
		Usage: &wslive.Usage{
			LimitsAsOf: now,
			Limits:     []wslive.UsageLimit{{Kind: wslive.LimitFiveHour, Percent: 64, ResetsAt: resetWork}},
		},
	})
	if hist := s.history(now); len(hist.Samples) != 1 || hist.Samples[0].SlotID != "work" {
		t.Fatalf("expected sample stamped with active slot 'work', got %+v", hist.Samples)
	}

	// Switch "work" -> "default": "work" must be stashed under claude:work (NOT claude:default).
	if err := slots.SetActive("claude", wscred.DefaultAccountSlotID); err != nil {
		t.Fatal(err)
	}
	s.invalidateSlots()

	s.mu.Lock()
	_, hasWorkStash := s.slotSnapshots[slotKey{harness: "claude", slotID: "work"}]
	_, hasWrongDefaultStash := s.slotSnapshots[slotKey{harness: "claude", slotID: wscred.DefaultAccountSlotID}]
	s.mu.Unlock()
	if !hasWorkStash || hasWrongDefaultStash {
		t.Fatalf("post-restart switch stashed under wrong slot: work=%v default=%v", hasWorkStash, hasWrongDefaultStash)
	}

	// Push on "default".
	s.pushClaude(&wslive.Snapshot{
		Adapter:   "claude",
		UpdatedAt: now + 10_000,
		Usage: &wslive.Usage{
			LimitsAsOf: now + 10_000,
			Limits:     []wslive.UsageLimit{{Kind: wslive.LimitFiveHour, Percent: 15, ResetsAt: resetDef}},
		},
	})

	// Switch "default" -> "work": "work" snapshot (64%) is restored immediately before any push.
	if err := slots.SetActive("claude", "work"); err != nil {
		t.Fatal(err)
	}
	s.invalidateSlots()
	entries := s.entries()
	if len(entries) != 1 || len(entries[0].Windows) != 1 || entries[0].Windows[0].Percent != 64 || entries[0].AsOf != now {
		t.Fatalf("expected immediate restore of 'work' snapshot (64%%), got %+v", entries)
	}

	// A newer push supersedes the restored snapshot.
	s.pushClaude(&wslive.Snapshot{
		Adapter:   "claude",
		UpdatedAt: now + 20_000,
		Usage: &wslive.Usage{
			LimitsAsOf: now + 20_000,
			Limits:     []wslive.UsageLimit{{Kind: wslive.LimitFiveHour, Percent: 72, ResetsAt: resetWork}},
		},
	})
	entries = s.entries()
	if len(entries) != 1 || entries[0].Windows[0].Percent != 72 || entries[0].AsOf != now+20_000 {
		t.Fatalf("newer push did not supersede restored snapshot: %+v", entries)
	}

	// Switch back to "default" so "work" is inactive and previewable, then remove "work".
	if err := slots.SetActive("claude", wscred.DefaultAccountSlotID); err != nil {
		t.Fatal(err)
	}
	s.invalidateSlots()
	if previews := s.slotPreviews(now + 25_000); previews["claude"]["work"].AsOf != now+20_000 {
		t.Fatalf("expected preview for inactive 'work', got %+v", previews)
	}

	if err := slots.RemoveSlot("claude", "work"); err != nil {
		t.Fatal(err)
	}
	s.invalidateSlots()

	if previews := s.slotPreviews(now + 25_000); len(previews["claude"]) != 0 {
		t.Fatalf("removed slot still present in previews: %+v", previews)
	}
	afterSnap, err := meta.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range afterSnap.LatestUsage[wsmeta.UsageSourcePlan].Entries {
		if e.Key == "claude:work" {
			t.Fatalf("removed slot key claude:work was not pruned from wsmeta")
		}
	}
}

// TestPlanUsageSlotSamplePartitioningAndLegacySamples verifies:
//   - Stamped samples from slot A do not leak into slot B's active-slot projection
//     (including as a neighbor link).
//   - Unstamped legacy samples remain drawable for the active slot, while strict
//     inactive-slot previews return only that slot's own stamped samples.
func TestPlanUsageSlotSamplePartitioningAndLegacySamples(t *testing.T) {
	s := usageTestStore(t)
	now := time.Now().UnixMilli()
	reset := now + 3*time.Hour.Milliseconds()
	base := now - 300_000

	legacy := planUsageSample{Harness: "claude", Kind: wslive.LimitFiveHour, Source: "Claude push", ResetsAt: reset, AsOf: base, Percent: 10}
	slotA := planUsageSample{Harness: "claude", Kind: wslive.LimitFiveHour, Source: "Claude push", SlotID: "default", ResetsAt: reset, AsOf: base + 1000, Percent: 20}
	slotB := planUsageSample{Harness: "claude", Kind: wslive.LimitFiveHour, Source: "Claude push", SlotID: "work", ResetsAt: reset, AsOf: base + 2000, Percent: 35}

	s.mu.Lock()
	s.appendHistoryLocked([]planUsageSample{legacy, slotA, slotB}, now)
	s.claudeSlotID = "work"
	s.claude = planUsageEntry{
		Harness: "claude",
		Source:  "Claude push",
		AsOf:    base + 2000,
		Windows: []planUsageWindow{{Kind: wslive.LimitFiveHour, Percent: 35, ResetsAt: reset, AsOf: base + 2000}},
	}
	s.slotSnapshots = map[slotKey]planUsageEntry{
		{harness: "claude", slotID: "default"}: {
			Harness: "claude",
			Source:  "Claude push",
			AsOf:    base + 1000,
			Windows: []planUsageWindow{{Kind: wslive.LimitFiveHour, Percent: 20, ResetsAt: reset, AsOf: base + 1000}},
		},
	}
	s.mu.Unlock()

	entries := []planUsageEntry{s.claude}
	var activeProj planUsageHistoryProjection
	if err := json.Unmarshal(s.historyView(entries, now), &activeProj); err != nil {
		t.Fatal(err)
	}
	// Active slot "work" sees legacy + slotB, and drops slotA completely (no neighbor link to slotA).
	if len(activeProj.Samples) != 2 || activeProj.Samples[0] != legacy || activeProj.Samples[1] != slotB {
		t.Fatalf("active slot projection leaked other slot or dropped legacy: %+v", activeProj.Samples)
	}

	// Inactive slot "default" preview sees ONLY its own stamped sample slotA (not legacy, not slotB).
	previews := s.slotPreviews(now)
	defPrev, ok := previews["claude"]["default"]
	if !ok {
		t.Fatalf("missing inactive slot preview: %+v", previews)
	}
	if len(defPrev.Samples) != 1 || defPrev.Samples[0] != slotA {
		t.Fatalf("inactive preview samples = %+v, want only %+v", defPrev.Samples, slotA)
	}
}

// TestPlanUsageSlotPreviewsAPIExpiredAndZeroProbe verifies:
//   - slotPreviews is absent without history=1
//   - slotPreviews omits the active slot and drops expired windows (and omits slots with no live windows)
//   - Codex previews include both 7D (shared history) and 5H (codex history) stamped samples
//   - inactive slots are never probed and slotPreviews reads zero files
func TestPlanUsageSlotPreviewsAPIExpiredAndZeroProbe(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	slotsFile := filepath.Join(t.TempDir(), "auth", wscred.AccountSlotsFileName)
	workCodexDir := filepath.Join(t.TempDir(), "codex-work")
	expiredCodexDir := filepath.Join(t.TempDir(), "codex-expired")
	writeCodexAuth(t, filepath.Join(home, ".codex"), "acct-default")
	writeCodexAuth(t, workCodexDir, "acct-work")
	writeCodexAuth(t, expiredCodexDir, "acct-expired")

	slots := wscred.NewAccountSlotRegistry(slotsFile)
	t.Cleanup(slots.Close)
	if _, err := slots.AddSlot(wscred.AccountSlot{ID: "work", Harness: "codex", Label: "Work", ConfigDir: workCodexDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := slots.AddSlot(wscred.AccountSlot{ID: "expired", Harness: "codex", Label: "Expired", ConfigDir: expiredCodexDir}); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	nowMs := now.UnixMilli()
	reset5h := now.Add(2 * time.Hour).UnixMilli()
	reset7d := now.Add(5 * 24 * time.Hour).UnixMilli()
	expiredReset := now.Add(-10 * time.Minute).UnixMilli()

	var probeCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan_type": "pro",
			"rate_limit": map[string]any{
				"primary_window":   map[string]any{"used_percent": 19.0, "limit_window_seconds": 18000, "reset_at": reset5h / 1000},
				"secondary_window": map[string]any{"used_percent": 33.0, "limit_window_seconds": 604800, "reset_at": reset7d / 1000},
			},
		})
	}))
	defer srv.Close()

	s := newTestPlanUsageStore(dir, home, func(string) string { return "" })
	defer s.close(context.Background())
	s.now = func() time.Time { return now }
	s.usageURL = srv.URL
	s.accountSlots = slots
	s.seedSlots()

	// Populate active "default" via one probe (records both 7D and 5H stamped samples).
	s.query(context.Background(), false)
	if probeCalls.Load() != 1 {
		t.Fatalf("expected 1 probe for active slot, got %d", probeCalls.Load())
	}

	// Switch to "work", seed an expired inactive slot and a live inactive "default",
	// and give "work" an existing entry so refresh does not trigger another probe.
	if err := slots.SetActive("codex", "work"); err != nil {
		t.Fatal(err)
	}
	s.invalidateSlots()
	s.mu.Lock()
	s.gpt = planUsageEntry{
		Harness: "codex",
		Plan:    "pro",
		Source:  codexUsageSource,
		AsOf:    nowMs,
		Windows: []planUsageWindow{{Kind: wslive.LimitSevenDay, Percent: 50, ResetsAt: reset7d, AsOf: nowMs}},
	}
	s.lastQuery = now
	s.slotSnapshots[slotKey{harness: "codex", slotID: "expired"}] = planUsageEntry{
		Harness: "codex",
		Source:  codexUsageSource,
		AsOf:    nowMs - 3600_000,
		Windows: []planUsageWindow{{Kind: wslive.LimitFiveHour, Percent: 88, ResetsAt: expiredReset, AsOf: nowMs - 3600_000}},
	}
	// Also put a stale entry under active slot key "work" to verify active slot is omitted.
	s.slotSnapshots[slotKey{harness: "codex", slotID: "work"}] = planUsageEntry{
		Harness: "codex",
		Source:  codexUsageSource,
		AsOf:    nowMs - 1000,
		Windows: []planUsageWindow{{Kind: wslive.LimitSevenDay, Percent: 40, ResetsAt: reset7d, AsOf: nowMs - 1000}},
	}
	s.mu.Unlock()

	var previewFileReads atomic.Int32
	s.readFile = func(path string) ([]byte, error) {
		previewFileReads.Add(1)
		return os.ReadFile(path)
	}

	d := &Daemon{dir: dir, planUsage: s, harnesses: testHarnessTraits()}

	// Without history=1: slotPreviews must be absent and zero extra probes run.
	recNoHist := httptest.NewRecorder()
	d.handlePlanUsage(recNoHist, httptest.NewRequest(http.MethodGet, "/rail/usage", nil))
	var noHist map[string]json.RawMessage
	if err := json.Unmarshal(recNoHist.Body.Bytes(), &noHist); err != nil {
		t.Fatal(err)
	}
	if _, ok := noHist["slotPreviews"]; ok {
		t.Fatalf("slotPreviews must be absent without history=1: %s", recNoHist.Body.String())
	}

	// With history=1: slotPreviews contains only inactive live "default" (omits active "work" and "expired").
	recHist := httptest.NewRecorder()
	d.handlePlanUsage(recHist, httptest.NewRequest(http.MethodGet, "/rail/usage?history=1", nil))
	var withHist struct {
		SlotPreviews map[string]map[string]planUsageSlotPreview `json:"slotPreviews"`
	}
	if err := json.Unmarshal(recHist.Body.Bytes(), &withHist); err != nil {
		t.Fatal(err)
	}
	codexPrev := withHist.SlotPreviews["codex"]
	if len(codexPrev) != 1 {
		t.Fatalf("expected exactly 1 live inactive codex preview, got %+v", codexPrev)
	}
	if _, hasActive := codexPrev["work"]; hasActive {
		t.Fatalf("active slot 'work' must be omitted from slotPreviews")
	}
	if _, hasExpired := codexPrev["expired"]; hasExpired {
		t.Fatalf("expired slot must be omitted from slotPreviews")
	}
	defPrev, ok := codexPrev["default"]
	if !ok || len(defPrev.Windows) != 2 {
		t.Fatalf("expected live 5H+7D windows for inactive 'default', got %+v", defPrev)
	}
	// Both 7D (from shared history) and 5H (from codex history) stamped samples must be projected.
	kinds := map[string]bool{}
	for _, smp := range defPrev.Samples {
		if smp.SlotID != "default" {
			t.Fatalf("unexpected preview sample slotId: %+v", smp)
		}
		kinds[smp.Kind] = true
	}
	if !kinds[wslive.LimitFiveHour] || !kinds[wslive.LimitSevenDay] {
		t.Fatalf("expected both 5H and 7D preview samples for inactive codex slot, got %+v", defPrev.Samples)
	}
	if probeCalls.Load() != 1 {
		t.Fatalf("inactive slots must never be probed, probe calls=%d", probeCalls.Load())
	}
	// Verify calling slotPreviews directly performs zero file reads.
	previewFileReads.Store(0)
	_ = s.slotPreviews(nowMs)
	if previewFileReads.Load() != 0 {
		t.Fatalf("slotPreviews read %d files, want 0", previewFileReads.Load())
	}
}

func TestPlanUsageQueryDiscardsResultWhenSlotChangesMidProbe(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	slotsFile := filepath.Join(t.TempDir(), "auth", wscred.AccountSlotsFileName)
	workDir := filepath.Join(t.TempDir(), "codex-work")
	writeCodexAuth(t, filepath.Join(home, ".codex"), "acct-default")
	writeCodexAuth(t, workDir, "acct-work")

	slots := wscred.NewAccountSlotRegistry(slotsFile)
	t.Cleanup(slots.Close)
	if _, err := slots.AddSlot(wscred.AccountSlot{ID: "work", Harness: "codex", Label: "Work", ConfigDir: workDir}); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	reset5h := now.Add(3 * time.Hour).UnixMilli()
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan_type": "pro",
			"rate_limit": map[string]any{
				"primary_window": map[string]any{"used_percent": 77.0, "limit_window_seconds": 18000, "reset_at": reset5h / 1000},
			},
		})
	}))
	defer srv.Close()

	s := newTestPlanUsageStore(dir, home, func(string) string { return "" })
	defer s.close(context.Background())
	s.now = func() time.Time { return now }
	s.usageURL = srv.URL
	s.accountSlots = slots
	s.seedSlots()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.query(context.Background(), true)
	}()

	<-started
	if err := slots.SetActive("codex", "work"); err != nil {
		t.Fatal(err)
	}
	s.invalidateSlots()
	close(release)
	<-done

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gptSlotID != "work" {
		t.Fatalf("gptSlotID = %q, want work", s.gptSlotID)
	}
	if len(s.gpt.Windows) != 0 {
		t.Fatalf("stale pre-switch probe polluted work slot: %+v", s.gpt)
	}
	if !s.lastQuery.IsZero() {
		t.Fatalf("lastQuery should stay zero when probe result is discarded, got %v", s.lastQuery)
	}
	if len(s.codexHist.file.Records) != 0 || len(s.hist.file.Records) != 0 {
		t.Fatalf("discarded probe must not append history samples")
	}
}
