package wsmeta_test

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wsmeta"
)

func TestWerkModelUsageRefusesBeforeImport(t *testing.T) {
	ctx := context.Background()
	s, err := wsmeta.Open(filepath.Join(t.TempDir(), "dashboard"), wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	err = s.AddWerkModelUsageDeltas(ctx, []wsmeta.WerkModelUsage{{
		Provider: "openrouter", Model: "anthropic/claude-sonnet-4", Requests: 1, InputTokens: 100,
	}})
	if !errors.Is(err, wsmeta.ErrNotImported) {
		t.Fatalf("AddWerkModelUsageDeltas before import = %v, want ErrNotImported", err)
	}
}

func TestWerkModelUsageDeltasPersistReloadAndKeepAccountSeparation(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "dashboard")
	s, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cutover(ctx, wsmeta.CutoverOptions{}); err != nil {
		t.Fatal(err)
	}
	if wsmeta.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1 (T0 additive table)", wsmeta.SchemaVersion)
	}

	// First batch: OpenRouter + OpenAI account A + malformed rows that must be skipped/clamped.
	if err := s.AddWerkModelUsageDeltas(ctx, []wsmeta.WerkModelUsage{
		{
			Provider: "openrouter", AccountKey: "", Model: "anthropic/claude-sonnet-4",
			Requests: 2, InputTokens: 1000, OutputTokens: 250, CachedTokens: 400, CacheWriteTokens: 100, ReasoningTokens: 30,
			CostUSD: 0.0125, UpdatedAt: 1000,
		},
		{
			Provider: "openai", AccountKey: "acct-a", Model: "gpt-5.4",
			Requests: 1, InputTokens: 500, OutputTokens: 100, CostUSD: 0, UpdatedAt: 1100,
		},
		{
			Provider: "openai", AccountKey: "acct-b", Model: "gpt-5.4",
			Requests: 1, InputTokens: 800, OutputTokens: 200, CostUSD: math.NaN(), UpdatedAt: 1200,
		},
		{Provider: "", Model: "ignored"},
		{Provider: "openrouter", Model: ""},
	}); err != nil {
		t.Fatal(err)
	}

	// Second batch: accumulate into OpenRouter and OpenAI account A, keeping account B untouched.
	if err := s.AddWerkModelUsageDeltas(ctx, []wsmeta.WerkModelUsage{
		{
			Provider: "openrouter", AccountKey: "", Model: "anthropic/claude-sonnet-4",
			Requests: 1, InputTokens: 500, OutputTokens: 50, CachedTokens: 100,
			CostUSD: 0.0075, UpdatedAt: 2000,
		},
		{
			Provider: "openai", AccountKey: "acct-a", Model: "gpt-5.4",
			Requests: 2, InputTokens: -50, OutputTokens: 40, CostUSD: -1.0, UpdatedAt: 900, // older UpdatedAt must not regress
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen and verify persisted snapshot.
	reopened, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	snap, err := reopened.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.WerkModelUsage) != 3 {
		t.Fatalf("len(WerkModelUsage) = %d, want 3: %+v", len(snap.WerkModelUsage), snap.WerkModelUsage)
	}

	or := snap.WerkModelUsage[wsmeta.WerkModelUsageKey{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"}]
	if or.Requests != 3 || or.InputTokens != 1500 || or.OutputTokens != 300 || or.CachedTokens != 500 || or.CacheWriteTokens != 100 || or.ReasoningTokens != 30 || math.Abs(or.CostUSD-0.02) > 1e-9 || or.UpdatedAt != 2000 {
		t.Fatalf("unexpected openrouter row: %+v", or)
	}
	oa := snap.WerkModelUsage[wsmeta.WerkModelUsageKey{Provider: "openai", AccountKey: "acct-a", Model: "gpt-5.4"}]
	if oa.Requests != 3 || oa.InputTokens != 500 || oa.OutputTokens != 140 || oa.CostUSD != 0 || oa.UpdatedAt != 1100 {
		t.Fatalf("unexpected openai acct-a row: %+v", oa)
	}
	ob := snap.WerkModelUsage[wsmeta.WerkModelUsageKey{Provider: "openai", AccountKey: "acct-b", Model: "gpt-5.4"}]
	if ob.Requests != 1 || ob.InputTokens != 800 || ob.OutputTokens != 200 || ob.CostUSD != 0 || ob.UpdatedAt != 1200 {
		t.Fatalf("unexpected openai acct-b row: %+v", ob)
	}
}
