package wslive

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func statusTokens(n int64) Event {
	return Event{Kind: KindStatus, Context: &UsageContext{Tokens: &n, Window: 200_000}}
}

func TestTurnAddedTokensNormalAndIncrementalSnapshot(t *testing.T) {
	st := NewState("sess-tok-normal")
	if _, err := st.Apply(statusTokens(10_000), 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(Event{Kind: KindTurnStart, Origin: OriginTerminal}, 2000); err != nil {
		t.Fatal(err)
	}
	if snap := st.Snapshot(2000); snap.Lead.TurnAddedTokens != 0 {
		t.Fatalf("at turn start TurnAddedTokens = %d, want 0", snap.Lead.TurnAddedTokens)
	}
	// Context advances mid-turn.
	res, err := st.Apply(statusTokens(11_400), 3000)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("expected Changed when TurnAddedTokens advanced")
	}
	if snap := st.Snapshot(3000); snap.Lead.TurnAddedTokens != 1_400 {
		t.Fatalf("mid-turn TurnAddedTokens = %d, want 1400", snap.Lead.TurnAddedTokens)
	}
	// Context advances again before turn end.
	if _, err := st.Apply(statusTokens(12_800), 4000); err != nil {
		t.Fatal(err)
	}
	if snap := st.Snapshot(4000); snap.Lead.TurnAddedTokens != 2_800 {
		t.Fatalf("pre-end TurnAddedTokens = %d, want 2800", snap.Lead.TurnAddedTokens)
	}
	endRes, err := st.Apply(Event{Kind: KindTurnEnd}, 5000)
	if err != nil {
		t.Fatal(err)
	}
	var turn *RingEntry
	for i := range endRes.Ring {
		if endRes.Ring[i].Kind == RingTurn {
			turn = &endRes.Ring[i]
		}
	}
	if turn == nil {
		t.Fatalf("expected RingTurn entry in %+v", endRes.Ring)
	}
	if turn.AddedTokens != 2_800 {
		t.Fatalf("RingTurn.AddedTokens = %d, want 2800", turn.AddedTokens)
	}
	// Snapshot running field clears on RingTurn write.
	if snap := st.Snapshot(5000); snap.Lead.TurnAddedTokens != 0 {
		t.Fatalf("after turn end TurnAddedTokens = %d, want 0", snap.Lead.TurnAddedTokens)
	}
}

