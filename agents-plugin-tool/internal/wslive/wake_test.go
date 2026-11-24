package wslive

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestWakeKindsAndTurn(t *testing.T) {
	if !slices.Contains(OptionalKinds, KindWake) {
		t.Fatal("wake must be negotiated")
	}
	for _, cause := range []string{"subagent_report", "subagent_done", "subagent_failed", "background_exit", "monitor", "mail", "message", "scheduled"} {
		st := NewState("secret-key")
		caption := "caption secret-key\nnext"
		res, err := st.Apply(Event{Kind: KindWake, Cause: cause, Source: "secret-key", Caption: &caption, Text: strings.Repeat("x", TextBytes+1) + " secret-key"}, 1)
		if err != nil || len(res.Ring) != 1 {
			t.Fatalf("%s: %+v %v", cause, res, err)
		}
		e := res.Ring[0]
		if e.Kind != RingWake || e.Cause != cause || !e.Truncated || strings.Contains(e.Caption+e.Source+e.Text, "secret-key") {
			t.Fatalf("bad wake: %+v", e)
		}
		st.Apply(Event{Kind: KindTurnStart}, 2)
		res, err = st.Apply(Event{Kind: KindTurnEnd}, 3)
		if err != nil || len(res.Ring) != 1 || !res.Ring[0].WakeTurn {
			t.Fatalf("wake turn: %+v %v", res, err)
		}
		st.Apply(Event{Kind: KindTurnStart}, 4)
		res, _ = st.Apply(Event{Kind: KindTurnEnd}, 5)
		if res.Ring[0].WakeTurn {
			t.Fatal("wake reused")
		}
	}
}

func TestWakeAfterOriginlessStartBeforeWork(t *testing.T) {
	st := NewState("key")
	st.Apply(Event{Kind: KindTurnStart}, 1)
	st.Apply(Event{Kind: KindWake, Cause: "subagent_report"}, 2)
	res, _ := st.Apply(Event{Kind: KindTurnEnd}, 3)
	if !res.Ring[0].WakeTurn {
		t.Fatal("Pi input after agent_start did not mark its turn")
	}
}

func TestWakeDoesNotClaimPersonOrRunningTurn(t *testing.T) {
	for _, person := range []bool{false, true} {
		st := NewState("key")
		if !person {
			st.Apply(Event{Kind: KindTurnStart, Origin: OriginTerminal}, 1)
		}
		st.Apply(Event{Kind: KindWake, Cause: "monitor"}, 2)
		if person {
			st.Apply(Event{Kind: KindTurnStart, Origin: OriginTerminal}, 3)
		}
		res, _ := st.Apply(Event{Kind: KindTurnEnd}, 4)
		if res.Ring[0].WakeTurn {
			t.Fatal("claimed a turn wake did not start")
		}
	}
	st := NewState("key")
	if _, err := st.Apply(Event{Kind: KindWake, Cause: "unknown"}, 1); err == nil {
		t.Fatal("unknown cause accepted")
	}
	st.Apply(Event{Kind: KindWake, Cause: "scheduled"}, 1)
	st.Apply(Event{Kind: KindReset}, 2)
	st.Apply(Event{Kind: KindTurnStart}, 3)
	res, _ := st.Apply(Event{Kind: KindTurnEnd}, 4)
	if res.Ring[0].WakeTurn {
		t.Fatal("reset kept wake")
	}
}

func TestRingEntryBodyBytesRoundTripAndWakeMailTruncation(t *testing.T) {
	// RingEntry.BodyBytes serializes with omitempty and decodes cleanly when absent (T0).
	withBytes := RingEntry{Kind: RingMail, Direction: MailRecv, Peer: "bob@machine", Title: "hi", BodyBytes: 512}
	raw, err := json.Marshal(withBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"bodyBytes":512`) {
		t.Fatalf("expected bodyBytes in JSON, got %s", raw)
	}
	var decoded RingEntry
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.BodyBytes != 512 {
		t.Fatalf("decoded BodyBytes = %d, want 512", decoded.BodyBytes)
	}

	zeroBytes := RingEntry{Kind: RingMail, Direction: MailSent, Peer: "bob@machine", Title: "hi"}
	rawZero, err := json.Marshal(zeroBytes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawZero), "bodyBytes") {
		t.Fatalf("zero BodyBytes must be omitted, got %s", rawZero)
	}
	var legacy RingEntry
	if err := json.Unmarshal([]byte(`{"kind":"mail","direction":"recv","peer":"bob@machine","title":"old"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.BodyBytes != 0 {
		t.Fatalf("legacy record BodyBytes = %d, want 0", legacy.BodyBytes)
	}

	// Mail wakes record the full scrubbed payload size even when capText truncates Text.
	st := NewState("secret-key")
	longMail := strings.Repeat("a", TextBytes+256)
	res, err := st.Apply(Event{Kind: KindWake, Cause: "mail", Text: longMail}, 1)
	if err != nil || len(res.Ring) != 1 {
		t.Fatalf("mail wake: %+v %v", res, err)
	}
	if !res.Ring[0].Truncated {
		t.Fatal("expected long mail wake to be truncated")
	}
	if res.Ring[0].BodyBytes != len(longMail) {
		t.Fatalf("mail wake BodyBytes = %d, want %d", res.Ring[0].BodyBytes, len(longMail))
	}

	// Non-mail wakes leave BodyBytes zero.
	res2, err := st.Apply(Event{Kind: KindWake, Cause: "monitor", Text: "ping"}, 2)
	if err != nil || len(res2.Ring) != 1 {
		t.Fatalf("monitor wake: %+v %v", res2, err)
	}
	if res2.Ring[0].BodyBytes != 0 {
		t.Fatalf("monitor wake BodyBytes = %d, want 0", res2.Ring[0].BodyBytes)
	}
}
