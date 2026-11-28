package wsdashboard

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsmeta"
)

func openTestMetaForUsage(t *testing.T) (*wsmeta.Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dashboard")
	meta, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := meta.Cutover(context.Background(), wsmeta.CutoverOptions{}); err != nil {
		_ = meta.Close()
		t.Fatal(err)
	}
	return meta, dir
}

func TestPlanUsageWerkBatchesDebouncedFlushAndReload(t *testing.T) {
	ctx := context.Background()
	meta, dir := openTestMetaForUsage(t)
	snap, err := meta.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}

	s := newPlanUsageStoreFromMeta(dir, t.TempDir(), func(string) string { return "" }, meta, snap)
	// Long debounce verifies writes do not hit SQLite per LineUsage event.
	s.werkFlushDebounce = time.Hour

	s.recordWerkUsage(WerkUsageDelta{
		Provider: "openrouter", Model: "anthropic/claude-sonnet-4",
		InputTokens: 1000, OutputTokens: 200, CachedTokens: 300, CostUSD: 0.01, At: 1000,
	})
	s.recordWerkUsage(WerkUsageDelta{
		Provider: "openrouter", Model: "anthropic/claude-sonnet-4",
		InputTokens: 500, OutputTokens: 100, CostUSD: 0.005, At: 2000,
	})
	s.recordWerkUsage(WerkUsageDelta{
		Provider: "openai", AccountKey: "client-a", Model: "gpt-5.4",
		InputTokens: 2000, OutputTokens: 400, At: 2100,
	})
	s.recordWerkUsage(WerkUsageDelta{
		Provider: "openai", AccountKey: "client-b", Model: "gpt-5.4",
		InputTokens: 700, OutputTokens: 150, At: 2200,
	})

	// RAM view reflects deltas immediately, before any SQLite flush.
	s.mu.Lock()
	orRows, orIn, orOut, orCost, _ := s.werkModelRowsLocked("openrouter", false, "", false)
	oaRows, oaIn, oaOut, _, _ := s.werkModelRowsLocked("openai", true, "client-a", true)
	obRows, obIn, obOut, _, _ := s.werkModelRowsLocked("openai", true, "client-b", true)
	s.mu.Unlock()
	if len(orRows) != 1 || orIn != 1500 || orOut != 300 || math.Abs(orCost-0.015) > 1e-9 {
		t.Fatalf("unexpected openrouter RAM rows: %+v (%d/%d/$%v)", orRows, orIn, orOut, orCost)
	}
	if len(oaRows) != 1 || oaIn != 2000 || oaOut != 400 {
		t.Fatalf("unexpected openai client-a RAM rows: %+v (%d/%d)", oaRows, oaIn, oaOut)
	}
	if len(obRows) != 1 || obIn != 700 || obOut != 150 {
		t.Fatalf("unexpected openai client-b RAM rows: %+v (%d/%d)", obRows, obIn, obOut)
	}

	// SQLite must still have zero rows before debounce or close flushes.
	preFlush, err := meta.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(preFlush.WerkModelUsage) != 0 {
		t.Fatalf("expected 0 SQLite rows before debounce flush, got %+v", preFlush.WerkModelUsage)
	}

	// Closing flushes accumulated pending deltas in a single batch.
	s.close(ctx)
	if err := meta.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen and verify persisted deltas reload cleanly into a fresh planUsageStore.
	reopened, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	snap2, err := reopened.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s2 := newPlanUsageStoreFromMeta(dir, t.TempDir(), func(string) string { return "" }, reopened, snap2)
	defer s2.close(ctx)

	s2.mu.Lock()
	orRows2, orIn2, orOut2, orCost2, _ := s2.werkModelRowsLocked("openrouter", false, "", false)
	oaRows2, oaIn2, oaOut2, _, _ := s2.werkModelRowsLocked("openai", true, "client-a", true)
	obRows2, obIn2, obOut2, _, _ := s2.werkModelRowsLocked("openai", true, "client-b", true)
	s2.mu.Unlock()
	if len(orRows2) != 1 || orIn2 != 1500 || orOut2 != 300 || math.Abs(orCost2-0.015) > 1e-9 {
		t.Fatalf("reloaded openrouter rows mismatch: %+v", orRows2)
	}
	if len(oaRows2) != 1 || oaIn2 != 2000 || oaOut2 != 400 {
		t.Fatalf("reloaded openai client-a rows mismatch: %+v", oaRows2)
	}
	if len(obRows2) != 1 || obIn2 != 700 || obOut2 != 150 {
		t.Fatalf("reloaded openai client-b rows mismatch: %+v", obRows2)
	}
}

func TestPlanUsageWerkDebounceTimerFlushesAutomatically(t *testing.T) {
	ctx := context.Background()
	meta, dir := openTestMetaForUsage(t)
	defer meta.Close()
	snap, err := meta.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}

	s := newPlanUsageStoreFromMeta(dir, t.TempDir(), func(string) string { return "" }, meta, snap)
	s.werkFlushDebounce = 25 * time.Millisecond
	defer s.close(ctx)

	s.recordWerkUsage(WerkUsageDelta{
		Provider: "openrouter", Model: "openai/gpt-5",
		InputTokens: 400, OutputTokens: 80, CostUSD: 0.002, At: 5000,
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := meta.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if row, ok := loaded.WerkModelUsage[wsmeta.WerkModelUsageKey{Provider: "openrouter", Model: "openai/gpt-5"}]; ok {
			if row.InputTokens == 400 && row.OutputTokens == 80 {
				return
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("debounced werk usage timer did not flush to wsmeta")
}
