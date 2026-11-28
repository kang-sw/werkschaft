package wsagent

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func TestWerkLoopReportsUsageWithTurnPinnedOpenAIAccount(t *testing.T) {
	var active atomic.Pointer[string]
	acctA, acctB := "client-a", "client-b"
	active.Store(&acctA)

	var mu sync.Mutex
	var deltas []wsdashboard.WerkUsageDelta

	cost1, cost2 := 0.01, 0.02
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(_ context.Context, _ werk.Request) (*werk.Response, error) {
			// Simulate switching the active account mid-turn: the in-flight turn
			// must stay attributed to client-a.
			active.Store(&acctB)
			return &werk.Response{
				ID:     "r1",
				Model:  "gpt-5.4",
				Output: []json.RawMessage{json.RawMessage(`{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"turn 1"}]}`)},
				Usage:  werk.Usage{InputTokens: 300, OutputTokens: 50, CostUSD: &cost1},
			}, nil
		},
		func(_ context.Context, _ werk.Request) (*werk.Response, error) {
			return &werk.Response{
				ID:     "r2",
				Model:  "gpt-5.4",
				Output: []json.RawMessage{json.RawMessage(`{"type":"message","id":"m2","role":"assistant","content":[{"type":"output_text","text":"turn 2"}]}`)},
				Usage:  werk.Usage{InputTokens: 400, OutputTokens: 70, CostUSD: &cost2},
			}, nil
		},
	}}

	spec := StartSpec{
		Ephemeral:       true,
		Provider:        string(wscred.ProviderOpenAI),
		Model:           "gpt-5.4",
		PermissionLevel: PermissionReadOnly,
	}
	loop, err := startWerkLoopWithHooks(
		context.Background(),
		spec,
		model,
		func() string { return *active.Load() },
		func(d wsdashboard.WerkUsageDelta) {
			mu.Lock()
			deltas = append(deltas, d)
			mu.Unlock()
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loop.Close() })

	if err := loop.Send("first"); err != nil {
		t.Fatal(err)
	}
	nextWerkLoopEvent(t, loop, func(e Event) bool { return e.TurnEnd })
	if err := loop.Send("second"); err != nil {
		t.Fatal(err)
	}
	nextWerkLoopEvent(t, loop, func(e Event) bool { return e.TurnEnd })

	mu.Lock()
	defer mu.Unlock()
	if len(deltas) != 2 {
		t.Fatalf("deltas = %d, want 2: %+v", len(deltas), deltas)
	}
	if deltas[0].Provider != "openai" || deltas[0].AccountKey != "client-a" || deltas[0].Model != "gpt-5.4" || deltas[0].InputTokens != 300 {
		t.Fatalf("turn 1 delta = %+v", deltas[0])
	}
	if deltas[1].Provider != "openai" || deltas[1].AccountKey != "client-b" || deltas[1].Model != "gpt-5.4" || deltas[1].InputTokens != 400 {
		t.Fatalf("turn 2 delta = %+v", deltas[1])
	}
}

func TestWerkMapperCompactionUsageAccumulatesCostWithoutClearingContext(t *testing.T) {
	m := newWerkMapper("s1")
	c1, c2 := 0.25, 0.05
	evs := m.mapEvent(werk.Line{T: werk.LineUsage, Turn: 1, Model: "anthropic/claude-sonnet-4", InputTokens: 1000, OutputTokens: 100, Context: 1100, Window: 10000, CostUSD: &c1})
	if len(evs) != 1 || evs[0].Live == nil || evs[0].Live.Cost == nil || evs[0].Live.Cost.USD != 0.25 {
		t.Fatalf("unexpected regular usage events: %+v", evs)
	}
	// Compaction usage must not emit a zero-context KindStatus event,
	// yet its cost must accumulate for subsequent status lines.
	if compEvs := m.mapEvent(werk.Line{T: werk.LineUsage, Compaction: true, Model: "anthropic/claude-sonnet-4", InputTokens: 500, OutputTokens: 80, CostUSD: &c2}); len(compEvs) != 0 {
		t.Fatalf("compaction usage emitted unexpected live events: %+v", compEvs)
	}
	c3 := 0.10
	evs2 := m.mapEvent(werk.Line{T: werk.LineUsage, Turn: 2, Model: "anthropic/claude-sonnet-4", InputTokens: 200, OutputTokens: 40, Context: 240, Window: 10000, CostUSD: &c3})
	if len(evs2) != 1 || evs2[0].Live == nil || evs2[0].Live.Cost == nil || evs2[0].Live.Cost.USD < 0.399 || evs2[0].Live.Cost.USD > 0.401 {
		t.Fatalf("expected accumulated cost ~0.40 after compaction, got %+v", evs2)
	}
}
