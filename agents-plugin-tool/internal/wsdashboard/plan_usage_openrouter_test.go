package wsdashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
)

type fakeOpenRouterStore struct {
	mu         sync.Mutex
	configured bool
	masked     string
	rejected   bool
	gen        uint64
	calls      int
	snap       wscred.OpenRouterUsageSnapshot
	err        error
}

func (f *fakeOpenRouterStore) OpenRouterUsageMeta() (bool, string, bool, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.configured, f.masked, f.rejected, f.gen
}

func (f *fakeOpenRouterStore) ProbeOpenRouterUsage(_ context.Context) (wscred.OpenRouterUsageSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.snap, f.err
}

func ptrFloat(v float64) *float64 { return &v }

func TestPlanUsageOpenRouterHybridCardAndDebounce(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	s := newPlanUsageStoreBase(dir, dir, func(string) string { return "" })
	defer s.close(context.Background())
	s.now = func() time.Time { return now }

	fake := &fakeOpenRouterStore{
		configured: true,
		masked:     "sk-or-***9999",
		gen:        1,
		snap: wscred.OpenRouterUsageSnapshot{
			Credits: &wscred.OpenRouterCredits{TotalCredits: 100, TotalUsage: 22.5},
			Key: &wscred.OpenRouterKeyLimits{
				Limit:          ptrFloat(50),
				LimitRemaining: ptrFloat(37.5),
				UsageDaily:     ptrFloat(1.2),
				UsageWeekly:    ptrFloat(5.4),
				UsageMonthly:   ptrFloat(12.5),
			},
			AsOf: now.UnixMilli(),
		},
	}
	s.credentials = fake

	s.recordWerkUsage(WerkUsageDelta{
		Provider:     "openrouter",
		Model:        "anthropic/claude-sonnet-4",
		InputTokens:  1200,
		OutputTokens: 300,
		CostUSD:      0.045,
		At:           now.UnixMilli(),
	})

	// First refresh triggers background probe.
	s.refreshOpenRouter()
	s.probes.Wait()
	if fake.calls != 1 {
		t.Fatalf("expected 1 probe call, got %d", fake.calls)
	}

	// Second refresh within 10 minutes is debounced (no additional probe).
	now = now.Add(9 * time.Minute)
	s.refreshOpenRouter()
	s.probes.Wait()
	if fake.calls != 1 {
		t.Fatalf("expected debounce to hold at 1 probe call, got %d", fake.calls)
	}

	// After 10 minutes, refresh probes again.
	now = now.Add(2 * time.Minute)
	s.refreshOpenRouter()
	s.probes.Wait()
	if fake.calls != 2 {
		t.Fatalf("expected probe after 10m debounce, got %d", fake.calls)
	}

	// Serve /rail/usage and verify additive hybrid JSON shape.
	d := &Daemon{dir: dir, planUsage: s, harnesses: testHarnessTraits(), index: NewIndex(IndexOptions{Dir: dir}), snapshots: &SnapshotCache{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rail/usage", nil)
	d.handlePlanUsage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Entries []planUsageEntry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Entries) != 1 || payload.Entries[0].Harness != "openrouter" {
		t.Fatalf("unexpected entries: %+v", payload.Entries)
	}
	e := payload.Entries[0]
	if e.AccountLabel != "sk-or-***9999" || e.OpenRouter == nil {
		t.Fatalf("unexpected openrouter entry: %+v", e)
	}
	if e.OpenRouter.Balance == nil || *e.OpenRouter.Balance != 77.5 {
		t.Fatalf("expected balance 77.5, got %+v", e.OpenRouter.Balance)
	}
	if e.OpenRouter.UsageDaily == nil || *e.OpenRouter.UsageDaily != 1.2 || len(e.OpenRouter.Models) != 1 || e.OpenRouter.Models[0].Model != "anthropic/claude-sonnet-4" {
		t.Fatalf("unexpected hybrid view: %+v", e.OpenRouter)
	}
}

func TestPlanUsageOpenRouterCreditsRestrictedKeepsKeyMetrics(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	s := newPlanUsageStoreBase(dir, dir, func(string) string { return "" })
	defer s.close(context.Background())
	s.now = func() time.Time { return now }

	s.credentials = &fakeOpenRouterStore{
		configured: true,
		masked:     "sk-or-***1111",
		gen:        1,
		snap: wscred.OpenRouterUsageSnapshot{
			CreditsRestricted: true,
			CreditsStatus:     http.StatusForbidden,
			KeyStatus:         http.StatusOK,
			Key: &wscred.OpenRouterKeyLimits{
				UsageDaily:   ptrFloat(0.75),
				UsageMonthly: ptrFloat(9.25),
			},
			AsOf: now.UnixMilli(),
		},
	}
	s.refreshOpenRouter()
	s.probes.Wait()

	entries := s.entries()
	if len(entries) != 1 || entries[0].Harness != "openrouter" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	e := entries[0]
	if e.Status != "" {
		t.Fatalf("credits 403 must not mark card errored/rejected: %q", e.Status)
	}
	if !e.OpenRouter.CreditsRestricted || e.OpenRouter.Balance != nil || e.OpenRouter.TotalCredits != nil {
		t.Fatalf("expected restricted credits without balance: %+v", e.OpenRouter)
	}
	if e.OpenRouter.UsageDaily == nil || *e.OpenRouter.UsageDaily != 0.75 {
		t.Fatalf("expected key daily spend: %+v", e.OpenRouter)
	}
	raw, _ := json.Marshal(e)
	if strings.Contains(string(raw), "totalCredits") || strings.Contains(string(raw), "balance") {
		t.Fatalf("unavailable credit fields must be omitted: %s", raw)
	}
}
