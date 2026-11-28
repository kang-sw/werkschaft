package wsdashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
)

type fakeOpenAIUsageStore struct {
	mu       sync.Mutex
	identity wscred.OpenAIUsageIdentity
	probeRes wscred.OpenAIPlanUsageResult
	probeErr error
	calls    atomic.Int32
}

func (f *fakeOpenAIUsageStore) ActiveOpenAIUsageIdentity() wscred.OpenAIUsageIdentity {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.identity
}

func (f *fakeOpenAIUsageStore) ProbeOpenAIPlanUsage(_ context.Context) (wscred.OpenAIPlanUsageResult, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.probeRes, f.probeErr
}

func writeCodexAuth(t *testing.T, codexHome, accountID string) {
	t.Helper()
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token": "codex-access-token",
			"account_id":   accountID,
		},
	})
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPlanUsageCodexOpenAIExactWorkspaceMatchUnifiesAndDeduplicatesProbe(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	const rawWorkspaceID = "acct-shared-workspace-999"

	writeCodexAuth(t, filepath.Join(home, ".codex"), rawWorkspaceID)

	var codexProbeCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		codexProbeCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan_type": "pro",
			"rate_limit": map[string]any{
				"primary_window":   map[string]any{"used_percent": 25.0, "limit_window_seconds": 18000, "reset_at": now.Add(2 * time.Hour).Unix()},
				"secondary_window": map[string]any{"used_percent": 40.0, "limit_window_seconds": 604800, "reset_at": now.Add(4 * 24 * time.Hour).Unix()},
			},
		})
	}))
	defer srv.Close()

	s := newPlanUsageStoreBase(dir, home, func(string) string { return "" })
	defer s.close(context.Background())
	s.now = func() time.Time { return now }
	s.usageURL = srv.URL
	s.harnesses = testHarnessTraits()

	fakeOA := &fakeOpenAIUsageStore{
		identity: wscred.OpenAIUsageIdentity{
			Configured:        true,
			ClientID:          "oaiapp_match_1",
			MaskedLabel:       "al***@example.com",
			MatchToken:        wscred.AccountMatchToken(rawWorkspaceID),
			HasAccountClaim:   true,
			PlanEnabled:       true,
			BackgroundConsent: true,
		},
	}
	s.credentials = fakeOA

	// Record usage for both the active account and another account; only active ClientID must appear.
	s.recordWerkUsage(WerkUsageDelta{
		Provider:     "openai",
		AccountKey:   "oaiapp_match_1",
		Model:        "gpt-5.4",
		InputTokens:  10000,
		OutputTokens: 2500,
		CostUSD:      0,
		At:           now.UnixMilli(),
	})
	s.recordWerkUsage(WerkUsageDelta{
		Provider:     "openai",
		AccountKey:   "oaiapp_other_2",
		Model:        "gpt-5.4-mini",
		InputTokens:  99999,
		OutputTokens: 11111,
		At:           now.UnixMilli(),
	})

	s.refresh(context.Background(), true)
	s.probes.Wait()

	if codexProbeCalls.Load() != 1 {
		t.Fatalf("expected strictly 1 Codex wham/usage probe, got %d", codexProbeCalls.Load())
	}
	if fakeOA.calls.Load() != 0 {
		t.Fatalf("unified match must deduplicate and skip Werk OpenAI wham/usage probe, got %d calls", fakeOA.calls.Load())
	}

	d := &Daemon{dir: dir, planUsage: s, harnesses: testHarnessTraits(), index: NewIndex(IndexOptions{Dir: dir}), snapshots: &SnapshotCache{}}
	rec := httptest.NewRecorder()
	d.handlePlanUsage(rec, httptest.NewRequest(http.MethodGet, "/rail/usage?history=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rawBody := rec.Body.String()
	if strings.Contains(rawBody, rawWorkspaceID) || strings.Contains(rawBody, "codex-access-token") {
		t.Fatalf("raw account ID or token leaked into /rail/usage: %s", rawBody)
	}

	var payload struct {
		Entries              []planUsageEntry `json:"entries"`
		CodexFiveHourHistory json.RawMessage  `json:"codexFiveHourHistory"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Entries) != 1 {
		t.Fatalf("expected 1 unified card, got %d: %+v", len(payload.Entries), payload.Entries)
	}
	e := payload.Entries[0]
	if e.Harness != codexOpenAIUnifiedHarness || e.Label != "Codex / OpenAI" || e.AccountLabel != "al***@example.com" {
		t.Fatalf("unexpected unified entry header: %+v", e)
	}
	if len(e.Windows) != 2 || e.Werk == nil || !e.Werk.Unified || !e.Werk.TokenFirst {
		t.Fatalf("unexpected unified entry payload: %+v", e)
	}
	if len(e.Werk.Models) != 1 || e.Werk.Models[0].Model != "gpt-5.4" || e.Werk.TotalInputTokens != 10000 {
		t.Fatalf("expected only active ClientID models, got %+v", e.Werk)
	}
	if len(payload.CodexFiveHourHistory) == 0 {
		t.Fatalf("expected codexFiveHourHistory projected for unified codex_openai card")
	}
}

func TestPlanUsageSameEmailDifferentWorkspacesStaySeparate(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	writeCodexAuth(t, filepath.Join(home, ".codex"), "acct-workspace-A")

	s := newPlanUsageStoreBase(dir, home, func(string) string { return "" })
	defer s.close(context.Background())
	s.now = func() time.Time { return now }
	s.gpt = planUsageEntry{
		Harness: "codex",
		Plan:    "pro",
		Source:  codexUsageSource,
		AsOf:    now.UnixMilli(),
		Windows: []planUsageWindow{{Kind: "seven_day", Percent: 18, ResetsAt: now.Add(24 * time.Hour).UnixMilli(), AsOf: now.UnixMilli()}},
	}

	// Same email (alice@example.com -> al***@example.com) but Workspace B != Workspace A.
	fakeOA := &fakeOpenAIUsageStore{
		identity: wscred.OpenAIUsageIdentity{
			Configured:        true,
			ClientID:          "oaiapp_ws_B",
			MaskedLabel:       "al***@example.com",
			MatchToken:        wscred.AccountMatchToken("acct-workspace-B"),
			HasAccountClaim:   true,
			PlanEnabled:       true,
			BackgroundConsent: false, // also verifies consent=false stays on local fallback
		},
	}
	s.credentials = fakeOA
	s.recordWerkUsage(WerkUsageDelta{
		Provider:     "openai",
		AccountKey:   "oaiapp_ws_B",
		Model:        "gpt-5.4",
		InputTokens:  4200,
		OutputTokens: 800,
		At:           now.UnixMilli(),
	})

	s.refreshWerkOpenAI()
	s.probes.Wait()
	if fakeOA.calls.Load() != 0 {
		t.Fatalf("background_consent=false must never run remote probe, got %d calls", fakeOA.calls.Load())
	}

	entries := s.entries()
	if len(entries) != 2 {
		t.Fatalf("expected separate codex and werk_openai cards, got %d: %+v", len(entries), entries)
	}
	if entries[0].Harness != "codex" || entries[0].Werk != nil || entries[0].AccountLabel != "" {
		t.Fatalf("unmatched codex card shape must stay unchanged: %+v", entries[0])
	}
	w := entries[1]
	if w.Harness != werkOpenAIHarness || w.AccountLabel != "al***@example.com" || w.Werk == nil {
		t.Fatalf("unexpected werk_openai card: %+v", w)
	}
	if w.Werk.Mode != "local" || w.Werk.FallbackReason != "consent_required" || w.Werk.TotalInputTokens != 4200 {
		t.Fatalf("unexpected local fallback view: %+v", w.Werk)
	}
}

func TestPlanUsageWerkOpenAIFallbacksAndSlotSwitchRecomputation(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	// Default codex home has Workspace A; secondary slot has Workspace B.
	writeCodexAuth(t, filepath.Join(home, ".codex"), "acct-slot-A")
	slotBDir := filepath.Join(t.TempDir(), "codex-slot-b")
	writeCodexAuth(t, slotBDir, "acct-slot-B")

	slots := wscred.NewAccountSlotRegistry(filepath.Join(dir, "slots.json"))
	slotB, err := slots.AddSlot(wscred.AccountSlot{ID: "work-b", Harness: "codex", Label: "Work B", ConfigDir: slotBDir})
	if err != nil {
		t.Fatal(err)
	}

	s := newPlanUsageStoreBase(dir, home, func(string) string { return "" })
	defer s.close(context.Background())
	s.now = func() time.Time { return now }
	s.accountSlots = slots
	s.harnesses = testHarnessTraits()
	s.gpt = planUsageEntry{
		Harness: "codex",
		Source:  codexUsageSource,
		AsOf:    now.UnixMilli(),
		Windows: []planUsageWindow{{Kind: "five_hour", Percent: 12, ResetsAt: now.Add(time.Hour).UnixMilli(), AsOf: now.UnixMilli()}},
	}

	// Werk OpenAI is signed into Workspace B.
	fakeOA := &fakeOpenAIUsageStore{
		identity: wscred.OpenAIUsageIdentity{
			Configured:        true,
			ClientID:          "oaiapp_b",
			MaskedLabel:       "bo***@work.example",
			MatchToken:        wscred.AccountMatchToken("acct-slot-B"),
			HasAccountClaim:   true,
			PlanEnabled:       true,
			BackgroundConsent: true,
		},
		probeRes: wscred.OpenAIPlanUsageResult{
			Supported:      false,
			FallbackReason: "auth_rejected",
			HTTPStatus:     http.StatusForbidden,
			AsOf:           now.UnixMilli(),
		},
	}
	s.credentials = fakeOA

	// Initially Codex default slot is Workspace A -> separate cards; probe runs and gets 403 local fallback.
	s.refreshWerkOpenAI()
	s.probes.Wait()
	if fakeOA.calls.Load() != 1 {
		t.Fatalf("expected 1 probe call on unmatched account, got %d", fakeOA.calls.Load())
	}
	entries := s.entries()
	if len(entries) != 2 || entries[0].Harness != "codex" || entries[1].Harness != werkOpenAIHarness || entries[1].Werk.FallbackReason != "auth_rejected" {
		t.Fatalf("expected separate cards with auth_rejected fallback, got %+v", entries)
	}

	// Switch Codex active slot to Workspace B via D10 slot registry and invalidateSlots().
	if err := slots.SetActive("codex", slotB.ID); err != nil {
		t.Fatal(err)
	}
	s.invalidateSlots()
	entries = s.entries()
	if len(entries) != 1 || entries[0].Harness != codexOpenAIUnifiedHarness {
		t.Fatalf("expected dynamic unification after D10 slot switch to matching workspace, got %+v", entries)
	}

	// Verify paused state and missing claim stay on local fallback without network calls.
	if err := slots.SetActive("codex", wscred.DefaultAccountSlotID); err != nil {
		t.Fatal(err)
	}
	s.invalidateSlots()

	fakeOA.mu.Lock()
	fakeOA.identity.PlanPaused = true
	fakeOA.mu.Unlock()
	s.refreshWerkOpenAI()
	s.probes.Wait()
	if fakeOA.calls.Load() != 1 {
		t.Fatalf("plan paused must not run probe, calls=%d", fakeOA.calls.Load())
	}
	entries = s.entries()
	if len(entries) != 2 || !entries[1].Werk.PlanPaused || entries[1].Werk.FallbackReason != "plan_paused" {
		t.Fatalf("expected plan_paused local fallback, got %+v", entries)
	}

	fakeOA.mu.Lock()
	fakeOA.identity.PlanPaused = false
	fakeOA.identity.HasAccountClaim = false
	fakeOA.identity.MatchToken = ""
	fakeOA.mu.Unlock()
	s.refreshWerkOpenAI()
	s.probes.Wait()
	if fakeOA.calls.Load() != 1 {
		t.Fatalf("missing claim must not run probe, calls=%d", fakeOA.calls.Load())
	}
	entries = s.entries()
	if len(entries) != 2 || entries[1].Werk.FallbackReason != "missing_claim" {
		t.Fatalf("expected missing_claim local fallback, got %+v", entries)
	}
}