func TestTurnAddedTokensCompactionClampAndReset(t *testing.T) {
	st := NewState("sess-tok-clamp")
	if _, err := st.Apply(statusTokens(120_000), 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(Event{Kind: KindTurnStart}, 2000); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(statusTokens(125_000), 2500); err != nil {
		t.Fatal(err)
	}
	if snap := st.Snapshot(2500); snap.Lead.TurnAddedTokens != 5_000 {
		t.Fatalf("before compaction TurnAddedTokens = %d, want 5000", snap.Lead.TurnAddedTokens)
	}
	// Compaction drops context below turn-start baseline -> clamp to 0.
	if _, err := st.Apply(Event{Kind: KindCompact, Phase: CompactEnd, Trigger: TriggerAuto}, 3000); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(statusTokens(32_000), 3500); err != nil {
		t.Fatal(err)
	}
	if snap := st.Snapshot(3500); snap.Lead.TurnAddedTokens != 0 {
		t.Fatalf("after compaction drop TurnAddedTokens = %d, want 0", snap.Lead.TurnAddedTokens)
	}
	endRes, err := st.Apply(Event{Kind: KindTurnEnd}, 4000)
	if err != nil {
		t.Fatal(err)
	}
	var turn *RingEntry
	for i := range endRes.Ring {
		if endRes.Ring[i].Kind == RingTurn {
			turn = &endRes.Ring[i]
		}
	}
	if turn == nil || turn.AddedTokens != 0 {
		t.Fatalf("expected clamped AddedTokens=0, got %+v", turn)
	}

	// KindReset mid-turn clears running TurnAddedTokens.
	if _, err := st.Apply(Event{Kind: KindTurnStart}, 5000); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(statusTokens(36_000), 5500); err != nil {
		t.Fatal(err)
	}
	if snap := st.Snapshot(5500); snap.Lead.TurnAddedTokens != 4_000 {
		t.Fatalf("pre-reset TurnAddedTokens = %d, want 4000", snap.Lead.TurnAddedTokens)
	}
	if _, err := st.Apply(Event{Kind: KindReset}, 6000); err != nil {
		t.Fatal(err)
	}
	if snap := st.Snapshot(6000); snap.Lead.TurnAddedTokens != 0 {
		t.Fatalf("post-reset TurnAddedTokens = %d, want 0", snap.Lead.TurnAddedTokens)
	}
}

func TestTurnAddedTokensFirstTurnBaselineAndOmitEmptyT0(t *testing.T) {
	st := NewState("sess-tok-first")
	// No prior status before first turn: first status sets turn baseline rather
	// than counting the existing/system prompt as added tokens.
	if _, err := st.Apply(Event{Kind: KindTurnStart}, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(statusTokens(20_000), 1500); err != nil {
		t.Fatal(err)
	}
	if snap := st.Snapshot(1500); snap.Lead.TurnAddedTokens != 0 {
		t.Fatalf("first status baseline TurnAddedTokens = %d, want 0", snap.Lead.TurnAddedTokens)
	}
	// Second status in the same turn computes delta from that baseline.
	if _, err := st.Apply(statusTokens(21_750), 2000); err != nil {
		t.Fatal(err)
	}
	if snap := st.Snapshot(2000); snap.Lead.TurnAddedTokens != 1_750 {
		t.Fatalf("second status delta TurnAddedTokens = %d, want 1750", snap.Lead.TurnAddedTokens)
	}
	endRes, err := st.Apply(Event{Kind: KindTurnEnd}, 2500)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(endRes.Ring[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"added_tokens":1750`) {
		t.Fatalf("expected added_tokens in JSON: %s", raw)
	}

	// Zero-delta turn omits added_tokens and snapshot omits turnAddedTokens (T0).
	zeroTurn := RingEntry{Kind: RingTurn, Done: 2, DurationMs: 500}
	zeroRaw, err := json.Marshal(zeroTurn)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(zeroRaw), "added_tokens") {
		t.Fatalf("zero AddedTokens should be omitted: %s", zeroRaw)
	}
	snapRaw, err := json.Marshal(st.Snapshot(3000))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(snapRaw), "turnAddedTokens") {
		t.Fatalf("zero TurnAddedTokens should be omitted: %s", snapRaw)
	}
	// Legacy JSON without added_tokens unmarshals cleanly with AddedTokens == 0.
	var decoded RingEntry
	if err := json.Unmarshal([]byte(`{"kind":"turn","done":3,"durationMs":900}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AddedTokens != 0 || decoded.Done != 3 {
		t.Fatalf("unexpected decoded legacy turn: %+v", decoded)
	}
}

func TestTurnRingOrderToolsBeforeTurn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ring")
	stepRingClock(t)
	st := NewState("sess-tok-order")
	if _, err := st.Apply(statusTokens(5_000), 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(Event{Kind: KindTurnStart, Origin: OriginTerminal}, 2000); err != nil {
		t.Fatal(err)
	}
	// Record a finished tool batch, then turn end.
	endAt := int64(2500)
	rawBatch, _ := json.Marshal([]map[string]any{{
		"epoch": "e1", "seq": 1, "toolNames": []string{"Read"}, "callCount": 1,
		"startedAt": 2100, "endedAt": endAt,
	}})
	if _, err := st.Apply(Event{Kind: KindAgent, Batches: []json.RawMessage{rawBatch[1 : len(rawBatch)-1]}}, 2600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(statusTokens(6_200), 2800); err != nil {
		t.Fatal(err)
	}
	endRes, err := st.Apply(Event{Kind: KindTurnEnd}, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendRing(dir, endRes.Ring); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadRing(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Kind != RingTools || recs[1].Kind != RingTurn {
		t.Fatalf("ring order = %+v, want [tools, turn]", recs)
	}
	if !(recs[0].Name < recs[1].Name) || recs[1].AddedTokens != 1_200 {
		t.Fatalf("order/tokens mismatch: tools=%s turn=%s added=%d", recs[0].Name, recs[1].Name, recs[1].AddedTokens)
	}
}
