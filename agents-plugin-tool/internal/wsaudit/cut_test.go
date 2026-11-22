package wsaudit

import (
	"testing"
	"time"
)

func makeSeqEvent(id string, seq uint64, kind Kind, modelSeen bool, meta map[string]any) Envelope {
	return Envelope{
		V:         SchemaVersion,
		ID:        id,
		Seq:       seq,
		Session:   "s1",
		At:        time.Date(2026, 11, 22, 8, 0, int(seq), 0, time.UTC),
		Harness:   "claude",
		Kind:      kind,
		ModelSeen: modelSeen,
		Meta:      meta,
	}
}

func TestApplyModelSeenCutFallbackToConversationStart(t *testing.T) {
	in := []Envelope{
		makeSeqEvent("e1", 1, KindThinking, false, nil),
		makeSeqEvent("e2", 2, KindToolCall, true, nil),
		makeSeqEvent("e3", 3, KindToolResult, false, nil),
	}

	got, cut := ApplyModelSeenCut(in)
	if cut.HasCompaction {
		t.Fatalf("cut.HasCompaction = true, want false")
	}
	if cut.Index != 0 || cut.EventID != "" || cut.Seq != 0 || cut.Estimated {
		t.Fatalf("cut = %+v, want conversation-start fallback at index 0", cut)
	}
	for i, ev := range got {
		if !ev.ModelSeen {
			t.Errorf("got[%d].ModelSeen = false, want true", i)
		}
	}
	// Pure function: input must not be mutated.
	if in[0].ModelSeen != false || in[2].ModelSeen != false {
		t.Fatalf("input slice was mutated: %+v", in)
	}
}

func TestApplyModelSeenCutMarksEarlierFalseAndKeepsCutAndLaterTrue(t *testing.T) {
	in := []Envelope{
		makeSeqEvent("e1", 1, KindToolCall, true, nil),
		makeSeqEvent("e2", 2, KindToolResult, true, nil),
		makeSeqEvent("e3", 3, KindCompaction, false, nil),
		makeSeqEvent("e4", 4, KindThinking, false, nil),
		makeSeqEvent("e5", 5, KindToolCall, true, nil),
	}

	got, cut := ApplyModelSeenCut(in)
	if !cut.HasCompaction || cut.Index != 2 || cut.EventID != "e3" || cut.Seq != 3 || cut.Estimated {
		t.Fatalf("cut = %+v, want index=2 id=e3 seq=3 hasCompaction=true estimated=false", cut)
	}

	wantSeen := []bool{false, false, true, true, true}
	for i, want := range wantSeen {
		if got[i].ModelSeen != want {
			t.Errorf("got[%d] (%s).ModelSeen = %v, want %v", i, got[i].ID, got[i].ModelSeen, want)
		}
	}
	// Input slice remains unchanged.
	if in[0].ModelSeen != true || in[2].ModelSeen != false {
		t.Fatalf("input slice was mutated")
	}
}

func TestApplyModelSeenCutMultipleCompactionsUsesLatestAndTracksEstimated(t *testing.T) {
	in := []Envelope{
		makeSeqEvent("e1", 1, KindToolCall, true, nil),
		makeSeqEvent("e2", 2, KindCompaction, true, nil),
		makeSeqEvent("e3", 3, KindToolResult, true, nil),
		makeSeqEvent("e4", 4, KindCompaction, true, map[string]any{MetaEstimated: true}),
		makeSeqEvent("e5", 5, KindHook, false, nil),
	}

	got, cut := ApplyModelSeenCut(in)
	if !cut.HasCompaction || cut.Index != 3 || cut.EventID != "e4" || cut.Seq != 4 || !cut.Estimated {
		t.Fatalf("cut = %+v, want index=3 id=e4 seq=4 estimated=true", cut)
	}

	wantSeen := []bool{false, false, false, true, true}
	for i, want := range wantSeen {
		if got[i].ModelSeen != want {
			t.Errorf("got[%d] (%s).ModelSeen = %v, want %v", i, got[i].ID, got[i].ModelSeen, want)
		}
	}
}

func TestApplyModelSeenCutBoundaries(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, cut := ApplyModelSeenCut(nil)
		if got != nil {
			t.Fatalf("got = %#v, want nil", got)
		}
		if cut.Index != -1 || cut.HasCompaction || cut.EventID != "" {
			t.Fatalf("cut = %+v, want Index=-1", cut)
		}
	})

	t.Run("compaction_at_start", func(t *testing.T) {
		in := []Envelope{
			makeSeqEvent("c0", 10, KindCompaction, false, nil),
			makeSeqEvent("e1", 11, KindToolCall, false, nil),
		}
		got, cut := ApplyModelSeenCut(in)
		if !cut.HasCompaction || cut.Index != 0 || cut.EventID != "c0" {
			t.Fatalf("cut = %+v, want index=0 id=c0", cut)
		}
		if !got[0].ModelSeen || !got[1].ModelSeen {
			t.Fatalf("expected all events at/after start compaction to be model_seen=true: %+v", got)
		}
	})

	t.Run("compaction_at_end", func(t *testing.T) {
		in := []Envelope{
			makeSeqEvent("e1", 1, KindToolCall, true, nil),
			makeSeqEvent("e2", 2, KindToolResult, true, nil),
			makeSeqEvent("c_end", 3, KindCompaction, false, nil),
		}
		got, cut := ApplyModelSeenCut(in)
		if !cut.HasCompaction || cut.Index != 2 || cut.EventID != "c_end" {
			t.Fatalf("cut = %+v, want index=2 id=c_end", cut)
		}
		if got[0].ModelSeen || got[1].ModelSeen || !got[2].ModelSeen {
			t.Fatalf("got ModelSeen = [%v %v %v], want [false false true]", got[0].ModelSeen, got[1].ModelSeen, got[2].ModelSeen)
		}
	})
}
